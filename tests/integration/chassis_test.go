//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/net/html"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
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

// TestChassisIntegration_AudioEventEndToEnd verifies the full audio scope
// pipeline from data plane to SSE client. It starts a real session via the
// scenario harness (which wires a real fakemister listener + ffmpeg data
// plane), mounts a chassis server with that manager as AudioScopeViewer, and
// asserts:
//  1. While a session is active, the audio SSE event carries "status":"live"
//     with vu / spectrum / goniometer fields.
//  2. After calling Stop, the audio SSE transitions to "status":"pending".
func TestChassisIntegration_AudioEventEndToEnd(t *testing.T) {
	sample := ensureSampleMP4(t, "5s.mp4", 5)
	h := newScenarioHarness(t)

	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:           config.BridgeConfig{},
		Manager:          h.Manager,
		Registry:         adapters.NewRegistry(),
		Version:          "integration-test",
		StartedAt:        time.Now(),
		HostIP:           "127.0.0.1",
		AudioScopeViewer: h.Manager,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	mux := http.NewServeMux()
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if err := h.Manager.StartSession(defaultRequest(sample, "audio-sse")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/receiver/events", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}

	// Wait for at least one live audio event (status:"live") — the audio tick
	// fires at 30 Hz so this should arrive well within 4 s once the plane is
	// streaming. The audio data plane only sets Generation > 0 after the first
	// real PCM chunk arrives via AudioMeter, so we tolerate up to 4 s of ramp.
	liveBody := chassisReadSSEUntil(t, resp, "event: audio\n", `"status":"live"`, 6*time.Second)
	for _, want := range []string{
		`"vu":`,
		`"spectrum":[`,
		`"goniometer":[`,
	} {
		if !strings.Contains(liveBody, want) {
			t.Errorf("live audio SSE missing %q in collected body:\n%s", want, liveBody)
		}
	}

	if err := h.Manager.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop the plane is torn down; the audio viewer returns nil →
	// the chassis emits a pending frame on the next audio tick (~33 ms).
	// The live→pending transition is not suppressed by audioShouldEmit.
	pendingBody := chassisReadSSEUntil(t, resp, "event: audio\n", `"status":"pending"`, 3*time.Second)
	if !strings.Contains(pendingBody, "event: audio\n") {
		t.Errorf("pending audio SSE response does not contain audio event:\n%s", pendingBody)
	}
}

// chassisReadSSEUntil reads from the SSE response body until both eventLine
// (e.g. "event: audio\n") and needle (e.g. `"status":"live"`) appear in the
// accumulated text, or timeout elapses. Returns the accumulated body text.
func chassisReadSSEUntil(t *testing.T, resp *http.Response, eventLine, needle string, timeout time.Duration) string {
	t.Helper()
	scanner := bufio.NewScanner(resp.Body)
	var collected strings.Builder
	deadline := time.Now().Add(timeout)

	var seenEventLine bool
	for time.Now().Before(deadline) {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("read SSE stream: %v; collected:\n%s", err, collected.String())
			}
			t.Fatalf("SSE stream closed before seeing %q; collected:\n%s", needle, collected.String())
		}
		line := scanner.Text() + "\n"
		collected.WriteString(line)

		if line == eventLine {
			seenEventLine = true
		}
		if seenEventLine && strings.Contains(collected.String(), needle) {
			return collected.String()
		}
	}
	t.Fatalf("timed out waiting for event %q with %q; collected:\n%s", eventLine, needle, collected.String())
	return collected.String()
}

// ---------------------------------------------------------------------------
// TestChassisIntegration_CastAndPresetEndToEnd — Task 14 (Phase 3A)
// ---------------------------------------------------------------------------

// integrationBridge returns a minimal BridgeConfig sufficient for chassis.New.
func integrationBridge(_ *testing.T) config.BridgeConfig {
	return config.BridgeConfig{UI: config.UIConfig{HTTPPort: 32500}}
}

// integrationManager returns the zero-value Manager used throughout the
// existing integration tests (&core.Manager{}).
func integrationManager(_ *testing.T) *core.Manager {
	return &core.Manager{}
}

// fakeURLStub records HandleQuickCast calls for the "url" tab.
type fakeURLStub struct {
	mu    sync.Mutex
	calls []adapters.QuickCastRequest
}

func (s *fakeURLStub) Name() string        { return "url" }
func (s *fakeURLStub) DisplayName() string { return "URL" }
func (s *fakeURLStub) Fields() []adapters.FieldDef {
	return nil
}
func (s *fakeURLStub) DecodeConfig(_ toml.Primitive, _ toml.MetaData) error { return nil }
func (s *fakeURLStub) IsEnabled() bool                                       { return true }
func (s *fakeURLStub) Start(_ context.Context) error                         { return nil }
func (s *fakeURLStub) Stop() error                                           { return nil }
func (s *fakeURLStub) Status() adapters.Status                               { return adapters.Status{} }
func (s *fakeURLStub) ApplyConfig(_ toml.Primitive, _ toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeNextCast, nil
}
func (s *fakeURLStub) QuickCastTabs() []adapters.QuickCastTab {
	return []adapters.QuickCastTab{{
		ID:       "url",
		Enabled:  true,
		Encoding: adapters.QuickCastEncodingForm,
		Fields:   []adapters.QuickCastField{{Name: "url", Type: "url"}},
	}}
}
func (s *fakeURLStub) HandleQuickCast(_ context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()
	return adapters.QuickCastResult{}, nil
}
func (s *fakeURLStub) last() adapters.QuickCastRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return adapters.QuickCastRequest{}
	}
	return s.calls[len(s.calls)-1]
}

// fakeTorrentStub records HandleQuickCast calls for the "torrent-*" tabs.
type fakeTorrentStub struct {
	mu    sync.Mutex
	calls []adapters.QuickCastRequest
}

func (s *fakeTorrentStub) Name() string        { return "torrent" }
func (s *fakeTorrentStub) DisplayName() string { return "Torrent" }
func (s *fakeTorrentStub) Fields() []adapters.FieldDef {
	return nil
}
func (s *fakeTorrentStub) DecodeConfig(_ toml.Primitive, _ toml.MetaData) error { return nil }
func (s *fakeTorrentStub) IsEnabled() bool                                       { return true }
func (s *fakeTorrentStub) Start(_ context.Context) error                         { return nil }
func (s *fakeTorrentStub) Stop() error                                           { return nil }
func (s *fakeTorrentStub) Status() adapters.Status                               { return adapters.Status{} }
func (s *fakeTorrentStub) ApplyConfig(_ toml.Primitive, _ toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeNextCast, nil
}
func (s *fakeTorrentStub) QuickCastTabs() []adapters.QuickCastTab {
	return []adapters.QuickCastTab{
		{
			ID:       "torrent-magnet",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingForm,
			Fields:   []adapters.QuickCastField{{Name: "magnet", Type: "text"}},
		},
		{
			ID:       "torrent-file",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingMultipart,
			Fields:   []adapters.QuickCastField{{Name: "torrent_file", Type: "file"}},
		},
	}
}
func (s *fakeTorrentStub) HandleQuickCast(_ context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()
	return adapters.QuickCastResult{}, nil
}
func (s *fakeTorrentStub) last() adapters.QuickCastRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return adapters.QuickCastRequest{}
	}
	return s.calls[len(s.calls)-1]
}

// streamsStub satisfies adapters.PresetViewer with static entries.
type streamsStub struct{}

func (streamsStub) Presets() [12]adapters.PresetEntry {
	var out [12]adapters.PresetEntry
	for i := range out {
		out[i] = adapters.PresetEntry{
			Slot:       i + 1,
			ProviderID: "stub-provider",
			ChannelID:  fmt.Sprintf("stub-channel-%d", i+1),
			Title:      fmt.Sprintf("Preset %d", i+1),
			BadgeLabel: "STUB",
			BadgeClass: "mtv",
		}
	}
	return out
}

// fakePresetCaster records CastPreset calls and satisfies adapters.PresetCaster.
type fakePresetCaster struct {
	mu    sync.Mutex
	slots []int
}

func (s *fakePresetCaster) CastPreset(_ context.Context, slot int) error {
	s.mu.Lock()
	s.slots = append(s.slots, slot)
	s.mu.Unlock()
	return nil
}

func (s *fakePresetCaster) lastSlot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.slots) == 0 {
		return 0
	}
	return s.slots[len(s.slots)-1]
}

// buildFakeRegistry creates a registry with url, torrent, and (optionally)
// streams stubs that record calls, returning the stubs for assertion.
func buildFakeRegistry(t *testing.T) (*adapters.Registry, *fakeURLStub, *fakeTorrentStub, *fakePresetCaster) {
	t.Helper()
	urlCalls := &fakeURLStub{}
	torrentCalls := &fakeTorrentStub{}
	streamsCalls := &fakePresetCaster{}

	reg := adapters.NewRegistry()
	if err := reg.Register(urlCalls); err != nil {
		t.Fatalf("register url: %v", err)
	}
	if err := reg.Register(torrentCalls); err != nil {
		t.Fatalf("register torrent: %v", err)
	}
	return reg, urlCalls, torrentCalls, streamsCalls
}

// mustPOSTForm issues a same-origin POST with application/x-www-form-urlencoded body.
func mustPOSTForm(t *testing.T, rawURL, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// mustPOSTRaw issues a same-origin POST with the given content-type and body.
func mustPOSTRaw(t *testing.T, rawURL, contentType string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rawURL, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// makeMultipart builds a multipart/form-data body with a single file field
// plus the hidden kind=file field that the chassis cast handler expects.
func makeMultipart(t *testing.T, fieldName, filename string, data []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("kind", "file"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestChassisIntegration_CastAndPresetEndToEnd(t *testing.T) {
	reg, urlCalls, torrentCalls, streamsCalls := buildFakeRegistry(t)

	srv, err := chassis.New(chassis.Config{
		Bridge:       integrationBridge(t),
		Manager:      integrationManager(t),
		Registry:     reg,
		Version:      "test",
		StartedAt:    time.Now(),
		HostIP:       "127.0.0.1",
		PresetViewer: streamsStub{},
		PresetCaster: streamsCalls,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	t.Run("url paste", func(t *testing.T) {
		body := url.Values{"kind": {"url"}, "payload": {"https://example.test/x.mp4"}}.Encode()
		resp := mustPOSTForm(t, ts.URL+"/receiver/cast", body)
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, bodyBytes)
		}
		if got := urlCalls.last(); got.TabID != "url" || got.Values["url"] != "https://example.test/x.mp4" {
			t.Errorf("url adapter received %+v, want TabID=url Values[url]=https://example.test/x.mp4", got)
		}
	})

	t.Run("magnet paste", func(t *testing.T) {
		body := url.Values{"kind": {"magnet"}, "payload": {"magnet:?xt=urn:btih:abc"}}.Encode()
		resp := mustPOSTForm(t, ts.URL+"/receiver/cast", body)
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, bodyBytes)
		}
		if got := torrentCalls.last(); got.TabID != "torrent-magnet" {
			t.Errorf("torrent adapter received TabID=%q, want torrent-magnet", got.TabID)
		}
	})

	t.Run("torrent upload", func(t *testing.T) {
		body, contentType := makeMultipart(t, "torrent_file", "example.torrent", []byte("d8:announce..."))
		resp := mustPOSTRaw(t, ts.URL+"/receiver/cast", contentType, body)
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, bodyBytes)
		}
		if got := torrentCalls.last(); got.TabID != "torrent-file" || got.File == nil || got.File.FieldName != "torrent_file" {
			t.Errorf("torrent adapter file upload incorrect: %+v", got)
		}
	})

	t.Run("preset click", func(t *testing.T) {
		resp := mustPOSTForm(t, ts.URL+"/receiver/preset/3/cast", "")
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, bodyBytes)
		}
		if got := streamsCalls.lastSlot(); got != 3 {
			t.Errorf("streams.CastPreset slot = %d, want 3", got)
		}
	})

	t.Run("preset bad slot", func(t *testing.T) {
		resp := mustPOSTForm(t, ts.URL+"/receiver/preset/0/cast", "")
		defer resp.Body.Close()
		if resp.StatusCode != 400 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, bodyBytes)
		}
	})

	t.Run("lit derives from transport adapter ref", func(t *testing.T) {
		// The streamsStub uses stub-provider/stub-channel-N pattern. A
		// session whose AdapterRef points at stub-channel-3 should light
		// slot 3.
		// We construct a separate chassis server with a SessionViewer that
		// emits the desired AdapterRef.
		sessionView := &fakeIntegrationSession{view: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "streams",
			AdapterRef: "streams:stub-provider:stub-channel-3:sess:42",
		}}
		litSrv, err := chassis.New(chassis.Config{
			Bridge:       integrationBridge(t),
			Manager:      integrationManager(t),
			Registry:     reg,
			Version:      "test",
			StartedAt:    time.Now(),
			HostIP:       "127.0.0.1",
			Session:      sessionView,
			PresetViewer: streamsStub{},
			PresetCaster: streamsCalls,
		})
		if err != nil {
			t.Fatalf("chassis.New: %v", err)
		}
		mux2 := http.NewServeMux()
		litSrv.Mount(mux2)
		ts2 := httptest.NewServer(mux2)
		defer ts2.Close()

		req, _ := http.NewRequest(http.MethodGet, ts2.URL+"/receiver", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /receiver: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		if !strings.Contains(bodyStr, `data-channel="stub-channel-3"`) {
			t.Errorf("rendered HTML missing slot 3 data-channel attribute")
		}

		// Find the slot 3 button and check it has the lit class. The
		// button element spans multiple lines, so we extract the substring
		// between "<button" and "data-channel=\"stub-channel-3\"" and
		// check that fragment for the lit class token.
		channelMarker := `data-channel="stub-channel-3"`
		markerIdx := strings.Index(bodyStr, channelMarker)
		if markerIdx < 0 {
			t.Fatal("slot 3 data-channel marker not found (already checked above)")
		}
		// Walk backward from the marker to find the opening <button tag.
		buttonOpen := strings.LastIndex(bodyStr[:markerIdx], "<button")
		if buttonOpen < 0 {
			t.Fatal("could not find <button before slot 3 data-channel marker")
		}
		buttonFragment := bodyStr[buttonOpen : markerIdx+len(channelMarker)]
		if !strings.Contains(buttonFragment, " lit") {
			t.Errorf("slot 3 button does not have lit class; button fragment:\n%s", buttonFragment)
		}
	})
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

// ---------------------------------------------------------------------------
// Phase 3B Task 28 — end-to-end integration tests
// ---------------------------------------------------------------------------

// recordingStreamsCaster satisfies adapters.StreamsCaster and records every
// CastChannel call. It also flips the bound fakeIntegrationSession into the
// matching "playing streams:<provider>:<channel>" state so subsequent /receiver
// renders surface the .casting lamp and .lit preset without needing a live
// core.Manager session.
//
// The name is intentionally distinct from fakePresetCaster (which
// satisfies PresetCaster, NOT StreamsCaster).
type recordingStreamsCaster struct {
	mu              sync.Mutex
	session         *fakeIntegrationSession
	lastProvider    string
	lastChannel     string
	calls           int
	lastChannelCast string // "<provider>:<channel>" for convenience assertions
}

func (r *recordingStreamsCaster) CastChannel(_ context.Context, providerID, channelID string) error {
	r.mu.Lock()
	r.lastProvider = providerID
	r.lastChannel = channelID
	r.lastChannelCast = providerID + ":" + channelID
	r.calls++
	sess := r.session
	r.mu.Unlock()
	if sess != nil {
		sess.set(core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "streams",
			AdapterRef: fmt.Sprintf("streams:%s:%s:sess:1", providerID, channelID),
			Title:      providerID + " · " + channelID,
		})
	}
	return nil
}

// chassisEnv holds the running pieces of a chassis integration server backed
// by the REAL streams adapter (for PresetViewer/PresetEditor/SourceAvailability/
// StreamsCatalogViewer) and a recording StreamsCaster (to avoid spinning up
// real playback against a zero-value *core.Manager).
type chassisEnv struct {
	t           *testing.T
	dir         string
	ts          *httptest.Server
	srv         *chassis.Server
	mux         *http.ServeMux
	streamsA    *streams.Adapter
	fakeStreams *recordingStreamsCaster
	session     *fakeIntegrationSession
}

// Close shuts down the embedded http test server AND the chassis cache
// refresher goroutine. Safe to call from t.Cleanup as well as end-of-test.
func (e *chassisEnv) Close() {
	if e == nil {
		return
	}
	if e.ts != nil {
		e.ts.Close()
	}
	if e.srv != nil {
		_ = e.srv.Close()
	}
}

// newChassisIntegrationEnv creates a fresh temp data_dir, pre-seeds an EMPTY
// chassis_presets.json so the streams preset bank starts with zero filled
// slots (required by TestReceiverPresetStar_AddRemoveBankFull), and delegates
// to newChassisIntegrationEnvIn.
func newChassisIntegrationEnv(t *testing.T) *chassisEnv {
	t.Helper()
	dir := t.TempDir()
	// Empty slots so star-add tests have predictable starting state.
	emptyStore := []byte(`{"version":1,"slots":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "chassis_presets.json"), emptyStore, 0o600); err != nil {
		t.Fatalf("seed empty chassis_presets.json: %v", err)
	}
	return newChassisIntegrationEnvIn(t, dir)
}

// newChassisIntegrationEnvIn builds a chassis Server wired to a real streams
// Adapter in dir. The streams adapter is enabled (SetEnabled(true)) so it
// reports Configured()=true to the source-lamp logic; remote manifest refresh
// is suppressed by leaving AllowRemoteManifest in its decoded-config default
// (we never call ApplyConfig/Start with remote manifest enabled in tests).
func newChassisIntegrationEnvIn(t *testing.T, dir string) *chassisEnv {
	t.Helper()

	streamsA, err := streams.New(streams.AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: dir},
		Core:   &core.Manager{}, // unused by the catalog/preset/source paths these tests exercise
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	// Configured()=IsEnabled(); needed for the .lamp.configured-idle class.
	streamsA.SetEnabled(true)

	session := &fakeIntegrationSession{view: core.StatusHomeView{State: core.StateIdle}}
	fakeStreams := &recordingStreamsCaster{session: session}

	reg := adapters.NewRegistry()

	srv, err := chassis.New(chassis.Config{
		Bridge:                    config.BridgeConfig{DataDir: dir, UI: config.UIConfig{HTTPPort: 32500}},
		Manager:                   &core.Manager{},
		Registry:                  reg,
		Version:                   "integration-test",
		StartedAt:                 time.Now(),
		HostIP:                    "127.0.0.1",
		Session:                   session,
		PresetViewer:              streamsA,
		PresetEditor:              streamsA,
		StreamsCatalogViewer:      streamsA,
		StreamsCaster:             fakeStreams,
		SourceAvailabilityViewers: []adapters.SourceAvailabilityViewer{streamsA},
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}

	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)

	return &chassisEnv{
		t:           t,
		dir:         dir,
		ts:          ts,
		srv:         srv,
		mux:         mux,
		streamsA:    streamsA,
		fakeStreams: fakeStreams,
		session:     session,
	}
}

// PostForm issues a same-origin form POST against the env's test server.
// The returned response body is the caller's responsibility to Close.
func (e *chassisEnv) PostForm(path string, form url.Values) *http.Response {
	e.t.Helper()
	return mustPOSTForm(e.t, e.ts.URL+path, form.Encode())
}

// GetReceiverHTML fetches GET /receiver and returns the body as a string.
// Always fails the test on transport error or non-200 status.
func (e *chassisEnv) GetReceiverHTML(t *testing.T) string {
	t.Helper()
	resp, err := http.Get(e.ts.URL + "/receiver")
	if err != nil {
		t.Fatalf("GET /receiver: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /receiver status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /receiver body: %v", err)
	}
	return string(body)
}

// sseStream wraps a long-lived /receiver/events connection with helpers for
// the canonical "drain initial burst, then watch for a specific event" pattern
// the integration tests need.
type sseStream struct {
	t      *testing.T
	resp   *http.Response
	rdr    *bufio.Reader
	cancel context.CancelFunc
}

// OpenEvents opens a long-lived SSE connection bound to a context. The caller
// MUST call Close on the returned stream when finished.
func (e *chassisEnv) OpenEvents(t *testing.T) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+"/receiver/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("new SSE request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET /receiver/events: %v", err)
	}
	return &sseStream{
		t:      t,
		resp:   resp,
		rdr:    bufio.NewReader(resp.Body),
		cancel: cancel,
	}
}

func (s *sseStream) Close() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.resp != nil {
		s.resp.Body.Close()
	}
}

// readEvent reads one (event, data) pair from the SSE stream, skipping
// "retry:" and ": heartbeat" lines and blank separators. Returns ("", "",
// error) on read failure or context cancellation. Returns ("", "", nil)
// if the deadline is exceeded.
//
// Implemented as a background read goroutine feeding a channel so the
// deadline interrupts a blocked ReadString — bufio.Reader.ReadString
// does not honor context cancellation directly.
func (s *sseStream) readEvent(deadline time.Time) (string, string, error) {
	type lineResult struct {
		line string
		err  error
	}
	var eventName, dataLine string
	for {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return "", "", nil
		}
		ch := make(chan lineResult, 1)
		go func() {
			line, err := s.rdr.ReadString('\n')
			ch <- lineResult{line: line, err: err}
		}()
		var timer *time.Timer
		var timerC <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return "", "", nil
			}
			timer = time.NewTimer(d)
			timerC = timer.C
		}
		var res lineResult
		select {
		case res = <-ch:
			if timer != nil {
				if !timer.Stop() {
					<-timer.C
				}
			}
		case <-timerC:
			// Deadline reached. The read goroutine still owns the
			// reader; closing the response body will unblock it.
			// Caller is expected to Close() the stream when done.
			return "", "", nil
		}
		if res.err != nil {
			return "", "", res.err
		}
		line := strings.TrimRight(res.line, "\r\n")
		if line == "" {
			if eventName != "" {
				return eventName, dataLine, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "retry:") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			continue
		}
	}
}

// DrainInitialBurstThrough reads SSE events up to and including the
// named event ("audio" is the canonical end-of-burst marker). Fails the
// test if the deadline elapses first.
func (s *sseStream) DrainInitialBurstThrough(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ev, _, err := s.readEvent(deadline)
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		if ev == "" {
			t.Fatalf("timed out waiting for %q during initial burst", name)
		}
		if ev == name {
			return
		}
	}
}

// WaitForEvent reads SSE events until one matches name or the deadline
// elapses. Returns true if name was seen, false on timeout.
func (s *sseStream) WaitForEvent(t *testing.T, name string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ev, _, err := s.readEvent(deadline)
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		if ev == "" {
			return false
		}
		if ev == name {
			return true
		}
	}
}

// CollectInitialEventNames opens a one-shot SSE connection and collects
// every event name emitted up to and including "audio" (the canonical
// end-of-initial-burst marker). The deadline bounds the wait.
func (e *chassisEnv) CollectInitialEventNames(t *testing.T, timeout time.Duration) []string {
	t.Helper()
	stream := e.OpenEvents(t)
	defer stream.Close()
	deadline := time.Now().Add(timeout)
	var names []string
	for {
		ev, _, err := stream.readEvent(deadline)
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		if ev == "" {
			t.Fatalf("timed out collecting initial burst; got so far: %v", names)
		}
		names = append(names, ev)
		if ev == "audio" {
			return names
		}
	}
}

// NextPresetsEvent opens an SSE connection and reads until the first
// "presets" event lands, then returns its parsed slots array as a slice
// of map[string]any (length 12). Fails on timeout.
func (e *chassisEnv) NextPresetsEvent(t *testing.T, timeout time.Duration) []map[string]any {
	t.Helper()
	stream := e.OpenEvents(t)
	defer stream.Close()
	deadline := time.Now().Add(timeout)
	for {
		ev, data, err := stream.readEvent(deadline)
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		if ev == "" {
			t.Fatalf("timed out waiting for presets event")
		}
		if ev != "presets" {
			continue
		}
		var payload struct {
			Slots []map[string]any `json:"slots"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("parse presets payload %q: %v", data, err)
		}
		return payload.Slots
	}
}

// drainJSON reads a JSON response body into map[string]any and closes it.
// Fails the test on read or parse error.
func drainJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("parse response body %q: %v", body, err)
	}
	return m
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestReceiverStreamsCast_EndToEnd(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()

	form := url.Values{"provider": {"mtv-rewind"}, "channel": {"80s"}}
	resp := env.PostForm("/receiver/streams/cast", form)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if got := env.fakeStreams.lastChannelCast; got != "mtv-rewind:80s" {
		t.Errorf("fakeStreams.lastChannelCast = %q, want mtv-rewind:80s", got)
	}
}

func TestReceiverPresetStar_AddRemoveBankFull(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()

	// Empty bank: add 80s → slot 1.
	resp := env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	body := drainJSON(t, resp)
	if !body["ok"].(bool) || int(body["slot"].(float64)) != 1 {
		t.Errorf("first add body = %v, want ok=true slot=1", body)
	}

	// Repeated add: no-op, returns slot 1.
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	body = drainJSON(t, resp)
	if !body["ok"].(bool) || int(body["slot"].(float64)) != 1 {
		t.Errorf("repeat add body = %v, want ok=true slot=1", body)
	}

	// Fill the remaining 11 slots.
	for i, ch := range []string{"90s", "trl", "120minutes", "unplugged", "amp", "loonytunes", "animaniacs", "heman", "all", "east", "movies"} {
		provider := "mtv-rewind"
		if i >= 5 {
			provider = "cartoon-rewind"
		}
		if i >= 9 {
			provider = "toonami-aftermath"
		}
		resp = env.PostForm("/receiver/preset/star", url.Values{
			"provider": {provider}, "channel": {ch}, "starred": {"true"},
		})
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("fill %d %s/%s status = %d body=%s", i, provider, ch, resp.StatusCode, b)
		}
		resp.Body.Close()
	}

	// Add 13th channel → 409 BANK FULL.
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"fuse"}, "starred": {"true"},
	})
	if resp.StatusCode != 409 {
		t.Errorf("13th add status = %d, want 409", resp.StatusCode)
	}
	body = drainJSON(t, resp)
	if body["chip"] != "BANK FULL" {
		t.Errorf("13th add chip = %v, want BANK FULL", body["chip"])
	}

	// Remove 80s → cleared=[1].
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"false"},
	})
	body = drainJSON(t, resp)
	cleared, _ := body["cleared"].([]any)
	if len(cleared) != 1 || int(cleared[0].(float64)) != 1 {
		t.Errorf("remove cleared = %v, want [1]", body["cleared"])
	}

	// Repeat remove is a no-op success.
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"false"},
	})
	if resp.StatusCode != 200 {
		t.Errorf("repeat remove status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestReceiverPresetMove_Swap(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()

	// Seed two distinct slots first.
	resp := env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	resp.Body.Close()
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"cartoon-rewind"}, "channel": {"heman"}, "starred": {"true"},
	})
	resp.Body.Close()

	resp = env.PostForm("/receiver/preset/move", url.Values{
		"from": {"1"}, "to": {"2"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("move status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Read presets via the SSE stream and assert the swap landed.
	slots := env.NextPresetsEvent(t, 2*time.Second)
	if len(slots) < 2 {
		t.Fatalf("presets snapshot has %d slots, want 12", len(slots))
	}
	if slots[0]["provider"] != "cartoon-rewind" || slots[1]["provider"] != "mtv-rewind" {
		t.Errorf("post-swap slots[1]=%v slots[2]=%v, want cartoon-rewind / mtv-rewind",
			slots[0]["provider"], slots[1]["provider"])
	}
}

func TestReceiverEvents_PresetsBetweenMeterAndAudio(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	names := env.CollectInitialEventNames(t, 2*time.Second)
	want := []string{"state", "vfd", "source", "visualizer", "transport", "volume", "meter", "presets", "audio"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("initial burst events = %v, want %v", names, want)
	}
}

func TestReceiverPresetStar_PersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	env1 := newChassisIntegrationEnvIn(t, dir)
	resp := env1.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"false"},
	})
	resp.Body.Close()
	env1.Close()

	env2 := newChassisIntegrationEnvIn(t, dir)
	defer env2.Close()
	slots := env2.NextPresetsEvent(t, 2*time.Second)
	if len(slots) < 2 {
		t.Fatalf("presets snapshot has %d slots, want 12", len(slots))
	}
	// 80s was bundled into slot 2; after removal+restart it should be empty.
	if got, _ := slots[1]["provider"].(string); got != "" {
		t.Errorf("restart slot 2 provider = %v, want empty (was removed)", slots[1]["provider"])
	}
}

func TestReceiverEvents_PresetsFollowUpChangedButNotNoop(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	stream := env.OpenEvents(t)
	defer stream.Close()
	stream.DrainInitialBurstThrough(t, "audio", 2*time.Second)

	resp := env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	resp.Body.Close()
	if !stream.WaitForEvent(t, "presets", 2*time.Second) {
		t.Fatalf("changed star operation did not emit presets follow-up")
	}

	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	resp.Body.Close()
	if stream.WaitForEvent(t, "presets", 300*time.Millisecond) {
		t.Fatalf("idempotent star operation emitted unexpected presets follow-up")
	}
}

func TestReceiverReloadMidCast_TunedSurfacesHydrateFromSnapshot(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	resp := env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	resp.Body.Close()
	resp = env.PostForm("/receiver/streams/cast", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"},
	})
	resp.Body.Close()

	// Force a synchronous snapshot rebuild so the GET below sees the
	// session update that the recordingStreamsCaster applied.
	deadline := time.Now().Add(2 * time.Second)
	var html string
	for time.Now().Before(deadline) {
		html = env.GetReceiverHTML(t)
		if strings.Contains(html, "casting") && strings.Contains(html, " lit") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	for _, want := range []string{
		`data-source-id="streams"`,
		`class="lamp configured-idle casting"`,
		`class="preset lit`,
		`class="ch-card tuned`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("reload html missing tuned/casting surface %q", want)
		}
	}
	// The preset-bank and catalog-tree templates split the tuned channel's
	// data-provider / data-channel attributes across separate lines; verify
	// both attributes are present (no single-line substring assertion is
	// possible against the actual template output).
	if !strings.Contains(html, `data-provider="mtv-rewind"`) {
		t.Errorf("reload html missing data-provider=mtv-rewind attribute")
	}
	if !strings.Contains(html, `data-channel="80s"`) {
		t.Errorf("reload html missing data-channel=80s attribute")
	}
}

func TestReceiverPresetStore_DropsStaleReferenceOnRestart(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"version":1,"slots":[{"slot":1,"provider":"gone","channel":"x"},{"slot":2,"provider":"mtv-rewind","channel":"80s"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "chassis_presets.json"), body, 0o600); err != nil {
		t.Fatalf("seed stale presets: %v", err)
	}
	env := newChassisIntegrationEnvIn(t, dir)
	defer env.Close()
	slots := env.NextPresetsEvent(t, 2*time.Second)
	if len(slots) < 2 {
		t.Fatalf("presets snapshot has %d slots, want 12", len(slots))
	}
	if got, _ := slots[0]["provider"].(string); got != "" {
		t.Errorf("stale slot provider = %v, want empty", slots[0]["provider"])
	}
	if slots[1]["provider"] != "mtv-rewind" || slots[1]["channel"] != "80s" {
		t.Errorf("valid slot = %v, want mtv-rewind/80s", slots[1])
	}
}
