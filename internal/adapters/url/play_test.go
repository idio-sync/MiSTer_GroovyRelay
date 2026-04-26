package url

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// newTestAdapter wires the new AdapterConfig signature for the bulk of
// the play_test cases — they don't care about DataDir, only need a
// constructed adapter with the given core.
func newTestAdapter(t *testing.T, c SessionManager) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   c,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

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
	a := newTestAdapter(t, &fakeCore{})
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
	a := newTestAdapter(t, &fakeCore{})
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
			a := newTestAdapter(t, fc)
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
	a := newTestAdapter(t, fc)
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
	a := newTestAdapter(t, fc)
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
	a := newTestAdapter(t, fc)
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

func TestPlay_HXRequest_RedactsCredentialsInBody(t *testing.T) {
	// A credentialed URL must be redacted in the HTML success fragment
	// shown to the operator (anyone shoulder-surfing the panel would
	// otherwise see the password). The JSON branch echoes the URL
	// verbatim — the API caller already possesses it.
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fuser%3Asecret%40example.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	body := w.Body.String()
	if strings.Contains(body, "secret") {
		t.Errorf("HTMX 202 fragment leaked password: %s", body)
	}
	if !strings.Contains(body, "example.com") {
		t.Errorf("redaction stripped the host too: %s", body)
	}
}

func TestPlay_NoHXRequest_RespondsJSON(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
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
	a := newTestAdapter(t, fc)
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
			a, err := New(AdapterConfig{
				Bridge: config.BridgeConfig{DataDir: t.TempDir()},
				Core:   nil,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			// Pretend a session is running.
			a.setState(adapters.StateRunning, "")
			// makeOnStop("", "") returns the closure that handlePlay
			// would normally produce; calling it with tc.reason
			// exercises the state-transition switch unchanged from
			// the deleted handleOnStop method.
			a.makeOnStop("", "")(tc.reason)
			if got := a.Status().State; got != tc.want {
				t.Errorf("after OnStop(%q), State = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

// fakeResolver is a stub for the ytdlp.Resolver, injected via the
// adapter's resolver field. Records calls; returns canned Resolution.
type fakeResolver struct {
	calls []resolveCall
	res   *ytdlp.Resolution
	err   error
}

type resolveCall struct {
	URL         string
	Format      string
	CookiesPath string
}

func (f *fakeResolver) Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error) {
	f.calls = append(f.calls, resolveCall{pageURL, format, cookiesPath})
	if f.err != nil {
		return nil, f.err
	}
	return f.res, nil
}

func newAdapterWithResolver(t *testing.T, fr resolverIface) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   &fakeCore{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.cfg.Enabled = true
	a.cfg.YtdlpEnabled = true
	// The fixture URLs in the mode-dispatch tests use youtu.be (the
	// short YouTube host); include it alongside youtube.com so the
	// allowlist actually covers them.
	a.cfg.YtdlpHosts = []string{"youtube.com", "youtu.be", "twitch.tv"}
	a.cfg.YtdlpFormat = "best"
	a.cfg.YtdlpResolveTimeoutSeconds = 5
	a.resolver = fr
	a.ytdlpProbe = ytdlpProbe{Path: "/usr/local/bin/yt-dlp", Version: "2026.04.20", OK: true}
	return a
}

func TestPlay_ModeAuto_HostInAllowlist_RoutesToYtdlp(t *testing.T) {
	fr := &fakeResolver{
		res: &ytdlp.Resolution{
			URL:     "https://googlevideo.com/playback?id=resolved",
			Headers: map[string]string{"User-Agent": "Mozilla/5.0"},
			Title:   "Test",
		},
	}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fyoutu.be%2Fabc&mode=auto")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	if len(fr.calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1", len(fr.calls))
	}
	// Manager should have received the resolved URL, not the page URL.
	got := a.core.(*fakeCore).lastReq
	if got.StreamURL != "https://googlevideo.com/playback?id=resolved" {
		t.Errorf("StreamURL = %q, want resolved URL", got.StreamURL)
	}
	if got.InputHeaders["User-Agent"] != "Mozilla/5.0" {
		t.Errorf("InputHeaders not threaded: %v", got.InputHeaders)
	}
}

func TestPlay_ModeAuto_HostNotInAllowlist_GoesDirect(t *testing.T) {
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fexample.com%2Fvideo.mp4&mode=auto")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if len(fr.calls) != 0 {
		t.Errorf("resolver called for non-allowlisted host: %v", fr.calls)
	}
	got := a.core.(*fakeCore).lastReq
	if got.StreamURL != "https://example.com/video.mp4" {
		t.Errorf("StreamURL = %q, want raw URL", got.StreamURL)
	}
}

func TestPlay_ModeYtdlp_AlwaysRoutesThroughResolver(t *testing.T) {
	fr := &fakeResolver{
		res: &ytdlp.Resolution{URL: "https://resolved.example/v.mp4"},
	}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fexample.com%2Fpage&mode=ytdlp")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1 (forced)", len(fr.calls))
	}
}

func TestPlay_ModeDirect_NeverRoutesThroughResolver(t *testing.T) {
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fyoutu.be%2Fabc&mode=direct")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	if len(fr.calls) != 0 {
		t.Errorf("resolver called in direct mode: %v", fr.calls)
	}
}

func TestPlay_ModeYtdlp_WithYtdlpDisabled_Returns400(t *testing.T) {
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)
	a.cfg.YtdlpEnabled = false

	body := strings.NewReader("url=https%3A%2F%2Fexample.com&mode=ytdlp")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPlay_UnknownMode_Returns400(t *testing.T) {
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fexample.com&mode=bogus")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPlay_ModeAbsent_DefaultsToAuto(t *testing.T) {
	fr := &fakeResolver{
		res: &ytdlp.Resolution{URL: "https://resolved.example/v"},
	}
	a := newAdapterWithResolver(t, fr)

	// Form has no mode field; should default to auto.
	body := strings.NewReader("url=https%3A%2F%2Fyoutu.be%2Fxyz")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("auto mode (default) didn't route youtu.be to resolver")
	}
}

func TestPlay_ResolverError_Returns500(t *testing.T) {
	fr := &fakeResolver{err: errors.New("ytdlp: This video is unavailable")}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fyoutu.be%2Fdead&mode=ytdlp")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// IMPORTANT: do NOT set HX-Request. The body assertion below
	// relies on the JSON branch of respondError, which echoes the
	// raw error message verbatim. The HTML fragment branch wraps
	// in a <p class="err"> with HTMLEscapeString — the literal
	// "This video is unavailable" still appears, but a future
	// change to the fragment markup could break this assertion.
	// Keeping the JSON path explicit here pins the contract.
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "This video is unavailable") {
		t.Errorf("body missing stderr line: %s", w.Body.String())
	}
}

func TestPlay_JSONResponse_IncludesResolvedVia(t *testing.T) {
	fr := &fakeResolver{
		res: &ytdlp.Resolution{URL: "https://resolved.example/v"},
	}
	a := newAdapterWithResolver(t, fr)

	// JSON request, mode=auto, allowlisted host.
	req := httptest.NewRequest("POST", "/ui/adapter/url/play",
		strings.NewReader(`{"url":"https://youtu.be/abc","mode":"auto"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if got["resolved_via"] != "ytdlp" {
		t.Errorf("resolved_via = %q, want ytdlp", got["resolved_via"])
	}
}

// TestPlay_ModeAuto_StripsPortFromHost guards against the regression
// where parsed.Host (with :port) was passed to the allowlist matcher.
// "youtube.com:443" doesn't match "youtube.com" via suffix-at-boundary,
// so a paste of an explicit-port URL would silently route to direct
// mode and ffmpeg would fetch the watch-page HTML.
func TestPlay_ModeAuto_StripsPortFromHost(t *testing.T) {
	fr := &fakeResolver{
		res: &ytdlp.Resolution{URL: "https://resolved.example/v.mp4"},
	}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fyoutu.be%3A443%2Fabc&mode=auto")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(fr.calls) != 1 {
		t.Errorf("explicit-port URL did not route to resolver: calls=%d", len(fr.calls))
	}
}

// TestPlay_AcceptsUppercaseMode pins the M2 fix: mode=AUTO/YTDLP/DIRECT
// from a curl user must be accepted (lowercase + trim normalize before
// the dispatch switch).
func TestPlay_AcceptsUppercaseMode(t *testing.T) {
	for _, mode := range []string{"AUTO", "Auto", " auto ", "YTDLP", "ytdlp"} {
		t.Run(mode, func(t *testing.T) {
			fr := &fakeResolver{
				res: &ytdlp.Resolution{URL: "https://resolved.example/v"},
			}
			a := newAdapterWithResolver(t, fr)
			body := strings.NewReader("url=https%3A%2F%2Fyoutu.be%2Fabc&mode=" + mode)
			req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			a.handlePlay(w, req)
			if w.Code != http.StatusAccepted {
				t.Errorf("mode=%q: status = %d, body=%s", mode, w.Code, w.Body.String())
			}
		})
	}
}
