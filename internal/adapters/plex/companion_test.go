package plex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jedivoodoo/mister-groovy-relay/internal/core"
)

func TestCompanion_RootReturns200(t *testing.T) {
	// Task 7.1 sanity: /resources must return 200 even without a wired-in
	// SessionManager — the resources endpoint advertises capabilities and does
	// not call into core. We pass nil here; other handlers (playMedia, etc.)
	// get a fakeCore in their own tests.
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "abc-123"}, nil)
	ts := httptest.NewServer(c.Handler())
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

// fakeCore is a SessionManager test double: records the last StartSession
// request, tracks pause/play/stop/seek calls, and can be queried for Status.
type fakeCore struct {
	mu        sync.Mutex
	lastReq   core.SessionRequest
	paused    bool
	played    bool
	stopped   bool
	lastSeek  int
	status    core.SessionStatus
	startErr  error
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

func TestPlayMedia_ParsesFields(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer"}, fc)
	ts := httptest.NewServer(c.Handler())
	defer ts.Close()

	url := ts.URL + "/player/playback/playMedia?" +
		"address=192.168.1.10&port=32400&protocol=http&" +
		"key=%2Flibrary%2Fmetadata%2F42&offset=0&" +
		"X-Plex-Client-Identifier=client-1&X-Plex-Token=tok"
	req, _ := http.NewRequest("GET", url, nil)
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
	if !fc.lastReq.Capabilities.CanPause || !fc.lastReq.Capabilities.CanSeek {
		t.Errorf("expected CanPause+CanSeek capabilities, got %+v", fc.lastReq.Capabilities)
	}
}

func TestPause_DelegatesToCore(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{}, fc)
	ts := httptest.NewServer(c.Handler())
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

func TestSeekTo_ParsesOffset(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{}, fc)
	ts := httptest.NewServer(c.Handler())
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
