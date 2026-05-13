# Toonami Aftermath Streams Design

**Date:** 2026-05-13  
**Status:** Review fixes applied; implementation plan not started
**Scope:** Add Toonami Aftermath to the Streams page as a bundled provider with four direct HLS channels.

## Problem

The Streams adapter currently exposes catalog-backed providers such as MTV Rewind and Cartoon Rewind. Those providers publish YouTube-ID playlist JSON, so the adapter builds queues of YouTube page URLs and resolves each item through yt-dlp before handing media to the core playback manager.

Toonami Aftermath fits the same user experience, but not the same catalog format. Its public channels are direct HLS playlists, currently exposed as `.m3u8` URLs. The URL adapter can already play those URLs one at a time, but that does not give the operator a native Streams provider card with channel buttons, current provider/channel status, and the same stop/replay/control surface as MTV Rewind and Cartoon Rewind.

## Goals

- Add a bundled Streams provider named `Toonami Aftermath`.
- Show four channel buttons under that provider:
  - `East`
  - `West`
  - `Movies`
  - `Radio`
- Reuse the existing Streams UI, queue ownership, control routes, core manager integration, FFmpeg pipeline, and Groovy data plane.
- Skip yt-dlp for Toonami Aftermath items because they are already direct HLS media URLs.
- Keep the implementation reusable for future fixed live stream providers without turning this pass into a general IPTV browser.
- Keep remote manifest behavior safe: untrusted remote provider data must not be able to inject arbitrary direct media URLs for FFmpeg to fetch.

## Non-Goals

- Program guide or schedule data.
- Exact time-sync with the website player.
- Chat, Discord, community features, or website embedding.
- Browser automation against `toonamiaftermath.com`.
- A general M3U/IPTV import feature.
- Native URL handoff for every Toonami Aftermath web page. Direct `.m3u8` URLs can still be played through the URL adapter.
- Remote-manifest creation of new direct-stream providers in this pass.

## Decisions Captured During Brainstorm

| Topic | Decision |
|---|---|
| Product shape | Add one `Toonami Aftermath` Streams provider, parallel to `MTV Rewind` and `Cartoon Rewind` |
| Channel set | Include East, West, Movies, and Radio |
| Provider implementation | Add a small direct-stream provider type |
| Playback path | Direct HLS items bypass yt-dlp and enter `core.Manager` directly |
| Remote safety | Direct-stream providers are bundled-only in v1 |
| UI changes | No custom layout; use the existing Streams provider/channel rendering |

## Current Toonami Aftermath Inputs

The design assumes these current public HLS endpoints:

| Channel | Stream URL |
|---|---|
| East | `http://api.toonamiaftermath.com:3000/est/playlist.m3u8` |
| West | `http://api.toonamiaftermath.com:3000/pst/playlist.m3u8` |
| Movies | `http://api.toonamiaftermath.com:3000/movies/playlist.m3u8` |
| Radio | `http://api.toonamiaftermath.com:3000/radio/playlist.m3u8` |

These were checked during design on 2026-05-13 with HTTP `HEAD` requests. Each returned `200 OK` and `Content-Type: application/vnd.apple.mpegurl`. A follow-up playlist-body check on 2026-05-13 showed ordinary HLS master playlists, child playlists, and relative `.ts` media segments. The Radio endpoint currently advertises both video and audio codecs, so this design treats Radio as a normal video HLS channel.

The plain HTTP scheme and `:3000` port are intentional for this bundled provider because those are the current public Toonami Aftermath HLS endpoints. This exception is not a general permission for remote manifests or operator-provided direct streams. The bundled Toonami definition must use only `api.toonamiaftermath.com:3000` with the four paths listed above.

## Provider Model

Add a new compiled bundled provider type named `direct-streams`.

Unlike `youtube-channel-json`, this type does not fetch a playlist JSON file and does not synthesize YouTube watch URLs. It builds a catalog directly from bundled provider/channel definitions. Each channel definition carries one fixed media URL and becomes a normal Streams `Channel` with a single `StreamItem`.

Extend `ChannelDefinition` with an optional URL field:

```go
type ChannelDefinition struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty"`
    GroupID     string   `json:"group_id,omitempty"`
    Icon        string   `json:"icon,omitempty"`
    URL         string   `json:"url,omitempty"`
    PlayMode    PlayMode `json:"play_mode,omitempty"`
    Order       int      `json:"order"`
}
```

The Toonami provider definition should be bundled in `internal/adapters/streams/assets.go`. It should not be mirrored in `docs/streams/providers.json` in v1 because that file is the hosted remote manifest surface and `direct-streams` is bundled-only. Hosted-manifest parity tests should compare only remote-eligible provider types and should assert that bundled-only `direct-streams` providers are absent from the hosted manifest.

Implementation must keep separate bundled and remote provider-type allowlists. `direct-streams` is valid when constructing bundled definitions and bundled startup catalogs, but remote and cached manifest validation must drop `direct-streams` entries before applying the ordinary `playlist_url`, `url_rules`, and fetchable-catalog requirements used by remote catalog providers.

Suggested bundled definition:

```text
Provider ID: toonami-aftermath
Display name: Toonami Aftermath
Type: direct-streams
Default channel: east
Default play mode: sequential (metadata default; single direct queues still use internal loopNone)
Groups: Live Channels
Channels:
  east   -> East   -> http://api.toonamiaftermath.com:3000/est/playlist.m3u8
  west   -> West   -> http://api.toonamiaftermath.com:3000/pst/playlist.m3u8
  movies -> Movies -> http://api.toonamiaftermath.com:3000/movies/playlist.m3u8
  radio  -> Radio  -> http://api.toonamiaftermath.com:3000/radio/playlist.m3u8
```

## Catalog Building

Add `provider_direct_streams.go` with a builder that:

- accepts only bundled `direct-streams` provider definitions;
- requires each surfaced channel to have a non-empty `URL`;
- accepts only `http` and `https` media URLs;
- rejects userinfo in media URLs;
- requires a host;
- for the bundled Toonami provider, requires `api.toonamiaftermath.com:3000` and one of the four approved playlist paths;
- records each channel's configured name, group, order, description, icon, and play mode;
- creates one `StreamItem` per channel, using the channel ID as the item ID, the channel name as the title, the channel URL as the item URL, and `Direct: true`;
- skips channels with invalid definitions by returning a provider build error, so broken bundled data is caught by tests and startup behavior rather than silently hidden.

The builder should not fetch the HLS playlist during catalog build. The actual media probe/fetch remains FFmpeg/core playback's responsibility, matching direct URL playback behavior.

The synthesized `StreamItem` must live in `Channel.Items` in the provider catalog, not only in a transient play request. Replay currently rebuilds queues from the latest catalog channel, so direct-stream Replay depends on catalog items being real.

`ChannelDefinition.URL` belongs only to `direct-streams`. Non-direct builders, including `youtube-channel-json` and `channelFromDefinition`, must ignore it so remote manifest metadata cannot smuggle a direct media URL into YouTube-backed catalogs.

## Manifest Validation And Merge

The existing `validateProviderDefinition` path requires `playlist_url`, at least one `url_rule`, and a supported default play mode because it validates remote catalog providers. Do not route bundled `direct-streams` definitions through that remote validation branch unchanged.

Implementation should split provider handling as follows:

- bundled manifest construction can include `direct-streams` and validates it through the direct-stream builder/validator;
- remote and cached manifest load paths filter out providers with type `direct-streams` before `validateProviderDefinition` enforces fetchable-catalog fields;
- `mergeManifests` must reject any remote or cached overlay for a bundled provider whose bundled type is `direct-streams`, including `toonami-aftermath`;
- `mergeManifests` must also reject remote overlays that attempt to change any bundled provider's type to or from `direct-streams`;
- remote overlays must not remove bundled providers. A bad remote manifest can fail or be ignored, but it cannot make the bundled Toonami provider disappear.

This keeps the direct-stream provider type off the remote manifest trust boundary and names the exact point where remote channel URL replacement is blocked.

## Catalog Refresh Integration

`refreshCatalogsDefault` currently fetches every provider through `fetchProviderPlaylist`, which requires `PlaylistURL`. `direct-streams` must bypass that path.

Refresh behavior should be:

- startup snapshots build the Toonami catalog directly from the bundled definition, without a seed file and without cache reads;
- background and manual catalog refreshes rebuild `direct-streams` catalogs locally from bundled definitions and do not call `fetchProviderPlaylist`;
- direct-stream catalog refresh does not write catalog cache entries because there is no remote catalog body;
- `RefreshNow(ctx, "toonami-aftermath")` succeeds even when `allow_remote_manifest=false`, because it is a local rebuild;
- remote manifest refresh may update remote-eligible providers, but must preserve the current bundled direct-stream catalog.

## Playback Flow

Current Streams playback always resolves a stream item through the yt-dlp resolver. Toonami Aftermath needs a direct branch:

1. Operator clicks a Toonami Aftermath channel.
2. Existing Streams route builds an `ActiveQueue` for `toonami-aftermath`.
3. The queue contains one direct item with the channel's `.m3u8` URL.
4. `playCurrent` detects the item as direct media.
5. Streams builds a `core.SessionRequest` with:
   - `StreamURL` set to the `.m3u8` URL;
   - no yt-dlp headers;
   - `DirectPlay: true`;
   - `Source: "streams"`;
   - `Capabilities: core.Capabilities{CanPause: false, CanSeek: false}`;
   - a non-zero `MediaInputPolicy` described below;
   - title set to `Toonami Aftermath / <Channel>`.
6. Core starts FFmpeg against the HLS URL and streams through the existing Groovy data plane.

The direct branch should be explicit. Add a `Direct bool` field to `StreamItem`, set it only from trusted catalog builders, and branch on that field during playback. Do not infer directness only from the URL string, because existing YouTube-backed items also carry page URLs.

Single-item direct-stream queues should use the existing internal `loopNone` behavior rather than public `sequential` looping. Next and Previous stay disabled for Toonami channels; Replay reconnects to the current channel URL. Do not add a new public play mode only for this provider. The status view must also avoid advertising Pause for direct live HLS queues, because core pause resumes by respawning FFmpeg with a time offset, which is not a useful operation against live manifests.

## Direct HLS Input Policy

Direct HLS playback must populate `core.SessionRequest.MediaInputPolicy` as defense in depth:

```go
core.MediaInputPolicy{
    ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"},
    DisableRedirects:  true,
    DisableReconnect:  true,
    RWTimeout:         5 * time.Second,
    BlockedHeaders:    []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer"},
}
```

This whitelist intentionally matches the HLS-capable DLNA policy breadth rather than only the current observed `http`/`tcp` protocols. FFmpeg's HLS demuxer may need `https`, `tls`, `crypto`, or `file` for redirects, nested playlists, or AES-128 key handling even when the top-level URL is HTTP.

`DisableRedirects` is a contract marker in the current FFmpeg policy and does not emit a redirect-disabling argv flag. This design does not claim full host-level validation of mutable HLS child resources. The safety boundary is that `direct-streams` is bundled-only, restricted to the known Toonami host and paths, carries no input headers, and is not available to remote manifests. Future untrusted direct-stream or IPTV support would need a validating HLS fetch/cache/proxy path before FFmpeg sees child playlists or segments.

## Remote Manifest Safety

The current remote manifest model allows data-only provider overlays for known compiled provider types. That is appropriate for `youtube-channel-json` because the remote data points to playlist JSON, and catalog fetching goes through `secureFetcher` protections before yt-dlp sees individual YouTube page URLs.

Direct-stream providers are different: their item URLs are media inputs that FFmpeg will fetch. In v1, direct-stream providers must be bundled-only:

- remote-only providers with type `direct-streams` are ignored during manifest merge;
- remote overlays must not change the provider type of a bundled provider;
- remote overlays for `toonami-aftermath` are ignored entirely in v1;
- cached remote manifests follow the same rules as freshly fetched remote manifests;
- tests should prove a remote manifest cannot add a new `direct-streams` provider that appears in the active catalog.

This keeps the new feature from becoming an untrusted FFmpeg URL injection path.

## Error Handling

- If the bundled provider definition is invalid, startup/catalog build should fail in tests and surface the provider error during manual refresh in runtime paths.
- If a Toonami HLS endpoint is temporarily unreachable, the core/FFmpeg playback start should fail using the existing Streams playback error path.
- Because each channel queue has one item, Next and Previous can remain disabled for Toonami channels.
- Replay should reconnect to the current channel URL.
- Radio currently advertises a video stream. If it later becomes audio-only, do not silently depend on core defaults; add an explicit `MediaKindMusic` + `VisualizerRequest` design or remove Radio from the bundled provider until audio-only handling is specified.
- Endpoint availability checks are manual diagnostics only. Do not add live network tests for Toonami Aftermath URLs to the normal test suite.

## UI

No custom Streams UI is needed. The existing provider renderer should show:

```text
Toonami Aftermath
  Live Channels
    East    1 item
    West    1 item
    Movies  1 item
    Radio   1 item
```

Provider ordering can remain alphabetical because `statusView` currently sorts provider IDs. If a future design adds provider ordering, Toonami Aftermath can be positioned deliberately then.

Update README references from "Cartoon Rewind, MTV Rewind" to include Toonami Aftermath.

## Testing

Add focused tests for:

- `direct-streams` catalog building creates four one-item channels.
- invalid direct-stream channel URLs are rejected.
- direct-stream catalog building rejects userinfo and missing hosts.
- non-direct builders ignore `ChannelDefinition.URL`.
- startup snapshot includes `toonami-aftermath`.
- hosted `docs/streams/providers.json` parity applies only to remote-eligible provider types, and the hosted manifest omits `toonami-aftermath`.
- direct-stream catalog refresh bypasses `fetchProviderPlaylist` and succeeds without `PlaylistURL`.
- `RefreshNow(ctx, "toonami-aftermath")` rebuilds the local direct catalog even when remote manifest fetches are disabled.
- remote manifests cannot introduce remote-only `direct-streams` providers.
- remote manifests and cached remote manifests cannot change `toonami-aftermath` channel URLs.
- remote manifests and cached remote manifests cannot change a bundled provider's type to or from `direct-streams`.
- remote manifests and cached remote manifests cannot remove bundled providers.
- Streams playback for a direct item skips the yt-dlp resolver and passes the HLS URL to `core.StartSession`.
- Streams playback for a direct item proves yt-dlp headers cannot leak into `InputHeaders` or `AudioInputHeaders`.
- Streams playback for a direct item sets the expected `MediaInputPolicy`.
- Streams playback for a direct item proves `BlockedHeaders` are filtered at the FFmpeg argv boundary.
- single-item direct-stream queues disable Next, Previous, and Pause while keeping Replay available.
- Existing YouTube-backed Streams playback still uses yt-dlp.
- Streams UI includes `Toonami Aftermath`, `East`, `West`, `Movies`, and `Radio`.
- README mentions Toonami Aftermath as a built-in Streams catalog source.
- Toonami endpoint checks, if retained, are optional/manual and are not part of CI.

## Implementation Notes

Likely touched files:

- `internal/adapters/streams/provider.go`
- `internal/adapters/streams/assets.go`
- `internal/adapters/streams/refresh.go`
- `internal/adapters/streams/manifest.go`
- `internal/adapters/streams/playback.go`
- `internal/adapters/streams/provider_direct_streams.go`
- Streams UI/status capability rendering for direct live queues
- relevant `*_test.go` files in `internal/adapters/streams`
- `docs/streams/providers.json` tests and/or comments, without adding Toonami as a remote manifest provider
- `README.md`

The direct-stream catalog builder can have a narrower signature than `buildYouTubeChannelCatalog` because it has no raw catalog body. The `buildProviderCatalog` dispatch should make that explicit, rather than forcing `direct-streams` through a fake `raw []byte` contract.

Also sweep README/troubleshooting/status-dashboard copy for references to the built-in Streams provider list so Toonami Aftermath appears anywhere the operator discovers bundled stream sources.

The implementation should keep unrelated worktree changes untouched.
