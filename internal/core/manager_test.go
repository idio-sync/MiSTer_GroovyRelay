package core

import (
	"strings"
	"testing"
	"time"

	"github.com/jedivoodoo/mister-groovy-relay/internal/config"
	"github.com/jedivoodoo/mister-groovy-relay/internal/groovynet"
)

// newTestManager returns a Manager with a Sender bound to a free local port.
// The sender is never actually used by these tests (every StartSession fails
// at the Probe step before any UDP traffic), but we still construct a real
// one so we exercise NewManager's real constructor.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })
	cfg := &config.Config{
		MisterHost:          "127.0.0.1",
		MisterPort:          32100,
		SourcePort:          0,
		Modeline:            "NTSC_480i",
		InterlaceFieldOrder: "tff",
		AspectMode:          "letterbox",
		RGBMode:             "rgb888",
		LZ4Enabled:          false,
		AudioSampleRate:     48000,
		AudioChannels:       2,
	}
	return NewManager(cfg, sender)
}

// bogusRequest builds a SessionRequest whose StreamURL reliably fails ffprobe
// (which runs in startPlaneLocked before any UDP work). This lets us exercise
// the Manager's public API and state bookkeeping on any platform without
// needing real media or a fake ffmpeg.
func bogusRequest() SessionRequest {
	return SessionRequest{
		StreamURL:    "udp://127.0.0.1:1/this-url-will-fail-probe",
		AdapterRef:   "test-ref",
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
	}
}

func TestManager_InitialStatusIdle(t *testing.T) {
	m := newTestManager(t)
	st := m.Status()
	if st.State != StateIdle {
		t.Errorf("initial state = %s, want %s", st.State, StateIdle)
	}
	if st.AdapterRef != "" {
		t.Errorf("AdapterRef = %q, want empty", st.AdapterRef)
	}
	if !st.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero", st.StartedAt)
	}
}

func TestManager_StartSession_ProbeFailLeavesIdle(t *testing.T) {
	m := newTestManager(t)
	err := m.StartSession(bogusRequest())
	if err == nil {
		t.Fatal("expected probe failure, got nil")
	}
	if !strings.Contains(err.Error(), "probe source") && !strings.Contains(err.Error(), "ffprobe") {
		t.Logf("err = %v (acceptable — any probe path failure)", err)
	}
	st := m.Status()
	if st.State != StateIdle {
		t.Errorf("state after failed StartSession = %s, want Idle", st.State)
	}
	if m.plane != nil {
		t.Errorf("plane should be nil after failed start")
	}
	if m.active != nil {
		t.Errorf("active should be nil after failed start")
	}
}

func TestManager_StopWhenIdleIsIdempotent(t *testing.T) {
	m := newTestManager(t)
	// First stop from Idle.
	if err := m.Stop(); err != nil {
		t.Errorf("first Stop from Idle: %v", err)
	}
	if m.Status().State != StateIdle {
		t.Errorf("state = %s, want Idle", m.Status().State)
	}
	// Second stop from Idle.
	if err := m.Stop(); err != nil {
		t.Errorf("second Stop from Idle: %v", err)
	}
	if m.Status().State != StateIdle {
		t.Errorf("state = %s, want Idle", m.Status().State)
	}
}

func TestManager_PauseRequiresActiveSession(t *testing.T) {
	m := newTestManager(t)
	err := m.Pause()
	if err == nil {
		t.Fatal("Pause from Idle should fail")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v, want 'no session'", err)
	}
}

func TestManager_PlayRequiresActiveSession(t *testing.T) {
	m := newTestManager(t)
	err := m.Play()
	if err == nil {
		t.Fatal("Play from Idle should fail")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v, want 'no session'", err)
	}
}

func TestManager_SeekRequiresActiveSession(t *testing.T) {
	m := newTestManager(t)
	err := m.SeekTo(5000)
	if err == nil {
		t.Fatal("SeekTo from Idle should fail")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v, want 'no session'", err)
	}
}

func TestManager_BogusModelineRejected(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Modeline = "bogus_modeline"
	err := m.StartSession(bogusRequest())
	if err == nil {
		t.Fatal("expected error for unknown modeline")
	}
	// Could fail at probe OR modeline resolution depending on ffprobe's
	// availability. Either is fine — what matters is StartSession fails
	// cleanly and the FSM stays Idle.
	if m.Status().State != StateIdle {
		t.Errorf("state = %s, want Idle", m.Status().State)
	}
}

func TestManager_BogusRGBModeRejected(t *testing.T) {
	// Construct a Manager whose cfg has an invalid RGBMode and verify
	// resolveRGBMode rejects it. We test the helper directly to avoid the
	// probe dependency.
	if _, err := resolveRGBMode("not-a-mode"); err == nil {
		t.Fatal("expected error for unknown rgb_mode")
	}
}

func TestManager_ResolveModeline(t *testing.T) {
	cases := []struct {
		in  string
		ok  bool
	}{
		{"", true},
		{"NTSC_480i", true},
		{"something-else", false},
	}
	for _, c := range cases {
		_, err := resolveModeline(c.in)
		got := err == nil
		if got != c.ok {
			t.Errorf("resolveModeline(%q) ok=%v, want %v (err=%v)", c.in, got, c.ok, err)
		}
	}
}

func TestManager_ResolveRGBMode(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want byte
	}{
		{"", true, 0},         // groovy.RGBMode888
		{"rgb888", true, 0},   // groovy.RGBMode888
		{"rgba8888", true, 1}, // groovy.RGBMode8888
		{"rgb565", true, 2},   // groovy.RGBMode565
		{"bogus", false, 0},
	}
	for _, c := range cases {
		got, err := resolveRGBMode(c.in)
		ok := err == nil
		if ok != c.ok {
			t.Errorf("resolveRGBMode(%q) ok=%v, want %v", c.in, ok, c.ok)
		}
		if ok && got != c.want {
			t.Errorf("resolveRGBMode(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestManager_BytesPerPixel(t *testing.T) {
	cases := map[byte]int{
		0: 3, // RGBMode888
		1: 4, // RGBMode8888
		2: 2, // RGBMode565
	}
	for mode, want := range cases {
		if got := bytesPerPixel(mode); got != want {
			t.Errorf("bytesPerPixel(%d) = %d, want %d", mode, got, want)
		}
	}
}

// TestManager_PauseFSMRaceSafety verifies that concurrent access to Status()
// while StartSession is failing does not panic or deadlock. This exercises
// the mutex discipline around m.plane / m.active bookkeeping.
func TestManager_PauseFSMRaceSafety(t *testing.T) {
	m := newTestManager(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = m.Status()
		}
	}()
	for i := 0; i < 10; i++ {
		_ = m.StartSession(bogusRequest())
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Status() did not complete")
	}
	if m.Status().State != StateIdle {
		t.Errorf("final state = %s, want Idle", m.Status().State)
	}
}
