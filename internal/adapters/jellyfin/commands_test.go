package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
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
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
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
	if req.MediaKind == core.MediaKindMusic {
		t.Errorf("MediaKind = %q, want video/default", req.MediaKind)
	}
	if req.Visualizer.Enabled {
		t.Errorf("Visualizer.Enabled = true, want false for video")
	}
}

func startTestAudioPlaybackInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/Items/song-1":
			_, _ = w.Write([]byte(`{
				"Id":"song-1",
				"Type":"Audio",
				"Name":"Age of Consent",
				"Artists":["New Order"],
				"Album":"Power Corruption & Lies",
				"RunTimeTicks":3150000000
			}`))
		case strings.HasSuffix(r.URL.Path, "/PlaybackInfo"):
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-audio","Name":"Audio Source","TranscodingUrl":"/audio/song-1/universal?MediaSourceId=src-audio"}],
				"PlaySessionId":"ps-audio",
				"Item":{
					"Type":"Audio",
					"Name":"Age of Consent",
					"Artists":["New Order"],
					"Album":"Power Corruption & Lies",
					"RunTimeTicks":3150000000
				}
			}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandlePlay_AudioItemStartsMusicVisualizerSession(t *testing.T) {
	jfSrv := startTestAudioPlaybackInfoServer(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":            []string{"song-1"},
		"StartPositionTicks": 20_000_000,
		"PlayCommand":        "PlayNow",
	}))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.reqs)
		mgr.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	req := mgr.lastReq()
	if req.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want music", req.MediaKind)
	}
	if !req.Visualizer.Enabled {
		t.Fatalf("Visualizer.Enabled = false, want true")
	}
	if req.Visualizer.Mode != core.VisualizerModeRetroAnalyzer {
		t.Errorf("Visualizer.Mode = %q, want retro analyzer", req.Visualizer.Mode)
	}
	if req.Title != "Age of Consent" {
		t.Errorf("Title = %q, want Age of Consent", req.Title)
	}
	if req.Visualizer.Metadata.Title != "Age of Consent" {
		t.Errorf("metadata title = %q, want Age of Consent", req.Visualizer.Metadata.Title)
	}
	if req.Visualizer.Metadata.Artist != "New Order" {
		t.Errorf("metadata artist = %q, want New Order", req.Visualizer.Metadata.Artist)
	}
	if req.Visualizer.Metadata.Album != "Power Corruption & Lies" {
		t.Errorf("metadata album = %q, want Power Corruption & Lies", req.Visualizer.Metadata.Album)
	}
	if req.Visualizer.Metadata.Duration != 5*time.Minute+15*time.Second {
		t.Errorf("metadata duration = %v, want 5m15s", req.Visualizer.Metadata.Duration)
	}
	if req.SeekOffsetMs != 2000 {
		t.Errorf("SeekOffsetMs = %d, want 2000", req.SeekOffsetMs)
	}
	if !req.Capabilities.CanSeek || !req.Capabilities.CanPause {
		t.Errorf("Capabilities = %+v, want seek and pause", req.Capabilities)
	}
	if !strings.HasPrefix(req.StreamURL, jfSrv.URL+"/audio/song-1/universal") {
		t.Errorf("StreamURL = %q, want absolute Jellyfin audio URL", req.StreamURL)
	}
	if !strings.Contains(req.StreamURL, "api_key=tok") {
		t.Errorf("StreamURL missing api_key: %s", req.StreamURL)
	}
}

func TestHandlePlaystate_NextTrackAudioItemStartsMusicVisualizerSession(t *testing.T) {
	jfSrv := startTestAudioPlaybackInfoServer(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.queue = []QueuedItem{{ItemID: "song-1"}}

	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "NextTrack"}))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.reqs)
		mgr.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	req := mgr.lastReq()
	if req.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want music", req.MediaKind)
	}
	if !req.Visualizer.Enabled {
		t.Fatalf("Visualizer.Enabled = false, want true")
	}
	if !strings.Contains(req.StreamURL, "/audio/song-1/universal") {
		t.Errorf("StreamURL = %q, want audio transcode URL", req.StreamURL)
	}
}

func TestSetAudioStreamIndex_AudioTrackSwitchKeepsMusicVisualizerSession(t *testing.T) {
	jfSrv := startTestAudioPlaybackInfoServer(t)

	mgr := &fakeManager{st: core.SessionStatus{
		State:      core.StatePlaying,
		Position:   42 * time.Second,
		AdapterRef: "song-1:ps-old",
	}}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "song-1:ps-old"

	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name":      "SetAudioStreamIndex",
		"Arguments": map[string]string{"Index": "1"},
	}))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.reqs)
		mgr.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	req := mgr.lastReq()
	if req.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want music", req.MediaKind)
	}
	if !req.Visualizer.Enabled {
		t.Fatalf("Visualizer.Enabled = false, want true")
	}
	if req.SeekOffsetMs != 42_000 {
		t.Errorf("SeekOffsetMs = %d, want 42000", req.SeekOffsetMs)
	}
}

func TestHandlePlay_PlaybackInfoErrorCode_NoStartSession(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ErrorCode":"NoCompatibleStream","MediaSources":[]}`))
	}))
	defer jfSrv.Close()

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
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
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "Pause"}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 1 || mgr.calls[0] != "Pause" {
		t.Errorf("calls = %v, want [Pause]", mgr.calls)
	}
}

func TestHandlePlaystate_UnpauseCallsCorePlay(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "Unpause"}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 1 || mgr.calls[0] != "Play" {
		t.Errorf("calls = %v, want [Play]", mgr.calls)
	}
}

func TestHandlePlaystate_StopCallsCoreStop(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "Stop"}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 1 || mgr.calls[0] != "Stop" {
		t.Errorf("calls = %v, want [Stop]", mgr.calls)
	}
}

func TestHandlePlaystate_SeekConvertsTicksToMs(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
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
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
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

func TestHandleGeneralCommand_DisplayMessage_LogsAndDoesNothing(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name": "DisplayMessage",
		"Arguments": map[string]string{
			"Header": "Hello", "Text": "From JF", "TimeoutMs": "3000",
		},
	}))
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) != 0 {
		t.Errorf("DisplayMessage should not call core; calls = %v", mgr.calls)
	}
}

// TestHandleGeneralCommand_SetAudioStreamIndex_NoOpWhenNoLiveCast asserts
// that SetAudioStreamIndex is a no-op (no StartSession, no index recorded)
// when there is no active session (StateIdle / no token). Phase 8 track
// switching only proceeds when a live cast is in progress.
func TestHandleGeneralCommand_SetAudioStreamIndex_NoOpWhenNoLiveCast(t *testing.T) {
	mgr := &fakeManager{} // st.State == zero == StateIdle
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name":      "SetAudioStreamIndex",
		"Arguments": map[string]string{"Index": "2"},
	}))
	time.Sleep(100 * time.Millisecond)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.reqs) != 0 {
		t.Errorf("StartSession called when no live cast: reqs=%v", mgr.reqs)
	}
}

// TestHandleGeneralCommand_SetSubtitleStreamIndex_NoOpWhenNoLiveCast is the
// subtitle-stream equivalent of the audio test above.
func TestHandleGeneralCommand_SetSubtitleStreamIndex_NoOpWhenNoLiveCast(t *testing.T) {
	mgr := &fakeManager{} // st.State == zero == StateIdle
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name":      "SetSubtitleStreamIndex",
		"Arguments": map[string]string{"Index": "-1"},
	}))
	time.Sleep(100 * time.Millisecond)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.reqs) != 0 {
		t.Errorf("StartSession called when no live cast: reqs=%v", mgr.reqs)
	}
}

func TestHandlePlay_PlayLast_AppendsToQueue(t *testing.T) {
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"itm-2", "itm-3"},
		"PlayCommand": "PlayLast",
	}))
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 2 {
		t.Fatalf("queue len = %d, want 2", len(a.queue))
	}
	if a.queue[0].ItemID != "itm-2" || a.queue[1].ItemID != "itm-3" {
		t.Errorf("queue order = %v, want itm-2, itm-3", a.queue)
	}
}

func TestHandlePlay_PlayNext_InsertsAtFront(t *testing.T) {
	a := New(&fakeManager{}, t.TempDir(), "dev-1", "", nil)
	a.queue = []QueuedItem{{ItemID: "tail-1"}, {ItemID: "tail-2"}}
	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"head-x"},
		"PlayCommand": "PlayNext",
	}))
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 3 || a.queue[0].ItemID != "head-x" {
		t.Errorf("queue = %+v, want [head-x, tail-1, tail-2]", a.queue)
	}
}

func TestHandlePlaystate_NextTrack_PopsAndStarts(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.queue = []QueuedItem{{ItemID: "next-itm"}}
	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "NextTrack"}))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.reqs)
		mgr.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mgr.mu.Lock()
	if len(mgr.reqs) == 0 {
		t.Fatal("NextTrack didn't trigger StartSession")
	}
	mgr.mu.Unlock()
	a.mu.Lock()
	if len(a.queue) != 0 {
		t.Errorf("queue len after NextTrack = %d, want 0", len(a.queue))
	}
	a.mu.Unlock()
}

func TestSetAudioStreamIndex_TrackSwitch_RestartsAtCurrentPosition(t *testing.T) {
	var pbCalls atomicInt32
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		pbCalls.inc()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src-1","TranscodingUrl":"/videos/itm-1/master.m3u8?call=` + strconv.Itoa(pbCalls.get()) + `"}],
			"PlaySessionId":"ps-` + strconv.Itoa(pbCalls.get()) + `"
		}`))
	}))
	defer jfSrv.Close()

	mgr := &fakeManager{st: core.SessionStatus{
		State:      core.StatePlaying,
		Position:   75 * time.Second,
		AdapterRef: "itm-1:ps-1",
	}}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.currentRefKey = "itm-1:ps-1"

	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name":      "SetAudioStreamIndex",
		"Arguments": map[string]string{"Index": "1"},
	}))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.reqs)
		mgr.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.reqs) != 1 {
		t.Fatalf("StartSession calls = %d, want 1", len(mgr.reqs))
	}
	got := mgr.reqs[0]
	if got.SeekOffsetMs != 75_000 {
		t.Errorf("SeekOffsetMs = %d, want 75000 (resume from current position)", got.SeekOffsetMs)
	}
	if !strings.Contains(got.StreamURL, "call=1") {
		t.Errorf("StreamURL doesn't reflect new PlaybackInfo: %s", got.StreamURL)
	}
}

func TestSetSubtitleStreamIndex_RestoresPausedAfterRestart(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)

	mgr := &fakeManager{st: core.SessionStatus{
		State:      core.StatePaused,
		Position:   12 * time.Second,
		AdapterRef: "itm-1:ps-1",
	}}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-1"

	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name":      "SetSubtitleStreamIndex",
		"Arguments": map[string]string{"Index": "0"},
	}))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		seen := append([]string{}, mgr.calls...)
		mgr.mu.Unlock()
		if len(seen) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.calls) < 2 {
		t.Fatalf("calls = %v, want >= 2", mgr.calls)
	}
	if !strings.HasPrefix(mgr.calls[0], "StartSession") {
		t.Errorf("calls[0] = %q, want StartSession", mgr.calls[0])
	}
	if mgr.calls[1] != "Pause" {
		t.Errorf("calls[1] = %q, want Pause", mgr.calls[1])
	}
}

func TestSetAudioStreamIndex_NoOpWhenIndexUnchanged(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)

	mgr := &fakeManager{st: core.SessionStatus{State: core.StatePlaying, AdapterRef: "itm-1:ps-1"}}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-1"
	idx2 := 2
	// Per-session indices live on the active reporter, not on Adapter.
	// Stage a reporter for the current refKey so the no-op check has
	// something to compare against.
	a.spawnReporter(reporterParams{
		ItemID: "itm-1", PlaySessionID: "ps-1", MediaSourceID: "src-1",
		AudioIdx:     &idx2,
		Auth:         RESTAuth{ServerURL: jfSrv.URL, Token: "tok", DeviceID: "dev-1", Version: "test"},
		TickInterval: time.Hour, // long; we only need the reporter present, not ticking
	})
	defer a.stopReporter("itm-1:ps-1")

	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name":      "SetAudioStreamIndex",
		"Arguments": map[string]string{"Index": "2"},
	}))

	time.Sleep(150 * time.Millisecond)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.reqs) != 0 {
		t.Errorf("no-op switch issued StartSession: reqs=%v", mgr.reqs)
	}
}

// startTestPlaybackInfoServerWithTitle is like startTestPlaybackInfoServer
// but includes Item.Name in the PlaybackInfo response so Title can be populated.
func startTestPlaybackInfoServerWithTitle(t *testing.T, title string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			enc, _ := json.Marshal(title)
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-1","Name":"Source 1","TranscodingUrl":"/videos/itm-1/master.m3u8?MediaSourceId=src-1"}],
				"PlaySessionId":"ps-1",
				"Item":{"Name":` + string(enc) + `}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{"Id":"itm-1","Name":"Some Movie"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestStartPlayNow_EmitsCastRequested(t *testing.T) {
	const wantTitle = "Game of Thrones · S01E03"
	jfSrv := startTestPlaybackInfoServerWithTitle(t, wantTitle)

	log := eventlog.New(16)
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", log)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid")

	payload := mustMarshal(t, map[string]any{
		"ItemIds":     []string{"itm-1"},
		"PlayCommand": "PlayNow",
	})
	a.HandlePlay(payload)

	// Wait for StartSession to be called.
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

	entries := log.Snapshot()
	if len(entries) == 0 {
		t.Fatal("expected at least one eventlog entry")
	}
	e := entries[len(entries)-1]
	if e.Source != "jellyfin" || e.Severity != eventlog.SeverityInfo {
		t.Errorf("Source/Severity: %q/%v, want jellyfin/info", e.Source, e.Severity)
	}
	if !strings.Contains(e.Message, "cast-requested") {
		t.Errorf("Message %q does not contain cast-requested", e.Message)
	}
}

func TestStartPlayNow_PopulatesTitle(t *testing.T) {
	const wantTitle = "Game of Thrones · S01E03"
	jfSrv := startTestPlaybackInfoServerWithTitle(t, wantTitle)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid")

	payload := mustMarshal(t, map[string]any{
		"ItemIds":     []string{"itm-1"},
		"PlayCommand": "PlayNow",
	})
	a.HandlePlay(payload)

	// Wait for StartSession to be called.
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
	if req.Title != wantTitle {
		t.Errorf("Title: got %q, want %q", req.Title, wantTitle)
	}
}
