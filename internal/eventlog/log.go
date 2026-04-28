// Package eventlog is a small in-memory ring buffer for adapter and
// core lifecycle events surfaced in the Status home and Diagnostics
// pages. See docs/specs/2026-04-26-ui-redesign-design.md §8.4.
//
// This package is a leaf utility: it depends on nothing else in this
// repo and is consumed by core, adapters, and ui. Do not import any
// internal package from here.
package eventlog

import "time"

// Severity ranks an entry. Render color in the UI keys off this.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityErr
)

// String returns a stable lowercase name. UI templates depend on these
// exact strings — do not rename.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarn:
		return "warn"
	case SeverityErr:
		return "err"
	default:
		return "unknown"
	}
}

// Entry is one ring-buffer record. Source is a short layer/component
// label (e.g. "core", "bridge", or an adapter name). Per spec §8.4
// each event class has exactly one writer; do not dual-write the
// same Source from multiple goroutines.
type Entry struct {
	Time     time.Time
	Severity Severity
	Source   string
	Message  string
}
