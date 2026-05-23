# AUX Analog Visualizer Design

**Date:** 2026-05-22
**Status:** Brainstormed; awaiting implementation plan.
**Scope:** Add a manual `AUX` receiver source that lets an external analog audio signal drive the existing CRT visualizer path. V1 supports one configured analog input while keeping the config and adapter boundaries ready for multiple named inputs later.

## Background

GroovyRelay already renders Plex and Jellyfin music casts by asking FFmpeg to synthesize video from an audio stream, then sending raw BGR video fields and optional PCM audio through the existing Groovy_MiSTer data plane. That path is the right foundation for analog audio visualization: the analog signal only needs to become an FFmpeg-readable audio input.

The deployment split matters:

- Native binaries can often read a host audio input directly.
- Docker/Unraid installs should not require passing through arbitrary sound devices. The practical remote path is a low-latency audio stream produced by the machine that has the line-in or USB audio interface.

## Goals

1. Add a manual `AUX` / analog source in the receiver UI.
2. Let `AUX` start a visualizer-enabled session from either a local FFmpeg capture device or a configured remote audio stream URL.
3. Default to visualizer-only operation: the captured audio drives CRT video, while the listener keeps using the original stereo/source audio path.
4. Allow opt-in audio monitoring through the MiSTer PCM path.
5. Bias the capture path toward low visual reaction latency without making v1 fragile.
6. Keep v1 to one configured `AUX` input, but use internal shapes that can grow to multiple named inputs.
7. Make `AUX` preempt the active cast, matching other source-start behavior.

## Non-goals

- Automatic signal detection or always-on idle visualization.
- A polished cross-platform capture-agent binary in v1.
- Multiple named inputs in v1.
- Beat detection, Go-side visualizer rendering, shader rendering, or changes to the Groovy_MiSTer UDP protocol.
- Docker sound-device passthrough as the primary Unraid path.
- Replacing the existing Plex/Jellyfin music visualizer path.

## User Decisions

| Topic | Decision |
| --- | --- |
| Deployment targets | Native direct capture and Unraid/remote streaming |
| Remote source model | Platform-neutral; prefer no helper when a stream already exists, FFmpeg command/script when needed |
| Audio output | Configurable; visualizer-only default, MiSTer monitoring opt-in |
| Source startup | Manual `AUX` control in the receiver UI |
| Number of inputs | One input in v1; design for more later |
| Latency priority | Visualizer should react as close to the real audio as practical |
| Active cast behavior | `AUX` preempts the active cast |

## Architecture

Add `internal/adapters/aux` as a normal adapter/source. The adapter owns AUX config, status, and session construction. The receiver chassis owns the `/receiver/aux/*` routes and calls the adapter through a narrow interface, preserving the existing pattern that chassis code does not import concrete adapter packages.

The adapter builds a `core.SessionRequest` with:

- `Source: "aux"`
- `AdapterRef: "aux:<input-id>"`
- `MediaKind: core.MediaKindMusic`
- `Visualizer.Enabled: true`
- metadata title/display fields such as `AUX` or the configured input name
- `AudioOutputMode: core.AudioOutputVisualOnly` by default, or `core.AudioOutputMonitor` when configured
- capabilities suitable for a live source: `CanPause=false`, `CanSeek=false`; stop remains available through core session stop

The adapter supports two input modes:

1. `local_capture` for native binaries. FFmpeg captures directly from a configured host audio device.
2. `stream_url` for Docker/Unraid and other remote cases. FFmpeg reads a low-latency audio stream URL.

The existing FFmpeg visualizer path remains the renderer. AUX does not introduce a separate Go-side video generator.

For `stream_url`, the AUX adapter does not hand the operator URL directly to FFmpeg. It creates two short-lived single-use tokens (probe + play) against a validating stream proxy owned by the AUX adapter and mounted on the existing UI listener. The proxy performs each outbound HTTP(S) GET with the AUX URL validator, no redirects, bounded timeout, and no caller-supplied headers, then exposes the response body on relay-local loopback URLs (`http://127.0.0.1:<ui-port>/internal/aux-proxy/?aux_token=...`) that FFprobe/FFmpeg consume for those reads. This makes the redirect policy enforceable instead of relying on FFmpeg's HTTP demuxer behavior. See §Stream URL Proxy for the token lifecycle and listener placement.

For stream URLs, `core.SessionRequest` also needs an explicit probe/play split so the single-use probe token is never consumed by the later FFmpeg playback process. For backward compatibility, non-AUX sessions leave `StreamProbeURL` empty and core probes `StreamURL` as it does today. AUX `stream_url` sessions set both fields: `StreamProbeURL` is the probe-token URL, and `StreamURL` is the play-token URL passed into `ffmpeg.PipelineSpec.InputURL`.

For local capture, `core.SessionRequest` needs a small adapter-agnostic capture input block so adapters do not smuggle capture syntax through `StreamURL`. Exactly one of the stream URL pair (`StreamURL` plus optional `StreamProbeURL`) or `AudioCapture.Enabled` may be set for an AUX session:

```go
type AudioCaptureInput struct {
    Enabled         bool
    Format          string
    Device          string
    SampleRate      int
    Channels        int
    ThreadQueueSize int
    AnalyzeDuration time.Duration
    ProbeSize       int
}

type AudioOutputMode string

const (
    AudioOutputDefault    AudioOutputMode = ""
    AudioOutputVisualOnly AudioOutputMode = "visual_only"
    AudioOutputMonitor    AudioOutputMode = "monitor"
)

type SessionRequest struct {
    // existing fields...
    StreamProbeURL  string // optional; empty means probe StreamURL
    AudioCapture    AudioCaptureInput
    AudioOutputMode AudioOutputMode
}
```

`core.Manager` maps this block and output mode into the FFmpeg/data-plane packages at the same core-to-FFmpeg boundary that already maps visualizer metadata, media input policy, subtitles, and audio settings.

## Configuration

V1 exposes one configured input. The config uses a single nested table because the current generic adapter settings pipeline serializes `FieldDef` keys as flat/dotted TOML assignments; it does not support `[[array-of-tables]]` fields. The Go side should still isolate the input fields in an `AUXInput` struct so v2 can migrate to multiple named inputs with an explicit migration when the UI grows an input selector.

The TOML uses `[adapters.aux.input]`. Field definitions use dotted keys such as `input.id` and `input.url`, which the existing form serializer emits as valid nested TOML under `[adapters.aux]`.

Conceptual TOML shape:

```toml
[adapters.aux]
enabled = false

[adapters.aux.input]
id = "aux"
name = "AUX"
mode = "stream_url" # "stream_url" or "local_capture"
audio_output = "visual_only" # "visual_only" or "monitor"

# stream_url mode
url = "http://capture-host:8090/aux.wav"

# local_capture mode
format = "dshow" # examples: dshow, avfoundation, alsa, pulse
device = "audio=Line In (USB Audio Device)"
sample_rate = 48000
channels = 2
thread_queue_size = 64
analyze_duration_ms = 100
probe_size = 32768
```

Validation rules:

- `enabled=false` allows incomplete input fields and an omitted `[adapters.aux.input]` table.
- `enabled=true` requires one `[adapters.aux.input]` table with `id`, `name`, and a supported `mode` populated.
- `stream_url` requires an absolute URL accepted by the AUX stream URL validator and its media input policy.
- `local_capture` requires `format` and `device`.
- `audio_output` defaults to `visual_only`.
- `sample_rate` and `channels` default to the bridge audio config unless explicitly set for capture.
- `thread_queue_size`, `analyze_duration_ms`, and `probe_size` are optional low-latency knobs. They are typed fields, not arbitrary FFmpeg option strings.

The implementation plan may choose exact field names that match existing adapter config conventions, but it should preserve this model.

### Field defaults

`AnalyzeDuration` is the Go-side `time.Duration` materialized from the integer `analyze_duration_ms` TOML field. Defaults marked "bridge" inherit from the existing `[bridge.audio]` section when the AUX field is unset.

| TOML field | Go type | Default | Notes |
| --- | --- | --- | --- |
| `enabled` | `bool` | `false` | Whole adapter gate. |
| `input.id` | `string` | required if `enabled=true` | Stable identifier, used in `AdapterRef`. |
| `input.name` | `string` | required if `enabled=true` | Display name (VFD, source button tooltip). |
| `input.mode` | `string` | required if `enabled=true` | `stream_url` or `local_capture`. |
| `input.audio_output` | `string` | `visual_only` | `visual_only` or `monitor`. |
| `input.url` | `string` | empty | Required when `mode=stream_url`. |
| `input.format` | `string` | empty | Required when `mode=local_capture` (e.g. `dshow`, `alsa`). |
| `input.device` | `string` | empty | Required when `mode=local_capture`. |
| `input.sample_rate` | `int` | bridge | Hz. |
| `input.channels` | `int` | bridge | 1 or 2. |
| `input.thread_queue_size` | `int` | `64` | FFmpeg `-thread_queue_size` for capture inputs only. |
| `input.analyze_duration_ms` | `int` (→ `time.Duration`) | `100` | Probe + FFmpeg `-analyzeduration`; 100 ms is a low-latency knob. |
| `input.probe_size` | `int` | `32768` | FFmpeg `-probesize`. |

### Apply scopes

Every field declares an `adapters.ApplyScope`, per [`internal/adapters/adapter.go:117-134`](../../../internal/adapters/adapter.go#L117-L134). Most AUX fields are `ScopeHotSwap` because AUX sessions are manually started and there is no persistent cast that needs rebuilding on edit; the next AUX-start always reads the current config. The exceptions are fields that change the session in flight.

| Field | Apply scope | Rationale |
| --- | --- | --- |
| `enabled` | `ScopeHotSwap` | Toggles the source-cluster button. Generic adapter settings may call `Start()` / `Stop()` on enabled transitions; AUX `Start()` does not start playback, while AUX `Stop()` must ownership-stop only an active AUX session and must no-op for foreign active sessions. |
| `input.id` / `input.name` | `ScopeHotSwap` | Identifier and display string; next AUX-start picks them up. Changing `id` mid-cast does not invalidate the running session because its `AdapterRef` was captured at start. |
| `input.mode` | `ScopeHotSwap` | A mode flip while live does not rewrite the running FFmpeg argv; the change applies to the next AUX-start. |
| `input.audio_output` | `ScopeHotSwap` | Same — visualizer-only ↔ monitor flips at the next start. Switching live would require rebuilding both FFmpeg argv and the data plane's `INIT` advertisement; the spec does not require live switching in v1. |
| `input.url` | `ScopeHotSwap` | The proxy reads it at AUX-start. |
| `input.format` / `input.device` | `ScopeHotSwap` | Capture binding is bound at FFmpeg spawn time. |
| `input.sample_rate` / `input.channels` | `ScopeHotSwap` | Bound at probe + spawn time. |
| `input.thread_queue_size` / `input.analyze_duration_ms` / `input.probe_size` | `ScopeHotSwap` | Low-latency knobs read at AUX-start. |

No AUX field is `ScopeRestartCast` (no live-session rewrite is supported in v1) or `ScopeRestartBridge` (nothing AUX touches is process-global state like `bridge.network.source_port`).

## FFmpeg Pipeline Changes

`ffmpeg.PipelineSpec` needs three small extensions.

First, capture inputs need structured pre-input fields mapped from `core.AudioCaptureInput`. The FFmpeg package owns the concrete argv mapping:

```go
type CaptureInputSpec struct {
    Enabled         bool
    Format          string
    Device          string
    SampleRate      int
    Channels        int
    ThreadQueueSize int
    AnalyzeDuration time.Duration
    ProbeSize       int
}
```

When enabled, `BuildCommand` places input options before `-i` without using shell strings. Examples include `-f dshow -i audio=Line In (...)` on Windows, `-f avfoundation -i :0` on macOS, or `-f alsa -i hw:1,0` on Linux. Low-latency options should also be structured argv tokens.

Second, probing must stop being URL-only. Add an input-shaped probe entry point rather than overloading `Probe(ctx, path, url, policy)`:

```go
type ProbeInputSpec struct {
    URL     string
    Policy  MediaInputPolicy
    Capture CaptureInputSpec
}

func ProbeInput(ctx context.Context, ffprobePath string, input ProbeInputSpec) (*ProbeResult, error)
```

For `stream_url`, `ProbeInput` receives `SessionRequest.StreamProbeURL` when set, otherwise `StreamURL`, preserves today's URL argv shape against that input, and applies `Policy`. `startPlaneLocked` still passes `SessionRequest.StreamURL` into `ffmpeg.PipelineSpec.InputURL`, so the play token is consumed only by FFmpeg. For `local_capture`, it runs a bounded live-input probe with the same structured capture args used by `BuildCommand`, for example `ffprobe -f <format> <capture-options> -i <device> -show_streams -print_format json`. `core.Manager.probeForStart` calls this input-shaped probe. It allows `StreamURL` to be empty when `AudioCapture.Enabled=true` and treats capture duration as zero because live inputs do not have a finite media duration.

### Visualizer "must have audio" gate must move off `probe.AudioRate`

Today, `core.probeForStart` at [`internal/core/manager.go:511-515`](../../../internal/core/manager.go#L511-L515) gates visualizer sessions on `probe.AudioRate > 0`. That gate works for Plex/Jellyfin because their input URLs are HLS/transcode endpoints whose `ffprobe` reliably reports the audio stream. It does NOT work for AUX `local_capture`: `ffprobe` against a live ALSA/DirectShow/avfoundation device often returns an empty or partial stream list depending on the FFmpeg build and the device's quirks, even when the device will produce audio under `ffmpeg` itself.

The gate must move from probe shape to request shape:

- `Visualizer.Enabled && StreamURL != "" && !AudioCapture.Enabled` — visualizer is fed by a remote audio source, probe MUST see an audio stream. This is today's behavior; AUX `stream_url` mode goes through this path because `StreamProbeURL` points at a relay-local proxy URL and `StreamURL` points at the separate relay-local play URL.
- `Visualizer.Enabled && AudioCapture.Enabled` — audio is asserted by configuration (the operator picked a capture device). Skip the `probe.AudioRate > 0` check. The bounded `ProbeInput` is still run to confirm the device opens at all, but a zero `AudioRate` is treated as "probe could not report rates for this live input" rather than "no audio." Downstream code must use `AudioCapture.SampleRate` / `AudioCapture.Channels` as the source of truth for `INIT` and FFmpeg argv when probe values are zero.
- `Visualizer.Enabled && StreamURL != "" && AudioCapture.Enabled` — rejected by the both-set validation in §Architecture before any probe.

For AUX `stream_url` specifically: the documented v1 producer must support the probe/read lifecycle described below, and its WAV/PCM stream should give `ffprobe` an audio stream during the probe token read. The gate's effective behavior for AUX `stream_url` is unchanged; the relaxed shape exists for `local_capture`.

Third, AUX needs a way to generate visualizer video while suppressing PCM output:

```go
SuppressAudioOutput bool
```

When `SuppressAudioOutput=true`, a visualizer session still maps the audio input into the visualizer filter graph, but does not append the `-map <audio> -f s16le <audio-pipe>` output.

This is not only an FFmpeg flag. The session-level `AudioOutputMode` must also flow into `dataplane.PlaneConfig`, so visual-only mode advertises `AudioRateOff` / zero channels in the Groovy `INIT`, does not start the audio pipe reader, and never waits for PCM chunks. `monitor` mode keeps the existing audio pipe and MiSTer PCM behavior. For `local_capture`, a successful probe whose `AudioRate` is zero must be normalized before building `PipelineSpec` / `PlaneConfig` by filling the effective audio rate and channel count from `AudioCapture.SampleRate` / `AudioCapture.Channels`; otherwise the existing single-input data-plane guard would suppress monitor audio. Tests must cover both FFmpeg argv and data-plane INIT/effective-audio behavior.

For `stream_url`, the existing URL input path should be reused, with AUX-specific low-latency defaults where safe. For `local_capture`, probing may need a short timeout and device-friendly behavior because live capture inputs do not always have finite duration.

## Receiver UI And Routes

The receiver UI should add an `AUX` source control. Pressing it sends a same-origin POST to start the configured input.

Proposed routes:

- `POST /receiver/aux/start`
- `POST /receiver/aux/stop` if transport stop is not yet available during implementation

Both routes use `application/x-www-form-urlencoded` bodies. In v1, `input_id` is optional: an empty body means "use the sole configured input." If `input_id` is present it must match the configured input id, otherwise the handler returns `422` (`AUX input unavailable`). This keeps v1 ergonomic while preserving the request shape needed for a later multi-input selector.

Responses follow the chassis JSON conventions:

- `204 No Content` on success.
- `400` for malformed requests.
- `403` from same-origin protection.
- `409` for stale or conflicting session state if the final handler needs generation checks.
- `422` for disabled, unconfigured, or unsupported AUX input.
- `500` for unexpected adapter/core errors.

The source cluster lights `AUX` when active. VFD display for an active AUX session should read like `AUX - Analog In` or the configured input name. If AUX is disabled or unconfigured, the UI should render the control unavailable and POSTs should still fail clearly.

### Route ownership: chassis, not adapter

The AUX-start route lives in `internal/chassis`, alongside the existing chassis-owned `POST /receiver/visualizer` route. The chassis talks to AUX through a narrow interface declared in `internal/chassis`; the chassis does NOT import the AUX adapter package, which keeps `TestProductionImports_NoCrossPackageCoupling` in [`internal/chassis/import_check_test.go`](../../../internal/chassis/import_check_test.go) green.

We considered the alternative of mounting `/receiver/aux/*` on the AUX adapter via `adapters.RouteProvider` (the pattern Plex uses for `/player/*`). That pattern fits Plex because `/player/*` is the Plex Companion protocol spoken to external Plex apps — it is bound to the adapter's external protocol surface. AUX-start is the opposite: it is a same-origin POST from the chassis's own receiver page, triggered by a button rendered in the chassis source cluster. The chassis already owns receiver-page state and visualizer-mode mutation; routing AUX-start through `RouteProvider` would split the chassis source-cluster click handlers across two packages, with one click handler living in the chassis (visualizer mode) and another in the adapter (AUX-start) for no consistent reason.

If a later spec wires source-cluster click handlers for `JELLYFIN` / `DLNA` / `STREAMS` (today they are inert display buttons per [`internal/chassis/data.go:65-72`](../../../internal/chassis/data.go#L65-L72)), those should follow the same pattern: chassis route, narrow interface, adapter satisfies the interface.

### Narrow interface

```go
type AUXStarter interface {
    AUXStatus(ctx context.Context) AUXStatus
    StartAUX(ctx context.Context, inputID string) (adapterRef string, err error)
    StopAUX(ctx context.Context, inputID string) (matched bool, err error)
}

type AUXStatus struct {
    Enabled      bool
    Configured   bool
    Active       bool
    InputID      string
    DisplayName  string
    AdapterRef   string
    ErrorMessage string
}
```

`StopAUX` must guard ownership. It calls `core.Manager.StopIfAdapterRef("aux:<input-id>")`, which is a no-op when the active session is a foreign Plex/Jellyfin/DLNA/URL cast. Tests must include a foreign-active-session stop attempt (expecting a 204 no-op response that does NOT stop the foreign session). The interface does not carry a `Generation uint64` because the stop button only needs adapter-ref-based ownership checks; generation gating is a transport-control concept (used by Spec 3's pause/seek) that AUX-start does not need.

### Source cluster layout

The current source cluster is four buttons (`STREAMS`, `PLEX`, `JELLYFIN`, `DLNA`) hardcoded in [`internal/chassis/data.go:217-222`](../../../internal/chassis/data.go#L217-L222). AUX is added as a fifth button — none of the existing buttons are removed. The desktop layout uses a single horizontal row; mobile breakpoints may need a 2x3 grid or a single-row horizontal scroll. The existing chassis responsive tests must be extended to cover the five-button layout at both desktop and mobile widths so AUX does not overflow or regress rendering.

## Stream URL Proxy

`stream_url` mode uses a relay-local validating proxy rather than passing the operator URL directly to FFmpeg.

Flow:

```text
operator capture URL
        |
        v
aux adapter validates URL shape
        |
        v
relay-local stream proxy opens outbound GET with redirects disabled
        |
        v
loopback proxy URLs handed to core.SessionRequest.StreamProbeURL and StreamURL
        |
        v
FFprobe probe input, then FFmpeg visualizer input
```

### Listener placement: shared UI listener, query-param token

The proxy mounts on the existing UI listener (the one socket bound on `bridge.ui.http_port`) rather than spinning a second loopback-bound listener. That keeps the "one HTTP listener" invariant from `CLAUDE.md` intact and avoids inventing a second socket lifecycle.

Ownership is adapter-side, not chassis-side: `internal/adapters/aux` implements `adapters.PublicRouteProvider` and mounts only `/internal/aux-proxy/` through the existing registry walk in `cmd/mister-groovy-relay/main.go`. The chassis receives only the `AUXStarter` interface for `/receiver/aux/*`. This keeps the receiver click route in chassis, the proxy token store/lifecycle in AUX, and avoids a chassis import of `internal/adapters/aux`.

The proxy is mounted under `/internal/aux-proxy/`. The path itself is constant; the token is carried as a query parameter `?aux_token=<128-bit base64url>`. Query-param placement (not path placement) is deliberate so the existing [`redactURL` helper in `internal/core/manager.go:56-78`](../../../internal/core/manager.go#L56-L78) can redact it from any operator log line that prints `req.StreamURL` or `req.StreamProbeURL`. `redactURL` must be extended to know about `aux_token` alongside the existing `api_key` / `X-Plex-Token` / `token` cases.

Because the listener is bound on `0.0.0.0` (it has to be, to serve the UI on the LAN), the proxy handler must refuse non-loopback clients. AUX generates proxy URLs with `127.0.0.1` (or `[::1]` if the listener is IPv6-only), and the handler rejects any `RemoteAddr` that is not loopback before token lookup. Token unguessability remains a second boundary and protects against accidental leakage or local races:

- Token entropy MUST be ≥128 bits, generated with `crypto/rand`.
- Tokens MUST be single-use per read (see two-token lifecycle below).
- Tokens MUST expire after a short TTL (recommended 5 seconds for the probe token, the full AUX-session lifetime for the play token, hard-capped at 24h regardless).
- The handler MUST use constant-time token comparison (`subtle.ConstantTimeCompare`).
- Logging of the full `StreamURL` / `StreamProbeURL` MUST route through `redactURL` everywhere either prints, including request logs, error wrapping, and event-log entries.

### Two-token lifecycle: separate probe and play tokens, sequential upstream GETs

Each AUX-start mints two independent tokens:

1. `probeToken` — single-use, ≤5 second TTL. Exposed via `SessionRequest.StreamProbeURL` and consumed by `ProbeInput`'s `ffprobe` invocation.
2. `playToken` — single-use, lives for the AUX session. Exposed via `SessionRequest.StreamURL` and consumed by `BuildCommand`'s `ffmpeg` invocation.

The proxy opens the upstream operator URL exactly twice per AUX-start: once when the probe token is consumed (a short read sized to satisfy `ffprobe` headers, closed when probe exits), once when the play token is consumed (long-lived for the cast). This costs one extra HTTP round-trip and dial against the operator's producer compared to a single-open model, but it is meaningfully simpler than tee-ing one upstream into two readers and avoids the synchronization required to make a single upstream serve both a short-lived probe and a long-lived play.

The producer must tolerate two sequential client connections per AUX-start. A one-shot `ffmpeg -listen 1 ...` command by itself is not sufficient because the probe can consume that single served request. The documented v1 producer should therefore be either a real HTTP audio endpoint that supports repeated GETs or a tiny restart loop around `ffmpeg -listen 1` so the producer is ready again after the probe read exits.

Each upstream GET independently:

- Applies the AUX URL validator before dialing.
- Uses Go's `http.Client` with `CheckRedirect: http.ErrUseLastResponse`.
- Treats any 3xx response as `AUX input redirected`.
- Rejects non-2xx responses.
- Uses bounded dial / TLS-handshake / response-header timeouts.
- Sends no operator-configurable request headers in v1.
- Closes the outbound response when the AUX session stops or FFmpeg exits.

Probe-token failures happen before core preempts the active cast because `probeForStart` runs before acquiring `Manager.mu`. Play-token failures happen during FFmpeg startup after the prior plane has already been cancelled by the existing core start path; those failures must surface as a clear AUX input error and clean up the play token, but v1 does not promise to preserve the previous cast after a probe-success/play-open failure.

FFmpeg consumes only the relay-local loopback URL. The `MediaInputPolicy` on that loopback URL should still disable reconnects and set `RWTimeout`, but SSRF and redirect safety live in the proxy, not in FFmpeg flags.

## Remote Stream Producer

V1 does not require a custom capture agent. For Unraid and other remote deployments, the documented path is an FFmpeg command on the machine with the analog input, exposing a low-latency audio stream that GroovyRelay consumes via `stream_url`.

V1 should recommend HTTP WAV/PCM as the default remote producer because it is easy to inspect, works with stock FFmpeg, avoids remote command execution, and is good enough for visualizer-first latency on a wired LAN. RTP/UDP can remain an advanced later option after it has real firewall and jitter testing.

Example producer patterns to validate and document during implementation:

```bash
# Linux / ALSA example producer on the machine with line-in.
# The loop is intentional: AUX probe + play consume two sequential GETs.
while true; do
  ffmpeg -nostdin -f alsa -thread_queue_size 64 -sample_rate 48000 -channels 2 -i hw:1,0 \
    -vn -ac 2 -ar 48000 -f wav -listen 1 http://0.0.0.0:8090/aux.wav
  sleep 0.2
done

# Windows / DirectShow shape. Device names are examples; operators list them
# with: ffmpeg -list_devices true -f dshow -i dummy
while ($true) {
  ffmpeg -nostdin -f dshow -thread_queue_size 64 -audio_buffer_size 50 `
    -i audio="Line In (USB Audio Device)" `
    -vn -ac 2 -ar 48000 -f wav -listen 1 http://0.0.0.0:8090/aux.wav
  Start-Sleep -Milliseconds 200
}
```

The exact commands may change after real-device validation, but the spec locks the v1 operator story: GroovyRelay consumes `http://capture-host:8090/aux.wav`; the capture machine owns OS-specific line-in capture; the producer must be able to serve the probe request and then a second long-lived play request.

The relay should only consume the configured URL. It should not SSH into remote machines or execute arbitrary remote commands.

## Latency Behavior

The visual reaction target is "as close to the real audio as practical." V1 should use low-latency FFmpeg options where they are known to help, such as smaller analyze/probe windows and reduced buffering. It should avoid brittle flags that frequently break device compatibility.

`visual_only` is the default because the original analog source can keep feeding the user's real listening chain. `monitor` mode sends captured PCM to the MiSTer, but operators should expect some latency and possible capture/network jitter.

Two distinct latencies matter here, with different budgets:

### First-frame latency (AUX-start → first visualizer field on screen)

Bounded by the existing data-plane startup pattern, not by AUX-specific factors:

- AUX adapter URL validation + proxy token mint: ≤10 ms.
- `ProbeInput` against the relay-local proxy URL or capture device: ≤500 ms typical (`analyze_duration_ms=100` plus a fixed-overhead device-open or HTTP dial).
- FFmpeg spawn + decoder/filter-graph initialization: ≤300 ms typical for showcqt/showspectrum.
- Data-plane `defaultPrebufferFields = 6` at NTSC 59.94 Hz ≈ 100 ms before the field tick loop starts. See [`internal/dataplane/plane.go:294-308`](../../../internal/dataplane/plane.go#L294-L308). This is load-bearing for startup smoothness and is NOT something AUX should tune away.
- FPGA structural pipeline ≈ 1 field period.

V1 first-frame target: ≤1 s from POST to visible field. This is a noticeable but acceptable startup delay for a manually-triggered source.

### Steady-state reactivity (analog transient → visible change on the CRT)

This is the "as close to the real audio as practical" target. It is independent of `defaultPrebufferFields`, which only governs startup; once the field tick is running the visualizer changes one field at a time from the most recent decoded frame.

The dominant terms are:

- Capture-side buffer at the producer (`thread_queue_size` × frame duration, plus the OS audio device's own buffer): ~10–60 ms depending on tuning.
- LAN + relay-local proxy hop (wired): <5 ms.
- FFmpeg demuxer + filter-graph latency at steady state: bounded by `analyze_duration_ms` initially, then by frame-by-frame decode (~1 NTSC field).
- One field period (~17 ms) for the visualizer field to be sent.
- FPGA pipeline (~1 field period).

V1 steady-state target: a transient in the analog input appears on the CRT within roughly 250 ms on a wired LAN with the default HTTP WAV/PCM producer and `visual_only` mode. This is a manual/diagnostic target rather than a hard automated test in v1, because capture devices and FFmpeg demuxers vary by platform.

`monitor` mode adds the data plane's `defaultAudioDelay = 67 ms` (see [`internal/dataplane/plane.go:308`](../../../internal/dataplane/plane.go#L308)) to the audio path. This delay affects audio-to-MiSTer timing, not visualizer reactivity, so the 250 ms steady-state target applies to `monitor` mode as well.

## Security

`stream_url` must use explicit validation plus the existing media input policy machinery. Policy flags alone are not a URL validator.

V1 allowed URL shape:

- Scheme: `http` or `https` only.
- Host: must parse as a host or IP address; empty hosts are rejected.
- Userinfo is rejected.
- Fragment is rejected.
- Redirects are not allowed. The relay-local proxy enforces this by refusing 3xx responses instead of following them.
- FFmpeg consumes only the relay-local loopback proxy URL, and the proxy handler rejects non-loopback clients. Default policy for that URL: `ProtocolWhitelist=["http","tcp"]`, `DisableReconnect=true`, bounded `RWTimeout`, and no input headers.
- `file`, `udp`, `rtp`, and `srt` stream URLs are out of v1 unless a later spec adds source-specific validation and operator-facing firewall guidance.

`local_capture` config must never be a shell command. It is parsed into explicit argv tokens. The adapter should reject suspicious empty or malformed capture fields early, but it does not need to sanitize for shell metacharacters because no shell is involved.

The receiver POST routes use the same same-origin protection as existing chassis mutation endpoints.

## Error Handling

- Disabled or unconfigured AUX: UI disabled; POST returns a clear `422` JSON error.
- Local capture cannot open: adapter reports an error state and the user sees a clear AUX input failure.
- Remote stream unreachable: map probe failures to a clear "AUX input unreachable" POST error before preempt; map play-open failures to a clear AUX error state/event-log entry after preempt.
- Unsupported FFmpeg capture format/device: surface the FFmpeg start failure without exposing sensitive local paths beyond the configured device name.
- Active cast replacement: `AUX` should preempt only after config validation and probe-token validation have succeeded, preserving the existing `core.Manager` pattern of probing before acquiring the session lock. V1 does not preserve the previous cast if the later play-token GET fails during FFmpeg startup.
- Stop route ownership: an AUX stop request that no longer matches the active adapter ref returns success-or-noop without stopping a foreign session.

## Testing

Unit tests:

- AUX config defaults and validation, including the single `[adapters.aux.input]` table shape and dotted `FieldDef` keys that round-trip through the current settings UI serializer.
- Per-field `ApplyScope` declarations match the §Configuration "Apply scopes" table.
- Settings save/toggle behavior for `enabled=false` while AUX is active: adapter `Stop()` ownership-stops only AUX and does not stop a foreign active session.
- Session request input validation rejects both-set (`StreamURL` or `StreamProbeURL` plus `AudioCapture.Enabled`) and neither-set inputs; `StreamProbeURL` without `StreamURL` is invalid.
- Session request construction for `stream_url` and `local_capture`.
- `visual_only` sets the FFmpeg audio-suppression flag.
- `visual_only` also disables data-plane audio advertisement (`AudioRateOff`, zero channels) and audio pipe reading.
- `monitor` preserves normal PCM output in both FFmpeg argv and data-plane INIT.
- Input-shaped probing supports both relay-local stream URL and local capture specs; local capture probe uses a default bounded timeout of 3 seconds and accepts zero duration.
- `stream_url` probe/play separation: `probeForStart` consumes `StreamProbeURL`, `BuildCommand` consumes `StreamURL`, and the play token is not consumed by ffprobe.
- Visualizer "must have audio" gate accepts `AudioCapture.Enabled=true` with a probe that reports zero audio rate, but still rejects `StreamURL`-only visualizer sessions whose probe has no audio stream.
- `local_capture + monitor` with zero probe audio rate still advertises configured audio rate/channels and starts the PCM reader after probe normalization.
- FFmpeg argv generation for local capture uses structured args in the right order.
- FFmpeg argv generation for stream URL applies media input policy and low-latency options in the right place.
- Stream URL validation rejects unsupported schemes, userinfo, empty host, and fragments; the stream proxy rejects redirects by refusing 3xx responses.
- Stream proxy mounts via `adapters.PublicRouteProvider` at `/internal/aux-proxy/`; route tests assert no chassis import of `internal/adapters/aux` and no route conflict with `/ui/*` or `/receiver/*`.
- Stream proxy rejects non-loopback clients before token lookup.
- Stream proxy mints distinct probe and play tokens per AUX-start, each single-use; reusing or cross-using a token returns 401/403.
- `redactURL` removes `aux_token` from `StreamURL` and `StreamProbeURL` in operator log output.
- Stream proxy rejects 3xx and non-2xx upstream responses, uses no operator-provided headers, and tears down tokens/upstream responses on failed starts, session stop, adapter stop, and bridge shutdown.
- Remote producer validation covers the two sequential GET requirement; a one-shot `ffmpeg -listen 1` without the restart loop is documented as insufficient.
- Receiver AUX start/stop route method checks, same-origin failures, malformed `input_id`, disabled/unconfigured errors, success, and foreign-session stop no-op (a no-op stop request issued while a Plex cast is active must NOT stop the Plex cast).
- Source cluster template/CSS tests cover the added five-button AUX control at desktop and mobile widths.

Integration or fake-MiSTer tests:

- A synthetic audio source starts an AUX visualizer session and produces video fields.
- Visualizer-only AUX produces video fields without requiring an audio pipe output.
- Starting AUX preempts a prior active session.

Manual validation:

- Native Windows local capture with a real line-in or USB audio device.
- Linux/Unraid remote stream URL fed by an FFmpeg command from another host.
- Visual transient latency check against the roughly 250 ms target on a wired LAN.
- Receiver UI source state: disabled, idle, active, and error.

## Done When

- The receiver exposes a manual `AUX` source.
- A configured `stream_url` AUX input starts a CRT visualizer session.
- A configured `local_capture` AUX input can start through structured FFmpeg capture args on at least one native platform.
- `visual_only` is the default and suppresses PCM output to the MiSTer.
- `monitor` mode sends captured PCM audio through the existing MiSTer audio path.
- AUX sessions use the configured global visualizer mode, matching existing music sessions.
- AUX start preempts the active cast only after config and probe-token validation succeeds; later play-token failures produce a clear AUX error state.
- Disabled/unconfigured/probe-unreachable states produce clear UI and JSON errors; play-open failures produce clear UI and event-log errors after preempt.
- Tests cover config, argv generation, route behavior, session construction, proxy lifecycle, probe/play URL separation, loopback-only proxy access, and audio-output suppression.
- README, `internal/config/example.toml`, and operator docs include the AUX config shape and the validated remote FFmpeg producer command.
