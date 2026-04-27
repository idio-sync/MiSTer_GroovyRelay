package plex

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func newLoopbackServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("TCP listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("listen loopback server: %v", err)
	}

	ts := httptest.NewUnstartedServer(h)
	ts.Listener = l
	ts.Start()
	return ts
}

func TestCompanion_RootReturns200(t *testing.T) {
	// Task 7.1 sanity: /resources must return 200 even without a wired-in
	// SessionManager — the resources endpoint advertises capabilities and does
	// not call into core. We pass nil here; other handlers (playMedia, etc.)
	// get a fakeCore in their own tests.
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "abc-123"}, nil)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/resources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestCompanion_OPTIONSPreflightReturns204(t *testing.T) {
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "bridge-uuid", Version: "1.2.3"}, nil)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/player/playback/playMedia", nil)
	req.Header.Set("X-Plex-Client-Identifier", "client-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := resp.Header.Get("X-Plex-Client-Identifier"); got != "bridge-uuid" {
		t.Errorf("X-Plex-Client-Identifier = %q, want bridge-uuid", got)
	}
	if got := resp.Header.Get("X-Plex-Device-Name"); got != "MiSTer" {
		t.Errorf("X-Plex-Device-Name = %q, want MiSTer", got)
	}
	if got := resp.Header.Get("X-Plex-Product"); got != companionProduct {
		t.Errorf("X-Plex-Product = %q, want %s", got, companionProduct)
	}
}

func TestCompanion_ResourcesAdvertiseStableIdentity(t *testing.T) {
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "bridge-uuid", Version: "1.2.3"}, nil)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/resources", nil)
	req.Header.Set("X-Plex-Client-Identifier", "controller-uuid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Plex-Client-Identifier"); got != "bridge-uuid" {
		t.Errorf("X-Plex-Client-Identifier = %q, want bridge-uuid", got)
	}

	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		`machineIdentifier="bridge-uuid"`,
		`protocol="plex"`,
		`product="MiSTer_GroovyRelay"`,
		`version="1.2.3"`,
		`device="MiSTer"`,
		`model="MiSTer"`,
		`provides="player"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("resources missing %q: %s", want, body)
		}
	}
}

// fakeCore is a SessionManager test double: records the last StartSession
// request, tracks pause/play/stop/seek calls, and can be queried for Status.
type fakeCore struct {
	mu       sync.Mutex
	lastReq  core.SessionRequest
	starts   int
	paused   bool
	played   bool
	stopped  bool
	lastSeek int
	status   core.SessionStatus
	startErr error
}

func (f *fakeCore) StartSession(r core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastReq = r
	f.starts++
	return f.startErr
}
func (f *fakeCore) Pause() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = true
	return nil
}
func (f *fakeCore) Play() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.played = true
	return nil
}
func (f *fakeCore) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	return nil
}
func (f *fakeCore) SeekTo(ms int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSeek = ms
	return nil
}
func (f *fakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}
func (f *fakeCore) DropActiveCast(reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	return nil
}

func TestPlayMedia_ParsesFields(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{
		DeviceName:  "MiSTer",
		DeviceUUID:  "our-uuid",
		ProfileName: "Custom Profile",
	}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	url := ts.URL + "/player/playback/playMedia?" +
		"address=192.168.1.10&port=32400&protocol=http&" +
		"key=%2Flibrary%2Fmetadata%2F42&offset=0&" +
		"token=tok"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Plex-Client-Identifier", "client-1")
	req.Header.Set("X-Plex-Target-Client-Identifier", "our-uuid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	// Adapter should have constructed a stream URL and called core.StartSession.
	if fc.lastReq.StreamURL == "" {
		t.Error("adapter did not construct a stream URL")
	}
	if fc.lastReq.AdapterRef != "/library/metadata/42" {
		t.Errorf("AdapterRef = %q, want /library/metadata/42", fc.lastReq.AdapterRef)
	}
	// The raw token is opaque to core but should be embedded in the stream URL.
	if !strings.Contains(fc.lastReq.StreamURL, "X-Plex-Token=tok") {
		t.Errorf("stream URL missing plex token: %s", fc.lastReq.StreamURL)
	}
	if !strings.Contains(fc.lastReq.StreamURL, "X-Plex-Client-Identifier=our-uuid") {
		t.Errorf("stream URL missing stable bridge client identifier: %s", fc.lastReq.StreamURL)
	}
	if strings.Contains(fc.lastReq.StreamURL, "X-Plex-Client-Identifier=client-1") {
		t.Errorf("stream URL should not use controller client identifier: %s", fc.lastReq.StreamURL)
	}
	if !strings.Contains(fc.lastReq.StreamURL, "X-Plex-Device-Name=MiSTer") {
		t.Errorf("stream URL missing bridge device name: %s", fc.lastReq.StreamURL)
	}
	if !strings.Contains(fc.lastReq.StreamURL, "X-Plex-Client-Profile-Name=Custom+Profile") {
		t.Errorf("stream URL missing plex profile name: %s", fc.lastReq.StreamURL)
	}
	if !strings.Contains(fc.lastReq.StreamURL, "transcodeSessionId=") {
		t.Errorf("stream URL missing transcode session id: %s", fc.lastReq.StreamURL)
	}
	if fc.lastReq.OnStop == nil {
		t.Error("plex session should register transcode cleanup callback")
	}
	if !fc.lastReq.Capabilities.CanPause || !fc.lastReq.Capabilities.CanSeek {
		t.Errorf("expected CanPause+CanSeek capabilities, got %+v", fc.lastReq.Capabilities)
	}
}

func TestPlayMedia_HonorsConfiguredMaxBitrate(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{
		DeviceName:          "MiSTer",
		DeviceUUID:          "our-uuid",
		MaxVideoBitrateKbps: 6000,
	}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	url := ts.URL + "/player/playback/playMedia?" +
		"address=192.168.1.10&port=32400&protocol=http&" +
		"key=%2Flibrary%2Fmetadata%2F42&offset=0&token=tok"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Plex-Client-Identifier", "client-1")
	req.Header.Set("X-Plex-Target-Client-Identifier", "our-uuid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !strings.Contains(fc.lastReq.StreamURL, "maxVideoBitrate=6000") {
		t.Errorf("stream URL did not honor configured max bitrate: %s", fc.lastReq.StreamURL)
	}
}

func TestCompanion_SetMaxVideoBitrateKbpsTakesEffectOnNextPlay(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{
		DeviceName:          "MiSTer",
		DeviceUUID:          "our-uuid",
		MaxVideoBitrateKbps: 1500,
	}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	c.SetMaxVideoBitrateKbps(8000)

	url := ts.URL + "/player/playback/playMedia?" +
		"address=192.168.1.10&port=32400&protocol=http&" +
		"key=%2Flibrary%2Fmetadata%2F42&offset=0&token=tok"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Plex-Client-Identifier", "client-1")
	req.Header.Set("X-Plex-Target-Client-Identifier", "our-uuid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !strings.Contains(fc.lastReq.StreamURL, "maxVideoBitrate=8000") {
		t.Errorf("post-update bitrate not picked up: %s", fc.lastReq.StreamURL)
	}
	if strings.Contains(fc.lastReq.StreamURL, "maxVideoBitrate=1500") {
		t.Errorf("stale bitrate leaked through: %s", fc.lastReq.StreamURL)
	}
}

func TestPlayMedia_ApplicationRouteDelegatesToCore(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer"}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	url := ts.URL + "/player/application/playMedia?" +
		"address=192.168.1.10&port=32400&protocol=http&" +
		"key=%2Flibrary%2Fmetadata%2F99&offset=0&token=tok"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Plex-Client-Identifier", "client-2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if fc.lastReq.AdapterRef != "/library/metadata/99" {
		t.Errorf("AdapterRef = %q, want /library/metadata/99", fc.lastReq.AdapterRef)
	}
}

func TestPlaybackCompatibilityRoutes_Return200(t *testing.T) {
	c := NewCompanion(CompanionConfig{}, &fakeCore{})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	routes := []string{
	}
	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			resp, err := http.Get(ts.URL + route)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("status = %d", resp.StatusCode)
			}
		})
	}
}

func TestPlaybackQueueRoutesRequireActivePlexSession(t *testing.T) {
	c := NewCompanion(CompanionConfig{}, &fakeCore{})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	for _, route := range []string{
		"/player/playback/refreshPlayQueue",
		"/player/playback/skipTo",
		"/player/playback/skipNext",
		"/player/playback/skipPrevious",
	} {
		t.Run(route, func(t *testing.T) {
			resp, err := http.Get(ts.URL + route)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestPause_DelegatesToCore(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/pause")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if !fc.paused {
		t.Error("core.Pause was not called")
	}
}

func TestPause_TargetedAtAnotherClientIsRejected(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceUUID: "bridge-uuid"}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/pause?X-Plex-Target-Client-Identifier=ps4-uuid")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusPreconditionFailed)
	}
	if fc.paused {
		t.Error("core.Pause should not run for another target client identifier")
	}
}

func TestSeekTo_RestartsPlexTranscodeAtOffset(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  "192.168.1.10",
		PlexServerPort:     "32400",
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ContainerKey:       "/playQueues/99",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		TranscodeSessionID: "old-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/seekTo?offset=90000&commandID=12")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if fc.lastSeek != 0 {
		t.Errorf("core.SeekTo should not be used for Plex transcodes, lastSeek = %d", fc.lastSeek)
	}
	if fc.starts != 1 {
		t.Errorf("StartSession calls = %d, want 1", fc.starts)
	}
	if fc.lastReq.SeekOffsetMs != 90000 {
		t.Errorf("SeekOffsetMs = %d, want 90000", fc.lastReq.SeekOffsetMs)
	}
	for _, want := range []string{
		"offset=90",
		"transcodeSessionId=",
		"X-Plex-Client-Identifier=our-uuid",
		"X-Plex-Token=tok",
	} {
		if !strings.Contains(fc.lastReq.StreamURL, want) {
			t.Errorf("seek restart URL missing %q: %s", want, fc.lastReq.StreamURL)
		}
	}
	if strings.Contains(fc.lastReq.StreamURL, "old-transcode-id") {
		t.Errorf("seek restart should use a fresh transcode session id: %s", fc.lastReq.StreamURL)
	}
	got := c.lastPlaySession()
	if got.OffsetMs != 90000 {
		t.Errorf("remembered OffsetMs = %d, want 90000", got.OffsetMs)
	}
	if got.CommandID != "12" {
		t.Errorf("remembered CommandID = %q, want 12", got.CommandID)
	}
}

func TestSeekTo_RestoresPausedStateAfterRestart(t *testing.T) {
	fc := &fakeCore{status: core.SessionStatus{State: core.StatePaused, Position: 45000 * time.Millisecond}}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  "192.168.1.10",
		PlexServerPort:     "32400",
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		TranscodeSessionID: "old-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/seekTo?offset=90000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if fc.starts != 1 {
		t.Errorf("StartSession calls = %d, want 1", fc.starts)
	}
	if !fc.paused {
		t.Error("core.Pause should be called after paused seek restart")
	}
}

func TestSetStreams_SelectsStreamsAndRestartsAtCurrentPosition(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotQuery url.Values
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/42":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><Video><Media><Part id="99"/></Media></Video></MediaContainer>`))
		case "/library/parts/99":
			gotMethod = r.Method
			gotPath = r.URL.Path
			gotQuery = r.URL.Query()
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer pms.Close()
	u, err := url.Parse(pms.URL)
	if err != nil {
		t.Fatal(err)
	}

	fc := &fakeCore{
		status: core.SessionStatus{
			State:    core.StatePlaying,
			Position: 83000 * time.Millisecond,
			Duration: 120000 * time.Millisecond,
		},
	}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  u.Hostname(),
		PlexServerPort:     u.Port(),
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		AudioStreamID:      "100",
		SubtitleStreamID:   "200",
		TranscodeSessionID: "old-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/setStreams?audioStreamID=101&subtitleStreamID=0&commandID=14")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/library/parts/99" {
		t.Errorf("path = %q, want /library/parts/99", gotPath)
	}
	for key, want := range map[string]string{
		"audioStreamID":    "101",
		"subtitleStreamID": "0",
		"allParts":         "1",
		"X-Plex-Token":     "tok",
	} {
		if got := gotQuery.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if fc.starts != 1 {
		t.Errorf("StartSession calls = %d, want 1", fc.starts)
	}
	for _, want := range []string{
		"offset=83",
		"audioStreamID=101",
		"subtitleStreamID=0",
		"subtitles=none",
		"transcodeSessionId=",
	} {
		if !strings.Contains(fc.lastReq.StreamURL, want) {
			t.Errorf("setStreams restart URL missing %q: %s", want, fc.lastReq.StreamURL)
		}
	}
	got := c.lastPlaySession()
	if got.AudioStreamID != "101" {
		t.Errorf("remembered AudioStreamID = %q, want 101", got.AudioStreamID)
	}
	if got.SubtitleStreamID != "0" {
		t.Errorf("remembered SubtitleStreamID = %q, want 0", got.SubtitleStreamID)
	}
	if got.OffsetMs != 83000 {
		t.Errorf("remembered OffsetMs = %d, want 83000", got.OffsetMs)
	}
	if got.CommandID != "14" {
		t.Errorf("remembered CommandID = %q, want 14", got.CommandID)
	}
}

// Plex Web's in-player gear panel re-issues setStreams with the current
// audio + subtitle IDs whenever it opens. Treating that as a real change
// rebuilt the ffmpeg pipeline and made PMS surface "There was an unexpected
// error during playback" to the controller — see companion.go's no-op guard.
func TestSetStreams_NoOpWhenStreamsUnchanged(t *testing.T) {
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected PMS call: %s %s", r.Method, r.URL.Path)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer pms.Close()
	u, err := url.Parse(pms.URL)
	if err != nil {
		t.Fatal(err)
	}

	fc := &fakeCore{status: core.SessionStatus{State: core.StatePlaying, Position: 83000 * time.Millisecond}}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  u.Hostname(),
		PlexServerPort:     u.Port(),
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		AudioStreamID:      "100",
		SubtitleStreamID:   "200",
		TranscodeSessionID: "live-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/setStreams?audioStreamID=100&subtitleStreamID=200")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if fc.starts != 0 {
		t.Errorf("StartSession calls = %d, want 0 (no-op echo of current selection)", fc.starts)
	}
	got := c.lastPlaySession()
	if got.TranscodeSessionID != "live-transcode-id" {
		t.Errorf("transcode session ID changed: %q (no-op echo should preserve it)", got.TranscodeSessionID)
	}
}

func TestSetStreams_RestoresPausedStateAfterRestart(t *testing.T) {
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/42":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><Video><Media><Part id="99"/></Media></Video></MediaContainer>`))
		case "/library/parts/99":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer pms.Close()
	u, err := url.Parse(pms.URL)
	if err != nil {
		t.Fatal(err)
	}

	fc := &fakeCore{status: core.SessionStatus{State: core.StatePaused, Position: 83000 * time.Millisecond}}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  u.Hostname(),
		PlexServerPort:     u.Port(),
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		TranscodeSessionID: "old-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/setStreams?subtitleStreamID=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if fc.starts != 1 {
		t.Errorf("StartSession calls = %d, want 1", fc.starts)
	}
	if !fc.paused {
		t.Error("core.Pause should be called after paused setStreams restart")
	}
}

func TestDebugSession_ReportsLocalAndPMSChecks(t *testing.T) {
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Token") != "tok" {
			t.Errorf("missing PMS token on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/status/sessions":
			_, _ = w.Write([]byte(`<MediaContainer size="1">
				<Video key="/library/metadata/42" ratingKey="42">
					<Player machineIdentifier="our-uuid"/>
				</Video>
			</MediaContainer>`))
		case "/transcode/sessions":
			_, _ = w.Write([]byte(`<MediaContainer size="1">
				<TranscodeSession key="/transcode/sessions/transcode-1" transcodeSessionId="transcode-1"/>
			</MediaContainer>`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer pms.Close()
	u, err := url.Parse(pms.URL)
	if err != nil {
		t.Fatal(err)
	}

	fc := &fakeCore{
		status: core.SessionStatus{
			State:    core.StatePlaying,
			Position: 12000 * time.Millisecond,
			Duration: 90000 * time.Millisecond,
		},
	}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  u.Hostname(),
		PlexServerPort:     u.Port(),
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ContainerKey:       "/playQueues/99?own=1",
		PlexToken:          "tok",
		PlayQueueItemID:    "item-42",
		TranscodeSessionID: "transcode-1",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/debug/plex/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "tok") {
		t.Fatalf("debug response leaked Plex token: %s", body)
	}
	var got debugSessionReport
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Local.State != core.StatePlaying {
		t.Errorf("state = %q, want playing", got.Local.State)
	}
	if got.Local.PositionMs != 12000 {
		t.Errorf("positionMs = %d, want 12000", got.Local.PositionMs)
	}
	if !got.Local.HasToken {
		t.Error("HasToken = false, want true")
	}
	if got.Local.MediaKey != "/library/metadata/42" {
		t.Errorf("MediaKey = %q, want /library/metadata/42", got.Local.MediaKey)
	}
	if !got.PMS.StatusSessions.OK || !got.PMS.StatusSessions.Matched || got.PMS.StatusSessions.Count != 1 {
		t.Errorf("status session check = %+v, want OK matched count=1", got.PMS.StatusSessions)
	}
	if !got.PMS.TranscodeSessions.OK || !got.PMS.TranscodeSessions.Matched || got.PMS.TranscodeSessions.Count != 1 {
		t.Errorf("transcode session check = %+v, want OK matched count=1", got.PMS.TranscodeSessions)
	}
	if strings.Contains(got.PMS.StatusSessions.URL, "X-Plex-Token") {
		t.Errorf("status sessions debug URL leaked token: %s", got.PMS.StatusSessions.URL)
	}
}

func TestSkipNext_FetchesPlayQueueAndRestartsNextItem(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer size="3">
			<Video key="/library/metadata/41" ratingKey="41" playQueueItemID="1"/>
			<Video key="/library/metadata/42" ratingKey="42" playQueueItemID="2"/>
			<Video key="/library/metadata/43" ratingKey="43" playQueueItemID="3"/>
		</MediaContainer>`))
	}))
	defer pms.Close()
	u, err := url.Parse(pms.URL)
	if err != nil {
		t.Fatal(err)
	}

	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  u.Hostname(),
		PlexServerPort:     u.Port(),
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ContainerKey:       "/playQueues/99?own=1",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		PlayQueueItemID:    "2",
		TranscodeSessionID: "old-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/skipNext?commandID=13")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if gotPath != "/playQueues/99" {
		t.Errorf("play queue path = %q, want /playQueues/99", gotPath)
	}
	for key, want := range map[string]string{
		"own":            "1",
		"includeBefore":  "1",
		"includeAfter":   "1",
		"X-Plex-Token":   "tok",
	} {
		if got := gotQuery.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if fc.starts != 1 {
		t.Errorf("StartSession calls = %d, want 1", fc.starts)
	}
	if !strings.Contains(fc.lastReq.StreamURL, "path=%2Flibrary%2Fmetadata%2F43") {
		t.Errorf("skipNext URL should target next item: %s", fc.lastReq.StreamURL)
	}
	if !strings.Contains(fc.lastReq.StreamURL, "offset=0") {
		t.Errorf("skipNext URL should start next item at offset 0: %s", fc.lastReq.StreamURL)
	}
	got := c.lastPlaySession()
	if got.MediaKey != "/library/metadata/43" {
		t.Errorf("remembered MediaKey = %q, want /library/metadata/43", got.MediaKey)
	}
	if got.PlayQueueItemID != "3" {
		t.Errorf("remembered PlayQueueItemID = %q, want 3", got.PlayQueueItemID)
	}
	if got.CommandID != "13" {
		t.Errorf("remembered CommandID = %q, want 13", got.CommandID)
	}
}

func TestSkipTo_FetchesPlayQueueAndRestartsRequestedItem(t *testing.T) {
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer size="3">
			<Video key="/library/metadata/41" ratingKey="41" playQueueItemID="1"/>
			<Video key="/library/metadata/42" ratingKey="42" playQueueItemID="2"/>
			<Video key="/library/metadata/43" ratingKey="43" playQueueItemID="3"/>
		</MediaContainer>`))
	}))
	defer pms.Close()
	u, err := url.Parse(pms.URL)
	if err != nil {
		t.Fatal(err)
	}

	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  u.Hostname(),
		PlexServerPort:     u.Port(),
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/41",
		ContainerKey:       "/playQueues/99",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		PlayQueueItemID:    "1",
		TranscodeSessionID: "old-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/skipTo?playQueueItemID=3&commandID=15")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if fc.starts != 1 {
		t.Errorf("StartSession calls = %d, want 1", fc.starts)
	}
	if !strings.Contains(fc.lastReq.StreamURL, "path=%2Flibrary%2Fmetadata%2F43") {
		t.Errorf("skipTo URL should target requested item: %s", fc.lastReq.StreamURL)
	}
	got := c.lastPlaySession()
	if got.MediaKey != "/library/metadata/43" {
		t.Errorf("remembered MediaKey = %q, want /library/metadata/43", got.MediaKey)
	}
	if got.PlayQueueItemID != "3" {
		t.Errorf("remembered PlayQueueItemID = %q, want 3", got.PlayQueueItemID)
	}
	if got.CommandID != "15" {
		t.Errorf("remembered CommandID = %q, want 15", got.CommandID)
	}
}

func TestSkipTo_RestoresPausedStateAfterRestart(t *testing.T) {
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer size="2">
			<Video key="/library/metadata/42" ratingKey="42" playQueueItemID="2"/>
			<Video key="/library/metadata/43" ratingKey="43" playQueueItemID="3"/>
		</MediaContainer>`))
	}))
	defer pms.Close()
	u, err := url.Parse(pms.URL)
	if err != nil {
		t.Fatal(err)
	}

	fc := &fakeCore{status: core.SessionStatus{State: core.StatePaused, Position: 83000 * time.Millisecond}}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid"}, fc)
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerAddress:  u.Hostname(),
		PlexServerPort:     u.Port(),
		PlexServerScheme:   "http",
		MediaKey:           "/library/metadata/42",
		ContainerKey:       "/playQueues/99",
		ClientID:           "controller-uuid",
		PlexToken:          "tok",
		PlayQueueItemID:    "2",
		TranscodeSessionID: "old-transcode-id",
	})
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/skipTo?key=%2Flibrary%2Fmetadata%2F43")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if fc.starts != 1 {
		t.Errorf("StartSession calls = %d, want 1", fc.starts)
	}
	if !fc.paused {
		t.Error("core.Pause should be called after paused skipTo restart")
	}
}

func TestRememberPlaySession_StoresLast(t *testing.T) {
	c := NewCompanion(CompanionConfig{}, &fakeCore{})
	p := PlayMediaRequest{MediaKey: "/library/metadata/7", ClientID: "c1"}
	c.rememberPlaySession(p)
	got := c.lastPlaySession()
	if got.MediaKey != "/library/metadata/7" {
		t.Errorf("MediaKey = %q, want /library/metadata/7", got.MediaKey)
	}
	if got.ClientID != "c1" {
		t.Errorf("ClientID = %q, want c1", got.ClientID)
	}
}

// TestSessionRequestFor_OnStopBroadcastsAndClearsForCrossAdapter
// is the URL-adapter precursor contract for the cross-adapter case:
// when OnStop fires for the prior Plex session and lastPlay still
// points at that session (no successor Plex playMedia interleaved),
// the closure must (1) push a stopped timeline using the captured
// PlayMediaRequest — NOT whatever Manager.Status() currently reports —
// and (2) clear c.lastPlay so the broker's next 1Hz tick doesn't keep
// pushing Plex media identity while a foreign adapter owns the session.
func TestSessionRequestFor_OnStopBroadcastsAndClearsForCrossAdapter(t *testing.T) {
	// Set up a controller endpoint and a broker pointed at it.
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	c := NewCompanion(CompanionConfig{
		DeviceName: "MiSTer", DeviceUUID: "uuid-1", ProfileName: "Plex Home Theater",
	}, nil)
	broker := NewTimelineBroker(TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		func() core.SessionStatus { return core.SessionStatus{} })
	// playContext returns a wrong/foreign play — simulates URL adapter active.
	broker.SetPlayContextProvider(func() PlayMediaRequest {
		return PlayMediaRequest{MediaKey: "/library/metadata/wrong"}
	})
	c.SetTimeline(broker)
	u, _ := url.Parse(srv.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	broker.Subscribe("client-a", host, port, "http", 0)

	// Remember a Plex play, then build a SessionRequest from it.
	// PlexServerAddress points at 127.0.0.1:1 — guaranteed-unreachable
	// loopback; StopTranscodeSession's TCP connect fails fast (refused)
	// across Linux/macOS/Windows so the test isn't gated on network
	// timeouts. Note that even if it WERE slow, the broadcast happens
	// BEFORE StopTranscodeSession in the closure (see step 4) so this
	// test would still complete deterministically.
	prior := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-1", PlexToken: "tok",
	}
	c.rememberPlaySession(prior)
	req := c.sessionRequestFor(prior)

	// Fire OnStop synchronously (simulates Manager preempt notifying
	// the prior session). The closure body itself is synchronous — the
	// broadcast pushes happen before OnStop returns.
	req.OnStop("preempted")

	// 1. lastPlay must be cleared.
	if got := c.lastPlaySession().MediaKey; got != "" {
		t.Errorf("lastPlay not cleared: MediaKey = %q", got)
	}

	// 2. Subscriber received exactly one stopped timeline addressed to
	//    the prior media key.
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("subscriber pushes = %d, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], `key="/library/metadata/42"`) {
		t.Errorf("body did not use captured prior MediaKey: %s", bodies[0])
	}
	if strings.Contains(bodies[0], "/library/metadata/wrong") {
		t.Errorf("body leaked playContext data: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], `state="stopped"`) {
		t.Errorf("body not state=stopped: %s", bodies[0])
	}
}

// TestSessionRequestFor_OnStop_CASClearNoOpsWhenLastPlayDiffers pins
// the CAS clear's contract (only zero lastPlay if MediaKey still matches
// captured). It exercises the *outcome* of the regression case from
// round-3 review — handlePlayMedia's race:
//
//  1. notifyStoppedTimeline (sync)
//  2. core.StartSession (spawns OnStop goroutine for OLD session)
//  3. rememberPlaySession(NEW)  // overwrites c.lastPlay
//  4. notifyTimeline             // broadcasts NEW
//
// — by simulating the post-race state synchronously: it calls
// rememberPlaySession(NEW) BEFORE firing OnStop. With
// clearPlaySessionIfMatches, the closure no-ops because c.lastPlay
// holds the NEW session's MediaKey. Race coverage proper requires the
// integration path through handlePlayMedia (out of scope for this
// unit test, but `go test -race` will catch concurrent unsafe access
// elsewhere).
func TestSessionRequestFor_OnStop_CASClearNoOpsWhenLastPlayDiffers(t *testing.T) {
	c := NewCompanion(CompanionConfig{
		DeviceName: "MiSTer", DeviceUUID: "uuid-1", ProfileName: "Plex Home Theater",
	}, nil)
	// No timeline broker — this test only exercises lastPlay state.

	prior := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-old", PlexToken: "tok",
	}
	c.rememberPlaySession(prior)
	req := c.sessionRequestFor(prior)

	// Simulate handlePlayMedia step 3 happening BEFORE OnStop's clear:
	// remember a NEW session so c.lastPlay no longer matches captured.
	newer := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/99", TranscodeSessionID: "tsid-new", PlexToken: "tok",
	}
	c.rememberPlaySession(newer)

	// Now fire the prior session's OnStop (simulating goroutine running
	// after rememberPlaySession). With clearPlaySessionIfMatches, this
	// must NOT wipe lastPlay because the captured MediaKey ("/library/
	// metadata/42") no longer matches c.lastPlay.MediaKey ("/library/
	// metadata/99").
	req.OnStop("preempted")

	if got := c.lastPlaySession().MediaKey; got != "/library/metadata/99" {
		t.Errorf("lastPlay MediaKey = %q, want %q (NEW session must survive prior OnStop)",
			got, "/library/metadata/99")
	}
}

// TestSessionRequestFor_OnStop_CASClearNoOpsOnSeekSameMedia covers the
// seek/setStreams race: the controller asks us to seek within the SAME
// movie, so handleSeekTo mints a fresh TranscodeSessionID but reuses
// the prior MediaKey. The flow is:
//
//  1. core.StartSession (spawns OnStop goroutine for OLD transcode)
//  2. rememberPlaySession(NEW)  // same MediaKey, new TranscodeSessionID
//  3. OnStop goroutine wakes up and runs clearPlaySessionIfMatches
//
// If clearPlaySessionIfMatches keys on MediaKey alone, the prior
// session's OnStop wipes c.lastPlay even though it now describes the
// NEW transcode — and the next timeline poll renders no key/ratingKey,
// causing controllers to lose track of the cast. The discriminator that
// actually identifies a session is TranscodeSessionID (UUID, never
// collides), so the CAS must compare on that.
func TestSessionRequestFor_OnStop_CASClearNoOpsOnSeekSameMedia(t *testing.T) {
	c := NewCompanion(CompanionConfig{
		DeviceName: "MiSTer", DeviceUUID: "uuid-1", ProfileName: "Plex Home Theater",
	}, nil)

	prior := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-old", PlexToken: "tok",
	}
	c.rememberPlaySession(prior)
	req := c.sessionRequestFor(prior)

	// Simulate handleSeekTo step 2 — same movie, fresh transcode session.
	seek := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-new", PlexToken: "tok",
		OffsetMs: 30000,
	}
	c.rememberPlaySession(seek)

	// OLD session's OnStop fires last (after rememberPlaySession). It must
	// NOT wipe lastPlay because the live transcode is the NEW one.
	req.OnStop("preempted")

	got := c.lastPlaySession()
	if got.MediaKey != "/library/metadata/42" {
		t.Errorf("lastPlay MediaKey = %q, want /library/metadata/42 (NEW seek session must survive prior OnStop)", got.MediaKey)
	}
	if got.TranscodeSessionID != "tsid-new" {
		t.Errorf("lastPlay TranscodeSessionID = %q, want tsid-new (NEW seek session must survive prior OnStop)", got.TranscodeSessionID)
	}
}
