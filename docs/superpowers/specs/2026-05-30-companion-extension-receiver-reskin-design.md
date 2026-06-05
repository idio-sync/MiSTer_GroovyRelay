# Companion Extension — Receiver-Chassis Visual Reskin

**Date:** 2026-05-30
**Status:** Design approved + code-reviewed; volume = functional; ready for implementation plan
**Scope:** Restyle the `extension/firefox/` companion popup and options page to
match the new `/receiver` chassis look, ahead of the new web UI shipping at
`/ui`. Visual reskin only — the extension keeps talking to the existing
`/ui/companion/*` JSON API. One small, isolated bridge-side addition is
included so the reused volume knob is functional (see §6).

## Problem

The companion popup and options page use the older "PR2" visual language
(cool blue `oklch` hue-235 palette, Space Grotesk / Inter Tight / JetBrains
Mono, flat buttons). Meanwhile the bridge's new receiver UI (`internal/chassis`)
has a distinct hardware-instrument aesthetic: a dark faceplate with corner
screws, an aqua **VFD** display in DSEG segmented fonts, glowing status LEDs,
physical-feel instrument buttons, and a rotary volume knob. When the receiver
UI replaces the current `/ui`, the extension will look out of place. We want
the extension to feel like part of the same instrument.

## Goals

- Reskin the popup (active "now-playing" + idle "cast-this-tab" states) to the
  receiver chassis design system.
- Reskin the options/settings page to match.
- Reuse the receiver `volume-control` knob's look (CSS + markup) in the popup,
  reimplementing its interaction JS against the companion API (see §6 / Visual).
- Bundle the receiver's DSEG + Inter fonts into the extension so the segmented
  displays render correctly offline.
- Preserve all current popup behavior and the `/ui/companion/*` integration.

## Non-Goals

- No migration to `/receiver/*` endpoints. The companion API stays `/ui/companion/*`.
- No cross-origin gate / CORS work on `/receiver`.
- No new popup features beyond volume (no visualizer mode, source select, EQ,
  catalog, presets, adapter admin).
- No functional change to casting, transport, launch, or options logic. (The
  one deliberate functional addition is the volume passthrough in §6 — called
  out as the single non-visual change; see Open Questions.)
- No framework: stays plain HTML/CSS/JS with the existing vitest suite.

## Decisions Captured During Brainstorm

| Topic | Decision |
|---|---|
| Backend | Keep `/ui/companion/*`; do **not** move to `/receiver`. |
| Density | **Condensed** faceplate (dot LEDs, slim chrome, no corner screws on the popup) over the full faceplate. |
| Volume | Reuse the receiver volume knob (requires a small companion volume passthrough — §6). |
| History | **Drop** the URL-history list from the popup. |
| Options page | Reskin it too. |
| Transport buttons | Match companion capabilities: pause/resume, stop, replay (+ seek). No previous/next (not in the companion API). |

## Visual Design

Source of truth for the look: `internal/chassis/static/chassis.css` and the
`internal/chassis/templates/*` partials. The extension does **not** import that
stylesheet; it defines a compact popup-local stylesheet that mirrors the same
tokens and component recipes, scaled to a 360px popup.

**Tokens (mirror chassis `:root`):**

```css
--vfd:        oklch(0.92 0.20 175);   /* aqua VFD text            */
--vfd-bright: oklch(0.96 0.18 175);
--vfd-glow:   oklch(0.85 0.22 175 / 0.75);
--vfd-glow-soft: oklch(0.85 0.22 175 / 0.32);
--vfd-dim:    oklch(0.55 0.16 175 / 0.78);
--vfd-faded:  oklch(0.55 0.16 175 / 0.35);
--amber:      oklch(0.82 0.18 75);    /* load-core / primary accent */
--ok:         oklch(0.78 0.14 150);   /* healthy / online           */
--err:        oklch(0.66 0.20 28);
/* chassis neutrals: panel #2a2a2e→#1f1f23, wells #0a0a0b/#050506 */
```

**Fonts (bundled woff2, copied from `internal/chassis/static/fonts/`):**

- `DSEG14-Classic` (Regular + Bold) — VFD text tiers, segmented labels.
- `DSEG7-Classic` (Regular + Bold) — times, counters, volume value.
- `Inter` (variable) — chrome labels, buttons, body.

The current Space Grotesk / Inter Tight / JetBrains Mono assets are removed
(no longer referenced). Font license notices updated: DSEG is MIT, Inter is
SIL OFL 1.1 (see `internal/chassis/static/fonts/LICENSE` and `SOURCES.md`).

### Popup — condensed faceplate

Shell: 360px-wide chassis panel (gradient `#2a2a2e → #1f1f23`, 1px `#0a0a0b`
outline, inset top highlight, soft drop shadow). No corner screws (condensed).
Honors `prefers-reduced-motion`.

**Status bar (both states):** dot LEDs on the left — `PWR` (green), `LINK`
(aqua), `CAST` (red, pulsing only while a cast is live) — and a right-aligned
brand plate (`GROOVYRELAY`).

LED → data mapping (the companion `health` data is thinner than it looks:
`health.bridge` is hardcoded `"online"` on any successful status call,
`health.mister` is hardcoded `"unknown"`, and only `health.url_adapter` is
dynamic via `companionAdapterHealth`):
- `PWR` (green): lit whenever a status poll succeeds (bridge reachable).
- `LINK` (aqua): lit on `health.bridge == "online"`.
- `CAST` (red, pulsing): lit when `session.state` is `playing` or `paused`.
- Idle health tiles: Bridge = `health.bridge`, URL = `health.url_adapter`
  (the meaningful one), MiSTer = `health.mister` (will read `unknown` until the
  backend provides real MiSTer health — render it muted, not alarming).

**Active / now-playing:**

- **VFD screen** (`.screen-frame` > `.screen.vfd` recipe): dark radial well with
  scanline overlay, aqua glow text. Three tiers:
  - `tier-primary` (DSEG14, ~17px): media title.
  - `tier-secondary` (DSEG14, ~11px): source display / adapter.
  - `tier-tertiary` (DSEG14-dim, ~10px): resolver mode (e.g. `URL · DIRECT`).
    Sourced from `session.resolved_via` (present in the status payload but **not
    rendered by the popup today** — small net-new render, not "preserved").
  - Right mini-panel: uptime/queue-style readout (DSEG7) + a `PLAY`/`PAUSE`
    label. Empty tiers collapse (`is-empty`).
- **Transport strip:** instrument buttons (`.trn` recipe — metal gradient, inset
  highlight) in capability order: replay, **pause/resume** (center, `.primary`,
  aqua, swaps ⏸/▶ by state), stop. Buttons disable from companion capability
  flags. The single pause/resume button dispatches `bridge.control("pause")` when
  playing and `("resume")` when paused. Below: a **display-only** `.seek` progress
  bar (aqua fill, `--seek-percent` from position/duration) and a DSEG7
  `elapsed / total` + percent line. The progress block is hidden when
  `duration_ms <= 0`. (The reskin drops the old draggable seek `input`; position
  is shown, not scrubbed — consistent with the mockup.)
- **Volume control:** reuse the receiver knob's **look + DOM shape** — rotary
  `.volume-dial` + `.volume-notch`, a `.volume-tick-ring`, hidden range input,
  DSEG7 value, `--volume-angle` bound to 0..100 — but **reimplement the
  interaction JS**. The tick ring is rendered as a **flat horizontal bar** in
  the popup (NOT the receiver's radial 21-`.tick-N` arrangement) — simpler, reads
  well at popup scale, matches the approved mockup. The dial rotation
  (`--volume-angle`) and DSEG7 value still mirror the receiver.
  The receiver's `volume-knob.js` cannot be reused verbatim: it POSTs form-
  encoded to a hardcoded `/receiver/volume` expecting **204**, syncs via the
  `/receiver/events` **SSE** stream, and uses `credentials: 'same-origin'`. The
  popup instead POSTs **JSON** to `/ui/companion/volume` expecting **200**, syncs
  via the existing 2s status poll, and authenticates with `X-Bridge-Extension`.
  So: borrow the markup + `angleFor()` math + drag/wheel/arrow handlers, swap the
  network + sync layer. Render the knob **once** (a single shared element below
  the swapped active/idle views), and **drop the receiver's hardcoded
  `id="receiver-volume-range"`** in favor of a popup-scoped id to avoid collisions.
- **Note on porting chassis CSS:** every chassis rule is scoped `body.receiver
  .x { … }`. When porting recipes into the popup stylesheet, either strip the
  `body.receiver` prefix or add a matching class to the popup root.
- **Footer:** Launch core (amber), Open Web UI, Setup.

**Idle / cast-this-tab:**

- Status bar with `CAST` LED dark.
- **Cast panel:** `CAST THIS TAB` label, active-tab URL (mono), primary aqua
  `Cast tab` button. Disabled with a reason when the tab has no http(s) URL.
- **Health tiles:** Bridge / MiSTer / URL, as compact chassis tiles (aqua when
  healthy, dim when idle/unknown) — sourced from companion `health`.
- Volume control still shown (matches receiver, which exposes volume when idle).
- Footer: Launch core, Open Web UI, Setup.

**Unconfigured / unreachable:** chassis-styled empty state — brand plate, a
short message in the VFD well, and a single `Configure` (or Open Web UI) action.

### Options page

Opens in a full tab. Centered chassis panel (~480px, with corner screws since
there's room): brand plate + `CONFIG` LED, a `BRIDGE CONFIGURATION` section, the
bridge-URL field rendered as a VFD-style inset input (aqua text + caret),
instrument **Save** (amber) and **Test connection** buttons, and an LED-style
result readout (`● Bridge online` in green-ok, `● …` in err red on failure).
Validation and test-connection logic are unchanged; only markup/CSS and the
status rendering change.

## Architecture / Files

Keep the existing `extension/firefox/` structure; reskin in place.

```text
extension/firefox/src/
  popup/
    popup.html   # restructure to chassis components; remove history markup
    popup.css    # full rewrite to popup-local chassis stylesheet
    popup.js     # render against new DOM; remove history; add volume-knob logic
  options/
    options.html # chassis panel markup (drops the inline light-theme <style>)
    options.css  # NEW — chassis styles extracted from the old inline block,
                 # for parity with popup.css (shares the token set)
    options.js   # unchanged logic; LED-style status rendering tweak
  lib/bridge.js  # +volume(level) companion client method (see §6);
                 # remove now-dead historyPlay/historyDelete (see §7)
```

**Fonts are committed files under `extension/firefox/src/fonts/`** (not generated).
`build.sh` has no font array — it zips the whole `src/` tree
(`includes = ["manifest.json", "src", "icons"]`). So the reskin must:

- Replace the committed woff2 files in `src/fonts/`: remove
  `SpaceGrotesk-600` / `InterTight-400` / `InterTight-500` / `JetBrainsMono-400`,
  add `DSEG14Classic-Regular` / `DSEG14Classic-Bold` / `DSEG7Classic-Regular` /
  `DSEG7Classic-Bold` / `Inter-Variable` (copy from `internal/chassis/static/fonts/`).
- Ensure the woff2 filenames match the `@font-face { src: ... }` URLs in the
  new `popup.css` / `options.css`, and update the `manifest.test.js` `existsSync`
  font list (it asserts the committed files are present).
- No `build.sh` change is required.

**License notices** (their own task — see §7): update `THIRD_PARTY_NOTICES.md`
and `LICENSES/` (and any per-font license-copy step in `build.sh`) — remove the
three retired typeface families, add DSEG (MIT) and Inter (SIL OFL 1.1).

`background.js` and the Plex-timeline content scripts are untouched.

**Manifest/CSP:** no `manifest.json` CSP change is needed — MV3 loads packaged
fonts from the extension's own origin (the current popup already `@font-face`s
local fonts). `data_collection_permissions` is unaffected by a reskin.

## §6 — The one backend touch: companion volume passthrough

The reused knob needs a data path. The companion API has no volume today, and
the popup must not call `/receiver/volume` (cross-origin + same-origin-gated —
explicitly out of scope). So add a thin volume passthrough to the **existing**
companion API in `internal/ui/companion.go`, delegating to the **same**
volume read/write the receiver already uses
(`uiserver.BridgeSaver.SaveOutputVolume` + a volume viewer):

- `GET /ui/companion/status` → add `"output_volume": <0..100>` to the payload.
- `POST /ui/companion/volume` (JSON `{ "output_volume": 0..100 }`) → validate,
  call the shared volume saver, return `{ "ok": true, "output_volume": n }`.
  Field name is `output_volume` to mirror the chassis `/receiver/volume` form
  field (avoids a `volume`/`output_volume` split). Reuses the existing
  `companionExtensionGate` + JSON helpers; same `400/403/500` contract.

**Wiring (corrected — this is more than "instances already exist"):** the
`VolumeViewer` / `VolumeSaver` interfaces today live in **`internal/chassis`**
(`internal/chassis/volume.go`), and are wired into **`chassis.Config`** at
`main.go` (`VolumeViewer: coreMgr`, `VolumeSaver: &volumeSaverAdapter{bs: saver}`).
`internal/ui` has **no** volume types and `ui.Config` has **no** volume fields.
So the work is: (1) add a small `VolumeViewer`-style read interface (+ a saver
field) to `internal/ui`; (2) add those fields to `ui.Config`; (3) pass
`coreMgr` + the existing `volumeSaverAdapter` (already defined in `main.go`)
into the `ui.New(...)` call. Small and feasible, but it is a `ui.Config` struct
change plus a new interface, not just reuse.

The popup computes `--volume-angle` client-side (the receiver knob's JS already
has an `angleFor()` it can borrow); the server-side `volumeAngle` Go template
func is not reachable from the extension. This passthrough is the only
non-visual change.

**Fallback if you'd rather keep this release strictly visual:** render the knob
display-only (no `POST`), bound to a future `output_volume` field, and land the
passthrough separately. The design works either way; default is to include it
so the knob functions.

## §7 — History removal cleanup + license notices

Dropping the history list from the popup is more than markup deletion:

- Remove the history-rendering DOM/JS from `popup.html` / `popup.js`.
- **Remove the now-dead `historyPlay` / `historyDelete`** from
  `src/lib/bridge.js` and any popup/background wiring that called them (or
  consciously keep them as unused API — default is to prune).
- **Delete/rewrite the existing history tests** in `test/popup.test.js`
  (`"plays and deletes history rows by opaque id"`, ~line 225) — these are
  currently passing tests, not hypothetical ones. The `popupMarkup()` fixture in
  that file (~lines 5-65) duplicates the popup DOM, including `#history`; keep it
  in sync with the new markup or every test using it breaks.
- If `historyPlay`/`historyDelete` are pruned from `bridge.js`, update
  `test/bridge.test.js` accordingly.
- Note: `GET /ui/companion/status` still *returns* a `history` array; the popup
  simply stops reading it. No bridge change for history.

**License notices task:** update `THIRD_PARTY_NOTICES.md` + `LICENSES/` (and any
`build.sh` license-copy step) to drop Space Grotesk / Inter Tight / JetBrains
Mono and add DSEG (MIT) + Inter (SIL OFL 1.1), per
`internal/chassis/static/fonts/LICENSE` and `SOURCES.md`.

## Behavior preserved

- Adaptive popup (active vs idle vs unconfigured/unreachable), 2s quiet polling
  of `/ui/companion/status`, polling paused during in-flight commands.
- Capability-driven control enable/disable (server-owned flags).
- Context-menu link/video casting via the companion play endpoint.
- Options validation + test-connection against companion status.
- Same `X-Bridge-Extension: 1` + extension-origin companion path.

## Testing

- **Extension (vitest):**
  - `popup.test.js`: render of active VFD tiers from title/source/resolver;
    transport button enable/disable from capability flags; pause/resume glyph
    swap by state; seek hidden when no duration; idle cast panel + health tiles;
    unconfigured/unreachable states; **history tests removed**; **new** volume
    knob tests (value → angle, drag/wheel → `bridge.volume`, disabled when no
    saver).
  - `options.test.js`: validation unchanged; LED status rendering for
    ok/err/testing.
  - `manifest.test.js`: versions in sync **and** its `extensionFiles` font
    assertion (currently hardcodes `SpaceGrotesk-600`/`InterTight-400`/
    `InterTight-500`/`JetBrainsMono-400`, ~lines 6-9) updated to the new font
    set; confirm `data_collection_permissions` + `strict_min_version: 140.0`
    gecko fields are preserved through the version bump.
  - Build/packaging test: required font files present; bundled font license
    notices cover DSEG (MIT) + Inter (OFL). `THIRD_PARTY_NOTICES.md`'s "Bundled
    UI Fonts" section currently (wrongly, post-swap) lists all three retired
    families as OFL — rewrite it to DSEG (MIT) + Inter (OFL).
- **Go (`internal/ui`):** companion status includes `output_volume`;
  `POST /ui/companion/volume` happy path + range/`400` + gate `403`;
  delegates to the shared saver.

## Build sequence

1. **Bridge:** companion volume passthrough (§6) + tests. Independent, no UI change.
2. **Fonts + CSS foundation:** swap the committed woff2 files in `src/fonts/`
   (DSEG/Inter in, PR2 families out) — no build.sh change; write the popup-local
   chassis stylesheet + tokens; update `THIRD_PARTY_NOTICES.md` for DSEG (MIT) +
   Inter (OFL).
3. **Popup:** rebuild `popup.html` markup + `popup.js` render (incl. volume
   knob, drop history); tests.
4. **Options page:** reskin `options.html` + status rendering; tests.
5. **Version bump + README:** lockstep `manifest.json`/`package.json`; refresh
   README "What it does" and any screenshot.

## Open Questions

_None — all resolved._

1. ~~Volume: include §6 passthrough now, or ship knob display-only?~~
   **RESOLVED (2026-05-30): functional.** Include the §6 companion volume
   passthrough (`ui.Config` change + new `internal/ui` volume interface +
   `POST /ui/companion/volume`) so the knob actually controls output volume.
