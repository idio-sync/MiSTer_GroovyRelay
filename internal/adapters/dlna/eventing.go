package dlna

import (
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
		client = &http.Client{Timeout: defaultNotifyTimeout}
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
		snapshots: map[eventService]eventProperties{},
	}
}

func (e *eventManager) setSnapshot(service eventService, props eventProperties) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.snapshots[service] = cloneEventProperties(props)
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
	initialSub := cloneSubscription(sub)
	e.mu.Unlock()

	w.Header().Set("SID", sid)
	w.Header().Set("TIMEOUT", "Second-"+strconv.Itoa(int(timeout/time.Second)))
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
	go e.sendNotify(initialSub, initial, 0)
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
	delete(e.subscriptions[service], sid)
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
				delete(e.subscriptions[service], sid)
			}
		}
	}
}

func (e *eventManager) sendNotify(_ *eventSubscription, _ eventProperties, _ uint32) {}

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
