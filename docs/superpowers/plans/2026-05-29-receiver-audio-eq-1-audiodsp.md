# Receiver Audio Tone/EQ Strip — Plan 1: `audiodsp` Package + Dataplane Integration

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a self-contained Go audio-DSP engine (`internal/dataplane/audiodsp`) — biquad design, a 16-slot filter chain, mono/balance, and a click-free `Processor` with crossfade — and wire it into the data plane's existing PCM send path so tone/EQ hot-swap like the volume knob.

**Architecture:** A new leaf package `internal/dataplane/audiodsp` owns all biquad math and per-chunk apply. The existing unexported biquad primitives in `audiometer.go` (used by the LUFS K-weighting) move into `audiodsp` and `audiometer.go` is updated to call the shared package — `audiodsp` must NOT import `dataplane`. The `Plane` gains an `atomic.Pointer[audiodsp.Coeffs]` (recomputed off the audio path on each `SetAudioDSP`) plus a `Processor` owned by the send goroutine; `sendAudio` runs the processor when DSP is active and falls back to the existing `scalePCMVolumeInPlace` when fully transparent (bit-identical legacy behavior).

**Tech Stack:** Go 1.26 stdlib (`math`, `encoding/binary`, `sync/atomic`), RBJ Audio EQ Cookbook biquad forms.

**Spec reference:** [`docs/superpowers/specs/2026-05-29-receiver-audio-tone-eq-strip-design.md`](../specs/2026-05-29-receiver-audio-tone-eq-strip-design.md) — §Architecture, §DSP specifics.

**Plan set:** This is **Plan 1 of 3**. Plan 2 covers config + core preview/commit + the uiserver saver; Plan 3 covers the chassis strip UI + volume relocation. This plan leaves the engine fully unit-tested and wired into the plane, transparent by default (no behavior change until Plan 2 feeds it real params).

---

## Naming note (resolves a spec ambiguity)

The spec's export list abbreviates. This plan uses:
- **`audiodsp.Biquad`** — one biquad's normalized coefficients (`B0,B1,B2,A1,A2`). The `Design*` functions return this.
- **`audiodsp.BiquadState`** — one biquad's coefficients + Direct-Form-I history (`x1,x2,y1,y2`), with `Process`.
- **`audiodsp.Coeffs`** — the whole-chain immutable set held in `atomic.Pointer[audiodsp.Coeffs]`: the 16 slot `Biquad`s + mono flag + balance gains + `Transparent`/`Engaged` flags + the source `Params`.
- **`audiodsp.Params`** — the plain runtime parameters (dB values + toggles + sample rate/channels).
- **`audiodsp.Processor`** — owns per-channel `BiquadState` + transition state; applies a chunk.

---

## File Structure

**Created files (all in the new leaf package `internal/dataplane/audiodsp/`):**

- `biquad.go` — `Biquad`, `BiquadState`, `Unity()`. ~45 lines.
- `biquad_test.go` — identity + IIR-step tests.
- `design.go` — `DesignHighShelf`, `DesignHighpass`, `DesignLowShelf`, `DesignPeaking`. ~90 lines.
- `design_test.go` — per-filter measured-gain tests.
- `params.go` — `Params`, slot indices + band centers, `Design(Params) Coeffs`, `Coeffs` + `Transparent`/`Engaged`. ~150 lines.
- `params_test.go` — chain-build, defeat, Nyquist, balance, engaged tests.
- `processor.go` — `Processor`, `NewProcessor`, `Process`, `Active`, classification + crossfade. ~180 lines.
- `processor_test.go` — transparency, shaping, mono, balance, click-free, ramp-supersede, clamp.

**Modified files:**

- `internal/dataplane/audiometer.go` — remove the local `biquadCoeffs`/`biquadState`/`designShelfHigh`/`designHighpass`; use `audiodsp.Biquad`/`BiquadState`/`DesignHighShelf`/`DesignHighpass`. Behavior unchanged.
- `internal/dataplane/plane.go` — `PlaneConfig.AudioDSP audiodsp.Params`; `Plane` gains `audioDSP atomic.Pointer[audiodsp.Coeffs]` + `audioDSPProc *audiodsp.Processor`; init in `NewPlane`; `sendAudio` integration; new `Plane.SetAudioDSP`.

**Intentionally unchanged:** everything outside `internal/dataplane/`. Plan 2 wires config/core; Plan 3 wires the UI.

---

## Task 1: `audiodsp.Biquad` + `BiquadState` (port the primitives)

**Files:**
- Create: `internal/dataplane/audiodsp/biquad.go`
- Create: `internal/dataplane/audiodsp/biquad_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/dataplane/audiodsp/biquad_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dataplane/audiodsp/ -run TestBiquad -v`
Expected: FAIL — package/`BiquadState`/`Unity` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/dataplane/audiodsp/biquad.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/dataplane/audiodsp/ -run TestBiquad -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/audiodsp/biquad.go internal/dataplane/audiodsp/biquad_test.go
git commit -m "feat(audiodsp): add Biquad + BiquadState primitives"
```

---

## Task 2: Biquad design functions (RBJ cookbook)

**Files:**
- Create: `internal/dataplane/audiodsp/design.go`
- Create: `internal/dataplane/audiodsp/design_test.go`

- [ ] **Step 1: Write the failing test**

The test drives a pure sine at a filter's center frequency through the
designed biquad and measures the steady-state RMS gain in dB, asserting it
matches the requested gain within tolerance. A 0 dB design must be ~unity.

Create `internal/dataplane/audiodsp/design_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dataplane/audiodsp/ -run TestDesign -v`
Expected: FAIL — `DesignPeaking` etc. undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/dataplane/audiodsp/design.go`. (`DesignHighShelf`/`DesignHighpass`
are the existing `audiometer.go` formulas, renamed/exported; `DesignLowShelf`/
`DesignPeaking` are the standard RBJ forms.)

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/dataplane/audiodsp/ -run TestDesign -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/audiodsp/design.go internal/dataplane/audiodsp/design_test.go
git commit -m "feat(audiodsp): add RBJ biquad design functions"
```

---

## Task 3: Move the meter's K-weighting onto `audiodsp`

This removes the duplicate primitives from `audiometer.go` so there is one
biquad implementation. Behavior must not change — the existing audiometer
tests are the guard.

**Files:**
- Modify: `internal/dataplane/audiometer.go`

- [ ] **Step 1: Replace the local biquad types + design with `audiodsp`**

In `internal/dataplane/audiometer.go`:

1. Add the import `"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/audiodsp"`.
2. **Delete** the local `biquadCoeffs` and `biquadState` type declarations and their `setCoeffs`/`process` methods (currently ~lines 120–139).
3. **Delete** the local `designShelfHigh` and `designHighpass` functions (currently ~lines 454–493).
4. Change `kWeightingPreFilter` and `kWeightingHighShelf` to return `audiodsp.Biquad` and call the shared designers:

```go
func kWeightingPreFilter(sampleRate int) audiodsp.Biquad {
	const (
		f0     = 1681.974450955533
		q      = 0.7071752369554196
		gainDB = 3.999843853973347
	)
	return audiodsp.DesignHighShelf(float64(sampleRate), f0, q, gainDB)
}

func kWeightingHighShelf(sampleRate int) audiodsp.Biquad {
	const (
		f0 = 38.13547087602444
		q  = 0.5003270373238773
	)
	return audiodsp.DesignHighpass(float64(sampleRate), f0, q)
}
```

5. Update the struct fields and their uses to the exported types:
   - The `AudioMeter` field holding `kPreCoeffs`/`kHighCoeffs` becomes `audiodsp.Biquad`.
   - The state fields `kPreL, kPreR, kHighL, kHighR` become `audiodsp.BiquadState`.
   - `setCoeffs(...)` call sites become `.SetCoeffs(...)`; `.process(...)` call sites become `.Process(...)`.

Use the compiler to find every site:

```bash
go build ./internal/dataplane/ 2>&1 | head -40
```

Fix each reported reference (mechanical: `setCoeffs`→`SetCoeffs`, `process`→`Process`, `biquadCoeffs`→`audiodsp.Biquad`, `biquadState`→`audiodsp.BiquadState`).

- [ ] **Step 2: Verify it builds**

Run: `go build ./internal/dataplane/...`
Expected: builds clean, no references to the deleted local symbols remain:

```bash
grep -n "biquadCoeffs\|biquadState\|designShelfHigh\|designHighpass" internal/dataplane/audiometer.go
```
Expected: no matches.

- [ ] **Step 3: Run the existing meter tests (behavior unchanged)**

Run: `go test ./internal/dataplane/ -run 'Audio|Meter|LUFS|Loudness' -v`
Expected: PASS — the K-weighting/LUFS values are unchanged because the
formulas are identical, only relocated.

- [ ] **Step 4: Full dataplane test sweep**

Run: `go test ./internal/dataplane/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/audiometer.go
git commit -m "refactor(dataplane): K-weighting uses the shared audiodsp biquads"
```

---

## Task 4: `Params` + `Design` — the 16-slot chain

**Files:**
- Create: `internal/dataplane/audiodsp/params.go`
- Create: `internal/dataplane/audiodsp/params_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/dataplane/audiodsp/params_test.go`:

```go
package audiodsp

import "testing"

func flatStereo() Params {
	return Params{Enabled: true, SampleRate: 48000, Channels: 2}
}

func TestDesign_FlatIsTransparent(t *testing.T) {
	t.Parallel()
	c := Design(flatStereo())
	if !c.Transparent {
		t.Error("flat enabled DSP should be Transparent")
	}
	if c.Engaged {
		t.Error("flat DSP should not be Engaged")
	}
	for i, s := range c.Slots {
		if s != Unity() {
			t.Errorf("slot %d = %+v, want Unity", i, s)
		}
	}
}

func TestDesign_BassEngagesAndShapes(t *testing.T) {
	t.Parallel()
	p := flatStereo()
	p.Bass = 6
	c := Design(p)
	if c.Transparent {
		t.Error("bass +6 should not be Transparent")
	}
	if !c.Engaged {
		t.Error("bass +6 should be Engaged")
	}
	if c.Slots[SlotBass] == Unity() {
		t.Error("bass slot should be shaped")
	}
}

func TestDesign_DefeatBypassesShapingButKeepsBalance(t *testing.T) {
	t.Parallel()
	p := flatStereo()
	p.Enabled = false // defeat
	p.Bass = 6
	p.Balance = -100
	c := Design(p)
	if c.Engaged {
		t.Error("defeated DSP must not be Engaged")
	}
	if c.Slots[SlotBass] != Unity() {
		t.Error("defeat must make the bass slot unity")
	}
	if c.BalL != 1.0 || c.BalR != 0.0 {
		t.Errorf("balance still applies under defeat: L=%v R=%v want 1,0", c.BalL, c.BalR)
	}
	if c.Transparent {
		t.Error("defeat with balance off-center is not transparent")
	}
}

func TestDesign_NyquistSlotsGoUnity(t *testing.T) {
	t.Parallel()
	p := Params{Enabled: true, SampleRate: 22050, Channels: 2, Loudness: true}
	p.EQ[9] = 6 // 16 kHz band, above Nyquist (11025)
	c := Design(p)
	if c.Slots[SlotEQ0+9] != Unity() {
		t.Error("16 kHz EQ band must be unity at 22050 Hz")
	}
	if c.Slots[SlotLoudHi] != Unity() {
		t.Error("12 kHz loudness shelf must be unity at 22050 Hz")
	}
	// 8 kHz band (index 8) is below Nyquist and may be shaped if requested.
	// LED still reflects the requested intent.
	if !c.Engaged {
		t.Error("requested loudness + EQ should light the LED even if a slot is Nyquist-clamped")
	}
}

func TestDesign_BalanceAttenuateOnly(t *testing.T) {
	t.Parallel()
	cases := []struct{ bal int; l, r float64 }{
		{0, 1, 1}, {-100, 1, 0}, {100, 0, 1}, {-50, 1, 0.5}, {50, 0.5, 1},
	}
	for _, tc := range cases {
		p := flatStereo()
		p.Balance = tc.bal
		c := Design(p)
		if c.BalL != tc.l || c.BalR != tc.r {
			t.Errorf("balance %d: L=%v R=%v, want %v,%v", tc.bal, c.BalL, c.BalR, tc.l, tc.r)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dataplane/audiodsp/ -run TestDesign_ -v`
Expected: FAIL — `Params`, `Design`, `SlotBass`, etc. undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/dataplane/audiodsp/params.go`:

```go
package audiodsp

// Slot indices into Coeffs.Slots (fixed chain order).
const (
	SlotSubsonic = iota
	SlotBass
	SlotMid
	SlotTreble
	SlotEQ0 // SlotEQ0+i for band i in 0..9
	_       // EQ1
	_       // EQ2
	_       // EQ3
	_       // EQ4
	_       // EQ5
	_       // EQ6
	_       // EQ7
	_       // EQ8
	_       // EQ9
	SlotLoudLo = SlotEQ0 + 10   // 14
	SlotLoudHi = SlotLoudLo + 1 // 15 (chain order: ...EQ9, loud-lo, loud-hi)
	NumSlots   = SlotLoudHi + 1 // 16
)

// eqCenters are the 10 ISO-octave band centers (Hz), index-aligned to EQ[i].
var eqCenters = [10]float64{31.25, 62.5, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

const (
	subsonicHz = 22.0
	bassHz     = 100.0
	midHz      = 1000.0
	trebleHz   = 10000.0
	loudLoHz   = 100.0
	loudHiHz   = 12000.0
	loudLoDB   = 6.0
	loudHiDB   = 4.0
	eqQ        = 1.4
)

// Params is the plain runtime parameter set the data plane feeds in. Gains
// are dB; Balance is -100..+100; EQ is index-aligned to eqCenters.
type Params struct {
	Enabled    bool
	Mono       bool
	Subsonic   bool
	Loudness   bool
	Bass       float64
	Mid        float64
	Treble     float64
	Balance    int
	EQ         [10]float64
	SampleRate int
	Channels   int
}

// Coeffs is the immutable precomputed chain held behind an atomic.Pointer.
type Coeffs struct {
	Slots       [NumSlots]Biquad
	Mono        bool
	BalL, BalR  float64
	Transparent bool // fully bit-identical to volume-only path (fast path)
	Engaged     bool // status LED: shaping requested (independent of mono/balance)
	Src         Params
}

// Design builds the chain from params, applying Nyquist safety and the
// defeat/transparency/engaged rules.
func Design(p Params) Coeffs {
	c := Coeffs{Src: p}
	for i := range c.Slots {
		c.Slots[i] = Unity()
	}
	fs := float64(p.SampleRate)
	nyquist := fs / 2

	shape := p.Enabled // when disabled (defeat), all frequency slots stay unity
	peakOK := func(hz float64) bool { return hz < nyquist }

	if shape {
		if p.Subsonic && peakOK(subsonicHz) {
			c.Slots[SlotSubsonic] = DesignHighpass(fs, subsonicHz, 0.707)
		}
		if p.Bass != 0 && peakOK(bassHz) {
			c.Slots[SlotBass] = DesignLowShelf(fs, bassHz, 0.707, p.Bass)
		}
		if p.Mid != 0 && peakOK(midHz) {
			c.Slots[SlotMid] = DesignPeaking(fs, midHz, 0.7, p.Mid)
		}
		if p.Treble != 0 && peakOK(trebleHz) {
			c.Slots[SlotTreble] = DesignHighShelf(fs, trebleHz, 0.707, p.Treble)
		}
		for i, g := range p.EQ {
			if g != 0 && peakOK(eqCenters[i]) {
				c.Slots[SlotEQ0+i] = DesignPeaking(fs, eqCenters[i], eqQ, g)
			}
		}
		if p.Loudness {
			if peakOK(loudLoHz) {
				c.Slots[SlotLoudLo] = DesignLowShelf(fs, loudLoHz, 0.707, loudLoDB)
			}
			if peakOK(loudHiHz) {
				c.Slots[SlotLoudHi] = DesignHighShelf(fs, loudHiHz, 0.707, loudHiDB)
			}
		}
	}

	c.Mono = p.Mono && p.Channels == 2
	c.BalL, c.BalR = balanceGains(p)

	c.Engaged = p.Enabled && shapingRequested(p)
	c.Transparent = !c.Mono && c.BalL == 1.0 && c.BalR == 1.0 && !c.Engaged
	return c
}

func shapingRequested(p Params) bool {
	if p.Subsonic || p.Loudness || p.Bass != 0 || p.Mid != 0 || p.Treble != 0 {
		return true
	}
	for _, g := range p.EQ {
		if g != 0 {
			return true
		}
	}
	return false
}

// balanceGains implements the attenuate-only law. On mono or a mono source
// balance is forced to center.
func balanceGains(p Params) (l, r float64) {
	if p.Mono || p.Channels != 2 {
		return 1, 1
	}
	b := p.Balance
	if b < -100 {
		b = -100
	}
	if b > 100 {
		b = 100
	}
	l, r = 1, 1
	if b > 0 {
		l = 1 - float64(b)/100
	} else if b < 0 {
		r = 1 + float64(b)/100
	}
	return l, r
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/dataplane/audiodsp/ -run TestDesign_ -v`
Expected: PASS (all five).

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/audiodsp/params.go internal/dataplane/audiodsp/params_test.go
git commit -m "feat(audiodsp): add Params + Design (16-slot chain, Nyquist, balance, defeat)"
```

---

## Task 5: `Processor` — apply + click-free transitions

**Files:**
- Create: `internal/dataplane/audiodsp/processor.go`
- Create: `internal/dataplane/audiodsp/processor_test.go`

`Process` converts int16→float, applies (mono → cascade → balance → volume →
saturating clamp), and manages click-free transitions. Classification:
- **Incremental** — same topology (Enabled/Mono/Subsonic/Loudness and the set
  of unity slots unchanged) and every gain delta ≤ 1 dB → swap coeffs, keep state.
- **Hard** — otherwise → crossfade old vs new cascade over `rampSamples`,
  seeding the new state from a copy of the old. A new hard target mid-ramp
  restarts the ramp from the current crossfaded output state.

- [ ] **Step 1: Write the failing test**

Create `internal/dataplane/audiodsp/processor_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dataplane/audiodsp/ -run TestProcessor -v`
Expected: FAIL — `NewProcessor`/`Process`/`Active` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/dataplane/audiodsp/processor.go`:

```go
package audiodsp

import (
	"encoding/binary"
	"math"
)

const (
	bytesPerSample = 2
	rampSamples    = 480 // ~10 ms at 48 kHz; crossfade window for hard changes
	hardGainDelta  = 1.0 // dB; > this on any slot is a hard change
)

// Processor applies a Coeffs chain to interleaved s16le PCM and owns the
// click-free transition between coefficient generations. Single-goroutine:
// the audio-send goroutine is the only caller.
type Processor struct {
	channels int

	cur    *Coeffs
	curSt  [][NumSlots]BiquadState // [channel][slot]
	fromSt [][NumSlots]BiquadState // old cascade during a ramp
	ramp   int                     // samples remaining in the active crossfade (0 = none)
}

func NewProcessor(channels int) *Processor {
	return &Processor{
		channels: channels,
		curSt:    make([][NumSlots]BiquadState, channels),
		fromSt:   make([][NumSlots]BiquadState, channels),
	}
}

// Active reports whether the processor is doing non-trivial work (shaping,
// mono, balance, or mid-ramp) — i.e. the float path is required.
func (p *Processor) Active() bool {
	return p.ramp > 0 || (p.cur != nil && !p.cur.Transparent)
}

// Process applies target to pcm in place. target is the latest atomically
// published chain; the processor diffs it against its current generation to
// classify the transition.
func (p *Processor) Process(pcm []byte, target *Coeffs, volume int) {
	if target == nil {
		return
	}
	if p.cur == nil {
		p.adopt(target)
	} else if target != p.cur {
		p.transition(target)
	}
	g := float64(clampVol(volume)) / 100

	frames := len(pcm) / (bytesPerSample * p.channels)
	for n := 0; n < frames; n++ {
		var s [2]float64
		for ch := 0; ch < p.channels; ch++ {
			s[ch] = sampleToFloat(pcm, (n*p.channels+ch)*bytesPerSample)
		}
		if p.channels == 2 && p.cur.Mono {
			m := (s[0] + s[1]) * 0.5
			s[0], s[1] = m, m
		}
		var t float64
		if p.ramp > 0 {
			t = 1 - float64(p.ramp)/float64(rampSamples)
		}
		for ch := 0; ch < p.channels; ch++ {
			y := p.cascade(&p.curSt[ch], p.cur, s[ch])
			if p.ramp > 0 {
				yOld := p.cascade(&p.fromSt[ch], p.curFrom(), s[ch])
				y = yOld*(1-t) + y*t
			}
			// balance (skip on mono / mono source)
			if ch == 0 {
				y *= p.cur.BalL
			} else {
				y *= p.cur.BalR
			}
			y *= g
			floatToSample(pcm, (n*p.channels+ch)*bytesPerSample, y)
		}
		if p.ramp > 0 {
			p.ramp--
		}
	}
}

// curFrom returns the coeffs the from-state cascade should run. During a
// ramp the old cascade keeps running the previous chain; we stash it in
// fromCoeffs.
func (p *Processor) curFrom() *Coeffs { return p.fromCoeffs }

func (p *Processor) cascade(st *[NumSlots]BiquadState, c *Coeffs, x float64) float64 {
	for i := range st {
		st[i].SetCoeffs(c.Slots[i])
		x = st[i].Process(x)
	}
	return x
}

func (p *Processor) adopt(c *Coeffs) {
	p.cur = c
	p.ramp = 0
	for ch := 0; ch < p.channels; ch++ {
		for i := range p.curSt[ch] {
			p.curSt[ch][i].SetCoeffs(c.Slots[i])
		}
	}
}

func (p *Processor) transition(next *Coeffs) {
	if p.isIncremental(p.cur, next) {
		// keep state, just swap target coeffs
		p.cur = next
		p.ramp = 0
		return
	}
	// hard change: seed from-state from current state, start a ramp.
	p.fromCoeffs = p.cur
	for ch := 0; ch < p.channels; ch++ {
		p.fromSt[ch] = p.curSt[ch] // value copy of the old per-slot state
	}
	p.cur = next
	p.ramp = rampSamples
}

func (p *Processor) isIncremental(a, b *Coeffs) bool {
	pa, pb := a.Src, b.Src
	if pa.Enabled != pb.Enabled || pa.Mono != pb.Mono ||
		pa.Subsonic != pb.Subsonic || pa.Loudness != pb.Loudness {
		return false
	}
	if (pa.Balance == 0) != (pb.Balance == 0) {
		return false
	}
	if math.Abs(pa.Bass-pb.Bass) > hardGainDelta ||
		math.Abs(pa.Mid-pb.Mid) > hardGainDelta ||
		math.Abs(pa.Treble-pb.Treble) > hardGainDelta {
		return false
	}
	for i := range pa.EQ {
		if math.Abs(pa.EQ[i]-pb.EQ[i]) > hardGainDelta {
			return false
		}
	}
	return true
}

func sampleToFloat(pcm []byte, off int) float64 {
	return float64(int16(binary.LittleEndian.Uint16(pcm[off:off+2]))) / 32768
}

func floatToSample(pcm []byte, off int, v float64) {
	s := v * 32768
	if s > 32767 {
		s = 32767
	} else if s < -32768 {
		s = -32768
	}
	binary.LittleEndian.PutUint16(pcm[off:off+2], uint16(int16(s)))
}

func clampVol(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
```

Add the `fromCoeffs *Coeffs` field to the `Processor` struct (used by
`curFrom`/`transition`):

```go
type Processor struct {
	channels   int
	cur        *Coeffs
	fromCoeffs *Coeffs
	curSt      [][NumSlots]BiquadState
	fromSt     [][NumSlots]BiquadState
	ramp       int
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/dataplane/audiodsp/ -v`
Expected: PASS (all processor + earlier tests).

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/audiodsp/processor.go internal/dataplane/audiodsp/processor_test.go
git commit -m "feat(audiodsp): add Processor with click-free crossfade transitions"
```

---

## Task 6: Wire `audiodsp` into the `Plane`

**Files:**
- Modify: `internal/dataplane/plane.go`
- Test: `internal/dataplane/plane_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/dataplane/plane_test.go`:

```go
func TestPlane_SetAudioDSPShapesAudio(t *testing.T) {
	t.Parallel()
	p := &Plane{cfg: PlaneConfig{AudioChans: 2, AudioRate: 48000}}
	p.initAudioDSP() // helper added in Step 3
	// Flat default: sendAudio path must be transparent (handled in Step 3's
	// integration; here we assert SetAudioDSP swaps the published coeffs).
	if err := p.SetAudioDSP(audiodsp.Params{Enabled: true, Bass: 6, SampleRate: 48000, Channels: 2}); err != nil {
		t.Fatalf("SetAudioDSP: %v", err)
	}
	c := p.audioDSP.Load()
	if c == nil || !c.Engaged {
		t.Fatalf("expected engaged coeffs after SetAudioDSP, got %+v", c)
	}
}
```

Add the import `"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/audiodsp"` to `plane_test.go` if absent.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dataplane/ -run TestPlane_SetAudioDSP -v`
Expected: FAIL — `PlaneConfig.AudioDSP`/`p.audioDSP`/`SetAudioDSP`/`initAudioDSP` undefined.

- [ ] **Step 3: Implement the wiring**

In `internal/dataplane/plane.go`:

1. Add the import `"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/audiodsp"`.
2. Add to `PlaneConfig` (next to `OutputVolume int`):

```go
	AudioDSP audiodsp.Params // tone/EQ chain; zero value = transparent
```

3. Add to the `Plane` struct (next to `outputVolume atomic.Int32`):

```go
	audioDSP     atomic.Pointer[audiodsp.Coeffs]
	audioDSPProc *audiodsp.Processor // owned by the send goroutine
```

4. Add an init helper and call it from `NewPlane` (right after
   `p.outputVolume.Store(...)` at ~line 453):

```go
func (p *Plane) initAudioDSP() {
	chans := p.cfg.AudioChans
	if chans <= 0 {
		chans = 2
	}
	params := p.cfg.AudioDSP
	if params.SampleRate == 0 {
		params.SampleRate = p.cfg.AudioRate
	}
	if params.Channels == 0 {
		params.Channels = chans
	}
	c := audiodsp.Design(params)
	p.audioDSP.Store(&c)
	p.audioDSPProc = audiodsp.NewProcessor(chans)
}
```

In `NewPlane`, after `p.outputVolume.Store(int32(clampOutputVolume(cfg.OutputVolume)))`:

```go
	p.initAudioDSP()
```

5. Replace the volume scaling in `sendAudio` (currently the single line
   `scalePCMVolumeInPlace(pcm, int(p.outputVolume.Load()))` at ~line 1436):

```go
	if c := p.audioDSP.Load(); c != nil && (p.audioDSPProc.Active() || !c.Transparent) {
		p.audioDSPProc.Process(pcm, c, int(p.outputVolume.Load()))
	} else {
		scalePCMVolumeInPlace(pcm, int(p.outputVolume.Load()))
	}
```

6. Add the live setter next to `SetOutputVolume` (~line 515):

```go
// SetAudioDSP recomputes the tone/EQ coefficient chain off the audio path and
// atomically publishes it. The send goroutine's Processor picks it up on its
// next chunk and crossfades hard changes. Safe to call concurrently with Run.
func (p *Plane) SetAudioDSP(params audiodsp.Params) error {
	if params.SampleRate == 0 {
		params.SampleRate = p.cfg.AudioRate
	}
	if params.Channels == 0 {
		params.Channels = p.cfg.AudioChans
	}
	c := audiodsp.Design(params)
	p.audioDSP.Store(&c)
	return nil
}
```

Note: `initAudioDSP` must be safe to call from the test on a bare `&Plane{cfg:...}` (it is — it only reads `cfg` and sets the two fields).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dataplane/ -run 'TestPlane_SetAudioDSP|TestSendAudioAppliesOutputVolume' -v`
Expected: PASS — the new test passes AND the existing
`TestSendAudioAppliesOutputVolume` still passes (flat default → `Transparent`
→ `scalePCMVolumeInPlace` path, unchanged).

- [ ] **Step 5: Full dataplane sweep + commit**

Run: `go test ./internal/dataplane/...`
Expected: PASS.

```bash
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "feat(dataplane): wire audiodsp Processor into the PCM send path"
```

---

## Task 7: Phase-1 verification gate

**Files:** none (verification only).

- [ ] **Step 1: vet + unit tests**

Run: `make lint && make test`
Expected: clean `go vet`; all unit tests pass.

- [ ] **Step 2: race (CI parity — runs in CI; locally only if cgo/gcc available)**

Run: `go test -race ./internal/dataplane/...`
Expected: PASS, no data-race reports. (If gcc is unavailable locally, rely on
CI for the race gate per the project's race-is-CI-only constraint.)

- [ ] **Step 3: confirm transparency / no-regression**

Run: `go test ./internal/dataplane/ -run 'Audio|Meter|LUFS|SendAudio' -v`
Expected: PASS — the meter/LUFS values and the volume-only audio path are
unchanged; the DSP engine is dormant (transparent) until Plan 2 feeds params.

- [ ] **Step 4: confirm the leaf-package boundary**

```bash
go list -deps ./internal/dataplane/audiodsp | grep 'internal/dataplane$' && echo "CYCLE!" || echo "ok: audiodsp does not import dataplane"
```
Expected: `ok: audiodsp does not import dataplane`.

---

## Self-review (run before handing off)

- **Spec coverage (this plan's slice):** `audiodsp` package with biquad
  design, 16-slot chain, Nyquist safety, balance attenuate-only law, defeat,
  transparent/engaged flags, click-free Processor with crossfade — all
  present (Tasks 1–5). Dataplane integration + `SetAudioDSP` + transparent
  bypass — present (Task 6). K-weighting move — Task 3.
- **Deferred to later plans (intentionally):** `config.AudioDSP`,
  `Manager.SetAudioDSP`/`PreviewAudioDSP`, `planeRunner.SetAudioDSP`,
  `StatusHomeView` fields, `BridgeSaver.SaveAudioDSP`, the chassis strip.
- **Type consistency:** `Biquad`/`BiquadState`/`Coeffs`/`Params`/`Processor`,
  slot consts, and `Design`/`Process`/`SetAudioDSP` signatures are used
  identically across tasks.
