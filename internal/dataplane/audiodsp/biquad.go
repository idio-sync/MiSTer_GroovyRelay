// Package audiodsp implements the receiver's live PCM tone/EQ chain:
// RBJ-cookbook biquad design, a fixed-slot filter chain, mono/balance,
// and a click-free Processor. It is a leaf package and must not import
// internal/dataplane (dataplane imports audiodsp, never the reverse).
package audiodsp

// Biquad holds one second-order section's coefficients, normalized so a0 = 1.
type Biquad struct {
	B0, B1, B2, A1, A2 float64
}

// Unity returns a pass-through biquad (output == input).
func Unity() Biquad { return Biquad{B0: 1} }

// BiquadState is a Biquad plus its Direct-Form-I sample history. Not safe
// for concurrent use; each channel/slot owns its own BiquadState on the
// audio goroutine.
type BiquadState struct {
	C              Biquad
	x1, x2, y1, y2 float64
}

// SetCoeffs swaps the coefficients without touching the sample history,
// so an incremental coefficient change does not reset the filter (no click).
func (b *BiquadState) SetCoeffs(c Biquad) { b.C = c }

// Reset clears the sample history.
func (b *BiquadState) Reset() { b.x1, b.x2, b.y1, b.y2 = 0, 0, 0, 0 }

// Process runs one input sample through the section (Direct Form I).
func (b *BiquadState) Process(x float64) float64 {
	y := b.C.B0*x + b.C.B1*b.x1 + b.C.B2*b.x2 - b.C.A1*b.y1 - b.C.A2*b.y2
	b.x2, b.x1 = b.x1, x
	b.y2, b.y1 = b.y1, y
	return y
}
