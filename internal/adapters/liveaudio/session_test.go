package liveaudio

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

// fakeCore mimics the core.Manager session-key semantics the Session
// relies on, including async OnStop notifications.
type fakeCore struct {
	mu       sync.Mutex
	mode     string
	startErr error
	active   *core.SessionRequest
	gen      uint64
	starts   []core.SessionRequest
	restarts []core.SessionRequest
	stops    []string
	nowPlay  []core.DisplayMetadata
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	if f.startErr != nil {
		err := f.startErr
		f.mu.Unlock()
		return err
	}
	prev := f.active
	f.starts = append(f.starts, req)
	f.active = &req
	f.gen++
	f.mu.Unlock()
	if prev != nil && prev.AdapterRef != req.AdapterRef && prev.OnStop != nil {
		go prev.OnStop("preempted")
	}
	return nil
}

func (f *fakeCore) StartSessionIfSession(req core.SessionRequest, ref string, gen uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil || f.active.AdapterRef != ref || f.gen != gen {
		return false, nil
	}
	f.restarts = append(f.restarts, req)
	f.active = &req
	f.gen++
	return true, nil
}

func (f *fakeCore) StopIfSession(ref string, gen uint64) (bool, error) {
	f.mu.Lock()
	if f.active == nil || f.active.AdapterRef != ref || f.gen != gen {
		f.mu.Unlock()
		return false, nil
	}
	onStop := f.active.OnStop
	f.active = nil
	f.stops = append(f.stops, ref)
	f.mu.Unlock()
	if onStop != nil {
		go onStop("stopped")
	}
	return true, nil
}

func (f *fakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil {
		return core.SessionStatus{}
	}
	return core.SessionStatus{AdapterRef: f.active.AdapterRef, Generation: f.gen}
}

func (f *fakeCore) UpdateNowPlayingIfSession(ref string, gen uint64, title string, d core.DisplayMetadata) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil || f.active.AdapterRef != ref || f.gen != gen {
		return false
	}
	f.nowPlay = append(f.nowPlay, d)
	f.active.DisplayMetadata = d
	return true
}

func (f *fakeCore) VisualizerMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

// endExternally simulates another source (or the UI) ending the session.
func (f *fakeCore) endExternally(reason string) {
	f.mu.Lock()
	prev := f.active
	f.active = nil
	f.mu.Unlock()
	if prev != nil && prev.OnStop != nil {
		go prev.OnStop(reason)
	}
}

func (f *fakeCore) snapshot() (starts, restarts []core.SessionRequest, stops []string, nowPlay []core.DisplayMetadata) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.SessionRequest(nil), f.starts...), append([]core.SessionRequest(nil), f.restarts...),
		append([]string(nil), f.stops...), append([]core.DisplayMetadata(nil), f.nowPlay...)
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool { t.stopped = true; return true }

type sessionHarness struct {
	t        *testing.T
	core     *fakeCore
	relay    *Relay
	sess     *Session
	dataDir  string
	mu       sync.Mutex
	timers   []*fakeTimer
	external []string
}

func newSessionHarness(t *testing.T, opts Options) *sessionHarness {
	t.Helper()
	h := &sessionHarness{t: t, core: &fakeCore{mode: "retro_analyzer"}, dataDir: t.TempDir()}
	h.relay = newRelay("/internal/liveaudio/spotify/pcm/", "tok", newFakeClock())
	h.sess = NewSession(SessionConfig{
		Source:   "spotify",
		Label:    "SPOTIFY",
		Core:     h.core,
		Relay:    h.relay,
		HTTPPort: 32500,
		DataDir:  h.dataDir,
		OnExternalStop: func(reason string) {
			h.mu.Lock()
			h.external = append(h.external, reason)
			h.mu.Unlock()
		},
		afterFunc: func(d time.Duration, f func()) stopper {
			tm := &fakeTimer{d: d, f: f}
			h.mu.Lock()
			h.timers = append(h.timers, tm)
			h.mu.Unlock()
			return tm
		},
	}, opts)
	t.Cleanup(h.sess.Close)
	return h
}

// sync waits until the session goroutine has processed everything queued.
func (h *sessionHarness) sync() {
	h.t.Helper()
	done := make(chan struct{})
	h.sess.post(func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		h.t.Fatal("session goroutine stuck")
	}
}

// inspect runs f on the session goroutine.
func (h *sessionHarness) inspect(f func(s *Session)) {
	h.t.Helper()
	done := make(chan struct{})
	h.sess.post(func() { f(h.sess); close(done) })
	<-done
}

func (h *sessionHarness) handle(evs ...Event) {
	h.t.Helper()
	for _, ev := range evs {
		h.sess.Handle(ev)
	}
	h.sync()
}

func (h *sessionHarness) lastTimer() *fakeTimer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.timers) == 0 {
		h.t.Fatal("no timer armed")
	}
	return h.timers[len(h.timers)-1]
}

func (h *sessionHarness) externalStops() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.external...)
}

func (h *sessionHarness) discarding() bool {
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	return h.relay.discard
}

// eventually polls cond, syncing the session between polls (async OnStop
// callbacks and artwork fetches arrive as messages).
func (h *sessionHarness) eventually(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.sync()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readText(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func pngBytes(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var track1 = TrackMeta{Title: "Blue Monday", Artist: "New Order", Album: "Power, Corruption & Lies"}

func TestSessionPlayStartsLiveVisualizerSession(t *testing.T) {
	h := newSessionHarness(t, Options{AudioOutput: core.AudioOutputMonitor, PauseGrace: 30 * time.Second})
	h.handle(Event{Kind: EventPlay})

	starts, _, _, _ := h.core.snapshot()
	if len(starts) != 1 {
		t.Fatalf("starts = %d, want 1", len(starts))
	}
	req := starts[0]
	if req.AdapterRef != "spotify:1" || req.Source != "spotify" || req.MediaKind != core.MediaKindMusic {
		t.Fatalf("request identity = %q %q %q", req.AdapterRef, req.Source, req.MediaKind)
	}
	if req.Capabilities.CanPause || req.Capabilities.CanSeek {
		t.Fatal("live session must not advertise pause/seek")
	}
	c := req.AudioCapture
	if !c.Enabled || c.Format != "s16le" || c.SampleRate != 44100 || c.Channels != 2 ||
		c.Device != "http://127.0.0.1:32500/internal/liveaudio/spotify/pcm/tok" {
		t.Fatalf("capture = %+v", c)
	}
	if req.AudioOutputMode != core.AudioOutputMonitor {
		t.Fatalf("audio output = %q, want monitor", req.AudioOutputMode)
	}
	if !req.Visualizer.Enabled || req.Visualizer.LiveTextDir == "" {
		t.Fatalf("visualizer = %+v", req.Visualizer)
	}
	if got := readText(t, req.Visualizer.LiveTextDir, ffmpeg.VisualizerTitleFile); got != "SPOTIFY" {
		t.Fatalf("title before any track = %q, want the SPOTIFY label", got)
	}
	if req.DisplayMetadata.Primary != "SPOTIFY" {
		t.Fatalf("display = %+v", req.DisplayMetadata)
	}
	if h.discarding() {
		t.Fatal("relay still discarding during a live session")
	}
	h.handle(Event{Kind: EventPlay}) // ignored while live
	if starts, _, _, _ := h.core.snapshot(); len(starts) != 1 {
		t.Fatalf("second Play started another session (%d starts)", len(starts))
	}
}

func TestSessionTrackUpdatesTextAndVFDWithoutRestart(t *testing.T) {
	h := newSessionHarness(t, Options{PauseGrace: time.Minute})
	h.handle(Event{Kind: EventPlay}, Event{Kind: EventTrack, Track: track1})

	starts, restarts, _, nowPlay := h.core.snapshot()
	dir := starts[0].Visualizer.LiveTextDir
	if got := readText(t, dir, ffmpeg.VisualizerTitleFile); got != "BLUE MONDAY" {
		t.Fatalf("title = %q", got)
	}
	if got := readText(t, dir, ffmpeg.VisualizerAlbumFile); got != "POWER, CORRUPTION & LIES" {
		t.Fatalf("album = %q", got)
	}
	want := core.DisplayMetadata{Primary: "Blue Monday", Secondary: "New Order", Tertiary: "Power, Corruption & Lies"}
	if len(nowPlay) != 1 || nowPlay[0] != want {
		t.Fatalf("now playing updates = %+v, want [%+v]", nowPlay, want)
	}
	if len(restarts) != 0 {
		t.Fatalf("text change restarted the pipeline %d time(s)", len(restarts))
	}
}

func TestSessionTrackBeforePlayShapesFirstRequest(t *testing.T) {
	h := newSessionHarness(t, Options{})
	art := track1
	art.ArtworkBytes = pngBytes(t, color.RGBA{R: 255, A: 255})
	h.handle(Event{Kind: EventTrack, Track: art}, Event{Kind: EventPlay})

	starts, _, _, _ := h.core.snapshot()
	req := starts[0]
	if req.Title != "Blue Monday" || req.Visualizer.Metadata.Artist != "New Order" {
		t.Fatalf("first request text = %q / %+v", req.Title, req.Visualizer.Metadata)
	}
	if req.Visualizer.Metadata.ArtworkPath == "" {
		t.Fatal("first request carries no artwork")
	}
	if _, err := os.Stat(req.Visualizer.Metadata.ArtworkPath); err != nil {
		t.Fatalf("artwork file: %v", err)
	}
}

func TestSessionPauseHoldsThenEndsAfterGrace(t *testing.T) {
	h := newSessionHarness(t, Options{PauseGrace: 30 * time.Second})
	h.handle(Event{Kind: EventPlay}, Event{Kind: EventPause})

	tm := h.lastTimer()
	if tm.d != 30*time.Second {
		t.Fatalf("grace = %v, want 30s", tm.d)
	}
	if _, _, stops, _ := h.core.snapshot(); len(stops) != 0 {
		t.Fatal("pause ended the session before the grace window")
	}
	starts, _, _, _ := h.core.snapshot()
	dir := starts[0].Visualizer.LiveTextDir

	tm.f()
	h.eventually("grace stop", func() bool {
		_, _, stops, _ := h.core.snapshot()
		return len(stops) == 1
	})
	if !h.discarding() {
		t.Fatal("relay not discarding after the session ended")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("text dir still present after the session ended: %v", err)
	}
	if got := h.externalStops(); len(got) != 0 {
		t.Fatalf("our own stop reported as external: %v", got)
	}
}

func TestSessionResumeInsideGraceKeepsSession(t *testing.T) {
	h := newSessionHarness(t, Options{PauseGrace: 30 * time.Second})
	h.handle(Event{Kind: EventPlay}, Event{Kind: EventPause})
	stale := h.lastTimer()
	h.handle(Event{Kind: EventResume})
	if !stale.stopped {
		t.Fatal("resume did not cancel the grace timer")
	}
	stale.f() // a timer that fired anyway must be ignored
	h.sync()
	starts, _, stops, _ := h.core.snapshot()
	if len(stops) != 0 || len(starts) != 1 {
		t.Fatalf("resume: starts=%d stops=%d, want the same session kept", len(starts), len(stops))
	}
	h.handle(Event{Kind: EventPause}, Event{Kind: EventPlay}) // Play resumes a held session too
	if starts, _, stops, _ := h.core.snapshot(); len(starts) != 1 || len(stops) != 0 {
		t.Fatal("Play while held did not resume the same session")
	}
}

func TestSessionZeroGraceEndsOnPause(t *testing.T) {
	h := newSessionHarness(t, Options{PauseGrace: 0})
	h.handle(Event{Kind: EventPlay}, Event{Kind: EventPause})
	h.eventually("stop", func() bool {
		_, _, stops, _ := h.core.snapshot()
		return len(stops) == 1
	})
}

func TestSessionStopEventEndsSession(t *testing.T) {
	h := newSessionHarness(t, Options{PauseGrace: time.Minute})
	h.handle(Event{Kind: EventPlay}, Event{Kind: EventTrack, Track: track1}, Event{Kind: EventStop})
	if _, _, stops, _ := h.core.snapshot(); len(stops) != 1 || stops[0] != "spotify:1" {
		t.Fatalf("stops = %v, want [spotify:1]", stops)
	}
	h.handle(Event{Kind: EventPlay})
	starts, _, _, _ := h.core.snapshot()
	if len(starts) != 2 || starts[1].AdapterRef != "spotify:2" || starts[1].Title != "SPOTIFY" {
		t.Fatalf("next session = %q %q, want spotify:2 with the track cleared", starts[1].AdapterRef, starts[1].Title)
	}
	if got := h.externalStops(); len(got) != 0 {
		t.Fatalf("our own stop reported as external: %v", got)
	}
}

func TestSessionExternalEndRestartsHelper(t *testing.T) {
	h := newSessionHarness(t, Options{PauseGrace: time.Minute})
	h.handle(Event{Kind: EventPlay})
	h.core.endExternally("preempted")
	h.eventually("external stop", func() bool { return len(h.externalStops()) == 1 })
	if got := h.externalStops()[0]; got != "preempted" {
		t.Fatalf("external stop reason = %q", got)
	}
	if !h.discarding() {
		t.Fatal("relay not discarding after preemption")
	}
	h.handle(Event{Kind: EventPlay})
	if starts, _, _, _ := h.core.snapshot(); len(starts) != 2 || starts[1].AdapterRef != "spotify:2" {
		t.Fatal("Play after preemption did not start a fresh session")
	}
}

func TestSessionArtworkChangeRestartsOnlyInCoverModes(t *testing.T) {
	h := newSessionHarness(t, Options{PauseGrace: time.Minute})
	first := track1
	first.ArtworkBytes = pngBytes(t, color.RGBA{R: 255, A: 255})
	h.handle(Event{Kind: EventTrack, Track: first}, Event{Kind: EventPlay})
	starts, _, _, _ := h.core.snapshot()
	firstArt := starts[0].Visualizer.Metadata.ArtworkPath

	// Non-cover mode: artwork changes are kept for later, no restart.
	h.handle(Event{Kind: EventArtwork, Track: TrackMeta{ArtworkBytes: pngBytes(t, color.RGBA{G: 255, A: 255})}})
	if _, restarts, _, _ := h.core.snapshot(); len(restarts) != 0 {
		t.Fatal("artwork change restarted a non-cover pipeline")
	}
	if _, err := os.Stat(firstArt); err != nil {
		t.Fatalf("artwork still used by the running pipeline was deleted: %v", err)
	}

	h.core.mu.Lock()
	h.core.mode = "cover_vu"
	h.core.mu.Unlock()
	h.handle(Event{Kind: EventArtwork, Track: TrackMeta{ArtworkBytes: pngBytes(t, color.RGBA{B: 255, A: 255})}})
	_, restarts, _, _ := h.core.snapshot()
	if len(restarts) != 1 {
		t.Fatalf("restarts = %d, want 1", len(restarts))
	}
	newArt := restarts[0].Visualizer.Metadata.ArtworkPath
	if restarts[0].AdapterRef != "spotify:1" || newArt == "" || newArt == firstArt {
		t.Fatalf("restart request = %q art %q", restarts[0].AdapterRef, newArt)
	}
	if _, err := os.Stat(firstArt); !os.IsNotExist(err) {
		t.Fatalf("replaced pipeline artwork not removed: %v", err)
	}

	// The restart moved the session to a new generation: track updates
	// must target it.
	h.handle(Event{Kind: EventTrack, Track: TrackMeta{Title: "Next"}})
	if _, _, _, nowPlay := h.core.snapshot(); len(nowPlay) != 1 || nowPlay[0].Primary != "Next" {
		t.Fatalf("now playing after restart = %+v", nowPlay)
	}
	h.handle(Event{Kind: EventStop})
	if _, err := os.Stat(newArt); !os.IsNotExist(err) {
		t.Fatalf("artwork not removed after the session ended: %v", err)
	}
}

func TestSessionStartFailureRecordsError(t *testing.T) {
	h := newSessionHarness(t, Options{})
	h.core.startErr = errors.New("ffmpeg missing")
	h.handle(Event{Kind: EventPlay})
	if got := h.sess.LastError(); !strings.Contains(got, "ffmpeg missing") {
		t.Fatalf("LastError = %q", got)
	}
	if !h.discarding() {
		t.Fatal("relay left accepting audio after a failed start")
	}
	var dir string
	h.inspect(func(s *Session) { dir = s.textDir })
	if dir != "" {
		t.Fatalf("text dir %q kept after a failed start", dir)
	}
	h.core.startErr = nil
	h.handle(Event{Kind: EventPlay})
	if got := h.sess.LastError(); got != "" {
		t.Fatalf("LastError after a successful start = %q", got)
	}
}

func TestSessionCloseEndsLiveSession(t *testing.T) {
	h := newSessionHarness(t, Options{})
	h.handle(Event{Kind: EventPlay})
	h.sess.Close()
	if _, _, stops, _ := h.core.snapshot(); len(stops) != 1 {
		t.Fatalf("Close did not stop the session (stops=%v)", stops)
	}
	h.sess.Handle(Event{Kind: EventPlay}) // dropped, must not block or panic
}

func TestSessionFetchesHTTPSArtwork(t *testing.T) {
	art := pngBytes(t, color.RGBA{R: 200, G: 100, A: 255})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(art)
	}))
	defer srv.Close()

	h := newSessionHarness(t, Options{PauseGrace: time.Minute})
	h.sess.cfg.HTTPClient = srv.Client()
	h.core.mode = "cover_spectrum"
	h.handle(Event{Kind: EventPlay})
	withURL := track1
	withURL.ArtworkURL = srv.URL + "/cover.jpg"
	h.handle(Event{Kind: EventTrack, Track: withURL})
	h.eventually("artwork restart", func() bool {
		_, restarts, _, _ := h.core.snapshot()
		return len(restarts) == 1
	})

	// Plain http is refused: no fetch, no restart.
	insecure := track1
	insecure.ArtworkURL = strings.Replace(srv.URL, "https://", "http://", 1) + "/cover.jpg"
	h.handle(Event{Kind: EventTrack, Track: insecure})
	time.Sleep(50 * time.Millisecond)
	h.sync()
	if _, restarts, _, _ := h.core.snapshot(); len(restarts) != 1 {
		t.Fatalf("http artwork URL caused a restart (%d)", len(restarts))
	}
}
