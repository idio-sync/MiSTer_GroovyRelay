package url

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
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

// fakeCore captures all SessionManager calls so tests can assert what
// the adapter passed and what it called. Per-method error fields let
// tests force failures.
type fakeCore struct {
	mu sync.Mutex

	// StartSession
	lastReq  core.SessionRequest
	startErr error

	// Status (default: zero-value SessionStatus). Override via statusFn
	// to return state-specific status from inside a test.
	statusFn func() core.SessionStatus

	// Pause / Play / Stop
	pauseErr    error
	playErr     error
	stopErr     error
	pauseCalled bool
	playCalled  bool
	stopCalled  bool

	// SeekTo
	seekErr      error
	seekCalled   bool
	seekOffsetMs int
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastReq = req
	return f.startErr
}

func (f *fakeCore) StartSessionIfSession(req core.SessionRequest, ref string, generation uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastReq = req
	return true, f.startErr
}

func (f *fakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	fn := f.statusFn
	f.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return core.SessionStatus{}
}

func (f *fakeCore) Pause() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauseCalled = true
	return f.pauseErr
}

func (f *fakeCore) PauseIfSession(string, uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauseCalled = true
	return true, f.pauseErr
}

func (f *fakeCore) Play() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playCalled = true
	return f.playErr
}

func (f *fakeCore) PlayIfSession(string, uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playCalled = true
	return true, f.playErr
}

func (f *fakeCore) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalled = true
	return f.stopErr
}

func (f *fakeCore) StopIfSession(string, uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalled = true
	return true, f.stopErr
}

func (f *fakeCore) SeekTo(offsetMs int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seekCalled = true
	f.seekOffsetMs = offsetMs
	return f.seekErr
}

func (f *fakeCore) SeekToIfSession(_ string, _ uint64, offsetMs int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seekCalled = true
	f.seekOffsetMs = offsetMs
	return true, f.seekErr
}

func (f *fakeCore) snapshot() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

type fakeStreamResolver struct {
	matched      bool
	res          streamhandoff.Resolution
	err          error
	startErr     error
	starts       int
	guardStarts  int
	guardRef     string
	guardGen     uint64
	guardMatched bool
}

func (f *fakeStreamResolver) ResolveStreamURL(ctx context.Context, rawURL string) (streamhandoff.Resolution, bool, error) {
	return f.res, f.matched, f.err
}

func (f *fakeStreamResolver) StartResolvedStream(ctx context.Context, res streamhandoff.Resolution) (streamhandoff.StartResult, error) {
	f.starts++
	if f.startErr != nil {
		return streamhandoff.StartResult{}, f.startErr
	}
	return streamhandoff.StartResult{
		AdapterRef: res.AdapterRef,
		ProviderID: res.ProviderID,
		ChannelID:  res.ChannelID,
		ItemID:     res.ItemID,
	}, nil
}

func (f *fakeStreamResolver) StartResolvedStreamIfSession(ctx context.Context, res streamhandoff.Resolution, ref string, gen uint64) (streamhandoff.StartResult, bool, error) {
	f.guardStarts++
	f.guardRef, f.guardGen = ref, gen
	if f.startErr != nil {
		return streamhandoff.StartResult{}, false, f.startErr
	}
	return streamhandoff.StartResult{
		AdapterRef: res.AdapterRef,
		ProviderID: res.ProviderID,
		ChannelID:  res.ChannelID,
		ItemID:     res.ItemID,
	}, f.guardMatched, nil
}

func TestCastURL_StreamsHandoffBeforeYTDLP(t *testing.T) {
	fr := &fakeResolver{res: &ytdlp.Resolution{URL: "https://resolved.example/video.mp4"}}
	a := newAdapterWithResolver(t, fr)
	a.cfg.YtdlpHosts = append(a.cfg.YtdlpHosts, "wantmymtv.vercel.app")
	f := &fakeStreamResolver{
		matched: true,
		res: streamhandoff.Resolution{
			AdapterRef: "streams:1",
			ProviderID: "mtv-rewind",
			ChannelID:  "metal",
		},
	}
	a.SetStreamResolver(f)

	ref, via, status, err := a.castURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal", "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if ref != "streams:1" || via != "streams" || status != http.StatusOK || f.starts != 1 {
		t.Fatalf("ref=%q via=%q status=%d starts=%d", ref, via, status, f.starts)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("yt-dlp resolver should not be called for streams handoff: %v", fr.calls)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != "" {
		t.Fatalf("URL core should not be started for streams handoff; StreamURL=%q", got)
	}
}

func TestCastURLGuardedStreamsResolverUsesSessionGuard(t *testing.T) {
	fc := &fakeCore{}
	fc.statusFn = func() core.SessionStatus {
		return core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:old", Generation: 7}
	}
	a := newTestAdapter(t, fc)
	f := &fakeStreamResolver{
		matched:      true,
		res:          streamhandoff.Resolution{AdapterRef: "streams:mtv:metal:sess:1", ProviderID: "mtv", ChannelID: "metal"},
		guardMatched: false,
	}
	a.SetStreamResolver(f)
	_, _, status, err := a.castURLGuarded(context.Background(), "https://wantmymtv.vercel.app/player.html?channel=metal", "auto", "url:old", 7)
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("castURLGuarded stream err = %v, want active session changed", err)
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if f.starts != 0 {
		t.Fatalf("unguarded stream start was called %d times", f.starts)
	}
	if f.guardStarts != 1 || f.guardRef != "url:old" || f.guardGen != 7 {
		t.Fatalf("guarded stream key = starts:%d ref:%q gen:%d", f.guardStarts, f.guardRef, f.guardGen)
	}
}

func TestCastURLGuardedRejectsStaleBeforeSideEffects(t *testing.T) {
	log := eventlog.New(16)
	coreStub := &providerCoreStub{
		status:       core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:new", Generation: 8},
		startMatched: false,
	}
	fr := &fakeResolver{res: &ytdlp.Resolution{URL: "https://cdn.example/video.mp4"}}
	a := newTestAdapterOpts(t, withCore(coreStub), withEventLog(log))
	a.cfg.Enabled = true
	a.cfg.YtdlpEnabled = true
	a.cfg.YtdlpHosts = []string{"youtu.be"}
	a.cfg.YtdlpFormat = "best"
	a.resolver = fr
	a.ytdlpProbe = ytdlpProbe{OK: true}

	_, _, status, err := a.castURLGuarded(context.Background(), "https://youtu.be/abc", "ytdlp", "url:old", 7)
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("castURLGuarded err = %v, want active session changed", err)
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if got := a.history.Len(); got != 0 {
		t.Fatalf("history Len = %d, want 0 for stale action", got)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("yt-dlp resolver was called before stale guard: %#v", fr.calls)
	}
	if entries := log.Snapshot(); len(entries) != 0 {
		t.Fatalf("event log entries = %#v, want none for stale action", entries)
	}
	if coreStub.startReq.StreamURL != "" {
		t.Fatalf("StartSessionIfSession request = %#v, want no guarded start before stale guard", coreStub.startReq)
	}
}

func TestCastURL_StreamsNoMatchFallsBack(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	f := &fakeStreamResolver{matched: false}
	a.SetStreamResolver(f)

	ref, via, status, err := a.castURL(t.Context(), "https://example.com/video.mp4", "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if ref == "" || via != "direct" || status != http.StatusOK || f.starts != 0 {
		t.Fatalf("ref=%q via=%q status=%d starts=%d", ref, via, status, f.starts)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != "https://example.com/video.mp4" {
		t.Fatalf("fallback StreamURL = %q", got)
	}
}

func TestCastURL_StreamsResolveErrorDoesNotFallBack(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	f := &fakeStreamResolver{
		matched: true,
		err:     errors.New("streams link is invalid"),
	}
	a.SetStreamResolver(f)

	_, _, status, err := a.castURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=", "auto")
	if err == nil {
		t.Fatal("castURL should return streams resolver error")
	}
	if status != http.StatusBadRequest || f.starts != 0 {
		t.Fatalf("status=%d starts=%d, want 400 and no start", status, f.starts)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != "" {
		t.Fatalf("URL core should not be started after streams resolve error; StreamURL=%q", got)
	}
}

func TestCastURL_StreamsStartErrorDoesNotFallBack(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	f := &fakeStreamResolver{
		matched: true,
		res: streamhandoff.Resolution{
			ProviderID: "mtv-rewind",
			ChannelID:  "metal",
		},
		startErr: errors.New("streams adapter is disabled"),
	}
	a.SetStreamResolver(f)

	_, _, status, err := a.castURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal", "auto")
	if err == nil {
		t.Fatal("castURL should return streams start error")
	}
	if status != http.StatusBadRequest || f.starts != 1 {
		t.Fatalf("status=%d starts=%d, want 400 and one streams start", status, f.starts)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != "" {
		t.Fatalf("URL core should not be started after streams start error; StreamURL=%q", got)
	}
}

func TestCastURL_OwncastHomepageResolvesToHLS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Fatalf("unexpected probe path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"serverTime":"2026-05-14T00:00:00Z","versionNumber":"0.2.3","online":true}`))
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})

	ref, via, status, err := a.castURL(t.Context(), server.URL+"/", "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if ref == "" || via != "direct" || status != http.StatusOK {
		t.Fatalf("ref=%q via=%q status=%d", ref, via, status)
	}
	if got, want := a.core.(*fakeCore).snapshot().StreamURL, server.URL+"/hls/stream.m3u8"; got != want {
		t.Fatalf("StreamURL = %q, want %q", got, want)
	}
	if got, want := a.snapshotLastURL(), server.URL+"/"; got != want {
		t.Fatalf("lastURL = %q, want operator-submitted URL %q", got, want)
	}
}

func TestCastURL_OwncastDirectHLSIsUntouched(t *testing.T) {
	probes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes++
		http.Error(w, "should not probe direct hls", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})
	raw := server.URL + "/hls/stream.m3u8"

	_, _, _, err := a.castURL(t.Context(), raw, "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != raw {
		t.Fatalf("StreamURL = %q, want %q", got, raw)
	}
	if probes != 0 {
		t.Fatalf("Owncast probe count = %d, want 0", probes)
	}
}

func TestCastURL_NonOwncastHomepageFallsThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"not owncast"}`))
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})
	raw := server.URL + "/"

	_, _, _, err := a.castURL(t.Context(), raw, "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != raw {
		t.Fatalf("StreamURL = %q, want %q", got, raw)
	}
}

func TestCastURL_OwncastProbeFailureFallsThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})
	raw := server.URL + "/"

	_, _, _, err := a.castURL(t.Context(), raw, "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != raw {
		t.Fatalf("StreamURL = %q, want %q", got, raw)
	}
}

func TestCastURL_OwncastForcedDirectStillResolves(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"serverTime":"2026-05-14T00:00:00Z","versionNumber":"0.2.3","online":false}`))
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})

	_, _, _, err := a.castURL(t.Context(), server.URL+"/", "direct")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got, want := a.core.(*fakeCore).snapshot().StreamURL, server.URL+"/hls/stream.m3u8"; got != want {
		t.Fatalf("StreamURL = %q, want %q", got, want)
	}
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
	if !got.Capabilities.CanPause || !got.Capabilities.CanSeek {
		t.Errorf("Capabilities should be {true,true} in v1.5, got %+v", got.Capabilities)
	}
	if !got.DirectPlay {
		t.Errorf("DirectPlay should be true in v1.5 (spec §Capability and DirectPlay flips)")
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

func TestURLDirectM3U8UsesBufferByDefault(t *testing.T) {
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)
	enableURLHLSBufferForTest(a)
	var gotOpts hlsbuffer.SessionOptions
	var closeCalls int
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		gotOpts = opts
		return &hlsbuffer.Session{
			PlaybackPath: "/tmp/url-buffered.m3u8",
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
			Close: func() error {
				closeCalls++
				return nil
			},
		}, nil
	}

	ref, via, status, err := a.castURL(t.Context(), "https://public.example/live.m3u8", "direct")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if ref == "" || via != "direct" || status != http.StatusOK {
		t.Fatalf("ref=%q via=%q status=%d", ref, via, status)
	}
	if gotOpts.SourceURL != "https://public.example/live.m3u8" {
		t.Fatalf("SourceURL = %q", gotOpts.SourceURL)
	}
	if gotOpts.TrustMode != hlsbuffer.TrustModeGenericPublic {
		t.Fatalf("TrustMode = %v, want generic public", gotOpts.TrustMode)
	}
	req := a.core.(*fakeCore).snapshot()
	if req.StreamURL != "/tmp/url-buffered.m3u8" {
		t.Fatalf("StreamURL = %q, want buffered local playlist", req.StreamURL)
	}
	if got := strings.Join(req.MediaInputPolicy.ProtocolWhitelist, ","); got != "file" {
		t.Fatalf("ProtocolWhitelist = %q, want file", got)
	}
	if req.MediaKind != core.MediaKindVideo {
		t.Fatalf("MediaKind = %q, want video", req.MediaKind)
	}
	req.OnStop("stopped")
	if closeCalls != 1 {
		t.Fatalf("buffer Close calls after OnStop = %d, want 1", closeCalls)
	}
}

func TestURLDirectM3U8OffBypassesBuffer(t *testing.T) {
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)
	enableURLHLSBufferForTest(a)
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		t.Fatal("hlsBufferOpen should not be called when hls_buffer=off")
		return nil, nil
	}

	body := strings.NewReader("url=https%3A%2F%2Fpublic.example%2Flive.m3u8&mode=direct&hls_buffer=off")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	got := a.core.(*fakeCore).snapshot()
	if got.StreamURL != "https://public.example/live.m3u8" {
		t.Fatalf("StreamURL = %q, want direct URL", got.StreamURL)
	}
}

func TestURLBufferedM3U8CleansBufferOnStartFailure(t *testing.T) {
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)
	enableURLHLSBufferForTest(a)
	a.core.(*fakeCore).startErr = errors.New("core start failed")
	var closeCalls int
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return &hlsbuffer.Session{
			PlaybackPath: "/tmp/url-buffered.m3u8",
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
			Close: func() error {
				closeCalls++
				return nil
			},
		}, nil
	}

	_, _, _, err := a.castURL(t.Context(), "https://public.example/live.m3u8", "direct")
	if err == nil {
		t.Fatal("castURL error = nil, want start failure")
	}
	if closeCalls != 1 {
		t.Fatalf("buffer Close calls = %d, want 1", closeCalls)
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

func enableURLHLSBufferForTest(a *Adapter) {
	a.bridge.HLSBuffer.Enabled = true
	a.bridge.HLSBuffer.LiveEdgeSegments = 3
	a.bridge.HLSBuffer.StartSegments = 2
	a.bridge.HLSBuffer.MaxCachedSegments = 6
	a.bridge.HLSBuffer.MaxCacheBytes = 268435456
	a.bridge.HLSBuffer.MaxPlaylistBytes = 1048576
	a.bridge.HLSBuffer.MaxSegmentBytes = 52428800
	a.bridge.HLSBuffer.SegmentTimeoutSeconds = 10
	a.bridge.HLSBuffer.PlaylistTimeoutSeconds = 10
	a.bridge.HLSBuffer.MaxVariantHeight = 720
	a.bridge.HLSBuffer.StaleCacheReapHours = 24
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

func TestPlay_SuccessfulResolve_WritesTitleToHistory(t *testing.T) {
	fr := &fakeResolver{
		res: &ytdlp.Resolution{
			URL:   "https://googlevideo.example/v",
			Title: "Big Buck Bunny",
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
	list := a.history.List()
	if len(list) != 1 {
		t.Fatalf("history len = %d, want 1", len(list))
	}
	if list[0].Title != "Big Buck Bunny" {
		t.Errorf("history title = %q, want %q (yt-dlp Title not propagated to history)",
			list[0].Title, "Big Buck Bunny")
	}
}

func TestPlay_ResolverError_DoesNotWriteTitle(t *testing.T) {
	// Failed resolve still records the URL (per spec) but must not
	// pretend it has a title.
	fr := &fakeResolver{err: errors.New("ytdlp: dead URL")}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fyoutu.be%2Fdead&mode=ytdlp")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	list := a.history.List()
	if len(list) != 1 {
		t.Fatalf("failed cast must still record URL; len = %d", len(list))
	}
	if list[0].Title != "" {
		t.Errorf("failed resolve attached a title: %q", list[0].Title)
	}
}

func TestPlay_DirectMode_DoesNotWriteTitle(t *testing.T) {
	// Direct mode bypasses yt-dlp entirely → no metadata, empty title.
	fr := &fakeResolver{}
	a := newAdapterWithResolver(t, fr)

	body := strings.NewReader("url=https%3A%2F%2Fexample.com%2Fraw.mp4&mode=direct")
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	list := a.history.List()
	if len(list) != 1 {
		t.Fatalf("history len = %d, want 1", len(list))
	}
	if list[0].Title != "" {
		t.Errorf("direct-mode entry got a title: %q", list[0].Title)
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

func TestPlay_HappyPath_RecordsHistory(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handlePlay(httptest.NewRecorder(), req)
	if a.history.Len() != 1 {
		t.Errorf("history.Len = %d, want 1 after happy-path cast", a.history.Len())
	}
}

func TestPlay_StartSessionFailure_StillRecordsHistory(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{startErr: errors.New("probe failed")})
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handlePlay(httptest.NewRecorder(), req)
	if a.history.Len() != 1 {
		t.Errorf("history.Len = %d, want 1 (failed casts must record so operator can retry)", a.history.Len())
	}
}

func TestPlay_BadURL_DoesNotRecordHistory(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=not%20a%20valid%20url"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handlePlay(httptest.NewRecorder(), req)
	if a.history.Len() != 0 {
		t.Errorf("history.Len = %d, want 0 (URLs failing validation must not be recorded)", a.history.Len())
	}
}

func TestPlay_RecastSameURL_BumpsNotDuplicates(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	post := func(u string) {
		req := httptest.NewRequest(http.MethodPost, "/play",
			strings.NewReader("url="+u))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		a.handlePlay(httptest.NewRecorder(), req)
	}
	post("https%3A%2F%2Fa%2F")
	post("https%3A%2F%2Fb%2F")
	post("https%3A%2F%2Fa%2F") // recast a → bump to position 0
	if a.history.Len() != 2 {
		t.Errorf("Len = %d, want 2", a.history.Len())
	}
	list := a.history.List()
	if list[0].URL != "https://a/" || list[1].URL != "https://b/" {
		t.Errorf("after recast, list = %v, want [a, b]", list)
	}
}

// ---- functional-options helpers for new F1 tests ----

// testAdapterOption is a functional option for newTestAdapterOpts.
type testAdapterOption func(*AdapterConfig)

func withEventLog(log *eventlog.Log) testAdapterOption {
	return func(cfg *AdapterConfig) { cfg.EventLog = log }
}

func withCore(c SessionManager) testAdapterOption {
	return func(cfg *AdapterConfig) { cfg.Core = c }
}

// newTestAdapterOpts constructs an Adapter from a set of functional options.
// The default Core is a zero-value fakeCore so the play handler can call
// StartSession without panicking.
func newTestAdapterOpts(t *testing.T, opts ...testAdapterOption) *Adapter {
	t.Helper()
	cfg := AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   &fakeCore{},
	}
	for _, o := range opts {
		o(&cfg)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// postPlay is a convenience wrapper: POST the given url.Values to the
// adapter's play handler and return the recorded response.
func postPlay(t *testing.T, a *Adapter, vals neturl.Values) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	return w.Result()
}

func TestPlay_EmitsCastRequested(t *testing.T) {
	log := eventlog.New(16)
	a := newTestAdapterOpts(t, withEventLog(log))
	body := neturl.Values{"url": {"https://example.com/test.mp4"}}
	resp := postPlay(t, a, body)
	if resp.StatusCode >= 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	entries := log.Snapshot()
	if len(entries) == 0 {
		t.Fatal("expected cast-requested entry")
	}
	if !strings.Contains(entries[0].Message, "cast-requested") {
		t.Errorf("Message: %q", entries[0].Message)
	}
	if entries[0].Source != "url" {
		t.Errorf("Source: %q", entries[0].Source)
	}
}

func TestPlay_PopulatesTitle(t *testing.T) {
	core := &fakeCore{}
	a := newTestAdapterOpts(t, withCore(core))
	body := neturl.Values{"url": {"https://example.com/clip.mp4"}}
	postPlay(t, a, body)
	want := "clip.mp4"
	if core.lastReq.Title != want {
		t.Errorf("Title: got %q, want %q", core.lastReq.Title, want)
	}
}

func TestURLMeterOverlayExposesHLSStatsForOwningSession(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.bridge.HLSBuffer.Enabled = true
	a.bridge.HLSBuffer.MaxCachedSegments = 8

	var openedMaxSegments int
	stats := hlsbuffer.Stats{
		CachedSegments:        3,
		CachedMediaDuration:   4500 * time.Millisecond,
		CacheBytes:            2048,
		PlaylistReloadsTotal:  2,
		SegmentDownloadsTotal: 3,
		SelectedVariant:       hlsbuffer.Variant{URI: "live.m3u8?token=secret", Width: 720, Height: 480, Bandwidth: 1500000, Codecs: "avc1.secret"},
		FailureReason:         "fetch /live.m3u8?token=secret failed",
	}
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		openedMaxSegments = opts.Config.MaxCachedSegments
		return &hlsbuffer.Session{
			PlaybackPath: filepath.Join(t.TempDir(), "playlist.m3u8"),
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return stats },
			Close:        func() error { return nil },
		}, nil
	}
	fc.statusFn = func() core.SessionStatus {
		req := fc.snapshot()
		return core.SessionStatus{State: core.StatePlaying, AdapterRef: req.AdapterRef, Generation: 11}
	}

	ref, _, status, err := a.castURLWithHLSBuffer(context.Background(), "https://example.test/live.m3u8", "direct", "auto")
	if err != nil || status != http.StatusOK {
		t.Fatalf("castURLWithHLSBuffer status=%d err=%v", status, err)
	}
	if openedMaxSegments != 8 {
		t.Fatalf("opened MaxCachedSegments = %d, want normalized configured value 8", openedMaxSegments)
	}
	a.mu.Lock()
	a.bridge.HLSBuffer.MaxCachedSegments = 99
	a.mu.Unlock()

	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: ref, Source: "url", Generation: 11}
	if snap.AdapterRef != ref {
		t.Fatalf("core AdapterRef = %q, want %q", snap.AdapterRef, ref)
	}
	overlay, ok := a.MeterOverlay(context.Background(), snap)
	if !ok || overlay.HLS == nil {
		t.Fatalf("MeterOverlay ok=%v overlay=%+v", ok, overlay)
	}
	if overlay.HLS.CachedSegments != 3 || overlay.HLS.MaxCachedSegments != openedMaxSegments {
		t.Fatalf("HLS overlay = %+v", overlay.HLS)
	}
	body, _ := json.Marshal(overlay)
	for _, leak := range []string{"http://", "https://", "://", "/live.m3u8", "token=", "sig=", "secret", "Authorization", "example.test", "avc1"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("overlay leaked %q: %s", leak, body)
		}
	}
}

func TestURLMeterOverlayClearsBeforeBaseOnStopAndClose(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.bridge.HLSBuffer.Enabled = true
	a.bridge.HLSBuffer.MaxCachedSegments = 6
	var order []string
	var baseSawOverlay bool
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return &hlsbuffer.Session{
			PlaybackPath: filepath.Join(t.TempDir(), "playlist.m3u8"),
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{CachedSegments: 1} },
			Close: func() error {
				return nil
			},
		}, nil
	}
	fc.statusFn = func() core.SessionStatus {
		req := fc.snapshot()
		return core.SessionStatus{State: core.StatePlaying, AdapterRef: req.AdapterRef, Generation: 12}
	}
	ref, _, status, err := a.castURLWithHLSBuffer(context.Background(), "https://example.test/live.m3u8", "direct", "auto")
	if err != nil || status != http.StatusOK {
		t.Fatalf("castURLWithHLSBuffer status=%d err=%v", status, err)
	}
	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: ref, Source: "url", Generation: 12}
	if _, ok := a.MeterOverlay(context.Background(), snap); !ok {
		t.Fatal("overlay should be active before stop")
	}
	baseOnStop := func(reason string) {
		order = append(order, "base")
		_, baseSawOverlay = a.MeterOverlay(context.Background(), snap)
	}
	onStop := withHLSBufferCleanup(a.hlsMeterClearingOnStop(ref, baseOnStop), &hlsbuffer.Session{
		Close: func() error {
			order = append(order, "close")
			return nil
		},
	})
	onStop("stopped")
	if baseSawOverlay {
		t.Fatal("overlay should be cleared before base OnStop state mutation")
	}
	if _, ok := a.MeterOverlay(context.Background(), core.StatusHomeView{State: core.StateIdle}); ok {
		t.Fatal("overlay should be cleared after stop")
	}
	if len(order) != 2 || order[0] != "base" || order[1] != "close" {
		t.Fatalf("stop order = %#v, want base before close", order)
	}
	_ = ref
}
