package chassis

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// Config is the dependencies bundle passed to New.
type Config struct {
	Bridge    config.BridgeConfig
	Manager   *core.Manager
	Registry  *adapters.Registry
	Version   string
	StartedAt time.Time
	HostIP    string

	// Session is the read-only session-state source for live VFD
	// rendering and SSE events. Optional: when nil, the chassis renders
	// idle-only and the /receiver/events stream emits the initial idle
	// snapshot then sits silent. *core.Manager satisfies the interface
	// structurally; main.go wires that.
	Session SessionViewer

	// TransportViewer is the optional read-only playback data source for
	// the chassis transport row. Nil/unowned snapshots render read-only.
	TransportViewer TransportViewer
	// TransportController is the optional playback action dispatcher for
	// the chassis transport row. Later tasks own handlers.
	TransportController TransportController

	// VisualizerViewer is the optional read-only visualizer-mode source.
	// When nil, chassis falls back to config/default mode data.
	VisualizerViewer VisualizerViewer
	// VisualizerSaver is the optional persistence hook for mode changes.
	// When nil, chassis can render mode state but later change handlers
	// should operate in read-only mode.
	VisualizerSaver VisualizerSaver

	// VolumeViewer is the optional read-only source for global output
	// volume. When nil, chassis falls back to startup bridge config.
	VolumeViewer VolumeViewer
	// VolumeSaver is the optional persistence hook for output-volume
	// changes. When nil, chassis renders the knob read-only for POSTs.
	VolumeSaver VolumeSaver

	// AudioScopeViewer is the optional read-only source for the latest
	// audio-analysis snapshot. When nil, the chassis emits a pending audio
	// frame on every 30 Hz tick. *core.Manager satisfies this structurally
	// via AudioScopes(); main.go wires that once the manager is live.
	AudioScopeViewer AudioScopeViewer

	AUX AUXStarter
}

// Server owns the chassis runtime state.
type Server struct {
	cfg      Config
	session  SessionViewer
	tmpl     *template.Template
	cssBytes []byte
	meter    *meterSampler

	overlayPanics *overlayPanicLimiter

	transportViewer     TransportViewer
	transportController TransportController

	visualizerViewer VisualizerViewer
	visualizerSaver  VisualizerSaver
	volumeViewer     VolumeViewer
	volumeSaver      VolumeSaver
	audioScopeViewer AudioScopeViewer
	aux              AUXStarter

	cache       *snapshotCache
	cacheOnce   sync.Once          // Mount starts the refresher exactly once
	cacheCancel context.CancelFunc // Close() signals the refresher to exit
	cacheDone   chan struct{}      // closed when the refresher goroutine returns

	meterRefusalLog *onePerSecondLimiter
}

// New builds a Server from cfg, validating fields required at startup.
func New(cfg Config) (*Server, error) {
	if cfg.Version == "" {
		return nil, fmt.Errorf("chassis: Config.Version is required")
	}
	if cfg.StartedAt.IsZero() {
		return nil, fmt.Errorf("chassis: Config.StartedAt is required")
	}
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	cssSrc, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		return nil, fmt.Errorf("chassis: read embedded chassis.css: %w", err)
	}
	cssBytes, err := preprocessCSS(cssSrc, cfg.Version)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:                 cfg,
		session:             cfg.Session,
		tmpl:                tmpl,
		cssBytes:            cssBytes,
		meter:               newMeterSampler(),
		overlayPanics:       newOverlayPanicLimiter(),
		transportViewer:     cfg.TransportViewer,
		transportController: cfg.TransportController,
		visualizerViewer:    cfg.VisualizerViewer,
		visualizerSaver:     cfg.VisualizerSaver,
		volumeViewer:        cfg.VolumeViewer,
		volumeSaver:         cfg.VolumeSaver,
		audioScopeViewer:    cfg.AudioScopeViewer,
		aux:                 cfg.AUX,
		cache:               &snapshotCache{},
		cacheDone:           make(chan struct{}),
		meterRefusalLog:     &onePerSecondLimiter{},
	}
	// Seed the cache synchronously so the first SSE connection always
	// sees a coherent snapshot — no zero-value VFD or stale state.
	// New deliberately does NOT start a goroutine: unmounted servers
	// (test ergonomics, offline-friendly modes) leak no background work.
	s.cache.Set(s.buildSnapshot(time.Now()))
	return s, nil
}

// buildSnapshot composes one ReceiverPageData from current session,
// visualizer, transport, and overlay state. The single shared
// meterSampler advances exactly once per refresher tick so multiple SSE
// tabs do not multiply core reads or sampler drift.
func (s *Server) buildSnapshot(now time.Time) ReceiverPageData {
	if s.session == nil {
		base := idleSnapshot(s.cfg, now)
		base.Meter = s.meter.Sample(core.StatusHomeView{State: core.StateIdle}, adapters.MeterOverlay{}, now)
		applyAUXSourceState(&base, s.aux)
		base.Visualizer.ActiveMode = liveVisualizerMode(s.cfg, s.visualizerViewer)
		// idleSnapshot already seeded Transport.OutputVolume from cfg; a
		// non-nil volumeViewer overrides with the live value so the knob
		// tracks runtime hot-swaps even without an active cast.
		if s.volumeViewer != nil {
			base.Transport.OutputVolume = s.volumeViewer.OutputVolume()
		}
		return base
	}
	view := s.session.StatusHomeView()
	base := snapshotFromStatusView(s.cfg, view, s.visualizerViewer, s.volumeViewer, s.transportViewer, s.aux, now)
	overlay := s.collectMeterOverlay(context.Background(), view)
	base.Meter = s.meter.Sample(view, overlay, now)
	return base
}

// Mount registers chassis routes on mux and starts the snapshot cache
// refresher exactly once. Safe to call multiple times (sync.Once
// guards the goroutine start) but only the first call wins.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /receiver", s.handleIndex)
	mux.HandleFunc("GET /receiver/{$}", s.handleIndex)
	mux.HandleFunc("GET /receiver/static/", s.handleStatic)
	mux.HandleFunc("GET /receiver/events", s.handleEvents)
	mux.Handle("POST /receiver/transport/action", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleTransportAction))))
	mux.Handle("POST /receiver/transport/seek", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleTransportSeek))))
	mux.Handle("POST /receiver/visualizer", requireSameOrigin(http.HandlerFunc(s.handleVisualizerPost)))
	mux.Handle("POST /receiver/volume", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleVolumePost))))
	mux.Handle("POST /receiver/aux/start", requireSameOrigin(http.HandlerFunc(s.handleAUXStartPost)))
	mux.Handle("POST /receiver/aux/stop", requireSameOrigin(http.HandlerFunc(s.handleAUXStopPost)))
	s.cacheOnce.Do(s.startSnapshotRefresher)
}

// startSnapshotRefresher starts the cache refresher goroutine once.
// Called from Mount via sync.Once so multiple Mounts (defensive) or
// no-Mount paths (tests / unmounted servers) don't spawn extras.
func (s *Server) startSnapshotRefresher() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cacheCancel = cancel
	go func() {
		defer close(s.cacheDone)
		t := time.NewTicker(chassisTickInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.cache.Set(s.buildSnapshot(time.Now()))
			}
		}
	}()
}

// Close stops the snapshot refresher goroutine (if Mount ever started
// one) and waits for it to exit. Safe to call multiple times
// sequentially; do not race with Mount. Calling Close without a prior
// Mount returns nil immediately. Production wires this from main.go's
// shutdown sequence; tests register via t.Cleanup.
func (s *Server) Close() error {
	if s.cacheCancel == nil {
		// Mount never ran — nothing to stop.
		return nil
	}
	// Cancel may be called from multiple Close calls; context.CancelFunc
	// is itself idempotent.
	s.cacheCancel()
	select {
	case <-s.cacheDone:
		// goroutine exited
	case <-time.After(time.Second):
		return fmt.Errorf("chassis: snapshot refresher did not exit within 1s")
	}
	return nil
}
