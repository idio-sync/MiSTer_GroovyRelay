# User Custom Providers — Phase 6: Chassis Authoring UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the operator-facing authoring UI for user custom providers inside the receiver catalog drawer — a guided provider form (identity, groups, channels, inline editor, Verify, drag-reorder, delete-with-cleanup) plus the CSP-safe palette CSS and the live `catalog`/`providerStatus` SSE rendering — all driving the Phase 5 `/ui/catalog/*` routes already merged on `main`.

**Architecture:** Phase 5 shipped the write routes (`POST/PUT/DELETE/reorder/verify`), the `UserProviderEditor` interface, auto-enable/hot-start, and the SSE envelope **Go types** (defined, not emitted). Phase 6 adds (1) two small **additive** backend read seams folded into one new `adapters.UserProviderViewer` interface — an authoring-shape read (`UserProviderForm(id)`) to populate the ✎ editor, and per-channel enumeration status (`UserProviderStatuses()`) for the status chips — neither of which changes any Phase 5 contract; (2) the `catalog-provider-form.html` partial + `provider-form.js` client logic (auto-detect, glyph suggest, Verify, drag-reorder, save/delete) wired to the existing chassis mux and `window.Chassis.*` namespace; (3) the `.ic.u-<token>`/`.badge.u-<token>` palette classes in `chassis.css`; and (4) `catalog`/`providerStatus` emission in the existing `handleEvents` snapshot-diff loop so an open drawer updates without reload. The auto-enable toast is driven from the **create route's HTTP response** (`autoEnabledStreams`), which Phase 5 already returns; the SSE `providerStatus.autoEnabledStreams` field stays unused by Phase 6's emitter.

**Tech Stack:** Go 1.26 (`internal/adapters`, `internal/adapters/streams`, `internal/chassis`, `cmd/mister-groovy-relay`); html/template + htmx + vanilla JS under `internal/chassis/templates` + `internal/chassis/static`; `node --test` (Node 24) for `*.behavior.test.js`. `go test -race` is a CI-only gate (no local cgo); run plain `go test` + `node --test` locally.

---

## Background: what is already merged on `main` (do NOT re-create — verified against the tree)

Phases 1–5 are merged. Verified against the current tree:

- **Write routes + handlers** (`internal/chassis/catalog_provider.go`): `handleCatalogProviderCreate` (`POST /ui/catalog/provider`), `handleCatalogProviderUpdate` (`PUT …/{id}`), `handleCatalogProviderDelete` (`DELETE …/{id}`), `handleCatalogProviderReorder` (`POST …/{id}/reorder`), `handleCatalogChannelVerify` (`POST /ui/catalog/channel/verify`). All registered in `Mount` (`server.go:354-358`) under `requireSameOrigin` (NOT setup-gated). All decode **camelCase JSON** request bodies (`catalogProviderRequest{displayName,badgeLabel,badgeColor,groups[],channels[{id,name,url,kind,playMode,groupId,order}]}`) and return JSON via `writeJSON`. The create route already returns `autoEnabledStreams` (`"on"`/`"restart-required"`) in `catalogProviderResponse` and every mutating route calls `s.refreshSnapshotNow()`.
- **SSE envelope Go types DEFINED but NOT emitted** (`internal/chassis/catalog_provider.go:13-73`): `catalogProviderEnvelope{id,displayName,badgeLabel,badgeClass,live,groups[]}`, `catalogGroupEnvelope{id,name,channels[]}`, `catalogChannelEnvelope{id,name,playMode,live}`, `catalogEnvelope{providers[]}`, `providerStatusEnvelope{provider,channels[],autoEnabledStreams?}`, `channelStatusEnvelope{channel,state("ready"|"pending"|"error"),itemCount}`, and the builder `buildCatalogProviderEnvelope(adapters.CatalogProvider) catalogProviderEnvelope`. `handleEvents` (`events.go:396`) does NOT emit `catalog` or `providerStatus`.
- **`UserProviderEditor`** (`internal/adapters/user_provider.go`): `CreateUserProvider`/`UpdateUserProvider`/`DeleteUserProvider`/`ReorderUserProvider`/`VerifyChannel` + payload/result types (`UserProviderForm{ID,DisplayName,BadgeLabel,BadgeColor,Groups[],Channels[]}`, `UserGroupForm{ID,Name,Order}`, `UserChannelForm{ID,Name,URL,Kind,PlayMode,GroupID,Order}`, `UserProviderResult{Provider,ClearedSlots,AutoEnableNeeded}`, `ReorderRequest{Channels,Groups}`, `UserOrderEntry{ID,Order}`, `VerifyChannelRequest{URL,Kind}`, `VerifyChannelResult{OK,Kind,ItemCount,IsLive,Message}`). Implemented on `*streams.Adapter`; injected in `main.go:411-413` by type assertion; consumed interface-only by the chassis (`server.go:91,176,244`).
- **Catalog display path** (`internal/adapters/streams/catalog.go`): `Catalog()` emits bundled-then-user via `buildChassisCatalogProvider(def ProviderDefinition)` which sets `BadgeClass = userBadgeClass(def.BadgeColor)` = `"u-" + normalizeBadgeColorForLoad(token)` for user providers and marks `Live: providerLive || ch.Kind == kindDirect` per channel. `buildChassisCatalogProvider` builds from **definitions only** — the runtime catalog (`ProviderCatalog.Channels[].Items`) is NOT consulted, so item counts and enumeration errors are absent from the display shape.
- **Palette tokens** (`internal/adapters/streams/provider_user.go:64-98`): `badgeColorTokens` = `{amber,red,teal,blue,purple,green,cyan,slate}`, `defaultBadgeColor = "slate"`, `validateBadgeColor`, `normalizeBadgeColorForLoad`. **Kind detection** (`provider_user.go:23-62`): `kindPlaylist`/`kindSingle`/`kindDirect`, `detectChannelKind` (`.m3u8`/`.m3u`/`.mpd` → direct; YouTube `list=` → playlist; else single), `isYouTubeHost` (`youtube.com`/`m.youtube.com`/`music.youtube.com`, `www.` stripped). `isUserProviderID` checks the `user:` prefix.
- **Runtime catalog build** (`provider_user.go` `buildUserCatalog(ctx, def, enum)`): per `ChannelDefinition.Kind`, sets `ch.Items` (direct/single → 1 item; playlist → `enum.channelItems(...)` live/cached, error LOGGED ONLY and channel kept with empty/stale items). Runtime `Channel` struct (`provider.go:93-102`) has NO enumeration-state/error/count field today.
- **Chassis catalog snapshot** (`internal/chassis/data.go`): `ReceiverPageData.Catalog` (type `CatalogData`) is rebuilt every snapshot tick from `cfg.StreamsCatalogViewer.Catalog()` via `buildCatalogData`. `CatalogProviderTab{ID,DisplayName,BadgeLabel,BadgeClass,Live,ChCount,Groups[]}`, `CatalogGroupTab{ID,Name,ChCount,Channels[]}`, `CatalogChannelCard{ID,Name,PlayMode,Live,Tuned,Starred,PresetSlot}` — no URL/Kind/BadgeColor/ItemCount/EnumState.
- **SSE diff loop** (`events.go:396-590`): `handleEvents` emits an initial frame per event, then every `chassisTickInterval` (250ms) reads `s.cache.Get()` (a `ReceiverPageData`) + `s.presetSnapshot()` (a viewer), diffs each against a function-scope `last*`, and `emit(w, name, payload)`s on change. Each event = an envelope type + a `*From(...)` builder + a `*Changed(a,b)` diff + a `lastX` var. `s.refreshSnapshotNow()` (`server.go:446`) rebuilds the cache synchronously for sub-tick visibility.
- **Front-end** (`internal/chassis/static`, `…/templates`): every JS file is an IIFE. Cross-file calls go through `window.Chassis.*`: `window.Chassis.events.subscribe(eventName, handler)` (defined in `vfd-live.js`, the central SSE registry), `window.Chassis.input.showError(text)` (input-cast.js, 4s error chip), `window.Chassis.settings.showNotice(text, variant)` (settings-drawer.js, 5s toast), `window.Chassis.setupBlocked()`. Existing mutating POSTs use `fetch(url,{method,credentials:'same-origin',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:URLSearchParams})` — but the **Phase 5 catalog routes decode JSON**, so `provider-form.js` must send `Content-Type: application/json` + `JSON.stringify` (the browser's `Origin`/`Sec-Fetch-Site` satisfies `requireSameOrigin`; no custom header is needed or allowed). The catalog drawer (`catalog-drawer.html`) renders a provider tab strip (`.catalog-provider-tab[data-provider]` with `<span class="ic {{.BadgeClass}}">{{.BadgeLabel}}</span>`), and `catalog-browser.js` clones per-provider hidden `<template id="catalog-tree-{id}">` trees (`button.catalog-rail-group[data-group]`, `.catalog-tree-grid[data-group]` containing `.ch-card[data-provider][data-channel]`) on tab/group switch. Templates parse via `template.ParseFS(chassisTemplatesFS, "templates/*.html")` (`templates.go`), so a new `{{define "catalog-provider-form"}}` partial auto-registers. Script tags live in `shell.html` (`defer`, ordered). `preset-reorder.js` is a pointer-drag (not HTML5 DnD) implementation, NOT factored for reuse.
- **JS behavior tests** (`internal/chassis/testdata/*.behavior.test.js`): run via `node --test <file>` (Node 24), NOT in the Makefile / `go test` / CI. Each file defines an inline fake DOM and loads the JS-under-test with `vm.runInContext`. The `settings-link.behavior.test.js` harness (FakeNode tree with `innerHTML` serialization) is the richer template to copy.

### Phase numbering

**Phase 6 = this plan: the chassis authoring UI + palette CSS + SSE emit/render + the two minimal read seams.** Phases 1–5 (data store, security, catalog integration, playlist enumeration, routes/editor/auto-enable) are DONE and merged. Do NOT re-plan backend write paths, the `UserProviderEditor` interface, the route contracts, or auto-enable/hot-start.

---

## Scope

**In scope (Phase 6):**
- One new `adapters.UserProviderViewer` read interface (authoring-form read + per-channel enumeration status), implemented on `*streams.Adapter`, injected into the chassis by type assertion, consumed interface-only.
- An in-memory per-channel enumeration `EnumState` on the runtime streams `Channel`, set in `buildUserCatalog` (the only new backend write).
- `GET /ui/catalog/provider/{id}` chassis route returning the authoring-form JSON for the ✎ editor.
- `catalog-provider-form.html` partial (tab-strip ✎/＋New affordances live in `catalog-drawer.html`; the form body in the new partial).
- `internal/chassis/static/provider-form.js` + `internal/chassis/testdata/provider-form.behavior.test.js`: form lifecycle, syntactic kind auto-detect, glyph auto-suggest, live host allow/deny hint, Verify button (✓/✗ chips, live-vs-VOD), play-mode visibility, group dropdown, drag-reorder (via a factored engine), delete-with-cleanup confirm, POST/PUT/DELETE/GET to the Phase 5 routes, auto-enable toast, cleared-slots notice, and form-only `providerStatus` chip rendering.
- `internal/chassis/static/reorder.js` + `reorder.behavior.test.js`: a small shared pointer-drag engine; `preset-reorder.js` refactored onto it (its existing behavior test is the regression gate), reused by `provider-form.js` (spec §9 item 7 — "reuse the preset-bank drag-reorder code, not a new DnD").
- `.ic.u-<token>` / `.badge.u-<token>` palette classes (8 CRT-tuned bg/fg pairs: amber/red/teal/blue/purple/green/cyan/slate + a default slate fallback) plus form/chip/swatch CSS in `chassis.css` — class-based, CSP-safe, no inline `style=` from user data.
- `catalog` + `providerStatus` emission wired into the existing `handleEvents` diff loop.

**Out of scope (Phase 6 — note explicitly):**
- No new adapter, editor, route contract, or change to `UserProviderEditor` (Phase 5 is complete). The new `UserProviderViewer` is **additive** read-only.
- No JSON import/export, logo images, per-channel cookies/auth UI (spec §13 future work).
- No new HTTP listener, no new framework (one mux, html/template + htmx + vanilla JS).
- The SSE `providerStatus.autoEnabledStreams` field is left unused by the emitter (the toast reads the create response instead); do not invent a second auto-enable channel.

---

## Invariants this plan MUST respect (verified against the tree)

- **One HTTP listener.** The form mounts inside the existing `/ui` page + drawer; the new GET route registers on the existing chassis mux via `Mount`. No second listener.
- **Chassis imports `internal/adapters/*` interfaces only**, never the concrete `streams` package. The new viewer is injected in `main.go` by type assertion exactly like `UserProviderEditor` (`main.go:411-413`).
- **`a.mu` is never held across network I/O.** The viewer reads (`userStore.Snapshot()`, a snapshot of `a.catalogs`) take `a.mu` only to copy, then build off-lock. `EnumState` is set inside `buildUserCatalog`, which already runs off-lock before install (CLAUDE.md invariant).
- **CSP-safe rendering.** Badge/color via predefined palette CLASSES only — never user-supplied inline `style=`. Save-time validation already restricts `badgeColor` to the 8 tokens (Phase 5); the template prefers the built-in `BadgeClass` and falls back to `u-<token>`, with `.u-slate` as the default defense-in-depth class.
- **SSE is a poll-diff loop.** `catalog`/`providerStatus` emission computes a stable fingerprint and emits on change inside the existing tick loop — NOT a new push channel. Mutating routes already call `refreshSnapshotNow()`; the new GET route is read-only and does not.
- **Same-origin.** Every mutating request from `provider-form.js` carries `credentials:'same-origin'` so the browser sends `Origin`/`Sec-Fetch-Site`; do not set a custom guard header. The existing `requireSameOrigin` helper only checks unsafe methods, so the new authoring GET route (it returns authored URLs) gets its own read-specific guard that performs the same `Sec-Fetch-Site`/`Origin`/`Referer` check for GET.
- **JS scope.** `provider-form.js` is its own IIFE; all cross-file calls use `window.Chassis.*`. Behavior tests (`node --test`, Node 24) are the real gate for new JS — a `node --check` + substring test is NOT sufficient.
- **`go test -race` is CI-only** (no local cgo). Run `go vet ./...`, `go test ./...`, `go test -tags=integration ./...`, and `node --test internal/chassis/testdata/<file>` locally.
- **`docs/superpowers/` is gitignored** — commit this plan and any new docs with `git add -f`. Stage ONLY intended paths; verify `git diff --cached --name-only` before each commit.

---

## Backend read seams (design decisions — both additive, no Phase 5 contract change)

**Seam A — authoring-form read (`UserProviderForm(id)`).** The ✎ pencil opens the editor pre-populated, but the display `adapters.CatalogProvider`/`CatalogChannel` shape carries no URL/Kind/BadgeColor/Order, and `UserProviderEditor` is write-only. There is no existing read path (verified). Add `UserProviderForm(id string) (adapters.UserProviderForm, bool)` to a new `adapters.UserProviderViewer`, implemented on the adapter as the reverse of `formToDefinition` over `userStore.Snapshot()`. Exposed via `GET /ui/catalog/provider/{id}` returning camelCase JSON the form consumes. URLs never enter the 4 Hz SSE snapshot — only this read-guarded, same-origin on-demand path.

**Seam B — per-channel enumeration status (`UserProviderStatuses()`).** `channelStatusEnvelope` wants `state ∈ {ready,pending,error}` + `itemCount`. `itemCount` derives from existing `Channel.Items`, but "error" is only LOGGED today, and `Catalog()` doesn't expose runtime items at all. Minimal seam: add `EnumState string` to the runtime `Channel`, set in `buildUserCatalog` (`direct`/`single` → `"ready"`; `playlist` → `"error"` if enumeration failed with zero items, `"ready"` if ≥1 item, else `"pending"`). Add `UserProviderStatuses() []adapters.UserProviderStatus` to the **same** `UserProviderViewer`, reading a snapshot of `a.catalogs` for user providers. The chassis reads it each SSE tick (like `presetSnapshot()`) to emit `providerStatus`. This keeps enumeration status off the display catalog path (`Catalog()`/`adapters.CatalogChannel` untouched) and out of the broadcast snapshot.

Both reads live on one interface → one `main.go` type assertion, one `chassis.Config` field.

---

## File Structure

**New**
- `internal/adapters/streams/user_provider_viewer.go` — `UserProviderForm(id)` + `UserProviderStatuses()` adapter methods (form read = reverse of `formToDefinition`; status read = snapshot of `a.catalogs`).
- `internal/adapters/streams/user_provider_viewer_test.go` — viewer unit tests.
- `internal/chassis/catalog_provider_form.go` — `handleCatalogProviderForm` (GET) + `catalogProviderFormResponse` + `catalog`/`providerStatus` SSE builders/diffs (kept next to the existing envelope types).
- `internal/chassis/catalog_provider_form_test.go` — GET handler + SSE builder/diff tests.
- `internal/chassis/templates/catalog-provider-form.html` — the authoring form partial.
- `internal/chassis/static/provider-form.js` — client form logic.
- `internal/chassis/static/reorder.js` — shared pointer-drag engine.
- `internal/chassis/testdata/provider-form.behavior.test.js` — `node --test` behavior tests.
- `internal/chassis/testdata/reorder.behavior.test.js` — engine behavior tests.
- `internal/chassis/testdata/catalog-browser.behavior.test.js` — catalog-rebuild node-builder tests.

**Modified**
- `internal/adapters/streams/provider.go` — `Channel.EnumState string`.
- `internal/adapters/streams/provider_user.go` — set `EnumState` in `buildUserCatalog`.
- `internal/adapters/streams/provider_user_test.go` (or `playlist_enum_test.go`) — `EnumState` assertions.
- `internal/adapters/user_provider.go` — `UserProviderViewer` interface + `UserProviderStatus`/`UserChannelStatus` types.
- `internal/adapters/user_provider_test.go` — viewer type-shape test.
- `cmd/mister-groovy-relay/main.go` — type-assert + inject `UserProviderViewer`.
- `internal/chassis/server.go` — `Config.UserProviderViewer` + `Server.userProviderViewer` field + `New` wiring + `Mount` GET route + `userProviderStatusSnapshot()` helper.
- `internal/chassis/events.go` — `catalog` + `providerStatus` initial emit + tick-loop diff.
- `internal/chassis/data.go` — (none required for status; `CatalogData` stays display-only). Touch only if a render test needs it.
- `internal/chassis/templates/catalog-drawer.html` — ✎ pencil on user tabs + dashed ＋New tab + include the form partial.
- `internal/chassis/templates/shell.html` — `<script>` includes for `reorder.js` + `provider-form.js`.
- `internal/chassis/static/preset-reorder.js` — refactor onto `reorder.js`.
- `internal/chassis/static/catalog-browser.js` — `catalog` SSE rebuild (tabs + hidden trees); cache presets/tuned payloads.
- `internal/chassis/static/chassis.css` — palette classes + form/chip/swatch styling.

---

## Task 1: Runtime per-channel `EnumState` (seam B origin)

Add the enumeration-state marker the `providerStatus` chips need. `direct`/`single` channels are always `ready`; `playlist` channels become `error` (failed with no items), `ready` (≥1 item), or `pending` (no items yet, no error).

**Files:**
- Modify: `internal/adapters/streams/provider.go` (the runtime `Channel` struct, line 93-102)
- Modify: `internal/adapters/streams/provider_user.go` (`buildUserCatalog`, ~line 309)
- Test: `internal/adapters/streams/provider_user_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/provider_user_test.go` (it is already `package streams` and uses the `fakeResolver`/`ytEntries` helpers from `playlist_enum_test.go`). **Note on the fake:** the merged `fakeResolver` (`test_helpers_test.go:273-318`) carries a SINGLE `enumErr` applied to all URLs and returns `(nil, nil)` for an unmapped URL — there is no per-URL error map. So the three states are exercised with three separate enumerators (matching the existing pattern at `playlist_enum_test.go:190`), not one mixed resolver:

```go
func TestBuildUserCatalog_SetsEnumState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"

	// Build A: direct + single + a playlist that enumerates → all "ready".
	defA := ProviderDefinition{
		Type: userProviderType, ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []ChannelDefinition{
			{ID: "live", Name: "Live", URL: "https://cdn.example.com/s.m3u8", Kind: kindDirect},
			{ID: "vod", Name: "VOD", URL: "https://twitch.tv/foo", Kind: kindSingle},
			{ID: "list", Name: "List", URL: playlistURL, Kind: kindPlaylist},
		},
	}
	enumReady := userPlaylistEnumerator{
		resolver: &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{playlistURL: ytEntries("a", "b")}},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	catA, err := buildUserCatalog(context.Background(), defA, enumReady)
	if err != nil {
		t.Fatalf("buildUserCatalog A: %v", err)
	}
	wantA := map[string]string{"live": "ready", "vod": "ready", "list": "ready"}
	for _, ch := range catA.Channels {
		if ch.EnumState != wantA[ch.ID] {
			t.Fatalf("A channel %q EnumState = %q, want %q", ch.ID, ch.EnumState, wantA[ch.ID])
		}
	}

	// Build B: a playlist whose enumeration errors with no cached items → "error".
	defB := ProviderDefinition{
		Type: userProviderType, ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []ChannelDefinition{{ID: "dead", Name: "Dead", URL: "https://www.youtube.com/playlist?list=PLX", Kind: kindPlaylist}},
	}
	enumErr := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("private playlist")},
		cacheDir: t.TempDir(), cfg: DefaultConfig(),
	}
	catB, err := buildUserCatalog(context.Background(), defB, enumErr)
	if err != nil {
		t.Fatalf("buildUserCatalog B: %v", err)
	}
	if catB.Channels[0].EnumState != "error" {
		t.Fatalf("B dead EnumState = %q, want error", catB.Channels[0].EnumState)
	}

	// Build C: a playlist with a CACHE-ONLY enumerator (resolver nil) and an
	// empty cache → no items, no error → "pending".
	enumPending := userPlaylistEnumerator{cacheDir: t.TempDir(), cfg: DefaultConfig()}
	catC, err := buildUserCatalog(context.Background(), defB, enumPending)
	if err != nil {
		t.Fatalf("buildUserCatalog C: %v", err)
	}
	if catC.Channels[0].EnumState != "pending" {
		t.Fatalf("C dead EnumState = %q, want pending", catC.Channels[0].EnumState)
	}
}
```

`provider_user_test.go` needs `"fmt"` and the `ytdlp` import (`github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp`) — add any missing. `DefaultConfig` is confirmed exported (`config.go:88`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestBuildUserCatalog_SetsEnumState`
Expected: BUILD FAIL — the references to `ch.EnumState`, `enumStateReady`/`enumStateError`/`enumStatePending`, and `playlistEnumState` are all undefined until Step 3 (a build error, printed as a `# package` compile failure with no `FAIL:` line, not a clean test failure).

- [ ] **Step 3: Add the field and set it**

In `internal/adapters/streams/provider.go`, add one field to the runtime `Channel` struct (after `Order int`, line 101):

```go
type Channel struct {
	ID          string
	Name        string
	Description string
	GroupID     string
	Icon        string
	Items       []StreamItem
	PlayMode    PlayMode
	Order       int
	// EnumState is the per-channel enumeration status surfaced to the chassis
	// providerStatus SSE event (spec §6/§8): "ready" | "pending" | "error".
	// Empty for non-user channels. Set by buildUserCatalog. In-memory only —
	// never persisted to user_providers.json.
	EnumState string
}
```

In `internal/adapters/streams/provider_user.go`, set `EnumState` inside `buildUserCatalog`'s per-kind switch. The `direct`/`single` arms set `ch.EnumState = enumStateReady`; the `playlist` arm sets it from the enumeration outcome. Add the constants and the helper near the kind constants (line 23-28), then update the switch:

```go
const (
	enumStateReady   = "ready"
	enumStatePending = "pending"
	enumStateError   = "error"
)

// playlistEnumState classifies a playlist enumeration outcome for the
// providerStatus chips. A hard failure with no items is "error"; any items
// (fresh or serve-stale) is "ready"; an empty, error-free result is "pending"
// (e.g. a cache-only startup snapshot before the first live refresh, spec §6).
func playlistEnumState(items []StreamItem, err error) string {
	if len(items) > 0 {
		return enumStateReady
	}
	if err != nil {
		return enumStateError
	}
	return enumStatePending
}
```

In the `buildUserCatalog` switch, set the state in each arm (the surrounding code is unchanged):

```go
		case kindDirect:
			ch.Items = []StreamItem{{ID: ch.ID, Title: ch.Name, URL: chDef.URL, SourceID: ch.ID, Direct: true}}
			ch.EnumState = enumStateReady
		case kindSingle:
			ch.Items = []StreamItem{{ID: ch.ID, Title: ch.Name, URL: chDef.URL, SourceID: ch.ID, Direct: false}}
			ch.EnumState = enumStateReady
		case kindPlaylist:
			items, err := enum.channelItems(ctx, def.ID, ch.ID, chDef.URL)
			if err != nil {
				slog.Warn("streams user provider: playlist enumeration error (channel kept, may be empty or stale)",
					"provider", def.ID, "channel", ch.ID, "err", playlistErrorForLog(err, chDef.URL))
			}
			ch.Items = items
			ch.EnumState = playlistEnumState(items, err)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestBuildUserCatalog_SetsEnumState`
Then the package: `go test ./internal/adapters/streams/`
Expected: PASS (existing tests unaffected — the field is additive and in-memory).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/provider.go internal/adapters/streams/provider_user.go internal/adapters/streams/provider_user_test.go
git commit -m "feat(streams): per-channel EnumState (ready/pending/error) on runtime Channel"
```

---

## Task 2: `UserProviderViewer` interface + adapter implementation (seams A + B)

One read interface: `UserProviderForm(id)` (authoring read) + `UserProviderStatuses()` (enumeration status). Implemented on `*streams.Adapter`; injected in `main.go` and `chassis.Config` like `UserProviderEditor`.

**Files:**
- Modify: `internal/adapters/user_provider.go` (append interface + status types)
- Test: `internal/adapters/user_provider_test.go` (append type-shape assertions)
- Create: `internal/adapters/streams/user_provider_viewer.go`
- Create: `internal/adapters/streams/user_provider_viewer_test.go`

- [ ] **Step 1: Write the failing test (shared package — type shape)**

Append to `internal/adapters/user_provider_test.go`:

```go
func TestUserProviderViewer_TypeShape(t *testing.T) {
	t.Parallel()
	var _ UserProviderViewer = stubViewer{}
	st := UserProviderStatus{ProviderID: "user:mix", Channels: []UserChannelStatus{{ChannelID: "a", State: "ready", ItemCount: 3}}}
	if st.Channels[0].State != "ready" || st.Channels[0].ItemCount != 3 {
		t.Fatal("status shape wrong")
	}
}

type stubViewer struct{}

func (stubViewer) UserProviderForm(string) (UserProviderForm, bool) { return UserProviderForm{}, false }
func (stubViewer) UserProviderStatuses() []UserProviderStatus       { return nil }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/ -run TestUserProviderViewer_TypeShape`
Expected: FAIL — `undefined: UserProviderViewer` / `UserProviderStatus`.

- [ ] **Step 3: Add the interface + types**

Append to `internal/adapters/user_provider.go`:

```go
// UserProviderViewer is the read-only companion to UserProviderEditor (Phase 6,
// additive — it does NOT change the editor contract). The chassis consumes it
// interface-only; the streams adapter implements it and main.go injects it by
// type assertion. It serves two reads the display catalog cannot:
//   - UserProviderForm: the authoring-shape definition (URL/Kind/BadgeColor/
//     Order per channel) for the ✎ edit form. The display CatalogProvider has
//     none of these.
//   - UserProviderStatuses: per-channel enumeration status for the
//     providerStatus SSE chips ("ready"/"pending"/"error" + item count).
type UserProviderViewer interface {
	UserProviderForm(id string) (UserProviderForm, bool)
	UserProviderStatuses() []UserProviderStatus
}

// UserProviderStatus is one user provider's per-channel enumeration status.
type UserProviderStatus struct {
	ProviderID string
	Channels   []UserChannelStatus
}

// UserChannelStatus is one channel's enumeration status (spec §6/§8).
type UserChannelStatus struct {
	ChannelID string
	State     string // "ready" | "pending" | "error"
	ItemCount int
}
```

- [ ] **Step 4: Write the failing adapter test**

Create `internal/adapters/streams/user_provider_viewer_test.go`:

```go
package streams

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func newViewerAdapter(t *testing.T, fr *fakeResolver) *Adapter {
	t.Helper()
	dir := t.TempDir()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.resolver = fr
	a.cacheDir = dir
	return a
}

func TestUserProviderForm_RoundTripsAuthoringFields(t *testing.T) {
	t.Parallel()
	a := newViewerAdapter(t, &fakeResolver{})
	saved, err := a.userStore.Put(ProviderDefinition{
		Type: userProviderType, DisplayName: "F1 TV", BadgeLabel: "F1", BadgeColor: "amber",
		Groups: []GroupDefinition{{ID: "races", Name: "Races", Order: 0}},
		Channels: []ChannelDefinition{
			{Name: "Live", URL: "https://cdn.example.com/live.m3u8", Kind: kindDirect, GroupID: "races", Order: 0},
		},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	form, ok := a.UserProviderForm(saved.ID)
	if !ok {
		t.Fatalf("UserProviderForm(%q) not found", saved.ID)
	}
	if form.ID != saved.ID || form.DisplayName != "F1 TV" || form.BadgeColor != "amber" {
		t.Fatalf("identity round-trip wrong: %+v", form)
	}
	if len(form.Groups) != 1 || form.Groups[0].ID != "races" {
		t.Fatalf("groups round-trip wrong: %+v", form.Groups)
	}
	if len(form.Channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(form.Channels))
	}
	c := form.Channels[0]
	if c.URL != "https://cdn.example.com/live.m3u8" || c.Kind != kindDirect || c.GroupID != "races" || c.ID == "" {
		t.Fatalf("channel round-trip wrong: %+v", c)
	}
	// A bundled (non-user) ID is not found.
	if _, ok := a.UserProviderForm("mtv-rewind"); ok {
		t.Fatal("bundled ID unexpectedly returned a form")
	}
}

func TestUserProviderStatuses_ReportsPerChannelState(t *testing.T) {
	t.Parallel()
	playlistURL := "https://www.youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{playlistURL: ytEntries("a", "b", "c")}}
	a := newViewerAdapter(t, fr)
	if _, err := a.userStore.Put(ProviderDefinition{
		Type: userProviderType, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []ChannelDefinition{
			{Name: "Live", URL: "https://cdn.example.com/s.m3u8", Kind: kindDirect},
			{Name: "List", URL: playlistURL, Kind: kindPlaylist},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Build + install the live catalog (no rebuildUserCatalogsLive convenience
	// wrapper exists in the merged tree — use the two merged Phase 5 methods,
	// exactly as user_provider_editor_test.go:52-54 does).
	snapshot, err := a.buildUserCatalogSnapshotLive(context.Background(), a.userStore.Snapshot())
	if err != nil {
		t.Fatalf("buildUserCatalogSnapshotLive: %v", err)
	}
	a.installUserCatalogSnapshot(snapshot)
	statuses := a.UserProviderStatuses()
	if len(statuses) != 1 || statuses[0].ProviderID != "user:mix" {
		t.Fatalf("statuses = %+v", statuses)
	}
	byID := map[string]adapters.UserChannelStatus{}
	for _, c := range statuses[0].Channels {
		byID[c.ChannelID] = c
	}
	if byID["live"].State != "ready" || byID["live"].ItemCount != 1 {
		t.Fatalf("live status = %+v", byID["live"])
	}
	if byID["list"].State != "ready" || byID["list"].ItemCount != 3 {
		t.Fatalf("list status = %+v", byID["list"])
	}
}
```

(`buildUserCatalogSnapshotLive`/`installUserCatalogSnapshot`/`userStore.Put` are merged Phase 3/5 methods. Channel IDs `live`/`list` are the `newChannelID` slugs of "Live"/"List"; provider ID `user:mix` is `newUserProviderID("Mix")`. These slugs are deterministic — no need to re-confirm.)

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestUserProviderForm_RoundTrips|TestUserProviderStatuses_'`
Expected: FAIL — `a.UserProviderForm undefined` / `a.UserProviderStatuses undefined`.

- [ ] **Step 6: Implement the viewer**

Create `internal/adapters/streams/user_provider_viewer.go`:

```go
package streams

import (
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// UserProviderForm returns the authoring-shape definition for a user provider
// (spec §8 ✎ edit). It is the reverse of formToDefinition: it maps the stored
// ProviderDefinition back to the chassis form payload, including per-channel
// URL/Kind/BadgeColor/Order the display CatalogProvider omits. Returns
// (zero, false) for unknown or non-user IDs.
func (a *Adapter) UserProviderForm(id string) (adapters.UserProviderForm, bool) {
	if a.userStore == nil || !isUserProviderID(id) {
		return adapters.UserProviderForm{}, false
	}
	for _, def := range a.userStore.Snapshot() {
		if def.ID != id {
			continue
		}
		groups := make([]adapters.UserGroupForm, 0, len(def.Groups))
		for _, g := range def.Groups {
			groups = append(groups, adapters.UserGroupForm{ID: g.ID, Name: g.Name, Order: g.Order})
		}
		channels := make([]adapters.UserChannelForm, 0, len(def.Channels))
		for _, c := range def.Channels {
			channels = append(channels, adapters.UserChannelForm{
				ID: c.ID, Name: c.Name, URL: c.URL, Kind: c.Kind,
				PlayMode: string(c.PlayMode), GroupID: c.GroupID, Order: c.Order,
			})
		}
		return adapters.UserProviderForm{
			ID: def.ID, DisplayName: def.DisplayName, BadgeLabel: def.BadgeLabel,
			BadgeColor: def.BadgeColor, Groups: groups, Channels: channels,
		}, true
	}
	return adapters.UserProviderForm{}, false
}

// UserProviderStatuses returns per-channel enumeration status for every user
// provider (spec §6/§8). Reads a snapshot of the installed catalogs under a.mu,
// then builds off-lock. ItemCount derives from the runtime Channel.Items;
// State is the per-channel EnumState set by buildUserCatalog.
func (a *Adapter) UserProviderStatuses() []adapters.UserProviderStatus {
	a.mu.Lock()
	type chanRow struct {
		id, state string
		count     int
	}
	rows := map[string][]chanRow{}
	order := make([]string, 0)
	for _, id := range a.definitionOrder {
		if !isUserProviderID(id) {
			continue
		}
		cat, ok := a.catalogs[id]
		if !ok {
			continue
		}
		order = append(order, id)
		list := make([]chanRow, 0, len(cat.Channels))
		for _, ch := range cat.Channels {
			list = append(list, chanRow{id: ch.ID, state: ch.EnumState, count: len(ch.Items)})
		}
		rows[id] = list
	}
	a.mu.Unlock()

	out := make([]adapters.UserProviderStatus, 0, len(order))
	for _, id := range order {
		st := adapters.UserProviderStatus{ProviderID: id}
		for _, r := range rows[id] {
			state := r.state
			if state == "" {
				state = enumStateReady // defensive: pre-Task-1 catalogs read as ready
			}
			st.Channels = append(st.Channels, adapters.UserChannelStatus{ChannelID: r.id, State: state, ItemCount: r.count})
		}
		out = append(out, st)
	}
	return out
}
```

Confirm the adapter field names against `adapter.go`: `a.catalogs map[string]ProviderCatalog`, `a.definitionOrder []string`, `a.userStore *userProviderStore`, `a.mu sync.Mutex`. Adjust the snapshot accessor if `a.catalogs` is keyed/typed differently (`grep -n "catalogs \|definitionOrder \|userStore " internal/adapters/streams/adapter.go`).

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/adapters/ ./internal/adapters/streams/ -run 'UserProviderViewer|UserProviderForm_RoundTrips|UserProviderStatuses_'`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/adapters/user_provider.go internal/adapters/user_provider_test.go internal/adapters/streams/user_provider_viewer.go internal/adapters/streams/user_provider_viewer_test.go
git commit -m "feat(adapters): UserProviderViewer (authoring-form read + per-channel enum status)"
```

---

## Task 3: Inject `UserProviderViewer` into the chassis

Wire the viewer from `main.go` (type assertion) into `chassis.Config`/`Server`, mirroring `UserProviderEditor`.

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go` (lines 391-414, 467-491)
- Modify: `internal/chassis/server.go` (`Config` ~line 91, `Server` ~line 176, `New` ~line 244)
- Test: `internal/chassis/server_test.go` (or the existing field-wiring test)

- [ ] **Step 1: Write the failing test**

Append a wiring assertion to the existing chassis server/wiring test (find it with `grep -rln "chassis.Config{" internal/chassis/*_test.go`; if none, create `internal/chassis/catalog_provider_form_test.go` now and put it there):

```go
func TestNew_WiresUserProviderViewer(t *testing.T) {
	t.Parallel()
	v := stubUserProviderViewer{}
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: v})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.userProviderViewer == nil {
		t.Fatal("userProviderViewer not wired")
	}
}

type stubUserProviderViewer struct{}

func (stubUserProviderViewer) UserProviderForm(string) (adapters.UserProviderForm, bool) {
	return adapters.UserProviderForm{}, false
}
func (stubUserProviderViewer) UserProviderStatuses() []adapters.UserProviderStatus { return nil }
```

Ensure the test file imports `"time"` and `"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestNew_WiresUserProviderViewer`
Expected: FAIL — `unknown field UserProviderViewer` / `s.userProviderViewer undefined`.

- [ ] **Step 3: Add the Config + Server fields and wire `New`**

In `internal/chassis/server.go`, add to `Config` (after `EnsureAdapterStarted`, ~line 95):

```go
	// UserProviderViewer backs the ✎ edit form read (GET …/provider/{id}) and
	// the providerStatus SSE chips. Read-only companion to UserProviderEditor;
	// interface-only, injected by type assertion in main.go. Nil in tests that
	// don't exercise the form read.
	UserProviderViewer adapters.UserProviderViewer
```

Add to `Server` (after `userProviderEditor`, ~line 176):

```go
	userProviderViewer adapters.UserProviderViewer
```

Wire it in `New` (after `userProviderEditor: cfg.UserProviderEditor,`, ~line 244):

```go
		userProviderViewer:   cfg.UserProviderViewer,
```

- [ ] **Step 4: Wire the injection in `main.go`**

In `cmd/mister-groovy-relay/main.go`, add the declaration beside `userProviderEditor` (line 394):

```go
	var userProviderViewer adapters.UserProviderViewer
```

Add the type assertion inside the `if streamsA, ok := reg.Get("streams"); ok {` block (after the `UserProviderEditor` assertion, line 413):

```go
		if v, ok := streamsA.(adapters.UserProviderViewer); ok {
			userProviderViewer = v
		}
```

Add the field to the `chassis.Config{...}` literal (after `UserProviderEditor: userProviderEditor,`, line 490):

```go
		UserProviderViewer:        userProviderViewer,
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run TestNew_WiresUserProviderViewer`
Then build the binary: `go build ./cmd/mister-groovy-relay`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/server.go cmd/mister-groovy-relay/main.go internal/chassis/catalog_provider_form_test.go
git commit -m "feat(chassis): inject UserProviderViewer (interface-only, like UserProviderEditor)"
```

---

## Task 4: `GET /ui/catalog/provider/{id}` — authoring-form read

The ✎ editor fetches this on open. Returns the authoring-shape JSON (camelCase, matching the request body so the form maps it symmetrically).

**Files:**
- Create: `internal/chassis/catalog_provider_form.go`
- Test: `internal/chassis/catalog_provider_form_test.go`
- Modify: `internal/chassis/server.go` (`Mount`, after line 358)

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/catalog_provider_form_test.go`:

```go
func TestHandleCatalogProviderForm_ReturnsAuthoringJSON(t *testing.T) {
	t.Parallel()
	v := formViewer{form: adapters.UserProviderForm{
		ID: "user:f1-tv", DisplayName: "F1 TV", BadgeLabel: "F1", BadgeColor: "amber",
		Groups: []adapters.UserGroupForm{{ID: "races", Name: "Races", Order: 0}},
		Channels: []adapters.UserChannelForm{
			{ID: "live", Name: "Live", URL: "https://cdn.example.com/live.m3u8", Kind: "direct", GroupID: "races", Order: 0},
		},
	}}
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: v})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:f1-tv", nil)
	req.SetPathValue("id", "user:f1-tv")
	rec := httptest.NewRecorder()
	s.handleCatalogProviderForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got catalogProviderFormResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK || got.ID != "user:f1-tv" || got.BadgeColor != "amber" {
		t.Fatalf("identity wrong: %+v", got)
	}
	if len(got.Channels) != 1 || got.Channels[0].URL != "https://cdn.example.com/live.m3u8" || got.Channels[0].Kind != "direct" {
		t.Fatalf("channels wrong: %+v", got.Channels)
	}
}

func TestHandleCatalogProviderForm_NotFound(t *testing.T) {
	t.Parallel()
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: formViewer{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:ghost", nil)
	req.SetPathValue("id", "user:ghost")
	rec := httptest.NewRecorder()
	s.handleCatalogProviderForm(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCatalogProviderFormRoute_MountedAndReadGuarded(t *testing.T) {
	t.Parallel()
	v := formViewer{form: adapters.UserProviderForm{ID: "user:f1-tv", DisplayName: "F1 TV", BadgeLabel: "F1", BadgeColor: "amber"}}
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: v})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", rec.Code)
	}
}

type formViewer struct {
	form adapters.UserProviderForm
}

func (v formViewer) UserProviderForm(id string) (adapters.UserProviderForm, bool) {
	if v.form.ID == id {
		return v.form, true
	}
	return adapters.UserProviderForm{}, false
}
func (formViewer) UserProviderStatuses() []adapters.UserProviderStatus { return nil }
```

(`formViewer` returns its form only when the requested ID matches, so the not-found test passes with a zero-value viewer. The mounted-route test catches stale route prefixes and proves the read-specific GET guard actually runs.) Add imports: `"encoding/json"`, `"net/http"`, `"net/http/httptest"`, `"time"`, the `adapters` package.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestHandleCatalogProviderForm`
Expected: FAIL — `s.handleCatalogProviderForm undefined` / `catalogProviderFormResponse undefined`.

- [ ] **Step 3: Implement the handler**

Create `internal/chassis/catalog_provider_form.go`:

```go
package chassis

import (
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// catalogProviderFormResponse is the GET …/provider/{id} body: the authoring
// form for the ✎ editor. Field tags mirror catalogProviderRequest so the
// client maps the read and the write symmetrically.
type catalogProviderFormResponse struct {
	OK          bool                    `json:"ok"`
	ID          string                  `json:"id"`
	DisplayName string                  `json:"displayName"`
	BadgeLabel  string                  `json:"badgeLabel"`
	BadgeColor  string                  `json:"badgeColor"`
	Groups      []catalogGroupRequest   `json:"groups"`
	Channels    []catalogChannelRequest `json:"channels"`
}

func catalogProviderFormResponseFrom(form adapters.UserProviderForm) catalogProviderFormResponse {
	groups := make([]catalogGroupRequest, 0, len(form.Groups))
	for _, g := range form.Groups {
		groups = append(groups, catalogGroupRequest{ID: g.ID, Name: g.Name, Order: g.Order})
	}
	channels := make([]catalogChannelRequest, 0, len(form.Channels))
	for _, c := range form.Channels {
		channels = append(channels, catalogChannelRequest{
			ID: c.ID, Name: c.Name, URL: c.URL, Kind: c.Kind,
			PlayMode: c.PlayMode, GroupID: c.GroupID, Order: c.Order,
		})
	}
	return catalogProviderFormResponse{
		OK: true, ID: form.ID, DisplayName: form.DisplayName, BadgeLabel: form.BadgeLabel,
		BadgeColor: form.BadgeColor, Groups: groups, Channels: channels,
	}
}

// requireSameOriginRead guards a read endpoint that returns authored URLs.
// requireSameOrigin only checks unsafe methods, so this helper applies the same
// browser signal policy to GET: allow same-origin/same-site, otherwise require a
// matching Origin or Referer when Sec-Fetch-Site is absent (older clients).
func requireSameOriginRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r)
	})
}

// handleCatalogProviderForm serves the authoring form for one user provider so
// the ✎ editor opens pre-populated. Read-only; the mounted route wraps it with
// requireSameOriginRead because it returns authored URLs. 404 when the viewer is
// unwired or the id is unknown/non-user.
func (s *Server) handleCatalogProviderForm(w http.ResponseWriter, r *http.Request) {
	if s.userProviderViewer == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	form, ok := s.userProviderViewer.UserProviderForm(r.PathValue("id"))
	if !ok {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	writeJSON(w, http.StatusOK, catalogProviderFormResponseFrom(form))
}
```

- [ ] **Step 4: Register the route**

In `internal/chassis/server.go` `Mount`, add after the verify route (line 358):

```go
	mux.Handle("GET /ui/catalog/provider/{id}", requireSameOriginRead(http.HandlerFunc(s.handleCatalogProviderForm)))
```

(Go's `net/http` mux distinguishes by method, so `GET …/provider/{id}` and `PUT/DELETE …/provider/{id}` coexist.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run TestHandleCatalogProviderForm`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/catalog_provider_form.go internal/chassis/catalog_provider_form_test.go internal/chassis/server.go
git commit -m "feat(chassis): GET /ui/catalog/provider/{id} authoring-form read"
```

---

## Task 5: Emit the `catalog` SSE event

Wire `catalog` into the `handleEvents` diff loop, built from `ReceiverPageData.Catalog` (already in the snapshot). Lets an open drawer rebuild its tab strip + tree templates when a provider is added/edited/deleted/reordered without a reload.

**Files:**
- Modify: `internal/chassis/catalog_provider_form.go` (add the builder + diff next to the envelope types)
- Modify: `internal/chassis/events.go` (`handleEvents` initial emit + tick diff)
- Test: `internal/chassis/catalog_provider_form_test.go`, `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/catalog_provider_form_test.go`:

```go
func TestCatalogEnvelopeFromData_And_Changed(t *testing.T) {
	t.Parallel()
	data := CatalogData{Providers: []CatalogProviderTab{
		{ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal", Live: false,
			Groups: []CatalogGroupTab{{ID: "", Name: "", Channels: []CatalogChannelCard{
				{ID: "live", Name: "Live", PlayMode: "", Live: true},
			}}}},
	}}
	env := catalogEnvelopeFromData(data)
	if len(env.Providers) != 1 || env.Providers[0].ID != "user:mix" || env.Providers[0].BadgeClass != "u-teal" {
		t.Fatalf("provider envelope wrong: %+v", env.Providers)
	}
	if len(env.Providers[0].Groups[0].Channels) != 1 || !env.Providers[0].Groups[0].Channels[0].Live {
		t.Fatalf("channel envelope wrong: %+v", env.Providers[0].Groups)
	}
	if catalogChanged(env, env) {
		t.Fatal("identical envelopes reported changed")
	}
	next := catalogEnvelopeFromData(data)
	next.Providers[0].DisplayName = "Mixed"
	if !catalogChanged(env, next) {
		t.Fatal("display-name change not detected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestCatalogEnvelopeFromData_And_Changed`
Expected: FAIL — `catalogEnvelopeFromData undefined` / `catalogChanged undefined`.

- [ ] **Step 3: Add the builder + diff**

Append to `internal/chassis/catalog_provider_form.go`:

```go
// catalogEnvelopeFromData projects the chassis CatalogData (already in the
// snapshot) into the wire `catalog` envelope. Star/lit/tuned overlays are NOT
// included — the client applies those from the presets/transport events, the
// same split catalog-browser.js already uses.
func catalogEnvelopeFromData(d CatalogData) catalogEnvelope {
	providers := make([]catalogProviderEnvelope, 0, len(d.Providers))
	for _, p := range d.Providers {
		groups := make([]catalogGroupEnvelope, 0, len(p.Groups))
		for _, g := range p.Groups {
			chans := make([]catalogChannelEnvelope, 0, len(g.Channels))
			for _, c := range g.Channels {
				chans = append(chans, catalogChannelEnvelope{ID: c.ID, Name: c.Name, PlayMode: c.PlayMode, Live: c.Live})
			}
			groups = append(groups, catalogGroupEnvelope{ID: g.ID, Name: g.Name, Channels: chans})
		}
		providers = append(providers, catalogProviderEnvelope{
			ID: p.ID, DisplayName: p.DisplayName, BadgeLabel: p.BadgeLabel,
			BadgeClass: p.BadgeClass, Live: p.Live, Groups: groups,
		})
	}
	return catalogEnvelope{Providers: providers}
}

// catalogChanged diffs the structural catalog (ids/names/badges/groups/
// channels). Excludes star/lit state (owned by other events).
func catalogChanged(a, b catalogEnvelope) bool {
	if len(a.Providers) != len(b.Providers) {
		return true
	}
	for i := range a.Providers {
		pa, pb := a.Providers[i], b.Providers[i]
		if pa.ID != pb.ID || pa.DisplayName != pb.DisplayName || pa.BadgeLabel != pb.BadgeLabel ||
			pa.BadgeClass != pb.BadgeClass || pa.Live != pb.Live || len(pa.Groups) != len(pb.Groups) {
			return true
		}
		for j := range pa.Groups {
			ga, gb := pa.Groups[j], pb.Groups[j]
			if ga.ID != gb.ID || ga.Name != gb.Name || len(ga.Channels) != len(gb.Channels) {
				return true
			}
			for k := range ga.Channels {
				if ga.Channels[k] != gb.Channels[k] {
					return true
				}
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Emit in `handleEvents`**

In `internal/chassis/events.go`, add the initial emit after the `history` initial emit (after line 451, where `lastHistory` is declared):

```go
	lastCatalog := catalogEnvelopeFromData(last.Catalog)
	if err := emit(w, "catalog", lastCatalog); err != nil {
		return
	}
```

Add the tick-loop diff inside the `case <-tick.C:` block, after the `history` diff arm (after line 571, before the `audioDSPChanged` arm):

```go
				currCatalog := catalogEnvelopeFromData(curr.Catalog)
				if catalogChanged(lastCatalog, currCatalog) {
					if err := emit(w, "catalog", currCatalog); err != nil {
						return
					}
					lastCatalog = currCatalog
				}
```

(`curr` is `s.cache.Get()`; `curr.Catalog` is the live `CatalogData`. `lastCatalog` lives at function scope beside `lastHistory`.)

- [ ] **Step 5: Update the existing SSE event-order test**

Update `internal/chassis/events_test.go`'s exact initial-event list to include the new `catalog` event in the same position as the implementation (after `history`). This catches stale event-order expectations that a standalone catalog builder test would miss.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run 'TestCatalogEnvelopeFromData_And_Changed'`
Then the events tests: `go test ./internal/chassis/ -run TestHandleEvents`
Expected: PASS (existing SSE tests still green; the new event is additive).

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/catalog_provider_form.go internal/chassis/events.go internal/chassis/catalog_provider_form_test.go internal/chassis/events_test.go
git commit -m "feat(chassis): emit catalog SSE event from the snapshot diff loop"
```

---

## Task 6: Emit the `providerStatus` SSE event

Per-user-provider per-channel enumeration status. Read from `UserProviderViewer.UserProviderStatuses()` each tick (like `presetSnapshot()`), diff per provider via a fingerprint, emit changed providers. The `autoEnabledStreams` field stays empty (the toast reads the create response).

**Files:**
- Modify: `internal/chassis/catalog_provider_form.go` (builder + fingerprint)
- Modify: `internal/chassis/server.go` (`userProviderStatusSnapshot()` helper)
- Modify: `internal/chassis/events.go` (initial emit + tick diff)
- Test: `internal/chassis/catalog_provider_form_test.go`, `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/catalog_provider_form_test.go`:

```go
func TestProviderStatusEnvelopes_And_Fingerprint(t *testing.T) {
	t.Parallel()
	in := []adapters.UserProviderStatus{{
		ProviderID: "user:mix",
		Channels: []adapters.UserChannelStatus{
			{ChannelID: "live", State: "ready", ItemCount: 1},
			{ChannelID: "list", State: "pending", ItemCount: 0},
		},
	}}
	envs := providerStatusEnvelopesFrom(in)
	if len(envs) != 1 || envs[0].Provider != "user:mix" || len(envs[0].Channels) != 2 {
		t.Fatalf("envelopes wrong: %+v", envs)
	}
	if envs[0].Channels[1].State != "pending" || envs[0].AutoEnabledStreams != "" {
		t.Fatalf("channel/auto-enable wrong: %+v", envs[0])
	}
	fp1 := providerStatusFingerprint(envs[0])
	envs2 := providerStatusEnvelopesFrom(in)
	if providerStatusFingerprint(envs2[0]) != fp1 {
		t.Fatal("fingerprint not stable")
	}
	envs2[0].Channels[1].State = "ready"
	if providerStatusFingerprint(envs2[0]) == fp1 {
		t.Fatal("fingerprint did not change on state change")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestProviderStatusEnvelopes_And_Fingerprint`
Expected: FAIL — `providerStatusEnvelopesFrom undefined` / `providerStatusFingerprint undefined`.

- [ ] **Step 3: Add the builder + fingerprint**

Append to `internal/chassis/catalog_provider_form.go` (add `"fmt"` and `"strings"` to the import block):

```go
// providerStatusEnvelopesFrom projects the adapter's per-channel enumeration
// status into one providerStatus envelope per user provider (spec §6/§8).
// AutoEnabledStreams is deliberately left empty — the auto-enable toast reads
// the create route's HTTP response, not this stream.
func providerStatusEnvelopesFrom(in []adapters.UserProviderStatus) []providerStatusEnvelope {
	out := make([]providerStatusEnvelope, 0, len(in))
	for _, p := range in {
		chans := make([]channelStatusEnvelope, 0, len(p.Channels))
		for _, c := range p.Channels {
			chans = append(chans, channelStatusEnvelope{Channel: c.ChannelID, State: c.State, ItemCount: c.ItemCount})
		}
		out = append(out, providerStatusEnvelope{Provider: p.ProviderID, Channels: chans})
	}
	return out
}

// providerStatusFingerprint is a stable per-provider change key for the SSE
// diff: provider id + each channel's id/state/count. Cheap string compare, no
// reflection (matches the explicit-diff idiom in events.go).
func providerStatusFingerprint(p providerStatusEnvelope) string {
	var b strings.Builder
	b.WriteString(p.Provider)
	for _, c := range p.Channels {
		fmt.Fprintf(&b, "|%s:%s:%d", c.Channel, c.State, c.ItemCount)
	}
	return b.String()
}
```

In `internal/chassis/server.go`, add the snapshot helper next to `presetSnapshot` (after line 462):

```go
// userProviderStatusSnapshot returns per-channel enumeration status for the
// providerStatus SSE event, or nil when no viewer is wired. Read once per SSE
// tick, like presetSnapshot.
func (s *Server) userProviderStatusSnapshot() []adapters.UserProviderStatus {
	if s.userProviderViewer == nil {
		return nil
	}
	return s.userProviderViewer.UserProviderStatuses()
}
```

- [ ] **Step 4: Emit in `handleEvents`**

In `internal/chassis/events.go`, add the initial emit after the new `catalog` initial emit (Task 5):

```go
	lastProviderStatus := map[string]string{}
	for _, env := range providerStatusEnvelopesFrom(s.userProviderStatusSnapshot()) {
		if err := emit(w, "providerStatus", env); err != nil {
			return
		}
		lastProviderStatus[env.Provider] = providerStatusFingerprint(env)
	}
```

Add the tick diff after the new `catalog` diff arm:

```go
				for _, env := range providerStatusEnvelopesFrom(s.userProviderStatusSnapshot()) {
					fp := providerStatusFingerprint(env)
					if lastProviderStatus[env.Provider] != fp {
						if err := emit(w, "providerStatus", env); err != nil {
							return
						}
						lastProviderStatus[env.Provider] = fp
					}
				}
```

(Removed providers are handled by the `catalog` event dropping their tab; their stale fingerprint in the map is harmless.)

- [ ] **Step 5: Update the existing SSE tests**

Update `internal/chassis/events_test.go` for the new `providerStatus` frame when a `UserProviderViewer` is wired. Keep the nil-viewer path covered so existing deployments without the viewer do not start emitting empty status frames.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run 'TestProviderStatusEnvelopes_And_Fingerprint'`
Then: `go test ./internal/chassis/`
Expected: PASS (full package green).

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/catalog_provider_form.go internal/chassis/server.go internal/chassis/events.go internal/chassis/catalog_provider_form_test.go internal/chassis/events_test.go
git commit -m "feat(chassis): emit providerStatus SSE event (per-channel enum status)"
```

---

## Task 7: Palette classes + form/chip/swatch CSS

Add the 8 CRT-tuned `.ic.u-<token>` / `.badge.u-<token>` pairs (+ a `.u-slate` default fallback) and the authoring-form/chip/swatch styling. Everything is class-based and scoped to `body.receiver` (the `css_scope_test.go` gate rejects unscoped selectors). The tokens MUST be exactly `amber red teal blue purple green cyan slate` (matching `badgeColorTokens`, `provider_user.go:67-72`).

**Files:**
- Modify: `internal/chassis/static/chassis.css` (append a Phase 6 block near the existing `.ic`/`.badge` rules)
- Test: `internal/chassis/css_scope_test.go` (already enforces scoping — add a presence assertion)

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/css_scope_test.go`:

```go
func TestChassisCSS_HasUserProviderPalette(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	css := string(src)
	// Every palette token (matching streams badgeColorTokens) needs an .ic and
	// a .badge class so the template's u-<token> fallback always resolves.
	for _, tok := range []string{"amber", "red", "teal", "blue", "purple", "green", "cyan", "slate"} {
		if !strings.Contains(css, ".ic.u-"+tok) {
			t.Errorf("missing .ic.u-%s palette class", tok)
		}
		if !strings.Contains(css, ".badge.u-"+tok) {
			t.Errorf("missing .badge.u-%s palette class", tok)
		}
	}
}
```

Ensure the test file imports `"strings"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestChassisCSS_HasUserProviderPalette'`
Expected: FAIL — missing palette classes.

- [ ] **Step 3: Add the CSS**

Append to `internal/chassis/static/chassis.css`. The `.ic.u-*` rules mirror the existing literal-hex idiom for `body.receiver .catalog-provider-tab .ic.mtv` (lines 4933-4935); the `.badge.u-*` rules mirror the `oklch` idiom for `body.receiver .preset .badge.mtv` (lines 3391-3401). Eight CRT-tuned bg/fg pairs + `slate` as the load-time default:

```css
/* ── Phase 6: user-provider palette (CSP-safe class tokens) ──────────────
   Tokens MUST match streams badgeColorTokens: amber red teal blue purple
   green cyan slate. The template prefers the bundled BadgeClass and falls
   back to u-<token>; .u-slate is the load-time default (defaultBadgeColor).
   No inline style= ever carries user data. */
body.receiver .catalog-provider-tab .ic.u-amber,
body.receiver .catalog-form .ic.u-amber  { background: #6e4a0c; color: #ffe0a0; }
body.receiver .catalog-provider-tab .ic.u-red,
body.receiver .catalog-form .ic.u-red    { background: #6e1c1c; color: #ffc2c2; }
body.receiver .catalog-provider-tab .ic.u-teal,
body.receiver .catalog-form .ic.u-teal   { background: #0c5a5a; color: #a8f0f0; }
body.receiver .catalog-provider-tab .ic.u-blue,
body.receiver .catalog-form .ic.u-blue   { background: #133e6e; color: #b5dbff; }
body.receiver .catalog-provider-tab .ic.u-purple,
body.receiver .catalog-form .ic.u-purple { background: #38136e; color: #d2b5ff; }
body.receiver .catalog-provider-tab .ic.u-green,
body.receiver .catalog-form .ic.u-green  { background: #1c5a24; color: #b8f0c0; }
body.receiver .catalog-provider-tab .ic.u-cyan,
body.receiver .catalog-form .ic.u-cyan   { background: #0c4a6e; color: #a8e0ff; }
body.receiver .catalog-provider-tab .ic.u-slate,
body.receiver .catalog-form .ic.u-slate  { background: #2a2e36; color: #c0c6d0; }

body.receiver .preset .badge.u-amber  { color: oklch(0.78 0.14 75); }
body.receiver .preset .badge.u-red    { color: oklch(0.70 0.18 25); }
body.receiver .preset .badge.u-teal   { color: oklch(0.74 0.10 190); }
body.receiver .preset .badge.u-blue   { color: oklch(0.68 0.12 250); }
body.receiver .preset .badge.u-purple { color: oklch(0.66 0.14 300); }
body.receiver .preset .badge.u-green  { color: oklch(0.74 0.14 150); }
body.receiver .preset .badge.u-cyan   { color: oklch(0.76 0.12 210); }
body.receiver .preset .badge.u-slate  { color: oklch(0.70 0.02 250); }

/* ── Phase 6: authoring form ─────────────────────────────────────────── */
body.receiver .catalog-form { display: none; }
body.receiver.catalog-form-open .catalog-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
}
body.receiver.catalog-form-open .catalog-browser { display: none; }

body.receiver .catalog-form .cf-identity {
  display: grid;
  grid-template-columns: 1fr 90px auto;
  gap: 10px;
  align-items: end;
}
body.receiver .catalog-form input.cf-input,
body.receiver .catalog-form select.cf-input {
  width: 100%;
  background: linear-gradient(180deg, #0a0a0b 0%, #050506 100%);
  border: 1px solid oklch(0.22 0.012 80);
  color: oklch(0.82 0.012 80);
  padding: 5px 8px;
  font: 500 11px 'Inter', sans-serif;
  border-radius: 2px;
}
body.receiver .catalog-form input.cf-input:focus,
body.receiver .catalog-form select.cf-input:focus {
  outline: none;
  border-color: oklch(0.32 0.06 175);
}

/* Color swatch palette — one button per token; .selected rings the active one. */
body.receiver .catalog-form .cf-swatches { display: flex; gap: 4px; flex-wrap: wrap; }
body.receiver .catalog-form .cf-swatch {
  width: 20px; height: 20px;
  border: 1px solid #050506;
  border-radius: 2px;
  cursor: pointer;
}
body.receiver .catalog-form .cf-swatch.selected { outline: 2px solid var(--vfd); outline-offset: 1px; }
body.receiver .catalog-form .cf-swatch.u-amber  { background: #6e4a0c; }
body.receiver .catalog-form .cf-swatch.u-red    { background: #6e1c1c; }
body.receiver .catalog-form .cf-swatch.u-teal   { background: #0c5a5a; }
body.receiver .catalog-form .cf-swatch.u-blue   { background: #133e6e; }
body.receiver .catalog-form .cf-swatch.u-purple { background: #38136e; }
body.receiver .catalog-form .cf-swatch.u-green  { background: #1c5a24; }
body.receiver .catalog-form .cf-swatch.u-cyan   { background: #0c4a6e; }
body.receiver .catalog-form .cf-swatch.u-slate  { background: #2a2e36; }

/* Channel rows + inline editor */
body.receiver .catalog-form .cf-channel {
  display: grid;
  grid-template-columns: 1fr 1fr auto auto auto auto;
  gap: 6px;
  align-items: center;
  padding: 4px 0;
  border-bottom: 1px solid oklch(0.16 0.012 80);
}
body.receiver .catalog-form .cf-channel .cf-hint {
  grid-column: 1 / -1;
  font: 500 9px 'Inter', sans-serif;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: #6a6a6e;
}
body.receiver .catalog-form .cf-chip {
  padding: 2px 6px;
  font: 700 9px 'Inter', sans-serif;
  letter-spacing: 0.12em;
  border-radius: 2px;
  background: oklch(0.20 0.01 80);
  color: var(--vfd-dim);
}
body.receiver .catalog-form .cf-chip.ok  { background: oklch(0.22 0.10 150); color: oklch(0.86 0.14 150); }
body.receiver .catalog-form .cf-chip.err { background: oklch(0.22 0.12 25);  color: oklch(0.86 0.16 25); }
body.receiver .catalog-form .cf-chip.pending { color: var(--vfd); animation: chip-resolve 1.2s ease-in-out infinite; }

body.receiver .catalog-form .cf-footer { display: flex; gap: 8px; justify-content: flex-end; margin-top: 8px; }

/* Drag affordance shared with preset reorder (Task 8 reorder.js). */
body.receiver .catalog-form .cf-channel[data-dragging="source"] { opacity: 0.4; }
body.receiver .catalog-form .cf-channel.drop-target { outline: 1px dashed var(--vfd-dim); }

/* ＋ New provider tab + ✎ edit pencil */
body.receiver .catalog-provider-tab .cf-pencil {
  margin-left: 4px;
  opacity: 0.6;
  cursor: pointer;
}
body.receiver .catalog-provider-tab .cf-pencil:hover { opacity: 1; }
body.receiver .catalog-provider-tab.cf-new {
  border-style: dashed;
  color: var(--vfd-dim);
}
```

If `@keyframes chip-resolve` is not already defined globally (it backs `.input-panel .chip.resolving`, so it is), reuse it; otherwise the `.cf-chip.pending` animation degrades to a static color.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestChassisCSS'`
Expected: PASS — both the new presence test AND the existing `TestChassisCSS_AllSelectorsScoped` (every new selector starts with `body.receiver`).

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/chassis.css internal/chassis/css_scope_test.go
git commit -m "feat(chassis): CSP-safe user-provider palette + authoring-form CSS"
```

---

## Task 8: Template — `catalog-provider-form.html` + ✎/＋New affordances

Add the form partial and the tab-strip affordances. The form is server-rendered empty (a shell); `provider-form.js` populates it client-side from the GET read or a fresh ＋New. Mark user tabs with a ✎ pencil and add a dashed ＋New tab.

**Files:**
- Create: `internal/chassis/templates/catalog-provider-form.html`
- Modify: `internal/chassis/templates/catalog-drawer.html`
- Test: `internal/chassis/catalog_provider_form_test.go` (render assertions via the existing template harness)

- [ ] **Step 1: Write the failing test**

Find how existing template-render tests execute a partial (`grep -n "ExecuteTemplate" internal/chassis/*_test.go`). Append to `internal/chassis/catalog_provider_form_test.go` using the same harness shape (a fresh `parseTemplates()` is fine here):

```go
func TestCatalogDrawer_RendersUserAffordances(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := CatalogData{
		ActiveProviderID: "user:mix",
		Providers: []CatalogProviderTab{
			{ID: "mtv-rewind", DisplayName: "MTV Rewind", BadgeLabel: "MTV", BadgeClass: "mtv"},
			{ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal",
				Groups: []CatalogGroupTab{{ID: "", Name: "", Channels: []CatalogChannelCard{{ID: "live", Name: "Live"}}}}},
		},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "catalog-drawer", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `data-edit-provider="user:mix"`) {
		t.Error("user provider tab missing ✎ edit affordance")
	}
	if strings.Contains(out, `data-edit-provider="mtv-rewind"`) {
		t.Error("bundled provider tab must NOT have an edit affordance")
	}
	if !strings.Contains(out, "catalog-provider-new") {
		t.Error("missing ＋New provider tab")
	}
	if !strings.Contains(out, `id="catalog-form"`) {
		t.Error("missing authoring form container")
	}
}
```

Ensure imports: `"bytes"`, `"strings"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestCatalogDrawer_RendersUserAffordances`
Expected: FAIL — affordances/form container absent.

- [ ] **Step 3: Add the ✎/＋New affordances to `catalog-drawer.html`**

**First**, register a `hasPrefix` template func (verified ABSENT from `templateFuncs` in `templates.go` — `list`/`upper`/`pad2` exist, `hasPrefix` does not). Add it to the `templateFuncs` map:

```go
	"hasPrefix": strings.HasPrefix,
```

(`strings` is already imported in `templates.go`.) The Task 8 render test calls `parseTemplates()` directly, so a missing func would surface as a parse error there — but add it up front to avoid a confusing red.

**Then** edit `internal/chassis/templates/catalog-drawer.html`: replace the ENTIRE template body (not just the tab loop) with the markup below — it preserves the existing `.catalog-browser > [.catalog-provider-tabs, .catalog-body]` structure (the `.catalog-browser` wrapper already exists in the merged template) and adds the ✎ pencil, the ＋New tab, and the form include. The user-vs-bundled split uses `hasPrefix .ID "user:"`. New body:

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
        {{- if hasPrefix .ID "user:" -}}
        <span class="cf-pencil" data-edit-provider="{{.ID}}" role="button" tabindex="0" title="Edit provider">&#9998;</span>
        {{- end -}}
      </button>
      {{- end -}}
      <button class="catalog-provider-tab cf-new" type="button" id="catalog-provider-new" title="New provider">
        <span class="ic">&#43;</span> New
      </button>
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
  {{template "catalog-provider-form" .}}
</div>
{{end}}
```

- [ ] **Step 4: Create the form partial**

Create `internal/chassis/templates/catalog-provider-form.html`. It is a static shell; `provider-form.js` fills `#cf-channels`, swatches, and group chips. The swatch buttons are rendered server-side from the fixed token list (CSP-safe — no user data):

```html
{{define "catalog-provider-form"}}
{{htmlComment "chassis:catalog-provider-form"}}
<div class="catalog-form" id="catalog-form" aria-hidden="true">
  <form id="cf-form" autocomplete="off">
    <input type="hidden" id="cf-provider-id" value="">
    <div class="cf-identity">
      <label>Name
        <input type="text" class="cf-input" id="cf-name" maxlength="64" placeholder="Provider name">
      </label>
      <label>Glyph
        <input type="text" class="cf-input" id="cf-glyph" maxlength="4" placeholder="GL">
      </label>
      <div>
        <span class="cf-label">Color</span>
        <div class="cf-swatches" id="cf-swatches" role="radiogroup" aria-label="Badge color">
          {{- range $tok := list "amber" "red" "teal" "blue" "purple" "green" "cyan" "slate" -}}
          <button type="button" class="cf-swatch u-{{$tok}}" data-color="{{$tok}}" role="radio" aria-checked="false" aria-label="{{$tok}}"></button>
          {{- end -}}
        </div>
      </div>
    </div>

    <div class="cf-groups" id="cf-groups">
      <span class="cf-label">Groups</span>
      <span class="cf-group-chips" id="cf-group-chips"></span>
      <button type="button" class="cf-add-group" id="cf-add-group">&#43; Group</button>
    </div>

    <div class="cf-channels" id="cf-channels"></div>
    <button type="button" class="cf-add-channel" id="cf-add-channel">&#43; Channel</button>

    <div class="cf-footer">
      <button type="button" class="cf-cancel" id="cf-cancel">Cancel</button>
      <button type="button" class="cf-delete" id="cf-delete" hidden>Delete provider</button>
      <button type="submit" class="cf-save" id="cf-save">Save provider</button>
    </div>
  </form>
</div>
{{end}}
```

This uses the existing `list` template func (verified registered at `templates.go:71` as `func(args ...string) []string { return args }`) — no new func needed. A server-rendered fixed token list keeps the swatch palette identical to `badgeColorTokens` with zero user data in markup.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run 'TestCatalogDrawer_RendersUserAffordances'`
Then the whole package: `go test ./internal/chassis/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/templates/catalog-provider-form.html internal/chassis/templates/catalog-drawer.html internal/chassis/templates.go internal/chassis/catalog_provider_form_test.go
git commit -m "feat(chassis): authoring-form template + ✎/＋New tab affordances"
```

---

## Task 9: Shared pointer-drag engine (`reorder.js`) + refactor `preset-reorder.js`

Spec §9 item 7: reuse the preset-bank drag-reorder code, not a new DnD. The merged `preset-reorder.js` is a pointer-drag implementation but is NOT factored. Extract the generic mechanics (press → threshold → clone → drop-target detection → `onDrop(fromEl, toEl)`) into `window.Chassis.reorder.makeSortable(...)`, then refactor `preset-reorder.js` onto it (its behavior test is the regression gate). `provider-form.js` reuses the same engine in Task 13. The engine reports the from/to elements; each caller supplies its own drop semantics (presets **swap** two fixed slots; channels/groups **reorder** a list).

**Files:**
- Create: `internal/chassis/static/reorder.js`
- Create: `internal/chassis/testdata/reorder.behavior.test.js`
- Modify: `internal/chassis/static/preset-reorder.js` (use the engine for pointer drag; keep swap + keyboard)
- Modify: `internal/chassis/testdata/preset-reorder.behavior.test.js` (load `reorder.js` before `preset-reorder.js`; provide `window.Chassis`)
- Modify: `internal/chassis/templates/shell.html` (load `reorder.js` before `preset-reorder.js`)

- [ ] **Step 1: Write the failing engine test**

Create `internal/chassis/testdata/reorder.behavior.test.js` (mirror the `preset-reorder.behavior.test.js` fake-DOM harness — `node:test` + `vm.runInContext`):

```javascript
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeClassList {
  constructor(o) { this.o = o; }
  add(n) { this.o.classes.add(n); }
  remove(n) { this.o.classes.delete(n); }
  contains(n) { return this.o.classes.has(n); }
  toggle(n, on) { if (on === undefined) on = !this.o.classes.has(n); on ? this.o.classes.add(n) : this.o.classes.delete(n); return on; }
}
class FakeEl {
  constructor(cls = '') {
    this.classes = new Set(String(cls).split(/\s+/).filter(Boolean));
    this.classList = new FakeClassList(this);
    this.dataset = {}; this.listeners = new Map(); this.style = {}; this.children = [];
  }
  addEventListener(n, fn) { (this.listeners.get(n) || this.listeners.set(n, []).get(n)).push(fn); }
  dispatch(n, e = {}) { for (const fn of this.listeners.get(n) || []) fn({ type: n, target: this, ...e }); }
  appendChild(c) { this.children.push(c); return c; }
  removeChild(c) { this.children = this.children.filter((x) => x !== c); }
  cloneNode() { return new FakeEl([...this.classes].join(' ')); }
  closest(sel) { return sel === '.item' && this.classes.has('item') ? this : null; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 100, height: 40 }; }
  setPointerCapture() {} releasePointerCapture() {} setAttribute() {} removeAttribute() {} remove() {}
}

function harness() {
  const container = new FakeEl('list');
  const a = new FakeEl('item'); a.dataset.id = 'a';
  const b = new FakeEl('item'); b.dataset.id = 'b';
  container.appendChild(a); container.appendChild(b);
  let pointAt = b;
  const document = { body: { appendChild() {}, removeChild() {}, style: {} }, elementFromPoint: () => pointAt };
  const drops = [];
  const ctx = {
    document, window: { Chassis: {} },
    setPointAt(el) { pointAt = el; },
    onDrop: (from, to) => drops.push([from.dataset.id, to.dataset.id]),
    getDrops: () => drops,
  };
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'static', 'reorder.js'), 'utf8'), ctx, { filename: 'reorder.js' });
  ctx.window.Chassis.reorder.makeSortable({ container, itemSelector: '.item', onDrop: ctx.onDrop });
  return { container, a, b, ctx };
}

test('makeSortable fires onDrop(from,to) after a threshold-exceeding drag', () => {
  const h = harness();
  h.ctx.setPointAt(h.b);
  h.container.dispatch('pointerdown', { target: h.a, button: 0, pointerType: 'mouse', pointerId: 1, clientX: 0, clientY: 0, preventDefault() {} });
  h.container.dispatch('pointermove', { pointerId: 1, clientX: 50, clientY: 0 });
  h.container.dispatch('pointerup', { pointerId: 1, clientX: 50, clientY: 0 });
  assert.deepEqual(h.ctx.getDrops(), [['a', 'b']]);
});

test('a sub-threshold press fires no onDrop', () => {
  const h = harness();
  h.container.dispatch('pointerdown', { target: h.a, button: 0, pointerType: 'mouse', pointerId: 2, clientX: 0, clientY: 0, preventDefault() {} });
  h.container.dispatch('pointermove', { pointerId: 2, clientX: 2, clientY: 0 });
  h.container.dispatch('pointerup', { pointerId: 2, clientX: 2, clientY: 0 });
  assert.equal(h.ctx.getDrops().length, 0);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test internal/chassis/testdata/reorder.behavior.test.js`
Expected: FAIL — `reorder.js` does not exist / `makeSortable` undefined.

- [ ] **Step 3: Implement `reorder.js`**

Create `internal/chassis/static/reorder.js`:

```javascript
(function () {
  'use strict';

  // makeSortable wires pointer-drag reordering on a container. It owns ONLY the
  // drag mechanics (press → 5px threshold → floating clone → drop-target
  // highlight) and calls onDrop(fromEl, toEl) once on a successful drop. Callers
  // supply the semantics: preset bank swaps two slots; the authoring form
  // reorders a list. Extracted from preset-reorder.js so both reuse one engine
  // (spec §9 item 7). opts: { container, itemSelector, onDrop, cloneClass? }.
  function makeSortable(opts) {
    const container = opts.container;
    const itemSelector = opts.itemSelector;
    const onDrop = opts.onDrop;
    const cloneClass = opts.cloneClass || 'reorder-drag-clone';
    const THRESHOLD = 5;
    let drag = null;

    function item(el) {
      return el && el.closest ? el.closest(itemSelector) : null;
    }

    container.addEventListener('pointerdown', function (e) {
      if (e.button !== 0 && e.pointerType === 'mouse') return;
      const target = item(e.target);
      if (!target || target.classList.contains('empty')) return;
      drag = { source: target, clone: null, startX: e.clientX, startY: e.clientY, lastTarget: null, pointerId: e.pointerId };
      target.classList.add('pressed');
      if (target.setPointerCapture) target.setPointerCapture(e.pointerId);
      e.preventDefault();
    });

    container.addEventListener('pointermove', function (e) {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const dx = e.clientX - drag.startX;
      const dy = e.clientY - drag.startY;
      if (!drag.clone) {
        if (Math.hypot(dx, dy) < THRESHOLD) return;
        const rect = drag.source.getBoundingClientRect();
        const clone = drag.source.cloneNode(true);
        clone.classList.add(cloneClass);
        clone.style.position = 'fixed';
        clone.style.left = rect.left + 'px';
        clone.style.top = rect.top + 'px';
        clone.style.width = rect.width + 'px';
        clone.style.height = rect.height + 'px';
        clone.style.pointerEvents = 'none';
        clone.style.zIndex = '9999';
        document.body.appendChild(clone);
        drag.clone = clone;
        drag.source.classList.remove('pressed');
        drag.source.setAttribute('data-dragging', 'source');
        document.body.style.cursor = 'grabbing';
      }
      drag.clone.style.transform = 'translate(' + dx + 'px,' + dy + 'px)';
      const below = document.elementFromPoint(e.clientX, e.clientY);
      const target = item(below);
      if (target && target !== drag.lastTarget) {
        if (drag.lastTarget) drag.lastTarget.classList.remove('drop-target');
        if (target !== drag.source) target.classList.add('drop-target');
        drag.lastTarget = target;
      }
    });

    container.addEventListener('pointerup', function (e) {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const source = drag.source;
      const target = drag.lastTarget;
      const hadClone = !!drag.clone;
      cancel();
      if (!hadClone || !target || target === source) return;
      onDrop(source, target);
    });

    function cancel() {
      if (!drag) return;
      if (drag.clone) document.body.removeChild(drag.clone);
      drag.source.classList.remove('pressed');
      drag.source.removeAttribute('data-dragging');
      if (drag.lastTarget) drag.lastTarget.classList.remove('drop-target');
      document.body.style.cursor = '';
      drag = null;
    }

    return { cancel: cancel };
  }

  window.Chassis = window.Chassis || {};
  window.Chassis.reorder = { makeSortable: makeSortable };
})();
```

- [ ] **Step 4: Refactor `preset-reorder.js` onto the engine**

Replace the inline `pointerdown`/`pointermove`/`pointerup` handlers in `preset-reorder.js` with a single `window.Chassis.reorder.makeSortable({ container: bank, itemSelector: '.preset', onDrop: onPresetDrop })` call, where `onPresetDrop(fromEl, toEl)` runs the existing `swapVisual(fromEl, toEl)` + `postMove(from, to)` + revert logic. Keep the existing keyboard handler and `postMove`/`reportChip` helpers unchanged. The element-level state (`from`/`to` slot numbers) comes from `fromEl.dataset.slot`/`toEl.dataset.slot` inside `onPresetDrop`:

```javascript
function onPresetDrop(fromEl, toEl) {
  const from = parseInt(fromEl.dataset.slot, 10);
  const to = parseInt(toEl.dataset.slot, 10);
  if (!Number.isFinite(from) || !Number.isFinite(to)) return;
  const revert = swapVisual(fromEl, toEl);
  postMove(from, to).then(function (ok) { if (!ok) revert(); });
}
// …after bank is resolved:
if (window.Chassis && window.Chassis.reorder) {
  window.Chassis.reorder.makeSortable({ container: bank, itemSelector: '.preset', onDrop: onPresetDrop });
}
```

(`postMove` returns a Promise; keep its `async` body.) Preserve `swapVisual`, `snapshotVisual`, `restoreVisual`, `reportChip`, and the `keydown` handler verbatim.

- [ ] **Step 5: Update the preset-reorder behavior harness**

Update `internal/chassis/testdata/preset-reorder.behavior.test.js` so the VM context provides `window.Chassis`, loads `static/reorder.js` before `static/preset-reorder.js`, and still asserts the existing preset drag behavior. The refactor makes `preset-reorder.js` depend on `window.Chassis.reorder`, so this existing regression test must evolve with the runtime dependency instead of only adding the new `reorder.behavior.test.js`.

- [ ] **Step 6: Add the script include**

In `internal/chassis/templates/shell.html`, add `reorder.js` immediately BEFORE `preset-reorder.js` (so `window.Chassis.reorder` exists when preset-reorder runs):

```html
    <script defer src="/ui/static/reorder.js?v={{.Version}}"></script>
    <script defer src="/ui/static/preset-reorder.js?v={{.Version}}"></script>
```

- [ ] **Step 7: Run both behavior tests to verify they pass**

Run: `node --test internal/chassis/testdata/reorder.behavior.test.js`
Run: `node --test internal/chassis/testdata/preset-reorder.behavior.test.js`
Expected: BOTH PASS — the engine test passes and the preset-reorder regression test still passes (press class visible on pointerdown, cleared on pointerup).

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/static/reorder.js internal/chassis/static/preset-reorder.js internal/chassis/testdata/reorder.behavior.test.js internal/chassis/testdata/preset-reorder.behavior.test.js internal/chassis/templates/shell.html
git commit -m "refactor(chassis): extract reorder.js drag engine; preset-reorder reuses it"
```

---

## Task 10: `provider-form.js` core — module, pure helpers, open/populate

Establish the IIFE module, the `window.Chassis.providerForm` namespace (cross-file + testable), the pure helpers the spec pins (`detectKind` mirroring Go `detectChannelKind`, `suggestGlyph` per spec §9 item 6, `hostHint` advisory client check, `postJSON`), and the open/close lifecycle (＋New blank form; ✎ pencil → GET read → populate). This task also establishes the `provider-form.behavior.test.js` harness reused by Tasks 11–15.

**Files:**
- Create: `internal/chassis/static/provider-form.js`
- Create: `internal/chassis/testdata/provider-form.behavior.test.js`
- Modify: `internal/chassis/templates/shell.html` (load `provider-form.js` after `settings-drawer.js`)

- [ ] **Step 1: Write the failing test (pure helpers)**

Create `internal/chassis/testdata/provider-form.behavior.test.js` with the harness + helper tests. The harness builds a fake DOM with the form's element ids present, runs `provider-form.js`, and exposes `window.Chassis.providerForm`:

```javascript
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeClassList {
  constructor(o) { this.o = o; }
  add(n) { this.o.classes.add(n); }
  remove(n) { this.o.classes.delete(n); }
  contains(n) { return this.o.classes.has(n); }
  toggle(n, on) { if (on === undefined) on = !this.o.classes.has(n); on ? this.o.classes.add(n) : this.o.classes.delete(n); return on; }
}
class FakeEl {
  constructor(tag = 'div', cls = '') {
    this.tagName = String(tag).toUpperCase();
    this.classes = new Set(String(cls).split(/\s+/).filter(Boolean));
    this.classList = new FakeClassList(this);
    this.dataset = {}; this.attrs = {}; this.listeners = new Map();
    this.style = {}; this.children = []; this.value = ''; this.hidden = false;
    this._html = ''; this.textContent = '';
  }
  get innerHTML() { return this._html; }
  set innerHTML(v) { this._html = String(v); if (v === '') this.children = []; }
  addEventListener(n, fn) { const l = this.listeners.get(n) || []; l.push(fn); this.listeners.set(n, l); }
  dispatch(n, e = {}) { for (const fn of this.listeners.get(n) || []) fn({ type: n, target: this, preventDefault() {}, stopPropagation() {}, ...e }); }
  appendChild(c) { this.children.push(c); c.parent = this; return c; }
  removeChild(c) { this.children = this.children.filter((x) => x !== c); }
  replaceChildren() { this.children = []; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k]; }
  removeAttribute(k) { delete this.attrs[k]; }
  closest(sel) { let n = this; while (n) { if (n._matches && n._matches(sel)) return n; n = n.parent; } return null; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
  cloneNode() { return new FakeEl(this.tagName, [...this.classes].join(' ')); }
}

function harness(opts) {
  opts = opts || {};
  const els = {};
  const ids = ['catalog-form', 'cf-form', 'cf-provider-id', 'cf-name', 'cf-glyph', 'cf-swatches',
    'cf-group-chips', 'cf-add-group', 'cf-channels', 'cf-add-channel', 'cf-cancel', 'cf-delete', 'cf-save',
    'catalog-provider-new', 'catalog-drawer'];
  ids.forEach((id) => { els[id] = new FakeEl('div'); els[id].id = id; });
  const body = new FakeEl('body');
  const docListeners = {};
  const document = {
    body,
    getElementById: (id) => els[id] || null,
    querySelector: () => null,
    querySelectorAll: () => [],
    createElement: (t) => new FakeEl(t),
    addEventListener: (t, fn) => { (docListeners[t] = docListeners[t] || []).push(fn); },
  };
  const subs = {};
  const ctx = {
    document,
    window: {
      Chassis: {
        events: { subscribe: (name, fn) => { (subs[name] = subs[name] || []).push(fn); } },
        input: { showError() {} },
        settings: { showNotice() {} },
        setupBlocked: () => false,
        reorder: { makeSortable() { return { cancel() {} }; } },
      },
    },
    fetch: opts.fetch || (async () => ({ ok: true, json: async () => ({ ok: true }) })),
    JSON, URL, URLSearchParams,
    setTimeout: (fn) => { if (opts.runTimers) fn(); return 0; },
    clearTimeout() {},
    console: { warn() {}, info() {} },
  };
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'static', 'provider-form.js'), 'utf8'), ctx, { filename: 'provider-form.js' });
  return { els, body, ctx, subs, docListeners, pf: ctx.window.Chassis.providerForm };
}

test('detectKind mirrors Go detectChannelKind', () => {
  const { pf } = harness();
  assert.equal(pf.detectKind('https://cdn.example.com/live.m3u8'), 'direct');
  assert.equal(pf.detectKind('https://host/stream.mpd'), 'direct');
  assert.equal(pf.detectKind('https://www.youtube.com/playlist?list=PL123'), 'playlist');
  assert.equal(pf.detectKind('https://www.youtube.com/watch?v=abc&list=PL123'), 'playlist');
  assert.equal(pf.detectKind('https://twitch.tv/foo'), 'single');
  assert.equal(pf.detectKind('not a url'), 'single');
});

test('suggestGlyph matches the spec §9 examples', () => {
  const { pf } = harness();
  assert.equal(pf.suggestGlyph('F1 TV'), 'F1');
  assert.equal(pf.suggestGlyph('Cartoon Network'), 'CN');
  assert.equal(pf.suggestGlyph('Lofi'), 'LO');
});

test('hostHint flags blocked hosts, allows LAN/public', () => {
  const { pf } = harness();
  assert.equal(pf.hostHint('https://192.168.1.5/x.m3u8').ok, true);
  assert.equal(pf.hostHint('http://localhost/x').ok, false);
  assert.equal(pf.hostHint('http://127.0.0.1/x').ok, false);
  assert.equal(pf.hostHint('http://169.254.169.254/latest').ok, false);
  assert.equal(pf.hostHint('file:///etc/passwd').ok, false);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: FAIL — `provider-form.js` does not exist.

- [ ] **Step 3: Implement the core module**

Create `internal/chassis/static/provider-form.js`:

```javascript
(function () {
  'use strict';

  // provider-form.js drives the user-provider authoring form inside the catalog
  // drawer (spec §8/§9). It is its own IIFE; cross-file calls go through
  // window.Chassis.* (events.subscribe, input.showError, settings.showNotice,
  // setupBlocked, reorder.makeSortable). Pure helpers are exposed on
  // window.Chassis.providerForm for the SSE handlers and node --test.

  var PALETTE = ['amber', 'red', 'teal', 'blue', 'purple', 'green', 'cyan', 'slate'];

  // detectKind mirrors Go detectChannelKind (provider_user.go): syntactic only.
  // The server re-detects + validates on save; this is advisory.
  function detectKind(raw) {
    var u;
    try { u = new URL(String(raw).trim()); } catch (_) { return 'single'; }
    if (!u.host) return 'single';
    var path = u.pathname.toLowerCase();
    var sfx = ['.m3u8', '.m3u', '.mpd'];
    for (var i = 0; i < sfx.length; i++) { if (path.endsWith(sfx[i])) return 'direct'; }
    var host = u.hostname.toLowerCase().replace(/^www\./, '');
    var isYT = host === 'youtube.com' || host === 'm.youtube.com' || host === 'music.youtube.com';
    if (isYT && u.searchParams.get('list')) return 'playlist';
    return 'single';
  }

  // suggestGlyph pre-fills the glyph from the provider name (spec §9 item 6).
  // "F1 TV"→"F1", "Cartoon Network"→"CN", "Lofi"→"LO".
  function suggestGlyph(name) {
    var n = String(name || '').trim();
    if (!n) return '';
    var words = n.split(/\s+/).filter(Boolean);
    if (words.length >= 2) {
      var w0 = words[0].replace(/[^A-Za-z0-9]/g, '');
      if (w0.length >= 2 && w0.length <= 4 && /\d/.test(w0)) return w0.toUpperCase().slice(0, 4);
      var i0 = (words[0].match(/[A-Za-z0-9]/) || [''])[0];
      var i1 = (words[1].match(/[A-Za-z0-9]/) || [''])[0];
      return (i0 + i1).toUpperCase();
    }
    var alnum = words[0].toUpperCase().replace(/[^A-Z0-9]/g, '');
    return alnum.slice(0, 2); // single word → first 2 alphanumerics ("Lofi" → "LO")
  }

  // hostHint is an advisory client-side SSRF check mirroring the obvious arms of
  // validateUserProviderHost. The server is authoritative (save-time + play-time).
  function hostHint(raw) {
    var u;
    try { u = new URL(String(raw).trim()); } catch (_) { return { ok: false, msg: 'INVALID URL' }; }
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return { ok: false, msg: 'SCHEME BLOCKED' };
    var h = u.hostname.toLowerCase().replace(/^\[|\]$/g, '');
    if (h === 'localhost' || h === '127.0.0.1' || h === '::1' || h === '0.0.0.0') return { ok: false, msg: 'LOOPBACK BLOCKED' };
    if (h.indexOf('169.254.') === 0 || h.indexOf('fe80:') === 0) return { ok: false, msg: 'LINK-LOCAL BLOCKED' };
    return { ok: true, msg: 'HOST ALLOWED' };
  }

  // postJSON is the one fetch wrapper for every catalog route — GET, POST, PUT,
  // DELETE. Despite the name it does NOT force a JSON body: when `body` is
  // omitted (GET/DELETE) it sends no Content-Type and no payload. The catalog
  // routes decode JSON (unlike the form-urlencoded preset routes), and
  // credentials:'same-origin' keeps first-party cookies/same-origin browser
  // signals on the request. Mutating routes pass requireSameOrigin; the
  // authoring GET passes requireSameOriginRead. No custom guard header is
  // needed or allowed.
  async function postJSON(method, url, body) {
    var init = { method: method, credentials: 'same-origin' };
    if (body !== undefined) {
      init.headers = { 'Content-Type': 'application/json' };
      init.body = JSON.stringify(body);
    }
    var resp = await fetch(url, init);
    var json = await resp.json().catch(function () { return { ok: false }; });
    return { resp: resp, json: json };
  }

  var doc = document;
  function el(id) { return doc.getElementById(id); }

  var state = { editingID: '' };

  function openForm() { doc.body.classList.add('catalog-form-open'); var f = el('catalog-form'); if (f) f.setAttribute('aria-hidden', 'false'); }
  function closeForm() { doc.body.classList.remove('catalog-form-open'); var f = el('catalog-form'); if (f) f.setAttribute('aria-hidden', 'true'); }

  // newProvider resets the form to a blank ＋New state.
  function newProvider() {
    state.editingID = '';
    setValue('cf-provider-id', '');
    setValue('cf-name', '');
    setValue('cf-glyph', '');
    selectColor('slate');
    renderGroups([]);
    renderChannels([{ id: '', name: '', url: '', kind: '', playMode: '', groupId: '', order: 0 }]);
    var del = el('cf-delete'); if (del) del.hidden = true;
    openForm();
  }

  // editProvider loads an existing provider's authoring form via the GET seam,
  // then populates the editor (spec §8 ✎). 404 → a notice, form stays closed.
  async function editProvider(id) {
    try {
      var r = await postJSON('GET', '/ui/catalog/provider/' + encodeURIComponent(id));
      if (!r.resp.ok || !r.json.ok) { notice('LOAD FAILED', 'err'); return; }
      populate(r.json);
    } catch (_) { notice('LOAD FAILED', 'err'); }
  }

  // populate fills the form from a catalogProviderFormResponse (or the saved
  // provider after create). Shared by editProvider and the create flow.
  function populate(form) {
    state.editingID = form.id || '';
    setValue('cf-provider-id', form.id || '');
    setValue('cf-name', form.displayName || '');
    setValue('cf-glyph', form.badgeLabel || '');
    selectColor(PALETTE.indexOf(form.badgeColor) >= 0 ? form.badgeColor : 'slate');
    renderGroups(form.groups || []);
    renderChannels((form.channels && form.channels.length) ? form.channels : [{ id: '', name: '', url: '', kind: '', playMode: '', groupId: '', order: 0 }]);
    var del = el('cf-delete'); if (del) del.hidden = !state.editingID;
    openForm();
  }

  function setValue(id, v) { var e = el(id); if (e) e.value = v; }
  function notice(text, variant) {
    if (window.Chassis && window.Chassis.settings && window.Chassis.settings.showNotice) {
      window.Chassis.settings.showNotice(text, variant);
    }
  }

  // wireStatic binds the ＋New tab + ✎ pencils (BOTH delegated on the drawer so
  // they survive catalog-browser.js rebuilding the tab strip on a `catalog` SSE
  // event — Task 16a — which would orphan a directly-bound button) and the
  // Cancel button. Channel/group/save handlers come in later tasks.
  function wireStatic() {
    var drawer = el('catalog-drawer');
    if (drawer) {
      drawer.addEventListener('click', function (e) {
        if (!e.target || !e.target.closest) return;
        var edit = e.target.closest('[data-edit-provider]');
        if (edit) { e.stopPropagation(); editProvider(edit.dataset.editProvider || edit.getAttribute('data-edit-provider')); return; }
        var add = e.target.closest('#catalog-provider-new');
        if (add) { e.stopPropagation(); newProvider(); }
      });
    }
    var cancel = el('cf-cancel');
    if (cancel) cancel.addEventListener('click', function () { closeForm(); });
  }

  // Placeholders overridden in later tasks (kept as no-op stubs so the core
  // module is self-consistent before Tasks 11-15 land).
  function selectColor(_token) {}
  function renderGroups(_groups) {}
  function renderChannels(_channels) {}

  wireStatic();

  window.Chassis = window.Chassis || {};
  window.Chassis.providerForm = {
    detectKind: detectKind,
    suggestGlyph: suggestGlyph,
    hostHint: hostHint,
    openForm: openForm,
    closeForm: closeForm,
    newProvider: newProvider,
    editProvider: editProvider,
    populate: populate,
    _state: state,
    _palette: PALETTE,
  };
})();
```

> The `selectColor`/`renderGroups`/`renderChannels` functions are real no-op stubs here and are **replaced with full implementations in Tasks 11–12** (same file, same names). This keeps the module compilable and the helper tests green now.

- [ ] **Step 4: Add the script include**

In `internal/chassis/templates/shell.html`, add `provider-form.js` after `settings-drawer.js` (it depends on `window.Chassis.events`/`input`/`settings`/`reorder`, all loaded earlier):

```html
    <script defer src="/ui/static/settings-drawer.js?v={{.Version}}"></script>
    <script defer src="/ui/static/provider-form.js?v={{.Version}}"></script>
```

- [ ] **Step 5: Run test to verify it passes**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Then sanity-check the syntax loads in Go's embed (build): `go build ./cmd/mister-groovy-relay`
Expected: helper tests PASS; build clean (the asset hash picks up the new file).

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/static/provider-form.js internal/chassis/testdata/provider-form.behavior.test.js internal/chassis/templates/shell.html
git commit -m "feat(chassis): provider-form.js core (helpers + open/populate lifecycle)"
```

---

## Task 11: Identity row — color swatches + glyph auto-suggest

Replace the `selectColor` stub with a real swatch selector and wire glyph auto-suggest (pre-fills from the name only while the glyph field is untouched — spec §9 item 6).

**Files:**
- Modify: `internal/chassis/static/provider-form.js`
- Test: `internal/chassis/testdata/provider-form.behavior.test.js`

- [ ] **Step 1: Write the failing test**

Append to `provider-form.behavior.test.js`:

```javascript
test('selectColor marks exactly one swatch selected', () => {
  const h = harness();
  // Seed swatch children the way the server template renders them.
  const sw = h.els['cf-swatches'];
  ['amber', 'teal', 'slate'].forEach((tok) => {
    const b = new FakeEl('button', 'cf-swatch u-' + tok);
    b.dataset.color = tok;
    b._matches = (s) => s === '.cf-swatch';
    sw.children.push(b);
  });
  sw.querySelectorAll = () => sw.children;
  h.pf.selectColor('teal');
  const selected = sw.children.filter((c) => c.classes.has('selected')).map((c) => c.dataset.color);
  assert.deepEqual(selected, ['teal']);
});

test('glyph auto-suggests from name while untouched, stops once edited', () => {
  const h = harness();
  const name = h.els['cf-name'];
  const glyph = h.els['cf-glyph'];
  h.pf.newProvider();
  name.value = 'Cartoon Network';
  name.dispatch('input');
  assert.equal(glyph.value, 'CN');
  // Operator edits the glyph → auto-suggest stops.
  glyph.value = 'XY';
  glyph.dispatch('input');
  name.value = 'Different Name';
  name.dispatch('input');
  assert.equal(glyph.value, 'XY');
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: FAIL — `selectColor` is a no-op (no `.selected`); glyph stays empty.

- [ ] **Step 3: Implement swatches + glyph**

In `provider-form.js`, replace the `selectColor` stub and extend `wireStatic`. Replace:

```javascript
  function selectColor(_token) {}
```

with:

```javascript
  function selectColor(token) {
    state.color = PALETTE.indexOf(token) >= 0 ? token : 'slate';
    var sw = el('cf-swatches');
    if (!sw) return;
    var btns = sw.querySelectorAll('.cf-swatch');
    for (var i = 0; i < btns.length; i++) {
      var on = btns[i].dataset.color === state.color;
      btns[i].classList.toggle('selected', on);
      btns[i].setAttribute('aria-checked', on ? 'true' : 'false');
    }
  }
```

Add glyph state to the `state` object initializer (change `var state = { editingID: '' };` to):

```javascript
  var state = { editingID: '', color: 'slate', glyphTouched: false };
```

In `wireStatic`, add swatch-click + name/glyph input wiring (append inside `wireStatic`, before its closing brace):

```javascript
    var sw = el('cf-swatches');
    if (sw) {
      sw.addEventListener('click', function (e) {
        var b = e.target && e.target.closest ? e.target.closest('.cf-swatch') : null;
        if (b) selectColor(b.dataset.color || b.getAttribute('data-color'));
      });
    }
    var name = el('cf-name');
    var glyph = el('cf-glyph');
    if (name) name.addEventListener('input', function () {
      if (!state.glyphTouched && glyph) glyph.value = suggestGlyph(name.value);
    });
    if (glyph) glyph.addEventListener('input', function () { state.glyphTouched = true; });
```

In `newProvider` and `populate`, reset/set `glyphTouched`: in `newProvider` add `state.glyphTouched = false;` (after `state.editingID = '';`); in `populate` add `state.glyphTouched = true;` (a loaded provider already has a glyph — don't clobber it on the next name edit). Expose `selectColor` on the namespace (add `selectColor: selectColor,` to the `window.Chassis.providerForm` object).

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: PASS (all helper + identity tests).

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/provider-form.js internal/chassis/testdata/provider-form.behavior.test.js
git commit -m "feat(chassis): provider-form identity row (color swatches + glyph auto-suggest)"
```

---

## Task 12: Channel rows + groups (render + inline interactions)

Replace the `renderChannels`/`renderGroups` stubs. Each channel row has Name · URL · an AUTO kind chip (with a ▾ override) · a play-mode control shown **only for `playlist`** · a group dropdown · delete, plus an inline hint line showing the detected kind + host allow/deny as the operator types (spec §8).

**Files:**
- Modify: `internal/chassis/static/provider-form.js`
- Test: `internal/chassis/testdata/provider-form.behavior.test.js`

- [ ] **Step 1: Write the failing test**

Append to `provider-form.behavior.test.js`:

```javascript
test('renderChannels builds one row per channel with kind + hint', () => {
  const h = harness();
  const host = h.els['cf-channels'];
  h.pf.renderChannels([
    { id: 'live', name: 'Live', url: 'https://cdn.example.com/s.m3u8', kind: '', playMode: '', groupId: '', order: 0 },
  ]);
  assert.equal(host.children.length, 1);
  const row = host.children[0];
  // The row caches its model so collectPayload (Task 14) can read it back.
  assert.equal(row.dataset.kind, 'direct'); // auto-detected from .m3u8
});

test('updateRowKind reveals play-mode only for playlist', () => {
  const h = harness();
  const row = h.pf._buildChannelRow({ id: '', name: '', url: '', kind: '', playMode: '', groupId: '', order: 0 }, []);
  h.pf._setRowURL(row, 'https://www.youtube.com/playlist?list=PL1');
  assert.equal(row.dataset.kind, 'playlist');
  assert.equal(row._playModeWrap.hidden, false);
  h.pf._setRowURL(row, 'https://twitch.tv/foo');
  assert.equal(row.dataset.kind, 'single');
  assert.equal(row._playModeWrap.hidden, true);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: FAIL — `renderChannels` is a no-op; `_buildChannelRow`/`_setRowURL` undefined.

- [ ] **Step 3: Implement channel/group rendering**

In `provider-form.js`, replace the two stubs:

```javascript
  function renderGroups(_groups) {}
  function renderChannels(_channels) {}
```

with full implementations. They build DOM nodes via `document.createElement` and cache the model on each row for `collectPayload` (Task 14):

```javascript
  function mk(tag, cls) { var e = doc.createElement(tag); if (cls) cls.split(' ').forEach(function (c) { e.classList.add(c); }); return e; }

  // kids snapshots a parent's children into a real Array. Real-DOM `.children`
  // is an HTMLCollection (no .map/.filter); the fake DOM's is an array. slice
  // normalizes both, so callers can use array methods safely in the browser.
  function kids(host) { return Array.prototype.slice.call(host.children); }

  function currentGroups() { return state.groups || []; }

  function renderGroups(groups) {
    state.groups = (groups || []).map(function (g, i) { return { id: g.id || '', name: g.name || '', order: g.order != null ? g.order : i }; });
    var host = el('cf-group-chips');
    if (!host) return;
    host.replaceChildren();
    state.groups.forEach(function (g) {
      var chip = mk('span', 'cf-group-chip');
      chip.dataset.group = g.id;
      chip.textContent = g.name || '(unnamed)';
      var del = mk('button', 'cf-group-del');
      del.textContent = '×';
      del.addEventListener('click', function () {
        state.groups = state.groups.filter(function (x) { return x !== g; });
        renderGroups(state.groups);
        // Re-render channels so their group dropdowns drop the removed option.
        renderChannels(collectChannelModels());
      });
      chip.appendChild(del);
      host.appendChild(chip);
    });
  }

  function buildChannelRow(ch, groups) {
    var row = mk('div', 'cf-channel');
    row.dataset.channelId = ch.id || '';
    row.dataset.kind = ch.kind || detectKind(ch.url || '');

    var name = mk('input', 'cf-input cf-ch-name'); name.value = ch.name || ''; name.setAttribute('placeholder', 'Channel name');
    var url = mk('input', 'cf-input cf-ch-url'); url.value = ch.url || ''; url.setAttribute('placeholder', 'URL');

    var kindChip = mk('button', 'cf-chip cf-kind'); kindChip.setAttribute('type', 'button');
    var override = mk('select', 'cf-input cf-kind-override');
    ['auto', 'direct', 'single', 'playlist'].forEach(function (k) {
      var o = mk('option'); o.value = k; o.textContent = k.toUpperCase(); override.appendChild(o);
    });
    override.value = ch.kind ? ch.kind : 'auto';

    var playWrap = mk('span', 'cf-playmode');
    var play = mk('select', 'cf-input cf-ch-play');
    ['sequential', 'shuffle', 'first_then_shuffle'].forEach(function (m) {
      var o = mk('option'); o.value = m; o.textContent = m.toUpperCase(); play.appendChild(o);
    });
    play.value = ch.playMode || 'sequential';
    playWrap.appendChild(play);

    var groupSel = mk('select', 'cf-input cf-ch-group');
    var none = mk('option'); none.value = ''; none.textContent = '(no group)'; groupSel.appendChild(none);
    (groups || currentGroups()).forEach(function (g) {
      var o = mk('option'); o.value = g.id; o.textContent = g.name || g.id; groupSel.appendChild(o);
    });
    groupSel.value = ch.groupId || '';

    var del = mk('button', 'cf-ch-del'); del.setAttribute('type', 'button'); del.textContent = '×';
    var hint = mk('div', 'cf-hint');

    row._name = name; row._url = url; row._kindChip = kindChip; row._override = override;
    row._play = play; row._playModeWrap = playWrap; row._group = groupSel; row._hint = hint;

    function refresh() {
      var chosen = override.value === 'auto' ? detectKind(url.value) : override.value;
      row.dataset.kind = chosen;
      kindChip.textContent = chosen.toUpperCase();
      playWrap.hidden = chosen !== 'playlist';
      var hh = url.value ? hostHint(url.value) : { ok: true, msg: '' };
      hint.textContent = url.value ? ('DETECTED: ' + chosen.toUpperCase() + ' · ' + hh.msg) : '';
      hint.classList.toggle('err', !hh.ok && !!url.value);
    }
    url.addEventListener('input', refresh);
    override.addEventListener('change', refresh);
    del.addEventListener('click', function () { var host = el('cf-channels'); if (host) host.removeChild(row); });
    refresh();

    [name, url, kindChip, override, playWrap, groupSel, del, hint].forEach(function (n) { row.appendChild(n); });
    return row;
  }

  function setRowURL(row, value) { row._url.value = value; row._url.dispatch ? row._url.dispatch('input') : row._url.dispatchEvent && row._url.dispatchEvent(new Event('input')); }

  function renderChannels(channels) {
    var host = el('cf-channels');
    if (!host) return;
    host.replaceChildren();
    (channels || []).forEach(function (ch) { host.appendChild(buildChannelRow(ch, currentGroups())); });
  }

  function collectChannelModels() {
    var host = el('cf-channels');
    if (!host) return [];
    return kids(host).map(function (row, i) {
      return {
        id: row.dataset.channelId || '',
        name: row._name.value, url: row._url.value,
        kind: row._override.value === 'auto' ? '' : row._override.value,
        playMode: row.dataset.kind === 'playlist' ? row._play.value : '',
        groupId: row._group.value, order: i,
      };
    });
  }
```

Wire ＋Channel / ＋Group buttons in `wireStatic` (append before its closing brace):

```javascript
    var addCh = el('cf-add-channel');
    if (addCh) addCh.addEventListener('click', function () {
      var host = el('cf-channels');
      if (host) host.appendChild(buildChannelRow({ id: '', name: '', url: '', kind: '', playMode: '', groupId: '', order: host.children.length }, currentGroups()));
    });
    var addG = el('cf-add-group');
    if (addG) addG.addEventListener('click', function () {
      var name = 'Group ' + (currentGroups().length + 1);
      var next = currentGroups().concat([{ id: 'g' + (currentGroups().length + 1), name: name, order: currentGroups().length }]);
      var models = collectChannelModels();
      renderGroups(next);
      renderChannels(models);
    });
```

Expose the new internals for testing (add to the namespace object): `renderChannels: renderChannels, renderGroups: renderGroups, collectChannelModels: collectChannelModels, _buildChannelRow: function (ch, g) { return buildChannelRow(ch, g); }, _setRowURL: setRowURL,`.

All children-iteration goes through the `kids()` helper (defined above), so `collectChannelModels` and the group-delete re-render work against the real-DOM `HTMLCollection` as well as the fake DOM's array — never call `.map` on `host.children` directly.

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/provider-form.js internal/chassis/testdata/provider-form.behavior.test.js
git commit -m "feat(chassis): provider-form channel rows + groups (kind detect, play-mode, host hint)"
```

---

## Task 13: Verify button (✓/✗ chips, live-vs-VOD)

Each channel row gets a Verify button that POSTs to `/ui/catalog/channel/verify` and renders the `VerifyChannelResult` as a ✓/✗ chip: playlist → "✓ N videos", live single → "✓ LIVE", VOD single → "✓ VIDEO", direct → "✓ DIRECT", error → "✗ {message}" (spec §9 items 4-5).

**Files:**
- Modify: `internal/chassis/static/provider-form.js`
- Test: `internal/chassis/testdata/provider-form.behavior.test.js`

- [ ] **Step 1: Write the failing test**

Append to `provider-form.behavior.test.js`:

```javascript
test('verifyRow renders a ✓ chip with the playlist count', async () => {
  const h = harness({ fetch: async () => ({ ok: true, json: async () => ({ ok: true, kind: 'playlist', itemCount: 47 }) }) });
  const row = h.pf._buildChannelRow({ id: '', name: 'L', url: 'https://www.youtube.com/playlist?list=PL1', kind: '', playMode: '', groupId: '', order: 0 }, []);
  await h.pf._verifyRow(row);
  assert.match(row._verifyChip.textContent, /47/);
  assert.equal(row._verifyChip.classes.has('ok'), true);
});

test('verifyRow renders a ✗ chip on failure', async () => {
  const h = harness({ fetch: async () => ({ ok: false, json: async () => ({ ok: false, message: 'PRIVATE' }) }) });
  const row = h.pf._buildChannelRow({ id: '', name: 'L', url: 'https://www.youtube.com/playlist?list=PL1', kind: '', playMode: '', groupId: '', order: 0 }, []);
  await h.pf._verifyRow(row);
  assert.equal(row._verifyChip.classes.has('err'), true);
});

test('verifyRow shows LIVE for a live single', async () => {
  const h = harness({ fetch: async () => ({ ok: true, json: async () => ({ ok: true, kind: 'single', isLive: true }) }) });
  const row = h.pf._buildChannelRow({ id: '', name: 'L', url: 'https://twitch.tv/foo', kind: '', playMode: '', groupId: '', order: 0 }, []);
  await h.pf._verifyRow(row);
  assert.match(row._verifyChip.textContent, /LIVE/);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: FAIL — `_verifyRow` undefined / `row._verifyChip` undefined.

- [ ] **Step 3: Implement Verify**

In `provider-form.js`, add a Verify button + chip to `buildChannelRow`. Inside `buildChannelRow`, after the `del` button is created, add:

```javascript
    var verify = mk('button', 'cf-verify'); verify.setAttribute('type', 'button'); verify.textContent = '✓?';
    var verifyChip = mk('span', 'cf-chip cf-verify-chip'); verifyChip.hidden = true;
    row._verify = verify; row._verifyChip = verifyChip;
    verify.addEventListener('click', function () { verifyRow(row); });
```

Add `verify` and `verifyChip` to the row's appended children list (extend the final append line):

```javascript
    [name, url, kindChip, override, playWrap, groupSel, verify, del, hint, verifyChip].forEach(function (n) { row.appendChild(n); });
```

Add the `verifyRow` function (module scope, near `collectChannelModels`):

```javascript
  // verifyRow dry-runs one channel against the Phase 5 verify route and paints
  // the result chip. Advisory; no persistence. The kind sent is the row's
  // resolved kind ("" → server auto-detects).
  async function verifyRow(row) {
    var chip = row._verifyChip;
    chip.hidden = false;
    chip.classList.remove('ok', 'err');
    chip.classList.add('pending');
    chip.textContent = '…';
    try {
      var kind = row._override.value === 'auto' ? '' : row._override.value;
      var r = await postJSON('POST', '/ui/catalog/channel/verify', { url: row._url.value, kind: kind });
      chip.classList.remove('pending');
      var v = r.json || {};
      if (r.resp.ok && v.ok) {
        chip.classList.add('ok');
        if (v.kind === 'playlist') chip.textContent = '✓ ' + (v.itemCount || 0) + ' videos';
        else if (v.kind === 'direct') chip.textContent = '✓ DIRECT';
        else chip.textContent = v.isLive ? '✓ LIVE' : '✓ VIDEO';
      } else {
        chip.classList.add('err');
        chip.textContent = '✗ ' + (v.message || 'failed');
      }
    } catch (_) {
      chip.classList.remove('pending');
      chip.classList.add('err');
      chip.textContent = '✗ failed';
    }
  }
```

Expose for tests: add `_verifyRow: function (row) { return verifyRow(row); },` to the namespace object.

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/provider-form.js internal/chassis/testdata/provider-form.behavior.test.js
git commit -m "feat(chassis): provider-form Verify button (✓/✗ chips, live-vs-VOD)"
```

---

## Task 14: Drag-to-reorder channels & groups (reuse `reorder.js`)

Wire the channel list (and group chip row) to `window.Chassis.reorder.makeSortable`, with a list-reorder `onDrop` that moves the dragged row before/after the drop target and POSTs the new order to `/ui/catalog/provider/{id}/reorder` (spec §9 item 7). Unlike preset swap, this is a list move.

**Files:**
- Modify: `internal/chassis/static/provider-form.js`
- Test: `internal/chassis/testdata/provider-form.behavior.test.js`

- [ ] **Step 1: Write the failing test**

Append to `provider-form.behavior.test.js`:

```javascript
test('reorderListMove moves the dragged row and recomputes order', () => {
  const h = harness();
  h.pf.newProvider();
  const host = h.els['cf-channels'];
  // Replace the default single blank row with three identifiable rows.
  h.pf.renderChannels([
    { id: 'a', name: 'A', url: 'https://x/a.m3u8', kind: '', playMode: '', groupId: '', order: 0 },
    { id: 'b', name: 'B', url: 'https://x/b.m3u8', kind: '', playMode: '', groupId: '', order: 1 },
    { id: 'c', name: 'C', url: 'https://x/c.m3u8', kind: '', playMode: '', groupId: '', order: 2 },
  ]);
  const [a, b, c] = host.children;
  h.pf._reorderListMove(host, c, a); // drop C onto A → C before A
  const ids = host.children.map((r) => r.dataset.channelId);
  assert.deepEqual(ids, ['c', 'a', 'b']);
});

test('group reorder moves chips and recomputes state.groups order', () => {
  const h = harness();
  h.pf.renderGroups([
    { id: 'g1', name: 'One', order: 0 },
    { id: 'g2', name: 'Two', order: 1 },
    { id: 'g3', name: 'Three', order: 2 },
  ]);
  const host = h.els['cf-group-chips'];
  const [g1, , g3] = host.children;
  h.pf._reorderListMove(host, g3, g1); // drop G3 onto G1 → G3 before G1
  h.pf._syncGroupsFromHost(host);
  assert.deepEqual(h.pf._currentGroups().map((g) => g.id), ['g3', 'g1', 'g2']);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: FAIL — `_reorderListMove` undefined.

- [ ] **Step 3: Implement list-move + wire the engine**

In `provider-form.js`, add the list-move helper and the reorder POST:

```javascript
  // reorderListMove moves fromEl to before/after toEl within host (a list move,
  // not a swap). Direction: if from is currently before to, insert AFTER to;
  // else insert BEFORE to — matching natural drag semantics.
  function reorderListMove(host, fromEl, toEl) {
    var kids = Array.prototype.slice.call(host.children);
    var fi = kids.indexOf(fromEl), ti = kids.indexOf(toEl);
    if (fi < 0 || ti < 0 || fi === ti) return;
    host.removeChild(fromEl);
    var after = fi < ti; // dragging downward → place after the target
    // Re-read children after removal.
    var ref = toEl;
    if (after) {
      // insert after toEl: append before toEl's next sibling
      var nk = Array.prototype.slice.call(host.children);
      var idx = nk.indexOf(toEl);
      if (idx + 1 < nk.length) insertBefore(host, fromEl, nk[idx + 1]); else host.appendChild(fromEl);
    } else {
      insertBefore(host, fromEl, ref);
    }
  }

  function insertBefore(host, node, ref) {
    if (host.insertBefore) { host.insertBefore(node, ref); return; }
    // Fake-DOM fallback used by node --test.
    var kids = Array.prototype.slice.call(host.children);
    var i = kids.indexOf(ref);
    host.children = kids.slice(0, i).concat([node], kids.slice(i));
  }

  async function postReorder() {
    if (!state.editingID) return; // new providers persist order on first save
    var channels = collectChannelModels().map(function (c, i) { return { id: c.id, order: i }; }).filter(function (e) { return e.id; });
    var groups = currentGroups().map(function (g, i) { return { id: g.id, order: i }; });
    try { await postJSON('POST', '/ui/catalog/provider/' + encodeURIComponent(state.editingID) + '/reorder', { channels: channels, groups: groups }); } catch (_) {}
  }

  function syncGroupsFromHost(host) {
    var byID = {};
    currentGroups().forEach(function (g) { byID[g.id] = g; });
    state.groups = kids(host).map(function (chip, i) {
      var id = chip.dataset.group || '';
      var g = byID[id] || { id: id, name: chip._groupName || chip.textContent || '', order: i };
      return { id: g.id, name: g.name, order: i };
    });
  }

  function wireChannelReorder() {
    var host = el('cf-channels');
    if (host && window.Chassis && window.Chassis.reorder) {
      window.Chassis.reorder.makeSortable({
        container: host, itemSelector: '.cf-channel',
        onDrop: function (from, to) { reorderListMove(host, from, to); postReorder(); },
      });
    }
  }

  function wireGroupReorder() {
    var host = el('cf-group-chips');
    if (host && window.Chassis && window.Chassis.reorder) {
      window.Chassis.reorder.makeSortable({
        container: host, itemSelector: '.cf-group-chip',
        onDrop: function (from, to) {
          reorderListMove(host, from, to);
          syncGroupsFromHost(host);
          renderChannels(collectChannelModels()); // keep group dropdown order in sync
          postReorder();
        },
      });
    }
  }
```

Make `.cf-channel` rows and `.cf-group-chip` chips matchable by `closest(...)` (the engine uses `item(el).closest(itemSelector)`). In the browser this is automatic; in the fake DOM, set `row._matches = function (s) { return s === '.cf-channel'; };` inside `buildChannelRow` (add after `var row = mk('div', 'cf-channel');`) and set `chip._matches = function (s) { return s === '.cf-group-chip'; }; chip._groupName = g.name || '';` inside `renderGroups` (add after `var chip = mk('span', 'cf-group-chip');`).

Call both `wireChannelReorder()` and `wireGroupReorder()` once at the end of `wireStatic` (the hosts are static; rows/chips are re-created but the container listeners persist). Expose `_reorderListMove: function (host, f, t) { return reorderListMove(host, f, t); }, _syncGroupsFromHost: syncGroupsFromHost, _currentGroups: currentGroups,` on the namespace.

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/provider-form.js internal/chassis/testdata/provider-form.behavior.test.js
git commit -m "feat(chassis): provider-form drag-reorder channels/groups via reorder.js"
```

---

## Task 15: Save / Cancel / Delete (+ auto-enable toast, cleared-slots, delete-with-cleanup confirm)

Collect the form into the camelCase payload and POST (create) / PUT (update) / DELETE. On create, read `autoEnabledStreams` from the response and toast "Streams source turned on…" or "restart the bridge to activate" (spec §10). Report `clearedSlots`. Deleting a provider (or a starred channel) warns first (spec §9 item 8). Validation errors keep the form open with a chip; success closes it.

**Files:**
- Modify: `internal/chassis/static/provider-form.js`
- Test: `internal/chassis/testdata/provider-form.behavior.test.js`

- [ ] **Step 1: Write the failing test**

Append to `provider-form.behavior.test.js`:

```javascript
test('collectPayload builds the camelCase create body', () => {
  const h = harness();
  h.pf.newProvider();
  h.els['cf-name'].value = 'F1 TV';
  h.els['cf-glyph'].value = 'F1';
  h.pf.selectColor('amber');
  h.pf.renderChannels([{ id: '', name: 'Live', url: 'https://cdn/x.m3u8', kind: '', playMode: '', groupId: '', order: 0 }]);
  const body = h.pf._collectPayload();
  assert.equal(body.displayName, 'F1 TV');
  assert.equal(body.badgeColor, 'amber');
  assert.equal(body.channels[0].url, 'https://cdn/x.m3u8');
});

test('save (create) posts JSON, toasts auto-enable, closes the form', async () => {
  const calls = [];
  const h = harness({
    runTimers: true,
    fetch: async (url, init) => { calls.push({ url, method: init.method, body: init.body }); return { ok: true, json: async () => ({ ok: true, provider: { id: 'user:f1-tv' }, autoEnabledStreams: 'on' }) }; },
  });
  let toast = null;
  h.ctx.window.Chassis.settings.showNotice = (t) => { toast = t; };
  h.pf.newProvider();
  h.els['cf-name'].value = 'F1 TV';
  await h.pf._save();
  assert.equal(calls[0].method, 'POST');
  assert.match(String(calls[0].body), /F1 TV/);
  assert.match(toast || '', /turned on/i);
  assert.equal(h.body.classes.has('catalog-form-open'), false); // closed on success
});

test('save (update) uses PUT to the provider id', async () => {
  const calls = [];
  const h = harness({ fetch: async (url, init) => { calls.push({ url, method: init.method }); return { ok: true, json: async () => ({ ok: true, provider: { id: 'user:mix' }, clearedSlots: [3] }) }; } });
  h.pf.populate({ ok: true, id: 'user:mix', displayName: 'Mix', badgeLabel: 'MX', badgeColor: 'teal', groups: [], channels: [{ id: 'a', name: 'A', url: 'https://x/a.m3u8', kind: '', playMode: '', groupId: '', order: 0 }] });
  await h.pf._save();
  assert.equal(calls[0].method, 'PUT');
  assert.match(calls[0].url, /user:mix/);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: FAIL — `_collectPayload` / `_save` undefined.

- [ ] **Step 3: Implement save/delete**

In `provider-form.js`, add the payload collector and the save/delete handlers:

```javascript
  function selectedColor() { return state.color || 'slate'; }

  function collectPayload() {
    return {
      displayName: (el('cf-name') || {}).value || '',
      badgeLabel: (el('cf-glyph') || {}).value || '',
      badgeColor: selectedColor(),
      groups: currentGroups().map(function (g, i) { return { id: g.id, name: g.name, order: i }; }),
      channels: collectChannelModels(),
    };
  }

  // starredKeys mirrors the 12-slot preset bank as a Set of "provider:channel",
  // fed by the presets SSE event (the same event catalog-browser.js consumes).
  // This makes the delete warning PRECISE (spec §9.8: warn only when this
  // provider actually has starred channels) rather than warning on every edit.
  // A DOM `.starred` scan is insufficient — only the active group's cards live
  // in the grid; the rest sit inert in <template> content querySelectorAll skips.
  var starredKeys = new Set();
  if (window.Chassis && window.Chassis.events && window.Chassis.events.subscribe) {
    window.Chassis.events.subscribe('presets', function (ev) {
      var data; try { data = JSON.parse(ev.data); } catch (_) { return; }
      var next = new Set();
      (data.slots || []).forEach(function (s) { if (s.provider && s.channel) next.add(s.provider + ':' + s.channel); });
      starredKeys = next;
    });
  }

  function anyStarredChannel() {
    if (!state.editingID) return false;
    var prefix = state.editingID + ':';
    var found = false;
    starredKeys.forEach(function (k) { if (k.indexOf(prefix) === 0) found = true; });
    return found;
  }

  async function save() {
    var payload = collectPayload();
    var creating = !state.editingID;
    var url = '/ui/catalog/provider' + (creating ? '' : '/' + encodeURIComponent(state.editingID));
    try {
      var r = await postJSON(creating ? 'POST' : 'PUT', url, payload);
      if (!r.resp.ok || !r.json.ok) {
        notice('SAVE FAILED', 'err'); // inline chip stays; form remains open
        return;
      }
      if (creating && r.json.autoEnabledStreams === 'on') notice('Streams source turned on so your provider can play', 'ok');
      else if (creating && r.json.autoEnabledStreams === 'restart-required') notice('Streams enabled — restart the bridge to activate', 'err');
      if (r.json.clearedSlots && r.json.clearedSlots.length) notice('Cleared ' + r.json.clearedSlots.length + ' preset slot(s)', 'ok');
      closeForm();
    } catch (_) { notice('SAVE FAILED', 'err'); }
  }

  async function deleteProvider() {
    if (!state.editingID) { closeForm(); return; }
    var warn = anyStarredChannel()
      ? 'Delete this provider? Starred channels will be removed from presets.'
      : 'Delete this provider?';
    if (typeof confirm === 'function' && !confirm(warn)) return;
    try {
      var r = await postJSON('DELETE', '/ui/catalog/provider/' + encodeURIComponent(state.editingID));
      if (r.resp.ok && r.json.ok) {
        if (r.json.clearedSlots && r.json.clearedSlots.length) notice('Cleared ' + r.json.clearedSlots.length + ' preset slot(s)', 'ok');
        closeForm();
      } else { notice('DELETE FAILED', 'err'); }
    } catch (_) { notice('DELETE FAILED', 'err'); }
  }
```

Wire submit + delete in `wireStatic` (append before its closing brace):

```javascript
    var form = el('cf-form');
    if (form) form.addEventListener('submit', function (e) { e.preventDefault(); save(); });
    var save0 = el('cf-save');
    if (save0) save0.addEventListener('click', function (e) { e.preventDefault(); save(); });
    var del0 = el('cf-delete');
    if (del0) del0.addEventListener('click', function () { deleteProvider(); });
```

Expose for tests: `_collectPayload: collectPayload, _save: save, _delete: deleteProvider,`. (`confirm` is absent in the fake DOM → the guard `typeof confirm === 'function'` skips it, so delete tests don't block; the browser shows the dialog.)

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/provider-form.js internal/chassis/testdata/provider-form.behavior.test.js
git commit -m "feat(chassis): provider-form save/update/delete (auto-enable toast, cleared-slots, confirm)"
```

---

## Task 16: SSE rendering — `providerStatus` chips (provider-form.js) + `catalog` rebuild (catalog-browser.js)

Two SSE events, two owners (spec §8 "Form lifecycle" — an open drawer updates without a reload). **provider-form.js** paints per-channel "Enumerating…/N videos/✗ error" chips on the OPEN form's playlist rows from `providerStatus`; Phase 6 does not render enum state/count chips on catalog cards. **catalog-browser.js** rebuilds the provider tab strip (children of the stable `.catalog-provider-tabs`, incl. ✎ pencils + ＋New) AND the hidden `<template id="catalog-tree-{id}">` trees from `catalog`, so a provider added/edited/removed by ANY client becomes navigable without a reload.

**Why catalog-browser.js owns the `catalog` rebuild (verified against the merged file):** it already holds `tabsContainer`/`railHost`/`gridHost`, the `getTreeTemplate`/`cloneRail`/`cloneGrid` helpers, `cssEscape`, the **delegated** tab/rail/grid click handlers (`catalog-browser.js:132,138,144` — so replacing tab-strip *children* keeps them live), and `applyStars`/`applyTuned`, which already walk `template[id^="catalog-tree-"]` (`:236,264`). The hidden trees are rendered by `preset-bank-catalog-trees` (`preset-bank.html:39-71`) — the rebuild must reproduce that exact `.catalog-tree-payload > [.catalog-rail-group, .catalog-tree-grid > .ch-card]` shape. The ✎/＋New **click** handlers stay in provider-form.js (delegated on the drawer, Task 10), so they survive the rebuild.

**Files:**
- Modify: `internal/chassis/static/provider-form.js` (providerStatus chips)
- Modify: `internal/chassis/static/catalog-browser.js` (catalog rebuild)
- Test: `internal/chassis/testdata/provider-form.behavior.test.js` + NEW `internal/chassis/testdata/catalog-browser.behavior.test.js`

### Part A — provider-form.js `providerStatus` chips

- [ ] **Step 1: Write the failing test** (append to `provider-form.behavior.test.js`)

```javascript
test('statusChipLabel maps state+count to a label', () => {
  const h = harness();
  assert.equal(h.pf._statusChipLabel({ state: 'pending', itemCount: 0 }), 'ENUMERATING…');
  assert.equal(h.pf._statusChipLabel({ state: 'ready', itemCount: 12 }), '12 VIDEOS');
  assert.equal(h.pf._statusChipLabel({ state: 'error', itemCount: 0 }), '✗ ERROR');
});

test('provider-form subscribes to providerStatus and paints chips on its open form rows', () => {
  const h = harness();
  h.pf.populate({ ok: true, id: 'user:mix', displayName: 'Mix', badgeLabel: 'MX', badgeColor: 'teal', groups: [],
    channels: [{ id: 'list', name: 'List', url: 'https://www.youtube.com/playlist?list=PL1', kind: 'playlist', playMode: 'sequential', groupId: '', order: 0 }] });
  assert.ok(h.subs['providerStatus'] && h.subs['providerStatus'].length >= 1, 'subscribed');
  h.subs['providerStatus'][0]({ data: JSON.stringify({ provider: 'user:mix', channels: [{ channel: 'list', state: 'ready', itemCount: 9 }] }) });
  const row = h.els['cf-channels'].children[0];
  assert.ok(row._enumChip && /9 VIDEOS/.test(row._enumChip.textContent));
});
```

- [ ] **Step 2: Run to verify it fails** — `node --test internal/chassis/testdata/provider-form.behavior.test.js` → FAIL (`_statusChipLabel` undefined; no providerStatus subscription).

- [ ] **Step 3: Implement** (in `provider-form.js`)

```javascript
  function statusChipLabel(c) {
    if (c.state === 'error') return '✗ ERROR';
    if (c.state === 'pending') return 'ENUMERATING…';
    return (c.itemCount || 0) + ' VIDEOS';
  }

  // applyProviderStatus paints enum chips on the OPEN form's playlist rows
  // (spec §6/§8). Fires only for the provider currently being edited; Phase 6
  // leaves catalog-card status/count rendering out of scope. Uses a dedicated
  // row._enumChip slot, separate from the Verify chip.
  function applyProviderStatus(env) {
    var host = el('cf-channels');
    if (!host || !env || state.editingID !== env.provider) return;
    var byCh = {}; (env.channels || []).forEach(function (c) { byCh[c.channel] = c; });
    kids(host).forEach(function (row) {
      var c = byCh[row.dataset.channelId];
      if (!c || row.dataset.kind !== 'playlist') return;
      var chip = row._enumChip;
      if (!chip) { chip = mk('span', 'cf-chip cf-enum-chip'); row._enumChip = chip; row.appendChild(chip); }
      chip.textContent = statusChipLabel(c);
      chip.classList.toggle('err', c.state === 'error');
      chip.classList.toggle('pending', c.state === 'pending');
      chip.classList.toggle('ok', c.state === 'ready');
    });
  }

  if (window.Chassis && window.Chassis.events && window.Chassis.events.subscribe) {
    window.Chassis.events.subscribe('providerStatus', function (ev) {
      var data; try { data = JSON.parse(ev.data); } catch (_) { return; }
      applyProviderStatus(data);
    });
  }
```

Expose: `_statusChipLabel: statusChipLabel, _applyProviderStatus: applyProviderStatus,`. provider-form.js does NOT rebuild the tab strip — that lives in catalog-browser.js (Part B).

- [ ] **Step 4: Run to verify it passes** — `node --test internal/chassis/testdata/provider-form.behavior.test.js` → PASS.

### Part B — catalog-browser.js `catalog` rebuild (tabs + hidden trees)

- [ ] **Step 5: Write the failing test** — create `internal/chassis/testdata/catalog-browser.behavior.test.js`. catalog-browser.js early-returns unless `window.Chassis.events` + the drawer/browse/rail/grid elements exist, so the harness seeds them, runs the file, and tests the exposed node builders (`window.Chassis.catalogBrowser._buildTab`/`_buildTreeNodes`) — the reconstruction logic that could drift from `preset-bank.html`:

```javascript
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class CL { constructor(o){this.o=o;} add(n){this.o.classes.add(n);} remove(n){this.o.classes.delete(n);} contains(n){return this.o.classes.has(n);} toggle(n,on){if(on===undefined)on=!this.o.classes.has(n);on?this.o.classes.add(n):this.o.classes.delete(n);return on;} }
class E {
  constructor(tag='div',cls=''){ this.tagName=String(tag).toUpperCase(); this.classes=new Set(String(cls).split(/\s+/).filter(Boolean)); this.classList=new CL(this); this.dataset={}; this.attrs={}; this.listeners=new Map(); this.style={setProperty(){}}; this.children=[]; this._text=''; this.hidden=false; this.title=''; this.type=''; this.id=''; }
  get className(){ return [...this.classes].join(' '); } set className(v){ this.classes=new Set(String(v).split(/\s+/).filter(Boolean)); }
  get textContent(){ return this._text; } set textContent(v){ this._text=String(v); this.children=[]; }
  addEventListener(n,fn){ const l=this.listeners.get(n)||[]; l.push(fn); this.listeners.set(n,l); }
  appendChild(c){ this.children.push(c); return c; } remove(){}
  setAttribute(k,v){ this.attrs[k]=String(v); } getAttribute(k){ return this.attrs[k]; }
  querySelector(){ return null; } querySelectorAll(){ return []; }
  getBoundingClientRect(){ return {left:0,top:0,width:0,height:0}; }
  cloneNode(){ return new E(this.tagName,[...this.classes].join(' ')); }
}

function harness() {
  const ids = {};
  ['catalog-drawer','browse-toggle','catalog-rail','catalog-grid','catalog-tab-indicator','preset-mode-label'].forEach((id)=>{ ids[id]=new E('div'); ids[id].id=id; });
  const tabs = new E('div','catalog-provider-tabs');
  ids['catalog-drawer'].querySelector = (sel)=> sel==='.catalog-provider-tabs'?tabs:null;
  ids['browse-toggle']._text='Browse';
  const document = {
    getElementById:(id)=> ids[id] || null,
    querySelector:()=> null,
    querySelectorAll:()=> [],
    createElement:(t)=> new E(t),
    createTextNode:(s)=>{ const n=new E('#text'); n._text=s; return n; },
    body:new E('body'),
    dispatchEvent(){},
  };
  const subs = {};
  const ctx = { document, window:{ Chassis:{ events:{ subscribe:(n,fn)=>{ (subs[n]=subs[n]||[]).push(fn); } }, input:{showError(){}} } }, CSS:{escape:(s)=>s}, CustomEvent:function(){}, setTimeout:()=>0 };
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(path.join(__dirname,'..','static','catalog-browser.js'),'utf8'), ctx, { filename:'catalog-browser.js' });
  return { ctx, subs, tabs, cb: ctx.window.Chassis.catalogBrowser };
}

test('_buildTab renders a user tab with badge, channel count and ✎ pencil', () => {
  const h = harness();
  const tab = h.cb._buildTab({ id:'user:mix', displayName:'Mix', badgeLabel:'MX', badgeClass:'u-teal', live:false,
    groups:[{ id:'', name:'', channels:[{id:'a'},{id:'b'}] }] });
  assert.equal(tab.dataset.provider, 'user:mix');
  assert.equal(tab.classes.has('catalog-provider-tab'), true);
  const pencil = tab.children.find((c)=>c.classes.has('cf-pencil'));
  assert.ok(pencil && pencil.dataset.editProvider === 'user:mix');
  assert.equal(tab.children.find((c)=>c.classes.has('ch-count')).textContent, '2');
});

test('_buildTreeNodes produces rail buttons + grid cards matching preset-bank shape', () => {
  const h = harness();
  const nodes = h.cb._buildTreeNodes({ id:'user:mix', groups:[{ id:'g1', name:'Races', channels:[{id:'live', name:'Live', playMode:'', live:true}] }] }, true);
  const rail = nodes.find((n)=>n.classes.has('catalog-rail-group'));
  assert.equal(rail.dataset.group, 'g1');
  const grid = nodes.find((n)=>n.classes.has('catalog-tree-grid'));
  assert.equal(grid.dataset.group, 'g1');
  const card = grid.children.find((c)=>c.classes.has('ch-card'));
  assert.equal(card.dataset.channel, 'live');
  assert.equal(card.dataset.provider, 'user:mix');
});

test('catalog-browser subscribes to the catalog event', () => {
  const h = harness();
  assert.ok(h.subs['catalog'] && h.subs['catalog'].length >= 1);
});
```

- [ ] **Step 6: Run to verify it fails** — `node --test internal/chassis/testdata/catalog-browser.behavior.test.js` → FAIL (`window.Chassis.catalogBrowser` undefined).

- [ ] **Step 7: Implement the rebuild in `catalog-browser.js`** — add inside the IIFE, after `applyStars`/`parseAdapterRef` and BEFORE the existing `subscribe` calls. Builds nodes with `textContent` (XSS-safe — no `innerHTML`, no manual escaping), replaces the CHILDREN of the stable `tabsContainer`, and find-or-creates the `<template>` trees:

```javascript
  // ---- Phase 6: live `catalog` rebuild (tabs + hidden trees, spec §8) ----
  let lastPresetsPayload = null;
  let lastTunedRef = ['', ''];

  function elem(tag, cls) { const e = document.createElement(tag); if (cls) e.className = cls; return e; }

  function buildTab(p) {
    const btn = elem('button', 'catalog-provider-tab' + (p.id === activeProviderID ? ' active' : ''));
    btn.type = 'button';
    btn.dataset.provider = p.id;
    const ic = elem('span', 'ic ' + (p.badgeClass || '')); ic.textContent = p.badgeLabel || ''; btn.appendChild(ic);
    btn.appendChild(document.createTextNode(' ' + (p.displayName || '') + ' '));
    let n = 0; (p.groups || []).forEach((g) => { n += (g.channels || []).length; });
    const cc = elem('span', 'ch-count'); cc.textContent = String(n); btn.appendChild(cc);
    if (p.id.indexOf('user:') === 0) {
      const pen = elem('span', 'cf-pencil');
      pen.dataset.editProvider = p.id;
      pen.setAttribute('role', 'button'); pen.setAttribute('tabindex', '0'); pen.title = 'Edit provider';
      pen.textContent = '✎';
      btn.appendChild(pen);
    }
    return btn;
  }

  function buildCard(p, c, i) {
    const card = elem('div', 'ch-card' + (c.live ? ' live' : ''));
    card.setAttribute('role', 'button'); card.setAttribute('tabindex', '0');
    card.dataset.provider = p.id; card.dataset.channel = c.id;
    if (card.style.setProperty) card.style.setProperty('--i', i);
    const star = elem('button', 'star'); star.type = 'button'; star.title = 'Save to preset'; star.textContent = '☆';
    const name = elem('div', 'name'); name.textContent = c.name || '';
    const meta = elem('div', 'meta');
    const idSpan = elem('span'); idSpan.textContent = String(c.id || '').toUpperCase();
    const modeSpan = elem('span', 'mode'); modeSpan.textContent = c.playMode || '';
    meta.appendChild(idSpan); meta.appendChild(modeSpan);
    card.appendChild(star); card.appendChild(name); card.appendChild(meta);
    return card;
  }

  // buildTreeNodes returns rail buttons + grid divs matching the
  // preset-bank-catalog-trees payload (the clone source). star/tuned state is
  // applied afterwards by applyStars/applyTuned.
  function buildTreeNodes(p, providerIsFirst) {
    const nodes = [];
    (p.groups || []).forEach((g, gi) => {
      const rail = elem('button', 'catalog-rail-group' + (providerIsFirst && gi === 0 ? ' active' : ''));
      rail.type = 'button'; rail.dataset.group = g.id;
      if (rail.style.setProperty) rail.style.setProperty('--i', gi);
      rail.appendChild(document.createTextNode(g.name || ''));
      const count = elem('span', 'count'); count.textContent = String((g.channels || []).length); rail.appendChild(count);
      nodes.push(rail);
    });
    (p.groups || []).forEach((g, gi) => {
      const grid = elem('div', 'catalog-tree-grid'); grid.dataset.group = g.id;
      if (!(providerIsFirst && gi === 0)) grid.hidden = true;
      (g.channels || []).forEach((c, ci) => grid.appendChild(buildCard(p, c, ci)));
      nodes.push(grid);
    });
    return nodes;
  }

  function treeHostParent() {
    const any = document.querySelector('template[id^="catalog-tree-"]');
    return (any && any.parentNode) || (drawer && drawer.parentNode) || document.body;
  }

  function rebuildCatalog(env) {
    if (!env || !Array.isArray(env.providers)) return;
    // Tab strip — replace CHILDREN of the stable tabsContainer (keeps the
    // delegated click handlers + the indicator element live).
    const tabNodes = [];
    if (indicator) tabNodes.push(indicator);
    env.providers.forEach((p) => tabNodes.push(buildTab(p)));
    const addTab = elem('button', 'catalog-provider-tab cf-new'); addTab.type = 'button'; addTab.id = 'catalog-provider-new';
    const addIc = elem('span', 'ic'); addIc.textContent = '+'; addTab.appendChild(addIc);
    addTab.appendChild(document.createTextNode(' New'));
    tabNodes.push(addTab);
    tabsContainer.replaceChildren.apply(tabsContainer, tabNodes);

    // Hidden trees — find-or-create <template id="catalog-tree-{id}">.
    const host = treeHostParent();
    env.providers.forEach((p, pi) => {
      let tpl = getTreeTemplate(p.id);
      if (!tpl) { tpl = document.createElement('template'); tpl.id = 'catalog-tree-' + p.id; host.appendChild(tpl); }
      const payload = elem('div', 'catalog-tree-payload'); payload.dataset.provider = p.id;
      buildTreeNodes(p, pi === 0).forEach((node) => payload.appendChild(node));
      tpl.content.replaceChildren(payload);
    });
    // Drop trees for providers no longer present.
    document.querySelectorAll('template[id^="catalog-tree-"]').forEach((tpl) => {
      const id = tpl.id.slice('catalog-tree-'.length);
      if (!env.providers.some((p) => p.id === id)) tpl.remove();
    });

    if (!env.providers.some((p) => p.id === activeProviderID) && env.providers.length) {
      activeProviderID = env.providers[0].id;
    }
    // Re-apply star/tuned to the fresh nodes, then re-render the open view.
    if (lastPresetsPayload) applyStars(lastPresetsPayload);
    applyTuned(lastTunedRef[0], lastTunedRef[1]);
    if (isOpen) { const cur = activeProviderID; activeProviderID = ''; switchProvider(cur); }
    positionIndicator();
  }
```

Then REPLACE the two existing `transport`/`presets` subscribe blocks (`catalog-browser.js:276-287`) to cache the payloads, and ADD the `catalog` subscribe + the test export:

```javascript
  window.Chassis.events.subscribe('transport', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    const [provider, channel] = parseAdapterRef(data.adapterRef);
    lastTunedRef = [provider || '', channel || ''];
    applyTuned(provider || '', channel || '');
  });

  window.Chassis.events.subscribe('presets', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    lastPresetsPayload = data;
    applyStars(data);
  });

  window.Chassis.events.subscribe('catalog', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    rebuildCatalog(data);
  });

  window.Chassis.catalogBrowser = { rebuild: rebuildCatalog, _buildTab: buildTab, _buildTreeNodes: buildTreeNodes };
```

(Full DOM-mutation of `rebuildCatalog` — replaceChildren, template find-or-create, re-render — is exercised end-to-end via the optional live render in Task 17; the unit tests pin the node-builder shape, which is where drift from `preset-bank.html` would bite.)

- [ ] **Step 8: Run both behavior tests to verify they pass**

Run: `node --test internal/chassis/testdata/catalog-browser.behavior.test.js`
Run: `node --test internal/chassis/testdata/provider-form.behavior.test.js`
Expected: BOTH PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/chassis/static/provider-form.js internal/chassis/static/catalog-browser.js internal/chassis/testdata/provider-form.behavior.test.js internal/chassis/testdata/catalog-browser.behavior.test.js
git commit -m "feat(chassis): live catalog SSE rebuild (catalog-browser) + providerStatus chips (provider-form)"
```

---

## Task 17: Full verification + optional visual check

Run every gate green and (optionally) eyeball the form against the `authoring-flow.html` mockup intent.

**Files:** none new — verification only.

- [ ] **Step 1: Go gates**

Run, expecting all clean:

```bash
go vet ./...
go test ./...
go test -tags=integration ./tests/integration/...   # requires ffmpeg + ffprobe on PATH
```

`go test -race ./...` is CI-only (no local cgo) — let CI run it; do NOT block locally.

- [ ] **Step 2: JS behavior gates (the real test for the new JS)**

Run each new/touched behavior test (NOT in the Makefile/CI — run manually):

```bash
node --test internal/chassis/testdata/provider-form.behavior.test.js
node --test internal/chassis/testdata/catalog-browser.behavior.test.js
node --test internal/chassis/testdata/reorder.behavior.test.js
node --test internal/chassis/testdata/preset-reorder.behavior.test.js
```

Expected: all PASS. A `node --check` is NOT sufficient — the behavior tests are the gate.

- [ ] **Step 3: (Optional) local visual render**

Per memory `reference_local_chassis_render`: a `CHASSIS_REVIEW`-gated httptest server (`New` + `Mount`) + headless-shell screenshot renders the chassis locally to confirm the ✎/＋New tabs, the form layout (identity row, swatches, channel rows), and the Verify/enum chips read correctly against the `authoring-flow.html` intent (a mockup, not a deliverable). Not required to pass; confidence only. The live last-deployed page is `http://192.168.50.138:32500/ui` (memory `reference_live_receiver_page`) — it runs the deployed binary, not local edits, so use it only to compare against, not to validate this branch.

- [ ] **Step 4: Verify the staged set, then final review commit (if anything pending)**

```bash
git status
git diff --cached --name-only   # confirm only intended Phase 6 paths
```

All task commits should already be in place; this step is the catch-all for any straggler (e.g. a docs note). Stage only intended paths.

- [ ] **Step 5: Commit the plan doc (gitignored — force-add)**

```bash
git add -f docs/superpowers/plans/2026-06-03-user-custom-providers-phase6-chassis-authoring-ui.md
git commit -m "docs(plans): user custom providers Phase 6 — chassis authoring UI"
```

---

## Self-Review: spec coverage map

Each spec §8/§9 UX element + the SSE/CSP/JS-test requirements mapped to a task:

| Spec requirement | Task(s) |
|---|---|
| §8 Provider tab strip: built-ins, then user (each ✎), then dashed ＋New | Task 8 (server template), Task 10 (＋New/✎ delegated click handlers), Task 16B (live rebuild in catalog-browser.js — tabs **and** hidden trees, so new providers are navigable without reload) |
| §8 Identity row: Name · Glyph (auto-suggested) · Color swatch palette | Task 8 (template), Task 11 (swatches + glyph) |
| §8/§9.6 Glyph auto-suggest ("F1 TV"→"F1", etc.; only while untouched) | Task 10 (`suggestGlyph`), Task 11 (wiring) |
| §8 Groups (optional) chip row + ＋Group; flat when none | Task 8 (template), Task 12 (`renderGroups`, add/delete) |
| §8 Channels list: Name · URL · AUTO kind chip + ▾ override · play-mode (playlist only) · group dropdown · delete | Task 8 (template), Task 12 |
| §8/§4.7 Channel-kind auto-detect (syntactic; mirrors `detectChannelKind`) | Task 10 (`detectKind`), Task 12 (live on input) |
| §8 Inline editor: live detected-kind + host allow/deny feedback | Task 10 (`hostHint`), Task 12 (hint line) |
| §8 Footer: Save / Cancel / Delete | Task 8 (template), Task 10 (Cancel), Task 15 (Save/Delete) |
| §8 Form lifecycle: save closes; validation errors keep open with chips | Task 15 |
| §8 Playlist channels save immediately; status resolves via SSE | Task 1 (`EnumState`), Task 6 (emit), Task 16A (form chips) |
| §8 `catalog`/`providerStatus` SSE emit on `GET /ui/events` | Task 5 (`catalog` emit), Task 6 (`providerStatus` emit) |
| §8 `catalog`/`providerStatus` SSE client render | Task 16B (catalog-browser structural rebuild), Task 16A (form-only status chips) |
| §8 `autoEnabledStreams` toast ("on"/"restart-required") | Task 15 (from create response) |
| §9.1 ID namespacing (locked `user:` IDs) | Phase 5 (backend) — surfaced read-only via Task 2/4 |
| §9.2 Inline error states surfacing §7.1 rejections | Task 12 (host hint) + Task 15 (save error chip) |
| §9.3 Async playlist enumeration pending/error states | Task 1 (`EnumState`), Task 6 (SSE emit), Task 16A (form chips) |
| §9.4 Verify button (✓/✗ chips) | Task 13 |
| §9.5 Live-vs-VOD detection (LIVE vs VIDEO chip) | Task 13 |
| §9.6 Glyph auto-suggest | Tasks 10/11 |
| §9.7 Drag-to-reorder channels & groups (reuse preset drag code) | Task 9 (engine), Task 14 (wire) |
| §9.8 Delete-with-cleanup + confirm; cleared-slots feedback | Task 15 (precise warn — tracks the preset bank via the `presets` SSE event, so it warns only when this provider actually has starred channels) |
| §4.3 CSP-safe badge: palette tokens → `.ic.u-<token>`/`.badge.u-<token>` | Task 7 (CSS), Task 8 (template fallback), backend `userBadgeClass` (Phase 5) |
| §8 ✎ edit needs authoring read (URL/Kind/BadgeColor/Order) | Task 2 (`UserProviderViewer.UserProviderForm`), Task 4 (GET route), Task 10 (`editProvider`) |
| Invariant: chassis JS behavior tests (`node --test`) are the gate | Tasks 9–16 (each ships `*.behavior.test.js`), Task 17 |
| Invariant: JSON (not form-urlencoded) for catalog routes + same-origin | Task 10 (`postJSON`) |
| Invariant: CSP-safe class-based palette, no inline `style=` from user data | Task 7 + Task 8 |
| Invariant: one listener / html-template + htmx + vanilla JS | Tasks 4, 8, 10 (mount on existing mux + drawer) |
| Invariant: SSE poll-diff (fingerprint, emit on change) | Tasks 5, 6 |
| Invariant: reuse preset-bank drag-reorder, not new DnD | Task 9 |

**Placeholder scan:** every code step ships complete code; the only intentional stubs are `selectColor`/`renderGroups`/`renderChannels` in Task 10, which are explicitly replaced with full implementations in Tasks 11–12 (same file, same names) — flagged inline.

**Type/name consistency:** `EnumState` (Task 1) ↔ `UserChannelStatus.State` (Task 2) ↔ `channelStatusEnvelope.State` (Phase 5 type) ↔ `_statusChipLabel` (Task 16). `UserProviderForm` (Phase 5 type) reused as the read return (Task 2) and the GET response source (Task 4). `detectKind` (JS, Task 10) mirrors `detectChannelKind` (Go, Phase 5) — pinned by the Task 10 test against the same cases. Palette tokens identical across `badgeColorTokens` (Go), the CSS classes (Task 7), the swatch template list (Task 8), and `PALETTE` (Task 10).

**Code-review fixes applied (two read-only reviewers, both verified against `main`).** Resolved before this revision: the phantom `rebuildUserCatalogsLive` (Task 2 test now uses the merged `buildUserCatalogSnapshotLive`+`installUserCatalogSnapshot`); the `fakeResolver` single-`enumErr` limitation (Task 1 now uses three enumerators for ready/error/pending — the fake has no per-URL error map); `suggestGlyph('Lofi')` now returns `LO` (was `LOFI`); `collectChannelModels` now goes through a `kids()` slice helper (real-DOM `children` is an `HTMLCollection`, no `.map`); the template `slice` func switched to the existing `list`; `hasPrefix` is now an explicit registration step; the §9.8 delete warning is now precise via a `presets`-tracking Set; and the Task 16 catalog rebuild moved to **catalog-browser.js** (the owner of the drawer DOM + delegated handlers + clone helpers) and now reconstructs the hidden `catalog-tree-{id}` trees from the envelope — so newly-added providers are navigable without reload, with node-builder behavior tests pinning the shape against `preset-bank.html`.

**Residual notes for the implementer (not blockers, verified):** `DefaultConfig` is exported (`config.go:88`); `a.mu` is a plain `sync.Mutex` (the viewer's `Lock`/copy/`Unlock`-then-build is correct and matches `catalogProvidersInOrder`, contending with `Catalog()` at the same 4 Hz the existing code already does); `list`/`upper`/`pad2` template funcs exist, `hasPrefix` is added in Task 8; the SSE insertion line numbers in Tasks 5/6 are relative to the file *as Task 5 left it* (apply tasks in order); the `providerStatus` stale-fingerprint on delete-then-recreate of an identical ID is benign (slugs de-dupe with `-2`/`-3`, so true ID reuse effectively can't happen).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-06-03-user-custom-providers-phase6-chassis-authoring-ui.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best for a 17-task plan that mixes Go + CSS + template + vanilla JS; the per-task review catches the Phase-5 symbol-name confirmations and the Task 16 SSE-rebuild risk early.

**2. Inline Execution** — I execute tasks in this session using executing-plans, batch execution with checkpoints for review.

**Which approach?**

(If subagent-driven: **REQUIRED SUB-SKILL** superpowers:subagent-driven-development — fresh subagent per task + two-stage review. If inline: **REQUIRED SUB-SKILL** superpowers:executing-plans — batch execution with checkpoints. Per the repo memory `feedback_reviewer_subagent_tools`, restrict any review subagents to read-only tools — the Explore agent type, never general-purpose.)
