# Live HLS Buffering Design

Date: 2026-05-14
Status: Design approved; pending implementation plan

## Context

The bridge can already play HLS `.m3u8` inputs by handing the manifest URL directly to FFmpeg. This is simple, but live remote streams can stall when playlist reloads or segment downloads arrive unevenly. Toonami Aftermath exposed this problem: video can freeze, then audio may resume out of sync because the dataplane duplicates video fields during underrun while audio can continue advancing.

The existing dataplane startup prebuffer holds decoded raw frames for about 100 ms by default. That helps FFmpeg warm-up choppiness, but it is not an HLS segment buffer and does not protect against live playlist or segment fetch jitter. The existing DLNA HLS cache validates and rewrites finite playlists, but it explicitly rejects live playlists and is tied to DLNA's stricter untrusted-LAN policy. This feature needs a new rolling live HLS buffer for the playback paths that are most likely to point at remote live video.

## Scope

Include:

- Bundled Streams direct HLS entries, currently Toonami Aftermath.
- URL adapter direct pasted `.m3u8` casts.
- Default-on behavior for eligible inputs.
- Stability-first live delay.
- A simple CRT-visible `BUFFERING...` slate while startup warmup or rebuffering is in progress.

Exclude for this pass:

- Plex transcode `.m3u8` URLs.
- Jellyfin transcode `.m3u8` URLs.
- DLNA HLS playback.
- yt-dlp resolved HLS outputs, unless a later design opts them in.
- A general IPTV/M3U importer.
- Full HLS feature coverage for encrypted streams, alternate audio renditions, byte ranges, discontinuities, low-latency HLS parts, or DRM.

## Goals

- Feed FFmpeg from a local, warmed HLS source instead of directly from live remote manifests.
- Smooth brief source/network jitter by staying behind the live edge and prefetching segments.
- Keep audio and video from drifting further apart when the local buffer still runs dry.
- Make default behavior better for remote live `.m3u8` casts without changing local media-server transcode behavior.
- Keep the new fetcher observable with cache-depth and segment-timing logs.
- Preserve a quick escape hatch for operators if a stream uses unsupported HLS features.

## Non-Goals

- Zero-latency live playback.
- Perfect recovery from a source that stops publishing new segments.
- Rewriting the FFmpeg pipeline or replacing FFmpeg's demuxer generally.
- Solving UDP loss, MiSTer receiver stalls, or host CPU starvation. Those are separate dataplane problems.
- Rendering rich CRT UI. The first slate is only centered `BUFFERING...` on black.

## Decisions

| Topic | Decision |
|---|---|
| Default behavior | Enable buffering by default for eligible Streams and URL direct `.m3u8` inputs |
| Live delay | Favor stability over latency |
| Initial target | Start around 3 HLS segments behind the live edge |
| Cache size | Keep a small rolling cache, initially about 6 segments |
| TV message | Show simple `BUFFERING...` |
| Excluded adapters | Leave Plex, Jellyfin, DLNA, and yt-dlp HLS out for v1 |
| Unsupported HLS | Fail clearly or bypass via kill switch rather than partially support risky variants |

## Architecture

Add a shared internal HLS buffering package, tentatively `internal/hlsbuffer`.

The package owns these responsibilities:

- Parse and reload HLS playlists.
- Select the active media playlist from a simple master playlist.
- Start playback behind the live edge.
- Prefetch upcoming media segments.
- Store a bounded rolling cache on disk under the bridge data directory or a session temp directory.
- Maintain a local rewritten playlist file and local segment files for FFmpeg, using atomic replace for playlist updates so FFmpeg never observes a partial playlist.
- Track cache depth, segment download duration, playlist reload count, stale playlist count, and failure reason.
- Clean up session files when playback stops.

The package must not know about Streams or URL adapter UI. Adapters request a buffered input and receive:

- local playback path for FFmpeg;
- cleanup function;
- status callback or stats snapshot for logs and future UI;
- original URL metadata for redacted logging.

Core `SessionRequest` should remain the handoff to the manager. The adapters can replace `StreamURL` with the local HLS buffer playlist path and keep the original source in adapter state for replay/history.

## Eligibility

Streams adapter:

- Direct HLS stream items marked trusted by bundled definitions are eligible.
- Toonami Aftermath direct HLS channels should use the buffer by default.
- Future bundled direct HLS providers may opt in through the same trusted path.

URL adapter:

- Direct mode or auto mode that resolves to direct playback is eligible when the original URL path looks like `.m3u8` or the response/content probe identifies HLS.
- Generic URL `.m3u8` buffering is default-on for public remote HTTP(S) URLs.
- Private or local network URLs should initially bypass to the old direct-FFmpeg path unless explicitly enabled later. This avoids expanding the URL adapter into a LAN proxy surface by accident.

Excluded:

- Plex and Jellyfin transcode URLs are not eligible. Their servers already manage transcoding and session lifecycle, and extra buffering can make timeline and resume behavior less predictable.
- DLNA keeps its current finite-HLS cache and validation behavior.
- yt-dlp HLS remains unchanged in v1 because resolved media may require headers, cookies, expiring query params, or site-specific behavior.

## Configuration

Add conservative config fields under a shared bridge section because the feature is consumed by more than one adapter:

```toml
[bridge.hls_buffer]
hls_buffer_enabled = true
hls_live_edge_segments = 3
hls_start_segments = 2
hls_max_cached_segments = 6
hls_segment_timeout_seconds = 10
```

Provide an environment kill switch for diagnostics and rollback:

```text
GROOVY_HLS_BUFFER=0
```

When disabled, eligible casts should use the existing direct FFmpeg path.

## Data Flow

Startup for an eligible HLS cast:

1. Adapter receives a trusted Streams direct HLS item or URL direct `.m3u8`.
2. Adapter asks `hlsbuffer` to open a session for the source URL.
3. `hlsbuffer` validates the top-level URL for the adapter trust level.
4. `hlsbuffer` loads the playlist, follows one supported master-to-media playlist path when needed, and starts around `hls_live_edge_segments` behind the live edge.
5. `hlsbuffer` prefetches until at least `hls_start_segments` are available locally.
6. Adapter starts `core.Manager` with `StreamURL` pointing to the local rewritten playlist file and a local-only media input policy for FFmpeg.
7. FFmpeg reads the local playlist and local segment files.
8. `hlsbuffer` continues playlist reload and segment prefetch in the background.
9. On stop/preempt/error, adapter calls the cleanup function. If core rejects the start, the guarded start loses its session, or another cast preempts during warmup, the adapter must clean up the opened buffer immediately.

Mid-playback:

- If remote segment fetches are briefly slow, FFmpeg continues reading cached local segments.
- If the local cache runs dry, FFmpeg may starve. The dataplane should treat this as a rebuffer condition, show `BUFFERING...`, and hold audio rather than continuing to advance audio alone.
- When decoded frames resume, normal video/audio output resumes.

## Fetching And Rewriting

Supported HLS v1 subset:

- `#EXTM3U`
- live media playlists without `#EXT-X-ENDLIST`
- finite media playlists if encountered through eligible URL direct casts
- simple master playlists with one selected variant
- relative and absolute segment URIs
- ordinary MPEG-TS or simple audio segment resources such as `.ts`, `.aac`, or `.mp3` after validation

Unsupported in v1:

- `#EXT-X-KEY`
- `#EXT-X-BYTERANGE`
- `#EXT-X-DISCONTINUITY`
- `#EXT-X-MAP` and fragmented MP4 streams that require initialization segments
- alternate media playlists via `#EXT-X-MEDIA:URI=...`
- multiple audio/subtitle renditions
- low-latency HLS parts/preload hints
- unknown URI-bearing tags

When unsupported tags appear, behavior should be deterministic:

- For bundled Streams, fail the cast clearly so the provider can be fixed or opted out.
- For URL adapter direct `.m3u8`, either fail with a useful message or fall back to direct FFmpeg only if the fallback does not bypass a security restriction. The first implementation should prefer clear failure plus kill switch over silent partial support.

## Security

Two trust levels are needed.

Bundled Streams HLS:

- URLs come from compiled bundled definitions.
- Toonami Aftermath remains constrained to the known host and approved paths.
- Child playlist and segment fetches still need provider-specific validation, redirect limits, timeouts, and byte limits. For Toonami Aftermath v1, child resources should remain on the known host and inside the expected channel path prefix unless an explicit provider allowlist says otherwise.

Generic URL HLS:

- Top-level URL must be HTTP or HTTPS.
- Userinfo is rejected.
- Public remote URLs are eligible by default.
- Loopback, link-local, metadata, private LAN, and other local addresses should bypass buffering in v1 unless explicitly enabled later.
- Child media playlist URLs and segment URLs must pass the same public-remote validation before fetch. A public top-level playlist must not be able to point the buffer at loopback, link-local, metadata, private LAN, `file:`, or other local resources.
- Redirects must be chased by our fetcher with validation at each hop.
- Request headers should be minimal. Do not forward cookies, authorization, proxy authorization, or referer headers through the buffer.
- Segment and playlist byte limits must be enforced.
- Cache filenames must be generated, not derived directly from remote paths.
- Local playlist output must not contain remote URLs.
- Buffered sessions should pass a local-only `MediaInputPolicy` to core/FFmpeg, for example a protocol whitelist limited to local file access, so a rewrite bug cannot make FFmpeg fetch remote playlist children directly.

The local rolling cache must not evict any segment still referenced by the playlist FFmpeg can read. Eviction should happen only after a segment has aged out of the published local playlist plus a small grace window, or after the session has stopped.

The buffer should reduce FFmpeg's direct remote fetching surface for eligible public HLS because FFmpeg reads local rewritten resources. It should not create a broader SSRF surface.

## BUFFERING Slate

Add a simple generated video slate in the dataplane:

- black background;
- centered ASCII `BUFFERING...`;
- small built-in bitmap font or simple block text renderer;
- generated as BGR24 in the active output dimensions;
- sent through the normal Groovy BLIT path.

Mid-playback behavior:

- After a short video underrun threshold, for example 500 ms, switch from duplicate-field BLITs to the generated buffering slate.
- While the slate is active, hold audio output so audio does not drift ahead of video.
- When real frames resume, return to normal output and resume audio.

Startup behavior:

- If HLS cache warmup happens before FFmpeg and dataplane startup, a startup slate requires a lightweight pre-roll sender or a temporary dataplane state before FFmpeg is ready.
- Implementing the startup slate can be phase 2 if needed. The first implementation may show the slate only after the dataplane is active, but the design should keep room for startup warmup display.

The slate is deliberately not a rich UI. It should not show provider art, title, progress bars, or cache counts in v1.

## Observability

Log one session-start line for the HLS buffer:

- source adapter;
- redacted source URL;
- selected variant bitrate/resolution if available;
- live edge segment count;
- start segment count;
- cache directory or session id;
- whether buffering was enabled by config or bypassed.

Log periodic or state-change stats:

- cached segment count;
- media seconds cached ahead of FFmpeg;
- playlist reload duration;
- segment download duration;
- stale playlist reloads;
- segment retry count;
- unsupported tag errors;
- cache underrun events.

Existing dataplane logs should continue to report underruns, duplicates, and frame echo stalls. Add enough context to tell whether an underrun happened while the HLS buffer was empty or while FFmpeg/dataplane was otherwise behind.

## Error Handling

- If initial playlist fetch fails, return the adapter's existing playback error path.
- If startup cache warmup cannot reach the start threshold before timeout, either start with partial cache and log warning or fail depending on config. Default should start with partial cache only if at least one valid segment is available.
- If a segment fails once, retry with a bounded count and timeout.
- If a live playlist stops advancing, keep retrying for a bounded stale-playlist window, then surface a playback error or let FFmpeg reach EOF from the local playlist.
- If cleanup fails, log a warning and continue session teardown.
- If the session cache directory or local playlist file cannot be created, fail before starting FFmpeg.

## Testing

Unit tests:

- playlist parser accepts supported simple media playlists;
- playlist parser accepts a simple master playlist and selects a variant;
- parser rejects unsupported tags listed above;
- URL resolver rewrites relative and absolute segment URIs safely;
- bundled Toonami child URLs outside the known host or expected channel path are rejected;
- child media playlist and segment URLs are rejected when they resolve to loopback, link-local, metadata, private LAN, `file:`, or other local resources;
- generated cache filenames cannot escape the cache root;
- public/private URL eligibility matches the security rules;
- local playlist writes are atomic and never reference evicted segment files;
- stale playlist reload and segment retry state transitions are deterministic;
- config defaults enable the buffer.

Integration-style tests with `httptest`:

- URL direct `.m3u8` cast starts core with local buffered URL;
- Streams Toonami direct HLS starts core with local buffered URL;
- Plex/Jellyfin-like transcode URLs are not routed through the HLS buffer;
- a slow segment server is smoothed by prefetched cached segments;
- unsupported HLS fails clearly;
- cleanup removes session files.

Dataplane tests:

- buffering slate generator produces a non-empty BGR24 frame at target dimensions;
- underrun threshold switches from duplicate fields to slate;
- audio is held while slate is active;
- real frames resume normal output after rebuffer.

## Rollout

1. Add shared `hlsbuffer` package and tests.
2. Integrate Streams bundled direct HLS through the buffer.
3. Integrate URL adapter direct pasted `.m3u8` through the buffer.
4. Add logs and kill switch.
5. Add mid-playback `BUFFERING...` slate and audio hold.
6. Consider startup warmup slate after the first buffered playback path is stable.

This order lets the cache prove itself before the dataplane display work grows the blast radius.
