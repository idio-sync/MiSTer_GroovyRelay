package chassis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
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

	// AudioDSPController is the live tone/EQ runtime (preview + read).
	AudioDSPController AudioDSPController
	// AudioDSPSaver persists committed params + manages EQ memories.
	AudioDSPSaver AudioDSPSaver

	// AudioScopeViewer is the optional read-only source for the latest
	// audio-analysis snapshot. When nil, the chassis emits a pending audio
	// frame on every 30 Hz tick. *core.Manager satisfies this structurally
	// via AudioScopes(); main.go wires that once the manager is live.
	AudioScopeViewer AudioScopeViewer

	AUX AUXStarter

	// PresetViewer is the optional source of the 12-slot chassis preset
	// bank. When nil, the preset bank renders all 12 slots in the
	// .empty state (no name, no badge). 3A wires the streams adapter.
	PresetViewer adapters.PresetViewer

	// PresetCaster is the optional handler for preset slot clicks.
	// When nil, POST /receiver/preset/{slot}/cast returns 404.
	PresetCaster adapters.PresetCaster

	// StreamsCatalogViewer / StreamsCaster: the streams adapter wired
	// through these so the chassis can render the catalog drawer and
	// fire channel casts without importing internal/adapters/streams.
	StreamsCatalogViewer adapters.StreamsCatalogViewer
	StreamsCaster        adapters.StreamsCaster

	// PresetEditor: streams adapter for star-toggle and move operations.
	PresetEditor adapters.PresetEditor

	// SourceAvailabilityViewers: every adapter that implements the
	// interface, in registration order. main.go assembles the slice
	// from the registry. The chassis does NOT inspect the registry
	// directly for source-lamp state — passing the typed slice keeps
	// import_check_test.go happy.
	SourceAvailabilityViewers []adapters.SourceAvailabilityViewer

	// BridgeSaver persists bridge-side settings drawer mutations and
	// exposes the live in-memory bridge config snapshot for renders.
	// Production passes *uiserver.BridgeSaver; the chassis defines its
	// own narrow interface so it does not import internal/uiserver.
	// May be nil in unit-test fixtures; handlers respond 503 NOT READY
	// in that case.
	BridgeSaver BridgeSettingsSaver

	// Prober runs the connectivity probe against the currently-saved
	// MiSTer host/port. Production passes a thin wrapper around
	// cmd/mister-groovy-relay/launcher.go's bridgeMisterProber, which
	// uses CMD_GET_STATUS over an ephemeral source port. May be nil in
	// unit-test fixtures; handlers respond 503 NOT READY in that case.
	Prober Prober

	// CoreLauncher SSH-sends the canonical load_core command to the
	// MiSTer for the Pipeline pane's "Launch core" action button.
	// Production passes the existing bridgeMisterLauncher instance from
	// cmd/mister-groovy-relay/launcher.go — the same launcher already
	// wired into ui.Config.MisterLauncher for /ui/*. May be nil in
	// unit-test fixtures; the handler responds 503 NOT READY when nil.
	CoreLauncher CoreLauncher

	// 4C: catalog pane state mutation + restore-defaults action.
	CatalogManager CatalogSettingsManager
	ConfigReset    ConfigReset

	// 4D: adapter settings pane persistence and streams manifest refresh.
	AdapterSettingsSaver AdapterSettingsSaver
	StreamsRefresher     StreamsRefresher

	// 4E: per-adapter link/pairing flow (Plex PIN, Jellyfin credentials).
	AdapterLinker AdapterLinker

	// 4F: URL adapter custom widgets.
	AdapterHostEditor  AdapterHostEditor
	AdapterCookieStore AdapterCookieStore

	// Local Files adapter custom widgets.
	LocalFiles              LocalFilesService
	LocalFilesLibraryEditor LocalFilesLibraryEditor
}

// Server owns the chassis runtime state.
type Server struct {
	cfg      Config
	firstRun FirstRunController
	session  SessionViewer
	tmpl     *template.Template
	cssBytes []byte
	assetVer string
	meter    *meterSampler

	overlayPanics *overlayPanicLimiter

	transportViewer     TransportViewer
	transportController TransportController

	visualizerViewer   VisualizerViewer
	visualizerSaver    VisualizerSaver
	volumeViewer       VolumeViewer
	volumeSaver        VolumeSaver
	audioDSPController AudioDSPController
	audioDSPSaver      AudioDSPSaver
	audioScopeViewer   AudioScopeViewer
	aux                AUXStarter
	presetViewer       adapters.PresetViewer
	presetCaster       adapters.PresetCaster

	streamsCatalogViewer adapters.StreamsCatalogViewer
	streamsCaster        adapters.StreamsCaster
	presetEditor         adapters.PresetEditor
	sourceViewers        []adapters.SourceAvailabilityViewer

	cache       *snapshotCache
	cacheOnce   sync.Once          // Mount starts the refresher exactly once
	cacheCancel context.CancelFunc // Close() signals the refresher to exit
	cacheDone   chan struct{}      // closed when the refresher goroutine returns

	// streamsRefreshGate enforces single-flight for the /streams-refresh
	// action on this server instance. Per-server (not process-wide) so that
	// tests with independent Server values do not share state.
	streamsRefreshGate sync.Mutex

	// linkStartGates enforces single-flight per adapter for the link/start
	// action. sync.Map keyed by adapter name → *sync.Mutex.
	linkStartGates sync.Map

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
	assetVer, err := staticAssetVersion(cfg.Version)
	if err != nil {
		return nil, err
	}
	cssBytes, err := preprocessCSS(cssSrc, assetVer)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:                  cfg,
		session:              cfg.Session,
		tmpl:                 tmpl,
		cssBytes:             cssBytes,
		assetVer:             assetVer,
		meter:                newMeterSampler(),
		overlayPanics:        newOverlayPanicLimiter(),
		transportViewer:      cfg.TransportViewer,
		transportController:  cfg.TransportController,
		visualizerViewer:     cfg.VisualizerViewer,
		visualizerSaver:      cfg.VisualizerSaver,
		volumeViewer:         cfg.VolumeViewer,
		volumeSaver:          cfg.VolumeSaver,
		audioDSPController:   cfg.AudioDSPController,
		audioDSPSaver:        cfg.AudioDSPSaver,
		audioScopeViewer:     cfg.AudioScopeViewer,
		aux:                  cfg.AUX,
		presetViewer:         cfg.PresetViewer,
		presetCaster:         cfg.PresetCaster,
		streamsCatalogViewer: cfg.StreamsCatalogViewer,
		streamsCaster:        cfg.StreamsCaster,
		presetEditor:         cfg.PresetEditor,
		sourceViewers:        cfg.SourceAvailabilityViewers,
		cache:                &snapshotCache{},
		cacheDone:            make(chan struct{}),
		meterRefusalLog:      &onePerSecondLimiter{},
	}
	s.firstRun = resolveFirstRun(cfg.BridgeSaver)
	// Seed the cache synchronously so the first SSE connection always
	// sees a coherent snapshot — no zero-value VFD or stale state.
	// New deliberately does NOT start a goroutine: unmounted servers
	// (test ergonomics, offline-friendly modes) leak no background work.
	s.cache.Set(s.buildSnapshot(time.Now()))
	return s, nil
}

func staticAssetVersion(base string) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte(base))
	err := fs.WalkDir(chassisStaticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := chassisStaticFS.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(b)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("chassis: hash static assets: %w", err)
	}
	sum := h.Sum(nil)
	return base + "-" + hex.EncodeToString(sum[:])[:12], nil
}

// buildSnapshot composes one ReceiverPageData from current session,
// visualizer, transport, and overlay state. The single shared
// meterSampler advances exactly once per refresher tick so multiple SSE
// tabs do not multiply core reads or sampler drift.
func (s *Server) buildSnapshot(now time.Time) ReceiverPageData {
	if s.session == nil {
		base := idleSnapshot(s.cfg, now)
		base.Version = s.assetVer
		audioLive := audioScopeViewerIsLive(s.audioScopeViewer)
		base.Meter = s.meter.Sample(core.StatusHomeView{State: core.StateIdle}, adapters.MeterOverlay{}, audioLive, now)
		applyAUXSourceState(&base, s.aux)
		applySourceLampState(&base, s.sourceViewers, "", "")
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
	base.Version = s.assetVer
	overlay := s.collectMeterOverlay(context.Background(), view)
	audioLive := audioScopeViewerIsLive(s.audioScopeViewer)
	base.Meter = s.meter.Sample(view, overlay, audioLive, now)
	return base
}

// audioScopeViewerIsLive returns true when the viewer has an active
// snapshot (Generation > 0). Used by the idle path to thread the
// discovery hook through meterSampler.Sample without a session.
func audioScopeViewerIsLive(v AudioScopeViewer) bool {
	if v == nil {
		return false
	}
	snap := v.AudioScopes()
	return snap != nil && snap.Generation > 0
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
	mux.Handle("POST /receiver/audio/dsp", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleAudioDSPPost))))
	mux.Handle("POST /receiver/audio/dsp/memory", requireSameOrigin(http.HandlerFunc(s.handleAudioDSPMemoryPost)))
	mux.Handle("POST /receiver/aux/start", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleAUXStartPost))))
	mux.Handle("POST /receiver/aux/stop", requireSameOrigin(http.HandlerFunc(s.handleAUXStopPost)))
	mux.Handle("POST /receiver/cast", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleCastPost))))
	mux.Handle("POST /receiver/history/play", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleHistoryPlayPost))))
	mux.Handle("POST /receiver/localfiles/browse",
		requireSameOrigin(http.HandlerFunc(s.handleReceiverLocalfilesBrowse)))
	mux.Handle("POST /receiver/localfiles/cast",
		requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleReceiverLocalfilesCast))))
	mux.Handle("POST /receiver/preset/{slot}/cast", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handlePresetCast))))
	mux.Handle("POST /receiver/streams/cast", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleStreamsCast))))
	mux.Handle("POST /receiver/preset/star", requireSameOrigin(http.HandlerFunc(s.handlePresetStar)))
	mux.Handle("POST /receiver/preset/move", requireSameOrigin(http.HandlerFunc(s.handlePresetMove)))
	mux.Handle("POST /receiver/settings/bridge",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsBridgePost)))
	mux.Handle("POST /receiver/settings/action/probe-mister",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsActionProbeMister)))
	mux.Handle("POST /receiver/settings/action/launch-core",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsActionLaunchCore)))
	mux.Handle("POST /receiver/settings/catalog/provider/{id}",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsCatalogProviderPost)))
	mux.Handle("POST /receiver/settings/catalog/direct-stream-hls-buffer",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsCatalogDirectStreamHLSBufferPost)))
	mux.Handle("POST /receiver/settings/action/restore-defaults",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsActionRestoreDefaults)))
	mux.Handle("POST /receiver/settings/adapter/{name}",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterPost)))
	mux.Handle("POST /receiver/settings/action/streams-refresh",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsActionStreamsRefresh)))
	mux.Handle("POST /receiver/settings/adapter/{name}/link/start",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLinkStart)))
	mux.Handle("GET /receiver/settings/adapter/{name}/link/status",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLinkStatus)))
	mux.Handle("POST /receiver/settings/adapter/{name}/link/unlink",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLinkUnlink)))
	mux.Handle("POST /receiver/settings/adapter/{name}/hosts",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterHostsPost)))
	mux.Handle("POST /receiver/settings/adapter/{name}/cookies",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterCookiesPost)))
	mux.Handle("POST /receiver/settings/adapter/{name}/cookies/clear",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterCookiesClear)))
	mux.Handle("POST /receiver/settings/adapter/localfiles/browse",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLocalfilesBrowse)))
	mux.Handle("POST /receiver/settings/adapter/localfiles/cast",
		requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleSettingsAdapterLocalfilesCast))))
	mux.Handle("POST /receiver/settings/adapter/localfiles/libraries",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLocalfilesLibraries)))
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

// refreshSnapshotNow rebuilds the cached snapshot synchronously.
// Successful preset mutations call this so connected SSE clients
// observe the change within one diff-tick rather than waiting for the
// next 250ms refresh. Safe to call from any goroutine; the cache uses
// its own mutex.
func (s *Server) refreshSnapshotNow() {
	s.cache.Set(s.buildSnapshot(time.Now()))
}

// presetSnapshot returns the current 12-slot preset entries from the
// configured PresetViewer, or an empty zero-value array if no viewer
// is wired. Used by handleEvents for the presets SSE event.
func (s *Server) presetSnapshot() [12]adapters.PresetEntry {
	if s.presetViewer == nil {
		var zero [12]adapters.PresetEntry
		for i := range zero {
			zero[i] = adapters.PresetEntry{Slot: i + 1}
		}
		return zero
	}
	return s.presetViewer.Presets()
}

// linkStartGate returns the per-adapter single-flight mutex for link/start.
func (s *Server) linkStartGate(name string) *sync.Mutex {
	v, _ := s.linkStartGates.LoadOrStore(name, &sync.Mutex{})
	return v.(*sync.Mutex)
}
