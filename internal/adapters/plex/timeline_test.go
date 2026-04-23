package plex

import (
	"io"
	"net/http"
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
	for _, want := range []string{
		`commandID="9"`,
		`state="playing"`,
		`key="/library/metadata/42"`,
		`machineIdentifier="server-uuid"`,
	} {
		if !strings.Contains(h.body, want) {
			t.Errorf("PMS body missing %q: %s", want, h.body)
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
