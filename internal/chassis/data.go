package chassis

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
