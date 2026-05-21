# Receiver Chassis Foundation — Phase 0 Design

**Status:** Brainstormed; awaiting implementation plan.
**Scope:** First sub-project of the receiver chassis UI rollout. Stands up the `/receiver` route, the chassis chrome, design tokens, fonts, base templates, CSS architecture, and a minimal JS runtime so subsequent specs can wire live behaviour into a pre-laid-out idle chassis.
**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored (`.gitignore` line 35); this spec was force-added intentionally so the rollout's foundational design is reviewable from the repo. Subsequent spec docs in this rollout should follow the same `git add -f` convention until the gitignore is revisited as a process decision.

## Background

The brainstorm mockup at `.superpowers/brainstorm/1973-1779237107/receiver-v24.html` (~6,100 lines) prototypes a complete UI redesign styled as a physical 1980s/90s AV receiver chassis: VFD, meter screen with audio scopes and telemetry, transport row, visualizer bank, source selector, input row, preset bank, history row, settings drawer, and per-adapter forms. The decision has been made to replace the current `/ui/*` surface with the chassis aesthetic.

Because the mockup spans roughly ten distinct feature areas, it cannot be specced as a single implementation. The rollout is decomposed into nine sub-specs across six phases:

- **Phase 0 — Foundation** *(this spec).*
- **Phase 1 — Live console.** VFD live + idle display; transport controls; visualizer mode selector.
- **Phase 2 — Telemetry meters.** Meter screen with all 3 rows; telemetry transport.
- **Phase 3 — Cast initiation.** Source cluster + input/cast surface; catalog browser + preset bank.
- **Phase 4 — Settings & adapters.** Bridge settings + adapter forms ported to chassis style.
- **Phase 5 — History & observability.** History row; event log.
- **Final cutover.** Route swap `/ui/*` → `/receiver/*`; retire old templates; mobile/responsive polish; accessibility pass.

The chassis is built in parallel at `/receiver/*`. The existing `/ui/*` routes stay untouched until the final cutover. This isolates risk, keeps production usable through every spec, and lets each phase land independently.

## Goals

1. Mount a new `/receiver/*` route tree alongside `/ui/*`. No impact on existing UI surfaces.
2. Render the full chassis chrome at `GET /receiver`: status bar, masthead, screws, VFD frame, meter frame, transport row frame, visualizer bank frame, input row, preset bank frame, history row frame, settings drawer (closed). Every section visible but in idle state with placeholder content.
3. Establish the production patterns later specs build on: file layout, Go data struct shape, template-partial composition, CSS architecture, idle/live class machinery, asset embedding via `go:embed`.
4. Self-host all chassis fonts (DSEG7-Classic + Modern, DSEG14-Classic + Modern, Inter) under `internal/chassis/static/fonts/` with attribution recorded alongside the woff2 files. No external font CDNs.
5. Ship a minimal JS runtime (~150 lines vanilla) that handles initial state-class application, dev-only state toggle hook, and provides the `window.Chassis` namespace later specs hang feature scripts off.

## Non-goals

- Any live data. No telemetry, no actual session state, no playback control, no preset/history persistence. Idle is the only state Phase 0 renders.
- Any backend POST routes for chassis interactions. Buttons render but click handlers are stubbed.
- Spectrum, goniometer, throughput, ACK canvas animation logic. Canvases exist as placeholders; no draw loops run. Spec 5 owns these.
- Final cutover of `/ui/*` → `/receiver/*`. Separate spec at the end of Phase 5.
- Migration of existing settings/bridge forms into chassis style — Spec 8.
- Mobile polish beyond the container-query breakpoints the mockup already defines. Phone-class (<480px) refinement is Phase 5 work.
- Accessibility audit beyond basic semantic HTML and visible focus states. Comprehensive a11y is Phase 5 polish.

## Done When

- `curl http://localhost:32500/receiver` returns HTML that renders the mockup's idle-state visual at every container-query breakpoint, in modern Chrome/Edge/Safari/Firefox.
- No regressions on `/ui/*`. Existing handler and template tests pass unchanged.
- All chassis CSS, fonts, and JS embedded via `go:embed`. Single-binary distribution unchanged.
- A new `internal/chassis/` package exists with `templates/` and `static/` subdirectories holding the foundation files later specs extend.
- The Inter font family is self-hosted alongside DSEG. No `<link>` to fonts.googleapis.com or any external host.

## Architecture

### File & directory layout

The chassis package owns its own assets so Go `embed` directives are local. Existing `internal/ui/` templates/static assets and `internal/uiserver/` saver plumbing are untouched.

```
internal/chassis/                 # NEW package, no imports from ui or uiserver
├── server.go
├── data.go
├── handler.go
├── templates.go
├── chassis_test.go
├── doc.go
├── templates/                    # parsed via go:embed templates/*.html
│   ├── shell.html
│   ├── status-bar.html
│   ├── masthead.html
│   ├── vfd-source-row.html       # composes vfd + source-cluster on the same row
│   ├── vfd.html
│   ├── source-cluster.html
│   ├── meter.html
│   ├── transport.html
│   ├── visualizer-bank.html
│   ├── input-row.html
│   ├── preset-bank.html
│   ├── history.html
│   └── settings-drawer.html
└── static/                       # served at /receiver/static/ via go:embed static
    ├── chassis.css               # single stylesheet, ~3,200 lines
    ├── chassis.js                # ~150-line runtime
    └── fonts/
        ├── DSEG7Classic-Regular.woff2
        ├── DSEG7Classic-Bold.woff2
        ├── DSEG7Modern-Regular.woff2
        ├── DSEG7Modern-Bold.woff2
        ├── DSEG14Classic-Regular.woff2
        ├── DSEG14Classic-Bold.woff2
        ├── DSEG14Modern-Regular.woff2
        ├── DSEG14Modern-Bold.woff2
        ├── Inter-Variable.woff2  # variable font, full weight axis, Latin subset
        ├── LICENSE               # DSEG + Inter attribution
        └── SOURCES.md            # upstream versions, URLs, and checksums

internal/uiserver/                # UNCHANGED — save/config plumbing only
internal/ui/templates/            # UNCHANGED — existing /ui templates stay here
internal/ui/static/               # UNCHANGED if it exists; not touched by Phase 0
```

Co-locating templates and static under the package lets `go:embed` reference them with package-local paths. Cross-package embed is not supported by the Go toolchain.

**Integration tests that mount both packages** (`TestMount_DoesNotShadowUIRoutes` and `TestReceiverEndToEnd`) live under `tests/integration/` with the existing `//go:build integration` tag. They are not part of the `internal/chassis/` package because they need to import both `internal/chassis` and `internal/ui`, which would violate the production cross-import rule if done from inside either package. See [Testing → Layer 3](#layer-3--integration-coverage) for the test list.

### Go package — `internal/chassis/`

New package, no imports of `internal/ui` or `internal/uiserver`. Mirrors the HTTP/template/static shape of `internal/ui`, while leaving `internal/uiserver` as save/config plumbing:

- `server.go` — `Config` and `Server` structs, `New(Config) (*Server, error)`, `Mount(mux *http.ServeMux)` method.
- `data.go` — Go structs templates render against (`ReceiverPageData` + sub-structs) and the `idleSnapshot()` helper.
- `handler.go` — HTTP handler funcs. Phase 0 ships only `handleIndex` for `GET /receiver` plus the static asset handler.
- `templates.go` — parses embedded templates at startup, registers helper FuncMap.
- `chassis_test.go` — unit + handler tests.
- `doc.go` — package-level Go doc explaining the parallel-replacement strategy and the relationship to `internal/ui`.

### Asset embedding

Two new `embed.FS` declarations in `internal/chassis/templates.go`:

```go
//go:embed templates/*.html
var chassisTemplatesFS embed.FS

//go:embed static
var chassisStaticFS embed.FS
```

The bare `static` form (not `static/**`) matches the existing `internal/ui/assets.go` pattern and recursively includes the `fonts/` subtree. Existing `embed.FS` in `internal/ui/` unchanged.

### Server config

Mirror `internal/ui.New(ui.Config{...})` rather than introducing a positional constructor. `cmd/mister-groovy-relay/main.go` already has the resolved values this route needs:

```go
type Config struct {
    Bridge    config.BridgeConfig
    Manager   *core.Manager
    Registry  *adapters.Registry
    Version   string
    StartedAt time.Time
    HostIP    string // resolved/autodetected by main.go when available; may be empty on offline hosts
}
```

`Version` and `StartedAt` are required even in Phase 0: asset URLs use the build version and idle VFD uptime uses `StartedAt`. `HostIP` is display-only and must be allowed to remain empty because `main.go` already keeps the bridge running when `outboundIP()` cannot resolve a route on offline hosts. `New` validates required fields and returns an error for zero `StartedAt`; tests should pass a fixed time for deterministic uptime assertions.

### Route mounting

`cmd/mister-groovy-relay/main.go` adds:

```go
chassisSrv, err := chassis.New(chassis.Config{
    Bridge:    sec.Bridge,
    Manager:   coreMgr,
    Registry:  reg,
    Version:   version,
    StartedAt: startedAt,
    HostIP:    hostIP,
})
if err != nil {
    dieFriendly("chassis init", err) // reuses the existing helper in cmd/mister-groovy-relay/banner.go
}
chassisSrv.Mount(mux)
```

`Mount` attaches:

- `GET /receiver` and `GET /receiver/{$}` → both render `shell.html` with idle placeholder data. Go 1.22's method-aware mux treats these as distinct patterns; mirror the existing `internal/ui/server.go` `/ui` + `/ui/{$}` registration to avoid a 301 redirect dance.
- `GET /receiver/static/` → serves embedded CSS/JS/fonts via `http.StripPrefix("/receiver/static/", http.FileServer(http.FS(...)))`, content-type lookup by extension, and `Cache-Control: public, max-age=31536000, immutable`. Do not use `GET /receiver/static/*`; Go `ServeMux` treats `*` as a literal path segment, not a subtree wildcard. `GET /receiver/static/{path...}` is also valid if the implementation prefers explicit wildcards.

  **MIME-type discipline:** `http.FileServer` falls back to `mime.TypeByExtension`, which on some Windows hosts and minimal Linux containers (Alpine, scratch images) returns `""` for `.woff2`, yielding `application/octet-stream` and tripping strict CSP rules. Register the type explicitly at package init:

  ```go
  func init() {
      mime.AddExtensionType(".woff2", "font/woff2")
      mime.AddExtensionType(".woff",  "font/woff")
  }
  ```

  Add a Layer 1 test (`TestStaticAssets_Fonts_HaveCorrectMIME`) that asserts the served `Content-Type` for `Inter-Variable.woff2` is `font/woff2`.

Mount order in `main.go`: existing `ui.Server` first, then `chassis.Server`. Path prefixes are disjoint so collision is structurally impossible, but the order is documented and tested.

## Data Model & Template Composition

### Page-level data struct

```go
type ReceiverPageData struct {
    Version    string
    BrandName  string
    HostInfo   HostInfo
    State      ReceiverState   // "idle" | "live" — body class controller
    VFD        VFDData
    Source     SourceData
    Meter      MeterData
    Transport  TransportData
    Visualizer VisualizerData
    Input      InputData
    Presets    PresetsData
    History    HistoryData
    Settings   SettingsData
}
```

Each sub-struct holds the smallest set of fields its partial needs. Live-state fields exist in the struct definition but are zero/empty in Phase 0. Subsequent specs populate them — no struct churn between phases.

Example sub-structs:

```go
type VFDData struct {
    State        string  // "idle" | "live"
    Title        string  // "STANDBY" in idle
    Marquee      string  // idle hint text
    QueueCurrent int
    QueueTotal   int
    SystemTime   string  // "HH:MM" formatted server-side; ticked client-side
    Uptime       string  // "Nh Nm" formatted
}

type MeterData struct {
    State       string
    SourceStrip SourceStripIdleData
    MidRow      MidRowIdleData
    Readout     ReadoutIdleData
}
```

### `idleSnapshot()` helper

Single source of truth for Phase 0's default page content:

```go
func idleSnapshot(cfg Config, now time.Time) ReceiverPageData
```

Returns a fully populated `ReceiverPageData` with `State = "idle"` and placeholder strings matching the mockup's idle state. It reads `cfg.Bridge`, `cfg.Version`, `cfg.HostIP`, and `cfg.StartedAt` so the status bar and uptime are deterministic under test; an empty `cfg.HostIP` renders as the literal display string `"OFFLINE"` (see Appendix A) rather than failing server startup. `handleIndex` calls it directly. Spec 2 (VFD live) replaces this with `snapshotFromSession()` that reads real session state, falling back to `idleSnapshot()` when no session is active.

### Template composition

`shell.html` is the only template the handler renders by name. It is top-level document markup, matching the existing `internal/ui/templates/shell.html` convention, and composes partials via `{{template …}}`:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.BrandName}}</title>
  <link rel="stylesheet" href="/receiver/static/chassis.css?v={{.Version}}">
  <script defer src="/receiver/static/chassis.js?v={{.Version}}"></script>
</head>
<body class="receiver {{.State}}">
  <div class="receiver">
    {{template "status-bar" .HostInfo}}
    {{template "masthead" .BrandName}}
    <div class="receiver-inner">
      {{template "vfd-source-row" .}}
      {{template "meter" .Meter}}
      {{template "transport" .Transport}}
      {{template "visualizer-bank" .Visualizer}}
      {{template "input-row" .Input}}
      {{template "preset-bank" .Presets}}
      {{template "history" .History}}
    </div>
    {{template "settings-drawer" .Settings}}
  </div>
</body>
</html>
```

Each partial is parsed from its own `.html` file via `template.ParseFS(chassisTemplatesFS, "templates/*.html")` at server startup. Standard Go html/template pattern. The handler executes `"shell.html"` directly; partial files contain `{{define "partial-name"}}` blocks. The `vfd-source-row` partial receives `.` (the full page data) because it composes VFD + source-cluster; every other partial receives its narrowly-scoped sub-struct.

**`vfd-source-row.html` structure:** the file contains exactly one `{{define "vfd-source-row"}}` block which wraps a layout container and invokes the two component partials with their narrow sub-structs:

```html
{{define "vfd-source-row"}}
<div class="vfd-source-row">
  {{template "vfd" .VFD}}
  {{template "source-cluster" .Source}}
</div>
{{end}}
```

This keeps the VFD and source-cluster partials independently testable and scope-isolated to their sub-structs; `vfd-source-row` exists solely to compose them onto a single chassis row.

### Helper FuncMap

The existing `inc`, `hasString`, and `replaceAll` helpers in `internal/ui/server.go` are unexported, and the parallel-replacement strategy prefers no cross-package coupling during the rollout. Phase 0 **duplicates** these three helpers verbatim into `internal/chassis/templates.go` rather than importing or re-exporting. Coupling-by-copy across two parallel UIs is preferable to coupling-by-import; the final cutover spec can deduplicate when `internal/ui` retires or is folded into the chassis package.

Add chassis-specific helpers:

- `pad2(int)` — zero-padded two-digit strings for clock display.
- `dim(bool)` — returns the CSS class string for inactive lamps.

Keep the helper surface tight; expand only when a partial genuinely needs one.

## CSS Architecture & Design Tokens

### Single `chassis.css` file

Roughly 3,200 lines, organized by banner-commented sections in this order:

1. `@font-face` declarations
2. `:root` design tokens
3. Reset and base typography
4. Chassis shell + screws + chrome
5. Status bar + masthead
6. VFD + source cluster
7. Meter screen (3 rows)
8. Transport row + seek bar
9. Visualizer bank
10. Input row + cast / torrent buttons
11. Preset bank + catalog
12. History + event log
13. Settings drawer
14. Idle / live state overrides
15. Responsive (container queries)

This matches the logical order the mockup already uses. Section banners are searchable comment headers.

### Scope isolation

Every chassis rule lives under `body.receiver`. The mockup uses bare selectors (`.receiver`, `.vfd`, etc.); the Phase 0 CSS port is a mechanical transformation pass with the rules below. The pass is explicit because subtle mistakes break the `/ui/*` isolation invariant. The body gets the `receiver` class as the CSS scope gate; the mockup's physical chassis shell keeps its inner `<div class="receiver">` class so mechanical selector porting still matches the original markup vocabulary.

**Porting rules** (apply in order; first matching rule wins):

1. **Leave untouched:** `:root` blocks, `@font-face`, `@container`, `@keyframes`, `@media`, comments. These do not select DOM elements, so no scoping is needed.

2. **Compound rewrite for body-rooted state selectors.** Any selector rooted at `body` becomes the same selector rooted at `body.receiver`:
   - `body.idle .foo` → `body.receiver.idle .foo`
   - `body.live .foo` → `body.receiver.live .foo`
   - `body:not(.idle) .foo` → `body.receiver:not(.idle) .foo`
   - `body.settings-open .foo` → `body.receiver.settings-open .foo`
   - `body.browse-open .foo` → `body.receiver.browse-open .foo`
   - `body.catalog-scanning .foo` → `body.receiver.catalog-scanning .foo`
   - `body[data-event-filter="warn"] .foo` → `body.receiver[data-event-filter="warn"] .foo`
   - `body.idle` (bare body selector) → `body.receiver.idle`

   The mockup uses descendant `body.idle ...` in ~50 places and also uses `body:not(.idle)`, `body.settings-open`, `body.browse-open`, `body.catalog-scanning`, and `body[data-event-filter=...]`. Without this rewrite, any future `/ui/*` page that ever sets one of those body states would inherit chassis overrides — the precise leak the scoping strategy is meant to prevent.

3. **Scope global element selectors.** The mockup contains a small custom reset that targets bare elements: `*`, `html`, `body`, scrollbar pseudo-elements. Scope each one under `body.receiver`:
   - `* { box-sizing: ... }` → `body.receiver, body.receiver * { box-sizing: ... }`
   - `html, body { ... }` → `body.receiver { ... }` (drop `html` — scoping under body is sufficient)
   - `*::-webkit-scrollbar { ... }` → `body.receiver ::-webkit-scrollbar { ... }`
   - Etc.

4. **Prepend `body.receiver` to every remaining top-level class/id/attribute selector.** Anything that begins with `.foo`, `#foo`, or `[attr]` becomes `body.receiver .foo`, etc.

5. **Inside `@container`, `@media`, and similar at-rules:** apply rules 2-4 to the nested selectors.

**Scope assertion test** (runs in `go test ./internal/chassis/...`):

```
TestChassisCSS_AllSelectorsScoped
```

The test scans full selector blocks, including multi-line selector lists and nested selectors inside `@container` / `@media`, and fails any element/class/id/attribute selector that is not either explicitly allowlisted (`:root`, `@font-face`, `@keyframes`, keyframe percentage selectors) or rooted at `body.receiver`. It also has explicit fixture assertions for the leak-prone mockup selectors: `body:not(.idle)`, `body.settings-open`, `body.browse-open`, `body.catalog-scanning`, `body[data-event-filter]`, bare `html`, bare `body`, and bare `*`.

**CSS parser implementation:** Use `github.com/tdewolff/parse/v2/css` (~MIT-licensed, single-purpose CSS tokenizer + parser, widely used in the Go ecosystem including Hugo). This adds one transitive dependency to `go.mod` — call it out in the Phase 0 PR description for review visibility. The tokenizer correctly handles multi-line selector lists, nested at-rules, comments, and string escapes — all of which the chassis CSS uses. Hand-rolling a brace-counting scanner was considered (no new dep, ~80 lines Go) but rejected because the mockup CSS is large enough that edge cases like `[attr*="x{y"]` selectors will accumulate; a real parser eliminates that whole class of false negatives. If a future dependency audit objects to `tdewolff/parse`, the fallback is to inline a vendored copy of just the `css` subpackage.

**Reasons for scope-by-body-class over class prefixing:**

- Single entry gate. Chassis CSS has zero effect on `/ui/*` pages even if assets are accidentally double-loaded.
- The mockup's class vocabulary ports without rename churn after the prefix pass.
- One global selector to invert behaviour in idle vs live: `body.receiver.idle` and `body.receiver.live`.
- Specificity bumps are predictable; internal compound selectors still override.

### Design tokens

OKLCH-based palette ports verbatim from `receiver-v24.html` lines 21-30 (do not paraphrase):

```css
:root {
  --vfd:           oklch(0.92 0.20 175);
  --vfd-bright:    oklch(0.96 0.18 175);
  --vfd-glow:      oklch(0.85 0.22 175 / 0.75);
  --vfd-glow-soft: oklch(0.85 0.22 175 / 0.32);
  --vfd-dim:       oklch(0.55 0.16 175 / 0.78);
  --vfd-faded:     oklch(0.55 0.16 175 / 0.35);
  --vfd-ghost:     oklch(0.42 0.12 175 / 0.18);
  --lock-amber:    oklch(0.82 0.18 75);
}
```

The mockup does not currently define chrome (`--chrome-*`, `--bezel`) or fluid-sizing tokens (`--vfd-title-size`, etc.) — chrome values live inline as `linear-gradient(...)` strings in component rules and fluid sizes live inline as `clamp(...)`. Phase 0 preserves the mockup's existing pattern; defining additional tokens is a Phase 5 polish opportunity if it materially helps later specs.

Phase 0 ports `:root` verbatim into the top of `chassis.css` (rule 1 of the scope-isolation pass leaves it untouched). Later specs reference tokens by name rather than re-deriving values.

### Font loading

All fonts self-hosted under `/receiver/static/fonts/`. No `<link>` to fonts.googleapis.com or any external host.

| Family | Weights / file | `font-display` | Notes |
|---|---|---|---|
| DSEG7-Classic | 400 + 700 (two woff2) | `block` | Matches mockup line 9-10. `swap` would flash Inter/monospace digits in place of segmented glyphs on every cold load — visually unacceptable for the VFD/meter readouts. |
| DSEG7-Modern | 400 + 700 (two woff2) | `block` | Same reasoning. |
| DSEG14-Classic | 400 + 700 (two woff2) | `block` | Same reasoning. |
| DSEG14-Modern | 400 + 700 (two woff2) | `block` | Same reasoning. |
| Inter | variable font (one woff2) | `swap` | Shipped as `Inter-Variable.woff2` (variable font supporting `wght` axis 100-900; `slnt` axis excluded from the Latin subset to keep file size down). Subset to Latin range, ~110-180 kB. Covers the only Inter weight the mockup actually uses (`font-weight: 800` at `.preset .num`) along with any weight subsequent specs add without per-weight binary growth. If a future spec needs italic Inter, a separate `Inter-Variable-Italic.woff2` is required — not in Phase 0 scope. |

DSEG fonts are MIT-licensed by [keshikan/DSEG](https://github.com/keshikan/DSEG). Inter is SIL OFL by [rsms/inter](https://github.com/rsms/inter). Both license texts and attributions get committed to `internal/chassis/static/fonts/LICENSE`. `internal/chassis/static/fonts/SOURCES.md` records the exact upstream release/tag, download URL, local filename, and SHA-256 checksum for every committed woff2 so the asset set is reproducible.

**License files are intentionally served as public static assets** via the `/receiver/static/fonts/LICENSE` and `/receiver/static/fonts/SOURCES.md` URLs (`//go:embed static` includes the entire subtree). This is the attribution-display mechanism — SIL OFL § 2 and the DSEG MIT clause both require that license text travel with redistributed copies, and embedding-then-serving is the lightest-weight way to comply for a single-binary distribution. The README mention in the deploy section should hyperlink to `/receiver/static/fonts/LICENSE` once the route is live. Do not exclude these files from the embed pattern.

**Why variable Inter instead of enumerated weights:** the mockup only uses 800 today, but porting it to a four-static-weight set (400/500/600/700) — which an earlier draft of this spec proposed — would miss 800 entirely and silently downgrade `.preset .num` numerals to the closest available weight. A variable font sidesteps this class of bug for the entire chassis rollout: every weight Phase 1-5 might want is already supported, with one woff2 file and one `@font-face` declaration.

### Container queries

Port from the mockup unchanged:

- `chassis ≤ 1180px` — drop network scopes from meter row 2.
- `chassis ≤ 900px` — drop goniometer; vfd-source-row collapses to single column.
- `chassis ≤ 600px` — drop meter source-strip; hide field-flip.
- `vfd ≤ 720px` — hide VFD right panel (time / queue / uptime).

Each breakpoint's rules cluster in section 15 of the stylesheet.

**`container-type` and `container-name` declarations from the mockup must be ported alongside the breakpoint rules.** The mockup declares `container-name: chassis; container-type: inline-size` on the chassis shell element and `container-name: vfd; container-type: inline-size` on the `.vfd` element. Without these declarations, `@container chassis (max-width: ...)` rules silently never match — the responsive breakpoints stop firing. Verify post-port by visually checking that the goniometer drops out at 900px viewport in the PR screenshots.

### Reset and base typography

Small custom reset (not Normalize.css, not Tailwind). Sets `box-sizing: border-box`, removes default margins on body and headings, sets chassis background and default Inter font on `body.receiver`.

## JS Runtime & State Machine

### Single `chassis.js` file

~150 lines vanilla ES2022. Loaded via `<script defer src="/receiver/static/chassis.js?v={{.Version}}">`. No bundler, no transpiler, no dependencies. Plain script with a top-level closure rather than an ES module — keeps `<script defer>` semantics simple and avoids the MIME-type pitfalls of module scripts served via `go:embed`.

### Module shape

```javascript
(() => {
  'use strict';

  const State = {
    IDLE: 'idle',
    LIVE: 'live',
    current() { return document.body.classList.contains('live') ? 'live' : 'idle'; },
    set(next) {
      document.body.classList.remove('idle', 'live');
      document.body.classList.add(next);
      animators.notify(next);
    },
  };

  const animators = {
    items: [],
    register(animator) { this.items.push(animator); animator.handleState?.(State.current()); },
    notify(state) { this.items.forEach(a => a.handleState?.(state)); },
  };

  function startSystemTimeTicker() {
    const el = document.querySelector('[data-system-time]');
    if (!el) return;
    const tick = () => {
      const d = new Date();
      el.textContent = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
    };
    tick();
    // Align the first interval to the next minute boundary so the clock
    // ticks visibly at :00 instead of an arbitrary phase offset. Math is
    // in unix ms so all timezones and DST transitions align correctly
    // (minute boundaries are universal). Long-lived tabs that survive a
    // system-clock change keep ticking at the original phase until reload —
    // acceptable for HH:MM display.
    const msUntilNextMinute = 60_000 - (Date.now() % 60_000);
    setTimeout(() => {
      tick();
      setInterval(tick, 60_000);
    }, msUntilNextMinute);
  }

  function pad(n) { return n.toString().padStart(2, '0'); }

  window.Chassis = { State, animators };

  document.addEventListener('DOMContentLoaded', () => {
    startSystemTimeTicker();
    if (new URLSearchParams(location.search).get('dev') === '1') {
      installDevStateToggle();
    }
  });

  function installDevStateToggle() { /* floating idle/live toggle button */ }
})();
```

### Idle vs live class machinery

Phase 0's handler always renders `<body class="receiver idle">`. The class is the trigger for every CSS rule under `body.receiver.idle .foo`. Spec 2 (VFD live) introduces the live state by reading session state server-side and pushing transitions over SSE; clients call `window.Chassis.State.set('live' | 'idle')`. Phase 0 ships the class hook and the entry point so Spec 2 does not need to refactor anything.

### Dev-only state toggle

Gated behind `?dev=1` query parameter handled in JS. Injects a fixed-position floating button that flips between idle and live for design iteration. Never visible unless the operator opens `/receiver?dev=1`. Subsequent specs may extend it (fake session payload, fake telemetry stream) but Phase 0 ships only the state-class flip.

### Files later specs add (reference only)

- Spec 2: `vfd-live.js` — SSE subscription, marquee text updates.
- Spec 3: `transport.js` — POSTs play / pause / stop / seek.
- Spec 4: `visualizer-bank.js` — POSTs `bridge.visualizer.mode`.
- Spec 5: `meter-animators.js` — registers spectrum / goniometer / throughput / ACK animators.

Each is loaded via its own `<script defer>` after `chassis.js` and attaches to `window.Chassis`. The base runtime in Phase 0 does not need to know they exist.

## Testing Approach

### Layer 1 — Go unit and handler tests (`internal/chassis/chassis_test.go`)

```
TestIdleSnapshot_AllFieldsPopulated
TestIdleSnapshot_DeterministicGivenSameNow
TestHandleIndex_RendersShell200
TestHandleIndex_IncludesEveryPartialMarker
TestHandleIndex_ExposesCorrectAssetPaths
TestHandleIndex_AssetURLsCarryVersionQueryParam
TestStaticAssets_CSS_Served
TestStaticAssets_CSS_VersionedFontURLs
TestStaticAssets_JS_Served
TestStaticAssets_Fonts_Served
TestStaticAssets_PathTraversalBlocked
TestStaticAssets_UnknownAsset404
TestChassisCSS_AllSelectorsScoped
```

Each partial renders a unique sentinel HTML comment (`<!-- chassis:vfd -->`, etc.). `TestHandleIndex_IncludesEveryPartialMarker` asserts every marker appears in the response body. Time-dependent tests inject a fixed `time.Time` via function argument — no `time.Now()` calls outside handler entry. Sentinel comments survive `html/template` rendering unchanged (only `<script>`-context comments are stripped); the test locks in that behaviour and would catch a regression if a future build step introduces a comment-stripping minifier.

`TestStaticAssets_CSS_VersionedFontURLs` asserts served CSS has substituted `?v=<Version>` font URLs and no raw `{{.Version}}` placeholders left behind. `TestChassisCSS_AllSelectorsScoped` enforces the scope rules described above, including multi-line selector lists and nested `@container` / `@media` selectors.

Phase 0 also adds a small `go test` import-check asserting production files in `internal/chassis` do not import `internal/ui` or `internal/uiserver`, and production files in `internal/ui` / `internal/uiserver` do not import `internal/chassis`. The check explicitly ignores `_test.go` files so integration-style coexistence tests can mount both packages without violating the production dependency rule. The risk register lists this as ship-in-Phase-0 work rather than the earlier "optional nicety" framing — the cross-package isolation invariant is load-bearing across all eight follow-up specs and the lint cost is trivial.

### Layer 2 — Template compilation tests

```
TestTemplatesParse
TestTemplatesExpectedHelpersAvailable
```

Catches syntax errors, missing `{{define …}}`, unknown function references, and renamed helpers before they reach a handler test.

### Layer 3 — Integration coverage

```
//go:build integration
TestReceiverEndToEnd
TestMount_DoesNotShadowUIRoutes
```

Spins a full bridge server matching the existing `tests/integration/` pattern. Parses the response body as HTML using `golang.org/x/net/html` and asserts presence of every section's root element (`.vfd`, `.meter-screen`, `.transport-strip`, `.viz-bank`, etc.). Guards "the chassis still renders" through every later spec's structural changes.

`TestMount_DoesNotShadowUIRoutes` constructs or boots the same mux shape as `main.go`, mounts the existing `ui.Server` first then `chassis.Server`, and asserts: `GET /ui/` returns the existing UI shell (not chassis content, not 404), `GET /ui/static/app.css` returns the existing stylesheet, `GET /receiver` returns chassis shell content, `GET /receiver/static/chassis.css` returns chassis CSS. The mount order in `main.go` is documented and structurally enforced by this test outside the `internal/chassis` package so the production no-cross-import invariant remains intact.

### Visual verification (manual checklist)

Phase 0's PR description includes a verification checklist for the merge reviewer. The checklist is invariant-based rather than a vague "pixel perfect" claim:

- Chrome / Edge / Firefox at 1920px — full chassis with all sections visible, matches mockup.
- 1440px — wide-desktop layout integrity.
- 1180px — network scopes hidden in meter row 2; rest unchanged.
- 900px — goniometer hidden; vfd-source-row collapses to single column.
- 600px — meter source strip hidden; field-flip hidden.
- 480px — degraded but readable (full mobile polish is Phase 5).
- Safari (current macOS) at 1920px and 600px — container queries + OKLCH render correctly.

Required invariants at every checked viewport: no body-level horizontal scroll, status bar/masthead/VFD/meter/transport/visualizer/input/preset/history regions visible unless that breakpoint explicitly hides the region, DSEG glyphs loaded for VFD/meter numerals, Inter loaded for non-segmented UI text, and focus rings visible on buttons/inputs. Screenshots at the desktop breakpoints attached to the PR. The Playwright tooling already configured for the brainstorm preview drives the WebKit smoke check.

**Decision on automated visual regression deferred to Phase 2.** If the manual checklist becomes noisy as live data lands, that is when we adopt Percy / Chromatic / reg-cli. Phase 0 does not need it.

### Test data fixtures

`testdata/` under `internal/chassis/` holds golden HTML fragments for the chassis chrome region only — where the visual is locked. Full-page golden diffs are too brittle across mockup tweaks.

### Explicitly out of scope

- Automated screenshot regression (see decision above).
- JS unit tests. Phase 0 runtime is ~150 lines, trivial state machinery. Spec 5 (telemetry) is where JS test infrastructure pays off.
- Cross-browser CI. Manual pre-merge smoke check per the visual verification checklist and attached screenshots.

### CI integration

No new CI jobs. The existing `.github/workflows/ci.yml` pipeline picks up `internal/chassis/*` automatically via `go vet`, `go test`, `go test -race`, and `go test -tags=integration`.

## Migration & Rollout

### Coexistence invariants

- `/ui/*` routes return byte-identical responses before vs. after the Phase 0 merge. Existing snapshot and integration tests pass unchanged.
- Production files in `internal/chassis/` have zero imports from `internal/ui/` or `internal/uiserver/`, and production files in `internal/ui/` / `internal/uiserver/` have zero imports from `internal/chassis/`. `cmd/mister-groovy-relay/main.go` is the composition root that wires both.
- Mount order in `cmd/mister-groovy-relay/main.go`: existing `ui.Server` first, then `chassis.Server`. `TestMount_DoesNotShadowUIRoutes` enforces this.

### Discovery and navigation

Phase 0 makes `/receiver` reachable but does not link to it from `/ui/*`, and `/ui/*` is not linked from `/receiver`. The eventual cutover spec will make the cross-link decision once Phase 1's live wiring exists. README receives a one-line "preview the new UI at /receiver — work in progress" note alongside the Phase 0 merge.

### Asset caching and versioning

`Cache-Control: public, max-age=31536000, immutable` on static assets. To avoid stale-asset bugs across releases, asset paths carry a build-version query parameter:

```html
<link rel="stylesheet" href="/receiver/static/chassis.css?v={{.Version}}">
<script defer src="/receiver/static/chassis.js?v={{.Version}}"></script>
```

The handler ignores `?v=` — purely a cache buster. Font URLs in `chassis.css` are likewise versioned via a one-time template pre-processing step at server startup (~20 lines, well within Phase 0 scope) that substitutes `{{.Version}}` placeholders inside the embedded `chassis.css` and caches the result.

**Use `text/template`, not `html/template`, for the `chassis.css` substitution pass.** CSS is not HTML and must not be context-aware-escaped. The concrete failure case is `<` and `>` characters inside CSS comments — e.g., a comment like `/* breakpoint <= 1180px hides scopes */` becomes `/* breakpoint &lt;= 1180px hides scopes */` under `html/template`, which silently corrupts the source and confuses any later CSS minifier or hand-grep for breakpoint comments. Use `text/template` for CSS, reserve `html/template` for `shell.html` and partials.

Hash-based filenames (`chassis.a3b1c2.css`) considered and rejected: cleaner cache semantics but adds a build step or filesystem rename dance the embed-FS distribution does not need.

### Config & flags

No new config fields. `/receiver` is unconditionally mounted because:

- It serves an idle preview only — no behaviour changes for non-chassis users.
- It is intentionally outside the existing `/ui/*` first-run guard. The preview is read-only in Phase 0, exposes no secret configuration values, and must not mutate or depend on setup state.
- A `bridge.ui.enable_chassis = true` toggle would have to be removed during cutover.
- The existing UI is unaffected.

A build tag (`//go:build !chassis`) is available if asset embedding ever becomes a binary-size concern in a later phase, but Phase 0 does not ship one.

### Docs

- `README.md` — one paragraph under deployment noting the new `/receiver` route exists and is preview-only. No screenshots until Phase 1+ lands real content.
- `internal/chassis/doc.go` — Go package doc explaining the parallel-replacement strategy, the relationship to `internal/ui`, and the slice ownership of subsequent specs.
- This design doc.

### Security considerations

Phase 0 ships no backend POST handlers, no user-input forms, and no session cookies, so the security surface is narrow. Two notes worth recording now so they don't get re-discovered in later specs:

- **`BrandName` and any future user-configurable template inputs.** Phase 0 hardcodes `BrandName` to `"GROOVY · RELAY"` in `idleSnapshot()`. If a later spec (Spec 8 — settings drawer) wires this to a bridge config field, validate the value against a printable-Unicode subset at the config layer rather than relying on `html/template` auto-escaping alone. `html/template` correctly escapes HTML contexts, but emoji, control characters, and overlong-length values can still produce unhappy `<title>` rendering across browsers.
- **CSP / framing.** Phase 0 inherits whatever Content-Security-Policy the bridge currently sets (none today). The chassis JS at `/receiver/static/chassis.js` is self-hosted and would be compatible with `script-src 'self'`; the inline `chassis.js` runtime uses `'use strict'` and no `eval`, `Function(...)`, or template `{{...}}`-style JS interpolation. If a future deployment adds a strict CSP, Phase 0 imposes no requirement for `'unsafe-inline'` or `'unsafe-eval'`. Spec 5 (telemetry SSE) should re-evaluate when adding `connect-src` requirements.

### Rollback strategy

`/receiver` is additive. Rollback = revert the merge commit; nothing in `/ui/*`, the bridge dataplane, or adapters is touched. No data migration, no config field, no persistent state to undo.

### Risk register

| Risk | Mitigation |
|---|---|
| Asset path collision with the existing `/ui/static/` route | `/ui/static/` already serves `app.css`, `htmx.min.js`, `now-playing.js`, `clipboard.js`, `streams-artwork.js`, and a `fonts/` subtree. Chassis assets use a different prefix (`/receiver/static/`); the prefix discipline is enforced by `TestMount_DoesNotShadowUIRoutes`. Font files are **duplicated**, not shared — per-package embed discipline trumps DRY during the parallel period. |
| `go:embed` for fonts inflates binary | 8 DSEG woff2 files (~15-25 kB each) ≈ 120-200 kB + Inter variable font Latin subset ≈ 110-180 kB. Combined ≈ 230-380 kB. Negligible at current binary size. |
| Font license issues | DSEG MIT, Inter SIL OFL. Licenses + attributions committed alongside woff2 files. |
| `chassis.css` grows too large to template-preprocess at startup | ~3,200 lines, ~80 kB minified. Substitution result cached in memory once at boot. Not a real concern at this size. |
| Subsequent specs accidentally introduce `/ui/*` ↔ `chassis` cross-imports | A production-file import-check asserts no cross-package import paths between `uiserver`/`ui` and `chassis`. Shipped in Phase 0 because the invariant is load-bearing for the eight follow-up specs and the lint cost is trivial. |
| Test flakiness from time-dependent idle snapshots | All snapshot tests inject a fixed `time.Time` via function argument. |
| Safari-specific container-query or OKLCH bug | Manual Safari smoke check is in the verification checklist. Container queries require Safari ≥ 16; OKLCH requires Safari ≥ 15.4. Both are current. |

### Cutover handoff

The final cutover spec at the end of Phase 5 will:

- Replace `/ui/*` registrations to forward to `/receiver/*` equivalents, or
- Remove `/ui/*` entirely and rename `/receiver/*` → `/ui/*`, retiring or folding `internal/ui/` after a deprecation cycle while preserving any still-needed saver plumbing from `internal/uiserver/`.

Phase 0 does not preempt that decision.

## Design Decisions Worth Revisiting

These are calls that are reasonable but not obviously correct. The spec documents them so reviewers know what is settled vs. what is a judgment call.

### Separate `internal/chassis/` package vs. extending `internal/ui/`

Roughly a 60/40 call in favour of separate package. Reasons for separation: clean isolation during the parallel period, no risk of accidental cross-coupling, and a clear boundary for the eventual collapse (which can either fold `chassis` into `internal/ui` or rename the chassis package into the primary UI package). Reasons against: duplicates plumbing (server struct, mount, embed FS, helper registration). A reviewer who prefers a single `internal/ui/` housing both UIs has a defensible case. Decision rationale: parallel-clean code is more valuable than de-duplicated plumbing during a multi-spec rollout.

### Dev-mode toggle via `?dev=1` query parameter

Not a security surface — the dev toggle only flips cosmetic state classes, exposes no real data, and triggers no backend calls. Alternatives considered: a debug HTTP header, a build tag, a config flag. Build tag and config flag add deploy-time friction; a debug header has zero friction but does not survive URL sharing, bookmarking, or browser history — and shareable dev URLs are the actual workflow we want. Query param matches how the brainstorm preview already works, survives bookmarks and links, and requires zero configuration. Reviewer who wants stricter gating should propose it explicitly; default stays as documented.

## Implementation Checklist

A focused checklist for the implementation plan to expand. Each item maps to a discrete unit of work.

1. Create `internal/chassis/` package skeleton: `server.go`, `data.go`, `handler.go`, `templates.go`, `doc.go`, `chassis_test.go`.
2. Add `embed.FS` declarations for `templates/*.html` and `static` inside `templates.go`. Use `text/template` for the `chassis.css` substitution pass; reserve `html/template` for `shell.html` and its partials.
3. Define `ReceiverPageData` struct and all sub-structs in `data.go`.
4. Implement `idleSnapshot(cfg Config, now)` populating every sub-struct with placeholder content matching the mockup's idle state.
5. Implement `handleIndex` and the static asset handler. Wire `Mount(mux)`.
6. Port mockup HTML into the 13 partial files under `internal/chassis/templates/` (12 component partials plus `vfd-source-row.html`, which is its own file containing a `{{define "vfd-source-row"}}` that composes the `vfd` and `source-cluster` partials onto the same row). Add `{{define …}}` blocks, sentinel comment markers (`<!-- chassis:<partial-name> -->`), and partial-scoped data references. The `vfd-source-row` partial is the only one that receives the full page data `.` instead of a narrow sub-struct, because it composes two siblings.
7. Port mockup CSS into `internal/chassis/static/chassis.css`. Run the `body.receiver` scope-prefix pass. Verify with `TestChassisCSS_AllSelectorsScoped`.
8. Copy the eight DSEG woff2 files from `.superpowers/brainstorm/1973-1779237107/` to `internal/chassis/static/fonts/`. Add a Latin-subset Inter variable font (`Inter-Variable.woff2`) from [rsms/inter](https://github.com/rsms/inter) releases to the same directory — one file, full weight axis. Commit `LICENSE` plus `SOURCES.md` with DSEG/Inter attributions, release URLs, and SHA-256 checksums.
9. Implement `chassis.js` with the `Chassis.State`, animator registry, minute-aligned system time ticker, and `?dev=1` toggle.
10. Implement the startup-time `{{.Version}}` substitution for `internal/chassis/static/chassis.css` font URLs.
11. Wire `chassis.New(chassis.Config{...})` into `cmd/mister-groovy-relay/main.go` after the existing `ui.Server` mount.
12. Write Layer 1 tests in `chassis_test.go`.
13. Write Layer 2 template-parse tests.
14. Write the integration test under `tests/integration/`.
15. Add the production-file cross-package import check asserting `internal/chassis` does not import `internal/uiserver` or `internal/ui`, and production files in those packages do not import `internal/chassis`.
16. Update `README.md` with the one-line preview note.
17. Run manual visual verification checklist at all breakpoints + Safari smoke. Attach screenshots to PR.

## Appendix A — `idleSnapshot()` Content Map

The implementer should populate every sub-struct field with the values below. Mockup references use **CSS selector anchors** rather than line numbers because the mockup is still iterating; selectors survive edits, line numbers don't. All anchors refer to elements in `.superpowers/brainstorm/1973-1779237107/receiver-v24.html`.

The `idleSnapshot()` helper is responsible for converting empty/zero config values to display strings — e.g., `cfg.HostIP == ""` becomes `"OFFLINE"` for `HostInfo.HostIP`. Keep these conversions in `idleSnapshot()`, not in templates, so the templates stay pure data renderers.

### Top-level `ReceiverPageData`

| Field | Go type | Idle value | Mockup anchor |
|---|---|---|---|
| Version | string | from `main.version`, passed via `chassis.Config.Version` | runtime |
| BrandName | string | `"GROOVY · RELAY"` | `.brand-plate .name` |
| State | string | `"idle"` | always for Phase 0 |
| HostInfo.HostIP | string | `cfg.HostIP` when non-empty, otherwise `"OFFLINE"` | runtime/config |
| HostInfo.HTTPPort | int | `cfg.Bridge.UI.HTTPPort` | config |

### `VFDData`

| Field | Go type | Idle value | Mockup anchor |
|---|---|---|---|
| State | string | `"idle"` | matches body class |
| Title | string | `"STANDBY"` | `.vfd-state--idle .title-line` |
| Marquee | string | `"MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET"` | `.vfd-state--idle .marquee-line` |
| QueueCurrent | int | `0` | idle has no queue |
| QueueTotal | int | `0` | idle has no queue |
| SystemTime | string | `"HH:MM"` from `now` (server-rendered; client ticker updates) | `.vfd .big-time .seg-display` |
| Uptime | string | `"Nh Nm"` from `now - cfg.StartedAt` | `.vfd .freq` (live mockup shows `"4H 12M"`) |

### `SourceData`

The four hardware buttons. Phase 0 ships all four with one (STREAMS) marked active by default — matches the mockup's standby state.

| Field | Go type | Idle value | Mockup anchor |
|---|---|---|---|
| Buttons | `[]SourceButton` | four entries below | `.source-cluster .hw-btn` |

Each `SourceButton`:

| Field | Idle value (per button) |
|---|---|
| `{Label: "STREAMS", Active: true,  Lit: false}` | first |
| `{Label: "PLEX",    Active: false, Lit: false}` | second |
| `{Label: "JELLYFIN",Active: false, Lit: false}` | third |
| `{Label: "DLNA",    Active: false, Lit: false}` | fourth |

### `MeterData`

Three sub-rows. Every field placeholder is the dim/empty version of its live counterpart.

`MeterData.SourceStrip` (row 1):

| Field | Idle value | Mockup anchor (live value for reference) |
|---|---|---|
| AudioIn | `"---"` | `.meter-source-strip .audio-grp .codec-grp .val` (live: `"AAC LC · STEREO"`) |
| AudioOut | `"---"` | `.meter-source-strip .audio-grp .grp:nth-child(2) .seg-display` (live: `"S16LE · 48k"`) |
| Src | `"---"` | `.meter-source-strip .video-grp .grp:nth-child(1) .seg-display` (live: `"1280×720@30 · H.264"`) |
| Crop | `"---"` | `.meter-source-strip .video-grp .grp:nth-child(2) .seg-display` (live: `"NONE · 16:9 NATIVE"`) |
| HLSBuffer | `"0 / 0 SEG"` | `.meter-source-strip .net-grp .grp:nth-child(1) .seg-display` |
| Drops | `"0.0"` | `.meter-source-strip .net-grp .grp:nth-child(2) .seg-display` |

`MeterData.MidRow` (row 2):

| Field | Idle value | Mockup anchor |
|---|---|---|
| BitrateMbps | `"0.0"` | `.meter-mid-row .video-grp .stat:nth-child(1) .v` |
| FreqKHz | `"---"` | `.meter-mid-row .video-grp .stat:nth-child(2) .v` |
| Mode | `"---"` | `.meter-mid-row .video-grp .stat:nth-child(3) .v` |
| StandardNTSC | `bool true` | `.meter-mid-row .std-lamps--stack .std-ind.active` |
| StandardPAL | `bool false` | `.meter-mid-row .std-lamps--stack .std-ind:not(.active)` |
| FieldFlip | `string "idle"` | `.meter-mid-row .field-flip` (freezes ODD/EVEN animation) |
| ThroughputMBs | `"0.0"` | `.meter-mid-row .net-grp .stat:nth-child(1) .v` |
| MSAck | `"--"` | `.meter-mid-row .net-grp .stat:nth-child(3) .v` |

`MeterData.Readout` (row 3):

| Field | Idle value | Mockup anchor |
|---|---|---|
| LRBars | 0 / 12 segments lit | `.meter-readout-line .tr-vu .ch-bar` (idle: no `.on` class on segments) |
| PhaseNeedle | string `"0"` (center) | `.meter-readout-line .vu-phase .needle` |
| LUFS | `"---"` | `#lufs-val` (live: `"-16.2"`) |
| Output | `"---"` | `.meter-readout-line .video-grp .grp:nth-child(1) .seg-display` (live: `"INTERLACE 480i · BT.601"`) |
| Aspect | `"---"` | `.meter-readout-line .video-grp .grp:nth-child(2) .seg-display` (live: `"4:3 PILLARBOX"`) |
| Pipe | `"---"` | `.meter-readout-line .net-grp .grp:nth-child(1) .seg-display` |
| Speed | `"---"` | `.meter-readout-line .net-grp .grp.speed-grp .val` |
| Link | `"---"` | `.meter-readout-line .net-grp .grp.link-grp .val` |

### `TransportData`

| Field | Idle value | Mockup anchor |
|---|---|---|
| PlayState | `"stopped"` | `.transport-row .trn.primary` (idle: no `.active`) |
| ElapsedTime | `"--:--"` | `.seek-time > .seg-display:first-of-type` |
| TotalTime | `"--:--"` | `.seek-time .total` |
| PercentPlayed | `"---"` | `.seek-time .pct` |
| SeekFillPercent | `0` | `.seek-bar .fill` (idle: width 0) |

### `VisualizerData`

| Field | Idle value | Mockup anchor |
|---|---|---|
| ActiveMode | `cfg.Bridge.Visualizer.Mode` (default `"retro_analyzer"`) | `.viz-bank .viz-btn.active` |
| Buttons | 4 entries: `retro_analyzer`, `oscilloscope_wave`, `stereo_scope`, `radial_spectrum` (preview) | `.viz-bank .viz-btn[data-viz=…]` |

Each viz button: `{Mode, Label, IconKind, IsPreview}`. `radial_spectrum` has `IsPreview: true` and renders the deferred-state badge. The handler reads `cfg.Bridge.Visualizer.Mode` to determine which button is active+lit at first render.

### `InputData`

| Field | Idle value | Mockup anchor |
|---|---|---|
| PastePlaceholder | `"Paste URL or magnet"` | `#paste-input[placeholder]` |
| DetectedKind | `"URL"` | `#paste-chip` |
| CastEnabled | `false` (CAST button disabled in idle) | `#cast-btn.disabled` |

### `PresetsData`

| Field | Idle value | Mockup anchor |
|---|---|---|
| ModeLabel | `"Memory · 0 / 12 slots"` | `#preset-mode-label` (live mockup shows `"Memory · 12 / 12 slots"`; idle = 0 filled) |
| Count | `"★ 0"` | `#preset-count` (live: `"★ 12"`) |
| Slots | `[12]PresetSlot{}` all empty | `.preset-bank .preset` (12-slot grid) |

Each empty `PresetSlot`: `{Filled: false, Title: "", Subtitle: ""}`. The slot still renders a numbered placeholder.

### `HistoryData`

| Field | Idle value | Mockup anchor |
|---|---|---|
| Rows | `[]HistoryRow{}` (empty) | `.history-section .history-row` (idle: none) |
| EmptyMessage | `"No recent casts"` | new for chassis (mockup elides this) |

### `SettingsData`

| Field | Idle value | Mockup anchor |
|---|---|---|
| Open | `false` | `.settings-panel` (drawer closed by default) |

The drawer markup is rendered with `display: none` (or analogous CSS) so the closed state has zero visual footprint. Spec 8 wires the open/close behaviour.

## Appendix B — Visual Verification Reference

The visual checklist in the Testing section references "matches mockup" at each breakpoint. Concretely, the implementer renders `/receiver` at each breakpoint and compares against the mockup served from `.superpowers/brainstorm/1973-1779237107/receiver-v24.html?idle=1` (idle toggle on) while also attaching screenshots to the PR. Because `.superpowers/` is a brainstorm workspace rather than a durable product source, the implementation PR must either commit a frozen reference copy under `docs/superpowers/reference/` or attach the exact mockup artifact used for comparison. A diff that's purely text content (`STANDBY` vs `FIRST DAY ON MTV`) is expected for fields that come from `idleSnapshot()`; the required review gate is the invariant checklist in the Testing section, not an undefined pixel-perfect threshold.

## Open Questions for Subsequent Specs

Not blocking Phase 0, but worth noting:

- Spec 2 chooses between SSE and WebSocket for the idle/live state stream. SSE is simpler and one-directional; WebSocket is overkill but matches what Spec 5 (telemetry) likely wants.
- Spec 5 chooses telemetry sampling rate, idle backpressure, and whether spectrum / goniometer canvases compute client-side from raw PCM or pre-rendered server-side.
- Spec 8 decides whether the settings drawer reuses the existing field-schema rendering (`bridge-panel.html` machinery) or reimplements in the chassis vocabulary.
- The final cutover spec decides between forwarding `/ui/*` to `/receiver/*` or renaming the chassis routes.
