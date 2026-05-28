package chassis

import (
	"bytes"
	"context"
	"fmt"
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

type fakeVolumeViewer struct {
	volume int
}

func (f *fakeVolumeViewer) OutputVolume() int {
	return f.volume
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
				{Label: "STREAMS"},
				{Label: "PLEX"},
				{Label: "JELLYFIN"},
				{Label: "DLNA"},
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
				BitrateMbps:   "---",
				FreqKHz:       "---",
				Mode:          "---",
				StandardNTSC:  false,
				StandardPAL:   false,
				FieldFlip:     "idle",
				FieldLock:     "idle",
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
			AudioScopes: AudioScopesData{Status: "pending"},
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
			OutputVolume:    0,
		},
		Visualizer: VisualizerData{
			ActiveMode: config.VisualizerModeStereoScope,
			Buttons: []VisualizerButton{
				{Mode: config.VisualizerModeRetroAnalyzer, Label: "ANALYZER", IconKind: "analyzer", IsPreview: false},
				{Mode: config.VisualizerModeOscilloscopeWave, Label: "OSCILLOSCOPE", IconKind: "wave", IsPreview: false},
				{Mode: config.VisualizerModeStereoScope, Label: "STEREO SCOPE", IconKind: "scope", IsPreview: false},
				{Mode: config.VisualizerModeVUCabinet, Label: "VU CABINET", IconKind: "scope", IsPreview: false},
				{Mode: config.VisualizerModeNeonGrid, Label: "NEON GRID", IconKind: "analyzer", IsPreview: false},
				{Mode: config.VisualizerModeRasterPulse, Label: "RASTER PULSE", IconKind: "wave", IsPreview: false},
				{Mode: config.VisualizerModeCoverVU, Label: "COVER VU", IconKind: "scope", IsPreview: false},
				{Mode: config.VisualizerModeCoverSpectrum, Label: "COVER SPECTRUM", IconKind: "analyzer", IsPreview: false},
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
			Slots: func() [12]PresetSlot {
				var s [12]PresetSlot
				for i := range s {
					s[i].Slot = i + 1
				}
				return s
			}(),
		},
		History: HistoryData{
			Rows:         nil,
			EmptyMessage: "No recent casts",
		},
		Settings: SettingsData{
			Open: false,
			Bridge: config.BridgeConfig{
				UI:         config.UIConfig{HTTPPort: 32500},
				Visualizer: config.VisualizerConfig{Mode: config.VisualizerModeStereoScope},
			},
			Errors: map[string]string{},
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
		OutputVolume:    0,
	}
	if !reflect.DeepEqual(got.Transport, want) {
		t.Errorf("idleSnapshot Transport mismatch:\n got: %+v\nwant: %+v", got.Transport, want)
	}
}

func TestIdleSnapshot_TransportOutputVolumeFromConfig(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 73

	got := idleSnapshot(cfg, fixedNow)

	if got.Transport.OutputVolume != 73 {
		t.Fatalf("Transport.OutputVolume = %d, want 73", got.Transport.OutputVolume)
	}
}

func TestSnapshotFromSession_OutputVolumeViewerOverridesConfigWhenIdle(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 12
	viewer := &fakeVolumeViewer{volume: 88}

	got := snapshotFromSession(cfg, nil, nil, viewer, nil, nil, fixedNow)

	if got.Transport.OutputVolume != 88 {
		t.Fatalf("Transport.OutputVolume = %d, want 88", got.Transport.OutputVolume)
	}
}

func TestSnapshotFromSession_OutputVolumeViewerSurvivesLiveTransportOverwrite(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 12
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Rio",
		Source:     "plex",
		AdapterRef: "plex:track:1",
		Generation: 9,
		Position:   30 * time.Second,
		Duration:   90 * time.Second,
	}}
	viewer := &fakeVolumeViewer{volume: 66}

	got := snapshotFromSession(cfg, sv, nil, viewer, nil, nil, fixedNow)

	if got.Transport.OutputVolume != 66 {
		t.Fatalf("Transport.OutputVolume = %d, want 66", got.Transport.OutputVolume)
	}
}

func TestVolumeAngle_MapsOutputVolumeToArc(t *testing.T) {
	t.Parallel()
	tests := []struct {
		volume int
		want   int
	}{
		{volume: -10, want: -135},
		{volume: 0, want: -135},
		{volume: 1, want: -132},
		{volume: 50, want: 0},
		{volume: 99, want: 132},
		{volume: 100, want: 135},
		{volume: 150, want: 135},
	}
	for _, tc := range tests {
		if got := volumeAngle(tc.volume); got != tc.want {
			t.Errorf("volumeAngle(%d) = %d, want %d", tc.volume, got, tc.want)
		}
	}
}

func TestHandleIndex_RendersVolumeKnobHooks(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 73
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)

	s.handleIndex(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		`data-volume-knob`,
		`data-volume-value="73"`,
		`data-volume-range`,
		`aria-label="Volume"`,
		`min="0"`,
		`max="100"`,
		`value="73"`,
		`--volume-angle: 62deg`,
		`/receiver/static/volume-knob.js?v=test-1.0.0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("receiver HTML missing %q\n%s", want, body)
		}
	}
	transportIdx := strings.Index(body, "transport.js?v=")
	volumeIdx := strings.Index(body, "volume-knob.js?v=")
	if transportIdx < 0 || volumeIdx < 0 {
		t.Fatalf("missing transport.js or volume-knob.js script tag")
	}
	if volumeIdx < transportIdx {
		t.Errorf("volume-knob.js must load after transport.js so shared transport/event hooks are initialized")
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

func TestIdleSnapshot_NewSupportedVisualizerModeIsActive(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = config.VisualizerModeVUCabinet
	got := idleSnapshot(cfg, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeVUCabinet {
		t.Errorf("Visualizer.ActiveMode = %q, want %q", got.Visualizer.ActiveMode, config.VisualizerModeVUCabinet)
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
		`"label controls controls controls"`,
		`"label seek volume gear"`,
		`grid-area: label;`,
		`grid-area: controls;`,
		`grid-area: seek;`,
		`grid-area: volume;`,
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
		"window.Chassis.events.subscribe('visualizer'",
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
		"window.Chassis.events.subscribe('transport'",
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
	if strings.Contains(body, `<!-- chassis:masthead -->`) {
		t.Errorf("body should not render separate masthead chrome")
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
		`class="power-cluster"`,
		`class="power-btn"`,
		`<span class="lbl">PWR</span>`,
		`<span class="lbl">LINK</span>`,
		`<span class="lbl">ON&nbsp;AIR</span>`,
		`<span class="lbl">CAST</span>`,
		`<span class="lbl">REC</span>`,
		`<span class="lbl">EQ</span>`,
		`class="load-core-btn loaded"`,
		`<span class="label-text">&#9656; Load core</span>`,
		`model GR-<em>480i</em> &middot; stereo bridge`,
		`<div class="input-panel" style="flex:1">`,
		`id="paste-clear" type="button"`,
		`id="torrent-file-input"`,
		`class="source-cluster" role="group" aria-label="Media source"`,
		`data-source-action="aux-start"`,
		`class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-system-time>`,
		`class="preset empty"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing stable hook %q", want)
		}
	}
	if strings.Contains(body, `<label class="input-panel"`) {
		t.Errorf("input panel must not be a label containing interactive children")
	}
	for _, unwanted := range []string{
		`class="host-readout"`,
		`10.0.0.5:32500`,
		`RECEIVER 2026`,
		`<!-- chassis:masthead -->`,
		`class="masthead"`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body should not contain old top chrome hook %q", unwanted)
		}
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

	got := snapshotFromSession(cfg, nil, nil, nil, nil, aux, fixedNow)
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

	got := snapshotFromSession(cfg, nil, nil, nil, nil, nil, fixedNow)
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
	got := snapshotFromSession(cfg, nil, viewer, nil, nil, nil, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeStereoScope {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (viewer overrides cfg.Bridge default)", got.Visualizer.ActiveMode, config.VisualizerModeStereoScope)
	}
}

func TestSnapshotFromSession_NilVisualizerViewerFallsBackToCfg(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	got := snapshotFromSession(cfg, nil, nil, nil, nil, nil, fixedNow)
	if got.Visualizer.ActiveMode != config.VisualizerModeOscilloscopeWave {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (nil viewer falls back to cfg.Bridge)", got.Visualizer.ActiveMode, config.VisualizerModeOscilloscopeWave)
	}
}

func TestSnapshotFromSession_NormalizesEmptyMode(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	viewer := &fakeVisualizerViewer{mode: ""}
	got := snapshotFromSession(cfg, nil, viewer, nil, nil, nil, fixedNow)
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

	got := snapshotFromSession(cfg, sv, nil, nil, tv, nil, fixedNow)

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
		AdapterRef:   "jellyfin:item:123",
		Generation:   12,
		OutputVolume: 0,
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

	got := snapshotFromSession(cfg, sv, nil, nil, tv, nil, fixedNow)

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
		OutputVolume:    0,
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

	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)
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

	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)

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
		OutputVolume:    0,
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
	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)

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
	got := snapshotFromSession(cfg, sv, viewer, nil, nil, nil, fixedNow)
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
	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)

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
	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)
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
	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)
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
	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)

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

func TestMeterTemplateHasDataHooks(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "meter", data.Meter); err != nil {
		t.Fatalf("execute meter: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"data-meter-audio-in",
		"data-meter-audio-out",
		"data-meter-src",
		"data-meter-crop",
		"data-meter-hls-buffer",
		"data-meter-drops",
		"data-meter-bitrate",
		"data-meter-freq-khz",
		"data-meter-mode",
		"data-meter-standard-ntsc",
		"data-meter-standard-pal",
		"data-meter-field-lock",
		"data-meter-throughput",
		"data-meter-ack",
		"data-meter-output",
		"data-meter-aspect",
		"data-meter-pipe",
		"data-meter-speed",
		"data-meter-link",
		"data-meter-audio-scopes-status",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter template missing %q; body:\n%s", want, body)
		}
	}
}

func TestMeterStaticUsesSubscribeAndNoDemoGenerators(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/meter.js")
	if err != nil {
		t.Fatalf("ReadFile(static/meter.js): %v", err)
	}
	body := string(js)
	for _, want := range []string{
		"window.Chassis.events.subscribe('meter'",
		"data-meter-audio-in",
		"data-meter-hls-seg",
		"throughput-canvas",
		"ack-canvas",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter.js missing %q", want)
		}
	}
	for _, bad := range []string{"new EventSource", "Math.random", "setInterval(", "demo"} {
		if strings.Contains(body, bad) {
			t.Fatalf("meter.js contains forbidden generator %q", bad)
		}
	}
}

func TestVFDLiveSubscribeHelperContract(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/vfd-live.js")
	if err != nil {
		t.Fatalf("ReadFile(static/vfd-live.js): %v", err)
	}
	body := string(js)
	for _, want := range []string{
		"subscribe(eventName, handler)",
		"subscriptions",
		"removeEventListener(eventName, handler)",
		"addEventListener(eventName, handler)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("vfd-live.js missing subscribe contract %q", want)
		}
	}
}

// TestMeterHTML_HasAudioScopeHooks verifies the existing 5A hooks are
// still present (regression guard — Task 12 must not accidentally
// remove or rename them via template churn).
func TestMeterHTML_HasAudioScopeHooks(t *testing.T) {
	cfg := nonZeroConfig()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()
	for _, hook := range []string{
		`id="phase-needle"`,
		`id="lufs-val"`,
		`id="spectrum"`,
		`id="spectrum-canvas"`,
		`id="gonio-canvas"`,
		`class="ch-bar"`,
		`data-meter-audio-scopes-status`,
	} {
		if !strings.Contains(body, hook) {
			t.Errorf("meter HTML missing existing 5A hook %q", hook)
		}
	}
}

// TestMeterJS_NoFakeValueGenerators ensures audio scope rendering
// drives values from the wire, never synthesizes them.
func TestMeterJS_NoFakeValueGenerators(t *testing.T) {
	src, err := chassisStaticFS.ReadFile("static/meter.js")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, forbidden := range []string{
		"Math.random", "Math.sin(", "Math.cos(", "Math.tan(",
		"Date.now(",
	} {
		if strings.Contains(s, forbidden) {
			t.Errorf("meter.js contains forbidden generator %q (audio scopes must drive real values, not fake animations)", forbidden)
		}
	}
}

// TestMeterJS_SubscribesToAudio ensures Task 12 wired the audio
// subscription that the rest of this task depends on.
func TestMeterJS_SubscribesToAudio(t *testing.T) {
	src, err := chassisStaticFS.ReadFile("static/meter.js")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(src), `subscribe('audio'`) && !strings.Contains(string(src), `subscribe("audio"`) {
		t.Error("meter.js does not subscribe to the audio SSE event")
	}
}

func TestChassisJS_NoRawEventSourceConsumers(t *testing.T) {
	files := []string{
		"static/transport.js",
		"static/visualizer-bank.js",
		"static/volume-knob.js",
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			src, err := chassisStaticFS.ReadFile(f)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			s := string(src)
			for _, forbidden := range []string{
				"events.source",
				"chassis:eventsource",
			} {
				if strings.Contains(s, forbidden) {
					t.Errorf("%s still contains raw EventSource consumer pattern %q; use window.Chassis.events.subscribe()", f, forbidden)
				}
			}
		})
	}
}

func TestIdleSnapshot_PresetsAreSlotNumberedEvenWhenEmpty(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.PresetViewer = nil
	data := idleSnapshot(cfg, time.Now())
	for i, slot := range data.Presets.Slots {
		if slot.Slot != i+1 {
			t.Errorf("Slots[%d].Slot = %d, want %d", i, slot.Slot, i+1)
		}
		if slot.Filled {
			t.Errorf("Slots[%d].Filled = true with nil PresetViewer, want false", i)
		}
	}
	if data.Presets.ModeLabel != "Memory · 0 / 12 slots" {
		t.Errorf("ModeLabel = %q, want %q", data.Presets.ModeLabel, "Memory · 0 / 12 slots")
	}
}

func TestIdleSnapshot_PresetsHydratedWhenViewerWired(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.PresetViewer = fakePresetViewer{
		entries: [12]adapters.PresetEntry{
			{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day on MTV", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
			// remaining entries left zero-valued to assert hydration handles them
		},
	}
	data := idleSnapshot(cfg, time.Now())
	if !data.Presets.Slots[0].Filled || data.Presets.Slots[0].Title != "First Day on MTV" {
		t.Errorf("slot 1 not hydrated: %+v", data.Presets.Slots[0])
	}
	if data.Presets.Slots[0].Subtitle != "MTV REWIND" {
		t.Errorf("slot 1 Subtitle = %q, want %q", data.Presets.Slots[0].Subtitle, "MTV REWIND")
	}
	if data.Presets.Slots[0].BadgeClass != "mtv" {
		t.Errorf("slot 1 BadgeClass = %q, want %q", data.Presets.Slots[0].BadgeClass, "mtv")
	}
	if data.Presets.Slots[1].Filled {
		t.Errorf("slot 2 should be empty (zero-valued PresetEntry), got Filled=true")
	}
	if data.Presets.Slots[1].Slot != 2 {
		t.Errorf("slot 2 Slot = %d, want 2 (numbered even when empty)", data.Presets.Slots[1].Slot)
	}
	if data.Presets.ModeLabel != "Memory · drag to reorder · 1 / 12" {
		t.Errorf("ModeLabel = %q, want %q", data.Presets.ModeLabel, "Memory · drag to reorder · 1 / 12")
	}
	if data.Presets.Count != "★ 1" {
		t.Errorf("Count = %q, want %q", data.Presets.Count, "★ 1")
	}
}

func TestSnapshotFromStatusView_LitDerivesFromAdapterRef(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.PresetViewer = bundledFakeViewer()
	view := fakeStatusView(t, "streams:mtv-rewind:90s:sess-1:42")
	data := snapshotFromStatusView(cfg, view, nil, nil, nil, nil, time.Now())
	if !data.Presets.Slots[2].Lit {
		t.Errorf("slot 3 not LIT: %+v", data.Presets.Slots[2])
	}
	if data.Presets.Slots[0].Lit {
		t.Errorf("slot 1 LIT, want false (not the active stream)")
	}
}

func TestSnapshotFromStatusView_NonStreamsAdapterRefClearsAllLit(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.PresetViewer = bundledFakeViewer()
	view := fakeStatusView(t, "url:https://example.test/x.mp4")
	data := snapshotFromStatusView(cfg, view, nil, nil, nil, nil, time.Now())
	for i, slot := range data.Presets.Slots {
		if slot.Lit {
			t.Errorf("Slots[%d].Lit = true, want false (non-streams adapter)", i)
		}
	}
}

type fakePresetViewer struct {
	entries [12]adapters.PresetEntry
}

func (f fakePresetViewer) Presets() [12]adapters.PresetEntry { return f.entries }

func bundledFakeViewer() fakePresetViewer {
	return fakePresetViewer{
		entries: [12]adapters.PresetEntry{
			{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day on MTV", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
			{Slot: 2, ProviderID: "mtv-rewind", ChannelID: "80s", Title: "MTV 80s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
			{Slot: 3, ProviderID: "mtv-rewind", ChannelID: "90s", Title: "MTV 90s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
		},
	}
}

func fakeStatusView(t *testing.T, adapterRef string) core.StatusHomeView {
	t.Helper()
	return core.StatusHomeView{State: core.StatePlaying, AdapterRef: adapterRef, Source: "streams"}
}

func parseTemplatesForTest(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	return tmpl
}

func TestPresetBankTemplate_RendersDataAttributes(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := PresetsData{Slots: [12]PresetSlot{
		{Slot: 1, Filled: true, Title: "First Day on MTV", Subtitle: "MTV REWIND", BadgeClass: "mtv", ProviderID: "mtv-rewind", ChannelID: "1stday"},
		{Slot: 11, Filled: true, Title: "Toonami East", Subtitle: "TOONAMI", BadgeClass: "toonami", ProviderID: "toonami-aftermath", ChannelID: "east", Live: true},
	}, ModeLabel: "Memory · 12 / 12 slots", Count: "★ 12"}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "preset-bank", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-slot="1"`,
		`data-provider="mtv-rewind"`,
		`data-channel="1stday"`,
		`data-slot="11"`,
		`data-provider="toonami-aftermath"`,
		`data-channel="east"`,
		`<div class="badge toonami">TOONAMI · LIVE</div>`,
		`<div class="badge mtv">MTV REWIND</div>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML missing %q.\nHTML:\n%s", want, html)
		}
	}
	if !strings.Contains(html, `live"`) {
		t.Errorf("preset.live class not rendered on Live slot.\nHTML:\n%s", html)
	}
	if strings.Contains(html, `<div class="badge mtv">MTV REWIND · LIVE`) {
		t.Errorf("non-live slot should not get LIVE suffix")
	}
}

func TestPresetBankTemplate_DataSlotPopulatedEvenForEmptySlots(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := PresetsData{}
	for i := 0; i < 12; i++ {
		data.Slots[i] = PresetSlot{Slot: i + 1}
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "preset-bank", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	for i := 1; i <= 12; i++ {
		want := fmt.Sprintf(`data-slot="%d"`, i)
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q for empty slot", want)
		}
	}
}

func TestInputRowTemplate_RendersChipKindAttribute(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := InputData{PastePlaceholder: "Paste URL or magnet", DetectedKind: "URL", CastEnabled: false}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "input-row", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `data-chip-kind=`) {
		t.Errorf("missing data-chip-kind attribute.\nHTML:\n%s", html)
	}
}

func TestInputCastJS_Exists(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/input-cast.js")
	if err != nil {
		t.Fatalf("ReadFile input-cast.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "fetch") {
		t.Errorf("input-cast.js missing fetch call")
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("input-cast.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}

func TestPresetBankJS_Exists(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/preset-bank.js")
	if err != nil {
		t.Fatalf("ReadFile preset-bank.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "window.Chassis.events.subscribe") {
		t.Errorf("preset-bank.js does not subscribe to events (missing window.Chassis.events.subscribe)")
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("preset-bank.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}

func TestShellTemplate_LoadsNewScripts(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`/receiver/static/input-cast.js?v=`,
		`/receiver/static/preset-bank.js?v=`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("shell.html missing %q script tag", want)
		}
	}
}

func TestChassisCSS_AddsCastRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		`body.receiver .chip[data-chip-kind="err"]`,
		`body.receiver .preset .badge.mtv`,
		`body.receiver .preset .badge.cartoon`,
		`body.receiver .preset .badge.toonami`,
		`body.receiver .browse-btn[disabled]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing selector %q", want)
		}
	}
}

func TestConfig_AcceptsNew3BInterfaces(t *testing.T) {
	t.Parallel()
	// Compile-time conformance: this test fails to build if any of the
	// new fields are missing from chassis.Config.
	var _ = Config{
		Bridge:                    config.BridgeConfig{},
		Version:                   "x",
		StartedAt:                 time.Now(),
		StreamsCatalogViewer:      nil,
		StreamsCaster:             nil,
		PresetEditor:              nil,
		SourceAvailabilityViewers: nil,
	}
}

func TestSourceClusterTemplate_LampSlotsForEmptyAction(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`<div class="lamp`,
		`data-source-id="streams"`,
		`data-source-id="plex"`,
		`data-source-id="jellyfin"`,
		`data-source-id="dlna"`,
		`class="hw-btn`, // AUX still renders as hw-btn
		`data-source-action="aux-start"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("source-cluster html missing %q", want)
		}
	}
}

func TestSourceClusterTemplate_LampClassReflectsState(t *testing.T) {
	t.Parallel()
	// Build a snapshot with STREAMS=Configured+Casting, PLEX=Configured, others unavailable.
	srv := newTestServerWithLampState(t,
		map[string]bool{"streams": true, "plex": true},
		"streams:mtv-rewind:80s:abc:def",
	)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	// STREAMS lamp must carry configured-idle AND casting classes.
	if !strings.Contains(html, `class="lamp configured-idle casting"`) {
		t.Errorf("STREAMS lamp missing configured-idle+casting classes: %s", excerpt(html, "STREAMS"))
	}
	// PLEX lamp configured-idle only.
	if !strings.Contains(html, `class="lamp configured-idle"`) {
		t.Errorf("PLEX lamp missing configured-idle class: %s", excerpt(html, "PLEX"))
	}
}

// newTestServerWithLampState constructs a chassis server with the
// supplied SourceAvailabilityViewers and a synthetic SessionViewer that
// returns the given adapterRef. Reuses the existing fakeSessionViewer
// helper type (which takes a full core.StatusHomeView, not just a ref).
func newTestServerWithLampState(t *testing.T, configured map[string]bool, adapterRef string) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	var viewers []adapters.SourceAvailabilityViewer
	for _, id := range []string{"streams", "plex", "jellyfin", "dlna"} {
		viewers = append(viewers, fakeSourceViewer{id: id, configured: boolToYesNo(configured[id])})
	}
	cfg.SourceAvailabilityViewers = viewers
	cfg.Session = &fakeSessionViewer{view: core.StatusHomeView{State: core.StatePlaying, AdapterRef: adapterRef}}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func boolToYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// excerpt extracts a short region of html around a keyword for error logs.
func excerpt(html, kw string) string {
	i := strings.Index(html, kw)
	if i < 0 {
		return "<keyword not found>"
	}
	start := i - 80
	if start < 0 {
		start = 0
	}
	end := i + 120
	if end > len(html) {
		end = len(html)
	}
	return html[start:end]
}

func TestCatalogDrawerTemplate_RendersProviderTabs(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`<div class="catalog-drawer"`,
		`<div class="catalog-provider-tabs">`,
		`data-provider="mtv-rewind"`,
		`data-provider="cartoon-rewind"`,
		`data-provider="toonami-aftermath"`,
		`<span class="ic mtv">MTV</span>`,
		`<span class="ic cartoon">CART</span>`,
		`<span class="ic toonami">TOON</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("catalog-drawer html missing %q", want)
		}
	}
}

func TestCatalogRailTemplate_RendersGroupsForActiveProvider(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	// MTV's first group renders with --i:0; second with --i:1, etc.
	if !strings.Contains(html, `class="catalog-rail-group active" data-group="shows" style="--i:0"`) {
		t.Errorf("catalog-rail missing active MTV shows group: %s", excerpt(html, "catalog-rail"))
	}
}

func TestCatalogGridTemplate_RendersChannelCards(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`data-provider="mtv-rewind" data-channel="1stday"`,
		`<button class="star"`,
		`<div class="name">First Day on MTV</div>`,
		`<span class="mode">SEQ</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("catalog-grid html missing %q", want)
		}
	}
}

func TestCatalogDrawerTemplate_RendersWithNilCatalogViewer(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.StreamsCatalogViewer = nil
	cfg.PresetViewer = nil
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()
	if !strings.Contains(html, `class="catalog-empty"`) {
		t.Errorf("nil catalog render missing empty state: %s", excerpt(html, "catalog-drawer"))
	}
}

func newTestServerWithCatalog(t *testing.T) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.StreamsCatalogViewer = fakeCatalogViewer{providers: []adapters.CatalogProvider{
		{ID: "mtv-rewind", DisplayName: "MTV Rewind", BadgeLabel: "MTV", BadgeClass: "mtv", Live: false,
			Groups: []adapters.CatalogGroup{
				{ID: "shows", Name: "MTV Shows", Channels: []adapters.CatalogChannel{
					{ID: "1stday", Name: "First Day on MTV", PlayMode: "SEQ"},
				}},
			}},
		{ID: "cartoon-rewind", DisplayName: "Cartoon Rewind", BadgeLabel: "CART", BadgeClass: "cartoon", Live: false,
			Groups: []adapters.CatalogGroup{
				{ID: "g1", Name: "Group 1", Channels: []adapters.CatalogChannel{
					{ID: "c1", Name: "Cartoon 1", PlayMode: "SHUFFLE"},
				}},
			}},
		{ID: "toonami-aftermath", DisplayName: "Toonami Aftermath", BadgeLabel: "TOON", BadgeClass: "toonami", Live: true,
			Groups: []adapters.CatalogGroup{
				{ID: "g1", Name: "Group 1", Channels: []adapters.CatalogChannel{
					{ID: "east", Name: "Toonami East", PlayMode: "", Live: true},
				}},
			}},
	}}
	cfg.PresetViewer = bundledFakeViewer()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestPresetBankTemplate_BrowseAndSearchEnabled(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	if strings.Contains(html, `<button class="browse-btn" id="browse-toggle" disabled`) {
		t.Errorf("browse button should NOT be disabled after 3B")
	}
	if !strings.Contains(html, `id="browse-toggle"`) {
		t.Errorf("browse button missing id browse-toggle")
	}
	// Search input must not have disabled/readonly.
	if !strings.Contains(html, `id="search-input"`) {
		t.Errorf("search input missing")
	}
	for _, banned := range []string{
		`id="search-input" disabled`,
		`id="search-input" readonly`,
	} {
		if strings.Contains(html, banned) {
			t.Errorf("search input should not be %q after 3B", banned)
		}
	}
	// BROWSE label closed form: "▸ Browse full catalog (N)"
	if !strings.Contains(html, "Browse full catalog (3)") {
		t.Errorf("browse button missing exact catalog count: %s", excerpt(html, "browse-toggle"))
	}
}

func TestPresetBankTemplate_EmitsCatalogTreeTemplatesPerProvider(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`<template id="catalog-tree-mtv-rewind">`,
		`<template id="catalog-tree-cartoon-rewind">`,
		`<template id="catalog-tree-toonami-aftermath">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("preset-bank missing %q", want)
		}
	}
}

func TestShellTemplate_LoadsNew3BScripts(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`/receiver/static/source-cluster.js?v=`,
		`/receiver/static/catalog-browser.js?v=`,
		`/receiver/static/preset-reorder.js?v=`,
		`/receiver/static/search-filter.js?v=`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("shell.html missing %q script tag", want)
		}
	}
	// Required order: chassis.js → vfd-live.js → source-cluster.js →
	// preset-bank.js → catalog-browser.js → preset-reorder.js → search-filter.js.
	order := []string{
		"chassis.js",
		"vfd-live.js",
		"source-cluster.js",
		"preset-bank.js",
		"catalog-browser.js",
		"preset-reorder.js",
		"search-filter.js",
	}
	lastIdx := -1
	for _, name := range order {
		idx := strings.Index(html, name)
		if idx < 0 {
			t.Errorf("script %s missing from shell.html", name)
			continue
		}
		if idx < lastIdx {
			t.Errorf("script %s appears before its predecessor in shell.html", name)
		}
		lastIdx = idx
	}
}

func TestSourceClusterJS_ExistsAndSubscribesToTransport(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/source-cluster.js")
	if err != nil {
		t.Fatalf("ReadFile source-cluster.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "window.Chassis.events.subscribe") {
		t.Errorf("source-cluster.js does not subscribe to events")
	}
	if !strings.Contains(s, "transport") {
		t.Errorf("source-cluster.js does not subscribe to 'transport' event")
	}
	if !strings.Contains(s, `subscribe('source'`) {
		t.Errorf("source-cluster.js does not subscribe to 'source' event")
	}
	for _, want := range []string{"configured-idle", "unavailable", "aria-label"} {
		if !strings.Contains(s, want) {
			t.Errorf("source-cluster.js missing lamp state update token %q", want)
		}
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("source-cluster.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}

func TestPresetBankJS_SubscribesToPresetsEvent(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/preset-bank.js")
	if err != nil {
		t.Fatalf("ReadFile preset-bank.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, `subscribe('presets'`) {
		t.Errorf("preset-bank.js does not subscribe to 'presets' event")
	}
	// Existing transport subscription must remain.
	if !strings.Contains(s, `subscribe('transport'`) {
		t.Errorf("preset-bank.js dropped transport subscription")
	}
	for _, want := range []string{"preset-count", "preset-mode-label", "dataset.closedText"} {
		if !strings.Contains(s, want) {
			t.Errorf("preset-bank.js missing header refresh token %q", want)
		}
	}
}

func TestCatalogBrowserJS_ExistsAndIntegrationPoints(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/catalog-browser.js")
	if err != nil {
		t.Fatalf("ReadFile catalog-browser.js: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"browse-open",
		"catalog-scanning",
		"catalog-tab-indicator",
		"/receiver/streams/cast",
		"/receiver/preset/star",
		`subscribe('transport'`,
		`subscribe('presets'`,
		"chassis:catalog-grid-changed",
		"star.textContent",
		"stopPropagation",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("catalog-browser.js missing %q", want)
		}
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("catalog-browser.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}

func TestPresetReorderJS_PointerEventsAndMoveRoute(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/preset-reorder.js")
	if err != nil {
		t.Fatalf("ReadFile preset-reorder.js: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"pointerdown",
		"pointermove",
		"pointerup",
		"/receiver/preset/move",
		"Ctrl",
		"ArrowLeft",
		"ArrowRight",
		"Escape",
		"swapVisual",
		"revert",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("preset-reorder.js missing %q", want)
		}
	}
	for _, forbidden := range []string{"draggable=\"true\"", "dragstart"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("preset-reorder.js uses HTML5 native DnD %q (must be pointer-based)", forbidden)
		}
	}
}

func TestSearchFilterJS_TogglesFilterMissClass(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/search-filter.js")
	if err != nil {
		t.Fatalf("ReadFile search-filter.js: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"#search-input",
		"filter-miss",
		"Escape",
		`subscribe('presets'`,
		"chassis:catalog-grid-changed",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("search-filter.js missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`style.opacity`,
		`style.pointerEvents`,
	} {
		if strings.Contains(s, forbidden) {
			t.Errorf("search-filter.js uses inline-style %q (must toggle classes only)", forbidden)
		}
	}
}

func TestChassisCSS_AddsLampRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"body.receiver .source-cluster .lamp",
		"body.receiver .source-cluster .lamp .led",
		"body.receiver .source-cluster .lamp .name",
		"body.receiver .source-cluster .lamp.configured-idle .led",
		"body.receiver .source-cluster .lamp.casting .led",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing lamp selector %q", want)
		}
	}
}

func TestChassisCSS_AddsCatalogDrawerRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"body.receiver .catalog-drawer",
		"body.receiver .catalog-browser",
		"body.receiver .catalog-provider-tabs",
		"body.receiver .catalog-tab-indicator",
		"body.receiver .catalog-rail",
		"body.receiver .catalog-grid",
		"body.receiver .ch-card",
		"body.receiver .ch-card .star",
		"body.receiver .ch-card.tuned",
		"body.receiver .ch-card.starred",
		"body.receiver .ch-card.live",
		"@keyframes ch-card-in",
		"@keyframes rail-in",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing catalog drawer selector %q", want)
		}
	}
}

func TestChassisCSS_AddsDragReorderRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		`body.receiver .preset[data-dragging="source"]`,
		"body.receiver .preset.drop-target",
		"body.receiver .preset-drag-clone",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing drag selector %q", want)
		}
	}
}

func TestChassisCSS_AddsSearchFilterRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"body.receiver .search-field",
		"body.receiver .search-field.has-value",
		"body.receiver .filter-miss:not(.tuned)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing search filter selector %q", want)
		}
	}
}

func TestConfig_AcceptsBridgeSaverAndProber(t *testing.T) {
	t.Parallel()
	cfg := Config{
		BridgeSaver: fakeBridgeSettingsSaver{},
		Prober:      fakeProber{},
	}
	if cfg.BridgeSaver == nil {
		t.Errorf("BridgeSaver = nil after assign")
	}
	if cfg.Prober == nil {
		t.Errorf("Prober = nil after assign")
	}
}

func TestMount_MountsBridgeAndProbeRoutes(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv, err := New(Config{
		Version:     "test",
		StartedAt:   time.Now(),
		Registry:    adapters.NewRegistry(),
		BridgeSaver: fakeBridgeSettingsSaver{},
		Prober:      fakeProber{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	srv.Mount(mux)
	for _, path := range []string{"/receiver/settings/bridge", "/receiver/settings/action/probe-mister"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s -> %d (not mounted)", path, rec.Code)
		}
	}
}

func TestMount_WrongOriginRejectsBothNewRoutes(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv, err := New(Config{
		Version:     "test",
		StartedAt:   time.Now(),
		Registry:    adapters.NewRegistry(),
		BridgeSaver: fakeBridgeSettingsSaver{},
		Prober:      fakeProber{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	srv.Mount(mux)
	for _, path := range []string{"/receiver/settings/bridge", "/receiver/settings/action/probe-mister"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		// No Sec-Fetch-Site header — requireSameOrigin rejects.
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without Sec-Fetch-Site -> %d, want 403", path, rec.Code)
		}
	}
}

func TestTemplateHelpers_Dict(t *testing.T) {
	t.Parallel()
	tmpl := template.Must(template.New("t").Funcs(template.FuncMap{
		"dict": dictHelper,
	}).Parse(`{{ $d := dict "k" "v" "n" 42 }}{{ index $d "k" }}|{{ index $d "n" }}`))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := buf.String(); got != "v|42" {
		t.Errorf("got %q, want v|42", got)
	}
}

func TestTemplateHelpers_Itoa(t *testing.T) {
	t.Parallel()
	if got := itoaHelper(32100); got != "32100" {
		t.Errorf("itoaHelper(32100) = %q, want 32100", got)
	}
}

func TestTemplateHelpers_ErrOf(t *testing.T) {
	t.Parallel()
	errs := map[string]string{"mister_host": "is required"}
	if got := errOfHelper(errs, "mister_host"); got != "is required" {
		t.Errorf("got %q, want 'is required'", got)
	}
	if got := errOfHelper(errs, "missing"); got != "" {
		t.Errorf("missing key -> %q, want empty", got)
	}
	if got := errOfHelper(nil, "any"); got != "" {
		t.Errorf("nil map -> %q, want empty", got)
	}
}

func TestTemplateHelpers_SettingsScopeLabel(t *testing.T) {
	t.Parallel()
	if got := settingsScopeLabelHelper("hot"); got != "HOT" {
		t.Errorf("got %q, want HOT", got)
	}
	if got := settingsScopeLabelHelper("reboot"); got != "REBOOT" {
		t.Errorf("got %q, want REBOOT", got)
	}
}

func TestTemplateHelpers_Stub(t *testing.T) {
	t.Parallel()
	got := stubHelper("pipeline", "Pipeline", "4B")
	if got.ID != "pipeline" || got.Title != "Pipeline" || got.Spec != "4B" {
		t.Errorf("got %+v", got)
	}
}

func TestFieldHelper_TextWithValue(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name":  "mister_host",
		"Type":  "text",
		"Label": "Host",
		"Value": "192.168.1.42",
		"Scope": "reboot",
	})
	s := string(html)
	checks := []string{
		`class="field-row"`,
		`name="mister_host"`,
		`value="192.168.1.42"`,
		`has-value`,
		`class="scope reboot"`,
		`>Host`,
	}
	for _, want := range checks {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestFieldHelper_TextEmptyHasNoHasValueClass(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "host_ip", "Type": "text", "Label": "Host IP",
		"Value": "", "Scope": "reboot", "Placeholder": "auto-detect",
	})
	if strings.Contains(string(html), "has-value") {
		t.Errorf("empty value should not have .has-value class:\n%s", html)
	}
	if !strings.Contains(string(html), `placeholder="auto-detect"`) {
		t.Errorf("missing placeholder:\n%s", html)
	}
}

func TestFieldHelper_NumberWithUnit(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "mister_port", "Type": "number", "Label": "Port",
		"Value": "32100", "Scope": "reboot", "Unit": "ms",
	})
	s := string(html)
	if !strings.Contains(s, `type="number"`) {
		t.Errorf("missing type=number")
	}
	if !strings.Contains(s, "ms") {
		t.Errorf("missing unit text 'ms'")
	}
	if !strings.Contains(s, `class="field-input num`) {
		t.Errorf("missing field-input.num class")
	}
}

func TestFieldHelper_Password(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "ssh_password", "Type": "password", "Label": "Password",
		"Value": "secret", "Scope": "hot",
	})
	if !strings.Contains(string(html), `type="password"`) {
		t.Errorf("missing type=password")
	}
}

func TestFieldHelper_Path(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "data_dir", "Type": "path", "Label": "Data dir",
		"Value": "", "Scope": "reboot", "Placeholder": "auto",
	})
	if !strings.Contains(string(html), `class="field-input path`) {
		t.Errorf("missing field-input.path class")
	}
}

func TestFieldHelper_Select(t *testing.T) {
	t.Parallel()
	opts := []map[string]any{
		{"Value": "NTSC_480i", "Label": "NTSC_480i"},
		{"Value": "NTSC_240p", "Label": "NTSC_240p"},
	}
	html := fieldHelper(map[string]any{
		"Name": "modeline", "Type": "select", "Label": "Modeline",
		"Value": "NTSC_240p", "Scope": "recast", "Options": opts,
	})
	s := string(html)
	if !strings.Contains(s, `<select`) || !strings.Contains(s, `</select>`) {
		t.Errorf("missing select tags")
	}
	if !strings.Contains(s, `<option value="NTSC_240p" selected`) {
		t.Errorf("missing selected attribute on the right option:\n%s", s)
	}
}

func TestFieldHelper_Switch(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "lz4", "Type": "switch", "Label": "LZ4",
		"Value": "true", "Scope": "recast",
	})
	s := string(html)
	if !strings.Contains(s, `class="switch on"`) {
		t.Errorf("missing switch.on class for true value:\n%s", s)
	}
	if !strings.Contains(s, `aria-pressed="true"`) {
		t.Errorf("missing aria-pressed:\n%s", s)
	}
	if !strings.Contains(s, `data-field="lz4"`) {
		t.Errorf("missing data-field attribute:\n%s", s)
	}
}

func TestFieldHelper_WithError(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "mister_host", "Type": "text", "Label": "Host",
		"Value": "bad", "Scope": "reboot", "Error": "not a valid IPv4 or hostname",
	})
	s := string(html)
	if !strings.Contains(s, "has-err") {
		t.Errorf("missing has-err class:\n%s", s)
	}
	if !strings.Contains(s, "not a valid IPv4 or hostname") {
		t.Errorf("missing error text:\n%s", s)
	}
}

func TestFieldHelper_HelpText(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "mister_host", "Type": "text", "Label": "Host",
		"Help": "IP or hostname of your MiSTer", "Value": "1.2.3.4", "Scope": "reboot",
	})
	if !strings.Contains(string(html), `class="help"`) {
		t.Errorf("missing .help span")
	}
	if !strings.Contains(string(html), "IP or hostname of your MiSTer") {
		t.Errorf("missing help text")
	}
}

func TestSettingsDrawerTemplate_RendersFiveTabsAndNoticeSlot(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := SettingsData{
		Bridge:               config.BridgeConfig{},
		Errors:               map[string]string{},
		AdapterCount:         6,
		CatalogProviderCount: 3,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	for _, want := range []string{
		`data-tab="network"`,
		`data-tab="pipeline"`,
		`data-tab="adapters"`,
		`<span class="badge">6</span>`,
		`data-tab="catalog"`,
		`<span class="badge">3</span>`,
		`data-tab="advanced"`,
		`id="settings-close"`,
		`class="settings-notice"`,
		`aria-live="polite"`,
		`hidden`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rendered drawer", want)
		}
	}
}

func TestSettingsDrawerTemplate_NetworkPaneRendersAllFields(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := SettingsData{
		Bridge: config.BridgeConfig{
			MiSTer: config.MisterConfig{Host: "192.168.1.42", Port: 32100, SourcePort: 32101},
			UI:     config.UIConfig{HTTPPort: 32500},
			DataDir: "",
			FFmpegPath: "/usr/bin/ffmpeg",
		},
		Errors: map[string]string{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	wantInputs := []string{
		`name="mister_host"`,
		`name="mister_port"`,
		`name="mister_source_port"`,
		`name="ui_http_port"`,
		`name="host_ip"`,
		`name="data_dir"`,
		`name="ffmpeg_path"`,
		`name="ffprobe_path"`,
		`name="ytdlp_path"`,
	}
	for _, w := range wantInputs {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in rendered Network pane", w)
		}
	}
	if !strings.Contains(s, `value="192.168.1.42"`) {
		t.Errorf("mister_host value not rendered from snapshot")
	}
	if !strings.Contains(s, `id="probe-mister-btn"`) {
		t.Errorf("probe button missing")
	}
	if !strings.Contains(s, `id="probe-mister-result"`) {
		t.Errorf("probe result slot missing")
	}
}

func TestSettingsDrawerTemplate_NetworkPaneFieldRowHasScope(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := SettingsData{Bridge: config.BridgeConfig{}, Errors: map[string]string{}}
	var buf bytes.Buffer
	_ = tmpl.ExecuteTemplate(&buf, "settings-drawer", data)
	s := buf.String()
	if !strings.Contains(s, `class="scope reboot"`) {
		t.Errorf("missing REBOOT scope badge")
	}
	if !strings.Contains(s, `class="scope hot"`) {
		t.Errorf("missing HOT scope badge")
	}
}

func TestSettingsDrawerTemplate_StubPanesRenderSpecLabels(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := SettingsData{Errors: map[string]string{}}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	wantPairs := []struct{ pane, spec string }{
		{"pipeline", "4B"},
		{"adapters", "4D"},
		{"catalog", "4C"},
		{"advanced", "4B"},
	}
	for _, w := range wantPairs {
		paneTag := fmt.Sprintf(`data-pane="%s"`, w.pane)
		if !strings.Contains(s, paneTag) {
			t.Errorf("missing pane %q", paneTag)
		}
		if !strings.Contains(s, fmt.Sprintf("Spec %s", w.spec)) {
			t.Errorf("missing Spec %s label for pane %s", w.spec, w.pane)
		}
	}
}

func TestTransportTemplate_GearButtonHasSettingsToggle(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := TransportData{}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "transport", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, `data-settings-toggle`) {
		t.Errorf("gear button missing data-settings-toggle attribute:\n%s", s)
	}
	// Preserve the existing id for legacy test references.
	if !strings.Contains(s, `id="gear-btn"`) {
		t.Errorf("gear button missing id=\"gear-btn\":\n%s", s)
	}
}

func TestShellTemplate_IncludesSettingsDrawerScript(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "settings-drawer.js") {
		t.Errorf("shell template missing settings-drawer.js <script> tag")
	}
}

func TestChassisCSS_HasSettingsInteriorRules(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	wantSelectors := []string{
		".field-row",
		".field-input",
		".field-input.has-value",
		".field-input.num",
		".field-input.path",
		".switch",
		".switch.on",
		".switch::before",
		".action-btn",
		".action-btn.primary",
		".action-result",
		".action-result.shown",
		".action-result.ok",
		".action-result.err",
		".scope",
		".scope.hot",
		".scope.next",
		".scope.recast",
		".scope.reboot",
		".field-row.has-err",
		".field-row .field-err",
		".field-row .row-end",
		".settings-notice",
		".settings-notice.ok",
		".settings-notice.err",
	}
	for _, sel := range wantSelectors {
		if !bytes.Contains(css, []byte(sel)) {
			t.Errorf("chassis.css missing selector %q", sel)
		}
	}
}
