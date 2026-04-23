// Package plex is the Plex Companion adapter. It exposes an HTTP server that
// Plex controllers (Plex for iOS/Android/Web, Plexamp) cast to, translates
// Plex Companion requests into adapter-agnostic core.SessionRequest calls,
// and pushes timeline status XML back to subscribers at 1 Hz.
//
// Per spec §4.5 the adapter depends on internal/core/ but core never imports
// back; there is no SourceAdapter interface in v1.
package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const (
	companionProduct  = "MiSTer_GroovyRelay"
	companionPlatform = "Linux"
	companionDevice   = "MiSTer"
	companionModel    = "MiSTer"
	companionProvides = "player"
	companionProtocol = "1.0"
)

// CompanionConfig carries the identity of this device as advertised to Plex
// controllers via /resources and timeline headers.
type CompanionConfig struct {
	DeviceName string
	DeviceUUID string
	Version    string
	// ProfileName is the Plex client profile name advertised back to PMS when
	// requesting a transcode start URL.
	ProfileName string
	// DataDir is the application data directory used to store downloaded
	// subtitle files under <DataDir>/subtitles/. Populated from config.Config.
	DataDir string
}

// Companion is the Plex Companion HTTP adapter. One per process. Thread-safe.
type Companion struct {
	cfg      CompanionConfig
	core     SessionManager // adapter-agnostic core.Manager
	timeline *TimelineBroker

	sessMu   sync.Mutex
	lastPlay PlayMediaRequest
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

func (c *Companion) notifyTimeline() {
	if c.timeline != nil {
		go c.timeline.broadcastOnce()
	}
}

// Handler returns the HTTP mux wrapped in the CORS/XML middleware. Mount
// this on the net.Listener returned from Phase 8's discovery code.
func (c *Companion) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/resources", c.handleResources)
	mux.HandleFunc("/player/playback/playMedia", c.handlePlayMedia)
	mux.HandleFunc("/player/application/playMedia", c.handlePlayMedia)
	mux.HandleFunc("/player/playback/pause", c.handlePause)
	mux.HandleFunc("/player/playback/play", c.handlePlay)
	mux.HandleFunc("/player/playback/stop", c.handleStop)
	mux.HandleFunc("/player/playback/seekTo", c.handleSeekTo)
	mux.HandleFunc("/player/playback/refreshPlayQueue", c.handleRefreshPlayQueue)
	mux.HandleFunc("/player/playback/skipTo", c.handleSkipTo)
	mux.HandleFunc("/player/playback/skipNext", c.handleSkipNext)
	mux.HandleFunc("/player/playback/skipPrevious", c.handleSkipPrevious)
	mux.HandleFunc("/player/playback/setParameters", c.handleSetParameters)
	mux.HandleFunc("/player/playback/setStreams", c.handleSetStreams)
	mux.HandleFunc("/player/timeline/subscribe", c.handleTimelineSubscribe)
	mux.HandleFunc("/player/timeline/unsubscribe", c.handleTimelineUnsubscribe)
	mux.HandleFunc("/player/timeline/poll", c.handleTimelinePoll)
	mux.HandleFunc("/player/mirror/details", c.handleMirrorDetails)
	return c.withHeaders(c.withTargetValidation(c.withSubscriberTouch(mux)))
}

// withSubscriberTouch refreshes the controller's timeline subscription TTL on
// any request that carries an X-Plex-Client-Identifier. Plex clients keep
// talking to us after subscribe/poll/playback calls; touching here prevents
// active controllers from being pruned after 90s.
func (c *Companion) withSubscriberTouch(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.timeline != nil {
			if clientID := queryOrHeader(r, "X-Plex-Client-Identifier"); clientID != "" {
				c.timeline.TouchSubscriberCommand(clientID, atoiDefault(queryOrHeader(r, "commandID"), 0))
			}
		}
		h.ServeHTTP(w, r)
	})
}

// withTargetValidation ensures control-plane requests that explicitly target
// a different Plex client identifier do not mutate this bridge's session or
// subscriber state. Discovery (/resources) is intentionally exempt because
// controllers probe it before they know whether this is the device they want.
func (c *Companion) withTargetValidation(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/resources" {
			h.ServeHTTP(w, r)
			return
		}
		if targetID := queryOrHeader(r, "X-Plex-Target-Client-Identifier"); c.cfg.DeviceUUID != "" && targetID != "" && targetID != c.cfg.DeviceUUID {
			http.Error(w, "target client mismatch", http.StatusPreconditionFailed)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// withHeaders injects the CORS + default XML Content-Type headers that all
// Plex Companion responses share. Handlers that emit a non-XML body must
// override Content-Type before writing.
func (c *Companion) withHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "X-Plex-Token, X-Plex-Session-Identifier, X-Plex-Client-Identifier, X-Plex-Device-Name, X-Plex-Product, X-Plex-Version, X-Plex-Platform, X-Plex-Platform-Version, X-Plex-Provides, X-Plex-Protocol, X-Plex-Target-Client-Identifier, Content-Type, Accept")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-Plex-Client-Identifier, X-Plex-Device-Name, X-Plex-Product, X-Plex-Version, X-Plex-Platform, X-Plex-Platform-Version, X-Plex-Provides, X-Plex-Protocol")
		w.Header().Set("X-Plex-Client-Identifier", c.cfg.DeviceUUID)
		w.Header().Set("X-Plex-Device-Name", c.cfg.DeviceName)
		w.Header().Set("X-Plex-Product", companionProduct)
		w.Header().Set("X-Plex-Version", c.cfg.Version)
		w.Header().Set("X-Plex-Platform", companionPlatform)
		w.Header().Set("X-Plex-Platform-Version", c.cfg.Version)
		w.Header().Set("X-Plex-Provides", companionProvides)
		w.Header().Set("X-Plex-Protocol", companionProtocol)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
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
		Protocol             string `xml:"protocol,attr"`
		ProtocolVersion      string `xml:"protocolVersion,attr"`
		ProtocolCapabilities string `xml:"protocolCapabilities,attr"`
		DeviceClass          string `xml:"deviceClass,attr"`
		Device               string `xml:"device,attr"`
		Model                string `xml:"model,attr"`
		Product              string `xml:"product,attr"`
		Version              string `xml:"version,attr"`
		Platform             string `xml:"platform,attr"`
		PlatformVersion      string `xml:"platformVersion,attr"`
		// provides="player" tells Plex controllers this device is a valid
		// cast target for media playback. Without it, controllers show the
		// target in the picker but refuse to cast, with "This content is
		// not currently supported when connected to this remote player."
		Provides string `xml:"provides,attr"`
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
			Protocol:             "plex",
			ProtocolVersion:      "1",
			ProtocolCapabilities: "timeline,playback,playqueues",
			DeviceClass:          "stb",
			Device:               companionDevice,
			Model:                companionModel,
			Product:              companionProduct,
			Version:              c.cfg.Version,
			Platform:             companionPlatform,
			PlatformVersion:      c.cfg.Version,
			Provides:             companionProvides,
		},
	}
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(mc)
}

// PlayMediaRequest is the adapter-local view of a Plex Companion /playMedia
// query. Stored on the Companion (rememberPlaySession) so the timeline broker
// can attribute status updates back to the Plex media entity (core.
// SessionStatus.AdapterRef only carries the media key).
type PlayMediaRequest struct {
	PlexServerAddress string
	PlexServerPort    string
	PlexServerScheme  string
	PlexMachineID     string
	MediaKey          string
	ContainerKey      string
	OffsetMs          int
	SessionID         string
	ClientID          string
	PlexToken         string
	SubtitleStreamID  string
	AudioStreamID     string
	CommandID         string
}

// handlePlayMedia parses the Plex Companion playMedia query, builds a stream
// URL via BuildTranscodeURL, translates into core.SessionRequest, and
// delegates to core. On error we return 400; the controller retries.
func (c *Companion) handlePlayMedia(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(queryOrHeader(r, "offset"))
	p := PlayMediaRequest{
		PlexServerAddress: queryOrHeader(r, "address"),
		PlexServerPort:    queryOrHeader(r, "port"),
		PlexServerScheme:  queryOrHeader(r, "protocol"),
		PlexMachineID:     queryOrHeader(r, "machineIdentifier"),
		MediaKey:          queryOrHeader(r, "key"),
		ContainerKey:      queryOrHeader(r, "containerKey"),
		OffsetMs:          offset,
		SessionID:         queryOrHeader(r, "X-Plex-Session-Identifier"),
		ClientID:          queryOrHeader(r, "X-Plex-Client-Identifier"),
		// Plex controllers commonly send `token` on playMedia (per the repo
		// references), while some tooling and older tests still use
		// `X-Plex-Token`. Accept both from query or headers so playback works
		// across web/mobile/controller variants.
		PlexToken:        queryOrHeader(r, "token", "X-Plex-Token"),
		SubtitleStreamID: queryOrHeader(r, "subtitleStreamID"),
		AudioStreamID:    queryOrHeader(r, "audioStreamID"),
		CommandID:        queryOrHeader(r, "commandID"),
	}

	// Translate Plex Companion request → generic core.SessionRequest.
	serverURL := fmt.Sprintf("%s://%s:%s", p.PlexServerScheme, p.PlexServerAddress, p.PlexServerPort)
	streamClientID := c.cfg.DeviceUUID
	if streamClientID == "" {
		streamClientID = p.ClientID
	}
	streamURL := BuildTranscodeURL(TranscodeRequest{
		PlexServerURL: serverURL,
		MediaPath:     p.MediaKey,
		Token:         p.PlexToken,
		OffsetMs:      p.OffsetMs,
		OutputWidth:   720,
		OutputHeight:  480,
		SessionID:     p.SessionID,
		ClientID:      streamClientID,
		DeviceName:    c.cfg.DeviceName,
		ProfileName:   c.cfg.ProfileName,
		Product:       companionProduct,
		Platform:      companionPlatform,
		Version:       c.cfg.Version,
		Provides:      companionProvides,
	})
	req := core.SessionRequest{
		StreamURL:    streamURL,
		SeekOffsetMs: p.OffsetMs,
		AdapterRef:   p.MediaKey,
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
	}
	// Resolve subtitle: if the controller asked for a stream and PMS has
	// one, download to a temp file so libass can read it. On any error
	// (PMS miss, network hiccup, transient 5xx), fall back to no burn-in
	// rather than failing the whole cast — callers can retry by issuing
	// playMedia again.
	var subtitlePath string
	var subtitleIndex int
	if p.SubtitleStreamID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		subURL, err := SubtitleURLFor(ctx, serverURL, p.MediaKey, p.SubtitleStreamID, p.PlexToken)
		cancel()
		if err != nil {
			slog.Warn("subtitle lookup", "err", err, "streamID", p.SubtitleStreamID)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			subtitlePath, err = FetchSubtitleToFile(ctx, subURL, c.cfg.DataDir, p.SessionID)
			cancel()
			if err != nil {
				slog.Warn("subtitle download", "err", err)
				subtitlePath = ""
			} else {
				subtitleIndex = 0
			}
		}
	}
	req.SubtitlePath = subtitlePath
	req.SubtitleIndex = subtitleIndex

	if err := c.core.StartSession(req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.rememberPlaySession(p)
	c.notifyTimeline()
	writeOKResponse(w)
}

func (c *Companion) handlePause(w http.ResponseWriter, r *http.Request) {
	if err := c.core.Pause(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.notifyTimeline()
	writeOKResponse(w)
}

func (c *Companion) handlePlay(w http.ResponseWriter, r *http.Request) {
	if err := c.core.Play(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.notifyTimeline()
	writeOKResponse(w)
}

func (c *Companion) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := c.core.Stop(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.notifyTimeline()
	writeOKResponse(w)
}

func (c *Companion) handleSeekTo(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if err := c.core.SeekTo(offset); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.notifyTimeline()
	writeOKResponse(w)
}

// Stubs filled in later. We currently acknowledge the fuller Plex Companion
// surface without acting so compatible controllers don't fail on 404 while
// queue/navigation semantics are still out of scope for the bridge.
func (c *Companion) handleRefreshPlayQueue(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}
func (c *Companion) handleSkipTo(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}
func (c *Companion) handleSkipNext(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}
func (c *Companion) handleSkipPrevious(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}
func (c *Companion) handleSetParameters(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}
func (c *Companion) handleSetStreams(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}
func (c *Companion) handleMirrorDetails(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}

// handleTimelineSubscribe registers a controller for 1 Hz timeline pushes.
// The protocol/port/clientID come from query params; the host is derived from
// the request's RemoteAddr (minus port) since Plex controllers don't
// advertise their own IP in the subscribe request.
func (c *Companion) handleTimelineSubscribe(w http.ResponseWriter, r *http.Request) {
	if c.timeline == nil {
		http.Error(w, "timeline not wired", 503)
		return
	}
	q := r.URL.Query()
	// r.RemoteAddr is "ip:port" (or "[::1]:port" for IPv6); strip the port.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	c.timeline.Subscribe(
		q.Get("X-Plex-Client-Identifier"),
		host,
		q.Get("port"),
		q.Get("protocol"),
		atoiDefault(q.Get("commandID"), 0),
	)
	// Plex controllers typically expect an immediate first timeline after
	// subscribe rather than waiting for the next 1 Hz tick.
	c.timeline.broadcastOnce()
	writeOKResponse(w)
}

func (c *Companion) handleTimelineUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if c.timeline == nil {
		http.Error(w, "timeline not wired", 503)
		return
	}
	c.timeline.Unsubscribe(r.URL.Query().Get("X-Plex-Client-Identifier"))
	writeOKResponse(w)
}

// handleTimelinePoll is the long-poll fallback used by Plexamp. v1 returns
// the current timeline XML immediately (no wait=1 blocking semantics); the
// broadcast loop still pushes updates to subscribed controllers.
func (c *Companion) handleTimelinePoll(w http.ResponseWriter, r *http.Request) {
	if c.timeline == nil {
		http.Error(w, "timeline not wired", 503)
		return
	}
	st := core.SessionStatus{}
	if c.core != nil {
		st = c.core.Status()
	}
	w.WriteHeader(200)
	_, _ = w.Write([]byte(c.timeline.buildTimelineXMLWithCommandID(
		st,
		atoiDefault(queryOrHeader(r, "commandID"), 0),
	)))
}

// atoiDefault parses an int, returning d on failure. Used for Plex query
// params that may be missing or malformed.
func atoiDefault(s string, d int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}

// queryOrHeader returns the first non-empty value found under any of the
// given names, checking query parameters first and then HTTP headers. Plex
// Companion clients vary here: e.g. playMedia commonly sends `token` in the
// query string while other callers/tests use `X-Plex-Token`.
func queryOrHeader(r *http.Request, names ...string) string {
	q := r.URL.Query()
	for _, name := range names {
		if v := q.Get(name); v != "" {
			return v
		}
	}
	for _, name := range names {
		if v := r.Header.Get(name); v != "" {
			return v
		}
	}
	return ""
}

// rememberPlaySession stores the last playMedia request so the timeline
// broker (Task 7.5) can attribute status updates to the right Plex media
// entity. Thread-safe; getter returns a copy.
func (c *Companion) rememberPlaySession(p PlayMediaRequest) {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	c.lastPlay = p
}

// lastPlaySession returns a copy of the last remembered playMedia request.
func (c *Companion) lastPlaySession() PlayMediaRequest {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	return c.lastPlay
}

// writeOKResponse writes the canonical Plex Companion 200 OK XML reply.
func writeOKResponse(w http.ResponseWriter) {
	w.WriteHeader(200)
	_, _ = w.Write([]byte(`<?xml version="1.0"?><Response code="200" status="OK"/>`))
}
