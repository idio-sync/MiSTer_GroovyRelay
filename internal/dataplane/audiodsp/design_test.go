package audiodsp

import (
	"math"
	"testing"
)

// measureGainDB drives a sine at freq through one biquad and returns the
// steady-state output/input amplitude ratio in dB. Skips a settling prefix.
func measureGainDB(c Biquad, sampleRate, freq float64) float64 {
	var bs BiquadState
	bs.SetCoeffs(c)
	const total = 8192
	const settle = 4096
	w := 2 * math.Pi * freq / sampleRate
	var sumIn, sumOut float64
	for n := 0; n < total; n++ {
		x := math.Sin(w * float64(n))
		y := bs.Process(x)
		if n >= settle {
			sumIn += x * x
			sumOut += y * y
		}
	}
	rmsIn := math.Sqrt(sumIn)
	rmsOut := math.Sqrt(sumOut)
	return 20 * math.Log10(rmsOut/rmsIn)
}

func TestDesignPeaking_CenterGain(t *testing.T) {
	t.Parallel()
	const fs, f0, q = 48000.0, 1000.0, 1.4
	for _, gain := range []float64{+6, -6, +12, 0} {
		c := DesignPeaking(fs, f0, q, gain)
		got := measureGainDB(c, fs, f0)
		if math.Abs(got-gain) > 0.5 {
			t.Errorf("peaking %+.0f dB @1k: measured %.2f dB", gain, got)
		}
	}
}

func TestDesignLowShelf_PassbandGain(t *testing.T) {
	t.Parallel()
	const fs, f0 = 48000.0, 100.0
	// Well below the corner, a low shelf approaches its full gain.
	c := DesignLowShelf(fs, f0, 0.707, +6)
	got := measureGainDB(c, fs, 30)
	if math.Abs(got-6) > 1.0 {
		t.Errorf("low shelf +6 dB @30Hz: measured %.2f dB", got)
	}
}

func TestDesignHighShelf_PassbandGain(t *testing.T) {
	t.Parallel()
	const fs, f0 = 48000.0, 10000.0
	c := DesignHighShelf(fs, f0, 0.707, +6)
	got := measureGainDB(c, fs, 18000)
	if math.Abs(got-6) > 1.0 {
		t.Errorf("high shelf +6 dB @18kHz: measured %.2f dB", got)
	}
}

func TestDesignHighpass_StopAndPass(t *testing.T) {
	t.Parallel()
	const fs, f0 = 48000.0, 100.0
	c := DesignHighpass(fs, f0, 0.707)
	if got := measureGainDB(c, fs, 20); got > -6 {
		t.Errorf("highpass @20Hz: measured %.2f dB, want strong attenuation", got)
	}
	if got := measureGainDB(c, fs, 2000); math.Abs(got) > 1.0 {
		t.Errorf("highpass @2kHz: measured %.2f dB, want ~0", got)
	}
}
