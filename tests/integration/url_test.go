//go:build integration

package integration

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	urladapter "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// urlBridgeConfig returns a minimal, valid bridge config the Manager can
// run with. Aspect "letterbox" skips ProbeCrop so we don't depend on
// ffmpeg's cropdetect for the smoke test.
func urlBridgeConfig(t *testing.T) config.BridgeConfig {
	t.Helper()
	return config.BridgeConfig{
		DataDir: t.TempDir(),
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			RGBMode:             "rgb888",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			LZ4Enabled:          false,
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
	}
}

// newURLAdapter wires the new AdapterConfig signature for the
// integration tests — they all need DataDir set (the config helper
// above provides it via t.TempDir).
func newURLAdapter(t *testing.T, mgr *core.Manager) *urladapter.Adapter {
	t.Helper()
	a, err := urladapter.New(urladapter.AdapterConfig{
		Bridge: urlBridgeConfig(t),
		Core:   mgr,
	})
	if err != nil {
		t.Fatalf("urladapter.New: %v", err)
	}
	return a
}

// TestURL_PlayDirectFile spins up an httptest.Server serving the tiny
// MP4 fixture, posts the URL through the URL adapter, and asserts that
// the data plane initialises against fake-mister (Init + Switchres
// observed) before the short clip ends.
func TestURL_PlayDirectFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg plane test requires Unix ExtraFiles; run on Linux/CI")
	}

	h := NewHarness(t)
	// Enable ACKs so the data plane's SendInitAwaitACK handshake
	// completes deterministically rather than racing the test's 5s
	// observation window. Matches the scenario harness convention.
	h.Listener.EnableACKs(true)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)

	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(mp4Path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(srv.Close)

	a := newURLAdapter(t, mgr)
	form := url.Values{"url": {srv.URL + "/tiny.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("/play status = %d, want 202: body=%s", w.Code, w.Body.String())
	}

	// Wait up to 5s for at least one Init + one Switchres on the wire.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := h.Recorder.Snapshot()
		if snap.Counts[groovy.CmdInit] >= 1 && snap.Counts[groovy.CmdSwitchres] >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] < 1 {
		t.Errorf("Init count = %d, want >= 1", snap.Counts[groovy.CmdInit])
	}
	if snap.Counts[groovy.CmdSwitchres] < 1 {
		t.Errorf("Switchres count = %d, want >= 1", snap.Counts[groovy.CmdSwitchres])
	}

	// Stop the manager so the plane goroutine exits before the test ends.
	_ = mgr.Stop()
}

// TestURL_RejectsBadScheme: posting a file:// URL yields 400 and never
// reaches the data plane.
func TestURL_RejectsBadScheme(t *testing.T) {
	h := NewHarness(t)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)
	a := newURLAdapter(t, mgr)

	form := url.Values{"url": {"file:///etc/passwd"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	// 100ms is plenty for any spurious wire activity to surface.
	time.Sleep(100 * time.Millisecond)
	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] != 0 {
		t.Errorf("Init count = %d, want 0 (no plane should have started)", snap.Counts[groovy.CmdInit])
	}
}

// TestURL_ProbeTimeout: an httptest.Server whose handler hangs longer
// than the Manager's 10s probe ceiling should yield a 500 within
// ~2 * probeTimeout. Asserts the timeout actually fires AND no plane
// starts.
func TestURL_ProbeTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("ProbeTimeout test takes ~12s; skipping in -short mode")
	}
	h := NewHarness(t)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)
	a := newURLAdapter(t, mgr)

	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the test ends — well past the 10s probe ceiling.
		select {
		case <-r.Context().Done():
		case <-hang:
		}
	}))
	t.Cleanup(srv.Close)

	form := url.Values{"url": {srv.URL + "/forever.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	start := time.Now()
	// Run handler in a goroutine with a context-bounded wait so a bug
	// that lets the handler hang forever still fails the test.
	done := make(chan struct{})
	go func() {
		a.UIRoutes()[0].Handler(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("handler did not return within 20s — probe timeout broken?")
	}
	elapsed := time.Since(start)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if elapsed < 8*time.Second {
		t.Errorf("handler returned in %v — probe timeout too short?", elapsed)
	}
	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] != 0 {
		t.Errorf("Init count = %d, want 0 (no plane should have started)", snap.Counts[groovy.CmdInit])
	}
}

// TestURL_PreemptsPlex_TimelineReportsStopped is the C1 contract test
// from the spec (§"Plex precursor"). It:
//  1. Stands up a fake controller HTTP endpoint and subscribes it to
//     the Plex timeline broker.
//  2. Starts a "Plex" session by directly calling Manager.StartSession
//     with a request whose OnStop is the same closure sessionRequestFor
//     builds (so we exercise the broadcast-on-stop wiring).
//  3. POSTs a URL to the URL adapter, which preempts.
//  4. Asserts the controller received a stopped timeline addressed to
//     the prior media key during the preempt window.
func TestURL_PreemptsPlex_TimelineReportsStopped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg plane test requires Unix ExtraFiles; run on Linux/CI")
	}

	h := NewHarness(t)
	// Enable ACKs so the data plane's INIT handshake completes
	// deterministically (matches D2's TestURL_PlayDirectFile pattern).
	h.Listener.EnableACKs(true)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)

	// Controller endpoint — collects timeline POSTs.
	var mu sync.Mutex
	var bodies []string
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	t.Cleanup(ctrl.Close)
	cu, _ := url.Parse(ctrl.URL)
	chost, cport, _ := net.SplitHostPort(cu.Host)

	// Build a real Plex Companion + TimelineBroker pointed at the manager.
	companion := plex.NewCompanion(plex.CompanionConfig{
		DeviceName: "MiSTer", DeviceUUID: "uuid-1", ProfileName: "Plex Home Theater",
	}, mgr)
	broker := plex.NewTimelineBroker(plex.TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		mgr.Status)
	broker.SetPlayContextProvider(companion.LastPlaySessionForTest) // exposed test helper
	companion.SetTimeline(broker)
	broker.Subscribe("client-a", chost, cport, "http", 0)

	// MP4 server reused from D2's harness pattern.
	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _ := os.Open(mp4Path)
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(srv.Close)

	// Fake "Plex play" — call sessionRequestFor through Companion's
	// exported test entry point, then StartSession. Port "1" matches
	// the unit test in A2: guaranteed-unreachable across platforms so
	// StopTranscodeSession's TCP connect fails fast (refused), avoiding
	// dependence on whether a real PMS happens to be running on the
	// developer's box.
	priorPlay := plex.PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-1", PlexToken: "tok",
	}
	plexReq := companion.SessionRequestForTest(priorPlay) // exposed test helper
	// Override StreamURL to the local MP4 — we don't want the test
	// reaching out to a real PMS.
	plexReq.StreamURL = srv.URL + "/tiny.mp4"
	companion.RememberPlaySessionForTest(priorPlay) // exposed test helper
	if err := mgr.StartSession(plexReq); err != nil {
		t.Fatalf("plex StartSession: %v", err)
	}

	// Drain any baseline pushes from broker startup. The broker's
	// 1Hz tick is NOT running in this test (no RunBroadcastLoop), so
	// there shouldn't be any baseline pushes — but reset bodies
	// defensively in case a future change adds startup notifications.
	mu.Lock()
	bodies = nil
	mu.Unlock()

	// URL preempts.
	urlAdapter := newURLAdapter(t, mgr)
	form := url.Values{"url": {srv.URL + "/tiny.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	urlAdapter.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("url /play status = %d, want 202: %s", w.Code, w.Body.String())
	}

	// notifySessionStop fires Plex's OnStop in a goroutine
	// (manager.go:38-43). Poll for the stopped-timeline push instead
	// of using a fixed sleep — Windows CI in particular can have
	// variable connection-refused latency for the inner
	// StopTranscodeSession call.
	deadline := time.Now().Add(5 * time.Second)
	stoppedCount := 0
	for time.Now().Before(deadline) {
		mu.Lock()
		stoppedCount = 0
		for _, b := range bodies {
			if strings.Contains(b, `state="stopped"`) && strings.Contains(b, `key="/library/metadata/42"`) {
				stoppedCount++
			}
		}
		mu.Unlock()
		if stoppedCount >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stoppedCount != 1 {
		mu.Lock()
		bodiesSnapshot := append([]string(nil), bodies...)
		mu.Unlock()
		t.Errorf("stopped-timeline-for-prior-key count = %d during preempt window, want 1; bodies: %v",
			stoppedCount, bodiesSnapshot)
	}

	_ = mgr.Stop()
}

// TestURL_CapCheckRejectsCrossAdapterPause: with a URL session active,
// calling Manager.Pause directly returns the cap-check error. Proves
// the cap check is the wall, not the data plane (spec §"Boundary-
// validation tests").
func TestURL_CapCheckRejectsCrossAdapterPause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg plane test requires Unix ExtraFiles; run on Linux/CI")
	}

	h := NewHarness(t)
	h.Listener.EnableACKs(true)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)

	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _ := os.Open(mp4Path)
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(srv.Close)

	a := newURLAdapter(t, mgr)
	form := url.Values{"url": {srv.URL + "/tiny.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}

	// The URL session is now active. Pause must be rejected.
	if err := mgr.Pause(); err == nil {
		t.Error("Manager.Pause returned nil for URL session; cap-check failed")
	} else if !strings.Contains(err.Error(), "pause") {
		t.Errorf("Pause error = %q, want a 'pause' message", err)
	}
	// Same for SeekTo.
	if err := mgr.SeekTo(5000); err == nil {
		t.Error("Manager.SeekTo returned nil for URL session; cap-check failed")
	} else if !strings.Contains(err.Error(), "seek") {
		t.Errorf("SeekTo error = %q, want a 'seek' message", err)
	}

	_ = mgr.Stop()
}
