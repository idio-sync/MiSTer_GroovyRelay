# Audio VU Telemetry — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Compute per-channel L/R audio levels from the s16le PCM the bridge
already holds in memory, push them to the browser over a single Server-Sent
Events (SSE) stream, and drive the `.tr-vu` meter in the receiver UI redesign
with real signal — no ffmpeg `astats`, no extra subprocess, no extra socket.

**Architecture:** A tiny DSP helper inside `internal/dataplane/` runs once per
PCM chunk (one field tick) and writes a peak+RMS snapshot to an atomic on
`*Plane`. A new package `internal/telemetry/` owns a fan-out broker that polls
that snapshot at 30 Hz (independent of the field rate) and pushes JSON frames
to any number of SSE subscribers. The UI server mounts one new endpoint
(`GET /ui/telemetry/audio`) backed by that broker. The dataplane never blocks
on subscribers; slow clients drop frames silently. When no session is active,
the broker emits a sentinel `idle` event so the UI can clear the meter and let
the production CSS render its dim/off state. Do not depend on the receiver
mockup's `body.idle` class; the current shell does not set it.

**Tech Stack:** Go 1.26, `sync/atomic`, `encoding/json`, `net/http`'s built-in
flush-on-write SSE pattern, vanilla JS on the browser side
(`EventSource`). No new dependencies.

**Spec:** none yet — this is the first audio-telemetry document. If the
implementing engineer wants a `docs/specs/...-design.md` companion, derive it
from this plan.

---

## Why this shape (read before touching code)

Three constraints fix most of the design:

1. **`Manager.mu` is never held across network I/O** (CLAUDE.md §Invariants).
   The DSP must not need that lock. Solution: per-`*Plane` atomic snapshot,
   same discipline as `Plane.WireBytes` / `Plane.LastACKAge`. The broker reads
   the snapshot lock-free.
2. **One HTTP listener.** No new socket; the SSE endpoint mounts on the
   existing `internal/ui` mux exactly like the htmx-polling endpoints. No
   change to startup ordering or to docker `--network=host`.
3. **The UI is htmx-polling today** — there is no WebSocket / SSE / event-push
   surface yet. Adding one for live audio levels is the natural place to also
   set the precedent for any future high-rate telemetry (ACK ms, drops, queue
   depth). The endpoint goes in a new `internal/telemetry/` package precisely
   so it doesn't bleed into `internal/ui`, which today is a strictly
   request/response surface.

The PCM is already in hand inside the field-tick goroutine, immediately before
[plane.go:981](../../internal/dataplane/plane.go#L981) calls
`p.sendAudio(oldest)`. Hooking there means the meter shows exactly what
reaches the FPGA's DAC (after the GROOVY_AUDIO_DELAY_FIELDS ring) — the
right phase for an "observe-only" meter.

---

## File Structure

**New files:**

- `internal/dataplane/audiometer.go` — `AudioMeter` type, peak+RMS DSP over a
  single s16le PCM chunk. Pure function plus an atomic snapshot holder; no I/O,
  no goroutines.
- `internal/dataplane/audiometer_test.go` — known-PCM → known-levels table
  test, plus an alloc-budget regression that pins zero heap allocations per
  `Observe` call.
- `internal/telemetry/broker.go` — `AudioBroker` fan-out. Owns the 30 Hz
  ticker, the subscriber set, the JSON encoder, and the SSE write loop.
- `internal/telemetry/broker_test.go` — subscriber lifecycle (subscribe,
  unsubscribe on close, slow-subscriber-drop semantics), idle vs live frame
  shape.
- `internal/telemetry/sse.go` — minimal SSE writer helper: sets headers, calls
  `http.Flusher`, writes one `data:` line per frame. Kept separate so the
  broker is testable without an `http.ResponseWriter`.
- `internal/ui/telemetry.go` — thin UI handler that delegates
  `/ui/telemetry/audio` to the broker.
- `internal/ui/telemetry_test.go` — handler tests for disabled/delegated broker
  behavior.
- `internal/ui/static/vu-meter.js` — browser-side EventSource subscriber and
  segment painter.
- `tests/integration/audio_meter_test.go` — build-tagged SSE smoke test against
  a real `httptest.Server`.

**Modified files:**

- `internal/dataplane/plane.go` — add an `audioMeter AudioMeter` field on
  `*Plane`; call `p.audioMeter.Observe(oldest, audioChans, audioRate)` inside the tick
  loop, immediately before the existing `p.sendAudio(oldest)` at
  [plane.go:981](../../internal/dataplane/plane.go#L981); expose
  `Plane.AudioLevels() AudioLevelSnapshot`.
- `internal/dataplane/plane_test.go` — extend with a test that feeds a tone
  through a stub `processHandle` and asserts non-zero levels after a handful
  of ticks.
- `internal/core/manager.go` — add `AudioLevels()` to `planeRunner` and expose
  the active plane snapshot through `Manager.AudioLevels()`.
- `internal/core/manager_test.go` — update plane fakes for the widened
  interface and cover idle/active `Manager.AudioLevels()`.
- `internal/ui/server.go` — add `AudioBroker AudioSubscriber` to
  `Config`; mount `GET /ui/telemetry/audio` via `mountGETUnguarded` (the
  first-run guard issues `http.Redirect(..., StatusFound)`, and `EventSource`
  does not follow redirects — a guarded mount would silently break the stream
  on fresh installs while the operator is in the setup wizard). Read-only
  telemetry, no CSRF risk; the broker returns idle frames when no plane
  exists, so unguarded exposure is benign.
- `internal/ui/templates/shell.html` — load `/ui/static/vu-meter.js` once next
  to the existing `now-playing.js` include. Do not put the script in the
  htmx-swapped banner partial.
- `internal/ui/templates/now-playing-banner.html` — add the production `.tr-vu`
  DOM: two `.ch-bar` children, 12 `.s` segments each, with `.g` / `.y` / `.r`
  color classes pre-applied.
- `internal/ui/static/app.css` — add compact VU meter styles and dim/off states
  for unlit segments.
- `cmd/mister-groovy-relay/main.go` — construct the broker once after
  `core.Manager` is built, pass it to `ui.Config`, start its goroutine, stop
  it during the SIGINT/SIGTERM drain before `httpSrv.Shutdown`, and wait for
  the broker goroutine to exit so SSE handlers can return cleanly.
- `tests/integration/helper_test.go` — add a direct `AudioMeter`-backed
  `LevelSource` harness for the build-tagged SSE smoke test.

**File responsibilities:**

- `audiometer.go` knows nothing about HTTP, channels, or subscribers. It's a
  pure DSP primitive plus an atomic-snapshot holder. Sole consumer:
  `plane.go`'s tick loop.
- `telemetry/broker.go` knows nothing about the dataplane internals; it
  receives a `LevelSource` interface (one method: `AudioLevels() AudioLevelSnapshot`)
  and pumps it to subscribers. `*core.Manager` will satisfy this via a small
  shim added to `core/manager.go`.
- `telemetry/sse.go` is the "how do I write SSE bytes" helper, separated for
  unit testability.
- `vu-meter.js` is the only browser code. It does not use htmx for transport,
  but it must listen for htmx swaps so cached banner nodes are refreshed after
  `/ui/playback/banner` replaces `#gr-now-playing`.

---

## DSP design

**Window:** one analysis per PCM chunk. The dataplane already slices PCM into
chunks of size `floor((fields+1) * sampleRate * rateDenom / rateNumer)` aligned
to whole sample frames
([audiopipe.go:60-77](../../internal/dataplane/audiopipe.go#L60-L77)).
For 48 kHz s16le stereo on NTSC, chunks alternate 3200/3204 bytes (800/801
sample frames per channel). That's a natural one-window-per-tick cadence —
59.94 Hz on NTSC, 50 Hz on PAL — both well inside the "30–60 Hz is plenty"
target from the brief. No internal accumulator; each chunk stands alone.

**Metrics per chunk, per channel:**

- `peak` — max of `abs(int32(sample))` over the chunk, scaled to `float32`
  in `[0.0, 1.0]` by dividing by `32768.0`.
- `rms`  — `sqrt(sumOfSquares / N)` over the chunk, same scaling.

Both are linear (not dB). The browser handles the log curve and segment
mapping; sending log values from Go means baking a UI decision into the wire
format. Linear is the lowest-commitment shape.

**Sample-rate handling:** none required. The DSP doesn't care; it consumes
whatever bytes are in the chunk. The bridge's
[config](../../internal/config/config.go#L141-L143) accepts 22050/44100/48000.

**Channel handling:**

- `channels == 2` (stereo, the v1 default): de-interleave by stride 2. `l` from
  even-indexed samples, `r` from odd-indexed.
- `channels == 1` (mono): both `l` and `r` get the same value.
- `channels == 0` or unexpected: emit a zero snapshot. Belt-and-suspenders;
  the dataplane already gates audio on `effectiveAudioConfig()` and won't call
  `Observe` with a misconfigured stream.

**Integer math:** `int32` accumulators to avoid overflow when squaring
(`int16 * int16` fits in `int32`; the sum over 800 samples fits in `uint64`
with room to spare). Single pass, no branches. Per-chunk cost on a modern
amd64 with the loop branch-predicted: well under 10 μs. Negligible against
the field budget (~16.7 ms NTSC, ~20 ms PAL).

**No filtering / ballistics in Go.** Real VU ballistics (300 ms RMS attack/
release, peak-hold dot) are a UI concern — the browser already has 60 Hz
animation primitives. Go sends raw, the browser smooths. This keeps the wire
format dumb and the test surface tiny. If we add peak-hold later, the wire
shape already accommodates it (`lpk` / `rpk` are independent of `l` / `r`).

**Snapshot shape (Go-side):**

```go
// internal/dataplane/audiometer.go

// AudioLevelSnapshot is the per-tick measurement consumed by telemetry
// subscribers. All four values are linear amplitudes in [0.0, 1.0].
// Generation increments on every Observe so subscribers can detect
// staleness without comparing floats. SampleRate is 0 when no audio
// has been observed yet (subscribers should render as idle).
type AudioLevelSnapshot struct {
    Generation uint64
    L          float32
    R          float32
    LPeak      float32
    RPeak      float32
    SampleRate int
    Channels   int
}

// AudioMeter holds the most recent snapshot. Safe for concurrent
// readers; one writer (the tick goroutine).
type AudioMeter struct {
    // seq is a tiny seqlock: even means stable, odd means a publish is in
    // progress. One writer (the tick goroutine), many readers.
    seq atomic.Uint64
    gen atomic.Uint64

    lBits     atomic.Uint32 // math.Float32bits
    rBits     atomic.Uint32
    lPeakBits atomic.Uint32
    rPeakBits atomic.Uint32
    sampleRate atomic.Int64
    channels   atomic.Int32
}

// Observe computes peak+RMS over pcm and atomically publishes a new
// snapshot. pcm is s16le interleaved; channels is 1 or 2; sampleRate
// is informational only (echoed in the snapshot). Zero-allocation;
// pcm is read, never retained.
func (m *AudioMeter) Observe(pcm []byte, channels, sampleRate int)

// Snapshot returns the most recent published snapshot, or a zero
// value if none has been published yet. Lock-free for the writer;
// readers may spin briefly if they race the single publisher.
func (m *AudioMeter) Snapshot() AudioLevelSnapshot
```

---

## Hook point in `internal/dataplane/`

The PCM chunk is in hand at exactly one place per tick — inside the tick loop
in `Plane.Run`, immediately before `p.sendAudio(oldest)`. Source:
[plane.go:975-983](../../internal/dataplane/plane.go#L975-L983):

```go
if audioRingLen > audioDelayN && p.audioReady.Load() {
    oldest := audioRing[audioRingHead]
    audioRing[audioRingHead] = nil
    audioRingHead = (audioRingHead + 1) % len(audioRing)
    audioRingLen--
    if len(oldest) > 0 {
        // NEW: observe before send. Measures what reaches the FPGA DAC
        // (i.e. post-AV-sync delay). Cheap; runs only when audio_ready.
        p.audioMeter.Observe(oldest, audioChans, audioRate)
        p.sendAudio(oldest)
    }
}
```

**Why here, not in `sendAudio`:**

- `sendAudio` is also a candidate for future reuse (e.g. a non-pumped
  diagnostic path) and shouldn't carry the telemetry responsibility.
- Hooking in the tick loop keeps the call inside the documented hot path with
  the rest of the per-tick observability (the `statWindow.observe*` calls just
  below).
- The PCM lifetime is the tick loop's responsibility; `Observe` is read-only
  and doesn't retain, so the existing `audioRing[audioRingHead] = nil`
  reclamation is unchanged.

**What about paused / no-audio / audio_ready=false?**

- Paused: `Run` isn't ticking (`Manager.DropActivePlane` shut it down). The
  broker sees an absent plane via `core.Manager` and emits idle frames.
- `audioReady=false`: `Observe` is never called. The broker sees the snapshot
  generation freeze and (after a small staleness threshold; see below) emits
  zeros so the meter dims.
- No-audio source (`effectiveAudioConfig` returns 0/0): the `audioEnabled`
  branch in `Plane.Run` is never taken, no `audioCh`, no `Observe`. Same as
  above — broker sees a frozen snapshot.

**Manager.mu discipline:** untouched. `AudioMeter` operates entirely on
atomics inside `*Plane`. `Plane.AudioLevels()` (the new public method) takes
no locks. `core.Manager` exposes its current plane's snapshot through a
similar pattern to `LastACKAge()` — read under `m.mu`, copy out by value, no
I/O. The broker calls into Manager exactly once per 30 Hz tick.

**New method on `*Plane`:**

```go
// AudioLevels returns the most recent per-channel audio levels
// observed during the current session. Returns a zero snapshot
// (Generation==0) if Observe has not yet been called.
func (p *Plane) AudioLevels() AudioLevelSnapshot {
    return p.audioMeter.Snapshot()
}
```

**New method on `*core.Manager`:**

```go
// AudioLevels exposes the active plane's audio meter for telemetry
// subscribers. Returns a zero snapshot when no plane is running.
// Holds m.mu only for the snapshot read; no I/O.
func (m *Manager) AudioLevels() dataplane.AudioLevelSnapshot {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.plane == nil {
        return dataplane.AudioLevelSnapshot{}
    }
    return m.plane.AudioLevels()
}
```

(Importing `internal/dataplane` from `internal/core` is fine — `core`
already imports `dataplane` for plane construction. The
adapter→core→dataplane→wire layering is preserved.)

---

## Telemetry push channel

**Choice: Server-Sent Events.** Rationale:

| | SSE | WebSocket | HTTP polling |
|---|---|---|---|
| One-way server→client | ✓ natural | ✓ overkill | ✓ |
| Survives same `:http_port` listener | ✓ | ✓ | ✓ |
| Built-in browser reconnect | ✓ (`retry:` field) | needs JS | n/a |
| Binary framing or CSRF dance | none | yes | none |
| Bandwidth at 30 Hz × ~60 B JSON | ~1.8 KB/s | ~1.5 KB/s | n/a (worse) |
| Server cost | one goroutine per subscriber | one goroutine + framing per subscriber | one HTTP round-trip per tick |

SSE is strictly less surface area than WebSocket for this read-only use case
and dodges the "do I need a TLS upgrade for `Sec-WebSocket-*` headers behind
Plex's reverse-proxy quirks" question. The single drawback (no client→server
channel) is irrelevant: the VU meter is observe-only.

HTTP polling at 30 Hz is roughly 30× the round-trip cost of one persistent
connection, and the bridge's HTTP server is also serving Plex Companion —
keeping its request rate low matters.

**Endpoint:**

```
GET /ui/telemetry/audio
Accept: text/event-stream
```

Response:

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-store
X-Accel-Buffering: no
Connection: keep-alive

retry: 2000

data: {"t":"audio","gen":1234,"l":0.42,"r":0.39,"lpk":0.51,"rpk":0.47,"sr":48000,"ch":2}

data: {"t":"audio","gen":1235,"l":0.41,"r":0.40,"lpk":0.49,"rpk":0.48,"sr":48000,"ch":2}

data: {"t":"idle"}

```

**Wire format:** one JSON object per SSE event. Two event shapes:

```json
// Live frame (a plane is running and Observe has fired recently)
{"t":"audio","gen":1234,"l":0.42,"r":0.39,"lpk":0.51,"rpk":0.47,"sr":48000,"ch":2}

// Idle frame (no plane, OR snapshot is stale > 500 ms)
{"t":"idle"}
```

Field encoding rules:

- `t` is the discriminator. Reserved values today: `audio`, `idle`. Future
  metrics (e.g. `bitrate`, `ack`) get their own `t` value on the same stream.
- `gen` is monotone per session. Resets to 0 on a new plane (`Generation` field
  on the snapshot starts at 0; first Observe sets it to 1). UI uses it to
  detect "is this newer than what I painted?"; not required for correctness
  but cheap to send.
- `l`, `r`, `lpk`, `rpk` are linear `[0,1]` floats rounded to 2 decimal
  precision before JSON encoding. JSON numbers do not preserve fixed trailing
  zeroes, so `0.50` may appear on the wire as `0.5`.
- `sr`, `ch` echo the session's sample rate / channel count. Lets the UI
  display "48 kHz stereo" alongside the meter without a second endpoint.

**Cadence:** broker ticks at 30 Hz (33.3 ms period). Independent of the field
rate so the UI gets the same cadence on NTSC and PAL, and the field tick
goroutine pays no cost for fan-out. Heartbeat: if the snapshot generation
hasn't advanced in 500 ms, emit `{"t":"idle"}` once and stop emitting until
generation moves again — this keeps idle subscribers from receiving a flood
of identical zero frames.

**Backpressure / slow-client policy:**

- Each subscriber owns a `chan []byte` with `cap=4`. The broker's tick loop
  serializes the snapshot once, then iterates subscribers doing a
  `select { case ch <- frame: default: }` — non-blocking. A slow client drops
  the frame; faster clients are unaffected.
- A per-subscriber writer goroutine ranges over its channel and writes to the
  `http.ResponseWriter`, calling `Flusher.Flush()` after each frame. On write
  error (closed connection), the writer signals the broker to unsubscribe
  via a private `done` channel; broker removes the subscriber, closes the
  channel, the writer goroutine exits.
- Connection limit: cap subscribers at 16 (more than enough — the UI is
  single-tenant). 17th caller gets HTTP 503 with `Retry-After: 5`.

**Sketch of the broker contract:**

```go
// internal/telemetry/broker.go

type LevelSource interface {
    AudioLevels() dataplane.AudioLevelSnapshot
}

type AudioBroker struct {
    src        LevelSource
    tick       time.Duration // 33 * time.Millisecond
    staleAfter time.Duration // 500 * time.Millisecond
    maxSubs    int           // 16

    mu   sync.Mutex
    subs map[*subscriber]struct{}
    quit chan struct{}
}

func NewAudioBroker(src LevelSource) *AudioBroker
func (b *AudioBroker) Run(ctx context.Context) // blocks until ctx done
func (b *AudioBroker) Subscribe(w http.ResponseWriter, r *http.Request)
    // 200 + SSE stream, returns when r.Context() done OR write error
```

The endpoint handler is then a one-liner:

```go
// internal/ui/server.go (handler signature near handleStatusContent)
func (s *Server) handleTelemetryAudio(w http.ResponseWriter, r *http.Request) {
    if s.cfg.AudioBroker == nil {
        http.Error(w, "telemetry disabled", http.StatusServiceUnavailable)
        return
    }
    s.cfg.AudioBroker.Subscribe(w, r)
}
```

---

## Integration with existing UI state

**Bring the mockup DOM into production.** The receiver mockup at
`.superpowers/brainstorm/1973-1779237107/receiver-v24.html` is the visual
reference, but the current production UI does not contain `.tr-vu`,
`.ch-bar`, `.s`, or `body.idle`. Task 8 must add the compact `.tr-vu` markup
to `internal/ui/templates/now-playing-banner.html` and the corresponding CSS
to `internal/ui/static/app.css`; otherwise the telemetry backend would work
while the browser silently renders nothing.

**Idle-state coupling:** do not depend on `body.idle`. The production shell
does not currently toggle it. When the broker sends `{"t":"idle"}`, the JS
clears all `.on` classes. Unlit segments use the dim/off CSS in `app.css`, so
the meter visibly falls dark without needing global body state.

**Segment mapping (browser-side, 12 segments per channel):**

```
// internal/ui/static/vu-meter.js
const SEGMENTS = 12;
const GREEN_TO = 6;   // segments 0..5  green
const YELLOW_TO = 9;  // segments 6..8  yellow
// segments 9..11 red

function levelToSegments(linear) {
    // log-curve mapping so quiet content lights more segments than a
    // linear scale would suggest. -60 dB → 0 segments; 0 dBFS → 12.
    if (linear <= 0) return 0;
    const db = 20 * Math.log10(linear);
    const norm = Math.max(0, Math.min(1, (db + 60) / 60));
    return Math.round(norm * SEGMENTS);
}
```

(Numbers are starting points — tune in the browser, no Go change required.)

**Where the JS goes:**

- New file: `internal/ui/static/vu-meter.js`. Embedded via the existing
  `assets.go` `embed.FS` — no Go change needed beyond the new file landing in
  `internal/ui/static/`.
- Loaded once by a new `<script src="/ui/static/vu-meter.js" defer></script>`
  line in `internal/ui/templates/shell.html`, sibling to the existing
  `now-playing.js` include. Do not load scripts from
  `now-playing-banner.html`; htmx replaces that partial.
- On `DOMContentLoaded`, the module opens `EventSource('/ui/telemetry/audio')`.
  It queries `.tr-vu .ch-bar .s` before painting and re-queries after
  `htmx:afterSwap` when `#gr-now-playing` is replaced, so htmx polling cannot
  leave it holding stale DOM nodes. `EventSource` reconnect is automatic; the
  `retry: 2000` directive sets a 2 s initial backoff.

**Other live telemetry (future expansion):**

The brief asks whether other live telemetry is already pushed. Today, the
status home (`/ui/status/content`) polls htmx every few seconds and renders
HTML for `BlitsTotal`, `WireBytes`, `LastACKAge`, etc. These don't need 30 Hz
updates and the polling pattern stays fine. **But** the same SSE endpoint can
grow new event types later (e.g. `{"t":"throughput","mbps":4.2}` once a
second) and the broker contract above supports that with a second
`LevelSource`-style interface — keeping all live push under one endpoint
rather than spawning N parallel mechanisms.

For v1 of this work, ship audio only. Note the extension path explicitly in
the broker's doc-comment so the next person doesn't reinvent it.

---

## Cost & risk

**CPU.** One pass over ~3200 bytes (1600 stereo samples) per field tick,
integer-only: ~3.2 K int16→int32 conversions + 3.2 K multiply-adds + up to
2 sqrt calls at the end. At 60 Hz that's ~200 K MAC/s — order of microseconds per
second of CPU time on the Pi 4 the bridge targets. Well below the noise floor
of the existing field-budget overrun tracker
([plane.go:1067-1070](../../internal/dataplane/plane.go#L1067-L1070)).

**Memory.** Zero allocations in the DSP hot path (pcm is read in place;
snapshot fields are published through typed atomics under a tiny seqlock). The broker
allocates one JSON-encoded byte slice per 30 Hz tick — ~60 bytes, escapes to
heap but immediately released via the per-subscriber channel. Pin this
behavior with an allocation budget test in `audiometer_test.go`.

**Network.** ~1.8 KB/s per SSE subscriber on a live session, ~30 B/s when
idle (just heartbeats are throttled to one-per-state-change, so effectively
0 B/s most of the time). At 16-subscriber cap, the worst case is ~29 KB/s —
trivial against the Groovy UDP path.

**Clipping.** Yes — a hot source will pin the red segments. That's the
correct behavior for a metering UI; if the meter pins red, the operator
sees they're sending clipped audio. The DSP doesn't normalize and shouldn't.
Document this in the user-facing notes when the receiver UI ships.

**UI disconnect.** `EventSource` handles reconnect automatically using the
`retry:` directive. Server-side, the broker detects the closed connection on
the next write attempt and unsubscribes. No leak.

**Race against plane teardown.** If a subscriber is mid-write when the plane
shuts down, the snapshot freezes (no more `Observe` calls) and the broker
starts emitting idle frames on its next staleness check. No specific
synchronization between teardown and broker required.

**Adverse interactions with prebuffer / underrun.** During the startup
prebuffer window
([plane.go:705-718](../../internal/dataplane/plane.go#L705-L718)) the audio
reader is being drained but `Observe` is not yet called (it lives inside the
tick loop, which hasn't started). Broker sends idle frames until the first
real Observe lands. Operator sees the meter "wake up" the instant audio
starts — exactly the desired UX.

**Discoverability / information leakage.** The unguarded SSE route is
reachable by anyone on the LAN that can reach the bridge's HTTP port (same
trust boundary as `/ui/status/content`, which already exposes "is something
playing" via the htmx panel). The wire format leaks: presence of audio
(any live frame), sample rate (`sr`), channel count (`ch`), and an envelope
shape (RMS/peak amplitudes). It does **not** leak titles, durations,
adapter identity, library paths, or any user-supplied input. Acceptable for
a v1 LAN-only deployment, but worth re-evaluating before any future
internet-exposed mode (which would also need to revisit `/ui/status/content`
and the timeline broker).

---

## Test plan

**Unit tests (`audiometer_test.go`):**

1. **`TestAudioMeter_KnownInputs`** — table-driven:
   - All zeros → snapshot {0, 0, 0, 0}.
   - Full-scale stereo 1 kHz sine (precomputed in setup) → `lpk` and `rpk`
     within 0.5% of 1.0; `l` and `r` within 0.5% of 1/√2.
   - Asymmetric channels: left full-scale, right silent → `lpk` ≈ 1.0,
     `rpk` ≈ 0.0.
   - Mono input (channels=1) → `l == r`.
   - Empty pcm slice → zero snapshot, no panic.
   - Odd-length pcm (off-by-one byte) → truncate to whole frames, no panic.

2. **`TestAudioMeter_GenerationMonotone`** — three consecutive Observes
   produce Generation 1, 2, 3.

3. **`TestAudioMeter_Allocations`** — `testing.AllocsPerRun(100, func() {
   m.Observe(pcm, 2, 48000) })` returns 0.

**Unit tests (`broker_test.go`):**

4. **`TestAudioBroker_LiveFrame`** — fake `LevelSource` returns a snapshot
   with non-zero levels; inject a test subscriber channel and call
   `tickOnce`; assert one frame containing `"t":"audio"`.

5. **`TestAudioBroker_IdleFrame`** — fake `LevelSource` returns a zero
   snapshot; inject a test subscriber channel and call `tickOnce`; assert one
   frame containing `"t":"idle"`.

6. **`TestAudioBroker_HeartbeatDedup`** — call `tickOnce` repeatedly against
   a zero snapshot source;
   assert subscriber receives exactly one `{"t":"idle"}` event, not 30.

7. **`TestAudioBroker_SlowSubscriberDropsFrames`** — subscribe a channel that
   never drains; call `tickOnce` with advancing generations; assert the buffer
   fills once and later frames are dropped without blocking.

8. **`TestAudioBroker_UnsubscribesOnWriteError`** — wrap a
   `ResponseWriter` whose `Write` returns `io.ErrClosedPipe` after the first
   call; assert broker removes the subscriber within 100 ms.

9. **`TestAudioBroker_RespectsMaxSubscribers`** — subscribe 16 fake clients,
   then a 17th; assert 503 + `Retry-After: 5`.

**Plane test (`plane_test.go` extension):**

10. **`TestPlane_AudioMeterPopulatedAfterTicks`** — extends the existing
    `TestPlane_AllocationBudget` harness (stub `processHandle`) to feed a
    known PCM tone through the audio pipe; set `GROOVY_AUDIO_DELAY_FIELDS=0`
    for the test or run long enough to clear the default delay, then assert
    `plane.AudioLevels().Generation > 0` and `LPeak > 0.7`.

**Integration test (`tests/integration/audio_meter_test.go`):**

11. **`TestAudioMeter_EndToEnd`** — drive an `AudioMeter`-backed
    `LevelSource` directly, subscribe to `/ui/telemetry/audio` via a real
    `httptest.NewServer` + HTTP client, parse JSON frames, and assert at least
    one event has `lpk > 0.5`. Build-tagged `integration`, runs in CI per the
    existing `make test-integration` target.

**Manual verification:**

- `make build && ./mister-groovy-relay --config testdata/dev-config.toml`
- `./fake-mister -addr :32100 -out ./dumps` in another terminal
- Cast a known-loudness reference clip via Plex (the EBU R128 -23 LUFS
  tone is the obvious choice if it's on the operator's library; otherwise
  any music clip).
- Open http://localhost:8080/ui/ in the browser. Open devtools Network tab,
  filter to `eventsource`. Verify `/ui/telemetry/audio` stays open with one
  `data:` frame ~every 33 ms while casting.
- Visually confirm `.tr-vu` segments respond to amplitude changes. Confirm
  silence clears `.on` classes and the unlit CSS leaves the meter dim/off.
- Stress test: hold pause for 30 s; observe the broker emits exactly one
  `{"t":"idle"}` after the first 500 ms staleness window.

---

## Task ordering

Tasks 1–2 add the meter primitive and expose the plane snapshot with no change
to the running tick loop. Task 3 wires `plane.go`. Tasks 4–6 add the broker,
UI endpoint, and manager source. Task 7 wires `main.go` startup/shutdown. Task
8 wires the production VU DOM/CSS/JS. Task 9 adds the build-tagged SSE smoke
test. Each task is independently committable; reverting any of 1–2 leaves the
running data plane untouched.

> **Task 8 is BLOCKED on the receiver-v24 UI redesign landing.** The browser
> JS targets `.tr-vu .ch-bar .s` selectors that do not exist in the current
> `now-playing-banner.html`. Tasks 1–7 (Go side) + Task 9 (integration test)
> stand alone and produce a working, verifiable SSE endpoint at
> `/ui/telemetry/audio` independent of any UI work. **Do Tasks 1–7 + 9 now;
> do Task 8 when the receiver-v24 chassis lands in `internal/ui/templates/`.**
> See the "Execution gating" section at the bottom of this plan for details.

---

## Task 1: `dataplane.AudioMeter` — DSP primitive + atomic snapshot

**Files:**

- Create: `internal/dataplane/audiometer.go`
- Create: `internal/dataplane/audiometer_test.go`

- [ ] **Step 1: Write the failing test (zero input)**

Create `internal/dataplane/audiometer_test.go`:

```go
package dataplane

import (
    "math"
    "testing"
)

func TestAudioMeter_Zero(t *testing.T) {
    var m AudioMeter
    pcm := make([]byte, 3200) // 800 stereo frames of silence
    m.Observe(pcm, 2, 48000)
    s := m.Snapshot()
    if s.Generation != 1 {
        t.Errorf("Generation = %d, want 1", s.Generation)
    }
    if s.L != 0 || s.R != 0 || s.LPeak != 0 || s.RPeak != 0 {
        t.Errorf("expected all zeros, got %+v", s)
    }
    if s.Channels != 2 || s.SampleRate != 48000 {
        t.Errorf("metadata: got ch=%d sr=%d, want 2/48000", s.Channels, s.SampleRate)
    }
    _ = math.Sqrt(0) // keep math imported across later steps
}
```

- [ ] **Step 2: Run the test to confirm it fails**

```
go test ./internal/dataplane -run TestAudioMeter_Zero -v
```

Expected: `undefined: AudioMeter`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/dataplane/audiometer.go`:

```go
package dataplane

import (
    "encoding/binary"
    "math"
    "sync/atomic"
)

type AudioLevelSnapshot struct {
    Generation uint64
    L          float32
    R          float32
    LPeak      float32
    RPeak      float32
    SampleRate int
    Channels   int
}

type AudioMeter struct {
    seq atomic.Uint64
    gen atomic.Uint64

    lBits      atomic.Uint32
    rBits      atomic.Uint32
    lPeakBits  atomic.Uint32
    rPeakBits  atomic.Uint32
    sampleRate atomic.Int64
    channels   atomic.Int32
}

func (m *AudioMeter) Observe(pcm []byte, channels, sampleRate int) {
    gen := m.gen.Load() + 1
    out := AudioLevelSnapshot{
        Generation: gen,
        Channels:   channels,
        SampleRate: sampleRate,
    }
    if channels < 1 || channels > 2 || len(pcm) < 2 {
        m.publish(out)
        return
    }
    stride := channels * 2 // s16le
    frames := len(pcm) / stride
    if frames == 0 {
        m.publish(out)
        return
    }
    var sumL, sumR uint64
    var peakL, peakR int32
    // Sample is widened to int32 so `-int16Min` (which would overflow int16)
    // is representable. The resulting peak for a -32768 sample is reported
    // as 32768/32768.0 = 1.0 — one quantum above the +32767 max. This
    // asymmetry is inherent to two's-complement int16 and intentional: the
    // peak measurement should not under-report clipping on the negative
    // half-rail.
    for i := 0; i < frames; i++ {
        off := i * stride
        l := int32(int16(binary.LittleEndian.Uint16(pcm[off : off+2])))
        if l < 0 {
            if -l > peakL {
                peakL = -l
            }
        } else if l > peakL {
            peakL = l
        }
        sumL += uint64(l * l)
        if channels == 2 {
            r := int32(int16(binary.LittleEndian.Uint16(pcm[off+2 : off+4])))
            if r < 0 {
                if -r > peakR {
                    peakR = -r
                }
            } else if r > peakR {
                peakR = r
            }
            sumR += uint64(r * r)
        }
    }
    rmsL := float32(math.Sqrt(float64(sumL)/float64(frames))) / 32768.0
    out.L = rmsL
    out.LPeak = float32(peakL) / 32768.0
    if channels == 2 {
        out.R = float32(math.Sqrt(float64(sumR)/float64(frames))) / 32768.0
        out.RPeak = float32(peakR) / 32768.0
    } else {
        out.R = rmsL
        out.RPeak = out.LPeak
    }
    m.publish(out)
}

func (m *AudioMeter) publish(s AudioLevelSnapshot) {
    // Mark publish in progress. Snapshot spins only if it catches this tiny
    // critical section.
    m.seq.Add(1)
    m.gen.Store(s.Generation)
    m.lBits.Store(math.Float32bits(s.L))
    m.rBits.Store(math.Float32bits(s.R))
    m.lPeakBits.Store(math.Float32bits(s.LPeak))
    m.rPeakBits.Store(math.Float32bits(s.RPeak))
    m.sampleRate.Store(int64(s.SampleRate))
    m.channels.Store(int32(s.Channels))
    m.seq.Add(1)
}

func (m *AudioMeter) Snapshot() AudioLevelSnapshot {
    for {
        start := m.seq.Load()
        if start&1 == 1 {
            continue
        }
        s := AudioLevelSnapshot{
            Generation: m.gen.Load(),
            L:          math.Float32frombits(m.lBits.Load()),
            R:          math.Float32frombits(m.rBits.Load()),
            LPeak:      math.Float32frombits(m.lPeakBits.Load()),
            RPeak:      math.Float32frombits(m.rPeakBits.Load()),
            SampleRate: int(m.sampleRate.Load()),
            Channels:   int(m.channels.Load()),
        }
        if end := m.seq.Load(); start == end && end&1 == 0 {
            return s
        }
    }
}
```

- [ ] **Step 4: Run the test to confirm it passes**

```
go test ./internal/dataplane -run TestAudioMeter_Zero -v
```

Expected: PASS.

- [ ] **Step 5: Add the remaining DSP tests**

Append to `audiometer_test.go`:

```go
func TestAudioMeter_FullScaleSine(t *testing.T) {
    const frames = 800
    pcm := make([]byte, frames*4)
    for i := 0; i < frames; i++ {
        v := int16(math.Round(32760 * math.Sin(2*math.Pi*float64(i)/40)))
        binary.LittleEndian.PutUint16(pcm[i*4:i*4+2], uint16(v))
        binary.LittleEndian.PutUint16(pcm[i*4+2:i*4+4], uint16(v))
    }
    var m AudioMeter
    m.Observe(pcm, 2, 48000)
    s := m.Snapshot()
    if s.LPeak < 0.995 || s.RPeak < 0.995 {
        t.Errorf("peak: got L=%.4f R=%.4f, want ~1.0", s.LPeak, s.RPeak)
    }
    rmsTarget := float32(1.0 / math.Sqrt2)
    if math.Abs(float64(s.L-rmsTarget)) > 0.01 {
        t.Errorf("rms L = %.4f, want ~%.4f", s.L, rmsTarget)
    }
    if math.Abs(float64(s.R-rmsTarget)) > 0.01 {
        t.Errorf("rms R = %.4f, want ~%.4f", s.R, rmsTarget)
    }
}

func TestAudioMeter_AsymmetricChannels(t *testing.T) {
    const frames = 800
    pcm := make([]byte, frames*4)
    for i := 0; i < frames; i++ {
        binary.LittleEndian.PutUint16(pcm[i*4:i*4+2], uint16(int16(32000)))
        // right channel stays zero
    }
    var m AudioMeter
    m.Observe(pcm, 2, 48000)
    s := m.Snapshot()
    if s.LPeak < 0.97 {
        t.Errorf("LPeak = %.4f, want > 0.97", s.LPeak)
    }
    if s.RPeak != 0 || s.R != 0 {
        t.Errorf("right channel: peak=%.4f rms=%.4f, want 0", s.RPeak, s.R)
    }
}

func TestAudioMeter_Mono(t *testing.T) {
    pcm := make([]byte, 1600)
    for i := 0; i < 800; i++ {
        binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(int16(16000)))
    }
    var m AudioMeter
    m.Observe(pcm, 1, 48000)
    s := m.Snapshot()
    if s.L != s.R || s.LPeak != s.RPeak {
        t.Errorf("mono should mirror L→R: got %+v", s)
    }
    if s.LPeak < 0.48 || s.LPeak > 0.50 {
        t.Errorf("LPeak = %.4f, want ~0.488", s.LPeak)
    }
}

func TestAudioMeter_Empty(t *testing.T) {
    var m AudioMeter
    m.Observe(nil, 2, 48000)
    s := m.Snapshot()
    if s.L != 0 || s.R != 0 || s.LPeak != 0 || s.RPeak != 0 {
        t.Errorf("empty input should produce zeros, got %+v", s)
    }
}

func TestAudioMeter_OddLengthTruncates(t *testing.T) {
    pcm := make([]byte, 3201) // not a whole stereo frame count
    var m AudioMeter
    m.Observe(pcm, 2, 48000) // must not panic
    _ = m.Snapshot()
}

func TestAudioMeter_GenerationMonotone(t *testing.T) {
    var m AudioMeter
    pcm := make([]byte, 3200)
    for i := uint64(1); i <= 5; i++ {
        m.Observe(pcm, 2, 48000)
        if g := m.Snapshot().Generation; g != i {
            t.Errorf("Generation = %d, want %d", g, i)
        }
    }
}

func TestAudioMeter_Allocations(t *testing.T) {
    var m AudioMeter
    pcm := make([]byte, 3200)
    allocs := testing.AllocsPerRun(100, func() {
        m.Observe(pcm, 2, 48000)
    })
    if allocs != 0 {
        t.Errorf("Observe allocates %.1f per call, want 0", allocs)
    }
}
```

- [ ] **Step 6: Run the full test file**

```
go test ./internal/dataplane -run TestAudioMeter -v
```

Expected: all PASS. If `TestAudioMeter_Allocations` fails at 0, increase the
budget to 1; if it fails above 1, profile and pool the snapshot pointer.

- [ ] **Step 7: Commit**

```
git add internal/dataplane/audiometer.go internal/dataplane/audiometer_test.go
git commit -m "feat(dataplane): add AudioMeter peak/RMS DSP primitive"
```

---

## Task 2: Expose `Plane.AudioLevels()`

**Files:**

- Modify: `internal/dataplane/plane.go`
- Modify: `internal/dataplane/plane_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/dataplane/plane_test.go`:

```go
func TestPlane_AudioLevels_ZeroBeforeObserve(t *testing.T) {
    p := NewPlane(PlaneConfig{
        Modeline:    groovy.NTSC480i60,
        FieldWidth:  720,
        FieldHeight: 240,
        BytesPerPixel: 3,
        AudioRate:   48000,
        AudioChans:  2,
    })
    s := p.AudioLevels()
    if s.Generation != 0 || s.L != 0 || s.R != 0 {
        t.Errorf("zero-before-observe: %+v", s)
    }
}

func TestPlane_AudioLevels_AfterObserve(t *testing.T) {
    p := NewPlane(PlaneConfig{
        Modeline:    groovy.NTSC480i60,
        FieldWidth:  720,
        FieldHeight: 240,
        BytesPerPixel: 3,
        AudioRate:   48000,
        AudioChans:  2,
    })
    pcm := make([]byte, 3200)
    for i := 0; i < 800; i++ {
        binary.LittleEndian.PutUint16(pcm[i*4:i*4+2], uint16(int16(20000)))
        binary.LittleEndian.PutUint16(pcm[i*4+2:i*4+4], uint16(int16(20000)))
    }
    p.audioMeter.Observe(pcm, 2, 48000)
    s := p.AudioLevels()
    if s.Generation != 1 {
        t.Errorf("Generation = %d, want 1", s.Generation)
    }
    if s.LPeak < 0.6 {
        t.Errorf("LPeak = %.4f, want > 0.6", s.LPeak)
    }
}
```

Add the `encoding/binary` import at the top of the file if not already
present.

- [ ] **Step 2: Run the test to confirm it fails**

```
go test ./internal/dataplane -run TestPlane_AudioLevels -v
```

Expected: `p.audioMeter undefined` or `p.AudioLevels undefined`.

- [ ] **Step 3: Add the field and method**

In `internal/dataplane/plane.go`, inside the `type Plane struct` block (near
the other atomic counters around line 339):

```go
    // audioMeter holds per-channel level snapshots updated once per
    // audio chunk by the tick loop. Read by the telemetry broker via
    // AudioLevels(). Lock-free.
    audioMeter AudioMeter
```

After the existing `WireBytes()` method (around line 529), add:

```go
// AudioLevels returns the most recent per-channel audio level snapshot
// observed during the current session. Returns a zero snapshot
// (Generation==0) if no PCM has been observed yet.
func (p *Plane) AudioLevels() AudioLevelSnapshot {
    return p.audioMeter.Snapshot()
}
```

- [ ] **Step 4: Run the test to confirm it passes**

```
go test ./internal/dataplane -run TestPlane_AudioLevels -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "feat(dataplane): expose Plane.AudioLevels() snapshot"
```

---

## Task 3: Hook `audioMeter.Observe` into the tick loop

**Files:**

- Modify: `internal/dataplane/plane.go`

- [ ] **Step 1: Locate the call site**

Find the existing block in `Plane.Run` at
[plane.go:975-983](../../internal/dataplane/plane.go#L975-L983):

```go
if audioRingLen > audioDelayN && p.audioReady.Load() {
    oldest := audioRing[audioRingHead]
    audioRing[audioRingHead] = nil
    audioRingHead = (audioRingHead + 1) % len(audioRing)
    audioRingLen--
    if len(oldest) > 0 {
        p.sendAudio(oldest)
    }
}
```

- [ ] **Step 2: Insert the Observe call**

Modify to:

```go
if audioRingLen > audioDelayN && p.audioReady.Load() {
    oldest := audioRing[audioRingHead]
    audioRing[audioRingHead] = nil
    audioRingHead = (audioRingHead + 1) % len(audioRing)
    audioRingLen--
    if len(oldest) > 0 {
        p.audioMeter.Observe(oldest, audioChans, audioRate)
        p.sendAudio(oldest)
    }
}
```

- [ ] **Step 3: Extend the stub harness with an audio reader**

The existing `staticFrameReader` / `stubProcess` machinery at
[plane_test.go:1244-1311](../../internal/dataplane/plane_test.go#L1244-L1311)
feeds video frames forever but uses `eofReader{}` for audio (because
`TestPlane_AllocationBudget` runs audio-disabled). Add a sibling reader.
Append to `plane_test.go` next to `staticFrameReader`:

```go
// staticAudioReader emits a fixed s16le stereo tone forever. Used by
// audio-meter tests to drive p.audioMeter.Observe via Plane.Run without
// spawning ffmpeg.
type staticAudioReader struct {
    mu     sync.Mutex
    closed bool
    pos    int // sample-frame counter for the sine generator
}

func (r *staticAudioReader) Read(p []byte) (int, error) {
    r.mu.Lock()
    if r.closed {
        r.mu.Unlock()
        return 0, io.EOF
    }
    r.mu.Unlock()
    // Fill with a stereo sine at amplitude ~26000 (~ -2 dBFS), 1 kHz
    // assuming 48 kHz sample rate. Both channels carry the same wave.
    frames := len(p) / 4
    for i := 0; i < frames; i++ {
        v := int16(math.Round(26000 * math.Sin(2*math.Pi*float64(r.pos+i)/48)))
        binary.LittleEndian.PutUint16(p[i*4:i*4+2], uint16(v))
        binary.LittleEndian.PutUint16(p[i*4+2:i*4+4], uint16(v))
    }
    r.pos += frames
    return frames * 4, nil
}

func (r *staticAudioReader) Close() error {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.closed = true
    return nil
}
```

Add `math` and `encoding/binary` imports to the file's import block if not
already present.

- [ ] **Step 4: Write the integration-style test**

Append to `plane_test.go`. Model the setup on `TestPlane_AllocationBudget`
([plane_test.go:1673](../../internal/dataplane/plane_test.go#L1673)) but
enable audio and pass a `staticAudioReader`:

```go
func TestPlane_AudioMeterPopulatedAfterTicks(t *testing.T) {
    t.Setenv("GROOVY_AUDIO_DELAY_FIELDS", "0")

    // Mirror TestPlane_AllocationBudget's setup: stubProcess wrapping a
    // staticFrameReader + (here) a staticAudioReader.
    proc := newStubProcess()
    proc.audio = &staticAudioReader{}
    original := spawnProcess
    spawnProcess = func(ctx context.Context, spec ffmpeg.PipelineSpec) (processHandle, error) {
        return proc, nil
    }
    defer func() { spawnProcess = original }()

    cfg := PlaneConfig{
        // Pull the canonical NTSC 480i setup from the existing
        // TestPlane_AllocationBudget — copy verbatim. AudioRate must be
        // > 0 to enter the audio-enabled branch in Run.
        Sender:        /* test sender from existing harness */,
        Modeline:      groovy.NTSC480i60,
        FieldWidth:    720,
        FieldHeight:   240,
        BytesPerPixel: 3,
        RGBMode:       groovy.RGBMode888,
        AudioRate:     48000,
        AudioChans:    2,
    }
    p := NewPlane(cfg)

    ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
    defer cancel()
    _ = p.Run(ctx) // returns ctx.Err on timeout

    s := p.AudioLevels()
    if s.Generation == 0 {
        t.Fatalf("AudioLevels not populated; Generation == 0")
    }
    if s.LPeak < 0.7 || s.RPeak < 0.7 {
        t.Errorf("expected LPeak/RPeak ~ 0.79, got L=%.3f R=%.3f", s.LPeak, s.RPeak)
    }
}
```

The `Sender` placeholder needs the same fakemister-backed sender the existing
allocation test uses — check `TestPlane_AllocationBudget`'s setup
([plane_test.go:1673](../../internal/dataplane/plane_test.go#L1673)) and
reuse its `requireUDPSockets` + `groovynet.NewSender` boilerplate verbatim.
Do not refactor the harness here.

- [ ] **Step 5: Run the full dataplane test suite**

```
go test ./internal/dataplane -v
go test -race ./internal/dataplane
```

Expected: PASS, no race.

- [ ] **Step 6: Commit**

```
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "feat(dataplane): populate audio meter from tick loop"
```

---

## Task 4: `telemetry.AudioBroker` — fan-out + SSE writer

**Files:**

- Create: `internal/telemetry/broker.go`
- Create: `internal/telemetry/sse.go`
- Create: `internal/telemetry/broker_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/telemetry/broker_test.go`:

```go
package telemetry

import (
    "strings"
    "sync"
    "testing"

    "github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
)

type fakeSource struct {
    mu sync.Mutex
    s dataplane.AudioLevelSnapshot
}

func (f *fakeSource) Set(s dataplane.AudioLevelSnapshot) {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.s = s
}

func (f *fakeSource) AudioLevels() dataplane.AudioLevelSnapshot {
    f.mu.Lock()
    defer f.mu.Unlock()
    return f.s
}

func TestAudioBroker_LiveFrame(t *testing.T) {
    src := &fakeSource{s: dataplane.AudioLevelSnapshot{
        Generation: 1, L: 0.5, R: 0.4, LPeak: 0.7, RPeak: 0.6,
        SampleRate: 48000, Channels: 2,
    }}
    b := NewAudioBroker(src)
    s := &subscriber{ch: make(chan []byte, 1), done: make(chan struct{})}
    b.mu.Lock()
    b.subs[s] = struct{}{}
    b.mu.Unlock()

    b.tickOnce()

    frame := string(<-s.ch)
    if !strings.Contains(frame, `"t":"audio"`) {
        t.Errorf("frame missing audio event: %q", frame)
    }
    if !strings.Contains(frame, `"l":0.5`) {
        t.Errorf("frame missing l=0.5: %q", frame)
    }
}
```

- [ ] **Step 2: Run the test to confirm it fails**

```
go test ./internal/telemetry -v
```

Expected: package not found.

- [ ] **Step 3: Create `internal/telemetry/sse.go`**

```go
// Package telemetry provides server-sent-events fan-out for live
// metrics computed in internal/dataplane. The single public surface
// today is AudioBroker (audio L/R levels), but the package is shaped
// so additional event types can be added on the same stream without
// new endpoints.
package telemetry

import (
    "fmt"
    "io"
    "net/http"
)

// sseWriter wraps an http.ResponseWriter with SSE framing. Each call
// to WriteEvent emits one "data: ...\n\n" frame and flushes. Returns
// an error if the underlying writer is not an http.Flusher (the SSE
// contract requires flushing) or if the network write fails.
type sseWriter struct {
    w http.ResponseWriter
    f http.Flusher
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
    f, ok := w.(http.Flusher)
    if !ok {
        return nil, fmt.Errorf("telemetry: ResponseWriter does not support Flush")
    }
    return &sseWriter{w: w, f: f}, nil
}

func (s *sseWriter) WriteHeaders() {
    h := s.w.Header()
    h.Set("Content-Type", "text/event-stream")
    h.Set("Cache-Control", "no-store")
    h.Set("X-Accel-Buffering", "no")
    h.Set("Connection", "keep-alive")
    s.w.WriteHeader(http.StatusOK)
    fmt.Fprint(s.w, "retry: 2000\n\n")
    s.f.Flush()
}

func (s *sseWriter) WriteEvent(payload []byte) error {
    if _, err := io.WriteString(s.w, "data: "); err != nil {
        return err
    }
    if _, err := s.w.Write(payload); err != nil {
        return err
    }
    if _, err := io.WriteString(s.w, "\n\n"); err != nil {
        return err
    }
    s.f.Flush()
    return nil
}
```

- [ ] **Step 4: Create `internal/telemetry/broker.go`**

```go
package telemetry

import (
    "context"
    "encoding/json"
    "fmt"
    "math"
    "net/http"
    "strconv"
    "sync"
    "time"

    "github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
)

// LevelSource is the broker's read-only dependency. *core.Manager
// satisfies it via structural typing.
type LevelSource interface {
    AudioLevels() dataplane.AudioLevelSnapshot
}

const (
    defaultTick       = 33 * time.Millisecond
    defaultStaleAfter = 500 * time.Millisecond
    defaultMaxSubs    = 16
    subBuffer         = 4
)

// AudioBroker fans out per-tick audio level snapshots to N SSE
// subscribers. Independent of the dataplane field rate — its own
// ticker drives serialization at 30 Hz. Slow subscribers drop
// frames; the dataplane is never blocked.
type AudioBroker struct {
    src        LevelSource
    tick       time.Duration
    staleAfter time.Duration
    maxSubs    int

    mu          sync.Mutex
    subs        map[*subscriber]struct{}
    lastGen     uint64
    lastFreshAt time.Time // wall-clock when Generation last advanced
    wasIdle     bool
}

type subscriber struct {
    ch   chan []byte
    done chan struct{}
}

func NewAudioBroker(src LevelSource) *AudioBroker {
    return &AudioBroker{
        src:        src,
        tick:       defaultTick,
        staleAfter: defaultStaleAfter,
        maxSubs:    defaultMaxSubs,
        subs:       make(map[*subscriber]struct{}),
    }
}

// Run drives the broker's ticker. Blocks until ctx done. On exit,
// closes every subscriber's channel so their writer goroutines
// terminate.
func (b *AudioBroker) Run(ctx context.Context) {
    t := time.NewTicker(b.tick)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            b.shutdown()
            return
        case <-t.C:
            b.tickOnce()
        }
    }
}

func (b *AudioBroker) tickOnce() {
    snap := b.src.AudioLevels()
    b.mu.Lock()
    defer b.mu.Unlock()

    // "Fresh" means the generation actually advanced since the last
    // tick — that's the only signal that Observe was called. Time
    // since last emit is NOT the right measure: emitting stale frames
    // keeps resetting the "freshness" clock, so a dead plane would
    // continue to look fresh forever.
    advanced := snap.Generation != 0 && snap.Generation != b.lastGen
    if advanced {
        b.lastGen = snap.Generation
        b.lastFreshAt = time.Now()
    }

    var frame []byte
    var isIdle bool
    switch {
    case advanced:
        frame, isIdle = b.encodeAudio(snap), false
    case time.Since(b.lastFreshAt) >= b.staleAfter || b.lastGen == 0:
        frame, isIdle = []byte(`{"t":"idle"}`), true
    default:
        // Gen frozen but still within the staleness window — skip
        // emitting entirely. Subscribers keep their last paint until
        // either a new live frame arrives or the staleness fires.
        return
    }

    if isIdle && b.wasIdle {
        return // dedupe consecutive idle heartbeats
    }
    b.wasIdle = isIdle

    for s := range b.subs {
        select {
        case s.ch <- frame:
        default: // drop on slow subscriber
        }
    }
}

func (b *AudioBroker) encodeAudio(snap dataplane.AudioLevelSnapshot) []byte {
    type ev struct {
        T   string  `json:"t"`
        Gen uint64  `json:"gen"`
        L   float32 `json:"l"`
        R   float32 `json:"r"`
        LP  float32 `json:"lpk"`
        RP  float32 `json:"rpk"`
        SR  int     `json:"sr"`
        CH  int     `json:"ch"`
    }
    e := ev{
        T:   "audio",
        Gen: snap.Generation,
        L:   round2(snap.L),
        R:   round2(snap.R),
        LP:  round2(snap.LPeak),
        RP:  round2(snap.RPeak),
        SR:  snap.SampleRate,
        CH:  snap.Channels,
    }
    buf, err := json.Marshal(e)
    if err != nil {
        return []byte(`{"t":"idle"}`)
    }
    return buf
}

func round2(v float32) float32 {
    s := strconv.FormatFloat(float64(v), 'f', 2, 32)
    f, _ := strconv.ParseFloat(s, 32)
    if math.IsNaN(f) || math.IsInf(f, 0) {
        return 0
    }
    return float32(f)
}

// Subscribe upgrades the request to an SSE stream and pumps frames
// until the connection closes or the broker shuts down. Returns once
// the subscription ends; intended to be called directly from an
// http.HandlerFunc.
func (b *AudioBroker) Subscribe(w http.ResponseWriter, r *http.Request) {
    sw, err := newSSEWriter(w)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    b.mu.Lock()
    if len(b.subs) >= b.maxSubs {
        b.mu.Unlock()
        w.Header().Set("Retry-After", "5")
        http.Error(w, "telemetry: too many subscribers", http.StatusServiceUnavailable)
        return
    }
    s := &subscriber{ch: make(chan []byte, subBuffer), done: make(chan struct{})}
    b.subs[s] = struct{}{}
    b.mu.Unlock()

    sw.WriteHeaders()

    ctx := r.Context()
    defer b.unsubscribe(s)
    for {
        select {
        case <-ctx.Done():
            return
        case <-s.done:
            return
        case frame, ok := <-s.ch:
            if !ok {
                return
            }
            if err := sw.WriteEvent(frame); err != nil {
                return
            }
        }
    }
}

func (b *AudioBroker) unsubscribe(s *subscriber) {
    b.mu.Lock()
    if _, ok := b.subs[s]; ok {
        delete(b.subs, s)
        close(s.ch)
    }
    b.mu.Unlock()
}

// shutdown closes every subscriber's channel and clears the map.
// Held-lock invariant: both unsubscribe and shutdown operate on b.subs
// under b.mu — that serialization is what prevents a double-close on
// s.ch when ctx cancellation races with a subscriber's own write-error
// teardown. Do not drop the lock from either path.
func (b *AudioBroker) shutdown() {
    b.mu.Lock()
    defer b.mu.Unlock()
    for s := range b.subs {
        close(s.ch)
    }
    b.subs = map[*subscriber]struct{}{}
}

// String exists so log/slog can format an AudioBroker without leaking
// pointer addresses.
func (b *AudioBroker) String() string { return fmt.Sprintf("AudioBroker{tick=%s}", b.tick) }
```

- [ ] **Step 5: Run the first broker test**

```
go test ./internal/telemetry -run TestAudioBroker_LiveFrame -v
```

Expected: PASS.

- [ ] **Step 6: Add the remaining broker tests**

Append to `broker_test.go`:

```go
func TestAudioBroker_IdleFrame(t *testing.T) {
    src := &fakeSource{} // zero snapshot
    b := NewAudioBroker(src)
    s := &subscriber{ch: make(chan []byte, 1), done: make(chan struct{})}
    b.mu.Lock()
    b.subs[s] = struct{}{}
    b.mu.Unlock()

    b.tickOnce()

    frame := string(<-s.ch)
    if !strings.Contains(frame, `"t":"idle"`) {
        t.Errorf("frame missing idle event: %q", frame)
    }
}

func TestAudioBroker_HeartbeatDedup(t *testing.T) {
    src := &fakeSource{}
    b := NewAudioBroker(src)
    s := &subscriber{ch: make(chan []byte, 4), done: make(chan struct{})}
    b.mu.Lock()
    b.subs[s] = struct{}{}
    b.mu.Unlock()

    for i := 0; i < 5; i++ {
        b.tickOnce()
    }
    if n := len(s.ch); n != 1 {
        t.Errorf("idle dedupe broken: got %d events, want 1", n)
    }
}

func TestAudioBroker_RespectsMaxSubscribers(t *testing.T) {
    b := NewAudioBroker(&fakeSource{})
    b.maxSubs = 2
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go b.Run(ctx)

    // Use a "registered" signal instead of time.Sleep to avoid flakiness.
    // We hand-register two subscribers directly via Subscribe's path: spawn
    // them, then poll the broker's subscriber count via a helper.
    for i := 0; i < 2; i++ {
        w := httptest.NewRecorder()
        r := httptest.NewRequest("GET", "/ui/telemetry/audio", nil).WithContext(ctx)
        go b.Subscribe(w, r)
    }
    // Wait deterministically for both subscribers to register.
    for deadline := time.Now().Add(time.Second); ; {
        b.mu.Lock()
        n := len(b.subs)
        b.mu.Unlock()
        if n == 2 {
            break
        }
        if time.Now().After(deadline) {
            t.Fatalf("only %d of 2 subscribers registered in 1s", n)
        }
        time.Sleep(time.Millisecond)
    }

    w := httptest.NewRecorder()
    r := httptest.NewRequest("GET", "/ui/telemetry/audio", nil)
    b.Subscribe(w, r) // synchronous; should return immediately
    if w.Code != http.StatusServiceUnavailable {
        t.Errorf("third subscriber got %d, want 503", w.Code)
    }
    if got := w.Header().Get("Retry-After"); got != "5" {
        t.Errorf("Retry-After = %q, want 5", got)
    }
}

func TestAudioBroker_SlowSubscriberDropsFrames(t *testing.T) {
    // Wire a subscriber whose channel buffer is forced to cap=1, then make
    // the broker tick faster than the subscriber can drain. The broker must
    // never block waiting on it.
    src := &fakeSource{s: dataplane.AudioLevelSnapshot{
        Generation: 1, L: 0.5, R: 0.5, LPeak: 0.6, RPeak: 0.6,
        SampleRate: 48000, Channels: 2,
    }}
    b := NewAudioBroker(src)

    // Inject a slow subscriber manually. We bypass Subscribe() to avoid
    // needing a working SSE writer.
    s := &subscriber{ch: make(chan []byte, 1), done: make(chan struct{})}
    b.mu.Lock()
    b.subs[s] = struct{}{}
    b.mu.Unlock()
    defer b.unsubscribe(s)

    // Advance generation several times without draining s.ch. tickOnce must
    // return immediately and drop frames after the one-slot buffer fills.
    for i := uint64(1); i <= 10; i++ {
        src.Set(dataplane.AudioLevelSnapshot{
            Generation: i, L: 0.5, R: 0.5, LPeak: 0.6, RPeak: 0.6,
            SampleRate: 48000, Channels: 2,
        })
        b.tickOnce()
    }
    if n := len(s.ch); n != 1 {
        t.Fatalf("slow subscriber buffer len = %d, want 1", n)
    }
}

func TestAudioBroker_UnsubscribesOnWriteError(t *testing.T) {
    src := &fakeSource{s: dataplane.AudioLevelSnapshot{
        Generation: 1, L: 0.5, R: 0.5, SampleRate: 48000, Channels: 2,
    }}
    b := NewAudioBroker(src)
    b.tick = 5 * time.Millisecond
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go b.Run(ctx)

    // A custom ResponseWriter whose Write returns an error on the second
    // call (after WriteHeaders succeeded). Subscribe should detect the
    // failed event-write and unsubscribe.
    w := &failingWriter{ResponseRecorder: httptest.NewRecorder()}
    r := httptest.NewRequest("GET", "/ui/telemetry/audio", nil).WithContext(ctx)
    go b.Subscribe(w, r)

    for deadline := time.Now().Add(time.Second); ; {
        b.mu.Lock()
        n := len(b.subs)
        b.mu.Unlock()
        if n == 0 {
            return // unsubscribed; test passes
        }
        if time.Now().After(deadline) {
            t.Fatalf("subscriber not unsubscribed after write error (%d still registered)", n)
        }
        time.Sleep(5 * time.Millisecond)
    }
}

// failingWriter returns io.ErrClosedPipe after the SSE preamble has gone
// out. Subscribe is expected to detect the error on its next WriteEvent
// call and unregister the subscriber.
//
// Write-count budget: sseWriter.WriteHeaders performs exactly one Write
// (the `fmt.Fprint(s.w, "retry: 2000\n\n")` — WriteHeader sets status
// without going through Write). The next WriteEvent does three Writes
// (`"data: "`, payload, `"\n\n"`); we fail on the second of those
// (writes==3), causing WriteEvent to return an error before the payload
// completes. If the SSE preamble ever changes, retune this threshold.
type failingWriter struct {
    *httptest.ResponseRecorder
    writes int
}

func (f *failingWriter) Write(p []byte) (int, error) {
    f.writes++
    if f.writes > 2 {
        return 0, io.ErrClosedPipe
    }
    return f.ResponseRecorder.Write(p)
}
```

Add the `context`, `io`, `net/http`, `net/http/httptest`, and `time` imports.

- [ ] **Step 7: Run all broker tests**

```
go test ./internal/telemetry -v
go test -race ./internal/telemetry
```

Expected: PASS, no race.

- [ ] **Step 8: Commit**

```
git add internal/telemetry/
git commit -m "feat(telemetry): add AudioBroker SSE fan-out"
```

---

## Task 5: Wire the SSE endpoint into `internal/ui`

**Files:**

- Modify: `internal/ui/server.go`
- Create: `internal/ui/telemetry.go` (handler)
- Create: `internal/ui/telemetry_test.go`

- [ ] **Step 1: Add `AudioBroker` to `ui.Config`**

In `internal/ui/server.go`, at the `Config` struct (around line 88), add a
field and the abstraction it depends on. Mirror the existing `StatusViewer`
pattern (narrow interface so tests can inject fakes):

```go
// AudioSubscriber is the UI's narrow view of telemetry.AudioBroker.
// Tests inject fakes; production wires *telemetry.AudioBroker.
type AudioSubscriber interface {
    Subscribe(w http.ResponseWriter, r *http.Request)
}
```

Add the field to `Config`:

```go
    AudioBroker AudioSubscriber // nil disables /ui/telemetry/audio
```

- [ ] **Step 2: Mount the route in `Server.Mount`**

In the same file, alongside the other GET mounts (around line 211), add:

```go
    // Unguarded — see plan §"File Structure". firstRunGuard 302-redirects
    // guarded GETs during setup, and EventSource closes on redirects, so
    // a guarded mount would silently break SSE on fresh installs.
    s.mountGETUnguarded(mux, "/ui/telemetry/audio", s.handleTelemetryAudio)
```

Also update the `mountGETUnguarded` doc-comment in `internal/ui/server.go`.
It currently says the helper is only for the root redirect; after this change
it is for root plus read-only long-lived streams that must bypass first-run
redirects.

- [ ] **Step 3: Add the handler**

Create `internal/ui/telemetry.go`:

```go
package ui

import "net/http"

func (s *Server) handleTelemetryAudio(w http.ResponseWriter, r *http.Request) {
    if s.cfg.AudioBroker == nil {
        http.Error(w, "telemetry disabled", http.StatusServiceUnavailable)
        return
    }
    s.cfg.AudioBroker.Subscribe(w, r)
}
```

- [ ] **Step 4: Add the handler test**

Create `internal/ui/telemetry_test.go`:

```go
package ui

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

type fakeAudioBroker struct {
    called bool
}

func (f *fakeAudioBroker) Subscribe(w http.ResponseWriter, r *http.Request) {
    f.called = true
    w.Header().Set("Content-Type", "text/event-stream")
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("data: test\n\n"))
}

func TestHandleTelemetryAudio_Disabled(t *testing.T) {
    _, mux := newTestServer(t, func(c *Config) { c.AudioBroker = nil })
    r := httptest.NewRequest("GET", "/ui/telemetry/audio", nil)
    w := httptest.NewRecorder()
    mux.ServeHTTP(w, r)
    if w.Code != http.StatusServiceUnavailable {
        t.Errorf("status = %d, want 503", w.Code)
    }
}

func TestHandleTelemetryAudio_DelegatesToBroker(t *testing.T) {
    fab := &fakeAudioBroker{}
    _, mux := newTestServer(t, func(c *Config) { c.AudioBroker = fab })
    r := httptest.NewRequest("GET", "/ui/telemetry/audio", nil)
    w := httptest.NewRecorder()
    mux.ServeHTTP(w, r)
    if !fab.called {
        t.Error("broker.Subscribe was not called")
    }
    if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
        t.Errorf("Content-Type = %q, want text/event-stream", got)
    }
}
```

(`newTestServer` already exists in `internal/ui/server_test.go`; use the
existing helper style rather than introducing a second mux helper.)

- [ ] **Step 5: Run all UI tests**

```
go test ./internal/ui -run TestHandleTelemetryAudio -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```
git add internal/ui/server.go internal/ui/telemetry.go internal/ui/telemetry_test.go
git commit -m "feat(ui): mount /ui/telemetry/audio SSE endpoint"
```

---

## Task 6: `Manager.AudioLevels()` — broker source

**Files:**

- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/core/manager_test.go`:

```go
func TestManager_AudioLevels_NoPlane(t *testing.T) {
    m := newTestManager(t)
    s := m.AudioLevels()
    if s.Generation != 0 {
        t.Errorf("idle: Generation = %d, want 0", s.Generation)
    }
}

func TestManager_AudioLevels_ActivePlane(t *testing.T) {
    m := newTestManager(t)
    snap := dataplane.AudioLevelSnapshot{Generation: 7, LPeak: 0.5}
    m.mu.Lock()
    m.plane = &fakePlane{audio: snap}
    m.mu.Unlock()
    got := m.AudioLevels()
    if got.Generation != snap.Generation || got.LPeak != snap.LPeak {
        t.Errorf("AudioLevels() = %+v, want %+v", got, snap)
    }
}
```

- [ ] **Step 2: Run the test to confirm it fails**

```
go test ./internal/core -run TestManager_AudioLevels -v
```

Expected: undefined method.

- [ ] **Step 3: Widen `planeRunner` and test fakes**

In `internal/core/manager.go`, add the new method to `planeRunner`:

```go
type planeRunner interface {
    Run(context.Context) error
    Done() <-chan struct{}
    Position() time.Duration
    SetFieldOrder(string) error
    BlitsTotal() uint64
    FramesTotal() uint64
    Underruns() uint64
    WireBytes() uint64
    LastACKAge() time.Duration
    AudioLevels() dataplane.AudioLevelSnapshot
}
```

Update every `planeRunner` fake in `internal/core/manager_test.go`:

```go
type fakePlane struct {
    audio dataplane.AudioLevelSnapshot
}

func (f *fakePlane) AudioLevels() dataplane.AudioLevelSnapshot { return f.audio }
func (f *contextDonePlane) AudioLevels() dataplane.AudioLevelSnapshot { return dataplane.AudioLevelSnapshot{} }
func (f *blockingDonePlane) AudioLevels() dataplane.AudioLevelSnapshot { return dataplane.AudioLevelSnapshot{} }
```

If converting `fakePlane` from an empty struct creates composite-literal
fallout, update those call sites to `&fakePlane{}`.

- [ ] **Step 4: Add the method**

In `internal/core/manager.go`, alongside `StatusHomeView` (around line 1146):

```go
// AudioLevels exposes the active plane's audio meter for telemetry
// subscribers. Returns a zero snapshot when no plane is running.
// Mu is held only for the snapshot read; no I/O. See
// docs/plans/2026-05-20-audio-vu-telemetry.md.
func (m *Manager) AudioLevels() dataplane.AudioLevelSnapshot {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.plane == nil {
        return dataplane.AudioLevelSnapshot{}
    }
    return m.plane.AudioLevels()
}
```

Verify `internal/core/manager.go` already imports `internal/dataplane` (it
should — `Plane` is constructed in `startPlaneLocked`).

- [ ] **Step 5: Run the test to confirm it passes**

```
go test ./internal/core -run TestManager_AudioLevels -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```
git add internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(core): expose Manager.AudioLevels for telemetry"
```

---

## Task 7: Wire broker into `main.go`

**Files:**

- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Construct + start the broker**

After `core.Manager` is built and before the HTTP server starts (see the
startup sequence in [CLAUDE.md §"Startup sequence"](../../CLAUDE.md)),
construct the broker and start its goroutine:

```go
// Telemetry broker — fans out per-tick audio levels to /ui/telemetry/audio
// subscribers. Independent of the dataplane field rate; safe to start
// before the first session. Stop it before HTTP shutdown so SSE handlers
// return promptly.
audioBroker := telemetry.NewAudioBroker(coreMgr)
brokerCtx, stopBroker := context.WithCancel(context.Background())
brokerDone := make(chan struct{})
go func() {
    defer close(brokerDone)
    audioBroker.Run(brokerCtx)
}()
```

(Adjust variable names to match the existing main.go conventions.)

- [ ] **Step 2: Pass it into `ui.Config`**

In the `ui.Config{...}` literal, add:

```go
    AudioBroker: audioBroker,
```

- [ ] **Step 3: Stop it during shutdown**

In the SIGINT/SIGTERM drain path, call `stopBroker()` and wait for
`brokerDone` before `httpSrv.Shutdown`. `http.Server.Shutdown` waits for
active handlers, and SSE handlers are intentionally long-lived; stopping the
broker first closes subscriber channels so `Subscribe` returns and HTTP
shutdown does not burn the 3 second grace period waiting on open EventSource
connections.

```go
stopBroker()
<-brokerDone
shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
_ = httpSrv.Shutdown(shutCtx)
```

- [ ] **Step 4: Build the bridge binary**

```
make build-bridge
```

Expected: clean build, no new lint warnings.

- [ ] **Step 5: Commit**

```
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(cmd): start AudioBroker and wire it into the UI server"
```

---

## Task 8: Browser-side `vu-meter.js` — **BLOCKED until receiver-v24 UI lands**

> ⛔ **Do not execute this task yet.** The JS module below targets
> `.tr-vu .ch-bar .s` selectors that live in the receiver-v24 chassis mockup
> (`.superpowers/brainstorm/1973-1779237107/receiver-v24.html`) — not in the
> currently-shipped `now-playing-banner.html`. Running this task today would:
>
> 1. Add a `<script src="/ui/static/vu-meter.js" defer>` tag that opens an
>    `EventSource` connection on every page load.
> 2. Have the JS find no `.tr-vu` element via `findBars()`, return silently,
>    and leak a permanent open SSE connection per tab with nothing to paint.
>
> Tasks 1–7 (Go side) + Task 9 (integration test) already deliver a fully
> working SSE endpoint at `/ui/telemetry/audio` that any future UI can
> subscribe to. Defer this task until the receiver-v24 chassis is merged
> into `internal/ui/templates/`, at which point this task is implementable
> as-written (the selectors are stable in the mockup).

**Files:**

- Create: `internal/ui/static/vu-meter.js`
- Modify: `internal/ui/templates/shell.html`
- Modify: `internal/ui/templates/now-playing-banner.html`
- Modify: `internal/ui/static/app.css`

- [ ] **Step 1: Create the JS module**

Create `internal/ui/static/vu-meter.js`:

```javascript
// L/R audio VU meter driver. Opens a single EventSource against
// /ui/telemetry/audio and paints `.s.on` classes onto the 12 segments
// per channel inside `.tr-vu .ch-bar`. Idle frames clear all `.on`
// classes; unlit segment CSS provides the dim/off state.
(function () {
    const SEGMENTS = 12;
    let bars = null;

    function findBars() {
        const meter = document.querySelector('.tr-vu');
        if (!meter) return null;
        const bars = meter.querySelectorAll('.ch-bar');
        if (bars.length < 2) return null;
        return {
            L: bars[0].querySelectorAll('.s'),
            R: bars[1].querySelectorAll('.s'),
        };
    }

    function refreshBars() {
        bars = findBars();
        return bars;
    }

    function levelToLit(linear) {
        if (linear <= 0) return 0;
        const db = 20 * Math.log10(linear);
        const norm = Math.max(0, Math.min(1, (db + 60) / 60));
        return Math.round(norm * SEGMENTS);
    }

    function paint(segments, lit) {
        for (let i = 0; i < segments.length; i++) {
            const on = i < lit;
            segments[i].classList.toggle('on', on);
        }
    }

    function clear() {
        const current = bars || refreshBars();
        if (!current) return;
        paint(current.L, 0);
        paint(current.R, 0);
    }

    function start() {
        if (!refreshBars()) return;
        document.addEventListener('htmx:afterSwap', function (ev) {
            const target = ev.detail && ev.detail.target;
            if (target && target.id === 'gr-now-playing') {
                refreshBars();
            }
        });

        const es = new EventSource('/ui/telemetry/audio');
        es.onmessage = (ev) => {
            let msg;
            try { msg = JSON.parse(ev.data); } catch (_) { return; }
            const current = bars || refreshBars();
            if (msg.t === 'idle') { clear(); return; }
            if (!current) return;
            if (msg.t === 'audio') {
                paint(current.L, levelToLit(msg.l));
                paint(current.R, levelToLit(msg.r));
                // Peak-hold (single segment) — paint as an extra .on
                // for one tick over the RMS lit count. Cheap and keeps
                // the meter feeling responsive.
                const lpk = levelToLit(msg.lpk);
                const rpk = levelToLit(msg.rpk);
                if (lpk > 0 && lpk <= SEGMENTS) current.L[lpk - 1].classList.add('on');
                if (rpk > 0 && rpk <= SEGMENTS) current.R[rpk - 1].classList.add('on');
            }
        };
        es.onerror = () => { clear(); /* EventSource will auto-reconnect */ };
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', start);
    } else {
        start();
    }
})();
```

- [ ] **Step 2: Add production VU markup and CSS**

In `internal/ui/templates/now-playing-banner.html`, add the compact meter
inside `.gr-now-playing-main` near the playback copy/actions:

```html
<div class="tr-vu" title="Audio out L/R level" aria-hidden="true">
    <span class="ch-lbl">L</span>
    <span class="ch-bar">
        <span class="s g"></span><span class="s g"></span><span class="s g"></span><span class="s g"></span><span class="s g"></span><span class="s g"></span>
        <span class="s y"></span><span class="s y"></span><span class="s y"></span>
        <span class="s r"></span><span class="s r"></span><span class="s r"></span>
    </span>
    <span class="ch-lbl">R</span>
    <span class="ch-bar">
        <span class="s g"></span><span class="s g"></span><span class="s g"></span><span class="s g"></span><span class="s g"></span><span class="s g"></span>
        <span class="s y"></span><span class="s y"></span><span class="s y"></span>
        <span class="s r"></span><span class="s r"></span><span class="s r"></span>
    </span>
</div>
```

Add matching `.tr-vu`, `.ch-bar`, `.s`, `.s.g.on`, `.s.y.on`, and `.s.r.on`
rules to `internal/ui/static/app.css`. Unlit segments should be visibly dim;
there is no `body.idle` dependency in production.

- [ ] **Step 3: Wire the script into the shell**

In `internal/ui/templates/shell.html`, alongside the existing
`<script src="/ui/static/now-playing.js" defer></script>` (find by grep for
`now-playing.js`), add:

```html
<script src="/ui/static/vu-meter.js" defer></script>
```

- [ ] **Step 4: Confirm the embed picks it up**

The new file lives under `internal/ui/static/` which is served via the
existing `embed.FS` in `internal/ui/assets.go`. No code change needed; the
file just needs to exist at build time.

```
make build-bridge
```

Expected: clean build.

- [ ] **Step 5: Manual smoke (no test added — JS DOM tests are out of scope)**

- Run the bridge against a fake-mister loopback per CLAUDE.md.
- Cast any audio source.
- Open http://localhost:8080/ui/ in a browser, devtools open.
- Network tab: confirm `/ui/telemetry/audio` is open as `EventSource`.
- Visually: `.tr-vu` segments respond to the audio.

- [ ] **Step 6: Commit**

```
git add internal/ui/static/vu-meter.js internal/ui/templates/shell.html internal/ui/templates/now-playing-banner.html internal/ui/static/app.css
git commit -m "feat(ui): paint L/R VU meter from /ui/telemetry/audio SSE stream"
```

---

## Task 9: Integration test against fake-mister

**Files:**

- Create: `tests/integration/audio_meter_test.go`

**Important context for this task:** the existing `Harness` struct in
`tests/integration/helper_test.go` only bundles a `*fakemister.Listener`,
`*groovynet.Sender`, and `*fakemister.Recorder` — it does **not** include a
`*core.Manager` or any cast-session driver. The Sender/Listener/Recorder
trio is the right shape for protocol-level scenarios (INIT/SWITCHRES/CLOSE,
field decode, etc.) but it stops short of running a Plane.

This task therefore has two parts:

1. Extend `helper_test.go` with a new helper — `NewLevelSourceHarness(t)` —
   that returns a `LevelSource` implementation backed by an `*AudioMeter`
   the test can drive directly. **Do not** thread a real `*core.Manager`
   through the harness yet — that's a larger refactor and is unnecessary
   for this test.
2. Write the integration test against that harness plus the real broker
   over a real `httptest.NewServer`.

The synthetic tone is generated in-test (no ffmpeg) so the integration test
stays fast and the existing `make test-integration` target doesn't slow down.

- [ ] **Step 1: Add the helper to `helper_test.go`**

Append to `tests/integration/helper_test.go`:

```go
import (
    // ...existing...
    "encoding/binary"
    "math"
    "github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
)

// directLevelSource wraps an AudioMeter so a test can drive levels
// without spinning up a full *core.Manager / *Plane. Implements
// telemetry.LevelSource via structural typing.
type directLevelSource struct {
    m *dataplane.AudioMeter
}

func (d *directLevelSource) AudioLevels() dataplane.AudioLevelSnapshot {
    return d.m.Snapshot()
}

// NewLevelSourceHarness returns a LevelSource bound to a fresh AudioMeter
// and the AudioMeter itself so tests can call Observe directly.
func NewLevelSourceHarness(t *testing.T) (*directLevelSource, *dataplane.AudioMeter) {
    t.Helper()
    m := &dataplane.AudioMeter{}
    return &directLevelSource{m: m}, m
}

// SyntheticToneStereo48k builds N stereo s16le sample frames of a 1 kHz
// sine at amplitude `amp` (max 32760). Returns ~one-tick worth of PCM.
func SyntheticToneStereo48k(frames int, amp int16) []byte {
    if amp > 32760 {
        amp = 32760
    }
    pcm := make([]byte, frames*4)
    for i := 0; i < frames; i++ {
        v := int16(math.Round(float64(amp) * math.Sin(2*math.Pi*float64(i)/48)))
        binary.LittleEndian.PutUint16(pcm[i*4:i*4+2], uint16(v))
        binary.LittleEndian.PutUint16(pcm[i*4+2:i*4+4], uint16(v))
    }
    return pcm
}
```

- [ ] **Step 2: Write the integration test**

Create `tests/integration/audio_meter_test.go`:

```go
//go:build integration

package integration

import (
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "github.com/idio-sync/MiSTer_GroovyRelay/internal/telemetry"
)

func TestAudioMeter_EndToEnd(t *testing.T) {
    src, meter := NewLevelSourceHarness(t)

    broker := telemetry.NewAudioBroker(src)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go broker.Run(ctx)

    srv := httptest.NewServer(http.HandlerFunc(broker.Subscribe))
    defer srv.Close()

    // Drive a synthetic tone at field cadence (~60 Hz) in a background
    // goroutine so the broker has fresh snapshots to fan out.
    go func() {
        tick := time.NewTicker(time.Millisecond * 17)
        defer tick.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-tick.C:
                meter.Observe(SyntheticToneStereo48k(800, 26000), 2, 48000)
            }
        }
    }()

    reqCtx, cancelReq := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancelReq()
    req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL, nil)
    if err != nil {
        t.Fatalf("new request: %v", err)
    }
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        t.Fatalf("subscribe: %v", err)
    }
    defer resp.Body.Close()

    var sawAudio bool
    buf := make([]byte, 4096)
    for {
        n, err := resp.Body.Read(buf)
        if reqCtx.Err() != nil {
            break
        }
        if err == io.EOF {
            break
        }
        if err != nil {
            t.Fatalf("read event stream: %v", err)
        }
        if n == 0 {
            continue
        }
        for _, line := range strings.Split(string(buf[:n]), "\n") {
            line = strings.TrimSpace(line)
            if !strings.HasPrefix(line, "data:") {
                continue
            }
            var e struct {
                T   string  `json:"t"`
                LPk float32 `json:"lpk"`
            }
            if err := json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &e); err != nil {
                continue
            }
            if e.T == "audio" && e.LPk > 0.5 {
                sawAudio = true
                break
            }
        }
        if sawAudio {
            break
        }
    }
    if !sawAudio {
        t.Error("did not observe any audio frame with lpk > 0.5 within 2s")
    }
}
```

(Why no real ffmpeg / `*core.Manager`: Tasks 1–3 already prove the DSP
correctness end-to-end against `*Plane` via the existing
`TestPlane_AllocationBudget` harness. The job of this integration test is
to prove the SSE wire format and broker fan-out reach a real HTTP client,
not to re-prove the DSP. A full Manager + ffmpeg path can be added later
under a separate plan if the SSE → DSP coupling ever changes.)

- [ ] **Step 3: Run the integration test**

```
go test -tags=integration ./tests/integration -run TestAudioMeter_EndToEnd -v
```

Expected: PASS.

- [ ] **Step 4: Run the full CI gate**

```
go vet ./...
go test ./...
go test -race ./...
go test -tags=integration ./tests/integration/...
```

Expected: all green.

- [ ] **Step 5: Commit**

```
git add tests/integration/audio_meter_test.go
git commit -m "test(integration): end-to-end audio meter SSE smoke"
```

---

## Execution gating

This plan splits cleanly into two waves. **Implementers running today should
do Wave 1 only.** Wave 2 unblocks when the receiver-v24 UI lands.

**Wave 1 — Go side + integration test (do now):**

- Task 1: `dataplane.AudioMeter` DSP primitive
- Task 2: `Plane.AudioLevels()` accessor
- Task 3: Hook `Observe` into the tick loop
- Task 4: `telemetry.AudioBroker` SSE fan-out
- Task 5: Mount `/ui/telemetry/audio` (unguarded)
- Task 6: `Manager.AudioLevels()` source
- Task 7: Wire broker into `main.go`
- Task 9: Integration test (`tests/integration/audio_meter_test.go`)

After Wave 1, the SSE endpoint serves accurate JSON frames. Verify with:

```
curl --no-buffer http://localhost:8080/ui/telemetry/audio
```

while casting — you should see one `data:` line ~every 33 ms.

**Wave 2 — Browser side (do when receiver-v24 UI is merged):**

- Task 8: `vu-meter.js` + template/CSS edits

The receiver-v24 chassis HTML/CSS is currently a design mockup in
`.superpowers/brainstorm/`. When it's promoted into `internal/ui/templates/`
(likely as a successor to `now-playing-banner.html` or a sibling shell
template), Task 8 becomes executable as-written — the `.tr-vu .ch-bar .s`
selectors in the mockup are stable.

**Why split:** Wave 1 produces a complete, testable subsystem. Wave 2 is
purely a presentation-layer add-on. Shipping Wave 2 before the chassis lands
would create a JS file that opens an SSE connection per tab with no DOM to
paint — wasteful, and the kind of thing that ages badly if the chassis
selectors evolve between now and then.

---

## Out of scope (do not implement in this plan)

- Volume control or audio passthrough — the bridge doesn't control MiSTer
  audio volume; the meter is observe-only.
- Surround / >2 channels — v1 is stereo. Snapshot shape is mono/stereo only.
- ffmpeg `astats` — the PCM is already in memory; forking through a filter
  is strictly worse on CPU and complexity.
- Peak-hold dot with slow decay — the wire format accommodates it (`lpk`,
  `rpk` are separate), but smoothing/decay belongs in `vu-meter.js`. The
  starter JS includes a one-tick peak overlay; richer ballistics are a UI
  polish pass.
- Other live metrics (throughput, ACK ms, drops). The broker contract
  supports them as new `t` values on the same stream; future plan.

---

## Self-review notes for the implementer

- **Spec coverage:** every section of the brief in the task message above is
  addressed — DSP design (Task 1), hook point (Task 3), telemetry channel
  (Tasks 4–5), UI integration (Tasks 6–8), cost/risk (this document, §Cost
  & risk), test plan (Tasks 1–4, 9).
- **`Manager.mu` discipline:** Task 6's `AudioLevels()` holds the lock only
  for the pointer read; no I/O, no nested calls.
- **One HTTP listener:** Task 5 mounts on the existing `internal/ui` mux.
  No new socket.
- **`ApplyScope`:** no new config fields, so no scope decision needed. If a
  future iteration adds a "VU meter enabled" toggle, that's `ScopeHotSwap` —
  the broker can be turned off mid-session without touching the dataplane.
- **CI:** every modified package has tests in this plan; `go test -race`
  and `go test -tags=integration` both stay green.
