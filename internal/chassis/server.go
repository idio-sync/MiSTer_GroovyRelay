package chassis

import (
	"fmt"
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
}

// Server owns the chassis runtime state.
type Server struct {
	cfg Config
	// tmpl and cssBytes land in later tasks.
}

// New builds a Server from cfg, validating fields required at startup.
func New(cfg Config) (*Server, error) {
	if cfg.Version == "" {
		return nil, fmt.Errorf("chassis: Config.Version is required")
	}
	if cfg.StartedAt.IsZero() {
		return nil, fmt.Errorf("chassis: Config.StartedAt is required")
	}
	return &Server{cfg: cfg}, nil
}

// Mount registers chassis routes on mux. Route wiring lands in Task 7.
func (s *Server) Mount(mux *http.ServeMux) {
	// Implemented in Task 7.
}
