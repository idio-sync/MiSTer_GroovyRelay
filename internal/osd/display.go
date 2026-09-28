package osd

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// How long each element stays on screen after it is triggered.
const (
	VolumeDuration    = 3 * time.Second
	ChannelDuration   = 4 * time.Second
	TransportDuration = 3 * time.Second
)

const (
	// VolumeSegments is the number of blocks in the volume bar.
	VolumeSegments = 20
	// MaxChannelRunes caps the channel label so it cannot run into the
	// transport label across the top of the screen.
	MaxChannelRunes = 10
)

var (
	colorWhite   = Color{R: 232, G: 232, B: 232}
	colorGreen   = Color{R: 64, G: 224, B: 64}
	colorDim     = Color{R: 24, G: 72, B: 24}
	colorRed     = Color{R: 232, G: 48, B: 48}
	colorOutline = Color{}
)

// Options are the operator-facing OSD settings.
type Options struct {
	Enabled bool
	// Clock shows the local time under the channel banner.
	Clock    bool
	Clock24h bool
}

// Transport is a playback action announced in the top-left corner.
type Transport uint8

const (
	TransportPlay Transport = iota + 1
	TransportFastForward
	TransportRewind
)

// Display is the OSD state shared between the control plane (Show*, from
// HTTP handlers) and the data plane (Draw, once per field). It outlives any
// single cast so an overlay triggered during preemption carries into the
// next session. All methods are safe for concurrent use and on a nil
// *Display (which shows nothing); the mutex is only held to copy state,
// never while drawing.
type Display struct {
	mu sync.Mutex
	st state

	// The clock string is formatted at most once per minute so steady-state
	// Draw never allocates.
	clockMinute int64
	clockH24    bool
	clockStr    string
}

// state is the copyable part of Display.
type state struct {
	opts Options

	volume      int
	volumeText  string // strconv.Itoa(volume), formatted off the tick path
	muted       bool
	volumeUntil time.Time

	channel      string
	channelUntil time.Time

	transport      Transport
	transportUntil time.Time
}

// NewDisplay returns an idle Display.
func NewDisplay(opts Options) *Display {
	return &Display{st: state{opts: opts}}
}

// SetOptions applies new settings; the next Draw uses them.
func (d *Display) SetOptions(opts Options) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.st.opts = opts
	d.mu.Unlock()
}

// ShowVolume shows the volume bar (or MUTING) for VolumeDuration.
func (d *Display) ShowVolume(volume int, muted bool, now time.Time) {
	if d == nil {
		return
	}
	volume = min(max(volume, 0), 100)
	text := strconv.Itoa(volume)
	d.mu.Lock()
	d.st.volume = volume
	d.st.volumeText = text
	d.st.muted = muted
	d.st.volumeUntil = now.Add(VolumeDuration)
	d.mu.Unlock()
}

// ShowChannel shows the channel banner (and clock, when enabled) for
// ChannelDuration. The label is upper-cased and truncated to
// MaxChannelRunes.
func (d *Display) ShowChannel(label string, now time.Time) {
	if d == nil {
		return
	}
	label = strings.ToUpper(label)
	if r := []rune(label); len(r) > MaxChannelRunes {
		label = string(r[:MaxChannelRunes])
	}
	d.mu.Lock()
	d.st.channel = label
	d.st.channelUntil = now.Add(ChannelDuration)
	d.mu.Unlock()
}

// ShowTransport announces a playback action for TransportDuration.
func (d *Display) ShowTransport(t Transport, now time.Time) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.st.transport = t
	d.st.transportUntil = now.Add(TransportDuration)
	d.mu.Unlock()
}

// Showing is a snapshot of the elements Draw would put on screen at a given
// instant. Hidden elements are zero-valued.
type Showing struct {
	VolumeVisible bool
	Volume        int
	Muted         bool
	Channel       string
	Transport     Transport
}

// Showing reports what Draw would draw at now. A nil or disabled Display
// shows nothing.
func (d *Display) Showing(now time.Time) Showing {
	if d == nil {
		return Showing{}
	}
	d.mu.Lock()
	s := d.st
	d.mu.Unlock()
	var out Showing
	if !s.opts.Enabled {
		return out
	}
	if now.Before(s.volumeUntil) {
		out.VolumeVisible, out.Volume, out.Muted = true, s.volume, s.muted
	}
	if now.Before(s.channelUntil) {
		out.Channel = s.channel
	}
	if now.Before(s.transportUntil) {
		out.Transport = s.transport
	}
	return out
}

// Draw stamps every live element onto c. A nil Display draws nothing.
func (d *Display) Draw(c Canvas, now time.Time) {
	if d == nil || !c.valid() {
		return
	}
	d.mu.Lock()
	s := d.st // copy; drawing happens unlocked
	var clock string
	if s.opts.Enabled && s.opts.Clock && now.Before(s.channelUntil) {
		clock = d.clockLocked(now, s.opts.Clock24h)
	}
	d.mu.Unlock()
	if !s.opts.Enabled {
		return
	}

	l := layoutFor(c)
	if now.Before(s.transportUntil) {
		if text := transportText(s.transport); text != "" {
			c.DrawText(l.safeX0, l.safeY0, text, l.style(colorWhite))
		}
	}
	if now.Before(s.channelUntil) {
		c.DrawText(l.safeX1-TextWidth(s.channel, l.sx), l.safeY0, s.channel, l.style(colorGreen))
		if clock != "" {
			c.DrawText(l.safeX1-TextWidth(clock, l.sx), l.safeY0+l.rowH, clock, l.style(colorWhite))
		}
	}
	if now.Before(s.volumeUntil) {
		if s.muted {
			c.DrawText(l.safeX0, l.volumeY, "MUTING", l.style(colorRed))
		} else {
			l.drawVolumeBar(c, s.volume, s.volumeText)
		}
	}
}

// layout is the OSD geometry for one canvas size. Glyphs sit inside the
// title-safe rectangle [safeX0,safeX1)×[safeY0,safeY1) — a 10% inset of the
// picture area that consumer CRTs never crop — and their outline may spill
// one pixel past it. Glyph scale follows the whole canvas so text keeps its
// size when the picture is shrunk.
type layout struct {
	sx, sy         int
	safeX0, safeY0 int
	safeX1, safeY1 int
	glyphH, rowH   int
	volumeY        int
	barX0          int
}

func layoutFor(c Canvas) layout {
	w, h, pic := c.Width, c.Height, c.picture()
	l := layout{
		// 3× wide and 2 field lines tall on a 720×240 field: ~28 scanlines
		// per character once both fields interleave on the tube.
		sx:     max(1, w/240),
		sy:     max(1, h/120),
		safeX0: pic.X + pic.W/10,
		safeY0: pic.Y + pic.H/10,
		safeX1: pic.X + pic.W - pic.W/10,
		safeY1: pic.Y + pic.H - pic.H/10,
	}
	l.glyphH = GlyphHeight * l.sy
	l.rowH = (GlyphHeight + 3) * l.sy
	l.volumeY = l.safeY1 - l.glyphH
	l.barX0 = l.safeX0 + len("VOLUME ")*CellWidth*l.sx
	return l
}

func (l layout) style(col Color) TextStyle {
	return TextStyle{Color: col, Outline: true, OutlineColor: colorOutline, ScaleX: l.sx, ScaleY: l.sy}
}

// Volume bar segments are two font columns wide with a one-column gap.
func (l layout) segmentPitch() int { return 3 * l.sx }

func (l layout) segmentCenter(i int) (x, y int) {
	return l.barX0 + i*l.segmentPitch() + l.sx, l.volumeY + l.glyphH/2
}

func (l layout) drawVolumeBar(c Canvas, volume int, volumeText string) {
	c.DrawText(l.safeX0, l.volumeY, "VOLUME", l.style(colorGreen))
	lit := (volume*VolumeSegments + 99) / 100 // any non-zero volume lights one
	segW := 2 * l.sx
	for i := 0; i < VolumeSegments; i++ {
		x := l.barX0 + i*l.segmentPitch()
		c.FillRect(x-1, l.volumeY-1, segW+2, l.glyphH+2, colorOutline)
		col := colorDim
		if i < lit {
			col = colorGreen
		}
		c.FillRect(x, l.volumeY, segW, l.glyphH, col)
	}
	numX := l.barX0 + VolumeSegments*l.segmentPitch() + CellWidth*l.sx
	c.DrawText(numX, l.volumeY, volumeText, l.style(colorWhite))
}

// clockLocked returns the clock string for now, reformatting only when the
// minute or 12h/24h setting changes. Caller holds d.mu.
func (d *Display) clockLocked(now time.Time, h24 bool) string {
	minute := now.Unix() / 60
	if d.clockStr == "" || minute != d.clockMinute || h24 != d.clockH24 {
		d.clockStr = clockText(now.Local(), h24)
		d.clockMinute, d.clockH24 = minute, h24
	}
	return d.clockStr
}

func transportText(t Transport) string {
	switch t {
	case TransportPlay:
		return "PLAY ▶"
	case TransportFastForward:
		return "FF ▶▶"
	case TransportRewind:
		return "REW ◀◀"
	}
	return ""
}

func clockText(t time.Time, h24 bool) string {
	if h24 {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}
