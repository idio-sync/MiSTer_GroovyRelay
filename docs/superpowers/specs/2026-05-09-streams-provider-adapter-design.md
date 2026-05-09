# Streams Provider Adapter Design

**Date:** 2026-05-09  
**Status:** Review fixes applied; implementation plan not started
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
- Treat remote manifest and catalog fetches as untrusted network input, with public-HTTPS and private-network protections before any URL is fetched.

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

## Priority Order

Implementation planning should preserve the brainstorming priority order:

1. Source compatibility: provider URLs and channel playback must work end to end before polish.
2. CRT/MiSTer-unique value: playback should keep the relay's SD/modeline bias and continuous channel feel.
3. Operator convenience: provider refresh, browser UI, and clear errors should make the feature pleasant to run.
4. Demo value: bundled MTV Rewind and Cartoon Rewind should show the generic model clearly.
5. Reliability polish: diagnostics, stale-cache status, and skipped-item reporting should follow once the playback spine is stable.

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

Provider metadata, including names, groups, URL rules, default play modes, and per-channel play mode overrides, comes from bundled provider definitions and the GitHub manifest. For MTV Rewind, the bundled metadata is a checked-in static snapshot derived from the public channel configuration during development; runtime refreshes use the data-only manifest and playlist JSON, and v1 never executes or evaluates `channels-config.js`. If a playlist contains a channel key missing from metadata, the adapter surfaces it under an "Ungrouped" group with a default shuffle mode.

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
manifest_request_timeout_seconds = 10
catalog_request_timeout_seconds = 20
youtube_format = "bv*[height<=480]+ba/b[height<=480]/bv*+ba/b"
allow_remote_manifest = true
allow_cached_remote_manifest = false
allow_local_manifest_urls = false
remote_provider_allowed_hosts = []

[adapters.streams.providers.mtv-rewind]
disabled = false
catalog_refresh_hours = 12
```

Notes:

- `enabled=false` avoids unexpected background network traffic until the operator opts in.
- `allow_remote_manifest=false` disables remote manifest fetches and ignores cached remote manifest overlays. It may still use cached provider catalogs for bundled provider definitions.
- `allow_cached_remote_manifest=true` is an explicit escape hatch for using the last good remote manifest overlay while remote fetching is disabled.
- `allow_local_manifest_urls=false` rejects loopback, private LAN, link-local, multicast, and unspecified manifest/catalog targets even when they use `http` or `https`. Operators who intentionally host provider data on their LAN must opt in explicitly.
- `remote_provider_allowed_hosts` is optional. When non-empty, remote-added provider catalog URLs must resolve to one of these hostnames after redirects; bundled providers are still subject to the private-network protections in Security.
- `[adapters.streams.providers.<id>]` entries are user overrides. `disabled=true` removes that provider from URL matching, UI lists, refreshes, and cached catalog loading. `catalog_refresh_hours` overrides the provider default after validation.
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
      "url_rules": [
        {
          "id": "mtv-player-channel",
          "schemes": ["https"],
          "hosts": ["wantmymtv.vercel.app", "wantmymtv.xyz"],
          "path": "/player.html",
          "target": "channel",
          "query_param": "channel"
        },
        {
          "id": "mtv-player-video",
          "schemes": ["https"],
          "hosts": ["wantmymtv.vercel.app", "wantmymtv.xyz"],
          "path": "/player.html",
          "target": "item",
          "query_param": "v"
        },
        {
          "id": "mtv-short-video",
          "schemes": ["https"],
          "hosts": ["wantmymtv.vercel.app", "wantmymtv.xyz"],
          "path_prefix": "/s/",
          "target": "item"
        }
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

Concrete schema:

```go
type Manifest struct {
	Version   int
	Providers []ProviderDefinition
}

type ProviderDefinition struct {
	ID                  string
	Type                string
	DisplayName         string
	BaseURL             string
	PlaylistURL         string
	URLRules            []URLRule
	DefaultChannel      string
	DefaultPlayMode     PlayMode
	CatalogRefreshHours *int
	Groups              []GroupDefinition
	Channels            []ChannelDefinition
}

type URLRule struct {
	ID         string
	Schemes    []string
	Hosts      []string
	Path       string
	PathPrefix string
	Target     string // "channel" or "item"
	QueryParam string // required for query extraction, omitted for path-prefix extraction
}

type CacheMetadata struct {
	ETag        string
	LastModified string
	FetchedAt   time.Time
	SourceURL   string
	Schema      int
	SHA256      string
}
```

Validation rules:

- `version` must be `1`. Unsupported versions are rejected as a whole without replacing the active manifest.
- Provider, group, channel, and rule IDs must match `^[a-z0-9][a-z0-9-]{0,63}$`.
- Provider `display_name`, `type`, `playlist_url`, `default_channel`, `default_play_mode`, and at least one `url_rule` are required.
- `type` must name a compiled provider type. Unknown provider types are ignored with a diagnostic.
- `playlist_url` and `base_url`, when present, must pass the remote fetch security checks before the provider is accepted.
- Duplicate provider IDs reject the later provider update. Duplicate group, channel, or URL rule IDs reject that provider update.
- A channel with an unknown `group_id` is accepted but moved to "Ungrouped" with a diagnostic.
- Unsupported play modes reject the affected channel override; unsupported provider defaults reject that provider update.
- Provider counts, group counts, channel counts, URL rule counts, and item counts are bounded by config constants before the new snapshot can replace the active snapshot.

Merge rules:

- Bundled providers are the baseline.
- Cached remote manifest overlays bundled providers only when `allow_remote_manifest=true` or `allow_cached_remote_manifest=true`.
- Remote manifest overlays cached and bundled providers after a successful fetch and full validation.
- User TOML can disable a provider by ID.
- A remote provider with an unknown `type` is ignored.
- A remote update for a bundled provider must keep the same `type`; otherwise that provider update is ignored.
- Remote providers may be added when their `type` is compiled into the app.
- The manifest is data only. There are no regex replacements, JavaScript snippets, shell commands, dynamic templates, or code hooks.
- Remote manifest and catalog URLs must use `https` unless the provider is bundled or the operator explicitly enables local manifest URLs. After DNS resolution, remote-sourced URLs that resolve to loopback, private, link-local, multicast, or unspecified addresses are rejected unless that local opt-in is enabled.

URL rule grammar:

- `schemes` and `hosts` are required. Remote manifest entries must use `https` unless local URLs are explicitly allowed.
- Hosts match exactly after parsing, lowercasing, default-port stripping, and IDNA normalization. No wildcard hosts are supported in v1.
- Exactly one of `path` or `path_prefix` is required. `path` is an exact normalized path match. `path_prefix` extracts the next single path segment after the prefix; extra path segments make the rule invalid for that URL.
- `target` is required and must be `channel` or `item`.
- `query_param` is required for exact-path query extraction. The parameter must appear exactly once and have a non-empty value after URL decoding.
- Additional unknown query keys are ignored. Fragments are ignored. Userinfo in provider URLs is rejected.
- Rules run in provider order, then rule order. The first successful extraction wins. A route match with an invalid extracted channel/item returns a streams error rather than falling back to generic URL casting.

Fetch behavior:

- On adapter start, synchronously load bundled definitions, allowed cached remote manifest overlays, and cached catalogs for active provider definitions. Startup must not block on the network.
- When enabled, start a background refresh loop after startup. Manual refresh uses the same refresh path but returns the final status to the UI/API caller.
- Use `ETag` and `If-None-Match` for the remote manifest and provider catalog URLs.
- Use `manifest_request_timeout_seconds` for manifest fetches and `catalog_request_timeout_seconds` for catalog fetches. Redirects are capped at three hops and every redirected target must pass the same URL security checks before following it.
- Failed background refreshes use bounded exponential backoff with jitter, capped by the configured refresh interval. They never clear the last good in-memory snapshot.
- Save the last good manifest and catalog snapshots under `data_dir/streams/`.
- Persist cache metadata alongside each snapshot: `etag`, optional `last_modified`, `fetched_at`, `source_url`, schema version, and a snapshot SHA-256. Restarted bridges use this metadata for conditional requests.
- Use atomic writes for cache and metadata files.
- On `304 Not Modified`, keep the in-memory snapshot and update freshness metadata.
- Purging `data_dir/streams/manifest*` removes remote manifest influence. With `allow_remote_manifest=false`, cached remote manifest files are ignored even if present.
- A newly fetched manifest or catalog replaces the active snapshot only after it fully validates. Active queues keep their already-built `[]StreamItem` snapshot and are never mutated by catalog refresh; replay or a new channel start uses the latest active catalog.
- Provider status exposes freshness, source (`bundled`, `cached`, or `remote`), last refresh time, current ETag, stale/error state, and the last user-safe error message.

### Remote Data Trust Boundary

Remote manifest data is inert, but URLs inside it are still security-sensitive because the bridge fetches them from the operator's LAN. Remote-sourced manifest and catalog URLs must pass scheme, host, DNS, IP range, size, and timeout validation before any HTTP request is made. This validation is separate from playback URLs synthesized from validated YouTube IDs.

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
	StartResolvedStream(ctx context.Context, res StreamURLResolution) (StreamStartResult, error)
}

type StreamURLResolution struct {
	AdapterRef string
	ProviderID string
	ChannelID  string
	ItemID     string
}

type StreamStartResult struct {
	AdapterRef string
	ProviderID string
	ChannelID  string
	ItemID     string
}
```

Before URL's direct/yt-dlp route decision, it asks the resolver whether the URL belongs to streams. `ResolveStreamURL` is pure matching/validation and must not start playback. When it returns `matched=true`, the URL adapter calls `StartResolvedStream`; that method is the only handoff point that starts native streams playback through the streams adapter.

Cases:

- No match: URL adapter continues existing behavior.
- Match and streams enabled: URL adapter calls `StartResolvedStream`, streams starts native playback, and URL adapter returns success with the streams `AdapterRef`.
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
	ItemToken     uint64
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

End-of-queue behavior:

- Provider channel queues loop by default. Single-item ad hoc queues created for a `?v=<id>` that is not found in any catalog stop on EOF instead of replaying forever.
- `sequential` wraps from the final item back to index 0 so channels remain continuous.
- `shuffle` plays every item in the shuffled cycle once, then creates a new shuffled cycle and continues.
- `first_then_shuffle` plays item 0 only when the channel is first started or explicitly replayed. After the shuffled tail is exhausted, the adapter reshuffles items 1..n and continues without replaying item 0. A single-item `first_then_shuffle` channel behaves like sequential replay of that one item.
- Manual `Next` wraps according to the same rules. Manual `Previous` moves to the previous queue item within the current cycle, wrapping at the cycle start.

Playback:

1. Build the next `StreamItem`.
2. Resolve it through the yt-dlp resolver.
3. Build `core.SessionRequest` with:
   - resolved video URL and headers
   - audio URL and audio headers when yt-dlp returns split DASH streams
   - `AdapterRef` as a streams session/item reference
   - `DirectPlay=true`
   - `Capabilities{CanPause: true, CanSeek: resolvedCanSeek}`
   - `OnStop` closure capturing queue generation, item token, session ID, and item identity
4. Call `core.Manager.StartSession`.

`resolvedCanSeek` defaults to `false` and becomes `true` only when the resolver result or probe metadata gives enough duration/seekability information for the manager to service seek requests. The streams UI and future companion API expose seek only when the active session capability is true and a known duration is available.

Next/previous:

- `Next` advances index and starts a new item, preempting the active session.
- `Previous` moves to the previous queue item, wrapping at the start.
- Manual next/previous cancels any in-flight item resolution before starting the requested item.

EOF advancement:

- On `OnStop("eof")`, the adapter advances to the next queue item only if the captured generation, item token, session ID, and item ID still match the active queue.
- On `OnStop("preempted")` or `OnStop("stopped")`, the adapter does not auto-advance.
- On `OnStop("error")`, the adapter records item failure and may skip to the next item until `max_consecutive_failures` is reached.

### Concurrency And Ownership

Every item start increments `ItemToken`. Every `OnStop` closure captures queue generation, item token, session id, item id, and adapter ref at request construction time. Before mutating queue state for any reason (`eof`, `preempted`, `stopped`, or `error`), the callback rechecks that all captured identifiers still match the current active queue.

Manual next/previous and replay increment the queue generation before starting the replacement item. The old item's later `OnStop("preempted")` is then stale and must not clear or advance the new queue. If `OnStop("preempted")` still matches the active queue identifiers, treat it as foreign-adapter preemption and clear the active queue. Manual stop clears only when the captured identifiers still match, so an old stop callback cannot erase a newer stream session.

## Required Core Lifecycle Fix

Current `core.Manager` already lets adapters provide `SessionRequest.OnStop`, and the URL adapter's `makeOnStop` handles reason `"eof"`. However, the manager does not currently fire `OnStop("eof")` when `Plane.Run` returns `nil`.

The streams implementation must include a small core fix:

- When the active plane exits with `runErr == nil`, read the active session's `OnStop`, subtitle path, and session identity while holding `Manager.mu`.
- Clear `m.active`, `m.plane`, and `m.cancelFn` for that completed session.
- Transition the FSM with `EvEOF`.
- Release `Manager.mu`, then remove subtitle files and fire `notifySessionStop(onStop, "eof")`.
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

Routes live under the existing adapter route mount, `/ui/adapter/streams/`. POST routes are wrapped by the UI server's CSRF middleware. Handlers return htmx fragments by default and JSON when `Accept: application/json` is sent.

| Method | Path | Purpose | Request | Response |
|---|---|---|---|---|
| `GET` | `panel` | Render the streams panel | none | HTML fragment |
| `GET` | `status` | Current provider and queue status | optional `provider_id` | HTML or JSON status |
| `GET` | `providers` | List provider/channel catalog summaries | optional `q`, `provider_id` | JSON catalog summary |
| `POST` | `refresh` | Refresh all providers or one provider | optional `provider_id` | status fragment or JSON |
| `POST` | `play` | Start a channel or item | `provider_id`, optional `channel_id`, optional `item_id` | status fragment or JSON start result |
| `POST` | `replay` | Rebuild and restart current channel from the latest catalog | none | status fragment or JSON |
| `POST` | `next` | Advance current streams queue | none | status fragment or JSON |
| `POST` | `previous` | Move current streams queue backward | none | status fragment or JSON |
| `POST` | `stop` | Stop the active streams-owned session | none | status fragment or JSON |

All mutating routes verify that streams owns the active queue before changing it, except `play`, which may preempt another adapter through the normal `core.Manager.StartSession` path. The routes follow the app's existing trusted-LAN UI posture and do not add separate authentication in v1.

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
- Active queue preempted by another adapter: clear active queue state on `OnStop("preempted")` only when the captured generation/session/item still matches the active queue.
- Manual stop: clear active queue state on `OnStop("stopped")` only when the captured generation/session/item still matches the active queue.

## Security

- Remote manifest is data only.
- Remote JavaScript is not evaluated.
- Bundled manifest/catalog URLs may use `http` or `https`, but remote manifest overlays must use `https` unless the operator sets `allow_local_manifest_urls=true`.
- Remote-sourced manifest/catalog targets that resolve to loopback, private LAN, link-local, multicast, or unspecified addresses are rejected after DNS resolution unless local manifest URLs are explicitly allowed.
- Unknown provider types are ignored.
- Catalog and manifest sizes are bounded.
- URL rules are matched against parsed URLs, not raw substring checks.
- `youtube-channel-json` only synthesizes YouTube watch URLs from validated IDs.
- Provider refresh honors context cancellation and bounded timeouts.
- Log output must not dump full remote catalog bodies.
- The adapter does not add LAN authentication. It follows the existing trusted-LAN UI posture.

## Testing Strategy

Unit tests:

- Manifest merge: bundled + cached + remote precedence.
- Remote provider with unknown type is ignored.
- Remote update that changes a bundled provider's type is ignored.
- Provider URL rule matching.
- Parsed URL rule non-matches for unrelated paths, hosts, query-only matches, userinfo, and raw substring traps.
- MTV `?channel=metal` resolution.
- MTV `?v=<id>` and `/s/<id>` resolution.
- Cartoon `?channel=heman` resolution.
- Queue construction for sequential, shuffle, and first-then-shuffle.
- Deterministic shuffle with injected RNG.
- Unknown playlist channel fallback into "Ungrouped".
- Malformed YouTube IDs skipped.
- Consecutive failure cap stops queue.
- Cache fallback on refresh failure.
- End-of-queue behavior for sequential, shuffle, and first-then-shuffle.
- Stale `OnStop` callbacks from manual next/previous do not mutate the new active queue.
- Foreign-adapter preemption clears only the matching active queue.
- `allow_remote_manifest=false` ignores cached remote manifests.

Adapter tests:

- Adapter fields validate config.
- UI route renders providers and channels.
- Play channel route starts a fake core session.
- Next/previous preempt correctly.
- Stop clears queue.
- URL adapter handoff calls pure `ResolveStreamURL` before normal URL routing.
- Matched URL handoff calls `StartResolvedStream` exactly once and does not start playback from `ResolveStreamURL`.
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
- Restarted bridge reuses persisted `ETag` metadata for conditional manifest and catalog fetches.
- Remote manifest/catalog URLs that resolve to private, loopback, link-local, multicast, or unspecified addresses are rejected by default.
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
