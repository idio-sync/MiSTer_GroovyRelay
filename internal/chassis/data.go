package chassis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// ReceiverPageData is the page-level struct shell.html renders against.
// Each sub-struct holds the smallest set of fields its partial needs;
// live-state fields stay zero/empty in Phase 0. Subsequent specs
// populate them, so the shape is forward-compatible by design.
type ReceiverPageData struct {
	Version             string
	BrandName           string
	State               ReceiverState
	LocalFilesStatusLED StatusLEDData
	VFD                 VFDData
	Source              SourceData
	Meter               MeterData
	Transport           TransportData
	AudioStrip          AudioStripData
	Visualizer          VisualizerData
	Input               InputData
	Presets             PresetsData
	Catalog             CatalogData
	History             HistoryData
	Settings            SettingsData
}

// StatusLEDData renders a compact lamp in the receiver status bar.
type StatusLEDData struct {
	Label     string
	On        bool
	Tone      string
	AriaLabel string
	Title     string
}

// ReceiverState is the body-class controller, either idle or live.
// Phase 0 always renders idle; later specs introduce live.
type ReceiverState string

const (
	StateIdle ReceiverState = "idle"
	StateLive ReceiverState = "live"
)

// VFDData drives the VFD frame in the top row of the chassis. The three
// tiers map per media type (see the metadata design spec); empty tiers
// render as collapsed rows. SystemTime is server-rendered for first
// paint and ticked client-side.
type VFDData struct {
	State        string // "idle" | "live"
	Primary      string
	Secondary    string
	Tertiary     string
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
// indicator lamps (Action == ""). The lamp fields Configured, Casting,
// and Issue drive four visual states:
//
//	Configured=false → unavailable (lamp dark)
//	Configured=true, Casting=false → idle (lamp dim amber)
//	Configured=true, Casting=true  → active (lamp bright green)
//	Issue=true                    → issue (lamp red; LastError gives detail)
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
	Issue      bool // lamp slots only — adapter Status() reports StateError
	LastError  string
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
	Source          string
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

// AudioStripData drives the always-on audio/EQ face module. Tone/balance/EQ
// are the live tone-control values; Memory carries per-slot labels + stored
// flags for the M1..M3 buttons. OutputVolume duplicates the transport's
// volume so the relocated knob renders from this module's data; the volume
// SSE event + volume-knob.js are unchanged.
type AudioStripData struct {
	Enabled      bool // false = defeat (EQ OUT engaged)
	Mono         bool
	Subsonic     bool
	Loudness     bool
	Bass         float64
	Mid          float64
	Treble       float64
	Balance      int
	EQ           []float64 // 10 bands
	EQLabels     []string  // frequency labels for EQ bands (e.g. "31", "63", ..., "16K")
	Presets      []string  // named preset buttons (e.g. "Flat", "Rock", ...)
	Memory       [3]AudioStripMemory
	Engaged      bool // status-bar EQ LED
	Persisted    bool // false while a preview runs ahead of disk
	OutputVolume int
}

// AudioStripMemory is one M1..M3 preset slot for the audio strip.
type AudioStripMemory struct {
	Slot   int
	Name   string
	Stored bool
}

// audioStripFromDSP flattens a config.AudioDSP (+ volume + persisted flag)
// into the render/SSE struct, normalizing the 3 memory slots so the template
// always renders M1..M3.
func audioStripFromDSP(d config.AudioDSP, engaged, persisted bool, volume int) AudioStripData {
	out := AudioStripData{
		Enabled:      d.Enabled,
		Mono:         d.Mono,
		Subsonic:     d.Subsonic,
		Loudness:     d.Loudness,
		Bass:         d.Bass,
		Mid:          d.Mid,
		Treble:       d.Treble,
		Balance:      d.Balance,
		EQ:           append([]float64(nil), d.EQ...),
		EQLabels:     []string{"31", "63", "125", "250", "500", "1K", "2K", "4K", "8K", "16K"},
		Presets:      []string{"Flat", "Rock", "Jazz", "Vocal"},
		Engaged:      engaged,
		Persisted:    persisted,
		OutputVolume: volume,
	}
	if len(out.EQ) != 10 {
		eq := make([]float64, 10)
		copy(eq, out.EQ)
		out.EQ = eq
	}
	for i := range out.Memory {
		out.Memory[i] = AudioStripMemory{Slot: i + 1}
	}
	for _, m := range d.Memory {
		if m.Slot >= 1 && m.Slot <= 3 {
			out.Memory[m.Slot-1] = AudioStripMemory{Slot: m.Slot, Name: m.Name, Stored: m.Stored}
		}
	}
	return out
}

// VisualizerData drives the visualizer-bank selector.
type VisualizerData struct {
	ActiveMode string
	Buttons    []VisualizerButton
}

// VisualizerButton represents one visualizer-bank button.
type VisualizerButton struct {
	Mode     string
	Label    string
	IconKind string
}

// InputData drives the paste/cast row.
type InputData struct {
	PastePlaceholder    string
	DetectedKind        string
	CastEnabled         bool
	LocalFilesAvailable bool
	LocalFilesLibraries []LocalFileLibraryRow
}

// PresetsData drives the 12-slot preset bank. CatalogTotalChannels is
// a copy of CatalogData.TotalChannels populated by the snapshot helpers
// — it lives on PresetsData so the preset-bank template (whose `.` is
// PresetsData) can render the "Browse full catalog (N)" label without
// a template wrapper.
type PresetsData struct {
	ModeLabel            string
	Count                string
	CatalogTotalChannels int
	Slots                [12]PresetSlot
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

// HistoryData is the recent-casts row. Rows come from registered adapters
// that expose a redacted companion history snapshot.
type HistoryData struct {
	Rows         []HistoryRow
	EmptyMessage string
}

// HistoryRow represents one entry in the recent-casts row.
type HistoryRow struct {
	Title    string
	Source   string
	When     string
	Artwork  string
	ReplayID string
}

type companionHistoryProvider interface {
	CompanionHistory() []companion.CompanionHistoryEntry
}

type companionHistoryPlayProvider interface {
	CompanionHistoryPlay(context.Context, string) (companion.CompanionPlayResult, error)
}

func historyDataFromRegistry(reg *adapters.Registry, now time.Time) HistoryData {
	out := HistoryData{EmptyMessage: "No recent casts"}
	if reg == nil {
		return out
	}

	type datedRow struct {
		row        HistoryRow
		lastPlayed time.Time
	}
	rows := []datedRow{}
	for _, adapter := range reg.List() {
		provider, ok := adapter.(companionHistoryProvider)
		if !ok {
			continue
		}
		_, canReplay := adapter.(companionHistoryPlayProvider)
		source := historySourceLabel(adapter)
		artwork := historyArtworkLabel(adapter)
		for _, entry := range provider.CompanionHistory() {
			title := strings.TrimSpace(entry.Title)
			if title == "" {
				title = strings.TrimSpace(entry.URLDisplay)
			}
			if title == "" {
				continue
			}
			replayID := ""
			if canReplay {
				replayID = strings.TrimSpace(entry.ID)
			}
			rows = append(rows, datedRow{
				row: HistoryRow{
					Title:    title,
					Source:   source,
					When:     formatHistoryAge(now, entry.LastPlayed),
					Artwork:  artwork,
					ReplayID: replayID,
				},
				lastPlayed: entry.LastPlayed,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].lastPlayed.After(rows[j].lastPlayed)
	})
	if len(rows) == 0 {
		return out
	}
	out.Rows = make([]HistoryRow, 0, len(rows))
	for _, r := range rows {
		out.Rows = append(out.Rows, r.row)
	}
	return out
}

func historySourceLabel(adapter adapters.Adapter) string {
	label := strings.TrimSpace(adapter.DisplayName())
	if label == "" {
		label = adapter.Name()
	}
	return strings.ToUpper(label)
}

func historyArtworkLabel(adapter adapters.Adapter) string {
	label := strings.TrimSpace(adapter.Name())
	if label == "" {
		label = adapter.DisplayName()
	}
	label = strings.ToUpper(label)
	if len(label) > 3 {
		return label[:3]
	}
	return label
}

func formatHistoryAge(now, lastPlayed time.Time) string {
	if lastPlayed.IsZero() {
		return "--"
	}
	d := now.Sub(lastPlayed)
	if d < time.Minute {
		return "NOW"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dM AGO", int(d/time.Minute))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dH AGO", int(d/time.Hour))
	}
	return fmt.Sprintf("%dD AGO", int(d/(24*time.Hour)))
}

// SettingsData carries the settings drawer state into the snapshot.
// First-render values come from BridgeSettingsSaver.Current() (or
// startup cfg.Bridge in offline test paths). Errors is always empty on
// the first server render; it is populated only when the server re-
// renders after a redirect following a failed save, which 4A never
// does (the JSON error path is purely client-side).
type SettingsData struct {
	Open                          bool
	Bridge                        config.BridgeConfig
	Errors                        map[string]string
	AdapterCount                  int
	CatalogProviderCount          int                    // existing tab badge count from StreamsCatalogViewer
	CatalogPaneProviderCount      int                    // 4C — len(CatalogProviders)
	CatalogProviders              []CatalogProviderState // 4C
	CatalogChannelCount           int                    // 4C — sum across CatalogProviders
	DirectStreamHLSBufferDisabled bool                   // 4C — true iff every Live provider has hls_buffer_disabled
	Adapters                      []AdapterPaneData      // 4D — per-adapter render context
}

// AdapterPaneData carries the per-adapter render context for the Adapters
// pane. Populated by settingsDataFromConfig: config Fields/Values from the
// AdapterSettingsSaver, and (4E) Linkable + LinkView from the AdapterLinker
// for adapters that expose a link/pairing flow (Plex, Jellyfin).
type AdapterPaneData struct {
	Name      string
	Hint      string
	Fields    []adapters.FieldDef
	Values    map[string]any
	Providers []AdapterProviderRow
	Linkable  bool     // 4E — render the Account sub-section for this pane
	LinkView  LinkView // 4E — valid only when Linkable is true

	// 4F — URL bespoke widgets. Flags gate rendering so a valid empty
	// host list still shows the tag editor. Zero/nil for other adapters.
	HasHostEditor  bool
	Hosts          []string
	HasCookieStore bool
	Cookie         *CookieStatusView

	// Local Files bespoke widgets.
	HasLibraryEditor bool
	Libraries        []LocalFileLibraryRow
	HasBrowseDrawer  bool
}

// AdapterProviderRow is one row in the Streams per-provider sub-section.
type AdapterProviderRow struct {
	ID                  string
	DisplayName         string
	CatalogRefreshHours int
}

// buildSettingsData composes the chassis-rendered settings drawer state
// from a bridge config, the adapter registry, the streams catalog
// viewer, and an optional CatalogSettingsManager. AUX is excluded from
// AdapterCount: it is a hardware-button surface, not a configurable
// adapter in the Settings UI sense.
func buildSettingsData(
	bridge config.BridgeConfig,
	registry *adapters.Registry,
	catalog adapters.StreamsCatalogViewer,
	catalogManager CatalogSettingsManager,
) SettingsData {
	adapterCount := 0
	if registry != nil {
		for _, a := range registry.List() {
			if a.Name() == "aux" {
				continue
			}
			adapterCount++
		}
	}
	catalogProviderCount := 0
	if catalog != nil {
		catalogProviderCount = len(catalog.Catalog())
	}
	out := SettingsData{
		Bridge:               bridge,
		Errors:               map[string]string{},
		AdapterCount:         adapterCount,
		CatalogProviderCount: catalogProviderCount,
	}
	if catalogManager != nil {
		providers := catalogManager.Providers()
		out.CatalogProviders = providers
		out.CatalogPaneProviderCount = len(providers)
		channelTotal := 0
		liveCount := 0
		liveDisabledCount := 0
		for _, p := range providers {
			channelTotal += p.ChannelCount
			if p.Live {
				liveCount++
				if p.HLSBufferDisabled {
					liveDisabledCount++
				}
			}
		}
		out.CatalogChannelCount = channelTotal
		out.DirectStreamHLSBufferDisabled = liveCount > 0 && liveDisabledCount == liveCount
	}
	return out
}

// CatalogData drives the catalog drawer. Open is always false at
// server-render time; client-side JS flips body.browse-open on
// BROWSE click. TunedProviderID/TunedChannelID derive from
// transport.AdapterRef so the drawer's .tuned state matches the
// preset bank's .lit and the source cluster's .casting.
type CatalogData struct {
	Open             bool
	Providers        []CatalogProviderTab
	ActiveProviderID string
	ActiveGroupID    string
	TunedProviderID  string
	TunedChannelID   string
	PresetMembership map[string]int // "provider:channel" → slot (1..12)
	TotalChannels    int            // sum across all providers
}

// CatalogProviderTab is one provider tab in the drawer's top strip.
type CatalogProviderTab struct {
	ID, DisplayName, BadgeLabel, BadgeClass string
	Live                                    bool
	ChCount                                 int
	Groups                                  []CatalogGroupTab
}

// CatalogGroupTab is one button in the rail.
type CatalogGroupTab struct {
	ID, Name string
	ChCount  int
	Channels []CatalogChannelCard
}

// CatalogChannelCard is one .ch-card in the grid.
type CatalogChannelCard struct {
	ID, Name, PlayMode string
	Live               bool
	Tuned              bool // matches transport.AdapterRef
	Starred            bool // is in a preset slot
	PresetSlot         int  // 0 if !Starred; 1..12 otherwise
}

// ProviderIndex returns the slice index of a provider by ID for use
// in catalog-rail.html / catalog-grid.html templates. Returns 0 if
// the ID is not found — defense-in-depth so template execution
// cannot panic on a transient catalog reload race.
func (d CatalogData) ProviderIndex(id string) int {
	for i, p := range d.Providers {
		if p.ID == id {
			return i
		}
	}
	return 0
}

// GroupIndex returns the slice index of a group within a provider.
// Returns 0 if either the provider or the group is not found.
func (d CatalogData) GroupIndex(providerID, groupID string) int {
	pi := d.ProviderIndex(providerID)
	if pi >= len(d.Providers) {
		return 0
	}
	for i, g := range d.Providers[pi].Groups {
		if g.ID == groupID {
			return i
		}
	}
	return 0
}

// buildCatalogData shape-shifts the adapter catalog into the chassis
// CatalogData. Resolves Starred + PresetSlot from the presets slice;
// resolves Tuned from the supplied adapterRef. Pure function — no
// allocations the chassis snapshot tick can't afford.
func buildCatalogData(cat []adapters.CatalogProvider, presets [12]adapters.PresetEntry, adapterRef string) CatalogData {
	membership := map[string]int{}
	for _, p := range presets {
		if p.ProviderID == "" {
			continue
		}
		membership[p.ProviderID+":"+p.ChannelID] = p.Slot
	}
	var tunedProvider, tunedChannel string
	if parseAdapterRefSource(adapterRef) == "streams" {
		tunedProvider, tunedChannel = parseStreamsAdapterRef(adapterRef)
	}

	data := CatalogData{
		TunedProviderID:  tunedProvider,
		TunedChannelID:   tunedChannel,
		PresetMembership: membership,
	}
	for _, p := range cat {
		tab := CatalogProviderTab{
			ID: p.ID, DisplayName: p.DisplayName,
			BadgeLabel: p.BadgeLabel, BadgeClass: p.BadgeClass,
			Live: p.Live,
		}
		for _, g := range p.Groups {
			gt := CatalogGroupTab{ID: g.ID, Name: g.Name}
			for _, c := range g.Channels {
				key := p.ID + ":" + c.ID
				// Map-ok pattern: inPreset is true iff the key exists in
				// membership; slot is the int value (0 when absent).
				slot, inPreset := membership[key]
				card := CatalogChannelCard{
					ID: c.ID, Name: c.Name, PlayMode: c.PlayMode,
					Live:       c.Live,
					Tuned:      p.ID == tunedProvider && c.ID == tunedChannel && tunedProvider != "",
					Starred:    inPreset,
					PresetSlot: slot,
				}
				gt.Channels = append(gt.Channels, card)
			}
			gt.ChCount = len(gt.Channels)
			tab.ChCount += gt.ChCount
			tab.Groups = append(tab.Groups, gt)
		}
		data.TotalChannels += tab.ChCount
		data.Providers = append(data.Providers, tab)
	}
	if len(data.Providers) > 0 {
		data.ActiveProviderID = data.Providers[0].ID
		if len(data.Providers[0].Groups) > 0 {
			data.ActiveGroupID = data.Providers[0].Groups[0].ID
		}
	}
	return data
}

// settingsDataFromConfig reads the bridge config from the BridgeSettingsSaver
// when wired (production), or falls back to startup cfg.Bridge for offline
// tests / nil-saver render paths. It is the single production entry point for
// the settings drawer snapshot: idleSnapshot and all live-session snapshot
// helpers call it, so the 4D-owned Adapters population must live here (not on
// the *Server method) to render in the running app. Mirrors how 4C threaded
// catalog data through this package path via cfg fields.
func settingsDataFromConfig(cfg Config) SettingsData {
	bridge := cfg.Bridge
	if cfg.BridgeSaver != nil {
		bridge = cfg.BridgeSaver.Current()
	}
	data := buildSettingsData(bridge, cfg.Registry, cfg.StreamsCatalogViewer, cfg.CatalogManager)
	// 4D — append per-adapter panes from the AdapterSettingsSaver. Nil-guarded
	// so 4A/4B/4C configs (no saver wired) leave data.Adapters nil, preserving
	// the prior package-builder behavior exactly.
	if saver := cfg.AdapterSettingsSaver; saver != nil {
		for _, name := range []string{"dlna", "torrent", "streams", "plex", "jellyfin", "url", "localfiles"} {
			fields, ok := saver.Fields(name)
			if !ok {
				continue
			}
			values, _ := saver.Current(name)
			pane := AdapterPaneData{
				Name:   name,
				Hint:   buildAdapterHint(cfg, name, values),
				Fields: fields,
				Values: values,
			}
			if name == "streams" {
				pane.Providers = buildStreamsProviderRows(cfg)
			}
			if name == "url" {
				pane.Hint = "PASTE-IN"
				if he := cfg.AdapterHostEditor; he != nil {
					if hosts, ok := he.Hosts("url"); ok {
						pane.HasHostEditor = true
						pane.Hosts = hosts
					}
				}
				if store := cfg.AdapterCookieStore; store != nil {
					if view, ok := store.CookieStatus("url"); ok {
						pane.HasCookieStore = true
						v := view
						pane.Cookie = &v
					}
				}
			}
			if name == "localfiles" {
				pane.Hint = "LOCAL DISK"
				if le := cfg.LocalFilesLibraryEditor; le != nil {
					pane.HasLibraryEditor = true
					pane.Libraries = le.Libraries()
					pane.HasBrowseDrawer = cfg.LocalFiles != nil
				}
			}
			if cfg.AdapterLinker != nil {
				if lv, ok := cfg.AdapterLinker.LinkView(name); ok {
					pane.Linkable = true
					pane.LinkView = lv
				}
			}
			data.Adapters = append(data.Adapters, pane)
		}
	}
	return data
}

// buildSettingsData (method) is the single named entry point for the settings
// drawer snapshot. It now simply delegates to the package-level
// settingsDataFromConfig, which both this method and the production
// idleSnapshot / live-session paths share. Kept for the plan's
// single-entry-point intent and existing test callers.
func (s *Server) buildSettingsData() SettingsData {
	return settingsDataFromConfig(s.cfg)
}

// buildAdapterHint returns the section-header subtitle for each adapter pane.
// DLNA, plex, and jellyfin reflect the enabled state (LISTENING/DISABLED),
// torrent is a static protocol tag, and streams sums provider channel counts
// from CatalogManager. Returns "" for any unknown adapter name
// (forward-compatible).
func buildAdapterHint(cfg Config, name string, values map[string]any) string {
	switch name {
	case "dlna":
		if v, _ := values["enabled"].(bool); v {
			return "PUSH · LISTENING"
		}
		return "PUSH · DISABLED"
	case "torrent":
		return "PASTE-IN · BT"
	case "streams":
		n := 0
		if cfg.CatalogManager != nil {
			for _, p := range cfg.CatalogManager.Providers() {
				n += p.ChannelCount
			}
			return fmt.Sprintf("PULL · %d CHANNELS · see Catalog tab", n)
		}
		return fmt.Sprintf("PULL · %d CHANNELS", n)
	case "localfiles":
		return "LOCAL DISK"
	case "plex":
		if v, _ := values["enabled"].(bool); v {
			return "CAST · LISTENING"
		}
		return "CAST · DISABLED"
	case "jellyfin":
		if v, _ := values["enabled"].(bool); v {
			return "CAST · LISTENING"
		}
		return "CAST · DISABLED"
	}
	return ""
}

// buildStreamsProviderRows projects 4C's CatalogManager.Providers()
// output into the Streams-pane per-provider override row shape.
// Returns nil when CatalogManager is unwired (offline tests).
func buildStreamsProviderRows(cfg Config) []AdapterProviderRow {
	if cfg.CatalogManager == nil {
		return nil
	}
	providers := cfg.CatalogManager.Providers()
	rows := make([]AdapterProviderRow, 0, len(providers))
	for _, p := range providers {
		rows = append(rows, AdapterProviderRow{
			ID:                  p.ID,
			DisplayName:         p.DisplayName,
			CatalogRefreshHours: p.CatalogRefreshHours,
		})
	}
	return rows
}

// idleSnapshot returns a fully populated ReceiverPageData with State =
// StateIdle and placeholder content matching the mockup's idle state.
// Later live-state specs replace this with session-derived snapshots.
func idleSnapshot(cfg Config, now time.Time) ReceiverPageData {
	base := ReceiverPageData{
		Version:             cfg.Version,
		BrandName:           "GROOVY · RELAY",
		State:               StateIdle,
		LocalFilesStatusLED: localFilesStatusLED(cfg.Registry),
		VFD: VFDData{
			State:        string(StateIdle),
			Primary:      "STANDBY",
			Secondary:    "MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET",
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
		AudioStrip: audioStripFromDSP(
			cfg.Bridge.Audio.DSP,
			cfg.Bridge.Audio.DSP.Engaged(),
			true, // idle = persisted (no preview)
			cfg.Bridge.Audio.OutputVolume,
		),
		Visualizer: VisualizerData{
			ActiveMode: defaultVisualizerMode(cfg),
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
		Input:    inputDataFromConfig(cfg),
		Presets:  buildPresetsData(cfg.PresetViewer, "", ""),
		Catalog:  idleCatalogData(cfg),
		History:  historyDataFromRegistry(cfg.Registry, now),
		Settings: settingsDataFromConfig(cfg),
	}
	// Bridge catalog count to PresetsData so the preset-bank template
	// can render "Browse full catalog (N)" without a template wrapper.
	base.Presets.CatalogTotalChannels = base.Catalog.TotalChannels
	return base
}

func localFilesStatusLED(reg *adapters.Registry) StatusLEDData {
	led := StatusLEDData{
		Label:     "LF",
		AriaLabel: "Local Files adapter not registered",
		Title:     "Local Files adapter not registered",
	}
	if reg == nil {
		return led
	}
	adapter, ok := reg.Get("localfiles")
	if !ok || adapter == nil {
		return led
	}
	status := adapter.Status()
	state := adapterStatusText(status.State)
	led.AriaLabel = "Local Files adapter " + state
	led.Title = led.AriaLabel
	switch status.State {
	case adapters.StateRunning:
		led.On = true
		led.Tone = "green"
	case adapters.StateError:
		led.On = true
		led.Tone = "red"
		if msg := strings.TrimSpace(status.LastError); msg != "" {
			led.AriaLabel += ": " + msg
			led.Title = led.AriaLabel
		}
	}
	return led
}

func adapterStatusText(state adapters.State) string {
	switch state {
	case adapters.StateRunning:
		return "running"
	case adapters.StateError:
		return "error"
	case adapters.StateStarting:
		return "starting"
	case adapters.StateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func inputDataFromConfig(cfg Config) InputData {
	data := InputData{
		PastePlaceholder: "Paste URL or magnet",
		DetectedKind:     "URL",
		CastEnabled:      false,
	}
	if cfg.LocalFiles == nil || cfg.LocalFilesLibraryEditor == nil {
		return data
	}
	libs := cfg.LocalFilesLibraryEditor.Libraries()
	data.LocalFilesLibraries = append([]LocalFileLibraryRow(nil), libs...)
	data.LocalFilesAvailable = len(data.LocalFilesLibraries) > 0
	return data
}

// idleCatalogData builds the page-load Catalog snapshot from the cfg
// adapters. Returns a zero-value CatalogData when either viewer is nil
// (test ergonomics) so idle renders cleanly without dependencies.
func idleCatalogData(cfg Config) CatalogData {
	if cfg.StreamsCatalogViewer == nil || cfg.PresetViewer == nil {
		return CatalogData{}
	}
	return buildCatalogData(
		cfg.StreamsCatalogViewer.Catalog(),
		cfg.PresetViewer.Presets(),
		"",
	)
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
	data.ModeLabel = fmt.Sprintf("Memory · drag to reorder · %d / 12", filled)
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

func normalizeSourceID(source string) string {
	id := strings.ToLower(strings.TrimSpace(source))
	switch id {
	case "streams", "plex", "jellyfin", "dlna":
		return id
	}
	return ""
}

func activeSourceID(source, adapterRef string) string {
	if id := normalizeSourceID(source); id != "" {
		return id
	}
	return parseAdapterRefSource(adapterRef)
}

// applySourceLampState populates Configured, Casting, and Issue on every
// lamp slot in base.Source.Buttons (Action == "") using the supplied viewers
// for Configured()/Status() and the canonical session Source for Casting.
// AdapterRef parsing remains a legacy fallback for sessions that predate
// Source. AUX (Action != "") is left untouched — applyAUXSourceState owns that
// state. Safe to call with nil viewers; lamps stay at zero (lamp dark).
func applySourceLampState(base *ReceiverPageData, viewers []adapters.SourceAvailabilityViewer, source, adapterRef string) {
	if base == nil {
		return
	}
	castingSource := activeSourceID(source, adapterRef)
	configured := map[string]bool{}
	issues := map[string]adapters.Status{}
	for _, v := range viewers {
		if v == nil {
			continue
		}
		id := normalizeSourceID(v.SourceID())
		if id == "" {
			continue
		}
		configured[id] = v.Configured()
		if statusViewer, ok := v.(interface{ Status() adapters.Status }); ok {
			st := statusViewer.Status()
			if st.State == adapters.StateError {
				issues[id] = st
			}
		}
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
		if st, ok := issues[id]; ok {
			b.Issue = true
			b.LastError = st.LastError
		} else {
			b.Issue = false
			b.LastError = ""
		}
	}
}

// formatUptime turns a duration into the "NH Nm" string used by the VFD.
// Zero or negative durations render as "0H 0m".
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "0H 0m"
	}
	total := int(d / time.Minute)
	hours := total / 60
	minutes := total % 60
	return fmt.Sprintf("%dH %dm", hours, minutes)
}

func defaultVisualizerMode(cfg Config) string {
	mode := config.NormalizeVisualizerMode(cfg.Bridge.Visualizer.Mode)
	if isSupportedVisualizerMode(mode) {
		return mode
	}
	return config.VisualizerModeRetroAnalyzer
}
