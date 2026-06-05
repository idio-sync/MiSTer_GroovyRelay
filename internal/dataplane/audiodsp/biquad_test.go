package audiodsp

import (
	"math"
	"testing"
)

func TestBiquadState_UnityIsIdentity(t *testing.T) {
	t.Parallel()
	var bs BiquadState
	bs.SetCoeffs(Unity())
	in := []float64{0.5, -0.3, 1.0, -1.0, 0.0, 0.25}
	for i, x := range in {
		if got := bs.Process(x); math.Abs(got-x) > 1e-12 {
			t.Errorf("sample %d: Process(%v) = %v, want identity", i, x, got)
		}
	}
}

func TestBiquadState_ResetClearsHistory(t *testing.T) {
	t.Parallel()
	var bs BiquadState
	// A leaky integrator-ish coeff set so history matters.
	bs.SetCoeffs(Biquad{B0: 0.5, B1: 0.5, B2: 0, A1: -0.5, A2: 0})
	bs.Process(1.0)
	bs.Process(1.0)
	first := bs.Process(0.0)
	bs.Reset()
	bs.SetCoeffs(Biquad{B0: 0.5, B1: 0.5, B2: 0, A1: -0.5, A2: 0})
	bs.Process(1.0)
	bs.Process(1.0)
	again := bs.Process(0.0)
	if math.Abs(first-again) > 1e-12 {
		t.Errorf("Reset did not restore initial state: first=%v again=%v", first, again)
	}
}
