package plex

import (
	"net/url"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// Config is the [adapters.plex] TOML section. Fields map 1:1 to TOML
// keys; the adapter decodes this type via DecodeConfig using
// toml.PrimitiveDecode so scalar type preservation flows through.
type Config struct {
	Enabled     bool   `toml:"enabled"`
	DeviceName  string `toml:"device_name"`
	DeviceUUID  string `toml:"device_uuid"`
	ProfileName string `toml:"profile_name"`
	ServerURL   string `toml:"server_url"`
}

// DefaultConfig is the zero-config baseline: enabled, with the display
// names the bridge ships with. DeviceUUID is populated at first boot
// from the token-store (not here) so a fresh config doesn't burn a
// UUID that nobody has seen yet.
func DefaultConfig() Config {
	return Config{
		Enabled:     true,
		DeviceName:  "MiSTer",
		ProfileName: "Plex Home Theater",
	}
}

// Validate returns a FieldErrors accumulator covering every bad key
// (not one-at-a-time), so the UI can annotate every offending input
// in a single round-trip.
func (c *Config) Validate() error {
	var errs adapters.FieldErrors

	if c.DeviceName == "" {
		errs = append(errs, adapters.FieldError{
			Key: "device_name",
			Msg: "Device name is required.",
		})
	}
	if c.ProfileName == "" {
		errs = append(errs, adapters.FieldError{
			Key: "profile_name",
			Msg: "Profile name is required.",
		})
	}
	// Empty ServerURL is fine — the adapter falls back to GDM/plex.tv
	// auto-discovery. A non-empty value must parse as an absolute
	// URL with a scheme and host, otherwise it's clearly user typo.
	if c.ServerURL != "" {
		u, err := url.Parse(c.ServerURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, adapters.FieldError{
				Key: "server_url",
				Msg: "Not a valid URL (expected e.g. http://192.168.1.100:32400).",
			})
		}
	}

	return errs.Err()
}
