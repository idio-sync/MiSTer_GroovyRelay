package calibration

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// IdleTimeout ends a calibration nobody has touched for this long, so an
// abandoned browser tab cannot leave the pattern on the CRT.
const IdleTimeout = 5 * time.Minute

var (
	// ErrBusy: the bridge is casting; calibration only starts when idle.
	ErrBusy = errors.New("calibration: bridge is busy")
	// ErrNotActive: there is no calibration (or unsaved draft) to act on.
	ErrNotActive = errors.New("calibration: not active")
)

// State is the calibration lifecycle as the UI sees it.
type State string

const (
	StateIdle   State = "idle"   // no calibration; the draft is the saved values
	StateActive State = "active" // pattern on the CRT, draft being adjusted
	StateEnded  State = "ended"  // pattern gone, unsaved draft kept (see EndReason)
)

// End reasons reported with StateEnded.
const (
	EndCast       = "cast"        // another cast took over
	EndStopped    = "stopped"     // stopped from the transport controls
	EndTimeout    = "timeout"     // IdleTimeout elapsed
	EndError      = "error"       // the session failed (e.g. MiSTer unreachable)
	EndSaveFailed = "save-failed" // Save could not write the config
)

// Snapshot is the calibration state for the UI.
type Snapshot struct {
	State     State                  `json:"state"`
	Draft     config.PictureGeometry `json:"draft"`
	Saved     config.PictureGeometry `json:"saved"`
	EndReason string                 `json:"endReason"`
	// RasterWidth and FieldLines size the UI's picture diagram for the
	// configured modeline (offsets are raster pixels and field lines).
	RasterWidth int `json:"rasterWidth"`
	FieldLines  int `json:"fieldLines"`
}

// Sessions is the slice of *core.Manager the controller drives. Every call
// is guarded by (ref, generation) so a real cast is never touched.
type Sessions interface {
	StartSessionIfIdleSnapshot(core.SessionRequest) (core.SessionStatus, bool, error)
	StopIfSession(ref string, generation uint64) (bool, error)
}

// Saver persists the picture geometry (a thin wrapper over the bridge
// settings saver).
type Saver interface {
	SavePicture(config.PictureGeometry) error
}

// Controller owns one calibration at a time. Its mutex is never held across
// a Sessions or Saver call.
type Controller struct {
	sessions Sessions
	saver    Saver
	bridge   func() config.BridgeConfig
	// afterFunc schedules the idle timeout; tests replace it.
	afterFunc func(time.Duration, func()) (stop func() bool)

	mu        sync.Mutex
	state     State
	starting  bool
	draft     config.PictureGeometry
	endReason string
	seq       uint64
	ref       string // session this controller owns; "" = none
	gen       uint64
	pattern   *Pattern
	width     int
	height    int
	interlace bool
	activity  uint64 // bumped per Start/Preview; stale timeouts compare it
	stopTimer func() bool
}

// NewController returns an idle controller. bridge returns the current
// bridge config (saved geometry and modeline).
func NewController(sessions Sessions, saver Saver, bridge func() config.BridgeConfig) *Controller {
	return &Controller{
		sessions: sessions,
		saver:    saver,
		bridge:   bridge,
		afterFunc: func(d time.Duration, f func()) func() bool {
			return time.AfterFunc(d, f).Stop
		},
		state: StateIdle,
	}
}

// Snapshot returns the current state. Idle reports the saved values as the
// draft.
func (c *Controller) Snapshot() Snapshot {
	b := c.bridge()
	saved := b.Video.Picture().Normalized()
	var rasterWidth, fieldLines int
	if preset, err := core.ResolvePreset(b.Video.Modeline); err == nil {
		rasterWidth, fieldLines = int(preset.Modeline.HActive), preset.Modeline.FieldHeight()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{
		State: c.state, Draft: c.draft, Saved: saved, EndReason: c.endReason,
		RasterWidth: rasterWidth, FieldLines: fieldLines,
	}
	if c.state == StateIdle {
		s.Draft = saved
	}
	return s
}

// Start puts the pattern on the CRT. It resumes an ended calibration's
// unsaved draft, otherwise starts from the saved values. It is a no-op when
// a calibration is already running, and ErrBusy when a cast is.
func (c *Controller) Start() error {
	b := c.bridge()
	preset, err := core.ResolvePreset(b.Video.Modeline)
	if err != nil {
		return err
	}
	ml := preset.Modeline

	c.mu.Lock()
	if c.state == StateActive || c.starting {
		c.mu.Unlock()
		return nil
	}
	draft := b.Video.Picture().Normalized()
	if c.state == StateEnded {
		draft = c.draft
	}
	c.seq++
	ref := fmt.Sprintf("calibration:%d", c.seq)
	w, h, il := int(ml.HActive), int(ml.VActive), ml.Interlaced()
	pattern := NewPattern(w, h, il, draft.Rect(w, h, il))
	c.starting, c.ref = true, ref
	c.mu.Unlock()

	st, matched, err := c.sessions.StartSessionIfIdleSnapshot(core.SessionRequest{
		Frames:     pattern,
		AdapterRef: ref,
		Source:     "calibration",
		Title:      "Test pattern",
		QuietOSD:   true,
		OnStop:     func(reason string) { c.onStop(ref, reason) },
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	c.starting = false
	if err != nil || !matched {
		if c.ref == ref {
			c.ref = ""
		}
		if err != nil {
			return err
		}
		return ErrBusy
	}
	if c.ref != ref {
		// Stopped (preempted, failed) before Start returned; onStop already
		// recorded why.
		return nil
	}
	c.state, c.endReason = StateActive, ""
	c.draft, c.gen, c.pattern = draft, st.Generation, pattern
	c.width, c.height, c.interlace = w, h, il
	c.touchLocked()
	return nil
}

// Preview makes g the draft and redraws the pattern for it.
func (c *Controller) Preview(g config.PictureGeometry) error {
	if err := g.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateActive || c.ref == "" {
		return ErrNotActive
	}
	c.draft = g.Normalized()
	// Rendering is ~1 ms of pure CPU; holding c.mu keeps the pattern in
	// the same order as the drafts.
	c.pattern.SetRect(c.draft.Rect(c.width, c.height, c.interlace))
	c.touchLocked()
	return nil
}

// Save ends the calibration and persists the draft. On a write failure the
// calibration ends with the draft kept so Save can be retried.
func (c *Controller) Save() error {
	c.mu.Lock()
	if c.state == StateIdle {
		c.mu.Unlock()
		return ErrNotActive
	}
	draft := c.draft
	ref, gen := c.detachLocked()
	c.mu.Unlock()

	c.stopSession(ref, gen)
	err := c.saver.SavePicture(draft)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.state, c.endReason = StateEnded, EndSaveFailed
		return err
	}
	c.resetLocked()
	return nil
}

// Cancel ends the calibration and discards the draft.
func (c *Controller) Cancel() {
	c.mu.Lock()
	ref, gen := c.detachLocked()
	c.resetLocked()
	c.mu.Unlock()
	c.stopSession(ref, gen)
}

func (c *Controller) stopSession(ref string, gen uint64) {
	if ref != "" {
		_, _ = c.sessions.StopIfSession(ref, gen)
	}
}

// onStop is the session's OnStop hook: the pattern left the CRT for a
// reason the controller did not cause.
func (c *Controller) onStop(ref, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ref == "" || ref != c.ref {
		return // a session this controller already let go of
	}
	c.detachLocked()
	c.state = StateEnded
	switch reason {
	case "preempted":
		c.endReason = EndCast
	case "stopped":
		c.endReason = EndStopped
	default:
		c.endReason = EndError
	}
}

// touchLocked records activity and re-arms the idle timeout.
func (c *Controller) touchLocked() {
	c.activity++
	token := c.activity
	if c.stopTimer != nil {
		c.stopTimer()
	}
	c.stopTimer = c.afterFunc(IdleTimeout, func() { c.expire(token) })
}

func (c *Controller) expire(token uint64) {
	c.mu.Lock()
	if c.state != StateActive || token != c.activity {
		c.mu.Unlock()
		return
	}
	ref, gen := c.detachLocked()
	c.state, c.endReason = StateEnded, EndTimeout
	c.mu.Unlock()
	c.stopSession(ref, gen)
}

// detachLocked releases the session so its OnStop is ignored, and returns
// what the caller should stop.
func (c *Controller) detachLocked() (ref string, gen uint64) {
	ref, gen = c.ref, c.gen
	c.ref, c.gen, c.pattern = "", 0, nil
	if c.stopTimer != nil {
		c.stopTimer()
		c.stopTimer = nil
	}
	return ref, gen
}

func (c *Controller) resetLocked() {
	c.state, c.endReason = StateIdle, ""
	c.draft = config.PictureGeometry{}
}
