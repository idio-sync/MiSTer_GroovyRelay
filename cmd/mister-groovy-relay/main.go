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
	// config.toml via WriteAtomic and updates the in-memory sec.Bridge.
	// Phase 7 will extend Save() to dispatch hot-swap / restart-cast
	// deltas to running adapters; for Phase 4 it only persists and
	// returns ScopeRestartBridge so the UI tells the operator to
	// restart.
	saver := &runtimeBridgeSaver{path: *cfgPath, sec: sec}

	uiSrv, err := ui.New(ui.Config{Registry: reg, BridgeSaver: saver})
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

// runtimeBridgeSaver implements ui.BridgeSaver against the running
// Sectioned config + the on-disk config.toml. Current() returns the
// live in-memory bridge for prefill; Save() overwrites sec.Bridge,
// re-marshals the whole Sectioned, and atomically replaces the file.
//
// Phase 4 returns ScopeRestartBridge unconditionally — every field
// edit shows the "restart the container" toast. Phase 7 will diff
// old vs new, call DropActiveCast for restart-cast fields, and
// probe binds for restart-bridge fields to produce the right scope
// per-save.
type runtimeBridgeSaver struct {
	path string
	sec  *config.Sectioned
	mu   sync.Mutex
}

func (r *runtimeBridgeSaver) Current() config.BridgeConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sec.Bridge
}

func (r *runtimeBridgeSaver) Save(newCfg config.BridgeConfig) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sec.Bridge = newCfg
	buf, err := marshalSectioned(r.sec)
	if err != nil {
		return 0, fmt.Errorf("marshal: %w", err)
	}
	if err := config.WriteAtomic(r.path, buf); err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}
	return adapters.ScopeRestartBridge, nil
}

// marshalSectioned serializes Sectioned back to TOML bytes. BurntSushi
// TOML's encoder skips toml.Primitive values (they go in as opaque
// blobs and come out as empty), so Phase 4 loses per-adapter sections
// on save. That's a known limitation acknowledged by the plan —
// Phase 7 will add a round-trip-safe marshaller. For v1 with only one
// adapter whose fields we don't yet edit through the Bridge save path,
// this is acceptable; the embedded [adapters.plex] section is
// re-seeded from defaults on next startup if it went missing.
func marshalSectioned(sec *config.Sectioned) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(sec); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
