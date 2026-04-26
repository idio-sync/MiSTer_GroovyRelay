package url

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// SessionManager is the adapter's narrow view of core.Manager. Declared
// here (rather than importing core.Manager concretely) so play_test.go
// can inject fakes without spinning up a real core. core.Manager
// satisfies this via structural typing.
type SessionManager interface {
	StartSession(core.SessionRequest) error
	Status() core.SessionStatus
}

// resolverIface is the adapter's narrow view of *ytdlp.Resolver. Lets
// play_test.go inject a stub without spinning up exec.
type resolverIface interface {
	Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error)
}

// ytdlpProbe is the cached result of probing yt-dlp at adapter Start.
// Computed once; read-only afterward, so no mu protection needed.
type ytdlpProbe struct {
	Path    string // absolute path; empty if not found
	Version string // output of `yt-dlp --version` first line
	OK      bool   // false ⇒ adapter behaves as if YtdlpEnabled=false
}

// Adapter implements adapters.Adapter for the URL-input cast source.
// Spec: docs/specs/2026-04-25-url-adapter-design.md.
//
// Concurrency: all field reads and writes (cfg, state, lastErr,
// stateSince, lastURL) go through mu. Status() and OnStop's mutator
// share the same lock so the panel fragment never observes a torn read.
type Adapter struct {
	core SessionManager

	// cookiesPath is computed once from cfg.Bridge.DataDir at New()
	// and is read-only thereafter — does not need mu.
	cookiesPath string

	// resolver is the yt-dlp resolver. Nil-tolerant: the play handler
	// returns 500 if it tries to invoke a nil resolver. Set in
	// Adapter.Start() when ytdlpProbe.OK is true; left nil otherwise.
	resolver resolverIface

	// ytdlpProbe is the cached yt-dlp version + path. Set in
	// Adapter.Start(). Surfaced in the panel via renderPanel.
	ytdlpProbe ytdlpProbe

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time
	lastURL    string // last URL handed to StartSession; surfaced in the panel
}

// AdapterConfig bundles the bridge-level context the URL adapter
// needs. The [adapters.url] TOML section flows through separately via
// DecodeConfig into Adapter.cfg — AdapterConfig carries only the
// adapter-agnostic pieces (bridge data_dir for the cookies file,
// session manager for StartSession).
//
// Mirrors the Plex AdapterConfig pattern at
// internal/adapters/plex/adapter.go:22-42 — the URL adapter joined that
// pattern when it grew the cookies feature (review fix C3).
type AdapterConfig struct {
	// Bridge is a snapshot of the bridge-level config: data_dir, etc.
	// The adapter reads DataDir from here for the url_cookies.txt path.
	Bridge config.BridgeConfig
	// Core is the adapter-agnostic session manager. core.Manager
	// satisfies this via structural typing. May be nil in tests that
	// don't exercise the play handler.
	Core SessionManager
}

// New constructs a ready-to-Start Adapter from the bundled config.
// Returns an error if Bridge.DataDir is empty (the cookies path is
// derived from it; an empty value would write to a relative path
// inside the container's working directory).
func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Bridge.DataDir == "" {
		return nil, fmt.Errorf("url: AdapterConfig.Bridge.DataDir is required")
	}
	return &Adapter{
		core:        cfg.Core,
		state:       adapters.StateStopped,
		stateSince:  time.Now(),
		cookiesPath: filepath.Join(cfg.Bridge.DataDir, "url_cookies.txt"),
	}, nil
}

// CookiesPath returns the absolute path to the cookies file. Stable
// across the adapter's lifetime; computed once at construction.
func (a *Adapter) CookiesPath() string { return a.cookiesPath }

// ---- adapters.Adapter interface ----

func (a *Adapter) Name() string        { return "url" }
func (a *Adapter) DisplayName() string { return "URL" }

func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{
			Key:        "enabled",
			Label:      "Enabled",
			Help:       "Turn the URL adapter on or off. When enabled, the Play URL form below accepts http(s) media URLs.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
		},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("url: decode config: %w", err)
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
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("url: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

// Start sets state to Running and returns nil. The URL adapter has no
// goroutines or upstream registration to bring up — "running" here means
// "enabled, ready to accept POSTs," not "background work in progress."
// Spec §"Lifecycle".
func (a *Adapter) Start(ctx context.Context) error {
	a.setState(adapters.StateRunning, "")
	return nil
}

// Stop sets state to Stopped and returns nil. Does NOT stop a mid-cast
// URL session — the data plane is owned by core.Manager. To stop a live
// cast, the operator issues a bridge-wide stop or POSTs another URL.
// Spec §"Operational edges / Disable while playing".
func (a *Adapter) Stop() error {
	a.setState(adapters.StateStopped, "")
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

// SetEnabled implements ui.EnableSetter. The toggle handler at
// internal/ui/adapter.go:handleAdapterToggle calls this in sync with
// Start/Stop. Without it the toggle endpoint returns 500.
func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Enabled = v
}

// ApplyConfig diffs and applies. With only `enabled` in v1 there's no
// real diff to compute; we just store the new value and return
// ScopeHotSwap. (`enabled` is handled out-of-band by the toggle endpoint
// per the Plex precedent.)
func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &newCfg); err != nil {
		return 0, fmt.Errorf("url: decode apply config: %w", err)
	}
	if err := newCfg.Validate(); err != nil {
		a.setState(adapters.StateError, err.Error())
		return 0, err
	}
	a.mu.Lock()
	a.cfg = newCfg
	a.mu.Unlock()
	return adapters.ScopeHotSwap, nil
}

// CurrentValues implements ui.ValueProvider via duck-typing — surfaces
// the current cfg values to the UI for form prefill.
func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"enabled": a.cfg.Enabled}
}

// setState atomically updates state, stateSince, and lastErr.
func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.stateSince = time.Now()
	a.lastErr = errMsg
}
