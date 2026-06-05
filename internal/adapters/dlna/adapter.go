package dlna

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// SessionManager is the adapter's narrow view of core.Manager. The DLNA
// adapter must not import internal/core for unrelated purposes; the only
// uses are the SessionRequest / SessionStatus value types referenced
// here. core.Manager satisfies this via structural typing
// (manager.go:223,239,268,363,394,425). Mirrors the URL adapter's
// SessionManager interface (internal/adapters/url/adapter.go:24-31).
type SessionManager interface {
	StartSession(core.SessionRequest) error
	StartSessionIfAdapterRef(core.SessionRequest, string) (bool, error)
	Status() core.SessionStatus
	Pause() error
	PauseIfAdapterRef(string) (bool, error)
	Play() error
	PlayIfAdapterRef(string) (bool, error)
	Stop() error
	StopIfAdapterRef(string) (bool, error)
	SeekTo(offsetMs int) error
	SeekToIfAdapterRef(string, int) (bool, error)
}

// errForeignSession is the sentinel returned by ownershipGuard when
// core.Manager reports an active session that is NOT the DLNA adapter's
// current session. SOAP handlers in P2.3 / P2.4 map this to UPnP error
// 701 ("Transition not available") per spec §Common Action Rules. Kept
// package-private; callers compare with errors.Is.
var errForeignSession = errors.New("dlna: active core session is owned by another adapter")

// newDiscoveryFn is the package-level seam Adapter.Start calls instead
// of NewDiscovery directly. Rebound by tests to a fake constructor that
// captures the DiscoveryConfig and returns a fake discoveryRunner (or
// an error to drive the StateError path) — same pattern Plex uses for
// its GDM Discovery (internal/adapters/plex/discovery.go:60).
//
// Returns discoveryRunner (not *Discovery) so tests can substitute a
// no-socket fake. Production wraps NewDiscovery so its concrete
// *Discovery satisfies the interface.
var newDiscoveryFn = func(cfg DiscoveryConfig) (discoveryRunner, error) {
	return NewDiscovery(cfg)
}

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
// core.Manager, threaded through the SessionManager interface stored
// on the adapter as `core` (added in Phase 2 P2.1).
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
	// version is the bridge build version threaded from main.go's
	// `version` symbol (set via `-ldflags "-X main.version=..."`). Used
	// to construct the SSDP SERVER header. Empty in tests; production
	// passes a non-empty string. Immutable post-construction.
	version string
	// core is the adapter-agnostic session manager. Set once at New()
	// and never mutated, so it is held outside mu — same precedent as
	// internal/adapters/url/adapter.go:54 and the locking discipline in
	// CLAUDE.md (never hold a.mu across a core.Manager call).
	core SessionManager

	// events owns GENA SUBSCRIBE/UNSUBSCRIBE state and NOTIFY delivery
	// queues. The pointer is immutable after New; eventManager protects
	// its own maps with eventManager.mu.
	events *eventManager

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time

	// volume and muted are RenderingControl:1's adapter-local virtual
	// state (spec §RenderingControl lines 442-449). They do NOT change
	// FFmpeg or Groovy audio in v1 — they exist so DLNA control points
	// (VLC, BubbleUPnP, Kodi) can probe and set the renderer's volume
	// without their UI flow failing. Defaults: volume=100, muted=false.
	// Reset on FactoryDefaults preset and on Adapter.Start (no disk
	// persistence in Phase 1). Guarded by mu.
	volume int
	muted  bool

	// currentRef is the active or in-flight DLNA session ref ("dlna:<hex>").
	// Empty means the adapter has no active session (no SetAVTransportURI /
	// Play has minted one yet, or the last session has been cleared by
	// OnStop / failed StartSession rollback). startInFlight is true between
	// markStartInFlight and clearStartInFlight — i.e. while StartSession
	// is being called for this ref. Together they implement spec
	// §Session Ref Lifecycle (steps 1-7); see helper methods below.
	// Both guarded by mu.
	currentRef    string
	startInFlight bool

	// loadedURI is the most-recently-stored validated media URI; "" when
	// no URI is loaded. loadedMeta is the parsed DIDL-Lite from the same
	// SetAVTransportURI call. loadedMetaRaw is the raw CurrentURIMetaData
	// XML the controller sent — round-tripped verbatim by GetMediaInfo /
	// GetPositionInfo so we don't have to reconstruct DIDL on the fly.
	// lastError is the redacted last-error message from a validation /
	// probe / playback failure (used by query actions to surface
	// ERROR_OCCURRED with context). All four are guarded by mu.
	//
	// The redaction discipline for lastError: never store userinfo or
	// query strings — at most scheme + host (URLs go through redactURL
	// before being included). DIDL parse errors store a generic message.
	loadedURI string
	// loadedPlaybackURI is the URI handed to core.Manager. For direct
	// HTTP(S) playback it matches loadedURI; for cached HLS it points at
	// the local rewritten manifest while loadedURI remains the
	// controller-facing URL.
	loadedPlaybackURI string
	loadedHLSCleanup  func() error
	loadedCanSeek     bool
	loadedMeta        DIDLMetadata
	loadedMetaRaw     string
	lastError         string

	// transportState is the UPnP TransportState surfaced via
	// GetTransportInfo. Values: "STOPPED" | "PLAYING" |
	// "PAUSED_PLAYBACK" | "TRANSITIONING" (TRANSITIONING is reserved
	// for Phase 4 eventing). Phase 2 transitions STOPPED → PLAYING on
	// Play and PLAYING/PAUSED_PLAYBACK → STOPPED on Stop or OnStop.
	// Initialized to "STOPPED" in New(). Guarded by mu.
	transportState string

	// discovery and discoveryDone are owned by Start/Stop. discovery is
	// nil when the adapter is disabled, when Start hasn't run, or after
	// Stop. discoveryDone is the close signal from the Run goroutine; we
	// block on it inside Stop so Stop returns only after SSDP has
	// fully shut down (mirror of plex.Adapter.discoDone).
	discovery     discoveryRunner
	discoveryDone chan struct{}
}

// discoveryRunner is the small interface Adapter.Start uses to drive
// SSDP. Production binds the *Discovery returned by NewDiscovery; tests
// substitute a fake via newDiscoveryFn that records Run/Close calls
// without binding sockets. Mirrors the plex.packetWriter seam-shape.
type discoveryRunner interface {
	Run(ctx context.Context)
	Close() error
}

// AdapterConfig bundles the bridge-level context the DLNA adapter
// needs. The [adapters.dlna] TOML section flows through separately
// via DecodeConfig into Adapter.cfg — AdapterConfig carries only the
// adapter-agnostic pieces (device UUID, host IP, HTTP port) that
// main.go has resolved before adapters are constructed.
//
// Mirrors the URL adapter's AdapterConfig pattern
// (internal/adapters/url/adapter.go:97-108). Phase 2 added Core so
// SetAVTransportURI / Play / Stop handlers can drive the data plane.
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
	// Version is the bridge build version (main.version). Threaded into
	// the SSDP SERVER header so controllers see a stable, distinguishable
	// product token across releases. Empty/whitespace falls back to
	// "0.0.0" inside buildServerToken — useful for tests and dev builds
	// where ldflags aren't in play.
	Version string
	// Core is the adapter-agnostic session manager. core.Manager
	// satisfies this via structural typing. Required: the SOAP
	// handlers added in P2.3 / P2.4 dereference it without nil-checking
	// per call, so a nil here is a configuration error, not a runtime
	// degradation path.
	Core SessionManager
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
	if cfg.Core == nil {
		return nil, fmt.Errorf("dlna: AdapterConfig.Core is required")
	}
	a := &Adapter{
		deviceUUID: cfg.DeviceUUID,
		hostIP:     cfg.HostIP,
		httpPort:   cfg.HTTPPort,
		version:    cfg.Version,
		core:       cfg.Core,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: time.Now(),
		// RenderingControl:1 defaults per spec §RenderingControl lines
		// 444-445: Volume=100, Mute=false.
		volume: 100,
		muted:  false,
		// AVTransport:1 starts in STOPPED. P2.4's Play handler advances
		// to PLAYING on a successful StartSession; OnStop / Stop bring
		// it back. Spec §Query Actions / state mapping (line 387-393).
		transportState: transportStateStopped,
		events:         newEventManager(eventManagerConfig{}),
	}
	a.seedEventSnapshots()
	return a, nil
}

// mintSessionRef returns a fresh "dlna:<hex>" ref. 8 bytes of crypto
// randomness gives 64 bits of uniqueness — enough that the chance of two
// concurrent sessions colliding is irrelevant in practice (the
// lifecycle's compare-and-clear on currentRef is the actual correctness
// boundary; uniqueness is just defensive). Returns the ref by value;
// the caller writes it under mu (see markStartInFlight).
//
// Pure helper — no state on the Adapter is touched, but kept as a method
// so future seam tests can override via embedding if needed.
func (a *Adapter) mintSessionRef() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should not fail in practice; fall back to a
		// time-based ref so we never return an empty string (an empty
		// ref would defeat the ownership guard's "" == "no session"
		// semantics). This branch is best-effort, not a security path.
		return fmt.Sprintf("dlna:%x", time.Now().UnixNano())
	}
	return "dlna:" + hex.EncodeToString(b[:])
}

// markStartInFlight mints a fresh ref, stores it in currentRef, sets
// startInFlight=true under mu, and returns the ref by value. Caller
// must NOT hold mu. Implements lifecycle steps 1-2 (spec §Session Ref
// Lifecycle): the returned ref is what callers capture into the
// per-session OnStop closure. After this returns, the caller drops the
// reference and calls core.Manager.StartSession with the lock released.
func (a *Adapter) markStartInFlight() string {
	ref := a.mintSessionRef()
	a.mu.Lock()
	a.currentRef = ref
	a.startInFlight = true
	a.mu.Unlock()
	return ref
}

// markStartInFlightFor conditionally admits a StartSession attempt.
// expectedRef == "" means "fresh start": mint and store a new DLNA ref,
// but only when no other start is already in flight. expectedRef != ""
// means "same-session rebuild": keep that ref and only mark in-flight if
// currentRef still matches. Caller must NOT hold mu.
func (a *Adapter) markStartInFlightFor(expectedRef string) (string, bool) {
	mintedRef := ""
	if expectedRef == "" {
		mintedRef = a.mintSessionRef()
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.startInFlight {
		return "", false
	}
	if expectedRef != "" {
		if a.currentRef != expectedRef {
			return "", false
		}
		a.startInFlight = true
		return expectedRef, true
	}
	a.currentRef = mintedRef
	a.startInFlight = true
	return mintedRef, true
}

// clearStartInFlight reconciles the in-flight bookkeeping after a
// StartSession call returns. Caller must NOT hold mu.
//
// Compare-and-clear semantics (spec §Session Ref Lifecycle steps 4-6):
//   - If currentRef != ref, a faster session has already replaced this
//     one (an OnStop or a newer markStartInFlight won the race). No-op
//     so we don't clobber the winner's state.
//   - If currentRef == ref and success: clear startInFlight, keep
//     currentRef (the session is now live and owned by this adapter).
//   - If currentRef == ref and !success: clear both (lifecycle step 6's
//     "roll back the just-minted ref"). Adapter has no active session.
func (a *Adapter) clearStartInFlight(ref string, success bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != ref {
		return
	}
	a.startInFlight = false
	if !success {
		a.currentRef = ""
	}
}

// clearStartInFlightPreserveRef clears only the in-flight flag for a
// same-session rebuild. Used by live-edge resume failures: core may still
// own the paused session, so the adapter must keep currentRef visible.
func (a *Adapter) clearStartInFlightPreserveRef(ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != ref {
		return
	}
	a.startInFlight = false
}

// replaceLoadedPlaybackURIIfCurrent is the Play-time HLS recache variant:
// it only attaches the freshly prepared cache if the loaded source still
// matches the snapshot that triggered recache.
func (a *Adapter) replaceLoadedPlaybackURIIfCurrent(expectedLoadedURI string, uri string, cleanup func() error, canSeek bool) bool {
	a.mu.Lock()
	if a.loadedURI != expectedLoadedURI || a.loadedPlaybackURI != "" || a.loadedCanSeek {
		a.mu.Unlock()
		return false
	}
	oldCleanup := a.loadedHLSCleanup
	a.loadedPlaybackURI = uri
	a.loadedHLSCleanup = cleanup
	a.loadedCanSeek = canSeek
	a.mu.Unlock()
	if oldCleanup != nil {
		_ = oldCleanup()
	}
	return true
}

func (a *Adapter) clearLoadedPlaybackCacheIfIdle() {
	a.mu.Lock()
	if a.currentRef != "" {
		a.mu.Unlock()
		return
	}
	cleanup := a.loadedHLSCleanup
	if cleanup != nil {
		a.loadedPlaybackURI = ""
		a.loadedHLSCleanup = nil
		a.loadedCanSeek = false
	}
	a.mu.Unlock()
	if cleanup != nil {
		_ = cleanup()
	}
}

// ownershipGuard is the read-only check Pause / Play / Stop / Seek run
// before mutating core.Manager. Returns nil when the action is allowed
// to proceed; errForeignSession when core has an active session whose
// AdapterRef is non-empty AND does not match the adapter's currentRef.
// Empty AdapterRef means "no active core session" — allowed (spec
// §Common Action Rules: "leave the foreign session untouched" only
// applies when there IS a foreign session).
//
// Locking discipline: never holds a.mu across a.core.Status() (CLAUDE.md
// "never hold Adapter.mu across core.Manager calls"). The brief
// snapshot read of currentRef releases mu before calling Status, then
// the status comparison is purely local.
func (a *Adapter) ownershipGuard() error {
	a.mu.Lock()
	owned := a.currentRef
	a.mu.Unlock()

	st := a.core.Status()
	if st.AdapterRef == "" {
		return nil
	}
	if st.AdapterRef == owned {
		return nil
	}
	return errForeignSession
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
	deviceName := a.cfg.DeviceName
	a.mu.Unlock()

	if !enabled {
		// No-op: Start can be called by main.go on every adapter,
		// including ones the operator left disabled. Keep StateStopped
		// and don't construct Discovery (asserted by
		// TestAdapterStart_NoDiscoveryWhenDisabled).
		return nil
	}

	trimmed := strings.TrimSpace(a.hostIP)
	if trimmed == "" || net.ParseIP(trimmed) == nil {
		const msg = "DLNA requires a reachable bridge.host_ip. Set it in config.toml or ensure route-based autodetection succeeds."
		a.setState(adapters.StateError, msg)
		return fmt.Errorf("dlna: %s", msg)
	}

	// Spec line 261: "If SSDP bind or multicast join fails, Start
	// should set adapter state to StateError and return the error."
	// NewDiscovery wraps the underlying join error; Start translates
	// that into the state machine.
	disco, err := newDiscoveryFn(DiscoveryConfig{
		DeviceUUID:  a.deviceUUID,
		DeviceName:  deviceName,
		HostIP:      trimmed,
		HTTPPort:    a.httpPort,
		ServerToken: buildServerToken(a.version),
	})
	if err != nil {
		a.setState(adapters.StateError, err.Error())
		return err
	}

	done := make(chan struct{})
	a.mu.Lock()
	a.discovery = disco
	a.discoveryDone = done
	a.mu.Unlock()

	go func() {
		defer close(done)
		disco.Run(ctx)
	}()

	a.setState(adapters.StateRunning, "")
	return nil
}

// Stop tears down SSDP background work and returns nil. Closing
// discovery sends the byebye burst, unblocks the Run goroutine, and
// frees the multicast/sender sockets. We block on discoveryDone so
// Stop returns only once SSDP has fully shut down — main.go assumes
// adapter shutdown is synchronous and a leaked goroutine would race
// the bridge's HTTP listener teardown.
//
// Stopping a disabled adapter (where discovery was never constructed)
// is a no-op state mutation.
func (a *Adapter) Stop() error {
	a.mu.Lock()
	disco := a.discovery
	done := a.discoveryDone
	a.discovery = nil
	a.discoveryDone = nil
	a.mu.Unlock()

	if disco != nil {
		_ = disco.Close()
		if done != nil {
			<-done
		}
	}

	if a.events != nil {
		a.events.resetSubscriptions()
	}
	a.clearLoadedPlaybackCacheIfIdle()
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

// SetEnabled implements ui.EnableSetter. The toggle handler
// (ui.Server handleAdapterToggle) calls this in sync with
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
