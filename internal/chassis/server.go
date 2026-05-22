package chassis

import (
	"fmt"
	"html/template"
	"net/http"
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
}

// Server owns the chassis runtime state.
type Server struct {
	cfg      Config
	session  SessionViewer
	tmpl     *template.Template
	cssBytes []byte // chassis.css with {{.Version}} substituted, cached
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
	return &Server{
		cfg:      cfg,
		session:  cfg.Session,
		tmpl:     tmpl,
		cssBytes: cssBytes,
	}, nil
}

// Mount registers chassis routes on mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /receiver", s.handleIndex)
	mux.HandleFunc("GET /receiver/{$}", s.handleIndex)
	mux.HandleFunc("GET /receiver/static/", s.handleStatic)
	mux.HandleFunc("GET /receiver/events", s.handleEvents)
}
