package fft

import (
	"math"
	"math/cmplx"
	"testing"
)

func TestReal1024_DCInput(t *testing.T) {
	in := make([]float32, 1024)
	for i := range in {
		in[i] = 1.0
	}
	out := Real1024(in, nil)
	if len(out) != 513 {
		t.Fatalf("len(out) = %d, want 513", len(out))
	}
	// DC bin should be N (= 1024)
	if math.Abs(float64(real(out[0]))-1024.0) > 1e-3 {
		t.Errorf("DC bin = %v, want ~1024", out[0])
	}
	// All other bins should be ~0
	for k := 1; k < 513; k++ {
		if math.Abs(float64(real(out[k]))) > 1e-3 || math.Abs(float64(imag(out[k]))) > 1e-3 {
			t.Errorf("bin[%d] = %v, want ~0", k, out[k])
		}
	}
}

func TestReal1024_BinCenteredSine(t *testing.T) {
	// Bin k=64 corresponds to frequency 64/1024 * sampleRate.
	// At sampleRate=48000, that's exactly 3000 Hz — a bin-centered tone.
	const targetBin = 64
	in := make([]float32, 1024)
	for n := range in {
		in[n] = float32(math.Sin(2 * math.Pi * float64(targetBin) * float64(n) / 1024))
	}
	out := Real1024(in, nil)
	// Magnitude at targetBin should dominate; other bins near-zero
	peakMag := cmplx.Abs(complex128(out[targetBin]))
	for k := 1; k < 513; k++ {
		if k == targetBin {
			continue
		}
		if mag := cmplx.Abs(complex128(out[k])); mag > peakMag*0.01 {
			t.Errorf("non-peak bin[%d] mag = %f, peak mag = %f", k, mag, peakMag)
		}
	}
}

func TestReal1024_ParsevalTheorem(t *testing.T) {
	// Random-ish input; assert ∑|x[n]|² ≈ (1/N) * ∑|X[k]|² over full spectrum
	in := make([]float32, 1024)
	for n := range in {
		in[n] = float32(math.Sin(2*math.Pi*5*float64(n)/1024) + 0.5*math.Cos(2*math.Pi*17*float64(n)/1024))
	}
	var timeEnergy float64
	for _, x := range in {
		timeEnergy += float64(x) * float64(x)
	}
	out := Real1024(in, nil)
	var freqEnergy float64
	// Real-input FFT: bins 0 and 512 (Nyquist) count once; bins 1..511 count twice
	freqEnergy += float64(real(out[0]))*float64(real(out[0])) + float64(imag(out[0]))*float64(imag(out[0]))
	freqEnergy += float64(real(out[512]))*float64(real(out[512])) + float64(imag(out[512]))*float64(imag(out[512]))
	for k := 1; k < 512; k++ {
		mag2 := float64(real(out[k]))*float64(real(out[k])) + float64(imag(out[k]))*float64(imag(out[k]))
		freqEnergy += 2 * mag2
	}
	freqEnergy /= 1024
	rel := math.Abs(freqEnergy-timeEnergy) / timeEnergy
	if rel > 1e-4 {
		t.Errorf("Parseval mismatch: time=%f freq=%f rel=%f", timeEnergy, freqEnergy, rel)
	}
}

func TestHann1024_Properties(t *testing.T) {
	w := Hann1024()
	if len(w) != 1024 {
		t.Fatalf("len = %d, want 1024", len(w))
	}
	if w[0] != 0 {
		t.Errorf("w[0] = %f, want 0", w[0])
	}
	mid := w[512]
	if math.Abs(float64(mid)-1.0) > 0.01 {
		t.Errorf("w[512] = %f, want ~1.0", mid)
	}
	// Energy gain sum(w²)/N should be 3/8 = 0.375
	var energy float64
	for _, x := range w {
		energy += float64(x) * float64(x)
	}
	gain := energy / 1024
	if math.Abs(gain-0.375) > 0.001 {
		t.Errorf("Hann energy gain = %f, want 0.375", gain)
	}
}
