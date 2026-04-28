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

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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

// Config is the dependencies bundle passed to New. Registry is
// required; BridgeSaver and AdapterSaver are required only for the
// handlers that write state (nil surfaces as a 500 at request time
// so unit tests that only exercise read paths can construct Server
// without them).
type Config struct {
	Registry       *adapters.Registry
	BridgeSaver    BridgeSaver
	AdapterSaver   AdapterSaver
	MisterLauncher MisterLauncher
}

// templateFuncs supplies the tiny set of helpers our templates need.
// Keep this list small — business logic belongs in Go, not templates.
// inc is used by the Bridge panel to render 1-indexed section numbers.
var templateFuncs = template.FuncMap{
	"inc":        func(i int) int { return i + 1 },
	"replaceAll": strings.ReplaceAll,
}

// Server owns the parsed templates + embedded static assets + a
// reference to the adapter registry. Constructed once at startup and
// mounted on the shared HTTP mux.
type Server struct {
	cfg  Config
	tmpl *template.Template
}

func New(cfg Config) (*Server, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("ui: Config.Registry is required")
	}
	tmpl, err := template.New("ui").Funcs(templateFuncs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("ui: parse templates: %w", err)
	}
	return &Server{cfg: cfg, tmpl: tmpl}, nil
}

// Mount registers the UI routes on mux. The mux is expected to be the
// bridge's shared HTTP mux — same listener Plex Companion routes sit
// on. The /ui/ prefix keeps the two sets disjoint.
func (s *Server) Mount(mux *http.ServeMux) {
	// Static assets served out of embedded FS under /ui/static/.
	// GETs don't pass through csrfMiddleware — reads have no side
	// effects, and the middleware short-circuits on GET anyway.
	staticSub, _ := fs.Sub(staticFS, "static")
	staticSrv := http.StripPrefix("/ui/static/", http.FileServer(http.FS(staticSub)))
	mux.Handle("GET /ui/static/", staticSrv)

	// Root + shell. Use {$} to match "/" exactly — a bare "GET /"
	// would be a catch-all that conflicts with adapter-owned prefix
	// routes (e.g., Plex Companion's "/player/") under Go 1.22's
	// method-aware mux.
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /ui/{$}", s.handleShell)
	mux.HandleFunc("GET /ui/", s.handleShell) // subpaths fall through to shell
	mux.HandleFunc("GET /ui", s.handleShell)  // no trailing slash

	// Bridge panel.
	mux.HandleFunc("GET /ui/bridge", s.handleBridgeGET)
	s.mountPOST(mux, "/ui/bridge/save", s.handleBridgePOST)
	s.mountPOST(mux, "/ui/bridge/dismiss-first-run", s.handleBridgeDismissFirstRun)
	s.mountPOST(mux, "/ui/bridge/mister/launch", s.handleBridgeMisterLaunch)

	// Sidebar status fragment (polled every 3s by the shell).
	mux.HandleFunc("GET /ui/sidebar/status", s.handleSidebarStatus)

	// Adapter panel.
	mux.HandleFunc("GET /ui/adapter/{name}", s.handleAdapterGET)
	mux.HandleFunc("GET /ui/adapter/{name}/status", s.handleAdapterStatus)
	s.mountPOST(mux, "/ui/adapter/{name}/toggle", s.handleAdapterToggle)
	s.mountPOST(mux, "/ui/adapter/{name}/save", s.handleAdapterSave)

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
				mux.Handle("GET "+pattern, handler)
			case "POST":
				mux.Handle("POST "+pattern, csrfMiddleware(handler))
			case "DELETE":
				mux.Handle("DELETE "+pattern, csrfMiddleware(handler))
			case "PUT":
				mux.Handle("PUT "+pattern, csrfMiddleware(handler))
			case "PATCH":
				mux.Handle("PATCH "+pattern, csrfMiddleware(handler))
			}
		}
	}
}

// handleSidebarStatus renders the <aside> fragment swapped in every
// 3 s by the shell's hx-trigger. Returns the entire <aside> (not
// just the inner dots) so the outer hx-get + hx-trigger attributes
// survive the outerHTML swap — the sidebar re-registers its own
// polling on every refresh.
func (s *Server) handleSidebarStatus(w http.ResponseWriter, r *http.Request) {
	data := s.shellData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(`<aside class="sidebar" id="sidebar" hx-get="/ui/sidebar/status" hx-trigger="every 3s" hx-swap="outerHTML">`)); err != nil {
		return
	}
	_ = s.tmpl.ExecuteTemplate(w, "sidebar-body", data)
	_, _ = w.Write([]byte(`</aside>`))
}

// mountPOST is the canonical way to register a POST handler on the UI
// mux. Wraps the handler in csrfMiddleware so every write endpoint
// (bridge/save, adapter/save, plex/link/start, etc.) gets the same
// cross-origin protection without each handler having to think about it.
func (s *Server) mountPOST(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	mux.Handle("POST "+pattern, csrfMiddleware(handler))
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
	data := s.shellData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shell.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// renderShellWithPanel renders the full shell page around a panel
// fragment so pushed URLs like /ui/bridge survive refresh/bookmark as
// proper document loads instead of returning a bare fragment.
func (s *Server) renderShellWithPanel(w http.ResponseWriter, panelName string, panelData any) {
	panelHTML, err := s.renderTemplateHTML(panelName, panelData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := s.shellData()
	data.PanelHTML = panelHTML
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shell.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) renderTemplateHTML(name string, data any) (template.HTML, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// shellData builds the template data for the shell page: sidebar
// entries (one per registered adapter) + status-dot classes.
func (s *Server) shellData() shellTemplateData {
	adaptersData := make([]sidebarAdapter, 0)
	for _, a := range s.cfg.Registry.List() {
		st := a.Status()
		adaptersData = append(adaptersData, sidebarAdapter{
			Name:        a.Name(),
			DisplayName: a.DisplayName(),
			DotGlyph:    dotGlyph(st.State),
			DotClass:    dotClass(st.State),
		})
	}
	return shellTemplateData{Adapters: adaptersData}
}

type shellTemplateData struct {
	Adapters  []sidebarAdapter
	PanelHTML template.HTML
}

type sidebarAdapter struct {
	Name        string
	DisplayName string
	DotGlyph    string
	DotClass    string
}

// dotGlyph returns the single-character status indicator for a state.
// Matches the palette conventions in app.css (.dot.run/.starting/.err/
// .off); changing glyphs here also requires updating any template
// that asserts against specific characters.
func dotGlyph(s adapters.State) string {
	switch s {
	case adapters.StateRunning:
		return "●"
	case adapters.StateStarting:
		return "◐"
	case adapters.StateError:
		return "●"
	default:
		return "○"
	}
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

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
