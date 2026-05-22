# Receiver Chassis Visualizer Mode — Phase 1 / Spec 4 Design

**Status:** Implementation plan drafted; blocked on the Spec 2 SSE/session foundation.
**Scope:** Third sub-project of Phase 1 (Live Console). Wires the chassis visualizer-bank buttons to `bridge.visualizer.mode` via a new `POST /receiver/visualizer` endpoint, adds a `visualizer` event to the existing SSE stream for cross-tab synchronization, and introduces the first chassis cross-origin defence (`Sec-Fetch-Site` middleware).
**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored (`.gitignore` line 35); this spec is force-added per the convention established by the Phase 0 design doc.

## Background

[Phase 0](2026-05-21-receiver-chassis-foundation-design.md) shipped the chassis chrome, including a 4-button visualizer bank at `internal/chassis/templates/visualizer-bank.html` with cosmetic JS-only toggle in `chassis.js` (flips `active`/`lit` classes; does not save). [Spec 2](2026-05-21-receiver-chassis-vfd-live-design.md) introduced the SSE stream at `GET /receiver/events` and the `SessionViewer` interface pattern. Spec 4 is the smallest sub-spec in the rollout: it turns the cosmetic toggle into a real save and extends Spec 2's SSE stream to keep multiple chassis tabs synchronized.

The bridge already persists `bridge.visualizer.mode` to `config.toml` via the existing `/ui/*` form-POST path. `uiserver.BridgeSaver.saveLocked` validates, atomic-writes, and refreshes `core.Manager`'s in-memory bridge config (via `UpdateBridge`). Visualizer mode is classified as `adapters.ScopeNextCast` — applies on the next cast, does not drop the active one. Spec 4 reuses this path verbatim through a new narrow-save method, mirroring the existing `SaveOutputVolume` pattern.

The deferred fourth mode (`radial_spectrum`) is intentionally absent from `config.SupportedVisualizerModes()` per the `feat(ffmpeg): defer radial_spectrum mode from v1` commit. The chassis must reject it server-side; the existing client-side disable is reinforced, not replaced.

## Prerequisite

Spec 4 is not standalone against the Phase 0-only chassis tree. It requires Spec 2 to land first, providing:

- `internal/chassis/events.go` with `GET /receiver/events`, the SSE encoder, heartbeat/retry behavior, and snapshot diff ticker.
- `internal/chassis/session.go` with `SessionViewer` and `snapshotFromSession(cfg, sv, now)`.
- `internal/chassis/static/vfd-live.js` with the shared chassis `EventSource`.
- `internal/chassis/server.go` support for the Spec 2 session viewer, snapshot cache, and `Close()` lifecycle hook.
- `internal/chassis/templates/shell.html` loading `vfd-live.js` before later SSE consumers.

If those files are absent, implement or merge Spec 2 before starting this spec. The file table below marks Spec 2-owned files as **prerequisite files modified by Spec 4**, not as files created here.

## Goals

1. Mount `POST /receiver/visualizer` accepting form-encoded `mode=<value>`. The chassis handler validates locally against `config.SupportedVisualizerModes()` before invoking the saver. Returns 204 on success, 400 on validation failure, 403 on cross-site origin, 405 on non-POST.
2. Persist accepted saves through `uiserver.BridgeSaver.SaveVisualizerMode` (new narrow-save method mirroring `SaveOutputVolume`). Atomic disk write + `core.Manager.UpdateBridge`.
3. Extend the SSE stream with a `visualizer` event emitted on connect (initial snapshot) and on every detected mode change. Cross-tab synchronization is automatic.
4. Add a new client script `internal/chassis/static/visualizer-bank.js` that POSTs on click (Hybrid feedback: momentary CSS pressed-state on click, `active`/`lit` classes only move when the SSE event arrives) and updates DOM on SSE events.
5. Introduce package-local `requireSameOrigin` middleware enforcing `Sec-Fetch-Site` checks. Sets the cross-origin defence pattern Spec 3 (transport) inherits.
6. Preserve the no-cross-import invariant. `internal/chassis/` still imports only `internal/core`, `internal/config`, `internal/adapters`. The `VisualizerSaver` interface lives in chassis; `main.go` wires a closure adapter over `uiserver.BridgeSaver`.

## Non-goals

- Transport controls (play / pause / stop / seek POSTs) — Spec 3.
- Telemetry events (spectrum / goniometer / throughput / ACK / audio scope) — Spec 5.
- Replacing the `/ui/*` bridge-panel visualizer dropdown. The chassis save is additive; both surfaces continue to write through the same `BridgeSaver`. Spec 8 (settings drawer) revisits this.
- Adding a publish/subscribe broker to `core.Manager`. Spec 4 extends Spec 2's diff-ticker pattern; Spec 5 reconsiders if telemetry rates demand a broker.
- Toast / banner error UI on save failure. The PR description's manual checklist treats save errors as out-of-band debugging via DevTools console. Spec 8 (settings) is where toast infrastructure pays off.
- JS unit tests for `visualizer-bank.js`. ~70 lines of DOM glue; exercised by integration tests + manual verification.

## Done When

- Clicking a non-preview visualizer-bank button POSTs `/receiver/visualizer` with the correct mode and receives 204.
- Within ~500 ms, all open `/receiver` tabs reflect the new active button.
- `config.toml`'s `bridge.visualizer.mode` reflects the persisted value, and `core.Manager.VisualizerMode()` returns the new mode.
- Restarting the bridge and reloading `/receiver` shows the persisted mode as the initial active button (server-side render, no flash).
- `radial_spectrum` is rejected with 400 even if posted directly via curl.
- Cross-site POST attempts (browsers signal these via `Sec-Fetch-Site: cross-site`) receive 403.
- No regressions on `/ui/*` (its bridge-panel visualizer dropdown reads/writes the same field).
- `TestProductionImports_NoCrossPackageCoupling` still green.
- `TestChassisCSS_AllSelectorsScoped` still green (new CSS for the pressed state is body.receiver-scoped).

## Architecture

### Files added

```
internal/chassis/
├── visualizer.go              # NEW: VisualizerViewer + VisualizerSaver interfaces,
│                              #      handleVisualizerPost, mode validation,
│                              #      JSON-error helpers
├── visualizer_test.go         # NEW: Layer 1 + Layer 2 tests
├── sameorigin.go              # NEW: requireSameOrigin middleware (~25 lines)
└── static/
    └── visualizer-bank.js     # NEW: EventSource subscriber + click handler
```

### Files modified

| File | Change |
|---|---|
| `internal/chassis/server.go` | `Config` gains optional `VisualizerViewer` and `VisualizerSaver` fields. Stored on `Server`. |
| `internal/chassis/events.go` (Spec 2 prerequisite) | Snapshot diff loop gains `visualizer` event emission. `vizEnvelope` added. |
| `internal/chassis/session.go` (Spec 2 prerequisite) | `snapshotFromSession` signature extended with `VisualizerViewer`; populates `ReceiverPageData.Visualizer.ActiveMode` from the live viewer. |
| `internal/chassis/handler.go` | `Mount` registers `POST /receiver/visualizer` wrapped in `requireSameOrigin`. |
| `internal/chassis/templates/shell.html` | New `<script defer src="/receiver/static/visualizer-bank.js?v={{.Version}}">` after `vfd-live.js`. |
| `internal/chassis/static/chassis.css` | Adds `body.receiver .viz-btn.pressed { ... }` rules for click feedback. |
| `internal/chassis/static/vfd-live.js` (Spec 2 prerequisite) | After `new EventSource(...)`, dispatches `CustomEvent('chassis:eventsource', { detail: { source } })`. Stores `source` on `window.Chassis.events.source` for late-attachers. |
| `internal/uiserver/bridge_saver.go` | New method `SaveVisualizerMode(mode string) (adapters.ApplyScope, error)`. |
| `internal/core/manager.go` | New method `VisualizerMode() string`. |
| `cmd/mister-groovy-relay/main.go` | Wires `VisualizerViewer: coreMgr` and `VisualizerSaver` (closure over `BridgeSaver.SaveVisualizerMode`) into `chassis.Config`. |

### Interfaces

```go
// internal/chassis/visualizer.go
package chassis

// VisualizerViewer is the read-only view of the live bridge's
// visualizer mode. *core.Manager satisfies it structurally via
// VisualizerMode() (added in this spec). Tests inject fakes.
// Mirrors Spec 2's SessionViewer pattern.
type VisualizerViewer interface {
    VisualizerMode() string
}

// VisualizerSaver persists a new visualizer mode and refreshes the
// live in-memory bridge config. main.go wires this via a closure over
// uiserver.BridgeSaver.SaveVisualizerMode so chassis doesn't depend
// on internal/uiserver.
type VisualizerSaver interface {
    SaveVisualizerMode(mode string) error
}
```

### Cross-package boundary preserved

`internal/chassis/` continues to import only `internal/core`, `internal/config`, `internal/adapters`. No new imports of `internal/ui` or `internal/uiserver`. The closure adapter in `main.go` is the single composition root. `TestProductionImports_NoCrossPackageCoupling` (Phase 0) enforces.

### Route additions

`Mount` gains one entry:

```go
mux.Handle("POST /receiver/visualizer",
    requireSameOrigin(http.HandlerFunc(s.handleVisualizerPost)))
```

Go 1.22+ method-aware mux registers POST distinctly from the existing GET routes. Non-POST requests fall through to a default 405 from the mux.

### Manager addition

```go
// VisualizerMode returns the live bridge's visualizer mode under
// m.mu. Tracks the in-memory bridge updated by UpdateBridge.
func (m *Manager) VisualizerMode() string {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.bridge.Visualizer.Mode
}
```

Pure in-memory read; honors the "Manager.mu is never held across network I/O" invariant.

### BridgeSaver addition

```go
// SaveVisualizerMode atomically persists only bridge.visualizer.mode
// against the latest in-memory bridge snapshot. Mirrors the
// SaveOutputVolume pattern so concurrent saves of other fields don't
// race against the in-memory current() snapshot.
func (r *BridgeSaver) SaveVisualizerMode(mode string) (adapters.ApplyScope, error) {
    r.mu.Lock()
    defer r.mu.Unlock()
    next := r.sec.Bridge
    next.Visualizer.Mode = mode
    return r.saveLocked(next)
}
```

`saveLocked` runs the full pipeline: validate → diff → atomic write → `core.Manager.UpdateBridge` → scope dispatch. Visualizer is `ScopeNextCast` per `scopeForBridgeField`; the active cast is not dropped.

### Closure adapter in main.go

The HTTP handler validates the mode before invoking the saver, so the `main.go` adapter does not translate validation errors or match `config.Validate()` prose. Use a small struct adapter (matches the rest of `main.go`'s adapter style — `bridgeSaver` is already passed by value/pointer rather than function-typed). The struct can be declared near the chassis-config wiring:

```go
// visualizerSaverAdapter bridges chassis.VisualizerSaver to the
// uiserver.BridgeSaver narrow-save path. Mode validation happens
// inside the chassis HTTP handler before this adapter is called.
type visualizerSaverAdapter struct {
    bs *uiserver.BridgeSaver
}

func (a *visualizerSaverAdapter) SaveVisualizerMode(mode string) error {
    _, err := a.bs.SaveVisualizerMode(mode)
    return err
}

// ... later in main.go, alongside other chassis-config wiring:
chassisCfg.VisualizerSaver = &visualizerSaverAdapter{bs: bridgeSaver}
```

## SSE Wire Protocol Extension

Spec 4 adds one event name to Spec 2's vocabulary. Connection headers, heartbeat, retry, and reconnect behaviour are unchanged.

### New event

```
event: visualizer
data: {"mode":"stereo_scope"}
```

| Field | Type | Notes |
|---|---|---|
| `mode` | string | One of `config.SupportedVisualizerModes()` (currently three values). Never `radial_spectrum`. Never empty (server normalizes via `config.NormalizeVisualizerMode`). |

### Envelope

```go
type vizEnvelope struct {
    Mode string `json:"mode"`
}
```

Explicit `json:"mode"` tag enforces camelCase regardless of Go field naming (matches Spec 2's discipline).

### Initial-snapshot sequence on connect

```
retry: 3000

event: state
data: {"state":"idle"}

event: vfd
data: {"title":"STANDBY",...}

event: visualizer
data: {"mode":"retro_analyzer"}

```

Trailing blank line terminates the SSE record. Reconnect replays this full initial burst so the client always re-syncs.

### Diff-ticker emission

The diff comparison runs against the same `ReceiverPageData` snapshot cache Spec 2 already populates — the cache's stored snapshot now carries the live `Visualizer.ActiveMode` because `snapshotFromSession` (above) writes it on every refresh tick. No second cache, no broker. The per-handler diff state grows by one field: `last.Visualizer.ActiveMode` lives next to Spec 2's `last.State` and `last.VFD` as a local variable in `handleEvents`, not as a new field on the cache struct.

In `events.go`'s diff loop, alongside the existing state and vfd checks:

```go
if curr.Visualizer.ActiveMode != last.Visualizer.ActiveMode {
    if err := emit(w, "visualizer", vizEnvelope{Mode: curr.Visualizer.ActiveMode}); err != nil {
        return
    }
    last.Visualizer.ActiveMode = curr.Visualizer.ActiveMode
}
```

Single-field compare — no helper extracted (Spec 2's `vfdChanged` covers multiple fields; this is one).

### Snapshot population

`snapshotFromSession` signature extended with a `VisualizerViewer` parameter. Spec 2's function has three return paths (nil session, idle session, live session); Spec 4's visualizer override must apply to all three because the live in-memory mode can differ from `cfg.Bridge.Visualizer.Mode` regardless of session state.

**Implementation directive:** refactor Spec 2's three early returns into a single fall-through that returns `base` once at the bottom, and apply the visualizer override in that single trailing step. The "inline at every return" alternative is rejected — it leaves the same line of code in three places (a future Spec 5 telemetry override would have to repeat the pattern a fourth time) and makes the override-layering order harder to read. The refactor diff against the freshly-merged Spec 2 PR is small (three `return idleSnapshot(...)` / `return base` lines become assignments + a single `return base`).

```go
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, now time.Time) ReceiverPageData {
    base := idleSnapshot(cfg, now)
    if sv != nil {
        view := sv.StatusHomeView()
        if view.State != core.StateIdle {
            base.State = StateLive
            base.VFD.State = string(StateLive)
            base.VFD.Title = view.Title
            base.VFD.Marquee = formatLiveMarquee(view)
        }
    }
    base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
    return base
}

func liveVisualizerMode(cfg Config, vv VisualizerViewer) string {
    if vv == nil {
        return defaultVisualizerMode(cfg) // Phase 0 fallback (reads cfg.Bridge)
    }
    return config.NormalizeVisualizerMode(vv.VisualizerMode())
}
```

Nil-viewer falls back to the Phase 0 helper (reads `cfg.Bridge.Visualizer.Mode`, a startup snapshot). Test-friendly + offline-friendly.

### Latency budget

Engineering estimate: click → POST → `BridgeSaver` write + `UpdateBridge` (≤10 ms) → cache refresher next tick (≤250 ms) → SSE emit → DOM update (~1 ms). Worst case ~260 ms + the POST roundtrip. The Hybrid `pressed`-class affordance fires within ~5 ms of click, so perceived latency is the press flash, not the active-class transition.

The **acceptance criterion in Done When** is "within ~500 ms" — a UX SLO with substantial headroom over the ~260 ms engineering estimate. Automated tests should assert eventual convergence with a generous timeout and separately cover the configured ticker interval; they should not hard-fail CI on a 500 ms wall-clock threshold.

## Save Endpoint

### Contract

```
POST /receiver/visualizer
Content-Type: application/x-www-form-urlencoded
Sec-Fetch-Site: same-origin | same-site

Body: mode=<one of retro_analyzer | oscilloscope_wave | stereo_scope>
```

| Outcome | Status | Body |
|---|---|---|
| Success | `204 No Content` | empty |
| Missing `mode` field OR empty `mode=` value | `400 Bad Request` | `{"error":"missing mode field"}` (same body for both — handler trims to empty and treats identically) |
| Unsupported mode (incl. `radial_spectrum`) | `400 Bad Request` | `{"error":"unsupported visualizer mode","mode":"<sent>"}` |
| Cross-site `Sec-Fetch-Site` (or `none`) | `403 Forbidden` | `{"error":"cross-site request blocked"}` |
| Missing `Sec-Fetch-Site` header | `403 Forbidden` | `{"error":"cross-site request blocked"}` |
| Non-POST (mux fall-through) | `405 Method Not Allowed` | empty; `Allow: POST` |
| `VisualizerSaver == nil` | `503 Service Unavailable` | `{"error":"visualizer save not configured"}` |
| Internal save failure | `500 Internal Server Error` | `{"error":"internal save failure"}` (detailed error logged, not returned) |

204 (not 200) signals "POST accepted; the SSE event is the success notification." For a chassis browser tab with an open SSE stream this is sufficient — the next `visualizer` event confirms the save end-to-end. For non-browser callers (curl / ops scripts that don't keep an SSE connection open), the operator verifies by inspecting `config.toml` or by issuing a follow-up `GET /receiver` and reading the rendered active button. The chassis-browser path is the primary flow this endpoint is designed for.

Form-encoded (not JSON) matches the existing `/ui/*` form-POST convention, aligns with `r.PostFormValue`, and would let a future no-JS fallback `<form action="/receiver/visualizer" method="post">` work if the JS bundle ever fails to load.

### Handler

The HTTP boundary owns mode validation. Unsupported values never reach `VisualizerSaver`, which keeps `cmd/mister-groovy-relay/main.go` free of brittle error-string translation. `BridgeSaver.saveLocked` still runs full config validation as a second line of defense.

```go
func (s *Server) handleVisualizerPost(w http.ResponseWriter, r *http.Request) {
    if s.cfg.VisualizerSaver == nil {
        writeJSONError(w, http.StatusServiceUnavailable, "visualizer save not configured")
        return
    }
    if err := r.ParseForm(); err != nil {
        writeJSONError(w, http.StatusBadRequest, "malformed form body")
        return
    }
    mode := strings.TrimSpace(r.PostFormValue("mode"))
    if mode == "" {
        writeJSONError(w, http.StatusBadRequest, "missing mode field")
        return
    }
    if !isSupportedVisualizerMode(mode) {
        writeJSONErrorWithMode(w, http.StatusBadRequest, "unsupported visualizer mode", mode)
        return
    }
    if err := s.cfg.VisualizerSaver.SaveVisualizerMode(mode); err != nil {
        // Log the full error server-side via stdlib log.Printf (the
        // chassis package has no logger today; introducing a structured
        // logger is out of scope for this spec — Phase 5 polish task).
        // The client receives only the generic message; the detailed
        // error reaches operators via the server log.
        log.Printf("chassis: visualizer save failed: mode=%q err=%v", mode, err)
        writeJSONError(w, http.StatusInternalServerError, "internal save failure")
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

func isSupportedVisualizerMode(mode string) bool {
    normalized := config.NormalizeVisualizerMode(mode)
    for _, supported := range config.SupportedVisualizerModes() {
        if normalized == supported {
            return true
        }
    }
    return false
}
```

`writeJSONError`, `writeJSONErrorWithMode`, and `isSupportedVisualizerMode` live in `visualizer.go`.

### Eventlog

`BridgeSaver.saveLocked` already appends `bridge-config-saved scope=ScopeNextCast` to the eventlog. No new event type. Spec 9 (event log UI) decides whether to distinguish chassis-triggered from /ui-triggered saves.

## Same-Origin Middleware

```go
// internal/chassis/sameorigin.go
package chassis

import "net/http"

// requireSameOrigin rejects POST requests whose Sec-Fetch-Site is not
// same-origin or same-site. All current browsers send Sec-Fetch-Site
// on every fetch request; the chassis targets modern browsers only
// (Phase 0's container queries already require Safari 16+).
//
// Accepts: same-origin, same-site.
// Rejects: cross-site, none, missing header.
//
// "none" is deliberately rejected for POST endpoints even though it's
// a legitimate browser value for top-level navigations (typed URL,
// bookmark click). Top-level navigations are GETs; a POST that arrives
// with Sec-Fetch-Site: none is not a real browser flow, so rejecting it
// tightens the accept surface without breaking any real user path.
//
// Non-browser clients (curl, ops scripts) can opt in by setting the
// header explicitly: `-H "Sec-Fetch-Site: same-origin"`. Documented in
// the README troubleshooting section.
func requireSameOrigin(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch r.Header.Get("Sec-Fetch-Site") {
        case "same-origin", "same-site":
            next.ServeHTTP(w, r)
        default:
            writeJSONError(w, http.StatusForbidden, "cross-site request blocked")
        }
    })
}
```

This is intentionally stricter than the existing `/ui/*` CSRF middleware: no browser-extension bypass, no `Origin` fallback, and no `Sec-Fetch-Site: none` acceptance. The chassis POST endpoints are first-party console controls driven by bundled JS; non-browser clients must opt in with the header above. Reused by Spec 3 (transport) when it lands; pattern stays in `internal/chassis/sameorigin.go` until a third consumer justifies extraction.

## Client-Side Update Strategy

### Hybrid feedback model

Decided in brainstorming: server-authoritative, but with an immediate CSS press affordance for haptic feedback. No client-side rollback path; the `active`/`lit` classes only move when the SSE event arrives.

### New file: `internal/chassis/static/visualizer-bank.js`

~70 lines vanilla ES2022. Loaded via `<script defer>` after `chassis.js` and `vfd-live.js`. Attaches to `window.Chassis`. No new globals.

```javascript
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('visualizer-bank: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  const PRESSED_MS = 180;

  function bankRoot() { return document.querySelector('.viz-bank'); }

  function setActiveMode(mode) {
    const root = bankRoot();
    if (!root) return;
    for (const btn of root.querySelectorAll('.viz-btn')) {
      const isActive = btn.dataset.viz === mode &&
                       !btn.classList.contains('viz-btn--preview');
      btn.classList.toggle('active', isActive);
      btn.classList.toggle('lit', isActive);
      btn.setAttribute('aria-checked', isActive ? 'true' : 'false');
    }
  }

  function flashPressed(btn) {
    btn.classList.add('pressed');
    setTimeout(() => btn.classList.remove('pressed'), PRESSED_MS);
  }

  async function postMode(mode) {
    const body = new URLSearchParams({ mode });
    try {
      const res = await fetch('/receiver/visualizer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body,
        credentials: 'same-origin',
      });
      if (res.status === 204) return;
      const text = await res.text().catch(() => '');
      console.warn('visualizer-bank: save failed', res.status, text);
    } catch (err) {
      console.warn('visualizer-bank: save request errored', err);
    }
  }

  function onClick(ev) {
    const btn = ev.target.closest('.viz-btn');
    if (!btn) return;
    if (btn.classList.contains('viz-btn--preview') || btn.disabled) return;
    const mode = btn.dataset.viz;
    if (!mode) return;
    flashPressed(btn);
    postMode(mode);
  }

  function handleVisualizerEvent(ev) {
    try {
      const { mode } = JSON.parse(ev.data);
      if (typeof mode === 'string' && mode.length > 0) {
        setActiveMode(mode);
      }
    } catch (err) {
      console.warn('visualizer-bank: bad payload', ev.data, err);
    }
  }

  function attachToEventSource(source) {
    if (source) source.addEventListener('visualizer', handleVisualizerEvent);
  }

  function attach() {
    const root = bankRoot();
    if (root) root.addEventListener('click', onClick);
    // Subscribe to the shared EventSource owned by vfd-live.js. Defer
    // scripts run in document order, so vfd-live.js's DOMContentLoaded
    // handler may already have fired by the time we reach this point;
    // the cached reference path covers that case, the CustomEvent path
    // covers a future reconnect that creates a fresh EventSource.
    if (window.Chassis.events && window.Chassis.events.source) {
      attachToEventSource(window.Chassis.events.source);
    }
    document.addEventListener('chassis:eventsource', (e) => {
      attachToEventSource(e.detail.source);
    });
  }

  document.addEventListener('DOMContentLoaded', attach);
})();
```

### Coordination with `vfd-live.js` (Spec 2 amendment)

Spec 2 currently creates a single `EventSource` inside `vfd-live.js`. Spec 4 needs to share that source rather than open a second one. Spec 4 amends Spec 2's `vfd-live.js`:

1. After `source = new EventSource('/receiver/events')`, store the reference on `window.Chassis.events = { source, reconnect: ... }` (the existing `reconnect` API stays).
2. Dispatch `document.dispatchEvent(new CustomEvent('chassis:eventsource', { detail: { source } }))` immediately after.

These two changes are ~3 lines and don't alter `vfd-live.js`'s wire behaviour or test surface.

**Defer-order semantics on first load.** Both scripts use `<script defer>`, which preserves document order. `vfd-live.js` loads first per `shell.html`'s ordering; its `DOMContentLoaded` handler creates the EventSource and dispatches the CustomEvent *before* `visualizer-bank.js`'s `DOMContentLoaded` handler runs. Result: on first connect, the cached `window.Chassis.events.source` reference is the path that always fires; the CustomEvent path matters only on reconnects (when `vfd-live.js` is wired to call `connect()` again, the new source replaces the cached one and the CustomEvent picks up late-attached listeners). This ordering is encoded in the JS comment. Until the Spec 5 Vitest/jsdom harness exists, Spec 4 verifies this through static asset checks plus integration/manual single-SSE-connection checks rather than a real JS unit test.

### Template changes

- `internal/chassis/templates/visualizer-bank.html`: **no change**. Phase 0 already renders `data-viz`, `class="viz-btn{{if eq .Mode $.ActiveMode}} active lit{{end}}"`, and `aria-checked`. The Spec 4 client reads/writes those attributes directly.
- `internal/chassis/templates/shell.html`: one extra `<script defer>` tag.

### CSS additions

```css
body.receiver .viz-btn {
  transition: transform 120ms ease, box-shadow 120ms ease;
}
body.receiver .viz-btn.pressed {
  transform: translateY(1px);
  box-shadow: inset 0 1px 3px rgba(0, 0, 0, 0.4);
  transition: none;
}
```

Both rules are `body.receiver`-rooted. `TestChassisCSS_AllSelectorsScoped` (Phase 0) verifies.

### Preview button behaviour

Phase 0 markup renders `radial_spectrum` with `disabled` + `aria-disabled="true"`. The click handler exits early on `btn.disabled` or `viz-btn--preview`. If someone hand-crafts a POST with `mode=radial_spectrum` while it remains absent from `config.SupportedVisualizerModes()`, the chassis handler rejects it before invoking the saver and returns 400 with `{"error":"unsupported visualizer mode","mode":"radial_spectrum"}`. Defense in depth.

## Testing Approach

### Layer 1 — Go unit + handler tests

```
TestVisualizerViewer_ManagerSatisfiesInterface
TestVisualizerSaver_NilSaverYields503

TestSnapshotFromSession_VisualizerModeOverridesIdleDefault
TestSnapshotFromSession_NilVisualizerViewerFallsBackToCfg
TestSnapshotFromSession_NormalizesEmptyMode

TestHandleVisualizerPost_AcceptsValidMode
TestHandleVisualizerPost_Returns204OnSuccess
TestHandleVisualizerPost_Returns400OnMissingOrEmptyModeField  // covers both: key absent OR mode=""
TestHandleVisualizerPost_Returns400OnUnsupportedMode
TestHandleVisualizerPost_Returns400OnRadialSpectrumDeferred
TestHandleVisualizerPost_DoesNotInvokeSaverForUnsupportedMode
TestHandleVisualizerPost_Returns405OnGet
TestHandleVisualizerPost_Returns503WhenSaverNil
TestHandleVisualizerPost_Returns500OnSaverInternalError
TestHandleVisualizerPost_LogsButNeverLeaksInternalError
TestHandleVisualizerPost_RapidSequentialClicks    // race-detected

TestRequireSameOrigin_AllowsSameOrigin
TestRequireSameOrigin_AllowsSameSite
TestRequireSameOrigin_BlocksNone                // POSTs with Sec-Fetch-Site: none are rejected
TestRequireSameOrigin_BlocksCrossSite
TestRequireSameOrigin_BlocksMissingHeader
TestRequireSameOrigin_BlocksReturns403JSON

TestHandleEvents_EmitsInitialVisualizerEventOnConnect
TestHandleEvents_EmitsVisualizerEventOnModeChange
TestHandleEvents_VisualizerEventOmittedWhenModeUnchanged

TestVizEnvelope_JSONCamelCase
TestVfdLive_ExposesEventSourceReference_StaticAssetCheck
```

**Fixtures:**

- `fakeVisualizerViewer struct { mu sync.Mutex; mode string; set(string) }` — mutex-guarded so the diff-ticker test can flip the mode mid-stream deterministically.
- `fakeVisualizerSaver struct { mu sync.Mutex; saved []string; err error }` — records calls, optionally returns a configured error.

The static `vfd-live.js` check is deliberately narrow: it verifies the file exposes `window.Chassis.events.source` and dispatches `chassis:eventsource`. Real dispatch-order behavior is covered by the browser/manual checklist until the Spec 5 JS harness lands.

### Layer 2 — template tests

```
TestVisualizerBankTemplate_RendersActiveButtonForConfiguredMode
TestVisualizerBankTemplate_RendersPreviewBadgeForRadialSpectrum
TestVisualizerBankTemplate_RendersDataVizOnEveryButton
```

### Layer 3 — integration (`//go:build integration`)

```
TestReceiverVisualizer_EndToEnd_PostAndSSEEvent
TestReceiverVisualizer_BlocksCrossSitePost
TestReceiverVisualizer_PreviewModeRejected
TestReceiverVisualizer_DoesNotShadowUIRoutes
```

`TestReceiverVisualizer_EndToEnd_PostAndSSEEvent` spins a full bridge with a real `BridgeSaver` writing to a temp dir; opens an SSE connection; reads the initial `visualizer` event; POSTs `/receiver/visualizer mode=stereo_scope`; waits for eventual `visualizer` convergence with a generous timeout; reads `config.toml` from disk and asserts `bridge.visualizer.mode = "stereo_scope"`; calls `coreMgr.VisualizerMode()` and asserts the live in-memory bridge matches.

### Manual verification (PR checklist)

- Open `/receiver` in two tabs. Click `STEREO SCOPE` in tab A. Tab B's active button moves within ~250ms. Repeat for each non-preview button. Open `/ui/*` bridge settings — visualizer dropdown reflects the chassis-driven value.
- Save `radial_spectrum` via curl with `Sec-Fetch-Site: same-origin`. Expect 400 JSON `unsupported visualizer mode`. Confirm `config.toml` unchanged.
- Run a music cast via Plex. Mid-cast, switch the visualizer mode. Cast continues uninterrupted (verifies `ScopeNextCast` not dropping). End and start a new cast — new mode is in effect (validated via `coreMgr.VisualizerMode()` until Spec 5 surfaces the live visualizer on the meter screen).
- `curl -X POST http://localhost:32500/receiver/visualizer -d mode=stereo_scope` with no Sec-Fetch-Site header. Expect 403.
- Restart the bridge, reload `/receiver`. Persisted mode is the active button on initial render (server-side, no flash).
- DevTools Network → EventStream: confirm `visualizer` events appear with correct payload.

### Explicitly out of scope

- **JS unit tests for `visualizer-bank.js`.** ~70 lines; exercised by Layer 3 integration tests + manual verification. Spec 5 (telemetry meter animators — `meter-animators.js`) is where dedicated JS test infrastructure pays off. Planned framework destination: **Vitest** running in jsdom, with a `tests/jslike/` directory at the repo root. Spec 5 will introduce the harness; Spec 4's `visualizer-bank.js` retrofits trivially once it exists (selector-based DOM assertions on a static fixture HTML, no chassis-specific test infrastructure required).
- Load tests on the POST endpoint. One operator at a time.
- Fuzz tests on the form parser. Stdlib + constrained enum.

### CI integration

Same as Spec 2 — `go vet`, `go test`, `go test -race`, `go test -tags=integration`. No new jobs.

## Migration & Rollout

### Coexistence invariants

- `/ui/*` bridge-panel visualizer dropdown and the chassis visualizer-bank stay synchronized via the shared `BridgeSaver`. Saves from either path land in the same `config.toml`, trigger the same `UpdateBridge`, are visible on the next page load of the other surface.
- `internal/chassis/` still imports only `internal/core`, `internal/config`, `internal/adapters`. The closure adapter in `main.go` is the only crossing point.
- Mount order in `main.go` unchanged. POST routes are method-distinct on the Go 1.22+ mux.

### Config & flags

No new config fields. The endpoint is unconditionally mounted.

### Asset caching and versioning

`visualizer-bank.js` served at `/receiver/static/visualizer-bank.js?v={{.Version}}` — same cache-buster mechanism as Phase 0 assets. `Cache-Control: public, max-age=31536000, immutable`.

### Docs

- `internal/chassis/doc.go` — append a paragraph noting the POST endpoint + `VisualizerSaver` interface + `visualizer` SSE event.
- `README.md` troubleshooting / operator notes — document that curl or ops scripts must include `Sec-Fetch-Site: same-origin` for chassis POST endpoints.
- This design doc.

### Rollback strategy

Spec 4 is additive. Revert = revert the merge commit. The chassis falls back to Phase 0's cosmetic toggle (no save); `/ui/*` continues to be the only persistence path. No data migration; no persistent state introduced.

### Risk register

| Risk | Mitigation |
|---|---|
| Two SSE connections per tab if visualizer-bank.js opens its own EventSource | The `chassis:eventsource` CustomEvent + `window.Chassis.events.source` cache deliberately share the one source from vfd-live.js. Integration test exercises the cohabitation; manual smoke check on DevTools Network panel verifies a single SSE connection. |
| Unsupported modes accidentally reach the persistence layer | `handleVisualizerPost` validates locally against `config.SupportedVisualizerModes()` and `TestHandleVisualizerPost_DoesNotInvokeSaverForUnsupportedMode` verifies the saver is not called for rejected values. `BridgeSaver.saveLocked` still performs full config validation as defense in depth. |
| Sec-Fetch-Site absent on a legitimate non-browser ops script | Documented in the PR: ops scripts must set `-H "Sec-Fetch-Site: same-origin"`. Trade-off accepted given the chassis's browser-only deployment model. |
| `radial_spectrum` slip-through while still deferred from v1 | `TestHandleVisualizerPost_Returns400OnRadialSpectrumDeferred` asserts the current deferred behavior. If a future spec intentionally adds `radial_spectrum` to `SupportedVisualizerModes()`, that spec updates the test and removes the preview-only UI state. |
| Diff-ticker latency feels sluggish on click | 250 ms is below the 500 ms feel-instant threshold. Hybrid `pressed`-class affordance fires within ~5 ms so perceived latency is the press flash, not the class change. Eager-push (synchronous emit on save) is available as a Spec 5 retrofit if user testing flags it. |
| Rapid sequential clicks faster than SSE roundtrip | Each POST is independent; `BridgeSaver.mu` serializes; last-write-wins. The diff ticker may collapse intermediate modes into a single SSE emit of the final mode — desirable behavior. `TestHandleVisualizerPost_RapidSequentialClicks` (race-detected) locks in this contract. |
| `vfd-live.js` amendment (CustomEvent + `window.Chassis.events.source` cache) accidentally breaks Spec 2's tests | The amendment is additive and post-EventSource-creation. Spec 2's test suite re-runs unchanged. A static asset check covers the new exposed surface; browser-level ordering is covered by integration/manual verification until the JS harness exists. |

### Cutover handoff

When Spec 8 lands the chassis settings drawer, the chassis save endpoint may become the canonical write path with `/ui/*` retired or downgraded to a read-only mirror. The final cutover spec decides.

## Design Decisions Worth Revisiting

### Hybrid feedback vs pessimistic vs optimistic

Decided in brainstorming. Hybrid wins on the JS-surface / failure-mode axis: zero new error-recovery code paths, no rollback bookkeeping, and the SSE event remains the single source of truth. Reviewer who prefers optimistic (snappier perceived latency) has a defensible position; the trade-off is the toast/error UI infrastructure that doesn't exist until Spec 8. Hybrid lands now without that dependency.

### `Sec-Fetch-Site` vs token-based CSRF

Decided in brainstorming. Sec-Fetch-Site is the modern same-origin enforcement primitive; zero state, zero cookies, zero template integration. The chassis deployment model (bundled first-party JS driving a LAN console) makes token CSRF heavyweight. This policy is intentionally stricter than `/ui/*`: no extension bypass, no `Origin` fallback, and no `Sec-Fetch-Site: none` acceptance for POST. Reviewer who prefers token CSRF or sharing `/ui/*` middleware has a defensible position; the trade-off is the broader compatibility surface that the chassis intentionally avoids.

### Diff-ticker emission vs synchronous broker

Stays on Spec 2's diff-ticker pattern. A synchronous emit-on-save (broker model) would shave ~125 ms off the worst-case latency but introduces a new package-level surface that Spec 5 may or may not want. Defer the decision to Spec 5 where telemetry rates may force the issue.

### Handler-local validation vs saver-only validation

The endpoint validates against `config.SupportedVisualizerModes()` before calling `VisualizerSaver`. This keeps invalid HTTP input at the HTTP boundary, avoids coupling `main.go` to `config.Validate()` error strings, and still lets `BridgeSaver.saveLocked` run full validation as defense in depth. A typed `config.ErrUnsupportedVisualizerMode` remains unnecessary for Spec 4 because no production caller needs to classify this error after handler-local validation.

## Open Questions for Subsequent Specs

- **Spec 3 (transport).** Will reuse `requireSameOrigin`. Open: do play/pause/stop/seek POSTs each emit a `transport` SSE event to confirm? Recommended yes. Open: does eventlog get a `source=chassis` field to distinguish chassis-triggered transport from `/ui` or adapter-triggered? Spec 9 decides.
- **Spec 5 (telemetry).** Diff-ticker latency vs broker. Resolves Spec 4's deferred decision.
- **Spec 8 (settings).** Whether the chassis save endpoint becomes the canonical write path and `/ui/*` retires its bridge-panel form.
- **Phase 5 polish.** Extraction of `requireSameOrigin` to a shared package once Spec 3 + Spec 4 establish the pattern; possible elevation of `window.Chassis.events.source` to a documented subscriber API.
