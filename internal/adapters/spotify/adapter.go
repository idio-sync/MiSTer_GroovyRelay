// Package spotify is the Spotify Connect receiver: a bridge-supervised
// librespot whose PCM drives the CRT music visualizer through liveaudio.
//
// Spec: docs/superpowers/specs/2026-09-24-live-audio-receivers-design.md.
package spotify

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/liveaudio"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const (
	sourceName = "spotify"
	pcmRoute   = "/internal/liveaudio/spotify/pcm/"
	eventRoute = "/internal/liveaudio/spotify/events/"
)

// AdapterConfig wires the adapter.
type AdapterConfig struct {
	Core     liveaudio.Core
	HTTPPort int    // bridge listener, for the loopback routes
	DataDir  string // librespot credential cache and artwork cache

	// Executable returns the bridge binary used as librespot's --onevent
	// program. Nil = os.Executable.
	Executable func() (string, error)
}

type Adapter struct {
	core       liveaudio.Core
	httpPort   int
	dataDir    string
	executable func() (string, error)
	relay      *liveaudio.Relay
	eventToken string

	// opMu serializes lifecycle transitions (Start, Stop, helper restarts
	// from ApplyConfig).
	opMu sync.Mutex

	mu       sync.Mutex
	cfg      Config
	running  bool
	startErr string
	since    time.Time
	session  *liveaudio.Session
	sup      *liveaudio.Supervisor
}

func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Core == nil {
		return nil, fmt.Errorf("spotify: AdapterConfig.Core is required")
	}
	relay, err := liveaudio.NewRelay(pcmRoute)
	if err != nil {
		return nil, err
	}
	token, err := liveaudio.RandomToken()
	if err != nil {
		return nil, err
	}
	if cfg.Executable == nil {
		cfg.Executable = os.Executable
	}
	return &Adapter{
		core:       cfg.Core,
		httpPort:   cfg.HTTPPort,
		dataDir:    cfg.DataDir,
		executable: cfg.Executable,
		relay:      relay,
		eventToken: token,
		cfg:        DefaultConfig(),
		since:      time.Now(),
	}, nil
}

func (a *Adapter) Name() string        { return sourceName }
func (a *Adapter) DisplayName() string { return "Spotify Connect" }

func (a *Adapter) Fields() []adapters.FieldDef {
	d := DefaultConfig()
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: false, ApplyScope: adapters.ScopeRestartCast},
		{Key: "name", Label: "Device Name", Help: "How the receiver appears in the Spotify app's device list.", Kind: adapters.KindText, Default: d.Name, Required: true, ApplyScope: adapters.ScopeRestartCast},
		{Key: "audio_output", Label: "Audio Output", Help: "monitor plays the music through the MiSTer; visual_only drives the visualizer silently.", Kind: adapters.KindEnum, Enum: []string{AudioOutputMonitor, AudioOutputVisualOnly}, Default: d.AudioOutput, ApplyScope: adapters.ScopeNextCast},
		{Key: "pause_grace_seconds", Label: "Pause Grace (s)", Help: "How long a paused session keeps the CRT before ending. 0 ends it on pause.", Kind: adapters.KindInt, Default: d.PauseGraceSeconds, ApplyScope: adapters.ScopeHotSwap},
		{Key: "bitrate", Label: "Bitrate", Kind: adapters.KindEnum, Enum: []string{"96", "160", "320"}, Default: strconv.Itoa(d.Bitrate), ApplyScope: adapters.ScopeRestartCast},
		{Key: "zeroconf_port", Label: "Discovery Port", Help: "Fixed TCP port for Spotify Connect discovery; 0 picks a random one.", Kind: adapters.KindInt, Default: d.ZeroconfPort, ApplyScope: adapters.ScopeRestartCast},
		{Key: "binary_path", Label: "librespot Path", Help: "Empty looks librespot up on PATH (bundled in the Docker image).", Kind: adapters.KindText, Placeholder: "librespot", ApplyScope: adapters.ScopeRestartCast},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("spotify: decode config: %w", err)
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
		return fmt.Errorf("spotify: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	a.cfg.Enabled = v
	a.mu.Unlock()
}

// SourceID and Configured light the chassis SPOTIFY lamp.
func (a *Adapter) SourceID() string { return sourceName }
func (a *Adapter) Configured() bool { return a.IsEnabled() }

func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":             a.cfg.Enabled,
		"name":                a.cfg.Name,
		"audio_output":        a.cfg.AudioOutput,
		"pause_grace_seconds": a.cfg.PauseGraceSeconds,
		"bitrate":             strconv.Itoa(a.cfg.Bitrate),
		"zeroconf_port":       a.cfg.ZeroconfPort,
		"binary_path":         a.cfg.BinaryPath,
	}
}

// MountPublicRoutes mounts the loopback PCM and event routes.
func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	a.relay.Mount(mux)
	mux.HandleFunc("POST "+eventRoute+"{token}", a.handleEvent)
}

func (a *Adapter) Start(context.Context) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	cfg := a.cfg
	running := a.running
	a.mu.Unlock()
	if running || !cfg.Enabled {
		return nil
	}
	return a.startLocked(cfg)
}

// startLocked brings the session and helper up. Caller holds opMu.
func (a *Adapter) startLocked(cfg Config) error {
	spec, err := a.helperSpec(cfg)
	if err != nil {
		a.setStartErr(err)
		return err
	}
	var sup *liveaudio.Supervisor
	session := liveaudio.NewSession(liveaudio.SessionConfig{
		Source:   sourceName,
		Label:    "SPOTIFY",
		Core:     a.core,
		Relay:    a.relay,
		HTTPPort: a.httpPort,
		DataDir:  a.dataDir,
		// The CRT stopped showing the stream without the phone asking:
		// drop librespot's connection so the phone notices.
		OnExternalStop: func(string) { sup.Restart() },
	}, sessionOptions(cfg))
	sup = liveaudio.NewSupervisor(liveaudio.SupervisorConfig{
		Name:   "librespot",
		Stdout: a.relay,
		// Whatever the reason, the phone's connection went with the helper.
		OnExit: func(error, bool) { session.Handle(liveaudio.Event{Kind: liveaudio.EventStop}) },
	})
	if err := sup.Start(spec); err != nil {
		session.Close()
		a.setStartErr(err)
		return err
	}
	a.mu.Lock()
	a.session, a.sup = session, sup
	a.running = true
	a.startErr = ""
	a.since = time.Now()
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Stop() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.stopLocked()
	a.mu.Lock()
	a.startErr = ""
	a.mu.Unlock()
	return nil
}

// stopLocked tears the helper and session down. Caller holds opMu.
func (a *Adapter) stopLocked() {
	a.mu.Lock()
	session, sup := a.session, a.sup
	a.session, a.sup = nil, nil
	wasRunning := a.running
	a.running = false
	a.mu.Unlock()
	if sup != nil {
		sup.Stop()
	}
	if session != nil {
		session.Close()
	}
	a.relay.Reset()
	if wasRunning {
		a.mu.Lock()
		a.since = time.Now()
		a.mu.Unlock()
	}
}

func (a *Adapter) Status() adapters.Status {
	a.mu.Lock()
	running, startErr, since := a.running, a.startErr, a.since
	session, sup := a.session, a.sup
	a.mu.Unlock()
	switch {
	case startErr != "":
		return adapters.Status{State: adapters.StateError, LastError: startErr, Since: since}
	case !running:
		return adapters.Status{State: adapters.StateStopped, Since: since}
	}
	if h := sup.Health(); h.Err != "" {
		return adapters.Status{State: adapters.StateError, LastError: "librespot: " + h.Err, Since: since}
	}
	if msg := session.LastError(); msg != "" {
		return adapters.Status{State: adapters.StateError, LastError: msg, Since: since}
	}
	return adapters.Status{State: adapters.StateRunning, Since: since}
}

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	next, err := decodeConfig(raw, meta)
	if err != nil {
		return 0, fmt.Errorf("spotify: decode apply config: %w", err)
	}
	if err := next.Validate(); err != nil {
		return 0, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	prev := a.cfg
	a.cfg = next
	session := a.session
	running := a.running
	a.mu.Unlock()

	scope := scopeForChange(prev, next)
	if session != nil {
		session.SetOptions(sessionOptions(next))
	}
	// Enable/disable is handled by the caller's lifecycle hook; a running
	// adapter whose command line changed restarts its helper in place.
	if running && next.Enabled && prev.helperSettings() != next.helperSettings() {
		a.stopLocked()
		if err := a.startLocked(next); err != nil {
			return scope, err
		}
	}
	return scope, nil
}

func scopeForChange(prev, next Config) adapters.ApplyScope {
	scope := adapters.ScopeHotSwap
	if prev.AudioOutput != next.AudioOutput {
		scope = adapters.MaxScope(scope, adapters.ScopeNextCast)
	}
	if prev.Enabled != next.Enabled || prev.helperSettings() != next.helperSettings() {
		scope = adapters.MaxScope(scope, adapters.ScopeRestartCast)
	}
	return scope
}

func sessionOptions(cfg Config) liveaudio.Options {
	output := core.AudioOutputMonitor
	if cfg.AudioOutput == AudioOutputVisualOnly {
		output = core.AudioOutputVisualOnly
	}
	return liveaudio.Options{
		AudioOutput: output,
		PauseGrace:  time.Duration(cfg.PauseGraceSeconds) * time.Second,
	}
}

func (a *Adapter) setStartErr(err error) {
	a.mu.Lock()
	a.startErr = err.Error()
	a.since = time.Now()
	a.mu.Unlock()
}

// helperSpec builds the librespot invocation for cfg.
func (a *Adapter) helperSpec(cfg Config) (liveaudio.HelperSpec, error) {
	exe, err := a.executable()
	if err != nil {
		return liveaudio.HelperSpec{}, fmt.Errorf("locate bridge binary for librespot events: %w", err)
	}
	hook, err := hookProgram(exe)
	if err != nil {
		return liveaudio.HelperSpec{}, err
	}
	path := strings.TrimSpace(cfg.BinaryPath)
	if path == "" {
		path = "librespot"
	}
	args := []string{
		"--name", strings.TrimSpace(cfg.Name),
		"--device-type", "tv",
		"--backend", "pipe", // no --device: PCM to stdout
		"--format", "S16",
		"--bitrate", strconv.Itoa(cfg.Bitrate),
		"--disable-audio-cache",
		"--onevent", hook + " " + HookFlag,
	}
	if a.dataDir != "" {
		args = append(args, "--cache", filepath.Join(a.dataDir, "librespot"))
	}
	if cfg.ZeroconfPort > 0 {
		args = append(args, "--zeroconf-port", strconv.Itoa(cfg.ZeroconfPort))
	}
	eventURL := fmt.Sprintf("http://127.0.0.1:%d%s%s", a.httpPort, eventRoute, a.eventToken)
	return liveaudio.HelperSpec{
		Path: path,
		Args: args,
		Env:  []string{eventURLEnv + "=" + eventURL},
	}, nil
}

func (a *Adapter) handleEvent(w http.ResponseWriter, r *http.Request) {
	if !liveaudio.RemoteIsLoopback(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PathValue("token")), []byte(a.eventToken)) != 1 {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	if ev, ok := eventFromForm(r.PostForm); ok && session != nil {
		session.Handle(ev)
	}
	w.WriteHeader(http.StatusNoContent)
}
