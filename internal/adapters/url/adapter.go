package url

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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

// Adapter implements adapters.Adapter for the URL-input cast source.
// Spec: docs/specs/2026-04-25-url-adapter-design.md.
//
// Concurrency: all field reads and writes (cfg, state, lastErr,
// stateSince, lastURL) go through mu. Status() and OnStop's mutator
// share the same lock so the panel fragment never observes a torn read.
type Adapter struct {
	core SessionManager

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time
	lastURL    string // last URL handed to StartSession; surfaced in the panel
}

// New constructs a ready-to-Start Adapter. core may be nil for tests
// that don't exercise the play handler.
func New(coreMgr SessionManager) *Adapter {
	return &Adapter{
		core:       coreMgr,
		state:      adapters.StateStopped,
		stateSince: time.Now(),
	}
}

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
