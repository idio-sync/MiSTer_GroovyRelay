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
// Phase 0 shipped the idle-only chassis preview. Phase 1 / Spec 2 wires the
// chassis VFD to live bridge session state: GET /receiver/events serves a
// long-lived Server-Sent Events stream emitting state and vfd events. The
// narrow SessionViewer interface (satisfied structurally by *core.Manager via
// its StatusHomeView method) is the read-only seam between the chassis and the
// bridge session; a per-server snapshot cache decouples connected-tab fan-out
// from core.Manager lock pressure.
//
// Phase 1 / Spec 4 adds receiver visualizer mode control. POST
// /receiver/visualizer accepts an application/x-www-form-urlencoded body with
// mode=<value> and returns 204 after the mode is persisted. Connected receiver
// tabs stay synchronized over GET /receiver/events through a visualizer SSE
// event whenever the active mode changes. The POST route is wrapped by
// requireSameOrigin, which accepts Sec-Fetch-Site values from the same browsing
// context and rejects missing or cross-site state-changing requests with 403.
//
// VisualizerViewer and VisualizerSaver are intentionally narrow interfaces:
// the chassis can read and save the bridge visualizer mode without importing
// internal/uiserver or depending on its settings form implementation.
// Later specs add transport controls and telemetry to the same SSE transport.
//
// See docs/superpowers/specs/2026-05-21-receiver-chassis-foundation-design.md
// and docs/superpowers/specs/2026-05-21-receiver-chassis-vfd-live-design.md
// for the full design.
package chassis
