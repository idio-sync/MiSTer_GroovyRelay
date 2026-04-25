package url

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// fakeCore captures the most recent StartSession call so tests can
// assert what the adapter passed.
type fakeCore struct {
	mu       sync.Mutex
	lastReq  core.SessionRequest
	startErr error
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastReq = req
	return f.startErr
}
func (f *fakeCore) Status() core.SessionStatus { return core.SessionStatus{} }

func (f *fakeCore) snapshot() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

func TestPlay_RejectsMalformedURL(t *testing.T) {
	a := New(&fakeCore{})
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=not%20a%20valid%20url"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlay_RejectsEmptyURL(t *testing.T) {
	a := New(&fakeCore{})
	req := httptest.NewRequest(http.MethodPost, "/play", strings.NewReader("url="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlay_RejectsBadScheme(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"rtsp://10.0.0.1/stream",
		"ftp://example.com/v.mp4",
		"javascript:alert(1)",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			fc := &fakeCore{}
			a := New(fc)
			req := httptest.NewRequest(http.MethodPost, "/play",
				strings.NewReader("url="+in))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			a.handlePlay(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if got := fc.snapshot().StreamURL; got != "" {
				t.Errorf("StartSession called despite bad scheme: %q", got)
			}
		})
	}
}

func TestPlay_HappyPath_BuildsSessionRequest(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fvideo.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	got := fc.snapshot()
	if got.StreamURL != "https://example.com/video.mp4" {
		t.Errorf("StreamURL = %q", got.StreamURL)
	}
	if got.Capabilities.CanPause || got.Capabilities.CanSeek {
		t.Errorf("Capabilities should be {false,false}, got %+v", got.Capabilities)
	}
	if got.DirectPlay {
		t.Errorf("DirectPlay should be false in v1")
	}
	if !strings.HasPrefix(got.AdapterRef, "url:") {
		t.Errorf("AdapterRef should start with 'url:', got %q", got.AdapterRef)
	}
	if got.OnStop == nil {
		t.Errorf("OnStop should be set")
	}
}

func TestPlay_StartSessionFailure_500(t *testing.T) {
	fc := &fakeCore{startErr: errors.New("probe failed")}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if a.Status().State != adapters.StateError {
		t.Errorf("State = %v, want StateError", a.Status().State)
	}
}

func TestPlay_HXRequest_RespondsHTML(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "example.com") {
		t.Errorf("response body should mention the URL: %s", w.Body.String())
	}
}

func TestPlay_NoHXRequest_RespondsJSON(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"adapter_ref"`) || !strings.Contains(body, `"state":"running"`) {
		t.Errorf("JSON body missing expected keys: %s", body)
	}
}

func TestPlay_AcceptsJSONBody(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	body := `{"url": "https://example.com/v.mp4"}`
	req := httptest.NewRequest(http.MethodPost, "/play", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if got := fc.snapshot().StreamURL; got != "https://example.com/v.mp4" {
		t.Errorf("StreamURL = %q", got)
	}
}

func TestRedactURL_StripsCredentials(t *testing.T) {
	got := redactURL("https://user:secret@example.com/v.mp4")
	if strings.Contains(got, "secret") {
		t.Errorf("password leaked: %q", got)
	}
	if !strings.Contains(got, "example.com") {
		t.Errorf("host stripped too: %q", got)
	}
}

func TestRedactURL_HandlesUnparseable(t *testing.T) {
	// Even on a parse failure the redactor must not panic and must not
	// echo arbitrary input verbatim.
	got := redactURL("\x00not-a-url")
	if got == "" {
		t.Error("redactURL returned empty for invalid input")
	}
}

func TestOnStop_ReasonHandling(t *testing.T) {
	cases := []struct {
		reason string
		want   adapters.State
	}{
		{"eof", adapters.StateStopped},
		{"preempted", adapters.StateStopped},
		{"stopped", adapters.StateStopped},
		{"", adapters.StateStopped}, // empty treated as eof
		{"error: ffmpeg crashed", adapters.StateError},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			a := New(nil)
			// Pretend a session is running.
			a.setState(adapters.StateRunning, "")
			a.handleOnStop(tc.reason)
			if got := a.Status().State; got != tc.want {
				t.Errorf("after OnStop(%q), State = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}
