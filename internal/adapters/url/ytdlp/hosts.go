// Package ytdlp implements the yt-dlp resolver for the URL adapter.
// hosts.go: hostname allowlist matching with suffix-at-boundary semantics.
package ytdlp

import "strings"

// Match reports whether host is covered by allow.
//
// Matching rules:
//   - Suffix-based at "." boundaries. "m.youtube.com" matches "youtube.com".
//   - Exact host equality also matches.
//   - "fakeyoutube.com" does NOT match "youtube.com" (substring rejected).
//   - "foo.com" does NOT match "com" (no naked TLD matching).
//   - Case-insensitive on both sides.
//   - Empty host or empty allowlist → false.
//
// Spec: docs/specs/2026-04-25-url-ytdlp-design.md §"Hostname allowlist".
func Match(host string, allow []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(allow) == 0 {
		return false
	}
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		// Reject naked TLDs to avoid "com" matching "foo.com".
		if !strings.Contains(a, ".") {
			continue
		}
		if host == a {
			return true
		}
		// Suffix at "." boundary: host ends with "." + a.
		if strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

// DefaultHosts is the curated allowlist baked into the adapter's
// default config. Operators extend or replace via
// [adapters.url].ytdlp_hosts in config.toml. The list is intentionally
// conservative — only sites with reputations for yt-dlp working out
// of the box, no auth required, stable extractors. Spec §"Hostname
// allowlist" lists the explicitly-excluded hostnames (tiktok,
// instagram, x.com, twitter, reddit) and the rationale for each.
func DefaultHosts() []string {
	return []string{
		"youtube.com",
		"youtu.be",
		"m.youtube.com",
		"twitch.tv",
		"vimeo.com",
		"archive.org",
		"dailymotion.com",
		"soundcloud.com",
		"bandcamp.com",
	}
}
