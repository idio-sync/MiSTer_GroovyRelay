package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// defaultSubscriberTTL is the Plex-mpv-shim canonical idle window. Subscribers
// that haven't been touched within this window are pruned on each broadcast
// tick. Exposed through TimelineBroker.TTL for testability.
const defaultSubscriberTTL = 90 * time.Second

// TimelineConfig carries the X-Plex-* identity headers the broker sends on
// every timeline push. Kept separate from CompanionConfig so the broker can
// be unit-tested without constructing a full Companion.
type TimelineConfig struct {
	DeviceUUID string
	DeviceName string
}

// subscriber is the broker's per-controller state. One entry per registered
// Plex controller UUID; pruned after TTL of inactivity.
type subscriber struct {
	clientID  string
	host      string // already stripped of :port by handleTimelineSubscribe
	port      string
	commandID int
	protocol  string
	lastSeen  time.Time
}

// TimelineBroker pushes 1 Hz timeline XML to registered Plex controllers and
// serves the /player/timeline/poll long-poll fallback. Thread-safe.
type TimelineBroker struct {
	cfg         TimelineConfig
	status      func() core.SessionStatus
	mu          sync.Mutex
	subscribers map[string]*subscriber
	stop        chan struct{}
	stopOnce    sync.Once

	// TTL is the stale-subscriber prune threshold. Defaults to 90s per the
	// Plex-mpv-shim convention; tests override this to avoid real waits.
	TTL time.Duration

	// now is swappable for tests that want to simulate the subscriber clock.
	// Production callers leave this nil and get time.Now.
	now func() time.Time

	// httpClient is swappable for tests that want to intercept pushes without
	// standing up a real httptest.Server.
	httpClient *http.Client
}

// NewTimelineBroker constructs a TimelineBroker. statusFn is called once per
// broadcast tick to fetch the live core.SessionStatus.
func NewTimelineBroker(cfg TimelineConfig, statusFn func() core.SessionStatus) *TimelineBroker {
	return &TimelineBroker{
		cfg:         cfg,
		status:      statusFn,
		subscribers: make(map[string]*subscriber),
		stop:        make(chan struct{}),
		TTL:         defaultSubscriberTTL,
		httpClient:  &http.Client{Timeout: 1 * time.Second},
	}
}

// RunBroadcastLoop ticks every second and pushes the current timeline XML to
// every live subscriber. Returns when Stop is called. Run in a goroutine.
func (t *TimelineBroker) RunBroadcastLoop() {
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			t.broadcastOnce()
		}
	}
}

// Stop signals the broadcast loop to exit. Idempotent; subsequent calls are
// no-ops (sync.Once-guarded so a second Stop doesn't panic on a double close).
func (t *TimelineBroker) Stop() {
	t.stopOnce.Do(func() { close(t.stop) })
}

// timeNow returns the broker's current time. Tests can override t.now to
// advance the subscriber TTL clock deterministically.
func (t *TimelineBroker) timeNow() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// broadcastOnce prunes stale subscribers, then pushes the current timeline
// XML to each live one. Isolated for test access.
func (t *TimelineBroker) broadcastOnce() {
	t.mu.Lock()
	now := t.timeNow()
	ttl := t.TTL
	if ttl == 0 {
		ttl = defaultSubscriberTTL
	}
	subs := make([]subscriber, 0, len(t.subscribers))
	for id, s := range t.subscribers {
		if now.Sub(s.lastSeen) > ttl {
			delete(t.subscribers, id)
			continue
		}
		subs = append(subs, *s)
	}
	cfg := t.cfg
	client := t.httpClient
	t.mu.Unlock()

	if len(subs) == 0 {
		return
	}

	st := t.status()
	xmlBody := t.buildTimelineXML(st)

	for _, s := range subs {
		protocol := s.protocol
		if protocol == "" {
			protocol = "http"
		}
		url := fmt.Sprintf("%s://%s:%s/:/timeline", protocol, s.host, s.port)
		req, err := http.NewRequestWithContext(context.Background(), "POST", url,
			strings.NewReader(xmlBody))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("X-Plex-Protocol", "1.0")
		req.Header.Set("X-Plex-Client-Identifier", cfg.DeviceUUID)
		req.Header.Set("X-Plex-Device-Name", cfg.DeviceName)
		req.Header.Set("X-Plex-Target-Client-Identifier", s.clientID)
		resp, err := client.Do(req)
		if err != nil {
			slog.Debug("timeline push failed", "sub", s.clientID, "err", err)
			continue
		}
		resp.Body.Close()
	}
}

// buildTimelineXML renders the three-<Timeline> MediaContainer Plex expects:
// music/photo are always stopped (we only play video); video carries the live
// state/position. State maps core.State → Plex strings.
func (t *TimelineBroker) buildTimelineXML(s core.SessionStatus) string {
	type Timeline struct {
		XMLName  xml.Name `xml:"Timeline"`
		Type     string   `xml:"type,attr"`
		State    string   `xml:"state,attr"`
		Time     int64    `xml:"time,attr"`
		Duration int64    `xml:"duration,attr"`
	}
	type MediaContainer struct {
		XMLName   xml.Name   `xml:"MediaContainer"`
		Timelines []Timeline `xml:"Timeline"`
	}
	plexState := "stopped"
	switch s.State {
	case core.StatePlaying:
		plexState = "playing"
	case core.StatePaused:
		plexState = "paused"
	}
	mc := MediaContainer{
		Timelines: []Timeline{
			{Type: "music", State: "stopped"},
			{Type: "photo", State: "stopped"},
			{
				Type:     "video",
				State:    plexState,
				Time:     s.Position.Milliseconds(),
				Duration: s.Duration.Milliseconds(),
			},
		},
	}
	out, _ := xml.Marshal(mc)
	return string(out)
}

// Subscribe registers or refreshes a controller subscription. The subscriber
// keeps receiving timeline pushes until Unsubscribe or until the TTL elapses.
func (t *TimelineBroker) Subscribe(clientID, host, port, protocol string, commandID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscribers[clientID] = &subscriber{
		clientID:  clientID,
		host:      host,
		port:      port,
		protocol:  protocol,
		commandID: commandID,
		lastSeen:  t.timeNow(),
	}
}

// Unsubscribe removes a controller. No-op if it wasn't registered.
func (t *TimelineBroker) Unsubscribe(clientID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.subscribers, clientID)
}

// TouchSubscriber refreshes a subscriber's lastSeen so the TTL prune doesn't
// reap an active controller. Called by the Companion on any request that
// carries a known X-Plex-Client-Identifier.
func (t *TimelineBroker) TouchSubscriber(clientID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.subscribers[clientID]; ok {
		s.lastSeen = t.timeNow()
	}
}

// subscriberCount is a test helper returning the current subscriber count.
func (t *TimelineBroker) subscriberCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subscribers)
}
