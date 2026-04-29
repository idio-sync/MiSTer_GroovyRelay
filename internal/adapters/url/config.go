// Package url is the URL-input cast adapter. It accepts an http(s) media
// URL via POST or via the settings UI's "Play URL" form, builds a
// core.SessionRequest, and delegates to core.Manager.StartSession.
//
// Spec: docs/specs/2026-04-25-url-adapter-design.md
//
// The package is intentionally minimal: one Config field (enabled), no
// goroutines, no upstream protocol — its primary purpose is to validate
// the core.Manager / adapters.Adapter abstraction boundary by being
// structurally different from the Plex adapter (cast target). See the
// spec's "Cross-adapter preemption" section for the contract this
// adapter enforces against the rest of the bridge.
package url

import (
	"fmt"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
)

// Config is the [adapters.url] TOML section.
//
// Defaults are produced by DefaultConfig(); operators override per-field
// in config.toml. Validate() enforces the documented constraints.
//
// New fields in v1.1 (yt-dlp resolver):
//   - YtdlpEnabled, YtdlpHosts, YtdlpFormat, YtdlpResolveTimeoutSeconds
type Config struct {
	Enabled                    bool     `toml:"enabled"`
	YtdlpEnabled               bool     `toml:"ytdlp_enabled"`
	YtdlpHosts                 []string `toml:"ytdlp_hosts"`
	YtdlpFormat                string   `toml:"ytdlp_format"`
	YtdlpResolveTimeoutSeconds int      `toml:"ytdlp_resolve_timeout_seconds"`
}

// DefaultConfig returns the zero-config baseline. The URL adapter is
// disabled by default; if enabled, yt-dlp resolution is on by default
// against the curated allowlist.
//
// Format selector: caps at 720p (CRT can't show more), avoids AV1 (slow
// software decode). Prefers `bv*+ba` (best video + best audio merged) so
// the bridge handles modern YouTube DASH-only sources, where progressive
// muxed formats have been retired. yt-dlp signals the merge by returning
// a `requested_formats` array of two entries; the resolver classifies
// them and the bridge runs ffmpeg with two -i inputs (video + audio)
// mapped via `-map 0:v -map 1:a`.
//
// Fallback chain:
//  1. bv*[height<=720][vcodec!*=av01]+ba — primary, DASH or progressive.
//  2. b[height<=720][vcodec!*=av01]      — pre-merged (rare on YT in 2026).
//  3. b                                  — last resort, any best.
func DefaultConfig() Config {
	return Config{
		Enabled:                    false,
		YtdlpEnabled:               true,
		YtdlpHosts:                 ytdlp.DefaultHosts(),
		YtdlpFormat:                "bv*[height<=720][vcodec!*=av01]+ba/b[height<=720][vcodec!*=av01]/b",
		YtdlpResolveTimeoutSeconds: 30,
	}
}

// Validate type-checks and range-checks the new fields. Lowercases
// hostnames in place. The Validate contract is pure-with-respect-to
// the rest of the system (no I/O), but it is allowed to normalize
// the receiver — same convention as Plex.
func (c *Config) Validate() error {
	var errs adapters.FieldErrors

	if c.YtdlpResolveTimeoutSeconds < 5 || c.YtdlpResolveTimeoutSeconds > 120 {
		errs = append(errs, adapters.FieldError{
			Key: "ytdlp_resolve_timeout_seconds",
			Msg: fmt.Sprintf("must be in [5, 120], got %d", c.YtdlpResolveTimeoutSeconds),
		})
	}

	// Hostname check: lowercase, no scheme/port/path/whitespace/URL syntax.
	// Rejected character class catches whitespace incl. CR/LF, slash, colon
	// (port + scheme separator), question mark, hash, and at-sign — covers
	// the URL-paste-into-hostname-field operator typo categories.
	cleaned := make([]string, 0, len(c.YtdlpHosts))
	hostsHadErrors := false
	for _, h := range c.YtdlpHosts {
		trimmed := strings.TrimSpace(h)
		if trimmed == "" {
			errs = append(errs, adapters.FieldError{
				Key: "ytdlp_hosts",
				Msg: "entries must not be empty",
			})
			hostsHadErrors = true
			continue
		}
		if strings.ContainsAny(trimmed, " \t\r\n/:?#@") {
			errs = append(errs, adapters.FieldError{
				Key: "ytdlp_hosts",
				Msg: fmt.Sprintf("entry %q contains URL syntax characters (whitespace, /, :, ?, #, @)", h),
			})
			hostsHadErrors = true
			continue
		}
		cleaned = append(cleaned, strings.ToLower(trimmed))
	}
	// Only commit the normalized list when validation succeeded for all
	// entries. Otherwise a caller that ignores the error would observe
	// silent dropouts of the bad entries — preserving the original list
	// keeps Validate's "all-or-nothing" mental model.
	if !hostsHadErrors {
		c.YtdlpHosts = cleaned
	}

	return errs.Err()
}
