package calibration

import (
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

func frameOf(p *Pattern) osd.Canvas {
	pix := make([]byte, p.FrameBytes())
	p.ReadFrame(pix)
	return osd.Canvas{Pix: pix, Width: p.width, Height: p.height}
}

func at(c osd.Canvas, x, y int) osd.Color {
	i := (y*c.Width + x) * 3
	return osd.Color{B: c.Pix[i], G: c.Pix[i+1], R: c.Pix[i+2]}
}

// The white border sits exactly on the picture rect's outermost pixels (two
// wide; two lines tall on interlaced, one on progressive) and nothing is
// drawn outside the rect.
func TestPatternBorderTracksPictureRect(t *testing.T) {
	cases := []struct {
		name       string
		w, h       int
		interlaced bool
		geom       config.PictureGeometry
	}{
		{"480i full", 720, 480, true, config.PictureGeometry{}},
		{"480i shrunk", 720, 480, true, config.PictureGeometry{HSize: 90, VSize: 90}},
		{"480i shifted", 720, 480, true, config.PictureGeometry{HSize: 90, VSize: 92, HOffset: 11, VOffset: -3}},
		{"240p shrunk", 720, 240, false, config.PictureGeometry{HSize: 85, VSize: 95}},
		{"576i shrunk", 720, 576, true, config.PictureGeometry{HSize: 95, VSize: 90}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.geom.Rect(tc.w, tc.h, tc.interlaced)
			c := frameOf(NewPattern(tc.w, tc.h, tc.interlaced, r))
			lineH := 1
			if tc.interlaced {
				lineH = 2
			}
			// A point on each edge away from grid lines and safe boxes.
			midX := r.X + r.W/2 + r.W/64
			midY := r.Y + r.H/2 + r.H/48
			for _, p := range []struct {
				name string
				x, y int
				want osd.Color
			}{
				{"left edge", r.X, midY, colorWhite},
				{"left edge inner", r.X + 1, midY, colorWhite},
				{"right edge", r.X + r.W - 1, midY, colorWhite},
				{"right edge inner", r.X + r.W - 2, midY, colorWhite},
				{"top edge", midX, r.Y, colorWhite},
				{"top edge last line", midX, r.Y + lineH - 1, colorWhite},
				{"below top edge", midX, r.Y + lineH, colorBlack},
				{"bottom edge", midX, r.Y + r.H - 1, colorWhite},
				{"above bottom edge", midX, r.Y + r.H - lineH - 1, colorBlack},
				{"right of left edge", r.X + 2, midY, colorBlack},
			} {
				if p.x < 0 || p.x >= c.Width || p.y < 0 || p.y >= c.Height {
					continue
				}
				if got := at(c, p.x, p.y); got != p.want {
					t.Errorf("%s (%d,%d) = %+v, want %+v", p.name, p.x, p.y, got, p.want)
				}
			}
			// Nothing outside the rect.
			for y := 0; y < c.Height; y++ {
				for x := 0; x < c.Width; x++ {
					if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
						continue
					}
					if got := at(c, x, y); got != colorBlack {
						t.Fatalf("pixel (%d,%d) outside rect %+v = %+v, want black", x, y, r, got)
					}
				}
			}
		})
	}
}

// A picture shifted partly off-raster is clipped, not wrapped or panicking:
// the off-raster edge disappears and the others stay.
func TestPatternClipsOffRasterPicture(t *testing.T) {
	r := config.PictureGeometry{HOffset: -10}.Rect(720, 480, true)
	c := frameOf(NewPattern(720, 480, true, r))
	if got := at(c, 0, 250); got == colorWhite {
		t.Fatalf("left edge should be off-raster, but column 0 is white")
	}
	if got := at(c, r.X+r.W-1, 250); got != colorWhite {
		t.Fatalf("right edge = %+v, want white", got)
	}
	if got := at(c, 0, 0); got != colorWhite {
		t.Fatalf("top edge at column 0 = %+v, want white", got)
	}
}

func TestPatternCentreAndCircle(t *testing.T) {
	r := config.PictureRect{W: 720, H: 480}
	c := frameOf(NewPattern(720, 480, true, r))
	if got := at(c, 360, 240); got != colorWhite {
		t.Fatalf("centre = %+v, want white cross", got)
	}
	// Circle: 0.4·H vertically (top at y≈48), 0.3·W horizontally (x≈144).
	if got := at(c, 361, 49); got != colorWhite {
		t.Errorf("circle top = %+v, want white", got)
	}
	if got := at(c, 145, 239); got != colorWhite {
		t.Errorf("circle left = %+v, want white", got)
	}
	if got := at(c, 361, 60); got != colorBlack {
		t.Errorf("inside circle below its top = %+v, want black", got)
	}
}

func TestPatternSetRectIsVisibleToNextRead(t *testing.T) {
	full := config.PictureRect{W: 720, H: 240}
	p := NewPattern(720, 240, false, full)
	if got := at(frameOf(p), 0, 120); got != colorWhite {
		t.Fatalf("full-raster left edge = %+v, want white", got)
	}
	p.SetRect(config.PictureGeometry{HSize: 90}.Rect(720, 240, false))
	c := frameOf(p)
	if got := at(c, 0, 120); got != colorBlack {
		t.Fatalf("after shrink, column 0 = %+v, want black", got)
	}
	if got := at(c, 36, 121); got != colorWhite {
		t.Fatalf("after shrink, new left edge = %+v, want white", got)
	}
}

// ReadFrame and SetRect run on different goroutines in production; every
// read must see one whole frame (run under -race in CI).
func TestPatternConcurrentReadsSeeWholeFrames(t *testing.T) {
	a := config.PictureRect{W: 720, H: 480}
	b := config.PictureGeometry{HSize: 80, VSize: 80}.Rect(720, 480, true)
	p := NewPattern(720, 480, true, a)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if i%2 == 0 {
				p.SetRect(b)
			} else {
				p.SetRect(a)
			}
		}
	}()
	pix := make([]byte, p.FrameBytes())
	for i := 0; i < 50; i++ {
		p.ReadFrame(pix)
		c := osd.Canvas{Pix: pix, Width: 720, Height: 480}
		full := at(c, 0, 250) == colorWhite
		shrunk := at(c, b.X, 250) == colorWhite && at(c, 0, 250) == colorBlack
		if full == shrunk {
			t.Fatalf("read %d is neither the full nor the shrunk frame", i)
		}
	}
	wg.Wait()
}
