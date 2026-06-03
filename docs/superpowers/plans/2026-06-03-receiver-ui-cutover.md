# Receiver UI Cutover — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the chassis the canonical UI by re-prefixing it `/receiver` → `/ui`, moving the legacy UI `/ui` → `/old_ui`, and keeping the Companion JSON API at `/ui/companion/*` — one atomic change.

**Architecture:** A literal, test-guarded route rename across three surfaces on the one shared `http.ServeMux`. The chassis (`internal/chassis`) moves to `/ui`; the legacy UI (`internal/ui` + adapter-owned `/ui/adapter` fragments) moves to `/old_ui`; the seven Companion routes, their `OPTIONS /ui/companion/` preflight, and the root redirect `GET /{$}`→`/ui/` stay at `/ui`. No behavior changes beyond path prefixes.

**Tech Stack:** Go 1.26, `net/http` `ServeMux` (Go 1.22 method-aware patterns), `html/template`, htmx, vanilla JS, embedded static assets.

**Spec:** `docs/superpowers/specs/2026-06-03-receiver-ui-cutover-design.md`

---

## Rename rules (apply throughout; the per-task lists say where)

- **Chassis:** `/receiver` → `/ui` everywhere in `internal/chassis` and in the cmd tests that exercise chassis routes. (No `/receiver` lives outside `internal/chassis` + those cmd tests.)
- **Legacy UI:** `/ui` → `/old_ui` everywhere in `internal/ui` and adapter-owned UI fragments, **except** the carve-outs below.
- **Carve-outs that STAY at `/ui` (do NOT rename):**
  - The seven `/ui/companion/...` route registrations + `OPTIONS /ui/companion/` (`internal/ui/server.go:221-228`) and their handlers in `companion.go`.
  - The root redirect `GET /{$}` → `/ui/` (`internal/ui/server.go:245`, `handleRoot` body at `:344`).
- **Beware substrings:** never turn `/ui/companion` into `/old_ui/companion`; never touch the word fragments inside identifiers, comments unrelated to routes, or strings like `build`/`gui`. Edit route strings, not arbitrary `ui` occurrences. Both test suites assert concrete paths and are the backstop.

**All commands run from the worktree root** (a worktree is created at execution time per `superpowers:using-git-worktrees`).

---

## Task 1: Chassis re-prefix `/receiver` → `/ui`

**Files:**
- Modify (Go): `internal/chassis/server.go` (Mount patterns), `handler.go` (static path check + `StripPrefix`), `cast.go`, `audiodsp_routes.go`, `settings.go`, `setup.go`, `transport.go`, `events.go`, `history.go`, `doc.go`, and any other `internal/chassis/*.go` containing `/receiver`.
- Modify (templates): `internal/chassis/templates/shell.html` (stylesheet/script `src`s) and any partial containing `/receiver`.
- Modify (JS): `internal/chassis/static/*.js` — every absolute `/receiver/...` URL → `/ui/...` (settings-drawer.js, input-cast.js, catalog-browser.js, preset-bank.js, preset-reorder.js, chassis.js, transport.js, vfd-live.js, visualizer-bank.js, audio-strip.js, volume-knob.js, load-core.js, setup.js).
- Modify (CSS): `internal/chassis/static/chassis.css` (`/receiver/static/fonts/...`).
- Modify (tests): every `internal/chassis/*_test.go` and `internal/chassis/testdata/*.behavior.test.js` asserting `/receiver`.
- Modify (cmd tests asserting chassis routes): `cmd/mister-groovy-relay/adapter_linker_e2e_test.go`, `adapter_settings_e2e_test.go`, `streams_refresher_test.go`, `url_widgets_e2e_test.go`.

- [ ] **Step 1: Rename `/receiver` → `/ui` across `internal/chassis` production code**

Edit each Go/template/JS/CSS file above, replacing every `/receiver` path with `/ui`. Notable concrete spots:
- `server.go` `Mount`: `GET /receiver` → `GET /ui`, `GET /receiver/{$}` → `GET /ui/{$}`, `GET /receiver/static/` → `GET /ui/static/`, `GET /receiver/events` → `GET /ui/events`, and every `POST /receiver/...` → `POST /ui/...` (cast, transport/action, transport/seek, visualizer, volume, audio/dsp, audio/dsp/memory, aux/start, aux/stop, history/play, localfiles/browse, localfiles/cast, preset/{slot}/cast, streams/cast, preset/star, preset/move, settings/bridge, settings/action/*, settings/catalog/*, settings/adapter/*, setup/status, setup/finish).
- `handler.go`: `r.URL.Path == "/receiver/static/chassis.css"` → `"/ui/static/chassis.css"` and `http.StripPrefix("/receiver/static/", …)` → `"/ui/static/"`.
- JS: every `fetch('/receiver/…')` → `fetch('/ui/…')`.
- `chassis.css`: `/receiver/static/fonts/…` → `/ui/static/fonts/…`.

- [ ] **Step 2: Rename `/receiver` → `/ui` in the chassis test suites + cmd e2e tests**

Update every `/receiver` path string in `internal/chassis/*_test.go`, the behavior testdata, and the four cmd test files listed above (e.g. `httptest.NewRequest("GET", "/receiver/settings/adapter/jellyfin/link/status", …)` → `"/ui/settings/adapter/jellyfin/link/status"`).

- [ ] **Step 3: Verify no `/receiver` remains in `internal/chassis` or the cmd tests**

Run: `rg -n "/receiver" internal/chassis cmd/mister-groovy-relay`
Expected: no matches (every chassis route is now `/ui`). If any remain, fix them.

- [ ] **Step 4: Run chassis + cmd tests and JS syntax check**

Run: `go test ./internal/chassis/ ./cmd/... && for f in internal/chassis/static/*.js; do node --check "$f" || exit 1; done && node --test internal/chassis/testdata/setup.behavior.test.js`
Expected: PASS (chassis + cmd packages green; all JS parses; behavior test passes).

> The legacy UI still serves `/ui/*` at this point; that's fine — no test mounts both the chassis and legacy servers on one mux, so there is no duplicate-pattern collision yet. (The combined mount is exercised only by the new smoke test in Task 4.)

- [ ] **Step 5: Commit**

```bash
git add internal/chassis cmd/mister-groovy-relay
git commit -m "refactor(chassis): re-prefix /receiver -> /ui"
```

---

## Task 2: Legacy UI re-prefix `/ui` → `/old_ui` (with Companion + root carve-out)

**Files:**
- Modify: `internal/ui/server.go` (Mount), `internal/ui/setup.go`, `internal/ui/middleware.go` (`firstRunGuard`, `isWizardAdapterRoute`), `internal/ui/bridge.go`, `internal/ui/adapter.go`, `internal/ui/status.go`, `internal/ui/sidebar.go`, `internal/ui/diagnostics.go`, `internal/ui/companion.go` (only non-route comments, if any), `internal/ui/assets.go`, `internal/ui/bridge_fields.go`, and any other `internal/ui/*.go` with a `/ui` route string.
- Modify (templates): `internal/ui/templates/shell.html`, `now-playing-banner.html`, `adapter-panel.html`, `bridge-panel.html`, `diagnostics-panel.html`, `status-panel.html`, `status-content.html`, `probe-result.html`, `setup-step-bridge.html`, `setup-step-adapters.html`, `setup-step-adapter-config.html`, `setup-step-done.html`.
- Modify (static): `internal/ui/static/app.css`, `internal/ui/static/now-playing.js`.
- Modify (tests): every `internal/ui/*_test.go` asserting `/ui` (server_test, bridge_test, setup_test, setup_e2e_test, middleware_test, adapter_test, etc.).

- [ ] **Step 1: Rename the legacy `Mount` routes, preserving the carve-outs (`internal/ui/server.go`)**

Rename every `/ui/...` route in `Mount` to `/old_ui/...` **except** the companion block and the root redirect. Concretely, in `internal/ui/server.go:218-309`:

KEEP unchanged (stay at `/ui`):
```go
	mux.Handle("OPTIONS /ui/companion/", companionExtensionGate(http.NotFoundHandler()))
	s.mountCompanion(mux, http.MethodGet, "/ui/companion/status", s.handleCompanionStatus)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/play", s.handleCompanionPlay)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/control", s.handleCompanionControl)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/history/play", s.handleCompanionHistoryPlay)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/history/delete", s.handleCompanionHistoryDelete)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/launch", s.handleCompanionLaunch)
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/volume", s.handleCompanionVolume)
	s.mountGETUnguarded(mux, "/{$}", s.handleRoot) // root redirect → /ui/ stays
```

MOVE the two broad extension-CORS preflights to `/old_ui` (they belong to the legacy visual surface, and must not stay broad at `/ui` or they would catch the chassis's `/ui/*` preflights):
```go
	mux.HandleFunc("OPTIONS /old_ui", handleExtensionCORSPreflight)
	mux.HandleFunc("OPTIONS /old_ui/", handleExtensionCORSPreflight)
```

RENAME every remaining `/ui/...` pattern to `/old_ui/...`:
- `GET /ui/static/` → `GET /old_ui/static/` and the `StripPrefix("/ui/static/", …)` → `"/old_ui/static/"`.
- `GET /ui/{$}`, `GET /ui/`, `GET /ui` → `/old_ui/{$}`, `/old_ui/`, `/old_ui`.
- `/ui/status/content`, `/ui/playback/*`, `/ui/bridge`, `/ui/bridge/*`, `/ui/sidebar/dots`, `/ui/diagnostics`, `/ui/diagnostics/probe`, `/ui/adapter/{name}`, `/ui/adapter/{name}/status|toggle|save`, `/ui/setup`, `/ui/setup/{$}`, `/ui/setup/step/{name}`, `/ui/setup/done` → the `/old_ui/...` equivalents.
- The RouteProvider loop pattern: `fmt.Sprintf("/ui/adapter/%s/%s", a.Name(), route.Path)` → `fmt.Sprintf("/old_ui/adapter/%s/%s", a.Name(), route.Path)`.
- The `handleRoot` body still redirects to `/ui/` (unchanged) — that now lands on the chassis.

- [ ] **Step 2: Rename `/ui` in setup.go, middleware.go, and the other legacy handlers/templates/static**

- `internal/ui/setup.go`: every internal wizard redirect/link (`/ui/setup`, `/ui/setup/step/...`, `/ui/setup/done`) → `/old_ui/setup...`, and `handleSetupDone`'s final `http.Redirect(..., "/ui/", ...)` → `"/old_ui/"`. Rename the whole redirect set together.
- `internal/ui/middleware.go` `firstRunGuard`: pass-through checks `"/ui/setup"`/`"/ui/setup/"` → `"/old_ui/setup"`/`"/old_ui/setup/"`; `"/ui/static/"` → `"/old_ui/static/"`; redirect target `"/ui/setup"` → `"/old_ui/setup"`; `isWizardAdapterRoute` prefix `"/ui/adapter/"` → `"/old_ui/adapter/"`.
- Templates: every `hx-get`/`hx-post`/`href`/`action` of the form `/ui/...` → `/old_ui/...` (the now-playing banner's `/ui/playback/*`, adapter/bridge/diagnostics/status panels, setup steps, shell links).
- `internal/ui/static/now-playing.js` and `app.css`: `/ui/...` → `/old_ui/...`.
- Go handler strings in `bridge.go`, `adapter.go`, `status.go`, `sidebar.go`, `diagnostics.go`, `assets.go`, `bridge_fields.go` (and the comment in `bridge.go` that references the chassis route is handled in Task 4's doc sweep).

- [ ] **Step 3: Rename `/ui` → `/old_ui` in the legacy test suite (keep companion tests at `/ui`)**

Update every `/ui/...` path assertion in `internal/ui/*_test.go` to `/old_ui/...`, **except** tests that assert `/ui/companion/*` or the root redirect target `/ui/` — those stay. The `firstRunGuard` tests now assert redirects to `/old_ui/setup`; the setup e2e asserts wizard nav under `/old_ui/setup` and `POST /old_ui/setup/done` → `/old_ui/`.

- [ ] **Step 4: Verify the carve-out and run the legacy suite**

Run: `rg -n '"/ui/' internal/ui --glob '!*_test.go'`
Expected: only companion routes (`/ui/companion/...`) and the `handleRoot` redirect target `/ui/` remain. Anything else is a missed rename — fix it.

Run: `go test ./internal/ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui
git commit -m "refactor(ui): re-prefix legacy UI /ui -> /old_ui, keep /ui/companion + root redirect"
```

---

## Task 3: Adapter-owned legacy-UI fragments `/ui/adapter` → `/old_ui/adapter`

**Files:**
- Modify: `internal/adapters/url/ui.go` (and `routes.go` if it hardcodes the full `/ui/adapter` path), `internal/adapters/plex/link_ui.go`, `internal/adapters/jellyfin/ui.go`, `internal/adapters/torrent/ui.go`, `internal/adapters/streams/ui.go`.
- Modify (tests): the corresponding `*_test.go` in each adapter package that assert the fragment strings.

> Context: these adapters implement `ExtraPanelHTML`/`RouteProvider` and emit absolute `/ui/adapter/<name>/...` URLs (htmx attributes, form actions, polling URLs) that render inside the legacy adapter panel. The route *patterns* are mounted by `internal/ui` (renamed in Task 2 to `/old_ui/adapter/...`), so these fragments must match or the `/old_ui` adapter panels break. The chassis settings drawer uses separate `/ui/settings/adapter/...` routes (renamed in Task 1) — do **not** touch those.

- [ ] **Step 1: Rename the fragment URLs**

In each `ui.go`/`link_ui.go`, replace every `/ui/adapter/<name>/...` string with `/old_ui/adapter/<name>/...`. Concrete known spots: `url/ui.go` (`hx-get="/ui/adapter/url/panel"`, `hx-post="/ui/adapter/url/history/delete"`, `hx-post="/ui/adapter/url/cookies"`), `plex/link_ui.go` (`/ui/adapter/plex/link/start`, `/link/status`, `/unlink`), `torrent/ui.go` (`/ui/adapter/torrent/live`), and the `ExtraPanelHTML` output in `jellyfin/ui.go` and `streams/ui.go`.

- [ ] **Step 2: Update the adapter package tests**

Update each adapter `*_test.go` that asserts a `/ui/adapter/...` fragment string to `/old_ui/adapter/...`.

- [ ] **Step 3: Verify no legacy `/ui/adapter` fragments remain**

Run: `rg -n "/ui/adapter" internal/adapters`
Expected: no matches.

- [ ] **Step 4: Run the adapter suites**

Run: `go test ./internal/adapters/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters
git commit -m "refactor(adapters): move legacy UI fragments /ui/adapter -> /old_ui/adapter"
```

---

## Task 4: Dual-mount smoke test, string audit, docs, full verification

**Files:**
- Create: `cmd/mister-groovy-relay/cutover_mount_test.go`
- Modify: `internal/ui/bridge.go` (stale comment), `README.md`.

- [ ] **Step 1: Write the dual-mount smoke test (fails first if any rename is incomplete)**

Create `cmd/mister-groovy-relay/cutover_mount_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	uipkg "github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

// TestCutover_DualMount asserts the chassis (/ui), legacy UI (/old_ui), and
// Companion API (/ui/companion) coexist on one mux without a duplicate-pattern
// panic, and that the swapped routes resolve to the right surface.
func TestCutover_DualMount(t *testing.T) {
	reg := adapters.NewRegistry()

	ui, err := uipkg.New(uipkg.Config{Registry: reg})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	ch, err := chassis.New(chassis.Config{Version: "test", StartedAt: time.Now(), Registry: reg})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	mux := http.NewServeMux()
	// Must not panic on duplicate patterns — mirrors main.go mount order.
	ui.Mount(mux)
	ch.Mount(mux)

	check := func(method, path string, wantNotFound bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		got404 := rec.Code == http.StatusNotFound
		if got404 != wantNotFound {
			t.Errorf("%s %s: code=%d wantNotFound=%v", method, path, rec.Code, wantNotFound)
		}
	}

	check("GET", "/ui", false)                     // chassis index
	check("GET", "/ui/", false)                    // chassis index
	check("GET", "/old_ui/", false)                // legacy shell
	check("GET", "/old_ui/setup", false)           // legacy wizard
	check("GET", "/ui/companion/status", false)    // companion API stays at /ui
	check("GET", "/receiver", true)                // retired
	check("GET", "/receiver/static/chassis.css", true) // retired
}
```

- [ ] **Step 2: Run the smoke test**

Run: `go test ./cmd/... -run TestCutover_DualMount -count=1`
Expected: PASS. A panic here means a stray un-renamed pattern collides (e.g. legacy still at `/ui/{$}`); a wrong 404/non-404 means a surface didn't move. Fix the offending rename, then re-run.

> If `chassis.New`/`ui.New` need a field this minimal config omits, mirror the construction used by existing tests (`internal/ui/adapter_test.go` builds `uipkg.New(Config{Registry: reg})`; `internal/chassis` tests build `New(Config{Version, StartedAt, Registry})`). Keep config minimal — `Mount` only needs to register patterns.

- [ ] **Step 3: Full-tree string audit**

Run:
```bash
rg -n '/receiver|/ui/adapter|/ui/setup|/ui/playback|/ui/static|/ui/bridge|/ui/diagnostics|/ui/sidebar' internal cmd README.md
```
Review each hit. **Expected survivors only:** canonical chassis `/ui/...` references; the Companion carve-out `/ui/companion/...`; `internal/ui`'s `handleRoot` redirect to `/ui/`; and the cutover smoke test's intentional `/receiver` 404 assertions in `cmd/mister-groovy-relay/cutover_mount_test.go`. Any *other* live `/receiver` or legacy `/ui/<visual>` route string is a miss — fix it. (Do not sweep `docs/superpowers/**` — those design/plan files are history.)

- [ ] **Step 4: Fix the stale comment + README**

- `internal/ui/bridge.go`: the comment referencing the chassis `/receiver/audio/dsp` route → `/ui/audio/dsp`.
- `README.md`: replace the "Preview UI at `/receiver` … work in progress" note (around the preview callout) with the factual state — the chassis is the UI at `/ui`, the legacy UI is at `/old_ui`; update the font-license path `/receiver/static/fonts/LICENSE` → `/ui/static/fonts/LICENSE`, and any `/receiver` example URLs (e.g. the `curl` visualizer example) → `/ui`.

- [ ] **Step 5: Full verification**

Run: `go vet ./... && go test ./...`
Expected: all packages `ok`. (`go test -race` is CI-only here — no local cgo. If `internal/adapters/jellyfin` flakes in the full run, re-run it alone with `-count=1`; it is a known Windows full-suite flake unrelated to this change.)

- [ ] **Step 6: Commit**

```bash
git add cmd/mister-groovy-relay/cutover_mount_test.go internal/ui/bridge.go README.md
git commit -m "test(cutover): dual-mount smoke test; docs: fix stale /receiver references"
```

---

## Notes for the executor

- **Atomic branch:** all four tasks land on one branch/PR. There is no shippable half-state — the cutover is only correct once Tasks 1–4 are all in. Per-task commits are fine; do not merge a partial branch to `main`.
- **Edit route strings, not arbitrary `ui`/`receiver` text.** Use reviewed per-file edits; the `rg` audits in Steps 3 and the two test suites catch misses. Avoid blind global `sed` (it would corrupt `/ui/companion`, identifiers, and comments).
- **The two carve-outs are load-bearing:** `/ui/companion/*` (+ its `OPTIONS /ui/companion/` preflight) and `GET /{$}`→`/ui/` must remain at `/ui`. The broad `OPTIONS /ui`/`OPTIONS /ui/` preflights move to `/old_ui`. A dedicated assertion (`GET /ui/companion/status` not-404) is in the Task 4 smoke test.
- **Do not touch** the Plex Companion cast-target routes (`/resources`, `/player/*`), the chassis settings-drawer adapter routes (now `/ui/settings/adapter/...`), or anything under `docs/superpowers/`.
