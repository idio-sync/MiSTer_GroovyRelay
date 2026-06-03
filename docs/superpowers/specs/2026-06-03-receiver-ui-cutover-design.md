# Receiver UI Cutover — `/ui` Becomes the Chassis — Design

**Status:** Brainstormed; awaiting implementation plan.
**Scope:** Make the receiver chassis UI the canonical UI by re-prefixing it from
`/receiver/*` to `/ui/*`, moving the legacy UI to a still-functional `/old_ui/*`, and
keeping the Companion JSON API at `/ui/companion/*`. This is the foundation design's
"Final cutover," restricted to the route swap only.
**Repo location:** Committed under `docs/superpowers/specs/`, normally gitignored
(`.gitignore` line 35); force-added (`git add -f`) per the receiver-chassis rollout
convention.

## Background

The receiver chassis UI (`internal/chassis`, served at `/receiver/*`) is feature-complete
— live playback, telemetry, casting, presets, history, full settings drawer, and (as of
2026-06-02) first-run setup mode. The legacy UI (`internal/ui`, served at `/ui/*`) is the
current primary surface: `/` redirects to `/ui/`, and a guarded first-run wizard lives at
`/ui/setup`.

Both UIs are mounted on one shared `http.ServeMux` on `bridge.ui.http_port`
(`cmd/mister-groovy-relay/main.go`): `uiSrv.Mount(mux)` then `chassisSrv.Mount(mux)`. The
Plex Companion cast-target routes (`/resources`, `/player/*`) are mounted separately by
the Plex adapter and are out of scope here.

A third surface currently lives under `/ui/`: the **Companion JSON API**
(`/ui/companion/status|play|control|history/play|history/delete|launch|volume`, mounted by
`internal/ui/server.go`). This is the backend for the browser-extension mini-remote — an
API, not part of the legacy visual UI — and it is consumed by installed extensions that
hardcode `/ui/companion/*`.

Both UIs hardcode their path prefix pervasively. The chassis has ~250 `/receiver`
references across Go, templates, ~12 JS files (≈40 distinct absolute fetch URLs), CSS, and
tests. The legacy UI has a comparable number of `/ui` references including htmx
`hx-get`/`hx-post` route attributes throughout its templates. There is no way to retire
`/receiver` without rewriting the chassis's absolute client URLs, and no way to move the
legacy UI to a working `/old_ui` without rewriting its absolute client URLs — a
prefix-stripping proxy cannot help because both UIs emit absolute self-referential links.

## Decisions (settled in brainstorming)

1. **True re-prefix** the chassis `/receiver` → `/ui`. `/receiver` ceases to exist; the
   browser URL shows `/ui`; the chassis is the literal canonical UI. (Not a redirect.)
2. **Companion API stays at `/ui/companion/*`** — carved out of the legacy-UI move so
   installed browser extensions keep working with zero changes.
3. **Functional `/old_ui`** — the legacy UI's visual/config surface is re-prefixed
   `/ui` → `/old_ui` and remains a working fallback (not deleted, not a stub).

## Goals

1. Visiting `/` or `/ui` serves the chassis. `/receiver/*` returns 404 (gone).
2. The legacy UI is fully functional at `/old_ui/*` (settings, adapters, setup wizard,
   diagnostics, now-playing banner — all working).
3. The Companion API remains reachable at `/ui/companion/*` with no extension changes.
4. First-run behaves correctly: a fresh install lands on the chassis setup mode at `/ui`;
   the legacy wizard at `/old_ui/setup` still works; both share the one sentinel.
5. No behavior changes beyond path prefixes. No visual changes.
6. `go vet ./...`, `go test ./...`, `go test -race` (CI), and integration tests stay green.

## Non-goals

- The Phase-5 event log / observability surface.
- Mobile/phone-class polish or the accessibility audit.
- Any visual or functional change to either UI beyond the path prefix.
- Deleting the legacy UI (it stays at `/old_ui`; removal is a future, separate decision).
- Re-homing the Plex Companion cast-target routes (`/resources`, `/player/*`) — untouched.
- Parameterizing the prefix into a constant/relative-path scheme — explicitly rejected in
  favour of a literal re-prefix (brainstorming Q1).

## End-state route map

One shared mux. After the change:

| Path(s) | Served by | Was |
| --- | --- | --- |
| `/` (`GET /{$}`) → redirect to `/ui/` | `internal/ui` `handleRoot` (unchanged) | same |
| `/ui`, `/ui/{$}`, `/ui/static/`, `/ui/events`, `/ui/cast`, `/ui/transport/*`, `/ui/visualizer`, `/ui/volume`, `/ui/audio/dsp*`, `/ui/aux/*`, `/ui/history/play`, `/ui/localfiles/*`, `/ui/preset/*`, `/ui/streams/cast`, `/ui/settings/*`, `/ui/setup/status`, `/ui/setup/finish` | **chassis** (`internal/chassis`) | `/receiver/*` |
| `/ui/companion/status\|play\|control\|history/play\|history/delete\|launch\|volume` (+ their CORS/preflight/gate) | **legacy UI companion handlers** (`internal/ui`) — unchanged | `/ui/companion/*` |
| `/old_ui`, `/old_ui/{$}`, `/old_ui/static/`, `/old_ui/setup/*`, `/old_ui/adapter/*`, `/old_ui/bridge/*`, `/old_ui/playback/*`, `/old_ui/diagnostics/*`, `/old_ui/sidebar/*`, `/old_ui/status/*` | **legacy UI** (`internal/ui`) | `/ui/*` |
| `/resources`, `/player/*` | Plex adapter (unchanged) | same |

`/receiver/*` is fully retired. The root redirect already targets `/ui/`, which now lands
on the chassis, so `handleRoot` needs no change.

## Architecture

### Part A — Chassis re-prefix (`/receiver` → `/ui`)

A literal rename across `internal/chassis`, no behavior change:

- **Go:** every route pattern in `server.go` `Mount` (`GET /receiver` → `GET /ui`, etc.);
  the static handler's literal path check and `http.StripPrefix("/receiver/static/", …)`
  in `handler.go`; any `/receiver` string in `cast.go`, `audiodsp_routes.go`, `settings.go`,
  `transport.go`, `events.go`, `doc.go`, etc.
- **Templates:** `shell.html` stylesheet/script `src`s (`/receiver/static/…` →
  `/ui/static/…`) and any partial referencing the prefix; the `setup-banner` include is
  data-driven and unaffected.
- **JS (≈40 absolute URLs across ~12 files):** every `fetch('/receiver/…')` →
  `fetch('/ui/…')` in `settings-drawer.js` (21), `input-cast.js`, `catalog-browser.js`,
  `preset-bank.js`, `preset-reorder.js`, `chassis.js`, `transport.js`, `vfd-live.js`,
  `visualizer-bank.js`, `audio-strip.js`, `volume-knob.js`, `load-core.js`, `setup.js`.
- **CSS:** the `/receiver/static/fonts/…` URL(s) in `chassis.css`.
- **Tests:** every `/receiver` path string across the `_test.go` files (chassis_test.go
  ~82, settings_test.go ~55, etc.) and the JS behavior testdata. The tests assert concrete
  paths, so they are the completeness proof: a missed reference fails.

The asset-version hash, same-origin wrapper, SSE stream, setup-mode gate, and CSS-scope
discipline all keep working unchanged — only the literal prefix differs.

### Part B — Legacy UI re-prefix (`/ui` → `/old_ui`) with Companion carve-out

A literal rename across `internal/ui` for everything **except** the Companion API:

- **Moved to `/old_ui`:** `Mount` patterns for the shell, static (`/ui/static/` →
  `/old_ui/static/`), setup wizard (`/ui/setup`, `/ui/setup/step/*`, `/ui/setup/done` →
  `/old_ui/setup*`), adapter routes (`/ui/adapter/*`), bridge (`/ui/bridge/*`), playback
  (`/ui/playback/*`), diagnostics (`/ui/diagnostics/*`), sidebar, status; the matching
  htmx `hx-get`/`hx-post` attributes in templates; `shell.html` asset `src`s; the
  now-playing banner; and `firstRunGuard`'s pass-through prefixes and redirect target
  (`internal/ui/middleware.go`: `/ui/setup` → `/old_ui/setup`, `/ui/static/` →
  `/old_ui/static/`, the `isWizardAdapterRoute` prefix `/ui/adapter/` → `/old_ui/adapter/`).
- **Stays at `/ui` (carve-out):**
  - The seven `/ui/companion/*` route registrations and their handlers.
  - The Companion CORS/preflight/gate that currently guards them (`OPTIONS /ui`,
    `OPTIONS /ui/`, `OPTIONS /ui/companion/`, `companionExtensionGate`,
    `extensionCORSMiddleware`/`handleExtensionCORSPreflight`). **Planning task:** determine
    precisely which of these preflight/middleware registrations exist to serve the
    companion endpoints and keep exactly those at `/ui`; the legacy visual pages at
    `/old_ui` no longer need extension-CORS since the extension never calls them.
  - The root redirect `GET /{$}` → `/ui/` (unchanged; now lands on the chassis).

Go's `ServeMux` allows the carve-out: `/ui/companion/*` paths are disjoint from every
chassis `/ui/*` pattern (the chassis registers specific patterns and a `/ui/static/`
subtree, none overlapping `/ui/companion/`), and the chassis registers no bare `/ui/`
catch-all. More-specific patterns win, so companion endpoints resolve to the legacy
handlers.

### Part C — Wiring, invariants, atomicity

- `main.go` continues to call `uiSrv.Mount(mux)` then `chassisSrv.Mount(mux)`. After the
  rename the two register **disjoint** patterns, so there is no duplicate-pattern panic.
  The single-listener / single-port invariant (CLAUDE.md "one HTTP listener") is preserved.
- **First-run interplay:** chassis setup mode owns first-run at `/ui` via the shared
  `*uiserver.BridgeSaver` sentinel. The legacy `firstRunGuard` + wizard move to `/old_ui`
  and read the same sentinel, so the two stay consistent; `/` → `/ui/` gives a fresh
  install the chassis setup mode.
- **Atomicity:** this lands as **one cohesive change** (single branch/PR). The chassis
  cannot move onto `/ui` before the legacy UI vacates `/ui`, or `ServeMux` panics on
  duplicate patterns (`/ui/{$}`, `/ui/static/`). Internally the work is staged (Part A,
  then Part B, then docs), but there is no shippable half-state on `main`.

## Done When

- `GET /` → 302 `/ui/`; `GET /ui` and `GET /ui/{$}` render the chassis.
- `GET /receiver` and any `/receiver/*` return 404.
- The legacy UI is fully functional under `/old_ui/*`, including the setup wizard and
  adapter/bridge saves.
- `GET /ui/companion/status` and the other six companion routes resolve to the legacy
  companion handlers (extension unaffected); their CORS preflight still works.
- First-run: fresh install at `/` → `/ui/` chassis setup mode; `/old_ui` → `/old_ui/setup`;
  both dismiss/read the one sentinel.
- `go vet ./...` and `go test ./...` green (both `internal/chassis` and `internal/ui`
  suites updated to the new paths); `go test -race` and integration green in CI.
- README's stale "Preview UI at /receiver" note and the `/ui`-vs-`/receiver` framing are
  factually corrected.

## Testing

- **Chassis suite:** re-prefixed path assertions pass; add/confirm a test that
  `/receiver/*` is unrouted (404) and that `/ui/cast` (etc.) is gated/served as before.
- **Legacy UI suite:** re-prefixed path assertions pass under `/old_ui`; the `firstRunGuard`
  tests assert redirects to `/old_ui/setup`; a test confirms `/ui/companion/*` still
  resolves while the visual routes do not exist at `/ui`.
- **Cross-cutting:** a wiring test (or `main`-level smoke) that all three surfaces mount
  without a duplicate-pattern panic and that `/ui/companion/play` + `/ui/cast` +
  `/old_ui/setup` all route to the right handlers.
- **Manual smoke:** load `/`, `/ui`, `/old_ui`; exercise a chassis cast and a settings
  save at `/ui`; confirm an extension call to `/ui/companion/status` succeeds; confirm
  `/receiver` 404s.
- `go test -race` is CI-only here (no local cgo); run vet + unit + integration locally.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Blind find-replace catches `/ui` substrings (inside `/ui/companion`, comments, words like "build", or the string "gui") | Per-file reviewed diffs; companion paths explicitly excluded from the legacy rename; both test suites are the backstop (concrete path assertions fail on any mismatch). |
| Companion CORS/preflight accidentally moved with the legacy UI → extension breaks | Treat the companion endpoints + their preflight/gate as one carve-out unit kept at `/ui`; dedicated test that `OPTIONS`/`GET /ui/companion/*` still works. |
| Duplicate-pattern panic from a stray un-renamed legacy `/ui/*` registration colliding with a chassis pattern | The single-PR atomic change + a mount smoke test surface the panic immediately; the legacy rename must be complete (no `/ui/*` left except companion). |
| Half-shipped intermediate state on `main` | Land as one branch/PR; do not merge partial. |
| Stale docs/specs referencing `/receiver` | README updated in this change; deeper spec/plan reconciliation is explicit follow-up (non-goal here). |

## Decomposition (for the implementation plan)

One spec, one plan, internally staged and landing atomically:

1. **Chassis re-prefix** `/receiver` → `/ui` (Go + templates + JS + CSS + chassis tests).
2. **Legacy UI re-prefix** `/ui` → `/old_ui` with the Companion + root-redirect carve-out
   (Go + templates + htmx attrs + ui tests + `firstRunGuard`).
3. **Wiring + verification + docs:** confirm disjoint mounts, mount/route smoke test,
   README/factual-doc fix, full `go vet`/`go test`.

## Design decisions worth revisiting

### Literal re-prefix vs. parameterized base path
Chosen: literal (brainstorming Q1). It hardcodes `/ui` exactly as `/receiver` was hardcoded
— large but mechanical, fully test-guarded, and it truly retires `/receiver`. A
parameterized base path would prevent future hardcoding but invests effort the user
explicitly declined for this change. Revisit only if a third move is ever contemplated.

### Keeping a functional `/old_ui` vs. deleting the legacy visual UI
Chosen: keep it functional (brainstorming Q3) as a transition fallback, despite the second
re-prefix cost. `internal/ui` stays in the binary regardless (it serves `/ui/companion`),
so the marginal cost is the visual-surface rename, not the whole package. Deleting the
legacy pages remains an easy future follow-up once confidence in the chassis is high.
