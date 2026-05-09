package dlna

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// Adapter implements adapters.Adapter for the DLNA / UPnP MediaRenderer
// cast source. Spec:
// docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md.
//
// Concurrency: deviceUUID, hostIP, and httpPort are immutable
// post-construction (set once in New, read-only thereafter — no mu
// needed for those reads). All other field reads and writes (cfg,
// state, lastErr, stateSince) go through mu. Status() and the
// setState mutator share the same lock so the panel fragment never
// observes a torn read. Same locking discipline as the URL adapter.
//
// Phase 1 scaffold: SSDP socket lifecycle (T3) and SOAP/SCPD handlers
// (T4) extend this struct in later tasks. The data plane is owned by
// core.Manager (threaded through a SessionManager interface added in
// Phase 2) — not stored here.
type Adapter struct {
	// deviceUUID is the persisted bridge UUID (store.DeviceUUID) used
	// to build the UPnP UDN. Threaded from main.go so DLNA does not
	// mint its own. Immutable post-construction.
	deviceUUID string
	// hostIP is the resolved bridge LAN IP advertised in SSDP
	// LOCATION headers and the device descriptor. Validated at
	// Start() time; an empty / unparseable value with Enabled=true
	// fails Start with StateError. Immutable post-construction.
	hostIP string
	// httpPort is bridge.ui.http_port — the bridge HTTP listener
	// where /dlna/* routes are mounted. Immutable post-construction.
	httpPort int

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time
}

// AdapterConfig bundles the bridge-level context the DLNA adapter
// needs. The [adapters.dlna] TOML section flows through separately
// via DecodeConfig into Adapter.cfg — AdapterConfig carries only the
// adapter-agnostic pieces (device UUID, host IP, HTTP port) that
// main.go has resolved before adapters are constructed.
//
// Mirrors the URL adapter's AdapterConfig pattern
// (internal/adapters/url/adapter.go:97-108). DLNA does not (yet)
// hold a reference to the session manager — that lands in Phase 2
// when SOAP handlers translate SetAVTransportURI into StartSession.
type AdapterConfig struct {
	// DeviceUUID is store.DeviceUUID from the persisted bridge store,
	// threaded via main.go. DLNA must not mint its own stable UUID
	// (spec §Architecture / Integration Boundary line 128). Required.
	DeviceUUID string
	// HostIP is bridge.host_ip after autodetection. May be empty at
	// construction; the non-empty + parseable check runs at Start
	// time so the adapter can register disabled even when the bridge
	// cannot resolve a stable IP.
	HostIP string
	// HTTPPort is bridge.ui.http_port — the single bridge HTTP
	// listener where /dlna/* routes are mounted. Must be in (0, 65535].
	HTTPPort int
}

// New constructs a ready-to-Start Adapter from the bundled config.
// Returns an error for invariants that must hold even when the
// adapter is registered disabled (DeviceUUID, HTTPPort). HostIP is
// deliberately deferred to Start because an empty value with
// Enabled=false is a valid configuration.
func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.DeviceUUID == "" {
		return nil, fmt.Errorf("dlna: AdapterConfig.DeviceUUID is required")
	}
	if cfg.HTTPPort <= 0 || cfg.HTTPPort > 65535 {
		return nil, fmt.Errorf("dlna: AdapterConfig.HTTPPort must be in (0, 65535], got %d", cfg.HTTPPort)
	}
	return &Adapter{
		deviceUUID: cfg.DeviceUUID,
		hostIP:     cfg.HostIP,
		httpPort:   cfg.HTTPPort,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: time.Now(),
	}, nil
}

// ---- adapters.Adapter interface ----

func (a *Adapter) Name() string        { return "dlna" }
func (a *Adapter) DisplayName() string { return "DLNA / UPnP" }

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("dlna: decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}

// Validate is the optional adapters.Validator hook. Decodes the
// TOML primitive against a fresh DefaultConfig() and runs the same
// Validate() the live config goes through, but does not mutate the
// adapter — letting the UI reject bad input before disk write.
func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("dlna: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

// Start runs the HostIP pre-flight when Enabled=true. Real SSDP
// startup (UDP socket bind, NOTIFY broadcast, alive refresh
// goroutine) lands in T3.
//
// HostIP rule (spec §Architecture / Integration Boundary line 132):
// when DLNA is enabled, the resolved bridge IP must be non-empty and
// parse as a concrete IP. Otherwise SSDP would advertise
// "http://:32500/dlna/device.xml" — controllers would file the
// device but every subsequent fetch would fail. Failing fast here
// surfaces the misconfig in the UI sidebar instead.
//
// Disabled adapters are valid: Start returns nil and stays in
// StateStopped without touching the network. The toggle path
// (SetEnabled + Start/Stop) handles transitions.
func (a *Adapter) Start(ctx context.Context) error {
	a.mu.Lock()
	enabled := a.cfg.Enabled
	a.mu.Unlock()

	if !enabled {
		// No-op: Start can be called by main.go on every adapter,
		// including ones the operator left disabled. Keep StateStopped.
		return nil
	}

	trimmed := strings.TrimSpace(a.hostIP)
	if trimmed == "" || net.ParseIP(trimmed) == nil {
		const msg = "DLNA requires a reachable bridge.host_ip. Set it in config.toml or ensure route-based autodetection succeeds."
		a.setState(adapters.StateError, msg)
		return fmt.Errorf("dlna: %s", msg)
	}

	a.setState(adapters.StateRunning, "")
	return nil
}

// Stop tears down SSDP background work and returns nil. Phase 1 has
// no SSDP yet, so this is just a state mutation. Stopping a disabled
// adapter is a no-op (already in StateStopped).
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
// Start/Stop. Without it the toggle endpoint returns 500 (mirrors
// internal/adapters/url/adapter.go:275-279).
func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Enabled = v
}

// ApplyConfig stores the new TOML and reports the highest-severity
// scope across changed fields. Per spec §Config (lines 540-548):
// device_name → ScopeRestartBridge; everything else → ScopeHotSwap.
//
// Returning ScopeHotSwap when no fields changed matches the
// "lowest scope" convention — the save path treats it as a
// no-op-equivalent, identical to a HotSwap-only save.
func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &newCfg); err != nil {
		return 0, fmt.Errorf("dlna: decode apply config: %w", err)
	}
	if err := newCfg.Validate(); err != nil {
		// Don't transition to StateError on Validate failure: the save
		// path rejects the change before disk write, so the running
		// adapter is unaffected. A validation-time error is a UI-level
		// rejection, not a runtime error, and BridgeSaver expects the
		// adapter's State to remain unchanged across a rejected save.
		// (The URL adapter currently does setState(StateError) here —
		// known divergence; DLNA follows the spec.)
		return 0, err
	}

	a.mu.Lock()
	oldCfg := a.cfg
	a.cfg = newCfg
	a.mu.Unlock()

	// Per-field scope contributions per spec §Config (the "Field
	// meanings and ApplyScope" table). The Enabled / AutoplayOnSetURI /
	// AllowPublicSourceURLs branches are arithmetically no-ops today
	// (all three are HotSwap, the floor — MaxScope(HotSwap, HotSwap) ==
	// HotSwap) but are kept explicit so a future field with a higher
	// scope adds a row to this table without inverting the structure.
	scope := adapters.ScopeHotSwap
	if oldCfg.Enabled != newCfg.Enabled {
		scope = adapters.MaxScope(scope, adapters.ScopeHotSwap)
	}
	if oldCfg.DeviceName != newCfg.DeviceName {
		scope = adapters.MaxScope(scope, adapters.ScopeRestartBridge)
	}
	if oldCfg.AutoplayOnSetURI != newCfg.AutoplayOnSetURI {
		scope = adapters.MaxScope(scope, adapters.ScopeHotSwap)
	}
	if oldCfg.AllowPublicSourceURLs != newCfg.AllowPublicSourceURLs {
		scope = adapters.MaxScope(scope, adapters.ScopeHotSwap)
	}
	return scope, nil
}

// setState atomically updates state, stateSince, and lastErr.
func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.stateSince = time.Now()
	a.lastErr = errMsg
}
