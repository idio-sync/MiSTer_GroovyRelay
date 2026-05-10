package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type RefreshStatus struct {
	ProviderID string
	Source     string
	FetchedAt  time.Time
	Err        error
}

func (a *Adapter) RefreshNow(ctx context.Context, providerID string) RefreshStatus {
	if ctx == nil {
		ctx = context.Background()
	}
	status := RefreshStatus{ProviderID: providerID}
	if a.refreshOnce != nil {
		status = a.refreshOnce(ctx, "manual")
		if status.ProviderID == "" {
			status.ProviderID = providerID
		}
	}
	return status
}

func (a *Adapter) refreshLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		status := RefreshStatus{}
		if a.refreshOnce != nil {
			status = a.refreshOnce(ctx, "background")
		}
		if ctx.Err() != nil {
			return
		}
		interval := a.refreshInterval(status.Err)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *Adapter) refreshInterval(err error) time.Duration {
	a.mu.Lock()
	hours := a.cfg.ManifestRefreshHours
	a.mu.Unlock()
	if hours < 1 {
		hours = 1
	}
	interval := time.Duration(hours) * time.Hour
	if err != nil && interval > time.Minute {
		return time.Minute
	}
	return interval
}

func (a *Adapter) refreshOnceDefault(ctx context.Context, reason string) RefreshStatus {
	cfg := a.configSnapshot()
	status := RefreshStatus{Source: "bundled", FetchedAt: time.Now().UTC()}
	if !cfg.AllowRemoteManifest {
		return status
	}

	manifest, meta, err := a.fetchManifest(ctx)
	if err != nil {
		a.recordRefreshFailure(err)
		status.Err = err
		return status
	}
	snapshot, err := buildRemoteSnapshot(ctx, cfg, manifest)
	if err != nil {
		a.recordRefreshFailure(err)
		status.Err = err
		return status
	}
	if err := writeManifestCache(a.cacheDir, manifest, meta); err != nil {
		a.recordRefreshFailure(err)
		status.Err = err
		return status
	}
	if err := writeCatalogCaches(a.cacheDir, snapshot.CatalogBodies, snapshot.CatalogMetas); err != nil {
		a.recordRefreshFailure(err)
		status.Err = err
		return status
	}

	a.mu.Lock()
	a.installSnapshotLocked(snapshot.Definitions, snapshot.Catalogs)
	a.lastErr = ""
	if a.state != adapters.StateStopped {
		a.state = adapters.StateRunning
	}
	a.stateSince = time.Now()
	a.mu.Unlock()

	status.Source = "remote"
	status.FetchedAt = meta.FetchedAt
	return status
}

func (a *Adapter) recordRefreshFailure(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.lastErr = err.Error()
	}
}

func (a *Adapter) fetchManifestDefault(ctx context.Context) (Manifest, CacheMetadata, error) {
	cfg := a.configSnapshot()
	timeout := time.Duration(cfg.ManifestRequestTimeoutSeconds) * time.Second
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := secureFetcher{}.Fetch(fetchCtx, cfg.ManifestURL, fetchLimits{
		MaxBytes:       cfg.MaxManifestBytes,
		AllowLocalURLs: cfg.AllowLocalManifestURLs,
	})
	if err != nil {
		return Manifest{}, CacheMetadata{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return Manifest{}, CacheMetadata{}, fmt.Errorf("parse manifest json: %w", err)
	}
	if err := validateManifest(fetchCtx, manifest, cfg); err != nil {
		return Manifest{}, CacheMetadata{}, err
	}
	return manifest, CacheMetadata{
		FetchedAt: time.Now().UTC(),
		SourceURL: cfg.ManifestURL,
	}, nil
}

type remoteSnapshot struct {
	Definitions   []ProviderDefinition
	Catalogs      []ProviderCatalog
	CatalogBodies map[string][]byte
	CatalogMetas  map[string]CacheMetadata
}

func buildStartupSnapshot(ctx context.Context, cfg Config, cacheDir string) ([]ProviderDefinition, []ProviderCatalog, error) {
	cached := loadCachedManifest(ctx, cfg, cacheDir)
	manifest := mergeManifests(cfg, bundledManifest(), cached, nil, providerFactories())
	return buildCachedOrSeedSnapshot(manifest.Providers, cfg, cacheDir)
}

func buildCachedOrSeedSnapshot(defs []ProviderDefinition, cfg Config, cacheDir string) ([]ProviderDefinition, []ProviderCatalog, error) {
	catalogs := make([]ProviderCatalog, 0, len(defs))
	for _, def := range defs {
		if raw, _, err := readCacheFile(cacheDir, catalogCacheKey(def.ID)); err == nil {
			cat, err := buildProviderCatalog(def, raw, cfg)
			if err == nil {
				catalogs = append(catalogs, cat)
				continue
			}
		}
		if path, ok := bundledSeedPath(def.ID); ok {
			raw, err := seedFS.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("read bundled seed %q: %w", def.ID, err)
			}
			cat, err := buildProviderCatalog(def, raw, cfg)
			if err != nil {
				return nil, nil, err
			}
			catalogs = append(catalogs, cat)
		}
	}
	return defs, catalogs, nil
}

func buildRemoteSnapshot(ctx context.Context, cfg Config, remote Manifest) (remoteSnapshot, error) {
	manifest := mergeManifests(cfg, bundledManifest(), nil, &remote, providerFactories())
	out := remoteSnapshot{
		Definitions:   manifest.Providers,
		Catalogs:      make([]ProviderCatalog, 0, len(manifest.Providers)),
		CatalogBodies: map[string][]byte{},
		CatalogMetas:  map[string]CacheMetadata{},
	}
	for _, def := range manifest.Providers {
		raw, err := fetchProviderPlaylist(ctx, def, cfg)
		if err != nil {
			return remoteSnapshot{}, err
		}
		cat, err := buildProviderCatalog(def, raw, cfg)
		if err != nil {
			return remoteSnapshot{}, err
		}
		out.Catalogs = append(out.Catalogs, cat)
		out.CatalogBodies[def.ID] = raw
		out.CatalogMetas[def.ID] = CacheMetadata{
			FetchedAt: time.Now().UTC(),
			SourceURL: def.PlaylistURL,
		}
	}
	return out, nil
}

func fetchProviderPlaylist(ctx context.Context, def ProviderDefinition, cfg Config) ([]byte, error) {
	timeout := time.Duration(cfg.CatalogRequestTimeoutSeconds) * time.Second
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := secureFetcher{}.Fetch(fetchCtx, def.PlaylistURL, fetchLimitsForProvider(def, cfg))
	if err != nil {
		return nil, fmt.Errorf("fetch provider %q playlist: %w", def.ID, err)
	}
	return body, nil
}

func buildProviderCatalog(def ProviderDefinition, raw []byte, cfg Config) (ProviderCatalog, error) {
	switch def.Type {
	case youtubeChannelJSONProviderType:
		return buildYouTubeChannelCatalog(def, raw, cfg)
	default:
		return ProviderCatalog{}, fmt.Errorf("provider %q type %q is unsupported", def.ID, def.Type)
	}
}

func bundledSeedPath(providerID string) (string, bool) {
	switch providerID {
	case "mtv-rewind":
		return "testdata/mtv-playlists.seed.json", true
	case "cartoon-rewind":
		return "testdata/cartoon-playlists.seed.json", true
	default:
		return "", false
	}
}

func providerFactories() map[string]ProviderFactory {
	return map[string]ProviderFactory{
		youtubeChannelJSONProviderType: func(ProviderDefinition) (Provider, error) { return struct{}{}, nil },
	}
}

func loadCachedManifest(ctx context.Context, cfg Config, cacheDir string) *Manifest {
	if !cfg.AllowRemoteManifest && !cfg.AllowCachedRemoteManifest {
		return nil
	}
	raw, _, err := readCacheFile(cacheDir, "manifest")
	if err != nil {
		return nil
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil
	}
	if err := validateCachedManifest(ctx, manifest, cfg); err != nil {
		return nil
	}
	return &manifest
}

func writeManifestCache(cacheDir string, manifest Manifest, meta CacheMetadata) error {
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeCacheFile(cacheDir, "manifest", body, meta)
}

func writeCatalogCaches(cacheDir string, bodies map[string][]byte, metas map[string]CacheMetadata) error {
	for providerID, body := range bodies {
		if err := writeCacheFile(cacheDir, catalogCacheKey(providerID), body, metas[providerID]); err != nil {
			return err
		}
	}
	return nil
}

func catalogCacheKey(providerID string) string {
	return "catalog-" + providerID
}

func fetchLimitsForProvider(def ProviderDefinition, cfg Config) fetchLimits {
	limits := fetchLimits{
		MaxBytes:       cfg.MaxCatalogBytes,
		AllowLocalURLs: cfg.AllowLocalManifestURLs,
	}
	if !isBundledProviderID(def.ID) {
		limits.AllowedHosts = mustHostSet(cfg.RemoteProviderAllowedHosts)
	}
	return limits
}

func isBundledProviderID(providerID string) bool {
	for _, def := range bundledManifest().Providers {
		if def.ID == providerID {
			return true
		}
	}
	return false
}

func mustHostSet(hosts []string) map[string]struct{} {
	set, err := normalizeHostSet(hosts)
	if err != nil {
		return nil
	}
	return set
}
