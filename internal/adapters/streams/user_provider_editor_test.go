package streams

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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
