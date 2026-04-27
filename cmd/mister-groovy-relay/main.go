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
	"syscall"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	urladapter "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/logging"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// version is spliced into the Plex Companion /resources response and
// X-Plex-Version headers. Override at build time with
// -ldflags "-X main.version=...".
var version = "1.0.0"

func main() {
	cfgPath := flag.String("config", "/config/config.toml", "path to config.toml")
	logLevel := flag.String("log-level", "info", "debug|info|warn|error")
	linkFlag := flag.Bool("link", false, "run plex.tv PIN linking and exit")
	linkJellyfin := flag.Bool("link-jellyfin", false, "run Jellyfin pairing flow on stdin and exit")
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

	if *linkFlag && *linkJellyfin {
		fmt.Fprintln(os.Stderr, "error: --link and --link-jellyfin are mutually exclusive; specify at most one")
		os.Exit(2)
	}

	if *linkFlag {
		runLinkFlow(sec, store)
		return
	}

	if *linkJellyfin {
		if err := runLinkJellyfin(sec); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	sender, err := groovynet.NewSender(sec.Bridge.MiSTer.Host, sec.Bridge.MiSTer.Port, sec.Bridge.MiSTer.SourcePort)
	if err != nil {
		slog.Error("sender init", "err", err)
		os.Exit(1)
	}
	defer sender.Close()

	// SendPayload pacing. Defaults to 10 µs per chunk inside NewSender —
	// empirically proven to hold steady 60 Hz on a wired LAN with no
	// receiver-side packet loss. GROOVY_PACING_US overrides at any
	// non-negative value: set to 0 to explicitly disable pacing (when
	// profiling shows it's unnecessary on a dedicated link), or to a
	// larger value (15-50) on Wi-Fi / power-line setups that need more
	// receiver-buffer drain time per chunk.
	if v := os.Getenv("GROOVY_PACING_US"); v != "" {
		if us, parseErr := time.ParseDuration(v + "us"); parseErr == nil && us >= 0 {
			sender.SetPacingInterval(us)
			slog.Info("SendPayload pacing override", "interval_us", us.Microseconds())
		} else {
			slog.Warn("invalid GROOVY_PACING_US; using built-in default", "value", v, "err", parseErr)
		}
	}

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

	// URL adapter (v1.1): minimum-viable HTTP/HTTPS URL acceptor with
	// optional yt-dlp resolution. Spec: docs/specs/2026-04-25-url-ytdlp-design.md.
	urlAdapter, err := urladapter.New(urladapter.AdapterConfig{
		Bridge: sec.Bridge,
		Core:   coreMgr,
	})
	if err != nil {
		slog.Error("url adapter init", "err", err)
		os.Exit(1)
	}
	if err := reg.Register(urlAdapter); err != nil {
		slog.Error("registry register url", "err", err)
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

	// Bridge + adapter savers live in internal/uiserver so integration
	// tests exercise the same code path the operator hits (review fix
	// C3). Both share one mutex so bridge + adapter saves serialize
	// against each other — both paths read-modify-write the same file.
	saver := uiserver.NewBridgeSaver(*cfgPath, sec, coreMgr, reg)
	adapterSaver := uiserver.NewAdapterSaver(*cfgPath, saver.Mu())

	misterLauncher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}

	uiSrv, err := ui.New(ui.Config{
		Registry:       reg,
		BridgeSaver:    saver,
		AdapterSaver:   adapterSaver,
		MisterLauncher: misterLauncher,
	})
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
