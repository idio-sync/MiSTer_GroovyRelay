//go:build integration

package integration

import (
	"context"
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
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// stubResolver records calls and returns canned Resolutions.
type stubResolver struct {
	res     *ytdlp.Resolution
	callsOK int
}

func (s *stubResolver) Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error) {
	s.callsOK++
	return s.res, nil
}

// urlPlayHandler returns the POST /play handler for a given adapter.
func urlPlayHandler(t *testing.T, a *urladapter.Adapter) http.HandlerFunc {
	t.Helper()
	for _, r := range a.UIRoutes() {
		if r.Method == "POST" && r.Path == "play" {
			return r.Handler
		}
	}
	t.Fatal("POST play route not found")
	return nil
}

func postPlay(t *testing.T, h http.HandlerFunc, mediaURL, mode string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"url": {mediaURL}, "mode": {mode}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// waitForInit polls h.Recorder for at least one Init+Switchres within
// a deadline. Mirrors the existing TestURL_PlayDirectFile pattern at
// tests/integration/url_test.go:88.
func waitForInit(t *testing.T, h *Harness, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		snap := h.Recorder.Snapshot()
		if snap.Counts[groovy.CmdInit] >= 1 && snap.Counts[groovy.CmdSwitchres] >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := h.Recorder.Snapshot()
	t.Fatalf("no Init+Switchres within %v: counts=%+v", d, snap.Counts)
}

// TestURL_YtdlpResolve_DirectionMatrix exercises mode dispatch end-to-end.
// Real ffmpeg + real fake-mister; resolver stubbed.
func TestURL_YtdlpResolve_DirectionMatrix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg plane test requires Unix ExtraFiles; run on Linux/CI")
	}

	// Tiny mp4 fixture (already used by TestURL_PlayDirectFile).
	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(mp4Path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(mediaSrv.Close)

	t.Run("direct mode does not invoke resolver", func(t *testing.T) {
		h := NewHarness(t)
		h.Listener.EnableACKs(true)
		mgr := core.NewManager(urlBridgeConfig(t), h.Sender)
		t.Cleanup(func() { _ = mgr.Stop() })

		a, err := urladapter.New(urladapter.AdapterConfig{
			Bridge: urlBridgeConfig(t),
			Core:   mgr,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		stub := &stubResolver{res: &ytdlp.Resolution{URL: mediaSrv.URL + "/x.mp4"}}
		a.SetResolverForTesting(stub)
		a.SetYtdlpProbeForTesting(urladapter.YtdlpProbe{
			Path: "/stub/yt-dlp", Version: "stub", OK: true,
		})

		w := postPlay(t, urlPlayHandler(t, a), mediaSrv.URL+"/direct.mp4", "direct")
		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
		if stub.callsOK != 0 {
			t.Errorf("direct mode: resolver called %d times, want 0", stub.callsOK)
		}
		waitForInit(t, h, 5*time.Second)
	})

	t.Run("ytdlp mode invokes resolver and casts resolved URL", func(t *testing.T) {
		h := NewHarness(t)
		h.Listener.EnableACKs(true)
		mgr := core.NewManager(urlBridgeConfig(t), h.Sender)
		t.Cleanup(func() { _ = mgr.Stop() })

		a, err := urladapter.New(urladapter.AdapterConfig{
			Bridge: urlBridgeConfig(t),
			Core:   mgr,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		stub := &stubResolver{res: &ytdlp.Resolution{URL: mediaSrv.URL + "/resolved.mp4"}}
		a.SetResolverForTesting(stub)
		a.SetYtdlpProbeForTesting(urladapter.YtdlpProbe{
			Path: "/stub/yt-dlp", Version: "stub", OK: true,
		})

		w := postPlay(t, urlPlayHandler(t, a), "https://example.com/page", "ytdlp")
		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
		if stub.callsOK != 1 {
			t.Errorf("ytdlp mode: resolver calls = %d, want 1", stub.callsOK)
		}
		// Real cast actually started — Init+Switchres reach fake-mister
		// (review fix I2: third leg coverage gap closed).
		waitForInit(t, h, 5*time.Second)
	})

	t.Run("auto mode + non-allowlisted host casts directly without resolver", func(t *testing.T) {
		h := NewHarness(t)
		h.Listener.EnableACKs(true)
		mgr := core.NewManager(urlBridgeConfig(t), h.Sender)
		t.Cleanup(func() { _ = mgr.Stop() })

		a, err := urladapter.New(urladapter.AdapterConfig{
			Bridge: urlBridgeConfig(t),
			Core:   mgr,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		stub := &stubResolver{res: &ytdlp.Resolution{URL: "should-not-be-used"}}
		a.SetResolverForTesting(stub)
		a.SetYtdlpProbeForTesting(urladapter.YtdlpProbe{
			Path: "/stub/yt-dlp", Version: "stub", OK: true,
		})

		// example.com is NOT in the default allowlist.
		w := postPlay(t, urlPlayHandler(t, a), mediaSrv.URL+"/auto.mp4", "auto")
		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d", w.Code)
		}
		if stub.callsOK != 0 {
			t.Errorf("auto+non-allowlist: resolver wrongly invoked")
		}
		// Real cast must still happen (review fix I2).
		waitForInit(t, h, 5*time.Second)
	})
}
