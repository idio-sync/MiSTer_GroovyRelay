package dlna

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func privateResolverForEventTests(ip string) dnsResolverFunc {
	parsed := net.ParseIP(ip)
	return func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{parsed}, nil
	}
}

func newEventManagerForTest(now time.Time) *eventManager {
	return newEventManager(eventManagerConfig{
		now:              func() time.Time { return now },
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           http.DefaultClient,
		maxPerService:    2,
		maxPerRemoteAddr: 1,
	})
}

func eventRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "192.168.1.44:54321"
	return req
}

type notifyCapture struct {
	Method string
	Header http.Header
	Body   string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func waitForNotify(t *testing.T, ch <-chan notifyCapture) notifyCapture {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NOTIFY")
		return notifyCapture{}
	}
}

func waitForSubscriptionPruned(t *testing.T, em *eventManager, service eventService, sid string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("subscription still exists after %d delivery failures", defaultMaxNotifyFailures)
		case <-tick.C:
			em.mu.Lock()
			_, exists := em.subscriptions[service][sid]
			em.mu.Unlock()
			if !exists {
				return
			}
		}
	}
}

func waitForFailedSeq(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("failed NOTIFY SEQ = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for failed NOTIFY %s", want)
	}
}

func TestEventSubscribe_NewSubscriptionContract(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	em := newEventManagerForTest(now)
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	req := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	req.Header.Set("CALLBACK", "<http://controller.local:1400/callback>")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("TIMEOUT", "Second-900")

	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, req, serviceAVTransport)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("SID"); !strings.HasPrefix(got, "uuid:") {
		t.Fatalf("SID = %q, want uuid: prefix", got)
	}
	if got := rr.Header().Get("TIMEOUT"); got != "Second-900" {
		t.Fatalf("TIMEOUT = %q, want Second-900", got)
	}
	if got := rr.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("Content-Length = %q, want 0", got)
	}
}

func TestEventSubscribe_SendsInitialNotify(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	notifies := make(chan notifyCapture, 1)
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read NOTIFY body: %v", err)
		}
		notifies <- notifyCapture{
			Method: r.Method,
			Header: r.Header.Clone(),
			Body:   string(body),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer cb.Close()
	em := newEventManager(eventManagerConfig{
		now:              func() time.Time { return now },
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           cb.Client(),
		maxPerService:    2,
		maxPerRemoteAddr: 2,
	})
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	req := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	req.Header.Set("CALLBACK", "<"+cb.URL+">")
	req.Header.Set("NT", "upnp:event")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, req, serviceAVTransport)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	got := waitForNotify(t, notifies)
	if got.Method != "NOTIFY" {
		t.Fatalf("NOTIFY method = %q, want NOTIFY", got.Method)
	}
	if seq := got.Header.Get("SEQ"); seq != "0" {
		t.Fatalf("NOTIFY SEQ = %q, want 0", seq)
	}
	if nt := got.Header.Get("NT"); nt != "upnp:event" {
		t.Fatalf("NOTIFY NT = %q, want upnp:event", nt)
	}
	if nts := got.Header.Get("NTS"); nts != "upnp:propchange" {
		t.Fatalf("NOTIFY NTS = %q, want upnp:propchange", nts)
	}
	if sid := got.Header.Get("SID"); sid != rr.Header().Get("SID") {
		t.Fatalf("NOTIFY SID = %q, want %q", sid, rr.Header().Get("SID"))
	}
	if !strings.Contains(got.Body, `<LastChange>initial</LastChange>`) {
		t.Fatalf("NOTIFY body missing initial LastChange:\n%s", got.Body)
	}
}

func TestEventSubscribe_RejectsMalformedNewSubscription(t *testing.T) {
	em := newEventManagerForTest(time.Now())
	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "missing callback", mutate: func(req *http.Request) { req.Header.Set("NT", "upnp:event") }},
		{name: "missing nt", mutate: func(req *http.Request) { req.Header.Set("CALLBACK", "<http://controller.local/cb>") }},
		{name: "unsupported nt", mutate: func(req *http.Request) {
			req.Header.Set("CALLBACK", "<http://controller.local/cb>")
			req.Header.Set("NT", "something-else")
		}},
		{name: "malformed callback", mutate: func(req *http.Request) {
			req.Header.Set("CALLBACK", "http://controller.local/cb")
			req.Header.Set("NT", "upnp:event")
		}},
		{name: "sid with callback", mutate: func(req *http.Request) {
			req.Header.Set("SID", "uuid:known")
			req.Header.Set("CALLBACK", "<http://controller.local/cb>")
			req.Header.Set("NT", "upnp:event")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
			tt.mutate(req)
			rr := httptest.NewRecorder()
			em.handleSubscribe(rr, req, serviceAVTransport)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rr.Code)
			}
		})
	}
}

func TestEventSubscribe_RejectsDisallowedCallbackWithPrecondition(t *testing.T) {
	em := newEventManager(eventManagerConfig{
		now:      time.Now,
		resolver: privateResolverForEventTests("127.0.0.1"),
		client:   http.DefaultClient,
	})
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	req := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	req.Header.Set("CALLBACK", "<http://controller.local/cb>")
	req.Header.Set("NT", "upnp:event")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, req, serviceAVTransport)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412 for disallowed callback address", rr.Code)
	}
}

func TestEventSubscribe_AcceptsLaterValidCallbackURL(t *testing.T) {
	resolver := func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "public.local":
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		case "private.local":
			return []net.IP{net.ParseIP("192.168.1.10")}, nil
		default:
			return nil, nil
		}
	}
	em := newEventManager(eventManagerConfig{
		now:              time.Now,
		resolver:         resolver,
		client:           http.DefaultClient,
		maxPerService:    4,
		maxPerRemoteAddr: 4,
	})
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	req := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	req.Header.Set("CALLBACK", "<http://public.local/cb><http://private.local/cb>")
	req.Header.Set("NT", "upnp:event")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, req, serviceAVTransport)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEventSubscribe_ResolverSeamAllowsHTTPTestCallback(t *testing.T) {
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer cb.Close()

	em := newEventManager(eventManagerConfig{
		now:              time.Now,
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           cb.Client(),
		maxPerService:    4,
		maxPerRemoteAddr: 4,
	})
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	req := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	req.Header.Set("CALLBACK", "<"+cb.URL+">")
	req.Header.Set("NT", "upnp:event")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, req, serviceAVTransport)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 through injected resolver; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEventSubscribe_RenewAndUnsubscribeContracts(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	em := newEventManagerForTest(now)
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	req := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	req.Header.Set("CALLBACK", "<http://controller.local:1400/callback>")
	req.Header.Set("NT", "upnp:event")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, req, serviceAVTransport)
	sid := rr.Header().Get("SID")

	renew := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	renew.Header.Set("SID", sid)
	renew.Header.Set("TIMEOUT", "Second-1200")
	rr = httptest.NewRecorder()
	em.handleSubscribe(rr, renew, serviceAVTransport)
	if rr.Code != http.StatusOK {
		t.Fatalf("renew status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("SID"); got != sid {
		t.Fatalf("renew SID = %q, want %q", got, sid)
	}

	unsub := eventRequest("UNSUBSCRIBE", "/dlna/event/AVTransport")
	unsub.Header.Set("SID", sid)
	rr = httptest.NewRecorder()
	em.handleUnsubscribe(rr, unsub, serviceAVTransport)
	if rr.Code != http.StatusOK {
		t.Fatalf("unsubscribe status = %d, want 200", rr.Code)
	}

	rr = httptest.NewRecorder()
	em.handleUnsubscribe(rr, unsub, serviceAVTransport)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("second unsubscribe status = %d, want 412", rr.Code)
	}
}

func TestEventSubscribe_RejectsMalformedSIDBeforeLookup(t *testing.T) {
	em := newEventManagerForTest(time.Now())

	renew := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	renew.Header.Set("SID", "not-a-uuid")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, renew, serviceAVTransport)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed renew status = %d, want 400", rr.Code)
	}

	unsub := eventRequest("UNSUBSCRIBE", "/dlna/event/AVTransport")
	unsub.Header.Set("SID", "not-a-uuid")
	rr = httptest.NewRecorder()
	em.handleUnsubscribe(rr, unsub, serviceAVTransport)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed unsubscribe status = %d, want 400", rr.Code)
	}
}

func TestEventSubscribe_UnknownWellFormedSIDPreconditionFailed(t *testing.T) {
	em := newEventManagerForTest(time.Now())
	unknownSID := "uuid:11111111-2222-3333-4444-555555555555"

	renew := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	renew.Header.Set("SID", unknownSID)
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, renew, serviceAVTransport)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("unknown renew status = %d, want 412", rr.Code)
	}

	unsub := eventRequest("UNSUBSCRIBE", "/dlna/event/AVTransport")
	unsub.Header.Set("SID", unknownSID)
	rr = httptest.NewRecorder()
	em.handleUnsubscribe(rr, unsub, serviceAVTransport)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("unknown unsubscribe status = %d, want 412", rr.Code)
	}
}

func TestEventSubscribe_TimeoutClamping(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", 1800 * time.Second},
		{"infinite", 1800 * time.Second},
		{"Second-12", 300 * time.Second},
		{"Second-900", 900 * time.Second},
		{"Second-999999", 1800 * time.Second},
		{"Second-9223372036854775807", 1800 * time.Second},
		{"garbage", 1800 * time.Second},
	}
	for _, tt := range tests {
		if got := parseEventTimeout(tt.raw); got != tt.want {
			t.Fatalf("parseEventTimeout(%q) = %s, want %s", tt.raw, got, tt.want)
		}
	}
}

func TestEventSubscribe_CapExhaustionRejectsNew(t *testing.T) {
	em := newEventManagerForTest(time.Now())
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	first := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	first.Header.Set("CALLBACK", "<http://controller-one.local/cb>")
	first.Header.Set("NT", "upnp:event")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, first, serviceAVTransport)
	if rr.Code != http.StatusOK {
		t.Fatalf("first subscribe status = %d, want 200", rr.Code)
	}

	second := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	second.Header.Set("CALLBACK", "<http://controller-two.local/cb>")
	second.Header.Set("NT", "upnp:event")
	rr = httptest.NewRecorder()
	em.handleSubscribe(rr, second, serviceAVTransport)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("second same-remote subscribe status = %d, want 503", rr.Code)
	}
}

func TestEventSubscribe_CapExhaustionSkipsCallbackResolution(t *testing.T) {
	resolveCalls := 0
	resolver := func(_ context.Context, _ string) ([]net.IP, error) {
		resolveCalls++
		return []net.IP{net.ParseIP("192.168.1.10")}, nil
	}
	em := newEventManager(eventManagerConfig{
		now:              time.Now,
		resolver:         resolver,
		client:           http.DefaultClient,
		maxPerService:    4,
		maxPerRemoteAddr: 1,
	})
	em.setSnapshot(serviceAVTransport, eventProperties{"LastChange": "initial"})

	first := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	first.Header.Set("CALLBACK", "<http://controller-one.local/cb>")
	first.Header.Set("NT", "upnp:event")
	rr := httptest.NewRecorder()
	em.handleSubscribe(rr, first, serviceAVTransport)
	if rr.Code != http.StatusOK {
		t.Fatalf("first subscribe status = %d, want 200", rr.Code)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolve calls after first subscribe = %d, want 1", resolveCalls)
	}

	second := eventRequest("SUBSCRIBE", "/dlna/event/AVTransport")
	second.Header.Set("CALLBACK", "<http://controller-two.local/cb>")
	second.Header.Set("NT", "upnp:event")
	rr = httptest.NewRecorder()
	em.handleSubscribe(rr, second, serviceAVTransport)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("second same-remote subscribe status = %d, want 503", rr.Code)
	}
	if resolveCalls != 1 {
		t.Fatalf("cap rejection performed callback DNS resolution: calls=%d, want 1", resolveCalls)
	}
}

func TestEventPublish_IncrementsSequenceAndWrapsWithoutZero(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	notifies := make(chan notifyCapture, 2)
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read NOTIFY body: %v", err)
		}
		notifies <- notifyCapture{
			Method: r.Method,
			Header: r.Header.Clone(),
			Body:   string(body),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer cb.Close()
	em := newEventManager(eventManagerConfig{
		now:              func() time.Time { return now },
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           cb.Client(),
		maxPerService:    2,
		maxPerRemoteAddr: 2,
	})
	em.subscriptions[serviceAVTransport]["uuid:11111111-2222-3333-4444-555555555555"] = &eventSubscription{
		SID:         "uuid:11111111-2222-3333-4444-555555555555",
		Service:     serviceAVTransport,
		CallbackURL: cb.URL,
		RemoteAddr:  "192.168.1.44",
		ExpiresAt:   now.Add(time.Hour),
		NextSeq:     4294967295,
	}

	em.publish(serviceAVTransport, eventProperties{"LastChange": "one"})
	first := waitForNotify(t, notifies)
	em.publish(serviceAVTransport, eventProperties{"LastChange": "two"})
	second := waitForNotify(t, notifies)

	if seq := first.Header.Get("SEQ"); seq != strconv.FormatUint(4294967295, 10) {
		t.Fatalf("first publish SEQ = %q, want 4294967295", seq)
	}
	if seq := second.Header.Get("SEQ"); seq != "1" {
		t.Fatalf("second publish SEQ = %q, want 1 after rollover", seq)
	}
}

func TestEventPublish_SuppressesUnchangedSnapshots(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	notifyCount := make(chan struct{}, 2)
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notifyCount <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer cb.Close()
	em := newEventManager(eventManagerConfig{
		now:              func() time.Time { return now },
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           cb.Client(),
		maxPerService:    2,
		maxPerRemoteAddr: 2,
	})
	em.subscriptions[serviceAVTransport]["uuid:11111111-2222-3333-4444-555555555555"] = &eventSubscription{
		SID:         "uuid:11111111-2222-3333-4444-555555555555",
		Service:     serviceAVTransport,
		CallbackURL: cb.URL,
		RemoteAddr:  "192.168.1.44",
		ExpiresAt:   now.Add(time.Hour),
		NextSeq:     1,
	}

	props := eventProperties{"LastChange": "same"}
	em.publish(serviceAVTransport, props)
	select {
	case <-notifyCount:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first NOTIFY")
	}
	em.publish(serviceAVTransport, cloneEventProperties(props))
	select {
	case <-notifyCount:
		t.Fatal("unchanged publish sent a NOTIFY")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEventPublish_PrunesAfterRepeatedDeliveryFailures(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	requests := make(chan struct{}, defaultMaxNotifyFailures)
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer cb.Close()
	em := newEventManager(eventManagerConfig{
		now:              func() time.Time { return now },
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           cb.Client(),
		maxPerService:    2,
		maxPerRemoteAddr: 2,
	})
	sid := "uuid:11111111-2222-3333-4444-555555555555"
	em.subscriptions[serviceAVTransport][sid] = &eventSubscription{
		SID:         sid,
		Service:     serviceAVTransport,
		CallbackURL: cb.URL,
		RemoteAddr:  "192.168.1.44",
		ExpiresAt:   now.Add(time.Hour),
		NextSeq:     1,
	}

	for i := 0; i < defaultMaxNotifyFailures; i++ {
		em.publish(serviceAVTransport, eventProperties{"LastChange": strconv.Itoa(i)})
		select {
		case <-requests:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for failed NOTIFY %d", i+1)
		}
	}

	waitForSubscriptionPruned(t, em, serviceAVTransport, sid)
}

func TestEventPublish_AccountsFailuresInSequenceOrder(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	firstArrived := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	failed := make(chan string, 3)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusInternalServerError
		switch r.Header.Get("SEQ") {
		case "1":
			firstArrived <- struct{}{}
			<-releaseFirst
			status = http.StatusOK
		default:
			failed <- r.Header.Get("SEQ")
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	em := newEventManager(eventManagerConfig{
		now:              func() time.Time { return now },
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           client,
		maxPerService:    2,
		maxPerRemoteAddr: 2,
	})
	sid := "uuid:11111111-2222-3333-4444-555555555555"
	em.subscriptions[serviceAVTransport][sid] = &eventSubscription{
		SID:         sid,
		Service:     serviceAVTransport,
		CallbackURL: "http://controller.local/cb",
		RemoteAddr:  "192.168.1.44",
		ExpiresAt:   now.Add(time.Hour),
		NextSeq:     1,
	}

	em.publish(serviceAVTransport, eventProperties{"LastChange": "one"})
	select {
	case <-firstArrived:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first NOTIFY")
	}

	em.publish(serviceAVTransport, eventProperties{"LastChange": "two"})
	select {
	case got := <-failed:
		t.Fatalf("NOTIFY SEQ %s started before SEQ 1 completed", got)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	waitForFailedSeq(t, failed, "2")
	em.publish(serviceAVTransport, eventProperties{"LastChange": "three"})
	waitForFailedSeq(t, failed, "3")
	em.publish(serviceAVTransport, eventProperties{"LastChange": "four"})
	waitForFailedSeq(t, failed, "4")
	waitForSubscriptionPruned(t, em, serviceAVTransport, sid)
}

func TestEventPublish_DeliveryQueuePreservesEnqueueOrderBeforeExecution(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	started := make(chan string, 2)
	releaseFirst := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seq := r.Header.Get("SEQ")
		started <- seq
		if seq == "1" {
			<-releaseFirst
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	em := newEventManager(eventManagerConfig{
		now:              func() time.Time { return now },
		resolver:         privateResolverForEventTests("192.168.1.10"),
		client:           client,
		maxPerService:    2,
		maxPerRemoteAddr: 2,
	})
	sid := "uuid:11111111-2222-3333-4444-555555555555"
	sub := &eventSubscription{
		SID:         sid,
		Service:     serviceAVTransport,
		CallbackURL: "http://controller.local/cb",
		RemoteAddr:  "192.168.1.44",
		ExpiresAt:   now.Add(time.Hour),
		NextSeq:     1,
	}
	em.subscriptions[serviceAVTransport][sid] = sub

	em.mu.Lock()
	key, queue, start := em.enqueueNotifyLocked(sub, eventProperties{"LastChange": "one"}, 1)
	_, _, startAgain := em.enqueueNotifyLocked(sub, eventProperties{"LastChange": "two"}, 2)
	em.mu.Unlock()
	if !start {
		t.Fatal("first enqueue did not request a delivery worker")
	}
	if startAgain {
		t.Fatal("second enqueue requested a second delivery worker")
	}

	go em.runDeliveryQueue(key, queue)
	select {
	case got := <-started:
		if got != "1" {
			t.Fatalf("first delivered SEQ = %q, want 1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SEQ 1 delivery")
	}
	select {
	case got := <-started:
		t.Fatalf("SEQ %s delivered before SEQ 1 completed", got)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case got := <-started:
		if got != "2" {
			t.Fatalf("second delivered SEQ = %q, want 2", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SEQ 2 delivery")
	}
}

func TestNewSubscriptionSIDFormatsUUIDAndFailsClosed(t *testing.T) {
	sid, err := newSubscriptionSIDFromReader(strings.NewReader("abcdefghijklmnop"))
	if err != nil {
		t.Fatalf("newSubscriptionSIDFromReader: %v", err)
	}
	if !strings.HasPrefix(sid, "uuid:") || len(sid) != len("uuid:00000000-0000-0000-0000-000000000000") {
		t.Fatalf("SID = %q, want uuid-formatted SID", sid)
	}

	_, err = newSubscriptionSIDFromReader(failingReader{})
	if err == nil {
		t.Fatal("newSubscriptionSIDFromReader(failingReader) err = nil, want entropy error")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
