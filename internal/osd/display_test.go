package osd

import (
	"testing"
	"time"
)

var (
	testNow = time.Date(2026, 9, 24, 21, 41, 0, 0, time.Local)
	bgFill  = Color{R: 0x55, G: 0x55, B: 0x55}
)

// newFieldCanvas returns a 720×240 canvas (one 480i field) filled with a
// background colour that no OSD palette entry uses.
func newFieldCanvas() Canvas {
	c := newTestCanvas(720, 240)
	c.FillRect(0, 0, c.Width, c.Height, bgFill)
	return c
}

type bbox struct{ x0, y0, x1, y1, n int }

// changed returns the bounding box and count of pixels that differ from
// the background fill.
func changed(c Canvas) bbox {
	b := bbox{x0: c.Width, y0: c.Height, x1: -1, y1: -1}
	for y := 0; y < c.Height; y++ {
		for x := 0; x < c.Width; x++ {
			if pixelAt(c, x, y) != bgFill {
				b.n++
				b.x0, b.y0 = min(b.x0, x), min(b.y0, y)
				b.x1, b.y1 = max(b.x1, x), max(b.y1, y)
			}
		}
	}
	return b
}

func countColor(c Canvas, col Color) int {
	n := 0
	for y := 0; y < c.Height; y++ {
		for x := 0; x < c.Width; x++ {
			if pixelAt(c, x, y) == col {
				n++
			}
		}
	}
	return n
}

func TestDisplayIdleDrawsNothing(t *testing.T) {
	c := newFieldCanvas()
	NewDisplay(Options{Enabled: true, Clock: true}).Draw(c, testNow)
	if b := changed(c); b.n != 0 {
		t.Fatalf("idle display changed %d pixels, want 0", b.n)
	}
}

func TestNilDisplayDrawIsNoOp(t *testing.T) {
	c := newFieldCanvas()
	var d *Display
	d.Draw(c, testNow)
	if b := changed(c); b.n != 0 {
		t.Fatalf("nil display changed %d pixels, want 0", b.n)
	}
}

func TestDisplayDisabledDrawsNothing(t *testing.T) {
	c := newFieldCanvas()
	d := NewDisplay(Options{Enabled: false})
	d.ShowVolume(50, false, testNow)
	d.ShowChannel("CH 07", testNow)
	d.ShowTransport(TransportPlay, testNow)
	d.Draw(c, testNow)
	if b := changed(c); b.n != 0 {
		t.Fatalf("disabled display changed %d pixels, want 0", b.n)
	}
}

func TestSetOptionsDisablesLiveOverlay(t *testing.T) {
	d := NewDisplay(Options{Enabled: true})
	d.ShowVolume(50, false, testNow)
	d.SetOptions(Options{Enabled: false})

	c := newFieldCanvas()
	d.Draw(c, testNow)
	if b := changed(c); b.n != 0 {
		t.Fatalf("display disabled mid-overlay changed %d pixels, want 0", b.n)
	}
}

func TestVolumeBarLightsProportionalSegments(t *testing.T) {
	cases := []struct{ volume, lit int }{
		{0, 0}, {1, 1}, {38, 8}, {50, 10}, {100, VolumeSegments},
	}
	for _, tc := range cases {
		c := newFieldCanvas()
		d := NewDisplay(Options{Enabled: true})
		d.ShowVolume(tc.volume, false, testNow)
		d.Draw(c, testNow)

		l := layoutFor(c.Width, c.Height)
		lit := 0
		for i := 0; i < VolumeSegments; i++ {
			x, y := l.segmentCenter(i)
			switch got := pixelAt(c, x, y); got {
			case colorGreen:
				lit++
			case colorDim:
			default:
				t.Fatalf("volume %d: segment %d centre = %+v, want green or dim", tc.volume, i, got)
			}
		}
		if lit != tc.lit {
			t.Errorf("volume %d: lit segments = %d, want %d", tc.volume, lit, tc.lit)
		}
	}
}

func TestVolumeOutOfRangeIsClamped(t *testing.T) {
	for _, v := range []int{-5, 150} {
		c := newFieldCanvas()
		d := NewDisplay(Options{Enabled: true})
		d.ShowVolume(v, false, testNow)
		d.Draw(c, testNow) // must not panic or index past the bar
	}
}

func TestMutedShowsMutingInsteadOfBar(t *testing.T) {
	c := newFieldCanvas()
	d := NewDisplay(Options{Enabled: true})
	d.ShowVolume(80, true, testNow)
	d.Draw(c, testNow)

	if countColor(c, colorRed) == 0 {
		t.Fatal("muted overlay drew no red MUTING text")
	}
	if n := countColor(c, colorGreen) + countColor(c, colorDim); n != 0 {
		t.Fatalf("muted overlay drew %d volume-bar pixels, want 0", n)
	}
}

func TestVolumeOverlayExpires(t *testing.T) {
	d := NewDisplay(Options{Enabled: true})
	d.ShowVolume(50, false, testNow)

	c := newFieldCanvas()
	d.Draw(c, testNow.Add(VolumeDuration-time.Millisecond))
	if changed(c).n == 0 {
		t.Fatal("volume overlay gone before VolumeDuration")
	}
	c = newFieldCanvas()
	d.Draw(c, testNow.Add(VolumeDuration))
	if b := changed(c); b.n != 0 {
		t.Fatalf("volume overlay still drawn at VolumeDuration (%d pixels)", b.n)
	}
}

func TestChannelIsGreenAndRightAligned(t *testing.T) {
	c := newFieldCanvas()
	d := NewDisplay(Options{Enabled: true})
	d.ShowChannel("CH 07", testNow)
	d.Draw(c, testNow)

	if countColor(c, colorGreen) == 0 {
		t.Fatal("channel label drew no green pixels")
	}
	l := layoutFor(c.Width, c.Height)
	b := changed(c)
	// Outline extends one pixel past the glyphs on each side.
	if b.x1 != l.safeX1 {
		t.Fatalf("channel right edge = %d, want %d (right-aligned to title-safe)", b.x1, l.safeX1)
	}
	if b.y0 != l.safeY0-1 {
		t.Fatalf("channel top = %d, want %d (top of title-safe)", b.y0, l.safeY0-1)
	}
}

func TestChannelLabelIsUppercased(t *testing.T) {
	draw := func(label string) Canvas {
		c := newFieldCanvas()
		d := NewDisplay(Options{Enabled: true})
		d.ShowChannel(label, testNow)
		d.Draw(c, testNow)
		return c
	}
	lower, upper := draw("Plex"), draw("PLEX")
	for i := range lower.Pix {
		if lower.Pix[i] != upper.Pix[i] {
			t.Fatal(`"Plex" rendered differently from "PLEX"`)
		}
	}
}

func TestLongChannelLabelIsTruncated(t *testing.T) {
	c := newFieldCanvas()
	d := NewDisplay(Options{Enabled: true})
	d.ShowChannel("A VERY LONG STREAM CHANNEL NAME", testNow)
	d.Draw(c, testNow)

	l := layoutFor(c.Width, c.Height)
	b := changed(c)
	if got, max := b.x1-b.x0+1, TextWidth("0123456789", l.sx)+2; got > max {
		t.Fatalf("channel label width = %d, want <= %d (%d runes + outline)", got, max, MaxChannelRunes)
	}
}

func TestChannelOverlayExpires(t *testing.T) {
	d := NewDisplay(Options{Enabled: true})
	d.ShowChannel("CH 07", testNow)
	c := newFieldCanvas()
	d.Draw(c, testNow.Add(ChannelDuration))
	if b := changed(c); b.n != 0 {
		t.Fatalf("channel overlay still drawn at ChannelDuration (%d pixels)", b.n)
	}
}

func TestClockAccompaniesChannelOnlyWhenEnabled(t *testing.T) {
	draw := func(opts Options) int {
		c := newFieldCanvas()
		d := NewDisplay(opts)
		d.ShowChannel("CH 07", testNow)
		d.Draw(c, testNow)
		return changed(c).n
	}
	without := draw(Options{Enabled: true})
	with := draw(Options{Enabled: true, Clock: true})
	if with <= without {
		t.Fatalf("clock-enabled banner changed %d pixels, clock-disabled %d; want more with clock", with, without)
	}
}

func TestClockText(t *testing.T) {
	cases := []struct {
		at   time.Time
		h24  bool
		want string
	}{
		{time.Date(2026, 1, 1, 21, 41, 0, 0, time.UTC), false, "9:41 PM"},
		{time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC), false, "12:05 AM"},
		{time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), false, "12:00 PM"},
		{time.Date(2026, 1, 1, 21, 41, 0, 0, time.UTC), true, "21:41"},
		{time.Date(2026, 1, 1, 7, 3, 0, 0, time.UTC), true, "07:03"},
	}
	for _, tc := range cases {
		if got := clockText(tc.at, tc.h24); got != tc.want {
			t.Errorf("clockText(%s, 24h=%t) = %q, want %q", tc.at.Format("15:04"), tc.h24, got, tc.want)
		}
	}
}

func TestTransportText(t *testing.T) {
	cases := map[Transport]string{
		TransportPlay:        "PLAY ▶",
		TransportFastForward: "FF ▶▶",
		TransportRewind:      "REW ◀◀",
		Transport(0):         "",
	}
	for tr, want := range cases {
		if got := transportText(tr); got != want {
			t.Errorf("transportText(%d) = %q, want %q", tr, got, want)
		}
	}
}

func TestTransportIsTopLeftAndExpires(t *testing.T) {
	d := NewDisplay(Options{Enabled: true})
	d.ShowTransport(TransportPlay, testNow)

	c := newFieldCanvas()
	d.Draw(c, testNow)
	l := layoutFor(c.Width, c.Height)
	b := changed(c)
	if b.n == 0 || b.x0 != l.safeX0-1 || b.y0 != l.safeY0-1 {
		t.Fatalf("transport bbox = %+v, want top-left at (%d,%d)", b, l.safeX0-1, l.safeY0-1)
	}

	c = newFieldCanvas()
	d.Draw(c, testNow.Add(TransportDuration))
	if b := changed(c); b.n != 0 {
		t.Fatalf("transport overlay still drawn at TransportDuration (%d pixels)", b.n)
	}
}

func TestAllElementsStayInsideTitleSafe(t *testing.T) {
	for _, size := range [][2]int{{720, 240}, {720, 288}, {640, 240}} {
		c := newTestCanvas(size[0], size[1])
		c.FillRect(0, 0, c.Width, c.Height, bgFill)
		d := NewDisplay(Options{Enabled: true, Clock: true})
		d.ShowVolume(100, false, testNow)
		d.ShowChannel("CH 12", testNow)
		d.ShowTransport(TransportRewind, testNow)
		d.Draw(c, time.Date(2026, 1, 1, 12, 59, 0, 0, time.Local)) // widest 12h clock

		l := layoutFor(c.Width, c.Height)
		b := changed(c)
		if b.x0 < l.safeX0-1 || b.y0 < l.safeY0-1 || b.x1 > l.safeX1 || b.y1 > l.safeY1 {
			t.Errorf("%dx%d: overlay bbox %+v escapes title-safe [%d,%d]-[%d,%d]",
				size[0], size[1], b, l.safeX0, l.safeY0, l.safeX1, l.safeY1)
		}
	}
}

func TestDisplayOnTinyCanvasDoesNotPanic(t *testing.T) {
	d := NewDisplay(Options{Enabled: true, Clock: true})
	d.ShowVolume(100, false, testNow)
	d.ShowChannel("CH 12", testNow)
	d.ShowTransport(TransportPlay, testNow)
	for _, size := range [][2]int{{1, 1}, {8, 8}, {0, 0}} {
		d.Draw(newTestCanvas(size[0], size[1]), testNow)
	}
}
