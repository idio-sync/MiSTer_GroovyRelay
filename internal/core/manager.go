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
	"os"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
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

// Manager is the adapter-agnostic session orchestrator. One Manager per
// process; all adapters share it. Thread-safe.
type Manager struct {
	bridge config.BridgeConfig
	sender *groovynet.Sender
	fsm    *StateMachine

	mu       sync.Mutex
	cancelFn context.CancelFunc
	plane    *dataplane.Plane // nil when idle
	active   *activeSession
}

// activeSession is the manager's private per-session context. Adapter-
// specific state (subscribers, media keys, etc.) stays in the adapter.
type activeSession struct {
	req            SessionRequest
	startedAt      time.Time
	baseOffsetMs   int           // offset the plane was spawned with
	pausedPosition time.Duration // snapshot from plane at Pause
	duration       time.Duration
}

// NewManager constructs a Manager. The Sender must already be bound to the
// MiSTer's address; Manager does not own its lifecycle (the sender is shared
// across the process lifetime so its source UDP port remains stable).
func NewManager(bridge config.BridgeConfig, sender *groovynet.Sender) *Manager {
	return &Manager{bridge: bridge, sender: sender, fsm: New()}
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

// probeForStart runs Probe and (conditionally) ProbeCrop with a bounded
// context so a stuck PMS cannot deadlock the control plane. Called by
// StartSession/Play/SeekTo BEFORE acquiring Manager.mu so the mutex is
// never held during network I/O.
func (m *Manager) probeForStart(req SessionRequest) (*ffmpeg.ProbeResult, *ffmpeg.CropRect, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe, err := ffmpeg.Probe(ctx, req.StreamURL)
	if err != nil {
		return nil, nil, fmt.Errorf("probe source: %w", err)
	}
	var cropRect *ffmpeg.CropRect
	if m.bridge.Video.AspectMode == "auto" {
		// ProbeCrop failures degrade gracefully to letterbox — ignore the error.
		cropRect, _ = ffmpeg.ProbeCrop(ctx, req.StreamURL, req.InputHeaders, 2*time.Second)
	}
	return probe, cropRect, nil
}

// startPlaneLocked spawns a new data plane. Caller MUST hold m.mu AND have
// already run Probe/ProbeCrop (passed in as probe + cropRect) — this
// function must not perform network I/O while the mutex is held.
func (m *Manager) startPlaneLocked(req SessionRequest, offsetMs int,
	probe *ffmpeg.ProbeResult, cropRect *ffmpeg.CropRect) error {
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
	if m.active != nil && m.active.req.SubtitlePath != req.SubtitlePath {
		oldSubtitle = m.active.req.SubtitlePath
	}
	if m.active != nil {
		oldOnStop = m.active.req.OnStop
	}
	if m.cancelFn != nil {
		slog.Info("preempting prior session for new request", "new_url", req.StreamURL)
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
		}
	}
	removeSubtitleFile(oldSubtitle)
	notifySessionStop(oldOnStop, "preempted")

	// Resolve the modeline preset from config (empty defaults to NTSC_480i).
	preset, err := ResolvePreset(m.bridge.Video.Modeline)
	if err != nil {
		return err
	}
	modeline := preset.Modeline
	rgbMode, err := resolveRGBMode(m.bridge.Video.RGBMode)
	if err != nil {
		return err
	}

	// Groovy SWITCHRES carries full-frame vActive even for interlaced modes;
	// the sender transmits one field at a time, so fieldH is half-height there.
	fieldH := modeline.FieldHeight()
	bpp := bytesPerPixel(rgbMode)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	spec := ffmpeg.PipelineSpec{
		InputURL:        req.StreamURL,
		InputHeaders:    req.InputHeaders,
		SeekSeconds:     float64(offsetMs) / 1000.0,
		UseSSSeek:       req.DirectPlay,
		SourceProbe:     probe,
		OutputWidth:     int(modeline.HActive),
		OutputHeight:    int(modeline.VActive),
		FieldOrder:      m.bridge.Video.InterlaceFieldOrder,
		OutputFpsExpr:   preset.FpsExpr,
		AspectMode:      m.bridge.Video.AspectMode,
		CropRect:        cropRect,
		SubtitleURL:     req.SubtitleURL,
		SubtitlePath:    req.SubtitlePath,
		SubtitleIndex:   req.SubtitleIndex,
		AudioSampleRate: m.bridge.Audio.SampleRate,
		AudioChannels:   m.bridge.Audio.Channels,
	}

	plane := dataplane.NewPlane(dataplane.PlaneConfig{
		Sender:        m.sender,
		SpawnSpec:     spec,
		Modeline:      modeline,
		FieldWidth:    int(modeline.HActive),
		FieldHeight:   fieldH,
		BytesPerPixel: bpp,
		RGBMode:       rgbMode,
		LZ4Enabled:    m.bridge.Video.LZ4Enabled,
		AudioRate:     m.bridge.Audio.SampleRate,
		AudioChans:    m.bridge.Audio.Channels,
		SeekOffsetMs:  offsetMs,
	})
	m.plane = plane
	m.active = &activeSession{
		req:          req,
		startedAt:    time.Now(),
		baseOffsetMs: offsetMs,
		duration:     probeDuration(probe),
	}

	go func() {
		runErr := plane.Run(ctx)
		m.logPlaneExit(runErr)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.plane != plane {
			return
		}
		m.plane = nil
		if runErr == nil {
			_ = m.fsm.Transition(EvEOF)
		}
	}()
	return nil
}

// StartSession is the adapter-agnostic entry point. Adapters translate their
// protocol-specific requests into a SessionRequest and call this. Any
// existing session is preempted and the prior goroutine awaited.
func (m *Manager) StartSession(req SessionRequest) error {
	probe, cropRect, err := m.probeForStart(req)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startPlaneLocked(req, req.SeekOffsetMs, probe, cropRect); err != nil {
		return err
	}
	return m.fsm.Transition(EvPlayMedia)
}

// Pause stops the data plane and transitions the FSM to Paused. The current
// plane position is snapshotted so Play can resume from it. Returns an error
// if there is no active session or the adapter does not advertise CanPause.
func (m *Manager) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return fmt.Errorf("no session to pause")
	}
	if !m.active.req.Capabilities.CanPause {
		return fmt.Errorf("adapter does not support pause")
	}
	// Snapshot current plane position so Play() can resume from it.
	if m.plane != nil {
		m.active.pausedPosition = m.plane.Position()
	}
	if m.cancelFn != nil {
		slog.Info("pausing active session")
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
		}
	}
	return m.fsm.Transition(EvPause)
}

// Play resumes a paused session by respawning the data plane at the
// snapshotted pause position.
func (m *Manager) Play() error {
	// Capture the active request outside the lock so we can probe against
	// the same URL without holding the mutex.
	m.mu.Lock()
	a := m.active
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("no session to resume")
	}
	req := a.req
	resumeMs := int(a.pausedPosition / time.Millisecond)
	if resumeMs <= 0 {
		resumeMs = a.baseOffsetMs
	}
	m.mu.Unlock()

	probe, cropRect, err := m.probeForStart(req)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startPlaneLocked(req, resumeMs, probe, cropRect); err != nil {
		return err
	}
	return m.fsm.Transition(EvPlay)
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
	slog.Info("dropping active cast", "reason", reason)
	return m.Stop()
}

// Stop tears down any active session. Idempotent — calling Stop when already
// idle is a no-op that leaves the FSM in Idle.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subtitlePath string
	var onStop func(string)
	if m.active != nil {
		subtitlePath = m.active.req.SubtitlePath
		onStop = m.active.req.OnStop
	}
	if m.active != nil || m.plane != nil {
		slog.Info("stopping active session")
	}
	if m.cancelFn != nil {
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
		}
	}
	m.active = nil
	removeSubtitleFile(subtitlePath)
	notifySessionStop(onStop, "stopped")
	return m.fsm.Transition(EvStop)
}

// SeekTo tears down the active plane and respawns it at offsetMs. The FSM
// stays in Playing (or Paused) per the Seek semantics; only the data plane
// changes. Requires an active session whose adapter advertises CanSeek.
func (m *Manager) SeekTo(offsetMs int) error {
	m.mu.Lock()
	a := m.active
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("no session")
	}
	if !a.req.Capabilities.CanSeek {
		m.mu.Unlock()
		return fmt.Errorf("adapter does not support seek")
	}
	req := a.req
	m.mu.Unlock()

	probe, cropRect, err := m.probeForStart(req)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startPlaneLocked(req, offsetMs, probe, cropRect); err != nil {
		return err
	}
	// Seek keeps state=playing; FSM's Seek event is a no-op transition.
	return m.fsm.Transition(EvSeek)
}

// Status returns the live session status, including the running plane's
// current playback position (for timeline broadcasts). Safe to call from
// any goroutine; the adapter's timeline loop typically polls this at 1 Hz.
func (m *Manager) Status() SessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := SessionStatus{State: m.fsm.State()}
	if m.active != nil {
		st.AdapterRef = m.active.req.AdapterRef
		st.StartedAt = m.active.startedAt
		st.Duration = m.active.duration
		if m.plane != nil {
			st.Position = m.plane.Position()
		} else {
			st.Position = m.active.pausedPosition
		}
	}
	return st
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
