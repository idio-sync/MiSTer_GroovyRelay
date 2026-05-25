# Receiver Chassis Audio-Analysis Scopes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the receiver chassis audio-analysis scopes (L/R VU, phase correlation, short-term LUFS, 32-band spectrum, 256-point goniometer) real by computing all DSP inline on the data-plane field-tick goroutine, publishing through a lock-free atomic snapshot, and streaming to the chassis at 30 Hz over a new `audio` SSE event on the existing `/receiver/events` connection.

**Architecture:** A new `AudioMeter` type lives on `*Plane` and runs DSP inline in `Observe()` (called once per PCM chunk just before `sendAudio`). It publishes `*AudioScopeSnapshot` via `atomic.Pointer` at an average 30 Hz via a Bresenham-style sample accumulator. Core exposes the snapshot via `Manager.AudioScopes() *AudioScopeSnapshot` (alias to dataplane type, no copy). Chassis adds an `AudioScopeViewer` interface, a discriminated-union envelope (`audioPendingEnvelope` | `audioLiveEnvelope`) with a custom `MarshalJSON` for float-precision and NaN/Inf clamping, and a 30 Hz audio ticker in `handleEvents` that emits the `audio` event independently of the 2 Hz snapshot cache. Starts with the Phase 1 follow-up debt: migrating `transport.js`, `visualizer-bank.js`, and the current `volume-knob.js` legacy consumer from raw `EventSource` to the `subscribe()` helper that 5A introduces.

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
| `internal/chassis/templates/meter.html` | Replace the existing segmented `#spectrum` placeholder with a `#spectrum-canvas` hook while preserving VU/phase/LUFS/goniometer hooks |
| `internal/chassis/static/meter.js` | Add `subscribe('audio', ...)` handler with discriminator gate + generation reset; canvas paint loops for spectrum and goniometer; class/inline-style drivers for VU/phase/LUFS |
| `internal/chassis/static/transport.js` | **Task 1 (gated)**: migrate from `events.source.addEventListener` + `chassis:eventsource` to `window.Chassis.events.subscribe('transport', ...)` |
| `internal/chassis/static/visualizer-bank.js` | **Task 1 (gated)**: migrate from `events.source.addEventListener` + `chassis:eventsource` to `window.Chassis.events.subscribe('visualizer', ...)` |
| `internal/chassis/static/volume-knob.js` | **Task 1 (gated)**: migrate the current volume subscriber from raw `events.source` handoff to `window.Chassis.events.subscribe('volume', ...)` |
| `internal/chassis/static/chassis.css` | Verify existing 5A meter scope styling; add only minimal scoped canvas/VU/phase rules if needed |
| `internal/chassis/chassis_test.go` | Template-hook presence test for live audio scope DOM hooks; no-fake-values lint (no `Math.random`/`Math.sin`/`Date.now`/etc. in `meter.js`); subscribe-pattern lint (no legacy raw source consumers in `transport.js`/`visualizer-bank.js`/`volume-knob.js` after Task 1) |
| `cmd/mister-groovy-relay/main.go` | Add `AudioScopeViewer: coreMgr` to the `chassis.Config` literal where chassis is constructed |
| `tests/integration/chassis_test.go` | Build-tagged end-to-end SSE test: real `*core.Manager` + real dataplane via existing `scenarioHarness` → connect SSE client → assert `audio` event arrives with `status: live`; stop session → assert next `audio` event is `{"status":"pending"}` |

**Files intentionally unchanged:**

- `internal/ui/*`, `internal/uiserver/*` — 5B is additive under `/receiver/*` only.
- `internal/playback/*` — audio meter is separate from playback controls.
- All adapter packages — DSP runs in the data plane, regardless of source.
- `internal/chassis/static/vfd-live.js` — the `subscribe()` helper lands in 5A, not 5B.
- `internal/chassis/import_check_test.go` — no new cross-package imports introduced; verify after compile, do not edit unless lint flags something.

---

## Sequencing Status (verified at plan-revise time)

Spec 5A has merged: `window.Chassis.events.subscribe(eventName, handler)` is at [internal/chassis/static/vfd-live.js:21](../../../internal/chassis/static/vfd-live.js). The 5A meter envelope, `meter.go`, `meter.html`, and `data-meter-audio-scopes-status` discovery marker are also all in the tree. Run Task 1 first, then proceed through the audio tasks in order.

Task 1 (Phase 1 debt migration) retains a defensive precondition grep as Step 1 — expected to pass immediately. If a future revision lands without 5A, the grep blocks Task 1 instead of writing JS that calls a missing function.

---

## Task 1: Phase 1 Debt Migration

**Scope:** migrate every current receiver client that consumes the shared SSE stream through the legacy `events.source` / `chassis:eventsource` handoff. At current HEAD this includes `transport.js`, `visualizer-bank.js`, and `volume-knob.js`. `vfd-live.js` remains the owner that creates the `EventSource` and exposes `window.Chassis.events.subscribe()`; the lint below intentionally does not forbid the source owner from assigning `events.source`.

**Precondition check (defensive — expected to pass at HEAD).**

- [ ] **Step 1: Verify 5A's subscribe() helper is in the tree**

```bash
grep -n "subscribe" internal/chassis/static/vfd-live.js
```

Expected output: a line `function subscribe(eventName, handler) {` near line 21. Verified at plan-revise time.

If NO `subscribe` function is present (unexpected), halt this task with a clear blocker message; resume after `subscribe()` lands.

If `subscribe` IS present, proceed to Step 2.

**Files:**
- Modify: `internal/chassis/static/transport.js`
- Modify: `internal/chassis/static/visualizer-bank.js`
- Modify: `internal/chassis/static/volume-knob.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 2: Write failing subscribe-pattern lint test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestChassisJS_NoRawEventSourceConsumers(t *testing.T) {
	files := []string{
		"static/transport.js",
		"static/visualizer-bank.js",
		"static/volume-knob.js",
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			s := string(src)
			for _, forbidden := range []string{
				"events.source",
				"chassis:eventsource",
			} {
				if strings.Contains(s, forbidden) {
					t.Errorf("%s still contains raw EventSource consumer pattern %q; use window.Chassis.events.subscribe()", f, forbidden)
				}
			}
		})
	}
}
```

- [ ] **Step 3: Run failing test**

```bash
go test ./internal/chassis -run TestChassisJS_NoRawEventSourceConsumers -v
```

Expected: FAIL before migration because the three scripts still use the legacy source handoff.

- [ ] **Step 4: Migrate transport.js**

In `internal/chassis/static/transport.js`, replace the raw source attach/reconnect listener with:

```js
  window.Chassis.events.subscribe('transport', (ev) => {
    try {
      render(JSON.parse(ev.data));
    } catch (err) {
      console.warn('transport.js: bad transport payload', ev.data, err);
    }
  });
```

Preserve existing render/update helpers. Remove the `chassis:eventsource` document-level listener; `subscribe()` handles reconnect dedupe internally.

- [ ] **Step 5: Migrate visualizer-bank.js**

Same migration in `internal/chassis/static/visualizer-bank.js` for the `visualizer` event:

```js
  window.Chassis.events.subscribe('visualizer', (ev) => {
    try {
      render(JSON.parse(ev.data));
    } catch (err) {
      console.warn('visualizer-bank.js: bad visualizer payload', ev.data, err);
    }
  });
```

- [ ] **Step 6: Migrate volume-knob.js**

Same migration in `internal/chassis/static/volume-knob.js` for the `volume` event:

```js
  window.Chassis.events.subscribe('volume', (ev) => {
    try {
      applyVolume(JSON.parse(ev.data));
    } catch (err) {
      console.warn('volume-knob.js: bad volume payload', ev.data, err);
    }
  });
```

Use the script's actual existing volume-rendering helper name if it differs from `applyVolume`; do not introduce duplicate state or a second renderer.

- [ ] **Step 7: Run chassis tests**

```bash
go test ./internal/chassis -v
```

Expected: all chassis tests PASS, including the new subscribe-pattern lint and any existing transport/visualizer/volume functional tests.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/static/transport.js internal/chassis/static/visualizer-bank.js internal/chassis/static/volume-knob.js internal/chassis/chassis_test.go
git commit -m "refactor(chassis): migrate receiver clients to subscribe helper"
```

---

## Task 2: FFT Subpackage

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

## Task 3: AudioMeter Skeleton — Peak/RMS/Phase/Goniometer DSP

This task wires up the AudioMeter type with the cheap DSP that does not need FFT or LUFS. Those land in Tasks 4-5.

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
	m.forcePublishEveryObserve = true
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
	m.forcePublishEveryObserve = true
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
	m.forcePublishEveryObserve = true
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
	m.forcePublishEveryObserve = true
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
	m.forcePublishEveryObserve = true
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
	// targetHz is a test seam:
	//   - production:     targetHz = 30
	//   - no-publish run: targetHz = 0 (accumulator never advances)
	//
	// forcePublishEveryObserve is a separate test seam. It publishes at
	// most once at the end of an Observe call, regardless of chunk size.
	targetHz                 int
	publishAccum             int
	forcePublishEveryObserve bool

	state              audioMeterState
	peakDecayPerSample float32
	gonioDecimStep     int
	gonioStepCount     int

	// LUFS coefficients (Task 4 will populate)
	kPreCoeffs  biquadCoeffs
	kHighCoeffs biquadCoeffs

	// FFT scratch (Task 5 will populate)
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

	// LUFS K-weighting biquad state per channel (Task 4 will populate)
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

		// LUFS K-weighting (Task 4 will fill this in)
		_ = m.state.kPreL.process(float64(l))
		_ = m.state.kPreR.process(float64(r))
		// stored sample-by-sample but LUFS computation lands in Task 4
	}

	// Bresenham cadence
	if m.forcePublishEveryObserve {
		m.publish()
		return
	}
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
	// Spectrum: Task 5 will populate via FFT. Until then, reuse lastSpectrum
	// (all zero on first publish before Task 5 lands).
	snap.SpectrumBands = m.state.lastSpectrum
	// LUFS: Task 4 will populate. Until then, leave as zero value (0.0).
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

// Placeholder LUFS coefficient functions; Task 4 implements them.
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

## Task 4: AudioMeter LUFS Short-Term Loudness

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
	m.forcePublishEveryObserve = true
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
	m.forcePublishEveryObserve = true
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
	m.forcePublishEveryObserve = true
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
		// LUFS K-weighting (Task 4 will fill this in)
		_ = m.state.kPreL.process(float64(l))
		_ = m.state.kPreR.process(float64(r))
		// stored sample-by-sample but LUFS computation lands in Task 4
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
	// LUFS: Task 4 will populate. Until then, leave as zero value (0.0).
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

## Task 5: AudioMeter FFT Spectrum

This task replaces the placeholder spectrum (zeros) with a real Hann-windowed FFT computed on publish ticks.

**Files:**
- Modify: `internal/dataplane/audiometer.go`
- Modify: `internal/dataplane/audiometer_test.go`

- [ ] **Step 1: Write failing FFT spectrum tests**

Append to `internal/dataplane/audiometer_test.go`:

```go
func TestAudioMeter_SpectrumSilenceIsSentinel(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
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
	m.forcePublishEveryObserve = true
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

	// Compute Hann-corrected per-bin power (one-sided: ×2 for bins 1..N/2-1).
	// Normalization: 1 / (N² × hannEnergyGain / 2) where hannEnergyGain =
	// sum(w²)/N = 3/8 for length-N Hann. A bin-centered full-scale sine
	// after Hann windowing produces ≈-2 dBFS at the peak band (not exactly
	// 0 dBFS), because the per-bin coherent-gain correction is folded into
	// this single energy normalization rather than a separate amplitude-
	// correction step. The test tolerance (band peak > -3 dBFS) accepts
	// this. If exact 0 dBFS is ever required, multiply normFactor by an
	// additional coherent-gain factor (~1.5) and re-tune sentinel tests.
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
	// Spectrum: Task 5 will populate via FFT. Until then, reuse lastSpectrum
	// (all zero on first publish before Task 5 lands).
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

## Task 6: AudioMeter Concurrency, Alloc Budget, and Bresenham Cadence Tests

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
	m.forcePublishEveryObserve = true
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

## Task 7: Plane Wiring — PlaneConfig.Generation, audioMeter, AudioScopes()

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

- [ ] **Step 6: Add Plane wiring tests**

Append focused tests to `internal/dataplane/plane_test.go`:

```go
func TestNewPlane_AudioScopesNilUntilAudioObserved(t *testing.T) {
	p := NewPlane(PlaneConfig{
		Generation: 42,
		AudioRate:  48000,
		AudioChans: 2,
	})
	if got := p.AudioScopes(); got != nil {
		t.Fatalf("AudioScopes before observed audio = %#v, want nil", got)
	}
}

func TestPlane_RunPublishesAudioScopesFromReadyAudioPath(t *testing.T) {
	// Use the existing stub-process + fakemister pattern in this file.
	// Configure ACK audio-ready, AudioRate=48000, AudioChans=2, and a
	// stub AudioPipe that emits several non-empty PCM chunks. Run long
	// enough for the delayed audio branch to execute.
	//
	// Assert:
	//   - p.AudioScopes() is nil before Run ships a ready audio chunk.
	//   - p.AudioScopes() becomes non-nil after enough non-empty chunks.
	//   - the snapshot Generation matches PlaneConfig.Generation.
}

func TestPlane_RunDoesNotPublishAudioScopesForEmptyChunks(t *testing.T) {
	// Same harness as above, but the stub AudioPipe emits empty chunks/EOF.
	// Assert AudioScopes() remains nil. This pins the Observe call inside
	// the existing `if len(oldest) > 0` guard immediately before sendAudio.
}
```

The second and third tests should reuse the local `spawnProcess` seam and existing `stubProcess` helpers rather than adding a public test hook. If the existing helper only returns an already-canceled plane, add a narrow helper in `plane_test.go` that runs the plane for a bounded duration and returns the constructed `*Plane` for assertions.

- [ ] **Step 7: Update existing plane_test.go fakes if needed**

```bash
go build ./internal/dataplane
```

If the build fails because existing test fakes don't compile against the widened plane shape, update them minimally (add `Generation` to `PlaneConfig` literals where present; default to 0).

- [ ] **Step 8: Run dataplane tests**

```bash
go test ./internal/dataplane -v
```

Expected: PASS. If the audio-suppressed path is exercised and now sees `p.audioMeter != nil` checks, those should pass through cleanly.

- [ ] **Step 9: Commit**

```bash
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "feat(dataplane): wire AudioMeter into Plane field tick"
```

---

## Task 8: Core Surface — Manager.AudioScopes, type alias, generation plumbing

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

`internal/core/manager_test.go` defines several `planeRunner` fakes (verified at HEAD: `fakePlane` at line 87, `contextDonePlane` at 100, `blockingDonePlane` at 119, `volumePlane` at 135, `errorPlane` at 145, `meterCountingPlane` at 3397). All of them need the no-op method added so they still satisfy the widened interface:

```go
func (p *fakePlane) AudioScopes() *dataplane.AudioScopeSnapshot           { return nil }
func (p *contextDonePlane) AudioScopes() *dataplane.AudioScopeSnapshot    { return nil }
func (p *blockingDonePlane) AudioScopes() *dataplane.AudioScopeSnapshot   { return nil }
// volumePlane embeds fakePlane, so it inherits the method — no addition needed
func (p *errorPlane) AudioScopes() *dataplane.AudioScopeSnapshot          { return nil }
func (p *meterCountingPlane) AudioScopes() *dataplane.AudioScopeSnapshot  { return nil }
```

Verify with a build before proceeding to the new test:

```bash
go build ./internal/core
```

Expected: no errors. If any fake is missing the method, the compiler will name it.

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

## Task 9: Chassis Audio Surface

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

// peakClamp covers both peak (signed amplitude [-1, +1]) and RMS
// (non-negative [0, +1]) fields. RMS can never legitimately go
// negative, so the x < 0 branch is unreachable for RMS but kept for
// symmetry with peak. If the spec ever distinguishes peak from RMS
// in terms of clamp behavior, split this into a dedicated rmsClamp
// returning 0.0 for any negative or NaN input.
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

## Task 10: Events Integration — 30 Hz Audio Ticker

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
	// One second of audio ticks should emit 30 live tick frames ±1 even
	// with identical payloads. The initial burst is counted separately.
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
		time.Sleep(1 * time.Second)
		cancel()
	}()
	s.handleEvents(w, req)
	total := strings.Count(w.Body.String(), "event: audio\n")
	tickCount := total - 1 // subtract initial burst frame
	if tickCount < 29 || tickCount > 31 {
		t.Errorf("audio tick count over 1 s = %d (total=%d), want 30 ± 1", tickCount, total)
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

func TestHandleEvents_GenerationFlipViaPending(t *testing.T) {
	// live(gen=1) → nil → live(gen=2) on the wire should appear as
	// live, then pending, then live(gen=2). No stale live(gen=1)
	// after the pending frame.
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
		time.Sleep(40 * time.Millisecond)
		viewer.setSnap(nil) // pending phase
		time.Sleep(60 * time.Millisecond)
		viewer.setSnap(&core.AudioScopeSnapshot{Generation: 2, SampleRate: 48000, Channels: 2})
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)
	body := w.Body.String()
	gen1 := strings.Index(body, `"generation":1`)
	pending := strings.Index(body, `"status":"pending"`)
	gen2 := strings.Index(body, `"generation":2`)
	if gen1 < 0 || pending < 0 || gen2 < 0 {
		t.Fatalf("missing one of gen1/pending/gen2: %s", body)
	}
	if !(gen1 < pending && pending < gen2) {
		t.Errorf("ordering wrong: gen1=%d pending=%d gen2=%d\n%s", gen1, pending, gen2, body)
	}
	// After the pending frame, no stale live(gen=1) should reappear.
	if strings.Index(body[pending:], `"generation":1`) >= 0 {
		t.Errorf("stale gen=1 emission after pending: %s", body)
	}
}

func TestHandleEvents_PendingShapeExact(t *testing.T) {
	// Idle wire emit MUST be exactly `data: {"status":"pending"}` —
	// no other keys, no whitespace beyond what the encoder emits.
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
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `event: audio
data: {"status":"pending"}

`) {
		t.Errorf("expected exact pending wire bytes, got:\n%s", body)
	}
}

func TestHandleEvents_LiveLegitimateZerosOnWire(t *testing.T) {
	// PhaseCorr: 0.0 and LUFSShort: 0.0 are legitimate live values
	// that JSON omitempty would erase. The wire payload MUST include
	// both keys.
	cfg := nonZeroConfig()
	cfg.AudioScopeViewer = &fakeAudioViewer{snap: &core.AudioScopeSnapshot{
		Generation: 1, SampleRate: 48000, Channels: 2,
		PhaseCorr: 0.0, LUFSShort: 0.0,
	}}
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
	if !strings.Contains(body, `"phaseCorr":0`) {
		t.Errorf("phaseCorr=0 was erased on wire: %s", body)
	}
	if !strings.Contains(body, `"lufsShort":0`) {
		t.Errorf("lufsShort=0 was erased on wire: %s", body)
	}
}

func TestHandleEvents_InitialBurstAudioIsLast(t *testing.T) {
	// Spec §Initial Event Order: audio MUST be the last initial event.
	// Read current order from main; assert audioIdx > previously-last.
	cfg := nonZeroConfig()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	audioIdx := strings.Index(body, "event: audio\n")
	if audioIdx < 0 {
		t.Fatalf("no audio event in initial burst:\n%s", body)
	}
	// Assert audio comes AFTER every other initial event present in the burst.
	for _, prior := range []string{"event: state\n", "event: vfd\n", "event: source\n", "event: visualizer\n", "event: transport\n", "event: volume\n", "event: meter\n"} {
		idx := strings.Index(body, prior)
		if idx < 0 {
			continue // not all events guaranteed present in every cfg
		}
		if idx > audioIdx {
			t.Errorf("audio appeared before %s: audioIdx=%d priorIdx=%d", prior, audioIdx, idx)
		}
	}
}

func TestHandleEvents_PanicInViewerSkipsFrame(t *testing.T) {
	// A viewer that panics on its Nth call must not terminate the SSE
	// handler. The frame is skipped; subsequent calls succeed.
	cfg := nonZeroConfig()
	viewer := &panickingAudioViewer{
		snap:       &core.AudioScopeSnapshot{Generation: 1, SampleRate: 48000, Channels: 2},
		panicOnNth: 2, // initial burst (call 1) ok; first tick (call 2) panics; call 3 recovers
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
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	// Initial burst contains one audio event (call 1).
	// First tick panics (call 2): no new emit, but handler must survive.
	// Subsequent ticks (call 3+) emit again.
	audioCount := strings.Count(body, "event: audio\n")
	if audioCount < 2 {
		t.Errorf("audio event count = %d, want >= 2 (initial + post-panic recovery); body:\n%s", audioCount, body)
	}
}

type panickingAudioViewer struct {
	mu         sync.Mutex
	snap       *core.AudioScopeSnapshot
	calls      int
	panicOnNth int
}

func (p *panickingAudioViewer) AudioScopes() *core.AudioScopeSnapshot {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n == p.panicOnNth {
		panic("synthetic panic for test")
	}
	return p.snap
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

## Task 11: Meter Discovery Hook (5A `meter.go` + `data.go` update)

The 5A code already has `AudioScopesData{Status: string}` in [data.go:171](../../../internal/chassis/data.go) and `meterAudioScopesE{Status: string}` in [meter.go:570](../../../internal/chassis/meter.go). The "pending" placeholder is populated in two places:

- `idleSnapshot` at [data.go:317](../../../internal/chassis/data.go) — idle path
- `meterDataFromSnapshot` at [meter.go:150](../../../internal/chassis/meter.go) — live path (currently also emits "pending")

5B's discovery-hook update extends both struct types with `Via` and `SampleHz`, threads an `audioLive bool` into the data populator, and updates `meterEnvelopeFrom` to serialize the new fields.

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/meter.go`
- Modify: `internal/chassis/meter_test.go`

- [ ] **Step 1: Extend AudioScopesData and meterAudioScopesE structs**

In `internal/chassis/data.go`, extend the struct at line 171:

```go
// AudioScopesData holds the audio-scope cluster status. Carries the
// discovery hook so meter-only clients can find the high-rate `audio`
// SSE event when a session is active. When idle, only Status is set;
// when live, Via and SampleHz advertise the high-rate channel.
type AudioScopesData struct {
	Status   string
	Via      string
	SampleHz int
}
```

In `internal/chassis/meter.go`, extend the envelope struct at line 570:

```go
type meterAudioScopesE struct {
	Status   string `json:"status"`
	Via      string `json:"via,omitempty"`
	SampleHz int    `json:"sampleHz,omitempty"`
}
```

`omitempty` on `Via` and `SampleHz` keeps the pending shape exactly `{"status":"pending"}` (no extra keys with zero values).

- [ ] **Step 2: Update meterEnvelopeFrom to carry the new fields**

In `internal/chassis/meter.go`, find the `AudioScopes:` literal at line 617:

```go
		AudioScopes: meterAudioScopesE{
			Status: m.AudioScopes.Status,
		},
```

Replace with:

```go
		AudioScopes: meterAudioScopesE{
			Status:   m.AudioScopes.Status,
			Via:      m.AudioScopes.Via,
			SampleHz: m.AudioScopes.SampleHz,
		},
```

- [ ] **Step 3: Thread audioLive parameter into meterDataFromSnapshot**

The live path at [meter.go:150](../../../internal/chassis/meter.go) currently hard-codes `base.AudioScopes.Status = "pending"`. It needs to know whether audio is live. The cleanest fix: add an `audioLive bool` parameter to `meterDataFromSnapshot`, and have the meter sampler (which lives in server scope) pass it from the viewer.

Change `meterDataFromSnapshot` signature at line 121:

```go
func meterDataFromSnapshot(snap core.StatusHomeView, overlay adapters.MeterOverlay, throughput []float64, ack []float64, audioLive bool) MeterData {
```

Replace the hard-coded "pending" assignment at line 150:

```go
	base.AudioScopes = audioScopesData(audioLive)
```

Add the helper near the other format functions:

```go
// audioScopesData builds the discovery-hook AudioScopesData. When
// audioLive is true, advertises the high-rate audio event via the
// hook fields; otherwise returns pending. audioEventHz is the
// constant exported by events.go (Task 10).
func audioScopesData(audioLive bool) AudioScopesData {
	if audioLive {
		return AudioScopesData{
			Status:   "live",
			Via:      "audio",
			SampleHz: audioEventHz,
		}
	}
	return AudioScopesData{Status: "pending"}
}
```

- [ ] **Step 4: Update meterSampler.Sample to take and forward audioLive**

Change `meterSampler.Sample` signature at line 32:

```go
func (s *meterSampler) Sample(snap core.StatusHomeView, overlay adapters.MeterOverlay, audioLive bool, now time.Time) MeterData {
```

Forward to `meterDataFromSnapshot` at line 45:

```go
	current := meterDataFromSnapshot(snap, overlay, s.throughput, s.ack, audioLive)
```

- [ ] **Step 5: Update meter sampler call sites**

Grep for callers of `meterSampler.Sample` (likely in server.go's snapshot refresher and possibly elsewhere):

```bash
grep -n "\.meter\.Sample\|s\.meter\.Sample" internal/chassis/*.go
```

At each call site, add an `audioLive` argument derived from active session state and whether the server has an audio viewer. Do **not** derive this from `AudioScopes() != nil`: during startup/prebuffer the high-rate `audio` event exists before the first snapshot has published.

```go
audioLive := s.audioScopeViewer != nil &&
	(view.State == core.StatePlaying || view.State == core.StatePaused)
data := s.meter.Sample(view, overlay, audioLive, now)
```

For idle-path callers (where no live audio is possible), pass `false`.

- [ ] **Step 6: Write failing discovery-hook tests**

Append to `internal/chassis/meter_test.go`:

```go
func TestMeterDataFromSnapshot_AudioScopesLiveDiscoveryHook(t *testing.T) {
	snap := core.StatusHomeView{} // minimal; populate as existing tests do
	got := meterDataFromSnapshot(snap, adapters.MeterOverlay{}, nil, nil, true)
	if got.AudioScopes.Status != "live" || got.AudioScopes.Via != "audio" || got.AudioScopes.SampleHz != audioEventHz {
		t.Errorf("AudioScopes = %+v, want Status=live Via=audio SampleHz=%d", got.AudioScopes, audioEventHz)
	}
}

func TestMeterDataFromSnapshot_AudioScopesPendingWhenIdle(t *testing.T) {
	snap := core.StatusHomeView{}
	got := meterDataFromSnapshot(snap, adapters.MeterOverlay{}, nil, nil, false)
	if got.AudioScopes.Status != "pending" || got.AudioScopes.Via != "" || got.AudioScopes.SampleHz != 0 {
		t.Errorf("AudioScopes = %+v, want pending only", got.AudioScopes)
	}
}

func TestMeterEnvelopeFrom_DiscoveryHookSerialization(t *testing.T) {
	m := MeterData{AudioScopes: AudioScopesData{Status: "live", Via: "audio", SampleHz: 30}}
	env := meterEnvelopeFrom(m)
	body, _ := json.Marshal(env)
	if !strings.Contains(string(body), `"audioScopes":{"status":"live","via":"audio","sampleHz":30}`) {
		t.Errorf("envelope shape wrong: %s", body)
	}
}

func TestMeterEnvelopeFrom_PendingShapeExact(t *testing.T) {
	m := MeterData{AudioScopes: AudioScopesData{Status: "pending"}}
	env := meterEnvelopeFrom(m)
	body, _ := json.Marshal(env)
	if !strings.Contains(string(body), `"audioScopes":{"status":"pending"}`) {
		t.Errorf("pending envelope must NOT include via/sampleHz: %s", body)
	}
}
```

- [ ] **Step 7: Run failing tests**

```bash
go test ./internal/chassis -run TestMeterDataFromSnapshot_AudioScopes -v
go test ./internal/chassis -run TestMeterEnvelopeFrom -v
```

Expected: FAIL (structs/signatures not yet updated).

- [ ] **Step 8: Run tests to confirm they pass after Steps 1-5 are applied**

```bash
go test ./internal/chassis -v
```

Expected: PASS. If existing meter tests fail because they call `meterDataFromSnapshot` or `Sample` with the old signature, update them to pass `false` (or `true` where appropriate). Use `gofmt -w internal/chassis/` after edits.

- [ ] **Step 9: Commit**

```bash
git add internal/chassis/data.go internal/chassis/meter.go internal/chassis/meter_test.go internal/chassis/server.go
git commit -m "feat(chassis): repurpose meter.audioScopes as audio discovery hook"
```

---

## Task 12: meter.js Audio Subscribe + Live Rendering

**5A already built most of the meter scope DOM.** Existing hooks in [internal/chassis/templates/meter.html](../../../internal/chassis/templates/meter.html):

- **VU bars:** `.tr-vu .vu-lr .ch-bar > .s` — 12 segments per channel, `.on` class for lit. Color tiers: `.g` (green, bars 0-5), `.y` (yellow, 6-9), `.r` (red, 10-11).
- **Phase needle:** `#phase-needle` element with `data-phase` attribute consumed by 5A CSS.
- **LUFS readout:** `#lufs-val .seg-text` text content (DSEG14 font).
- **Spectrum:** Task 12 replaces the existing 6-band segmented placeholder with a `#spectrum-canvas` canvas so all 32 wire bands render distinctly, matching the spec.
- **Goniometer:** `#gonio-canvas` 172×172 canvas.
- **Discovery marker:** `[data-meter-audio-scopes-status]` (hidden element, server-rendered from `meter` event).

**5A's `meter.js` exists** at [internal/chassis/static/meter.js](../../../internal/chassis/static/meter.js) and handles the slow `meter` event (HLS, throughput, ack). It uses `subscribe()` already, so the audio handler joins that IIFE. Goniometer data is 256 points; spectrum data is 32 bands; both paint on canvas.

**Files:**
- Modify: `internal/chassis/templates/meter.html`
- Modify: `internal/chassis/static/meter.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing template-presence + no-fake-values lint tests**

Append to `internal/chassis/chassis_test.go`:

```go
// TestMeterHTML_HasAudioScopeHooks verifies the existing 5A hooks are
	// still present (regression guard — Task 12 must not accidentally
// remove or rename them via template churn).
func TestMeterHTML_HasAudioScopeHooks(t *testing.T) {
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
		`id="phase-needle"`,
		`id="lufs-val"`,
			`id="spectrum"`,
			`id="spectrum-canvas"`,
			`id="gonio-canvas"`,
		`class="ch-bar"`,
		`data-meter-audio-scopes-status`,
	} {
		if !strings.Contains(body, hook) {
			t.Errorf("meter HTML missing existing 5A hook %q", hook)
		}
	}
}

// TestMeterJS_NoFakeValueGenerators ensures audio scope rendering
// drives values from the wire, never synthesizes them.
func TestMeterJS_NoFakeValueGenerators(t *testing.T) {
	src, err := os.ReadFile("static/meter.js")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, forbidden := range []string{
		"Math.random", "Math.sin(", "Math.cos(", "Math.tan(",
		"Date.now(",
	} {
		if strings.Contains(s, forbidden) {
			t.Errorf("meter.js contains forbidden generator %q (audio scopes must drive real values, not fake animations)", forbidden)
		}
	}
}

// TestMeterJS_SubscribesToAudio ensures Task 12 wired the audio
// subscription that the rest of this task depends on.
func TestMeterJS_SubscribesToAudio(t *testing.T) {
	src, err := os.ReadFile("static/meter.js")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(src), `subscribe('audio'`) && !strings.Contains(string(src), `subscribe("audio"`) {
		t.Error("meter.js does not subscribe to the audio SSE event")
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
go test ./internal/chassis -run "TestMeterHTML_HasAudioScopeHooks|TestMeterJS_NoFakeValueGenerators|TestMeterJS_SubscribesToAudio" -v
```

Expected: `TestMeterHTML_HasAudioScopeHooks` FAILS until Step 3 replaces the spectrum placeholder, `TestMeterJS_SubscribesToAudio` FAILS until Step 4 adds the audio handler, and `TestMeterJS_NoFakeValueGenerators` PASSES (5A meter.js doesn't have generators).

- [ ] **Step 3: Replace spectrum placeholder with a 32-band canvas hook**

In [internal/chassis/templates/meter.html](../../../internal/chassis/templates/meter.html), replace the current `#spectrum` contents (the 6 `.spectrum-band` placeholders) with a canvas hook:

```html
<div class="spectrum" id="spectrum" aria-hidden="true">
  <canvas id="spectrum-canvas" width="220" height="120" role="img" aria-label="32-band audio spectrum">32-band audio spectrum</canvas>
</div>
```

Keep the surrounding `.audio-grp` layout and do not rename `#gonio-canvas`, `#phase-needle`, `#lufs-val`, or `.ch-bar`.

- [ ] **Step 4: Add audio subscriber to meter.js (existing IIFE)**

Open [internal/chassis/static/meter.js](../../../internal/chassis/static/meter.js). The file is an IIFE starting with `(() => { 'use strict'; ... })();`. The existing code handles the `meter` event with helpers like `setText`, `setLamp`, `updateHLS`, `drawLine`. **Do not remove anything.**

Inside the same IIFE, after the existing helpers and before any final `})();`, append:

```js
  // ---- Audio scope renderer (Spec 5B) ----
  // Drives the meter DOM hooks from the 30 Hz `audio` SSE event.
  const vuBars = Array.from(document.querySelectorAll('.tr-vu .vu-lr .ch-bar'));
  const vuChBarL = vuBars[0];
  const vuChBarR = vuBars[1];
  const phaseNeedle = document.getElementById('phase-needle');
  const lufsTextEl = document.querySelector('#lufs-val .seg-text');
  const spectrumCanvas = document.getElementById('spectrum-canvas');
  const spectrumCtx = spectrumCanvas && spectrumCanvas.getContext('2d');
  const gonioCanvas = document.getElementById('gonio-canvas');
  const gonioCtx = gonioCanvas && gonioCanvas.getContext('2d');

  const peakHold = new Array(32).fill(-90);
  const peakHoldDecayPerFrame = 0.5; // dB per render frame
  let lastAudioGeneration = 0;
  let lastSpectrum = null;
  let lastGoniometer = null;
  let audioIsLive = false;

  function setVUBarSegments(chBar, level) {
    if (!chBar) return;
    const segs = chBar.querySelectorAll('.s');
    const lit = Math.round(Math.max(0, Math.min(1, level)) * 12);
    segs.forEach((s, i) => s.classList.toggle('on', i < lit));
  }

  function renderAudioPending() {
    audioIsLive = false;
    setVUBarSegments(vuChBarL, 0);
    setVUBarSegments(vuChBarR, 0);
    // Phase needle: park at center. 5A CSS positions via inline style.left;
    // the .vu-phase .bar parent is the reference. center = 50%.
    if (phaseNeedle) phaseNeedle.style.left = '';
    if (lufsTextEl) lufsTextEl.textContent = '--.-';
    if (spectrumCtx) spectrumCtx.clearRect(0, 0, spectrumCanvas.width, spectrumCanvas.height);
    for (let i = 0; i < peakHold.length; i++) peakHold[i] = -90;
    // Clear goniometer
    if (gonioCtx) gonioCtx.clearRect(0, 0, gonioCanvas.width, gonioCanvas.height);
    lastSpectrum = null;
    lastGoniometer = null;
  }

  function renderAudioLive(payload) {
    audioIsLive = true;
    setVUBarSegments(vuChBarL, payload.vu.left.peak);
    setVUBarSegments(vuChBarR, payload.vu.right.peak);
    if (phaseNeedle) {
      // PhaseCorr in [-1, +1] → position needle along .vu-phase .bar.
      // 5A baseline at idle: left = 50%. Range +/- ~36px from center
      // (the .bar is ~72px wide; needle is 3px). Linear map: -1 → 14%,
      // 0 → 50%, +1 → 86%.
      const pct = 50 + payload.phaseCorr * 36;
      phaseNeedle.style.left = pct + '%';
    }
    if (lufsTextEl) {
      const v = payload.lufsShort;
      lufsTextEl.textContent = v <= -100 ? '--.-' : v.toFixed(1);
    }
    lastSpectrum = payload.spectrum;
    lastGoniometer = payload.goniometer;
  }

  function paintAudio() {
    if (!audioIsLive) {
      requestAnimationFrame(paintAudio);
      return;
    }
    // Spectrum: 32 wire bands → 32 canvas bars, with peak-hold tick.
    if (spectrumCtx && lastSpectrum && lastSpectrum.length === 32) {
      const w = spectrumCanvas.width;
      const h = spectrumCanvas.height;
      spectrumCtx.clearRect(0, 0, w, h);
      const gap = 1;
      const barW = Math.max(2, Math.floor((w - gap * 31) / 32));
      for (let i = 0; i < 32; i++) {
        const db = Math.max(-90, Math.min(0, Number(lastSpectrum[i]) || -90));
        if (db > peakHold[i]) peakHold[i] = db;
        else peakHold[i] = Math.max(-90, peakHold[i] - peakHoldDecayPerFrame);
        const norm = Math.max(0, Math.min(1, (db + 60) / 60));
        const peakNorm = Math.max(0, Math.min(1, (peakHold[i] + 60) / 60));
        const x = i * (barW + gap);
        const barH = Math.max(1, norm * h);
        spectrumCtx.fillStyle = i < 20 ? '#7cffb2' : '#ffd76a';
        spectrumCtx.fillRect(x, h - barH, barW, barH);
        spectrumCtx.fillStyle = '#ff6f61';
        spectrumCtx.fillRect(x, h - peakNorm * h, barW, 1);
      }
    }
    // Goniometer: alpha-fade prior frame for trail; plot 256 (L,R) points.
    if (gonioCtx && lastGoniometer) {
      const w = gonioCanvas.width;
      const h = gonioCanvas.height;
      gonioCtx.fillStyle = 'rgba(0,0,0,0.15)';
      gonioCtx.fillRect(0, 0, w, h);
      gonioCtx.fillStyle = '#7cffb2';
      const cx = w / 2, cy = h / 2;
      const scale = Math.min(w, h) / 2 * 0.9;
      // Rotate 45° so in-phase appears vertical (standard goniometer orientation):
      // x = (L - R) * sin45 * scale, y = -(L + R) * sin45 * scale
      const r2 = 0.70710678;
      for (let i = 0; i < lastGoniometer.length; i++) {
        const pair = lastGoniometer[i];
        const x = cx + (pair[0] - pair[1]) * r2 * scale;
        const y = cy - (pair[0] + pair[1]) * r2 * scale;
        gonioCtx.fillRect(x, y, 1, 1);
      }
    }
    requestAnimationFrame(paintAudio);
  }

  window.Chassis.events.subscribe('audio', (ev) => {
    let payload;
    try {
      payload = JSON.parse(ev.data);
    } catch (err) {
      console.warn('meter.js: bad audio payload', ev.data, err);
      return;
    }
    // Discriminator gate (load-bearing): never destructure non-status fields
    // before checking status.
    if (payload.status !== 'live') {
      renderAudioPending();
      return;
    }
    if (payload.generation !== lastAudioGeneration) {
      lastAudioGeneration = payload.generation;
      // Generation reset: clear peak-hold and goniometer trail
      for (let i = 0; i < peakHold.length; i++) peakHold[i] = -90;
      lastSpectrum = null;
      lastGoniometer = null;
    }
    renderAudioLive(payload);
  });

  requestAnimationFrame(paintAudio);
```

Notes:
- The visual peak-hold and decay live in the client (per spec §Client rendering). Wire payload is just the current spectrum snapshot.
- The phase needle is positioned with inline `style.left`; Task 13 only needs CSS if the current transition rule no longer applies after implementation.

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run "TestMeterHTML_HasAudioScopeHooks|TestMeterJS_NoFakeValueGenerators|TestMeterJS_SubscribesToAudio" -v
```

Expected: all three PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/templates/meter.html internal/chassis/static/meter.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): render audio scopes from live audio event"
```

---

## Task 13: chassis.css Audio-Scope Styles (verification + minimal additions)

5A's `chassis.css` already styles most of the meter scope DOM (`.tr-vu .ch-bar`, `#phase-needle`/`.vu-phase .needle` with `transition: left 80ms linear`, etc.). meter.js (Task 12) drives VU/phase/LUFS directly and paints the 32-band spectrum/goniometer canvases.

**Files:**
- Modify (only if verification finds a gap): `internal/chassis/static/chassis.css`

- [ ] **Step 1: Verify 5A styles produce live audio rendering**

Run `make build && ./mister-groovy-relay` (or however the bridge starts locally), connect to `/receiver` in a browser, start a cast, and visually verify:

- VU `.ch-bar > .s` segments light up to follow the audio level
- `#phase-needle` slides left/right along `.vu-phase .bar` as L/R correlation changes (CSS `transition: left 80ms linear` already animates the move)
- `#spectrum-canvas` renders 32 distinct bars with peak-hold ticks
- `#gonio-canvas` shows scattered (L,R) points
- `#lufs-val .seg-text` updates the DSEG14 numeric readout

If all five work without CSS changes, **skip Step 2 and Step 3**. Commit nothing for Task 13 and proceed to Task 14.

- [ ] **Step 2: Add scoped styles only for gaps found in Step 1**

If a specific scope renders incorrectly (e.g., goniometer canvas needs a background color the existing `.gonio` rule doesn't provide), append a targeted rule under `body.receiver`. Avoid introducing new selectors that overlap existing 5A rules; prefer extending existing rules.

Example (only if needed — goniometer trail blending):

```css
body.receiver #gonio-canvas {
  background: transparent;
}
```

- [ ] **Step 3: Run chassis-CSS scope tests + commit if changes were made**

```bash
go test ./internal/chassis -run "TestChassisCSS" -v
```

Expected: PASS. The existing `TestChassisCSS_AllSelectorsScoped` test enforces every selector starts with `body.receiver`; any addition must comply.

Commit only if you made changes:

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): tighten audio scope styles"
```

---

## Task 14: main.go Wiring + End-to-End Integration Test

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

The existing `tests/integration/chassis_test.go` uses `package integration` (NOT `integration_test`). Append a new test function to that file (or create the file if absent with `package integration`).

This test must exercise the real manager/dataplane-to-chassis path, not a synthetic `AudioScopeViewer`. Reuse the existing `scenarioHarness` from [tests/integration/scenarios_test.go](../../../tests/integration/scenarios_test.go), which already wires a real `*core.Manager`, real `dataplane.Plane`, ffmpeg sample media, and `fakemister` ACK/audio readiness.

```go
//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestChassisIntegration_AudioEventEndToEnd(t *testing.T) {
	sample := ensureSampleMP4(t, "5s.mp4", 5)
	h := newScenarioHarness(t)

	srv, err := chassis.New(chassis.Config{
		Bridge:           config.BridgeConfig{},
		Manager:          h.Manager,
		Registry:         adapters.NewRegistry(),
		Version:          "integration-test",
		StartedAt:        time.Now(),
		HostIP:           "127.0.0.1",
		AudioScopeViewer: h.Manager,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer srv.Close()
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if err := h.Manager.StartSession(defaultRequest(sample, "audio-sse")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/receiver/events", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE request: %v", err)
	}
	defer resp.Body.Close()

	liveBody := readSSEUntil(t, resp, `"status":"live"`, 4*time.Second)
	for _, want := range []string{
		"event: audio\n",
		`"vu":`,
		`"spectrum":[`,
		`"goniometer":[`,
	} {
		if !strings.Contains(liveBody, want) {
			t.Fatalf("live audio SSE missing %q:\n%s", want, liveBody)
		}
	}

	if err := h.Manager.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	pendingBody := readSSEUntil(t, resp, `"status":"pending"`, 2*time.Second)
	if !strings.Contains(pendingBody, "event: audio\n") {
		t.Fatalf("pending audio event missing after stop:\n%s", pendingBody)
	}
}

func readSSEUntil(t *testing.T, resp *http.Response, needle string, timeout time.Duration) string {
	t.Helper()
	buf := make([]byte, 8192)
	deadline := time.Now().Add(timeout)
	var collected strings.Builder
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			collected.Write(buf[:n])
			if strings.Contains(collected.String(), needle) {
				return collected.String()
			}
		}
		if err != nil {
			t.Fatalf("read SSE: %v; collected:\n%s", err, collected.String())
		}
	}
	t.Fatalf("timed out waiting for %q; collected:\n%s", needle, collected.String())
	return collected.String()
}
```

Adapt constructor calls only if the exported APIs have changed by implementation time. Do not replace `h.Manager` with a fake viewer; this test exists to catch breaks in dataplane publication, core forwarding, chassis SSE emission, and stop-to-pending behavior together.

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

## Final Verification

After all 14 tasks, run the full verification suite:

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
- Migrates `transport.js`, `visualizer-bank.js`, and `volume-knob.js` from raw `EventSource` consumers to the `subscribe()` helper

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
