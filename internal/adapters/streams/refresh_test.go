package streams

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
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

	status := a.RefreshNow(t.Context(), "")
	if status.Err == nil {
		t.Fatal("manual refresh should report error")
	}
	after := a.catalogSnapshotForTest("mtv-rewind")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed refresh replaced last good catalog")
	}
}

func TestRefreshOnceFallsBackToBundledCatalogsWhenManifestFetchFails(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits[req.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/mtv.json":
			_, _ = w.Write([]byte(`{"metal":["BBBBBBBBBBB"]}`))
		case "/cartoon.json":
			_, _ = w.Write([]byte(`{"heman":["AAAAAAAAAAA"]}`))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)

	mtvDef := bundledMTVDefinition()
	mtvDef.BaseURL = server.URL
	mtvDef.PlaylistURL = server.URL + "/mtv.json"
	cartoonDef := bundledCartoonDefinition()
	cartoonDef.BaseURL = server.URL
	cartoonDef.PlaylistURL = server.URL + "/cartoon.json"
	a.replaceDefinitionsForTest([]ProviderDefinition{mtvDef, cartoonDef})
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = true
	a.cfg.AllowLocalManifestURLs = true
	a.mu.Unlock()
	a.fetchManifest = func(context.Context) (Manifest, CacheMetadata, error) {
		return Manifest{}, CacheMetadata{}, errors.New("manifest unavailable")
	}

	status := a.refreshOnceDefault(t.Context(), "manual")
	if status.Err != nil {
		t.Fatalf("refreshOnceDefault: %v", status.Err)
	}
	if !reflect.DeepEqual(status.refreshedProviderIDs, []string{"mtv-rewind", "cartoon-rewind"}) {
		t.Fatalf("refreshed provider IDs = %#v, want bundled providers", status.refreshedProviderIDs)
	}
	if hits["/mtv.json"] != 1 || hits["/cartoon.json"] != 1 {
		t.Fatalf("playlist fetches = %#v, want both bundled provider playlists", hits)
	}
	mtv := a.catalogSnapshotForTest("mtv-rewind")
	if got := mtv.Channel("metal").Items[0].SourceID; got != "BBBBBBBBBBB" {
		t.Fatalf("mtv catalog item = %q, want bundled playlist refresh", got)
	}
	cartoon := a.catalogSnapshotForTest("cartoon-rewind")
	if got := cartoon.Channel("heman").Items[0].SourceID; got != "AAAAAAAAAAA" {
		t.Fatalf("cartoon catalog item = %q, want bundled playlist refresh", got)
	}
}

func TestRefreshIntervalBacksOffRepeatedFailuresWithJitter(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.mu.Lock()
	a.cfg.ManifestRefreshHours = 1
	a.rng = rand.New(rand.NewSource(1))
	a.mu.Unlock()

	first := a.refreshInterval(errors.New("network down"), 1)
	third := a.refreshInterval(errors.New("network down"), 3)
	capped := a.refreshInterval(errors.New("network down"), 20)
	success := a.refreshInterval(nil, 0)

	if first < 45*time.Second || first > 75*time.Second {
		t.Fatalf("first failure interval = %s, want about one minute with jitter", first)
	}
	if third < 3*time.Minute || third > 5*time.Minute {
		t.Fatalf("third failure interval = %s, want about four minutes with jitter", third)
	}
	if third <= first {
		t.Fatalf("third failure interval = %s, want greater than first %s", third, first)
	}
	if capped > time.Hour {
		t.Fatalf("capped failure interval = %s, want no more than configured interval", capped)
	}
	if success != time.Hour {
		t.Fatalf("success interval = %s, want configured interval", success)
	}
}

func TestRefreshScheduleUsesCatalogIntervalBeforeManifestInterval(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ManifestRefreshHours = 24
	cfg.CatalogRefreshHours = 12
	defs := []ProviderDefinition{bundledMTVDefinition()}
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	schedule := refreshSchedule{}

	first := schedule.nextJob(now, cfg, defs)
	if first.Kind != refreshJobManifest {
		t.Fatalf("first job = %+v, want manifest", first)
	}
	schedule.mark(first, now, defs)

	next := schedule.nextJob(now.Add(12*time.Hour), cfg, defs)
	if next.Kind != refreshJobCatalog || len(next.ProviderIDs) != 1 || next.ProviderIDs[0] != "mtv-rewind" {
		t.Fatalf("next job = %+v, want catalog refresh for mtv-rewind", next)
	}
	wait := schedule.nextInterval(now, cfg, defs)
	if wait != 12*time.Hour {
		t.Fatalf("next interval = %s, want 12h catalog interval", wait)
	}
}

func TestRefreshScheduleUsesProviderCatalogRefreshOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ManifestRefreshHours = 24
	cfg.CatalogRefreshHours = 12
	cfg.Providers["mtv-rewind"] = ProviderConfig{CatalogRefreshHours: 3}
	defs := []ProviderDefinition{bundledMTVDefinition(), bundledCartoonDefinition()}
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	schedule := refreshSchedule{}

	first := schedule.nextJob(now, cfg, defs)
	if first.Kind != refreshJobManifest {
		t.Fatalf("first job = %+v, want manifest", first)
	}
	schedule.mark(first, now, defs)

	next := schedule.nextJob(now.Add(3*time.Hour), cfg, defs)
	if next.Kind != refreshJobCatalog || len(next.ProviderIDs) != 1 || next.ProviderIDs[0] != "mtv-rewind" {
		t.Fatalf("next job = %+v, want only mtv-rewind provider override due", next)
	}
}

func TestRefreshScheduleMarksOnlySuccessfulCatalogProvidersOnPartialFailure(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ManifestRefreshHours = 24
	cfg.CatalogRefreshHours = 12
	defs := []ProviderDefinition{bundledMTVDefinition(), bundledCartoonDefinition()}
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	schedule := refreshSchedule{}

	schedule.mark(refreshJob{Kind: refreshJobManifest}, now, defs)
	job := refreshJob{Kind: refreshJobCatalog, ProviderIDs: []string{"mtv-rewind", "cartoon-rewind"}}
	schedule.markRefreshResult(job, RefreshStatus{
		Err:                  errors.New("mtv failed"),
		refreshedProviderIDs: []string{"cartoon-rewind"},
	}, now.Add(12*time.Hour), defs)

	next := schedule.nextJob(now.Add(12*time.Hour), cfg, defs)
	if next.Kind != refreshJobCatalog || len(next.ProviderIDs) != 1 || next.ProviderIDs[0] != "mtv-rewind" {
		t.Fatalf("next job = %+v, want only failed mtv-rewind provider still due", next)
	}
}

func TestRefreshScheduleCapsPartialFailureBackoffByNextHealthyProviderDue(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ManifestRefreshHours = 24
	cfg.CatalogRefreshHours = 12
	cfg.Providers["cartoon-rewind"] = ProviderConfig{CatalogRefreshHours: 3}
	defs := []ProviderDefinition{bundledMTVDefinition(), bundledCartoonDefinition()}
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	schedule := refreshSchedule{}

	schedule.mark(refreshJob{Kind: refreshJobManifest}, now, defs)
	job := refreshJob{Kind: refreshJobCatalog, ProviderIDs: []string{"mtv-rewind", "cartoon-rewind"}}
	status := RefreshStatus{
		Err:                  errors.New("mtv failed"),
		refreshedProviderIDs: []string{"cartoon-rewind"},
	}
	markTime := now.Add(12 * time.Hour)
	schedule.markRefreshResult(job, status, markTime, defs)

	got := schedule.intervalAfterRefreshResult(markTime, cfg, defs, job, status, 6*time.Hour)
	if got != 3*time.Hour {
		t.Fatalf("partial failure retry interval = %s, want capped by cartoon next due time 3h", got)
	}
}

func TestRefreshScheduleCapsFailedOnlyRetryByNextHealthyProviderDue(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ManifestRefreshHours = 24
	cfg.CatalogRefreshHours = 12
	cfg.Providers["cartoon-rewind"] = ProviderConfig{CatalogRefreshHours: 3}
	defs := []ProviderDefinition{bundledMTVDefinition(), bundledCartoonDefinition()}
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	schedule := refreshSchedule{}

	schedule.mark(refreshJob{Kind: refreshJobManifest}, now, defs)
	initialJob := refreshJob{Kind: refreshJobCatalog, ProviderIDs: []string{"mtv-rewind", "cartoon-rewind"}}
	initialStatus := RefreshStatus{
		Err:                  errors.New("mtv failed"),
		refreshedProviderIDs: []string{"cartoon-rewind"},
	}
	markTime := now.Add(12 * time.Hour)
	schedule.markRefreshResult(initialJob, initialStatus, markTime, defs)

	retryNow := markTime.Add(time.Hour)
	retryJob := refreshJob{Kind: refreshJobCatalog, ProviderIDs: []string{"mtv-rewind"}}
	retryStatus := RefreshStatus{Err: errors.New("mtv still failed")}
	got := schedule.intervalAfterRefreshResult(retryNow, cfg, defs, retryJob, retryStatus, 6*time.Hour)
	if got != 2*time.Hour {
		t.Fatalf("failed-only retry interval = %s, want capped by cartoon remaining due time 2h", got)
	}
}

func TestRefreshCatalogsContinuesAfterProviderFailure(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits[req.URL.Path]++
		switch req.URL.Path {
		case "/mtv.json":
			http.Error(w, "upstream down", http.StatusBadGateway)
		case "/cartoon.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"heman":["AAAAAAAAAAA"]}`))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)

	mtvDef := bundledMTVDefinition()
	mtvDef.BaseURL = server.URL
	mtvDef.PlaylistURL = server.URL + "/mtv.json"
	cartoonDef := bundledCartoonDefinition()
	cartoonDef.BaseURL = server.URL
	cartoonDef.PlaylistURL = server.URL + "/cartoon.json"
	a.replaceDefinitionsForTest([]ProviderDefinition{mtvDef, cartoonDef})
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = true
	a.cfg.AllowLocalManifestURLs = true
	a.mu.Unlock()

	status := a.refreshCatalogsDefault(t.Context(), []string{"mtv-rewind", "cartoon-rewind"}, "background")
	if status.Err == nil {
		t.Fatal("partial catalog refresh should report the failed provider")
	}
	if !reflect.DeepEqual(status.refreshedProviderIDs, []string{"cartoon-rewind"}) {
		t.Fatalf("refreshed provider IDs = %#v, want only cartoon-rewind", status.refreshedProviderIDs)
	}
	if hits["/mtv.json"] != 1 || hits["/cartoon.json"] != 1 {
		t.Fatalf("playlist fetches = %#v, want both providers attempted", hits)
	}
	cartoon := a.catalogSnapshotForTest("cartoon-rewind")
	if got := cartoon.Channel("heman").Items[0].SourceID; got != "AAAAAAAAAAA" {
		t.Fatalf("cartoon catalog item = %q, want refreshed catalog despite mtv failure", got)
	}
	mtv := a.catalogSnapshotForTest("mtv-rewind")
	if got := mtv.Channel("metal").Items[0].SourceID; got != "dQw4w9WgXcQ" {
		t.Fatalf("mtv catalog item = %q, want last good catalog after failed refresh", got)
	}
}

func TestRefreshNowProviderIDRefreshesOnlyThatCatalog(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	manifestFetches := 0
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits[req.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/mtv.json":
			_, _ = w.Write([]byte(`{"metal":["BBBBBBBBBBB"]}`))
		case "/cartoon.json":
			_, _ = w.Write([]byte(`{"heman":["AAAAAAAAAAA"]}`))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)

	mtvDef := bundledMTVDefinition()
	mtvDef.BaseURL = server.URL
	mtvDef.PlaylistURL = server.URL + "/mtv.json"
	cartoonDef := bundledCartoonDefinition()
	cartoonDef.BaseURL = server.URL
	cartoonDef.PlaylistURL = server.URL + "/cartoon.json"
	a.replaceDefinitionsForTest([]ProviderDefinition{mtvDef, cartoonDef})
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = true
	a.cfg.AllowLocalManifestURLs = true
	a.mu.Unlock()
	a.fetchManifest = func(context.Context) (Manifest, CacheMetadata, error) {
		manifestFetches++
		return Manifest{}, CacheMetadata{}, nil
	}

	status := a.RefreshNow(t.Context(), "cartoon-rewind")
	if status.Err != nil {
		t.Fatalf("RefreshNow provider: %v", status.Err)
	}
	if manifestFetches != 0 {
		t.Fatalf("provider refresh fetched manifest %d times", manifestFetches)
	}
	if hits["/cartoon.json"] != 1 || hits["/mtv.json"] != 0 {
		t.Fatalf("playlist fetches = %#v, want only cartoon provider fetched", hits)
	}
	cartoon := a.catalogSnapshotForTest("cartoon-rewind")
	if got := cartoon.Channel("heman").Items[0].SourceID; got != "AAAAAAAAAAA" {
		t.Fatalf("cartoon catalog item = %q, want refreshed provider catalog", got)
	}
	mtv := a.catalogSnapshotForTest("mtv-rewind")
	if got := mtv.Channel("metal").Items[0].SourceID; got != "dQw4w9WgXcQ" {
		t.Fatalf("mtv catalog item = %q, want unchanged provider catalog", got)
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
	if err := writeCacheFile(cacheDir, "manifest", manifestBody, CacheMetadata{SourceURL: DefaultConfig().ManifestURL}); err != nil {
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

func TestStartIgnoresCachedRemoteManifestForDifferentSourceURL(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	remoteDef := cachedProviderDefinitionForTest()
	manifestBody, err := json.Marshal(Manifest{Version: 1, Providers: []ProviderDefinition{remoteDef}})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := writeCacheFile(cacheDir, "manifest", manifestBody, CacheMetadata{SourceURL: "https://old.example/providers.json"}); err != nil {
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
	a.cfg.ManifestURL = "https://new.example/providers.json"
	a.cfg.AllowRemoteManifest = false
	a.cfg.AllowCachedRemoteManifest = true
	a.mu.Unlock()

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if cat := a.catalogSnapshotForTest("cached-provider"); cat.ProviderID != "" {
		t.Fatalf("cached provider loaded from mismatched manifest source: %+v", cat)
	}
}

func TestStartIgnoresCachedCatalogForDifferentSourceURL(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	remoteDef := cachedProviderDefinitionForTest()
	manifestURL := "https://cached.example/providers.json"
	manifestBody, err := json.Marshal(Manifest{Version: 1, Providers: []ProviderDefinition{remoteDef}})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := writeCacheFile(cacheDir, "manifest", manifestBody, CacheMetadata{SourceURL: manifestURL}); err != nil {
		t.Fatalf("write manifest cache: %v", err)
	}
	if err := writeCacheFile(cacheDir, "catalog-cached-provider", []byte(`{"metal":["dQw4w9WgXcQ"]}`), CacheMetadata{SourceURL: "https://old.example/playlist.json"}); err != nil {
		t.Fatalf("write catalog cache: %v", err)
	}

	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dataDir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.cfg.ManifestURL = manifestURL
	a.cfg.AllowRemoteManifest = false
	a.cfg.AllowCachedRemoteManifest = true
	a.mu.Unlock()

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if cat := a.catalogSnapshotForTest("cached-provider"); cat.ProviderID != "" {
		t.Fatalf("cached catalog loaded from mismatched playlist source: %+v", cat)
	}
}

func TestFetchManifestDefaultUsesCacheOnNotModified(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"cached.example": []string{"93.184.216.34"},
	})

	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	remoteDef := cachedProviderDefinitionForTest()
	manifestBody, err := json.Marshal(Manifest{Version: 1, Providers: []ProviderDefinition{remoteDef}})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	const etag = `"manifest-v1"`
	const lastModified = "Wed, 21 Oct 2015 07:28:00 GMT"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("If-None-Match"); got != etag {
			t.Fatalf("If-None-Match = %q, want %q", got, etag)
		}
		if got := req.Header.Get("If-Modified-Since"); got != lastModified {
			t.Fatalf("If-Modified-Since = %q", got)
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastModified)
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(server.Close)
	if err := writeCacheFile(cacheDir, "manifest", manifestBody, CacheMetadata{
		ETag:         etag,
		LastModified: lastModified,
		SourceURL:    server.URL,
	}); err != nil {
		t.Fatalf("write manifest cache: %v", err)
	}

	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dataDir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.cfg.ManifestURL = server.URL
	a.cfg.AllowLocalManifestURLs = true
	a.mu.Unlock()

	manifest, meta, err := a.fetchManifestDefault(t.Context())
	if err != nil {
		t.Fatalf("fetchManifestDefault: %v", err)
	}
	if len(manifest.Providers) != 1 || manifest.Providers[0].ID != "cached-provider" {
		t.Fatalf("manifest providers = %+v", manifest.Providers)
	}
	if meta.ETag != etag || meta.LastModified != lastModified {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestFetchManifestDefaultIgnoresCachedValidatorsForDifferentSourceURL(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"cached.example": []string{"93.184.216.34"},
	})

	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	oldManifestBody, err := json.Marshal(Manifest{Version: 1, Providers: []ProviderDefinition{cachedProviderDefinitionForTest()}})
	if err != nil {
		t.Fatalf("marshal old manifest: %v", err)
	}
	if err := writeCacheFile(cacheDir, "manifest", oldManifestBody, CacheMetadata{
		ETag:         `"old-manifest"`,
		LastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
		SourceURL:    "https://old.example/providers.json",
	}); err != nil {
		t.Fatalf("write manifest cache: %v", err)
	}

	seenHeaders := make(chan http.Header, 1)
	newManifestBody, err := json.Marshal(Manifest{Version: 1, Providers: []ProviderDefinition{cachedProviderDefinitionForTest()}})
	if err != nil {
		t.Fatalf("marshal new manifest: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seenHeaders <- req.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(newManifestBody)
	}))
	t.Cleanup(server.Close)

	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dataDir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.cfg.ManifestURL = server.URL
	a.cfg.AllowLocalManifestURLs = true
	a.mu.Unlock()

	if _, _, err := a.fetchManifestDefault(t.Context()); err != nil {
		t.Fatalf("fetchManifestDefault: %v", err)
	}
	headers := <-seenHeaders
	if got := headers.Get("If-None-Match"); got != "" {
		t.Fatalf("If-None-Match = %q, want empty for different cache source", got)
	}
	if got := headers.Get("If-Modified-Since"); got != "" {
		t.Fatalf("If-Modified-Since = %q, want empty for different cache source", got)
	}
}

func TestRefreshUsesCachedCatalogOnNotModified(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	const etag = `"catalog-v1"`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("If-None-Match"); got != etag {
			t.Fatalf("If-None-Match = %q, want %q", got, etag)
		}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(server.Close)
	if err := writeCacheFile(cacheDir, "catalog-cached-provider", []byte(`{"metal":["dQw4w9WgXcQ"]}`), CacheMetadata{
		ETag:      etag,
		SourceURL: server.URL,
	}); err != nil {
		t.Fatalf("write catalog cache: %v", err)
	}

	remoteDef := cachedProviderDefinitionForTest()
	remoteDef.PlaylistURL = server.URL
	remoteDef.BaseURL = server.URL

	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dataDir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = true
	a.cfg.AllowLocalManifestURLs = true
	a.cfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}
	a.cfg.Providers["cartoon-rewind"] = ProviderConfig{Disabled: true}
	a.mu.Unlock()
	a.fetchManifest = func(context.Context) (Manifest, CacheMetadata, error) {
		return Manifest{Version: 1, Providers: []ProviderDefinition{remoteDef}}, CacheMetadata{FetchedAt: time.Now().UTC()}, nil
	}

	status := a.RefreshNow(t.Context(), "")
	if status.Err != nil {
		t.Fatalf("RefreshNow: %v", status.Err)
	}
	cat := a.catalogSnapshotForTest("cached-provider")
	if cat.ProviderID != "cached-provider" || len(cat.Channels) != 1 || len(cat.Channels[0].Items) != 1 {
		t.Fatalf("cached catalog not loaded: %+v", cat)
	}
}

func TestRefreshIgnoresCatalogCacheValidatorsForDifferentSourceURL(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := filepath.Join(dataDir, "streams")
	if err := writeCacheFile(cacheDir, "catalog-cached-provider", []byte(`{"metal":["dQw4w9WgXcQ"]}`), CacheMetadata{
		ETag:      `"old-catalog"`,
		SourceURL: "https://old.example/playlist.json",
	}); err != nil {
		t.Fatalf("write catalog cache: %v", err)
	}

	seenHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seenHeaders <- req.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metal":["9bZkp7q19f0"]}`))
	}))
	t.Cleanup(server.Close)

	remoteDef := cachedProviderDefinitionForTest()
	remoteDef.PlaylistURL = server.URL
	remoteDef.BaseURL = server.URL

	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dataDir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = true
	a.cfg.AllowLocalManifestURLs = true
	a.cfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}
	a.cfg.Providers["cartoon-rewind"] = ProviderConfig{Disabled: true}
	a.mu.Unlock()
	a.fetchManifest = func(context.Context) (Manifest, CacheMetadata, error) {
		return Manifest{Version: 1, Providers: []ProviderDefinition{remoteDef}}, CacheMetadata{FetchedAt: time.Now().UTC()}, nil
	}

	status := a.RefreshNow(t.Context(), "")
	if status.Err != nil {
		t.Fatalf("RefreshNow: %v", status.Err)
	}
	headers := <-seenHeaders
	if got := headers.Get("If-None-Match"); got != "" {
		t.Fatalf("If-None-Match = %q, want empty for different cache source", got)
	}
	cat := a.catalogSnapshotForTest("cached-provider")
	if got := cat.Channel("metal").Items[0].SourceID; got != "9bZkp7q19f0" {
		t.Fatalf("catalog item = %q, want freshly fetched body", got)
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
	if err := writeCacheFile(cacheDir, "manifest", manifestBody, CacheMetadata{SourceURL: DefaultConfig().ManifestURL}); err != nil {
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
