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

func TestUIRoutes_AllPanelRoutesRegistered(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	routes := a.UIRoutes()
	if len(routes) != 6 {
		t.Fatalf("UIRoutes count = %d, want 6", len(routes))
	}
	have := map[string]string{}
	for _, r := range routes {
		have[r.Method+" "+r.Path] = "ok"
	}
	want := []string{
		"POST play",
		"POST history/play",
		"POST history/delete",
		"GET panel",
		"POST cookies",
		"DELETE cookies",
	}
	for _, w := range want {
		if _, ok := have[w]; !ok {
			t.Errorf("missing route %q; have: %v", w, have)
		}
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
	if strings.Contains(body, `hx-post="/old_ui/adapter/url/play"`) || strings.Contains(body, `name="url"`) {
		t.Errorf("URL panel should not render the page-local launch form: %s", body)
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

func TestRenderPanel_DoesNotRenderLaunchModeRadio(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	a.cfg.YtdlpEnabled = true
	a.ytdlpProbe = ytdlpProbe{Path: "/usr/local/bin/yt-dlp", Version: "2026.04.20", OK: true}

	html := a.renderPanel()
	for _, forbidden := range []string{
		`name="mode"`,
		`value="auto"`,
		`value="ytdlp"`,
		`value="direct"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("URL panel should not render launch mode control %q\n%s", forbidden, html)
		}
	}
	for _, want := range []string{"Auto-resolves", "yt-dlp 2026.04.20"} {
		if !strings.Contains(html, want) {
			t.Errorf("panel lost expected status text %q\n%s", want, html)
		}
	}
}

func TestQuickCastTabsOmitsHLSBufferControl(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	tabs := a.QuickCastTabs()
	if len(tabs) != 1 {
		t.Fatalf("QuickCastTabs len = %d, want 1", len(tabs))
	}
	for _, field := range tabs[0].Fields {
		if field.Name == "hls_buffer" {
			t.Fatalf("QuickCastTabs should not render hls_buffer field; got %+v", field)
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

func TestPanel_StatePlaying_HidesTransportRow(t *testing.T) {
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
	for _, label := range []string{">Pause<", ">Stop<", ">Replay<"} {
		if strings.Contains(body, label) {
			t.Errorf("playing panel should not have transport button %s: %s", label, body)
		}
	}
}

func TestPanel_StatePaused_HidesResumeRow(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePaused,
		Duration:   1 * time.Hour,
		Position:   30 * time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	for _, label := range []string{">Resume<", ">Pause<"} {
		if strings.Contains(body, label) {
			t.Errorf("paused panel should not have transport button %s: %s", label, body)
		}
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

func TestPanel_PositionHiddenWhenDurationPositive(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   45*time.Minute + 12*time.Second,
		Position:   1*time.Minute + 23*time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if strings.Contains(body, "01:23 / 45:12") {
		t.Errorf("URL panel should not render active position line: %s", body)
	}
}

func TestPanel_PositionFormatHHMMSS_OverOneHourHidden(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   2 * time.Hour,
		Position:   1*time.Hour + 23*time.Minute + 45*time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if strings.Contains(body, "01:23:45 / 02:00:00") {
		t.Errorf("URL panel should not render active position line: %s", body)
	}
}

func TestPanel_ScrubBarHiddenWhenDurationPositive(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		Duration:   1 * time.Hour,
		Position:   30 * time.Second,
		AdapterRef: "url:abc",
	})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if strings.Contains(body, `type="range"`) || strings.Contains(body, `hx-post="/old_ui/adapter/url/seek"`) {
		t.Errorf("URL panel should not render scrub controls: %s", body)
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
	if strings.Contains(body, `/old_ui/adapter/url/stop`) {
		t.Errorf("foreign AdapterRef should not render URL panel transport controls: %s", body)
	}
}

func TestPanel_HXTrigger_SlowWhenActive(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc"})
	a := newTestAdapter(t, fc)
	a.markRunning("https://example.com/v.mp4")
	body := renderForTest(t, a)
	if !strings.Contains(body, `hx-trigger="every 5s"`) {
		t.Errorf("URL panel should keep slow polling when active: %s", body)
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
	if strings.Contains(body, `hx-post="/old_ui/adapter/url/history/play"`) || strings.Contains(body, ">Cast<") {
		t.Errorf("history list should not render page-local recast controls: %s", body)
	}
	if !strings.Contains(body, `hx-post="/old_ui/adapter/url/history/delete"`) {
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

func TestPanel_HistoryEntry_RendersTitleWhenSet(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://youtu.be/abc")
	a.history.SetTitle("https://youtu.be/abc", "Big Buck Bunny")
	body := renderForTest(t, a)
	if !strings.Contains(body, "Big Buck Bunny") {
		t.Errorf("history list missing title: %s", body)
	}
	if !strings.Contains(body, "history-title") {
		t.Errorf("history list missing .history-title wrapper: %s", body)
	}
	if !strings.Contains(body, "youtu.be/abc") {
		t.Errorf("history list still must show URL alongside title: %s", body)
	}
}

func TestPanel_HistoryEntry_NoTitleClassWhenAbsent(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://example.com/raw.mp4")
	body := renderForTest(t, a)
	if strings.Contains(body, "history-title") {
		t.Errorf("untitled entry rendered .history-title wrapper: %s", body)
	}
	if !strings.Contains(body, "example.com/raw.mp4") {
		t.Errorf("history list missing URL for untitled entry: %s", body)
	}
}

func TestPanel_HistoryEntry_TitleHTMLEscaped(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://example.com/v")
	a.history.SetTitle("https://example.com/v", `<script>alert(1)</script>`)
	body := renderForTest(t, a)
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("title rendered without HTML escaping: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("title not HTML-escaped: %s", body)
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

func TestURLPanelDoesNotRenderActiveTransportControls(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 3, Duration: time.Minute}}
	a := &Adapter{core: coreStub, history: LoadHistory("")}
	html := a.renderPanel()
	for _, forbidden := range []string{
		"/old_ui/adapter/url/pause",
		"/old_ui/adapter/url/resume",
		"/old_ui/adapter/url/stop",
		"/old_ui/adapter/url/replay",
		"/old_ui/adapter/url/seek",
		`hx-post="/old_ui/adapter/url/play"`,
		`hx-post="/old_ui/adapter/url/history/play"`,
		`name="url"`,
		">Play<",
		">Cast<",
		`class="scrub"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("URL panel still renders %q: %s", forbidden, html)
		}
	}
	for _, want := range []string{"yt-dlp", "cookies"} {
		if !strings.Contains(html, want) {
			t.Fatalf("URL panel lost expected %q: %s", want, html)
		}
	}
}
