package jellyfin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// capturedREST records every POST to /Sessions/Playing*. Tests assert
// on cadence, body shape, and ordering against this fixture.
type capturedREST struct {
	mu     sync.Mutex
	starts []PlaybackProgressInfo
	progs  []PlaybackProgressInfo
	stops  []PlaybackStopInfo
	pings  int

	// stoppedFailures counts how many times the test wants the
	// /Sessions/Playing/Stopped handler to return 500 before
	// succeeding. Used to exercise postPlaybackStopped's retry.
	stoppedFailures atomic.Int32
}

func (c *capturedREST) install(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Sessions/Playing", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var dto PlaybackProgressInfo
		_ = json.Unmarshal(body, &dto)
		c.mu.Lock()
		c.starts = append(c.starts, dto)
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/Sessions/Playing/Progress", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var dto PlaybackProgressInfo
		_ = json.Unmarshal(body, &dto)
		c.mu.Lock()
		c.progs = append(c.progs, dto)
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/Sessions/Playing/Stopped", func(w http.ResponseWriter, r *http.Request) {
		if c.stoppedFailures.Load() > 0 {
			c.stoppedFailures.Add(-1)
			http.Error(w, "transient", http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var dto PlaybackStopInfo
		_ = json.Unmarshal(body, &dto)
		c.mu.Lock()
		c.stops = append(c.stops, dto)
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/Sessions/Playing/Ping", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.pings++
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (c *capturedREST) startCount() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.starts) }
func (c *capturedREST) progCount() int  { c.mu.Lock(); defer c.mu.Unlock(); return len(c.progs) }
func (c *capturedREST) stopCount() int  { c.mu.Lock(); defer c.mu.Unlock(); return len(c.stops) }
func (c *capturedREST) pingCount() int  { c.mu.Lock(); defer c.mu.Unlock(); return c.pings }

func (c *capturedREST) lastStart() PlaybackProgressInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.starts) == 0 {
		return PlaybackProgressInfo{}
	}
	return c.starts[len(c.starts)-1]
}

func (c *capturedREST) lastStop() PlaybackStopInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.stops) == 0 {
		return PlaybackStopInfo{}
	}
	return c.stops[len(c.stops)-1]
}

// authFor returns a RESTAuth pointed at srv. Used by tests that
// spawn reporters directly without going through the WS path.
func authFor(srv *httptest.Server) RESTAuth {
	return RESTAuth{
		ServerURL: srv.URL, Token: "tok", DeviceID: "dev-1",
		DeviceName: "MiSTer", Version: "test",
	}
}

func waitFor(t *testing.T, deadline time.Duration, pred func() bool) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if pred() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestDotNetTicks_UnixEpochRoundTrip(t *testing.T) {
	got := dotNetTicks(time.Unix(0, 0).UTC())
	if got != dotNetUnixEpochTicks {
		t.Errorf("dotNetTicks(unix epoch) = %d, want %d", got, dotNetUnixEpochTicks)
	}
	// Add 1 second; expect 10 million more ticks (1 tick = 100 ns).
	got2 := dotNetTicks(time.Unix(1, 0).UTC())
	if got2-got != 10_000_000 {
		t.Errorf("delta for +1s = %d, want 10_000_000", got2-got)
	}
}

func TestProgressInfo_FieldsPopulated(t *testing.T) {
	aud, sub := 1, 2
	r := &reporter{
		capturedRefKey: "itm-1:ps-7",
		itemID:         "itm-1",
		playSessionID:  "ps-7",
		mediaSourceID:  "src-1",
		audioIdx:       &aud,
		subtitleIdx:    &sub,
		startedAt:      time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
	st := core.SessionStatus{
		State:    core.StatePlaying,
		Position: 90 * time.Second,
	}
	body := r.buildProgressInfo(st, "sess-x")

	if body.ItemID != "itm-1" {
		t.Errorf("ItemId = %q", body.ItemID)
	}
	if body.SessionID != "sess-x" {
		t.Errorf("SessionId = %q", body.SessionID)
	}
	if body.PositionTicks == nil || *body.PositionTicks != 900_000_000 {
		t.Errorf("PositionTicks = %v", body.PositionTicks)
	}
	if body.AudioStreamIndex == nil || *body.AudioStreamIndex != 1 {
		t.Errorf("AudioStreamIndex = %v", body.AudioStreamIndex)
	}
	if body.SubtitleStreamIndex == nil || *body.SubtitleStreamIndex != 2 {
		t.Errorf("SubtitleStreamIndex = %v", body.SubtitleStreamIndex)
	}
	if body.PlayMethod != "Transcode" {
		t.Errorf("PlayMethod = %q", body.PlayMethod)
	}
}

func TestProgressInfo_NilTracksAreOmitted(t *testing.T) {
	r := &reporter{itemID: "i", playSessionID: "p", mediaSourceID: "s", startedAt: time.Now()}
	body := r.buildProgressInfo(core.SessionStatus{State: core.StatePlaying}, "")

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if _, present := got["AudioStreamIndex"]; present {
		t.Errorf("AudioStreamIndex emitted as %v with nil pointer; expected omitempty", got["AudioStreamIndex"])
	}
	if _, present := got["SubtitleStreamIndex"]; present {
		t.Errorf("SubtitleStreamIndex emitted with nil pointer; expected omitempty")
	}
	if _, present := got["SessionId"]; present {
		t.Errorf("SessionId emitted with empty string; expected omitempty")
	}
}

func TestProgressInfo_AudioStreamIndexZeroIsPreserved(t *testing.T) {
	zero := 0
	r := &reporter{
		itemID: "i", playSessionID: "p", mediaSourceID: "s",
		audioIdx: &zero, startedAt: time.Now(),
	}
	body := r.buildProgressInfo(core.SessionStatus{State: core.StatePlaying}, "")
	data, _ := json.Marshal(body)
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	v, present := got["AudioStreamIndex"]
	if !present {
		t.Fatal("AudioStreamIndex was omitted; pointer-zero must be preserved")
	}
	if v.(float64) != 0 {
		t.Errorf("AudioStreamIndex = %v, want 0", v)
	}
}

func TestProgressInfo_PausedReportsTrue(t *testing.T) {
	r := &reporter{itemID: "i", playSessionID: "ps", mediaSourceID: "s", startedAt: time.Now()}
	body := r.buildProgressInfo(core.SessionStatus{State: core.StatePaused}, "")
	if !body.IsPaused {
		t.Errorf("IsPaused = false, want true")
	}
}

func TestStop_DrainsReporters(t *testing.T) {
	cap := &capturedREST{}
	srv := cap.install(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")

	// Park manager in Playing so reporters do not immediately classify
	// as Idle and exit before Stop runs.
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		Auth: authFor(srv), TickInterval: 50 * time.Millisecond,
	})
	a.spawnReporter(reporterParams{
		ItemID: "itm-2", PlaySessionID: "ps-9", MediaSourceID: "src-2",
		Auth: authFor(srv), TickInterval: 50 * time.Millisecond,
	})

	a.mu.Lock()
	pre := len(a.reporters)
	a.mu.Unlock()
	if pre != 2 {
		t.Fatalf("setup: reporters before Stop = %d, want 2", pre)
	}

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reporters) != 0 {
		t.Errorf("after Stop: reporters = %d, want 0", len(a.reporters))
	}
}

func TestReporter_EmitsPlaybackStartAndProgress(t *testing.T) {
	cap := &capturedREST{}
	srv := cap.install(t)

	mgr := &fakeManager{st: core.SessionStatus{
		State: core.StatePlaying, Position: 45 * time.Second,
		AdapterRef: "itm-1:ps-7",
	}}
	a := New(mgr, t.TempDir(), "dev-1")

	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		Auth: authFor(srv), TickInterval: 30 * time.Millisecond,
	})
	defer a.stopReporter("itm-1:ps-7")

	if !waitFor(t, 1*time.Second, func() bool {
		return cap.startCount() >= 1 && cap.progCount() >= 1
	}) {
		t.Fatalf("starts=%d progs=%d, want >=1 of each", cap.startCount(), cap.progCount())
	}
	if start := cap.lastStart(); start.PlaySessionID != "ps-7" {
		t.Errorf("PlaybackStart.PlaySessionId = %q", start.PlaySessionID)
	}
}

func TestReporter_StatusIdleEndsLoopWithStopped(t *testing.T) {
	cap := &capturedREST{}
	srv := cap.install(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		Auth: authFor(srv), TickInterval: 30 * time.Millisecond,
	})
	time.Sleep(60 * time.Millisecond)

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StateIdle}
	mgr.mu.Unlock()

	if !waitFor(t, 1*time.Second, func() bool { return cap.stopCount() >= 1 }) {
		t.Fatalf("no PlaybackStopped seen")
	}
	if a.lookupReporter("itm-1:ps-7") != nil {
		t.Errorf("reporter still registered after StateIdle")
	}
	if cap.lastStop().Failed {
		t.Errorf("Failed = true on clean idle, want false")
	}
}

func TestReporter_ExternalPreemptEmitsStoppedNotFailed(t *testing.T) {
	cap := &capturedREST{}
	srv := cap.install(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")

	a.currentRefKey = "itm-1:ps-7"
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		Auth: authFor(srv), TickInterval: 30 * time.Millisecond,
	})
	time.Sleep(60 * time.Millisecond)

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "plex:other"}
	mgr.mu.Unlock()

	if !waitFor(t, 1*time.Second, func() bool { return cap.stopCount() >= 1 }) {
		t.Fatal("no PlaybackStopped on external preempt")
	}
	if cap.lastStop().Failed {
		t.Errorf("Failed = true on external preempt, want false")
	}
}

func TestReporter_SelfPreemptElidesStopped(t *testing.T) {
	cap := &capturedREST{}
	srv := cap.install(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")

	a.currentRefKey = "itm-1:ps-7"
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		Auth: authFor(srv), TickInterval: 30 * time.Millisecond,
	})
	time.Sleep(60 * time.Millisecond)

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-99"}
	mgr.mu.Unlock()
	a.mu.Lock()
	a.currentRefKey = "itm-1:ps-99"
	a.mu.Unlock()

	time.Sleep(150 * time.Millisecond)

	if cap.stopCount() != 0 {
		t.Errorf("self-preempt should elide PlaybackStopped; stops=%d", cap.stopCount())
	}
	if a.lookupReporter("itm-1:ps-7") != nil {
		t.Errorf("reporter still registered after self-preempt")
	}
}

func TestReporter_OnStopErrorMarksFailedTrue(t *testing.T) {
	cap := &capturedREST{}
	srv := cap.install(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()
	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		Auth: authFor(srv), TickInterval: 30 * time.Millisecond,
	})
	time.Sleep(60 * time.Millisecond)

	a.makeOnStop("itm-1:ps-7")("error")
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StateIdle}
	mgr.mu.Unlock()

	if !waitFor(t, 1*time.Second, func() bool { return cap.stopCount() >= 1 }) {
		t.Fatal("no PlaybackStopped after error")
	}
	if !cap.lastStop().Failed {
		t.Errorf("Failed = false on OnStop error, want true")
	}
}

func TestReporter_StoppedRetriesOnTransientFailure(t *testing.T) {
	cap := &capturedREST{}
	cap.stoppedFailures.Store(1) // first POST returns 500, second succeeds
	srv := cap.install(t)

	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}}
	a := New(mgr, t.TempDir(), "dev-1")

	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		Auth: authFor(srv), TickInterval: 20 * time.Millisecond,
	})

	if !waitFor(t, 2*time.Second, func() bool { return cap.stopCount() >= 1 }) {
		t.Fatalf("no successful PlaybackStopped after retry; stops=%d", cap.stopCount())
	}
}

// atomicInt32 is a small counter used by commands_test.go.
type atomicInt32 struct{ v atomic.Int32 }

func (a *atomicInt32) inc()     { a.v.Add(1) }
func (a *atomicInt32) get() int { return int(a.v.Load()) }

func TestReporter_PingsWithCustomTicker(t *testing.T) {
	// Override pingTickInterval is not exposed; verify ping wiring via
	// a helper that drives r.pingTicker directly.
	cap := &capturedREST{}
	srv := cap.install(t)

	a := New(&fakeManager{st: core.SessionStatus{State: core.StatePlaying, AdapterRef: "x"}},
		t.TempDir(), "dev-1")
	r := &reporter{playSessionID: "ps-7", auth: authFor(srv)}
	a.emitPing(r)
	if !waitFor(t, 1*time.Second, func() bool { return cap.pingCount() >= 1 }) {
		t.Fatalf("ping not received; count=%d", cap.pingCount())
	}
}
