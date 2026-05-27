// Package chassis settings.go defines the chassis-owned interfaces and
// handlers for Phase 4A: the settings drawer + Network pane.
//
// internal/chassis intentionally does NOT import internal/uiserver. The
// production *uiserver.BridgeSaver satisfies BridgeSettingsSaver from
// outside; the typed settingsError wrapper that uiserver returns
// satisfies the settingsChipError interface structurally, matched via
// errors.As against the interface (Go 1.21+).
package chassis

import (
	"context"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// BridgeSettingsSaver is the narrow chassis-side interface for bridge
// settings persistence and snapshot. Production passes
// *uiserver.BridgeSaver, but internal/chassis does not import
// internal/uiserver — the wiring lives in cmd/mister-groovy-relay.
type BridgeSettingsSaver interface {
	// Current returns the live in-memory bridge config snapshot. The
	// chassis settings drawer uses this for first render and for
	// composing patches (current + touched-field overlay) on each save.
	Current() config.BridgeConfig

	// Save persists the patch to disk and dispatches in-memory side
	// effects. The returned ApplyScope is the max-wins scope across all
	// changed fields; the chassis maps it via scopeLabel before emitting
	// to the wire.
	Save(config.BridgeConfig) (adapters.ApplyScope, error)
}

// Prober is the narrow chassis-side interface the probe-mister action
// uses. Production passes a thin wrapper around the existing
// bridgeMisterProber in cmd/mister-groovy-relay/launcher.go, which uses
// CMD_GET_STATUS over an ephemeral source port (NOT the live sender's
// bound source port).
type Prober interface {
	ProbeMister(ctx context.Context, bridge config.BridgeConfig) (ProbeResult, error)
}

// ProbeResult is the structured success payload from a probe attempt.
// LatencyMs is the wall-clock round-trip in milliseconds (e.g. 4.2).
type ProbeResult struct {
	LatencyMs float64
	Host      string
	Port      int
}

// settingsChipError is matched structurally so saver-layer typed errors
// can carry HTTP/chip details across the interface boundary without a
// uiserver import. The chassis handler uses errors.As against the
// interface (Go 1.21+).
type settingsChipError interface {
	error
	StatusCode() int
	Chip() string
}
