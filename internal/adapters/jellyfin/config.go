// Package jellyfin is the Jellyfin cast-target adapter. See
// docs/specs/2026-04-25-jellyfin-adapter-design.md for the design.
package jellyfin

import (
	"fmt"
	"net/url"
	"unicode"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// Config is the [adapters.jellyfin] TOML section. Secrets (access
// token, user id, server id) are persisted separately under
// <data_dir>/jellyfin/token.json — see tokenstore.go.
type Config struct {
	Enabled             bool   `toml:"enabled"`
	ServerURL           string `toml:"server_url"`
	DeviceName          string `toml:"device_name"`
	MaxVideoBitrateKbps int    `toml:"max_video_bitrate_kbps"`
	AutoAdvance         bool   `toml:"auto_advance"`
}

// DefaultConfig returns the zero-value config the adapter starts with
// when [adapters.jellyfin] is missing from config.toml.
func DefaultConfig() Config {
	return Config{
		Enabled:             false,
		ServerURL:           "",
		DeviceName:          "",
		MaxVideoBitrateKbps: 4000,
		AutoAdvance:         false,
	}
}

// Validate enforces the rules in the spec §"Validate() rules". Returns
// nil when the config is acceptable. Empty server_url is permitted when
// Enabled=false (the operator has not yet linked).
//
// Failures are adapters.FieldErrors keyed by TOML key so the settings UI can
// show them against the field; a plain error reaches the operator as a
// misleading "couldn't write config.toml" notice (issue #7).
func (c Config) Validate() error {
	var errs adapters.FieldErrors
	if c.MaxVideoBitrateKbps < 200 || c.MaxVideoBitrateKbps > 50000 {
		errs = append(errs, adapters.FieldError{
			Key: "max_video_bitrate_kbps",
			Msg: fmt.Sprintf("must be between 200 and 50000, got %d", c.MaxVideoBitrateKbps),
		})
	}
	if msg := validateDeviceName(c.DeviceName); msg != "" {
		errs = append(errs, adapters.FieldError{Key: "device_name", Msg: msg})
	}
	if msg := validateServerURL(c.ServerURL, c.Enabled); msg != "" {
		errs = append(errs, adapters.FieldError{Key: "server_url", Msg: msg})
	}
	return errs.Err()
}

func validateDeviceName(name string) string {
	if len(name) > 64 {
		return fmt.Sprintf("length %d, must be at most 64", len(name))
	}
	for _, r := range name {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return fmt.Sprintf("must be ASCII-printable; got rune %U", r)
		}
	}
	return ""
}

func validateServerURL(raw string, enabled bool) string {
	if raw == "" {
		if enabled {
			return "required before enabling Jellyfin; enter the server URL and link first"
		}
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Sprintf("parse: %v", err)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Sprintf("scheme %q must be http or https", u.Scheme)
	case u.User != nil:
		return "must not contain username/password; credentials are stored separately after linking"
	case u.Path != "" && u.Path != "/":
		return fmt.Sprintf("path %q must be empty", u.Path)
	case u.RawQuery != "":
		return fmt.Sprintf("query string must be empty (got %q)", u.RawQuery)
	case u.Fragment != "":
		return fmt.Sprintf("fragment must be empty (got %q)", u.Fragment)
	case u.Host == "":
		return "host is required"
	}
	return ""
}
