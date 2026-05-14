# Live HLS Buffering Design

Date: 2026-05-14
Status: V1 implemented on feature branch; dataplane slate/audio follow-up pending

## Context

The bridge can already play HLS `.m3u8` inputs by handing the manifest URL directly to FFmpeg. This is simple, but live remote streams can stall when playlist reloads or segment downloads arrive unevenly. Toonami Aftermath exposed this problem: video can freeze, then audio may resume out of sync because the dataplane duplicates video fields during underrun while audio can continue advancing.

The existing dataplane startup prebuffer holds decoded raw frames for about 100 ms by default. That helps FFmpeg warm-up choppiness, but it is not an HLS segment buffer and does not protect against live playlist or segment fetch jitter. The existing DLNA HLS cache validates and rewrites finite playlists, but it explicitly rejects live playlists and is tied to DLNA's stricter untrusted-LAN policy. This feature needs a new rolling live HLS buffer for the playback paths that are most likely to point at remote live video.

## Scope

Include in v1:

- Bundled Streams direct HLS entries, currently Toonami Aftermath.
- URL adapter direct pasted `.m3u8` casts.
- Default-on behavior for eligible inputs.
- Stability-first live delay.
- Global kill switch plus per-source or per-cast opt-out for cases where the buffer misidentifies or cannot support a stream.
- Bounded rolling segment cache, adapter wiring, and observability.

Track separately after v1:

- A simple CRT-visible `BUFFERING...` slate while startup warmup or rebuffering is in progress.
- Audio hold or rebuffer-state behavior in the dataplane.

Exclude for v1:

- Plex transcode `.m3u8` URLs.
- Jellyfin transcode `.m3u8` URLs.
- DLNA HLS playback.
- yt-dlp resolved HLS outputs, unless a later design opts them in.
- A general IPTV/M3U importer.
- Audio-only HLS manifests and music visualizer handoff.
- Dataplane slate rendering or audio-hold changes.
- Full HLS feature coverage for encrypted streams, alternate audio renditions, byte ranges, discontinuities, low-latency HLS parts, or DRM.

## Goals

- Feed FFmpeg from a local, warmed HLS source instead of directly from live remote manifests.
- Smooth brief source/network jitter by staying behind the live edge and prefetching segments.
- Reduce how often source jitter reaches the dataplane as a video underrun. Explicit rebuffer state, slate rendering, and audio-hold semantics are phase-2 dataplane work.
- Make default behavior better for remote live `.m3u8` casts without changing local media-server transcode behavior.
- Keep the new fetcher observable with cache-depth and segment-timing logs.
- Preserve a quick escape hatch for operators if a stream uses unsupported HLS features.

## Non-Goals

- Zero-latency live playback.
- Perfect recovery from a source that stops publishing new segments.
- Rewriting the FFmpeg pipeline or replacing FFmpeg's demuxer generally.
- Solving UDP loss, MiSTer receiver stalls, or host CPU starvation. Those are separate dataplane problems.
- Rendering CRT UI or holding audio during rebuffer in v1. The desired first slate is only centered `BUFFERING...` on black, but it needs a separate dataplane design before implementation.

## Decisions

| Topic | Decision |
|---|---|
| Default behavior | Enable buffering by default for eligible Streams and URL direct `.m3u8` inputs |
| Live delay | Favor stability over latency |
| Initial target | Start around 3 HLS segments behind the live edge |
| Cache size | Keep a small rolling cache, initially about 6 segments and 256 MiB, whichever limit is hit first |
| TV message | Defer simple `BUFFERING...` slate to a phase-2 dataplane spec |
| Excluded adapters | Leave Plex, Jellyfin, DLNA, and yt-dlp HLS out for v1 |
| Unsupported HLS | Fail clearly or bypass via kill switch rather than partially support risky variants |

## Architecture

Add a shared internal HLS buffering package, `internal/hlsbuffer`.

The package owns these responsibilities:

- Parse and reload HLS playlists.
- Select the active media playlist from a simple master playlist.
- Start playback behind the live edge.
- Prefetch upcoming media segments.
- Store a bounded rolling cache on disk under the bridge data directory or a session temp directory.
- Maintain a local rewritten playlist file and local segment files for FFmpeg, using atomic replace for playlist updates so FFmpeg never observes a partial playlist.
- Track cache depth, segment download duration, playlist reload count, stale playlist count, and failure reason.
- Reap stale session cache directories left behind by prior bridge exits.
- Clean up session files when playback stops.

The package must not know about Streams or URL adapter UI. Adapters request a buffered input and receive:

- local playback path for FFmpeg;
- cleanup function;
- status callback or stats snapshot for logs and future UI;
- original URL metadata for redacted logging.
- selected variant metadata for status/logs.

Core `SessionRequest` should remain the handoff to the manager. The adapters can replace `StreamURL` with the local HLS buffer playlist path and keep the original source in adapter state for replay/history.

## Eligibility

Streams adapter:

- Direct HLS stream items marked trusted by bundled definitions are eligible.
- Toonami Aftermath direct HLS channels should use the buffer by default.
- A provider or channel override must be able to disable HLS buffering for a bundled direct stream without disabling the whole provider.
- Future bundled direct HLS providers may opt in through the same trusted path.

URL adapter:

- Direct mode or auto mode that resolves to direct playback is eligible when the original URL path ends in `.m3u8`. Response/content probing is deferred.
- Generic URL `.m3u8` buffering is default-on for public remote HTTP(S) URLs.
- The URL play surface must expose an HLS buffering mode, default `auto`, with `off` bypassing the buffer for that cast/history replay.
- Private or local network URLs should initially bypass to the old direct-FFmpeg path unless explicitly enabled later. This avoids expanding the URL adapter into a LAN proxy surface by accident.

Excluded:

- Plex and Jellyfin transcode URLs are not eligible. Their servers already manage transcoding and session lifecycle, and extra buffering can make timeline and resume behavior less predictable.
- DLNA keeps its current finite-HLS cache and validation behavior.
- yt-dlp HLS remains unchanged in v1 because resolved media may require headers, cookies, expiring query params, or site-specific behavior.

## Configuration

Add conservative config fields under a shared bridge section because the feature is consumed by more than one adapter:

```toml
[bridge.hls_buffer]
enabled = true
live_edge_segments = 3
start_segments = 2
max_cached_segments = 6
max_cache_bytes = 268435456
max_playlist_bytes = 1048576
max_segment_bytes = 52428800
segment_timeout_seconds = 10
playlist_timeout_seconds = 10
max_variant_height = 720
stale_cache_reap_hours = 24
```

Bridge field definitions and `scopeForBridgeField` must declare these fields explicitly:

| Field | Default | Validation | ApplyScope |
|---|---:|---|---|
| `hls_buffer.enabled` | `true` | boolean | `ScopeRestartCast` |
| `hls_buffer.live_edge_segments` | `3` | integer in `[1, 12]` and `>= start_segments` | `ScopeRestartCast` |
| `hls_buffer.start_segments` | `2` | integer in `[1, 6]` and `<= live_edge_segments` | `ScopeRestartCast` |
| `hls_buffer.max_cached_segments` | `6` | integer in `[2, 24]` and `>= start_segments` | `ScopeRestartCast` |
| `hls_buffer.max_cache_bytes` | `268435456` | integer in `[16777216, 2147483648]` | `ScopeRestartCast` |
| `hls_buffer.max_playlist_bytes` | `1048576` | integer in `[4096, 8388608]` | `ScopeRestartCast` |
| `hls_buffer.max_segment_bytes` | `52428800` | integer in `[1048576, 536870912]` | `ScopeRestartCast` |
| `hls_buffer.segment_timeout_seconds` | `10` | integer in `[1, 60]` | `ScopeRestartCast` |
| `hls_buffer.playlist_timeout_seconds` | `10` | integer in `[1, 60]` | `ScopeRestartCast` |
| `hls_buffer.max_variant_height` | `720` | integer in `[240, 2160]` | `ScopeRestartCast` |
| `hls_buffer.stale_cache_reap_hours` | `24` | integer in `[1, 168]` | `ScopeRestartCast` |

`ScopeRestartCast` is conservative but correct for v1 because these values change how the current FFmpeg input is built, how much media is prefetched, and which local playlist path is handed to core. Later work may hot-swap purely diagnostic or future idle-only knobs, but v1 should rebuild the cast.

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
4. `hlsbuffer` loads the playlist, follows one supported master-to-media playlist path when needed, and starts around `live_edge_segments` behind the live edge.
5. `hlsbuffer` prefetches until at least `start_segments` are available locally.
6. Adapter starts `core.Manager` with `StreamURL` pointing to the local rewritten playlist file and a local-only media input policy for FFmpeg.
7. FFmpeg reads the local playlist and local segment files.
8. `hlsbuffer` continues media playlist reload and segment prefetch in the background for the selected media playlist.
9. On stop/preempt/error, adapter calls the cleanup function. If core rejects the start, the guarded start loses its session, or another cast preempts during warmup, the adapter must clean up the opened buffer immediately.

Mid-playback:

- If remote segment fetches are briefly slow, FFmpeg continues reading cached local segments.
- If the local cache runs dry in v1, FFmpeg may starve and the existing dataplane underrun path applies: duplicate video fields are sent and audio continues through the current ring/drain logic.
- V1 must log enough HLS buffer state to identify this as a cache underrun. It must not add slate rendering or audio hold semantics.
- When decoded frames resume, the existing dataplane recovery path resumes normal video/audio output.

## Fetching And Rewriting

Supported HLS v1 subset:

- `#EXTM3U`
- live media playlists without `#EXT-X-ENDLIST`
- finite media playlists if encountered through eligible URL direct casts
- simple master playlists with one selected variant
- relative and absolute segment URIs
- ordinary MPEG-TS video segment resources after validation

Unsupported in v1:

- `#EXT-X-KEY`
- `#EXT-X-BYTERANGE`
- `#EXT-X-DISCONTINUITY`
- `#EXT-X-MAP` and fragmented MP4 streams that require initialization segments
- alternate media playlists via `#EXT-X-MEDIA:URI=...`
- multiple audio/subtitle renditions
- low-latency HLS parts/preload hints
- unknown URI-bearing tags
- audio-only HLS manifests

When unsupported tags appear, behavior should be deterministic:

- For bundled Streams, fail the cast clearly so the provider can be fixed or opted out.
- For URL adapter direct `.m3u8`, either fail with a useful message or fall back to direct FFmpeg only if the fallback does not bypass a security restriction. The first implementation should prefer clear failure plus kill switch over silent partial support.

Master playlist variant selection should be deterministic:

1. Ignore variants whose declared codecs are clearly unsupported by the local FFmpeg probe or whose height is greater than `max_variant_height`.
2. Prefer the lowest-bandwidth variant whose declared height is at least the active output height. For interlaced 480i and 576i modes, use the full frame height, not the field height.
3. If every declared height is below the active output height, choose the highest declared height, then the lowest bandwidth within that height.
4. If no variants declare resolution, choose the lowest declared bandwidth.
5. If neither resolution nor bandwidth is declared, choose the first variant in playlist order and log that metadata was unavailable.

The selected variant is locked for the session. If a later master playlist reload changes the selected variant URI, codecs, or resolution in a way that would require rebuilding FFmpeg, v1 should fail clearly or ask the adapter to restart the cast rather than switching variants silently.

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
- Segment, playlist, and total cache byte limits must be enforced.
- Cache filenames must be generated, not derived directly from remote paths.
- Local playlist output must not contain remote URLs.
- Buffered sessions should pass a local-only `MediaInputPolicy` to core/FFmpeg, for example a protocol whitelist limited to local file access, so a rewrite bug cannot make FFmpeg fetch remote playlist children directly.

The local rolling cache must not evict any segment still referenced by the playlist FFmpeg can read. Eviction should happen only after a segment has aged out of the published local playlist plus a small grace window, or after the session has stopped.

The buffer should reduce FFmpeg's direct remote fetching surface for eligible public HLS because FFmpeg reads local rewritten resources. It should not create a broader SSRF surface.

## Deferred BUFFERING Slate

The desired TV message remains simple: black background with centered ASCII `BUFFERING...`. It is not part of v1.

A follow-up dataplane spec must decide all of these before implementation:

- Placement in the current architecture: an alternate `videoCh` producer, a new `Plane.RenderSlateFrame` style API, or another explicit mechanism.
- Rebuffer-state trigger and threshold. It must not overload the existing 30-field underrun warning threshold without naming the interaction.
- Audio semantics while the slate is active: whether the audio reader continues draining, whether chunks are retained or dropped, what happens to `audioRing`, and how A/V resyncs when real frames resume.
- Startup behavior before FFmpeg and `Plane.Run` are active.
- Tests for slate pixels, state transitions, and audio-ring behavior.

Do not implement slate rendering or audio hold from this spec alone.

## Observability

V1 exposes an `hlsbuffer.Stats` snapshot with explicit units:

- `CachedSegments`: count of cached segments observed by the session;
- `CachedMediaDuration`: media duration represented by cached segments;
- `CacheBytes`: bytes on disk;
- `PlaylistReloadsTotal`: count;
- `SegmentDownloadsTotal`: count;
- `SelectedVariant`: selected master-playlist variant metadata when applicable;
- `FailureReason`: latest refresh or startup failure reason.

Follow-up logging should add a session-start line and periodic or state-change stats for source adapter, redacted URL, cache directory or session id, variant metadata, download timings in milliseconds, downloaded bytes, stale reloads, retries, unsupported tag errors, and cache underruns.

Existing dataplane logs continue to report underruns, duplicates, and frame echo stalls. A later dataplane phase can add enough context to tell whether an underrun happened while the HLS buffer was empty or while FFmpeg/dataplane was otherwise behind.

## Error Handling

- If initial playlist fetch fails, return the adapter's existing playback error path.
- If startup cache warmup cannot fetch the selected start segments, v1 fails before starting FFmpeg.
- If a segment or refresh fails mid-session, v1 records the latest failure reason and keeps the previous local playlist available while the refresh loop retries on the next interval.
- A bounded stale-playlist window and explicit retry counters are deferred.
- If cleanup fails, log a warning and continue session teardown.
- If the session cache directory or local playlist file cannot be created, fail before starting FFmpeg.
- On bridge startup, reap stale HLS buffer session directories older than `stale_cache_reap_hours`. Active sessions should use a lock or owner marker so startup cleanup never removes a live session from another bridge process.

## Testing

Unit tests:

- playlist parser accepts supported simple media playlists;
- playlist parser accepts a simple master playlist and selects a variant;
- parser rejects unsupported tags listed above;
- parser rejects audio-only HLS manifests for v1;
- variant selection follows the documented resolution/bandwidth fallback order;
- selected variant changes that require FFmpeg rebuild are detected deterministically; deferred until variant reload is implemented;
- URL resolver rewrites relative and absolute segment URIs safely;
- bundled Toonami child URLs outside the known host or expected channel path are rejected;
- child media playlist and segment URLs are rejected when they resolve to loopback, link-local, metadata, private LAN, `file:`, or other local resources;
- generated cache filenames cannot escape the cache root;
- public/private URL eligibility matches the security rules;
- local playlist writes are atomic and never reference evicted segment files;
- segment-count and byte-count cache limits are enforced together;
- live playlist reload publishes newly cached segments;
- stale playlist reload and segment retry state transitions are deterministic; deferred with explicit retry counters;
- stale session cache reaping removes only expired inactive sessions;
- config defaults enable the buffer;
- bridge fields and `scopeForBridgeField` declare `ScopeRestartCast` for every `hls_buffer.*` field.

Integration-style tests with `httptest`:

- URL direct `.m3u8` cast starts core with local buffered URL;
- Streams Toonami direct HLS starts core with local buffered URL;
- buffered casts keep `MediaKindVideo` and do not accidentally request the music visualizer path;
- URL per-cast `hls_buffer=off` bypasses buffering while global default stays on;
- Streams provider/channel opt-out bypasses buffering while other eligible streams still buffer;
- Plex/Jellyfin-like transcode URLs are not routed through the HLS buffer;
- a slow segment server is smoothed by prefetched cached segments;
- unsupported HLS fails clearly;
- cleanup removes session files.

## Rollout

1. Add bridge `hls_buffer` config, validation, field definitions, `ScopeRestartCast` wiring, and tests.
2. Add shared `hlsbuffer` package and tests.
3. Integrate Streams bundled direct HLS through the buffer, including provider/channel opt-out.
4. Integrate URL adapter direct pasted `.m3u8` through the buffer, including per-cast opt-out.
5. Add kill switch, stale-cache reaping, docs, and stats units; defer detailed periodic logs.
6. Write a separate dataplane spec for `BUFFERING...` slate and audio-hold behavior.

This order lets the cache prove itself before dataplane display and A/V state work grows the blast radius.
