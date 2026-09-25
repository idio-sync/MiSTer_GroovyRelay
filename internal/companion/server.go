package companion

import (
	"context"
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// MisterLauncher loads the Groovy core on the MiSTer (over SSH in
// production). Optional: nil surfaces as a 500 from /ui/companion/launch.
type MisterLauncher interface {
	Launch(ctx context.Context) error
}

// VolumeViewer reads the live global output volume for the companion
// popup's volume knob. *core.Manager satisfies it via OutputVolume().
type VolumeViewer interface {
	OutputVolume() int
}

// VolumeSaver persists a new global output volume (0..100) and applies
// it live. main.go wires the same volumeSaverAdapter used by the chassis.
type VolumeSaver interface {
	SaveOutputVolume(volume int) error
}

// Config is the dependencies bundle passed to New. Registry is
// required; every other field is optional and degrades per-route
// (a nil URL source surfaces as a 500 on the mutating routes, a nil
// Session reports idle, and so on).
type Config struct {
	Registry     *adapters.Registry
	Session      CompanionSessionProvider
	URL          CompanionURLSource
	Display      CompanionDisplayProvider
	VolumeViewer VolumeViewer
	VolumeSaver  VolumeSaver
	Launcher     MisterLauncher
}

// Server serves the /ui/companion/* JSON API consumed by the browser
// extension (extension/firefox). It lives at /ui/companion/* for
// compatibility with installed extensions; the chassis owns the rest
// of /ui/*.
type Server struct {
	cfg Config
}

func New(cfg Config) (*Server, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("companion: Config.Registry is required")
	}
	return &Server{cfg: cfg}, nil
}

// Mount registers the companion routes plus the OPTIONS /ui/companion/
// preflight on mux (the bridge's shared HTTP mux).
func (s *Server) Mount(mux *http.ServeMux) {
	mux.Handle("OPTIONS /ui/companion/", companionExtensionGate(http.NotFoundHandler()))
	s.mountCompanion(mux, http.MethodGet, "/ui/companion/status", s.handleCompanionStatus)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/play", s.handleCompanionPlay)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/control", s.handleCompanionControl)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/history/play", s.handleCompanionHistoryPlay)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/history/delete", s.handleCompanionHistoryDelete)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/launch", s.handleCompanionLaunch)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/volume", s.handleCompanionVolume)
}
