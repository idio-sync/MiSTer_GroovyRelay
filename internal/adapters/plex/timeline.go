package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// defaultSubscriberTTL is the Plex-mpv-shim canonical idle window. Subscribers
// that haven't been touched within this window are pruned on each broadcast
// tick. Exposed through TimelineBroker.TTL for testability.
const defaultSubscriberTTL = 90 * time.Second

// TimelineConfig carries the X-Plex-* identity headers the broker sends on
// every timeline push. Kept separate from CompanionConfig so the broker can
// be unit-tested without constructing a full Companion.
type TimelineConfig struct {
	DeviceUUID string
	DeviceName string
}

// subscriber is the broker's per-controller state. One entry per registered
// Plex controller UUID; pruned after TTL of inactivity.
type subscriber struct {
	clientID  string
	host      string // already stripped of :port by handleTimelineSubscribe
	port      string
	commandID int
	protocol  string
	lastSeen  time.Time
}

// TimelineBroker pushes 1 Hz timeline XML to registered Plex controllers and
// serves the /player/timeline/poll long-poll fallback. Thread-safe.
type TimelineBroker struct {
	cfg         TimelineConfig
	status      func() core.SessionStatus
	playContext func() PlayMediaRequest
	mu          sync.Mutex
	subscribers map[string]*subscriber
	lastPMSURL  string
	lastPMSBody string
	lastPMSToken string
	stop        chan struct{}
	stopOnce    sync.Once

	// TTL is the stale-subscriber prune threshold. Defaults to 90s per the
	// Plex-mpv-shim convention; tests override this to avoid real waits.
	TTL time.Duration

	// now is swappable for tests that want to simulate the subscriber clock.
	// Production callers leave this nil and get time.Now.
	now func() time.Time

	// httpClient is swappable for tests that want to intercept pushes without
	// standing up a real httptest.Server.
	httpClient *http.Client
}

// NewTimelineBroker constructs a TimelineBroker. statusFn is called once per
// broadcast tick to fetch the live core.SessionStatus.
func NewTimelineBroker(cfg TimelineConfig, statusFn func() core.SessionStatus) *TimelineBroker {
	return &TimelineBroker{
		cfg:         cfg,
		status:      statusFn,
		subscribers: make(map[string]*subscriber),
		stop:        make(chan struct{}),
		TTL:         defaultSubscriberTTL,
		httpClient:  &http.Client{Timeout: 1 * time.Second},
	}
}

// SetPlayContextProvider wires a callback that returns the last remembered
// playMedia request. The broker uses it to enrich timeline XML with Plex
// media identity and source-PMS fields.
func (t *TimelineBroker) SetPlayContextProvider(fn func() PlayMediaRequest) {
	t.playContext = fn
}

// RunBroadcastLoop ticks every second and pushes the current timeline XML to
// every live subscriber. Returns when Stop is called. Run in a goroutine.
func (t *TimelineBroker) RunBroadcastLoop() {
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			t.broadcastOnce()
		}
	}
}

// Stop signals the broadcast loop to exit. Idempotent; subsequent calls are
// no-ops (sync.Once-guarded so a second Stop doesn't panic on a double close).
func (t *TimelineBroker) Stop() {
	t.stopOnce.Do(func() { close(t.stop) })
}

// timeNow returns the broker's current time. Tests can override t.now to
// advance the subscriber TTL clock deterministically.
func (t *TimelineBroker) timeNow() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// postTimeline sends one timeline payload to either a subscribed controller
// or PMS. targetClientID is only set for controller pushes. token, when
// present, is appended both as a query param and header for PMS auth.
func (t *TimelineBroker) postTimeline(client *http.Client, urlStr, xmlBody, targetClientID, token string) error {
	reqURL, err := url.Parse(urlStr)
	if err != nil {
		return err
	}
	if token != "" {
		q := reqURL.Query()
		q.Set("X-Plex-Token", token)
		reqURL.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, reqURL.String(),
		strings.NewReader(xmlBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Protocol", "1.0")
	req.Header.Set("X-Plex-Client-Identifier", t.cfg.DeviceUUID)
	req.Header.Set("X-Plex-Device-Name", t.cfg.DeviceName)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	if targetClientID != "" {
		req.Header.Set("X-Plex-Target-Client-Identifier", targetClientID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("timeline post: %s", resp.Status)
	}
	return nil
}

func (t *TimelineBroker) postPMSTimeline(client *http.Client, urlStr string, st core.SessionStatus, play PlayMediaRequest) error {
	reqURL, err := url.Parse(urlStr)
	if err != nil {
		return err
	}
	q := reqURL.Query()
	if play.PlexToken != "" {
		q.Set("X-Plex-Token", play.PlexToken)
	}
	if play.MediaKey != "" {
		q.Set("key", play.MediaKey)
		q.Set("ratingKey", ratingKeyFromMediaKey(play.MediaKey))
	}
	q.Set("state", plexStateFromCore(st.State))
	q.Set("time", strconv.FormatInt(st.Position.Milliseconds(), 10))
	if st.Duration > 0 {
		q.Set("duration", strconv.FormatInt(st.Duration.Milliseconds(), 10))
	}
	if play.PlayQueueItemID != "" {
		q.Set("playQueueItemID", play.PlayQueueItemID)
	}
	if play.ContainerKey != "" {
		q.Set("containerKey", play.ContainerKey)
	}
	// transcodeSession ties this heartbeat to our active PMS transcoder.
	// Without it PMS reaps the transcoder on idle, causing ffmpeg to 404
	// on segments mid-stream and Tautulli's session view to fall back to
	// source-media labels rather than transcode-output state.
	if play.TranscodeSessionID != "" {
		q.Set("transcodeSession", play.TranscodeSessionID)
	}
	q.Set("identifier", "com.plexapp.plugins.library")
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, reqURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Protocol", "1.0")
	req.Header.Set("X-Plex-Client-Identifier", t.cfg.DeviceUUID)
	req.Header.Set("X-Plex-Device-Name", t.cfg.DeviceName)
	req.Header.Set("X-Plex-Product", companionProduct)
	req.Header.Set("X-Plex-Platform", companionPlatform)
	req.Header.Set("X-Plex-Provides", companionProvides)
	if play.PlexToken != "" {
		req.Header.Set("X-Plex-Token", play.PlexToken)
	}
	if play.SessionID != "" {
		req.Header.Set("X-Plex-Session-Identifier", play.SessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("pms timeline post: %s", resp.Status)
	}
	return nil
}

// broadcastOnce prunes stale subscribers, then pushes the current timeline
// XML to each live subscriber and the source PMS when known. Isolated for test
// access.
func (t *TimelineBroker) broadcastOnce() {
	t.broadcastStatusOnce(t.status())
}

func (t *TimelineBroker) broadcastStatusOnce(st core.SessionStatus) {
	t.mu.Lock()
	now := t.timeNow()
	ttl := t.TTL
	if ttl == 0 {
		ttl = defaultSubscriberTTL
	}
	subs := make([]subscriber, 0, len(t.subscribers))
	for id, s := range t.subscribers {
		if now.Sub(s.lastSeen) > ttl {
			delete(t.subscribers, id)
			continue
		}
		subs = append(subs, *s)
	}
	client := t.httpClient
	t.mu.Unlock()

	play := PlayMediaRequest{}
	if t.playContext != nil {
		play = t.playContext()
	}

	for _, s := range subs {
		xmlBody := t.buildTimelineXMLWithCommandID(st, s.commandID)
		protocol := s.protocol
		if protocol == "" {
			protocol = "http"
		}
		url := fmt.Sprintf("%s://%s:%s/:/timeline", protocol, s.host, s.port)
		if err := t.postTimeline(client, url, xmlBody, s.clientID, ""); err != nil {
			slog.Debug("timeline push failed", "sub", s.clientID, "err", err)
			continue
		}
	}

	if play.PlexServerAddress == "" || play.PlexServerPort == "" || play.PlexServerScheme == "" {
		return
	}
	pmsURL := fmt.Sprintf("%s://%s:%s/:/timeline", play.PlexServerScheme, play.PlexServerAddress, play.PlexServerPort)
	pmsBody := t.buildTimelineXML(st)
	t.mu.Lock()
	duplicatePMS := t.lastPMSURL == pmsURL && t.lastPMSBody == pmsBody && t.lastPMSToken == play.PlexToken
	t.mu.Unlock()
	if duplicatePMS {
		return
	}
	if err := t.postPMSTimeline(client, pmsURL, st, play); err != nil {
		slog.Debug("timeline push failed", "target", "pms", "err", err)
		return
	}
	t.mu.Lock()
	t.lastPMSURL = pmsURL
	t.lastPMSBody = pmsBody
	t.lastPMSToken = play.PlexToken
	t.mu.Unlock()
}

// buildTimelineXML renders the three-<Timeline> MediaContainer Plex expects:
// music/photo are always stopped (we only play video); video carries the live
// state/position. State maps core.State → Plex strings.
func (t *TimelineBroker) buildTimelineXML(s core.SessionStatus) string {
	return t.buildTimelineXMLWithCommandID(s, 0)
}

func (t *TimelineBroker) buildTimelineXMLWithCommandID(s core.SessionStatus, commandID int) string {
	type Timeline struct {
		XMLName           xml.Name `xml:"Timeline"`
		Type              string   `xml:"type,attr"`
		State             string   `xml:"state,attr"`
		Time              int64    `xml:"time,attr"`
		Duration          int64    `xml:"duration,attr"`
		RatingKey         string   `xml:"ratingKey,attr,omitempty"`
		Key               string   `xml:"key,attr,omitempty"`
		ContainerKey      string   `xml:"containerKey,attr,omitempty"`
		Address           string   `xml:"address,attr,omitempty"`
		Port              string   `xml:"port,attr,omitempty"`
		Protocol          string   `xml:"protocol,attr,omitempty"`
		MachineIdentifier string   `xml:"machineIdentifier,attr,omitempty"`
		SeekRange         string   `xml:"seekRange,attr,omitempty"`
		AudioStreamID     string   `xml:"audioStreamID,attr,omitempty"`
		SubtitleStreamID  string   `xml:"subtitleStreamID,attr,omitempty"`
		PlayQueueItemID   string   `xml:"playQueueItemID,attr,omitempty"`
		Controllable      string   `xml:"controllable,attr,omitempty"`
	}
	type MediaContainer struct {
		XMLName   xml.Name   `xml:"MediaContainer"`
		CommandID string     `xml:"commandID,attr,omitempty"`
		Location  string     `xml:"location,attr"`
		Timelines []Timeline `xml:"Timeline"`
	}
	plexState := plexStateFromCore(s.State)
	location := ""
	switch s.State {
	case core.StatePlaying:
		location = "fullScreenVideo"
	case core.StatePaused:
		location = "fullScreenVideo"
	}
	play := PlayMediaRequest{}
	if t.playContext != nil {
		play = t.playContext()
	}
	cmd := play.CommandID
	if commandID > 0 {
		cmd = strconv.Itoa(commandID)
	}
	video := Timeline{
		Type:     "video",
		State:    plexState,
		Time:     s.Position.Milliseconds(),
		Duration: s.Duration.Milliseconds(),
	}
	if play.MediaKey != "" {
		video.RatingKey = ratingKeyFromMediaKey(play.MediaKey)
		video.Key = play.MediaKey
		video.ContainerKey = play.ContainerKey
		video.Address = play.PlexServerAddress
		video.Port = play.PlexServerPort
		video.Protocol = play.PlexServerScheme
		video.MachineIdentifier = play.PlexMachineID
		video.AudioStreamID = play.AudioStreamID
		video.SubtitleStreamID = play.SubtitleStreamID
		video.PlayQueueItemID = play.PlayQueueItemID
	}
	if s.Duration > 0 {
		video.SeekRange = fmt.Sprintf("0-%d", s.Duration.Milliseconds())
	}
	if location != "" {
		video.Controllable = "playPause,stop,seekTo"
	}
	mc := MediaContainer{
		CommandID: cmd,
		Location:  location,
		Timelines: []Timeline{
			{Type: "music", State: "stopped"},
			{Type: "photo", State: "stopped"},
			video,
		},
	}
	out, _ := xml.Marshal(mc)
	return string(out)
}

// Subscribe registers or refreshes a controller subscription. The subscriber
// keeps receiving timeline pushes until Unsubscribe or until the TTL elapses.
func (t *TimelineBroker) Subscribe(clientID, host, port, protocol string, commandID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscribers[clientID] = &subscriber{
		clientID:  clientID,
		host:      host,
		port:      port,
		protocol:  protocol,
		commandID: commandID,
		lastSeen:  t.timeNow(),
	}
}

// Unsubscribe removes a controller. No-op if it wasn't registered.
func (t *TimelineBroker) Unsubscribe(clientID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.subscribers, clientID)
}

// TouchSubscriber refreshes a subscriber's lastSeen so the TTL prune doesn't
// reap an active controller. Called by the Companion on any request that
// carries a known X-Plex-Client-Identifier.
func (t *TimelineBroker) TouchSubscriber(clientID string) {
	t.TouchSubscriberCommand(clientID, 0)
}

// TouchSubscriberCommand refreshes a subscriber and optionally updates the
// latest controller commandID so pushed timelines can mirror it back.
func (t *TimelineBroker) TouchSubscriberCommand(clientID string, commandID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.subscribers[clientID]; ok {
		s.lastSeen = t.timeNow()
		if commandID > 0 {
			s.commandID = commandID
		}
	}
}

// subscriberCount is a test helper returning the current subscriber count.
func (t *TimelineBroker) subscriberCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subscribers)
}

func ratingKeyFromMediaKey(mediaKey string) string {
	const prefix = "/library/metadata/"
	if strings.HasPrefix(mediaKey, prefix) {
		return strings.TrimPrefix(mediaKey, prefix)
	}
	return mediaKey
}

func plexStateFromCore(state core.State) string {
	switch state {
	case core.StatePlaying:
		return "playing"
	case core.StatePaused:
		return "paused"
	default:
		return "stopped"
	}
}
