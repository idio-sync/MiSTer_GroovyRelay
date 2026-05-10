# Music Visualizer Design

**Date:** 2026-05-10  
**Status:** Review fixes applied; implementation plan not started  
**Scope:** Add CRT music visualization for audio-only Plex and Jellyfin casts, starting with a Retro Analyzer mode rendered through the existing MiSTer data plane.

## Problem

GroovyRelay can cast video from Plex, Jellyfin, URL sources, DLNA, and catalog-backed stream providers. It does not currently handle a natural music-cast flow: selecting a song, album, or playlist in Plex/Plexamp/Jellyfin and seeing a visual output on the CRT while audio plays through the MiSTer.

The current playback pipeline expects FFmpeg to provide a video stream. Audio-only music items can probe successfully for audio, but the command builder still maps `0:v:0`, so a true music item cannot become a normal GroovyRelay session without synthesizing video.

## Goals

- Support true audio-only music items cast from Plex and Jellyfin in v1.
- Render the v1 visualizer on the CRT through the MiSTer, not only in the web UI.
- Generate a Retro Analyzer visual style first: black background, green spectrum bars, rich now-playing text, elapsed time, duration, and a simple progress indication.
- Keep the MiSTer/Groovy data plane unchanged by making FFmpeg output the same raw BGR video pipe and s16le PCM audio pipe it outputs for video sessions today.
- Preserve normal video-cast behavior and command shape.
- Keep adapter ownership clear: Plex and Jellyfin detect music, fetch metadata, negotiate streams, and report timeline/control state.
- Leave clean design room for later visualizer modes: oscilloscope glow and album-art console.

## Non-Goals

- Web UI visualizer rendering in v1.
- Album art rendering in v1.
- Oscilloscope or waveform rendering in v1.
- Beat detection, scene changes, shaders, or custom Go-side video generation.
- DLNA, URL, or Streams music visualizer support in the first pass.
- A user-editable visualizer theme system in v1.
- Replacing the existing FFmpeg video pipeline for normal video casts.
- Changing the Groovy_MiSTer UDP protocol or dataplane timing model.

## Decisions Captured During Brainstorm

| Topic | Decision |
|---|---|
| Display target | CRT through the MiSTer |
| V1 style | Retro Analyzer |
| Later modes | Oscilloscope glow, then album-art console |
| Media support | True audio-only Plex and Jellyfin music in v1 |
| Metadata target | Title, artist, album, elapsed time, duration, progress |
| Technical approach | Add an audio visualizer session mode using FFmpeg-generated video |
| MiSTer data plane | Reuse existing raw video and PCM audio pipes |

## Architecture

Add an audio visualizer session flavor to the existing core playback path. This is not a separate adapter. Plex and Jellyfin remain the public cast targets, and they translate music casts into `core.SessionRequest` values that ask core to start an audio visualizer session.

The high-level flow is:

```text
Plex/Jellyfin music cast
        |
        v
adapter detects audio item and fetches metadata
        |
        v
core.SessionRequest with Visualizer enabled
        |
        v
FFmpeg audio input
        |
        +--> s16le PCM audio pipe --> existing dataplane audio path
        |
        +--> spectrum/text video --> raw BGR video pipe --> existing dataplane video path
```

Core still resolves modeline, probes media, builds a pipeline spec, starts a `dataplane.Plane`, and owns session preemption. The only functional branch is in FFmpeg command construction: a visualizer session maps audio to both the audio output and the generated video output.

This keeps the timing-sensitive MiSTer sender boring. Once FFmpeg has synthesized frames, the data plane handles them like any other source.

## Core Request Model

Extend `core.SessionRequest` with a media kind and a small visualizer block:

```go
type MediaKind string

const (
    MediaKindVideo MediaKind = "video"
    MediaKindMusic MediaKind = "music"
)

type VisualizerRequest struct {
    Enabled  bool
    Mode     VisualizerMode
    Metadata VisualizerMetadata
}

type VisualizerMode string

const (
    VisualizerModeRetroAnalyzer VisualizerMode = "retro_analyzer"
)

type VisualizerMetadata struct {
    Title    string
    Artist   string
    Album    string
    Duration time.Duration
}
```

`Title` on `SessionRequest` remains the short session label for status views. `MediaKind` tells status consumers and adapter reporters whether the active session is video or music. `Visualizer.Metadata` is the render-focused metadata consumed by the FFmpeg pipeline builder.

Rules:

- `Visualizer.Enabled=false` preserves today's behavior.
- Empty `MediaKind` is treated as `video` for backward compatibility.
- `Visualizer.Enabled=true` means `MediaKind=music`, the input may be audio-only, and FFmpeg must synthesize video.
- Core rejects requests where `Visualizer.Enabled=true` and `MediaKind` is explicitly set to a non-music value.
- V1 supports only `retro_analyzer`; unknown modes are rejected before spawning FFmpeg.
- Empty metadata fields are allowed. The renderer falls back to `Title`, then `Now Playing`.
- Duration may be zero when the server does not provide it; the visualizer omits duration/progress in that case.

`core.SessionStatus` and `core.StatusHomeView` should carry the same `MediaKind`. `Manager` copies it from the active request when building status. This gives Plex's timeline broker and the web status page a code-owned signal instead of requiring them to infer music sessions from adapter refs or titles.

## Probe Path

Visualizer sessions still run `ffprobe` before acquiring `Manager.mu`, but they must not run crop probing:

- `Probe` runs against `SessionRequest.StreamURL` as usual.
- A visualizer session requires an audio stream. If `ProbeResult.AudioRate <= 0`, core rejects the request before spawning FFmpeg.
- `ProbeCrop` is skipped for visualizer sessions, even when `bridge.video.aspect_mode = "auto"`.
- `CropRect` is always nil for visualizer sessions.
- Duration is resolved from adapter metadata first, then from `ProbeResult.Duration`.
- Video width, height, frame rate, and interlace fields from probe are ignored for visualizer layout. The active modeline controls generated video size and cadence.

This prevents the current video-only crop probe from running against audio-only URLs and preserves the existing video probe/crop behavior for normal casts.

## FFmpeg Pipeline

Add a concrete visualizer block to `ffmpeg.PipelineSpec`:

```go
type VisualizerSpec struct {
    Enabled  bool
    Mode     string
    Metadata VisualizerMetadata
}
```

`ffmpeg` should define package-local visualizer metadata types rather than importing `internal/core`. `core.Manager` maps the core request metadata into `ffmpeg.PipelineSpec` at the existing core-to-FFmpeg boundary.

`BuildCommand` splits into two command shapes:

- **Video sessions:** keep the existing `-map 0:v:0` plus `-vf buildFilterChain(s)` path. The existing `buildFilterChain` remains video-only: `yadif`, `fps`, aspect handling, anamorphic stretch, and subtitles.
- **Visualizer sessions:** do not call `buildFilterChain` and do not pass `-vf`. Use `-filter_complex` to synthesize video from audio and label the generated video pad for `-map`.

The visualizer graph must preserve the existing CRT aspect contract. It should render the analyzer into the square-pixel logical canvas returned by `logicalCanvas(OutputHeight)`, then anamorphic-stretch to `OutputWidth x OutputHeight` before raw output. This mirrors the video path's 4:3-visible CRT behavior instead of treating 720x480 as square pixels.

V1 filter behavior:

- Use the audio stream as input `0:a:0`.
- Output audio as s16le PCM using the same sample rate and channel config as normal sessions.
- Generate video with FFmpeg audio visualization filters such as `showfreqs` or another built-in spectrum filter selected during implementation testing.
- Generate at logical canvas size, then stretch to the active modeline's full frame size, for example `720x480` or `720x576`.
- Terminate the video branch with `fps=<OutputFpsExpr>` so the data plane receives frames at the modeline's field cadence and does not continuously underrun.
- Convert to `bgr24` rawvideo for the existing video pipe.
- Overlay text using `drawtext` only if the bundled FFmpeg supports it.
- Escape metadata before injecting it into filter arguments.

If `drawtext` support is unavailable in a bundled or operator-provided FFmpeg, v1 should still play with spectrum bars and no metadata overlay. The status UI and adapter timeline still carry metadata in that case. Implementation planning should add a lightweight capability check or a graceful command-builder fallback rather than making text rendering a hard dependency.

The command shape is conceptually:

```text
ffmpeg -i <audio-url>
  -filter_complex "[0:a:0]<audio to logical-canvas spectrum>,<drawtext>,fps=<cadence>,scale=<outputW>:<outputH>,format=bgr24[visualizer_video]"
  -map "[visualizer_video]" -pix_fmt bgr24 -f rawvideo <video-pipe>
  -map 0:a:0 -ar <rate> -ac <channels> -f s16le <audio-pipe>
```

The exact filter chain should be verified against the sidecar FFmpeg during implementation. The design intent is stable: audio in, two outputs out, no Go-side frame synthesis.

### Metadata Escaping

Add an `escapeFilterText` helper in the FFmpeg package and test it directly. It should:

- replace ASCII control characters, including CR/LF/TAB, with spaces;
- escape backslash (`\`) before any other character-specific escaping;
- escape single quote (`'`) for single-quoted drawtext text values;
- escape FFmpeg filter separators and expression characters used by this graph: colon (`:`), comma (`,`), semicolon (`;`), percent (`%`), left bracket (`[`), and right bracket (`]`);
- preserve ordinary non-ASCII characters after escaping the ASCII syntax characters above.

No adapter should construct drawtext fragments directly. Adapters pass raw metadata strings; only the FFmpeg package escapes them for filter use.

## Plex Adapter

Plex currently builds video transcode URLs through `/video/:/transcode/universal/start.ts` and reports live state on the video timeline while music/photo timelines are stopped. Music visualizer support adds a music branch.

Detection:

- Treat a cast as music when the play request or fetched PMS metadata identifies the item as audio/music.
- If the initial play request lacks media type or artist/album/duration, fetch PMS metadata for the media key.
- If detection is ambiguous, prefer the existing video path. The visualizer path should be used only when the adapter can identify the item as music/audio.

Stream negotiation:

- Add a separate music transcode builder instead of reusing `BuildTranscodeURL`.
- Target Plex's music transcode endpoint family: `/music/:/transcode/universal/start.mp3`.
- Use audio-oriented query parameters: `path`, `protocol=http`, `directPlay=0`, `directStream=0`, `offset`, `transcodeSessionId`, Plex identity headers/query values, and an explicit audio codec/bitrate profile suitable for FFmpeg input.
- Keep the existing video transcode builder pointed at `/video/:/transcode/universal/start.ts`.
- Preserve seek offset and selected audio stream where Plex provides one.
- Keep adapter capabilities `CanSeek=true` and `CanPause=true` when PMS can honor them.

Metadata:

- Capture title, artist, album, and duration from PMS metadata.
- Fallback order for title: play request title, PMS title, media key basename, `Now Playing`.
- Artist and album may be blank without failing playback.

Timeline:

- For visualizer sessions, Plex timeline XML reports the `music` timeline as playing/paused and the `video` timeline as stopped.
- Existing video sessions continue reporting on the `video` timeline.
- The timeline broker uses `core.SessionStatus.MediaKind` to choose which timeline is live. It must not infer media kind from `PlayMediaRequest.MediaKey`.
- Music timelines carry the same key/ratingKey/container/queue metadata currently attached to video timelines where Plex supplies those values.
- `location` should remain compatible with Plex controllers. If Plex music controllers ignore `fullScreenVideo`, omit the location field for music sessions rather than pretending the session is video.
- Stop/preempt behavior must still notify Plex controllers promptly and stop PMS transcode sessions where applicable.

Queue and track changes:

- Each new Plex music track starts a new core session and a new FFmpeg process.
- Next/previous/skip behavior follows the existing play queue restart pattern, but constructs a music visualizer session instead of a video session.
- Same-track seek restarts the FFmpeg process at the requested offset, as current seek behavior does for video.

## Jellyfin Adapter

Jellyfin currently advertises playable media types including `Audio`, but its playback profile and session construction are video-oriented. Music visualizer support adds an audio path while keeping the existing video path unchanged.

Detection:

- Treat a websocket `Play` message as music when item metadata or PlaybackInfo identifies the item as audio.
- Fetch item metadata when PlaybackInfo does not include enough title/artist/album/duration information.
- If detection is ambiguous, use the current video path.

Stream negotiation:

- Add an audio-capable playback profile for audio items instead of using the current video-only `TranscodingProfile`.
- The audio profile should include a `TranscodingProfile` with `Type: "Audio"`, `Container: "mp3"`, `AudioCodec: "mp3"`, `Protocol: "http"`, `Context: "Streaming"`, and `MaxAudioChannels: "2"`.
- Adjust the profile structs so `VideoCodec` is omitted for audio profiles.
- Keep the existing video profile unchanged for video items.
- Request PlaybackInfo with the audio profile when the item is music/audio.
- Build an absolute FFmpeg-consumable audio stream/transcode URL.
- Preserve start position, seek, pause/resume, and reporting behavior.

Metadata:

- Capture title, artist, album, and duration from Jellyfin item metadata.
- Fallback order for title: item name from PlaybackInfo, media source name, item ID, `Now Playing`.
- Artist and album may be blank without failing playback.

Reporting:

- Continue using Jellyfin playback start/progress/stop reporting.
- Report position from core status as today.
- Include the now-playing queue with the current music item first, matching existing reporter behavior.

Queue and track changes:

- Each new Jellyfin music item starts a new core session and a new FFmpeg process.
- `PlayNext` and `PlayLast` continue to manage the adapter queue. When the current track ends, EOF advancement builds a new audio visualizer session for the next audio item.
- Same-track seek restarts the FFmpeg process at the requested offset, as current seek behavior does for video.

## Session Transitions

Visualizer sessions use the same preemption semantics as every other core session:

- Starting music while video is active preempts the video session and invokes the prior session's `OnStop("preempted")`.
- Starting video while music is active preempts the music session and invokes the prior music cleanup.
- Starting a different music track preempts the prior music track.
- Seeking or resuming the same music track is a same-session replay only when the adapter intentionally keeps the same `AdapterRef`.
- Adapters should include enough identity in `AdapterRef` to distinguish server-side transcode resources that require explicit cleanup. For Plex music, that means including or otherwise tracking `TranscodeSessionID`, not only media key.

## Config And UI

V1 adds no new user-facing config. Visualizer playback is automatic for detected audio-only Plex/Jellyfin music casts. Existing Plex and Jellyfin adapter enable toggles remain the operator control for whether those sources can start sessions.

Future phases can add bridge-level config once there is more than one mode:

```toml
[bridge.music_visualizer]
enabled = true
mode = "retro_analyzer"
```

Apply scopes:

| Field | Scope | Reason |
|---|---|---|
| `music_visualizer.enabled` | `ScopeHotSwap` | Allows blocking new visualizer sessions without changing existing video playback. |
| `music_visualizer.mode` | `ScopeRestartCast` | Affects the FFmpeg graph for future sessions. |

When config is eventually added, `music_visualizer.enabled` is a new-session gate only. It does not live-reconfigure, stop, or restart an already-running FFmpeg visualizer process. The settings UI should render future visualizer fields as a small bridge-level section, not as a new adapter. No visualizer preview is required in v1.

## Spec Location

This spec intentionally lives under `docs/superpowers/specs/` because it was produced through the current Superpowers brainstorming workflow. Older code comments still reference `docs/specs/` for earlier designs. New implementation comments for this feature should reference the actual path: `docs/superpowers/specs/2026-05-10-music-visualizer-design.md`.

## Error Handling

- Metadata fetch failure does not block playback. Use fallback text.
- Missing artist or album does not block playback. Omit blank lines.
- Missing duration does not block playback. Omit duration and progress.
- Audio stream negotiation failure is a playback error owned by the adapter.
- Probe failure is handled like existing source probe failures.
- Visualizer mode validation failure is a core or config error before FFmpeg spawn.
- FFmpeg visualizer startup failure is a normal plane startup error.
- Missing `drawtext` support degrades to bars-only playback if practical.
- Metadata passed to FFmpeg filters must be escaped so quotes, colons, backslashes, brackets, percent signs, and non-ASCII song text cannot break the filter graph.

## Testing

Unit tests:

- Core request/status tests for visualizer fields and default disabled behavior.
- Core tests proving visualizer sessions skip `ProbeCrop` and reject sources without audio.
- FFmpeg command tests for:
  - existing video command shape unchanged;
  - visualizer command uses `-filter_complex`, not `-vf`;
  - visualizer command maps audio to PCM output;
  - visualizer command creates raw BGR video output;
  - visualizer graph includes `fps=<OutputFpsExpr>`;
  - visualizer graph preserves logical-canvas-to-output stretch;
  - visualizer command rejects unknown modes;
  - metadata escaping handles punctuation-heavy titles.
- Plex adapter tests for:
  - music detection from metadata;
  - music transcode URL uses `/music/:/transcode/universal/start.mp3`;
  - fallback to video path when media type is ambiguous;
  - title/artist/album/duration extraction;
  - music timeline playing while video timeline is stopped.
- Jellyfin adapter tests for:
  - audio item detection;
  - audio PlaybackInfo request uses an audio `TranscodingProfile`;
  - metadata extraction and fallback;
  - reporting still starts and advances.

Integration or smoke tests:

- A tiny generated audio fixture can start a visualizer session through the FFmpeg builder without requiring Plex/Jellyfin servers.
- Existing video integration tests continue passing.
- If sidecar FFmpeg is available, a short command smoke test should verify the chosen visualizer filter exists.

## Phasing

### Phase 1: Retro Analyzer

- True audio-only Plex and Jellyfin music casts.
- Spectrum bars rendered on CRT.
- Rich text metadata when FFmpeg supports `drawtext`.
- Pause, resume, seek, stop, timeline/progress reporting.

### Phase 2: Oscilloscope Glow

- Add a second visualizer mode using waveform-style rendering.
- Reuse the same core request and FFmpeg visualizer branch.
- Add config/UI mode selection for choosing between Retro Analyzer and Oscilloscope Glow.

### Phase 3: Album Art Console

- Fetch album art from Plex/Jellyfin.
- Cache artwork per session.
- Render artwork plus now-playing metadata and subtle analyzer motion.
- Keep artwork failures non-fatal with a bars-only fallback.

## Open Implementation Checks

These are verification tasks for the implementation plan, not unresolved product requirements:

- Confirm which FFmpeg visualization filter gives the best Retro Analyzer output on the bundled sidecars.
- Confirm bundled FFmpeg builds include `drawtext`; if not, implement bars-only fallback first.
- Smoke-test `/music/:/transcode/universal/start.mp3` against a live PMS and capture Plexamp Companion request fields.
- Confirm Jellyfin audio PlaybackInfo response shape for music items across the supported server version range.
