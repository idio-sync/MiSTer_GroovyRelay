package url

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// withStatus returns a fakeCore whose Status() returns the given value.
func withStatus(s core.SessionStatus) *fakeCore {
	fc := &fakeCore{}
	fc.statusFn = func() core.SessionStatus { return s }
	return fc
}

// postEmpty issues a POST without a body to a control endpoint, against
// the given handler.
func postEmpty(handler http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestPause_StatePlaying_CallsPause(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc"})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handlePause)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !fc.pauseCalled {
		t.Error("expected core.Pause() to be called")
	}
}

func TestPause_StatePaused_ShortCircuits(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePaused, AdapterRef: "url:abc"})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handlePause)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 on already-paused short-circuit", w.Code)
	}
	if fc.pauseCalled {
		t.Error("Pause() should NOT be called when already paused (FSM would reject EvPause)")
	}
}

func TestPause_NoSession_ReturnsConflict(t *testing.T) {
	fc := &fakeCore{
		pauseErr: errors.New("no session to pause"),
	}
	fc.statusFn = func() core.SessionStatus { return core.SessionStatus{State: core.StateIdle} }
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handlePause)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no session") {
		t.Errorf("body should mention error: %s", w.Body.String())
	}
}

func TestPause_ForeignAdapterRef_409(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		AdapterRef: "plex:xyz",
	})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handlePause)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if fc.pauseCalled {
		t.Error("Pause() must NOT be called for foreign session")
	}
	if !strings.Contains(w.Body.String(), "another adapter") {
		t.Errorf("body should mention cross-adapter conflict: %s", w.Body.String())
	}
}

func TestResume_StatePaused_DurationPositive_CallsPlay(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePaused,
		Duration:   10 * time.Minute,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	w := postEmpty(a.handleResume)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !fc.playCalled {
		t.Error("expected core.Play()")
	}
	if fc.lastReq.StreamURL != "" {
		t.Error("StartSession should NOT have been called (Duration > 0)")
	}
}

func TestResume_StatePaused_DurationZero_CallsStartSession(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePaused,
		Duration:   0,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://live.example/feed")
	w := postEmpty(a.handleResume)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if fc.playCalled {
		t.Error("Play() should NOT be called for live (Duration == 0)")
	}
	if fc.lastReq.StreamURL != "https://live.example/feed" {
		t.Errorf("StartSession.StreamURL = %q, want lastURL", fc.lastReq.StreamURL)
	}
	if fc.lastReq.SeekOffsetMs != 0 {
		t.Errorf("SeekOffsetMs = %d, want 0 (live reconnect from edge)", fc.lastReq.SeekOffsetMs)
	}
}

func TestResume_StatePaused_DurationZero_LastURLEmpty_400(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePaused,
		Duration:   0,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handleResume)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if fc.playCalled || fc.lastReq.StreamURL != "" {
		t.Error("no core mutation expected when lastURL empty")
	}
}

func TestResume_StatePaused_ForeignAdapterRef_409(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePaused,
		Duration:   10 * time.Minute,
		AdapterRef: "plex:xyz",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	w := postEmpty(a.handleResume)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if fc.playCalled || fc.lastReq.StreamURL != "" {
		t.Error("no core mutation expected when AdapterRef is foreign")
	}
	if !strings.Contains(w.Body.String(), "another adapter") {
		t.Errorf("body should mention cross-adapter conflict: %s", w.Body.String())
	}
}

func TestResume_StatePlaying_ShortCircuits(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handleResume)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if fc.playCalled || fc.lastReq.StreamURL != "" {
		t.Error("no core mutation expected when already Playing")
	}
}

func TestResume_StatePlaying_ForeignAdapterRef_409(t *testing.T) {
	// Foreign-Playing session should hit the ownership guard, not the
	// Playing short-circuit. Tests the IM-2 ordering fix.
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		AdapterRef: "plex:xyz",
	})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handleResume)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (ownership-guard-before-short-circuit)", w.Code)
	}
	if fc.playCalled {
		t.Error("Play() must NOT be called for foreign Playing session")
	}
}

func TestStop_URLSession_CallsStop(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handleStop)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !fc.stopCalled {
		t.Error("expected core.Stop()")
	}
}

func TestStop_ForeignSession_409(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		AdapterRef: "plex:xyz",
	})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handleStop)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if fc.stopCalled {
		t.Error("Stop() must NOT be called for foreign session")
	}
}

func TestStop_IdleEmptyRef_CallsStopIdempotent(t *testing.T) {
	// AdapterRef empty (no active session) is treated as ownership-OK
	// per the panel-layout rule "non-empty AND not 'url:' prefixed →
	// foreign". core.Stop() is idempotent at the manager level
	// (state.go:76: EvStop lands in Idle from any state).
	fc := withStatus(core.SessionStatus{State: core.StateIdle, AdapterRef: ""})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handleStop)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !fc.stopCalled {
		t.Error("Stop() should be called even when idle (idempotent at manager level)")
	}
}

func TestReplay_LastURLSet_CallsStartSession(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc"})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	w := postEmpty(a.handleReplay)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if fc.lastReq.StreamURL != "https://example.com/v.mp4" {
		t.Errorf("StreamURL = %q, want lastURL", fc.lastReq.StreamURL)
	}
	if fc.lastReq.SeekOffsetMs != 0 {
		t.Errorf("SeekOffsetMs = %d, want 0 for replay-from-start", fc.lastReq.SeekOffsetMs)
	}
}

func TestReplay_LastURLEmpty_400(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StateIdle, AdapterRef: ""})
	a := newTestAdapter(t, fc)
	w := postEmpty(a.handleReplay)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if fc.lastReq.StreamURL != "" {
		t.Error("StartSession must not be called when lastURL empty")
	}
}

func TestReplay_ForeignSession_409(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "plex:xyz"})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	w := postEmpty(a.handleReplay)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if fc.lastReq.StreamURL != "" {
		t.Error("StartSession must not be called for foreign session")
	}
}

func TestSeek_ValidOffset_CallsSeekTo(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   10 * time.Minute,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	body := strings.NewReader("offset_ms=120000")
	req := httptest.NewRequest(http.MethodPost, "/seek", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleSeek(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !fc.seekCalled || fc.seekOffsetMs != 120000 {
		t.Errorf("seekCalled=%v offset=%d, want 120000", fc.seekCalled, fc.seekOffsetMs)
	}
}

func TestSeek_OffsetClampedAboveDuration(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   1 * time.Minute, // 60_000 ms
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	body := strings.NewReader("offset_ms=999999")
	req := httptest.NewRequest(http.MethodPost, "/seek", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handleSeek(httptest.NewRecorder(), req)
	if fc.seekOffsetMs != 60000 {
		t.Errorf("clamped offset = %d, want 60000 (Duration ms)", fc.seekOffsetMs)
	}
}

func TestSeek_OffsetClampedBelowZero(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   10 * time.Minute,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	body := strings.NewReader("offset_ms=-50")
	req := httptest.NewRequest(http.MethodPost, "/seek", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handleSeek(httptest.NewRecorder(), req)
	if fc.seekOffsetMs != 0 {
		t.Errorf("clamped offset = %d, want 0", fc.seekOffsetMs)
	}
}

func TestSeek_NonInteger_400(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, Duration: 10 * time.Minute})
	a := newTestAdapter(t, fc)
	body := strings.NewReader("offset_ms=abc")
	req := httptest.NewRequest(http.MethodPost, "/seek", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleSeek(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if fc.seekCalled {
		t.Error("SeekTo must not be called on parse failure")
	}
}

func TestSeek_DurationZero_409(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   0, // unseekable source
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	body := strings.NewReader("offset_ms=1000")
	req := httptest.NewRequest(http.MethodPost, "/seek", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleSeek(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if fc.seekCalled {
		t.Error("SeekTo must not be called when Duration == 0")
	}
}

func TestSeek_ForeignAdapterRef_409(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   10 * time.Minute,
		AdapterRef: "plex:xyz",
	})
	a := newTestAdapter(t, fc)
	body := strings.NewReader("offset_ms=120000")
	req := httptest.NewRequest(http.MethodPost, "/seek", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleSeek(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if fc.seekCalled {
		t.Error("SeekTo() must NOT be called for foreign session")
	}
	if !strings.Contains(w.Body.String(), "another adapter") {
		t.Errorf("body should mention cross-adapter conflict: %s", w.Body.String())
	}
}

func TestResume_PlayProbeError_RedactsURL(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePaused,
		Duration:   10 * time.Minute,
		AdapterRef: "url:abc",
	})
	// Force core.Play() to return an error whose body contains the URL
	// with credentials. Mimics ffprobe stderr leaking through.
	fc.playErr = errors.New("probe failed: ffprobe https://user:secret@host/v.mp4 ...")
	a := newTestAdapter(t, fc)
	a.markRunning("https://user:secret@host/v.mp4") // sets a.lastURL
	w := postEmpty(a.handleResume)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "secret") {
		t.Errorf("password leaked into error response: %s", body)
	}
	if !strings.Contains(body, "host") {
		t.Errorf("error response should still mention host: %s", body)
	}
}

func TestSeek_ProbeError_RedactsURL(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   10 * time.Minute,
		AdapterRef: "url:abc",
	})
	fc.seekErr = errors.New("probe failed: ffprobe https://user:secret@host/v.mp4 ...")
	a := newTestAdapter(t, fc)
	a.markRunning("https://user:secret@host/v.mp4")
	body := strings.NewReader("offset_ms=120000")
	req := httptest.NewRequest(http.MethodPost, "/seek", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleSeek(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	resp := w.Body.String()
	if strings.Contains(resp, "secret") {
		t.Errorf("password leaked into error response: %s", resp)
	}
	if !strings.Contains(resp, "host") {
		t.Errorf("error response should still mention host: %s", resp)
	}
}
