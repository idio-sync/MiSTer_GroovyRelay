//go:build integration

package integration

import (
	"image/png"
	"os"
	"testing"
	"time"
)

// assertPixelVariance opens a PNG dumped by fakemister.Dumper and sanity-
// checks its R-channel variance. A uniform-black or solid-color field has
// near-zero variance and usually indicates a broken pipeline (ffmpeg stuck
// on the decoder's first I-frame, or a BLIT payload that never landed).
// Threshold 100 is conservative — real 8-bit content with any detail
// comfortably exceeds it.
func assertPixelVariance(t *testing.T, pngPath string) {
	t.Helper()
	f, err := os.Open(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	var sum, sumSq uint64
	var n uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			sum += uint64(r >> 8)
			sumSq += uint64(r>>8) * uint64(r>>8)
			n++
		}
	}
	if n == 0 {
		t.Errorf("empty image at %s", pngPath)
		return
	}
	mean := float64(sum) / float64(n)
	variance := float64(sumSq)/float64(n) - mean*mean
	if variance < 100 {
		t.Errorf("pixel variance too low (%f) — image may be uniform black/solid", variance)
	}
}

// assertInterFieldTiming verifies that the gap between consecutive BLIT
// arrivals stays close to one 59.94 Hz field period (~16.68 ms). The
// acceptance band is wide (10–30 ms) to absorb scheduling jitter on the
// test host and the Plane's under-run "duplicate field" behavior that
// still ticks at the same cadence. Fewer than 2 timestamps is a no-op —
// a meaningful check needs at least one gap.
func assertInterFieldTiming(t *testing.T, fieldTimestamps []time.Time) {
	t.Helper()
	if len(fieldTimestamps) < 2 {
		return
	}
	var gaps []time.Duration
	for i := 1; i < len(fieldTimestamps); i++ {
		gaps = append(gaps, fieldTimestamps[i].Sub(fieldTimestamps[i-1]))
	}
	for i, g := range gaps {
		if g < 10*time.Millisecond || g > 30*time.Millisecond {
			t.Errorf("field %d gap = %v, expected ~17ms", i, g)
		}
	}
}
