// Package plex is the Plex Companion adapter. It exposes an HTTP server that
// Plex controllers (Plex for iOS/Android/Web, Plexamp) cast to, translates
// Plex Companion requests into adapter-agnostic core.SessionRequest calls,
// and pushes timeline status XML back to subscribers at 1 Hz.
//
// Per spec §4.5 the adapter depends on internal/core/ but core never imports
// back; there is no SourceAdapter interface in v1.
package plex

import (
	"encoding/xml"
	"net/http"

	"github.com/jedivoodoo/mister-groovy-relay/internal/core"
)

// CompanionConfig carries the identity of this device as advertised to Plex
// controllers via /resources and timeline headers.
type CompanionConfig struct {
	DeviceName string
	DeviceUUID string
	Version    string
}

// Companion is the Plex Companion HTTP adapter. One per process. Thread-safe.
type Companion struct {
	cfg      CompanionConfig
	core     SessionManager // adapter-agnostic core.Manager
	timeline *TimelineBroker
}

// SessionManager is the adapter's narrow view of core.Manager. Declared as
// an interface here (rather than importing core.Manager concretely) to keep
// tests in this package mockable without spinning up a real core.
type SessionManager interface {
	StartSession(core.SessionRequest) error
	Pause() error
	Play() error
	Stop() error
	SeekTo(offsetMs int) error
	Status() core.SessionStatus
}

// NewCompanion constructs a Companion. core may be nil for tests that only
// exercise handlers which don't delegate to core (e.g. /resources).
func NewCompanion(cfg CompanionConfig, core SessionManager) *Companion {
	return &Companion{cfg: cfg, core: core}
}

// SetTimeline wires the timeline broker after construction. Done this way
// because Phase 11's NewAdapter instantiates both and cross-links them.
func (c *Companion) SetTimeline(t *TimelineBroker) { c.timeline = t }

// Handler returns the HTTP mux wrapped in the CORS/XML middleware. Mount
// this on the net.Listener returned from Phase 8's discovery code.
func (c *Companion) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/resources", c.handleResources)
	mux.HandleFunc("/player/playback/playMedia", c.handlePlayMedia)
	mux.HandleFunc("/player/playback/pause", c.handlePause)
	mux.HandleFunc("/player/playback/play", c.handlePlay)
	mux.HandleFunc("/player/playback/stop", c.handleStop)
	mux.HandleFunc("/player/playback/seekTo", c.handleSeekTo)
	mux.HandleFunc("/player/playback/setParameters", c.handleSetParameters)
	mux.HandleFunc("/player/playback/setStreams", c.handleSetStreams)
	mux.HandleFunc("/player/timeline/subscribe", c.handleTimelineSubscribe)
	mux.HandleFunc("/player/timeline/unsubscribe", c.handleTimelineUnsubscribe)
	mux.HandleFunc("/player/timeline/poll", c.handleTimelinePoll)
	mux.HandleFunc("/player/mirror/details", c.handleMirrorDetails)
	return withHeaders(mux)
}

// withHeaders injects the CORS + default XML Content-Type headers that all
// Plex Companion responses share. Handlers that emit a non-XML body must
// override Content-Type before writing.
func withHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "X-Plex-Token, X-Plex-Client-Identifier, X-Plex-Device-Name, X-Plex-Product, X-Plex-Version, X-Plex-Platform, X-Plex-Platform-Version, X-Plex-Provides, X-Plex-Protocol, X-Plex-Target-Client-Identifier, Content-Type, Accept")
		w.Header().Set("Content-Type", "text/xml")
		h.ServeHTTP(w, r)
	})
}

// handleResources returns our advertised Plex Companion capabilities. Plex
// controllers fetch this during cast-target discovery.
func (c *Companion) handleResources(w http.ResponseWriter, r *http.Request) {
	type Player struct {
		Title                string `xml:"title,attr"`
		MachineIdentifier    string `xml:"machineIdentifier,attr"`
		ProtocolVersion      string `xml:"protocolVersion,attr"`
		ProtocolCapabilities string `xml:"protocolCapabilities,attr"`
		DeviceClass          string `xml:"deviceClass,attr"`
		Product              string `xml:"product,attr"`
		Platform             string `xml:"platform,attr"`
		PlatformVersion      string `xml:"platformVersion,attr"`
	}
	type MediaContainer struct {
		XMLName xml.Name `xml:"MediaContainer"`
		Size    int      `xml:"size,attr"`
		Player  Player   `xml:"Player"`
	}
	mc := MediaContainer{
		Size: 1,
		Player: Player{
			Title:                c.cfg.DeviceName,
			MachineIdentifier:    c.cfg.DeviceUUID,
			ProtocolVersion:      "1",
			ProtocolCapabilities: "timeline,playback,playqueues",
			DeviceClass:          "stb",
			Product:              "MiSTer_GroovyRelay",
			Platform:             "Linux",
			PlatformVersion:      c.cfg.Version,
		},
	}
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(mc)
}

// Handlers below are stubs filled in by later tasks in this phase (7.4, 7.5).
func (c *Companion) handlePlayMedia(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handlePause(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handlePlay(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleStop(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleSeekTo(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleSetParameters(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleSetStreams(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleTimelineSubscribe(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleTimelineUnsubscribe(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleTimelinePoll(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
func (c *Companion) handleMirrorDetails(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", 501)
}
