package chassis

import (
	"html/template"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

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
			PlayState:       "stopped",
			ElapsedTime:     "--:--",
			TotalTime:       "--:--",
			PercentPlayed:   "---",
			SeekFillPercent: 0,
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

func TestHandleStatic_JS_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
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
		`class="hw-btn active" type="button" role="radio" aria-checked="true" aria-label="STREAMS selected"`,
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
		`<span class="seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text">--:--</span></span>`,
		`<span class="total seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text">--:--</span></span>`,
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
		`<button class="trn" type="button" aria-label="Previous" title="Previous">`,
		`<button class="trn" type="button" aria-label="Next" title="Next">`,
		`<button class="trn primary" type="button" aria-label="Pause or resume" title="Pause / Resume">`,
		`<button class="trn" type="button" aria-label="Stop" title="Stop">`,
		`<button class="trn" type="button" aria-label="Replay" title="Replay">`,
		`class="seek-bar" role="progressbar" aria-label="Cast position" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"`,
		`class="hw-btn viz-btn viz-btn--preview" type="button" role="radio" aria-checked="false" aria-disabled="true" disabled`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing transport/visualizer accessibility hook %q", want)
		}
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

	for _, path := range []string{"/receiver", "/receiver/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", path, rr.Code, http.StatusOK)
		}
	}
}
