package audiodsp

import (
	"encoding/binary"
	"math"
	"testing"
)

func sinePCM(frames, channels int, sampleRate, freq float64) []byte {
	buf := make([]byte, frames*channels*2)
	w := 2 * math.Pi * freq / sampleRate
	for n := 0; n < frames; n++ {
		v := int16(math.Sin(w*float64(n)) * 20000)
		for ch := 0; ch < channels; ch++ {
			binary.LittleEndian.PutUint16(buf[(n*channels+ch)*2:], uint16(v))
		}
	}
	return buf
}

func maxAbsDiff(a, b []byte) int {
	d := 0
	for i := 0; i+1 < len(a) && i+1 < len(b); i += 2 {
		x := int(int16(binary.LittleEndian.Uint16(a[i:])))
		y := int(int16(binary.LittleEndian.Uint16(b[i:])))
		if v := x - y; v < 0 {
			if -v > d {
				d = -v
			}
		} else if v > d {
			d = v
		}
	}
	return d
}

func TestProcessor_TransparentWithinOneLSB(t *testing.T) {
	t.Parallel()
	c := Design(flatStereo())
	p := NewProcessor(2)
	in := sinePCM(512, 2, 48000, 440)
	got := append([]byte(nil), in...)
	p.Process(got, &c, 100)
	if d := maxAbsDiff(in, got); d > 1 {
		t.Errorf("transparent path altered PCM by %d LSB, want <=1", d)
	}
}

func TestProcessor_VolumeScales(t *testing.T) {
	t.Parallel()
	params := flatStereo()
	params.Bass = 0.0001 // force the float path (not transparent)
	c := Design(params)
	p := NewProcessor(2)
	in := sinePCM(512, 2, 48000, 440)
	got := append([]byte(nil), in...)
	p.Process(got, &c, 50)
	// ~half amplitude; allow generous tolerance for the tiny shaping + rounding.
	if d := maxAbsDiff(in, got); d < 5000 {
		t.Errorf("volume=50 barely changed PCM (maxdiff %d); expected large attenuation", d)
	}
}

func TestProcessor_MonoFoldEqualizesChannels(t *testing.T) {
	t.Parallel()
	params := flatStereo()
	params.Mono = true
	c := Design(params)
	p := NewProcessor(2)
	// Hard-panned input: L loud, R silent.
	buf := make([]byte, 256*2*2)
	for n := 0; n < 256; n++ {
		binary.LittleEndian.PutUint16(buf[(n*2)*2:], uint16(int16(10000)))
	}
	p.Process(buf, &c, 100)
	// After fold both channels should be ~equal (within rounding).
	for n := 0; n < 256; n++ {
		l := int16(binary.LittleEndian.Uint16(buf[(n*2)*2:]))
		r := int16(binary.LittleEndian.Uint16(buf[(n*2+1)*2:]))
		if d := int(l) - int(r); d < -1 || d > 1 {
			t.Fatalf("frame %d not mono-folded: L=%d R=%d", n, l, r)
		}
	}
}

func TestProcessor_HardToggleNoFullStep(t *testing.T) {
	t.Parallel()
	// Steady DC-ish tone, then enable a big boost: the first post-toggle
	// samples must ramp, not jump by the full boosted delta.
	p := NewProcessor(2)
	flat := Design(flatStereo())
	boosted := flatStereo()
	boosted.Bass = 12
	bc := Design(boosted)

	in := sinePCM(64, 2, 48000, 60)
	a := append([]byte(nil), in...)
	p.Process(a, &flat, 100) // settle on flat
	b := append([]byte(nil), in...)
	p.Process(b, &bc, 100) // hard change → must crossfade
	// The very first frame after the toggle should be close to the flat
	// output, not the fully boosted output.
	flatOut := append([]byte(nil), in...)
	q := NewProcessor(2)
	q.Process(flatOut, &flat, 100)
	if d := maxAbsDiff(b[:8], flatOut[:8]); d > 4000 {
		t.Errorf("first frames jumped by %d LSB; ramp should keep them near flat", d)
	}
	if !p.Active() {
		t.Error("processor should report Active during/after a shaping change")
	}
}
