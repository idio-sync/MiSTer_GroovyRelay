package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// fakeManager records calls into a SessionManager.
type fakeManager struct {
	mu    sync.Mutex
	reqs  []core.SessionRequest
	calls []string
	st    core.SessionStatus
	err   error
}

func (f *fakeManager) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.calls = append(f.calls, "StartSession:"+req.StreamURL)
	f.mu.Unlock()
	return f.err
}
func (f *fakeManager) Pause() error { f.add("Pause"); return f.err }
func (f *fakeManager) Play() error  { f.add("Play"); return f.err }
func (f *fakeManager) Stop() error  { f.add("Stop"); return f.err }
func (f *fakeManager) SeekTo(ms int) error {
	f.mu.Lock()
	f.calls = append(f.calls, "SeekTo")
	f.mu.Unlock()
	return f.err
}
func (f *fakeManager) Status() core.SessionStatus { f.mu.Lock(); defer f.mu.Unlock(); return f.st }
func (f *fakeManager) add(name string)            { f.mu.Lock(); f.calls = append(f.calls, name); f.mu.Unlock() }
func (f *fakeManager) lastReq() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		return core.SessionRequest{}
	}
	return f.reqs[len(f.reqs)-1]
}

func startTestPlaybackInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-1","TranscodingUrl":"/videos/itm-1/master.m3u8?MediaSourceId=src-1"}],
				"PlaySessionId":"ps-1"
			}`))
			return
		}
		_, _ = w.Write([]byte(`{"Id":"itm-1","Name":"Some Movie"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandlePlay_PlayNow_CallsStartSession(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid")

	payload := mustMarshal(t, map[string]any{
		"ItemIds":            []string{"itm-1"},
		"StartPositionTicks": 0,
		"PlayCommand":        "PlayNow",
	})

	a.HandlePlay(payload)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.calls)
		mgr.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	req := mgr.lastReq()
	if req.AdapterRef != "itm-1:ps-1" {
		t.Errorf("AdapterRef = %q, want itm-1:ps-1", req.AdapterRef)
	}
	if !strings.Contains(req.StreamURL, "/videos/itm-1/master.m3u8") {
		t.Errorf("StreamURL = %q", req.StreamURL)
	}
}

func TestHandlePlay_PlaybackInfoErrorCode_NoStartSession(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ErrorCode":"NoCompatibleStream","MediaSources":[]}`))
	}))
	defer jfSrv.Close()

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	payload := mustMarshal(t, map[string]any{
		"ItemIds":            []string{"itm-1"},
		"StartPositionTicks": 0,
		"PlayCommand":        "PlayNow",
	})

	a.HandlePlay(payload)

	time.Sleep(200 * time.Millisecond)

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 0 {
		t.Errorf("calls on PlaybackInfo error = %v, want none", mgr.calls)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandlePlaystate_PauseCallsCorePause(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "Pause"}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 1 || mgr.calls[0] != "Pause" {
		t.Errorf("calls = %v, want [Pause]", mgr.calls)
	}
}

func TestHandlePlaystate_UnpauseCallsCorePlay(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "Unpause"}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 1 || mgr.calls[0] != "Play" {
		t.Errorf("calls = %v, want [Play]", mgr.calls)
	}
}

func TestHandlePlaystate_StopCallsCoreStop(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "Stop"}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 1 || mgr.calls[0] != "Stop" {
		t.Errorf("calls = %v, want [Stop]", mgr.calls)
	}
}

func TestHandlePlaystate_SeekConvertsTicksToMs(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1")
	a.HandlePlaystate(mustMarshal(t, map[string]any{
		"Command": "Seek", "SeekPositionTicks": 50_000_000, // 5 seconds
	}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 1 || mgr.calls[0] != "SeekTo" {
		t.Errorf("calls = %v, want [SeekTo]", mgr.calls)
	}
}

func TestHandlePlaystate_PlayPauseTogglesByState(t *testing.T) {
	mgr := &fakeManager{st: core.SessionStatus{State: core.StatePlaying}}
	a := New(mgr, t.TempDir(), "dev-1")
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "PlayPause"}))
	mgr.mu.Lock()
	first := mgr.calls
	mgr.mu.Unlock()
	if len(first) != 1 || first[0] != "Pause" {
		t.Errorf("PlayPause from Playing → calls=%v, want [Pause]", first)
	}

	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StatePaused}
	mgr.mu.Unlock()

	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "PlayPause"}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 2 || mgr.calls[1] != "Play" {
		t.Errorf("PlayPause from Paused → calls=%v, want [..., Play]", mgr.calls)
	}
}
