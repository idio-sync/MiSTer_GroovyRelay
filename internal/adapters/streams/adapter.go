package streams

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

type AdapterConfig struct {
	Bridge        config.BridgeConfig
	Core          SessionManager
	YTDLPResolver ytdlp.BinaryResolver
}

type SessionManager interface {
	StartSession(core.SessionRequest) error
	StartSessionIfIdle(core.SessionRequest) (bool, error)
	StartSessionIfSession(core.SessionRequest, string, uint64) (bool, error)
	PauseIfAdapterRef(string) (bool, error)
	StopIfAdapterRef(string) (bool, error)
	StopIfSession(string, uint64) (bool, error)
	Status() core.SessionStatus
}

type streamResolver interface {
	Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error)
	EnumeratePlaylist(ctx context.Context, pageURL, cookiesPath string, maxItems int) ([]ytdlp.PlaylistEntry, error)
}

type hlsBufferOpener func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error)

const defaultYTDLPResolveTimeout = 30 * time.Second

type Adapter struct {
	core          SessionManager
	bridge        config.BridgeConfig
	cookiesPath   string
	cacheDir      string
	ytdlpBinary   ytdlp.BinaryResolver
	resolver      streamResolver
	hlsBufferOpen hlsBufferOpener

	// User-provider play-time SSRF seams (lazily defaulted in playback.go).
	userURLResolver  hostResolver
	userRedirectDoer httpDoer

	fetchManifest func(context.Context) (Manifest, CacheMetadata, error)
	refreshOnce   func(ctx context.Context, reason string) RefreshStatus

	// Test seam for deterministic stale-continuation interleaving coverage.
	beforeQueueContinuation func()
	// Test seam for deterministic StopQueue/OnStop interleaving coverage.
	beforeStopQueuePlaybackLock func(queueCapture)
	// Test seam for deterministic Replay replacement interleaving coverage.
	beforeReplayReplace func()
	playbackMu          sync.Mutex
	// userEditMu serializes user-provider create/update/delete/reorder across
	// candidate planning, off-lock rebuild, atomic JSON commit, catalog install,
	// and preset cleanup. It is intentionally separate from a.mu so catalog
	// enumeration never happens while the adapter state lock is held.
	userEditMu sync.Mutex
	mu         sync.Mutex
	cfg                 Config
	state               adapters.State
	lastErr             string
	stateSince          time.Time
	rng                 *rand.Rand
	expectedStops       map[queueCapture]struct{}
	definitions         map[string]ProviderDefinition
	definitionOrder     []string
	catalogs            map[string]ProviderCatalog
	presetStore         *presetStore
	userStore           *userProviderStore
	active              *ActiveQueue
	badItems            map[string]badStreamItem

	activeOverlay *hlsMeterHandle

	loopCtx    context.Context
	loopCancel context.CancelFunc
	loopDone   chan struct{}
}

func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Bridge.DataDir == "" {
		return nil, fmt.Errorf("streams: AdapterConfig.Bridge.DataDir is required")
	}
	a := &Adapter{
		core:          cfg.Core,
		bridge:        cfg.Bridge,
		cookiesPath:   filepath.Join(cfg.Bridge.DataDir, "streams_cookies.txt"),
		cacheDir:      filepath.Join(cfg.Bridge.DataDir, "streams"),
		ytdlpBinary:   cfg.YTDLPResolver,
		hlsBufferOpen: hlsbuffer.OpenSession,
		cfg:           DefaultConfig(),
		state:         adapters.StateStopped,
		stateSince:    time.Now(),
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
		definitions:   map[string]ProviderDefinition{},
		catalogs:      map[string]ProviderCatalog{},
		badItems:      map[string]badStreamItem{},
	}
	a.fetchManifest = a.fetchManifestDefault
	a.refreshOnce = a.refreshOnceDefault

	var err error
	a.presetStore, err = newPresetStore(
		filepath.Join(cfg.Bridge.DataDir, "chassis_presets.json"),
		a.resolvePresetEntry,
	)
	if err != nil {
		return nil, fmt.Errorf("streams: preset store: %w", err)
	}
	a.userStore, err = newUserProviderStore(
		filepath.Join(cfg.Bridge.DataDir, "user_providers.json"),
	)
	if err != nil {
		return nil, fmt.Errorf("streams: user provider store: %w", err)
	}
	return a, nil
}

// resolvePresetEntry is the catalogResolver bound to this adapter.
// Looks up (provider, channel) against the bundled chassis catalog
// (via Catalog) and returns the populated PresetEntry. Used by the
// presetStore for both load-time hydration and SetStarred validation.
func (a *Adapter) resolvePresetEntry(providerID, channelID string) (adapters.PresetEntry, bool) {
	for _, p := range a.Catalog() {
		if p.ID != providerID {
			continue
		}
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if c.ID == channelID {
					return adapters.PresetEntry{
						ProviderID: p.ID,
						ChannelID:  c.ID,
						Title:      c.Name,
						BadgeLabel: p.BadgeLabel,
						BadgeClass: p.BadgeClass,
						Live:       c.Live,
					}, true
				}
			}
		}
	}
	return adapters.PresetEntry{}, false
}

func (a *Adapter) Name() string        { return "streams" }
func (a *Adapter) DisplayName() string { return "Streams" }

func (a *Adapter) Fields() []adapters.FieldDef {
	defaults := DefaultConfig()
	fields := []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: defaults.Enabled, ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_url", Label: "Manifest URL", Kind: adapters.KindText, Default: defaults.ManifestURL, Required: true, ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_refresh_hours", Label: "Manifest Refresh Hours", Kind: adapters.KindInt, Default: defaults.ManifestRefreshHours, ApplyScope: adapters.ScopeHotSwap},
		{Key: "catalog_refresh_hours", Label: "Catalog Refresh Hours", Kind: adapters.KindInt, Default: defaults.CatalogRefreshHours, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_manifest_bytes", Label: "Max Manifest Bytes", Kind: adapters.KindInt, Default: defaults.MaxManifestBytes, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_catalog_bytes", Label: "Max Catalog Bytes", Kind: adapters.KindInt, Default: defaults.MaxCatalogBytes, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_items_per_channel", Label: "Max Items Per Channel", Kind: adapters.KindInt, Default: defaults.MaxItemsPerChannel, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_consecutive_failures", Label: "Max Consecutive Failures", Kind: adapters.KindInt, Default: defaults.MaxConsecutiveFailures, ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_request_timeout_seconds", Label: "Manifest Request Timeout Seconds", Kind: adapters.KindInt, Default: defaults.ManifestRequestTimeoutSeconds, ApplyScope: adapters.ScopeHotSwap},
		{Key: "catalog_request_timeout_seconds", Label: "Catalog Request Timeout Seconds", Kind: adapters.KindInt, Default: defaults.CatalogRequestTimeoutSeconds, ApplyScope: adapters.ScopeHotSwap},
		{Key: "youtube_format", Label: "YouTube Format", Kind: adapters.KindText, Default: defaults.YoutubeFormat, Required: true, ApplyScope: adapters.ScopeRestartCast},
		{Key: "allow_remote_manifest", Label: "Allow Remote Manifest", Kind: adapters.KindBool, Default: defaults.AllowRemoteManifest, ApplyScope: adapters.ScopeHotSwap},
		{Key: "allow_cached_remote_manifest", Label: "Allow Cached Remote Manifest", Kind: adapters.KindBool, Default: defaults.AllowCachedRemoteManifest, ApplyScope: adapters.ScopeHotSwap},
		{Key: "allow_local_manifest_urls", Label: "Allow Local Manifest URLs", Kind: adapters.KindBool, Default: defaults.AllowLocalManifestURLs, ApplyScope: adapters.ScopeHotSwap},
		{Key: "remote_provider_allowed_hosts", Label: "Remote Provider Allowed Hosts", Kind: adapters.KindText, Default: formatHostList(defaults.RemoteProviderAllowedHosts), ApplyScope: adapters.ScopeHotSwap},
	}

	a.mu.Lock()
	providerIDs := make([]string, 0, len(a.cfg.Providers))
	for id := range a.cfg.Providers {
		providerIDs = append(providerIDs, id)
	}
	a.mu.Unlock()
	sort.Strings(providerIDs)

	for _, id := range providerIDs {
		fields = append(fields,
			adapters.FieldDef{
				Key:        fmt.Sprintf("providers.%s.disabled", id),
				Label:      fmt.Sprintf("%s Disabled", id),
				Kind:       adapters.KindBool,
				Default:    false,
				ApplyScope: adapters.ScopeHotSwap,
				Section:    "Provider Overrides",
			},
			adapters.FieldDef{
				Key:        fmt.Sprintf("providers.%s.catalog_refresh_hours", id),
				Label:      fmt.Sprintf("%s Catalog Refresh Hours", id),
				Kind:       adapters.KindInt,
				Default:    0,
				ApplyScope: adapters.ScopeHotSwap,
				Section:    "Provider Overrides",
			},
			adapters.FieldDef{
				Key:        fmt.Sprintf("providers.%s.hls_buffer_disabled", id),
				Label:      fmt.Sprintf("%s HLS Buffer Disabled", id),
				Kind:       adapters.KindBool,
				Default:    false,
				ApplyScope: adapters.ScopeHotSwap,
				Section:    "Provider Overrides",
			},
		)
	}
	return fields
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("streams: decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("streams: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Enabled = v
}

func (a *Adapter) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var resolver streamResolver
	if a.ytdlpBinary != nil {
		resolver = &ytdlp.Resolver{
			BinaryResolver: a.ytdlpBinary,
			Timeout:        defaultYTDLPResolveTimeout,
			Runner:         ytdlp.OSRunner{},
		}
	}

	defs, catalogs, err := a.buildStartupSnapshot(ctx)
	if err != nil {
		return err
	}

	a.mu.Lock()
	if resolver != nil {
		a.resolver = resolver // keep a pre-set resolver when ytdlpBinary is nil
	}
	a.installSnapshotLocked(defs, catalogs)
	a.loopCtx = ctx
	a.state = adapters.StateRunning
	a.lastErr = ""
	a.stateSince = time.Now()
	startLoop := a.cfg.Enabled && a.cfg.AllowRemoteManifest
	startLocalOnlyRefresh := a.cfg.Enabled && !a.cfg.AllowRemoteManifest
	if startLoop {
		a.loopCtx, a.loopCancel = context.WithCancel(ctx)
		a.loopDone = make(chan struct{})
	}
	loopCtx := a.loopCtx
	loopDone := a.loopDone
	a.mu.Unlock()

	if startLoop {
		go a.refreshLoop(loopCtx, loopDone)
	} else if startLocalOnlyRefresh {
		// Local-only (AllowRemoteManifest=false): the periodic loop won't run, so
		// user playlist channels would never enumerate beyond the cache-only
		// startup snapshot. Kick ONE background catalog refresh — it rebuilds the
		// direct + user inline catalogs (live yt-dlp enumeration) and skips the
		// remote-fetch branch (refresh.go). Serve-stale/cache keeps it cheap.
		// Resolves the Phase 4 documented residual.
		go func() { _ = a.refreshCatalogsDefault(ctx, nil, "startup-local") }()
	}
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	cancel := a.loopCancel
	done := a.loopDone
	coreManager := a.core
	a.loopCtx = nil
	a.loopCancel = nil
	a.loopDone = nil
	a.state = adapters.StateStopped
	a.lastErr = ""
	a.stateSince = time.Now()
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	a.playbackMu.Lock()
	defer a.playbackMu.Unlock()

	a.mu.Lock()
	ref := activeAdapterRef(a.active)
	hadActive := a.active != nil
	if hadActive {
		a.clearActiveLocked()
	}
	a.mu.Unlock()

	if hadActive && coreManager != nil && ref != "" {
		_, err := coreManager.StopIfAdapterRef(ref)
		return err
	}
	return nil
}

func (a *Adapter) Status() adapters.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return adapters.Status{
		State:     a.state,
		LastError: a.lastErr,
		Since:     a.stateSince,
	}
}

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg, err := decodeConfig(raw, meta)
	if err != nil {
		return 0, fmt.Errorf("streams: decode apply config: %w", err)
	}
	if err := newCfg.Validate(); err != nil {
		return 0, err
	}

	defs, catalogs, err := buildStartupSnapshot(context.Background(), newCfg, a.cacheDir, a.userStore.Snapshot())
	if err != nil {
		return 0, err
	}

	a.mu.Lock()
	oldCfg := a.cfg
	a.cfg = newCfg
	a.installSnapshotLocked(defs, catalogs)
	a.mu.Unlock()

	a.reconcileRefreshLoop()
	return configChangeScope(oldCfg, newCfg), nil
}

func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	values := map[string]any{
		"enabled":                          a.cfg.Enabled,
		"manifest_url":                     a.cfg.ManifestURL,
		"manifest_refresh_hours":           a.cfg.ManifestRefreshHours,
		"catalog_refresh_hours":            a.cfg.CatalogRefreshHours,
		"max_manifest_bytes":               a.cfg.MaxManifestBytes,
		"max_catalog_bytes":                a.cfg.MaxCatalogBytes,
		"max_items_per_channel":            a.cfg.MaxItemsPerChannel,
		"max_consecutive_failures":         a.cfg.MaxConsecutiveFailures,
		"manifest_request_timeout_seconds": a.cfg.ManifestRequestTimeoutSeconds,
		"catalog_request_timeout_seconds":  a.cfg.CatalogRequestTimeoutSeconds,
		"youtube_format":                   a.cfg.YoutubeFormat,
		"allow_remote_manifest":            a.cfg.AllowRemoteManifest,
		"allow_cached_remote_manifest":     a.cfg.AllowCachedRemoteManifest,
		"allow_local_manifest_urls":        a.cfg.AllowLocalManifestURLs,
		"remote_provider_allowed_hosts":    formatHostList(a.cfg.RemoteProviderAllowedHosts),
	}
	for id, provider := range a.cfg.Providers {
		values[fmt.Sprintf("providers.%s.disabled", id)] = provider.Disabled
		values[fmt.Sprintf("providers.%s.catalog_refresh_hours", id)] = provider.CatalogRefreshHours
	}
	return values
}

func decodeConfig(raw toml.Primitive, meta toml.MetaData) (Config, error) {
	cfg := DefaultConfig()
	if reflect.ValueOf(raw).IsZero() {
		return cfg, nil
	}
	wire := configToWire(cfg)
	if err := meta.PrimitiveDecode(raw, &wire); err != nil {
		return Config{}, err
	}
	return wireToConfig(wire), nil
}

func configChangeScope(oldCfg, newCfg Config) adapters.ApplyScope {
	scope := adapters.ScopeHotSwap
	if oldCfg.YoutubeFormat != newCfg.YoutubeFormat {
		scope = adapters.MaxScope(scope, adapters.ScopeRestartCast)
	}
	return scope
}

func (a *Adapter) configSnapshot() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// ConfigSnapshot returns a deep copy of the adapter's current Config.
// Maps (Providers, per-provider Channels) are independently allocated;
// the caller can mutate the returned value without affecting the live
// adapter state. Used by the chassis Catalog manager wrapper to read
// provider state for rendering and to mutate a snapshot prior to
// ApplyConfigValue.
func (a *Adapter) ConfigSnapshot() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return deepCopyConfig(a.cfg)
}

// deepCopyConfig returns a value copy of in with independently allocated
// Providers and per-provider Channels maps. Scalar fields and the
// RemoteProviderAllowedHosts slice are copied so callers cannot leak
// mutations back into the source.
func deepCopyConfig(in Config) Config {
	out := in
	if in.RemoteProviderAllowedHosts != nil {
		out.RemoteProviderAllowedHosts = append([]string(nil), in.RemoteProviderAllowedHosts...)
	}
	if in.Providers != nil {
		out.Providers = make(map[string]ProviderConfig, len(in.Providers))
		for id, pc := range in.Providers {
			pcCopy := pc
			if pc.Channels != nil {
				pcCopy.Channels = make(map[string]ChannelConfig, len(pc.Channels))
				for cid, cc := range pc.Channels {
					pcCopy.Channels[cid] = cc
				}
			}
			out.Providers[id] = pcCopy
		}
	}
	return out
}

func (a *Adapter) installSnapshotLocked(defs []ProviderDefinition, catalogs []ProviderCatalog) {
	a.definitions = map[string]ProviderDefinition{}
	a.definitionOrder = a.definitionOrder[:0]
	for _, def := range defs {
		a.definitions[def.ID] = def
		a.definitionOrder = append(a.definitionOrder, def.ID)
	}
	a.catalogs = map[string]ProviderCatalog{}
	for _, cat := range catalogs {
		a.catalogs[cat.ProviderID] = cat
	}
}

func (a *Adapter) needsStartupSnapshot() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.definitions) == 0 || len(a.catalogs) == 0
}

func (a *Adapter) ensureStartupSnapshot(ctx context.Context) error {
	if !a.needsStartupSnapshot() {
		return nil
	}
	defs, catalogs, err := a.buildStartupSnapshot(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.definitions) == 0 || len(a.catalogs) == 0 {
		a.installSnapshotLocked(defs, catalogs)
	}
	return nil
}

func (a *Adapter) buildStartupSnapshot(ctx context.Context) ([]ProviderDefinition, []ProviderCatalog, error) {
	cfg := a.configSnapshot()
	return buildStartupSnapshot(ctx, cfg, a.cacheDir, a.userStore.Snapshot())
}

func (a *Adapter) reconcileRefreshLoop() {
	a.mu.Lock()
	desired := a.state == adapters.StateRunning && a.cfg.Enabled && a.cfg.AllowRemoteManifest
	running := a.loopCancel != nil && a.loopDone != nil
	if desired == running {
		a.mu.Unlock()
		return
	}
	if !desired {
		cancel := a.loopCancel
		done := a.loopDone
		a.loopCancel = nil
		a.loopDone = nil
		a.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
		return
	}

	parent := a.loopCtx
	if parent == nil {
		parent = context.Background()
	}
	loopCtx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	a.loopCtx = loopCtx
	a.loopCancel = cancel
	a.loopDone = done
	a.mu.Unlock()
	go a.refreshLoop(loopCtx, done)
}

var buildStartupSnapshotForApplyConfigValue = buildStartupSnapshot

// ApplyConfigValue validates and rebuilds the startup snapshot before
// persisting newCfg via save (which writes the [adapters.streams]
// section to disk under the AdapterSaver mutex) and installing it as
// the live config. Validation or snapshot-rebuild failures leave both
// disk and in-memory state untouched. Returns the aggregated ApplyScope
// from configChangeScope(old, new).
//
// Save is invoked exactly once after validation and snapshot rebuild
// both succeed; in-memory install happens after save returns nil so
// disk and memory stay coherent (matches the BridgeSaver write-before-
// apply contract).
func (a *Adapter) ApplyConfigValue(newCfg Config, save func(name string, raw []byte) error) (adapters.ApplyScope, error) {
	if err := newCfg.Validate(); err != nil {
		return 0, err
	}
	defs, catalogs, err := buildStartupSnapshotForApplyConfigValue(context.Background(), newCfg, a.cacheDir, a.userStore.Snapshot())
	if err != nil {
		return 0, err
	}
	tomlBytes, err := encodeSectionTOML(newCfg)
	if err != nil {
		return 0, fmt.Errorf("streams: encode section: %w", err)
	}
	if err := save("streams", tomlBytes); err != nil {
		return 0, err
	}
	a.mu.Lock()
	oldCfg := a.cfg
	a.cfg = newCfg
	a.installSnapshotLocked(defs, catalogs)
	a.mu.Unlock()
	a.reconcileRefreshLoop()
	return configChangeScope(oldCfg, newCfg), nil
}

// StopActiveCast drops the streams adapter's active queue and stops the
// underlying core session via SessionManager.StopIfAdapterRef. It does
// NOT cancel the manifest refresh loop or change the adapter's State —
// this is intentionally a narrower operation than Stop(). Used by the
// chassis catalogManager wrapper as the RECAST runtime side effect
// when Catalog-pane saves change per-provider HLS posture.
//
// Locking discipline mirrors Adapter.Stop(): playbackMu first, then
// mu for the snapshot read and clearActiveLocked, then mu released
// before the (potentially blocking) StopIfAdapterRef call.
func (a *Adapter) StopActiveCast() error {
	a.playbackMu.Lock()
	defer a.playbackMu.Unlock()

	a.mu.Lock()
	ref := activeAdapterRef(a.active)
	hadActive := a.active != nil
	coreManager := a.core
	if hadActive {
		a.clearActiveLocked()
	}
	a.mu.Unlock()

	if hadActive && coreManager != nil && ref != "" {
		_, err := coreManager.StopIfAdapterRef(ref)
		return err
	}
	return nil
}

// encodeSectionTOML encodes a Config to the TOML bytes that belong
// inside the [adapters.streams] section. Sibling of the existing
// configToWire conversion.
func encodeSectionTOML(cfg Config) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(configToWire(cfg)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
