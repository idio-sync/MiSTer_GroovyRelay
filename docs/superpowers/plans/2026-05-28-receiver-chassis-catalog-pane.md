# Receiver Chassis Catalog Pane — Phase 4C Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the "Spec 4C — implementation in progress" stub in the receiver chassis settings drawer with a functional Catalog pane (provider rows + enabled toggles + global HLS-override switch) and add a Reset-to-defaults action to the Advanced pane's new Diagnostics section, end to end.

**Architecture:** All chassis additions go through two new narrow chassis-owned interfaces (`CatalogSettingsManager`, `ConfigReset`) satisfied by production wrappers in `cmd/mister-groovy-relay/`. The streams adapter gains three small public methods (`ConfigSnapshot`, `ApplyConfigValue`, `StopActiveCast`) and extends its existing `Catalog()` to populate two new fields on `adapters.CatalogProvider`. `internal/config` exports one new helper (`DefaultConfigTOML`). The chassis stays isolated — zero new forbidden imports.

**Tech Stack:** Go 1.26 (standard library + BurntSushi/toml + html/template), embedded plain ES2022 client JS, CSS scoped under `body.receiver`. Tests via `go test`, `go test -race`, and `go test -tags=integration`.

**Spec reference:** [docs/superpowers/specs/2026-05-28-receiver-chassis-catalog-pane-design.md](../specs/2026-05-28-receiver-chassis-catalog-pane-design.md). Every task below cites the spec section that contains the design rationale; copy code verbatim from there when the plan reproduces it.

---

## Task 1: Extend `adapters.CatalogProvider` with `Origin` and `Kind`

**Files:**
- Modify: `internal/adapters/catalog.go`
- Test: existing tests in `internal/adapters/catalog_test.go` if present; otherwise the change compiles cleanly and is exercised in Task 5.

Spec reference: §Architecture — Streams Adapter Changes, "Edit 5: `adapters.CatalogProvider` shape extension".

- [ ] **Step 1: Open `internal/adapters/catalog.go` and locate the `CatalogProvider` struct.**

Verify current shape:

```bash
rg -n "type CatalogProvider struct" internal/adapters/catalog.go
```

Expected: a line number matching the struct definition at or around line 9.

- [ ] **Step 2: Add `Origin` and `Kind` fields to the struct.**

Edit `internal/adapters/catalog.go` so the struct reads:

```go
type CatalogProvider struct {
	ID             string         // e.g. "mtv-rewind"
	DisplayName    string         // e.g. "MTV Rewind"
	BadgeLabel     string         // e.g. "MTV" — small text in .ic glyph
	BadgeClass     string         // e.g. "mtv" | "cartoon" | "toonami" — CSS hook
	Origin         string         // 4C: parsed BaseURL.Host of the provider's manifest, e.g. "wantmymtv.vercel.app"
	Kind           string         // 4C: provider-type tag, e.g. "youtube-channel-json" | "direct-streams"
	Live           bool           // whole provider is always-live (direct streams)
	DefaultChannel string         // for the catalog's initial selection
	Groups         []CatalogGroup // ordered
}
```

- [ ] **Step 3: Verify the package still compiles.**

Run: `go build ./...`
Expected: no errors. `internal/adapters/streams` will compile because the new fields are unused (zero-value strings).

- [ ] **Step 4: Commit.**

```bash
git add internal/adapters/catalog.go
git commit -m "feat(adapters): add Origin and Kind fields to CatalogProvider

4C Catalog pane stat-line surface. Empty strings render cleanly in the
existing 3B browse drawer; streams adapter populates them in a follow-up
task. Backwards compatible — no existing consumer references either
field."
```

---

## Task 2: Export `config.DefaultConfigTOML(dataDir)` helper

**Files:**
- Modify: `internal/config/example.go`
- Test: `internal/config/example_test.go` (create if absent)

Spec reference: §Implementation Checklist line citing `DefaultConfigTOML` + §Goal 2 "Restore-defaults works end to end".

- [ ] **Step 1: Write the failing test.**

Create or extend `internal/config/example_test.go`:

```go
package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDefaultConfigTOML_SubstitutesDataDir(t *testing.T) {
	got, err := DefaultConfigTOML("/tmp/custom-data-dir")
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	if !strings.Contains(string(got), `data_dir = "/tmp/custom-data-dir"`) {
		t.Fatalf("expected data_dir = \"/tmp/custom-data-dir\" in output; got:\n%s", string(got))
	}
}

func TestDefaultConfigTOML_EmptyDataDirUsesPlatformDefault(t *testing.T) {
	got, err := DefaultConfigTOML("")
	if err != nil {
		t.Fatalf("DefaultConfigTOML(\"\"): %v", err)
	}
	// Output must NOT contain the literal `data_dir = ""` template marker;
	// empty input means "platform default" and the helper substitutes.
	if strings.Contains(string(got), `data_dir = ""`) {
		t.Fatalf("expected platform-default substitution; got literal empty marker:\n%s", string(got))
	}
}

func TestDefaultConfigTOML_ParsesAsSectioned(t *testing.T) {
	body, err := DefaultConfigTOML("/tmp/x")
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	var sec Sectioned
	if _, err := toml.Decode(string(body), &sec); err != nil {
		t.Fatalf("DefaultConfigTOML output does not parse via Sectioned: %v", err)
	}
	if err := sec.Validate(); err != nil {
		t.Fatalf("DefaultConfigTOML output fails Sectioned.Validate(): %v", err)
	}
}
```

- [ ] **Step 2: Run the test; expect FAIL because `DefaultConfigTOML` does not exist.**

Run: `go test ./internal/config -run TestDefaultConfigTOML -v`
Expected: compile error `undefined: DefaultConfigTOML` (or equivalent).

- [ ] **Step 3: Implement `DefaultConfigTOML` in `internal/config/example.go`.**

Append (or insert alongside the existing helpers):

```go
// DefaultConfigTOML returns the bundled example.toml with the supplied
// dataDir substituted into the `data_dir = ""` template marker. When
// dataDir is empty, falls through to the platform default via
// defaultDataDirForConfigWrite() so the output matches first-run
// semantics. Used by the 4C restore-defaults action wrapper.
//
// Why not toml.NewEncoder(&buf).Encode(Sectioned): BurntSushi/toml does
// not round-trip toml.Primitive values, so re-encoding Sectioned would
// silently drop adapter sections. The embedded example.toml is the
// authoritative round-trippable source — see
// internal/uiserver/adapter_saver.go:13-18 for the parallel rationale.
func DefaultConfigTOML(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return defaultConfigTOML()
	}
	line := []byte(`data_dir = ""`)
	repl := []byte(`data_dir = ` + strconv.Quote(dataDir))
	out := bytes.Replace(ExampleTOML(), line, repl, 1)
	if bytes.Equal(out, exampleTOML) {
		return nil, fmt.Errorf("DefaultConfigTOML: data_dir template marker missing")
	}
	return out, nil
}
```

(The required imports — `bytes`, `fmt`, `strconv` — are already present in the file.)

- [ ] **Step 4: Run the test; expect PASS.**

Run: `go test ./internal/config -run TestDefaultConfigTOML -v`
Expected: all three subtests PASS.

- [ ] **Step 5: Run race detector for the whole config package.**

Run: `go test -race ./internal/config`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/config/example.go internal/config/example_test.go
git commit -m "feat(config): export DefaultConfigTOML(dataDir) helper

Wraps the existing defaultConfigTOML line-replace pattern with a
caller-supplied data_dir override. Used by the 4C restore-defaults
action wrapper to preserve the operator's data_dir through a reset
(keeps device UUID, plex.tv token, streams cache reachable).

Avoids toml.NewEncoder(Sectioned) which drops adapter sections."
```

---

## Task 3: Add `streams.Adapter.ConfigSnapshot()` + `deepCopyConfig`

**Files:**
- Modify: `internal/adapters/streams/adapter.go`
- Test: `internal/adapters/streams/adapter_test.go` (extend)

Spec reference: §Architecture — Streams Adapter Changes "Edit 1: `Adapter.ConfigSnapshot() Config`".

- [ ] **Step 1: Write the failing test.**

Append to `internal/adapters/streams/adapter_test.go` (or a new test file):

```go
func TestAdapter_ConfigSnapshot_IndependentProvidersMap(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false, HLSBufferDisabled: false},
	}

	snap := a.ConfigSnapshot()
	snap.Providers["mtv-rewind"] = ProviderConfig{Disabled: true, HLSBufferDisabled: true}

	if a.cfg.Providers["mtv-rewind"].Disabled {
		t.Fatalf("mutating snapshot mutated adapter Providers[id].Disabled")
	}
	if a.cfg.Providers["mtv-rewind"].HLSBufferDisabled {
		t.Fatalf("mutating snapshot mutated adapter Providers[id].HLSBufferDisabled")
	}
}

func TestAdapter_ConfigSnapshot_IndependentChannelsMap(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"toonami-aftermath": {
			Channels: map[string]ChannelConfig{
				"east": {HLSBufferDisabled: false},
			},
		},
	}

	snap := a.ConfigSnapshot()
	snap.Providers["toonami-aftermath"].Channels["east"] = ChannelConfig{HLSBufferDisabled: true}

	if a.cfg.Providers["toonami-aftermath"].Channels["east"].HLSBufferDisabled {
		t.Fatalf("mutating snapshot mutated adapter nested ChannelConfig")
	}
}

func TestAdapter_ConfigSnapshot_IndependentAllowedHostsSlice(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.RemoteProviderAllowedHosts = []string{"example.com"}

	snap := a.ConfigSnapshot()
	snap.RemoteProviderAllowedHosts = append(snap.RemoteProviderAllowedHosts, "evil.example")

	if len(a.cfg.RemoteProviderAllowedHosts) != 1 {
		t.Fatalf("appending to snapshot slice mutated adapter slice; len=%d",
			len(a.cfg.RemoteProviderAllowedHosts))
	}
}
```

A `newTestAdapter` helper already exists in `internal/adapters/streams/test_helpers_test.go` (lines 16-19) with signature `func newTestAdapter(t *testing.T) (*Adapter, error)` — it calls `New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})`. Use it via:

```go
a, err := newTestAdapter(t)
if err != nil {
	t.Fatalf("newTestAdapter: %v", err)
}
// then set cfg fields directly: a.cfg.Providers = ...
```

For brevity in the tests above, wrap it in a tiny local helper that fatals on error:

```go
func mustTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	a, err := newTestAdapter(t)
	if err != nil {
		t.Fatalf("newTestAdapter: %v", err)
	}
	return a
}
```

…and call `mustTestAdapter(t)` instead of `newTestAdapter(t)` in the assertions above.

- [ ] **Step 2: Run; expect FAIL because `ConfigSnapshot` does not exist.**

Run: `go test ./internal/adapters/streams -run TestAdapter_ConfigSnapshot -v`
Expected: compile error `a.ConfigSnapshot undefined`.

- [ ] **Step 3: Implement `ConfigSnapshot` and `deepCopyConfig` in `internal/adapters/streams/adapter.go`.**

Append near the bottom of the file (after the existing `Adapter` methods):

```go
// ConfigSnapshot returns a deep copy of the adapter's current Config.
// Maps (Providers, per-provider Channels) are independently allocated;
// the caller can mutate the returned value without affecting the live
// adapter state. Used by the chassis Catalog manager wrapper to read
// provider state for rendering and to mutate a snapshot prior to
// ApplyConfigValue.
func (a *Adapter) ConfigSnapshot() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return deepCopyConfig(a.cfg)
}

// deepCopyConfig returns a value copy of in with independently allocated
// Providers and per-provider Channels maps. Scalar fields and the
// RemoteProviderAllowedHosts slice are copied so callers cannot leak
// mutations back into the source.
func deepCopyConfig(in Config) Config {
	out := in
	if in.RemoteProviderAllowedHosts != nil {
		out.RemoteProviderAllowedHosts = append([]string(nil), in.RemoteProviderAllowedHosts...)
	}
	if in.Providers != nil {
		out.Providers = make(map[string]ProviderConfig, len(in.Providers))
		for id, pc := range in.Providers {
			pcCopy := pc
			if pc.Channels != nil {
				pcCopy.Channels = make(map[string]ChannelConfig, len(pc.Channels))
				for cid, cc := range pc.Channels {
					pcCopy.Channels[cid] = cc
				}
			}
			out.Providers[id] = pcCopy
		}
	}
	return out
}
```

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/adapters/streams -run TestAdapter_ConfigSnapshot -v`
Expected: all three subtests PASS.

- [ ] **Step 5: Run race detector for the package.**

Run: `go test -race ./internal/adapters/streams`
Expected: PASS (no new test cases needed for race; the snapshot is taken under `a.mu`).

- [ ] **Step 6: Commit.**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/adapter_test.go
git commit -m "feat(streams): add Adapter.ConfigSnapshot for chassis Catalog pane

Returns a deep copy of the live Config with independently allocated
Providers + Channels maps and RemoteProviderAllowedHosts slice. Used
by the chassis catalogManager wrapper to read provider state and to
build a mutable snapshot prior to ApplyConfigValue."
```

---

## Task 4: Add `streams.Adapter.ApplyConfigValue` + `encodeSectionTOML`

**Files:**
- Modify: `internal/adapters/streams/adapter.go`
- Test: `internal/adapters/streams/adapter_test.go` (extend)

Spec reference: §Architecture — Streams Adapter Changes "Edit 2: `Adapter.ApplyConfigValue`".

- [ ] **Step 1: Write the failing test.**

Append to `internal/adapters/streams/adapter_test.go`:

```go
func TestAdapter_ApplyConfigValue_PersistsAndApplies(t *testing.T) {
	a := mustTestAdapter(t)
	// Seed a non-default starting config.
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false},
	}

	var savedName string
	var savedBytes []byte
	save := func(name string, raw []byte) error {
		savedName = name
		savedBytes = append([]byte(nil), raw...)
		return nil
	}

	newCfg := a.ConfigSnapshot()
	newCfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}

	scope, err := a.ApplyConfigValue(newCfg, save)
	if err != nil {
		t.Fatalf("ApplyConfigValue: %v", err)
	}
	if scope == 0 {
		t.Fatalf("expected non-zero ApplyScope; got 0")
	}
	if savedName != "streams" {
		t.Fatalf("expected saver to be called with name=%q; got %q", "streams", savedName)
	}
	if len(savedBytes) == 0 {
		t.Fatalf("expected saver to receive non-empty TOML bytes")
	}
	if !a.cfg.Providers["mtv-rewind"].Disabled {
		t.Fatalf("expected in-memory cfg to reflect Disabled=true; got false")
	}
}

func TestAdapter_ApplyConfigValue_ValidationFailureNoSave(t *testing.T) {
	a := mustTestAdapter(t)
	called := false
	save := func(name string, raw []byte) error {
		called = true
		return nil
	}

	bad := a.ConfigSnapshot()
	bad.ManifestURL = "   " // Validate() rejects empty/whitespace.

	_, err := a.ApplyConfigValue(bad, save)
	if err == nil {
		t.Fatalf("expected validation error; got nil")
	}
	if called {
		t.Fatalf("saver should NOT be called on validation failure")
	}
}

func TestAdapter_ApplyConfigValue_SaveFailureNoInMemoryChange(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false},
	}
	original := a.cfg.Providers["mtv-rewind"].Disabled

	saveErr := errors.New("disk write failed")
	save := func(name string, raw []byte) error { return saveErr }

	newCfg := a.ConfigSnapshot()
	newCfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}

	_, err := a.ApplyConfigValue(newCfg, save)
	if !errors.Is(err, saveErr) {
		t.Fatalf("expected save error to surface; got %v", err)
	}
	if a.cfg.Providers["mtv-rewind"].Disabled != original {
		t.Fatalf("in-memory state mutated after save failure")
	}
}

func TestAdapter_ApplyConfigValue_SnapshotRebuildFailureNoSaveNoInMemoryChange(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false},
	}
	original := a.cfg.Providers["mtv-rewind"].Disabled
	called := false
	save := func(name string, raw []byte) error {
		called = true
		return nil
	}

	rebuildErr := errors.New("snapshot rebuild failed")
	oldBuild := buildStartupSnapshotForApplyConfigValue
	buildStartupSnapshotForApplyConfigValue = func(ctx context.Context, cfg Config, cacheDir string) ([]ProviderDefinition, []ProviderCatalog, error) {
		return nil, nil, rebuildErr
	}
	t.Cleanup(func() { buildStartupSnapshotForApplyConfigValue = oldBuild })

	newCfg := a.ConfigSnapshot()
	newCfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}

	_, err := a.ApplyConfigValue(newCfg, save)
	if !errors.Is(err, rebuildErr) {
		t.Fatalf("expected rebuild error to surface; got %v", err)
	}
	if called {
		t.Fatalf("saver should NOT be called when snapshot rebuild fails")
	}
	if a.cfg.Providers["mtv-rewind"].Disabled != original {
		t.Fatalf("in-memory state mutated after snapshot rebuild failure")
	}
}
```

(If `context` or `errors` is not yet imported in the test file, add them to the import block.)

- [ ] **Step 2: Run; expect FAIL because `ApplyConfigValue` does not exist.**

Run: `go test ./internal/adapters/streams -run TestAdapter_ApplyConfigValue -v`
Expected: compile error.

- [ ] **Step 3: Implement `ApplyConfigValue` and `encodeSectionTOML` in `internal/adapters/streams/adapter.go`.**

Append:

```go
var buildStartupSnapshotForApplyConfigValue = buildStartupSnapshot

// ApplyConfigValue validates and rebuilds the startup snapshot before
// persisting newCfg via save (which writes the [adapters.streams]
// section to disk under the AdapterSaver mutex) and installing it as
// the live config. Validation or snapshot-rebuild failures leave both
// disk and in-memory state untouched. Returns the aggregated ApplyScope
// from configChangeScope(old, new).
//
// Save is invoked exactly once after validation and snapshot rebuild
// both succeed; in-memory install happens after save returns nil so
// disk and memory stay coherent (matches the BridgeSaver write-before-
// apply contract).
func (a *Adapter) ApplyConfigValue(newCfg Config, save func(name string, raw []byte) error) (adapters.ApplyScope, error) {
	if err := newCfg.Validate(); err != nil {
		return 0, err
	}
	defs, catalogs, err := buildStartupSnapshotForApplyConfigValue(context.Background(), newCfg, a.cacheDir)
	if err != nil {
		return 0, err
	}
	tomlBytes, err := encodeSectionTOML(newCfg)
	if err != nil {
		return 0, fmt.Errorf("streams: encode section: %w", err)
	}
	if err := save("streams", tomlBytes); err != nil {
		return 0, err
	}
	a.mu.Lock()
	oldCfg := a.cfg
	a.cfg = newCfg
	a.installSnapshotLocked(defs, catalogs)
	a.mu.Unlock()
	a.reconcileRefreshLoop()
	return configChangeScope(oldCfg, newCfg), nil
}

// encodeSectionTOML encodes a Config to the TOML bytes that belong
// inside the [adapters.streams] section. Sibling of the existing
// configToWire conversion.
func encodeSectionTOML(cfg Config) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(configToWire(cfg)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
```

Add `"bytes"` to the file's imports if not already present.

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/adapters/streams -run TestAdapter_ApplyConfigValue -v`
Expected: all four subtests PASS.

- [ ] **Step 5: Run race detector.**

Run: `go test -race ./internal/adapters/streams`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/adapter_test.go
git commit -m "feat(streams): add Adapter.ApplyConfigValue for chassis Catalog pane

Config-value entry point parallel to the existing ApplyConfig (which
takes toml.Primitive). Validates → rebuilds startup snapshot → encodes
via encodeSectionTOML → saves → installs snapshot under a.mu →
reconciles refresh loop.
Returns configChangeScope(old, new) just like ApplyConfig. Used by the
chassis catalogManager wrapper after a ConfigSnapshot/mutate cycle."
```

---

## Task 5: Add `streams.Adapter.StopActiveCast`

**Files:**
- Modify: `internal/adapters/streams/adapter.go`
- Test: `internal/adapters/streams/adapter_test.go` (extend)

Spec reference: §Architecture — Streams Adapter Changes "Edit 3: `Adapter.StopActiveCast`".

- [ ] **Step 1: Write the failing test.**

Append to `internal/adapters/streams/adapter_test.go`:

```go
func TestAdapter_StopActiveCast_DropsActiveQueueAndCallsCore(t *testing.T) {
	a := mustTestAdapter(t)
	core := &fakeCore{}
	a.core = core
	a.active = newFakeActiveQueueForStopActiveCast(t)
	activeRef := activeAdapterRef(a.active)
	if activeRef == "" {
		t.Fatalf("activeAdapterRef returned empty ref for seeded queue")
	}
	core.status.AdapterRef = activeRef

	if err := a.StopActiveCast(); err != nil {
		t.Fatalf("StopActiveCast: %v", err)
	}
	if a.active != nil {
		t.Fatalf("expected a.active to be cleared; got non-nil")
	}
	if core.stopCalls != 1 {
		t.Fatalf("StopIfAdapterRef calls = %d; want 1", core.stopCalls)
	}
	if core.status.AdapterRef != "" {
		t.Fatalf("core AdapterRef = %q; want cleared", core.status.AdapterRef)
	}
}

func TestAdapter_StopActiveCast_NoActiveQueue_NoOp(t *testing.T) {
	a := mustTestAdapter(t)
	core := &fakeCore{}
	a.core = core
	a.active = nil

	if err := a.StopActiveCast(); err != nil {
		t.Fatalf("StopActiveCast: %v", err)
	}
	if core.stopCalls != 0 {
		t.Fatalf("StopIfAdapterRef calls = %d; want 0", core.stopCalls)
	}
}

func TestAdapter_StopActiveCast_NoCoreManager_NoOp(t *testing.T) {
	a := mustTestAdapter(t)
	a.core = nil
	a.active = newFakeActiveQueueForStopActiveCast(t)

	if err := a.StopActiveCast(); err != nil {
		t.Fatalf("StopActiveCast: %v", err)
	}
	// active is still cleared even when core is nil — clearActiveLocked runs.
	if a.active != nil {
		t.Fatalf("expected a.active to be cleared even without core; got non-nil")
	}
}

func newFakeActiveQueueForStopActiveCast(t *testing.T) *ActiveQueue {
	t.Helper()
	return &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "1stday",
		SessionID:  "streams-session",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "one", URL: "https://media.example/one.m3u8"}},
	}
}
```

This reuses the existing streams-package `fakeCore` from `test_helpers_test.go`, which already satisfies the full `SessionManager` interface. The nonzero `SessionID` and `ItemToken` are required because `activeAdapterRef` returns an empty string for queues without an item token.

- [ ] **Step 2: Run; expect FAIL.**

Run: `go test ./internal/adapters/streams -run TestAdapter_StopActiveCast -v`
Expected: compile error `a.StopActiveCast undefined`.

- [ ] **Step 3: Implement `StopActiveCast` in `internal/adapters/streams/adapter.go`.**

Append:

```go
// StopActiveCast drops the streams adapter's active queue and stops the
// underlying core session via SessionManager.StopIfAdapterRef. It does
// NOT cancel the manifest refresh loop or change the adapter's State —
// this is intentionally a narrower operation than Stop(). Used by the
// chassis catalogManager wrapper as the RECAST runtime side effect
// when Catalog-pane saves change per-provider HLS posture.
//
// Locking discipline mirrors Adapter.Stop(): playbackMu first, then
// mu for the snapshot read and clearActiveLocked, then mu released
// before the (potentially blocking) StopIfAdapterRef call.
func (a *Adapter) StopActiveCast() error {
	a.playbackMu.Lock()
	defer a.playbackMu.Unlock()

	a.mu.Lock()
	ref := activeAdapterRef(a.active)
	hadActive := a.active != nil
	coreManager := a.core
	if hadActive {
		a.clearActiveLocked()
	}
	a.mu.Unlock()

	if hadActive && coreManager != nil && ref != "" {
		_, err := coreManager.StopIfAdapterRef(ref)
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/adapters/streams -run TestAdapter_StopActiveCast -v`
Expected: all three subtests PASS.

- [ ] **Step 5: Run race detector.**

Run: `go test -race ./internal/adapters/streams`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/adapter_test.go
git commit -m "feat(streams): add Adapter.StopActiveCast for Catalog-side RECAST

Bottom-half of Adapter.Stop() factored out — clears the active queue
and calls SessionManager.StopIfAdapterRef with the active AdapterRef,
without touching the manifest refresh loop or adapter lifecycle state.
Used by the chassis catalogManager wrapper to honor RECAST scope on
per-provider HLS toggles. No internal/core change required."
```

---

## Task 6: Extend `streams.Adapter.Catalog()` to populate `Origin` + `Kind`

**Files:**
- Modify: `internal/adapters/streams/catalog.go` (where `Catalog()` is currently defined per `rg -n "func.*Catalog()" internal/adapters/streams/`)
- Test: `internal/adapters/streams/adapter_test.go` (extend)

Spec reference: §Architecture — Streams Adapter Changes "Edit 4: `Catalog()` populates `Origin` and `Kind`".

- [ ] **Step 1: Write the failing test.**

Append to `internal/adapters/streams/adapter_test.go`:

```go
func TestAdapter_Catalog_PopulatesOriginAndKindForBundledProviders(t *testing.T) {
	a := mustTestAdapter(t)

	want := map[string]struct {
		origin string
		kind   string
	}{
		"mtv-rewind":        {origin: "wantmymtv.vercel.app", kind: "youtube-channel-json"},
		"cartoon-rewind":    {origin: "cartoonrewind.tv", kind: "youtube-channel-json"},
		"toonami-aftermath": {origin: "api.toonamiaftermath.com", kind: "direct-streams"},
	}

	got := a.Catalog()
	if len(got) < len(want) {
		t.Fatalf("expected at least %d providers; got %d", len(want), len(got))
	}
	byID := map[string]adapters.CatalogProvider{}
	for _, p := range got {
		byID[p.ID] = p
	}
	for id, expect := range want {
		p, ok := byID[id]
		if !ok {
			t.Fatalf("provider %q missing from Catalog()", id)
		}
		if p.Origin != expect.origin {
			t.Errorf("provider %q Origin = %q; want %q", id, p.Origin, expect.origin)
		}
		if p.Kind != expect.kind {
			t.Errorf("provider %q Kind = %q; want %q", id, p.Kind, expect.kind)
		}
	}
}
```

`mustTestAdapter(t)` resolves via the wrapper introduced in Task 3 around the existing `newTestAdapter` helper. A freshly `New()`'d streams adapter returns a populated `Catalog()` thanks to the local-only bootstrap path at [catalog.go:74-80](../../../internal/adapters/streams/catalog.go#L74-L80) — `Start()` is not required for this test.

- [ ] **Step 2: Run; expect FAIL — assertions on `p.Origin` and `p.Kind` evaluate against empty strings.**

Run: `go test ./internal/adapters/streams -run TestAdapter_Catalog_PopulatesOriginAndKind -v`
Expected: per-provider errors like `provider "mtv-rewind" Origin = ""; want "wantmymtv.vercel.app"`.

- [ ] **Step 3: Find the `Catalog()` builder and extend it.**

```bash
rg -n "func .* Catalog\\(\\)" internal/adapters/streams
```

Open the file (`catalog.go`). Locate where the returned `adapters.CatalogProvider` value is constructed for each provider. Add:

```go
// Within the per-provider construction block:
origin := ""
if u, err := url.Parse(def.BaseURL); err == nil {
	origin = u.Host
}
if origin == "" {
	if u, err := url.Parse(def.PlaylistURL); err == nil {
		origin = u.Host
	}
}

// Then populate the returned struct:
out = append(out, adapters.CatalogProvider{
	ID:             def.ID,
	DisplayName:    def.DisplayName,
	BadgeLabel:     def.FallbackLabel,
	BadgeClass:     /* existing logic */,
	Origin:         origin,
	Kind:           def.Type,
	Live:           live,
	DefaultChannel: def.DefaultChannel,
	Groups:         groups,
})
```

Add `"net/url"` to the file's imports if not already present.

The exact existing variable names (`def`, `live`, `groups`) and the wider builder code follow the current `catalog.go` shape — don't rewrite the surrounding logic, just add Origin/Kind.

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/adapters/streams -run TestAdapter_Catalog_PopulatesOriginAndKind -v`
Expected: PASS for all three providers.

- [ ] **Step 5: Run the full streams package test suite + race.**

Run: `go test -race ./internal/adapters/streams`
Expected: PASS. Adding two string fields to a previously-empty zero-value position should not change any existing assertion that checks struct values.

- [ ] **Step 6: Commit.**

```bash
git add internal/adapters/streams/catalog.go internal/adapters/streams/adapter_test.go
git commit -m "feat(streams): populate Origin and Kind in Catalog() output

Origin is parsed BaseURL.Host with PlaylistURL.Host fallback. Kind is
the provider Type tag (\"youtube-channel-json\" / \"direct-streams\"
constants from assets.go). Surfaces the mockup's stat-line metadata
through the chassis-shaped CatalogProvider read view. Backwards
compatible — empty strings (e.g., for adapters that don't supply
these fields) render cleanly in both the 3B browse drawer and the new
4C Catalog pane."
```

---

## Task 7: Declare chassis-owned interfaces and types in `internal/chassis/settings.go`

**Files:**
- Modify: `internal/chassis/settings.go`
- Test: compilation only (handlers + routes ship in later tasks)

Spec reference: §Architecture — Chassis-Owned Interfaces.

- [ ] **Step 1: Open `internal/chassis/settings.go` and locate the existing interface declarations.**

```bash
rg -n "^type .* interface" internal/chassis/settings.go
```

You will see `BridgeSettingsSaver`, `Prober`, `CoreLauncher`, `settingsChipError`. Add the new interfaces and types alongside.

- [ ] **Step 2: Append the new declarations.**

Insert (after the existing interface declarations, before the handler functions):

```go
// CatalogSettingsManager is the chassis-side interface for Catalog-pane
// state mutation. Production passes a thin wrapper around *streams.Adapter
// from cmd/mister-groovy-relay; internal/chassis does NOT import
// internal/adapters/streams.
type CatalogSettingsManager interface {
	// Providers returns the renderable Catalog-pane state. Stable order
	// matches StreamsCatalogViewer.Catalog() so the two surfaces agree
	// on ID/order. Safe to call before adapter Start.
	Providers() []CatalogProviderState

	// UpdateProvider applies the patch's non-nil flags to providers.<id>
	// in a single snapshot/save/apply cycle. Either pointer may be nil
	// (means "do not change that field"); both nil is rejected by the
	// chassis handler before invoking the interface. Returns the
	// aggregated ApplyScope (with the Catalog-side declared-scope floor
	// already applied by the production wrapper).
	UpdateProvider(id string, patch CatalogProviderPatch) (adapters.ApplyScope, error)

	// SetDirectStreamHLSBuffer flips providers.<id>.hls_buffer_disabled
	// for every provider where Live == true in one save. Returns the
	// max-wins scope (RECAST after the declared-scope floor).
	SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error)
}

// CatalogProviderPatch is the optional-field patch consumed by
// UpdateProvider. Pointer-to-bool encodes the tri-state {unset, true,
// false} the chassis handler needs to distinguish "this form key was
// omitted" from "this form key was set to false."
type CatalogProviderPatch struct {
	Enabled           *bool
	HLSBufferDisabled *bool
}

// CatalogProviderState is the chassis-shaped per-provider state for
// rendering and mutation. All fields are populated by the production
// wrapper from streams.Config + adapters.CatalogProvider; the chassis
// renders directly from this struct.
type CatalogProviderState struct {
	ID                string
	DisplayName       string
	BadgeLabel        string
	BadgeClass        string
	Origin            string
	Kind              string
	DefaultChannel    string
	Live              bool
	ChannelCount      int
	Enabled           bool
	HLSBufferDisabled bool
}

// ConfigReset is the chassis-side interface for the restore-defaults
// action. Production passes a wrapper that calls config.WriteAtomic
// with the bundled defaults TOML, preserving the operator's data_dir.
// Scope is REBOOT (live process continues with old config; restart
// applies defaults).
type ConfigReset interface {
	// ResetToDefaults atomically rewrites the on-disk config.toml with
	// the bundled defaults (data_dir preserved from the live config).
	// MUST NOT touch data_dir contents, MUST NOT mutate in-memory
	// bridge/adapter state. Disk-write failures return a typed error
	// satisfying settingsChipError so the chassis can map to
	// {chip:"WRITE FAILED"} cleanly.
	ResetToDefaults() error
}
```

- [ ] **Step 3: Verify the chassis package still compiles.**

Run: `go build ./internal/chassis`
Expected: no errors.

- [ ] **Step 4: Verify the chassis isolation contract holds.**

Run: `go test ./internal/chassis -run TestForbiddenImports -v`
Expected: PASS. The new declarations import only `internal/adapters` (for `ApplyScope`), which is already allowed.

- [ ] **Step 5: Commit.**

```bash
git add internal/chassis/settings.go
git commit -m "feat(chassis): declare CatalogSettingsManager and ConfigReset interfaces

Two new narrow chassis-owned interfaces for Phase 4C, alongside the
4A/4B BridgeSettingsSaver, Prober, CoreLauncher, settingsChipError.
Includes CatalogProviderState (render shape) and CatalogProviderPatch
(tri-state mutation patch). Production satisfies these from
cmd/mister-groovy-relay; chassis remains free of internal/uiserver,
internal/misterctl, and internal/adapters/streams imports."
```

---

## Task 8: Extend `chassis.SettingsData` and `buildSettingsData`

**Files:**
- Modify: `internal/chassis/data.go`
- Test: `internal/chassis/data_test.go` (extend)

Spec reference: §Architecture — `SettingsData` and Snapshot Wiring.

- [ ] **Step 1: Write the failing test.**

Append to `internal/chassis/data_test.go`:

```go
func TestBuildSettingsData_CatalogPaneProviderCountAndChannels(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "mtv-rewind", ChannelCount: 73, Live: false},
			{ID: "cartoon-rewind", ChannelCount: 13, Live: false},
			{ID: "toonami-aftermath", ChannelCount: 4, Live: true, HLSBufferDisabled: false},
		},
	}
	catalog := fakeCatalogViewer{providers: []adapters.CatalogProvider{
		{ID: "browse-only"},
	}}
	got := buildSettingsData(config.BridgeConfig{}, nil, catalog, mgr)
	if got.CatalogProviderCount != 1 {
		t.Errorf("CatalogProviderCount = %d; want 1 (existing tab badge from StreamsCatalogViewer)", got.CatalogProviderCount)
	}
	if got.CatalogPaneProviderCount != 3 {
		t.Errorf("CatalogPaneProviderCount = %d; want 3", got.CatalogPaneProviderCount)
	}
	if got.CatalogChannelCount != 90 {
		t.Errorf("CatalogChannelCount = %d; want 90", got.CatalogChannelCount)
	}
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false (one Live, HLSBufferDisabled=false)")
	}
}

func TestBuildSettingsData_DirectStreamHLSAllLiveDisabled(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "toonami-aftermath", Live: true, HLSBufferDisabled: true},
		},
	}
	got := buildSettingsData(config.BridgeConfig{}, nil, nil, mgr)
	if !got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = false; want true (all Live disabled)")
	}
}

func TestBuildSettingsData_DirectStreamHLSMixedStateRendersOff(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "toonami-aftermath", Live: true, HLSBufferDisabled: true},
			{ID: "live2", Live: true, HLSBufferDisabled: false},
		},
	}
	got := buildSettingsData(config.BridgeConfig{}, nil, nil, mgr)
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false (mixed renders as off)")
	}
}

func TestBuildSettingsData_NoLiveProvidersDirectStreamHLSFalse(t *testing.T) {
	mgr := &fakeCatalogManager{
		providers: []CatalogProviderState{
			{ID: "mtv-rewind", Live: false, HLSBufferDisabled: true},
		},
	}
	got := buildSettingsData(config.BridgeConfig{}, nil, nil, mgr)
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false (no Live)")
	}
}

func TestBuildSettingsData_NilCatalogManagerEmpty(t *testing.T) {
	catalog := fakeCatalogViewer{providers: []adapters.CatalogProvider{
		{ID: "mtv-rewind"},
		{ID: "cartoon-rewind"},
	}}
	got := buildSettingsData(config.BridgeConfig{}, nil, catalog, nil)
	if got.CatalogProviderCount != 2 {
		t.Errorf("CatalogProviderCount = %d; want 2 (tab badge fallback)", got.CatalogProviderCount)
	}
	if got.CatalogPaneProviderCount != 0 {
		t.Errorf("CatalogPaneProviderCount = %d; want 0", got.CatalogPaneProviderCount)
	}
	if got.CatalogProviders != nil {
		t.Errorf("CatalogProviders = %v; want nil", got.CatalogProviders)
	}
	if got.CatalogChannelCount != 0 {
		t.Errorf("CatalogChannelCount = %d; want 0", got.CatalogChannelCount)
	}
	if got.DirectStreamHLSBufferDisabled {
		t.Errorf("DirectStreamHLSBufferDisabled = true; want false")
	}
}

// fakeCatalogManager is a CatalogSettingsManager test double. Mutation
// methods record their args; Providers returns the configured slice.
type fakeCatalogManager struct {
	providers []CatalogProviderState
}

func (f *fakeCatalogManager) Providers() []CatalogProviderState { return f.providers }
func (f *fakeCatalogManager) UpdateProvider(id string, patch CatalogProviderPatch) (adapters.ApplyScope, error) {
	return 0, nil
}
func (f *fakeCatalogManager) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
	return 0, nil
}
```

- [ ] **Step 2: Run; expect FAIL because new `SettingsData` fields and the `buildSettingsData` argument do not exist yet.**

Run: `go test ./internal/chassis -run TestBuildSettingsData -v`
Expected: compile error on the `mgr` argument or missing struct fields.

- [ ] **Step 3: Extend `SettingsData` in `internal/chassis/data.go`.**

Locate the struct and add:

```go
type SettingsData struct {
	Open                          bool
	Bridge                        config.BridgeConfig
	Errors                        map[string]string
	AdapterCount                  int
	CatalogProviderCount          int                    // existing tab badge count from StreamsCatalogViewer
	CatalogPaneProviderCount      int                    // 4C — len(CatalogProviders)
	CatalogProviders              []CatalogProviderState // 4C
	CatalogChannelCount           int                    // 4C — sum across CatalogProviders
	DirectStreamHLSBufferDisabled bool                   // 4C — true iff every Live provider has hls_buffer_disabled
}
```

- [ ] **Step 4: Extend `buildSettingsData` to take a `CatalogSettingsManager` and populate the new fields.**

Update the signature (current callers — `snapshotFromStatusView`, `idleSnapshot` in `session.go` — will be fixed in the next step). Inside the function, after the existing 4A/4B body:

```go
func buildSettingsData(
	bridge config.BridgeConfig,
	registry *adapters.Registry,
	catalog adapters.StreamsCatalogViewer,
	catalogManager CatalogSettingsManager,
) SettingsData {
	out := SettingsData{
		Bridge:       bridge,
		AdapterCount: countConfigurableAdapters(registry),
	}
	if catalog != nil {
		out.CatalogProviderCount = len(catalog.Catalog())
	}
	if catalogManager != nil {
		providers := catalogManager.Providers()
		out.CatalogProviders = providers
		out.CatalogPaneProviderCount = len(providers)
		channelTotal := 0
		liveCount := 0
		liveDisabledCount := 0
		for _, p := range providers {
			channelTotal += p.ChannelCount
			if p.Live {
				liveCount++
				if p.HLSBufferDisabled {
					liveDisabledCount++
				}
			}
		}
		out.CatalogChannelCount = channelTotal
		out.DirectStreamHLSBufferDisabled = liveCount > 0 && liveDisabledCount == liveCount
	}
	return out
}
```

The helper `countConfigurableAdapters` is the existing 4A logic; preserve it. The existing `CatalogProviderCount` remains the settings-tab badge count from the 3B browse drawer's `StreamsCatalogViewer`; the new `CatalogPaneProviderCount` is the Catalog-pane header count from `CatalogManager.Providers()`.

- [ ] **Step 5: Update `internal/chassis/session.go` callers.**

```bash
rg -n "buildSettingsData\\(" internal/chassis/session.go
```

Update each call site to pass `s.cfg.CatalogManager` (which will be wired by Task 16). For now, since `Config.CatalogManager` does not yet exist, callers must pass `nil`. Add `nil` as the fourth argument at each call site.

- [ ] **Step 6: Run; expect PASS.**

Run: `go test ./internal/chassis -run TestBuildSettingsData -v`
Expected: all five subtests PASS.

- [ ] **Step 7: Run the broader chassis package + race.**

Run: `go test -race ./internal/chassis`
Expected: PASS.

- [ ] **Step 8: Commit.**

```bash
git add internal/chassis/data.go internal/chassis/data_test.go internal/chassis/session.go
git commit -m "feat(chassis): SettingsData carries Catalog provider state

buildSettingsData gains a CatalogSettingsManager arg and populates
CatalogPaneProviderCount + CatalogProviders + CatalogChannelCount +
DirectStreamHLSBufferDisabled. The global HLS switch reflects \"every
Live provider disabled\" — mixed state renders as off per spec. The
existing CatalogProviderCount stays the 3B browse drawer tab badge.
Session callers pass nil until main.go wires the production manager in
a later task."
```

---

## Task 9: Add `CatalogManager` and `ConfigReset` fields to `chassis.Config`

**Files:**
- Modify: `internal/chassis/server.go`

Spec reference: §Architecture — Chassis-Owned Interfaces, `Server.Config` extension.

- [ ] **Step 1: Open `internal/chassis/server.go` and locate the `Config` struct.**

```bash
rg -n "type Config struct" internal/chassis/server.go
```

- [ ] **Step 2: Add the two new fields.**

Inside the `Config` struct, after the existing 4A/4B fields (`BridgeSaver`, `Prober`, `CoreLauncher`, etc.):

```go
// 4C: catalog pane state mutation + restore-defaults action.
CatalogManager CatalogSettingsManager
ConfigReset    ConfigReset
```

- [ ] **Step 3: Update internal Server wiring if the existing pattern stores fields on `*Server`.**

```bash
rg -n -m 5 "cfg\\.BridgeSaver|s\\.bridgeSaver" internal/chassis/server.go
```

If the existing pattern is `s.cfg.<field>`, no further wiring is needed — the field is read directly from the `Config` value. If the pattern is to store into `*Server`, mirror it for the two new fields.

- [ ] **Step 4: Build.**

Run: `go build ./internal/chassis`
Expected: no errors.

- [ ] **Step 5: Commit.**

```bash
git add internal/chassis/server.go
git commit -m "feat(chassis): add CatalogManager and ConfigReset to Server.Config

Wire-points for the 4C catalog handlers (mounted in a later task) and
the restore-defaults action. Both default to nil when not wired; the
handlers respond 503 NOT READY in that case per spec."
```

---

## Task 10: Implement `cmd/mister-groovy-relay/catalog_manager.go` — Providers()

**Files:**
- Create: `cmd/mister-groovy-relay/catalog_manager.go`
- Create: `cmd/mister-groovy-relay/catalog_manager_test.go`

Spec reference: §Architecture — Production Wrappers.

- [ ] **Step 1: Write the failing test for `Providers()`.**

Create `cmd/mister-groovy-relay/catalog_manager_test.go`:

```go
package main

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// seedProviderCfg mutates the streams adapter's per-provider config
// via the public ApplyConfigValue path so cmd-side tests can stage
// state without touching unexported fields.
func seedProviderCfg(t *testing.T, a *streams.Adapter, providers map[string]streams.ProviderConfig) {
	t.Helper()
	cfg := a.ConfigSnapshot()
	cfg.Providers = providers
	if _, err := a.ApplyConfigValue(cfg, func(name string, raw []byte) error { return nil }); err != nil {
		t.Fatalf("seedProviderCfg: %v", err)
	}
}

func TestCatalogManager_Providers_EnrichesWithCfgState(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"mtv-rewind": {Disabled: true, HLSBufferDisabled: false},
	})
	m := &catalogManager{adapter: a, adapterSaver: nil}

	got := m.Providers()
	if len(got) == 0 {
		t.Fatalf("expected at least one provider; got 0")
	}
	var mtv chassis.CatalogProviderState
	for _, p := range got {
		if p.ID == "mtv-rewind" {
			mtv = p
			break
		}
	}
	if mtv.ID == "" {
		t.Fatalf("mtv-rewind missing from Providers() output")
	}
	if mtv.Enabled {
		t.Errorf("Enabled = true; want false (cfg.Disabled is true)")
	}
	if mtv.HLSBufferDisabled {
		t.Errorf("HLSBufferDisabled = true; want false")
	}
	if mtv.Origin == "" {
		t.Errorf("Origin should be populated by streams.Catalog() upstream")
	}
}

func TestCatalogManager_Providers_AbsentCfgEntryDefaultsToEnabled(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	// Freshly New()'d adapter has cfg.Providers == nil from
	// DefaultConfig(); no seeding needed.
	m := &catalogManager{adapter: a, adapterSaver: nil}

	got := m.Providers()
	for _, p := range got {
		if !p.Enabled {
			t.Errorf("provider %q Enabled = false; want true (zero-value ProviderConfig)", p.ID)
		}
	}
}

// newStreamsForCatalogTest constructs a streams.Adapter via the public
// New() API. The local-only bootstrap inside
// streams.Adapter.chassisCatalogSnapshot (catalog.go:74-80) populates
// Catalog() without requiring Start(), so this is safe for unit tests.
func newStreamsForCatalogTest(t *testing.T) *streams.Adapter {
	t.Helper()
	a, err := streams.New(streams.AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	return a
}
```

The tests use `chassis.CatalogProviderState` directly — the production wrapper's `Providers()` return type already matches the chassis-owned struct declared in Task 7.

- [ ] **Step 2: Run; expect FAIL.**

Run: `go test ./cmd/mister-groovy-relay -run TestCatalogManager_Providers -v`
Expected: failures on `catalogManager` undefined and the placeholder `t.Fatalf`.

- [ ] **Step 3: Create `cmd/mister-groovy-relay/catalog_manager.go` with the type and `Providers()` only.**

```go
package main

import (
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// catalogManager satisfies chassis.CatalogSettingsManager from outside
// internal/chassis. It composes the streams adapter (read snapshot +
// apply value + drop active cast) and the shared uiserver.AdapterSaver
// (atomic [adapters.streams] section rewrite under the bridge mutex).
type catalogManager struct {
	adapter      *streams.Adapter
	adapterSaver *uiserver.AdapterSaver
}

func (m *catalogManager) Providers() []chassis.CatalogProviderState {
	cfg := m.adapter.ConfigSnapshot()
	cat := m.adapter.Catalog()
	out := make([]chassis.CatalogProviderState, 0, len(cat))
	for _, p := range cat {
		channels := 0
		for _, g := range p.Groups {
			channels += len(g.Channels)
		}
		pc := cfg.Providers[p.ID]
		out = append(out, chassis.CatalogProviderState{
			ID:                p.ID,
			DisplayName:       p.DisplayName,
			BadgeLabel:        p.BadgeLabel,
			BadgeClass:        p.BadgeClass,
			Origin:            p.Origin,
			Kind:              p.Kind,
			DefaultChannel:    p.DefaultChannel,
			Live:              p.Live,
			ChannelCount:      channels,
			Enabled:           !pc.Disabled,
			HLSBufferDisabled: pc.HLSBufferDisabled,
		})
	}
	return out
}

var _ = adapters.ScopeHotSwap // imported for use in later tasks
```

- [ ] **Step 4: Run; expect PASS for `Providers()` tests.**

Run: `go test ./cmd/mister-groovy-relay -run TestCatalogManager_Providers -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add cmd/mister-groovy-relay/catalog_manager.go cmd/mister-groovy-relay/catalog_manager_test.go
git commit -m "feat(cmd): catalogManager.Providers wraps streams adapter

Production satisfier of chassis.CatalogSettingsManager.Providers().
Reads streams.Adapter.ConfigSnapshot() + Catalog() and projects each
provider into the chassis CatalogProviderState shape. Mutation methods
(UpdateProvider, SetDirectStreamHLSBuffer) land in the next task."
```

---

## Task 11: Implement `catalogManager.UpdateProvider`, `SetDirectStreamHLSBuffer`, and `reportAndDispatch`

**Files:**
- Modify: `cmd/mister-groovy-relay/catalog_manager.go`
- Modify: `cmd/mister-groovy-relay/catalog_manager_test.go`

Spec reference: §Architecture — Production Wrappers, including the declared-scope floor logic.

- [ ] **Step 1: Write the failing tests.**

NB: `streams.Adapter.cfg` is unexported; cmd-side tests must seed provider state via the public API (`ConfigSnapshot` + `ApplyConfigValue`). Task 10 already added this helper near the top of `catalog_manager_test.go`; keep it and reuse it here:

```go
// seedProviderCfg mutates the streams adapter's per-provider config
// via the public ApplyConfigValue path so cmd-side tests can stage
// state without touching unexported fields.
func seedProviderCfg(t *testing.T, a *streams.Adapter, providers map[string]streams.ProviderConfig) {
	t.Helper()
	cfg := a.ConfigSnapshot()
	cfg.Providers = providers
	if _, err := a.ApplyConfigValue(cfg, func(name string, raw []byte) error { return nil }); err != nil {
		t.Fatalf("seedProviderCfg: %v", err)
	}
}
```

Then the tests:

```go
func TestCatalogManager_UpdateProvider_EnabledOnly_HotScope(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"mtv-rewind": {Disabled: false},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	enabled := false
	scope, err := m.UpdateProvider("mtv-rewind", chassis.CatalogProviderPatch{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v; want ScopeHotSwap", scope)
	}
	if !a.ConfigSnapshot().Providers["mtv-rewind"].Disabled {
		t.Errorf("in-memory Disabled = false; want true")
	}
}

func TestCatalogManager_UpdateProvider_HLSOnly_RecastScope(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"toonami-aftermath": {HLSBufferDisabled: false},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v; want ScopeRestartCast (declared floor)", scope)
	}
	if !a.ConfigSnapshot().Providers["toonami-aftermath"].HLSBufferDisabled {
		t.Errorf("in-memory HLSBufferDisabled = false; want true")
	}
}

func TestCatalogManager_UpdateProvider_BothFields_MaxWinsRecast(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"toonami-aftermath": {},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	enabled := true
	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath",
		chassis.CatalogProviderPatch{Enabled: &enabled, HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v; want ScopeRestartCast (max-wins)", scope)
	}
}

func TestCatalogManager_UpdateProvider_NoopHLSStillReportsRecast(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"toonami-aftermath": {HLSBufferDisabled: false},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	// Set HLSBufferDisabled to its current value — no diff.
	disabled := false
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("no-op HLS save scope = %v; want ScopeRestartCast (declared floor)", scope)
	}
}

func TestCatalogManager_SetDirectStreamHLSBuffer_FlipsOnlyLive(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	scope, err := m.SetDirectStreamHLSBuffer(true)
	if err != nil {
		t.Fatalf("SetDirectStreamHLSBuffer: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v; want ScopeRestartCast", scope)
	}
	// Only Live providers should be flipped.
	cfg := a.ConfigSnapshot()
	for _, p := range a.Catalog() {
		got := cfg.Providers[p.ID].HLSBufferDisabled
		if p.Live && !got {
			t.Errorf("Live provider %q HLSBufferDisabled = false; want true", p.ID)
		}
		if !p.Live && got {
			t.Errorf("Non-Live provider %q HLSBufferDisabled = true; want false", p.ID)
		}
	}
}

// (No "zero Live providers" test — the bundled manifest always includes
// one Live provider via newStreamsForCatalogTest. The declared-scope
// floor's no-diff RECAST path is covered by the no-op HLS test above.)

// tmpConfigPath writes a minimal valid config.toml fixture and returns
// the path. Cleanup is handled by t.TempDir().
func tmpConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body, err := config.DefaultConfigTOML(dir)
	if err != nil {
		t.Fatalf("seed default config: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}
	return path
}

// The bundled manifest always contains one Live provider
// (toonami-aftermath), so a "zero Live providers" fixture is not
// reachable through the public streams.New API. Instead, drop the
// TestCatalogManager_SetDirectStreamHLSBuffer_ZeroLiveStillRecast
// test and rely on TestCatalogManager_UpdateProvider_NoopHLSStillReportsRecast
// to exercise the declared-scope floor's no-diff RECAST path — the
// floor logic is independent of how many providers participate in the
// save, so the assertion is equivalent.
```

Add required imports: `"sync"`, `"path/filepath"`, `"os"`, `"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"`, `"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"`.

- [ ] **Step 2: Run; expect FAIL.**

Run: `go test ./cmd/mister-groovy-relay -run "TestCatalogManager_(UpdateProvider|SetDirectStream)" -v`
Expected: `UpdateProvider` and `SetDirectStreamHLSBuffer` undefined.

- [ ] **Step 3: Implement the mutation methods + helpers in `catalog_manager.go`.**

Append:

```go
func (m *catalogManager) UpdateProvider(id string, patch chassis.CatalogProviderPatch) (adapters.ApplyScope, error) {
	scope, err := m.patch(func(cfg *streams.Config) {
		ensureProvider(cfg, id)
		pc := cfg.Providers[id]
		if patch.Enabled != nil {
			pc.Disabled = !*patch.Enabled
		}
		if patch.HLSBufferDisabled != nil {
			pc.HLSBufferDisabled = *patch.HLSBufferDisabled
		}
		cfg.Providers[id] = pc
	})
	if err != nil {
		return 0, err
	}
	return m.reportAndDispatch(scope, declaredProviderScope(patch))
}

func (m *catalogManager) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
	cat := m.adapter.Catalog()
	scope, err := m.patch(func(cfg *streams.Config) {
		if cfg.Providers == nil {
			cfg.Providers = map[string]streams.ProviderConfig{}
		}
		for _, p := range cat {
			if !p.Live {
				continue
			}
			pc := cfg.Providers[p.ID]
			pc.HLSBufferDisabled = disabled
			cfg.Providers[p.ID] = pc
		}
	})
	if err != nil {
		return 0, err
	}
	return m.reportAndDispatch(scope, adapters.ScopeRestartCast)
}

func (m *catalogManager) patch(apply func(*streams.Config)) (adapters.ApplyScope, error) {
	cfg := m.adapter.ConfigSnapshot()
	apply(&cfg)
	return m.adapter.ApplyConfigValue(cfg, m.adapterSaver.Save)
}

// reportAndDispatch floors actual at the declared scope and dispatches
// the RECAST runtime side effect when the reported scope is RECAST.
// Runs only after the save/apply succeeds, so a failed write or
// rejected streams snapshot never drops the active cast.
func (m *catalogManager) reportAndDispatch(actual, floor adapters.ApplyScope) (adapters.ApplyScope, error) {
	reported := maxScope(actual, floor)
	if reported == adapters.ScopeRestartCast {
		if err := m.adapter.StopActiveCast(); err != nil {
			return reported, err
		}
	}
	return reported, nil
}

// declaredProviderScope returns the max-wins declared scope across the
// patch's non-nil fields.
func declaredProviderScope(patch chassis.CatalogProviderPatch) adapters.ApplyScope {
	s := adapters.ApplyScope(0)
	if patch.Enabled != nil {
		s = maxScope(s, adapters.ScopeHotSwap)
	}
	if patch.HLSBufferDisabled != nil {
		s = maxScope(s, adapters.ScopeRestartCast)
	}
	return s
}

func maxScope(a, b adapters.ApplyScope) adapters.ApplyScope {
	if a > b {
		return a
	}
	return b
}

func ensureProvider(cfg *streams.Config, id string) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]streams.ProviderConfig{}
	}
	if _, ok := cfg.Providers[id]; !ok {
		cfg.Providers[id] = streams.ProviderConfig{}
	}
}
```

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./cmd/mister-groovy-relay -run "TestCatalogManager_(UpdateProvider|SetDirectStream)" -v`
Expected: PASS for all subtests.

- [ ] **Step 5: Run race detector.**

Run: `go test -race ./cmd/mister-groovy-relay`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add cmd/mister-groovy-relay/catalog_manager.go cmd/mister-groovy-relay/catalog_manager_test.go
git commit -m "feat(cmd): catalogManager mutations + declared-scope floor

UpdateProvider applies an optional-field patch in one snapshot/apply
cycle. SetDirectStreamHLSBuffer flips every Live provider. Both honor
the declared-scope floor — no-op HLS saves still report RECAST so the
wire contract guarantees match what configChangeScope can decide. The
reportAndDispatch helper invokes streams.Adapter.StopActiveCast after a
successful RECAST save to honor the operator's \"change immediately\"
intent."
```

---

## Task 12: Implement `cmd/mister-groovy-relay/config_reset.go`

**Files:**
- Create: `cmd/mister-groovy-relay/config_reset.go`
- Create: `cmd/mister-groovy-relay/config_reset_test.go`

Spec reference: §Architecture — Production Wrappers (`config_reset.go`), including the deadlock-avoidance discussion.

- [ ] **Step 1: Write the failing test.**

Create `cmd/mister-groovy-relay/config_reset_test.go`:

```go
package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestConfigReset_ResetToDefaults_WritesDefaultTOMLPreservingDataDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// Seed a non-default config.
	if err := os.WriteFile(path, []byte("# non-default content\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sec := &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/custom/data/dir"}}
	r := &configReset{path: path, mu: &sync.Mutex{}, sectioned: sec}

	if err := r.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	want, err := config.DefaultConfigTOML("/custom/data/dir")
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("disk content differs from DefaultConfigTOML(/custom/data/dir)")
	}
}

func TestConfigReset_ResetToDefaults_DataDirEmptyFallsThroughToPlatformDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("# initial\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sec := &config.Sectioned{Bridge: config.BridgeConfig{DataDir: ""}}
	r := &configReset{path: path, mu: &sync.Mutex{}, sectioned: sec}

	if err := r.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) == "# initial\n" {
		t.Errorf("disk was not overwritten")
	}
}

func TestConfigReset_ResetToDefaults_DiskFailureReturnsChipError(t *testing.T) {
	r := &configReset{
		path:      "/nonexistent/dir/config.toml",
		mu:        &sync.Mutex{},
		sectioned: &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/x"}},
	}

	err := r.ResetToDefaults()
	if err == nil {
		t.Fatalf("expected disk error; got nil")
	}
	// configResetError implements the structural interface via:
	//   Error() string; Unwrap() error; StatusCode() int; Chip() string
	// Verify the interface contract directly.
	if codeErr, ok := err.(interface{ StatusCode() int }); !ok {
		t.Errorf("error does not satisfy StatusCode() int; got %T", err)
	} else if codeErr.StatusCode() != http.StatusInternalServerError {
		t.Errorf("StatusCode() = %d; want 500", codeErr.StatusCode())
	}
	if chipErr, ok := err.(interface{ Chip() string }); !ok {
		t.Errorf("error does not satisfy Chip() string; got %T", err)
	} else if chipErr.Chip() != "WRITE FAILED" {
		t.Errorf("Chip() = %q; want \"WRITE FAILED\"", chipErr.Chip())
	}
}

func TestConfigReset_DoesNotTouchDataDirContents(t *testing.T) {
	dataDir := t.TempDir()
	sentinel := filepath.Join(dataDir, "device_uuid")
	if err := os.WriteFile(sentinel, []byte("uuid-from-before-reset"), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("# initial\n"), 0o644); err != nil {
		t.Fatalf("seed cfg: %v", err)
	}

	sec := &config.Sectioned{Bridge: config.BridgeConfig{DataDir: dataDir}}
	r := &configReset{path: cfgPath, mu: &sync.Mutex{}, sectioned: sec}
	if err := r.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}

	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(got) != "uuid-from-before-reset" {
		t.Errorf("data_dir sentinel was disturbed; got %q", string(got))
	}
}

func TestConfigReset_ResetToDefaults_CompletesWithoutSelfDeadlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("# initial\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := &configReset{
		path:      path,
		mu:        &sync.Mutex{},
		sectioned: &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/x"}},
	}

	done := make(chan error, 1)
	go func() { done <- r.ResetToDefaults() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ResetToDefaults: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("ResetToDefaults timed out; possible self-deadlock on shared mutex")
	}
}
```

Atomic tempfile/rename failure semantics are covered by `internal/config/atomic_test.go`; this wrapper test suite verifies that `configReset` composes `config.WriteAtomic` and serializes through the shared mutex without self-deadlocking.

- [ ] **Step 2: Run; expect FAIL because `configReset` does not exist.**

Run: `go test ./cmd/mister-groovy-relay -run TestConfigReset -v`
Expected: `configReset undefined`.

- [ ] **Step 3: Create `cmd/mister-groovy-relay/config_reset.go`.**

```go
package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// configReset satisfies chassis.ConfigReset from outside
// internal/chassis. It composes:
//   - path: the on-disk config.toml location
//   - mu:   BridgeSaver.Mu() — shared with bridge + adapter writes so a
//           reset cannot interleave with a partial save
//   - sectioned: a pointer to the live config snapshot, used to read
//                the operator's current data_dir for preservation
//                through the reset. Read under the already-held mu;
//                does NOT call bridgeSaver.Current() because Current()
//                acquires the same mutex internally and would deadlock.
type configReset struct {
	path      string
	mu        *sync.Mutex
	sectioned *config.Sectioned
}

func (r *configReset) ResetToDefaults() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Preserve the operator's current data_dir so persistent state
	// (device UUID, plex.tv token, streams cache, .first-run-complete)
	// stays chained to the post-reset config. Read sectioned.Bridge
	// directly while holding BridgeSaver.Mu(); do NOT call
	// BridgeSaver.Current() here, because Current() takes the same
	// mutex and would deadlock.
	dataDir := ""
	if r.sectioned != nil {
		dataDir = r.sectioned.Bridge.DataDir
	}
	rendered, err := config.DefaultConfigTOML(dataDir)
	if err != nil {
		return &configResetError{cause: fmt.Errorf("render defaults: %w", err)}
	}
	if err := config.WriteAtomic(r.path, rendered); err != nil {
		return &configResetError{cause: fmt.Errorf("write: %w", err)}
	}
	return nil
}

// configResetError satisfies chassis.settingsChipError so disk failures
// map to {chip:"WRITE FAILED"} via the existing chassis errors.As path.
type configResetError struct{ cause error }

func (e *configResetError) Error() string   { return e.cause.Error() }
func (e *configResetError) Unwrap() error   { return e.cause }
func (e *configResetError) StatusCode() int { return http.StatusInternalServerError }
func (e *configResetError) Chip() string    { return "WRITE FAILED" }

```

If `config.WriteAtomic` does not exist in this codebase, locate the actual atomic-write helper in `internal/config/` and use its name. The spec consistently references `config.WriteAtomic`; verify before finalizing this task with `rg -n "WriteAtomic\\b|atomic" internal/config`.

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./cmd/mister-groovy-relay -run TestConfigReset -v`
Expected: PASS for all five subtests.

- [ ] **Step 5: Run race detector.**

Run: `go test -race ./cmd/mister-groovy-relay`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add cmd/mister-groovy-relay/config_reset.go cmd/mister-groovy-relay/config_reset_test.go
git commit -m "feat(cmd): configReset wrapper for restore-defaults action

Satisfies chassis.ConfigReset. Reads current data_dir from injected
*config.Sectioned (not BridgeSaver.Current — would self-deadlock on
the shared mutex), renders defaults via the new
config.DefaultConfigTOML helper, writes atomically. data_dir
contents are untouched so device UUID + plex token + streams cache
stay chained to the post-reset config.

Disk failures return a typed *configResetError satisfying the chassis
settingsChipError structural interface (StatusCode=500, Chip=WRITE
FAILED), reusing 4A's chip envelope path."
```

---

## Task 13: Wire `catalogManager` and `configReset` in `cmd/mister-groovy-relay/main.go`

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

Spec reference: §Architecture — Production Wrappers (main.go wiring).

- [ ] **Step 1: Locate the chassis construction block.**

```bash
rg -n "chassis.New|chassis.Config|chassisCfg" cmd/mister-groovy-relay/main.go
```

- [ ] **Step 2: Construct the wrappers and assign them.**

After the existing `BridgeSaver` and `AdapterSaver` construction (search for `uiserver.NewAdapterSaver` to find the location), add:

```go
cm := &catalogManager{adapter: streamsAdapter, adapterSaver: adapterSaver}
cr := &configReset{path: cfgPath, mu: bridgeSaver.Mu(), sectioned: sectioned}
```

Where:
- `streamsAdapter` is the already-constructed `*streams.Adapter` (search for `streams.New(`)
- `adapterSaver` is the `*uiserver.AdapterSaver` instance
- `cfgPath` is the path to `config.toml` already passed to other savers
- `bridgeSaver` is the `*uiserver.BridgeSaver` instance
- `sectioned` is the `*config.Sectioned` loaded at startup (search for `config.LoadSectioned`)

Then in the `chassis.Config` literal:

```go
chassisCfg.CatalogManager = cm
chassisCfg.ConfigReset = cr
```

If the chassis is constructed by passing a `chassis.Config` value into `chassis.New(...)`, set the fields on the literal directly.

- [ ] **Step 3: Verify the binary builds.**

Run: `go build ./cmd/mister-groovy-relay`
Expected: no errors.

- [ ] **Step 4: Update the `session.go` `buildSettingsData` calls** (if not done in Task 8) to pass `s.cfg.CatalogManager` instead of `nil`.

```bash
rg -n "buildSettingsData\\(" internal/chassis/session.go
```

For each call site, replace the trailing `nil` with `s.cfg.CatalogManager`.

- [ ] **Step 5: Run the chassis tests to confirm nothing regressed.**

Run: `go test ./internal/chassis -race`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add cmd/mister-groovy-relay/main.go internal/chassis/session.go
git commit -m "wire(cmd): pass catalogManager and configReset into chassis.Config

Production wrappers built atop the existing *streams.Adapter,
*uiserver.AdapterSaver, *uiserver.BridgeSaver, and *config.Sectioned
instances. SnapshotFromStatusView and idleSnapshot now receive a real
manager and the Catalog pane data populates."
```

---

## Task 14: Implement `handleSettingsCatalogProviderPost`

**Files:**
- Modify: `internal/chassis/settings.go`
- Test: `internal/chassis/settings_test.go` (extend)

Spec reference: §Wire Contract `POST /receiver/settings/catalog/provider/{id}`.

- [ ] **Step 1: Write the failing tests.**

Append to `internal/chassis/settings_test.go`:

```go
func TestHandleSettingsCatalogProviderPost_EnabledOnly_HotScope(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{
		providers: []CatalogProviderState{{ID: "mtv-rewind"}},
		scope:     adapters.ScopeHotSwap,
	}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/mtv-rewind", "enabled=false")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req.WithPathValue("id", "mtv-rewind"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "hot")
	if mgr.lastID != "mtv-rewind" {
		t.Errorf("UpdateProvider id = %q; want mtv-rewind", mgr.lastID)
	}
	if mgr.lastPatch.Enabled == nil || *mgr.lastPatch.Enabled != false {
		t.Errorf("patch.Enabled = %v; want &false", mgr.lastPatch.Enabled)
	}
	if mgr.lastPatch.HLSBufferDisabled != nil {
		t.Errorf("patch.HLSBufferDisabled = %v; want nil", mgr.lastPatch.HLSBufferDisabled)
	}
}

func TestHandleSettingsCatalogProviderPost_HLSOnly_RecastScope(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{
		providers: []CatalogProviderState{{ID: "toonami-aftermath"}},
		scope:     adapters.ScopeRestartCast,
	}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/toonami-aftermath", "hls_buffer_disabled=true")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req.WithPathValue("id", "toonami-aftermath"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "recast")
}

func TestHandleSettingsCatalogProviderPost_BothFields_RecastMaxWins(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{
		providers: []CatalogProviderState{{ID: "toonami-aftermath"}},
		scope:     adapters.ScopeRestartCast,
	}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/toonami-aftermath", "enabled=true&hls_buffer_disabled=true")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req.WithPathValue("id", "toonami-aftermath"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "recast")
}

func TestHandleSettingsCatalogProviderPost_UnknownProvider_404(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{
		providers: []CatalogProviderState{{ID: "mtv-rewind"}},
	}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/does-not-exist", "enabled=true")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req.WithPathValue("id", "does-not-exist"))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rr.Code)
	}
	if mgr.lastID != "" {
		t.Errorf("UpdateProvider should NOT be called; got id=%q", mgr.lastID)
	}
}

func TestHandleSettingsCatalogProviderPost_BadBool_400FieldError(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{providers: []CatalogProviderState{{ID: "mtv-rewind"}}}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/mtv-rewind", "enabled=maybe")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req.WithPathValue("id", "mtv-rewind"))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONFieldErrors(t, rr.Body.Bytes(), map[string]string{
		"enabled": "must be true or false",
	})
}

func TestHandleSettingsCatalogProviderPost_EmptyBody_BadInputChip(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{providers: []CatalogProviderState{{ID: "mtv-rewind"}}}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/mtv-rewind", "")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req.WithPathValue("id", "mtv-rewind"))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "BAD INPUT")
}

func TestHandleSettingsCatalogProviderPost_NilManager_NotReady503(t *testing.T) {
	srv := newTestServerForCatalog(t, nil)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/x", "enabled=true")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req.WithPathValue("id", "x"))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "NOT READY")
}

// Helpers — add near the existing test helpers.

type fakeCatalogManagerMutating struct {
	providers []CatalogProviderState
	scope     adapters.ApplyScope
	err       error
	lastID    string
	lastPatch CatalogProviderPatch
	lastDirectStreamDisabled *bool
}

func (f *fakeCatalogManagerMutating) Providers() []CatalogProviderState { return f.providers }
func (f *fakeCatalogManagerMutating) UpdateProvider(id string, patch CatalogProviderPatch) (adapters.ApplyScope, error) {
	f.lastID = id
	f.lastPatch = patch
	return f.scope, f.err
}
func (f *fakeCatalogManagerMutating) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
	f.lastDirectStreamDisabled = &disabled
	return f.scope, f.err
}

func newCatalogFormReq(t *testing.T, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func newTestServerForCatalog(t *testing.T, mgr CatalogSettingsManager) *Server {
	t.Helper()
	cfg := Config{CatalogManager: mgr}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// assertJSONField / assertJSONFieldErrors mirror the existing 4A test
// helpers; if they already exist, reuse them.
```

NB: `req.WithPathValue` is a hand-written helper that wraps `req` with `mux.SetPathValue` equivalent semantics for testing handlers directly. If the test pattern in this codebase uses `httptest.Server` with the real mux, swap the per-handler invocation for a full-mux POST.

- [ ] **Step 2: Run; expect FAIL because `handleSettingsCatalogProviderPost` does not exist.**

Run: `go test ./internal/chassis -run TestHandleSettingsCatalogProviderPost -v`
Expected: undefined-method failure.

- [ ] **Step 3: Implement the handler in `internal/chassis/settings.go`.**

Append:

```go
// handleSettingsCatalogProviderPost is the POST handler for
// /receiver/settings/catalog/provider/{id}. Accepts any subset of
// supported form keys (enabled, hls_buffer_disabled); missing keys
// mean "do not change that field." Returns 404 for an unknown id,
// 400 for bad bools or empty body, 503 if the manager is unwired.
func (s *Server) handleSettingsCatalogProviderPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CatalogManager == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	id := r.PathValue("id")
	// Resolve against the live provider snapshot — one call, used for
	// the rest of the request.
	known := s.cfg.CatalogManager.Providers()
	if !catalogContainsProvider(known, id) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "unknown provider",
		})
		return
	}

	var patch CatalogProviderPatch
	errs := map[string]string{}
	if vals, ok := r.PostForm["enabled"]; ok && len(vals) > 0 {
		v, err := decodeStrictBool(vals[0])
		if err != nil {
			errs["enabled"] = "must be true or false"
		} else {
			patch.Enabled = &v
		}
	}
	if vals, ok := r.PostForm["hls_buffer_disabled"]; ok && len(vals) > 0 {
		v, err := decodeStrictBool(vals[0])
		if err != nil {
			errs["hls_buffer_disabled"] = "must be true or false"
		} else {
			patch.HLSBufferDisabled = &v
		}
	}
	if len(errs) > 0 {
		writeSettingsFieldErrors(w, http.StatusBadRequest, errs)
		return
	}
	if patch.Enabled == nil && patch.HLSBufferDisabled == nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}

	scope, err := s.cfg.CatalogManager.UpdateProvider(id, patch)
	if err != nil {
		var ce settingsChipError
		if errors.As(err, &ce) {
			writeSettingsChip(w, ce.StatusCode(), ce.Chip())
			return
		}
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	label, ok := scopeLabel(scope)
	if !ok {
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	writeSettingsSuccess(w, label)
}

func catalogContainsProvider(providers []CatalogProviderState, id string) bool {
	for _, p := range providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

// decodeStrictBool accepts only "true" / "false" verbatim. The 4C wire
// contract is precise — no "1"/"0", no "yes"/"no", no case-insensitive
// matching.
func decodeStrictBool(s string) (bool, error) {
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("must be true or false")
	}
}
```

Also add a `writeJSON` helper if absent — or reuse the existing 4A/4B `writeSettingsChip`/`writeSettingsSuccess` pattern for the 404 case:

```go
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/chassis -run TestHandleSettingsCatalogProviderPost -v`
Expected: all subtests PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): POST /receiver/settings/catalog/provider/{id}

Accepts optional enabled and hls_buffer_disabled form keys; dispatches
through CatalogSettingsManager.UpdateProvider in one call.

Envelope:
- 200 {ok:true, scope:hot|recast} on success
- 400 errors:{enabled|hls_buffer_disabled:\"must be true or false\"}
- 400 chip:BAD INPUT (empty body / both keys missing)
- 404 error:unknown provider
- 503 chip:NOT READY (manager not wired)
- 5xx via settingsChipError if the manager returns a typed error"
```

---

## Task 15: Implement `handleSettingsCatalogDirectStreamHLSBufferPost`

**Files:**
- Modify: `internal/chassis/settings.go`
- Test: `internal/chassis/settings_test.go` (extend)

Spec reference: §Wire Contract `POST /receiver/settings/catalog/direct-stream-hls-buffer`.

- [ ] **Step 1: Write the failing tests.**

Append to `settings_test.go`:

```go
func TestHandleSettingsCatalogDirectStreamHLSBufferPost_Success(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{scope: adapters.ScopeRestartCast}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "disabled=true")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "recast")
	if mgr.lastDirectStreamDisabled == nil || *mgr.lastDirectStreamDisabled != true {
		t.Errorf("manager.SetDirectStreamHLSBuffer arg = %v; want &true", mgr.lastDirectStreamDisabled)
	}
}

func TestHandleSettingsCatalogDirectStreamHLSBufferPost_BadBool_400(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "disabled=maybe")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONFieldErrors(t, rr.Body.Bytes(), map[string]string{
		"disabled": "must be true or false",
	})
}

func TestHandleSettingsCatalogDirectStreamHLSBufferPost_EmptyBody_BadInput(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "BAD INPUT")
}

func TestHandleSettingsCatalogDirectStreamHLSBufferPost_NilManager_503(t *testing.T) {
	srv := newTestServerForCatalog(t, nil)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "disabled=false")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "NOT READY")
}
```

- [ ] **Step 2: Run; expect FAIL.**

Run: `go test ./internal/chassis -run TestHandleSettingsCatalogDirectStream -v`
Expected: undefined.

- [ ] **Step 3: Implement the handler.**

Append to `internal/chassis/settings.go`:

```go
func (s *Server) handleSettingsCatalogDirectStreamHLSBufferPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CatalogManager == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	vals, ok := r.PostForm["disabled"]
	if !ok || len(vals) == 0 {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	v, err := decodeStrictBool(vals[0])
	if err != nil {
		writeSettingsFieldErrors(w, http.StatusBadRequest, map[string]string{
			"disabled": "must be true or false",
		})
		return
	}

	scope, err := s.cfg.CatalogManager.SetDirectStreamHLSBuffer(v)
	if err != nil {
		var ce settingsChipError
		if errors.As(err, &ce) {
			writeSettingsChip(w, ce.StatusCode(), ce.Chip())
			return
		}
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	label, ok := scopeLabel(scope)
	if !ok {
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	writeSettingsSuccess(w, label)
}
```

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/chassis -run TestHandleSettingsCatalogDirectStream -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): POST /receiver/settings/catalog/direct-stream-hls-buffer

Global HLS-override toggle handler. Requires the disabled key (true|false);
dispatches CatalogSettingsManager.SetDirectStreamHLSBuffer which flips
hls_buffer_disabled on every Live provider in one save. Same envelope
shape as the per-provider route, minus the 404."
```

---

## Task 16: Implement `handleSettingsActionRestoreDefaults` and mount three routes

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/server.go` (route mounts)
- Test: `internal/chassis/settings_test.go` (extend)

Spec reference: §Wire Contract `POST /receiver/settings/action/restore-defaults` + `Server.RegisterRoutes` extension.

- [ ] **Step 1: Write the failing tests.**

Append to `settings_test.go`:

```go
func TestHandleSettingsActionRestoreDefaults_Success(t *testing.T) {
	cr := &fakeConfigReset{}
	srv := newTestServerForReset(t, cr)

	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/restore-defaults", nil)
	rr := httptest.NewRecorder()
	srv.handleSettingsActionRestoreDefaults(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "reboot")
	if cr.calls != 1 {
		t.Errorf("ResetToDefaults called %d times; want 1", cr.calls)
	}
}

func TestHandleSettingsActionRestoreDefaults_NilReset_503(t *testing.T) {
	srv := newTestServerForReset(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/restore-defaults", nil)
	rr := httptest.NewRecorder()
	srv.handleSettingsActionRestoreDefaults(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "NOT READY")
}

func TestHandleSettingsActionRestoreDefaults_ChipError(t *testing.T) {
	cr := &fakeConfigReset{err: &fakeChipErr{status: 500, chip: "WRITE FAILED"}}
	srv := newTestServerForReset(t, cr)

	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/restore-defaults", nil)
	rr := httptest.NewRecorder()
	srv.handleSettingsActionRestoreDefaults(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "WRITE FAILED")
}

type fakeConfigReset struct {
	calls int
	err   error
}

func (f *fakeConfigReset) ResetToDefaults() error {
	f.calls++
	return f.err
}

type fakeChipErr struct {
	status int
	chip   string
}

func (e *fakeChipErr) Error() string   { return e.chip }
func (e *fakeChipErr) StatusCode() int { return e.status }
func (e *fakeChipErr) Chip() string    { return e.chip }

func newTestServerForReset(t *testing.T, cr ConfigReset) *Server {
	t.Helper()
	srv, err := New(Config{ConfigReset: cr})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}
```

- [ ] **Step 2: Run; expect FAIL.**

Run: `go test ./internal/chassis -run TestHandleSettingsActionRestoreDefaults -v`
Expected: undefined.

- [ ] **Step 3: Implement the handler in `internal/chassis/settings.go`.**

Append:

```go
// handleSettingsActionRestoreDefaults is the POST handler for
// /receiver/settings/action/restore-defaults. Empty body. Returns
// success with scope:"reboot" so the client toasts the dedicated
// "Defaults restored — restart container to apply" message via the
// 4A REBOOT toast helper.
func (s *Server) handleSettingsActionRestoreDefaults(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ConfigReset == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := s.cfg.ConfigReset.ResetToDefaults(); err != nil {
		var ce settingsChipError
		if errors.As(err, &ce) {
			writeSettingsChip(w, ce.StatusCode(), ce.Chip())
			return
		}
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	writeSettingsSuccess(w, "reboot")
}
```

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/chassis -run TestHandleSettingsActionRestoreDefaults -v`
Expected: PASS.

- [ ] **Step 5: Mount the three new routes in `RegisterRoutes`.**

Open `internal/chassis/server.go` and find the existing route mounts (`rg -n "mux.Handle.*settings" internal/chassis/server.go`). Add:

```go
mux.Handle("POST /receiver/settings/catalog/provider/{id}",
	requireSameOrigin(http.HandlerFunc(s.handleSettingsCatalogProviderPost)))
mux.Handle("POST /receiver/settings/catalog/direct-stream-hls-buffer",
	requireSameOrigin(http.HandlerFunc(s.handleSettingsCatalogDirectStreamHLSBufferPost)))
mux.Handle("POST /receiver/settings/action/restore-defaults",
	requireSameOrigin(http.HandlerFunc(s.handleSettingsActionRestoreDefaults)))
```

- [ ] **Step 6: Run the chassis package tests + race.**

Run: `go test -race ./internal/chassis`
Expected: PASS.

- [ ] **Step 7: Commit.**

```bash
git add internal/chassis/settings.go internal/chassis/server.go internal/chassis/settings_test.go
git commit -m "feat(chassis): restore-defaults handler + mount all three 4C routes

POST /receiver/settings/action/restore-defaults returns {ok:true,
scope:\"reboot\"} on success — reuses the 4A bridge-save REBOOT
envelope so the client toast helper is identical.

Routes mounted under requireSameOrigin matching the 4A/4B convention:
  - POST /receiver/settings/catalog/provider/{id}
  - POST /receiver/settings/catalog/direct-stream-hls-buffer
  - POST /receiver/settings/action/restore-defaults"
```

---

## Task 17: Add chassis CSS for `.provider-row`, `.single-col`, `.confirming`, button variants

**Files:**
- Modify: `internal/chassis/static/chassis.css`

Spec reference: §Architecture — Templates "CSS additions in internal/chassis/static/chassis.css".

- [ ] **Step 1: Locate the existing 4A/4B CSS block scoped under `body.receiver`.**

```bash
rg -n -m 5 "body\\.receiver" internal/chassis/static/chassis.css
```

- [ ] **Step 2: Append the new rules.** Place them after the existing 4A settings-panel block so cascade order matches.

```css
/* 4C: bundled-provider rows in the Catalog pane — ported verbatim
   from the v24 mockup, lines 2098-2121. */
body.receiver .settings-panel .provider-row {
  display: grid;
  grid-template-columns: 56px 1fr auto auto;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 1px solid #2b2b2f;
}
body.receiver .settings-panel .provider-row:last-child { border-bottom: 0; }
body.receiver .settings-panel .provider-row .icon {
  background: #1a1a1d;
  color: oklch(0.62 0.06 200);
  font: 700 11px 'DSEG14-Classic', monospace;
  text-align: center;
  padding: 6px 8px;
  border-radius: 2px;
  letter-spacing: 0.06em;
}
body.receiver .settings-panel .provider-row .icon.cartoon { color: oklch(0.62 0.06 80); }
body.receiver .settings-panel .provider-row .icon.toonami { color: oklch(0.62 0.06 280); }
body.receiver .settings-panel .provider-row .meta .name {
  font: 600 12px 'Inter', sans-serif;
  color: #d4d4d8;
}
body.receiver .settings-panel .provider-row .meta .stat {
  font: 400 10px 'DSEG14-Classic', monospace;
  color: var(--vfd-dim);
  margin-top: 2px;
  letter-spacing: 0.08em;
}
body.receiver .settings-panel .provider-row .meta .stat code {
  font-family: inherit;
  color: var(--vfd);
  background: transparent;
}
body.receiver .settings-panel .provider-row .channel-count {
  font: 700 11px 'DSEG7-Classic', monospace;
  color: var(--vfd);
  text-shadow: 0 0 3px var(--vfd-glow-soft);
}

/* 4C: single-column pane override (Catalog pane uses .single-col). */
body.receiver .settings-pane.single-col.active { grid-template-columns: 1fr; }

/* 4C: inline-confirm state for the destructive restore-defaults action. */
body.receiver .settings-panel .field-row.confirming > .scope { visibility: hidden; }
body.receiver .settings-panel .field-row.confirming > div {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
body.receiver .settings-panel .confirm-prompt {
  font-size: 11px;
  color: var(--vfd-dim);
}
body.receiver .settings-panel .action-btn.cancel {
  /* uses the default .action-btn cyan border + neutral text */
}
body.receiver .settings-panel .action-btn.confirm {
  color: oklch(0.78 0.16 25);
  border-color: oklch(0.40 0.16 25);
}
```

- [ ] **Step 3: Verify the file parses (no Go-side test gate, but the chassis css preprocessor runs `text/template`; build and ensure it doesn't choke).**

Run: `go test ./internal/chassis -run TestCSSPreprocess -v` (if such a test exists). Otherwise: `go build ./internal/chassis`.
Expected: no errors.

- [ ] **Step 4: Commit.**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port provider-row CSS + inline-confirm styles

Catalog pane bundled-provider rows verbatim from v24 mockup
(.provider-row family, .icon variants for cartoon/toonami, .channel-count
in DSEG7). Single-col pane override. Inline-confirm two-step pattern
(.field-row.confirming hides the trailing scope badge; .action-btn.cancel
and .action-btn.confirm variants for the restore-defaults row).

All rules scoped under body.receiver per chassis convention."
```

---

## Task 18: Create `settings-catalog.html` template and swap the drawer stub

**Files:**
- Create: `internal/chassis/templates/settings-catalog.html`
- Modify: `internal/chassis/templates/settings-drawer.html`
- Test: `internal/chassis/chassis_test.go` (extend)

Spec reference: §Architecture — Templates "New: `internal/chassis/templates/settings-catalog.html`" + drawer edit.

- [ ] **Step 1: Write the failing template render tests.**

Append to `internal/chassis/chassis_test.go`:

```go
func TestRenderCatalogPane_ProvidersRendered(t *testing.T) {
	data := SettingsData{
		CatalogPaneProviderCount: 3,
		CatalogChannelCount:      90,
		CatalogProviders: []CatalogProviderState{
			{ID: "mtv-rewind", DisplayName: "MTV Rewind", BadgeLabel: "MTV", BadgeClass: "",
				Origin: "wantmymtv.vercel.app", Kind: "youtube-channel-json",
				DefaultChannel: "1stday", ChannelCount: 73, Enabled: true},
			{ID: "cartoon-rewind", DisplayName: "Cartoon Rewind", BadgeLabel: "CART", BadgeClass: "cartoon",
				Origin: "cartoonrewind.tv", Kind: "youtube-channel-json",
				DefaultChannel: "all", ChannelCount: 13, Enabled: true},
			{ID: "toonami-aftermath", DisplayName: "Toonami Aftermath", BadgeLabel: "TOON", BadgeClass: "toonami",
				Origin: "api.toonamiaftermath.com", Kind: "direct-streams",
				DefaultChannel: "", ChannelCount: 4, Live: true, Enabled: true,
				HLSBufferDisabled: false},
		},
		DirectStreamHLSBufferDisabled: false,
	}
	html := renderCatalogPane(t, data)

	wantContains := []string{
		`data-pane="catalog"`,
		`3 PROVIDERS · 90 CHANNELS`,
		`data-catalog-provider="mtv-rewind"`,
		`data-catalog-field="enabled"`,
		`data-catalog-direct-hls`,
		`wantmymtv.vercel.app · youtube-channel-json`,
		`<code>1stday</code>`,
		`73 CH`,
		`data-catalog-provider="toonami-aftermath"`,
		`<span class="scope recast">RECAST</span>`,
	}
	for _, want := range wantContains {
		if !strings.Contains(html, want) {
			t.Errorf("catalog pane HTML missing %q\n---\n%s\n---", want, html)
		}
	}

	// Critical: data-field MUST NOT appear on the catalog switches —
	// collision guard against the existing 4A handler at
	// internal/chassis/static/settings-drawer.js:187.
	if strings.Contains(html, `data-field="enabled"`) {
		t.Errorf("catalog switch must not carry data-field=enabled; would double-fire 4A handler")
	}
}

func TestRenderCatalogPane_DefaultChannelOmittedWhenEmpty(t *testing.T) {
	data := SettingsData{
		CatalogProviders: []CatalogProviderState{
			{ID: "toonami-aftermath", BadgeLabel: "TOON", BadgeClass: "toonami",
				Origin: "api.toonamiaftermath.com", Kind: "direct-streams",
				DefaultChannel: "", ChannelCount: 4, Live: true},
		},
	}
	html := renderCatalogPane(t, data)
	if strings.Contains(html, `default: <code>`) {
		t.Errorf("expected no `default:` segment when DefaultChannel is empty; got: %s", html)
	}
}

func TestRenderCatalogPane_EmptyProvidersStillRendersHLSSection(t *testing.T) {
	data := SettingsData{
		CatalogPaneProviderCount: 0,
		CatalogChannelCount:      0,
		CatalogProviders:         nil,
	}
	html := renderCatalogPane(t, data)
	if !strings.Contains(html, `0 PROVIDERS · 0 CHANNELS`) {
		t.Errorf("expected `0 PROVIDERS · 0 CHANNELS` heading; got: %s", html)
	}
	if !strings.Contains(html, `Per-provider HLS buffer override`) {
		t.Errorf("HLS override section should still render when no providers; got: %s", html)
	}
}

// renderCatalogPane executes the chassis template suite against the given
// SettingsData and returns the rendered settings-catalog block.
func renderCatalogPane(t *testing.T, data SettingsData) string {
	t.Helper()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-catalog", data); err != nil {
		t.Fatalf("execute settings-catalog: %v", err)
	}
	return buf.String()
}
```

- [ ] **Step 2: Run; expect FAIL because `settings-catalog` template does not exist.**

Run: `go test ./internal/chassis -run TestRenderCatalogPane -v`
Expected: template-not-defined error.

- [ ] **Step 3: Create `internal/chassis/templates/settings-catalog.html`.**

```html
{{- define "settings-catalog" -}}
{{htmlComment "chassis:settings-catalog"}}
<div class="settings-pane single-col" data-pane="catalog">

  <div class="settings-section wide">
    <h4>Bundled providers <span class="hint">{{ .CatalogPaneProviderCount }} PROVIDERS · {{ .CatalogChannelCount }} CHANNELS</span></h4>
    {{ range .CatalogProviders }}
      {{ template "settings-catalog-provider-row" . }}
    {{ end }}
  </div>

  <div class="settings-section wide">
    <h4>Per-provider HLS buffer override</h4>
    <div class="field-row" id="direct-stream-hls-buffer-row">
      <label>Disable HLS buffer for direct-stream providers <span class="help">Toonami Aftermath etc. bypass the shared HLS cache. Safer for live feeds with strict origin policies.</span></label>
      <div></div>
      <span class="row-end">
        <button class="switch{{ if .DirectStreamHLSBufferDisabled }} on{{ end }}"
                type="button"
                data-catalog-direct-hls
                aria-pressed="{{ .DirectStreamHLSBufferDisabled }}"></button>
        <span class="scope recast">RECAST</span>
      </span>
    </div>
  </div>

</div>
{{- end -}}

{{- define "settings-catalog-provider-row" -}}
<div class="provider-row" data-provider="{{ .ID }}">
  <div class="icon{{ if .BadgeClass }} {{ .BadgeClass }}{{ end }}">{{ .BadgeLabel }}</div>
  <div class="meta">
    <div class="name">{{ .DisplayName }}</div>
    <div class="stat">{{ .Origin }} · {{ .Kind }}{{ if .DefaultChannel }} · default: <code>{{ .DefaultChannel }}</code>{{ end }}</div>
  </div>
  <div class="channel-count">{{ .ChannelCount }} CH</div>
  <button class="switch{{ if .Enabled }} on{{ end }}"
          type="button"
          data-catalog-provider="{{ .ID }}"
          data-catalog-field="enabled"
          aria-pressed="{{ .Enabled }}"></button>
</div>
{{- end -}}
```

- [ ] **Step 4: Edit `internal/chassis/templates/settings-drawer.html` to swap the stub.**

Replace the line `{{ template "settings-stub" (stub "catalog" "Streams catalog" "4C") }}` with:

```html
{{ template "settings-catalog" .Settings }}
```

- [ ] **Step 5: Run; expect PASS.**

Run: `go test ./internal/chassis -run TestRenderCatalogPane -v`
Expected: PASS for all subtests.

- [ ] **Step 6: Run the full chassis test suite (catches drawer-render regressions).**

Run: `go test -race ./internal/chassis`
Expected: PASS.

- [ ] **Step 7: Commit.**

```bash
git add internal/chassis/templates/settings-catalog.html internal/chassis/templates/settings-drawer.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): Catalog pane template + provider-row partial

DOM mirrors v24 mockup lines 4532-4582 verbatim. Provider rows carry
data-catalog-provider + data-catalog-field (deliberately NOT data-field,
to avoid colliding with the existing 4A bridge-switch handler). Global
HLS-override switch uses data-catalog-direct-hls. Section header counts
are template-driven from SettingsData."
```

---

## Task 19: Extend `settings-advanced.html` with the Diagnostics + restore-defaults row

**Files:**
- Modify: `internal/chassis/templates/settings-advanced.html`
- Test: `internal/chassis/chassis_test.go` (extend)

Spec reference: §Architecture — Templates "Edit: internal/chassis/templates/settings-advanced.html".

- [ ] **Step 1: Write the failing test.**

Append to `chassis_test.go`:

```go
func TestRenderAdvancedPane_DiagnosticsRestoreDefaults(t *testing.T) {
	data := SettingsData{Bridge: config.BridgeConfig{}}
	html := renderAdvancedPane(t, data)

	wantContains := []string{
		`<h4>Diagnostics <span class="hint">read-only</span></h4>`,
		`id="restore-defaults-row"`,
		`id="restore-defaults-btn"`,
		`⚠ Reset…`,
		`id="restore-defaults-result"`,
		`<span class="scope reboot">REBOOT</span>`,
		`config.toml`,
	}
	for _, want := range wantContains {
		if !strings.Contains(html, want) {
			t.Errorf("advanced pane HTML missing %q\n---\n%s\n---", want, html)
		}
	}
}

func renderAdvancedPane(t *testing.T, data SettingsData) string {
	t.Helper()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-advanced", data); err != nil {
		t.Fatalf("execute settings-advanced: %v", err)
	}
	return buf.String()
}
```

- [ ] **Step 2: Run; expect FAIL.**

Run: `go test ./internal/chassis -run TestRenderAdvancedPane_DiagnosticsRestoreDefaults -v`
Expected: failures on each missing substring.

- [ ] **Step 3: Append the Diagnostics section to `settings-advanced.html`.**

After the existing Logging section, before the closing `</div>` of the `settings-pane`:

```html
<div class="settings-section">
  <h4>Diagnostics <span class="hint">read-only</span></h4>
  <div class="field-row" id="restore-defaults-row">
    <label>Reset to defaults <span class="help">Rewrites <code>config.toml</code> with application defaults. Persisted state in <code>data_dir</code> (device UUID, Plex token, streams cache) is preserved. Requires a container restart to apply.</span></label>
    <div>
      <button class="action-btn"
              id="restore-defaults-btn"
              type="button"
              style="color:oklch(0.78 0.16 25);border-color:oklch(0.40 0.16 25);">⚠ Reset…</button>
      <div class="action-result" id="restore-defaults-result"></div>
    </div>
    <span class="scope reboot">REBOOT</span>
  </div>
</div>
```

- [ ] **Step 4: Run; expect PASS.**

Run: `go test ./internal/chassis -run TestRenderAdvancedPane_DiagnosticsRestoreDefaults -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/chassis/templates/settings-advanced.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): Diagnostics section with restore-defaults row in Advanced

First row of the Diagnostics section per v24 mockup lines 4654-4671;
activity-ring and build-info rows ship in Phase 5. Destructive button
styling (oklch red border) copied verbatim from mockup inline style.
Inline two-step confirm wiring is JS-side (Task 22)."
```

---

## Task 20: JS — provider-row switch handler

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

Spec reference: §Architecture — Client JS "Provider-row switches".

- [ ] **Step 1: Identify the existing JS structure.**

```bash
rg -n "drawer.querySelectorAll|button\\.switch" internal/chassis/static/settings-drawer.js
```

The 4A handler at line 187 binds `button.switch[data-field]`. The 4C handlers attach by different selectors (`data-catalog-provider`, `data-catalog-direct-hls`) so they coexist without selector overlap.

- [ ] **Step 2: Append the provider-row switch handler inside the IIFE (just below the existing 4A switch handler).**

```javascript
// 4C: provider-row switches. The catalog switches deliberately use
// data-catalog-field instead of data-field so the existing 4A bridge
// switch handler at this same file's button.switch[data-field] selector
// does NOT match — otherwise both handlers would fire on click and
// the 4A path would POST a stray enabled=true to /receiver/settings/bridge.
drawer.querySelectorAll('button.switch[data-catalog-provider]').forEach(el => {
  el.addEventListener('click', async () => {
    if (el.disabled) return;
    const id = el.dataset.catalogProvider;
    const field = el.dataset.catalogField;
    const next = !el.classList.contains('on');
    el.classList.toggle('on', next);
    el.setAttribute('aria-pressed', next ? 'true' : 'false');
    const form = new URLSearchParams();
    form.set(field, next ? 'true' : 'false');
    let body = {};
    try {
      const res = await fetch(`/receiver/settings/catalog/provider/${encodeURIComponent(id)}`, {
        method: 'POST', body: form, credentials: 'same-origin'
      });
      body = await res.json().catch(() => ({}));
      if (res.ok && body.ok) return;
    } catch (_) {
      body = { chip: 'WRITE FAILED' };
    }
    // Revert optimistic toggle.
    el.classList.toggle('on', !next);
    el.setAttribute('aria-pressed', !next ? 'true' : 'false');
    if (body.errors) {
      showNotice('BAD INPUT', 'err');
    } else if (body.error) {
      showNotice(body.error, 'err');
    } else if (body.chip) {
      showNotice(body.chip, 'err');
    } else {
      showNotice('WRITE FAILED', 'err');
    }
  });
});
```

The `showNotice` helper is the 4A drawer-local toast emitter — verify its signature matches what you call here by reading the existing file.

- [ ] **Step 3: Build and start the chassis (smoke-only — no JS unit tests today).**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 4: Commit.**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): JS handler for provider-row enabled switches

Click toggles .switch.on optimistically, POSTs enabled=true|false to
/receiver/settings/catalog/provider/{id}, reverts on failure, toasts
the error chip into the drawer-local notice slot. Selector keyed on
data-catalog-provider to avoid collision with the 4A bridge handler."
```

---

## Task 21: JS — global HLS-override switch handler

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

Spec reference: §Architecture — Client JS "Global HLS-override switch".

- [ ] **Step 1: Append the global HLS handler just below the provider-row handler.**

```javascript
// 4C: global HLS-override switch — single switch under the "Per-provider
// HLS buffer override" section. Flips hls_buffer_disabled on every Live
// provider in one save (server side). Same optimistic-toggle pattern
// as the per-provider switches.
const directHlsBtn = drawer.querySelector('button.switch[data-catalog-direct-hls]');
if (directHlsBtn) directHlsBtn.addEventListener('click', async () => {
  if (directHlsBtn.disabled) return;
  const next = !directHlsBtn.classList.contains('on');
  directHlsBtn.classList.toggle('on', next);
  directHlsBtn.setAttribute('aria-pressed', next ? 'true' : 'false');
  const form = new URLSearchParams();
  form.set('disabled', next ? 'true' : 'false');
  let body = {};
  try {
    const res = await fetch('/receiver/settings/catalog/direct-stream-hls-buffer', {
      method: 'POST', body: form, credentials: 'same-origin'
    });
    body = await res.json().catch(() => ({}));
    if (res.ok && body.ok) return;
  } catch (_) {
    body = { chip: 'WRITE FAILED' };
  }
  directHlsBtn.classList.toggle('on', !next);
  directHlsBtn.setAttribute('aria-pressed', !next ? 'true' : 'false');
  if (body.errors) {
    showNotice('BAD INPUT', 'err');
  } else if (body.chip) {
    showNotice(body.chip, 'err');
  } else {
    showNotice('WRITE FAILED', 'err');
  }
});
```

- [ ] **Step 2: Build.**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 3: Commit.**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): JS handler for global HLS-override switch

Click flips hls_buffer_disabled across every Live provider in one POST
to /receiver/settings/catalog/direct-stream-hls-buffer. Optimistic
toggle + revert on failure. Mixed-state-renders-as-off semantics live
server-side in buildSettingsData; the client toggle is destructive (a
click from mixed state synchronizes all live providers to the new
value) per spec."
```

---

## Task 22: JS — restore-defaults inline-confirm state machine

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

Spec reference: §5 of the design — Restore-defaults UX (inline two-step confirm), and §Architecture — Client JS.

- [ ] **Step 1: Append the inline-confirm IIFE at the end of `settings-drawer.js`.**

```javascript
// 4C: restore-defaults inline two-step confirm. Click ⚠ Reset… → button
// row morphs to "[This wipes config.toml.] [Cancel] [Confirm reset]";
// confirm POSTs; cancel or 10s armed timeout returns to idle.
// DOM construction uses createElement + textContent + replaceChildren
// rather than innerHTML — even though the prompt content is fully
// static, modeling safe patterns here prevents an implementer from
// later interpolating dynamic content into the same code path and
// reintroducing an XSS surface.
(function initRestoreDefaults() {
  const row = document.getElementById('restore-defaults-row');
  if (!row) return;
  const idleBtn = document.getElementById('restore-defaults-btn');
  const result = document.getElementById('restore-defaults-result');
  if (!idleBtn || !result) return;

  let armTimer = null;

  function toIdle() {
    if (armTimer) { clearTimeout(armTimer); armTimer = null; }
    row.classList.remove('confirming');
    const rightCell = idleBtn.parentElement;
    rightCell.replaceChildren(idleBtn, result);
    idleBtn.disabled = false;
  }

  function toArmed() {
    row.classList.add('confirming');
    const rightCell = idleBtn.parentElement;
    const prompt = document.createElement('span');
    prompt.className = 'confirm-prompt';
    prompt.textContent = 'This wipes config.toml. ';
    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'action-btn cancel';
    cancelBtn.type = 'button';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.addEventListener('click', toIdle);
    const confirmBtn = document.createElement('button');
    confirmBtn.className = 'action-btn confirm';
    confirmBtn.type = 'button';
    confirmBtn.textContent = 'Confirm reset';
    confirmBtn.addEventListener('click', fire);
    rightCell.replaceChildren(prompt, cancelBtn, confirmBtn, result);
    armTimer = setTimeout(toIdle, 10_000);
  }

  async function fire() {
    if (armTimer) { clearTimeout(armTimer); armTimer = null; }
    row.querySelectorAll('button').forEach(b => { b.disabled = true; });
    result.className = 'action-result';
    result.textContent = '';
    let body = {};
    try {
      const res = await fetch('/receiver/settings/action/restore-defaults', {
        method: 'POST', credentials: 'same-origin'
      });
      body = await res.json().catch(() => ({}));
      if (res.ok && body.ok && body.scope === 'reboot') {
        toIdle();
        result.className = 'action-result shown ok';
        result.textContent = '▸ Defaults restored · restart to apply';
        showNotice('Defaults restored — restart container to apply', 'ok');
        return;
      }
    } catch (_) {
      body = { chip: 'WRITE FAILED' };
    }
    toIdle();
    if (body.chip) {
      showNotice(body.chip, 'err');
    } else if (body.error) {
      result.className = 'action-result shown err';
      result.textContent = `▸ ERROR · ${body.error}`;
    } else {
      result.className = 'action-result shown err';
      result.textContent = '▸ ERROR · unknown';
    }
  }

  idleBtn.addEventListener('click', toArmed);
})();
```

- [ ] **Step 2: Build.**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 3: Commit.**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): restore-defaults inline two-step confirm

Idle → armed (button row morphs to Cancel/Confirm + prompt) → confirmed
fires POST → idle. 10-second armed timeout silently reverts. DOM
construction uses createElement + textContent + replaceChildren even
for static content — prevents an implementer from later interpolating
into innerHTML and introducing XSS."
```

---

## Task 23: Integration tests — chassis end-to-end

**Files:**
- Modify: `tests/integration/chassis_test.go` (extend with 4C cases)

Spec reference: §Testing — Integration tests.

- [ ] **Step 1: Identify the existing integration test scaffold.**

```bash
rg -n -m 10 "func TestChassis_|startTestBridge|startTestChassis" tests/integration/chassis_test.go
```

- [ ] **Step 2: Append the 4C integration cases (preserve the existing build tag at the top of the file).**

```go
//go:build integration

// ... existing imports + helpers ...

func TestChassis_CatalogPane_RendersProviderRows(t *testing.T) {
	ts := startTestChassis(t)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/receiver")
	if err != nil {
		t.Fatalf("GET /receiver: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	defer res.Body.Close()

	for _, want := range []string{
		`data-pane="catalog"`,
		`data-catalog-provider="mtv-rewind"`,
		`data-catalog-direct-hls`,
		`id="restore-defaults-btn"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("GET /receiver body missing %q", want)
		}
	}
}

func TestChassis_PostCatalogProvider_EnabledFalse_DiskAndMemoryUpdated(t *testing.T) {
	ts := startTestChassis(t)
	defer ts.Close()

	form := url.Values{"enabled": []string{"false"}}
	res, err := http.PostForm(ts.URL+"/receiver/settings/catalog/provider/mtv-rewind", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d; body %s", res.StatusCode, string(body))
	}

	// Verify on-disk TOML now contains [adapters.streams.providers.mtv-rewind] disabled = true
	toml := readConfigTOML(t, ts.CfgPath)
	if !strings.Contains(toml, "[adapters.streams.providers.mtv-rewind]") || !strings.Contains(toml, "disabled = true") {
		t.Errorf("disk TOML did not record mtv-rewind disabled = true; got:\n%s", toml)
	}
}

func TestChassis_PostCatalogProvider_HLSBufferDisabled_RecastScope(t *testing.T) {
	ts := startTestChassis(t)
	defer ts.Close()

	form := url.Values{"hls_buffer_disabled": []string{"true"}}
	res, err := http.PostForm(ts.URL+"/receiver/settings/catalog/provider/toonami-aftermath", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"recast"`) {
		t.Errorf("expected scope:recast; got: %s", string(body))
	}
}

func TestChassis_PostCatalogProvider_BothKeys_RecastMaxWins(t *testing.T) {
	ts := startTestChassis(t)
	defer ts.Close()

	form := url.Values{
		"enabled":             []string{"true"},
		"hls_buffer_disabled": []string{"true"},
	}
	res, err := http.PostForm(ts.URL+"/receiver/settings/catalog/provider/toonami-aftermath", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"recast"`) {
		t.Errorf("expected scope:recast (max-wins); got: %s", string(body))
	}
}

func TestChassis_PostCatalogDirectStreamHLS_FlipsLiveOnly(t *testing.T) {
	ts := startTestChassis(t)
	defer ts.Close()

	form := url.Values{"disabled": []string{"true"}}
	res, err := http.PostForm(ts.URL+"/receiver/settings/catalog/direct-stream-hls-buffer", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"recast"`) {
		t.Errorf("expected scope:recast; got: %s", string(body))
	}

	toml := readConfigTOML(t, ts.CfgPath)
	if !strings.Contains(toml, "[adapters.streams.providers.toonami-aftermath]") || !strings.Contains(toml, "hls_buffer_disabled = true") {
		t.Errorf("disk TOML did not record toonami-aftermath hls_buffer_disabled = true; got:\n%s", toml)
	}
	// MTV is not Live; must not be flipped.
	if strings.Contains(toml, "[adapters.streams.providers.mtv-rewind]") && strings.Contains(toml, "hls_buffer_disabled = true") {
		t.Errorf("non-Live provider mtv-rewind was incorrectly flipped")
	}
}

func TestChassis_PostCatalogProvider_UnknownID_404(t *testing.T) {
	ts := startTestChassis(t)
	defer ts.Close()
	form := url.Values{"enabled": []string{"true"}}
	res, err := http.PostForm(ts.URL+"/receiver/settings/catalog/provider/does-not-exist", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("status %d; want 404", res.StatusCode)
	}
}

func TestChassis_PostActionRestoreDefaults_DiskMatchesDefaults(t *testing.T) {
	ts := startTestChassis(t)
	defer ts.Close()

	// Mutate something first so we can prove reset overwrites.
	form := url.Values{"enabled": []string{"false"}}
	if _, err := http.PostForm(ts.URL+"/receiver/settings/catalog/provider/mtv-rewind", form); err != nil {
		t.Fatalf("pre-reset POST: %v", err)
	}

	// Sentinel in data_dir must survive.
	sentinel := filepath.Join(ts.DataDir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("survives"), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	res, err := http.Post(ts.URL+"/receiver/settings/action/restore-defaults", "", nil)
	if err != nil {
		t.Fatalf("POST restore-defaults: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), `"scope":"reboot"`) {
		t.Errorf("expected scope:reboot; got %s", string(body))
	}

	gotTOML := readConfigTOML(t, ts.CfgPath)
	wantTOML, err := config.DefaultConfigTOML(ts.DataDir)
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	if gotTOML != string(wantTOML) {
		t.Errorf("disk TOML differs from DefaultConfigTOML(%q)", ts.DataDir)
	}

	if got, _ := os.ReadFile(sentinel); string(got) != "survives" {
		t.Errorf("data_dir sentinel was disturbed by reset")
	}
}

// readConfigTOML reads cfgPath as a string with cleanup deferred.
func readConfigTOML(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
```

Required additional imports: `"net/url"`, `"path/filepath"`, `"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"`.

If the existing `startTestChassis(t)` helper does not yet expose `CfgPath` and `DataDir`, extend it to do so (it already manages a tmp dir; just add the field accessors).

- [ ] **Step 2: Run the integration suite.**

Run: `go test -tags=integration ./tests/integration -run TestChassis_ -v`
Expected: all 4C cases PASS. Existing 4A/4B tests continue to pass.

- [ ] **Step 3: Commit.**

```bash
git add tests/integration/chassis_test.go
git commit -m "test(integration): Catalog pane + restore-defaults end-to-end

Covers GET /receiver rendering of provider rows, per-provider save
with disk + in-memory verification, global HLS-override switch
restricting writes to Live providers, unknown-provider 404, and
restore-defaults producing the exact DefaultConfigTOML(dataDir)
output while preserving a data_dir sentinel."
```

---

## Task 24: Cross-side drift integration test (`cmd/mister-groovy-relay/catalog_scope_test.go`)

**Files:**
- Create: `cmd/mister-groovy-relay/catalog_scope_test.go`
- Modify: `internal/chassis/settings.go` (only if `WireLabelForScope` is not already exported)

Spec reference: §Testing — Cross-side drift catchers.

- [ ] **Step 1: Create the new file.**

Create the drift test in `cmd/mister-groovy-relay` rather than `tests/integration`: `catalogManager` is package `main`, so a separate integration package cannot import the real production wrapper without mirroring it. Same-package integration tests exercise the actual wrapper and still cross the streams/chassis boundary.

```go
//go:build integration

package main

import (
	"context"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func TestCatalogScope_HLSBufferDisabled_WireLabelMatchesRecast(t *testing.T) {
	a := newStreamsForCatalogScopeTest(t, nil)
	saver := uiserver.NewAdapterSaver(tmpConfigPath(t), &sync.Mutex{})

	m := &catalogManager{adapter: a, adapterSaver: saver}
	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %v; want ScopeRestartCast", scope)
	}
	// Chassis wire label for this scope:
	label, ok := chassis.WireLabelForScope(scope)
	if !ok || label != "recast" {
		t.Fatalf("chassis label = %q ok=%v; want \"recast\" true", label, ok)
	}
}

func TestCatalogScope_EnabledFalse_WireLabelMatchesHot(t *testing.T) {
	a := newStreamsForCatalogScopeTest(t, nil)
	saver := uiserver.NewAdapterSaver(tmpConfigPath(t), &sync.Mutex{})

	m := &catalogManager{adapter: a, adapterSaver: saver}
	enabled := false
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v; want ScopeHotSwap", scope)
	}
	label, ok := chassis.WireLabelForScope(scope)
	if !ok || label != "hot" {
		t.Fatalf("chassis label = %q ok=%v; want \"hot\" true", label, ok)
	}
}

func TestCatalogScope_HLSBufferDisabled_StopsActiveStreamsCast(t *testing.T) {
	fakeCore := &catalogScopeFakeCore{}
	a := newStreamsForCatalogScopeTest(t, fakeCore)
	enableStreamsForCatalogScopeTest(t, a)
	channelID := firstCatalogChannelForProvider(t, a, "toonami-aftermath")
	if err := a.CastChannel(context.Background(), "toonami-aftermath", channelID); err != nil {
		t.Fatalf("CastChannel: %v", err)
	}
	if fakeCore.currentRef() == "" {
		t.Fatalf("expected active core AdapterRef after CastChannel")
	}

	saver := uiserver.NewAdapterSaver(tmpConfigPath(t), &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}
	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %v; want ScopeRestartCast", scope)
	}
	if fakeCore.stopCalls() != 1 {
		t.Fatalf("StopIfAdapterRef calls = %d; want 1", fakeCore.stopCalls())
	}
	if fakeCore.currentRef() != "" {
		t.Fatalf("active core AdapterRef = %q; want cleared", fakeCore.currentRef())
	}
}

func newStreamsForCatalogScopeTest(t *testing.T, c streams.SessionManager) *streams.Adapter {
	t.Helper()
	a, err := streams.New(streams.AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   c,
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	return a
}

func enableStreamsForCatalogScopeTest(t *testing.T, a *streams.Adapter) {
	t.Helper()
	cfg := a.ConfigSnapshot()
	cfg.Enabled = true
	if _, err := a.ApplyConfigValue(cfg, func(name string, raw []byte) error { return nil }); err != nil {
		t.Fatalf("enable streams: %v", err)
	}
}

func firstCatalogChannelForProvider(t *testing.T, a *streams.Adapter, providerID string) string {
	t.Helper()
	for _, p := range a.Catalog() {
		if p.ID != providerID {
			continue
		}
		for _, g := range p.Groups {
			if len(g.Channels) > 0 {
				return g.Channels[0].ID
			}
		}
	}
	t.Fatalf("provider %q has no channels in Catalog()", providerID)
	return ""
}

type catalogScopeFakeCore struct {
	mu     sync.Mutex
	status core.SessionStatus
	stops  int
}

func (f *catalogScopeFakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.AdapterRef = req.AdapterRef
	return nil
}

func (f *catalogScopeFakeCore) StartSessionIfIdle(req core.SessionRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status.AdapterRef != "" {
		return false, nil
	}
	f.status.AdapterRef = req.AdapterRef
	return true, nil
}

func (f *catalogScopeFakeCore) StartSessionIfSession(req core.SessionRequest, ref string, generation uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref == "" || f.status.AdapterRef != ref || f.status.Generation != generation {
		return false, nil
	}
	f.status.AdapterRef = req.AdapterRef
	return true, nil
}

func (f *catalogScopeFakeCore) PauseIfAdapterRef(ref string) (bool, error) { return false, nil }

func (f *catalogScopeFakeCore) StopIfAdapterRef(ref string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref == "" || f.status.AdapterRef != ref {
		return false, nil
	}
	f.stops++
	f.status.AdapterRef = ""
	return true, nil
}

func (f *catalogScopeFakeCore) StopIfSession(ref string, generation uint64) (bool, error) {
	return false, nil
}

func (f *catalogScopeFakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *catalogScopeFakeCore) currentRef() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status.AdapterRef
}

func (f *catalogScopeFakeCore) stopCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}
```

Note: the spec requires the chassis to expose a public `WireLabelForScope` (or equivalent) helper. If it does not yet exist as an exported symbol, expose it in `internal/chassis/settings.go`:

```go
// WireLabelForScope returns the JSON wire label ("hot"/"next"/"recast"/"reboot")
// for the given ApplyScope. Exported for cross-package drift tests.
func WireLabelForScope(s adapters.ApplyScope) (string, bool) { return scopeLabel(s) }
```

- [ ] **Step 2: Run the integration test.**

Run: `go test -tags=integration ./cmd/mister-groovy-relay -run TestCatalogScope -v`
Expected: PASS for all three subtests.

- [ ] **Step 3: Commit.**

```bash
git add cmd/mister-groovy-relay/catalog_scope_test.go internal/chassis/settings.go
git commit -m "test(integration): Catalog scope cross-side drift catcher

Boots a real streams.Adapter + real AdapterSaver and the real production
catalogManager wrapper in package main. Asserts the chassis wire label
matches the scope returned by UpdateProvider for HLS-buffer-disabled
(RECAST → \"recast\") and enabled (HOT → \"hot\"), and verifies RECAST
saves stop an active streams cast through StopIfAdapterRef. Catches
future drift between the wrapper's declared-scope floor, chassis scope
labels, and runtime recast side effect."
```

---

## Pre-Task 25 Spec-Parity Gate

Before the manual JS walk, verify the implementation still matches the 4C invariants that are easiest to regress while following the task snippets:

- [ ] `SettingsData.CatalogProviderCount` is still the 3B browse-drawer tab badge count; `CatalogPaneProviderCount` is the Catalog pane header count.
- [ ] `ApplyConfigValue` runs `buildStartupSnapshotForApplyConfigValue` before `encodeSectionTOML` and before `save`, so rejected snapshot rebuilds do not touch disk or memory.
- [ ] `catalogManager.reportAndDispatch` invokes `StopActiveCast` only after a successful save/apply cycle and only for reported `ScopeRestartCast`.
- [ ] `configReset.ResetToDefaults` reads `sectioned.Bridge.DataDir` under the shared mutex and never calls `BridgeSaver.Current()` while holding that mutex.
- [ ] No 4C task edits `internal/ui/*` or the legacy streams `FieldDef.ApplyScope` / `configChangeScope` scope tables.

---

## Task 25: Manual JS verification walk + cleanup

**Files:** none modified; manual verification only.

Spec reference: §Testing — JS behavior — manual verification checklist.

- [ ] **Step 1: Start the bridge against fake-mister.**

```bash
make build
./fake-mister -addr :32100 -out ./dumps -png-every 60 &
# In a second terminal, edit config.toml so bridge.mister.host = "127.0.0.1".
./mister-groovy-relay --config path/to/config.toml &
```

Open the chassis at `http://localhost:<ui_http_port>/receiver` and click the gear button.

- [ ] **Step 2: Walk the JS checklist from the spec (§Testing — JS behavior — manual verification checklist).**

For each item, verify and tick:

- Click each provider-row switch → POST visible in DevTools Network with correct id + form key; optimistic toggle holds on success.
- Click the global HLS-override switch → POST visible; receives `scope:"recast"`.
- Click `⚠ Reset…` → button row morphs to inline confirm; scope badge hides; `.field-row.confirming` is on the row.
- Click `Cancel` → row reverts; no network request in DevTools.
- Click `⚠ Reset…`, wait 11 seconds → row auto-reverts silently; DevTools shows zero requests fired by the timeout.
- Click `⚠ Reset…` → `Confirm reset` → drawer-local notice toasts `Defaults restored — restart container to apply`; `.action-result.shown.ok` reads `▸ Defaults restored · restart to apply`.
- Force `WRITE FAILED` by `chmod 0444 config.toml` mid-click → `.action-result.shown.err` paints; notice toasts `WRITE FAILED`.
- Mixed-state walk-through: pre-set one Live provider HLS-on, one HLS-off via curl; reload drawer; global HLS switch renders off; click on → all Live providers go to disabled; click off → all Live providers go to enabled.
- Refresh between any save and the next interaction → page renders current saved state.
- JS disabled → Catalog pane + restore-defaults row render but clicks have no effect.

- [ ] **Step 3: Run the complete test suite + race + integration one final time.**

```bash
make test
go test -race ./...
make test-integration
```

Expected: all green.

- [ ] **Step 4: Commit any cleanup (delete TODO markers in the new files, fix any compile warnings).**

```bash
git status
# Address any unstaged cleanup, then:
git commit -m "chore: post-4C verification cleanup"
```

If nothing needs cleanup, skip this commit.

---

## Self-Review Notes (post-write)

**Spec coverage:** Goals 1-7 each map to one or more tasks above:
- Goal 1 (Catalog pane functional) → Tasks 10, 11, 14, 15, 18, 20, 21
- Goal 2 (Restore-defaults works) → Tasks 2, 12, 16, 19, 22
- Goal 3 (Two new chassis interfaces; no new forbidden imports) → Tasks 7, 9
- Goal 4 (Visual fidelity) → Tasks 17, 18, 19
- Goal 5 (Chassis declared-scope floor) → Task 11 (the `reportAndDispatch` + `declaredProviderScope` helpers)
- Goal 6 (Zero new wire-envelope keys / scope tiers) → no dedicated task; enforced by the handler implementations in Tasks 14-16 reusing `writeSettingsSuccess`/`writeSettingsChip`/`writeSettingsFieldErrors`
- Goal 7 (`/ui/*` unchanged) → no dedicated task; enforced by the absence of edits under `internal/ui/`

**Type consistency:** `CatalogSettingsManager`, `CatalogProviderState`, `CatalogProviderPatch`, `ConfigReset` all spelled identically across Tasks 7-16. `UpdateProvider` signature consistent across the wrapper (Task 11) and handler (Task 14). `data-catalog-provider` + `data-catalog-field` attribute names consistent across Task 18 template and Task 20 JS handler.

**Test discipline:** Every Go-code task has a failing-test-first step. Template tasks (18, 19) also write failing tests first. JS-only tasks (20, 21, 22) have no JS test runner today; they're exercised by the integration tests in Task 23 (HTTP-level) and the manual checklist in Task 25.

**Commit cadence:** 25 commits across the plan (one per task + one optional cleanup). Each commit is self-contained and builds cleanly.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-28-receiver-chassis-catalog-pane.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
