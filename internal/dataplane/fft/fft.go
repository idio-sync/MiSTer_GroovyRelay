// Package fft provides power-of-two radix-2 real-input FFTs for the
// audio-scope DSP path. Private to internal/dataplane.
//
// A Plan holds the precomputed twiddles, bit-reversal table, and Hann
// window for one transform size. The package-level Real1024/Hann1024
// helpers wrap a cached 1024-point plan; the audio meter builds its own
// plan via NewPlan for larger sizes (finer low-frequency resolution).
package fft

import "math"

// N is the legacy fixed FFT size for the Real1024/Hann1024 helpers.
const N = 1024

// Plan holds the precomputed coefficients for one power-of-two FFT size.
// Construct with NewPlan.
//
// Real reuses an internal work buffer, so a single Plan is NOT safe for
// concurrent transforms — give each goroutine its own Plan (the audio
// meter owns one per session on its single producer goroutine). Hann is
// safe to call concurrently. The twiddle/bit-reversal/window tables are
// read-only after construction.
type Plan struct {
	n        int
	bits     int
	twiddles []complex64
	bitRev   []int
	hann     []float32
	work     []complex64 // reused by Real; not concurrency-safe
}

// NewPlan builds a Plan for the given FFT size, which must be a power of
// two ≥ 2. Panics otherwise.
func NewPlan(n int) *Plan {
	if n < 2 || n&(n-1) != 0 {
		panic("fft.NewPlan: size must be a power of two ≥ 2")
	}
	p := &Plan{
		n:        n,
		twiddles: make([]complex64, n),
		bitRev:   make([]int, n),
		hann:     make([]float32, n),
		work:     make([]complex64, n),
	}
	for bits := 0; (1 << bits) < n; bits++ {
		p.bits = bits + 1
	}
	// Twiddle factors for forward FFT: w_k = exp(-2πi*k/N), k=0..N-1.
	for k := 0; k < n; k++ {
		theta := -2 * math.Pi * float64(k) / float64(n)
		p.twiddles[k] = complex(float32(math.Cos(theta)), float32(math.Sin(theta)))
	}
	// Bit-reversal table.
	for i := 0; i < n; i++ {
		r := 0
		x := i
		for b := 0; b < p.bits; b++ {
			r = (r << 1) | (x & 1)
			x >>= 1
		}
		p.bitRev[i] = r
	}
	// Hann window of length N.
	for k := 0; k < n; k++ {
		p.hann[k] = float32(0.5 * (1 - math.Cos(2*math.Pi*float64(k)/float64(n-1))))
	}
	return p
}

// Size returns the FFT size this plan transforms.
func (p *Plan) Size() int { return p.n }

// Hann returns a fresh copy of the plan's Hann window. Callers may mutate
// the returned slice; subsequent calls return new copies.
func (p *Plan) Hann() []float32 {
	out := make([]float32, p.n)
	copy(out, p.hann)
	return out
}

// Real computes the DFT of an n-sample real input, where n == p.Size().
// Returns n/2+1 complex bins: DC, the positive frequencies, and Nyquist.
// If out has capacity ≥ n/2+1 it is reused (re-sliced); otherwise a fresh
// slice is allocated. Panics if len(in) != p.Size().
func (p *Plan) Real(in []float32, out []complex64) []complex64 {
	n := p.n
	if len(in) != n {
		panic("fft.Plan.Real: input length must equal plan size")
	}
	if cap(out) < n/2+1 {
		out = make([]complex64, n/2+1)
	} else {
		out = out[:n/2+1]
	}

	// Bit-reversal: place real input into the reusable work buffer at
	// reversed indices. (Not concurrency-safe — see Plan doc.)
	work := p.work
	for i := 0; i < n; i++ {
		work[p.bitRev[i]] = complex(in[i], 0)
	}

	// Cooley-Tukey decimation-in-time butterflies.
	for size := 2; size <= n; size *= 2 {
		half := size / 2
		step := n / size
		for i := 0; i < n; i += size {
			for j := 0; j < half; j++ {
				k := i + j
				t := p.twiddles[j*step] * work[k+half]
				work[k+half] = work[k] - t
				work[k] = work[k] + t
			}
		}
	}

	copy(out, work[:n/2+1])
	return out
}

// plan1024 backs the legacy fixed-size helpers.
var plan1024 = NewPlan(N)

// Hann1024 returns a fresh copy of the precomputed Hann window of length 1024.
// Callers may mutate the returned slice; subsequent calls return new copies.
func Hann1024() []float32 { return plan1024.Hann() }

// Real1024 computes the 1024-point DFT of a 1024-sample real input.
// Returns 513 complex bins: DC, 511 positive frequencies, Nyquist.
// If out has capacity ≥ 513 it is reused (and re-sliced to length 513);
// otherwise a fresh slice is allocated. Panics if len(in) != 1024.
func Real1024(in []float32, out []complex64) []complex64 { return plan1024.Real(in, out) }
