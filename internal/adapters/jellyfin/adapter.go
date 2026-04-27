package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// SessionManager is the narrow view of core.Manager the JF adapter
// uses. Declared in this package (not core) so commands_test.go and
// playback_test.go can inject fakes. core.Manager satisfies this via
// structural typing.
type SessionManager interface {
	StartSession(req core.SessionRequest) error
	Pause() error
	Play() error
	Stop() error
	SeekTo(offsetMs int) error
	Status() core.SessionStatus
}

// Adapter implements adapters.Adapter for the Jellyfin cast-target
// integration. Concurrency: every read/write of cfg, state, lastErr,
// stateSince, link, queue, currentRefKey, pendingRollback, ws, and
// reporters goes through mu. The mu is NEVER held during network I/O
// — see internal/core/CLAUDE.md §"Invariants" for the discipline.
type Adapter struct {
	core     SessionManager
	dataDir  string // bridge data_dir; tokens go under <dataDir>/jellyfin/token.json
	deviceID string // bridge.device_uuid, reused across protocols

	mu              sync.Mutex
	cfg             Config
	state           adapters.State
	lastErr         string
	stateSince      time.Time
	link            *LinkState
	currentRefKey   string           // packed "<itemId>:<playSessionId>" for self-preempt elision
	pendingRollback string           // saved currentRefKey for StartSession-failure rollback
	queue           []QueuedItem     // adapter-local FIFO for PlayNext / PlayLast
	reporters       map[string]*reporter // refKey → reporter; populated in Phase 7
	ws              wsConn           // populated in Phase 4
	outboundCh      chan outboundEnvelope
	keepaliveSet    chan time.Duration
	pendingBuf      *ringBuffer // adapter-level drop-oldest queue used while outboundCh is nil (WS down); drained by runOneConn on reconnect
	// handleInbound routes inbound JF WS messages by MessageType.
	// Set by New() to a.dispatchInbound; tests swap freely before
	// startWS is called.
	handleInbound inboundDispatcher
	// startCancel is set in Phase 4 when Start() spawns the WS goroutines.
	// runDone is closed by the runSession goroutine when it returns;
	// Stop() waits on it so Start→Stop→Start cannot double-post Capabilities.
	startCancel context.CancelFunc
	runDone     chan struct{}
}

// QueuedItem is an item enqueued via PlayNext / PlayLast. The fields
// match what's needed to call PlaybackInfo when the item's turn arrives.
// Defined here so config_test / adapter_interface_test compile cleanly;
// populated in Phase 6 (Task 6.4 — Queue).
type QueuedItem struct {
	ItemID              string
	StartPositionTicks  int64
	MediaSourceID       string
	AudioStreamIndex    *int // pointer because 0 is meaningful
	SubtitleStreamIndex *int
}

// reporter is the per-session progress-reporting goroutine. Populated
// in Phase 7. Defined as an empty struct here so adapter.go compiles.
type reporter struct {
	// fields added in Task 7.1
}

// wsConn is the package-local interface over a JF WebSocket
// connection. Populated in Phase 4.
type wsConn interface {
	Close() error
}

// inboundDispatcher routes a parsed JF inbound WS message envelope.
// Wired by New() to a.dispatchInbound (Task 6.1); tests may swap it.
type inboundDispatcher func(messageType string, data json.RawMessage)

// New constructs a JF adapter bound to a SessionManager (typically
// *core.Manager), the bridge data_dir, and the bridge device UUID.
// core may be nil for tests that don't exercise StartSession.
func New(coreMgr SessionManager, dataDir, deviceID string) *Adapter {
	return &Adapter{
		core:       coreMgr,
		dataDir:    dataDir,
		deviceID:   deviceID,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: time.Now(),
		link:       NewLinkState(),
		reporters:  map[string]*reporter{},
	}
}

// tokenPath is the absolute path to the persisted JF token file.
func (a *Adapter) tokenPath() string {
	return filepath.Join(a.dataDir, "jellyfin", "token.json")
}

// ---- adapters.Adapter interface ----

func (a *Adapter) Name() string        { return "jellyfin" }
func (a *Adapter) DisplayName() string { return "Jellyfin" }

func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{
			Key:        "enabled",
			Label:      "Enabled",
			Help:       "Turn the Jellyfin adapter on or off. Requires a successful link first.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:         "server_url",
			Label:       "Server URL",
			Help:        "Base URL of the Jellyfin server (e.g. https://jellyfin.example.com). No path or query string.",
			Kind:        adapters.KindText,
			Default:     "",
			ApplyScope:  adapters.ScopeRestartBridge,
			Required:    true,
			Placeholder: "https://jellyfin.example.com",
		},
		{
			Key:        "device_name",
			Label:      "Device Name",
			Help:       "Name shown in JF clients' Cast menu. Blank inherits bridge.device_name.",
			Kind:       adapters.KindText,
			Default:    "",
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "max_video_bitrate_kbps",
			Label:      "Max video bitrate (kbps)",
			Help:       "Cap on the requested transcode bitrate. JF will adapt down for low-bandwidth clients.",
			Kind:       adapters.KindInt,
			Default:    4000,
			ApplyScope: adapters.ScopeRestartCast,
		},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("jellyfin: decode config: %w", err)
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
		return fmt.Errorf("jellyfin: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

// SetEnabled implements ui.EnableSetter. The toggle handler at
// internal/ui/adapter.go:handleAdapterToggle calls this in sync with
// Start/Stop. Without it the toggle endpoint returns 500.
func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Enabled = v
}

// CurrentValues implements ui.ValueProvider via duck-typing — surfaces
// the current cfg values to the UI for form prefill.
func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":                a.cfg.Enabled,
		"server_url":             a.cfg.ServerURL,
		"device_name":            a.cfg.DeviceName,
		"max_video_bitrate_kbps": a.cfg.MaxVideoBitrateKbps,
	}
}

// Start is implemented in Phase 5 (Task 5.3). For now it returns an
// error so accidental calls fail loudly during incremental development.
func (a *Adapter) Start(ctx context.Context) error {
	return fmt.Errorf("jellyfin.Adapter.Start: not implemented yet (Phase 5)")
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	cancel := a.startCancel
	done := a.runDone
	a.startCancel = nil
	a.runDone = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Wait for runSession to exit so Start→Stop→Start can't double-post
	// Capabilities. 10 s upper bound keeps the UI thread responsive even
	// if a network call is wedged; in practice runSession exits quickly
	// once its context is cancelled.
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
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

// ApplyConfig diffs old vs new cfg and returns the maximum scope across
// changed fields. Mirrors Plex's ApplyConfig discipline.
func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &newCfg); err != nil {
		return 0, fmt.Errorf("jellyfin: decode apply config: %w", err)
	}
	if err := newCfg.Validate(); err != nil {
		return 0, err
	}

	a.mu.Lock()
	old := a.cfg
	a.cfg = newCfg
	a.mu.Unlock()

	scope := adapters.ScopeHotSwap
	if old.ServerURL != newCfg.ServerURL {
		scope = adapters.MaxScope(scope, adapters.ScopeRestartBridge)
	}
	if old.MaxVideoBitrateKbps != newCfg.MaxVideoBitrateKbps {
		scope = adapters.MaxScope(scope, adapters.ScopeRestartCast)
	}
	// device_name is ScopeHotSwap (already covered by initial value).
	return scope, nil
}

// setState atomically updates state, stateSince, and lastErr.
func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.stateSince = time.Now()
	a.lastErr = errMsg
	if s == adapters.StateError && errMsg != "" {
		slog.Error("jellyfin adapter error", "err", errMsg)
	}
}
