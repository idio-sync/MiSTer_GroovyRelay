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

`startTorrentURL(ctx, rawURL)` accepts only `http` and `https`. The original URL and every redirect target must not contain userinfo; URLs such as `https://user:pass@example.com/file.torrent` are rejected instead of stripped so credentials are never sent by the HTTP stack.

The acceptance predicate is:

- `pathCandidate`: the original URL path or final URL path ends in `.torrent`, case-insensitive;
- `contentCandidate`: the final response `Content-Type`, parsed with `mime.ParseMediaType` so case and parameters are ignored, is `application/x-bittorrent` or `application/x-torrent`;
- accept when `pathCandidate || contentCandidate`;
- `application/octet-stream` is only tolerated for a `pathCandidate`; it never creates `contentCandidate` by itself.

If neither predicate matches, the request fails with a bad-input error such as `URL does not look like a torrent file`. This keeps arbitrary public binary downloads out of the Torrent adapter.

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
- direct transport only: `http.Transport.Proxy` must be `nil`, so environment proxy settings cannot bypass destination validation;
- no retry loop;
- no decompression requirement. If Go transparently decodes a compressed response, the decoded body still counts against the byte cap.

Each URL hop is parsed and DNS-resolved before the client follows it. A hop is rejected if any resolved address is loopback, link-local, multicast, unspecified, private RFC1918/ULA, carrier-grade NAT, documentation-only, IPv6 unique-local, IPv4-mapped private, or another special-purpose/non-routable address class. Multiple A/AAAA answers are all-or-nothing: every resolved address must be public-routable. Implement this with `net/netip` normalization and explicit deny-prefix checks rather than relying on a single broad predicate. This blocks common SSRF paths and keeps local `.torrent` use on the existing upload path.

The downloader should use a custom `http.Transport.DialContext` or equivalent hook that classifies the actual address being dialed, not only a preflight resolver result. This closes the DNS-rebinding gap where validation resolves a public address but the HTTP transport later dials a private address.

Fetch order:

1. Parse and classify the original URL.
2. If the original path does not end in `.torrent`, run a redirect-aware `HEAD` request through the hardened no-proxy transport.
3. For non-`.torrent` originals, require `HEAD` to succeed with `2xx` and establish `pathCandidate` or `contentCandidate`. If `HEAD` fails, omits a useful content type, or returns only `application/octet-stream` without a final `.torrent` path, reject before `GET`.
4. If the original path ends in `.torrent`, skip preflight `HEAD`; the explicit path is enough to permit a capped `GET`.
5. Run the capped `GET` through the same hardened no-proxy transport. Its redirect chain is validated again, including scheme, userinfo, address class, hop count, and dial-time address.
6. Require the final `GET` response to be `2xx`, then apply the same `pathCandidate || contentCandidate` predicate using the original path, final path, and final `Content-Type`.

For non-`.torrent` originals, `HEAD` is required so the bridge does not download arbitrary bodies just to sniff them. A server that does not support `HEAD` is still usable by naming the file with a `.torrent` URL path.

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
2. Reject unsupported schemes and URL userinfo.
3. Snapshot config through the existing `snapshotForStart`, so disabled/acknowledgement behavior matches magnet/upload before any network fetch.
4. Fetch and validate remote metainfo bytes with the capped fetcher.
5. Run `snapshotForStart` again immediately before creating or touching the torrent client. If `enabled` or `traffic_acknowledged` was revoked during the fetch, abort and discard the fetched bytes.
6. Call a shared lower-level helper such as `startTorrentBytesWithConfig(ctx, cfg, body)`.
7. Return the existing `StartedSession` response.

The second gate check is intentional. It prevents a long fetch from creating the torrent client after the operator disables the adapter or revokes traffic acknowledgement.

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
| URL contains userinfo | `torrent URL must not include credentials` | 400 |
| URL does not look like metainfo | `URL does not look like a torrent file` | 400 |
| private/local address | `torrent URL resolves to a disallowed address` | 400 |
| too many redirects | `torrent URL redirect chain is too long` | 400 |
| non-2xx response | `torrent URL fetch failed` | 400 |
| body over 4 MiB | `torrent file exceeds 4 MiB` | 413 |
| metainfo parse failure | existing `torrent file could not be added` | 400 |

Remote URL validation errors use `ErrBadInput` and map to 400. Do not reuse `ErrNonLoopback`; that existing 403 error is for non-loopback clients trying to read the adapter's local media route.

Logs and wrapped errors must use a redacted URL form. Credentials are rejected before request construction and stripped with `url.URL.Redacted()` in any defensive redaction helper. Query strings may be omitted entirely for log messages unless needed for a host/path-only diagnostic. The raw URL should not appear in `Error()` output returned to the UI.

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
- `startTorrentURL` rechecks gates after a successful fetch; if `enabled` or `traffic_acknowledged` is revoked before torrent client creation, no client or `AddMetaInfo` path is touched.
- fetcher rejects URLs that do not look like `.torrent` files.
- fetcher accepts final `Content-Type: application/x-bittorrent`, including case differences and parameters such as `; charset=binary`.
- fetcher accepts `application/octet-stream` only when a candidate URL path ends in `.torrent`.
- fetcher rejects oversized bodies at `4 MiB + 1`.
- fetcher maps oversized remote bodies to the 413 route/status behavior.
- fetcher rejects redirect chains over the hop limit.
- fetcher rejects loopback, link-local, private, multicast, unspecified, and mixed public/private DNS answers.
- fetcher rejects URL userinfo on the original URL and redirect targets.
- fetcher disables proxies and does not honor environment proxy settings.
- fetcher blocks redirect-to-private and DNS-rebinding-to-private at actual dial time.
- fetcher validates both `HEAD` and `GET` redirect chains.
- fetcher rejects non-`2xx` `HEAD` and `GET` responses where required.
- fetcher rejects non-`.torrent` originals when `HEAD` is unsupported or only reports `application/octet-stream`.
- errors/log-safe strings do not include raw credentials or full query strings.

Integration-level coverage can stay narrow:

- existing upload/magnet tests remain green;
- `internal/ui` quick-cast parsing continues to handle the new form tab without changes to multipart behavior;
- receiver input routing tests should be added with the transport implementation, not in this spec's first implementation pass unless that wiring is touched.

## Open Decisions

No open decisions remain for this implementation pass. The first version accepts public HTTP(S) torrent metadata URLs only and keeps private/local metadata sources on the upload path.
