# User Custom Providers — Phase 5: Routes, Editor Interface & Auto-Enable Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make user-authored providers fully manageable from the receiver catalog drawer — create / update / delete / reorder / verify via new `/receiver/catalog/*` chassis routes backed by a new `adapters.UserProviderEditor` interface (implemented by the streams adapter), with live catalog rebuilds that bypass the TOML `ApplyScope` path, preset-slot cleanup on removal, and an auto-enable + hot-start capability that turns Streams on when the operator saves their first provider.

**Architecture:** A new `adapters.UserProviderEditor` interface (shared `internal/adapters` package, consumed by the chassis interface-only, mirroring `PresetEditor`) is implemented directly on `*streams.Adapter`. Each mutation (`Create`/`Update`/`Delete`/`Reorder`) is serialized by a dedicated `a.userEditMu`, validates a candidate user-store snapshot, rebuilds the user-provider catalogs **off-lock** (live yt-dlp enumeration when the resolver is up, cache-only otherwise), persists the candidate JSON atomically, then installs the already-built snapshot under `a.mu` via the existing `installSnapshotLocked` — exactly the "edits are data, not TOML" path the preset star/move actions already use, never `ApplyConfig`. Removed channels clear matching preset slots through three new preset-store methods. `main.go` constructs a single-flight `startAdapter(name)` capability (it owns the process ctx + registry + the streams `catalogManager`) and injects it into the chassis; the create route invokes it when the first provider is saved while Streams is disabled. The Phase-4 "local-only playlists never enumerate" residual is closed inside `Start` by kicking a single background catalog refresh when the periodic loop is gated off. Verify reuses the merged `Resolve`/`EnumeratePlaylist`/`resolveUserDirectURL` primitives and performs resolved-host SSRF validation before any yt-dlp dereference.

**Tech Stack:** Go 1.26, packages `internal/adapters`, `internal/adapters/streams`, `internal/chassis`, `cmd/mister-groovy-relay`. Tests use the existing `fakeResolver`/`newTestAdapterWithCatalog` harness in the streams package and a new `fakeUserProviderEditor` stub in the chassis package; no network. `go test -race` is a CI-only gate (no local cgo); run plain `go test` locally.

---

## Background: what is already merged on `main` (do NOT re-create)

Phases 1–4 are merged. Verified against the current tree:

- **Data model** (`internal/adapters/streams/provider.go`): `ProviderDefinition` has `BadgeColor`, `BadgeLabel` (lines 30-31); `ChannelDefinition` has `Kind` and `Order` (lines 63,67); `GroupDefinition` has `Order` (line 55). `userProviderType = "user"`, `userProviderIDPrefix = "user:"`, `kindPlaylist`/`kindSingle`/`kindDirect`, `maxUserProviders = 32`, `maxChannelsPerProvider = 100` (`provider_user.go:17-34`).
- **Store** (`internal/adapters/streams/user_provider_store.go`): `newUserProviderStore(path)`, `Snapshot() []ProviderDefinition` (deep copy via `cloneUserProvider`), `Put(def) (ProviderDefinition, error)` (assigns locked `user:` ID on empty ID, normalizes, persists atomically; rejects when the 32-provider bank is full), `Delete(id) (bool, error)`. Built in `New()` and stored as `a.userStore` (`adapter.go`).
- **Normalize/validate** (`provider_user.go`): `normalizeUserProvider(def, seen)` (full-replace validation: prefix, glyph 2-4 chars, palette token, channel limit, group/channel ID stability + dedupe, `validateUserProviderHost` per channel URL, `detectChannelKind` when `Kind==""`), `newUserProviderID`, `newChannelID`, `validateBadgeColor`, `detectChannelKind`, `isUserProviderID`. Channel kinds: `kindPlaylist`/`kindSingle`/`kindDirect`.
- **Security** (`url_security.go`): `validateUserProviderHost` (syntactic), `validateUserProviderResolvedHost(ctx, resolver, url)` (DNS-resolved IP recheck, `addr.Unmap()` before classification), `validateUserProviderIP`, `userDirectInputPolicy()`, `resolveUserDirectURL(ctx, doer, resolver, url, maxHops)` (redirect-prevalidating HEAD walk), `newUserRedirectClient()`, `maxUserRedirectHops = 3`, `userDirectProbeTimeout = 10s`. `hostResolver` is the merged DNS seam (`net.DefaultResolver` in prod).
- **Catalog wiring** (Phase 3): `buildUserCatalog(ctx, def, enum)` (`provider_user.go:309`), `buildInlineCatalog(ctx, def, enum)` (`refresh.go:548`), `Catalog()` emits bundled-then-user (`catalog.go`), `buildChassisCatalogProvider(def)` renders user badge/live/ungrouped + `userBadgeClass`, play-time `revalidateResolvedUserURLs` (`playback.go`).
- **Playlist enumeration** (Phase 4): `userPlaylistEnumerator{resolver, cookiesPath, cacheDir, cfg}` with `channelItems(ctx, providerID, channelID, pageURL)` (live-enumerate+cache / cache-only / serve-stale), `EnumeratePlaylist(ctx, pageURL, cookies, maxItems)` on the resolver and `streamResolver` interface, `userPlaylistCacheKey`. `buildStartupSnapshot` uses a **cache-only** enumerator; `refreshOnceDefault`/`refreshCatalogsDefault` use a **live** enumerator (`resolver: a.resolver`).
- **Refresh primitives** (`refresh.go`): `RefreshNow(ctx, providerID)` → `refreshCatalogsDefault(ctx, []string{id}, "manual")`; `refreshCatalogsDefault(ctx, nil, …)` rebuilds **all** direct+user inline catalogs (live enum) and **skips** the remote-fetch branch when `!cfg.AllowRemoteManifest` (the gate at `refresh.go:370` only guards the fetch path, NOT the `buildInlineCatalog` branch at `refresh.go:352`). `installSnapshotLocked(defs, catalogs)` (`adapter.go`).
- **First-run setup** (merged in parallel): `requireSetupComplete` middleware (`firstrun.go:66`) is **opt-in per route** — only cast-initiation routes wrap it. Config-edit routes (`preset/star`, `preset/move`, `settings/catalog/provider/{id}`) are wrapped with `requireSameOrigin` only. New catalog-edit routes follow the config-edit pattern (NOT setup-gated).

### Phase numbering

**Phase 5 = this plan: routes + `UserProviderEditor` interface + auto-enable/hot-start + the local-only refresh residual.** Phase 6 = the authoring UI: `catalog-provider-form.html`, `provider-form.js` (+ `*.behavior.test.js`), the `.ic.u-<token>`/`.badge.u-<token>` palette CSS, and the SSE `catalog`/`providerStatus` **client rendering**. Phase 5 DEFINES the SSE envelope Go types but does not wire the SSE diff-emit loop or client chips.

---

## Scope

**In scope (Phase 5):**
- `adapters.UserProviderEditor` interface (`Create`/`Update`/`Delete`/`Reorder`/`Verify`) + payload/result types in the shared `internal/adapters` package.
- Streams-adapter implementation of that interface (store mutation → off-lock live rebuild → install → preset cleanup → rehydrate).
- User-provider edit serialization + candidate-store commits so concurrent edits cannot install stale catalog snapshots and failed rebuilds do not acknowledge or persist a half-applied edit.
- Preset-store cleanup methods (`ClearProviderSlots`, `ClearChannelSlots`, `Rehydrate`).
- `Start`-level local-only one-shot user-catalog refresh (Phase-4 `AllowRemoteManifest=false` residual).
- `catalogManager.EnsureStreamsEnabled()` + `main.go` guarded `startAdapter`/`ensureStarted` closure + ctx relocation + chassis injection.
- Chassis `Config`/`Server` fields, route handlers, and `Mount` registration for `POST /receiver/catalog/provider`, `PUT/DELETE /receiver/catalog/provider/{id}`, `POST /receiver/catalog/channel/verify`, `POST /receiver/catalog/provider/{id}/reorder`, with same-origin checks applied to every unsafe method, not POST only.
- SSE envelope **Go types** + pure builders (shape only).
- Integration test: add → cast direct → star → cast preset → delete → cleanup.

**Out of scope (Phase 6 — note explicitly):** the authoring form template, `provider-form.js`, the `.ic.u-<token>`/`.badge.u-<token>` palette CSS, and the SSE `catalog`/`providerStatus` **client rendering** (Phase 5 only defines the envelope Go shape and surfaces the auto-enable outcome in the create route's HTTP response body).

---

## Invariants this plan MUST respect (verified against the tree)

- **One HTTP listener.** Routes mount on the existing chassis mux via `Server.Mount` (`server.go:318`). No second listener.
- **Chassis imports `internal/adapters/*` interfaces only**, never the concrete `streams` package — the editor is injected by type-assertion in `main.go` exactly like `PresetEditor` (`main.go:406-408`).
- **`a.mu` is never held across network I/O.** Every rebuild/enumerate/verify builds off-lock; only `installSnapshotLocked` + preset writes run under the lock. This matches `refreshCatalogsDefault` (`refresh.go:351-368`). User-provider edits are serialized with a separate `a.userEditMu`, which may be held across the off-lock rebuild to preserve mutation order; it is NOT `a.mu`.
- **Edits bypass `ApplyScope`/TOML entirely** (spec §10), except the single auto-enable path which persists `[adapters.streams].enabled=true` via the existing `catalogManager` → `ApplyConfigValue` machinery.
- **SSRF posture stays:** authoring-time `validateUserProviderHost` (in `normalizeUserProvider`, via the user store) + play-time resolved-IP recheck; Verify reuses these and also calls `validateUserProviderResolvedHost` before `Resolve`/`EnumeratePlaylist` so yt-dlp never dereferences a hostname that resolves to a blocked address. `addr.Unmap()` remains part of the resolved-IP classifier.
- **Catalog-edit routes are NOT setup-gated** — `requireSameOrigin` only, like `preset/star`/`preset/move`; broaden `requireSameOrigin` in this phase so PUT/DELETE/PATCH are guarded too.
- **`go test -race` is CI-only** (no local cgo). `*.behavior.test.js` run via `node --test`, not `go test`/CI (no new JS in Phase 5).
- **`docs/superpowers/` is gitignored** — commit this plan and any new docs with `git add -f`. Stage ONLY intended paths; verify `git diff --cached --name-only` before each commit.

---

## File Structure

**New**
- `internal/adapters/user_provider.go` — `UserProviderEditor` interface + `UserProviderForm`/`UserGroupForm`/`UserChannelForm`/`UserProviderResult`/`ReorderRequest`/`UserOrderEntry`/`VerifyChannelRequest`/`VerifyChannelResult` types (shared package, like `preset.go`).
- `internal/adapters/streams/user_provider_editor.go` — `*Adapter` methods implementing the interface + form→`ProviderDefinition` translation + off-lock rebuild helper.
- `internal/adapters/streams/user_provider_editor_test.go` — editor unit tests.
- `internal/chassis/catalog_provider.go` — route handlers (`handleCatalogProviderCreate`/`Update`/`Delete`/`Reorder`/`handleCatalogChannelVerify`) + JSON helpers + SSE envelope types/builders.
- `internal/chassis/catalog_provider_test.go` — handler tests with a `fakeUserProviderEditor`.

**Modified**
- `internal/adapters/streams/preset_store.go` — `ClearProviderSlots`, `ClearChannelSlots`, `Rehydrate`.
- `internal/adapters/streams/preset_store_test.go` — cleanup/rehydrate tests.
- `internal/adapters/streams/user_provider_store.go` — candidate planning/commit helpers used by serialized edit transactions.
- `internal/adapters/streams/adapter.go` — `userEditMu` field; `Start` local-only one-shot refresh (residual); `isUserProvidersPresent` helper if needed.
- `internal/adapters/streams/adapter_test.go` — `Start` residual test.
- `cmd/mister-groovy-relay/catalog_manager.go` — `EnsureStreamsEnabled()` method.
- `cmd/mister-groovy-relay/main.go` — add `sync` import; relocate ctx creation; construct guarded `startAdapter`; type-assert `UserProviderEditor`; inject both into `chassis.Config`; use `startAdapter` for the normal startup loop.
- `internal/chassis/server.go` — `Config`/`Server` fields (`UserProviderEditor`, `EnsureAdapterStarted`); route registration in `Mount`.
- `internal/chassis/sameorigin.go` / `sameorigin_test.go` — guard all unsafe mutation methods, not POST only.
- `internal/chassis/server_test.go` (or existing wiring test) — field-wiring assertion.
- `tests/integration/user_provider_test.go` — end-to-end (new).

---

## Task 1: `UserProviderEditor` interface + payload/result types

**Files:**
- Create: `internal/adapters/user_provider.go`
- Test: `internal/adapters/user_provider_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/user_provider_test.go`:

```go
package adapters

import (
	"context"
	"testing"
)

// compile-time proof the result/payload types and the interface are usable.
func TestUserProviderEditor_TypeShape(t *testing.T) {
	t.Parallel()
	form := UserProviderForm{
		DisplayName: "F1 TV",
		BadgeLabel:  "F1",
		BadgeColor:  "amber",
		Groups:      []UserGroupForm{{ID: "g1", Name: "Races", Order: 0}},
		Channels: []UserChannelForm{
			{Name: "Live", URL: "https://cdn.example.com/live.m3u8", Kind: "direct", GroupID: "g1", Order: 0},
		},
	}
	if form.Channels[0].Kind != "direct" {
		t.Fatal("channel kind not set")
	}
	res := UserProviderResult{
		Provider:         CatalogProvider{ID: "user:f1-tv"},
		ClearedSlots:     []int{3, 7},
		AutoEnableNeeded: true,
	}
	if res.Provider.ID != "user:f1-tv" || len(res.ClearedSlots) != 2 || !res.AutoEnableNeeded {
		t.Fatal("result shape wrong")
	}
	vr := VerifyChannelResult{OK: true, Kind: "playlist", ItemCount: 47}
	if !vr.OK || vr.ItemCount != 47 {
		t.Fatal("verify result shape wrong")
	}
	// interface assignability via a local stub (never called).
	var _ UserProviderEditor = stubEditor{}
	_ = context.Background()
}

type stubEditor struct{}

func (stubEditor) CreateUserProvider(context.Context, UserProviderForm) (UserProviderResult, error) {
	return UserProviderResult{}, nil
}
func (stubEditor) UpdateUserProvider(context.Context, string, UserProviderForm) (UserProviderResult, error) {
	return UserProviderResult{}, nil
}
func (stubEditor) DeleteUserProvider(context.Context, string) (UserProviderResult, error) {
	return UserProviderResult{}, nil
}
func (stubEditor) ReorderUserProvider(context.Context, string, ReorderRequest) error { return nil }
func (stubEditor) VerifyChannel(context.Context, VerifyChannelRequest) (VerifyChannelResult, error) {
	return VerifyChannelResult{}, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/ -run TestUserProviderEditor_TypeShape`
Expected: FAIL — `undefined: UserProviderForm` (and the rest).

- [ ] **Step 3: Implement the interface and types**

Create `internal/adapters/user_provider.go`:

```go
package adapters

import "context"

// UserProviderEditor mutates user-authored catalog providers (spec §8).
// The chassis HTTP handler translates the authoring-form POST/PUT/DELETE
// into these calls. Like PresetEditor, the chassis consumes this interface
// only — the concrete implementation lives in the streams adapter and is
// injected by type assertion in main.go.
//
// Create/Update/Delete each persist through the user-provider store and
// rebuild the live catalog (bypassing the TOML ApplyScope path — spec §10),
// returning the saved provider in chassis-shaped form plus any preset slots
// that were cleared by removed channels. Reorder persists only the Order
// fields (no re-enumeration). Verify is a non-persisting dry-run.
type UserProviderEditor interface {
	CreateUserProvider(ctx context.Context, form UserProviderForm) (UserProviderResult, error)
	UpdateUserProvider(ctx context.Context, id string, form UserProviderForm) (UserProviderResult, error)
	DeleteUserProvider(ctx context.Context, id string) (UserProviderResult, error)
	ReorderUserProvider(ctx context.Context, id string, req ReorderRequest) error
	VerifyChannel(ctx context.Context, req VerifyChannelRequest) (VerifyChannelResult, error)
}

// UserProviderForm is the create/update payload from the authoring form.
// On create, ID is empty and the store assigns a locked "user:" slug. On
// update, ID is the locked provider ID; channel rows carry their locked ID
// (empty ID → new channel, server-assigned).
type UserProviderForm struct {
	ID          string
	DisplayName string
	BadgeLabel  string // glyph, 2-4 chars
	BadgeColor  string // palette token (amber|red|teal|blue|purple|green|cyan|slate)
	Groups      []UserGroupForm
	Channels    []UserChannelForm
}

type UserGroupForm struct {
	ID    string
	Name  string
	Order int
}

type UserChannelForm struct {
	ID       string // empty → server-assigned; locked thereafter
	Name     string
	URL      string
	Kind     string // "" → auto-detect; else playlist|single|direct
	PlayMode string // meaningful only for playlist
	GroupID  string
	Order    int
}

// UserProviderResult is the typed return from Create/Update/Delete.
type UserProviderResult struct {
	Provider         CatalogProvider // saved provider, chassis-shaped (zero on Delete)
	ClearedSlots     []int           // preset slots cleared by removed/deleted channels
	AutoEnableNeeded bool            // create-only: first user provider while Streams disabled
}

// ReorderRequest carries new display/sequential order for a provider's
// channels and groups. Touches only Order; never re-enumerates (spec §8).
type ReorderRequest struct {
	Channels []UserOrderEntry
	Groups   []UserOrderEntry
}

type UserOrderEntry struct {
	ID    string
	Order int
}

// VerifyChannelRequest is a dry-run probe of a single channel URL/kind.
type VerifyChannelRequest struct {
	URL  string
	Kind string // "" → auto-detect
}

// VerifyChannelResult is the advisory dry-run outcome (spec §8/§9 item 4-5).
// JSON tags match the chassis wire envelope.
type VerifyChannelResult struct {
	OK        bool   `json:"ok"`
	Kind      string `json:"kind"`
	ItemCount int    `json:"itemCount,omitempty"` // playlist entry count
	IsLive    bool   `json:"isLive,omitempty"`    // single: yt-dlp is_live (derived, never persisted)
	Message   string `json:"message,omitempty"`   // error/redacted reason when OK=false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/ -run TestUserProviderEditor_TypeShape`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/user_provider.go internal/adapters/user_provider_test.go
git commit -m "feat(adapters): UserProviderEditor interface + form/result types"
```

---

## Task 2: Preset-store cleanup + rehydrate

The merged `presetStore.SetStarred(provider, channel, false)` clears slots but first calls `s.resolve(...)` and 404s when the channel is absent from the catalog — useless once a channel is deleted. Delete-with-cleanup needs methods that operate on the persistent `(Provider, Channel)` triple directly. `Rehydrate` refreshes display fields after a catalog rebuild (e.g. a renamed channel) without dropping still-pending references.

**Files:**
- Modify: `internal/adapters/streams/preset_store.go` (append methods after `Move`, ~line 241)
- Test: `internal/adapters/streams/preset_store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/preset_store_test.go`:

```go
func newPresetStoreForCleanup(t *testing.T) *presetStore {
	t.Helper()
	resolve := func(providerID, channelID string) (adapters.PresetEntry, bool) {
		// Resolve any user:* channel to a populated entry; unknown → stale.
		if isUserProviderID(providerID) {
			return adapters.PresetEntry{ProviderID: providerID, ChannelID: channelID, Title: channelID + " title", BadgeLabel: "UX", BadgeClass: "u-teal"}, true
		}
		return adapters.PresetEntry{}, false
	}
	st, err := newPresetStore(filepath.Join(t.TempDir(), "chassis_presets.json"), resolve)
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	return st
}

func TestPresetStore_ClearProviderSlots(t *testing.T) {
	t.Parallel()
	st := newPresetStoreForCleanup(t)
	if _, err := st.SetStarred("user:mix", "a", true); err != nil {
		t.Fatalf("star a: %v", err)
	}
	if _, err := st.SetStarred("user:mix", "b", true); err != nil {
		t.Fatalf("star b: %v", err)
	}
	if _, err := st.SetStarred("user:other", "c", true); err != nil {
		t.Fatalf("star c: %v", err)
	}
	cleared, err := st.ClearProviderSlots("user:mix")
	if err != nil {
		t.Fatalf("ClearProviderSlots: %v", err)
	}
	if len(cleared) != 2 {
		t.Fatalf("cleared = %v, want 2 slots", cleared)
	}
	// user:other survives.
	snap := st.Snapshot()
	count := 0
	for _, e := range snap {
		if e.ProviderID != "" {
			count++
			if e.ProviderID == "user:mix" {
				t.Fatalf("user:mix slot survived: %+v", e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("surviving slots = %d, want 1 (user:other)", count)
	}
}

func TestPresetStore_ClearChannelSlots(t *testing.T) {
	t.Parallel()
	st := newPresetStoreForCleanup(t)
	if _, err := st.SetStarred("user:mix", "keep", true); err != nil {
		t.Fatalf("star keep: %v", err)
	}
	if _, err := st.SetStarred("user:mix", "drop", true); err != nil {
		t.Fatalf("star drop: %v", err)
	}
	cleared, err := st.ClearChannelSlots("user:mix", "drop")
	if err != nil {
		t.Fatalf("ClearChannelSlots: %v", err)
	}
	if len(cleared) != 1 {
		t.Fatalf("cleared = %v, want 1", cleared)
	}
	for _, e := range st.Snapshot() {
		if e.ProviderID == "user:mix" && e.ChannelID == "drop" {
			t.Fatal("dropped channel slot survived")
		}
	}
}

func TestPresetStore_RehydrateRefreshesDisplayFields(t *testing.T) {
	t.Parallel()
	title := "old"
	resolve := func(providerID, channelID string) (adapters.PresetEntry, bool) {
		if providerID != "user:mix" {
			return adapters.PresetEntry{}, false
		}
		return adapters.PresetEntry{ProviderID: providerID, ChannelID: channelID, Title: title, BadgeLabel: "MX", BadgeClass: "u-teal"}, true
	}
	st, err := newPresetStore(filepath.Join(t.TempDir(), "chassis_presets.json"), resolve)
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	if _, err := st.SetStarred("user:mix", "a", true); err != nil {
		t.Fatalf("star: %v", err)
	}
	title = "new" // simulate a rename reflected by the rebuilt catalog
	st.Rehydrate()
	got := ""
	for _, e := range st.Snapshot() {
		if e.ProviderID == "user:mix" && e.ChannelID == "a" {
			got = e.Title
		}
	}
	if got != "new" {
		t.Fatalf("rehydrated title = %q, want new", got)
	}
}
```

`preset_store_test.go` needs `"path/filepath"`, `"testing"`, and the `adapters` import — add any missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestPresetStore_ClearProviderSlots|TestPresetStore_ClearChannelSlots|TestPresetStore_Rehydrate'`
Expected: FAIL — `undefined: (*presetStore).ClearProviderSlots` etc.

- [ ] **Step 3: Implement the methods**

In `internal/adapters/streams/preset_store.go`, append after `Move` (after line 241):

```go
// ClearProviderSlots clears every slot whose ProviderID == providerID,
// persists, and returns the cleared slot numbers (1-indexed, ascending).
// Unlike SetStarred it does NOT consult the resolver, so it works after the
// provider/channel has already left the catalog (delete-with-cleanup, spec
// §9 item 8). A no-match is a no-op success (nil, nil) with no file write.
func (s *presetStore) ClearProviderSlots(providerID string) ([]int, error) {
	return s.clearMatching(func(e adapters.PresetEntry) bool {
		return e.ProviderID == providerID
	})
}

// ClearChannelSlots clears slots matching the full (providerID, channelID)
// pair — channel IDs are provider-scoped (spec §4.5), so a same-named channel
// under a different provider is never touched.
func (s *presetStore) ClearChannelSlots(providerID, channelID string) ([]int, error) {
	return s.clearMatching(func(e adapters.PresetEntry) bool {
		return e.ProviderID == providerID && e.ChannelID == channelID
	})
}

func (s *presetStore) clearMatching(match func(adapters.PresetEntry) bool) ([]int, error) {
	s.mu.Lock()
	next := s.slots
	cleared := []int{}
	for i, e := range next {
		if e.ProviderID != "" && match(e) {
			cleared = append(cleared, i+1)
			next[i] = adapters.PresetEntry{Slot: i + 1}
		}
	}
	if len(cleared) == 0 {
		s.mu.Unlock()
		return nil, nil
	}
	if err := s.persistSlotsLocked(next); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.slots = next
	s.mu.Unlock()
	sort.Ints(cleared)
	return cleared, nil
}

// Rehydrate re-resolves the display fields (Title/BadgeLabel/BadgeClass/Live)
// of every populated slot against the current catalog, so an in-app rename
// reflects in the preset bank without a restart (spec §10 "re-derive
// presets"). It is NON-destructive: a slot whose reference no longer resolves
// (e.g. a playlist channel mid-enumeration) keeps its persistent triple and
// last-known display fields rather than being dropped — explicit removals go
// through ClearProviderSlots/ClearChannelSlots. The persistent triple is
// unchanged, so no file write is needed.
func (s *presetStore) Rehydrate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.slots {
		if e.ProviderID == "" {
			continue
		}
		if fresh, ok := s.resolve(e.ProviderID, e.ChannelID); ok {
			fresh.Slot = e.Slot
			s.slots[i] = fresh
		}
	}
}
```

(`sort` is already imported in `preset_store.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run 'TestPresetStore_'`
Expected: PASS (including the pre-existing preset tests).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/preset_store.go internal/adapters/streams/preset_store_test.go
git commit -m "feat(streams): preset-store ClearProviderSlots/ClearChannelSlots/Rehydrate"
```

---

## Task 3: Off-lock live user-catalog rebuild helper

A single helper rebuilds the merged snapshot (bundled + user) with a chosen enumerator and installs it under the lock — reused by Create/Update (live) and Reorder (cache-only). It mirrors `buildStartupSnapshot` (`refresh.go:499`) but takes the enumerator instead of hard-coding cache-only, and reuses `buildCachedOrSeedSnapshot`.

**Files:**
- Modify: `internal/adapters/streams/refresh.go` (add `buildSnapshotWithEnumerator`; refactor `buildStartupSnapshot` to call it)
- Modify: `internal/adapters/streams/adapter.go` (add `userEditMu sync.Mutex` to `Adapter`)
- Modify: `internal/adapters/streams/user_provider_store.go` (candidate planning/commit helpers)
- Modify: `internal/adapters/streams/user_provider_editor.go` (new file — add the adapter rebuild method here; created in this task, expanded in Tasks 4-8)
- Test: `internal/adapters/streams/user_provider_editor_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/streams/user_provider_editor_test.go`:

```go
package streams

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
)

// installUserProviderForEdit seeds the store with one provider and gives the
// adapter a live resolver so the rebuild path enumerates playlists.
func newEditAdapter(t *testing.T, fr *fakeResolver) *Adapter {
	t.Helper()
	dir := t.TempDir()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.resolver = fr // simulate a started adapter (resolver present)
	a.cacheDir = dir
	return a
}

func TestRebuildUserCatalogsLive_EnumeratesAndInstalls(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	a := newEditAdapter(t, fr)
	if _, err := a.userStore.Put(ProviderDefinition{
		Type: userProviderType, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []ChannelDefinition{
			{Name: "List", URL: playlistURL, Kind: kindPlaylist},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := a.rebuildUserCatalogsLive(context.Background()); err != nil {
		t.Fatalf("rebuildUserCatalogsLive: %v", err)
	}
	// The user provider is now installed with its playlist enumerated.
	a.mu.Lock()
	cat, ok := a.catalogs["user:mix"]
	a.mu.Unlock()
	if !ok {
		t.Fatal("user:mix catalog not installed")
	}
	if len(cat.Channels) != 1 || len(cat.Channels[0].Items) != 2 {
		t.Fatalf("playlist not enumerated: %+v", cat.Channels)
	}
	if fr.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1", fr.enumCalls)
	}
}
```

`user_provider_editor_test.go` needs `"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"` — add it. (`ytEntries` is defined in `playlist_enum_test.go`, same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestRebuildUserCatalogsLive`
Expected: FAIL — `a.rebuildUserCatalogsLive undefined`.

- [ ] **Step 3: Refactor the snapshot builder and add the rebuild helper**

In `internal/adapters/streams/refresh.go`, replace `buildStartupSnapshot` (lines 499-509) with a thin wrapper over a new enumerator-parameterized builder:

```go
func buildStartupSnapshot(ctx context.Context, cfg Config, cacheDir string, userProviders []ProviderDefinition) ([]ProviderDefinition, []ProviderCatalog, error) {
	// resolver nil → cache-only, non-blocking. Start() calls this before
	// a.resolver is assigned, so we must never block on a yt-dlp subprocess at
	// startup; live enumeration is the refresh loop's (and the edit path's) job.
	return buildSnapshotWithEnumerator(ctx, cfg, cacheDir, userProviders, userPlaylistEnumerator{cacheDir: cacheDir, cfg: cfg})
}

// buildSnapshotWithEnumerator merges bundled+cached+user providers and builds
// their catalogs using the supplied enumerator. A cache-only enumerator
// (resolver nil) serves cached playlist items without yt-dlp; a live
// enumerator (resolver set) enumerates fresh. Pure/off-lock: callers install
// the result under a.mu.
func buildSnapshotWithEnumerator(ctx context.Context, cfg Config, cacheDir string, userProviders []ProviderDefinition, enum userPlaylistEnumerator) ([]ProviderDefinition, []ProviderCatalog, error) {
	cached := loadCachedManifest(ctx, cfg, cacheDir)
	bundled := sanitizeManifestArtwork(ctx, bundledManifest(), cfg, validateProviderArtworkURLSyntax)
	manifest := mergeManifests(cfg, bundled, cached, nil, remoteProviderFactories())
	manifest.Providers = appendUserProviders(manifest.Providers, cfg, userProviders)
	return buildCachedOrSeedSnapshot(ctx, manifest.Providers, cfg, cacheDir, enum)
}
```

In `internal/adapters/streams/adapter.go`, add the edit serializer near `playbackMu`:

```go
	// userEditMu serializes user-provider create/update/delete/reorder across
	// candidate planning, off-lock rebuild, atomic JSON commit, catalog install,
	// and preset cleanup. It is intentionally separate from a.mu so catalog
	// enumeration never happens while the adapter state lock is held.
	userEditMu sync.Mutex
```

In `internal/adapters/streams/user_provider_store.go`, add candidate helpers after `Delete`. These keep validation/dedupe centralized in the store but let editor methods build the live catalog from a candidate before committing it to disk:

```go
func (s *userProviderStore) PlanPut(def ProviderDefinition) (ProviderDefinition, []ProviderDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, next, err := s.planPutLocked(def)
	if err != nil {
		return ProviderDefinition{}, nil, err
	}
	return saved, cloneUserProviders(next), nil
}

func (s *userProviderStore) PlanDelete(id string) ([]ProviderDefinition, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.providers {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, false
	}
	next := append([]ProviderDefinition(nil), s.providers[:idx]...)
	next = append(next, s.providers[idx+1:]...)
	return cloneUserProviders(next), true
}

func (s *userProviderStore) CommitProviders(providers []ProviderDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneUserProviders(providers)
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.providers = next
	return nil
}

func (s *userProviderStore) Exists(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (s *userProviderStore) planPutLocked(def ProviderDefinition) (ProviderDefinition, []ProviderDefinition, error) {
	idx := -1
	if isUserProviderID(def.ID) {
		for i, p := range s.providers {
			if p.ID == def.ID {
				idx = i
				break
			}
		}
	}
	if def.ID != "" {
		if err := validateUserProviderID(def.ID); err != nil {
			return ProviderDefinition{}, nil, err
		}
		if idx < 0 {
			return ProviderDefinition{}, nil, notFoundUserProvider(def.ID)
		}
	}
	if idx < 0 && len(s.providers) >= maxUserProviders {
		return ProviderDefinition{}, nil, badRequest("BANK FULL",
			fmt.Sprintf("streams: at most %d user providers", maxUserProviders))
	}
	taken := func(id string) bool {
		for i, p := range s.providers {
			if i == idx {
				continue
			}
			if p.ID == id {
				return true
			}
		}
		return false
	}
	if def.ID == "" {
		def.ID = newUserProviderID(def.DisplayName, taken)
	}
	norm, err := normalizeUserProvider(def, nil)
	if err != nil {
		return ProviderDefinition{}, nil, err
	}
	next := cloneUserProviders(s.providers)
	if idx >= 0 {
		next[idx] = norm
	} else {
		next = append(next, norm)
	}
	return norm, next, nil
}

func cloneUserProviders(in []ProviderDefinition) []ProviderDefinition {
	out := make([]ProviderDefinition, len(in))
	for i := range in {
		out[i] = cloneUserProvider(in[i])
	}
	return out
}

func notFoundUserProvider(id string) error {
	return &adapters.QuickCastError{
		Status:  http.StatusNotFound,
		Chip:    "NOT FOUND",
		Message: fmt.Sprintf("streams: user provider %q not found", id),
	}
}
```

Replace the existing `Put` body with:

```go
func (s *userProviderStore) Put(def ProviderDefinition) (ProviderDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	norm, next, err := s.planPutLocked(def)
	if err != nil {
		return ProviderDefinition{}, err
	}
	if err := s.persistLocked(next); err != nil {
		return ProviderDefinition{}, err
	}
	s.providers = next
	return norm, nil
}
```

Keep the existing `Put` tests green; editor methods use the new candidate helpers.

Create `internal/adapters/streams/user_provider_editor.go` with the rebuild helpers (interface methods are added in Tasks 4-8):

```go
package streams

import (
	"context"
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// liveEnumerator builds the live (yt-dlp-backed) playlist enumerator from the
// adapter's resolver. a.resolver and a.cookiesPath are set once at Start/New
// time and never mutated, matching the off-lock read pattern in
// refreshCatalogsDefault (refresh.go:346-350).
func (a *Adapter) liveEnumerator(cfg Config) userPlaylistEnumerator {
	return userPlaylistEnumerator{resolver: a.resolver, cookiesPath: a.cookiesPath, cacheDir: a.cacheDir, cfg: cfg}
}

type userCatalogSnapshot struct {
	definitions []ProviderDefinition
	catalogs    []ProviderCatalog
}

// buildUserCatalogSnapshotLive rebuilds the full merged snapshot with LIVE
// playlist enumeration from an explicit user-provider candidate. It is pure
// with respect to adapter state: no store write and no a.mu install.
func (a *Adapter) buildUserCatalogSnapshotLive(ctx context.Context, userProviders []ProviderDefinition) (userCatalogSnapshot, error) {
	cfg := a.configSnapshot()
	defs, catalogs, err := buildSnapshotWithEnumerator(ctx, cfg, a.cacheDir, userProviders, a.liveEnumerator(cfg))
	if err != nil {
		return userCatalogSnapshot{}, fmt.Errorf("streams: build user catalogs: %w", err)
	}
	return userCatalogSnapshot{definitions: defs, catalogs: catalogs}, nil
}

func (a *Adapter) buildUserCatalogSnapshotCacheOnly(ctx context.Context, userProviders []ProviderDefinition) (userCatalogSnapshot, error) {
	cfg := a.configSnapshot()
	defs, catalogs, err := buildSnapshotWithEnumerator(ctx, cfg, a.cacheDir, userProviders, userPlaylistEnumerator{cacheDir: a.cacheDir, cfg: cfg})
	if err != nil {
		return userCatalogSnapshot{}, fmt.Errorf("streams: build user catalogs (cache-only): %w", err)
	}
	return userCatalogSnapshot{definitions: defs, catalogs: catalogs}, nil
}

func (a *Adapter) installUserCatalogSnapshot(snapshot userCatalogSnapshot) {
	a.mu.Lock()
	a.installSnapshotLocked(snapshot.definitions, snapshot.catalogs)
	if a.state != adapters.StateStopped {
		a.state = adapters.StateRunning
	}
	a.mu.Unlock()
}

// rebuildUserCatalogsLive preserves the existing refresh-like convenience path
// for non-mutating callers. Mutating editor methods use the explicit
// build→commit→install sequence above so failed rebuilds never persist an edit.
func (a *Adapter) rebuildUserCatalogsLive(ctx context.Context) error {
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, a.userStore.Snapshot())
	if err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	return nil
}

// rebuildUserCatalogsCacheOnly rebuilds + installs WITHOUT re-enumerating
// playlists (reuses cached items). Used by Reorder, which only re-sorts by
// Order (spec §8 "Reorder ... does not re-enumerate").
func (a *Adapter) rebuildUserCatalogsCacheOnly(ctx context.Context) error {
	snapshot, err := a.buildUserCatalogSnapshotCacheOnly(ctx, a.userStore.Snapshot())
	if err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestRebuildUserCatalogsLive`
Then full package: `go test ./internal/adapters/streams/`
Expected: PASS (the `buildStartupSnapshot` refactor is signature-preserving, so existing snapshot tests stay green).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/refresh.go internal/adapters/streams/user_provider_editor.go internal/adapters/streams/user_provider_editor_test.go
git commit -m "feat(streams): off-lock live/cache-only user-catalog rebuild helpers"
```

---

## Task 4: `CreateUserProvider`

Translate the form → `ProviderDefinition`, validate/assign IDs via `userStore.PlanPut`, rebuild live from the candidate, atomically commit the candidate, install the rebuilt snapshot, and report whether this is the first provider while Streams is disabled (so the route can auto-enable).

**Files:**
- Modify: `internal/adapters/streams/user_provider_editor.go`
- Test: `internal/adapters/streams/user_provider_editor_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/user_provider_editor_test.go`:

```go
func sampleForm() adapters.UserProviderForm {
	return adapters.UserProviderForm{
		DisplayName: "F1 TV",
		BadgeLabel:  "F1",
		BadgeColor:  "amber",
		Channels: []adapters.UserChannelForm{
			{Name: "Live", URL: "https://cdn.example.com/live.m3u8"}, // kind auto-detect → direct
		},
	}
}

func TestCreateUserProvider_PersistsRebuildsAndFlagsAutoEnable(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	// Streams disabled by default (DefaultConfig Enabled=false).
	res, err := a.CreateUserProvider(context.Background(), sampleForm())
	if err != nil {
		t.Fatalf("CreateUserProvider: %v", err)
	}
	if res.Provider.ID != "user:f1-tv" {
		t.Fatalf("provider ID = %q, want user:f1-tv", res.Provider.ID)
	}
	if res.Provider.BadgeLabel != "F1" || res.Provider.BadgeClass != "u-amber" {
		t.Fatalf("badge = (%q,%q), want (F1,u-amber)", res.Provider.BadgeLabel, res.Provider.BadgeClass)
	}
	if !res.AutoEnableNeeded {
		t.Fatal("AutoEnableNeeded = false, want true (first provider while disabled)")
	}
	// Persisted to the store and installed in the live catalog.
	if got := a.userStore.Snapshot(); len(got) != 1 || got[0].ID != "user:f1-tv" {
		t.Fatalf("store snapshot = %+v", got)
	}
	a.mu.Lock()
	_, ok := a.catalogs["user:f1-tv"]
	a.mu.Unlock()
	if !ok {
		t.Fatal("user:f1-tv catalog not installed after create")
	}
}

func TestCreateUserProvider_SecondProviderNoAutoEnable(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	if _, err := a.CreateUserProvider(context.Background(), sampleForm()); err != nil {
		t.Fatalf("first create: %v", err)
	}
	f2 := sampleForm()
	f2.DisplayName = "Cartoon"
	res, err := a.CreateUserProvider(context.Background(), f2)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if res.AutoEnableNeeded {
		t.Fatal("AutoEnableNeeded = true on second provider, want false")
	}
}

func TestCreateUserProvider_ConcurrentCreatesOnlyOneAutoEnable(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	forms := []adapters.UserProviderForm{sampleForm(), sampleForm()}
	forms[1].DisplayName = "Cartoon"
	forms[1].BadgeLabel = "CN"
	results := make(chan adapters.UserProviderResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, form := range forms {
		form := form
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := a.CreateUserProvider(context.Background(), form)
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("CreateUserProvider error: %v", err)
	}
	autoEnabled := 0
	for res := range results {
		if res.AutoEnableNeeded {
			autoEnabled++
		}
	}
	if autoEnabled != 1 {
		t.Fatalf("AutoEnableNeeded count = %d, want 1", autoEnabled)
	}
	if got := a.userStore.Snapshot(); len(got) != 2 {
		t.Fatalf("store snapshot len = %d, want 2: %+v", len(got), got)
	}
}

func TestCreateUserProvider_InvalidBadgeColorIsClientError(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	f := sampleForm()
	f.BadgeColor = "chartreuse" // not in the palette
	_, err := a.CreateUserProvider(context.Background(), f)
	if err == nil {
		t.Fatal("err = nil, want a client validation error")
	}
	var qerr *adapters.QuickCastError
	if !errorsAs(t, err, &qerr) || qerr.Status != http.StatusBadRequest {
		t.Fatalf("err = %v, want *QuickCastError{400}", err)
	}
}
```

Add a tiny local helper at the bottom of the test file (keeps the assertion terse) and the imports `"errors"`, `"net/http"`, `"sync"`:

```go
func errorsAs(t *testing.T, err error, target any) bool {
	t.Helper()
	return errors.As(err, target)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestCreateUserProvider`
Expected: FAIL — `a.CreateUserProvider undefined`.

- [ ] **Step 3: Implement `CreateUserProvider` + form translation + error mapping**

Append to `internal/adapters/streams/user_provider_editor.go` (add `"errors"` and `"net/http"` to the import block):

```go
// formToDefinition maps the chassis authoring form to a ProviderDefinition.
// The locked ID (provider + channel) is preserved verbatim; normalization,
// kind auto-detection, slug assignment, and validation all happen inside
// userStore.PlanPut → normalizeUserProvider (spec §4.5/§4.7).
func formToDefinition(id string, form adapters.UserProviderForm) ProviderDefinition {
	groups := make([]GroupDefinition, 0, len(form.Groups))
	for _, g := range form.Groups {
		groups = append(groups, GroupDefinition{ID: g.ID, Name: g.Name, Order: g.Order})
	}
	channels := make([]ChannelDefinition, 0, len(form.Channels))
	for _, c := range form.Channels {
		channels = append(channels, ChannelDefinition{
			ID:       c.ID,
			Name:     c.Name,
			GroupID:  c.GroupID,
			Kind:     c.Kind, // "" → detectChannelKind in normalize
			URL:      c.URL,
			PlayMode: PlayMode(c.PlayMode),
			Order:    c.Order,
		})
	}
	return ProviderDefinition{
		ID:          id,
		Type:        userProviderType,
		DisplayName: form.DisplayName,
		BadgeLabel:  form.BadgeLabel,
		BadgeColor:  form.BadgeColor,
		Groups:      groups,
		Channels:    channels,
	}
}

// userInputError wraps validation/normalization failures as client-facing
// 400s so the chassis renders them inline. Store persistence failures are
// internal errors and MUST bubble as 500s.
func userInputError(err error) error {
	if err == nil {
		return nil
	}
	var qerr *adapters.QuickCastError
	if errors.As(err, &qerr) {
		return err
	}
	return &adapters.QuickCastError{Status: http.StatusBadRequest, Chip: "BAD INPUT", Message: err.Error()}
}

// CreateUserProvider persists a new user provider and rebuilds the live
// catalog. AutoEnableNeeded is true when this is the FIRST user provider and
// Streams is currently disabled — the chassis then invokes the injected
// EnsureStarted capability (spec §10).
func (a *Adapter) CreateUserProvider(ctx context.Context, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	if a.userStore == nil {
		return adapters.UserProviderResult{}, fmt.Errorf("streams: user provider store not initialized")
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()

	saved, candidate, err := a.userStore.PlanPut(formToDefinition("", form))
	if err != nil {
		return adapters.UserProviderResult{}, userInputError(err)
	}
	firstProvider := len(candidate) == 1
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, candidate)
	if err != nil {
		return adapters.UserProviderResult{}, err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return adapters.UserProviderResult{}, err
	}
	a.installUserCatalogSnapshot(snapshot)
	a.presetStore.Rehydrate()
	return adapters.UserProviderResult{
		Provider:         buildChassisCatalogProvider(saved),
		AutoEnableNeeded: firstProvider && !a.IsEnabled(),
	}, nil
}
```

> Note: `userStore.PlanPut` with an empty `def.ID` slugifies the display name and assigns the locked `user:` ID (`provider_user.go:185-188` via the store). The test's `"F1 TV"` → `user:f1-tv` (slugify lowercases, collapses the space to a hyphen).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestCreateUserProvider`
Expected: PASS (all three sub-tests).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/user_provider_editor.go internal/adapters/streams/user_provider_editor_test.go
git commit -m "feat(streams): CreateUserProvider (persist + live rebuild + auto-enable flag)"
```

---

## Task 5: `UpdateUserProvider` (full replace + removed-channel preset cleanup)

Update is a full replace preserving locked IDs (`userStore.PlanPut` handles the ID-stability rules). Channels present in the old definition but absent from the update must clear their preset slots; the result reports the cleared slots.

**Files:**
- Modify: `internal/adapters/streams/user_provider_editor.go`
- Test: `internal/adapters/streams/user_provider_editor_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/user_provider_editor_test.go`:

```go
func TestUpdateUserProvider_RemovedChannelClearsPresetSlots(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	// Create a provider with two direct channels.
	form := adapters.UserProviderForm{
		DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{ID: "", Name: "Keep", URL: "https://cdn.example.com/keep.m3u8"},
			{ID: "", Name: "Drop", URL: "https://cdn.example.com/drop.m3u8"},
		},
	}
	created, err := a.CreateUserProvider(context.Background(), form)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Star both channels into the preset bank.
	for _, g := range created.Provider.Groups {
		for _, ch := range g.Channels {
			if _, err := a.SetPresetStarred(context.Background(), created.Provider.ID, ch.ID, true); err != nil {
				t.Fatalf("star %s: %v", ch.ID, err)
			}
		}
	}
	// Update: drop the "drop" channel (re-send only "keep", with its locked ID).
	keepID, dropID := channelIDsByName(t, created.Provider)
	upd := adapters.UserProviderForm{
		ID: created.Provider.ID, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{ID: keepID, Name: "Keep", URL: "https://cdn.example.com/keep.m3u8"},
		},
	}
	res, err := a.UpdateUserProvider(context.Background(), created.Provider.ID, upd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(res.ClearedSlots) != 1 {
		t.Fatalf("ClearedSlots = %v, want 1 (the dropped channel)", res.ClearedSlots)
	}
	// The dropped channel's slot is gone; the kept channel's slot remains.
	for _, e := range a.presetStore.Snapshot() {
		if e.ChannelID == dropID {
			t.Fatal("dropped channel still starred")
		}
	}
}

func TestUpdateUserProvider_UnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	_, err := a.UpdateUserProvider(context.Background(), "user:ghost", sampleForm())
	var qerr *adapters.QuickCastError
	if !errorsAs(t, err, &qerr) || qerr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want *QuickCastError{404}", err)
	}
}

// channelIDsByName returns the (keep, drop) channel IDs from a chassis-shaped
// provider whose channels are named "Keep"/"Drop".
func channelIDsByName(t *testing.T, p adapters.CatalogProvider) (keep, drop string) {
	t.Helper()
	for _, g := range p.Groups {
		for _, ch := range g.Channels {
			switch ch.Name {
			case "Keep":
				keep = ch.ID
			case "Drop":
				drop = ch.ID
			}
		}
	}
	if keep == "" || drop == "" {
		t.Fatalf("could not find keep/drop channel IDs in %+v", p.Groups)
	}
	return keep, drop
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestUpdateUserProvider`
Expected: FAIL — `a.UpdateUserProvider undefined`.

- [ ] **Step 3: Implement `UpdateUserProvider`**

Append to `internal/adapters/streams/user_provider_editor.go`:

```go
// UpdateUserProvider replaces a user provider's definition (preserving locked
// provider/channel IDs via userStore.PlanPut) and clears preset slots for any
// channel that was removed in the update (spec §4.5: "Previously-known channel
// ID absent from the submitted provider → delete that channel and clear any
// preset slots that reference (providerID, channelID)").
func (a *Adapter) UpdateUserProvider(ctx context.Context, id string, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	if a.userStore == nil {
		return adapters.UserProviderResult{}, fmt.Errorf("streams: user provider store not initialized")
	}
	if !isUserProviderID(id) {
		return adapters.UserProviderResult{}, userInputError(fmt.Errorf("provider id %q is not a user provider", id))
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()
	if !a.userStore.Exists(id) {
		return adapters.UserProviderResult{}, notFoundUserProvider(id)
	}
	prevChannelIDs := a.userChannelIDs(id)

	saved, candidate, err := a.userStore.PlanPut(formToDefinition(id, form))
	if err != nil {
		return adapters.UserProviderResult{}, userInputError(err)
	}
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, candidate)
	if err != nil {
		return adapters.UserProviderResult{}, err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return adapters.UserProviderResult{}, err
	}
	a.installUserCatalogSnapshot(snapshot)

	// Clear presets for channels that existed before but are gone now.
	nextIDs := map[string]bool{}
	for _, ch := range saved.Channels {
		nextIDs[ch.ID] = true
	}
	var cleared []int
	for chID := range prevChannelIDs {
		if !nextIDs[chID] {
			slots, err := a.presetStore.ClearChannelSlots(id, chID)
			if err != nil {
				return adapters.UserProviderResult{}, err
			}
			cleared = append(cleared, slots...)
		}
	}
	a.presetStore.Rehydrate()
	sortInts(cleared)
	return adapters.UserProviderResult{
		Provider:     buildChassisCatalogProvider(saved),
		ClearedSlots: cleared,
	}, nil
}

// userChannelIDs returns the set of channel IDs for a stored user provider
// (empty set if absent). Read off the store snapshot — no lock on a.mu.
func (a *Adapter) userChannelIDs(id string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, def := range a.userStore.Snapshot() {
		if def.ID != id {
			continue
		}
		for _, ch := range def.Channels {
			out[ch.ID] = struct{}{}
		}
	}
	return out
}
```

Add the small int-sort helper at the bottom of `user_provider_editor.go` (add `"sort"` to imports):

```go
func sortInts(xs []int) {
	sort.Ints(xs)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestUpdateUserProvider`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/user_provider_editor.go internal/adapters/streams/user_provider_editor_test.go
git commit -m "feat(streams): UpdateUserProvider (full replace + removed-channel preset cleanup)"
```

---

## Task 6: `DeleteUserProvider` (+ provider preset cleanup)

**Files:**
- Modify: `internal/adapters/streams/user_provider_editor.go`
- Test: `internal/adapters/streams/user_provider_editor_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/user_provider_editor_test.go`:

```go
func TestDeleteUserProvider_ClearsAllItsPresetSlots(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	created, err := a.CreateUserProvider(context.Background(), adapters.UserProviderForm{
		DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{Name: "A", URL: "https://cdn.example.com/a.m3u8"},
			{Name: "B", URL: "https://cdn.example.com/b.m3u8"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, g := range created.Provider.Groups {
		for _, ch := range g.Channels {
			if _, err := a.SetPresetStarred(context.Background(), created.Provider.ID, ch.ID, true); err != nil {
				t.Fatalf("star: %v", err)
			}
		}
	}
	res, err := a.DeleteUserProvider(context.Background(), created.Provider.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(res.ClearedSlots) != 2 {
		t.Fatalf("ClearedSlots = %v, want 2", res.ClearedSlots)
	}
	if got := a.userStore.Snapshot(); len(got) != 0 {
		t.Fatalf("store snapshot = %+v, want empty", got)
	}
	a.mu.Lock()
	_, ok := a.catalogs[created.Provider.ID]
	a.mu.Unlock()
	if ok {
		t.Fatal("catalog still present after delete")
	}
}

func TestDeleteUserProvider_UnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	_, err := a.DeleteUserProvider(context.Background(), "user:ghost")
	var qerr *adapters.QuickCastError
	if !errorsAs(t, err, &qerr) || qerr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want *QuickCastError{404}", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestDeleteUserProvider`
Expected: FAIL — `a.DeleteUserProvider undefined`.

- [ ] **Step 3: Implement `DeleteUserProvider`**

Append to `internal/adapters/streams/user_provider_editor.go`:

```go
// DeleteUserProvider removes a user provider, rebuilds the catalog (the
// provider disappears), and clears every preset slot referencing the
// provider's channels (spec §4.5/§9 item 8).
func (a *Adapter) DeleteUserProvider(ctx context.Context, id string) (adapters.UserProviderResult, error) {
	if a.userStore == nil {
		return adapters.UserProviderResult{}, fmt.Errorf("streams: user provider store not initialized")
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()
	candidate, ok := a.userStore.PlanDelete(id)
	if !ok {
		return adapters.UserProviderResult{}, notFoundUserProvider(id)
	}
	// Rebuild WITHOUT the deleted provider, then clear its preset slots by
	// provider ID (the channels are gone from the catalog, so the resolver-gated
	// SetStarred path cannot do this — that is why ClearProviderSlots exists).
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, candidate)
	if err != nil {
		return adapters.UserProviderResult{}, err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return adapters.UserProviderResult{}, err
	}
	a.installUserCatalogSnapshot(snapshot)
	cleared, err := a.presetStore.ClearProviderSlots(id)
	if err != nil {
		return adapters.UserProviderResult{}, err
	}
	a.presetStore.Rehydrate()
	return adapters.UserProviderResult{ClearedSlots: cleared}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestDeleteUserProvider`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/user_provider_editor.go internal/adapters/streams/user_provider_editor_test.go
git commit -m "feat(streams): DeleteUserProvider (+ preset-slot cleanup count)"
```

---

## Task 7: `ReorderUserProvider` (Order-only, no re-enumeration)

Reorder applies new `Order` values to the stored provider's channels/groups, persists via `Put`, and rebuilds **cache-only** so cached playlist items are reused and only the sort changes (spec §8 "Reorder ... does not re-enumerate").

**Files:**
- Modify: `internal/adapters/streams/user_provider_editor.go`
- Test: `internal/adapters/streams/user_provider_editor_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/user_provider_editor_test.go`:

```go
func TestReorderUserProvider_PersistsOrderWithoutEnumerating(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("dQw4w9WgXcQ"),
	}}
	a := newEditAdapter(t, fr)
	created, err := a.CreateUserProvider(context.Background(), adapters.UserProviderForm{
		DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []adapters.UserChannelForm{
			{Name: "First", URL: "https://cdn.example.com/a.m3u8", Order: 0},
			{Name: "Listy", URL: playlistURL, Order: 1},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	enumAfterCreate := fr.enumCalls
	keep, _ := firstTwoChannelIDs(t, created.Provider)

	if err := a.ReorderUserProvider(context.Background(), created.Provider.ID, adapters.ReorderRequest{
		Channels: []adapters.UserOrderEntry{{ID: keep, Order: 5}},
	}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if fr.enumCalls != enumAfterCreate {
		t.Fatalf("reorder triggered enumeration (calls %d → %d); it must reuse cache", enumAfterCreate, fr.enumCalls)
	}
	// Persisted Order survived.
	for _, def := range a.userStore.Snapshot() {
		if def.ID != created.Provider.ID {
			continue
		}
		for _, ch := range def.Channels {
			if ch.ID == keep && ch.Order != 5 {
				t.Fatalf("channel %q Order = %d, want 5", keep, ch.Order)
			}
		}
	}
}

func firstTwoChannelIDs(t *testing.T, p adapters.CatalogProvider) (string, string) {
	t.Helper()
	var ids []string
	for _, g := range p.Groups {
		for _, ch := range g.Channels {
			ids = append(ids, ch.ID)
		}
	}
	if len(ids) < 2 {
		t.Fatalf("want >=2 channels, got %v", ids)
	}
	return ids[0], ids[1]
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestReorderUserProvider`
Expected: FAIL — `a.ReorderUserProvider undefined`.

- [ ] **Step 3: Implement `ReorderUserProvider`**

Append to `internal/adapters/streams/user_provider_editor.go`:

```go
// ReorderUserProvider applies new Order values to a stored provider's channels
// and groups, then rebuilds CACHE-ONLY so playlist items are reused (no
// yt-dlp). Unknown IDs in the request are ignored (defensive: the browser may
// be stale). Order-only edits never affect preset references.
func (a *Adapter) ReorderUserProvider(ctx context.Context, id string, req adapters.ReorderRequest) error {
	if a.userStore == nil {
		return fmt.Errorf("streams: user provider store not initialized")
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()
	var target ProviderDefinition
	found := false
	for _, def := range a.userStore.Snapshot() {
		if def.ID == id {
			target = def
			found = true
			break
		}
	}
	if !found {
		return notFoundUserProvider(id)
	}
	chOrder := map[string]int{}
	for _, e := range req.Channels {
		chOrder[e.ID] = e.Order
	}
	grOrder := map[string]int{}
	for _, e := range req.Groups {
		grOrder[e.ID] = e.Order
	}
	for i := range target.Channels {
		if o, ok := chOrder[target.Channels[i].ID]; ok {
			target.Channels[i].Order = o
		}
	}
	for i := range target.Groups {
		if o, ok := grOrder[target.Groups[i].ID]; ok {
			target.Groups[i].Order = o
		}
	}
	_, candidate, err := a.userStore.PlanPut(target)
	if err != nil {
		return userInputError(err)
	}
	snapshot, err := a.buildUserCatalogSnapshotCacheOnly(ctx, candidate)
	if err != nil {
		return err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	return nil
}
```

> Note: `userStore.PlanPut` with the existing (non-empty) locked ID updates in place and re-normalizes; the channels already carry their locked IDs, so no slug reassignment happens.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestReorderUserProvider`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/user_provider_editor.go internal/adapters/streams/user_provider_editor_test.go
git commit -m "feat(streams): ReorderUserProvider (Order-only, cache-only rebuild)"
```

---

## Task 8: `VerifyChannel` (dry-run probe)

Verify is advisory and non-persisting (spec §8/§9 item 4-5). It auto-detects the kind when unset, then probes:
- `direct` → `resolveUserDirectURL` (the merged redirect-prevalidating HEAD walk) reachability + SSRF.
- `playlist` → `EnumeratePlaylist` count.
- `single` → `Resolve` (yields `IsLive` for the LIVE/VIDEO chip).

**Files:**
- Modify: `internal/adapters/streams/user_provider_editor.go`
- Test: `internal/adapters/streams/user_provider_editor_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/user_provider_editor_test.go`:

```go
func TestVerifyChannel_PlaylistReturnsCount(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("a", "b", "c"),
	}}
	a := newEditAdapter(t, fr)
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{URL: playlistURL})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Kind != kindPlaylist || res.ItemCount != 3 {
		t.Fatalf("verify result = %+v, want ok playlist count=3", res)
	}
}

func TestVerifyChannel_SingleSurfacesIsLive(t *testing.T) {
	t.Parallel()
	fr := &fakeResolver{res: &ytdlp.Resolution{URL: "https://edge/live.m3u8", IsLive: true, Title: "Stream"}}
	a := newEditAdapter(t, fr)
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{URL: "https://twitch.tv/foo"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Kind != kindSingle || !res.IsLive {
		t.Fatalf("verify result = %+v, want ok single isLive=true", res)
	}
}

func TestVerifyChannel_RejectsBlockedHost(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	// Syntactic gate rejects loopback before any network call.
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{URL: "http://127.0.0.1/stream.m3u8"})
	if err != nil {
		t.Fatalf("verify returned error (should be a soft not-OK result): %v", err)
	}
	if res.OK {
		t.Fatalf("verify OK for loopback host, want OK=false: %+v", res)
	}
}

func TestVerifyChannel_PlaylistRejectsResolvedBlockedHostBeforeYTDLP(t *testing.T) {
	t.Parallel()
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		"https://evil.example/playlist": ytEntries("a"),
	}}
	a := newEditAdapter(t, fr)
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"evil.example": {"127.0.0.1"}}}
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{
		URL:  "https://evil.example/playlist",
		Kind: kindPlaylist,
	})
	if err != nil {
		t.Fatalf("verify returned error (should be a soft not-OK result): %v", err)
	}
	if res.OK {
		t.Fatalf("verify OK for DNS-to-loopback host, want OK=false: %+v", res)
	}
	if fr.enumCalls != 0 {
		t.Fatalf("enumCalls = %d, want 0 (blocked before yt-dlp)", fr.enumCalls)
	}
}

func TestVerifyChannel_InvalidKindIsSoftFailure(t *testing.T) {
	t.Parallel()
	a := newEditAdapter(t, &fakeResolver{})
	res, err := a.VerifyChannel(context.Background(), adapters.VerifyChannelRequest{
		URL:  "https://cdn.example.com/live.m3u8",
		Kind: "weird",
	})
	if err != nil {
		t.Fatalf("verify returned error (should be a soft not-OK result): %v", err)
	}
	if res.OK {
		t.Fatalf("verify OK for invalid kind, want OK=false: %+v", res)
	}
}
```

> The `fakeResolver` already has `res`/`err` fields for `Resolve` (`test_helpers_test.go:272-279`) and `enumEntries` for `EnumeratePlaylist` (Phase 4). The direct path is exercised in the integration test (Task 13) and the dedicated `resolveUserDirectURL` tests already merged in Phase 3; the unit tests here cover syntactic loopback rejection, DNS-to-loopback rejection before yt-dlp, and invalid kind handling.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestVerifyChannel`
Expected: FAIL — `a.VerifyChannel undefined`.

- [ ] **Step 3: Implement `VerifyChannel`**

Append to `internal/adapters/streams/user_provider_editor.go` (add `"time"` to the import block):

```go
// VerifyChannel performs a non-persisting dry-run of one channel URL (spec §8).
// A blocked/invalid host returns OK=false with a message (a soft result, not a
// Go error) so the form renders a red chip; only an internal failure returns an
// error. Kind is auto-detected when unset and echoed back. is_live is derived
// here for the chip and never persisted (spec §9 item 5).
func (a *Adapter) VerifyChannel(ctx context.Context, req adapters.VerifyChannelRequest) (adapters.VerifyChannelResult, error) {
	url := strings.TrimSpace(req.URL)
	kind := req.Kind
	if kind == "" {
		kind = detectChannelKind(url)
	}
	out := adapters.VerifyChannelResult{Kind: kind}
	switch kind {
	case kindDirect, kindPlaylist, kindSingle:
	default:
		out.Message = fmt.Sprintf("kind %q is not allowed", kind)
		return out, nil
	}

	// Syntactic SSRF gate first — never call the network for a blocked host.
	if err := validateUserProviderHost(url); err != nil {
		out.Message = err.Error()
		return out, nil
	}

	cfg := a.configSnapshot()
	timeout := time.Duration(cfg.CatalogRequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	vctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch kind {
	case kindDirect:
		// Reuse the merged play-time seams (playback.go:1546-1565): userHostResolver()
		// returns a.userURLResolver or net.DefaultResolver; userRedirectHTTPDoer()
		// returns a.userRedirectDoer or a no-redirect client. This keeps Verify and
		// play-time SSRF behavior identical and lets tests inject stubs.
		final, err := resolveUserDirectURL(vctx, a.userRedirectHTTPDoer(), a.userHostResolver(), url, maxUserRedirectHops)
		if err != nil {
			out.Message = playlistErrorForLog(err, url)
			return out, nil
		}
		_ = final
		out.OK = true
		out.IsLive = true // a direct m3u8/HLS stream is treated as live (no pause)
		return out, nil
	case kindPlaylist:
		if err := validateUserProviderResolvedHost(vctx, a.userHostResolver(), url); err != nil {
			out.Message = playlistErrorForLog(err, url)
			return out, nil
		}
		if a.resolver == nil {
			out.Message = "streams source is not running; enable it to verify playlists"
			return out, nil
		}
		entries, err := a.resolver.EnumeratePlaylist(vctx, url, a.cookiesPath, cfg.MaxItemsPerChannel)
		if err != nil {
			out.Message = playlistErrorForLog(err, url)
			return out, nil
		}
		out.OK = true
		out.ItemCount = len(playlistEntriesToItems(entries, cfg.MaxItemsPerChannel))
		return out, nil
	default: // kindSingle
		if err := validateUserProviderResolvedHost(vctx, a.userHostResolver(), url); err != nil {
			out.Message = playlistErrorForLog(err, url)
			return out, nil
		}
		if a.resolver == nil {
			out.Message = "streams source is not running; enable it to verify channels"
			return out, nil
		}
		resolved, err := a.resolver.Resolve(vctx, url, cfg.YoutubeFormat, a.cookiesPath)
		if err != nil {
			out.Message = playlistErrorForLog(err, url)
			return out, nil
		}
		out.OK = true
		out.IsLive = resolved.IsLive
		return out, nil
	}
}
```

All referenced symbols already exist in the merged code (verified) — do NOT invent new ones:
- `a.userHostResolver()` and `a.userRedirectHTTPDoer()` are merged methods on `*Adapter` (`playback.go:1546` and `:1553`). They return the test-injectable `a.userURLResolver`/`a.userRedirectDoer` fields (`adapter.go:57-58`) or production defaults (`net.DefaultResolver` / a no-redirect client). `resolveUserDirectURL` is already called with exactly these two seams at `playback.go:1565`, so Verify reuses the identical SSRF path.
- `cfg.YoutubeFormat` is the yt-dlp format string the play path passes to `Resolve` (`playback.go:295,449`: `format := a.cfg.YoutubeFormat`). Reuse it; there is NO `defaultStreamFormat` constant.

> No new helper or constant is needed for this task — the dry-run reuses the merged play-time resolver seams and config field verbatim.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestVerifyChannel`
Then prove the interface is satisfied — append to `user_provider_editor_test.go`:

```go
var _ adapters.UserProviderEditor = (*Adapter)(nil)
```

Run: `go vet ./internal/adapters/streams/`
Expected: PASS (compile-time proof `*Adapter` implements `UserProviderEditor`).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/user_provider_editor.go internal/adapters/streams/user_provider_editor_test.go
git commit -m "feat(streams): VerifyChannel dry-run (direct probe / playlist count / single is_live)"
```

---

## Task 9: `Start` local-only one-shot user-catalog refresh (Phase-4 residual)

When `cfg.Enabled && !cfg.AllowRemoteManifest`, the periodic refresh loop never spawns (`adapter.go:279`), so user playlist channels enumerated only at edit time would stay empty across a restart's cache-only startup snapshot. Fix it where `Start` decides not to spawn the loop: kick one background `refreshCatalogsDefault(ctx, nil, …)`, which rebuilds the direct+user inline catalogs (live enumeration) and skips the remote-fetch branch. This also covers the auto-enable path (EnsureStarted → Start).

**Files:**
- Modify: `internal/adapters/streams/adapter.go` (`Start`, around lines 279-290)
- Test: `internal/adapters/streams/adapter_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/adapter_test.go`:

```go
func TestStart_LocalOnlyEnumeratesUserPlaylists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	// Seed a user provider with a playlist channel.
	body := []byte(`{"version":1,"providers":[{"id":"user:mix","type":"user","display_name":"Mix","badge_label":"MX","badge_color":"teal","channels":[{"id":"list","name":"Listy","url":"` + playlistURL + `","kind":"playlist"}]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "user_providers.json"), body, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.AllowRemoteManifest = false // local-only: periodic loop is gated OFF
	a := newTestAdapterWithConfig(t, dir, cfg) // see note below
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{playlistURL: ytEntries("a", "b")}}
	a.resolver = fr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The one-shot refresh runs in a goroutine; wait for the playlist to fill.
	waitFor(t, 2*time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		cat, ok := a.catalogs["user:mix"]
		return ok && len(cat.Channels) == 1 && len(cat.Channels[0].Items) == 2
	})
}
```

> Implementation note for the test harness: this test needs an adapter whose `Start` will use the `fakeResolver` rather than constructing a real `ytdlp.Resolver` from a binary. Inspect `Start` (`adapter.go:259-265`): it builds a local `resolver` only when `a.ytdlpBinary != nil`. Construct the adapter with `ytdlpBinary == nil` and pre-set `a.resolver = fr` BEFORE `Start`; update `Start` so it only assigns `a.resolver = resolver` when `resolver != nil`, otherwise it keeps the pre-set resolver. Add `newTestAdapterWithConfig`/`waitFor` as thin helpers in `test_helpers_test.go` if they don't already exist (poll-with-timeout is the standard pattern for the async refresh). Keep these helpers minimal and assert behavior, not timing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestStart_LocalOnlyEnumeratesUserPlaylists`
Expected: FAIL — the playlist never fills (no one-shot refresh; loop gated off).

- [ ] **Step 3: Implement the one-shot in `Start`**

In `internal/adapters/streams/adapter.go`, locate the loop-spawn block in `Start` (around lines 279-290):

```go
		if resolver != nil {
			a.resolver = resolver
		}
		startLoop := a.cfg.Enabled && a.cfg.AllowRemoteManifest
		startLocalOnlyRefresh := a.cfg.Enabled && !a.cfg.AllowRemoteManifest
		...
		if startLoop {
			go a.refreshLoop(loopCtx, loopDone)
	}
```

Replace the trailing `if startLoop { ... }` with a branch that also handles the local-only case using the copied `startLocalOnlyRefresh` bool:

```go
	if startLoop {
		go a.refreshLoop(loopCtx, loopDone)
	} else if startLocalOnlyRefresh {
		// Local-only (AllowRemoteManifest=false): the periodic loop won't run, so
		// user playlist channels would never enumerate beyond the cache-only
		// startup snapshot. Kick ONE background catalog refresh — it rebuilds the
		// direct + user inline catalogs (live yt-dlp enumeration) and skips the
		// remote-fetch branch (refresh.go:370). Serve-stale/cache keeps it cheap.
		// Resolves the Phase 4 documented residual.
		go func() { _ = a.refreshCatalogsDefault(ctx, nil, "startup-local") }()
	}
```

> Use the captured `ctx` (the process-lifetime context passed to `Start`), not `loopCtx` — `loopCtx` is only created in the `startLoop` branch. Confirm `ctx` is in scope at that point in `Start`; if `Start` reassigns `ctx` for the loop, capture the original into a local before the branch.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestStart_LocalOnlyEnumeratesUserPlaylists`
Then full package: `go test ./internal/adapters/streams/`
Expected: PASS. (The remote-on path is unchanged; the loop's first tick still enumerates there, so no double-enumeration is introduced.)

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/adapter_test.go internal/adapters/streams/test_helpers_test.go
git commit -m "fix(streams): enumerate user playlists on Start for local-only setups (Phase 4 residual)"
```

---

## Task 10: `EnsureStreamsEnabled` + `main.go` wiring (auto-enable capability)

`main.go` constructs a guarded `startAdapter(name)` capability (it owns the process ctx, the registry, and the streams `catalogManager`) and injects it into the chassis. The persistence step reuses `catalogManager.patch` → `ApplyConfigValue` (the only TOML-touching path in the feature, spec §10); the hot-start step calls `Start(ctx)` through the same single-flight gate used by the normal startup loop, so the streams adapter cannot be started concurrently.

**Files:**
- Modify: `cmd/mister-groovy-relay/catalog_manager.go` (add `EnsureStreamsEnabled`)
- Modify: `cmd/mister-groovy-relay/main.go` (add `sync`; relocate ctx; type-assert editor; build guarded `startAdapter`; inject)
- Test: `cmd/mister-groovy-relay/catalog_manager_test.go` (if present) or a new focused test

- [ ] **Step 1: Write the failing test**

Append to (or create) `cmd/mister-groovy-relay/catalog_manager_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func TestCatalogManager_EnsureStreamsEnabledPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.WriteAtomic(cfgPath, []byte("[adapters.streams]\nenabled = false\n")); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	a, err := streams.New(streams.AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// NewAdapterSaver(path, mu) — the mutex serializes bridge+adapter saves on the
	// shared config file (uiserver/adapter_saver.go:31). A fresh mutex is fine for
	// this isolated unit test.
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	cm := &catalogManager{adapter: a, adapterSaver: saver}

	if a.IsEnabled() {
		t.Fatal("precondition: streams should be disabled")
	}
	if err := cm.EnsureStreamsEnabled(); err != nil {
		t.Fatalf("EnsureStreamsEnabled: %v", err)
	}
	if !a.IsEnabled() {
		t.Fatal("streams not enabled in-memory after EnsureStreamsEnabled")
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), "enabled = true") {
		t.Fatalf("config file was not updated:\n%s", raw)
	}
	// Idempotent second call.
	if err := cm.EnsureStreamsEnabled(); err != nil {
		t.Fatalf("EnsureStreamsEnabled (idempotent): %v", err)
	}
}
```

> Verified: `func NewAdapterSaver(path string, mu *sync.Mutex) *AdapterSaver` (`uiserver/adapter_saver.go:31`); production wiring is `uiserver.NewAdapterSaver(*cfgPath, saver.Mu())` (`main.go:358`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mister-groovy-relay/ -run TestCatalogManager_EnsureStreamsEnabledPersists`
Expected: FAIL — `cm.EnsureStreamsEnabled undefined`.

- [ ] **Step 3: Implement `EnsureStreamsEnabled` and wire `ensureStarted`**

In `cmd/mister-groovy-relay/catalog_manager.go`, add:

```go
// EnsureStreamsEnabled persists [adapters.streams].enabled=true and applies it
// in memory via the existing ApplyConfigValue path (atomic section rewrite
// under the bridge mutex). Idempotent: a no-op write when already enabled.
// This is the ONLY persistent-TOML touch in the user-providers feature (spec
// §10); it does NOT start the adapter — the caller hot-starts separately.
func (m *catalogManager) EnsureStreamsEnabled() error {
	_, err := m.patch(func(cfg *streams.Config) {
		cfg.Enabled = true
	})
	return err
}
```

In `cmd/mister-groovy-relay/main.go`:

1. **Relocate the process-context creation** above `chassis.New` so the `ensureStarted` closure can capture it. Move these two lines (currently at lines 502-503):
   ```go
   ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
   defer stop()
   ```
   to just **before** the `chassisSrv, err := chassis.New(...)` call (before line 430). The HTTP listener block and the adapter-start loop (lines 505-528) already reference `ctx` after this point and are unaffected by the earlier declaration.

2. **Type-assert the editor** in the existing streams-capability block (after line 408, inside the `if streamsA, ok := reg.Get("streams")` block). Declare `userProviderEditor` beside `presetEditor`, then assign it in the streams block:
   ```go
   	var userProviderEditor adapters.UserProviderEditor
   	// ... inside the existing `if streamsA, ok := reg.Get("streams"); ok {` block:
   		if ed, ok := streamsA.(adapters.UserProviderEditor); ok {
   			userProviderEditor = ed
   		}
   ```

3. **Build the guarded capability closure** after `cm := &catalogManager{...}` (line 418). Add `"sync"` to `main.go` imports.
   ```go
   	var startMu sync.Mutex
   	// startAdapter is the single path for normal startup and mid-process
   	// auto-enable hot-start. It serializes Status/Start so non-idempotent adapter
   	// Start methods (notably streams refresh-loop setup) cannot race.
   	startAdapter := func(name string) error {
   		startMu.Lock()
   		defer startMu.Unlock()
   		a, ok := reg.Get(name)
   		if !ok {
   			return fmt.Errorf("startAdapter: adapter %q not registered", name)
   		}
   		if a.Status().State == adapters.StateRunning {
   			return nil
   		}
   		return a.Start(ctx)
   	}
   	// ensureStarted enables + hot-starts a disabled adapter mid-process (spec
   	// §10). Only "streams" is supported today. It (1) persists enabled=true via
   	// the catalog manager, then (2) starts through startAdapter.
   	ensureStarted := func(name string) error {
   		if name != "streams" {
   			return fmt.Errorf("ensureStarted: unsupported adapter %q", name)
   		}
   		if err := cm.EnsureStreamsEnabled(); err != nil {
   			return err
   		}
   		return startAdapter("streams")
   	}
   ```

4. **Use the same gate in the normal startup loop** (lines 520-528):
   ```go
   	for _, a := range reg.List() {
   		if !a.IsEnabled() {
   			slog.Info("adapter disabled", "name", a.Name())
   			continue
   		}
   		if err := startAdapter(a.Name()); err != nil {
   			slog.Error("adapter start", "name", a.Name(), "err", err)
   		}
   	}
   ```

5. **Inject into `chassis.Config`** (add to the `chassis.New(chassis.Config{...})` literal, near `PresetEditor`):
   ```go
   		PresetEditor:              presetEditor,
   		UserProviderEditor:        userProviderEditor,
   		EnsureAdapterStarted:      ensureStarted,
   ```

> `fmt` and `adapters` are already imported in `main.go`; add `sync`. After editing, the `ctx` relocation means `signal`/`os`/`syscall` are used earlier but still imported.

- [ ] **Step 4: Run test + build to verify**

Run: `go test ./cmd/mister-groovy-relay/ -run TestCatalogManager_EnsureStreamsEnabledPersists`
Then: `go build ./cmd/mister-groovy-relay/`
Expected: test PASS; build succeeds once Task 11 adds the `chassis.Config` fields. (If building before Task 11, the two new Config fields will be "unknown field" — implement Task 11 first or together. Run `go vet ./cmd/...` after Task 11.)

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/catalog_manager.go cmd/mister-groovy-relay/main.go cmd/mister-groovy-relay/catalog_manager_test.go
git commit -m "feat(cmd): EnsureStreamsEnabled + ensureStarted auto-enable capability"
```

> If the build depends on Task 11's Config fields, stage and commit Tasks 10 and 11 together (note this in the commit and run the full `go build ./...` before committing).

---

## Task 11: Chassis `Config`/`Server` fields + SSE envelope types

Add the injected dependencies to the chassis and define the SSE envelope Go types (shape only — Phase 6 wires the emit loop + client rendering).

**Files:**
- Modify: `internal/chassis/server.go` (`Config` ~lines 19-136, `Server` ~lines 139-184, `New` ~lines 228-232)
- Create: `internal/chassis/catalog_provider.go` (envelope types + builder; handlers added in Task 12)
- Test: `internal/chassis/catalog_provider_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/chassis/catalog_provider_test.go`:

```go
package chassis

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestBuildCatalogProviderEnvelope_Shape(t *testing.T) {
	t.Parallel()
	p := adapters.CatalogProvider{
		ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal",
		Groups: []adapters.CatalogGroup{{ID: "g1", Name: "Group", Channels: []adapters.CatalogChannel{
			{ID: "a", Name: "A", PlayMode: "SHUFFLE", Live: false},
		}}},
	}
	env := buildCatalogProviderEnvelope(p)
	if env.ID != "user:mix" || env.BadgeClass != "u-teal" {
		t.Fatalf("envelope identity = %+v", env)
	}
	if len(env.Groups) != 1 || len(env.Groups[0].Channels) != 1 || env.Groups[0].Channels[0].ID != "a" {
		t.Fatalf("envelope groups = %+v", env.Groups)
	}
}

func TestProviderStatusEnvelope_AutoEnabledField(t *testing.T) {
	t.Parallel()
	env := providerStatusEnvelope{Provider: "user:mix", AutoEnabledStreams: "on"}
	if env.AutoEnabledStreams != "on" {
		t.Fatal("autoEnabledStreams not carried")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestBuildCatalogProviderEnvelope_Shape|TestProviderStatusEnvelope_AutoEnabledField'`
Expected: FAIL — `undefined: buildCatalogProviderEnvelope` / `providerStatusEnvelope`.

- [ ] **Step 3: Add Config/Server fields + envelope types**

In `internal/chassis/server.go`, add to the `Config` struct (near `PresetEditor`, ~line 86):

```go
	// UserProviderEditor backs the catalog authoring routes (create/update/
	// delete/reorder/verify). Interface-only, like PresetEditor — the streams
	// adapter implements it and main.go injects it by type assertion.
	UserProviderEditor adapters.UserProviderEditor
	// EnsureAdapterStarted enables + hot-starts a disabled adapter mid-process
	// (spec §10). Injected from main.go (it owns the process ctx + registry).
	// Nil in tests that don't exercise auto-enable.
	EnsureAdapterStarted func(name string) error
```

Add to the `Server` struct (near `presetEditor`, ~line 166):

```go
	userProviderEditor   adapters.UserProviderEditor
	ensureAdapterStarted func(name string) error
```

Wire them in `New()` (near `presetEditor: cfg.PresetEditor`, ~line 230):

```go
		userProviderEditor:   cfg.UserProviderEditor,
		ensureAdapterStarted: cfg.EnsureAdapterStarted,
```

Create `internal/chassis/catalog_provider.go` with the envelope types + builder (handlers come in Task 12):

```go
package chassis

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// --- SSE envelope shapes (Phase 5 defines the shape; Phase 6 wires emission
// into handleEvents and renders the chips client-side). ---

// catalogProviderEnvelope is one provider in a `catalog` SSE event.
type catalogProviderEnvelope struct {
	ID          string                  `json:"id"`
	DisplayName string                  `json:"displayName"`
	BadgeLabel  string                  `json:"badgeLabel"`
	BadgeClass  string                  `json:"badgeClass"`
	Live        bool                    `json:"live"`
	Groups      []catalogGroupEnvelope  `json:"groups"`
}

type catalogGroupEnvelope struct {
	ID       string                   `json:"id"`
	Name     string                   `json:"name"`
	Channels []catalogChannelEnvelope `json:"channels"`
}

type catalogChannelEnvelope struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PlayMode string `json:"playMode"`
	Live     bool   `json:"live"`
}

// catalogEnvelope is the `catalog` SSE event payload.
type catalogEnvelope struct {
	Providers []catalogProviderEnvelope `json:"providers"`
}

// providerStatusEnvelope is the `providerStatus` SSE event payload: per-channel
// enumeration status plus the optional auto-enable signal (spec §8). The chassis
// renders "Enumerating…"/"N videos"/"✗ error" chips and the auto-enable toast
// from this in Phase 6.
type providerStatusEnvelope struct {
	Provider           string                  `json:"provider"`
	Channels           []channelStatusEnvelope `json:"channels,omitempty"`
	AutoEnabledStreams string                  `json:"autoEnabledStreams,omitempty"` // "on" | "restart-required"
}

type channelStatusEnvelope struct {
	Channel   string `json:"channel"`
	State     string `json:"state"`     // "ready" | "pending" | "error"
	ItemCount int    `json:"itemCount"`
}

func buildCatalogProviderEnvelope(p adapters.CatalogProvider) catalogProviderEnvelope {
	groups := make([]catalogGroupEnvelope, 0, len(p.Groups))
	for _, g := range p.Groups {
		chans := make([]catalogChannelEnvelope, 0, len(g.Channels))
		for _, c := range g.Channels {
			chans = append(chans, catalogChannelEnvelope{ID: c.ID, Name: c.Name, PlayMode: c.PlayMode, Live: c.Live})
		}
		groups = append(groups, catalogGroupEnvelope{ID: g.ID, Name: g.Name, Channels: chans})
	}
	return catalogProviderEnvelope{
		ID: p.ID, DisplayName: p.DisplayName, BadgeLabel: p.BadgeLabel,
		BadgeClass: p.BadgeClass, Live: p.Live, Groups: groups,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run 'TestBuildCatalogProviderEnvelope_Shape|TestProviderStatusEnvelope_AutoEnabledField'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/catalog_provider.go internal/chassis/catalog_provider_test.go
git commit -m "feat(chassis): UserProviderEditor/EnsureAdapterStarted wiring + SSE envelope types"
```

---

## Task 12: Chassis route handlers + Mount

Add the five routes. They parse JSON bodies, call the injected editor, map `*adapters.QuickCastError` to inline error responses (like preset handlers), trigger auto-enable on create, refresh the chassis snapshot, and return the saved provider + auto-enable outcome.

**Files:**
- Modify: `internal/chassis/catalog_provider.go` (add handlers + JSON helpers)
- Modify: `internal/chassis/server.go` (`Mount`, ~lines 339-352)
- Modify: `internal/chassis/sameorigin.go` (guard all unsafe methods)
- Test: `internal/chassis/sameorigin_test.go`
- Test: `internal/chassis/catalog_provider_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/catalog_provider_test.go` (add imports `"bytes"`, `"context"`, `"encoding/json"`, `"errors"`, `"net/http"`, `"net/http/httptest"`):

```go
type fakeUserProviderEditor struct {
	createRes     adapters.UserProviderResult
	createErr     error
	updateRes     adapters.UserProviderResult
	deleteRes     adapters.UserProviderResult
	verifyRes     adapters.VerifyChannelResult
	reorderErr    error
	lastUpdateID  string
	lastDeleteID  string
	lastReorderID string
	lastCreate    adapters.UserProviderForm
}

func (f *fakeUserProviderEditor) CreateUserProvider(_ context.Context, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	f.lastCreate = form
	return f.createRes, f.createErr
}
func (f *fakeUserProviderEditor) UpdateUserProvider(_ context.Context, id string, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	f.lastUpdateID = id
	return f.updateRes, nil
}
func (f *fakeUserProviderEditor) DeleteUserProvider(_ context.Context, id string) (adapters.UserProviderResult, error) {
	f.lastDeleteID = id
	return f.deleteRes, nil
}
func (f *fakeUserProviderEditor) ReorderUserProvider(_ context.Context, id string, adapters.ReorderRequest) error {
	f.lastReorderID = id
	return f.reorderErr
}
func (f *fakeUserProviderEditor) VerifyChannel(context.Context, adapters.VerifyChannelRequest) (adapters.VerifyChannelResult, error) {
	return f.verifyRes, nil
}

func newCatalogTestServer(t *testing.T, ed adapters.UserProviderEditor, ensure func(string) error) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.UserProviderEditor = ed
	cfg.EnsureAdapterStarted = ensure
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func catalogRoute(t *testing.T, s *Server) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

func postJSON(t *testing.T, h http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func routeJSON(t *testing.T, h http.Handler, method, target string, body any, sameOrigin bool) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	if sameOrigin {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	} else {
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandleCatalogProviderCreate_AutoEnableInvokesEnsure(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{createRes: adapters.UserProviderResult{
		Provider:         adapters.CatalogProvider{ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal"},
		AutoEnableNeeded: true,
	}}
	called := false
	ensure := func(name string) error { called = (name == "streams"); return nil }
	s := newCatalogTestServer(t, ed, ensure)

	rr := postJSON(t, s.handleCatalogProviderCreate, http.MethodPost, "/receiver/catalog/provider",
		map[string]any{"displayName": "Mix", "badgeLabel": "MX", "badgeColor": "teal",
			"channels": []map[string]any{{"name": "Live", "url": "https://cdn.example.com/live.m3u8"}}})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Fatal("EnsureAdapterStarted not called on first-provider create")
	}
	var resp struct {
		OK                 bool   `json:"ok"`
		Provider           struct{ ID string `json:"id"` } `json:"provider"`
		AutoEnabledStreams string `json:"autoEnabledStreams"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Provider.ID != "user:mix" || resp.AutoEnabledStreams != "on" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestHandleCatalogProviderCreate_AutoEnableFailureReportsRestart(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{createRes: adapters.UserProviderResult{
		Provider: adapters.CatalogProvider{ID: "user:mix"}, AutoEnableNeeded: true,
	}}
	ensure := func(string) error { return errorsNew("yt-dlp missing") }
	s := newCatalogTestServer(t, ed, ensure)
	rr := postJSON(t, s.handleCatalogProviderCreate, http.MethodPost, "/receiver/catalog/provider",
		map[string]any{"displayName": "Mix", "badgeLabel": "MX", "badgeColor": "teal"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (provider still saved), body = %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		AutoEnabledStreams string `json:"autoEnabledStreams"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.AutoEnabledStreams != "restart-required" {
		t.Fatalf("autoEnabledStreams = %q, want restart-required", resp.AutoEnabledStreams)
	}
}

func TestHandleCatalogChannelVerify_ReturnsResult(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{verifyRes: adapters.VerifyChannelResult{OK: true, Kind: "playlist", ItemCount: 47}}
	s := newCatalogTestServer(t, ed, nil)
	rr := postJSON(t, s.handleCatalogChannelVerify, http.MethodPost, "/receiver/catalog/channel/verify",
		map[string]any{"url": "https://www.youtube.com/playlist?list=PL1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var vr adapters.VerifyChannelResult
	if err := json.Unmarshal(rr.Body.Bytes(), &vr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !vr.OK || vr.ItemCount != 47 {
		t.Fatalf("verify resp = %+v", vr)
	}
}

func TestCatalogProviderRoutes_MountedMethodsCallEditor(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{
		updateRes: adapters.UserProviderResult{
			Provider: adapters.CatalogProvider{ID: "user:mix", DisplayName: "Mix"},
		},
		deleteRes: adapters.UserProviderResult{ClearedSlots: []int{2}},
		verifyRes: adapters.VerifyChannelResult{OK: true, Kind: "direct"},
	}
	s := newCatalogTestServer(t, ed, nil)
	mux := catalogRoute(t, s)

	if rr := routeJSON(t, mux, http.MethodPut, "/receiver/catalog/provider/user:mix", map[string]any{"displayName": "Mix"}, true); rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rr.Code, rr.Body.String())
	}
	if ed.lastUpdateID != "user:mix" {
		t.Fatalf("lastUpdateID = %q", ed.lastUpdateID)
	}
	if rr := routeJSON(t, mux, http.MethodPost, "/receiver/catalog/provider/user:mix/reorder", map[string]any{"channels": []map[string]any{{"id": "a", "order": 1}}}, true); rr.Code != http.StatusOK {
		t.Fatalf("reorder status = %d body=%s", rr.Code, rr.Body.String())
	}
	if ed.lastReorderID != "user:mix" {
		t.Fatalf("lastReorderID = %q", ed.lastReorderID)
	}
	if rr := routeJSON(t, mux, http.MethodDelete, "/receiver/catalog/provider/user:mix", map[string]any{}, true); rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body=%s", rr.Code, rr.Body.String())
	} else {
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal delete response: %v", err)
		}
		if _, ok := body["provider"]; ok {
			t.Fatalf("delete response unexpectedly included provider: %s", rr.Body.String())
		}
	}
	if ed.lastDeleteID != "user:mix" {
		t.Fatalf("lastDeleteID = %q", ed.lastDeleteID)
	}
	if rr := routeJSON(t, mux, http.MethodPost, "/receiver/catalog/channel/verify", map[string]any{"url": "https://cdn.example.com/live.m3u8"}, true); rr.Code != http.StatusOK {
		t.Fatalf("verify status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCatalogProviderRoutes_BlockCrossSiteUnsafeMethods(t *testing.T) {
	t.Parallel()
	s := newCatalogTestServer(t, &fakeUserProviderEditor{}, nil)
	mux := catalogRoute(t, s)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/receiver/catalog/provider/user:mix"},
		{http.MethodDelete, "/receiver/catalog/provider/user:mix"},
	}
	for _, tc := range cases {
		rr := routeJSON(t, mux, tc.method, tc.path, map[string]any{}, false)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", tc.method, tc.path, rr.Code)
		}
	}
}

func errorsNew(s string) error { return errors.New(s) }
```

> Note: the first create/verify tests call handler funcs directly for terse response-shape assertions; `TestCatalogProviderRoutes_MountedMethodsCallEditor` and `TestCatalogProviderRoutes_BlockCrossSiteUnsafeMethods` exercise `Mount` + `requireSameOrigin` on the actual mux.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestHandleCatalogProvider|TestHandleCatalogChannel'`
Expected: FAIL — `s.handleCatalogProviderCreate undefined` etc.

- [ ] **Step 3: Implement the handlers + JSON helpers + Mount**

Append to `internal/chassis/catalog_provider.go` (add imports `"encoding/json"`, `"errors"`, `"net/http"`, `"strings"`). Reuse the existing package-level `writeJSON` from `settings.go`; do not add another function with that name.

```go
// --- request/response wire shapes (camelCase to match the receiver JS) ---

type catalogProviderRequest struct {
	DisplayName string                  `json:"displayName"`
	BadgeLabel  string                  `json:"badgeLabel"`
	BadgeColor  string                  `json:"badgeColor"`
	Groups      []catalogGroupRequest   `json:"groups"`
	Channels    []catalogChannelRequest `json:"channels"`
}

type catalogGroupRequest struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

type catalogChannelRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	PlayMode string `json:"playMode"`
	GroupID  string `json:"groupId"`
	Order    int    `json:"order"`
}

type catalogProviderResponse struct {
	OK                 bool                     `json:"ok"`
	Provider           *catalogProviderEnvelope `json:"provider,omitempty"`
	ClearedSlots       []int                    `json:"clearedSlots,omitempty"`
	AutoEnabledStreams string                   `json:"autoEnabledStreams,omitempty"`
}

func (r catalogProviderRequest) toForm(id string) adapters.UserProviderForm {
	groups := make([]adapters.UserGroupForm, 0, len(r.Groups))
	for _, g := range r.Groups {
		groups = append(groups, adapters.UserGroupForm{ID: g.ID, Name: g.Name, Order: g.Order})
	}
	channels := make([]adapters.UserChannelForm, 0, len(r.Channels))
	for _, c := range r.Channels {
		channels = append(channels, adapters.UserChannelForm{
			ID: c.ID, Name: c.Name, URL: c.URL, Kind: c.Kind,
			PlayMode: c.PlayMode, GroupID: c.GroupID, Order: c.Order,
		})
	}
	return adapters.UserProviderForm{
		ID: id, DisplayName: r.DisplayName, BadgeLabel: r.BadgeLabel,
		BadgeColor: r.BadgeColor, Groups: groups, Channels: channels,
	}
}

func writeCatalogError(w http.ResponseWriter, err error) {
	var qerr *adapters.QuickCastError
	if errors.As(err, &qerr) {
		writeSettingsChip(w, qerr.Status, qerr.Chip)
		return
	}
	writeSettingsChip(w, http.StatusInternalServerError, "SAVE FAILED")
}

func (s *Server) handleCatalogProviderCreate(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	var req catalogProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	res, err := s.userProviderEditor.CreateUserProvider(r.Context(), req.toForm(""))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	provider := buildCatalogProviderEnvelope(res.Provider)
	resp := catalogProviderResponse{OK: true, Provider: &provider}
	if res.AutoEnableNeeded {
		// Provider is already saved; auto-enable is best-effort. On failure we
		// fall back to the existing "restart the bridge" UX tier (spec §10).
		if s.ensureAdapterStarted == nil {
			resp.AutoEnabledStreams = "restart-required"
		} else if err := s.ensureAdapterStarted("streams"); err != nil {
			resp.AutoEnabledStreams = "restart-required"
		} else {
			resp.AutoEnabledStreams = "on"
		}
	}
	s.refreshSnapshotNow()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCatalogProviderUpdate(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	id := r.PathValue("id")
	var req catalogProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	res, err := s.userProviderEditor.UpdateUserProvider(r.Context(), id, req.toForm(id))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	s.refreshSnapshotNow()
	provider := buildCatalogProviderEnvelope(res.Provider)
	writeJSON(w, http.StatusOK, catalogProviderResponse{
		OK: true, Provider: &provider, ClearedSlots: res.ClearedSlots,
	})
}

func (s *Server) handleCatalogProviderDelete(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	id := r.PathValue("id")
	res, err := s.userProviderEditor.DeleteUserProvider(r.Context(), id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	s.refreshSnapshotNow()
	writeJSON(w, http.StatusOK, catalogProviderResponse{OK: true, ClearedSlots: res.ClearedSlots})
}

func (s *Server) handleCatalogProviderReorder(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	id := r.PathValue("id")
	var body struct {
		Channels []adapters.UserOrderEntry `json:"channels"`
		Groups   []adapters.UserOrderEntry `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if err := s.userProviderEditor.ReorderUserProvider(r.Context(), id, adapters.ReorderRequest{
		Channels: body.Channels, Groups: body.Groups,
	}); err != nil {
		writeCatalogError(w, err)
		return
	}
	s.refreshSnapshotNow()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCatalogChannelVerify(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	var body struct {
		URL  string `json:"url"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	res, err := s.userProviderEditor.VerifyChannel(r.Context(), adapters.VerifyChannelRequest{URL: body.URL, Kind: body.Kind})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
```

In `internal/chassis/server.go`, register the routes in `Mount` (after the preset routes, ~line 340 — config edits, so `requireSameOrigin` only, NOT `requireSetupComplete`):

```go
	mux.Handle("POST /receiver/catalog/provider", requireSameOrigin(http.HandlerFunc(s.handleCatalogProviderCreate)))
	mux.Handle("PUT /receiver/catalog/provider/{id}", requireSameOrigin(http.HandlerFunc(s.handleCatalogProviderUpdate)))
	mux.Handle("DELETE /receiver/catalog/provider/{id}", requireSameOrigin(http.HandlerFunc(s.handleCatalogProviderDelete)))
	mux.Handle("POST /receiver/catalog/provider/{id}/reorder", requireSameOrigin(http.HandlerFunc(s.handleCatalogProviderReorder)))
	mux.Handle("POST /receiver/catalog/channel/verify", requireSameOrigin(http.HandlerFunc(s.handleCatalogChannelVerify)))
```

In `internal/chassis/sameorigin.go`, broaden the guard from POST-only to every unsafe method:

```go
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafeMethod(r.Method) {
			switch r.Header.Get("Sec-Fetch-Site") {
			case "same-origin", "same-site":
				// allowed
			case "":
				if !sameOriginByOriginOrReferer(r) {
					writeJSONError(w, http.StatusForbidden, "cross-site request blocked")
					return
				}
			default:
				writeJSONError(w, http.StatusForbidden, "cross-site request blocked")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}
```

Append these focused cases to `internal/chassis/sameorigin_test.go`:

```go
func TestRequireSameOriginBlocksCrossSiteUnsafeMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
			req := httptest.NewRequest(method, "/receiver/catalog/provider/user:mix", nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			w := httptest.NewRecorder()
			requireSameOrigin(next).ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
			if called {
				t.Fatal("next handler called for cross-site unsafe method")
			}
		})
	}
}

func TestRequireSameOriginAllowsSameOriginUnsafeMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(method, "/receiver/catalog/provider/user:mix", nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			w := httptest.NewRecorder()
			requireSameOrigin(next).ServeHTTP(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", w.Code)
			}
			if !called {
				t.Fatal("next handler not called for same-origin unsafe method")
			}
		})
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run 'TestHandleCatalogProvider|TestHandleCatalogChannel|TestBuildCatalogProviderEnvelope'`
Then build everything: `go build ./...` and `go vet ./...`
Expected: PASS; build clean (Tasks 10+11+12 together make `main.go` compile).

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/catalog_provider.go internal/chassis/catalog_provider_test.go internal/chassis/server.go internal/chassis/sameorigin.go internal/chassis/sameorigin_test.go
git commit -m "feat(chassis): catalog provider create/update/delete/reorder/verify routes"
```

---

## Task 13: Integration test (add → cast → star → cast preset → delete → cleanup)

End-to-end with the streams adapter (no chassis HTTP needed — drive the editor + caster directly), proving a user `direct` channel casts and that delete clears its preset slot. Uses the existing fake-mister/ffmpeg harness conventions (spec §12).

**Files:**
- Create: `tests/integration/user_provider_test.go`

- [ ] **Step 1: Write the failing test**

Create `tests/integration/user_provider_test.go`. Inspect a sibling integration test first (`ls tests/integration` + read one) to copy the exact build tag, adapter construction, fake-mister/ffmpeg setup, and a direct-stream HLS fixture URL already used there — then adapt:

```go
//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	// ... plus the config/core/fake-mister imports the sibling tests use.
)

func TestUserProvider_AddCastStarDeleteCleanup(t *testing.T) {
	// 1. Build a streams adapter wired to a fake core/manager + fake-mister
	//    (mirror the existing integration harness exactly).
	// 2. editor := streams.Adapter (it implements adapters.UserProviderEditor).
	// 3. CreateUserProvider with one `direct` channel whose URL is the local
	//    test HLS fixture the other integration tests serve.
	// 4. Assert the provider appears in editor-cast path: cast the channel and
	//    assert a session starts (reuse the sibling test's cast assertion).
	// 5. Star the channel (SetPresetStarred), cast the preset slot, assert play.
	// 6. DeleteUserProvider → assert ClearedSlots includes the starred slot and
	//    the preset bank no longer references it.
	_ = context.Background
	_ = adapters.UserProviderResult{}
	_ = streams.AdapterConfig{}
	t.Skip("fill in using the sibling integration harness — see TestBasic_* in tests/integration")
}
```

> This task starts as a scaffold only to force the harness discovery step. Replace the `t.Skip` with real assertions before committing; do NOT commit this test while it still skips. Keep the direct-stream fixture local (no external network) — reuse whatever local m3u8 the existing direct-stream integration test uses.

- [ ] **Step 2: Run test to verify it fails (then is filled in)**

Run: `go test -tags=integration ./tests/integration/ -run TestUserProvider_AddCastStarDeleteCleanup`
Expected first: SKIP (scaffold). After filling in the harness + assertions: it must FAIL before the feature behaves and PASS after. (Requires ffmpeg + ffprobe on PATH per CLAUDE.md.)

- [ ] **Step 3: Fill in the harness + assertions**

Copy the construction + cast-assertion lines from the sibling direct-stream integration test, point the channel URL at the same local fixture, and implement steps 3-6 above. Remove the `t.Skip`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test -tags=integration ./tests/integration/ -run TestUserProvider_AddCastStarDeleteCleanup`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/user_provider_test.go
git commit -m "test(integration): user provider add/cast/star/delete-cleanup end-to-end"
```

---

## Final verification (run before declaring the phase done)

- [ ] `go vet ./...`
- [ ] `go test ./...` (all unit tests green)
- [ ] `go test -tags=integration ./...` (ffmpeg + ffprobe on PATH)
- [ ] `go build ./...`
- [ ] `git diff --cached --name-only` shows only intended paths before each commit.

> `go test -race ./...` is CI-only (no local cgo/gcc) — do not attempt locally; CI runs it.

Commit the plan itself (gitignored dir → force-add):

```bash
git add -f docs/superpowers/plans/2026-06-03-user-custom-providers-phase5-routes-editor-autoenable.md
git commit -m "docs(plan): user custom providers Phase 5 (routes/editor/auto-enable)"
```

---

## Spec-coverage self-review

Checked each Phase-5 spec requirement against a task:

- **§8 `UserProviderEditor` (Create/Update/Delete/Verify)** → Tasks 1, 4, 5, 6, 8. Reorder (§8 reorder route) → Tasks 1, 7. ✓ (Reorder added to the interface; the user brief's "Create/Update/Delete/Verify" shorthand omitted it but the route is explicitly in scope.)
- **§8 routes on the single chassis mux** (`POST /receiver/catalog/provider`, `PUT/DELETE .../{id}`, `POST .../channel/verify`, `POST .../{id}/reorder`) → Task 12. One-listener invariant honored (mounted on the existing mux). ✓
- **§8 same-origin/CSRF guard** → `requireSameOrigin` wrapping in Task 12; middleware broadened from POST-only to all unsafe methods, with PUT/DELETE/PATCH tests. ✓
- **§8 update = full replace preserving locked IDs; cleared preset slots reported** → Task 5. ✓
- **§8/§9 delete + preset cleanup count** → Task 6 + Task 2 (`ClearProviderSlots`). ✓
- **§8 Verify dry-run** (direct→HEAD/GET, single/playlist→yt-dlp) → Task 8; reuses `resolveUserDirectURL`/`Resolve`/`EnumeratePlaylist`. Decision documented: single uses `Resolve` (already a non-downloading dump-json resolve that yields `is_live`) instead of a new `--simulate` method — faithful to intent, reuses merged code. ✓
- **§8 reorder persists Order, no re-enumerate** → Task 7 (cache-only rebuild; test asserts `enumCalls` unchanged). ✓
- **§10 live rebuild bypassing ApplyConfig/TOML** → Task 3 + Tasks 4-6 (serialized `userEditMu`; candidate `PlanPut`/`PlanDelete`; off-lock `buildSnapshotWithEnumerator`; atomic `CommitProviders`; `installSnapshotLocked`; preset `Rehydrate`). ✓
- **§10 auto-enable + hot-start `EnsureStarted(name)`** → Task 10 (closure in main.go: `EnsureStreamsEnabled` persist + guarded `startAdapter`), Task 11 (chassis injection), Task 12 (create route invokes it; failure → `restart-required`). ctx-relocation handled and normal startup uses the same gate. ✓
- **§10 failure path = restart toast** → Task 12 (`AutoEnabledStreams: "restart-required"`, provider still saved). ✓
- **Phase-4 residual (`AllowRemoteManifest=false` local-only enumeration)** → Task 9 (`Start` one-shot `refreshCatalogsDefault(ctx, nil, …)` when the loop is gated off; also covers the auto-enable Start). Decision documented. ✓
- **SSRF posture + `addr.Unmap()`** → Verify reuses `validateUserProviderHost` (syntactic) + `resolveUserDirectURL`/`validateUserProviderResolvedHost` (resolved-IP, `Unmap`) from Phase 2/3. ✓
- **`a.mu` never held across I/O** → all rebuilds/enumerate/verify off-lock; only install + preset writes under lock (Tasks 3-8). ✓
- **Chassis imports adapter interfaces only** → editor type-asserted in main.go (Task 10), chassis holds `adapters.UserProviderEditor` (Task 11). ✓
- **First-run setup interaction** → catalog-edit routes are config edits, NOT setup-gated (`requireSameOrigin` only), matching preset star/move and `settings/catalog/provider/{id}` (verified in Mount); documented in Task 12. ✓
- **SSE event shape defined (rendering deferred)** → Task 11 envelope types + builder; auto-enable outcome returned in the create HTTP response. Client SSE emit/render is Phase 6. ✓

**Deferred to Phase 6 (noted):** authoring form template, `provider-form.js` (+ `*.behavior.test.js`), `.ic.u-<token>`/`.badge.u-<token>` palette CSS, SSE `catalog`/`providerStatus` emit-loop + client chip rendering. No new chassis JS in Phase 5 (so no `node --test` additions).

**Placeholder scan:** the only intentionally-deferred concretion is the Task 13 integration harness, which must be copied from a sibling integration test and must not be committed while `t.Skip` remains. All Go code blocks for novel logic are otherwise complete.

**Type consistency:** `UserProviderForm`/`UserProviderResult`/`ReorderRequest`/`VerifyChannelResult` (Task 1) are used verbatim in Tasks 4-8 (streams) and Task 12 (chassis `toForm`). `buildChassisCatalogProvider` (merged) returns `adapters.CatalogProvider`, consumed by `buildCatalogProviderEnvelope` (Task 11). `ClearProviderSlots`/`ClearChannelSlots`/`Rehydrate` (Task 2) called in Tasks 5/6. `buildUserCatalogSnapshotLive`/`buildUserCatalogSnapshotCacheOnly`, `CommitProviders`, and `installUserCatalogSnapshot` (Task 3) are used in Tasks 4-7. Names match across tasks.
