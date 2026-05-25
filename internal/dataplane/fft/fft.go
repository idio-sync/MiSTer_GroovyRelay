// Package fft provides a fixed-size 1024-point radix-2 real-input FFT
// for the audio-scope DSP path. Private to internal/dataplane.
package fft

import "math"

// N is the FFT size. Fixed at 1024 for the audio-scope path; the
// audio meter is built around this size and would require coefficient
// recomputation to change.
const N = 1024

var (
	twiddles [N]complex64
	bitRev   [N]int
	hann     [N]float32
)

func init() {
	// Twiddle factors for forward FFT: w_k = exp(-2πi*k/N), k=0..N-1.
	for k := 0; k < N; k++ {
		theta := -2 * math.Pi * float64(k) / float64(N)
		twiddles[k] = complex(float32(math.Cos(theta)), float32(math.Sin(theta)))
	}
	// Bit-reversal table for N = 2^10
	const bits = 10
	for i := 0; i < N; i++ {
		r := 0
		x := i
		for b := 0; b < bits; b++ {
			r = (r << 1) | (x & 1)
			x >>= 1
		}
		bitRev[i] = r
	}
	// Hann window of length N
	for n := 0; n < N; n++ {
		hann[n] = float32(0.5 * (1 - math.Cos(2*math.Pi*float64(n)/float64(N-1))))
	}
}

// Hann1024 returns a fresh copy of the precomputed Hann window of length 1024.
// Callers may mutate the returned slice; subsequent calls return new copies.
func Hann1024() []float32 {
	out := make([]float32, N)
	copy(out, hann[:])
	return out
}

// Real1024 computes the 1024-point DFT of a 1024-sample real input.
// Returns 513 complex bins: DC, 511 positive frequencies, Nyquist.
// If out has capacity ≥ 513 it is reused (and re-sliced to length 513);
// otherwise a fresh slice is allocated.
//
// Panics if len(in) != 1024.
func Real1024(in []float32, out []complex64) []complex64 {
	if len(in) != N {
		panic("fft.Real1024: input must be length 1024")
	}
	if cap(out) < N/2+1 {
		out = make([]complex64, N/2+1)
	} else {
		out = out[:N/2+1]
	}

	// Bit-reversal: place real input into work buffer at reversed indices.
	var work [N]complex64
	for i := 0; i < N; i++ {
		work[bitRev[i]] = complex(in[i], 0)
	}

	// Cooley-Tukey decimation-in-time butterflies.
	for size := 2; size <= N; size *= 2 {
		half := size / 2
		step := N / size
		for i := 0; i < N; i += size {
			for j := 0; j < half; j++ {
				k := i + j
				t := twiddles[j*step] * work[k+half]
				work[k+half] = work[k] - t
				work[k] = work[k] + t
			}
		}
	}

	// Copy first N/2+1 bins; the rest are complex conjugates for real input.
	copy(out, work[:N/2+1])
	return out
}
