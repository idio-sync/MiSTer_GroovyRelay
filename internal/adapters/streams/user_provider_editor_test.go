package streams

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func newEditAdapter(t *testing.T, fr *fakeResolver) *Adapter {
	t.Helper()
	dir := t.TempDir()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.resolver = fr // simulate a started adapter (resolver present)
	a.cacheDir = dir
	return a
}

func TestRebuildUserCatalogsLive_EnumeratesAndInstalls(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	a := newEditAdapter(t, fr)
	if _, err := a.userStore.Put(ProviderDefinition{
		Type: userProviderType, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []ChannelDefinition{
			{Name: "List", URL: playlistURL, Kind: kindPlaylist},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := a.rebuildUserCatalogsLive(context.Background()); err != nil {
		t.Fatalf("rebuildUserCatalogsLive: %v", err)
	}
	a.mu.Lock()
	cat, ok := a.catalogs["user:mix"]
	a.mu.Unlock()
	if !ok {
		t.Fatal("user:mix catalog not installed")
	}
	if len(cat.Channels) != 1 || len(cat.Channels[0].Items) != 2 {
		t.Fatalf("playlist not enumerated: %+v", cat.Channels)
	}
	if fr.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1", fr.enumCalls)
	}
}
