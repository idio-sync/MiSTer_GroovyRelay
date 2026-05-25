package aux

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

type SessionManager interface {
	StartSession(core.SessionRequest) error
	StopIfAdapterRef(string) (bool, error)
	Status() core.SessionStatus
}

type AdapterConfig struct {
	Bridge   config.BridgeConfig
	Core     SessionManager
	HTTPPort int
	EventLog *eventlog.Log
}

type Adapter struct {
	core          SessionManager
	bridge        config.BridgeConfig
	httpPort      int
	eventLog      *eventlog.Log
	now           func() time.Time
	mu            sync.Mutex
	cfg           Config
	state         adapters.State
	lastErr       string
	stateSince    time.Time
	activeRef     string
	activeGen     uint64
	activeCleanup func()
	enableErr     error
	proxy         proxyStore
	proxyHTTP     *http.Client
}

func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Core == nil {
		return nil, fmt.Errorf("aux: AdapterConfig.Core is required")
	}
	now := time.Now
	return &Adapter{
		core:       cfg.Core,
		bridge:     cfg.Bridge,
		httpPort:   cfg.HTTPPort,
		eventLog:   cfg.EventLog,
		now:        now,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: now(),
		proxy:      proxyStore{now: now},
		proxyHTTP:  newProxyHTTPClient(),
	}, nil
}

func (a *Adapter) Name() string        { return "aux" }
func (a *Adapter) DisplayName() string { return "AUX" }

func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: false, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.id", Label: "Input ID", Kind: adapters.KindText, Default: "aux", Required: true, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.name", Label: "Name", Kind: adapters.KindText, Default: "AUX", Required: true, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.mode", Label: "Mode", Kind: adapters.KindEnum, Enum: []string{ModeStreamURL, ModeLocalCapture}, Default: ModeStreamURL, Required: true, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.audio_output", Label: "Audio Output", Kind: adapters.KindEnum, Enum: []string{AudioOutputVisualOnly, AudioOutputMonitor}, Default: AudioOutputVisualOnly, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.url", Label: "Stream URL", Kind: adapters.KindText, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.format", Label: "Capture Format", Kind: adapters.KindText, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.device", Label: "Capture Device", Kind: adapters.KindText, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.sample_rate", Label: "Sample Rate", Kind: adapters.KindInt, Default: a.bridge.Audio.SampleRate, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.channels", Label: "Channels", Kind: adapters.KindInt, Default: a.bridge.Audio.Channels, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.thread_queue_size", Label: "Thread Queue", Kind: adapters.KindInt, Default: 64, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.analyze_duration_ms", Label: "Analyze Duration", Kind: adapters.KindInt, Default: 100, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.probe_size", Label: "Probe Size", Kind: adapters.KindInt, Default: 32768, ApplyScope: adapters.ScopeHotSwap},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("aux: decode config: %w", err)
	}
	cfg = a.normalizeConfig(cfg)
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.enableErr = nil
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("aux: decode config: %w", err)
	}
	cfg = a.normalizeConfig(cfg)
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

func (a *Adapter) Start(context.Context) error {
	a.mu.Lock()
	cfg := a.normalizeConfig(a.cfg)
	enableErr := a.enableErr
	a.mu.Unlock()
	if enableErr != nil {
		return enableErr
	}
	if !cfg.Enabled {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		a.setState(adapters.StateError, fmt.Sprintf("validation failed: %v", err))
		return err
	}
	a.setState(adapters.StateRunning, "")
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	ref := a.activeRef
	cleanup := a.activeCleanup
	a.activeRef = ""
	a.activeGen = 0
	a.activeCleanup = nil
	a.state = adapters.StateStopped
	a.lastErr = ""
	a.stateSince = a.now()
	coreManager := a.core
	a.mu.Unlock()

	if cleanup != nil {
		cleanup()
	}
	if ref != "" && coreManager != nil {
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

func (a *Adapter) SetEnabled(v bool) {
	if !v {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.cfg.Enabled = false
		a.enableErr = nil
		a.lastErr = ""
		if a.state == adapters.StateError {
			a.state = adapters.StateStopped
			a.stateSince = a.now()
		}
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.normalizeConfig(a.cfg)
	next.Enabled = true
	if err := next.Validate(); err != nil {
		a.cfg.Enabled = false
		a.state = adapters.StateError
		a.enableErr = fmt.Errorf("validation failed: %w", err)
		a.lastErr = a.enableErr.Error()
		a.stateSince = a.now()
		return
	}
	a.cfg = next
	a.enableErr = nil
	a.lastErr = ""
}

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg, err := decodeConfig(raw, meta)
	if err != nil {
		return 0, fmt.Errorf("aux: decode apply config: %w", err)
	}
	newCfg = a.normalizeConfig(newCfg)
	if err := newCfg.Validate(); err != nil {
		return 0, err
	}

	a.mu.Lock()
	a.cfg = newCfg
	if a.enableErr != nil {
		a.enableErr = nil
		a.lastErr = ""
		if a.state == adapters.StateError {
			a.state = adapters.StateStopped
			a.stateSince = a.now()
		}
	}
	a.mu.Unlock()
	return adapters.ScopeHotSwap, nil
}

func (a *Adapter) normalizeConfig(cfg Config) Config {
	if cfg.Input.Mode != ModeLocalCapture {
		return cfg
	}
	if cfg.Input.SampleRate == 0 {
		cfg.Input.SampleRate = a.bridge.Audio.SampleRate
	}
	if cfg.Input.Channels == 0 {
		cfg.Input.Channels = a.bridge.Audio.Channels
	}
	return cfg
}

func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":                   a.cfg.Enabled,
		"input.id":                  a.cfg.Input.ID,
		"input.name":                a.cfg.Input.Name,
		"input.mode":                a.cfg.Input.Mode,
		"input.audio_output":        a.cfg.Input.AudioOutput,
		"input.url":                 a.cfg.Input.URL,
		"input.format":              a.cfg.Input.Format,
		"input.device":              a.cfg.Input.Device,
		"input.sample_rate":         a.cfg.Input.SampleRate,
		"input.channels":            a.cfg.Input.Channels,
		"input.thread_queue_size":   a.cfg.Input.ThreadQueueSize,
		"input.analyze_duration_ms": a.cfg.Input.AnalyzeDurationMillis,
		"input.probe_size":          a.cfg.Input.ProbeSize,
	}
}

func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.lastErr = errMsg
	a.stateSince = a.now()
}

func decodeConfig(raw toml.Primitive, meta toml.MetaData) (Config, error) {
	cfg := DefaultConfig()
	if reflect.ValueOf(raw).IsZero() {
		return cfg, nil
	}
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
