// Package jellyfin is the Jellyfin cast-target adapter. See
// docs/specs/2026-04-25-jellyfin-adapter-design.md for the design.
package jellyfin

import (
	"errors"
	"fmt"
	"net/url"
	"unicode"
)

// Config is the [adapters.jellyfin] TOML section. Secrets (access
// token, user id, server id) are persisted separately under
// <data_dir>/jellyfin/token.json — see tokenstore.go.
type Config struct {
	Enabled             bool   `toml:"enabled"`
	ServerURL           string `toml:"server_url"`
	DeviceName          string `toml:"device_name"`
	MaxVideoBitrateKbps int    `toml:"max_video_bitrate_kbps"`
}

// DefaultConfig returns the zero-value config the adapter starts with
// when [adapters.jellyfin] is missing from config.toml.
func DefaultConfig() Config {
	return Config{
		Enabled:             false,
		ServerURL:           "",
		DeviceName:          "",
		MaxVideoBitrateKbps: 4000,
	}
}

// Validate enforces the rules in the spec §"Validate() rules". Returns
// nil when the config is acceptable. Empty server_url is permitted when
// Enabled=false (the operator has not yet linked).
func (c Config) Validate() error {
	if c.MaxVideoBitrateKbps < 200 || c.MaxVideoBitrateKbps > 50000 {
		return fmt.Errorf("max_video_bitrate_kbps = %d, must be between 200 and 50000", c.MaxVideoBitrateKbps)
	}
	if len(c.DeviceName) > 64 {
		return fmt.Errorf("device_name length = %d, must be <= 64", len(c.DeviceName))
	}
	for _, r := range c.DeviceName {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return fmt.Errorf("device_name must be ASCII-printable; got rune %U", r)
		}
	}
	if !c.Enabled && c.ServerURL == "" {
		return nil
	}
	if c.ServerURL == "" {
		return errors.New("server_url is required when enabled = true")
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return fmt.Errorf("server_url: parse: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("server_url: scheme %q must be http or https", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("server_url: must not contain username/password; credentials are stored separately after linking")
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("server_url: path %q must be empty", u.Path)
	}
	if u.RawQuery != "" {
		return fmt.Errorf("server_url: query string must be empty (got %q)", u.RawQuery)
	}
	if u.Fragment != "" {
		return fmt.Errorf("server_url: fragment must be empty (got %q)", u.Fragment)
	}
	if u.Host == "" {
		return errors.New("server_url: host is required")
	}
	return nil
}
