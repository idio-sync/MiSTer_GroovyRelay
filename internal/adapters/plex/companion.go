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
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// MaxVideoBitrateKbps is the maxVideoBitrate value PMS sees when we
	// request a transcode. Snapshotted at finalization from plex.Config;
	// changes are ScopeRestartCast so the next play picks up new values.
	MaxVideoBitrateKbps int
}

// Companion is the Plex Companion HTTP adapter. One per process. Thread-safe.
type Companion struct {
	cfg      CompanionConfig
	core     SessionManager // adapter-agnostic core.Manager
	timeline *TimelineBroker

	// maxVideoBitrateKbps mirrors CompanionConfig.MaxVideoBitrateKbps as
	// an atomic so the UI's ApplyConfig (ScopeRestartCast) can update the
	// live companion without racing concurrent sessionRequestFor reads.
	// Other CompanionConfig fields (DeviceName, ProfileName, ...) remain
	// snapshot-at-finalize: changing them today still requires a bridge
	// restart despite their declared scopes — that's a pre-existing quirk
	// tracked separately.
	maxVideoBitrateKbps atomic.Int64

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
	// DropActiveCast tears down any in-flight session with the given
	// reason logged. Invoked by Plex ApplyConfig for restart-cast
	// field changes.
	DropActiveCast(reason string) error
}

// NewCompanion constructs a Companion. core may be nil for tests that only
// exercise handlers which don't delegate to core (e.g. /resources).
func NewCompanion(cfg CompanionConfig, core SessionManager) *Companion {
	c := &Companion{cfg: cfg, core: core}
	c.maxVideoBitrateKbps.Store(int64(cfg.MaxVideoBitrateKbps))
	return c
}

// SetMaxVideoBitrateKbps updates the live transcode bitrate ceiling. Called
// by Adapter.ApplyConfig when the UI saves a new value; the next playMedia
// will read the updated value through sessionRequestFor.
func (c *Companion) SetMaxVideoBitrateKbps(kbps int) {
	c.maxVideoBitrateKbps.Store(int64(kbps))
}

// SetTimeline wires the timeline broker after construction. Done this way
// because Phase 11's NewAdapter instantiates both and cross-links them.
func (c *Companion) SetTimeline(t *TimelineBroker) { c.timeline = t }

func (c *Companion) notifyTimeline() {
	if c.timeline != nil {
		go c.timeline.broadcastOnce()
	}
}

func (c *Companion) notifyStoppedTimeline(st core.SessionStatus) {
	if c.timeline == nil {
		return
	}
	st.State = core.StateIdle
	c.timeline.broadcastStatusOnce(st)
}

func (c *Companion) restorePausedIfNeeded(w http.ResponseWriter, wasPaused bool) bool {
	if !wasPaused {
		return true
	}
	if err := c.core.Pause(); err != nil {
		http.Error(w, err.Error(), 400)
		return false
	}
	c.notifyTimeline()
	return true
}

// transcodeRequestFor mirrors the TranscodeRequest sessionRequestFor builds, so
// the debug decision endpoint can reproduce the exact PMS request that drives
// playback. Keep these two callers in sync — diagnostic accuracy depends on it.
func (c *Companion) transcodeRequestFor(p PlayMediaRequest) TranscodeRequest {
	serverURL := fmt.Sprintf("%s://%s:%s", p.PlexServerScheme, p.PlexServerAddress, p.PlexServerPort)
	streamClientID := c.cfg.DeviceUUID
	if streamClientID == "" {
		streamClientID = p.ClientID
	}
	return TranscodeRequest{
		PlexServerURL:      serverURL,
		MediaPath:          p.MediaKey,
		Token:              p.PlexToken,
		OffsetMs:           p.OffsetMs,
		OutputWidth:        720,
		OutputHeight:       480,
		SessionID:          p.SessionID,
		ClientID:           streamClientID,
		DeviceName:         c.cfg.DeviceName,
		ProfileName:        c.cfg.ProfileName,
		Product:            companionProduct,
		Platform:           companionPlatform,
		Version:            c.cfg.Version,
		Provides:           companionProvides,
		MaxBitrate:         int(c.maxVideoBitrateKbps.Load()),
		AudioStreamID:      p.AudioStreamID,
		SubtitleStreamID:   p.SubtitleStreamID,
		TranscodeSessionID: p.TranscodeSessionID,
	}
}

func (c *Companion) sessionRequestFor(p PlayMediaRequest) core.SessionRequest {
	tr := c.transcodeRequestFor(p)
	serverURL := tr.PlexServerURL
	streamURL := BuildTranscodeURL(tr)
	req := core.SessionRequest{
		StreamURL:    streamURL,
		SeekOffsetMs: p.OffsetMs,
		AdapterRef:   p.MediaKey,
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
	}
	// Capture the prior PlayMediaRequest at request-construction time.
	// Reading lastPlay or Manager.Status() from inside OnStop is unsafe:
	// by the time the goroutine runs, a foreign adapter may have already
	// taken over and both will reflect the new session, not this one.
	captured := p
	req.OnStop = func(reason string) {
		// Order matters: notify subscribed Plex controllers FIRST, then
		// clear local state (conditionally), then make the best-effort PMS
		// hint last. This way the controller sees the stopped state
		// immediately even if PMS is slow/unreachable (StopTranscodeSession
		// has a 5s timeout and we don't want to gate the controller-cleanup
		// latency on it).
		if c.timeline != nil {
			c.timeline.broadcastStoppedFor(core.SessionStatus{State: core.StateIdle}, captured)
		}
		// Conditional clear: only wipe lastPlay if it still references THIS
		// transcode. A successor handler (handlePlayMedia for a different
		// movie, or handleSeekTo / handleSetStreams / restartFromPlayQueueItem
		// for the same movie) may have already called rememberPlaySession(NEW)
		// before this goroutine runs. We discriminate on TranscodeSessionID
		// rather than MediaKey because seek/setStreams keep the MediaKey
		// constant while minting a fresh transcode session — keying on
		// MediaKey would wipe the just-stored NEW session and the next
		// timeline poll would render no key/ratingKey, causing controllers
		// to lose track of the cast.
		c.clearPlaySessionIfMatches(captured.TranscodeSessionID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := StopTranscodeSession(ctx, serverURL, captured.TranscodeSessionID, captured.PlexToken); err != nil {
			slog.Debug("plex stop transcode", "reason", reason, "session", captured.TranscodeSessionID, "err", err)
		}
	}
	return req
}

type playQueueItem struct {
	Key             string `xml:"key,attr"`
	RatingKey       string `xml:"ratingKey,attr"`
	PlayQueueItemID string `xml:"playQueueItemID,attr"`
}

type playQueueContainer struct {
	Items []playQueueItem `xml:",any"`
}

func (c *Companion) fetchPlayQueue(ctx context.Context, p PlayMediaRequest) ([]playQueueItem, error) {
	if p.ContainerKey == "" {
		return nil, fmt.Errorf("no play queue container key")
	}
	serverURL := fmt.Sprintf("%s://%s:%s", p.PlexServerScheme, p.PlexServerAddress, p.PlexServerPort)
	reqURL, err := url.Parse(serverURL + p.ContainerKey)
	if err != nil {
		return nil, err
	}
	q := reqURL.Query()
	q.Set("includeBefore", "1")
	q.Set("includeAfter", "1")
	if p.PlexToken != "" {
		q.Set("X-Plex-Token", p.PlexToken)
	}
	reqURL.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, err
	}
	if p.PlexToken != "" {
		req.Header.Set("X-Plex-Token", p.PlexToken)
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch play queue: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch play queue: %s", resp.Status)
	}
	var pq playQueueContainer
	if err := xml.NewDecoder(resp.Body).Decode(&pq); err != nil {
		return nil, fmt.Errorf("parse play queue: %w", err)
	}
	return pq.Items, nil
}

func nextPlayQueueItem(items []playQueueItem, currentID, currentKey string, delta int) (playQueueItem, bool) {
	if delta == 0 {
		return playQueueItem{}, false
	}
	idx := -1
	for i, item := range items {
		switch {
		case currentID != "" && item.PlayQueueItemID == currentID:
			idx = i
		case currentID == "" && currentKey != "" && item.Key == currentKey:
			idx = i
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		return playQueueItem{}, false
	}
	next := idx + delta
	if next < 0 || next >= len(items) {
		return playQueueItem{}, false
	}
	return items[next], true
}

func playQueueItemByIDOrKey(items []playQueueItem, id, key string) (playQueueItem, bool) {
	if id == "" && key == "" {
		return playQueueItem{}, false
	}
	for _, item := range items {
		if id != "" && item.PlayQueueItemID == id {
			return item, true
		}
		if key != "" && item.Key == key {
			return item, true
		}
		if key != "" && item.RatingKey != "" && "/library/metadata/"+item.RatingKey == key {
			return item, true
		}
	}
	return playQueueItem{}, false
}

func (c *Companion) restartFromPlayQueueItem(w http.ResponseWriter, r *http.Request, selectItem func([]playQueueItem, PlayMediaRequest) (playQueueItem, bool)) bool {
	prevStatus := core.SessionStatus{}
	if c.core != nil {
		prevStatus = c.core.Status()
	}
	p := c.lastPlaySession()
	if p.MediaKey == "" {
		http.Error(w, "no plex session", 400)
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	items, err := c.fetchPlayQueue(ctx, p)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return false
	}
	item, ok := selectItem(items, p)
	if !ok {
		http.Error(w, "play queue item not found", 400)
		return false
	}
	key := item.Key
	if key == "" && item.RatingKey != "" {
		key = "/library/metadata/" + item.RatingKey
	}
	if key == "" {
		http.Error(w, "play queue item has no media key", 400)
		return false
	}
	p.MediaKey = key
	p.PlayQueueItemID = item.PlayQueueItemID
	p.OffsetMs = 0
	p.CommandID = queryOrHeader(r, "commandID")
	p.TranscodeSessionID = NewTranscodeSessionID()
	req := c.sessionRequestFor(p)
	if prevStatus.State != core.StateIdle {
		c.notifyStoppedTimeline(prevStatus)
	}
	if err := c.core.StartSession(req); err != nil {
		http.Error(w, err.Error(), 400)
		return false
	}
	c.rememberPlaySession(p)
	if !c.restorePausedIfNeeded(w, prevStatus.State == core.StatePaused) {
		return false
	}
	c.notifyTimeline()
	return true
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
	mux.HandleFunc("/debug/plex/session", c.handleDebugSession)
	mux.HandleFunc("/debug/plex/decision", c.handleDebugDecision)
	return c.withRequestLog(c.withHeaders(c.withTargetValidation(c.withSubscriberTouch(mux))))
}

// withRequestLog logs every /player/* Companion request with the params that
// matter for diagnosing unexpected session teardowns. Plex controllers
// occasionally re-issue setStreams/playMedia with the current selection on
// purely UI-side events (e.g. opening the in-player gear panel in Plex Web)
// — surfacing those calls makes "why did the cast end?" answerable from logs
// alone instead of having to instrument and re-deploy.
func (c *Companion) withRequestLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/player/") || r.URL.Path == "/resources" {
			slog.Info("plex companion request",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"client_id", queryOrHeader(r, "X-Plex-Client-Identifier"),
				"target_id", queryOrHeader(r, "X-Plex-Target-Client-Identifier"),
				"product", queryOrHeader(r, "X-Plex-Product"),
				"device_name", queryOrHeader(r, "X-Plex-Device-Name"),
				"command_id", queryOrHeader(r, "commandID"),
				"wait", queryOrHeader(r, "wait"),
				"key", queryOrHeader(r, "key"),
				"audio_stream_id", queryOrHeader(r, "audioStreamID"),
				"subtitle_stream_id", queryOrHeader(r, "subtitleStreamID"),
				"offset", queryOrHeader(r, "offset"),
			)
		}
		h.ServeHTTP(w, r)
	})
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
	PlexServerAddress  string
	PlexServerPort     string
	PlexServerScheme   string
	PlexMachineID      string
	MediaKey           string
	ContainerKey       string
	OffsetMs           int
	SessionID          string
	ClientID           string
	PlexToken          string
	SubtitleStreamID   string
	AudioStreamID      string
	CommandID          string
	PlayQueueItemID    string
	TranscodeSessionID string
}

// handlePlayMedia parses the Plex Companion playMedia query, builds a stream
// URL via BuildTranscodeURL, translates into core.SessionRequest, and
// delegates to core. On error we return 400; the controller retries.
func (c *Companion) handlePlayMedia(w http.ResponseWriter, r *http.Request) {
	prevStatus := core.SessionStatus{}
	if c.core != nil {
		prevStatus = c.core.Status()
	}
	prevPlay := c.lastPlaySession()
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
		PlexToken:          queryOrHeader(r, "token", "X-Plex-Token"),
		SubtitleStreamID:   queryOrHeader(r, "subtitleStreamID"),
		AudioStreamID:      queryOrHeader(r, "audioStreamID"),
		CommandID:          queryOrHeader(r, "commandID"),
		PlayQueueItemID:    queryOrHeader(r, "playQueueItemID"),
		TranscodeSessionID: NewTranscodeSessionID(),
	}
	// Some Plex controllers (notably mobile apps) omit
	// X-Plex-Session-Identifier on playMedia. PMS uses this id to tie
	// segment fetches and /:/timeline heartbeats to the same player
	// session — without it, PMS may evict our transcoder mid-stream
	// (HLS segment 404s) and Tautulli's /status/sessions view falls
	// back to source-media labels rather than transcode-output state.
	// Mint a UUID when missing so identity is always present.
	if p.SessionID == "" {
		p.SessionID = NewTranscodeSessionID()
	}

	serverURL := fmt.Sprintf("%s://%s:%s", p.PlexServerScheme, p.PlexServerAddress, p.PlexServerPort)
	req := c.sessionRequestFor(p)
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

	if prevPlay.MediaKey != "" && prevStatus.State != core.StateIdle {
		c.notifyStoppedTimeline(prevStatus)
	}
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
	st := core.SessionStatus{}
	if c.core != nil {
		st = c.core.Status()
	}
	if err := c.core.Stop(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.notifyStoppedTimeline(st)
	c.clearPlaySession()
	writeOKResponse(w)
}

func (c *Companion) handleSeekTo(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	p := c.lastPlaySession()
	if p.MediaKey == "" {
		http.Error(w, "no plex session", 400)
		return
	}
	st := core.SessionStatus{}
	if c.core != nil {
		st = c.core.Status()
	}
	p.OffsetMs = offset
	p.CommandID = queryOrHeader(r, "commandID")
	p.TranscodeSessionID = NewTranscodeSessionID()
	req := c.sessionRequestFor(p)
	if err := c.core.StartSession(req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.rememberPlaySession(p)
	if !c.restorePausedIfNeeded(w, st.State == core.StatePaused) {
		return
	}
	c.notifyTimeline()
	writeOKResponse(w)
}

// Stubs filled in later. We currently acknowledge the fuller Plex Companion
// surface without acting so compatible controllers don't fail on 404 while
// queue/navigation semantics are still out of scope for the bridge.
func (c *Companion) handleRefreshPlayQueue(w http.ResponseWriter, r *http.Request) {
	p := c.lastPlaySession()
	if p.MediaKey == "" {
		http.Error(w, "no plex session", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := c.fetchPlayQueue(ctx, p); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeOKResponse(w)
}
func (c *Companion) handleSkipTo(w http.ResponseWriter, r *http.Request) {
	playQueueItemID := queryOrHeader(r, "playQueueItemID")
	key := queryOrHeader(r, "key")
	if !c.restartFromPlayQueueItem(w, r, func(items []playQueueItem, p PlayMediaRequest) (playQueueItem, bool) {
		return playQueueItemByIDOrKey(items, playQueueItemID, key)
	}) {
		return
	}
	writeOKResponse(w)
}
func (c *Companion) handleSkipNext(w http.ResponseWriter, r *http.Request) {
	if !c.restartFromPlayQueueItem(w, r, func(items []playQueueItem, p PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, p.PlayQueueItemID, p.MediaKey, 1)
	}) {
		return
	}
	writeOKResponse(w)
}
func (c *Companion) handleSkipPrevious(w http.ResponseWriter, r *http.Request) {
	if !c.restartFromPlayQueueItem(w, r, func(items []playQueueItem, p PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, p.PlayQueueItemID, p.MediaKey, -1)
	}) {
		return
	}
	writeOKResponse(w)
}
func (c *Companion) handleSetParameters(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}
func (c *Companion) handleSetStreams(w http.ResponseWriter, r *http.Request) {
	p := c.lastPlaySession()
	if p.MediaKey == "" {
		http.Error(w, "no plex session", 400)
		return
	}
	audioStreamID := queryOrHeader(r, "audioStreamID")
	subtitleStreamID := queryOrHeader(r, "subtitleStreamID")
	if audioStreamID == "" && subtitleStreamID == "" {
		writeOKResponse(w)
		return
	}
	// Plex Web's in-player gear panel re-issues setStreams with the CURRENT
	// audio/subtitle IDs whenever it opens, even if the user hasn't picked a
	// new track. Treating that as a real change rebuilds the ffmpeg pipeline
	// and invalidates the PMS transcode session — the controller, still
	// polling the old transcode ID, surfaces "There was an unexpected error
	// during playback." Only rebuild when at least one stream actually
	// differs from what the live cast is using.
	audioUnchanged := audioStreamID == "" || audioStreamID == p.AudioStreamID
	subtitleUnchanged := subtitleStreamID == "" || subtitleStreamID == p.SubtitleStreamID
	if audioUnchanged && subtitleUnchanged {
		writeOKResponse(w)
		return
	}
	st := core.SessionStatus{}
	if c.core != nil {
		st = c.core.Status()
	}
	serverURL := fmt.Sprintf("%s://%s:%s", p.PlexServerScheme, p.PlexServerAddress, p.PlexServerPort)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := SetStreamSelection(ctx, serverURL, p.MediaKey, p.PlexToken, audioStreamID, subtitleStreamID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if audioStreamID != "" {
		p.AudioStreamID = audioStreamID
	}
	if subtitleStreamID != "" {
		p.SubtitleStreamID = subtitleStreamID
	}
	p.OffsetMs = int(st.Position.Milliseconds())
	p.CommandID = queryOrHeader(r, "commandID")
	p.TranscodeSessionID = NewTranscodeSessionID()
	req := c.sessionRequestFor(p)
	if err := c.core.StartSession(req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.rememberPlaySession(p)
	if !c.restorePausedIfNeeded(w, st.State == core.StatePaused) {
		return
	}
	c.notifyTimeline()
	writeOKResponse(w)
}
func (c *Companion) handleMirrorDetails(w http.ResponseWriter, r *http.Request) {
	writeOKResponse(w)
}

type debugPMSCheck struct {
	URL        string `json:"url"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode,omitempty"`
	Count      int    `json:"count"`
	Matched    bool   `json:"matched"`
	Error      string `json:"error,omitempty"`
}

type debugSessionReport struct {
	DeviceUUID string `json:"deviceUUID"`
	DeviceName string `json:"deviceName"`
	Local      struct {
		State              core.State `json:"state"`
		PositionMs         int64      `json:"positionMs"`
		DurationMs         int64      `json:"durationMs"`
		MediaKey           string     `json:"mediaKey,omitempty"`
		RatingKey          string     `json:"ratingKey,omitempty"`
		ContainerKey       string     `json:"containerKey,omitempty"`
		PlayQueueItemID    string     `json:"playQueueItemID,omitempty"`
		AudioStreamID      string     `json:"audioStreamID,omitempty"`
		SubtitleStreamID   string     `json:"subtitleStreamID,omitempty"`
		TranscodeSessionID string     `json:"transcodeSessionId,omitempty"`
		PlexSessionID      string     `json:"plexSessionId,omitempty"`
		PlexServer         string     `json:"plexServer,omitempty"`
		HasToken           bool       `json:"hasToken"`
	} `json:"local"`
	PMS struct {
		StatusSessions    debugPMSCheck `json:"statusSessions"`
		TranscodeSessions debugPMSCheck `json:"transcodeSessions"`
	} `json:"pms"`
}

func (c *Companion) handleDebugSession(w http.ResponseWriter, r *http.Request) {
	play := c.lastPlaySession()
	st := core.SessionStatus{}
	if c.core != nil {
		st = c.core.Status()
	}
	report := debugSessionReport{
		DeviceUUID: c.cfg.DeviceUUID,
		DeviceName: c.cfg.DeviceName,
	}
	report.Local.State = st.State
	report.Local.PositionMs = st.Position.Milliseconds()
	report.Local.DurationMs = st.Duration.Milliseconds()
	report.Local.MediaKey = play.MediaKey
	report.Local.RatingKey = ratingKeyFromMediaKey(play.MediaKey)
	report.Local.ContainerKey = play.ContainerKey
	report.Local.PlayQueueItemID = play.PlayQueueItemID
	report.Local.AudioStreamID = play.AudioStreamID
	report.Local.SubtitleStreamID = play.SubtitleStreamID
	report.Local.TranscodeSessionID = play.TranscodeSessionID
	report.Local.PlexSessionID = play.SessionID
	report.Local.HasToken = play.PlexToken != ""
	if play.PlexServerAddress != "" && play.PlexServerPort != "" && play.PlexServerScheme != "" {
		report.Local.PlexServer = fmt.Sprintf("%s://%s:%s", play.PlexServerScheme, play.PlexServerAddress, play.PlexServerPort)
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		report.PMS.StatusSessions = c.debugPMSStatusSessions(ctx, report.Local.PlexServer, play)
		report.PMS.TranscodeSessions = c.debugPMSTranscodeSessions(ctx, report.Local.PlexServer, play)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(report)
}

type debugDecisionReport struct {
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode,omitempty"`
	Body       string `json:"body,omitempty"`
	Error      string `json:"error,omitempty"`
}

// handleDebugDecision asks PMS what it would actually do with our transcode
// request by hitting /video/:/transcode/universal/decision with the same
// params BuildTranscodeURL produces. The raw XML body PMS returns includes
// directPlayDecisionCode/Text and transcodeDecisionCode/Text on the top-level
// MediaContainer, plus per-stream Decision attributes — definitive answer to
// "is PMS direct-playing despite directPlay=0&directStream=0?".
func (c *Companion) handleDebugDecision(w http.ResponseWriter, r *http.Request) {
	play := c.lastPlaySession()
	report := debugDecisionReport{}
	if play.MediaKey == "" || play.PlexServerAddress == "" || play.PlexServerPort == "" || play.PlexServerScheme == "" {
		report.Error = "no plex session"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(report)
		return
	}
	tr := c.transcodeRequestFor(play)
	decisionURL := BuildDecisionURL(tr)
	report.URL = decisionURL
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, decisionURL, nil)
	if err != nil {
		report.Error = err.Error()
	} else {
		if play.PlexToken != "" {
			req.Header.Set("X-Plex-Token", play.PlexToken)
		}
		resp, err := plexHTTPClient.Do(req)
		if err != nil {
			report.Error = err.Error()
		} else {
			defer resp.Body.Close()
			report.StatusCode = resp.StatusCode
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				report.Error = err.Error()
			} else {
				report.Body = string(body)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(report)
}

func (c *Companion) debugPMSStatusSessions(ctx context.Context, serverURL string, play PlayMediaRequest) debugPMSCheck {
	return debugPMSXML(ctx, serverURL, "/status/sessions", play.PlexToken, func(se xml.StartElement) (bool, bool) {
		if se.Name.Local == "Video" {
			for _, attr := range se.Attr {
				if (attr.Name.Local == "key" && attr.Value == play.MediaKey) ||
					(attr.Name.Local == "ratingKey" && attr.Value == ratingKeyFromMediaKey(play.MediaKey)) {
					return true, false
				}
			}
			return false, false
		}
		if se.Name.Local == "Player" {
			for _, attr := range se.Attr {
				if attr.Name.Local == "machineIdentifier" && attr.Value == c.cfg.DeviceUUID {
					return false, true
				}
			}
		}
		return false, false
	})
}

func (c *Companion) debugPMSTranscodeSessions(ctx context.Context, serverURL string, play PlayMediaRequest) debugPMSCheck {
	return debugPMSXML(ctx, serverURL, "/transcode/sessions", play.PlexToken, func(se xml.StartElement) (bool, bool) {
		if se.Name.Local != "TranscodeSession" {
			return false, false
		}
		if play.TranscodeSessionID == "" {
			return true, false
		}
		for _, attr := range se.Attr {
			if attr.Value == play.TranscodeSessionID || strings.Contains(attr.Value, play.TranscodeSessionID) {
				return true, true
			}
		}
		return true, false
	})
}

func debugPMSXML(ctx context.Context, serverURL, path, token string, inspect func(xml.StartElement) (count bool, matched bool)) debugPMSCheck {
	reqURL, err := url.Parse(strings.TrimRight(serverURL, "/") + path)
	check := debugPMSCheck{URL: strings.TrimRight(serverURL, "/") + path}
	if err != nil {
		check.Error = err.Error()
		return check
	}
	if token != "" {
		q := reqURL.Query()
		q.Set("X-Plex-Token", token)
		reqURL.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	defer resp.Body.Close()
	check.StatusCode = resp.StatusCode
	if resp.StatusCode >= 400 {
		check.Error = resp.Status
		return check
	}
	dec := xml.NewDecoder(resp.Body)
	for {
		tok, err := dec.Token()
		if err != nil {
			if err != io.EOF {
				check.Error = err.Error()
			}
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		count, matched := inspect(se)
		if count {
			check.Count++
		}
		if matched {
			check.Matched = true
		}
	}
	check.OK = check.Error == ""
	return check
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

// pollLongWaitTimeout caps how long handleTimelinePoll will hold a wait=1
// request open. 30 s matches the cadence Plex controllers expect: the
// controller assumes the connection will return within ~30 s either with a
// real state change or with the unchanged current state, and reissues the
// poll immediately afterward. Exposed as a package var so tests can shorten
// it without using real time.
var pollLongWaitTimeout = 30 * time.Second

// pollLongWaitTick is how often the wait loop re-reads core/lastPlay state
// to detect changes. Adapter-internal bookkeeping is in-memory so a coarse
// tick is fine; the goal is to break out of the wait promptly when a play
// command changes state, not to provide millisecond-accurate timeline.
var pollLongWaitTick = 250 * time.Millisecond

// handleTimelinePoll services /player/timeline/poll. With no wait param it
// returns the current timeline immediately (legacy behavior). With wait=1
// it honors Plex's long-poll semantics: the connection is held open until
// either timeline state changes or pollLongWaitTimeout elapses. Plex for
// Windows in particular sends wait=1 and treats a steady stream of
// instantly-returning polls — every reply identical — as an unresponsive
// player, deselecting the cast target after a handful of seconds.
func (c *Companion) handleTimelinePoll(w http.ResponseWriter, r *http.Request) {
	if c.timeline == nil {
		http.Error(w, "timeline not wired", 503)
		return
	}
	st := c.coreStatus()
	play := c.lastPlaySession()
	if queryOrHeader(r, "wait") == "1" {
		st, play = c.waitForTimelineChange(r.Context(), st, play)
		if r.Context().Err() != nil {
			// Client disconnected mid-wait. Skip the response write — it
			// would be discarded by the http stack anyway, but bailing
			// avoids one log line per abandoned poll on noisy controllers.
			return
		}
	}
	// Diagnostic: pair with the "plex companion request" log entry on the
	// same request to correlate the reply we sent with what the controller
	// asked for. last_play_* fields surface any cross-session metadata
	// leaking into the reply.
	slog.Info("plex timeline poll reply",
		"requesting_client_id", queryOrHeader(r, "X-Plex-Client-Identifier"),
		"remote_addr", r.RemoteAddr,
		"wait", queryOrHeader(r, "wait"),
		"core_state", string(st.State),
		"last_play_client_id", play.ClientID,
		"last_play_media_key", play.MediaKey,
		"last_play_pms", play.PlexMachineID,
	)
	w.WriteHeader(200)
	_, _ = w.Write([]byte(c.timeline.buildTimelineXMLWithCommandID(
		st,
		play,
		atoiDefault(queryOrHeader(r, "commandID"), 0),
	)))
}

// coreStatus is a small wrapper that lets tests construct a Companion with
// a nil core and still hit the poll handler. Production paths always have
// core wired up.
func (c *Companion) coreStatus() core.SessionStatus {
	if c.core == nil {
		return core.SessionStatus{}
	}
	return c.core.Status()
}

// waitForTimelineChange polls in-memory state every pollLongWaitTick until
// either a meaningful change is observed (see timelineChanged), the request
// context is cancelled, or pollLongWaitTimeout elapses. Returns the
// most-recent observed state — equal to the input on timeout/cancel, the
// new state on change.
func (c *Companion) waitForTimelineChange(ctx context.Context, st core.SessionStatus, play PlayMediaRequest) (core.SessionStatus, PlayMediaRequest) {
	timer := time.NewTimer(pollLongWaitTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(pollLongWaitTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return st, play
		case <-timer.C:
			return st, play
		case <-ticker.C:
			newSt := c.coreStatus()
			newPlay := c.lastPlaySession()
			if timelineChanged(st, play, newSt, newPlay) {
				return newSt, newPlay
			}
		}
	}
}

// pollPlayingPositionGranularity is how much the playing position must
// advance before the long-poll wakes up to deliver an updated `time`
// attribute. Picking ~1 s matches the 1 Hz broadcast cadence already used
// for subscribed controllers and keeps progress bars visibly moving on
// poll-only clients (notably Plex for Windows). A coarser value frees more
// wait time but stalls the progress bar; finer collapses back into a tight
// loop. Exposed as a var so tests can poke a smaller value.
var pollPlayingPositionGranularity = time.Second

// timelineChanged returns true when newSt/newPlay represent a transition
// the controller cares about. Three buckets:
//
//   - Hard state/identity changes: state, MediaKey, SessionID, stream
//     selections, PlayQueue item. These are always reported.
//   - Playback progress: while actively playing, every
//     pollPlayingPositionGranularity of forward drift is reported so the
//     controller's progress bar gets fresh `time` values. Without this,
//     wait=1 holds for the full safety timeout (~30 s) and the bar freezes
//     between releases.
//   - Steady idle/paused state: NOT a change; long-poll can hold the full
//     timeout because there's no position to update.
func timelineChanged(oldSt core.SessionStatus, oldPlay PlayMediaRequest, newSt core.SessionStatus, newPlay PlayMediaRequest) bool {
	if oldSt.State != newSt.State {
		return true
	}
	if oldPlay.MediaKey != newPlay.MediaKey {
		return true
	}
	if oldPlay.SessionID != newPlay.SessionID {
		return true
	}
	if oldPlay.AudioStreamID != newPlay.AudioStreamID {
		return true
	}
	if oldPlay.SubtitleStreamID != newPlay.SubtitleStreamID {
		return true
	}
	if oldPlay.PlayQueueItemID != newPlay.PlayQueueItemID {
		return true
	}
	if newSt.State == core.StatePlaying {
		delta := newSt.Position - oldSt.Position
		if delta < 0 {
			delta = -delta
		}
		if delta >= pollPlayingPositionGranularity {
			return true
		}
	}
	return false
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

func (c *Companion) clearPlaySession() {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	c.lastPlay = PlayMediaRequest{}
}

// clearPlaySessionIfMatches resets c.lastPlay to its zero value ONLY
// if the current TranscodeSessionID matches the supplied one. Used by
// OnStop closures to safely clear stale Plex session state without
// racing against a concurrent rememberPlaySession from a successor
// handler. If lastPlay has already been overwritten with a fresh
// session (preempt or seek/setStreams on the same movie), this is a
// no-op. TranscodeSessionID is the right discriminator because it is
// minted per call and never collides — MediaKey alone would wipe a
// just-stored seek of the same movie.
func (c *Companion) clearPlaySessionIfMatches(transcodeSessionID string) {
	if transcodeSessionID == "" {
		// Defensive: an empty captured id would otherwise match an
		// already-cleared lastPlay (idempotent no-op) but more
		// dangerously could match a NEW session built without a
		// TranscodeSessionID. Skip the comparison entirely.
		return
	}
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	if c.lastPlay.TranscodeSessionID == transcodeSessionID {
		c.lastPlay = PlayMediaRequest{}
	}
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
