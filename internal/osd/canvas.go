package osd

import "unicode/utf8"

// Color is an 8-bit-per-channel RGB colour. Canvas writes it in the
// pipeline's bgr24 byte order.
type Color struct{ R, G, B uint8 }

// Canvas is a bgr24 pixel buffer: Width*Height pixels, 3 bytes each, rows
// packed with no padding. For interlaced modes this is one field, so one
// canvas row is two scanlines on the CRT.
type Canvas struct {
	Pix    []byte
	Width  int
	Height int
}

// TextStyle controls DrawText. Scale 0 is treated as 1. Outline draws a
// one-canvas-pixel border in OutlineColor around every lit glyph cell.
type TextStyle struct {
	Color        Color
	Outline      bool
	OutlineColor Color
	ScaleX       int
	ScaleY       int
}

// valid reports whether Pix covers the stated geometry. Drawing into an
// undersized buffer is a no-op rather than a panic: canvases are drawn from
// the data-plane tick goroutine.
func (c Canvas) valid() bool {
	return c.Width > 0 && c.Height > 0 && len(c.Pix) >= c.Width*c.Height*3
}

// FillRect fills the w×h rectangle at (x,y), clipped to the canvas.
func (c Canvas) FillRect(x, y, w, h int, col Color) {
	if !c.valid() {
		return
	}
	x0, y0 := max(x, 0), max(y, 0)
	x1, y1 := min(x+w, c.Width), min(y+h, c.Height)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	for row := y0; row < y1; row++ {
		i := (row*c.Width + x0) * 3
		for col0 := x0; col0 < x1; col0++ {
			c.Pix[i] = col.B
			c.Pix[i+1] = col.G
			c.Pix[i+2] = col.R
			i += 3
		}
	}
}

// DrawText draws s with its top-left corner at (x,y). Runes without a glyph
// render as a blank cell so layout stays predictable.
func (c Canvas) DrawText(x, y int, s string, st TextStyle) {
	if !c.valid() {
		return
	}
	sx, sy := max(st.ScaleX, 1), max(st.ScaleY, 1)
	if st.Outline {
		c.eachLitCell(x, y, s, sx, sy, func(px, py int) {
			c.FillRect(px-1, py-1, sx+2, sy+2, st.OutlineColor)
		})
	}
	c.eachLitCell(x, y, s, sx, sy, func(px, py int) {
		c.FillRect(px, py, sx, sy, st.Color)
	})
}

func (c Canvas) eachLitCell(x, y int, s string, sx, sy int, fn func(px, py int)) {
	cx := x
	for _, r := range s {
		if g, ok := glyphs[r]; ok {
			for gy, row := range g {
				for gx := 0; gx < GlyphWidth; gx++ {
					if row[gx] == '#' {
						fn(cx+gx*sx, y+gy*sy)
					}
				}
			}
		}
		cx += CellWidth * sx
	}
}

// TextWidth is the drawn width of s at horizontal scale sx, excluding the
// trailing inter-glyph gap.
func TextWidth(s string, sx int) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n*CellWidth - 1) * max(sx, 1)
}
