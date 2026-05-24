//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func TestReceiverEndToEnd(t *testing.T) {
	t.Parallel()

	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{UI: config.UIConfig{HTTPPort: 32500}},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	mux := http.NewServeMux()
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/receiver")
	if err != nil {
		t.Fatalf("GET /receiver: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /receiver status = %d, body = %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /receiver body: %v", err)
	}
	for _, want := range []string{
		`<!-- chassis:shell -->`,
		`<!-- chassis:vfd -->`,
		`<!-- chassis:meter -->`,
		`<!-- chassis:transport -->`,
		`<!-- chassis:visualizer-bank -->`,
		`<!-- chassis:source-cluster -->`,
		`<!-- chassis:input-row -->`,
		`<!-- chassis:preset-bank -->`,
		`<!-- chassis:history -->`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("GET /receiver HTML missing marker %q", want)
		}
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse /receiver HTML: %v", err)
	}
	classes := collectClasses(doc)

	for _, want := range []string{
		"vfd",
		"meter-screen",
		"transport-strip",
		"viz-bank",
		"source-cluster",
		"input-section",
		"preset-bank",
		"history-section",
	} {
		if !classes[want] {
			t.Errorf("GET /receiver HTML missing class %q", want)
		}
	}
}

func TestMount_DoesNotShadowUIRoutes(t *testing.T) {
	t.Parallel()

	uiSrv, err := ui.New(ui.Config{
		Registry: adapters.NewRegistry(),
		Version:  "integration-test",
	})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{UI: config.UIConfig{HTTPPort: 32500}},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()

	mux := http.NewServeMux()
	uiSrv.Mount(mux)
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	for _, tc := range []struct {
		path              string
		contentTypePrefix string
		want              []string
		notWant           []string
	}{
		{
			path: "/ui",
			want: []string{`class="gr-shell"`},
		},
		{
			path:              "/ui/static/app.css",
			contentTypePrefix: "text/css",
			want:              []string{".gr-shell {", ".gr-sidebar {"},
			notWant:           []string{`class="gr-shell"`, `<!-- chassis:shell -->`},
		},
		{
			path: "/receiver",
			want: []string{`<!-- chassis:shell -->`},
		},
		{
			path:              "/receiver/static/chassis.css",
			contentTypePrefix: "text/css",
			want:              []string{"body.receiver .meter-screen", "body.receiver .transport-strip"},
			notWant:           []string{`class="gr-shell"`, `<!-- chassis:shell -->`},
		},
	} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s body: %v", tc.path, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close %s body: %v", tc.path, closeErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", tc.path, resp.StatusCode, body)
		}
		if tc.contentTypePrefix != "" {
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.contentTypePrefix) {
				t.Fatalf("GET %s Content-Type = %q, want prefix %q", tc.path, got, tc.contentTypePrefix)
			}
		}
		bodyText := string(body)
		for _, want := range tc.want {
			if !strings.Contains(bodyText, want) {
				t.Fatalf("GET %s body missing %q", tc.path, want)
			}
		}
		for _, notWant := range tc.notWant {
			if strings.Contains(bodyText, notWant) {
				t.Fatalf("GET %s body unexpectedly contained %q", tc.path, notWant)
			}
		}
	}
}

func TestReceiverEvents_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Now(),
		HostIP:    "10.0.0.5",
		// Session=nil — exercises the idle-only path through the real handler.
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/receiver/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	// Read until we observe the initial state + vfd events.
	rdr := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var sawState, sawVfd, sawRetry bool
	for time.Now().Before(deadline) && !(sawState && sawVfd && sawRetry) {
		line, err := rdr.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE stream: %v", err)
		}
		switch {
		case strings.HasPrefix(line, "retry: 3000"):
			sawRetry = true
		case strings.HasPrefix(line, "event: state"):
			sawState = true
		case strings.HasPrefix(line, "event: vfd"):
			sawVfd = true
		}
	}
	if !sawRetry {
		t.Errorf("did not observe retry: 3000 directive")
	}
	if !sawState {
		t.Errorf("did not observe event: state record")
	}
	if !sawVfd {
		t.Errorf("did not observe event: vfd record")
	}
}

func TestReceiverEvents_LivePathReachesClient(t *testing.T) {
	// Exercises the full live-session SSE path: a fake SessionViewer
	// reports core.StatePlaying; the chassis Server (with Mount started
	// so the refresher runs) must emit a vfd event whose payload
	// contains the cast title within the first few records.
	mux := http.NewServeMux()

	// integration-local fake matching the chassis.SessionViewer shape.
	// Defined here (not imported from internal/chassis) because the
	// integration package legitimately depends on both internal/chassis
	// and internal/core — the production cross-import lint exempts
	// _test.go files (Phase 0).
	fake := &fakeIntegrationSession{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "Integration Live Title", Source: "plex",
		Position: 10 * time.Second, Duration: 90 * time.Second,
	}}
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Now(),
		HostIP:    "10.0.0.5",
		Session:   fake,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/receiver/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer resp.Body.Close()

	rdr := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var sawLiveState, sawLiveTitle bool
	for time.Now().Before(deadline) && !(sawLiveState && sawLiveTitle) {
		line, err := rdr.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE stream: %v", err)
		}
		if strings.Contains(line, `"state":"live"`) {
			sawLiveState = true
		}
		if strings.Contains(line, `"title":"Integration Live Title"`) {
			sawLiveTitle = true
		}
	}
	if !sawLiveState {
		t.Errorf("did not observe live state event")
	}
	if !sawLiveTitle {
		t.Errorf("did not observe live title in vfd event payload")
	}
}

// fakeIntegrationSession is the integration-test SessionViewer fake.
// Lives in the integration package; chassis.SessionViewer is satisfied
// structurally via the StatusHomeView() method signature.
type fakeIntegrationSession struct {
	mu   sync.Mutex
	view core.StatusHomeView
}

func (f *fakeIntegrationSession) StatusHomeView() core.StatusHomeView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view
}

func (f *fakeIntegrationSession) set(view core.StatusHomeView) {
	f.mu.Lock()
	f.view = view
	f.mu.Unlock()
}

type fakeIntegrationTransport struct {
	mu       sync.Mutex
	lastReq  adapters.PlaybackActionRequest
	hasReq   bool
	result   adapters.PlaybackActionResult
	err      error
	onAction func(adapters.PlaybackActionRequest)
}

func (f *fakeIntegrationTransport) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	f.mu.Lock()
	f.lastReq = req
	f.hasReq = true
	result := f.result
	err := f.err
	onAction := f.onAction
	f.mu.Unlock()

	if onAction != nil {
		onAction(req)
	}
	return result, err
}

func (f *fakeIntegrationTransport) lastRequest() (adapters.PlaybackActionRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq, f.hasReq
}

type fakeIntegrationTransportViewer struct {
	mu   sync.Mutex
	view adapters.PlaybackBannerAdapterView
	owns bool
}

func (f *fakeIntegrationTransportViewer) PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view, f.owns
}

func newChassisTransportIntegrationServer(t *testing.T, session *fakeIntegrationSession, viewer *fakeIntegrationTransportViewer, controller *fakeIntegrationTransport) *httptest.Server {
	t.Helper()

	cfg := chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Now(),
		HostIP:    "10.0.0.5",
	}
	if session != nil {
		cfg.Session = session
	}
	if viewer != nil {
		cfg.TransportViewer = viewer
	}
	if controller != nil {
		cfg.TransportController = controller
	}
	chassisSrv, err := chassis.New(cfg)
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = chassisSrv.Close() })

	mux := http.NewServeMux()
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func postReceiverTransport(t *testing.T, ts *httptest.Server, path, body, fetchSite string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func assertIntegrationTransportJSONError(t *testing.T, resp *http.Response, status int) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, status, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestReceiverTransport_PostActionDispatchesViaController(t *testing.T) {
	controller := &fakeIntegrationTransport{}
	ts := newChassisTransportIntegrationServer(t, nil, nil, controller)

	resp := postReceiverTransport(t, ts, "/receiver/transport/action", "adapter_ref=plex:abc&generation=42&action=pause", "same-origin")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204; body=%s", resp.StatusCode, body)
	}

	got, ok := controller.lastRequest()
	if !ok {
		t.Fatal("controller did not receive playback action")
	}
	if got.Action != adapters.PlaybackActionPause || got.AdapterRef != "plex:abc" || got.Generation != 42 {
		t.Fatalf("controller request = %+v, want pause plex:abc generation 42", got)
	}
}

func TestReceiverTransport_PostActionStaleGenerationReturns409(t *testing.T) {
	controller := &fakeIntegrationTransport{err: adapters.ErrActiveSessionChanged}
	ts := newChassisTransportIntegrationServer(t, nil, nil, controller)

	resp := postReceiverTransport(t, ts, "/receiver/transport/action", "adapter_ref=plex:abc&generation=42&action=pause", "same-origin")
	assertIntegrationTransportJSONError(t, resp, http.StatusConflict)
}

func TestReceiverTransport_PostSeekDispatchesOffset(t *testing.T) {
	controller := &fakeIntegrationTransport{}
	ts := newChassisTransportIntegrationServer(t, nil, nil, controller)

	resp := postReceiverTransport(t, ts, "/receiver/transport/seek", "adapter_ref=plex:abc&generation=42&offset_ms=12345", "same-origin")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204; body=%s", resp.StatusCode, body)
	}

	got, ok := controller.lastRequest()
	if !ok {
		t.Fatal("controller did not receive playback action")
	}
	if got.Action != adapters.PlaybackActionSeek || got.AdapterRef != "plex:abc" || got.Generation != 42 || got.OffsetMS != 12345 {
		t.Fatalf("controller request = %+v, want seek plex:abc generation 42 offset 12345", got)
	}
}

type receiverTransportEvent struct {
	state string
	err   error
}

func streamReceiverTransportEvents(r io.Reader) <-chan receiverTransportEvent {
	events := make(chan receiverTransportEvent, 8)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(r)
		var name, data string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if name == "transport" {
					var env struct {
						State string `json:"state"`
					}
					if err := json.Unmarshal([]byte(data), &env); err != nil {
						events <- receiverTransportEvent{err: err}
						return
					}
					events <- receiverTransportEvent{state: env.State}
				}
				name, data = "", ""
			}
		}
		if err := scanner.Err(); err != nil {
			events <- receiverTransportEvent{err: err}
		}
	}()
	return events
}

func TestReceiverTransport_SSEReflectsAction(t *testing.T) {
	session := &fakeIntegrationSession{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Integration Live Title",
		Source:     "plex",
		AdapterRef: "plex:abc",
		Generation: 42,
		Position:   10 * time.Second,
		Duration:   90 * time.Second,
	}}
	viewer := &fakeIntegrationTransportViewer{
		owns: true,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{
				{ID: adapters.PlaybackActionPause, Enabled: true},
				{ID: adapters.PlaybackActionStop, Enabled: true},
			},
			Seek: &adapters.PlaybackSeek{Enabled: true, OffsetMS: 10000, DurationMS: 90000},
		},
	}
	controller := &fakeIntegrationTransport{
		onAction: func(req adapters.PlaybackActionRequest) {
			if req.Action != adapters.PlaybackActionPause {
				return
			}
			session.set(core.StatusHomeView{
				State:      core.StatePaused,
				Title:      "Integration Live Title",
				Source:     "plex",
				AdapterRef: "plex:abc",
				Generation: 42,
				Position:   10 * time.Second,
				Duration:   90 * time.Second,
			})
		},
	}
	ts := newChassisTransportIntegrationServer(t, session, viewer, controller)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/receiver/events", nil)
	sseResp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", sseResp.StatusCode)
	}
	events := streamReceiverTransportEvents(sseResp.Body)

	postResp := postReceiverTransport(t, ts, "/receiver/transport/action", "adapter_ref=plex:abc&generation=42&action=pause", "same-origin")
	_ = postResp.Body.Close()
	if postResp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST pause status = %d, want 204", postResp.StatusCode)
	}

	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("SSE stream closed before paused transport event arrived")
			}
			if ev.err != nil {
				t.Fatalf("read transport event: %v", ev.err)
			}
			if ev.state == "paused" {
				return
			}
		case <-timeout:
			t.Fatal(`no transport event with state "paused" within 2s`)
		}
	}
}

func TestReceiverTransport_PostActionRejectsCrossSiteOrMissingFetchSite(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fetchSite string
	}{
		{name: "cross-site", fetchSite: "cross-site"},
		{name: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := &fakeIntegrationTransport{}
			ts := newChassisTransportIntegrationServer(t, nil, nil, controller)

			resp := postReceiverTransport(t, ts, "/receiver/transport/action", "adapter_ref=plex:abc&generation=42&action=pause", tc.fetchSite)
			assertIntegrationTransportJSONError(t, resp, http.StatusForbidden)
		})
	}
}

func TestReceiverTransport_PostActionUnsupportedReturns422(t *testing.T) {
	controller := &fakeIntegrationTransport{err: adapters.UnsupportedPlaybackActionError("pause unavailable")}
	ts := newChassisTransportIntegrationServer(t, nil, nil, controller)

	resp := postReceiverTransport(t, ts, "/receiver/transport/action", "adapter_ref=plex:abc&generation=42&action=pause", "same-origin")
	assertIntegrationTransportJSONError(t, resp, http.StatusUnprocessableEntity)
}

func TestReceiverTransport_PostSeekNonIntegerOffsetReturns400(t *testing.T) {
	controller := &fakeIntegrationTransport{}
	ts := newChassisTransportIntegrationServer(t, nil, nil, controller)

	resp := postReceiverTransport(t, ts, "/receiver/transport/seek", "adapter_ref=plex:abc&generation=42&offset_ms=twelve", "same-origin")
	assertIntegrationTransportJSONError(t, resp, http.StatusBadRequest)
}

func TestReceiverTransport_GetRoutesReturn405(t *testing.T) {
	ts := newChassisTransportIntegrationServer(t, nil, nil, &fakeIntegrationTransport{})

	for _, path := range []string{"/receiver/transport/action", "/receiver/transport/seek"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s body: %v", path, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close %s body: %v", path, closeErr)
		}
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s status = %d, want 405; body=%s", path, resp.StatusCode, body)
		}
	}
}

type chassisVisualizerSaver struct {
	bs *uiserver.BridgeSaver
}

func (s *chassisVisualizerSaver) SaveVisualizerMode(mode string) error {
	_, err := s.bs.SaveVisualizerMode(mode)
	return err
}

type visualizerEvent struct {
	mode string
	err  error
}

func streamVisualizerEvents(r io.Reader) <-chan visualizerEvent {
	events := make(chan visualizerEvent, 8)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(r)
		var name, data string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if name == "visualizer" {
					var env struct {
						Mode string `json:"mode"`
					}
					if err := json.Unmarshal([]byte(data), &env); err != nil {
						events <- visualizerEvent{err: err}
						return
					}
					events <- visualizerEvent{mode: env.Mode}
				}
				name, data = "", ""
			}
		}
		if err := scanner.Err(); err != nil {
			events <- visualizerEvent{err: err}
		}
	}()
	return events
}

func nextVisualizerMode(t *testing.T, events <-chan visualizerEvent, timeout time.Duration) string {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("SSE stream closed before visualizer event arrived")
		}
		if ev.err != nil {
			t.Fatalf("read visualizer event: %v", ev.err)
		}
		return ev.mode
	case <-time.After(timeout):
		t.Fatalf("no visualizer event within %v", timeout)
		return ""
	}
}

func newChassisVisualizerIntegrationServer(t *testing.T) (*httptest.Server, *core.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := fmt.Sprintf(testChassisVisualizerConfig, filepath.ToSlash(dir))
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sec, err := config.LoadSectioned(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatalf("groovynet.NewSender: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	mgr := core.NewManager(sec.Bridge, sender)
	reg := adapters.NewRegistry()
	saver := uiserver.NewBridgeSaver(cfgPath, sec, mgr, reg)
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:           sec.Bridge,
		Manager:          mgr,
		Registry:         reg,
		Version:          "integration-test",
		StartedAt:        time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:           "127.0.0.1",
		Session:          mgr,
		VisualizerViewer: mgr,
		VisualizerSaver:  &chassisVisualizerSaver{bs: saver},
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = chassisSrv.Close() })
	mux := http.NewServeMux()
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, mgr, dir
}

func TestReceiverVisualizer_EndToEnd_PostAndSSEEvent(t *testing.T) {
	ts, mgr, dir := newChassisVisualizerIntegrationServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/receiver/events", nil)
	sseResp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", sseResp.StatusCode)
	}
	events := streamVisualizerEvents(sseResp.Body)
	if got := nextVisualizerMode(t, events, 5*time.Second); got != config.VisualizerModeRetroAnalyzer {
		t.Fatalf("initial visualizer mode = %q, want %q", got, config.VisualizerModeRetroAnalyzer)
	}

	postReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/receiver/visualizer", strings.NewReader("mode=stereo_scope"))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Sec-Fetch-Site", "same-origin")
	postResp, err := ts.Client().Do(postReq)
	if err != nil {
		t.Fatalf("POST visualizer: %v", err)
	}
	_ = postResp.Body.Close()
	if postResp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST status = %d, want 204", postResp.StatusCode)
	}
	if got := nextVisualizerMode(t, events, 5*time.Second); got != config.VisualizerModeStereoScope {
		t.Fatalf("post-save visualizer mode = %q, want %q", got, config.VisualizerModeStereoScope)
	}
	if got := mgr.VisualizerMode(); got != config.VisualizerModeStereoScope {
		t.Fatalf("Manager.VisualizerMode() = %q, want %q", got, config.VisualizerModeStereoScope)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(raw), `mode = "stereo_scope"`) {
		t.Fatalf("config.toml missing saved mode:\n%s", raw)
	}
}

func TestReceiverVisualizer_BlocksCrossSitePost(t *testing.T) {
	ts, mgr, _ := newChassisVisualizerIntegrationServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/receiver/visualizer", strings.NewReader("mode=stereo_scope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST visualizer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403; body=%s", resp.StatusCode, body)
	}
	if got := mgr.VisualizerMode(); got != config.VisualizerModeRetroAnalyzer {
		t.Fatalf("Manager.VisualizerMode() = %q, want %q", got, config.VisualizerModeRetroAnalyzer)
	}
}

func TestReceiverVisualizer_PreviewModeRejected(t *testing.T) {
	ts, _, _ := newChassisVisualizerIntegrationServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/receiver/visualizer", strings.NewReader("mode=radial_spectrum"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST visualizer: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"mode":"radial_spectrum"`) {
		t.Fatalf("body should echo rejected mode; got %s", body)
	}
}

func TestReceiverVisualizer_GetReturns405(t *testing.T) {
	ts, _, _ := newChassisVisualizerIntegrationServer(t)
	resp, err := ts.Client().Get(ts.URL + "/receiver/visualizer")
	if err != nil {
		t.Fatalf("GET visualizer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 405; body=%s", resp.StatusCode, body)
	}
}

const testChassisVisualizerConfig = `
[bridge]
data_dir = "%s"

[bridge.video]
modeline = "NTSC_480i"
interlace_field_order = "tff"
aspect_mode = "auto"
rgb_mode = "rgb888"
lz4_enabled = true

[bridge.audio]
sample_rate = 48000
channels = 2
output_volume = 100

[bridge.mister]
host = "127.0.0.1"
port = 32100
source_port = 32101

[bridge.ui]
http_port = 32500

[bridge.visualizer]
mode = "retro_analyzer"
`

func TestReceiverEvents_DoesNotShadowUIRoutes(t *testing.T) {
	mux := http.NewServeMux()

	uiSrv, err := ui.New(ui.Config{
		Registry: adapters.NewRegistry(),
		Version:  "integration-test",
	})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	uiSrv.Mount(mux)

	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Now(),
		HostIP:    "10.0.0.5",
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// /ui/playback/banner (the existing htmx-polled live banner) is
	// independent of /receiver/events.
	uiResp, err := srv.Client().Get(srv.URL + "/ui/playback/banner")
	if err != nil {
		t.Fatalf("GET /ui/playback/banner: %v", err)
	}
	defer uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK {
		t.Errorf("/ui/playback/banner status = %d, want 200", uiResp.StatusCode)
	}
	if got := uiResp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("/ui/playback/banner Content-Type = %q, want text/html prefix", got)
	}

	// /receiver/events is SSE.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/receiver/events", nil)
	rxResp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer rxResp.Body.Close()
	if got := rxResp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("/receiver/events Content-Type = %q, want text/event-stream", got)
	}
}

func collectClasses(n *html.Node) map[string]bool {
	classes := make(map[string]bool)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				if attr.Key != "class" {
					continue
				}
				for _, className := range strings.Fields(attr.Val) {
					classes[className] = true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return classes
}
