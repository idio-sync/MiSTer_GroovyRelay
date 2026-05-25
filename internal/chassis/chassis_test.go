package chassis

import (
	"bytes"
	"context"
	"html/template"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/playback"
)

type fakeVisualizerViewer struct {
	mu   sync.Mutex
	mode string
}

func (f *fakeVisualizerViewer) VisualizerMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *fakeVisualizerViewer) set(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

type fakeVisualizerSaver struct {
	mu    sync.Mutex
	saved []string
	err   error
}

func (f *fakeVisualizerSaver) SaveVisualizerMode(mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, mode)
	return f.err
}

func (f *fakeVisualizerSaver) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.saved))
	copy(out, f.saved)
	return out
}

// nonZeroConfig returns a Config valid enough for New(). Tests that
// want to assert error paths shadow individual fields with zero values.
func nonZeroConfig() Config {
	return Config{
		Bridge:    config.BridgeConfig{UI: config.UIConfig{HTTPPort: 32500}},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "test-1.0.0",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "10.0.0.5",
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNew_ReturnsServerWithValidConfig(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("New returned nil Server with no error")
	}
}

func TestServer_StoresVisualizerViewerAndSaverFromConfig(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	saver := &fakeVisualizerSaver{}
	cfg.VisualizerViewer = viewer
	cfg.VisualizerSaver = saver

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.visualizerViewer != viewer {
		t.Errorf("Server.visualizerViewer not stored from Config")
	}
	if s.visualizerSaver != saver {
		t.Errorf("Server.visualizerSaver not stored from Config")
	}
}

func TestNew_RejectsZeroStartedAt(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.StartedAt = time.Time{}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for zero StartedAt, got nil")
	}
}

func TestNew_RejectsEmptyVersion(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Version = ""
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for empty Version, got nil")
	}
}

func TestNew_AllowsEmptyHostIP(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.HostIP = ""
	_, err := New(cfg)
	if err != nil {
		t.Fatalf("New should accept empty HostIP, got: %v", err)
	}
}

func TestInit_RegistersWoff2MIME(t *testing.T) {
	t.Parallel()
	// Package init must register font/woff2 so http.FileServer's
	// content-type lookup succeeds on hosts where the system MIME
	// database doesn't know woff2 (Alpine, scratch containers).
	got := mime.TypeByExtension(".woff2")
	if got != "font/woff2" {
		t.Fatalf(`TypeByExtension(".woff2") = %q, want "font/woff2"`, got)
	}
}

func TestInit_RegistersWoffMIME(t *testing.T) {
	t.Parallel()
	got := mime.TypeByExtension(".woff")
	if got != "font/woff" {
		t.Fatalf(`TypeByExtension(".woff") = %q, want "font/woff"`, got)
	}
}

func TestIdleSnapshot_AllFieldsPopulated(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.UI.HTTPPort = 32500
	cfg.Bridge.Visualizer.Mode = config.VisualizerModeStereoScope
	got := idleSnapshot(cfg, fixedNow)

	want := ReceiverPageData{
		Version:   "test-1.0.0",
		BrandName: "GROOVY · RELAY",
		State:     StateIdle,
		HostInfo: HostInfo{
			HostIP:   "10.0.0.5",
			HTTPPort: 32500,
		},
		VFD: VFDData{
			State:        string(StateIdle),
			Title:        "STANDBY",
			Marquee:      "MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET",
			QueueCurrent: 0,
			QueueTotal:   0,
			SystemTime:   "22:47",
			Uptime:       "10H 47M",
		},
		Source: SourceData{
			Buttons: []SourceButton{
				{Label: "STREAMS", Active: true, Lit: false},
				{Label: "PLEX", Active: false, Lit: false},
				{Label: "JELLYFIN", Active: false, Lit: false},
				{Label: "DLNA", Active: false, Lit: false},
				{Label: "AUX", Active: false, Lit: false, Action: SourceActionAUXStart},
			},
		},
		Meter: MeterData{
			State: string(StateIdle),
			SourceStrip: SourceStripIdleData{
				AudioIn:   "---",
				AudioOut:  "---",
				Src:       "---",
				Crop:      "---",
				HLSBuffer: "0 / 0 SEG",
				Drops:     "0.0",
			},
			MidRow: MidRowIdleData{
				BitrateMbps:   "0.0",
				FreqKHz:       "---",
				Mode:          "---",
				StandardNTSC:  true,
				StandardPAL:   false,
				FieldFlip:     "idle",
				ThroughputMBs: "0.0",
				MSAck:         "--",
			},
			Readout: ReadoutIdleData{
				LRBars:      0,
				PhaseNeedle: "0",
				LUFS:        "---",
				Output:      "---",
				Aspect:      "---",
				Pipe:        "---",
				Speed:       "---",
				Link:        "---",
			},
		},
		Transport: TransportData{
			State:           "stopped",
			SeekFillPercent: 0,
			ElapsedTime:     "",
			TotalTime:       "",
			PercentPlayed:   "",
			OffsetMS:        0,
			DurationMS:      0,
			ActionsEnabled:  ActionsEnabled{},
			AdapterRef:      "",
			Generation:      0,
		},
		Visualizer: VisualizerData{
			ActiveMode: config.VisualizerModeStereoScope,
			Buttons: []VisualizerButton{
				{Mode: "retro_analyzer", Label: "ANALYZER", IconKind: "analyzer", IsPreview: false},
				{Mode: "oscilloscope_wave", Label: "OSCILLOSCOPE", IconKind: "wave", IsPreview: false},
				{Mode: "stereo_scope", Label: "STEREO SCOPE", IconKind: "scope", IsPreview: false},
				{Mode: "radial_spectrum", Label: "RADIAL", IconKind: "radial", IsPreview: true},
			},
		},
		Input: InputData{
			PastePlaceholder: "Paste URL or magnet",
			DetectedKind:     "URL",
			CastEnabled:      false,
		},
		Presets: PresetsData{
			ModeLabel: "Memory · 0 / 12 slots",
			Count:     "★ 0",
		},
		History: HistoryData{
			Rows:         nil,
			EmptyMessage: "No recent casts",
		},
		Settings: SettingsData{
			Open: false,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idleSnapshot() = %+v, want %+v", got, want)
	}
}

func TestIdleSnapshot_TransportDataMatchesNewIdleShape(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	got := idleSnapshot(cfg, fixedNow)

	want := TransportData{
		State:           "stopped",
		SeekFillPercent: 0,
		ElapsedTime:     "",
		TotalTime:       "",
		PercentPlayed:   "",
		OffsetMS:        0,
		DurationMS:      0,
		ActionsEnabled:  ActionsEnabled{},
		AdapterRef:      "",
		Generation:      0,
	}
	if !reflect.DeepEqual(got.Transport, want) {
		t.Errorf("idleSnapshot Transport mismatch:\n got: %+v\nwant: %+v", got.Transport, want)
	}
}

func TestIdleSnapshot_DeterministicGivenSameNow(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	a := idleSnapshot(cfg, fixedNow)
	b := idleSnapshot(cfg, fixedNow)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("idleSnapshot is not deterministic: a=%+v b=%+v", a, b)
	}
}

func TestIdleSnapshot_EmptyHostIPRendersAsOFFLINE(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.HostIP = ""
	got := idleSnapshot(cfg, fixedNow)
	if got.HostInfo.HostIP != "OFFLINE" {
		t.Errorf("HostInfo.HostIP with empty cfg.HostIP = %q, want OFFLINE", got.HostInfo.HostIP)
	}
}

func TestIdleSnapshot_InvalidVisualizerModeFallsBack(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = "bogus"
	got := idleSnapshot(cfg, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeRetroAnalyzer {
		t.Errorf("Visualizer.ActiveMode = %q, want %q", got.Visualizer.ActiveMode, config.VisualizerModeRetroAnalyzer)
	}
}

func TestIdleSnapshot_RadialPreviewModeFallsBack(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = "radial_spectrum"
	got := idleSnapshot(cfg, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeRetroAnalyzer {
		t.Errorf("Visualizer.ActiveMode = %q, want %q for preview-only radial_spectrum", got.Visualizer.ActiveMode, config.VisualizerModeRetroAnalyzer)
	}
}

func TestIdleSnapshot_UptimeFromStartedAt(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 5, 21, 18, 35, 0, 0, time.UTC)
	now := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.StartedAt = startedAt
	got := idleSnapshot(cfg, now)
	if got.VFD.Uptime != "4H 12M" {
		t.Errorf("VFD.Uptime = %q, want 4H 12M", got.VFD.Uptime)
	}
}

func TestTemplatesParse(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	if tmpl.Lookup("shell.html") == nil {
		t.Error("shell.html not found in parsed templates")
	}
}

func TestTemplatesExpectedHelpersAvailable(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	// Render a tiny harness that exercises each helper. If a helper is
	// missing or renamed, html/template fails to execute and we see it
	// here instead of in a later handler test where the failure mode
	// is harder to diagnose.
	probes := []struct {
		name, src string
	}{
		{"inc", `{{inc 0}}`},
		{"hasString", `{{hasString (list "a" "b") "b"}}`},
		{"replaceAll", `{{replaceAll "a/b" "/" "-"}}`},
		{"pad2", `{{pad2 5}}`},
		{"dim", `{{dim true}}`},
		{"htmlComment", `{{htmlComment "chassis:test"}}`},
		{"until", `{{range until 3}}x{{end}}`},
	}
	for _, p := range probes {
		_, err := tmpl.New("probe-" + p.name).Parse(p.src)
		if err != nil {
			t.Fatalf("probe parse %s: %v", p.name, err)
		}
	}
	for _, p := range probes {
		var sb strings.Builder
		if err := tmpl.ExecuteTemplate(&sb, "probe-"+p.name, nil); err != nil {
			t.Errorf("helper %q execute: %v", p.name, err)
		}
	}
}

func TestPreprocessCSS_SubstitutesVersionPlaceholder(t *testing.T) {
	t.Parallel()
	src := []byte(`@font-face { src: url('/receiver/static/fonts/Inter-Variable.woff2?v={{.Version}}'); }`)
	got, err := preprocessCSS(src, "test-1.2.3")
	if err != nil {
		t.Fatalf("preprocessCSS: %v", err)
	}
	want := "?v=test-1.2.3"
	if !strings.Contains(string(got), want) {
		t.Errorf("preprocessCSS output = %s, want substring %s", got, want)
	}
	if strings.Contains(string(got), "{{.Version}}") {
		t.Errorf("preprocessCSS left a raw placeholder in output: %s", got)
	}
}

func TestPreprocessCSS_LeavesCSSCommentsAlone(t *testing.T) {
	t.Parallel()
	// html/template would mangle "<=" inside CSS comments; text/template
	// must not. This test locks that distinction in.
	src := []byte(`/* breakpoint <= 1180px */`)
	got, err := preprocessCSS(src, "v1")
	if err != nil {
		t.Fatalf("preprocessCSS: %v", err)
	}
	if !strings.Contains(string(got), `<=`) {
		t.Errorf("preprocessCSS escaped <= in CSS comment: %s", got)
	}
}

func TestChassisCSS_UsesOnlyHostedFontFamilies(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	for _, banned := range []string{
		"fonts.googleapis.com",
		"fonts.gstatic.com",
		"Orbitron",
		"Major Mono",
		"JetBrains",
	} {
		if strings.Contains(string(css), banned) {
			t.Errorf("static/chassis.css contains banned font reference %q", banned)
		}
	}
}

func TestChassisCSS_DoesNotGenerateReadableGhostSegments(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	if strings.Contains(string(css), "attr(data-ghost)") {
		t.Fatal("CSS must not generate decorative segment ghost text from data-ghost")
	}
}

func TestChassisCSS_TransportNarrowLayoutAndPreviewDisabled(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(css)
	for _, want := range []string{
		`@container chassis (max-width: 420px)`,
		`"label controls controls"`,
		`"label seek gear"`,
		`grid-area: label;`,
		`grid-area: controls;`,
		`grid-area: seek;`,
		`grid-area: gear;`,
		`body.receiver .seek-time`,
		`display: none;`,
		`body.receiver .viz-btn--preview:disabled`,
		`cursor: not-allowed;`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chassis.css missing transport/preview contract %q", want)
		}
	}
}

func TestChassisCSS_VisualizerPressedStateScoped(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(css)
	for _, want := range []string{
		`body.receiver .viz-btn {`,
		`body.receiver .viz-btn.pressed {`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chassis.css missing visualizer pressed-state selector %q", want)
		}
	}
}

func TestChassisJS_RuntimeContracts(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/chassis.js")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.js): %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"window.Chassis = { State, animators };",
		"const State = {",
		"IDLE: 'idle'",
		"LIVE: 'live'",
		"current()",
		"document.body.classList.contains('live')",
		"set(next)",
		"if (next !== State.IDLE && next !== State.LIVE) {",
		"document.body.classList.remove('idle', 'live')",
		"document.body.classList.add(next)",
		"animators.notify(next)",
		"const animators = {",
		"items: []",
		"register(animator)",
		"animator.handleState(State.current())",
		"notify(state)",
		"this.items.slice().forEach((animator) => {",
		"document.querySelector('[data-system-time]')",
		"if (!el) {",
		"const scheduleNextTick = () => {",
		"const msUntilNextMinute = 60_000 - (Date.now() % 60_000);",
		"setTimeout(() => {",
		"scheduleNextTick();",
		"new URLSearchParams(location.search).get('dev') === '1'",
		"btn.id = 'chassis-dev-state-toggle'",
		"[dev] state:",
		"document.addEventListener('DOMContentLoaded'",
		"installSourceActions()",
		`[data-source-action="aux-start"]`,
		"/receiver/aux/start",
		"application/x-www-form-urlencoded",
		"data-input-id",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("static/chassis.js missing runtime contract %q", want)
		}
	}
	for _, unwanted := range []string{
		"setInterval(tick, 60_000)",
	} {
		if strings.Contains(text, unwanted) {
			t.Errorf("static/chassis.js contains obsolete runtime contract %q", unwanted)
		}
	}
}

func TestChassisJSInstallsAUXSourceAction(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/chassis.js")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.js): %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"function installSourceActions()",
		`document.querySelectorAll('[data-source-action="aux-start"]')`,
		`fetch('/receiver/aux/start'`,
		"URLSearchParams",
		"input_id",
		".source-cluster .hw-btn",
		"classList.add('active', 'lit')",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("static/chassis.js missing AUX source action contract %q", want)
		}
	}
}

func TestVisualizerBankJS_RuntimeContracts(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/visualizer-bank.js")
	if err != nil {
		t.Fatalf("ReadFile(static/visualizer-bank.js): %v", err)
	}
	text := string(js)
	for _, want := range []string{
		"window.Chassis",
		"chassis:eventsource",
		"/receiver/visualizer",
		"data-viz",
		"viz-btn--preview",
		"queuedMode",
		"res.status !== 204",
		"visualizer-bank: save failed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("static/visualizer-bank.js missing runtime contract %q", want)
		}
	}
}

func TestHandleStatic_JS_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/chassis.js", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") && !strings.HasPrefix(got, "application/javascript") {
		t.Fatalf("Content-Type = %q, want JavaScript content type", got)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"window.Chassis = { State, animators };",
		"document.querySelector('[data-system-time]')",
		"URLSearchParams(location.search)",
		"chassis-dev-state-toggle",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("served chassis.js missing %q", want)
		}
	}
}

func TestHandleStatic_TransportJSServed(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/transport.js", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"chassis:eventsource",
		"data-transport-action",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("served transport.js missing %q", want)
		}
	}
}

func TestTransportJS_SeekUsesRawDurationMsNotClockText(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/transport.js", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "data-transport-duration-ms") {
		t.Errorf("served transport.js missing raw duration hook")
	}
	for _, unwanted := range []string{
		".split(':')",
		"parseClockToMs",
		"MM:SS",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("served transport.js contains clock parsing sentinel %q", unwanted)
		}
	}
}

func TestTransportJS_RefusesPauseResumeClickWhenStopped(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/transport.js", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "transportState === 'stopped'") {
		t.Errorf("served transport.js missing stopped-state pause/resume guard")
	}
}

func TestTransportJS_SeekDragCapturesSessionIdentity(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/transport.js")
	if err != nil {
		t.Fatalf("ReadFile(static/transport.js): %v", err)
	}
	body := string(js)
	for _, want := range []string{
		"adapterRef: adapterRef",
		"generation: generation",
		"durationMs: durationMs",
		"adapterRef !== drag.adapterRef",
		"generation !== drag.generation",
		"drag.durationMs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transport.js missing seek session guard %q", want)
		}
	}
}

func TestTransportJS_SeekCancelRestoresServerBackedVisual(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/transport.js")
	if err != nil {
		t.Fatalf("ReadFile(static/transport.js): %v", err)
	}
	body := string(js)
	for _, want := range []string{
		"let serverSeekPercent = 0",
		"serverSeekPercent = data.seekFillPercent || 0",
		"function restoreSeekVisual(bar)",
		"restoreSeekVisual(bar);",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transport.js missing seek visual restore contract %q", want)
		}
	}
}

func TestHandleIndex_RendersShell200(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html prefix", got)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<body class="receiver idle">`,
		`GROOVY · RELAY`,
		`<!-- chassis:shell -->`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleIndex_IncludesEveryPartialMarker(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<!-- chassis:status-bar -->`,
		`<!-- chassis:masthead -->`,
		`<!-- chassis:vfd-source-row -->`,
		`<!-- chassis:vfd -->`,
		`<!-- chassis:source-cluster -->`,
		`<!-- chassis:meter -->`,
		`<!-- chassis:transport -->`,
		`<!-- chassis:visualizer-bank -->`,
		`<!-- chassis:input-row -->`,
		`<!-- chassis:preset-bank -->`,
		`<!-- chassis:history -->`,
		`<!-- chassis:settings-drawer -->`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleIndex_RendersStableTemplateHooks(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`class="host-readout"`,
		`10.0.0.5:32500`,
		`<div class="input-panel" style="flex:1">`,
		`id="paste-clear" type="button"`,
		`id="torrent-file-input"`,
		`class="hw-btn active" type="button"`,
		`class="source-cluster" role="radiogroup" aria-label="Media source"`,
		`aria-checked="true"`,
		`aria-label="STREAMS selected"`,
		`data-source-action="aux-start"`,
		`class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-system-time>`,
		`class="preset empty" type="button"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing stable hook %q", want)
		}
	}
	if strings.Contains(body, `<label class="input-panel"`) {
		t.Errorf("input panel must not be a label containing interactive children")
	}
}

func TestIdleSnapshotRendersFiveSourceButtonsIncludingAUX(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	aux := &fakeAUXStarter{status: adapters.AUXStatus{
		Enabled:    true,
		Configured: true,
		InputID:    "aux",
	}}

	got := snapshotFromSession(cfg, nil, nil, nil, aux, fixedNow)
	var labels []string
	for _, button := range got.Source.Buttons {
		labels = append(labels, button.Label)
	}
	want := []string{"STREAMS", "PLEX", "JELLYFIN", "DLNA", "AUX"}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("source labels = %#v, want %#v", labels, want)
	}
}

func TestSourceClusterAUXButtonCarriesStartAction(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	data := idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	applyAUXSourceState(&data, &fakeAUXStarter{status: adapters.AUXStatus{
		Enabled:    true,
		Configured: true,
		InputID:    "aux",
	}})
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "source-cluster", data.Source); err != nil {
		t.Fatalf("execute source-cluster partial: %v", err)
	}
	body := buf.String()
	auxButton := regexp.MustCompile(`(?s)<button[^>]*data-source-action="aux-start"[^>]*>AUX</button>`).FindString(body)
	if auxButton == "" {
		t.Fatalf("AUX source button with start action not found; full output:\n%s", body)
	}
	if !strings.Contains(auxButton, `data-input-id="aux"`) {
		t.Fatalf("AUX source button missing input id; button:\n%s", auxButton)
	}
}

func TestSourceClusterAUXDisabledWhenUnavailable(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.AUX = &fakeAUXStarter{status: adapters.AUXStatus{
		Enabled:    false,
		Configured: true,
		InputID:    "aux",
	}}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	body := rr.Body.String()
	auxButton := regexp.MustCompile(`(?s)<button[^>]*data-source-action="aux-start"[^>]*>AUX</button>`).FindString(body)
	if auxButton == "" {
		t.Fatalf("AUX source button not found; full output:\n%s", body)
	}
	for _, want := range []string{`disabled`, `aria-disabled="true"`} {
		if !strings.Contains(auxButton, want) {
			t.Errorf("AUX unavailable button missing %q; button:\n%s", want, auxButton)
		}
	}
}

func TestHandleIndex_RendersMeterGhostSegmentsAccessibly(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	meterStart := strings.Index(body, `<!-- chassis:meter -->`)
	meterEnd := strings.Index(body, `<!-- chassis:transport -->`)
	if meterStart == -1 || meterEnd == -1 || meterEnd <= meterStart {
		t.Fatalf("rendered body missing ordered meter/transport markers")
	}
	meterHTML := body[meterStart:meterEnd]
	for _, want := range []string{
		`class="seg-ghost" aria-hidden="true">~88.8</span><span class="seg-text">---</span></span>`,
		`<span class="val seg-display" id="lufs-val">`,
		`<span class="val seg-display locked">`,
		`<canvas id="gonio-canvas" width="172" height="172" role="img" aria-labelledby="gonio-lbl">`,
		`<canvas id="gonio-canvas" width="172" height="172" role="img" aria-labelledby="gonio-lbl">Stereo phase scope</canvas>`,
		`id="gonio-lbl">PHASE &middot; X/Y</span>`,
		`<canvas id="throughput-canvas" width="220" height="120" role="img" aria-labelledby="throughput-lbl">`,
		`<canvas id="throughput-canvas" width="220" height="120" role="img" aria-labelledby="throughput-lbl">Network throughput graph</canvas>`,
		`id="throughput-lbl">NET &middot; 0.0 MB/S</span>`,
		`<canvas id="ack-canvas" width="220" height="120" role="img" aria-labelledby="ack-lbl">`,
		`<canvas id="ack-canvas" width="220" height="120" role="img" aria-labelledby="ack-lbl">ACK latency graph</canvas>`,
		`id="ack-lbl">ACK &middot; -- MS</span>`,
	} {
		if !strings.Contains(meterHTML, want) {
			t.Errorf("body missing accessible meter ghost markup %q", want)
		}
	}
	if strings.Contains(meterHTML, `data-ghost=`) {
		t.Fatal("rendered meter markup must not contain legacy data-ghost attributes")
	}
}

func TestHandleIndex_RendersTransportGhostSegmentsAccessibly(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	transportStart := strings.Index(body, `<!-- chassis:transport -->`)
	transportEnd := strings.Index(body, `<!-- chassis:visualizer-bank -->`)
	if transportStart == -1 || transportEnd == -1 || transportEnd <= transportStart {
		t.Fatalf("rendered body missing ordered transport/visualizer markers")
	}
	transportHTML := body[transportStart:transportEnd]
	for _, want := range []string{
		`<span class="seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-transport-elapsed></span></span>`,
		`<span class="total seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-transport-total></span></span>`,
	} {
		if !strings.Contains(transportHTML, want) {
			t.Errorf("body missing accessible transport ghost markup %q", want)
		}
	}
	if strings.Contains(transportHTML, `data-ghost=`) {
		t.Fatal("rendered transport markup must not contain legacy data-ghost attributes")
	}
}

func TestHandleIndex_RendersTransportAndVisualizerAccessibilityHooks(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`data-transport-action="previous"`,
		`aria-label="Previous" title="Previous"`,
		`data-transport-action="next"`,
		`aria-label="Next" title="Next"`,
		`data-transport-action="pauseResume"`,
		`aria-label="Pause or resume" title="Pause / Resume"`,
		`data-state-icon="playing"`,
		`data-state-icon="paused"`,
		`data-transport-action="stop"`,
		`aria-label="Stop" title="Stop"`,
		`data-transport-action="replay"`,
		`aria-label="Replay" title="Replay"`,
		`class="seek-bar" data-transport-seek`,
		`role="progressbar" aria-label="Cast position" aria-valuemin="0" aria-valuemax="100"`,
		`class="hw-btn viz-btn viz-btn--preview" type="button" role="radio" aria-checked="false" aria-disabled="true" disabled`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing transport/visualizer accessibility hook %q", want)
		}
	}
}

func TestTransportTemplate_RendersDataAttributeHooks(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	data := TransportData{
		State:           "playing",
		SeekFillPercent: 44,
		ElapsedTime:     "04:23",
		TotalTime:       "09:56",
		PercentPlayed:   "44%",
		OffsetMS:        263000,
		DurationMS:      596000,
		ActionsEnabled: ActionsEnabled{
			Previous:    true,
			Next:        true,
			PauseResume: true,
			Stop:        true,
			Replay:      false,
			Seek:        true,
		},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "transport", data); err != nil {
		t.Fatalf("execute transport partial: %v", err)
	}
	body := buf.String()

	for _, want := range []string{
		`data-transport-state="playing"`,
		`data-transport-action="previous"`,
		`data-transport-action="next"`,
		`data-transport-action="pauseResume"`,
		`data-transport-action="stop"`,
		`data-transport-action="replay"`,
		`data-transport-seek`,
		`data-transport-seek-fill`,
		`data-transport-elapsed`,
		`data-transport-total`,
		`data-transport-percent`,
		`data-transport-offset-ms="263000"`,
		`data-transport-duration-ms="596000"`,
		`data-state-icon="playing"`,
		`data-state-icon="paused"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transport partial missing %q; full output:\n%s", want, body)
		}
	}
	replayDisabled := regexp.MustCompile(`(?s)<button[^>]*data-transport-action="replay"[^>]*\sdisabled(?:\s|>)`)
	if !replayDisabled.MatchString(body) {
		t.Fatalf("replay button should be disabled when ActionsEnabled.Replay=false; full output:\n%s", body)
	}

	for name, pattern := range map[string]string{
		"previous":    `(?s)<button[^>]*data-transport-action="previous"[^>]*aria-label="Previous"[^>]*title="Previous"[^>]*>`,
		"next":        `(?s)<button[^>]*data-transport-action="next"[^>]*aria-label="Next"[^>]*title="Next"[^>]*>`,
		"pauseResume": `(?s)<button[^>]*data-transport-action="pauseResume"[^>]*aria-label="Pause or resume"[^>]*title="Pause / Resume"[^>]*>`,
		"stop":        `(?s)<button[^>]*data-transport-action="stop"[^>]*aria-label="Stop"[^>]*title="Stop"[^>]*>`,
		"replay":      `(?s)<button[^>]*data-transport-action="replay"[^>]*disabled[^>]*aria-label="Replay"[^>]*title="Replay"[^>]*>`,
	} {
		if !regexp.MustCompile(pattern).MatchString(body) {
			t.Errorf("transport %s button missing expected attributes on one element; full output:\n%s", name, body)
		}
	}
	pauseButton := regexp.MustCompile(`(?s)<button[^>]*data-transport-action="pauseResume"[^>]*>.*?</button>`).FindString(body)
	if pauseButton == "" {
		t.Fatalf("pauseResume button not found; full output:\n%s", body)
	}
	for _, want := range []string{`data-state-icon="playing"`, `data-state-icon="paused"`} {
		if !strings.Contains(pauseButton, want) {
			t.Errorf("pauseResume button missing %q; button output:\n%s", want, pauseButton)
		}
	}
	seekBar := regexp.MustCompile(`(?s)<div[^>]*class="seek-bar"[^>]*data-transport-seek[^>]*data-transport-offset-ms="263000"[^>]*data-transport-duration-ms="596000"[^>]*aria-valuenow="44"[^>]*aria-disabled="false"[^>]*>`)
	if !seekBar.MatchString(body) {
		t.Errorf("transport seek bar missing expected attributes on one element; full output:\n%s", body)
	}
}

func TestTransportTemplate_SeekFillStyleReflectsPercent(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	for _, percent := range []int{0, 44, 100} {
		data := TransportData{SeekFillPercent: percent}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "transport", data); err != nil {
			t.Fatalf("execute transport partial with percent %d: %v", percent, err)
		}
		want := `style="width: ` + strconv.Itoa(percent) + `%"`
		if !strings.Contains(buf.String(), want) {
			t.Errorf("seek fill style with percent %d missing %q; full output:\n%s", percent, want, buf.String())
		}
	}
}

func TestTransportTemplate_SeekDisabledReflectsActionsEnabled(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}

	for _, tt := range []struct {
		name             string
		seekEnabled      bool
		wantDisabledAttr bool
		wantAriaDisabled string
	}{
		{name: "disabled", seekEnabled: false, wantDisabledAttr: true, wantAriaDisabled: `aria-disabled="true"`},
		{name: "enabled", seekEnabled: true, wantDisabledAttr: false, wantAriaDisabled: `aria-disabled="false"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := TransportData{ActionsEnabled: ActionsEnabled{Seek: tt.seekEnabled}}
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "transport", data); err != nil {
				t.Fatalf("execute transport partial: %v", err)
			}
			body := buf.String()

			if got := strings.Contains(body, `data-transport-seek-disabled`); got != tt.wantDisabledAttr {
				t.Errorf("data-transport-seek-disabled present = %v, want %v; full output:\n%s", got, tt.wantDisabledAttr, body)
			}
			if !strings.Contains(body, tt.wantAriaDisabled) {
				t.Errorf("transport partial missing %q; full output:\n%s", tt.wantAriaDisabled, body)
			}
		})
	}
}

func TestHandleIndex_AssetURLsCarryVersionQueryParam(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`/receiver/static/chassis.css?v=test-1.0.0`,
		`/receiver/static/chassis.js?v=test-1.0.0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleIndex_TemplateErrorReturns500WithoutPartialBody(t *testing.T) {
	t.Parallel()
	tmpl := template.Must(template.New("chassis").Parse(`{{define "shell.html"}}partial body {{.MissingField}}{{end}}`))
	s := &Server{cfg: nonZeroConfig(), tmpl: tmpl}
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rr.Body.String(), "partial body") {
		t.Errorf("response leaked partial template output: %q", rr.Body.String())
	}
}

func TestHandleStatic_CSS_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/chassis.css", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css prefix", got)
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("Cache-Control = %q, want max-age=31536000", got)
	}
}

func TestHandleStatic_CSS_VersionedFontURLs(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/chassis.css", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "?v=test-1.0.0") {
		t.Errorf("CSS missing version query param: %s", body)
	}
	if strings.Contains(body, "{{.Version}}") {
		t.Errorf("CSS contains raw version placeholder: %s", body)
	}
}

func TestHandleStatic_Fonts_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	for _, font := range []string{
		"DSEG7Classic-Regular.woff2",
		"DSEG7Classic-Bold.woff2",
		"DSEG7Modern-Regular.woff2",
		"DSEG7Modern-Bold.woff2",
		"DSEG14Classic-Regular.woff2",
		"DSEG14Classic-Bold.woff2",
		"DSEG14Modern-Regular.woff2",
		"DSEG14Modern-Bold.woff2",
		"Inter-Variable.woff2",
	} {
		font := font
		t.Run(font, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/receiver/static/fonts/"+font, nil)
			rr := httptest.NewRecorder()

			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if got := rr.Header().Get("Content-Type"); got != "font/woff2" {
				t.Fatalf("Content-Type = %q, want font/woff2", got)
			}
			if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=31536000") {
				t.Fatalf("Cache-Control = %q, want max-age=31536000", got)
			}
		})
	}
}

func TestHandleStatic_LICENSE_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/fonts/LICENSE", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"DSEG Font License (MIT)",
		"Copyright (c) 2017 Keshikan",
		"Permission is hereby granted",
		"SIL OPEN FONT LICENSE Version 1.1",
		"Copyright (c) 2016 The Inter Project Authors.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("LICENSE body missing %q", want)
		}
	}
}

func TestHandleStatic_UnknownAsset404(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/nonexistent.css", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleStatic_PathTraversalBlocked(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/../config.toml", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatal("path traversal returned 200")
	}
}

func TestMount_TrailingSlashIndex(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	for _, path := range []string{"/receiver", "/receiver/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", path, rr.Code, http.StatusOK)
		}
	}
}

func TestSessionViewer_StatusHomeViewSatisfiesInterface(t *testing.T) {
	t.Parallel()
	// Compile-time and runtime assertion that *core.Manager satisfies
	// the chassis SessionViewer interface. Catches regressions where
	// core.Manager.StatusHomeView() changes signature without the
	// chassis side noticing.
	var _ SessionViewer = (*core.Manager)(nil)

	cfg := nonZeroConfig()
	cfg.Session = cfg.Manager
	if cfg.Session == nil {
		t.Fatal("expected non-nil Session after assignment from Manager")
	}
}

func TestTransportViewer_DispatcherSatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ TransportViewer = (*playback.Dispatcher)(nil)
	var _ TransportController = (*playback.Dispatcher)(nil)

	cfg := nonZeroConfig()
	d := testTransportDispatcher(cfg)
	cfg.TransportViewer = d
	cfg.TransportController = d
	if cfg.TransportViewer == nil || cfg.TransportController == nil {
		t.Fatal("transport fields should be assignable from *playback.Dispatcher")
	}
}

func testTransportDispatcher(cfg Config) *playback.Dispatcher {
	return playback.NewDispatcher(cfg.Manager, cfg.Registry)
}

func TestServer_StoresTransportViewerAndControllerFromConfig(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	d := testTransportDispatcher(cfg)
	cfg.TransportViewer = d
	cfg.TransportController = d

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.transportViewer != d {
		t.Errorf("Server.transportViewer not stored from Config")
	}
	if s.transportController != d {
		t.Errorf("Server.transportController not stored from Config")
	}
}

func TestVisualizerViewer_ManagerSatisfiesInterface(t *testing.T) {
	t.Parallel()
	// Compile-time + runtime assertion that *core.Manager satisfies
	// the chassis VisualizerViewer interface via its VisualizerMode()
	// method. Catches regressions where Manager's signature changes
	// without the chassis side noticing.
	var _ VisualizerViewer = (*core.Manager)(nil)
}

func TestSnapshotFromSession_NilSessionFallsBackToIdle(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Session = nil

	got := snapshotFromSession(cfg, nil, nil, nil, nil, fixedNow)
	want := idleSnapshot(cfg, fixedNow)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nil Session should match idleSnapshot exactly; got %+v\nwant %+v", got, want)
	}
}

func TestSnapshotFromSession_VisualizerModeOverridesIdleDefault(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	got := snapshotFromSession(cfg, nil, viewer, nil, nil, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeStereoScope {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (viewer overrides cfg.Bridge default)", got.Visualizer.ActiveMode, config.VisualizerModeStereoScope)
	}
}

func TestSnapshotFromSession_NilVisualizerViewerFallsBackToCfg(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	got := snapshotFromSession(cfg, nil, nil, nil, nil, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeOscilloscopeWave {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (nil viewer falls back to cfg.Bridge)", got.Visualizer.ActiveMode, config.VisualizerModeOscilloscopeWave)
	}
}

func TestSnapshotFromSession_NormalizesEmptyMode(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	viewer := &fakeVisualizerViewer{mode: ""}
	got := snapshotFromSession(cfg, nil, viewer, nil, nil, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeRetroAnalyzer {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (empty viewer mode should normalize to retro_analyzer)", got.Visualizer.ActiveMode, config.VisualizerModeRetroAnalyzer)
	}
}

// fakeSessionViewer is the test double for SessionViewer. Lets tests
// drive the chassis from a known StatusHomeView without spinning up a
// real *core.Manager (which requires a full bridge graph).
type fakeSessionViewer struct {
	view core.StatusHomeView
}

func (f *fakeSessionViewer) StatusHomeView() core.StatusHomeView { return f.view }

type fakeTransportViewer struct {
	view  adapters.PlaybackBannerAdapterView
	owns  bool
	calls []core.StatusHomeView
}

func (f *fakeTransportViewer) PlaybackViewForSnapshot(_ context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool) {
	f.calls = append(f.calls, snap)
	return f.view, f.owns
}

func TestSnapshotFromSession_PopulatesTransportFromAdapterView(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	status := core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "The Chauffeur",
		Source:     "jellyfin",
		AdapterRef: "jellyfin:item:123",
		Generation: 12,
		Position:   2*time.Minute + 8*time.Second,
		Duration:   3*time.Minute + 20*time.Second,
	}
	sv := &fakeSessionViewer{view: status}
	tv := &fakeTransportViewer{
		owns: true,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{
				{ID: adapters.PlaybackActionPrevious, Enabled: true},
				{ID: adapters.PlaybackActionNext, Enabled: true},
				{ID: adapters.PlaybackActionPause, Enabled: true},
				{ID: adapters.PlaybackActionStop, Enabled: true},
				{ID: adapters.PlaybackActionReplay, Enabled: true},
			},
			Seek: &adapters.PlaybackSeek{
				Enabled:    true,
				OffsetMS:   129999,
				DurationMS: 201234,
			},
		},
	}

	got := snapshotFromSession(cfg, sv, nil, tv, nil, fixedNow)

	want := TransportData{
		State:           "playing",
		SeekFillPercent: 64,
		ElapsedTime:     "02:08",
		TotalTime:       "03:20",
		PercentPlayed:   "64%",
		OffsetMS:        129999,
		DurationMS:      201234,
		ActionsEnabled: ActionsEnabled{
			Previous:    true,
			Next:        true,
			PauseResume: true,
			Stop:        true,
			Replay:      true,
			Seek:        true,
		},
		AdapterRef: "jellyfin:item:123",
		Generation: 12,
	}
	if got.Transport != want {
		t.Errorf("Transport = %+v, want %+v", got.Transport, want)
	}
	if len(tv.calls) != 1 || !reflect.DeepEqual(tv.calls[0], status) {
		t.Errorf("PlaybackViewForSnapshot calls = %+v, want one call with status snapshot", tv.calls)
	}
}

func TestSnapshotFromSession_NoProviderKeepsReadOnlyStateAndTime(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	status := core.StatusHomeView{
		State:      core.StatePaused,
		Title:      "Rio",
		Source:     "url",
		AdapterRef: "url:abc",
		Generation: 4,
		Position:   90 * time.Second,
		Duration:   5 * time.Minute,
	}
	sv := &fakeSessionViewer{view: status}
	tv := &fakeTransportViewer{
		owns: false,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{
				{ID: adapters.PlaybackActionResume, Enabled: true},
				{ID: adapters.PlaybackActionStop, Enabled: true},
			},
			Seek: &adapters.PlaybackSeek{Enabled: true, OffsetMS: 91000, DurationMS: 301000},
		},
	}

	got := snapshotFromSession(cfg, sv, nil, tv, nil, fixedNow)

	want := TransportData{
		State:           "paused",
		SeekFillPercent: 30,
		ElapsedTime:     "01:30",
		TotalTime:       "05:00",
		PercentPlayed:   "30%",
		OffsetMS:        90000,
		DurationMS:      300000,
		ActionsEnabled:  ActionsEnabled{},
		AdapterRef:      "url:abc",
		Generation:      4,
	}
	if got.Transport != want {
		t.Errorf("Transport = %+v, want read-only %+v", got.Transport, want)
	}
	if len(tv.calls) != 1 || !reflect.DeepEqual(tv.calls[0], status) {
		t.Errorf("PlaybackViewForSnapshot calls = %+v, want one call with status snapshot", tv.calls)
	}
}

func TestSnapshotFromSession_NilTransportViewerIdleKeepsTransportZero(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:      core.StateIdle,
		AdapterRef: "url:abc",
		Generation: 4,
		Position:   90 * time.Second,
		Duration:   5 * time.Minute,
	}}

	got := snapshotFromSession(cfg, sv, nil, nil, nil, fixedNow)
	want := idleSnapshot(cfg, fixedNow).Transport

	if got.Transport != want {
		t.Errorf("idle Transport = %+v, want idle zero transport %+v", got.Transport, want)
	}
}

func TestSnapshotFromSession_NilTransportViewerKeepsActiveReadOnlyStateAndTime(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Planet Earth",
		Source:     "plex",
		AdapterRef: "plex:track:def",
		Generation: 8,
		Position:   4*time.Minute + 23*time.Second,
		Duration:   0,
	}}

	got := snapshotFromSession(cfg, sv, nil, nil, nil, fixedNow)

	want := TransportData{
		State:           "playing",
		SeekFillPercent: 0,
		ElapsedTime:     "04:23",
		TotalTime:       "--:--",
		PercentPlayed:   "",
		OffsetMS:        263000,
		DurationMS:      0,
		ActionsEnabled:  ActionsEnabled{},
		AdapterRef:      "plex:track:def",
		Generation:      8,
	}
	if got.Transport != want {
		t.Errorf("Transport = %+v, want read-only %+v", got.Transport, want)
	}
}

func TestSnapshotFromSession_LiveStateOverridesIdleDefaults(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:    core.StatePlaying,
		Title:    "First Day on MTV",
		Source:   "plex",
		Position: 4*time.Minute + 23*time.Second,
		Duration: 9*time.Minute + 56*time.Second,
	}}
	got := snapshotFromSession(cfg, sv, nil, nil, nil, fixedNow)

	if got.State != StateLive {
		t.Errorf("State = %q, want %q", got.State, StateLive)
	}
	if got.VFD.Title != "First Day on MTV" {
		t.Errorf("VFD.Title = %q, want First Day on MTV", got.VFD.Title)
	}
	if got.VFD.Marquee != "PLEX · 04:23 / 09:56" {
		t.Errorf("VFD.Marquee = %q, want PLEX · 04:23 / 09:56", got.VFD.Marquee)
	}
	if got.VFD.State != string(StateLive) {
		t.Errorf("VFD.State = %q, want %q (mirrors top-level State)", got.VFD.State, StateLive)
	}
}

func TestSnapshotFromSession_LiveStateOverridesIdleDefaults_StillSetsVisualizer(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "First Day on MTV", Source: "plex", Position: 4*time.Minute + 23*time.Second, Duration: 9*time.Minute + 56*time.Second,
	}}
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	got := snapshotFromSession(cfg, sv, viewer, nil, nil, fixedNow)
	if got.State != StateLive {
		t.Errorf("State = %q, want %q", got.State, StateLive)
	}
	if got.Visualizer.ActiveMode != config.VisualizerModeStereoScope {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (live state still applies viz override)", got.Visualizer.ActiveMode, config.VisualizerModeStereoScope)
	}
}

func TestSnapshotFromSession_PausedMapsToLive(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePaused, Title: "Take On Me", Source: "plex",
	}}
	got := snapshotFromSession(cfg, sv, nil, nil, nil, fixedNow)

	// core.StatePaused -> chassis "live" so the body stays bright
	// during transport pause. The transport-row pause indicator is
	// Spec 3's job; chassis state class should not flicker.
	if got.State != StateLive {
		t.Errorf("paused -> chassis State = %q, want %q", got.State, StateLive)
	}
}

func TestSnapshotFromSession_IdleStateMatchesIdleSnapshot(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	got := snapshotFromSession(cfg, sv, nil, nil, nil, fixedNow)
	want := idleSnapshot(cfg, fixedNow)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idle-from-session should match idleSnapshot exactly")
	}
}

func TestSnapshotFromSession_UnknownStateFallsBackToIdle(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.State("buffering"), Title: "Not Yet Supported", Source: "plex",
	}}
	got := snapshotFromSession(cfg, sv, nil, nil, nil, fixedNow)
	want := idleSnapshot(cfg, fixedNow)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unknown session state should fall back to idleSnapshot; got %+v\nwant %+v", got, want)
	}
}

func TestSnapshotFromSession_MapsStatusHomeViewToVFDData(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:    core.StatePlaying,
		Title:    "TEST",
		Source:   "jellyfin",
		Position: 30 * time.Second,
		Duration: 3 * time.Minute,
	}}
	got := snapshotFromSession(cfg, sv, nil, nil, nil, fixedNow)

	if got.VFD.Marquee != "JELLYFIN · 00:30 / 03:00" {
		t.Errorf("VFD.Marquee = %q, want JELLYFIN · 00:30 / 03:00", got.VFD.Marquee)
	}
	if got.VFD.QueueCurrent != 0 || got.VFD.QueueTotal != 0 {
		t.Errorf("queue should be 0/0 placeholder in Phase 1; got %d/%d",
			got.VFD.QueueCurrent, got.VFD.QueueTotal)
	}
}

func TestHandleIndex_RendersLiveStateFromSession(t *testing.T) {
	t.Parallel()
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:    core.StatePlaying,
		Title:    "Burning Down the House",
		Source:   "plex",
		Position: 8 * time.Second,
		Duration: 4*time.Minute + 1*time.Second,
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="receiver live"`) {
		t.Errorf("body missing live body class: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "Burning Down the House") {
		t.Errorf("body missing live title")
	}
	if !strings.Contains(body, "PLEX · 00:08 / 04:01") {
		t.Errorf("body missing live marquee")
	}
}

func TestVfdTemplate_RendersDataAttributeHooks(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	var buf bytes.Buffer
	data := VFDData{
		Title:        "TEST-TITLE",
		Marquee:      "TEST-MARQUEE",
		QueueCurrent: 1,
		QueueTotal:   12,
		SystemTime:   "22:47",
		Uptime:       "4H 12M",
	}
	if err := tmpl.ExecuteTemplate(&buf, "vfd", data); err != nil {
		t.Fatalf("execute vfd partial: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"data-vfd-title",
		"data-vfd-marquee",
		"data-vfd-queue",
		"data-vfd-uptime",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("vfd partial missing %q hook; full output:\n%s", want, body)
		}
	}
	// Ghost overlays must still be present — regression guard against
	// accidentally placing data-vfd-* on the outer divs and breaking
	// the seg-text/seg-ghost overlay vocabulary.
	if !strings.Contains(body, "seg-ghost") {
		t.Errorf("vfd partial is missing seg-ghost spans; overlay vocabulary broken")
	}
}

func TestShellTemplate_LoadsVfdLiveScript(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/receiver/static/vfd-live.js?v=test-1.0.0`) {
		t.Errorf("shell.html should include versioned vfd-live.js script tag")
	}
	chassisIdx := strings.Index(body, "chassis.js?v=")
	vfdIdx := strings.Index(body, "vfd-live.js?v=")
	if chassisIdx < 0 || vfdIdx < 0 {
		t.Fatalf("missing one of the script tags")
	}
	if vfdIdx <= chassisIdx {
		t.Errorf("vfd-live.js script must appear AFTER chassis.js so the deferred load order initializes window.Chassis first")
	}
}

func TestShellTemplate_LoadsVisualizerBankScriptAfterVfdLive(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/receiver/static/visualizer-bank.js?v=test-1.0.0`) {
		t.Errorf("shell.html should include versioned visualizer-bank.js script tag")
	}
	vfdIdx := strings.Index(body, "vfd-live.js?v=")
	vizIdx := strings.Index(body, "visualizer-bank.js?v=")
	if vfdIdx < 0 || vizIdx < 0 {
		t.Fatalf("missing one of the script tags")
	}
	if vizIdx <= vfdIdx {
		t.Errorf("visualizer-bank.js script must appear AFTER vfd-live.js so it can use the shared EventSource")
	}
}

func TestShellTemplate_LoadsTransportScript(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/receiver/static/transport.js?v=test-1.0.0`) {
		t.Errorf("shell.html should include versioned transport.js script tag")
	}
	vfdIdx := strings.Index(body, "vfd-live.js?v=")
	transportIdx := strings.Index(body, "transport.js?v=")
	if vfdIdx < 0 || transportIdx < 0 {
		t.Fatalf("missing one of the script tags")
	}
	if transportIdx <= vfdIdx {
		t.Errorf("transport.js script must appear AFTER vfd-live.js so it can use live transport data hooks")
	}
}

func TestShellTemplate_EmitsChassisMetaTags(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Session = &fakeSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "plex:abc",
		Generation: 42,
	}}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	for _, want := range []string{
		`<meta name="chassis-adapter-ref" content="plex:abc"`,
		`<meta name="chassis-generation" content="42"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell.html missing %q", want)
		}
	}
}

func TestFormatLiveMarquee_HandlesUnknownDurationAndHours(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		view core.StatusHomeView
		want string
	}{
		{
			name: "unknown duration",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "plex",
				Position: 4*time.Minute + 23*time.Second},
			want: "PLEX · 04:23 / --:--",
		},
		{
			name: "zero position unknown duration",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "plex"},
			want: "PLEX · 00:00 / --:--",
		},
		{
			name: "empty source fallback",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "",
				Position: 30 * time.Second, Duration: 3 * time.Minute},
			want: "BRIDGE · 00:30 / 03:00",
		},
		{
			name: "hour-long position single-digit hours",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "plex",
				Position: time.Hour + 4*time.Minute + 5*time.Second,
				Duration: time.Hour + 30*time.Minute},
			want: "PLEX · 1:04:05 / 1:30:00",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLiveMarquee(tc.view)
			if got != tc.want {
				t.Errorf("formatLiveMarquee = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleStatic_VfdLiveJSServed(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodGet, "/receiver/static/vfd-live.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET vfd-live.js status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") && !strings.HasPrefix(got, "application/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript or application/javascript", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "window.Chassis.events") {
		t.Errorf("served vfd-live.js doesn't contain window.Chassis.events namespace export")
	}
}

func TestVfdLive_ExposesEventSourceReference_StaticAssetCheck(t *testing.T) {
	t.Parallel()
	bytes, err := chassisStaticFS.ReadFile("static/vfd-live.js")
	if err != nil {
		t.Fatalf("read vfd-live.js: %v", err)
	}
	js := string(bytes)
	for _, want := range []string{
		"window.Chassis.events.source = source",
		"chassis:eventsource",
		"detail: { source }",
		"new EventSource",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("vfd-live.js missing %q", want)
		}
	}
}

func TestVFDLiveJSHandlesSourceEvents(t *testing.T) {
	t.Parallel()
	bytes, err := chassisStaticFS.ReadFile("static/vfd-live.js")
	if err != nil {
		t.Fatalf("read vfd-live.js: %v", err)
	}
	js := string(bytes)
	for _, want := range []string{
		"source.addEventListener('source'",
		"handleSourceEvent",
		"`[data-source-action=\"${button.action}\"]`",
		"classList.toggle('active'",
		"classList.toggle('lit'",
		"aria-checked",
		"aria-disabled",
		"data-input-id",
		"disabled",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("vfd-live.js missing source event contract %q", want)
		}
	}
}
