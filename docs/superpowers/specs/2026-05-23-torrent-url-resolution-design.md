# Torrent URL Resolution Design

**Date:** 2026-05-23  
**Status:** Draft for review  
**Scope:** Let HTTP(S) URLs that point to `.torrent` metainfo resolve through the Torrent adapter and stream through the existing torrent playback path.

## Problem

The Torrent adapter can currently play magnet links and uploaded `.torrent` files. If an operator pastes an HTTP(S) URL to a `.torrent` file, the bridge treats it like an ordinary URL source. That does not fetch metainfo, does not join the torrent swarm, and does not stream the selected file.

The previous torrent design intentionally made remote `.torrent` fetching a v1 non-goal because it creates a new server-side fetch surface. This feature adds that fetch surface with strict bounds: only explicit torrent metadata URLs are accepted, fetching is capped, redirects are validated, and the downloaded bytes feed the same metainfo parser used by uploads.

## Goals

- Accept public `http://` and `https://` URLs that resolve to BitTorrent metainfo.
- Reuse a shared guarded URL fetch/address-classification implementation instead of adding a third SSRF classifier beside Streams and DLNA/HLS.
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
- Wiring the `/receiver` paste row in this implementation pass. That belongs to the future receiver quick-cast/cast-drawer phase.
- Changing magnet or uploaded `.torrent` behavior.
- Adding new torrent legality or copyright enforcement. The operator remains responsible for the content they provide.

## Shared Fetch Guard Prerequisite

Before adding Torrent URL fetching, extract the existing hardened source-fetch pieces into a shared internal package such as `internal/sourcefetch`.

The package should be based on `internal/adapters/streams/fetch.go` plus the DLNA/HLS dial-time validation lessons:

- one public-routable address classifier using `net/netip`;
- one capped body reader using `io.LimitReader(body, maxBytes+1)` before `io.ReadAll`;
- one no-proxy transport builder;
- one redirect walker that validates each hop;
- one dial-time pinning hook that rechecks the actual address before dialing;
- one TLS policy that pins `TLSClientConfig.ServerName` to the original hostname and never sets `InsecureSkipVerify`;
- typed/sanitized errors that do not expose raw URLs.

Implementation order:

1. Extract the shared guarded-fetch package.
2. Migrate Streams to the shared fetcher/classifier without behavior changes.
3. Make DLNA's generic validator and HLS fetcher call the shared address classifier, even if they keep their adapter-specific SOAP/media semantics.
4. Add Torrent URL fetching on top of the shared package.

After this feature lands, Torrent, Streams, and DLNA/HLS must not carry independent address-prefix lists.

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

IP-literal hosts are rejected in v1, including bracketed IPv6 literals such as `http://[::1]/file.torrent`. Host parsing must use `url.URL.Hostname()` and `url.URL.Port()`, not manual string splitting. Dial targets are assembled with `net.JoinHostPort` after DNS resolution so IPv6 addresses are bracketed correctly.

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
- maximum redirects: 3, fixed and non-configurable in v1;
- maximum downloaded body: `maxTorrentUploadBytes + 1`, where `maxTorrentUploadBytes` is the existing 4 MiB upload cap (`4194304` bytes);
- request timeout: 15 seconds total, fixed and non-configurable in v1;
- user agent: `MiSTer_GroovyRelay-torrent-url-fetcher/1`;
- no cookies, authorization headers, or operator-provided headers;
- direct transport only: `http.Transport.Proxy` must be `nil`, so environment proxy settings cannot bypass destination validation;
- no retry loop;
- compression disabled: set `http.Transport.DisableCompression = true` and do not send `Accept-Encoding`, so the byte cap applies to bytes read from the response body.

Each URL hop is parsed and DNS-resolved before the client follows it. Multiple A/AAAA answers are all-or-nothing: every resolved address must be public-routable. Implement this with `net/netip` normalization, unmapping IPv4-mapped IPv6 addresses before classification.

The normative deny prefixes are:

```text
IPv4:
0.0.0.0/8
10.0.0.0/8
100.64.0.0/10
127.0.0.0/8
169.254.0.0/16
172.16.0.0/12
192.0.0.0/24
192.0.2.0/24
192.88.99.0/24
192.168.0.0/16
198.18.0.0/15
198.51.100.0/24
203.0.113.0/24
224.0.0.0/4
240.0.0.0/4

IPv6:
::/128
::1/128
64:ff9b::/96
64:ff9b:1::/48
100::/64
2001::/23
2001:db8::/32
2002::/16
3fff::/20
fc00::/7
fe80::/10
ff00::/8
::ffff:0:0/96
```

This list intentionally includes unspecified addresses (`0.0.0.0/8`, `::/128`), documentation ranges, benchmarking ranges, old 6to4/6bone/Teredo-related ranges, NAT64 well-known prefixes, multicast, future-use space, and IPv4-mapped IPv6 forms. `0.0.0.0` is explicitly denied because bridge deployments may run with host networking.

The downloader should use a custom `http.Transport.DialContext` or equivalent hook that classifies the actual address being dialed, not only a preflight resolver result. This closes the DNS-rebinding gap where validation resolves a public address but the HTTP transport later dials a private address.

For HTTPS, the pinned dial address must not weaken certificate verification. The transport must set `TLSClientConfig.ServerName` to the validated hostname and must never set `InsecureSkipVerify: true`.

Fetch order:

1. Parse and classify the original URL.
2. If the original path does not end in `.torrent`, run a redirect-aware `HEAD` request through the hardened no-proxy transport.
3. For non-`.torrent` originals, require `HEAD` to succeed with `2xx` and establish `pathCandidate` or `contentCandidate`. If `HEAD` fails, omits a useful content type, or returns only `application/octet-stream` without a final `.torrent` path, reject before `GET`.
4. If the original path ends in `.torrent`, skip preflight `HEAD`; the explicit path is enough to permit a capped `GET`.
5. Run the capped `GET` through the same hardened no-proxy transport. Its redirect chain is validated again, including scheme, userinfo, address class, hop count, and dial-time address.
6. Require the final `GET` response to be `2xx`, then apply the same `pathCandidate || contentCandidate` predicate using the original path, final path, and final `Content-Type`.

For non-`.torrent` originals, `HEAD` is required so the bridge does not download arbitrary bodies just to sniff them. A server that does not support `HEAD` is still usable by naming the file with a `.torrent` URL path. This means some presigned GET-only URLs whose path does not end in `.torrent` are rejected in v1 with an actionable error: `torrent URL must end in .torrent or support HEAD with a BitTorrent content type`.

The final response must be a successful `2xx` status. Non-`2xx` responses fail with a bad-input/fetch error and must not be passed to the metainfo parser.

## Architecture

New launch behavior lives in `internal/adapters/torrent`; network safety primitives live in the shared guarded-fetch package described above.

Add a narrow fetcher seam:

```go
type TorrentURLFetchResult struct {
    Body        []byte
    FinalURL    string
    ContentType string
}

type torrentURLFetcher interface {
    FetchTorrentURL(ctx context.Context, rawURL string, limit int64) (TorrentURLFetchResult, error)
}
```

`FinalURL` and `ContentType` are required so the adapter can evaluate `pathCandidate || contentCandidate` without re-parsing ambiguous out-of-band strings. Production code uses the shared guarded-fetch package backed by `net/http` and `net.Resolver`. Tests inject a fake fetcher through the adapter or through a package-level test hook, matching the adapter's existing fake-client patterns.

Add adapter methods:

```go
func (a *Adapter) startTorrentURL(ctx context.Context, rawURL string) (*StartedSession, error)
func (a *Adapter) fetchTorrentURL(ctx context.Context, rawURL string, cfg Config) (TorrentURLFetchResult, error)
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

No new Torrent TOML fields are added in v1, so `Config`, `Validate`, `DecodeConfig`, `ApplyConfig`, and `torrentConfigFieldCount` do not change. The timeout and redirect constants are hardcoded implementation constants, not operator settings, and therefore have no `ApplyScope`.

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

The existing compatibility endpoints under `/ui/adapter/torrent/play` and `/ui/adapter/torrent/upload` do not need a new visible panel form. A direct JSON/form endpoint may be added later, but this design scopes the user-visible launch path to quick-cast.

## Receiver Paste Routing

The current receiver chassis input row has a single text field intended to route by prefix, but [the receiver chassis transport spec](2026-05-22-receiver-chassis-transport-design.md) explicitly defers quick-cast / cast drawer integration to Phase 3. This section records the future routing order only; it is not part of this implementation pass.

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
| non-`.torrent` GET-only URL | `torrent URL must end in .torrent or support HEAD with a BitTorrent content type` | 400 |
| private/local address | `torrent URL resolves to a disallowed address` | 400 |
| too many redirects | `torrent URL redirect chain is too long` | 400 |
| non-2xx response | `torrent URL fetch failed` | 400 |
| body over 4 MiB | `torrent file exceeds 4 MiB` | 413 |
| metainfo parse failure | existing `torrent file could not be added` | 400 |

Remote URL validation errors use `ErrBadInput` and map to 400. Do not reuse `ErrNonLoopback`; that existing 403 error is for non-loopback clients trying to read the adapter's local media route.

Logs and wrapped errors must use a redacted URL form. Credentials are rejected before request construction and stripped with `url.URL.Redacted()` in any defensive redaction helper. Query strings may be omitted entirely for log messages unless needed for a host/path-only diagnostic. The raw URL should not appear in `Error()` output returned to the UI.

Do not return or wrap raw `*url.Error` / `net/http` errors directly with `%w` if their `Error()` string may include the submitted URL. Convert network failures to typed Torrent errors with sanitized messages, and keep raw lower-level errors out of UI-visible and log-visible error chains.

## Documentation

Update:

- `README.md`: feature list and adapter table should mention magnet links, uploaded `.torrent` files, and HTTP(S) `.torrent` URLs.
- `docs/torrent.md`: add a "Torrent URLs" section describing accepted URLs, public-only fetch policy, 4 MiB cap, redirect validation, and the traffic acknowledgement gate.
- [2026-05-10-torrent-adapter-design.md](2026-05-10-torrent-adapter-design.md) remains historical. This spec supersedes only its remote `.torrent` URL non-goal.

## Testing

Use TDD for implementation.

Unit tests in `internal/adapters/torrent`:

- `QuickCastTabs` includes `Torrent URL` with the same disabled reason behavior as the other Torrent tabs.
- `HandleQuickCast` rejects empty `torrent_url`.
- `HandleQuickCast` rejects disabled and unacknowledged adapter before invoking the fetcher.
- `HandleQuickCast` starts a session from fetched bytes and returns the `AdapterRef`.
- `startTorrentURL` rejects non-HTTP(S) schemes.
- `startTorrentURL` rechecks gates after a successful fetch; if `enabled` or `traffic_acknowledged` is revoked before torrent client creation, the test observes `ClientFactory` call count remains zero and `AddMetaInfo` is not touched.
- fetcher rejects URLs that do not look like `.torrent` files.
- fetcher returns body, final URL, and content type in one result struct.
- fetcher accepts final `Content-Type: application/x-bittorrent`, including case differences and parameters such as `; charset=binary`.
- fetcher accepts `application/octet-stream` only when a candidate URL path ends in `.torrent`.
- fetcher reads remote bodies through `io.LimitReader(body, maxBytes+1)` before `io.ReadAll`.
- fetcher rejects oversized bodies at `4 MiB + 1`.
- fetcher maps oversized remote bodies to the 413 route/status behavior.
- fetcher rejects redirect chains over the hop limit.
- shared classifier rejects every normative deny prefix listed in this spec, including `0.0.0.0`, NAT64, benchmarking, documentation, and future-use ranges.
- fetcher rejects loopback, link-local, private, multicast, unspecified, IP-literal, and mixed public/private DNS answers.
- fetcher rejects URL userinfo on the original URL and redirect targets.
- fetcher disables proxies and does not honor environment proxy settings.
- fetcher blocks redirect-to-private and DNS-rebinding-to-private at actual dial time.
- HTTPS tests verify pinned dialing preserves `TLSClientConfig.ServerName` and never uses `InsecureSkipVerify`.
- fetcher validates both `HEAD` and `GET` redirect chains.
- fetcher rejects non-`2xx` `HEAD` and `GET` responses where required.
- fetcher rejects non-`.torrent` originals when `HEAD` is unsupported or only reports `application/octet-stream`.
- errors/log-safe strings do not include raw credentials or full query strings.

Integration-level coverage can stay narrow:

- existing upload/magnet tests remain green;
- `internal/ui` quick-cast parsing continues to handle the new form tab without changes to multipart behavior;
- receiver input routing tests should be added with the transport implementation, not in this spec's first implementation pass unless that wiring is touched.

## Resolved Decisions

The first version accepts public HTTP(S) torrent metadata URLs only and keeps private/local metadata sources on the upload path.

- No new TOML: timeout, redirect count, user agent, and fetch size cap are hardcoded v1 constants.
- GET-only presigned URLs are supported only when the URL path itself ends in `.torrent`; otherwise the server must support `HEAD` with a BitTorrent content type.
- The fetcher returns a structured result with body, final URL, and content type.
- IP-literal hosts, including bracketed IPv6 literals, are rejected in v1.
- Pinned HTTPS dialing preserves certificate verification through `TLSClientConfig.ServerName`; `InsecureSkipVerify` is forbidden.
