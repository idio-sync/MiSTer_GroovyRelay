package streams

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestStartDisabledLoadsBundledAndDoesNotFetch(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fetches := 0
	a.fetchManifest = func(context.Context) (Manifest, CacheMetadata, error) {
		fetches++
		return Manifest{}, CacheMetadata{}, nil
	}

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if fetches != 0 {
		t.Fatalf("disabled Start fetched remote manifest %d times", fetches)
	}
	mtv := a.catalogSnapshotForTest("mtv-rewind")
	if mtv.ProviderID != "mtv-rewind" || len(mtv.Channels) == 0 {
		t.Fatalf("bundled MTV catalog not loaded: %+v", mtv)
	}
}

func TestStartEnabledStartsRefreshLoopAndStopCancels(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(true)
	started := make(chan struct{})
	var once sync.Once
	a.refreshOnce = func(ctx context.Context, reason string) RefreshStatus {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return RefreshStatus{Err: ctx.Err()}
	}

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh loop did not start")
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestManualRefreshKeepsLastGoodOnFailure(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	before := a.catalogSnapshotForTest("mtv-rewind")
	a.refreshOnce = func(ctx context.Context, reason string) RefreshStatus {
		return RefreshStatus{Err: errors.New("network down")}
	}

	status := a.RefreshNow(t.Context(), "mtv-rewind")
	if status.Err == nil {
		t.Fatal("manual refresh should report error")
	}
	after := a.catalogSnapshotForTest("mtv-rewind")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed refresh replaced last good catalog")
	}
}

func TestStartLoadsCachedRemoteManifestAndCatalogWithoutNetwork(t *testing.T) {
	useManifestValidationResolver(t, blockingResolver{})

	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	remoteDef := cachedProviderDefinitionForTest()
	manifestBody, err := json.Marshal(Manifest{Version: 1, Providers: []ProviderDefinition{remoteDef}})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := writeCacheFile(cacheDir, "manifest", manifestBody, CacheMetadata{SourceURL: "https://cached.example/providers.json"}); err != nil {
		t.Fatalf("write manifest cache: %v", err)
	}
	if err := writeCacheFile(cacheDir, "catalog-cached-provider", []byte(`{"metal":["dQw4w9WgXcQ"]}`), CacheMetadata{SourceURL: remoteDef.PlaylistURL}); err != nil {
		t.Fatalf("write catalog cache: %v", err)
	}

	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dataDir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = false
	a.cfg.AllowCachedRemoteManifest = true
	a.mu.Unlock()
	fetches := 0
	a.fetchManifest = func(context.Context) (Manifest, CacheMetadata, error) {
		fetches++
		return Manifest{}, CacheMetadata{}, nil
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if fetches != 0 {
		t.Fatalf("Start fetched network manifest %d times", fetches)
	}
	cat := a.catalogSnapshotForTest("cached-provider")
	if cat.ProviderID != "cached-provider" || len(cat.Channels) != 1 || len(cat.Channels[0].Items) != 1 {
		t.Fatalf("cached catalog not loaded: %+v", cat)
	}
}

func TestApplyConfigLoadsCachedOverlayWhenEnabled(t *testing.T) {
	useManifestValidationResolver(t, blockingResolver{})

	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	remoteDef := cachedProviderDefinitionForTest()
	manifestBody, err := json.Marshal(Manifest{Version: 1, Providers: []ProviderDefinition{remoteDef}})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := writeCacheFile(cacheDir, "manifest", manifestBody, CacheMetadata{SourceURL: "https://cached.example/providers.json"}); err != nil {
		t.Fatalf("write manifest cache: %v", err)
	}
	if err := writeCacheFile(cacheDir, "catalog-cached-provider", []byte(`{"metal":["dQw4w9WgXcQ"]}`), CacheMetadata{SourceURL: remoteDef.PlaylistURL}); err != nil {
		t.Fatalf("write catalog cache: %v", err)
	}

	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dataDir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = false
	a.cfg.AllowCachedRemoteManifest = false
	a.mu.Unlock()
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if cat := a.catalogSnapshotForTest("cached-provider"); cat.ProviderID != "" {
		t.Fatalf("cached provider loaded before allow_cached_remote_manifest=true: %+v", cat)
	}

	raw, meta := decodeStreamsSection(t, `
enabled = false
allow_remote_manifest = false
allow_cached_remote_manifest = true
`)
	if _, err := a.ApplyConfig(raw, meta); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	cat := a.catalogSnapshotForTest("cached-provider")
	if cat.ProviderID != "cached-provider" || len(cat.Channels) != 1 {
		t.Fatalf("cached provider not loaded after ApplyConfig: %+v", cat)
	}
}

func TestApplyConfigDisablesProviderInInstalledSnapshot(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	raw, meta := decodeStreamsSection(t, `
enabled = false
allow_remote_manifest = false

[adapters.streams.providers.mtv-rewind]
disabled = true
`)
	if _, err := a.ApplyConfig(raw, meta); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if matched {
		t.Fatalf("disabled provider matched: %+v", res)
	}
}

func TestCatalogFetchAllowlistOnlyAppliesToRemoteProviders(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RemoteProviderAllowedHosts = []string{"trusted.example"}
	if got := fetchLimitsForProvider(bundledMTVDefinition(), cfg).AllowedHosts; len(got) != 0 {
		t.Fatalf("bundled provider got remote allowlist: %#v", got)
	}

	remoteDef := cachedProviderDefinitionForTest()
	got := fetchLimitsForProvider(remoteDef, cfg).AllowedHosts
	if _, ok := got["trusted.example"]; !ok || len(got) != 1 {
		t.Fatalf("remote provider allowlist = %#v, want trusted.example", got)
	}
}

func TestApplyConfigStartsRefreshLoopWhenRemoteManifestEnabled(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = false
	a.mu.Unlock()

	started := make(chan string, 1)
	a.refreshOnce = func(ctx context.Context, reason string) RefreshStatus {
		started <- reason
		<-ctx.Done()
		return RefreshStatus{Err: ctx.Err()}
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	select {
	case reason := <-started:
		t.Fatalf("refresh loop started before allow_remote_manifest=true, reason=%q", reason)
	default:
	}

	raw, meta := decodeStreamsSection(t, `
enabled = true
allow_remote_manifest = true
`)
	if _, err := a.ApplyConfig(raw, meta); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	select {
	case reason := <-started:
		if reason != "background" {
			t.Fatalf("refresh reason = %q, want background", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh loop did not start after allow_remote_manifest=true")
	}
}

func cachedProviderDefinitionForTest() ProviderDefinition {
	return ProviderDefinition{
		ID:              "cached-provider",
		Type:            youtubeChannelJSONProviderType,
		DisplayName:     "Cached Provider",
		BaseURL:         "https://cached.example",
		PlaylistURL:     "https://cached.example/playlist.json",
		DefaultChannel:  "metal",
		DefaultPlayMode: PlayShuffle,
		URLRules: []URLRule{{
			ID:         "cached-channel",
			Schemes:    []string{"https"},
			Hosts:      []string{"cached.example"},
			Path:       "/player.html",
			Target:     "channel",
			QueryParam: "channel",
		}},
		Channels: []ChannelDefinition{{ID: "metal", Name: "Metal", PlayMode: PlayShuffle}},
	}
}
