package chassis

import (
	"mime"
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
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "test-1.0.0",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "10.0.0.5",
	}
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
