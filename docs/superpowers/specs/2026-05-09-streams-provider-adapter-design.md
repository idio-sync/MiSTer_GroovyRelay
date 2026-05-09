# Streams Provider Adapter Design

**Date:** 2026-05-09  
**Status:** Design approved for spec; implementation plan not started  
**Scope:** Add a generic catalog-backed `streams` adapter, with MTV Rewind and Cartoon Rewind as the first bundled providers.

## Problem

GroovyRelay can already cast individual URLs, including direct video files, M3U/M3U8 streams, and pages handled by yt-dlp. Some sites are more than a single URL, though. MTV Rewind and Cartoon Rewind expose browsable channel catalogs backed by YouTube IDs. A URL-only cast can play a single shared song link, but it cannot preserve the channel experience: groups, shuffle behavior, next/previous, EOF advancement, channel defaults, and future mini-remote browsing.

The relay needs a source adapter that treats these sites as channel providers rather than one-off URLs, while still using the existing FFmpeg/Groovy data plane for playback.

## Goals

- Add a generic adapter named `streams`.
- Support catalog-backed providers through compiled provider types, starting with `youtube-channel-json`.
- Bundle provider definitions for:
  - MTV Rewind (`https://wantmymtv.vercel.app/`, plus `wantmymtv.xyz` links)
  - Cartoon Rewind (`https://cartoonrewind.tv/`)
- Play provider channels as queues through the existing core manager and yt-dlp resolver path.
- Resolve provider URLs such as `https://wantmymtv.vercel.app/player.html?channel=metal` into native streams adapter playback.
- Resolve provider video links such as `player.html?v=<youtubeId>` or `/s/<youtubeId>`.
- Support channel browser UI, provider refresh, play, stop, replay, next, previous, and current queue status.
- Periodically refresh provider metadata from a GitHub-hosted manifest without requiring an app update.
- Keep the remote manifest data-only. It may add or update providers for known compiled provider types, but it may not define executable behavior.

## Non-Goals

- MTV Rewind live Firebase sync.
- Full IPTV-org browsing in v1.
- Internet Archive, Media RSS, PeerTube, Vimeo, Dailymotion, or other provider types in v1.
- Running remote JavaScript or arbitrary scrapers.
- Extension mini-remote integration in this pass, beyond keeping the streams control model compatible with the companion API later.
- Cookie management for provider YouTube videos. Existing URL adapter cookie work remains separate.
- EPG/guide data, scheduled programming, or exact "linear TV" time sync.
- A public plugin system.

## Decisions Captured During Brainstorm

| Topic | Decision |
|---|---|
| Product shape | Generic `streams` adapter, not an MTV-only adapter |
| First provider type | `youtube-channel-json` |
| First bundled providers | MTV Rewind and Cartoon Rewind |
| URL adapter role | Detect matching provider URLs and hand off to streams |
| Remote update model | GitHub-hosted manifest can update provider data only |
| Live sync | Deferred |
| Core playback path | Reuse `core.Manager` and yt-dlp, no new media plane |

## Provider Model

The `streams` adapter normalizes every provider into a shared catalog model:

```go
type ProviderCatalog struct {
	ProviderID string
	Name       string
	Groups     []ChannelGroup
	Channels   []Channel
	UpdatedAt  time.Time
}

type ChannelGroup struct {
	ID    string
	Name  string
	Order int
}

type Channel struct {
	ID          string
	Name        string
	Description string
	GroupID     string
	Icon        string
	Items       []StreamItem
	PlayMode    PlayMode
	Order       int
}

type StreamItem struct {
	ID       string
	Title    string
	URL      string
	SourceID string
}

type PlayMode string

const (
	PlaySequential      PlayMode = "sequential"
	PlayShuffle         PlayMode = "shuffle"
	PlayFirstThenShuffle PlayMode = "first_then_shuffle"
)
```

For `youtube-channel-json`, the provider's playlist data is a JSON object whose keys are channel IDs and whose values are arrays of YouTube video IDs. The provider synthesizes item URLs as `https://www.youtube.com/watch?v=<id>` after validating that each ID matches the expected YouTube ID shape. The provider playlist URL never becomes a direct media input.

Provider metadata, including names, groups, URL patterns, default play modes, and per-channel play mode overrides, comes from bundled provider definitions and the GitHub manifest. For MTV Rewind, this metadata is derived from the public channel configuration, but v1 does not execute or evaluate `channels-config.js`. If a playlist contains a channel key missing from metadata, the adapter surfaces it under an "Ungrouped" group with a default shuffle mode.

## Package Layout

```text
internal/adapters/streams/
  adapter.go                         # Adapter lifecycle, config, status
  config.go                          # TOML config and validation
  routes.go                          # UI route handlers
  ui.go                              # ExtraPanelHTML and view models
  queue.go                           # Queue state, play modes, next/previous
  playback.go                        # yt-dlp resolution and core.SessionRequest construction
  manifest.go                        # bundled/cached/remote manifest load and merge
  provider.go                        # Provider interface and normalized types
  provider_youtube_channel_json.go   # MTV/Cartoon provider type
  url_resolver.go                    # provider URL matching and handoff
  cache.go                           # atomic cache reads/writes in data_dir
  *_test.go
```

This package may import `internal/adapters/url/ytdlp` to reuse the existing resolver implementation. It must not import URL adapter runtime internals or call URL adapter handlers.

## Config

Add `[adapters.streams]`:

```toml
[adapters.streams]
enabled = false
manifest_url = "https://raw.githubusercontent.com/idio-sync/MiSTer_GroovyRelay/main/docs/streams/providers.json"
manifest_refresh_hours = 24
catalog_refresh_hours = 12
max_catalog_bytes = 10485760
max_items_per_channel = 5000
max_consecutive_failures = 5
youtube_format = "bv*[height<=480]+ba/b[height<=480]/bv*+ba/b"
allow_remote_manifest = true
```

Notes:

- `enabled=false` avoids unexpected background network traffic until the operator opts in.
- `allow_remote_manifest=false` uses only bundled and cached provider definitions.
- The default `youtube_format` mirrors the URL adapter's conservative SD bias. Future changes may tune the streams default without changing URL adapter behavior.
- The adapter uses the bridge-level `yt-dlp` sidecar/path resolution, not a provider-specific binary.

## Manifest

The manifest is versioned JSON:

```json
{
  "version": 1,
  "providers": [
    {
      "id": "mtv-rewind",
      "type": "youtube-channel-json",
      "display_name": "MTV Rewind",
      "base_url": "https://wantmymtv.vercel.app",
      "playlist_url": "https://wantmymtv.vercel.app/public/mtv-playlists.json",
      "url_patterns": [
        "https://wantmymtv.vercel.app/player.html*",
        "https://wantmymtv.xyz/player.html*",
        "https://wantmymtv.vercel.app/s/*",
        "https://wantmymtv.xyz/s/*"
      ],
      "default_channel": "1stday",
      "default_play_mode": "shuffle",
      "groups": [
        { "id": "shows", "name": "MTV Shows", "order": 50 },
        { "id": "decades", "name": "By Decade", "order": 60 }
      ],
      "channels": [
        {
          "id": "metal",
          "name": "Headbangers Ball",
          "group_id": "shows",
          "play_mode": "shuffle",
          "order": 40
        }
      ]
    }
  ]
}
```

Merge rules:

- Bundled providers are the baseline.
- Cached manifest overlays bundled providers when valid.
- Remote manifest overlays cached and bundled providers after a successful fetch.
- User TOML can disable a provider by ID.
- A remote provider with an unknown `type` is ignored.
- A remote update for a bundled provider must keep the same `type`; otherwise that provider update is ignored.
- Remote providers may be added when their `type` is compiled into the app.
- The manifest is data only. There are no regex replacements, JavaScript snippets, shell commands, dynamic templates, or code hooks.

Fetch behavior:

- Use `ETag` and `If-None-Match` for the remote manifest and provider catalog URLs.
- Save the last good manifest and catalog snapshots under `data_dir/streams/`.
- Use atomic writes for cache files.
- On `304 Not Modified`, keep the in-memory snapshot and update freshness metadata.

## Bundled Providers

### MTV Rewind

Provider ID: `mtv-rewind`  
Provider type: `youtube-channel-json`  
Playlist URL: `https://wantmymtv.vercel.app/public/mtv-playlists.json`

URL handling:

- `player.html?channel=metal` starts the `metal` channel.
- `player.html?v=<id>` starts that YouTube ID. If the ID exists in one or more known channels, use the first containing channel by provider/channel order and move the selected item to the front of the queue. If the ID is not in the catalog, play it as a single-item queue under MTV Rewind.
- `/s/<id>` is treated like `?v=<id>`.

Play mode defaults:

- `1stday`, `liveaid`, `bangladesh`, and `flexlist`: sequential.
- Curated/director channels whose first item is an intro or signature item: first-then-shuffle.
- Broad era/genre/show channels such as `metal`, `80s`, `90s`, `all`: shuffle.
- Unknown channels found in the playlist JSON: shuffle.

### Cartoon Rewind

Provider ID: `cartoon-rewind`  
Provider type: `youtube-channel-json`  
Playlist URL: `https://cartoonrewind.tv/cartoon-playlists.json`

URL handling:

- `player.html?channel=heman` starts the `heman` channel.
- `player.html?v=<id>` starts that video or a single-item queue.

Play mode defaults:

- All normal channels: shuffle.
- `all`: shuffle across every non-commercial channel.
- The `commercials` playlist is excluded from normal channel queues in v1.
- PSA interruptions are deferred. A later version can add optional timed PSA insertion.

## URL Handoff

The URL adapter gets an optional dependency:

```go
type StreamURLResolver interface {
	ResolveStreamURL(ctx context.Context, rawURL string) (StreamURLResolution, bool, error)
}

type StreamURLResolution struct {
	AdapterRef string
	ProviderID string
	ChannelID  string
	ItemID     string
}
```

Before URL's direct/yt-dlp route decision, it asks the resolver whether the URL belongs to streams.

Cases:

- No match: URL adapter continues existing behavior.
- Match and streams enabled: streams starts native playback and URL adapter returns success.
- Match and streams disabled: URL adapter returns a clear error that the streams adapter is disabled.
- Match but provider/channel/item invalid: URL adapter returns the streams error as a user-facing error.

Only explicit provider links are handed off in v1. A bare `https://wantmymtv.vercel.app/` or `https://cartoonrewind.tv/` does not auto-start a provider default channel, because casting a landing page should not unexpectedly start a large queue.

## Queue And Playback

The streams adapter owns queue state:

```go
type ActiveQueue struct {
	SessionID     string
	ProviderID    string
	ChannelID     string
	Items         []StreamItem
	Index         int
	Generation    uint64
	Failures      []ItemFailure
	StartedAt     time.Time
	LastResolvedAt time.Time
}
```

Queue construction:

- `sequential`: preserve provider order.
- `shuffle`: shuffle using an injected RNG so tests can be deterministic.
- `first_then_shuffle`: keep item 0 first, shuffle items 1..n.
- `all` channels concatenate eligible channels, then apply the configured play mode.
- Empty queues fail before touching `core.Manager`.

Playback:

1. Build the next `StreamItem`.
2. Resolve it through the yt-dlp resolver.
3. Build `core.SessionRequest` with:
   - resolved video URL and headers
   - audio URL and audio headers when yt-dlp returns split DASH streams
   - `AdapterRef` as a streams session/item reference
   - `DirectPlay=true`
   - `Capabilities{CanPause: true, CanSeek: true}`
   - `OnStop` closure capturing queue generation and item identity
4. Call `core.Manager.StartSession`.

Next/previous:

- `Next` advances index and starts a new item, preempting the active session.
- `Previous` moves to the previous queue item, wrapping at the start.
- Manual next/previous cancels any in-flight item resolution before starting the requested item.

EOF advancement:

- On `OnStop("eof")`, the adapter advances to the next queue item if the captured generation still matches the active queue.
- On `OnStop("preempted")` or `OnStop("stopped")`, the adapter does not auto-advance.
- On `OnStop("error")`, the adapter records item failure and may skip to the next item until `max_consecutive_failures` is reached.

## Required Core Lifecycle Fix

Current `core.Manager` already lets adapters provide `SessionRequest.OnStop`, and the URL adapter's `makeOnStop` handles reason `"eof"`. However, the manager does not currently fire `OnStop("eof")` when `Plane.Run` returns `nil`.

The streams implementation must include a small core fix:

- When the active plane exits with `runErr == nil`, capture the active session's `OnStop`, subtitle path, and session identity outside the lock.
- Clear `m.active`, `m.plane`, and `m.cancelFn` for that completed session.
- Transition the FSM with `EvEOF`.
- Fire `notifySessionStop(onStop, "eof")` after releasing `Manager.mu`.
- Preserve the existing pause behavior: `context.Canceled` from pause must not fire `OnStop`.
- Preserve error behavior: errors continue to fire `OnStop("error")`.

This fix is necessary for queue adapters and also makes the existing URL adapter's EOF reason handling reachable.

## UI

The adapter panel should expose:

- Provider status cards: name, catalog freshness, channel count, item count, last refresh error.
- Manual refresh button.
- Provider selector.
- Channel group list.
- Search/filter over visible channels.
- Channel rows with item count and play button.
- Now-playing/queue controls when streams owns the active session:
  - stop
  - replay channel
  - previous
  - next
  - refresh catalog
- A compact failure list for skipped videos.

The UI stays htmx/template based and follows the PR2 visual language when that lands. It should not require a JavaScript framework.

## Companion API Compatibility

The companion mini-remote spec renders foreign sessions read-only unless server-provided capabilities say otherwise. Streams should provide enough display/capability metadata for a later companion pass:

- provider display name
- channel name
- item title when yt-dlp resolves it
- `can_stop`
- `can_replay`
- `can_next`
- `can_previous`
- `can_pause`
- `can_seek` when duration is known

The first streams pass does not have to add mini-remote UI, but its internal queue/control API should avoid URL-adapter-only assumptions.

## Error Handling

- Remote manifest unavailable: use cached manifest or bundled defaults.
- Provider catalog unavailable: use cached catalog.
- No cached catalog: provider enters `StateError` with a retryable message.
- Unknown provider type: ignore with warning.
- Provider URL matches but channel is missing: return a clear error such as `stream provider channel not found: metal`.
- Playlist JSON too large: reject and keep last good cache.
- Malformed YouTube IDs: skip those items and count them in provider diagnostics.
- yt-dlp resolve failure for an item: record item failure and try the next item.
- Playback error: record failure and try the next item until `max_consecutive_failures`.
- Consecutive failure cap reached: stop queue, leave adapter in `StateError`, and show recent failure messages.
- Active queue preempted by another adapter: clear active queue state on `OnStop("preempted")`.
- Manual stop: clear active queue state on `OnStop("stopped")`.

## Security

- Remote manifest is data only.
- Remote JavaScript is not evaluated.
- Only `http` and `https` manifest/catalog URLs are accepted.
- Unknown provider types are ignored.
- Catalog and manifest sizes are bounded.
- URL patterns are matched against parsed URLs, not raw substring checks.
- `youtube-channel-json` only synthesizes YouTube watch URLs from validated IDs.
- Provider refresh honors context cancellation and bounded timeouts.
- Log output must not dump full remote catalog bodies.
- The adapter does not add LAN authentication. It follows the existing trusted-LAN UI posture.

## Testing Strategy

Unit tests:

- Manifest merge: bundled + cached + remote precedence.
- Remote provider with unknown type is ignored.
- Remote update that changes a bundled provider's type is ignored.
- Provider URL pattern matching.
- MTV `?channel=metal` resolution.
- MTV `?v=<id>` and `/s/<id>` resolution.
- Cartoon `?channel=heman` resolution.
- Queue construction for sequential, shuffle, and first-then-shuffle.
- Deterministic shuffle with injected RNG.
- Unknown playlist channel fallback into "Ungrouped".
- Malformed YouTube IDs skipped.
- Consecutive failure cap stops queue.
- Cache fallback on refresh failure.

Adapter tests:

- Adapter fields validate config.
- UI route renders providers and channels.
- Play channel route starts a fake core session.
- Next/previous preempt correctly.
- Stop clears queue.
- URL adapter handoff calls the streams resolver before normal URL routing.
- Disabled streams adapter produces a clear URL handoff error.

Core tests:

- Natural EOF fires `OnStop("eof")`.
- Pause cancellation still does not fire `OnStop`.
- Error still fires `OnStop("error")`.
- Stop still fires `OnStop("stopped")`.
- Preemption still fires `OnStop("preempted")`.

Fake HTTP server tests:

- Fetch remote manifest with `ETag`.
- Fetch provider playlist JSON with `ETag`.
- `304 Not Modified` keeps cached catalog.
- Malformed manifest leaves previous good manifest active.
- Malformed provider catalog leaves previous good catalog active.
- Oversized manifest/catalog is rejected.

## Rollout

1. Add core EOF lifecycle fix with tests.
2. Add `streams` adapter skeleton, config, cache, and manifest loading.
3. Add `youtube-channel-json` provider and bundled MTV/Cartoon definitions.
4. Add queue playback and controls.
5. Add Web UI panel.
6. Add URL adapter handoff.
7. Add README section and example provider links.

Each phase should be shippable behind `adapters.streams.enabled=false` until the full adapter is ready.

## Future Provider Types

The generic shape intentionally leaves room for later compiled provider types:

- `m3u-catalog` for IPTV-org and user M3U catalogs.
- `archive-collection` for Internet Archive public-domain movie queues.
- `media-rss` for video podcast feeds.
- `peertube` for public PeerTube channels/accounts.
- `yt-dlp-playlist` for YouTube playlists/channels and other yt-dlp playlist extractors.

These future types should go through their own design/spec pass. The v1 manifest can list only provider types compiled into the app.
