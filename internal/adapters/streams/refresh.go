package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type RefreshStatus struct {
	ProviderID           string
	Source               string
	FetchedAt            time.Time
	Err                  error
	refreshedProviderIDs []string
}

type refreshJobKind string

const (
	refreshJobNone     refreshJobKind = ""
	refreshJobManifest refreshJobKind = "manifest"
	refreshJobCatalog  refreshJobKind = "catalog"
)

type refreshJob struct {
	Kind        refreshJobKind
	ProviderIDs []string
}

type refreshSchedule struct {
	lastManifest time.Time
	lastCatalog  map[string]time.Time
}

func (a *Adapter) RefreshNow(ctx context.Context, providerID string) RefreshStatus {
	if ctx == nil {
		ctx = context.Background()
	}
	status := RefreshStatus{ProviderID: providerID}
	if providerID != "" {
		return a.refreshCatalogsDefault(ctx, []string{providerID}, "manual")
	}
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
	failures := 0
	schedule := refreshSchedule{}
	for {
		cfg := a.configSnapshot()
		defs := a.definitionSnapshot()
		job := schedule.nextJob(time.Now(), cfg, defs)
		status := RefreshStatus{}
		switch job.Kind {
		case refreshJobManifest:
			if a.refreshOnce != nil {
				status = a.refreshOnce(ctx, "background")
			}
		case refreshJobCatalog:
			status = a.refreshCatalogsDefault(ctx, job.ProviderIDs, "background")
		default:
			status = RefreshStatus{Source: "remote", FetchedAt: time.Now().UTC()}
		}
		if ctx.Err() != nil {
			return
		}
		markTime := time.Now()
		defs = a.definitionSnapshot()
		schedule.markRefreshResult(job, status, markTime, defs)
		if status.Err != nil {
			failures++
		} else {
			failures = 0
		}
		interval := a.refreshInterval(status.Err, failures)
		interval = schedule.intervalAfterRefreshResult(time.Now(), a.configSnapshot(), a.definitionSnapshot(), job, status, interval)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *refreshSchedule) nextJob(now time.Time, cfg Config, defs []ProviderDefinition) refreshJob {
	if s.lastManifest.IsZero() || !now.Before(s.lastManifest.Add(refreshHoursDuration(cfg.ManifestRefreshHours))) {
		return refreshJob{Kind: refreshJobManifest}
	}
	due := make([]string, 0, len(defs))
	for _, def := range defs {
		last := s.lastCatalogTime(def.ID)
		if last.IsZero() || !now.Before(last.Add(providerCatalogRefreshDuration(cfg, def))) {
			due = append(due, def.ID)
		}
	}
	if len(due) != 0 {
		return refreshJob{Kind: refreshJobCatalog, ProviderIDs: due}
	}
	return refreshJob{Kind: refreshJobNone}
}

func (s *refreshSchedule) nextInterval(now time.Time, cfg Config, defs []ProviderDefinition) time.Duration {
	return s.nextIntervalExcludingCatalogIDs(now, cfg, defs, nil)
}

func (s *refreshSchedule) nextIntervalExcludingCatalogIDs(now time.Time, cfg Config, defs []ProviderDefinition, excluded map[string]struct{}) time.Duration {
	next := s.lastManifest.Add(refreshHoursDuration(cfg.ManifestRefreshHours))
	if s.lastManifest.IsZero() {
		next = now
	}
	for _, def := range defs {
		if _, ok := excluded[def.ID]; ok {
			continue
		}
		catalogNext := s.lastCatalogTime(def.ID).Add(providerCatalogRefreshDuration(cfg, def))
		if s.lastCatalogTime(def.ID).IsZero() {
			catalogNext = now
		}
		if catalogNext.Before(next) {
			next = catalogNext
		}
	}
	if !next.After(now) {
		return 0
	}
	return next.Sub(now)
}

func (s *refreshSchedule) mark(job refreshJob, at time.Time, defs []ProviderDefinition) {
	if job.Kind == refreshJobNone {
		return
	}
	if s.lastCatalog == nil {
		s.lastCatalog = map[string]time.Time{}
	}
	switch job.Kind {
	case refreshJobManifest:
		s.lastManifest = at
		for _, def := range defs {
			s.lastCatalog[def.ID] = at
		}
	case refreshJobCatalog:
		for _, providerID := range job.ProviderIDs {
			s.lastCatalog[providerID] = at
		}
	}
}

func (s *refreshSchedule) markRefreshResult(job refreshJob, status RefreshStatus, at time.Time, defs []ProviderDefinition) {
	if job.Kind == refreshJobCatalog {
		if len(status.refreshedProviderIDs) != 0 {
			s.mark(refreshJob{Kind: refreshJobCatalog, ProviderIDs: status.refreshedProviderIDs}, at, defs)
			return
		}
		if status.Err != nil {
			return
		}
	}
	if status.Err == nil {
		s.mark(job, at, defs)
	}
}

func (s *refreshSchedule) intervalAfterRefreshResult(now time.Time, cfg Config, defs []ProviderDefinition, job refreshJob, status RefreshStatus, failureInterval time.Duration) time.Duration {
	if status.Err == nil {
		return s.nextInterval(now, cfg, defs)
	}
	if job.Kind != refreshJobCatalog {
		return failureInterval
	}
	failed := failedCatalogProviderSet(job.ProviderIDs, status.refreshedProviderIDs)
	if len(failed) == 0 {
		return failureInterval
	}
	nextHealthy := s.nextIntervalExcludingCatalogIDs(now, cfg, defs, failed)
	if nextHealthy < failureInterval {
		return nextHealthy
	}
	return failureInterval
}

func failedCatalogProviderSet(requested, refreshed []string) map[string]struct{} {
	refreshedSet := make(map[string]struct{}, len(refreshed))
	for _, providerID := range refreshed {
		refreshedSet[providerID] = struct{}{}
	}
	failed := make(map[string]struct{}, len(requested))
	for _, providerID := range requested {
		if _, ok := refreshedSet[providerID]; !ok {
			failed[providerID] = struct{}{}
		}
	}
	return failed
}

func (s *refreshSchedule) lastCatalogTime(providerID string) time.Time {
	if s.lastCatalog == nil {
		return time.Time{}
	}
	return s.lastCatalog[providerID]
}

func refreshHoursDuration(hours int) time.Duration {
	if hours < 1 {
		hours = 1
	}
	return time.Duration(hours) * time.Hour
}

func providerCatalogRefreshDuration(cfg Config, def ProviderDefinition) time.Duration {
	hours := cfg.CatalogRefreshHours
	if def.CatalogRefreshHours != nil && *def.CatalogRefreshHours > 0 {
		hours = *def.CatalogRefreshHours
	}
	if override, ok := cfg.Providers[def.ID]; ok && override.CatalogRefreshHours > 0 {
		hours = override.CatalogRefreshHours
	}
	return refreshHoursDuration(hours)
}

func (a *Adapter) refreshInterval(err error, failures int) time.Duration {
	a.mu.Lock()
	hours := a.cfg.ManifestRefreshHours
	a.mu.Unlock()
	interval := refreshHoursDuration(hours)
	if err == nil {
		return interval
	}
	if failures < 1 {
		failures = 1
	}

	delay := time.Minute
	for i := 1; i < failures && delay < interval; i++ {
		delay *= 2
		if delay > interval {
			delay = interval
		}
	}
	return a.jitterRefreshDelay(delay, interval)
}

func (a *Adapter) jitterRefreshDelay(delay, max time.Duration) time.Duration {
	jitter := delay / 10
	if jitter <= 0 {
		return delay
	}
	a.mu.Lock()
	if a.rng != nil {
		delay += time.Duration(a.rng.Int63n(int64(jitter)*2+1)) - jitter
	}
	a.mu.Unlock()
	if delay < time.Second {
		return time.Second
	}
	if delay > max {
		return max
	}
	return delay
}

func (a *Adapter) refreshOnceDefault(ctx context.Context, reason string) RefreshStatus {
	cfg := a.configSnapshot()
	status := RefreshStatus{Source: "bundled", FetchedAt: time.Now().UTC()}
	if !cfg.AllowRemoteManifest {
		return status
	}

	manifest, meta, err := a.fetchManifest(ctx)
	if err != nil {
		fallback := a.refreshCatalogsDefault(ctx, nil, reason)
		if len(fallback.refreshedProviderIDs) != 0 {
			return fallback
		}
		if fallback.Err != nil {
			err = errors.Join(err, fallback.Err)
		}
		a.recordRefreshFailure(err)
		status.Err = err
		return status
	}
	enum := userPlaylistEnumerator{resolver: a.resolver, cookiesPath: a.cookiesPath, cacheDir: a.cacheDir, cfg: cfg}
	snapshot, err := buildRemoteSnapshot(ctx, cfg, manifest, a.cacheDir, a.userStore.Snapshot(), enum)
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

func (a *Adapter) refreshCatalogsDefault(ctx context.Context, providerIDs []string, reason string) RefreshStatus {
	_ = reason
	cfg := a.configSnapshot()
	status := RefreshStatus{Source: "bundled", FetchedAt: time.Now().UTC()}
	if len(providerIDs) == 1 {
		status.ProviderID = providerIDs[0]
	}

	defs, err := a.definitionsForRefresh(providerIDs)
	if err != nil {
		a.recordRefreshFailure(err)
		status.Err = err
		return status
	}

	remoteAllowed := cfg.AllowRemoteManifest
	var errs []error
	remoteRefreshed := false
	// Build the live enumerator once — its fields are identical for every
	// provider iteration. a.resolver and a.cookiesPath are set once at
	// Start/New time and never mutated, so reading them outside a.mu here
	// matches the existing off-lock read pattern in playback.go.
	enum := userPlaylistEnumerator{resolver: a.resolver, cookiesPath: a.cookiesPath, cacheDir: a.cacheDir, cfg: cfg}
	for _, def := range defs {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			// buildInlineCatalog runs enumeration BEFORE a.mu is taken —
			// preserving "no lock across network I/O."
			cat, err := buildInlineCatalog(ctx, def, enum)
			if err != nil {
				errs = append(errs, fmt.Errorf("provider %q build catalog: %w", def.ID, err))
				continue
			}
			a.mu.Lock()
			a.catalogs[cat.ProviderID] = cat
			if a.state != adapters.StateStopped {
				a.state = adapters.StateRunning
			}
			a.stateSince = time.Now()
			a.mu.Unlock()
			status.refreshedProviderIDs = append(status.refreshedProviderIDs, def.ID)
			continue
		}
		if !remoteAllowed {
			continue
		}
		raw, meta, err := fetchProviderPlaylist(ctx, def, cfg, a.cacheDir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cat, err := buildProviderCatalog(def, raw, cfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("provider %q build catalog: %w", def.ID, err))
			continue
		}
		if err := writeCatalogCaches(a.cacheDir, map[string][]byte{def.ID: raw}, map[string]CacheMetadata{def.ID: meta}); err != nil {
			errs = append(errs, fmt.Errorf("provider %q write catalog cache: %w", def.ID, err))
			continue
		}

		a.mu.Lock()
		a.catalogs[cat.ProviderID] = cat
		if a.state != adapters.StateStopped {
			a.state = adapters.StateRunning
		}
		a.stateSince = time.Now()
		a.mu.Unlock()

		remoteRefreshed = true
		status.refreshedProviderIDs = append(status.refreshedProviderIDs, def.ID)
		if meta.FetchedAt.After(status.FetchedAt) {
			status.FetchedAt = meta.FetchedAt
		}
	}
	if remoteRefreshed {
		status.Source = "remote"
	}
	if len(errs) != 0 {
		status.Err = errors.Join(errs...)
		a.recordRefreshFailure(status.Err)
		return status
	}
	if len(status.refreshedProviderIDs) == 0 {
		return status
	}

	a.mu.Lock()
	a.lastErr = ""
	a.mu.Unlock()

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

	cachedBody, cachedMeta, cachedOK := readConditionalCache(a.cacheDir, "manifest", cfg.ManifestURL)
	resp, err := secureFetcher{}.FetchConditional(fetchCtx, cfg.ManifestURL, fetchLimits{
		MaxBytes:       cfg.MaxManifestBytes,
		AllowLocalURLs: cfg.AllowLocalManifestURLs,
	}, fetchConditionFromMeta(cachedMeta))
	if err != nil {
		return Manifest{}, CacheMetadata{}, err
	}
	body := resp.Body
	meta := cacheMetadataFromFetch(cfg.ManifestURL, resp, cachedMeta)
	if resp.NotModified {
		if !cachedOK {
			return Manifest{}, CacheMetadata{}, fmt.Errorf("manifest returned not modified without a valid cache")
		}
		body = cachedBody
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return Manifest{}, CacheMetadata{}, fmt.Errorf("parse manifest json: %w", err)
	}
	if resp.NotModified {
		if err := validateCachedManifest(fetchCtx, manifest, cfg); err != nil {
			return Manifest{}, CacheMetadata{}, err
		}
		manifest = sanitizeManifestArtwork(fetchCtx, manifest, cfg, validateProviderArtworkURLSyntax)
		return manifest, meta, nil
	}
	if err := validateManifest(fetchCtx, manifest, cfg); err != nil {
		return Manifest{}, CacheMetadata{}, err
	}
	manifest = sanitizeManifestArtwork(fetchCtx, manifest, cfg, validateProviderArtworkURL)
	return manifest, meta, nil
}

type remoteSnapshot struct {
	Definitions   []ProviderDefinition
	Catalogs      []ProviderCatalog
	CatalogBodies map[string][]byte
	CatalogMetas  map[string]CacheMetadata
}

// appendUserProviders appends user-authored providers to a merged provider
// list, skipping any whose per-provider override is Disabled — the same
// disable check mergeManifests applies to bundled/remote providers. User IDs
// carry the reserved "user:" prefix (validateUserProviderID), so they can
// never collide with bundled/remote IDs already in the slice.
func appendUserProviders(providers []ProviderDefinition, cfg Config, userProviders []ProviderDefinition) []ProviderDefinition {
	for _, up := range userProviders {
		if !isUserProviderID(up.ID) {
			// Defense in depth (spec §4.5): a user provider without the reserved
			// "user:" prefix could shadow a bundled/remote provider in the merged
			// map. The store validates this at load/Put time; guard here too so a
			// future caller of the pure build path can't introduce a shadow.
			slog.Warn("user_providers: skipping provider without user: prefix", "id", up.ID)
			continue
		}
		if override, ok := cfg.Providers[up.ID]; ok && override.Disabled {
			continue
		}
		providers = append(providers, up)
	}
	return providers
}

func buildStartupSnapshot(ctx context.Context, cfg Config, cacheDir string, userProviders []ProviderDefinition) ([]ProviderDefinition, []ProviderCatalog, error) {
	// resolver nil → cache-only, non-blocking. Start() calls this before
	// a.resolver is assigned, so we must never block on a yt-dlp subprocess at
	// startup; live enumeration is the refresh loop's (and the edit path's) job.
	return buildSnapshotWithEnumerator(ctx, cfg, cacheDir, userProviders, userPlaylistEnumerator{cacheDir: cacheDir, cfg: cfg})
}

// buildSnapshotWithEnumerator merges bundled+cached+user providers and builds
// their catalogs using the supplied enumerator. A cache-only enumerator
// (resolver nil) serves cached playlist items without yt-dlp; a live
// enumerator (resolver set) enumerates fresh. Pure/off-lock: callers install
// the result under a.mu.
func buildSnapshotWithEnumerator(ctx context.Context, cfg Config, cacheDir string, userProviders []ProviderDefinition, enum userPlaylistEnumerator) ([]ProviderDefinition, []ProviderCatalog, error) {
	cached := loadCachedManifest(ctx, cfg, cacheDir)
	bundled := sanitizeManifestArtwork(ctx, bundledManifest(), cfg, validateProviderArtworkURLSyntax)
	manifest := mergeManifests(cfg, bundled, cached, nil, remoteProviderFactories())
	manifest.Providers = appendUserProviders(manifest.Providers, cfg, userProviders)
	return buildCachedOrSeedSnapshot(ctx, manifest.Providers, cfg, cacheDir, enum)
}

func buildCachedOrSeedSnapshot(ctx context.Context, defs []ProviderDefinition, cfg Config, cacheDir string, enum userPlaylistEnumerator) ([]ProviderDefinition, []ProviderCatalog, error) {
	catalogs := make([]ProviderCatalog, 0, len(defs))
	for _, def := range defs {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildInlineCatalog(ctx, def, enum)
			if err != nil {
				return nil, nil, err
			}
			catalogs = append(catalogs, cat)
			continue
		}
		if raw, _, ok := readConditionalCache(cacheDir, catalogCacheKey(def.ID), def.PlaylistURL); ok {
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

// buildInlineCatalog builds the locally-derived (no remote fetch) catalogs:
// direct-streams (pure) and user providers (playlist channels use enum). It
// centralizes the direct-vs-user branch shared by the startup, remote, and
// catalog-refresh paths.
func buildInlineCatalog(ctx context.Context, def ProviderDefinition, enum userPlaylistEnumerator) (ProviderCatalog, error) {
	if def.Type == userProviderType {
		return buildUserCatalog(ctx, def, enum)
	}
	return buildDirectStreamsCatalog(def)
}

func (a *Adapter) definitionSnapshot() []ProviderDefinition {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ProviderDefinition, 0, len(a.definitionOrder))
	for _, id := range a.definitionOrder {
		if def, ok := a.definitions[id]; ok {
			out = append(out, def)
		}
	}
	return out
}

func (a *Adapter) definitionsForRefresh(providerIDs []string) ([]ProviderDefinition, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(providerIDs) == 0 {
		out := make([]ProviderDefinition, 0, len(a.definitionOrder))
		for _, id := range a.definitionOrder {
			if def, ok := a.definitions[id]; ok {
				out = append(out, def)
			}
		}
		return out, nil
	}
	out := make([]ProviderDefinition, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		def, ok := a.definitions[providerID]
		if !ok {
			return nil, fmt.Errorf("streams provider %q is not active", providerID)
		}
		out = append(out, def)
	}
	return out, nil
}

func buildRemoteSnapshot(ctx context.Context, cfg Config, remote Manifest, cacheDir string, userProviders []ProviderDefinition, enum userPlaylistEnumerator) (remoteSnapshot, error) {
	bundled := sanitizeManifestArtwork(ctx, bundledManifest(), cfg, validateProviderArtworkURLSyntax)
	remote = sanitizeManifestArtwork(ctx, remote, cfg, validateProviderArtworkURL)
	manifest := mergeManifests(cfg, bundled, nil, &remote, remoteProviderFactories())
	manifest.Providers = appendUserProviders(manifest.Providers, cfg, userProviders)
	out := remoteSnapshot{
		Definitions:   manifest.Providers,
		Catalogs:      make([]ProviderCatalog, 0, len(manifest.Providers)),
		CatalogBodies: map[string][]byte{},
		CatalogMetas:  map[string]CacheMetadata{},
	}
	for _, def := range manifest.Providers {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildInlineCatalog(ctx, def, enum)
			if err != nil {
				return remoteSnapshot{}, fmt.Errorf("provider %q build catalog: %w", def.ID, err)
			}
			out.Catalogs = append(out.Catalogs, cat)
			continue
		}
		raw, meta, err := fetchProviderPlaylist(ctx, def, cfg, cacheDir)
		if err != nil {
			return remoteSnapshot{}, err
		}
		cat, err := buildProviderCatalog(def, raw, cfg)
		if err != nil {
			return remoteSnapshot{}, err
		}
		out.Catalogs = append(out.Catalogs, cat)
		out.CatalogBodies[def.ID] = raw
		out.CatalogMetas[def.ID] = meta
	}
	return out, nil
}

func fetchProviderPlaylist(ctx context.Context, def ProviderDefinition, cfg Config, cacheDir string) ([]byte, CacheMetadata, error) {
	timeout := time.Duration(cfg.CatalogRequestTimeoutSeconds) * time.Second
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cacheKey := catalogCacheKey(def.ID)
	cachedBody, cachedMeta, cachedOK := readConditionalCache(cacheDir, cacheKey, def.PlaylistURL)
	resp, err := secureFetcher{}.FetchConditional(fetchCtx, def.PlaylistURL, fetchLimitsForProvider(def, cfg), fetchConditionFromMeta(cachedMeta))
	if err != nil {
		return nil, CacheMetadata{}, fmt.Errorf("fetch provider %q playlist: %w", def.ID, err)
	}
	meta := cacheMetadataFromFetch(def.PlaylistURL, resp, cachedMeta)
	if resp.NotModified {
		if !cachedOK {
			return nil, CacheMetadata{}, fmt.Errorf("fetch provider %q playlist: not modified without a valid cache", def.ID)
		}
		return cachedBody, meta, nil
	}
	return resp.Body, meta, nil
}

func buildProviderCatalog(def ProviderDefinition, raw []byte, cfg Config) (ProviderCatalog, error) {
	switch def.Type {
	case youtubeChannelJSONProviderType:
		return buildYouTubeChannelCatalog(def, raw, cfg)
	case directStreamsProviderType:
		return buildDirectStreamsCatalog(def)
	case userProviderType:
		// Cache-only path: the nil-resolver enumerator never runs yt-dlp, so
		// context.Background() is fine (no network, no deadline to honor). Live
		// playlist enumeration uses the caller's ctx via the startup/remote/
		// catalog-refresh snapshot paths (Task 6). Do NOT wire a non-nil resolver
		// into this dispatch — it would enumerate under a deadline-less ctx.
		return buildUserCatalog(context.Background(), def, userPlaylistEnumerator{cfg: cfg})
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

func loadCachedManifest(ctx context.Context, cfg Config, cacheDir string) *Manifest {
	if !cfg.AllowRemoteManifest && !cfg.AllowCachedRemoteManifest {
		return nil
	}
	raw, _, ok := readConditionalCache(cacheDir, "manifest", cfg.ManifestURL)
	if !ok {
		return nil
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil
	}
	if err := validateCachedManifest(ctx, manifest, cfg); err != nil {
		return nil
	}
	manifest = sanitizeManifestArtwork(ctx, manifest, cfg, validateProviderArtworkURLSyntax)
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

func readConditionalCache(cacheDir, key, sourceURL string) ([]byte, CacheMetadata, bool) {
	body, meta, err := readCacheFile(cacheDir, key)
	if err != nil {
		return nil, CacheMetadata{}, false
	}
	if meta.SourceURL != sourceURL {
		return nil, CacheMetadata{}, false
	}
	return body, meta, true
}

func fetchConditionFromMeta(meta CacheMetadata) fetchCondition {
	return fetchCondition{
		ETag:         meta.ETag,
		LastModified: meta.LastModified,
	}
}

func cacheMetadataFromFetch(sourceURL string, resp fetchResponse, cached CacheMetadata) CacheMetadata {
	now := time.Now().UTC()
	if resp.NotModified {
		meta := cached
		meta.FetchedAt = now
		meta.SourceURL = sourceURL
		if resp.ETag != "" {
			meta.ETag = resp.ETag
		}
		if resp.LastModified != "" {
			meta.LastModified = resp.LastModified
		}
		return meta
	}
	return CacheMetadata{
		ETag:         resp.ETag,
		LastModified: resp.LastModified,
		FetchedAt:    now,
		SourceURL:    sourceURL,
	}
}
