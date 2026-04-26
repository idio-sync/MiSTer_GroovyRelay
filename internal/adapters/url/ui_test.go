package url

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// Note: newTestAdapter is defined in play_test.go (same package).

func TestUIRoutes_HasPlayAndPanel(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	routes := a.UIRoutes()
	have := map[string]string{}
	for _, r := range routes {
		have[r.Method+" "+r.Path] = "ok"
	}
	if _, ok := have["POST play"]; !ok {
		t.Errorf("missing POST play route: %v", have)
	}
	if _, ok := have["GET panel"]; !ok {
		t.Errorf("missing GET panel route: %v", have)
	}
}

func TestPanel_RendersIdle(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Idle") {
		t.Errorf("idle panel missing 'Idle' text: %s", body)
	}
	if !strings.Contains(body, `hx-post="/ui/adapter/url/play"`) {
		t.Errorf("panel form should hx-post to /ui/adapter/url/play: %s", body)
	}
}

func TestPanel_RendersPlaying(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.markRunning("https://example.com/video.mp4")
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Playing") {
		t.Errorf("playing panel missing 'Playing' text: %s", body)
	}
	if !strings.Contains(body, "example.com/video.mp4") {
		t.Errorf("playing panel missing URL: %s", body)
	}
}

func TestPanel_RendersError(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.setState(adapters.StateError, "probe failed: connection refused")
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "probe failed") {
		t.Errorf("error panel missing error text: %s", body)
	}
}

func TestExtraPanelHTML_EmbedsPanel(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	html := string(a.ExtraPanelHTML())
	if !strings.Contains(html, "url-panel") {
		t.Errorf("ExtraPanelHTML should include the panel; got %s", html)
	}
}

func TestRenderPanel_IncludesModeRadio(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = true
	a.ytdlpProbe = ytdlpProbe{Path: "/usr/local/bin/yt-dlp", Version: "2026.04.20", OK: true}

	html := a.renderPanel()
	for _, want := range []string{
		`name="mode"`,
		`value="auto"`,
		`value="ytdlp"`,
		`value="direct"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("panel missing %q\n%s", want, html)
		}
	}
}

func TestRenderPanel_HidesModeRadio_WhenYtdlpDisabled(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = false

	html := a.renderPanel()
	if strings.Contains(html, `name="mode"`) {
		t.Error("mode radio rendered even though YtdlpEnabled=false")
	}
}

func TestRenderPanel_HidesModeRadio_WhenProbeNotOK(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = true
	a.ytdlpProbe = ytdlpProbe{OK: false}

	html := a.renderPanel()
	if strings.Contains(html, `name="mode"`) {
		t.Error("mode radio rendered even though probe.OK=false")
	}
	if !strings.Contains(html, "yt-dlp not found") {
		t.Error("expected 'yt-dlp not found' line when probe.OK=false")
	}
}

func TestRenderPanel_VersionLine_ShownWhenProbeOK(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = true
	a.ytdlpProbe = ytdlpProbe{
		Path:    "/usr/local/bin/yt-dlp",
		Version: "2026.04.20",
		OK:      true,
	}
	html := a.renderPanel()
	if !strings.Contains(html, "yt-dlp 2026.04.20") {
		t.Errorf("version line missing\n%s", html)
	}
	if !strings.Contains(html, "/usr/local/bin/yt-dlp") {
		t.Error("path missing from version line")
	}
}

func TestRenderPanel_AutoResolvesLine(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = true
	a.ytdlpProbe = ytdlpProbe{OK: true}
	a.cfg.YtdlpHosts = []string{"youtube.com", "twitch.tv", "vimeo.com"}

	html := a.renderPanel()
	if !strings.Contains(html, "Auto-resolves") {
		t.Error("'Auto-resolves' label missing")
	}
	for _, h := range a.cfg.YtdlpHosts {
		if !strings.Contains(html, h) {
			t.Errorf("hostname %q missing from auto-resolves line", h)
		}
	}
}

func TestRenderPanel_AutoResolvesLine_TruncatesByCharBudget(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = true
	a.ytdlpProbe = ytdlpProbe{OK: true}
	// 14 long hostnames → joined > 70 chars (review fix M2).
	a.cfg.YtdlpHosts = []string{
		"youtube.com", "youtu.be", "m.youtube.com", "twitch.tv",
		"vimeo.com", "archive.org", "dailymotion.com", "soundcloud.com",
		"bandcamp.com", "ten.com", "eleven.com", "twelve.com",
		"thirteen.com", "fourteen.com",
	}
	html := a.renderPanel()
	if !strings.Contains(html, "(14 total)") {
		t.Errorf("expected '(14 total)' suffix; html:\n%s", html)
	}
}

func TestRenderPanel_CookiesSection_AutocompleteOff(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = true
	a.ytdlpProbe = ytdlpProbe{OK: true}

	html := a.renderPanel()
	// Browser-autofill defense (review fix I4): textarea AND wrapping
	// form must have autocomplete="off"; textarea also spellcheck="false".
	if !strings.Contains(html, `autocomplete="off"`) {
		t.Error("missing autocomplete=off")
	}
	if !strings.Contains(html, `spellcheck="false"`) {
		t.Error("missing spellcheck=false on cookies textarea")
	}
	if strings.Contains(html, `name="password"`) || strings.Contains(html, `name="token"`) {
		t.Error("cookies textarea name pattern-matches credential heuristics")
	}
}

func TestRenderPanel_CookiesSection_NeverEchoesContent(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if _, err := saveCookies(a.CookiesPath(), []byte(sampleCookies)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	html := a.renderPanel()
	// The textarea must render empty even when a cookies file exists.
	// Canonical literal from sampleCookies that would surface if the
	// content were echoed.
	if strings.Contains(html, "LOGIN_INFO") || strings.Contains(html, "abc123") {
		t.Error("cookies content leaked into panel HTML")
	}
}

func TestRenderPanel_CookiesStatusLine_ShowsBytesAndMtime(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if _, err := saveCookies(a.CookiesPath(), []byte(sampleCookies)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	html := a.renderPanel()
	// Some byte count near sampleCookies length should appear.
	if !strings.Contains(html, "bytes") {
		t.Error("cookies status missing 'bytes'")
	}
}

// renderForTest is a helper for the v1.5 panel-state tests below.
func renderForTest(t *testing.T, a *Adapter) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("panel status = %d, want 200", w.Code)
	}
	return w.Body.String()
}

func TestPanel_StatePlaying_RendersPauseRow(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   1 * time.Hour,
		Position:   30 * time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if !strings.Contains(body, "Playing:") || !strings.Contains(body, "example.com/v.mp4") {
		t.Errorf("lifecycle status line missing/incorrect: %s", body)
	}
	if !strings.Contains(body, ">Pause<") {
		t.Errorf("playing panel should have Pause button: %s", body)
	}
	if !strings.Contains(body, ">Stop<") {
		t.Errorf("playing panel should have Stop button: %s", body)
	}
	if !strings.Contains(body, ">Replay<") {
		t.Errorf("playing panel should have Replay button: %s", body)
	}
}

func TestPanel_StatePaused_RendersResumeRow(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePaused,
		Duration:   1 * time.Hour,
		Position:   30 * time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if !strings.Contains(body, ">Resume<") {
		t.Errorf("paused panel should have Resume button: %s", body)
	}
	if strings.Contains(body, ">Pause<") {
		t.Errorf("paused panel should NOT have Pause button: %s", body)
	}
}

func TestPanel_StateIdle_NoControlRow(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StateIdle})
	a := newTestAdapter(t, fc)
	body := renderForTest(t, a)
	for _, label := range []string{">Pause<", ">Resume<", ">Stop<", ">Replay<"} {
		if strings.Contains(body, label) {
			t.Errorf("idle panel should not have %s: %s", label, body)
		}
	}
}

func TestPanel_PositionRenderedWhenDurationPositive(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   45*time.Minute + 12*time.Second,
		Position:   1*time.Minute + 23*time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if !strings.Contains(body, "01:23 / 45:12") {
		t.Errorf("position line missing/incorrect: %s", body)
	}
}

func TestPanel_PositionFormatHHMMSS_OverOneHour(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   2 * time.Hour,
		Position:   1*time.Hour + 23*time.Minute + 45*time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if !strings.Contains(body, "01:23:45 / 02:00:00") {
		t.Errorf("HH:MM:SS format missing/incorrect: %s", body)
	}
}

func TestPanel_ScrubBarRenderedWhenDurationPositive(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   1 * time.Hour,
		Position:   30 * time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if !strings.Contains(body, `type="range"`) {
		t.Errorf("playing panel with Duration > 0 must render <input type=range>: %s", body)
	}
	if !strings.Contains(body, `hx-post="/ui/adapter/url/seek"`) {
		t.Errorf("scrub bar must hx-post to seek: %s", body)
	}
}

func TestPanel_NoScrubBarWhenDurationZero(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   0, // live
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://live.example/feed")
	body := renderForTest(t, a)
	if strings.Contains(body, `type="range"`) {
		t.Errorf("Duration == 0 panel must NOT render scrub bar: %s", body)
	}
}

func TestPanel_NoScrubBarWhenStateIdle_StateFirstRule(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StateIdle,
		Duration:   1 * time.Hour, // stale m.active leaks Duration
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	body := renderForTest(t, a)
	if strings.Contains(body, `type="range"`) {
		t.Errorf("Idle state must hide scrub bar regardless of Duration: %s", body)
	}
}

func TestPanel_ForeignAdapterRef_StopButtonDisabled(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   1 * time.Hour,
		AdapterRef: "plex:xyz",
	})
	a := newTestAdapter(t, fc)
	body := renderForTest(t, a)
	if !strings.Contains(body, "disabled") {
		t.Errorf("foreign AdapterRef should disable the control row: %s", body)
	}
}

func TestPanel_HXTrigger_FastWhenActive(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc"})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if !strings.Contains(body, `hx-trigger="every 1s"`) {
		t.Errorf("active panel should poll every 1s: %s", body)
	}
}

func TestPanel_HXTrigger_SlowWhenIdle(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StateIdle})
	a := newTestAdapter(t, fc)
	body := renderForTest(t, a)
	if !strings.Contains(body, `hx-trigger="every 5s"`) {
		t.Errorf("idle panel should poll every 5s: %s", body)
	}
}

func TestPanel_HistoryListRendered(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://a.example/1")
	a.history.AddOrBump("https://b.example/2")
	body := renderForTest(t, a)
	if !strings.Contains(body, "a.example/1") || !strings.Contains(body, "b.example/2") {
		t.Errorf("history list should render both URLs: %s", body)
	}
	if !strings.Contains(body, `hx-post="/ui/adapter/url/history/play"`) {
		t.Errorf("history Cast button should hx-post to history/play: %s", body)
	}
	if !strings.Contains(body, `hx-post="/ui/adapter/url/history/delete"`) {
		t.Errorf("history delete button should hx-post to history/delete: %s", body)
	}
}

func TestPanel_HistoryEmpty_NoListSection(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	body := renderForTest(t, a)
	if strings.Contains(body, "Recent:") {
		t.Errorf("empty history should not render the Recent section: %s", body)
	}
}

func TestPanel_RedactsCredentialsInDisplay(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://user:secret@example.com/v.mp4")
	body := renderForTest(t, a)
	if strings.Contains(body, "secret") {
		t.Errorf("password leaked into rendered panel: %s", body)
	}
	if !strings.Contains(body, "example.com") {
		t.Errorf("host stripped from redacted display: %s", body)
	}
}
