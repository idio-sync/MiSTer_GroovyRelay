# Toonami Aftermath Streams Design

**Date:** 2026-05-13  
**Status:** Design approved; implementation plan not started  
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

These were checked during design on 2026-05-13 with HTTP `HEAD` requests. Each returned `200 OK` and `Content-Type: application/vnd.apple.mpegurl`.

## Provider Model

Add a new compiled provider type named `direct-streams`.

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

The Toonami provider definition should be bundled in `internal/adapters/streams/assets.go` and mirrored in `docs/streams/providers.json` for hosted manifest parity. Because remote direct-stream providers are not supported in v1, the hosted JSON entry is documentation/parity for bundled data, not a way to introduce arbitrary new direct media providers at runtime.

Suggested bundled definition:

```text
Provider ID: toonami-aftermath
Display name: Toonami Aftermath
Type: direct-streams
Default channel: east
Default play mode: sequential
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
- uses each channel's configured name, group, order, description, icon, and play mode;
- creates one `StreamItem` per channel, using the channel ID as the item ID, the channel name as the title, and the channel URL as the item URL;
- skips channels with invalid definitions by returning a provider build error, so broken bundled data is caught by tests and startup behavior rather than silently hidden.

The builder should not fetch the HLS playlist during catalog build. The actual media probe/fetch remains FFmpeg/core playback's responsibility, matching direct URL playback behavior.

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
   - `Capabilities: core.Capabilities{CanPause: true, CanSeek: false}`;
   - title set to `Toonami Aftermath / <Channel>`.
6. Core starts FFmpeg against the HLS URL and streams through the existing Groovy data plane.

The direct branch should be explicit. Add a `Direct bool` field to `StreamItem`, set it only from trusted catalog builders, and branch on that field during playback. Do not infer directness only from the URL string, because existing YouTube-backed items also carry page URLs.

## Remote Manifest Safety

The current remote manifest model allows data-only provider overlays for known compiled provider types. That is appropriate for `youtube-channel-json` because the remote data points to playlist JSON, and catalog fetching goes through `secureFetcher` protections before yt-dlp sees individual YouTube page URLs.

Direct-stream providers are different: their item URLs are media inputs that FFmpeg will fetch. In v1, direct-stream providers must be bundled-only:

- remote-only providers with type `direct-streams` are ignored during manifest merge;
- remote overlays must not change the provider type of a bundled provider;
- remote overlays for `toonami-aftermath` are ignored entirely in v1;
- tests should prove a remote manifest cannot add a new `direct-streams` provider that appears in the active catalog.

This keeps the new feature from becoming an untrusted FFmpeg URL injection path.

## Error Handling

- If the bundled provider definition is invalid, startup/catalog build should fail in tests and surface the provider error during manual refresh in runtime paths.
- If a Toonami HLS endpoint is temporarily unreachable, the core/FFmpeg playback start should fail using the existing Streams playback error path.
- Because each channel queue has one item, Next and Previous can remain disabled for Toonami channels.
- Replay should restart the current channel URL.
- Radio may be audio-only. The existing core/audio behavior should handle that path; no Toonami-specific visualizer code is part of this design.

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
- startup snapshot includes `toonami-aftermath`.
- hosted `docs/streams/providers.json` remains in parity with bundled definitions.
- remote manifests cannot introduce remote-only `direct-streams` providers.
- Streams playback for a direct item skips the yt-dlp resolver and passes the HLS URL to `core.StartSession`.
- Existing YouTube-backed Streams playback still uses yt-dlp.
- Streams UI includes `Toonami Aftermath`, `East`, `West`, `Movies`, and `Radio`.
- README mentions Toonami Aftermath as a built-in Streams catalog source.

## Implementation Notes

Likely touched files:

- `internal/adapters/streams/provider.go`
- `internal/adapters/streams/assets.go`
- `internal/adapters/streams/refresh.go`
- `internal/adapters/streams/manifest.go`
- `internal/adapters/streams/playback.go`
- `internal/adapters/streams/provider_direct_streams.go`
- relevant `*_test.go` files in `internal/adapters/streams`
- `docs/streams/providers.json`
- `README.md`

The implementation should keep unrelated worktree changes untouched.
