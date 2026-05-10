# Music Visualizer Design

**Date:** 2026-05-10  
**Status:** Design approved; implementation plan not started  
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

Extend `core.SessionRequest` with a small visualizer block:

```go
type VisualizerRequest struct {
    Enabled bool
    Mode    VisualizerMode
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

`Title` on `SessionRequest` remains the short session label for status views. `Visualizer.Metadata` is the render-focused metadata consumed by the FFmpeg pipeline builder.

Rules:

- `Visualizer.Enabled=false` preserves today's behavior.
- `Visualizer.Enabled=true` means the input may be audio-only and FFmpeg must synthesize video.
- V1 supports only `retro_analyzer`; unknown modes are rejected before spawning FFmpeg.
- Empty metadata fields are allowed. The renderer falls back to `Title`, then `Now Playing`.
- Duration may be zero when the server does not provide it; the visualizer omits duration/progress in that case.

## FFmpeg Pipeline

Add visualizer fields to `ffmpeg.PipelineSpec` mirroring the core request. `BuildCommand` keeps the existing video path when visualizer is disabled. When enabled, it builds a filter graph that consumes audio and produces video.

V1 filter behavior:

- Use the audio stream as input `0:a:0`.
- Output audio as s16le PCM using the same sample rate and channel config as normal sessions.
- Generate video with FFmpeg audio visualization filters such as `showfreqs` or another built-in spectrum filter selected during implementation testing.
- Size the generated video to the active modeline's full frame size, for example `720x480` or `720x576`.
- Convert to `bgr24` rawvideo for the existing video pipe.
- Overlay text using `drawtext` only if the bundled FFmpeg supports it.
- Escape metadata before injecting it into filter arguments.

If `drawtext` support is unavailable in a bundled or operator-provided FFmpeg, v1 should still play with spectrum bars and no metadata overlay. The status UI and adapter timeline still carry metadata in that case. Implementation planning should add a lightweight capability check or a graceful command-builder fallback rather than making text rendering a hard dependency.

The command shape is conceptually:

```text
ffmpeg -i <audio-url>
  -filter_complex "<audio to spectrum video>,<metadata text>,<format/scale>"
  -map "[visualizer_video]" -pix_fmt bgr24 -f rawvideo <video-pipe>
  -map 0:a:0 -ar <rate> -ac <channels> -f s16le <audio-pipe>
```

The exact filter chain should be verified against the sidecar FFmpeg during implementation. The design intent is stable: audio in, two outputs out, no Go-side frame synthesis.

## Plex Adapter

Plex currently builds video transcode URLs through `/video/:/transcode/universal/start.ts` and reports live state on the video timeline while music/photo timelines are stopped. Music visualizer support adds a music branch.

Detection:

- Treat a cast as music when the play request or fetched PMS metadata identifies the item as audio/music.
- If the initial play request lacks media type or artist/album/duration, fetch PMS metadata for the media key.
- If detection is ambiguous, prefer the existing video path. The visualizer path should be used only when the adapter can identify the item as music/audio.

Stream negotiation:

- Build an audio-consumable PMS stream/transcode URL for the selected item.
- Preserve seek offset and selected audio stream where Plex provides one.
- Keep adapter capabilities `CanSeek=true` and `CanPause=true` when PMS can honor them.

Metadata:

- Capture title, artist, album, and duration from PMS metadata.
- Fallback order for title: play request title, PMS title, media key basename, `Now Playing`.
- Artist and album may be blank without failing playback.

Timeline:

- For visualizer sessions, Plex timeline XML should report the `music` timeline as playing/paused and the `video` timeline as stopped.
- Existing video sessions continue reporting on the `video` timeline.
- Stop/preempt behavior must still notify Plex controllers promptly and stop PMS transcode sessions where applicable.

## Jellyfin Adapter

Jellyfin currently advertises playable media types including `Audio`, but its playback profile and session construction are video-oriented. Music visualizer support adds an audio path while keeping the existing video path unchanged.

Detection:

- Treat a websocket `Play` message as music when item metadata or PlaybackInfo identifies the item as audio.
- Fetch item metadata when PlaybackInfo does not include enough title/artist/album/duration information.
- If detection is ambiguous, use the current video path.

Stream negotiation:

- Request an audio-capable PlaybackInfo response for audio items.
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

When config is eventually added, the settings UI should render it as a small bridge-level section, not as a new adapter. No visualizer preview is required in v1.

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
- FFmpeg command tests for:
  - existing video command shape unchanged;
  - visualizer command maps audio to PCM output;
  - visualizer command creates raw BGR video output;
  - visualizer command rejects unknown modes;
  - metadata escaping handles punctuation-heavy titles.
- Plex adapter tests for:
  - music detection from metadata;
  - fallback to video path when media type is ambiguous;
  - title/artist/album/duration extraction;
  - music timeline playing while video timeline is stopped.
- Jellyfin adapter tests for:
  - audio item detection;
  - audio PlaybackInfo request construction;
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
- Confirm Plex's best audio stream endpoint for music items and how Plexamp populates Companion play requests.
- Confirm Jellyfin audio PlaybackInfo response shape for music items across the supported server version range.
