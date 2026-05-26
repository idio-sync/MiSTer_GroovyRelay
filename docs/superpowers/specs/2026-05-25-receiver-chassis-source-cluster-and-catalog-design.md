# Receiver Chassis Source Cluster & Streams Catalog — Phase 3B Design

**Status:** Brainstormed; awaiting implementation plan.

**Scope:** Second sub-spec of Phase 3 (Cast Initiation). Re-casts the four non-AUX source-cluster buttons (STREAMS / PLEX / JELLYFIN / DLNA) as 80s/90s AV-style three-state indicator lamps; wires the BROWSE button in the preset-bank header to a server-rendered Streams catalog drawer (provider tabs → group rail → channel grid); adds user-curated preset editing via star-toggle in the catalog plus pointer-based drag-to-reorder of the preset bank; enables the preset-header search/filter field to live-filter both surfaces. The catalog drawer is Streams-only in 3B and intentionally exposes only the three bundled/mockup Streams providers; Plex / Jellyfin / DLNA library browsing and remote-manifest provider browsing require new adapter library/catalog surfaces and are out of scope.

**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored; this spec is force-added per the receiver chassis rollout convention.

## Context

[Phase 0](2026-05-21-receiver-chassis-foundation-design.md) shipped the chassis chrome at `/receiver`, including idle-state placeholder templates for the source-cluster and catalog drawer. Phase 1 wired live VFD, transport, and visualizer state. Phase 2 closed the meter screen (telemetry + audio scopes). [Phase 3A](2026-05-25-receiver-chassis-input-and-presets-design.md) wired the input row (paste-to-cast via `adapters.QuickCastProvider` and a new typed `adapters.QuickCastError`) and the 12-slot preset bank (read-only, bundled from `streams/assets.go`). 3A established `POST /receiver/cast` and `POST /receiver/preset/{slot}/cast` plus the `PresetViewer` / `PresetCaster` adapter interface convention.

**Phase 3 decomposition (revised in this spec):**

- 3A — Input row + preset bank (shipped, merge `0e0baa7`)
- **3B (this spec)** — Source-cluster lamps + Streams catalog drawer + user-curated preset editing (save/recall via star toggle, drag-to-reorder) + search/filter
- 3D (future) — Slot rename, restore-defaults UI, settings-drawer integration

The original Phase 3 decomposition had a separate Phase 3C for "user-curated presets (save/recall, drag-to-slot, rename)". Save/recall and drag-reorder are folded into 3B because they're the natural consumers of the catalog drawer's star affordance. Slot rename has no mockup precedent and creates a third interaction model on preset slots; it stays as 3D.

**Mockup reference:** `docs/superpowers/reference/2026-05-21-receiver-v24.html`. This spec cites mockup line numbers throughout. Where the mockup's behavior diverges from production reality (e.g., the catalog JS hardcodes provider data; the real chassis pulls from an adapter interface), this spec calls the divergence out explicitly.

## Goals

1. **Source-cluster lamps work end-to-end.** STREAMS/PLEX/JELLYFIN/DLNA render as Pioneer/Sansui-style indicator lamps (LED dot + engraved nameplate). Three states per lamp: unavailable (adapter not registered or `Configured()` false, including `IsEnabled()` false), configured-idle (linked/enabled), casting (currently the active cast source). AUX stays as its existing `hw-btn` alongside.
2. **Catalog drawer matches the mockup.** Clicking the (currently `disabled`) BROWSE button in the preset-header opens the drawer beneath the preset bank. Drawer renders the three bundled Streams provider tabs (MTV Rewind / Cartoon Rewind / Toonami Aftermath), a 168px left rail of groups, and a right channel-card grid. Catalog data is server-rendered from `internal/adapters/streams/assets.go` plus local seed/cache data (which already has the provider → group → channel structure the mockup expects).
3. **Channel click immediately casts** via a new `POST /receiver/streams/cast` route and a new narrow `adapters.StreamsCaster` interface. Reuses the `{ok, chip}` JSON shape and `*adapters.QuickCastError` typed wrapping established in 3A.
4. **Star toggle edits the preset bank.** Clicking the corner star on a channel card sends an idempotent desired state (`starred=true|false`) that adds the channel to the first empty slot, or removes it from its current slot(s). Persisted to `{data_dir}/chassis_presets.json` via atomic write. All-12-full triggers a 409 `BANK FULL` chip. Drag-and-drop reorder of preset slots (pointer-based, swap semantics) lives alongside.
5. **Search/filter is live, client-only.** The preset-header `<input id="search-input">` (currently disabled after 3A) becomes active. Filters both the preset bank and the catalog grid by name / badge / channel-id substring. ESC clears.
6. **Source-cluster, catalog grid, and preset bank share `tuned` state.** All three derive from the existing `transport` SSE event's `AdapterRef`. No three-way state divergence is possible.
7. **`internal/chassis/` adds no new concrete-adapter imports.** The new `StreamsCatalogViewer`, `StreamsCaster`, `PresetEditor`, and `SourceAvailabilityViewer` interfaces live in `internal/adapters/`. `import_check_test.go` continues to enforce isolation.
8. **`/ui/*` is unchanged.** 3B is additive under `/receiver/*`.

## Non-Goals

- **Plex / Jellyfin / DLNA library browsing.** These adapters are pull-style (Plex/Jellyfin) or discovery-only (DLNA); none expose library data today. Wiring real library browsing for any of them is a multi-spec effort (per-adapter library fetch, pagination, refresh, permissions). Their lamps remain status-only.
- **Remote-manifest Streams provider browsing in `/receiver`.** The existing Streams adapter may merge remote/cached manifest providers for playback and `/ui/streams/*`, but 3B's chassis catalog stays faithful to the mockup's three bundled providers. Remote provider tabs need fallback badge/artwork rules and are deferred.
- **Slot rename.** User-overridable Title field on preset slots. Deferred to 3D.
- **"Restore defaults" UI button.** Users can `rm {data_dir}/chassis_presets.json` to revert; a UI button lives in 3D alongside the settings-drawer port.
- **Hover-X "remove" affordance on preset slots.** Removal goes through the catalog star toggle. Reduces 3B's surface and keeps each slot single-purpose (click = cast). 3D can revisit if usage data warrants.
- **Reordering across drawer modes.** Dragging a preset slot onto a catalog card is not a valid drop. The two surfaces stay logically separate.
- **History row population.** Phase 5.
- **Search-result deep-link.** The filter is ephemeral; URL doesn't reflect it. Refresh clears.
- **A keyboard "move to slot N" shortcut** beyond Ctrl+Arrow nudge. A jump like "Ctrl+G then 3" is rejected as out-of-idiom for a 12-slot bank.

## Design Decisions

| Decision | Resolution |
|---|---|
| Source-cluster button vocabulary | Four buttons → four indicator lamps. STREAMS/PLEX/JELLYFIN/DLNA are not interactive in 3B. AUX stays as its existing `hw-btn`. |
| Lamp visual idiom | Pioneer/Sansui: LED dot (left) + engraved brushed-metal nameplate (right). Three states: dark / dim amber (`--lock-amber`) / bright green (`--vfd`). |
| Lamp state derivation | New narrow `adapters.SourceAvailabilityViewer` interface. Each source adapter implements `SourceID()` + `Configured()`. Chassis snapshot helper walks the registry per tick. |
| DLNA lamp | DLNA adapter exists and implements `IsEnabled()`. Lamp dark when disabled, dim amber when enabled, bright green when actively serving. |
| Catalog drawer entry | Preset-header BROWSE button only. Source-cluster lamps are not click-targets. |
| Catalog data source | `StreamsCatalogViewer.Catalog()` returns a chassis-shaped slice of the three bundled provider IDs → groups → channels. Implementation reads existing `ProviderDefinition` / `ChannelDefinition` / `GroupDefinition` from `streams/assets.go` and local seed/cache data — no new data shape. |
| Catalog cast HTTP shape | New sibling route `POST /receiver/streams/cast` with `provider` + `channel` form fields. Returns `{ok, chip}` matching 3A. Reuses `*adapters.QuickCastError` for typed status/chip propagation. |
| Preset editing HTTP shape | `POST /receiver/preset/star` (idempotent desired star state, race-safe for double-click/multi-tab repeats) and `POST /receiver/preset/move` (swap semantics, 1..12 each). Both return the extended `{ok, chip, slot?, cleared?}` JSON shape. |
| Bank-full behavior | Reject with `409 BANK FULL` chip. User clears a slot manually before retrying. No silent overwrites or FIFO eviction. |
| Preset persistence | `{data_dir}/chassis_presets.json` atomic write via temp file + `os.Rename`. Format: sparse array of `(slot, provider, channel)` triples; display fields re-derived from catalog at load. First-run seeds from existing `bundledChassisPresets`. Stale references drop silently. |
| Adapter interface rename | `PresetViewer.BundledPresets()` → `PresetViewer.Presets()`. The returned list is no longer guaranteed-bundled after edits; the method name reflects that. 3A call sites update. |
| Drag-reorder mechanic | Pointer-based (`pointerdown`/`pointermove`/`pointerup`), not HTML5 native DnD. Native DnD's touch story is too uneven for the chassis's mouse/touch parity standard. |
| Drag-reorder semantics | Swap, not insert/shift. Drag slot 7 onto slot 3 → 7 and 3 trade. Empty slots are valid drop targets. |
| Search/filter | Client-only, no server route. Matches `name` / `badge label` / `channel id` substring (case-insensitive). Applies to preset bank (always) and catalog grid (when drawer open). ESC clears. `:not(.tuned)` exception preserves active-cast visibility through the filter. |
| Search/filter persistence | None. Refresh clears the field. |
| SSE | New `presets` event emitted by the existing cache-diff SSE loop whenever the 12-slot snapshot changes after a successful Star/Move. Carries the full 12-slot array with derived display fields. Existing `transport` event still drives source-cluster/catalog/preset `.tuned` migration. |
| LIT state sharing | Source-cluster STREAMS lamp, `.preset.lit`, and `.ch-card.tuned` all derive from `transport.AdapterRef`. Single source of truth; no three-way divergence. |
| Chip vocabulary additions | `BANK FULL` (409), `BAD SLOT` (400, existing from 3A but also used by move). |
| `BROWSE` button rename behavior | Closed: `"▸ Browse full catalog (N)"`. Open: `"◂ Back to presets"`. Server renders the closed form; JS swaps on toggle. Mirrors mockup [v24:5460-5483](../reference/2026-05-21-receiver-v24.html). |
| Mode label content | Closed: `"Memory · N / 12 slots"` (or `"Memory · drag to reorder · N / 12"` when N ≥ 1). Open: `"Catalog · <provider> · <group> · <channel-count>"`. Server renders closed form; JS swaps on toggle and rail/tab switch. |
| Star is read-from-catalog only | `.ch-card .star` shows ★ if the channel is in any preset slot, ☆ otherwise. Click posts the opposite desired state. The preset bank itself has no edit affordances in 3B. |

## Implementation Checklist (sketch — implementation plan elaborates)

- `internal/adapters/source.go`: add `SourceAvailabilityViewer` interface. (New file rather than appending to `adapter.go` because the interface is unrelated to the existing `LinkAware` / capability set there; keeping discovery-mechanism interfaces in their own files matches the existing `playback.go` / `preset.go` precedent.)
- `internal/adapters/catalog.go`: add `CatalogProvider` / `CatalogGroup` / `CatalogChannel` types, `StreamsCatalogViewer` and `StreamsCaster` interfaces.
- `internal/adapters/preset.go`: rename `PresetViewer.BundledPresets()` → `Presets()`; add `PresetEditor` interface with `SetPresetStarred` and `MovePreset`; add `PresetStarResult` type.
- `internal/adapters/streams/source.go`: implement `SourceAvailabilityViewer` (`SourceID() = "streams"`, `Configured() = IsEnabled()`).
- `internal/adapters/streams/catalog.go`: implement `StreamsCatalogViewer.Catalog()` and `StreamsCaster.CastChannel`; filter the chassis catalog to the bundled/mockup provider IDs; provider badge lookup table in `assets.go`.
- `internal/adapters/streams/preset_store.go`: in-memory slot array with file persistence; initialized from `New`/lazy path so `Presets()` is safe before adapter `Start`; first-run seed from existing `bundledChassisPresets`; atomic write; load-time stale-reference scrubbing.
- `internal/adapters/streams/preset.go`: rename `BundledPresets()` → `Presets()`; add `SetPresetStarred` and `MovePreset` methods backed by the new store.
- `internal/adapters/plex/source.go`, `jellyfin/source.go`, `dlna/source.go`: implement `SourceAvailabilityViewer`.
- `internal/chassis/cast.go`: add three sibling helpers — `writePresetStarSuccess(w, starred bool, slot int, cleared []int)`, `writePresetMoveSuccess(w)`, and `writePresetEditError(w, status int, chip string)`. Existing `writeCastJSON` from 3A stays unchanged so all 3A callers remain stable; the new helpers share an internal `presetEditBody` struct with `omitempty` JSON tags. Split helpers (vs one 7-positional signature) prevent the footgun of passing `slot` and `cleared` on error responses or `starred=true` paths populating `cleared`.
- `internal/chassis/streams_cast.go`: add `POST /receiver/streams/cast` handler.
- `internal/chassis/preset.go`: add `POST /receiver/preset/star` and `POST /receiver/preset/move` handlers; existing 3A handler stays.
- `internal/chassis/server.go`: add `Config.StreamsCatalogViewer`, `Config.StreamsCaster`, `Config.PresetEditor`, `Config.SourceAvailabilityViewers`. Store on `*Server`. Wire two new mux routes. Add `refreshSnapshotNow()` as a tiny helper around `s.cache.Set(s.buildSnapshot(time.Now()))` for successful preset mutations.
- `internal/chassis/data.go`: extend `SourceButton` with `Configured` and `Casting`; add `CatalogData` and sub-types; add `applySourceLampState` and `buildCatalogData` helpers; rewrite source-cluster idle/snapshot blocks.
- `internal/chassis/session.go`: `snapshotFromStatusView` populates `Source` lamp states and `Catalog` via the new helpers.
- `internal/chassis/events.go`: add `presets` envelope + `presetsChanged` diff helper; `handleEvents` emits it from the existing per-connection cache-diff loop.
- `internal/chassis/templates/source-cluster.html`: branch on `Action` — empty → render `.lamp`, non-empty → existing `.hw-btn` (AUX).
- `internal/chassis/templates/preset-bank.html`: render preset slots with reorder data attributes; add `<template id="catalog-tree-<provider>">` blocks holding the full DOM for each provider's rail+grid (one `<template>` per provider; JS clones content on tab switch); un-disable the BROWSE button and search field.
- `internal/chassis/templates/catalog-drawer.html`, `catalog-rail.html`, `catalog-grid.html`: new partials matching the mockup structure.
- `internal/chassis/static/chassis.css`: port mockup catalog rules under `body.receiver` scope; new lamp rules; drag-state rules; search filter rules.
- `internal/chassis/static/source-cluster.js`: subscribe to `transport`, update lamp `.casting` / `.configured-idle` / `.unavailable` classes.
- `internal/chassis/static/catalog-browser.js`: BROWSE toggle, provider tab / rail group switching (client-side, against pre-rendered DOM), `POST /receiver/streams/cast` on channel click, `POST /receiver/preset/star` on star click, `transport` + `presets` SSE subscriptions.
- `internal/chassis/static/preset-bank.js` (extend from 3A): subscribe to `presets` SSE, re-render slots; co-exists with existing transport-driven `.lit` migration.
- `internal/chassis/static/preset-reorder.js`: pointer-drag implementation; Ctrl+Arrow keyboard shortcut.
- `internal/chassis/static/search-filter.js`: live substring filter; re-applies on drawer open / tab switch / `presets` event.
- `cmd/mister-groovy-relay/main.go`: pass streams adapter as `StreamsCatalogViewer`, `StreamsCaster`, and `PresetEditor`; pass each enabled-source-adapter as `SourceAvailabilityViewer`.
- `internal/chassis/import_check_test.go`: forbidden-imports list unchanged (3A already added streams/url/torrent/plex/jellyfin/dlna).
- Tests named in §Testing.

## Wire Contract — HTTP Routes

### `POST /receiver/streams/cast` (catalog channel click)

**Headers:** `Sec-Fetch-Site: same-origin` (enforced by `requireSameOrigin`).

**Body:**

```
Content-Type: application/x-www-form-urlencoded

provider=mtv-rewind&channel=80s
```

**Server logic:**

1. Validate both `provider` and `channel` are non-empty after trimming. Empty → `400 {"ok":false,"chip":"BAD INPUT"}`.
2. If `s.streamsCaster == nil` → `404 {"ok":false,"chip":"NOT FOUND"}`.
3. Call `streamsCaster.CastChannel(ctx, provider, channel)`.
4. On success: `writeCastJSON(w, 200, true, "")`.
5. On error: `var qerr *adapters.QuickCastError; errors.As(err, &qerr)` for status/chip; untyped fallback is `500 / "CAST FAILED"`.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true}` | Cast started; SSE will migrate `.tuned` shortly |
| 400 | `{"ok":false,"chip":"BAD INPUT"}` | Missing provider or channel |
| 403 | (middleware) | Wrong origin |
| 404 | `{"ok":false,"chip":"NOT FOUND"}` | `StreamsCaster` nil OR unknown provider/channel |
| 503 | `{"ok":false,"chip":"NOT READY"}` | Streams disabled or startup snapshot not ready |
| 500 | `{"ok":false,"chip":"CAST FAILED"}` | Untyped error fallback |

### `POST /receiver/preset/star` (catalog star click)

**Headers:** `Sec-Fetch-Site: same-origin`.

**Body:** `provider=mtv-rewind&channel=trl&starred=true`

**Server logic:**

1. Validate `provider`, `channel`, and `starred`. The accepted lexical form for `starred` is **strictly `"true"` or `"false"`** (case-sensitive, matching the documented body); do not use `strconv.ParseBool` (which would also accept `"1"`, `"t"`, `"TRUE"`, etc.). Missing/empty provider or channel, or `starred` not equal to one of the two literal strings → `400 BAD INPUT`. Rationale: clients round-trip from JSON `"starred": true|false` via `strconv.FormatBool`, which always produces `"true"`/`"false"` — accepting only that form keeps the wire deterministic and rejects sloppy clients early.
2. If `s.presetEditor == nil` → `404 NOT FOUND`.
3. Call `presetEditor.SetPresetStarred(ctx, provider, channel, starred)`.
4. Server-side desired-state semantics:
   - `starred=true` and channel already exists in a slot → no-op success, return that slot.
   - `starred=true` and channel is absent → write to the lowest-numbered empty slot, return that slot.
   - `starred=true` and all 12 slots are full → return `*adapters.QuickCastError{Status: 409, Chip: "BANK FULL"}`.
   - `starred=false` and channel exists → clear all matching slots and return `Cleared`.
   - `starred=false` and channel is absent → no-op success, return an empty `Cleared`.
5. On success: `writePresetStarSuccess(w, starred, slot, cleared)`. The helper sets `slot=0`/`cleared=nil` automatically on the inverse path; clients see only the field that applies. JSON tags use `omitempty` so the wire never carries stale fields.
6. On error: `errors.As` extraction same as cast routes; emit via `writePresetEditError(w, status, chip)`. Untyped fallback `500 / "CAST FAILED"`.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"starred":true,"slot":N}` | Channel present in slot N after the request (newly added or already present) |
| 200 | `{"ok":true,"starred":false,"cleared":[N,...]}` | Channel absent after the request (cleared slots, or empty list for no-op) |
| 400 | `{"ok":false,"chip":"BAD INPUT"}` | Missing provider/channel or invalid `starred` |
| 403 | (middleware) | Wrong origin |
| 404 | `{"ok":false,"chip":"NOT FOUND"}` | `PresetEditor` nil OR unknown provider/channel |
| 409 | `{"ok":false,"chip":"BANK FULL"}` | `starred=true` with all 12 slots full and channel not currently starred |
| 500 | `{"ok":false,"chip":"CAST FAILED"}` | Untyped error fallback |

### `POST /receiver/preset/move` (drag reorder)

**Headers:** `Sec-Fetch-Site: same-origin`.

**Body:** `from=7&to=3`

**Server logic:**

1. Parse `from` and `to` as integers in 1..12. Out-of-range or non-integer → `400 BAD SLOT`.
2. If `s.presetEditor == nil` → `404 NOT FOUND`. (404 precedes the `from == to` short-circuit so a connectivity test never gets a misleading `200` from a chassis that has no editor wired.)
3. `from == to` → `200 {"ok":true}` no-op (no persistence write; chassis still calls `refreshSnapshotNow()` for uniformity, but the events-loop diff suppresses the spurious emit).
4. Call `presetEditor.MovePreset(ctx, from, to)` (swap semantics).
5. Call `refreshSnapshotNow()`. Connected `/receiver/events` streams emit `presets` from their normal diff loop when the slot array changes.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true}` | Swap completed (or from == to no-op) |
| 400 | `{"ok":false,"chip":"BAD SLOT"}` | `from`/`to` not in 1..12 |
| 403 | (middleware) | Wrong origin |
| 404 | `{"ok":false,"chip":"NOT FOUND"}` | `PresetEditor` nil |
| 500 | `{"ok":false,"chip":"CAST FAILED"}` | Untyped error fallback |

### Chip Vocabulary additions

| Chip | When |
|---|---|
| `BANK FULL` | Preset star-add attempted with all 12 slots already occupied by other channels (409) |
| `BAD SLOT` | Preset move `from`/`to` out of 1..12 range (400) — already in 3A vocabulary for preset cast; same semantics here |

All other chips (`BAD INPUT`, `NOT FOUND`, `NOT READY`, `CAST FAILED`, etc.) inherit unchanged from 3A.

## Architecture

### Adapter interfaces — `internal/adapters/`

```go
// source.go

// SourceAvailabilityViewer reports whether a source-adapter is
// configured/ready to receive casts. The chassis uses this to drive
// the source-cluster lamp state distinction between "unavailable"
// (lamp dark) and "configured-idle" (lamp dim amber).
//
// Implementations should treat Configured() as a fast in-memory
// check — it's called per chassis snapshot tick. Anything that
// requires I/O should be cached behind an internal field that the
// adapter updates on its own clock.
type SourceAvailabilityViewer interface {
    SourceID() string // "streams" | "plex" | "jellyfin" | "dlna"
    Configured() bool
}

// catalog.go

type CatalogProvider struct {
    ID             string         // "mtv-rewind"
    DisplayName    string         // "MTV Rewind"
    BadgeLabel     string         // "MTV" — small text in .ic glyph
    BadgeClass     string         // "mtv" | "cartoon" | "toonami" — CSS hook
    Live           bool           // whole provider is always-live (direct streams)
    DefaultChannel string         // for the catalog's initial selection
    Groups         []CatalogGroup
}

type CatalogGroup struct {
    ID       string
    Name     string
    Channels []CatalogChannel
}

type CatalogChannel struct {
    ID       string
    Name     string
    PlayMode string // "SEQ" | "SHUFFLE" — uppercased PlayMode for the .meta line
    Live     bool   // channel is live (always true when provider.Live)
}

type StreamsCatalogViewer interface {
    Catalog() []CatalogProvider
}

type StreamsCaster interface {
    CastChannel(ctx context.Context, providerID, channelID string) error
}

// preset.go (extends 3A)

type PresetViewer interface {
    Presets() [12]PresetEntry // renamed from BundledPresets()
}

type PresetEditor interface {
    SetPresetStarred(ctx context.Context, providerID, channelID string, starred bool) (PresetStarResult, error)
    MovePreset(ctx context.Context, from, to int) error
}

// PresetStarResult is the typed return from PresetEditor.SetPresetStarred.
// Zero-value rules (enforced by the chassis JSON helpers, not the type):
//   - Starred=true:  Slot in 1..12, Cleared MUST be nil.
//   - Starred=false: Slot MUST be 0, Cleared MAY be empty (no-op remove) or
//                    populated (one or more slots cleared). Never nil.
// The JSON helpers use omitempty so the wire never carries stale fields
// from the inverse path.
type PresetStarResult struct {
    Starred bool  `json:"starred"`
    Slot    int   `json:"slot,omitempty"`    // 1..12 when Starred; 0 otherwise (omitted)
    Cleared []int `json:"cleared,omitempty"` // populated when !Starred; nil when Starred
}
```

### Streams adapter implementations

**`internal/adapters/streams/source.go`** — `SourceID() = "streams"`, `Configured() = IsEnabled()`. Streams is bundled, but the lamp still goes dark when the operator disables the adapter.

**`internal/adapters/streams/catalog.go`** — `Catalog()` walks the adapter's current definition/catalog snapshot and produces the chassis-shaped slice, filtered to the three bundled/mockup provider IDs in `bundledChassisCatalogProviderIDs`. Remote/cached manifest providers may remain available to URL resolution and `/ui/streams/*`, but they do not appear in the `/receiver` drawer in 3B. Badge metadata lookup table in `assets.go`:

```go
var bundledChassisCatalogProviderIDs = []string{
    "mtv-rewind",
    "cartoon-rewind",
    "toonami-aftermath",
}

var providerBadges = map[string]struct{ Label, Class string }{
    "mtv-rewind":        {"MTV", "mtv"},
    "cartoon-rewind":    {"CART", "cartoon"},
    "toonami-aftermath": {"TOON", "toonami"},
}
```

`CastChannel`:

```go
func (a *Adapter) CastChannel(ctx context.Context, providerID, channelID string) error {
    if !a.IsEnabled() {
        return &adapters.QuickCastError{Status: 503, Chip: "NOT READY",
            Message: "streams adapter is disabled"}
    }
    if err := a.ensureStartupSnapshot(ctx); err != nil {
        return &adapters.QuickCastError{Status: 503, Chip: "NOT READY",
            Message: "streams catalog is not ready", Cause: err}
    }
    res := streamhandoff.Resolution{ProviderID: providerID, ChannelID: channelID}
    if err := a.validatePlayRequest(res); err != nil {
        return &adapters.QuickCastError{Status: 404, Chip: "NOT FOUND",
            Message: err.Error(), Cause: err}
    }
    _, err := a.StartResolvedStream(ctx, res)
    return err
}
```

**Deliberate divergence from `CastPreset` (3A):** `CastPreset` lets `validatePlayRequest` errors fall through as untyped, collapsing to `500 / CAST FAILED` via the chassis fallback. That's correct for the preset path because `preset_test.go` asserts every bundled slot resolves — a `validatePlayRequest` error there indicates an adapter-coding bug, not a user-facing condition. `CastChannel` here intentionally wraps the same error as `404 NOT FOUND` because the input came from a user click on a catalog card, which can legitimately point to a stale or removed channel if the catalog reloaded between page render and click. Keep the two paths divergent — do not normalize one to match the other.

**`internal/adapters/streams/preset_store.go`** — new file. Owns the in-memory 12-slot array, file path, and mutation methods. The store must be initialized from `streams.New` or a `sync.Once` lazy path reached by `Presets()`; it must not depend on adapter `Start`, because `main.go` binds HTTP before starting enabled adapters and disabled adapters may never start. Initialization:

1. Try to read `{data_dir}/chassis_presets.json`.
2. If missing → seed in-memory from the existing `bundledChassisPresets` literal; do not write the file yet (lazy write on first mutation).
3. If present → parse, validate slot numbers, drop entries pointing to unknown providers/channels (logged at debug).
4. Display fields (`Title`, `BadgeLabel`, `BadgeClass`, `Live`) are derived at load by looking up each `(provider, channel)` pair in the existing catalog.

Mutation methods (`SetPresetStarred`, `MovePreset`) lock the store, mutate the in-memory array only when the requested state changes it, write the new state atomically (`os.CreateTemp` in the same directory + `os.Rename`), unlock, return result. Idempotent no-ops (`starred=true` for an already-starred channel, `starred=false` for an absent channel, or `from == to`) skip the file write entirely. The chassis handler still calls `refreshSnapshotNow()` uniformly after a successful adapter return — the no-op suppression happens downstream in the events-loop `presetsChanged` diff (see §SSE), not in the store or handler.

**`internal/adapters/streams/preset.go`** (renamed/extended from 3A):

```go
func (a *Adapter) Presets() [12]adapters.PresetEntry {
    return a.presetStore.Snapshot()
}

func (a *Adapter) SetPresetStarred(ctx context.Context, providerID, channelID string, starred bool) (adapters.PresetStarResult, error) {
    // Validate channel exists in catalog (return *QuickCastError{404, "NOT FOUND"} if not).
    // Delegate to a.presetStore.SetStarred(...).
}

func (a *Adapter) MovePreset(ctx context.Context, from, to int) error {
    // Validate from, to in 1..12 (return *QuickCastError{400, "BAD SLOT"} if not).
    // Delegate to a.presetStore.Move(from, to).
}
```

`CastPreset` (3A) stays unchanged — it reads `a.presetStore.Snapshot()` instead of `bundledChassisPresets[slot-1]` going forward.

### Startup and disabled-adapter behavior

`main.go` binds and serves HTTP before `Adapter.Start(ctx)` runs. Every 3B read path must therefore be safe before startup:

- `StreamsCatalogViewer.Catalog()` returns the current in-memory snapshot. If the snapshot is empty, it performs a local-only bootstrap from bundled definitions plus any valid on-disk cache; it never performs a remote fetch on a chassis render path.
- `PresetViewer.Presets()` always returns a 12-slot array. Store load failures fall back to bundled defaults in memory and log a warning; the bad file is left untouched until the next successful mutation rewrites it.
- `PresetEditor.SetPresetStarred` and `MovePreset` work while Streams is disabled because they only edit local chassis preset state. `StreamsCaster.CastChannel` returns `503 NOT READY` while disabled.
- Chassis `Config.StreamsCatalogViewer` and `Config.PresetEditor` are wired whenever the Streams adapter is registered. `SourceAvailabilityViewer.Configured()` remains the source of truth for whether the STREAMS lamp is dark or amber.

### Per-adapter `SourceAvailabilityViewer` implementations

| Adapter | `SourceID()` | `Configured()` |
|---|---|---|
| `streams` | `"streams"` | `IsEnabled()` |
| `plex` | `"plex"` | `IsEnabled() && IsLinked()` (combine existing methods) |
| `jellyfin` | `"jellyfin"` | `IsEnabled() && IsLinked()` (the Jellyfin adapter exposes a single `IsLinked()` accessor today; per-server visibility is out of scope for the chassis lamp in 3B) |
| `dlna` | `"dlna"` | `IsEnabled()` (no linking concept; SSDP discovery is passive) |

Each implementation is ~10 lines; the interface methods become small thin wrappers over existing adapter accessors. Where an adapter doesn't have an `IsLinked()` or equivalent today, the implementer adds one as a single method exposing existing internal state — no new state is introduced.

### Chassis `Config` additions — `internal/chassis/server.go`

```go
type Config struct {
    // ... existing 3A fields ...

    // StreamsCatalogViewer / StreamsCaster: the streams adapter wired
    // through these so the chassis can render the catalog drawer and
    // fire channel casts without importing internal/adapters/streams
    // directly.
    StreamsCatalogViewer adapters.StreamsCatalogViewer
    StreamsCaster        adapters.StreamsCaster

    // PresetEditor: streams adapter again, for star-toggle and move
    // operations. PresetViewer (existing 3A field) is unchanged in
    // type but its concrete method name changes from BundledPresets
    // to Presets.
    PresetEditor adapters.PresetEditor

    // SourceAvailabilityViewers: every adapter that implements the
    // interface, in registration order. main.go assembles the slice
    // from the registry. The chassis does NOT inspect the registry
    // directly for source-lamp state — passing the typed slice keeps
    // import_check_test.go happy.
    SourceAvailabilityViewers []adapters.SourceAvailabilityViewer
}
```

### Chassis data model — `internal/chassis/data.go`

**`SourceButton` extends:**

```go
type SourceButton struct {
    Label       string
    Active      bool   // unchanged — AUX
    Lit         bool   // unchanged — AUX
    Unavailable bool   // unchanged — AUX
    Action      string // unchanged
    InputID     string // unchanged — AUX

    Configured bool // new: adapter present & linked/enabled (non-AUX slots)
    Casting    bool // new: this source is the active cast (non-AUX slots)
}
```

The template branches on `Action`: empty → render `.lamp` (uses `Configured`/`Casting`); non-empty → render `.hw-btn` (existing AUX path, untouched).

**`CatalogData` new top-level field on `ReceiverPageData`:**

```go
type CatalogData struct {
    Open             bool                   // body.browse-open class hook (always false on server-render; JS flips)
    Providers        []CatalogProviderTab   // chassis-shaped, ready for templates
    ActiveProviderID string                 // initial active tab
    ActiveGroupID    string                 // initial active rail group
    TunedProviderID  string                 // from transport.AdapterRef
    TunedChannelID   string                 // from transport.AdapterRef
    PresetMembership map[string]int         // "provider:channel" → slot 1..12
    TotalChannels    int                    // sum across all providers, for "Browse full catalog (N)" label
}

type CatalogProviderTab struct {
    ID, DisplayName, BadgeLabel, BadgeClass string
    Live    bool
    ChCount int                       // total across all groups in this provider
    Groups  []CatalogGroupTab
}

type CatalogGroupTab struct {
    ID, Name string
    ChCount  int
    Channels []CatalogChannelCard
}

type CatalogChannelCard struct {
    ID, Name, PlayMode string
    Live       bool
    Tuned      bool // matches transport.AdapterRef
    Starred    bool // is in a preset slot
    PresetSlot int  // 0 if not starred; 1..12 otherwise (drives tooltip)
}
```

`PresetSlot` extends 3A — the same `[12]PresetEntry` returned by `PresetViewer.Presets()` populates the snapshot's preset bank AND `CatalogData.PresetMembership`. Single derivation per snapshot tick.

### Snapshot wiring

Two new helpers, called from both `idleSnapshot` and `snapshotFromStatusView`:

```go
// applySourceLampState fills the four non-AUX source slots' Configured
// and Casting fields. AUX (Action != "") is left to applyAUXSourceState.
func applySourceLampState(base *ReceiverPageData, viewers []adapters.SourceAvailabilityViewer, adapterRef string)

// buildCatalogData produces the CatalogData struct, including resolved
// .Tuned and .Starred flags. Callers resolve the adapter interfaces
// first so this helper stays pure.
func buildCatalogData(catalog []adapters.CatalogProvider, presets [12]adapters.PresetEntry, adapterRef string) CatalogData
```

Both helpers are pure from the chassis's perspective — no chassis-side mutex, no I/O. The adapter-side `Configured()` / `Catalog()` / `Presets()` calls may take short adapter-internal mutexes (e.g., `dlna.IsEnabled()` and `jellyfin.IsEnabled()` lock their per-adapter mutex briefly); those are bounded and uncontended. `idleSnapshot` / `snapshotFromStatusView` call the adapter interfaces first, then pass already-resolved values into `applySourceLampState` and `buildCatalogData`, so the helpers themselves stay allocation-free and lock-free.

### Templates

#### `internal/chassis/templates/source-cluster.html`

Branches on `Action`:

```html
{{define "source-cluster"}}
{{htmlComment "chassis:source-cluster"}}
<div class="source-cluster" role="group" aria-label="Media source">
  {{range .Buttons}}
  {{- if eq .Action "" -}}
    <div class="lamp{{if .Configured}} configured-idle{{end}}{{if .Casting}} casting{{end}}"
         data-source-id="{{lower .Label}}"
         role="status"
         {{- /* No leading space inside the if-blocks to avoid screen-reader double-space ("STREAMS  currently casting"). */ -}}
         aria-label="{{.Label}}{{if .Casting}}, currently casting{{else if .Configured}}, ready{{else}}, not configured{{end}}"
         title="{{.Label}} - {{if .Casting}}currently casting{{else if .Configured}}linked, idle{{else}}not configured{{end}}">
      <span class="led" aria-hidden="true"></span>
      <span class="name">{{.Label}}</span>
    </div>
  {{- else -}}
    <!-- existing AUX hw-btn rendering, unchanged -->
    <button class="hw-btn{{if .Active}} active{{end}}{{if .Lit}} lit{{end}}" ...>{{.Label}}</button>
  {{- end -}}
  {{end}}
</div>
{{end}}
```

A new `lower` helper is added to the chassis FuncMap (one-liner).

#### `internal/chassis/templates/preset-bank.html`

Extended from 3A. Each preset slot grows `data-slot`/`data-provider`/`data-channel` attributes (data-slot already exists in 3A; the others are new). The BROWSE button drops `disabled` and `aria-disabled`. The search input drops `disabled` and `readonly`. The mode label and BROWSE button text render their closed-state form server-side.

Catalog tree pre-rendering: the preset-bank template emits one `<template id="catalog-tree-<provider-id>">` per provider holding that provider's full rail+grid DOM. Using HTML `<template>` (not `display: none`) means the inert nodes don't appear in the active document tree — `.content` provides a `DocumentFragment` that JS clones on tab switch into `.catalog-rail` and `.catalog-grid`.

**Payload size:** roughly estimated at ~6KB pre-gzip across all three providers (~76 channels × ~80 bytes per rendered `.ch-card` markup, plus ~14 `.catalog-rail-group` buttons), ~2KB after gzip. Re-measure before merge — if pre-gzip exceeds 12KB, revisit whether per-tab-click AJAX (`/receiver/catalog/<provider>`) is a better tradeoff. Acceptable as drafted; verify with actual rendered template output during implementation.

The drawer's initial active provider's tree is server-rendered directly into the visible rail+grid containers (no clone needed on first paint).

#### `internal/chassis/templates/catalog-drawer.html` (new)

Matches the mockup [v24:3879-3902](../reference/2026-05-21-receiver-v24.html#L3879-L3902) structure:

```html
{{define "catalog-drawer"}}
{{htmlComment "chassis:catalog-drawer"}}
<div class="catalog-drawer" id="catalog-drawer" aria-hidden="true">
  <div class="catalog-browser">
    <div class="catalog-provider-tabs">
      <span class="catalog-tab-indicator" id="catalog-tab-indicator"></span>
      {{range .Providers}}
      <button class="catalog-provider-tab{{if eq .ID $.ActiveProviderID}} active{{end}}"
              data-provider="{{.ID}}">
        <span class="ic {{.BadgeClass}}">{{.BadgeLabel}}</span>
        {{.DisplayName}}
        <span class="ch-count">{{.ChCount}}</span>
      </button>
      {{end}}
    </div>
    <div class="catalog-body">
      <div class="catalog-rail" id="catalog-rail">
        {{template "catalog-rail" .}}
      </div>
      <div class="catalog-grid" id="catalog-grid">
        {{template "catalog-grid" .}}
      </div>
    </div>
  </div>
</div>
{{end}}
```

#### `internal/chassis/templates/catalog-rail.html` (new)

Renders the rail group buttons for the active provider:

```html
{{define "catalog-rail"}}
{{- $active := .ActiveGroupID -}}
{{- range $i, $g := (index .Providers (.ProviderIndex .ActiveProviderID)).Groups -}}
<button class="catalog-rail-group{{if eq $g.ID $active}} active{{end}}"
        data-group="{{$g.ID}}" style="--i:{{$i}}">
  {{$g.Name}}<span class="count">{{$g.ChCount}}</span>
</button>
{{- end -}}
{{end}}
```

(`ProviderIndex` is a helper method on `CatalogData` that returns the slice index of a provider by ID. **Fallback contract:** when `ActiveProviderID` doesn't match any provider in `Providers` (shouldn't happen, but defense-in-depth against catalog reload mid-page-render), returns `0` so `index .Providers 0` always succeeds. `GroupIndex` follows the same pattern. The chassis snapshot helper guarantees `ActiveProviderID` is always set to a real provider ID at snapshot build time, so the fallback never fires in practice — but it prevents a template `panic` if invariants ever drift.)

#### `internal/chassis/templates/catalog-grid.html` (new)

Renders the channel cards for the active provider+group:

```html
{{define "catalog-grid"}}
{{- $cd := . -}}
{{- $p := index .Providers (.ProviderIndex .ActiveProviderID) -}}
{{- $g := index $p.Groups (.GroupIndex .ActiveProviderID .ActiveGroupID) -}}
{{- range $i, $c := $g.Channels -}}
<div role="button" tabindex="0"
     class="ch-card{{if $c.Tuned}} tuned{{end}}{{if $c.Starred}} starred{{end}}{{if $c.Live}} live{{end}}"
     data-provider="{{$p.ID}}" data-channel="{{$c.ID}}" style="--i:{{$i}}">
  <button class="star" type="button"
          title="{{if $c.Starred}}In preset {{pad2 $c.PresetSlot}}{{else}}Save to preset{{end}}">{{if $c.Starred}}★{{else}}☆{{end}}</button>
  <div class="name">{{$c.Name}}</div>
  <div class="meta"><span>{{upper $c.ID}}</span><span class="mode">{{$c.PlayMode}}</span></div>
</div>
{{- end -}}
{{end}}
```

Each tab/group switch in JS re-renders these by reading from the pre-rendered hidden DOM region; no server round-trip.

**Stagger-in `--i` semantics:** `{{$i}}` is the within-group channel index (`range $i, $c := $g.Channels`). The stagger-in animation restarts per group switch (slot 0 fades in first, slot 1 next, etc.) — this is intentional. The mockup's "feels like the grid is scanning in each station as the catalog locks" effect is most legible on every group/tab change, not just on initial page render. Group-relative indexing matches that intent.

### Client JS

#### `internal/chassis/static/source-cluster.js` (new, ~40 lines)

Subscribes to the `transport` SSE event. On each tick, reads `transport.adapterRef`, derives the active source (`streams` / `plex` / `jellyfin` / `dlna` / `""`), updates `.casting` class on the matching `.source-cluster .lamp` and clears it from siblings. The `.configured-idle` class is initial-rendered server-side and not migrated client-side (linkage state changes are rare; require a chassis page reload to update — documented as a known minor gap).

No POST handlers, no click handlers. The four lamps are non-interactive.

#### `internal/chassis/static/catalog-browser.js` (new, ~150 lines)

Responsibilities:
1. **BROWSE toggle.** Click `#browse-toggle` → flip `body.browse-open`, swap button text (closed: `▸ Browse full catalog (N)` / open: `◂ Back to presets`), swap mode-label text, trigger `body.catalog-scanning` for 600ms (VFD scan animation per mockup [v24:2507-2532](../reference/2026-05-21-receiver-v24.html#L2507-L2532)), position `.catalog-tab-indicator`.
2. **Provider tab switching.** Click `.catalog-provider-tab` → set `.active`, swap the rail and grid contents from pre-rendered hidden DOM, update `activeProviderID` / `activeGroupID`, reposition the tab indicator, re-derive `.tuned` / `.starred` from current SSE state.
3. **Rail group switching.** Click `.catalog-rail-group` → swap grid contents to that group's channels.
4. **Channel cast.** Click `.ch-card` (not on `.star`) → `POST /receiver/streams/cast` with `provider`+`channel`. JSON response; on error, route the chip text through the preset-header status surface.
5. **Star toggle.** Click `.ch-card .star` → read current `.starred`, post `POST /receiver/preset/star` with `provider`, `channel`, and the opposite `starred` desired state. Add a transient `.pending` class while the request is in flight, but do not flip `.starred` until a successful JSON response or the next `presets` SSE diff. On non-OK/network failure, clear `.pending`, leave the old state in place, and route the chip text through the preset-header status surface. The star's `click` and `keydown` handlers must call `event.stopPropagation()` so star activation does NOT also fire the parent `.ch-card`'s cast handler. Same applies to Space/Enter on the star button (the `<button>` natively fires `click` on those keys; stopPropagation covers both).
6. **`transport` SSE subscription.** Migrate `.ch-card.tuned` across all rendered provider trees on each tick.
7. **`presets` SSE subscription.** Re-derive `.ch-card.starred` across all rendered provider trees from the slot data.
8. **Keyboard.** Enter / Space on a focused `.ch-card` triggers the cast (already partially in the mockup [v24:5413-5418](../reference/2026-05-21-receiver-v24.html#L5413-L5418)).

#### `internal/chassis/static/preset-bank.js` (extend from 3A, +~30 lines)

Adds: subscribe to the new `presets` SSE event. On each tick, re-render slot DOM content (name, badge label, badge class, `.empty` / `.live` / `.lit` classes) from the slot array. The slot's `data-provider` / `data-channel` attributes update too — this is what makes `transport`-driven `.lit` migration continue to work after a slot's content changes.

#### `internal/chassis/static/preset-reorder.js` (new, ~110 lines)

Pointer-based drag implementation:

1. `pointerdown` on `.preset:not(.empty)` → record source slot, prevent default scroll.
2. `pointermove` past 5px → enter drag mode. Clone source slot to `position: fixed`, hide source via opacity, set `cursor: grabbing`.
3. `pointermove` thereafter → reposition clone; use `document.elementFromPoint(x, y).closest('.preset')` to detect hover target. Apply `.drop-target` class to the matching slot, clear from siblings.
4. `pointerup` → if hovered target exists and differs from source: optimistically swap the source and target slot DOM positions locally (so the user sees the result instantly without waiting for SSE round-trip), then `POST /receiver/preset/move` with `from`/`to`. The clone is removed at this point regardless of fetch state. On HTTP error or network failure: revert the optimistic swap (restore the original DOM positions), surface the chip via the preset-header status surface. On success: the next `presets` SSE event arrives and re-derives the slot DOM, which should be a no-op since the optimistic state already matches; if it differs (e.g., server-side state drifted from another tab's mutation), the SSE-derived state wins.
5. `pointercancel` / `keydown Escape` → cancel; spring clone back to source.
6. `keydown` on focused `.preset:not(.empty)`: `Ctrl+ArrowLeft` → `POST move {from: this, to: this-1}` (wrap to 12 at slot 1). `Ctrl+ArrowRight` → `to: this+1` (wrap to 1 at slot 12). **Filled→empty wrap is allowed** (swap semantics make this a clean move: source slot becomes empty, target slot gains the content). The `:not(.empty)` gate only restricts the *initial focus* — once a filled slot is focused and the user hits Ctrl+Arrow, the destination's empty/filled state doesn't matter.

CSS uses `transition: transform 200ms ease-out` on `.preset`; the swap animation falls out naturally from the SSE re-render.

#### `internal/chassis/static/search-filter.js` (new, ~60 lines)

1. Attach `input` listener to `#search-input`. On change, normalize the query (`.toLowerCase().trim()`).
2. Walk `.preset-bank .preset` and `.catalog-grid .ch-card` (when drawer is open). For each, match against `name` / `badge label` (preset) or `name` / `channel-id` / provider display name (catalog card). Add/remove `.filter-miss`; never apply inline `opacity` or `pointer-events`.
3. CSS applies the dimming/disabled treatment to `.filter-miss:not(.tuned)` so the active-cast stays visible regardless of filter state.
4. Update `#search-scope` chip text with match counts (presets: `N`, catalog: `M`).
5. Add `.has-value` class to `#search-field` when query is non-empty.
6. `keydown Escape` → clear input, re-apply filter (showing everything).
7. Re-apply hooks: `presets` SSE event, drawer open, provider tab switch, group switch. Each calls `applyFilter()`.

### CSS — `internal/chassis/static/chassis.css`

All new rules scoped under `body.receiver` per the chassis scope-isolation invariant. Section ordering in the stylesheet:

- **Source-cluster lamps** (new section, sits where the existing source-cluster button rules are): `.lamp` flex container, `.lamp .led` 8px circle, three state variants (`.configured-idle .led` amber, `.casting .led` cyan/green with full glow, default = dark glass with inset), `.lamp .name` brushed-metal engraved text with state-dependent color.
- **Catalog drawer** (port verbatim from mockup [v24:2190-2502](../reference/2026-05-21-receiver-v24.html#L2190-L2502) with `body.receiver` prefix per the scope-isolation pass): `.catalog-drawer` collapsible container, `.catalog-browser` recessed inner frame, `.catalog-provider-tabs`, `.catalog-tab-indicator` sliding amber bar, `.catalog-rail`, `.catalog-grid`, `.ch-card` + `.star` + `.live` + `.tuned` + `.starred` variants.
- **Catalog-scan VFD animation** (port from mockup [v24:2504-2532](../reference/2026-05-21-receiver-v24.html#L2504-L2532)): `body.catalog-scanning .vfd::after` overlay + `scan-blink` keyframe.
- **Stagger-in animations** (port from mockup [v24:2481-2502](../reference/2026-05-21-receiver-v24.html#L2481-L2502)): `@keyframes ch-card-in`, `@keyframes rail-in`, applied via the `--i` CSS variable each template renders on the item.
- **Drag-reorder visuals** (new): `.preset[data-dragging="source"]` opacity + scale; `.preset.drop-target` amber border glow; `.preset-drag-clone` body-attached fixed-position element.
- **Search filter visuals** (port from mockup [v24:5493-5520](../reference/2026-05-21-receiver-v24.html#L5493-L5520) but stripped to CSS — the JS only toggles classes): `.search-field.has-value` "active filter" treatment plus `.filter-miss:not(.tuned)` dimming / pointer-disable rules.

### SSE — new `presets` event

Emitted by `handleEvents` whenever the cached snapshot's 12-slot preset array differs from the last array emitted to a given connection. Successful `SetPresetStarred` or `MovePreset` handlers call a small `refreshSnapshotNow()` helper after the adapter returns so the shared cache reflects the mutation immediately; connected SSE clients still receive the event through the existing per-connection diff loop.

**Initial burst position:** insert the `presets` envelope into `handleEvents`' initial-burst sequence **between `meter` and `audio`**. Today's canonical order in [events.go](../../../internal/chassis/events.go) is `retry → state → vfd → source → visualizer → transport → volume → meter → audio`; presets logically follows meter (both are aggregate UI state, lower-cadence than the play-state events) and precedes audio (which is the highest-cadence event and runs last). A new local `lastPresets [12]adapters.PresetEntry` variable in the events goroutine holds the previous emit for the diff arm.

**Diff semantics — `presetsChanged(prev, next [12]adapters.PresetEntry) bool`:** compares only the **persistent (slot, provider, channel) triples**, NOT the derived display fields (`title`, `badgeLabel`, `badgeClass`, `live`). Rationale: a catalog reload could legitimately change the derived display of an unchanged slot (e.g., a channel rename in `assets.go` after a code update). Treating that as a `presets` mutation would emit a spurious event with no user-driven slot change. The persistent-only comparison guarantees one-to-one correspondence between "user action → persistence write → SSE event."

**No-op `refreshSnapshotNow()` behavior:** the helper is **always** called after a successful `SetPresetStarred` or `MovePreset` adapter return, regardless of whether the store mutated. This keeps the handler logic uniform. The events-loop diff is the single suppression point — when the user posts `starred=true` for a channel that's already starred, the store no-ops, `refreshSnapshotNow()` rebuilds an unchanged snapshot, the next events tick computes `presetsChanged(prev, next) == false`, and no SSE frame is sent. The cost of one redundant snapshot rebuild per no-op is negligible (snapshot build is in-memory, sub-millisecond).

Payload:

```json
{
  "slots": [
    {"slot": 1, "provider": "mtv-rewind", "channel": "1stday",
     "title": "First Day on MTV", "badgeLabel": "MTV REWIND",
     "badgeClass": "mtv", "live": false},
    {"slot": 2, "provider": "", "channel": "", "title": "", ...},
    ...
  ]
}
```

12 entries always; empty slots have `provider: ""`. Display fields are derived server-side so the client doesn't need a catalog lookup table.

**Why a new event vs reusing `transport`:** the existing `transport` event fires on every play-state tick (sub-second cadence in live sessions). A dedicated `presets` event fires only when the preset slot array changes — bandwidth saved on the cast-state path.

**Server emission point:** no broadcast hub is introduced in 3B. Add `presetEnvelopeFromSnapshot`, `presetsChanged`, and `emit(w, "presets", ...)` branches to `handleEvents`, matching the existing `source` / `transport` diff style. The new local-state variable in the events goroutine is `lastPresets [12]adapters.PresetEntry` (zero value at goroutine start; the initial burst seeds it).

**Initial state:** the page-load snapshot already contains the rendered preset bank and catalog `.starred` state. `handleEvents` also emits an initial `presets` event in the opening SSE burst, so late-loading JS can hydrate from SSE without scraping DOM. Subsequent `presets` events arrive only when the slot array changes.

### Persistence — `{data_dir}/chassis_presets.json`

```json
{
  "version": 1,
  "slots": [
    {"slot": 1,  "provider": "mtv-rewind",        "channel": "1stday"},
    {"slot": 2,  "provider": "mtv-rewind",        "channel": "80s"},
    {"slot": 3,  "provider": "mtv-rewind",        "channel": "90s"},
    {"slot": 4,  "provider": "mtv-rewind",        "channel": "trl"},
    {"slot": 5,  "provider": "mtv-rewind",        "channel": "120minutes"},
    {"slot": 6,  "provider": "mtv-rewind",        "channel": "unplugged"},
    {"slot": 7,  "provider": "cartoon-rewind",    "channel": "loonytunes"},
    {"slot": 8,  "provider": "cartoon-rewind",    "channel": "animaniacs"},
    {"slot": 9,  "provider": "cartoon-rewind",    "channel": "heman"},
    {"slot": 10, "provider": "cartoon-rewind",    "channel": "all"},
    {"slot": 11, "provider": "toonami-aftermath", "channel": "east"},
    {"slot": 12, "provider": "toonami-aftermath", "channel": "movies"}
  ]
}
```

- Empty slots: absent from the array (not `null`-padded).
- Atomic write: temp file in same directory, `os.Rename`. Same pattern as `device_uuid` and `plex.tv` token.
- Load failure (parse error, version mismatch) → adapter logs warning, falls back to in-memory bundled defaults, leaves file alone (next mutation rewrites cleanly).
- Stale references (channel removed from `assets.go`) drop at load. **Log level: `info`** when at least one entry is dropped, with a single summary line like `chassis_presets: dropped 3 stale references on startup (mtv-rewind:olduri, ...)`. Info-level (not debug) so operators who hand-edit `chassis_presets.json` see when their entries were silently rejected. Next mutation persists the cleaned array.
- First-run: file is absent → in-memory seed from `bundledChassisPresets` literal in `streams/assets.go` → file written on first mutation only.

### Mockup divergences (called out explicitly)

| Mockup behavior | Real chassis | Why |
|---|---|---|
| Catalog data hardcoded in JS | Server-rendered from `StreamsCatalogViewer.Catalog()` | Single source of truth; the streams adapter owns provider/channel data. |
| Star tooltip on unstarred channels: "Save to preset" | Same in 3B | Mockup's mockup-only star-toggle is now real. |
| Star click on filled-bank → silently overwrites (mockup) | Star click on filled-bank → `409 BANK FULL` chip | Mockup didn't think about overflow; spec rejects per user decision. |
| Provider/group switching re-renders cards from a JS object | Same UX, but cards live in pre-rendered hidden DOM | Avoids a roundtrip on every tab switch. |
| Search field placeholder: `FILTER PRESETS · CATALOG` | Same | Wiring this up enables the placeholder behavior. |
| `.ch-card.live::after` red pulse dot | Same | Port the CSS. |
| `body.catalog-scanning` 600ms VFD overlay | Same | Port the CSS + 600ms `setTimeout` in JS. |
| `castTo()` updates VFD title text directly | Real chassis lets `transport` SSE drive the VFD | Mockup is a mockup; the real path goes through core.Manager → SSE. |
| Mockup applies search-miss styling via inline JS (`element.style.opacity = '0.22'; element.style.pointerEvents = 'none'`) | Real chassis toggles a `.filter-miss` class; CSS handles the styling via `.filter-miss:not(.tuned)` | Separates concerns — JS only manages set membership; CSS owns visual treatment. The `:not(.tuned)` exception is a pure CSS selector concern. |
| Mockup channel-cast click flashes the VFD via `vfd-cast-ack` class flip in `castTo()` | Real chassis lets the `transport` SSE event drive VFD updates including the `cast-ack` brightness flash | Same single-source-of-truth principle as the mockup row above. |

## Testing

### Layer 1 — Unit (per-package)

- **`internal/adapters/streams/source_test.go`**: `SourceID()` returns `"streams"`; `Configured()` tracks `IsEnabled()` true/false.
- **`internal/adapters/streams/catalog_test.go`**: `Catalog()` returns the three bundled providers in `bundledManifest()` order even when remote/cached manifest providers exist; each provider has correct `BadgeLabel`/`BadgeClass`; toonami-aftermath has `Live: true` and all its channels have `Live: true`; mtv-rewind has `Live: false` and its channels too; group ordering preserved; channel `PlayMode` strings uppercased; `Catalog()` before `Start` returns a non-empty bundled/seed snapshot and does not fetch the network. **Integrity test:** assert `len(bundledChassisCatalogProviderIDs) == 3` AND every entry resolves to a real provider in `bundledManifest().Providers` AND every entry has a matching `providerBadges` row. Prevents silent drift when `assets.go` adds a new bundled provider without updating the chassis exposure list (or vice versa).
- **`internal/adapters/streams/catalog_test.go`**: `CastChannel(ctx, valid)` reaches `StartResolvedStream` via fake core; disabled streams returns `*QuickCastError{503, "NOT READY"}`; `CastChannel(ctx, "nonexistent", "x")` returns `*QuickCastError{404, "NOT FOUND"}`; pre-startup snapshot failure returns `*QuickCastError{503, "NOT READY"}`.
- **`internal/adapters/streams/preset_store_test.go`**: load-from-file happy path; load-with-missing-file seeds from `bundledChassisPresets`; load-with-stale-channel drops the entry and logs; load-with-parse-error falls back to defaults; `SetStarred(true)` add path finds first empty slot; `SetStarred(true)` for an existing channel is a no-op returning the current slot; `SetStarred(false)` remove path clears all matching; `SetStarred(false)` for an absent channel is a no-op; `SetStarred(true)` on full bank with new channel returns 409 BANK FULL; changed star operations write file atomically (verify temp file pattern + rename); `Move` swaps two slots and writes; `Move(from, from)` is a no-op; `Move` out-of-range returns 400 BAD SLOT.
- **`internal/adapters/streams/preset_test.go`** (extend 3A): `Presets()` returns from the store (was: from the literal); `SetPresetStarred` and `MovePreset` delegate to the store.
- **`internal/adapters/plex/source_test.go`**, **`jellyfin/source_test.go`**, **`dlna/source_test.go`**: each `SourceID()` returns the expected string; `Configured()` returns the expected logic-AND of `IsEnabled()` and link status.
- **`internal/adapters/preset_test.go`**: `PresetStarResult` JSON encoding; `QuickCastError` extraction round-trips through `errors.As` for new chips (`BANK FULL`).
- **`internal/chassis/cast_test.go`** (extend 3A): each of the three new helpers — `writePresetStarSuccess`, `writePresetMoveSuccess`, `writePresetEditError` — emits the correct JSON shape; `omitempty` rules are enforced (success-with-starred-slot has no `cleared` key; success-with-unstarred-cleared has no `slot` key; errors have neither). Round-trip via `encoding/json` to confirm the wire format.
- **`internal/chassis/streams_cast_test.go`** (new): handler success / 400 / 403 / 404 / 503 / 500 paths; same-origin guard; typed-error extraction.
- **`internal/chassis/preset_test.go`** (extend 3A): `POST /receiver/preset/star` handler with fake `PresetEditor`; covers add, existing no-op, remove, absent no-op, BANK FULL, invalid `starred` field (specifically: empty string, missing entirely, and `starred=1`/`t`/`TRUE` — all of which must return 400 per the strict-lexical rule), and same-origin; `POST /receiver/preset/move` happy + BAD SLOT + 404 + same-origin; **404 precedes from==to no-op** (a chassis with nil `PresetEditor` returns 404 even for `from=1&to=1`).
- **`internal/chassis/data_test.go`**: `applySourceLampState` derives `Configured` + `Casting` correctly given a fake registry; `buildCatalogData` populates `PresetMembership` correctly given fake `PresetViewer`; `.tuned` matches `transport.AdapterRef` parsed segments.

### Layer 2 — Template & CSS

- **`internal/chassis/chassis_test.go`** (extend):
  - `source-cluster.html` renders `.lamp` for empty `Action`; renders `.hw-btn` for non-empty Action (AUX). 3-state classes apply correctly.
  - `catalog-drawer.html` renders all three provider tabs with badge metadata.
  - `catalog-rail.html` renders one button per group in the active provider, with `--i` index.
  - `catalog-grid.html` renders one card per channel, with star state matching `PresetMembership`.
  - `preset-bank.html` un-disables BROWSE and `#search-input`; renders `data-provider`/`data-channel`/`data-slot` on filled slots.
  - `shell.html` loads all four new JS files in correct order (chassis.js → vfd-live.js → source-cluster.js → preset-bank.js → catalog-browser.js → preset-reorder.js → search-filter.js).
- `TestChassisCSS_AllSelectorsScoped` (existing) covers new rules.
- No-fake-values JS lint: new JS files must not contain `Math.random`/`Math.sin`/etc.

### Layer 3 — Integration

- **`tests/integration/chassis_test.go`** (extend):
  - `POST /receiver/streams/cast` with valid provider+channel reaches a fake `StreamsCaster.CastChannel`.
  - `POST /receiver/preset/star` add path: empty bank → channel lands in slot 1; repeated add is a no-op; full bank with new channel → 409 BANK FULL; repeated remove is a no-op.
  - `POST /receiver/preset/move` swaps slots; subsequent `Presets()` call reflects swap.
  - SSE `presets` initial burst contains the slot array AND arrives **between `meter` and `audio`** in the canonical event order (assert position via SSE frame ordering, not just presence). A changed `SetPresetStarred` or `MovePreset` emits a follow-up `presets` event through the cache-diff loop; idempotent no-ops do not emit a follow-up event.
  - Browser/DOM behavior test: catalog tab cloning preserves star/tuned state, star request failure leaves the old star state visible, search filtering uses `.filter-miss:not(.tuned)`, and pointer reorder posts `from`/`to`.
  - Page reload mid-cast shows correct `.tuned` on source-cluster STREAMS lamp + matching preset slot + matching catalog card (all three surfaces from one snapshot).
  - Star toggle persists across simulated bridge restart: write file, restart fake bridge, verify slots loaded.
  - Stale reference handling: pre-seed file with `(provider="gone", channel="x")` slot, restart, verify slot renders empty (no crash).

### Layer 4 — Manual smoke

- Open `/receiver`; observe four source-cluster lamps in default states (STREAMS amber, Plex/Jellyfin/DLNA dark unless linked).
- Cast a Streams channel via input row → STREAMS lamp lights green, matching preset slot lights cyan, matching catalog card glows cyan (open drawer to verify).
- Click BROWSE → drawer opens with catalog-scan VFD flash; provider tabs/rail/grid all populated; first provider's first group's channels visible.
- Switch provider tab → rail and grid update; tab indicator slides.
- Click a channel card → cast starts; LIT migrates across all three surfaces.
- Click a star on an unstarred channel → starred; preset bank updates; PRESETS slot count increments.
- Click the same star again → unstarred; preset bank slot becomes empty.
- Fill all 12 slots; try to star a 13th → BANK FULL chip in preset header.
- Drag preset slot 1 onto slot 7 → swap; preset bank reflects swap; persistence file rewritten.
- Type in search field → preset bank and catalog dim non-matches; scope chip updates count; ESC clears.
- **`.tuned` exception:** while a channel is actively casting, type a search query that does NOT match the casting channel's name — confirm the casting card stays at full opacity (not dimmed) per the `.filter-miss:not(.tuned)` rule. Then clear the query; everything returns to normal.
- Refresh page → search field empty (no persistence), preset bank shows persisted state from file.

## Migration

None for users. 3B is purely additive under `/receiver/*`. `/ui/playback/quick-cast`, `/ui/streams/*`, and all 3A routes (`/receiver/cast`, `/receiver/preset/{slot}/cast`) stay untouched.

For developers: any code that called `PresetViewer.BundledPresets()` updates to `Presets()`. Call sites at the time of this spec (verified via grep — recount before implementation):

- Interface declaration in `internal/adapters/preset.go`
- Streams implementation in `internal/adapters/streams/preset.go`
- Chassis consumer in `internal/chassis/data.go`
- Streams unit tests in `internal/adapters/streams/preset_test.go` (multiple)
- Chassis handler tests in `internal/chassis/chassis_test.go` (fake `PresetViewer`)
- Integration test stub in `tests/integration/chassis_test.go`

Roughly 9 lines across 6 files. The rename is a single mechanical pass; no semantic change to the contract.

## Forward Compatibility

- **Phase 3D** can wire slot rename, restore-defaults UI, and settings-drawer integration on top of this spec's persistence and SSE plumbing. No data model change needed beyond adding an optional `title_override` field to the persisted slot entry.
- **Plex/Jellyfin library browsing** (future phase) plugs into the same drawer shape: each adapter gets its own `LibraryViewer` interface returning the same `CatalogProvider` slice type. The drawer's provider-tab strip extends; everything below it is reused.
- **Phase 5 "history row"** is unrelated to the preset bank and does NOT consume the `presets` SSE event. (The earlier draft of this spec implied a coupling that doesn't exist — history rows are a separate "recent casts" surface that consume `transport` SSE for their `.last-cast` marker.) The `presets` event's only consumers are the preset bank, the catalog grid's `.starred` state, and the search-filter scope counter.

## Notes for the Implementer

- **The catalog data ALREADY exists** in `internal/adapters/streams/provider.go` as `ProviderDefinition.Groups[]` and `ChannelDefinition.GroupID`. The `bundledManifest()` populates the three 3B chassis providers. Do not invent a parallel data model in the chassis layer — `StreamsCatalogViewer.Catalog()` is a thin shape-shifter from existing data into the chassis-shaped `[]CatalogProvider`, filtered to `bundledChassisCatalogProviderIDs`.
- **The mockup's "live" annotation** is provider-type-derived: `directStreamsProviderType` providers (Toonami Aftermath) are always-live; `youtubeChannelJSONProviderType` providers are not. Use the existing provider Type field — do not invent a per-channel `Live` field on `ChannelDefinition`.
- **The mode label string** has three forms: closed-empty, closed-filled, open. Render the closed form server-side based on slot fill count. JS swaps to the open form on BROWSE click and updates on tab/group switch.
- **Pre-rendering the three bundled provider trees** in the preset-bank template (as hidden DOM siblings) is the right tradeoff vs server fetch on every tab switch. The alternative requires `/receiver/catalog/<provider>` AJAX with a network round-trip on every tab click. Re-measure actual payload size during implementation (see §preset-bank.html for the ~6KB pre-gzip estimate and the >12KB revisit threshold). Don't optimize prematurely below that threshold.
- **The `transport` SSE event drives source-cluster `.casting`, preset `.lit`, AND catalog `.tuned` simultaneously.** All three surfaces parse the same `AdapterRef`. If a future ref-format change happens, update all three parsers in one PR. The shared parsing logic lives in `parseAdapterRefSource(ref)` in `internal/chassis/data.go` for the source ID and the existing 3A parser for the streams provider/channel pair.
- **The `presets` SSE event carries the full 12-slot array each time** (not a diff). This is intentional — the client doesn't need to maintain prior state to apply the update; the payload is small (~2KB worst case).
- **Pointer drag implementation: prefer `pointerdown`/`pointermove`/`pointerup`** over `mousedown`/`touchstart`/etc. — they handle all input types with one code path. Do NOT use HTML5 native DnD (`draggable="true"` + `dragstart` etc.); its touch story is too uneven.
- **Server-side star semantics matter for race safety.** Two clients both seeing "channel X is in slot 7" and posting "remove" must be deterministic. The server is authoritative; the client sends the desired final state (`starred=true|false`), never a blind toggle.
- **`Move(from, to)` swap semantics, not insert/shift.** Drag slot 7 onto slot 3 → 7 and 3 trade. Do not cascade 4-10 down to 5-11 or similar — that's insert semantics and doesn't match the chassis's "12 fixed positions" model.
- **The persistence file is small enough to rewrite entirely on every mutation.** Don't introduce a journal, diff format, or partial update protocol. Atomic temp-file + `os.Rename` is the whole strategy.
- **Search filter is `:not(.tuned)` aware.** The currently-casting item stays loud even when filtered out, so the user can always see what's playing. JS only toggles `.filter-miss`; bake the tuned exception into CSS selectors.
