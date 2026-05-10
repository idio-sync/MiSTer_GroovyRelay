# Torrent Adapter Design

**Date:** 2026-05-10  
**Status:** Design approved; implementation plan not started  
**Scope:** Add a standalone torrent adapter that can stream video from magnet links and uploaded `.torrent` files through the existing GroovyRelay data plane.

## Problem

GroovyRelay can cast server-backed media, direct URLs, yt-dlp-supported pages, DLNA pushed URLs, and catalog-backed Streams channels. It cannot currently take a magnet link or `.torrent` file and play the video through the MiSTer.

Torrent playback has more lifecycle state than a normal URL cast: metadata discovery, peer connections, piece priority, selected-file buffering, cache cleanup, and explicit operator consent for BitTorrent traffic. Folding that into the URL adapter would make the URL adapter responsible for long-lived P2P state it does not otherwise own.

## Goals

- Add a standalone adapter named `torrent`.
- Accept magnet links and uploaded `.torrent` files in v1.
- Use an in-process Go BitTorrent library so operators do not need to install Transmission, qBittorrent, aria2, or another service.
- Auto-select the largest playable video file when a torrent contains multiple files.
- Stream the selected file to FFmpeg through a local HTTP route owned by the adapter.
- Reuse `core.Manager`, FFmpeg probing/transcoding, modeline handling, and the Groovy data plane.
- Keep torrent data session-only by default, with an optional persistent cache bounded by size.
- Require both `enabled=true` and an explicit torrent-traffic acknowledgement before any swarm activity starts.
- Provide clear UI/status/errors for metadata loading, file selection, buffering, playback, stop, and cleanup.

## Non-Goals

- Remote HTTP(S) `.torrent` URL fetching in v1.
- Torrent search, index browsing, RSS feeds, DHT search, or curated torrent catalogs.
- Manual file picker in v1.
- Subtitles inside torrents.
- Cross-session queueing or playlist playback.
- Seeding policy controls beyond what is necessary to download/play the selected file.
- Copyright or content-legality enforcement. The operator is responsible for the content they provide.
- Replacing the URL adapter or Streams adapter.
- Shipping an external torrent engine sidecar.

## Decisions Captured During Brainstorm

| Topic | Decision |
|---|---|
| Product shape | Standalone Torrent adapter |
| Multi-file torrents | Auto-pick largest playable video |
| Cache default | Session-only; optional keep-cache setting |
| Traffic consent | Require explicit acknowledgement toggle |
| Engine model | In-process Go library |
| First library target | `github.com/anacrolix/torrent` |
| Accepted inputs | Magnet paste and `.torrent` upload |

## Dependency Choice

Use `github.com/anacrolix/torrent` as the implementation target. Current public package documentation describes it as a Go BitTorrent client intended for library use, with support for streaming, seeking, readaheads, file readers, magnet input, `.torrent` metainfo, DHT, PEX, uTP, and file storage backends.

Implementation planning should verify the exact version and APIs before coding. The design assumes these stable capabilities:

- create a client with configured storage and listen behavior;
- add a torrent from a magnet URI;
- add a torrent from parsed metainfo;
- wait for metadata;
- enumerate torrent files;
- set file or piece priority;
- create an `io.Reader`/range-capable reader for the selected file;
- close/drop torrents and client resources.

## Architecture

Add a new package:

```text
internal/adapters/torrent/
  adapter.go        # Adapter lifecycle, config, status, fields
  config.go         # TOML defaults, decode, validation, apply scopes
  routes.go         # UI/public route declarations
  ui.go             # ExtraPanelHTML and view model
  client.go         # Narrow interface over anacrolix/torrent
  session.go        # Active session state and cleanup
  select.go         # Playable-file detection and largest-file selection
  server.go         # Local tokenized media serving with Range support
  cache.go          # Session dirs, optional persistent cache, pruning
  errors.go         # Error taxonomy and safe display messages
  *_test.go
```

The adapter mounts UI routes under `/ui/adapter/torrent/*` and a tokenized media route under an adapter-owned route such as `/torrent/session/{token}/media`. FFmpeg receives a loopback URL to that media route. The adapter route must reject non-loopback requests, require a high-entropy session token, and stop serving as soon as the session ends.

The core boundary stays unchanged:

```go
core.SessionRequest{
    StreamURL:    localMediaURL,
    AdapterRef:   "torrent:" + sessionID,
    Source:       "torrent",
    DirectPlay:   true,
    Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
    Title:        selectedDisplayName,
    OnStop:       sessionCleanup,
}
```

## Config

Add `[adapters.torrent]`:

```toml
[adapters.torrent]
enabled = false
traffic_acknowledged = false
download_dir = ""                 # default <data_dir>/torrent
keep_completed = false            # session-only by default
max_cache_bytes = 21474836480     # 20 GiB
metadata_timeout_seconds = 60
startup_buffer_seconds = 10
max_upload_rate_kbps = 0          # 0 = library/default unlimited
max_download_rate_kbps = 0        # 0 = library/default unlimited
listen_port = 0                   # 0 = library/default random
```

Apply scopes:

| Field | Scope | Reason |
|---|---|---|
| `enabled` | `ScopeHotSwap` | Starts/stops adapter availability through existing toggle flow. |
| `traffic_acknowledged` | `ScopeHotSwap` | Allows or blocks new torrent sessions without restarting. |
| `download_dir` | `ScopeRestartCast` | Active sessions and client storage are rooted there. |
| `keep_completed` | `ScopeHotSwap` | Affects cleanup policy for future session endings. |
| `max_cache_bytes` | `ScopeHotSwap` | Pruner reads the current limit. |
| `metadata_timeout_seconds` | `ScopeHotSwap` | Applied to new metadata waits. |
| `startup_buffer_seconds` | `ScopeHotSwap` | Applied to new sessions before core playback starts. |
| `max_upload_rate_kbps` | `ScopeRestartCast` | Changes torrent client transfer behavior. |
| `max_download_rate_kbps` | `ScopeRestartCast` | Changes torrent client transfer behavior. |
| `listen_port` | `ScopeRestartCast` | Changes torrent client network listener. |

Validation:

- `download_dir` may be empty or a valid filesystem path accepted by existing config path validation helpers.
- `max_cache_bytes` must be at least 1 GiB and at most 1 TiB.
- `metadata_timeout_seconds` must be 5-600.
- `startup_buffer_seconds` must be 0-120.
- transfer limits must be 0 or positive.
- `listen_port` must be 0 or 1-65535.

## UI And HTTP Surface

The adapter panel shows:

- enabled/running/error state using existing adapter panel conventions;
- acknowledgement state and a short warning that BitTorrent traffic uses the operator's network;
- magnet input form;
- `.torrent` upload form with a bounded upload size;
- current session status: metadata, selected file, buffer/download progress if available, peers if cheap to compute, and active title;
- stop button;
- cleanup cache button when persistent cache has data.

Routes:

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/ui/adapter/torrent/panel` | Render panel fragment. |
| `POST` | `/ui/adapter/torrent/play/magnet` | Validate and start a magnet session. |
| `POST` | `/ui/adapter/torrent/play/file` | Accept multipart `.torrent`, validate size, and start a session. |
| `POST` | `/ui/adapter/torrent/stop` | Stop the active torrent-owned core session. |
| `DELETE` | `/ui/adapter/torrent/cache` | Delete inactive cached data. |
| `GET` | `/torrent/session/{token}/media` | Serve selected file to local FFmpeg with Range support. |

The media route is not a user-facing route. It must:

- require a valid active session token;
- reject requests whose remote address is not loopback;
- support `Range` requests well enough for FFmpeg probe/seek;
- return `404` after session cleanup;
- avoid exposing torrent paths on disk.

## Playback Data Flow

1. User enables the adapter and acknowledges BitTorrent traffic.
2. User submits a magnet link or uploads a `.torrent`.
3. Adapter validates the input without logging sensitive magnet query details.
4. Adapter creates or reuses its torrent client rooted at `<data_dir>/torrent` or `download_dir`.
5. Adapter adds the torrent and waits for metadata with `metadata_timeout_seconds`.
6. Adapter enumerates files and chooses the largest supported video file.
7. Adapter deprioritizes unwanted files where the library API supports it.
8. Adapter prioritizes selected-file pieces and read-ahead near the start of the file.
9. Adapter opens a selected-file reader and registers a tokenized local media route.
10. Adapter optionally waits for `startup_buffer_seconds` worth of initial data when measurable; otherwise it proceeds once the file reader can begin serving.
11. Adapter calls `core.StartSession`.
12. On stop, preempt, or playback error, adapter closes the media route, closes torrent/session resources, and deletes session data unless `keep_completed=true`.

## File Selection

Supported video extensions for v1:

- `.mp4`
- `.m4v`
- `.mkv`
- `.avi`
- `.mov`
- `.mpg`
- `.mpeg`
- `.ts`
- `.webm`
- `.wmv`

Selection rules:

1. Exclude files with unsupported extensions.
2. Exclude zero-length files.
3. Pick the largest remaining file.
4. If two files have the same size, choose lexicographically by display path for deterministic behavior.
5. Use the selected file's basename as the session title, sanitized for display/logging.

Archives, sample clips, disc images, subtitle files, and metadata files are not playable in v1 unless their extension is one of the supported video extensions.

## Cache And Cleanup

Default behavior is session-only:

- create a per-session directory;
- remove it when playback ends or session start fails;
- remove any inactive session dirs at adapter start.

When `keep_completed=true`:

- data may remain under `download_dir`;
- inactive cache pruning enforces `max_cache_bytes`;
- active session data is never pruned;
- pruning deletes oldest inactive entries first using filesystem mtime or adapter-maintained metadata.

The adapter should never delete outside its configured torrent data root.

## Safety And Privacy

- No torrent client starts and no swarm activity occurs unless both `enabled` and `traffic_acknowledged` are true.
- v1 accepts only local user-provided magnet text and uploaded `.torrent` bytes.
- v1 does not fetch remote `.torrent` URLs, avoiding another SSRF validation surface.
- Logs use torrent name, selected file name, and info hash when available; full magnet URIs are redacted.
- Uploaded `.torrent` bodies are size-limited before parsing.
- Media serving requires loopback remote address plus a high-entropy session token.
- The adapter does not expose a remote torrent-control API.
- On adapter disable, stop, preempt, or bridge shutdown, active torrent resources are closed promptly.

## Error Handling

User-visible errors:

- Adapter disabled: "Torrent adapter is disabled."
- Traffic not acknowledged: "Enable torrent traffic acknowledgement before starting torrents."
- Invalid magnet: "Magnet link is not valid."
- Invalid upload: "Uploaded file is not a valid .torrent file."
- Metadata timeout: "Torrent metadata was unavailable before the timeout."
- No playable file: "Torrent contains no supported video files."
- Media route expired: "Torrent session is no longer active."
- Core start failure: "Torrent playback could not start."

Operational handling:

- Metadata timeout closes the torrent and cleans session data according to policy.
- No playable file closes the torrent and cleans session data according to policy.
- Core start failure closes the media route before returning the error.
- Core `OnStop("eof"|"stopped"|"preempted"|"error")` closes session resources and sets adapter state consistently with existing adapters.
- Cleanup failures are logged as warnings and surfaced in diagnostics when practical, but they do not block stopping playback.

## Testing

Most tests use fake torrent interfaces rather than real swarm traffic.

Unit tests:

- config defaults, decode, validation, field definitions, apply scopes;
- magnet and `.torrent` route validation;
- disabled and acknowledgement gate behavior;
- playable extension detection;
- largest-video selection and deterministic ties;
- no-playable-file error;
- session token generation and rejection;
- loopback-only media route enforcement;
- HTTP range request handling;
- cleanup behavior for session-only and persistent cache modes;
- cache pruning bounded by `max_cache_bytes`;
- safe magnet redaction in errors/log helpers.

Session tests with fake torrent client:

- metadata success starts core with `Source="torrent"`;
- metadata timeout cleans up;
- selected file route is registered before `core.StartSession`;
- core start failure closes route/session;
- `OnStop` cleans resources for eof, stopped, preempted, and error;
- new torrent cast preempts previous torrent-owned session.

Integration-lite tests:

- instantiate the adapter with a fake core manager;
- submit a fake magnet/upload through HTTP handlers;
- assert `core.SessionRequest` has `DirectPlay=true`, pause/seek capabilities, title, adapter ref, source, and local media URL.

No normal test contacts public trackers, DHT, or peers. A future opt-in manual test may use a tiny legal test torrent, guarded by an environment variable.

## Documentation Updates

Implementation should update:

- `README.md` with the Torrent adapter section, warning, supported inputs, and example config;
- `internal/config/example.toml` with disabled defaults;
- `THIRD_PARTY_NOTICES.md` for `anacrolix/torrent` and transitive license obligations;
- any release/packaging notes if the new dependency changes binary size or platform behavior.

## Implementation Risks

- Torrent streaming quality depends on peer availability and piece distribution. The UI should make metadata/buffering failures clear rather than implying URL-like reliability.
- FFmpeg range behavior may request non-linear reads. The media route and reader wrapper must handle seeks without deadlocking.
- In-process torrent networking broadens the bridge's network behavior. The acknowledgement gate and disabled-by-default config are required, not polish.
- `anacrolix/torrent` API details may require small adjustments during planning, especially around file priority and rate limits. The adapter wrapper exists to isolate those details from the rest of the package.
