# Receiver Chassis Meter Low-Rate Telemetry - Phase 2 / Spec 5A Design

**Status:** Brainstormed; awaiting implementation plan.
**Scope:** First sub-project of Phase 2 (Telemetry Meters). Makes the receiver chassis meter screen real for low-rate session, pipeline, source, HLS, network, ACK, field-order, and link telemetry. Spec 5B follows with real audio-analysis telemetry for spectrum, goniometer, VU, phase correlation, and LUFS.
**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored; this spec is force-added per the convention established by the receiver chassis rollout docs.

## Background

[Phase 0](2026-05-21-receiver-chassis-foundation-design.md) shipped the complete receiver chassis chrome at `/receiver` with an idle meter screen. Phase 1 wired live VFD state, visualizer-mode control, and transport controls through the shared `/receiver/events` SSE stream. The frozen v24 reference mockup at [docs/superpowers/reference/2026-05-21-receiver-v24.html](../reference/2026-05-21-receiver-v24.html) contains a much richer three-row meter surface:

- Row 1: `AUDIO IN`, `AUDIO OUT`, `SRC`, `CROP`, `HLS BUF`, `DROPS`.
- Row 2: spectrum, goniometer, bitrate, kHz, mode, NTSC/PAL lamps, ODD/EVEN field lock, MB/S, throughput graph, MS ACK, ACK scatter.
- Row 3: L/R VU, phase, LUFS, `OUTPUT`, `ASPECT`, `PIPE`, `SPEED`, `LINK`.

The user decision for Phase 2 is that all data present in the reference mockup should become real. To keep implementation and review tractable, Phase 2 is split:

- **Spec 5A (this spec):** low-rate meter data that can be derived from core session state, stored probe/crop facts, data-plane counters, bridge config, and optional provider overlays.
- **Spec 5B:** real audio-analysis telemetry from PCM, including spectrum bands, goniometer points, L/R VU, phase correlation, and LUFS.

Spec 5A must not animate fake audio values. It can reserve hooks and quiet display states for Spec 5B, but once the meter is real, the remaining audio scopes should be explicitly pending rather than theatrical.

## Goals

1. Add a single `meter` named event to `GET /receiver/events` carrying low-rate meter data for the full three-row meter screen, excluding real audio-analysis samples reserved for Spec 5B.
2. Extend core's read-only session view with stable session-start facts: probe summary, crop summary, effective aspect mode, modeline timing, output format, compression flags, and audio output shape.
3. Preserve the existing Phase 1 snapshot-cache invariant: the chassis refresher acquires `core.Manager` state once per tick total, regardless of browser tab count.
4. Let adapters optionally contribute source-specific low-rate overlays, starting with URL and Streams HLS buffer stats.
5. Add `internal/chassis/static/meter.js` to update meter DOM hooks, HLS lamps, throughput history, ACK history, field-order state, and quiet audio-scope pending states.
6. Keep `/ui/*` unchanged. The chassis remains additive under `/receiver/*`.

## Non-Goals

- Real spectrum bands, goniometer dots, L/R VU bars, phase correlation, or LUFS calculations. Spec 5B owns PCM taps and audio analysis.
- A telemetry pub/sub broker in `core.Manager`. Spec 5A uses the existing snapshot cache plus a low-rate meter sampler. Spec 5B can introduce a broker if high-rate audio telemetry demands it.
- New settings, settings drawer wiring, event-log UI, preset/catalog browsing, or source-cluster actions.
- Per-frame ACK round-trip measurement unless the existing protocol exposes it cleanly during implementation. Spec 5A treats `LastACKAge` as ACK freshness, not guaranteed RTT.
- Replacing the old `/ui` status surfaces.

## User Decisions

| Topic | Decision |
| --- | --- |
| Phase 2 shape | Split into 5A low-rate meter telemetry and 5B audio-analysis telemetry. |
| Data completeness | The v24 reference mockup is the target data surface. 5A handles every non-audio-analysis field now; 5B handles audio scopes later. |
| Data authenticity | No fake audio animation after 5A. Unknown values render as explicit placeholders. |
| Ownership | Core owns stable pipeline facts and runtime counters; adapters contribute optional overlays; chassis formats for display. |
| SSE shape | One combined `meter` event on the existing `/receiver/events` stream. |
| Cadence | 2 Hz live meter emission for 5A. Initial snapshot always includes `meter`. |

## Done When

- A live cast renders real low-rate meter text on `/receiver` within about 500 ms: `AUDIO OUT`, `SRC`, `CROP`, `HLS BUF`, `DROPS`, bitrate, kHz, mode, standard lamps, field lock, MB/S, MS ACK, `OUTPUT`, `ASPECT`, `PIPE`, `SPEED`, and `LINK`.
- The initial SSE burst on `/receiver/events` emits `state`, `vfd`, `visualizer`, `transport`, then `meter`.
- During a live session, `meter` updates arrive at about 2 Hz and include enough numeric samples for the throughput and ACK canvases.
- Multiple open chassis tabs consume the same cached meter snapshots without increasing `core.Manager.StatusHomeView()` call rate.
- HLS-buffered URL and Streams casts show live HLS segment counts and selected-variant data when available. Non-HLS casts show `0 / 0 SEG`.
- Idle or unknown values show placeholders, not demo values.
- Audio scopes are visually quiet and marked as pending/empty until Spec 5B.
- `/ui/*` behavior and response shapes are unchanged.

## Architecture

### Core Base Data

Core already exposes `StatusHomeView` with state, source, modeline, position, duration, generation, and data-plane counters. Spec 5A extends that read-only view with a core-owned meter summary. The design prefers core-owned structs rather than exporting `ffmpeg.ProbeResult`, `ffmpeg.CropRect`, or `groovy.Modeline` through UI-facing types.

Conceptual shape:

```go
type StatusHomeView struct {
    // existing fields...
    Meter MeterHomeView
}

type MeterHomeView struct {
    Source     SourceMeterView
    Crop       CropMeterView
    Pipeline   PipelineMeterView
    Runtime    RuntimeMeterView
}

type SourceMeterView struct {
    Width            int
    Height           int
    FrameRate        float64
    Interlaced       bool
    VideoCodec       string
    AudioCodec       string
    AudioRate        int
    AudioChannels    int
    VideoBitrateBPS  int64
    AudioBitrateBPS  int64
    FormatBitrateBPS int64
}

type CropMeterView struct {
    Mode          string // effective aspect mode: "letterbox", "zoom", "auto"
    Detected      bool
    W, H, X, Y     int
}

type PipelineMeterView struct {
    ModelineName       string
    OutputWidth        int
    OutputHeight       int
    FieldHeight        int
    FieldRateHz        float64
    HorizontalKHz      float64
    InterlacedOutput   bool
    Standard           string // "ntsc", "pal", or ""
    FieldOrder         string // "tff" or "bff"
    RGBMode            string
    LZ4Enabled         bool
    DeltaLZ4Enabled    bool
    AudioSampleRate    int
    AudioChannels      int
    AudioOutputVolume  int
    EffectiveAspectMode string
}

type RuntimeMeterView struct {
    BlitsTotal  uint64
    FramesTotal uint64
    Underruns   uint64
    WireBytes   uint64
    LastACKAge  time.Duration
    StartedAt   time.Time
    Generation  uint64
}
```

Implementation can tune exact names, but the boundaries should stay:

- Core exposes facts and counters.
- Chassis formats display strings.
- No core import of `internal/chassis`, `internal/ui`, or `internal/uiserver`.
- No network probing while holding `Manager.mu`.

### Capturing Session Facts

`core.Manager.probeForStart` already has the source probe, crop result, effective aspect mode, resolved FFmpeg path, and media input policy before `startPlaneLocked` begins. `startPlaneLocked` already resolves the modeline preset, RGB mode, field height, audio output, and compression flags. Spec 5A records a compact meter summary on `activeSession` at that boundary.

The active session should retain:

- Probe-derived source facts: width, height, frame rate, interlace flag, audio rate, duration, codec names, channels, and bitrate where available.
- Crop/effective aspect facts: whether a crop was detected, crop rectangle, and final aspect mode.
- Pipeline facts: modeline name, output dimensions, field height, field rate, horizontal kHz, standard, field order, RGB mode, LZ4 flags, audio sample rate/channels, and output volume.
- Runtime facts: existing plane counters copied from the current plane on each `StatusHomeView()`.

`ffmpeg.ProbeResult` should grow the extra fields needed by the meter: video codec, audio codec, audio channels, and bitrate values. If ffprobe omits a value, leave it zero and let chassis render placeholders.

### Provider Overlays

Some meter data belongs to adapters because core does not know why a given stream URL was transformed. The first required overlay is HLS buffering.

Add a neutral adapter-side interface, likely in `internal/adapters`, so the chassis can discover it structurally through the existing registry:

```go
type MeterOverlayProvider interface {
    MeterOverlay(ctx context.Context, snap core.StatusHomeView) (MeterOverlay, bool)
}

type MeterOverlay struct {
    HLS *HLSMeterOverlay
}

type HLSMeterOverlay struct {
    CachedSegments        int
    MaxCachedSegments     int
    CachedMediaDurationMS int
    CacheBytes            int64
    PlaylistReloadsTotal  int64
    SegmentDownloadsTotal int64
    SelectedVariantWidth  int
    SelectedVariantHeight int
    SelectedVariantBPS    int64
    FailureReason         string
}
```

URL and Streams already hold `*hlsbuffer.Session` during buffered casts. Spec 5A makes that state observable while the adapter owns the active core session:

- Hold the active HLS stats function together with adapter ref and generation.
- Return overlay only when `snap.AdapterRef` and `snap.Generation` match the active buffered session.
- Clear overlay on stop/preempt through the existing `OnStop` cleanup path.
- Do not expose source URLs or tokens through the overlay.

Non-HLS providers return no overlay; chassis renders `0 / 0 SEG`.

### Chassis Composition

`ReceiverPageData.Meter` remains the template-facing data. Spec 5A extends it with raw fields that the client needs for canvas histories and state resets.

The chassis snapshot refresher owns a small `meterSampler`:

- Input: the latest `StatusHomeView`, optional provider overlay, and wall-clock time.
- Output: display-ready `MeterData` plus raw numeric samples.
- State: previous generation, previous wire bytes, previous blit count, previous sample time, throughput history, and ACK history.
- Reset: when session generation changes or state returns idle.

SSE handlers should read the cached `ReceiverPageData`. They must not compute byte deltas per tab. This preserves the Spec 2 lock and fan-out invariant.

## Meter Event Contract

The `meter` event is one JSON payload. The field names are camelCase to match existing chassis events.

Conceptual payload:

```json
{
  "state": "live",
  "generation": 12,
  "sourceStrip": {
    "audioIn": "AAC LC - STEREO",
    "audioOut": "S16LE - 48k",
    "src": "1280x720@30 - H.264",
    "crop": "NONE - 16:9 NATIVE",
    "hlsBuffer": "6 / 12 SEG",
    "hlsCachedSegments": 6,
    "hlsMaxSegments": 12,
    "hlsCacheBytes": 12345678,
    "drops": "0.0"
  },
  "midRow": {
    "bitrateMbps": "2.4",
    "freqKHz": "15.7",
    "mode": "704",
    "standard": "ntsc",
    "fieldOrder": "tff",
    "fieldLock": "LOCK",
    "throughputMBs": "14.2",
    "throughputSampleMBs": 14.2,
    "throughputHistoryMBs": [14.0, 14.3, 14.2],
    "ackMS": "04.0",
    "ackSampleMS": 4.0,
    "ackHistoryMS": [3.8, 4.0, 4.1]
  },
  "readout": {
    "output": "INTERLACE 480i - BT.601",
    "aspect": "4:3 LETTERBOX",
    "pipe": "LZ4+D - TFF",
    "speed": "1.00x LOCK",
    "speedRatio": 1.0,
    "link": "MiSTer - 4ms"
  },
  "audioScopes": {
    "status": "pending"
  }
}
```

The implementation can avoid sending full histories if the client maintains them from samples. The server must at least send the latest raw numeric throughput and ACK samples. If histories are server-owned, keep them bounded:

- Throughput: last 60 samples at 2 Hz, covering 30 seconds.
- ACK: last 128 samples, matching the reference scatter graph capacity.

### Field Mapping

| Mockup field | 5A source | Idle/unknown |
| --- | --- | --- |
| `AUDIO IN` | probe audio codec + channels, or provider/probe overlay | `---` |
| `AUDIO OUT` | bridge audio format from pipeline summary | `---` when no session |
| `SRC` | probe width/height/frame-rate + video codec | `---` |
| `CROP` | effective aspect + crop summary | `---` when no source, `NONE` when known no crop |
| `HLS BUF` | provider HLS overlay | `0 / 0 SEG` |
| `DROPS` | underruns divided by emitted opportunities | `0.0` |
| bitrate | probe or selected-variant bitrate | `---` |
| kHz | modeline horizontal frequency | `---` |
| mode | BT.601 clean aperture for shipped SD modelines, fallback output active width | `---` |
| NTSC/PAL | modeline standard | both dim when unknown |
| ODD/EVEN/LOCK | field order + field rate; visual tick may be client clocked | idle lock |
| MB/S | wire-byte delta per sample | `0.0` |
| MS ACK | ACK freshness from `LastACKAge`; not guaranteed RTT | `--` |
| `OUTPUT` | scan mode and color matrix | `---` |
| `ASPECT` | effective aspect display string | `---` |
| `PIPE` | compression flags + field order | `---` |
| `SPEED` | blit rate divided by expected field rate | `---` |
| `LINK` | MiSTer ACK freshness summary | `---` |
| spectrum/goniometer/VU/phase/LUFS | reserved for Spec 5B | quiet/pending |

### Formatting Rules

Chassis owns display formatting. Core and adapters expose facts.

- Codec names are normalized for display: examples include `H.264`, `AAC LC`, `MP3`, `OPUS`.
- Frame rate displays as an integer when very close to whole, otherwise one or two decimals.
- Source shape displays as `<width>x<height>@<fps> - <codec>`.
- Audio output displays as `S16LE - 48k` for 48 kHz, `S16LE - 44.1k` for 44.1 kHz.
- Horizontal kHz displays with one decimal.
- Throughput displays in decimal MB/s using `1000 * 1000` bytes per MB, matching network operator convention.
- ACK displays with one decimal, left-padded to match the v24 feel where useful, for example `04.0`.
- `PIPE` uses ASCII display text in code and JSON, for example `LZ4+D - TFF`.
- `SPEED` is `1.00x LOCK` when within a small tolerance of expected field rate; otherwise `0.98x SLOW` or `1.02x FAST`.

## SSE Behavior

`GET /receiver/events` keeps the existing stream and adds `meter`.

Initial burst order:

1. `state`
2. `vfd`
3. `visualizer`
4. `transport`
5. `meter`

Live cadence:

- The server snapshot refresher can keep its existing 250 ms cadence.
- `meter` emits at most every 500 ms while live, unless a session generation change requires immediate convergence.
- Idle transitions emit one final idle `meter` event so clients clear histories.
- Heartbeats remain unchanged.

`meterChanged` should compare the event fields or generation, not unrelated page data. It can also gate by cadence so text-only jitter does not cause excessive writes.

## Client Behavior

Add `internal/chassis/static/meter.js` after `transport.js` and `visualizer-bank.js` in `shell.html`, or before them if implementation needs meter hooks initialized earlier. It uses the shared `window.Chassis.events.subscribe` surface from `vfd-live.js`.

Client responsibilities:

- Update only explicit `data-meter-*` hooks. Do not rely on `nth-child` selectors.
- Update HLS buffer lamps from `hlsCachedSegments` and `hlsMaxSegments`.
- Draw throughput and ACK canvases from incoming samples or histories.
- Reset histories on idle state or generation change.
- Respect `prefers-reduced-motion`: draw latest/static snapshots without animated trails.
- Keep audio scopes quiet while `audioScopes.status == "pending"`.
- Avoid opening any additional EventSource connection.

Template work:

- Add `data-meter-*` hooks throughout `internal/chassis/templates/meter.html`.
- Keep existing visual structure and CSS class names where possible.
- Add pending/empty audio-scope hooks without introducing fake values.

## Error Handling And Unknowns

Unknowns are normal:

- Missing probe codec or bitrate: render `---` for that component.
- No crop probe: render `NONE` when source shape is known and no crop was detected; render `---` when no source shape is known.
- No HLS overlay: render `0 / 0 SEG` and all buffer lamps off.
- No ACK yet: render `--` and an empty ACK canvas.
- No wire-byte delta yet after session start: render `0.0 MB/S` until the second sample.
- HLS overlay failure reason: do not show raw URLs. If exposed, keep it in a debug-only field or sanitized text.

Provider overlay failures should not break the SSE stream. The chassis should log and render placeholders for that overlay only.

## Package Boundaries

The package boundaries remain:

- `internal/core` may import `internal/ffmpeg`, `internal/dataplane`, and `internal/groovy`, but not `internal/chassis`, `internal/ui`, `internal/uiserver`, or `internal/adapters`.
- `internal/adapters` may define the optional meter overlay interface and may import `internal/core`.
- `internal/chassis` may import `internal/core`, `internal/config`, and `internal/adapters`, and may use the existing registry to discover overlay providers.
- `internal/ui` is not changed for 5A.

Update `internal/chassis/import_check_test.go` so production code keeps these boundaries: `internal/core` cannot import `internal/adapters`, `internal/chassis`, `internal/ui`, or `internal/uiserver`; `internal/adapters` cannot import `internal/chassis`, `internal/ui`, or `internal/uiserver`; and `internal/chassis` remains free of `internal/ui` and `internal/uiserver`.

## Testing

Layer 1, core:

- `StatusHomeView` includes zero-value meter data when idle.
- Starting a session stores probe, crop, aspect, pipeline, and output audio facts.
- `StatusHomeView` updates runtime counters from a fake plane without re-probing.
- Interlace field-order hot-swap updates the meter view.
- Output volume hot-swap updates audio output facts if volume is included in the meter view.
- Probe parser captures codec names, channels, and bitrate fields.

Layer 1, adapters:

- URL HLS-buffered cast exposes HLS stats only while its adapter ref + generation owns the active session.
- Streams HLS-buffered cast exposes the same overlay.
- Overlay clears on stop/preempt.
- Overlay never leaks raw source URLs or tokens.

Layer 1, chassis:

- `MeterData` mapping renders idle placeholders exactly.
- Live mapping formats source, output, crop/aspect, pipe, speed, link, HLS, drops, and standard lamps.
- Meter sampler computes throughput from `WireBytes` deltas and resets on generation change.
- Meter sampler computes speed from blit deltas and expected field rate.
- Initial SSE burst includes `meter` after `transport`.
- Live SSE emits `meter` at the expected 2 Hz cadence, not every 250 ms tick.
- Multiple SSE clients do not multiply `StatusHomeView()` calls.

Static asset tests:

- `meter.js` subscribes through `window.Chassis.events.subscribe`.
- `meter.js` does not create `new EventSource`.
- `meter.js` uses `data-meter-*` hooks.
- `meter.js` does not contain pseudo audio generators for spectrum/goniometer/VU/LUFS.

Integration:

- A fake plane with deterministic counters drives `/receiver/events`; the `meter` payload shows throughput, ACK, drops, pipe, speed, and link.
- A buffered URL or Streams path shows HLS buffer stats in the `meter` payload.
- `/ui/*` route-shadowing tests remain green.

Manual browser checklist:

- Open `/receiver` during a live cast. Verify source strip, video stats, network stats, and readout line populate within about 500 ms.
- Open two `/receiver` tabs. Verify throughput and ACK graphs update in both without opening multiple SSE streams per tab.
- Stop the cast. Verify all low-rate meter fields return to idle placeholders and canvas histories clear.
- Start a buffered HLS cast. Verify the HLS bar lights according to cached segment count.

## Performance

Spec 5A is intentionally low-rate:

- `StatusHomeView()` remains one call per server refresher tick, not one call per connected tab.
- Throughput and speed are delta calculations over existing counters.
- Provider overlays are in-memory reads; no network I/O.
- Canvas histories are bounded.
- The SSE stream remains text JSON; no binary transport is introduced.

If Spec 5B's audio telemetry needs higher cadence or PCM volume, that spec can introduce a dedicated broker or a second event family without revisiting 5A's display contract.

## Rollout And Rollback

Spec 5A is additive. The chassis already renders idle meter placeholders, and `/ui/*` continues to be the production UI until final cutover.

Rollback is a revert of the implementation merge commit:

- `meter` SSE event disappears.
- `meter.js` is no longer served.
- Core meter view fields disappear.
- Existing VFD, visualizer, transport, and `/ui/*` behavior remain intact if the revert is clean.

## Risks

| Risk | Mitigation |
| --- | --- |
| Probe data is incomplete for some streams | Render placeholders and prefer provider HLS bitrate when available. |
| `MS ACK` is mistaken for RTT | Document it as ACK freshness in 5A; reserve true RTT for a later protocol-aware change. |
| Meter event spam increases browser work | Gate `meter` emission to 2 Hz and keep histories bounded. |
| Provider overlay leaks URLs | Overlay structs carry counts/variant metadata only; tests scan for raw URL leakage. |
| Field-order animation appears fake | Event carries real field order/rate; client clock only visualizes cadence. No session facts are invented. |
| Core status structs become UI-shaped | Core exposes neutral facts only; chassis owns display strings. |

## Follow-Up: Spec 5B

Spec 5B should add real audio telemetry:

- PCM tap location and ownership.
- Spectrum band calculation for the six reference bands.
- Goniometer sample stream or reduced point cloud.
- L/R VU levels.
- Phase correlation.
- Short-term LUFS.
- Cadence, batching, and whether a pub/sub broker is needed.

Spec 5A reserves quiet hooks and event space, but it does not compute or fake these values.
