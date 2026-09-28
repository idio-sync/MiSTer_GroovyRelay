package config

import (
	"fmt"
	"math"
)

// Picture geometry bounds. Size shrinks the picture inside the raster so a
// CRT that overscans shows the whole image; 80% covers the heaviest consumer
// overscan. Offsets move it within (or partly off) the raster: 72 px is the
// full horizontal border at 80% of a 720-wide raster, and 28 field lines is
// the vertical border at 80% of PAL's 288.
const (
	DefaultPictureSize   = 100.0
	MinPictureSize       = 80.0
	MaxPictureSize       = 100.0
	MaxPictureHOffset    = 72
	MaxPictureVOffset    = 28
	pictureMinDimensions = 2
)

// PictureGeometry is the operator's picture size and position calibration:
// sizes in percent of the raster (0 = 100), offsets in raster pixels
// (horizontal) and field lines (vertical). It is the persisted
// [bridge.video] picture_* values and the calibration draft alike.
type PictureGeometry struct {
	HSize   float64 `json:"hSize"`
	VSize   float64 `json:"vSize"`
	HOffset int     `json:"hOffset"`
	VOffset int     `json:"vOffset"`
}

// PictureRect is where the picture lands in the output raster, in raster
// pixels and full-frame lines. X/Y may be negative and X+W / Y+H may exceed
// the raster when an offset pushes the picture partly off-screen; Visible
// clips it.
type PictureRect struct {
	X, Y, W, H int
}

// Picture returns the persisted picture geometry.
func (v VideoConfig) Picture() PictureGeometry {
	return PictureGeometry{
		HSize:   v.PictureHSize,
		VSize:   v.PictureVSize,
		HOffset: v.PictureHOffset,
		VOffset: v.PictureVOffset,
	}
}

// EffectivePictureHSize resolves an unset horizontal size to 100%.
func (v VideoConfig) EffectivePictureHSize() float64 { return v.Picture().EffectiveHSize() }

// EffectivePictureVSize resolves an unset vertical size to 100%.
func (v VideoConfig) EffectivePictureVSize() float64 { return v.Picture().EffectiveVSize() }

// PictureRect places the persisted picture in an outW×outH raster. See
// PictureGeometry.Rect.
func (v VideoConfig) PictureRect(outW, outH int, interlaced bool) PictureRect {
	return v.Picture().Rect(outW, outH, interlaced)
}

// EffectiveHSize resolves an unset horizontal size to 100%.
func (g PictureGeometry) EffectiveHSize() float64 { return effectivePictureSize(g.HSize) }

// EffectiveVSize resolves an unset vertical size to 100%.
func (g PictureGeometry) EffectiveVSize() float64 { return effectivePictureSize(g.VSize) }

// Normalized returns g with unset sizes resolved to 100%, so two geometries
// that place the picture identically compare equal.
func (g PictureGeometry) Normalized() PictureGeometry {
	g.HSize, g.VSize = g.EffectiveHSize(), g.EffectiveVSize()
	return g
}

func effectivePictureSize(size float64) float64 {
	if size == 0 {
		return DefaultPictureSize
	}
	return size
}

// Rect places the picture in an outW×outH raster. The picture is scaled to
// the configured size, centred, then shifted by the offsets. Vertical
// offsets are field lines, so on interlaced output one step is two raster
// lines; H and Y stay even there so each field carries the same number of
// picture lines. The defaults return the whole raster.
func (g PictureGeometry) Rect(outW, outH int, interlaced bool) PictureRect {
	lineStep := 1
	if interlaced {
		lineStep = 2
	}
	w := evenAtLeast(float64(outW)*g.EffectiveHSize()/100, pictureMinDimensions)
	h := evenAtLeast(float64(outH)*g.EffectiveVSize()/100, pictureMinDimensions)
	w, h = min(w, outW), min(h, outH)
	y := (outH - h) / 2
	if interlaced {
		y &^= 1
	}
	return PictureRect{
		X: (outW-w)/2 + g.HOffset,
		Y: y + g.VOffset*lineStep,
		W: w,
		H: h,
	}
}

// evenAtLeast rounds x to the nearest even integer, but not below floor.
func evenAtLeast(x float64, floor int) int {
	return max(int(math.Round(x/2))*2, floor)
}

// Visible clips r to an outW×outH raster. An empty result means the picture
// is entirely off-screen, which the config bounds rule out.
func (r PictureRect) Visible(outW, outH int) PictureRect {
	x0, y0 := max(r.X, 0), max(r.Y, 0)
	x1, y1 := min(r.X+r.W, outW), min(r.Y+r.H, outH)
	if x1 <= x0 || y1 <= y0 {
		return PictureRect{}
	}
	return PictureRect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// IsFull reports whether r is exactly the whole outW×outH raster.
func (r PictureRect) IsFull(outW, outH int) bool {
	return r == PictureRect{W: outW, H: outH}
}

// Validate checks the geometry against the picture bounds. Sizes of 0 mean
// 100% and are accepted.
func (g PictureGeometry) Validate() error {
	for _, f := range []struct {
		key  string
		size float64
	}{
		{"picture_h_size", g.HSize},
		{"picture_v_size", g.VSize},
	} {
		if f.size == 0 {
			continue // unset = 100%
		}
		if math.IsNaN(f.size) || f.size < MinPictureSize || f.size > MaxPictureSize {
			return fmt.Errorf("bridge.video.%s must be in %g..%g, got %g", f.key, MinPictureSize, MaxPictureSize, f.size)
		}
	}
	if g.HOffset < -MaxPictureHOffset || g.HOffset > MaxPictureHOffset {
		return fmt.Errorf("bridge.video.picture_h_offset must be in -%d..%d, got %d", MaxPictureHOffset, MaxPictureHOffset, g.HOffset)
	}
	if g.VOffset < -MaxPictureVOffset || g.VOffset > MaxPictureVOffset {
		return fmt.Errorf("bridge.video.picture_v_offset must be in -%d..%d, got %d", MaxPictureVOffset, MaxPictureVOffset, g.VOffset)
	}
	return nil
}

func validatePicture(v VideoConfig) error { return v.Picture().Validate() }
