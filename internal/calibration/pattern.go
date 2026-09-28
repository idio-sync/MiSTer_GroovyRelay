// Package calibration puts a geometry test pattern on the CRT and lets the
// operator adjust picture size and position against it. The pattern is drawn
// in Go straight into the output raster and fed to the data plane in place
// of ffmpeg, so each adjustment redraws in place with no pipeline restart.
// See docs/superpowers/specs/2026-09-28-crt-picture-calibration-design.md.
package calibration

import (
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

// Pattern colours. White and the safe-area colours stay a little under full
// scale so the border does not bloom on a bright set.
var (
	colorBlack      = osd.Color{}
	colorWhite      = osd.Color{R: 235, G: 235, B: 235}
	colorGrid       = osd.Color{R: 112, G: 112, B: 112}
	colorActionSafe = osd.Color{R: 235, G: 200, B: 0} // 95%
	colorTitleSafe  = osd.Color{R: 0, G: 200, B: 235} // 90%
)

// Grid cells are square in the 4:3 picture: 16 across, 12 down.
const (
	gridCols = 16
	gridRows = 12
)

// Pattern is a dataplane.FrameSource that serves one pre-rendered bgr24
// raster frame. SetRect redraws it for a new picture rect; ReadFrame (the
// data plane's reader goroutine) always sees a whole frame, old or new.
type Pattern struct {
	width, height int
	interlaced    bool

	renderMu sync.Mutex // serializes SetRect; guards back
	back     []byte

	mu    sync.Mutex // guards front
	front []byte
}

// NewPattern returns a pattern for a width×height bgr24 raster, drawn at r.
func NewPattern(width, height int, interlaced bool, r config.PictureRect) *Pattern {
	p := &Pattern{
		width:      width,
		height:     height,
		interlaced: interlaced,
		back:       make([]byte, width*height*3),
		front:      make([]byte, width*height*3),
	}
	p.SetRect(r)
	return p
}

// FrameBytes is the size of one frame ReadFrame fills.
func (p *Pattern) FrameBytes() int { return p.width * p.height * 3 }

// ReadFrame copies the current frame into dst.
func (p *Pattern) ReadFrame(dst []byte) {
	p.mu.Lock()
	copy(dst, p.front)
	p.mu.Unlock()
}

// SetRect redraws the pattern with its edge on r and publishes it.
func (p *Pattern) SetRect(r config.PictureRect) {
	p.renderMu.Lock()
	defer p.renderMu.Unlock()
	render(osd.Canvas{Pix: p.back, Width: p.width, Height: p.height}, r, p.interlaced)
	p.mu.Lock()
	p.front, p.back = p.back, p.front
	p.mu.Unlock()
}

// render draws the pattern for picture rect r. Every element is placed by
// its fraction of the rect, which is how ffmpeg's fit, anamorphic stretch
// and placement map real content, so a size that distorts the aspect
// distorts the pattern the same way. Horizontal lines are two raster lines
// on interlaced output so each field carries one and nothing twitters;
// vertical lines are two pixels.
func render(c osd.Canvas, r config.PictureRect, interlaced bool) {
	c.FillRect(0, 0, c.Width, c.Height, colorBlack)
	if r.W <= 0 || r.H <= 0 {
		return
	}
	lineH := 1
	if interlaced {
		lineH = 2
	}
	const lineW = 2
	px := func(fx float64) int { return r.X + int(fx*float64(r.W)+0.5) }
	py := func(fy float64) int { return r.Y + int(fy*float64(r.H)+0.5) }
	vline := func(x, y0, y1 int, col osd.Color) { c.FillRect(x-lineW/2, y0, lineW, y1-y0, col) }
	hline := func(y, x0, x1 int, col osd.Color) { c.FillRect(x0, y-lineH/2, x1-x0, lineH, col) }
	box := func(inset float64, col osd.Color) {
		x0, y0, x1, y1 := px(inset), py(inset), px(1-inset), py(1-inset)
		c.FillRect(x0, y0, lineW, y1-y0, col)
		c.FillRect(x1-lineW, y0, lineW, y1-y0, col)
		c.FillRect(x0, y0, x1-x0, lineH, col)
		c.FillRect(x0, y1-lineH, x1-x0, lineH, col)
	}

	// Crosshatch.
	for i := 1; i < gridCols; i++ {
		vline(px(float64(i)/gridCols), r.Y, r.Y+r.H, colorGrid)
	}
	for j := 1; j < gridRows; j++ {
		hline(py(float64(j)/gridRows), r.X, r.X+r.W, colorGrid)
	}

	// Safe areas: 95% action-safe, 90% title-safe.
	box(0.025, colorActionSafe)
	box(0.05, colorTitleSafe)

	// Centre cross, two grid cells each way.
	cx, cy := px(0.5), py(0.5)
	vline(cx, py(0.5-2.0/gridRows), py(0.5+2.0/gridRows), colorWhite)
	hline(cy, px(0.5-2.0/gridCols), px(0.5+2.0/gridCols), colorWhite)

	// Circle, 0.8 of the picture height across: 0.8 of the height and 0.6
	// of the width, since the picture is 4:3.
	ring(c, float64(r.X)+0.5*float64(r.W), float64(r.Y)+0.5*float64(r.H),
		0.3*float64(r.W), 0.4*float64(r.H), lineW, lineH, colorWhite)

	// Edge border, on top: the outermost pixels of the picture.
	box(0, colorWhite)
}

// ring draws the band between the ellipse with radii (rx, ry) centred on
// (cx, cy) and the one inset by (tx, ty), clipped to the canvas.
func ring(c osd.Canvas, cx, cy, rx, ry float64, tx, ty int, col osd.Color) {
	inside := func(x, y, rx, ry float64) bool {
		if rx <= 0 || ry <= 0 {
			return false
		}
		dx, dy := (x-cx)/rx, (y-cy)/ry
		return dx*dx+dy*dy <= 1
	}
	x0, x1 := max(int(cx-rx)-1, 0), min(int(cx+rx)+2, c.Width)
	y0, y1 := max(int(cy-ry)-1, 0), min(int(cy+ry)+2, c.Height)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if inside(fx, fy, rx, ry) && !inside(fx, fy, rx-float64(tx), ry-float64(ty)) {
				c.FillRect(x, y, 1, 1, col)
			}
		}
	}
}
