package streams

import (
	"context"
	"fmt"
	"html/template"
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
)

type AdapterConfig struct {
	Bridge        config.BridgeConfig
	Core          SessionManager
	YTDLPResolver ytdlp.BinaryResolver
}

type SessionManager interface {
	StartSession(core.SessionRequest) error
	PauseIfAdapterRef(string) (bool, error)
	StopIfAdapterRef(string) (bool, error)
	Status() core.SessionStatus
}

type streamResolver interface {
	Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error)
}

const defaultYTDLPResolveTimeout = 30 * time.Second

type Adapter struct {
	core        SessionManager
	cookiesPath string
	ytdlpBinary ytdlp.BinaryResolver
	resolver    streamResolver

	fetchManifest func(context.Context) (Manifest, CacheMetadata, error)
	refreshOnce   func(ctx context.Context, reason string) RefreshStatus

	// Test seam for deterministic stale-continuation interleaving coverage.
	beforeQueueContinuation func()
	// Test seam for deterministic StopQueue/OnStop interleaving coverage.
	beforeStopQueuePlaybackLock func(queueCapture)
	playbackMu                  sync.Mutex
	mu                          sync.Mutex
	cfg                         Config
	state                       adapters.State
	lastErr                     string
	stateSince                  time.Time
	rng                         *rand.Rand
	definitions                 map[string]ProviderDefinition
	definitionOrder             []string
	catalogs                    map[string]ProviderCatalog
	active                      *ActiveQueue

	loopCtx    context.Context
	loopCancel context.CancelFunc
	loopDone   chan struct{}
}

func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Bridge.DataDir == "" {
		return nil, fmt.Errorf("streams: AdapterConfig.Bridge.DataDir is required")
	}
	a := &Adapter{
		core:        cfg.Core,
		cookiesPath: filepath.Join(cfg.Bridge.DataDir, "streams_cookies.txt"),
		ytdlpBinary: cfg.YTDLPResolver,
		cfg:         DefaultConfig(),
		state:       adapters.StateStopped,
		stateSince:  time.Now(),
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
		definitions: map[string]ProviderDefinition{},
		catalogs:    map[string]ProviderCatalog{},
	}
	a.refreshOnce = func(context.Context, string) RefreshStatus { return RefreshStatus{} }
	return a, nil
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
	var resolver streamResolver
	if a.ytdlpBinary != nil {
		resolver = &ytdlp.Resolver{
			BinaryResolver: a.ytdlpBinary,
			Timeout:        defaultYTDLPResolveTimeout,
			Runner:         ytdlp.OSRunner{},
		}
	}

	a.mu.Lock()
	a.resolver = resolver
	a.loopCtx = ctx
	a.state = adapters.StateRunning
	a.lastErr = ""
	a.stateSince = time.Now()
	a.mu.Unlock()
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

	a.mu.Lock()
	oldCfg := a.cfg
	a.cfg = newCfg
	a.mu.Unlock()

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

func (a *Adapter) UIRoutes() []adapters.Route {
	return nil
}

func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML("")
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
