package chassis

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// ReceiverPageData is the page-level struct shell.html renders against.
// Each sub-struct holds the smallest set of fields its partial needs;
// live-state fields stay zero/empty in Phase 0. Subsequent specs
// populate them, so the shape is forward-compatible by design.
type ReceiverPageData struct {
	Version    string
	BrandName  string
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

const (
	SourceActionAUXStart = "aux-start"
)

// SourceData is the hardware source selector cluster.
type SourceData struct {
	Buttons []SourceButton
}

// SourceButton represents one entry in the source cluster. AUX renders
// as an hw-btn (Action != ""); STREAMS/PLEX/JELLYFIN/DLNA render as
// indicator lamps (Action == ""). The lamp fields Configured and
// Casting drive three visual states:
//
//	Configured=false → unavailable (lamp dark)
//	Configured=true, Casting=false → idle (lamp dim amber)
//	Configured=true, Casting=true  → active (lamp bright green)
//
// Active / Lit / Unavailable / InputID remain in use for AUX only;
// lamp slots leave them at the zero value.
type SourceButton struct {
	Label       string
	Active      bool
	Lit         bool
	Unavailable bool
	Action      string
	InputID     string

	Configured bool // lamp slots only — adapter is linked/enabled
	Casting    bool // lamp slots only — this source matches transport.AdapterRef
}

func applyAUXSourceState(base *ReceiverPageData, aux AUXStarter) {
	if aux == nil {
		return
	}
	st := aux.AUXStatus(context.Background())
	if st.Active {
		for i := range base.Source.Buttons {
			base.Source.Buttons[i].Active = false
		}
	}
	for i := range base.Source.Buttons {
		if base.Source.Buttons[i].Action != SourceActionAUXStart {
			continue
		}
		base.Source.Buttons[i].Unavailable = !st.Enabled || !st.Configured
		base.Source.Buttons[i].Lit = st.Active
		base.Source.Buttons[i].InputID = st.InputID
		if st.Active {
			base.Source.Buttons[i].Active = true
		}
	}
}

// MeterData is the 3-row signal meter screen. Each sub-row holds idle
// placeholder strings; live values arrive in later specs.
type MeterData struct {
	State       string
	Paused      bool
	Generation  uint64
	SampleSeq   uint64
	SourceStrip SourceStripIdleData
	MidRow      MidRowIdleData
	Readout     ReadoutIdleData
	AudioScopes AudioScopesData
}

// SourceStripIdleData is the top row of the meter screen: input-side
// metadata about the cast source, including codec, dimensions, crop,
// buffer, and drops.
type SourceStripIdleData struct {
	AudioIn           string
	AudioOut          string
	Src               string
	Crop              string
	HLSBuffer         string
	HLSCachedSegments int
	HLSMaxSegments    int
	HLSCacheBytes     int64
	Drops             string
	DropsPercent      float64
	BlitsTotal        uint64
	UnderrunsTotal    uint64
}

// MidRowIdleData is the analytics row: bitrate, sample rate, mode,
// NTSC/PAL lamps, field-flip, throughput, and ACK.
type MidRowIdleData struct {
	BitrateMbps          string
	FreqKHz              string
	Mode                 string
	Standard             string
	StandardNTSC         bool
	StandardPAL          bool
	FieldOrder           string
	FieldRateHz          float64
	InterlacedOutput     bool
	FieldLock            string
	FieldFlip            string
	ThroughputMBs        string
	ThroughputSampleMBs  float64
	ThroughputHistoryMBs []float64
	MSAck                string
	AckSampleMS          float64
	AckHistoryMS         []float64
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
	SpeedRatio  float64
	Link        string
}

// AudioScopesData holds the audio-scope cluster status. Carries the
// discovery hook so meter-only clients can find the high-rate `audio`
// SSE event when a session is active. When idle, only Status is set;
// when live, Via and SampleHz advertise the high-rate channel.
type AudioScopesData struct {
	Status   string
	Via      string
	SampleHz int
}

// TransportData drives the transport row: play/pause/stop buttons,
// seek bar, time readout, and gear. Phase 0 idle keeps buttons dim and
// seek fill at 0%.
type TransportData struct {
	State           string
	SeekFillPercent int
	ElapsedTime     string
	TotalTime       string
	PercentPlayed   string
	OffsetMS        int
	DurationMS      int
	ActionsEnabled  ActionsEnabled
	AdapterRef      string
	Generation      uint64
	OutputVolume    int
}

type ActionsEnabled struct {
	Previous    bool
	Next        bool
	PauseResume bool
	Stop        bool
	Replay      bool
	Seek        bool
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

	Slot       int    // 1..12 — needed for the POST URL
	BadgeClass string // "mtv" | "cartoon" | "toonami" — CSS color hook
	Lit        bool   // currently casting from this slot (server-side initial paint)
	Live       bool   // .preset.live class — always-on live channels
	ProviderID string // streams provider id — for client-side LIT migration
	ChannelID  string // streams channel id — same
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
	return ReceiverPageData{
		Version:   cfg.Version,
		BrandName: "GROOVY · RELAY",
		State:     StateIdle,
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
				{Label: "STREAMS"}, // lamp slot — Action="" routes through applySourceLampState
				{Label: "PLEX"},
				{Label: "JELLYFIN"},
				{Label: "DLNA"},
				{Label: "AUX", Action: SourceActionAUXStart},
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
				Standard:      "",
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
			OutputVolume:    cfg.Bridge.Audio.OutputVolume,
		},
		Visualizer: VisualizerData{
			ActiveMode: defaultVisualizerMode(cfg),
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
		Presets: buildPresetsData(cfg.PresetViewer, "", ""),
		History: HistoryData{
			Rows:         nil,
			EmptyMessage: "No recent casts",
		},
		Settings: SettingsData{
			Open: false,
		},
	}
}

// buildPresetsData hydrates a PresetsData from a PresetViewer (or an
// empty/numbered view when viewer is nil). When activeProviderID and
// activeChannelID are non-empty, the matching slot's Lit is set.
func buildPresetsData(viewer adapters.PresetViewer, activeProviderID, activeChannelID string) PresetsData {
	var data PresetsData
	for i := 0; i < 12; i++ {
		data.Slots[i] = PresetSlot{Slot: i + 1}
	}
	if viewer == nil {
		data.ModeLabel = "Memory · 0 / 12 slots"
		data.Count = "★ 0"
		return data
	}
	entries := viewer.Presets()
	filled := 0
	for i, e := range entries {
		slot := PresetSlot{Slot: i + 1}
		if e.ProviderID != "" {
			slot.Filled = true
			slot.Title = e.Title
			slot.Subtitle = e.BadgeLabel
			slot.BadgeClass = e.BadgeClass
			slot.Live = e.Live
			slot.ProviderID = e.ProviderID
			slot.ChannelID = e.ChannelID
			if activeProviderID != "" && activeChannelID != "" &&
				e.ProviderID == activeProviderID && e.ChannelID == activeChannelID {
				slot.Lit = true
			}
			filled++
		}
		data.Slots[i] = slot
	}
	data.ModeLabel = fmt.Sprintf("Memory · %d / 12 slots", filled)
	data.Count = fmt.Sprintf("★ %d", filled)
	return data
}

// parseStreamsAdapterRef extracts (providerID, channelID) from a streams
// AdapterRef of the form "streams:<providerID>:<channelID>:<sessionID>:
// <itemToken>" (see queueAdapterRef in internal/adapters/streams/
// playback.go). Returns empty strings if the ref doesn't start with
// "streams:" or has fewer than 3 segments.
func parseStreamsAdapterRef(ref string) (providerID, channelID string) {
	if !strings.HasPrefix(ref, "streams:") {
		return "", ""
	}
	parts := strings.SplitN(ref, ":", 5)
	if len(parts) < 3 {
		return "", ""
	}
	return parts[1], parts[2]
}

// parseAdapterRefSource extracts the leading source identifier from
// an AdapterRef ("streams:..." → "streams", "plex:..." → "plex",
// etc.). Returns "" for empty or unknown-format refs. Mirrors the
// chassis source-cluster lamp identifier set.
func parseAdapterRefSource(ref string) string {
	if ref == "" {
		return ""
	}
	colon := strings.IndexByte(ref, ':')
	if colon <= 0 {
		return ""
	}
	switch ref[:colon] {
	case "streams", "plex", "jellyfin", "dlna":
		return ref[:colon]
	}
	return ""
}

// applySourceLampState populates Configured + Casting on every lamp
// slot in base.Source.Buttons (Action == "") using the supplied viewers
// for Configured() and the AdapterRef prefix for Casting. AUX
// (Action != "") is left untouched — applyAUXSourceState owns that
// state. Safe to call with nil viewers; lamps stay at zero (lamp dark).
func applySourceLampState(base *ReceiverPageData, viewers []adapters.SourceAvailabilityViewer, adapterRef string) {
	if base == nil {
		return
	}
	castingSource := parseAdapterRefSource(adapterRef)
	configured := map[string]bool{}
	for _, v := range viewers {
		if v == nil {
			continue
		}
		configured[v.SourceID()] = v.Configured()
	}
	for i := range base.Source.Buttons {
		b := &base.Source.Buttons[i]
		if b.Action != "" {
			// AUX slot — leave alone.
			continue
		}
		id := strings.ToLower(b.Label)
		b.Configured = configured[id]
		b.Casting = id == castingSource && id != ""
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

func defaultVisualizerMode(cfg Config) string {
	mode := config.NormalizeVisualizerMode(cfg.Bridge.Visualizer.Mode)
	if isSupportedVisualizerMode(mode) {
		return mode
	}
	return config.VisualizerModeRetroAnalyzer
}
