# User Custom Providers — Phase 3: Catalog Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the (already-merged) Phase 1 user-provider store and Phase 2 security helpers into the running Streams adapter so user-authored providers appear in the receiver catalog AND are castable, with play-time SSRF revalidation that the syntactic authoring-time check cannot provide.

**Architecture:** Build a `userProviderStore` in `New()` from `{data_dir}/user_providers.json`; merge its `Snapshot()` into the provider snapshot in BOTH the startup path (`buildStartupSnapshot`) and the remote-refresh path (`buildRemoteSnapshot`) so a background manifest refresh never wipes user providers; add a `user`-type catalog builder; expose user providers through `Catalog()`; and make user channels safe to cast by branching playback onto `userDirectInputPolicy()` (no `file` protocol) plus a play-time DNS-resolved-IP recheck (`validateUserProviderIP` on every resolved address) and adapter-side redirect prevalidation for `direct` items.

**Tech Stack:** Go 1.26, `net/netip` (IP classification), `net/http` (redirect HEAD walk), `net` (`net.DefaultResolver`), package `internal/adapters/streams`. Tests use the existing `fakeCore`/`fakeResolver` harness in `test_helpers_test.go` and new stub `hostResolver`/`httpDoer` seams.

---

## Background: what is already merged (do NOT re-create)

- `provider_user.go` — `userProviderType = "user"`, `userProviderIDPrefix = "user:"`, `kindPlaylist`/`kindSingle`/`kindDirect`, `isUserProviderID(id)`, `detectChannelKind`, `normalizeUserProvider`, `validateBadgeColor`, `normalizeBadgeColorForLoad`, `cloneUserProvider`. `ProviderDefinition.BadgeColor`, `ProviderDefinition.BadgeLabel`, `ChannelDefinition.Kind` exist (`provider.go:30-31,63`).
- `user_provider_store.go` — `newUserProviderStore(path) (*userProviderStore, error)` (self-healing load), `Snapshot() []ProviderDefinition`, `Put`, `Delete`. NOT yet built in `New()`.
- `url_security.go` — `validateUserProviderHost(raw)` (syntactic, authoring-time), `validateUserProviderIP(addr netip.Addr)` (handles IPv4-mapped via caller `.Unmap()` AND IPv4-compatible `::a.b.c.d` internally), `userDirectInputPolicy()` (mirrors `directHLSInputPolicy()` minus `file`). `userDirectInputPolicy()` is defined but NOT wired to any playback path.

## Security invariants (load-bearing — memory `project-netip-unmap-ssrf`)

- `validateUserProviderHost` is **syntactic only**: it does NOT resolve hostnames and does NOT cover non-canonical IP text (`http://2130706433/`, hex). The **play-time resolved-IP recheck is REQUIRED, not optional** — until it is wired, user channels must not be castable.
- Always call `validateUserProviderIP(resolvedAddr.Unmap())` on **every** resolved IP (`.Unmap()` for the IPv4-mapped case; `validateUserProviderIP` re-classifies IPv4-compatible `::/96` internally).
- Never let a user URL reach FFmpeg with `directHLSInputPolicy()` — it whitelists `file`. User `direct` items MUST use `userDirectInputPolicy()`.
- `DisableRedirects` emits **no FFmpeg flag** (`internal/ffmpeg/policy.go:34-60`). The adapter — not FFmpeg — must resolve and revalidate each `Location` hop (max 3) before the final URL reaches FFmpeg.

## Workflow conventions

- Go 1.26. `go test -race` CANNOT run locally (no cgo/gcc) — CI-only gate. Use plain `go test`. Run `go vet ./...`, `go test ./internal/adapters/streams/...`, and (where ffmpeg is on PATH) `go test -tags=integration ./...` locally; keep all four CI gates green.
- `docs/superpowers/` is gitignored — commit the plan and any new docs with `git add -f`. Stage ONLY the intended paths; verify with `git diff --cached --name-only` before each commit.
- `a.mu` is never held across network I/O. The snapshot is built BEFORE acquiring the lock and installed atomically via `installSnapshotLocked`. `buildUserCatalog` for `direct`/`single` is pure (no network), so it is safe in the build path. The play-time DNS recheck and redirect HEAD walk happen OUTSIDE `a.mu` (in the resolve phase of `playCurrentWithStarter`, after the lock is released at `playback.go:311`).

---

## File Structure

**Modified**
- `internal/adapters/streams/adapter.go` — add `userStore *userProviderStore` field + `New()` wiring; thread `a.userStore.Snapshot()` into the three `buildStartupSnapshot` call sites.
- `internal/adapters/streams/refresh.go` — `buildStartupSnapshot`/`buildRemoteSnapshot` take a `userProviders` param; `appendUserProviders` helper; `userProviderType` handled in `buildCachedOrSeedSnapshot`, `buildRemoteSnapshot`'s loop, and `refreshCatalogsDefault`; `buildProviderCatalog` gains a `user` case.
- `internal/adapters/streams/provider_user.go` — new `buildUserCatalog(def)`.
- `internal/adapters/streams/catalog.go` — `catalogProvidersInOrder()`; `Catalog()` emits enabled bundled + user providers; `buildChassisCatalogProvider` user badge/live/ungrouped-channel handling; `userBadgeClass` helper.
- `internal/adapters/streams/url_security.go` — `httpDoer`, `validateUserProviderResolvedHost`, `resolveUserDirectURL`, `newUserRedirectClient`, constants.
- `internal/adapters/streams/playback.go` — add `userURLResolver`/`userRedirectDoer` seams; gate `shouldBufferDirectHLS` off for user providers; branch the `direct` path onto `userDirectInputPolicy()` + redirect prevalidation; revalidate resolved `URL`+`AudioURL` for `single`/`playlist` items.

**Test files touched/created**
- `internal/adapters/streams/provider_user_test.go` (new) — `buildUserCatalog` per-kind.
- `internal/adapters/streams/refresh_test.go` — update `buildRemoteSnapshot` call (signature change); add merge tests.
- `internal/adapters/streams/adapter_test.go` — update `buildStartupSnapshotForApplyConfigValue` override closure (signature change); add store-wiring test.
- `internal/adapters/streams/catalog_test.go` — add user-provider catalog exposure tests.
- `internal/adapters/streams/url_security_test.go` — `validateUserProviderResolvedHost` + `resolveUserDirectURL` matrix (new stub `hostResolver`/`httpDoer`).
- `internal/adapters/streams/playback_test.go` — user `direct` policy + revalidation wiring tests.

---

## Task 1: Wire `userProviderStore` into the Adapter

**Files:**
- Modify: `internal/adapters/streams/adapter.go` (struct ~line 46-84, `New()` ~line 86-117)
- Test: `internal/adapters/streams/adapter_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/adapter_test.go`:

```go
func TestNew_BuildsUserProviderStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`{"version":1,"providers":[{"id":"user:demo","type":"user","display_name":"Demo","badge_label":"DM","badge_color":"teal","channels":[{"name":"Live","url":"https://example.com/stream.m3u8","kind":"direct"}]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "user_providers.json"), body, 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.userStore == nil {
		t.Fatal("a.userStore is nil; want a built store")
	}
	got := a.userStore.Snapshot()
	if len(got) != 1 || got[0].ID != "user:demo" {
		t.Fatalf("Snapshot = %+v, want one provider user:demo", got)
	}
}
```

Ensure `adapter_test.go`'s import block contains `"os"`, `"path/filepath"`, and `"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"` (add any that are missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestNew_BuildsUserProviderStore`
Expected: FAIL — compile error `a.userStore undefined (type *Adapter has no field or method userStore)`.

- [ ] **Step 3: Add the field and wire it in `New()`**

In `internal/adapters/streams/adapter.go`, add the field to the `Adapter` struct (next to `presetStore *presetStore` at line 75):

```go
	presetStore         *presetStore
	userStore           *userProviderStore
```

In `New()`, after the `presetStore` block (after line 115, before `return a, nil`), add:

```go
	a.userStore, err = newUserProviderStore(
		filepath.Join(cfg.Bridge.DataDir, "user_providers.json"),
	)
	if err != nil {
		return nil, fmt.Errorf("streams: user provider store: %w", err)
	}
```

(`err` is already declared by the `presetStore` assignment above; reuse it with `=`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestNew_BuildsUserProviderStore`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/adapter_test.go
git commit -m "feat(streams): build user provider store in adapter New()"
```

---

## Task 2: `user`-type catalog builder

**Files:**
- Modify: `internal/adapters/streams/provider_user.go` (add `buildUserCatalog`)
- Modify: `internal/adapters/streams/refresh.go` (`buildProviderCatalog` switch ~line 596-605)
- Test: `internal/adapters/streams/provider_user_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/streams/provider_user_test.go`:

```go
package streams

import "testing"

func userCatalogTestDef() ProviderDefinition {
	return ProviderDefinition{
		ID:          "user:mix",
		Type:        userProviderType,
		DisplayName: "Mix",
		BadgeLabel:  "MX",
		BadgeColor:  "teal",
		Groups:      []GroupDefinition{{ID: "g1", Name: "Group One"}},
		Channels: []ChannelDefinition{
			{ID: "live", Name: "Live", GroupID: "g1", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"},
			{ID: "vid", Name: "Single", GroupID: "g1", Kind: kindSingle, URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
			{ID: "list", Name: "Playlist", GroupID: "g1", Kind: kindPlaylist, URL: "https://www.youtube.com/playlist?list=PL123"},
		},
	}
}

func TestBuildUserCatalog_KindsProduceCorrectItems(t *testing.T) {
	t.Parallel()
	cat, err := buildUserCatalog(userCatalogTestDef())
	if err != nil {
		t.Fatalf("buildUserCatalog: %v", err)
	}
	if cat.ProviderID != "user:mix" || cat.Name != "Mix" {
		t.Fatalf("catalog identity = (%q,%q), want (user:mix, Mix)", cat.ProviderID, cat.Name)
	}
	// playlist channel is skipped in Phase 3; direct + single remain.
	if len(cat.Channels) != 2 {
		t.Fatalf("len(Channels) = %d, want 2 (playlist skipped)", len(cat.Channels))
	}
	byID := map[string]Channel{}
	for _, ch := range cat.Channels {
		byID[ch.ID] = ch
	}
	live, ok := byID["live"]
	if !ok || len(live.Items) != 1 || !live.Items[0].Direct || live.Items[0].URL != "https://cdn.example.com/live.m3u8" {
		t.Fatalf("direct channel item = %+v, want one Direct:true item with the m3u8 URL", live.Items)
	}
	vid, ok := byID["vid"]
	if !ok || len(vid.Items) != 1 || vid.Items[0].Direct || vid.Items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("single channel item = %+v, want one Direct:false item with the watch URL", vid.Items)
	}
	if _, ok := byID["list"]; ok {
		t.Fatalf("playlist channel should be skipped in Phase 3")
	}
}

func TestBuildProviderCatalog_DispatchesUserType(t *testing.T) {
	t.Parallel()
	cat, err := buildProviderCatalog(userCatalogTestDef(), nil, DefaultConfig())
	if err != nil {
		t.Fatalf("buildProviderCatalog(user): %v", err)
	}
	if cat.ProviderID != "user:mix" {
		t.Fatalf("ProviderID = %q, want user:mix", cat.ProviderID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestBuildUserCatalog_KindsProduceCorrectItems|TestBuildProviderCatalog_DispatchesUserType'`
Expected: FAIL — compile error `undefined: buildUserCatalog`.

- [ ] **Step 3: Implement `buildUserCatalog` and the dispatch case**

In `internal/adapters/streams/provider_user.go`, add `"log/slog"` and `"time"` to the import block, then append:

```go
// buildUserCatalog turns a normalized user ProviderDefinition into a
// ProviderCatalog. It mirrors buildDirectStreamsCatalog (provider_direct_streams.go)
// but branches per ChannelDefinition.Kind:
//   - direct → one StreamItem{URL, Direct:true} (straight to FFmpeg + the
//     user direct policy at play time).
//   - single → one StreamItem{URL, Direct:false} (resolved by yt-dlp at play time).
//   - playlist → SKIPPED in Phase 3 (Phase 4 adds yt-dlp --flat-playlist
//     enumeration); skipping logs and leaves the rest of the provider usable.
// It is pure (no network), so it is safe inside the snapshot build path.
func buildUserCatalog(def ProviderDefinition) (ProviderCatalog, error) {
	if def.Type != userProviderType {
		return ProviderCatalog{}, fmt.Errorf("provider %q type %q is unsupported", def.ID, def.Type)
	}
	groupByID := make(map[string]ChannelGroup, len(def.Groups))
	groups := make([]ChannelGroup, 0, len(def.Groups))
	for _, group := range def.Groups {
		g := ChannelGroup{ID: group.ID, Name: group.Name, Order: group.Order}
		groupByID[group.ID] = g
		groups = append(groups, g)
	}
	channels := make([]Channel, 0, len(def.Channels))
	for _, chDef := range def.Channels {
		switch chDef.Kind {
		case kindPlaylist:
			slog.Info("streams user provider: skipping playlist channel (enumeration is Phase 4)",
				"provider", def.ID, "channel", chDef.ID)
			continue
		case kindDirect, kindSingle:
		default:
			return ProviderCatalog{}, fmt.Errorf("provider %q channel %q: invalid kind %q", def.ID, chDef.ID, chDef.Kind)
		}
		ch := channelFromDefinition(chDef.ID, chDef, true, def)
		ch.Items = []StreamItem{{
			ID:       ch.ID,
			Title:    ch.Name,
			URL:      chDef.URL,
			SourceID: ch.ID,
			Direct:   chDef.Kind == kindDirect,
		}}
		if ch.GroupID != "" {
			if _, ok := groupByID[ch.GroupID]; !ok {
				return ProviderCatalog{}, fmt.Errorf("provider %q channel %q references unknown group %q", def.ID, ch.ID, ch.GroupID)
			}
		}
		channels = append(channels, ch)
	}
	sortChannelGroups(groups)
	sortChannels(channels)
	return ProviderCatalog{
		ProviderID: def.ID,
		Name:       def.DisplayName,
		Groups:     groups,
		Channels:   channels,
		UpdatedAt:  time.Now(),
	}, nil
}
```

In `internal/adapters/streams/refresh.go`, add a case to `buildProviderCatalog` (the switch at line 596-605):

```go
func buildProviderCatalog(def ProviderDefinition, raw []byte, cfg Config) (ProviderCatalog, error) {
	switch def.Type {
	case youtubeChannelJSONProviderType:
		return buildYouTubeChannelCatalog(def, raw, cfg)
	case directStreamsProviderType:
		return buildDirectStreamsCatalog(def)
	case userProviderType:
		return buildUserCatalog(def)
	default:
		return ProviderCatalog{}, fmt.Errorf("provider %q type %q is unsupported", def.ID, def.Type)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run 'TestBuildUserCatalog_KindsProduceCorrectItems|TestBuildProviderCatalog_DispatchesUserType'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/provider_user.go internal/adapters/streams/refresh.go internal/adapters/streams/provider_user_test.go
git commit -m "feat(streams): user-type catalog builder (direct/single; skip playlist)"
```

---

## Task 3: Merge user providers into the snapshot (startup + remote refresh)

This threads `userProviders` through `buildStartupSnapshot` and `buildRemoteSnapshot` so user providers appear at startup AND survive a successful background manifest refresh (without this, `refreshOnceDefault` → `buildRemoteSnapshot` → `installSnapshotLocked` would wipe them). The signature change breaks two existing call sites and two existing tests — they are updated in the same step so the package compiles.

**Files:**
- Modify: `internal/adapters/streams/refresh.go` (`buildStartupSnapshot` ~467, `buildCachedOrSeedSnapshot` ~474, `buildRemoteSnapshot` ~542, `refreshCatalogsDefault` ~344, `refreshOnceDefault` ~295)
- Modify: `internal/adapters/streams/adapter.go` (`ApplyConfig` ~339, `buildStartupSnapshot` method ~479-482, `ApplyConfigValue` ~537)
- Modify: `internal/adapters/streams/refresh_test.go` (existing `buildRemoteSnapshot` call ~174)
- Modify: `internal/adapters/streams/adapter_test.go` (existing override closure ~244)
- Test: `internal/adapters/streams/refresh_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/refresh_test.go`:

```go
func TestBuildStartupSnapshot_MergesUserProviders(t *testing.T) {
	t.Parallel()
	user := ProviderDefinition{
		ID:          "user:demo",
		Type:        userProviderType,
		DisplayName: "Demo",
		BadgeLabel:  "DM",
		BadgeColor:  "teal",
		Channels: []ChannelDefinition{
			{ID: "live", Name: "Live", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"},
		},
	}
	defs, cats, err := buildStartupSnapshot(t.Context(), DefaultConfig(), t.TempDir(), []ProviderDefinition{user})
	if err != nil {
		t.Fatalf("buildStartupSnapshot: %v", err)
	}
	if !containsProviderID(defs, "user:demo") {
		t.Fatalf("definitions missing user:demo: %v", providerIDs(defs))
	}
	if !containsCatalogID(cats, "user:demo") {
		t.Fatalf("catalogs missing user:demo")
	}
}

func TestBuildStartupSnapshot_RespectsUserProviderDisabled(t *testing.T) {
	t.Parallel()
	user := ProviderDefinition{
		ID: "user:demo", Type: userProviderType, DisplayName: "Demo", BadgeLabel: "DM", BadgeColor: "teal",
		Channels: []ChannelDefinition{{ID: "live", Name: "Live", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"}},
	}
	cfg := DefaultConfig()
	cfg.Providers = map[string]ProviderConfig{"user:demo": {Disabled: true}}
	defs, _, err := buildStartupSnapshot(t.Context(), cfg, t.TempDir(), []ProviderDefinition{user})
	if err != nil {
		t.Fatalf("buildStartupSnapshot: %v", err)
	}
	if containsProviderID(defs, "user:demo") {
		t.Fatalf("disabled user provider should be excluded: %v", providerIDs(defs))
	}
}

func TestBuildRemoteSnapshot_KeepsUserProviders(t *testing.T) {
	t.Parallel()
	user := ProviderDefinition{
		ID: "user:demo", Type: userProviderType, DisplayName: "Demo", BadgeLabel: "DM", BadgeColor: "teal",
		Channels: []ChannelDefinition{{ID: "live", Name: "Live", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"}},
	}
	snap, err := buildRemoteSnapshot(t.Context(), DefaultConfig(), Manifest{Version: 1}, t.TempDir(), []ProviderDefinition{user})
	if err != nil {
		t.Fatalf("buildRemoteSnapshot: %v", err)
	}
	if !containsProviderID(snap.Definitions, "user:demo") {
		t.Fatalf("remote snapshot dropped user:demo: %v", providerIDs(snap.Definitions))
	}
	if !containsCatalogID(snap.Catalogs, "user:demo") {
		t.Fatalf("remote snapshot missing user:demo catalog")
	}
}

func containsProviderID(defs []ProviderDefinition, id string) bool {
	for _, d := range defs {
		if d.ID == id {
			return true
		}
	}
	return false
}

func providerIDs(defs []ProviderDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.ID)
	}
	return out
}

func containsCatalogID(cats []ProviderCatalog, id string) bool {
	for _, c := range cats {
		if c.ProviderID == id {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestBuildStartupSnapshot_Merges|TestBuildStartupSnapshot_Respects|TestBuildRemoteSnapshot_Keeps'`
Expected: FAIL — compile error `too many arguments in call to buildStartupSnapshot` / `buildRemoteSnapshot`.

- [ ] **Step 3: Change signatures, add the merge, update all call sites**

In `internal/adapters/streams/refresh.go`, add the helper and update `buildStartupSnapshot`:

```go
// appendUserProviders appends user-authored providers to a merged provider
// list, skipping any whose per-provider override is Disabled — the same
// disable check mergeManifests applies to bundled/remote providers. User IDs
// carry the reserved "user:" prefix (validateUserProviderID), so they can
// never collide with bundled/remote IDs already in the slice.
func appendUserProviders(providers []ProviderDefinition, cfg Config, userProviders []ProviderDefinition) []ProviderDefinition {
	for _, up := range userProviders {
		if override, ok := cfg.Providers[up.ID]; ok && override.Disabled {
			continue
		}
		providers = append(providers, up)
	}
	return providers
}

func buildStartupSnapshot(ctx context.Context, cfg Config, cacheDir string, userProviders []ProviderDefinition) ([]ProviderDefinition, []ProviderCatalog, error) {
	cached := loadCachedManifest(ctx, cfg, cacheDir)
	bundled := sanitizeManifestArtwork(ctx, bundledManifest(), cfg, validateProviderArtworkURLSyntax)
	manifest := mergeManifests(cfg, bundled, cached, nil, remoteProviderFactories())
	manifest.Providers = appendUserProviders(manifest.Providers, cfg, userProviders)
	return buildCachedOrSeedSnapshot(manifest.Providers, cfg, cacheDir)
}
```

In `buildCachedOrSeedSnapshot` (line 477), widen the inline-build branch to also cover `user` (both are pure builders that need neither cache nor remote fetch):

```go
	for _, def := range defs {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildProviderCatalog(def, nil, cfg)
			if err != nil {
				return nil, nil, err
			}
			catalogs = append(catalogs, cat)
			continue
		}
```

Update `buildRemoteSnapshot` (line 542) to take and merge `userProviders`, and widen its loop's inline branch:

```go
func buildRemoteSnapshot(ctx context.Context, cfg Config, remote Manifest, cacheDir string, userProviders []ProviderDefinition) (remoteSnapshot, error) {
	bundled := sanitizeManifestArtwork(ctx, bundledManifest(), cfg, validateProviderArtworkURLSyntax)
	remote = sanitizeManifestArtwork(ctx, remote, cfg, validateProviderArtworkURL)
	manifest := mergeManifests(cfg, bundled, nil, &remote, remoteProviderFactories())
	manifest.Providers = appendUserProviders(manifest.Providers, cfg, userProviders)
	out := remoteSnapshot{
		Definitions:   manifest.Providers,
		Catalogs:      make([]ProviderCatalog, 0, len(manifest.Providers)),
		CatalogBodies: map[string][]byte{},
		CatalogMetas:  map[string]CacheMetadata{},
	}
	for _, def := range manifest.Providers {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildProviderCatalog(def, nil, cfg)
			if err != nil {
				return remoteSnapshot{}, fmt.Errorf("provider %q build catalog: %w", def.ID, err)
			}
			out.Catalogs = append(out.Catalogs, cat)
			continue
		}
		raw, meta, err := fetchProviderPlaylist(ctx, def, cfg, cacheDir)
		if err != nil {
			return remoteSnapshot{}, err
		}
		cat, err := buildProviderCatalog(def, raw, cfg)
		if err != nil {
			return remoteSnapshot{}, err
		}
		out.Catalogs = append(out.Catalogs, cat)
		out.CatalogBodies[def.ID] = raw
		out.CatalogMetas[def.ID] = meta
	}
	return out, nil
}
```

Update `refreshCatalogsDefault` (line 345) so the background catalog refresh treats user providers like direct providers (pure rebuild, no fetch) instead of trying to fetch their empty `PlaylistURL`:

```go
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildProviderCatalog(def, nil, cfg)
```

Update `refreshOnceDefault` (line 295) to pass the user snapshot into `buildRemoteSnapshot`:

```go
	snapshot, err := buildRemoteSnapshot(ctx, cfg, manifest, a.cacheDir, a.userStore.Snapshot())
```

In `internal/adapters/streams/adapter.go`, update the three call sites:

`ApplyConfig` (line 339):
```go
	defs, catalogs, err := buildStartupSnapshot(context.Background(), newCfg, a.cacheDir, a.userStore.Snapshot())
```

`buildStartupSnapshot` method (line 481):
```go
func (a *Adapter) buildStartupSnapshot(ctx context.Context) ([]ProviderDefinition, []ProviderCatalog, error) {
	cfg := a.configSnapshot()
	return buildStartupSnapshot(ctx, cfg, a.cacheDir, a.userStore.Snapshot())
}
```

`ApplyConfigValue` (line 537):
```go
	defs, catalogs, err := buildStartupSnapshotForApplyConfigValue(context.Background(), newCfg, a.cacheDir, a.userStore.Snapshot())
```

Update the existing test override in `internal/adapters/streams/adapter_test.go` (line 244) to match the new signature:

```go
	buildStartupSnapshotForApplyConfigValue = func(ctx context.Context, cfg Config, cacheDir string, userProviders []ProviderDefinition) ([]ProviderDefinition, []ProviderCatalog, error) {
```

Update the existing `buildRemoteSnapshot` call in `internal/adapters/streams/refresh_test.go` (line 174) to pass `nil`:

```go
	snapshot, err := buildRemoteSnapshot(t.Context(), cfg, remote, t.TempDir(), nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams/`
Expected: PASS (full package — confirms the signature change and all call sites/tests compile and pass together).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/refresh.go internal/adapters/streams/adapter.go internal/adapters/streams/refresh_test.go internal/adapters/streams/adapter_test.go
git commit -m "feat(streams): merge user providers into startup and remote snapshots"
```

---

## Task 4: Expose user providers through `Catalog()`

Make `Catalog()` emit enabled bundled providers (unchanged order) followed by user providers in definition order, and teach `buildChassisCatalogProvider` to render user-provider badges (from `BadgeLabel` + a `u-<token>` class), compute per-channel `Live` from channel kind, and include ungrouped channels (user providers may have no groups). `BundledCatalog()` stays settings-only and is left untouched.

**Files:**
- Modify: `internal/adapters/streams/catalog.go` (`Catalog()` ~23-34, `buildChassisCatalogProvider` ~66-102)
- Test: `internal/adapters/streams/catalog_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/catalog_test.go`:

```go
func userExposureTestDef() ProviderDefinition {
	return ProviderDefinition{
		ID:          "user:mix",
		Type:        userProviderType,
		DisplayName: "Mix",
		BadgeLabel:  "MX",
		BadgeColor:  "teal",
		// no groups → channels are ungrouped (flat)
		Channels: []ChannelDefinition{
			{ID: "live", Name: "Live", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"},
			{ID: "vid", Name: "Single", Kind: kindSingle, URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
	}
}

func TestBuildChassisCatalogProvider_UserBadgeLiveAndUngrouped(t *testing.T) {
	t.Parallel()
	p := buildChassisCatalogProvider(userExposureTestDef())
	if p.BadgeLabel != "MX" {
		t.Errorf("BadgeLabel = %q, want MX", p.BadgeLabel)
	}
	if p.BadgeClass != "u-teal" {
		t.Errorf("BadgeClass = %q, want u-teal", p.BadgeClass)
	}
	if p.Live {
		t.Errorf("user provider.Live = true, want false (only direct-streams providers are provider-level live)")
	}
	// ungrouped channels must still appear (synthetic group with empty ID).
	var live, vid *adapters.CatalogChannel
	for gi := range p.Groups {
		for ci := range p.Groups[gi].Channels {
			c := &p.Groups[gi].Channels[ci]
			switch c.ID {
			case "live":
				live = c
			case "vid":
				vid = c
			}
		}
	}
	if live == nil || vid == nil {
		t.Fatalf("ungrouped channels missing: live=%v vid=%v", live, vid)
	}
	if !live.Live {
		t.Errorf("direct channel Live = false, want true")
	}
	if vid.Live {
		t.Errorf("single channel Live = true, want false")
	}
}

func TestCatalog_EmitsBundledThenUser(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	// Install bundled defs + a user def (replaceDefinitionsForTest preserves order).
	a.replaceDefinitionsForTest([]ProviderDefinition{
		bundledMTVDefinition(),
		bundledCartoonDefinition(),
		bundledToonamiAftermathDefinition(),
		userExposureTestDef(),
	})
	cat := a.Catalog()
	if len(cat) != 4 {
		t.Fatalf("len(Catalog) = %d, want 4 (3 bundled + 1 user)", len(cat))
	}
	wantIDs := []string{"mtv-rewind", "cartoon-rewind", "toonami-aftermath", "user:mix"}
	for i, want := range wantIDs {
		if cat[i].ID != want {
			t.Errorf("Catalog[%d].ID = %q, want %q", i, cat[i].ID, want)
		}
	}
}

func TestCatalog_OmitsBundledProviderAbsentFromDefinitions(t *testing.T) {
	t.Parallel()
	// mergeManifests filters disabled bundled providers out of the installed
	// definitions (catalog.go doc comment), so a disabled bundled provider is
	// simply absent from definitions. Simulate that by installing only two of
	// the three bundled defs plus a user def: Catalog must skip the missing
	// bundled entry and still emit the rest in order.
	a := newTestAdapterWithCatalog(t)
	a.replaceDefinitionsForTest([]ProviderDefinition{
		bundledMTVDefinition(),
		bundledCartoonDefinition(),
		// toonami-aftermath intentionally omitted (stand-in for disabled→filtered)
		userExposureTestDef(),
	})
	cat := a.Catalog()
	wantIDs := []string{"mtv-rewind", "cartoon-rewind", "user:mix"}
	if len(cat) != len(wantIDs) {
		t.Fatalf("len(Catalog) = %d, want %d", len(cat), len(wantIDs))
	}
	for i, want := range wantIDs {
		if cat[i].ID != want {
			t.Errorf("Catalog[%d].ID = %q, want %q", i, cat[i].ID, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestBuildChassisCatalogProvider_UserBadge|TestCatalog_EmitsBundledThenUser'`
Expected: FAIL — `TestCatalog_EmitsBundledThenUser` returns 3 (user provider not emitted); `TestBuildChassisCatalogProvider_UserBadge...` fails on `BadgeClass`/ungrouped channels.

- [ ] **Step 3: Implement the ordered exposure and user-aware conversion**

In `internal/adapters/streams/catalog.go`, replace `Catalog()` (lines 23-34) with:

```go
func (a *Adapter) Catalog() []adapters.CatalogProvider {
	ordered := a.catalogProvidersInOrder()
	byID := make(map[string]ProviderDefinition, len(ordered))
	for _, d := range ordered {
		byID[d.ID] = d
	}
	out := make([]adapters.CatalogProvider, 0, len(ordered))
	// Bundled chassis providers first, in their fixed mockup order. Absent
	// entries (e.g. disabled → filtered from definitions) are skipped.
	for _, id := range bundledChassisCatalogProviderIDs {
		if d, ok := byID[id]; ok {
			out = append(out, buildChassisCatalogProvider(d))
		}
	}
	// User providers next, in definition (install/file) order.
	for _, d := range ordered {
		if isUserProviderID(d.ID) {
			out = append(out, buildChassisCatalogProvider(d))
		}
	}
	return out
}

// catalogProvidersInOrder returns the installed definitions in definitionOrder,
// seeding from bundledManifest() if Start-time installation has not run yet
// (local-only bootstrap; no remote fetch on a chassis render path).
func (a *Adapter) catalogProvidersInOrder() []ProviderDefinition {
	a.mu.Lock()
	if len(a.definitions) > 0 {
		out := make([]ProviderDefinition, 0, len(a.definitionOrder))
		for _, id := range a.definitionOrder {
			if d, ok := a.definitions[id]; ok {
				out = append(out, d)
			}
		}
		a.mu.Unlock()
		return out
	}
	a.mu.Unlock()

	m := bundledManifest()
	out := make([]ProviderDefinition, 0, len(m.Providers))
	out = append(out, m.Providers...)
	return out
}
```

Replace `buildChassisCatalogProvider` (lines 66-102) with the user-aware version:

```go
func buildChassisCatalogProvider(def ProviderDefinition) adapters.CatalogProvider {
	badge := providerBadges[def.ID]
	badgeLabel, badgeClass := badge.Label, badge.Class
	if isUserProviderID(def.ID) {
		// providerBadges has no user entries: fall back to the authored
		// glyph + a CSS class derived from the BadgeColor token. The
		// "u-<token>" classes (.ic.u-amber / .badge.u-amber, spec §8) are
		// added to chassis.css in Phase 6.
		badgeLabel = def.BadgeLabel
		badgeClass = userBadgeClass(def.BadgeColor)
	}
	// Only direct-streams providers are provider-level "always live". User
	// providers are mixed, so provider.Live stays false and liveness is
	// computed per channel from its Kind below.
	providerLive := def.Type == directStreamsProviderType
	origin := ""
	if u, err := url.Parse(def.BaseURL); err == nil {
		origin = u.Host
	}
	if origin == "" {
		if u, err := url.Parse(def.PlaylistURL); err == nil {
			origin = u.Host
		}
	}
	p := adapters.CatalogProvider{
		ID:             def.ID,
		DisplayName:    def.DisplayName,
		BadgeLabel:     badgeLabel,
		BadgeClass:     badgeClass,
		Origin:         origin,
		Kind:           def.Type,
		Live:           providerLive,
		DefaultChannel: def.DefaultChannel,
	}
	channelByGroup := groupChannels(def)
	appendChannel := func(cg *adapters.CatalogGroup, ch ChannelDefinition) {
		cg.Channels = append(cg.Channels, adapters.CatalogChannel{
			ID:       ch.ID,
			Name:     ch.Name,
			PlayMode: strings.ToUpper(string(ch.PlayMode)),
			Live:     providerLive || ch.Kind == kindDirect,
		})
	}
	for _, g := range def.Groups {
		cg := adapters.CatalogGroup{ID: g.ID, Name: g.Name}
		for _, ch := range channelByGroup[g.ID] {
			appendChannel(&cg, ch)
		}
		p.Groups = append(p.Groups, cg)
	}
	// Ungrouped channels (GroupID == "") — bundled providers have none, but
	// user providers may omit groups entirely (spec §8: "channels list flat").
	if ungrouped := channelByGroup[""]; len(ungrouped) > 0 {
		cg := adapters.CatalogGroup{ID: "", Name: ""}
		for _, ch := range ungrouped {
			appendChannel(&cg, ch)
		}
		p.Groups = append(p.Groups, cg)
	}
	return p
}

// userBadgeClass maps a user provider's BadgeColor token to the CSS class the
// chassis renders (spec §8: ".ic.u-<token>" / ".badge.u-<token>"). It runs the
// load-time normalizer so a malformed/empty token falls back to the default
// palette class instead of emitting "u-".
func userBadgeClass(token string) string {
	return "u-" + normalizeBadgeColorForLoad(token)
}
```

(`chassisCatalogSnapshot` at lines 107-130 becomes unused by `Catalog()` after this change — it has no other caller in the package. Leaving it compiles cleanly (Go does not flag unused methods), but it now duplicates `catalogProvidersInOrder`'s bootstrap logic; a follow-up commit in this phase (or Phase 5) should delete it to avoid drift. Removing it now is also acceptable — verify with `grep -rn chassisCatalogSnapshot internal/adapters/streams/` that nothing else references it first.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams/ -run 'TestCatalog|TestBuildChassisCatalogProvider'`
Expected: PASS (including the pre-existing `TestCatalog_*` bundled tests — order and badges for bundled providers are unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/catalog.go internal/adapters/streams/catalog_test.go
git commit -m "feat(streams): expose user providers in Catalog() with badge/live/ungrouped handling"
```

---

## Task 5: Play-time resolved-host validator (`single`/`playlist` SSRF guard)

Add `validateUserProviderResolvedHost` — the DNS-resolved-IP recheck that closes what the syntactic authoring check cannot (hostnames, decimal/hex IP encodings). This task adds the pure helper and its stub seams; Task 6 wires it into the resolve path.

**Files:**
- Modify: `internal/adapters/streams/url_security.go` (add imports + helper)
- Test: `internal/adapters/streams/url_security_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/streams/url_security_test.go`:

```go
package streams

import (
	"context"
	"fmt"
	"testing"
)

// stubHostResolver implements hostResolver (sourcefetch.Resolver) for tests.
type stubHostResolver struct {
	hosts map[string][]string
	err   error
}

func (s stubHostResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	if ips, ok := s.hosts[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("no such host %q", host)
}

func TestValidateUserProviderResolvedHost(t *testing.T) {
	t.Parallel()
	resolver := stubHostResolver{hosts: map[string][]string{
		"public.example.com":   {"93.184.216.34"},
		"lan.example.com":      {"192.168.1.50"},
		"rebind.example.com":   {"127.0.0.1"},
		"metadata.example.com": {"169.254.169.254"},
		"mixed.example.com":    {"93.184.216.34", "127.0.0.1"},
	}}
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"public hostname", "https://public.example.com/v.m3u8", false},
		{"lan hostname allowed", "https://lan.example.com/v.m3u8", false},
		{"ip literal public", "https://93.184.216.34/v.m3u8", false},
		{"ip literal lan", "http://10.0.0.5/v.m3u8", false},
		{"decimal loopback literal", "http://2130706433/v.m3u8", true},
		{"dns rebind to loopback", "https://rebind.example.com/v.m3u8", true},
		{"dns to cloud metadata", "https://metadata.example.com/v", true},
		{"any resolved ip blocked fails", "https://mixed.example.com/v.m3u8", true},
		{"ipv4-mapped loopback literal", "http://[::ffff:127.0.0.1]/v", true},
		{"ipv4-compatible loopback literal", "http://[::127.0.0.1]/v", true},
		{"unresolvable host", "https://nope.example.com/v", true},
		{"userinfo rejected", "https://user:pass@public.example.com/v", true},
		{"empty host", "https:///v", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUserProviderResolvedHost(context.Background(), resolver, tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("url %q: got nil error, want error", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("url %q: got error %v, want nil", tc.url, err)
			}
		})
	}
}
```

> Note: `http://2130706433/` parses to host `"2130706433"`, which is not a valid `netip.Addr`, so the helper falls through to DNS — the stub has no entry → error. That is the intended outcome (a decimal-encoded loopback never reaches FFmpeg). The test asserts rejection.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestValidateUserProviderResolvedHost`
Expected: FAIL — `undefined: validateUserProviderResolvedHost`.

- [ ] **Step 3: Implement the resolved-host validator**

In `internal/adapters/streams/url_security.go`, add `"context"` and `"net/http"` to the import block (alongside the existing `fmt`, `net/netip`, `net/url`, `strings`, `time`, and `internal/core`), then append:

```go
// httpDoer is the testable boundary around *http.Client for the user-direct
// redirect prevalidation walk (resolveUserDirectURL). Production wiring uses a
// no-redirect client (newUserRedirectClient); tests inject a stub.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

const (
	// maxUserRedirectHops bounds the adapter-side Location walk for user direct
	// streams (spec §7.2: "max 3 hops").
	maxUserRedirectHops = 3
	// userDirectProbeTimeout bounds each HEAD request in the redirect walk.
	userDirectProbeTimeout = 10 * time.Second
	// userResolvedHostLookupTimeout bounds DNS resolution during the play-time
	// resolved-URL recheck.
	userResolvedHostLookupTimeout = 10 * time.Second
)

// validateUserProviderResolvedHost enforces the §7.1 "allow LAN, block
// internals" posture against a URL at DEREFERENCE time. Unlike the syntactic
// validateUserProviderHost (authoring time), this RESOLVES hostnames and
// classifies every returned address — closing DNS-rebind, decimal/hex IP
// encodings, and hostnames that resolve to blocked ranges. An IP-literal host
// is classified directly; a hostname is resolved via resolver.LookupHost and
// EVERY resolved address must pass validateUserProviderIP(addr.Unmap()).
func validateUserProviderResolvedHost(ctx context.Context, resolver hostResolver, rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed in url")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return validateUserProviderIP(addr.Unmap())
	}
	ips, err := resolver.LookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to no addresses", host)
	}
	for _, ipStr := range ips {
		addr, err := netip.ParseAddr(ipStr)
		if err != nil {
			return fmt.Errorf("resolved address %q: %w", ipStr, err)
		}
		if err := validateUserProviderIP(addr.Unmap()); err != nil {
			return fmt.Errorf("resolved address %q: %w", ipStr, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestValidateUserProviderResolvedHost`
Expected: PASS (all sub-cases).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/url_security.go internal/adapters/streams/url_security_test.go
git commit -m "feat(streams): play-time resolved-host SSRF validator for user URLs"
```

---

## Task 6: Adapter-side redirect prevalidation for user `direct` URLs

Add `resolveUserDirectURL` — the bounded Location walk that revalidates every hop's resolved host before the final URL reaches FFmpeg (the `DisableRedirects` contract in `policy.go`). It reuses `validateUserProviderResolvedHost` per hop.

**Files:**
- Modify: `internal/adapters/streams/url_security.go` (add helper + default client)
- Test: `internal/adapters/streams/url_security_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/url_security_test.go` (add `"io"`, `"net/http"`, `"strings"` to its import block):

```go
// stubDoer maps absolute request URLs to canned responses for the redirect walk.
type stubDoer struct {
	resp map[string]*http.Response
	err  error
}

func (s stubDoer) Do(req *http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	if r, ok := s.resp[req.URL.String()]; ok {
		return r, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func redirectResp(location string) *http.Response {
	h := http.Header{}
	h.Set("Location", location)
	return &http.Response{StatusCode: 302, Header: h, Body: io.NopCloser(strings.NewReader(""))}
}

func TestResolveUserDirectURL(t *testing.T) {
	t.Parallel()
	resolver := stubHostResolver{hosts: map[string][]string{
		"a.example.com": {"93.184.216.34"},
		"b.example.com": {"93.184.216.35"},
		"evil.example.com": {"169.254.169.254"},
	}}

	t.Run("no redirect returns original", func(t *testing.T) {
		final, err := resolveUserDirectURL(context.Background(), stubDoer{}, resolver, "https://a.example.com/s.m3u8", maxUserRedirectHops)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if final != "https://a.example.com/s.m3u8" {
			t.Fatalf("final = %q, want original", final)
		}
	})

	t.Run("one safe redirect followed and revalidated", func(t *testing.T) {
		doer := stubDoer{resp: map[string]*http.Response{
			"https://a.example.com/s.m3u8": redirectResp("https://b.example.com/real.m3u8"),
		}}
		final, err := resolveUserDirectURL(context.Background(), doer, resolver, "https://a.example.com/s.m3u8", maxUserRedirectHops)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if final != "https://b.example.com/real.m3u8" {
			t.Fatalf("final = %q, want redirect target", final)
		}
	})

	t.Run("redirect to blocked host rejected", func(t *testing.T) {
		doer := stubDoer{resp: map[string]*http.Response{
			"https://a.example.com/s.m3u8": redirectResp("https://evil.example.com/meta"),
		}}
		_, err := resolveUserDirectURL(context.Background(), doer, resolver, "https://a.example.com/s.m3u8", maxUserRedirectHops)
		if err == nil {
			t.Fatal("got nil error, want rejection of metadata-host redirect")
		}
	})

	t.Run("too many hops rejected", func(t *testing.T) {
		doer := stubDoer{resp: map[string]*http.Response{
			"https://a.example.com/0": redirectResp("https://a.example.com/1"),
			"https://a.example.com/1": redirectResp("https://a.example.com/2"),
			"https://a.example.com/2": redirectResp("https://a.example.com/3"),
			"https://a.example.com/3": redirectResp("https://a.example.com/4"),
		}}
		_, err := resolveUserDirectURL(context.Background(), doer, resolver, "https://a.example.com/0", maxUserRedirectHops)
		if err == nil {
			t.Fatal("got nil error, want redirect-chain-exceeded")
		}
	})

	t.Run("first-hop blocked host rejected before any request", func(t *testing.T) {
		_, err := resolveUserDirectURL(context.Background(), stubDoer{}, resolver, "https://evil.example.com/s.m3u8", maxUserRedirectHops)
		if err == nil {
			t.Fatal("got nil error, want rejection of metadata host")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestResolveUserDirectURL`
Expected: FAIL — `undefined: resolveUserDirectURL`.

- [ ] **Step 3: Implement the redirect walk + default client**

Append to `internal/adapters/streams/url_security.go`:

```go
// resolveUserDirectURL walks up to maxHops HTTP redirects for a user direct
// (m3u8/HLS) URL, re-running validateUserProviderResolvedHost on EVERY hop
// before any request is issued, and returns the final non-redirect URL to hand
// to FFmpeg. This is the adapter-side prevalidation the DisableRedirects policy
// contract requires (internal/ffmpeg/policy.go:34-60): FFmpeg emits no
// redirect-disabling flag, so the adapter must resolve Location chains itself.
func resolveUserDirectURL(ctx context.Context, doer httpDoer, resolver hostResolver, rawURL string, maxHops int) (string, error) {
	current := strings.TrimSpace(rawURL)
	for hop := 0; hop <= maxHops; hop++ {
		if err := validateUserProviderResolvedHost(ctx, resolver, current); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, current, nil)
		if err != nil {
			return "", fmt.Errorf("build HEAD request: %w", err)
		}
		resp, err := doer.Do(req)
		if err != nil {
			return "", fmt.Errorf("probe redirect chain: %w", err)
		}
		status := resp.StatusCode
		location := resp.Header.Get("Location")
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if status < 300 || status > 399 {
			return current, nil
		}
		if location == "" {
			return "", fmt.Errorf("redirect missing Location header")
		}
		base, err := url.Parse(current)
		if err != nil {
			return "", err
		}
		ref, err := url.Parse(location)
		if err != nil {
			return "", fmt.Errorf("invalid redirect location: %w", err)
		}
		current = base.ResolveReference(ref).String()
	}
	return "", fmt.Errorf("redirect chain exceeded %d hops", maxHops)
}

// newUserRedirectClient is the production httpDoer: a client that surfaces each
// redirect response (CheckRedirect returns ErrUseLastResponse) so the adapter
// validates and follows Location headers itself, bounded by a per-request
// timeout.
func newUserRedirectClient() *http.Client {
	return &http.Client{
		Timeout: userDirectProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestResolveUserDirectURL`
Expected: PASS (all sub-cases).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/url_security.go internal/adapters/streams/url_security_test.go
git commit -m "feat(streams): adapter-side redirect prevalidation for user direct URLs"
```

---

## Task 7: Wire user `direct` playback — policy, redirect prevalidation, HLS-buffer gate

Branch the `direct` playback path so user direct items use `userDirectInputPolicy()` (no `file`) and a prevalidated final URL, and ensure user direct items do NOT route through the bundled HLS buffer (whose `TrustModeBundledToonami` validator only accepts the bundled Toonami host — a user `.m3u8` would be rejected there). HLS buffering for user providers is future work.

**Files:**
- Modify: `internal/adapters/streams/playback.go` (struct seams; `shouldBufferDirectHLS` ~549-566; direct branch ~318-417; add helper)
- Modify: `internal/adapters/streams/adapter.go` (`Adapter` struct: add `userURLResolver`, `userRedirectDoer` fields)
- Test: `internal/adapters/streams/playback_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/playback_test.go` (ensure its import block has `"context"`, `"io"`, `"net/http"`, `"strings"`, `"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"`, `"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"` — add only the missing ones):

```go
func installUserDirectAdapter(t *testing.T) (*Adapter, *fakeCore) {
	t.Helper()
	a, c := newTestAdapterWithFakeCore(t)
	def := ProviderDefinition{
		ID: "user:cdn", Type: userProviderType, DisplayName: "CDN", BadgeLabel: "CD", BadgeColor: "teal",
		Channels: []ChannelDefinition{{ID: "live", Name: "Live", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"}},
	}
	cat, err := buildUserCatalog(def)
	if err != nil {
		t.Fatalf("buildUserCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"cdn.example.com": {"93.184.216.34"}}}
	a.userRedirectDoer = stubDoer{} // no redirect → 200
	return a, c
}

func TestUserDirect_UsesUserPolicyAndPrevalidatedURL(t *testing.T) {
	a, c := installUserDirectAdapter(t)
	enableBridgeHLSBufferForTest(a) // even with the buffer enabled, user direct must skip it
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		t.Fatal("hlsBufferOpen must not be called for user providers")
		return nil, nil
	}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:cdn", ChannelID: "live"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if c.lastReq.StreamURL != "https://cdn.example.com/live.m3u8" {
		t.Fatalf("StreamURL = %q, want prevalidated direct URL", c.lastReq.StreamURL)
	}
	want := userDirectInputPolicy()
	got := c.lastReq.MediaInputPolicy
	for _, p := range got.ProtocolWhitelist {
		if p == "file" {
			t.Fatalf("user direct policy must not whitelist 'file': %v", got.ProtocolWhitelist)
		}
	}
	if len(got.ProtocolWhitelist) != len(want.ProtocolWhitelist) {
		t.Fatalf("ProtocolWhitelist = %v, want %v", got.ProtocolWhitelist, want.ProtocolWhitelist)
	}
}

func TestUserDirect_BlockedRedirectFailsCast(t *testing.T) {
	a, c := installUserDirectAdapter(t)
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{
		"cdn.example.com":  {"93.184.216.34"},
		"evil.example.com": {"169.254.169.254"},
	}}
	a.userRedirectDoer = stubDoer{resp: map[string]*http.Response{
		"https://cdn.example.com/live.m3u8": redirectResp("https://evil.example.com/meta"),
	}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:cdn", ChannelID: "live"})
	if err == nil {
		t.Fatal("StartResolvedStream succeeded, want failure on metadata-host redirect")
	}
	if c.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0 (URL never reaches FFmpeg)", c.startCalls)
	}
}
```

> `enableBridgeHLSBufferForTest` is already defined in `playback_test.go` (used e.g. at line 410; locate it with `grep -n 'func enableBridgeHLSBufferForTest' internal/adapters/streams/playback_test.go`). `redirectResp`/`stubDoer`/`stubHostResolver` come from `url_security_test.go` (same package).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestUserDirect_'`
Expected: FAIL — compile error `a.userURLResolver undefined` / `a.userRedirectDoer undefined`.

- [ ] **Step 3: Add the seams, the HLS gate, and the direct-branch wiring**

In `internal/adapters/streams/adapter.go`, add fields to the `Adapter` struct (next to `hlsBufferOpen hlsBufferOpener` at line 53):

```go
	hlsBufferOpen hlsBufferOpener

	// User-provider play-time SSRF seams (lazily defaulted in playback.go).
	userURLResolver  hostResolver
	userRedirectDoer httpDoer
```

In `internal/adapters/streams/playback.go`, add `"net"` to the import block.

Gate `shouldBufferDirectHLS` (line 549) so user providers never enter the bundled buffer — insert at the top of the function body:

```go
func (a *Adapter) shouldBufferDirectHLS(q *ActiveQueue, item StreamItem) bool {
	if isUserProviderID(q.ProviderID) {
		// User direct streams are not host-locked and the bundled buffer's
		// TrustModeBundledToonami validator only accepts the Toonami host, so
		// user .m3u8 must bypass the buffer and go straight to FFmpeg with the
		// user direct policy. HLS buffering for user providers is future work.
		return false
	}
	if !item.Direct || !a.bridge.HLSBuffer.Enabled {
		return false
	}
```

Add the prevalidation helper and lazy seam accessors at the end of `playback.go`:

```go
func (a *Adapter) userHostResolver() hostResolver {
	if a.userURLResolver != nil {
		return a.userURLResolver
	}
	return net.DefaultResolver
}

func (a *Adapter) userRedirectHTTPDoer() httpDoer {
	if a.userRedirectDoer != nil {
		return a.userRedirectDoer
	}
	return newUserRedirectClient()
}

// prevalidateUserDirectURL revalidates and follows the redirect chain for a
// user direct URL, returning the final URL safe to hand to FFmpeg.
func (a *Adapter) prevalidateUserDirectURL(ctx context.Context, rawURL string) (string, error) {
	return resolveUserDirectURL(ctx, a.userRedirectHTTPDoer(), a.userHostResolver(), rawURL, maxUserRedirectHops)
}

// revalidateResolvedUserURLs runs the play-time resolved-host SSRF recheck on
// each non-empty URL (video + audio for DASH dual-stream resolves).
func (a *Adapter) revalidateResolvedUserURLs(ctx context.Context, urls ...string) error {
	lookupCtx, cancel := context.WithTimeout(ctx, userResolvedHostLookupTimeout)
	defer cancel()
	resolver := a.userHostResolver()
	for _, u := range urls {
		if strings.TrimSpace(u) == "" {
			continue
		}
		if err := validateUserProviderResolvedHost(lookupCtx, resolver, u); err != nil {
			return err
		}
	}
	return nil
}
```

In the direct branch of `playCurrentWithStarter`, replace the single line `mediaPolicy := directHLSInputPolicy()` (line 329) with the user-aware policy + prevalidation. The insertion runs while `resolveCtx` is still live (it is cancelled at line 372) and `shouldBufferDirectHLS` returns false for user providers, so the buffer block below is skipped and `playbackURL`/`mediaPolicy` keep the user values:

```go
			playbackURL := pageURL
			mediaPolicy := directHLSInputPolicy()
			if isUserProviderID(q.ProviderID) {
				mediaPolicy = userDirectInputPolicy()
				finalURL, err := a.prevalidateUserDirectURL(resolveCtx, pageURL)
				if err != nil {
					cancel()
					a.clearResolveIfCurrent(capture)
					slog.Warn("streams playback blocked unsafe user direct url",
						"provider", q.ProviderID, "channel", q.ChannelID, "item", capture.ItemID, "err", err)
					return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "stream url is not allowed")
				}
				playbackURL = finalURL
			}
```

(Remove the original `playbackURL := pageURL` line that previously preceded `mediaPolicy := directHLSInputPolicy()` at line 328 — it is folded into the block above. The subsequent `if a.shouldBufferDirectHLS(...)` block is unchanged.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams/ -run 'TestUserDirect_|TestStreamsDirect'`
Expected: PASS (new user-direct tests pass; pre-existing Toonami direct/HLS tests still pass — bundled providers are unaffected).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/playback.go internal/adapters/streams/adapter.go internal/adapters/streams/playback_test.go
git commit -m "feat(streams): cast user direct items with userDirectInputPolicy + redirect prevalidation"
```

---

## Task 8: Wire resolved-URL revalidation into the `single`/`playlist` playback path

Revalidate the yt-dlp-resolved media `URL` and `AudioURL` (DASH dual-stream) against the §7.1 IP rules before they reach FFmpeg. On a block, advance to the next queue item (mirroring the existing "no playable media" failure handling) rather than crashing the queue.

**Files:**
- Modify: `internal/adapters/streams/playback.go` (resolve path, after the nil/empty-URL check ~465, before building the `req` at ~468)
- Test: `internal/adapters/streams/playback_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/playback_test.go`:

```go
func installUserSingleAdapter(t *testing.T, resolved *ytdlp.Resolution) (*Adapter, *fakeCore) {
	t.Helper()
	a, c := newTestAdapterWithFakeCore(t)
	def := ProviderDefinition{
		ID: "user:tw", Type: userProviderType, DisplayName: "TW", BadgeLabel: "TW", BadgeColor: "purple",
		Channels: []ChannelDefinition{{ID: "vod", Name: "VOD", Kind: kindSingle, URL: "https://www.twitch.tv/foo"}},
	}
	cat, err := buildUserCatalog(def)
	if err != nil {
		t.Fatalf("buildUserCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	a.resolver = &fakeResolver{res: resolved}
	return a, c
}

func TestUserSingle_BlockedResolvedHostFailsCast(t *testing.T) {
	a, c := installUserSingleAdapter(t, &ytdlp.Resolution{URL: "https://media.evil.com/v.mp4"})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"media.evil.com": {"169.254.169.254"}}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:tw", ChannelID: "vod"})
	if err == nil {
		t.Fatal("StartResolvedStream succeeded, want failure on metadata resolved host")
	}
	if c.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0 (resolved URL never reaches FFmpeg)", c.startCalls)
	}
}

func TestUserSingle_BlockedAudioURLFailsCast(t *testing.T) {
	a, c := installUserSingleAdapter(t, &ytdlp.Resolution{
		URL:      "https://media.ok.com/v.mp4",
		AudioURL: "https://media.evil.com/a.mp4",
	})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{
		"media.ok.com":   {"93.184.216.34"},
		"media.evil.com": {"127.0.0.1"},
	}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:tw", ChannelID: "vod"})
	if err == nil {
		t.Fatal("StartResolvedStream succeeded, want failure on blocked AudioURL")
	}
	if c.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0", c.startCalls)
	}
}

func TestUserSingle_SafeResolvedHostCasts(t *testing.T) {
	a, c := installUserSingleAdapter(t, &ytdlp.Resolution{URL: "https://media.ok.com/v.mp4"})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"media.ok.com": {"93.184.216.34"}}}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:tw", ChannelID: "vod"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if c.lastReq.StreamURL != "https://media.ok.com/v.mp4" {
		t.Fatalf("StreamURL = %q, want resolved URL", c.lastReq.StreamURL)
	}
}
```

Ensure `playback_test.go` imports `"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"` (it is already used elsewhere in the package; add to this file's import block if missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestUserSingle_'`
Expected: FAIL — `TestUserSingle_BlockedResolvedHostFailsCast`/`...BlockedAudioURLFailsCast` succeed in casting (no revalidation yet) → `core start calls = 1`.

- [ ] **Step 3: Wire revalidation into the resolve path**

In `internal/adapters/streams/playback.go`, in the non-direct resolve path, insert the revalidation immediately AFTER the `resolved == nil || strings.TrimSpace(resolved.URL) == ""` block (after line 465) and BEFORE `title := streamSessionTitle(item, resolved.Title)` (line 466):

```go
	if isUserProviderID(q.ProviderID) {
		if err := a.revalidateResolvedUserURLs(ctx, resolved.URL, resolved.AudioURL); err != nil {
			slog.Warn("streams playback blocked unsafe resolved user url",
				"provider", q.ProviderID,
				"channel", q.ChannelID,
				"item", capture.ItemID,
				"err", err,
			)
			if next, ok := a.recordStartFailureAndAdvance(capture, "resolved stream url is not allowed"); ok {
				a.runBeforeQueueContinuation()
				return a.playCurrentWithStarter(ctx, next, starter)
			}
			return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "resolved stream url is not allowed")
		}
	}
```

(`ctx` here is the parent context — the per-resolve `resolveCtx` was already cancelled at line 432; `revalidateResolvedUserURLs` derives its own bounded lookup context.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams/ -run 'TestUserSingle_'`
Expected: PASS (blocked URL + blocked AudioURL fail with 0 core starts; safe URL casts).

- [ ] **Step 5: Full-package + vet verification**

Run:
```bash
go vet ./internal/adapters/streams/...
go test ./internal/adapters/streams/
```
Expected: vet clean; all tests PASS (Phase 3 surface is self-contained in the streams package; no integration-tagged changes in this phase).

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/playback.go internal/adapters/streams/playback_test.go
git commit -m "feat(streams): revalidate yt-dlp-resolved URL+AudioURL for user providers before FFmpeg"
```

---

## Accepted residual risk (documented, not a gap)

- **HLS child-segment fetches for user `direct` streams.** FFmpeg fetches the manifest's child segment URLs directly (no per-child adapter validation) because user direct items bypass the bundled HLS buffer. The top-level URL + its redirect chain are prevalidated (Task 6/7), `DisableReconnect` mitigates server-side rebind, and `userDirectInputPolicy` constrains protocols (no `file`). This matches the operator/LAN trust boundary the spec accepts (§7.3, §7.4). A LAN-aware HLS-buffer trust mode for user providers is future work.
- **yt-dlp page-URL dereference.** yt-dlp resolves the page URL itself and may follow redirects before the adapter sees an output URL (spec §7.3). The §7.1 authoring check reduces but does not eliminate this; the Task 8 output-URL revalidation is the FFmpeg-side guard. Unchanged from today's quick-cast URL path.

## Out of scope for Phase 3 (later phases)

- Playlist enumeration (`yt-dlp --flat-playlist`), cache keys, async pending/error states — **Phase 4** (`playlist` channels are skipped with an info log here).
- Chassis routes (`POST/PUT/DELETE /receiver/catalog/provider`, Verify, reorder), `UserProviderEditor` interface, live catalog rebuild after an in-app edit, `EnsureStarted` auto-enable/hot-start — **Phase 5**. (Phase 3 factored the merge through `buildStartupSnapshot(..., userProviders)` + `a.userStore.Snapshot()`, so a Phase 5 route can rebuild by calling `buildStartupSnapshot` and `installSnapshotLocked` atomically off-lock.)
- `chassis.css` `.ic.u-<token>` / `.badge.u-<token>` palette classes and the authoring form template/JS — **Phase 6** (`buildChassisCatalogProvider` already emits the `u-<token>` `BadgeClass` they target).

---

## Self-Review

**Spec coverage (§4 / §5 / §7.2 / §8 / §10):**
- §4.1/§4.2 store load + merge → Task 1 (build store), Task 3 (merge at startup + remote refresh).
- §4.3 `Type:"user"`, `BadgeColor`, `Kind` → already merged; consumed by Tasks 2 & 4.
- §4.4 channel kinds → Task 2 (`direct`→`Direct:true`, `single`→`Direct:false`, `playlist` skipped Phase 3).
- §5 playback reuse + "Catalog() must switch to enabled bundled + user providers" → Task 4; per-kind FFmpeg routing → Tasks 7 & 8.
- §7.2 play-time defense in depth: `userDirectInputPolicy` (no `file`) → Task 7; resolved `URL`+`AudioURL` IP recheck → Tasks 5 & 8; redirect prevalidation per `DisableRedirects` contract → Tasks 6 & 7.
- §8 badge rendering: `BadgeLabel` + `u-<token>` class fallback, load-time normalization defense → Task 4.
- §10 "edits are data not TOML" / merge is factored for Phase 5 → Task 3 note; no `ApplyScope` change made. Auto-enable/hot-start explicitly deferred to Phase 5.

**Placeholder scan:** No `TBD`/`handle edge cases`/"similar to Task N" — every code step is complete. Each failing test names the exact `go test -run` invocation and expected failure mode.

**Type consistency:** `buildStartupSnapshot`/`buildRemoteSnapshot` 4-/5-arg signatures are used identically across impl, the three `adapter.go` call sites, the `refreshOnceDefault` call, and both updated tests. `validateUserProviderResolvedHost(ctx, hostResolver, string)`, `resolveUserDirectURL(ctx, httpDoer, hostResolver, string, int)`, `userDirectInputPolicy()`, `validateUserProviderIP(addr.Unmap())`, `isUserProviderID`, `userProviderType`, `kindDirect`/`kindSingle`/`kindPlaylist`, `normalizeBadgeColorForLoad` all match their definitions in the already-merged files. `hostResolver`/`httpDoer` seam fields and their lazy accessors (`userHostResolver`/`userRedirectHTTPDoer`) are named consistently between `adapter.go` (fields) and `playback.go` (accessors + helpers).
