# Receiver Chassis Adapters Pane (Simple) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Phase 4D of the receiver chassis settings drawer — replace the single "Spec 4D–4F" Adapters-pane stub with six adapter sections (3 functional: DLNA, Torrent, Streams; 3 stubs: Plex, Jellyfin, URL), an additive `*uiserver.AdapterSaver.SaveTouched` method paralleling 4A's `BridgeSaver.SaveTouched`, two chassis-owned interfaces (`AdapterSettingsSaver`, `StreamsRefresher`), and one streams-refresh action.

**Architecture:** Phase 4D rides every 4A/4B/4C primitive without redesign: the `field` template helper, JSON envelope, scope vocabulary, `requireSameOrigin`, `*settingsError` wrapper, and chassis-isolation contract. The structural new work is `*uiserver.AdapterSaver.SaveTouched(name, touched, adapter, fields)` — it mirrors `BridgeSaver.SaveTouched`'s read-overlay-validate-write-apply pipeline and is the load-bearing fix for per-field auto-save. The 4-arg signature carries the writable-field allowlist so the saver can reject keys the chassis projected out (per-provider `disabled` / `hls_buffer_disabled`, owned by 4C's Catalog pane). Three per-adapter section templates render the 4+10+15 fields; one container template stacks them with three stub siblings. Two thin production wrappers in `cmd/mister-groovy-relay/` bind the chassis-owned interfaces to `*uiserver.AdapterSaver` and `*streams.Adapter`.

**Tech Stack:** Go 1.26 stdlib (`html/template`, `net/http`, `sync`), BurntSushi/toml for round-trip decode/encode in `SaveTouched`, embedded HTML/CSS/JS via `go:embed`, vanilla ES2022.

**Spec reference:** [`docs/superpowers/specs/2026-05-28-receiver-chassis-adapters-simple-pane-design.md`](../specs/2026-05-28-receiver-chassis-adapters-simple-pane-design.md).

---

## Prerequisites

**Branch 4D from `phase-4c-catalog-pane`, not from `main`.** Phase 4C is in-progress on that branch; its chassis interfaces, types, `SettingsData` extensions, `chassis.Config` fields, and production wrappers are already committed. Branching 4D from 4C's tip gives 4D the real `CatalogSettingsManager` / `CatalogProviderState` / `chassis.Config.CatalogManager` directly, without needing a fallback shim. When 4C eventually merges to `main`, 4D rebases — most of the chassis-side overlap is already in 4D's base, so the rebase should be near-clean.

```bash
git worktree add ../MiSTer_GroovyRelay-4d -b phase-4d-adapters-pane phase-4c-catalog-pane
cd ../MiSTer_GroovyRelay-4d
# Run 4D's subagent-driven execution here.
```

**Verify the 4C contract is in the branch before starting Task 1:**
```bash
grep -n "CatalogSettingsManager\|CatalogProviderState\|CatalogManager" internal/chassis/settings.go internal/chassis/data.go internal/chassis/server.go
```
Expected: matches in each file. The current 4C branch tip (`1e72c87` or later) includes the `CatalogProviderState.CatalogRefreshHours int` field 4D's Streams pane consumes — confirm via:
```bash
grep -n "CatalogRefreshHours" internal/chassis/settings.go
```
Expected: one match on `CatalogProviderState`. If missing, the contract patch was rolled back; coordinate with whoever owns the 4C branch before proceeding.

**The 4C contract 4D consumes (verified against the 4C branch as of `1e72c87`):**

| Symbol | Location | Used by 4D for |
|---|---|---|
| `chassis.CatalogSettingsManager` interface | `internal/chassis/settings.go` | Source of provider list for the Streams pane per-provider sub-section |
| `CatalogSettingsManager.Providers() []CatalogProviderState` | same | Read at drawer-paint time |
| `chassis.CatalogProviderState` with fields `ID`, `DisplayName`, `ChannelCount`, `CatalogRefreshHours` (int) | same | Render-side data for per-provider override rows |
| `chassis.Config.CatalogManager` field | `internal/chassis/server.go` | Where the production binding plugs in |
| `chassis.SettingsData.CatalogChannelCount` int | `internal/chassis/data.go` | Streams section header N count |
| `chassis.SettingsData.CatalogProviders []CatalogProviderState` | same | Already populated by 4C; 4D could read this slice directly instead of calling `CatalogManager.Providers()` a second time |
| `buildSettingsData(bridge, registry, catalog, catalogManager) SettingsData` | `internal/chassis/data.go` | 4C already extends this signature; 4D's wrapper method calls it with the same args + appends `Adapters` |

**Last-resort fallback (NOT recommended; document only):** If 4C is catastrophically delayed or invalidated, declare a 4D-local interface `StreamsProviderLister` wrapping `streams.Adapter.Catalog()` directly, with a `cmd/mister-groovy-relay/streams_provider_lister.go` production binding. Tasks 17-19 substitute this interface for `CatalogManager`. Reserved for the case where the 4C branch never reaches `main`; do not adopt as a parallel path.

**4A and 4B must remain intact.** This plan is purely additive over 4A's drawer chrome, field renderer, and JSON envelope, plus 4B's Pipeline/Advanced helpers. The bridge save handler at `internal/chassis/settings.go:577` (`handleSettingsBridgePost`) and the JS handlers it consumes are modified only via additive selector narrowing (Task 29) — no behavior change for bridge saves.

**Naming gotchas in the existing codebase** (verified against the current `main` branch — do not "correct" any of these on encountering them):

| Looks like | Actually is |
|---|---|
| `chassis.NewServer` | `chassis.New(cfg) (*Server, error)` — see `internal/chassis/server.go:147` |
| `*Server` method `Handler()` | `*Server.Mount(mux *http.ServeMux)` — see `internal/chassis/server.go:241`; tests build a `*http.ServeMux`, call `Mount`, then `mux.ServeHTTP(...)` |
| `*Server` method `requireSameOrigin` | Package-level function `requireSameOrigin(next http.Handler) http.Handler` — see `internal/chassis/sameorigin.go:12` |
| `scopeLabel(s) string` | `scopeLabel(s adapters.ApplyScope) (string, bool)` — see `internal/chassis/settings.go:478` |
| Method `s.buildSettingsData()` | Package function `buildSettingsData(bridge, registry, catalog, catalogManager) SettingsData` — see `internal/chassis/data.go:297` (4C extended the signature with `catalogManager`) |
| Template helper `scopeLabel` | `settingsScopeLabel` (uppercases a wire-label string) — see `internal/chassis/templates.go:78,203` |
| JS helpers `showSettingsChip` / `markFieldRowError` | `showNotice(text, variant)`, `paintFieldError(name, msg)`, `clearFieldError(name)` — see `internal/chassis/static/settings-drawer.js:80,89,114` |
| Test `TestForbiddenImports` | `TestProductionImports_NoCrossPackageCoupling` — see `internal/chassis/import_check_test.go:87`; an additional fixture test is `TestChassisForbiddenImports_IncludesMisterctl` at line 140 |
| `chassisCSSBytes()` accessor | `chassisStaticFS.ReadFile("static/chassis.css")` |
| `settingsDrawerJSBytes()` accessor | `chassisStaticFS.ReadFile("static/settings-drawer.js")` |

If a task uses one of the left-column names, treat it as a transcription bug in the plan and substitute the right-column equivalent.

---

## File Structure

**Modified files (extend existing 4A/4B/4C surface):**

- `internal/uiserver/adapter_saver.go` — add `SaveTouched(name, touched, adapter, fields) (ApplyScope, error)` method + supporting unexported helpers (`extractAdapterSectionBody`, `readAdapterSectionMap`, `overlayTouched`, `encodeAdapterMap`, `decodeAdapterSection`, `currentValuesOf`). ~180 new lines.
- `internal/uiserver/adapter_saver_test.go` (new file alongside the saver) — unit tests for SaveTouched and helpers.
- `internal/chassis/settings.go` — add `AdapterSettingsSaver` interface (3 methods) + `StreamsRefresher` interface (1 method) + `StreamsRefreshResult` struct. Add `handleSettingsAdapterPost`, `handleSettingsActionStreamsRefresh`. Add a private `streamsRefreshGate` mutex for single-flight enforcement. ~250 new lines.
- `internal/chassis/settings_test.go` — handler unit tests covering every success/error branch listed in the spec's §Wire Contract.
- `internal/chassis/server.go` — add `AdapterSettingsSaver` and `StreamsRefresher` fields on `Config`. Mount `POST /receiver/settings/adapter/{name}` and `POST /receiver/settings/action/streams-refresh` behind `requireSameOrigin`.
- `internal/chassis/data.go` — extend `SettingsData` with `Adapters []AdapterPaneData`. Extend `buildSettingsData` to populate adapter Fields/Values/Providers + section header hints. Reuse 4C's `CatalogChannelCount` and `CatalogManager.Providers()` for streams.
- `internal/chassis/chassis_test.go` — template render tests for each adapter section.
- `internal/chassis/templates/settings-drawer.html` — replace `{{ template "settings-stub" (stub "adapters" "Adapter forms" "4D – 4F") }}` with `{{ template "settings-adapters" . }}`.
- `internal/chassis/static/settings-drawer.js` — add `[data-adapter]` blur/click handlers for adapter switches and inputs; add `[data-settings-action="streams-refresh"]` handler with TryLock-style single-flight.
- `internal/chassis/static/chassis.css` — port `.settings-section .hint` rule from mockup, add new `.settings-subhead` rule.
- `internal/chassis/import_check_test.go` — **no edits** (entries unchanged).
- `cmd/mister-groovy-relay/main.go` — construct the two new production wrappers and pass them into `chassis.Config`.

**Created files:**

- `internal/uiserver/adapter_saver_test.go` — unit tests for the new `SaveTouched` method.
- `internal/chassis/templates/settings-adapters.html` — container template; emits six `<section>` blocks in mockup order (Plex stub, DLNA, URL stub, Torrent, Jellyfin stub, Streams).
- `internal/chassis/templates/settings-adapter-dlna.html` — `{{ define "settings-adapter-dlna" }}` (4 fields).
- `internal/chassis/templates/settings-adapter-torrent.html` — `{{ define "settings-adapter-torrent" }}` (10 fields, wide modifier).
- `internal/chassis/templates/settings-adapter-streams.html` — `{{ define "settings-adapter-streams" }}` (15 top-level fields + per-provider sub-section + refresh action row).
- `cmd/mister-groovy-relay/adapter_settings_saver.go` — production binding for `chassis.AdapterSettingsSaver` over `*uiserver.AdapterSaver` + the adapter registry.
- `cmd/mister-groovy-relay/streams_refresher.go` — production binding for `chassis.StreamsRefresher` over `*streams.Adapter`.
- `cmd/mister-groovy-relay/adapter_settings_e2e_test.go` — integration-tag DLNA end-to-end save through the chassis route + wrapper.
- `cmd/mister-groovy-relay/adapter_settings_saver_test.go` — wrapper-level Torrent / Streams save + projection coverage.
- `cmd/mister-groovy-relay/streams_refresher_test.go` — wrapper-level streams refresh + chassis route coverage.

**Files intentionally unchanged:** `internal/adapters/{dlna,torrent,streams}/*` (the spec's Goal 4 constraint), `internal/uiserver/bridge_saver.go`, `internal/misterctl/*`, `internal/core/*`, `internal/ui/*`.

---

## Task 1: Add `currentValuesOf` helper to uiserver

**Files:**
- Modify: `internal/uiserver/adapter_saver.go`
- Create: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/uiserver/adapter_saver_test.go`:

```go
package uiserver

import (
	"context"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakeAdapterWithCurrent struct {
	values map[string]any
}

func (f *fakeAdapterWithCurrent) Name() string                 { return "fake" }
func (f *fakeAdapterWithCurrent) DisplayName() string          { return "Fake" }
func (f *fakeAdapterWithCurrent) Status() adapters.Status      { return adapters.Status{} }
func (f *fakeAdapterWithCurrent) Fields() []adapters.FieldDef  { return nil }
func (f *fakeAdapterWithCurrent) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeAdapterWithCurrent) IsEnabled() bool              { return true }
func (f *fakeAdapterWithCurrent) Start(context.Context) error  { return nil }
func (f *fakeAdapterWithCurrent) Stop() error                  { return nil }
func (f *fakeAdapterWithCurrent) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeAdapterWithCurrent) CurrentValues() map[string]any {
	return f.values
}

type fakeAdapterNoCurrent struct{}

func (f *fakeAdapterNoCurrent) Name() string                 { return "no-current" }
func (f *fakeAdapterNoCurrent) DisplayName() string          { return "No Current" }
func (f *fakeAdapterNoCurrent) Status() adapters.Status      { return adapters.Status{} }
func (f *fakeAdapterNoCurrent) Fields() []adapters.FieldDef  { return nil }
func (f *fakeAdapterNoCurrent) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeAdapterNoCurrent) IsEnabled() bool              { return false }
func (f *fakeAdapterNoCurrent) Start(context.Context) error  { return nil }
func (f *fakeAdapterNoCurrent) Stop() error                  { return nil }
func (f *fakeAdapterNoCurrent) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func TestCurrentValuesOf_DuckTypeMatch(t *testing.T) {
	t.Parallel()
	a := &fakeAdapterWithCurrent{values: map[string]any{"enabled": true, "name": "X"}}
	got, ok := currentValuesOf(a)
	if !ok {
		t.Fatalf("currentValuesOf returned ok=false for adapter with CurrentValues")
	}
	if got["enabled"] != true || got["name"] != "X" {
		t.Errorf("got = %#v, want map with enabled=true, name=X", got)
	}
}

func TestCurrentValuesOf_NoMethod(t *testing.T) {
	t.Parallel()
	a := &fakeAdapterNoCurrent{}
	_, ok := currentValuesOf(a)
	if ok {
		t.Errorf("currentValuesOf returned ok=true for adapter without CurrentValues; want false")
	}
}
```

Note: `adapters.Adapter` is the interface defined in `internal/adapters/adapter.go`. The fakes above intentionally satisfy the full current method set (`Name`, `DisplayName`, `Fields`, `DecodeConfig`, `IsEnabled`, `Start`, `Stop`, `Status`, `ApplyConfig`) so the test compiles before `currentValuesOf` exists.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/uiserver -run TestCurrentValuesOf -v`
Expected: FAIL with `undefined: currentValuesOf`.

- [ ] **Step 3: Add the helper to `internal/uiserver/adapter_saver.go`**

Append to `internal/uiserver/adapter_saver.go`:

```go
// currentValuesOf returns the adapter's current in-memory values as a
// generic map[string]any, or false if the adapter does not implement
// the optional CurrentValues() method. This is the same duck-typed
// interface internal/ui consumes for form prefill — keeping it
// optional preserves backwards compatibility with adapters that only
// implement the core adapters.Adapter contract.
func currentValuesOf(a adapters.Adapter) (map[string]any, bool) {
	type currentValuer interface {
		CurrentValues() map[string]any
	}
	cv, ok := a.(currentValuer)
	if !ok {
		return nil, false
	}
	return cv.CurrentValues(), true
}
```

Make sure the top-of-file import block already pulls `internal/adapters`. If not, add:

```go
import (
	// ... existing imports ...
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/uiserver -run TestCurrentValuesOf -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver.go internal/uiserver/adapter_saver_test.go
git commit -m "feat(uiserver): add currentValuesOf duck-type helper for SaveTouched"
```

---

## Task 2: Add `overlayTouched` helper

**Files:**
- Modify: `internal/uiserver/adapter_saver.go`
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
func TestOverlayTouched_BoolField(t *testing.T) {
	t.Parallel()
	current := map[string]any{"enabled": false, "name": "M"}
	touched := map[string]string{"enabled": "true"}
	fields := []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "name", Kind: adapters.KindText},
	}
	got, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) != 0 {
		t.Fatalf("ferrs = %v, want none", ferrs)
	}
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if got["name"] != "M" {
		t.Errorf("name = %v, want unchanged 'M'", got["name"])
	}
}

func TestOverlayTouched_IntField(t *testing.T) {
	t.Parallel()
	current := map[string]any{"port": int64(32100)}
	touched := map[string]string{"port": "32200"}
	fields := []adapters.FieldDef{{Key: "port", Kind: adapters.KindInt}}
	got, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) != 0 {
		t.Fatalf("ferrs = %v, want none", ferrs)
	}
	if got["port"] != int64(32200) {
		t.Errorf("port = %v (%T), want int64(32200)", got["port"], got["port"])
	}
}

func TestOverlayTouched_BadInt(t *testing.T) {
	t.Parallel()
	current := map[string]any{"port": int64(32100)}
	touched := map[string]string{"port": "not-a-number"}
	fields := []adapters.FieldDef{{Key: "port", Kind: adapters.KindInt}}
	_, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) == 0 {
		t.Fatalf("ferrs empty, want one entry for 'port'")
	}
	if ferrs[0].Key != "port" {
		t.Errorf("ferrs[0].Key = %q, want 'port'", ferrs[0].Key)
	}
}

func TestOverlayTouched_UnknownKey(t *testing.T) {
	t.Parallel()
	current := map[string]any{"enabled": true}
	touched := map[string]string{"unknown": "x"}
	fields := []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool}}
	_, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) == 0 {
		t.Fatalf("ferrs empty, want one entry for unknown key")
	}
	if ferrs[0].Key != "unknown" {
		t.Errorf("ferrs[0].Key = %q, want 'unknown'", ferrs[0].Key)
	}
}

func TestOverlayTouched_DottedProviderKey(t *testing.T) {
	t.Parallel()
	// providers.<id>.catalog_refresh_hours is the dotted key shape used by
	// the streams adapter's per-provider rows. overlayTouched must accept
	// it without requiring an exact FieldDef entry — the streams adapter's
	// Fields() emits per-provider rows dynamically; the static fields list
	// passed to overlayTouched is the top-level schema. Dotted keys
	// matching providers.<id>.<key> are routed by suffix.
	current := map[string]any{
		"enabled":   true,
		"providers": map[string]any{},
	}
	touched := map[string]string{"providers.foo.catalog_refresh_hours": "12"}
	fields := []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		// Dotted-key wildcard: providers.*.catalog_refresh_hours is int.
		{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
	}
	got, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) != 0 {
		t.Fatalf("ferrs = %v, want none", ferrs)
	}
	providers, ok := got["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers not a map: %#v", got["providers"])
	}
	foo, ok := providers["foo"].(map[string]any)
	if !ok {
		t.Fatalf("providers.foo not a map: %#v", providers["foo"])
	}
	if foo["catalog_refresh_hours"] != int64(12) {
		t.Errorf("providers.foo.catalog_refresh_hours = %v, want 12", foo["catalog_refresh_hours"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/uiserver -run TestOverlayTouched -v`
Expected: FAIL with `undefined: overlayTouched`.

- [ ] **Step 3: Add the helper**

Append to `internal/uiserver/adapter_saver.go`:

```go
// overlayTouched merges typed values from `touched` (string-encoded
// form values) onto `current` (the adapter's in-memory snapshot),
// using the FieldDef table for type dispatch. Returns the merged map
// plus per-field errors for any key that fails to decode or is not
// in the schema. The result is a fresh map; `current` is not mutated.
//
// Dotted-key keys (e.g. providers.foo.catalog_refresh_hours) are
// matched against schema entries whose Key uses the * wildcard
// (providers.*.catalog_refresh_hours). Wildcard-matching values are
// nested into a map-of-maps shape compatible with the BurntSushi
// TOML encoder.
func overlayTouched(current map[string]any, touched map[string]string, fields []adapters.FieldDef) (map[string]any, []adapters.FieldError) {
	out := cloneMap(current)
	var errs []adapters.FieldError
	for key, raw := range touched {
		fd, dotted, ok := matchFieldDef(fields, key)
		if !ok {
			errs = append(errs, adapters.FieldError{Key: key, Msg: "unknown field"})
			continue
		}
		val, perr := decodeTouchedValue(raw, fd.Kind)
		if perr != "" {
			errs = append(errs, adapters.FieldError{Key: key, Msg: perr})
			continue
		}
		if dotted {
			if err := setDottedValue(out, key, val); err != nil {
				errs = append(errs, adapters.FieldError{Key: key, Msg: err.Error()})
			}
			continue
		}
		out[key] = val
	}
	return out, errs
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if child, ok := v.(map[string]any); ok {
			out[k] = cloneMap(child)
			continue
		}
		out[k] = v
	}
	return out
}

// matchFieldDef returns the FieldDef matching key. Exact match wins;
// otherwise a wildcard FieldDef whose Key uses * in dotted segments
// (e.g. providers.*.catalog_refresh_hours) is matched against the
// dotted form of key. The dotted bool tells the caller whether to
// invoke setDottedValue or assign directly.
func matchFieldDef(fields []adapters.FieldDef, key string) (adapters.FieldDef, bool, bool) {
	for _, fd := range fields {
		if fd.Key == key {
			return fd, false, true
		}
	}
	for _, fd := range fields {
		if !strings.Contains(fd.Key, "*") {
			continue
		}
		if dottedKeyMatchesWildcard(key, fd.Key) {
			return fd, true, true
		}
	}
	return adapters.FieldDef{}, false, false
}

func dottedKeyMatchesWildcard(key, pattern string) bool {
	keyParts := strings.Split(key, ".")
	patParts := strings.Split(pattern, ".")
	if len(keyParts) != len(patParts) {
		return false
	}
	for i, p := range patParts {
		if p == "*" {
			continue
		}
		if p != keyParts[i] {
			return false
		}
	}
	return true
}

// decodeTouchedValue parses a string-encoded form value into the
// typed Go value the TOML encoder expects. Numeric kinds become
// int64; bool kind parses "true"/"false"; text kinds pass through.
func decodeTouchedValue(raw string, kind adapters.FieldKind) (any, string) {
	switch kind {
	case adapters.KindBool:
		switch raw {
		case "true":
			return true, ""
		case "false":
			return false, ""
		default:
			return nil, fmt.Sprintf("not a bool: %q", raw)
		}
	case adapters.KindInt:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Sprintf("not an integer: %q", raw)
		}
		return n, ""
	case adapters.KindText, adapters.KindSecret, adapters.KindEnum:
		return raw, ""
	default:
		return nil, fmt.Sprintf("unsupported kind: %v", kind)
	}
}

// setDottedValue assigns val at the dotted path in m, creating
// intermediate map[string]any nodes as needed.
func setDottedValue(m map[string]any, key string, val any) error {
	parts := strings.Split(key, ".")
	cur := m
	for i, p := range parts[:len(parts)-1] {
		child, ok := cur[p]
		if !ok {
			next := map[string]any{}
			cur[p] = next
			cur = next
			continue
		}
		nextMap, ok := child.(map[string]any)
		if !ok {
			return fmt.Errorf("path %q: segment %q is not a table", key, strings.Join(parts[:i+1], "."))
		}
		cur = nextMap
	}
	cur[parts[len(parts)-1]] = val
	return nil
}
```

Make sure the imports at the top of `adapter_saver.go` include `strconv` (for `ParseInt`); add it if missing.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/uiserver -run TestOverlayTouched -v`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver.go internal/uiserver/adapter_saver_test.go
git commit -m "feat(uiserver): add overlayTouched helper with wildcard dotted-key support"
```

---

## Task 3: Add `encodeAdapterMap` helper

**Files:**
- Modify: `internal/uiserver/adapter_saver.go`
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
func TestEncodeAdapterMap_TopLevelFields(t *testing.T) {
	t.Parallel()
	merged := map[string]any{
		"enabled":     true,
		"device_name": "MiSTer",
		"port":        int64(32100),
	}
	got, err := encodeAdapterMap("dlna", merged)
	if err != nil {
		t.Fatalf("encodeAdapterMap err = %v", err)
	}
	for _, want := range []string{
		`enabled = true`,
		`device_name = "MiSTer"`,
		`port = 32100`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("encoded does not contain %q\nencoded:\n%s", want, got)
		}
	}
}

func TestEncodeAdapterMap_NestedProviders(t *testing.T) {
	t.Parallel()
	merged := map[string]any{
		"enabled": true,
		"providers": map[string]any{
			"foo": map[string]any{"catalog_refresh_hours": int64(12)},
		},
	}
	got, err := encodeAdapterMap("streams", merged)
	if err != nil {
		t.Fatalf("encodeAdapterMap err = %v", err)
	}
		// encodeAdapterMap prefixes nested tables so replaceAdapterSection can
		// insert a body whose descendant tables remain under [adapters.streams].
		for _, want := range []string{
			`enabled = true`,
			`[adapters.streams.providers.foo]`,
			`catalog_refresh_hours = 12`,
		} {
		if !strings.Contains(string(got), want) {
			t.Errorf("encoded does not contain %q\nencoded:\n%s", want, got)
		}
	}
}
```

Make sure the test file imports `strings`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/uiserver -run TestEncodeAdapterMap -v`
Expected: FAIL with `undefined: encodeAdapterMap`.

- [ ] **Step 3: Add the helper**

Append to `internal/uiserver/adapter_saver.go`:

```go
// encodeAdapterMap serializes a generic map[string]any into a TOML body
// for [adapters.<name>]. Top-level keys remain bare `key = value` lines;
// nested tables are rewritten from BurntSushi's relative [providers.foo]
// form to absolute [adapters.<name>.providers.foo] headers. That keeps
// replaceAdapterSection's existing contract (it inserts the parent header)
// while preserving descendant adapter subtables.
func encodeAdapterMap(name string, m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encode adapter map: %w", err)
	}
	return prefixAdapterSubtableHeaders(name, buf.Bytes()), nil
}

func prefixAdapterSubtableHeaders(name string, body []byte) []byte {
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inner := strings.Trim(trimmed, "[]")
			line = fmt.Sprintf("[adapters.%s.%s]", name, inner)
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}
```

Imports at the top of the file need `github.com/BurntSushi/toml`. Confirm it is in `go.mod` (it is — used by `internal/ui`'s adapter code). Add the import line to `adapter_saver.go`:

```go
import (
	// ... existing imports ...
	"github.com/BurntSushi/toml"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/uiserver -run TestEncodeAdapterMap -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver.go internal/uiserver/adapter_saver_test.go
git commit -m "feat(uiserver): add encodeAdapterMap helper for SaveTouched re-encoding"
```

---

## Task 4: Add `decodeAdapterSection` helper

**Files:**
- Modify: `internal/uiserver/adapter_saver.go`
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
import (
	// ... ensure github.com/BurntSushi/toml is imported
)

func TestDecodeAdapterSection_RoundTrip(t *testing.T) {
	t.Parallel()
	snippet := []byte("enabled = true\ndevice_name = \"MiSTer\"\n")
	prim, meta, err := decodeAdapterSection(snippet, "dlna")
	if err != nil {
		t.Fatalf("decodeAdapterSection err = %v", err)
	}
	var got struct {
		Enabled    bool   `toml:"enabled"`
		DeviceName string `toml:"device_name"`
	}
	if err := meta.PrimitiveDecode(prim, &got); err != nil {
		t.Fatalf("PrimitiveDecode err = %v", err)
	}
	if got.Enabled != true || got.DeviceName != "MiSTer" {
		t.Errorf("decoded = %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/uiserver -run TestDecodeAdapterSection -v`
Expected: FAIL with `undefined: decodeAdapterSection`.

- [ ] **Step 3: Add the helper**

Append to `internal/uiserver/adapter_saver.go`:

```go
// decodeAdapterSection wraps a bare body snippet (key = value lines,
// no [adapters.<name>] header) in the appropriate header and decodes
// it into a toml.Primitive + MetaData handle the adapter's
// Validate() / ApplyConfig() methods can consume. Mirrors the same
// pattern internal/ui/adapter.go uses; lives in uiserver so the new
// SaveTouched method can call it without importing internal/ui.
func decodeAdapterSection(body []byte, name string) (toml.Primitive, toml.MetaData, error) {
	wrapper := fmt.Sprintf("[adapters.%s]\n%s", name, body)
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(wrapper, &envelope)
	if err != nil {
		return toml.Primitive{}, toml.MetaData{}, fmt.Errorf("decode adapter section %q: %w", name, err)
	}
	return envelope.Adapters[name], meta, nil
}

// readAdapterSectionMap reads the latest on-disk [adapters.<name>] table
// plus all [adapters.<name>.*] descendant tables into a generic map. This
// is the source of truth for SaveTouched overlays; CurrentValues() is only
// a fallback for a missing section, never the primary preservation path.
func readAdapterSectionMap(doc []byte, name string) (map[string]any, bool, error) {
	body, ok := extractAdapterSectionBody(doc, name)
	if !ok {
		return nil, false, nil
	}
	prim, meta, err := decodeAdapterSection(body, name)
	if err != nil {
		return nil, true, err
	}
	current := map[string]any{}
	if err := meta.PrimitiveDecode(prim, &current); err != nil {
		return nil, true, fmt.Errorf("decode current adapter section %q: %w", name, err)
	}
	return current, true, nil
}

func extractAdapterSectionBody(doc []byte, name string) ([]byte, bool) {
	parent := fmt.Sprintf("[adapters.%s]", name)
	descendantPrefix := fmt.Sprintf("[adapters.%s.", name)
	lines := strings.Split(string(doc), "\n")
	out := make([]string, 0)
	found := false
	for i := 0; i < len(lines); {
		tr := strings.TrimSpace(lines[i])
		if tr == parent || (strings.HasPrefix(tr, descendantPrefix) && strings.HasSuffix(tr, "]")) {
			found = true
			if tr != parent {
				out = append(out, lines[i])
			}
			i++
			for i < len(lines) {
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, "[") && strings.HasSuffix(next, "]") {
					break
				}
				out = append(out, lines[i])
				i++
			}
			continue
		}
		i++
	}
	return []byte(strings.Join(out, "\n")), found
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/uiserver -run TestDecodeAdapterSection -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver.go internal/uiserver/adapter_saver_test.go
git commit -m "feat(uiserver): add decodeAdapterSection helper for SaveTouched validation"
```

---

## Task 5: Add `SaveTouched` happy path

**Files:**
- Modify: `internal/uiserver/adapter_saver.go`
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/uiserver/adapter_saver_test.go`. This requires a more complete fake adapter that implements Validate + ApplyConfig:

```go
import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// fakeFullAdapter implements adapters.Adapter plus CurrentValues; it
// records the ApplyConfig call for assertions and reports a fixed
// scope from ApplyConfig. The `fields` slice is per-test so Task 7's
// nested-subtable preservation test can inject a wildcard schema
// (providers.*.catalog_refresh_hours) without retrofitting Tasks 5/6.
type fakeFullAdapter struct {
	mu       sync.Mutex
	values   map[string]any
	fields   []adapters.FieldDef
	scope    adapters.ApplyScope
	validErr error
	applyErr error
	applied  []map[string]any
}

func (f *fakeFullAdapter) Name() string                      { return "fake" }
func (f *fakeFullAdapter) DisplayName() string               { return "Fake" }
func (f *fakeFullAdapter) Status() adapters.Status           { return adapters.Status{} }
func (f *fakeFullAdapter) IsEnabled() bool                   { return false }
func (f *fakeFullAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeFullAdapter) Start(context.Context) error       { return nil }
func (f *fakeFullAdapter) Stop() error                       { return nil }
func (f *fakeFullAdapter) Fields() []adapters.FieldDef {
	if f.fields != nil {
		return f.fields
	}
	// Default schema covers Tasks 5/6's dlna-shaped tests.
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "device_name", Kind: adapters.KindText},
	}
}
func (f *fakeFullAdapter) CurrentValues() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(f.values))
	for k, v := range f.values {
		out[k] = v
	}
	return out
}
func (f *fakeFullAdapter) Validate(prim toml.Primitive, meta toml.MetaData) error {
	return f.validErr
}
func (f *fakeFullAdapter) ApplyConfig(prim toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	if f.applyErr != nil {
		return 0, f.applyErr
	}
	var decoded map[string]any
	_ = meta.PrimitiveDecode(prim, &decoded)
	f.mu.Lock()
	f.applied = append(f.applied, decoded)
	f.mu.Unlock()
	return f.scope, nil
}

func newTempConfigWithSection(t *testing.T, name string, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf("[bridge]\nmister.host = \"x\"\n\n[adapters.%s]\n%s", name, body)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestSaveTouched_HappyPath(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "dlna", `enabled = false
device_name = "Old"
`)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	adapter := &fakeFullAdapter{
		values: map[string]any{"enabled": false, "device_name": "Old"},
		scope:  adapters.ScopeHotSwap,
	}
	scope, err := saver.SaveTouched("dlna", map[string]string{"enabled": "true"}, adapter, adapter.Fields())
	if err != nil {
		t.Fatalf("SaveTouched err = %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
	// Disk-side: read back, assert enabled=true survived; device_name preserved.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), `enabled = true`) {
		t.Errorf("disk does not contain enabled = true:\n%s", got)
	}
	if !strings.Contains(string(got), `device_name = "Old"`) {
		t.Errorf("disk does not preserve device_name:\n%s", got)
	}
	// ApplyConfig was invoked exactly once with the merged map.
	if n := len(adapter.applied); n != 1 {
		t.Fatalf("ApplyConfig calls = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/uiserver -run TestSaveTouched_HappyPath -v`
Expected: FAIL with `*AdapterSaver has no field or method SaveTouched`.

- [ ] **Step 3: Add the `SaveTouched` method**

Append to `internal/uiserver/adapter_saver.go`:

```go
// SaveTouched applies a touched-key envelope to the latest on-disk
// [adapters.<name>] TOML section: reads the parent table plus all
// descendant subtables under the shared saver mutex, overlays only the
// submitted keys, re-encodes the full section, validates it, writes
// atomically to disk, and dispatches runtime side effects via
// adapter.ApplyConfig. The shared mutex (the same one BridgeSaver uses)
// serializes against bridge saves and other adapter auto-saves.
//
// The fields argument is the writable-surface allowlist — the chassis
// wrapper passes the projection of adapter.Fields() that excludes
// keys owned by other panes (e.g., for streams, providers.*.disabled
// and providers.*.hls_buffer_disabled are owned by 4C's Catalog pane
// and rejected here). Passing adapter.Fields() unchanged is also
// valid for adapters without a split surface (DLNA, Torrent).
//
// Returns the wire scope (max-wins across changed fields, as
// determined by adapter.ApplyConfig) and a typed error on failure.
// Per-field errors (decode failures, schema-unknown keys, validation
// failures) are wrapped in *adapterFieldErrors so the chassis-side
// AdapterSettingsSaver wrapper can extract them and render
// {ok:false, errors:{...}}.
func (r *AdapterSaver) SaveTouched(name string, touched map[string]string, adapter adapters.Adapter, fields []adapters.FieldDef) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	doc, err := os.ReadFile(r.path)
	if err != nil {
		return 0, fmt.Errorf("read config: %w", err)
	}
	current, found, err := readAdapterSectionMap(doc, name)
	if err != nil {
		return 0, err
	}
	if !found {
		var ok bool
		current, ok = currentValuesOf(adapter)
		if !ok {
			current = map[string]any{}
		}
	}

	merged, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) > 0 {
		return 0, &adapterFieldErrors{Errs: ferrs}
	}

	snippet, err := encodeAdapterMap(name, merged)
	if err != nil {
		return 0, fmt.Errorf("encode merged config: %w", err)
	}

	prim, meta, err := decodeAdapterSection(snippet, name)
	if err != nil {
		return 0, fmt.Errorf("decode re-encoded section: %w", err)
	}

	if validator, ok := adapter.(adapters.Validator); ok {
		if err := validator.Validate(prim, meta); err != nil {
			if ferr, ok := err.(adapters.FieldErrors); ok {
				return 0, &adapterFieldErrors{Errs: []adapters.FieldError(ferr)}
			}
			return 0, fmt.Errorf("validate: %w", err)
		}
	}

	updated := replaceAdapterSection(doc, name, snippet)
	if err := config.WriteAtomic(r.path, updated); err != nil {
		return 0, fmt.Errorf("write config: %w", err)
	}

	scope, err := adapter.ApplyConfig(prim, meta)
	if err != nil {
		return scope, fmt.Errorf("apply config: %w", err)
	}
	return scope, nil
}

// adapterFieldErrors is the typed error SaveTouched returns when per-
// field decoding or validation fails. The chassis-side wrapper unwraps
// this and renders the JSON envelope {ok:false, errors:{...}}.
type adapterFieldErrors struct {
	Errs []adapters.FieldError
}

func (e *adapterFieldErrors) Error() string {
	if len(e.Errs) == 0 {
		return "adapter field errors"
	}
	return fmt.Sprintf("%d adapter field error(s)", len(e.Errs))
}

func (e *adapterFieldErrors) FieldErrors() []adapters.FieldError {
	return e.Errs
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/uiserver -run TestSaveTouched_HappyPath -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver.go internal/uiserver/adapter_saver_test.go
git commit -m "feat(uiserver): add AdapterSaver.SaveTouched method for per-field auto-save"
```

---

## Task 6: SaveTouched per-field error propagation

**Files:**
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
func TestSaveTouched_BadDecode(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "dlna", `enabled = false
`)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	adapter := &fakeFullAdapter{values: map[string]any{"enabled": false}}
	_, err := saver.SaveTouched("dlna", map[string]string{"enabled": "yes-please"}, adapter, adapter.Fields())
	var ferrs *adapterFieldErrors
	if !errors.As(err, &ferrs) {
		t.Fatalf("err = %v (%T), want *adapterFieldErrors", err, err)
	}
	if len(ferrs.Errs) != 1 || ferrs.Errs[0].Key != "enabled" {
		t.Errorf("ferrs = %+v, want one entry for 'enabled'", ferrs.Errs)
	}
}

func TestSaveTouched_ValidateFieldErrors(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "dlna", `enabled = true
device_name = "Old"
`)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	adapter := &fakeFullAdapter{
		values:   map[string]any{"enabled": true, "device_name": "Old"},
		validErr: adapters.FieldErrors{{Key: "device_name", Msg: "must not be empty"}},
	}
	_, err := saver.SaveTouched("dlna", map[string]string{"device_name": ""}, adapter, adapter.Fields())
	var ferrs *adapterFieldErrors
	if !errors.As(err, &ferrs) {
		t.Fatalf("err = %v (%T), want *adapterFieldErrors", err, err)
	}
	if len(ferrs.Errs) != 1 || ferrs.Errs[0].Key != "device_name" {
		t.Errorf("ferrs = %+v, want device_name error", ferrs.Errs)
	}
	// Disk side: nothing was written because Validate failed.
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `device_name = "Old"`) {
		t.Errorf("disk was mutated despite Validate failure:\n%s", got)
	}
}

func TestSaveTouched_ApplyConfigError(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "dlna", `enabled = false
`)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	adapter := &fakeFullAdapter{
		values:   map[string]any{"enabled": false},
		applyErr: errors.New("upstream failure"),
	}
	_, err := saver.SaveTouched("dlna", map[string]string{"enabled": "true"}, adapter, adapter.Fields())
	if err == nil {
		t.Fatalf("err = nil, want apply config error")
	}
	if !strings.Contains(err.Error(), "apply config") {
		t.Errorf("err = %v, want wrapped 'apply config' message", err)
	}
}
```

Imports need `errors` if not already present.

- [ ] **Step 2: Run tests to verify they fail or pass appropriately**

Run: `go test ./internal/uiserver -run TestSaveTouched_ -v`
Expected: bad-decode and validate-field-errors PASS already (Task 5 wired the typed-error path); apply-config-error also PASS. If any FAIL, fix the SaveTouched implementation to match the contract before proceeding.

- [ ] **Step 3: (no impl change)**

These tests pin behavior already implemented in Task 5.

- [ ] **Step 4: Re-run for confirmation**

Run: `go test ./internal/uiserver -run TestSaveTouched -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver_test.go
git commit -m "test(uiserver): pin SaveTouched per-field + apply-config error paths"
```

---

## Task 7: SaveTouched preserves nested provider channels

**Files:**
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
func TestSaveTouched_PreservesNestedSubtables(t *testing.T) {
	t.Parallel()
	body := `enabled = true
manifest_url = "https://example/manifest.json"

[adapters.streams.providers.foo]
catalog_refresh_hours = 6

[adapters.streams.providers.foo.channels.alpha]
hls_buffer_disabled = true
`
	path := newTempConfigWithSection(t, "streams", body)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	adapter := &fakeFullAdapter{
		values: map[string]any{
			"enabled":      true,
			"manifest_url": "https://example/manifest.json",
				// Intentionally omit provider channel subtables from CurrentValues.
				// SaveTouched must preserve them by reading the current disk section.
				"providers": map[string]any{
					"foo": map[string]any{"catalog_refresh_hours": int64(6)},
				},
		},
		scope: adapters.ScopeHotSwap,
	}
	// Touch a per-provider catalog_refresh_hours field; expect the nested
	// channels.alpha subtable to survive the round-trip.
	touched := map[string]string{"providers.foo.catalog_refresh_hours": "12"}
	if _, err := saver.SaveTouched("streams", touched, adapter, adapter.Fields()); err != nil {
		t.Fatalf("SaveTouched err = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), `catalog_refresh_hours = 12`) {
		t.Errorf("touched field not written:\n%s", got)
	}
		if !strings.Contains(string(got), `[adapters.streams.providers.foo.channels.alpha]`) {
			t.Errorf("nested channels subtable lost:\n%s", got)
		}
	if !strings.Contains(string(got), `hls_buffer_disabled = true`) {
		t.Errorf("nested channels.alpha.hls_buffer_disabled lost:\n%s", got)
	}
}
```

	Note the adapter must be given a fields list that accepts the per-provider override. The fake intentionally does **not** expose channel subtables through `CurrentValues()`; the preservation guarantee comes from reading the current on-disk `[adapters.streams]` section plus descendants under the saver mutex.

Adjust `fakeFullAdapter.Fields()` (only for this test — extract it to a `Fields` field on the struct or add a second fake) so the schema list includes:
```go
{Key: "enabled", Kind: adapters.KindBool},
{Key: "manifest_url", Kind: adapters.KindText},
{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
```

Set the fake's `fields` slice on the test fixture (Task 5 already declared `fields []adapters.FieldDef` as a struct field with the default-when-nil fallback). For this test, inject the three-row schema above so `overlayTouched` routes the dotted `providers.foo.catalog_refresh_hours` key through the wildcard path.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/uiserver -run TestSaveTouched_PreservesNestedSubtables -v`
	Expected: FAIL until `SaveTouched` uses `readAdapterSectionMap` as specified. A CurrentValues-only implementation will drop `[adapters.streams.providers.foo.channels.alpha]` and must be fixed, not worked around in the fake.

- [ ] **Step 3: Make it pass**

	If this fails, audit `extractAdapterSectionBody`, `readAdapterSectionMap`, and `encodeAdapterMap`. The saver must preserve subtables present on disk even when the adapter's `CurrentValues()` does not surface them.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/uiserver -run TestSaveTouched_PreservesNestedSubtables -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver_test.go
git commit -m "test(uiserver): SaveTouched preserves nested provider channels subtables"
```

---

## Task 8: SaveTouched concurrent-save serialization

**Files:**
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
func TestSaveTouched_ConcurrentSaves(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "dlna", `enabled = false
device_name = "M"
`)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	// Shared adapter fake across goroutines. Each save should observe the
	// prior writer's disk state before overlaying its own.
	var stateMu sync.Mutex
	state := map[string]any{"enabled": false, "device_name": "M"}
	adapter := &fakeFullAdapter{
		values: state,
		scope:  adapters.ScopeHotSwap,
	}
	// Update fake.values from ApplyConfig results too, mirroring real adapter
	// state, but disk read-under-lock is what closes the save race.
	adapter.applyHook = func(decoded map[string]any) {
		stateMu.Lock()
		for k, v := range decoded {
			state[k] = v
		}
		stateMu.Unlock()
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			touched := map[string]string{}
			if n%2 == 0 {
				touched["enabled"] = "true"
			} else {
				touched["device_name"] = fmt.Sprintf("M%d", n)
			}
			if _, err := saver.SaveTouched("dlna", touched, adapter, adapter.Fields()); err != nil {
				t.Errorf("SaveTouched(n=%d) err = %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `enabled = true`) {
		t.Errorf("final disk does not contain enabled = true:\n%s", got)
	}
}
```

This test also requires extending `fakeFullAdapter` with:
```go
applyHook func(decoded map[string]any)
```
and calling `f.applyHook(decoded)` inside `ApplyConfig` when non-nil.

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -race ./internal/uiserver -run TestSaveTouched_ConcurrentSaves -v`
Expected: PASS without `-race` warnings. If the race detector fires, audit `currentValuesOf` and `overlayTouched` — both must operate on copies, not references into the adapter's locked state.

- [ ] **Step 3: (no impl change unless race detector caught a bug)**

If a race is found, fix it in `adapter_saver.go` (likely in `cloneMap` or `overlayTouched`'s wildcard path) — never weaken the test.

- [ ] **Step 4: Confirm**

Run: `go test -race ./internal/uiserver -v`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver_test.go internal/uiserver/adapter_saver.go
git commit -m "test(uiserver): SaveTouched concurrent saves serialize under shared mutex"
```

---

## Task 9: Add chassis `AdapterSettingsSaver` interface

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/settings_test.go`:

```go
// fakeAdapterSettingsSaver implements chassis.AdapterSettingsSaver for tests.
type fakeAdapterSettingsSaver struct {
	current map[string]map[string]any
	fields  map[string][]adapters.FieldDef
	saveErr error
	scope   string
	touched map[string]map[string]string // adapter name -> last touched map
}

func (f *fakeAdapterSettingsSaver) Current(name string) (map[string]any, bool) {
	cur, ok := f.current[name]
	if !ok {
		return nil, false
	}
	out := make(map[string]any, len(cur))
	for k, v := range cur {
		out[k] = v
	}
	return out, true
}

func (f *fakeAdapterSettingsSaver) Fields(name string) ([]adapters.FieldDef, bool) {
	fd, ok := f.fields[name]
	return fd, ok
}

func (f *fakeAdapterSettingsSaver) SaveTouched(name string, touched map[string]string) (string, error) {
	if f.touched == nil {
		f.touched = map[string]map[string]string{}
	}
	f.touched[name] = touched
	if f.saveErr != nil {
		return "", f.saveErr
	}
	return f.scope, nil
}

func TestAdapterSettingsSaver_StructuralConformance(t *testing.T) {
	t.Parallel()
	var s AdapterSettingsSaver = &fakeAdapterSettingsSaver{}
	_ = s
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestAdapterSettingsSaver_StructuralConformance -v`
Expected: FAIL with `undefined: AdapterSettingsSaver`.

- [ ] **Step 3: Add the interface to `internal/chassis/settings.go`**

Insert after the `CoreLauncher` interface declaration:

```go
// AdapterSettingsSaver is the chassis-side mirror of BridgeSettingsSaver
// for adapter-section writes. Production binding wraps
// *uiserver.AdapterSaver + the adapter registry; the chassis does not
// import internal/uiserver or any concrete adapter package.
type AdapterSettingsSaver interface {
	// Current returns the adapter's current in-memory values, keyed by
	// FieldDef.Key. Returns (nil, false) for unknown adapter names.
	Current(name string) (map[string]any, bool)

	// Fields returns the 4D writable FieldDef surface. DLNA/Torrent return
	// their full FieldDef table; Streams returns top-level fields plus a
	// wildcard providers.*.catalog_refresh_hours allowlist entry. Template
	// rendering skips provider wildcard rows and renders provider overrides
	// from AdapterPaneData. Returns (nil, false) for unknown adapter names.
	Fields(name string) ([]adapters.FieldDef, bool)

	// SaveTouched applies the touched-keys subset to the adapter's
	// [adapters.<name>] TOML section, validates, writes atomically,
	// and dispatches the runtime apply. Returns the wire scope label
	// ("hot" / "next" / "recast" / "reboot") and a typed error
	// implementing settingsChipError on failure. Mirror of
	// BridgeSettingsSaver.SaveTouched.
	SaveTouched(name string, touched map[string]string) (string, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestAdapterSettingsSaver_StructuralConformance -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add AdapterSettingsSaver chassis-owned interface"
```

---

## Task 10: Add chassis `StreamsRefresher` + `StreamsRefreshResult`

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/settings_test.go`:

```go
// Ensure settings_test.go imports sync/atomic for fakeStreamsRefresher.calls.

type fakeStreamsRefresher struct {
	result StreamsRefreshResult
	err    error
	calls  atomic.Int32
	gate   chan struct{} // optional; close to release
}

func (f *fakeStreamsRefresher) RefreshNow(ctx context.Context) (StreamsRefreshResult, error) {
	f.calls.Add(1)
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return StreamsRefreshResult{}, ctx.Err()
		}
	}
	if f.err != nil {
		return f.result, f.err
	}
	return f.result, nil
}

func TestStreamsRefresher_StructuralConformance(t *testing.T) {
	t.Parallel()
	var r StreamsRefresher = &fakeStreamsRefresher{}
	_ = r
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestStreamsRefresher_StructuralConformance -v`
Expected: FAIL with `undefined: StreamsRefresher`.

- [ ] **Step 3: Add the interface + struct**

Append to `internal/chassis/settings.go`, after `AdapterSettingsSaver`:

```go
// StreamsRefresher is the chassis-side interface backing the
// /receiver/settings/action/streams-refresh action. Production binding
// wraps *streams.Adapter.RefreshNow(ctx, "") — the canonical manifest-
// refresh entry point. The chassis does not import internal/adapters/streams.
type StreamsRefresher interface {
	// RefreshNow fetches the streams manifest (and ripples to provider
	// catalogs as a side effect). The returned result carries the
	// source label ("remote" / "cache") and a non-nil Err if the
	// refresh failed. The chassis handler wraps the call in a 30s
	// context.
	RefreshNow(ctx context.Context) (StreamsRefreshResult, error)
}

// StreamsRefreshResult is the scalar status returned by RefreshNow.
type StreamsRefreshResult struct {
	Source     string
	DurationMS int64
	Err        error
}
```

Confirm `context` is in the import list.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestStreamsRefresher_StructuralConformance -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add StreamsRefresher interface + StreamsRefreshResult"
```

---

## Task 11: Add the two new fields to `Server.Config`

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/settings.go` (the test helper `newTestServerForLaunchCore` may need updating to keep compiling — extend or duplicate)
- Modify: `internal/chassis/chassis_test.go` (or settings_test.go) — nil-handling baseline tests

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func TestAdapterSettingsSaver_NotReadyWhenNil(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0)}}
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/dlna", nil)
	rec := httptest.NewRecorder()
	s.handleSettingsAdapterPost(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Code = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got, _ := body["chip"].(string); got != "NOT READY" {
		t.Errorf("body.chip = %q, want NOT READY", got)
	}
}

func TestStreamsRefresher_NotReadyWhenNil(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0)}}
	req := httptest.NewRequest("POST", "/receiver/settings/action/streams-refresh", nil)
	rec := httptest.NewRecorder()
	s.handleSettingsActionStreamsRefresh(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Code = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got, _ := body["chip"].(string); got != "NOT READY" {
		t.Errorf("body.chip = %q, want NOT READY", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chassis -run "TestAdapterSettingsSaver_NotReadyWhenNil|TestStreamsRefresher_NotReadyWhenNil" -v`
Expected: FAIL with `undefined: handleSettingsAdapterPost` and `handleSettingsActionStreamsRefresh`.

- [ ] **Step 3: Add the two `Config` fields + skeleton handlers**

In `internal/chassis/server.go`, add to the `Config` struct (alongside `CoreLauncher`, `BridgeSaver`, etc.):

```go
type Config struct {
	// ... existing fields ...
	AdapterSettingsSaver AdapterSettingsSaver
	StreamsRefresher     StreamsRefresher
}
```

In `internal/chassis/settings.go`, add skeleton handlers (real logic in later tasks):

```go
func (s *Server) handleSettingsAdapterPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterSettingsSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	writeSettingsChip(w, http.StatusInternalServerError, "NOT IMPLEMENTED")
}

func (s *Server) handleSettingsActionStreamsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StreamsRefresher == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	writeSettingsChip(w, http.StatusInternalServerError, "NOT IMPLEMENTED")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis -run "TestAdapterSettingsSaver_NotReadyWhenNil|TestStreamsRefresher_NotReadyWhenNil" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): wire AdapterSettingsSaver + StreamsRefresher into Config; skeleton handlers"
```

---

## Task 12: Mount the two new routes

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/settings_test.go` (or `server_test.go` if route-mounting tests live there) — add same-origin tests for both routes

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func TestAdapterSettingsPost_RequiresSameOrigin(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/dlna", strings.NewReader("enabled=true"))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Host", "127.0.0.1:32102")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Code = %d, want 403", rec.Code)
	}
}

func TestStreamsRefresh_RequiresSameOrigin(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("POST", "/receiver/settings/action/streams-refresh", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Host", "127.0.0.1:32102")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Code = %d, want 403", rec.Code)
	}
}
```

`newTestServer(t)` builds a `*Server` via `chassis.New(Config{Version:"test", StartedAt: time.Now(), ...})` with safe defaults — copy the helper shape from `internal/chassis/chassis_test.go` (it already exists for 4A/4B route tests). Tests that need the HTTP mux call `s.Mount(mux)` themselves; there is no `s.Handler()` method on `*Server` — `Mount(mux *http.ServeMux)` is the only routing entry point (see `internal/chassis/server.go:241`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chassis -run "AdapterSettingsPost_RequiresSameOrigin|StreamsRefresh_RequiresSameOrigin" -v`
Expected: FAIL with 404 (route not mounted).

- [ ] **Step 3: Mount the routes**

In `internal/chassis/server.go`, find the section where 4A/4B mount existing routes behind `requireSameOrigin`, and add:

```go
mux.Handle("POST /receiver/settings/adapter/{name}", requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterPost)))
mux.Handle("POST /receiver/settings/action/streams-refresh", requireSameOrigin(http.HandlerFunc(s.handleSettingsActionStreamsRefresh)))
```

`requireSameOrigin` is a package-level function (`internal/chassis/sameorigin.go:12`), not a method. The Go 1.22+ pattern syntax `{name}` is already used by 4A/4B routes.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis -run "AdapterSettingsPost_RequiresSameOrigin|StreamsRefresh_RequiresSameOrigin" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/settings_test.go
git commit -m "feat(chassis): mount /receiver/settings/adapter and /streams-refresh routes"
```

---

## Task 13: `handleSettingsAdapterPost` — unknown adapter + bad input

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func newTestServerForAdapterSave(saver AdapterSettingsSaver) *Server {
	return &Server{
		cfg: Config{
			Version:              "test",
			StartedAt:            time.Unix(0, 0),
			AdapterSettingsSaver: saver,
		},
	}
}

func postAdapterSave(t *testing.T, s *Server, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/"+name, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	s.handleSettingsAdapterPost(rec, req)
	return rec
}

func TestAdapterSave_UnknownAdapter(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{},
		fields:  map[string][]adapters.FieldDef{},
	}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "doesnotexist", "enabled=true")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Code = %d, want 404", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got, _ := body["chip"].(string); got != "UNKNOWN ADAPTER" {
		t.Errorf("body.chip = %q, want UNKNOWN ADAPTER", got)
	}
}

func TestAdapterSave_UnknownField(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
		scope: "hot",
	}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "dlna", "bogus_field=true")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got, _ := body["chip"].(string); got != "BAD INPUT" {
		t.Errorf("body.chip = %q, want BAD INPUT", got)
	}
}

func TestAdapterSave_MalformedBody(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := newTestServerForAdapterSave(saver)
	// Force a Form-parse error by sending an explicitly bad Content-Type
	// with binary content. Easier: skip parse by sending an empty body
	// and assert the handler still routes — adjust to the actual parse
	// failure mode the handler triggers (verify against go's url.Parse).
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/dlna", strings.NewReader("%ZZ"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("name", "dlna")
	rec := httptest.NewRecorder()
	s.handleSettingsAdapterPost(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chassis -run TestAdapterSave_ -v`
Expected: FAIL — handler still returns the `NOT IMPLEMENTED` skeleton.

- [ ] **Step 3: Implement `handleSettingsAdapterPost` save dispatch**

Replace the skeleton handler in `internal/chassis/settings.go` with:

```go
func (s *Server) handleSettingsAdapterPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterSettingsSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	fields, ok := s.cfg.AdapterSettingsSaver.Fields(name)
	if !ok {
		writeSettingsChip(w, http.StatusNotFound, "UNKNOWN ADAPTER")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	touched, ferrs := touchedFromForm(r.PostForm, fields)
	if len(ferrs) > 0 {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if !atLeastOneKey(touched) {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	scope, err := s.cfg.AdapterSettingsSaver.SaveTouched(name, touched)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsSuccess(w, scope)
}

// touchedFromForm extracts and validates the subset of form keys that
// match the adapter's schema (exact match or wildcard for dotted-key
// per-provider rows). Unknown keys are reported to the caller so it can
// emit the spec's BAD INPUT chip.
func touchedFromForm(form url.Values, fields []adapters.FieldDef) (map[string]string, map[string]string) {
	touched := map[string]string{}
	ferrs := map[string]string{}
	for key, values := range form {
		if len(values) == 0 {
			continue
		}
		if !keyMatchesSchema(key, fields) {
			ferrs[key] = "unknown field"
			continue
		}
		touched[key] = values[0]
	}
	if len(ferrs) > 0 {
		return nil, ferrs
	}
	return touched, nil
}

func keyMatchesSchema(key string, fields []adapters.FieldDef) bool {
	for _, fd := range fields {
		if fd.Key == key {
			return true
		}
		if strings.Contains(fd.Key, "*") && dottedKeyMatchesPattern(key, fd.Key) {
			return true
		}
	}
	return false
}

// dottedKeyMatchesPattern matches keys against patterns like
// "providers.*.catalog_refresh_hours". Mirrors the uiserver helper
// but kept local so the chassis does not import uiserver.
func dottedKeyMatchesPattern(key, pattern string) bool {
	keyParts := strings.Split(key, ".")
	patParts := strings.Split(pattern, ".")
	if len(keyParts) != len(patParts) {
		return false
	}
	for i, p := range patParts {
		if p == "*" {
			continue
		}
		if p != keyParts[i] {
			return false
		}
	}
	return true
}

func atLeastOneKey(m map[string]string) bool {
	for range m {
		return true
	}
	return false
}

// fieldErrorBearerForChassis is the named interface emitSaveError
// unwraps adapter field errors against. cmd's *cmdAdapterFieldErrors
// (declared in Task 32) satisfies this structurally — no concrete-type
// coupling between layers. Declared at package scope because Go's
// errors.As requires a named target type; anonymous interface targets
// do not compile.
type fieldErrorBearerForChassis interface {
	error
	FieldErrors() []adapters.FieldError
}

// emitSaveError unwraps typed saver errors into the appropriate JSON
// envelope. settingsChipError carries chip + status; FieldErrors
// becomes the errors map. Falls back to a generic WRITE FAILED chip.
func emitSaveError(w http.ResponseWriter, err error) {
	var chipErr settingsChipError
	if errors.As(err, &chipErr) {
		writeSettingsChip(w, chipErr.StatusCode(), chipErr.Chip())
		return
	}
	var feb fieldErrorBearerForChassis
	if errors.As(err, &feb) {
		ferrs := map[string]string{}
		for _, fe := range feb.FieldErrors() {
			ferrs[fe.Key] = fe.Msg
		}
		writeSettingsFieldErrors(w, http.StatusBadRequest, ferrs)
		return
	}
	writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
}
```

`settingsChipError` is the chassis-side 4A interface. `fieldErrorBearerForChassis` is a new named interface (declared at package scope) that the cmd-side wrapper's error type (Task 32) implements structurally. Named-interface targets are required by Go's `errors.As` — anonymous interface literal targets do not compile.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis -run TestAdapterSave_ -v`
Expected: PASS for unknown adapter and bad input branches.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): handleSettingsAdapterPost dispatches saves with chip/field error envelopes"
```

---

## Task 14: `handleSettingsAdapterPost` — success + error envelopes

**Files:**
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func TestAdapterSave_Success(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
		scope: "hot",
	}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "dlna", "enabled=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("body.ok = %v, want true", body["ok"])
	}
	if scope, _ := body["scope"].(string); scope != "hot" {
		t.Errorf("body.scope = %q, want 'hot'", scope)
	}
	if got := saver.touched["dlna"]; got["enabled"] != "true" {
		t.Errorf("saver.touched = %v, want enabled=true", got)
	}
}

func TestAdapterSave_DottedProviderKey(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool},
				{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
			},
		},
		scope: "hot",
	}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "streams", "providers.youtube.catalog_refresh_hours=12")
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	got := saver.touched["streams"]
	if got["providers.youtube.catalog_refresh_hours"] != "12" {
		t.Errorf("touched = %v, want dotted-key entry", got)
	}
}

// fakeSaverWithFieldErrors lets us inject a typed FieldErrors-bearing error.
type fakeSaverWithFieldErrors struct {
	fakeAdapterSettingsSaver
}

func (f *fakeSaverWithFieldErrors) SaveTouched(name string, touched map[string]string) (string, error) {
	return "", &chassisAdapterFieldErrors{Errs: []adapters.FieldError{
		{Key: "max_cache_bytes", Msg: "must be in [1 GiB, 1 TiB]"},
	}}
}

func TestAdapterSave_FieldErrors(t *testing.T) {
	t.Parallel()
	saver := &fakeSaverWithFieldErrors{fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"torrent": {"max_cache_bytes": int64(1 << 30)}},
		fields: map[string][]adapters.FieldDef{
			"torrent": {{Key: "max_cache_bytes", Kind: adapters.KindInt}},
		},
	}}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "torrent", "max_cache_bytes=99")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if okv, _ := body["ok"].(bool); okv {
		t.Errorf("body.ok = true, want false")
	}
	errsMap, _ := body["errors"].(map[string]any)
	if errsMap["max_cache_bytes"] != "must be in [1 GiB, 1 TiB]" {
		t.Errorf("body.errors = %v, want max_cache_bytes message", errsMap)
	}
}

func TestAdapterSave_ChipError(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
		saveErr: &chassisChipError{httpStatus: http.StatusInternalServerError, chip: "WRITE FAILED"},
	}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "dlna", "enabled=true")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if chip, _ := body["chip"].(string); chip != "WRITE FAILED" {
		t.Errorf("body.chip = %q, want WRITE FAILED", chip)
	}
}
```

Add test-only shim types in `settings_test.go` for this chassis handler test. Production error shims live in `cmd/mister-groovy-relay/` (Task 32) and satisfy the same structural interfaces:

```go
// chassisAdapterFieldErrors is a chassis-side concrete shim that the
// production AdapterSettingsSaver wrapper returns when SaveTouched
// fails with per-field errors. The chassis handler unwraps it via
// errors.As to render {ok:false, errors:{...}}.
type chassisAdapterFieldErrors struct {
	Errs []adapters.FieldError
}

func (e *chassisAdapterFieldErrors) Error() string { return "adapter field errors" }
func (e *chassisAdapterFieldErrors) FieldErrors() []adapters.FieldError { return e.Errs }

// chassisChipError is a chassis-side concrete shim for chip-style
// errors (matches the 4A settingsChipError contract).
type chassisChipError struct {
	httpStatus int
	chip       string
}

func (e *chassisChipError) Error() string     { return e.chip }
func (e *chassisChipError) StatusCode() int   { return e.httpStatus }
func (e *chassisChipError) Chip() string      { return e.chip }
```

Keep these in `settings_test.go`; do not add chassis concrete error types just for production. The production wrapper returns its own cmd-side concrete types in Task 32.

- [ ] **Step 2: Run tests to verify they fail/pass**

Run: `go test ./internal/chassis -run TestAdapterSave -v`
Expected: PASS for Success, DottedProviderKey, FieldErrors, ChipError if Task 13's handler is correct. Iterate on the handler until all pass.

- [ ] **Step 3: Implementation refinement**

If the FieldErrors / ChipError unwrap path needs adjustment in `emitSaveError`, fix it here. The structural `interface{ FieldErrors() []adapters.FieldError }` match should work with `errors.As` against an interface satisfied by `*chassisAdapterFieldErrors`. If it does not, use a concrete-type unwrap.

- [ ] **Step 4: Run all save tests**

Run: `go test ./internal/chassis -run TestAdapterSave -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "test(chassis): pin adapter save success + field/chip error envelopes"
```

---

## Task 14a: Writable-surface projection — reject Catalog-owned streams keys

**Spec ref:** spec §Goals 3 + §Wire Contract line 235 + §Testing line 312/318.

The Streams Catalog pane (4C) owns `providers.<id>.disabled` and `providers.<id>.hls_buffer_disabled`; the Streams form pane (this phase) owns `providers.<id>.catalog_refresh_hours` and the 15 top-level fields. A POST to `/receiver/settings/adapter/streams` that touches `providers.foo.disabled` must return 400 `BAD INPUT` — the chassis writable-surface allowlist excludes Catalog-owned keys.

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func TestAdapterSave_StreamsCatalogOwnedKeysRejected(t *testing.T) {
	t.Parallel()
	// Saver returns the streams adapter's *full* Fields() schema (including
	// the per-provider disabled / hls_buffer_disabled rows that the
	// adapter declares dynamically) — the chassis handler must reject
	// touches against those keys because Catalog owns them. The
	// projection lives in AdapterSettingsSaver.Fields(); the handler
	// only sees the projected list.
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		// Already-projected schema: catalog-owned keys absent.
		fields: map[string][]adapters.FieldDef{
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool},
				{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
			},
		},
		scope: "hot",
	}
	s := newTestServerForAdapterSave(saver)
	for _, key := range []string{
		"providers.foo.disabled",
		"providers.foo.hls_buffer_disabled",
	} {
		rec := postAdapterSave(t, s, "streams", key+"=true")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s Code = %d, want 400", key, rec.Code)
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if chip, _ := body["chip"].(string); chip != "BAD INPUT" {
			t.Errorf("POST %s body.chip = %q, want BAD INPUT", key, chip)
		}
	}
}

func TestAdapterSave_StreamsCatalogRefreshHoursAccepted(t *testing.T) {
	t.Parallel()
	// Counter-test: the chassis-owned key MUST pass through, so the
	// rejection above is the projection's responsibility, not a blanket
	// providers.* rejection.
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool},
				{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
			},
		},
		scope: "hot",
	}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "streams", "providers.foo.catalog_refresh_hours=12")
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/chassis -run "TestAdapterSave_StreamsCatalogOwnedKeysRejected|TestAdapterSave_StreamsCatalogRefreshHoursAccepted" -v`
Expected: PASS — Task 13's `keyMatchesSchema` already rejects keys not present in the projected schema. This task's contribution is fixture data that pins the chassis-side projection contract for the wrapper to honor (Task 32 implements the projection itself; this test asserts the handler interaction is correct given the projection).

- [ ] **Step 3: (no chassis-side impl change)**

The projection logic lives in the production `*bridgeAdapterSettingsSaver.Fields(name)` wrapper at Task 32 (line below) — not in the chassis. The chassis trusts what the saver returns.

- [ ] **Step 4: Confirm full chassis suite green**

Run: `go test ./internal/chassis -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings_test.go
git commit -m "test(chassis): streams Catalog-owned keys rejected at adapter save"
```

---

## Task 15: `handleSettingsActionStreamsRefresh` — success + refresh failure

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func newTestServerForStreamsRefresh(r StreamsRefresher) *Server {
	return &Server{
		cfg: Config{
			Version:          "test",
			StartedAt:        time.Unix(0, 0),
			StreamsRefresher: r,
		},
	}
}

func postStreamsRefresh(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/receiver/settings/action/streams-refresh", nil)
	rec := httptest.NewRecorder()
	s.handleSettingsActionStreamsRefresh(rec, req)
	return rec
}

func TestStreamsRefresh_Success(t *testing.T) {
	t.Parallel()
	refresher := &fakeStreamsRefresher{result: StreamsRefreshResult{Source: "remote", DurationMS: 42}}
	s := newTestServerForStreamsRefresh(refresher)
	rec := postStreamsRefresh(t, s)
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if okv, _ := body["ok"].(bool); !okv {
		t.Errorf("body.ok = %v, want true", body["ok"])
	}
	if src, _ := body["source"].(string); src != "remote" {
		t.Errorf("body.source = %q, want 'remote'", src)
	}
	// JSON numbers decode as float64.
	if dur, _ := body["duration_ms"].(float64); int64(dur) < 1 {
		t.Errorf("body.duration_ms = %v, want a positive measured value", body["duration_ms"])
	}
	if calls := refresher.calls.Load(); calls != 1 {
		t.Errorf("refresher.calls = %d, want 1", calls)
	}
}

func TestStreamsRefresh_RefreshFailure(t *testing.T) {
	t.Parallel()
	refresher := &fakeStreamsRefresher{
		result: StreamsRefreshResult{Source: "remote"},
		err:    errors.New("connection refused"),
	}
	s := newTestServerForStreamsRefresh(refresher)
	rec := postStreamsRefresh(t, s)
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200 (action ran cleanly); body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if okv, _ := body["ok"].(bool); okv {
		t.Errorf("body.ok = true, want false")
	}
	got, _ := body["error"].(string)
	if !strings.Contains(got, "connection refused") {
		t.Errorf("body.error = %q, want substring 'connection refused'", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chassis -run TestStreamsRefresh_ -v`
Expected: FAIL — skeleton handler still returns NOT IMPLEMENTED.

- [ ] **Step 3: Implement the handler**

Replace the skeleton in `internal/chassis/settings.go`:

```go
// streamsRefreshGate enforces process-wide single-flight for the
// /streams-refresh action. TryLock-style: a second concurrent click
// returns BUSY rather than queueing behind the first.
var streamsRefreshGate sync.Mutex

const streamsRefreshTimeout = 30 * time.Second

func (s *Server) handleSettingsActionStreamsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StreamsRefresher == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if !streamsRefreshGate.TryLock() {
		writeSettingsChip(w, http.StatusConflict, "BUSY")
		return
	}
	defer streamsRefreshGate.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), streamsRefreshTimeout)
	defer cancel()
	start := time.Now()
	result, err := s.cfg.StreamsRefresher.RefreshNow(ctx)
	elapsed := time.Since(start)
	if err != nil {
		writeStreamsRefreshError(w, sanitizeRefreshError(err))
		return
	}
	if result.Err != nil {
		writeStreamsRefreshError(w, sanitizeRefreshError(result.Err))
		return
	}
	durationMS := result.DurationMS
	if durationMS == 0 {
		durationMS = elapsed.Milliseconds()
	}
	writeStreamsRefreshSuccess(w, result.Source, durationMS)
}

func writeStreamsRefreshSuccess(w http.ResponseWriter, source string, durationMS int64) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{
		"ok":          true,
		"summary":     fmt.Sprintf("Manifest refreshed from %s in %dms", source, durationMS),
		"source":      source,
		"duration_ms": durationMS,
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeStreamsRefreshError(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{
		"ok":    false,
		"error": fmt.Sprintf("manifest refresh failed: %s", reason),
	}
	_ = json.NewEncoder(w).Encode(body)
}

// sanitizeRefreshError trims the upstream error message to 200 chars
// (matching 4A's sanitizeProbeError cap) so the .action-result slot
// has predictable size.
func sanitizeRefreshError(err error) string {
	const cap = 200
	msg := strings.TrimSpace(err.Error())
	if len(msg) > cap {
		msg = msg[:cap-3] + "..."
	}
	return msg
}
```

Confirm `sync`, `time`, `context`, `fmt`, `strings`, `encoding/json` are imported.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis -run TestStreamsRefresh_ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): handleSettingsActionStreamsRefresh success + refresh failure paths"
```

---

## Task 16: Streams refresh — BUSY single-flight + timeout

**Files:**
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func TestStreamsRefresh_BusyOnConcurrentClick(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	refresher := &fakeStreamsRefresher{
		result: StreamsRefreshResult{Source: "remote"},
		gate:   gate,
	}
	s := newTestServerForStreamsRefresh(refresher)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- postStreamsRefresh(t, s)
	}()

	// Wait for the first request to be inside RefreshNow.
	deadline := time.Now().Add(2 * time.Second)
	for refresher.calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if calls := refresher.calls.Load(); calls < 1 {
		t.Fatalf("first refresh never reached RefreshNow; calls = %d", calls)
	}

	rec := postStreamsRefresh(t, s)
	if rec.Code != http.StatusConflict {
		t.Errorf("second-click Code = %d, want 409", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if chip, _ := body["chip"].(string); chip != "BUSY" {
		t.Errorf("body.chip = %q, want BUSY", chip)
	}

	close(gate)
	first := <-done
	if first.Code != http.StatusOK {
		t.Errorf("first-click Code = %d, want 200", first.Code)
	}
}

func TestStreamsRefresh_ContextTimeout(t *testing.T) {
	t.Parallel()
	// The fake honors ctx via its `gate` channel; never close gate, so
	// ctx.Done fires first.
	refresher := &fakeStreamsRefresher{
		result: StreamsRefreshResult{Source: "remote"},
		gate:   make(chan struct{}),
	}
	s := &Server{cfg: Config{
		Version:          "test",
		StartedAt:        time.Unix(0, 0),
		StreamsRefresher: refresher,
	}}

	// Drive the test with a 50ms request-context override so we don't
	// wait the full 30s. The handler should respect r.Context()'s
	// deadline via its WithTimeout wrap.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, "POST", "/receiver/settings/action/streams-refresh", nil)
	rec := httptest.NewRecorder()
	s.handleSettingsActionStreamsRefresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200 (action ran cleanly even on timeout)", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if okv, _ := body["ok"].(bool); okv {
		t.Errorf("body.ok = true, want false")
	}
	got, _ := body["error"].(string)
	if !strings.Contains(got, "deadline exceeded") && !strings.Contains(got, "context canceled") {
		t.Errorf("body.error = %q, want substring 'deadline exceeded' or 'context canceled'", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/chassis -run "TestStreamsRefresh_BusyOnConcurrentClick|TestStreamsRefresh_ContextTimeout" -v`
Expected: PASS if Task 15's TryLock + r.Context() wrap are correct. If not, refine the handler.

- [ ] **Step 3: Implementation adjustments**

If the timeout test fails because the handler uses `context.Background` for the `WithTimeout` parent instead of `r.Context()`, fix the handler to chain off `r.Context()` as Task 15 specifies.

- [ ] **Step 4: Run all streams-refresh tests**

Run: `go test ./internal/chassis -run TestStreamsRefresh -v`
Expected: PASS for all.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "test(chassis): streams-refresh BUSY single-flight + context timeout paths"
```

---

## Task 17: Extend `SettingsData` with adapter sections + scaffolding

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/chassis_test.go` (or wherever buildSettingsData tests live)

- [ ] **Step 1: Write the failing test**

Append to the relevant chassis test file:

```go
func TestSettingsData_PopulatesAdapters(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"dlna":    {"enabled": true, "device_name": "M"},
			"torrent": {"enabled": false, "traffic_acknowledged": false},
			"streams": {"enabled": true, "manifest_url": "https://x/y.json"},
		},
		fields: map[string][]adapters.FieldDef{
			"dlna": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "device_name", Kind: adapters.KindText, Label: "Device name", ApplyScope: adapters.ScopeRestartBridge},
			},
			"torrent": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "traffic_acknowledged", Kind: adapters.KindBool, Label: "BT traffic acknowledged", ApplyScope: adapters.ScopeHotSwap},
			},
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "manifest_url", Kind: adapters.KindText, Label: "Manifest URL", ApplyScope: adapters.ScopeHotSwap},
			},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	data := s.buildSettingsData()
	if len(data.Adapters) != 3 {
		t.Fatalf("len(Adapters) = %d, want 3", len(data.Adapters))
	}
	byName := map[string]AdapterPaneData{}
	for _, a := range data.Adapters {
		byName[a.Name] = a
	}
	if dlna, ok := byName["dlna"]; !ok || len(dlna.Fields) != 2 {
		t.Errorf("dlna pane not populated: %+v", byName)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsData_PopulatesAdapters -v`
Expected: FAIL — `SettingsData` has no `Adapters` field.

- [ ] **Step 3: Extend `SettingsData`**

In `internal/chassis/data.go`, add:

```go
// AdapterPaneData carries the per-adapter render context for the
// Adapters pane. Populated by buildSettingsData from
// AdapterSettingsSaver for the three real adapters; stubs are
// emitted by the template directly without an AdapterPaneData entry.
type AdapterPaneData struct {
	Name       string                  // "dlna", "torrent", "streams"
	Hint       string                  // section header subtitle (e.g. "PUSH · LISTENING")
		Fields     []adapters.FieldDef     // 4D writable schema; provider wildcard rows are not top-level rendered
	Values     map[string]any          // current values, keyed by Field.Key
	Providers  []AdapterProviderRow    // streams only; empty for dlna/torrent
}

// AdapterProviderRow is one row in the Streams per-provider sub-section.
// CatalogRefreshHours type matches CatalogProviderState.CatalogRefreshHours
// (int) — sourced from the streams.ProviderConfig field of the same name.
type AdapterProviderRow struct {
	ID                  string
	DisplayName         string
	CatalogRefreshHours int
}

type SettingsData struct {
	// ... existing fields ...
	Adapters []AdapterPaneData
}
```

Extend `SettingsData` (the existing struct) and add a new `*Server` method that composes the package-level `buildSettingsData` with the new adapter-populating step. **The existing `buildSettingsData` is a package function** at `data.go:297` with signature `func buildSettingsData(bridge config.BridgeConfig, registry *adapters.Registry, catalog adapters.StreamsCatalogViewer, catalogManager CatalogSettingsManager) SettingsData` (4C extended the original signature with the `catalogManager` parameter; verify the exact arg list at task start). 4D does **not** edit the existing function's signature again; instead, it adds a thin wrapper method on `*Server` that calls the function and then appends the `Adapters` slice. This keeps existing 4A/4B/4C call sites untouched and gives Tasks 18-25's test code a single entry point (`s.buildSettingsData()`).

Add to `internal/chassis/data.go`:

```go
// buildSettingsData (method) wraps the package-level buildSettingsData
// and adds the 4D-owned Adapters slice. Used by every chassis handler
// that renders the settings drawer. Reuses the package function's
// existing 4-arg signature (4C extended it with catalogManager); 4D
// adds no parameters to the function itself.
func (s *Server) buildSettingsData() SettingsData {
	data := buildSettingsData(
		s.cfg.BridgeSaver.Current(),  // or s.cfg.Bridge if no saver wired (offline tests)
		s.cfg.Registry,
		s.cfg.CatalogViewer,           // adapters.StreamsCatalogViewer
		s.cfg.CatalogManager,          // 4C-introduced
	)
	if saver := s.cfg.AdapterSettingsSaver; saver != nil {
		for _, name := range []string{"dlna", "torrent", "streams"} {
			fields, ok := saver.Fields(name)
			if !ok {
				continue
			}
			values, _ := saver.Current(name)
			data.Adapters = append(data.Adapters, AdapterPaneData{
				Name:   name,
				Hint:   "", // populated by Task 19
				Fields: fields,
				Values: values,
			})
		}
	}
	return data
}
```

Verify the actual Config-field names at task start — the snippet above uses placeholder names (`s.cfg.BridgeSaver`, `s.cfg.CatalogViewer`) that may differ in the merged 4C branch. Update every existing chassis HTTP handler that currently calls `buildSettingsData(...)` directly to call `s.buildSettingsData()` instead. Behavior is unchanged for 4A/4B/4C panes; only the `Adapters` slice is newly populated.

Update every chassis HTTP handler that currently calls the package function to use the new method (`s.buildSettingsData()`) — keeping a consistent entry point for 4D and beyond. The behavior is unchanged for 4A/4B/4C panes; only the `Adapters` slice is newly populated.

For reference, here's the populate-`Adapters` body in isolation (it's the only new logic in the method):

```go
// Inside whichever entry point is chosen — function body or wrapper method.
if saver := s.cfg.AdapterSettingsSaver; saver != nil {
	for _, name := range []string{"dlna", "torrent", "streams"} {
		fields, ok := saver.Fields(name)
		if !ok {
			continue
		}
		values, _ := saver.Current(name)
		data.Adapters = append(data.Adapters, AdapterPaneData{
			Name:   name,
			Hint:   "", // populated by Task 19
			Fields: fields,
			Values: values,
		})
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestSettingsData_PopulatesAdapters -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/data.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): extend SettingsData with AdapterPaneData entries"
```

---

## Task 18: Populate streams `Providers` from CatalogManager

**Prerequisite:** This task reads from 4C surfaces that should already be present in the branch (you should have branched 4D from `phase-4c-catalog-pane` per the Prerequisites section). Specifically: `s.cfg.CatalogManager.Providers()` returns `[]chassis.CatalogProviderState` with fields `ID`, `DisplayName`, `ChannelCount`, and `CatalogRefreshHours` (int). All four fields are populated by 4C's `cmd/mister-groovy-relay/catalog_manager.go` wrapper from the streams adapter's per-provider config.

If you branched 4D from `main` instead and the grep check at Prerequisites fails, fall back to the documented `StreamsProviderLister` interface and substitute `s.cfg.StreamsProviderLister` for `s.cfg.CatalogManager` here. The rest of the task structure is identical — both paths fill `AdapterProviderRow` from a sorted provider list.

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append to the chassis tests:

```go
type fakeCatalogManagerProviders struct {
	providers []CatalogProviderState
}

// Spec-aligned: 4D reads provider list through 4C's CatalogSettingsManager
// interface, specifically the existing Providers() method. The fake here
// must satisfy whatever signature 4C ships. If 4C uses a different name
// for the method (e.g., List, ProviderStates), substitute accordingly
// — the spec assumes Providers().
func (f *fakeCatalogManagerProviders) Providers() []CatalogProviderState {
	return f.providers
}

func TestSettingsData_StreamsProvidersFromCatalog(t *testing.T) {
	t.Parallel()
	// The streams adapter's CurrentValues() contents are no longer
	// read for the per-provider override (4C's CatalogProviderState
	// carries CatalogRefreshHours directly); the values map is still
	// passed in for top-level fields the form needs.
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"streams": {
				"enabled":      true,
				"manifest_url": "https://x/y.json",
			},
		},
		fields: map[string][]adapters.FieldDef{
			"streams": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	cat := &fakeCatalogManagerProviders{providers: []CatalogProviderState{
		{ID: "youtube", DisplayName: "YouTube", CatalogRefreshHours: 12},
		{ID: "radio", DisplayName: "Radio"}, // CatalogRefreshHours zero → "inherit global"
	}}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
		CatalogManager:       cat,
	}}
	data := s.buildSettingsData()
	var streams *AdapterPaneData
	for i, a := range data.Adapters {
		if a.Name == "streams" {
			streams = &data.Adapters[i]
			break
		}
	}
	if streams == nil {
		t.Fatalf("streams pane missing")
	}
	if len(streams.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(streams.Providers))
	}
	byID := map[string]AdapterProviderRow{}
	for _, p := range streams.Providers {
		byID[p.ID] = p
	}
	if got := byID["youtube"]; got.DisplayName != "YouTube" || got.CatalogRefreshHours != 12 {
		t.Errorf("youtube row = %+v", got)
	}
	if got := byID["radio"]; got.CatalogRefreshHours != 0 {
		t.Errorf("radio row CatalogRefreshHours = %d, want 0 (no override)", got.CatalogRefreshHours)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsData_StreamsProvidersFromCatalog -v`
Expected: FAIL — `buildSettingsData` does not populate `Providers`.

- [ ] **Step 3: Wire in `Providers` population**

In `internal/chassis/data.go`, extend the `Server.buildSettingsData` wrapper method from Task 17 with a streams-branch that projects `CatalogManager.Providers()` into `AdapterProviderRow` rows. 4C's `CatalogProviderState` already carries `CatalogRefreshHours`, so no values-map dig is needed:

```go
// In the per-adapter loop inside Server.buildSettingsData (Task 17):
pane := AdapterPaneData{
	Name:   name,
	Fields: fields,
	Values: values,
}
if name == "streams" {
	pane.Providers = s.buildStreamsProviderRows()
}
data.Adapters = append(data.Adapters, pane)
```

Add the helper:

```go
// buildStreamsProviderRows projects 4C's CatalogManager.Providers()
// output into the Streams-pane per-provider override row shape.
// CatalogRefreshHours comes directly from CatalogProviderState — the
// 4C production wrapper (cmd/mister-groovy-relay/catalog_manager.go)
// threads streams.ProviderConfig.CatalogRefreshHours through. Returns
// nil when CatalogManager is unwired (offline tests).
func (s *Server) buildStreamsProviderRows() []AdapterProviderRow {
	if s.cfg.CatalogManager == nil {
		return nil
	}
	providers := s.cfg.CatalogManager.Providers()
	rows := make([]AdapterProviderRow, 0, len(providers))
	for _, p := range providers {
		rows = append(rows, AdapterProviderRow{
			ID:                  p.ID,
			DisplayName:         p.DisplayName,
			CatalogRefreshHours: p.CatalogRefreshHours,
		})
	}
	return rows
}
```

Provider ordering matches whatever order 4C's `CatalogSettingsManager.Providers()` returns (which is registration order from `streams.Adapter.BundledCatalog()`); no re-sort needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestSettingsData_StreamsProvidersFromCatalog -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/data.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): populate streams Providers rows from CatalogManager"
```

---

## Task 19: Compute per-adapter section header hints

**Prerequisite:** Same as Task 18 — the Streams hint reads channel count from 4C surfaces. Prefer the existing `s.cfg.CatalogManager.Providers()` returning `CatalogProviderState.ChannelCount`. If you branched from `main` and 4C is unmerged, substitute the `StreamsProviderLister` fallback's `ChannelCount` field.

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestSettingsData_DLNAHint_Listening(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "dlna" {
			if a.Hint != "PUSH · LISTENING" {
				t.Errorf("dlna hint = %q, want 'PUSH · LISTENING'", a.Hint)
			}
		}
	}
}

func TestSettingsData_DLNAHint_Disabled(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "dlna" && a.Hint != "PUSH · DISABLED" {
			t.Errorf("dlna hint = %q, want 'PUSH · DISABLED'", a.Hint)
		}
	}
}

func TestSettingsData_TorrentHint_Static(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"torrent": {"enabled": false}},
		fields: map[string][]adapters.FieldDef{
			"torrent": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "torrent" && a.Hint != "PASTE-IN · BT" {
			t.Errorf("torrent hint = %q, want 'PASTE-IN · BT'", a.Hint)
		}
	}
}

func TestSettingsData_StreamsHint_ChannelCount(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"streams": {{Key: "enabled", Kind: adapters.KindBool}},
		},
	}
	// 4C populates CatalogChannelCount on SettingsData; the buildSettingsData
	// function must have already computed it before computing the streams hint.
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	// Inject CatalogChannelCount by stubbing whatever 4C field populates it.
	// If 4C exposes a CatalogManager interface that returns channel count,
	// inject that here; otherwise pre-set the count in a way matching 4C.
	data := s.buildSettingsData()
	for _, a := range data.Adapters {
		if a.Name == "streams" {
			// Expectation: hint reads "PULL · 0 CHANNELS · see Catalog tab"
			// when CatalogManager is nil; otherwise "PULL · N CHANNELS · see Catalog tab".
			if !strings.HasPrefix(a.Hint, "PULL") || !strings.Contains(a.Hint, "CHANNELS") {
				t.Errorf("streams hint = %q, want PULL · N CHANNELS prefix", a.Hint)
			}
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chassis -run "TestSettingsData_DLNAHint|TestSettingsData_TorrentHint|TestSettingsData_StreamsHint" -v`
Expected: FAIL — `Hint` is always empty.

- [ ] **Step 3: Implement hint computation**

Add to `internal/chassis/data.go`:

```go
func (s *Server) buildAdapterHint(name string, values map[string]any) string {
	switch name {
	case "dlna":
		if v, _ := values["enabled"].(bool); v {
			return "PUSH · LISTENING"
		}
		return "PUSH · DISABLED"
	case "torrent":
		return "PASTE-IN · BT"
	case "streams":
		// 4C exposes CatalogChannelCount on SettingsData; we cannot read it
		// from buildAdapterHint directly because SettingsData is still
		// under construction. Compute the same value here from
		// CatalogManager — it's the canonical source 4C uses.
		n := 0
		if s.cfg.CatalogManager != nil {
			for _, p := range s.cfg.CatalogManager.Providers() {
				n += p.ChannelCount // verify exact field name on the 4C struct
			}
			return fmt.Sprintf("PULL · %d CHANNELS · see Catalog tab", n)
		}
		return fmt.Sprintf("PULL · %d CHANNELS", n)
	}
	return ""
}
```

Wire it into `buildSettingsData`:

```go
pane := AdapterPaneData{
	Name:   name,
	Fields: fields,
	Values: values,
	Hint:   s.buildAdapterHint(name, values),
}
```

	No extra chassis interface is introduced for hints. 4D keeps the spec's two-interface boundary (`AdapterSettingsSaver`, `StreamsRefresher`) and derives the DLNA hint from the rendered config snapshot.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis -run "TestSettingsData_DLNAHint|TestSettingsData_TorrentHint|TestSettingsData_StreamsHint" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/data.go internal/chassis/settings.go internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): compute per-adapter section header hints"
```

---

## Task 20: Create `settings-adapters.html` container template

**Files:**
- Create: `internal/chassis/templates/settings-adapters.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestSettingsAdaptersTemplate_RendersSixSections(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"dlna":    {"enabled": true, "device_name": "M"},
			"torrent": {"enabled": false, "traffic_acknowledged": false},
			"streams": {"enabled": true},
		},
		fields: map[string][]adapters.FieldDef{
			"dlna":    {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
			"torrent": {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
			"streams": {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	html := renderDrawer(t, s)
	for _, want := range []string{
		// Six section headers in mockup order.
		`>Plex<`, `>DLNA<`, `>URL<`, `>Torrent<`, `>Jellyfin<`, `>Streams catalog<`,
		// Three stubs all carry the "pending" hint.
		`>— pending<`,
		// Three real sections carry computed hints (rough substring check).
		`PUSH ·`, `PASTE-IN · BT`, `PULL ·`,
		// Spec labels on the three stubs.
		`Spec 4E`, `Spec 4F`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered drawer does not contain %q", want)
		}
	}
}
```

`renderDrawer` is the chassis test helper that renders the full drawer template. Reuse the helper 4A/4B established; if it doesn't exist, write one that exercises `s.renderSettingsDrawer(buf, data)` or whatever the production entrypoint is.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsAdaptersTemplate_RendersSixSections -v`
Expected: FAIL — template not yet defined; the existing stub is still rendered.

- [ ] **Step 3: Create the container template**

Create `internal/chassis/templates/settings-adapters.html`:

```html
{{- define "settings-adapters" -}}
<div class="settings-pane" data-pane="adapters">

  <!-- Plex (4E) -->
  <section class="settings-section">
    <h4>Plex <span class="hint">— pending</span></h4>
    <div class="action-result shown">▸ Spec 4E — implementation in progress</div>
  </section>

  {{ template "settings-adapter-dlna" (adapterPane .Adapters "dlna") }}

  <!-- URL (4F) -->
  <section class="settings-section">
    <h4>URL <span class="hint">— pending</span></h4>
    <div class="action-result shown">▸ Spec 4F — implementation in progress</div>
  </section>

  {{ template "settings-adapter-torrent" (adapterPane .Adapters "torrent") }}

  <!-- Jellyfin (4E) -->
  <section class="settings-section">
    <h4>Jellyfin <span class="hint">— pending</span></h4>
    <div class="action-result shown">▸ Spec 4E — implementation in progress</div>
  </section>

  {{ template "settings-adapter-streams" (adapterPane .Adapters "streams") }}

</div>
{{- end -}}
```

Add an `adapterPane` template helper in the same file as the existing helpers (`internal/chassis/templates.go` or wherever `field`/`dict` live):

```go
// adapterPane returns the AdapterPaneData for the named adapter, or a
// zero-valued AdapterPaneData if the slice has no entry (e.g., the
// AdapterSettingsSaver is nil in offline tests). Keeps templates from
// panicking on missing data.
func adapterPane(adapters []AdapterPaneData, name string) AdapterPaneData {
	for _, a := range adapters {
		if a.Name == name {
			return a
		}
	}
	return AdapterPaneData{Name: name}
}
```

Register `"adapterPane"` in the template `Funcs` map alongside `field`, `dict`, etc.

- [ ] **Step 4: Run test (still fails until per-adapter sub-templates exist)**

Run: `go test ./internal/chassis -run TestSettingsAdaptersTemplate_RendersSixSections -v`
Expected: still FAIL — `settings-adapter-dlna` etc. aren't defined yet. That's OK; subsequent tasks add them. Move on to Task 21.

- [ ] **Step 5: Do not commit yet**

The render test is intentionally red until the three real sub-templates exist. Carry these edits forward and commit after Task 24, when the drawer can render green.

---

## Task 21: Wire `settings-adapters` into the drawer

**Files:**
- Modify: `internal/chassis/templates/settings-drawer.html`

- [ ] **Step 1: (test is the same as Task 20; will pass once sub-templates land)**

- [ ] **Step 2: Replace the stub line in `settings-drawer.html`**

Change:
```html
    {{ template "settings-stub" (stub "adapters" "Adapter forms" "4D – 4F") }}
```

To:
```html
    {{ template "settings-adapters" . }}
```

- [ ] **Step 3: Build to verify template parses**

Run: `go build ./internal/chassis`
Expected: build succeeds. Go's template parser allows unresolved `{{ template }}` calls until execution, so full drawer render tests remain deferred until Tasks 22-24 add the sub-templates.

- [ ] **Step 4: Run the chassis tests not depending on sub-templates**

Run: `go test ./internal/chassis -run "TestAdapterSettingsSaver|TestStreamsRefresher|TestAdapterSave|TestStreamsRefresh|TestSettingsData" -v`
Expected: PASS for all (these tests don't render the full drawer).

- [ ] **Step 5: Do not commit yet**

Wiring the drawer before the sub-templates exist makes the full drawer render fail. Carry this edit forward and commit with Task 24's template set.

---

## Task 22: Create `settings-adapter-dlna.html`

**Files:**
- Create: `internal/chassis/templates/settings-adapter-dlna.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing render test**

Append:

```go
func TestSettingsAdapterDLNATemplate_RendersFields(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"dlna": {"enabled": true, "device_name": "MiSTer"}},
		fields: map[string][]adapters.FieldDef{
			"dlna": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "device_name", Kind: adapters.KindText, Label: "Device name", ApplyScope: adapters.ScopeRestartBridge, Required: true},
				{Key: "autoplay_on_set_uri", Kind: adapters.KindBool, Label: "Autoplay on SetURI", ApplyScope: adapters.ScopeHotSwap},
				{Key: "allow_public_source_urls", Kind: adapters.KindBool, Label: "Allow public source URLs", ApplyScope: adapters.ScopeHotSwap},
			},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	html := renderDrawer(t, s)
	for _, want := range []string{
		`name="enabled"`,
		`name="device_name"`,
		`name="autoplay_on_set_uri"`,
		`name="allow_public_source_urls"`,
		// Scope badges match code (not the buggy mockup).
		`<span class="scope hot">HOT</span>`,
		`<span class="scope reboot">REBOOT</span>`,
		// data-adapter attribute steers JS to the right route.
		`data-adapter="dlna"`,
		// Section header hint.
		`PUSH · LISTENING`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered DLNA section does not contain %q", want)
		}
	}
	// device_name is REBOOT; the mockup shows it as REBOOT too (correct match).
	// enabled is HOT in code; the mockup shows REBOOT (wrong) — assert HOT renders.
	if !strings.Contains(html, `name="enabled"`) {
		t.Errorf("missing enabled input")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsAdapterDLNATemplate_RendersFields -v`
Expected: FAIL — template not defined.

- [ ] **Step 3: Create the template**

Create `internal/chassis/templates/settings-adapter-dlna.html`:

```html
{{- define "settings-adapter-dlna" -}}
<section class="settings-section">
  <h4>DLNA <span class="hint">{{ .Hint }}</span></h4>
  {{ range .Fields }}
  {{ template "adapter-field-row" (dict
    "Adapter" "dlna"
    "Field" .
    "Values" $.Values
  ) }}
  {{ end }}
</section>
{{- end -}}
```

Add a shared partial `adapter-field-row` in the same file (or in `settings-adapters.html`):

```html
{{- define "adapter-field-row" -}}
{{- $fd := .Field -}}
{{- $val := index .Values $fd.Key -}}
{{- $scope := adapterScopeWire $fd.ApplyScope -}}
{{- $kind := fieldKindWire $fd.Kind -}}
{{ field (dict
  "Name" $fd.Key
  "Type" $kind
  "Label" $fd.Label
  "Help" $fd.Help
  "Value" (adapterFieldValue $val $kind)
  "Scope" $scope
  "Adapter" .Adapter
  "Required" $fd.Required) }}
{{- end -}}
```

Add helper functions and register `fieldKindWire`, `adapterScopeWire`, and `adapterFieldValue` in `templateFuncs`:

```go
// fieldKindWire maps a FieldKind to the field-helper "Type" string.
func fieldKindWire(k adapters.FieldKind) string {
	switch k {
	case adapters.KindBool:
		return "switch"
	case adapters.KindInt:
		return "number"
	case adapters.KindSecret:
		return "password"
	case adapters.KindEnum:
		return "select"
	case adapters.KindText:
		return "text"
	}
	return "text"
}

func adapterScopeWire(scope adapters.ApplyScope) string {
	label, ok := scopeLabel(scope)
	if !ok {
		return "hot"
	}
	return label
}

// adapterFieldValue stringifies an arbitrary current-value into the
// form the `field` helper accepts.
func adapterFieldValue(v any, kind string) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		// JSON-decoded numbers; ints round-trip via Itoa.
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}
```

Extend the existing `field` helper to accept the `Adapter` option and emit `data-adapter="<adapter>"` on inputs and switches. The Adapter option is non-empty for adapter-section fields and empty for bridge-section fields, so the 4A bridge save path remains unchanged.

- [ ] **Step 4: Run the test**

Run: `go test ./internal/chassis -run TestSettingsAdapterDLNATemplate_RendersFields -v`
Expected: this may still fail if Task 21 has already wired the container and the Torrent/Streams sub-templates are not present yet. Confirm the DLNA template parses, then carry the edit forward to Task 24 for the green render pass.

- [ ] **Step 5: Do not commit yet**

Commit the three sub-templates together in Task 24 so the drawer never lands in a red intermediate state.

---

## Task 23: Create `settings-adapter-torrent.html`

**Files:**
- Create: `internal/chassis/templates/settings-adapter-torrent.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing render test**

Append:

```go
func TestSettingsAdapterTorrentTemplate_RendersFields(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"torrent": {
				"enabled":                  false,
				"traffic_acknowledged":     false,
				"download_dir":             "",
				"keep_completed":           false,
				"max_cache_bytes":          int64(20 * 1024 * 1024 * 1024),
				"metadata_timeout_seconds": int64(60),
				"startup_buffer_seconds":   int64(10),
				"max_upload_rate_kbps":     int64(512),
				"max_download_rate_kbps":   int64(0),
				"listen_port":              int64(0),
			},
		},
		fields: map[string][]adapters.FieldDef{
			"torrent": {
				{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
				{Key: "traffic_acknowledged", Kind: adapters.KindBool, Label: "Traffic acknowledged", ApplyScope: adapters.ScopeHotSwap},
				{Key: "download_dir", Kind: adapters.KindText, Label: "Download directory", ApplyScope: adapters.ScopeRestartCast},
				{Key: "keep_completed", Kind: adapters.KindBool, Label: "Keep completed", ApplyScope: adapters.ScopeHotSwap},
				{Key: "max_cache_bytes", Kind: adapters.KindInt, Label: "Max cache size", ApplyScope: adapters.ScopeHotSwap},
				{Key: "metadata_timeout_seconds", Kind: adapters.KindInt, Label: "Metadata timeout", ApplyScope: adapters.ScopeHotSwap},
				{Key: "startup_buffer_seconds", Kind: adapters.KindInt, Label: "Startup buffer", ApplyScope: adapters.ScopeHotSwap},
				{Key: "max_upload_rate_kbps", Kind: adapters.KindInt, Label: "Max upload rate", ApplyScope: adapters.ScopeRestartCast},
				{Key: "max_download_rate_kbps", Kind: adapters.KindInt, Label: "Max download rate", ApplyScope: adapters.ScopeRestartCast},
				{Key: "listen_port", Kind: adapters.KindInt, Label: "Listen port", ApplyScope: adapters.ScopeRestartCast},
			},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	html := renderDrawer(t, s)
	for _, want := range []string{
			`<section class="settings-section wide"`,
		`>Torrent <`,
		`PASTE-IN · BT`,
		`name="traffic_acknowledged"`,
		`name="download_dir"`,
		`name="max_cache_bytes"`,
		// humanizeBytes hint in row-end.
		`20 GB`,
		// Scope mix per real code (not mockup).
		`<span class="scope recast">RECAST</span>`,
		`<span class="scope hot">HOT</span>`,
		`data-adapter="torrent"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered Torrent section does not contain %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsAdapterTorrentTemplate_RendersFields -v`
Expected: FAIL — template not defined.

- [ ] **Step 3: Create the template**

Create `internal/chassis/templates/settings-adapter-torrent.html`:

```html
{{- define "settings-adapter-torrent" -}}
<section class="settings-section wide">
  <h4>Torrent <span class="hint">{{ .Hint }}</span></h4>
  {{ range .Fields }}
    {{ if eq .Key "max_cache_bytes" }}
      {{ template "adapter-field-row-bytes" (dict
        "Adapter" "torrent"
        "Field" .
        "Values" $.Values
      ) }}
    {{ else }}
      {{ template "adapter-field-row" (dict
        "Adapter" "torrent"
        "Field" .
        "Values" $.Values
      ) }}
    {{ end }}
  {{ end }}
</section>
{{- end -}}

{{- define "adapter-field-row-bytes" -}}
{{- $fd := .Field -}}
{{- $val := index .Values $fd.Key -}}
{{- $scope := adapterScopeWire $fd.ApplyScope -}}
{{ field (dict
  "Name" $fd.Key
  "Type" "number"
  "Label" $fd.Label
  "Help" $fd.Help
  "Value" (adapterFieldValue $val "number")
  "Scope" $scope
  "Adapter" .Adapter
  "RowEnd" (humanizeBytes (asInt64 $val))) }}
{{- end -}}
```

The `asInt64` helper coerces an `any` to `int64` for `humanizeBytes`. Add it alongside the other helpers:

```go
func asInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}
```

Extend the `field` helper's option-bag handling to accept a `RowEnd` argument and emit it inside `<span class="row-end">` next to the input. If the `field` helper already supports `RowEnd` from 4B (used for HLS humanizeBytes hints), reuse the existing branch.

- [ ] **Step 4: Run the test**

Run: `go test ./internal/chassis -run TestSettingsAdapterTorrentTemplate_RendersFields -v`
Expected: this may still fail until the Streams sub-template lands. Confirm the Torrent template parses, then carry the edit forward to Task 24 for the green render pass.

- [ ] **Step 5: Do not commit yet**

Commit the three sub-templates together in Task 24 so the drawer never lands in a red intermediate state.

---

## Task 24: Create `settings-adapter-streams.html` — top-level fields

**Files:**
- Create: `internal/chassis/templates/settings-adapter-streams.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing render test**

Append:

```go
func TestSettingsAdapterStreamsTemplate_RendersTopLevelFields(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"streams": {
				"enabled":                          true,
				"manifest_url":                     "https://x/y.json",
				"manifest_refresh_hours":           int64(24),
				"catalog_refresh_hours":            int64(12),
				"max_manifest_bytes":               int64(1048576),
				"max_catalog_bytes":                int64(10485760),
				"max_items_per_channel":            int64(5000),
				"max_consecutive_failures":         int64(25),
				"manifest_request_timeout_seconds": int64(10),
				"catalog_request_timeout_seconds":  int64(20),
				"youtube_format":                   "b[height<=480]/bv*[height<=480]+ba/bv*+ba/b",
				"allow_remote_manifest":            true,
				"allow_cached_remote_manifest":     false,
				"allow_local_manifest_urls":        false,
				"remote_provider_allowed_hosts":    "",
			},
		},
		fields: map[string][]adapters.FieldDef{
			"streams": streamsTopLevelFieldsForTest(),
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	html := renderDrawer(t, s)
	for _, want := range []string{
		`>Streams catalog <`,
		`PULL ·`,
		`name="enabled"`,
		`name="manifest_url"`,
		`name="manifest_refresh_hours"`,
		`name="catalog_refresh_hours"`,
		`name="max_manifest_bytes"`,
		`name="youtube_format"`,
		`name="allow_remote_manifest"`,
		`name="allow_local_manifest_urls"`,
		`name="remote_provider_allowed_hosts"`,
		`data-adapter="streams"`,
		// Scope is RECAST for youtube_format.
		`<span class="scope recast">RECAST</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered Streams top-level fields missing %q", want)
		}
	}
}

func streamsTopLevelFieldsForTest() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_url", Kind: adapters.KindText, Label: "Manifest URL", ApplyScope: adapters.ScopeHotSwap, Required: true},
		{Key: "manifest_refresh_hours", Kind: adapters.KindInt, Label: "Manifest refresh hours", ApplyScope: adapters.ScopeHotSwap},
		{Key: "catalog_refresh_hours", Kind: adapters.KindInt, Label: "Catalog refresh hours", ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_manifest_bytes", Kind: adapters.KindInt, Label: "Max manifest bytes", ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_catalog_bytes", Kind: adapters.KindInt, Label: "Max catalog bytes", ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_items_per_channel", Kind: adapters.KindInt, Label: "Max items per channel", ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_consecutive_failures", Kind: adapters.KindInt, Label: "Max consecutive failures", ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_request_timeout_seconds", Kind: adapters.KindInt, Label: "Manifest timeout", ApplyScope: adapters.ScopeHotSwap},
		{Key: "catalog_request_timeout_seconds", Kind: adapters.KindInt, Label: "Catalog timeout", ApplyScope: adapters.ScopeHotSwap},
		{Key: "youtube_format", Kind: adapters.KindText, Label: "YouTube format", ApplyScope: adapters.ScopeRestartCast, Required: true},
		{Key: "allow_remote_manifest", Kind: adapters.KindBool, Label: "Allow remote manifest", ApplyScope: adapters.ScopeHotSwap},
		{Key: "allow_cached_remote_manifest", Kind: adapters.KindBool, Label: "Allow cached remote manifest", ApplyScope: adapters.ScopeHotSwap},
		{Key: "allow_local_manifest_urls", Kind: adapters.KindBool, Label: "Allow local manifest URLs", ApplyScope: adapters.ScopeHotSwap},
		{Key: "remote_provider_allowed_hosts", Kind: adapters.KindText, Label: "Allowed hosts", ApplyScope: adapters.ScopeHotSwap},
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsAdapterStreamsTemplate_RendersTopLevelFields -v`
Expected: FAIL — template not defined.

- [ ] **Step 3: Create the template (top-level only; provider sub-section + action row in next tasks)**

Create `internal/chassis/templates/settings-adapter-streams.html`:

```html
{{- define "settings-adapter-streams" -}}
<section class="settings-section">
  <h4>Streams catalog <span class="hint">{{ .Hint }}</span></h4>
  {{ range .Fields }}
    {{ if not (isProviderOverrideField .Key) }}
    {{ if isStreamsBytesField .Key }}
      {{ template "adapter-field-row-bytes" (dict
        "Adapter" "streams"
        "Field" .
        "Values" $.Values
      ) }}
    {{ else }}
      {{ template "adapter-field-row" (dict
        "Adapter" "streams"
        "Field" .
        "Values" $.Values
      ) }}
    {{ end }}
    {{ end }}
  {{ end }}

  {{ if .Providers }}
  <h5 class="settings-subhead">Provider overrides</h5>
  {{ range .Providers }}
  <div class="field-row provider-override">
    <label>{{ .DisplayName }} <span class="help">Refresh cadence override; 0 = inherit global.</span></label>
    <input class="field-input num" name="providers.{{ .ID }}.catalog_refresh_hours"
      data-adapter="streams" value="{{ if gt .CatalogRefreshHours 0 }}{{ .CatalogRefreshHours }}{{ end }}">
    <span class="scope hot">HOT</span>
  </div>
  {{ end }}
  {{ end }}

  <div class="field-row action-row">
    <label>Manifest refresh</label>
    <button class="action-btn" data-settings-action="streams-refresh" type="button">↻ Refresh manifest now</button>
    <span class="action-result" id="streams-refresh-result"></span>
  </div>
</section>
{{- end -}}
```

Add the `isProviderOverrideField` and `isStreamsBytesField` template helpers and register both in `templateFuncs`:

```go
func isProviderOverrideField(key string) bool {
	return strings.HasPrefix(key, "providers.")
}

func isStreamsBytesField(key string) bool {
	switch key {
	case "max_manifest_bytes", "max_catalog_bytes":
		return true
	}
	return false
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/chassis -run TestSettingsAdapterStreamsTemplate_RendersTopLevelFields -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/settings-adapters.html internal/chassis/templates/settings-drawer.html internal/chassis/templates/settings-adapter-dlna.html internal/chassis/templates/settings-adapter-torrent.html internal/chassis/templates/settings-adapter-streams.html internal/chassis/templates.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add settings-adapters pane templates"
```

---

## Task 25: Add per-provider rows + refresh action render tests

**Files:**
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestSettingsAdapterStreams_RendersProviderRows(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"streams": {
				"enabled": true,
				"providers": map[string]any{
					"youtube": map[string]any{"catalog_refresh_hours": int64(12)},
					"radio":   map[string]any{},
				},
			},
		},
		fields: map[string][]adapters.FieldDef{
			"streams": {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
		},
	}
	cat := &fakeCatalogManagerProviders{providers: []CatalogProviderState{
		{ID: "youtube", DisplayName: "YouTube"},
		{ID: "radio", DisplayName: "Radio"},
	}}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
		CatalogManager:       cat,
	}}
	html := renderDrawer(t, s)
	for _, want := range []string{
		`<h5 class="settings-subhead">Provider overrides</h5>`,
		`name="providers.youtube.catalog_refresh_hours"`,
		`value="12"`,
		`name="providers.radio.catalog_refresh_hours"`,
		// Radio has no override; the input renders with empty value.
		`>YouTube `, `>Radio `,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered streams provider rows missing %q", want)
		}
	}
}

func TestSettingsAdapterStreams_RendersRefreshAction(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"streams": {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	html := renderDrawer(t, s)
	for _, want := range []string{
		`data-settings-action="streams-refresh"`,
		`↻ Refresh manifest now`,
		`id="streams-refresh-result"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered streams refresh action missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/chassis -run "TestSettingsAdapterStreams_RendersProviderRows|TestSettingsAdapterStreams_RendersRefreshAction" -v`
Expected: PASS (Task 24 already wrote the template; these tests pin the per-provider and action paths against fixture data).

- [ ] **Step 3: (no impl change)**

- [ ] **Step 4: Run the full chassis suite to confirm everything still green**

Run: `go test ./internal/chassis -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/chassis_test.go
git commit -m "test(chassis): pin streams provider rows + refresh action rendering"
```

---

## Task 26: Mockup scope-mismatch test (render-side discipline)

**Files:**
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing tests**

Append a focused test that catches if a future edit silently aligns scopes with the mockup:

```go
// TestMockupScopeMismatches asserts the chassis renders code-declared
// ApplyScope (HOT/RECAST), not the mockup's (REBOOT for several fields).
// The spec's §Goal 6 documents 7 mismatches; each must surface as
// the code-side scope in the rendered HTML.
func TestMockupScopeMismatches(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{
			"torrent": {
				"enabled":                  false,
				"download_dir":             "",
				"startup_buffer_seconds":   int64(10),
				"max_upload_rate_kbps":     int64(512),
				"max_download_rate_kbps":   int64(0),
				"listen_port":              int64(0),
				"max_cache_bytes":          int64(20 << 30),
				"metadata_timeout_seconds": int64(60),
				"keep_completed":           false,
				"traffic_acknowledged":     false,
			},
			"dlna": {"enabled": false, "device_name": "M"},
		},
		fields: map[string][]adapters.FieldDef{
			"torrent": []adapters.FieldDef{
				// All HOT/RECAST scopes are the code-side values, not mockup.
				{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
				{Key: "download_dir", Kind: adapters.KindText, ApplyScope: adapters.ScopeRestartCast},
				{Key: "startup_buffer_seconds", Kind: adapters.KindInt, ApplyScope: adapters.ScopeHotSwap},
				{Key: "max_upload_rate_kbps", Kind: adapters.KindInt, ApplyScope: adapters.ScopeRestartCast},
				{Key: "max_download_rate_kbps", Kind: adapters.KindInt, ApplyScope: adapters.ScopeRestartCast},
				{Key: "listen_port", Kind: adapters.KindInt, ApplyScope: adapters.ScopeRestartCast},
			},
			"dlna": []adapters.FieldDef{
				{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
				{Key: "device_name", Kind: adapters.KindText, ApplyScope: adapters.ScopeRestartBridge},
			},
		},
	}
	s := &Server{cfg: Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
	}}
	html := renderDrawer(t, s)
	// Find the row for each mismatched key and assert the scope badge
	// is the code-side value. Use a substring locator that grabs the
	// field's row chunk to avoid cross-field bleed.
	pairs := []struct {
		key   string
		scope string
	}{
		{"name=\"enabled\"", "hot"},                    // both DLNA + Torrent enabled
		{"name=\"download_dir\"", "recast"},
		{"name=\"startup_buffer_seconds\"", "hot"},
		{"name=\"max_upload_rate_kbps\"", "recast"},
		{"name=\"max_download_rate_kbps\"", "recast"},
		{"name=\"listen_port\"", "recast"},
		{"name=\"device_name\"", "reboot"},
	}
	for _, p := range pairs {
		// Verify the row containing p.key has a scope-{p.scope} class.
		idx := strings.Index(html, p.key)
		if idx < 0 {
			t.Errorf("row for %s missing", p.key)
			continue
		}
		// Search forward 500 chars for the matching scope token.
		window := html[idx : idx+500]
		want := fmt.Sprintf(`scope %s`, p.scope)
		if !strings.Contains(window, want) {
			t.Errorf("row for %s does not contain scope %s; window:\n%s", p.key, p.scope, window)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestMockupScopeMismatches -v`
Expected: PASS — Tasks 22-24 already render code-side scopes.

- [ ] **Step 3: (no impl change)**

- [ ] **Step 4: Final chassis suite run**

Run: `go test ./internal/chassis -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/chassis_test.go
git commit -m "test(chassis): mockup-vs-code scope mismatches surface as code-side scopes"
```

---

## Task 27: Provider disappears between render and POST (race test)

**Files:**
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestAdapterSave_StaleProviderKeyAccepted(t *testing.T) {
	t.Parallel()
	// Spec §Risks: a provider can disappear between drawer render and
	// POST. The handler must accept the dotted key (the wildcard matches)
	// and forward to the saver, which writes the orphaned override into
	// cfg.Providers[id]. The chassis layer is permissive; per-row 404 is
	// wrong.
	saver := &fakeAdapterSettingsSaver{
		current: map[string]map[string]any{"streams": {"enabled": true}},
		fields: map[string][]adapters.FieldDef{
			"streams": {
				{Key: "enabled", Kind: adapters.KindBool},
				{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
			},
		},
		scope: "hot",
	}
	s := newTestServerForAdapterSave(saver)
	rec := postAdapterSave(t, s, "streams", "providers.dead_provider.catalog_refresh_hours=12")
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := saver.touched["streams"]; got["providers.dead_provider.catalog_refresh_hours"] != "12" {
		t.Errorf("touched = %v, want dead_provider override forwarded", got)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/chassis -run TestAdapterSave_StaleProviderKeyAccepted -v`
Expected: PASS — the wildcard-match logic in Task 13 already accepts the key.

- [ ] **Step 3: (no impl change)**

- [ ] **Step 4: Confirm full chassis suite**

Run: `go test -race ./internal/chassis -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings_test.go
git commit -m "test(chassis): stale provider key from drawer race accepted by save handler"
```

---

## Task 28: Port `.settings-section .hint` and add `.settings-subhead` CSS

**Files:**
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/chassis_test.go` (CSS round-trip / embedded asset test)

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestChassisCSS_ContainsSettingsHintRule(t *testing.T) {
	t.Parallel()
	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile chassis.css: %v", err)
	}
	css := string(cssBytes)
	for _, want := range []string{
		`body.receiver .settings-section .hint`,
		`body.receiver .settings-subhead`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("chassis.css missing selector %q", want)
		}
	}
}
```

If a different accessor is used (e.g. the file is embedded via `go:embed`), reach into that variable.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestChassisCSS_ContainsSettingsHintRule -v`
Expected: FAIL — selectors not present.

- [ ] **Step 3: Add the CSS rules**

Append to `internal/chassis/static/chassis.css` (scoped under `body.receiver`):

```css
body.receiver .settings-section .hint {
  font-size: 10px;
  font-weight: 400;
  color: var(--vfd-faded);
  letter-spacing: 0;
  margin-left: 8px;
  text-transform: uppercase;
}

body.receiver .settings-subhead {
  font-size: 11px;
  font-weight: 600;
  color: var(--vfd-faded);
  letter-spacing: 0;
  text-transform: uppercase;
  margin: 18px 0 6px 0;
}
```

If the exact color tokens differ in chassis.css, adjust to match adjacent rules (use the same `--vfd-faded` / `--accent` tokens already in use).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestChassisCSS_ContainsSettingsHintRule -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/chassis.css internal/chassis/chassis_test.go
git commit -m "feat(chassis): add .settings-section .hint + .settings-subhead CSS rules"
```

---

## Task 28a: Narrow bridge JS selectors to `[data-field]`

**Spec ref:** spec §Design Decisions line 116 + §Modified files line 268.

The existing 4A JS in `internal/chassis/static/settings-drawer.js` binds bridge text/number/path input blurs and select changes via selectors like `input.field-input` (no attribute filter). After 4D adds adapter inputs that also match `.field-input`, an adapter blur would fire BOTH the 4D adapter handler AND the 4A bridge handler — the latter would POST a stray request to `/receiver/settings/bridge` with the adapter's form key, surfacing a spurious BAD INPUT chip. Narrowing the bridge selector to `input.field-input[data-field], select.field-input[data-field]` eliminates the overlap.

The existing switch handler already uses `button.switch[data-field]` (see settings-drawer.js around the toggle logic) so switches need no change.

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestSettingsDrawerJS_BridgeSelectorsNarrowedToDataField(t *testing.T) {
	t.Parallel()
	js, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile settings-drawer.js: %v", err)
	}
	src := string(js)
	for _, want := range []string{
		// Narrowed text/number/path selector.
		`input.field-input[data-field]`,
		// Narrowed select selector.
		`select.field-input[data-field]`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("settings-drawer.js missing narrowed selector %q", want)
		}
	}
	// Must NOT contain the un-narrowed selector forms that would catch
	// adapter inputs.
	for _, banned := range []string{
		// Whitespace-flexible substring checks.
		`'input.field-input'`,
		`"input.field-input"`,
		`'select.field-input'`,
		`"select.field-input"`,
	} {
		if strings.Contains(src, banned) {
			t.Errorf("settings-drawer.js still contains un-narrowed selector %q — adapter inputs would double-fire", banned)
		}
	}
}
```

The exact accessor `chassisStaticFS` is the existing embedded-FS variable in `internal/chassis`; verify the name with `grep -nE "embed.FS|embed.S|chassisStatic" internal/chassis/*.go` if uncertain.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_BridgeSelectorsNarrowedToDataField -v`
Expected: FAIL — the un-narrowed selectors still appear in the 4A code.

- [ ] **Step 3: Narrow the selectors in `settings-drawer.js`**

Locate the bridge text/number/path blur handler (search for `field-input` near a blur listener) and edit the selector. The exact line varies based on which 4A revision is in `main`; the pattern is roughly:

```js
// BEFORE (4A):
document.addEventListener('blur', (ev) => {
  const inp = ev.target.closest && ev.target.closest('input.field-input');
  if (!inp) return;
  // ...bridge save logic...
}, true);

// AFTER (4D narrowing):
document.addEventListener('blur', (ev) => {
  const inp = ev.target.closest && ev.target.closest('input.field-input[data-field]');
  if (!inp) return;
  // ...bridge save logic unchanged...
}, true);
```

Do the same narrowing for the select-change handler:

```js
// BEFORE: 'select.field-input'
// AFTER: 'select.field-input[data-field]'
```

Do NOT change the switch handler — it already uses `button.switch[data-field]` (verify; if not, narrow it too).

- [ ] **Step 4: Run the test**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_BridgeSelectorsNarrowedToDataField -v`
Expected: PASS.

- [ ] **Step 5: Smoke-check 4A bridge save still works**

Run: `go test ./internal/chassis -v` (full suite; previous 4A tests that exercise bridge save through the JS-substring-grep helpers should still pass).
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/static/settings-drawer.js internal/chassis/chassis_test.go
git commit -m "fix(chassis): narrow 4A bridge JS selectors to [data-field] so adapter blurs don't double-fire"
```

---

## Task 29: JS handlers for `[data-adapter]` switches and inputs

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`
- Modify: `internal/chassis/chassis_test.go` (light text-grep on the bundled JS)

- [ ] **Step 1: Write the failing assertion test**

Append:

```go
func TestSettingsDrawerJS_HandlesAdapterAttribute(t *testing.T) {
	t.Parallel()
	jsBytes, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile settings-drawer.js: %v", err)
	}
	js := string(jsBytes)
	for _, want := range []string{
		`button.switch[data-adapter]`,
		`input.field-input[data-adapter]`,
		`/receiver/settings/adapter/`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("settings-drawer.js missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_HandlesAdapterAttribute -v`
Expected: FAIL.

- [ ] **Step 3: Add the JS handlers**

In `internal/chassis/static/settings-drawer.js`, alongside the existing `[data-field]` bridge handlers, add (vanilla ES2022, no framework):

```js
// Adapter save handlers — mirror the 4A bridge handlers but POST to
// /receiver/settings/adapter/{adapter} with the adapter name pulled
// from the data-adapter attribute. The two paths never both fire for
// one click because [data-field] and [data-adapter] never coexist on
// the same element.

document.addEventListener('click', (ev) => {
  const sw = ev.target.closest('button.switch[data-adapter]');
  if (!sw) return;
  ev.preventDefault();
  toggleAdapterSwitch(sw);
});

document.addEventListener('blur', (ev) => {
  const inp = ev.target.closest && ev.target.closest('input.field-input[data-adapter]');
  if (!inp) return;
  saveAdapterField(inp);
}, true);

async function toggleAdapterSwitch(btn) {
  const adapter = btn.getAttribute('data-adapter');
  const key = btn.getAttribute('name');
  const wasOn = btn.classList.contains('on');
  // Optimistic toggle.
  btn.classList.toggle('on');
  const body = new URLSearchParams();
  body.set(key, wasOn ? 'false' : 'true');
  try {
    const res = await fetch(`/receiver/settings/adapter/${encodeURIComponent(adapter)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    });
    const payload = await res.json();
    handleAdapterSaveResponse(btn, payload, !wasOn);
  } catch (e) {
    // Revert on network error.
    btn.classList.toggle('on');
    showNotice('NETWORK ERROR', 'err');
  }
}

async function saveAdapterField(inp) {
  const adapter = inp.getAttribute('data-adapter');
  const key = inp.getAttribute('name');
  if (inp.dataset.lastSaved === inp.value) return;
  const body = new URLSearchParams();
  body.set(key, inp.value);
  try {
    const res = await fetch(`/receiver/settings/adapter/${encodeURIComponent(adapter)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    });
    const payload = await res.json();
    handleAdapterSaveResponse(inp, payload, inp.value);
    if (payload.ok) inp.dataset.lastSaved = inp.value;
  } catch (e) {
    showNotice('NETWORK ERROR', 'err');
  }
}

function handleAdapterSaveResponse(target, payload, currentValue) {
  const key = target.getAttribute('name');
  if (payload.ok) {
    if (payload.scope === 'reboot') {
      showNotice(`Restart container to apply new ${target.getAttribute('aria-label') || key}`, 'ok');
    }
    clearFieldError(key);
    return;
  }
  if (payload.errors) {
    const msg = payload.errors[key];
    if (msg) paintFieldError(key, msg);
    return;
  }
  if (payload.chip) {
    showNotice(payload.chip, 'err');
  }
}
```

Reuse 4A's `showNotice(text, variant)`, `paintFieldError(name, msg)`, and `clearFieldError(name)` helpers (declared in `internal/chassis/static/settings-drawer.js` around lines 80, 89, 114). Note: chip-display goes through the same `showNotice` slot used by REBOOT toasts — the 'err' variant styles it differently. The helpers operate on field `name`, not the DOM node, so the adapter handlers thread the `name` attribute through rather than walking from `target` to `.field-row`.

- [ ] **Step 4: Run the test**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_HandlesAdapterAttribute -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/settings-drawer.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): JS handlers for [data-adapter] adapter-section auto-save"
```

---

## Task 30: JS handler for streams-refresh action

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing assertion test**

Append:

```go
func TestSettingsDrawerJS_StreamsRefreshHandler(t *testing.T) {
	t.Parallel()
	jsBytes, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("ReadFile settings-drawer.js: %v", err)
	}
	js := string(jsBytes)
	for _, want := range []string{
		`data-settings-action="streams-refresh"`,
		`/receiver/settings/action/streams-refresh`,
		// Single-flight client-side guard.
		`disabled`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("settings-drawer.js missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_StreamsRefreshHandler -v`
Expected: FAIL.

- [ ] **Step 3: Add the handler**

Append to `internal/chassis/static/settings-drawer.js`:

```js
document.addEventListener('click', async (ev) => {
  const btn = ev.target.closest('button[data-settings-action="streams-refresh"]');
  if (!btn) return;
  ev.preventDefault();
  if (btn.disabled) return;
  btn.disabled = true;
  const slot = document.getElementById('streams-refresh-result');
  if (slot) {
    slot.textContent = '…';
    slot.classList.remove('ok', 'err', 'shown');
    slot.classList.add('shown');
  }
  try {
    const res = await fetch('/receiver/settings/action/streams-refresh', { method: 'POST' });
    const payload = await res.json();
    if (slot) {
      if (payload.ok) {
        slot.textContent = payload.summary || 'Refreshed';
        slot.classList.add('ok');
      } else if (payload.chip) {
        // BUSY / NOT READY chip — surface via the notice slot and clear
        // the action result.
        showNotice(payload.chip, 'err');
        slot.textContent = '';
        slot.classList.remove('shown');
      } else if (payload.error) {
        slot.textContent = payload.error;
        slot.classList.add('err');
      }
    }
  } catch (e) {
    if (slot) {
      slot.textContent = 'Network error';
      slot.classList.add('err');
    }
  } finally {
    btn.disabled = false;
  }
});
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_StreamsRefreshHandler -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/settings-drawer.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): JS handler for streams-refresh action (single-flight)"
```

---

## Task 31: Verify chassis forbidden-imports test still passes

**Files:**
- (read-only check) `internal/chassis/import_check_test.go`

- [ ] **Step 1: Run the existing forbidden-imports check**

Run: `go test ./internal/chassis -run "TestProductionImports_NoCrossPackageCoupling|TestChassisForbiddenImports_IncludesMisterctl" -v`
Expected: PASS — 4D adds no new chassis-forbidden imports.

- [ ] **Step 2: If it fails — audit any accidental import**

If a chassis file accidentally pulled `internal/uiserver`, `internal/misterctl`, or a concrete adapter package, remove the import and route through one of the chassis-owned interfaces from Tasks 9-10 instead.

- [ ] **Step 3: Re-run**

Run: `go test ./internal/chassis -v`
Expected: all PASS.

- [ ] **Step 4: (no commit unless an audit edit was made)**

If you needed to fix an accidental import:

```bash
git add <fixed-file>
git commit -m "fix(chassis): preserve forbidden-imports posture for 4D additions"
```

- [ ] **Step 5: Done**

---

## Task 32: Production wrapper — `adapter_settings_saver.go`

**Files:**
- Create: `cmd/mister-groovy-relay/adapter_settings_saver.go`
- Create: `cmd/mister-groovy-relay/adapter_settings_saver_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/adapter_settings_saver_test.go`:

```go
package main

import (
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func TestBridgeAdapterSettingsSaver_Current_UnknownReturnsFalse(t *testing.T) {
	t.Parallel()
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/config.toml", mu)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	_, ok := wrapper.Current("unknown")
	if ok {
		t.Errorf("Current(unknown) returned ok=true; want false")
	}
}

type fakeRegistry struct {
	entries map[string]adapters.Adapter
}

func (f *fakeRegistry) Get(name string) (adapters.Adapter, bool) {
	a, ok := f.entries[name]
	return a, ok
}
```

The wrapper accepts the narrow `adapterLookup` interface, so tests can use `fakeRegistry` directly while production passes `*adapters.Registry`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeAdapterSettingsSaver_Current_UnknownReturnsFalse -v`
Expected: FAIL — wrapper not defined.

- [ ] **Step 3: Implement the wrapper**

Per spec §287, the saver-failure error shims live in `cmd/mister-groovy-relay/` (not in `internal/chassis`). The chassis-side handler unwraps them via duck-typed structural interfaces declared at file scope in `internal/chassis/settings.go` — those interfaces are named types (required by `errors.As`).

Create `cmd/mister-groovy-relay/adapter_settings_saver.go`:

```go
package main

import (
	"errors"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// adapterLookup is the minimal capability the wrapper needs from the
// adapter registry: name -> Adapter. Production passes *adapters.Registry;
// tests pass a small fake.
type adapterLookup interface {
	Get(name string) (adapters.Adapter, bool)
}

// bridgeAdapterSettingsSaver implements chassis.AdapterSettingsSaver
// over a *uiserver.AdapterSaver and the registry. The chassis package
// does not import either of these directly (forbidden-imports check).
type bridgeAdapterSettingsSaver struct {
	saver *uiserver.AdapterSaver
	reg   adapterLookup
}

func newBridgeAdapterSettingsSaver(saver *uiserver.AdapterSaver, reg adapterLookup) *bridgeAdapterSettingsSaver {
	return &bridgeAdapterSettingsSaver{saver: saver, reg: reg}
}

func (b *bridgeAdapterSettingsSaver) Current(name string) (map[string]any, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	type currentValuer interface {
		CurrentValues() map[string]any
	}
	cv, ok := a.(currentValuer)
	if !ok {
		return nil, false
	}
	return cv.CurrentValues(), true
}

// Fields returns the 4D writable surface for this adapter. For streams,
// per-provider `disabled` and `hls_buffer_disabled` are owned by 4C's
// Catalog pane and projected out so a POST to /receiver/settings/adapter/streams
// that touches them is rejected by the chassis as BAD INPUT.
func (b *bridgeAdapterSettingsSaver) Fields(name string) ([]adapters.FieldDef, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	return projectWritableSurface(name, a.Fields()), true
}

// projectWritableSurface filters adapter.Fields() to the 4D-owned subset.
// Per-adapter rules:
//   - dlna, torrent: return as-is.
//   - streams: keep the 15 top-level fields, drop concrete provider rows, and
//     append one wildcard providers.*.catalog_refresh_hours allowlist entry.
func projectWritableSurface(name string, fields []adapters.FieldDef) []adapters.FieldDef {
	if name != "streams" {
		return fields
	}
	out := fields[:0:0] // fresh backing array; do not alias the streams adapter's slice
	for _, fd := range fields {
		if strings.HasPrefix(fd.Key, "providers.") {
			continue
		}
		out = append(out, fd)
	}
	out = append(out, adapters.FieldDef{
		Key:        "providers.*.catalog_refresh_hours",
		Label:      "Catalog Refresh Hours",
		Kind:       adapters.KindInt,
		Default:    int64(0),
		ApplyScope: adapters.ScopeHotSwap,
	})
	return out
}

func isCatalogOwnedStreamsKey(key string) bool {
	if !strings.HasPrefix(key, "providers.") {
		return false
	}
	return strings.HasSuffix(key, ".disabled") || strings.HasSuffix(key, ".hls_buffer_disabled")
}

func (b *bridgeAdapterSettingsSaver) SaveTouched(name string, touched map[string]string) (string, error) {
	a, ok := b.reg.Get(name)
	if !ok {
		return "", &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	fields := projectWritableSurface(name, a.Fields())
	scope, err := b.saver.SaveTouched(name, touched, a, fields)
	if err != nil {
		return "", translateSaverError(err)
	}
	label, labelOK := chassis.WireScopeLabel(scope) // exported helper added in Task 11
	if !labelOK {
		return "", &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return label, nil
}

func translateSaverError(err error) error {
	// uiserver returns *uiserver.adapterFieldErrors as a typed error
	// exposing FieldErrors() []adapters.FieldError. Unwrap via errors.As
	// against the named local interface.
	var feb fieldErrorBearer
	if errors.As(err, &feb) {
		return &cmdAdapterFieldErrors{errs: feb.FieldErrors()}
	}
	return &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
}

// fieldErrorBearer is the local-named interface errors.As unwraps
// against. *uiserver.adapterFieldErrors satisfies this structurally.
type fieldErrorBearer interface {
	error
	FieldErrors() []adapters.FieldError
}

// cmdChipError is the cmd-side typed error implementing chassis's
// structural settingsChipError contract (StatusCode() + Chip()).
type cmdChipError struct {
	status int
	chip   string
}

func (e *cmdChipError) Error() string     { return e.chip }
func (e *cmdChipError) StatusCode() int   { return e.status }
func (e *cmdChipError) Chip() string      { return e.chip }

// cmdAdapterFieldErrors is the cmd-side typed error carrying per-field
// errors. The chassis handler unwraps it via the named interface
// fieldErrorBearerForChassis declared in internal/chassis/settings.go.
type cmdAdapterFieldErrors struct {
	errs []adapters.FieldError
}

func (e *cmdAdapterFieldErrors) Error() string { return "adapter field errors" }
func (e *cmdAdapterFieldErrors) FieldErrors() []adapters.FieldError { return e.errs }
```

Make sure `strings` is imported (used in `isCatalogOwnedStreamsKey`).

In `internal/chassis/settings.go` (chassis side — does NOT import uiserver or cmd), `fieldErrorBearerForChassis` and `emitSaveError` should already exist from Task 13. Add only the exported wire-scope-label helper here; if Task 13 was implemented differently, reconcile it to the shape below rather than duplicating declarations:

```go
// WireScopeLabel exports the wire-label form of scopeLabel for
// production wrappers that need to translate adapters.ApplyScope into
// the JSON-envelope "hot"/"next"/"recast"/"reboot" string. Returns
// (label, true) for known scopes, ("", false) otherwise.
func WireScopeLabel(s adapters.ApplyScope) (string, bool) {
	return scopeLabel(s)
}
```

Update `handleSettingsAdapterPost`'s `emitSaveError` (from Task 13) to use the named interface for `errors.As`:

```go
func emitSaveError(w http.ResponseWriter, err error) {
	var chipErr settingsChipError
	if errors.As(err, &chipErr) {
		writeSettingsChip(w, chipErr.StatusCode(), chipErr.Chip())
		return
	}
	var feb fieldErrorBearerForChassis
	if errors.As(err, &feb) {
		ferrs := map[string]string{}
		for _, fe := range feb.FieldErrors() {
			ferrs[fe.Key] = fe.Msg
		}
		writeSettingsFieldErrors(w, http.StatusBadRequest, ferrs)
		return
	}
	writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
}
```

**Task 14's test shims stay in `internal/chassis/settings_test.go`** (they can't import the `cmd` package — that's both forbidden by Go's package rules for test files and would create a cycle). Both layers have their own typed errors satisfying the chassis-side structural interfaces:

| Layer | Type | Lives in | Purpose |
|---|---|---|---|
| Production | `*cmdChipError` | `cmd/mister-groovy-relay/adapter_settings_saver.go` | Real save failures from the wrapper |
| Production | `*cmdAdapterFieldErrors` | `cmd/mister-groovy-relay/adapter_settings_saver.go` | Real per-field errors from the wrapper |
| Test (chassis) | `*chassisChipError` (or rename to `*testChipError`) | `internal/chassis/settings_test.go` | Inject chip errors into the chassis handler test |
| Test (chassis) | `*chassisAdapterFieldErrors` (or rename to `*testAdapterFieldErrors`) | `internal/chassis/settings_test.go` | Inject field errors into the chassis handler test |

All four implement the same two structural interfaces (`settingsChipError`, `fieldErrorBearerForChassis`). The chassis handler's `emitSaveError` (Task 13) unwraps via `errors.As` against the named interfaces — which side the concrete type comes from is invisible to the handler. Optional polish: rename Task 14's `chassisChipError`/`chassisAdapterFieldErrors` to `testChipError`/`testAdapterFieldErrors` to make the test-only intent obvious.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeAdapterSettingsSaver_Current_UnknownReturnsFalse -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_settings_saver.go cmd/mister-groovy-relay/adapter_settings_saver_test.go internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(cmd): bridgeAdapterSettingsSaver wraps uiserver.AdapterSaver for chassis"
```

---

## Task 33: Production wrapper — `streams_refresher.go`

**Files:**
- Create: `cmd/mister-groovy-relay/streams_refresher.go`
- Create: `cmd/mister-groovy-relay/streams_refresher_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/streams_refresher_test.go`:

```go
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
)

func TestBridgeStreamsRefresher_NilAdapter(t *testing.T) {
	t.Parallel()
	r := newBridgeStreamsRefresher(nil)
	_, err := r.RefreshNow(context.Background())
	if err == nil {
		t.Errorf("RefreshNow(nil adapter) err = nil, want non-nil")
	}
}

func TestBridgeStreamsRefresher_DelegatesToManifestPath(t *testing.T) {
	t.Parallel()
	// Pin that the wrapper calls RefreshNow(ctx, "") — the manifest path,
	// not a per-provider path.
	fake := &fakeManifestRefresher{status: streams.RefreshStatus{Source: "remote", FetchedAt: time.Now()}}
	r := newBridgeStreamsRefresher(fake)
	result, err := r.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("RefreshNow err = %v", err)
	}
	if fake.lastProviderID != "" {
		t.Errorf("providerID = %q, want empty manifest-path sentinel", fake.lastProviderID)
	}
	if result.Source != "remote" {
		t.Errorf("Source = %q, want 'remote'", result.Source)
	}
}

func TestBridgeStreamsRefresher_PropagatesErr(t *testing.T) {
	t.Parallel()
	fake := &fakeManifestRefresher{
		status: streams.RefreshStatus{Source: "remote", Err: errors.New("dns: no such host")},
	}
	r := newBridgeStreamsRefresher(fake)
	result, _ := r.RefreshNow(context.Background())
	if result.Err == nil || result.Err.Error() != "dns: no such host" {
		t.Errorf("result.Err = %v, want 'dns: no such host'", result.Err)
	}
}

type fakeManifestRefresher struct {
	status         streams.RefreshStatus
	lastProviderID string
}

func (f *fakeManifestRefresher) RefreshNow(ctx context.Context, providerID string) streams.RefreshStatus {
	f.lastProviderID = providerID
	return f.status
}
```

The wrapper takes the narrow `streamsManifestRefresher` interface, so no new streams package test seam is required.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeStreamsRefresher -v`
Expected: FAIL — wrapper not defined.

- [ ] **Step 3: Implement the wrapper**

Create `cmd/mister-groovy-relay/streams_refresher.go`:

```go
package main

import (
	"context"
	"errors"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

// streamsManifestRefresher is the narrow capability the wrapper needs.
// Production passes *streams.Adapter; tests pass an inline fake.
type streamsManifestRefresher interface {
	RefreshNow(ctx context.Context, providerID string) streams.RefreshStatus
}

type bridgeStreamsRefresher struct {
	adapter streamsManifestRefresher
}

func newBridgeStreamsRefresher(a streamsManifestRefresher) *bridgeStreamsRefresher {
	return &bridgeStreamsRefresher{adapter: a}
}

func (b *bridgeStreamsRefresher) RefreshNow(ctx context.Context) (chassis.StreamsRefreshResult, error) {
	if b.adapter == nil {
		return chassis.StreamsRefreshResult{}, errors.New("streams adapter not registered")
	}
	start := time.Now()
	status := b.adapter.RefreshNow(ctx, "") // manifest path — providerID "" is the canonical entry point
	return chassis.StreamsRefreshResult{
		Source:     status.Source,
		DurationMS: time.Since(start).Milliseconds(),
		Err:        status.Err,
	}, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeStreamsRefresher -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/streams_refresher.go cmd/mister-groovy-relay/streams_refresher_test.go
git commit -m "feat(cmd): bridgeStreamsRefresher wraps streams.Adapter.RefreshNow(\"\") for chassis"
```

---

## Task 34: Wire wrappers into `main.go`

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Locate the chassis.Config construction**

Open `cmd/mister-groovy-relay/main.go` and find where `chassis.Config{...}` is constructed (search for `CoreLauncher:` to anchor on the 4B field).

- [ ] **Step 2: Add the wrapper construction**

Right before the `chassis.Config{...}` literal, instantiate the two wrappers (the necessary inputs — `*uiserver.AdapterSaver`, the registry, the `*streams.Adapter` — are already available in `main.go` from 4A/4B/4C):

```go
adapterSaverWrapper := newBridgeAdapterSettingsSaver(adapterSaver, registry)
streamsRefresherWrapper := newBridgeStreamsRefresher(streamsAdapter)
```

(`adapterSaver`, `registry`, `streamsAdapter` — use the actual variable names main.go already uses; if they're constructed slightly differently, adjust the wiring.)

Add the two new fields to the `chassis.Config{...}` literal:

```go
chassis.Config{
    // ... existing fields ...
    AdapterSettingsSaver: adapterSaverWrapper,
    StreamsRefresher:     streamsRefresherWrapper,
}
```

If `streamsAdapter` may be nil (when streams is unregistered), set the wrapper to nil in that case:

```go
var streamsRefresherWrapper *bridgeStreamsRefresher
if streamsAdapter != nil {
    streamsRefresherWrapper = newBridgeStreamsRefresher(streamsAdapter)
}
```

Same defensive guard for `adapterSaverWrapper` if the saver is conditionally constructed.

- [ ] **Step 3: Build to verify wiring compiles**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Smoke-test by starting the bridge against a temp config**

Run the bridge with a minimal `config.toml` (or use whatever local smoke pattern the project established for 4A/4B). Open the chassis settings drawer; click the Adapters tab; verify the DLNA, Torrent, Streams sections render with current values; verify a single field save round-trips (modify and reload).

If anything fails at this step, the most likely cause is a Step-2 wiring oversight (wrong field name, missing nil guard); fix and re-test.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(cmd): wire bridgeAdapterSettingsSaver + bridgeStreamsRefresher into chassis"
```

---

## Task 35: End-to-end test — DLNA save round-trips disk + adapter state

**Decision:** Tasks 35-38 in earlier revisions lived in `tests/integration/` and depended on `dlna.NewAdapter` / `torrent.NewAdapter` / `streams.NewAdapter` constructors that have heavy dependency trees (bittorrent client, manifest fetcher, yt-dlp resolver, etc.). The replacement strategy is to put end-to-end tests in `cmd/mister-groovy-relay/` where the wrappers live, drive them through the chassis Server directly using the same construction `main.go` uses, and rely on each adapter's existing test seams.

DLNA is the simplest of the three: 4 string-or-bool fields, `Validate()` doesn't need `bridge.DataDir`, and the constructor is parameter-light. Worth a real end-to-end test.

**Files:**
- Create: `cmd/mister-groovy-relay/adapter_settings_e2e_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/adapter_settings_e2e_test.go`:

```go
//go:build integration
// +build integration

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/dlna"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func TestE2E_DLNA_SaveEnabledToggle(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := `[bridge]
mister.host = "127.0.0.1"
data_dir = "` + dir + `"

[adapters.dlna]
enabled = false
device_name = "MiSTer"
autoplay_on_set_uri = false
allow_public_source_urls = false
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Step A: Construct the DLNA adapter the way main.go does. The exact
	// `dlna.NewAdapter(...)` signature must be read from internal/adapters/dlna/adapter.go
	// at the start of this task and the constructor args filled in here.
	a := dlna.NewAdapter(/* args per real constructor */)

	// Step B: Decode the on-disk [adapters.dlna] section to seed in-memory state.
	// The exact entry point depends on the config package; read internal/config/sectioned.go
	// and use whatever main.go uses to round-trip a section into the adapter's
	// DecodeConfig method.
	if err := decodeAdapterSectionFromFile(t, cfgPath, "dlna", a); err != nil {
		t.Fatalf("decodeAdapterSection: %v", err)
	}

	// Step C: Wire the chassis Server with just this one adapter registered.
	mu := &sync.Mutex{}
	adapterSaver := uiserver.NewAdapterSaver(cfgPath, mu)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"dlna": a}}
	wrapper := newBridgeAdapterSettingsSaver(adapterSaver, reg)

	srv, err := chassis.New(chassis.Config{
		Version:              "test",
		AdapterSettingsSaver: wrapper,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Step D: POST the enabled toggle.
	res, err := http.Post(ts.URL+"/receiver/settings/adapter/dlna",
		"application/x-www-form-urlencoded",
		strings.NewReader("enabled=true"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(res.Body)
		t.Fatalf("Status = %d; body = %s", res.StatusCode, out)
	}
	var payload map[string]any
	_ = json.NewDecoder(res.Body).Decode(&payload)
	if scope, _ := payload["scope"].(string); scope != "hot" {
		t.Errorf("scope = %q, want hot", scope)
	}

	// Disk side.
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `enabled = true`) {
		t.Errorf("config.toml missing enabled = true:\n%s", got)
	}
	// In-memory side.
	cv, ok := a.(interface{ CurrentValues() map[string]any })
	if !ok {
		t.Fatalf("dlna adapter does not expose CurrentValues")
	}
	if v, _ := cv.CurrentValues()["enabled"].(bool); !v {
		t.Errorf("CurrentValues().enabled = false; want true")
	}
}

// decodeAdapterSectionFromFile is a test helper to seed adapter state
// from a config file. The exact path depends on the production seam;
// either call config.LoadSectioned (or its equivalent) and pass the
// adapters[name] primitive into adapter.DecodeConfig, or build the
// equivalent flow by reading the section bytes and using
// adapter.DecodeConfig + meta directly. Read internal/config/sectioned.go
// at task start.
func decodeAdapterSectionFromFile(t *testing.T, path, name string, a adapters.Adapter) error {
	t.Helper()
	// Implementation: see config.LoadSectioned in internal/config/sectioned.go.
	// Returns the toml.Primitive + MetaData for adapters[name]; pass into
	// a.DecodeConfig(prim, meta).
	return nil
}
```

The two task-time substitutions (Step A's `dlna.NewAdapter(...)` constructor args, Step B's `decodeAdapterSectionFromFile` implementation) are reads against existing production code, not invented helpers. Resolve them at task start by reading `internal/adapters/dlna/adapter.go` and `internal/config/sectioned.go`.

- [ ] **Step 2: Run with the integration build tag**

Run: `go test -tags=integration ./cmd/mister-groovy-relay -run TestE2E_DLNA_SaveEnabledToggle -v`
Expected: FAIL initially due to placeholder constructor / helper bodies.

- [ ] **Step 3: Resolve constructor args and helper body**

Read the real `dlna.NewAdapter` signature (likely takes a `core.Manager` reference or similar — confirm by reading `main.go`'s DLNA construction) and the real config-section loading path. Fill in Step A and Step B literally.

- [ ] **Step 4: Verify PASS**

Run: `go test -tags=integration ./cmd/mister-groovy-relay -run TestE2E_DLNA_SaveEnabledToggle -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_settings_e2e_test.go
git commit -m "test(e2e): DLNA enabled toggle round-trips through chassis save"
```

---

## Task 36: Wrapper-level test — Torrent RECAST scope

**Decision:** Torrent has a heavy `torrent.NewAdapter` dependency tree (bittorrent client, peer manager) that takes significant setup. An end-to-end test along Task 35's pattern would either need a full smoke harness or a refactor of the torrent adapter's constructor. Both are out of 4D's scope. Instead, prove the same property — `download_dir` save dispatches RECAST scope — at the wrapper boundary using a minimal fake adapter that satisfies the writable-surface allowlist and reports `ScopeRestartCast` from `ApplyConfig`.

This covers the same behavior the spec's integration test calls for (RECAST scope on `download_dir` change) without depending on the production torrent constructor.

**Files:**
- Modify: `cmd/mister-groovy-relay/adapter_settings_saver_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestBridgeAdapterSettingsSaver_TorrentDownloadDirReportsRecast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"
data_dir = "`+dir+`"

[adapters.torrent]
enabled = false
download_dir = ""
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(cfgPath, mu)

	// Minimal fake torrent adapter — Fields(), CurrentValues(), Validate (no-op),
	// ApplyConfig returns ScopeRestartCast for download_dir touch.
	fake := &fakeTorrentAdapter{
		current: map[string]any{"enabled": false, "download_dir": ""},
	}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"torrent": fake}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)

	scope, err := wrapper.SaveTouched("torrent", map[string]string{"download_dir": "/srv/torrents"})
	if err != nil {
		t.Fatalf("SaveTouched: %v", err)
	}
	if scope != "recast" {
		t.Errorf("scope = %q, want recast", scope)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `download_dir = "/srv/torrents"`) {
		t.Errorf("config.toml missing download_dir:\n%s", got)
	}
}

// fakeTorrentAdapter is a minimal adapters.Adapter implementation for the
// torrent wrapper test. Implements all required methods of the interface
// at internal/adapters/adapter.go — keep this fake in sync if the
// adapters.Adapter contract evolves.
type fakeTorrentAdapter struct {
	current map[string]any
}

func (f *fakeTorrentAdapter) Name() string                   { return "torrent" }
func (f *fakeTorrentAdapter) DisplayName() string            { return "Torrent" }
func (f *fakeTorrentAdapter) Status() adapters.Status        { return adapters.Status{} }
func (f *fakeTorrentAdapter) IsEnabled() bool                { return false }
func (f *fakeTorrentAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "download_dir", Kind: adapters.KindText, ApplyScope: adapters.ScopeRestartCast},
	}
}
func (f *fakeTorrentAdapter) CurrentValues() map[string]any {
	out := make(map[string]any, len(f.current))
	for k, v := range f.current {
		out[k] = v
	}
	return out
}
func (f *fakeTorrentAdapter) DecodeConfig(prim toml.Primitive, meta toml.MetaData) error { return nil }
func (f *fakeTorrentAdapter) Validate(prim toml.Primitive, meta toml.MetaData) error     { return nil }
func (f *fakeTorrentAdapter) ApplyConfig(prim toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	// download_dir change implies RECAST per the torrent scope table.
	return adapters.ScopeRestartCast, nil
}
func (f *fakeTorrentAdapter) Start(ctx context.Context) error { return nil }
func (f *fakeTorrentAdapter) Stop() error                     { return nil }
```

The fake's method set is the full `adapters.Adapter` interface. If `adapters.Adapter` declares additional methods at execution time, extend the fake with the same shape. Plain no-ops are fine.

- [ ] **Step 2: Run test**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeAdapterSettingsSaver_TorrentDownloadDirReportsRecast -v`
Expected: PASS — the wrapper + saver path is exercised, scope translates "recast" via the chassis scope-label table.

- [ ] **Step 3: (no impl change unless a real bug surfaces)**

- [ ] **Step 4: Confirm**

Run: `go test ./cmd/mister-groovy-relay -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_settings_saver_test.go
git commit -m "test(cmd): torrent download_dir touch reports recast at wrapper boundary"
```

---

## Task 37: Wrapper-level test — Streams top-level + per-provider + projection

**Decision:** Streams has the same "heavy constructor" problem as Torrent. Same approach: prove the same behaviors at the wrapper boundary with a minimal fake adapter, plus include the writable-surface projection test that was the missing spec coverage from Task 14a.

**Files:**
- Modify: `cmd/mister-groovy-relay/adapter_settings_saver_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestBridgeAdapterSettingsSaver_StreamsTopLevelSave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"
data_dir = "`+dir+`"

[adapters.streams]
enabled = true
manifest_refresh_hours = 24
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(cfgPath, mu)
	fake := &fakeStreamsAdapter{
		current: map[string]any{
			"enabled":                true,
			"manifest_refresh_hours": int64(24),
		},
	}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"streams": fake}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)

	if _, err := wrapper.SaveTouched("streams", map[string]string{"manifest_refresh_hours": "12"}); err != nil {
		t.Fatalf("SaveTouched: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `manifest_refresh_hours = 12`) {
		t.Errorf("config.toml missing top-level edit:\n%s", got)
	}
}

func TestBridgeAdapterSettingsSaver_StreamsPerProviderSave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"
data_dir = "`+dir+`"

[adapters.streams]
enabled = true

[adapters.streams.providers.youtube]
catalog_refresh_hours = 6
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(cfgPath, mu)
	fake := &fakeStreamsAdapter{
		current: map[string]any{
			"enabled": true,
			"providers": map[string]any{
				"youtube": map[string]any{"catalog_refresh_hours": int64(6)},
			},
		},
	}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"streams": fake}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	if _, err := wrapper.SaveTouched("streams", map[string]string{"providers.youtube.catalog_refresh_hours": "24"}); err != nil {
		t.Fatalf("SaveTouched: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `[providers.youtube]`) && !strings.Contains(string(got), `[adapters.streams.providers.youtube]`) {
		t.Errorf("config.toml missing provider subtable:\n%s", got)
	}
	if !strings.Contains(string(got), `catalog_refresh_hours = 24`) {
		t.Errorf("config.toml missing per-provider edit:\n%s", got)
	}
}

func TestBridgeAdapterSettingsSaver_StreamsProjectionHidesCatalogKeys(t *testing.T) {
	t.Parallel()
	fake := &fakeStreamsAdapter{}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"streams": fake}}
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/cfg.toml", &sync.Mutex{})
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	fields, ok := wrapper.Fields("streams")
	if !ok {
		t.Fatalf("Fields(streams) returned ok=false")
	}
	for _, fd := range fields {
		if isCatalogOwnedStreamsKey(fd.Key) {
			t.Errorf("Fields(streams) leaks Catalog-owned key %q", fd.Key)
		}
	}
	if !containsFieldKey(fields, "providers.*.catalog_refresh_hours") {
		t.Errorf("Fields(streams) missing wildcard catalog_refresh_hours allowlist: %#v", fields)
	}
}

func containsFieldKey(fields []adapters.FieldDef, key string) bool {
	for _, fd := range fields {
		if fd.Key == key {
			return true
		}
	}
	return false
}

type fakeStreamsAdapter struct {
	current map[string]any
}

func (f *fakeStreamsAdapter) Name() string                   { return "streams" }
func (f *fakeStreamsAdapter) DisplayName() string            { return "Streams" }
func (f *fakeStreamsAdapter) Status() adapters.Status        { return adapters.Status{} }
func (f *fakeStreamsAdapter) IsEnabled() bool                { return true }
func (f *fakeStreamsAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_refresh_hours", Kind: adapters.KindInt, ApplyScope: adapters.ScopeHotSwap},
		// Catalog-owned (must be projected out by the wrapper).
		{Key: "providers.youtube.disabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "providers.youtube.hls_buffer_disabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		// 4D-owned per-provider override.
		{Key: "providers.youtube.catalog_refresh_hours", Kind: adapters.KindInt, ApplyScope: adapters.ScopeHotSwap},
	}
}
func (f *fakeStreamsAdapter) CurrentValues() map[string]any {
	if f.current == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(f.current))
	for k, v := range f.current {
		out[k] = v
	}
	return out
}
func (f *fakeStreamsAdapter) DecodeConfig(prim toml.Primitive, meta toml.MetaData) error { return nil }
func (f *fakeStreamsAdapter) Validate(prim toml.Primitive, meta toml.MetaData) error     { return nil }
func (f *fakeStreamsAdapter) ApplyConfig(prim toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeStreamsAdapter) Start(ctx context.Context) error { return nil }
func (f *fakeStreamsAdapter) Stop() error                     { return nil }
```

- [ ] **Step 2: Run tests**

Run: `go test ./cmd/mister-groovy-relay -run "TestBridgeAdapterSettingsSaver_Streams" -v`
Expected: PASS.

- [ ] **Step 3: (no impl change)**

- [ ] **Step 4: Confirm**

Run: `go test ./cmd/mister-groovy-relay -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_settings_saver_test.go
git commit -m "test(cmd): streams top-level + per-provider save + Catalog-key projection"
```

---

## Task 38: Wrapper-level test — Streams refresh action

**Files:**
- Modify: `cmd/mister-groovy-relay/streams_refresher_test.go` (extend the Task 33 file)

- [ ] **Step 1: Write the failing test**

Task 33 already lays out the wrapper test scaffold with a `streamsManifestRefresher` interface and an inline fake. Extend with the end-to-end chassis Server round-trip:

```go
func TestE2E_StreamsRefreshThroughChassis(t *testing.T) {
	t.Parallel()
	// Pin that the wrapper called RefreshNow(ctx, "") — manifest path —
	// and that the chassis Server's JSON envelope matches the spec.
	fake := &fakeManifestRefresherForChassis{
		status: streams.RefreshStatus{Source: "remote"},
	}
	refresher := newBridgeStreamsRefresher(fake)

	srv, err := chassis.New(chassis.Config{
		Version:          "test",
		StreamsRefresher: refresher,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/receiver/settings/action/streams-refresh", "", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("Status = %d; body = %s", res.StatusCode, body)
	}
	var payload map[string]any
	_ = json.NewDecoder(res.Body).Decode(&payload)
	if okv, _ := payload["ok"].(bool); !okv {
		t.Errorf("payload.ok = %v, want true", payload["ok"])
	}
	if src, _ := payload["source"].(string); src != "remote" {
		t.Errorf("payload.source = %q, want 'remote'", src)
	}
	if fake.lastProviderID != "" {
		t.Errorf("wrapper called RefreshNow with providerID = %q, want \"\" (manifest path)", fake.lastProviderID)
	}
}

type fakeManifestRefresherForChassis struct {
	status         streams.RefreshStatus
	lastProviderID string
}

func (f *fakeManifestRefresherForChassis) RefreshNow(ctx context.Context, providerID string) streams.RefreshStatus {
	f.lastProviderID = providerID
	return f.status
}
```

- [ ] **Step 2: Run test**

Run: `go test ./cmd/mister-groovy-relay -run TestE2E_StreamsRefreshThroughChassis -v`
Expected: PASS.

- [ ] **Step 3: (no impl change unless a real bug surfaces)**

- [ ] **Step 4: Confirm**

Run: `go test ./cmd/mister-groovy-relay -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/streams_refresher_test.go
git commit -m "test(cmd): streams refresh through chassis Server with manifest-path assertion"
```

---

## Task 39: Final sweep — vet + race + integration + lint

**Files:**
- (no files; CI-style sweep)

- [ ] **Step 1: Run `go vet ./...`**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 2: Run `go test -race ./...`**

Run: `go test -race ./...`
Expected: all PASS, no race warnings.

- [ ] **Step 3: Run integration tests**

Run: `go test -tags=integration ./cmd/mister-groovy-relay -run TestE2E_ -v`
Expected: all PASS. Requires the same local binaries as the cmd package's existing integration fixtures.

- [ ] **Step 4: Verify the chassis forbidden-imports test still passes**

Run: `go test ./internal/chassis -run "TestProductionImports_NoCrossPackageCoupling|TestChassisForbiddenImports_IncludesMisterctl" -v`
Expected: PASS.

- [ ] **Step 5: Smoke-test the live drawer**

Build the bridge, run it against a non-trivial `config.toml` (DLNA + Torrent + Streams enabled), and click through the Adapters tab:
- DLNA `enabled` toggle: should auto-save HOT.
- DLNA `device_name` text: should save REBOOT and surface the restart-container toast.
- Torrent `download_dir`: should save RECAST.
- Streams `manifest_refresh_hours`: should save HOT.
- Streams per-provider `catalog_refresh_hours`: should save HOT.
- Streams refresh button: click once → see `Manifest refreshed from <source> in Nms` in the result slot. Click twice in rapid succession → second click should immediately surface a BUSY chip (or 409 from the network panel).

If anything is off, fix it in the relevant earlier task's scope (not in this sweep — sweep is for verification).

- [ ] **Step 6: Final commit (if anything changed in steps 1–5)**

```bash
git add -A
git commit -m "chore: final 4D sweep — vet/race/integration clean"
```

If no edits, no commit needed.

---

## Done

All 39 tasks complete. The Phase 4D Adapters pane is now functional for DLNA, Torrent, and Streams with the per-field auto-save discipline 4A/4B/4C established. Plex / Jellyfin / URL stubs await 4E / 4F.
