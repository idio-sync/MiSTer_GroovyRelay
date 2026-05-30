# Receiver Audio Tone / Equalizer Strip — Design

**Date:** 2026-05-29
**Status:** Approved (design); pending implementation plan
**Surfaces:** receiver chassis web UI (new always-on face strip), data plane
(PCM audio stage), core, config

## Goal

Add an always-on **audio processing strip** to the receiver chassis face
that emulates an 80s/90s AV-stack / graphic-equalizer component. Every
control shapes the live **s16le PCM stream in Go**, applied where the
existing output-volume gain already runs, so the controls **hot-swap live**
exactly like the volume knob does today — no FFmpeg restart, no recast.

The strip holds, left → right:

- **Tone knobs** — Bass, Mid, Treble (rotary, matching the volume knob).
- **Balance** — L/R, center-detented (rotary).
- **10-band graphic equalizer** — vertical sliders at ISO octave centers
  (31 Hz … 16 kHz), ±12 dB.
- **Preset curves** — Flat / Rock / Jazz / Vocal (snap the 10 sliders).
- **EQ memories** — M1–M3 user voicings (tap = recall, hold = store).
- **Switches** — Loudness, Mono/Stereo, Subsonic, Tone/EQ defeat.
- **Volume** — the existing global output knob, **relocated** here from the
  transport strip.

## Scope decisions (locked during brainstorming)

- **Audio only.** Video "picture" controls (brightness/tint/sharpness) and
  DSP reverb sound-fields were explicitly **out** of this effort.
- **Placement:** always-on face strip (not a flip-down drawer, not a Setup
  pane). It sits on the brushed-metal faceplate as a new module between the
  transport row and the visualizer bank.
- **Volume relocates** out of the transport strip into this strip.
- **No spectrum backlight.** The meter module already renders a 32-band FFT
  analyzer ([meter.html](../../../internal/chassis/templates/meter.html),
  [meter.js](../../../internal/chassis/static/meter.js)); a second spectrum
  behind the EQ would duplicate it. Dropped.
- **Visual styling is finalized at implementation time against the live
  app**, reusing existing component classes. This sandbox could not
  faithfully reproduce the runtime, so this spec fixes *structure,
  behavior, and the signal chain*; exact CSS is an implementation concern
  verified against the running chassis. The strip reuses
  `.volume-control` knobs, `.action-btn` (preset/memory), `.switch`
  (booleans), the faceplate/`.screw` treatment, and the **existing but
  currently-inactive `EQ` status LED** in the status bar
  ([status-bar.html](../../../internal/chassis/templates/status-bar.html)).

## Approach (chosen)

**Go PCM DSP in the data plane.** The audio path already applies software
gain to outgoing PCM via `scalePCMVolumeInPlace` (invoked in the send path at
[plane.go:1436](../../../internal/dataplane/plane.go#L1436); defined at
[L1460](../../../internal/dataplane/plane.go#L1460)) — the integration point
for the new DSP stage — driven by an
`atomic.Int32` that `SetOutputVolume` updates live
([plane.go:509](../../../internal/dataplane/plane.go#L509)). We extend that
same stage with a biquad filter chain. The codebase already designs RBJ biquads — `designShelfHigh`
([audiometer.go:456](../../../internal/dataplane/audiometer.go#L456)) and
`designHighpass`
([audiometer.go:477](../../../internal/dataplane/audiometer.go#L477)), plus
the `biquadCoeffs` / `biquadState` types — whose formulas and state model this
feature moves into the shared `audiodsp` package instead of reaching across
package boundaries.

Rejected alternatives:
- **FFmpeg `-af` filtergraph** — filters are fixed at spawn, so every knob
  change forces a recast (or fragile `zmq`/`sendcmd` sockets). Breaks parity
  with the live volume knob. Rejected.
- **Hybrid** (static in ffmpeg, live in Go) — split-brain, no upside.

## Architecture

> **New vs existing.** Symbols introduced by this feature —
> `config.AudioDSP`, the `internal/dataplane/audiodsp` package (`Coeffs`,
> `BiquadState`, `Design*`, `Processor`, `Params`),
> `Manager.SetAudioDSP` / `PreviewAudioDSP` and the new `StatusHomeView` DSP
> fields, `planeRunner.SetAudioDSP`, and `BridgeSaver.SaveAudioDSP` — **do not
> exist yet**; the implementation creates them. Cited `file:line` references
> (`scalePCMVolumeInPlace`, `SetOutputVolume`, `diffBridgeConfig` /
> `scopeForBridgeField` / `applyHotSwapSideEffects`, `VolumeSaver` /
> `VolumeViewer`, `config.AudioConfig`) point at **existing** code this
> feature mirrors or extends.

A new package **`internal/dataplane/audiodsp`** owns biquad design + the
per-chunk apply. `plane.go` stays thin. The existing RBJ formulas in
[audiometer.go](../../../internal/dataplane/audiometer.go) are not reusable
"as-is" because they are unexported members of package `dataplane`; the
implementation moves the shared coefficient/state primitives into
`audiodsp` (`Coeffs`, `BiquadState`, `DesignHighShelf`,
`DesignHighpass`, plus the new `DesignLowShelf` and `DesignPeaking`) and
updates the meter to call the shared package. The design must not leave
`audiodsp` importing from `dataplane`, which would create the wrong dependency
boundary.

**Two-object split (live + click-free):**

1. **`audiodsp.Coeffs`** — an immutable, precomputed coefficient set held
   behind an `atomic.Pointer[audiodsp.Coeffs]` on the `Plane`. A UI change
   recomputes it **off the audio path** and atomically swaps the pointer
   (the many-parameter analogue of the existing `outputVolume atomic.Int32`).
2. **Per-channel biquad state** (`z1/z2` history for the new DSP path) —
   owned by the audio-send goroutine and **never swapped**. Because filter
   state lives outside the swapped object, *incremental* coefficient changes
   (dragging a knob/slider) stay click-free: the filter memory carries over
   instead of being reset to zero. See "Parameter smoothing" below for hard
   on/off transitions.

**Fixed-slot chain (unity when off).** The chain is always the same ordered
set of biquad slots; a disabled stage (or a whole **defeat**) becomes a
pass-through biquad (`b0=1, b1=b2=a1=a2=0`). This keeps state indices stable
across swaps, so toggling stages never scrambles the per-slot history.

**Parameter smoothing (hard transitions).** A unity biquad in the new
transposed Direct-Form II DSP path still carries settling state, so an
*abrupt* coefficient jump — enabling/disabling a slot (defeat, subsonic),
flipping Mono, or a large gain step — can produce an audible transient even
though state is retained.

`audiodsp.Processor` therefore owns the runtime transition state in the
audio-send goroutine, separate from the atomically swapped target coeffs:
`current`, `target`, `fromState`, `toState`, `remainingSamples`, and
`totalSamples`. When a new coeff generation arrives, the processor classifies
the change:

- **Incremental drag** (same enabled/mono topology and every gain delta
  <= 1.0 dB): switch to the new coeffs at the next chunk boundary and retain
  state.
- **Hard change** (defeat/subsonic/loudness/mono toggle, a slot entering or
  leaving unity, or any gain delta > 1.0 dB): run old and new cascades in
  parallel for ~5–10 ms and crossfade their outputs. The new cascade starts
  from a copy of the old per-slot state, not zeros. If another hard update
  arrives mid-ramp, start a new ramp from the current effective output state
  toward the newest target. There is no middle classification.

This is the load-bearing detail behind the click-free guarantee and is
covered by the swap tests below.

Slots, in order:

```
[ subsonic HPF · bass shelf · mid peak · treble shelf · EQ0..EQ9 · loud-lo shelf · loud-hi shelf ]
```

**Per-chunk apply order** (the confirmed signal chain):

```
int16 → float32
  → mono fold (avg L,R when Mono on)
  → biquad cascade (subsonic, bass, mid, treble, EQ0..9, loud-lo, loud-hi; unity slots are no-ops)
  → balance per-channel gain when !mono && channels == 2
  → output volume gain (existing)
  → saturating clamp → int16
```

Processing is done in **float32** across the whole cascade and clamped to
int16 exactly once at the end: the 16-slot int16 cascade (subsonic + 3 tone +
10 EQ + 2 loudness, per channel) would otherwise accumulate rounding noise
and could wrap. When DSP is fully transparent (enabled, no shaping, mono off,
balance centered), the data plane bypasses the float cascade and uses the
existing `scalePCMVolumeInPlace` behavior so legacy output-volume math remains
bit-identical. When shaping is active, volume is applied in the float path and
tests allow a defined ±1 LSB tolerance for the final PCM conversion.

## DSP specifics

Biquads use the RBJ Audio EQ Cookbook forms, designed at the session sample
rate (48000/44100/22050). `audiodsp` owns the shared design functions:
high-shelf and high-pass are moved from the meter implementation, and this
feature adds low-shelf and peaking forms.

**Nyquist safety:** coefficient design must never request a center frequency
at or above Nyquist. At 44100 and 48000 all v1 slots are designable. At
22050, the 12 kHz loudness high shelf and 16 kHz EQ band are above Nyquist
and compile to unity no-op slots; their persisted slider/switch values are
kept so changing the bridge sample rate back to 44100/48000 restores the
voicing. The UI may leave those controls visible to avoid layout churn, but
the data plane exposes the effective inactive slots for tests. The EQ status
LED lights from requested non-flat params, while the sample-rate tests assert
the inactive high slots produce no NaN/Inf and no audible change at 22050.

| Slot | Type | Center | Q | Gain |
|---|---|---|---|---|
| Subsonic | high-pass | ~22 Hz | 0.707 | on/off |
| Bass | low-shelf | ~100 Hz | — | `bass` dB |
| Mid | peaking | ~1 kHz | 0.7 | `mid` dB |
| Treble | high-shelf | ~10 kHz | — | `treble` dB |
| EQ 0–9 | peaking | 31·63·125·250·500·1k·2k·4k·8k·16k Hz | 1.4 (≈√2, octave) | each band dB |
| Loud-lo | low-shelf | ~100 Hz | — | +6 dB when Loudness on |
| Loud-hi | high-shelf | ~12 kHz | — | +4 dB when Loudness on |

- **Loudness** is a **fixed** contour in v1 (predictable). Volume-tracking
  equal-loudness is a noted non-goal extension.
- **Balance** is a per-channel linear gain using an **attenuate-only law**
  (never boosts, so it adds no clipping risk). For `balance ∈ [−100, +100]`,
  center `0` = unity both channels:
  - `L_gain = balance <= 0 ? 1.0 : 1.0 − balance/100.0`
  - `R_gain = balance >= 0 ? 1.0 : 1.0 + balance/100.0`

  So full-left (−100) → L=1.0, R=0.0; full-right (+100) → L=0.0, R=1.0.
  (Deliberately *not* a boost law that pushes the near channel above
  0 dBFS.)
- **Mono** folds L/R to their average before the cascade. When Mono is
  engaged, balance is **disabled in the UI and not applied** (folding then
  re-splitting with balance would desync identical channels). On a mono
  source (single-channel input) both Mono and Balance are no-ops.

**Preset voicings** set the 10 EQ bands only (tone knobs stay independent).
Starting curves (tunable during implementation):

- **Flat** `[0,0,0,0,0,0,0,0,0,0]`
- **Rock** `[+4,+3,+1,0,−1,−1,0,+2,+3,+4]`
- **Jazz** `[+2,+1,0,+1,+1,0,0,+1,+2,+2]`
- **Vocal** `[−2,−1,0,+2,+3,+3,+2,+1,0,−1]`

**Defeat (EQ OUT):** sets subsonic + tone + EQ + loudness slots to
pass-through; **mono, balance, and volume stay live**.

**EQ status LED:** the existing status-bar `EQ` indicator lights when
`enabled && shaping is non-trivial` (subsonic or loudness on, or any tone /
band ≠ 0). Dark when flat or defeated.

**Headroom/clipping:** stacked boosts can exceed 0 dBFS — e.g. several EQ
bands near +12 dB on top of an already-hot source, plus the +6/+4 dB
loudness contour, easily overshoots. v1 policy is a **saturating clamp** at
the int16 write (never wrap); processing in float32 keeps intermediate
stages clean so only the final clamp can clip. There is no automatic
make-up attenuation in v1 — operator guidance is to **cut rather than only
boost**, or trim volume. A future auto-makeup-trim is a non-goal.

## Config & persistence

A new nested `[bridge.audio.dsp]` table extends the existing
`[bridge.audio]` section (`sample_rate`, `channels`, `output_volume`)
without modifying it. The canonical struct is `config.AudioDSP` (TOML-tagged),
mirroring how `output_volume` is a plain field that core copies into the
plane config. The plane and `audiodsp` package consume a plain
`audiodsp.Params` value that core maps from `config.AudioDSP`, so
`internal/dataplane` keeps no dependency on `internal/config` (the same
boundary the `int` volume already respects).

`AudioDSP` nests on the existing `config.AudioConfig`
([config.go:183](../../../internal/config/config.go#L183)) as a `DSP AudioDSP`
field (`toml:"dsp"`); the loader reads `[bridge.audio.dsp]` into
`BridgeConfig.Audio.DSP`. **A missing table is normalized to the transparent
defaults at load** (`enabled=true`, all 0 dB, nothing engaged) rather than
relying on Go zero-values — otherwise a pre-existing config would decode to
`enabled=false` (defeat) and an ambiguous control state. Normalizing at load
keeps both the audio behavior and the rendered knob/switch positions
well-defined for configs written before this feature.

```toml
[bridge.audio.dsp]
enabled  = true        # master engage; false = "defeat" (shaping bypassed)
mono     = false
subsonic = false
loudness = false
bass     = 0.0         # tone, dB
mid      = 0.0
treble   = 0.0
balance  = 0           # -100 (L) .. +100 (R); 0 = center
eq       = [0,0,0,0,0,0,0,0,0,0]   # 10 bands, 31 Hz .. 16 kHz, dB

[[bridge.audio.dsp.memory]]        # EQ memories M1–M3 (voicing = tone + curve)
slot = 1                           # 1..3; absent slot = empty
name = "M1"
stored = true                      # distinguishes saved-flat from empty
bass = 0.0
mid  = 0.0
treble = 0.0
eq   = [0,0,0,0,0,0,0,0,0,0]
```

- **Defaults are fully transparent** — all 0 dB, everything off,
  `enabled=true` but flat = bit-identical to today. A config with **no**
  `[bridge.audio.dsp]` table behaves exactly as the current build.
- **A memory stores only the frequency curve** — `bass`, `mid`, `treble`,
  and the 10 EQ band gains. It does **not** store `mono`, `balance`,
  `loudness`, `subsonic`, `enabled` (defeat), or `volume`; those are
  live-only and are left unchanged when a memory is recalled. Three slots.
  Missing memory tables normalize to empty UI slots; a present slot with
  `stored=true` is recallable even if the saved voicing is all zeros.
- **Validation** (bridge save path, like the `output_volume` 0–100 check):
  dB outside **±12**, balance outside **±100**, an `eq` length other than 10,
  duplicate/out-of-range memory slots, or unknown preset/memory names are
  rejected before any disk write or live apply. Handlers return field errors;
  they do not silently clamp invalid input.

## Core plumbing

Mirror the `OutputVolume` dual-write pattern
([manager.go:1210](../../../internal/core/manager.go#L1210)), with one extra
runtime slot for previews:

- `m.bridge.Audio.DSP` remains the persisted source of truth.
- `m.audioDSPRuntime config.AudioDSP` holds the currently auditioned params
  (committed or preview).
- `m.audioDSPPersisted bool` marks whether runtime equals the persisted
  bridge snapshot.

- `Manager.SetAudioDSP(config.AudioDSP) error` — the committed path. Under
  `m.mu`: validate → update `m.bridge.Audio.DSP` and `m.audioDSPRuntime` →
  mark `m.audioDSPPersisted=true` → if a plane is live, call
  `plane.SetAudioDSP(...)` (recompute coeffs + atomic swap).
- `Manager.PreviewAudioDSP(config.AudioDSP) error` — the unpersisted drag
  path. It uses the same validation/apply helper, updates the current runtime
  snapshot (`m.audioDSPRuntime`) without touching `m.bridge.Audio.DSP`, marks
  `m.audioDSPPersisted=false`, and is allowed to run ahead of disk until the
  trailing commit succeeds or rolls back.
- `Manager.AudioDSP() config.AudioDSP` — reads the current in-memory/runtime
  params, like `OutputVolume()`. On process start and new casts this starts
  from persisted config; during an active preview it may be ahead of disk.
- New `Plane` config field `AudioDSP` set at plane start
  ([plane.go:270](../../../internal/dataplane/plane.go#L270) area), so a new
  cast picks up `m.bridge.Audio.DSP` (the persisted settings) on its first
  field. Uncommitted previews do not leak into a recast/preemption; subsequent
  preview posts can still apply to the new plane live.
- **Memories:** save = write a slot to config; recall = `SetAudioDSP` with
  the stored voicing. Pure config + the same setter.
- `StatusHomeView` gains `AudioDSP config.AudioDSP` plus exported
  `AudioDSPEngaged bool` for the LED and `AudioDSPPersisted bool` for the
  preview/commit wire state, populated next to `OutputVolume`/`Title`.
- **ApplyScope: `ScopeHotSwap`** — a bridge-level live control like volume
  and field-order; never a recast.

`m.mu` guards only the in-memory `m.bridge.Audio.DSP` / `m.audioDSPRuntime` /
`m.audioDSPPersisted` fields and the live `plane.SetAudioDSP` coeff swap (no
I/O under the lock); it is released before the uiserver saver performs the
atomic config write, preserving the "`Manager.mu` is never held across I/O"
invariant.

**Persistence/saver:** add `BridgeSaver.SaveAudioDSP` (+ memory save/recall)
in `internal/uiserver`, and a chassis-owned `AudioDSPSaver` / `AudioDSPViewer`
interface wired in `cmd/mister-groovy-relay/main.go`, exactly paralleling the
existing `VolumeSaver`/`VolumeViewer` pair. The bridge saver touchpoints are
explicit:

- `diffBridgeConfig` reports `audio.dsp` when any nested DSP value or memory
  slot changes.
- `scopeForBridgeField("audio.dsp")` returns `adapters.ScopeHotSwap`.
- `applyHotSwapSideEffects` calls `core.SetAudioDSP(newCfg.Audio.DSP)` after
  a successful atomic config write.
- Bridge-saver tests cover diff detection, hot-swap scope, side-effect
  dispatch, and write-failure/no-apply behavior.

## Chassis UI

- **Template** `internal/chassis/templates/audio-strip.html`
  (`{{define "audio-strip"}}`), mounted in
  [shell.html](../../../internal/chassis/templates/shell.html) **between the
  `transport` and `visualizer-bank`** templates in the `receiver-inner`
  stack. The `.volume-control` block is **removed from**
  [transport.html](../../../internal/chassis/templates/transport.html) and
  rebuilt inside the strip.
- **Data:** new `AudioStripData` in
  [data.go](../../../internal/chassis/data.go) (tone/balance/volume values,
  10 EQ bands, toggle states, preset list + active preset, memory names +
  active slot, `engaged`, `persisted`), populated in the
  `snapshotFromStatusView` path from `view.AudioDSP`, so initial render shows
  real positions.
- **Controls reuse existing idioms.** Knobs (bass/mid/treble/balance/volume)
  generalize [volume-knob.js](../../../internal/chassis/static/volume-knob.js)
  — a hidden `<input type=range>` under a styled dial for keyboard/scroll/a11y.
  EQ bands are vertical `type=range` (±12 dB, 0 detent). Presets/memories are
  `.action-btn`; booleans are `.switch`.
- **Memory UX:** tap M1–M3 = recall, hold = store (with a brief "STORED"
  flash). Presets snap the 10 sliders to a voicing.
- **Live + multi-client sync.** Each change posts to new same-origin routes
  behind `requireSameOrigin`:
  - `POST /receiver/audio/dsp` — merge a partial params patch into the current
    runtime DSP params.
  - `POST /receiver/audio/dsp/memory` — save/recall a slot.
  Dragging uses preview posts for live audio and a trailing commit post on
  pointer/key release. Buttons and switches commit immediately. A new SSE
  `audioDsp` event broadcasts param changes (with `engaged`) so a second
  browser tracks in real time — same pattern as the volume / VFD events
  ([events.go](../../../internal/chassis/events.go)). Change-detection fires
  on param/`engaged`/`persisted` change only; idle wire stays quiet.

  Canonical request examples:

  ```json
  { "commit": false, "params": { "eq": [0,0,0,2,3,3,2,1,0,-1] } }
  { "commit": true,  "params": { "bass": 4.0, "balance": -12 } }
  { "op": "recall", "slot": 1 }
  { "op": "store",  "slot": 2, "name": "M2" }
  ```

  `commit=false` validates and applies a preview through
  `Manager.PreviewAudioDSP` without writing disk. `commit=true` validates,
  writes the bridge config atomically through `SaveAudioDSP`, then applies via
  the saver hot-swap side effect.
  SSE payloads use one shape for both cases:

  ```json
  {
    "params": { "enabled": true, "mono": false, "subsonic": false,
      "loudness": false, "bass": 0, "mid": 0, "treble": 0,
      "balance": 0, "eq": [0,0,0,0,0,0,0,0,0,0] },
    "engaged": false,
    "persisted": true
  }
  ```
- New static `audio-strip.js` (registered in shell.html's script list) +
  styles in [chassis.css](../../../internal/chassis/static/chassis.css).
  `prefers-reduced-motion` is honored on dial/needle transitions.

## Error handling & edge cases

- **Validation before apply/disk:** out-of-range → `400` with a field error,
  no apply, no write (mirrors the volume handler).
- **Preview/commit ordering:** previews intentionally let runtime run ahead
  of disk during a drag. Commits use validate → atomic config write → apply
  live. If the write fails before the hot-swap side effect, the handler rolls
  runtime back to the old `BridgeSaver.Current().Audio.DSP`, emits an
  `audioDsp` SSE update with `persisted=true`, and returns the error. If the
  write succeeds but the hot-swap apply fails, disk is now authoritative; the
  handler reconciles runtime to the committed `BridgeSaver.Current().Audio.DSP`
  as a best-effort retry, emits the same SSE shape, and returns the apply
  error. After a commit succeeds or fails, runtime and persisted config
  converge again. Concurrent commits serialize on the shared saver mutex;
  preview posts are throttled by the client and the final commit coalesces the
  drag.
- **No active session:** committed `SetAudioDSP` updates persisted + runtime
  state and applies on the next cast start (like volume when idle). Preview
  updates runtime/UI state only; without a commit, the next cast still starts
  from persisted DSP.
- **Preemption:** each new plane reads persisted DSP at start and owns its
  own coeff pointer + filter state.
- **Backward-compat:** missing `[bridge.audio.dsp]` → transparent defaults;
  existing configs behave identically.
- **No-audio casts** (audio output disabled,
  [pipeline.go:111](../../../internal/ffmpeg/pipeline.go#L111)
  `audioOutputEnabled`): the DSP stage simply isn't in the path.
- **Empty memory recall** → no-op (button disabled). **Reduced motion**
  honored. **SSE** emits only on change.

## Testing

Keep all four CI gates green: `go vet ./...`, `go test ./...`,
`go test -race ./...`, `go test -tags=integration ./tests/integration/...`.

- **`audiodsp` unit tests:** per-filter gain correctness (known sine at band
  center → measured gain within tolerance), pass-through slot = identity,
  cascade stability at 22050/44100/48000 including inactive above-Nyquist
  slots, **float→int16 saturates (never wraps)**, mono fold,
  the **attenuate-only balance law** (full-left mutes R, leaves L at unity;
  never exceeds unity), and **click-free transitions** — both an incremental
  coeff swap (retained state, no discontinuity) **and** a hard on/off toggle
  (assert the ramp/crossfade bounds the boundary discontinuity, e.g. within a
  small dB threshold, rather than a full-amplitude step), including an update
  that supersedes a ramp mid-flight.
- **Transparency guarantee:** defaults (flat + nothing engaged) → output PCM
  equals input within ±1 LSB, and the transparent+volume path remains
  bit-identical to the existing `scalePCMVolumeInPlace` tests. The
  no-regression anchor.
- **dataplane:** `SetAudioDSP` applies; DSP↔volume interaction; existing
  `TestSendAudioAppliesOutputVolume` still passes.
- **core:** `SetAudioDSP` committed path, `PreviewAudioDSP` unpersisted path,
  `AudioDSP()` getter, range rejection, rollback to persisted params, and
  `StatusHomeView` carries params + `engaged` + `persisted` — mirrors the
  volume tests.
- **config + uiserver:** `[bridge.audio.dsp]` + memories round-trip,
  missing-table defaults, saved-flat memory distinguished from empty memory,
  `diffBridgeConfig`/`scopeForBridgeField("audio.dsp")` hot-swap coverage,
  `SaveAudioDSP`/memory save-recall validate-then-atomic-write, concurrent
  serialize, and write-failure skips live apply.
- **chassis:** `snapshotFromStatusView` maps `view.AudioDSP` →
  `AudioStripData`; `audioDsp` SSE envelope + change-detection (fires on
  param/`engaged`/`persisted` change, not on unrelated fields); route tests
  cover preview apply, commit save, failed commit rollback, memory store/recall
  envelopes, and same-origin rejection; template renders the strip + states;
  **migrate the volume tests** (volume relocated out of
  transport into the strip) — including any
  [transport.html](../../../internal/chassis/templates/transport.html)
  template assertions and behavior tests; new JS behavior test for the strip
  (knob/slider/preset/memory/switch + debounced POST + reduced-motion).
- **integration:** SSE body includes `audioDsp`; end-to-end POST applies;
  fake-mister smoke confirms flat DSP = unchanged audio and 22050 Hz does not
  design above-Nyquist filters.

Before implementation, enumerate the volume-relocation fallout:

```bash
rg 'volume-control|data-volume|output_volume|OutputVolume' internal/chassis tests/integration
```

Move the transport-owned volume hits into the new strip's template/JS/tests.

## Out of scope (non-goals)

- Video picture controls (brightness/contrast/color/tint/sharpness) and DSP
  reverb sound-field presets.
- Spectrum backlight behind the EQ (the meter already renders a 32-band
  analyzer).
- Volume-tracking (equal-loudness) Loudness; auto-makeup-trim for boost
  headroom.
- Per-source / per-adapter DSP — the processor is global, like a physical
  AV stack's signal chain.
- Any change to the transport actions, source cluster, meter, visualizer,
  catalog, or settings panes beyond relocating the volume knob.

## Risks & mitigations

- **Clicks/pops** — incremental knob drags are click-free via retained state
  across coeff swaps; hard on/off toggles (defeat, subsonic, mono) and large
  jumps are smoothed by a short gain ramp/crossfade (see "Parameter
  smoothing"). Covered by the click-free transition test.
- **Boost clipping** — saturating clamp prevents wraparound; float pipeline
  keeps intermediate precision; documented operator guidance (cut, not only
  boost).
- **CPU per tick** — stereo × 16 biquads in float at 48 kHz is modest, but
  the implementation should benchmark the apply stage and confirm no field
  underruns under load (the meter already surfaces underruns/drops).
- **Volume-relocation regressions** — the `rg` sweep above + migrated tests
  ensure the moved control keeps working and SSE volume sync is intact.
- **Wire-format addition** — the new `audioDsp` SSE event and routes are
  internal (server + bundled JS ship together); no external consumers.
