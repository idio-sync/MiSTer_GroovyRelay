// Package ui serves the browser settings UI — HTML fragments rendered
// via html/template, styled with app.css, and driven client-side by
// htmx. Mounts under /ui/ on the shared :http_port listener so Plex
// Companion API routes and the UI share one socket (design §7).
package ui

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/playback"
)

// BridgeSaver abstracts the bridge-level save operation so the UI
// package doesn't depend on main.go's wiring. Current() returns the
// live in-memory BridgeConfig for prefill; Save(new) writes to disk
// and (Phase 7) applies the delta to running adapters, returning
// the scope used.
type BridgeSaver interface {
	Current() config.BridgeConfig
	Save(new config.BridgeConfig) (adapters.ApplyScope, error)
}

// OutputVolumeSaver is an optional BridgeSaver extension for atomic
// single-field volume writes from the global Now Playing banner.
type OutputVolumeSaver interface {
	SaveOutputVolume(volume int) (adapters.ApplyScope, error)
}

// FirstRunAware is an optional extension of BridgeSaver — implement
// it to drive the first-run banner in the Bridge panel. IsFirstRun
// returns true when the dismissal marker is missing (fresh install);
// DismissFirstRun persists the dismissal so subsequent page loads
// hide the banner. Filesystem-based so dismissal survives restart.
type FirstRunAware interface {
	IsFirstRun() bool
	DismissFirstRun() error
}

// AdapterSaver persists an adapter's [adapters.<name>] TOML section
// to disk. The UI package does not know how to marshal back; main.go
// wires a closure that rewrites the section + writes atomically.
// Per-adapter serialization (concurrent saves on the same adapter)
// happens inside the UI package via a small lock map, so
// implementations don't need to coordinate beyond their own file
// I/O.
type AdapterSaver interface {
	Save(name string, rawTOMLSection []byte) error
}

// MisterLauncher abstracts the load-core-over-SSH operation so the
// UI package doesn't depend on internal/misterctl directly. Mirrors
// BridgeSaver / AdapterSaver — main.go wires a real implementation
// (a closure that snapshots live credentials from BridgeSaver and
// calls misterctl.LaunchGroovy). Optional: nil surfaces as 500 at
// click time, so unit tests that don't exercise the launch button
// can construct Server with MisterLauncher=nil.
type MisterLauncher interface {
	Launch(ctx context.Context) error
}

// StatusViewer is the UI's narrow view of core.Manager — just the one
// method status.go needs. Declared here so tests can inject fakes;
// production wires *core.Manager which satisfies via structural typing.
type StatusViewer interface {
	StatusHomeView() core.StatusHomeView
}

// PlaybackService is the narrow interface the /ui playback handlers
// depend on. *playback.Dispatcher satisfies it structurally. Optional
// on Config: when nil, New constructs a Dispatcher from StatusViewer +
// Registry so existing test fixtures don't need updating.
type PlaybackService interface {
	PlaybackView(ctx context.Context) (adapters.PlaybackBannerAdapterView, bool)
	PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool)
	HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error)
}

// MisterProber sends a single safe reachability probe to the configured
// MiSTer. Distinct from MisterLauncher (which loads a core over SSH —
// firing it on every diagnostics click would have side effects on the
// operator's hardware). Production wires this from a closure that
// dials UDP / sends a minimal probe packet that the FPGA either ignores
// or ACKs without engaging the data plane. Returns nil on success,
// error on timeout/unreachable.
type MisterProber interface {
	Probe(ctx context.Context) error
}

// CompanionVolumeViewer reads the live global output volume for the
// companion popup's volume knob. *core.Manager satisfies it via OutputVolume().
type CompanionVolumeViewer interface {
	OutputVolume() int
}

// CompanionVolumeSaver persists a new global output volume (0..100) and
// applies it live. main.go wires the same volumeSaverAdapter used by the chassis.
type CompanionVolumeSaver interface {
	SaveOutputVolume(volume int) error
}

// Config is the dependencies bundle passed to New. Registry is
// required; BridgeSaver and AdapterSaver are required only for the
// handlers that write state (nil surfaces as a 500 at request time
// so unit tests that only exercise read paths can construct Server
// without them).
type Config struct {
	Registry              *adapters.Registry
	BridgeSaver           BridgeSaver
	AdapterSaver          AdapterSaver
	MisterLauncher        MisterLauncher
	CompanionSession      CompanionSessionProvider
	CompanionURL          CompanionURLSource
	CompanionDisplay      CompanionDisplayProvider
	CompanionVolumeViewer CompanionVolumeViewer
	CompanionVolumeSaver  CompanionVolumeSaver
	MisterProber          MisterProber // nil disables reachability probe on diagnostics page
	StatusViewer          StatusViewer // nil disables live data on status home
	Playback              PlaybackService
	EventLog              *eventlog.Log // nil disables activity feed
	Version               string        // build version, displayed in diagnostics
	StartedAt             time.Time     // process start time for status/diagnostics uptime
}

// sectionCtxData is the context object passed to the "field-section"
// template partial. It carries everything the partial needs: the
// section itself, its 0-based index (for sequential section numbering),
// and the enclosing adapter name (for building hx-post URLs and slot
// IDs for KindAction buttons).
type sectionCtxData struct {
	AdapterName string
	Index       int
	Section     bridgeSection
}

// templateFuncs supplies the tiny set of helpers our templates need.
// Keep this list small — business logic belongs in Go, not templates.
//
//	inc        — bridge/adapter panels render 1-indexed section numbers.
//	replaceAll — bridge/adapter panels sanitize KindAction Keys (which
//	             may contain "/") into HTML id attributes.
//	hasString  — bridge panel renders the applied-live pip per
//	             changed key by membership-check on AppliedPipKeys.
//	sectionCtx — adapter-panel template partial assembles per-section
//	             context (adapter name + index + section) into one
//	             value so {{template "field-section" ...}} has all it
//	             needs without multiple top-level bindings.
var templateFuncs = template.FuncMap{
	"inc":        func(i int) int { return i + 1 },
	"replaceAll": strings.ReplaceAll,
	"hasString": func(haystack []string, needle string) bool {
		for _, s := range haystack {
			if s == needle {
				return true
			}
		}
		return false
	},
	"sectionCtx": func(adapterName string, index int, section bridgeSection) sectionCtxData {
		return sectionCtxData{AdapterName: adapterName, Index: index, Section: section}
	},
	"quickCastTabActive":  quickCastTabActive,
	"playbackActionGlyph": playbackActionGlyph,
}

// Server owns the parsed templates + embedded static assets + a
// reference to the adapter registry. Constructed once at startup and
// mounted on the shared HTTP mux.
type Server struct {
	cfg      Config
	tmpl     *template.Template
	playback PlaybackService
	guard    func(http.Handler) http.Handler
}

func New(cfg Config) (*Server, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("ui: Config.Registry is required")
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now()
	}
	tmpl, err := template.New("ui").Funcs(templateFuncs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("ui: parse templates: %w", err)
	}
	if cfg.Playback == nil && cfg.StatusViewer != nil {
		cfg.Playback = playback.NewDispatcher(cfg.StatusViewer, cfg.Registry)
	}
	s := &Server{cfg: cfg, tmpl: tmpl, playback: cfg.Playback}

	// Build the first-run guard once. Guard against nil BridgeSaver before
	// the type assertion — a naked assert on a nil interface panics.
	var firstRun FirstRunAware
	if cfg.BridgeSaver != nil {
		if fra, ok := cfg.BridgeSaver.(FirstRunAware); ok {
			firstRun = fra
		}
	}
	s.guard = firstRunGuard(firstRun)

	return s, nil
}

// Mount registers the UI routes on mux. The mux is expected to be the
// bridge's shared HTTP mux — same listener Plex Companion routes sit
// on. The /ui/ prefix keeps the two sets disjoint.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("OPTIONS /ui", handleExtensionCORSPreflight)
	mux.HandleFunc("OPTIONS /ui/", handleExtensionCORSPreflight)
	mux.Handle("OPTIONS /ui/companion/", companionExtensionGate(http.NotFoundHandler()))
	s.mountCompanion(mux, http.MethodGet, "/ui/companion/status", s.handleCompanionStatus)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/play", s.handleCompanionPlay)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/control", s.handleCompanionControl)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/history/play", s.handleCompanionHistoryPlay)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/history/delete", s.handleCompanionHistoryDelete)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/launch", s.handleCompanionLaunch)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/volume", s.handleCompanionVolume)

	// Static assets served out of embedded FS under /ui/static/.
	// GETs don't pass through csrfMiddleware — reads have no side
	// effects, and the middleware short-circuits on GET anyway.
	// The guard passes /ui/static/* through unconditionally (rule 3),
	// but we still wrap so the middleware chain is consistent.
	staticSub, _ := fs.Sub(staticFS, "static")
	staticSrv := http.StripPrefix("/ui/static/", http.FileServer(http.FS(staticSub)))
	mux.Handle("GET /ui/static/", extensionCORSMiddleware(s.guard(staticSrv)))

	// Root + shell. Use {$} to match "/" exactly — a bare "GET /"
	// would be a catch-all that conflicts with adapter-owned prefix
	// routes (e.g., Plex Companion's "/player/") under Go 1.22's
	// method-aware mux.
	// Root redirect is unguarded: GET / → /ui/ must always work so the
	// operator can reach /ui/setup even before first-run is complete.
	s.mountGETUnguarded(mux, "/{$}", s.handleRoot)
	s.mountGET(mux, "/ui/{$}", s.handleStatusHome)
	s.mountGET(mux, "/ui/status/content", s.handleStatusContent)
	s.mountGET(mux, "/ui/playback/banner", s.handlePlaybackBanner)
	s.mountPOST(mux, "/ui/playback/action", s.handlePlaybackAction)
	s.mountPOST(mux, "/ui/playback/seek", s.handlePlaybackSeek)
	s.mountPOST(mux, "/ui/playback/volume", s.handlePlaybackVolume)
	s.mountPOST(mux, "/ui/playback/quick-cast", s.handlePlaybackQuickCast)
	s.mountGET(mux, "/ui/", s.handleShell) // subpaths fall through to shell
	s.mountGET(mux, "/ui", s.handleShell)  // no trailing slash

	// Bridge panel.
	s.mountGET(mux, "/ui/bridge", s.handleBridgeGET)
	s.mountPOST(mux, "/ui/bridge/save", s.handleBridgePOST)
	s.mountPOST(mux, "/ui/bridge/dismiss-first-run", s.handleBridgeDismissFirstRun)
	s.mountPOST(mux, "/ui/bridge/mister/launch", s.handleBridgeMisterLaunch)

	// Sidebar dots fragment (per-adapter status indicators).
	s.mountGET(mux, "/ui/sidebar/dots", s.handleSidebarDots)

	// Diagnostics page.
	s.mountGET(mux, "/ui/diagnostics", s.handleDiagnosticsGET)
	s.mountPOST(mux, "/ui/diagnostics/probe", s.handleDiagnosticsProbe)

	// Adapter panel.
	s.mountGET(mux, "/ui/adapter/{name}", s.handleAdapterGET)
	s.mountGET(mux, "/ui/adapter/{name}/status", s.handleAdapterStatus)
	s.mountPOST(mux, "/ui/adapter/{name}/toggle", s.handleAdapterToggle)
	s.mountPOST(mux, "/ui/adapter/{name}/save", s.handleAdapterSave)

	// First-run wizard routes. These are wrapped in mountGET/mountPOST
	// which apply firstRunGuard — the guard passes /ui/setup/* through
	// unconditionally (rule 2) so the wizard can render during first-run.
	s.mountGET(mux, "/ui/setup/{$}", s.handleSetupRoot)
	s.mountGET(mux, "/ui/setup", s.handleSetupRoot)
	s.mountGET(mux, "/ui/setup/step/{name}", s.handleSetupStepGET)
	s.mountPOST(mux, "/ui/setup/step/{name}", s.handleSetupStepPOST)
	s.mountPOST(mux, "/ui/setup/done", s.handleSetupDone)

	// Per-adapter routes contributed via RouteProvider (e.g., Plex's
	// link/start, link/status, unlink). Mounted under
	// /ui/adapter/<name>/<route.Path>; POSTs are wrapped in
	// csrfMiddleware uniformly.
	for _, a := range s.cfg.Registry.List() {
		rp, ok := a.(adapters.RouteProvider)
		if !ok {
			continue
		}
		for _, route := range rp.UIRoutes() {
			pattern := fmt.Sprintf("/ui/adapter/%s/%s", a.Name(), route.Path)
			handler := http.HandlerFunc(route.Handler)
			switch route.Method {
			case "GET":
				mux.Handle("GET "+pattern, extensionCORSMiddleware(s.guard(handler)))
			case "POST":
				mux.Handle("POST "+pattern, extensionCORSMiddleware(s.guard(csrfMiddleware(handler))))
			case "DELETE":
				mux.Handle("DELETE "+pattern, extensionCORSMiddleware(s.guard(csrfMiddleware(handler))))
			case "PUT":
				mux.Handle("PUT "+pattern, extensionCORSMiddleware(s.guard(csrfMiddleware(handler))))
			case "PATCH":
				mux.Handle("PATCH "+pattern, extensionCORSMiddleware(s.guard(csrfMiddleware(handler))))
			}
		}
	}
}

// mountPOST is the canonical way to register a POST handler on the UI
// mux. Wraps the handler in csrfMiddleware and firstRunGuard so every
// write endpoint (bridge/save, adapter/save, plex/link/start, etc.)
// gets cross-origin protection and first-run enforcement without each
// handler having to think about it.
func (s *Server) mountPOST(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	mux.Handle("POST "+pattern, extensionCORSMiddleware(s.guard(csrfMiddleware(handler))))
}

// mountGET registers a guarded GET handler. All UI GET routes pass
// through firstRunGuard so unauthenticated first-run state redirects
// to /ui/setup.
func (s *Server) mountGET(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	mux.Handle("GET "+pattern, extensionCORSMiddleware(s.guard(handler)))
}

// mountGETUnguarded registers a GET handler that bypasses firstRunGuard.
// Use ONLY for the root redirect (GET /{$}) so that GET / always
// redirects to /ui/ regardless of first-run state — the operator must
// be able to reach /ui/setup from a bare host URL.
func (s *Server) mountGETUnguarded(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	mux.Handle("GET "+pattern, extensionCORSMiddleware(handler))
}

// handleRoot redirects / to /ui/. Any other path slips through to the
// mux's NotFound handler (which, when the UI mux is also the Plex
// mux, falls through to Plex Companion routes).
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

// handleShell renders the full shell page with the sidebar populated
// from the registry and an empty panel.
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	data := s.shellDataForPath(r.URL.Path)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shell.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// renderShellWithPanel renders the full shell page around a panel
// fragment so pushed URLs like /ui/bridge survive refresh/bookmark as
// proper document loads instead of returning a bare fragment.
func (s *Server) renderShellWithPanel(w http.ResponseWriter, r *http.Request, panelName string, panelData any) {
	panelHTML, err := s.renderTemplateHTML(panelName, panelData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := s.shellDataForPath(r.URL.Path)
	data.PanelHTML = panelHTML
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shell.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) renderPanelWithSidebar(w http.ResponseWriter, r *http.Request, panelName string, panelData any) {
	panelHTML, err := s.renderTemplateHTML(panelName, panelData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sidebarHTML, err := s.renderTemplateHTML("sidebar-body", s.shellDataForPath(r.URL.Path))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, panelHTML)
	fmt.Fprintf(w, `<aside id="gr-sidebar" hx-swap-oob="innerHTML">%s</aside>`, sidebarHTML)
}

func (s *Server) renderTemplateHTML(name string, data any) (template.HTML, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// shellDataForPath is shellData() with CurrentPath populated for
// active-link rendering. Used by every shell-rendering handler;
// the path drives the .active class on the matching sidebar <a>.
func (s *Server) shellDataForPath(path string) shellTemplateData {
	data := s.shellData()
	data.CurrentPath = path
	data.PanelClass = panelClassForPath(path)
	data.Playback = s.buildPlaybackBannerData(context.Background(), playbackRenderOptions{})
	return data
}

// shellData builds the template data for the shell page: sidebar
// entries (one per registered adapter) + status-dot classes.
// All registered adapters are listed — disabled ones render with the
// "off" dot so the operator can navigate to their settings page (and
// enable them) without first running the wizard. Matches the PR2
// preview at C:/tmp/groovyrelay-pr2-preview.html.
func (s *Server) shellData() shellTemplateData {
	adaptersData := make([]sidebarAdapter, 0)
	for _, a := range s.cfg.Registry.List() {
		st := a.Status()
		dc := dotClass(st.State)
		if !a.IsEnabled() {
			dc = "off"
		}
		adaptersData = append(adaptersData, sidebarAdapter{
			Name:        a.Name(),
			DisplayName: a.DisplayName(),
			DotClass:    dc,
		})
	}
	statusDot, statusMeta := s.statusSidebarState()
	return shellTemplateData{
		Adapters:       adaptersData,
		PanelClass:     "panel",
		StatusDotClass: statusDot,
		StatusMeta:     statusMeta,
	}
}

type shellTemplateData struct {
	Adapters       []sidebarAdapter
	PanelHTML      template.HTML
	CurrentPath    string
	PanelClass     string
	StatusDotClass string
	StatusMeta     string
	SetupMode      bool
	Steps          []stepperItem
	Playback       playbackBannerData
}

type sidebarAdapter struct {
	Name        string
	DisplayName string
	DotClass    string
}

// dotClass returns the CSS class for a state (colors the dot).
func dotClass(s adapters.State) string {
	switch s {
	case adapters.StateRunning:
		return "run"
	case adapters.StateStarting:
		return "starting"
	case adapters.StateError:
		return "err"
	default:
		return "off"
	}
}

func panelClassForPath(path string) string {
	switch {
	case path == "/ui/" || path == "/ui/diagnostics":
		return "gr-main"
	case path == "/ui/bridge" || strings.HasPrefix(path, "/ui/adapter/"):
		return "gr-config"
	default:
		return "panel"
	}
}

func (s *Server) statusSidebarState() (dotClass, meta string) {
	if s.cfg.StatusViewer == nil {
		return "off", ""
	}
	switch s.cfg.StatusViewer.StatusHomeView().State {
	case core.StatePlaying:
		return "run", "live"
	case core.StatePaused:
		return "starting", "paused"
	default:
		return "off", ""
	}
}

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
