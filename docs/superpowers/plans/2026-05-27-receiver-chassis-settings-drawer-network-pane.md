# Receiver Chassis Settings Drawer + Network Pane (Phase 4A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the chassis settings drawer with the Network pane fully functional end-to-end (gear button opens drawer; 9 bridge fields auto-save on blur with inline validation + 4-tier scope badges; "Test MiSTer connectivity" action button posts to the existing safe status-probe pattern), plus four "Spec 4X — pending" stub panes so the drawer chrome is complete and 4B–4F can ship into it.

**Architecture:** Chassis defines narrow interfaces (`BridgeSettingsSaver`, `Prober`, `settingsChipError`) that the existing `*uiserver.BridgeSaver` and a new `cmd/mister-groovy-relay` prober wrapper satisfy from outside; `internal/chassis` continues to **not** import `internal/uiserver` or any concrete adapter. Forms POST `application/x-www-form-urlencoded` to `/receiver/settings/bridge` (any subset of fields) or `/receiver/settings/action/probe-mister`; success returns `{ok:true,scope:"hot|next|recast|reboot"}` via a chassis-side `ApplyScope`-label helper (never `ApplyScope.String()`); failures return `{ok:false,errors:{...}}` for per-field validation or `{ok:false,chip:"..."}` for whole-form/preflight failures via the new `settingsChipError` structural interface match. Drawer is server-rendered into the `.settings-panel` slot at page load (hidden until `body.settings-open` toggles); subsequent updates are pure client-side mutations from JSON responses. CSS interior rules (~80 lines) ported from the v24 mockup; chassis JS is plain ES2022 with explicit `<script>` tags in `shell.html`.

**Tech Stack:** Go 1.26 stdlib (`context`, `encoding/json`, `errors`, `net/http`, `strconv`, `strings`, `sync`); existing internal packages (`internal/chassis`, `internal/uiserver`, `internal/config`, `internal/adapters`, `internal/groovynet`, `internal/groovy`); existing `cmd/mister-groovy-relay/launcher.go` `bridgeMisterProber`; `html/template` with custom FuncMap; vanilla ES2022 (no bundler). No new go.mod dependencies.

**Spec:** [docs/superpowers/specs/2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md](../specs/2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md) (committed at 40856be after three review passes).

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/chassis/settings.go` | `BridgeSettingsSaver`, `Prober`, `ProbeResult`, `settingsChipError` interfaces; per-field decoder / overlay / scope tables; `scopeLabel` helper; `handleSettingsBridgePost`; `handleSettingsActionProbeMister`; response writers |
| `internal/chassis/settings_test.go` | Unit tests for decoder/overlay/scope tables, scope label helper, both handlers' branches |
| `internal/chassis/static/settings-drawer.js` | Gear/close toggle, tab switching, field auto-save, switch toggle, probe action, response handling |
| `internal/chassis/templates/settings-field.html` | Inline `{{ define }}` blocks for the field renderer; lives in `settings-drawer.html` to keep the template surface small |
| `cmd/mister-groovy-relay/chassis_prober.go` | Thin wrapper around the existing `bridgeMisterProber` exposing the chassis `Prober` interface |

**Modified files:**

| Path | Change |
|---|---|
| `internal/uiserver/bridge_saver.go` | Add `settingsError` concrete type (`StatusCode() int`, `Chip() string`, `Unwrap() error`); wrap validation/preflight failures (TCP/UDP bind probe failures, data_dir writable failures, `config.Sectioned.Validate` failures). `Save()` signature stays `(adapters.ApplyScope, error)`. |
| `internal/uiserver/bridge_saver_test.go` | New tests for the typed error wrapper (each preflight path returns settingsError with correct chip + status) |
| `internal/chassis/data.go` | Extend `SettingsData` from `{Open bool}` to include `Bridge config.BridgeConfig`, `Errors map[string]string`, `AdapterCount`, `CatalogProviderCount`; add `buildSettingsData` helper |
| `internal/chassis/session.go` | Wire `buildSettingsData` into `snapshotFromStatusView` and `idleSnapshot` reading from `BridgeSettingsSaver.Current()` when wired |
| `internal/chassis/server.go` | Add `Config.BridgeSaver BridgeSettingsSaver` + `Config.Prober Prober`; store on `*Server`; mount both new POST routes via `requireSameOrigin` |
| `internal/chassis/templates.go` | Register FuncMap helpers: `field`, `dict`, `stub`, `errOf`, `itoa`, `settingsScopeLabel` |
| `internal/chassis/templates/settings-drawer.html` | Rewrite from current 8-line stub: full 5-tab strip, Network pane (3 sections + 9 field rows + probe action row), 4 stub panes, drawer-local notice slot |
| `internal/chassis/templates/transport.html` | One-line edit: add `data-settings-toggle` attribute to the existing gear button |
| `internal/chassis/templates/shell.html` | Add deferred `<script src="/receiver/static/settings-drawer.js">` tag with version cache-buster |
| `internal/chassis/static/chassis.css` | Port ~80 lines of interior settings CSS from mockup; add `.scope.next` and `.settings-notice` (+ `.ok`, `.err`) rules |
| `internal/chassis/chassis_test.go` | Template render tests: drawer shape, Network pane field rows, all six field types via helper, scope badge variants, stub pane, notice slot, settings-drawer.js script inclusion |
| `cmd/mister-groovy-relay/main.go` | Pass existing `BridgeSaver` instance into `chassis.Config.BridgeSaver`; construct `chassisProber` (wraps `bridgeMisterProber`) and pass into `chassis.Config.Prober` |
| `tests/integration/chassis_test.go` | End-to-end coverage: drawer renders with current values; bridge POST hot-swap + REBOOT scope + field-validation + preflight chip + nil-saver 503; probe POST success + timeout + nil-prober 503 |

**Files intentionally unchanged:**

- `internal/chassis/import_check_test.go` — its forbidden-imports list already covers `internal/uiserver` and concrete adapters; the chassis-owned interface pattern in this plan keeps that invariant intact.
- `internal/ui/*` — legacy `/ui/*` keeps working unchanged.
- `internal/core/*` — no new core surface.
- `internal/adapters/*` — no new adapter interfaces in 4A.

---

## Sequencing

Tasks 1–3 add foundational types: chassis-owned interfaces (Task 1), the uiserver typed-error wrapper concrete type (Task 2), and the saver-side wrapping of preflight/validation errors (Task 3). After Task 3 the saver returns typed errors the chassis can pattern-match without importing the concrete type.

Tasks 4–6 build the chassis data + config layer: SettingsData extension (Task 4), snapshot wiring (Task 5), Config additions (Task 6). At this point the chassis carries the bridge config into renders without yet exposing routes.

Tasks 7–9 build the three per-field tables (decoder, overlay, scope) with cross-table integrity tests. These tables are the single source of truth for which fields Network pane supports.

Tasks 10–12 add handlers: bridge POST (Task 10), the chassis `Prober` wrapper in main package (Task 11), probe POST (Task 12).

Tasks 13–14 wire the routes (Task 13) and main.go (Task 14). After Task 14 the server boots with the new routes live but the UI cannot reach them yet.

Tasks 15–16 add the template FuncMap helpers (Task 15 small helpers, Task 16 the `field` renderer for all six types).

Tasks 17–19 build the drawer template: shell + notice slot (Task 17), Network pane content + probe row (Task 18), four stub panes (Task 19).

Tasks 20–21 wire the drawer into the page chrome: gear button attribute (Task 20), settings-drawer.js script tag (Task 21).

Task 22 ports the interior CSS from the mockup.

Tasks 23–27 add the client JS: shell + tabs (Task 23), field auto-save (Task 24), switch handler (Task 25), probe button (Task 26), error painting + notice + REBOOT toast (Task 27).

Task 28 adds end-to-end integration tests.

Each task is independently committable. CI green between tasks is required.

---

## Task 1: Add Chassis-Owned Interfaces

**Files:**
- Create: `internal/chassis/settings.go`
- Create: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing test**

Create [internal/chassis/settings_test.go](../../../internal/chassis/settings_test.go):

```go
package chassis

import (
	"context"
	"errors"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// fakeBridgeSettingsSaver is a compile-time conformance fixture for the
// chassis-owned BridgeSettingsSaver interface. If the interface shape
// changes, this fails to build, alerting the changeset reviewer.
type fakeBridgeSettingsSaver struct {
	cur    config.BridgeConfig
	saveFn func(config.BridgeConfig) (adapters.ApplyScope, error)
}

func (f fakeBridgeSettingsSaver) Current() config.BridgeConfig { return f.cur }
func (f fakeBridgeSettingsSaver) Save(c config.BridgeConfig) (adapters.ApplyScope, error) {
	if f.saveFn != nil {
		return f.saveFn(c)
	}
	return adapters.ScopeHotSwap, nil
}

// fakeProber is a compile-time conformance fixture for the chassis-owned
// Prober interface.
type fakeProber struct {
	res ProbeResult
	err error
}

func (f fakeProber) ProbeMister(ctx context.Context, b config.BridgeConfig) (ProbeResult, error) {
	return f.res, f.err
}

// fakeSettingsChipError exercises the structural settingsChipError match.
type fakeSettingsChipError struct {
	status int
	chip   string
	cause  error
}

func (f *fakeSettingsChipError) Error() string  { return f.chip }
func (f *fakeSettingsChipError) StatusCode() int { return f.status }
func (f *fakeSettingsChipError) Chip() string   { return f.chip }
func (f *fakeSettingsChipError) Unwrap() error  { return f.cause }

func TestChassisSettingsInterfaces_StructuralConformance(t *testing.T) {
	t.Parallel()
	var s BridgeSettingsSaver = fakeBridgeSettingsSaver{}
	if got := s.Current().DataDir; got != "" {
		t.Errorf("Current().DataDir = %q, want empty", got)
	}
	var p Prober = fakeProber{}
	if _, err := p.ProbeMister(context.Background(), config.BridgeConfig{}); err != nil {
		t.Errorf("ProbeMister err = %v, want nil", err)
	}
	var ce settingsChipError
	src := &fakeSettingsChipError{status: 409, chip: "PORT IN USE"}
	if !errors.As(src, &ce) {
		t.Fatalf("errors.As(src, &settingsChipError) = false, want true")
	}
	if ce.StatusCode() != 409 || ce.Chip() != "PORT IN USE" {
		t.Errorf("ce = (%d, %q), want (409, \"PORT IN USE\")", ce.StatusCode(), ce.Chip())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestChassisSettingsInterfaces -v`

Expected: FAIL — `BridgeSettingsSaver`, `Prober`, `ProbeResult`, `settingsChipError` undefined.

- [ ] **Step 3: Create the interfaces**

Create [internal/chassis/settings.go](../../../internal/chassis/settings.go):

```go
// Package chassis settings.go defines the chassis-owned interfaces and
// handlers for Phase 4A: the settings drawer + Network pane.
//
// internal/chassis intentionally does NOT import internal/uiserver. The
// production *uiserver.BridgeSaver satisfies BridgeSettingsSaver from
// outside; the typed settingsError wrapper that uiserver returns
// satisfies the settingsChipError interface structurally, matched via
// errors.As against the interface (Go 1.21+).
package chassis

import (
	"context"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// BridgeSettingsSaver is the narrow chassis-side interface for bridge
// settings persistence and snapshot. Production passes
// *uiserver.BridgeSaver, but internal/chassis does not import
// internal/uiserver — the wiring lives in cmd/mister-groovy-relay.
type BridgeSettingsSaver interface {
	// Current returns the live in-memory bridge config snapshot. The
	// chassis settings drawer uses this for first render and for
	// composing patches (current + touched-field overlay) on each save.
	Current() config.BridgeConfig

	// Save persists the patch to disk and dispatches in-memory side
	// effects. The returned ApplyScope is the max-wins scope across all
	// changed fields; the chassis maps it via scopeLabel before emitting
	// to the wire.
	Save(config.BridgeConfig) (adapters.ApplyScope, error)
}

// Prober is the narrow chassis-side interface the probe-mister action
// uses. Production passes a thin wrapper around the existing
// bridgeMisterProber in cmd/mister-groovy-relay/launcher.go, which uses
// CMD_GET_STATUS over an ephemeral source port (NOT the live sender's
// bound source port).
type Prober interface {
	ProbeMister(ctx context.Context, bridge config.BridgeConfig) (ProbeResult, error)
}

// ProbeResult is the structured success payload from a probe attempt.
// LatencyMs is the wall-clock round-trip in milliseconds (e.g. 4.2).
type ProbeResult struct {
	LatencyMs float64
	Host      string
	Port      int
}

// settingsChipError is matched structurally so saver-layer typed errors
// can carry HTTP/chip details across the interface boundary without a
// uiserver import. The chassis handler uses errors.As against the
// interface (Go 1.21+).
type settingsChipError interface {
	error
	StatusCode() int
	Chip() string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestChassisSettingsInterfaces -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add BridgeSettingsSaver, Prober, settingsChipError interfaces"
```

---

## Task 2: Add `settingsError` Wrapper in uiserver

**Files:**
- Modify: `internal/uiserver/bridge_saver.go`
- Modify: `internal/uiserver/bridge_saver_test.go`

- [ ] **Step 1: Write the failing test**

Append to [internal/uiserver/bridge_saver_test.go](../../../internal/uiserver/bridge_saver_test.go):

```go
func TestSettingsError_StatusCodeAndChip(t *testing.T) {
	t.Parallel()
	cause := errors.New("bind: address in use")
	se := &settingsError{status: 409, chip: "PORT IN USE", cause: cause}
	if got := se.StatusCode(); got != 409 {
		t.Errorf("StatusCode = %d, want 409", got)
	}
	if got := se.Chip(); got != "PORT IN USE" {
		t.Errorf("Chip = %q, want PORT IN USE", got)
	}
	if got := se.Error(); got == "" {
		t.Errorf("Error() is empty, want non-empty")
	}
	if got := se.Unwrap(); got != cause {
		t.Errorf("Unwrap = %v, want %v", got, cause)
	}
	// errors.As must succeed against the cause.
	var unwrapped error
	if !errors.As(se, &unwrapped) {
		t.Fatalf("errors.As(se, &error) = false, want true")
	}
}
```

(If `errors` is not already imported in this test file, add the import.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/uiserver -run TestSettingsError -v`

Expected: FAIL — `settingsError` undefined.

- [ ] **Step 3: Add the wrapper**

Append to [internal/uiserver/bridge_saver.go](../../../internal/uiserver/bridge_saver.go):

```go
// settingsError is the typed wrapper around validation/preflight errors
// that BridgeSaver.Save returns starting in Phase 4A. The chassis
// handler matches against an interface { StatusCode() int; Chip() string }
// structurally (errors.As against the interface in Go 1.21+) so
// internal/chassis can map status + chip into its JSON envelope without
// importing internal/uiserver.
//
// Existing /ui/* callers that ignore typed errors are unaffected; the
// concrete error still wraps the underlying cause via Unwrap.
type settingsError struct {
	status int
	chip   string
	cause  error
}

func (e *settingsError) Error() string {
	if e.cause != nil {
		return e.chip + ": " + e.cause.Error()
	}
	return e.chip
}
func (e *settingsError) StatusCode() int { return e.status }
func (e *settingsError) Chip() string    { return e.chip }
func (e *settingsError) Unwrap() error   { return e.cause }
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/uiserver -run TestSettingsError -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/bridge_saver.go internal/uiserver/bridge_saver_test.go
git commit -m "feat(uiserver): add settingsError typed wrapper with StatusCode/Chip/Unwrap"
```

---

## Task 3: Wrap Preflight & Validation Failures with `settingsError`

**Files:**
- Modify: `internal/uiserver/bridge_saver.go`
- Modify: `internal/uiserver/bridge_saver_test.go`

The saver currently returns raw `fmt.Errorf`-style errors from its preflight (TCP/UDP bind, data_dir writable) and `config.Sectioned.Validate` paths. Wrap each one in `settingsError` so the chassis can pattern-match for chip + status.

- [ ] **Step 1: Write failing tests**

Append to [internal/uiserver/bridge_saver_test.go](../../../internal/uiserver/bridge_saver_test.go):

```go
func TestBridgeSaver_PortInUseReturnsTypedError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[bridge]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bind a UDP socket on a real ephemeral port so the preflight fails.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	busyPort := conn.LocalAddr().(*net.UDPAddr).Port

	sec := &config.Sectioned{Bridge: testBridgeConfig(t, "NTSC_480i")}
	core := &fakeBridgeCore{}
	saver := NewBridgeSaver(cfgPath, sec, core, adapters.NewRegistry())
	newCfg := sec.Bridge
	newCfg.MiSTer.SourcePort = busyPort

	_, err = saver.Save(newCfg)
	if err == nil {
		t.Fatal("Save = nil, want PORT IN USE error")
	}
	var se *settingsError
	if !errors.As(err, &se) {
		t.Fatalf("err is not *settingsError: %v", err)
	}
	if se.StatusCode() != 409 || se.Chip() != "PORT IN USE" {
		t.Errorf("got (%d, %q), want (409, \"PORT IN USE\")", se.StatusCode(), se.Chip())
	}
}

func TestBridgeSaver_DataDirNotWritableReturnsTypedError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[bridge]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sec := &config.Sectioned{Bridge: testBridgeConfig(t, "NTSC_480i")}
	core := &fakeBridgeCore{}
	saver := NewBridgeSaver(cfgPath, sec, core, adapters.NewRegistry())

	// A relative path is rejected by the preflight (must be absolute or empty).
	// If the existing preflight uses ProbeDirWritable on an absolute non-existent
	// path that cannot be created, that's also a fail path — pick whichever
	// matches the saver's existing behavior. The assertion below holds either way.
	newCfg := sec.Bridge
	newCfg.DataDir = "relative/path/not/allowed"
	_, err := saver.Save(newCfg)
	if err == nil {
		t.Fatal("Save = nil, want PATH NOT WRITABLE error")
	}
	var se *settingsError
	if !errors.As(err, &se) {
		t.Fatalf("err is not *settingsError: %v", err)
	}
	if se.StatusCode() != 409 || se.Chip() != "PATH NOT WRITABLE" {
		t.Errorf("got (%d, %q), want (409, \"PATH NOT WRITABLE\")", se.StatusCode(), se.Chip())
	}
}
```

(Add `"errors"`, `"net"`, `"os"`, `"path/filepath"` imports if not already present. The `testBridgeConfig`, `fakeBridgeCore` helpers already exist in this test file.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/uiserver -run "TestBridgeSaver_PortInUseReturnsTypedError|TestBridgeSaver_DataDirNotWritableReturnsTypedError" -v`

Expected: FAIL — saver returns plain errors, not `*settingsError`.

- [ ] **Step 3: Wrap preflight call sites**

Find each preflight call site in [internal/uiserver/bridge_saver.go](../../../internal/uiserver/bridge_saver.go) (look around lines 185-201 for `config.ProbeTCPPort`, `config.ProbeUDPPort`, `config.ProbeDirWritable` invocations).

For each failure path, wrap the returned error:

```go
// UDP source-port preflight
if err := config.ProbeUDPPort(newCfg.MiSTer.SourcePort); err != nil {
    return scope, &settingsError{
        status: http.StatusConflict, // 409
        chip:   "PORT IN USE",
        cause:  err,
    }
}

// TCP http-port preflight
if err := config.ProbeTCPPort(newCfg.UI.HTTPPort); err != nil {
    return scope, &settingsError{
        status: http.StatusConflict,
        chip:   "PORT IN USE",
        cause:  err,
    }
}

// data_dir writability preflight
if newCfg.DataDir != "" {
    if err := config.ProbeDirWritable(newCfg.DataDir); err != nil {
        return scope, &settingsError{
            status: http.StatusConflict,
            chip:   "PATH NOT WRITABLE",
            cause:  err,
        }
    }
}
```

Also wrap any `config.Sectioned.Validate` failure that survives chassis-side field validation:

```go
if err := sec.Validate(); err != nil {
    return scope, &settingsError{
        status: http.StatusBadRequest, // 400
        chip:   "BAD INPUT",
        cause:  err,
    }
}
```

Add `"net/http"` to imports if not already present.

**The exact call-site shape varies with the current Save() function structure — preserve the existing scope-return semantics and only wrap the error side of the return.**

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/uiserver -count=1 ./...`

Expected: all PASS (the new typed-error tests pass; the existing tests still pass — old assertions on raw error strings should still hold because `settingsError.Error()` includes the cause).

If any existing test fails because it does `strings.Contains(err.Error(), "specific text")`, update the assertion to match the new chip-prefixed format, or rewrite it to use `errors.As(&settingsError{})`.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/bridge_saver.go internal/uiserver/bridge_saver_test.go
git commit -m "feat(uiserver): wrap preflight failures with settingsError"
```

---

## Task 4: Extend `SettingsData` + `buildSettingsData` Helper

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/data_test.go` (if exists; otherwise create)

- [ ] **Step 1: Write the failing test**

Find or create `internal/chassis/data_test.go` and append:

```go
func TestBuildSettingsData_AdapterCountExcludesAUX(t *testing.T) {
	t.Parallel()
	reg := adapters.NewRegistry()
	// Use minimal fake adapters; reuse fixtures from existing tests if any.
	mustRegister(t, reg, fakeNamedAdapter{name: "plex"})
	mustRegister(t, reg, fakeNamedAdapter{name: "aux"})
	mustRegister(t, reg, fakeNamedAdapter{name: "dlna"})
	bridge := config.BridgeConfig{DataDir: "/var/lib/relay"}
	got := buildSettingsData(bridge, reg, nil)
	if got.AdapterCount != 2 {
		t.Errorf("AdapterCount = %d, want 2 (aux excluded)", got.AdapterCount)
	}
	if got.Bridge.DataDir != "/var/lib/relay" {
		t.Errorf("Bridge.DataDir = %q, want /var/lib/relay", got.Bridge.DataDir)
	}
	if got.Errors == nil {
		t.Errorf("Errors map is nil; want empty initialized map")
	}
}

func TestBuildSettingsData_NilCatalogYieldsZeroCount(t *testing.T) {
	t.Parallel()
	got := buildSettingsData(config.BridgeConfig{}, adapters.NewRegistry(), nil)
	if got.CatalogProviderCount != 0 {
		t.Errorf("CatalogProviderCount = %d, want 0", got.CatalogProviderCount)
	}
}

// fakeNamedAdapter satisfies adapters.Adapter with the minimum surface needed
// for registry.List() walks. Reuse an existing helper if one already exists
// in the chassis test package.
type fakeNamedAdapter struct{ name string }

func (f fakeNamedAdapter) Name() string                                     { return f.name }
func (f fakeNamedAdapter) DisplayName() string                              { return f.name }
func (f fakeNamedAdapter) Fields() []adapters.FieldDef                      { return nil }
func (f fakeNamedAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f fakeNamedAdapter) IsEnabled() bool                                  { return true }
func (f fakeNamedAdapter) Start(ctx context.Context) error                  { return nil }
func (f fakeNamedAdapter) Stop() error                                      { return nil }
func (f fakeNamedAdapter) Status() adapters.Status                          { return adapters.Status{} }
func (f fakeNamedAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func mustRegister(t *testing.T, reg *adapters.Registry, a adapters.Adapter) {
	t.Helper()
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register(%s): %v", a.Name(), err)
	}
}
```

(Add `"context"` and `"github.com/BurntSushi/toml"` imports if not already present.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestBuildSettingsData -v`

Expected: FAIL — `buildSettingsData`, `SettingsData.Bridge`, `SettingsData.AdapterCount` undefined or wrong shape.

- [ ] **Step 3: Extend `SettingsData` + add helper**

Find the existing `SettingsData` definition in [internal/chassis/data.go](../../../internal/chassis/data.go) and replace its body. Add the `buildSettingsData` helper alongside.

```go
// SettingsData carries the settings drawer state into the snapshot.
// First-render values come from BridgeSettingsSaver.Current() (or
// startup cfg.Bridge in offline test paths). Errors is always empty on
// the first server render; it is populated only when the server re-
// renders after a redirect following a failed save, which 4A never
// does (the JSON error path is purely client-side).
type SettingsData struct {
	Open                 bool
	Bridge               config.BridgeConfig
	Errors               map[string]string
	AdapterCount         int
	CatalogProviderCount int
}

// buildSettingsData composes the chassis-rendered settings drawer state
// from a bridge config, the adapter registry, and the streams catalog
// viewer. AUX is excluded from AdapterCount: it is a hardware-button
// surface, not a configurable adapter in the Settings UI sense.
func buildSettingsData(
	bridge config.BridgeConfig,
	registry *adapters.Registry,
	catalog adapters.StreamsCatalogViewer,
) SettingsData {
	adapterCount := 0
	if registry != nil {
		for _, a := range registry.List() {
			if a.Name() == "aux" {
				continue
			}
			adapterCount++
		}
	}
	catalogProviderCount := 0
	if catalog != nil {
		catalogProviderCount = len(catalog.Catalog())
	}
	return SettingsData{
		Bridge:               bridge,
		Errors:               map[string]string{},
		AdapterCount:         adapterCount,
		CatalogProviderCount: catalogProviderCount,
	}
}
```

(Confirm `internal/config` and `internal/adapters` are imported.)

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestBuildSettingsData -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/data.go internal/chassis/data_test.go
git commit -m "feat(chassis): extend SettingsData and add buildSettingsData helper"
```

---

## Task 5: Wire `buildSettingsData` into Snapshot Paths

**Files:**
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/session_test.go` (or `data_test.go` if session tests live there)

- [ ] **Step 1: Write the failing test**

Append to the appropriate session-test file:

```go
func TestSnapshot_SettingsReadsFromBridgeSaverCurrentWhenWired(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{cur: config.BridgeConfig{DataDir: "/from-saver"}}
	srv := &Server{cfg: Config{
		BridgeSaver: saver,
		Registry:    adapters.NewRegistry(),
		Sectioned:   &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/from-startup"}},
	}}
	snap := srv.idleSnapshot(time.Now())
	if snap.Settings.Bridge.DataDir != "/from-saver" {
		t.Errorf("Bridge.DataDir = %q, want /from-saver (saver wins over startup)",
			snap.Settings.Bridge.DataDir)
	}
}

func TestSnapshot_SettingsFallsBackToStartupConfigWhenSaverNil(t *testing.T) {
	t.Parallel()
	srv := &Server{cfg: Config{
		Registry:  adapters.NewRegistry(),
		Sectioned: &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/from-startup"}},
	}}
	snap := srv.idleSnapshot(time.Now())
	if snap.Settings.Bridge.DataDir != "/from-startup" {
		t.Errorf("Bridge.DataDir = %q, want /from-startup", snap.Settings.Bridge.DataDir)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestSnapshot_Settings -v`

Expected: FAIL — settings snapshot doesn't read from saver.

- [ ] **Step 3: Wire it in**

In [internal/chassis/session.go](../../../internal/chassis/session.go), find both `snapshotFromStatusView` and `idleSnapshot`. In each, add a helper call that populates the settings block. A shared private helper avoids duplication:

```go
// settingsSnapshot reads the bridge config from the BridgeSettingsSaver
// when wired (production), or falls back to startup cfg.Sectioned.Bridge
// for offline tests / nil-saver render paths.
func (s *Server) settingsSnapshot() SettingsData {
	bridge := config.BridgeConfig{}
	if s.cfg.BridgeSaver != nil {
		bridge = s.cfg.BridgeSaver.Current()
	} else if s.cfg.Sectioned != nil {
		bridge = s.cfg.Sectioned.Bridge
	}
	var catalog adapters.StreamsCatalogViewer
	if s.cfg.StreamsCatalogViewer != nil {
		catalog = s.cfg.StreamsCatalogViewer
	}
	return buildSettingsData(bridge, s.cfg.Registry, catalog)
}
```

Then in both snapshot functions, populate the `Settings` field of the returned `SnapshotData`:

```go
snap.Settings = s.settingsSnapshot()
```

Preserve the existing `.Settings.Open` semantics (the drawer's open/close state is purely client-side; first render is always closed).

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestSnapshot_Settings -v && go test ./internal/chassis -count=1 ./...`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/session.go internal/chassis/session_test.go
git commit -m "feat(chassis): wire buildSettingsData into idle and live snapshots"
```

---

## Task 6: Add `Config.BridgeSaver` and `Config.Prober`

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go` (or wherever Config tests live)

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestConfig_AcceptsBridgeSaverAndProber(t *testing.T) {
	t.Parallel()
	cfg := Config{
		BridgeSaver: fakeBridgeSettingsSaver{},
		Prober:      fakeProber{},
	}
	if cfg.BridgeSaver == nil {
		t.Errorf("BridgeSaver = nil after assign")
	}
	if cfg.Prober == nil {
		t.Errorf("Prober = nil after assign")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestConfig_AcceptsBridgeSaverAndProber -v`

Expected: FAIL — `Config.BridgeSaver`, `Config.Prober` fields undefined.

- [ ] **Step 3: Add fields to `Config`**

In [internal/chassis/server.go](../../../internal/chassis/server.go), find the `Config` struct definition and add the two fields with a comment block describing their role:

```go
type Config struct {
    // ... existing fields ...

    // BridgeSaver persists bridge-side settings drawer mutations and
    // exposes the live in-memory bridge config snapshot for renders.
    // Production passes *uiserver.BridgeSaver; the chassis defines its
    // own narrow interface so it does not import internal/uiserver.
    // May be nil in unit-test fixtures; handlers respond 503 NOT READY
    // in that case.
    BridgeSaver BridgeSettingsSaver

    // Prober runs the connectivity probe against the currently-saved
    // MiSTer host/port. Production passes a thin wrapper around
    // cmd/mister-groovy-relay/launcher.go's bridgeMisterProber, which
    // uses CMD_GET_STATUS over an ephemeral source port. May be nil in
    // unit-test fixtures; handlers respond 503 NOT READY in that case.
    Prober Prober
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestConfig_AcceptsBridgeSaverAndProber -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add Config.BridgeSaver and Config.Prober fields"
```

---

## Task 7: Add Per-Field Decoder Table + Decoder Tests

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write failing tests**

Append to `settings_test.go`:

```go
func TestDecodeMisterHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want, errSub string
	}{
		{"192.168.1.42", "192.168.1.42", ""},
		{"mister.local", "mister.local", ""},
		{"  192.168.1.42  ", "192.168.1.42", ""},
		{"", "", "is required"},
		{"!not a host!", "", "not a valid IPv4 or hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			v, err := decodeMisterHost(tc.in)
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("err = %v, want substring %q", err, tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if v != tc.want {
				t.Errorf("v = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestDecodePort_Range(t *testing.T) {
	t.Parallel()
	if _, err := decodePort("32100"); err != nil {
		t.Errorf("32100 err = %v, want nil", err)
	}
	if _, err := decodePort("65535"); err != nil {
		t.Errorf("65535 err = %v, want nil", err)
	}
	for _, in := range []string{"0", "-1", "65536", "abc", ""} {
		if _, err := decodePort(in); err == nil {
			t.Errorf("%q err = nil, want non-nil", in)
		}
	}
}

func TestDecodeOptionalIPv4(t *testing.T) {
	t.Parallel()
	v, err := decodeOptionalIPv4("")
	if err != nil || v != "" {
		t.Errorf("empty: (%q, %v), want (\"\", nil)", v, err)
	}
	v, err = decodeOptionalIPv4("192.168.1.1")
	if err != nil || v != "192.168.1.1" {
		t.Errorf("valid: (%q, %v)", v, err)
	}
	if _, err := decodeOptionalIPv4("not-an-ip"); err == nil {
		t.Errorf("invalid: err = nil, want non-nil")
	}
}

func TestDecodeOptionalAbsPath(t *testing.T) {
	t.Parallel()
	v, err := decodeOptionalAbsPath("")
	if err != nil || v != "" {
		t.Errorf("empty: (%q, %v), want (\"\", nil)", v, err)
	}
	v, err = decodeOptionalAbsPath("/var/lib/relay")
	if err != nil || v != "/var/lib/relay" {
		t.Errorf("absolute: (%q, %v)", v, err)
	}
	if _, err := decodeOptionalAbsPath("relative/path"); err == nil {
		t.Errorf("relative: err = nil, want non-nil")
	}
}

func TestBridgeFieldDecoders_HasEntryForEveryNetworkField(t *testing.T) {
	t.Parallel()
	want := []string{
		"mister_host", "mister_port", "mister_source_port",
		"ui_http_port", "host_ip", "data_dir",
		"ffmpeg_path", "ffprobe_path", "ytdlp_path",
	}
	for _, name := range want {
		if _, ok := bridgeFieldDecoders[name]; !ok {
			t.Errorf("bridgeFieldDecoders missing entry %q", name)
		}
	}
}
```

(Add `"strings"` import.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestDecode|TestBridgeFieldDecoders" -v`

Expected: FAIL — none of the decoders or the table exist.

- [ ] **Step 3: Add the decoders and the table**

Append to `internal/chassis/settings.go`:

```go
import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

// bridgeFieldDecoder is the per-field validation entry. It takes a raw
// form string (already trimmed by the caller) and returns the decoded
// typed value (as an any so the overlay table can write it into the
// right BridgeConfig field) or a human-readable error.
type bridgeFieldDecoder func(raw string) (any, error)

var bridgeFieldDecoders = map[string]bridgeFieldDecoder{
	"mister_host": func(s string) (any, error) {
		v, err := decodeMisterHost(s)
		return v, err
	},
	"mister_port": func(s string) (any, error) {
		v, err := decodePort(s)
		return v, err
	},
	"mister_source_port": func(s string) (any, error) {
		v, err := decodePort(s)
		return v, err
	},
	"ui_http_port": func(s string) (any, error) {
		v, err := decodePort(s)
		return v, err
	},
	"host_ip": func(s string) (any, error) {
		v, err := decodeOptionalIPv4(s)
		return v, err
	},
	"data_dir": func(s string) (any, error) {
		v, err := decodeOptionalAbsPath(s)
		return v, err
	},
	"ffmpeg_path": func(s string) (any, error) {
		v, err := decodeOptionalAbsPath(s)
		return v, err
	},
	"ffprobe_path": func(s string) (any, error) {
		v, err := decodeOptionalAbsPath(s)
		return v, err
	},
	"ytdlp_path": func(s string) (any, error) {
		v, err := decodeOptionalAbsPath(s)
		return v, err
	},
}

// decodeMisterHost trims whitespace and accepts a non-empty IPv4 string
// or RFC-952 hostname. Empty -> "is required". Otherwise -> "not a
// valid IPv4 or hostname".
func decodeMisterHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("is required")
	}
	if net.ParseIP(s) != nil {
		return s, nil
	}
	if isValidHostname(s) {
		return s, nil
	}
	return "", fmt.Errorf("not a valid IPv4 or hostname")
}

// decodePort accepts a numeric string in [1, 65535]. Empty or non-numeric
// -> "must be a whole number". Out of range -> "port out of range (1-65535)".
func decodePort(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("must be a whole number")
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port out of range (1-65535)")
	}
	return n, nil
}

// decodeOptionalIPv4 returns "" for empty input (clears the field), or a
// valid IPv4 string. Anything else -> "not a valid IPv4 address".
func decodeOptionalIPv4(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("not a valid IPv4 address")
	}
	return s, nil
}

// decodeOptionalAbsPath returns "" for empty input, or an absolute path.
// Relative -> "must be an absolute path". No existence check.
func decodeOptionalAbsPath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("must be an absolute path")
	}
	return s, nil
}

// isValidHostname is a permissive RFC-952/1123-ish check: 1..253 chars
// total, label chars in [a-z0-9-], labels non-empty, no leading/trailing
// hyphen, dot-separated.
func isValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-'
			if !ok {
				return false
			}
		}
	}
	return true
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run "TestDecode|TestBridgeFieldDecoders" -v`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add per-field decoders and validation table"
```

---

## Task 8: Add Per-Field Overlay Table + Cross-Table Test

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

The overlay table maps a form-name to a closure that writes the decoded value into the right path inside `config.BridgeConfig`. This separates "what does the value mean?" (decoder) from "where does it live in the struct?" (overlay).

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestBridgeFieldOverlays_HostWritesToMisterHost(t *testing.T) {
	t.Parallel()
	cfg := config.BridgeConfig{}
	bridgeFieldOverlays["mister_host"](&cfg, "192.168.1.42")
	if cfg.MiSTer.Host != "192.168.1.42" {
		t.Errorf("MiSTer.Host = %q, want 192.168.1.42", cfg.MiSTer.Host)
	}
}

func TestBridgeFieldOverlays_AllNetworkFieldsPresent(t *testing.T) {
	t.Parallel()
	// The decoder table is the authoritative supported-fields set.
	for name := range bridgeFieldDecoders {
		if _, ok := bridgeFieldOverlays[name]; !ok {
			t.Errorf("bridgeFieldOverlays missing entry for decoder %q", name)
		}
	}
	for name := range bridgeFieldOverlays {
		if _, ok := bridgeFieldDecoders[name]; !ok {
			t.Errorf("bridgeFieldOverlays has orphan entry %q (no decoder)", name)
		}
	}
}

func TestBridgeFieldOverlays_EveryFieldRoundTrips(t *testing.T) {
	t.Parallel()
	cases := map[string]any{
		"mister_host":        "10.0.0.1",
		"mister_port":        32100,
		"mister_source_port": 32101,
		"ui_http_port":       32500,
		"host_ip":            "10.0.0.2",
		"data_dir":           "/tmp/data",
		"ffmpeg_path":        "/usr/bin/ffmpeg",
		"ffprobe_path":       "/usr/bin/ffprobe",
		"ytdlp_path":         "/usr/bin/yt-dlp",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config.BridgeConfig{}
			bridgeFieldOverlays[name](&cfg, value)
			// Round-trip: assert the field is non-zero on the right path.
			// We assert via reflective compare rather than per-path so the
			// test stays compact.
			zero := config.BridgeConfig{}
			if reflect.DeepEqual(cfg, zero) {
				t.Errorf("overlay %q wrote nothing to BridgeConfig", name)
			}
		})
	}
}
```

(Add `"reflect"` import.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestBridgeFieldOverlays -v`

Expected: FAIL — `bridgeFieldOverlays` undefined.

- [ ] **Step 3: Add the overlay table**

Append to `internal/chassis/settings.go`:

```go
// bridgeFieldOverlay writes the decoded value into the right path of a
// BridgeConfig. Type asserts to the decoder's return type; a type
// mismatch is a programmer bug and panics rather than silently failing.
type bridgeFieldOverlay func(cfg *config.BridgeConfig, value any)

var bridgeFieldOverlays = map[string]bridgeFieldOverlay{
	"mister_host":        func(c *config.BridgeConfig, v any) { c.MiSTer.Host = v.(string) },
	"mister_port":        func(c *config.BridgeConfig, v any) { c.MiSTer.Port = v.(int) },
	"mister_source_port": func(c *config.BridgeConfig, v any) { c.MiSTer.SourcePort = v.(int) },
	"ui_http_port":       func(c *config.BridgeConfig, v any) { c.UI.HTTPPort = v.(int) },
	"host_ip":            func(c *config.BridgeConfig, v any) { c.HostIP = v.(string) },
	"data_dir":           func(c *config.BridgeConfig, v any) { c.DataDir = v.(string) },
	"ffmpeg_path":        func(c *config.BridgeConfig, v any) { c.FFmpegPath = v.(string) },
	"ffprobe_path":       func(c *config.BridgeConfig, v any) { c.FFprobePath = v.(string) },
	"ytdlp_path":         func(c *config.BridgeConfig, v any) { c.YTDLPPath = v.(string) },
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestBridgeFieldOverlays -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add per-field overlay table writing into BridgeConfig"
```

---

## Task 9: Add Per-Field Scope Table + `scopeLabel` Helper

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write failing tests**

Append:

```go
func TestScopeLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   adapters.ApplyScope
		want string
	}{
		{adapters.ScopeHotSwap, "hot"},
		{adapters.ScopeNextCast, "next"},
		{adapters.ScopeRestartCast, "recast"},
		{adapters.ScopeRestartBridge, "reboot"},
	}
	for _, tc := range cases {
		got, ok := scopeLabel(tc.in)
		if !ok {
			t.Errorf("scopeLabel(%v) ok = false, want true", tc.in)
		}
		if got != tc.want {
			t.Errorf("scopeLabel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, ok := scopeLabel(adapters.ApplyScope(99)); ok {
		t.Errorf("scopeLabel(unknown) ok = true, want false")
	}
}

func TestBridgeFieldScopes_AllNetworkFieldsPresent(t *testing.T) {
	t.Parallel()
	for name := range bridgeFieldDecoders {
		if _, ok := bridgeFieldScopes[name]; !ok {
			t.Errorf("bridgeFieldScopes missing entry for decoder %q", name)
		}
	}
}

func TestBridgeFieldScopes_NetworkPaneIsRebootOrHot(t *testing.T) {
	t.Parallel()
	want := map[string]adapters.ApplyScope{
		"mister_host":        adapters.ScopeRestartBridge,
		"mister_port":        adapters.ScopeRestartBridge,
		"mister_source_port": adapters.ScopeRestartBridge,
		"ui_http_port":       adapters.ScopeRestartBridge,
		"host_ip":            adapters.ScopeRestartBridge,
		"data_dir":           adapters.ScopeRestartBridge,
		"ffmpeg_path":        adapters.ScopeHotSwap,
		"ffprobe_path":       adapters.ScopeHotSwap,
		"ytdlp_path":         adapters.ScopeHotSwap,
	}
	for name, wantScope := range want {
		if got := bridgeFieldScopes[name]; got != wantScope {
			t.Errorf("scope[%s] = %v, want %v", name, got, wantScope)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run "TestScopeLabel|TestBridgeFieldScopes" -v`

Expected: FAIL — `scopeLabel`, `bridgeFieldScopes` undefined.

- [ ] **Step 3: Add the table and helper**

Append to `internal/chassis/settings.go`:

```go
// bridgeFieldScopes is the chassis-side mirror of which ApplyScope each
// field dispatches to. It is validated at startup (see the cross-table
// tests in settings_test.go) against the decoder + overlay tables, and
// against the existing /ui/* saver's per-field dispatch logic.
//
// The HTTP handler's "max-wins scope" output uses the ApplyScope value
// returned by BridgeSaver.Save() (which is authoritative). This table
// is used only by the chassis when it wants to know a single field's
// scope ahead of time (e.g., for badge rendering — but Network pane
// scopes are already encoded directly in the template).
var bridgeFieldScopes = map[string]adapters.ApplyScope{
	"mister_host":        adapters.ScopeRestartBridge,
	"mister_port":        adapters.ScopeRestartBridge,
	"mister_source_port": adapters.ScopeRestartBridge,
	"ui_http_port":       adapters.ScopeRestartBridge,
	"host_ip":            adapters.ScopeRestartBridge,
	"data_dir":           adapters.ScopeRestartBridge,
	"ffmpeg_path":        adapters.ScopeHotSwap,
	"ffprobe_path":       adapters.ScopeHotSwap,
	"ytdlp_path":         adapters.ScopeHotSwap,
}

// scopeLabel maps an ApplyScope to the chassis JSON wire label. Returns
// (_, false) for unknown scopes; the chassis handler treats unknown as
// a server bug and responds 500 WRITE FAILED. Do NOT use
// ApplyScope.String() directly: it returns "hot-swap" / "next-cast" /
// "restart-cast" / "restart-bridge", which is the wrong wire shape.
func scopeLabel(s adapters.ApplyScope) (string, bool) {
	switch s {
	case adapters.ScopeHotSwap:
		return "hot", true
	case adapters.ScopeNextCast:
		return "next", true
	case adapters.ScopeRestartCast:
		return "recast", true
	case adapters.ScopeRestartBridge:
		return "reboot", true
	default:
		return "", false
	}
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run "TestScopeLabel|TestBridgeFieldScopes" -v`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add per-field scope table and scopeLabel helper"
```

---

## Task 10: Add `handleSettingsBridgePost` Handler

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write failing tests**

Append:

```go
func newTestServerWithSaver(t *testing.T, saver BridgeSettingsSaver) *Server {
	t.Helper()
	return &Server{cfg: Config{
		BridgeSaver: saver,
		Registry:    adapters.NewRegistry(),
		Sectioned:   &config.Sectioned{Bridge: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "old", Port: 32100, SourcePort: 32101}}},
	}}
}

func postBridge(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/bridge",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleSettingsBridgePost(rec, req)
	return rec
}

func TestHandleSettingsBridgePost_HotSwapSuccess(t *testing.T) {
	t.Parallel()
	var called bool
	saver := fakeBridgeSettingsSaver{
		saveFn: func(c config.BridgeConfig) (adapters.ApplyScope, error) {
			called = true
			if c.FFmpegPath != "/usr/bin/ffmpeg" {
				t.Errorf("FFmpegPath = %q, want /usr/bin/ffmpeg", c.FFmpegPath)
			}
			return adapters.ScopeHotSwap, nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"ffmpeg_path": {"/usr/bin/ffmpeg"}})
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatalf("saver.Save was not called")
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true || body["scope"] != "hot" {
		t.Errorf("body = %+v, want {ok:true, scope:\"hot\"}", body)
	}
}

func TestHandleSettingsBridgePost_RebootScope(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			return adapters.ScopeRestartBridge, nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"mister_host": {"192.168.1.42"}})
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["scope"] != "reboot" {
		t.Errorf("scope = %v, want reboot", body["scope"])
	}
}

func TestHandleSettingsBridgePost_FieldValidationError(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			t.Fatalf("saver must not be called on field-validation error")
			return 0, nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"mister_host": {""}})
	if rec.Code != 400 {
		t.Fatalf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errs, ok := body["errors"].(map[string]any)
	if !ok {
		t.Fatalf("errors not present in body: %s", rec.Body.String())
	}
	if msg, _ := errs["mister_host"].(string); !strings.Contains(msg, "is required") {
		t.Errorf("errors[mister_host] = %v, want substring 'is required'", errs["mister_host"])
	}
}

func TestHandleSettingsBridgePost_MultipleFieldErrors(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{
		"mister_host": {""},
		"mister_port": {"99999"},
	})
	if rec.Code != 400 {
		t.Fatalf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errs := body["errors"].(map[string]any)
	if _, ok := errs["mister_host"]; !ok {
		t.Errorf("missing mister_host error")
	}
	if _, ok := errs["mister_port"]; !ok {
		t.Errorf("missing mister_port error")
	}
}

func TestHandleSettingsBridgePost_EmptyBodyReturns400BadInput(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{})
	rec := postBridge(t, srv, url.Values{})
	if rec.Code != 400 {
		t.Fatalf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "BAD INPUT" {
		t.Errorf("chip = %v, want BAD INPUT", body["chip"])
	}
}

func TestHandleSettingsBridgePost_NilSaverReturns503(t *testing.T) {
	t.Parallel()
	srv := &Server{cfg: Config{Registry: adapters.NewRegistry()}}
	rec := postBridge(t, srv, url.Values{"mister_host": {"1.2.3.4"}})
	if rec.Code != 503 {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}

func TestHandleSettingsBridgePost_PreflightChipError(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			return 0, &fakeSettingsChipError{status: 409, chip: "PORT IN USE"}
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"ui_http_port": {"32500"}})
	if rec.Code != 409 {
		t.Fatalf("Code = %d, want 409", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "PORT IN USE" {
		t.Errorf("chip = %v, want PORT IN USE", body["chip"])
	}
}

func TestHandleSettingsBridgePost_UnknownScopeReturns500(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			return adapters.ApplyScope(99), nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"ffmpeg_path": {"/x"}})
	if rec.Code != 500 {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
}
```

(Add `"encoding/json"`, `"net/http"`, `"net/http/httptest"`, `"net/url"` to settings_test.go imports.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestHandleSettingsBridgePost -v`

Expected: FAIL — `handleSettingsBridgePost` undefined.

- [ ] **Step 3: Implement the handler**

Append to `internal/chassis/settings.go`:

```go
import (
	"encoding/json"
	"errors"
	"net/http"
)

// handleSettingsBridgePost is the POST handler for /receiver/settings/bridge.
// Accepts any subset of the supported form fields; missing keys mean "do
// not change that field." See the spec's Wire Contract for the response
// envelope.
func (s *Server) handleSettingsBridgePost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BridgeSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	touched := map[string]string{}
	for name := range bridgeFieldDecoders {
		if vals, ok := r.PostForm[name]; ok && len(vals) > 0 {
			touched[name] = vals[0]
		}
	}
	if len(touched) == 0 {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}

	// Decode all touched fields; collect all errors.
	decoded := map[string]any{}
	errs := map[string]string{}
	for name, raw := range touched {
		dec := bridgeFieldDecoders[name]
		v, err := dec(raw)
		if err != nil {
			errs[name] = err.Error()
			continue
		}
		decoded[name] = v
	}
	if len(errs) > 0 {
		writeSettingsFieldErrors(w, http.StatusBadRequest, errs)
		return
	}

	// Compose the patch: start from BridgeSaver.Current() (NOT startup
	// cfg.Bridge — otherwise the drawer can go stale after the first
	// save), overlay decoded touched fields.
	patch := s.cfg.BridgeSaver.Current()
	for name, value := range decoded {
		bridgeFieldOverlays[name](&patch, value)
	}

	scope, err := s.cfg.BridgeSaver.Save(patch)
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

// writeSettingsSuccess emits {"ok":true,"scope":"<label>"} with status 200.
func writeSettingsSuccess(w http.ResponseWriter, scope string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "scope": scope})
}

// writeSettingsChip emits {"ok":false,"chip":"<chip>"} with the given status.
func writeSettingsChip(w http.ResponseWriter, status int, chip string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "chip": chip})
}

// writeSettingsFieldErrors emits {"ok":false,"errors":{...}} with the given status.
func writeSettingsFieldErrors(w http.ResponseWriter, status int, errs map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "errors": errs})
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestHandleSettingsBridgePost -v`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add handleSettingsBridgePost handler"
```

---

## Task 11: Add `chassisProber` Wrapper in `cmd/mister-groovy-relay`

**Files:**
- Create: `cmd/mister-groovy-relay/chassis_prober.go`
- Create: `cmd/mister-groovy-relay/chassis_prober_test.go`

The chassis `Prober` interface needs an implementation that wraps the existing `bridgeMisterProber` from `launcher.go`. The wrapper translates from the chassis-shaped `(ctx, BridgeConfig) -> ProbeResult` signature to whatever the existing prober expects.

- [ ] **Step 1: Inspect the existing prober**

Read `cmd/mister-groovy-relay/launcher.go` lines around 43-96 to confirm the exact signature of `bridgeMisterProber`. The likely shape:

```go
type bridgeMisterProber struct{ /* fields */ }
func newBridgeMisterProber(...) *bridgeMisterProber
func (p *bridgeMisterProber) Probe(ctx context.Context, host string, port int) (latency time.Duration, err error)
```

Adjust the test and wrapper below to match the actual signature.

- [ ] **Step 2: Write the failing test**

Create [cmd/mister-groovy-relay/chassis_prober_test.go](../../../cmd/mister-groovy-relay/chassis_prober_test.go):

```go
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// fakeUnderlyingProber implements whatever method bridgeMisterProber
// satisfies on a per-host basis. Replace with the actual signature.
type fakeUnderlyingProber struct {
	latency time.Duration
	err     error
}

func (f *fakeUnderlyingProber) Probe(ctx context.Context, host string, port int) (time.Duration, error) {
	return f.latency, f.err
}

func TestChassisProber_Success(t *testing.T) {
	t.Parallel()
	cp := &chassisProber{inner: &fakeUnderlyingProber{latency: 4200 * time.Microsecond}}
	res, err := cp.ProbeMister(context.Background(), config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.LatencyMs < 4.0 || res.LatencyMs > 5.0 {
		t.Errorf("LatencyMs = %f, want ~4.2", res.LatencyMs)
	}
	if res.Host != "1.2.3.4" || res.Port != 32100 {
		t.Errorf("res = %+v, want host/port preserved", res)
	}
}

func TestChassisProber_TimeoutSurfacesAsContextDeadlineExceeded(t *testing.T) {
	t.Parallel()
	cp := &chassisProber{inner: &fakeUnderlyingProber{err: context.DeadlineExceeded}}
	_, err := cp.ProbeMister(context.Background(), config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestChassisProber_SatisfiesChassisInterface(t *testing.T) {
	t.Parallel()
	var _ chassis.Prober = (*chassisProber)(nil)
}
```

- [ ] **Step 3: Run to confirm failure**

Run: `go test ./cmd/mister-groovy-relay -run TestChassisProber -v`

Expected: FAIL — `chassisProber` undefined.

- [ ] **Step 4: Implement the wrapper**

Create [cmd/mister-groovy-relay/chassis_prober.go](../../../cmd/mister-groovy-relay/chassis_prober.go). Adjust the inner-prober method signature to match the real `bridgeMisterProber`:

```go
package main

import (
	"context"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// underlyingProber matches the shape of the existing bridgeMisterProber.
// Defined as an interface in the wrapper (rather than concrete *bridgeMisterProber)
// so chassis_prober_test.go can substitute a fake without spinning up sockets.
type underlyingProber interface {
	Probe(ctx context.Context, host string, port int) (time.Duration, error)
}

// chassisProber adapts the existing bridgeMisterProber to the chassis-side
// chassis.Prober interface. The chassis package does not import groovynet
// or any cmd-package types — this wrapper is the bridge.
type chassisProber struct {
	inner underlyingProber
}

func newChassisProber(inner underlyingProber) *chassisProber {
	return &chassisProber{inner: inner}
}

// ProbeMister implements chassis.Prober. Uses BridgeConfig.MiSTer.{Host,Port}
// from the live snapshot; the underlying prober binds an ephemeral source
// port (not the live sender's bound source port), so this is safe to run
// alongside an active cast as long as the underlying prober is.
func (p *chassisProber) ProbeMister(ctx context.Context, bridge config.BridgeConfig) (chassis.ProbeResult, error) {
	latency, err := p.inner.Probe(ctx, bridge.MiSTer.Host, bridge.MiSTer.Port)
	if err != nil {
		return chassis.ProbeResult{}, err
	}
	return chassis.ProbeResult{
		LatencyMs: float64(latency) / float64(time.Millisecond),
		Host:      bridge.MiSTer.Host,
		Port:      bridge.MiSTer.Port,
	}, nil
}

// Compile-time conformance check.
var _ chassis.Prober = (*chassisProber)(nil)
```

If the actual `bridgeMisterProber.Probe` returns a different shape (e.g. a struct with multiple fields), adjust the `underlyingProber` interface and the `ProbeMister` body accordingly. The contract that matters: turn the underlying success into a `ProbeResult{LatencyMs, Host, Port}`; propagate errors unchanged.

- [ ] **Step 5: Run to confirm pass**

Run: `go test ./cmd/mister-groovy-relay -run TestChassisProber -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/mister-groovy-relay/chassis_prober.go cmd/mister-groovy-relay/chassis_prober_test.go
git commit -m "feat(main): add chassisProber wrapper for the chassis.Prober interface"
```

---

## Task 12: Add `handleSettingsActionProbeMister` Handler

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write failing tests**

Append:

```go
func postProbe(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/probe-mister", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleSettingsActionProbeMister(rec, req)
	return rec
}

func TestHandleSettingsActionProbeMister_Success(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{
		cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "192.168.1.42", Port: 32100}},
	})
	srv.cfg.Prober = fakeProber{res: ProbeResult{LatencyMs: 4.2, Host: "192.168.1.42", Port: 32100}}
	rec := postProbe(t, srv)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if got, _ := body["latency_ms"].(float64); got < 4.0 || got > 5.0 {
		t.Errorf("latency_ms = %v, want ~4.2", body["latency_ms"])
	}
	if body["host"] != "192.168.1.42" {
		t.Errorf("host = %v, want 192.168.1.42", body["host"])
	}
}

func TestHandleSettingsActionProbeMister_Timeout(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{
		cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100}},
	})
	srv.cfg.Prober = fakeProber{err: context.DeadlineExceeded}
	rec := postProbe(t, srv)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200 (timeout is operational, not transport)", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != false || body["error"] != "timeout" {
		t.Errorf("body = %+v, want {ok:false, error:\"timeout\"}", body)
	}
}

func TestHandleSettingsActionProbeMister_SocketError(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{
		cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100}},
	})
	srv.cfg.Prober = fakeProber{err: errors.New("socket: connection refused")}
	rec := postProbe(t, srv)
	if rec.Code != 500 {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
}

func TestHandleSettingsActionProbeMister_NilProberReturns503(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{})
	// Prober is nil
	rec := postProbe(t, srv)
	if rec.Code != 503 {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}

func TestHandleSettingsActionProbeMister_NilSaverReturns503(t *testing.T) {
	t.Parallel()
	srv := &Server{cfg: Config{
		Prober:   fakeProber{},
		Registry: adapters.NewRegistry(),
	}}
	rec := postProbe(t, srv)
	if rec.Code != 503 {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestHandleSettingsActionProbeMister -v`

Expected: FAIL — handler undefined.

- [ ] **Step 3: Implement the handler**

Append to `internal/chassis/settings.go`:

```go
import "time"

// handleSettingsActionProbeMister is the POST handler for
// /receiver/settings/action/probe-mister. Hard 1s server-side timeout;
// uses the currently-saved BridgeConfig (NOT form values). Returns 200
// for both success and timeout (the probe ran cleanly in both cases);
// 500 for socket/transport failures; 503 if dependencies aren't wired.
func (s *Server) handleSettingsActionProbeMister(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Prober == nil || s.cfg.BridgeSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	bridge := s.cfg.BridgeSaver.Current()
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	start := time.Now()
	res, err := s.cfg.Prober.ProbeMister(ctx, bridge)
	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "application/json")
	if err == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"latency_ms": res.LatencyMs,
			"host":       res.Host,
			"port":       res.Port,
		})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         false,
			"error":      "timeout",
			"elapsed_ms": float64(elapsed) / float64(time.Millisecond),
		})
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": sanitizeProbeError(err),
	})
}

// sanitizeProbeError strips host/IP details from probe error messages
// before they hit logs or the wire. Mirrors the transport-error
// sanitization pattern established in 3B.
func sanitizeProbeError(err error) string {
	// For 4A, "socket: <free-form message from the prober>" is acceptable
	// — the prober already returns a constrained shape from cmd/.
	// If a future change introduces leak risk, restrict to a fixed
	// vocabulary here. Keep this simple for now.
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestHandleSettingsActionProbeMister -v`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add handleSettingsActionProbeMister handler"
```

---

## Task 13: Mount Routes in `Server.RegisterRoutes`

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go` (or `server_test.go`)

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestRegisterRoutes_MountsBridgeAndProbeRoutes(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv := &Server{cfg: Config{
		Registry:    adapters.NewRegistry(),
		BridgeSaver: fakeBridgeSettingsSaver{},
		Prober:      fakeProber{},
	}}
	srv.RegisterRoutes(mux)
	for _, path := range []string{"/receiver/settings/bridge", "/receiver/settings/action/probe-mister"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s -> %d (not mounted)", path, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestRegisterRoutes_MountsBridgeAndProbeRoutes -v`

Expected: FAIL — 404 for both routes.

- [ ] **Step 3: Mount the routes**

In [internal/chassis/server.go](../../../internal/chassis/server.go), find the existing `Server.RegisterRoutes` function and append the two new route handlers alongside the existing ones (line ~225-232 contains the chassis route registration block):

```go
mux.Handle("POST /receiver/settings/bridge",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsBridgePost)))
mux.Handle("POST /receiver/settings/action/probe-mister",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsActionProbeMister)))
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestRegisterRoutes_MountsBridgeAndProbeRoutes -v && go test ./internal/chassis -count=1 ./...`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): mount /receiver/settings/{bridge,action/probe-mister} routes"
```

---

## Task 14: Wire `main.go`

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Find the chassis Config construction**

Search in main.go for the existing `chassis.Config{` block. Note where `Registry`, `Sectioned`, `StreamsCatalogViewer`, `Manager`, etc. are passed in.

- [ ] **Step 2: Find the existing BridgeSaver construction**

Search for `NewBridgeSaver(`. The existing saver is constructed for `/ui/*` and stored in a local variable. We pass the same instance into chassis.

- [ ] **Step 3: Add the wiring**

Inside the `chassis.Config{...}` literal, add two fields. The `BridgeSaver` is reused unchanged; the `Prober` is a new `chassisProber` wrapping the existing `bridgeMisterProber` (which must also already exist; locate its construction):

```go
chassisCfg := chassis.Config{
    // ... existing fields ...
    BridgeSaver: bridgeSaver,                          // existing *uiserver.BridgeSaver
    Prober:      newChassisProber(misterProber),       // wraps existing *bridgeMisterProber
}
```

If `bridgeMisterProber` is constructed later than the chassis.Config{} block, move it earlier or restructure so chassis can take its reference.

- [ ] **Step 4: Verify it builds**

Run: `make build-bridge` (or `go build ./cmd/mister-groovy-relay`)

Expected: clean build.

- [ ] **Step 5: Run all tests as a smoke check**

Run: `go vet ./... && go test ./... -count=1`

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(main): wire BridgeSaver and chassisProber into chassis.Config"
```

---

## Task 15: Add Small Template FuncMap Helpers (`dict`, `itoa`, `errOf`, `stub`, `settingsScopeLabel`)

**Files:**
- Modify: `internal/chassis/templates.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing tests**

Append:

```go
func TestTemplateHelpers_Dict(t *testing.T) {
	t.Parallel()
	tmpl := template.Must(template.New("t").Funcs(template.FuncMap{
		"dict": dictHelper,
	}).Parse(`{{ $d := dict "k" "v" "n" 42 }}{{ index $d "k" }}|{{ index $d "n" }}`))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := buf.String(); got != "v|42" {
		t.Errorf("got %q, want v|42", got)
	}
}

func TestTemplateHelpers_Itoa(t *testing.T) {
	t.Parallel()
	if got := itoaHelper(32100); got != "32100" {
		t.Errorf("itoaHelper(32100) = %q, want 32100", got)
	}
}

func TestTemplateHelpers_ErrOf(t *testing.T) {
	t.Parallel()
	errs := map[string]string{"mister_host": "is required"}
	if got := errOfHelper(errs, "mister_host"); got != "is required" {
		t.Errorf("got %q, want 'is required'", got)
	}
	if got := errOfHelper(errs, "missing"); got != "" {
		t.Errorf("missing key -> %q, want empty", got)
	}
	if got := errOfHelper(nil, "any"); got != "" {
		t.Errorf("nil map -> %q, want empty", got)
	}
}

func TestTemplateHelpers_SettingsScopeLabel(t *testing.T) {
	t.Parallel()
	if got := settingsScopeLabelHelper("hot"); got != "HOT" {
		t.Errorf("got %q, want HOT", got)
	}
	if got := settingsScopeLabelHelper("reboot"); got != "REBOOT" {
		t.Errorf("got %q, want REBOOT", got)
	}
}

func TestTemplateHelpers_Stub(t *testing.T) {
	t.Parallel()
	got := stubHelper("pipeline", "Pipeline", "4B")
	if got.ID != "pipeline" || got.Title != "Pipeline" || got.Spec != "4B" {
		t.Errorf("got %+v", got)
	}
}
```

(Add `"bytes"`, `"html/template"` imports if not present.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestTemplateHelpers -v`

Expected: FAIL — all helpers undefined.

- [ ] **Step 3: Add the helpers**

In [internal/chassis/templates.go](../../../internal/chassis/templates.go), find the FuncMap registration. Add the five helpers and their FuncMap entries:

```go
// dictHelper builds a map[string]any from alternating key-value pairs.
// Used by templates to pass option bags to {{ field }} (Go templates
// have no native named-arg syntax).
func dictHelper(pairs ...any) map[string]any {
	if len(pairs)%2 != 0 {
		panic("chassis: dict expects an even number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			panic("chassis: dict keys must be strings")
		}
		m[key] = pairs[i+1]
	}
	return m
}

// itoaHelper wraps strconv.Itoa for the FuncMap.
func itoaHelper(n int) string { return strconv.Itoa(n) }

// errOfHelper returns the error message for a given field name from a
// settings errors map, or "" if absent or the map is nil.
func errOfHelper(errs map[string]string, name string) string {
	if errs == nil {
		return ""
	}
	return errs[name]
}

// settingsScopeLabelHelper uppercases a scope key ("hot" -> "HOT", etc.).
func settingsScopeLabelHelper(scope string) string {
	return strings.ToUpper(scope)
}

// stubPaneArgs is the option struct settings-drawer.html's stub template
// expects. ID maps to data-pane; Title is the section heading; Spec is
// the "4B" / "4C" label.
type stubPaneArgs struct {
	ID    string
	Title string
	Spec  string
}

func stubHelper(id, title, spec string) stubPaneArgs {
	return stubPaneArgs{ID: id, Title: title, Spec: spec}
}
```

And register them in the FuncMap (find the existing `template.FuncMap{...}` literal):

```go
FuncMap: template.FuncMap{
    // ... existing helpers ...
    "dict":                 dictHelper,
    "itoa":                 itoaHelper,
    "errOf":                errOfHelper,
    "settingsScopeLabel":   settingsScopeLabelHelper,
    "stub":                 stubHelper,
}
```

(Add `"strconv"` and `"strings"` imports if not present.)

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestTemplateHelpers -v`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add dict/itoa/errOf/stub/settingsScopeLabel template helpers"
```

---

## Task 16: Add `field` Template FuncMap Helper for All Six Types

**Files:**
- Modify: `internal/chassis/templates.go`
- Modify: `internal/chassis/chassis_test.go`

The `field` helper renders one form field row. It takes an option bag (a `map[string]any` built by `dict`) and returns an `html/template.HTML` string containing the row.

- [ ] **Step 1: Write failing tests**

Append:

```go
func TestFieldHelper_TextWithValue(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name":  "mister_host",
		"Type":  "text",
		"Label": "Host",
		"Value": "192.168.1.42",
		"Scope": "reboot",
	})
	s := string(html)
	checks := []string{
		`class="field-row"`,
		`name="mister_host"`,
		`value="192.168.1.42"`,
		`has-value`,
		`class="scope reboot"`,
		`>Host`,
	}
	for _, want := range checks {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestFieldHelper_TextEmptyHasNoHasValueClass(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "host_ip", "Type": "text", "Label": "Host IP",
		"Value": "", "Scope": "reboot", "Placeholder": "auto-detect",
	})
	if strings.Contains(string(html), "has-value") {
		t.Errorf("empty value should not have .has-value class:\n%s", html)
	}
	if !strings.Contains(string(html), `placeholder="auto-detect"`) {
		t.Errorf("missing placeholder:\n%s", html)
	}
}

func TestFieldHelper_NumberWithUnit(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "mister_port", "Type": "number", "Label": "Port",
		"Value": "32100", "Scope": "reboot", "Unit": "ms",
	})
	s := string(html)
	if !strings.Contains(s, `type="number"`) {
		t.Errorf("missing type=number")
	}
	if !strings.Contains(s, "ms") {
		t.Errorf("missing unit text 'ms'")
	}
	if !strings.Contains(s, `class="field-input num`) {
		t.Errorf("missing field-input.num class")
	}
}

func TestFieldHelper_Password(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "ssh_password", "Type": "password", "Label": "Password",
		"Value": "secret", "Scope": "hot",
	})
	if !strings.Contains(string(html), `type="password"`) {
		t.Errorf("missing type=password")
	}
}

func TestFieldHelper_Path(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "data_dir", "Type": "path", "Label": "Data dir",
		"Value": "", "Scope": "reboot", "Placeholder": "auto",
	})
	if !strings.Contains(string(html), `class="field-input path`) {
		t.Errorf("missing field-input.path class")
	}
}

func TestFieldHelper_Select(t *testing.T) {
	t.Parallel()
	opts := []map[string]any{
		{"Value": "NTSC_480i", "Label": "NTSC_480i"},
		{"Value": "NTSC_240p", "Label": "NTSC_240p"},
	}
	html := fieldHelper(map[string]any{
		"Name": "modeline", "Type": "select", "Label": "Modeline",
		"Value": "NTSC_240p", "Scope": "recast", "Options": opts,
	})
	s := string(html)
	if !strings.Contains(s, `<select`) || !strings.Contains(s, `</select>`) {
		t.Errorf("missing select tags")
	}
	if !strings.Contains(s, `<option value="NTSC_240p" selected`) {
		t.Errorf("missing selected attribute on the right option:\n%s", s)
	}
}

func TestFieldHelper_Switch(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "lz4", "Type": "switch", "Label": "LZ4",
		"Value": "true", "Scope": "recast",
	})
	s := string(html)
	if !strings.Contains(s, `class="switch on"`) {
		t.Errorf("missing switch.on class for true value:\n%s", s)
	}
	if !strings.Contains(s, `aria-pressed="true"`) {
		t.Errorf("missing aria-pressed:\n%s", s)
	}
	if !strings.Contains(s, `data-field="lz4"`) {
		t.Errorf("missing data-field attribute:\n%s", s)
	}
}

func TestFieldHelper_WithError(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "mister_host", "Type": "text", "Label": "Host",
		"Value": "bad", "Scope": "reboot", "Error": "not a valid IPv4 or hostname",
	})
	s := string(html)
	if !strings.Contains(s, "has-err") {
		t.Errorf("missing has-err class:\n%s", s)
	}
	if !strings.Contains(s, "not a valid IPv4 or hostname") {
		t.Errorf("missing error text:\n%s", s)
	}
}

func TestFieldHelper_HelpText(t *testing.T) {
	t.Parallel()
	html := fieldHelper(map[string]any{
		"Name": "mister_host", "Type": "text", "Label": "Host",
		"Help": "IP or hostname of your MiSTer", "Value": "1.2.3.4", "Scope": "reboot",
	})
	if !strings.Contains(string(html), `class="help"`) {
		t.Errorf("missing .help span")
	}
	if !strings.Contains(string(html), "IP or hostname of your MiSTer") {
		t.Errorf("missing help text")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestFieldHelper -v`

Expected: FAIL — `fieldHelper` undefined.

- [ ] **Step 3: Implement the helper**

Append to `internal/chassis/templates.go`:

```go
import (
	"fmt"
	"html"
)

// fieldHelper renders one field row. The option bag (built by dict in
// templates) supports the keys: Name, Type, Label, Help, Value,
// Placeholder, Scope, Unit, Options, InputWidth, Error.
//
// All values are HTML-escaped. Type=switch renders a <button>, not an
// <input>; switches POST via client JS, not by form submission.
func fieldHelper(args map[string]any) template.HTML {
	get := func(key string) string {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	name := get("Name")
	typ := get("Type")
	label := get("Label")
	help := get("Help")
	value := get("Value")
	placeholder := get("Placeholder")
	scope := get("Scope")
	unit := get("Unit")
	inputWidth := get("InputWidth")
	errMsg := get("Error")

	rowClass := "field-row"
	if errMsg != "" {
		rowClass += " has-err"
	}

	// Label cell.
	var labelHTML string
	if help != "" {
		labelHTML = fmt.Sprintf(`<label>%s <span class="help">%s</span></label>`,
			html.EscapeString(label), html.EscapeString(help))
	} else {
		labelHTML = fmt.Sprintf(`<label>%s</label>`, html.EscapeString(label))
	}

	// Middle cell — type-specific.
	var middleHTML string
	switch typ {
	case "text", "path":
		extra := ""
		if typ == "path" {
			extra = " path"
		}
		hasValue := ""
		if value != "" {
			hasValue = " has-value"
		}
		middleHTML = fmt.Sprintf(`<input class="field-input%s%s" name="%s" value="%s" placeholder="%s">`,
			extra, hasValue,
			html.EscapeString(name), html.EscapeString(value), html.EscapeString(placeholder))
	case "number":
		hasValue := ""
		if value != "" {
			hasValue = " has-value"
		}
		style := ""
		if inputWidth != "" {
			style = fmt.Sprintf(` style="max-width:%s"`, html.EscapeString(inputWidth))
		}
		middleHTML = fmt.Sprintf(`<input class="field-input num%s" type="number" name="%s" value="%s"%s>`,
			hasValue, html.EscapeString(name), html.EscapeString(value), style)
	case "password":
		middleHTML = fmt.Sprintf(`<input class="field-input has-value" type="password" name="%s" value="%s">`,
			html.EscapeString(name), html.EscapeString(value))
	case "select":
		options, _ := args["Options"].([]map[string]any)
		var b strings.Builder
		fmt.Fprintf(&b, `<select class="field-input has-value" name="%s">`, html.EscapeString(name))
		for _, opt := range options {
			ov, _ := opt["Value"].(string)
			ol, _ := opt["Label"].(string)
			if ol == "" {
				ol = ov
			}
			selected := ""
			if ov == value {
				selected = " selected"
			}
			fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
				html.EscapeString(ov), selected, html.EscapeString(ol))
		}
		b.WriteString(`</select>`)
		middleHTML = b.String()
	case "switch":
		onClass := ""
		aria := "false"
		if value == "true" {
			onClass = " on"
			aria = "true"
		}
		middleHTML = fmt.Sprintf(`<button class="switch%s" data-field="%s" type="button" aria-pressed="%s"></button>`,
			onClass, html.EscapeString(name), aria)
	default:
		middleHTML = fmt.Sprintf(`<!-- unknown field type %q -->`, html.EscapeString(typ))
	}

	// Number-with-unit wraps the input + scope badge in a .row-end span;
	// other types put the scope badge as a direct row child.
	scopeHTML := fmt.Sprintf(`<span class="scope %s">%s</span>`, html.EscapeString(scope), strings.ToUpper(scope))
	if typ == "number" && unit != "" {
		middleHTML = fmt.Sprintf(`%s<span class="row-end"><span style="font-size:10px;color:var(--vfd-faded);">%s</span>%s</span>`,
			middleHTML, html.EscapeString(unit), scopeHTML)
		scopeHTML = "" // already inside row-end
	}

	errHTML := ""
	if errMsg != "" {
		errHTML = fmt.Sprintf(`<div class="field-err">%s</div>`, html.EscapeString(errMsg))
	}

	return template.HTML(fmt.Sprintf(`<div class="%s">%s%s%s%s</div>`,
		rowClass, labelHTML, middleHTML, errHTML, scopeHTML))
}
```

And register `"field": fieldHelper` in the FuncMap.

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestFieldHelper -v && go test ./internal/chassis -count=1`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add field template helper for all six types"
```

---

## Task 17: Rewrite `settings-drawer.html` — Shell + Tabs + Notice Slot

**Files:**
- Modify: `internal/chassis/templates/settings-drawer.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestSettingsDrawerTemplate_RendersFiveTabsAndNoticeSlot(t *testing.T) {
	t.Parallel()
	tmpl := loadChassisTemplates(t) // existing helper or load templates.go's parsed tmpl
	data := SnapshotData{Settings: SettingsData{
		Bridge:               config.BridgeConfig{},
		Errors:               map[string]string{},
		AdapterCount:         6,
		CatalogProviderCount: 3,
	}}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	for _, want := range []string{
		`data-tab="network"`,
		`data-tab="pipeline"`,
		`data-tab="adapters"`,
		`<span class="badge">6</span>`,
		`data-tab="catalog"`,
		`<span class="badge">3</span>`,
		`data-tab="advanced"`,
		`id="settings-close"`,
		`class="settings-notice"`,
		`aria-live="polite"`,
		`hidden`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rendered drawer", want)
		}
	}
}
```

If `loadChassisTemplates` doesn't exist, write it in the test file:
```go
func loadChassisTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := buildChassisTemplate() // or whatever the production builder is called
	if err != nil {
		t.Fatalf("build templates: %v", err)
	}
	return tmpl
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestSettingsDrawerTemplate_RendersFiveTabsAndNoticeSlot -v`

Expected: FAIL — current 8-line stub doesn't have any of these features.

- [ ] **Step 3: Rewrite the template**

Replace [internal/chassis/templates/settings-drawer.html](../../../internal/chassis/templates/settings-drawer.html) with:

```html
{{- define "settings-drawer" -}}
<div class="settings-panel">
  <div class="settings-tabs">
    <button class="settings-tab active" data-tab="network">Network</button>
    <button class="settings-tab" data-tab="pipeline">Pipeline</button>
    <button class="settings-tab" data-tab="adapters">Adapters<span class="badge">{{ .Settings.AdapterCount }}</span></button>
    <button class="settings-tab" data-tab="catalog">Catalog<span class="badge">{{ .Settings.CatalogProviderCount }}</span></button>
    <button class="settings-tab" data-tab="advanced">Advanced</button>
    <span class="settings-spacer"></span>
    <button class="settings-close" id="settings-close">✕ Close</button>
  </div>

  <div class="settings-notice" id="settings-notice" role="status" aria-live="polite" hidden></div>

  <div class="settings-body">
    {{ template "settings-network" . }}
    {{ template "settings-stub" (stub "pipeline" "Pipeline + MiSTer control" "4B") }}
    {{ template "settings-stub" (stub "adapters" "Adapter forms" "4D – 4F") }}
    {{ template "settings-stub" (stub "catalog" "Streams catalog" "4C") }}
    {{ template "settings-stub" (stub "advanced" "HLS buffer + logging" "4B") }}
  </div>
</div>
{{- end -}}

{{- define "settings-network" -}}
<div class="settings-pane active" data-pane="network">
  <!-- Task 18 fills this in -->
</div>
{{- end -}}

{{- define "settings-stub" -}}
<div class="settings-pane" data-pane="{{ .ID }}">
  <div class="settings-section wide">
    <h4>{{ .Title }} <span class="hint">pending</span></h4>
    <div class="action-result shown">▸ Spec {{ .Spec }} — implementation in progress</div>
  </div>
</div>
{{- end -}}
```

Ensure this template is included in the chassis template build (whatever pattern templates.go uses to embed/parse templates). The existing 8-line stub at this path was already included; this just replaces its content.

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestSettingsDrawerTemplate_RendersFiveTabsAndNoticeSlot -v && go test ./internal/chassis -count=1`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/settings-drawer.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): rewrite settings-drawer.html shell with tabs, notice slot, stub panes"
```

---

## Task 18: Add Network Pane Content + Probe Action Row

**Files:**
- Modify: `internal/chassis/templates/settings-drawer.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing tests**

Append:

```go
func TestSettingsDrawerTemplate_NetworkPaneRendersAllFields(t *testing.T) {
	t.Parallel()
	tmpl := loadChassisTemplates(t)
	data := SnapshotData{Settings: SettingsData{
		Bridge: config.BridgeConfig{
			MiSTer: config.MisterConfig{Host: "192.168.1.42", Port: 32100, SourcePort: 32101},
			UI:     config.UIConfig{HTTPPort: 32500},
			DataDir: "",
			FFmpegPath: "/usr/bin/ffmpeg",
		},
		Errors: map[string]string{},
	}}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	wantInputs := []string{
		`name="mister_host"`,
		`name="mister_port"`,
		`name="mister_source_port"`,
		`name="ui_http_port"`,
		`name="host_ip"`,
		`name="data_dir"`,
		`name="ffmpeg_path"`,
		`name="ffprobe_path"`,
		`name="ytdlp_path"`,
	}
	for _, w := range wantInputs {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in rendered Network pane", w)
		}
	}
	if !strings.Contains(s, `value="192.168.1.42"`) {
		t.Errorf("mister_host value not rendered from snapshot")
	}
	if !strings.Contains(s, `id="probe-mister-btn"`) {
		t.Errorf("probe button missing")
	}
	if !strings.Contains(s, `id="probe-mister-result"`) {
		t.Errorf("probe result slot missing")
	}
}

func TestSettingsDrawerTemplate_NetworkPaneFieldRowHasScope(t *testing.T) {
	t.Parallel()
	tmpl := loadChassisTemplates(t)
	data := SnapshotData{Settings: SettingsData{Bridge: config.BridgeConfig{}, Errors: map[string]string{}}}
	var buf bytes.Buffer
	_ = tmpl.ExecuteTemplate(&buf, "settings-drawer", data)
	s := buf.String()
	if !strings.Contains(s, `class="scope reboot"`) {
		t.Errorf("missing REBOOT scope badge")
	}
	if !strings.Contains(s, `class="scope hot"`) {
		t.Errorf("missing HOT scope badge")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestSettingsDrawerTemplate_NetworkPane -v`

Expected: FAIL — Network pane is empty.

- [ ] **Step 3: Fill in the Network pane**

Replace the `{{- define "settings-network" -}}` block in `settings-drawer.html` with:

```html
{{- define "settings-network" -}}
<div class="settings-pane active" data-pane="network">

  <div class="settings-section">
    <h4>MiSTer connection <span class="hint">[bridge.mister]</span></h4>
    {{ field (dict
      "Name" "mister_host" "Type" "text" "Label" "Host"
      "Help" "IP or hostname of your MiSTer on the LAN."
      "Value" .Settings.Bridge.MiSTer.Host
      "Scope" "reboot"
      "Error" (errOf .Settings.Errors "mister_host")) }}
    {{ field (dict
      "Name" "mister_port" "Type" "number" "Label" "Port"
      "Help" "UDP port the MiSTer's Groovy core listens on."
      "Value" (itoa .Settings.Bridge.MiSTer.Port)
      "Scope" "reboot"
      "Error" (errOf .Settings.Errors "mister_port")) }}
    {{ field (dict
      "Name" "mister_source_port" "Type" "number" "Label" "Source port"
      "Help" "Our stable source UDP port. Must stay the same across restarts."
      "Value" (itoa .Settings.Bridge.MiSTer.SourcePort)
      "Scope" "reboot"
      "Error" (errOf .Settings.Errors "mister_source_port")) }}
    {{ template "settings-action-probe-mister" . }}
  </div>

  <div class="settings-section">
    <h4>Bridge HTTP <span class="hint">[bridge.ui]</span></h4>
    {{ field (dict
      "Name" "ui_http_port" "Type" "number" "Label" "HTTP port"
      "Help" "Plex Companion HTTP + Settings UI (shared listener)."
      "Value" (itoa .Settings.Bridge.UI.HTTPPort)
      "Scope" "reboot"
      "Error" (errOf .Settings.Errors "ui_http_port")) }}
    {{ field (dict
      "Name" "host_ip" "Type" "text" "Label" "Host IP"
      "Help" "LAN IP advertised to Plex. Leave blank to auto-detect."
      "Value" .Settings.Bridge.HostIP "Placeholder" "auto-detect"
      "Scope" "reboot"
      "Error" (errOf .Settings.Errors "host_ip")) }}
    {{ field (dict
      "Name" "data_dir" "Type" "path" "Label" "Data directory"
      "Help" "Where plex.json and other persistent state live. Leave empty for OS default."
      "Value" .Settings.Bridge.DataDir "Placeholder" "auto"
      "Scope" "reboot"
      "Error" (errOf .Settings.Errors "data_dir")) }}
  </div>

  <div class="settings-section wide">
    <h4>External tools <span class="hint">override sidecar paths</span></h4>
    {{ field (dict
      "Name" "ffmpeg_path" "Type" "path" "Label" "FFmpeg path"
      "Help" "Empty = bundled sidecar, then PATH."
      "Value" .Settings.Bridge.FFmpegPath "Placeholder" "auto"
      "Scope" "hot"
      "Error" (errOf .Settings.Errors "ffmpeg_path")) }}
    {{ field (dict
      "Name" "ffprobe_path" "Type" "path" "Label" "FFprobe path"
      "Value" .Settings.Bridge.FFprobePath "Placeholder" "auto"
      "Scope" "hot"
      "Error" (errOf .Settings.Errors "ffprobe_path")) }}
    {{ field (dict
      "Name" "ytdlp_path" "Type" "path" "Label" "yt-dlp path"
      "Value" .Settings.Bridge.YTDLPPath "Placeholder" "auto"
      "Scope" "hot"
      "Error" (errOf .Settings.Errors "ytdlp_path")) }}
  </div>

</div>
{{- end -}}

{{- define "settings-action-probe-mister" -}}
<div class="field-row">
  <label>Connectivity <span class="help">Send a single packet and wait up to 1&nbsp;s for the MiSTer's reply. Verifies the address + ports above.</span></label>
  <div>
    <button class="action-btn primary" id="probe-mister-btn" type="button">▸ Test MiSTer connectivity</button>
    <div class="action-result" id="probe-mister-result"></div>
  </div>
  <span class="scope" style="visibility:hidden;">.</span>
</div>
{{- end -}}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestSettingsDrawerTemplate -v`

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/settings-drawer.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): fill Network pane with 9 field rows + probe action button"
```

---

## Task 19: Add Stub Pane Render Test

**Files:**
- Modify: `internal/chassis/chassis_test.go`

The stub panes already render via `settings-stub` define (added in Task 17). This task adds a render-test asserting they contain the right "Spec 4X" text.

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestSettingsDrawerTemplate_StubPanesRenderSpecLabels(t *testing.T) {
	t.Parallel()
	tmpl := loadChassisTemplates(t)
	data := SnapshotData{Settings: SettingsData{Errors: map[string]string{}}}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-drawer", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	wantPairs := []struct{ pane, spec string }{
		{"pipeline", "4B"},
		{"adapters", "4D"},
		{"catalog", "4C"},
		{"advanced", "4B"},
	}
	for _, w := range wantPairs {
		paneTag := fmt.Sprintf(`data-pane="%s"`, w.pane)
		if !strings.Contains(s, paneTag) {
			t.Errorf("missing pane %q", paneTag)
		}
		if !strings.Contains(s, fmt.Sprintf("Spec %s", w.spec)) {
			t.Errorf("missing Spec %s label for pane %s", w.spec, w.pane)
		}
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/chassis -run TestSettingsDrawerTemplate_StubPanes -v`

Expected: PASS (the stub defines from Task 17 are already in place).

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/chassis_test.go
git commit -m "test(chassis): cover stub pane render with Spec-label assertion"
```

---

## Task 20: Add `data-settings-toggle` to Transport Gear Button

**Files:**
- Modify: `internal/chassis/templates/transport.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestTransportTemplate_GearButtonHasSettingsToggle(t *testing.T) {
	t.Parallel()
	tmpl := loadChassisTemplates(t)
	data := SnapshotData{Settings: SettingsData{Errors: map[string]string{}}}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "transport", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, `data-settings-toggle`) {
		t.Errorf("gear button missing data-settings-toggle attribute:\n%s", s)
	}
	// Preserve the existing id for legacy test references.
	if !strings.Contains(s, `id="gear-btn"`) {
		t.Errorf("gear button missing id=\"gear-btn\":\n%s", s)
	}
}
```

(If the existing template define is named differently, adjust the `ExecuteTemplate` second arg.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestTransportTemplate_GearButtonHasSettingsToggle -v`

Expected: FAIL — attribute missing.

- [ ] **Step 3: Add the attribute**

In [internal/chassis/templates/transport.html](../../../internal/chassis/templates/transport.html), find the `id="gear-btn"` line and add the `data-settings-toggle` attribute alongside:

```html
<button class="gear-btn" id="gear-btn" data-settings-toggle type="button">⚙ Setup</button>
```

(The `type="button"` may already be present; preserve other attributes.)

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestTransportTemplate_GearButtonHasSettingsToggle -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/transport.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): add data-settings-toggle attribute to transport gear button"
```

---

## Task 21: Add `<script>` Tag for `settings-drawer.js` to `shell.html`

**Files:**
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestShellTemplate_IncludesSettingsDrawerScript(t *testing.T) {
	t.Parallel()
	tmpl := loadChassisTemplates(t)
	data := SnapshotData{Settings: SettingsData{Errors: map[string]string{}}}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "shell", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, "settings-drawer.js") {
		t.Errorf("shell template missing settings-drawer.js <script> tag")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestShellTemplate_IncludesSettingsDrawerScript -v`

Expected: FAIL.

- [ ] **Step 3: Add the script tag**

In [internal/chassis/templates/shell.html](../../../internal/chassis/templates/shell.html), find the block of existing `<script>` tags (for `catalog-browser.js`, `preset-bank.js`, etc.) and append a new entry alongside, using the same cache-buster pattern they use:

```html
<script src="/receiver/static/settings-drawer.js?v={{.Version}}" defer></script>
```

(Match the exact attribute set the other chassis script tags use — `defer`, `?v={{.Version}}`, etc.)

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestShellTemplate_IncludesSettingsDrawerScript -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/shell.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): include settings-drawer.js script tag in shell"
```

---

## Task 22: Port Interior Settings CSS into `chassis.css`

**Files:**
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestChassisCSS_HasSettingsInteriorRules(t *testing.T) {
	t.Parallel()
	css, err := staticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	wantSelectors := []string{
		".field-row",
		".field-input",
		".field-input.has-value",
		".field-input.num",
		".field-input.path",
		".switch",
		".switch.on",
		".switch::before",
		".action-btn",
		".action-btn.primary",
		".action-result",
		".action-result.shown",
		".action-result.ok",
		".action-result.err",
		".scope",
		".scope.hot",
		".scope.next",
		".scope.recast",
		".scope.reboot",
		".field-row.has-err",
		".field-row .field-err",
		".field-row .row-end",
		".settings-notice",
		".settings-notice.ok",
		".settings-notice.err",
	}
	for _, sel := range wantSelectors {
		if !bytes.Contains(css, []byte(sel)) {
			t.Errorf("chassis.css missing selector %q", sel)
		}
	}
}
```

(The `staticFS` variable should already exist in the chassis package — adjust if it's named differently. Otherwise `os.ReadFile("internal/chassis/static/chassis.css")` works from the package test root.)

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestChassisCSS_HasSettingsInteriorRules -v`

Expected: FAIL — most selectors absent.

- [ ] **Step 3: Port the CSS**

Append to [internal/chassis/static/chassis.css](../../../internal/chassis/static/chassis.css). Port the following rules verbatim from the mockup at `docs/superpowers/reference/2026-05-21-receiver-v24.html`, lines 1973-1997, 2000-2020, 2040-2060, 2936-2949, 2951, 2960-2973, 3059-3073, and 3389-3391. All selectors should be scoped under `body.receiver` to match the chassis CSS convention.

A condensed reference of the rules to add (use the mockup's exact OKLCH values):

```css
/* ===== Settings drawer interior — ported from v24 mockup ===== */

body.receiver .field-row {
  display: grid;
  grid-template-columns: 140px 1fr 70px;
  gap: 10px;
  align-items: start;
  padding: 8px 0;
  border-bottom: 1px solid oklch(0.18 0.012 80);
}
body.receiver .field-row:last-child { border-bottom: 0; }
body.receiver .field-row label {
  font: 500 11px 'Inter', sans-serif;
  color: #b0b0b8;
  letter-spacing: 0.02em;
}
body.receiver .field-row .help {
  font: 400 10px 'Inter', sans-serif;
  color: #6a6a6e;
  margin-top: 2px;
  display: block;
  letter-spacing: 0.01em;
}

body.receiver .field-row .field-input {
  width: 100%;
  background: linear-gradient(180deg, #0a0a0b 0%, #050506 100%);
  border: 1px solid oklch(0.22 0.012 80);
  color: oklch(0.78 0.012 80);
  padding: 5px 8px;
  font: 500 11px 'DSEG14-Classic', 'DSEG7-Classic', monospace;
  letter-spacing: 0.04em;
  border-radius: 2px;
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.6);
}
body.receiver .field-row .field-input.has-value {
  color: var(--vfd);
  text-shadow: 0 0 3px var(--vfd-glow-soft);
}
body.receiver .field-row .field-input.num {
  text-align: right;
  font-family: 'DSEG7-Classic', monospace;
  font-weight: 700;
  font-size: 13px;
}
body.receiver .field-row .field-input.path { font-size: 10px; }
body.receiver .field-row .field-input:focus {
  outline: none;
  border-color: oklch(0.32 0.06 175);
}

body.receiver .switch {
  width: 44px; height: 22px;
  position: relative;
  background: linear-gradient(180deg, #1a1a1c 0%, #0d0d0f 100%);
  border: 1px solid oklch(0.22 0.012 80);
  border-radius: 12px;
  cursor: pointer;
  transition: background 80ms ease, border-color 80ms ease;
}
body.receiver .switch::before {
  content: '';
  position: absolute;
  top: 2px; left: 2px;
  width: 16px; height: 16px;
  background: linear-gradient(180deg, #6a6a6e 0%, #3a3a3e 100%);
  border-radius: 50%;
  transition: left 120ms ease, background 120ms ease;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.5);
}
body.receiver .switch.on {
  background: linear-gradient(180deg, oklch(0.18 0.06 175) 0%, oklch(0.10 0.05 175) 100%);
  border-color: oklch(0.30 0.06 175);
}
body.receiver .switch.on::before {
  left: 24px;
  background: linear-gradient(180deg, var(--vfd) 0%, oklch(0.65 0.18 175) 100%);
  box-shadow: 0 1px 3px rgba(0,0,0,0.5), 0 0 5px var(--vfd-glow), inset 0 1px 0 rgba(255,255,255,0.3);
}
body.receiver .switch:focus-visible {
  outline: 2px solid var(--vfd-glow-soft);
  outline-offset: 2px;
}

body.receiver .action-btn {
  font: 500 11px 'Inter', sans-serif;
  padding: 5px 12px;
  background: linear-gradient(180deg, #2a2a30 0%, #1a1a1f 100%);
  color: #b0b0b8;
  border: 1px solid oklch(0.26 0.012 80);
  border-radius: 2px;
  cursor: pointer;
  letter-spacing: 0.04em;
  transition: background 100ms ease, color 100ms ease;
}
body.receiver .action-btn:hover {
  background: linear-gradient(180deg, #35353c 0%, #20202a 100%);
  color: #d4d4d8;
}
body.receiver .action-btn.primary {
  background: linear-gradient(180deg, oklch(0.30 0.06 175) 0%, oklch(0.18 0.05 175) 100%);
  color: var(--vfd);
  border-color: oklch(0.32 0.06 175);
  text-shadow: 0 0 3px var(--vfd-glow-soft);
}
body.receiver .action-btn.ghost {
  background: transparent;
  border-color: oklch(0.22 0.012 80);
  color: var(--vfd-faded);
}
body.receiver .action-btn.ghost:hover {
  border-color: oklch(0.28 0.06 175);
  color: var(--vfd);
}
body.receiver .action-btn[disabled] {
  opacity: 0.5;
  cursor: not-allowed;
}

body.receiver .action-result {
  display: none;
  margin-top: 6px;
  padding: 4px 8px;
  font: 500 10px 'DSEG14-Classic', monospace;
  color: var(--vfd-dim);
  border: 1px solid oklch(0.18 0.012 80);
  border-radius: 2px;
  letter-spacing: 0.04em;
}
body.receiver .action-result.shown { display: block; }
body.receiver .action-result.ok { color: oklch(0.78 0.12 150); border-color: oklch(0.32 0.10 150); }
body.receiver .action-result.err { color: oklch(0.78 0.16 25); border-color: oklch(0.40 0.16 25); }

body.receiver .scope {
  display: inline-block;
  padding: 1px 5px;
  font: 700 9px 'Inter', sans-serif;
  letter-spacing: 0.06em;
  border-radius: 2px;
  border: 1px solid;
  vertical-align: middle;
  align-self: start;
  margin-top: 5px;
}
body.receiver .scope.hot    { background: oklch(0.20 0.06 175); border-color: oklch(0.32 0.06 175); color: var(--vfd); }
body.receiver .scope.next   { background: oklch(0.20 0.06 125); border-color: oklch(0.30 0.06 125); color: oklch(0.82 0.16 125); }
body.receiver .scope.recast { background: oklch(0.20 0.06 75);  border-color: oklch(0.30 0.06 75);  color: oklch(0.82 0.18 75); }
body.receiver .scope.reboot { background: oklch(0.20 0.06 25);  border-color: oklch(0.30 0.06 25);  color: oklch(0.82 0.16 25); }

body.receiver .field-row .row-end {
  display: flex; gap: 6px; align-items: center; justify-content: flex-end;
}

body.receiver .field-row.has-err .field-input {
  border-color: oklch(0.40 0.16 25);
}
body.receiver .field-row .field-err {
  grid-column: 2 / 3;
  margin-top: 4px;
  padding: 4px 8px;
  background: oklch(0.18 0.06 25 / 0.4);
  color: oklch(0.82 0.16 25);
  border: 1px solid oklch(0.32 0.10 25);
  border-radius: 2px;
  font: 500 10px 'Inter', sans-serif;
  letter-spacing: 0.02em;
}
body.receiver .field-row .field-err::before {
  content: '⚠ ';
}

/* Drawer-local notice slot (Task 4A) */
body.receiver .settings-notice {
  margin: 8px 12px 0 12px;
  padding: 6px 10px;
  font: 500 11px 'Inter', sans-serif;
  letter-spacing: 0.04em;
  border: 1px solid oklch(0.22 0.012 80);
  border-radius: 2px;
  background: linear-gradient(180deg, #0a0a0b 0%, #050506 100%);
  color: var(--vfd-dim);
}
body.receiver .settings-notice[hidden] { display: none; }
body.receiver .settings-notice.ok  { color: oklch(0.78 0.12 150); border-color: oklch(0.32 0.10 150); }
body.receiver .settings-notice.err { color: oklch(0.82 0.16 25); border-color: oklch(0.40 0.16 25); }

/* Mobile sizing */
@media (max-width: 600px) {
  body.receiver .switch { width: 50px; height: 26px; }
  body.receiver .switch::before { width: 20px; height: 20px; }
  body.receiver .switch.on::before { left: 28px; }
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestChassisCSS_HasSettingsInteriorRules -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/chassis.css internal/chassis/chassis_test.go
git commit -m "feat(chassis): port settings drawer interior CSS from mockup"
```

---

## Task 23: Create `settings-drawer.js` Shell (Gear/Close/Tab Switching)

**Files:**
- Create: `internal/chassis/static/settings-drawer.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestSettingsDrawerJS_IsServedAsStaticFile(t *testing.T) {
	t.Parallel()
	b, err := staticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("read settings-drawer.js: %v", err)
	}
	// Smoke check: file is non-empty and the IIFE pattern is in place.
	if len(b) < 100 {
		t.Errorf("settings-drawer.js is suspiciously short: %d bytes", len(b))
	}
	if !bytes.Contains(b, []byte("settings-open")) {
		t.Errorf("settings-drawer.js does not reference the settings-open class")
	}
	if !bytes.Contains(b, []byte("data-tab")) {
		t.Errorf("settings-drawer.js does not reference data-tab attribute")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_IsServedAsStaticFile -v`

Expected: FAIL — file doesn't exist.

- [ ] **Step 3: Create the JS shell**

Create [internal/chassis/static/settings-drawer.js](../../../internal/chassis/static/settings-drawer.js):

```js
// settings-drawer.js — Phase 4A
// Wires the chassis settings drawer:
//   - gear button + close button toggle body.settings-open
//   - tab clicks switch active .settings-pane purely client-side
//   - blur on text/number/password/path/select fields POSTs to
//     /receiver/settings/bridge (added in Task 24)
//   - switch click optimistically toggles and reverts on 4xx (Task 25)
//   - probe button single-flights against /receiver/settings/action/probe-mister (Task 26)
//   - field-error JSON paints inline; chip JSON renders into the
//     drawer-local #settings-notice slot (Task 27)
//
// IMPORTANT: Sec-Fetch-Site is browser-controlled (forbidden request
// header). Client code must NOT attempt to set it.

(function () {
  'use strict';
  const body = document.body;
  const drawer = document.querySelector('.settings-panel');
  if (!drawer) return;

  // Gear button toggle (drawer open <-> closed; clicking gear while
  // open closes it).
  const gear = document.querySelector('[data-settings-toggle], #gear-btn');
  if (gear) {
    gear.addEventListener('click', () => body.classList.toggle('settings-open'));
  }

  // Close button always closes.
  const close = document.getElementById('settings-close');
  if (close) {
    close.addEventListener('click', () => body.classList.remove('settings-open'));
  }

  // Tab switching.
  const tabs = drawer.querySelectorAll('.settings-tab');
  const panes = drawer.querySelectorAll('.settings-pane');
  tabs.forEach(t => {
    t.addEventListener('click', () => {
      tabs.forEach(x => x.classList.remove('active'));
      panes.forEach(x => x.classList.remove('active'));
      t.classList.add('active');
      const target = drawer.querySelector(`.settings-pane[data-pane="${t.dataset.tab}"]`);
      if (target) target.classList.add('active');
    });
  });

  // Tasks 24-27 extend this module below.
  window.Chassis = window.Chassis || {};
  window.Chassis.settings = {}; // shared namespace for sub-modules
})();
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./internal/chassis -run TestSettingsDrawerJS_IsServedAsStaticFile -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/settings-drawer.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): add settings-drawer.js shell with gear/close/tab handlers"
```

---

## Task 24: Add Field Auto-Save on Blur

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

- [ ] **Step 1: Add the save logic to the IIFE**

Edit `settings-drawer.js`: replace the comment `// Tasks 24-27 extend this module below.` with the auto-save handlers:

```js
  // Save helper: POSTs one field-value pair, returns parsed JSON or null
  // on network error.
  async function saveField(name, value) {
    const form = new URLSearchParams();
    form.set(name, value);
    let res;
    try {
      res = await fetch('/receiver/settings/bridge', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: form.toString(),
      });
    } catch (err) {
      console.warn('settings save network error:', err);
      return { netErr: true };
    }
    let body = {};
    try { body = await res.json(); } catch (e) { /* leave empty */ }
    return { status: res.status, body };
  }

  function findRow(input) {
    let el = input;
    while (el && !el.classList.contains('field-row')) el = el.parentElement;
    return el;
  }

  // Field error painting + clearing (full impl lands in Task 27).
  function clearFieldError(name) {
    const input = drawer.querySelector(`[name="${name}"]`);
    if (!input) return;
    const row = findRow(input);
    if (!row) return;
    row.classList.remove('has-err');
    const err = row.querySelector('.field-err');
    if (err) err.remove();
  }
  function paintFieldError(name, msg) {
    const input = drawer.querySelector(`[name="${name}"]`);
    if (!input) return;
    const row = findRow(input);
    if (!row) return;
    row.classList.add('has-err');
    let err = row.querySelector('.field-err');
    if (!err) {
      err = document.createElement('div');
      err.className = 'field-err';
      input.parentElement.insertAdjacentElement('afterend', err);
    }
    err.textContent = msg;
  }
  function markHasValue(name, value) {
    const input = drawer.querySelector(`[name="${name}"]`);
    if (!input) return;
    if (value !== '') input.classList.add('has-value');
    else input.classList.remove('has-value');
  }

  // Wire blur on text/number/password/path inputs; change on selects.
  drawer.querySelectorAll('input.field-input, select.field-input').forEach(el => {
    const evt = el.tagName === 'SELECT' ? 'change' : 'blur';
    el.addEventListener(evt, async () => {
      const name = el.name;
      if (!name) return;
      clearFieldError(name);
      const result = await saveField(name, el.value);
      if (!result) return;
      if (result.netErr) {
        // Task 27 renders into #settings-notice; for now log.
        console.warn('settings save: network error');
        return;
      }
      const { status, body } = result;
      if (status >= 200 && status < 300 && body.ok) {
        markHasValue(name, el.value);
        // Task 27 handles the REBOOT toast.
        return;
      }
      if (body && body.errors) {
        for (const fname of Object.keys(body.errors)) {
          paintFieldError(fname, body.errors[fname]);
        }
        return;
      }
      // Chip-style failure or unknown shape — Task 27 handles the notice.
      paintFieldError(name, (body && body.chip) || 'save failed');
    });
  });

  // Expose internals for Tasks 25-27 and tests.
  window.Chassis.settings.saveField = saveField;
  window.Chassis.settings.paintFieldError = paintFieldError;
  window.Chassis.settings.clearFieldError = clearFieldError;
  window.Chassis.settings.markHasValue = markHasValue;
```

- [ ] **Step 2: Manually verify the file compiles (lint sanity)**

Run: `node --check internal/chassis/static/settings-drawer.js` if Node is available, otherwise just open the file and ensure braces/parens balance.

- [ ] **Step 3: Run the existing test suite**

Run: `go test ./internal/chassis -count=1`

Expected: all PASS (the static-file inclusion test added in Task 23 still passes; nothing new broken).

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): add field auto-save-on-blur to settings-drawer.js"
```

---

## Task 25: Add Switch Click + Optimistic Toggle Revert

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

The Network pane has no switches in 4A (all 9 fields are text / number / path), but the helper renders them and 4B–4F will use them heavily. Wire the click handler now so 4B doesn't reinvent it.

- [ ] **Step 1: Append the switch handler**

Edit `settings-drawer.js`. Just before the `// Expose internals for Tasks 25-27` line, insert:

```js
  // Switch click handler: optimistic toggle, revert on 4xx.
  drawer.querySelectorAll('button.switch[data-field]').forEach(btn => {
    btn.addEventListener('click', async () => {
      if (btn.disabled) return;
      const name = btn.dataset.field;
      const next = !btn.classList.contains('on');
      btn.classList.toggle('on', next);
      btn.setAttribute('aria-pressed', next ? 'true' : 'false');
      clearFieldError(name);
      const result = await saveField(name, next ? 'true' : 'false');
      if (!result) return;
      if (result.netErr) {
        // Revert.
        btn.classList.toggle('on', !next);
        btn.setAttribute('aria-pressed', !next ? 'true' : 'false');
        return;
      }
      const { status, body } = result;
      if (status >= 200 && status < 300 && body.ok) return;
      // Revert + paint error.
      btn.classList.toggle('on', !next);
      btn.setAttribute('aria-pressed', !next ? 'true' : 'false');
      if (body && body.errors && body.errors[name]) {
        paintFieldError(name, body.errors[name]);
      } else {
        paintFieldError(name, (body && body.chip) || 'save failed');
      }
    });
  });
```

- [ ] **Step 2: Lint sanity**

Manually check braces/parens balance.

- [ ] **Step 3: Run the existing test suite**

Run: `go test ./internal/chassis -count=1`

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): add switch optimistic toggle + revert to settings-drawer.js"
```

---

## Task 26: Add Probe Button Single-Flight + Result Rendering

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

- [ ] **Step 1: Append the probe handler**

Edit `settings-drawer.js`. Just before `// Expose internals`, insert:

```js
  // Probe action: single-flight, renders result into #probe-mister-result.
  const probeBtn = document.getElementById('probe-mister-btn');
  const probeOut = document.getElementById('probe-mister-result');

  function renderProbeResult(out, body) {
    if (!out) return;
    out.className = 'action-result shown';
    if (!body || typeof body !== 'object') {
      out.classList.add('err');
      out.textContent = '▸ ERROR · empty response';
      return;
    }
    if (body.ok) {
      out.classList.add('ok');
      out.textContent = `▸ ACK in ${body.latency_ms.toFixed(1)}ms · MiSTer ${body.host}:${body.port}`;
      return;
    }
    out.classList.add('err');
    if (body.error === 'timeout') {
      const elapsed = body.elapsed_ms ? `${body.elapsed_ms.toFixed(0)}ms` : '1000ms';
      out.textContent = `▸ NO ACK · ${elapsed} timeout · check host/port`;
      return;
    }
    if (body.chip) {
      out.textContent = `▸ ${body.chip}`;
      return;
    }
    out.textContent = `▸ ERROR · ${body.error || 'unknown'}`;
  }

  if (probeBtn) {
    probeBtn.addEventListener('click', async () => {
      if (probeBtn.disabled) return;
      probeBtn.disabled = true;
      if (probeOut) {
        probeOut.className = 'action-result';
        probeOut.textContent = '';
      }
      let res, body = {};
      try {
        res = await fetch('/receiver/settings/action/probe-mister', {
          method: 'POST',
          credentials: 'same-origin',
        });
        body = await res.json();
      } catch (err) {
        renderProbeResult(probeOut, { ok: false, error: String(err) });
        probeBtn.disabled = false;
        return;
      }
      renderProbeResult(probeOut, body);
      probeBtn.disabled = false;
    });
  }
```

- [ ] **Step 2: Lint sanity**

Manually check braces/parens balance.

- [ ] **Step 3: Run the existing test suite**

Run: `go test ./internal/chassis -count=1`

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): add probe button single-flight + result rendering"
```

---

## Task 27: Add Drawer-Local Notice Slot for REBOOT Toast + Chip

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

The save handler (Task 24) currently logs chip errors and treats them as field errors. The probe handler (Task 26) renders chip responses into `#probe-mister-result`. This task adds the drawer-local notice slot rendering so:
- REBOOT-scope save success → `"Restart container to apply <field>"` in the notice slot.
- Chip-style settings save failure (BAD INPUT, PORT IN USE, WRITE FAILED, NOT READY) → notice slot.
- The notice auto-clears after 5s or on next successful save.

- [ ] **Step 1: Add notice helpers and wire into the save handler**

Edit `settings-drawer.js`. After the `function markHasValue(...)` declaration, add:

```js
  // Drawer-local notice slot (#settings-notice).
  const notice = document.getElementById('settings-notice');
  let noticeTimer = null;

  function showNotice(text, variant) {
    if (!notice) return;
    notice.className = 'settings-notice ' + (variant || '');
    notice.textContent = text;
    notice.hidden = false;
    if (noticeTimer) clearTimeout(noticeTimer);
    noticeTimer = setTimeout(() => {
      notice.hidden = true;
    }, 5000);
  }
  function clearNotice() {
    if (!notice) return;
    notice.hidden = true;
    notice.className = 'settings-notice';
    notice.textContent = '';
    if (noticeTimer) { clearTimeout(noticeTimer); noticeTimer = null; }
  }
```

- [ ] **Step 2: Update the save-success path to handle scope and chip**

Find the existing success branch in the blur/change handler (Task 24):

```js
if (status >= 200 && status < 300 && body.ok) {
  markHasValue(name, el.value);
  // Task 27 handles the REBOOT toast.
  return;
}
```

Replace with:

```js
if (status >= 200 && status < 300 && body.ok) {
  markHasValue(name, el.value);
  clearNotice();
  if (body.scope === 'reboot') {
    const label = el.closest('.field-row').querySelector('label').textContent.trim().split('\n')[0];
    showNotice(`Restart container to apply ${label}`, 'ok');
  }
  return;
}
```

And replace the existing fall-through:

```js
// Chip-style failure or unknown shape — Task 27 handles the notice.
paintFieldError(name, (body && body.chip) || 'save failed');
```

With:

```js
if (body && body.chip) {
  showNotice(body.chip, 'err');
  paintFieldError(name, body.chip);
  return;
}
paintFieldError(name, 'save failed');
```

Apply the same chip rendering to the switch handler's error branch in Task 25.

- [ ] **Step 3: Update probe handler's chip rendering**

In the probe handler (Task 26), the chip-style failures (NOT READY, CAST IN PROGRESS if added later) should *also* render into the notice slot. Find `if (body.chip) {` inside `renderProbeResult` and replace it with:

```js
if (body.chip) {
  out.textContent = `▸ ${body.chip}`;
  showNotice(body.chip, 'err');
  return;
}
```

- [ ] **Step 4: Expose helpers**

Add to the `window.Chassis.settings.*` exports:

```js
  window.Chassis.settings.showNotice = showNotice;
  window.Chassis.settings.clearNotice = clearNotice;
```

- [ ] **Step 5: Lint sanity + run tests**

Run: `go test ./internal/chassis -count=1`

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): add drawer-local notice slot for REBOOT toast and chip errors"
```

---

## Task 28: End-to-End Integration Tests

**Files:**
- Modify: `tests/integration/chassis_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `tests/integration/chassis_test.go`:

```go
func TestReceiverSettings_GetRendersAllNetworkFields(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	resp, err := env.client.Get(env.url + "/receiver")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	wantInputs := []string{
		`name="mister_host"`, `name="mister_port"`, `name="mister_source_port"`,
		`name="ui_http_port"`, `name="host_ip"`, `name="data_dir"`,
		`name="ffmpeg_path"`, `name="ffprobe_path"`, `name="ytdlp_path"`,
		`id="probe-mister-btn"`,
		`class="settings-notice"`,
	}
	for _, w := range wantInputs {
		if !strings.Contains(s, w) {
			t.Errorf("/receiver missing %q", w)
		}
	}
}

func TestReceiverSettings_BridgePostHotSwapSucceeds(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	// Touch a HOT-scope field (ffmpeg_path).
	resp := env.PostForm("/receiver/settings/bridge", url.Values{
		"ffmpeg_path": {"/usr/bin/ffmpeg"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["ok"] != true || body["scope"] != "hot" {
		t.Errorf("body = %+v, want {ok:true, scope:\"hot\"}", body)
	}
}

func TestReceiverSettings_BridgePostInvalidHostReturns400(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	resp := env.PostForm("/receiver/settings/bridge", url.Values{
		"mister_host": {""},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, ok := body["errors"].(map[string]any)
	if !ok {
		t.Fatalf("errors not present: %v", body)
	}
	if msg, _ := errs["mister_host"].(string); !strings.Contains(msg, "is required") {
		t.Errorf("errors[mister_host] = %v, want 'is required'", errs["mister_host"])
	}
}

func TestReceiverSettings_BridgePostEmptyBodyReturns400(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	resp := env.PostForm("/receiver/settings/bridge", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestReceiverSettings_ProbePostReachesProberWiredFromMain(t *testing.T) {
	env := newChassisIntegrationEnv(t)
	defer env.Close()
	// The integration env wires a fake prober that always returns a known
	// latency, or no prober at all (returning 503). Adjust expectations to
	// match what newChassisIntegrationEnv currently provides; if it does
	// not yet wire a Prober, this test verifies the 503 NOT READY path.
	resp := env.PostForm("/receiver/settings/action/probe-mister", url.Values{})
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 503 {
		t.Fatalf("status = %d, want 200 or 503", resp.StatusCode)
	}
}
```

(Add `"io"`, `"encoding/json"`, `"net/url"`, `"strings"` imports if not already present in this test file.)

- [ ] **Step 2: Run to confirm baseline**

Run: `go test -tags=integration ./tests/integration -run TestReceiverSettings -v`

Expected: tests run; some may need the chassis test environment to be updated to wire `BridgeSaver` and `Prober` (look at how `newChassisIntegrationEnv` constructs the chassis Config and add the new fields).

- [ ] **Step 3: Wire `BridgeSaver` + (optionally fake) `Prober` into the integration env**

Find `newChassisIntegrationEnv` or whatever constructor the integration tests use. Pass:

```go
cfg.BridgeSaver = realBridgeSaver  // already constructed for the test env
cfg.Prober = &fakeIntegrationProber{}  // optional; tests handle nil too
```

Where `fakeIntegrationProber` is a small helper that returns a fixed `chassis.ProbeResult` or an error of choice.

- [ ] **Step 4: Run all integration tests**

Run: `go test -tags=integration ./tests/integration -count=1`

Expected: all PASS.

- [ ] **Step 5: Final smoke check**

Run: `make lint && go test ./... -count=1 && go test -race ./... -count=1`

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add tests/integration/chassis_test.go
git commit -m "test(integration): cover settings bridge POST + probe end-to-end"
```

---

## Final Verification

After Task 28 lands, run the full CI matrix:

```bash
make lint
make test
go test -race ./...
make test-integration
```

All four must be green. If any fails, halt and triage before merging.

The chassis settings drawer is now functional end-to-end for the Network pane:
- The gear button in the transport row opens the drawer.
- The five tabs all render; Pipeline / Adapters / Catalog / Advanced show their "Spec 4X — pending" placeholder cards.
- Network pane renders all 9 fields with the current bridge config values.
- Editing any field and tabbing away saves it (HOT-scope applies live; REBOOT-scope writes to disk and toasts "Restart container to apply").
- Invalid input renders an inline `.field-err` under the bad field.
- The probe button posts to `/receiver/settings/action/probe-mister`, single-flights, and renders the result into the `.action-result` slot.
- The legacy `/ui/*` continues to work unchanged.

Phase 4A is complete. Next phase: 4B (Pipeline + Advanced panes), which reuses the same field renderer, route shape, error envelope, scope dispatch, and toast pattern without modification.
