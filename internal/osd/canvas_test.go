package osd

import (
	"strings"
	"testing"
)

var (
	testFG = Color{R: 0x40, G: 0xE0, B: 0x40}
	testBG = Color{R: 0x10, G: 0x20, B: 0x30}
	black  = Color{}
)

func newTestCanvas(w, h int) Canvas {
	return Canvas{Pix: make([]byte, w*h*3), Width: w, Height: h}
}

func pixelAt(c Canvas, x, y int) Color {
	i := (y*c.Width + x) * 3
	return Color{B: c.Pix[i], G: c.Pix[i+1], R: c.Pix[i+2]}
}

func TestFillRectWritesBGRByteOrder(t *testing.T) {
	c := newTestCanvas(4, 3)
	c.FillRect(1, 1, 2, 1, Color{R: 0xAA, G: 0xBB, B: 0xCC})

	i := (1*4 + 1) * 3
	if got := c.Pix[i : i+3]; got[0] != 0xCC || got[1] != 0xBB || got[2] != 0xAA {
		t.Fatalf("pixel bytes = % x, want cc bb aa (B,G,R)", got)
	}
	if got := pixelAt(c, 0, 1); got != black {
		t.Fatalf("pixel (0,1) = %+v, want untouched", got)
	}
	if got := pixelAt(c, 3, 1); got != black {
		t.Fatalf("pixel (3,1) = %+v, want untouched", got)
	}
}

func TestFillRectClipsToCanvas(t *testing.T) {
	c := newTestCanvas(4, 3)
	c.FillRect(-2, -2, 4, 4, testFG) // covers (0,0)-(1,1)
	c.FillRect(3, 2, 10, 10, testFG) // covers (3,2) only
	c.FillRect(10, 10, 5, 5, testFG) // fully off-canvas
	c.FillRect(0, 0, -1, 5, testFG)  // negative size

	lit := 0
	for y := 0; y < c.Height; y++ {
		for x := 0; x < c.Width; x++ {
			if pixelAt(c, x, y) == testFG {
				lit++
			}
		}
	}
	if lit != 5 {
		t.Fatalf("lit pixels = %d, want 5", lit)
	}
	for _, p := range [][2]int{{0, 0}, {1, 1}, {3, 2}} {
		if got := pixelAt(c, p[0], p[1]); got != testFG {
			t.Fatalf("pixel %v = %+v, want fg", p, got)
		}
	}
}

func TestCanvasShorterThanGeometryIsNoOp(t *testing.T) {
	c := Canvas{Pix: make([]byte, 10), Width: 4, Height: 3}
	c.FillRect(0, 0, 4, 3, testFG)
	c.DrawText(0, 0, "A", TextStyle{Color: testFG})
	for i, b := range c.Pix {
		if b != 0 {
			t.Fatalf("byte %d = %#x, want untouched on undersized buffer", i, b)
		}
	}
}

func TestDrawTextRendersScaledGlyph(t *testing.T) {
	const sx, sy = 3, 2
	c := newTestCanvas(40, 30)
	c.DrawText(2, 1, "A", TextStyle{Color: testFG, ScaleX: sx, ScaleY: sy})

	g, ok := glyphs['A']
	if !ok {
		t.Fatal("no glyph for 'A'")
	}
	for gy, row := range g {
		for gx, cell := range row {
			for dy := 0; dy < sy; dy++ {
				for dx := 0; dx < sx; dx++ {
					x, y := 2+gx*sx+dx, 1+gy*sy+dy
					got := pixelAt(c, x, y)
					if cell == '#' && got != testFG {
						t.Fatalf("glyph cell (%d,%d) → pixel (%d,%d) = %+v, want fg", gx, gy, x, y, got)
					}
					if cell == '.' && got != black {
						t.Fatalf("glyph cell (%d,%d) → pixel (%d,%d) = %+v, want untouched", gx, gy, x, y, got)
					}
				}
			}
		}
	}
}

func TestDrawTextAdvancesByCellWidth(t *testing.T) {
	c := newTestCanvas(40, 10)
	c.DrawText(0, 0, "II", TextStyle{Color: testFG, ScaleX: 1, ScaleY: 1})

	// 'I' has a lit top row across all five columns; the second glyph starts
	// one cell (glyph width + 1 gap column) to the right.
	for x := 0; x < GlyphWidth; x++ {
		if pixelAt(c, x, 0) != testFG || pixelAt(c, CellWidth+x, 0) != testFG {
			t.Fatalf("top row of 'II' not lit at column %d", x)
		}
	}
	if got := pixelAt(c, GlyphWidth, 0); got != black {
		t.Fatalf("gap column = %+v, want untouched", got)
	}
}

func TestDrawTextOutlinesGlyph(t *testing.T) {
	c := newTestCanvas(20, 12)
	for i := range c.Pix {
		c.Pix[i] = 0x77
	}
	c.DrawText(2, 2, "I", TextStyle{Color: testFG, Outline: true, OutlineColor: testBG, ScaleX: 1, ScaleY: 1})

	// 'I' top-left lit cell sits at (2,2); its up-left neighbour is outline.
	if got := pixelAt(c, 1, 1); got != testBG {
		t.Fatalf("outline pixel = %+v, want outline colour", got)
	}
	if got := pixelAt(c, 2, 2); got != testFG {
		t.Fatalf("glyph pixel = %+v, want fg (fg must draw over outline)", got)
	}
	if got := pixelAt(c, 15, 10); got != (Color{R: 0x77, G: 0x77, B: 0x77}) {
		t.Fatalf("far pixel = %+v, want untouched background", got)
	}
}

func TestDrawTextUnknownRuneIsBlankCell(t *testing.T) {
	c := newTestCanvas(30, 10)
	c.DrawText(0, 0, "~I", TextStyle{Color: testFG, ScaleX: 1, ScaleY: 1})

	for y := 0; y < GlyphHeight; y++ {
		for x := 0; x < CellWidth; x++ {
			if got := pixelAt(c, x, y); got != black {
				t.Fatalf("unknown-rune cell pixel (%d,%d) = %+v, want blank", x, y, got)
			}
		}
	}
	if got := pixelAt(c, CellWidth, 0); got != testFG {
		t.Fatalf("glyph after unknown rune not drawn at next cell")
	}
}

func TestDrawTextZeroScaleDefaultsToOne(t *testing.T) {
	c := newTestCanvas(20, 10)
	c.DrawText(0, 0, "I", TextStyle{Color: testFG})
	if got := pixelAt(c, 0, 0); got != testFG {
		t.Fatalf("pixel (0,0) = %+v, want fg with default scale", got)
	}
}

func TestTextWidth(t *testing.T) {
	if got := TextWidth("", 2); got != 0 {
		t.Fatalf("TextWidth(\"\") = %d, want 0", got)
	}
	// Trailing gap column is not counted: n cells minus one gap.
	if got, want := TextWidth("CH 07", 3), (5*CellWidth-1)*3; got != want {
		t.Fatalf("TextWidth = %d, want %d", got, want)
	}
	if got, want := TextWidth("▶", 1), GlyphWidth; got != want {
		t.Fatalf("TextWidth(▶) = %d, want %d (counts runes, not bytes)", got, want)
	}
}

func TestGlyphTableShapes(t *testing.T) {
	required := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 :.-/▶◀■"
	for _, r := range required {
		if _, ok := glyphs[r]; !ok {
			t.Errorf("missing glyph for %q", r)
		}
	}
	for r, g := range glyphs {
		for i, row := range g {
			if len(row) != GlyphWidth || strings.Trim(row, "#.") != "" {
				t.Errorf("glyph %q row %d = %q, want %d chars of '#'/'.'", r, i, row, GlyphWidth)
			}
		}
	}
}
