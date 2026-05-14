// Package core is the adapter-agnostic control-plane root. It owns the
// session state machine and the FFmpeg → Groovy data-plane lifecycle.
// Adapters (Plex, URL-input, Jellyfin, ...) live under internal/adapters/
// and translate protocol-specific requests into core.SessionRequest before
// calling Manager.StartSession. Per spec §4.5, core imports no adapter
// package and no SourceAdapter interface is defined in v1/v2.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// removeSubtitleFile deletes the file at path if path is non-empty.
// Errors (including "file not found") are logged at debug and otherwise
// ignored — the bridge cannot block session teardown on subtitle-file
// cleanup, and a missing file just means a parallel cleanup already ran.
func removeSubtitleFile(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Debug("subtitle file cleanup", "path", path, "err", err)
	}
}

func notifySessionStop(fn func(string), reason string) {
	if fn == nil {
		return
	}
	go fn(reason)
}

// redactURL returns rawURL with any auth-token query parameters replaced by
// "REDACTED". Adapters thread credentials through stream URLs via well-known
// param names: Jellyfin uses api_key=, Plex uses X-Plex-Token=, and a few
// other servers honour a generic token=. This helper lets log sites write
// StreamURL without leaking the secret to operator logs.
//
// Returns rawURL unchanged if it is empty, not parseable, or carries no
// known token param. The redaction is case-insensitive.
func redactURL(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	changed := false
	for k := range q {
		switch strings.ToLower(k) {
		case "api_key", "x-plex-token", "token":
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if !changed {
		return rawURL
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Manager is the adapter-agnostic session orchestrator. One Manager per
// process; all adapters share it. Thread-safe.
type Manager struct {
	bridge config.BridgeConfig
	sender *groovynet.Sender
	fsm    *StateMachine

	ffmpegResolver  BinaryResolver
	ffprobeResolver BinaryResolver

	mu             sync.Mutex
	cancelFn       context.CancelFunc
	plane          planeRunner // nil when idle
	active         *activeSession
	nextGeneration uint64

	eventLog *eventlog.Log // nilable; nil disables event emission
}

type planeRunner interface {
	Run(context.Context) error
	Done() <-chan struct{}
	Position() time.Duration
	SetFieldOrder(string) error
	BlitsTotal() uint64
	FramesTotal() uint64
	Underruns() uint64
	WireBytes() uint64
	LastACKAge() time.Duration
}

var newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
	return dataplane.NewPlane(cfg)
}

// BinaryResolver is the manager's narrow view of an external binary resolver.
type BinaryResolver interface {
	Resolve() (string, error)
}

// ManagerOption customizes Manager construction without forcing tests to wire
// production resolver objects.
type ManagerOption func(*Manager)

// WithBinaryResolvers wires ffmpeg/ffprobe resolution for native sidecars.
func WithBinaryResolvers(ffmpegResolver, ffprobeResolver BinaryResolver) ManagerOption {
	return func(m *Manager) {
		m.ffmpegResolver = ffmpegResolver
		m.ffprobeResolver = ffprobeResolver
	}
}

// WithEventLog wires the event-log ring buffer for cast lifecycle
// emissions. Nil disables emission; tests that don't care about
// events can omit this option.
func WithEventLog(log *eventlog.Log) ManagerOption {
	return func(m *Manager) {
		m.eventLog = log
	}
}

// emit appends to the eventlog if one is wired. Safe to call when
// eventLog is nil.
func (m *Manager) emit(sev eventlog.Severity, msg string) {
	if m.eventLog == nil {
		return
	}
	m.eventLog.Append(eventlog.Entry{
		Time:     time.Now(),
		Severity: sev,
		Source:   "core",
		Message:  msg,
	})
}

// makeOnInitCallback constructs the dataplane.PlaneConfig.OnInit closure
// for a new session, capturing adapterRef and modelineName at call time
// (i.e. while m.mu is held in startPlaneLocked). The returned closure
// fires from the Plane.Run goroutine without holding m.mu, so it must NOT
// read any m.bridge fields at fire time — that would race with concurrent
// UpdateBridge calls. See PR2 code review concern #1.
func (m *Manager) makeOnInitCallback(adapterRef, modelineName string) func(error) {
	return func(err error) {
		if err != nil {
			m.emit(eventlog.SeverityErr, fmt.Sprintf("init-failed: %v", err))
			return
		}
		m.emit(eventlog.SeverityInfo, fmt.Sprintf("cast-started %s · %s",
			adapterRef, modelineName))
	}
}

// activeSession is the manager's private per-session context. Adapter-
// specific state (subscribers, media keys, etc.) stays in the adapter.
type activeSession struct {
	req            SessionRequest
	startedAt      time.Time
	generation     uint64
	baseOffsetMs   int           // offset the plane was spawned with
	pausedPosition time.Duration // snapshot from plane at Pause
	duration       time.Duration
}

var errAdapterRefChanged = errors.New("adapter ref changed")

type sessionGuard struct {
	ref            string
	generation     uint64
	fullSessionKey bool
}

func adapterRefGuard(ref string) sessionGuard {
	return sessionGuard{ref: ref}
}

func fullSessionGuard(ref string, generation uint64) sessionGuard {
	return sessionGuard{ref: ref, generation: generation, fullSessionKey: true}
}

func (g sessionGuard) enabled() bool {
	return g.ref != "" || g.fullSessionKey
}

func (m *Manager) allocateGenerationLocked() uint64 {
	m.nextGeneration++
	if m.nextGeneration == 0 {
		m.nextGeneration = 1
	}
	return m.nextGeneration
}

func (m *Manager) sessionMatchesLocked(ref string, generation uint64) bool {
	return ref != "" &&
		generation != 0 &&
		m.active != nil &&
		m.active.req.AdapterRef == ref &&
		m.active.generation == generation
}

func (m *Manager) sessionGuardMatchesLocked(g sessionGuard) bool {
	if !g.enabled() {
		return true
	}
	if g.fullSessionKey {
		return m.sessionMatchesLocked(g.ref, g.generation)
	}
	return g.ref != "" && m.active != nil && m.active.req.AdapterRef == g.ref
}

func (m *Manager) startGuardMatchesLocked(guard sessionGuard, requireIdle bool) bool {
	if requireIdle {
		return m.active == nil
	}
	return m.sessionGuardMatchesLocked(guard)
}

// NewManager constructs a Manager. The Sender must already be bound to the
// MiSTer's address; Manager does not own its lifecycle (the sender is shared
// across the process lifetime so its source UDP port remains stable).
func NewManager(bridge config.BridgeConfig, sender *groovynet.Sender, opts ...ManagerOption) *Manager {
	m := &Manager{bridge: bridge, sender: sender, fsm: New()}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func resolveBinary(r BinaryResolver, fallback string) (string, error) {
	if r == nil {
		return fallback, nil
	}
	return r.Resolve()
}

func (m *Manager) logPlaneExit(runErr error) {
	if runErr == nil || errors.Is(runErr, context.Canceled) {
		return
	}
	if groovynet.IsInitACKTimeout(runErr) {
		slog.Warn(
			"MiSTer did not acknowledge INIT; it may be powered off, unreachable, or not listening on the configured port",
			"mister_host", m.bridge.MiSTer.Host,
			"mister_port", m.bridge.MiSTer.Port,
			"source_port", m.sender.SourcePort(),
			"err", runErr,
		)
		return
	}
	slog.Warn("data plane exited", "err", runErr)
}

func (m *Manager) handlePlaneExit(plane planeRunner, runErr error) {
	m.logPlaneExit(runErr)

	var onStop func(string)
	var reason string
	var subtitlePath string

	m.mu.Lock()
	if m.plane != plane {
		m.mu.Unlock()
		return
	}
	m.plane = nil
	switch {
	case runErr == nil:
		_ = m.fsm.Transition(EvEOF)
		if m.active != nil {
			reason = "eof"
			onStop = m.active.req.OnStop
			subtitlePath = m.active.req.SubtitlePath
			m.active = nil
			m.cancelFn = nil
		}
	case errors.Is(runErr, context.Canceled):
		// Pause/Stop/preempt intentionally cancel the plane. Preserve
		// session ownership here; the caller that canceled owns final cleanup.
	default:
		_ = m.fsm.Transition(EvError)
		if m.active != nil {
			reason = "error"
			onStop = m.active.req.OnStop
			subtitlePath = m.active.req.SubtitlePath
			m.active = nil
			m.cancelFn = nil
		}
	}
	m.mu.Unlock()

	removeSubtitleFile(subtitlePath)
	if reason != "" {
		notifySessionStop(onStop, reason)
	}
}

// probeFn / probeCropFn are the package-private indirections through
// which probeForStart calls into the ffmpeg package. Production sets them
// to ffmpeg.Probe / ffmpeg.ProbeCrop; tests swap them with stubs that
// capture the arguments passed in (specifically the MediaInputPolicy)
// without actually spawning ffprobe/ffmpeg. Mirrors the spawnProcess
// pattern in internal/dataplane/plane.go.
var (
	probeFn = func(ctx context.Context, ffprobePath, url string, policy ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return ffmpeg.Probe(ctx, ffprobePath, url, policy)
	}
	probeCropFn = func(ctx context.Context, ffmpegPath, inputURL string, headers map[string]string, duration time.Duration, policy ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return ffmpeg.ProbeCrop(ctx, ffmpegPath, inputURL, headers, duration, policy)
	}
)

func probeErrorIsLikelyUnreachable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection refused",
		"connection reset",
		"connection timed out",
		"could not resolve host",
		"dial tcp",
		"failed to resolve",
		"host not found",
		"http 4",
		"http 5",
		"http error 4",
		"http error 5",
		"i/o timeout",
		"network is unreachable",
		"no route to host",
		"no such host",
		"not found",
		"server returned 4",
		"server returned 5",
		"temporary failure in name resolution",
		"timed out",
		"tls handshake timeout",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func validateVisualizerRequest(req SessionRequest) error {
	if !req.Visualizer.Enabled {
		return nil
	}
	if NormalizeMediaKind(req.MediaKind) != MediaKindMusic {
		return fmt.Errorf("visualizer requires music media kind")
	}
	if req.Visualizer.Mode != VisualizerModeRetroAnalyzer {
		return fmt.Errorf("unsupported visualizer mode %q", req.Visualizer.Mode)
	}
	return nil
}

func visualizerDuration(req SessionRequest, probe *ffmpeg.ProbeResult) time.Duration {
	if req.Visualizer.Enabled && req.Visualizer.Metadata.Duration > 0 {
		return req.Visualizer.Metadata.Duration
	}
	return probeDuration(probe)
}

func ffmpegVisualizerSpec(v VisualizerRequest) ffmpeg.VisualizerSpec {
	if !v.Enabled {
		return ffmpeg.VisualizerSpec{}
	}
	return ffmpeg.VisualizerSpec{
		Enabled: true,
		Mode:    ffmpeg.VisualizerModeRetroAnalyzer,
		Metadata: ffmpeg.VisualizerMetadata{
			Title:    v.Metadata.Title,
			Artist:   v.Metadata.Artist,
			Album:    v.Metadata.Album,
			Duration: v.Metadata.Duration,
		},
	}
}

// probeForStart runs Probe and (conditionally) ProbeCrop with a bounded
// context so a stuck PMS cannot deadlock the control plane. Called by
// StartSession/Play/SeekTo BEFORE acquiring Manager.mu so the mutex is
// never held during network I/O.
func (m *Manager) probeForStart(req SessionRequest) (*ffmpeg.ProbeResult, *ffmpeg.CropRect, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ffmpegPath, err := resolveBinary(m.ffmpegResolver, "ffmpeg")
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve ffmpeg: %w", err)
	}
	ffprobePath, err := resolveBinary(m.ffprobeResolver, "ffprobe")
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve ffprobe: %w", err)
	}
	probe, err := probeFn(ctx, ffprobePath, req.StreamURL, req.MediaInputPolicy)
	if err != nil {
		wrapped := fmt.Errorf("probe source: %w", err)
		if probeErrorIsLikelyUnreachable(err) {
			// Join the underlying ffprobe error with ErrProbeUnreachable
			// only for likely source reachability failures. Parse /
			// invalid-data failures remain generic probe errors so DLNA
			// maps them to 501 instead of over-reporting Resource not found.
			return nil, nil, "", errors.Join(wrapped, ErrProbeUnreachable)
		}
		return nil, nil, "", wrapped
	}
	if req.Visualizer.Enabled {
		if probe == nil || probe.AudioRate <= 0 {
			return nil, nil, "", fmt.Errorf("visualizer source has no audio")
		}
		return probe, nil, ffmpegPath, nil
	}
	var cropRect *ffmpeg.CropRect
	if m.bridge.Video.AspectMode == "auto" {
		// ProbeCrop failures degrade gracefully to letterbox — ignore the error.
		// Filter headers through the policy's BlockedHeaders before they reach
		// ffmpeg so a forgotten "Referer" / "Cookie" can't leak via the crop
		// probe (spec line 115 mandates this happens at the core/FFmpeg
		// boundary so adapters stay naive).
		filteredHeaders := req.MediaInputPolicy.FilterHeaders(req.InputHeaders)
		cropRect, _ = probeCropFn(ctx, ffmpegPath, req.StreamURL, filteredHeaders, 2*time.Second, req.MediaInputPolicy)
	}
	return probe, cropRect, ffmpegPath, nil
}

// startPlaneLocked spawns a new data plane. Caller MUST hold m.mu AND have
// already run Probe/ProbeCrop (passed in as probe + cropRect) — this
// function must not perform network I/O while the mutex is held.
func (m *Manager) startPlaneLocked(req SessionRequest, offsetMs int,
	probe *ffmpeg.ProbeResult, cropRect *ffmpeg.CropRect, ffmpegPath string,
	generation uint64, guard sessionGuard, requireIdle bool) error {
	// 1. Preempt and await prior plane. Drop the lock while awaiting Done()
	//    so the plane's exit goroutine (which re-acquires m.mu to clear
	//    m.plane) is free to run.
	//
	//    On preempt, clean up the previous session's subtitle file IF the
	//    incoming request brought a DIFFERENT path (or no path). Play and
	//    SeekTo both pass the same req as m.active, so their SubtitlePath
	//    will match and cleanup is correctly skipped — the resumed plane
	//    still needs the file.
	var oldSubtitle string
	var oldOnStop func(string)
	var oldRef string
	if m.active != nil && m.active.req.SubtitlePath != req.SubtitlePath {
		oldSubtitle = m.active.req.SubtitlePath
	}
	if m.active != nil {
		oldOnStop = m.active.req.OnStop
		oldRef = m.active.req.AdapterRef
	}
	// genuinePreempt is true only when this StartSession actually replaces
	// a different adapter session (different AdapterRef), NOT when Play
	// (resume) or SeekTo replays the same SessionRequest into
	// startPlaneLocked. Same-session replay carries the same OnStop
	// closure and AdapterRef forward to the new activeSession, so firing
	// "preempted" notifications here would clear the adapter's ref while
	// core.Manager retains the session — see the DLNA Pause→Resume
	// regression where DLNA's onStopForRef sees its own ref → clears
	// currentRef → next SOAP action fails despite core having a healthy
	// active session.
	//
	// Architectural assumption: a new SessionRequest with the same
	// AdapterRef as the active session means "same session continues"
	// (DLNA Pause→Resume, DLNA live-edge reconnect, DLNA Seek). Adapters
	// whose protocol mints fresh server-side resources per seek MUST
	// vary AdapterRef across those boundaries to opt back into the
	// preempt path; otherwise OnStop will not fire on seek.
	//
	// Plex caveat (FIXME plex-orphan-transcode): Plex's companion adapter
	// currently sets AdapterRef = MediaKey and mints a fresh
	// TranscodeSessionID on every SeekTo. Under this gate, Plex seek
	// leaves an orphan TranscodeSessionID at PMS until PMS's own idle
	// timeout reaps it (~60–120 s). Migrating Plex's AdapterRef to
	// include TranscodeSessionID (e.g. MediaKey + ":" + TranscodeSessionID)
	// would restore explicit StopTranscodeSession on seek. Tracked
	// separately from the Phase 3 DLNA scope; PMS's reaper makes the
	// orphan benign at the controller-side and only observable in PMS
	// dashboards.
	genuinePreempt := oldRef != "" && oldRef != req.AdapterRef
	if m.cancelFn != nil {
		// The slog line is kept unconditional: it logs at debug volume
		// for operators tracing a replay path. For same-session replay
		// this is still technically a preempt of the prior PLANE
		// goroutine (it gets torn down and replaced), even though the
		// SESSION continues. The eventlog "cast-preempted" entry below
		// is operator-visible state and must match the OnStop
		// notification, so it is gated on genuinePreempt.
		slog.Info("preempting prior session for new request", "new_url", redactURL(req.StreamURL))
		if genuinePreempt {
			m.emit(eventlog.SeverityInfo, fmt.Sprintf("cast-preempted %s", oldRef))
		}
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
			if !m.startGuardMatchesLocked(guard, requireIdle) {
				return errAdapterRefChanged
			}
		}
	}
	removeSubtitleFile(oldSubtitle)
	if genuinePreempt {
		notifySessionStop(oldOnStop, "preempted")
	}

	// Resolve the modeline preset from config (empty defaults to NTSC_480i).
	// Wrap with ErrPlaneError so adapters that fault-map can categorize
	// these as plane-setup failures rather than network-unreachable. In
	// practice these only fire on bridge config corruption — operator-
	// visible — so 501 (generic failure) is the right SOAP fault.
	preset, err := ResolvePreset(m.bridge.Video.Modeline)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPlaneError, err)
	}
	modeline := preset.Modeline
	rgbMode, err := resolveRGBMode(m.bridge.Video.RGBMode)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPlaneError, err)
	}

	// Groovy SWITCHRES carries full-frame vActive even for interlaced modes;
	// the sender transmits one field at a time, so fieldH is half-height there.
	fieldH := modeline.FieldHeight()
	bpp := bytesPerPixel(rgbMode)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	// Filter input headers through the policy's BlockedHeaders before they
	// reach FFmpeg. The filter belongs at this boundary (spec line 115) so
	// adapters can keep building InputHeaders / AudioInputHeaders naively
	// — they don't need to remember which header names are unsafe under a
	// constrained-input policy.
	inputHeaders := req.MediaInputPolicy.FilterHeaders(req.InputHeaders)
	audioInputHeaders := req.MediaInputPolicy.FilterHeaders(req.AudioInputHeaders)

	spec := ffmpeg.PipelineSpec{
		InputURL:          req.StreamURL,
		InputHeaders:      inputHeaders,
		AudioInputURL:     req.AudioStreamURL,
		AudioInputHeaders: audioInputHeaders,
		SeekSeconds:       float64(offsetMs) / 1000.0,
		UseSSSeek:         req.DirectPlay,
		SourceProbe:       probe,
		OutputWidth:       int(modeline.HActive),
		OutputHeight:      int(modeline.VActive),
		FieldOrder:        m.bridge.Video.InterlaceFieldOrder,
		OutputFpsExpr:     preset.FpsExpr,
		AspectMode:        m.bridge.Video.AspectMode,
		CropRect:          cropRect,
		Visualizer:        ffmpegVisualizerSpec(req.Visualizer),
		SubtitleURL:       req.SubtitleURL,
		SubtitlePath:      req.SubtitlePath,
		SubtitleIndex:     req.SubtitleIndex,
		AudioSampleRate:   m.bridge.Audio.SampleRate,
		AudioChannels:     m.bridge.Audio.Channels,
		FFmpegPath:        ffmpegPath,
		Policy:            req.MediaInputPolicy,
	}

	plane := newPlane(dataplane.PlaneConfig{
		Sender:          m.sender,
		SpawnSpec:       spec,
		Modeline:        modeline,
		FieldWidth:      int(modeline.HActive),
		FieldHeight:     fieldH,
		BytesPerPixel:   bpp,
		RGBMode:         rgbMode,
		LZ4Enabled:      m.bridge.Video.LZ4Enabled,
		DeltaLZ4Enabled: m.bridge.Video.DeltaLZ4Enabled,
		AudioRate:       m.bridge.Audio.SampleRate,
		AudioChans:      m.bridge.Audio.Channels,
		SeekOffsetMs:    offsetMs,
		OnInit:          m.makeOnInitCallback(req.AdapterRef, m.bridge.Video.Modeline),
	})
	m.plane = plane
	m.active = &activeSession{
		req:          req,
		startedAt:    time.Now(),
		generation:   generation,
		baseOffsetMs: offsetMs,
		duration:     visualizerDuration(req, probe),
	}

	go func() {
		runErr := plane.Run(ctx)
		m.handlePlaneExit(plane, runErr)
	}()
	return nil
}

// StartSession is the adapter-agnostic entry point. Adapters translate their
// protocol-specific requests into a SessionRequest and call this. Any
// existing session is preempted and the prior goroutine awaited.
func (m *Manager) StartSession(req SessionRequest) error {
	if err := validateVisualizerRequest(req); err != nil {
		return err
	}
	probe, cropRect, ffmpegPath, err := m.probeForStart(req)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	generation := m.allocateGenerationLocked()
	if err := m.startPlaneLocked(req, req.SeekOffsetMs, probe, cropRect, ffmpegPath, generation, sessionGuard{}, false); err != nil {
		return err
	}
	return m.fsm.Transition(EvPlayMedia)
}

// StartSessionIfAdapterRef starts req only when the active session still
// matches expectedRef. expectedRef == "" means "there must be no active
// session"; a non-empty expectedRef means "replace/replay this same adapter
// session only."
func (m *Manager) StartSessionIfAdapterRef(req SessionRequest, expectedRef string) (bool, error) {
	if expectedRef == "" {
		return m.startSessionIfSessionGuard(req, sessionGuard{}, true)
	}
	return m.startSessionIfSessionGuard(req, adapterRefGuard(expectedRef), false)
}

// StartSessionIfSession starts req only when both AdapterRef and Generation
// still match the caller's full session key.
func (m *Manager) StartSessionIfSession(req SessionRequest, expectedRef string, generation uint64) (bool, error) {
	if expectedRef == "" || generation == 0 {
		return false, nil
	}
	return m.startSessionIfSessionGuard(req, fullSessionGuard(expectedRef, generation), false)
}

// StartSessionIfIdle starts req only when no session is active.
func (m *Manager) StartSessionIfIdle(req SessionRequest) (bool, error) {
	return m.startSessionIfSessionGuard(req, sessionGuard{}, true)
}

func (m *Manager) startSessionIfSessionGuard(req SessionRequest, guard sessionGuard, requireIdle bool) (bool, error) {
	m.mu.Lock()
	matched := m.startGuardMatchesLocked(guard, requireIdle)
	m.mu.Unlock()
	if !matched {
		return false, nil
	}

	if err := validateVisualizerRequest(req); err != nil {
		m.mu.Lock()
		stillMatched := m.startGuardMatchesLocked(guard, requireIdle)
		m.mu.Unlock()
		if !stillMatched {
			return false, nil
		}
		return true, err
	}
	probe, cropRect, ffmpegPath, err := m.probeForStart(req)
	if err != nil {
		m.mu.Lock()
		stillMatched := m.startGuardMatchesLocked(guard, requireIdle)
		m.mu.Unlock()
		if !stillMatched {
			return false, nil
		}
		return true, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.startGuardMatchesLocked(guard, requireIdle) {
		return false, nil
	}
	generation := m.allocateGenerationLocked()
	if err := m.startPlaneLocked(req, req.SeekOffsetMs, probe, cropRect, ffmpegPath, generation, guard, requireIdle); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, m.fsm.Transition(EvPlayMedia)
}

// Pause stops the data plane and transitions the FSM to Paused. The current
// plane position is snapshotted so Play can resume from it. Returns an error
// if there is no active session or the adapter does not advertise CanPause.
func (m *Manager) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pauseLocked(sessionGuard{})
}

// PauseIfAdapterRef pauses the active session only when the current
// AdapterRef still matches ref. It lets adapters avoid a Status()+Pause()
// check-then-act race against cross-adapter preemption.
func (m *Manager) PauseIfAdapterRef(ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	guard := adapterRefGuard(ref)
	if !m.sessionGuardMatchesLocked(guard) {
		return false, nil
	}
	if err := m.pauseLocked(guard); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}

// PauseIfSession pauses the active session only when both AdapterRef and
// Generation still match the caller's full session key.
func (m *Manager) PauseIfSession(ref string, generation uint64) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	guard := fullSessionGuard(ref, generation)
	if !m.sessionGuardMatchesLocked(guard) {
		return false, nil
	}
	if err := m.pauseLocked(guard); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}

func (m *Manager) pauseLocked(guard sessionGuard) error {
	if m.active == nil {
		return fmt.Errorf("no session to pause")
	}
	if !m.sessionGuardMatchesLocked(guard) {
		return errAdapterRefChanged
	}
	if !m.active.req.Capabilities.CanPause {
		return fmt.Errorf("adapter does not support pause")
	}
	// Snapshot current plane position so Play() can resume from it.
	if m.plane != nil {
		m.active.pausedPosition = m.plane.Position()
	}
	if m.cancelFn != nil {
		slog.Debug("pausing active session")
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
			if !m.sessionGuardMatchesLocked(guard) {
				return errAdapterRefChanged
			}
		}
	}
	return m.fsm.Transition(EvPause)
}

// Play resumes a paused session by respawning the data plane at the
// snapshotted pause position.
func (m *Manager) Play() error {
	_, err := m.playIfSessionGuard(sessionGuard{})
	return err
}

// PlayIfAdapterRef resumes the active paused session only when the current
// AdapterRef still matches ref. It avoids a Status()+Play() check-then-act
// race against cross-adapter preemption.
func (m *Manager) PlayIfAdapterRef(ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	return m.playIfSessionGuard(adapterRefGuard(ref))
}

// PlayIfSession resumes the active paused session only when both AdapterRef
// and Generation still match the caller's full session key.
func (m *Manager) PlayIfSession(ref string, generation uint64) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	return m.playIfSessionGuard(fullSessionGuard(ref, generation))
}

func (m *Manager) playIfSessionGuard(guard sessionGuard) (bool, error) {
	// Capture the active request outside the lock so we can probe against
	// the same URL without holding the mutex.
	m.mu.Lock()
	a := m.active
	if a == nil {
		m.mu.Unlock()
		if guard.enabled() {
			return false, nil
		}
		return true, fmt.Errorf("no session to resume")
	}
	if !m.sessionGuardMatchesLocked(guard) {
		m.mu.Unlock()
		return false, nil
	}
	req := a.req
	generation := a.generation
	resumeMs := int(a.pausedPosition / time.Millisecond)
	if resumeMs <= 0 {
		resumeMs = a.baseOffsetMs
	}
	m.mu.Unlock()

	if err := validateVisualizerRequest(req); err != nil {
		if guard.enabled() {
			m.mu.Lock()
			matched := m.sessionGuardMatchesLocked(guard)
			m.mu.Unlock()
			if !matched {
				return false, nil
			}
		}
		return true, err
	}
	probe, cropRect, ffmpegPath, err := m.probeForStart(req)
	if err != nil {
		if guard.enabled() {
			m.mu.Lock()
			matched := m.sessionGuardMatchesLocked(guard)
			m.mu.Unlock()
			if !matched {
				return false, nil
			}
		}
		return true, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.sessionGuardMatchesLocked(guard) {
		return false, nil
	}
	if err := m.startPlaneLocked(req, resumeMs, probe, cropRect, ffmpegPath, generation, guard, false); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, m.fsm.Transition(EvPlay)
}

// SetInterlaceFieldOrder changes the interlace polarity live —
// both the in-memory bridge config and, if a cast is active, the
// running Plane's SetFieldOrder. Dual-write so the setting sticks
// across cast-restart boundaries:
//
//   - Future sessions see m.bridge.Video.InterlaceFieldOrder.
//   - The currently-emitting session sees the new polarity on the
//     next field tick via Plane.fieldOrderFlip.
//
// Without the dual-write, a mid-cast flip would be forgotten when
// the session naturally ends + a new one starts.
func (m *Manager) SetInterlaceFieldOrder(order string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch order {
	case "tff", "bff":
	default:
		return fmt.Errorf("interlace_field_order must be tff or bff, got %q", order)
	}
	m.bridge.Video.InterlaceFieldOrder = order
	if m.plane != nil {
		return m.plane.SetFieldOrder(order)
	}
	return nil
}

// CurrentInterlaceOrder returns the in-memory interlace field order.
// Integration tests use this to assert that a UI save reached the
// manager; production runtime reads it directly via bridge fields.
func (m *Manager) CurrentInterlaceOrder() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bridge.Video.InterlaceFieldOrder
}

// UpdateBridge replaces the manager's in-memory bridge config. Used
// by the UI save path for restart-cast fields: the updated bridge
// must be visible to the next session-rebuild path, so we stash it
// here rather than relying on main.go's copy. Does NOT drop the
// active cast — callers do that via DropActiveCast as part of the
// restart-cast dispatch sequence.
func (m *Manager) UpdateBridge(b config.BridgeConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bridge = b
}

// DropActiveCast terminates the current cast session (if any) with the
// given reason logged. Idempotent — returns nil when no session is
// active. Called by the UI save path for restart-cast field changes:
// the ffmpeg pipeline can't reconfigure mid-cast, so we drop and let
// the next play request rebuild with the new settings. Returns the
// FSM transition error if any.
func (m *Manager) DropActiveCast(reason string) error {
	m.mu.Lock()
	if m.active == nil && m.plane == nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	slog.Debug("dropping active cast", "reason", reason)
	return m.Stop()
}

// Stop tears down any active session. Idempotent — calling Stop when already
// idle is a no-op that leaves the FSM in Idle.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(sessionGuard{})
}

// StopIfAdapterRef stops the active session only when the current AdapterRef
// still matches ref. It lets adapters avoid a Status()+Stop() check-then-act
// race against cross-adapter preemption.
func (m *Manager) StopIfAdapterRef(ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	guard := adapterRefGuard(ref)
	if !m.sessionGuardMatchesLocked(guard) {
		return false, nil
	}
	if err := m.stopLocked(guard); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}

// StopIfSession stops the active session only when both AdapterRef and
// Generation still match the caller's full session key.
func (m *Manager) StopIfSession(ref string, generation uint64) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	guard := fullSessionGuard(ref, generation)
	if !m.sessionGuardMatchesLocked(guard) {
		return false, nil
	}
	if err := m.stopLocked(guard); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}

func (m *Manager) stopLocked(guard sessionGuard) error {
	var subtitlePath string
	var onStop func(string)
	var adapterRef string
	if !m.sessionGuardMatchesLocked(guard) {
		return errAdapterRefChanged
	}
	if m.active != nil {
		subtitlePath = m.active.req.SubtitlePath
		onStop = m.active.req.OnStop
		adapterRef = m.active.req.AdapterRef
	}
	if m.active != nil || m.plane != nil {
		slog.Debug("stopping active session")
	}
	if m.cancelFn != nil {
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
			if !m.sessionGuardMatchesLocked(guard) {
				return errAdapterRefChanged
			}
		}
	}
	m.active = nil
	if adapterRef != "" {
		m.emit(eventlog.SeverityInfo, fmt.Sprintf("cast-ended %s", adapterRef))
	}
	removeSubtitleFile(subtitlePath)
	notifySessionStop(onStop, "stopped")
	return m.fsm.Transition(EvStop)
}

// SeekTo tears down the active plane and respawns it at offsetMs. The FSM
// stays in Playing (or Paused) per the Seek semantics; only the data plane
// changes. Requires an active session whose adapter advertises CanSeek.
func (m *Manager) SeekTo(offsetMs int) error {
	_, err := m.seekToIfSessionGuard(sessionGuard{}, offsetMs)
	return err
}

// SeekToIfAdapterRef seeks the active session only when the current
// AdapterRef still matches ref. It lets adapters avoid a Status()+SeekTo()
// check-then-act race against cross-adapter preemption.
func (m *Manager) SeekToIfAdapterRef(ref string, offsetMs int) (bool, error) {
	if ref == "" {
		return false, nil
	}
	return m.seekToIfSessionGuard(adapterRefGuard(ref), offsetMs)
}

// SeekToIfSession seeks the active session only when both AdapterRef and
// Generation still match the caller's full session key.
func (m *Manager) SeekToIfSession(ref string, generation uint64, offsetMs int) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	return m.seekToIfSessionGuard(fullSessionGuard(ref, generation), offsetMs)
}

func (m *Manager) seekToIfSessionGuard(guard sessionGuard, offsetMs int) (bool, error) {
	m.mu.Lock()
	a := m.active
	if a == nil {
		m.mu.Unlock()
		if guard.enabled() {
			return false, nil
		}
		return true, fmt.Errorf("no session")
	}
	if !m.sessionGuardMatchesLocked(guard) {
		m.mu.Unlock()
		return false, nil
	}
	if !a.req.Capabilities.CanSeek {
		m.mu.Unlock()
		return true, fmt.Errorf("adapter does not support seek")
	}
	req := a.req
	generation := a.generation
	m.mu.Unlock()

	if err := validateVisualizerRequest(req); err != nil {
		if guard.enabled() {
			m.mu.Lock()
			matched := m.sessionGuardMatchesLocked(guard)
			m.mu.Unlock()
			if !matched {
				return false, nil
			}
		}
		return true, err
	}
	probe, cropRect, ffmpegPath, err := m.probeForStart(req)
	if err != nil {
		if guard.enabled() {
			m.mu.Lock()
			matched := m.sessionGuardMatchesLocked(guard)
			m.mu.Unlock()
			if !matched {
				return false, nil
			}
		}
		return true, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.sessionGuardMatchesLocked(guard) {
		return false, nil
	}
	if err := m.startPlaneLocked(req, offsetMs, probe, cropRect, ffmpegPath, generation, guard, false); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	// Seek keeps state=playing; FSM's Seek event is a no-op transition.
	return true, m.fsm.Transition(EvSeek)
}

// Status returns the live session status, including the running plane's
// current playback position (for timeline broadcasts). Safe to call from
// any goroutine; the adapter's timeline loop typically polls this at 1 Hz.
func (m *Manager) Status() SessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := SessionStatus{State: m.fsm.State(), MediaKind: MediaKindVideo}
	if m.active != nil {
		st.MediaKind = NormalizeMediaKind(m.active.req.MediaKind)
		st.AdapterRef = m.active.req.AdapterRef
		st.StartedAt = m.active.startedAt
		st.Duration = m.active.duration
		st.Generation = m.active.generation
		if m.plane != nil {
			st.Position = m.plane.Position()
		} else {
			st.Position = m.active.pausedPosition
		}
	}
	return st
}

// StatusHomeView builds the aggregated view consumed by the UI Status
// home and Diagnostics pages. Composes from the active session, the
// running plane (if any), and the bridge config. Mu is held for the
// snapshot only — no I/O, no allocations beyond the returned struct.
//
// See docs/specs/2026-05-08-ui-redesign-pr2-design.md §S3.
func (m *Manager) StatusHomeView() StatusHomeView {
	m.mu.Lock()
	defer m.mu.Unlock()

	view := StatusHomeView{State: m.fsm.State(), MediaKind: MediaKindVideo}
	if m.active != nil {
		view.MediaKind = NormalizeMediaKind(m.active.req.MediaKind)
		view.Title = m.active.req.Title
		view.AdapterRef = m.active.req.AdapterRef
		view.Source = m.active.req.Source
		view.StartedAt = m.active.startedAt
		view.Duration = m.active.duration
		view.Generation = m.active.generation
		view.Modeline = m.bridge.Video.Modeline
		view.Position = m.active.pausedPosition
	}
	if m.plane != nil {
		view.Modeline = m.bridge.Video.Modeline
		view.Position = m.plane.Position()
		view.BlitsTotal = m.plane.BlitsTotal()
		view.FramesTotal = m.plane.FramesTotal()
		view.Underruns = m.plane.Underruns()
		view.WireBytes = m.plane.WireBytes()
		view.LastACKAge = m.plane.LastACKAge()
	}
	return view
}

// probeDuration turns ffprobe's floating-point seconds into a time.Duration.
// Unknown/invalid durations collapse to zero so live streams and malformed
// sources don't advertise nonsense to Plex timelines.
func probeDuration(probe *ffmpeg.ProbeResult) time.Duration {
	if probe == nil || probe.Duration <= 0 {
		return 0
	}
	return time.Duration(probe.Duration * float64(time.Second))
}

// resolveRGBMode maps config's `rgb_mode` string to the Groovy wire byte.
func resolveRGBMode(name string) (byte, error) {
	switch name {
	case "", "rgb888":
		return groovy.RGBMode888, nil
	case "rgba8888":
		return groovy.RGBMode8888, nil
	case "rgb565":
		return groovy.RGBMode565, nil
	}
	return 0, fmt.Errorf("unknown rgb_mode %q", name)
}

// bytesPerPixel returns the raw-video byte stride per pixel for a given RGB
// mode. RGB888 → 3, RGBA8888 → 4, RGB565 → 2.
func bytesPerPixel(rgbMode byte) int {
	switch rgbMode {
	case groovy.RGBMode8888:
		return 4
	case groovy.RGBMode565:
		return 2
	}
	return 3
}
