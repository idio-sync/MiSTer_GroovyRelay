// Command mister-groovy-relay is the MiSTer GroovyMiSTer adapter bridge.
// It parses the sectioned config, constructs the GroovyMiSTer UDP sender,
// builds the adapter-agnostic core.Manager, populates the adapter
// registry, decodes per-adapter config, binds one HTTP listener on
// bridge.ui.http_port that both the Plex Companion API and the Settings
// UI share, and starts every enabled adapter. Shutdown on SIGINT/SIGTERM
// drains the HTTP server and then iterates the registry in registration
// order. The --link flag runs the plex.tv PIN pairing flow and exits.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/logging"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

// version is spliced into the Plex Companion /resources response and
// X-Plex-Version headers. Override at build time with
// -ldflags "-X main.version=...".
var version = "1.0.0"

func main() {
	cfgPath := flag.String("config", "/config/config.toml", "path to config.toml")
	logLevel := flag.String("log-level", "info", "debug|info|warn|error")
	linkFlag := flag.Bool("link", false, "run plex.tv PIN linking and exit")
	flag.Parse()

	slog.SetDefault(logging.New(*logLevel))

	sec, err := config.LoadSectioned(*cfgPath)
	if err != nil {
		var created *config.ErrConfigCreated
		if errors.As(err, &created) {
			fmt.Fprintf(os.Stderr,
				"No config found. Wrote defaults to %s.\nEdit it (set bridge.mister.host) and restart.\n",
				created.Path)
			os.Exit(2)
		}
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	// Token storage lives in the Plex adapter package because v1 only
	// has one adapter that needs persistent auth; future adapters get
	// their own stores. The DeviceUUID survives restarts so Plex
	// controllers don't treat the bridge as a new device each boot.
	store, err := plex.LoadStoredData(sec.Bridge.DataDir)
	if err != nil || store.DeviceUUID == "" {
		store = &plex.StoredData{DeviceUUID: newUUID()}
		if err := plex.SaveStoredData(sec.Bridge.DataDir, store); err != nil {
			slog.Error("save stored data", "err", err)
			os.Exit(1)
		}
	}

	if *linkFlag {
		runLinkFlow(sec, store)
		return
	}

	sender, err := groovynet.NewSender(sec.Bridge.MiSTer.Host, sec.Bridge.MiSTer.Port, sec.Bridge.MiSTer.SourcePort)
	if err != nil {
		slog.Error("sender init", "err", err)
		os.Exit(1)
	}
	defer sender.Close()

	coreMgr := core.NewManager(sec.Bridge, sender)

	hostIP := sec.Bridge.HostIP
	if hostIP == "" {
		hostIP = outboundIP()
		slog.Warn("host_ip not set; auto-detected via default route — override in config for multi-NIC hosts",
			"detected", hostIP)
	}

	// Build the registry. Future adapters (URL-input, Jellyfin) plug in
	// here with the same shape: construct + Register. DecodeConfig runs
	// in a second pass so Register order (which determines sidebar
	// order) is independent of decode ordering.
	reg := adapters.NewRegistry()

	plexAdapter, err := plex.NewAdapter(plex.AdapterConfig{
		Bridge:     sec.Bridge,
		Core:       coreMgr,
		TokenStore: store,
		HostIP:     hostIP,
		Version:    version,
	})
	if err != nil {
		slog.Error("plex adapter init", "err", err)
		os.Exit(1)
	}
	if err := reg.Register(plexAdapter); err != nil {
		slog.Error("registry register plex", "err", err)
		os.Exit(1)
	}

	for _, a := range reg.List() {
		raw := sec.Adapters[a.Name()]
		if err := a.DecodeConfig(raw, sec.MetaData()); err != nil {
			slog.Error("adapter DecodeConfig", "name", a.Name(), "err", err)
			os.Exit(1)
		}
	}

	// Shared HTTP mux: Plex Companion handlers + Settings UI. One
	// listener, one port (bridge.ui.http_port), disjoint path prefixes
	// (design §7.1). Plex adapter mounts /resources + /player/* ;
	// ui.Server mounts /ui/* and the root redirect.
	mux := http.NewServeMux()
	plexAdapter.MountRoutes(mux)

	// runtimeBridgeSaver persists Bridge changes to the on-disk
	// config.toml, updates the in-memory sec.Bridge + core.Manager's
	// bridge copy, and dispatches per-field scope (hot-swap for
	// interlace_field_order, restart-cast for video/audio pipeline
	// fields, restart-bridge for port/path fields with pre-flight
	// probes).
	saver := &runtimeBridgeSaver{
		path:     *cfgPath,
		sec:      sec,
		core:     coreMgr,
		registry: reg,
	}

	// runtimeAdapterSaver rewrites the [adapters.<name>] section of
	// the on-disk config.toml via a line-level splice (not a full
	// re-encode). Shares the bridge mutex so bridge + adapter saves
	// serialize against each other — both paths read then write the
	// same file.
	adapterSaver := &runtimeAdapterSaver{path: *cfgPath, mu: &saver.mu}

	uiSrv, err := ui.New(ui.Config{Registry: reg, BridgeSaver: saver, AdapterSaver: adapterSaver})
	if err != nil {
		slog.Error("ui init", "err", err)
		os.Exit(1)
	}
	uiSrv.Mount(mux)

	addr := fmt.Sprintf(":%d", sec.Bridge.UI.HTTPPort)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("listening", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http listener", "err", err)
		}
	}()

	// Start each enabled adapter's background work (timeline, GDM,
	// plex.tv registration). HTTP handlers were already mounted above.
	for _, a := range reg.List() {
		if !a.IsEnabled() {
			slog.Info("adapter disabled", "name", a.Name())
			continue
		}
		if err := a.Start(ctx); err != nil {
			slog.Error("adapter start", "name", a.Name(), "err", err)
		}
	}

	<-ctx.Done()
	slog.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	for _, a := range reg.List() {
		if err := a.Stop(); err != nil {
			slog.Warn("adapter stop", "name", a.Name(), "err", err)
		}
	}
}

// newUUID returns a crypto/rand-based UUID v4 string. Panics on rand
// failure because the bridge can't function without a stable device
// identifier and a working PRNG is a baseline expectation.
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("uuid: %w", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10 (RFC 4122)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// outboundIP returns the local IP the kernel would use for an outbound
// packet to a well-known external address. No packet is actually sent —
// net.Dial on UDP just resolves the route and binds a local socket.
// Returns "" on failure (offline host); callers treat empty as "skip
// plex.tv registration" so the bridge still runs on the LAN via GDM.
func outboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		slog.Warn("outboundIP: no route", "err", err)
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// runtimeBridgeSaver implements ui.BridgeSaver with real per-field
// scope dispatch (design §9). Current() returns the live in-memory
// bridge for prefill; Save() diffs old vs new, runs pre-flight
// probes for bindable restart-bridge fields, persists to disk via
// a bridge-only rewrite (preserves adapter sections), and dispatches
// the runtime side effects: hot-swap interlace, drop cast for
// restart-cast, no-op for restart-bridge (user restarts the
// container; toast surfaces the command).
type runtimeBridgeSaver struct {
	path     string
	sec      *config.Sectioned
	core     *core.Manager
	registry *adapters.Registry
	mu       sync.Mutex
}

func (r *runtimeBridgeSaver) Current() config.BridgeConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sec.Bridge
}

// firstRunMarker is the dot-prefixed sentinel file placed in data_dir
// once the operator dismisses the first-run banner. Missing = banner
// still shows; present = banner hidden. Filesystem-based so dismissal
// survives process restart without mutating config.toml.
const firstRunMarker = ".first-run-complete"

// IsFirstRun implements ui.FirstRunAware. True until DismissFirstRun
// runs for the first time.
func (r *runtimeBridgeSaver) IsFirstRun() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := os.Stat(filepath.Join(r.sec.Bridge.DataDir, firstRunMarker))
	return os.IsNotExist(err)
}

// DismissFirstRun implements ui.FirstRunAware. Writes the sentinel
// file so subsequent page loads skip the quick-start banner.
func (r *runtimeBridgeSaver) DismissFirstRun() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := filepath.Join(r.sec.Bridge.DataDir, firstRunMarker)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}

func (r *runtimeBridgeSaver) Save(newCfg config.BridgeConfig) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.sec.Bridge
	changed := diffBridgeConfig(old, newCfg)

	scope := adapters.ScopeHotSwap
	for _, k := range changed {
		scope = adapters.MaxScope(scope, scopeForBridgeField(k))
	}

	// Pre-flight probes for bindable restart-bridge fields. Fails
	// the whole save — the file is untouched, matches the "validate
	// before write" contract from the bridge panel.
	if scope == adapters.ScopeRestartBridge {
		if containsStr(changed, "ui.http_port") && newCfg.UI.HTTPPort != old.UI.HTTPPort {
			if err := config.ProbeTCPPort(newCfg.UI.HTTPPort); err != nil {
				return 0, fmt.Errorf("ui.http_port pre-flight failed: %w", err)
			}
		}
		if containsStr(changed, "mister.source_port") && newCfg.MiSTer.SourcePort != old.MiSTer.SourcePort {
			if err := config.ProbeUDPPort(newCfg.MiSTer.SourcePort); err != nil {
				return 0, fmt.Errorf("mister.source_port pre-flight failed: %w", err)
			}
		}
		if containsStr(changed, "data_dir") && newCfg.DataDir != old.DataDir {
			if err := config.ProbeDirWritable(newCfg.DataDir); err != nil {
				return 0, fmt.Errorf("data_dir pre-flight failed: %w", err)
			}
		}
	}

	// Persist to disk (write-before-apply).
	r.sec.Bridge = newCfg
	r.core.UpdateBridge(newCfg)
	buf, err := marshalBridgeSection(r.sec, r.path)
	if err != nil {
		return 0, fmt.Errorf("marshal: %w", err)
	}
	if err := config.WriteAtomic(r.path, buf); err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}

	// Apply per scope.
	switch scope {
	case adapters.ScopeHotSwap:
		if containsStr(changed, "video.interlace_field_order") {
			if err := r.core.SetInterlaceFieldOrder(newCfg.Video.InterlaceFieldOrder); err != nil {
				return 0, fmt.Errorf("interlace hot-swap: %w", err)
			}
		}

	case adapters.ScopeRestartCast:
		// UpdateBridge already ran above so the next session picks
		// up the new aspect_mode / audio settings. Now drop the
		// active cast so the pipeline rebuilds.
		if err := r.core.DropActiveCast("bridge config change"); err != nil {
			return scope, fmt.Errorf("drop-cast: %w", err)
		}

	case adapters.ScopeRestartBridge:
		// Nothing to do at runtime; file is persisted, UI flashes
		// restart-required toast with the docker command.
	}

	return scope, nil
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// marshalBridgeSection rewrites only the [bridge*] tables of the
// TOML file, preserving [adapters.*] sections intact. Avoids
// round-tripping toml.Primitive values through the encoder. Side
// effect: the new [bridge] block always lands at EOF regardless of
// where it was originally — TOML is order-insensitive so the bridge
// still reads it correctly, but users inspecting the file will see
// the re-ordering after first save. v2: splice at original position.
func marshalBridgeSection(sec *config.Sectioned, path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	without := stripBridgeSections(data)

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(struct {
		Bridge config.BridgeConfig `toml:"bridge"`
	}{sec.Bridge}); err != nil {
		return nil, err
	}
	return append(append(without, []byte("\n")...), buf.Bytes()...), nil
}

// stripBridgeSections removes every line from the first "[bridge"
// header through the next non-bridge header (or EOF).
func stripBridgeSections(doc []byte) []byte {
	lines := strings.Split(string(doc), "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, ln := range lines {
		tr := strings.TrimSpace(ln)
		if strings.HasPrefix(tr, "[bridge") {
			skipping = true
			continue
		}
		if skipping && strings.HasPrefix(tr, "[") && strings.HasSuffix(tr, "]") {
			skipping = false
		}
		if !skipping {
			out = append(out, ln)
		}
	}
	return []byte(strings.Join(out, "\n"))
}

func diffBridgeConfig(oldCfg, newCfg config.BridgeConfig) []string {
	var keys []string
	if oldCfg.DataDir != newCfg.DataDir {
		keys = append(keys, "data_dir")
	}
	if oldCfg.HostIP != newCfg.HostIP {
		keys = append(keys, "host_ip")
	}
	if oldCfg.Video.Modeline != newCfg.Video.Modeline {
		keys = append(keys, "video.modeline")
	}
	if oldCfg.Video.InterlaceFieldOrder != newCfg.Video.InterlaceFieldOrder {
		keys = append(keys, "video.interlace_field_order")
	}
	if oldCfg.Video.AspectMode != newCfg.Video.AspectMode {
		keys = append(keys, "video.aspect_mode")
	}
	if oldCfg.Video.RGBMode != newCfg.Video.RGBMode {
		keys = append(keys, "video.rgb_mode")
	}
	if oldCfg.Video.LZ4Enabled != newCfg.Video.LZ4Enabled {
		keys = append(keys, "video.lz4_enabled")
	}
	if oldCfg.Audio.SampleRate != newCfg.Audio.SampleRate {
		keys = append(keys, "audio.sample_rate")
	}
	if oldCfg.Audio.Channels != newCfg.Audio.Channels {
		keys = append(keys, "audio.channels")
	}
	if oldCfg.MiSTer.Host != newCfg.MiSTer.Host {
		keys = append(keys, "mister.host")
	}
	if oldCfg.MiSTer.Port != newCfg.MiSTer.Port {
		keys = append(keys, "mister.port")
	}
	if oldCfg.MiSTer.SourcePort != newCfg.MiSTer.SourcePort {
		keys = append(keys, "mister.source_port")
	}
	if oldCfg.UI.HTTPPort != newCfg.UI.HTTPPort {
		keys = append(keys, "ui.http_port")
	}
	return keys
}

func scopeForBridgeField(key string) adapters.ApplyScope {
	switch key {
	case "video.interlace_field_order":
		return adapters.ScopeHotSwap
	case "video.modeline",
		"video.aspect_mode",
		"video.rgb_mode",
		"video.lz4_enabled",
		"audio.sample_rate",
		"audio.channels":
		return adapters.ScopeRestartCast
	default:
		// mister.*, host_ip, data_dir, ui.http_port — all restart-bridge.
		return adapters.ScopeRestartBridge
	}
}

// runtimeAdapterSaver replaces the [adapters.<name>] section of the
// on-disk config.toml with a new TOML snippet. Uses a line-level
// rewrite (replaceAdapterSection) rather than re-encoding the whole
// Sectioned — BurntSushi's encoder doesn't round-trip toml.Primitive
// values faithfully, so a full re-encode would lose adapter sections
// the UI doesn't currently touch.
type runtimeAdapterSaver struct {
	path string
	mu   *sync.Mutex // shared with runtimeBridgeSaver
}

func (r *runtimeAdapterSaver) Save(name string, rawTOMLSection []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	updated := replaceAdapterSection(data, name, rawTOMLSection)
	return config.WriteAtomic(r.path, updated)
}

// replaceAdapterSection rewrites (or appends) the [adapters.<name>]
// block inside doc. The section is matched by exact header line; its
// body extends to the next [header] line or EOF. The replacement
// section is normalized to end with exactly one newline before
// splicing so repeated saves don't accumulate blank lines or run
// adjacent lines together.
func replaceAdapterSection(doc []byte, name string, section []byte) []byte {
	section = bytes.TrimRight(section, "\r\n\t ")
	section = append(section, '\n')

	header := fmt.Sprintf("[adapters.%s]", name)
	lines := strings.Split(string(doc), "\n")

	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == header {
			start = i
			break
		}
	}

	if start < 0 {
		// Append. Ensure doc ends with a newline before concatenating.
		out := strings.TrimRight(string(doc), "\r\n\t ") + "\n\n"
		out += header + "\n" + string(section)
		return []byte(out)
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		tr := strings.TrimSpace(lines[i])
		if strings.HasPrefix(tr, "[") && strings.HasSuffix(tr, "]") {
			end = i
			break
		}
	}

	newLines := append([]string{}, lines[:start+1]...)
	sectionLines := strings.Split(strings.TrimRight(string(section), "\n"), "\n")
	newLines = append(newLines, sectionLines...)
	if end < len(lines) {
		newLines = append(newLines, "")
	}
	newLines = append(newLines, lines[end:]...)
	return []byte(strings.Join(newLines, "\n"))
}

// runLinkFlow drives the plex.tv PIN pairing dance: request a PIN,
// print it to stdout for the operator to enter at plex.tv/link, poll
// until the user completes the claim, then persist the returned auth
// token. The device name surfaced to plex.tv comes from the
// [adapters.plex] section (falls back to "MiSTer" if unset). Writes
// the code to stdout so it can be piped to `tee` or a QR generator.
// Exits non-zero on any failure; the caller can re-run `--link` to
// retry.
func runLinkFlow(sec *config.Sectioned, store *plex.StoredData) {
	var plexCfg plex.Config
	if raw, ok := sec.Adapters["plex"]; ok {
		meta := sec.MetaData()
		_ = meta.PrimitiveDecode(raw, &plexCfg)
	}
	if plexCfg.DeviceName == "" {
		plexCfg.DeviceName = "MiSTer"
	}

	pin, err := plex.RequestPIN(store.DeviceUUID, plexCfg.DeviceName)
	if err != nil {
		slog.Error("pin request", "err", err)
		os.Exit(1)
	}
	fmt.Printf("Open https://plex.tv/link and enter this code: %s\n", pin.Code)
	token, err := plex.PollPIN(pin.ID, store.DeviceUUID, 5*time.Minute)
	if err != nil {
		slog.Error("pin poll", "err", err)
		os.Exit(1)
	}
	store.AuthToken = token
	if err := plex.SaveStoredData(sec.Bridge.DataDir, store); err != nil {
		slog.Error("save token", "err", err)
		os.Exit(1)
	}
	fmt.Println("Linked successfully.")
}
