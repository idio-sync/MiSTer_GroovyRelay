package localfiles

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

const adapterName = "localfiles"

var _ adapters.Adapter = (*Adapter)(nil)
var _ adapters.Validator = (*Adapter)(nil)

type AdapterConfig struct {
	Bridge  config.BridgeConfig
	Core    SessionManager
	FFprobe BinaryResolver
}

type SessionManager interface {
	StartSession(core.SessionRequest) error
}

type BinaryResolver interface {
	Resolve() (string, error)
}

type probeFunc func(context.Context, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error)

type Adapter struct {
	core    SessionManager
	bridge  config.BridgeConfig
	ffprobe BinaryResolver

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time
	probe      probeFunc
}

func New(cfg AdapterConfig) (*Adapter, error) {
	a := &Adapter{
		core:       cfg.Core,
		bridge:     cfg.Bridge,
		ffprobe:    cfg.FFprobe,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: time.Now(),
	}
	a.probe = a.probeDefault
	return a, nil
}

func (a *Adapter) Name() string        { return adapterName }
func (a *Adapter) DisplayName() string { return "Local Files" }

func (a *Adapter) Fields() []adapters.FieldDef {
	defaults := DefaultConfig()
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: defaults.Enabled, ApplyScope: adapters.ScopeHotSwap},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("localfiles: decode config: %w", err)
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
		return fmt.Errorf("localfiles: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

func (a *Adapter) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.setState(adapters.StateRunning, "")
	return nil
}

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

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return 0, fmt.Errorf("localfiles: decode apply config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return 0, err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return adapters.ScopeHotSwap, nil
}

func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.lastErr = errMsg
	a.stateSince = time.Now()
}

func (a *Adapter) configSnapshot() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.cfg
	if a.cfg.Libraries != nil {
		out.Libraries = append([]Library(nil), a.cfg.Libraries...)
	}
	return out
}

func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"enabled": a.cfg.Enabled}
}

func (a *Adapter) CurrentLibraries() []Library {
	return a.configSnapshot().Libraries
}

func (a *Adapter) probeDefault(ctx context.Context, url string, policy ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
	if a.ffprobe == nil {
		return nil, fmt.Errorf("localfiles: ffprobe resolver is not configured")
	}
	bin, err := a.ffprobe.Resolve()
	if err != nil {
		return nil, err
	}
	return ffmpeg.Probe(ctx, bin, url, policy)
}
