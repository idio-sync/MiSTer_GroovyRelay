package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jedivoodoo/mister-groovy-relay/internal/config"
)

// AdapterConfig bundles everything the Plex adapter needs to stand up the
// Companion HTTP server, GDM discovery, the 1 Hz timeline broadcaster, and
// (when a stored auth token is present) the plex.tv device-registration
// loop. Kept in one struct so cmd/mister-groovy-relay/main.go has a single,
// obvious call-site for adapter wiring.
type AdapterConfig struct {
	// Cfg is the parsed application config. Must be non-nil.
	Cfg *config.Config
	// Core is the adapter-agnostic session manager. core.Manager satisfies
	// this via structural typing; declared as SessionManager (defined in
	// companion.go) so tests can inject fakes. Must be non-nil.
	Core SessionManager
	// TokenStore carries the persisted device UUID + plex.tv auth token.
	// When AuthToken is empty the registration loop is skipped — the bridge
	// still serves the LAN via GDM, but won't show up on mobile/cellular.
	TokenStore *StoredData
	// HostIP is the LAN address advertised to plex.tv in the registration
	// loop. Empty string disables registration even if a token is present
	// (the caller typically passes outboundIP()).
	HostIP string
	// Version is the build version string spliced into the /resources
	// platformVersion field and X-Plex-Version headers.
	Version string
}

// Adapter owns the lifecycle of every Plex subsystem: the Companion HTTP
// server, the GDM multicast responder, the timeline broadcaster, and the
// plex.tv registration loop. Start returns quickly after spawning the
// background goroutines; Stop blocks until they exit cleanly.
//
// There is deliberately no universal SourceAdapter interface yet (spec
// §4.5); future adapters (URL-input, Jellyfin) will match this shape
// conceptually but are expected to be wired by main.go directly.
type Adapter struct {
	cfg       AdapterConfig
	companion *Companion
	timeline  *TimelineBroker

	// disco is nil if GDM init fails (e.g. port 32412 already bound by
	// another Plex player on the host). We log and continue — out-of-LAN
	// registration still works if a token is configured.
	disco *Discovery

	httpSrv *http.Server
	srvWG   sync.WaitGroup

	// regCancel cancels the plex.tv registration loop; nil when no loop
	// was started (missing token / host IP).
	regCancel context.CancelFunc

	// discoDone is closed when Discovery.Run exits so Stop can wait for
	// the goroutine cleanly. Nil when GDM discovery isn't running.
	discoDone chan struct{}
}

// NewAdapter constructs a ready-to-Start Adapter. It instantiates the
// Companion + TimelineBroker and cross-wires them, but defers all network
// and goroutine work to Start so constructor errors are purely
// configuration errors.
func NewAdapter(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Cfg == nil {
		return nil, errors.New("plex.NewAdapter: AdapterConfig.Cfg is required")
	}
	if cfg.Core == nil {
		return nil, errors.New("plex.NewAdapter: AdapterConfig.Core is required")
	}
	companion := NewCompanion(CompanionConfig{
		DeviceName: cfg.Cfg.DeviceName,
		DeviceUUID: cfg.Cfg.DeviceUUID,
		Version:    cfg.Version,
	}, cfg.Core)
	timeline := NewTimelineBroker(
		TimelineConfig{DeviceUUID: cfg.Cfg.DeviceUUID, DeviceName: cfg.Cfg.DeviceName},
		cfg.Core.Status,
	)
	companion.SetTimeline(timeline)
	return &Adapter{cfg: cfg, companion: companion, timeline: timeline}, nil
}

// Start brings the Plex adapter online. Order: HTTP server first (so any
// immediate discovery reply lands on a live port), then timeline
// broadcaster, then GDM discovery, then optional plex.tv registration.
// Returns quickly; background goroutines run until Stop.
func (a *Adapter) Start(ctx context.Context) error {
	// 1. HTTP server for Plex Companion endpoints.
	addr := fmt.Sprintf(":%d", a.cfg.Cfg.HTTPPort)
	a.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           a.companion.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	a.srvWG.Add(1)
	go func() {
		defer a.srvWG.Done()
		if err := a.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("plex HTTP server exited", "err", err)
		}
	}()
	slog.Info("plex Companion listening", "addr", addr)

	// 2. Timeline broadcaster (1 Hz push loop). Runs until Stop closes the
	// broker's stop channel.
	go a.timeline.RunBroadcastLoop()

	// 3. GDM multicast discovery. Best-effort: port conflicts shouldn't
	// take down the adapter — out-of-LAN registration may still succeed.
	disco, err := NewDiscovery(DiscoveryConfig{
		DeviceName: a.cfg.Cfg.DeviceName,
		DeviceUUID: a.cfg.Cfg.DeviceUUID,
		HTTPPort:   a.cfg.Cfg.HTTPPort,
	})
	if err != nil {
		slog.Warn("GDM discovery disabled", "err", err)
	} else {
		a.disco = disco
		a.discoDone = make(chan struct{})
		go func() {
			defer close(a.discoDone)
			disco.Run()
		}()
		slog.Info("GDM discovery active", "port", 32412)
	}

	// 4. plex.tv device registration loop. Requires a linked auth token
	// and an outbound IP to advertise; otherwise we log and skip so the
	// user knows to run `--link`.
	if a.cfg.TokenStore != nil && a.cfg.TokenStore.AuthToken != "" && a.cfg.HostIP != "" {
		regCtx, cancel := context.WithCancel(ctx)
		a.regCancel = cancel
		go RunRegistrationLoop(regCtx,
			a.cfg.TokenStore.DeviceUUID,
			a.cfg.TokenStore.AuthToken,
			a.cfg.HostIP,
			a.cfg.Cfg.HTTPPort,
		)
		slog.Info("plex.tv device registration loop started", "hostIP", a.cfg.HostIP)
	} else {
		slog.Info("plex.tv registration skipped (no auth token; run with --link)")
	}
	return nil
}

// Stop tears everything down in reverse-dependency order and blocks until
// every background goroutine has exited. Idempotent-ish: safe to call once
// after a successful Start; calling twice on the same Adapter is not
// supported (the HTTP server can only be shut down once).
func (a *Adapter) Stop() {
	if a.regCancel != nil {
		a.regCancel()
	}
	if a.disco != nil {
		_ = a.disco.Close()
		if a.discoDone != nil {
			<-a.discoDone
		}
	}
	if a.timeline != nil {
		a.timeline.Stop()
	}
	if a.httpSrv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.httpSrv.Shutdown(shutCtx)
		a.srvWG.Wait()
	}
}
