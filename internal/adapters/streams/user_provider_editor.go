package streams

import (
	"context"
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// liveEnumerator builds the live (yt-dlp-backed) playlist enumerator from the
// adapter's resolver. a.resolver and a.cookiesPath are set once at Start/New
// time and never mutated, matching the off-lock read pattern in
// refreshCatalogsDefault (refresh.go:346-350).
func (a *Adapter) liveEnumerator(cfg Config) userPlaylistEnumerator {
	return userPlaylistEnumerator{resolver: a.resolver, cookiesPath: a.cookiesPath, cacheDir: a.cacheDir, cfg: cfg}
}

type userCatalogSnapshot struct {
	definitions []ProviderDefinition
	catalogs    []ProviderCatalog
}

// buildUserCatalogSnapshotLive rebuilds the full merged snapshot with LIVE
// playlist enumeration from an explicit user-provider candidate. It is pure
// with respect to adapter state: no store write and no a.mu install.
func (a *Adapter) buildUserCatalogSnapshotLive(ctx context.Context, userProviders []ProviderDefinition) (userCatalogSnapshot, error) {
	cfg := a.configSnapshot()
	defs, catalogs, err := buildSnapshotWithEnumerator(ctx, cfg, a.cacheDir, userProviders, a.liveEnumerator(cfg))
	if err != nil {
		return userCatalogSnapshot{}, fmt.Errorf("streams: build user catalogs: %w", err)
	}
	return userCatalogSnapshot{definitions: defs, catalogs: catalogs}, nil
}

func (a *Adapter) buildUserCatalogSnapshotCacheOnly(ctx context.Context, userProviders []ProviderDefinition) (userCatalogSnapshot, error) {
	cfg := a.configSnapshot()
	defs, catalogs, err := buildSnapshotWithEnumerator(ctx, cfg, a.cacheDir, userProviders, userPlaylistEnumerator{cacheDir: a.cacheDir, cfg: cfg})
	if err != nil {
		return userCatalogSnapshot{}, fmt.Errorf("streams: build user catalogs (cache-only): %w", err)
	}
	return userCatalogSnapshot{definitions: defs, catalogs: catalogs}, nil
}

func (a *Adapter) installUserCatalogSnapshot(snapshot userCatalogSnapshot) {
	a.mu.Lock()
	a.installSnapshotLocked(snapshot.definitions, snapshot.catalogs)
	if a.state != adapters.StateStopped {
		a.state = adapters.StateRunning
	}
	a.mu.Unlock()
}

// rebuildUserCatalogsLive preserves the existing refresh-like convenience path
// for non-mutating callers. Mutating editor methods use the explicit
// build→commit→install sequence above so failed rebuilds never persist an edit.
func (a *Adapter) rebuildUserCatalogsLive(ctx context.Context) error {
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, a.userStore.Snapshot())
	if err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	return nil
}

// rebuildUserCatalogsCacheOnly rebuilds + installs WITHOUT re-enumerating
// playlists (reuses cached items). Used by Reorder, which only re-sorts by
// Order (spec §8 "Reorder ... does not re-enumerate").
func (a *Adapter) rebuildUserCatalogsCacheOnly(ctx context.Context) error {
	snapshot, err := a.buildUserCatalogSnapshotCacheOnly(ctx, a.userStore.Snapshot())
	if err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	return nil
}
