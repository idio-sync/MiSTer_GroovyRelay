// Package chassis serves the receiver-chassis-styled UI under /receiver/.
//
// Phase 0 of a 9-spec rollout replaces the existing /ui/* surface later,
// while the chassis ships in parallel under /receiver/* until cutover.
// Until then, /ui/* is unaffected.
//
// Design isolation: this package has zero imports of internal/ui or
// internal/uiserver, and those packages have zero imports of this one. The
// composition root is cmd/mister-groovy-relay/main.go, which wires both
// servers onto the same http.ServeMux.
//
// Phase 0 renders the chassis in idle state only: no live data, playback
// control, or telemetry. Later specs replace the idle-only surface with live
// receiver behavior.
//
// See docs/superpowers/specs/2026-05-21-receiver-chassis-foundation-design.md
// for the full design.
package chassis
