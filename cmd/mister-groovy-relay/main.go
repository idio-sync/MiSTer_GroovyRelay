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
	"path/filepath"
	"syscall"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/dlna"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/jellyfin"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	urladapter "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/extbin"
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
	startedAt := time.Now()
	defaultCfg := defaultConfigForRuntime()
	cfgPath := flag.String("config", defaultCfg, "path to config.toml")
	logLevel := flag.String("log-level", "info", "debug|info|warn|error")
	linkFlag := flag.Bool("link", false, "run plex.tv PIN linking and exit")
	linkJellyfin := flag.Bool("link-jellyfin", false, "run Jellyfin pairing flow on stdin and exit")
	flag.Parse()

	slog.SetDefault(logging.New(*logLevel))
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "--config not set and no default available; pass --config explicitly or set MISTER_GROOVY_CONFIG.")
		os.Exit(2)
	}

	sec, err := config.LoadSectioned(*cfgPath)
	if err != nil {
		var created *config.ErrConfigCreated
		if errors.As(err, &created) {
			firstRunMessage(created.Path)
			waitForEnterOnWindows()
			os.Exit(2)
		}
		dieFriendly("load config", err)
	}
	if err := config.EnsureDataDirWritable(sec.Bridge.DataDir); err != nil {
		dieFriendly("data_dir preflight", err)
	}

	selfDir := executableDir()
	ffmpegResolver := extbin.New("ffmpeg", sec.Bridge.FFmpegPath, selfDir)
	ffprobeResolver := extbin.New("ffprobe", sec.Bridge.FFprobePath, selfDir)
	ytdlpResolver := extbin.New("yt-dlp", sec.Bridge.YTDLPPath, selfDir)

	// Apply persisted Debug Logging toggle on top of the boot flag.
	// Precedence: explicit non-default --log-level wins; otherwise the
	// settings-UI checkbox state (saved to bridge.logging.debug)
	// promotes Info to Debug. Lets a power-user override at the
	// command line without losing the UI's hot-swap path.
	if sec.Bridge.Logging.Debug && *logLevel == "info" {
		logging.SetLevel("debug")
	}

	// Token storage lives in the Plex adapter package because v1 only
	// has one adapter that needs persistent auth; future adapters get
	// their own stores. The DeviceUUID survives restarts so Plex
	// controllers don't treat the bridge as a new device each boot.
	store, err := plex.LoadStoredData(sec.Bridge.DataDir)
	if err != nil || store.DeviceUUID == "" {
		store = &plex.StoredData{DeviceUUID: newUUID()}
		if err := plex.SaveStoredData(sec.Bridge.DataDir, store); err != nil {
			dieFriendly("save stored data", err)
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

	// Event log: in-memory ring buffer for status home + diagnostics.
	// Capacity 256 per spec §8.4. Pure synchronous append-and-snapshot;
	// no goroutines.
	elog := eventlog.New(256)

	sender, err := groovynet.NewSender(sec.Bridge.MiSTer.Host, sec.Bridge.MiSTer.Port, sec.Bridge.MiSTer.SourcePort)
	if err != nil {
		dieFriendly("sender init", err)
	}
	defer sender.Close()

	// SendPayload pacing. Defaults to 20 µs per chunk inside NewSender so
	// full RAW-field bursts stay below gigabit line rate. GROOVY_PACING_US
	// overrides at any non-negative value: set to 0 to explicitly disable
	// pacing when profiling shows it's unnecessary on a dedicated link, or
	// to a larger value (25-50) on Wi-Fi / power-line setups that need more
	// receiver-buffer drain time per chunk.
	if v := os.Getenv("GROOVY_PACING_US"); v != "" {
		if us, parseErr := time.ParseDuration(v + "us"); parseErr == nil && us >= 0 {
			sender.SetPacingInterval(us)
			slog.Info("SendPayload pacing override", "interval_us", us.Microseconds())
		} else {
			slog.Warn("invalid GROOVY_PACING_US; using built-in default", "value", v, "err", parseErr)
		}
	}

	coreMgr := core.NewManager(sec.Bridge, sender,
		core.WithBinaryResolvers(ffmpegResolver, ffprobeResolver),
		core.WithEventLog(elog))

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
		EventLog:   elog,
	})
	if err != nil {
		dieFriendly("plex adapter init", err)
	}
	if err := reg.Register(plexAdapter); err != nil {
		dieFriendly("registry register plex", err)
	}

	// URL adapter (v1.1): minimum-viable HTTP/HTTPS URL acceptor with
	// optional yt-dlp resolution. Spec: docs/specs/2026-04-25-url-ytdlp-design.md.
	urlAdapter, err := urladapter.New(urladapter.AdapterConfig{
		Bridge:        sec.Bridge,
		Core:          coreMgr,
		YTDLPResolver: ytdlpResolver,
		EventLog:      elog,
	})
	if err != nil {
		dieFriendly("url adapter init", err)
	}
	if err := reg.Register(urlAdapter); err != nil {
		dieFriendly("registry register url", err)
	}

	// Jellyfin adapter: HTTP-based session control + WebSocket push events.
	// Spec: docs/specs/2026-04-25-jellyfin-adapter-design.md.
	jfAdapter := jellyfin.New(coreMgr, sec.Bridge.DataDir, store.DeviceUUID, sec.Bridge.Video.Modeline, elog)
	jfAdapter.SetVersion(version)
	if err := reg.Register(jfAdapter); err != nil {
		dieFriendly("registry register jellyfin", err)
	}

	// DLNA / UPnP MediaRenderer adapter (Phase 1: descriptors + SSDP +
	// Phase-1 SOAP surface; playback in Phase 2+).
	// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md.
	//
	// Future adapters follow the same construct-then-Register pattern.
	// /dlna/* HTTP routes mount via the PublicRouteProvider walk below;
	// no explicit MountRoutes call is needed here.
	dlnaAdapter, err := dlna.New(dlna.AdapterConfig{
		DeviceUUID: store.DeviceUUID,
		HostIP:     hostIP,
		HTTPPort:   sec.Bridge.UI.HTTPPort,
		Core:       coreMgr,
	})
	if err != nil {
		dieFriendly("dlna adapter init", err)
	}
	if err := reg.Register(dlnaAdapter); err != nil {
		dieFriendly("registry register dlna", err)
	}

	for _, a := range reg.List() {
		raw := sec.Adapters[a.Name()]
		if err := a.DecodeConfig(raw, sec.MetaData()); err != nil {
			dieFriendly("adapter DecodeConfig", err, "name", a.Name())
		}
	}

	// Shared HTTP mux: Plex Companion handlers + Settings UI. One
	// listener, one port (bridge.ui.http_port), disjoint path prefixes
	// (design §7.1). Plex adapter mounts /resources + /player/* ;
	// ui.Server mounts /ui/* and the root redirect.
	mux := http.NewServeMux()
	plexAdapter.MountRoutes(mux)

	// Mount adapter-owned public protocol routes (DLNA SSDP descriptors,
	// SOAP control, GENA SUBSCRIBE) on the shared mux. These paths
	// bypass the settings-UI CSRF middleware that wraps /ui/* because
	// protocol clients (UPnP control points) are not browsers and do not
	// send the headers that middleware expects. Plex's existing explicit
	// MountRoutes call above is the precedent; future adapters should
	// implement PublicRouteProvider instead of an explicit call here.
	for _, a := range reg.List() {
		if pp, ok := a.(adapters.PublicRouteProvider); ok {
			pp.MountPublicRoutes(mux)
		}
	}

	// Bridge + adapter savers live in internal/uiserver so integration
	// tests exercise the same code path the operator hits (review fix
	// C3). Both share one mutex so bridge + adapter saves serialize
	// against each other — both paths read-modify-write the same file.
	saver := uiserver.NewBridgeSaver(*cfgPath, sec, coreMgr, reg, uiserver.ToolResolvers{
		FFmpeg:  ffmpegResolver,
		FFprobe: ffprobeResolver,
		YTDLP:   ytdlpResolver,
	})
	saver.WithEventLog(elog)
	adapterSaver := uiserver.NewAdapterSaver(*cfgPath, saver.Mu())

	misterLauncher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}
	misterProber := bridgeMisterProber{bridge: saver, timeout: 1 * time.Second}

	uiSrv, err := ui.New(ui.Config{
		Registry:         reg,
		BridgeSaver:      saver,
		AdapterSaver:     adapterSaver,
		MisterLauncher:   misterLauncher,
		CompanionSession: coreMgr,
		CompanionURL:     urlAdapter,
		CompanionDisplay: urlAdapter,
		StatusViewer:     coreMgr, // *core.Manager satisfies StatusViewer via StatusHomeView()
		EventLog:         elog,    // in-memory ring buffer constructed above
		Version:          version, // build-time ldflags variable
		MisterProber:     misterProber,
		StartedAt:        startedAt,
	})
	if err != nil {
		dieFriendly("ui init", err)
	}
	uiSrv.Mount(mux)

	addr := fmt.Sprintf(":%d", sec.Bridge.UI.HTTPPort)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		dieFriendly("http listener bind", err)
	}

	// bridge-boot: emitted once the TCP port is bound — the earliest
	// point at which incoming connections are accepted. Per spec §S7,
	// Source "bridge", Severity Info.
	elog.Append(eventlog.Entry{
		Time:     time.Now(),
		Severity: eventlog.SeverityInfo,
		Source:   "bridge",
		Message:  fmt.Sprintf("bridge-boot v%s on %s:%d", version, hostIP, sec.Bridge.UI.HTTPPort),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http listener", "err", err)
		}
	}()

	// Greeter prints once after the listener is bound and adapters are
	// started. Suppressed in JSON mode by default (see banner.go) so
	// log aggregators receive a clean strict-JSON stream.
	printGreeting(logging.IsTextMode(), version, hostIP, sec.Bridge.UI.HTTPPort, reg)

	slog.Info("listening", "addr", addr)

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

func defaultConfigForRuntime() string {
	if v := os.Getenv("MISTER_GROOVY_CONFIG"); v != "" {
		return v
	}
	return config.DefaultConfigPath()
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("resolve executable path; sidecar lookup disabled", "err", err)
		return ""
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	return filepath.Dir(resolved)
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

	pin, err := plex.RequestPIN(store.DeviceUUID, plexCfg.DeviceName, version)
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
