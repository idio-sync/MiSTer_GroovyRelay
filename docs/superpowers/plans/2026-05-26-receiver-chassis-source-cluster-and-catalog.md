# Receiver Chassis Source Cluster & Streams Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recast the four non-AUX source-cluster buttons as three-state indicator lamps driven by per-adapter `Configured()`; wire the BROWSE button in the preset header to a server-rendered Streams catalog drawer (provider tabs → group rail → channel grid); enable star-toggle preset editing and pointer-based drag-reorder backed by a persistent slot store at `{data_dir}/chassis_presets.json`; and make the preset-header search field live-filter both the preset bank and the catalog grid.

**Architecture:** Four new narrow adapter interfaces (`SourceAvailabilityViewer`, `StreamsCatalogViewer`, `StreamsCaster`, `PresetEditor`) live in `internal/adapters/`. The streams adapter implements all three of the latter via a new `presetStore` that owns the 12-slot in-memory array and atomic file persistence. The chassis `Config` gains four corresponding fields; HTTP handlers translate user actions into adapter calls and a new `presets` SSE event drives client-side re-render. The `transport` SSE event continues to drive `.tuned`/`.lit`/`.casting` across source-cluster lamps, preset slots, and catalog cards from one `AdapterRef`. Client JS adds five new modules: `source-cluster.js`, `catalog-browser.js`, `preset-reorder.js`, `search-filter.js`, plus an extension to `preset-bank.js`.

**Tech Stack:** Go 1.26 stdlib (`context`, `encoding/json`, `errors`, `net/http`, `os`, `path/filepath`, `strconv`, `strings`, `sync`), existing internal packages (`internal/adapters`, `internal/adapters/streams`, `internal/adapters/plex`, `internal/adapters/jellyfin`, `internal/adapters/dlna`, `internal/chassis`, `internal/core`), vanilla ES2022 (no bundler), HTML `<template>` cloning for catalog pre-rendering. No new go.mod dependencies.

**Spec:** [docs/superpowers/specs/2026-05-25-receiver-chassis-source-cluster-and-catalog-design.md](../specs/2026-05-25-receiver-chassis-source-cluster-and-catalog-design.md)

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/adapters/source.go` | `SourceAvailabilityViewer` interface |
| `internal/adapters/source_test.go` | Interface-shape assertion test (compile-time only) |
| `internal/adapters/catalog.go` | `CatalogProvider` / `CatalogGroup` / `CatalogChannel` types; `StreamsCatalogViewer` and `StreamsCaster` interfaces |
| `internal/adapters/streams/source.go` | Streams `SourceID()` / `Configured()` impl |
| `internal/adapters/streams/source_test.go` | Streams `SourceAvailabilityViewer` impl tests |
| `internal/adapters/streams/catalog.go` | Streams `Catalog()` + `CastChannel()` impl; `bundledChassisCatalogProviderIDs` and `providerBadges` lookup |
| `internal/adapters/streams/catalog_test.go` | Catalog shape + cast error mapping |
| `internal/adapters/streams/preset_store.go` | In-memory 12-slot array + atomic file persistence + stale-reference scrubbing |
| `internal/adapters/streams/preset_store_test.go` | Store lifecycle: load, seed, star add/remove, move, BANK FULL, atomic write |
| `internal/adapters/plex/source.go` | Plex `SourceID()` / `Configured()` impl |
| `internal/adapters/plex/source_test.go` | Plex `SourceAvailabilityViewer` impl tests |
| `internal/adapters/jellyfin/source.go` | Jellyfin `SourceID()` / `Configured()` impl |
| `internal/adapters/jellyfin/source_test.go` | Jellyfin `SourceAvailabilityViewer` impl tests |
| `internal/adapters/dlna/source.go` | DLNA `SourceID()` / `Configured()` impl |
| `internal/adapters/dlna/source_test.go` | DLNA `SourceAvailabilityViewer` impl tests |
| `internal/chassis/streams_cast.go` | `POST /receiver/streams/cast` handler |
| `internal/chassis/streams_cast_test.go` | Streams cast handler unit tests |
| `internal/chassis/templates/catalog-drawer.html` | Catalog drawer container + provider tabs + slots for rail/grid |
| `internal/chassis/templates/catalog-rail.html` | Group rail buttons template |
| `internal/chassis/templates/catalog-grid.html` | Channel cards template |
| `internal/chassis/static/source-cluster.js` | Subscribes to `transport`; updates lamp `.casting` class |
| `internal/chassis/static/catalog-browser.js` | BROWSE toggle, tab/rail switching, channel cast, star toggle, SSE subscriptions |
| `internal/chassis/static/preset-reorder.js` | Pointer-based drag-reorder + Ctrl+Arrow keyboard shortcut |
| `internal/chassis/static/search-filter.js` | Live substring filter against preset bank + catalog grid |

**Modified files:**

| Path | Change |
|---|---|
| `internal/adapters/preset.go` | Rename `PresetViewer.BundledPresets()` → `Presets()`; add `PresetEditor` interface + `PresetStarResult` type |
| `internal/adapters/streams/preset.go` | Rename method to `Presets()`; back with `presetStore.Snapshot()` instead of literal; add `SetPresetStarred` + `MovePreset` thin wrappers |
| `internal/adapters/streams/preset_test.go` | Rename calls; cover store-backed behavior; new tests for `SetPresetStarred` + `MovePreset` |
| `internal/adapters/streams/adapter.go` | Initialize `presetStore` in `New` |
| `internal/chassis/server.go` | Add `Config.StreamsCatalogViewer`, `Config.StreamsCaster`, `Config.PresetEditor`, `Config.SourceAvailabilityViewers`; store on `*Server`; register two new mux routes; add `refreshSnapshotNow()` helper |
| `internal/chassis/data.go` | Extend `SourceButton` with `Configured`/`Casting`; add `CatalogData` + sub-types; rename viewer call from `BundledPresets()` → `Presets()`; add `applySourceLampState` + `buildCatalogData` + `parseAdapterRefSource` helpers |
| `internal/chassis/session.go` | Wire `applySourceLampState` + `buildCatalogData` into `snapshotFromStatusView` |
| `internal/chassis/cast.go` | Add `writePresetStarSuccess`, `writePresetMoveSuccess`, `writePresetEditError` helpers and shared `presetEditBody` struct |
| `internal/chassis/preset.go` | Add `POST /receiver/preset/star` and `POST /receiver/preset/move` handlers; existing slot-cast handler unchanged |
| `internal/chassis/preset_test.go` | Add tests for new star and move handlers |
| `internal/chassis/events.go` | Add `presetsEnvelope` + `presetEnvelopeFromSnapshot` + `presetsChanged` + initial-burst position between `meter` and `audio` + diff arm in tick loop |
| `internal/chassis/events_test.go` | Cover `presetsChanged` and event ordering |
| `internal/chassis/cast_test.go` | Cover new preset edit helpers |
| `internal/chassis/data_test.go` | Cover `applySourceLampState`, `buildCatalogData`, `parseAdapterRefSource` |
| `internal/chassis/chassis_test.go` | Add template render tests for source-cluster lamp/AUX branch, catalog drawer/rail/grid, preset-bank un-disable, BROWSE/search text; shell.html script load tests; no-fake-values JS lint for new JS files |
| `internal/chassis/templates.go` | Add `lower` FuncMap helper |
| `internal/chassis/templates/source-cluster.html` | Branch on `Action` — empty → `.lamp`, non-empty → existing `.hw-btn` (AUX) |
| `internal/chassis/templates/preset-bank.html` | Un-disable BROWSE + search input; add `data-slot`/`data-provider`/`data-channel` (already present in 3A); render mode label closed-state form; add `<template id="catalog-tree-<provider>">` blocks holding pre-rendered DOM |
| `internal/chassis/templates/shell.html` | Add `<script>` tags for `source-cluster.js`, `catalog-browser.js`, `preset-reorder.js`, `search-filter.js` |
| `internal/chassis/static/chassis.css` | Add lamp rules, catalog drawer rules, catalog-scan VFD overlay, stagger-in keyframes, drag-reorder rules, search-filter rules — all scoped under `body.receiver` |
| `internal/chassis/static/preset-bank.js` | Subscribe to new `presets` SSE event; re-render slots from envelope |
| `cmd/mister-groovy-relay/main.go` | Pass streams adapter as `StreamsCatalogViewer`, `StreamsCaster`, `PresetEditor`; assemble `[]adapters.SourceAvailabilityViewer` from registry; rename consumer of `PresetViewer.BundledPresets()` → `Presets()` if any |
| `tests/integration/chassis_test.go` | End-to-end tests: streams cast, star add/remove/BANK FULL, move swap, SSE `presets` ordering + position |

**Files intentionally unchanged:**

- `internal/chassis/import_check_test.go` — the chassis forbidden-imports list already covers `streams/url/torrent/plex/jellyfin/dlna` (added in 3A).
- `internal/ui/*` and `internal/uiserver/*` — 3B is additive under `/receiver/*`.
- `internal/core/*` — no new core surface; existing transport SSE event carries `AdapterRef`.
- `internal/adapters/playback.go` — `QuickCastError` and `MaxQuickCastBytes` from 3A reused as-is.
- `internal/chassis/templates/source-cluster.html`'s AUX branch — handler logic untouched; only the empty-Action case adds the `.lamp` rendering.

---

## Sequencing

Tasks 1-3 add the new adapter interfaces and rename `PresetViewer.BundledPresets()` → `Presets()`. These land first so subsequent tasks compile against the new contracts. Task 3 includes the call-site migration for the existing 3A consumers (chassis `data.go` + tests).

Tasks 4-8 implement the streams-side concrete types: `SourceAvailabilityViewer` (4), per-adapter source impls for plex/jellyfin/dlna (5-7), `Catalog()` and `CastChannel()` (8).

Tasks 9-10 are the preset store: persistence file format, atomic write, stale scrubbing, and the rename + new mutation methods on the streams adapter.

Tasks 11-14 add the chassis-side data model and snapshot wiring (Config additions, CatalogData types, source lamp state, snapshot integration).

Tasks 15-17 add the new HTTP handlers (streams cast, preset star, preset move) with the cast helper trio.

Task 18 adds the `presets` SSE event with ordering between `meter` and `audio`.

Tasks 19-22 update templates (source-cluster lamp branch, catalog drawer/rail/grid, preset-bank extension, shell script tags).

Tasks 23-26 add the client-side JS modules.

Task 27 adds CSS rules under `body.receiver` scope.

Task 28 wires `main.go` and adds the integration test suite.

Each task is independently committable. CI green between tasks is required.

---

## Task 1: Add `SourceAvailabilityViewer` Interface

**Files:**
- Create: `internal/adapters/source.go`
- Create: `internal/adapters/source_test.go`

- [ ] **Step 1: Write the failing test**

Create [internal/adapters/source_test.go](../../../internal/adapters/source_test.go):

```go
package adapters

import "testing"

// fakeSourceAvailabilityViewer is a compile-time conformance fixture.
// If the SourceAvailabilityViewer interface changes shape, this fails
// to build, alerting changeset reviewers before the surface drifts.
type fakeSourceAvailabilityViewer struct {
	id   string
	conf bool
}

func (f fakeSourceAvailabilityViewer) SourceID() string { return f.id }
func (f fakeSourceAvailabilityViewer) Configured() bool { return f.conf }

func TestSourceAvailabilityViewer_StructuralConformance(t *testing.T) {
	t.Parallel()
	var v SourceAvailabilityViewer = fakeSourceAvailabilityViewer{id: "streams", conf: true}
	if v.SourceID() != "streams" {
		t.Errorf("SourceID = %q, want streams", v.SourceID())
	}
	if !v.Configured() {
		t.Errorf("Configured = false, want true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters -run TestSourceAvailabilityViewer_StructuralConformance -v`

Expected: FAIL — `SourceAvailabilityViewer` undefined.

- [ ] **Step 3: Create the interface**

Create [internal/adapters/source.go](../../../internal/adapters/source.go):

```go
package adapters

// SourceAvailabilityViewer reports whether a source-adapter is
// configured/ready to receive casts. The chassis uses this to drive
// the source-cluster lamp distinction between "unavailable" (lamp dark)
// and "configured-idle" (lamp dim amber).
//
// Implementations should treat Configured() as a fast in-memory check —
// it is invoked per chassis snapshot tick (4 Hz today). Anything that
// requires I/O should be cached behind an internal field updated on
// the adapter's own clock.
//
// SourceID() returns one of the canonical source identifiers used by
// the chassis source-cluster: "streams" | "plex" | "jellyfin" | "dlna".
type SourceAvailabilityViewer interface {
	SourceID() string
	Configured() bool
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters -run TestSourceAvailabilityViewer_StructuralConformance -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/source.go internal/adapters/source_test.go
git commit -m "feat(adapters): add SourceAvailabilityViewer interface"
```

---

## Task 2: Add Catalog Types and Interfaces

**Files:**
- Create: `internal/adapters/catalog.go`

The catalog types are pure data structs. The interfaces have no Go-side state beyond their method shapes. A separate test file is unnecessary — the streams catalog test in Task 8 exercises the real implementation. Adapter packages that want to provide library browsing later (plex, jellyfin) will pick up the same `CatalogProvider` shape.

- [ ] **Step 1: Create catalog.go**

Create [internal/adapters/catalog.go](../../../internal/adapters/catalog.go):

```go
package adapters

import "context"

// CatalogProvider is one provider tab in the receiver catalog drawer.
// 3B exposes exactly the three bundled Streams providers (MTV Rewind,
// Cartoon Rewind, Toonami Aftermath). Future per-source library specs
// (plex, jellyfin) will return the same shape.
type CatalogProvider struct {
	ID             string         // e.g. "mtv-rewind"
	DisplayName    string         // e.g. "MTV Rewind"
	BadgeLabel     string         // e.g. "MTV" — small text in .ic glyph
	BadgeClass     string         // e.g. "mtv" | "cartoon" | "toonami" — CSS hook
	Live           bool           // whole provider is always-live (direct streams)
	DefaultChannel string         // for the catalog's initial selection
	Groups         []CatalogGroup // ordered
}

// CatalogGroup is a left-rail entry: a named group of channels within
// one provider.
type CatalogGroup struct {
	ID       string
	Name     string
	Channels []CatalogChannel // ordered
}

// CatalogChannel is one channel card. PlayMode is uppercased ("SEQ" /
// "SHUFFLE") so the template renders the .meta line literally. Live is
// true when the channel is always-live (Toonami direct streams) or its
// provider.Live is true.
type CatalogChannel struct {
	ID       string
	Name     string
	PlayMode string
	Live     bool
}

// StreamsCatalogViewer returns the chassis-shaped catalog for the
// receiver drawer. Read-only; the chassis snapshots this per page
// render.
//
// Implementations must be safe to call before adapter Start (main.go
// binds HTTP first). The streams impl returns the bundled-manifest
// providers as a local-only bootstrap when remote refresh has not yet
// populated catalogs.
type StreamsCatalogViewer interface {
	Catalog() []CatalogProvider
}

// StreamsCaster casts a specific catalog channel. The chassis HTTP
// handler validates the inputs are non-empty and forwards directly;
// implementations must validate against their own catalog and return
// a typed *QuickCastError for status/chip propagation.
type StreamsCaster interface {
	CastChannel(ctx context.Context, providerID, channelID string) error
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go vet ./internal/adapters/...`

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/catalog.go
git commit -m "feat(adapters): add catalog types and StreamsCatalogViewer/StreamsCaster interfaces"
```

---

## Task 3: Rename `BundledPresets()` → `Presets()`; Add `PresetEditor` + `PresetStarResult`

**Files:**
- Modify: `internal/adapters/preset.go`
- Modify: `internal/adapters/streams/preset.go` (rename only)
- Modify: `internal/adapters/streams/preset_test.go` (rename call sites)
- Modify: `internal/chassis/data.go` (call-site migration in `buildPresetsData`)
- Modify: `internal/chassis/chassis_test.go` (fake viewer rename)

The rename is mechanical; the interface addition is additive. Keeping the rename in the same task as the additions avoids two compile-broken commits.

- [ ] **Step 1: Write failing test for `PresetStarResult` JSON shape**

Append to [internal/adapters/preset_test.go](../../../internal/adapters/preset_test.go) (create the file if it doesn't exist — header `package adapters`):

```go
package adapters

import (
	"encoding/json"
	"testing"
)

func TestPresetStarResult_StarredOmitsCleared(t *testing.T) {
	t.Parallel()
	res := PresetStarResult{Starred: true, Slot: 7}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(body)
	want := `{"starred":true,"slot":7}`
	if got != want {
		t.Errorf("Marshal(Starred) = %s, want %s", got, want)
	}
}

func TestPresetStarResult_UnstarredOmitsSlot(t *testing.T) {
	t.Parallel()
	res := PresetStarResult{Starred: false, Cleared: []int{3, 9}}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(body)
	want := `{"starred":false,"cleared":[3,9]}`
	if got != want {
		t.Errorf("Marshal(Unstarred) = %s, want %s", got, want)
	}
}

func TestPresetStarResult_UnstarredEmptyClearedOmitsCleared(t *testing.T) {
	t.Parallel()
	// Empty Cleared slice on the unstarred no-op path: omitempty kicks in.
	res := PresetStarResult{Starred: false}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(body)
	want := `{"starred":false}`
	if got != want {
		t.Errorf("Marshal(Unstarred no-op) = %s, want %s", got, want)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters -run TestPresetStarResult -v`

Expected: FAIL — `PresetStarResult` undefined.

- [ ] **Step 3: Replace [internal/adapters/preset.go](../../../internal/adapters/preset.go)**

Open [internal/adapters/preset.go](../../../internal/adapters/preset.go) and replace its contents with:

```go
package adapters

import "context"

// PresetEntry is one slot in the chassis preset bank — a reference to
// a specific streams catalog entry plus the display metadata the
// chassis needs to render it.
//
// Display fields (Title, BadgeLabel, BadgeClass, Live) are derived
// from the catalog at load time and refreshed on each catalog reload;
// the persistent (Slot, ProviderID, ChannelID) triple is the only data
// written to {data_dir}/chassis_presets.json.
type PresetEntry struct {
	Slot       int    // 1..12, 1-indexed to match the mockup
	ProviderID string // e.g. "mtv-rewind"
	ChannelID  string // e.g. "1stday"
	Title      string // "First Day on MTV" — derived from catalog
	BadgeLabel string // "MTV REWIND" — derived from catalog
	BadgeClass string // "mtv" | "cartoon" | "toonami" — derived from catalog
	Live       bool   // derived from provider/channel Live flag
}

// PresetViewer returns the 12-slot preset bank snapshot. 3B backs this
// with a mutable file-persisted store; the method name no longer
// implies "bundled" because users can edit the slots.
type PresetViewer interface {
	Presets() [12]PresetEntry
}

// PresetCaster fires a cast for a specific preset slot. Implementations
// look up the slot's catalog entry from their own state.
//
// Slot is 1-indexed (1..12). Implementations MUST return a non-nil
// error for slots outside this range as defense-in-depth.
type PresetCaster interface {
	CastPreset(ctx context.Context, slot int) error
}

// PresetEditor mutates the 12-slot preset bank. The chassis HTTP
// handler translates user actions (catalog star click, drag-reorder)
// into these calls.
//
// SetPresetStarred is idempotent in the desired-state sense:
//   - starred=true, channel present → no-op, return current slot
//   - starred=true, channel absent → write to first empty slot
//   - starred=true, all 12 slots full → *QuickCastError{409, BANK FULL}
//   - starred=false, channel present → clear all matching slots
//   - starred=false, channel absent → no-op
//
// MovePreset is swap semantics: from and to trade. from==to is a
// no-op success. from or to outside 1..12 → *QuickCastError{400, BAD SLOT}.
type PresetEditor interface {
	SetPresetStarred(ctx context.Context, providerID, channelID string, starred bool) (PresetStarResult, error)
	MovePreset(ctx context.Context, from, to int) error
}

// PresetStarResult is the typed return from PresetEditor.SetPresetStarred.
// Zero-value rules (enforced by callers, not the type):
//   - Starred=true:  Slot in 1..12, Cleared MUST be nil.
//   - Starred=false: Slot MUST be 0, Cleared MAY be empty (no-op remove)
//                    or populated. The JSON tags use omitempty so the
//                    wire never carries stale fields.
type PresetStarResult struct {
	Starred bool  `json:"starred"`
	Slot    int   `json:"slot,omitempty"`
	Cleared []int `json:"cleared,omitempty"`
}
```

- [ ] **Step 4: Update streams adapter to use `Presets()` name**

Open [internal/adapters/streams/preset.go](../../../internal/adapters/streams/preset.go) and rename the method:

```go
// BundledPresets returns the 12 default chassis preset slots.
func (a *Adapter) BundledPresets() [12]adapters.PresetEntry {
	return bundledChassisPresets
}
```

becomes (the body stays the same in this task — the store backing happens in Task 10):

```go
// Presets returns the current 12-slot chassis preset bank snapshot.
// Task 10 swaps the underlying source from the bundled literal to a
// file-backed store; for now this is a straight rename.
func (a *Adapter) Presets() [12]adapters.PresetEntry {
	return bundledChassisPresets
}
```

- [ ] **Step 5: Update streams preset_test call sites**

Rename every `a.BundledPresets()` invocation in [internal/adapters/streams/preset_test.go](../../../internal/adapters/streams/preset_test.go) to `a.Presets()`. The four test functions are `TestBundledPresets_ReturnsTwelveEntries`, `TestBundledPresets_EveryEntryResolvesAgainstBundledManifest`, `TestBundledPresets_BadgeClassWithinEnum`, `TestBundledPresets_LiveFlagOnToonamiSlots`, and `TestBundledPresets_SlotsAre1Indexed`.

Rename the test function names to use `TestPresets_…` for consistency. Replace test names as a single mechanical substitution.

```bash
# Mechanical: BundledPresets → Presets in preset_test.go (no other files in this package use the old name).
```

- [ ] **Step 6: Update chassis `buildPresetsData` call site**

In [internal/chassis/data.go](../../../internal/chassis/data.go), find:

```go
entries := viewer.BundledPresets()
```

Replace with:

```go
entries := viewer.Presets()
```

- [ ] **Step 7: Update chassis `fakePresetViewer` call site**

In [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go), find:

```go
func (f fakePresetViewer) BundledPresets() [12]adapters.PresetEntry { return f.entries }
```

Replace with:

```go
func (f fakePresetViewer) Presets() [12]adapters.PresetEntry { return f.entries }
```

- [ ] **Step 8: Run the full test suite to confirm rename is complete**

Run: `go vet ./... && go test ./internal/adapters/... ./internal/chassis/... -count=1`

Expected: PASS. Any remaining `BundledPresets()` call sites surface as compile errors here.

- [ ] **Step 9: Commit**

```bash
git add internal/adapters/preset.go internal/adapters/preset_test.go internal/adapters/streams/preset.go internal/adapters/streams/preset_test.go internal/chassis/data.go internal/chassis/chassis_test.go
git commit -m "refactor(adapters): rename PresetViewer.BundledPresets to Presets; add PresetEditor + PresetStarResult"
```

---

## Task 4: Streams `SourceAvailabilityViewer` Implementation

**Files:**
- Create: `internal/adapters/streams/source.go`
- Create: `internal/adapters/streams/source_test.go`

- [ ] **Step 1: Write failing tests**

Create [internal/adapters/streams/source_test.go](../../../internal/adapters/streams/source_test.go):

```go
package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsStreams(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "streams" {
		t.Errorf("SourceID = %q, want streams", v.SourceID())
	}
}

func TestConfigured_TracksIsEnabled(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	a.SetEnabled(true)
	if !a.Configured() {
		t.Errorf("Configured(enabled=true) = false, want true")
	}
	a.SetEnabled(false)
	if a.Configured() {
		t.Errorf("Configured(enabled=false) = true, want false")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters/streams -run "TestSourceID|TestConfigured" -v`

Expected: FAIL — methods undefined.

- [ ] **Step 3: Create the implementation**

Create [internal/adapters/streams/source.go](../../../internal/adapters/streams/source.go):

```go
package streams

// SourceID identifies this adapter to the chassis source-cluster.
// The chassis maps "streams" to the STREAMS lamp slot.
func (a *Adapter) SourceID() string { return "streams" }

// Configured returns whether the streams adapter is ready to receive
// casts. Streams is bundled, so configuration means "operator has
// enabled it in the bridge config"; remote-manifest readiness is
// orthogonal — disabled-but-ready and enabled-but-empty are both
// possible, but the chassis lamp distinguishes only dark vs amber.
func (a *Adapter) Configured() bool { return a.IsEnabled() }
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/adapters/streams -run "TestSourceID|TestConfigured" -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/source.go internal/adapters/streams/source_test.go
git commit -m "feat(streams): implement SourceAvailabilityViewer"
```

---

## Task 5: Plex `SourceAvailabilityViewer` Implementation

**Files:**
- Create: `internal/adapters/plex/source.go`
- Create: `internal/adapters/plex/source_test.go`

Per spec §"Per-adapter `SourceAvailabilityViewer` implementations": `SourceID() = "plex"`, `Configured() = IsEnabled() && IsLinked()`. Both accessors already exist on the plex adapter (`adapter.go:413` and `:421`).

- [ ] **Step 1: Write failing tests**

Create [internal/adapters/plex/source_test.go](../../../internal/adapters/plex/source_test.go):

```go
package plex

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsPlex(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "plex" {
		t.Errorf("SourceID = %q, want plex", v.SourceID())
	}
}

func TestConfigured_RequiresEnabledAndLinked(t *testing.T) {
	t.Parallel()
	// Cases: combine IsEnabled and IsLinked via SetEnabled and the
	// existing PIN-flow plumbing in this adapter's tests. If the plex
	// adapter test file already has a helper that returns a linked
	// instance, reuse it. Otherwise: directly poke the fields the
	// accessors read (mu-guarded private fields; pattern used by
	// adapter_interface_test.go).
	//
	// The matrix:
	//   enabled=false, linked=false → Configured=false
	//   enabled=true,  linked=false → Configured=false
	//   enabled=false, linked=true  → Configured=false
	//   enabled=true,  linked=true  → Configured=true
	//
	// Use whichever in-package helper or direct field access already
	// works in adapter_interface_test.go to flip these two states.
	type matrix struct {
		enabled, linked, want bool
	}
	for _, m := range []matrix{
		{false, false, false},
		{true, false, false},
		{false, true, false},
		{true, true, true},
	} {
		a := newPlexForConfiguredTest(t, m.enabled, m.linked)
		if got := a.Configured(); got != m.want {
			t.Errorf("Configured(enabled=%v, linked=%v) = %v, want %v",
				m.enabled, m.linked, got, m.want)
		}
	}
}
```

Add `newPlexForConfiguredTest` directly to `source_test.go` (same package, so it has access to unexported fields). The plex adapter's `IsLinked()` returns `a.cfg.TokenStore.AuthToken != ""`, so flipping the linked state means populating that field.

```go
func newPlexForConfiguredTest(t *testing.T, enabled, linked bool) *Adapter {
	t.Helper()
	a := &Adapter{
		cfg: AdapterConfig{TokenStore: &StoredData{}},
	}
	a.SetEnabled(enabled)
	if linked {
		a.mu.Lock()
		a.cfg.TokenStore.AuthToken = "test-token"
		a.mu.Unlock()
	}
	return a
}
```

If the actual `AdapterConfig` / `StoredData` field names differ from this sketch (inspect [internal/adapters/plex/adapter.go](../../../internal/adapters/plex/adapter.go) lines 30-50 for `AdapterConfig` and the `StoredData` definition), substitute the real names. The intent is fixed: construct a `*Adapter` where `IsLinked()` returns `linked` and `IsEnabled()` returns `enabled`, without invoking any network code paths.

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters/plex -run "TestSourceID|TestConfigured" -v`

Expected: FAIL — `SourceID`/`Configured` undefined.

- [ ] **Step 3: Create the implementation**

Create [internal/adapters/plex/source.go](../../../internal/adapters/plex/source.go):

```go
package plex

// SourceID identifies this adapter to the chassis source-cluster.
func (a *Adapter) SourceID() string { return "plex" }

// Configured returns whether the plex adapter is ready to receive
// casts. Plex requires both an enabled config flag AND a successful
// PIN-flow link to plex.tv (IsLinked() reflects token presence).
func (a *Adapter) Configured() bool { return a.IsEnabled() && a.IsLinked() }
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/adapters/plex -run "TestSourceID|TestConfigured" -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/plex/source.go internal/adapters/plex/source_test.go
git commit -m "feat(plex): implement SourceAvailabilityViewer"
```

---

## Task 6: Jellyfin `SourceAvailabilityViewer` Implementation

**Files:**
- Create: `internal/adapters/jellyfin/source.go`
- Create: `internal/adapters/jellyfin/source_test.go`

Per spec: `SourceID() = "jellyfin"`, `Configured() = IsEnabled() && IsLinked()`. The jellyfin adapter exposes a single `IsLinked()` accessor today (per-server visibility is out of scope for 3B).

- [ ] **Step 1: Write failing tests**

Create [internal/adapters/jellyfin/source_test.go](../../../internal/adapters/jellyfin/source_test.go):

```go
package jellyfin

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsJellyfin(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "jellyfin" {
		t.Errorf("SourceID = %q, want jellyfin", v.SourceID())
	}
}

func TestConfigured_RequiresEnabledAndLinked(t *testing.T) {
	t.Parallel()
	type matrix struct {
		enabled, linked, want bool
	}
	for _, m := range []matrix{
		{false, false, false},
		{true, false, false},
		{false, true, false},
		{true, true, true},
	} {
		a := newJellyfinForConfiguredTest(t, m.enabled, m.linked)
		if got := a.Configured(); got != m.want {
			t.Errorf("Configured(enabled=%v, linked=%v) = %v, want %v",
				m.enabled, m.linked, got, m.want)
		}
	}
}
```

Inspect [internal/adapters/jellyfin/adapter.go](../../../internal/adapters/jellyfin/adapter.go) lines 283-300 to confirm `IsEnabled()` and `IsLinked()` exist (they do per the spec audit). Add the helper `newJellyfinForConfiguredTest` matching the same pattern used in Task 5: flip the minimum state to make each accessor return the desired bool.

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters/jellyfin -run "TestSourceID|TestConfigured" -v`

Expected: FAIL.

- [ ] **Step 3: Create the implementation**

Create [internal/adapters/jellyfin/source.go](../../../internal/adapters/jellyfin/source.go):

```go
package jellyfin

// SourceID identifies this adapter to the chassis source-cluster.
func (a *Adapter) SourceID() string { return "jellyfin" }

// Configured returns whether the jellyfin adapter is ready to receive
// casts. Requires both the enabled config flag AND a successful
// server-link operation (IsLinked() reflects credentialed-server
// presence). Per-server visibility is out of scope for the 3B lamp.
func (a *Adapter) Configured() bool { return a.IsEnabled() && a.IsLinked() }
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/adapters/jellyfin -run "TestSourceID|TestConfigured" -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/jellyfin/source.go internal/adapters/jellyfin/source_test.go
git commit -m "feat(jellyfin): implement SourceAvailabilityViewer"
```

---

## Task 7: DLNA `SourceAvailabilityViewer` Implementation

**Files:**
- Create: `internal/adapters/dlna/source.go`
- Create: `internal/adapters/dlna/source_test.go`

Per spec: `SourceID() = "dlna"`, `Configured() = IsEnabled()`. DLNA has no linking concept (SSDP discovery is passive); the lamp goes dark when the operator disables the adapter, amber when enabled, green when actively serving (the `casting` state is derived elsewhere from `transport.AdapterRef`).

- [ ] **Step 1: Write failing tests**

Create [internal/adapters/dlna/source_test.go](../../../internal/adapters/dlna/source_test.go):

```go
package dlna

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsDLNA(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "dlna" {
		t.Errorf("SourceID = %q, want dlna", v.SourceID())
	}
}

func TestConfigured_TracksIsEnabled(t *testing.T) {
	t.Parallel()
	a := newDLNAForConfiguredTest(t, false)
	if a.Configured() {
		t.Errorf("Configured(enabled=false) = true, want false")
	}
	a = newDLNAForConfiguredTest(t, true)
	if !a.Configured() {
		t.Errorf("Configured(enabled=true) = false, want true")
	}
}
```

Add `newDLNAForConfiguredTest` to the same file or to an existing test-helper file in the package. Inspect [internal/adapters/dlna/adapter.go](../../../internal/adapters/dlna/adapter.go) line 449 (`IsEnabled()`) for the underlying field; flip it under the adapter's mutex to construct the test fixture.

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters/dlna -run "TestSourceID|TestConfigured" -v`

Expected: FAIL.

- [ ] **Step 3: Create the implementation**

Create [internal/adapters/dlna/source.go](../../../internal/adapters/dlna/source.go):

```go
package dlna

// SourceID identifies this adapter to the chassis source-cluster.
func (a *Adapter) SourceID() string { return "dlna" }

// Configured returns whether the dlna adapter is ready to serve.
// SSDP discovery is passive — there is no link state — so configured
// means "operator enabled it in the bridge config." The lamp's
// .casting state is derived from transport.AdapterRef elsewhere.
func (a *Adapter) Configured() bool { return a.IsEnabled() }
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/adapters/dlna -run "TestSourceID|TestConfigured" -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/dlna/source.go internal/adapters/dlna/source_test.go
git commit -m "feat(dlna): implement SourceAvailabilityViewer"
```

---

## Task 8: Streams `StreamsCatalogViewer` and `StreamsCaster` Implementation

**Files:**
- Create: `internal/adapters/streams/catalog.go`
- Create: `internal/adapters/streams/catalog_test.go`
- Modify: `internal/adapters/streams/assets.go` (add lookup tables)

The chassis catalog is filtered to exactly the three bundled-mockup providers. `CastChannel` returns typed `*QuickCastError` so HTTP handlers can map status/chip.

- [ ] **Step 1: Write failing tests**

Create [internal/adapters/streams/catalog_test.go](../../../internal/adapters/streams/catalog_test.go):

```go
package streams

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestCatalog_ReturnsThreeBundledProviders(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	cat := a.Catalog()
	if len(cat) != 3 {
		t.Fatalf("len(Catalog) = %d, want 3", len(cat))
	}
	gotIDs := []string{cat[0].ID, cat[1].ID, cat[2].ID}
	wantIDs := []string{"mtv-rewind", "cartoon-rewind", "toonami-aftermath"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Errorf("Catalog[%d].ID = %q, want %q", i, gotIDs[i], wantIDs[i])
		}
	}
}

func TestCatalog_BadgesMatchTable(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		want, ok := providerBadges[p.ID]
		if !ok {
			t.Errorf("provider %q missing from providerBadges", p.ID)
			continue
		}
		if p.BadgeLabel != want.Label || p.BadgeClass != want.Class {
			t.Errorf("Catalog[%q] badge = (%q, %q), want (%q, %q)",
				p.ID, p.BadgeLabel, p.BadgeClass, want.Label, want.Class)
		}
	}
}

func TestCatalog_ToonamiIsLiveAndChannelsLive(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		if p.ID != "toonami-aftermath" {
			continue
		}
		if !p.Live {
			t.Errorf("toonami-aftermath provider.Live = false, want true")
		}
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if !c.Live {
					t.Errorf("toonami-aftermath channel %q Live = false, want true (inherits provider.Live)", c.ID)
				}
			}
		}
	}
}

func TestCatalog_MTVNotLive(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		if p.ID != "mtv-rewind" {
			continue
		}
		if p.Live {
			t.Errorf("mtv-rewind provider.Live = true, want false (youtube-channel-json)")
		}
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if c.Live {
					t.Errorf("mtv-rewind channel %q Live = true, want false", c.ID)
				}
			}
		}
	}
}

func TestCatalog_PlayModeUppercased(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if c.PlayMode == "" {
					continue
				}
				if c.PlayMode != strings.ToUpper(c.PlayMode) {
					t.Errorf("channel %q PlayMode = %q, want uppercased", c.ID, c.PlayMode)
				}
			}
		}
	}
}

func TestCatalog_BundledProviderListIntegrity(t *testing.T) {
	t.Parallel()
	if len(bundledChassisCatalogProviderIDs) != 3 {
		t.Fatalf("bundledChassisCatalogProviderIDs len = %d, want 3", len(bundledChassisCatalogProviderIDs))
	}
	manifest := bundledManifest()
	providers := map[string]bool{}
	for _, p := range manifest.Providers {
		providers[p.ID] = true
	}
	for _, id := range bundledChassisCatalogProviderIDs {
		if !providers[id] {
			t.Errorf("bundledChassisCatalogProviderIDs entry %q not in bundled manifest", id)
		}
		if _, ok := providerBadges[id]; !ok {
			t.Errorf("bundledChassisCatalogProviderIDs entry %q missing from providerBadges", id)
		}
	}
}

func TestCatalog_BeforeStartReturnsBundled(t *testing.T) {
	t.Parallel()
	// Adapter constructed with New but Start NOT called: Catalog must
	// still return the bundled providers (local-only bootstrap, no network).
	a, _ := newTestAdapter(t)
	cat := a.Catalog()
	if len(cat) != 3 {
		t.Fatalf("pre-Start Catalog len = %d, want 3", len(cat))
	}
}

func TestCastChannel_DisabledReturnsNotReady(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(false)
	err := a.CastChannel(context.Background(), "mtv-rewind", "80s")
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusServiceUnavailable || qerr.Chip != "NOT READY" {
		t.Errorf("qerr = %+v, want Status=503 Chip=NOT READY", qerr)
	}
}

func TestCastChannel_UnknownReturnsNotFound(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(true)
	err := a.CastChannel(context.Background(), "nonexistent", "x")
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusNotFound || qerr.Chip != "NOT FOUND" {
		t.Errorf("qerr = %+v, want Status=404 Chip=NOT FOUND", qerr)
	}
}

func TestCastChannel_ValidForwardsToFakeCore(t *testing.T) {
	t.Parallel()
	// newTestAdapterWithFakeCore exists from 3A's preset_test.go suite.
	a, core := newTestAdapterWithFakeCore(t)
	if err := a.CastChannel(context.Background(), "cartoon-rewind", "heman"); err != nil {
		t.Fatalf("CastChannel err = %v", err)
	}
	if core.lastReq.Source != "streams" {
		t.Errorf("lastReq.Source = %q, want streams", core.lastReq.Source)
	}
	if !strings.HasPrefix(core.lastReq.AdapterRef, "streams:cartoon-rewind:heman:") {
		t.Errorf("lastReq.AdapterRef = %q, want prefix streams:cartoon-rewind:heman:", core.lastReq.AdapterRef)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters/streams -run "TestCatalog|TestCastChannel" -v`

Expected: FAIL — Catalog, CastChannel, bundledChassisCatalogProviderIDs, providerBadges all undefined.

- [ ] **Step 3: Add the lookup tables to `assets.go`**

Append to [internal/adapters/streams/assets.go](../../../internal/adapters/streams/assets.go) (after the `bundledChassisPresets` block at line 262):

```go

// bundledChassisCatalogProviderIDs is the subset of bundledManifest()
// providers the chassis catalog drawer exposes. Remote/cached manifest
// providers stay available to URL resolution and /ui/streams/*, but
// the receiver chassis is intentionally limited to the 3 mockup
// providers in 3B. A future spec can lift this restriction once
// per-provider catalog browsing rules exist.
var bundledChassisCatalogProviderIDs = []string{
	"mtv-rewind",
	"cartoon-rewind",
	"toonami-aftermath",
}

// providerBadges holds the small-glyph badge metadata for each chassis
// catalog provider. Mirrors the mockup's .ic glyph rendering (label =
// short uppercase string; class = CSS hook for color/treatment).
var providerBadges = map[string]struct {
	Label string
	Class string
}{
	"mtv-rewind":        {"MTV", "mtv"},
	"cartoon-rewind":    {"CART", "cartoon"},
	"toonami-aftermath": {"TOON", "toonami"},
}
```

- [ ] **Step 4: Create `catalog.go`**

Create [internal/adapters/streams/catalog.go](../../../internal/adapters/streams/catalog.go):

```go
package streams

import (
	"context"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

// Catalog returns the chassis-shaped view of the bundled streams
// providers. Called per chassis snapshot (4 Hz) — keep it allocation-
// light. The filter to bundledChassisCatalogProviderIDs means remote
// or cached manifest providers do NOT appear in /receiver in 3B.
//
// Safe to call before Adapter.Start: definitions/catalogs are seeded
// from bundledManifest() if a.definitions is empty.
func (a *Adapter) Catalog() []adapters.CatalogProvider {
	defs, catalogs := a.chassisCatalogSnapshot()
	out := make([]adapters.CatalogProvider, 0, len(bundledChassisCatalogProviderIDs))
	for _, id := range bundledChassisCatalogProviderIDs {
		def, ok := defs[id]
		if !ok {
			continue
		}
		cat, _ := catalogs[id]
		badge := providerBadges[id]
		live := def.Type == directStreamsProviderType
		p := adapters.CatalogProvider{
			ID:             def.ID,
			DisplayName:    def.DisplayName,
			BadgeLabel:     badge.Label,
			BadgeClass:     badge.Class,
			Live:           live,
			DefaultChannel: def.DefaultChannel,
		}
		channelByGroup := groupChannels(def, cat)
		for _, g := range def.Groups {
			cg := adapters.CatalogGroup{ID: g.ID, Name: g.Name}
			for _, ch := range channelByGroup[g.ID] {
				cg.Channels = append(cg.Channels, adapters.CatalogChannel{
					ID:       ch.ID,
					Name:     ch.Name,
					PlayMode: strings.ToUpper(string(ch.PlayMode)),
					Live:     live, // inherit provider.Live
				})
			}
			p.Groups = append(p.Groups, cg)
		}
		out = append(out, p)
	}
	return out
}

// chassisCatalogSnapshot returns the definitions and catalogs maps,
// seeding from bundledManifest if the adapter has not yet completed
// Start-time installation. Always returns non-empty maps.
func (a *Adapter) chassisCatalogSnapshot() (map[string]ProviderDefinition, map[string]ProviderCatalog) {
	a.mu.Lock()
	if len(a.definitions) > 0 {
		defs := make(map[string]ProviderDefinition, len(a.definitions))
		cats := make(map[string]ProviderCatalog, len(a.catalogs))
		for id, d := range a.definitions {
			defs[id] = d
		}
		for id, c := range a.catalogs {
			cats[id] = c
		}
		a.mu.Unlock()
		return defs, cats
	}
	a.mu.Unlock()

	// Local-only bootstrap: no remote fetch on a chassis render path.
	m := bundledManifest()
	defs := make(map[string]ProviderDefinition, len(m.Providers))
	for _, p := range m.Providers {
		defs[p.ID] = p
	}
	return defs, nil
}

// groupChannels indexes a provider's channels by GroupID. When a
// catalog is present it wins (catalogs may add per-channel items not
// in the static definition); otherwise the definition's ChannelDefinition
// slice is used directly.
func groupChannels(def ProviderDefinition, cat ProviderCatalog) map[string][]ChannelDefinition {
	by := map[string][]ChannelDefinition{}
	for _, ch := range def.Channels {
		by[ch.GroupID] = append(by[ch.GroupID], ch)
	}
	return by
}

// CastChannel starts a Streams cast for the (provider, channel) pair
// clicked in the catalog drawer. Returns typed *QuickCastError so the
// chassis emits the correct status + chip pair.
//
// Deliberate divergence from CastPreset (3A): validatePlayRequest
// errors here wrap to 404 NOT FOUND because the input came from a
// user-clicked catalog card that can legitimately point to a stale
// channel if the catalog reloaded between page render and click. The
// preset path treats the same error as 500 because every bundled slot
// is asserted to resolve.
func (a *Adapter) CastChannel(ctx context.Context, providerID, channelID string) error {
	if !a.IsEnabled() {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams adapter is disabled",
		}
	}
	if err := a.ensureStartupSnapshot(ctx); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams catalog is not ready",
			Cause:   err,
		}
	}
	res := streamhandoff.Resolution{ProviderID: providerID, ChannelID: channelID}
	if err := a.validatePlayRequest(res); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusNotFound,
			Chip:    "NOT FOUND",
			Message: err.Error(),
			Cause:   err,
		}
	}
	_, err := a.StartResolvedStream(ctx, res)
	return err
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams -run "TestCatalog|TestCastChannel" -v`

Expected: PASS.

- [ ] **Step 6: Verify nothing else broke**

Run: `go vet ./internal/adapters/streams/... && go test ./internal/adapters/streams/... -count=1`

Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/streams/catalog.go internal/adapters/streams/catalog_test.go internal/adapters/streams/assets.go
git commit -m "feat(streams): implement StreamsCatalogViewer and StreamsCaster"
```

---

## Task 9: Streams `presetStore` — In-Memory + File-Persisted 12-Slot Array

**Files:**
- Create: `internal/adapters/streams/preset_store.go`
- Create: `internal/adapters/streams/preset_store_test.go`
- Modify: `internal/adapters/streams/adapter.go` (init `presetStore` in `New`)

The store owns the 12-slot array, the path, and the mutation methods. Display fields (Title/BadgeLabel/BadgeClass/Live) are derived from the catalog at load time; only the persistent triple (Slot/ProviderID/ChannelID) is on disk.

- [ ] **Step 1: Write failing tests**

Create [internal/adapters/streams/preset_store_test.go](../../../internal/adapters/streams/preset_store_test.go):

```go
package streams

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func newStoreForTest(t *testing.T) (*presetStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "chassis_presets.json")
	st, err := newPresetStore(path, defaultCatalogResolver(t))
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	return st, path
}

// defaultCatalogResolver returns a function the store uses to validate
// (provider, channel) pairs against the bundled streams catalog.
// Implementation detail: the store takes a catalogResolver func at
// construction so unit tests don't depend on a full Adapter.
func defaultCatalogResolver(t *testing.T) catalogResolver {
	t.Helper()
	m := bundledManifest()
	return func(providerID, channelID string) (adapters.PresetEntry, bool) {
		for _, p := range m.Providers {
			if p.ID != providerID {
				continue
			}
			for _, c := range p.Channels {
				if c.ID != channelID {
					continue
				}
				badge := providerBadges[p.ID]
				return adapters.PresetEntry{
					ProviderID: p.ID,
					ChannelID:  c.ID,
					Title:      c.Name,
					BadgeLabel: badge.Label,
					BadgeClass: badge.Class,
					Live:       p.Type == directStreamsProviderType,
				}, true
			}
		}
		return adapters.PresetEntry{}, false
	}
}

func TestPresetStore_LoadMissingFileSeedsFromBundled(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	if _, err := os.Stat(path); err == nil {
		t.Errorf("file %s should not exist on first load", path)
	}
	snap := st.Snapshot()
	filled := 0
	for _, e := range snap {
		if e.ProviderID != "" {
			filled++
		}
	}
	if filled != 12 {
		t.Errorf("seeded snapshot filled = %d, want 12 (from bundledChassisPresets)", filled)
	}
}

func TestPresetStore_SetStarredAddFillsFirstEmpty(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// Clear slot 7 so we know which one will be hit by the add.
	if _, err := st.SetStarred("cartoon-rewind", "loonytunes", false); err != nil {
		t.Fatalf("remove pre-condition: %v", err)
	}
	res, err := st.SetStarred("mtv-rewind", "amp", true)
	if err != nil {
		t.Fatalf("SetStarred add: %v", err)
	}
	if !res.Starred || res.Slot != 7 {
		t.Errorf("res = %+v, want Starred=true Slot=7", res)
	}
}

func TestPresetStore_SetStarredAddExistingNoop(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// "1stday" is already in slot 1 from the seeded defaults.
	res, err := st.SetStarred("mtv-rewind", "1stday", true)
	if err != nil {
		t.Fatalf("SetStarred add existing: %v", err)
	}
	if !res.Starred || res.Slot != 1 {
		t.Errorf("res = %+v, want Starred=true Slot=1 (no-op)", res)
	}
}

func TestPresetStore_SetStarredAddBankFull(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// Seeded snapshot is exactly 12 filled.
	_, err := st.SetStarred("mtv-rewind", "amp", true)
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusConflict || qerr.Chip != "BANK FULL" {
		t.Errorf("qerr = %+v, want Status=409 Chip=BANK FULL", qerr)
	}
}

func TestPresetStore_SetStarredRemoveExisting(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// "1stday" is in slot 1 from defaults.
	res, err := st.SetStarred("mtv-rewind", "1stday", false)
	if err != nil {
		t.Fatalf("SetStarred remove: %v", err)
	}
	if res.Starred {
		t.Errorf("res.Starred = true, want false")
	}
	if len(res.Cleared) != 1 || res.Cleared[0] != 1 {
		t.Errorf("res.Cleared = %v, want [1]", res.Cleared)
	}
}

func TestPresetStore_SetStarredRemoveAbsentNoop(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	res, err := st.SetStarred("mtv-rewind", "amp", false)
	if err != nil {
		t.Fatalf("SetStarred remove absent: %v", err)
	}
	if res.Starred {
		t.Errorf("res.Starred = true, want false")
	}
	if len(res.Cleared) != 0 {
		t.Errorf("res.Cleared = %v, want empty", res.Cleared)
	}
}

func TestPresetStore_UnknownChannelReturnsNotFound(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	_, err := st.SetStarred("mtv-rewind", "nonexistent", true)
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusNotFound || qerr.Chip != "NOT FOUND" {
		t.Errorf("qerr = %+v, want Status=404 Chip=NOT FOUND", qerr)
	}
}

func TestPresetStore_MoveSwap(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	pre := st.Snapshot()
	if err := st.Move(1, 7); err != nil {
		t.Fatalf("Move: %v", err)
	}
	post := st.Snapshot()
	if post[0].ChannelID != pre[6].ChannelID {
		t.Errorf("slot 1 channel = %q, want %q (was in slot 7)", post[0].ChannelID, pre[6].ChannelID)
	}
	if post[6].ChannelID != pre[0].ChannelID {
		t.Errorf("slot 7 channel = %q, want %q (was in slot 1)", post[6].ChannelID, pre[0].ChannelID)
	}
}

func TestPresetStore_MoveNoOpFromEqualsTo(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	// File should NOT be written for a no-op move.
	if err := st.Move(3, 3); err != nil {
		t.Fatalf("Move(3,3): %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("no-op Move should not write the file")
	}
}

func TestPresetStore_MoveOutOfRange(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	for _, pair := range []struct{ from, to int }{
		{0, 1}, {1, 0}, {13, 1}, {1, 13}, {-1, 1}, {1, -1},
	} {
		err := st.Move(pair.from, pair.to)
		var qerr *adapters.QuickCastError
		if !errors.As(err, &qerr) {
			t.Errorf("Move(%d,%d) err = %v, want *QuickCastError", pair.from, pair.to, err)
			continue
		}
		if qerr.Status != http.StatusBadRequest || qerr.Chip != "BAD SLOT" {
			t.Errorf("Move(%d,%d) qerr = %+v, want Status=400 Chip=BAD SLOT", pair.from, pair.to, qerr)
		}
	}
}

func TestPresetStore_AtomicWriteVisible(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	if _, err := st.SetStarred("mtv-rewind", "1stday", false); err != nil {
		t.Fatalf("SetStarred remove: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after mutation: %v", err)
	}
	var doc struct {
		Version int                 `json:"version"`
		Slots   []persistedPresetIO `json:"slots"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1", doc.Version)
	}
	// slot 1 was cleared, so the persisted slice should NOT contain slot=1.
	for _, s := range doc.Slots {
		if s.Slot == 1 {
			t.Errorf("file still contains slot 1 after clear: %+v", s)
		}
	}
}

func TestPresetStore_LoadStaleReferencesDropped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "chassis_presets.json")
	// Pre-seed file with a stale reference (provider doesn't exist) plus one valid entry.
	body := `{"version":1,"slots":[{"slot":1,"provider":"gone","channel":"x"},{"slot":2,"provider":"mtv-rewind","channel":"80s"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, err := newPresetStore(path, defaultCatalogResolver(t))
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	snap := st.Snapshot()
	if snap[0].ProviderID != "" {
		t.Errorf("snap[0] = %+v, want empty (stale ref dropped)", snap[0])
	}
	if snap[1].ProviderID != "mtv-rewind" || snap[1].ChannelID != "80s" {
		t.Errorf("snap[1] = %+v, want mtv-rewind/80s", snap[1])
	}
}

func TestPresetStore_LoadParseErrorFallsBackToBundled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "chassis_presets.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, err := newPresetStore(path, defaultCatalogResolver(t))
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	snap := st.Snapshot()
	filled := 0
	for _, e := range snap {
		if e.ProviderID != "" {
			filled++
		}
	}
	if filled != 12 {
		t.Errorf("parse-error fallback filled = %d, want 12 (bundled defaults)", filled)
	}
}

func TestPresetStore_SnapshotSlotsAre1Indexed(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	snap := st.Snapshot()
	for i, e := range snap {
		if e.Slot != i+1 {
			t.Errorf("snap[%d].Slot = %d, want %d", i, e.Slot, i+1)
		}
	}
}

func TestPresetStore_NoOpRemoveDoesNotWriteFile(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	if _, err := st.SetStarred("mtv-rewind", "amp", false); err != nil {
		t.Fatalf("remove absent: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("no-op remove should not write file")
	}
	_ = sort.Search // import sentinel; can be removed once tests grow
	_ = context.Background
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters/streams -run TestPresetStore -v`

Expected: FAIL — `presetStore`, `newPresetStore`, `catalogResolver`, `persistedPresetIO` all undefined.

- [ ] **Step 3: Create `preset_store.go`**

Create [internal/adapters/streams/preset_store.go](../../../internal/adapters/streams/preset_store.go):

```go
package streams

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// catalogResolver maps (providerID, channelID) to a fully populated
// PresetEntry (or returns ok=false for stale references). The store
// uses this to:
//   1. Hydrate display fields on load.
//   2. Validate SetStarred(true) inputs before mutating.
type catalogResolver func(providerID, channelID string) (adapters.PresetEntry, bool)

// persistedPresetIO is the on-disk representation of one slot. Display
// fields (Title/BadgeLabel/BadgeClass/Live) are NOT persisted — they
// derive from the catalog at load time. Only the persistent triple
// (Slot, Provider, Channel) is stable across catalog reloads.
type persistedPresetIO struct {
	Slot     int    `json:"slot"`
	Provider string `json:"provider"`
	Channel  string `json:"channel"`
}

type persistedFile struct {
	Version int                 `json:"version"`
	Slots   []persistedPresetIO `json:"slots"`
}

const presetStoreFileVersion = 1

// presetStore owns the 12-slot in-memory preset array and the file
// persistence side-effect. All mutation methods return values that the
// HTTP handler can shape into the wire envelope; the store itself does
// not know about HTTP.
type presetStore struct {
	mu       sync.Mutex
	path     string
	resolve  catalogResolver
	slots    [12]adapters.PresetEntry // index = slot-1
	saveErrs *onePerInstanceLog       // bounded log noise for save errors
}

// newPresetStore reads the file at path (if present), validates each
// entry against the resolver (dropping stale references with an info
// log), and returns a populated store. A missing or unreadable file
// seeds from bundledChassisPresets in memory without writing the file —
// lazy write occurs on first successful mutation.
func newPresetStore(path string, resolve catalogResolver) (*presetStore, error) {
	st := &presetStore{
		path:     path,
		resolve:  resolve,
		saveErrs: &onePerInstanceLog{},
	}
	for i := 0; i < 12; i++ {
		st.slots[i] = adapters.PresetEntry{Slot: i + 1}
	}

	body, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.seedFromBundled()
		return st, nil
	case err != nil:
		// Non-NotExist read failure (permissions, etc.): seed from bundled
		// and log. Don't fail adapter init — the chassis must still serve.
		slog.Warn("chassis_presets: read failed; using bundled defaults", "err", err, "path", path)
		st.seedFromBundled()
		return st, nil
	}

	var doc persistedFile
	if err := json.Unmarshal(body, &doc); err != nil {
		slog.Warn("chassis_presets: parse failed; using bundled defaults", "err", err, "path", path)
		st.seedFromBundled()
		return st, nil
	}
	if doc.Version != presetStoreFileVersion {
		slog.Warn("chassis_presets: version mismatch; using bundled defaults",
			"got", doc.Version, "want", presetStoreFileVersion, "path", path)
		st.seedFromBundled()
		return st, nil
	}

	dropped := []string{}
	for _, p := range doc.Slots {
		if p.Slot < 1 || p.Slot > 12 {
			continue
		}
		if entry, ok := resolve(p.Provider, p.Channel); ok {
			entry.Slot = p.Slot
			st.slots[p.Slot-1] = entry
		} else {
			dropped = append(dropped, fmt.Sprintf("%s:%s", p.Provider, p.Channel))
		}
	}
	if len(dropped) > 0 {
		slog.Info("chassis_presets: dropped stale references on startup",
			"count", len(dropped), "refs", dropped)
	}
	return st, nil
}

// seedFromBundled fills the in-memory slots from the bundled preset
// literal without writing to disk. Lazy-write semantics: the file is
// created only when the user's first mutation succeeds.
func (s *presetStore) seedFromBundled() {
	for i, p := range bundledChassisPresets {
		// Re-derive display fields via the resolver so badge metadata
		// stays consistent with the catalog (defense against assets.go
		// drift between bundledChassisPresets and the manifest).
		entry, ok := s.resolve(p.ProviderID, p.ChannelID)
		if !ok {
			// Stale bundled entry — keep the persistent triple but show
			// the literal fields. Should not happen if assets.go is
			// internally consistent.
			s.slots[i] = p
			continue
		}
		entry.Slot = i + 1
		s.slots[i] = entry
	}
}

// Snapshot returns a value copy of the 12-slot array. The chassis
// reads this once per snapshot tick — keep it allocation-light.
func (s *presetStore) Snapshot() [12]adapters.PresetEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slots
}

// SetStarred applies the user's desired-state intent for one channel.
// Returns the slot index (when starred) or the list of cleared slots
// (when unstarred). Idempotent: starring an existing channel returns
// the current slot without writing; unstarring an absent channel
// returns empty Cleared without writing.
func (s *presetStore) SetStarred(providerID, channelID string, starred bool) (adapters.PresetStarResult, error) {
	if starred {
		entry, ok := s.resolve(providerID, channelID)
		if !ok {
			return adapters.PresetStarResult{}, &adapters.QuickCastError{
				Status:  http.StatusNotFound,
				Chip:    "NOT FOUND",
				Message: fmt.Sprintf("streams: %s:%s not in catalog", providerID, channelID),
			}
		}
		s.mu.Lock()
		// Already in some slot? Return that slot, no write.
		for i, e := range s.slots {
			if e.ProviderID == providerID && e.ChannelID == channelID {
				s.mu.Unlock()
				return adapters.PresetStarResult{Starred: true, Slot: i + 1}, nil
			}
		}
		// Find first empty slot.
		target := -1
		for i, e := range s.slots {
			if e.ProviderID == "" {
				target = i
				break
			}
		}
		if target < 0 {
			s.mu.Unlock()
			return adapters.PresetStarResult{}, &adapters.QuickCastError{
				Status:  http.StatusConflict,
				Chip:    "BANK FULL",
				Message: "streams: preset bank is full",
			}
		}
		entry.Slot = target + 1
		s.slots[target] = entry
		s.persistLocked()
		s.mu.Unlock()
		return adapters.PresetStarResult{Starred: true, Slot: target + 1}, nil
	}

	// starred=false: clear all matching slots.
	s.mu.Lock()
	cleared := []int{}
	for i, e := range s.slots {
		if e.ProviderID == providerID && e.ChannelID == channelID {
			cleared = append(cleared, i+1)
			s.slots[i] = adapters.PresetEntry{Slot: i + 1}
		}
	}
	if len(cleared) > 0 {
		s.persistLocked()
	}
	s.mu.Unlock()
	sort.Ints(cleared)
	return adapters.PresetStarResult{Starred: false, Cleared: cleared}, nil
}

// Move swaps the contents of two slots. from==to is a no-op success
// (no file write). Either slot may be empty — swap semantics still
// apply, the empty slot becomes filled and vice versa.
func (s *presetStore) Move(from, to int) error {
	if from < 1 || from > 12 || to < 1 || to > 12 {
		return &adapters.QuickCastError{
			Status:  http.StatusBadRequest,
			Chip:    "BAD SLOT",
			Message: fmt.Sprintf("streams: move(%d,%d): slots must be 1..12", from, to),
		}
	}
	if from == to {
		return nil
	}
	s.mu.Lock()
	s.slots[from-1], s.slots[to-1] = s.slots[to-1], s.slots[from-1]
	s.slots[from-1].Slot = from
	s.slots[to-1].Slot = to
	s.persistLocked()
	s.mu.Unlock()
	return nil
}

// persistLocked writes the current in-memory state to s.path atomically
// (temp file in the same directory + os.Rename). Called with s.mu held.
// Errors are logged at one-per-instance cadence; the in-memory state
// is the source of truth for the current process, so a write failure
// does not roll the mutation back — the next successful write
// re-syncs.
func (s *presetStore) persistLocked() {
	doc := persistedFile{Version: presetStoreFileVersion}
	for _, e := range s.slots {
		if e.ProviderID == "" {
			continue
		}
		doc.Slots = append(doc.Slots, persistedPresetIO{
			Slot:     e.Slot,
			Provider: e.ProviderID,
			Channel:  e.ChannelID,
		})
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		s.saveErrs.Warn("chassis_presets: marshal failed", "err", err)
		return
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, "chassis_presets-*.json.tmp")
	if err != nil {
		s.saveErrs.Warn("chassis_presets: create temp failed", "err", err, "dir", dir)
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		s.saveErrs.Warn("chassis_presets: write temp failed", "err", err, "path", tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		s.saveErrs.Warn("chassis_presets: close temp failed", "err", err, "path", tmpPath)
		return
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		s.saveErrs.Warn("chassis_presets: rename failed", "err", err, "src", tmpPath, "dst", s.path)
		return
	}
}

// onePerInstanceLog is a tiny rate limiter so a misconfigured data
// dir doesn't spam the slog on every preset edit.
type onePerInstanceLog struct {
	once sync.Once
}

func (o *onePerInstanceLog) Warn(msg string, kv ...any) {
	o.once.Do(func() { slog.Warn(msg, kv...) })
}
```

- [ ] **Step 4: Wire `presetStore` into adapter `New`**

Edit [internal/adapters/streams/adapter.go](../../../internal/adapters/streams/adapter.go) to initialize the store inside `New`. Insert after the existing `a := &Adapter{...}` block (around line 100):

```go
	a.presetStore, err = newPresetStore(
		filepath.Join(cfg.Bridge.DataDir, "chassis_presets.json"),
		a.resolvePresetEntry,
	)
	if err != nil {
		return nil, fmt.Errorf("streams: preset store: %w", err)
	}
```

Add the `presetStore *presetStore` field to the `Adapter` struct (near the existing private fields around line 70-80).

Add the resolver method below `New`:

```go
// resolvePresetEntry is the catalogResolver bound to this adapter.
// Looks up (provider, channel) against the bundled chassis catalog
// (via Catalog) and returns the populated PresetEntry. Used by the
// presetStore for both load-time hydration and SetStarred validation.
func (a *Adapter) resolvePresetEntry(providerID, channelID string) (adapters.PresetEntry, bool) {
	for _, p := range a.Catalog() {
		if p.ID != providerID {
			continue
		}
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if c.ID == channelID {
					return adapters.PresetEntry{
						ProviderID: p.ID,
						ChannelID:  c.ID,
						Title:      c.Name,
						BadgeLabel: p.BadgeLabel,
						BadgeClass: p.BadgeClass,
						Live:       c.Live,
					}, true
				}
			}
		}
	}
	return adapters.PresetEntry{}, false
}
```

- [ ] **Step 5: Run tests to confirm pass**

Run: `go test ./internal/adapters/streams -run TestPresetStore -v && go vet ./internal/adapters/streams/...`

Expected: PASS.

- [ ] **Step 6: Re-run full streams suite**

Run: `go test ./internal/adapters/streams/... -count=1`

Expected: all green. If any previously passing test broke (e.g., a test that constructs an `Adapter{}` literal that now lacks an initialized `presetStore`), update those tests to use a helper that calls `New` properly, or guard nil `presetStore` access in `Presets()` / `SetPresetStarred` / `MovePreset` (Task 10).

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/streams/preset_store.go internal/adapters/streams/preset_store_test.go internal/adapters/streams/adapter.go
git commit -m "feat(streams): add presetStore with atomic file persistence"
```

---

## Task 10: Streams `Presets()`, `SetPresetStarred`, `MovePreset` Methods

**Files:**
- Modify: `internal/adapters/streams/preset.go`
- Modify: `internal/adapters/streams/preset_test.go` (extend with mutation tests)

Replace the literal-backed `Presets()` with a store-backed one, and add the two mutation methods as thin wrappers.

- [ ] **Step 1: Write failing tests**

Append to [internal/adapters/streams/preset_test.go](../../../internal/adapters/streams/preset_test.go):

```go
func TestSetPresetStarred_DelegatesToStore(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	// Remove an existing default, then add it back.
	res, err := a.SetPresetStarred(context.Background(), "mtv-rewind", "1stday", false)
	if err != nil {
		t.Fatalf("SetPresetStarred remove: %v", err)
	}
	if res.Starred || len(res.Cleared) != 1 {
		t.Errorf("remove res = %+v, want Starred=false Cleared=[1]", res)
	}
	res, err = a.SetPresetStarred(context.Background(), "mtv-rewind", "1stday", true)
	if err != nil {
		t.Fatalf("SetPresetStarred add: %v", err)
	}
	if !res.Starred || res.Slot != 1 {
		t.Errorf("add res = %+v, want Starred=true Slot=1", res)
	}
}

func TestMovePreset_DelegatesToStore(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	pre := a.Presets()
	if err := a.MovePreset(context.Background(), 1, 7); err != nil {
		t.Fatalf("MovePreset: %v", err)
	}
	post := a.Presets()
	if post[0].ChannelID != pre[6].ChannelID || post[6].ChannelID != pre[0].ChannelID {
		t.Errorf("swap incomplete: pre[1]=%q pre[7]=%q post[1]=%q post[7]=%q",
			pre[0].ChannelID, pre[6].ChannelID, post[0].ChannelID, post[6].ChannelID)
	}
}

func TestPresets_ReadsFromStore(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	// Snapshot before & after a mutation diverge — proves Presets reads
	// the store, not the bundledChassisPresets literal.
	pre := a.Presets()
	if _, err := a.SetPresetStarred(context.Background(), "mtv-rewind", "1stday", false); err != nil {
		t.Fatalf("SetPresetStarred: %v", err)
	}
	post := a.Presets()
	if pre[0].ProviderID == post[0].ProviderID {
		t.Errorf("Presets did not reflect mutation: pre[1]=%+v post[1]=%+v", pre[0], post[0])
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/adapters/streams -run "TestSetPresetStarred|TestMovePreset|TestPresets_ReadsFromStore" -v`

Expected: FAIL — methods undefined.

- [ ] **Step 3: Replace `preset.go`**

Replace [internal/adapters/streams/preset.go](../../../internal/adapters/streams/preset.go) contents with:

```go
package streams

import (
	"context"
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

// Presets returns the current 12-slot chassis preset bank snapshot
// from the store. Display fields are re-derived from the catalog at
// load time; only the persistent triple lives on disk.
func (a *Adapter) Presets() [12]adapters.PresetEntry {
	if a.presetStore == nil {
		return bundledChassisPresets
	}
	return a.presetStore.Snapshot()
}

// SetPresetStarred is the desired-state preset edit hook. The chassis
// HTTP handler validates inputs (non-empty provider/channel, strict
// "true"/"false" lexical form for starred) before forwarding here.
func (a *Adapter) SetPresetStarred(ctx context.Context, providerID, channelID string, starred bool) (adapters.PresetStarResult, error) {
	if a.presetStore == nil {
		return adapters.PresetStarResult{}, fmt.Errorf("streams: preset store not initialized")
	}
	return a.presetStore.SetStarred(providerID, channelID, starred)
}

// MovePreset swaps two slot contents. Out-of-range returns *QuickCastError{400, BAD SLOT}.
func (a *Adapter) MovePreset(ctx context.Context, from, to int) error {
	if a.presetStore == nil {
		return fmt.Errorf("streams: preset store not initialized")
	}
	return a.presetStore.Move(from, to)
}

// CastPreset starts a Streams cast for slot N (1-indexed). Reads the
// slot's (provider, channel) from the live store snapshot rather than
// the bundledChassisPresets literal so user edits take effect.
func (a *Adapter) CastPreset(ctx context.Context, slot int) error {
	if slot < 1 || slot > 12 {
		return &adapters.QuickCastError{
			Status:  http.StatusBadRequest,
			Chip:    "BAD SLOT",
			Message: fmt.Sprintf("streams: preset slot %d out of range", slot),
		}
	}
	if err := a.ensureStartupSnapshot(ctx); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams catalog is not ready",
			Cause:   err,
		}
	}
	snap := a.Presets()
	entry := snap[slot-1]
	if entry.ProviderID == "" {
		return &adapters.QuickCastError{
			Status:  http.StatusNotFound,
			Chip:    "NOT FOUND",
			Message: fmt.Sprintf("streams: preset slot %d is empty", slot),
		}
	}
	res := streamhandoff.Resolution{
		ProviderID: entry.ProviderID,
		ChannelID:  entry.ChannelID,
	}
	if err := a.validatePlayRequest(res); err != nil {
		return err
	}
	_, err := a.StartResolvedStream(ctx, res)
	return err
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/adapters/streams -count=1`

Expected: all green. The existing `TestCastPreset_*` tests still pass because the store-backed snapshot returns the same defaults as `bundledChassisPresets` on first run.

- [ ] **Step 5: Confirm cross-package vet**

Run: `go vet ./... && go test ./internal/adapters/... -count=1`

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/preset.go internal/adapters/streams/preset_test.go
git commit -m "feat(streams): back Presets with store; add SetPresetStarred and MovePreset"
```

---

## Task 11: Chassis `Config` Additions and `Server` Storage

**Files:**
- Modify: `internal/chassis/server.go`

Add four new Config fields and corresponding `*Server` fields. Wiring into `New` is a one-line each.

- [ ] **Step 1: Write failing test**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestConfig_AcceptsNew3BInterfaces(t *testing.T) {
	t.Parallel()
	// Compile-time conformance: this test fails to build if any of the
	// new fields are missing from chassis.Config.
	var _ = Config{
		Bridge:                    config.BridgeConfig{},
		Version:                   "x",
		StartedAt:                 time.Now(),
		StreamsCatalogViewer:      nil,
		StreamsCaster:             nil,
		PresetEditor:              nil,
		SourceAvailabilityViewers: nil,
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestConfig_AcceptsNew3BInterfaces -v`

Expected: FAIL — fields undefined.

- [ ] **Step 3: Extend `Config` struct**

In [internal/chassis/server.go](../../../internal/chassis/server.go) `type Config struct` (line 17-70), append after the existing `PresetCaster` field:

```go
	// StreamsCatalogViewer / StreamsCaster: the streams adapter wired
	// through these so the chassis can render the catalog drawer and
	// fire channel casts without importing internal/adapters/streams.
	StreamsCatalogViewer adapters.StreamsCatalogViewer
	StreamsCaster        adapters.StreamsCaster

	// PresetEditor: streams adapter for star-toggle and move operations.
	PresetEditor adapters.PresetEditor

	// SourceAvailabilityViewers: every adapter that implements the
	// interface, in registration order. main.go assembles the slice
	// from the registry. The chassis does NOT inspect the registry
	// directly for source-lamp state — passing the typed slice keeps
	// import_check_test.go happy.
	SourceAvailabilityViewers []adapters.SourceAvailabilityViewer
```

- [ ] **Step 4: Add corresponding `*Server` fields**

In the same file, extend the `type Server struct` block (line 73-100) with:

```go
	streamsCatalogViewer adapters.StreamsCatalogViewer
	streamsCaster        adapters.StreamsCaster
	presetEditor         adapters.PresetEditor
	sourceViewers        []adapters.SourceAvailabilityViewer
```

In `New(cfg Config) (*Server, error)`, wire them into the returned `*Server`:

```go
		streamsCatalogViewer: cfg.StreamsCatalogViewer,
		streamsCaster:        cfg.StreamsCaster,
		presetEditor:         cfg.PresetEditor,
		sourceViewers:        cfg.SourceAvailabilityViewers,
```

(Insert these lines in the existing `s := &Server{ ... }` literal, after `presetCaster` to keep the field-init order matching the struct declaration.)

- [ ] **Step 5: Run test to confirm pass**

Run: `go test ./internal/chassis -run TestConfig_AcceptsNew3BInterfaces -v`

Expected: PASS.

- [ ] **Step 6: Confirm full chassis package still vets and tests**

Run: `go vet ./internal/chassis/... && go test ./internal/chassis/... -count=1`

Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): wire StreamsCatalogViewer/StreamsCaster/PresetEditor/SourceAvailabilityViewers into Config"
```

---

## Task 12: Extend `SourceButton` + `applySourceLampState` Helper + `parseAdapterRefSource`

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/data_test.go` (extend if exists; create otherwise)

Add `Configured`/`Casting` fields to `SourceButton` and the helpers that populate them.

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/data_test.go](../../../internal/chassis/data_test.go) (create the file if it doesn't exist — header `package chassis`):

```go
package chassis

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakeSourceViewer struct {
	id, configured string
}

func (f fakeSourceViewer) SourceID() string { return f.id }
func (f fakeSourceViewer) Configured() bool { return f.configured == "yes" }

func TestParseAdapterRefSource_KnownPrefixes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ref, want string
	}{
		{"", ""},
		{"streams:mtv-rewind:80s:abc:def", "streams"},
		{"plex:server/key/123", "plex"},
		{"jellyfin:item/abc", "jellyfin"},
		{"dlna:urn:xyz", "dlna"},
		{"weird-no-prefix", ""},
		{"unknown:source:x", ""},
	}
	for _, c := range cases {
		got := parseAdapterRefSource(c.ref)
		if got != c.want {
			t.Errorf("parseAdapterRefSource(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestApplySourceLampState_LampSlotsDerivedFromViewersAndRef(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "STREAMS", Action: ""},
		{Label: "PLEX", Action: ""},
		{Label: "JELLYFIN", Action: ""},
		{Label: "DLNA", Action: ""},
		{Label: "AUX", Action: SourceActionAUXStart}, // AUX must NOT get Configured/Casting touched
	}}}
	viewers := []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
		fakeSourceViewer{id: "plex", configured: "no"},
		fakeSourceViewer{id: "jellyfin", configured: "yes"},
		fakeSourceViewer{id: "dlna", configured: "no"},
	}
	applySourceLampState(base, viewers, "streams:mtv-rewind:80s:abc:def")
	want := []struct {
		label                 string
		configured, casting   bool
	}{
		{"STREAMS", true, true},
		{"PLEX", false, false},
		{"JELLYFIN", true, false},
		{"DLNA", false, false},
		{"AUX", false, false}, // AUX path is the existing applyAUXSourceState — these stay zero here
	}
	for i, w := range want {
		got := base.Source.Buttons[i]
		if got.Label != w.label {
			t.Errorf("button[%d].Label = %q, want %q", i, got.Label, w.label)
		}
		if got.Configured != w.configured {
			t.Errorf("button[%d=%s].Configured = %v, want %v", i, w.label, got.Configured, w.configured)
		}
		if got.Casting != w.casting {
			t.Errorf("button[%d=%s].Casting = %v, want %v", i, w.label, got.Casting, w.casting)
		}
	}
}

func TestApplySourceLampState_EmptyRefClearsCasting(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "STREAMS", Action: "", Casting: true}, // stale from prior tick
	}}}
	applySourceLampState(base, []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
	}, "")
	if base.Source.Buttons[0].Casting {
		t.Errorf("Casting = true, want false (empty ref must clear)")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestParseAdapterRefSource|TestApplySourceLampState" -v`

Expected: FAIL — `parseAdapterRefSource`, `applySourceLampState`, `SourceButton.Configured`, `SourceButton.Casting` all undefined.

- [ ] **Step 3: Clear `Action` from non-AUX source buttons in `idleSnapshot`**

The existing `idleSnapshot` populates `Action: SourceActionStreams`, `SourceActionPlex`, `SourceActionJellyfin`, `SourceActionDLNA` on the four non-AUX buttons. The 3B template branch (`{{if eq .Action ""}} lamp {{else}} hw-btn`) needs `Action == ""` on those four to render as lamps. Strip the action from each:

In [internal/chassis/data.go](../../../internal/chassis/data.go) `idleSnapshot`, replace the existing `Source: SourceData{...}` block (around line 288-296):

```go
		Source: SourceData{
			Buttons: []SourceButton{
				{Label: "STREAMS", Active: true, Lit: false, Action: SourceActionStreams},
				{Label: "PLEX", Active: false, Lit: false, Action: SourceActionPlex},
				{Label: "JELLYFIN", Active: false, Lit: false, Action: SourceActionJellyfin},
				{Label: "DLNA", Active: false, Lit: false, Action: SourceActionDLNA},
				{Label: "AUX", Active: false, Lit: false, Action: SourceActionAUXStart},
			},
		},
```

with:

```go
		Source: SourceData{
			Buttons: []SourceButton{
				{Label: "STREAMS"},  // lamp slot — Action="" routes through applySourceLampState
				{Label: "PLEX"},
				{Label: "JELLYFIN"},
				{Label: "DLNA"},
				{Label: "AUX", Action: SourceActionAUXStart},
			},
		},
```

Also delete the four now-unused constants from `data.go` (around line 56-60):

```go
const (
	SourceActionStreams  = "streams"
	SourceActionPlex     = "plex"
	SourceActionJellyfin = "jellyfin"
	SourceActionDLNA     = "dlna"
	SourceActionAUXStart = "aux-start"
)
```

becomes:

```go
const (
	SourceActionAUXStart = "aux-start"
)
```

Check for any external references to the deleted constants. If `cmd/mister-groovy-relay/main.go` or any other file uses `chassis.SourceActionStreams` etc., the build will fail until those references are removed. Use:

```bash
grep -rn "SourceActionStreams\|SourceActionPlex\|SourceActionJellyfin\|SourceActionDLNA" .
```

If any results outside `internal/chassis/data.go` itself surface, delete or update those references.

The `vfd-live.js` source-event handler queries `[data-source-action="..."]` to update each AUX button's Active/Lit. Lamps have no `data-source-action` attribute (the new template doesn't emit one), so the existing handler naturally skips them — no JS change needed here.

The `applySourceLampState` helper (added in Step 5 below) matches lamp slots by `strings.ToLower(b.Label)` against viewer `SourceID()`, NOT by `Action`. So clearing Action does not break the lamp-state derivation.

- [ ] **Step 4: Extend `SourceButton`**

In [internal/chassis/data.go](../../../internal/chassis/data.go), modify the `SourceButton` struct (around line 71-78):

```go
// SourceButton represents one entry in the source cluster. AUX renders
// as an hw-btn (Action != ""); STREAMS/PLEX/JELLYFIN/DLNA render as
// indicator lamps (Action == ""). The lamp fields Configured and
// Casting drive three visual states:
//
//   Configured=false → unavailable (lamp dark)
//   Configured=true, Casting=false → idle (lamp dim amber)
//   Configured=true, Casting=true  → active (lamp bright green)
//
// Active / Lit / Unavailable / InputID remain in use for AUX only;
// lamp slots leave them at the zero value.
type SourceButton struct {
	Label       string
	Active      bool
	Lit         bool
	Unavailable bool
	Action      string
	InputID     string

	Configured bool // lamp slots only — adapter is linked/enabled
	Casting    bool // lamp slots only — this source matches transport.AdapterRef
}
```

- [ ] **Step 5: Add `parseAdapterRefSource` and `applySourceLampState` helpers**

Append to the same file (after `parseStreamsAdapterRef`):

```go
// parseAdapterRefSource extracts the leading source identifier from
// an AdapterRef ("streams:..." → "streams", "plex:..." → "plex",
// etc.). Returns "" for empty or unknown-format refs. Mirrors the
// chassis source-cluster lamp identifier set.
func parseAdapterRefSource(ref string) string {
	if ref == "" {
		return ""
	}
	colon := strings.IndexByte(ref, ':')
	if colon <= 0 {
		return ""
	}
	switch ref[:colon] {
	case "streams", "plex", "jellyfin", "dlna":
		return ref[:colon]
	}
	return ""
}

// applySourceLampState populates Configured + Casting on every lamp
// slot in base.Source.Buttons (Action == "") using the supplied viewers
// for Configured() and the AdapterRef prefix for Casting. AUX
// (Action != "") is left untouched — applyAUXSourceState owns that
// state. Safe to call with nil viewers; lamps stay at zero (lamp dark).
func applySourceLampState(base *ReceiverPageData, viewers []adapters.SourceAvailabilityViewer, adapterRef string) {
	if base == nil {
		return
	}
	castingSource := parseAdapterRefSource(adapterRef)
	configured := map[string]bool{}
	for _, v := range viewers {
		if v == nil {
			continue
		}
		configured[v.SourceID()] = v.Configured()
	}
	for i := range base.Source.Buttons {
		b := &base.Source.Buttons[i]
		if b.Action != "" {
			// AUX slot — leave alone.
			continue
		}
		id := strings.ToLower(b.Label)
		b.Configured = configured[id]
		b.Casting = id == castingSource && id != ""
	}
}
```

- [ ] **Step 6: Run tests to confirm pass**

Run: `go test ./internal/chassis -run "TestParseAdapterRefSource|TestApplySourceLampState" -v`

Expected: PASS.

- [ ] **Step 7: Verify nothing else broke**

Run: `go vet ./... && go test ./internal/chassis -count=1`

Expected: green. If any pre-existing test asserts the deleted `SourceActionStreams`/`Plex`/`Jellyfin`/`DLNA` constants directly, update those tests to either remove the assertion or check that the lamp slot's `Action == ""`.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/data.go internal/chassis/data_test.go
git commit -m "feat(chassis): clear Action on lamp slots; extend SourceButton with Configured/Casting"
```

---

## Task 13: `CatalogData` Types + `buildCatalogData` Helper

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/data_test.go`

Add the chassis-shaped catalog data model with `ProviderIndex`/`GroupIndex` helper methods (template-callable) and the snapshot builder.

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/data_test.go](../../../internal/chassis/data_test.go):

```go
func TestBuildCatalogData_ChannelsCarryStarredFromPresets(t *testing.T) {
	t.Parallel()
	cat := []adapters.CatalogProvider{
		{
			ID: "mtv-rewind", DisplayName: "MTV Rewind",
			BadgeLabel: "MTV", BadgeClass: "mtv", Live: false,
			DefaultChannel: "1stday",
			Groups: []adapters.CatalogGroup{
				{ID: "shows", Name: "MTV Shows", Channels: []adapters.CatalogChannel{
					{ID: "1stday", Name: "First Day on MTV", PlayMode: "SEQ", Live: false},
					{ID: "amp", Name: "AMP", PlayMode: "SHUFFLE", Live: false},
				}},
			},
		},
	}
	presets := [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday"},
		{Slot: 2}, {Slot: 3}, {Slot: 4}, {Slot: 5}, {Slot: 6},
		{Slot: 7}, {Slot: 8}, {Slot: 9}, {Slot: 10}, {Slot: 11}, {Slot: 12},
	}
	data := buildCatalogData(cat, presets, "streams:mtv-rewind:1stday:abc:def")
	if data.ActiveProviderID != "mtv-rewind" {
		t.Errorf("ActiveProviderID = %q, want mtv-rewind", data.ActiveProviderID)
	}
	if data.TotalChannels != 2 {
		t.Errorf("TotalChannels = %d, want 2", data.TotalChannels)
	}
	first := data.Providers[0].Groups[0].Channels[0]
	if !first.Starred || first.PresetSlot != 1 {
		t.Errorf("first channel Starred/PresetSlot = (%v, %d), want (true, 1)", first.Starred, first.PresetSlot)
	}
	if !first.Tuned {
		t.Errorf("first channel Tuned = false, want true")
	}
	second := data.Providers[0].Groups[0].Channels[1]
	if second.Starred || second.PresetSlot != 0 {
		t.Errorf("second channel Starred/PresetSlot = (%v, %d), want (false, 0)", second.Starred, second.PresetSlot)
	}
	if second.Tuned {
		t.Errorf("second channel Tuned = true, want false")
	}
}

func TestCatalogData_ProviderIndexFallback(t *testing.T) {
	t.Parallel()
	data := CatalogData{
		Providers: []CatalogProviderTab{{ID: "a"}, {ID: "b"}},
		ActiveProviderID: "missing",
	}
	// ProviderIndex with a missing ID must return 0 (defense-in-depth).
	if got := data.ProviderIndex("missing"); got != 0 {
		t.Errorf("ProviderIndex(missing) = %d, want 0", got)
	}
	if got := data.ProviderIndex("b"); got != 1 {
		t.Errorf("ProviderIndex(b) = %d, want 1", got)
	}
}

func TestCatalogData_GroupIndexFallback(t *testing.T) {
	t.Parallel()
	data := CatalogData{
		Providers: []CatalogProviderTab{
			{ID: "a", Groups: []CatalogGroupTab{{ID: "g1"}, {ID: "g2"}}},
		},
	}
	if got := data.GroupIndex("a", "g2"); got != 1 {
		t.Errorf("GroupIndex(a,g2) = %d, want 1", got)
	}
	if got := data.GroupIndex("a", "missing"); got != 0 {
		t.Errorf("GroupIndex(a,missing) = %d, want 0", got)
	}
	if got := data.GroupIndex("missing", "g1"); got != 0 {
		t.Errorf("GroupIndex(missing,g1) = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestBuildCatalogData|TestCatalogData_" -v`

Expected: FAIL.

- [ ] **Step 3: Add `CatalogData` types**

In [internal/chassis/data.go](../../../internal/chassis/data.go), append before `idleSnapshot`:

```go
// CatalogData drives the catalog drawer. Open is always false at
// server-render time; client-side JS flips body.browse-open on
// BROWSE click. TunedProviderID/TunedChannelID derive from
// transport.AdapterRef so the drawer's .tuned state matches the
// preset bank's .lit and the source cluster's .casting.
type CatalogData struct {
	Open             bool
	Providers        []CatalogProviderTab
	ActiveProviderID string
	ActiveGroupID    string
	TunedProviderID  string
	TunedChannelID   string
	PresetMembership map[string]int // "provider:channel" → slot (1..12)
	TotalChannels    int            // sum across all providers
}

// CatalogProviderTab is one provider tab in the drawer's top strip.
type CatalogProviderTab struct {
	ID, DisplayName, BadgeLabel, BadgeClass string
	Live    bool
	ChCount int
	Groups  []CatalogGroupTab
}

// CatalogGroupTab is one button in the rail.
type CatalogGroupTab struct {
	ID, Name string
	ChCount  int
	Channels []CatalogChannelCard
}

// CatalogChannelCard is one .ch-card in the grid.
type CatalogChannelCard struct {
	ID, Name, PlayMode string
	Live       bool
	Tuned      bool // matches transport.AdapterRef
	Starred    bool // is in a preset slot
	PresetSlot int  // 0 if !Starred; 1..12 otherwise
}

// ProviderIndex returns the slice index of a provider by ID for use
// in catalog-rail.html / catalog-grid.html templates. Returns 0 if
// the ID is not found — defense-in-depth so template execution
// cannot panic on a transient catalog reload race.
func (d CatalogData) ProviderIndex(id string) int {
	for i, p := range d.Providers {
		if p.ID == id {
			return i
		}
	}
	return 0
}

// GroupIndex returns the slice index of a group within a provider.
// Returns 0 if either the provider or the group is not found.
func (d CatalogData) GroupIndex(providerID, groupID string) int {
	pi := d.ProviderIndex(providerID)
	if pi >= len(d.Providers) {
		return 0
	}
	for i, g := range d.Providers[pi].Groups {
		if g.ID == groupID {
			return i
		}
	}
	return 0
}

// buildCatalogData shape-shifts the adapter catalog into the chassis
// CatalogData. Resolves Starred + PresetSlot from the presets slice;
// resolves Tuned from the supplied adapterRef. Pure function — no
// allocations the chassis snapshot tick can't afford.
func buildCatalogData(cat []adapters.CatalogProvider, presets [12]adapters.PresetEntry, adapterRef string) CatalogData {
	membership := map[string]int{}
	for _, p := range presets {
		if p.ProviderID == "" {
			continue
		}
		membership[p.ProviderID+":"+p.ChannelID] = p.Slot
	}
	var tunedProvider, tunedChannel string
	if parseAdapterRefSource(adapterRef) == "streams" {
		tunedProvider, tunedChannel = parseStreamsAdapterRef(adapterRef)
	}

	data := CatalogData{
		TunedProviderID:  tunedProvider,
		TunedChannelID:   tunedChannel,
		PresetMembership: membership,
	}
	for _, p := range cat {
		tab := CatalogProviderTab{
			ID: p.ID, DisplayName: p.DisplayName,
			BadgeLabel: p.BadgeLabel, BadgeClass: p.BadgeClass,
			Live: p.Live,
		}
		for _, g := range p.Groups {
			gt := CatalogGroupTab{ID: g.ID, Name: g.Name}
			for _, c := range g.Channels {
				key := p.ID + ":" + c.ID
				slot, starred := membership[key]
				card := CatalogChannelCard{
					ID: c.ID, Name: c.Name, PlayMode: c.PlayMode,
					Live:       c.Live,
					Tuned:      p.ID == tunedProvider && c.ID == tunedChannel && tunedProvider != "",
					Starred:    starred,
					PresetSlot: slot,
				}
				gt.Channels = append(gt.Channels, card)
			}
			gt.ChCount = len(gt.Channels)
			tab.ChCount += gt.ChCount
			tab.Groups = append(tab.Groups, gt)
		}
		data.TotalChannels += tab.ChCount
		data.Providers = append(data.Providers, tab)
	}
	if len(data.Providers) > 0 {
		data.ActiveProviderID = data.Providers[0].ID
		if len(data.Providers[0].Groups) > 0 {
			data.ActiveGroupID = data.Providers[0].Groups[0].ID
		}
	}
	return data
}
```

- [ ] **Step 4: Add `Catalog` field to `ReceiverPageData` and `CatalogTotalChannels` to `PresetsData`**

In the same file, extend the top-level `ReceiverPageData` struct (around line 17-30) to add `Catalog`, AND extend `PresetsData` (around line 230-234) to add `CatalogTotalChannels`. The latter is a bridge field that lets the `preset-bank` template render the BROWSE button label (which receives `.Presets` as its `.`, not the page root) without needing a template wrapper.

```go
type ReceiverPageData struct {
	Version    string
	BrandName  string
	State      ReceiverState
	VFD        VFDData
	Source     SourceData
	Meter      MeterData
	Transport  TransportData
	Visualizer VisualizerData
	Input      InputData
	Presets    PresetsData
	Catalog    CatalogData
	History    HistoryData
	Settings   SettingsData
}

// PresetsData drives the 12-slot preset bank. CatalogTotalChannels is
// a copy of CatalogData.TotalChannels populated by the snapshot helpers
// — it lives on PresetsData so the preset-bank template (whose `.` is
// PresetsData) can render the "Browse full catalog (N)" label without
// a template wrapper.
type PresetsData struct {
	ModeLabel             string
	Count                 string
	CatalogTotalChannels  int
	Slots                 [12]PresetSlot
}
```

Update `idleSnapshot` to populate `Catalog` from `cfg.StreamsCatalogViewer` + `cfg.PresetViewer` (both may be nil — pass through empty values), AND bridge the catalog count to `PresetsData.CatalogTotalChannels`. The existing `idleSnapshot` returns a struct literal directly; restructure to use a named variable so post-literal assignment is possible.

Find the existing `idleSnapshot`:

```go
func idleSnapshot(cfg Config, now time.Time) ReceiverPageData {
	return ReceiverPageData{
		// ... existing fields ...
		Presets: buildPresetsData(cfg.PresetViewer, "", ""),
		// ...
	}
}
```

Replace with:

```go
func idleSnapshot(cfg Config, now time.Time) ReceiverPageData {
	base := ReceiverPageData{
		// ... existing fields unchanged ...
		Presets: buildPresetsData(cfg.PresetViewer, "", ""),
		Catalog: idleCatalogData(cfg),
		// ...
	}
	// Bridge catalog count to PresetsData so the preset-bank template
	// can render "Browse full catalog (N)" without a template wrapper.
	base.Presets.CatalogTotalChannels = base.Catalog.TotalChannels
	return base
}
```

And add the `idleCatalogData` helper at the bottom of the file:

```go
// idleCatalogData builds the page-load Catalog snapshot from the cfg
// adapters. Returns a zero-value CatalogData when either viewer is nil
// (test ergonomics) so idle renders cleanly without dependencies.
func idleCatalogData(cfg Config) CatalogData {
	if cfg.StreamsCatalogViewer == nil || cfg.PresetViewer == nil {
		return CatalogData{}
	}
	return buildCatalogData(
		cfg.StreamsCatalogViewer.Catalog(),
		cfg.PresetViewer.Presets(),
		"",
	)
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/chassis -run "TestBuildCatalogData|TestCatalogData_|TestApplySourceLampState|TestParseAdapterRefSource" -v && go vet ./internal/chassis/...`

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/data.go internal/chassis/data_test.go
git commit -m "feat(chassis): add CatalogData types and buildCatalogData helper"
```

---

## Task 14: Snapshot Wiring — `idleSnapshot` + `snapshotFromStatusView`

**Files:**
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/data.go` (idleSnapshot — covered in Task 13)

Wire `applySourceLampState` into both snapshot paths and rebuild `Catalog` from the live `AdapterRef`.

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/data_test.go](../../../internal/chassis/data_test.go):

```go
func TestSnapshotFromStatusView_PopulatesCatalogAndLamps(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	// fake viewers/casters
	cfg.StreamsCatalogViewer = fakeCatalogViewer{
		providers: []adapters.CatalogProvider{
			{ID: "mtv-rewind", DisplayName: "MTV Rewind",
				BadgeLabel: "MTV", BadgeClass: "mtv",
				Groups: []adapters.CatalogGroup{
					{ID: "shows", Name: "Shows", Channels: []adapters.CatalogChannel{
						{ID: "1stday", Name: "First Day", PlayMode: "SEQ"},
					}},
				}},
		},
	}
	cfg.PresetViewer = fakePresetViewer{entries: [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday"},
	}}
	cfg.SourceAvailabilityViewers = []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
	}
	view := core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "streams:mtv-rewind:1stday:abc:def",
		Generation: 5,
	}
	snap := snapshotFromStatusView(cfg, view, nil, nil, nil, nil, time.Now())
	// Source-cluster STREAMS lamp shows Casting=true.
	var streams *SourceButton
	for i := range snap.Source.Buttons {
		if snap.Source.Buttons[i].Label == "STREAMS" {
			streams = &snap.Source.Buttons[i]
		}
	}
	if streams == nil {
		t.Fatal("STREAMS button missing")
	}
	if !streams.Configured || !streams.Casting {
		t.Errorf("STREAMS button = %+v, want Configured=true Casting=true", streams)
	}
	// Catalog populated and the channel is Tuned.
	if len(snap.Catalog.Providers) != 1 {
		t.Fatalf("Catalog.Providers len = %d, want 1", len(snap.Catalog.Providers))
	}
	channel := snap.Catalog.Providers[0].Groups[0].Channels[0]
	if !channel.Tuned {
		t.Errorf("catalog channel Tuned = false, want true")
	}
	if !channel.Starred || channel.PresetSlot != 1 {
		t.Errorf("catalog channel star = (%v, %d), want (true, 1)", channel.Starred, channel.PresetSlot)
	}
}

type fakeCatalogViewer struct {
	providers []adapters.CatalogProvider
}

func (f fakeCatalogViewer) Catalog() []adapters.CatalogProvider { return f.providers }
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestSnapshotFromStatusView_PopulatesCatalogAndLamps -v`

Expected: FAIL — snapshot path does not yet wire lamp state or catalog.

- [ ] **Step 3: Wire `snapshotFromStatusView`**

In [internal/chassis/session.go](../../../internal/chassis/session.go), modify `snapshotFromStatusView` (around line 74). Replace the existing `base.Presets = buildPresetsData(cfg.PresetViewer, providerID, channelID)` block at the end of the function with:

```go
	providerID, channelID := parseStreamsAdapterRef(view.AdapterRef)
	base.Presets = buildPresetsData(cfg.PresetViewer, providerID, channelID)
	// applySourceLampState runs AFTER applyAUXSourceState so the AUX
	// slot's Active/Lit handling is not clobbered. The helper guards
	// AUX (Action != "") internally.
	applySourceLampState(&base, cfg.SourceAvailabilityViewers, view.AdapterRef)
	if cfg.StreamsCatalogViewer != nil && cfg.PresetViewer != nil {
		base.Catalog = buildCatalogData(
			cfg.StreamsCatalogViewer.Catalog(),
			cfg.PresetViewer.Presets(),
			view.AdapterRef,
		)
		// Bridge the catalog count down to PresetsData so the preset-bank
		// template (whose `.` is PresetsData) can render the BROWSE label.
		base.Presets.CatalogTotalChannels = base.Catalog.TotalChannels
	}
	return base
}
```

- [ ] **Step 4: Wire idle path**

In `snapshotFromSession` (the same file, around line 54), the idle branch already calls `idleSnapshot(cfg, now)` which after Task 13 populates `Catalog` via `idleCatalogData(cfg)`. Add the lamp-state call to the idle branch too:

Find:

```go
	if sv == nil {
		base := idleSnapshot(cfg, now)
		applyAUXSourceState(&base, aux)
		base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
		if volv != nil {
			base.Transport.OutputVolume = volv.OutputVolume()
		}
		return base
	}
```

Replace with:

```go
	if sv == nil {
		base := idleSnapshot(cfg, now)
		applyAUXSourceState(&base, aux)
		applySourceLampState(&base, cfg.SourceAvailabilityViewers, "")
		base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
		if volv != nil {
			base.Transport.OutputVolume = volv.OutputVolume()
		}
		return base
	}
```

Apply the same change to the `s.session == nil` branch inside [internal/chassis/server.go](../../../internal/chassis/server.go) `buildSnapshot` (around line 156). Insert `applySourceLampState(&base, s.cfg.SourceAvailabilityViewers, "")` after `applyAUXSourceState(&base, s.aux)`.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/chassis -count=1`

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/session.go internal/chassis/server.go internal/chassis/data_test.go
git commit -m "feat(chassis): wire applySourceLampState + Catalog into snapshot paths"
```

---

## Task 15: Cast Helpers — `writePresetStarSuccess`, `writePresetMoveSuccess`, `writePresetEditError`

**Files:**
- Modify: `internal/chassis/cast.go`
- Modify: `internal/chassis/cast_test.go`

Three sibling helpers share an internal `presetEditBody` struct with `omitempty` JSON tags. Splitting (vs one 7-positional signature) prevents passing `slot` and `cleared` on error responses or on the inverse path.

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/cast_test.go](../../../internal/chassis/cast_test.go):

```go
func TestWritePresetStarSuccess_StarredEmitsSlotNoCleared(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetStarSuccess(rec, true, 5, nil)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":true,"slot":5}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetStarSuccess_UnstarredEmitsClearedNoSlot(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetStarSuccess(rec, false, 0, []int{3, 9})
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":false,"cleared":[3,9]}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetStarSuccess_UnstarredEmptyClearedOmitsCleared(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetStarSuccess(rec, false, 0, nil)
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":false}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetMoveSuccess_MinimalShape(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetMoveSuccess(rec)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetEditError_NoSlotOrCleared(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetEditError(rec, 409, "BANK FULL")
	if rec.Code != 409 {
		t.Fatalf("Code = %d, want 409", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":false,"chip":"BANK FULL"}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestWritePresetStarSuccess|TestWritePresetMoveSuccess|TestWritePresetEditError" -v`

Expected: FAIL — helpers undefined.

- [ ] **Step 3: Add the helpers**

Append to [internal/chassis/cast.go](../../../internal/chassis/cast.go) (after `writeCastJSON`):

```go
// presetEditBody is the JSON envelope shared by the three preset edit
// helpers. omitempty rules:
//   - Success: Ok=true, Chip stays empty (omitted).
//   - Error: Ok=false, Chip set, Starred/Slot/Cleared stay zero
//     (omitted by *bool / omitempty rules below).
//   - Star success: Ok=true, Starred=*true, Slot in 1..12 (omitted when 0).
//   - Move success: Ok=true; all other fields zero/nil and omitted.
//
// Starred is a *bool so the unstarred-success path emits "starred":false
// (a value bool would be omitempty-dropped when false).
type presetEditBody struct {
	Ok      bool   `json:"ok"`
	Chip    string `json:"chip,omitempty"`
	Starred *bool  `json:"starred,omitempty"`
	Slot    int    `json:"slot,omitempty"`
	Cleared []int  `json:"cleared,omitempty"`
}

// writePresetStarSuccess emits {"ok":true,"starred":<starred>,...} with
// Slot populated on the starred path and Cleared populated on the
// unstarred path. Callers pass zero values for the inapplicable fields.
func writePresetStarSuccess(w http.ResponseWriter, starred bool, slot int, cleared []int) {
	body := presetEditBody{Ok: true, Starred: &starred}
	if starred {
		body.Slot = slot
		// Cleared MUST be nil on the starred path; the field's omitempty
		// rule will drop it. Caller passing a non-nil cleared is a bug
		// caught by the unit tests.
	} else {
		body.Cleared = cleared
		// Slot must be zero on the unstarred path; omitempty drops it.
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// writePresetMoveSuccess emits the minimal {"ok":true} success envelope
// for a successful POST /receiver/preset/move (including the from==to
// no-op).
func writePresetMoveSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(presetEditBody{Ok: true})
}

// writePresetEditError emits {"ok":false,"chip":<chip>} with no slot
// or cleared fields. Status drives the HTTP code.
func writePresetEditError(w http.ResponseWriter, status int, chip string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(presetEditBody{Ok: false, Chip: chip})
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/chassis -run "TestWritePresetStarSuccess|TestWritePresetMoveSuccess|TestWritePresetEditError" -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/cast.go internal/chassis/cast_test.go
git commit -m "feat(chassis): add preset edit helpers (star success, move success, edit error)"
```

---

## Task 16: `POST /receiver/streams/cast` Handler

**Files:**
- Create: `internal/chassis/streams_cast.go`
- Create: `internal/chassis/streams_cast_test.go`
- Modify: `internal/chassis/server.go` (register route)

- [ ] **Step 1: Write failing tests**

Create [internal/chassis/streams_cast_test.go](../../../internal/chassis/streams_cast_test.go):

```go
package chassis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakeStreamsCaster struct {
	mu      sync.Mutex
	calls   [][2]string
	respond func(provider, channel string) error
}

func (f *fakeStreamsCaster) CastChannel(ctx context.Context, providerID, channelID string) error {
	f.mu.Lock()
	f.calls = append(f.calls, [2]string{providerID, channelID})
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(providerID, channelID)
	}
	return nil
}

func newServerWithStreamsCasterForTest(t *testing.T, caster adapters.StreamsCaster) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.StreamsCaster = caster
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func postStreamsCast(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/streams/cast", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleStreamsCast(rec, req)
	return rec
}

func TestStreamsCast_Success(t *testing.T) {
	t.Parallel()
	caster := &fakeStreamsCaster{}
	srv := newServerWithStreamsCasterForTest(t, caster)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(caster.calls) != 1 || caster.calls[0] != [2]string{"mtv-rewind", "80s"} {
		t.Errorf("calls = %v, want [[mtv-rewind 80s]]", caster.calls)
	}
}

func TestStreamsCast_MissingProviderOrChannelReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithStreamsCasterForTest(t, &fakeStreamsCaster{})
	for _, body := range []string{
		"",
		"provider=",
		"channel=80s",
		"provider=mtv-rewind",
		"provider=&channel=80s",
		"provider=mtv-rewind&channel=",
	} {
		t.Run(body, func(t *testing.T) {
			rec := postStreamsCast(t, srv, body)
			if rec.Code != 400 {
				t.Errorf("body=%q Code = %d, want 400", body, rec.Code)
			}
			var got map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got["chip"] != "BAD INPUT" {
				t.Errorf("body=%q chip = %v, want BAD INPUT", body, got["chip"])
			}
		})
	}
}

func TestStreamsCast_NilCasterReturns404(t *testing.T) {
	t.Parallel()
	srv := newServerWithStreamsCasterForTest(t, nil)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404", rec.Code)
	}
}

func TestStreamsCast_QuickCastErrorPropagates(t *testing.T) {
	t.Parallel()
	caster := &fakeStreamsCaster{respond: func(p, c string) error {
		return &adapters.QuickCastError{Status: 503, Chip: "NOT READY"}
	}}
	srv := newServerWithStreamsCasterForTest(t, caster)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 503 {
		t.Errorf("Code = %d, want 503", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["chip"] != "NOT READY" {
		t.Errorf("chip = %v, want NOT READY", got["chip"])
	}
}

func TestStreamsCast_UntypedErrorCollapsesTo500(t *testing.T) {
	t.Parallel()
	caster := &fakeStreamsCaster{respond: func(p, c string) error {
		return errors.New("synthetic")
	}}
	srv := newServerWithStreamsCasterForTest(t, caster)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 500 {
		t.Errorf("Code = %d, want 500", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["chip"] != "CAST FAILED" {
		t.Errorf("chip = %v, want CAST FAILED", got["chip"])
	}
}

func TestStreamsCast_RouteRequiresSameOrigin(t *testing.T) {
	t.Parallel()
	srv := newServerWithStreamsCasterForTest(t, &fakeStreamsCaster{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/streams/cast",
		strings.NewReader("provider=mtv-rewind&channel=80s"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No Sec-Fetch-Site header.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestStreamsCast -v`

Expected: FAIL — `handleStreamsCast` undefined, route unregistered.

- [ ] **Step 3: Create the handler**

Create [internal/chassis/streams_cast.go](../../../internal/chassis/streams_cast.go):

```go
package chassis

import (
	"errors"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (s *Server) handleStreamsCast(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}
	provider := strings.TrimSpace(r.PostFormValue("provider"))
	channel := strings.TrimSpace(r.PostFormValue("channel"))
	if provider == "" || channel == "" {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}
	if s.streamsCaster == nil {
		writeCastJSON(w, http.StatusNotFound, false, "NOT FOUND")
		return
	}
	if err := s.streamsCaster.CastChannel(r.Context(), provider, channel); err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writeCastJSON(w, qerr.Status, false, qerr.Chip)
			return
		}
		writeCastJSON(w, http.StatusInternalServerError, false, "CAST FAILED")
		return
	}
	writeCastJSON(w, http.StatusOK, true, "")
}
```

- [ ] **Step 4: Register the route**

In [internal/chassis/server.go](../../../internal/chassis/server.go) `Mount` (around line 192), append after the `POST /receiver/preset/{slot}/cast` registration:

```go
	mux.Handle("POST /receiver/streams/cast", requireSameOrigin(http.HandlerFunc(s.handleStreamsCast)))
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/chassis -run TestStreamsCast -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/streams_cast.go internal/chassis/streams_cast_test.go internal/chassis/server.go
git commit -m "feat(chassis): add POST /receiver/streams/cast handler"
```

---

## Task 17: `POST /receiver/preset/star` and `POST /receiver/preset/move` Handlers

**Files:**
- Modify: `internal/chassis/preset.go`
- Modify: `internal/chassis/preset_test.go`
- Modify: `internal/chassis/server.go` (register two routes; add `refreshSnapshotNow` helper)

The star handler enforces strict lexical `"true"|"false"` (NOT `strconv.ParseBool`). Both handlers call `refreshSnapshotNow()` after successful adapter return so the cached snapshot updates immediately. 404 precedes the `from==to` no-op short-circuit in `move` (defense-in-depth against misleading 200s from a chassis with nil PresetEditor).

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/preset_test.go](../../../internal/chassis/preset_test.go):

```go
type fakePresetEditor struct {
	mu        sync.Mutex
	starCalls []struct {
		Provider, Channel string
		Starred           bool
	}
	moveCalls   [][2]int
	starRespond func(p, c string, starred bool) (adapters.PresetStarResult, error)
	moveRespond func(from, to int) error
}

func (f *fakePresetEditor) SetPresetStarred(ctx context.Context, p, c string, starred bool) (adapters.PresetStarResult, error) {
	f.mu.Lock()
	f.starCalls = append(f.starCalls, struct {
		Provider, Channel string
		Starred           bool
	}{p, c, starred})
	f.mu.Unlock()
	if f.starRespond != nil {
		return f.starRespond(p, c, starred)
	}
	if starred {
		return adapters.PresetStarResult{Starred: true, Slot: 1}, nil
	}
	return adapters.PresetStarResult{Starred: false, Cleared: []int{1}}, nil
}

func (f *fakePresetEditor) MovePreset(ctx context.Context, from, to int) error {
	f.mu.Lock()
	f.moveCalls = append(f.moveCalls, [2]int{from, to})
	f.mu.Unlock()
	if f.moveRespond != nil {
		return f.moveRespond(from, to)
	}
	return nil
}

func newServerWithPresetEditorForTest(t *testing.T, editor adapters.PresetEditor) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.PresetEditor = editor
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func postStar(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/star", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePresetStar(rec, req)
	return rec
}

func postMove(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/move", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePresetMove(rec, req)
	return rec
}

func TestPresetStar_AddSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=true")
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":true,"slot":1}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPresetStar_RemoveSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=false")
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":false,"cleared":[1]}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPresetStar_StrictLexicalRejectsBoolean1(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	for _, val := range []string{"1", "0", "t", "f", "TRUE", "False", "yes", ""} {
		t.Run("starred="+val, func(t *testing.T) {
			rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred="+val)
			if rec.Code != 400 {
				t.Errorf("Code = %d, want 400 for starred=%q", rec.Code, val)
			}
			var got map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got["chip"] != "BAD INPUT" {
				t.Errorf("chip = %v, want BAD INPUT", got["chip"])
			}
		})
	}
}

func TestPresetStar_MissingProviderOrChannelReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	for _, body := range []string{
		"provider=&channel=80s&starred=true",
		"provider=mtv-rewind&channel=&starred=true",
		"channel=80s&starred=true",
		"provider=mtv-rewind&starred=true",
	} {
		t.Run(body, func(t *testing.T) {
			rec := postStar(t, srv, body)
			if rec.Code != 400 {
				t.Errorf("body=%q Code = %d, want 400", body, rec.Code)
			}
		})
	}
}

func TestPresetStar_NilEditorReturns404(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, nil)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=true")
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404", rec.Code)
	}
}

func TestPresetStar_BankFullPropagates(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{starRespond: func(p, c string, starred bool) (adapters.PresetStarResult, error) {
		return adapters.PresetStarResult{}, &adapters.QuickCastError{Status: 409, Chip: "BANK FULL"}
	}}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=true")
	if rec.Code != 409 {
		t.Fatalf("Code = %d, want 409", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":false,"chip":"BANK FULL"}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPresetMove_SwapSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postMove(t, srv, "from=7&to=3")
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(editor.moveCalls) != 1 || editor.moveCalls[0] != [2]int{7, 3} {
		t.Errorf("moveCalls = %v, want [[7 3]]", editor.moveCalls)
	}
}

func TestPresetMove_FromEqualsToNoOpButStillSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postMove(t, srv, "from=5&to=5")
	if rec.Code != 200 {
		t.Errorf("Code = %d, want 200", rec.Code)
	}
}

func TestPresetMove_OutOfRangeReturnsBadSlot(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	for _, body := range []string{
		"from=0&to=1", "from=1&to=0", "from=13&to=1", "from=1&to=13",
		"from=abc&to=1", "from=1&to=abc", "from=&to=1", "from=1&to=",
	} {
		t.Run(body, func(t *testing.T) {
			rec := postMove(t, srv, body)
			if rec.Code != 400 {
				t.Errorf("body=%q Code = %d, want 400", body, rec.Code)
			}
			var got map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got["chip"] != "BAD SLOT" {
				t.Errorf("chip = %v, want BAD SLOT", got["chip"])
			}
		})
	}
}

func TestPresetMove_NilEditorReturns404EvenForFromEqualsTo(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, nil)
	rec := postMove(t, srv, "from=1&to=1")
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404 (404 precedes from==to no-op)", rec.Code)
	}
}

func TestPresetStarRouteRequiresSameOrigin(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/star",
		strings.NewReader("provider=mtv-rewind&channel=80s&starred=true"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}

func TestPresetMoveRouteRequiresSameOrigin(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/move",
		strings.NewReader("from=1&to=2"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestPresetStar|TestPresetMove" -v`

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Add `refreshSnapshotNow` helper to `server.go`**

In [internal/chassis/server.go](../../../internal/chassis/server.go), after the `Close` method (around line 248), add:

```go
// refreshSnapshotNow rebuilds the cached snapshot synchronously.
// Successful preset mutations call this so connected SSE clients
// observe the change within one diff-tick rather than waiting for the
// next 250ms refresh. Safe to call from any goroutine; the cache uses
// its own mutex.
func (s *Server) refreshSnapshotNow() {
	s.cache.Set(s.buildSnapshot(time.Now()))
}
```

- [ ] **Step 4: Add the handlers in `preset.go`**

Append to [internal/chassis/preset.go](../../../internal/chassis/preset.go):

```go
const (
	presetStarFormStarred  = "starred"
	presetStarFormProvider = "provider"
	presetStarFormChannel  = "channel"
	presetMoveFormFrom     = "from"
	presetMoveFormTo       = "to"
)

func (s *Server) handlePresetStar(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writePresetEditError(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	provider := strings.TrimSpace(r.PostFormValue(presetStarFormProvider))
	channel := strings.TrimSpace(r.PostFormValue(presetStarFormChannel))
	starredRaw := r.PostFormValue(presetStarFormStarred)
	if provider == "" || channel == "" {
		writePresetEditError(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	// Strict lexical form: ONLY the literal strings "true" and "false"
	// are accepted. strconv.ParseBool would also accept "1", "t", "TRUE",
	// etc. — rejected here so the wire is deterministic and sloppy
	// clients get a fast 400 instead of mysterious downstream behavior.
	var starred bool
	switch starredRaw {
	case "true":
		starred = true
	case "false":
		starred = false
	default:
		writePresetEditError(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if s.presetEditor == nil {
		writePresetEditError(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	res, err := s.presetEditor.SetPresetStarred(r.Context(), provider, channel, starred)
	if err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writePresetEditError(w, qerr.Status, qerr.Chip)
			return
		}
		writePresetEditError(w, http.StatusInternalServerError, "CAST FAILED")
		return
	}
	s.refreshSnapshotNow()
	writePresetStarSuccess(w, res.Starred, res.Slot, res.Cleared)
}

func (s *Server) handlePresetMove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writePresetEditError(w, http.StatusBadRequest, "BAD SLOT")
		return
	}
	fromStr := r.PostFormValue(presetMoveFormFrom)
	toStr := r.PostFormValue(presetMoveFormTo)
	from, errFrom := strconv.Atoi(fromStr)
	to, errTo := strconv.Atoi(toStr)
	if errFrom != nil || errTo != nil || from < 1 || from > 12 || to < 1 || to > 12 {
		writePresetEditError(w, http.StatusBadRequest, "BAD SLOT")
		return
	}
	if s.presetEditor == nil {
		// 404 precedes the from==to no-op short-circuit so a connectivity
		// test never gets a misleading 200 from a chassis that has no
		// editor wired.
		writePresetEditError(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	if from == to {
		// No-op success; chassis still refreshes snapshot for uniformity
		// (the events-loop diff suppresses the spurious emit).
		s.refreshSnapshotNow()
		writePresetMoveSuccess(w)
		return
	}
	if err := s.presetEditor.MovePreset(r.Context(), from, to); err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writePresetEditError(w, qerr.Status, qerr.Chip)
			return
		}
		writePresetEditError(w, http.StatusInternalServerError, "CAST FAILED")
		return
	}
	s.refreshSnapshotNow()
	writePresetMoveSuccess(w)
}
```

Add the needed import for `strings`:

```go
import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)
```

- [ ] **Step 5: Register the routes**

In [internal/chassis/server.go](../../../internal/chassis/server.go) `Mount`, append after the streams cast route from Task 16:

```go
	mux.Handle("POST /receiver/preset/star", requireSameOrigin(http.HandlerFunc(s.handlePresetStar)))
	mux.Handle("POST /receiver/preset/move", requireSameOrigin(http.HandlerFunc(s.handlePresetMove)))
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/chassis -run "TestPresetStar|TestPresetMove|TestPresetStarRoute|TestPresetMoveRoute" -count=1 -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/preset.go internal/chassis/preset_test.go internal/chassis/server.go
git commit -m "feat(chassis): add POST /receiver/preset/star and /receiver/preset/move handlers"
```

---

## Task 18: `presets` SSE Event

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

Add the `presets` envelope and integrate it into the events loop. Position is between `meter` and `audio` in the initial burst. Diff compares only persistent triples, not derived display fields.

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/events_test.go](../../../internal/chassis/events_test.go):

```go
func TestPresetsChanged_PersistentTriplesOnly(t *testing.T) {
	t.Parallel()
	a := [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "80s", Title: "MTV 80s"},
	}
	b := a
	b[0].Title = "MTV 80s — Renamed" // display-only change
	if presetsChanged(a, b) {
		t.Errorf("presetsChanged ignored title rename, but reported a change")
	}
	c := a
	c[0].ChannelID = "90s" // persistent triple changed
	if !presetsChanged(a, c) {
		t.Errorf("presetsChanged missed a real channel-id mutation")
	}
}

func TestPresetsChanged_EmptyVsFilled(t *testing.T) {
	t.Parallel()
	empty := [12]adapters.PresetEntry{}
	filled := empty
	filled[2].ProviderID = "mtv-rewind"
	filled[2].ChannelID = "amp"
	if !presetsChanged(empty, filled) {
		t.Errorf("presetsChanged missed an add into an empty slot")
	}
}

func TestPresetEnvelopeFromSnapshot_Shape(t *testing.T) {
	t.Parallel()
	snap := [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
		// remaining slots empty (Slot will be 0 — that's OK; the envelope re-derives slot=i+1)
	}
	env := presetEnvelopeFromSnapshot(snap)
	if len(env.Slots) != 12 {
		t.Fatalf("len(Slots) = %d, want 12", len(env.Slots))
	}
	if env.Slots[0].Slot != 1 || env.Slots[0].Provider != "mtv-rewind" {
		t.Errorf("Slots[0] = %+v, want Slot=1 Provider=mtv-rewind", env.Slots[0])
	}
	if env.Slots[1].Provider != "" {
		t.Errorf("empty Slots[1].Provider = %q, want empty", env.Slots[1].Provider)
	}
	if env.Slots[1].Slot != 2 {
		t.Errorf("empty Slots[1].Slot = %d, want 2", env.Slots[1].Slot)
	}
}

func TestHandleEvents_InitialBurstIncludesPresetsBetweenMeterAndAudio(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.PresetViewer = bundledFakeViewer()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/receiver/events", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer resp.Body.Close()

	// Read the initial burst by collecting event names until we see "audio",
	// then assert the position invariant.
	rd := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var names []string
	for time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "event: ") {
			names = append(names, strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
			if names[len(names)-1] == "audio" {
				break
			}
		}
	}
	// Expected: [state, vfd, source, visualizer, transport, volume, meter, presets, audio]
	want := []string{"state", "vfd", "source", "visualizer", "transport", "volume", "meter", "presets", "audio"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("initial burst events = %v, want %v", names, want)
	}
}
```

Add the needed imports to `events_test.go` if missing: `bufio`, `net/http`, `net/http/httptest`, `reflect`, `time`.

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestPresetsChanged|TestPresetEnvelopeFromSnapshot|TestHandleEvents_InitialBurstIncludesPresetsBetweenMeterAndAudio" -v`

Expected: FAIL.

- [ ] **Step 3: Add envelope + helpers**

Append to [internal/chassis/events.go](../../../internal/chassis/events.go) (after `volumeChanged`):

```go
// presetsEnvelope is the wire payload for the `presets` SSE event. Each
// frame carries the full 12-slot array — the client doesn't maintain
// prior state.
type presetsEnvelope struct {
	Slots []presetSlotEnvelope `json:"slots"`
}

type presetSlotEnvelope struct {
	Slot       int    `json:"slot"`
	Provider   string `json:"provider"`
	Channel    string `json:"channel"`
	Title      string `json:"title"`
	BadgeLabel string `json:"badgeLabel"`
	BadgeClass string `json:"badgeClass"`
	Live       bool   `json:"live"`
}

// presetEnvelopeFromSnapshot flattens a [12]PresetEntry into the wire
// envelope, ensuring each slot has Slot in 1..12 even for empty entries
// (display fields blank for empty slots).
func presetEnvelopeFromSnapshot(snap [12]adapters.PresetEntry) presetsEnvelope {
	out := presetsEnvelope{Slots: make([]presetSlotEnvelope, 12)}
	for i, p := range snap {
		out.Slots[i] = presetSlotEnvelope{
			Slot:       i + 1, // always 1-indexed, regardless of p.Slot
			Provider:   p.ProviderID,
			Channel:    p.ChannelID,
			Title:      p.Title,
			BadgeLabel: p.BadgeLabel,
			BadgeClass: p.BadgeClass,
			Live:       p.Live,
		}
	}
	return out
}

// presetsChanged compares only the persistent (slot index, provider,
// channel) triples — display fields are intentionally excluded. A
// catalog reload that changes a slot's Title is NOT a presets event.
func presetsChanged(prev, next [12]adapters.PresetEntry) bool {
	for i := range prev {
		if prev[i].ProviderID != next[i].ProviderID || prev[i].ChannelID != next[i].ChannelID {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Integrate into `handleEvents` initial burst**

In the same file, find the initial-burst emission block (lines 246-289). Insert a `presets` emit between `meter` and `audio`:

```go
	if err := emit(w, "meter", meterEnvelopeFrom(last.Meter)); err != nil {
		return
	}

	// presets envelope: position between meter and audio so the high-
	// rate audio stream stays last in the canonical order.
	lastPresets := s.cache.Get().Presets // snapshot — already consistent with `last`
	if err := emitPresets(w, last); err != nil {
		return
	}
	_ = lastPresets // also tracked separately below for diff arming

	// Audio scope initial burst — emit last in the canonical order.
```

Where `emitPresets` is a small new helper added next to `emit`. The cleanest insertion is to:

1. Read `last := s.cache.Get()` (already done at line 246).
2. Capture `lastPresetsEnvelope := presetEnvelopeFromBuildSnapshot(last)` where the helper extracts the [12]PresetEntry from a `ReceiverPageData`. The cleanest path: extend `ReceiverPageData` consumers to read `last.Presets.Slots` and pull a [12]PresetEntry out — but `PresetsData` does NOT carry the raw `[12]PresetEntry`; the snapshot only has the rendered `[12]PresetSlot` (a different shape).

Solution: have the events loop pull the raw `[12]PresetEntry` directly from the `PresetViewer`. Add it to `*Server` by exposing a getter, then in the events handler:

```go
	// presets envelope (between meter and audio).
	rawPresets := s.presetSnapshot()
	if err := emit(w, "presets", presetEnvelopeFromSnapshot(rawPresets)); err != nil {
		return
	}
	lastPresets := rawPresets
```

Add to `server.go`:

```go
// presetSnapshot returns the current 12-slot preset entries from the
// configured PresetViewer, or an empty zero-value array if no viewer
// is wired. Used by handleEvents for the presets SSE event.
func (s *Server) presetSnapshot() [12]adapters.PresetEntry {
	if s.presetViewer == nil {
		var zero [12]adapters.PresetEntry
		for i := range zero {
			zero[i] = adapters.PresetEntry{Slot: i + 1}
		}
		return zero
	}
	return s.presetViewer.Presets()
}
```

- [ ] **Step 5: Add the diff arm in the tick loop**

In `handleEvents`'s `select { case <-tick.C:` block (around lines 324-376), append after the `meterChanged` branch and before the audio handling:

```go
		curr := s.cache.Get()
		// ... existing diff arms (state, vfd, source, visualizer, transport, volume, meter)
		currPresets := s.presetSnapshot()
		if presetsChanged(lastPresets, currPresets) {
			if err := emit(w, "presets", presetEnvelopeFromSnapshot(currPresets)); err != nil {
				return
			}
			lastPresets = currPresets
		}
```

`lastPresets` is the goroutine-local variable initialized from the initial-burst section above.

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/chassis -count=1 -v -run "TestPresetsChanged|TestPresetEnvelopeFromSnapshot|TestHandleEvents_InitialBurstIncludesPresetsBetweenMeterAndAudio"`

Expected: PASS.

- [ ] **Step 7: Run the full events test suite**

Run: `go test ./internal/chassis -run TestHandleEvents -count=1 -v`

Expected: PASS. If any pre-existing initial-burst ordering test asserts a specific event sequence, update it to include `presets` between `meter` and `audio`.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go internal/chassis/server.go
git commit -m "feat(chassis): emit presets SSE event between meter and audio in canonical order"
```

---

## Task 19: Source-Cluster Template — Lamp vs AUX Branch + `lower` FuncMap Helper

**Files:**
- Modify: `internal/chassis/templates.go` (add `lower` helper)
- Modify: `internal/chassis/templates/source-cluster.html`
- Modify: `internal/chassis/chassis_test.go` (rendering test)

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestSourceClusterTemplate_LampSlotsForEmptyAction(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`<div class="lamp`,
		`data-source-id="streams"`,
		`data-source-id="plex"`,
		`data-source-id="jellyfin"`,
		`data-source-id="dlna"`,
		`class="hw-btn`, // AUX still renders as hw-btn
		`data-source-action="aux-start"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("source-cluster html missing %q", want)
		}
	}
}

func TestSourceClusterTemplate_LampClassReflectsState(t *testing.T) {
	t.Parallel()
	// Build a snapshot with STREAMS=Configured+Casting, PLEX=Configured, others unavailable.
	srv := newTestServerWithLampState(t,
		map[string]bool{"streams": true, "plex": true},
		"streams:mtv-rewind:80s:abc:def",
	)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	// STREAMS lamp must carry configured-idle AND casting classes.
	if !strings.Contains(html, `class="lamp configured-idle casting"`) {
		t.Errorf("STREAMS lamp missing configured-idle+casting classes: %s", excerpt(html, "STREAMS"))
	}
	// PLEX lamp configured-idle only.
	if !strings.Contains(html, `class="lamp configured-idle"`) {
		t.Errorf("PLEX lamp missing configured-idle class: %s", excerpt(html, "PLEX"))
	}
}

// newTestServerWithLampState constructs a chassis server with the
// supplied SourceAvailabilityViewers and a synthetic SessionViewer that
// returns the given adapterRef. The fake viewers are bare structs
// implementing the interface.
func newTestServerWithLampState(t *testing.T, configured map[string]bool, adapterRef string) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	var viewers []adapters.SourceAvailabilityViewer
	for _, id := range []string{"streams", "plex", "jellyfin", "dlna"} {
		viewers = append(viewers, fakeSourceViewer{id: id, configured: boolToYesNo(configured[id])})
	}
	cfg.SourceAvailabilityViewers = viewers
	cfg.Session = fakeSessionViewer{ref: adapterRef}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func boolToYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// fakeSessionViewer returns a StatusHomeView with a fixed AdapterRef
// and StatePlaying so the chassis snapshot path runs the live branch.
type fakeSessionViewer struct{ ref string }

func (f fakeSessionViewer) StatusHomeView() core.StatusHomeView {
	return core.StatusHomeView{State: core.StatePlaying, AdapterRef: f.ref}
}

// excerpt extracts a short region of html around a keyword for error logs.
func excerpt(html, kw string) string {
	i := strings.Index(html, kw)
	if i < 0 {
		return "<keyword not found>"
	}
	start := i - 80
	if start < 0 {
		start = 0
	}
	end := i + 120
	if end > len(html) {
		end = len(html)
	}
	return html[start:end]
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestSourceClusterTemplate" -v`

Expected: FAIL — template still renders all 5 as `hw-btn`; `lower` helper undefined.

- [ ] **Step 3: Add `lower` FuncMap helper**

In [internal/chassis/templates.go](../../../internal/chassis/templates.go), extend the `templateFuncs` map (around line 39-68):

```go
	"lower": strings.ToLower,
```

The `strings` package is already imported in this file.

- [ ] **Step 4: Replace `source-cluster.html`**

Replace [internal/chassis/templates/source-cluster.html](../../../internal/chassis/templates/source-cluster.html) contents with:

```html
{{define "source-cluster"}}
{{htmlComment "chassis:source-cluster"}}
<div class="source-cluster" role="group" aria-label="Media source">
  {{- range .Buttons -}}
  {{- if eq .Action "" -}}
    <div class="lamp{{if .Configured}} configured-idle{{end}}{{if .Casting}} casting{{end}}"
         data-source-id="{{lower .Label}}"
         role="status"
         aria-label="{{.Label}}{{if .Casting}}, currently casting{{else if .Configured}}, ready{{else}}, not configured{{end}}"
         title="{{.Label}} - {{if .Casting}}currently casting{{else if .Configured}}linked, idle{{else}}not configured{{end}}">
      <span class="led" aria-hidden="true"></span>
      <span class="name">{{.Label}}</span>
    </div>
  {{- else -}}
    <button class="hw-btn{{if .Active}} active{{end}}{{if .Lit}} lit{{end}}"
            type="button"
            role="radio"
            aria-checked="{{if .Active}}true{{else}}false{{end}}"
            aria-disabled="{{if .Unavailable}}true{{else}}false{{end}}"
            data-source-action="{{.Action}}"
            data-input-id="{{.InputID}}"
            {{- if .Unavailable}} disabled{{end}}
            aria-label="{{.Label}}{{if .Active}} selected{{end}}{{if .Lit}} casting{{end}}"
            title="{{.Label}}{{if .Active}} selected{{end}}{{if .Lit}} casting{{end}}">{{.Label}}</button>
  {{- end -}}
  {{- end -}}
</div>
{{end}}
```

- [ ] **Step 5: Update existing `vfd-live.js` source-event handler**

The existing handler at [internal/chassis/static/vfd-live.js](../../../internal/chassis/static/vfd-live.js):76-99 queries `[data-source-action="..."]` to update Active/Lit on each `hw-btn`. AUX still uses that path. Lamps don't have a `data-source-action` and should be ignored by that handler — confirm by reading the code: the handler does `document.querySelector(\`[data-source-action="${button.action}"]\`)`. Source-cluster `source` SSE events still emit one entry per Button. For lamp slots, `button.action` will be empty in the envelope (since the lamp's `Action` is empty). The query selector `[data-source-action=""]` will not match any element (lamps have no such attribute), so the loop naturally skips them. No code change needed here.

If the existing test harness (one of the `vfd-live` tests) asserts that all source-cluster buttons get visited, update it to accept that lamps are not part of the `[data-source-action]` set.

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/chassis -run "TestSourceClusterTemplate" -v -count=1`

Expected: PASS.

- [ ] **Step 7: Run the full chassis test suite to catch any AUX-render regression**

Run: `go test ./internal/chassis -count=1`

Expected: all green. If existing AUX-render tests break (they assert exact button HTML), update the expected strings to match the new template — the AUX branch's HTML output is structurally the same as before but may have minor whitespace differences from the if/else block.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/templates.go internal/chassis/templates/source-cluster.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): branch source-cluster template — lamp slots vs AUX hw-btn"
```

---

## Task 20: Catalog Drawer Templates — `catalog-drawer.html`, `catalog-rail.html`, `catalog-grid.html`

**Files:**
- Create: `internal/chassis/templates/catalog-drawer.html`
- Create: `internal/chassis/templates/catalog-rail.html`
- Create: `internal/chassis/templates/catalog-grid.html`
- Modify: `internal/chassis/templates.go` (add `upper` helper)
- Modify: `internal/chassis/chassis_test.go` (render tests)

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestCatalogDrawerTemplate_RendersProviderTabs(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`<div class="catalog-drawer"`,
		`<div class="catalog-provider-tabs">`,
		`data-provider="mtv-rewind"`,
		`data-provider="cartoon-rewind"`,
		`data-provider="toonami-aftermath"`,
		`<span class="ic mtv">MTV</span>`,
		`<span class="ic cartoon">CART</span>`,
		`<span class="ic toonami">TOON</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("catalog-drawer html missing %q", want)
		}
	}
}

func TestCatalogRailTemplate_RendersGroupsForActiveProvider(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	// MTV's first group renders with --i:0; second with --i:1, etc.
	if !strings.Contains(html, `class="catalog-rail-group active" data-group="shows" style="--i:0"`) {
		t.Errorf("catalog-rail missing active MTV shows group: %s", excerpt(html, "catalog-rail"))
	}
}

func TestCatalogGridTemplate_RendersChannelCards(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`data-provider="mtv-rewind" data-channel="1stday"`,
		`<button class="star"`,
		`<div class="name">First Day on MTV</div>`,
		`<span class="mode">SEQ</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("catalog-grid html missing %q", want)
		}
	}
}

func newTestServerWithCatalog(t *testing.T) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.StreamsCatalogViewer = fakeCatalogViewer{providers: []adapters.CatalogProvider{
		{ID: "mtv-rewind", DisplayName: "MTV Rewind", BadgeLabel: "MTV", BadgeClass: "mtv", Live: false,
			Groups: []adapters.CatalogGroup{
				{ID: "shows", Name: "MTV Shows", Channels: []adapters.CatalogChannel{
					{ID: "1stday", Name: "First Day on MTV", PlayMode: "SEQ"},
				}},
			}},
		{ID: "cartoon-rewind", DisplayName: "Cartoon Rewind", BadgeLabel: "CART", BadgeClass: "cartoon", Live: false,
			Groups: []adapters.CatalogGroup{
				{ID: "g1", Name: "Group 1", Channels: []adapters.CatalogChannel{
					{ID: "c1", Name: "Cartoon 1", PlayMode: "SHUFFLE"},
				}},
			}},
		{ID: "toonami-aftermath", DisplayName: "Toonami Aftermath", BadgeLabel: "TOON", BadgeClass: "toonami", Live: true,
			Groups: []adapters.CatalogGroup{
				{ID: "g1", Name: "Group 1", Channels: []adapters.CatalogChannel{
					{ID: "east", Name: "Toonami East", PlayMode: "", Live: true},
				}},
			}},
	}}
	cfg.PresetViewer = bundledFakeViewer()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestCatalogDrawerTemplate|TestCatalogRailTemplate|TestCatalogGridTemplate" -v`

Expected: FAIL.

- [ ] **Step 3: Add `upper` and `pad2`-equivalent FuncMap helpers**

In [internal/chassis/templates.go](../../../internal/chassis/templates.go), `templateFuncs`:

```go
	"upper": strings.ToUpper,
```

(`pad2` already exists.)

- [ ] **Step 4: Create the three drawer templates**

Create [internal/chassis/templates/catalog-drawer.html](../../../internal/chassis/templates/catalog-drawer.html):

```html
{{define "catalog-drawer"}}
{{htmlComment "chassis:catalog-drawer"}}
<div class="catalog-drawer" id="catalog-drawer" aria-hidden="true">
  <div class="catalog-browser">
    <div class="catalog-provider-tabs">
      <span class="catalog-tab-indicator" id="catalog-tab-indicator"></span>
      {{- range .Providers -}}
      <button class="catalog-provider-tab{{if eq .ID $.ActiveProviderID}} active{{end}}"
              type="button"
              data-provider="{{.ID}}">
        <span class="ic {{.BadgeClass}}">{{.BadgeLabel}}</span>
        {{.DisplayName}}
        <span class="ch-count">{{.ChCount}}</span>
      </button>
      {{- end -}}
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

Create [internal/chassis/templates/catalog-rail.html](../../../internal/chassis/templates/catalog-rail.html):

```html
{{define "catalog-rail"}}
{{- $active := .ActiveGroupID -}}
{{- $providerIdx := .ProviderIndex .ActiveProviderID -}}
{{- with index .Providers $providerIdx -}}
{{- range $i, $g := .Groups -}}
<button class="catalog-rail-group{{if eq $g.ID $active}} active{{end}}"
        type="button"
        data-group="{{$g.ID}}"
        style="--i:{{$i}}">
  {{$g.Name}}<span class="count">{{$g.ChCount}}</span>
</button>
{{- end -}}
{{- end -}}
{{end}}
```

Create [internal/chassis/templates/catalog-grid.html](../../../internal/chassis/templates/catalog-grid.html):

```html
{{define "catalog-grid"}}
{{- $providerIdx := .ProviderIndex .ActiveProviderID -}}
{{- $groupIdx := .GroupIndex .ActiveProviderID .ActiveGroupID -}}
{{- with index .Providers $providerIdx -}}
{{- $p := . -}}
{{- with index .Groups $groupIdx -}}
{{- range $i, $c := .Channels -}}
<div role="button" tabindex="0"
     class="ch-card{{if $c.Tuned}} tuned{{end}}{{if $c.Starred}} starred{{end}}{{if $c.Live}} live{{end}}"
     data-provider="{{$p.ID}}"
     data-channel="{{$c.ID}}"
     style="--i:{{$i}}">
  <button class="star" type="button"
          title="{{if $c.Starred}}In preset {{pad2 $c.PresetSlot}}{{else}}Save to preset{{end}}">{{if $c.Starred}}&#9733;{{else}}&#9734;{{end}}</button>
  <div class="name">{{$c.Name}}</div>
  <div class="meta"><span>{{upper $c.ID}}</span><span class="mode">{{$c.PlayMode}}</span></div>
</div>
{{- end -}}
{{- end -}}
{{- end -}}
{{end}}
```

- [ ] **Step 5: Embed the drawer into the page**

The drawer is rendered as a sibling of `preset-bank`. Inspect [internal/chassis/templates/shell.html](../../../internal/chassis/templates/shell.html) to find where `{{template "preset-bank" .Presets}}` appears, and insert immediately after it:

```html
        {{template "catalog-drawer" .Catalog}}
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./internal/chassis -run "TestCatalogDrawerTemplate|TestCatalogRailTemplate|TestCatalogGridTemplate" -count=1 -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/templates/catalog-drawer.html internal/chassis/templates/catalog-rail.html internal/chassis/templates/catalog-grid.html internal/chassis/templates.go internal/chassis/templates/shell.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): add catalog drawer/rail/grid templates"
```

---

## Task 21: Preset-Bank Template Extension — Un-Disable BROWSE/Search, Pre-Render Provider Trees

**Files:**
- Modify: `internal/chassis/templates/preset-bank.html`
- Modify: `internal/chassis/chassis_test.go`

Un-disable the BROWSE button and search input; render closed-state mode label text server-side; emit one `<template id="catalog-tree-<providerID>">` block per provider holding that provider's rail+grid markup for client-side cloning.

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestPresetBankTemplate_BrowseAndSearchEnabled(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	if strings.Contains(html, `<button class="browse-btn" id="browse-toggle" disabled`) {
		t.Errorf("browse button should NOT be disabled after 3B")
	}
	if !strings.Contains(html, `id="browse-toggle"`) {
		t.Errorf("browse button missing id browse-toggle")
	}
	// Search input must not have disabled/readonly.
	if !strings.Contains(html, `id="search-input"`) {
		t.Errorf("search input missing")
	}
	for _, banned := range []string{
		`id="search-input" disabled`,
		`id="search-input" readonly`,
	} {
		if strings.Contains(html, banned) {
			t.Errorf("search input should not be %q after 3B", banned)
		}
	}
	// BROWSE label closed form: "▸ Browse full catalog (N)"
	if !strings.Contains(html, "Browse full catalog (") {
		t.Errorf("browse button missing 'Browse full catalog (N)' label")
	}
}

func TestPresetBankTemplate_EmitsCatalogTreeTemplatesPerProvider(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`<template id="catalog-tree-mtv-rewind">`,
		`<template id="catalog-tree-cartoon-rewind">`,
		`<template id="catalog-tree-toonami-aftermath">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("preset-bank missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestPresetBankTemplate_BrowseAndSearchEnabled|TestPresetBankTemplate_EmitsCatalogTreeTemplatesPerProvider" -v`

Expected: FAIL.

- [ ] **Step 3: Replace preset-bank.html**

Replace [internal/chassis/templates/preset-bank.html](../../../internal/chassis/templates/preset-bank.html) with:

```html
{{define "preset-bank"}}
{{htmlComment "chassis:preset-bank"}}
<div class="preset-strip preset-section">
  <span class="strip-label">Presets</span>
  <div>
    <div class="preset-header">
      <span class="title" id="preset-mode-label">{{.ModeLabel}}</span>
      <span class="count" id="preset-count">{{.Count}}</span>
      <div class="search-field" id="search-field">
        <input type="search" id="search-input" placeholder="FILTER PRESETS &middot; CATALOG" aria-label="Filter presets and catalog">
        <span class="search-scope" id="search-scope">&nbsp;</span>
      </div>
      <button class="browse-btn" id="browse-toggle" type="button" aria-expanded="false">{{template "browse-button-label" .}}</button>
    </div>
    <div class="preset-bank">
      {{- range $i, $slot := .Slots -}}
      <button class="preset{{if not $slot.Filled}} empty{{end}}{{if $slot.Lit}} lit{{end}}{{if $slot.Live}} live{{end}}"
              type="button"
              data-slot="{{$slot.Slot}}"
              data-provider="{{$slot.ProviderID}}"
              data-channel="{{$slot.ChannelID}}">
        <div class="num">{{pad2 (inc $i)}}</div>
        {{- if $slot.Filled -}}
        <div class="name">{{$slot.Title}}</div>
        <div class="badge {{$slot.BadgeClass}}">{{$slot.Subtitle}}{{if $slot.Live}} &middot; LIVE{{end}}</div>
        {{- end -}}
      </button>
      {{- end -}}
    </div>
  </div>
</div>
{{end}}

{{define "browse-button-label"}}
{{- /* Closed-state label only — JS swaps to "◂ Back to presets" on open. */ -}}
&#9656; Browse full catalog ({{.TotalChannels}})
{{end}}
```

The mode label closed-state form is already provided by `PresetsData.ModeLabel` (e.g., "Memory · 0 / 12 slots"). 3B doesn't change that string at server-render time; JS swaps to the open form. Update [internal/chassis/data.go](../../../internal/chassis/data.go) `buildPresetsData` so the closed form for N≥1 includes the drag hint:

In `buildPresetsData`, replace:

```go
	data.ModeLabel = fmt.Sprintf("Memory · %d / 12 slots", filled)
```

with:

```go
	if filled > 0 {
		data.ModeLabel = fmt.Sprintf("Memory · drag to reorder · %d / 12", filled)
	} else {
		data.ModeLabel = "Memory · 0 / 12 slots"
	}
```

The `{{template "browse-button-label" .}}` invocation passes `.` (PresetsData), and the template references `.CatalogTotalChannels` — the field added in Task 13 and populated in Tasks 13's `idleSnapshot` + Task 14's `snapshotFromStatusView`. No new struct field or wiring is needed in this task; the template just reads the existing field.

- [ ] **Step 4: Append catalog-tree templates to preset-bank.html**

At the bottom of `preset-bank.html`, after the closing `{{end}}` of the `preset-bank` template, append (still in the same file):

```html
{{define "preset-bank-catalog-trees"}}
{{- $cat := .Catalog -}}
{{- range $i, $p := $cat.Providers -}}
<template id="catalog-tree-{{$p.ID}}">
  <div class="catalog-tree-payload" data-provider="{{$p.ID}}">
    {{- range $gi, $g := $p.Groups -}}
    <button class="catalog-rail-group{{if and (eq $i 0) (eq $gi 0)}} active{{end}}"
            type="button"
            data-group="{{$g.ID}}"
            style="--i:{{$gi}}">
      {{$g.Name}}<span class="count">{{$g.ChCount}}</span>
    </button>
    {{- end -}}
    {{- range $gi, $g := $p.Groups -}}
    <div class="catalog-tree-grid" data-group="{{$g.ID}}"{{if not (and (eq $i 0) (eq $gi 0))}} hidden{{end}}>
      {{- range $ci, $c := $g.Channels -}}
      <div role="button" tabindex="0"
           class="ch-card{{if $c.Tuned}} tuned{{end}}{{if $c.Starred}} starred{{end}}{{if $c.Live}} live{{end}}"
           data-provider="{{$p.ID}}"
           data-channel="{{$c.ID}}"
           style="--i:{{$ci}}">
        <button class="star" type="button"
                title="{{if $c.Starred}}In preset {{pad2 $c.PresetSlot}}{{else}}Save to preset{{end}}">{{if $c.Starred}}&#9733;{{else}}&#9734;{{end}}</button>
        <div class="name">{{$c.Name}}</div>
        <div class="meta"><span>{{upper $c.ID}}</span><span class="mode">{{$c.PlayMode}}</span></div>
      </div>
      {{- end -}}
    </div>
    {{- end -}}
  </div>
</template>
{{- end -}}
{{end}}
```

Update [internal/chassis/templates/shell.html](../../../internal/chassis/templates/shell.html) to invoke the new tree templates after `{{template "preset-bank" .Presets}}` (passing `.` so the template sees the whole page data):

```html
        {{template "preset-bank-catalog-trees" .}}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/chassis -run "TestPresetBankTemplate" -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Run full chassis tests**

Run: `go test ./internal/chassis -count=1`

Expected: green. If any existing `TestPresetBank*` test asserts the old `disabled` BROWSE button or the old mode-label format, update its expectation strings.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/templates/preset-bank.html internal/chassis/templates/shell.html internal/chassis/data.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): un-disable BROWSE/search; emit catalog-tree templates for client cloning"
```

---

## Task 22: Shell Template Script Tags

**Files:**
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestShellTemplate_LoadsNew3BScripts(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`/receiver/static/source-cluster.js?v=`,
		`/receiver/static/catalog-browser.js?v=`,
		`/receiver/static/preset-reorder.js?v=`,
		`/receiver/static/search-filter.js?v=`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("shell.html missing %q script tag", want)
		}
	}
	// Required order: chassis.js → vfd-live.js → source-cluster.js →
	// preset-bank.js → catalog-browser.js → preset-reorder.js → search-filter.js.
	order := []string{
		"chassis.js",
		"vfd-live.js",
		"source-cluster.js",
		"preset-bank.js",
		"catalog-browser.js",
		"preset-reorder.js",
		"search-filter.js",
	}
	lastIdx := -1
	for _, name := range order {
		idx := strings.Index(html, name)
		if idx < 0 {
			t.Errorf("script %s missing from shell.html", name)
			continue
		}
		if idx < lastIdx {
			t.Errorf("script %s appears before its predecessor in shell.html", name)
		}
		lastIdx = idx
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestShellTemplate_LoadsNew3BScripts -v`

Expected: FAIL.

- [ ] **Step 3: Update shell.html**

Open [internal/chassis/templates/shell.html](../../../internal/chassis/templates/shell.html) and locate the existing `<script>` block. After the line that loads `preset-bank.js`, insert (in this exact order):

```html
    <script src="/receiver/static/source-cluster.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/catalog-browser.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/preset-reorder.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/search-filter.js?v={{.Version}}" defer></script>
```

Also move the `source-cluster.js` tag to its required position (after `vfd-live.js`, before `preset-bank.js`). The final order in shell.html's script block, top to bottom:

```html
    <script src="/receiver/static/chassis.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/vfd-live.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/source-cluster.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/transport.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/visualizer-bank.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/volume-knob.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/meter.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/input-cast.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/preset-bank.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/catalog-browser.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/preset-reorder.js?v={{.Version}}" defer></script>
    <script src="/receiver/static/search-filter.js?v={{.Version}}" defer></script>
```

(Inspect the existing shell.html to confirm the exact set and order of scripts present at 3A's tip; preserve any scripts not listed above. The minimum invariant is that the four 3B scripts appear in the listed positions.)

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/chassis -run TestShellTemplate_LoadsNew3BScripts -count=1 -v`

Expected: FAIL on the file presence checks (next step creates the JS files). The script tag count should now be correct, but the static handler may 404 for files that don't exist yet. The Step 4 test only checks that the HTML contains the script tags, so it should pass now — the actual JS files come in Tasks 23-26.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/shell.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): load source-cluster/catalog-browser/preset-reorder/search-filter JS in shell"
```

---

## Task 23: `source-cluster.js` — Lamp `.casting` Updates from `transport` SSE

**Files:**
- Create: `internal/chassis/static/source-cluster.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing presence/lint tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestSourceClusterJS_ExistsAndSubscribesToTransport(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/source-cluster.js")
	if err != nil {
		t.Fatalf("ReadFile source-cluster.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "window.Chassis.events.subscribe") {
		t.Errorf("source-cluster.js does not subscribe to events")
	}
	if !strings.Contains(s, "transport") {
		t.Errorf("source-cluster.js does not subscribe to 'transport' event")
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("source-cluster.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestSourceClusterJS_ExistsAndSubscribesToTransport -v`

Expected: FAIL.

- [ ] **Step 3: Create source-cluster.js**

Create [internal/chassis/static/source-cluster.js](../../../internal/chassis/static/source-cluster.js):

```javascript
(function () {
  'use strict';
  if (!window.Chassis || !window.Chassis.events || typeof window.Chassis.events.subscribe !== 'function') {
    console.warn('source-cluster: chassis events bus missing');
    return;
  }

  const KNOWN_SOURCES = ['streams', 'plex', 'jellyfin', 'dlna'];

  function parseAdapterRefSource(ref) {
    if (!ref || typeof ref !== 'string') return '';
    const colon = ref.indexOf(':');
    if (colon <= 0) return '';
    const id = ref.slice(0, colon);
    return KNOWN_SOURCES.indexOf(id) >= 0 ? id : '';
  }

  function applyCasting(activeSourceID) {
    document.querySelectorAll('.source-cluster .lamp').forEach((el) => {
      const id = el.getAttribute('data-source-id') || '';
      el.classList.toggle('casting', id !== '' && id === activeSourceID);
    });
  }

  function onTransport(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyCasting(parseAdapterRefSource(data.adapterRef));
  }

  window.Chassis.events.subscribe('transport', onTransport);
})();
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/chassis -run TestSourceClusterJS_ExistsAndSubscribesToTransport -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/source-cluster.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): add source-cluster.js for lamp .casting state from transport SSE"
```

---

## Task 24: `preset-bank.js` Extension — Subscribe to `presets` SSE Event

**Files:**
- Modify: `internal/chassis/static/preset-bank.js`
- Modify: `internal/chassis/chassis_test.go`

The 3A `preset-bank.js` already subscribes to `transport` for `.lit` migration. 3B adds a `presets` subscription that re-renders slot DOM content (name/badge/empty/live classes plus `data-provider`/`data-channel` attributes).

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestPresetBankJS_SubscribesToPresetsEvent(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/preset-bank.js")
	if err != nil {
		t.Fatalf("ReadFile preset-bank.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, `subscribe('presets'`) {
		t.Errorf("preset-bank.js does not subscribe to 'presets' event")
	}
	// Existing transport subscription must remain.
	if !strings.Contains(s, `subscribe('transport'`) {
		t.Errorf("preset-bank.js dropped transport subscription")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestPresetBankJS_SubscribesToPresetsEvent -v`

Expected: FAIL.

- [ ] **Step 3: Extend preset-bank.js**

Replace [internal/chassis/static/preset-bank.js](../../../internal/chassis/static/preset-bank.js) with:

```javascript
(function () {
  'use strict';
  const bank = document.querySelector('.preset-bank');
  if (!bank) return;

  function slots() { return Array.from(bank.querySelectorAll('.preset')); }

  function clearLit() {
    slots().forEach((el) => el.classList.remove('lit'));
  }

  function applyLit(providerId, channelId) {
    clearLit();
    if (!providerId || !channelId) return;
    for (const el of slots()) {
      if (el.dataset.provider === providerId && el.dataset.channel === channelId) {
        el.classList.add('lit');
        break;
      }
    }
  }

  function parseAdapterRef(ref) {
    if (!ref || typeof ref !== 'string') return [null, null];
    if (!ref.startsWith('streams:')) return [null, null];
    const parts = ref.split(':');
    if (parts.length < 3) return [null, null];
    return [parts[1], parts[2]];
  }

  function onTransport(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    const [providerId, channelId] = parseAdapterRef(data.adapterRef);
    applyLit(providerId, channelId);
  }

  function pad2(n) {
    return n < 10 ? '0' + n : '' + n;
  }

  function applyPresets(payload) {
    if (!payload || !Array.isArray(payload.slots)) return;
    const elements = slots();
    payload.slots.forEach((s, i) => {
      const el = elements[i];
      if (!el) return;
      const filled = !!s.provider && !!s.channel;
      el.classList.toggle('empty', !filled);
      el.classList.toggle('live', !!s.live);
      el.dataset.slot = String(s.slot);
      el.dataset.provider = s.provider || '';
      el.dataset.channel = s.channel || '';
      // Re-render slot content.
      let num = el.querySelector('.num');
      if (!num) {
        num = document.createElement('div');
        num.className = 'num';
        el.insertBefore(num, el.firstChild);
      }
      num.textContent = pad2(s.slot);
      const name = el.querySelector('.name');
      const badge = el.querySelector('.badge');
      if (filled) {
        if (!name) {
          const div = document.createElement('div');
          div.className = 'name';
          div.textContent = s.title || '';
          el.appendChild(div);
        } else {
          name.textContent = s.title || '';
        }
        const badgeText = (s.badgeLabel || '') + (s.live ? ' · LIVE' : '');
        if (!badge) {
          const div = document.createElement('div');
          div.className = 'badge ' + (s.badgeClass || '');
          div.textContent = badgeText;
          el.appendChild(div);
        } else {
          badge.className = 'badge ' + (s.badgeClass || '');
          badge.textContent = badgeText;
        }
      } else {
        if (name) name.remove();
        if (badge) badge.remove();
      }
    });
  }

  function onPresets(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyPresets(data);
    // After re-render, re-apply LIT if a cast is still active. The
    // transport SSE event will fire on its own cadence, but we want
    // the LIT highlight to survive a presets-only mutation (e.g., the
    // currently-tuned channel just got starred into a new slot).
    document.dispatchEvent(new CustomEvent('chassis:preset-rerendered'));
  }

  function reportError(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  bank.addEventListener('click', async (e) => {
    const btn = e.target.closest('.preset');
    if (!btn || btn.classList.contains('empty')) return;
    if (e.target.closest('.preset-drag-clone')) return;
    const slot = btn.dataset.slot;
    if (!slot) return;
    try {
      const resp = await fetch('/receiver/preset/' + encodeURIComponent(slot) + '/cast', {
        method: 'POST',
        credentials: 'same-origin',
      });
      const body = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!body.ok) reportError(body.chip);
    } catch (_) {
      reportError('CAST FAILED');
    }
  });

  if (window.Chassis && window.Chassis.events && typeof window.Chassis.events.subscribe === 'function') {
    window.Chassis.events.subscribe('transport', onTransport);
    window.Chassis.events.subscribe('presets', onPresets);
  }
})();
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/chassis -run TestPresetBankJS_SubscribesToPresetsEvent -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/preset-bank.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): subscribe preset-bank.js to presets SSE event"
```

---

## Task 25: `catalog-browser.js` — BROWSE Toggle, Tab Switching, Channel Cast, Star Toggle

**Files:**
- Create: `internal/chassis/static/catalog-browser.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestCatalogBrowserJS_ExistsAndIntegrationPoints(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/catalog-browser.js")
	if err != nil {
		t.Fatalf("ReadFile catalog-browser.js: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"browse-open",
		"catalog-scanning",
		"catalog-tab-indicator",
		"/receiver/streams/cast",
		"/receiver/preset/star",
		`subscribe('transport'`,
		`subscribe('presets'`,
		"stopPropagation",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("catalog-browser.js missing %q", want)
		}
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("catalog-browser.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestCatalogBrowserJS_ExistsAndIntegrationPoints -v`

Expected: FAIL.

- [ ] **Step 3: Create catalog-browser.js**

Create [internal/chassis/static/catalog-browser.js](../../../internal/chassis/static/catalog-browser.js):

```javascript
(function () {
  'use strict';
  if (!window.Chassis || !window.Chassis.events || typeof window.Chassis.events.subscribe !== 'function') {
    return;
  }
  const drawer = document.getElementById('catalog-drawer');
  const browseBtn = document.getElementById('browse-toggle');
  const railHost = document.getElementById('catalog-rail');
  const gridHost = document.getElementById('catalog-grid');
  if (!drawer || !browseBtn || !railHost || !gridHost) return;

  const tabsContainer = drawer.querySelector('.catalog-provider-tabs');
  const indicator = document.getElementById('catalog-tab-indicator');
  const modeLabel = document.getElementById('preset-mode-label');
  const browseClosedText = browseBtn.textContent.trim();

  let activeProviderID = (tabsContainer.querySelector('.catalog-provider-tab.active') || {}).dataset?.provider || '';
  let activeGroupID = (railHost.querySelector('.catalog-rail-group.active') || {}).dataset?.group || '';
  let isOpen = false;

  function getTreeTemplate(providerID) {
    return document.getElementById('catalog-tree-' + providerID);
  }

  function cloneRail(providerID) {
    const tpl = getTreeTemplate(providerID);
    if (!tpl) return [];
    return Array.from(tpl.content.querySelectorAll('button.catalog-rail-group'));
  }

  function cloneGrid(providerID, groupID) {
    const tpl = getTreeTemplate(providerID);
    if (!tpl) return null;
    const grid = tpl.content.querySelector('.catalog-tree-grid[data-group="' + cssEscape(groupID) + '"]');
    return grid ? grid.cloneNode(true) : null;
  }

  function cssEscape(s) {
    if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') return CSS.escape(s);
    return String(s).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
  }

  function setBrowseLabel() {
    browseBtn.textContent = isOpen ? '◂ Back to presets' : browseClosedText;
    browseBtn.setAttribute('aria-expanded', isOpen ? 'true' : 'false');
  }

  function setModeLabel() {
    if (!modeLabel) return;
    if (!isOpen) {
      // Server-rendered closed-state text is the source of truth.
      // Re-apply it on close.
      modeLabel.textContent = modeLabel.dataset.closedText || modeLabel.textContent;
      return;
    }
    const provName = (tabsContainer.querySelector(`.catalog-provider-tab[data-provider="${cssEscape(activeProviderID)}"]`) || {}).textContent || '';
    const groupBtn = railHost.querySelector('.catalog-rail-group.active');
    const groupName = groupBtn ? groupBtn.firstChild.textContent.trim() : '';
    const channelCount = gridHost.querySelectorAll('.ch-card').length;
    modeLabel.textContent = `Catalog · ${provName.trim()} · ${groupName} · ${channelCount} channels`;
  }

  function positionIndicator() {
    if (!indicator) return;
    const active = tabsContainer.querySelector('.catalog-provider-tab.active');
    if (!active) return;
    const tabsRect = tabsContainer.getBoundingClientRect();
    const r = active.getBoundingClientRect();
    indicator.style.transform = `translateX(${r.left - tabsRect.left}px)`;
    indicator.style.width = r.width + 'px';
  }

  function toggleBrowse() {
    isOpen = !isOpen;
    document.body.classList.toggle('browse-open', isOpen);
    drawer.setAttribute('aria-hidden', isOpen ? 'false' : 'true');
    if (isOpen) {
      document.body.classList.add('catalog-scanning');
      setTimeout(() => document.body.classList.remove('catalog-scanning'), 600);
      positionIndicator();
    }
    setBrowseLabel();
    setModeLabel();
  }

  function switchProvider(providerID) {
    if (!providerID || providerID === activeProviderID) return;
    activeProviderID = providerID;
    tabsContainer.querySelectorAll('.catalog-provider-tab').forEach((b) => {
      b.classList.toggle('active', b.dataset.provider === providerID);
    });
    // Repopulate rail from the hidden template.
    railHost.replaceChildren();
    const railButtons = cloneRail(providerID);
    railButtons.forEach((b) => railHost.appendChild(b.cloneNode(true)));
    const firstRailBtn = railHost.querySelector('.catalog-rail-group');
    activeGroupID = firstRailBtn ? firstRailBtn.dataset.group : '';
    railHost.querySelectorAll('.catalog-rail-group').forEach((b) => {
      b.classList.toggle('active', b.dataset.group === activeGroupID);
    });
    switchGrid(activeGroupID);
    positionIndicator();
    setModeLabel();
  }

  function switchGrid(groupID) {
    if (!groupID) return;
    activeGroupID = groupID;
    railHost.querySelectorAll('.catalog-rail-group').forEach((b) => {
      b.classList.toggle('active', b.dataset.group === groupID);
    });
    gridHost.replaceChildren();
    const cloned = cloneGrid(activeProviderID, groupID);
    if (cloned) {
      while (cloned.firstChild) gridHost.appendChild(cloned.firstChild);
    }
    setModeLabel();
  }

  browseBtn.addEventListener('click', toggleBrowse);

  tabsContainer.addEventListener('click', (e) => {
    const tab = e.target.closest('.catalog-provider-tab');
    if (!tab) return;
    switchProvider(tab.dataset.provider);
  });

  railHost.addEventListener('click', (e) => {
    const btn = e.target.closest('.catalog-rail-group');
    if (!btn) return;
    switchGrid(btn.dataset.group);
  });

  gridHost.addEventListener('click', (e) => {
    const star = e.target.closest('.star');
    if (star) {
      e.stopPropagation();
      handleStarClick(star);
      return;
    }
    const card = e.target.closest('.ch-card');
    if (!card) return;
    handleChannelCast(card);
  });

  gridHost.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    const target = e.target;
    if (target.classList && target.classList.contains('star')) {
      e.preventDefault();
      e.stopPropagation();
      handleStarClick(target);
      return;
    }
    const card = target.closest && target.closest('.ch-card');
    if (card) {
      e.preventDefault();
      handleChannelCast(card);
    }
  });

  function reportChip(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  async function handleChannelCast(card) {
    const provider = card.dataset.provider;
    const channel = card.dataset.channel;
    if (!provider || !channel) return;
    try {
      const body = new URLSearchParams({ provider, channel });
      const resp = await fetch('/receiver/streams/cast', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const json = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!json.ok) reportChip(json.chip);
    } catch (_) {
      reportChip('CAST FAILED');
    }
  }

  async function handleStarClick(star) {
    const card = star.closest('.ch-card');
    if (!card) return;
    const provider = card.dataset.provider;
    const channel = card.dataset.channel;
    if (!provider || !channel) return;
    const wasStarred = card.classList.contains('starred');
    const desired = !wasStarred;
    star.classList.add('pending');
    try {
      const body = new URLSearchParams({ provider, channel, starred: desired ? 'true' : 'false' });
      const resp = await fetch('/receiver/preset/star', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const json = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!json.ok) {
        reportChip(json.chip);
        return;
      }
      // Visual flip happens on the next `presets` SSE tick, which is
      // imminent because the chassis calls refreshSnapshotNow().
    } catch (_) {
      reportChip('CAST FAILED');
    } finally {
      star.classList.remove('pending');
    }
  }

  function applyTuned(providerId, channelId) {
    document.querySelectorAll('.ch-card').forEach((el) => {
      el.classList.toggle('tuned',
        el.dataset.provider === providerId && el.dataset.channel === channelId &&
        providerId !== '' && channelId !== '');
    });
    // Same for the hidden trees so a tab switch shows the right .tuned card.
    document.querySelectorAll('template[id^="catalog-tree-"]').forEach((tpl) => {
      tpl.content.querySelectorAll('.ch-card').forEach((el) => {
        el.classList.toggle('tuned',
          el.dataset.provider === providerId && el.dataset.channel === channelId &&
          providerId !== '' && channelId !== '');
      });
    });
  }

  function applyStars(payload) {
    if (!payload || !Array.isArray(payload.slots)) return;
    const membership = new Set();
    payload.slots.forEach((s) => {
      if (s.provider && s.channel) membership.add(s.provider + ':' + s.channel);
    });
    const stars = document.querySelectorAll('.ch-card');
    stars.forEach((el) => {
      const key = (el.dataset.provider || '') + ':' + (el.dataset.channel || '');
      el.classList.toggle('starred', membership.has(key));
    });
    document.querySelectorAll('template[id^="catalog-tree-"]').forEach((tpl) => {
      tpl.content.querySelectorAll('.ch-card').forEach((el) => {
        const key = (el.dataset.provider || '') + ':' + (el.dataset.channel || '');
        el.classList.toggle('starred', membership.has(key));
      });
    });
  }

  function parseAdapterRef(ref) {
    if (!ref || typeof ref !== 'string' || !ref.startsWith('streams:')) return [null, null];
    const parts = ref.split(':');
    if (parts.length < 3) return [null, null];
    return [parts[1], parts[2]];
  }

  window.Chassis.events.subscribe('transport', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    const [provider, channel] = parseAdapterRef(data.adapterRef);
    applyTuned(provider || '', channel || '');
  });

  window.Chassis.events.subscribe('presets', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyStars(data);
  });

  // Capture the server-rendered closed-state mode label so we can
  // restore it on close (open-state form is computed in setModeLabel).
  if (modeLabel) modeLabel.dataset.closedText = modeLabel.textContent;
})();
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/chassis -run TestCatalogBrowserJS_ExistsAndIntegrationPoints -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/catalog-browser.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): add catalog-browser.js for BROWSE/tab/star/cast"
```

---

## Task 26: `preset-reorder.js` + `search-filter.js`

**Files:**
- Create: `internal/chassis/static/preset-reorder.js`
- Create: `internal/chassis/static/search-filter.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestPresetReorderJS_PointerEventsAndMoveRoute(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/preset-reorder.js")
	if err != nil {
		t.Fatalf("ReadFile preset-reorder.js: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"pointerdown",
		"pointermove",
		"pointerup",
		"/receiver/preset/move",
		"Ctrl",
		"ArrowLeft",
		"ArrowRight",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("preset-reorder.js missing %q", want)
		}
	}
	for _, forbidden := range []string{"draggable=\"true\"", "dragstart"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("preset-reorder.js uses HTML5 native DnD %q (must be pointer-based)", forbidden)
		}
	}
}

func TestSearchFilterJS_TogglesFilterMissClass(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/search-filter.js")
	if err != nil {
		t.Fatalf("ReadFile search-filter.js: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"#search-input",
		"filter-miss",
		"Escape",
		`subscribe('presets'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("search-filter.js missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`style.opacity`,
		`style.pointerEvents`,
	} {
		if strings.Contains(s, forbidden) {
			t.Errorf("search-filter.js uses inline-style %q (must toggle classes only)", forbidden)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestPresetReorderJS|TestSearchFilterJS" -v`

Expected: FAIL.

- [ ] **Step 3: Create preset-reorder.js**

Create [internal/chassis/static/preset-reorder.js](../../../internal/chassis/static/preset-reorder.js):

```javascript
(function () {
  'use strict';
  const bank = document.querySelector('.preset-bank');
  if (!bank) return;

  let dragging = null;     // { from, sourceEl, clone, startX, startY, lastTarget }
  const DRAG_THRESHOLD = 5; // px before pointermove transitions to drag

  function preset(el) {
    return el && el.classList && el.classList.contains('preset') ? el : (el ? el.closest('.preset') : null);
  }

  bank.addEventListener('pointerdown', (e) => {
    if (e.button !== 0 && e.pointerType === 'mouse') return;
    const target = preset(e.target);
    if (!target || target.classList.contains('empty')) return;
    dragging = {
      from: parseInt(target.dataset.slot, 10),
      sourceEl: target,
      clone: null,
      startX: e.clientX,
      startY: e.clientY,
      lastTarget: null,
      pointerId: e.pointerId,
    };
    target.setPointerCapture(e.pointerId);
    e.preventDefault();
  });

  bank.addEventListener('pointermove', (e) => {
    if (!dragging || e.pointerId !== dragging.pointerId) return;
    const dx = e.clientX - dragging.startX;
    const dy = e.clientY - dragging.startY;
    if (!dragging.clone) {
      if (Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      // Begin drag.
      const rect = dragging.sourceEl.getBoundingClientRect();
      const clone = dragging.sourceEl.cloneNode(true);
      clone.classList.add('preset-drag-clone');
      clone.style.position = 'fixed';
      clone.style.left = rect.left + 'px';
      clone.style.top = rect.top + 'px';
      clone.style.width = rect.width + 'px';
      clone.style.height = rect.height + 'px';
      clone.style.pointerEvents = 'none';
      clone.style.zIndex = '9999';
      document.body.appendChild(clone);
      dragging.clone = clone;
      dragging.sourceEl.setAttribute('data-dragging', 'source');
      document.body.style.cursor = 'grabbing';
    }
    dragging.clone.style.transform = `translate(${dx}px, ${dy}px)`;
    const below = document.elementFromPoint(e.clientX, e.clientY);
    const target = preset(below);
    if (target && target !== dragging.lastTarget) {
      if (dragging.lastTarget) dragging.lastTarget.classList.remove('drop-target');
      if (target !== dragging.sourceEl) target.classList.add('drop-target');
      dragging.lastTarget = target;
    }
  });

  bank.addEventListener('pointerup', async (e) => {
    if (!dragging || e.pointerId !== dragging.pointerId) return;
    const finish = (cleanup) => {
      if (cleanup && dragging) {
        if (dragging.clone) dragging.clone.remove();
        if (dragging.sourceEl) dragging.sourceEl.removeAttribute('data-dragging');
        if (dragging.lastTarget) dragging.lastTarget.classList.remove('drop-target');
      }
      document.body.style.cursor = '';
      dragging = null;
    };
    if (!dragging.clone) { finish(false); return; }
    const target = dragging.lastTarget;
    if (!target || target === dragging.sourceEl) { finish(true); return; }
    const to = parseInt(target.dataset.slot, 10);
    const from = dragging.from;
    finish(true);
    if (!Number.isFinite(from) || !Number.isFinite(to)) return;
    await postMove(from, to);
  });

  bank.addEventListener('pointercancel', () => {
    if (dragging) {
      if (dragging.clone) dragging.clone.remove();
      if (dragging.sourceEl) dragging.sourceEl.removeAttribute('data-dragging');
      if (dragging.lastTarget) dragging.lastTarget.classList.remove('drop-target');
      document.body.style.cursor = '';
      dragging = null;
    }
  });

  bank.addEventListener('keydown', async (e) => {
    if (!e.ctrlKey) return;
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    const target = preset(e.target);
    if (!target || target.classList.contains('empty')) return;
    const from = parseInt(target.dataset.slot, 10);
    if (!Number.isFinite(from)) return;
    let to;
    if (e.key === 'ArrowLeft') {
      to = from === 1 ? 12 : from - 1;
    } else {
      to = from === 12 ? 1 : from + 1;
    }
    e.preventDefault();
    await postMove(from, to);
  });

  function reportChip(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  async function postMove(from, to) {
    try {
      const body = new URLSearchParams({ from: String(from), to: String(to) });
      const resp = await fetch('/receiver/preset/move', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const json = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!json.ok) reportChip(json.chip);
    } catch (_) {
      reportChip('CAST FAILED');
    }
  }
})();
```

- [ ] **Step 4: Create search-filter.js**

Create [internal/chassis/static/search-filter.js](../../../internal/chassis/static/search-filter.js):

```javascript
(function () {
  'use strict';
  const input = document.getElementById('search-input');
  const scope = document.getElementById('search-scope');
  const field = document.getElementById('search-field');
  if (!input) return;

  let query = '';

  function matchesPreset(el, q) {
    if (!q) return true;
    const name = (el.querySelector('.name') || {}).textContent || '';
    const badge = (el.querySelector('.badge') || {}).textContent || '';
    const channel = el.dataset.channel || '';
    return (name + ' ' + badge + ' ' + channel).toLowerCase().indexOf(q) >= 0;
  }

  function matchesCard(el, q) {
    if (!q) return true;
    const name = (el.querySelector('.name') || {}).textContent || '';
    const channel = el.dataset.channel || '';
    const provider = el.dataset.provider || '';
    return (name + ' ' + channel + ' ' + provider).toLowerCase().indexOf(q) >= 0;
  }

  function applyFilter() {
    const q = query.toLowerCase().trim();
    if (field) field.classList.toggle('has-value', q !== '');
    let presetMatches = 0;
    let catalogMatches = 0;
    document.querySelectorAll('.preset-bank .preset').forEach((el) => {
      const ok = matchesPreset(el, q);
      el.classList.toggle('filter-miss', !ok);
      if (ok && !el.classList.contains('empty')) presetMatches += 1;
    });
    document.querySelectorAll('.catalog-grid .ch-card').forEach((el) => {
      const ok = matchesCard(el, q);
      el.classList.toggle('filter-miss', !ok);
      if (ok) catalogMatches += 1;
    });
    if (scope) {
      scope.textContent = q === '' ? ' ' : (`presets: ${presetMatches} · catalog: ${catalogMatches}`);
    }
  }

  input.addEventListener('input', () => {
    query = input.value || '';
    applyFilter();
  });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      input.value = '';
      query = '';
      applyFilter();
    }
  });

  // Re-apply when presets change (filtered-out slots may have become
  // empty/filled and need their .filter-miss class re-derived).
  if (window.Chassis && window.Chassis.events && typeof window.Chassis.events.subscribe === 'function') {
    window.Chassis.events.subscribe('presets', () => applyFilter());
  }
  // Re-apply when the catalog drawer opens (grid contents change).
  document.addEventListener('chassis:preset-rerendered', () => applyFilter());

  // Initial paint.
  applyFilter();
})();
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/chassis -run "TestPresetReorderJS|TestSearchFilterJS" -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/static/preset-reorder.js internal/chassis/static/search-filter.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): add preset-reorder.js (pointer drag) and search-filter.js"
```

---

## Task 27: CSS Additions — Lamps, Catalog Drawer, Drag-Reorder, Search Filter

**Files:**
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/css_scope_test.go` (extend allowlists for new selectors)
- Modify: `internal/chassis/chassis_test.go` (selector presence assertions)

All new rules must be scoped under `body.receiver` per the existing chassis CSS invariant (enforced by `TestChassisCSS_AllSelectorsScoped`). The 3B mockup already contains the canonical CSS in the reference HTML — port the rules verbatim with `body.receiver` prefixes prepended.

**Prerequisites to verify before adding new CSS:**

Run `grep -n "body.receiver .ch-card\|body.receiver .catalog-body\|body.receiver .catalog-grid\|@keyframes rec-pulse\|body.receiver.catalog-scanning" internal/chassis/static/chassis.css` first. Several selectors already exist from earlier chassis phases (foundation port included `.ch-card`, `.ch-card.tuned`, `.ch-card.live`, `.ch-card .meta`, `.catalog-body`, `.catalog-grid`, the `rec-pulse` keyframe, and `body.receiver.catalog-scanning .vfd::after` plus its reduced-motion override). **Do NOT overwrite them.** The CSS additions below add new properties via separate rule blocks (rules with the same selector merge in CSS) and add only selectors that do not yet exist.

Selectors that are entirely new in 3B (verify these are NOT in chassis.css before adding):
- `body.receiver .source-cluster .lamp` (and its `.configured-idle` / `.casting` state variants)
- `body.receiver .catalog-rail-group`, `body.receiver .catalog-tab-indicator`, `body.receiver .catalog-provider-tabs`, `body.receiver .catalog-provider-tab`
- `body.receiver .ch-card .star` (and `.starred` variant)
- `@keyframes ch-card-in`, `@keyframes rail-in`
- `body.receiver .preset[data-dragging="source"]`, `body.receiver .preset.drop-target`, `body.receiver .preset-drag-clone`
- `body.receiver .search-field`, `body.receiver .filter-miss:not(.tuned)`

If any of these *do* already exist (perhaps the foundation port included visual stubs beyond what this audit caught), reconcile by reading the existing rule and either:
- merging its properties into the new rule block (if its property set is a subset of the new one), or
- leaving it in place and skipping that rule in the additions below (if its property set already matches the spec).

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestChassisCSS_AddsLampRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"body.receiver .source-cluster .lamp",
		"body.receiver .source-cluster .lamp .led",
		"body.receiver .source-cluster .lamp .name",
		"body.receiver .source-cluster .lamp.configured-idle .led",
		"body.receiver .source-cluster .lamp.casting .led",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing lamp selector %q", want)
		}
	}
}

func TestChassisCSS_AddsCatalogDrawerRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"body.receiver .catalog-drawer",
		"body.receiver .catalog-browser",
		"body.receiver .catalog-provider-tabs",
		"body.receiver .catalog-tab-indicator",
		"body.receiver .catalog-rail",
		"body.receiver .catalog-grid",
		"body.receiver .ch-card",
		"body.receiver .ch-card .star",
		"body.receiver .ch-card.tuned",
		"body.receiver .ch-card.starred",
		"body.receiver .ch-card.live",
		"@keyframes ch-card-in",
		"@keyframes rail-in",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing catalog drawer selector %q", want)
		}
	}
}

func TestChassisCSS_AddsDragReorderRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		`body.receiver .preset[data-dragging="source"]`,
		"body.receiver .preset.drop-target",
		"body.receiver .preset-drag-clone",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing drag selector %q", want)
		}
	}
}

func TestChassisCSS_AddsSearchFilterRules(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		"body.receiver .search-field",
		"body.receiver .search-field.has-value",
		"body.receiver .filter-miss:not(.tuned)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing search filter selector %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestChassisCSS_AddsLampRules|TestChassisCSS_AddsCatalogDrawerRules|TestChassisCSS_AddsDragReorderRules|TestChassisCSS_AddsSearchFilterRules" -v`

Expected: FAIL.

- [ ] **Step 3: Add the CSS sections to chassis.css**

Open [internal/chassis/static/chassis.css](../../../internal/chassis/static/chassis.css) and append the four new sections.

**Source-cluster lamps** (place at the end of the existing source-cluster section, after the `body.receiver .source-cluster` block):

```css
/* ---- Source-cluster lamps (3B) ---- */

body.receiver .source-cluster .lamp {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  min-height: 32px;
  border-radius: 3px;
  background: linear-gradient(180deg, #1a1a1d, #0e0e10);
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.04), 0 1px 2px rgba(0,0,0,0.6);
  font: 600 11px Inter, sans-serif;
  letter-spacing: 0.16em;
  text-transform: uppercase;
}

body.receiver .source-cluster .lamp .led {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: radial-gradient(circle at 35% 35%, #2a2a2e, #050506 75%);
  box-shadow: inset 0 0 1px rgba(0,0,0,0.6);
  transition: background 200ms ease-out, box-shadow 200ms ease-out;
}

body.receiver .source-cluster .lamp .name {
  color: #5a5a5e;
  transition: color 200ms ease-out, text-shadow 200ms ease-out;
}

body.receiver .source-cluster .lamp.configured-idle .led {
  background: radial-gradient(circle at 35% 35%, #d99340, #6b3c10 70%);
  box-shadow: 0 0 6px rgba(217,147,64,0.4), inset 0 0 1px rgba(0,0,0,0.4);
}

body.receiver .source-cluster .lamp.configured-idle .name {
  color: var(--lock-amber, #d99340);
}

body.receiver .source-cluster .lamp.casting .led {
  background: radial-gradient(circle at 35% 35%, #67ffb5, #105d3a 70%);
  box-shadow: 0 0 10px rgba(103,255,181,0.7), inset 0 0 1px rgba(0,0,0,0.4);
}

body.receiver .source-cluster .lamp.casting .name {
  color: var(--vfd, #67ffb5);
  text-shadow: 0 0 6px rgba(103,255,181,0.4);
}
```

**Catalog drawer** (place after the existing preset-bank section):

The mockup's catalog drawer CSS lives at lines 2190-2502 of `docs/superpowers/reference/2026-05-21-receiver-v24.html`. Port verbatim, scoping each selector with `body.receiver`. Use this skeleton — every selector matches a known-existing block in the reference:

```css
/* ---- Catalog drawer (3B) ---- */

body.receiver .catalog-drawer {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 280ms ease-out;
  overflow: hidden;
}

body.receiver.browse-open .catalog-drawer {
  grid-template-rows: 1fr;
}

body.receiver .catalog-browser {
  min-height: 0;
  margin-top: 12px;
  padding: 12px;
  background: linear-gradient(180deg, #0c0c0e, #060606);
  border: 1px solid #1c1c1f;
  border-radius: 4px;
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.04), 0 4px 8px rgba(0,0,0,0.5);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

body.receiver .catalog-provider-tabs {
  position: relative;
  display: flex;
  gap: 4px;
  border-bottom: 1px solid #1c1c1f;
}

body.receiver .catalog-provider-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: transparent;
  border: none;
  color: #8a8a8e;
  font: 600 11px Inter, sans-serif;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  cursor: pointer;
  transition: color 180ms ease-out;
}

body.receiver .catalog-provider-tab:hover { color: #c0c0c4; }
body.receiver .catalog-provider-tab.active { color: var(--lock-amber, #d99340); }

body.receiver .catalog-provider-tab .ic {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 32px;
  padding: 2px 4px;
  background: #1a1a1d;
  border-radius: 2px;
  font: 700 9px Inter, sans-serif;
  letter-spacing: 0.08em;
}

body.receiver .catalog-provider-tab .ic.mtv     { background: #6e2b0c; color: #ffd29c; }
body.receiver .catalog-provider-tab .ic.cartoon { background: #133e6e; color: #b5dbff; }
body.receiver .catalog-provider-tab .ic.toonami { background: #38136e; color: #d2b5ff; }

body.receiver .catalog-provider-tab .ch-count {
  margin-left: 6px;
  padding: 1px 6px;
  border-radius: 8px;
  background: #1a1a1d;
  color: #6a6a6e;
  font: 600 9px Inter, sans-serif;
}

body.receiver .catalog-tab-indicator {
  position: absolute;
  bottom: -1px;
  left: 0;
  height: 2px;
  background: var(--lock-amber, #d99340);
  transition: transform 220ms ease-out, width 220ms ease-out;
  pointer-events: none;
}

body.receiver .catalog-body {
  display: grid;
  grid-template-columns: 168px 1fr;
  gap: 10px;
  min-height: 220px;
}

body.receiver .catalog-rail {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

body.receiver .catalog-rail-group {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 10px;
  background: transparent;
  border: none;
  color: #8a8a8e;
  font: 500 10px Inter, sans-serif;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  cursor: pointer;
  text-align: left;
  border-radius: 3px;
  animation: rail-in 240ms ease-out both;
  animation-delay: calc(40ms * var(--i, 0));
  transition: background 180ms ease-out, color 180ms ease-out;
}

body.receiver .catalog-rail-group:hover { background: #1a1a1d; color: #c0c0c4; }
body.receiver .catalog-rail-group.active { background: #2a1f10; color: var(--lock-amber, #d99340); }

body.receiver .catalog-rail-group .count {
  color: #5a5a5e;
  font: 500 9px Inter, sans-serif;
}

body.receiver .catalog-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: 6px;
  align-content: start;
}

body.receiver .ch-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  background: linear-gradient(180deg, #131316, #0a0a0c);
  border: 1px solid #1c1c1f;
  border-radius: 3px;
  cursor: pointer;
  font: 600 11px Inter, sans-serif;
  color: #c0c0c4;
  user-select: none;
  animation: ch-card-in 220ms ease-out both;
  animation-delay: calc(20ms * var(--i, 0));
  transition: border-color 180ms ease-out, background 180ms ease-out;
}

body.receiver .ch-card:hover { border-color: #2a2a2e; background: linear-gradient(180deg, #18181b, #0c0c0e); }
body.receiver .ch-card:focus-visible { outline: 2px solid var(--lock-amber, #d99340); outline-offset: 2px; }

body.receiver .ch-card .name {
  color: inherit;
  letter-spacing: 0.04em;
}

body.receiver .ch-card .meta {
  display: flex;
  justify-content: space-between;
  color: #6a6a6e;
  font: 500 9px Inter, sans-serif;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

body.receiver .ch-card .meta .mode { color: #8a8a8e; }

body.receiver .ch-card .star {
  position: absolute;
  top: 4px;
  right: 4px;
  background: transparent;
  border: none;
  color: #5a5a5e;
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
  padding: 2px;
}

body.receiver .ch-card .star:hover { color: var(--lock-amber, #d99340); }
body.receiver .ch-card.starred .star { color: var(--lock-amber, #d99340); }
body.receiver .ch-card .star.pending { opacity: 0.6; }

body.receiver .ch-card.tuned {
  border-color: var(--vfd, #67ffb5);
  background: linear-gradient(180deg, #0f1d18, #06120e);
  color: #d2ffe9;
}

body.receiver .ch-card.live::after {
  content: "";
  position: absolute;
  top: 8px;
  right: 24px;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #ff4040;
  box-shadow: 0 0 6px rgba(255,64,64,0.6);
  animation: rec-pulse 1.6s ease-in-out infinite;
}

@keyframes ch-card-in {
  from { opacity: 0; transform: translateY(4px); }
  to   { opacity: 1; transform: translateY(0); }
}

@keyframes rail-in {
  from { opacity: 0; transform: translateX(-4px); }
  to   { opacity: 1; transform: translateX(0); }
}

@media (prefers-reduced-motion: reduce) {
  body.receiver .ch-card,
  body.receiver .catalog-rail-group {
    animation: none;
  }
}
```

**Drag-reorder visuals**:

```css
/* ---- Preset drag-reorder (3B) ---- */

body.receiver .preset[data-dragging="source"] {
  opacity: 0.3;
  transform: scale(0.96);
}

body.receiver .preset.drop-target {
  border-color: var(--lock-amber, #d99340);
  box-shadow: 0 0 8px rgba(217,147,64,0.45);
}

body.receiver .preset-drag-clone {
  opacity: 0.85;
  box-shadow: 0 6px 14px rgba(0,0,0,0.65);
  pointer-events: none;
  transform-origin: center;
}

body.receiver .preset {
  transition: transform 200ms ease-out, border-color 180ms ease-out;
}
```

**Search filter visuals**:

```css
/* ---- Search filter (3B) ---- */

body.receiver .search-field {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  background: #0a0a0c;
  border: 1px solid #1c1c1f;
  border-radius: 3px;
  transition: border-color 180ms ease-out;
}

body.receiver .search-field input {
  background: transparent;
  border: none;
  outline: none;
  color: #c0c0c4;
  font: 500 10px Inter, sans-serif;
  letter-spacing: 0.08em;
  width: 220px;
}

body.receiver .search-field .search-scope {
  color: #6a6a6e;
  font: 500 9px Inter, sans-serif;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

body.receiver .search-field.has-value {
  border-color: var(--lock-amber, #d99340);
}

body.receiver .filter-miss:not(.tuned) {
  opacity: 0.22;
  pointer-events: none;
  transition: opacity 180ms ease-out;
}
```

- [ ] **Step 4: Confirm scope-test still passes**

Run: `go test ./internal/chassis -run "TestChassisCSS_AllSelectorsScoped|TestChassisCSS_LeakProneSelectorsAreScoped" -v`

Expected: PASS. Every new selector above is prefixed with `body.receiver`.

- [ ] **Step 5: Run new tests**

Run: `go test ./internal/chassis -run "TestChassisCSS_AddsLampRules|TestChassisCSS_AddsCatalogDrawerRules|TestChassisCSS_AddsDragReorderRules|TestChassisCSS_AddsSearchFilterRules" -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Update ruleset count threshold**

`TestChassisCSS_RulesetCountSanity` asserts `count >= 450`. The 3B CSS adds roughly 35-50 new rulesets. Bump the threshold:

In [internal/chassis/css_scope_test.go](../../../internal/chassis/css_scope_test.go) `TestChassisCSS_RulesetCountSanity`, change:

```go
	const minRulesets = 450
```

to:

```go
	const minRulesets = 485
```

(Adjust the number once. The "at least N" floor is meant to catch accidental truncation, not to be a precise count.)

- [ ] **Step 7: Run full chassis tests**

Run: `go test ./internal/chassis -count=1`

Expected: green.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/static/chassis.css internal/chassis/chassis_test.go internal/chassis/css_scope_test.go
git commit -m "feat(chassis): add CSS for lamps, catalog drawer, drag-reorder, search filter"
```

---

## Task 28: Wire `main.go` + End-to-End Integration Tests + Final Verification

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`
- Modify: `tests/integration/chassis_test.go`

`main.go` already wires `PresetViewer` and `PresetCaster` for streams (3A); 3B extends this with three additional viewer/editor/caster casts AND assembles the `SourceAvailabilityViewers` slice from registered adapters that implement the interface.

- [ ] **Step 1: Write failing integration tests**

Append to [tests/integration/chassis_test.go](../../../tests/integration/chassis_test.go):

```go
func TestReceiverStreamsCast_EndToEnd(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()

	form := url.Values{"provider": {"mtv-rewind"}, "channel": {"80s"}}
	resp := env.PostForm("/receiver/streams/cast", form)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	// Fake streams caster recorded the call.
	if got := env.fakeStreams.lastChannelCast; got != ("mtv-rewind:80s") {
		t.Errorf("CastChannel arg = %q, want mtv-rewind:80s", got)
	}
}

func TestReceiverPresetStar_AddRemoveBankFull(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()

	// Empty bank: add 80s → slot 1.
	resp := env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	body := drainJSON(t, resp)
	if !body["ok"].(bool) || int(body["slot"].(float64)) != 1 {
		t.Errorf("first add body = %v, want ok=true slot=1", body)
	}

	// Repeated add: no-op, returns slot 1.
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	body = drainJSON(t, resp)
	if !body["ok"].(bool) || int(body["slot"].(float64)) != 1 {
		t.Errorf("repeat add body = %v, want ok=true slot=1", body)
	}

	// Fill the remaining 11 slots.
	for i, ch := range []string{"90s", "trl", "120minutes", "unplugged", "amp", "loonytunes", "animaniacs", "heman", "all", "east", "movies"} {
		provider := "mtv-rewind"
		if i >= 5 {
			provider = "cartoon-rewind"
		}
		if i >= 9 {
			provider = "toonami-aftermath"
		}
		resp = env.PostForm("/receiver/preset/star", url.Values{
			"provider": {provider}, "channel": {ch}, "starred": {"true"},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("fill %d %s/%s status = %d", i, provider, ch, resp.StatusCode)
		}
	}

	// Add 13th channel → 409 BANK FULL.
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"fuse"}, "starred": {"true"},
	})
	if resp.StatusCode != 409 {
		t.Errorf("13th add status = %d, want 409", resp.StatusCode)
	}
	body = drainJSON(t, resp)
	if body["chip"] != "BANK FULL" {
		t.Errorf("13th add chip = %v, want BANK FULL", body["chip"])
	}

	// Remove 80s → cleared=[1].
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"false"},
	})
	body = drainJSON(t, resp)
	cleared := body["cleared"].([]any)
	if len(cleared) != 1 || int(cleared[0].(float64)) != 1 {
		t.Errorf("remove cleared = %v, want [1]", body["cleared"])
	}

	// Repeat remove is a no-op success.
	resp = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"false"},
	})
	if resp.StatusCode != 200 {
		t.Errorf("repeat remove status = %d, want 200", resp.StatusCode)
	}
}

func TestReceiverPresetMove_Swap(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()

	// Seed two distinct slots first.
	_ = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"true"},
	})
	_ = env.PostForm("/receiver/preset/star", url.Values{
		"provider": {"cartoon-rewind"}, "channel": {"heman"}, "starred": {"true"},
	})

	resp := env.PostForm("/receiver/preset/move", url.Values{
		"from": {"1"}, "to": {"2"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("move status = %d, want 200", resp.StatusCode)
	}

	// Read presets via the SSE stream and assert the swap landed.
	slots := env.NextPresetsEvent(t, 2*time.Second)
	if slots[0]["provider"] != "cartoon-rewind" || slots[1]["provider"] != "mtv-rewind" {
		t.Errorf("post-swap slots[1]=%v slots[2]=%v, want cartoon-rewind / mtv-rewind",
			slots[0]["provider"], slots[1]["provider"])
	}
}

func TestReceiverEvents_PresetsBetweenMeterAndAudio(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	names := env.CollectInitialEventNames(t, 2*time.Second)
	want := []string{"state", "vfd", "source", "visualizer", "transport", "volume", "meter", "presets", "audio"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("initial burst events = %v, want %v", names, want)
	}
}

func TestReceiverPresetStar_PersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	env1 := newChassisIntegrationEnvIn(t, dir)
	_ = env1.PostForm("/receiver/preset/star", url.Values{
		"provider": {"mtv-rewind"}, "channel": {"80s"}, "starred": {"false"},
	})
	env1.Close()

	env2 := newChassisIntegrationEnvIn(t, dir)
	defer env2.Close()
	slots := env2.NextPresetsEvent(t, 2*time.Second)
	// 80s was bundled into slot 2; after removal+restart it should be empty.
	if slots[1]["provider"] != "" {
		t.Errorf("restart slot 2 provider = %v, want empty (was removed)", slots[1]["provider"])
	}
}
```

`newChassisIntegrationEnv` / `newChassisIntegrationEnvIn` / helpers (`PostForm`, `NextPresetsEvent`, `CollectInitialEventNames`, `drainJSON`) follow the 3A pattern in the same file. Inspect [tests/integration/chassis_test.go](../../../tests/integration/chassis_test.go) for existing helper shapes before adding new ones; reuse what's there.

**Critical pre-condition for `TestReceiverPresetStar_AddRemoveBankFull`:** The real streams adapter initializes from `bundledChassisPresets` (12 filled slots by default). To get an "empty bank" starting state, the env must pre-write an empty `chassis_presets.json` in the temp `data_dir` BEFORE the streams adapter's `New()` runs:

```go
func newChassisIntegrationEnv(t *testing.T) *chassisEnv {
	dir := t.TempDir()
	// Pre-seed an empty preset store so the test starts with all 12 slots empty.
	emptyDoc := []byte(`{"version":1,"slots":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "chassis_presets.json"), emptyDoc, 0o600); err != nil {
		t.Fatalf("seed empty presets: %v", err)
	}
	return newChassisIntegrationEnvIn(t, dir)
}
```

Tests that need the bundled defaults (e.g. `TestReceiverEvents_PresetsBetweenMeterAndAudio`, where the initial-burst content is incidental) call `newChassisIntegrationEnvWithDefaults` instead, which calls `newChassisIntegrationEnvIn` without pre-seeding the file (the streams adapter then seeds from `bundledChassisPresets` itself).

Required helper additions if not already in the file:

- A `fakeStreamsCaster` exposing `lastChannelCast string` ("provider:channel" of the most recent CastChannel call). Wired in place of the real streams adapter for `TestReceiverStreamsCast_EndToEnd` to assert the forwarded args without running the real cast machinery.
- `PostForm(path string, form url.Values) *http.Response`.
- `CollectInitialEventNames(t *testing.T, deadline time.Duration) []string` — opens a fresh `/receiver/events` SSE stream, collects each `event: <name>` line until `audio` arrives or the deadline.
- `NextPresetsEvent(t *testing.T, deadline time.Duration) []map[string]any` — opens (or reuses) the SSE stream, reads until the next `presets` envelope, returns the parsed `slots` array as `[]map[string]any` (matching the field names in `presetsEnvelope`).
- `drainJSON(t *testing.T, resp *http.Response) map[string]any` — reads the response body, parses JSON, fails the test on error.

The integration env must wire either the real streams adapter (for star/move/persistence tests — needs a writable `data_dir`) or the fake one (for the cast-arg assertion test) as `StreamsCaster`/`StreamsCatalogViewer`/`PresetEditor`/`PresetViewer`. Stub-adapter `SourceAvailabilityViewer` impls are fine — the source-cluster lamp state isn't asserted in these tests, but the chassis snapshot path expects non-nil `SourceAvailabilityViewers` to avoid the default-empty-slice degraded mode.

- [ ] **Step 2: Run to confirm failure**

Run: `go test -tags=integration ./tests/integration -run "TestReceiverStreamsCast_EndToEnd|TestReceiverPresetStar|TestReceiverPresetMove|TestReceiverEvents_PresetsBetweenMeterAndAudio|TestReceiverPresetStar_PersistsAcrossRestart" -v`

Expected: FAIL.

- [ ] **Step 3: Wire main.go**

Open [cmd/mister-groovy-relay/main.go](../../../cmd/mister-groovy-relay/main.go) and find the existing block (around lines 354-363) that probes the streams adapter for `PresetViewer`/`PresetCaster`:

```go
		if c, ok := streamsA.(adapters.PresetCaster); ok {
			presetCaster = c
		}
```

Extend with three new probes, inside the **same** `if streamsA := streamsAdapter; streamsA != nil { ... }` block that already wraps the 3A `presetCaster` probe. After the existing `presetCaster` assertion, declare the new locals at the same scope level as `presetViewer` / `presetCaster` and probe inside the existing block:

```go
	// (existing 3A scope, with presetViewer / presetCaster already declared)
	var streamsCatalogViewer adapters.StreamsCatalogViewer
	var streamsCaster adapters.StreamsCaster
	var presetEditor adapters.PresetEditor
	if streamsA := streamsAdapter; streamsA != nil {
		// ... existing 3A probes (presetViewer, presetCaster) ...
		if v, ok := any(streamsA).(adapters.StreamsCatalogViewer); ok {
			streamsCatalogViewer = v
		}
		if c, ok := any(streamsA).(adapters.StreamsCaster); ok {
			streamsCaster = c
		}
		if ed, ok := any(streamsA).(adapters.PresetEditor); ok {
			presetEditor = ed
		}
	}
```

The `any(streamsA).(...)` form normalizes the assertion against the static type of `streamsAdapter` (a `*streams.Adapter`) so the linter doesn't trip on "impossible type assertion" even though the `*streams.Adapter` does implement these interfaces — the static analyzer cannot always see that through method-set inference when the method set is defined in a separate file. Inspect the existing 3A code in main.go to see whether the prior `presetViewer` / `presetCaster` probes use this form or a direct `streamsA.(adapters.X)` form; match whichever already works in the file.

Then assemble the source-availability viewers slice from the registry:

```go
	var sourceViewers []adapters.SourceAvailabilityViewer
	for _, a := range reg.List() {
		if v, ok := a.(adapters.SourceAvailabilityViewer); ok {
			sourceViewers = append(sourceViewers, v)
		}
	}
```

Place this block immediately before the `chassis.New(chassis.Config{...})` call. Then extend that call with the four new fields:

```go
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:                    sec.Bridge,
		Manager:                   coreMgr,
		Registry:                  reg,
		Version:                   version,
		StartedAt:                 startedAt,
		HostIP:                    hostIP,
		Session:                   coreMgr,
		TransportViewer:           playbackDispatcher,
		TransportController:       playbackDispatcher,
		VisualizerViewer:          coreMgr,
		VisualizerSaver:           &visualizerSaverAdapter{bs: saver},
		VolumeViewer:              coreMgr,
		VolumeSaver:               &volumeSaverAdapter{bs: saver},
		AudioScopeViewer:          coreMgr,
		AUX:                       auxAdapter,
		PresetViewer:              presetViewer,
		PresetCaster:              presetCaster,
		StreamsCatalogViewer:      streamsCatalogViewer,
		StreamsCaster:             streamsCaster,
		PresetEditor:              presetEditor,
		SourceAvailabilityViewers: sourceViewers,
	})
```

- [ ] **Step 4: Run unit tests**

Run: `go vet ./... && go test ./internal/... -count=1`

Expected: green.

- [ ] **Step 5: Run integration tests**

Run: `go test -tags=integration ./tests/integration -count=1 -v`

Expected: all green, including the new 3B tests.

- [ ] **Step 6: Run race detector**

Run: `go test -race ./internal/... -count=1`

Expected: green.

- [ ] **Step 7: Verify the full CI gauntlet locally**

Run:

```bash
go vet ./...
go test ./...
go test -race ./...
go test -tags=integration ./...
```

All four must be green.

- [ ] **Step 8: Manual smoke (optional but recommended)**

- Start the bridge with `./mister-groovy-relay --config /path/to/config.toml` (or the fake-mister loopback variant from CLAUDE.md).
- Open `http://localhost:<http_port>/receiver`.
- Confirm the four source lamps render at idle, with STREAMS amber and others matching their `Configured()` state.
- Click BROWSE → drawer expands, catalog-scan VFD flash plays.
- Click a channel card → STREAMS lamp lights green, the matching preset slot gains `.lit`, the catalog card gains `.tuned`.
- Click a channel's star → preset slot updates (re-renders via `presets` SSE).
- Drag preset slot 1 onto slot 7 → swap; refresh the page → swap persists from `chassis_presets.json`.
- Fill all 12 slots; star a 13th → `BANK FULL` chip surfaces via the preset-header status channel.
- Type in the search field → preset bank + catalog grid dim non-matches; the currently-casting card stays at full opacity.

- [ ] **Step 9: Commit and open PR**

```bash
git add cmd/mister-groovy-relay/main.go tests/integration/chassis_test.go
git commit -m "feat(chassis): wire 3B adapters in main.go and add end-to-end integration tests"
```

Open the PR against `main` with the title `feat(chassis): Phase 3B source cluster + catalog`. Body should list:

- Source-cluster lamps (STREAMS/PLEX/JELLYFIN/DLNA) with three-state derivation from `SourceAvailabilityViewer.Configured()` + `transport.AdapterRef`.
- Catalog drawer with three Streams provider tabs, client-side tab/group switching from pre-rendered HTML `<template>` blocks.
- `POST /receiver/streams/cast` for catalog channel clicks.
- `POST /receiver/preset/star` (idempotent desired-state semantics) + `POST /receiver/preset/move` (swap semantics) for user-curated preset edits.
- Pointer-based drag-reorder + Ctrl+Arrow keyboard shortcut.
- Live client-only search filter against preset bank + catalog grid; `.tuned` exception keeps the active cast visible.
- New `presets` SSE event between `meter` and `audio` in canonical order, diffed on persistent triples only.
- Persistent slot store at `{data_dir}/chassis_presets.json` with atomic temp-file + rename writes.
- All five new JS modules + CSS scoped under `body.receiver`.
- 3A→3B migration: `PresetViewer.BundledPresets()` renamed to `Presets()` (single mechanical pass).

---










