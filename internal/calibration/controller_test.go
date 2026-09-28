package calibration

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeSessions struct {
	mu       sync.Mutex
	busy     bool
	startErr error
	gen      uint64
	started  []core.SessionRequest
	stopped  []string
	calls    []string
	// duringStart runs inside StartSessionIfIdleSnapshot, to simulate a
	// session ending before Start returns.
	duringStart func(core.SessionRequest)
}

func (f *fakeSessions) StartSessionIfIdleSnapshot(req core.SessionRequest) (core.SessionStatus, bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "start")
	if f.startErr != nil || f.busy {
		f.mu.Unlock()
		return core.SessionStatus{}, !f.busy, f.startErr
	}
	f.gen++
	gen := f.gen
	f.started = append(f.started, req)
	hook := f.duringStart
	f.mu.Unlock()
	if hook != nil {
		hook(req)
	}
	return core.SessionStatus{AdapterRef: req.AdapterRef, Generation: gen}, true, nil
}

func (f *fakeSessions) StopIfSession(ref string, gen uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop")
	f.stopped = append(f.stopped, ref)
	return true, nil
}

func (f *fakeSessions) last() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[len(f.started)-1]
}

type fakeSaver struct {
	err   error
	saved []config.PictureGeometry
	log   *[]string
}

func (s *fakeSaver) SavePicture(g config.PictureGeometry) error {
	if s.log != nil {
		*s.log = append(*s.log, "save")
	}
	if s.err != nil {
		return s.err
	}
	s.saved = append(s.saved, g)
	return nil
}

type manualTimer struct {
	fire func()
}

func newTestController(t *testing.T) (*Controller, *fakeSessions, *fakeSaver, *manualTimer, *config.BridgeConfig) {
	t.Helper()
	bridge := &config.BridgeConfig{Video: config.VideoConfig{Modeline: "NTSC_480i", PictureHSize: 95, PictureVSize: 100}}
	sessions := &fakeSessions{}
	saver := &fakeSaver{log: &sessions.calls}
	c := NewController(sessions, saver, func() config.BridgeConfig { return *bridge })
	timer := &manualTimer{}
	c.afterFunc = func(_ time.Duration, f func()) func() bool {
		timer.fire = f
		return func() bool { return true }
	}
	return c, sessions, saver, timer, bridge
}

func TestControllerStartBuildsQuietFrameSession(t *testing.T) {
	c, sessions, _, _, _ := newTestController(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	req := sessions.last()
	if req.Frames == nil || !req.QuietOSD || req.Source != "calibration" || req.StreamURL != "" {
		t.Fatalf("start request = %+v", req)
	}
	p := req.Frames.(*Pattern)
	if p.width != 720 || p.height != 480 || !p.interlaced {
		t.Fatalf("pattern raster = %dx%d interlaced=%v, want 720x480i", p.width, p.height, p.interlaced)
	}
	snap := c.Snapshot()
	if snap.State != StateActive || snap.Draft != (config.PictureGeometry{HSize: 95, VSize: 100}) {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Starting again while active is a no-op, not a second session.
	if err := c.Start(); err != nil || len(sessions.started) != 1 {
		t.Fatalf("second Start err=%v sessions=%d", err, len(sessions.started))
	}
}

func TestControllerStartBusy(t *testing.T) {
	c, sessions, _, _, _ := newTestController(t)
	sessions.busy = true
	if err := c.Start(); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start on a busy bridge = %v, want ErrBusy", err)
	}
	if c.Snapshot().State != StateIdle {
		t.Fatal("busy Start must leave the controller idle")
	}
}

func TestControllerPreviewRedrawsWithoutSessionCalls(t *testing.T) {
	c, sessions, _, _, _ := newTestController(t)
	if err := c.Preview(config.PictureGeometry{}); !errors.Is(err, ErrNotActive) {
		t.Fatalf("Preview while idle = %v, want ErrNotActive", err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	callsBefore := len(sessions.calls)
	g := config.PictureGeometry{HSize: 90, VSize: 90, HOffset: 4}
	if err := c.Preview(g); err != nil {
		t.Fatal(err)
	}
	if len(sessions.calls) != callsBefore {
		t.Fatalf("Preview made session calls: %v", sessions.calls[callsBefore:])
	}
	if got := c.Snapshot().Draft; got != g {
		t.Fatalf("draft = %+v, want %+v", got, g)
	}
	// The pattern now has its left edge at the draft rect.
	r := g.Rect(720, 480, true)
	frame := frameOf(sessions.last().Frames.(*Pattern))
	if at(frame, r.X, 250) != colorWhite || at(frame, 0, 250) != colorBlack {
		t.Fatal("pattern was not redrawn at the draft rect")
	}
	if err := c.Preview(config.PictureGeometry{HSize: 50}); err == nil {
		t.Fatal("Preview accepted an out-of-range size")
	}
	if got := c.Snapshot().Draft; got != g {
		t.Fatalf("invalid preview changed the draft to %+v", got)
	}
}

func TestControllerCastTakeoverKeepsDraftAndResumes(t *testing.T) {
	c, sessions, saver, _, _ := newTestController(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	g := config.PictureGeometry{HSize: 88, VSize: 92, VOffset: -2}
	if err := c.Preview(g); err != nil {
		t.Fatal(err)
	}
	sessions.last().OnStop("preempted")
	snap := c.Snapshot()
	if snap.State != StateEnded || snap.EndReason != EndCast || snap.Draft != g {
		t.Fatalf("after takeover snapshot = %+v", snap)
	}
	// Calibrate again resumes the unsaved draft.
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot().Draft; got != g {
		t.Fatalf("resumed draft = %+v, want %+v", got, g)
	}
	// Save still works after an ended calibration.
	sessions.last().OnStop("preempted")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if len(saver.saved) != 1 || saver.saved[0] != g {
		t.Fatalf("saved = %+v", saver.saved)
	}
}

func TestControllerStopReasons(t *testing.T) {
	for reason, want := range map[string]string{"preempted": EndCast, "stopped": EndStopped, "error": EndError, "eof": EndError} {
		c, sessions, _, _, _ := newTestController(t)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		sessions.last().OnStop(reason)
		if got := c.Snapshot().EndReason; got != want {
			t.Errorf("OnStop(%q) end reason = %q, want %q", reason, got, want)
		}
	}
}

// OnStop from a session the controller already released (its own Save or
// Cancel, or a previous calibration) must not end the current one.
func TestControllerIgnoresStaleOnStop(t *testing.T) {
	c, sessions, _, _, _ := newTestController(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	first := sessions.last()
	c.Cancel()
	first.OnStop("stopped") // Manager reports the stop Cancel caused
	if st := c.Snapshot().State; st != StateIdle {
		t.Fatalf("stale OnStop after Cancel moved state to %s", st)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	first.OnStop("preempted")
	if st := c.Snapshot().State; st != StateActive {
		t.Fatalf("old session's OnStop ended the new calibration (state %s)", st)
	}
}

func TestControllerSessionEndingDuringStart(t *testing.T) {
	c, sessions, _, _, _ := newTestController(t)
	sessions.duringStart = func(req core.SessionRequest) { req.OnStop("error") }
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if snap := c.Snapshot(); snap.State != StateEnded || snap.EndReason != EndError {
		t.Fatalf("snapshot = %+v, want ended/error", snap)
	}
}

func TestControllerTimeout(t *testing.T) {
	c, sessions, _, timer, _ := newTestController(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	staleFire := timer.fire
	if err := c.Preview(config.PictureGeometry{HSize: 90}); err != nil {
		t.Fatal(err)
	}
	staleFire() // the timer armed before the preview no longer counts
	if st := c.Snapshot().State; st != StateActive {
		t.Fatalf("stale timeout ended calibration (state %s)", st)
	}
	timer.fire()
	snap := c.Snapshot()
	if snap.State != StateEnded || snap.EndReason != EndTimeout || snap.Draft.HSize != 90 {
		t.Fatalf("after timeout snapshot = %+v", snap)
	}
	if len(sessions.stopped) != 1 {
		t.Fatalf("timeout stopped %d sessions, want 1", len(sessions.stopped))
	}
}

func TestControllerSaveStopsBeforeWriting(t *testing.T) {
	c, sessions, saver, _, _ := newTestController(t)
	if err := c.Save(); !errors.Is(err, ErrNotActive) {
		t.Fatalf("Save while idle = %v, want ErrNotActive", err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	g := config.PictureGeometry{HSize: 93, VSize: 97, HOffset: -3, VOffset: 1}
	if err := c.Preview(g); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if got := sessions.calls; len(got) != 3 || got[1] != "stop" || got[2] != "save" {
		t.Fatalf("call order = %v, want start, stop, save", got)
	}
	if saver.saved[0] != g {
		t.Fatalf("saved %+v, want %+v", saver.saved[0], g)
	}
	if st := c.Snapshot().State; st != StateIdle {
		t.Fatalf("state after save = %s, want idle", st)
	}
}

func TestControllerSaveFailureKeepsDraftForRetry(t *testing.T) {
	c, _, saver, _, _ := newTestController(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	g := config.PictureGeometry{HSize: 91, VSize: 100}
	_ = c.Preview(g)
	saver.err = errors.New("disk full")
	if err := c.Save(); err == nil {
		t.Fatal("Save swallowed the write error")
	}
	snap := c.Snapshot()
	if snap.State != StateEnded || snap.EndReason != EndSaveFailed || snap.Draft != g {
		t.Fatalf("after failed save snapshot = %+v", snap)
	}
	saver.err = nil
	if err := c.Save(); err != nil {
		t.Fatalf("retry Save: %v", err)
	}
	if len(saver.saved) != 1 || saver.saved[0] != g {
		t.Fatalf("saved = %+v", saver.saved)
	}
}

func TestControllerCancelDiscardsDraft(t *testing.T) {
	c, sessions, saver, _, _ := newTestController(t)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	_ = c.Preview(config.PictureGeometry{HSize: 85, VSize: 85})
	c.Cancel()
	c.Cancel() // idempotent
	snap := c.Snapshot()
	if snap.State != StateIdle || snap.Draft != snap.Saved {
		t.Fatalf("after cancel snapshot = %+v", snap)
	}
	if len(saver.saved) != 0 || len(sessions.stopped) != 1 {
		t.Fatalf("cancel saved=%d stopped=%d, want 0/1", len(saver.saved), len(sessions.stopped))
	}
}

func TestControllerSnapshotReportsRasterForModeline(t *testing.T) {
	c, _, _, _, bridge := newTestController(t)
	if s := c.Snapshot(); s.RasterWidth != 720 || s.FieldLines != 240 {
		t.Fatalf("NTSC_480i raster = %d x %d field lines, want 720 x 240", s.RasterWidth, s.FieldLines)
	}
	bridge.Video.Modeline = "PAL_576i"
	if s := c.Snapshot(); s.FieldLines != 288 {
		t.Fatalf("PAL_576i field lines = %d, want 288", s.FieldLines)
	}
}
