package streams

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

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
		Groups: []GroupDefinition{{ID: "races", Name: "Races", Order: 0}},
		Channels: []ChannelDefinition{
			{Name: "Live", URL: "https://cdn.example.com/live.m3u8", Kind: kindDirect, GroupID: "races", Order: 0},
		},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	form, ok := a.UserProviderForm(saved.ID)
	if !ok {
		t.Fatalf("UserProviderForm(%q) not found", saved.ID)
	}
	if form.ID != saved.ID || form.DisplayName != "F1 TV" || form.BadgeColor != "amber" {
		t.Fatalf("identity round-trip wrong: %+v", form)
	}
	if len(form.Groups) != 1 || form.Groups[0].ID != "races" {
		t.Fatalf("groups round-trip wrong: %+v", form.Groups)
	}
	if len(form.Channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(form.Channels))
	}
	c := form.Channels[0]
	if c.URL != "https://cdn.example.com/live.m3u8" || c.Kind != kindDirect || c.GroupID != "races" || c.ID == "" {
		t.Fatalf("channel round-trip wrong: %+v", c)
	}
	if _, ok := a.UserProviderForm("mtv-rewind"); ok {
		t.Fatal("bundled ID unexpectedly returned a form")
	}
}

func TestUserProviderStatuses_ReportsPerChannelState(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{playlistURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk", "lmnopqrstuv")}}
	a := newViewerAdapter(t, fr)
	if _, err := a.userStore.Put(ProviderDefinition{
		Type: userProviderType, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []ChannelDefinition{
			{Name: "Live", URL: "https://cdn.example.com/s.m3u8", Kind: kindDirect},
			{Name: "List", URL: playlistURL, Kind: kindPlaylist},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snapshot, err := a.buildUserCatalogSnapshotLive(context.Background(), a.userStore.Snapshot())
	if err != nil {
		t.Fatalf("buildUserCatalogSnapshotLive: %v", err)
	}
	a.installUserCatalogSnapshot(snapshot)
	statuses := a.UserProviderStatuses()
	if len(statuses) != 1 || statuses[0].ProviderID != "user:mix" {
		t.Fatalf("statuses = %+v", statuses)
	}
	byID := map[string]adapters.UserChannelStatus{}
	for _, c := range statuses[0].Channels {
		byID[c.ChannelID] = c
	}
	if byID["live"].State != "ready" || byID["live"].ItemCount != 1 {
		t.Fatalf("live status = %+v", byID["live"])
	}
	if byID["list"].State != "ready" || byID["list"].ItemCount != 3 {
		t.Fatalf("list status = %+v", byID["list"])
	}
}
