//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

	a := urladapter.New(mgr)
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
	a := urladapter.New(mgr)

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
	a := urladapter.New(mgr)

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
