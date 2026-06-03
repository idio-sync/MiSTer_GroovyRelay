package streams

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func newEditAdapter(t *testing.T, fr *fakeResolver) *Adapter {
	t.Helper()
	dir := t.TempDir()
	// Pre-create an empty preset file so the store starts with all 12 slots
	// vacant (bundled defaults reference built-in channels not present in the
	// test catalog, which would otherwise fill the bank via the stale-entry
	// fallback path and prevent test stars from being inserted).
	presetsPath := filepath.Join(dir, "chassis_presets.json")
	if err := os.WriteFile(presetsPath, []byte(`{"version":1,"slots":[]}`), 0o600); err != nil {
		t.Fatalf("pre-create empty presets file: %v", err)
	}
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

func sampleForm() adapters.UserProviderForm {
	return adapters.UserProviderForm{
		DisplayName: "F1 TV",
		BadgeLabel:  "F1",
		BadgeColor:  "amber",
		Channels: []adapters.UserChannelForm{
			{Name: "Live", URL: "https://cdn.example.com/live.m3u8"}, // kind auto-detect → direct
		},
	}
}

func TestCreateUserProvider_PersistsRebuildsAndFlagsAutoEnable(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	res, err := a.CreateUserProvider(context.Background(), sampleForm())
	if err != nil {
		t.Fatalf("CreateUserProvider: %v", err)
	}
	if res.Provider.ID != "user:f1-tv" {
		t.Fatalf("provider ID = %q, want user:f1-tv", res.Provider.ID)
	}
	if res.Provider.BadgeLabel != "F1" || res.Provider.BadgeClass != "u-amber" {
		t.Fatalf("badge = (%q,%q), want (F1,u-amber)", res.Provider.BadgeLabel, res.Provider.BadgeClass)
	}
	if !res.AutoEnableNeeded {
		t.Fatal("AutoEnableNeeded = false, want true (first provider while disabled)")
	}
	if got := a.userStore.Snapshot(); len(got) != 1 || got[0].ID != "user:f1-tv" {
		t.Fatalf("store snapshot = %+v", got)
	}
	a.mu.Lock()
	_, ok := a.catalogs["user:f1-tv"]
	a.mu.Unlock()
	if !ok {
		t.Fatal("user:f1-tv catalog not installed after create")
	}
}

func TestCreateUserProvider_SecondProviderNoAutoEnable(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	if _, err := a.CreateUserProvider(context.Background(), sampleForm()); err != nil {
		t.Fatalf("first create: %v", err)
	}
	f2 := sampleForm()
	f2.DisplayName = "Cartoon"
	res, err := a.CreateUserProvider(context.Background(), f2)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if res.AutoEnableNeeded {
		t.Fatal("AutoEnableNeeded = true on second provider, want false")
	}
}

func TestCreateUserProvider_ConcurrentCreatesOnlyOneAutoEnable(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	forms := []adapters.UserProviderForm{sampleForm(), sampleForm()}
	forms[1].DisplayName = "Cartoon"
	forms[1].BadgeLabel = "CN"
	results := make(chan adapters.UserProviderResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, form := range forms {
		form := form
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := a.CreateUserProvider(context.Background(), form)
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("CreateUserProvider error: %v", err)
	}
	autoEnabled := 0
	for res := range results {
		if res.AutoEnableNeeded {
			autoEnabled++
		}
	}
	if autoEnabled != 1 {
		t.Fatalf("AutoEnableNeeded count = %d, want 1", autoEnabled)
	}
	if got := a.userStore.Snapshot(); len(got) != 2 {
		t.Fatalf("store snapshot len = %d, want 2: %+v", len(got), got)
	}
}

func TestCreateUserProvider_InvalidBadgeColorIsClientError(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	f := sampleForm()
	f.BadgeColor = "chartreuse" // not in the palette
	_, err := a.CreateUserProvider(context.Background(), f)
	if err == nil {
		t.Fatal("err = nil, want a client validation error")
	}
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) || qerr.Status != http.StatusBadRequest {
		t.Fatalf("err = %v, want *QuickCastError{400}", err)
	}
}

func TestUpdateUserProvider_RemovedChannelClearsPresetSlots(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	// Create a provider with two direct channels.
	form := adapters.UserProviderForm{
		DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{ID: "", Name: "Keep", URL: "https://cdn.example.com/keep.m3u8"},
			{ID: "", Name: "Drop", URL: "https://cdn.example.com/drop.m3u8"},
		},
	}
	created, err := a.CreateUserProvider(context.Background(), form)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Star both channels into the preset bank.
	for _, g := range created.Provider.Groups {
		for _, ch := range g.Channels {
			if _, err := a.SetPresetStarred(context.Background(), created.Provider.ID, ch.ID, true); err != nil {
				t.Fatalf("star %s: %v", ch.ID, err)
			}
		}
	}
	// Update: drop the "drop" channel (re-send only "keep", with its locked ID).
	keepID, dropID := channelIDsByName(t, created.Provider)
	upd := adapters.UserProviderForm{
		ID: created.Provider.ID, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{ID: keepID, Name: "Keep", URL: "https://cdn.example.com/keep.m3u8"},
		},
	}
	res, err := a.UpdateUserProvider(context.Background(), created.Provider.ID, upd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(res.ClearedSlots) != 1 {
		t.Fatalf("ClearedSlots = %v, want 1 (the dropped channel)", res.ClearedSlots)
	}
	// The dropped channel's slot is gone; the kept channel's slot remains.
	for _, e := range a.presetStore.Snapshot() {
		if e.ChannelID == dropID {
			t.Fatal("dropped channel still starred")
		}
	}
}

func TestUpdateUserProvider_UnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	_, err := a.UpdateUserProvider(context.Background(), "user:ghost", sampleForm())
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) || qerr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want *QuickCastError{404}", err)
	}
}

func TestDeleteUserProvider_ClearsAllItsPresetSlots(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	created, err := a.CreateUserProvider(context.Background(), adapters.UserProviderForm{
		DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{Name: "A", URL: "https://cdn.example.com/a.m3u8"},
			{Name: "B", URL: "https://cdn.example.com/b.m3u8"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, g := range created.Provider.Groups {
		for _, ch := range g.Channels {
			if _, err := a.SetPresetStarred(context.Background(), created.Provider.ID, ch.ID, true); err != nil {
				t.Fatalf("star: %v", err)
			}
		}
	}
	res, err := a.DeleteUserProvider(context.Background(), created.Provider.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(res.ClearedSlots) != 2 {
		t.Fatalf("ClearedSlots = %v, want 2", res.ClearedSlots)
	}
	if got := a.userStore.Snapshot(); len(got) != 0 {
		t.Fatalf("store snapshot = %+v, want empty", got)
	}
	a.mu.Lock()
	_, ok := a.catalogs[created.Provider.ID]
	a.mu.Unlock()
	if ok {
		t.Fatal("catalog still present after delete")
	}
}

func TestDeleteUserProvider_UnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	_, err := a.DeleteUserProvider(context.Background(), "user:ghost")
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) || qerr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want *QuickCastError{404}", err)
	}
}

func TestReorderUserProvider_PersistsOrderWithoutEnumerating(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("dQw4w9WgXcQ"),
	}}
	a := newEditAdapter(t, fr)
	created, err := a.CreateUserProvider(context.Background(), adapters.UserProviderForm{
		DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{Name: "First", URL: "https://cdn.example.com/a.m3u8", Order: 0},
			{Name: "Listy", URL: playlistURL, Order: 1},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	enumAfterCreate := fr.enumCalls
	keep, _ := firstTwoChannelIDs(t, created.Provider)

	if err := a.ReorderUserProvider(context.Background(), created.Provider.ID, adapters.ReorderRequest{
		Channels: []adapters.UserOrderEntry{{ID: keep, Order: 5}},
	}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if fr.enumCalls != enumAfterCreate {
		t.Fatalf("reorder triggered enumeration (calls %d → %d); it must reuse cache", enumAfterCreate, fr.enumCalls)
	}
	// Persisted Order survived.
	for _, def := range a.userStore.Snapshot() {
		if def.ID != created.Provider.ID {
			continue
		}
		for _, ch := range def.Channels {
			if ch.ID == keep && ch.Order != 5 {
				t.Fatalf("channel %q Order = %d, want 5", keep, ch.Order)
			}
		}
	}
}

func firstTwoChannelIDs(t *testing.T, p adapters.CatalogProvider) (string, string) {
	t.Helper()
	var ids []string
	for _, g := range p.Groups {
		for _, ch := range g.Channels {
			ids = append(ids, ch.ID)
		}
	}
	if len(ids) < 2 {
		t.Fatalf("want >=2 channels, got %v", ids)
	}
	return ids[0], ids[1]
}

// channelIDsByName returns the (keep, drop) channel IDs from a chassis-shaped
// provider whose channels are named "Keep"/"Drop".
func channelIDsByName(t *testing.T, p adapters.CatalogProvider) (keep, drop string) {
	t.Helper()
	for _, g := range p.Groups {
		for _, ch := range g.Channels {
			switch ch.Name {
			case "Keep":
				keep = ch.ID
			case "Drop":
				drop = ch.ID
			}
		}
	}
	if keep == "" || drop == "" {
		t.Fatalf("could not find keep/drop channel IDs in %+v", p.Groups)
	}
	return keep, drop
}

func TestVerifyChannel_PlaylistReturnsCount(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("a", "b", "c"),
	}}
	a := newEditAdapter(t, fr)
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"www.youtube.com": {"93.184.216.34"}}}
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{URL: playlistURL})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Kind != kindPlaylist || res.ItemCount != 3 {
		t.Fatalf("verify result = %+v, want ok playlist count=3", res)
	}
}

func TestVerifyChannel_SingleSurfacesIsLive(t *testing.T) {
	t.Parallel()
	fr := &fakeResolver{res: &ytdlp.Resolution{URL: "https://edge/live.m3u8", IsLive: true, Title: "Stream"}}
	a := newEditAdapter(t, fr)
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"twitch.tv": {"93.184.216.34"}}}
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{URL: "https://twitch.tv/foo"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Kind != kindSingle || !res.IsLive {
		t.Fatalf("verify result = %+v, want ok single isLive=true", res)
	}
}

func TestVerifyChannel_RejectsBlockedHost(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	// Syntactic gate rejects loopback before any network call.
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{URL: "http://127.0.0.1/stream.m3u8"})
	if err != nil {
		t.Fatalf("verify returned error (should be a soft not-OK result): %v", err)
	}
	if res.OK {
		t.Fatalf("verify OK for loopback host, want OK=false: %+v", res)
	}
}

func TestVerifyChannel_PlaylistRejectsResolvedBlockedHostBeforeYTDLP(t *testing.T) {
	t.Parallel()
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		"https://evil.example/playlist": ytEntries("a"),
	}}
	a := newEditAdapter(t, fr)
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"evil.example": {"127.0.0.1"}}}
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{
		URL:  "https://evil.example/playlist",
		Kind: kindPlaylist,
	})
	if err != nil {
		t.Fatalf("verify returned error (should be a soft not-OK result): %v", err)
	}
	if res.OK {
		t.Fatalf("verify OK for DNS-to-loopback host, want OK=false: %+v", res)
	}
	if fr.enumCalls != 0 {
		t.Fatalf("enumCalls = %d, want 0 (blocked before yt-dlp)", fr.enumCalls)
	}
}

func TestVerifyChannel_InvalidKindIsSoftFailure(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{
		URL:  "https://cdn.example.com/live.m3u8",
		Kind: "weird",
	})
	if err != nil {
		t.Fatalf("verify returned error (should be a soft not-OK result): %v", err)
	}
	if res.OK {
		t.Fatalf("verify OK for invalid kind, want OK=false: %+v", res)
	}
}

func TestVerifyChannel_DirectOK(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"cdn.example.com": {"93.184.216.34"}}}
	a.userRedirectDoer = stubDoer{} // no redirect → 200, walk terminates at the original URL
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{URL: "https://cdn.example.com/live.m3u8"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Kind != kindDirect || !res.IsLive {
		t.Fatalf("verify result = %+v, want ok direct isLive=true", res)
	}
}

var _ adapters.UserProviderEditor = (*Adapter)(nil)
