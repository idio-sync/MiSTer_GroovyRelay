package osd

import "testing"

// A calibrated picture area moves the title-safe inset inside it, so the
// OSD stays on the part of the raster the CRT actually shows.
func TestLayoutFollowsPictureArea(t *testing.T) {
	c := newFieldCanvas()
	c.Picture = Rect{X: 40, Y: 12, W: 648, H: 216}
	l := layoutFor(c)
	if l.safeX0 != 40+64 || l.safeX1 != 40+648-64 {
		t.Fatalf("safe x = [%d,%d), want [104,624)", l.safeX0, l.safeX1)
	}
	if l.safeY0 != 12+21 || l.safeY1 != 12+216-21 {
		t.Fatalf("safe y = [%d,%d), want [33,207)", l.safeY0, l.safeY1)
	}
	if full := layoutFor(newFieldCanvas()); l.sx != full.sx || l.sy != full.sy {
		t.Fatalf("glyph scale changed with the picture area: %d×%d, want %d×%d", l.sx, l.sy, full.sx, full.sy)
	}

	d := NewDisplay(Options{Enabled: true})
	d.ShowChannel("CH 07", testNow)
	d.Draw(c, testNow)
	if b := changed(c); b.x1 != l.safeX1 || b.y0 != l.safeY0-1 {
		t.Fatalf("channel bbox right/top = %d/%d, want %d/%d", b.x1, b.y0, l.safeX1, l.safeY0-1)
	}
}

func TestCanvasPictureFallsBackToWholeCanvas(t *testing.T) {
	c := newTestCanvas(720, 240)
	for _, r := range []Rect{{}, {X: 800, W: 100, H: 100}, {W: -1, H: 10}} {
		c.Picture = r
		if got := c.picture(); got != (Rect{W: 720, H: 240}) {
			t.Errorf("picture(%+v) = %+v, want whole canvas", r, got)
		}
	}
	c.Picture = Rect{X: -10, Y: 5, W: 720, H: 240}
	if got, want := c.picture(), (Rect{X: 0, Y: 5, W: 710, H: 235}); got != want {
		t.Errorf("clipped picture = %+v, want %+v", got, want)
	}
}
