package dlna

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type eventService string

const (
	serviceAVTransport       eventService = "AVTransport"
	serviceRenderingControl  eventService = "RenderingControl"
	serviceConnectionManager eventService = "ConnectionManager"

	eventMinTimeout = 300 * time.Second
	eventMaxTimeout = 1800 * time.Second

	defaultMaxSubscriptionsPerService = 16
	defaultMaxSubscriptionsPerRemote  = 4
	defaultNotifyTimeout              = 2 * time.Second
	defaultNotifyResponseLimit        = int64(1024)
	defaultMaxNotifyFailures          = 3
	defaultMaxPendingNotifyDeliveries = 16
)

var (
	errCallbackMalformed = errors.New("dlna: callback header malformed")
	errCallbackPolicy    = errors.New("dlna: callback URL rejected by address policy")
)

type eventManagerConfig struct {
	now              func() time.Time
	resolver         dnsResolverFunc
	client           *http.Client
	maxPerService    int
	maxPerRemoteAddr int
}

type eventManager struct {
	mu sync.Mutex

	now      func() time.Time
	resolver dnsResolverFunc
	client   *http.Client

	maxPerService    int
	maxPerRemoteAddr int

	subscriptions map[eventService]map[string]*eventSubscription
	snapshots     map[eventService]eventProperties
	deliveries    map[string]*eventDeliveryQueue
}

type eventSubscription struct {
	SID          string
	Service      eventService
	CallbackURL  string
	RemoteAddr   string
	ExpiresAt    time.Time
	NextSeq      uint32
	FailureCount int
}

type eventDelivery struct {
	sub   *eventSubscription
	props eventProperties
	seq   uint32
}

type eventDeliveryQueue struct {
	deliveries []eventDelivery
	running    bool
}

func newEventManager(cfg eventManagerConfig) *eventManager {
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	resolver := cfg.resolver
	if resolver == nil {
		resolver = defaultDNSResolver
	}
	client := cfg.client
	if client == nil {
		client = newPolicyNotifyClient(resolver)
	}
	maxPerService := cfg.maxPerService
	if maxPerService <= 0 {
		maxPerService = defaultMaxSubscriptionsPerService
	}
	maxPerRemote := cfg.maxPerRemoteAddr
	if maxPerRemote <= 0 {
		maxPerRemote = defaultMaxSubscriptionsPerRemote
	}
	return &eventManager{
		now:              now,
		resolver:         resolver,
		client:           client,
		maxPerService:    maxPerService,
		maxPerRemoteAddr: maxPerRemote,
		subscriptions: map[eventService]map[string]*eventSubscription{
			serviceAVTransport:       {},
			serviceRenderingControl:  {},
			serviceConnectionManager: {},
		},
		snapshots:  map[eventService]eventProperties{},
		deliveries: map[string]*eventDeliveryQueue{},
	}
}

func (e *eventManager) setSnapshot(service eventService, props eventProperties) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.snapshots[service] = cloneEventProperties(props)
}

func (e *eventManager) resetSubscriptions() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for service := range e.subscriptions {
		e.subscriptions[service] = map[string]*eventSubscription{}
	}
	e.deliveries = map[string]*eventDeliveryQueue{}
}

func (e *eventManager) handleSubscribe(w http.ResponseWriter, r *http.Request, service eventService) {
	if sid := strings.TrimSpace(r.Header.Get("SID")); sid != "" {
		if !isValidSubscriptionSID(sid) {
			http.Error(w, "bad event renewal", http.StatusBadRequest)
			return
		}
		e.handleRenewal(w, r, service, sid)
		return
	}
	e.handleNewSubscription(w, r, service)
}

func (e *eventManager) handleNewSubscription(w http.ResponseWriter, r *http.Request, service eventService) {
	if strings.TrimSpace(r.Header.Get("CALLBACK")) == "" || strings.TrimSpace(r.Header.Get("NT")) != "upnp:event" {
		http.Error(w, "bad event subscription", http.StatusBadRequest)
		return
	}

	remote := remoteHost(r.RemoteAddr)
	timeout := parseEventTimeout(r.Header.Get("TIMEOUT"))
	e.mu.Lock()
	e.pruneExpiredLocked(e.now())
	if !e.hasCapacityLocked(service, remote) {
		e.mu.Unlock()
		http.Error(w, "subscription limit reached", http.StatusServiceUnavailable)
		return
	}
	e.mu.Unlock()

	callback, err := e.validateCallbackHeader(r.Context(), r.Header.Get("CALLBACK"))
	if err != nil {
		if errors.Is(err, errCallbackMalformed) {
			http.Error(w, "bad callback header", http.StatusBadRequest)
			return
		}
		http.Error(w, "callback precondition failed", http.StatusPreconditionFailed)
		return
	}
	sid, err := newSubscriptionSID()
	if err != nil {
		http.Error(w, "subscription SID unavailable", http.StatusInternalServerError)
		return
	}

	e.mu.Lock()
	e.pruneExpiredLocked(e.now())
	if !e.hasCapacityLocked(service, remote) {
		e.mu.Unlock()
		http.Error(w, "subscription limit reached", http.StatusServiceUnavailable)
		return
	}
	sub := &eventSubscription{
		SID:         sid,
		Service:     service,
		CallbackURL: callback,
		RemoteAddr:  remote,
		ExpiresAt:   e.now().Add(timeout),
		NextSeq:     1,
	}
	e.subscriptions[service][sid] = sub
	initial := cloneEventProperties(e.snapshots[service])
	key, queue, startWorker := e.enqueueNotifyLocked(sub, initial, 0)
	e.mu.Unlock()

	w.Header().Set("SID", sid)
	w.Header().Set("TIMEOUT", "Second-"+strconv.Itoa(int(timeout/time.Second)))
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
	if startWorker {
		go e.runDeliveryQueue(key, queue)
	}
}

func (e *eventManager) handleRenewal(w http.ResponseWriter, r *http.Request, service eventService, sid string) {
	if strings.TrimSpace(r.Header.Get("CALLBACK")) != "" || strings.TrimSpace(r.Header.Get("NT")) != "" {
		http.Error(w, "bad event renewal", http.StatusBadRequest)
		return
	}
	timeout := parseEventTimeout(r.Header.Get("TIMEOUT"))
	e.mu.Lock()
	e.pruneExpiredLocked(e.now())
	sub := e.subscriptions[service][sid]
	if sub == nil {
		e.mu.Unlock()
		http.Error(w, "unknown subscription", http.StatusPreconditionFailed)
		return
	}
	sub.ExpiresAt = e.now().Add(timeout)
	e.mu.Unlock()
	w.Header().Set("SID", sid)
	w.Header().Set("TIMEOUT", "Second-"+strconv.Itoa(int(timeout/time.Second)))
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

func (e *eventManager) handleUnsubscribe(w http.ResponseWriter, r *http.Request, service eventService) {
	sid := strings.TrimSpace(r.Header.Get("SID"))
	if sid == "" || strings.TrimSpace(r.Header.Get("CALLBACK")) != "" || strings.TrimSpace(r.Header.Get("NT")) != "" {
		http.Error(w, "bad event unsubscribe", http.StatusBadRequest)
		return
	}
	if !isValidSubscriptionSID(sid) {
		http.Error(w, "bad event unsubscribe", http.StatusBadRequest)
		return
	}
	e.mu.Lock()
	e.pruneExpiredLocked(e.now())
	sub := e.subscriptions[service][sid]
	if sub == nil {
		e.mu.Unlock()
		http.Error(w, "unknown subscription", http.StatusPreconditionFailed)
		return
	}
	e.deleteSubscriptionLocked(service, sid)
	e.mu.Unlock()
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

func parseEventTimeout(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "Second-") {
		return eventMaxTimeout
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(raw, "Second-"), 10, 64)
	if err != nil {
		return eventMaxTimeout
	}
	if n < int64(eventMinTimeout/time.Second) {
		return eventMinTimeout
	}
	if n > int64(eventMaxTimeout/time.Second) {
		return eventMaxTimeout
	}
	return time.Duration(n) * time.Second
}

func newSubscriptionSID() (string, error) {
	return newSubscriptionSIDFromReader(rand.Reader)
}

func newSubscriptionSIDFromReader(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("subscription SID entropy unavailable: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return fmt.Sprintf("uuid:%s-%s-%s-%s-%s", hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32]), nil
}

func isValidSubscriptionSID(sid string) bool {
	const sidLength = len("uuid:00000000-0000-0000-0000-000000000000")
	if len(sid) != sidLength || !strings.HasPrefix(sid, "uuid:") {
		return false
	}
	for i := len("uuid:"); i < len(sid); i++ {
		switch i {
		case 13, 18, 23, 28:
			if sid[i] != '-' {
				return false
			}
		default:
			if !isHexByte(sid[i]) {
				return false
			}
		}
	}
	return true
}

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'f') ||
		(b >= 'A' && b <= 'F')
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func cloneEventProperties(in eventProperties) eventProperties {
	out := make(eventProperties, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneSubscription(in *eventSubscription) *eventSubscription {
	cp := *in
	return &cp
}

func subscriptionKey(service eventService, sid string) string {
	return string(service) + "\x00" + sid
}

func (e *eventManager) deleteSubscriptionLocked(service eventService, sid string) {
	delete(e.subscriptions[service], sid)
	delete(e.deliveries, subscriptionKey(service, sid))
}

func (e *eventManager) countRemoteLocked(service eventService, remote string) int {
	n := 0
	for _, sub := range e.subscriptions[service] {
		if sub.RemoteAddr == remote {
			n++
		}
	}
	return n
}

func (e *eventManager) hasCapacityLocked(service eventService, remote string) bool {
	return len(e.subscriptions[service]) < e.maxPerService &&
		e.countRemoteLocked(service, remote) < e.maxPerRemoteAddr
}

func (e *eventManager) pruneExpiredLocked(now time.Time) {
	for service, subs := range e.subscriptions {
		for sid, sub := range subs {
			if !sub.ExpiresAt.After(now) {
				e.deleteSubscriptionLocked(service, sid)
			}
		}
	}
}

func (e *eventManager) publish(service eventService, props eventProperties) {
	props = cloneEventProperties(props)
	type workerStart struct {
		key   string
		queue *eventDeliveryQueue
	}
	var starts []workerStart

	e.mu.Lock()
	e.pruneExpiredLocked(e.now())
	if eventPropertiesEqual(e.snapshots[service], props) {
		e.mu.Unlock()
		return
	}
	e.snapshots[service] = cloneEventProperties(props)
	for _, sub := range e.subscriptions[service] {
		seq := sub.NextSeq
		sub.NextSeq = nextEventSeq(seq)
		key, queue, startWorker := e.enqueueNotifyLocked(sub, props, seq)
		if startWorker {
			starts = append(starts, workerStart{key: key, queue: queue})
		}
	}
	e.mu.Unlock()

	for _, start := range starts {
		go e.runDeliveryQueue(start.key, start.queue)
	}
}

func (e *eventManager) enqueueNotifyLocked(sub *eventSubscription, props eventProperties, seq uint32) (string, *eventDeliveryQueue, bool) {
	key := subscriptionKey(sub.Service, sub.SID)
	queue := e.deliveries[key]
	if queue == nil {
		queue = &eventDeliveryQueue{}
		e.deliveries[key] = queue
	}
	if len(queue.deliveries) >= defaultMaxPendingNotifyDeliveries {
		e.deleteSubscriptionLocked(sub.Service, sub.SID)
		return key, nil, false
	}
	queue.deliveries = append(queue.deliveries, eventDelivery{
		sub:   cloneSubscription(sub),
		props: cloneEventProperties(props),
		seq:   seq,
	})
	if queue.running {
		return key, queue, false
	}
	queue.running = true
	return key, queue, true
}

func (e *eventManager) runDeliveryQueue(key string, queue *eventDeliveryQueue) {
	for {
		e.mu.Lock()
		if e.deliveries[key] != queue {
			e.mu.Unlock()
			return
		}
		if len(queue.deliveries) == 0 {
			queue.running = false
			e.mu.Unlock()
			return
		}
		delivery := queue.deliveries[0]
		copy(queue.deliveries, queue.deliveries[1:])
		queue.deliveries[len(queue.deliveries)-1] = eventDelivery{}
		queue.deliveries = queue.deliveries[:len(queue.deliveries)-1]
		e.mu.Unlock()

		e.sendNotify(delivery.sub, delivery.props, delivery.seq)
	}
}

func (e *eventManager) sendNotify(sub *eventSubscription, props eventProperties, seq uint32) {
	if e.deliverNotify(sub, props, seq) {
		e.recordNotifySuccess(sub)
		return
	}
	e.recordNotifyFailure(sub)
}

func (e *eventManager) deliverNotify(sub *eventSubscription, props eventProperties, seq uint32) bool {
	ctx, cancel := context.WithTimeout(context.Background(), defaultNotifyTimeout)
	defer cancel()

	v := &urlValidator{resolver: e.resolver, client: e.client}
	callbackURL, _, err := v.parseAndClassify(ctx, sub.CallbackURL, PolicyPrivateOnly)
	if err != nil {
		return false
	}

	body := buildGENAPropertySet(props)
	req, err := http.NewRequestWithContext(ctx, "NOTIFY", callbackURL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("CONTENT-TYPE", `text/xml; charset="utf-8"`)
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SID", sub.SID)
	req.Header.Set("SEQ", strconv.FormatUint(uint64(seq), 10))
	req.ContentLength = int64(len(body))

	resp, err := noRedirectNotifyClient(e.client).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, defaultNotifyResponseLimit)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false
	}
	return true
}

func noRedirectNotifyClient(base *http.Client) *http.Client {
	if base == nil {
		base = newPolicyNotifyClient(defaultDNSResolver)
	}
	cp := *base
	cp.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cp
}

func newPolicyNotifyClient(resolver dnsResolverFunc) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: defaultNotifyTimeout}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := resolver(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve %q: %v", ErrAddressNotAllowed, host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w: %q resolved to no addresses", ErrAddressNotAllowed, host)
		}
		for _, ip := range ips {
			class := classifyIP(ip)
			if class != ipClassPrivate {
				return nil, fmt.Errorf("%w: %s -> %s (policy=PrivateOnly)", ErrAddressNotAllowed, host, ip)
			}
		}
		if net.ParseIP(host) != nil {
			return dialer.DialContext(ctx, network, addr)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{
		Timeout:   defaultNotifyTimeout,
		Transport: transport,
	}
}

func nextEventSeq(seq uint32) uint32 {
	next := seq + 1
	if next == 0 {
		return 1
	}
	return next
}

func eventPropertiesEqual(a, b eventProperties) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || bv != av {
			return false
		}
	}
	return true
}

func (e *eventManager) recordNotifySuccess(sub *eventSubscription) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.subscriptions[sub.Service][sub.SID]
	if current == nil {
		return
	}
	// Delivery failures are counted consecutively; any success resets the streak.
	current.FailureCount = 0
}

func (e *eventManager) recordNotifyFailure(sub *eventSubscription) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.subscriptions[sub.Service][sub.SID]
	if current == nil {
		return
	}
	current.FailureCount++
	if current.FailureCount >= defaultMaxNotifyFailures {
		e.deleteSubscriptionLocked(sub.Service, sub.SID)
	}
}

func (e *eventManager) validateCallbackHeader(ctx context.Context, header string) (string, error) {
	callbacks, err := parseCallbackHeader(header)
	if err != nil {
		return "", err
	}
	v := &urlValidator{resolver: e.resolver, client: e.client}
	policyRejected := false
	for _, raw := range callbacks {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || u.Hostname() == "" {
			return "", errCallbackMalformed
		}
		if strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https" {
			return "", errCallbackMalformed
		}
		if _, _, err := v.parseAndClassify(ctx, u.String(), PolicyPrivateOnly); err == nil {
			return u.String(), nil
		}
		policyRejected = true
	}
	if policyRejected {
		return "", errCallbackPolicy
	}
	return "", errCallbackMalformed
}

func parseCallbackHeader(header string) ([]string, error) {
	rest := strings.TrimSpace(header)
	var callbacks []string
	for rest != "" {
		if !strings.HasPrefix(rest, "<") {
			return nil, errCallbackMalformed
		}
		end := strings.Index(rest, ">")
		if end <= 1 {
			return nil, errCallbackMalformed
		}
		callbacks = append(callbacks, rest[1:end])
		rest = strings.TrimSpace(rest[end+1:])
	}
	if len(callbacks) == 0 {
		return nil, errCallbackMalformed
	}
	return callbacks, nil
}
