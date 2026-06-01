package jellyfin

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

func jellyfinTinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{B: 0xff, A: 0xff})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// fakeManager records calls into a SessionManager.
type fakeManager struct {
	mu               sync.Mutex
	reqs             []core.SessionRequest
	idleReqs         []core.SessionRequest
	calls            []string
	st               core.SessionStatus
	err              error
	nextGen          uint64
	startIdleStarted bool
	startIdleErr     error
	onStartIdle      func()
	stopIfCalls      int
	stopIfRef        string
	stopIfGeneration uint64
	onStatus         func()
	mode             string
	onStop           func()
}

func (f *fakeManager) nextGenerationLocked() uint64 {
	f.nextGen++
	if f.nextGen == 0 {
		f.nextGen = 1
	}
	return f.nextGen
}

func (f *fakeManager) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.calls = append(f.calls, "StartSession:"+req.StreamURL)
	f.mu.Unlock()
	return f.err
}
func (f *fakeManager) StartSessionIfIdle(req core.SessionRequest) (bool, error) {
	_, started, err := f.StartSessionIfIdleSnapshot(req)
	return started, err
}
func (f *fakeManager) StartSessionIfIdleSnapshot(req core.SessionRequest) (core.SessionStatus, bool, error) {
	if f.onStartIdle != nil {
		f.onStartIdle()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idleReqs = append(f.idleReqs, req)
	f.calls = append(f.calls, "StartSessionIfIdle:"+req.StreamURL)
	if f.st.State != core.StateIdle || !f.startIdleStarted {
		return core.SessionStatus{}, false, nil
	}
	err := f.startIdleErr
	if err == nil {
		err = f.err
	}
	if err != nil {
		return core.SessionStatus{}, true, err
	}
	f.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: req.AdapterRef, Generation: f.nextGenerationLocked()}
	return f.st, true, nil
}
func (f *fakeManager) Pause() error { f.add("Pause"); return f.err }
func (f *fakeManager) Play() error  { f.add("Play"); return f.err }
func (f *fakeManager) Stop() error {
	f.add("Stop")
	if f.onStop != nil {
		f.onStop()
	}
	return f.err
}
func (f *fakeManager) StopIfSession(ref string, generation uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopIfCalls++
	f.stopIfRef = ref
	f.stopIfGeneration = generation
	if ref == "" || generation == 0 || f.st.AdapterRef != ref || f.st.Generation != generation {
		return false, nil
	}
	f.st = core.SessionStatus{State: core.StateIdle}
	return true, nil
}
func (f *fakeManager) SeekTo(ms int) error {
	f.mu.Lock()
	f.calls = append(f.calls, "SeekTo")
	f.mu.Unlock()
	return f.err
}
func (f *fakeManager) Status() core.SessionStatus {
	if f.onStatus != nil {
		f.onStatus()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}
func (f *fakeManager) add(name string) { f.mu.Lock(); f.calls = append(f.calls, name); f.mu.Unlock() }
func (f *fakeManager) VisualizerMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}
func (f *fakeManager) lastReq() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		return core.SessionRequest{}
	}
	return f.reqs[len(f.reqs)-1]
}
func (f *fakeManager) lastIdleReq() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.idleReqs) == 0 {
		return core.SessionRequest{}
	}
	return f.idleReqs[len(f.idleReqs)-1]
}

func TestFakeManager_StartSessionIfIdleBusySuppressesStartError(t *testing.T) {
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StatePlaying, AdapterRef: "busy"},
		startIdleStarted: true,
		startIdleErr:     errors.New("start failed"),
	}

	started, err := mgr.StartSessionIfIdle(core.SessionRequest{AdapterRef: "next"})

	if err != nil {
		t.Fatalf("err = %v, want nil when idle admission fails", err)
	}
	if started {
		t.Fatal("started = true, want false when core is busy")
	}
}

func TestFakeManager_StartSessionIfIdleAdmittedErrorReportsStarted(t *testing.T) {
	startErr := errors.New("start failed")
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
		startIdleErr:     startErr,
	}

	started, err := mgr.StartSessionIfIdle(core.SessionRequest{AdapterRef: "next"})

	if !errors.Is(err, startErr) {
		t.Fatalf("err = %v, want %v", err, startErr)
	}
	if !started {
		t.Fatal("started = false, want true when idle admission matched but start failed")
	}
}

func waitForFakeManagerReq(t *testing.T, mgr *fakeManager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.reqs)
		mgr.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for StartSession")
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

func startMappedPlaybackInfoServer(t *testing.T, failPlaybackInfoFor map[string]bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "Items" && parts[len(parts)-1] == "PlaybackInfo" {
			itemID := parts[1]
			if failPlaybackInfoFor[itemID] {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-` + itemID + `","TranscodingUrl":"/videos/` + itemID + `/master.m3u8?MediaSourceId=src-` + itemID + `"}],
				"PlaySessionId":"ps-` + itemID + `",
				"Item":{"Name":"` + itemID + `"}
			}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"metadata","Name":"metadata"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func startBlockingPlaybackInfoServer(t *testing.T, blockedItemID string) (*httptest.Server, <-chan struct{}, func()) {
	t.Helper()
	blockedStarted := make(chan struct{})
	releaseBlocked := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "Items" && parts[len(parts)-1] == "PlaybackInfo" {
			itemID := parts[1]
			if itemID == blockedItemID {
				startedOnce.Do(func() { close(blockedStarted) })
				select {
				case <-releaseBlocked:
				case <-r.Context().Done():
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-` + itemID + `","TranscodingUrl":"/videos/` + itemID + `/master.m3u8?MediaSourceId=src-` + itemID + `"}],
				"PlaySessionId":"ps-` + itemID + `",
				"Item":{"Name":"` + itemID + `"}
			}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"metadata","Name":"metadata"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	release := func() {
		releaseOnce.Do(func() { close(releaseBlocked) })
	}
	t.Cleanup(release)
	return srv, blockedStarted, release
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

func TestHandlePlayRecordsCompanionHistory(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid")

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"itm-1"},
		"PlayCommand": "PlayNow",
	}))
	waitForFakeManagerReq(t, mgr)

	history := a.CompanionHistory()
	if len(history) != 1 {
		t.Fatalf("CompanionHistory len = %d, want 1", len(history))
	}
	entry := history[0]
	if entry.Title != "Some Movie" {
		t.Fatalf("history Title = %q, want Some Movie", entry.Title)
	}
	if entry.URLDisplay != "itm-1" {
		t.Fatalf("history URLDisplay = %q, want itm-1", entry.URLDisplay)
	}
	if entry.LastPlayed.IsZero() {
		t.Fatalf("history LastPlayed is zero")
	}
}

func TestCompanionHistorySurvivesNewAdapter(t *testing.T) {
	dataDir := t.TempDir()
	a := New(&fakeManager{}, dataDir, "dev-1", "", nil)
	a.recordCompanionHistory("itm-1", PlaybackInfoResult{Title: "Some Movie"}, time.Now())

	fresh := New(&fakeManager{}, dataDir, "dev-1", "", nil)
	history := fresh.CompanionHistory()
	if len(history) != 1 {
		t.Fatalf("fresh CompanionHistory len = %d, want 1", len(history))
	}
	if history[0].Title != "Some Movie" || history[0].URLDisplay != "itm-1" {
		t.Fatalf("fresh history[0] = %+v, want persisted Jellyfin row", history[0])
	}
	if history[0].LastPlayed.IsZero() {
		t.Fatalf("fresh history LastPlayed is zero")
	}
}

func startTestAudioPlaybackInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/Items/song-1/Images/Primary":
			w.WriteHeader(http.StatusNotFound)
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
	// Jellyfin stays neutral about the user's visualizer choice. It supplies
	// the legacy compatibility mode here; core.Manager resolves the configured
	// bridge visualizer mode when the session starts.
	if req.Visualizer.Mode != core.VisualizerModeRetroAnalyzer {
		t.Errorf("Visualizer.Mode = %q, want compatibility retro analyzer", req.Visualizer.Mode)
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

func startTestAudioArtworkServer(t *testing.T, imageStatus int) (*httptest.Server, *string) {
	t.Helper()
	var gotAPIKey string
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/Items/song-1/Images/Primary":
			gotAPIKey = r.URL.Query().Get("api_key")
			if imageStatus >= 400 {
				w.WriteHeader(imageStatus)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(jellyfinTinyPNG(t))
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
	return srv, &gotAPIKey
}

func TestHandlePlay_AudioItemCachesPrimaryArtworkAndCleansUp(t *testing.T) {
	jfSrv, gotAPIKey := startTestAudioArtworkServer(t, http.StatusOK)

	mgr := &fakeManager{mode: string(core.VisualizerModeCoverVU)}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"song-1"},
		"PlayCommand": "PlayNow",
	}))
	waitForFakeManagerReq(t, mgr)

	req := mgr.lastReq()
	if *gotAPIKey != "tok" {
		t.Fatalf("image api_key = %q, want tok", *gotAPIKey)
	}
	path := req.Visualizer.Metadata.ArtworkPath
	if path == "" {
		t.Fatal("ArtworkPath empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cached artwork stat: %v", err)
	}
	if req.OnStop == nil {
		t.Fatal("OnStop nil")
	}
	req.OnStop("stopped")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cached artwork after OnStop stat err = %v, want missing", err)
	}
}

func TestHandlePlay_AudioItemArtwork404FallsBackToEmptyPath(t *testing.T) {
	jfSrv, _ := startTestAudioArtworkServer(t, http.StatusNotFound)

	mgr := &fakeManager{mode: string(core.VisualizerModeCoverVU)}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	t.Cleanup(func() { quiesceAdapter(a) })
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"song-1"},
		"PlayCommand": "PlayNow",
	}))
	waitForFakeManagerReq(t, mgr)

	req := mgr.lastReq()
	if req.MediaKind != core.MediaKindMusic || !req.Visualizer.Enabled {
		t.Fatalf("request = %+v, want music visualizer", req)
	}
	if req.Visualizer.Metadata.ArtworkPath != "" {
		t.Fatalf("ArtworkPath = %q, want empty on fetch failure", req.Visualizer.Metadata.ArtworkPath)
	}
}

func TestHandlePlay_AudioItemStartSessionFailureRemovesArtwork(t *testing.T) {
	jfSrv, _ := startTestAudioArtworkServer(t, http.StatusOK)

	mgr := &fakeManager{err: errors.New("reject"), mode: string(core.VisualizerModeCoverVU)}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"song-1"},
		"PlayCommand": "PlayNow",
	}))
	waitForFakeManagerReq(t, mgr)

	path := mgr.lastReq().Visualizer.Metadata.ArtworkPath
	if path == "" {
		t.Fatal("ArtworkPath empty")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cached artwork after StartSession failure stat err = %v, want missing", err)
	}
}

func TestHandlePlay_AudioItemPlaybackInfoFailureRemovesArtwork(t *testing.T) {
	playbackInfoHit := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/Items/song-1/Images/Primary":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(jellyfinTinyPNG(t))
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
			once.Do(func() { close(playbackInfoHit) })
			http.Error(w, "no stream", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	jfSrv := httptest.NewServer(mux)
	t.Cleanup(jfSrv.Close)

	dataDir := t.TempDir()
	mgr := &fakeManager{mode: string(core.VisualizerModeCoverVU)}
	a := New(mgr, dataDir, "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"song-1"},
		"PlayCommand": "PlayNow",
	}))

	select {
	case <-playbackInfoHit:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for PlaybackInfo request")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if entries, err := os.ReadDir(artworkcache.Root(dataDir)); err == nil && len(entries) == 0 {
			return
		} else if os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	entries, err := os.ReadDir(artworkcache.Root(dataDir))
	if err != nil {
		t.Fatalf("read artwork root: %v", err)
	}
	t.Fatalf("cached artwork remained after PlaybackInfo failure: %v", entries)
}

func TestHandlePlaystate_NextTrackAudioItemStartsMusicVisualizerSession(t *testing.T) {
	jfSrv := startTestAudioPlaybackInfoServer(t)

	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	t.Cleanup(func() { quiesceAdapter(a) })
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

// quiesceAdapter drains the adapter's detached start goroutines so that
// t.TempDir()'s automatic RemoveAll cleanup can't race their in-flight
// writes into the data dir (artwork-cache PNGs and jellyfin_history.json).
// On Windows that race is fatal: RemoveAll fails with "directory is not
// empty", or the atomic history rename fails with "Access is denied".
// Register via t.Cleanup right after New() so it runs before the TempDir
// removal (cleanups run LIFO; t.TempDir registered its removal first).
func quiesceAdapter(a *Adapter) {
	// Stop reporters first so a reporter-driven OnStop can't spawn a
	// fresh auto-advance start after we begin waiting.
	_ = a.Stop()
	// Then wait for any in-flight controller/auto-advance start
	// goroutine to finish its data-dir writes.
	a.bgStarts.Wait()
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

func startRestartPlaybackInfoServer(t *testing.T) (*httptest.Server, *atomicInt32, *atomic.Int64) {
	t.Helper()
	var calls atomicInt32
	var gotStartTicks atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			calls.inc()
			var body struct {
				StartTimeTicks int64  `json:"StartTimeTicks"`
				MediaSourceID  string `json:"MediaSourceId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotStartTicks.Store(body.StartTimeTicks)
			if body.MediaSourceID != "src-1" {
				t.Errorf("PlaybackInfo MediaSourceId = %q, want src-1", body.MediaSourceID)
			}
			n := calls.get()
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-1","TranscodingUrl":"/videos/itm-1/master.m3u8?call=` + strconv.Itoa(n) + `"}],
				"PlaySessionId":"ps-` + strconv.Itoa(n) + `",
				"Item":{"Name":"Some Movie"}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{"Id":"itm-1","Name":"Some Movie"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls, &gotStartTicks
}

func TestHandlePlaystate_SeekRestartsJellyfinTranscodeAtTarget(t *testing.T) {
	jfSrv, pbCalls, gotStartTicks := startRestartPlaybackInfoServer(t)

	mgr := &fakeManager{st: core.SessionStatus{
		State:      core.StatePlaying,
		Position:   12 * time.Second,
		AdapterRef: "itm-1:ps-old",
	}}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-old"
	a.reporters["itm-1:ps-old"] = &reporter{
		capturedRefKey: "itm-1:ps-old",
		itemID:         "itm-1",
		playSessionID:  "ps-old",
		mediaSourceID:  "src-1",
	}

	a.HandlePlaystate(mustMarshal(t, map[string]any{
		"Command":           "Seek",
		"SeekPositionTicks": 50_000_000,
	}))

	waitForFakeManagerReq(t, mgr)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.reqs) != 1 {
		t.Fatalf("StartSession calls = %d, want 1", len(mgr.reqs))
	}
	if pbCalls.get() != 1 {
		t.Fatalf("PlaybackInfo calls = %d, want 1", pbCalls.get())
	}
	if gotStartTicks.Load() != 50_000_000 {
		t.Errorf("PlaybackInfo StartTimeTicks = %d, want 50000000", gotStartTicks.Load())
	}
	got := mgr.reqs[0]
	if got.SeekOffsetMs != 5_000 {
		t.Errorf("SeekOffsetMs = %d, want 5000", got.SeekOffsetMs)
	}
	if !strings.Contains(got.StreamURL, "call=1") {
		t.Errorf("StreamURL did not come from fresh PlaybackInfo: %s", got.StreamURL)
	}
	for _, call := range mgr.calls {
		if call == "SeekTo" {
			t.Errorf("core.SeekTo called for Jellyfin transcode restart; calls=%v", mgr.calls)
		}
	}
}

func TestHandlePlaystate_UnpauseRestartsJellyfinTranscodeAtPausedPosition(t *testing.T) {
	jfSrv, _, gotStartTicks := startRestartPlaybackInfoServer(t)

	mgr := &fakeManager{st: core.SessionStatus{
		State:      core.StatePaused,
		Position:   42 * time.Second,
		AdapterRef: "itm-1:ps-old",
	}}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-old"
	a.reporters["itm-1:ps-old"] = &reporter{
		capturedRefKey: "itm-1:ps-old",
		itemID:         "itm-1",
		playSessionID:  "ps-old",
		mediaSourceID:  "src-1",
	}

	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "Unpause"}))

	waitForFakeManagerReq(t, mgr)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	got := mgr.reqs[0]
	if gotStartTicks.Load() != 420_000_000 {
		t.Errorf("PlaybackInfo StartTimeTicks = %d, want 420000000", gotStartTicks.Load())
	}
	if got.SeekOffsetMs != 42_000 {
		t.Errorf("SeekOffsetMs = %d, want 42000", got.SeekOffsetMs)
	}
	for _, call := range mgr.calls {
		if call == "Play" {
			t.Errorf("core.Play called for Jellyfin transcode resume; calls=%v", mgr.calls)
		}
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

func TestHandlePlay_PlayNowCapturesTailQueueFromStartIndex(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":       []string{"itm-0", "itm-1", "itm-2", "itm-3"},
		"StartIndex":    1,
		"PlayCommand":   "PlayNow",
		"MediaSourceId": "src-selected",
	}))

	waitForFakeManagerReq(t, mgr)
	req := mgr.lastReq()
	if req.AdapterRef != "itm-1:ps-1" {
		t.Fatalf("started AdapterRef = %q, want itm-1:ps-1", req.AdapterRef)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 2 {
		t.Fatalf("queue len = %d, want 2", len(a.queue))
	}
	if a.queue[0].ItemID != "itm-2" || a.queue[1].ItemID != "itm-3" {
		t.Fatalf("queue order = %+v, want itm-2,itm-3", a.queue)
	}
	if a.queue[0].QueueEntryID == 0 || a.queue[1].QueueEntryID == 0 || a.queue[0].QueueEntryID == a.queue[1].QueueEntryID {
		t.Fatalf("QueueEntryID values = %d,%d, want distinct non-zero IDs", a.queue[0].QueueEntryID, a.queue[1].QueueEntryID)
	}
	if a.queue[0].MediaSourceID != "src-selected" || a.queue[1].MediaSourceID != "src-selected" {
		t.Fatalf("MediaSourceID not preserved in queue: %+v", a.queue)
	}
}

func TestHandlePlay_PlayNowInvalidStartIndexUsesZero(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"itm-1", "itm-2"},
		"StartIndex":  99,
		"PlayCommand": "PlayNow",
	}))

	waitForFakeManagerReq(t, mgr)
	if got := mgr.lastReq().AdapterRef; got != "itm-1:ps-1" {
		t.Fatalf("started AdapterRef = %q, want itm-1:ps-1", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "itm-2" {
		t.Fatalf("queue = %+v, want [itm-2]", a.queue)
	}
}

func TestHandlePlay_PlayNextMultipleItemsPreservesOrderAheadOfTail(t *testing.T) {
	a := New(&fakeManager{}, t.TempDir(), "dev-1", "", nil)
	a.queue = []QueuedItem{{QueueEntryID: 99, ItemID: "tail-itm"}}
	a.nextQueueEntryID = 99

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":       []string{"head-1", "head-2"},
		"PlayCommand":   "PlayNext",
		"MediaSourceId": "src-next",
	}))

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 3 {
		t.Fatalf("queue len = %d, want 3", len(a.queue))
	}
	if a.queue[0].ItemID != "head-1" || a.queue[1].ItemID != "head-2" || a.queue[2].ItemID != "tail-itm" {
		t.Fatalf("queue order = %+v, want [head-1 head-2 tail-itm]", a.queue)
	}
	if a.queue[0].QueueEntryID != 100 || a.queue[1].QueueEntryID != 101 || a.queue[2].QueueEntryID != 99 {
		t.Fatalf("QueueEntryIDs = %d,%d,%d, want 100,101,99", a.queue[0].QueueEntryID, a.queue[1].QueueEntryID, a.queue[2].QueueEntryID)
	}
	if a.queue[0].MediaSourceID != "src-next" || a.queue[1].MediaSourceID != "src-next" {
		t.Fatalf("MediaSourceID not preserved for inserted items: %+v", a.queue)
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
	if len(mgr.idleReqs) != 0 {
		t.Fatalf("manual NextTrack used StartSessionIfIdle: %d calls", len(mgr.idleReqs))
	}
	mgr.mu.Unlock()
	a.mu.Lock()
	if len(a.queue) != 0 {
		t.Errorf("queue len after NextTrack = %d, want 0", len(a.queue))
	}
	a.mu.Unlock()

	history := a.CompanionHistory()
	if len(history) != 1 {
		t.Fatalf("CompanionHistory len = %d, want 1", len(history))
	}
	if history[0].Title == "" {
		t.Fatal("history Title is empty")
	}
	if history[0].URLDisplay != "next-itm" {
		t.Fatalf("history URLDisplay = %q, want next-itm", history[0].URLDisplay)
	}
}

func TestAutoAdvanceEOF_StartsNextQueuedItemIfIdle(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := mgr.lastIdleReq().AdapterRef; got != "next-itm:ps-next-itm" {
		t.Fatalf("idle AdapterRef = %q, want next-itm:ps-next-itm", got)
	}
	if got := a.snapshotCurrentRefKey(); got != "next-itm:ps-next-itm" {
		t.Fatalf("currentRefKey = %q, want next-itm:ps-next-itm", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "tail-itm" {
		t.Fatalf("queue after auto advance = %+v, want [tail-itm]", a.queue)
	}
	if _, ok := a.reporters["next-itm:ps-next-itm"]; !ok {
		t.Fatalf("reporter for next item not registered")
	}
}

func TestAutoAdvanceEOF_GuardMissLeavesQueueAndRefUnchanged(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: false}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}

func TestAutoAdvanceEOF_PlaybackInfoFailureLeavesQueueAndRefUnchanged(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, map[string]bool{"bad-itm": true})
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "bad-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", idleCalls)
	}
	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 2 || a.queue[0].ItemID != "bad-itm" || a.queue[1].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want unchanged [bad-itm tail-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}

func TestAutoAdvanceEOF_StartSessionIfIdleErrorLeavesQueueAndRefUnchanged(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
		startIdleErr:     errors.New("probe failed"),
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 2 || a.queue[0].ItemID != "next-itm" || a.queue[1].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm tail-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}

func TestAutoAdvanceEOF_ControllerStartsDuringIdleGuardStandsDown(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	mgr.onStartIdle = func() {
		mgr.mu.Lock()
		mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "controller-itm:ps-controller"}
		mgr.mu.Unlock()
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}

func TestAutoAdvanceEOF_CommitFailureStopsAutoStartedSession(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}
	a.beforeAutoAdvanceCommit = func() {
		a.mu.Lock()
		a.currentRefKey = "controller-itm:ps-controller"
		a.mu.Unlock()
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "controller-itm:ps-controller" {
		t.Fatalf("currentRefKey = %q, want controller-owned ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "next-itm:ps-next-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}

func TestAutoAdvanceEOF_CorePreemptBeforeCommitDoesNotClaimStaleStart(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	var statusCalls int
	mgr.onStatus = func() {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		statusCalls++
		if statusCalls == 3 {
			mgr.st = core.SessionStatus{
				State:      core.StatePlaying,
				AdapterRef: "other:session",
				Generation: mgr.nextGenerationLocked(),
			}
		}
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.AdapterRef != "other:session" {
		t.Fatalf("core status = %+v, want other session preserved", status)
	}
}

func TestAutoAdvanceEOF_CorePreemptAfterCommitRollsBackAdapterState(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}
	a.afterAutoAdvanceCommit = func() {
		mgr.mu.Lock()
		mgr.st = core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "other:session",
			Generation: mgr.nextGenerationLocked(),
		}
		mgr.mu.Unlock()
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 2 || queue[0].ItemID != "next-itm" || queue[1].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want restored [next-itm tail-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.AdapterRef != "other:session" {
		t.Fatalf("core status = %+v, want other session preserved", status)
	}
}

func TestAutoAdvanceEOF_PlayNextBeforeCommitStopsStaleStart(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}
	a.nextQueueEntryID = 2
	a.beforeAutoAdvanceCommit = func() {
		a.queueAt(playMessageData{ItemIDs: []string{"inserted-itm"}, PlayCommand: "PlayNext"}, 0)
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 3 || queue[0].ItemID != "inserted-itm" || queue[1].ItemID != "next-itm" || queue[2].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want [inserted-itm next-itm tail-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "next-itm:ps-next-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}

func TestAutoAdvanceEOF_StaleCurrentRefDoesNotMutateQueue(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "new-itm:ps-new"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.advanceAfterEOF("old-itm:ps-old")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", idleCalls)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}
}

func TestAutoAdvanceEOF_DuplicateQueuedItemsInsertedAheadStopsStaleStart(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "dup-itm"}, {QueueEntryID: 2, ItemID: "dup-itm"}}
	a.nextQueueEntryID = 2
	a.beforeAutoAdvanceCommit = func() {
		a.queueAt(playMessageData{ItemIDs: []string{"dup-itm"}, PlayCommand: "PlayNext"}, 0)
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 3 {
		t.Fatalf("queue = %+v, want inserted duplicate plus original two duplicates", queue)
	}
	if queue[0].QueueEntryID != 3 || queue[1].QueueEntryID != 1 || queue[2].QueueEntryID != 2 {
		t.Fatalf("QueueEntryIDs = %d,%d,%d, want inserted 3 then originals 1,2", queue[0].QueueEntryID, queue[1].QueueEntryID, queue[2].QueueEntryID)
	}
	if queue[0].ItemID != "dup-itm" || queue[1].ItemID != "dup-itm" || queue[2].ItemID != "dup-itm" {
		t.Fatalf("queue ItemIDs = %+v, want all dup-itm", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "dup-itm:ps-dup-itm" {
		t.Fatalf("StopIfSession ref = %q, want dup-itm:ps-dup-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "dup-itm:ps-dup-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}

func TestAutoAdvanceEOF_PlayNowQueueReplacementBeforeCommitFailsCleanly(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}
	a.nextQueueEntryID = 2
	a.beforeAutoAdvanceCommit = func() {
		_ = a.replaceQueueForPlayNow(playMessageData{
			ItemIDs:     []string{"replacement-now", "replacement-tail"},
			PlayCommand: "PlayNow",
		})
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref because commit should fail", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "replacement-tail" {
		t.Fatalf("queue = %+v, want replacement tail only", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "next-itm:ps-next-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}

func TestAutoAdvanceEOF_PendingManualNextTrackBlocksAutoAdvance(t *testing.T) {
	jfSrv, manualPlaybackInfoStarted, releaseManual := startBlockingPlaybackInfoServer(t, "manual-itm")
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	t.Cleanup(func() { quiesceAdapter(a) })
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "old-itm:ps-old"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "manual-itm"}, {QueueEntryID: 2, ItemID: "auto-itm"}}

	a.HandlePlaystate(mustMarshal(t, map[string]any{"Command": "NextTrack"}))
	select {
	case <-manualPlaybackInfoStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for manual NextTrack PlaybackInfo")
	}

	a.advanceAfterEOF("old-itm:ps-old")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0 while manual NextTrack is pending", idleCalls)
	}
	if got := a.snapshotCurrentRefKey(); got != "old-itm:ps-old" {
		t.Fatalf("currentRefKey = %q, want old ref while manual NextTrack is pending", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "auto-itm" {
		t.Fatalf("queue = %+v, want remaining [auto-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	releaseManual()
	waitForFakeManagerReq(t, mgr)
}

func TestAutoAdvanceEOF_PendingPlayNowBlocksAutoAdvance(t *testing.T) {
	jfSrv, playNowPlaybackInfoStarted, releasePlayNow := startBlockingPlaybackInfoServer(t, "now-itm")
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "old-itm:ps-old"

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"now-itm", "tail-itm"},
		"PlayCommand": "PlayNow",
	}))
	select {
	case <-playNowPlaybackInfoStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PlayNow PlaybackInfo")
	}

	a.advanceAfterEOF("old-itm:ps-old")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0 while PlayNow is pending", idleCalls)
	}
	if got := a.snapshotCurrentRefKey(); got != "old-itm:ps-old" {
		t.Fatalf("currentRefKey = %q, want old ref while PlayNow is pending", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want PlayNow tail [tail-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	releasePlayNow()
	waitForFakeManagerReq(t, mgr)
}

func TestAutoAdvanceEOF_PendingSeekRestartBlocksAutoAdvance(t *testing.T) {
	jfSrv, restartPlaybackInfoStarted, releaseRestart := startBlockingPlaybackInfoServer(t, "old-itm")
	mgr := &fakeManager{
		st: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "old-itm:ps-old",
			Position:   30 * time.Second,
		},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "old-itm:ps-old"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.HandlePlaystate(mustMarshal(t, map[string]any{
		"Command":           "Seek",
		"SeekPositionTicks": int64(45 * time.Second / (100 * time.Nanosecond)),
	}))
	select {
	case <-restartPlaybackInfoStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for seek restart PlaybackInfo")
	}
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StateIdle}
	mgr.mu.Unlock()

	a.advanceAfterEOF("old-itm:ps-old")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0 while seek restart is pending", idleCalls)
	}
	if got := a.snapshotCurrentRefKey(); got != "old-itm:ps-old" {
		t.Fatalf("currentRefKey = %q, want old ref while seek restart is pending", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	releaseRestart()
	waitForFakeManagerReq(t, mgr)
}

func TestAutoAdvanceEOF_PendingTrackSwitchBlocksAutoAdvance(t *testing.T) {
	jfSrv, trackSwitchPlaybackInfoStarted, releaseTrackSwitch := startBlockingPlaybackInfoServer(t, "old-itm")
	mgr := &fakeManager{
		st: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "old-itm:ps-old",
			Position:   30 * time.Second,
		},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "old-itm:ps-old"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.HandleGeneralCommand(mustMarshal(t, map[string]any{
		"Name":      "SetAudioStreamIndex",
		"Arguments": map[string]string{"Index": "1"},
	}))
	select {
	case <-trackSwitchPlaybackInfoStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for track switch PlaybackInfo")
	}
	mgr.mu.Lock()
	mgr.st = core.SessionStatus{State: core.StateIdle}
	mgr.mu.Unlock()

	a.advanceAfterEOF("old-itm:ps-old")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0 while track switch is pending", idleCalls)
	}
	if got := a.snapshotCurrentRefKey(); got != "old-itm:ps-old" {
		t.Fatalf("currentRefKey = %q, want old ref while track switch is pending", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	releaseTrackSwitch()
	waitForFakeManagerReq(t, mgr)
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
