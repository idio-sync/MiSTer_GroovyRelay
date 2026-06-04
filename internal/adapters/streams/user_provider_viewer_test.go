package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

var _ adapters.UserProviderViewer = (*Adapter)(nil)

func newViewerAdapter(t *testing.T, fr *fakeResolver) *Adapter {
	t.Helper()
	dir := t.TempDir()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.resolver = fr
	a.cacheDir = dir
	return a
}

func TestUserProviderForm_RoundTripsAuthoringFields(t *testing.T) {
	t.Parallel()
	a := newViewerAdapter(t, &fakeResolver{})
	saved, err := a.userStore.Put(ProviderDefinition{
		Type: userProviderType, DisplayName: "F1 TV", BadgeLabel: "F1", BadgeColor: "amber",
		Groups: []GroupDefinition{{ID: "races", Name: "Races", Order: 7}},
		Channels: []ChannelDefinition{
			{
				Name:     "Archive",
				URL:      "https://www.youtube.com/playlist?list=PL1",
				Kind:     kindPlaylist,
				PlayMode: PlayFirstThenShuffle,
				GroupID:  "races",
				Order:    9,
			},
		},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	form, ok := a.UserProviderForm(saved.ID)
	if !ok {
		t.Fatalf("UserProviderForm(%q) not found", saved.ID)
	}
	if form.ID != saved.ID || form.DisplayName != "F1 TV" || form.BadgeLabel != "F1" || form.BadgeColor != "amber" {
		t.Fatalf("identity round-trip wrong: %+v", form)
	}
	if len(form.Groups) != 1 || form.Groups[0].ID != "races" || form.Groups[0].Name != "Races" || form.Groups[0].Order != 7 {
		t.Fatalf("groups round-trip wrong: %+v", form.Groups)
	}
	if len(form.Channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(form.Channels))
	}
	c := form.Channels[0]
	if c.URL != "https://www.youtube.com/playlist?list=PL1" || c.Kind != kindPlaylist || c.PlayMode != string(PlayFirstThenShuffle) || c.GroupID != "races" || c.Order != 9 || c.ID == "" {
		t.Fatalf("channel round-trip wrong: %+v", c)
	}
	if _, ok := a.UserProviderForm("mtv-rewind"); ok {
		t.Fatal("bundled ID unexpectedly returned a form")
	}
}

func TestUserProviderStatuses_ReportsPerChannelState(t *testing.T) {
	t.Parallel()
	a := newViewerAdapter(t, &fakeResolver{})
	a.mu.Lock()
	a.installSnapshotLocked(
		[]ProviderDefinition{
			{ID: "user:second", Type: userProviderType, DisplayName: "Second"},
			{ID: "mtv-rewind", Type: "youtube_channel_json", DisplayName: "Bundled"},
			{ID: "user:first", Type: userProviderType, DisplayName: "First"},
		},
		[]ProviderCatalog{
			{
				ProviderID: "mtv-rewind",
				Channels:   []Channel{{ID: "bundled", EnumState: enumStateError, Items: []StreamItem{{ID: "b"}}}},
			},
			{
				ProviderID: "user:first",
				Channels: []Channel{
					{ID: "zeta", EnumState: enumStateReady, Items: []StreamItem{{ID: "z1"}, {ID: "z2"}}},
				},
			},
			{
				ProviderID: "user:second",
				Channels: []Channel{
					{ID: "pending", EnumState: enumStatePending},
					{ID: "error", EnumState: enumStateError},
					{ID: "ready", EnumState: enumStateReady, Items: []StreamItem{{ID: "r1"}, {ID: "r2"}, {ID: "r3"}}},
				},
			},
		},
	)
	a.mu.Unlock()

	statuses := a.UserProviderStatuses()
	if len(statuses) != 2 {
		t.Fatalf("statuses len = %d, want 2: %+v", len(statuses), statuses)
	}
	if statuses[0].ProviderID != "user:second" || statuses[1].ProviderID != "user:first" {
		t.Fatalf("provider order/filter wrong: %+v", statuses)
	}
	wantSecond := []adapters.UserChannelStatus{
		{ChannelID: "pending", State: enumStatePending, ItemCount: 0},
		{ChannelID: "error", State: enumStateError, ItemCount: 0},
		{ChannelID: "ready", State: enumStateReady, ItemCount: 3},
	}
	if len(statuses[0].Channels) != len(wantSecond) {
		t.Fatalf("second channels len = %d, want %d: %+v", len(statuses[0].Channels), len(wantSecond), statuses[0].Channels)
	}
	for i, want := range wantSecond {
		if statuses[0].Channels[i] != want {
			t.Fatalf("second channel %d = %+v, want %+v", i, statuses[0].Channels[i], want)
		}
	}
	wantFirst := []adapters.UserChannelStatus{{ChannelID: "zeta", State: enumStateReady, ItemCount: 2}}
	if len(statuses[1].Channels) != len(wantFirst) {
		t.Fatalf("first channels len = %d, want %d: %+v", len(statuses[1].Channels), len(wantFirst), statuses[1].Channels)
	}
	for i, want := range wantFirst {
		if statuses[1].Channels[i] != want {
			t.Fatalf("first channel %d = %+v, want %+v", i, statuses[1].Channels[i], want)
		}
	}
}
