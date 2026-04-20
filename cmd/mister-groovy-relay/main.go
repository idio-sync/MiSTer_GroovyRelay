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
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/jedivoodoo/mister-groovy-relay/internal/adapters/plex"
	"github.com/jedivoodoo/mister-groovy-relay/internal/config"
	"github.com/jedivoodoo/mister-groovy-relay/internal/core"
	"github.com/jedivoodoo/mister-groovy-relay/internal/groovynet"
	"github.com/jedivoodoo/mister-groovy-relay/internal/logging"
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

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

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
	plexAdapter, err := plex.NewAdapter(plex.AdapterConfig{
		Cfg:        cfg,
		Core:       coreMgr,
		TokenStore: store,
		HostIP:     outboundIP(),
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

// runLinkFlow is filled in by Task 11.2. For Task 11.1 it's a stub so the
// --link verb is wired at the CLI layer without requiring the plex.tv
// client calls to be usable yet.
func runLinkFlow(cfg *config.Config, store *plex.StoredData) {
	// See Task 11.2.
	_ = cfg
	_ = store
}
