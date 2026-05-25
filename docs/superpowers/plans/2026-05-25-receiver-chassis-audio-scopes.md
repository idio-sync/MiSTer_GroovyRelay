# Receiver Chassis Audio-Analysis Scopes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the receiver chassis audio-analysis scopes (L/R VU, phase correlation, short-term LUFS, 32-band spectrum, 256-point goniometer) real by computing all DSP inline on the data-plane field-tick goroutine, publishing through a lock-free atomic snapshot, and streaming to the chassis at 30 Hz over a new `audio` SSE event on the existing `/receiver/events` connection.

**Architecture:** A new `AudioMeter` type lives on `*Plane` and runs DSP inline in `Observe()` (called once per PCM chunk just before `sendAudio`). It publishes `*AudioScopeSnapshot` via `atomic.Pointer` at an average 30 Hz via a Bresenham-style sample accumulator. Core exposes the snapshot via `Manager.AudioScopes() *AudioScopeSnapshot` (alias to dataplane type, no copy). Chassis adds an `AudioScopeViewer` interface, a discriminated-union envelope (`audioPendingEnvelope` | `audioLiveEnvelope`) with a custom `MarshalJSON` for float-precision and NaN/Inf clamping, and a 30 Hz audio ticker in `handleEvents` that emits the `audio` event independently of the 2 Hz snapshot cache. Folds in the Phase 1 follow-up debt: migrating `transport.js` and `visualizer-bank.js` from raw `EventSource` to the `subscribe()` helper that 5A introduces.

**Tech Stack:** Go 1.26 stdlib (`math`, `sync/atomic`, `encoding/json`, `strconv`, `time`), existing internal packages (`dataplane`, `core`, `chassis`), vanilla ES2022 (no bundler). One new internal subpackage `internal/dataplane/fft/`. No new go.mod dependencies.

**Spec:** [docs/superpowers/specs/2026-05-25-receiver-chassis-audio-scopes-design.md](../specs/2026-05-25-receiver-chassis-audio-scopes-design.md)

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/dataplane/fft/fft.go` | Radix-2 1024-pt complex FFT with real-input convenience wrapper, precomputed Hann window, no deps |
| `internal/dataplane/fft/fft_test.go` | Parseval theorem + known-frequency sine peak + Hann window energy gain tests |
| `internal/dataplane/audiometer.go` | `AudioMeter` type, DSP (peak/RMS/phase/goniometer/spectrum/LUFS), atomic snapshot, biquad filters, K-weighting coefficients |
| `internal/dataplane/audiometer_test.go` | DSP correctness, Bresenham cadence, alloc budget, concurrent-reader race test |
| `internal/chassis/audio.go` | `AudioScopeViewer` interface, `audioPendingEnvelope`, `audioLiveEnvelope`, custom `MarshalJSON`, `audioEnvelopeFromViewer`, `audioShouldEmit` |
| `internal/chassis/audio_test.go` | Envelope serialization, pending/live shapes, NaN/Inf clamping, discriminator, audioShouldEmit semantics |

**Modified files:**

| Path | Change |
|---|---|
| `internal/dataplane/plane.go` | Add `PlaneConfig.Generation`; add `audioMeter *AudioMeter` field on `Plane`; initialize in `NewPlane`; call `p.audioMeter.Observe(oldest, audioChans, audioRate)` immediately before `sendAudio(oldest)` at the existing line 1000; expose `Plane.AudioScopes() *AudioScopeSnapshot` |
| `internal/dataplane/plane_test.go` | Cover audio meter wiring through fake processHandle; verify Observe is called only inside the existing `if len(oldest) > 0` guard |
| `internal/core/types.go` | Add `type AudioScopeSnapshot = dataplane.AudioScopeSnapshot` type alias |
| `internal/core/manager.go` | Extend `planeRunner` interface with `AudioScopes() *dataplane.AudioScopeSnapshot`; add public `Manager.AudioScopes() *AudioScopeSnapshot`; pass session generation into `PlaneConfig.Generation` in `startPlaneLocked` |
| `internal/core/manager_test.go` | Widen fake plane to satisfy `AudioScopes()`; cover nil-when-idle, non-nil-when-live, nil-after-pause |
| `internal/chassis/meter.go` | Discovery hook: meter envelope's `audioScopes` field emits `{"status":"live","via":"audio","sampleHz":30}` when session active, `{"status":"pending"}` when idle. Reference `audioEventHz` constant |
| `internal/chassis/meter_test.go` | Discovery hook test: assert envelope shape transitions correctly |
| `internal/chassis/server.go` | Add `Config.AudioScopeViewer AudioScopeViewer`; add `Server.audioScopeViewer` field; store in `New()` |
| `internal/chassis/events.go` | Add `audioTickInterval = time.Second / 30`; add `audioEventHz = 30` constant; add 30 Hz audio ticker in `handleEvents` with closure-scoped panic recovery; emit initial audio frame last in burst |
| `internal/chassis/events_test.go` | Initial-burst order extension; exact-count cadence assertion; pending suppression; generation flip via pending and direct; legitimate-zero serialization; meter discovery hook; panic recovery |
| `internal/chassis/templates/meter.html` | Replace 5A pending placeholders with `data-scope-*` hooks for live data driving |
| `internal/chassis/static/meter.js` | Add `subscribe('audio', ...)` handler with discriminator gate + generation reset; canvas paint loops for spectrum and goniometer; CSS-variable drivers for VU/phase/LUFS |
| `internal/chassis/static/transport.js` | **Task 14 (gated)**: migrate from `events.source.addEventListener` + `chassis:eventsource` to `window.Chassis.events.subscribe('transport', ...)` |
| `internal/chassis/static/visualizer-bank.js` | **Task 14 (gated)**: migrate from `events.source.addEventListener` + `chassis:eventsource` to `window.Chassis.events.subscribe('visualizer', ...)` |
| `internal/chassis/static/chassis.css` | Canvas sizing, VU bar transitions, phase needle rotation transition; scoped under `body.receiver` |
| `internal/chassis/chassis_test.go` | Template-hook presence test for live audio scope DOM hooks; no-fake-values lint (no `Math.random`/`Math.sin`/etc. in `meter.js`); subscribe-pattern lint (no `events.source` or `chassis:eventsource` in `transport.js`/`visualizer-bank.js` after Task 14) |
| `cmd/mister-groovy-relay/main.go` | Add `AudioScopeViewer: coreMgr` to the `chassis.Config` literal where chassis is constructed |
| `tests/integration/chassis_test.go` | Build-tagged end-to-end SSE test: real `*core.Manager` + fake processHandle → connect SSE client → assert `audio` event arrives with `status: live`; stop session → assert next `audio` event is `{"status":"pending"}` |

**Files intentionally unchanged:**

- `internal/ui/*`, `internal/uiserver/*` — 5B is additive under `/receiver/*` only.
- `internal/playback/*` — audio meter is separate from playback controls.
- All adapter packages — DSP runs in the data plane, regardless of source.
- `internal/chassis/static/vfd-live.js` — the `subscribe()` helper lands in 5A, not 5B.
- `internal/chassis/import_check_test.go` — no new cross-package imports introduced; verify after compile, do not edit unless lint flags something.

---

## Sequencing Constraint

5B Task 14 (Phase 1 debt migration) requires that 5A's `subscribe()` helper has merged into `internal/chassis/static/vfd-live.js` first. As of this plan's authoring, **5A has NOT merged** — `vfd-live.js` exposes `window.Chassis.events.source` and dispatches `chassis:eventsource`, but no `subscribe()` function exists.

**Execution rule:** Tasks 1-13 are independent of 5A and may proceed immediately. **Task 14 has a precondition check** — its first step is to verify `window.Chassis.events.subscribe` exists in `vfd-live.js` (via grep). If not, halt Task 14 with a clear blocker message; resume after 5A lands.

This ordering avoids two failure modes: (a) Task 14 silently writing JS that calls a function that doesn't exist; (b) double-shipping `subscribe()` if 5A merges in parallel.

---

## Task 1: FFT Subpackage

**Files:**
- Create: `internal/dataplane/fft/fft.go`
- Create: `internal/dataplane/fft/fft_test.go`

- [ ] **Step 1: Write failing tests for FFT correctness**

Create `internal/dataplane/fft/fft_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/dataplane/fft -v
```

Expected: FAIL with `package fft is not in std` or similar (file doesn't exist yet).

- [ ] **Step 3: Implement the FFT**

Create `internal/dataplane/fft/fft.go`:

```go
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
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/dataplane/fft -v
```

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/fft/
git commit -m "feat(dataplane): add 1024-pt real FFT subpackage"
```

---

## Task 2: AudioMeter Skeleton — Peak/RMS/Phase/Goniometer DSP

This task wires up the AudioMeter type with the cheap DSP that does not need FFT or LUFS. Those land in Tasks 3-4.

**Files:**
- Create: `internal/dataplane/audiometer.go`
- Create: `internal/dataplane/audiometer_test.go`

- [ ] **Step 1: Write failing tests for peak/RMS/phase/goniometer DSP**

Create `internal/dataplane/audiometer_test.go`:

```go
package dataplane

import (
	"encoding/binary"
	"math"
	"testing"
)

// makeStereoPCM generates a stereo s16le PCM chunk of length frames.
// generators[ch] is called for each frame index. Returns the byte buffer.
func makeStereoPCM(frames int, generators [2]func(i int) float32) []byte {
	buf := make([]byte, frames*2*2) // frames * channels * bytesPerSample
	for i := 0; i < frames; i++ {
		for ch := 0; ch < 2; ch++ {
			v := generators[ch](i)
			if v > 1.0 {
				v = 1.0
			} else if v < -1.0 {
				v = -1.0
			}
			s := int16(v * 32767)
			binary.LittleEndian.PutUint16(buf[(i*2+ch)*2:], uint16(s))
		}
	}
	return buf
}

func TestAudioMeter_SilenceSnapshot(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000 // force publish on every Observe
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0 },
		func(i int) float32 { return 0 },
	})
	m.Observe(pcm, 2, 48000)
	snap := m.AudioScopes()
	if snap == nil {
		t.Fatal("snapshot is nil after Observe")
	}
	if snap.Peak[0] != 0 || snap.Peak[1] != 0 {
		t.Errorf("Peak = %v, want [0 0]", snap.Peak)
	}
	if snap.RMS[0] != 0 || snap.RMS[1] != 0 {
		t.Errorf("RMS = %v, want [0 0]", snap.RMS)
	}
	if snap.Generation != 1 {
		t.Errorf("Generation = %d, want 1", snap.Generation)
	}
	if snap.SampleRate != 48000 || snap.Channels != 2 {
		t.Errorf("sampleRate/channels = %d/%d, want 48000/2", snap.SampleRate, snap.Channels)
	}
}

func TestAudioMeter_FullScaleSinePeakAndRMS(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000 // force publish
	// 1 kHz full-scale sine on both channels, in phase
	sineL := func(i int) float32 { return float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)) }
	// feed ~300 ms so the RMS window fills
	for chunk := 0; chunk < 18; chunk++ { // 18 * 800 = 14400 samples ≈ 300 ms
		pcm := makeStereoPCM(800, [2]func(int) float32{sineL, sineL})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	// Peak ≈ 1.0 (within decay tolerance — peak just hit, not decayed)
	if math.Abs(float64(snap.Peak[0])-1.0) > 0.05 || math.Abs(float64(snap.Peak[1])-1.0) > 0.05 {
		t.Errorf("Peak = %v, want ~[1.0 1.0]", snap.Peak)
	}
	// RMS ≈ 0.707
	if math.Abs(float64(snap.RMS[0])-0.707) > 0.02 || math.Abs(float64(snap.RMS[1])-0.707) > 0.02 {
		t.Errorf("RMS = %v, want ~[0.707 0.707]", snap.RMS)
	}
	// In-phase L/R → phase correlation ≈ +1
	if math.Abs(float64(snap.PhaseCorr)-1.0) > 0.02 {
		t.Errorf("PhaseCorr = %f, want ~+1", snap.PhaseCorr)
	}
}

func TestAudioMeter_OutOfPhasePhaseCorr(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000
	sineL := func(i int) float32 { return float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)) }
	sineR := func(i int) float32 { return float32(-math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)) }
	for chunk := 0; chunk < 18; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sineL, sineR})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	if math.Abs(float64(snap.PhaseCorr)-(-1.0)) > 0.02 {
		t.Errorf("PhaseCorr = %f, want ~-1", snap.PhaseCorr)
	}
}

func TestAudioMeter_PeakDecayExponential(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000
	// One full-scale sample, then silence
	hit := func(i int) float32 {
		if i == 0 {
			return 1.0
		}
		return 0
	}
	pcm := makeStereoPCM(1, [2]func(int) float32{hit, hit})
	m.Observe(pcm, 2, 48000)
	// Now feed 48000 zero samples in 60 chunks of 800
	silent := func(i int) float32 { return 0 }
	for chunk := 0; chunk < 60; chunk++ {
		zeros := makeStereoPCM(800, [2]func(int) float32{silent, silent})
		m.Observe(zeros, 2, 48000)
	}
	// 1 second elapsed; peak should decay by -20 dB (factor 0.1)
	snap := m.AudioScopes()
	if math.Abs(float64(snap.Peak[0])-0.1) > 0.02 {
		t.Errorf("Peak after 1 s decay = %f, want ~0.1 (-20 dB)", snap.Peak[0])
	}
}

func TestAudioMeter_GoniometerCapturesRecentSamples(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000
	sineL := func(i int) float32 { return float32(math.Sin(2 * math.Pi * 100 * float64(i) / 48000)) }
	sineR := func(i int) float32 { return float32(math.Cos(2 * math.Pi * 100 * float64(i) / 48000)) }
	for chunk := 0; chunk < 4; chunk++ { // 4 * 800 = 3200 samples ≈ 67 ms
		pcm := makeStereoPCM(800, [2]func(int) float32{sineL, sineR})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	// Goniometer is fixed length 256
	if len(snap.Goniometer) != 256 {
		t.Fatalf("Goniometer length = %d, want 256", len(snap.Goniometer))
	}
	// Values should be within [-1, +1]
	for i, pair := range snap.Goniometer {
		if pair[0] < -1.0 || pair[0] > 1.0 || pair[1] < -1.0 || pair[1] > 1.0 {
			t.Errorf("Goniometer[%d] = %v, out of range", i, pair)
		}
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/dataplane -run TestAudioMeter -v
```

Expected: FAIL with `undefined: NewAudioMeter` etc.

- [ ] **Step 3: Implement `audiometer.go` (peak/RMS/phase/goniometer only)**

Create `internal/dataplane/audiometer.go`:

```go
package dataplane

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/fft"
)

const (
	audioTargetHzDefault     = 30
	audioBytesPerSample      = 2
	audioFFTSize             = 1024
	audioSpectrumBands       = 32
	audioGoniometerSize      = 256
	audioGoniometerWindowSec = 0.050
	audioPhaseWindowSec      = 0.300
	audioLUFSWindowSec       = 3.0
	audioLUFSSilenceFloor    = -100.0
	audioSpectrumSentinel    = -90.0
	audioPeakDecayDBPerSec   = -20.0
)

// AudioScopeSnapshot is the lock-free snapshot of audio-analysis values
// published by AudioMeter. Returned pointer is read-only — callers MUST
// NOT mutate the pointee.
type AudioScopeSnapshot struct {
	Generation    uint64
	SampleRate    int
	Channels      int
	Peak          [2]float32
	RMS           [2]float32
	PhaseCorr     float32
	LUFSShort     float32
	SpectrumBands [audioSpectrumBands]float32
	Goniometer    [audioGoniometerSize][2]float32
	PublishedAt   time.Time
}

// AudioMeter computes audio-analysis values from PCM chunks observed
// inline on the field-tick goroutine. Single-producer; many readers via
// AudioScopes(). Construction allocates the running-state buffers; Observe
// is allocation-free on non-publish ticks and allocates one snapshot
// pointer on publish ticks.
type AudioMeter struct {
	generation uint64
	sampleRate int
	channels   int
	snapshot   atomic.Pointer[AudioScopeSnapshot]

	// Bresenham cadence: Observe adds samplesInChunk * targetHz; publish
	// when publishAccum >= sampleRate, then subtract sampleRate.
	//
	// targetHz is exported as a test seam:
	//   - production:        targetHz = 30
	//   - no-publish run:    targetHz = 0 (accumulator never advances)
	//   - always-publish:    targetHz = sampleRate (publish every Observe)
	targetHz     int
	publishAccum int

	state              audioMeterState
	peakDecayPerSample float32
	gonioDecimStep     int
	gonioStepCount     int

	// LUFS coefficients (Task 3 will populate)
	kPreCoeffs  biquadCoeffs
	kHighCoeffs biquadCoeffs

	// FFT scratch (Task 4 will populate)
	fftWindowed [audioFFTSize]float32
	fftOut      []complex64
	hannWindow  []float32
}

type audioMeterState struct {
	// peak per channel with exponential decay
	peak [2]float32

	// RMS sliding window (300 ms): ring of squared samples per channel
	rmsRingL, rmsRingR   []float32
	rmsHead              int
	rmsCount             int
	rmsSumSqL, rmsSumSqR float64

	// Phase correlation accumulators (300 ms window)
	phaseSumL, phaseSumR       float64
	phaseSumLR                 float64
	phaseSumSqL, phaseSumSqR   float64
	phaseRingL, phaseRingR     []float32
	phaseHead                  int
	phaseCount                 int

	// FFT input ring (1024 samples, mono mix-down)
	fftRing [audioFFTSize]float32
	fftHead int

	// Goniometer ring (256 decimated stereo pairs)
	gonio     [audioGoniometerSize][2]float32
	gonioHead int

	// Last published spectrum (kept across non-publish ticks)
	lastSpectrum [audioSpectrumBands]float32

	// LUFS K-weighting biquad state per channel (Task 3 will populate)
	kPreL, kPreR   biquadState
	kHighL, kHighR biquadState

	// LUFS sliding window (3 s) — K-weighted squared samples per channel
	lufsRingL, lufsRingR     []float32
	lufsHead                 int
	lufsCount                int
	lufsSumSqL, lufsSumSqR   float64
}

type biquadCoeffs struct {
	b0, b1, b2, a1, a2 float64
}

type biquadState struct {
	c          biquadCoeffs
	x1, x2     float64
	y1, y2     float64
}

func (b *biquadState) setCoeffs(c biquadCoeffs) {
	b.c = c
}

func (b *biquadState) process(x float64) float64 {
	y := b.c.b0*x + b.c.b1*b.x1 + b.c.b2*b.x2 - b.c.a1*b.y1 - b.c.a2*b.y2
	b.x2, b.x1 = b.x1, x
	b.y2, b.y1 = b.y1, y
	return y
}

// NewAudioMeter constructs an AudioMeter for the given session generation
// and audio format. AudioMeter assumes sampleRate is constant for its
// lifetime; source switches that change sampleRate must construct a new
// AudioMeter.
func NewAudioMeter(generation uint64, sampleRate, channels int) *AudioMeter {
	m := &AudioMeter{
		generation: generation,
		sampleRate: sampleRate,
		channels:   channels,
		targetHz:   audioTargetHzDefault,
	}
	m.peakDecayPerSample = float32(math.Pow(10, audioPeakDecayDBPerSec/20.0/float64(sampleRate)))

	rmsLen := int(audioPhaseWindowSec * float64(sampleRate))
	m.state.rmsRingL = make([]float32, rmsLen)
	m.state.rmsRingR = make([]float32, rmsLen)
	m.state.phaseRingL = make([]float32, rmsLen)
	m.state.phaseRingR = make([]float32, rmsLen)

	lufsLen := int(audioLUFSWindowSec * float64(sampleRate))
	m.state.lufsRingL = make([]float32, lufsLen)
	m.state.lufsRingR = make([]float32, lufsLen)

	m.kPreCoeffs = kWeightingPreFilter(sampleRate)
	m.kHighCoeffs = kWeightingHighShelf(sampleRate)
	m.state.kPreL.setCoeffs(m.kPreCoeffs)
	m.state.kPreR.setCoeffs(m.kPreCoeffs)
	m.state.kHighL.setCoeffs(m.kHighCoeffs)
	m.state.kHighR.setCoeffs(m.kHighCoeffs)

	m.gonioDecimStep = int(audioGoniometerWindowSec*float64(sampleRate)) / audioGoniometerSize
	if m.gonioDecimStep < 1 {
		m.gonioDecimStep = 1
	}

	m.fftOut = make([]complex64, audioFFTSize/2+1)
	m.hannWindow = fft.Hann1024()
	return m
}

// Observe processes one PCM chunk on the field-tick goroutine. pcm is
// s16le interleaved; len(pcm) MUST equal samplesInChunk*channels*2.
// Allocation-free on non-publish ticks.
func (m *AudioMeter) Observe(pcm []byte, channels, sampleRate int) {
	if channels != m.channels || sampleRate != m.sampleRate {
		// Sample-rate or channel-count change is not supported within a
		// meter's lifetime (see spec §Sample rate hot-swap). Panic loudly
		// so the implementer of a future hot-swap feature sees this
		// boundary clearly.
		panic("dataplane: AudioMeter.Observe sampleRate/channels mismatch")
	}
	samplesInChunk := len(pcm) / (audioBytesPerSample * channels)
	if samplesInChunk == 0 {
		return
	}

	for i := 0; i < samplesInChunk; i++ {
		var l, r float32
		l = int16ToFloat(pcm, i*channels*2)
		if channels == 2 {
			r = int16ToFloat(pcm, (i*channels+1)*2)
		} else {
			r = l
		}

		// Peak: instant attack, exponential release
		m.state.peak[0] *= m.peakDecayPerSample
		if a := abs32(l); a > m.state.peak[0] {
			m.state.peak[0] = a
		}
		m.state.peak[1] *= m.peakDecayPerSample
		if a := abs32(r); a > m.state.peak[1] {
			m.state.peak[1] = a
		}

		// RMS sliding window
		old := m.state.rmsRingL[m.state.rmsHead]
		m.state.rmsSumSqL += float64(l)*float64(l) - float64(old)*float64(old)
		m.state.rmsRingL[m.state.rmsHead] = l
		old = m.state.rmsRingR[m.state.rmsHead]
		m.state.rmsSumSqR += float64(r)*float64(r) - float64(old)*float64(old)
		m.state.rmsRingR[m.state.rmsHead] = r
		m.state.rmsHead = (m.state.rmsHead + 1) % len(m.state.rmsRingL)
		if m.state.rmsCount < len(m.state.rmsRingL) {
			m.state.rmsCount++
		}

		// Phase correlation: maintain Σl, Σr, Σlr, Σl², Σr² over window
		oldL := m.state.phaseRingL[m.state.phaseHead]
		oldR := m.state.phaseRingR[m.state.phaseHead]
		m.state.phaseSumL += float64(l - oldL)
		m.state.phaseSumR += float64(r - oldR)
		m.state.phaseSumLR += float64(l)*float64(r) - float64(oldL)*float64(oldR)
		m.state.phaseSumSqL += float64(l)*float64(l) - float64(oldL)*float64(oldL)
		m.state.phaseSumSqR += float64(r)*float64(r) - float64(oldR)*float64(oldR)
		m.state.phaseRingL[m.state.phaseHead] = l
		m.state.phaseRingR[m.state.phaseHead] = r
		m.state.phaseHead = (m.state.phaseHead + 1) % len(m.state.phaseRingL)
		if m.state.phaseCount < len(m.state.phaseRingL) {
			m.state.phaseCount++
		}

		// FFT input ring (mono mix-down)
		mono := (l + r) / 2
		m.state.fftRing[m.state.fftHead] = mono
		m.state.fftHead = (m.state.fftHead + 1) % audioFFTSize

		// Goniometer decimation
		m.state.gonioStepCount = (m.state.gonioStepCount + 1) % m.gonioDecimStep
		if m.state.gonioStepCount == 0 {
			m.state.gonio[m.state.gonioHead] = [2]float32{l, r}
			m.state.gonioHead = (m.state.gonioHead + 1) % audioGoniometerSize
		}

		// LUFS K-weighting (Task 3 will fill this in)
		_ = m.state.kPreL.process(float64(l))
		_ = m.state.kPreR.process(float64(r))
		// stored sample-by-sample but LUFS computation lands in Task 3
	}

	// Bresenham cadence
	if m.targetHz <= 0 {
		return
	}
	m.publishAccum += samplesInChunk * m.targetHz
	for m.publishAccum >= m.sampleRate {
		m.publishAccum -= m.sampleRate
		m.publish()
	}
}

// AudioScopes returns the latest published snapshot or nil if no
// snapshot has been published yet. Lock-free. Read-only.
func (m *AudioMeter) AudioScopes() *AudioScopeSnapshot {
	return m.snapshot.Load()
}

// publish builds a fresh snapshot from current running state and
// atomically stores it. Called inline from Observe on publish ticks.
func (m *AudioMeter) publish() {
	snap := &AudioScopeSnapshot{
		Generation:  m.generation,
		SampleRate:  m.sampleRate,
		Channels:    m.channels,
		Peak:        m.state.peak,
		PublishedAt: time.Now(),
	}
	// RMS = sqrt(mean square over filled window)
	if m.state.rmsCount > 0 {
		snap.RMS[0] = float32(math.Sqrt(m.state.rmsSumSqL / float64(m.state.rmsCount)))
		snap.RMS[1] = float32(math.Sqrt(m.state.rmsSumSqR / float64(m.state.rmsCount)))
	}
	// Phase: Pearson correlation
	if m.state.phaseCount > 1 {
		n := float64(m.state.phaseCount)
		num := m.state.phaseSumLR - (m.state.phaseSumL*m.state.phaseSumR)/n
		denL := m.state.phaseSumSqL - (m.state.phaseSumL*m.state.phaseSumL)/n
		denR := m.state.phaseSumSqR - (m.state.phaseSumR*m.state.phaseSumR)/n
		den := math.Sqrt(denL * denR)
		if den > 1e-12 {
			snap.PhaseCorr = float32(num / den)
		}
	}
	// Goniometer: copy ring in arrival order (oldest first)
	for i := 0; i < audioGoniometerSize; i++ {
		idx := (m.state.gonioHead + i) % audioGoniometerSize
		snap.Goniometer[i] = m.state.gonio[idx]
	}
	// Spectrum: Task 4 will populate via FFT. Until then, reuse lastSpectrum
	// (all zero on first publish before Task 4 lands).
	snap.SpectrumBands = m.state.lastSpectrum
	// LUFS: Task 3 will populate. Until then, leave as zero value (0.0).
	snap.LUFSShort = 0
	m.snapshot.Store(snap)
}

func int16ToFloat(pcm []byte, offset int) float32 {
	// Little-endian s16
	lo := uint16(pcm[offset])
	hi := uint16(pcm[offset+1])
	s := int16(lo | hi<<8)
	return float32(s) / 32768
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// Placeholder LUFS coefficient functions; Task 3 implements them.
func kWeightingPreFilter(sampleRate int) biquadCoeffs  { return biquadCoeffs{b0: 1} }
func kWeightingHighShelf(sampleRate int) biquadCoeffs { return biquadCoeffs{b0: 1} }
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/dataplane -run TestAudioMeter -v
```

Expected: all five tests PASS. If `TestAudioMeter_FullScaleSinePeakAndRMS` reports peak slightly under 1.0, that's the per-sample decay applied during the chunk; raise the test tolerance to 0.05 or seed the test with a fresh meter and one full-scale sample first.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/audiometer.go internal/dataplane/audiometer_test.go
git commit -m "feat(dataplane): add AudioMeter with peak/RMS/phase/goniometer DSP"
```

---

## Task 3: AudioMeter LUFS Short-Term Loudness

This task replaces the placeholder K-weighting coefficient functions with real BS.1770-4 filters and adds the LUFS-short integration over the 3-second sliding window.

**Files:**
- Modify: `internal/dataplane/audiometer.go`
- Modify: `internal/dataplane/audiometer_test.go`

- [ ] **Step 1: Write failing LUFS calibration tests**

Append to `internal/dataplane/audiometer_test.go`:

```go
func TestAudioMeter_LUFSShortMonoCalibration(t *testing.T) {
	// BS.1770-4: 1 kHz sine at -20 dBFS RMS mono → -20.7 LUFS ±0.5
	m := NewAudioMeter(1, 48000, 1)
	m.targetHz = 48000
	// -20 dBFS RMS sine: amplitude = sqrt(2) * 10^(-20/20) = 0.1414
	const amp = 0.1414213562
	sine := func(i int) float32 { return float32(amp * math.Sin(2*math.Pi*1000*float64(i)/48000)) }
	// Feed > 3 s to fill the LUFS window
	for chunk := 0; chunk < 180; chunk++ { // 180 * 800 = 144000 samples = 3 s
		pcm := makeMonoPCM(800, sine)
		m.Observe(pcm, 1, 48000)
	}
	snap := m.AudioScopes()
	want := -20.7
	if math.Abs(float64(snap.LUFSShort)-want) > 0.5 {
		t.Errorf("LUFSShort = %f, want %f ± 0.5", snap.LUFSShort, want)
	}
}

func TestAudioMeter_LUFSDualMonoStereoIsLouder(t *testing.T) {
	// Dual-mono stereo at -20 dBFS RMS → ~3 dB louder than mono (channel power sum)
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000
	const amp = 0.1414213562
	sine := func(i int) float32 { return float32(amp * math.Sin(2*math.Pi*1000*float64(i)/48000)) }
	for chunk := 0; chunk < 180; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sine, sine})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	want := -17.7 // -20.7 + 3.0
	if math.Abs(float64(snap.LUFSShort)-want) > 0.7 {
		t.Errorf("LUFSShort dual-mono = %f, want %f ± 0.7", snap.LUFSShort, want)
	}
}

func TestAudioMeter_LUFSSilenceReturnsSentinel(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000
	silent := func(i int) float32 { return 0 }
	for chunk := 0; chunk < 180; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{silent, silent})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	if snap.LUFSShort != audioLUFSSilenceFloor {
		t.Errorf("LUFSShort silence = %f, want %f", snap.LUFSShort, audioLUFSSilenceFloor)
	}
}

// makeMonoPCM is a helper paralleling makeStereoPCM but for 1-channel input.
func makeMonoPCM(frames int, gen func(i int) float32) []byte {
	buf := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		v := gen(i)
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		s := int16(v * 32767)
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}
```

- [ ] **Step 2: Run LUFS tests to confirm they fail**

```bash
go test ./internal/dataplane -run TestAudioMeter_LUFS -v
```

Expected: FAIL — LUFSShort is 0 (placeholder), not the BS.1770 values.

- [ ] **Step 3: Implement BS.1770 K-weighting and LUFS integration**

In `internal/dataplane/audiometer.go`, replace the placeholder coefficient functions and the LUFS section of `Observe` and `publish`.

Replace the placeholder functions at the bottom of the file:

```go
// kWeightingPreFilter computes the BS.1770-4 pre-filter (high-frequency
// shelving boost at ~1.68 kHz, +4 dB) for the given sample rate using
// RBJ Audio EQ Cookbook biquad design.
func kWeightingPreFilter(sampleRate int) biquadCoeffs {
	const (
		f0     = 1681.974450955533
		q      = 0.7071752369554196
		gainDB = 3.999843853973347
	)
	return designShelfHigh(float64(sampleRate), f0, q, gainDB)
}

// kWeightingHighShelf computes the BS.1770-4 RLB filter (high-pass at
// ~38 Hz) for the given sample rate.
func kWeightingHighShelf(sampleRate int) biquadCoeffs {
	const (
		f0 = 38.13547087602444
		q  = 0.5003270373238773
	)
	return designHighpass(float64(sampleRate), f0, q)
}

// designShelfHigh designs a 2nd-order high-frequency shelving biquad
// (RBJ Cookbook, "highShelf"). Returns normalized (a0 = 1) coefficients.
func designShelfHigh(sampleRate, f0, q, gainDB float64) biquadCoeffs {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / sampleRate
	cosW := math.Cos(w0)
	sinW := math.Sin(w0)
	alpha := sinW / (2 * q)
	sqrtA := math.Sqrt(A)

	b0 := A * ((A + 1) + (A-1)*cosW + 2*sqrtA*alpha)
	b1 := -2 * A * ((A - 1) + (A+1)*cosW)
	b2 := A * ((A + 1) + (A-1)*cosW - 2*sqrtA*alpha)
	a0 := (A + 1) - (A-1)*cosW + 2*sqrtA*alpha
	a1 := 2 * ((A - 1) - (A+1)*cosW)
	a2 := (A + 1) - (A-1)*cosW - 2*sqrtA*alpha
	return biquadCoeffs{
		b0: b0 / a0, b1: b1 / a0, b2: b2 / a0,
		a1: a1 / a0, a2: a2 / a0,
	}
}

// designHighpass designs a 2nd-order high-pass biquad (RBJ Cookbook).
func designHighpass(sampleRate, f0, q float64) biquadCoeffs {
	w0 := 2 * math.Pi * f0 / sampleRate
	cosW := math.Cos(w0)
	sinW := math.Sin(w0)
	alpha := sinW / (2 * q)

	b0 := (1 + cosW) / 2
	b1 := -(1 + cosW)
	b2 := (1 + cosW) / 2
	a0 := 1 + alpha
	a1 := -2 * cosW
	a2 := 1 - alpha
	return biquadCoeffs{
		b0: b0 / a0, b1: b1 / a0, b2: b2 / a0,
		a1: a1 / a0, a2: a2 / a0,
	}
}
```

In `Observe`'s per-sample loop, replace the placeholder K-weighting block with full LUFS integration. Find the existing two lines:

```go
		// LUFS K-weighting (Task 3 will fill this in)
		_ = m.state.kPreL.process(float64(l))
		_ = m.state.kPreR.process(float64(r))
		// stored sample-by-sample but LUFS computation lands in Task 3
```

…and replace with:

```go
		// LUFS K-weighting: cascade pre-filter then RLB high-pass, then
		// integrate squared output over the 3 s sliding window per channel.
		kL := m.state.kHighL.process(m.state.kPreL.process(float64(l)))
		kR := m.state.kHighR.process(m.state.kPreR.process(float64(r)))
		oldKL := m.state.lufsRingL[m.state.lufsHead]
		oldKR := m.state.lufsRingR[m.state.lufsHead]
		m.state.lufsSumSqL += kL*kL - float64(oldKL)*float64(oldKL)
		m.state.lufsSumSqR += kR*kR - float64(oldKR)*float64(oldKR)
		m.state.lufsRingL[m.state.lufsHead] = float32(kL)
		m.state.lufsRingR[m.state.lufsHead] = float32(kR)
		m.state.lufsHead = (m.state.lufsHead + 1) % len(m.state.lufsRingL)
		if m.state.lufsCount < len(m.state.lufsRingL) {
			m.state.lufsCount++
		}
```

In `publish`, replace the LUFS placeholder:

```go
	// LUFS: Task 3 will populate. Until then, leave as zero value (0.0).
	snap.LUFSShort = 0
```

…with:

```go
	// LUFS short-term (BS.1770-4): L_K = -0.691 + 10 * log10(Σ G_ch * meanSquare_ch).
	// G_L = G_R = 1.0 for stereo; mono is a single-channel sum.
	if m.state.lufsCount >= len(m.state.lufsRingL) {
		var totalPower float64
		meanSqL := m.state.lufsSumSqL / float64(m.state.lufsCount)
		if m.channels == 1 {
			totalPower = meanSqL
		} else {
			meanSqR := m.state.lufsSumSqR / float64(m.state.lufsCount)
			totalPower = meanSqL + meanSqR
		}
		if totalPower > 1e-12 {
			snap.LUFSShort = float32(-0.691 + 10*math.Log10(totalPower))
		} else {
			snap.LUFSShort = audioLUFSSilenceFloor
		}
	} else {
		snap.LUFSShort = audioLUFSSilenceFloor
	}
```

- [ ] **Step 4: Run LUFS tests to confirm they pass**

```bash
go test ./internal/dataplane -run TestAudioMeter_LUFS -v
```

Expected: all three LUFS tests PASS. The dual-mono +3 dB assertion has ±0.7 tolerance to absorb biquad numerical drift at non-48-kHz rates; at 48 kHz it should land within ±0.3.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/audiometer.go internal/dataplane/audiometer_test.go
git commit -m "feat(dataplane): add BS.1770 K-weighting and LUFS-short integration"
```

---

## Task 4: AudioMeter FFT Spectrum

This task replaces the placeholder spectrum (zeros) with a real Hann-windowed FFT computed on publish ticks.

**Files:**
- Modify: `internal/dataplane/audiometer.go`
- Modify: `internal/dataplane/audiometer_test.go`

- [ ] **Step 1: Write failing FFT spectrum tests**

Append to `internal/dataplane/audiometer_test.go`:

```go
func TestAudioMeter_SpectrumSilenceIsSentinel(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000
	// Feed enough silence to fill the FFT input ring and publish
	silent := func(i int) float32 { return 0 }
	for chunk := 0; chunk < 2; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{silent, silent})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	for i, v := range snap.SpectrumBands {
		if v != audioSpectrumSentinel {
			t.Errorf("SpectrumBands[%d] = %f, want sentinel %f", i, v, audioSpectrumSentinel)
		}
	}
}

func TestAudioMeter_SpectrumBinCenteredSinePeak(t *testing.T) {
	// Sine at 3000 Hz (= bin 64 at sampleRate=48000, fftSize=1024)
	// should produce a peak in the log band that contains bin 64.
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000
	const freq = 3000.0
	sine := func(i int) float32 { return float32(math.Sin(2 * math.Pi * freq * float64(i) / 48000)) }
	// Feed 2 chunks (1600 samples) to overfill the 1024-sample FFT ring
	for chunk := 0; chunk < 3; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sine, sine})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	// Find the band containing freq using the spec's log spacing:
	// edge[i] = 20 * (1000^(i/32)) for i = 0..32
	targetBand := 0
	for i := 0; i < audioSpectrumBands; i++ {
		lo := 20.0 * math.Pow(1000, float64(i)/32)
		hi := 20.0 * math.Pow(1000, float64(i+1)/32)
		if freq >= lo && freq < hi {
			targetBand = i
			break
		}
	}
	// Peak band should be ≈ 0 dBFS (full-scale sine); other bands ≪ -20 dBFS
	if snap.SpectrumBands[targetBand] < -3 {
		t.Errorf("target band %d = %f dBFS, want > -3 (peak)", targetBand, snap.SpectrumBands[targetBand])
	}
	for i, v := range snap.SpectrumBands {
		if i == targetBand {
			continue
		}
		if v > -10 {
			t.Errorf("non-target band %d = %f dBFS, want < -10 (suppressed)", i, v)
		}
	}
}
```

- [ ] **Step 2: Run spectrum tests to confirm they fail**

```bash
go test ./internal/dataplane -run TestAudioMeter_Spectrum -v
```

Expected: FAIL — spectrum is zeros (`-Inf` log) or zero literal, not sentinel and not peak.

- [ ] **Step 3: Implement spectrum FFT in publish path**

In `internal/dataplane/audiometer.go`, add a `computeSpectrum` method that runs the FFT and updates `m.state.lastSpectrum`. Call it before populating the snapshot.

Add after the `publish` method:

```go
// computeSpectrum runs one FFT pass on the current FFT input ring,
// updating m.state.lastSpectrum with band-summed power in dBFS. Called
// on publish ticks only.
func (m *AudioMeter) computeSpectrum() {
	// Copy ring in arrival order (oldest first) into windowed scratch
	for i := 0; i < audioFFTSize; i++ {
		idx := (m.state.fftHead + i) % audioFFTSize
		m.fftWindowed[i] = m.state.fftRing[idx] * m.hannWindow[i]
	}

	// Detect all-zero input (silence) and emit sentinel without running FFT
	allZero := true
	for _, v := range m.fftWindowed {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		for i := range m.state.lastSpectrum {
			m.state.lastSpectrum[i] = audioSpectrumSentinel
		}
		return
	}

	bins := fft.Real1024(m.fftWindowed[:], m.fftOut)
	nyquist := float64(m.sampleRate) / 2

	// Compute Hann-corrected per-bin power (one-sided: ×2 for bins 1..N/2-1)
	// Calibration: a bin-centered full-scale sine produces |X[k]| ≈ N/2 * sqrt(8/3)
	// after Hann windowing. To recover 0 dBFS peak: divide by N^2 * (sum(w²)/N) / 2.
	hannEnergyGain := 3.0 / 8.0
	normFactor := 1.0 / (float64(audioFFTSize) * float64(audioFFTSize) * hannEnergyGain / 2)

	for band := 0; band < audioSpectrumBands; band++ {
		lo := 20.0 * math.Pow(1000, float64(band)/32)
		hi := 20.0 * math.Pow(1000, float64(band+1)/32)
		if lo >= nyquist {
			m.state.lastSpectrum[band] = audioSpectrumSentinel
			continue
		}
		loBin := int(lo * float64(audioFFTSize) / float64(m.sampleRate))
		hiBin := int(hi * float64(audioFFTSize) / float64(m.sampleRate))
		if loBin < 1 {
			loBin = 1
		}
		if hiBin > audioFFTSize/2 {
			hiBin = audioFFTSize / 2
		}
		if hiBin <= loBin {
			hiBin = loBin + 1
		}
		var power float64
		for k := loBin; k < hiBin; k++ {
			re := float64(real(bins[k]))
			im := float64(imag(bins[k]))
			power += (re*re + im*im) * 2 // one-sided spectrum
		}
		power *= normFactor
		if power < 1e-9 {
			m.state.lastSpectrum[band] = audioSpectrumSentinel
		} else {
			db := 10 * math.Log10(power)
			if db < audioSpectrumSentinel {
				db = audioSpectrumSentinel
			}
			m.state.lastSpectrum[band] = float32(db)
		}
	}
}
```

Modify `publish` to call `computeSpectrum` before copying `lastSpectrum` into the snapshot. Find:

```go
	// Spectrum: Task 4 will populate via FFT. Until then, reuse lastSpectrum
	// (all zero on first publish before Task 4 lands).
	snap.SpectrumBands = m.state.lastSpectrum
```

…replace with:

```go
	// Spectrum: run FFT on current ring, then copy into snapshot.
	m.computeSpectrum()
	snap.SpectrumBands = m.state.lastSpectrum
```

- [ ] **Step 4: Run spectrum tests to confirm they pass**

```bash
go test ./internal/dataplane -run TestAudioMeter_Spectrum -v
```

Expected: both spectrum tests PASS. The peak-band tolerance is ±3 dB to absorb Hann scalloping (bin-centered tones still spread main-lobe energy across 3-4 bins).

- [ ] **Step 5: Run full audiometer test suite to confirm no regressions**

```bash
go test ./internal/dataplane -run TestAudioMeter -v
```

Expected: all eight (or however many) tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/dataplane/audiometer.go internal/dataplane/audiometer_test.go
git commit -m "feat(dataplane): add Hann-windowed FFT spectrum to AudioMeter"
```

---

## Task 5: AudioMeter Concurrency, Alloc Budget, and Bresenham Cadence Tests

**Files:**
- Modify: `internal/dataplane/audiometer_test.go`

- [ ] **Step 1: Add alloc-budget and cadence tests**

Append to `internal/dataplane/audiometer_test.go`:

```go
func TestAudioMeter_NoPublishHotPathZeroAllocs(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 0 // disable publishes entirely
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0.5 },
		func(i int) float32 { return -0.5 },
	})
	// Warm the meter so RMS/phase rings are populated
	for i := 0; i < 30; i++ {
		m.Observe(pcm, 2, 48000)
	}
	allocs := testing.AllocsPerRun(100, func() {
		m.Observe(pcm, 2, 48000)
	})
	if allocs != 0 {
		t.Errorf("AllocsPerRun = %f, want 0 (no-publish hot path)", allocs)
	}
}

func TestAudioMeter_AlwaysPublishExactlyOneAlloc(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 48000 // publish on every Observe
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0.5 },
		func(i int) float32 { return -0.5 },
	})
	// Warm
	for i := 0; i < 30; i++ {
		m.Observe(pcm, 2, 48000)
	}
	allocs := testing.AllocsPerRun(100, func() {
		m.Observe(pcm, 2, 48000)
	})
	if allocs != 1 {
		t.Errorf("AllocsPerRun = %f, want exactly 1 (snapshot pointer)", allocs)
	}
}

func TestAudioMeter_BresenhamCadenceNTSC(t *testing.T) {
	// 48 kHz @ 59.94 Hz field rate: 800 frames per chunk.
	// 10 s of audio = 600 chunks → expect 300 publishes ± 1.
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 30
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0 },
		func(i int) float32 { return 0 },
	})
	count := 0
	prevSnap := m.AudioScopes()
	for i := 0; i < 600; i++ {
		m.Observe(pcm, 2, 48000)
		curr := m.AudioScopes()
		if curr != prevSnap {
			count++
			prevSnap = curr
		}
	}
	if count < 299 || count > 301 {
		t.Errorf("NTSC publish count over 10 s = %d, want 300 ± 1", count)
	}
}

func TestAudioMeter_BresenhamCadencePAL(t *testing.T) {
	// 48 kHz @ 50 Hz field rate: 960 frames per chunk.
	// 10 s of audio = 500 chunks → expect 300 publishes ± 1.
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 30
	pcm := makeStereoPCM(960, [2]func(int) float32{
		func(i int) float32 { return 0 },
		func(i int) float32 { return 0 },
	})
	count := 0
	prevSnap := m.AudioScopes()
	for i := 0; i < 500; i++ {
		m.Observe(pcm, 2, 48000)
		curr := m.AudioScopes()
		if curr != prevSnap {
			count++
			prevSnap = curr
		}
	}
	if count < 299 || count > 301 {
		t.Errorf("PAL publish count over 10 s = %d, want 300 ± 1", count)
	}
}

func TestAudioMeter_ConcurrentReadersUnderRace(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 30
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0.3 },
		func(i int) float32 { return -0.3 },
	})
	done := make(chan struct{})
	// 5 concurrent readers
	for r := 0; r < 5; r++ {
		go func() {
			for i := 0; i < 10000; i++ {
				snap := m.AudioScopes()
				if snap != nil {
					_ = snap.Peak[0]
					_ = snap.SpectrumBands[0]
				}
			}
			done <- struct{}{}
		}()
	}
	// Single writer (this goroutine)
	for i := 0; i < 1000; i++ {
		m.Observe(pcm, 2, 48000)
	}
	for r := 0; r < 5; r++ {
		<-done
	}
	// If -race did not flag anything, we pass. The test itself just exercises.
}
```

- [ ] **Step 2: Run tests; race detector required**

```bash
go test ./internal/dataplane -run TestAudioMeter -race -v
```

Expected: all tests PASS, no race detector output. If `TestAudioMeter_AlwaysPublishExactlyOneAlloc` returns >1 alloc, inspect the publish path for an accidental slice copy or map operation.

- [ ] **Step 3: Commit**

```bash
git add internal/dataplane/audiometer_test.go
git commit -m "test(dataplane): cover AudioMeter cadence, alloc budget, race"
```

---

## Task 6: Plane Wiring — PlaneConfig.Generation, audioMeter, AudioScopes()

**Files:**
- Modify: `internal/dataplane/plane.go`
- Modify: `internal/dataplane/plane_test.go`

- [ ] **Step 1: Add PlaneConfig.Generation field**

In `internal/dataplane/plane.go`, locate `type PlaneConfig struct` (currently around line 257) and add `Generation` near the top of the struct:

```go
type PlaneConfig struct {
	Sender              *groovynet.Sender
	SpawnSpec           ffmpeg.PipelineSpec
	Modeline            groovy.Modeline
	FieldWidth          int
	FieldHeight         int
	BytesPerPixel       int
	RGBMode             byte
	LZ4Enabled          bool
	DeltaLZ4Enabled     bool
	AudioRate           int
	AudioChans          int
	SuppressAudioOutput bool
	OutputVolume        int
	SeekOffsetMs        int
	Generation          uint64 // session generation; stamps audio snapshots

	OnInit func(err error)
}
```

- [ ] **Step 2: Add audioMeter field on Plane**

Locate `type Plane struct` (currently around line 326). Add an `audioMeter *AudioMeter` field:

```go
type Plane struct {
	cfg         PlaneConfig
	audioMeter  *AudioMeter
	// … existing fields below …
}
```

- [ ] **Step 3: Initialize audioMeter in NewPlane**

Locate the `NewPlane` constructor (grep for `func NewPlane`). After the existing field initialization, add:

```go
	// Audio meter is constructed even when audio is suppressed, so
	// AudioScopes() can return a meaningful nil-vs-zero distinction.
	// Construction is cheap (~ a few KB of ring allocations).
	p.audioMeter = NewAudioMeter(cfg.Generation, cfg.AudioRate, cfg.AudioChans)
```

If `cfg.AudioRate` or `cfg.AudioChans` could be zero (audio-disabled paths), guard:

```go
	if cfg.AudioRate > 0 && cfg.AudioChans > 0 {
		p.audioMeter = NewAudioMeter(cfg.Generation, cfg.AudioRate, cfg.AudioChans)
	}
```

- [ ] **Step 4: Call audioMeter.Observe before sendAudio**

Locate the field-tick loop's audio branch (currently at plane.go:988-1003, see the existing `p.sendAudio(oldest)` call at line 1000). Modify:

```go
				if audioRingLen > audioDelayN && p.audioReady.Load() {
					oldest := audioRing[audioRingHead]
					audioRing[audioRingHead] = nil
					audioRingHead = (audioRingHead + 1) % len(audioRing)
					audioRingLen--
					if len(oldest) > 0 {
						if p.audioMeter != nil {
							p.audioMeter.Observe(oldest, audioChans, audioRate)
						}
						p.sendAudio(oldest)
					}
				}
```

- [ ] **Step 5: Add Plane.AudioScopes()**

Add a new method near other `Plane.*` getters (e.g., next to `Plane.WireBytes`):

```go
// AudioScopes returns the latest audio-analysis snapshot or nil if no
// snapshot has been published yet (or if audio is suppressed and no
// meter was constructed). Returned pointer is read-only.
func (p *Plane) AudioScopes() *AudioScopeSnapshot {
	if p.audioMeter == nil {
		return nil
	}
	return p.audioMeter.AudioScopes()
}
```

- [ ] **Step 6: Update existing plane_test.go fakes if needed**

```bash
go build ./internal/dataplane
```

If the build fails because existing test fakes don't compile against the widened plane shape, update them minimally (add `Generation` to `PlaneConfig` literals where present; default to 0).

- [ ] **Step 7: Run dataplane tests**

```bash
go test ./internal/dataplane -v
```

Expected: PASS. If the audio-suppressed path is exercised and now sees `p.audioMeter != nil` checks, those should pass through cleanly.

- [ ] **Step 8: Commit**

```bash
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "feat(dataplane): wire AudioMeter into Plane field tick"
```

---

## Task 7: Core Surface — Manager.AudioScopes, type alias, generation plumbing

**Files:**
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Add type alias in core/types.go**

In `internal/core/types.go`, add the alias near other re-exported dataplane types (or at the bottom if none):

```go
// AudioScopeSnapshot is an alias for dataplane.AudioScopeSnapshot, so
// chassis can read the type via internal/core without importing
// internal/dataplane directly. Using an alias (not a wrapper struct)
// preserves the pointer-return contract: no copies cross package
// boundaries.
type AudioScopeSnapshot = dataplane.AudioScopeSnapshot
```

Ensure the `dataplane` import is present at the top of the file.

- [ ] **Step 2: Extend planeRunner interface**

In `internal/core/manager.go`, locate the `planeRunner` interface definition (grep for `type planeRunner interface`). Add the method:

```go
type planeRunner interface {
	// … existing methods …
	AudioScopes() *dataplane.AudioScopeSnapshot
}
```

- [ ] **Step 3: Add Manager.AudioScopes**

Add a new public method next to other `Manager` getters (e.g., near `OutputVolume()`):

```go
// AudioScopes returns the latest published audio-analysis snapshot from
// the active plane, or nil if no plane is active (idle, paused, between
// sessions). Returned pointer is read-only — callers MUST NOT mutate
// the pointee.
//
// Brief m.mu lock to load m.plane, then atomic.Pointer load on the
// plane itself. With many chassis tabs at 30 Hz, audio-tick stalls
// during session-lifecycle ops (start/stop/pause/seek/preempt) are
// acceptable; see spec §Lock-jitter acknowledgment.
func (m *Manager) AudioScopes() *AudioScopeSnapshot {
	m.mu.Lock()
	p := m.plane
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.AudioScopes()
}
```

- [ ] **Step 4: Pass Generation into PlaneConfig in startPlaneLocked**

Locate `startPlaneLocked` (grep for `func.*startPlaneLocked`). Find the `PlaneConfig{...}` literal being built. Add `Generation: generation,` (using whatever local variable holds the generation — typically called `generation` per the existing code). If the value is computed elsewhere in the function, ensure it's wired into the literal.

- [ ] **Step 5: Write failing test for Manager.AudioScopes nil/non-nil paths**

Add to `internal/core/manager_test.go`:

```go
func TestManager_AudioScopesNilWhenIdle(t *testing.T) {
	m := newTestManager(t)
	if got := m.AudioScopes(); got != nil {
		t.Errorf("AudioScopes() when idle = %v, want nil", got)
	}
}

func TestManager_AudioScopesNonNilWhenPlaneActive(t *testing.T) {
	m := newTestManager(t)
	snap := &dataplane.AudioScopeSnapshot{Generation: 7, Peak: [2]float32{0.5, 0.5}}
	m.plane = &audioScopesFakePlane{snap: snap}
	if got := m.AudioScopes(); got == nil || got.Generation != 7 {
		t.Errorf("AudioScopes() with plane = %v, want gen=7", got)
	}
}

type audioScopesFakePlane struct {
	fakePlane // existing test helper
	snap      *dataplane.AudioScopeSnapshot
}

func (p *audioScopesFakePlane) AudioScopes() *dataplane.AudioScopeSnapshot { return p.snap }
```

If the existing `fakePlane` does not satisfy `planeRunner`, you'll need to add a no-op `AudioScopes() *dataplane.AudioScopeSnapshot { return nil }` method to it — this is the test-update implied by widening the interface.

- [ ] **Step 6: Run core tests**

```bash
go test ./internal/core -v
```

Expected: PASS. If existing tests fail because the `planeRunner` interface widened and various fakes don't satisfy it, add the no-op `AudioScopes()` method to those fakes.

- [ ] **Step 7: Commit**

```bash
git add internal/core/types.go internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(core): expose Manager.AudioScopes with type alias"
```

---

## Task 8: Chassis Audio Surface

This task adds the `AudioScopeViewer` interface, the discriminated-union envelope types with custom `MarshalJSON` (NaN/Inf clamping, float-precision pin), `audioEnvelopeFromViewer`, and `audioShouldEmit`.

**Files:**
- Create: `internal/chassis/audio.go`
- Create: `internal/chassis/audio_test.go`
- Modify: `internal/chassis/server.go`

- [ ] **Step 1: Write failing envelope tests**

Create `internal/chassis/audio_test.go`:

```go
package chassis

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeAudioViewer struct {
	snap *core.AudioScopeSnapshot
}

func (f *fakeAudioViewer) AudioScopes() *core.AudioScopeSnapshot { return f.snap }

func TestAudioEnvelopeFromViewer_NilViewerReturnsPending(t *testing.T) {
	got := audioEnvelopeFromViewer(nil)
	pending, ok := got.(*audioPendingEnvelope)
	if !ok || pending.Status != "pending" {
		t.Errorf("got %#v, want *audioPendingEnvelope{Status:pending}", got)
	}
}

func TestAudioEnvelopeFromViewer_NilSnapshotReturnsPending(t *testing.T) {
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: nil})
	if _, ok := got.(*audioPendingEnvelope); !ok {
		t.Errorf("got %#v, want pending", got)
	}
}

func TestAudioEnvelopeFromViewer_ZeroGenerationReturnsPending(t *testing.T) {
	snap := &core.AudioScopeSnapshot{Generation: 0, SampleRate: 48000, Channels: 2}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	if _, ok := got.(*audioPendingEnvelope); !ok {
		t.Errorf("got %#v, want pending", got)
	}
}

func TestAudioEnvelopeFromViewer_LiveSnapshotReturnsLive(t *testing.T) {
	snap := &core.AudioScopeSnapshot{
		Generation: 42, SampleRate: 48000, Channels: 2,
		Peak: [2]float32{0.8, 0.7}, RMS: [2]float32{0.5, 0.4},
		PhaseCorr: 0.9, LUFSShort: -14.2,
	}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	live, ok := got.(*audioLiveEnvelope)
	if !ok {
		t.Fatalf("got %#v, want *audioLiveEnvelope", got)
	}
	if live.Status != "live" || live.Generation != 42 {
		t.Errorf("status/generation = %q/%d, want live/42", live.Status, live.Generation)
	}
	if live.VU.Left.Peak != 0.8 || live.VU.Right.RMS != 0.4 {
		t.Errorf("VU = %+v, want Peak=0.8 RMS=0.4", live.VU)
	}
}

func TestAudioLiveEnvelope_MarshalJSONIncludesLegitimateZeros(t *testing.T) {
	snap := &core.AudioScopeSnapshot{
		Generation: 1, SampleRate: 48000, Channels: 2,
		PhaseCorr: 0.0, // legitimate uncorrelated value
		LUFSShort: 0.0, // also legitimate
	}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, `"phaseCorr":0`) {
		t.Errorf("phaseCorr=0 omitted: %s", s)
	}
	if !strings.Contains(s, `"lufsShort":0`) {
		t.Errorf("lufsShort=0 omitted: %s", s)
	}
}

func TestAudioLiveEnvelope_MarshalJSONClampsNaNInf(t *testing.T) {
	snap := &core.AudioScopeSnapshot{
		Generation: 1, SampleRate: 48000, Channels: 2,
		Peak:      [2]float32{float32(math.NaN()), float32(math.Inf(1))},
		PhaseCorr: float32(math.Inf(-1)),
		LUFSShort: float32(math.NaN()),
	}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(body)
	if strings.Contains(s, "NaN") || strings.Contains(s, "Inf") {
		t.Errorf("NaN/Inf leaked: %s", s)
	}
}

func TestAudioShouldEmit_PendingToPendingFalse(t *testing.T) {
	a := &audioPendingEnvelope{Status: "pending"}
	b := &audioPendingEnvelope{Status: "pending"}
	if audioShouldEmit(a, b) {
		t.Error("pending→pending should not emit")
	}
}

func TestAudioShouldEmit_LiveAlwaysTrue(t *testing.T) {
	a := &audioLiveEnvelope{Status: "live", Generation: 1}
	b := &audioLiveEnvelope{Status: "live", Generation: 1}
	if !audioShouldEmit(a, b) {
		t.Error("live→live (identical) should emit (cadence preservation)")
	}
}

func TestAudioShouldEmit_TransitionsTrue(t *testing.T) {
	pending := &audioPendingEnvelope{Status: "pending"}
	live := &audioLiveEnvelope{Status: "live", Generation: 1}
	if !audioShouldEmit(pending, live) {
		t.Error("pending→live should emit")
	}
	if !audioShouldEmit(live, pending) {
		t.Error("live→pending should emit")
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run TestAudio -v
```

Expected: FAIL with `undefined: audioEnvelopeFromViewer` etc.

- [ ] **Step 3: Implement audio.go**

Create `internal/chassis/audio.go`:

```go
package chassis

import (
	"bytes"
	"math"
	"strconv"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// AudioScopeViewer is the read-only source for the latest audio-analysis
// snapshot. *core.Manager satisfies this structurally via AudioScopes().
// Tests use fakes. When the viewer is nil or returns nil, the chassis
// emits a pending audio frame. The returned pointer is read-only.
type AudioScopeViewer interface {
	AudioScopes() *core.AudioScopeSnapshot
}

// audioPendingEnvelope is the SSE payload for the `audio` event when no
// session is active. Encodes as {"status":"pending"} exactly.
type audioPendingEnvelope struct {
	Status string `json:"status"`
}

// audioLiveEnvelope is the SSE payload for the `audio` event when a
// session is active. Encoded via a custom MarshalJSON to pin float
// precision (5 sig figs, float32) and clamp NaN/Inf to safe values
// before serialization. No `omitempty` — every field is always emitted,
// including legitimate zeros (phaseCorr: 0.0 for uncorrelated stereo).
type audioLiveEnvelope struct {
	Status     string
	Generation uint64
	SampleRate int
	Channels   int
	VU         vuEnvelope
	PhaseCorr  float32
	LUFSShort  float32
	Spectrum   []float32
	Goniometer [][2]float32
}

type vuEnvelope struct {
	Left  channelLevel `json:"left"`
	Right channelLevel `json:"right"`
}

type channelLevel struct {
	Peak float32 `json:"peak"`
	RMS  float32 `json:"rms"`
}

// audioEnvelopeFromViewer returns *audioPendingEnvelope or
// *audioLiveEnvelope based on the viewer's current snapshot.
func audioEnvelopeFromViewer(v AudioScopeViewer) any {
	if v == nil {
		return &audioPendingEnvelope{Status: "pending"}
	}
	snap := v.AudioScopes()
	if snap == nil || snap.Generation == 0 {
		return &audioPendingEnvelope{Status: "pending"}
	}
	return &audioLiveEnvelope{
		Status:     "live",
		Generation: snap.Generation,
		SampleRate: snap.SampleRate,
		Channels:   snap.Channels,
		VU: vuEnvelope{
			Left:  channelLevel{Peak: snap.Peak[0], RMS: snap.RMS[0]},
			Right: channelLevel{Peak: snap.Peak[1], RMS: snap.RMS[1]},
		},
		PhaseCorr:  snap.PhaseCorr,
		LUFSShort:  snap.LUFSShort,
		Spectrum:   snap.SpectrumBands[:],
		Goniometer: snap.Goniometer[:],
	}
}

// audioShouldEmit is the single source of truth for "should I emit?".
// Always emits live frames (preserves 30 Hz cadence even on identical
// payloads). Suppresses only pending→pending so idle wire is quiet.
func audioShouldEmit(prev, curr any) bool {
	_, prevPending := prev.(*audioPendingEnvelope)
	_, currPending := curr.(*audioPendingEnvelope)
	if prevPending && currPending {
		return false
	}
	return true
}

// MarshalJSON pins float formatting and clamps NaN/Inf at the
// serialization boundary. This is the single chokepoint for those
// policies; do not duplicate the logic elsewhere.
func (e *audioLiveEnvelope) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	buf.WriteString(`"status":"`)
	buf.WriteString(e.Status)
	buf.WriteString(`",`)
	buf.WriteString(`"generation":`)
	buf.WriteString(strconv.FormatUint(e.Generation, 10))
	buf.WriteByte(',')
	buf.WriteString(`"sampleRate":`)
	buf.WriteString(strconv.Itoa(e.SampleRate))
	buf.WriteByte(',')
	buf.WriteString(`"channels":`)
	buf.WriteString(strconv.Itoa(e.Channels))
	buf.WriteByte(',')
	buf.WriteString(`"vu":{"left":{"peak":`)
	writeFloat(&buf, e.VU.Left.Peak, peakClamp)
	buf.WriteString(`,"rms":`)
	writeFloat(&buf, e.VU.Left.RMS, peakClamp)
	buf.WriteString(`},"right":{"peak":`)
	writeFloat(&buf, e.VU.Right.Peak, peakClamp)
	buf.WriteString(`,"rms":`)
	writeFloat(&buf, e.VU.Right.RMS, peakClamp)
	buf.WriteString(`}},`)
	buf.WriteString(`"phaseCorr":`)
	writeFloat(&buf, e.PhaseCorr, corrClamp)
	buf.WriteByte(',')
	buf.WriteString(`"lufsShort":`)
	writeFloat(&buf, e.LUFSShort, lufsClamp)
	buf.WriteByte(',')
	buf.WriteString(`"spectrum":[`)
	for i, v := range e.Spectrum {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeFloat(&buf, v, spectrumClamp)
	}
	buf.WriteString(`],"goniometer":[`)
	for i, pair := range e.Goniometer {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('[')
		writeFloat(&buf, pair[0], peakClamp)
		buf.WriteByte(',')
		writeFloat(&buf, pair[1], peakClamp)
		buf.WriteByte(']')
	}
	buf.WriteString(`]}`)
	return buf.Bytes(), nil
}

// Clamp policies per field type.
type clampPolicy func(float32) float32

func peakClamp(x float32) float32 {
	if isBadFloat32(x) {
		if x > 0 {
			return 1.0
		}
		if x < 0 {
			return -1.0
		}
		return 0
	}
	return x
}

func corrClamp(x float32) float32 {
	if isBadFloat32(x) {
		if x > 0 {
			return 1.0
		}
		if x < 0 {
			return -1.0
		}
		return 0
	}
	return x
}

func lufsClamp(x float32) float32 {
	if isBadFloat32(x) {
		return -100.0
	}
	return x
}

func spectrumClamp(x float32) float32 {
	if isBadFloat32(x) {
		return -90.0
	}
	return x
}

func isBadFloat32(x float32) bool {
	x64 := float64(x)
	return math.IsNaN(x64) || math.IsInf(x64, 0)
}

func writeFloat(buf *bytes.Buffer, x float32, clamp clampPolicy) {
	v := clamp(x)
	buf.WriteString(strconv.FormatFloat(float64(v), 'g', 5, 32))
}
```

- [ ] **Step 4: Wire AudioScopeViewer into server config**

In `internal/chassis/server.go`, locate the `Config` struct and add:

```go
type Config struct {
	// … existing fields …

	// AudioScopeViewer is the optional read-only source for the live
	// audio-analysis snapshot. *core.Manager satisfies it via
	// AudioScopes(). When nil, the chassis emits permanent pending
	// audio frames.
	AudioScopeViewer AudioScopeViewer
}
```

Add to the `Server` struct:

```go
type Server struct {
	// … existing fields …
	audioScopeViewer AudioScopeViewer
}
```

In `New`, store it:

```go
audioScopeViewer: cfg.AudioScopeViewer,
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run TestAudio -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/audio.go internal/chassis/audio_test.go internal/chassis/server.go
git commit -m "feat(chassis): add audio scope envelope and viewer surface"
```

---

## Task 9: Events Integration — 30 Hz Audio Ticker

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Add failing events tests**

Append to `internal/chassis/events_test.go`:

```go
func TestHandleEvents_InitialBurstIncludesAudio(t *testing.T) {
	cfg := nonZeroConfig()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "event: audio\n") {
		t.Errorf("initial burst missing audio event:\n%s", body)
	}
}

func TestHandleEvents_LiveAudioEmitsAtCadence(t *testing.T) {
	// 100 ms of audio ticks should emit exactly 3 live frames even
	// with identical payloads.
	cfg := nonZeroConfig()
	viewer := &countingAudioViewer{
		snap: &core.AudioScopeSnapshot{Generation: 1, SampleRate: 48000, Channels: 2},
	}
	cfg.AudioScopeViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)
	count := strings.Count(w.Body.String(), "event: audio\n")
	// 1 initial + ~3 ticks at 33 ms each = 4 (allow 3-5)
	if count < 3 || count > 5 {
		t.Errorf("audio event count over 100 ms = %d, want 3-5", count)
	}
}

func TestHandleEvents_PendingSuppressedAtCadence(t *testing.T) {
	cfg := nonZeroConfig()
	cfg.AudioScopeViewer = &fakeAudioViewer{snap: nil} // permanent pending
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	count := strings.Count(w.Body.String(), "event: audio\n")
	// Initial burst only; identical pending frames suppressed
	if count != 1 {
		t.Errorf("idle audio event count = %d, want 1 (initial only)", count)
	}
}

func TestHandleEvents_GenerationFlipDirectLiveToLive(t *testing.T) {
	cfg := nonZeroConfig()
	viewer := &mutableAudioViewer{snap: &core.AudioScopeSnapshot{Generation: 1, SampleRate: 48000, Channels: 2}}
	cfg.AudioScopeViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		viewer.setSnap(&core.AudioScopeSnapshot{Generation: 2, SampleRate: 48000, Channels: 2})
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `"generation":1`) {
		t.Errorf("missing gen=1 emission: %s", body)
	}
	if !strings.Contains(body, `"generation":2`) {
		t.Errorf("missing gen=2 emission: %s", body)
	}
}

type countingAudioViewer struct {
	mu   sync.Mutex
	snap *core.AudioScopeSnapshot
	n    int
}

func (c *countingAudioViewer) AudioScopes() *core.AudioScopeSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.snap
}

type mutableAudioViewer struct {
	mu   sync.Mutex
	snap *core.AudioScopeSnapshot
}

func (m *mutableAudioViewer) AudioScopes() *core.AudioScopeSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap
}

func (m *mutableAudioViewer) setSnap(s *core.AudioScopeSnapshot) {
	m.mu.Lock()
	m.snap = s
	m.mu.Unlock()
}
```

If `sync` is not already imported in events_test.go, add it.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run TestHandleEvents_(InitialBurstIncludesAudio|LiveAudio|PendingSuppressed|GenerationFlip) -v
```

Expected: FAIL — no `event: audio` lines in the output.

- [ ] **Step 3: Wire 30 Hz audio ticker into handleEvents**

In `internal/chassis/events.go`, add the package-level constants near other intervals:

```go
// audioTickInterval pins the audio event cadence at exactly 30 Hz.
// time.Second / 30 = 33.333… ms, vs the looser 33 ms literal.
var audioTickInterval = time.Second / 30

// audioEventHz is the rate the meter discovery hook advertises in its
// `sampleHz` field. MUST stay in sync with audioTickInterval.
const audioEventHz = 30
```

In `handleEvents`, add the audio emit closure and ticker after the existing initial-burst emits (after `transport` is emitted, before the `tick := time.NewTicker(chassisTickInterval)`):

```go
	// Audio scope initial burst — emit last in the canonical order.
	var lastAudio any = audioEnvelopeFromViewer(s.audioScopeViewer)
	if err := emit(w, "audio", lastAudio); err != nil {
		return
	}
	flusher.Flush()
```

And add the ticker + closure before the existing `tick := time.NewTicker(chassisTickInterval)`:

```go
	audioTick := time.NewTicker(audioTickInterval)
	defer audioTick.Stop()

	// emitAudio runs in a closure so defer recover() actually fires
	// per-tick. A panic in viewer/marshal skips the frame but does
	// NOT terminate the SSE handler.
	emitAudio := func() (alive bool) {
		alive = true
		defer func() {
			if r := recover(); r != nil {
				// Panic recovered; alive stays true so handler continues
				_ = r
			}
		}()
		curr := audioEnvelopeFromViewer(s.audioScopeViewer)
		if !audioShouldEmit(lastAudio, curr) {
			return true
		}
		if err := emit(w, "audio", curr); err != nil {
			return false
		}
		lastAudio = curr
		flusher.Flush()
		return true
	}
```

Add a new case to the `select` loop:

```go
		case <-audioTick.C:
			if !emitAudio() {
				return
			}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run TestHandleEvents -v
```

Expected: all pass, including pre-existing handle-events tests. If `TestHandleEvents_PendingSuppressedAtCadence` fails because the initial burst's pending envelope and the first tick's envelope are different pointer values (audioShouldEmit compares types), verify the assertion: pending→pending should be type-equal, not pointer-equal.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "feat(chassis): emit audio scope event at 30 Hz"
```

---

## Task 10: Meter Discovery Hook (5A `meter.go` update)

**Files:**
- Modify: `internal/chassis/meter.go`
- Modify: `internal/chassis/meter_test.go`

- [ ] **Step 1: Locate the meter envelope's audioScopes field**

```bash
grep -n "audioScopes" internal/chassis/meter.go
```

The 5A meter envelope builder emits `{"status":"pending"}` for the `audioScopes` slot. Find that emit (likely a struct literal or a map assignment).

- [ ] **Step 2: Write failing discovery-hook test**

Append to `internal/chassis/meter_test.go`:

```go
func TestMeterEnvelope_AudioScopesDiscoveryHookWhenLive(t *testing.T) {
	// When a session is active (Generation > 0 on audio snapshot), the
	// meter event's audioScopes field must advertise the high-rate audio
	// event so meter-only clients can discover it.
	cfg := nonZeroConfig()
	cfg.AudioScopeViewer = &fakeAudioViewer{snap: &core.AudioScopeSnapshot{Generation: 1}}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Build a meter envelope as the live path would — use whatever
	// existing helper meter.go exposes for envelope construction.
	env := buildMeterEnvelopeForTest(s)
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s_ := string(body)
	if !strings.Contains(s_, `"audioScopes":{"status":"live","via":"audio","sampleHz":30}`) {
		t.Errorf("discovery hook missing or wrong: %s", s_)
	}
}

func TestMeterEnvelope_AudioScopesPendingWhenIdle(t *testing.T) {
	cfg := nonZeroConfig() // no AudioScopeViewer → idle
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env := buildMeterEnvelopeForTest(s)
	body, _ := json.Marshal(env)
	if !strings.Contains(string(body), `"audioScopes":{"status":"pending"}`) {
		t.Errorf("idle audioScopes hook wrong: %s", body)
	}
}
```

`buildMeterEnvelopeForTest` is a test helper that calls whatever path `events.go` uses to build the meter envelope — depending on how 5A structured this, it might be a direct function call or a snapshot+envelope chain. If no such helper exists, add a minimal one in `meter_test.go`.

- [ ] **Step 3: Run failing tests**

```bash
go test ./internal/chassis -run TestMeterEnvelope_AudioScopes -v
```

Expected: FAIL.

- [ ] **Step 4: Update meter envelope builder**

In `internal/chassis/meter.go`, find the `audioScopes` emission. Replace with a discovery-hook builder that consults the `AudioScopeViewer`:

```go
type audioScopesHook struct {
	Status   string `json:"status"`
	Via      string `json:"via,omitempty"`
	SampleHz int    `json:"sampleHz,omitempty"`
}

func audioScopesHookFromViewer(v AudioScopeViewer) audioScopesHook {
	if v == nil {
		return audioScopesHook{Status: "pending"}
	}
	snap := v.AudioScopes()
	if snap == nil || snap.Generation == 0 {
		return audioScopesHook{Status: "pending"}
	}
	return audioScopesHook{Status: "live", Via: "audio", SampleHz: audioEventHz}
}
```

Use `audioScopesHookFromViewer(s.audioScopeViewer)` everywhere the meter envelope currently emits the static pending placeholder for `audioScopes`. The `audioEventHz` constant ties the literal to `audioTickInterval` (see Task 9).

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run TestMeterEnvelope_AudioScopes -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/meter.go internal/chassis/meter_test.go
git commit -m "feat(chassis): repurpose meter.audioScopes as audio discovery hook"
```

---

## Task 11: Meter Template + meter.js Subscribe + Canvas Rendering

**Files:**
- Modify: `internal/chassis/templates/meter.html`
- Modify: `internal/chassis/static/meter.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing template + no-fake-values lint tests**

Append to `internal/chassis/chassis_test.go`:

```go
func TestMeterHTML_HasScopeHooks(t *testing.T) {
	cfg := nonZeroConfig()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()
	for _, hook := range []string{
		"data-scope-vu-left",
		"data-scope-vu-right",
		"data-scope-phase",
		"data-scope-lufs",
		"data-scope-spectrum",
		"data-scope-goniometer",
	} {
		if !strings.Contains(body, hook) {
			t.Errorf("meter HTML missing hook %q", hook)
		}
	}
}

func TestMeterJS_NoFakeValueGenerators(t *testing.T) {
	src, err := os.ReadFile("static/meter.js")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, forbidden := range []string{
		"Math.random", "Math.sin(", "Math.cos(", "Math.tan(",
	} {
		if strings.Contains(s, forbidden) {
			t.Errorf("meter.js contains forbidden generator %q (audio scopes must drive real values, not fake animations)", forbidden)
		}
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
go test ./internal/chassis -run "TestMeterHTML_HasScopeHooks|TestMeterJS_NoFakeValueGenerators" -v
```

Expected: FAIL (hooks not present yet; meter.js may or may not contain forbidden generators).

- [ ] **Step 3: Update meter.html with live data hooks**

In `internal/chassis/templates/meter.html`, replace 5A's pending placeholders for the audio scopes with `data-scope-*` hooks. Approximate structure (adapt to existing markup):

```html
<div class="meter-row-3">
  <div class="meter-vu meter-vu-left">
    <div class="vu-bar" data-scope-vu-left></div>
    <span class="vu-label">L</span>
  </div>
  <div class="meter-vu meter-vu-right">
    <div class="vu-bar" data-scope-vu-right></div>
    <span class="vu-label">R</span>
  </div>
  <div class="meter-phase">
    <div class="phase-needle" data-scope-phase></div>
    <span class="phase-label">φ</span>
  </div>
  <div class="meter-lufs" data-scope-lufs>--.- LUFS</div>
</div>

<div class="meter-row-2">
  <canvas class="meter-canvas" data-scope-spectrum width="320" height="80"></canvas>
  <canvas class="meter-canvas" data-scope-goniometer width="80" height="80"></canvas>
</div>
```

Preserve other existing meter row markup; only replace the audio-scope partials with these `data-scope-*` hooks.

- [ ] **Step 4: Add audio subscribe handler + canvas paint loops to meter.js**

In `internal/chassis/static/meter.js`, add at the top level of the existing IIFE:

```js
(() => {
  // … existing 5A meter.js code (kept) …

  // Audio scope renderer — driven by the 30 Hz `audio` SSE event.
  if (!window.Chassis || !window.Chassis.events || !window.Chassis.events.subscribe) {
    console.warn('meter.js: window.Chassis.events.subscribe not available');
    return;
  }

  const vuLeft = document.querySelector('[data-scope-vu-left]');
  const vuRight = document.querySelector('[data-scope-vu-right]');
  const phase = document.querySelector('[data-scope-phase]');
  const lufs = document.querySelector('[data-scope-lufs]');
  const spectrumCanvas = document.querySelector('[data-scope-spectrum]');
  const goniometerCanvas = document.querySelector('[data-scope-goniometer]');
  const spectrumCtx = spectrumCanvas && spectrumCanvas.getContext('2d');
  const goniometerCtx = goniometerCanvas && goniometerCanvas.getContext('2d');

  let lastGeneration = 0;
  let lastSpectrum = null;
  let lastGoniometer = null;
  let isLive = false;

  function renderPending() {
    isLive = false;
    if (vuLeft) vuLeft.style.setProperty('--vu-level', '0');
    if (vuRight) vuRight.style.setProperty('--vu-level', '0');
    if (phase) phase.style.setProperty('--phase-angle', '0deg');
    if (lufs) lufs.textContent = '--.- LUFS';
    if (spectrumCtx) {
      spectrumCtx.clearRect(0, 0, spectrumCanvas.width, spectrumCanvas.height);
    }
    if (goniometerCtx) {
      goniometerCtx.clearRect(0, 0, goniometerCanvas.width, goniometerCanvas.height);
    }
    lastSpectrum = null;
    lastGoniometer = null;
  }

  function renderLive(payload) {
    isLive = true;
    if (vuLeft) vuLeft.style.setProperty('--vu-level', String(payload.vu.left.peak));
    if (vuRight) vuRight.style.setProperty('--vu-level', String(payload.vu.right.peak));
    if (phase) {
      const angle = payload.phaseCorr * 45;
      phase.style.setProperty('--phase-angle', angle + 'deg');
    }
    if (lufs) lufs.textContent = payload.lufsShort.toFixed(1) + ' LUFS';
    lastSpectrum = payload.spectrum;
    lastGoniometer = payload.goniometer;
  }

  function paintFrame() {
    if (!isLive) {
      requestAnimationFrame(paintFrame);
      return;
    }
    if (spectrumCtx && lastSpectrum) {
      const w = spectrumCanvas.width;
      const h = spectrumCanvas.height;
      spectrumCtx.clearRect(0, 0, w, h);
      const barW = w / lastSpectrum.length;
      spectrumCtx.fillStyle = '#0f0';
      for (let i = 0; i < lastSpectrum.length; i++) {
        const db = lastSpectrum[i];
        const norm = Math.max(0, Math.min(1, (db + 60) / 60)); // -60..0 dBFS
        const barH = norm * h;
        spectrumCtx.fillRect(i * barW, h - barH, barW - 1, barH);
      }
    }
    if (goniometerCtx && lastGoniometer) {
      const w = goniometerCanvas.width;
      const h = goniometerCanvas.height;
      // Alpha-blend prior frame for trail effect
      goniometerCtx.fillStyle = 'rgba(0,0,0,0.2)';
      goniometerCtx.fillRect(0, 0, w, h);
      goniometerCtx.fillStyle = '#0f0';
      const cx = w / 2, cy = h / 2;
      const scale = Math.min(w, h) / 2 * 0.9;
      for (const [l, r] of lastGoniometer) {
        const x = cx + (l - r) * scale * 0.707;
        const y = cy - (l + r) * scale * 0.707;
        goniometerCtx.fillRect(x, y, 1, 1);
      }
    }
    requestAnimationFrame(paintFrame);
  }

  window.Chassis.events.subscribe('audio', (ev) => {
    let payload;
    try {
      payload = JSON.parse(ev.data);
    } catch (err) {
      console.warn('meter.js: bad audio payload', ev.data, err);
      return;
    }
    if (payload.status !== 'live') {
      renderPending();
      return;
    }
    if (payload.generation !== lastGeneration) {
      lastGeneration = payload.generation;
      // Generation reset: clear histories (peak-hold, trails)
      lastSpectrum = null;
      lastGoniometer = null;
    }
    renderLive(payload);
  });

  requestAnimationFrame(paintFrame);
})();
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run "TestMeterHTML_HasScopeHooks|TestMeterJS_NoFakeValueGenerators" -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/templates/meter.html internal/chassis/static/meter.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): render audio scopes from live audio event"
```

---

## Task 12: chassis.css Audio-Scope Styles

**Files:**
- Modify: `internal/chassis/static/chassis.css`

- [ ] **Step 1: Add audio scope styles scoped under body.receiver**

Append to `internal/chassis/static/chassis.css`:

```css
body.receiver .vu-bar {
  display: block;
  width: 100%;
  height: 8px;
  background: linear-gradient(to right, #0a0, #aa0 70%, #a00 90%);
  transform-origin: left center;
  transform: scaleX(var(--vu-level, 0));
  transition: transform 60ms linear;
}

body.receiver .phase-needle {
  display: block;
  width: 2px;
  height: 24px;
  background: #888;
  transform-origin: center bottom;
  transform: rotate(var(--phase-angle, 0deg));
  transition: transform 80ms ease-out;
}

body.receiver .meter-lufs {
  font-family: 'DSEG14Classic', monospace;
  font-size: 14px;
  color: #ccc;
}

body.receiver .meter-canvas {
  background: #0a0a0a;
  border: 1px solid #1a1a1a;
  image-rendering: pixelated;
}

body.receiver .meter-vu {
  display: flex;
  align-items: center;
  gap: 4px;
}

body.receiver .vu-label,
body.receiver .phase-label {
  font-family: 'Inter', sans-serif;
  font-size: 9px;
  color: #666;
  text-transform: uppercase;
}
```

- [ ] **Step 2: Run chassis-CSS scope tests to confirm no regressions**

```bash
go test ./internal/chassis -run "TestChassisCSS" -v
```

Expected: PASS. The existing scope-test parses CSS via tdewolff and confirms every selector starts with `body.receiver`; the new rules already comply.

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): add audio scope styles"
```

---

## Task 13: main.go Wiring + End-to-End Integration Test

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`
- Modify: `tests/integration/chassis_test.go` (or create if absent)

- [ ] **Step 1: Wire AudioScopeViewer in main.go**

In `cmd/mister-groovy-relay/main.go`, find the `chassis.Config{...}` literal that builds the chassis server (grep for `chassis.New`). Add:

```go
AudioScopeViewer: coreMgr,
```

…to the literal, matching the existing pattern used for `VisualizerViewer`/`VolumeViewer`/`TransportViewer`.

- [ ] **Step 2: Write failing end-to-end integration test**

In `tests/integration/chassis_test.go`, add (or append to) a build-tagged file:

```go
//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestChassisIntegration_AudioEventEndToEnd(t *testing.T) {
	// Construct a real Manager with a fake plane that publishes a known
	// snapshot. Mount chassis at /receiver. Connect SSE client; assert
	// `audio` event arrives with status: live within 200 ms.
	mgr := core.NewManager(/* test config — adapt to existing helper */)
	// Inject a fake plane that returns a known audio snapshot…
	// (Implementation-specific; adapt to whatever core_test exports.)

	srv := chassis.New(chassis.Config{
		Manager:          mgr,
		AudioScopeViewer: mgr,
		Version:          "test",
		StartedAt:        time.Now(),
		HostIP:           "127.0.0.1",
	})
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/receiver/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE request: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 8192)
	deadline := time.Now().Add(300 * time.Millisecond)
	var collected strings.Builder
	for time.Now().Before(deadline) {
		n, _ := resp.Body.Read(buf)
		if n > 0 {
			collected.Write(buf[:n])
			if strings.Contains(collected.String(), "event: audio") {
				break
			}
		}
	}
	body := collected.String()
	if !strings.Contains(body, "event: audio\n") {
		t.Errorf("no audio event in stream:\n%s", body)
	}
	if !strings.Contains(body, `"status":"pending"`) && !strings.Contains(body, `"status":"live"`) {
		t.Errorf("audio event missing status discriminator:\n%s", body)
	}
}
```

Adapt the constructor calls (`core.NewManager`, `chassis.New`) to match the actual exported APIs.

- [ ] **Step 3: Run tests**

```bash
go test -tags=integration ./tests/integration/... -run TestChassisIntegration_AudioEvent -v
```

Expected: PASS. If `chassis.New` doesn't currently take an `AudioScopeViewer` field and main.go is the only call site, the test scaffolding may need a small adapter — adjust as needed.

- [ ] **Step 4: Run full test suite to confirm no regressions**

```bash
go test ./...
go test -race ./...
go test -tags=integration ./tests/integration/...
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/main.go tests/integration/chassis_test.go
git commit -m "feat(chassis): wire AudioScopeViewer and add end-to-end test"
```

---

## Task 14: Phase 1 Debt Migration (GATED on 5A)

**Precondition check before starting this task.**

- [ ] **Step 1: Verify 5A's subscribe() helper is in the tree**

```bash
grep -n "subscribe" internal/chassis/static/vfd-live.js
```

Expected output: at least one line containing `subscribe` as a function definition (e.g., `subscribe: function(name, handler) {` or `subscribe(name, handler) {`).

If NO `subscribe` function is present, **halt this task** with the following blocker message:

> Task 14 (Phase 1 debt migration) cannot proceed: 5A's `subscribe()` helper has not yet merged into `internal/chassis/static/vfd-live.js`. Either:
> (a) Wait for 5A to merge, then resume Task 14.
> (b) Land the `subscribe()` helper in this task as a prerequisite step — coordinate with 5A's author to avoid double-shipping.

Tasks 1-13 are complete and shippable without this task. Do not block them.

If `subscribe` IS present, proceed to Step 2.

**Files:**
- Modify: `internal/chassis/static/transport.js`
- Modify: `internal/chassis/static/visualizer-bank.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 2: Write failing subscribe-pattern lint test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestChassisJS_NoRawEventSourcePattern(t *testing.T) {
	files := []string{"static/transport.js", "static/visualizer-bank.js"}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			s := string(src)
			for _, forbidden := range []string{
				"events.source.addEventListener",
				"chassis:eventsource",
			} {
				if strings.Contains(s, forbidden) {
					t.Errorf("%s contains forbidden raw-EventSource pattern %q (use subscribe() instead)", f, forbidden)
				}
			}
			if !strings.Contains(s, "window.Chassis.events.subscribe(") {
				t.Errorf("%s does not use subscribe() at all", f)
			}
		})
	}
}
```

- [ ] **Step 3: Run test to confirm it fails**

```bash
go test ./internal/chassis -run TestChassisJS_NoRawEventSourcePattern -v
```

Expected: FAIL (both files still use the raw pattern).

- [ ] **Step 4: Migrate transport.js**

In `internal/chassis/static/transport.js`, find the existing event listener setup (typically `events.source.addEventListener('transport', …)` plus a `chassis:eventsource` listener for reconnect-time reattach). Replace with:

```js
window.Chassis.events.subscribe('transport', (ev) => {
  // existing handler body — unchanged
});
```

Remove the `chassis:eventsource` document-level listener (subscribe() handles reconnect dedupe internally).

- [ ] **Step 5: Migrate visualizer-bank.js**

Same migration in `internal/chassis/static/visualizer-bank.js` for the `visualizer` event:

```js
window.Chassis.events.subscribe('visualizer', (ev) => {
  // existing handler body — unchanged
});
```

- [ ] **Step 6: Run tests to confirm they pass**

```bash
go test ./internal/chassis -v
```

Expected: all chassis tests PASS, including the new subscribe-pattern lint and any existing transport/visualizer functional tests.

- [ ] **Step 7: Manual sanity check**

If practical, start the chassis and verify in a browser that transport controls and visualizer-bank buttons still respond to SSE events after the migration. (Skip if no live test rig available; the lint + existing tests are the structural safety net.)

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/static/transport.js internal/chassis/static/visualizer-bank.js internal/chassis/chassis_test.go
git commit -m "refactor(chassis): migrate transport.js and visualizer-bank.js to subscribe()"
```

---

## Final Verification

After all 14 tasks (or 13 + deferred Task 14), run the full verification suite:

- [ ] **Step 1: Run unit tests, race detector, and integration tests**

```bash
go test ./...
go test -race ./...
go test -tags=integration ./tests/integration/...
```

Expected: all PASS.

- [ ] **Step 2: Run linter**

```bash
make lint
```

Expected: clean.

- [ ] **Step 3: Verify import_check_test.go still passes without modification**

```bash
go test ./internal/chassis -run TestProductionImports -v
```

Expected: PASS. No new cross-package imports were introduced (chassis still imports only core; core gains a dataplane import but it already had one for plane types).

- [ ] **Step 4: Push and open PR**

```bash
git push -u origin <branch>
gh pr create --title "feat(chassis): receiver audio-analysis scopes (Spec 5B)" --body "$(cat <<'EOF'
## Summary

- Adds receiver-chassis audio-analysis scopes: L/R VU, phase correlation, short-term LUFS (BS.1770-4), 32-band Hann-windowed FFT spectrum, 256-point goniometer
- DSP runs inline on the data-plane field-tick goroutine, publishes lock-free via `atomic.Pointer` at average 30 Hz (Bresenham-style cadence; works at both NTSC and PAL field rates)
- Streams to the chassis at 30 Hz over a new `audio` SSE event on the existing `/receiver/events` connection
- Repurposes 5A's reserved `meter.audioScopes` slot as a discovery hook
- Migrates `transport.js` and `visualizer-bank.js` from raw `EventSource` to the `subscribe()` helper

## Test plan

- [ ] `go test ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `go test -tags=integration ./tests/integration/...` passes
- [ ] Manual browser verification: connect to `/receiver`, start a cast, confirm spectrum/goniometer/VU/phase/LUFS scopes animate; stop cast, confirm scopes clear to idle backdrop

Spec: docs/superpowers/specs/2026-05-25-receiver-chassis-audio-scopes-design.md

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Notes for the Implementer

- The spec's "Single-producer goroutine invariant" is load-bearing. Do NOT move snapshot construction (the `publish()` method) into a separate goroutine for "performance" — it would race the next `Observe`'s in-place updates of `audioMeterState`.
- The `audioShouldEmit` helper has cadence-preserving semantics: always emit live frames even if numerically identical. Do NOT add a change-detect optimization here; the 30 Hz cadence is the wire contract.
- The custom `MarshalJSON` on `audioLiveEnvelope` is the single place where float formatting (5 sig figs) and NaN/Inf clamping happen. Do not duplicate clamping logic elsewhere.
- The FFT cost shows up only on publish ticks (~30 Hz). Total CPU per second is ~3 ms on the field-tick goroutine — well under budget. If profiling shows otherwise, the first thing to check is whether the FFT is accidentally running on every chunk instead of every publish tick.
- Generation flips: the chassis envelope construction does NOT filter snapshots by generation. Clients are the authority. Tests cover both transition types (via pending and direct).
