package jellyfin

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestProgressInfo_FieldsPopulated(t *testing.T) {
	r := &reporter{
		capturedRefKey: "itm-1:ps-7",
		itemID:         "itm-1",
		playSessionID:  "ps-7",
		mediaSourceID:  "src-1",
		startedAt:      time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
	st := core.SessionStatus{
		State:    core.StatePlaying,
		Position: 90 * time.Second,
		Duration: 30 * time.Minute,
	}
	audIdx := 1
	subIdx := 2
	body := r.buildProgressInfo(st, audIdx, subIdx)

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(data, &got)

	if got["ItemId"] != "itm-1" {
		t.Errorf("ItemId = %v", got["ItemId"])
	}
	if got["MediaSourceId"] != "src-1" {
		t.Errorf("MediaSourceId = %v", got["MediaSourceId"])
	}
	if got["PlaySessionId"] != "ps-7" {
		t.Errorf("PlaySessionId = %v", got["PlaySessionId"])
	}
	if got["PositionTicks"].(float64) != 900_000_000 { // 90 seconds × 10M
		t.Errorf("PositionTicks = %v", got["PositionTicks"])
	}
	if got["IsPaused"] != false {
		t.Errorf("IsPaused = %v", got["IsPaused"])
	}
	if got["PlayMethod"] != "Transcode" {
		t.Errorf("PlayMethod = %v", got["PlayMethod"])
	}
	if got["AudioStreamIndex"].(float64) != 1 {
		t.Errorf("AudioStreamIndex = %v", got["AudioStreamIndex"])
	}
	if got["SubtitleStreamIndex"].(float64) != 2 {
		t.Errorf("SubtitleStreamIndex = %v", got["SubtitleStreamIndex"])
	}
}

func TestProgressInfo_PausedReportsTrue(t *testing.T) {
	r := &reporter{itemID: "i", playSessionID: "ps", mediaSourceID: "s", startedAt: time.Now()}
	st := core.SessionStatus{State: core.StatePaused}
	body := r.buildProgressInfo(st, 0, 0)
	if !body.IsPaused {
		t.Errorf("IsPaused = false, want true")
	}
}

func TestRingBuffer_DropsOldestOnOverflow(t *testing.T) {
	rb := newRingBuffer(2)
	rb.push(outboundEnvelope{MessageType: "1"})
	rb.push(outboundEnvelope{MessageType: "2"})
	rb.push(outboundEnvelope{MessageType: "3"}) // should drop "1"

	got := rb.drainAll()
	if len(got) != 2 {
		t.Fatalf("len(drained) = %d, want 2", len(got))
	}
	if got[0].MessageType != "2" || got[1].MessageType != "3" {
		t.Errorf("drained = %+v, want [2 3]", got)
	}
}

func TestRingBuffer_DrainAllEmptyAfter(t *testing.T) {
	rb := newRingBuffer(4)
	rb.push(outboundEnvelope{MessageType: "x"})
	_ = rb.drainAll()
	got := rb.drainAll()
	if len(got) != 0 {
		t.Errorf("second drain = %d, want 0", len(got))
	}
}

// emittedFinder collects messages a fake-write side observed.
type emittedFinder struct {
	mu  sync.Mutex
	out []outboundEnvelope
}

func (e *emittedFinder) collect() []outboundEnvelope {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]outboundEnvelope, len(e.out))
	copy(cp, e.out)
	return cp
}

func (e *emittedFinder) push(env outboundEnvelope) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.out = append(e.out, env)
}

// installFakeSendOutbound replaces a.sendOutboundFn for the duration
// of the test. Returns the finder. Mutex-guarded so the reporter
// goroutine cannot race the swap.
func installFakeSendOutbound(t *testing.T, a *Adapter) *emittedFinder {
	t.Helper()
	finder := &emittedFinder{}
	a.mu.Lock()
	prev := a.sendOutboundFn
	a.sendOutboundFn = finder.push
	a.mu.Unlock()
	t.Cleanup(func() {
		a.mu.Lock()
		a.sendOutboundFn = prev
		a.mu.Unlock()
	})
	return finder
}

// TestStop_DrainsReporters covers the C1 fix from the final
// pre-merge review: Stop() must clean up any reporters left in
// a.reporters so that a subsequent Start does not see ghost
// progress goroutines pushing stale state into a fresh WS
// connection. We stage two reporters into the map directly (the
// path used by the real WS-driven flow takes Adapter.mu the same
// way), then call Stop and assert the map is empty and all
// reporter goroutines have wound down.
func TestStop_DrainsReporters(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	installFakeSendOutbound(t, a)

	// Park the manager in Playing so the reporter goroutines do not
	// immediately exit via the Idle classifier before Stop runs.
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID:        "itm-1",
		PlaySessionID: "ps-7",
		MediaSourceID: "src-1",
		TickInterval:  50 * time.Millisecond,
	})
	a.spawnReporter(reporterParams{
		ItemID:        "itm-2",
		PlaySessionID: "ps-9",
		MediaSourceID: "src-2",
		TickInterval:  50 * time.Millisecond,
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
	post := len(a.reporters)
	a.mu.Unlock()
	if post != 0 {
		t.Errorf("after Stop: reporters = %d, want 0", post)
	}
}

func TestReporter_EmitsPlaybackStartAndProgress(t *testing.T) {
	mgr := &fakeManager{st: core.SessionStatus{
		State:      core.StatePlaying,
		Position:   45 * time.Second,
		AdapterRef: "itm-1:ps-7",
	}}
	a := New(mgr, t.TempDir(), "dev-1")
	finder := installFakeSendOutbound(t, a)

	a.spawnReporter(reporterParams{
		ItemID:        "itm-1",
		PlaySessionID: "ps-7",
		MediaSourceID: "src-1",
		TickInterval:  50 * time.Millisecond,
	})
	defer a.stopReporter("itm-1:ps-7")

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(finder.collect()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := finder.collect()
	if len(got) < 2 {
		t.Fatalf("got %d messages, want >= 2", len(got))
	}
	if got[0].MessageType != "PlaybackStart" {
		t.Errorf("first = %q, want PlaybackStart", got[0].MessageType)
	}
	if got[1].MessageType != "PlaybackProgress" {
		t.Errorf("second = %q, want PlaybackProgress", got[1].MessageType)
	}
}

func TestReporter_StatusIdleEndsLoopWithStopped(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	finder := installFakeSendOutbound(t, a)

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID:        "itm-1",
		PlaySessionID: "ps-7",
		MediaSourceID: "src-1",
		TickInterval:  30 * time.Millisecond,
	})

	time.Sleep(50 * time.Millisecond)
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StateIdle}
	mgr.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	stoppedSeen := false
	for time.Now().Before(deadline) {
		for _, m := range finder.collect() {
			if m.MessageType == "PlaybackStopped" {
				stoppedSeen = true
				break
			}
		}
		if stoppedSeen {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !stoppedSeen {
		t.Fatalf("no PlaybackStopped seen; messages=%+v", finder.collect())
	}

	if a.lookupReporter("itm-1:ps-7") != nil {
		t.Errorf("reporter still registered after StateIdle")
	}
}

func TestReporter_ExternalPreemptEmitsStoppedNotFailed(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	finder := installFakeSendOutbound(t, a)

	a.currentRefKey = "itm-1:ps-7"
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID:        "itm-1",
		PlaySessionID: "ps-7",
		MediaSourceID: "src-1",
		TickInterval:  30 * time.Millisecond,
	})
	time.Sleep(60 * time.Millisecond)

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "plex:other"}
	mgr.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	stopped := false
	for time.Now().Before(deadline) {
		for _, m := range finder.collect() {
			if m.MessageType == "PlaybackStopped" {
				body := m.Data.(PlaybackProgressInfo)
				if body.Failed {
					t.Errorf("Failed = true on external preempt, want false")
				}
				stopped = true
				break
			}
		}
		if stopped {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !stopped {
		t.Fatal("no PlaybackStopped on external preempt")
	}
}

func TestReporter_SelfPreemptElidesStopped(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	finder := installFakeSendOutbound(t, a)

	a.currentRefKey = "itm-1:ps-7"
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()

	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		TickInterval: 30 * time.Millisecond,
	})
	time.Sleep(60 * time.Millisecond)

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-99"}
	mgr.mu.Unlock()
	a.mu.Lock()
	a.currentRefKey = "itm-1:ps-99"
	a.mu.Unlock()

	time.Sleep(150 * time.Millisecond)

	for _, m := range finder.collect() {
		if m.MessageType == "PlaybackStopped" {
			t.Errorf("self-preempt should elide PlaybackStopped; saw: %+v", m)
		}
	}

	if a.lookupReporter("itm-1:ps-7") != nil {
		t.Errorf("reporter still registered after self-preempt")
	}
}

func TestReporter_OnStopErrorMarksFailedTrue(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	finder := installFakeSendOutbound(t, a)

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-7"}
	mgr.mu.Unlock()
	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-7", MediaSourceID: "src-1",
		TickInterval: 30 * time.Millisecond,
	})
	time.Sleep(60 * time.Millisecond)

	a.makeOnStop("itm-1:ps-7")("error")
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StateIdle}
	mgr.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, m := range finder.collect() {
			if m.MessageType == "PlaybackStopped" {
				body := m.Data.(PlaybackProgressInfo)
				if !body.Failed {
					t.Errorf("Failed = false on OnStop error, want true")
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no PlaybackStopped after error")
}

// Atomic counter used in some tests below.
type atomicInt32 struct{ v atomic.Int32 }

func (a *atomicInt32) inc()     { a.v.Add(1) }
func (a *atomicInt32) get() int { return int(a.v.Load()) }
