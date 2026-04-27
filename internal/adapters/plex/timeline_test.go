package plex

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func newTestBroker(t *testing.T, status core.SessionStatus) *TimelineBroker {
	t.Helper()
	b := NewTimelineBroker(TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		func() core.SessionStatus { return status })
	return b
}

func TestTimeline_SubscribeAddsSubscriber(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	b.Subscribe("client-a", "127.0.0.1", "32500", "http", 0)
	if got := b.subscriberCount(); got != 1 {
		t.Errorf("subscriberCount = %d, want 1", got)
	}
}

func TestTimeline_UnsubscribeRemoves(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	b.Subscribe("client-a", "127.0.0.1", "32500", "http", 0)
	b.Unsubscribe("client-a")
	if got := b.subscriberCount(); got != 0 {
		t.Errorf("subscriberCount after unsubscribe = %d, want 0", got)
	}
}

func TestTimeline_StaleSubscriberPrunedAfterTTL(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	// Swap the broker's clock so we can advance past the TTL deterministically
	// instead of sleeping for 90s.
	base := time.Now()
	now := base
	b.now = func() time.Time { return now }
	b.TTL = 100 * time.Millisecond

	b.Subscribe("client-a", "127.0.0.1", "32500", "http", 0)
	if got := b.subscriberCount(); got != 1 {
		t.Fatalf("pre-advance subscriberCount = %d, want 1", got)
	}
	// Advance past TTL; broadcastOnce should prune.
	now = base.Add(1 * time.Second)
	b.broadcastOnce()
	if got := b.subscriberCount(); got != 0 {
		t.Errorf("post-advance subscriberCount = %d, want 0 (pruned)", got)
	}
}

func TestTimeline_TouchResetsLastSeen(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	base := time.Now()
	now := base
	b.now = func() time.Time { return now }
	b.TTL = 100 * time.Millisecond

	b.Subscribe("client-a", "127.0.0.1", "32500", "http", 0)
	now = base.Add(50 * time.Millisecond)
	b.TouchSubscriber("client-a")
	now = base.Add(120 * time.Millisecond) // past 100ms from base, but 70ms from touch
	b.broadcastOnce()
	if got := b.subscriberCount(); got != 1 {
		t.Errorf("subscriber pruned despite touch: count = %d, want 1", got)
	}
}

func TestTimeline_BuildXML_StatePlaying(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	xml := b.buildTimelineXML(core.SessionStatus{
		State:    core.StatePlaying,
		Position: 12345 * time.Millisecond,
		Duration: 60000 * time.Millisecond,
	})
	if !strings.Contains(xml, `state="playing"`) {
		t.Errorf("xml missing state=playing: %s", xml)
	}
	if !strings.Contains(xml, `time="12345"`) {
		t.Errorf("xml missing time=12345: %s", xml)
	}
	if !strings.Contains(xml, `duration="60000"`) {
		t.Errorf("xml missing duration=60000: %s", xml)
	}
}

func TestTimeline_BuildXML_IncludesPlexMetadata(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	b.SetPlayContextProvider(func() PlayMediaRequest {
		return PlayMediaRequest{
			PlexServerAddress: "192.168.1.10",
			PlexServerPort:    "32400",
			PlexServerScheme:  "http",
			PlexMachineID:     "server-uuid",
			MediaKey:          "/library/metadata/42",
			ContainerKey:      "/playQueues/99?own=1",
			AudioStreamID:     "11",
			SubtitleStreamID:  "22",
			PlayQueueItemID:   "item-123",
			CommandID:         "7",
		}
	})
	got := b.buildTimelineXML(core.SessionStatus{
		State:    core.StatePlaying,
		Position: 12345 * time.Millisecond,
		Duration: 60000 * time.Millisecond,
	})
	for _, want := range []string{
		`commandID="7"`,
		`location="fullScreenVideo"`,
		`ratingKey="42"`,
		`key="/library/metadata/42"`,
		`containerKey="/playQueues/99?own=1"`,
		`address="192.168.1.10"`,
		`port="32400"`,
		`protocol="http"`,
		`machineIdentifier="server-uuid"`,
		`seekRange="0-60000"`,
		`audioStreamID="11"`,
		`subtitleStreamID="22"`,
		`playQueueItemID="item-123"`,
		`controllable="playPause,stop,seekTo"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("xml missing %q: %s", want, got)
		}
	}
}

func TestTimeline_BuildXML_StateMapping(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	cases := []struct {
		state core.State
		want  string
	}{
		{core.StateIdle, `state="stopped"`},
		{core.StatePlaying, `state="playing"`},
		{core.StatePaused, `state="paused"`},
	}
	for _, tc := range cases {
		got := b.buildTimelineXML(core.SessionStatus{State: tc.state})
		if !strings.Contains(got, tc.want) {
			t.Errorf("state=%s: xml missing %q: %s", tc.state, tc.want, got)
		}
	}
}

func TestTimeline_BuildXML_AlwaysIncludesMusicAndPhoto(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	got := b.buildTimelineXML(core.SessionStatus{State: core.StatePlaying})
	if !strings.Contains(got, `type="music"`) {
		t.Error("xml missing music timeline")
	}
	if !strings.Contains(got, `type="photo"`) {
		t.Error("xml missing photo timeline")
	}
	if !strings.Contains(got, `type="video"`) {
		t.Error("xml missing video timeline")
	}
}

func TestTimeline_BroadcastPushesToSubscribers(t *testing.T) {
	// Spin up a fake Plex controller that records POSTs to /:/timeline.
	type received struct {
		headers http.Header
		body    string
	}
	var mu sync.Mutex
	var hits []received

	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/:/timeline" {
			http.Error(w, "unexpected path", 404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, received{headers: r.Header.Clone(), body: string(body)})
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	b := NewTimelineBroker(
		TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		func() core.SessionStatus {
			return core.SessionStatus{
				State:    core.StatePlaying,
				Position: 5000 * time.Millisecond,
			}
		},
	)
	b.Subscribe("client-xyz", u.Hostname(), u.Port(), "http", 7)
	b.broadcastOnce()

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 {
		t.Fatalf("controller received %d POSTs, want 1", len(hits))
	}
	h := hits[0]
	if h.headers.Get("X-Plex-Client-Identifier") != "uuid-1" {
		t.Errorf("X-Plex-Client-Identifier = %q, want uuid-1", h.headers.Get("X-Plex-Client-Identifier"))
	}
	if h.headers.Get("X-Plex-Device-Name") != "MiSTer" {
		t.Errorf("X-Plex-Device-Name = %q, want MiSTer", h.headers.Get("X-Plex-Device-Name"))
	}
	if h.headers.Get("X-Plex-Target-Client-Identifier") != "client-xyz" {
		t.Errorf("X-Plex-Target-Client-Identifier = %q, want client-xyz",
			h.headers.Get("X-Plex-Target-Client-Identifier"))
	}
	if !strings.Contains(h.body, `commandID="7"`) {
		t.Errorf("body missing commandID=7: %s", h.body)
	}
	if !strings.Contains(h.body, `state="playing"`) {
		t.Errorf("body missing state=playing: %s", h.body)
	}
}

func TestTimeline_BroadcastPushesToPMSWithoutSubscribers(t *testing.T) {
	type received struct {
		headers http.Header
		body    string
		query   url.Values
	}
	var mu sync.Mutex
	var hits []received

	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/:/timeline" {
			http.Error(w, "unexpected path", 404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, received{
			headers: r.Header.Clone(),
			body:    string(body),
			query:   r.URL.Query(),
		})
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	b := NewTimelineBroker(
		TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		func() core.SessionStatus {
			return core.SessionStatus{
				State:    core.StatePlaying,
				Position: 7000 * time.Millisecond,
				Duration: 12000 * time.Millisecond,
			}
		},
	)
	b.SetPlayContextProvider(func() PlayMediaRequest {
		return PlayMediaRequest{
			PlexServerAddress: u.Hostname(),
			PlexServerPort:    u.Port(),
			PlexServerScheme:  "http",
			PlexMachineID:     "server-uuid",
			MediaKey:          "/library/metadata/42",
			PlexToken:         "tok-123",
			CommandID:         "9",
			PlayQueueItemID:   "item-77",
			SessionID:         "play-session-1",
		}
	})
	b.broadcastOnce()
	b.broadcastOnce()

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 {
		t.Fatalf("PMS received %d POSTs, want 1 (duplicate idle/unchanged payloads should be suppressed)", len(hits))
	}
	h := hits[0]
	if h.headers.Get("X-Plex-Client-Identifier") != "uuid-1" {
		t.Errorf("X-Plex-Client-Identifier = %q, want uuid-1", h.headers.Get("X-Plex-Client-Identifier"))
	}
	if h.headers.Get("X-Plex-Device-Name") != "MiSTer" {
		t.Errorf("X-Plex-Device-Name = %q, want MiSTer", h.headers.Get("X-Plex-Device-Name"))
	}
	if h.headers.Get("X-Plex-Token") != "tok-123" {
		t.Errorf("X-Plex-Token = %q, want tok-123", h.headers.Get("X-Plex-Token"))
	}
	if h.query.Get("X-Plex-Token") != "tok-123" {
		t.Errorf("query X-Plex-Token = %q, want tok-123", h.query.Get("X-Plex-Token"))
	}
	if h.headers.Get("X-Plex-Session-Identifier") != "play-session-1" {
		t.Errorf("X-Plex-Session-Identifier = %q, want play-session-1", h.headers.Get("X-Plex-Session-Identifier"))
	}
	for key, want := range map[string]string{
		"key":             "/library/metadata/42",
		"ratingKey":       "42",
		"state":           "playing",
		"time":            "7000",
		"duration":        "12000",
		"playQueueItemID": "item-77",
	} {
		if got := h.query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
}

func TestTimeline_BroadcastStoppedUsesProvidedStatus(t *testing.T) {
	type received struct {
		query url.Values
	}
	var mu sync.Mutex
	var hits []received

	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/:/timeline" {
			http.Error(w, "unexpected path", 404)
			return
		}
		mu.Lock()
		hits = append(hits, received{query: r.URL.Query()})
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	b := NewTimelineBroker(
		TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		func() core.SessionStatus {
			return core.SessionStatus{State: core.StateIdle}
		},
	)
	b.SetPlayContextProvider(func() PlayMediaRequest {
		return PlayMediaRequest{
			PlexServerAddress: u.Hostname(),
			PlexServerPort:    u.Port(),
			PlexServerScheme:  "http",
			MediaKey:          "/library/metadata/42",
			PlexToken:         "tok-123",
			PlayQueueItemID:   "item-77",
		}
	})
	b.broadcastStatusOnce(core.SessionStatus{
		State:    core.StateIdle,
		Position: 47000 * time.Millisecond,
		Duration: 120000 * time.Millisecond,
	})

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 {
		t.Fatalf("PMS received %d POSTs, want 1", len(hits))
	}
	for key, want := range map[string]string{
		"key":             "/library/metadata/42",
		"ratingKey":       "42",
		"state":           "stopped",
		"time":            "47000",
		"duration":        "120000",
		"playQueueItemID": "item-77",
	} {
		if got := hits[0].query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
}

func TestTimeline_StopIsIdempotent(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	b.Stop()
	b.Stop() // must not panic on double close
}

func TestCompanion_TimelineSubscribeWiresBroker(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceUUID: "uuid-1"}, fc)
	b := NewTimelineBroker(TimelineConfig{DeviceUUID: "uuid-1"}, fc.Status)
	c.SetTimeline(b)

	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/timeline/subscribe?" +
		"port=32500&protocol=http&commandID=1&X-Plex-Client-Identifier=client-A")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if b.subscriberCount() != 1 {
		t.Errorf("subscriberCount = %d, want 1", b.subscriberCount())
	}

	resp2, err := http.Get(ts.URL + "/player/timeline/unsubscribe?X-Plex-Client-Identifier=client-A")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if b.subscriberCount() != 0 {
		t.Errorf("subscriberCount after unsub = %d, want 0", b.subscriberCount())
	}
}

func TestCompanion_RequestTouchesSubscriberTTL(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceUUID: "uuid-1"}, fc)
	b := NewTimelineBroker(TimelineConfig{DeviceUUID: "uuid-1"}, fc.Status)
	base := time.Now()
	now := base
	b.now = func() time.Time { return now }
	b.TTL = 100 * time.Millisecond
	c.SetTimeline(b)

	b.Subscribe("client-A", "127.0.0.1", "32500", "http", 1)

	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	now = base.Add(50 * time.Millisecond)
	resp, err := http.Get(ts.URL + "/player/timeline/poll?X-Plex-Client-Identifier=client-A")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	now = base.Add(120 * time.Millisecond)
	b.broadcastOnce()
	if got := b.subscriberCount(); got != 1 {
		t.Errorf("subscriberCount after touched poll = %d, want 1", got)
	}
}

func TestCompanion_TimelinePollReturnsXML(t *testing.T) {
	fc := &fakeCore{status: core.SessionStatus{State: core.StatePlaying}}
	c := NewCompanion(CompanionConfig{}, fc)
	b := NewTimelineBroker(TimelineConfig{}, fc.Status)
	c.SetTimeline(b)

	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/timeline/poll")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `state="playing"`) {
		t.Errorf("poll body missing state=playing: %s", body)
	}
}

// TestCompanion_TimelinePoll_WaitOneHoldsOpenUntilStateChange verifies that
// a poll request carrying wait=1 does not return until either the timeline
// state changes or the long-poll timeout elapses. Plex for Windows sends
// wait=1 and treats a steady stream of immediately-returning polls as an
// unresponsive cast target.
func TestCompanion_TimelinePoll_WaitOneHoldsOpenUntilStateChange(t *testing.T) {
	fc := &fakeCore{status: core.SessionStatus{State: core.StateIdle}}
	c := NewCompanion(CompanionConfig{}, fc)
	b := NewTimelineBroker(TimelineConfig{}, fc.Status)
	c.SetTimeline(b)

	prevTimeout, prevTick := pollLongWaitTimeout, pollLongWaitTick
	pollLongWaitTimeout = 2 * time.Second
	pollLongWaitTick = 25 * time.Millisecond
	t.Cleanup(func() {
		pollLongWaitTimeout = prevTimeout
		pollLongWaitTick = prevTick
	})

	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	// Flip core state from idle → playing after a short delay so the wait
	// loop sees a transition. Without this, the poll would only return on
	// the safety timeout — also a valid outcome but a weaker assertion.
	go func() {
		time.Sleep(150 * time.Millisecond)
		fc.mu.Lock()
		fc.status = core.SessionStatus{State: core.StatePlaying}
		fc.mu.Unlock()
	}()

	start := time.Now()
	resp, err := http.Get(ts.URL + "/player/timeline/poll?wait=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	// The handler must wait at least one tick before noticing the state
	// flip. Anything under ~100ms means wait=1 was ignored entirely.
	if elapsed < 100*time.Millisecond {
		t.Errorf("wait=1 returned in %v, expected >100ms (handler ignored wait)", elapsed)
	}
	if elapsed >= pollLongWaitTimeout {
		t.Errorf("wait=1 hit safety timeout (%v); state-change wakeup didn't fire", elapsed)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `state="playing"`) {
		t.Errorf("poll body missing state=playing: %s", body)
	}
}

// TestCompanion_TimelinePoll_WaitOneTimesOutWhenStateUnchanged verifies that
// the wait loop returns after pollLongWaitTimeout when nothing changes — the
// controller then immediately reissues a fresh poll. Without this safety
// timeout, an idle bridge would keep poll connections open forever.
func TestCompanion_TimelinePoll_WaitOneTimesOutWhenStateUnchanged(t *testing.T) {
	fc := &fakeCore{status: core.SessionStatus{State: core.StateIdle}}
	c := NewCompanion(CompanionConfig{}, fc)
	b := NewTimelineBroker(TimelineConfig{}, fc.Status)
	c.SetTimeline(b)

	prevTimeout, prevTick := pollLongWaitTimeout, pollLongWaitTick
	pollLongWaitTimeout = 200 * time.Millisecond
	pollLongWaitTick = 25 * time.Millisecond
	t.Cleanup(func() {
		pollLongWaitTimeout = prevTimeout
		pollLongWaitTick = prevTick
	})

	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	start := time.Now()
	resp, err := http.Get(ts.URL + "/player/timeline/poll?wait=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if elapsed < pollLongWaitTimeout {
		t.Errorf("wait=1 returned in %v before safety timeout (%v)", elapsed, pollLongWaitTimeout)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `state="stopped"`) {
		t.Errorf("poll body should report stopped on timeout: %s", body)
	}
}

// TestCompanion_TimelinePoll_NoWaitReturnsImmediately preserves the legacy
// fast-path: requests without wait=1 must not be subjected to the long-poll
// loop. Older controllers and ad-hoc tooling rely on /timeline/poll being
// effectively synchronous.
func TestCompanion_TimelinePoll_NoWaitReturnsImmediately(t *testing.T) {
	fc := &fakeCore{status: core.SessionStatus{State: core.StateIdle}}
	c := NewCompanion(CompanionConfig{}, fc)
	b := NewTimelineBroker(TimelineConfig{}, fc.Status)
	c.SetTimeline(b)

	prevTimeout := pollLongWaitTimeout
	pollLongWaitTimeout = 5 * time.Second
	t.Cleanup(func() { pollLongWaitTimeout = prevTimeout })

	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	start := time.Now()
	resp, err := http.Get(ts.URL + "/player/timeline/poll")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("no-wait poll took %v; should return immediately", elapsed)
	}
}

// TestTimeline_BroadcastStoppedFor_UsesCapturedPlay verifies that the
// new broker entry point synthesizes timeline XML from the captured
// PlayMediaRequest and IGNORES playContext (which may already point at
// a foreign session after cross-adapter preempt).
func TestTimeline_BroadcastStoppedFor_UsesCapturedPlay(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	// playContext returns a DIFFERENT play (simulating "URL adapter has
	// already taken over"). The broker MUST NOT consult it.
	b.SetPlayContextProvider(func() PlayMediaRequest {
		return PlayMediaRequest{MediaKey: "/library/metadata/wrong"}
	})

	captured := PlayMediaRequest{
		PlexServerAddress: "192.168.1.10",
		PlexServerPort:    "32400",
		PlexServerScheme:  "http",
		MediaKey:          "/library/metadata/42",
		ContainerKey:      "/playQueues/99?own=1",
		PlayQueueItemID:   "item-123",
	}

	// Stand up an httptest controller endpoint and subscribe to it.
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	b.Subscribe("client-a", host, port, "http", 0)

	stopped := core.SessionStatus{State: core.StateIdle}
	b.broadcastStoppedFor(stopped, captured)
	// broadcastStoppedFor pushes synchronously, so no sleep is needed.

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("subscriber received %d pushes, want 1", len(bodies))
	}
	body := bodies[0]
	if !strings.Contains(body, `state="stopped"`) {
		t.Errorf("body missing state=stopped: %s", body)
	}
	if !strings.Contains(body, `key="/library/metadata/42"`) {
		t.Errorf("body did not use captured MediaKey: %s", body)
	}
	if strings.Contains(body, "/library/metadata/wrong") {
		t.Errorf("body leaked playContext data: %s", body)
	}
}

// TestTimeline_BroadcastStoppedFor_NoSubscribers is a no-op smoke test
// — calling with zero subscribers must not panic and must not push to PMS
// (the captured PlayMediaRequest may not have a PMS address set when the
// only target is a controller).
func TestTimeline_BroadcastStoppedFor_NoSubscribers(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	b.broadcastStoppedFor(core.SessionStatus{State: core.StateIdle}, PlayMediaRequest{})
	// No assertion — just must not panic.
}
