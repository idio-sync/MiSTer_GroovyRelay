package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// AdapterConfig bundles the bridge-level context the Plex adapter
// needs. The [adapters.plex] TOML section flows through separately via
// DecodeConfig into Adapter.plexCfg — AdapterConfig carries only the
// adapter-agnostic pieces (bridge ports, session manager, token store,
// host IP, build version).
type AdapterConfig struct {
	// Bridge is a snapshot of the bridge-level config: data_dir, UI
	// port, MiSTer destination, video/audio settings. The adapter reads
	// UI.HTTPPort + DataDir from here; other fields belong to the data
	// plane / core.Manager.
	Bridge config.BridgeConfig
	// Core is the adapter-agnostic session manager. core.Manager
	// satisfies this via structural typing; declared as SessionManager
	// (in companion.go) so tests can inject fakes. Must be non-nil.
	Core SessionManager
	// TokenStore carries the persisted device UUID + plex.tv auth
	// token. When AuthToken is empty the registration loop is skipped.
	TokenStore *StoredData
	// HostIP is the LAN address advertised to plex.tv in the
	// registration loop. Empty disables registration even if a token
	// is present (typically set to outboundIP() by main.go).
	HostIP string
	// Version is the build version string spliced into /resources
	// platformVersion and X-Plex-Version headers.
	Version string
}

// Adapter owns the Plex Companion handlers, the GDM multicast
// responder, the timeline broadcaster, and the plex.tv registration
// loop. It does NOT own the HTTP listener anymore — main.go binds a
// single :http_port socket that both this adapter and the Settings
// UI attach to (design §7.1). Satisfies adapters.Adapter.
type Adapter struct {
	cfg     AdapterConfig
	plexCfg Config // typed [adapters.plex] section, populated by DecodeConfig

	stateMu    sync.Mutex
	state      adapters.State
	lastErr    string
	stateSince time.Time

	// finalizeOnce guards lazy construction of companion + timeline.
	// Either MountRoutes (called before the shared listener starts)
	// or Start (background work) may trigger finalization — Once
	// keeps it single-shot regardless of call order.
	//
	// NOTE: this makes the initial companion/timeline pair immutable.
	// A future UI-driven re-enable flow that calls Stop() then Start()
	// will NOT get a fresh TimelineBroker (TimelineBroker.Stop is
	// one-shot). Phase 5's toggle work must recreate restartable
	// pieces or refactor them to be restart-safe.
	finalizeOnce sync.Once

	companion *Companion
	timeline  *TimelineBroker

	// disco is nil if GDM init fails (e.g. port 32412 already bound
	// by another Plex player on the host). We log and continue —
	// out-of-LAN registration still works if a token is configured.
	disco *Discovery

	// regCancel cancels the plex.tv registration loop; nil when no
	// loop was started (missing token / host IP).
	regCancel context.CancelFunc

	// discoDone is closed when Discovery.Run exits so Stop can wait
	// for the goroutine cleanly. Nil when GDM discovery isn't running.
	discoDone chan struct{}
}

// NewAdapter constructs a ready-to-Start Adapter. Companion + timeline
// are NOT built here: they depend on plexCfg.DeviceName, which isn't
// populated until DecodeConfig runs. Lazy construction happens inside
// ensureFinalized (called from MountRoutes or Start).
func NewAdapter(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Core == nil {
		return nil, errors.New("plex.NewAdapter: AdapterConfig.Core is required")
	}
	return &Adapter{cfg: cfg}, nil
}

// MountRoutes mounts the Plex Companion routes on the provided mux.
// Called by main.go before the shared listener starts. The concrete
// paths are matched as catch-all prefixes — the companion's inner mux
// does the real sub-path routing to playMedia/pause/play/etc.
func (a *Adapter) MountRoutes(mux *http.ServeMux) {
	a.ensureFinalized()
	h := a.companion.Handler()
	mux.Handle("/resources", h)
	mux.Handle("/player/", h)
}

// Start brings the Plex adapter's background work online (timeline
// broadcaster, GDM discovery, plex.tv registration). The HTTP
// Companion handlers are mounted separately via MountRoutes; by the
// time Start runs the shared listener is already accepting requests.
// Returns quickly; background goroutines run until Stop.
func (a *Adapter) Start(ctx context.Context) error {
	a.ensureFinalized()
	a.setState(adapters.StateStarting, "")

	// Timeline broadcaster (1 Hz push loop). Runs until Stop closes
	// the broker's stop channel.
	go a.timeline.RunBroadcastLoop()

	// GDM multicast discovery. Best-effort: port conflicts shouldn't
	// take down the adapter — out-of-LAN registration may still succeed.
	disco, err := NewDiscovery(DiscoveryConfig{
		DeviceName: a.plexCfg.DeviceName,
		DeviceUUID: a.cfg.TokenStore.DeviceUUID,
		HTTPPort:   a.cfg.Bridge.UI.HTTPPort,
	})
	if err != nil {
		slog.Warn("GDM discovery disabled", "err", err)
	} else {
		a.disco = disco
		a.discoDone = make(chan struct{})
		go func() {
			defer close(a.discoDone)
			disco.Run()
		}()
		slog.Info("GDM discovery active", "port", 32412)
	}

	// plex.tv device registration loop. Requires a linked auth token
	// and an outbound IP to advertise; otherwise we log and skip so
	// the user knows to run `--link`.
	if a.cfg.TokenStore != nil && a.cfg.TokenStore.AuthToken != "" && a.cfg.HostIP != "" {
		regCtx, cancel := context.WithCancel(ctx)
		a.regCancel = cancel
		go RunRegistrationLoop(regCtx,
			a.cfg.TokenStore.DeviceUUID,
			a.cfg.TokenStore.AuthToken,
			a.cfg.HostIP,
			a.cfg.Bridge.UI.HTTPPort,
		)
		slog.Info("plex.tv device registration loop started", "hostIP", a.cfg.HostIP)
	} else {
		slog.Info("plex.tv registration skipped (no auth token; run with --link)")
	}

	a.setState(adapters.StateRunning, "")
	return nil
}

// Stop tears down background work in reverse-dependency order. The
// HTTP listener is owned by main.go and shut down separately. Returns
// nil on clean shutdown (interface contract).
func (a *Adapter) Stop() error {
	if a.regCancel != nil {
		a.regCancel()
	}
	if a.disco != nil {
		_ = a.disco.Close()
		if a.discoDone != nil {
			<-a.discoDone
		}
	}
	if a.timeline != nil {
		a.timeline.Stop()
	}
	a.setState(adapters.StateStopped, "")
	return nil
}

// ensureFinalized lazily constructs companion + timeline after
// DecodeConfig has populated plexCfg. Called from both MountRoutes
// (main-goroutine startup) and Start — sync.Once guarantees the
// construction runs exactly once. DeviceUUID comes from the token
// store (the bridge's identity, not a user-editable field).
func (a *Adapter) ensureFinalized() {
	a.finalizeOnce.Do(func() {
		a.companion = NewCompanion(CompanionConfig{
			DeviceName: a.plexCfg.DeviceName,
			DeviceUUID: a.cfg.TokenStore.DeviceUUID,
			Version:    a.cfg.Version,
			DataDir:    a.cfg.Bridge.DataDir,
		}, a.cfg.Core)
		a.timeline = NewTimelineBroker(
			TimelineConfig{DeviceUUID: a.cfg.TokenStore.DeviceUUID, DeviceName: a.plexCfg.DeviceName},
			a.cfg.Core.Status,
		)
		a.companion.SetTimeline(a.timeline)
	})
}

// ---- adapters.Adapter interface implementation ----

func (a *Adapter) Name() string        { return "plex" }
func (a *Adapter) DisplayName() string { return "Plex" }

// Fields declares the UI form schema for the Plex adapter (design §6.2).
func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{
			Key:        "enabled",
			Label:      "Enabled",
			Help:       "Turn the Plex adapter on or off. Disabling stops the Companion HTTP server and de-registers from plex.tv.",
			Kind:       adapters.KindBool,
			Default:    true,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "device_name",
			Label:      "Device Name",
			Help:       "Shown in the Plex cast-target list.",
			Kind:       adapters.KindText,
			Required:   true,
			Default:    "MiSTer",
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "Identity",
		},
		{
			Key:        "profile_name",
			Label:      "Profile Name",
			Help:       "Client-capability profile advertised to Plex Media Server.",
			Kind:       adapters.KindText,
			Required:   true,
			Default:    "Plex Home Theater",
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "Identity",
		},
		{
			Key:         "server_url",
			Label:       "Pin Server URL",
			Help:        "Optional: pin a specific PMS (http://host:32400) instead of GDM auto-discovery.",
			Kind:        adapters.KindText,
			ApplyScope:  adapters.ScopeRestartCast,
			Placeholder: "auto-discover",
			Section:     "Server",
		},
	}
}

// DecodeConfig hydrates a.plexCfg from the TOML primitive. Called by
// main.go (registry wiring) between parse and MountRoutes/Start so
// downstream paths see a fully-populated plexCfg.
func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("plex: decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.plexCfg = cfg
	return nil
}

func (a *Adapter) IsEnabled() bool { return a.plexCfg.Enabled }

func (a *Adapter) Status() adapters.Status {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return adapters.Status{
		State:     a.state,
		LastError: a.lastErr,
		Since:     a.stateSince,
	}
}

func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.state = s
	a.stateSince = time.Now()
	a.lastErr = errMsg
}

// ApplyConfig is stubbed here — Phase 7 implements real diff + per-
// field dispatch. For now a successful decode + validate is taken as
// a ScopeHotSwap change.
func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &newCfg); err != nil {
		return 0, err
	}
	if err := newCfg.Validate(); err != nil {
		return 0, err
	}
	a.plexCfg = newCfg
	return adapters.ScopeHotSwap, nil
}
