# Torrent Adapter Design

**Date:** 2026-05-10  
**Status:** Review fixes applied; implementation plan not started
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
| Live consent/config changes | `enabled=false` and `traffic_acknowledged=false` block new sessions only; active playback continues until Stop, preempt, EOF, or error |
| Engine model | In-process Go library |
| Client lifetime | Create torrent client lazily on first eligible play, after both gates pass |
| First library target | `github.com/anacrolix/torrent` |
| Accepted inputs | Magnet paste and `.torrent` upload |
| Concurrent play request | New torrent play requests preempt the active core session, matching existing cast sources |

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

Route integration uses the existing adapter extension points:

- UI endpoints implement `adapters.RouteProvider` through `UIRoutes()` and mount below `/ui/adapter/torrent/*`.
- Media serving implements `adapters.PublicRouteProvider` through `MountPublicRoutes(*http.ServeMux)` because `/torrent/session/{token}/media` lives outside `/ui/*` and must not pass through the settings UI route prefix.
- Public routes must use a torrent-specific prefix disjoint from Plex `/resources` and `/player/*`, DLNA routes, and all `/ui/*` routes.

The core boundary stays unchanged:

```go
core.SessionRequest{
    StreamURL:    localMediaURL,
    AdapterRef:   "torrent:" + sessionID,
    Source:       "torrent",
    DirectPlay:   true,
    Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
    Title:        selectedDisplayName,
    MediaInputPolicy: core.MediaInputPolicy{
        ProtocolWhitelist: []string{"http", "tcp"},
        DisableRedirects:  true,
        DisableReconnect:  true,
        RWTimeout:         30 * time.Second,
        BlockedHeaders:    []string{"Cookie", "Authorization", "Proxy-Authorization"},
    },
    OnStop:       sessionCleanup,
}
```

The adapter must record active session ownership before calling `core.StartSession`. Once `StartSession` returns nil, core owns the playback lifecycle and may invoke `OnStop` from a goroutine at any time. Torrent cleanup must therefore be idempotent, session-token guarded, and safe if it races with a play handler returning, a new play request, or an operator Stop.

The torrent client is lazy:

- `Start(ctx)` configures adapter state but does not open a BitTorrent listen port.
- The first play request creates the torrent client only after `enabled=true` and `traffic_acknowledged=true`.
- If either gate later becomes false, the adapter rejects new sessions but does not tear down the active one. The operator can stop it explicitly, or it will end through EOF, error, or preemption.
- If no active torrents remain and `keep_completed=false`, the adapter may close the client after cleanup. If persistent cache is enabled, it may keep the client until adapter Stop to preserve cache attachment efficiency, but it must not seed inactive torrents after playback stops.

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
max_upload_rate_kbps = 512        # 0 = explicit library/default unlimited
max_download_rate_kbps = 0        # 0 = library/default unlimited
listen_port = 0                   # 0 = library/default random
```

Apply scopes:

| Field | Scope | Reason |
|---|---|---|
| `enabled` | `ScopeHotSwap` | Allows or blocks new torrent sessions; active sessions continue until Stop, preempt, EOF, or error. |
| `traffic_acknowledged` | `ScopeHotSwap` | Allows or blocks new torrent sessions; active sessions continue until Stop, preempt, EOF, or error. |
| `download_dir` | `ScopeRestartCast` | Active sessions and client storage are rooted there. |
| `keep_completed` | `ScopeHotSwap` | Affects cleanup policy for future session endings. |
| `max_cache_bytes` | `ScopeHotSwap` | Pruner reads the current limit. |
| `metadata_timeout_seconds` | `ScopeHotSwap` | Applied to new metadata waits. |
| `startup_buffer_seconds` | `ScopeHotSwap` | Applied to new sessions before core playback starts. |
| `max_upload_rate_kbps` | `ScopeRestartCast` | Changes torrent client transfer behavior. |
| `max_download_rate_kbps` | `ScopeRestartCast` | Changes torrent client transfer behavior. |
| `listen_port` | `ScopeRestartCast` | Changes torrent client network listener. |

`download_dir` is treated as a parent location, not a deletion root. When empty, the adapter stores data under `<data_dir>/torrent`. When set, the adapter creates an owned child directory such as `<download_dir>/groovyrelay-torrent` and stores all session/cache data inside that child.

The adapter owns its `download_dir` validation. There is no existing generic directory validator in `internal/config`; `validateExternalToolPath` is executable-specific and must not be reused.

Validation:

- `download_dir` may be empty or a cleaned filesystem path.
- after `filepath.Clean`, `download_dir` must not contain unresolved `..` elements.
- `download_dir` must not be a filesystem root, home directory, or other dangerous broad root.
- when non-empty, `download_dir` must already exist or be creatable, and the adapter-owned child directory must be writable.
- the derived adapter-owned storage root must stay inside `download_dir`.
- `max_cache_bytes` must be at least 1 GiB and at most 1 TiB.
- `metadata_timeout_seconds` must be 5-600.
- `startup_buffer_seconds` must be 0-120.
- transfer limits must be 0 or positive. Upload defaults to a conservative cap; `0` is an explicit operator opt-in to the library/default unlimited behavior.
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
- reject requests whose remote address is not loopback. Use `net.ParseIP(host).IsLoopback()` after splitting host/port, so both IPv4 loopback (`127.0.0.0/8`) and IPv6 loopback (`::1`) are accepted and other addresses are rejected;
- support `Range` requests well enough for FFmpeg probe/seek;
- return `404` after session cleanup;
- avoid exposing torrent paths on disk.

Upload handling:

- v1 accepts one multipart file field named `torrent_file`.
- The request body is capped with `http.MaxBytesReader` before multipart parsing.
- The v1 upload limit is 4 MiB (`4194304` bytes), fixed rather than configurable.
- Multipart memory use is bounded; implementations should spill through the standard multipart path instead of reading unbounded bodies.
- Over-limit uploads return a validation error before torrent parsing.

## Playback Data Flow

1. User enables the adapter and acknowledges BitTorrent traffic.
2. User submits a magnet link or uploads a `.torrent`.
3. Adapter validates the input without logging sensitive magnet query details.
4. Adapter lazily creates or reuses its torrent client rooted at the adapter-owned storage root.
5. Adapter adds the torrent and waits for metadata with `metadata_timeout_seconds`.
6. Adapter enumerates files and chooses the largest supported video file.
7. Adapter deprioritizes unwanted files where the library API supports it.
8. Adapter prioritizes selected-file pieces and read-ahead near the start of the file.
9. Adapter opens a selected-file reader and registers a tokenized local media route.
10. Adapter optionally waits for `startup_buffer_seconds` worth of initial data when measurable; otherwise it proceeds once the file reader can begin serving.
11. Adapter calls `core.StartSession`.
12. On stop, preempt, or playback error, adapter closes the media route, closes torrent/session resources, and deletes session data unless `keep_completed=true`.

New torrent play requests preempt the current core session rather than returning a conflict. This matches existing cast sources: `core.StartSession` replaces any active session with a different `AdapterRef` and invokes the previous session's `OnStop("preempted")`.

For repeated sessions with the same info hash:

- Active torrent objects are keyed by info hash within the client.
- Adding the same info hash while the client is running reuses the existing torrent object instead of creating duplicate torrent state.
- If a previous persistent cache entry exists, the new session reuses the info-hash cache directory and attaches a new adapter session token/media route.
- Reuse still runs file selection against the current metadata and reprioritizes the selected file for the new session.
- Session-only mode may reuse the active in-memory torrent while it exists, but once cleanup removes the marked session directory the next play starts from whatever data remains available through the swarm.

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

Title/display sanitization:

- Use torrent file paths only as display strings, never as filesystem deletion paths.
- Normalize separators to `/` for display.
- Drop control characters and trim leading/trailing whitespace.
- Collapse empty or all-dot basenames to a safe fallback such as the info hash prefix.
- HTML rendering must escape the title through existing template escaping.

## Cache And Cleanup

Default behavior is session-only:

- create a per-session directory under the adapter-owned storage root;
- write an adapter marker file, such as `.groovyrelay-torrent-session.json`, before any torrent data is written;
- remove it when playback ends or session start fails;
- remove any inactive session dirs at adapter start.

When `keep_completed=true`:

- data may remain under the adapter-owned storage root;
- inactive cache pruning enforces `max_cache_bytes`;
- active session data is never pruned;
- pruning deletes oldest inactive entries first using adapter-maintained metadata or filesystem mtime.

Cleanup and pruning rules:

- Creation order is `mkdir session dir` -> `write marker` -> `write torrent data`.
- The adapter must never delete outside the adapter-owned storage root.
- The adapter must never delete the configured `download_dir` itself.
- The adapter must never delete a directory or file lacking the adapter marker.
- If the process crashes after `mkdir` but before marker write, the orphan directory is intentionally ignored by automatic cleanup. It may be surfaced in diagnostics or removed manually by the operator.
- Unsafe roots are rejected during validation before any cleanup logic can run.
- Path traversal-like torrent file paths are treated as display names only and never as paths to delete.

## Safety And Privacy

- No torrent client starts and no swarm activity occurs unless both `enabled` and `traffic_acknowledged` are true.
- v1 accepts only local user-provided magnet text and uploaded `.torrent` bytes.
- v1 does not fetch remote `.torrent` URLs, avoiding another SSRF validation surface.
- Logs use torrent name, selected file name, and info hash when available; full magnet URIs are redacted.
- Uploaded `.torrent` bodies are capped at 4 MiB before parsing.
- Media serving requires loopback remote address plus a high-entropy session token.
- Core playback uses a restrictive `MediaInputPolicy` for the local route: `http,tcp` only, redirects/reconnect disabled, bounded read/write timeout, and no sensitive headers.
- The adapter does not expose a remote torrent-control API.
- On explicit Stop, preempt, EOF, playback error, adapter Stop, or bridge shutdown, active torrent resources are closed promptly.
- Session-only mode closes the torrent on stop/preempt/error, which also stops any ongoing upload/seeding for that session. Persistent cache does not imply persistent seeding after playback stops.

Magnet redaction:

- Keep only a normalized info-hash hint such as `btih:<first-8-hex>` when available.
- Drop `tr`, `dn`, `xs`, `ws`, `as`, and any other query parameters from logs, HTTP errors, and event messages.
- If no info hash can be parsed, log only `magnet:<invalid>`.

The `BlockedHeaders` policy on the local media URL is defense-in-depth. The torrent media route should not need credentials, but stripping `Cookie`, `Authorization`, and `Proxy-Authorization` at the core/FFmpeg boundary prevents future route changes from accidentally forwarding sensitive headers.

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

HTTP status mapping:

| Case | Status |
|---|---:|
| adapter disabled | `409 Conflict` |
| traffic not acknowledged | `403 Forbidden` |
| invalid magnet or malformed `.torrent` | `400 Bad Request` |
| upload over 4 MiB | `413 Payload Too Large` |
| metadata timeout | `504 Gateway Timeout` |
| no playable file | `422 Unprocessable Entity` |
| expired or unknown media token | `404 Not Found` |
| non-loopback media request | `403 Forbidden` |
| core start failure | `500 Internal Server Error` |

Operational handling:

- Metadata timeout closes the torrent and cleans session data according to policy.
- No playable file closes the torrent and cleans session data according to policy.
- Core start failure closes the media route before returning the error.
- Core `OnStop("eof"|"stopped"|"preempted"|"error")` closes session resources and sets adapter state consistently with existing adapters.
- Disabling the adapter or unchecking traffic acknowledgement blocks new sessions but does not synthesize an `OnStop`; active sessions continue until an explicit Stop, EOF, error, or preemption.
- Cleanup failures are logged as warnings and surfaced in diagnostics when practical, but they do not block stopping playback.

## Testing

Most tests use fake torrent interfaces rather than real swarm traffic.

Unit tests:

- config defaults, decode, validation, field definitions, apply scopes;
- magnet and `.torrent` route validation;
- disabled and acknowledgement gate behavior;
- dangerous `download_dir` rejection;
- adapter-owned subdirectory derivation for custom `download_dir`;
- custom `download_dir` validator rejects filesystem roots, home directories, unresolved `..`, and unwritable adapter-owned roots;
- `enabled=false` and `traffic_acknowledged=false` reject new sessions without tearing down an active fake session;
- lazy client creation happens only after both gates pass;
- playable extension detection;
- largest-video selection and deterministic ties;
- title/display sanitization;
- no-playable-file error;
- session token generation and rejection;
- loopback-only media route enforcement for `127.0.0.1`, another `127.0.0.0/8` address, `::1`, and a rejected non-loopback address;
- HTTP range request handling;
- cleanup behavior for session-only and persistent cache modes;
- cleanup never deletes unmarked files or directories;
- crash orphan case leaves unmarked directories untouched;
- cache pruning bounded by `max_cache_bytes`;
- same-info-hash play reuses existing torrent/cache state;
- oversized upload rejection before parsing;
- malformed metainfo handling;
- huge file-list metainfo handling;
- duplicate and path-traversal-like torrent display paths;
- error-to-HTTP-status mapping;
- safe magnet redaction in errors/log helpers.

Session tests with fake torrent client:

- metadata success starts core with `Source="torrent"`;
- metadata timeout cleans up;
- selected file route is registered before `core.StartSession`;
- core start failure closes route/session;
- play handler does not assume ownership remains after `core.StartSession` returns nil; cleanup is idempotent if `OnStop` races the response;
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

Dependency version pinning belongs in the implementation plan after checking the current `anacrolix/torrent` release, transitive dependencies, licenses, and supported platform behavior.

## Implementation Risks

- Torrent streaming quality depends on peer availability and piece distribution. The UI should make metadata/buffering failures clear rather than implying URL-like reliability.
- FFmpeg range behavior may request non-linear reads. The media route and reader wrapper must handle seeks without deadlocking.
- In-process torrent networking broadens the bridge's network behavior. The acknowledgement gate and disabled-by-default config are required, not polish.
- `anacrolix/torrent` API details may require small adjustments during planning, especially around file priority and rate limits. The adapter wrapper exists to isolate those details from the rest of the package.
- Very large but syntactically valid metainfo files may parse within the 4 MiB upload cap yet still contain many files. Implementation should bound accepted file count during metadata normalization and fail with a clear validation error.
