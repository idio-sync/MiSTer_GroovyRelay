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

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// Config is the [adapters.url] TOML section. Single field in v1.
type Config struct {
	Enabled bool `toml:"enabled"`
}

// DefaultConfig returns the zero-config baseline: disabled. Operators
// must opt in via the settings UI toggle (or by editing the section
// in config.toml).
func DefaultConfig() Config {
	return Config{Enabled: false}
}

// Validate is a no-op in v1 (no range checks needed for a single bool).
// Returns the FieldErrors accumulator pattern for consistency with other
// adapters and to keep the door open for future fields.
func (c *Config) Validate() error {
	var errs adapters.FieldErrors
	return errs.Err()
}
