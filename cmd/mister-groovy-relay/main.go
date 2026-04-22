// Command mister-groovy-relay is the MiSTer GroovyMiSTer Plex adapter bridge.
// It parses config, sets up the GroovyMiSTer UDP sender, constructs the
// adapter-agnostic core.Manager, wires the Plex adapter (Companion HTTP +
// GDM discovery + plex.tv linking + 1 Hz timeline broadcaster), and runs
// until SIGINT/SIGTERM. The --link flag runs the plex.tv PIN pairing flow
// and exits; --config points at the TOML config file.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/logging"
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

	// Flatten the sectioned config into the pre-UI flat shape the data
	// plane and Plex adapter currently read. LoadSectioned has already
	// validated bridge-level fields; Phase 2 (adapter interface) removes
	// this shim when the Plex adapter takes a typed plex.Config directly.
	cfg := sec.ToLegacy()

	// Load or create device UUID + auth token. Token storage lives in the
	// Plex adapter package because v1 only has one adapter that needs
	// persistent auth; future adapters get their own stores.
	store, err := plex.LoadStoredData(cfg.DataDir)
	if err != nil || store.DeviceUUID == "" {
		store = &plex.StoredData{DeviceUUID: newUUID()}
		if err := plex.SaveStoredData(cfg.DataDir, store); err != nil {
			slog.Error("save stored data", "err", err)
			os.Exit(1)
		}
	}
	cfg.DeviceUUID = store.DeviceUUID

	if *linkFlag {
		runLinkFlow(cfg, store)
		return
	}

	sender, err := groovynet.NewSender(cfg.MisterHost, cfg.MisterPort, cfg.SourcePort)
	if err != nil {
		slog.Error("sender init", "err", err)
		os.Exit(1)
	}
	defer sender.Close()

	// Core: adapter-agnostic session manager. Imports no adapters.
	coreMgr := core.NewManager(cfg, sender)

	// Plex adapter: wraps Companion HTTP + GDM discovery + plex.tv linking
	// + timeline broadcaster + HTTP server lifecycle. Takes core.Manager
	// as its session backend. Future adapters (URL-input, Jellyfin) plug
	// in the same way — see spec §4.5.
	hostIP := cfg.HostIP
	if hostIP == "" {
		hostIP = outboundIP()
		slog.Warn("host_ip not set; auto-detected via default route — override in config for multi-NIC hosts",
			"detected", hostIP)
	}
	plexAdapter, err := plex.NewAdapter(plex.AdapterConfig{
		Cfg:        cfg,
		Core:       coreMgr,
		TokenStore: store,
		HostIP:     hostIP,
		Version:    version,
	})
	if err != nil {
		slog.Error("plex adapter init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := plexAdapter.Start(ctx); err != nil {
		slog.Error("plex adapter start", "err", err)
		os.Exit(1)
	}

	<-ctx.Done()
	slog.Info("shutting down")
	plexAdapter.Stop()
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

// runLinkFlow drives the plex.tv PIN pairing dance: request a PIN, print
// it to stdout for the operator to enter at plex.tv/link, poll until the
// user completes the claim, then persist the returned auth token. Writes
// the code to stdout (not stderr) so the operator can pipe it to a QR
// generator or `tee` file. Exits non-zero on any failure; the caller can
// re-run `--link` to retry.
func runLinkFlow(cfg *config.Config, store *plex.StoredData) {
	pin, err := plex.RequestPIN(cfg.DeviceUUID, cfg.DeviceName)
	if err != nil {
		slog.Error("pin request", "err", err)
		os.Exit(1)
	}
	fmt.Printf("Open https://plex.tv/link and enter this code: %s\n", pin.Code)
	token, err := plex.PollPIN(pin.ID, cfg.DeviceUUID, 5*time.Minute)
	if err != nil {
		slog.Error("pin poll", "err", err)
		os.Exit(1)
	}
	store.AuthToken = token
	if err := plex.SaveStoredData(cfg.DataDir, store); err != nil {
		slog.Error("save token", "err", err)
		os.Exit(1)
	}
	fmt.Println("Linked successfully.")
}
