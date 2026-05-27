package chassis

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeSourceViewer struct {
	id, configured string
}

func (f fakeSourceViewer) SourceID() string { return f.id }
func (f fakeSourceViewer) Configured() bool { return f.configured == "yes" }

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
	applySourceLampState(base, viewers, "streams:mtv-rewind:80s:abc:def")
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

func TestApplySourceLampState_EmptyRefClearsCasting(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "STREAMS", Action: "", Casting: true}, // stale from prior tick
	}}}
	applySourceLampState(base, []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
	}, "")
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
