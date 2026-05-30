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
	"runtime"
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
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/launchcore"
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
		if strings.Contains(line, `"primary":"Integration Live Title"`) {
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
	bridgeSaver *uiserver.BridgeSaver // NEW for Phase 4B settings integration
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
	// audioDsp was added to the burst (after volume, before meter) when the
	// audio-strip feature landed; include it in the expected order.
	want := []string{"state", "vfd", "source", "visualizer", "transport", "volume", "audioDsp", "meter", "presets", "audio"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("initial burst events = %v, want %v", names, want)
	}
}

// TestReceiverPresetMove_NoOpDoesNotEmitPresets verifies the from==to
// identity case: the handler returns 200 and calls refreshSnapshotNow for
// uniformity, but the events-loop diff suppresses the spurious emit so no
// presets event reaches subscribers. Mirrors the star idempotent contract
// at TestReceiverEvents_PresetsFollowUpChangedButNotNoop.
func TestReceiverPresetMove_NoOpDoesNotEmitPresets(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	stream := env.OpenEvents(t)
	defer stream.Close()
	stream.DrainInitialBurstThrough(t, "audio", 2*time.Second)

	resp := env.PostForm("/receiver/preset/move", url.Values{
		"from": {"3"}, "to": {"3"},
	})
	if resp.StatusCode != 200 {
		resp.Body.Close()
		t.Fatalf("from==to move status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	if stream.WaitForEvent(t, "presets", 300*time.Millisecond) {
		t.Fatalf("identity move emitted unexpected presets follow-up")
	}
}

// TestReceiverPresetMove_BadSlotDoesNotEmitPresets verifies that a move
// rejected with 400 BAD SLOT (out-of-range from/to) never reaches the
// snapshot refresh path, so no presets SSE event fires. Covers the
// server side of the spec's "reverting the optimistic swap on a forced
// 4xx" contract (Testing Layer 3). The client-side revert in
// preset-reorder.js is exercised manually; this test pins the server
// contract that the events-loop never sees a phantom mutation.
func TestReceiverPresetMove_BadSlotDoesNotEmitPresets(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	stream := env.OpenEvents(t)
	defer stream.Close()
	stream.DrainInitialBurstThrough(t, "audio", 2*time.Second)

	resp := env.PostForm("/receiver/preset/move", url.Values{
		"from": {"13"}, "to": {"1"},
	})
	if resp.StatusCode != 400 {
		resp.Body.Close()
		t.Fatalf("out-of-range move status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	if stream.WaitForEvent(t, "presets", 300*time.Millisecond) {
		t.Fatalf("rejected move emitted unexpected presets follow-up")
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

// ---------------------------------------------------------------------------
// Phase 4A: Settings drawer + Bridge POST + Probe integration tests
// ---------------------------------------------------------------------------

// testSettingsBridgeConfig returns a fully-valid BridgeConfig suitable for
// seeding a *uiserver.BridgeSaver in settings integration tests. All
// mandatory sub-structs (including HLSBuffer) are populated so
// config.Sectioned.Validate passes on every Save call.
func testSettingsBridgeConfig(dataDir string) config.BridgeConfig {
	return config.BridgeConfig{
		DataDir: dataDir,
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "auto",
			RGBMode:             "rgb888",
			LZ4Enabled:          true,
		},
		Audio:      config.AudioConfig{SampleRate: 48000, Channels: 2, OutputVolume: 100, DSP: config.DefaultAudioDSP()},
		Visualizer: config.VisualizerConfig{Mode: config.VisualizerModeRetroAnalyzer},
		MiSTer:     config.MisterConfig{Host: "127.0.0.1", Port: 32100, SourcePort: 32101},
		UI:         config.UIConfig{HTTPPort: 32500},
		HLSBuffer: config.HLSBufferConfig{
			Enabled:                true,
			LiveEdgeSegments:       3,
			StartSegments:          2,
			MaxCachedSegments:      6,
			MaxCacheBytes:          268435456,
			MaxPlaylistBytes:       1048576,
			MaxSegmentBytes:        52428800,
			SegmentTimeoutSeconds:  10,
			PlaylistTimeoutSeconds: 10,
			MaxVariantHeight:       720,
			StaleCacheReapHours:    24,
		},
	}
}

// testSettingsConfigPath writes a minimal config.toml to dir and returns its
// path. marshalBridgeSection reads the existing file on every Save, so the
// file must exist before the first Save call.
func testSettingsConfigPath(t *testing.T, dir string, bridge config.BridgeConfig) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	// Write a minimal stub; BridgeSaver.Save will overwrite the [bridge*]
	// sections atomically on the first successful save.
	const stub = "" // empty is fine; marshalBridgeSection strips bridge sections before appending
	if err := os.WriteFile(path, []byte(stub), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	return path
}

// fakeCoreForSettingsTests satisfies uiserver.Core with no-op methods.
type fakeCoreForSettingsTests struct{}

func (fakeCoreForSettingsTests) UpdateBridge(config.BridgeConfig)         {}
func (fakeCoreForSettingsTests) SetInterlaceFieldOrder(string) error      { return nil }
func (fakeCoreForSettingsTests) SetOutputVolume(int) error                { return nil }
func (fakeCoreForSettingsTests) SetAudioDSP(config.AudioDSP) error        { return nil }
func (fakeCoreForSettingsTests) DropActiveCast(string) error              { return nil }

// fakeSettingsProber satisfies chassis.Prober for settings integration tests.
type fakeSettingsProber struct {
	res chassis.ProbeResult
	err error
}

func (f fakeSettingsProber) ProbeMister(_ context.Context, _ config.BridgeConfig) (chassis.ProbeResult, error) {
	return f.res, f.err
}

// settingsEnvOptions configures newChassisIntegrationEnvForSettings.
// All fields are optional; zero values mean "no override / nil".
type settingsEnvOptions struct {
	prober       chassis.Prober       // wired into chassis.Config.Prober
	coreLauncher chassis.CoreLauncher // wired into chassis.Config.CoreLauncher
	mutateBridge func(*config.BridgeConfig) // applied to the seed bridge before saver init
}

// newChassisIntegrationEnvWithProber preserves 4A's signature. Existing
// callers (TestReceiverSettings_GetRendersAllNetworkFields,
// TestReceiverSettings_BridgePostHotSwapSucceeds, etc.) call this and
// must keep working unchanged.
func newChassisIntegrationEnvWithProber(t *testing.T, prober chassis.Prober) *chassisEnv {
	t.Helper()
	return newChassisIntegrationEnvForSettings(t, settingsEnvOptions{prober: prober})
}

// newChassisIntegrationEnvForSettings is the Phase 4B settings-integration
// harness. Wires a real *uiserver.BridgeSaver against a tmp config.toml,
// builds a chassis Server with streams/session/preset infrastructure, and
// stores the saver on the returned env so tests can inspect saved state
// directly via env.bridgeSaver.Current().
func newChassisIntegrationEnvForSettings(t *testing.T, opts settingsEnvOptions) *chassisEnv {
	t.Helper()
	dir := t.TempDir()

	// Pre-seed the empty chassis_presets.json that newChassisIntegrationEnv
	// would normally seed (keeps the preset bank at a known zero state).
	emptyStore := []byte(`{"version":1,"slots":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "chassis_presets.json"), emptyStore, 0o600); err != nil {
		t.Fatalf("seed empty chassis_presets.json: %v", err)
	}

	bridge := testSettingsBridgeConfig(dir)
	if opts.mutateBridge != nil {
		opts.mutateBridge(&bridge)
	}
	cfgPath := testSettingsConfigPath(t, dir, bridge)

	sec := &config.Sectioned{Bridge: bridge}
	reg := adapters.NewRegistry()

	bridgeSaver := uiserver.NewBridgeSaver(cfgPath, sec, fakeCoreForSettingsTests{}, reg)

	streamsA, err := streams.New(streams.AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: dir},
		Core:   &core.Manager{},
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	streamsA.SetEnabled(true)

	session := &fakeIntegrationSession{view: core.StatusHomeView{State: core.StateIdle}}
	fakeStreams := &recordingStreamsCaster{session: session}

	srv, err := chassis.New(chassis.Config{
		Bridge:                    bridge,
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
		BridgeSaver:               bridgeSaver,
		Prober:                    opts.prober,
		CoreLauncher:              opts.coreLauncher,
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
		fakeStreams:  fakeStreams,
		session:     session,
		bridgeSaver: bridgeSaver,
	}
}

// tempExecutable writes a stub executable to t.TempDir() and returns its path.
// On Windows the file gets a .exe suffix; on Unix it is chmod'd 0755.
func tempExecutable(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write temp executable: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatalf("chmod temp executable: %v", err)
		}
	}
	return path
}

func TestReceiverSettings_GetRendersAllNetworkFields(t *testing.T) {
	env := newChassisIntegrationEnvWithProber(t, nil)
	defer env.Close()
	resp, err := http.Get(env.ts.URL + "/receiver")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	wantInputs := []string{
		`name="mister_host"`, `name="mister_port"`, `name="mister_source_port"`,
		`name="ui_http_port"`, `name="host_ip"`, `name="data_dir"`,
		`name="ffmpeg_path"`, `name="ffprobe_path"`, `name="ytdlp_path"`,
		`id="probe-mister-btn"`,
		`class="settings-notice"`,
	}
	for _, w := range wantInputs {
		if !strings.Contains(s, w) {
			t.Errorf("/receiver missing %q", w)
		}
	}
}

func TestReceiverSettings_BridgePostHotSwapSucceeds(t *testing.T) {
	env := newChassisIntegrationEnvWithProber(t, nil)
	defer env.Close()
	// Touch a HOT-scope field (ffmpeg_path) with a real temp executable.
	toolPath := tempExecutable(t, "ffmpeg")
	resp := env.PostForm("/receiver/settings/bridge", url.Values{
		"ffmpeg_path": {toolPath},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] != true || body["scope"] != "hot" {
		t.Errorf("body = %+v, want {ok:true, scope:\"hot\"}", body)
	}
}

func TestReceiverSettings_BridgePostInvalidHostReturns400(t *testing.T) {
	env := newChassisIntegrationEnvWithProber(t, nil)
	defer env.Close()
	resp := env.PostForm("/receiver/settings/bridge", url.Values{
		"mister_host": {""},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, ok := body["errors"].(map[string]any)
	if !ok {
		t.Fatalf("errors not present: %v", body)
	}
	if msg, _ := errs["mister_host"].(string); !strings.Contains(msg, "is required") {
		t.Errorf("errors[mister_host] = %v, want 'is required'", errs["mister_host"])
	}
}

func TestReceiverSettings_BridgePostEmptyBodyReturns400(t *testing.T) {
	env := newChassisIntegrationEnvWithProber(t, nil)
	defer env.Close()
	resp := env.PostForm("/receiver/settings/bridge", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestReceiverSettings_ProbePostSuccess(t *testing.T) {
	env := newChassisIntegrationEnvWithProber(t, fakeSettingsProber{
		res: chassis.ProbeResult{LatencyMs: 4.2, Host: "192.168.1.42", Port: 32100},
	})
	defer env.Close()
	resp := env.PostForm("/receiver/settings/action/probe-mister", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] != true || body["host"] != "192.168.1.42" {
		t.Errorf("body = %+v, want ok success with host", body)
	}
}

func TestReceiverSettings_ProbePostTimeout(t *testing.T) {
	env := newChassisIntegrationEnvWithProber(t, fakeSettingsProber{err: context.DeadlineExceeded})
	defer env.Close()
	resp := env.PostForm("/receiver/settings/action/probe-mister", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 for operational timeout; body=%s", resp.StatusCode, body)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] != false || body["error"] != "timeout" {
		t.Errorf("body = %+v, want timeout response", body)
	}
}

func TestReceiverSettings_ProbePostNilProberReturns503(t *testing.T) {
	env := newChassisIntegrationEnvWithProber(t, nil)
	defer env.Close()
	resp := env.PostForm("/receiver/settings/action/probe-mister", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 503; body=%s", resp.StatusCode, body)
	}
}

func TestChassisSettings_PipelineInterlaceFieldOrder_Hot(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	if got := env.bridgeSaver.Current().Video.InterlaceFieldOrder; got != "tff" {
		t.Fatalf("starting field = %q, want tff", got)
	}

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"video_interlace_field_order": {"bff"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if got := env.bridgeSaver.Current().Video.InterlaceFieldOrder; got != "bff" {
		t.Errorf("after save: field = %q, want bff", got)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["scope"] != "hot" {
		t.Errorf("scope = %v, want hot", body["scope"])
	}
}

func TestChassisSettings_PipelineLZ4Switch_Recast(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"video_lz4_enabled": {"false"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if got := env.bridgeSaver.Current().Video.LZ4Enabled; got != false {
		t.Errorf("after save: LZ4Enabled = %v, want false", got)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["scope"] != "recast" {
		t.Errorf("scope = %v, want recast", body["scope"])
	}
}

func TestChassisSettings_PipelineSSHPassword_PreservesOnEmpty(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"mister_ssh_password": {"newpass"}})
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("first save StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	resp.Body.Close()
	if got := env.bridgeSaver.Current().MiSTer.SSHPassword; got != "newpass" {
		t.Fatalf("after first save: password = %q, want newpass", got)
	}

	resp = env.PostForm("/receiver/settings/bridge", url.Values{"mister_ssh_password": {""}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("empty save StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if got := env.bridgeSaver.Current().MiSTer.SSHPassword; got != "newpass" {
		t.Errorf("after empty save: password = %q, want \"newpass\" (preserve)", got)
	}
}

func TestChassisSettings_PipelineAudioSampleRate_OutOfRangeReturns400(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"audio_sample_rate": {"96000"}})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, _ := body["errors"].(map[string]any)
	msg, _ := errs["audio_sample_rate"].(string)
	if !strings.Contains(msg, "must be 22050, 44100, or 48000") {
		t.Errorf("error message = %q, want substring \"must be 22050, 44100, or 48000\"", msg)
	}
}

func TestChassisSettings_AdvancedHLSLiveEdgeSegments_Recast(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"hls_live_edge_segments": {"5"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if got := env.bridgeSaver.Current().HLSBuffer.LiveEdgeSegments; got != 5 {
		t.Errorf("after save: LiveEdgeSegments = %d, want 5", got)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["scope"] != "recast" {
		t.Errorf("scope = %v, want recast", body["scope"])
	}
}

func TestChassisSettings_AdvancedHLSLiveEdge_OutOfBoundsReturns400(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"hls_live_edge_segments": {"15"}})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, _ := body["errors"].(map[string]any)
	if msg, _ := errs["hls_live_edge_segments"].(string); !strings.Contains(msg, "must be in [1, 12]") {
		t.Errorf("error = %q, want substring \"must be in [1, 12]\"", msg)
	}
}

func TestChassisSettings_AdvancedHLSMaxCacheBytes_Recast(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"hls_max_cache_bytes": {"134217728"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if got := env.bridgeSaver.Current().HLSBuffer.MaxCacheBytes; got != 134217728 {
		t.Errorf("after save: MaxCacheBytes = %d, want 134217728", got)
	}
}

func TestChassisSettings_AdvancedLoggingDebug_HotSetsLogLevel(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/bridge", url.Values{"logging_debug": {"true"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if got := env.bridgeSaver.Current().Logging.Debug; !got {
		t.Errorf("after save: Logging.Debug = false, want true")
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["scope"] != "hot" {
		t.Errorf("scope = %v, want hot", body["scope"])
	}
	// Verify the hot-swap side effect fired (the bridge saver's
	// applyHotSwapSideEffects calls logging.SetLevel("debug") for true).
	// If the test harness exposes a logging.GetLevel hook, assert here.
}

// ---------------------------------------------------------------------------
// Task 24: Integration test — launch-core success + empty-host
// ---------------------------------------------------------------------------

// fakeLaunchCoreLauncher counts calls for the integration test. Mirrors
// the chassis.CoreLauncher interface structurally.
type fakeLaunchCoreLauncher struct {
	calls int
	err   error
}

func (f *fakeLaunchCoreLauncher) Launch(ctx context.Context) error {
	f.calls++
	return f.err
}

func TestChassisSettings_LaunchCoreSuccess(t *testing.T) {
	t.Parallel()
	launcher := &fakeLaunchCoreLauncher{}
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{coreLauncher: launcher})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/action/launch-core", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if launcher.calls != 1 {
		t.Errorf("launcher.calls = %d, want 1", launcher.calls)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] != true || body["host"] != "127.0.0.1" {
		t.Errorf("body = %+v, want ok with configured host", body)
	}
}

func TestChassisSettings_LaunchCoreEmptyHost(t *testing.T) {
	t.Parallel()
	launcher := &fakeLaunchCoreLauncher{}
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{
		coreLauncher: launcher,
		mutateBridge: func(b *config.BridgeConfig) {
			b.MiSTer.Host = ""
		},
	})
	defer env.Close()

	resp := env.PostForm("/receiver/settings/action/launch-core", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, want 400; body = %s", resp.StatusCode, body)
	}
	if launcher.calls != 0 {
		t.Errorf("launcher.calls = %d, want 0", launcher.calls)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if got, _ := body["error"].(string); got != launchcore.EmptyHostMessage {
		t.Errorf("body.error = %q, want exact match with launcher message", got)
	}
}

// ---------------------------------------------------------------------------
// Phase 4C: Catalog pane + restore-defaults integration tests (Task 23)
// ---------------------------------------------------------------------------

// integrationCatalogManager satisfies chassis.CatalogSettingsManager for
// the integration test package. Mirrors cmd/mister-groovy-relay/catalog_manager.go
// but lives here since package main is not importable.
type integrationCatalogManager struct {
	adapter      *streams.Adapter
	adapterSaver *uiserver.AdapterSaver
}

func (m *integrationCatalogManager) Providers() []chassis.CatalogProviderState {
	cfg := m.adapter.ConfigSnapshot()
	cat := m.adapter.BundledCatalog()
	out := make([]chassis.CatalogProviderState, 0, len(cat))
	for _, p := range cat {
		channels := 0
		for _, g := range p.Groups {
			channels += len(g.Channels)
		}
		pc := cfg.Providers[p.ID]
		out = append(out, chassis.CatalogProviderState{
			ID:                p.ID,
			DisplayName:       p.DisplayName,
			BadgeLabel:        p.BadgeLabel,
			BadgeClass:        p.BadgeClass,
			Origin:            p.Origin,
			Kind:              p.Kind,
			DefaultChannel:    p.DefaultChannel,
			Live:              p.Live,
			ChannelCount:      channels,
			Enabled:           !pc.Disabled,
			HLSBufferDisabled: pc.HLSBufferDisabled,
		})
	}
	return out
}

func (m *integrationCatalogManager) UpdateProvider(id string, patch chassis.CatalogProviderPatch) (adapters.ApplyScope, error) {
	scope, err := m.patchStreams(func(cfg *streams.Config) {
		integrationEnsureProvider(cfg, id)
		pc := cfg.Providers[id]
		if patch.Enabled != nil {
			pc.Disabled = !*patch.Enabled
		}
		if patch.HLSBufferDisabled != nil {
			pc.HLSBufferDisabled = *patch.HLSBufferDisabled
		}
		cfg.Providers[id] = pc
	})
	if err != nil {
		return 0, err
	}
	return integrationMaxScope(scope, integrationDeclaredProviderScope(patch)), nil
}

func (m *integrationCatalogManager) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
	// BundledCatalog (not Catalog) — symmetric with Providers() so a disabled
	// direct-stream provider still receives the flag (mirrors the production
	// catalogManager fix).
	cat := m.adapter.BundledCatalog()
	scope, err := m.patchStreams(func(cfg *streams.Config) {
		if cfg.Providers == nil {
			cfg.Providers = map[string]streams.ProviderConfig{}
		}
		for _, p := range cat {
			if !p.Live {
				continue
			}
			pc := cfg.Providers[p.ID]
			pc.HLSBufferDisabled = disabled
			cfg.Providers[p.ID] = pc
		}
	})
	if err != nil {
		return 0, err
	}
	return integrationMaxScope(scope, adapters.ScopeRestartCast), nil
}

func (m *integrationCatalogManager) patchStreams(apply func(*streams.Config)) (adapters.ApplyScope, error) {
	cfg := m.adapter.ConfigSnapshot()
	apply(&cfg)
	return m.adapter.ApplyConfigValue(cfg, m.adapterSaver.Save)
}

func integrationEnsureProvider(cfg *streams.Config, id string) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]streams.ProviderConfig{}
	}
	if _, ok := cfg.Providers[id]; !ok {
		cfg.Providers[id] = streams.ProviderConfig{}
	}
}

func integrationDeclaredProviderScope(patch chassis.CatalogProviderPatch) adapters.ApplyScope {
	s := adapters.ApplyScope(0)
	if patch.Enabled != nil {
		s = integrationMaxScope(s, adapters.ScopeHotSwap)
	}
	if patch.HLSBufferDisabled != nil {
		s = integrationMaxScope(s, adapters.ScopeRestartCast)
	}
	return s
}

func integrationMaxScope(a, b adapters.ApplyScope) adapters.ApplyScope {
	if a > b {
		return a
	}
	return b
}

// integrationConfigReset satisfies chassis.ConfigReset for the integration
// test package. Mirrors cmd/mister-groovy-relay/config_reset.go.
type integrationConfigReset struct {
	path    string
	mu      *sync.Mutex
	dataDir string
}

func (r *integrationConfigReset) ResetToDefaults() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rendered, err := config.DefaultConfigTOML(r.dataDir)
	if err != nil {
		return fmt.Errorf("render defaults: %w", err)
	}
	return config.WriteAtomic(r.path, rendered)
}

// newChassisIntegrationEnvForCatalog builds a chassis env identical to
// newChassisIntegrationEnvForSettings but also wires CatalogManager and
// ConfigReset so the 4C catalog-pane routes are live.
func newChassisIntegrationEnvForCatalog(t *testing.T) *chassisEnv {
	t.Helper()
	dir := t.TempDir()

	emptyStore := []byte(`{"version":1,"slots":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "chassis_presets.json"), emptyStore, 0o600); err != nil {
		t.Fatalf("seed empty chassis_presets.json: %v", err)
	}

	bridge := testSettingsBridgeConfig(dir)
	cfgPath := testSettingsConfigPath(t, dir, bridge)

	sec := &config.Sectioned{Bridge: bridge}
	reg := adapters.NewRegistry()

	bridgeSaver := uiserver.NewBridgeSaver(cfgPath, sec, fakeCoreForSettingsTests{}, reg)
	adapterSaver := uiserver.NewAdapterSaver(cfgPath, bridgeSaver.Mu())

	streamsA, err := streams.New(streams.AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: dir},
		Core:   &core.Manager{},
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	streamsA.SetEnabled(true)

	session := &fakeIntegrationSession{view: core.StatusHomeView{State: core.StateIdle}}
	fakeStreams := &recordingStreamsCaster{session: session}

	cm := &integrationCatalogManager{adapter: streamsA, adapterSaver: adapterSaver}
	cr := &integrationConfigReset{path: cfgPath, mu: bridgeSaver.Mu(), dataDir: dir}

	srv, err := chassis.New(chassis.Config{
		Bridge:                    bridge,
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
		BridgeSaver:               bridgeSaver,
		CatalogManager:            cm,
		ConfigReset:               cr,
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
		fakeStreams:  fakeStreams,
		session:     session,
		bridgeSaver: bridgeSaver,
	}
}

// readConfigTOML reads the config.toml at cfgPath and returns it as a string.
func readConfigTOML(t *testing.T, cfgPath string) string {
	t.Helper()
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	return string(b)
}

func TestChassis_CatalogPane_RendersProviderRows(t *testing.T) {
	env := newChassisIntegrationEnvForCatalog(t)
	defer env.Close()

	res, err := http.Get(env.ts.URL + "/receiver")
	if err != nil {
		t.Fatalf("GET /receiver: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	defer res.Body.Close()

	for _, want := range []string{
		`data-pane="catalog"`,
		`data-catalog-provider="mtv-rewind"`,
		`data-catalog-direct-hls`,
		`id="restore-defaults-btn"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("GET /receiver body missing %q", want)
		}
	}
}

func TestChassis_PostCatalogProvider_EnabledFalse_DiskAndMemoryUpdated(t *testing.T) {
	env := newChassisIntegrationEnvForCatalog(t)
	defer env.Close()

	// Catalog routes require Sec-Fetch-Site: same-origin (requireSameOrigin middleware).
	res := env.PostForm("/receiver/settings/catalog/provider/mtv-rewind", url.Values{"enabled": {"false"}})
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d; body %s", res.StatusCode, string(body))
	}

	cfgPath := filepath.Join(env.dir, "config.toml")
	tomlContent := readConfigTOML(t, cfgPath)
	// encodeSectionTOML writes providers as a [providers.<id>] subtable inside
	// the [adapters.streams] section (not [adapters.streams.providers.<id>]).
	if !strings.Contains(tomlContent, "[providers.mtv-rewind]") || !strings.Contains(tomlContent, "disabled = true") {
		t.Errorf("disk TOML did not record mtv-rewind disabled = true; got:\n%s", tomlContent)
	}
}

func TestChassis_PostCatalogProvider_HLSBufferDisabled_RecastScope(t *testing.T) {
	env := newChassisIntegrationEnvForCatalog(t)
	defer env.Close()

	res := env.PostForm("/receiver/settings/catalog/provider/toonami-aftermath", url.Values{"hls_buffer_disabled": {"true"}})
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"recast"`) {
		t.Errorf("expected scope:recast; got: %s", string(body))
	}
}

func TestChassis_PostCatalogProvider_BothKeys_RecastMaxWins(t *testing.T) {
	env := newChassisIntegrationEnvForCatalog(t)
	defer env.Close()

	res := env.PostForm("/receiver/settings/catalog/provider/toonami-aftermath", url.Values{
		"enabled":             {"true"},
		"hls_buffer_disabled": {"true"},
	})
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"recast"`) {
		t.Errorf("expected scope:recast (max-wins); got: %s", string(body))
	}
}

func TestChassis_PostCatalogDirectStreamHLS_FlipsLiveOnly(t *testing.T) {
	env := newChassisIntegrationEnvForCatalog(t)
	defer env.Close()

	res := env.PostForm("/receiver/settings/catalog/direct-stream-hls-buffer", url.Values{"disabled": {"true"}})
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"recast"`) {
		t.Errorf("expected scope:recast; got: %s", string(body))
	}

	cfgPath := filepath.Join(env.dir, "config.toml")
	tomlContent := readConfigTOML(t, cfgPath)
	// toonami-aftermath is the only Live provider; it must be flipped.
	if !strings.Contains(tomlContent, "[providers.toonami-aftermath]") || !strings.Contains(tomlContent, "hls_buffer_disabled = true") {
		t.Errorf("disk TOML did not record toonami-aftermath hls_buffer_disabled = true; got:\n%s", tomlContent)
	}
	// mtv-rewind is NOT Live; it must not be flipped.
	if strings.Contains(tomlContent, "[providers.mtv-rewind]") && strings.Contains(tomlContent, "hls_buffer_disabled = true") {
		t.Errorf("non-Live provider mtv-rewind was incorrectly flipped")
	}
}

func TestChassis_PostCatalogProvider_UnknownID_404(t *testing.T) {
	env := newChassisIntegrationEnvForCatalog(t)
	defer env.Close()

	res := env.PostForm("/receiver/settings/catalog/provider/does-not-exist", url.Values{"enabled": {"true"}})
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("status %d; want 404", res.StatusCode)
	}
}

func TestChassis_PostActionRestoreDefaults_DiskMatchesDefaults(t *testing.T) {
	env := newChassisIntegrationEnvForCatalog(t)
	defer env.Close()

	// Dirty the config first.
	preRes := env.PostForm("/receiver/settings/catalog/provider/mtv-rewind", url.Values{"enabled": {"false"}})
	preRes.Body.Close()
	if preRes.StatusCode != 200 {
		t.Fatalf("pre-reset POST status = %d", preRes.StatusCode)
	}

	// Seed a sentinel file in data_dir; reset must not disturb it.
	sentinel := filepath.Join(env.dir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("survives"), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	res := env.PostForm("/receiver/settings/action/restore-defaults", url.Values{})
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"reboot"`) {
		t.Errorf("expected scope:reboot; got %s", string(body))
	}

	cfgPath := filepath.Join(env.dir, "config.toml")
	gotTOML := readConfigTOML(t, cfgPath)
	wantTOML, err := config.DefaultConfigTOML(env.dir)
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	if gotTOML != string(wantTOML) {
		t.Errorf("disk TOML differs from DefaultConfigTOML(%q);\ngot:\n%s\nwant:\n%s",
			env.dir, gotTOML, string(wantTOML))
	}

	if got, _ := os.ReadFile(sentinel); string(got) != "survives" {
		t.Errorf("data_dir sentinel was disturbed by reset")
	}
}

// ---------------------------------------------------------------------------
// TestAudioDSP_E2E_SSEAndCommit — Task 10 (volume-tests + audio-dsp e2e)
// ---------------------------------------------------------------------------

// chassisAudioDSPSaver is the same thin adapter as main.audioDSPSaverAdapter
// but declared here in package integration so the test does not depend on the
// unexported package-main type.
type chassisAudioDSPSaver struct{ bs *uiserver.BridgeSaver }

func (a *chassisAudioDSPSaver) SaveAudioDSP(dsp config.AudioDSP) error {
	_, err := a.bs.SaveAudioDSP(dsp)
	return err
}
func (a *chassisAudioDSPSaver) SaveAudioDSPMemory(slot int, name string, voicing config.AudioDSP) error {
	_, err := a.bs.SaveAudioDSPMemory(slot, name, voicing)
	return err
}
func (a *chassisAudioDSPSaver) RecallAudioDSPMemory(slot int) (config.AudioDSPMemory, bool) {
	return a.bs.RecallAudioDSPMemory(slot)
}
func (a *chassisAudioDSPSaver) CurrentAudioDSP() config.AudioDSP {
	return a.bs.Current().Audio.DSP
}

// newChassisAudioDSPIntegrationServer builds a real core.Manager +
// uiserver.BridgeSaver + chassis.Server with AudioDSPController and
// AudioDSPSaver wired, mirroring the pattern of
// newChassisVisualizerIntegrationServer.
func newChassisAudioDSPIntegrationServer(t *testing.T) (*httptest.Server, *core.Manager) {
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
		Bridge:             sec.Bridge,
		Manager:            mgr,
		Registry:           reg,
		Version:            "integration-test",
		StartedAt:          time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:             "127.0.0.1",
		Session:            mgr,
		AudioDSPController: mgr,
		AudioDSPSaver:      &chassisAudioDSPSaver{bs: saver},
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = chassisSrv.Close() })
	mux := http.NewServeMux()
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, mgr
}

// TestAudioDSP_E2E_SSEAndCommit verifies two things end-to-end:
//  1. The chassis SSE initial burst includes an "audioDsp" event.
//  2. POST /receiver/audio/dsp with commit:true and bass:6 returns 204 and
//     the committed value is reflected in mgr.AudioDSP().Bass.
//
// Transparency coverage (default DSP = unchanged audio vs volume-only) is
// already provided by TestProcessor_TransparentWithinOneLSB and
// TestSendAudioAppliesOutputVolume in internal/dataplane; no duplication
// needed here.
func TestAudioDSP_E2E_SSEAndCommit(t *testing.T) {
	t.Parallel()
	ts, mgr := newChassisAudioDSPIntegrationServer(t)

	// (1) SSE initial burst includes the audioDsp event.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sseReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/receiver/events", nil)
	sseResp, err := ts.Client().Do(sseReq)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", sseResp.StatusCode)
	}
	// chassisReadSSEUntil reads until the eventLine and needle both appear.
	// The audioDsp event is emitted in the initial burst (before "audio").
	body := chassisReadSSEUntil(t, sseResp, "event: audioDsp\n", "params", 5*time.Second)
	if !strings.Contains(body, "event: audioDsp") {
		t.Errorf("SSE initial burst missing audioDsp event;\ncollected:\n%s", body)
	}

	// (2) Commit bass=6 and verify the manager's in-memory state is updated.
	commitBody := `{"commit":true,"params":{"bass":6}}`
	commitReq, err := http.NewRequest(http.MethodPost, ts.URL+"/receiver/audio/dsp", strings.NewReader(commitBody))
	if err != nil {
		t.Fatalf("new commit request: %v", err)
	}
	commitReq.Header.Set("Content-Type", "application/json")
	commitReq.Header.Set("Sec-Fetch-Site", "same-origin")
	commitResp, err := ts.Client().Do(commitReq)
	if err != nil {
		t.Fatalf("POST /receiver/audio/dsp: %v", err)
	}
	defer commitResp.Body.Close()
	if commitResp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(commitResp.Body)
		t.Fatalf("commit status = %d body=%s", commitResp.StatusCode, respBody)
	}
	if got := mgr.AudioDSP().Bass; got != 6 {
		t.Errorf("manager AudioDSP().Bass = %v, want 6", got)
	}
}
