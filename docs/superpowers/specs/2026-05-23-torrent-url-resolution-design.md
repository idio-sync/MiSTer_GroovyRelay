# Torrent URL Resolution Design

**Date:** 2026-05-23  
**Status:** Draft for review  
**Scope:** Let HTTP(S) URLs that point to `.torrent` metainfo resolve through the Torrent adapter and stream through the existing torrent playback path.

## Problem

The Torrent adapter can currently play magnet links and uploaded `.torrent` files. If an operator pastes an HTTP(S) URL to a `.torrent` file, the bridge treats it like an ordinary URL source. That does not fetch metainfo, does not join the torrent swarm, and does not stream the selected file.

The previous torrent design intentionally made remote `.torrent` fetching a v1 non-goal because it creates a new server-side fetch surface. This feature adds that fetch surface with strict bounds: only explicit torrent metadata URLs are accepted, fetching is capped, redirects are validated, and the downloaded bytes feed the same metainfo parser used by uploads.

## Goals

- Accept public `http://` and `https://` URLs that resolve to BitTorrent metainfo.
- Reuse the existing Torrent adapter lifecycle, traffic acknowledgement gate, torrent client, file selection, local media route, and cleanup behavior.
- Keep normal media URLs owned by the URL adapter.
- Reject ambiguous or unsafe remote fetches before any torrent client activity starts.
- Avoid logging raw remote URLs with credentials or sensitive query strings.
- Cover the feature with unit tests before implementation.

## Non-Goals

- Torrent search, index browsing, RSS, or catalog discovery.
- Fetching arbitrary URLs and sniffing unbounded bodies.
- Supporting local/private HTTP torrent metadata hosts in this version. Operators can upload local `.torrent` files directly.
- Adding a new global source router.
- Changing magnet or uploaded `.torrent` behavior.
- Adding new torrent legality or copyright enforcement. The operator remains responsible for the content they provide.

## User-Facing Behavior

The global Cast drawer gains a third Torrent tab:

| Tab | Field | Behavior |
|---|---|---|
| `Magnet` | `magnet` | Existing magnet link path. |
| `Torrent File` | `torrent_file` | Existing multipart upload path. |
| `Torrent URL` | `torrent_url` | New HTTP(S) metainfo fetch path. |

When the Torrent adapter is disabled, all Torrent quick-cast tabs remain disabled. When `traffic_acknowledged=false`, all Torrent quick-cast tabs remain disabled with the existing acknowledgement message.

A valid Torrent URL cast returns the same success shape as magnet/upload quick cast: `"torrent started"` plus the new `AdapterRef`. Once metainfo bytes are fetched, playback is indistinguishable from an uploaded `.torrent` session.

## Input Acceptance

`startTorrentURL(ctx, rawURL)` accepts only `http` and `https`.

The adapter should treat a URL as a candidate Torrent URL when at least one of these is true:

- the original URL path ends in `.torrent`, case-insensitive;
- a followed redirect's final URL path ends in `.torrent`, case-insensitive;
- the final response `Content-Type` is one of:
  - `application/x-bittorrent`
  - `application/x-torrent`
  - `application/octet-stream`

`application/octet-stream` is accepted only after the URL path rule matches on either the original or final URL. This avoids routing unrelated binary downloads into the torrent parser.

If none of these rules match, the request fails with a bad-input error such as `URL does not look like a torrent file`.

## Fetch Safety

Remote `.torrent` fetching must not happen until both existing Torrent gates pass:

- `enabled=true`
- `traffic_acknowledged=true`

After those gates pass, the adapter fetches metainfo with a small dedicated HTTP client:

- allowed schemes: `http`, `https`;
- maximum redirects: 3;
- maximum downloaded body: `maxTorrentUploadBytes + 1`;
- request timeout: 15 seconds total;
- user agent may be the Go default unless the codebase already has a bridge user-agent helper;
- no cookies, authorization headers, or operator-provided headers;
- no retry loop;
- no decompression requirement. If Go transparently decodes a compressed response, the decoded body still counts against the byte cap.

Each URL hop is parsed and DNS-resolved before the client follows it. A hop is rejected if any resolved address is loopback, link-local, multicast, unspecified, private RFC1918/ULA, carrier-grade NAT, or another local-only address class. Multiple A/AAAA answers are all-or-nothing: every resolved address must be public. This blocks common SSRF paths and keeps local `.torrent` use on the existing upload path.

The downloader should use a custom `http.Transport.DialContext` or equivalent hook that classifies the actual address being dialed, not only a preflight resolver result. This closes the DNS-rebinding gap where validation resolves a public address but the HTTP transport later dials a private address.

Fetch order:

1. Parse and classify the original URL.
2. Run a redirect-aware `HEAD` request when the original path does not end in `.torrent`.
3. Accept the URL for download if the final path ends in `.torrent` or the final `Content-Type` is BitTorrent-specific.
4. Reject non-`.torrent` paths when `HEAD` fails, omits a usable content type, or returns only `application/octet-stream`.
5. Run the capped `GET` only after the candidate check passes.

For original or final paths ending in `.torrent`, a server that does not support `HEAD` may still be fetched with capped `GET`. For non-`.torrent` paths, `HEAD` is required so the bridge does not download arbitrary bodies just to sniff them.

The final response must be a successful `2xx` status. Non-`2xx` responses fail with a bad-input/fetch error and must not be passed to the metainfo parser.

## Architecture

All new behavior lives in `internal/adapters/torrent`.

Add a narrow fetcher seam:

```go
type torrentURLFetcher interface {
    FetchTorrentURL(ctx context.Context, rawURL string, limit int64) ([]byte, string, error)
}
```

Production code uses a package-local implementation backed by `net/http` and `net.Resolver`. Tests inject a fake fetcher through the adapter or through a package-level test hook, matching the adapter's existing fake-client patterns.

Add adapter methods:

```go
func (a *Adapter) startTorrentURL(ctx context.Context, rawURL string) (*StartedSession, error)
func (a *Adapter) fetchTorrentURLBytes(ctx context.Context, rawURL string, cfg Config) ([]byte, string, error)
```

`startTorrentURL` follows this sequence:

1. Parse and trim the submitted URL.
2. Snapshot config through the existing `snapshotForStart`, so disabled/acknowledgement behavior matches magnet/upload.
3. Fetch and validate remote metainfo bytes with the capped fetcher.
4. Call the existing `startTorrentBytes(ctx, body)` or a shared lower-level helper that avoids taking the snapshot twice.
5. Return the existing `StartedSession` response.

The implementation may extract a helper such as `startTorrentBytesWithConfig(ctx, cfg, body)` to avoid re-checking gates after the remote fetch. The public behavior must still be that gates are checked before any remote HTTP request and before any torrent client creation.

## Quick-Cast Integration

`QuickCastTabs()` adds:

```go
{
    ID:       "torrent-url",
    Label:    "Torrent URL",
    Enabled:  enabled && ack,
    Encoding: adapters.QuickCastEncodingForm,
    Fields: []adapters.QuickCastField{{
        Name:        "torrent_url",
        Label:       "Torrent URL",
        Type:        "url",
        Placeholder: "https://example.com/file.torrent",
        Required:    true,
    }},
}
```

`HandleQuickCast` adds a `torrent-url` branch:

- trim `req.Values["torrent_url"]`;
- return `torrent_url is required` when empty;
- call `startTorrentURL`;
- return `QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}`.

The existing compatibility endpoints under `/ui/adapter/torrent/play` and `/ui/adapter/torrent/upload` do not need a new visible panel form. A direct JSON/form endpoint may be added later, but this design scopes the user-visible launch path to quick-cast and the receiver paste row work.

## Receiver Paste Routing

The current receiver chassis input row has a single text field intended to route by prefix. This feature should make `.torrent` URLs route to the Torrent adapter when the receiver transport work wires the input row to real quick-cast calls.

Detection order for text input should be:

1. `magnet:?` -> Torrent magnet.
2. `http(s)` URL ending in `.torrent` -> Torrent URL.
3. other `http(s)` URL -> URL adapter.
4. everything else -> invalid.

This avoids sending explicit torrent metadata URLs to the URL adapter.

## Error Handling

Add or reuse Torrent errors so route handlers can map them consistently:

| Condition | User message | HTTP status |
|---|---|---|
| disabled | `torrent adapter is disabled` | 409 |
| traffic not acknowledged | `BitTorrent traffic must be acknowledged before starting a torrent` | 403 |
| malformed URL | `invalid torrent URL` | 400 |
| unsupported scheme | `torrent URL must use http or https` | 400 |
| URL does not look like metainfo | `URL does not look like a torrent file` | 400 |
| private/local address | `torrent URL resolves to a disallowed address` | 400 |
| too many redirects | `torrent URL redirect chain is too long` | 400 |
| non-2xx response | `torrent URL fetch failed` | 400 |
| body over 4 MiB | `torrent file exceeds 4 MiB` | 413 |
| metainfo parse failure | existing `torrent file could not be added` | 400 |

Logs and wrapped errors must use a redacted URL form. Credentials are stripped with `url.URL.Redacted()`. Query strings may be omitted entirely for log messages unless needed for a host/path-only diagnostic. The raw URL should not appear in `Error()` output returned to the UI.

## Documentation

Update:

- `README.md`: feature list and adapter table should mention magnet links, uploaded `.torrent` files, and HTTP(S) `.torrent` URLs.
- `docs/torrent.md`: add a "Torrent URLs" section describing accepted URLs, public-only fetch policy, 4 MiB cap, redirect validation, and the traffic acknowledgement gate.
- The old Torrent design remains historical. This spec supersedes only the remote `.torrent` URL non-goal.

## Testing

Use TDD for implementation.

Unit tests in `internal/adapters/torrent`:

- `QuickCastTabs` includes `Torrent URL` with the same disabled reason behavior as the other Torrent tabs.
- `HandleQuickCast` rejects empty `torrent_url`.
- `HandleQuickCast` rejects disabled and unacknowledged adapter before invoking the fetcher.
- `HandleQuickCast` starts a session from fetched bytes and returns the `AdapterRef`.
- `startTorrentURL` rejects non-HTTP(S) schemes.
- fetcher rejects URLs that do not look like `.torrent` files.
- fetcher accepts final `Content-Type: application/x-bittorrent`.
- fetcher accepts `application/octet-stream` only when a candidate URL path ends in `.torrent`.
- fetcher rejects oversized bodies at `4 MiB + 1`.
- fetcher rejects redirect chains over the hop limit.
- fetcher rejects loopback, link-local, private, multicast, unspecified, and mixed public/private DNS answers.
- errors/log-safe strings do not include raw credentials or full query strings.

Integration-level coverage can stay narrow:

- existing upload/magnet tests remain green;
- `internal/ui` quick-cast parsing continues to handle the new form tab without changes to multipart behavior;
- receiver input routing tests should be added with the transport implementation, not in this spec's first implementation pass unless that wiring is touched.

## Open Decisions

No open decisions remain for this implementation pass. The first version accepts public HTTP(S) torrent metadata URLs only and keeps private/local metadata sources on the upload path.
