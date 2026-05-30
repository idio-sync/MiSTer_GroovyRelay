package url

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

// SessionManager is the adapter's narrow view of core.Manager.
// core.Manager satisfies this via structural typing.
type SessionManager interface {
	StartSession(core.SessionRequest) error
	StartSessionIfSession(core.SessionRequest, string, uint64) (bool, error)
	Status() core.SessionStatus
	Pause() error
	Play() error
	Stop() error
	SeekTo(offsetMs int) error
	PauseIfSession(string, uint64) (bool, error)
	PlayIfSession(string, uint64) (bool, error)
	StopIfSession(string, uint64) (bool, error)
	SeekToIfSession(string, uint64, int) (bool, error)
}

// resolverIface is the adapter's narrow view of *ytdlp.Resolver. Lets
// play_test.go inject a stub without spinning up exec.
type resolverIface interface {
	Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error)
}

type hlsBufferOpener func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error)

// ytdlpProbe is the cached result of probing yt-dlp at adapter Start.
// Computed once; read-only afterward, so no mu protection needed.
type ytdlpProbe struct {
	Path    string // absolute path; empty if not found
	Version string // output of `yt-dlp --version` first line
	OK      bool   // false ⇒ adapter behaves as if YtdlpEnabled=false
}

// Adapter implements adapters.Adapter for the URL-input cast source.
// Spec: docs/specs/2026-04-25-url-adapter-design.md.
//
// Concurrency: all field reads and writes (cfg, state, lastErr,
// stateSince, lastURL) go through mu. Status() and OnStop's mutator
// share the same lock so the panel fragment never observes a torn read.
type Adapter struct {
	core SessionManager
	// bridge is the bridge-level config snapshot used for URL-owned cache
	// roots and shared HLS buffer defaults.
	bridge config.BridgeConfig

	// cookiesPath is computed once from cfg.Bridge.DataDir at New()
	// and is read-only thereafter — does not need mu.
	cookiesPath string

	// resolver is the yt-dlp resolver. Nil-tolerant: the play handler
	// returns 500 if it tries to invoke a nil resolver. Set in
	// Adapter.Start() when ytdlpProbe.OK is true; left nil otherwise.
	resolver resolverIface

	// ytdlpProbe is the cached yt-dlp version + path. Set in
	// Adapter.Start(). Surfaced in the panel via renderPanel.
	ytdlpProbe ytdlpProbe

	// probeFn returns the result of probing for yt-dlp. Defaults to
	// realProbeYtdlp; tests inject stubs.
	probeFn func() ytdlpProbe

	hlsBufferOpen hlsBufferOpener

	ytdlpBinaryResolver ytdlp.BinaryResolver
	streamResolver      streamhandoff.Resolver

	mu            sync.Mutex
	cfg           Config
	state         adapters.State
	lastErr       string
	stateSince    time.Time
	lastURL       string // last URL handed to StartSession; surfaced in the panel
	activeOverlay *hlsMeterHandle

	// history is the LRU URL-recall list shown in the panel. Its own
	// mutex; not guarded by a.mu. Path computed once at New() from
	// cfg.Bridge.DataDir, mirroring the cookiesPath pattern.
	history *History

	// eventLog is the optional ring buffer for cast lifecycle events
	// (cast-requested). Nil-safe: emit() checks before appending.
	// Set at construction from AdapterConfig.EventLog.
	eventLog *eventlog.Log
}

// AdapterConfig bundles the bridge-level context the URL adapter
// needs. The [adapters.url] TOML section flows through separately via
// DecodeConfig into Adapter.cfg — AdapterConfig carries only the
// adapter-agnostic pieces (bridge data_dir for the cookies file,
// session manager for StartSession).
//
// Mirrors the Plex AdapterConfig pattern at
// internal/adapters/plex/adapter.go:22-42 — the URL adapter joined that
// pattern when it grew the cookies feature (review fix C3).
type AdapterConfig struct {
	// Bridge is a snapshot of the bridge-level config: data_dir, etc.
	// The adapter reads DataDir from here for the url_cookies.txt path.
	Bridge config.BridgeConfig
	// Core is the adapter-agnostic session manager. core.Manager
	// satisfies this via structural typing. May be nil in tests that
	// don't exercise the play handler.
	Core SessionManager
	// YTDLPResolver resolves yt-dlp through the shared sidecar/PATH resolver.
	// Nil preserves the legacy PATH-only probe used by tests.
	YTDLPResolver ytdlp.BinaryResolver

	// EventLog is the optional ring buffer for cast-requested events.
	// Nil-safe: the adapter's emit() method checks before appending.
	EventLog *eventlog.Log
}

// New constructs a ready-to-Start Adapter from the bundled config.
// Returns an error if Bridge.DataDir is empty (the cookies path is
// derived from it; an empty value would write to a relative path
// inside the container's working directory).
func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Bridge.DataDir == "" {
		return nil, fmt.Errorf("url: AdapterConfig.Bridge.DataDir is required")
	}
	historyPath := filepath.Join(cfg.Bridge.DataDir, "url_history.json")
	probeFn := realProbeYtdlp
	if cfg.YTDLPResolver != nil {
		probeFn = func() ytdlpProbe { return probeYtdlp(cfg.YTDLPResolver) }
	}
	return &Adapter{
		core:                cfg.Core,
		bridge:              cfg.Bridge,
		state:               adapters.StateStopped,
		stateSince:          time.Now(),
		cookiesPath:         filepath.Join(cfg.Bridge.DataDir, "url_cookies.txt"),
		probeFn:             probeFn,
		hlsBufferOpen:       hlsbuffer.OpenSession,
		history:             LoadHistory(historyPath),
		ytdlpBinaryResolver: cfg.YTDLPResolver,
		eventLog:            cfg.EventLog,
	}, nil
}

// emit appends a lifecycle event to the event log if one is configured.
// Nil-safe: a nil eventLog is a no-op (most tests don't wire a log).
func (a *Adapter) emit(sev eventlog.Severity, msg string) {
	if a.eventLog == nil {
		return
	}
	a.eventLog.Append(eventlog.Entry{
		Time:     time.Now(),
		Severity: sev,
		Source:   "url",
		Message:  msg,
	})
}

// CookiesPath returns the absolute path to the cookies file. Stable
// across the adapter's lifetime; computed once at construction.
func (a *Adapter) CookiesPath() string { return a.cookiesPath }

func (a *Adapter) SetStreamResolver(r streamhandoff.Resolver) {
	a.mu.Lock()
	a.streamResolver = r
	a.mu.Unlock()
}

// ---- adapters.Adapter interface ----

func (a *Adapter) Name() string        { return "url" }
func (a *Adapter) DisplayName() string { return "URL" }

func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{
			Key:        "enabled",
			Label:      "Enabled",
			Help:       "Turn the URL adapter on or off. When enabled, the global Cast drawer accepts http(s) media URLs.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "ytdlp_enabled",
			Label:      "yt-dlp resolver",
			Help:       "Master switch. When on, mode=auto routes URLs whose host matches the list below through yt-dlp.",
			Kind:       adapters.KindBool,
			Default:    true,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "ytdlp_format",
			Label:      "yt-dlp format",
			Help:       "Format selector. Default prefers ≤720p video with broad codec compatibility.",
			Kind:       adapters.KindText,
			Default:    "bv*[height<=720]+ba/bv*+ba/b",
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "ytdlp_resolve_timeout_seconds",
			Label:      "Resolve timeout (s)",
			Help:       "Per-URL timeout for yt-dlp resolution (5–120s).",
			Kind:       adapters.KindInt,
			Default:    30,
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

// Start probes for yt-dlp and caches the result. The probe is
// best-effort; if yt-dlp is not on PATH or doesn't respond, Start
// still returns nil and the adapter degrades to direct-only mode.
//
// Probe is computed once and not refreshed (review fix M5 — refresh
// is a deferred follow-up). Operator-initiated yt-dlp updates
// inside the running container show stale UI until next bridge
// restart; the entrypoint update path makes this a non-issue for
// the typical container-restart workflow.
func (a *Adapter) Start(ctx context.Context) error {
	probe := a.probeFn()

	// Build the resolver outside the lock (no shared state involved)
	// then assign + cache probe under a.mu in one critical section.
	// This keeps a.resolver writes lock-protected, matching the
	// locked read in handlePlay (review fix I4 — prevents -race
	// detector flag in CI).
	var resolver resolverIface
	a.mu.Lock()
	cfgTimeout := a.cfg.YtdlpResolveTimeoutSeconds
	a.mu.Unlock()
	if probe.OK || a.ytdlpBinaryResolver != nil {
		resolver = &ytdlp.Resolver{
			Binary:         probe.Path,
			BinaryResolver: a.ytdlpBinaryResolver,
			Timeout:        time.Duration(cfgTimeout) * time.Second,
			Runner:         ytdlp.OSRunner{},
		}
	}

	a.mu.Lock()
	a.ytdlpProbe = probe
	a.resolver = resolver
	a.mu.Unlock()

	a.setState(adapters.StateRunning, "")
	return nil
}

// realProbeYtdlp is the production probe. Looks up the yt-dlp binary
// via PATH, runs `yt-dlp --version` with a short timeout, and reports
// the result. Failure is non-fatal — adapter degrades to direct-only.
func realProbeYtdlp() ytdlpProbe {
	path, err := exec.LookPath("yt-dlp")
	if err != nil {
		return ytdlpProbe{OK: false}
	}
	return probeYtdlpPath(path)
}

func probeYtdlp(resolver ytdlp.BinaryResolver) ytdlpProbe {
	path, err := resolver.Resolve()
	if err != nil {
		return ytdlpProbe{OK: false}
	}
	return probeYtdlpPath(path)
}

func probeYtdlpPath(path string) ytdlpProbe {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ytdlpProbe{Path: path, OK: false}
	}
	version := strings.TrimSpace(string(bytes.SplitN(out, []byte("\n"), 2)[0]))
	return ytdlpProbe{Path: path, Version: version, OK: true}
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

// ApplyConfig stores the new TOML and rebuilds any state that depends
// on hot-swappable fields. Returns ScopeHotSwap.
//
// Resolver rebuild: cfg.YtdlpResolveTimeoutSeconds feeds into
// Resolver.Timeout, which is captured at Adapter.Start. Without
// rebuilding here, an operator changing the timeout via the UI/TOML
// would see "applied live" in the toast but the next resolve would
// still use the Start-time timeout — a documented HotSwap field
// silently failing to hot-swap. The rebuild only fires when the
// timeout actually changed, and only if probe.OK (no resolver to
// rebuild otherwise).
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
	oldTimeout := a.cfg.YtdlpResolveTimeoutSeconds
	probe := a.ytdlpProbe
	a.cfg = newCfg
	// Rebuild the resolver if (a) the binary is present and (b) the
	// timeout-driving field actually changed. Reuses OSRunner because
	// the Runner has no per-call state.
	if (probe.OK || a.ytdlpBinaryResolver != nil) && oldTimeout != newCfg.YtdlpResolveTimeoutSeconds {
		a.resolver = &ytdlp.Resolver{
			Binary:         probe.Path,
			BinaryResolver: a.ytdlpBinaryResolver,
			Timeout:        time.Duration(newCfg.YtdlpResolveTimeoutSeconds) * time.Second,
			Runner:         ytdlp.OSRunner{},
		}
	}
	a.mu.Unlock()
	return adapters.ScopeHotSwap, nil
}

// CurrentValues implements ui.ValueProvider via duck-typing — surfaces
// the current cfg values to the UI for form prefill. Must stay in
// lockstep with Fields(): the chassis form prefill (4D's
// AdapterSettingsSaver.Current path) renders blank/off for any Fields()
// key missing here, and this map is also SaveTouched's missing-section
// fallback. ytdlp_hosts is intentionally absent — the host widget reads
// it via CurrentHosts(), and a first-save with no disk section keeps the
// DefaultConfig() host list (ApplyConfig decodes onto DefaultConfig).
func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":                       a.cfg.Enabled,
		"ytdlp_enabled":                 a.cfg.YtdlpEnabled,
		"ytdlp_format":                  a.cfg.YtdlpFormat,
		"ytdlp_resolve_timeout_seconds": a.cfg.YtdlpResolveTimeoutSeconds,
	}
}

// setState atomically updates state, stateSince, and lastErr.
func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.stateSince = time.Now()
	a.lastErr = errMsg
}
