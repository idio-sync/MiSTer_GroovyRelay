package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeCompanionSession struct {
	status core.SessionStatus
}

func (f fakeCompanionSession) Status() core.SessionStatus { return f.status }

type fakeCompanionURL struct {
	history         []CompanionHistoryEntry
	lastDisplay     string
	playFn          func(context.Context, string, string) (CompanionPlayResult, error)
	pauseFn         func(context.Context) error
	resumeFn        func(context.Context) error
	stopFn          func(context.Context) error
	replayFn        func(context.Context) (CompanionPlayResult, error)
	seekFn          func(context.Context, int) error
	historyPlayFn   func(context.Context, string) (CompanionPlayResult, error)
	historyDeleteFn func(context.Context, string) error
}

func (f fakeCompanionURL) CompanionHistory() []CompanionHistoryEntry {
	return f.history
}
func (f fakeCompanionURL) CompanionLastURLDisplay() string { return f.lastDisplay }
func (f fakeCompanionURL) CompanionPlay(ctx context.Context, rawURL, mode string) (CompanionPlayResult, error) {
	if f.playFn != nil {
		return f.playFn(ctx, rawURL, mode)
	}
	return CompanionPlayResult{}, errors.New("unused")
}
func (f fakeCompanionURL) CompanionPause(ctx context.Context) error {
	if f.pauseFn != nil {
		return f.pauseFn(ctx)
	}
	return errors.New("unused")
}
func (f fakeCompanionURL) CompanionResume(ctx context.Context) error {
	if f.resumeFn != nil {
		return f.resumeFn(ctx)
	}
	return errors.New("unused")
}
func (f fakeCompanionURL) CompanionStop(ctx context.Context) error {
	if f.stopFn != nil {
		return f.stopFn(ctx)
	}
	return errors.New("unused")
}
func (f fakeCompanionURL) CompanionReplay(ctx context.Context) (CompanionPlayResult, error) {
	if f.replayFn != nil {
		return f.replayFn(ctx)
	}
	return CompanionPlayResult{}, errors.New("unused")
}
func (f fakeCompanionURL) CompanionSeek(ctx context.Context, offsetMs int) error {
	if f.seekFn != nil {
		return f.seekFn(ctx, offsetMs)
	}
	return errors.New("unused")
}
func (f fakeCompanionURL) CompanionHistoryPlay(ctx context.Context, id string) (CompanionPlayResult, error) {
	if f.historyPlayFn != nil {
		return f.historyPlayFn(ctx, id)
	}
	return CompanionPlayResult{}, errors.New("unused")
}
func (f fakeCompanionURL) CompanionHistoryDelete(ctx context.Context, id string) error {
	if f.historyDeleteFn != nil {
		return f.historyDeleteFn(ctx, id)
	}
	return errors.New("unused")
}

type fakeCompanionHTTPError struct {
	status int
	msg    string
}

func (e fakeCompanionHTTPError) Error() string   { return e.msg }
func (e fakeCompanionHTTPError) HTTPStatus() int { return e.status }

type fakeCompanionDisplay struct {
	display CompanionSessionDisplay
}

func (f fakeCompanionDisplay) CompanionDisplay(string) CompanionSessionDisplay {
	return f.display
}

func newCompanionRouteServer(t *testing.T, source fakeCompanionURL) (*Server, *http.ServeMux) {
	t.Helper()
	return newCompanionRouteServerWithLauncher(t, source, nil)
}

func newCompanionRouteServerWithLauncher(t *testing.T, source fakeCompanionURL, launcher MisterLauncher) (*Server, *http.ServeMux) {
	t.Helper()
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry: reg,
		CompanionSession: fakeCompanionSession{status: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "url:abc",
		}},
		CompanionURL:   source,
		MisterLauncher: launcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return s, mux
}

func companionJSONRequest(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	return rw
}

func TestCompanionGateRequiresExtensionOriginAndHeader(t *testing.T) {
	h := companionExtensionGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cases := []struct {
		name   string
		origin string
		header string
		want   int
	}{
		{"missing both", "", "", http.StatusForbidden},
		{"origin only", "moz-extension://abc", "", http.StatusForbidden},
		{"header only no origin", "", "1", http.StatusOK},
		{"web origin", "https://evil.example", "1", http.StatusForbidden},
		{"extension pair", "moz-extension://abc", "1", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.header != "" {
				req.Header.Set("X-Bridge-Extension", tc.header)
			}
			rw := httptest.NewRecorder()
			h.ServeHTTP(rw, req)
			if rw.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rw.Code, tc.want, rw.Body.String())
			}
			if tc.want == http.StatusForbidden && !strings.Contains(rw.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("forbidden content type = %q, want JSON", rw.Header().Get("Content-Type"))
			}
		})
	}
}

func TestCompanionGatePreflightUsesExtensionCORS(t *testing.T) {
	h := companionExtensionGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("preflight should not reach route")
	}))
	req := httptest.NewRequest(http.MethodOptions, "/ui/companion/status", nil)
	req.Header.Set("Origin", "chrome-extension://abc")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rw.Code)
	}
	if got := rw.Header().Get("Access-Control-Allow-Origin"); got != "chrome-extension://abc" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rw.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Fatalf("Access-Control-Allow-Private-Network = %q", got)
	}
}

func TestCompanionStatusURLSessionIncludesCapabilitiesAndHistory(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry: reg,
		CompanionSession: fakeCompanionSession{status: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "url:abc",
			Position:   90 * time.Second,
			Duration:   3 * time.Minute,
			StartedAt:  time.Date(2026, 5, 9, 1, 2, 3, 0, time.UTC),
		}},
		CompanionURL: fakeCompanionURL{
			lastDisplay: "example.com/video.mp4",
			history: []CompanionHistoryEntry{{
				ID:         "h_7f4c9e2b8a1d4c0aa9d3e6f124b8c2d1",
				Title:      "Example",
				URLDisplay: "example.com/video.mp4",
				LastPlayed: time.Date(2026, 5, 9, 1, 2, 3, 0, time.UTC),
			}},
		},
		CompanionDisplay: fakeCompanionDisplay{display: CompanionSessionDisplay{
			AdapterName:   "URL",
			Title:         "Example",
			SourceDisplay: "example.com/video.mp4",
			ResolvedVia:   "direct",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	session := got["session"].(map[string]any)
	if session["state"] != "playing" {
		t.Fatalf("state = %v, want playing", session["state"])
	}
	if session["adapter_name"] != "URL" || session["title"] != "Example" {
		t.Fatalf("session display = %#v", session)
	}
	if session["position_ms"] != float64(90000) || session["duration_ms"] != float64(180000) {
		t.Fatalf("session timing = %#v", session)
	}
	caps := session["capabilities"].(map[string]any)
	if caps["can_pause"] != true || caps["can_seek"] != true || caps["can_resume"] != false {
		t.Fatalf("capabilities = %#v", caps)
	}
	history := got["history"].([]any)
	first := history[0].(map[string]any)
	if first["id"] != "h_7f4c9e2b8a1d4c0aa9d3e6f124b8c2d1" {
		t.Fatalf("history id = %v", first["id"])
	}
	if first["url_display"] != "example.com/video.mp4" {
		t.Fatalf("history url_display = %v", first["url_display"])
	}
}

// TestCompanionStatusURLSessionUnknownDurationDisablesSeek covers the
// capability rule at spec line 256: can_seek is true only when duration
// is known. Live streams (Duration == 0) must surface can_seek=false even
// while playing or paused, so the popup hides its scrub bar instead of
// rendering a misleading 0/0 control.
func TestCompanionStatusURLSessionUnknownDurationDisablesSeek(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry: reg,
		CompanionSession: fakeCompanionSession{status: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "url:live",
			Position:   30 * time.Second,
			// Duration intentionally zero — live stream / unknown.
		}},
		CompanionURL: fakeCompanionURL{lastDisplay: "live.example.com/feed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	session := got["session"].(map[string]any)
	if session["state"] != "playing" {
		t.Fatalf("state = %v, want playing", session["state"])
	}
	if session["duration_ms"] != float64(0) {
		t.Fatalf("duration_ms = %v, want 0", session["duration_ms"])
	}
	caps := session["capabilities"].(map[string]any)
	if caps["can_seek"] != false {
		t.Fatalf("can_seek = %v, want false (Duration == 0)", caps["can_seek"])
	}
	if caps["can_pause"] != true || caps["can_stop"] != true {
		t.Fatalf("non-seek caps lost when Duration == 0: %#v", caps)
	}
}

func TestCompanionStatusForeignSessionReadOnly(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry: reg,
		CompanionSession: fakeCompanionSession{status: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "plex:machine",
		}},
		CompanionURL: fakeCompanionURL{},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
	req.Header.Set("Origin", "chrome-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	session := got["session"].(map[string]any)
	if session["adapter_name"] != "Plex" {
		t.Fatalf("adapter_name = %v, want Plex", session["adapter_name"])
	}
	caps := session["capabilities"].(map[string]any)
	for k, v := range caps {
		if v != false {
			t.Fatalf("foreign capability %s = %v, want false", k, v)
		}
	}
}

func TestCompanionPlayRouteReturns202AndPlaying(t *testing.T) {
	var gotURL, gotMode string
	_, mux := newCompanionRouteServer(t, fakeCompanionURL{
		playFn: func(ctx context.Context, rawURL, mode string) (CompanionPlayResult, error) {
			gotURL, gotMode = rawURL, mode
			return CompanionPlayResult{AdapterRef: "url:abc", ResolvedVia: "direct"}, nil
		},
	})
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/play", `{"url":"https://example.com/v.mp4","mode":"auto"}`)
	if rw.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rw.Code, rw.Body.String())
	}
	if gotURL != "https://example.com/v.mp4" || gotMode != "auto" {
		t.Fatalf("called with url=%q mode=%q", gotURL, gotMode)
	}
	if strings.Contains(rw.Body.String(), `"url"`) {
		t.Fatalf("companion play response leaked url: %s", rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), `"state":"playing"`) {
		t.Fatalf("response missing playing state: %s", rw.Body.String())
	}
}

func TestCompanionPlayRouteRequiresJSONContentType(t *testing.T) {
	_, mux := newCompanionRouteServer(t, fakeCompanionURL{})
	req := httptest.NewRequest(http.MethodPost, "/ui/companion/play", strings.NewReader(`{"url":"https://example.com"}`))
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rw.Code)
	}
}

func TestCompanionControlSeekDispatchesAbsoluteOffset(t *testing.T) {
	var gotOffset int
	_, mux := newCompanionRouteServer(t, fakeCompanionURL{
		seekFn: func(ctx context.Context, offsetMs int) error {
			gotOffset = offsetMs
			return nil
		},
	})
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/control", `{"action":"seek","offset_ms":90000}`)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	if gotOffset != 90000 {
		t.Fatalf("offset = %d, want absolute 90000", gotOffset)
	}
}

func TestCompanionHistoryPlayMissingIDReturns404(t *testing.T) {
	_, mux := newCompanionRouteServer(t, fakeCompanionURL{
		historyPlayFn: func(ctx context.Context, id string) (CompanionPlayResult, error) {
			return CompanionPlayResult{}, fakeCompanionHTTPError{status: http.StatusNotFound, msg: "history entry no longer exists"}
		},
	})
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/history/play", `{"id":"h_00000000000000000000000000000000"}`)
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rw.Code)
	}
}

func TestCompanionLaunchEmptyBodyNoContentTypeAccepted(t *testing.T) {
	launcher := &fakeMisterLauncher{}
	_, mux := newCompanionRouteServerWithLauncher(t, fakeCompanionURL{}, launcher)
	req := httptest.NewRequest(http.MethodPost, "/ui/companion/launch", nil)
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	if !launcher.called {
		t.Fatalf("launcher was not called")
	}
}

type fakeCompanionVolume struct{ level int }

func (f *fakeCompanionVolume) OutputVolume() int            { return f.level }
func (f *fakeCompanionVolume) SaveOutputVolume(v int) error { f.level = v; return nil }

func TestCompanionVolumeSetsAndValidates(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	saver := &fakeCompanionVolume{}
	s, err := New(Config{Registry: reg, CompanionVolumeSaver: saver})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	// happy path
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/volume", `{"output_volume":42}`)
	if rw.Code != http.StatusOK {
		t.Fatalf("ok status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	if saver.level != 42 {
		t.Fatalf("saved = %d, want 42", saver.level)
	}

	// out of range
	if rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/volume", `{"output_volume":150}`); rw.Code != http.StatusBadRequest {
		t.Fatalf("range status = %d, want 400", rw.Code)
	}

	// missing field
	if rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/volume", `{}`); rw.Code != http.StatusBadRequest {
		t.Fatalf("missing status = %d, want 400", rw.Code)
	}
}

func TestCompanionVolumeRejectsNonExtension(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, _ := New(Config{Registry: reg, CompanionVolumeSaver: &fakeCompanionVolume{}})
	mux := http.NewServeMux()
	s.Mount(mux)

	// No extension Origin / header — must be rejected by the gate.
	req := httptest.NewRequest(http.MethodPost, "/ui/companion/volume", strings.NewReader(`{"output_volume":10}`))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rw.Code)
	}
}

func TestCompanionStatusIncludesOutputVolume(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry:              reg,
		CompanionVolumeViewer: &fakeCompanionVolume{level: 73},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	rw := companionJSONRequest(t, mux, http.MethodGet, "/ui/companion/status", "")
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var body struct {
		OutputVolume int `json:"output_volume"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.OutputVolume != 73 {
		t.Fatalf("output_volume = %d, want 73", body.OutputVolume)
	}
}
