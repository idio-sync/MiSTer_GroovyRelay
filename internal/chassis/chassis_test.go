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
		LocalFilesStatusLED: StatusLEDData{
			Label:     "LF",
			AriaLabel: "Local Files adapter not registered",
			Title:     "Local Files adapter not registered",
		},
		VFD: VFDData{
			State:        string(StateIdle),
			Primary:      "STANDBY",
			Secondary:    "MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET",
			QueueCurrent: 0,
			QueueTotal:   0,
			SystemTime:   "22:47",
			Uptime:       "10H 47m",
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
		AudioStrip: AudioStripData{
			EQ:        make([]float64, 10),
			EQLabels:  []string{"31", "63", "125", "250", "500", "1K", "2K", "4K", "8K", "16K"},
			Presets:   []string{"Flat", "Rock", "Jazz", "Vocal"},
			Memory:    [3]AudioStripMemory{{Slot: 1}, {Slot: 2}, {Slot: 3}},
			Persisted: true,
		},
		Visualizer: VisualizerData{
			ActiveMode: config.VisualizerModeStereoScope,
			Buttons: []VisualizerButton{
				{Mode: config.VisualizerModeRetroAnalyzer, Label: "ANALYZER", IconKind: "analyzer"},
				{Mode: config.VisualizerModeOscilloscopeWave, Label: "OSCILLOSCOPE", IconKind: "wave"},
				{Mode: config.VisualizerModeStereoScope, Label: "STEREO SCOPE", IconKind: "scope"},
				{Mode: config.VisualizerModeVUCabinet, Label: "VU CABINET", IconKind: "scope"},
				{Mode: config.VisualizerModeSpectrumWaterfall, Label: "WATERFALL", IconKind: "waterfall"},
				{Mode: config.VisualizerModeRasterPulse, Label: "RASTER PULSE", IconKind: "wave"},
				{Mode: config.VisualizerModeCoverVU, Label: "COVER VU", IconKind: "scope"},
				{Mode: config.VisualizerModeCoverSpectrum, Label: "COVER SPECTRUM", IconKind: "analyzer"},
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
			EmptyMessage: "No recent casts — paste a URL or pick a preset",
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)

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
		`/ui/static/volume-knob.js?v=test-1.0.0`,
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
	if got.VFD.Uptime != "4H 12m" {
		t.Errorf("VFD.Uptime = %q, want 4H 12m", got.VFD.Uptime)
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
		{"options", `{{index (index (options (dict "Value" "a")) 0) "Value"}}`},
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
	src := []byte(`@font-face { src: url('/ui/static/fonts/Inter-Variable.woff2?v={{.Version}}'); }`)
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

func TestChassisCSS_HasWaterfallIcon(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	if !strings.Contains(string(css), "viz-icon--waterfall") {
		t.Error("chassis.css missing .viz-icon--waterfall rule for the WATERFALL button")
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
		"/ui/aux/start",
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
		`fetch('/ui/aux/start'`,
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
		"/ui/visualizer",
		"data-viz",
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/chassis.js", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/transport.js", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/transport.js", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/transport.js", nil)
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
		"serverSeekPercent = rawOffsetMs !== null && durationMs > 0",
		"? seekPercentFromOffset(offsetMs, durationMs)",
		": data.seekFillPercent || 0",
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
		`<!-- chassis:audio-strip -->`,
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

func TestHandleIndex_SettingsDrawerSitsBelowTransport(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	transportIdx := strings.Index(body, `<!-- chassis:transport -->`)
	settingsIdx := strings.Index(body, `<!-- chassis:settings-drawer -->`)
	audioIdx := strings.Index(body, `<!-- chassis:audio-strip -->`)
	if transportIdx < 0 || settingsIdx < 0 || audioIdx < 0 {
		t.Fatalf("missing transport/settings/audio markers")
	}
	if !(transportIdx < settingsIdx && settingsIdx < audioIdx) {
		t.Fatalf("settings drawer should render as the transport service door before audio strip; transport=%d settings=%d audio=%d", transportIdx, settingsIdx, audioIdx)
	}
}

func TestHandleIndex_RendersStableTemplateHooks(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
		`<div class="input-controls">`,
		`<div class="input-panel">`,
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

func TestReceiverInputRendersLocalFilesButtonBesideTorrent(t *testing.T) {
	t.Parallel()
	lf := &fakeLocalFilesService{libraries: []LocalFileLibraryRow{{Name: "Movies", Root: "/media/movies"}}}
	cfg := nonZeroConfig()
	cfg.LocalFiles = lf
	cfg.LocalFilesLibraryEditor = lf
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	torrent := strings.Index(body, `id="upload-btn"`)
	local := strings.Index(body, `id="localfiles-btn"`)
	if torrent < 0 || local < 0 {
		t.Fatalf("input row missing torrent/local buttons: torrent=%d local=%d", torrent, local)
	}
	if local < torrent {
		t.Fatalf("LOCAL FILES button rendered before .TORRENT; want beside it after torrent")
	}
	for _, want := range []string{
		`class="upload-btn" id="localfiles-btn"`,
		`aria-expanded="false"`,
		`>LOCAL FILES</button>`,
		`id="localfiles-drawer"`,
		`data-receiver-localfiles-drawer`,
		`<option value="Movies">Movies</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("receiver local files UI missing %q in:\n%s", want, excerpt(body, "localfiles-btn"))
		}
	}
	if strings.Contains(body, `id="localfiles-close-btn"`) {
		t.Fatalf("receiver local files drawer should use the toolbar button as its close control:\n%s", excerpt(body, "localfiles-drawer"))
	}
}

func TestReceiverInputDisablesLocalFilesButtonWithoutLibraries(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `class="upload-btn" id="localfiles-btn" type="button" aria-controls="localfiles-drawer" aria-expanded="false" disabled`) {
		t.Fatalf("local files button should render disabled without libraries:\n%s", excerpt(body, "localfiles-btn"))
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	transportStart := strings.Index(body, `<!-- chassis:transport -->`)
	transportEnd := strings.Index(body, `<!-- chassis:audio-strip -->`)
	if transportStart == -1 || transportEnd == -1 || transportEnd <= transportStart {
		t.Fatalf("rendered body missing ordered transport/audio-strip markers")
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
		want := fmt.Sprintf(`--seek-percent: %d%%`, percent)
		if !strings.Contains(buf.String(), want) {
			t.Errorf("seek CSS variable with percent %d missing %q; full output:\n%s", percent, want, buf.String())
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`/ui/static/chassis.css?v=test-1.0.0`,
		`/ui/static/chassis.js?v=test-1.0.0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleIndex_AssetURLsIncludeStaticFingerprint(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	rawVersionURL := `/ui/static/chassis.css?v=test-1.0.0"`
	if strings.Contains(body, rawVersionURL) {
		t.Fatalf("asset URL uses only the app version and can stay stale across local Docker rebuilds: %s", rawVersionURL)
	}
	fingerprinted := regexp.MustCompile(`/ui/static/chassis\.css\?v=test-1\.0\.0-[0-9a-f]{12}"`)
	if !fingerprinted.MatchString(body) {
		t.Fatalf("asset URL missing static fingerprint; body:\n%s", body)
	}
}

func TestHandleIndex_TemplateErrorReturns500WithoutPartialBody(t *testing.T) {
	t.Parallel()
	tmpl := template.Must(template.New("chassis").Parse(`{{define "shell.html"}}partial body {{.MissingField}}{{end}}`))
	s := &Server{cfg: nonZeroConfig(), tmpl: tmpl}
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/chassis.css", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/chassis.css", nil)
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
			req := httptest.NewRequest(http.MethodGet, "/ui/static/fonts/"+font, nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/fonts/LICENSE", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/nonexistent.css", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/static/../config.toml", nil)
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

	for _, path := range []string{"/ui", "/ui/"} {
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
		Source:       "jellyfin",
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
		Source:          "plex",
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
	if got.VFD.Primary != "First Day on MTV" {
		t.Errorf("VFD.Primary = %q, want First Day on MTV", got.VFD.Primary)
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

	if got.VFD.Secondary != "" {
		t.Errorf("VFD.Secondary = %q, want empty (no DisplayMetadata set)", got.VFD.Secondary)
	}
	if got.VFD.QueueCurrent != 0 || got.VFD.QueueTotal != 0 {
		t.Errorf("queue should be 0/0 placeholder in Phase 1; got %d/%d",
			got.VFD.QueueCurrent, got.VFD.QueueTotal)
	}
}

func TestSnapshotFromSession_PropagatesAllDisplayTiers(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:  core.StatePlaying,
		Title:  "Legacy Title",
		Source: "jellyfin",
		Display: core.DisplayMetadata{
			Primary:   "Burning Down the House",
			Secondary: "Talking Heads",
			Tertiary:  "Speaking in Tongues · 1983",
		},
	}}
	got := snapshotFromSession(cfg, sv, nil, nil, nil, nil, fixedNow)

	// Display.Primary wins over the legacy Title fallback, and all three
	// tiers propagate end-to-end onto the VFD rows.
	if got.VFD.Primary != "Burning Down the House" {
		t.Errorf("VFD.Primary = %q, want Burning Down the House", got.VFD.Primary)
	}
	if got.VFD.Secondary != "Talking Heads" {
		t.Errorf("VFD.Secondary = %q, want Talking Heads", got.VFD.Secondary)
	}
	if got.VFD.Tertiary != "Speaking in Tongues · 1983" {
		t.Errorf("VFD.Tertiary = %q, want Speaking in Tongues · 1983", got.VFD.Tertiary)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="receiver live"`) {
		t.Errorf("body missing live body class: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "Burning Down the House") {
		t.Errorf("body missing live title (VFD primary)")
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
		Primary:      "TEST-PRIMARY",
		Secondary:    "TEST-SECONDARY",
		Tertiary:     "TEST-TERTIARY",
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
		"vfd-density",
		"vfd-density--dense",
		"vfd-legend-rail",
		"vfd-queue-memory",
		"data-vfd-queue-slots",
		"data-vfd-clock-module",
		"data-vfd-primary",
		"data-vfd-secondary",
		"data-vfd-tertiary",
		"data-vfd-uptime",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("vfd partial missing %q hook; full output:\n%s", want, body)
		}
	}
	legacyQueueHook := regexp.MustCompile(`data-vfd-queue(?:\s|=|>)`)
	if legacyQueueHook.MatchString(body) {
		t.Errorf("vfd partial should not render the legacy right-panel data-vfd-queue hook; full output:\n%s", body)
	}
	// Ghost overlays must still be present — regression guard against
	// accidentally placing data-vfd-* on the outer divs and breaking
	// the seg-text/seg-ghost overlay vocabulary.
	if !strings.Contains(body, "seg-ghost") {
		t.Errorf("vfd partial is missing seg-ghost spans; overlay vocabulary broken")
	}
	if !strings.Contains(body, `class="uptime-label">UPTIME</span>`) {
		t.Errorf("vfd uptime label should use panel-label typography; full output:\n%s", body)
	}
	if strings.Contains(body, `<span class="seg-text">UPTIME</span>`) {
		t.Errorf("vfd uptime label should not render as seven-segment text; full output:\n%s", body)
	}
}

func TestVfdTemplate_LiveDataUsesLiveStateVariant(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	var buf bytes.Buffer
	data := VFDData{
		State:        string(StateLive),
		Primary:      "Burning Down the House",
		Secondary:    "PLEX · 00:08 / 04:01",
		QueueCurrent: 0,
		QueueTotal:   0,
		SystemTime:   "22:47",
		Uptime:       "4H 12M",
	}
	if err := tmpl.ExecuteTemplate(&buf, "vfd", data); err != nil {
		t.Fatalf("execute vfd partial: %v", err)
	}
	body := buf.String()
	liveIdx := strings.Index(body, `class="vfd-state vfd-state--live"`)
	if liveIdx < 0 {
		t.Fatalf("live VFD data must render in a live state wrapper; body:\n%s", body)
	}
	if !strings.Contains(body[liveIdx:], `data-vfd-primary>Burning Down the House</span>`) {
		t.Fatalf("live VFD state wrapper missing live primary hook; body:\n%s", body)
	}
}

func TestShellTemplate_LoadsVfdLiveScript(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/ui/static/vfd-live.js?v=test-1.0.0`) {
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/ui/static/visualizer-bank.js?v=test-1.0.0`) {
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/ui/static/transport.js?v=test-1.0.0`) {
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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

func TestVfdTiersFromView_FallsBackToTitleWhenDisplayPrimaryEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		view          core.StatusHomeView
		wantPrimary   string
		wantSecondary string
		wantTertiary  string
	}{
		{
			name:          "legacy title fallback when Display is empty",
			view:          core.StatusHomeView{State: core.StatePlaying, Title: "My Track"},
			wantPrimary:   "My Track",
			wantSecondary: "",
			wantTertiary:  "",
		},
		{
			name: "Display.Primary overrides Title",
			view: core.StatusHomeView{
				State: core.StatePlaying, Title: "Old Title",
				Display: core.DisplayMetadata{Primary: "New Primary", Secondary: "Sec", Tertiary: "Ter"},
			},
			wantPrimary:   "New Primary",
			wantSecondary: "Sec",
			wantTertiary:  "Ter",
		},
		{
			name:          "empty title and empty Display yields empty primary",
			view:          core.StatusHomeView{State: core.StatePlaying},
			wantPrimary:   "",
			wantSecondary: "",
			wantTertiary:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, s, ter := vfdTiersFromView(tc.view)
			if p != tc.wantPrimary {
				t.Errorf("primary = %q, want %q", p, tc.wantPrimary)
			}
			if s != tc.wantSecondary {
				t.Errorf("secondary = %q, want %q", s, tc.wantSecondary)
			}
			if ter != tc.wantTertiary {
				t.Errorf("tertiary = %q, want %q", ter, tc.wantTertiary)
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

	req := httptest.NewRequest(http.MethodGet, "/ui/static/vfd-live.js", nil)
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
		"data-meter-field-order",
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

func TestMeterTemplate_LinkReadoutReservesStableLatencyWidth(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := idleSnapshot(nonZeroConfig(), time.Unix(1, 0)).Meter
	data.Readout.Link = "MiSTer - 4ms"
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "meter", data); err != nil {
		t.Fatalf("execute meter: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`class="link-value seg-display"`,
		`class="seg-ghost" aria-hidden="true">~~~~~~ - 888~~</span><span class="seg-text" data-meter-link>MiSTer - 4ms</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter LINK readout does not reserve stable latency width %q; body:\n%s", want, body)
		}
	}

	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	cssText := string(cssBytes)
	linkValueRule := cssRuleBlock(t, cssText, "body.receiver .meter-screen--compact .meter-readout-line .grp.link-grp .link-value")
	for _, want := range []string{
		"display: inline-grid;",
		"justify-items: end;",
	} {
		if !strings.Contains(linkValueRule, want) {
			t.Fatalf("LINK value fixed-width rule missing %q: %s", want, linkValueRule)
		}
	}
	linkGhostRule := cssRuleBlock(t, cssText, "body.receiver .meter-screen--compact .meter-readout-line .grp.link-grp .link-value .seg-ghost")
	for _, want := range []string{
		"position: static;",
		"grid-area: 1 / 1;",
	} {
		if !strings.Contains(linkGhostRule, want) {
			t.Fatalf("LINK ghost must reserve width in normal layout, missing %q: %s", want, linkGhostRule)
		}
	}
}

func TestMeterTemplate_FieldOrderDrivesStaticOddEvenLock(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := idleSnapshot(nonZeroConfig(), time.Unix(1, 0)).Meter
	data.MidRow.FieldOrder = "bff"
	data.MidRow.FieldLock = "BFF LOCK"
	data.MidRow.FieldFlip = "BFF LOCK"
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "meter", data); err != nil {
		t.Fatalf("execute meter: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`data-meter-field-order data-field-order="bff"`,
		`data-field-row="tff"`,
		`data-field-row="bff"`,
		`data-meter-field-lock>BFF LOCK`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter template missing field-order contract %q; body:\n%s", want, body)
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
		"data-meter-field-order",
		"setFieldOrder",
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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

func TestChassisJS_SubscribesToHistory(t *testing.T) {
	src, err := chassisStaticFS.ReadFile("static/chassis.js")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(src), `subscribe('history'`) && !strings.Contains(string(src), `subscribe("history"`) {
		t.Error("chassis.js does not subscribe to the history SSE event")
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

func TestInputRowTemplate_UsesResponsiveControlHooks(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := InputData{PastePlaceholder: "Paste URL or magnet", DetectedKind: "URL", CastEnabled: true}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "input-row", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `class="input-controls"`) {
		t.Fatalf("input row must expose a class hook for responsive wrapping.\nHTML:\n%s", html)
	}
	for _, forbidden := range []string{
		`style="display:flex; gap:6px;"`,
		`class="input-panel" style="flex:1"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("input row must not rely on brittle inline layout %q.\nHTML:\n%s", forbidden, html)
		}
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`/ui/static/input-cast.js?v=`,
		`/ui/static/preset-bank.js?v=`,
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`<div class="lamp`,
		`<span class="led-well"`,
		`<span class="state">OFF</span>`,
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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

func TestSourceClusterTemplate_IssueStateUsesSingleLargeLED(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithLampStatus(t,
		map[string]adapters.Status{
			"dlna": {State: adapters.StateError, LastError: "missing bridge host"},
		},
		"",
	)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	dlna := regexp.MustCompile(`(?s)<div class="lamp configured-idle issue"[^>]*data-source-id="dlna"[^>]*>.*?</div>`).FindString(html)
	if dlna == "" {
		t.Fatalf("DLNA issue source module not found: %s", excerpt(html, "DLNA"))
	}
	for _, want := range []string{
		`<span class="led-well"`,
		`<span class="led" aria-hidden="true"></span>`,
		`<span class="state">ISSUE</span>`,
		`aria-label="DLNA, issue: missing bridge host"`,
	} {
		if !strings.Contains(dlna, want) {
			t.Errorf("DLNA issue source module missing %q: %s", want, dlna)
		}
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

func newTestServerWithLampStatus(t *testing.T, statuses map[string]adapters.Status, adapterRef string) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	var viewers []adapters.SourceAvailabilityViewer
	for _, id := range []string{"streams", "plex", "jellyfin", "dlna"} {
		configured := "no"
		st, ok := statuses[id]
		if ok {
			configured = "yes"
		} else {
			st = adapters.Status{State: adapters.StateRunning}
		}
		viewers = append(viewers, fakeSourceViewer{id: id, configured: configured, status: st})
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`/ui/static/source-cluster.js?v=`,
		`/ui/static/catalog-browser.js?v=`,
		`/ui/static/preset-reorder.js?v=`,
		`/ui/static/search-filter.js?v=`,
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
	for _, want := range []string{"issue", "led-well", "state"} {
		if !strings.Contains(s, want) {
			t.Errorf("source-cluster.js missing single-LED status token %q", want)
		}
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("source-cluster.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}

func TestInputCastJSWiresReceiverLocalFilesBrowser(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/input-cast.js")
	if err != nil {
		t.Fatalf("ReadFile input-cast.js: %v", err)
	}
	text := string(src)
	for _, want := range []string{
		`document.getElementById('localfiles-btn')`,
		`document.getElementById('localfiles-drawer')`,
		`/ui/localfiles/browse`,
		`/ui/localfiles/cast`,
		`data-localfiles-dir`,
		`data-localfiles-file`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("input-cast.js missing receiver local files hook %q", want)
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
		"/ui/streams/cast",
		"/ui/preset/star",
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
		"/ui/preset/move",
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
		"body.receiver .source-cluster .lamp .led-well",
		"body.receiver .source-cluster .lamp .led",
		"body.receiver .source-cluster .lamp .name",
		"body.receiver .source-cluster .lamp .state",
		"body.receiver .source-cluster .lamp.configured-idle .led",
		"body.receiver .source-cluster .lamp.casting .led",
		"body.receiver .source-cluster .lamp.issue .led",
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

func TestChassisCSS_CatalogDrawerUsesHardwareControlVocabulary(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(src)
	for _, contract := range []struct {
		name     string
		selector string
		want     []string
	}{
		{
			name:     "provider tab",
			selector: "body.receiver .catalog-provider-tab",
			want: []string{
				"background: linear-gradient(180deg, #3a3a40 0%, #2a2a2e 50%, #1a1a1c 100%)",
				"border: 1px solid #0a0a0b",
				"box-shadow:",
			},
		},
		{
			name:     "provider tab led",
			selector: "body.receiver .catalog-provider-tab::before",
			want: []string{
				"width: 5px",
				"border-radius: 50%",
				"background: #1a1a1c",
			},
		},
		{
			name:     "active provider tab led",
			selector: "body.receiver .catalog-provider-tab.active::before",
			want: []string{
				"background: var(--lock-amber",
				"box-shadow: 0 0 5px",
			},
		},
		{
			name:     "rail group",
			selector: "body.receiver .catalog-rail-group",
			want: []string{
				"background: linear-gradient(180deg, #2a2a30 0%, #1a1a1f 100%)",
				"border: 1px solid #0a0a0b",
				"box-shadow:",
			},
		},
	} {
		rule := cssRuleBlock(t, text, contract.selector)
		for _, want := range contract.want {
			if !strings.Contains(rule, want) {
				t.Errorf("%s rule missing %q:\n%s", contract.name, want, rule)
			}
		}
	}
	indicatorRule := cssRuleBlock(t, text, "body.receiver .catalog-tab-indicator")
	if strings.Contains(indicatorRule, "width 220ms") {
		t.Errorf("catalog tab indicator should not animate width like a web tab:\n%s", indicatorRule)
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
		Version:      "test",
		StartedAt:    time.Now(),
		Registry:     adapters.NewRegistry(),
		BridgeSaver:  fakeBridgeSettingsSaver{},
		Prober:       fakeProber{},
		CoreLauncher: &fakeCoreLauncher{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	srv.Mount(mux)
	for _, path := range []string{
		"/ui/settings/bridge",
		"/ui/settings/action/probe-mister",
		"/ui/settings/action/launch-core",
	} {
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
		Version:      "test",
		StartedAt:    time.Now(),
		Registry:     adapters.NewRegistry(),
		BridgeSaver:  fakeBridgeSettingsSaver{},
		Prober:       fakeProber{},
		CoreLauncher: &fakeCoreLauncher{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	srv.Mount(mux)
	for _, path := range []string{
		"/ui/settings/bridge",
		"/ui/settings/action/probe-mister",
		"/ui/settings/action/launch-core",
	} {
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

func TestSettingsDrawerCSS_AllowsLongPanesToScroll(t *testing.T) {
	t.Parallel()
	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile chassis.css: %v", err)
	}
	css := string(cssBytes)
	for _, want := range []string{
		"grid-template-rows: auto auto minmax(0, 1fr);",
		"max-height: min(1600px, calc(100dvh - 48px));",
		"body.receiver .settings-body {\n  min-height: 0;\n  overflow-y: auto;",
		"overscroll-behavior: contain;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("settings drawer CSS missing scroll contract %q", want)
		}
	}
}

func TestSettingsDrawerTemplate_NetworkPaneRendersAllFields(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := SettingsData{
		Bridge: config.BridgeConfig{
			MiSTer:     config.MisterConfig{Host: "192.168.1.42", Port: 32100, SourcePort: 32101},
			UI:         config.UIConfig{HTTPPort: 32500},
			DataDir:    "",
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

	// pipeline, advanced, catalog, and adapters are now real panes. Plex and
	// Jellyfin are real sections as of 4E; URL is real as of 4F — no stubs remain.
	if strings.Contains(s, "Spec 4F") {
		t.Errorf("Spec 4F stub still present; URL adapter has a real section now")
	}
	if strings.Contains(s, "Spec 4E") {
		t.Errorf("Spec 4E stub still present; Plex and Jellyfin have real sections now")
	}

	// Real panes must be present.
	for _, pane := range []string{"adapters", "pipeline", "advanced", "catalog"} {
		if !strings.Contains(s, fmt.Sprintf(`data-pane="%s"`, pane)) {
			t.Errorf("missing pane %q in drawer", pane)
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
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
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

func TestSettingsDrawerJS_IsServedAsStaticFile(t *testing.T) {
	t.Parallel()
	b, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("read settings-drawer.js: %v", err)
	}
	// Smoke check: file is non-empty and the IIFE pattern is in place.
	if len(b) < 100 {
		t.Errorf("settings-drawer.js is suspiciously short: %d bytes", len(b))
	}
	if !bytes.Contains(b, []byte("settings-open")) {
		t.Errorf("settings-drawer.js does not reference the settings-open class")
	}
	if !bytes.Contains(b, []byte("data-tab")) {
		t.Errorf("settings-drawer.js does not reference data-tab attribute")
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
		// Drawer chrome: section card, hint, tab badge, spacer, close.
		".settings-section",
		".settings-section.wide",
		".settings-section h4 .hint",
		".settings-tab .badge",
		".settings-tab.active .badge",
		".settings-spacer",
		".settings-close",
	}
	for _, sel := range wantSelectors {
		if !bytes.Contains(css, []byte(sel)) {
			t.Errorf("chassis.css missing selector %q", sel)
		}
	}
}

func TestChassisCSS_BrandPlateUsesPrintedComponentLabel(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	rule := cssRuleBlock(t, string(css), "body.receiver .brand-plate .name")
	for _, want := range []string{
		"'Inter'",
		"font: 850 13px/1 'Inter', sans-serif;",
		"letter-spacing: 0.18em;",
		"text-transform: uppercase;",
	} {
		if !strings.Contains(rule, want) {
			t.Fatalf("brand plate should use printed component-label typography; missing %q in:\n%s", want, rule)
		}
	}
	for _, banned := range []string{
		"'DSEG14-Modern'",
		"var(--vfd-glow",
		"var(--vfd)",
	} {
		if strings.Contains(rule, banned) {
			t.Fatalf("brand plate should not use display/VFD typography token %q in:\n%s", banned, rule)
		}
	}
}

func TestHumanizeBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1 KB"},
		{1048576, "1 MB"},
		{1073741824, "1 GB"},
		{268435456, "256 MB"},
		{52428800, "50 MB"},
		{2147483648, "2 GB"},
		{1048576 + 524288, "1.5 MB"}, // exercises the fractional path
	}
	for _, tc := range cases {
		got := humanizeBytes(tc.in)
		if got != tc.want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBoolStr(t *testing.T) {
	t.Parallel()
	if got := boolStr(true); got != "true" {
		t.Errorf("boolStr(true) = %q, want \"true\"", got)
	}
	if got := boolStr(false); got != "false" {
		t.Errorf("boolStr(false) = %q, want \"false\"", got)
	}
}

func TestI64toa(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{268435456, "268435456"},
		{-1, "-1"},
		{1 << 40, "1099511627776"},
	}
	for _, tc := range cases {
		if got := i64toa(tc.in); got != tc.want {
			t.Errorf("i64toa(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPasswordPlaceholder(t *testing.T) {
	t.Parallel()
	if got := passwordPlaceholder(""); got != "not set" {
		t.Errorf("passwordPlaceholder(empty) = %q, want \"not set\"", got)
	}
	if got := passwordPlaceholder("hunter2"); got != "••••••••" {
		t.Errorf("passwordPlaceholder(\"hunter2\") = %q, want \"••••••••\"", got)
	}
	if got := passwordPlaceholder("x"); got != "••••••••" {
		t.Errorf("passwordPlaceholder(\"x\") = %q, want \"••••••••\" (any non-empty)", got)
	}
}

func TestOptions_BuildsSelectOptions(t *testing.T) {
	t.Parallel()
	got := optionsHelper(
		map[string]any{"Value": "NTSC_480i"},
		map[string]any{"Value": "PAL_576i", "Label": "PAL_576i (experimental)"},
	)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0]["Value"] != "NTSC_480i" {
		t.Errorf("got[0].Value = %v, want NTSC_480i", got[0]["Value"])
	}
	if got[1]["Label"] != "PAL_576i (experimental)" {
		t.Errorf("got[1].Label = %v, want PAL_576i (experimental)", got[1]["Label"])
	}
}

func TestFieldHelper_PasswordEmptyValueAndPlaceholder(t *testing.T) {
	t.Parallel()
	html := string(fieldHelper(map[string]any{
		"Name":        "mister_ssh_password",
		"Type":        "password",
		"Label":       "SSH password",
		"Value":       "", // always empty for password
		"Placeholder": "••••••••",
		"Scope":       "hot",
	}))
	if !strings.Contains(html, `type="password"`) {
		t.Errorf("missing type=password: %s", html)
	}
	if !strings.Contains(html, `name="mister_ssh_password"`) {
		t.Errorf("missing name: %s", html)
	}
	if !strings.Contains(html, `value=""`) {
		t.Errorf("expected value=\"\", got: %s", html)
	}
	if !strings.Contains(html, `placeholder="`) {
		t.Errorf("missing placeholder attribute: %s", html)
	}
	// The hex of "••••••••" is escaped under html.EscapeString; the dot
	// glyph passes through as-is so we can match on the literal.
	if !strings.Contains(html, "••••••••") {
		t.Errorf("missing placeholder dots in: %s", html)
	}
	// Phase 4A's password branch auto-applied has-value; 4B does not.
	if strings.Contains(html, "has-value") {
		t.Errorf("unexpected has-value class on empty password input: %s", html)
	}
}

func TestFieldHelper_SkipEmptyEmitsDataAttribute(t *testing.T) {
	t.Parallel()
	html := string(fieldHelper(map[string]any{
		"Name":      "mister_ssh_password",
		"Type":      "password",
		"Label":     "SSH password",
		"Value":     "",
		"SkipEmpty": true,
		"Scope":     "hot",
	}))
	if !strings.Contains(html, `data-skip-empty="true"`) {
		t.Errorf("expected data-skip-empty=true attr, got: %s", html)
	}
}

func TestFieldHelper_SkipEmptyDefaultsOff(t *testing.T) {
	t.Parallel()
	html := string(fieldHelper(map[string]any{
		"Name":  "mister_ssh_user",
		"Type":  "text",
		"Label": "SSH user",
		"Value": "root",
		"Scope": "hot",
	}))
	if strings.Contains(html, `data-skip-empty`) {
		t.Errorf("unexpected data-skip-empty attr on text field: %s", html)
	}
}

func TestFieldHelper_SwitchIncludesNameForErrorPainting(t *testing.T) {
	t.Parallel()
	html := string(fieldHelper(map[string]any{
		"Name":  "logging_debug",
		"Type":  "switch",
		"Label": "Debug logging",
		"Value": "true",
		"Scope": "hot",
	}))
	if !strings.Contains(html, `data-field="logging_debug"`) {
		t.Errorf("switch missing data-field: %s", html)
	}
	if !strings.Contains(html, `name="logging_debug"`) {
		t.Errorf("switch missing name attr for settings-drawer.js error lookup: %s", html)
	}
}

// TestFieldHelper_BridgeInputsCarryDataField pins the mutual-exclusivity
// invariant: bridge fields (no Adapter) are addressed by data-field; adapter
// fields (Adapter set) by data-adapter, never both. This keeps the 4A bridge
// JS handlers ([data-field]) from matching adapter inputs and the 4D adapter
// JS handlers ([data-adapter]) from matching bridge inputs.
func TestFieldHelper_BridgeInputsCarryDataField(t *testing.T) {
	t.Parallel()

	// Bridge text field (no Adapter) must carry data-field, not data-adapter.
	bridge := string(fieldHelper(map[string]any{
		"Name":  "logging_debug",
		"Type":  "text",
		"Label": "Debug logging",
		"Value": "x",
		"Scope": "hot",
	}))
	if !strings.Contains(bridge, `type="text"`) && !strings.Contains(bridge, `class="field-input`) {
		t.Fatalf("expected a text input in bridge render: %s", bridge)
	}
	if !strings.Contains(bridge, `data-field="logging_debug"`) {
		t.Errorf("bridge text field missing data-field: %s", bridge)
	}
	if strings.Contains(bridge, `data-adapter=`) {
		t.Errorf("bridge text field must not carry data-adapter: %s", bridge)
	}

	// Adapter text field (Adapter set) must carry data-adapter, not data-field.
	adapter := string(fieldHelper(map[string]any{
		"Name":    "download_dir",
		"Type":    "text",
		"Label":   "Download dir",
		"Value":   "/downloads",
		"Scope":   "recast",
		"Adapter": "torrent",
	}))
	if !strings.Contains(adapter, `data-adapter="torrent"`) {
		t.Errorf("adapter text field missing data-adapter: %s", adapter)
	}
	if strings.Contains(adapter, `data-field=`) {
		t.Errorf("adapter text field must not carry data-field: %s", adapter)
	}
}

func TestSettingsPipelineTemplate_RendersAllFields(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := SettingsData{
		Bridge: config.BridgeConfig{
			Video:  config.VideoConfig{Modeline: "NTSC_480i", InterlaceFieldOrder: "bff", AspectMode: "auto", LZ4Enabled: true, DeltaLZ4Enabled: true},
			Audio:  config.AudioConfig{SampleRate: 48000, Channels: 2},
			MiSTer: config.MisterConfig{SSHUser: "root", SSHPassword: "stored"},
		},
		Errors: map[string]string{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-pipeline", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	required := []string{
		`data-pane="pipeline"`,
		`name="video_modeline"`,
		`name="video_interlace_field_order"`,
		`name="video_aspect_mode"`,
		`data-field="video_lz4_enabled"`, // switch renders <button data-field=...>
		`data-field="video_delta_lz4_enabled"`,
		`name="audio_sample_rate"`,
		`name="audio_channels"`,
		`name="mister_ssh_user"`,
		`name="mister_ssh_password"`,
		`id="launch-core-btn"`,
		`id="launch-core-result"`,
		`<span class="scope hot">HOT</span>`,       // interlace + ssh_user + ssh_password
		`<span class="scope recast">RECAST</span>`, // most other Pipeline fields
		`data-skip-empty="true"`,
		`••••••••`, // placeholder for stored password
	}
	for _, sub := range required {
		if !strings.Contains(html, sub) {
			t.Errorf("missing %q in:\n%s", sub, html)
		}
	}
	// The password value must NEVER leak into the response.
	if strings.Contains(html, `value="stored"`) {
		t.Errorf("stored password leaked into rendered HTML")
	}
}

func TestSettingsAdvancedTemplate_RendersAllFields(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := SettingsData{
		Bridge: config.BridgeConfig{
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
			Logging: config.LoggingConfig{Debug: false},
		},
		Errors: map[string]string{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-advanced", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	required := []string{
		`data-pane="advanced"`,
		`data-field="hls_enabled"`, // switch
		`name="hls_live_edge_segments"`,
		`name="hls_start_segments"`,
		`name="hls_max_cached_segments"`,
		`name="hls_max_cache_bytes"`,
		`name="hls_max_playlist_bytes"`,
		`name="hls_max_segment_bytes"`,
		`name="hls_segment_timeout_seconds"`,
		`name="hls_playlist_timeout_seconds"`,
		`name="hls_max_variant_height"`,
		`name="hls_stale_cache_reap_hours"`,
		`data-field="logging_debug"`, // switch
		// Byte ceilings edit in a human unit; the raw byte count stays the wire
		// value via data-bytes-scale (settings-drawer.js multiplies back).
		`data-bytes-scale="1048576"`, // cache + segment edit in MB
		`data-bytes-scale="1024"`,    // playlist edits in KB
		`value="256"`,                // 268435456 B -> 256 MB
		`value="50"`,                 // 52428800 B -> 50 MB
		`value="1024"`,               // 1048576 B -> 1024 KB
		`>MB<`,
		`>KB<`,
		// Static "px" unit:
		`>px<`,
	}
	for _, sub := range required {
		if !strings.Contains(html, sub) {
			t.Errorf("missing %q in:\n%s", sub, html)
		}
	}
}

func TestSettingsDrawer_PipelineAndAdvancedReplaceStubs(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := SettingsData{
		Bridge: config.BridgeConfig{
			Video:     config.VideoConfig{Modeline: "NTSC_480i", InterlaceFieldOrder: "bff", AspectMode: "auto"},
			Audio:     config.AudioConfig{SampleRate: 48000, Channels: 2},
			MiSTer:    config.MisterConfig{SSHUser: "root"},
			HLSBuffer: config.HLSBufferConfig{Enabled: true, LiveEdgeSegments: 3, StartSegments: 2, MaxCachedSegments: 6, MaxCacheBytes: 268435456, MaxPlaylistBytes: 1048576, MaxSegmentBytes: 52428800, SegmentTimeoutSeconds: 10, PlaylistTimeoutSeconds: 10, MaxVariantHeight: 720, StaleCacheReapHours: 24},
		},
		Errors: map[string]string{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()

	// Drawer should now contain the real Pipeline + Advanced markers.
	for _, sub := range []string{
		`data-pane="pipeline"`,
		`name="video_modeline"`,
		`id="launch-core-btn"`,
		`data-pane="advanced"`,
		`name="hls_live_edge_segments"`,
		`data-field="logging_debug"`,
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("missing %q in drawer HTML", sub)
		}
	}

	// Stub placeholders must be gone for Pipeline, Advanced, and Catalog
	// (still present for Adapters only).
	if strings.Contains(html, "Spec 4B — implementation in progress") {
		t.Errorf("drawer still contains 4B stub placeholder text")
	}
	if strings.Contains(html, "Spec 4C — implementation in progress") {
		t.Errorf("drawer still contains 4C stub placeholder text")
	}
}

func TestRenderCatalogPane_ProvidersRendered(t *testing.T) {
	data := SettingsData{
		CatalogPaneProviderCount: 3,
		CatalogChannelCount:      90,
		CatalogProviders: []CatalogProviderState{
			{ID: "mtv-rewind", DisplayName: "MTV Rewind", BadgeLabel: "MTV", BadgeClass: "",
				Origin: "wantmymtv.vercel.app", Kind: "youtube-channel-json",
				DefaultChannel: "1stday", ChannelCount: 73, Enabled: true},
			{ID: "cartoon-rewind", DisplayName: "Cartoon Rewind", BadgeLabel: "CART", BadgeClass: "cartoon",
				Origin: "cartoonrewind.tv", Kind: "youtube-channel-json",
				DefaultChannel: "all", ChannelCount: 13, Enabled: true},
			{ID: "toonami-aftermath", DisplayName: "Toonami Aftermath", BadgeLabel: "TOON", BadgeClass: "toonami",
				Origin: "api.toonamiaftermath.com", Kind: "direct-streams",
				DefaultChannel: "", ChannelCount: 4, Live: true, Enabled: true,
				HLSBufferDisabled: false},
		},
		DirectStreamHLSBufferDisabled: false,
	}
	html := renderCatalogPane(t, data)

	wantContains := []string{
		`data-pane="catalog"`,
		`3 PROVIDERS · 90 CHANNELS`,
		`data-catalog-provider="mtv-rewind"`,
		`data-catalog-field="enabled"`,
		`data-catalog-direct-hls`,
		`wantmymtv.vercel.app · youtube-channel-json`,
		`<code>1stday</code>`,
		`73 CH`,
		`data-catalog-provider="toonami-aftermath"`,
		`<span class="scope recast">RECAST</span>`,
	}
	for _, want := range wantContains {
		if !strings.Contains(html, want) {
			t.Errorf("catalog pane HTML missing %q\n---\n%s\n---", want, html)
		}
	}

	// Critical: data-field MUST NOT appear on the catalog switches —
	// collision guard against the existing 4A handler.
	if strings.Contains(html, `data-field="enabled"`) {
		t.Errorf("catalog switch must not carry data-field=enabled; would double-fire 4A handler")
	}
}

func TestRenderCatalogPane_DefaultChannelOmittedWhenEmpty(t *testing.T) {
	data := SettingsData{
		CatalogProviders: []CatalogProviderState{
			{ID: "toonami-aftermath", BadgeLabel: "TOON", BadgeClass: "toonami",
				Origin: "api.toonamiaftermath.com", Kind: "direct-streams",
				DefaultChannel: "", ChannelCount: 4, Live: true},
		},
	}
	html := renderCatalogPane(t, data)
	if strings.Contains(html, `default: <code>`) {
		t.Errorf("expected no `default:` segment when DefaultChannel is empty; got: %s", html)
	}
}

func TestRenderCatalogPane_EmptyOriginOmitsLeadingSeparator(t *testing.T) {
	// When Origin is empty (a provider whose BaseURL/PlaylistURL fail to parse),
	// the stat line must not begin with a dangling " · " separator — Kind leads.
	data := SettingsData{
		CatalogProviders: []CatalogProviderState{
			{ID: "x", DisplayName: "X", BadgeLabel: "X", Origin: "", Kind: "direct-streams",
				DefaultChannel: "", ChannelCount: 1},
		},
	}
	html := renderCatalogPane(t, data)
	if !strings.Contains(html, `<div class="stat">direct-streams`) {
		t.Errorf("empty Origin should make the stat line start with Kind (no leading separator); got: %s", html)
	}
}

func TestRenderCatalogPane_EmptyProvidersStillRendersHLSSection(t *testing.T) {
	data := SettingsData{
		CatalogPaneProviderCount: 0,
		CatalogChannelCount:      0,
		CatalogProviders:         nil,
	}
	html := renderCatalogPane(t, data)
	if !strings.Contains(html, `0 PROVIDERS · 0 CHANNELS`) {
		t.Errorf("expected `0 PROVIDERS · 0 CHANNELS` heading; got: %s", html)
	}
	if !strings.Contains(html, `Per-provider HLS buffer override`) {
		t.Errorf("HLS override section should still render when no providers; got: %s", html)
	}
}

// renderCatalogPane executes the chassis template suite against the given
// SettingsData and returns the rendered settings-catalog block.
func renderCatalogPane(t *testing.T, data SettingsData) string {
	t.Helper()
	tmpl := parseTemplatesForTest(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-catalog", data); err != nil {
		t.Fatalf("execute settings-catalog: %v", err)
	}
	return buf.String()
}

func TestRenderAdvancedPane_DiagnosticsRestoreDefaults(t *testing.T) {
	data := SettingsData{Bridge: config.BridgeConfig{}}
	html := renderAdvancedPane(t, data)

	wantContains := []string{
		`<h4>Diagnostics <span class="hint">read-only</span></h4>`,
		`id="restore-defaults-row"`,
		`id="restore-defaults-btn"`,
		`⚠ Reset…`,
		`id="restore-defaults-result"`,
		`<span class="scope reboot">REBOOT</span>`,
		`config.toml`,
	}
	for _, want := range wantContains {
		if !strings.Contains(html, want) {
			t.Errorf("advanced pane HTML missing %q\n---\n%s\n---", want, html)
		}
	}
}

func renderAdvancedPane(t *testing.T, data SettingsData) string {
	t.Helper()
	tmpl := parseTemplatesForTest(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-advanced", data); err != nil {
		t.Fatalf("execute settings-advanced: %v", err)
	}
	return buf.String()
}

// renderDrawer renders the full settings-drawer template against the given
// SettingsData. Shared by the 4D adapters-pane render tests. The adapters
// pane is composed inside the drawer, so executing the drawer exercises the
// real composition path (settings-adapters -> per-adapter sub-templates).
func renderDrawer(t *testing.T, data SettingsData) string {
	t.Helper()
	tmpl := parseTemplatesForTest(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("execute settings-drawer: %v", err)
	}
	return buf.String()
}

// streamsTopLevelFieldsForTest returns the streams adapter top-level field
// set used by the 4D render tests. Mirrors the real streams adapter Fields()
// shape closely enough to exercise the template's kind/scope/bytes branches.
func streamsTopLevelFieldsForTest() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_url", Kind: adapters.KindText, Label: "Manifest URL", ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_refresh_hours", Kind: adapters.KindInt, Label: "Manifest refresh (h)", ApplyScope: adapters.ScopeHotSwap},
		{Key: "catalog_refresh_hours", Kind: adapters.KindInt, Label: "Catalog refresh (h)", ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_manifest_bytes", Kind: adapters.KindInt, Label: "Max manifest bytes", ApplyScope: adapters.ScopeHotSwap},
		{Key: "youtube_format", Kind: adapters.KindText, Label: "YouTube format", ApplyScope: adapters.ScopeRestartCast},
		{Key: "allow_remote_manifest", Kind: adapters.KindBool, Label: "Allow remote manifest", ApplyScope: adapters.ScopeHotSwap},
		{Key: "allow_local_manifest_urls", Kind: adapters.KindBool, Label: "Allow local manifest URLs", ApplyScope: adapters.ScopeHotSwap},
		{Key: "remote_provider_allowed_hosts", Kind: adapters.KindText, Label: "Remote provider allowed hosts", ApplyScope: adapters.ScopeHotSwap},
	}
}

func TestSettingsAdaptersTemplate_RendersSixSections(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Errors: map[string]string{},
		Adapters: []AdapterPaneData{
			{Name: "dlna", Hint: "PUSH · LISTENING", Fields: []adapters.FieldDef{
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
			}, Values: map[string]any{"enabled": true}},
			{Name: "torrent", Hint: "PASTE-IN · BT", Fields: []adapters.FieldDef{
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
			}, Values: map[string]any{"enabled": false}},
			{Name: "streams", Hint: "PULL · 0 CHANNELS", Fields: []adapters.FieldDef{
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
			}, Values: map[string]any{"enabled": true}},
		},
	}
	s := renderDrawer(t, data)
	// Section headers carry a trailing space before the <span class="hint">,
	// so the faithful header assertion is ">Plex " not ">Plex<".
	for _, want := range []string{
		">Plex ", ">DLNA ", ">URL ", ">Torrent ", ">Jellyfin ", ">Streams catalog ",
		"PUSH ·", "PASTE-IN · BT", "PULL ·",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rendered adapters pane", want)
		}
	}
	// Plex, Jellyfin, and URL stubs are gone; real section templates are now in place.
	for _, gone := range []string{"Spec 4E", "Spec 4F"} {
		if strings.Contains(s, gone) {
			t.Errorf("%q stub still present in rendered adapters pane", gone)
		}
	}
}

func TestSettingsAdapterDLNATemplate_RendersFields(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Errors: map[string]string{},
		Adapters: []AdapterPaneData{
			{Name: "dlna", Hint: "PUSH · LISTENING", Fields: []adapters.FieldDef{
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "device_name", Kind: adapters.KindText, Label: "Device name", ApplyScope: adapters.ScopeRestartBridge},
				{Key: "autoplay_on_set_uri", Kind: adapters.KindBool, Label: "Autoplay on SetAVTransportURI", ApplyScope: adapters.ScopeHotSwap},
				{Key: "allow_public_source_urls", Kind: adapters.KindBool, Label: "Allow public source URLs", ApplyScope: adapters.ScopeHotSwap},
			}, Values: map[string]any{
				"enabled":                  true,
				"device_name":              "GROOVY",
				"autoplay_on_set_uri":      true,
				"allow_public_source_urls": false,
			}},
		},
	}
	s := renderDrawer(t, data)
	for _, want := range []string{
		`name="enabled"`,
		`name="device_name"`,
		`name="autoplay_on_set_uri"`,
		`name="allow_public_source_urls"`,
		`<span class="scope hot">HOT</span>`,
		`<span class="scope reboot">REBOOT</span>`,
		`data-adapter="dlna"`,
		"PUSH · LISTENING",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rendered DLNA pane", want)
		}
	}
}

func TestSettingsAdapterTorrentTemplate_RendersFields(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Errors: map[string]string{},
		Adapters: []AdapterPaneData{
			{Name: "torrent", Hint: "PASTE-IN · BT", Fields: []adapters.FieldDef{
				{Key: "traffic_acknowledged", Kind: adapters.KindBool, Label: "BT traffic acknowledged", ApplyScope: adapters.ScopeHotSwap},
				{Key: "download_dir", Kind: adapters.KindText, Label: "Download dir", ApplyScope: adapters.ScopeRestartCast},
				{Key: "max_cache_bytes", Kind: adapters.KindInt, Label: "Max cache bytes", ApplyScope: adapters.ScopeRestartCast},
			}, Values: map[string]any{
				"traffic_acknowledged": false,
				"download_dir":         "/downloads",
				"max_cache_bytes":      int64(20 * 1024 * 1024 * 1024),
			}},
		},
	}
	s := renderDrawer(t, data)
	for _, want := range []string{
		`<section class="settings-section wide"`,
		">Torrent <",
		"PASTE-IN · BT",
		`name="traffic_acknowledged"`,
		`name="download_dir"`,
		`name="max_cache_bytes"`,
		"20 GB",
		`<span class="scope recast">RECAST</span>`,
		`<span class="scope hot">HOT</span>`,
		`data-adapter="torrent"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rendered Torrent pane", want)
		}
	}
}

func TestSettingsAdapterStreamsTemplate_RendersTopLevelFields(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Errors: map[string]string{},
		Adapters: []AdapterPaneData{
			{Name: "streams", Hint: "PULL · 0 CHANNELS", Fields: streamsTopLevelFieldsForTest(), Values: map[string]any{
				"enabled":                       true,
				"manifest_url":                  "https://x/y.json",
				"manifest_refresh_hours":        int64(6),
				"catalog_refresh_hours":         int64(12),
				"max_manifest_bytes":            int64(4 * 1024 * 1024),
				"youtube_format":                "bestvideo",
				"allow_remote_manifest":         false,
				"allow_local_manifest_urls":     false,
				"remote_provider_allowed_hosts": "",
			}},
		},
	}
	s := renderDrawer(t, data)
	for _, want := range []string{
		">Streams catalog <",
		"PULL ·",
		`name="enabled"`,
		`name="manifest_url"`,
		`name="manifest_refresh_hours"`,
		`name="catalog_refresh_hours"`,
		`name="max_manifest_bytes"`,
		`name="youtube_format"`,
		`name="allow_remote_manifest"`,
		`name="allow_local_manifest_urls"`,
		`name="remote_provider_allowed_hosts"`,
		`data-adapter="streams"`,
		`<span class="scope recast">RECAST</span>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rendered Streams pane", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 25 — provider rows + refresh action render tests
// ---------------------------------------------------------------------------

func TestSettingsAdapterStreams_RendersProviderRows(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"streams": {"enabled": true, "manifest_url": "https://x/y.json"},
		},
		fields: map[string][]adapters.FieldDef{
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "manifest_url", Kind: adapters.KindText, Label: "Manifest URL", ApplyScope: adapters.ScopeHotSwap},
			},
		},
	}
	cat := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "youtube", DisplayName: "YouTube", CatalogRefreshHours: 12},
			{ID: "radio", DisplayName: "Radio", CatalogRefreshHours: 0},
		},
	}
	srv := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
		CatalogManager:       cat,
	}}
	html := renderDrawer(t, srv.buildSettingsData())
	for _, want := range []string{
		`<h5 class="settings-subhead">Provider overrides</h5>`,
		`name="providers.youtube.catalog_refresh_hours"`,
		`name="providers.radio.catalog_refresh_hours"`,
		`value="12"`,
		`>YouTube `,
		`>Radio `,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in rendered Streams provider rows", want)
		}
	}
}

func TestSettingsAdapterStreams_RendersRefreshAction(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"streams": {"enabled": true},
		},
		fields: map[string][]adapters.FieldDef{
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
			},
		},
	}
	srv := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	html := renderDrawer(t, srv.buildSettingsData())
	for _, want := range []string{
		`data-settings-action="streams-refresh"`,
		`↻ Refresh manifest now`,
		`id="streams-refresh-result"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in rendered Streams refresh action row", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 26 — mockup scope-mismatch test
// ---------------------------------------------------------------------------

func TestMockupScopeMismatches(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"torrent": {
				"enabled": false, "download_dir": "", "startup_buffer_seconds": int64(10),
				"max_upload_rate_kbps": int64(512), "max_download_rate_kbps": int64(0),
				"listen_port": int64(0), "max_cache_bytes": int64(20 << 30),
				"metadata_timeout_seconds": int64(60), "keep_completed": false, "traffic_acknowledged": false,
			},
			"dlna": {"enabled": false, "device_name": "M"},
		},
		fields: map[string][]adapters.FieldDef{
			"torrent": {
				{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
				{Key: "download_dir", Kind: adapters.KindText, ApplyScope: adapters.ScopeRestartCast},
				{Key: "startup_buffer_seconds", Kind: adapters.KindInt, ApplyScope: adapters.ScopeHotSwap},
				{Key: "max_upload_rate_kbps", Kind: adapters.KindInt, ApplyScope: adapters.ScopeRestartCast},
				{Key: "max_download_rate_kbps", Kind: adapters.KindInt, ApplyScope: adapters.ScopeRestartCast},
				{Key: "listen_port", Kind: adapters.KindInt, ApplyScope: adapters.ScopeRestartCast},
			},
			"dlna": {
				{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
				{Key: "device_name", Kind: adapters.KindText, ApplyScope: adapters.ScopeRestartBridge},
			},
		},
	}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}}
	html := renderDrawer(t, s.buildSettingsData())
	pairs := []struct{ key, scope string }{
		{"name=\"enabled\"", "hot"},
		{"name=\"download_dir\"", "recast"},
		{"name=\"startup_buffer_seconds\"", "hot"},
		{"name=\"max_upload_rate_kbps\"", "recast"},
		{"name=\"max_download_rate_kbps\"", "recast"},
		{"name=\"listen_port\"", "recast"},
		{"name=\"device_name\"", "reboot"},
	}
	for _, p := range pairs {
		idx := strings.Index(html, p.key)
		if idx < 0 {
			t.Errorf("row for %s missing", p.key)
			continue
		}
		end := idx + 500
		if end > len(html) {
			end = len(html)
		}
		window := html[idx:end]
		want := fmt.Sprintf(`scope %s`, p.scope)
		if !strings.Contains(window, want) {
			t.Errorf("row for %s does not contain scope %s; window:\n%s", p.key, p.scope, window)
		}
	}
}

func TestChassisCSS_ContainsSettingsHintRule(t *testing.T) {
	t.Parallel()
	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile chassis.css: %v", err)
	}
	css := string(cssBytes)
	for _, want := range []string{
		`body.receiver .settings-section .hint`,
		`body.receiver .settings-subhead`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("chassis.css missing selector %q", want)
		}
	}
}

func TestSettingsDrawerJS_BridgeSelectorsNarrowedToDataField(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile settings-drawer.js: %v", err)
	}
	src := string(js)
	for _, want := range []string{
		`input.field-input[data-field]`,
		`select.field-input[data-field]`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("settings-drawer.js missing narrowed selector %q", want)
		}
	}
	for _, banned := range []string{
		`'input.field-input'`,
		`"input.field-input"`,
		`'select.field-input'`,
		`"select.field-input"`,
	} {
		if strings.Contains(src, banned) {
			t.Errorf("settings-drawer.js still contains un-narrowed selector %q — adapter inputs would double-fire", banned)
		}
	}
}

func TestSettingsDrawerJS_HandlesAdapterAttribute(t *testing.T) {
	t.Parallel()
	jsBytes, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile settings-drawer.js: %v", err)
	}
	js := string(jsBytes)
	for _, want := range []string{
		`button.switch[data-adapter]`,
		`input.field-input[data-adapter]`,
		`/ui/settings/adapter/`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("settings-drawer.js missing %q", want)
		}
	}
}

func TestSettingsDrawerJS_StreamsRefreshHandler(t *testing.T) {
	t.Parallel()
	jsBytes, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile settings-drawer.js: %v", err)
	}
	js := string(jsBytes)
	for _, want := range []string{
		`data-settings-action="streams-refresh"`,
		`/ui/settings/action/streams-refresh`,
		`disabled`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("settings-drawer.js missing %q", want)
		}
	}
}

func TestSettingsDrawerJS_LocalFilesBrowseDrawerVisibilityContract(t *testing.T) {
	t.Parallel()
	jsBytes, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile settings-drawer.js: %v", err)
	}
	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile chassis.css: %v", err)
	}
	js := string(jsBytes)
	for _, want := range []string{
		`classList.add('localfiles-open')`,
		`classList.remove('localfiles-open')`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("settings-drawer.js missing Local Files drawer contract %q", want)
		}
	}
	if !strings.Contains(string(cssBytes), `body.receiver .catalog-drawer.localfiles-open`) {
		t.Error("chassis.css missing Local Files drawer open selector")
	}
}

func TestReceiverLocalFilesButtonToggleContract(t *testing.T) {
	t.Parallel()
	jsBytes, err := chassisStaticFS.ReadFile("static/input-cast.js")
	if err != nil {
		t.Fatalf("ReadFile input-cast.js: %v", err)
	}
	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile chassis.css: %v", err)
	}
	js := string(jsBytes)
	for _, want := range []string{
		`document.body.classList.toggle('localfiles-open', open)`,
		`localFilesBtn.setAttribute('aria-expanded', open ? 'true' : 'false')`,
		`isLocalFilesDrawerOpen()`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("input-cast.js missing receiver Local Files toggle contract %q", want)
		}
	}
	css := string(cssBytes)
	for _, want := range []string{
		`body.receiver.localfiles-open .input-controls #localfiles-btn`,
		`body.receiver .input-controls #localfiles-btn:active`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("chassis.css missing receiver Local Files button state selector %q", want)
		}
	}
}

func TestSettingsDrawerJS_AdapterRebootToastUsesLabel(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	src := string(js)
	// The adapter reboot-toast path must derive the human label from the
	// field row's <label> (like the 4A bridge handler), not the raw key or aria-label.
	if strings.Contains(src, "getAttribute('aria-label') || key") {
		t.Errorf("adapter reboot toast still uses aria-label||key instead of the field-row label")
	}
}

func renderLink(t *testing.T, view LinkView) string {
	t.Helper()
	tmpl := parseTemplatesForTest(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-link", view); err != nil {
		t.Fatalf("execute settings-link: %v", err)
	}
	return buf.String()
}

func TestSettingsLink_Renders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		view LinkView
		want string
	}{
		{"pin-unlinked", LinkView{Kind: "pin", Phase: "unlinked"}, "Link Plex Account"},
		{"pin-pending", LinkView{Kind: "pin", Phase: "pending", Code: "K3F9", ExpiresInSec: 120}, "K3F9"},
		{"pin-linked", LinkView{Kind: "pin", Phase: "linked"}, "✓ Linked"},
		{"cred-linked", LinkView{Kind: "credential", Phase: "linked", LinkedAs: "jake on s"}, "jake on s"},
		{"cred-needurl", LinkView{Kind: "credential", Phase: "unlinked", NeedsServerURL: true}, "Server URL"},
		{"cred-form", LinkView{Kind: "credential", Phase: "unlinked", Fields: []LinkField{{Key: "username", Label: "Username", Kind: "text"}, {Key: "password", Label: "Password", Kind: "secret"}}}, "data-link-field"},
		{"error", LinkView{Kind: "credential", Phase: "error", Error: "Invalid credentials", Fields: []LinkField{{Key: "username", Label: "Username", Kind: "text"}}}, "Invalid credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderLink(t, tc.view)
			if !strings.Contains(got, tc.want) {
				t.Errorf("render(%+v) missing %q:\n%s", tc.view, tc.want, got)
			}
		})
	}
}

func TestSettingsAdapterStreams_CatalogOwnedKeysNotRenderedAsInputs(t *testing.T) {
	t.Parallel()
	// Even if catalog-owned per-provider keys reach the pane's Fields (they
	// shouldn't in production — the cmd projection strips them), the template's
	// isProviderOverrideField guard must keep them out of the rendered form.
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "providers.youtube.disabled", Kind: adapters.KindBool, Label: "Disabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "providers.youtube.hls_buffer_disabled", Kind: adapters.KindBool, Label: "HLS buffer disabled", ApplyScope: adapters.ScopeHotSwap},
			},
		},
	}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}}
	html := renderDrawer(t, s.buildSettingsData())
	for _, banned := range []string{
		`name="providers.youtube.disabled"`,
		`name="providers.youtube.hls_buffer_disabled"`,
	} {
		if strings.Contains(html, banned) {
			t.Errorf("Streams form rendered Catalog-owned key as input: %q", banned)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 14 — Plex/Jellyfin section templates + stub swap
// ---------------------------------------------------------------------------

// renderAdaptersPane renders the settings-adapters template against
// settingsDataFromConfig(cfg). The settings-adapters template is composed
// inside the settings-drawer, so we execute the drawer and return its output;
// equivalently we could execute "settings-adapters" directly — the drawer
// guarantees composition has run through the real adapterPane/dict path.
func renderAdaptersPane(t *testing.T, cfg Config) string {
	t.Helper()
	return renderDrawer(t, settingsDataFromConfig(cfg))
}

func TestAdaptersPane_PlexJellyfinSections(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		fields: map[string][]adapters.FieldDef{
			"plex":     {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled"}},
			"jellyfin": {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled"}},
		},
		current: map[string]map[string]any{"plex": {"enabled": true}, "jellyfin": {"enabled": false}},
	}
	linker := &fakeAdapterLinker{views: map[string]LinkView{
		"plex":     {Kind: "pin", Phase: "unlinked"},
		"jellyfin": {Kind: "credential", Phase: "unlinked", NeedsServerURL: true},
	}}
	html := renderAdaptersPane(t, Config{AdapterSettingsSaver: saver, AdapterLinker: linker})

	if strings.Contains(html, "Spec 4E") {
		t.Errorf("stub copy still present:\n%s", html)
	}
	// Section order: Plex above Jellyfin. Assert against the robust
	// data-adapter-section attributes the section templates emit (the <h4>
	// header text is ">Plex <span..." with a trailing space, so ">Plex<"
	// would never match — making the check a silent no-op).
	plexIdx := strings.Index(html, `data-adapter-section="plex"`)
	jfIdx := strings.Index(html, `data-adapter-section="jellyfin"`)
	if plexIdx < 0 || jfIdx < 0 {
		t.Fatalf("plex/jellyfin sections missing: plexIdx=%d jfIdx=%d", plexIdx, jfIdx)
	}
	if plexIdx > jfIdx {
		t.Errorf("Plex section should render above Jellyfin (plexIdx=%d jfIdx=%d)", plexIdx, jfIdx)
	}
	if !strings.Contains(html, "Link Plex Account") {
		t.Errorf("plex Account sub-section missing")
	}
}

func TestHandleIndex_RendersThreeVfdTierHooks(t *testing.T) {
	s := newTestServer(t) // existing helper at chassis_test.go:83
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, hook := range []string{"data-vfd-primary", "data-vfd-secondary", "data-vfd-tertiary"} {
		if !strings.Contains(body, hook) {
			t.Errorf("rendered shell missing %q", hook)
		}
	}
	if strings.Contains(body, "data-vfd-title") || strings.Contains(body, "data-vfd-marquee") {
		t.Errorf("rendered shell still contains old data-vfd-title/marquee hooks")
	}
}

func TestStaticCSS_HasVfdTierAndScrollRules(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/chassis.css", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	css := rr.Body.String()
	for _, want := range []string{".tier-primary", ".tier-secondary", ".tier-tertiary", "vfd-marquee", "prefers-reduced-motion", ".vfd-row.is-empty"} {
		if !strings.Contains(css, want) {
			t.Errorf("chassis.css missing %q", want)
		}
	}
}

func TestRender_AudioStripPresent(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 50
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-audio-strip`,
		`data-dsp-knob="bass"`,
		`data-dsp-eq="0"`,
		`data-dsp-eq="9"`,
		`data-dsp-switch="loudness"`,
		`data-dsp-preset="Flat"`,
		`data-dsp-memory="1"`,
		`data-volume-knob`, // volume relocated into the strip
		`data-eq-led`,      // status-bar EQ LED tagged for live toggle
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

func TestRender_VolumeNotInTransport(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// Locate the transport section (between transport and audio-strip markers).
	transportStart := strings.Index(body, `<!-- chassis:transport -->`)
	transportEnd := strings.Index(body, `<!-- chassis:audio-strip -->`)
	if transportStart == -1 || transportEnd == -1 || transportEnd <= transportStart {
		t.Fatalf("rendered body missing ordered transport/audio-strip markers")
	}
	transportSection := body[transportStart:transportEnd]
	if strings.Contains(transportSection, "data-volume-knob") {
		t.Error("transport section still contains the volume knob; it should have moved to the audio strip")
	}

	// Volume knob must appear inside the audio strip section.
	stripStart := strings.Index(body, `<!-- chassis:audio-strip -->`)
	stripEnd := strings.Index(body, `<!-- chassis:visualizer-bank -->`)
	if stripStart == -1 || stripEnd == -1 || stripEnd <= stripStart {
		t.Fatalf("rendered body missing ordered audio-strip/visualizer markers")
	}
	stripSection := body[stripStart:stripEnd]
	if !strings.Contains(stripSection, "data-volume-knob") {
		t.Error("audio strip section missing volume knob; relocation failed")
	}
}

func TestRender_EQLedOnWhenEngaged(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	// Engaged=true: Enabled=true + at least one shaping parameter non-zero.
	cfg.Bridge.Audio.DSP.Enabled = true
	cfg.Bridge.Audio.DSP.Loudness = true
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="led aqua on"`) {
		t.Errorf("EQ LED should have class 'led aqua on' when AudioStrip.Engaged=true; body snippet around EQ: %s",
			body[max(0, strings.Index(body, "data-eq-led")-50):min(len(body), strings.Index(body, "data-eq-led")+100)])
	}
	if !strings.Contains(body, `data-eq-led`) {
		t.Error("EQ LED missing data-eq-led attribute")
	}
}

func TestRender_StatusBarShowsLocalFilesAdapterLED(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Registry = adapters.NewRegistryWith(fakeStatusAdapter{
		fakeNamedAdapter: fakeNamedAdapter{name: "localfiles"},
		status:           adapters.Status{State: adapters.StateRunning},
	})
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	lf := regexp.MustCompile(`(?s)<span class="led on green"[^>]*data-localfiles-led[^>]*>.*?<span class="lbl">LF</span>`).FindString(body)
	if lf == "" {
		t.Fatalf("Local Files status LED missing or not green/running: %s", excerpt(body, "data-localfiles-led"))
	}
	if !strings.Contains(lf, `aria-label="Local Files adapter running"`) {
		t.Fatalf("Local Files LED missing accessible running status: %s", lf)
	}
}

func TestStaticJS_VfdLiveUsesTierHooks(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/vfd-live.js", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	js := rr.Body.String()
	for _, want := range []string{"data-vfd-primary", "data-vfd-secondary", "data-vfd-tertiary", "is-scrolling", "fonts"} {
		if !strings.Contains(js, want) {
			t.Errorf("vfd-live.js missing %q", want)
		}
	}
}

func TestFieldByKey(t *testing.T) {
	t.Parallel()
	fields := []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "ytdlp_format", Kind: adapters.KindText},
	}
	got := fieldByKey(fields, "ytdlp_format")
	if got == nil || got.Key != "ytdlp_format" || got.Kind != adapters.KindText {
		t.Errorf("fieldByKey = %+v, want ytdlp_format/Text", got)
	}
	missing := fieldByKey(fields, "nope")
	if missing != nil {
		t.Errorf("fieldByKey(missing) = %+v, want nil", missing)
	}
}

func renderSettingsAdapters(t *testing.T, data SettingsData) string {
	t.Helper()
	tmpl := parseTemplatesForTest(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-adapters", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func TestRenderURLPane_TagsAndCookiePill(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Adapters: []AdapterPaneData{{
			Name:           "url",
			Hint:           "PASTE-IN",
			Fields:         []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}, {Key: "ytdlp_enabled", Kind: adapters.KindBool, Label: "yt-dlp resolver", ApplyScope: adapters.ScopeHotSwap}, {Key: "ytdlp_format", Kind: adapters.KindText, Label: "yt-dlp format", ApplyScope: adapters.ScopeHotSwap}, {Key: "ytdlp_resolve_timeout_seconds", Kind: adapters.KindInt, Label: "Resolve timeout (s)", ApplyScope: adapters.ScopeHotSwap}},
			Values:         map[string]any{"enabled": true, "ytdlp_enabled": true, "ytdlp_format": "best", "ytdlp_resolve_timeout_seconds": 30},
			HasHostEditor:  true,
			Hosts:          []string{"youtube.com", "twitch.tv"},
			HasCookieStore: true,
			Cookie:         &CookieStatusView{Loaded: true, Bytes: 64, SetAt: "2026-05-29 00:00:00Z"},
		}},
	}
	out := renderSettingsAdapters(t, data)
	for _, want := range []string{
		`data-host-editor="url"`,
		`data-host="youtube.com"`,
		`data-remove-host="twitch.tv"`,
		`data-add-host`,
		`data-cookies="url"`,
		`data-cookies-save`,
		`data-cookies-clear`,
		`64 B · set 2026-05-29 00:00:00Z`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered URL pane missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderURLPane_EmptyHostsStillShowsEditor(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Adapters: []AdapterPaneData{{
			Name:           "url",
			Hint:           "PASTE-IN",
			Fields:         []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
			Values:         map[string]any{"enabled": false},
			HasHostEditor:  true,
			Hosts:          nil,
			HasCookieStore: true,
			Cookie:         &CookieStatusView{Loaded: false},
		}},
	}
	out := renderSettingsAdapters(t, data)
	if !strings.Contains(out, `data-add-host`) {
		t.Errorf("empty host list dropped the tag editor:\n%s", out)
	}
	if !strings.Contains(out, "not loaded") {
		t.Errorf("cookie pill missing 'not loaded':\n%s", out)
	}
	if strings.Contains(out, `name=""`) {
		t.Errorf("missing URL fields rendered an empty-name input:\n%s", out)
	}
}
