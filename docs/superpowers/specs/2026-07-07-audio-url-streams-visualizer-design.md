# Audio URL, Streams, and Local Files Visualizer Design

Date: 2026-07-07

## Goal

Audio-only media that enters the bridge through pasted URLs, yt-dlp-backed
pages, Streams/user-provider entries, and the Local Files browser should play
on the CRT through the existing music visualizer path.

The feature should make SoundCloud, Bandcamp, direct audio URLs, audio-only
stream entries, and local audio files feel like first-class cast sources while
preserving existing video behavior.

## Scope

In scope:

- URL adapter casts resolved through yt-dlp, including SoundCloud and Bandcamp.
- URL adapter direct HTTP(S) casts that point to audio-only media.
- Streams adapter bundled items and user-provider entries, including both
  yt-dlp-resolved items and direct media/HLS items.
- Local Files browser casts for supported audio extensions.
- Metadata-only visualizer sessions. Title, source/channel, and duration should
  be populated when available.

Out of scope:

- Fetching yt-dlp thumbnails for cover visualizer modes.
- Extracting embedded album art from local files.
- New UI mode switches for "play as audio".
- Changing core to automatically reinterpret every adapter's video request.
- Expanding the live-HLS buffer to support audio-only playlists. Clear
  audio-only HLS should bypass the buffer and play through the visualizer path.

Cover visualizer modes (`cover_vu`, `cover_spectrum`) will continue to work
using the existing placeholder when no artwork path is supplied.

## Existing Behavior

Plex and Jellyfin already start CRT visualizer sessions for audio-only music by
setting:

- `core.SessionRequest.MediaKind = core.MediaKindMusic`
- `core.SessionRequest.Visualizer.Enabled = true`
- visualizer metadata such as title, artist, album, duration, and artwork path

Core validates the request, applies the configured global visualizer mode, and
passes a `ffmpeg.VisualizerSpec` into the pipeline. FFmpeg then generates video
from the audio input.

Local Files already has most of the desired behavior: it accepts common audio
extensions, probes browse entries for `AudioOnly`, and `Cast` starts a music
visualizer session when ffprobe reports audio with no video.

The URL and Streams adapters already resolve yt-dlp pages and support DASH
video+audio handoff, but they currently default resolved media to video unless
another path explicitly overrides `MediaKind`.

## Approach

Use adapter-side classification with a shared helper.

Adapters remain responsible for translating their source-specific result into a
clear `core.SessionRequest`. Core should not silently convert every audio-only
probe into a visualizer session because that would change semantics for adapters
outside this feature.

Add a small helper in the adapter layer that can apply the visualizer shape to a
session request when a source is known to be audio-only. The intended home is
`internal/adapters`, for example:

```go
type AudioOnlyVisualizerMetadata struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
}

func ApplyAudioOnlyVisualizer(req *core.SessionRequest, meta AudioOnlyVisualizerMetadata)
```

The exact helper name can be finalized during implementation, but the behavior
must be shared by URL, Streams, and Local Files where practical. The helper
should:

- set `MediaKind` to `core.MediaKindMusic`
- set `Visualizer.Enabled`
- set the default request mode to `core.VisualizerModeRetroAnalyzer`
- populate `Visualizer.Metadata.Title`
- populate `Visualizer.Metadata.Artist` and `.Album` from source context when
  available
- populate `Visualizer.Metadata.Duration` when known
- leave `ArtworkPath` empty for this pass

Core will still normalize the actual visualizer mode from bridge config at
session start, matching Plex/Jellyfin behavior.

## Classification Contract

Audio-only means the source has at least one audio stream and no video stream.

Classification sources, in priority order:

1. yt-dlp codec metadata when it is explicit.
2. A bounded ffprobe fallback when yt-dlp metadata is ambiguous or the source is
   a direct URL.
3. Existing video fallback when classification cannot complete.

The classifier should return one of three values: `video`, `audio-only`, or
`unknown`. Only `audio-only` may trigger the visualizer helper.

Codec strings must be normalized with trim + lowercase. yt-dlp uses `"none"`
as the explicit sentinel for a missing stream; ffprobe-derived missing values
usually arrive as empty strings.

Truth table for a single stream or single requested format:

| vcodec | acodec | Result |
| --- | --- | --- |
| non-empty and not `none` | any value | `video` |
| `none` | non-empty and not `none` | `audio-only` |
| `none` | `none` or empty | `unknown` |
| empty | any value | `unknown` |

Missing codec fields are unknown, not audio-only. This preserves video playback
for extractors that omit codec hints.

yt-dlp resolver results should expose enough media shape to make this decision:

- dual requested formats with video-only plus audio-only remain video
- top-level or single-format `vcodec` plus `acodec` can classify single-stream
  results
- `duration` should be parsed for visualizer progress when present
- title/channel/uploader/upload date continue to feed display metadata

For direct URLs, the URL adapter should do a short ffprobe before start. If it
sees audio and no video, the adapter starts a visualizer session. If the probe
sees video or fails, the adapter keeps the current video path.

For Streams, classification happens after item resolution and before queue start.
If the item is audio-only, Streams still owns queue state, metadata, next/prev,
and failure advancement.

For Local Files, the existing `isAudioOnlyProbe` behavior should remain. If a
shared helper removes duplication without expanding scope, Local Files can use
it; otherwise its current behavior is acceptable.

## Probe Contract

Classification probes must be bounded, non-fatal, and use the same trust posture
as the media path they are checking.

- URL and Streams adapters should receive the shared `ffprobe` resolver in
  their adapter config, matching Local Files' existing `FFprobe` wiring. Tests
  should keep a probe function seam so classification can be exercised without
  spawning a real binary.
- Default timeout for classification probes should be short enough to avoid
  making casts feel stuck. Use the existing Local Files `800ms` timeout as the
  starting point unless implementation discovers it is too short for direct
  network URLs; any longer timeout must be explicitly justified in the plan.
- Probe errors, missing `ffprobe`, context cancellation, and timeouts are
  non-fatal classification failures. Log at debug or warning level with redacted
  URLs, then continue through the existing video path.
- Probe policy must match the source:
  - Local Files use `localFilePolicy()`.
  - Streams user-provider direct items use `userDirectInputPolicy()` after the
    existing redirect/host prevalidation returns the final URL.
  - Streams bundled direct items use the same policy they would hand to FFmpeg
    if the item were not buffered.
  - URL direct public media uses the same policy the URL adapter would hand to
    core for that cast. For current non-buffered URL direct casts this is the
    zero-value policy.
  - Buffered HLS probe-before-buffer uses the original URL and the policy that
    would be used if the buffer were bypassed.
- Probe headers should follow the media input headers. Extend
  `ffmpeg.ProbeInputSpec` with headers if needed so URL/Streams can probe
  yt-dlp-resolved media that requires `User-Agent`, `Referer`, or similar
  sanitized headers. Headers must pass through the same policy filtering used
  before FFmpeg execution.
- Core's normal start probe should also receive filtered input headers for the
  primary input. Otherwise a header-dependent audio-only URL could classify
  correctly, then fail core's "visualizer source has no audio" gate.

## URL Adapter Flow

For yt-dlp casts:

1. Validate the submitted URL and route as today.
2. Resolve with yt-dlp.
3. Build the base `core.SessionRequest` exactly as today for URL, headers,
   audio input, capabilities, source, title, display metadata, and direct-play
   behavior.
4. Classify the resolved media.
5. If audio-only, apply the visualizer helper.
6. Start the session.

For direct HTTP(S) casts:

1. Validate and optionally resolve Owncast behavior as today.
2. For direct `.m3u8` URLs, run a bounded probe against the original URL before
   opening the HLS buffer.
3. If that probe clearly reports audio-only media, skip the HLS buffer and build
   a direct visualizer request against the original URL.
4. If the `.m3u8` probe reports video or fails, continue through the existing
   HLS buffer path.
5. For other direct HTTP(S) URLs, build the base request, run a bounded probe
   against the playback URL, and apply the visualizer helper only when the probe
   clearly reports audio-only media.
6. If probing fails, start as video to preserve today's behavior.

Direct HLS buffering keeps its current video-oriented rules. Audio-only HLS
support comes from routing clear audio-only playlists around that buffer rather
than teaching the buffer to cache audio-only media.

## Streams Flow

For yt-dlp-resolved queue items:

1. Preserve the existing queue selection and item-resolution lifecycle.
2. Resolve the item through yt-dlp.
3. Revalidate user-provider resolved URLs as today.
4. Build the base `core.SessionRequest` with Streams source, adapter ref,
   headers, queue metadata, controls, and display rows.
5. Classify the resolved media.
6. If audio-only, apply the visualizer helper.
7. Start the session through the existing guarded starter.

Audio-only item failures should use the same error paths as other item start
failures: item-level failures can advance to the next queue item, while global
resolver/tool failures stop the queue.

For direct bundled and user-provider queue items:

1. Preserve the existing direct-item path and queue lifecycle.
2. For user-provider direct items, run the existing redirect/host prevalidation
   before any classification probe.
3. For direct `.m3u8` items that would normally open the Streams HLS buffer, run
   the bounded classification probe before opening the buffer.
4. If the probe clearly reports audio-only media, skip the HLS buffer and build
   a direct visualizer request against the validated original/final URL.
5. If the probe reports video, is unknown, or fails, continue through the
   existing direct video/HLS buffer path.
6. For non-HLS direct media items, run the same bounded classification probe
   against the validated playback URL before starting core.
7. Preserve current direct-item capabilities. Bundled direct streams that
   currently disallow pause/seek should still disallow pause/seek when
   visualized.
8. Preserve queue advancement and cleanup semantics, including HLS buffer
   cleanup when the video path opens a buffer.

## Local Files Flow

Local Files should continue to:

- show common audio extensions as playable
- probe browse entries for duration and audio-only status
- start a music visualizer session when a cast file probes as audio-only
- fall back to the video path if probing fails

The browser UI can continue showing duration in seconds. A visual distinction
for audio-only entries is not required for this feature because cast behavior is
automatic.

## Error Handling

Audio detection must not make video casting more fragile.

- Clear audio-only metadata starts a visualizer session.
- Clear video metadata starts the existing video session.
- Ambiguous metadata falls back to a bounded probe.
- Probe failure or timeout falls back to the existing video path.
- If core rejects a visualizer request because the source has no audio, surface
  the existing core error.
- Streams item-level failures continue to advance when possible.
- Credential-bearing URLs and resolver errors must stay redacted in logs and
  HTTP responses using existing URL adapter redaction.

## Metadata

Minimum metadata:

- Title: yt-dlp title, stream item title, direct URL basename, or local file
  basename.
- Secondary row: channel/uploader for URL; provider/channel context for Streams;
  library name for Local Files.
- Tertiary row: upload date or other existing adapter detail where available.
- Duration: yt-dlp duration or ffprobe duration when known.
- Visualizer metadata mapping:
  - URL: `Title` from resolved/direct title, `Artist` from channel/uploader,
    `Album` empty unless yt-dlp exposes a stable album field.
  - Streams: `Title` from item/resolved title, `Artist` from channel name,
    `Album` from provider name.
  - Local Files: `Title` from basename, `Artist` empty, `Album` from library
    name.

Artist and album can be carried if yt-dlp exposes stable fields, but this
feature does not depend on them.

Artwork path stays empty.

## Tests

Unit tests:

- yt-dlp resolver parses single-stream `vcodec`, `acodec`, and `duration`.
- yt-dlp dual-stream YouTube-style results remain video.
- yt-dlp single audio-only fixtures classify as music.
- Missing or empty yt-dlp codec fields classify as unknown and fall back safely.
- URL adapter starts `MediaKindMusic` with `Visualizer.Enabled` for
  SoundCloud/Bandcamp-like resolver output.
- URL adapter keeps video behavior for dual-stream and progressive video.
- URL adapter starts a visualizer for direct audio URLs when the probe reports
  audio/no video.
- URL adapter falls back to video when direct probing fails.
- URL adapter skips the HLS buffer for direct audio-only `.m3u8` when the probe
  reports audio/no video.
- URL adapter uses the existing HLS buffer for direct `.m3u8` when the probe
  reports video, is unknown, or fails.
- Streams starts a visualizer for audio-only resolved items while preserving
  queue metadata and controls.
- Streams starts a visualizer for direct bundled and user-provider audio-only
  items while preserving queue metadata, security validation, and controls.
- Streams direct `.m3u8` audio-only skips the HLS buffer; direct video/probe
  failure keeps the existing HLS buffer path.
- Streams video items remain unchanged.
- Streams audio-only start failures advance like other item failures.
- Local Files existing audio visualizer tests remain passing; add helper tests if
  shared request shaping is extracted.

Integration tests:

- Reuse the existing FFmpeg visualizer smoke coverage.
- Add an integration smoke only if it can use local fixtures or stubs. Do not add
  a real SoundCloud/Bandcamp network test.

## Acceptance Criteria

- Pasting a SoundCloud or Bandcamp URL with yt-dlp enabled starts CRT visualizer
  playback when yt-dlp resolves audio-only media.
- Pasting a direct `.mp3`, `.flac`, `.ogg`, `.opus`, `.m4a`, `.aac`, or `.wav`
  URL starts CRT visualizer playback when probing confirms audio-only media.
- Pasting a direct audio-only `.m3u8` URL starts CRT visualizer playback when
  probing confirms audio-only media, without using the HLS buffer.
- Streams/user-provider audio-only items start CRT visualizer playback without
  losing queue behavior.
- Local Files audio casts through the browser continue to start CRT visualizer
  playback.
- Existing URL, Streams, and Local Files video tests keep passing.
- No new artwork fetching is introduced.
