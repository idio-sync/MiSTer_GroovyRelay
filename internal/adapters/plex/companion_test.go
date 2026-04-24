package plex

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"

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
	if !fc.lastReq.Capabilities.CanPause || !fc.lastReq.Capabilities.CanSeek {
		t.Errorf("expected CanPause+CanSeek capabilities, got %+v", fc.lastReq.Capabilities)
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
		"/player/playback/refreshPlayQueue",
		"/player/playback/skipTo",
		"/player/playback/skipNext",
		"/player/playback/skipPrevious",
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

func TestSeekTo_ParsesOffset(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/player/playback/seekTo?offset=12345")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if fc.lastSeek != 12345 {
		t.Errorf("lastSeek = %d, want 12345", fc.lastSeek)
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
