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

- A live cast renders real low-rate meter text on `/receiver` within about 500 ms: `AUDIO IN`, `AUDIO OUT`, `SRC`, `CROP`, `HLS BUF`, `DROPS`, bitrate, kHz, mode, standard lamps, field lock, MB/S, MS ACK, `OUTPUT`, `ASPECT`, `PIPE`, `SPEED`, and `LINK`.
- The initial SSE burst on `/receiver/events` emits `state`, `vfd`, `source`, `visualizer`, `transport`, then `meter` — `source` is the existing Phase 1 event surfaced by `handleEvents` (events.go) and must be preserved.
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
    Width                 int
    Height                int
    FrameRate             float64
    Interlaced            bool
    SampleAspectRatioNum  int
    SampleAspectRatioDen  int
    DisplayAspectRatioNum int
    DisplayAspectRatioDen int
    VideoCodec            string
    AudioCodec            string
    AudioRate             int
    AudioChannels         int
    VideoBitrateBPS       int64
    AudioBitrateBPS       int64
    FormatBitrateBPS      int64
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
    Generation  uint64 // duplicate of StatusHomeView.Generation, copied here
                       // so the sampler can read one atomic snapshot of all
                       // runtime fields it needs without re-reading the
                       // outer view.
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

- Probe-derived source facts: width, height, frame rate, interlace flag, sample/display aspect ratio, audio rate, duration, codec names, channels, and bitrate where available.
- Crop/effective aspect facts: whether a crop was detected, crop rectangle, final aspect mode, and the source display aspect used to derive `CROP` and `ASPECT` labels.
- Pipeline facts: modeline name, output dimensions, field height, field rate, horizontal kHz, standard, field order, RGB mode, LZ4 flags, audio sample rate/channels, and output volume.
- Runtime facts: existing plane counters copied from the current plane on each `StatusHomeView()`.

`ffmpeg.ProbeResult` should grow the extra fields needed by the meter: video codec, audio codec, audio channels, sample aspect ratio, display aspect ratio, and bitrate values. If ffprobe omits a value, leave it zero and let chassis render placeholders. Chassis may derive display aspect from width, height, and sample aspect ratio only when ffprobe does not provide display aspect directly.

### Provider Overlays

Some meter data belongs to adapters because core does not know why a given stream URL was transformed. The first required overlay is HLS buffering.

Add a neutral adapter-side interface in `internal/adapters`, following the same structural-discovery pattern used by `internal/playback.Dispatcher` for `PlaybackControlProvider`. The chassis discovers providers through the existing adapter registry by type assertion. A `TestMeterOverlayProvider_Registration` test asserts that URL and Streams both satisfy the interface after registration; any future adapter that owns a buffered or otherwise observable source MUST also satisfy it.

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

`HLSMeterOverlay.CachedSegments`, `CachedMediaDurationMS`, and `CacheBytes` mean **current local cache occupancy**, not lifetime downloaded segment totals. Existing `hlsbuffer.Stats` currently exposes cumulative `CachedSegments`, `CachedMediaDuration`, and `CacheBytes`; the implementation must either extend `hlsbuffer.Stats` with current cache occupancy or expose an equivalent safe snapshot from `hlsbuffer.Session`. `SegmentDownloadsTotal` remains the lifetime counter.

URL and Streams currently create `*hlsbuffer.Session` values for buffered casts and capture them for cleanup. Spec 5A must also store an active HLS stats handle, under the adapter's existing lock, while the adapter owns the active core session:

- After a successful core start, read `core.Status()` outside adapter locks and install the stats handle only when the returned `AdapterRef` matches the request. Store the returned core generation with the handle; do not use Streams queue generation as a substitute.
- Capture `MaxCachedSegments` from the normalized HLS buffer config used to open the active session. Do not read the current bridge config during overlay rendering; hot-swapped config should affect the next HLS session, not the denominator for the current one.
- Return overlay only when `snap.AdapterRef` and `snap.Generation` match the active buffered session.
- Clear overlay on stop/preempt through the existing `OnStop` cleanup path, before any adapter state mutation or HLS close work.
- Do not expose source URLs, playlist URLs, segment URLs, variant URIs, cookies, headers, tokens, or raw failure strings through the overlay.

The overlay conversion is an allowlist. Adapters may read broad `hlsbuffer.Stats`, but `toHLSMeterOverlay` copies only current cache counts/durations/bytes, reload/download counters, selected variant width/height/bandwidth, captured max segments, and a sanitized short failure reason. It must never copy `hlsbuffer.Variant.URI`, `Variant.Codecs`, or unsanitized `Stats.FailureReason`.

Non-HLS providers return no overlay; chassis renders `0 / 0 SEG`.

Install / clear ordering (both URL and Streams use this exact shape; this is the only place spec text fixes the order, so other future overlays can copy it verbatim):

```
// install (inside adapter.startBuffered, on the same goroutine that
// returned from core.StartSession):
hlsCfg := hlsConfigFromBridge(bridge.HLSBuffer)
session := openHLSBuffer(..., hlsCfg)    // *hlsbuffer.Session
ref := newAdapterRef()
if err := core.StartSession(req with AdapterRef=ref, OnStop=cleanup); err != nil {
    session.Close()
    return err
}
st := core.Status()
adapter.mu.Lock()
if st.AdapterRef == ref && st.Generation != 0 {
    adapter.activeOverlay = &hlsHandle{
        ref:               ref,
        generation:        st.Generation,
        stats:             session.Stats,   // hlsbuffer.Stats accessor
        maxCachedSegments: hlsCfg.MaxCachedSegments,
    }
}
adapter.mu.Unlock()
// If the guard failed (ref mismatch or generation 0), the handle is
// dropped on the floor; OnStop will still close `session` because the
// session is captured in the cleanup closure independently.

// read (inside MeterOverlay, on the chassis refresher goroutine):
adapter.mu.RLock()
h := adapter.activeOverlay
adapter.mu.RUnlock()
if h == nil || h.ref != snap.AdapterRef || h.generation != snap.Generation {
    return MeterOverlay{}, false
}
return MeterOverlay{HLS: toHLSMeterOverlay(h.stats(), h.maxCachedSegments)}, true

// clear (inside OnStop closure, before any other OnStop work):
adapter.mu.Lock()
if adapter.activeOverlay != nil && adapter.activeOverlay.ref == stoppedRef {
    adapter.activeOverlay = nil
}
adapter.mu.Unlock()
```

The existing URL and Streams `withHLSBufferCleanup` helpers currently call their base `OnStop` before closing the HLS session. Spec 5A must change or bypass that wrapper so the order becomes: clear overlay handle, run adapter `OnStop` state work, then close the HLS session.

Overlay call-site contract. `MeterOverlay` is called from the chassis snapshot refresher goroutine, once per refresher tick per registered provider. It MUST:

- Return promptly (no network I/O, no disk I/O, no waiting on channels).
- Not hold an adapter lock across the return value. Copy the active overlay handle under the adapter lock, release it, then call the handle's internally synchronized stats accessor and build an allowlisted `MeterOverlay`.
- Tolerate a `snap` that does not match the active session by returning `(MeterOverlay{}, false)`; chassis treats `ok == false` and a zero-value return identically.
- Survive a panic inside one provider: chassis recovers per call so one bad provider cannot stall the meter for the rest. A recovered panic logs once and renders that overlay's fields as placeholders.

### Chassis Composition

`ReceiverPageData.Meter` remains the template-facing data. Spec 5A extends it with raw fields that the client needs for canvas histories and state resets.

The chassis snapshot refresher owns a small `meterSampler`:

- Input: the latest `StatusHomeView`, optional provider overlay, and wall-clock time.
- Output: display-ready `MeterData` plus raw numeric samples.
- State: previous generation, previous wire bytes, previous blit count, previous underrun count, previous sample time, throughput history, and ACK history.
- Reset: when session generation changes or state returns idle.
- Paused sessions keep the latest text facts and freeze throughput, speed, and ACK histories instead of appending zero samples. If a paused session has no plane, the event state remains `live` with `paused: true` so clients can preserve the last real values without inventing motion.

SSE handlers should read the cached `ReceiverPageData`. They must not compute byte deltas per tab. The initial `meter` burst uses the cached snapshot as well; it must not call `snapshotFromSession` or `StatusHomeView()` directly per connection. This preserves the Spec 2 lock and fan-out invariant.

## Meter Event Contract

The `meter` event is one JSON payload. The field names are camelCase to match existing chassis events.

Conceptual payload:

```json
{
  "state": "live",
  "paused": false,
  "generation": 12,
  "sourceStrip": {
    "audioIn": "AAC LC - STEREO",
    "audioOut": "S16LE - 48k - STEREO",
    "src": "1280x720@30 - H.264",
    "crop": "NONE - 16:9 NATIVE",
    "hlsBuffer": "6 / 12 SEG",
    "hlsCachedSegments": 6,
    "hlsMaxSegments": 12,
    "hlsCacheBytes": 12345678,
    "drops": "0.0",
    "dropsPercent": 0.0,
    "blitsTotal": 7200,
    "underrunsTotal": 0
  },
  "midRow": {
    "bitrateMbps": "2.4",
    "freqKHz": "15.7",
    "mode": "704",
    "standard": "ntsc",
    "fieldOrder": "tff",
    "fieldRateHz": 59.94,
    "interlacedOutput": true,
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

The example value `"mode": "704"` is the BT.601 clean-aperture width for the shipped SD modelines (480i/576i); for any future non-BT.601 modeline it falls back to the output active width. The field is intentionally generic — never anchor a client check on the literal `704`.

Clients MUST ignore unknown top-level keys and unknown sub-keys. Spec 5B will add real values under `audioScopes` (e.g. `audioScopes.spectrum`, `audioScopes.lufs`) and may add other top-level keys; the 5A schema is forward-compatible by convention, not by version field.

The implementation can avoid sending full histories if the client maintains them from samples. The server must at least send the latest raw numeric throughput and ACK samples. If histories are server-owned, keep them bounded:

- Throughput: last 60 samples at 2 Hz, covering 30 seconds.
- ACK: last 128 samples at 2 Hz, covering ≈64 seconds. Note: the v24 mockup's ACK scatter is visually evocative of per-frame samples, but Spec 5A samples `LastACKAge` at 2 Hz only, so the rendered scatter is a sparse trail rather than a true per-field RTT cloud. A future protocol-aware change can densify this without revisiting the event contract.

### Field Mapping

| Mockup field | 5A source | Idle/unknown |
| --- | --- | --- |
| `AUDIO IN` | probe audio codec + channels, or provider/probe overlay | `---` |
| `AUDIO OUT` | bridge audio format from pipeline summary | `---` when no session |
| `SRC` | probe width/height/frame-rate + video codec | `---` |
| `CROP` | effective aspect + crop summary | `---` when no source, `NONE` when known no crop |
| `HLS BUF` | provider HLS overlay | `0 / 0 SEG` |
| `DROPS` | per-sample drop percentage from underrun and blit deltas | `0.0` |
| bitrate | probe or selected-variant bitrate | `---` |
| kHz | modeline horizontal frequency | `---` |
| mode | BT.601 clean aperture for shipped SD modelines, fallback output active width | `---` |
| NTSC/PAL | modeline standard | both dim when unknown |
| ODD/EVEN/LOCK | field order + field rate + interlace flag; visual tick may be client clocked from real cadence | idle lock |
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
- Audio output displays as `S16LE - <rate> - <channels>`, e.g. `S16LE - 48k - STEREO` or `S16LE - 44.1k - MONO`. Channels reflect `bridge.Audio.Channels` (currently configurable; 1 → MONO, 2 → STEREO). The channel suffix exists for symmetry with `AUDIO IN`, which already carries channel info.
- Horizontal kHz displays with one decimal.
- Throughput displays in decimal MB/s using `1000 * 1000` bytes per MB, matching network operator convention.
- ACK displays with one decimal, left-padded to match the v24 feel where useful, for example `04.0`.
- `PIPE` uses ASCII display text in code and JSON, for example `LZ4+D - TFF`.
- `SPEED` is `1.00x LOCK` when measured ratio is within ±2 % of expected field rate; outside that band it renders `<ratio>x SLOW` (ratio < 0.98) or `<ratio>x FAST` (ratio > 1.02). The 2 % band absorbs ordinary jitter at 2 Hz sampling without lamp flicker.
- `DROPS` is a percentage over the latest sampler interval: `100 * deltaUnderruns / max(1, deltaBlits)`. Justification: `Plane.BlitsTotal` increments once per emitted field, including the duplicate field the underrun→sendDuplicate path emits when a deadline is missed ([internal/dataplane/plane.go](../../../internal/dataplane/plane.go), `advancePosition` is called after BLIT or BLIT-dup). `Plane.Underruns` increments once per missed deadline. So `dU ⊆ dB` and the fraction of emitted fields that were forced duplicates is `dU / dB`. The first sample after a generation reset displays `0.0` because there is no previous sample. Counter regressions reset the sampler. Paused samples do not append a new drop value.
- `CROP` and `ASPECT` formatting must use display aspect ratio when present. Pixel dimensions alone are only a fallback. Anamorphic sources must not be silently labeled by storage dimensions.

Field-flip behavior:

- `interlacedOutput == true`: the client may clock ODD/EVEN lamps from `fieldRateHz`, preserving `fieldOrder` for labels and phase.
- `interlacedOutput == false`: ODD/EVEN lamps stay dim and the lock text renders `PROG` or the implementation's equivalent non-interlace state.
- Idle: ODD/EVEN lamps stay dim and lock text renders the idle placeholder.
- Reduced motion: no ticking animation; render the latest static field state/lock text only.
- Lamps are a **best-effort visualization**, not a literal per-field truth source. Server samples arrive at 2 Hz; the client interpolates a 60 Hz visual cadence between them. Pause/resume, generation flip, or `fieldRateHz` change will visibly resync lamp phase. The label text (`TFF`/`BFF`/`PROG`/`LOCK`) remains authoritative; the lamp ticking is purely cosmetic.

## SSE Behavior

`GET /receiver/events` keeps the existing stream and adds `meter`.

Initial burst order (mirrors the existing `handleEvents` in events.go; `source` is already shipping and must not be dropped):

1. `state`
2. `vfd`
3. `source`
4. `visualizer`
5. `transport`
6. `meter`

Live cadence:

- The server snapshot refresher can keep its existing 250 ms cadence.
- The meter **sampler** appends one sample every 500 ms (every other refresher tick) while live, so throughput/ACK histories grow at 2 Hz regardless of emit gating.
- The SSE **emitter** emits a `meter` event when ANY of the following are true on a given tick:
  1. the sampler appended a new sample (i.e. a 500 ms boundary was crossed),
  2. `state` transitioned (idle ↔ live, or live → live with `paused` flipped),
  3. `generation` changed, or
  4. any structural meter field changed: `standard`, `fieldOrder`, `interlacedOutput`, `output`, `aspect`, `pipe`, `link` text (these are rare and operator-meaningful — a 500 ms delay would feel laggy).
- Pure text-only jitter inside the 500 ms window (e.g. `LastACKAge` ticking from 4 ms to 5 ms without a new sample boundary) does NOT emit.
- Idle transitions emit one final idle `meter` event so clients clear histories.
- Heartbeats remain unchanged.

`meterChanged` implements rules 2–4 above. The sampler-driven rule 1 is the primary cadence; the others are convergence guarantees.

## Client Behavior

Add `internal/chassis/static/meter.js` after `transport.js` and `visualizer-bank.js` in `shell.html`, or before them if implementation needs meter hooks initialized earlier.

Spec 5A also adds a small shared event helper to `internal/chassis/static/vfd-live.js`:

```js
window.Chassis.events.subscribe(eventName, handler)
```

The helper attaches `handler` to the current `/receiver/events` `EventSource` when one exists, reattaches it after the existing `chassis:eventsource` reconnect event, and returns an unsubscribe function. It must not open a second `EventSource`, must not attach duplicate listeners after reconnect, and unsubscribe must remove the handler from both the current source and future reconnect bookkeeping. Existing transport and visualizer scripts can keep their current `window.Chassis.events.source` / `chassis:eventsource` pattern in 5A; `meter.js` should use `subscribe` so new event consumers have one path.

Follow-up debt (not part of 5A scope): migrate `transport.js` and `visualizer-bank.js` off the raw `window.Chassis.events.source` + `chassis:eventsource` listener pattern and onto `subscribe()`. Tracked here so 5A doesn't ship and forget; addressing it in 5B or a small follow-up keeps the two-pattern coexistence from calcifying.

Client responsibilities:

- Update only explicit `data-meter-*` hooks. Do not rely on `nth-child` selectors.
- Update HLS buffer lamps from `hlsCachedSegments` and `hlsMaxSegments`.
- Draw throughput and ACK canvases from incoming samples or histories.
- Reset histories on idle state or generation change.
- Freeze histories when `paused == true`; resume appending when live samples continue for the same generation.
- Respect `prefers-reduced-motion`: draw latest/static snapshots without animated trails.
- Keep audio scopes quiet while `audioScopes.status == "pending"`.
- Avoid opening any additional EventSource connection.

Template work:

- Add `data-meter-*` hooks throughout `internal/chassis/templates/meter.html`, including explicit hooks for `AUDIO IN`, `AUDIO OUT`, `SRC`, `CROP`, `HLS BUF`, `DROPS`, every middle-row stat, and every readout field.
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
- Probe parser captures codec names, channels, sample/display aspect ratio, and bitrate fields.
- Aspect formatting tests cover 4:3, 16:9, anamorphic, cropped, and no-crop inputs.

Layer 1, adapters:

- URL HLS-buffered cast exposes HLS stats only while its adapter ref + generation owns the active session.
- Streams HLS-buffered cast exposes the same overlay.
- URL and Streams install HLS stats handles only after a successful core start and a matching `core.Status()` read.
- Streams overlay tests prove core session generation is used, not queue generation.
- URL and Streams capture `MaxCachedSegments` from the normalized session-open config and keep using that captured value after bridge config hot-swap.
- HLS overlay current occupancy tests distinguish current cached segments/bytes/duration from lifetime segment-download totals, including after cache eviction.
- Overlay clears on stop/preempt before adapter `OnStop` state work and before HLS session close.
- Overlay conversion never leaks raw source URLs, variant URIs, codecs strings, raw failure reasons, or tokens. Include fixtures where `SelectedVariant.URI`, `Variant.Codecs`, and `FailureReason` contain relative signed URLs and secrets.

Layer 1, chassis:

- `MeterData` mapping renders idle placeholders exactly.
- Idle placeholder tests assert both NTSC and PAL standard lamps are dim; the old idle default that lit NTSC must not survive into real telemetry.
- Live mapping formats audio-in, source, output, crop/aspect, pipe, speed, link, HLS, drops, and standard lamps.
- `meter` payload contract tests assert `audioIn`, `dropsPercent`, raw underrun/blit counters, `fieldRateHz`, `interlacedOutput`, and `paused` from a deterministic `StatusHomeView` plus HLS overlay.
- Meter sampler computes throughput from `WireBytes` deltas and resets on generation change.
- Meter sampler computes speed from blit deltas and expected field rate.
- Meter sampler computes `DROPS` with the exact per-sample formula and resets on generation change or counter regression.
- Paused snapshots freeze histories and mark `paused: true` without adding synthetic zero samples.
- Overlay panic recovery tests prove one panicking provider logs once per generation/provider, base meter fields still render, and other providers can still contribute overlays.
- Initial SSE burst includes `meter` after `transport`.
- Initial SSE burst reads the cached `ReceiverPageData.Meter` snapshot rather than recomputing a per-connection meter snapshot.
- Live SSE emits `meter` at the expected 2 Hz cadence, not every 250 ms tick.
- `meterChanged` / emitter-gating table tests cover: sample-boundary emit, immediate pause flip emit, immediate generation emit, immediate structural-field emit, idle clear emit, and suppression of ACK/text jitter before the next sample boundary.
- Multiple SSE clients do not multiply `StatusHomeView()` calls.

Static asset tests:

- `vfd-live.js` exposes `window.Chassis.events.subscribe`.
- `meter.js` subscribes through `window.Chassis.events.subscribe`.
- `meter.js` does not create `new EventSource`.
- `window.Chassis.events.subscribe` tests prove unsubscribe removes the handler from the current `EventSource`, prevents future reconnect reattachment, and reconnects do not double-deliver a handler.
- `meter.js` uses `data-meter-*` hooks.
- `meter.js` updates `AUDIO IN` from `sourceStrip.audioIn`; it must not remain a static template value during live SSE.
- `meter.js` does not contain pseudo audio generators for spectrum/goniometer/VU/phase/LUFS.
- `meter.js` does not synthesize non-audio meter values with `Math.random`, hard-coded demo samples, or demo timers. Throughput, ACK, HLS, drops, and speed canvases/readouts update only from SSE samples or histories.

Integration:

- A fake plane with deterministic counters drives `/receiver/events`; the `meter` payload shows audio-in, throughput, ACK, drops, pipe, speed, field-rate/interlace fields, and link.
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

## Security

The `meter` event ships to any browser that can reach `/receiver/events`. The same audience already sees title, modeline, and transport state via Phase 0/1 events, so 5A does not change the audience — but the new payload pulls from adapter-owned state (HLS overlay) for the first time, which raises the leak surface. The discipline is:

- Overlay structs carry counts, durations, byte totals, and selected-variant metadata (width / height / bps) only. No source URL, no playlist URL, no segment URL, no variant URI, no codec string, no `Authorization` header, no cookie, no token. `FailureReason` (when populated) is a short adapter-emitted enum/string that cannot contain URL-like substrings.
- The HLS overlay reads stats via `hlsbuffer.Session.Stats`, but that `Stats` struct currently includes URL-bearing data (`SelectedVariant.URI`) and may include raw `FailureReason` text. Spec 5A therefore treats `Stats` as input only: adapters must transform it through the allowlist mapper described above before it reaches chassis or JSON.
- Tests grep the serialized `meter` JSON for `http://`, `https://`, `://`, `/live.m3u8`, `token=`, `sig=`, `secret`, `Authorization`, the source URL fixture, and a deliberately secret-bearing relative `SelectedVariant.URI`; any match fails the test. Apply the same test to both URL and Streams overlays.
- New providers that want to surface adapter-owned data through this overlay MUST be reviewed for the same leak vectors before they ship.

## Observability

A stuck meter — events arriving but values not advancing, or events not arriving at all — is the most likely 5A failure mode in production. Add minimal instrumentation so it can be diagnosed without attaching a debugger:

- `slog.Debug("chassis: meter emit refused", "reason", "<cadence|unchanged|paused>")` at most once per second while ticks are refused, to avoid log spam.
- `slog.Warn("chassis: meter overlay panic", "provider", "<name>", "err", err)` on recovered overlay panic (rule 4 of the call-site contract). Logged at most once per generation per provider.
- An incrementing counter on `Manager.StatusHomeView()` invocations is already implicit via existing logs; no new counter is required. The fan-out test asserts the call rate is bounded by the refresher cadence.
- The chassis `meter` SSE response includes the cached snapshot's `generation` in every emit. A reader watching the stream with `curl -N` can confirm by inspection that generation advances across cast boundaries.

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
