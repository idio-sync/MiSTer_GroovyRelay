# Receiver Chassis Input Row & Preset Bank — Phase 3A Design

**Status:** Brainstormed; awaiting implementation plan.

**Scope:** First sub-spec of Phase 3 (Cast Initiation). Wires the chassis input row (paste field + CAST button + .TORRENT upload) to the existing `adapters.QuickCastProvider` interface that the URL and torrent adapters already implement, and makes the 12-slot preset bank render and click-cast the bundled Streams catalog entries the v24 mockup specifies. Source-cluster non-AUX button wiring and the catalog browser drawer are deferred to Phase 3B.

**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored; this spec is force-added per the receiver chassis rollout convention.

## Context

[Phase 0](2026-05-21-receiver-chassis-foundation-design.md) shipped the chassis chrome at `/receiver`, including idle-state placeholder templates for the input row ([input-row.html](../../../internal/chassis/templates/input-row.html)) and preset bank ([preset-bank.html](../../../internal/chassis/templates/preset-bank.html)). Phase 1 wired live VFD, transport, and visualizer state. Phase 2 (telemetry + audio scopes) closed the meter screen. Phase 3 covers cast initiation: source cluster, input/cast surface, catalog browser, and preset bank.

Phase 3's reserved scope is large — the catalog drawer alone is the biggest single surface on the chassis. To avoid a 3,000-line implementation plan, Phase 3 decomposes into:

- **3A — Input row + preset bank** (this spec). Cast initiation MVP: paste a URL or magnet, hit CAST; click a preset slot, fire a Streams catalog cast.
- **3B — Source cluster wiring + catalog browser drawer** (future). Source buttons become "active browse tab" selectors; catalog drawer renders hierarchical Streams + Plex + Jellyfin libraries.
- **3C — User-curated presets** (future). Save/recall, drag-to-slot, rename. The 3A preset bank is read-only from the streams adapter's bundled defaults.

The existing `/ui/playback/quick-cast` endpoint at [internal/ui/playback.go:166](../../../internal/ui/playback.go) is the prior art. URL adapter ([internal/adapters/url/playback_provider.go:144](../../../internal/adapters/url/playback_provider.go)) and torrent adapter ([internal/adapters/torrent/playback_provider.go:44](../../../internal/adapters/torrent/playback_provider.go)) already implement `adapters.QuickCastProvider`. 3A adds a chassis-native cast surface that reuses that interface; the old `/ui/playback/quick-cast` route stays untouched until the final Phase 5 cutover.

The Streams adapter does not yet implement `QuickCastProvider`. 3A adds two small new interfaces on the `internal/adapters/` package (`PresetViewer`, `PresetCaster`) that the streams adapter implements. Future per-source preset banks could implement them too; that is out of scope for 3A.

## Goals

1. **Input row works end-to-end.** The paste field detects URL vs magnet live as the user types and updates the kind chip; the CAST button fires `adapters.QuickCastProvider.HandleQuickCast` on the URL adapter (URL paste) or torrent adapter (magnet paste); the `.TORRENT` button opens a file picker and submits multipart to the torrent adapter's file tab.
2. **Preset bank renders the 12-slot bundled defaults from the streams adapter.** Each slot shows num + name + color-coded badge ("MTV REWIND", "CARTOON", "TOONAMI"). The `.lit` class lights when the current cast matches the slot's `provider:channel`. The `.live` class lights when the slot's channel has `live: true` in the catalog (Toonami Aftermath).
3. **Preset click fires a Streams cast** via a new `adapters.PresetCaster.CastPreset(ctx, slot)` method. The streams adapter looks up the slot's `provider:channel` and starts the appropriate session through its existing playback path.
4. **Cast errors surface inline in the chip.** The detected-kind chip gains a `.err` variant: red background, short uppercase message (`BAD URL`, `BLOCKED HOST`, `CAST FAILED`). Auto-clears on next keystroke or 4 s timeout. No toast, no banner, no dialog.
5. **LIT state is derived from the existing `transport` SSE event.** No new SSE event. Preset bank JS subscribes to `transport`, parses `AdapterRef`, finds the matching slot, toggles `.lit`. Cast transitions across all chassis tabs stay coherent.
6. **`internal/chassis/` adds no concrete-adapter imports.** Chassis imports only `internal/adapters` (the interface package). The existing [import_check_test.go](../../../internal/chassis/import_check_test.go) stays green.
7. **`/ui/*` is unchanged.** 3A is additive under `/receiver/*`.

## Non-Goals

- **User-curated presets.** Save/recall by the operator, drag-to-slot, slot rename, persistence in `bridge.data_dir`. Phase 3C.
- **Catalog browser drawer.** The "BROWSE" button in the preset header stays inert in 3A. Phase 3B.
- **Source cluster non-AUX button wiring.** STREAMS/PLEX/JELLYFIN/DLNA buttons keep their existing render (STREAMS defaults to `Active: true` per [data.go:278](../../../internal/chassis/data.go) but has no click handler). Phase 3B makes them filter the catalog drawer.
- **Plex/Jellyfin URL paste support.** Their adapters do not implement `QuickCastProvider`. Pasting a Plex deep-link returns `BLOCKED HOST` (URL adapter rejects the unknown host); pasting a Jellyfin URL same. Not a regression — matches current `/ui/playback/quick-cast` behavior.
- **A new SSE event for presets.** LIT state derives from the existing `transport` event.
- **Search/filter UI in the preset header.** The mockup's `<input id="search-input" placeholder="FILTER PRESETS · CATALOG">` is a Phase 3B concern (it filters the catalog drawer). The input field renders disabled in 3A.
- **History row population.** Phase 5.
- **Removing or migrating `/ui/playback/quick-cast`.** Old route stays; cutover is the final Phase 5 ticket.
- **Adding new font files, CSS files, or external JS dependencies.** 3A reuses the existing chassis asset surface.

## Design Decisions

| Decision | Resolution |
|---|---|
| Spec scope | Input row + preset bank only. Source cluster and catalog drawer defer to 3B; user-curated presets to 3C. |
| Adapter dependency model | Reuse existing `adapters.QuickCastProvider` for URL/torrent. Add two narrow new interfaces (`PresetViewer`, `PresetCaster`) for streams. Match the chassis Viewer + Saver/Controller split convention. |
| Kind → tab routing | Hardcoded `castKindToTab` map in `internal/chassis/cast.go`. Startup-time test asserts every value resolves to a real `QuickCastTab.ID` in the registered adapters. |
| Preset defaults location | `internal/adapters/streams/assets.go` (next to the bundled provider/channel definitions). Streams adapter exposes `BundledPresets() [12]PresetEntry`. |
| Cast HTTP shape | Two routes: `POST /receiver/cast` (input row, form-urlencoded or multipart) and `POST /receiver/preset/{slot}/cast` (preset bank). Both same-origin gated. JSON response. |
| Cast response shape | `{"ok": true}` on success; `{"ok": false, "chip": "<short uppercase>"}` on validation/cast failure. Client JS swaps the chip class/text from the JSON. |
| Server-side kind verification | Server re-detects kind from payload string. Client `kind` field is UI hint only and not trusted for routing. |
| LIT derivation | Client-side from existing `transport` SSE event's `AdapterRef`. No new SSE event. No server-side preset state. |
| Click-while-casting | Standard `core.Manager` preempt. No client-side confirm dialog. Matches mockup behavior and physical-receiver UX. |
| Error UX | Inline chip variant (`.err` class on the existing kind chip), 4 s auto-clear, cancelled on next keystroke / file-pick. |
| Multipart limit | Match existing `maxQuickCastMultipartBytes = 4*1024*1024 + 64*1024` from [internal/ui/playback.go:212](../../../internal/ui/playback.go). If not already exported, expose as `adapters.MaxQuickCastBytes`. |

## Wire Contract — HTTP Routes

### `POST /receiver/cast` (input row)

**Headers:** `Sec-Fetch-Site: same-origin` (enforced by `requireSameOrigin` middleware).

**Body — URL or magnet (form-urlencoded):**

```
kind=url&payload=https%3A%2F%2Ftwitch.tv%2Fsomechannel
kind=magnet&payload=magnet%3A%3Fxt%3Durn%3Abtih%3Aabc...
```

**Body — torrent file (multipart/form-data):**

```
Content-Type: multipart/form-data; boundary=...

--...
Content-Disposition: form-data; name="kind"

file
--...
Content-Disposition: form-data; name="file"; filename="example.torrent"
Content-Type: application/x-bittorrent

<binary torrent data, ≤ ~4MB>
```

**Server logic:**

1. Parse form (size cap = `adapters.MaxQuickCastBytes` for multipart).
2. Re-detect kind from the payload (URL prefix `http://` or `https://` → `url`; `magnet:?` → `magnet`; file part present → `file`). The form's `kind` field is logged but not used for routing — defense-in-depth.
3. Look up tab ID via `castKindToTab[kind]`. If kind is unknown or the tab does not resolve, respond `{"ok": false, "chip": "BAD INPUT"}` with status 400.
4. Construct `adapters.QuickCastRequest{TabID: tabID, Values: {payload field}, File: <if multipart>}`.
5. Resolve adapter via `quickCastProviderForTab(tabID)` (mirror of [internal/ui/playback.go:338](../../../internal/ui/playback.go)).
6. Call `provider.HandleQuickCast(ctx, req)`.
7. On success respond `{"ok": true}` (status 200). On adapter error respond `{"ok": false, "chip": "<derived short uppercase>"}` (status 4xx or 5xx; see Chip Vocabulary below).

**Responses:**

- `200 OK` with `{"ok":true}` — cast started.
- `400 Bad Request` with `{"ok":false,"chip":"BAD URL" | "BAD INPUT" | ...}` — payload didn't parse or kind couldn't route.
- `403 Forbidden` — wrong origin (middleware).
- `404 Not Found` — no adapter registered for the resolved tab.
- `409 Conflict` — adapter refused (e.g., torrent traffic ack disabled, URL host not in allowed list). Chip: `BLOCKED HOST` for URL adapter rejections; `BLOCKED` for torrent adapter rejections.
- `500 Internal Server Error` — cast failed mid-handoff. Chip: `CAST FAILED`.

### `POST /receiver/preset/{slot}/cast` (preset bank)

**URL parameter:** `slot` ∈ `1..12`.

**Headers:** `Sec-Fetch-Site: same-origin`.

**Body:** empty form (the slot index is in the URL).

**Server logic:**

1. Validate `slot` is integer in `[1, 12]`. Out of range → 400, `{"ok":false,"chip":"BAD SLOT"}`.
2. If chassis was constructed without a `PresetCaster` → 404 (the route exists but the caster doesn't).
3. Call `PresetCaster.CastPreset(ctx, slot)`.
4. On success respond `{"ok": true}`.
5. On adapter error respond `{"ok": false, "chip": "<short>"}`. No DOM update in response body — LIT migrates via the next `transport` SSE event.

**Responses:**

- `200 OK` with `{"ok":true}` — cast started; SSE will migrate LIT shortly.
- `400 Bad Request` — slot out of range.
- `403 Forbidden` — wrong origin.
- `404 Not Found` — `PresetCaster` is nil.
- `500 Internal Server Error` — cast failed. Chip: `CAST FAILED`.

### Chip Vocabulary

Short uppercase strings, ≤ ~14 characters so they fit the chip without truncation. Client clears after 4 s.

| Chip | When |
|---|---|
| `URL` | Idle, paste contains a valid URL |
| `MAGNET` | Idle, paste contains a magnet URI |
| `TORRENT FILE` | A torrent file is queued (with `• <basename>` suffix, truncated to fit) |
| `PASTE URL` | Idle, paste is empty |
| `BAD URL` | URL doesn't parse |
| `BAD INPUT` | Payload doesn't match any known kind |
| `BAD SLOT` | Preset slot out of range |
| `BLOCKED HOST` | URL adapter rejects the host (not in allowed list) |
| `BLOCKED` | Torrent adapter rejected (e.g., traffic ack disabled) |
| `CAST FAILED` | Adapter returned an error mid-handoff |

The `chip` JSON field carries the error text verbatim. The client interprets any response with `chip` present as the error state: it sets `data-chip-kind="err"` on the chip element and uses `chip` as the displayed text. The 4 s auto-clear timer starts when the chip enters the error state; the next keystroke or file-pick cancels the timer.

## Architecture

### New interfaces — `internal/adapters/preset.go`

```go
// PresetEntry is one slot in the chassis preset bank — a reference to
// a specific streams catalog entry plus the display metadata the
// chassis needs to render it.
//
// 3A: produced exclusively by the streams adapter's BundledPresets.
// Future per-source preset banks may produce these too, but only
// streams is registered as a PresetViewer in 3A.
type PresetEntry struct {
    Slot       int    // 1..12, 1-indexed to match the mockup
    ProviderID string // e.g. "mtv-rewind"
    ChannelID  string // e.g. "1stday"
    Title      string // "First Day on MTV" — rendered in the slot's name line
    BadgeLabel string // "MTV REWIND" — rendered in the slot's badge line
    BadgeClass string // "mtv" | "cartoon" | "toonami" — CSS hook for badge color
    Live       bool   // matches mockup `.preset.live` — always-on live channels
}

// PresetViewer returns the 12-slot preset bank snapshot. The chassis
// reads this once per page render. 3A treats the result as static for
// the lifetime of the bridge process; future user-edit specs may
// expose a notification channel.
type PresetViewer interface {
    BundledPresets() [12]PresetEntry
}

// PresetCaster fires a cast for a specific preset slot. Implementations
// look up the slot's catalog entry from their own state and start the
// appropriate session.
//
// Slot is 1-indexed (1..12) to match the URL path parameter and the
// mockup. Implementations MUST return a non-nil error for slots
// outside this range; chassis validates first as defense-in-depth.
type PresetCaster interface {
    CastPreset(ctx context.Context, slot int) error
}
```

### Streams adapter implementations — `internal/adapters/streams/preset.go`

```go
// BundledPresets returns the 12 default chassis preset slots. The
// list is constant for the adapter's lifetime; 3A does not support
// editing.
//
// The 12 entries are pinned in assets.go alongside the bundled
// provider definitions they reference. A unit test asserts every
// preset's ProviderID:ChannelID resolves against the bundled catalog,
// catching renames and typos at compile time of the test, not runtime.
func (a *Adapter) BundledPresets() [12]adapters.PresetEntry {
    return bundledChassisPresets
}

// CastPreset starts a Streams cast for slot N (1-indexed). Looks up
// the slot's provider:channel and delegates to the existing playback
// handler that /ui/streams/play uses today (refactored to a shared
// helper if not already callable from non-route code).
func (a *Adapter) CastPreset(ctx context.Context, slot int) error {
    if slot < 1 || slot > 12 {
        return fmt.Errorf("streams: preset slot %d out of range", slot)
    }
    entry := bundledChassisPresets[slot-1]
    return a.startChannel(ctx, entry.ProviderID, entry.ChannelID)
}
```

The 12 entries in `assets.go`:

```go
var bundledChassisPresets = [12]adapters.PresetEntry{
    {Slot: 1,  ProviderID: "mtv-rewind",       ChannelID: "1stday",      Title: "First Day on MTV",     BadgeLabel: "MTV REWIND",  BadgeClass: "mtv"},
    {Slot: 2,  ProviderID: "mtv-rewind",       ChannelID: "80s",         Title: "MTV 80s",              BadgeLabel: "MTV REWIND",  BadgeClass: "mtv"},
    {Slot: 3,  ProviderID: "mtv-rewind",       ChannelID: "90s",         Title: "MTV 90s",              BadgeLabel: "MTV REWIND",  BadgeClass: "mtv"},
    {Slot: 4,  ProviderID: "mtv-rewind",       ChannelID: "trl",         Title: "TRL",                  BadgeLabel: "MTV REWIND",  BadgeClass: "mtv"},
    {Slot: 5,  ProviderID: "mtv-rewind",       ChannelID: "120minutes",  Title: "120 Minutes",          BadgeLabel: "MTV REWIND",  BadgeClass: "mtv"},
    {Slot: 6,  ProviderID: "mtv-rewind",       ChannelID: "unplugged",   Title: "Unplugged",            BadgeLabel: "MTV REWIND",  BadgeClass: "mtv"},
    {Slot: 7,  ProviderID: "cartoon-rewind",   ChannelID: "loonytunes",  Title: "Looney Tunes",         BadgeLabel: "CARTOON",     BadgeClass: "cartoon"},
    {Slot: 8,  ProviderID: "cartoon-rewind",   ChannelID: "animaniacs",  Title: "Animaniacs",           BadgeLabel: "CARTOON",     BadgeClass: "cartoon"},
    {Slot: 9,  ProviderID: "cartoon-rewind",   ChannelID: "heman",       Title: "He-Man",               BadgeLabel: "CARTOON",     BadgeClass: "cartoon"},
    {Slot: 10, ProviderID: "cartoon-rewind",   ChannelID: "all",         Title: "All Cartoons",         BadgeLabel: "CARTOON",     BadgeClass: "cartoon"},
    {Slot: 11, ProviderID: "toonami-aftermath", ChannelID: "east",       Title: "Toonami East",         BadgeLabel: "TOONAMI",     BadgeClass: "toonami", Live: true},
    {Slot: 12, ProviderID: "toonami-aftermath", ChannelID: "movies",     Title: "Toonami Movies",       BadgeLabel: "TOONAMI",     BadgeClass: "toonami", Live: true},
}
```

Implementation note: `startChannel` should be an existing or factored-out helper from the streams adapter's playback path (the same one [routes.go:122](../../../internal/adapters/streams/routes.go) `handlePlay` uses). If `handlePlay` currently does its work inline, factor the channel-start logic into a method first, then call it from both `handlePlay` and `CastPreset`.

### Chassis `Config` additions — `internal/chassis/server.go`

```go
type Config struct {
    // ... existing fields ...

    // PresetViewer is the optional source of the 12-slot chassis
    // preset bank. When nil, the preset bank renders all 12 slots in
    // the .empty state (no name, no badge). 3A wires the streams
    // adapter here.
    PresetViewer PresetViewer

    // PresetCaster is the optional handler for preset slot clicks.
    // When nil, POST /receiver/preset/{slot}/cast returns 404.
    PresetCaster PresetCaster
}
```

`cmd/mister-groovy-relay/main.go` passes the streams adapter as both (streams adapter implements both interfaces).

### Kind → tab mapping — `internal/chassis/cast.go`

```go
// castKindToTab maps the chassis input row's detected kind to the
// QuickCastTab.ID it should submit against. A startup-time test
// asserts every value resolves to a real tab in the registered
// adapters; if an adapter renames a tab, that test fails before
// deploy.
var castKindToTab = map[string]string{
    "url":    "url",            // url adapter
    "magnet": "torrent-magnet", // torrent adapter
    "file":   "torrent-file",   // torrent adapter
}

func (s *Server) detectCastKind(payload string, hasFile bool) string {
    switch {
    case hasFile:
        return "file"
    case strings.HasPrefix(payload, "magnet:?"):
        return "magnet"
    case strings.HasPrefix(payload, "http://") || strings.HasPrefix(payload, "https://"):
        return "url"
    default:
        return ""
    }
}
```

### Chassis data model — `internal/chassis/data.go`

Extend `PresetSlot` with the fields the live render needs:

```go
type PresetSlot struct {
    // Existing:
    Filled   bool
    Title    string
    Subtitle string // mockup .badge label, e.g. "MTV REWIND"

    // New in 3A:
    Slot       int    // 1..12 — needed for the POST URL
    BadgeClass string // "mtv" | "cartoon" | "toonami" — CSS color hook
    Lit        bool   // currently casting from this slot — server-side initial paint
    Live       bool   // .preset.live class — always-on live channels
    ProviderID string // streams provider id — for client-side LIT migration
    ChannelID  string // streams channel id — same
}
```

**Field mapping from `PresetEntry` → `PresetSlot`** (used by `idleSnapshot` and `snapshotFromStatusView`):

| `PresetEntry` field | `PresetSlot` field | Notes |
|---|---|---|
| `Slot` | `Slot` | direct |
| `ProviderID` | `ProviderID` | direct |
| `ChannelID` | `ChannelID` | direct |
| `Title` | `Title` | direct |
| `BadgeLabel` | `Subtitle` | renamed — `Subtitle` is the existing chassis field name; `BadgeLabel` is the adapter-side name. Kept distinct deliberately: the chassis can call the field whatever fits its template vocabulary without forcing the adapter to know about chassis CSS class names. |
| `BadgeClass` | `BadgeClass` | direct |
| `Live` | `Live` | direct |
| (computed) | `Filled` | true iff the source `PresetEntry` is non-zero (`ProviderID != ""`) |
| (computed) | `Lit` | true iff the current transport `AdapterRef` parses to a `streams:<providerID>:<channelID>:...` matching this slot |

`Lit` is computed at page render time from the current transport state, so the initial paint after a hard reload during an active cast lights the correct slot without waiting for the first SSE tick. The client then keeps it in sync via the `transport` event subscription.

`PresetsData.ModeLabel` becomes `"Memory · 12 / 12 slots"` (matching the mockup) when `PresetViewer` is wired, `"Memory · 0 / 12 slots"` when nil.

`PresetsData.Count` becomes `"★ 12"` / `"★ 0"` similarly.

### Templates — `internal/chassis/templates/`

#### `input-row.html`

Existing template already has the right DOM bones. Additions:

- `<span class="chip" id="paste-chip" data-chip-kind="...">` — add `data-chip-kind` so CSS can drive the kind- and error-state styles. The error variant is `data-chip-kind="err"` plus a `data-chip-err` text override.
- New `data-cast-state` attribute on the input panel for tracking submit-in-flight (disables CAST during the network round-trip).

#### `preset-bank.html`

Existing template renders 12 slots from `.Slots`. Additions:

```html
<button class="preset{{if not $slot.Filled}} empty{{end}}{{if $slot.Lit}} lit{{end}}{{if $slot.Live}} live{{end}}"
        type="button"
        data-slot="{{$slot.Slot}}"
        data-provider="{{$slot.ProviderID}}"
        data-channel="{{$slot.ChannelID}}">
  <div class="num">{{pad2 (inc $i)}}</div>
  {{if $slot.Filled}}
  <div class="name">{{$slot.Title}}</div>
  <div class="badge {{$slot.BadgeClass}}">{{$slot.Subtitle}}</div>
  {{end}}
</button>
```

The BROWSE button (`<button class="browse-btn">`) renders disabled in 3A:

```html
<button class="browse-btn" id="browse-toggle" disabled aria-disabled="true">▸ Browse full catalog</button>
```

Same for the search field.

### Client JS

#### `internal/chassis/static/input-cast.js` (new)

- Live kind detection, debounced 120 ms.
- `data-chip-kind` and chip text updates.
- CAST button enabled gate.
- Form submit via `fetch()`; reads JSON response; on error sets `data-chip-kind="err"` + chip text; 4 s auto-clear.
- `.TORRENT` button opens hidden file input; queued file state; cancel button to clear.
- Multipart submit when a file is queued.

#### `internal/chassis/static/preset-bank.js` (new)

- Subscribes to `transport` event via the `subscribe()` helper from [vfd-live.js](../../../internal/chassis/static/vfd-live.js).
- Parses `transport.adapterRef`. The streams adapter constructs refs via [queueAdapterRef](../../../internal/adapters/streams/playback.go) as `streams:<providerID>:<channelID>:<sessionID>:<itemToken>` — 5 colon-separated segments. The preset-bank parser extracts segments at indices 1 and 2 (the providerID and channelID) and discards the rest. Refs that don't start with `streams:` or have fewer than 3 segments are treated as non-streams; all `.lit` classes clear.
- On match, toggles `.lit` on the slot whose `data-provider:data-channel` equals the parsed pair.
- Click handler: `POST /receiver/preset/{slot}/cast` with the slot number; reads JSON; on error renders the error chip in the input row (presets reuse the input row's error surface — single source of truth for cast errors). No DOM update on success; LIT migrates via SSE.

### CSS additions — `internal/chassis/static/chassis.css`

All new rules scoped under `body.receiver` (existing `css_scope_test.go` enforces this):

- `.chip[data-chip-kind="err"]` — red background, white text, brief flash animation on transition into.
- `.badge.mtv`, `.badge.cartoon`, `.badge.toonami` — color variants per mockup (already partially defined in v24, port the exact colors).
- `.browse-btn[disabled]` — visually disabled state.

## SSE — No Changes

3A does not add any SSE event. The existing `transport` event already carries `AdapterRef`, `State`, and `Generation`; the preset bank derives its LIT state client-side from that event.

The chassis snapshot cache and refresher remain unchanged. `buildSnapshot` calls `PresetViewer.BundledPresets()` per refresh tick (cheap — returns a constant array); this is acceptable for 3A because the list is bounded at 12 entries and the adapter side is just an array copy. If the streams catalog becomes dynamically refreshable in a later spec, the snapshot path may need a change cursor or notification channel — out of scope for 3A.

## Testing

### Layer 1 — Unit (per-package)

- **`internal/adapters/streams/preset_test.go`**
  - `BundledPresets()` returns 12 entries.
  - Every `(ProviderID, ChannelID)` resolves to a real channel in `bundledManifest()`.
  - `BadgeClass` values are in the set `{"mtv", "cartoon", "toonami"}`.
  - Slots 11 and 12 have `Live: true`.
  - `CastPreset(ctx, 0)` and `CastPreset(ctx, 13)` return a non-nil error.
  - `CastPreset(ctx, 7)` calls into the existing `startChannel` (or refactored helper) with `("cartoon-rewind", "loonytunes")`.

- **`internal/chassis/cast_test.go`**
  - `detectCastKind` returns `"url"`, `"magnet"`, `"file"`, `""` for representative inputs.
  - `castKindToTab` startup verification: with the real registry (URL + torrent adapters), every value in the map resolves to a real `QuickCastTab.ID`.
  - `castKindToTab` handles a missing adapter gracefully (e.g., torrent not registered) — the test exercises a registry without the torrent adapter and asserts the verification helper returns a structured "missing tab" error rather than panicking. Production startup wires this verification.
  - JSON response shapes: success / 400 / 404 / 409 / 500 each encode `{"ok": bool, "chip": string?}` correctly.

- **`internal/chassis/preset_test.go`**
  - Handler: 404 when `PresetCaster` is nil.
  - Handler: 400 for slot=0, slot=13, slot="abc".
  - Handler: 200 on `CastPreset` success.
  - Handler: 500 on `CastPreset` error, with chip "CAST FAILED".
  - Same-origin guard rejects cross-origin.
  - Data: `idleSnapshot` produces a PresetsData with Filled=false slots when `PresetViewer` is nil; with a fake `PresetViewer`, `Filled=true` slots inherit Slot/Title/Subtitle/BadgeClass/Live correctly.
  - Lit derivation: a fake transport view with `AdapterRef: "streams:mtv-rewind:1stday:sess-1:42"` lights slot 1 in the snapshot. Refs with truncated prefixes (`streams:mtv-rewind:`) and non-streams refs (`url:...`) clear all `.lit`.

### Layer 2 — Template + CSS

- **`internal/chassis/chassis_test.go`**
  - `preset-bank.html` renders `data-slot`, `data-provider`, `data-channel` on each slot.
  - `.lit`, `.live`, `.empty` classes apply correctly given representative data.
  - `input-row.html` renders the chip with `data-chip-kind`.
  - `chassis.css` scope check (existing — should be green; new rules conform).

- No-fake-values JS lint: `preset-bank.js` and `input-cast.js` must not contain `Math.random`, `Math.sin`, or other fake-data generators. (Existing lint in `chassis_test.go` from prior specs.)

### Layer 3 — Integration

- **`tests/integration/chassis_test.go`** (build-tagged `integration`)
  - Real chassis + fake registry (URL + torrent adapter stubs + streams stub implementing `PresetViewer`/`PresetCaster`).
  - `POST /receiver/cast` with `kind=url&payload=...` → fake URL adapter's `HandleQuickCast` was called with the right tab.
  - `POST /receiver/cast` with multipart file → fake torrent adapter's `HandleQuickCast` was called with the file part.
  - `POST /receiver/preset/3/cast` → fake streams `CastPreset(ctx, 3)` was called.
  - `POST /receiver/preset/0/cast` returns 400.
  - Connect SSE, fake a transport event with `AdapterRef: "streams:mtv-rewind:90s"` → next snapshot's `Presets.Slots[2].Lit` is true.

### Layer 4 — Manual smoke (post-merge)

- Open `/receiver`.
- Paste a known-good URL → chip shows "URL" → CAST → cast starts → return to `/receiver` and confirm LIT migrates if it lands on a presetted streams channel (it won't for a URL paste; LIT stays clear).
- Paste a magnet → chip shows "MAGNET" → CAST → torrent adapter session starts.
- Upload a .torrent file → chip shows "TORRENT FILE • basename" → CAST → torrent adapter session starts.
- Paste garbage → chip shows "BAD INPUT" or "BAD URL".
- Click preset slot 1 → Streams cast starts → slot 1 LIT lights → click slot 7 → previous LIT clears, slot 7 lights.
- Click slot 11 → cast starts, slot 11 lights and shows `.live` styling.

## Migration

None. 3A is purely additive under `/receiver/*`. `/ui/playback/quick-cast` is untouched.

## Forward Compatibility

- **Phase 3B** (catalog browser drawer) will use the same `castKindToTab` and `PresetCaster` plumbing. The BROWSE button stays a no-op in 3A and gets wired in 3B.
- **Phase 3C** (user-curated presets) will likely replace `BundledPresets()` with a `PresetStore` interface that wraps the bundled defaults plus a `bridge.data_dir`-persisted override layer. The `PresetEntry` shape may grow a `UserDefined bool` field; 3A's struct is forward-compatible.
- **Future adapters that want input-row casting:** add `Kind` to `castKindToTab` if it's a new kind (e.g., `dlna-uri`), or extend an existing adapter's `QuickCastTabs()` to advertise a tab matching one of the existing kinds.

## Notes for the Implementer

- The 12-slot list is the source of truth for the mockup's preset bank. Do not invent new slots, reorder them, or change the badge labels without updating the mockup at [docs/superpowers/reference/2026-05-21-receiver-v24.html](../reference/2026-05-21-receiver-v24.html) too — drift between the mockup and the live render is the single most common source of churn in this rollout.
- The `castKindToTab` startup verification is load-bearing. It is what guards the chassis from silently breaking when an adapter renames a `QuickCastTab.ID`. Do not skip it; do not turn it into a soft warning.
- The chassis must not import `internal/adapters/streams` directly. `import_check_test.go` will fail loudly if it does. The streams adapter is wired exclusively through the `PresetViewer` / `PresetCaster` interfaces in the chassis `Config`.
- Server-side kind re-detection (`detectCastKind` on the parsed payload) is required for defense-in-depth. A malicious or buggy client could submit `kind=url` with a `magnet:?` payload; the server routes by what's in the payload, not what the client claims.
- The streams adapter's `startChannel` helper (if it doesn't already exist as a method) is a refactor inside the streams adapter; do not introduce it as a public method on `*Adapter` if it can stay package-private. The `CastPreset` method on the adapter is what gets exported.
- The `LIT` derivation parses the first three segments of `transport.adapterRef`: `streams:<providerID>:<channelID>:...` (further segments — session, item token — are discarded). Confirmed at spec time against [streams/playback.go:1328](../../../internal/adapters/streams/playback.go). If a future change to the streams adapter widens or narrows that prefix, update both the client-side parser in `preset-bank.js` and the server-side `Lit` computation in `data.go` together — these are paired, not independent.
