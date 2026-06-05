package audiodsp

import "math"

// DesignPeaking designs a peaking-EQ biquad (RBJ Cookbook).
func DesignPeaking(sampleRate, f0, q, gainDB float64) Biquad {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / sampleRate
	cosW := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	b0 := 1 + alpha*A
	b1 := -2 * cosW
	b2 := 1 - alpha*A
	a0 := 1 + alpha/A
	a1 := -2 * cosW
	a2 := 1 - alpha/A
	return Biquad{B0: b0 / a0, B1: b1 / a0, B2: b2 / a0, A1: a1 / a0, A2: a2 / a0}
}

// DesignLowShelf designs a low-frequency shelving biquad (RBJ Cookbook).
func DesignLowShelf(sampleRate, f0, q, gainDB float64) Biquad {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / sampleRate
	cosW := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	sqrtA := math.Sqrt(A)
	b0 := A * ((A + 1) - (A-1)*cosW + 2*sqrtA*alpha)
	b1 := 2 * A * ((A - 1) - (A+1)*cosW)
	b2 := A * ((A + 1) - (A-1)*cosW - 2*sqrtA*alpha)
	a0 := (A + 1) + (A-1)*cosW + 2*sqrtA*alpha
	a1 := -2 * ((A - 1) + (A+1)*cosW)
	a2 := (A + 1) + (A-1)*cosW - 2*sqrtA*alpha
	return Biquad{B0: b0 / a0, B1: b1 / a0, B2: b2 / a0, A1: a1 / a0, A2: a2 / a0}
}

// DesignHighShelf designs a high-frequency shelving biquad (RBJ Cookbook).
func DesignHighShelf(sampleRate, f0, q, gainDB float64) Biquad {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / sampleRate
	cosW := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	sqrtA := math.Sqrt(A)
	b0 := A * ((A + 1) + (A-1)*cosW + 2*sqrtA*alpha)
	b1 := -2 * A * ((A - 1) + (A+1)*cosW)
	b2 := A * ((A + 1) + (A-1)*cosW - 2*sqrtA*alpha)
	a0 := (A + 1) - (A-1)*cosW + 2*sqrtA*alpha
	a1 := 2 * ((A - 1) - (A+1)*cosW)
	a2 := (A + 1) - (A-1)*cosW - 2*sqrtA*alpha
	return Biquad{B0: b0 / a0, B1: b1 / a0, B2: b2 / a0, A1: a1 / a0, A2: a2 / a0}
}

// DesignHighpass designs a 2nd-order high-pass biquad (RBJ Cookbook).
func DesignHighpass(sampleRate, f0, q float64) Biquad {
	w0 := 2 * math.Pi * f0 / sampleRate
	cosW := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	b0 := (1 + cosW) / 2
	b1 := -(1 + cosW)
	b2 := (1 + cosW) / 2
	a0 := 1 + alpha
	a1 := -2 * cosW
	a2 := 1 - alpha
	return Biquad{B0: b0 / a0, B1: b1 / a0, B2: b2 / a0, A1: a1 / a0, A2: a2 / a0}
}
