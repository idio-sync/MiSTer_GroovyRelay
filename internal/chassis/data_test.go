package chassis

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeSourceViewer struct {
	id, configured string
	status         adapters.Status
}

func (f fakeSourceViewer) SourceID() string { return f.id }
func (f fakeSourceViewer) Configured() bool { return f.configured == "yes" }
func (f fakeSourceViewer) Status() adapters.Status {
	if f.status.State != 0 || f.status.LastError != "" {
		return f.status
	}
	return adapters.Status{State: adapters.StateRunning}
}

func TestParseAdapterRefSource_KnownPrefixes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ref, want string
	}{
		{"", ""},
		{"streams:mtv-rewind:80s:abc:def", "streams"},
		{"plex:server/key/123", "plex"},
		{"jellyfin:item/abc", "jellyfin"},
		{"dlna:urn:xyz", "dlna"},
		{"weird-no-prefix", ""},
		{"unknown:source:x", ""},
	}
	for _, c := range cases {
		got := parseAdapterRefSource(c.ref)
		if got != c.want {
			t.Errorf("parseAdapterRefSource(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestApplySourceLampState_LampSlotsDerivedFromViewersAndRef(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "STREAMS", Action: ""},
		{Label: "PLEX", Action: ""},
		{Label: "JELLYFIN", Action: ""},
		{Label: "DLNA", Action: ""},
		{Label: "AUX", Action: SourceActionAUXStart}, // AUX must NOT get Configured/Casting touched
	}}}
	viewers := []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
		fakeSourceViewer{id: "plex", configured: "no"},
		fakeSourceViewer{id: "jellyfin", configured: "yes"},
		fakeSourceViewer{id: "dlna", configured: "no"},
	}
	applySourceLampState(base, viewers, "", "streams:mtv-rewind:80s:abc:def")
	want := []struct {
		label               string
		configured, casting bool
	}{
		{"STREAMS", true, true},
		{"PLEX", false, false},
		{"JELLYFIN", true, false},
		{"DLNA", false, false},
		{"AUX", false, false}, // AUX path is the existing applyAUXSourceState — these stay zero here
	}
	for i, w := range want {
		got := base.Source.Buttons[i]
		if got.Label != w.label {
			t.Errorf("button[%d].Label = %q, want %q", i, got.Label, w.label)
		}
		if got.Configured != w.configured {
			t.Errorf("button[%d=%s].Configured = %v, want %v", i, w.label, got.Configured, w.configured)
		}
		if got.Casting != w.casting {
			t.Errorf("button[%d=%s].Casting = %v, want %v", i, w.label, got.Casting, w.casting)
		}
	}
}

func TestSnapshotFromStatusView_SourceMarksOpaqueAdapterRefsCasting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		source     string
		adapterRef string
		wantLabel  string
	}{
		{
			name:       "plex video media key",
			source:     "plex",
			adapterRef: "/library/metadata/42",
			wantLabel:  "PLEX",
		},
		{
			name:       "jellyfin item play session key",
			source:     "jellyfin",
			adapterRef: "item-1:play-session-1",
			wantLabel:  "JELLYFIN",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := nonZeroConfig()
			cfg.SourceAvailabilityViewers = []adapters.SourceAvailabilityViewer{
				fakeSourceViewer{id: "streams", configured: "yes"},
				fakeSourceViewer{id: "plex", configured: "yes"},
				fakeSourceViewer{id: "jellyfin", configured: "yes"},
				fakeSourceViewer{id: "dlna", configured: "yes"},
			}
			snap := snapshotFromStatusView(cfg, core.StatusHomeView{
				State:      core.StatePlaying,
				Source:     tc.source,
				AdapterRef: tc.adapterRef,
			}, nil, nil, nil, nil, time.Now())

			for _, button := range snap.Source.Buttons {
				if button.Label != tc.wantLabel {
					continue
				}
				if !button.Configured || !button.Casting {
					t.Fatalf("%s button = %+v, want configured and casting", tc.wantLabel, button)
				}
				return
			}
			t.Fatalf("%s button missing", tc.wantLabel)
		})
	}
}

func TestApplySourceLampState_DerivesIssueFromAdapterStatus(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "DLNA", Action: ""},
	}}}
	applySourceLampState(base, []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{
			id:         "dlna",
			configured: "yes",
			status: adapters.Status{
				State:     adapters.StateError,
				LastError: "DLNA requires a reachable bridge.host_ip",
			},
		},
	}, "", "")
	got := base.Source.Buttons[0]
	if !got.Configured {
		t.Fatalf("Configured = false, want true so issue is distinguishable from disabled")
	}
	if !got.Issue {
		t.Fatalf("Issue = false, want true for adapters.StateError")
	}
}

func TestApplySourceLampState_EmptyRefClearsCasting(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "STREAMS", Action: "", Casting: true}, // stale from prior tick
	}}}
	applySourceLampState(base, []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
	}, "", "")
	if base.Source.Buttons[0].Casting {
		t.Errorf("Casting = true, want false (empty ref must clear)")
	}
}

func TestBuildCatalogData_ChannelsCarryStarredFromPresets(t *testing.T) {
	t.Parallel()
	cat := []adapters.CatalogProvider{
		{
			ID: "mtv-rewind", DisplayName: "MTV Rewind",
			BadgeLabel: "MTV", BadgeClass: "mtv", Live: false,
			DefaultChannel: "1stday",
			Groups: []adapters.CatalogGroup{
				{ID: "shows", Name: "MTV Shows", Channels: []adapters.CatalogChannel{
					{ID: "1stday", Name: "First Day on MTV", PlayMode: "SEQ", Live: false},
					{ID: "amp", Name: "AMP", PlayMode: "SHUFFLE", Live: false},
				}},
			},
		},
	}
	presets := [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday"},
		{Slot: 2}, {Slot: 3}, {Slot: 4}, {Slot: 5}, {Slot: 6},
		{Slot: 7}, {Slot: 8}, {Slot: 9}, {Slot: 10}, {Slot: 11}, {Slot: 12},
	}
	data := buildCatalogData(cat, presets, "streams:mtv-rewind:1stday:abc:def")
	if data.ActiveProviderID != "mtv-rewind" {
		t.Errorf("ActiveProviderID = %q, want mtv-rewind", data.ActiveProviderID)
	}
	if data.TotalChannels != 2 {
		t.Errorf("TotalChannels = %d, want 2", data.TotalChannels)
	}
	first := data.Providers[0].Groups[0].Channels[0]
	if !first.Starred || first.PresetSlot != 1 {
		t.Errorf("first channel Starred/PresetSlot = (%v, %d), want (true, 1)", first.Starred, first.PresetSlot)
	}
	if !first.Tuned {
		t.Errorf("first channel Tuned = false, want true")
	}
	second := data.Providers[0].Groups[0].Channels[1]
	if second.Starred || second.PresetSlot != 0 {
		t.Errorf("second channel Starred/PresetSlot = (%v, %d), want (false, 0)", second.Starred, second.PresetSlot)
	}
	if second.Tuned {
		t.Errorf("second channel Tuned = true, want false")
	}
}

func TestCatalogData_ProviderIndexFallback(t *testing.T) {
	t.Parallel()
	data := CatalogData{
		Providers:        []CatalogProviderTab{{ID: "a"}, {ID: "b"}},
		ActiveProviderID: "missing",
	}
	// ProviderIndex with a missing ID must return 0 (defense-in-depth).
	if got := data.ProviderIndex("missing"); got != 0 {
		t.Errorf("ProviderIndex(missing) = %d, want 0", got)
	}
	if got := data.ProviderIndex("b"); got != 1 {
		t.Errorf("ProviderIndex(b) = %d, want 1", got)
	}
}

func TestCatalogData_GroupIndexFallback(t *testing.T) {
	t.Parallel()
	data := CatalogData{
		Providers: []CatalogProviderTab{
			{ID: "a", Groups: []CatalogGroupTab{{ID: "g1"}, {ID: "g2"}}},
		},
	}
	if got := data.GroupIndex("a", "g2"); got != 1 {
		t.Errorf("GroupIndex(a,g2) = %d, want 1", got)
	}
	if got := data.GroupIndex("a", "missing"); got != 0 {
		t.Errorf("GroupIndex(a,missing) = %d, want 0", got)
	}
	if got := data.GroupIndex("missing", "g1"); got != 0 {
		t.Errorf("GroupIndex(missing,g1) = %d, want 0", got)
	}
}

func TestSnapshotFromStatusView_PopulatesCatalogAndLamps(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	// fake viewers/casters
	cfg.StreamsCatalogViewer = fakeCatalogViewer{
		providers: []adapters.CatalogProvider{
			{ID: "mtv-rewind", DisplayName: "MTV Rewind",
				BadgeLabel: "MTV", BadgeClass: "mtv",
				Groups: []adapters.CatalogGroup{
					{ID: "shows", Name: "Shows", Channels: []adapters.CatalogChannel{
						{ID: "1stday", Name: "First Day", PlayMode: "SEQ"},
					}},
				}},
		},
	}
	cfg.PresetViewer = fakePresetViewer{entries: [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday"},
	}}
	cfg.SourceAvailabilityViewers = []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
	}
	view := core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "streams:mtv-rewind:1stday:abc:def",
		Generation: 5,
	}
	snap := snapshotFromStatusView(cfg, view, nil, nil, nil, nil, time.Now())
	// Source-cluster STREAMS lamp shows Casting=true.
	var streams *SourceButton
	for i := range snap.Source.Buttons {
		if snap.Source.Buttons[i].Label == "STREAMS" {
			streams = &snap.Source.Buttons[i]
		}
	}
	if streams == nil {
		t.Fatal("STREAMS button missing")
	}
	if !streams.Configured || !streams.Casting {
		t.Errorf("STREAMS button = %+v, want Configured=true Casting=true", streams)
	}
	// Catalog populated and the channel is Tuned.
	if len(snap.Catalog.Providers) != 1 {
		t.Fatalf("Catalog.Providers len = %d, want 1", len(snap.Catalog.Providers))
	}
	channel := snap.Catalog.Providers[0].Groups[0].Channels[0]
	if !channel.Tuned {
		t.Errorf("catalog channel Tuned = false, want true")
	}
	if !channel.Starred || channel.PresetSlot != 1 {
		t.Errorf("catalog channel star = (%v, %d), want (true, 1)", channel.Starred, channel.PresetSlot)
	}
}

type fakeCatalogViewer struct {
	providers []adapters.CatalogProvider
}

func (f fakeCatalogViewer) Catalog() []adapters.CatalogProvider { return f.providers }

func TestBuildSettingsData_AdapterCountExcludesAUX(t *testing.T) {
	t.Parallel()
	reg := adapters.NewRegistry()
	mustRegister(t, reg, fakeNamedAdapter{name: "plex"})
	mustRegister(t, reg, fakeNamedAdapter{name: "aux"})
	mustRegister(t, reg, fakeNamedAdapter{name: "dlna"})
	bridge := config.BridgeConfig{DataDir: "/var/lib/relay"}
	got := buildSettingsData(bridge, reg, nil, nil)
	if got.AdapterCount != 2 {
		t.Errorf("AdapterCount = %d, want 2 (aux excluded)", got.AdapterCount)
	}
	if got.Bridge.DataDir != "/var/lib/relay" {
		t.Errorf("Bridge.DataDir = %q, want /var/lib/relay", got.Bridge.DataDir)
	}
	if got.Errors == nil {
		t.Errorf("Errors map is nil; want empty initialized map")
	}
}

func TestBuildSettingsData_NilCatalogYieldsZeroCount(t *testing.T) {
	t.Parallel()
	got := buildSettingsData(config.BridgeConfig{}, adapters.NewRegistry(), nil, nil)
	if got.CatalogProviderCount != 0 {
		t.Errorf("CatalogProviderCount = %d, want 0", got.CatalogProviderCount)
	}
}

func TestBuildSettingsData_CatalogPaneProviderCountAndChannels(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "mtv-rewind", ChannelCount: 73, Live: false},
			{ID: "cartoon-rewind", ChannelCount: 13, Live: false},
			{ID: "toonami-aftermath", ChannelCount: 4, Live: true, HLSBufferDisabled: false},
		},
	}
	catalog := fakeCatalogViewer{providers: []adapters.CatalogProvider{
		{ID: "browse-only"},
	}}
	got := buildSettingsData(config.BridgeConfig{}, nil, catalog, mgr)
	if got.CatalogProviderCount != 1 {
		t.Errorf("CatalogProviderCount = %d; want 1 (existing tab badge from StreamsCatalogViewer)", got.CatalogProviderCount)
	}
	if got.CatalogPaneProviderCount != 3 {
		t.Errorf("CatalogPaneProviderCount = %d; want 3", got.CatalogPaneProviderCount)
	}
	if got.CatalogChannelCount != 90 {
		t.Errorf("CatalogChannelCount = %d; want 90", got.CatalogChannelCount)
	}
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false (one Live, HLSBufferDisabled=false)")
	}
}

func TestBuildSettingsData_DirectStreamHLSAllLiveDisabled(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "toonami-aftermath", Live: true, HLSBufferDisabled: true},
		},
	}
	got := buildSettingsData(config.BridgeConfig{}, nil, nil, mgr)
	if !got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = false; want true (all Live disabled)")
	}
}

func TestBuildSettingsData_DirectStreamHLSMixedStateRendersOff(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "toonami-aftermath", Live: true, HLSBufferDisabled: true},
			{ID: "live2", Live: true, HLSBufferDisabled: false},
		},
	}
	got := buildSettingsData(config.BridgeConfig{}, nil, nil, mgr)
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false (mixed renders as off)")
	}
}

func TestBuildSettingsData_NoLiveProvidersDirectStreamHLSFalse(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "mtv-rewind", Live: false, HLSBufferDisabled: true},
		},
	}
	got := buildSettingsData(config.BridgeConfig{}, nil, nil, mgr)
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false (no Live)")
	}
}

func TestBuildSettingsData_NilCatalogManagerEmpty(t *testing.T) {
	catalog := fakeCatalogViewer{providers: []adapters.CatalogProvider{
		{ID: "mtv-rewind"},
		{ID: "cartoon-rewind"},
	}}
	got := buildSettingsData(config.BridgeConfig{}, nil, catalog, nil)
	if got.CatalogProviderCount != 2 {
		t.Errorf("CatalogProviderCount = %d; want 2 (tab badge fallback)", got.CatalogProviderCount)
	}
	if got.CatalogPaneProviderCount != 0 {
		t.Errorf("CatalogPaneProviderCount = %d; want 0", got.CatalogPaneProviderCount)
	}
	if got.CatalogProviders != nil {
		t.Errorf("CatalogProviders = %v; want nil", got.CatalogProviders)
	}
	if got.CatalogChannelCount != 0 {
		t.Errorf("CatalogChannelCount = %d; want 0", got.CatalogChannelCount)
	}
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false")
	}
}

// fakeCatalogManager is a CatalogSettingsManager test double. Mutation
// methods record their args; Providers returns the configured slice.
type fakeCatalogManager struct {
	providers []CatalogProviderState
}

func (f *fakeCatalogManager) Providers() []CatalogProviderState { return f.providers }
func (f *fakeCatalogManager) UpdateProvider(id string, patch CatalogProviderPatch) (adapters.ApplyScope, error) {
	return 0, nil
}
func (f *fakeCatalogManager) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
	return 0, nil
}

// fakeNamedAdapter satisfies adapters.Adapter with the minimum surface needed
// for registry.List() walks.
type fakeNamedAdapter struct{ name string }

func (f fakeNamedAdapter) Name() string                                     { return f.name }
func (f fakeNamedAdapter) DisplayName() string                              { return f.name }
func (f fakeNamedAdapter) Fields() []adapters.FieldDef                      { return nil }
func (f fakeNamedAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f fakeNamedAdapter) IsEnabled() bool                                  { return true }
func (f fakeNamedAdapter) Start(ctx context.Context) error                  { return nil }
func (f fakeNamedAdapter) Stop() error                                      { return nil }
func (f fakeNamedAdapter) Status() adapters.Status                          { return adapters.Status{} }
func (f fakeNamedAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func mustRegister(t *testing.T, reg *adapters.Registry, a adapters.Adapter) {
	t.Helper()
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register(%s): %v", a.Name(), err)
	}
}

func TestSnapshot_SettingsReadsFromBridgeSaverCurrentWhenWired(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{cur: config.BridgeConfig{DataDir: "/from-saver"}}
	cfg := Config{
		BridgeSaver: saver,
		Registry:    adapters.NewRegistry(),
		Bridge:      config.BridgeConfig{DataDir: "/from-startup"},
	}
	snap := idleSnapshot(cfg, time.Now())
	if snap.Settings.Bridge.DataDir != "/from-saver" {
		t.Errorf("Bridge.DataDir = %q, want /from-saver (saver wins over startup)",
			snap.Settings.Bridge.DataDir)
	}
}

func TestSnapshot_SettingsFallsBackToStartupConfigWhenSaverNil(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Registry: adapters.NewRegistry(),
		Bridge:   config.BridgeConfig{DataDir: "/from-startup"},
	}
	snap := idleSnapshot(cfg, time.Now())
	if snap.Settings.Bridge.DataDir != "/from-startup" {
		t.Errorf("Bridge.DataDir = %q, want /from-startup", snap.Settings.Bridge.DataDir)
	}
}

func TestSnapshot_SettingsLivePathCarriesBridgeSaverData(t *testing.T) {
	t.Parallel()
	cfg := Config{
		BridgeSaver: fakeBridgeSettingsSaver{cur: config.BridgeConfig{DataDir: "/from-saver-live"}},
		Registry:    adapters.NewRegistry(),
		Bridge:      config.BridgeConfig{DataDir: "/from-startup"},
	}
	snap := snapshotFromStatusView(
		cfg,
		core.StatusHomeView{State: core.StatePlaying},
		nil, nil, nil, nil,
		time.Now(),
	)
	if snap.Settings.Bridge.DataDir != "/from-saver-live" {
		t.Errorf("Bridge.DataDir = %q, want /from-saver-live", snap.Settings.Bridge.DataDir)
	}
}

func TestSettingsData_PopulatesAdapters(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"dlna":    {"enabled": true, "device_name": "M"},
			"torrent": {"enabled": false, "traffic_acknowledged": false},
			"streams": {"enabled": true, "manifest_url": "https://x/y.json"},
		},
		fields: map[string][]adapters.FieldDef{
			"dlna": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "device_name", Kind: adapters.KindText, Label: "Device name", ApplyScope: adapters.ScopeRestartBridge},
			},
			"torrent": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "traffic_acknowledged", Kind: adapters.KindBool, Label: "BT traffic acknowledged", ApplyScope: adapters.ScopeHotSwap},
			},
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "manifest_url", Kind: adapters.KindText, Label: "Manifest URL", ApplyScope: adapters.ScopeHotSwap},
			},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	data := s.buildSettingsData()
	if len(data.Adapters) != 3 {
		t.Fatalf("len(Adapters) = %d, want 3", len(data.Adapters))
	}
	byName := map[string]AdapterPaneData{}
	for _, a := range data.Adapters {
		byName[a.Name] = a
	}
	if dlna, ok := byName["dlna"]; !ok || len(dlna.Fields) != 2 {
		t.Errorf("dlna pane not populated: %+v", byName)
	}
}

func TestSettingsData_StreamsProvidersFromCatalog(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"streams": {
				"enabled":      true,
				"manifest_url": "https://x/y.json",
			},
		},
		fields: map[string][]adapters.FieldDef{
			"streams": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	cat := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "youtube", DisplayName: "YouTube", CatalogRefreshHours: 12},
			{ID: "radio", DisplayName: "Radio", CatalogRefreshHours: 0},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
		CatalogManager:       cat,
	}}
	data := s.buildSettingsData()
	var streams *AdapterPaneData
	for i, a := range data.Adapters {
		if a.Name == "streams" {
			streams = &data.Adapters[i]
			break
		}
	}
	if streams == nil {
		t.Fatalf("streams pane missing")
	}
	if len(streams.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(streams.Providers))
	}
	byID := map[string]AdapterProviderRow{}
	for _, p := range streams.Providers {
		byID[p.ID] = p
	}
	if got := byID["youtube"]; got.DisplayName != "YouTube" || got.CatalogRefreshHours != 12 {
		t.Errorf("youtube row = %+v", got)
	}
	if got := byID["radio"]; got.CatalogRefreshHours != 0 {
		t.Errorf("radio row CatalogRefreshHours = %d, want 0 (no override)", got.CatalogRefreshHours)
	}
}

func TestSettingsData_LocalFilesPaneHasLibraryEditorAndBrowseDrawer(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"localfiles": {"enabled": true},
		},
		fields: map[string][]adapters.FieldDef{
			"localfiles": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	lf := &fakeLocalFilesService{libraries: []LocalFileLibraryRow{{Name: "Movies", Root: "/media/movies"}}}
	s := &Server{cfg: Config{
		Version:                 "test",
		StartedAt:               time.Unix(0, 0),
		AdapterSettingsSaver:    saver,
		LocalFiles:              lf,
		LocalFilesLibraryEditor: lf,
	}}
	data := s.buildSettingsData()
	var pane *AdapterPaneData
	for i := range data.Adapters {
		if data.Adapters[i].Name == "localfiles" {
			pane = &data.Adapters[i]
			break
		}
	}
	if pane == nil {
		t.Fatal("localfiles pane missing")
	}
	if !pane.HasLibraryEditor || !pane.HasBrowseDrawer {
		t.Fatalf("pane flags = library:%v browse:%v, want both true", pane.HasLibraryEditor, pane.HasBrowseDrawer)
	}
	if len(pane.Libraries) != 1 || pane.Libraries[0].Name != "Movies" {
		t.Fatalf("Libraries = %+v", pane.Libraries)
	}
}

func TestSettingsData_LocalFilesPaneDoesNotRenderBrowseDrawerWithoutService(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"localfiles": {"enabled": true},
		},
		fields: map[string][]adapters.FieldDef{
			"localfiles": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	lf := &fakeLocalFilesService{libraries: []LocalFileLibraryRow{{Name: "Movies", Root: "/media/movies"}}}
	s := &Server{cfg: Config{
		Version:                 "test",
		StartedAt:               time.Unix(0, 0),
		AdapterSettingsSaver:    saver,
		LocalFilesLibraryEditor: lf,
	}}
	data := s.buildSettingsData()
	var pane *AdapterPaneData
	for i := range data.Adapters {
		if data.Adapters[i].Name == "localfiles" {
			pane = &data.Adapters[i]
			break
		}
	}
	if pane == nil {
		t.Fatal("localfiles pane missing")
	}
	if !pane.HasLibraryEditor {
		t.Fatal("HasLibraryEditor = false, want true")
	}
	if pane.HasBrowseDrawer {
		t.Fatal("HasBrowseDrawer = true without LocalFiles service; want false")
	}
}

func TestSettingsData_DLNAHint_Listening(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}}
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "dlna" {
			if a.Hint != "PUSH · LISTENING" {
				t.Errorf("dlna hint = %q, want 'PUSH · LISTENING'", a.Hint)
			}
		}
	}
}

func TestSettingsData_DLNAHint_Disabled(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}}
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "dlna" && a.Hint != "PUSH · DISABLED" {
			t.Errorf("dlna hint = %q, want 'PUSH · DISABLED'", a.Hint)
		}
	}
}

// TestSettingsData_CastHint_EnabledState pins that plex and jellyfin hints
// reflect the live enabled state (CAST · LISTENING/DISABLED), mirroring the
// DLNA PUSH · LISTENING/DISABLED convention rather than a static auth-mechanism
// label.
func TestSettingsData_CastHint_EnabledState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		enabled bool
		want    string
	}{
		{"plex", true, "CAST · LISTENING"},
		{"plex", false, "CAST · DISABLED"},
		{"jellyfin", true, "CAST · LISTENING"},
		{"jellyfin", false, "CAST · DISABLED"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%s_%t", tc.name, tc.enabled), func(t *testing.T) {
			t.Parallel()
			saver := &fakeAdapterSettingsSaver{
				current: map[string]map[string]any{tc.name: {"enabled": tc.enabled}},
				fields: map[string][]adapters.FieldDef{
					tc.name: {{Key: "enabled", Kind: adapters.KindBool}},
				},
			}
			s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}}
			data := s.buildSettingsData()
			found := false
			for _, a := range data.Adapters {
				if a.Name == tc.name {
					found = true
					if a.Hint != tc.want {
						t.Errorf("%s hint = %q, want %q", tc.name, a.Hint, tc.want)
					}
				}
			}
			if !found {
				t.Fatalf("%s pane missing from data.Adapters", tc.name)
			}
		})
	}
}

func TestSettingsData_TorrentHint_Static(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"torrent": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"torrent": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}}
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "torrent" && a.Hint != "PASTE-IN · BT" {
			t.Errorf("torrent hint = %q, want 'PASTE-IN · BT'", a.Hint)
		}
	}
}

func TestSettingsData_StreamsHint_ChannelCount(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"streams": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}}
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "streams" {
			if !strings.HasPrefix(a.Hint, "PULL") || !strings.Contains(a.Hint, "CHANNELS") {
				t.Errorf("streams hint = %q, want PULL · N CHANNELS prefix", a.Hint)
			}
		}
	}
}

// TestSettingsDataFromConfig_PopulatesAdaptersInProductionPath pins the fix
// for the Tasks 17-19 bug: the production drawer-render path runs through the
// PACKAGE-level settingsDataFromConfig (via idleSnapshot / snapshotFromStatusView),
// never the *Server method. This calls the package function directly and
// asserts adapters are populated, so a regression that moves the loop back onto
// the method (leaving production empty) fails here.
func TestSettingsDataFromConfig_PopulatesAdaptersInProductionPath(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	cfg := Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterSettingsSaver: saver}
	data := settingsDataFromConfig(cfg)
	if len(data.Adapters) < 1 {
		t.Fatalf("len(Adapters) = %d, want >= 1 (production path must populate adapter panes)", len(data.Adapters))
	}
}

func TestSettingsData_LinkablePanes(t *testing.T) {
	linker := &fakeAdapterLinker{views: map[string]LinkView{
		"plex":     {Kind: "pin", Phase: "unlinked"},
		"jellyfin": {Kind: "credential", Phase: "linked", LinkedAs: "jake on s"},
	}}
	saver := &fakeAdapterSettingsSaver{
		fields: map[string][]adapters.FieldDef{
			"plex":     {{Key: "enabled", Kind: adapters.KindBool}},
			"jellyfin": {{Key: "enabled", Kind: adapters.KindBool}},
		},
		current: map[string]map[string]any{
			"plex": {"enabled": true}, "jellyfin": {"enabled": false},
		},
	}
	data := settingsDataFromConfig(Config{AdapterSettingsSaver: saver, AdapterLinker: linker})

	byName := map[string]AdapterPaneData{}
	for _, p := range data.Adapters {
		byName[p.Name] = p
	}
	if !byName["plex"].Linkable || byName["plex"].LinkView.Kind != "pin" {
		t.Errorf("plex pane = %+v, want Linkable pin", byName["plex"])
	}
	if !byName["jellyfin"].Linkable || byName["jellyfin"].LinkView.LinkedAs != "jake on s" {
		t.Errorf("jellyfin pane = %+v, want Linkable + LinkedAs", byName["jellyfin"])
	}
}

func TestSettingsData_NilLinkerNotLinkable(t *testing.T) {
	saver := &fakeAdapterSettingsSaver{
		fields:  map[string][]adapters.FieldDef{"plex": {{Key: "enabled", Kind: adapters.KindBool}}},
		current: map[string]map[string]any{"plex": {"enabled": true}},
	}
	data := settingsDataFromConfig(Config{AdapterSettingsSaver: saver}) // no AdapterLinker
	for _, p := range data.Adapters {
		if p.Name == "plex" && p.Linkable {
			t.Errorf("plex Linkable=true with nil AdapterLinker; want false")
		}
	}
}

func TestIdleSnapshot_SeedsAudioStripFromConfig(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.DSP = config.DefaultAudioDSP()
	cfg.Bridge.Audio.DSP.Bass = 4
	cfg.Bridge.Audio.OutputVolume = 55
	snap := idleSnapshot(cfg, time.Unix(0, 0))
	if snap.AudioStrip.Bass != 4 {
		t.Errorf("idle AudioStrip.Bass = %v, want 4", snap.AudioStrip.Bass)
	}
	if len(snap.AudioStrip.EQ) != 10 {
		t.Errorf("idle AudioStrip.EQ length = %d, want 10", len(snap.AudioStrip.EQ))
	}
	if snap.AudioStrip.OutputVolume != 55 {
		t.Errorf("AudioStrip.OutputVolume = %d, want 55", snap.AudioStrip.OutputVolume)
	}
}

func TestSettingsData_URLPanePopulatesWidgets(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		fields: map[string][]adapters.FieldDef{
			"url": {{Key: "enabled", Kind: adapters.KindBool}},
		},
		current: map[string]map[string]any{
			"url": {"enabled": true},
		},
	}
	he := &fakeHostEditor{hosts: []string{"youtube.com"}, hostsOK: true}
	cs := &fakeCookieStore{status: CookieStatusView{Loaded: true, Bytes: 64, SetAt: "2026-05-29 00:00:00Z"}, statusOK: true}
	cfg := Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
		AdapterHostEditor:    he,
		AdapterCookieStore:   cs,
	}
	data := settingsDataFromConfig(cfg)
	var urlPane *AdapterPaneData
	for i := range data.Adapters {
		if data.Adapters[i].Name == "url" {
			urlPane = &data.Adapters[i]
		}
	}
	if urlPane == nil {
		t.Fatal("url pane not present in SettingsData.Adapters")
	}
	if !urlPane.HasHostEditor || len(urlPane.Hosts) != 1 || urlPane.Hosts[0] != "youtube.com" {
		t.Errorf("host editor data = %+v", urlPane)
	}
	if !urlPane.HasCookieStore || urlPane.Cookie == nil || !urlPane.Cookie.Loaded || urlPane.Cookie.Bytes != 64 {
		t.Errorf("cookie data = %+v", urlPane.Cookie)
	}
	if urlPane.Hint != "PASTE-IN" {
		t.Errorf("Hint = %q, want PASTE-IN", urlPane.Hint)
	}
}
