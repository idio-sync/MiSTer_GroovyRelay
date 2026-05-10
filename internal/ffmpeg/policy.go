package ffmpeg

import (
	"fmt"
	"strings"
	"time"
)

// MediaInputPolicy constrains how ffprobe / ffmpeg dereference a media input
// URL. Adapters that hand untrusted URLs to core.Manager (DLNA today; any
// future adapter that surfaces operator-supplied or LAN-controlled URLs)
// populate this on core.SessionRequest; adapters with curated/server-provided
// URLs (Plex, Jellyfin, URL-input) leave it zero-valued and preserve current
// behavior end-to-end.
//
// The zero value is the "no policy applied" sentinel: an empty
// ProtocolWhitelist suppresses the -protocol_whitelist flag entirely, false
// booleans leave FFmpeg defaults alone, a zero RWTimeout omits -rw_timeout,
// and an empty BlockedHeaders list does not filter any headers. Existing
// adapters therefore require no behavior migration.
//
// The policy lives in package ffmpeg so the data-plane / probe call sites
// can consume it directly without an import cycle. core.MediaInputPolicy is
// a type alias to this so adapters can stay inside the core import surface
// (see internal/core/policy.go). Spec source: §Architecture / Core Media
// Input Policy in docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md.
type MediaInputPolicy struct {
	// ProtocolWhitelist is the value passed to ffmpeg/ffprobe's
	// -protocol_whitelist flag (e.g. "file,http,https,tcp,tls,crypto"). An
	// empty / nil slice suppresses the flag and keeps the FFmpeg-default
	// allow-everything behavior. Spec line 343.
	ProtocolWhitelist []string

	// DisableRedirects exists as a contract marker but emits NO FFmpeg
	// flag. Path-A safety against redirect-based SSRF requires TWO things,
	// and the policy alone is NOT sufficient:
	//
	//   1. The adapter resolves and revalidates EVERY HTTP Location header
	//      server-side BEFORE handing the final URL to core (spec line 340:
	//      max 3 redirects, re-validate each target, reject disallowed
	//      schemes/addresses). This is load-bearing for IP-address safety.
	//   2. ProtocolWhitelist is set to constrain which schemes ffmpeg's
	//      HTTP demuxer will follow on any redirect it does observe.
	//
	// Item 1 is the part that closes the SSRF gap. ProtocolWhitelist alone
	// DOES NOT re-run the adapter's RFC1918 / loopback / link-local /
	// metadata-endpoint rejection on a redirect target — a 302 from a
	// public hostname to 169.254.169.254 (cloud metadata) is allowed by
	// `-protocol_whitelist=https` and would bypass the original URL's
	// IP-address check. The adapter MUST do the prevalidation pass; the
	// policy cannot do it for the adapter.
	//
	// There is no single FFmpeg flag that disables redirect-following on
	// the HTTP demuxer in a way the spec considers safe to rely on, so
	// this field deliberately produces no argv. It is kept in the policy
	// struct so future per-source policy can express "redirects allowed"
	// vs "redirects forbidden" without requiring code changes elsewhere
	// (e.g. if v2 introduces a validating proxy, this flag becomes its
	// configuration anchor). Setting this true today does not change argv.
	DisableRedirects bool

	// DisableReconnect emits FOUR flags that together cover every
	// reconnect path the FFmpeg HTTP demuxer exposes:
	//
	//   -reconnect 0
	//   -reconnect_at_eof 0
	//   -reconnect_streamed 0
	//   -reconnect_on_network_error 0
	//
	// All four default to off in current FFmpeg builds, so the explicit
	// zeros are defense-in-depth: a future FFmpeg release could flip any
	// of them on by default and silently regress the policy if we relied
	// on defaults. The DLNA spec requires this because reconnects can
	// race a server-side rebind after the validator accepted the URL
	// (lines 345-346) — a different IP could be served on the retry.
	//
	// All four option names are present in FFmpeg 4.4+ (the project's
	// bundled FFmpeg via Alpine 3.20 ships 6.1.1, well above that floor).
	DisableReconnect bool

	// RWTimeout > 0 emits "-rw_timeout <microseconds>" so a stalled remote
	// cannot pin the data plane indefinitely. FFmpeg expects this in
	// microseconds, not milliseconds (spec line 346 bounds it via an
	// adapter constant).
	RWTimeout time.Duration

	// BlockedHeaders is a case-insensitive deny-list for input header
	// field names. Adapters keep building InputHeaders / AudioInputHeaders
	// naively; core.Manager filters them through this list before any
	// FFmpeg invocation so a "Referer" or "Cookie" that an adapter forgot
	// about cannot leak the bridge's internal hostnames to a redirect
	// server (spec line 347, line 115).
	BlockedHeaders []string

	// DisablePlaylists is reserved for future demuxer constraints (e.g.
	// rejecting HLS / DASH child-resource fetches at the input level).
	// Phase 2 DLNA defers HLS/DASH protocolInfo to Phase 5 (spec line 348)
	// and meanwhile relies on adapter-side metadata validation, so this
	// field has no behavioral effect today. It is documented here so the
	// shape of the policy surface stays stable when Phase 5 lands.
	DisablePlaylists bool
}

// IsZero reports whether p is the zero value. Used by the tests and by
// the FFmpeg argv builders to short-circuit when no policy was set.
func (p MediaInputPolicy) IsZero() bool {
	return len(p.ProtocolWhitelist) == 0 &&
		!p.DisableRedirects &&
		!p.DisableReconnect &&
		p.RWTimeout == 0 &&
		len(p.BlockedHeaders) == 0 &&
		!p.DisablePlaylists
}

// Apply returns args with the policy's input-side flags appended. Caller
// must place the result before "-i <url>" so the flags take effect on the
// next input. When p is the zero value, args is returned unchanged so
// existing FFmpeg invocations retain identical argv.
//
// DisableRedirects deliberately emits no flag — see the field's doc
// comment. The flags emitted are:
//
//   - "-protocol_whitelist <list>" when ProtocolWhitelist is non-empty.
//   - "-reconnect 0 -reconnect_at_eof 0 -reconnect_streamed 0
//     -reconnect_on_network_error 0" when DisableReconnect is true. All
//     four are emitted as defense-in-depth against a future FFmpeg
//     changing reconnect defaults; see the field doc.
//   - "-rw_timeout <usec>" when RWTimeout > 0 (FFmpeg expects µs).
//
// Sorted/deterministic for testability: protocol_whitelist first, then
// reconnect block, then timeout.
func (p MediaInputPolicy) Apply(args []string) []string {
	if len(p.ProtocolWhitelist) > 0 {
		args = append(args, "-protocol_whitelist", strings.Join(p.ProtocolWhitelist, ","))
	}
	if p.DisableReconnect {
		args = append(args,
			"-reconnect", "0",
			"-reconnect_at_eof", "0",
			"-reconnect_streamed", "0",
			"-reconnect_on_network_error", "0",
		)
	}
	if p.RWTimeout > 0 {
		usec := p.RWTimeout.Microseconds()
		args = append(args, "-rw_timeout", fmt.Sprintf("%d", usec))
	}
	return args
}

// FilterHeaders returns a copy of headers with any keys whose name matches
// (case-insensitively) an entry in p.BlockedHeaders removed. Returns
// headers unchanged when p.BlockedHeaders is empty. Returns nil when
// headers is nil and there is nothing to filter, preserving the
// "no -headers arg emitted" path through appendHeadersArg.
//
// Matching is on the header field NAME only (not value), so a deny-list
// entry of "Referer" drops "referer", "Referer", and "REFERER" alike.
func (p MediaInputPolicy) FilterHeaders(headers map[string]string) map[string]string {
	if len(p.BlockedHeaders) == 0 || len(headers) == 0 {
		return headers
	}
	denySet := make(map[string]struct{}, len(p.BlockedHeaders))
	for _, name := range p.BlockedHeaders {
		denySet[strings.ToLower(name)] = struct{}{}
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if _, blocked := denySet[strings.ToLower(k)]; blocked {
			continue
		}
		out[k] = v
	}
	return out
}
