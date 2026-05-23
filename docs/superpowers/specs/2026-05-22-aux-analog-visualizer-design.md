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

For `stream_url`, the AUX adapter should not hand the operator URL directly to FFmpeg. It should create a short-lived validating stream proxy owned by the relay process. The proxy performs the outbound HTTP(S) GET with the AUX URL validator, no redirects, bounded timeout, and no caller-supplied headers, then exposes the response body on a loopback URL that FFmpeg consumes for that one session. This makes the redirect policy enforceable instead of relying on FFmpeg's HTTP demuxer behavior.

For local capture, `core.SessionRequest` needs a small adapter-agnostic capture input block so adapters do not smuggle capture syntax through `StreamURL`. Exactly one of `StreamURL` or `AudioCapture.Enabled` may be set for an AUX session:

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
    AudioCapture   AudioCaptureInput
    AudioOutputMode AudioOutputMode
}
```

`core.Manager` maps this block and output mode into the FFmpeg/data-plane packages at the same core-to-FFmpeg boundary that already maps visualizer metadata, media input policy, subtitles, and audio settings.

## Configuration

V1 exposes one configured input, but the config should be shaped like a named input rather than a flat pile of fields. A later version can lift the single struct into a slice/map without changing adapter concepts.

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

- `enabled=false` allows incomplete input fields.
- `enabled=true` requires `input.id`, `input.name`, and a supported `mode`.
- `stream_url` requires an absolute URL accepted by the AUX stream URL validator and its media input policy.
- `local_capture` requires `format` and `device`.
- `audio_output` defaults to `visual_only`.
- `sample_rate` and `channels` default to the bridge audio config unless explicitly set for capture.
- `thread_queue_size`, `analyze_duration_ms`, and `probe_size` are optional low-latency knobs. They are typed fields, not arbitrary FFmpeg option strings.

The implementation plan may choose exact field names that match existing adapter config conventions, but it should preserve this model.

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

For `stream_url`, `ProbeInput` receives the relay-local proxy URL, preserves today's URL argv shape against that loopback input, and applies `Policy`. For `local_capture`, it runs a bounded live-input probe with the same structured capture args used by `BuildCommand`, for example `ffprobe -f <format> <capture-options> -i <device> -show_streams -print_format json`. `core.Manager.probeForStart` calls this input-shaped probe. It allows `StreamURL` to be empty when `AudioCapture.Enabled=true`, still rejects visualizer sessions whose probe has no audio stream, and treats capture duration as zero because live inputs do not have a finite media duration.

Third, AUX needs a way to generate visualizer video while suppressing PCM output:

```go
SuppressAudioOutput bool
```

When `SuppressAudioOutput=true`, a visualizer session still maps the audio input into the visualizer filter graph, but does not append the `-map <audio> -f s16le <audio-pipe>` output.

This is not only an FFmpeg flag. The session-level `AudioOutputMode` must also flow into `dataplane.PlaneConfig`, so visual-only mode advertises `AudioRateOff` / zero channels in the Groovy `INIT`, does not start the audio pipe reader, and never waits for PCM chunks. `monitor` mode keeps the existing audio pipe and MiSTer PCM behavior. Tests must cover both FFmpeg argv and data-plane INIT/effective-audio behavior.

For `stream_url`, the existing URL input path should be reused, with AUX-specific low-latency defaults where safe. For `local_capture`, probing may need a short timeout and device-friendly behavior because live capture inputs do not always have finite duration.

## Receiver UI And Routes

The receiver UI should add an `AUX` source control. Pressing it sends a same-origin POST to start the configured input.

Proposed routes:

- `POST /receiver/aux/start`
- `POST /receiver/aux/stop` if transport stop is not yet available during implementation

Responses follow the chassis JSON conventions:

- `204 No Content` on success.
- `400` for malformed requests.
- `403` from same-origin protection.
- `409` for stale or conflicting session state if the final handler needs generation checks.
- `422` for disabled, unconfigured, or unsupported AUX input.
- `500` for unexpected adapter/core errors.

The source cluster lights `AUX` when active. VFD display for an active AUX session should read like `AUX - Analog In` or the configured input name. If AUX is disabled or unconfigured, the UI should render the control unavailable and POSTs should still fail clearly.

The chassis talks to AUX through narrow interfaces declared in `internal/chassis`, not through the concrete adapter:

```go
type AUXStarter interface {
    AUXStatus(ctx context.Context) AUXStatus
    StartAUX(ctx context.Context, inputID string) (adapterRef string, generation uint64, err error)
    StopAUX(ctx context.Context, inputID string, generation uint64) (matched bool, err error)
}

type AUXStatus struct {
    Enabled      bool
    Configured   bool
    Active       bool
    InputID      string
    DisplayName  string
    AdapterRef   string
    Generation   uint64
    ErrorMessage string
}
```

`StopAUX` must guard ownership. It may call `core.Manager.StopIfSession("aux:<input-id>", generation)` when the UI has a fresh generation, or `StopIfAdapterRef("aux:<input-id>")` for a simpler stop button, but it must not stop a foreign active Plex/Jellyfin/DLNA/URL session. Tests must include a foreign-active-session stop attempt.

The current source cluster is four buttons (`STREAMS`, `PLEX`, `JELLYFIN`, `DLNA`). AUX implementation must update the cluster layout and responsive tests so adding `AUX` does not overflow or regress mobile rendering.

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
loopback proxy URL handed to core.SessionRequest.StreamURL
        |
        v
FFmpeg visualizer input
```

Proxy requirements:

- Listen only on loopback or use an unguessable in-process route mounted on the existing UI listener.
- Generate one opaque session token per AUX start.
- Allow only the owning session token to read the stream.
- Use Go's `http.Client` with `CheckRedirect` returning `http.ErrUseLastResponse`.
- Treat any 3xx as `AUX input redirected` and fail start before preempting the active cast.
- Reject non-2xx responses.
- Apply the same AUX URL validator before dialing.
- Set bounded dial/TLS/header timeouts.
- Send no operator-configurable request headers in v1.
- Close the outbound response when the AUX session stops or FFmpeg exits.

FFmpeg consumes only the relay-local URL. The `MediaInputPolicy` on that loopback URL should still disable reconnects and set `RWTimeout`, but SSRF and redirect safety live in the proxy, not in FFmpeg flags.

## Remote Stream Producer

V1 does not require a custom capture agent. For Unraid and other remote deployments, the documented path is an FFmpeg command on the machine with the analog input, exposing a low-latency audio stream that GroovyRelay consumes via `stream_url`.

V1 should recommend HTTP WAV/PCM as the default remote producer because it is easy to inspect, works with stock FFmpeg, avoids remote command execution, and is good enough for visualizer-first latency on a wired LAN. RTP/UDP can remain an advanced later option after it has real firewall and jitter testing.

Example producer patterns to validate and document during implementation:

```bash
# Linux / ALSA example producer on the machine with line-in.
ffmpeg -f alsa -thread_queue_size 64 -sample_rate 48000 -channels 2 -i hw:1,0 \
  -vn -ac 2 -ar 48000 -f wav -listen 1 http://0.0.0.0:8090/aux.wav

# Windows / DirectShow shape. Device names are examples; operators list them
# with: ffmpeg -list_devices true -f dshow -i dummy
ffmpeg -f dshow -thread_queue_size 64 -audio_buffer_size 50 \
  -i audio="Line In (USB Audio Device)" \
  -vn -ac 2 -ar 48000 -f wav -listen 1 http://0.0.0.0:8090/aux.wav
```

The exact commands may change after real-device validation, but the spec locks the v1 operator story: GroovyRelay consumes `http://capture-host:8090/aux.wav`; the capture machine owns OS-specific line-in capture.

The relay should only consume the configured URL. It should not SSH into remote machines or execute arbitrary remote commands.

## Latency Behavior

The visual reaction target is "as close to the real audio as practical." V1 should use low-latency FFmpeg options where they are known to help, such as smaller analyze/probe windows and reduced buffering. It should avoid brittle flags that frequently break device compatibility.

`visual_only` is the default because the original analog source can keep feeding the user's real listening chain. `monitor` mode sends captured PCM to the MiSTer, but operators should expect some latency and possible capture/network jitter.

Validation target: on a wired LAN with the default HTTP WAV/PCM producer and `visual_only` mode, a visible transient in the analog input should appear in the CRT visualizer within roughly 250 ms. This is a manual/diagnostic target rather than a hard automated test in v1, because capture devices and FFmpeg demuxers vary by platform.

## Security

`stream_url` must use explicit validation plus the existing media input policy machinery. Policy flags alone are not a URL validator.

V1 allowed URL shape:

- Scheme: `http` or `https` only.
- Host: must parse as a host or IP address; empty hosts are rejected.
- Userinfo is rejected.
- Fragment is rejected.
- Redirects are not allowed. The relay-local proxy enforces this by refusing 3xx responses instead of following them.
- FFmpeg consumes only the relay-local proxy URL. Default policy for that URL: `ProtocolWhitelist=["http","tcp"]`, `DisableReconnect=true`, bounded `RWTimeout`, and no input headers.
- `file`, `udp`, `rtp`, and `srt` stream URLs are out of v1 unless a later spec adds source-specific validation and operator-facing firewall guidance.

`local_capture` config must never be a shell command. It is parsed into explicit argv tokens. The adapter should reject suspicious empty or malformed capture fields early, but it does not need to sanitize for shell metacharacters because no shell is involved.

The receiver POST routes use the same same-origin protection as existing chassis mutation endpoints.

## Error Handling

- Disabled or unconfigured AUX: UI disabled; POST returns a clear `422` JSON error.
- Local capture cannot open: adapter reports an error state and the user sees a clear AUX input failure.
- Remote stream unreachable: map probe/open failures to a clear "AUX input unreachable" class of error.
- Unsupported FFmpeg capture format/device: surface the FFmpeg start failure without exposing sensitive local paths beyond the configured device name.
- Active cast replacement: `AUX` should preempt only after validation and probing have succeeded enough to start the new session, preserving the existing `core.Manager` pattern of probing before acquiring the session lock.
- Stop route ownership: an AUX stop request that no longer matches the active adapter ref/generation returns success-or-noop without stopping a foreign session.

## Testing

Unit tests:

- AUX config defaults and validation.
- Session request input validation rejects both-set (`StreamURL` plus `AudioCapture.Enabled`) and neither-set inputs.
- Session request construction for `stream_url` and `local_capture`.
- `visual_only` sets the FFmpeg audio-suppression flag.
- `visual_only` also disables data-plane audio advertisement (`AudioRateOff`, zero channels) and audio pipe reading.
- `monitor` preserves normal PCM output in both FFmpeg argv and data-plane INIT.
- Input-shaped probing supports both relay-local stream URL and local capture specs; local capture probe uses a default bounded timeout of 3 seconds and accepts zero duration.
- FFmpeg argv generation for local capture uses structured args in the right order.
- FFmpeg argv generation for stream URL applies media input policy and low-latency options in the right place.
- Stream URL validation rejects unsupported schemes, userinfo, empty host, and fragments; the stream proxy rejects redirects by refusing 3xx responses.
- Stream proxy rejects 3xx and non-2xx upstream responses, uses no operator-provided headers, and tears down the upstream response on session stop.
- Receiver AUX start/stop route method checks, same-origin failures, malformed input, disabled/unconfigured errors, success, and foreign-session stop no-op.
- Source cluster template/CSS tests cover the added AUX control at desktop and mobile widths.

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
- AUX start preempts the active cast only after start validation succeeds.
- Disabled/unconfigured/unreachable states produce clear UI and JSON errors.
- Tests cover config, argv generation, route behavior, session construction, and audio-output suppression.
- README, `internal/config/example.toml`, and operator docs include the AUX config shape and the validated remote FFmpeg producer command.
