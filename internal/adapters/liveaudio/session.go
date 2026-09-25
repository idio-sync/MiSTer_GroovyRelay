package liveaudio

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

// Core is the slice of *core.Manager a Session drives.
type Core interface {
	StartSession(core.SessionRequest) error
	StartSessionIfSession(req core.SessionRequest, expectedRef string, generation uint64) (bool, error)
	StopIfSession(ref string, generation uint64) (bool, error)
	Status() core.SessionStatus
	UpdateNowPlayingIfSession(ref string, generation uint64, title string, display core.DisplayMetadata) bool
	VisualizerMode() string
}

// SessionConfig wires a Session.
type SessionConfig struct {
	Source   string // core Source and AdapterRef prefix: "spotify", "airplay"
	Label    string // headline when the track has no title: "SPOTIFY"
	Core     Core
	Relay    *Relay
	HTTPPort int    // bridge listener, for the relay URL
	DataDir  string // artwork cache location; "" disables artwork

	// OnExternalStop is called (on the session goroutine) when the CRT
	// session ends without the sender asking: another source preempted
	// it, it was stopped from the UI, or the pipeline failed. The adapter
	// restarts its helper so the phone sees a disconnect instead of
	// playing into the void.
	OnExternalStop func(reason string)

	HTTPClient *http.Client // artwork fetches; nil = a 10 s timeout client

	afterFunc func(time.Duration, func()) stopper // tests
}

type stopper interface{ Stop() bool }

// Options are the session settings a config save can change at runtime.
type Options struct {
	AudioOutput core.AudioOutputMode // applies from the next session
	PauseGrace  time.Duration        // 0 ends the session as soon as it pauses
}

type sessionState int

const (
	stateIdle sessionState = iota
	stateLive
	stateHeld
)

// Session runs the Idle/Live/Held state machine for one live audio
// adapter. All state lives on one goroutine; helper events, grace timers,
// core stop callbacks, and artwork fetches are all messages to it, so no
// lock is ever held across a core call.
type Session struct {
	cfg    SessionConfig
	msgs   chan any
	closed chan struct{}
	done   chan struct{}
	once   sync.Once

	optMu   sync.Mutex
	opts    Options
	lastErr string // guarded by optMu

	// Owned by the run goroutine.
	state     sessionState
	ref       string
	gen       uint64
	nextID    uint64
	textDir   string
	track     TrackMeta // text only
	artwork   string    // cached artwork for the current track
	pipeArt   string    // artwork the running pipeline was started with
	artSeq    uint64
	grace     stopper
	graceSeq  uint64
	fetchStop context.CancelFunc
}

type graceExpired struct{ seq uint64 }

type coreStopped struct {
	ref    string
	reason string
}

type artworkReady struct {
	seq  uint64
	path string
	err  error
}

type closeRequest struct{}

func NewSession(cfg SessionConfig, opts Options) *Session {
	if cfg.afterFunc == nil {
		cfg.afterFunc = func(d time.Duration, f func()) stopper { return time.AfterFunc(d, f) }
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Label == "" {
		cfg.Label = strings.ToUpper(cfg.Source)
	}
	s := &Session{
		cfg:    cfg,
		msgs:   make(chan any, 64),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
		opts:   opts,
	}
	go s.run()
	return s
}

// Handle queues a helper event. Safe from any goroutine; dropped after Close.
func (s *Session) Handle(ev Event) { s.post(ev) }

// SetOptions replaces the runtime options.
func (s *Session) SetOptions(opts Options) {
	s.optMu.Lock()
	s.opts = opts
	s.optMu.Unlock()
}

func (s *Session) options() Options {
	s.optMu.Lock()
	defer s.optMu.Unlock()
	return s.opts
}

// LastError is the most recent session start failure, "" after a
// successful start.
func (s *Session) LastError() string {
	s.optMu.Lock()
	defer s.optMu.Unlock()
	return s.lastErr
}

func (s *Session) setLastError(msg string) {
	s.optMu.Lock()
	s.lastErr = msg
	s.optMu.Unlock()
}

// Close ends the session if it is still ours and stops the goroutine.
func (s *Session) Close() {
	s.once.Do(func() {
		s.post(closeRequest{})
		close(s.closed)
	})
	<-s.done
}

func (s *Session) post(msg any) bool {
	select {
	case <-s.closed:
		return false
	default:
	}
	select {
	case s.msgs <- msg:
		return true
	case <-s.closed:
		return false
	}
}

func (s *Session) run() {
	defer close(s.done)
	for msg := range s.msgs {
		switch m := msg.(type) {
		case Event:
			s.handleEvent(m)
		case graceExpired:
			if m.seq == s.graceSeq && s.state == stateHeld {
				s.end("pause grace expired")
			}
		case coreStopped:
			if m.ref == s.ref && s.state != stateIdle {
				s.externalStop(m.reason)
			}
		case artworkReady:
			s.applyArtwork(m)
		case func(): // tests: run on the session goroutine
			m()
		case closeRequest:
			s.end("adapter stopped")
			s.dropArtwork()
			return
		}
	}
}

func (s *Session) handleEvent(ev Event) {
	switch ev.Kind {
	case EventPlay, EventResume:
		switch s.state {
		case stateIdle:
			s.start()
		case stateHeld:
			s.cancelGrace()
			s.state = stateLive
		}
	case EventPause:
		if s.state == stateLive {
			s.state = stateHeld
			s.armGrace()
		}
	case EventTrack:
		s.setTrack(ev.Track)
	case EventArtwork:
		s.setArtwork(ev.Track)
	case EventStop:
		if s.state != stateIdle {
			s.end("sender stopped")
		}
		s.track = TrackMeta{}
		s.dropArtwork()
	}
}

func (s *Session) start() {
	opts := s.options()
	dir, err := os.MkdirTemp("", "groovyrelay-"+s.cfg.Source+"-text-*")
	if err != nil {
		s.fail(fmt.Errorf("live text dir: %w", err))
		return
	}
	s.textDir = dir
	if err := ffmpeg.WriteVisualizerText(dir, s.overlayText()); err != nil {
		s.removeTextDir()
		s.fail(err)
		return
	}
	s.nextID++
	s.ref = fmt.Sprintf("%s:%d", s.cfg.Source, s.nextID)
	s.cfg.Relay.SetDiscard(false)
	req := s.request(opts.AudioOutput)
	s.pipeArt = s.artwork
	if err := s.cfg.Core.StartSession(req); err != nil {
		s.releaseArt(s.toIdle())
		s.removeTextDir()
		s.fail(fmt.Errorf("start %s session: %w", s.cfg.Source, err))
		return
	}
	s.state = stateLive
	st := s.cfg.Core.Status()
	if st.AdapterRef != s.ref {
		// Another source replaced us before we could read our generation.
		// Its OnStop("preempted") for our ref arrives after we give the ref
		// up, so handle the preemption here.
		s.externalStop("preempted")
		return
	}
	s.gen = st.Generation
	s.setLastError("")
	slog.Info("live audio session started", "source", s.cfg.Source, "ref", s.ref)
}

func (s *Session) fail(err error) {
	s.setLastError(err.Error())
	slog.Warn("live audio session", "source", s.cfg.Source, "err", err)
}

// request builds the SessionRequest for the current track, reusing s.ref.
func (s *Session) request(output core.AudioOutputMode) core.SessionRequest {
	ref := s.ref
	text := s.overlayText()
	return core.SessionRequest{
		AdapterRef:      ref,
		Source:          s.cfg.Source,
		Title:           text.Title,
		DisplayMetadata: s.display(),
		MediaKind:       core.MediaKindMusic,
		Capabilities:    core.Capabilities{CanPause: false, CanSeek: false},
		AudioCapture: core.AudioCaptureInput{
			Enabled:         true,
			Format:          PCMFormat,
			Device:          s.cfg.Relay.URL(s.cfg.HTTPPort),
			SampleRate:      SampleRate,
			Channels:        Channels,
			ThreadQueueSize: 64,
			AnalyzeDuration: 100 * time.Millisecond,
			ProbeSize:       32768,
		},
		AudioOutputMode: output,
		Visualizer: core.VisualizerRequest{
			Enabled: true,
			Metadata: core.VisualizerMetadata{
				Title:       text.Title,
				Artist:      text.Artist,
				Album:       text.Album,
				ArtworkPath: s.artwork,
			},
			LiveTextDir: s.textDir,
		},
		OnStop: func(reason string) { s.post(coreStopped{ref: ref, reason: reason}) },
	}
}

func (s *Session) overlayText() ffmpeg.VisualizerMetadata {
	title := strings.TrimSpace(s.track.Title)
	if title == "" {
		title = s.cfg.Label
	}
	return ffmpeg.VisualizerMetadata{Title: title, Artist: s.track.Artist, Album: s.track.Album}
}

func (s *Session) display() core.DisplayMetadata {
	text := s.overlayText()
	return core.DisplayMetadata{Primary: text.Title, Secondary: text.Artist, Tertiary: text.Album}
}

func (s *Session) setTrack(t TrackMeta) {
	s.track = TrackMeta{Title: t.Title, Artist: t.Artist, Album: t.Album, Duration: t.Duration}
	if s.state != stateIdle {
		if err := ffmpeg.WriteVisualizerText(s.textDir, s.overlayText()); err != nil {
			slog.Warn("live audio overlay text", "source", s.cfg.Source, "err", err)
		}
		s.cfg.Core.UpdateNowPlayingIfSession(s.ref, s.gen, s.overlayText().Title, s.display())
	}
	if t.hasArtwork() {
		s.setArtwork(t)
	}
}

// setArtwork caches t's artwork: inline bytes now, a URL asynchronously.
// Empty artwork clears it.
func (s *Session) setArtwork(t TrackMeta) {
	s.artSeq++
	seq := s.artSeq
	if s.fetchStop != nil {
		s.fetchStop()
		s.fetchStop = nil
	}
	switch {
	case s.cfg.DataDir == "":
		return
	case len(t.ArtworkBytes) > 0:
		path, err := artworkcache.StoreToCache(s.cfg.DataDir, t.ArtworkBytes)
		s.applyArtwork(artworkReady{seq: seq, path: path, err: err})
	case t.ArtworkURL != "":
		u, err := url.Parse(t.ArtworkURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			s.applyArtwork(artworkReady{seq: seq, err: fmt.Errorf("artwork URL must be https")})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		s.fetchStop = cancel
		go func() {
			defer cancel()
			path, err := artworkcache.FetchToCache(ctx, artworkcache.FetchOptions{
				DataDir: s.cfg.DataDir, URL: u.String(), Client: s.cfg.HTTPClient,
			})
			if !s.post(artworkReady{seq: seq, path: path, err: err}) {
				artworkcache.Remove(path)
			}
		}()
	default:
		s.applyArtwork(artworkReady{seq: seq}) // clear
	}
}

func (s *Session) applyArtwork(m artworkReady) {
	if m.seq != s.artSeq {
		artworkcache.Remove(m.path) // superseded
		return
	}
	if m.err != nil {
		slog.Debug("live audio artwork", "source", s.cfg.Source, "err", m.err)
		return
	}
	old := s.artwork
	s.artwork = m.path
	if old != s.pipeArt {
		artworkcache.Remove(old)
	}
	if s.state != stateIdle && s.artwork != s.pipeArt && usesArtwork(s.cfg.Core.VisualizerMode()) {
		s.restartForArtwork()
	}
}

// restartForArtwork respawns the pipeline with the new artwork. The same
// AdapterRef makes core treat it as the session continuing (no OnStop),
// and the relay keeps buffering across the gap.
func (s *Session) restartForArtwork() {
	req := s.request(s.options().AudioOutput)
	started, err := s.cfg.Core.StartSessionIfSession(req, s.ref, s.gen)
	if !started {
		return // replaced meanwhile; its OnStop is on the way
	}
	st := s.cfg.Core.Status()
	if st.AdapterRef != s.ref {
		// A failed same-ref restart ends the session without an OnStop.
		if err != nil {
			s.fail(fmt.Errorf("artwork restart: %w", err))
		}
		s.externalStop("artwork restart failed")
		return
	}
	if err != nil {
		// Rejected before the old pipeline was touched; keep it.
		slog.Warn("live audio artwork restart", "source", s.cfg.Source, "err", err)
		return
	}
	s.gen = st.Generation
	old := s.pipeArt
	s.pipeArt = s.artwork
	if old != s.artwork {
		artworkcache.Remove(old)
	}
}

func usesArtwork(mode string) bool {
	switch config.NormalizeVisualizerMode(mode) {
	case config.VisualizerModeCoverVU, config.VisualizerModeCoverSpectrum:
		return true
	}
	return false
}

func (s *Session) armGrace() {
	s.cancelGrace()
	grace := s.options().PauseGrace
	if grace <= 0 {
		s.end("paused")
		return
	}
	s.graceSeq++
	seq := s.graceSeq
	s.grace = s.cfg.afterFunc(grace, func() { s.post(graceExpired{seq: seq}) })
}

func (s *Session) cancelGrace() {
	if s.grace != nil {
		s.grace.Stop()
		s.grace = nil
	}
	s.graceSeq++
}

// end stops the core session if it is still ours and returns to idle.
func (s *Session) end(reason string) {
	if s.state == stateIdle {
		return
	}
	ref, gen := s.ref, s.gen
	pipeArt := s.toIdle() // clears s.ref first: the OnStop this triggers is ignored
	if _, err := s.cfg.Core.StopIfSession(ref, gen); err != nil {
		slog.Warn("live audio session stop", "source", s.cfg.Source, "err", err)
	}
	// After the stop, which waits for ffmpeg to exit: its files are free.
	s.removeTextDir()
	s.releaseArt(pipeArt)
	slog.Info("live audio session ended", "source", s.cfg.Source, "ref", ref, "reason", reason)
}

// externalStop handles core ending our session on its own; the pipeline
// is already gone.
func (s *Session) externalStop(reason string) {
	ref := s.ref
	s.releaseArt(s.toIdle())
	s.removeTextDir()
	slog.Info("live audio session ended externally", "source", s.cfg.Source, "ref", ref, "reason", reason)
	if s.cfg.OnExternalStop != nil {
		s.cfg.OnExternalStop(reason)
	}
}

// toIdle resets session state and returns the artwork the pipeline was
// using, which the caller releases once the pipeline is gone.
func (s *Session) toIdle() (pipeArt string) {
	s.cancelGrace()
	s.state = stateIdle
	s.ref = ""
	s.gen = 0
	s.cfg.Relay.SetDiscard(true)
	pipeArt, s.pipeArt = s.pipeArt, ""
	return pipeArt
}

// releaseArt deletes a pipeline's artwork unless it is still the current
// track's.
func (s *Session) releaseArt(path string) {
	if path != s.artwork {
		artworkcache.Remove(path)
	}
}

func (s *Session) removeTextDir() {
	if s.textDir == "" {
		return
	}
	if err := os.RemoveAll(s.textDir); err != nil {
		slog.Debug("live audio text dir cleanup", "dir", s.textDir, "err", err)
	}
	s.textDir = ""
}

func (s *Session) dropArtwork() {
	s.artSeq++ // invalidates in-flight fetches
	if s.fetchStop != nil {
		s.fetchStop()
		s.fetchStop = nil
	}
	artworkcache.Remove(s.artwork)
	s.artwork = ""
}
