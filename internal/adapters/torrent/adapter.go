package torrent

import (
	"context"
	"fmt"
	"html/template"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

const torrentAdapterName = "torrent"

// SessionManager is the adapter's narrow view of core.Manager.
type SessionManager interface {
	StartSession(core.SessionRequest) error
	Status() core.SessionStatus
	Stop() error
}

// ClientFactory creates the torrent client. Start deliberately does not
// invoke it in this skeleton; later tasks will bind it to play requests.
type ClientFactory func(ClientConfig) (TorrentClient, error)

// AdapterConfig carries bridge-level context into the torrent adapter.
type AdapterConfig struct {
	Bridge        config.BridgeConfig
	Core          SessionManager
	ClientFactory ClientFactory
	EventLog      *eventlog.Log
}

// Adapter implements adapters.Adapter for torrent-backed casts.
type Adapter struct {
	core     SessionManager
	bridge   config.BridgeConfig
	factory  ClientFactory
	eventLog *eventlog.Log

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time
	client     TorrentClient

	sessions    map[string]*Session
	torrents    map[string]*torrentUse
	activeToken string
}

func New(cfg AdapterConfig) (*Adapter, error) {
	if strings.TrimSpace(cfg.Bridge.DataDir) == "" {
		return nil, fmt.Errorf("torrent: AdapterConfig.Bridge.DataDir is required")
	}
	factory := cfg.ClientFactory
	if factory == nil {
		factory = newRealClient
	}
	return &Adapter{
		core:       cfg.Core,
		bridge:     cfg.Bridge,
		factory:    factory,
		eventLog:   cfg.EventLog,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: time.Now(),
		sessions:   map[string]*Session{},
		torrents:   map[string]*torrentUse{},
	}, nil
}

func (a *Adapter) Name() string        { return torrentAdapterName }
func (a *Adapter) DisplayName() string { return "Torrent" }

func (a *Adapter) Fields() []adapters.FieldDef {
	return torrentFields()
}

func torrentFields() []adapters.FieldDef {
	defaults := DefaultConfig()
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: defaults.Enabled, ApplyScope: adapters.ScopeHotSwap},
		{Key: "traffic_acknowledged", Label: "Traffic Acknowledged", Kind: adapters.KindBool, Default: defaults.TrafficAcknowledged, ApplyScope: adapters.ScopeHotSwap},
		{
			Key:        "download_dir",
			Label:      "Download Directory",
			Help:       "Directory where torrent cache data is stored. Empty uses bridge data_dir/groovyrelay-torrent.",
			Kind:       adapters.KindText,
			Default:    defaults.DownloadDir,
			ApplyScope: adapters.ScopeRestartCast,
		},
		{Key: "keep_completed", Label: "Keep Completed", Kind: adapters.KindBool, Default: defaults.KeepCompleted, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_cache_bytes", Label: "Max Cache Bytes", Kind: adapters.KindInt, Default: defaults.MaxCacheBytes, ApplyScope: adapters.ScopeHotSwap},
		{Key: "metadata_timeout_seconds", Label: "Metadata Timeout Seconds", Kind: adapters.KindInt, Default: defaults.MetadataTimeoutSeconds, ApplyScope: adapters.ScopeHotSwap},
		{Key: "startup_buffer_seconds", Label: "Startup Buffer Seconds", Kind: adapters.KindInt, Default: defaults.StartupBufferSeconds, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_upload_rate_kbps", Label: "Max Upload Rate Kbps", Kind: adapters.KindInt, Default: defaults.MaxUploadRateKbps, ApplyScope: adapters.ScopeRestartCast},
		{Key: "max_download_rate_kbps", Label: "Max Download Rate Kbps", Kind: adapters.KindInt, Default: defaults.MaxDownloadRateKbps, ApplyScope: adapters.ScopeRestartCast},
		{Key: "listen_port", Label: "Listen Port", Kind: adapters.KindInt, Default: defaults.ListenPort, ApplyScope: adapters.ScopeRestartCast},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("torrent: decode config: %w", err)
	}
	if err := validateConfig(cfg, a.bridge.DataDir); err != nil {
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
		return fmt.Errorf("torrent: decode config: %w", err)
	}
	return validateConfig(cfg, a.bridge.DataDir)
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
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()

	if client != nil {
		if err := client.Close(); err != nil {
			a.setState(adapters.StateError, err.Error())
			return err
		}
	}
	a.mu.Lock()
	a.client = nil
	a.sessions = map[string]*Session{}
	a.torrents = map[string]*torrentUse{}
	a.activeToken = ""
	a.mu.Unlock()
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

func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Enabled = v
}

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &newCfg); err != nil {
		return 0, fmt.Errorf("torrent: decode apply config: %w", err)
	}
	if err := validateConfig(newCfg, a.bridge.DataDir); err != nil {
		return 0, err
	}

	a.mu.Lock()
	scope := configChangeScope(a.cfg, newCfg)
	a.cfg = newCfg
	a.mu.Unlock()
	return scope, nil
}

func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":                  a.cfg.Enabled,
		"traffic_acknowledged":     a.cfg.TrafficAcknowledged,
		"download_dir":             a.cfg.DownloadDir,
		"keep_completed":           a.cfg.KeepCompleted,
		"max_cache_bytes":          a.cfg.MaxCacheBytes,
		"metadata_timeout_seconds": a.cfg.MetadataTimeoutSeconds,
		"startup_buffer_seconds":   a.cfg.StartupBufferSeconds,
		"max_upload_rate_kbps":     a.cfg.MaxUploadRateKbps,
		"max_download_rate_kbps":   a.cfg.MaxDownloadRateKbps,
		"listen_port":              a.cfg.ListenPort,
	}
}

func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel())
}

func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.lastErr = errMsg
	a.stateSince = time.Now()
}

func (a *Adapter) emit(msg string) {
	if a.eventLog == nil {
		return
	}
	a.eventLog.Append(eventlog.Entry{
		Time:     time.Now(),
		Severity: eventlog.SeverityInfo,
		Source:   torrentAdapterName,
		Message:  sanitizeLogMessage(msg),
	})
}

func (a *Adapter) logSafe(msg string, args ...any) {
	if a.eventLog == nil {
		return
	}
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	a.emit(msg)
}

func sanitizeLogMessage(msg string) string {
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	return strings.TrimSpace(msg)
}

type torrentUse struct {
	torrent  TorrentHandle
	refs     int
	keepData bool
}
