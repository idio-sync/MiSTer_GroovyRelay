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

// Adapter owns the lifecycle of every Plex subsystem: the Companion
// HTTP server, the GDM multicast responder, the timeline broadcaster,
// and the plex.tv registration loop. Satisfies adapters.Adapter.
type Adapter struct {
	cfg     AdapterConfig
	plexCfg Config // typed [adapters.plex] section, populated by DecodeConfig

	stateMu    sync.Mutex
	state      adapters.State
	lastErr    string
	stateSince time.Time

	companion *Companion
	timeline  *TimelineBroker

	// disco is nil if GDM init fails (e.g. port 32412 already bound by
	// another Plex player on the host). We log and continue — out-of-LAN
	// registration still works if a token is configured.
	disco *Discovery

	httpSrv *http.Server
	srvWG   sync.WaitGroup

	// regCancel cancels the plex.tv registration loop; nil when no
	// loop was started (missing token / host IP).
	regCancel context.CancelFunc

	// discoDone is closed when Discovery.Run exits so Stop can wait
	// for the goroutine cleanly. Nil when GDM discovery isn't running.
	discoDone chan struct{}
}

// NewAdapter constructs a ready-to-Start Adapter. Companion + timeline
// are NOT built here: they depend on plexCfg.DeviceName, which isn't
// populated until DecodeConfig runs. Construction defers to Start.
func NewAdapter(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Core == nil {
		return nil, errors.New("plex.NewAdapter: AdapterConfig.Core is required")
	}
	return &Adapter{cfg: cfg}, nil
}

// Start brings the Plex adapter online. Order: HTTP server first (so
// any immediate discovery reply lands on a live port), then timeline
// broadcaster, then GDM discovery, then optional plex.tv registration.
// Returns quickly; background goroutines run until Stop.
func (a *Adapter) Start(ctx context.Context) error {
	a.setState(adapters.StateStarting, "")

	// Construct companion + timeline now that config is decoded. The
	// DeviceUUID comes from the token store (populated at first boot)
	// rather than plexCfg — it's the bridge's identity, not a user-
	// editable field.
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

	// 1. HTTP server for Plex Companion endpoints.
	addr := fmt.Sprintf(":%d", a.cfg.Bridge.UI.HTTPPort)
	a.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           a.companion.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	a.srvWG.Add(1)
	go func() {
		defer a.srvWG.Done()
		if err := a.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("plex HTTP server exited", "err", err)
			a.setState(adapters.StateError, err.Error())
		}
	}()
	slog.Info("plex Companion listening", "addr", addr)

	// 2. Timeline broadcaster (1 Hz push loop). Runs until Stop closes
	// the broker's stop channel.
	go a.timeline.RunBroadcastLoop()

	// 3. GDM multicast discovery. Best-effort: port conflicts shouldn't
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

	// 4. plex.tv device registration loop. Requires a linked auth
	// token and an outbound IP to advertise; otherwise we log and
	// skip so the user knows to run `--link`.
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

// Stop tears everything down in reverse-dependency order and blocks
// until every background goroutine has exited. Returns nil on clean
// shutdown (interface contract); the error return is reserved for
// future adapters that need to surface cleanup failures.
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
	if a.httpSrv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.httpSrv.Shutdown(shutCtx)
		a.srvWG.Wait()
	}
	a.setState(adapters.StateStopped, "")
	return nil
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
// main.go (registry wiring) between parse and Start so Start can see
// a fully-populated plexCfg.
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
