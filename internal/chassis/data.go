package chassis

import (
	"fmt"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// ReceiverPageData is the page-level struct shell.html renders against.
// Each sub-struct holds the smallest set of fields its partial needs;
// live-state fields stay zero/empty in Phase 0. Subsequent specs
// populate them, so the shape is forward-compatible by design.
type ReceiverPageData struct {
	Version    string
	BrandName  string
	HostInfo   HostInfo
	State      ReceiverState
	VFD        VFDData
	Source     SourceData
	Meter      MeterData
	Transport  TransportData
	Visualizer VisualizerData
	Input      InputData
	Presets    PresetsData
	History    HistoryData
	Settings   SettingsData
}

// ReceiverState is the body-class controller, either idle or live.
// Phase 0 always renders idle; later specs introduce live.
type ReceiverState string

const (
	StateIdle ReceiverState = "idle"
	StateLive ReceiverState = "live"
)

// HostInfo renders into the status bar. HostIP may be the literal
// string "OFFLINE" if cfg.HostIP was empty on an offline host.
type HostInfo struct {
	HostIP   string
	HTTPPort int
}

// VFDData drives the VFD frame in the top row of the chassis. Idle
// state shows STANDBY plus the marquee hint. SystemTime is rendered
// by the server for initial paint and ticked client-side at minute
// boundaries.
type VFDData struct {
	State        string // "idle" | "live"
	Title        string
	Marquee      string
	QueueCurrent int
	QueueTotal   int
	SystemTime   string
	Uptime       string
}

// SourceData is the 4-button hardware source selector cluster.
type SourceData struct {
	Buttons []SourceButton
}

// SourceButton represents one hardware-style button in the source
// cluster. Active means this tab is currently selected for browsing;
// Lit means this source is currently casting.
type SourceButton struct {
	Label  string
	Active bool
	Lit    bool
}

// MeterData is the 3-row signal meter screen. Each sub-row holds idle
// placeholder strings; live values arrive in later specs.
type MeterData struct {
	State       string
	SourceStrip SourceStripIdleData
	MidRow      MidRowIdleData
	Readout     ReadoutIdleData
}

// SourceStripIdleData is the top row of the meter screen: input-side
// metadata about the cast source, including codec, dimensions, crop,
// buffer, and drops.
type SourceStripIdleData struct {
	AudioIn   string
	AudioOut  string
	Src       string
	Crop      string
	HLSBuffer string
	Drops     string
}

// MidRowIdleData is the analytics row: bitrate, sample rate, mode,
// NTSC/PAL lamps, field-flip, throughput, and ACK.
type MidRowIdleData struct {
	BitrateMbps   string
	FreqKHz       string
	Mode          string
	StandardNTSC  bool
	StandardPAL   bool
	FieldFlip     string
	ThroughputMBs string
	MSAck         string
}

// ReadoutIdleData is the bottom row: audio scope cluster plus output,
// aspect, pipe, speed, and link readouts.
type ReadoutIdleData struct {
	LRBars      int // 0-12, number of lit segments per L/R bar
	PhaseNeedle string
	LUFS        string
	Output      string
	Aspect      string
	Pipe        string
	Speed       string
	Link        string
}

// TransportData drives the transport row: play/pause/stop buttons,
// seek bar, time readout, and gear. Phase 0 idle keeps buttons dim and
// seek fill at 0%.
type TransportData struct {
	PlayState       string
	ElapsedTime     string
	TotalTime       string
	PercentPlayed   string
	SeekFillPercent int
}

// VisualizerData drives the 4-button visualizer-bank selector. One of
// the buttons, radial_spectrum, is rendered as a deferred preview.
type VisualizerData struct {
	ActiveMode string
	Buttons    []VisualizerButton
}

// VisualizerButton represents one visualizer-bank button. IsPreview
// renders the deferred-state badge and short-circuits click handlers.
type VisualizerButton struct {
	Mode      string
	Label     string
	IconKind  string
	IsPreview bool
}

// InputData drives the paste/cast row.
type InputData struct {
	PastePlaceholder string
	DetectedKind     string
	CastEnabled      bool
}

// PresetsData drives the 12-slot preset bank.
type PresetsData struct {
	ModeLabel string
	Count     string
	Slots     [12]PresetSlot
}

// PresetSlot is one entry in the preset grid. Empty Filled=false slots
// still render a numbered placeholder.
type PresetSlot struct {
	Filled   bool
	Title    string
	Subtitle string
}

// HistoryData is the recent-casts row. It is empty in Phase 0 idle.
type HistoryData struct {
	Rows         []HistoryRow
	EmptyMessage string
}

// HistoryRow represents one entry in the recent-casts row. It is empty
// in Phase 0 and populated in a later history spec.
type HistoryRow struct {
	Title   string
	Source  string
	When    string
	Artwork string
}

// SettingsData is the settings drawer, closed in Phase 0.
type SettingsData struct {
	Open bool
}

// idleSnapshot returns a fully populated ReceiverPageData with State =
// StateIdle and placeholder content matching the mockup's idle state.
// Later live-state specs replace this with session-derived snapshots.
func idleSnapshot(cfg Config, now time.Time) ReceiverPageData {
	hostIP := cfg.HostIP
	if hostIP == "" {
		hostIP = "OFFLINE"
	}

	return ReceiverPageData{
		Version:   cfg.Version,
		BrandName: "GROOVY · RELAY",
		State:     StateIdle,
		HostInfo: HostInfo{
			HostIP:   hostIP,
			HTTPPort: cfg.Bridge.UI.HTTPPort,
		},
		VFD: VFDData{
			State:        string(StateIdle),
			Title:        "STANDBY",
			Marquee:      "MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET",
			QueueCurrent: 0,
			QueueTotal:   0,
			SystemTime:   fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute()),
			Uptime:       formatUptime(now.Sub(cfg.StartedAt)),
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
			ActiveMode: defaultVisualizerMode(cfg),
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
}

// formatUptime turns a duration into the "NH NM" string used by the VFD.
// Zero or negative durations render as "0H 0M".
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "0H 0M"
	}
	total := int(d / time.Minute)
	hours := total / 60
	minutes := total % 60
	return fmt.Sprintf("%dH %dM", hours, minutes)
}

// defaultVisualizerMode returns the configured visualizer mode when it is
// renderable in Phase 0, otherwise it falls back to retro_analyzer.
func defaultVisualizerMode(cfg Config) string {
	switch mode := cfg.Bridge.Visualizer.Mode; mode {
	case config.VisualizerModeRetroAnalyzer,
		config.VisualizerModeOscilloscopeWave,
		config.VisualizerModeStereoScope:
		return mode
	default:
		return config.VisualizerModeRetroAnalyzer
	}
}
