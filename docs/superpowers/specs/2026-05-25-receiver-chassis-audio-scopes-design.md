# Receiver Chassis Audio-Analysis Scopes — Phase 2 / Spec 5B Design

**Status:** Draft.

**Scope:** Second and final sub-project of Phase 2 (Telemetry Meters). Makes the receiver chassis audio-analysis scopes real by computing per-channel L/R VU, phase correlation, short-term LUFS, a 32-band spectrum, and a 256-point goniometer from the s16le PCM the bridge already holds in memory, and streaming the result to the chassis at 30 Hz over a new `audio` SSE event on the existing `/receiver/events` connection. Also folds in the [Phase 1 follow-up debt](2026-05-24-receiver-chassis-meter-telemetry-design.md#L421) called out in the 5A spec: migrating `transport.js` and `visualizer-bank.js` off the raw `window.Chassis.events.source` / `chassis:eventsource` pattern onto the `subscribe()` helper that 5A introduces.

## Context

[Phase 0](2026-05-21-receiver-chassis-foundation-design.md) shipped the chassis chrome at `/receiver`. [Phase 1](2026-05-21-receiver-chassis-vfd-live-design.md) wired live VFD, transport, and visualizer state through the shared `/receiver/events` SSE stream. [Phase 2 / Spec 5A](2026-05-24-receiver-chassis-meter-telemetry-design.md) made the low-rate meter screen real — session, pipeline, source, HLS, network, ACK, field-order, and link telemetry — and reserved the `audioScopes` key in the `meter` event with `{"status": "pending"}` to mark the audio scopes as deliberately quiet. The 5A spec also added a `window.Chassis.events.subscribe(eventName, handler)` helper to `vfd-live.js` and noted that two existing client scripts (`transport.js`, `visualizer-bank.js`) still use the raw `EventSource` pattern; the migration to `subscribe()` was deferred to "5B or a small follow-up."

5B is that resolution. After 5B lands, every chassis client script uses one subscription idiom, and every meter-row data field on the v24 reference mockup at [docs/superpowers/reference/2026-05-21-receiver-v24.html](../reference/2026-05-21-receiver-v24.html) is driven by real data — completing Phase 2 and clearing the receiver-chassis path for Phase 3 (cast initiation).

Prior art: [docs/plans/2026-05-20-audio-vu-telemetry.md](../../plans/2026-05-20-audio-vu-telemetry.md) drafted an audio-meter design targeting the old `/ui/*` surface with a dedicated `/ui/telemetry/audio` endpoint and a fan-out broker in a new `internal/telemetry/` package. That plan was never implemented and is now superseded; 5B reuses its DSP-on-PCM-in-the-field-tick instinct and its atomic-snapshot pattern, but rejects the broker and dedicated endpoint because the chassis already streams SSE through `/receiver/events`.

## Goals

1. Add a single `audio` named event to `GET /receiver/events` carrying the full audio-analysis telemetry — L/R VU, phase correlation, short-term LUFS, 32-band spectrum, and 256-point goniometer — at 30 Hz.
2. Compute all DSP inline on the existing data-plane field-tick goroutine, with zero new locks, zero heap allocations on non-publish ticks, and one bounded snapshot allocation only on publish ticks.
3. Publish DSP results through a lock-free atomic snapshot pointer on `*Plane`, read by `core.Manager.AudioScopes()`, and surfaced to chassis via a new `AudioScopeViewer` interface — matching the existing `VisualizerViewer` / `VolumeViewer` / `TransportViewer` pattern.
4. Bypass the 2 Hz chassis snapshot cache for the audio event; the 30 Hz audio ticker reads the viewer directly so audio cadence is independent of meter cadence.
5. Migrate `transport.js` and `visualizer-bank.js` onto `window.Chassis.events.subscribe()`. After 5B, no chassis client script uses the raw `events.source` / `chassis:eventsource` pattern.
6. Replace the 5A pending hooks in `meter.html` and `meter.js` with live data hooks. Audio scopes never animate fake values; idle/pending state is explicit.
7. Repurpose 5A's reserved `meter.audioScopes` slot as a discovery hook so meter-only clients can learn that the high-rate `audio` event exists (rather than seeing the slot permanently pending).
8. Keep `/ui/*` unchanged. 5B is additive under `/receiver/*`.

## Non-Goals

- True-peak (inter-sample peak) measurement. Sample-peak only.
- Integrated or momentary LUFS. Only short-term LUFS (3 s sliding K-weighted window) ships in 5B.
- Per-frame ACK round-trip measurement. `LastACKAge` from 5A is still the freshness signal at 2 Hz.
- Any change to `/ui/*`. The old UI keeps its existing audio-telemetry surface (none) until the eventual cutover.
- A telemetry pub/sub broker, a new `internal/telemetry/` package, or a dedicated SSE endpoint for audio. The existing `/receiver/events` stream is the only transport.
- Mobile/responsive polish for the audio canvases. Phase 5 owns that pass.
- Adding new font files, CSS files, or external JS dependencies. 5B reuses the existing chassis asset surface.
- `Last-Event-ID` SSE replay of the `audio` event. The chassis stream doesn't issue `id:` lines today and 5B doesn't add them. If chassis ever adopts SSE replay (e.g., for survival across a deploy), the `audio` event MUST be excluded from any buffered history — a 30 Hz stream of ~6 KB frames multiplied by replay duration would explode memory. The audio stream is real-time-only by contract; reconnects start fresh from the next live snapshot.

## Design Decisions

| Decision | Resolution |
|---|---|
| Spec scope | All five audio analyses ship in one cut (spectrum, goniometer, VU, phase, LUFS-short). |
| Transport | New `audio` SSE event on existing `/receiver/events` at 30 Hz, alongside the 2 Hz `meter` event. |
| DSP placement | Inline on the field-tick goroutine in `internal/dataplane/plane.go`, immediately before `sendAudio(oldest)`. |
| Snapshot pattern | `atomic.Pointer[AudioScopeSnapshot]` on `*Plane`, lock-free read. |
| Core surface | `*core.Manager` gains `AudioScopes() *AudioScopeSnapshot`; loads the active plane under the existing manager mutex, then reads the plane snapshot atomically. |
| Chassis surface | New `AudioScopeViewer` interface, structurally satisfied by `*core.Manager`; matches existing viewer pattern. |
| Cache strategy | Audio event **bypasses** `s.cache` and reads the viewer directly each tick. |
| FFT | Small in-house radix-2 real FFT in `internal/dataplane/fft/` subpackage. ~150 lines, no new go.mod deps. |
| LUFS variant | Short-term (S-loudness, 3 s sliding K-weighted window) only. Integrated and momentary LUFS deferred. |
| Phase 1 debt | Migrate `transport.js` and `visualizer-bank.js` to `subscribe()` as Task 1 of the implementation plan, before any audio work. |
| 5A `meter.audioScopes` field | Repurpose as a discovery hook: emit `{"status":"live","via":"audio","sampleHz":30}` while a session is active, `{"status":"pending"}` while idle. Meter-only clients can find the high-rate `audio` stream without subscribing to it. |
| Pause behavior | Plane teardown on pause (see [manager.go pauseLocked](../../../internal/core/manager.go) — `m.plane = nil` after cancel) means there is no paused-but-live viewer. Pause emits one pending audio frame; clients clear the scopes to the idle/pending presentation. No `paused: true` envelope shape. |
| Generation flip | Emit one synthetic pending frame, then live frames; clients reset histories on generation change. |

## Wire Contract — `audio` Event

The audio event payload is one of two distinct shapes, distinguished by the `status` discriminator.

**Live:**

```json
{
  "status": "live",
  "generation": 42,
  "sampleRate": 48000,
  "channels": 2,
  "vu": {
    "left":  { "peak": 0.82, "rms": 0.47 },
    "right": { "peak": 0.79, "rms": 0.45 }
  },
  "phaseCorr": 0.91,
  "lufsShort": -14.2,
  "spectrum":   [-48.1, -42.0, /* … 32 floats total, dBFS per band */],
  "goniometer": [[0.12, -0.08], [0.11, -0.07], /* … 256 (L,R) pairs */]
}
```

**Pending** (no plane, paused session, or `generation == 0`):

```json
{ "status": "pending" }
```

**Schema rules:**

- Clients MUST ignore unknown top-level keys (matches the 5A rule).
- `status` is the canonical liveness signal. When `status == "pending"`, no other keys are present. When `status == "live"`, **every** field above is present, including legitimate zeros (`phaseCorr: 0.0` for uncorrelated stereo, `lufsShort: -23.0` for any value).
- `generation` matches `core.SessionStatus.Generation`. Used by clients to reset peak-hold and goniometer trail state on session change.
- `spectrum` is exactly 32 floats, dBFS, logarithmically-spaced bands covering 20 Hz–20 kHz. Bands beyond the Nyquist for the current `sampleRate` clamp to a sentinel low value (`-90.0`). At the bridge's typical 48 kHz audio rate the Nyquist is 24 kHz, so the sentinel only applies on lower-rate sources (22.05 kHz, 11.025 kHz).
- `goniometer` is exactly 256 `[L, R]` pairs, each in `[-1.0, +1.0]`, evenly decimated from the most recent ~50 ms window of input PCM. The decimation factor is `(sampleRate * 0.050) / 256` — at 48 kHz that's ~9, at 44.1 kHz ~8, at 22.05 kHz ~4. Length is fixed at 256 across all sample rates so clients can preallocate.
- `phaseCorr` is the Pearson correlation of L and R over a 300 ms window, in `[-1.0, +1.0]`.
- `lufsShort` is in dB. A sentinel of `-100.0` represents "below measurable floor / silence."
- `vu.left.peak` and `vu.left.rms` are linear in `[0.0, 1.0]`. Clients apply their own log/dB mapping for the segmented bar display.
- Mono streams report VU only on `left`; `right` mirrors `left`. `channels` is the authoritative count.

**Schema discriminator (load-bearing):** clients MUST gate on `status` before reading any other field. Do not infer pending from missing/zero values. This rule constrains the server side too: the live shape MUST NOT use Go's `encoding/json` `omitempty` on any scalar field, because `omitempty` would erase legitimate-zero values like `phaseCorr: 0.0`. See §Chassis surface for the two-struct encoding pattern that enforces this at compile time.

**Float formatting:** server emits floats via `strconv.FormatFloat(x, 'g', 5, 32)` (5 significant figures, `float32` precision). Pinning this is necessary so the per-frame wire size is bounded and so tests can assert byte-stable outputs. Pure-zero values serialize as `"0"`, never erased.

**Forward-compat:** future scopes (e.g. true-peak, integrated LUFS, per-band peak-hold) add new top-level keys. The schema has no version field; key absence is the only break signal, and clients ignore unknowns.

**Per-frame size:** ~6–7 KB serialized JSON in the live shape, dominated by the 256-pair goniometer array (256 × 2 floats × ~12 chars including brackets/commas ≈ 6 KB) plus spectrum (~600 bytes) and scalars. At 30 Hz that's ~200 KB/s per connected tab (~1.6 Mbps) — comfortable on a LAN, visible on a busy NIC. The pending shape is ~22 bytes. If 200 KB/s grows uncomfortable later (e.g. several tabs over a WAN tunnel), the goniometer is the obvious knob: dropping to 128 pairs cuts roughly half; quantizing spectrum to int8 dBFS saves another ~10%. Neither would break the schema for clients that already follow the discriminator rule. The implementation plan SHOULD measure the actual byte count once with a representative envelope and update this paragraph with the measured number.

## Initial Event Order

The 5A spec extended the canonical initial-burst order to `state, vfd, source, visualizer, transport, meter`. **5B owns one ordering rule and one only: `audio` is the final initial event.**

The volume-knob spec (in flight in parallel) inserts `volume` somewhere in the middle of the existing sequence. The relative order of `volume` and `meter` is owned by whichever of those two specs ships second — that spec extends the test assertion accordingly. 5B's responsibility is to append `audio` to whatever order is in place when 5B's plan starts.

Concretely, the implementation plan reads the current state of `events_test.go` at task time:

- Add one more event index (`audioIdx`) to the test's index parses.
- Add one more ordering constraint: `audioIdx > <whatever-was-previously-last>`.
- The preceding event list in the assertion is left exactly as `main` has it at the moment 5B's plan begins; do not retype prior events.

This wording prevents the test fixture from going stale based on which of 5A's follow-ups (volume-knob, 5B) merges first. The §Testing section below references this rule rather than hardcoding any specific preceding-event list.

## Architecture

### Glossary

5B's DSP discussion uses a handful of terms with subtle distinctions. Pinning them once here so the cadence math and test assertions are unambiguous:

- **Sample frame** (or just **frame** in DSP context): one `(L, R)` pair for stereo, one `L` for mono. The unit `samplesInChunk` in the Bresenham accumulator below counts **sample frames**, not interleaved samples. Concretely: `samplesInChunk := len(pcm) / (bytesPerSample * channels)`. For 800 frames of stereo s16le PCM, `len(pcm) == 3200`, and `samplesInChunk == 800`.
- **Sample**: one signed-int16 value on the wire (PCM byte view). Interleaved-sample count is `samplesInChunk * channels`. Used only when discussing wire format, never in cadence math.
- **Chunk**: one `Observe(pcm, channels, sampleRate)` call worth of PCM, sized by `ReadAudioFromPipeContext`. Roughly one field period at typical rates (~16.7 ms at 60 Hz, ~20 ms at 50 Hz).
- **Field tick**: the dataplane's per-field timer fire that drives one `Observe` call (one chunk). Not the same as a publish tick.
- **Publish tick**: an `Observe` call where the Bresenham accumulator rolls over, causing the meter to compute spectrum and publish a new snapshot. Averages 30 Hz; cadence is irregular at non-NTSC field rates.
- **Audio tick** (chassis-side): the `audioTickInterval` ticker inside `handleEvents` that reads `s.audioScopeViewer` and emits the SSE `audio` event. Exactly 30 Hz; decoupled from publish ticks.

### DSP — `internal/dataplane/audiometer.go`

A new `AudioMeter` type owns the running DSP state and the published snapshot pointer:

```go
type AudioMeter struct {
    generation uint64

    // running per-channel state (peak decay, RMS sliding window,
    // K-weighting biquad state for LUFS, FFT input ring)
    state audioMeterState

    // lock-free published snapshot. Observe() stores into this pointer;
    // Plane.AudioScopes() loads it. Single producer (field-tick goroutine),
    // many readers (one per connected SSE handler, plus tests).
    snapshot atomic.Pointer[AudioScopeSnapshot]

    // publish cadence control: Bresenham-style sample accumulator.
    // Observe adds samplesInChunk * targetHz; when the accumulator reaches
    // sampleRate, the meter publishes and subtracts sampleRate. This hits an
    // average 30 Hz at both 50 Hz and 59.94/60 Hz field cadences.
    //
    // targetHz is the test seam:
    //   - production: targetHz = 30
    //   - no-publish test run: targetHz = 0 (accumulator never advances)
    //   - always-publish test run: targetHz = sampleRate (accumulator
    //     crosses threshold every single Observe call, regardless of chunk size)
    //
    // The accumulator carries state across Observe calls and is reset only
    // by AudioMeter construction. sampleRate is assumed constant for the
    // meter's lifetime; see §Sample rate hot-swap below.
    targetHz int
    publishAccum int
}

// Observe is called once per PCM chunk on the field-tick goroutine.
// Allocation-free on non-publish ticks. Updates running DSP state, runs FFT
// on publish ticks (~30 Hz), and publishes a fresh snapshot when the
// sample accumulator rolls over.
func (m *AudioMeter) Observe(pcm []byte, channels int, sampleRate int)

// AudioScopes returns a pointer to the latest published snapshot, or
// nil if nothing has been published yet. Lock-free. Returned pointer
// is read-only — callers MUST NOT mutate the pointee.
func (m *AudioMeter) AudioScopes() *AudioScopeSnapshot
```

The snapshot type:

```go
type AudioScopeSnapshot struct {
    Generation    uint64           // matches core session generation; 0 = idle
    SampleRate    int              // input rate at time of snapshot
    Channels      int              // 1 or 2
    Peak          [2]float32       // per-channel sample peak, linear [0,1]
    RMS           [2]float32       // per-channel RMS, linear [0,1]
    PhaseCorr     float32          // Pearson L/R correlation, [-1, +1]
    LUFSShort     float32          // dB; -100.0 sentinel for "silence"
    SpectrumBands [32]float32      // dBFS per band, logarithmically spaced
    Goniometer    [256][2]float32  // last 256 (L,R) pairs, decimated
    PublishedAt   time.Time        // wallclock for staleness checks
}
```

No `Paused` field: pause tears down the plane, so there is no paused-but-publishing state to represent. See §Plane integration for the full pause/prebuffer behavior.

`AudioMeter` is initialized with the session generation that `core.Manager.startPlaneLocked` already receives. Every published snapshot is stamped with that generation so clients can reset peak-hold and goniometer history on session changes without guessing from transport state.

`Observe` publishes a fresh `*AudioScopeSnapshot` only when the sample accumulator crosses its 30 Hz threshold, keeping per-second allocations bounded and predictable. At 48 kHz/60 Hz this is roughly every other chunk; at 48 kHz/50 Hz the accumulator alternates publish gaps to average 30 Hz instead of falling to 25 Hz or jumping to 50 Hz. On non-publish ticks, running DSP state is updated in place on `audioMeterState` (no allocation). Readers always see a consistent snapshot — atomic publication via `atomic.Pointer.Store` is a single CPU-ordered write.

**Single-producer goroutine invariant (load-bearing):** snapshot construction reads from `audioMeterState` after the same `Observe` call has just finished updating it, on the field-tick goroutine. Both the running-state update and the publish-build run inline in `Observe`; there is no separate publish goroutine and there must not be one. If a future contributor extracts the publish path into a background goroutine (e.g., for "performance"), the publish would race the next `Observe`'s in-place updates of `audioMeterState`, producing torn snapshots that `go test -race` would not reliably catch (the runtime DSP state has no `sync.Pointer` semantics). Tests should pin this invariant by asserting publish-tick callbacks observe the same goroutine ID as the most recent `Observe` call.

### DSP cost budget per chunk

| Stage | Approx cost | Notes |
|---|---|---|
| Peak + RMS, both channels | ~1 µs | Per-sample max + accumulate, then divide on publish |
| Phase correlation accumulation | ~3 µs | Three running sums (Σl, Σr, Σlr) over the window |
| LUFS K-weighting biquads | ~5 µs | Two biquads per channel, 3 s mean-square accumulator |
| Goniometer ring write | ~1 µs | Decimated copy into a 256-deep ring |
| FFT (publish ticks only, ~30 Hz) | ~80 µs amortized | 1024-pt Hann-windowed real FFT, binned to 32 log bands; only runs on publish ticks |
| **Per non-publish chunk** | **~10 µs** | DSP state updates only; no FFT, no snapshot publish |
| **Per publish chunk** | **~90 µs** | DSP updates + FFT + snapshot build + atomic store |

Per-second budget at 48 kHz/60 Hz: 30 publish chunks × ~90 µs + 30 non-publish chunks × ~10 µs ≈ 3 ms/s of CPU on the field-tick goroutine, against 1000 ms wall-clock = **<0.3% budget**. Numbers are coarse estimates from typical x86 throughput; measured budgets land in the alloc/perf test pass.

### Peak ballistics and LUFS channel summation

Two DSP details that the cost-budget table elides but must be pinned so implementers don't make arbitrary calls that diverge from §Testing assertions:

**Peak decay:** the per-channel `Peak` value in the published snapshot is NOT simply the maximum sample since the last publish — that would render as a visually jumpy bar without the expected fall-off behavior of a hardware VU meter. The published peak combines:

- **Attack:** instant. Any sample whose absolute value exceeds the current peak immediately raises it.
- **Release:** exponential decay at `-20 dB/s`, applied per-sample (not per-publish). Concretely: `peak *= decayPerSample` where `decayPerSample = pow(10, -20.0 / 20.0 / sampleRate)` (≈ 0.99998 at 48 kHz). Sample-based rather than wall-clock means the decay rate is reproducible across irregular publish cadence and identical across NTSC/PAL field rates.
- Decay state lives on `audioMeterState` and survives across publish ticks. On plane teardown the meter is destroyed and the next session starts fresh.

A DSP test in pillar 1 covers the decay curve: feed one full-scale sample, then 48000 zero samples; assert the published peak after ~100 ms ≈ `1.0 * pow(10, -20*0.1/20)` = 0.794, and after 1 s ≈ `0.1`. Tolerance ±5%.

**LUFS channel summation:** ITU-R BS.1770 short-term loudness is computed as:

```
L_K = -0.691 + 10 * log10(Σ_ch G_ch * meanSquare_ch)
```

…where:
- `meanSquare_ch` is the mean of the K-weighted signal squared, taken over the 3 s sliding window, per channel.
- `G_ch` is the BS.1770 channel weight: `G_L = G_R = 1.0` for stereo. For mono, treat as a single channel with `G = 1.0`.
- The sum is over active channels: 1 for mono, 2 for stereo.

**Critical:** do not compute per-channel LUFS and average them — power sums, then log. Averaging in the log domain gives different (and wrong) numbers. The `-0.691` calibration constant is applied once, after the channel-weighted power sum. This is what makes dual-mono stereo read +3 dB louder than mono (P_L + P_R = 2 × P_mono → 10 × log10(2) ≈ +3.01 dB), as asserted by pillar 1.

### FFT input handling

The FFT cost numbers in the table above assume an explicit windowing policy that the previous draft elided. Pinning the policy here so the implementer doesn't make an arbitrary call that diverges from the test expectations:

- **Mix-down**: stereo input is mixed to mono via `(L + R) / 2` before FFT input. For mono input, samples are used directly.
- **Input buffer**: a 1024-sample ring buffer on `AudioMeter` holds the most recent mono-mixed samples. `Observe()` appends each chunk's samples into the ring (wrap-around overwrite). At 48 kHz with ~800 samples per chunk, the ring rolls over slightly faster than once per chunk.
- **FFT cadence**: one FFT per publish tick (~30 Hz), not per chunk. On non-publish ticks the spectrum bands in the published snapshot are reused from the prior FFT, so consumers always see fresh peak/RMS/LUFS/goniometer values alongside slightly-stale spectrum (about 33 ms staleness, indistinguishable visually).
- **Window**: a precomputed Hann window of length 1024 is applied at FFT call time (element-wise multiply into a scratch buffer). The window is computed once at `AudioMeter` construction.
- **FFT**: 1024-point real FFT → 513 complex bins (DC + 511 positive + Nyquist). Magnitude squared computed for bins 1..511 (DC and Nyquist contribute almost nothing to the audible spectrum).
- **Binning**: 32 logarithmically-spaced bands from 20 Hz to 20 kHz. Band edges computed as `20 * (1000^(i/32))` for `i = 0..32` (so band 0 covers 20 Hz to ~24.8 Hz, band 31 covers ~16.1 kHz to 20 kHz). Each band's value is the sum of Hann-window-corrected power over its bins, then `10 * log10(power)` to convert to peak-referenced dBFS. Bands whose lower edge falls above the Nyquist (`sampleRate / 2`) emit the `-90.0` sentinel.
- **Calibration**: FFT power is corrected by the Hann window's energy gain (`sum(w²) / N`) and converted to peak-referenced dBFS by adding the sine RMS-to-peak offset. A bin-centered full-scale sine whose main lobe sits inside one band emits ≈0 dBFS in that band; tested by §Testing pillar 1. This avoids the common mistake of using only `2 / N²`, which ignores the Hann window gain and under-reports the tone.

### FFT subpackage — `internal/dataplane/fft/`

Small radix-2 real FFT, ~150 lines:

- `Real1024(in []float32, out []complex64)` — fixed-size convenience wrapper around the generic implementation.
- Table-driven twiddle factors computed once at package init.
- Hann window helper provided alongside.
- Zero dependencies. Fully testable against `math/cmplx` reference values and the Parseval theorem (∑|x[n]|² = (1/N)·∑|X[k]|²).
- Kept private to `internal/dataplane` so the rest of the codebase cannot grow a dependency on a chassis-internal FFT. The subpackage exists rather than a single `internal/dataplane/fft.go` file so the public surface (`Real1024`, `Hann1024`) is grouped with its test file and twiddle tables; the rest of `internal/dataplane` continues to live at the top level.

### Sample rate hot-swap

`AudioMeter` assumes `sampleRate` is constant for its lifetime. The Bresenham accumulator's threshold (`sampleRate`) and per-Observe increment (`samplesInChunk * targetHz`) are both expressed in units that depend on `sampleRate`; changing it mid-life would leave `publishAccum` carrying state in the wrong units, producing several over- or under-published ticks until the accumulator naturally re-equilibrates.

Currently this can't happen: any source switch that changes the audio sample rate tears down the plane (and the meter with it), and the next session starts with a fresh `AudioMeter` whose accumulator is initialized to 0. The implementation may pass `sampleRate` as a parameter to `Observe` (matching how chunk-level metadata flows today), but the implementation MAY assert that `sampleRate` matches the value passed at construction and panic on mismatch — preferred for catching the eventual "hot-swap support" feature attempt at the right boundary.

Future hot-swap support (e.g., 48 kHz → 44.1 kHz within one plane) MUST reset `publishAccum` to 0 and rebuild the K-weighting biquad state to preserve cadence and LUFS calibration. This is out of scope for 5B.

### Plane integration — `internal/dataplane/plane.go`

A single new field on `dataplane.PlaneConfig`, set by `core.Manager.startPlaneLocked` from the generation argument it already receives:

```go
type PlaneConfig struct {
    // … existing fields …
    Generation uint64
}
```

A single new field on `*Plane`, initialized by `NewPlane` with `cfg.Generation` and the 30 Hz target cadence:

```go
type Plane struct {
    // … existing fields …
    audioMeter AudioMeter
}
```

And a single new call in the field-tick goroutine, inserted immediately before [the existing `p.sendAudio(oldest)` at plane.go:1000](../../../internal/dataplane/plane.go#L1000):

```go
if len(oldest) > 0 {
    p.audioMeter.Observe(oldest, audioChans, audioRate)
    p.sendAudio(oldest)
}
```

A new public method exposes the snapshot:

```go
// AudioScopes returns a pointer to the latest published snapshot, or
// nil if no snapshot has been published yet on this plane. Returned
// pointer is read-only — callers MUST NOT mutate the pointee.
func (p *Plane) AudioScopes() *AudioScopeSnapshot {
    return p.audioMeter.AudioScopes()
}
```

**Pause and prebuffer behavior:** `Observe` only fires inside the `if len(oldest) > 0` guard, which itself only runs when the ring has shifted out a non-empty chunk (audio ring is past the prebuffer phase and `audioReady` is set). So the meter is silent during:

- The early prebuffer phase (`audioRingLen <= audioDelayN`).
- After a `Pause()` cancels the plane (the plane goroutine exits; no further calls happen).
- When `audioReady` clears.

Plane teardown does not destroy `audioMeter`'s last published snapshot — but the meter is unreachable from outside once `Manager.plane` is nilled (which happens immediately after the goroutine returns; see [manager.go:285](../../../internal/core/manager.go)). So in practice the chassis sees pending the moment a session is paused or stops, and the spec's pause-as-pending design holds without any explicit "pause stamping" on the snapshot.

### Core surface — `internal/core/manager.go`

The existing `planeRunner` interface gains one method:

```go
type planeRunner interface {
    // … existing methods …
    AudioScopes() *dataplane.AudioScopeSnapshot
}
```

`*core.Manager` gains a public method:

```go
type AudioScopeSnapshot = dataplane.AudioScopeSnapshot

// AudioScopes returns the latest published snapshot, or nil if no plane
// is active (idle, paused, or between sessions). Returned pointer is
// read-only — callers MUST NOT mutate the pointee.
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

Both `Plane.AudioScopes()` and `Manager.AudioScopes()` return `*AudioScopeSnapshot` rather than a value to avoid copying the ~2 KB goniometer array on every read. With many tabs open at 30 Hz, value-copies at two package boundaries would burn hundreds of KB/s of bcopy for no benefit; the pointer return is what makes the "same atomic memory shared across all subscribers" claim true. The trade-off is the read-only contract on the pointee — the chassis envelope construction reads fields and copies what it needs into the JSON shape, never retaining the pointer past the local function.

`core.AudioScopeSnapshot` is a type alias to `dataplane.AudioScopeSnapshot`, not a mirrored struct. A mirror would force either a value copy or unsafe pointer conversion; the alias keeps the chassis API surface under `internal/core` while preserving the no-copy pointer return. This is acceptable because `internal/core` already imports `internal/dataplane`, and `internal/chassis` still imports `internal/core` only.

The brief `Lock` around the plane pointer load uses the existing `sync.Mutex` on `Manager`; it doesn't hold across the actual snapshot read. 5B deliberately does not convert `Manager.mu` to `sync.RWMutex`, because that would be a broad manager-level concurrency change unrelated to audio scopes.

### Chassis surface — `internal/chassis/audio.go`

A new viewer interface:

```go
// AudioScopeViewer is the read-only source for the latest audio-analysis
// snapshot. *core.Manager satisfies this structurally via AudioScopes().
// Tests use fakes. When the viewer is nil or returns nil, the chassis
// emits a pending audio frame. The returned pointer is read-only.
type AudioScopeViewer interface {
    AudioScopes() *core.AudioScopeSnapshot
}
```

A new wire envelope, encoded as a discriminated union of two distinct struct types to make the schema-discriminator rule hold at the encoder. **No `omitempty` on any live-shape field** — the live shape always emits every scalar, including legitimate zeros (`phaseCorr: 0.0` for uncorrelated stereo, etc.).

```go
// Pending shape. Encoded as {"status":"pending"} exactly.
type audioPendingEnvelope struct {
    Status string `json:"status"`
}

// Live shape. Every scalar field is required and emitted unconditionally.
// VU is a value type (not a pointer) so its sub-fields cannot be omitted
// by accident. Spectrum and Goniometer are slices that always have their
// fixed lengths (32 / 256); empty slices would indicate a publisher bug.
type audioLiveEnvelope struct {
    Status     string         `json:"status"`     // always "live"
    Generation uint64         `json:"generation"`
    SampleRate int            `json:"sampleRate"`
    Channels   int            `json:"channels"`
    VU         vuEnvelope     `json:"vu"`
    PhaseCorr  float32        `json:"phaseCorr"`
    LUFSShort  float32        `json:"lufsShort"`
    Spectrum   []float32      `json:"spectrum"`
    Goniometer [][2]float32   `json:"goniometer"`
}

type vuEnvelope struct {
    Left  channelLevel `json:"left"`
    Right channelLevel `json:"right"`
}

type channelLevel struct {
    Peak float32 `json:"peak"`
    RMS  float32 `json:"rms"`
}
```

Construction and change-detection helpers:

```go
// audioEnvelopeFromViewer returns either an *audioPendingEnvelope or an
// *audioLiveEnvelope. The events handler passes the returned value
// directly to emit(), which json.Marshals it. Encoding either struct
// produces the documented wire shape.
func audioEnvelopeFromViewer(v AudioScopeViewer) any

// audioShouldEmit is the single source of truth for "should I emit?".
// It always emits live frames so the stream keeps its 30 Hz cadence.
// It suppresses only repeated pending frames while idle.
func audioShouldEmit(prev, curr any) bool
```

`audioEnvelopeFromViewer`:

- viewer is nil → `&audioPendingEnvelope{Status: "pending"}`
- viewer returns nil → `&audioPendingEnvelope{Status: "pending"}`
- snapshot has `Generation == 0` → `&audioPendingEnvelope{Status: "pending"}`
- otherwise → `&audioLiveEnvelope{...}` populated from the snapshot.

**Generation gating policy (load-bearing):** `audioEnvelopeFromViewer` does NOT compare snapshot.Generation against any "current core generation." If a stale snapshot from a torn-down plane briefly leaks through (e.g., during the narrow window between `Manager.startPlaneLocked` swapping in a new plane and the new plane publishing its first snapshot, the chassis tick could observe the new plane but with an empty atomic pointer, see nil → pending, then on the next tick see the new plane's first live snapshot with the new generation), the chassis emits exactly what the dataplane published. Clients are the authority for generation tracking: any client maintaining peak-hold/goniometer history compares `audio.generation` against its previous-frame generation and resets on mismatch. This pushes generation reset semantics to clients (one place) rather than splitting them between chassis envelope construction and the client (two places).

The downstream consequence: a direct live(gen=N) → live(gen=N+1) transition (no intervening pending frame) is a legal sequence on the wire. Tests must cover it (see §Testing pillar 3).

`audioShouldEmit` returns true for every live envelope, even if two consecutive snapshots happen to be numerically identical. This preserves the 30 Hz stream contract for silence, steady test tones, and fake viewers. It returns true for pending↔live transitions, true for live↔pending transitions, and false only for pending→pending. That means the bridge emits one pending frame when a session ends, then goes quiet while idle.

**Float formatting** for the live shape pins `strconv.FormatFloat(x, 'g', 5, 32)` (5 significant figures, float32 precision) via a custom `MarshalJSON` on `audioLiveEnvelope`. The marshaller must walk the `Spectrum []float32` and `Goniometer [][2]float32` slices element-by-element — Go's default `encoding/json` does not invoke a struct-level `MarshalJSON` for slice elements. Estimated size: ~60-80 lines including per-slice walks.

**NaN/Inf clamping (required before format):** before formatting, every float is clamped:
- `NaN → 0`
- `+Inf → +1.0` for linear `[0, 1]` fields (peak, RMS); `0.0` for correlation; `0.0` for dB fields (the spectrum/LUFS sentinels handle the unreachable end)
- `-Inf → -1.0` for correlation; the appropriate sentinel (`-90.0` for spectrum, `-100.0` for LUFS) for dB fields.

Without clamping, `strconv.FormatFloat(NaN, 'g', 5, 32)` returns `"NaN"`, which is invalid JSON. `json.Marshal` would return an `InvalidJSONError` and the SSE handler's per-tick recovery would swallow the frame — visible to operators as a sporadic skipped tick under audio numeric edge cases (denormals in RMS sliding-window divisions, log of zero). Clamping at the marshaller is the single chokepoint.

### Events integration — `internal/chassis/events.go`

A new package-level var for the audio cadence:

```go
var audioTickInterval = time.Second / 30
```

Inside `handleEvents`, after the existing initial-burst emits and after `tick := time.NewTicker(chassisTickInterval)`:

```go
audioTick := time.NewTicker(audioTickInterval)
defer audioTick.Stop()

var lastAudio any = audioEnvelopeFromViewer(s.audioScopeViewer)
if err := emit(w, "audio", lastAudio); err != nil {
    return
}
flusher.Flush()
```

The audio emit logic is wrapped in a closure so `defer recover()` actually runs after each tick (a free-floating `defer` in a `case` body would only run at `handleEvents` return, which would not survive a single bad frame). The closure returns a `bool` saying whether the writer is still healthy; on a write error, the outer loop returns:

```go
emitAudio := func() (alive bool) {
    defer func() {
        if r := recover(); r != nil {
            slog.Warn("chassis: audio emit panicked", "panic", r)
            alive = true // keep the SSE stream alive; skip this frame
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

And inside the `select` loop, a new case:

```go
case <-audioTick.C:
    if !emitAudio() {
        return
    }
```

Key properties:

- The audio path does not touch `s.cache`. The cache's 2 Hz refresh cadence and the 30 Hz audio cadence are decoupled.
- `audioShouldEmit` suppresses identical-pending frames at 30 Hz when the bridge is idle — the wire goes quiet between casts.
- Live frames are emitted on every `audioTickInterval`, even if the values are identical, so the wire cadence remains 30 Hz under silence and steady tones.
- Generation gating: when the previous emit was a live envelope and the new viewer call returns nil/pending, `audioShouldEmit` reports true, emits the pending frame, and the client knows the session ended. The next live snapshot's new generation triggers a fresh emit and client reset.
- The existing heartbeat ticker (30 s comment) still fires when no audio change is emitted, defeating reverse-proxy idle timeouts.
- The `defer recover()` is scoped to a single tick via the closure pattern above — a panic computing or marshaling one frame skips that frame and continues the stream, rather than terminating the SSE handler. This matches the per-tick recovery pattern 5A uses for the meter sampler.

### Meter discovery hook — `internal/chassis/meter.go` (5A code)

The 5A spec reserved `meter.audioScopes: {"status":"pending"}` as a placeholder slot for the audio-scope data Spec 5B was originally expected to fill. 5B redirects the actual audio data to the new dedicated 30 Hz `audio` event instead, but the meter slot remains useful as a **discovery hook** so meter-only clients (clients that subscribe to the slow `meter` event but not the high-rate `audio` event) can detect that the audio stream is available.

5B's implementation plan extends the existing meter envelope builder in `internal/chassis/meter.go` (the 5A file) to emit:

```json
"audioScopes": {
    "status": "live",
    "via":    "audio",
    "sampleHz": 30
}
```

…when a session is active (i.e., when the same condition that makes the rest of the meter event live is true), and the existing `{"status":"pending"}` when idle. Meter-only clients learn that the `audio` event exists and can decide whether to subscribe to it; clients that already subscribe to `audio` ignore the meter hook.

The hook is a 5A surface change, not a 5B-side new code path. The 5B plan adds two lines to the meter envelope builder and one test case in `events_test.go` asserting the discovery shape.

### Snapshot cache invariant

The 5A spec established that `s.cache.Set` is called once per `chassisTickInterval` by a single refresher goroutine, regardless of how many tabs are connected — `Manager.StatusHomeView()` is invoked once per 250 ms total. 5B preserves that exact invariant: the audio path never calls `StatusHomeView()`.

5B does add a second viewer-read at 30 Hz × N tabs (each tab calls `s.audioScopeViewer.AudioScopes()` from its own SSE handler). Each read briefly takes the existing `Manager.mu` to load `m.plane`, then performs an `atomic.Pointer` load on the plane itself. With N tabs open, the dataplane publishes one snapshot at an average 30 Hz, and each tab independently reads a pointer to the latest snapshot — the underlying memory is shared, not copied, thanks to the pointer-return design (see §Core surface). No new fan-out goroutine, no new broker, no new lock.

**Lock-jitter acknowledgment:** `Manager.mu` is also contended by session-lifecycle operations — `StartSession`, `Pause`, `Stop`, `Seek`, `SetFieldOrder`, `SetOutputVolume` — some of which hold the lock for tens of ms (probe + plane teardown is not lock-free). With 5+ chassis tabs open, the audio ticker can stall on `m.mu` long enough to drop one or two publishes during a session transition. This is acceptable: the operations are infrequent, the gap is invisible at 30 Hz (one missing frame = ~33 ms), and the next tick recovers cleanly. If profiling ever shows audio-tick starvation as a real problem, the resolution is to add an `atomic.Pointer[planeRunner]` shadow of `m.plane` for hot reads — not to convert `m.mu` to `sync.RWMutex` (that would be a manager-wide concurrency change unrelated to audio scopes).

## Phase 1 Follow-Up Debt Migration

**Sequencing constraint (load-bearing):** 5B's Task 1 requires that 5A's `subscribe()` helper has merged into `vfd-live.js` first. The current `vfd-live.js` (verified against `main`) only exposes `window.Chassis.events.source` and dispatches `chassis:eventsource` — there is no `subscribe()` function in the tree today. If 5A is still in flight when 5B's plan begins, one of:

1. Defer 5B's Task 1 until 5A lands, and start with Task 2 (DSP scaffolding). The migration is structurally independent of the rest of 5B.
2. Land the `subscribe()` helper in 5B's Task 1 itself, with cross-spec coordination so 5A doesn't double-add it on merge.

The implementation plan MUST commit to one of these at plan-write time and document the choice in Task 1's preamble.

The 5A spec adds `window.Chassis.events.subscribe(eventName, handler)` to `vfd-live.js` with reconnect-time reattach and dedupe. Two scripts predate that helper and still use the raw pattern:

- `internal/chassis/static/transport.js`
- `internal/chassis/static/visualizer-bank.js`

Both contain code roughly of this shape:

```js
const src = window.Chassis.events.source;
src.addEventListener('transport', handler);
document.addEventListener('chassis:eventsource', (ev) => {
    ev.detail.source.addEventListener('transport', handler);
});
```

After 5B both are rewritten to:

```js
const unsubscribe = window.Chassis.events.subscribe('transport', handler);
```

The migration happens as **Task 1 of the implementation plan**, before any 5B audio code. Reasons:

1. `meter.js` (from 5A) is already the first `subscribe()` consumer. After Task 1, every chassis client script uses one subscription idiom.
2. The new `audio` event handler in `meter.js` will be the second `subscribe()` consumer. Landing the migration first gives 5B a single pattern to extend.
3. The change is mechanically small and structurally testable — a regex grep in `chassis_test.go` can assert the old pattern is gone.

Behavior of `transport.js` and `visualizer-bank.js` does not change; this is a pure refactor. Existing transport and visualizer tests pass unchanged.

## Client Rendering — `internal/chassis/static/meter.js`

The 5A version of `meter.js` ships the pending hooks for the audio scopes — DOM elements present, no values driven, no fake animation. 5B replaces the pending paths with live data handlers; the `meter` event handler is untouched.

**Consumer decode pattern (load-bearing):** unlike every other chassis event whose payload always has the same shape, `audio` is a discriminated union. The `subscribe('audio', ...)` handler MUST read `status` first and only then read the other fields:

```js
window.Chassis.events.subscribe('audio', (ev) => {
    const payload = JSON.parse(ev.data);
    if (payload.status !== 'live') {
        renderPending();
        return;
    }
    // safe to destructure now
    const { generation, vu, phaseCorr, lufsShort, spectrum, goniometer } = payload;
    if (generation !== lastGeneration) {
        resetHistories(); // clears peak-hold, goniometer trail, etc.
        lastGeneration = generation;
    }
    renderLive(vu, phaseCorr, lufsShort, spectrum, goniometer);
});
```

Existing chassis event handlers (transport, state, vfd) destructure payload fields directly because those payloads always have all keys — `meter.js` should NOT use that idiom for `audio`. The `status` discriminator and `generation` reset are both load-bearing client responsibilities; see §Generation gating policy in the chassis surface section for why the chassis envelope does not filter stale-generation snapshots on the client's behalf.

| Scope | Render strategy | Rationale |
|---|---|---|
| L/R VU | CSS variables on existing `.tr-vu .ch-bar` segments; integer-stepped lit/unlit per the v24 mockup's 12-segment design | Matches the hardware-meter aesthetic, cheap, no canvas |
| Phase correlation | CSS variable driving needle rotation on an SVG/CSS needle element | Same |
| LUFS | DSEG14 numeric readout in the existing font slot, formatted `-14.2 LUFS` | Already-loaded font, already-styled element |
| Spectrum | `<canvas>` paint loop on `requestAnimationFrame`, 32 bars with logarithmic Y-axis, client-side peak-hold decay | High visual rate (60 fps render) decoupled from 30 Hz wire rate |
| Goniometer | `<canvas>` paint loop, 256 `(L,R)` points per frame with slight trail (alpha-blend prior frame) | Canvas is the right tool for scatter |

**Pause behavior:** pause tears down the plane (see §Plane integration), so the next `audio` event after pause carries `status: "pending"`. The client receives no further events until resume produces a new live frame with a new generation. Canvas paint loops detect the pending event and clear/fade to the idle backdrop. No frozen-live state exists, and no fake values are drawn.

**LUFS on resume:** because the K-weighting biquads and 3 s accumulator live on `AudioMeter` (which is destroyed with the plane on pause), the resumed session starts a fresh LUFS-short integration. Operators see a brief `-100.0 LUFS` sentinel during the first ~3 s of resumed playback while the accumulator fills. This matches every other "fresh session" case in the bridge and avoids special-case state preservation across plane lifetimes.

**Bandwidth on idle:** when the bridge is idle, `audioShouldEmit` suppresses identical pending frames at 30 Hz — the wire emits one pending frame on session-end transition, then goes quiet until the next session starts. Per-tab idle bandwidth is dominated by the 30 s heartbeat comment, not the audio event. While live, the audio event is emitted every 30 Hz tick even if the payload is numerically unchanged.

**No fake values, ever.** When `status === "pending"`:

- Spectrum canvas: renders the 5A idle backdrop.
- Goniometer canvas: same.
- VU bars: sit at zero.
- LUFS readout: shows `--.- LUFS`.
- Phase needle: parks at center.

A `chassis_test.go` assertion enforces this: `meter.js` source must contain no `Math.random`, `Math.sin`, `Math.cos`, or numeric constants that could plausibly seed a generator — same lint posture 5A established. The regex set is small and explicit so it is easy to extend.

**No new fonts, no new CSS files.** Small additions to `chassis.css` (canvas sizing, VU bar transitions, phase needle rotation transition) go under the existing `body.receiver` scope. The 5A pending hooks already define every selector 5B needs.

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/dataplane/audiometer.go` | `AudioMeter` type, DSP, atomic snapshot holder |
| `internal/dataplane/audiometer_test.go` | Known-PCM correctness, alloc budget, concurrency |
| `internal/dataplane/fft/fft.go` | Radix-2 real FFT, ~150 lines, no deps |
| `internal/dataplane/fft/fft_test.go` | Table tests vs `math/cmplx`, Parseval check |
| `internal/chassis/audio.go` | `AudioScopeViewer`, `audioEnvelope`, conversion + change-detect |
| `internal/chassis/audio_test.go` | Envelope serialization, idle/pending shape |

**Modified files:**

| Path | Change |
|---|---|
| `internal/dataplane/plane.go` | `PlaneConfig.Generation`; `audioMeter AudioMeter` field initialized from generation; `Observe` call before `sendAudio`; `Plane.AudioScopes() *AudioScopeSnapshot` |
| `internal/dataplane/plane_test.go` | Stub processHandle → non-nil snapshot after N ticks; pause leaves Manager.AudioScopes nil |
| `internal/core/types.go` | `type AudioScopeSnapshot = dataplane.AudioScopeSnapshot` alias |
| `internal/core/manager.go` | Pass session generation into `PlaneConfig`; `planeRunner.AudioScopes() *dataplane.AudioScopeSnapshot`; `Manager.AudioScopes() *AudioScopeSnapshot` |
| `internal/core/manager_test.go` | Fake plane satisfies widened interface; nil-when-idle and non-nil-when-live coverage |
| `internal/chassis/meter.go` | Discovery-hook update: meter envelope's `audioScopes` field emits `{"status":"live","via":"audio","sampleHz":30}` while active |
| `internal/chassis/meter_test.go` | Discovery-hook test: meter envelope shape when audio is live vs pending |
| `internal/chassis/server.go` | `Config.AudioScopeViewer`, `Server.audioScopeViewer` |
| `internal/chassis/events.go` | Second ticker (30 Hz), audio-burst append, per-tick panic-recovery closure |
| `internal/chassis/events_test.go` | Order assertion (append `audioIdx`), 30 Hz cadence, pending shape, gen-flip reset, idle suppression, legitimate-zero serialization |
| `internal/chassis/templates/meter.html` | Replace 5A pending placeholders with live data hooks |
| `internal/chassis/static/meter.js` | New `subscribe('audio', …)` handler; canvas paint loops; pause → idle backdrop |
| `internal/chassis/static/transport.js` | **Task 1**: migrate to `subscribe()` |
| `internal/chassis/static/visualizer-bank.js` | **Task 1**: migrate to `subscribe()` |
| `internal/chassis/static/chassis.css` | Canvas sizing, VU/phase transitions (scoped additions only) |
| `internal/chassis/chassis_test.go` | Template hooks present; no-fake-values lint; subscribe-pattern lint |
| `cmd/mister-groovy-relay/main.go` | `AudioScopeViewer: coreMgr` in chassis.Config |
| `tests/integration/chassis_test.go` | End-to-end: plane → SSE → `audio` event with `status: live` |

**Files intentionally unchanged:**

- `internal/ui/*`, `internal/uiserver/*` — 5B is additive under `/receiver/*`.
- `internal/playback/*` — audio meter is separate from playback controls.
- All adapter packages — DSP runs in the data plane, regardless of source.
- `internal/chassis/static/vfd-live.js` — the `subscribe()` helper already lands in 5A.

## Testing Strategy

Six pillars, each mapping to one or more test files:

### 1. DSP correctness — `audiometer_test.go`, `fft_test.go`

- Pure-PCM tables: silence → snapshot with zero peak/RMS, `phaseCorr == 0`, all spectrum bands at `-90.0` sentinel; full-scale 1 kHz sine → RMS ≈ 0.707, peak ≈ 1.0; out-of-phase L/R → `phaseCorr ≈ -1.0`; in-phase → `phaseCorr ≈ +1.0`.
- LUFS calibration (BS.1770-4): after the 3 s window is full, a mono 1 kHz sine at -20 dBFS RMS → `lufsShort ≈ -20.7` within ±0.5 dB tolerance (`-0.691 + 10*log10(meanSquare)`, with K-weighting approximately unity at 1 kHz). A dual-mono stereo fixture should read about 3.0 dB louder than the mono fixture because BS.1770 sums weighted channel power.
- FFT calibration: a bin-centered full-scale sine whose Hann main lobe sits inside one log band → that band ≈0 dBFS; the same sine at -12 dB peak amplitude → that band ≈-12 dBFS. Tests MUST use a bin-centered frequency and a band wide enough to contain the Hann main lobe.
- FFT Parseval: ∑|x[n]|² = (1/N)·∑|X[k]|² within float32 tolerance for several test signals.
- Alloc budget: split into two runs against an `AudioMeter` test seam exposing cadence controls:
  - **No-publish run:** set `targetHz = 0` so the Bresenham accumulator never advances. `testing.AllocsPerRun(100, func() { meter.Observe(pcm, 2, 48000) })` MUST equal 0. This is the non-publish hot-path allocation budget.
  - **Always-publish run:** set `targetHz = sampleRate` (e.g., 48000 for 48 kHz fixtures). The accumulator crosses the publish threshold on every Observe call regardless of chunk size, so each call publishes exactly once. `testing.AllocsPerRun(100, ...)` MUST equal exactly 1 (the snapshot pointer). Catches accidental allocations introduced by publish-path code.
  - **Cadence run:** with `targetHz = 30`, feed synthetic chunks matching both 50 Hz and 59.94/60 Hz field cadences for 10 s of audio and assert the publish count stays within one frame of 300.

### 2. Atomic snapshot safety — `plane_test.go`, `manager_test.go`

- Concurrent `Observe` (single publisher) vs `AudioScopes` (many readers) under `go test -race`.
- Idle plane (before any Observe): `AudioScopes()` returns nil.
- Live plane returns non-nil snapshot after enough audio samples have crossed the 1/30 s publish threshold.
- Pause path: cancel plane context, wait for `Done()`, assert `Manager.AudioScopes()` returns nil afterward (proves the pause-as-pending design holds end-to-end).
- `Manager.AudioScopes()` returns nil when no plane exists.

### 3. SSE wire contract — `events_test.go`

- Initial-burst order: extend the existing assertion to add `audioIdx` last, with constraint `audioIdx > <previous-last>`. Do NOT retype the preceding event list; read it from main at task time (see §Initial Event Order).
- Pending shape on idle: `data: {"status":"pending"}` exactly, no other keys, no whitespace beyond what the encoder emits.
- Live shape on active session: 32 spectrum entries, 256 goniometer pairs, all required scalar fields present **including legitimate zeros** (test forces `phaseCorr: 0.0` and `lufsShort: 0.0` through a fake viewer and asserts both keys appear in the JSON, not omitted).
- `audioShouldEmit` suppresses identical pending frames at 30 Hz when bridge is idle (assert: 100 ms of idle ticks produces exactly one pending frame).
- Live cadence (exact count): while the fake viewer returns a live envelope, 100 ms of audio ticks emits **exactly** three live frames even when the payload values are numerically identical. A longer-window check: 1 s of ticks emits 30 ± 1 frames (the ±1 accounts for ticker-edge timing). Asserting exact counts catches a regression where someone restores change-detect semantics or accidentally double-emits per tick.
- Generation flip via pending: when the viewer transitions live(gen=N) → nil → live(gen=N+1), the wire sequence contains live(N), then pending, then live(N+1) with no stale live(N) after pending.
- Generation flip direct (no pending): when the viewer transitions live(gen=N) → live(gen=N+1) directly (preemption case — new plane up before chassis tick observes a nil read), the wire emits two distinct live envelopes carrying the correct generations in order. The chassis does NOT filter the second one on its way out; clients are the authority for generation reset (see §Generation gating policy).
- Heartbeat ticker still fires when no audio change has been emitted for 30 s.
- Panic recovery: a viewer that panics on its second call causes one skipped frame, not handler termination. Assert the third call's output appears in the stream.
- Meter discovery hook: when a session is active, the `meter` event's `audioScopes` field is `{"status":"live","via":"audio","sampleHz":30}`. When idle, it is `{"status":"pending"}`. The literal `30` in the hook MUST be derived from the same source as `audioTickInterval` (e.g., a shared constant `audioEventHz`) so a future cadence change can't leave the discovery hook lying about the actual rate. The test references that constant rather than the literal `30`.

### 4. No-fake-values lint — `chassis_test.go`

- `meter.js` source contains no `Math.random`, `Math.sin`, `Math.cos`, `Math.tan`, or `Date.now()` calls. Same regex-grep posture 5A established. New audio handlers must drive every visible value from the `audio` event payload.

### 5. Subscribe-pattern lint — `chassis_test.go` (extended)

- `transport.js` and `visualizer-bank.js` source contain no `addEventListener('transport'`, `addEventListener('visualizer'`, `events.source`, or `chassis:eventsource` references.
- Both files contain at least one `window.Chassis.events.subscribe(` call.
- Regex substring is sufficient here because no chassis JS file embeds documentation snippets containing the forbidden patterns; the production JS surface is small and the lint's false-positive surface is zero.
- Structural test prevents regression to the old pattern.

### 6. End-to-end integration — `tests/integration/chassis_test.go`

Build-tagged (`//go:build integration`):

- Construct a real `*core.Manager` and a fake `processHandle` that emits a known 1 kHz tone PCM stream.
- Start a real session; connect an `httptest.Server` SSE client to `/receiver/events`.
- Assert: receives `audio` event within 100 ms; payload `status == "live"`; `vu.left.peak` within tolerance of expected for the tone; `spectrum[band_at_1kHz]` is significantly higher than neighboring bands.
- Stop the session; assert next `audio` event is `{"status":"pending"}`.

## Layering and Import Rules

The existing `internal/chassis/import_check_test.go` lint enforces:

- `internal/core` cannot import `internal/adapters`, `internal/chassis`, `internal/playback`, `internal/ui`, or `internal/uiserver`.
- `internal/adapters` cannot import `internal/chassis`, `internal/playback`, `internal/ui`, or `internal/uiserver`.

5B preserves both rules:

- `internal/dataplane` is unchanged in its imports (it does not depend on anything that would break the lint).
- `internal/dataplane/fft` is a leaf subpackage with no chassis or core imports.
- `internal/core` imports `internal/dataplane` (already does) and aliases `AudioScopeSnapshot` so it does not need to make chassis import dataplane directly.
- `internal/chassis` imports `internal/core` only (already does).

No change to `import_check_test.go` is expected. If the audit-grade lint flags anything, the resolution is to fix the violating import — not to relax the rule.

## Rollback / Failure Modes

- **DSP cost regression:** if `AudioMeter.Observe` grows beyond its budget (test pins zero allocs on non-publish ticks), the field-tick goroutine could miss its 16.7 ms deadline. Detection: existing `statWindow.observeQueues` reports backed-up queues; rollback by skipping `Observe` when queue depth exceeds a sentinel.
- **SSE bandwidth on many tabs:** ~200 KB/s per tab at 30 Hz (~1.6 Mbps), dominated by the goniometer. Five tabs ≈ 1 MB/s. Comfortable on a LAN, visible on a WAN tunnel. If problematic, drop goniometer to 128 pairs (halves the frame) or quantize spectrum to int8 dBFS (saves ~10%); the schema discriminator rule means clients tolerate both changes without an explicit version bump. Idle bandwidth is negligible (one suppressed pending frame between casts plus a 30 s heartbeat comment).
- **FFT correctness regression:** Parseval test catches most FFT bugs; the spectrum integration test catches binning bugs. A failing test must block the merge; do not skip.
- **Pause / generation race:** if the dataplane publishes a snapshot from a stale generation just as the next session starts, clients see a one-frame glitch. The chassis-side `audioShouldEmit` emits the new live frame; clients reset on generation change. The acceptable visible artifact is at most one frame at 30 Hz (~33 ms).

## Out of Scope (Phase 3+)

- True-peak (inter-sample) detection — would require oversampling, not justified for a CRT-out monitoring meter.
- Integrated LUFS over the session — would require a session-scoped accumulator separate from the per-chunk DSP.
- Per-band peak-hold or spectral history beyond what the client computes locally.
- Mobile-specific scope rendering.
- WebSocket transport.
- A telemetry pub/sub broker or dedicated SSE endpoint.
- Any change to `/ui/*`.

These are not deferred to a follow-up spec; they are explicitly outside Phase 2's mandate. If a future Phase wants them, it will need to revisit transport, sample rates, and broker decisions; the 5B contract is forward-compatible with all of them.
