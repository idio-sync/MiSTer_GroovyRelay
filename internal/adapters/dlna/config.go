// Package dlna is the DLNA / UPnP MediaRenderer adapter. It advertises
// the bridge as a MediaRenderer:1 device on the LAN, accepts SOAP
// AVTransport / RenderingControl / ConnectionManager actions from
// DLNA control points (VLC, BubbleUPnP, Kodi, Windows "Cast to
// device"), and translates SetAVTransportURI + Play into a
// core.SessionRequest.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
//
// Phase 1 scaffolds: this package currently only owns the [adapters.dlna]
// TOML section, the UI fields, the lifecycle stubs, and stub HTTP
// handlers for the 13 protocol routes. SSDP discovery (T3),
// SCPD/device descriptors, and SOAP handlers (T4) arrive in later
// tasks. See docs/superpowers/plans/ for the phased plan.
package dlna

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// deviceNameMaxLen caps the friendly name at 64 runes. SSDP / UPnP
// don't impose a hard limit, but real DLNA controllers truncate or
// misrender longer values, and the friendlyName is baked into device
// descriptor XML at startup. 64 is generous for a human-readable label.
const deviceNameMaxLen = 64

// Config is the [adapters.dlna] TOML section.
//
// Defaults are produced by DefaultConfig(); operators override
// per-field in config.toml. Validate() enforces the documented
// constraints. Per-field ApplyScope is in the spec's §Config table
// and reflected in the FieldDef table returned from Fields().
type Config struct {
	Enabled               bool   `toml:"enabled"`
	DeviceName            string `toml:"device_name"`
	AutoplayOnSetURI      bool   `toml:"autoplay_on_set_uri"`
	AllowPublicSourceURLs bool   `toml:"allow_public_source_urls"`
}

// DefaultConfig returns the disabled-by-default baseline. DLNA exposes
// unauthenticated LAN control, so the operator must opt in.
//
// DeviceName defaults to "MiSTer" — short, friendly, matches the Plex
// adapter's convention and the spec §Config example.
func DefaultConfig() Config {
	return Config{
		Enabled:               false,
		DeviceName:            "MiSTer",
		AutoplayOnSetURI:      false,
		AllowPublicSourceURLs: false,
	}
}

// Validate checks the config for SSDP/descriptor compatibility.
// Operator's casing is preserved (DLNA controllers display the name
// verbatim — lower-casing it would surprise users).
//
// DeviceName must be non-empty, ≤ deviceNameMaxLen runes, and contain
// only printable characters. Control characters in the friendlyName
// would break XML escaping in the device descriptor and SSDP
// USN/ST headers; guard at the validation boundary so the disk write
// path can stay simple.
func (c *Config) Validate() error {
	var errs adapters.FieldErrors

	trimmedName := strings.TrimSpace(c.DeviceName)
	switch {
	case trimmedName == "":
		errs = append(errs, adapters.FieldError{
			Key: "device_name",
			Msg: "must not be empty",
		})
	default:
		// Length cap measured in runes, not bytes — spec example uses
		// "MiSTer" but operators are free to use non-ASCII labels.
		if runeLen := len([]rune(trimmedName)); runeLen > deviceNameMaxLen {
			errs = append(errs, adapters.FieldError{
				Key: "device_name",
				Msg: fmt.Sprintf("must be at most %d characters, got %d", deviceNameMaxLen, runeLen),
			})
		}
		// Reject non-printable characters (control chars, etc.).
		// strconv.IsPrint matches Unicode letter/number/punctuation/
		// symbol/space — same definition Go's fmt verb %q uses.
		for _, r := range trimmedName {
			if !strconv.IsPrint(r) {
				errs = append(errs, adapters.FieldError{
					Key: "device_name",
					Msg: "must contain only printable characters",
				})
				break
			}
		}
	}

	return errs.Err()
}
