// Package airplay is the AirPlay (classic, AirPlay 1) receiver: a
// bridge-supervised shairport-sync whose PCM drives the CRT music
// visualizer through liveaudio, with track metadata and cover art read
// from shairport-sync's UDP metadata socket.
//
// Spec: docs/superpowers/specs/2026-09-24-live-audio-receivers-design.md.
package airplay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/liveaudio"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const (
	sourceName = "airplay"
	pcmRoute   = "/internal/liveaudio/airplay/pcm/"
)

// errUnsupportedPlatform: shairport-sync does not build natively on Windows.
var errUnsupportedPlatform = errors.New("AirPlay is not supported on Windows (shairport-sync runs on Linux and macOS; use the Docker image)")

// AdapterConfig wires the adapter.
type AdapterConfig struct {
	Core     liveaudio.Core
	HTTPPort int    // bridge listener, for the loopback PCM route
	DataDir  string // shairport-sync config file and artwork cache

	goos string // tests; empty = runtime.GOOS
}

type Adapter struct {
	dataDir  string
	goos     string
	relay    *liveaudio.Relay
	receiver *liveaudio.Receiver

	// opMu serializes lifecycle transitions (Start, Stop, helper restarts
	// from ApplyConfig).
	opMu sync.Mutex

	mu       sync.Mutex
	cfg      Config
	meta     net.PacketConn // metadata socket; nil when stopped
	metaDone chan struct{}
}

func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Core == nil {
		return nil, fmt.Errorf("airplay: AdapterConfig.Core is required")
	}
	relay, err := liveaudio.NewRelay(pcmRoute)
	if err != nil {
		return nil, err
	}
	goos := cfg.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	return &Adapter{
		dataDir: cfg.DataDir,
		goos:    goos,
		relay:   relay,
		receiver: liveaudio.NewReceiver(liveaudio.ReceiverConfig{
			Source:     sourceName,
			Label:      "AIRPLAY",
			HelperName: "shairport-sync",
			Core:       cfg.Core,
			Relay:      relay,
			HTTPPort:   cfg.HTTPPort,
			DataDir:    cfg.DataDir,
		}),
		cfg: DefaultConfig(),
	}, nil
}

func (a *Adapter) Name() string        { return sourceName }
func (a *Adapter) DisplayName() string { return "AirPlay" }

func (a *Adapter) Fields() []adapters.FieldDef {
	d := DefaultConfig()
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: false, ApplyScope: adapters.ScopeRestartCast},
		{Key: "name", Label: "Speaker Name", Help: "How the receiver appears in the AirPlay picker.", Kind: adapters.KindText, Default: d.Name, Required: true, ApplyScope: adapters.ScopeRestartCast},
		{Key: "audio_output", Label: "Audio Output", Help: "monitor plays the music through the MiSTer; visual_only drives the visualizer silently.", Kind: adapters.KindEnum, Enum: []string{AudioOutputMonitor, AudioOutputVisualOnly}, Default: d.AudioOutput, ApplyScope: adapters.ScopeNextCast},
		{Key: "pause_grace_seconds", Label: "Pause Grace (s)", Help: "How long a paused session keeps the CRT before ending. 0 ends it on pause.", Kind: adapters.KindInt, Default: d.PauseGraceSeconds, ApplyScope: adapters.ScopeHotSwap},
		{Key: "port", Label: "RTSP Port", Help: "AirPlay control port; change it if another AirPlay receiver on this host already uses 5000.", Kind: adapters.KindInt, Default: d.Port, ApplyScope: adapters.ScopeRestartCast},
		{Key: "binary_path", Label: "shairport-sync Path", Help: "Empty looks shairport-sync up on PATH (bundled in the Docker image).", Kind: adapters.KindText, Placeholder: "shairport-sync", ApplyScope: adapters.ScopeRestartCast},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("airplay: decode config: %w", err)
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
		return fmt.Errorf("airplay: decode config: %w", err)
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

// SourceID and Configured light the chassis AIRPLAY lamp.
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
		"port":                a.cfg.Port,
		"binary_path":         a.cfg.BinaryPath,
	}
}

// MountPublicRoutes mounts the loopback PCM route.
func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	a.relay.Mount(mux)
}

func (a *Adapter) Start(context.Context) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !cfg.Enabled || a.receiver.Running() {
		return nil
	}
	return a.startLocked(cfg)
}

// startLocked opens the metadata socket, writes the shairport-sync
// config, and starts the receiver. Caller holds opMu.
func (a *Adapter) startLocked(cfg Config) error {
	if a.goos == "windows" {
		a.receiver.SetStartError(errUnsupportedPlatform)
		return errUnsupportedPlatform
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		err = fmt.Errorf("airplay metadata socket: %w", err)
		a.receiver.SetStartError(err)
		return err
	}
	confPath, err := a.writeConf(cfg, conn.LocalAddr().(*net.UDPAddr).Port)
	if err != nil {
		conn.Close()
		a.receiver.SetStartError(err)
		return err
	}
	// Listen before the helper can send anything.
	done := make(chan struct{})
	go a.listen(conn, done)
	path := strings.TrimSpace(cfg.BinaryPath)
	if path == "" {
		path = "shairport-sync"
	}
	spec := liveaudio.HelperSpec{Path: path, Args: []string{"-c", confPath}}
	if err := a.receiver.Start(spec, sessionOptions(cfg)); err != nil {
		conn.Close()
		<-done
		return err
	}
	a.mu.Lock()
	a.meta, a.metaDone = conn, done
	a.mu.Unlock()
	return nil
}

// writeConf writes the shairport-sync config into the data dir (or a
// temp dir without one) and returns its path.
func (a *Adapter) writeConf(cfg Config, metaPort int) (string, error) {
	dir := filepath.Join(a.dataDir, "airplay")
	if a.dataDir == "" {
		dir = filepath.Join(os.TempDir(), "mister-groovy-relay-airplay")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("airplay config dir: %w", err)
	}
	path := filepath.Join(dir, "shairport-sync.conf")
	if err := os.WriteFile(path, []byte(renderShairportConf(cfg, metaPort)), 0o600); err != nil {
		return "", fmt.Errorf("write shairport-sync config: %w", err)
	}
	return path, nil
}

// listen feeds metadata datagrams through the parser into the receiver
// until conn closes. The socket is bound to loopback, so only local
// processes (shairport-sync) can reach it.
func (a *Adapter) listen(conn net.PacketConn, done chan struct{}) {
	defer close(done)
	var parser metadataParser
	buf := make([]byte, 65536)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Warn("airplay metadata socket", "err", err)
			}
			return
		}
		for _, ev := range parser.packet(buf[:n]) {
			a.receiver.Handle(ev)
		}
	}
}

func (a *Adapter) Stop() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.stopLocked()
	return nil
}

// stopLocked stops the receiver and the metadata listener. Caller holds opMu.
func (a *Adapter) stopLocked() {
	a.receiver.Stop()
	a.mu.Lock()
	conn, done := a.meta, a.metaDone
	a.meta, a.metaDone = nil, nil
	a.mu.Unlock()
	if conn != nil {
		conn.Close()
		<-done
	}
}

func (a *Adapter) Status() adapters.Status { return a.receiver.Status() }

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	next, err := decodeConfig(raw, meta)
	if err != nil {
		return 0, fmt.Errorf("airplay: decode apply config: %w", err)
	}
	if err := next.Validate(); err != nil {
		return 0, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	prev := a.cfg
	a.cfg = next
	a.mu.Unlock()

	scope := scopeForChange(prev, next)
	a.receiver.SetOptions(sessionOptions(next))
	// Enable/disable is handled by the caller's lifecycle hook; a running
	// adapter whose helper settings changed restarts it in place.
	if a.receiver.Running() && next.Enabled && prev.helperSettings() != next.helperSettings() {
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
