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
//
// Locking discipline (review fix C2):
//
//	mu guards plexCfg, state/lastErr/stateSince, pending, regCancel,
//	and the in-memory auth-token field on cfg.TokenStore. Hold mu for
//	short critical sections only — never across network I/O. Snapshot
//	helpers (snapshotCfg, snapshotPending) copy the fields out so
//	callers can render/compare without the lock held.
//
// companion/timeline/disco/discoDone are assigned once (during
// ensureFinalized / Start) from a single goroutine and read-only
// thereafter, so they don't need mu.
type Adapter struct {
	cfg AdapterConfig

	mu         sync.Mutex
	plexCfg    Config // typed [adapters.plex] section, populated by DecodeConfig
	state      adapters.State
	lastErr    string
	stateSince time.Time
	regCancel  context.CancelFunc // cancels the plex.tv registration loop; nil when inactive
	pending    *pendingLink       // in-flight PIN flow; nil between flows

	// linkStartMu serializes handleLinkStart so two rapid clicks can't
	// interleave RequestPIN calls. Separate from mu because RequestPIN
	// is a network RTT and we don't want to block sidebar polls.
	linkStartMu sync.Mutex

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

	// discoDone is closed when Discovery.Run exits so Stop can wait
	// for the goroutine cleanly. Nil when GDM discovery isn't running.
	discoDone chan struct{}
}

// snapshotCfg returns a copy of plexCfg taken under mu. Callers render
// or compare against the snapshot without holding the lock.
func (a *Adapter) snapshotCfg() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.plexCfg
}

// snapshotToken returns the current in-memory AuthToken under mu.
// Empty string when TokenStore is nil or unlinked.
func (a *Adapter) snapshotToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.TokenStore == nil {
		return ""
	}
	return a.cfg.TokenStore.AuthToken
}

// snapshotPending returns the current pendingLink pointer under mu.
// The returned pointer may be nil. pendingLink has its own mutex so
// callers can read its state without holding a.mu.
func (a *Adapter) snapshotPending() *pendingLink {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending
}

// NewAdapter constructs a ready-to-Start Adapter. Companion + timeline
// are NOT built here: they depend on plexCfg.DeviceName, which isn't
// populated until DecodeConfig runs. Lazy construction happens inside
// ensureFinalized (called from MountRoutes or Start).
//
// TokenStore is required (review fix I3): the linking handlers and
// ensureFinalized both dereference it, and a nil store would surface
// as a runtime panic the first time an operator clicks "Link Plex
// Account". Fail fast here instead.
func NewAdapter(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Core == nil {
		return nil, errors.New("plex.NewAdapter: AdapterConfig.Core is required")
	}
	if cfg.TokenStore == nil {
		return nil, errors.New("plex.NewAdapter: AdapterConfig.TokenStore is required")
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

	// Snapshot the fields we need under mu so we don't hold the lock
	// across network construction or goroutine launches.
	cfgSnap := a.snapshotCfg()
	deviceUUID := a.cfg.TokenStore.DeviceUUID // TokenStore guaranteed non-nil by NewAdapter
	authToken := a.snapshotToken()

	// Timeline broadcaster (1 Hz push loop). Runs until Stop closes
	// the broker's stop channel.
	go a.timeline.RunBroadcastLoop()

	// GDM multicast discovery. Best-effort: port conflicts shouldn't
	// take down the adapter — out-of-LAN registration may still succeed.
	disco, err := NewDiscovery(DiscoveryConfig{
		DeviceName: cfgSnap.DeviceName,
		DeviceUUID: deviceUUID,
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
	if authToken != "" && a.cfg.HostIP != "" {
		regCtx, cancel := context.WithCancel(ctx)
		a.mu.Lock()
		a.regCancel = cancel
		a.mu.Unlock()
		go RunRegistrationLoop(regCtx,
			deviceUUID,
			authToken,
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
	a.mu.Lock()
	cancel := a.regCancel
	a.regCancel = nil
	pending := a.pending
	a.pending = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if pending != nil {
		pending.abandon()
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
		cfgSnap := a.snapshotCfg()
		deviceUUID := a.cfg.TokenStore.DeviceUUID
		a.companion = NewCompanion(CompanionConfig{
			DeviceName: cfgSnap.DeviceName,
			DeviceUUID: deviceUUID,
			Version:    a.cfg.Version,
			DataDir:    a.cfg.Bridge.DataDir,
		}, a.cfg.Core)
		a.timeline = NewTimelineBroker(
			TimelineConfig{DeviceUUID: deviceUUID, DeviceName: cfgSnap.DeviceName},
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
			Key:   "device_name",
			Label: "Device Name",
			Help: "Shown in the Plex cast-target list. Requires a bridge restart" +
				" (Companion /resources, GDM, timeline headers, and plex.tv registration" +
				" all snapshot this at startup).",
			Kind:       adapters.KindText,
			Required:   true,
			Default:    "MiSTer",
			ApplyScope: adapters.ScopeRestartBridge,
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
	a.mu.Lock()
	a.plexCfg = cfg
	a.mu.Unlock()
	return nil
}

// Validate is the pure-check path used by the UI save handler
// BEFORE it persists a candidate config to disk. Decodes raw into
// a throwaway Config and runs plex.Config.Validate without touching
// a.plexCfg. Satisfies adapters.Validator.
func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("plex: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.plexCfg.Enabled
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

func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.stateSince = time.Now()
	a.lastErr = errMsg
}

// ApplyConfig diffs the candidate config against the live plexCfg,
// looks up each changed field's ApplyScope in scopeForPlexField,
// aggregates via max-scope-wins (design §9.1), and enacts the scope:
//
//   - ScopeHotSwap: mutate plexCfg; running goroutines re-read it.
//   - ScopeRestartCast: DropActiveCast on core so the next play
//     rebuilds the ffmpeg pipeline with the new settings.
//   - ScopeRestartBridge: mutate plexCfg for UI prefill, but the
//     running process keeps old values until the operator restarts
//     the container (the UI toast surfaces the restart command).
//
// On validation failure plexCfg is NOT mutated — the disk-side
// write-before-apply contract depends on this.
func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &newCfg); err != nil {
		return 0, fmt.Errorf("plex: decode apply config: %w", err)
	}
	if err := newCfg.Validate(); err != nil {
		return 0, err
	}

	a.mu.Lock()
	changed := diffPlexConfig(a.plexCfg, newCfg)
	scope := adapters.ScopeHotSwap
	for _, key := range changed {
		scope = adapters.MaxScope(scope, scopeForPlexField(key))
	}
	a.plexCfg = newCfg
	a.mu.Unlock()

	// Side effects outside the lock: DropActiveCast can block on
	// session teardown, and we don't want sidebar polls waiting on it.
	if scope == adapters.ScopeRestartCast && a.cfg.Core != nil {
		_ = a.cfg.Core.DropActiveCast("plex config change")
	}
	return scope, nil
}

// diffPlexConfig returns the set of field keys that differ between
// old and new. Key strings match the Fields() schema.
func diffPlexConfig(oldCfg, newCfg Config) []string {
	var changed []string
	if oldCfg.Enabled != newCfg.Enabled {
		changed = append(changed, "enabled")
	}
	if oldCfg.DeviceName != newCfg.DeviceName {
		changed = append(changed, "device_name")
	}
	if oldCfg.DeviceUUID != newCfg.DeviceUUID {
		changed = append(changed, "device_uuid")
	}
	if oldCfg.ProfileName != newCfg.ProfileName {
		changed = append(changed, "profile_name")
	}
	if oldCfg.ServerURL != newCfg.ServerURL {
		changed = append(changed, "server_url")
	}
	return changed
}

// scopeForPlexField returns the ApplyScope for a given field key.
//
// device_name is ScopeRestartBridge per the 7.4 review correction:
// Companion /resources, timeline push headers, GDM replies, and
// plex.tv registration all snapshot the identity at startup into
// long-lived structs. Live identity propagation requires coordinated
// updates across every one of those surfaces; until that lands, the
// conservative choice is restart-required.
//
// profile_name / server_url force a cast drop so the next play's
// pipeline sees the new settings. device_uuid is restart-required
// (GDM + plex.tv use it as stable device identity — mid-flight flips
// look like a new device and confuse controllers).
func scopeForPlexField(key string) adapters.ApplyScope {
	switch key {
	case "enabled":
		return adapters.ScopeHotSwap // handled out-of-band by the toggle endpoint
	case "device_name", "device_uuid":
		return adapters.ScopeRestartBridge
	case "profile_name", "server_url":
		return adapters.ScopeRestartCast
	default:
		return adapters.ScopeHotSwap
	}
}

// SetEnabled mutates the plexCfg.Enabled flag. Called by the UI
// toggle endpoint via the EnableSetter optional interface.
func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.plexCfg.Enabled = v
}

// CurrentValues exposes the current plexCfg values to the UI for
// form prefill. Implements ui.ValueProvider via duck-typing.
func (a *Adapter) CurrentValues() map[string]any {
	cfg := a.snapshotCfg()
	return map[string]any{
		"enabled":      cfg.Enabled,
		"device_name":  cfg.DeviceName,
		"profile_name": cfg.ProfileName,
		"server_url":   cfg.ServerURL,
	}
}
