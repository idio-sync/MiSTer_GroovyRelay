# Owncast URL Resolution Design

## Goal

Make Owncast homepage URLs usable in the URL adapter. Operators should be able to paste an Owncast site URL such as `https://retroblast.tv/` or `https://live.retrostrange.com/` and have the bridge play the site's HLS stream without manually changing the URL to `/hls/stream.m3u8`.

## Scope

The resolver applies to any HTTP or HTTPS Owncast host. It is not limited to a curated hostname allowlist. Direct media URLs, stream catalog URLs, and existing yt-dlp page handling keep their current behavior.

## Detection

Before the URL adapter chooses direct playback or yt-dlp, it attempts a conservative Owncast probe for page-like URLs. The probe builds a same-origin status URL:

```text
<submitted scheme>://<submitted host>/api/status
```

If that endpoint returns JSON shaped like an Owncast status response, the submitted URL resolves to:

```text
<submitted scheme>://<submitted host>/hls/stream.m3u8
```

The probe should require a successful HTTP response and recognizable fields such as `serverTime`, `versionNumber`, and `online`. The `online` value does not have to be true; offline streams can still be handed to FFmpeg so the normal playback/probe path reports the real media error.

## Data Flow

1. `castURLWithStarter` validates that the submitted URL has an `http` or `https` scheme.
2. The streams handoff resolver gets first chance, preserving existing catalog links.
3. The Owncast homepage resolver checks page-like URLs against same-origin `/api/status`.
4. On a positive Owncast match, the URL adapter updates `streamURL` to same-origin `/hls/stream.m3u8` and continues through the existing direct FFmpeg path.
5. If detection fails or times out, the adapter falls through to the existing direct/yt-dlp route decision unchanged.

## Error Handling

Owncast detection is best-effort. Network errors, timeouts, non-JSON responses, malformed JSON, and non-Owncast JSON all mean "no match" rather than a user-facing failure. The only user-visible errors remain the existing URL validation, yt-dlp resolution, and FFmpeg/core playback errors.

The detection timeout should be short enough that a dead homepage does not make the URL form feel stuck.

## Security

The resolver only constructs same-origin URLs from the submitted scheme and host. It does not parse arbitrary HTML, follow cross-origin hints, or use redirects to discover stream URLs.

This does not add a bundled stream source. It only improves operator-submitted URL convenience, so existing URL adapter trust boundaries remain unchanged.

## Testing

Add URL adapter unit tests for:

- Owncast homepage resolves to same-origin `/hls/stream.m3u8`.
- Existing direct Owncast HLS URLs are left untouched.
- Non-Owncast page URLs fall through unchanged.
- Owncast probe failures fall through unchanged.
- Forced `direct` mode still receives the Owncast convenience rewrite.
