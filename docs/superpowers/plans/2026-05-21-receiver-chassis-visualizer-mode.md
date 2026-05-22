# Receiver Chassis Visualizer Mode (Phase 1 / Spec 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Import hygiene:** When snippets show imports for an existing Go file, merge the new names into the existing import block and run `gofmt`; do not create a second `import` declaration.

**Goal:** Wire the chassis visualizer-bank buttons to `bridge.visualizer.mode` via a `POST /receiver/visualizer` endpoint, extend the SSE stream with a `visualizer` event for cross-tab sync, and introduce the first chassis cross-origin defence (`Sec-Fetch-Site` middleware).

**Architecture:** Two new narrow chassis interfaces (`VisualizerViewer`, `VisualizerSaver`) wired by `main.go` over `*core.Manager` and `uiserver.BridgeSaver`. The HTTP handler validates locally against `config.SupportedVisualizerModes()` before invoking the saver, so the `main.go` closure adapter is a 3-line passthrough. A `Sec-Fetch-Site` middleware enforces same-origin/same-site POSTs. The SSE diff ticker (Spec 2) gains one extra event name (`visualizer`); the snapshot cache and handler diff state extend by one field. Client-side: a new `visualizer-bank.js` shares Spec 2's `EventSource` through `window.Chassis.events.subscribe('visualizer', handler)`, POSTs on click with a Hybrid CSS pressed-state affordance, and waits for the SSE event to move the `active`/`lit` classes.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `encoding/json`, `embed`, `strings`, `errors`), zero new dependencies. Browser-side `EventSource` + `fetch()` + URLSearchParams. Existing test infrastructure (`go test`, `go test -race`, `go test -tags=integration`).

**Spec:** [docs/superpowers/specs/2026-05-21-receiver-chassis-visualizer-mode-design.md](../specs/2026-05-21-receiver-chassis-visualizer-mode-design.md).

**Spec 2 plan reference (format model):** [docs/superpowers/plans/2026-05-21-receiver-chassis-vfd-live.md](2026-05-21-receiver-chassis-vfd-live.md).

---

## Baseline Check

Spec 4 depends on Spec 2, which has landed in this branch. Before starting Task 1, verify the expected Spec 2 baseline files exist in the checkout:

```bash
ls internal/chassis/events.go
ls internal/chassis/session.go
ls internal/chassis/static/vfd-live.js
```

If any of those are missing, the checkout is on the wrong baseline; switch to the branch that includes Spec 2 before implementing this plan. Spec 2's plan is at `docs/superpowers/plans/2026-05-21-receiver-chassis-vfd-live.md`.

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/chassis/visualizer.go` | `VisualizerViewer` + `VisualizerSaver` interfaces, `handleVisualizerPost` HTTP handler, `isSupportedVisualizerMode` helper, `writeJSONError` / `writeJSONErrorWithMode` helpers |
| `internal/chassis/visualizer_test.go` | Layer 1 tests for handler + interfaces + helpers |
| `internal/chassis/sameorigin.go` | `requireSameOrigin` middleware (~25 lines) |
| `internal/chassis/sameorigin_test.go` | Middleware unit tests |
| `internal/chassis/static/visualizer-bank.js` | EventSource subscriber + click handler |

**Files modified:**

| Path | Change |
|---|---|
| `internal/chassis/server.go` | `Config` gains `VisualizerViewer` + `VisualizerSaver` fields. `Server` gains matching fields. `Mount` registers `POST /receiver/visualizer`. |
| `internal/chassis/session.go` | `snapshotFromSession` signature extended with `VisualizerViewer`; refactored to a single fall-through return; new `liveVisualizerMode` helper. |
| `internal/chassis/events.go` | Add `vizEnvelope` struct. Snapshot diff loop emits `visualizer` event on initial-snapshot + on mode change. |
| `internal/chassis/chassis_test.go` | Existing tests updated for the new `snapshotFromSession` signature; new `TestVisualizerViewer_ManagerSatisfiesInterface`. |
| `internal/chassis/events_test.go` | New tests for visualizer-event emission. |
| `internal/chassis/templates/shell.html` | New `<script defer src="/receiver/static/visualizer-bank.js?v={{.Version}}">` after the existing `vfd-live.js` script tag. |
| `internal/chassis/static/chassis.css` | Adds `body.receiver .viz-btn.pressed { ... }` rules. |
| `internal/chassis/static/vfd-live.js` | Adds a listener-registry-backed `window.Chassis.events.subscribe(name, fn)` helper. `connect()` reattaches registered listeners after each new `EventSource`. |
| `internal/uiserver/bridge_saver.go` | New method `SaveVisualizerMode(mode string) (adapters.ApplyScope, error)`. |
| `internal/uiserver/bridge_saver_test.go` | New test for `SaveVisualizerMode`. |
| `internal/core/manager.go` | New method `VisualizerMode() string`. |
| `internal/core/manager_test.go` | New test for `VisualizerMode()`. |
| `cmd/mister-groovy-relay/main.go` | Adds `visualizerSaverAdapter`; wires `VisualizerViewer` + `VisualizerSaver` into `chassis.Config`. |
| `tests/integration/chassis_test.go` | New integration tests for the POST endpoint + SSE event. |
| `internal/chassis/doc.go` | Append paragraph about the POST endpoint + interfaces + new SSE event. |
| `README.md` | Add troubleshooting note about `Sec-Fetch-Site` requirement for ops scripts. |

**Files unchanged:** Existing chassis templates stay unchanged by Spec 4 except `shell.html`. `internal/chassis/templates/visualizer-bank.html` already carries the right `data-viz` + `active`/`lit` + `aria-checked` attributes (verified in spec). `internal/ui/*` is untouched.

---

## Task 1: Manager.VisualizerMode() method

**Files:**
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/core/manager_test.go`:

```go
func TestManager_VisualizerMode_ReturnsLiveBridgeMode(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)

	// testBridgeConfig (manager_test.go:30) intentionally omits
	// Visualizer.Mode, so the initial value is the zero string.
	// UpdateBridge is the path the saver uses to refresh; this test
	// asserts VisualizerMode reads through it.
	b := testBridgeConfig(t)
	b.Visualizer.Mode = config.VisualizerModeStereoScope
	m.UpdateBridge(b)

	if got := m.VisualizerMode(); got != config.VisualizerModeStereoScope {
		t.Errorf("VisualizerMode() = %q, want %q", got, config.VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerMode_ReturnsEmptyBeforeUpdateBridge(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)
	// testBridgeConfig omits Visualizer.Mode, so newTestManager constructs
	// the Manager with an empty mode. The getter returns whatever is in
	// m.bridge with no defaulting — that's the saver/UI's responsibility,
	// not core's.
	if got := m.VisualizerMode(); got != "" {
		t.Errorf("VisualizerMode() = %q, want empty (testBridgeConfig leaves field unset)", got)
	}
}
```

`newTestManager` and `testBridgeConfig` already exist in the file. If `config` is not imported in the test file already, add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/ -run TestManager_VisualizerMode`
Expected: FAIL — undefined `VisualizerMode` method on `*Manager`.

- [ ] **Step 3: Implement VisualizerMode**

Edit `internal/core/manager.go`. Append the method after the existing `UpdateBridge` definition (search for `func (m *Manager) UpdateBridge`):

```go
// VisualizerMode returns the live bridge's visualizer mode under
// m.mu. Tracks the in-memory bridge updated by UpdateBridge.
// Pure in-memory read; honors the "Manager.mu is never held across
// network I/O" invariant.
func (m *Manager) VisualizerMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bridge.Visualizer.Mode
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/ -run TestManager_VisualizerMode`
Expected: PASS.

Also run the race detector for the new lock:

Run: `go test -race ./internal/core/ -run TestManager_VisualizerMode`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/manager.go internal/core/manager_test.go
git commit -m "$(cat <<'EOF'
feat(core): Manager.VisualizerMode() returns live bridge mode

Phase 1 / Spec 4 task 1. New in-memory getter under m.mu that returns
the visualizer mode from the live bridge config (refreshed by
UpdateBridge). The chassis SSE diff ticker reads this each tick to
emit visualizer events; main.go wires *core.Manager as the
chassis.VisualizerViewer implementation in a later task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: BridgeSaver.SaveVisualizerMode narrow-save method

**Files:**
- Modify: `internal/uiserver/bridge_saver.go`
- Modify: `internal/uiserver/bridge_saver_test.go`

- [ ] **Step 1: Write the failing test**

The existing uiserver tests use a `fakeBridgeCore` struct with an `updated config.BridgeConfig` field and inline `NewBridgeSaver(...)` construction — see `TestBridgeSaver_SaveOutputVolumePreservesLatestBridgeFields` at `internal/uiserver/bridge_saver_test.go:348-378` for the canonical pattern. The `testBridgeConfig` helper in this package takes a modeline string as a second argument: `testBridgeConfig(t, "NTSC_480i")`. `testConfigPath(t)` returns a `t.TempDir()`-backed path.

Append to `internal/uiserver/bridge_saver_test.go`:

```go
func TestBridgeSaver_SaveVisualizerMode_PersistsAndReturnsScope(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	scope, err := s.SaveVisualizerMode(config.VisualizerModeStereoScope)
	if err != nil {
		t.Fatalf("SaveVisualizerMode: %v", err)
	}
	if scope != adapters.ScopeNextCast {
		t.Errorf("scope = %v, want ScopeNextCast", scope)
	}

	// In-memory bridge updated.
	if got := s.Current().Visualizer.Mode; got != config.VisualizerModeStereoScope {
		t.Errorf("Current().Visualizer.Mode = %q, want %q", got, config.VisualizerModeStereoScope)
	}
	// core.Manager notified via UpdateBridge.
	if got := core.updated.Visualizer.Mode; got != config.VisualizerModeStereoScope {
		t.Errorf("core.updated.Visualizer.Mode = %q, want %q", got, config.VisualizerModeStereoScope)
	}
	// Disk-side check (the existing TestBridgeSaver_VisualizerModePersistsVisualizerTable
	// at line 380 already exercises [bridge.visualizer] table-roundtripping via
	// the generic Save() path; this test focuses on the narrow-save semantics
	// matching SaveOutputVolume's contract).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), `mode = "stereo_scope"`) {
		t.Errorf("config.toml does not contain new mode; content:\n%s", string(raw))
	}
}

func TestBridgeSaver_SaveVisualizerMode_PreservesLatestBridgeFields(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	// Mutate an unrelated in-memory field BEFORE invoking the narrow save.
	// The mirror of SaveOutputVolume's "latest snapshot" semantics applies
	// here: the on-disk write must preserve the mutation, not roll back to
	// the original Sectioned snapshot.
	s.sec.Bridge.MiSTer.Host = "198.51.100.9"

	if _, err := s.SaveVisualizerMode(config.VisualizerModeOscilloscopeWave); err != nil {
		t.Fatalf("SaveVisualizerMode: %v", err)
	}

	if got := s.Current().MiSTer.Host; got != "198.51.100.9" {
		t.Errorf("MiSTer.Host = %q, want preserved 198.51.100.9", got)
	}
	if got := s.Current().Visualizer.Mode; got != config.VisualizerModeOscilloscopeWave {
		t.Errorf("Visualizer.Mode = %q, want oscilloscope_wave", got)
	}
}

func TestBridgeSaver_SaveVisualizerMode_RejectsUnsupportedMode(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	_, err := s.SaveVisualizerMode("radial_spectrum")
	if err == nil {
		t.Fatal("expected error for unsupported mode, got nil")
	}
	if !strings.Contains(err.Error(), "bridge.visualizer.mode must be one of") {
		t.Errorf("error = %q, want it to contain validate-prose substring", err.Error())
	}
	// Disk-side check: the file must NOT contain radial_spectrum.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "radial_spectrum") {
		t.Errorf("config.toml unexpectedly contains rejected mode:\n%s", string(raw))
	}
}
```

If `os`, `strings`, `config`, `adapters` are not imported, add them.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/uiserver/ -run TestBridgeSaver_SaveVisualizerMode`
Expected: FAIL — undefined method `SaveVisualizerMode`.

- [ ] **Step 3: Implement SaveVisualizerMode**

Edit `internal/uiserver/bridge_saver.go`. Append the method after the existing `SaveOutputVolume` (search for `func (r *BridgeSaver) SaveOutputVolume`):

```go
// SaveVisualizerMode atomically persists only bridge.visualizer.mode
// against the latest in-memory bridge snapshot. Mirrors the
// SaveOutputVolume pattern so concurrent saves of other fields don't
// race against the in-memory current() snapshot. Returns the applied
// scope (always ScopeNextCast for this field unless a future
// scopeForBridgeField update changes it) and any validation/write
// error.
func (r *BridgeSaver) SaveVisualizerMode(mode string) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.sec.Bridge
	next.Visualizer.Mode = mode
	return r.saveLocked(next)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/uiserver/ -run TestBridgeSaver_SaveVisualizerMode`
Expected: PASS — both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/bridge_saver.go internal/uiserver/bridge_saver_test.go
git commit -m "$(cat <<'EOF'
feat(uiserver): BridgeSaver.SaveVisualizerMode narrow-save method

Phase 1 / Spec 4 task 2. Mirrors the SaveOutputVolume pattern: locks,
builds the candidate Sectioned config from the current in-memory
bridge with only Visualizer.Mode replaced, then delegates to
saveLocked for the full validate → atomic write → UpdateBridge →
scope dispatch pipeline. Returns ScopeNextCast per the existing
scopeForBridgeField map; the active cast is not dropped.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: chassis.VisualizerViewer + VisualizerSaver interfaces

**Files:**
- Create: `internal/chassis/visualizer.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test for interface satisfaction**

Append to `internal/chassis/chassis_test.go`:

```go
func TestVisualizerViewer_ManagerSatisfiesInterface(t *testing.T) {
	t.Parallel()
	// Compile-time + runtime assertion that *core.Manager satisfies
	// the chassis VisualizerViewer interface via its VisualizerMode()
	// method (added in task 1). Catches regressions where Manager's
	// signature changes without the chassis side noticing.
	var _ VisualizerViewer = (*core.Manager)(nil)
}
```

Ensure the file already imports `"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"` (Spec 2's tests added it; merge if needed).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestVisualizerViewer_ManagerSatisfiesInterface`
Expected: FAIL — undefined `VisualizerViewer`.

- [ ] **Step 3: Create visualizer.go with both interfaces**

Create `internal/chassis/visualizer.go`:

```go
// Package chassis — visualizer.go owns the visualizer-mode save +
// SSE-broadcast surface for Phase 1 / Spec 4. The handler lives here;
// the SSE diff-loop integration lives in events.go (Spec 2) which
// reads from the snapshot populated by session.go.
package chassis

// VisualizerViewer is the narrow read-only view of the live bridge's
// visualizer mode. *core.Manager satisfies it structurally via
// VisualizerMode() (added in Spec 4's core change). Tests inject
// fakes. Mirrors Spec 2's SessionViewer pattern.
type VisualizerViewer interface {
	VisualizerMode() string
}

// VisualizerSaver persists a new visualizer mode and refreshes the
// live in-memory bridge config. main.go wires this via a small
// adapter struct over uiserver.BridgeSaver.SaveVisualizerMode so
// chassis doesn't depend on internal/uiserver. The chassis HTTP
// handler validates the mode against config.SupportedVisualizerModes
// before invoking the saver, so this interface does not need a typed
// error for unsupported modes.
type VisualizerSaver interface {
	SaveVisualizerMode(mode string) error
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestVisualizerViewer_ManagerSatisfiesInterface`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/visualizer.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): VisualizerViewer + VisualizerSaver interfaces

Phase 1 / Spec 4 task 3. New visualizer.go declares the narrow
read-only and write-only chassis interfaces for visualizer-mode
state. *core.Manager satisfies VisualizerViewer structurally via the
VisualizerMode() method added in task 1. Subsequent tasks build the
handler + helpers on top of these interfaces; main.go wires the
production implementations in a later task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Config.VisualizerViewer + VisualizerSaver fields

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestServer_StoresVisualizerViewerAndSaverFromConfig(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	saver := &fakeVisualizerSaver{}
	cfg.VisualizerViewer = viewer
	cfg.VisualizerSaver = saver

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.visualizerViewer != viewer {
		t.Errorf("Server.visualizerViewer not stored from Config")
	}
	if s.visualizerSaver != saver {
		t.Errorf("Server.visualizerSaver not stored from Config")
	}
}

// fakeVisualizerViewer is the test double for VisualizerViewer.
// Mutex-guarded so diff-ticker tests in task 8 can flip the mode
// mid-stream deterministically.
type fakeVisualizerViewer struct {
	mu   sync.Mutex
	mode string
}

func (f *fakeVisualizerViewer) VisualizerMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *fakeVisualizerViewer) set(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

// fakeVisualizerSaver records SaveVisualizerMode calls in order and
// optionally returns a configured error.
type fakeVisualizerSaver struct {
	mu    sync.Mutex
	saved []string
	err   error
}

func (f *fakeVisualizerSaver) SaveVisualizerMode(mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, mode)
	return f.err
}

func (f *fakeVisualizerSaver) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.saved))
	copy(out, f.saved)
	return out
}
```

If `sync` or `config` are not imported, add them.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestServer_StoresVisualizerViewerAndSaverFromConfig`
Expected: FAIL — undefined `Config.VisualizerViewer`, undefined `Server.visualizerViewer`.

- [ ] **Step 3: Add the Config + Server fields**

Edit `internal/chassis/server.go`. Update the `Config` struct (preserve existing fields):

```go
// Config is the dependencies bundle passed to New.
type Config struct {
	Bridge    config.BridgeConfig
	Manager   *core.Manager
	Registry  *adapters.Registry
	Version   string
	StartedAt time.Time
	HostIP    string

	// Session is the read-only session-state source for live VFD
	// rendering and SSE events. Optional: nil falls back to idle-only
	// mode. Added by Spec 2; *core.Manager satisfies the interface
	// structurally.
	Session SessionViewer

	// VisualizerViewer reads the live bridge's visualizer mode so the
	// SSE stream + initial page render show the current selection.
	// Optional: nil falls back to cfg.Bridge.Visualizer.Mode (a
	// startup snapshot). *core.Manager satisfies the interface.
	VisualizerViewer VisualizerViewer

	// VisualizerSaver persists chassis-initiated mode changes. Optional:
	// nil makes the POST /receiver/visualizer endpoint return 503
	// (read-only mode). main.go wires a closure over uiserver.BridgeSaver
	// in production.
	VisualizerSaver VisualizerSaver
}
```

Update the `Server` struct in the same file:

```go
// Server owns the chassis runtime state.
type Server struct {
	cfg              Config
	session          SessionViewer  // Spec 2
	visualizerViewer VisualizerViewer
	visualizerSaver  VisualizerSaver
	tmpl             *template.Template
	cssBytes         []byte
	// ... preserve any Spec-2 cache/sync fields that already exist here
}
```

Update `New` to populate the new fields in the existing Spec 2 `s := &Server{...}` literal. Do not replace `New` with an immediate return: preserve the synchronous cache seed, `cache`, and `cacheDone` setup that Spec 2 added.

```go
s := &Server{
	cfg:              cfg,
	session:          cfg.Session,
	visualizerViewer: cfg.VisualizerViewer,
	visualizerSaver:  cfg.VisualizerSaver,
	tmpl:             tmpl,
	cssBytes:         cssBytes,
	cache:            &snapshotCache{},
	cacheDone:        make(chan struct{}),
}
// Preserve Spec 2's cache seed. Task 7 updates this call to pass
// s.visualizerViewer after snapshotFromSession grows the fourth arg.
s.cache.Set(snapshotFromSession(s.cfg, s.session, time.Now()))
return s, nil
```

Note: Spec 2 already added the `session` field; preserve it. The new fields slot in alongside.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestServer_StoresVisualizerViewerAndSaverFromConfig`
Expected: PASS.

Also run the full chassis package suite to confirm no Spec 2 regression:

Run: `go test ./internal/chassis/...`
Expected: PASS — all existing tests + the new one.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): Config + Server gain VisualizerViewer + VisualizerSaver

Phase 1 / Spec 4 task 4. Both fields are optional: nil VisualizerViewer
falls back to cfg.Bridge.Visualizer.Mode (the Phase 0 startup
snapshot), nil VisualizerSaver makes the POST endpoint return 503.
Tests inject fakeVisualizerViewer + fakeVisualizerSaver doubles for
subsequent tasks. *core.Manager will be wired in main.go as the
production VisualizerViewer; a visualizerSaverAdapter wraps
uiserver.BridgeSaver.SaveVisualizerMode in the same wiring task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: JSON-error helpers + isSupportedVisualizerMode

**Files:**
- Modify: `internal/chassis/visualizer.go`
- Create: `internal/chassis/visualizer_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/chassis/visualizer_test.go`:

```go
package chassis

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestIsSupportedVisualizerMode_AcceptsAllSupported(t *testing.T) {
	t.Parallel()
	for _, mode := range config.SupportedVisualizerModes() {
		if !isSupportedVisualizerMode(mode) {
			t.Errorf("isSupportedVisualizerMode(%q) = false, want true", mode)
		}
	}
}

func TestIsSupportedVisualizerMode_RejectsRadialSpectrum(t *testing.T) {
	t.Parallel()
	// radial_spectrum is intentionally deferred from v1 (see
	// feat(ffmpeg): defer radial_spectrum mode from v1) and must not
	// be in SupportedVisualizerModes. The handler relies on this
	// helper to return 400 for it.
	if isSupportedVisualizerMode("radial_spectrum") {
		t.Error("isSupportedVisualizerMode(radial_spectrum) = true, want false (deferred from v1)")
	}
}

func TestIsSupportedVisualizerMode_RejectsArbitraryStrings(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "  ", "STEREO_SCOPE", "stereo-scope", "garbage", "../../../etc/passwd"} {
		if isSupportedVisualizerMode(mode) {
			t.Errorf("isSupportedVisualizerMode(%q) = true, want false", mode)
		}
	}
}

func TestWriteJSONError_FormatsBodyAndHeaders(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	writeJSONError(w, 400, "bad")

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v; body = %q", err, w.Body.String())
	}
	if body["error"] != "bad" {
		t.Errorf("body.error = %q, want \"bad\"", body["error"])
	}
}

func TestWriteJSONErrorWithMode_IncludesMode(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	writeJSONErrorWithMode(w, 400, "unsupported visualizer mode", "radial_spectrum")

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"error":"unsupported visualizer mode"`) {
		t.Errorf("missing error key; body = %s", body)
	}
	if !strings.Contains(body, `"mode":"radial_spectrum"`) {
		t.Errorf("missing mode key; body = %s", body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestIsSupportedVisualizerMode|TestWriteJSONError'`
Expected: FAIL — undefined `isSupportedVisualizerMode`, `writeJSONError`, `writeJSONErrorWithMode`.

- [ ] **Step 3: Implement the helpers**

Edit `internal/chassis/visualizer.go`. Add the imports and helper functions:

```go
package chassis

import (
	"encoding/json"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// ... existing interface definitions stay ...

// isSupportedVisualizerMode returns true when mode is exactly one of
// the values in config.SupportedVisualizerModes(). It does NOT call
// config.NormalizeVisualizerMode (which would convert empty/whitespace
// to retro_analyzer and return true — wrong for input validation):
// the handler is responsible for trimming whitespace and rejecting the
// empty string before calling this helper. Callers outside the handler
// must do the same.
func isSupportedVisualizerMode(mode string) bool {
	for _, supported := range config.SupportedVisualizerModes() {
		if mode == supported {
			return true
		}
	}
	return false
}

// writeJSONError writes a JSON error response with a single "error"
// field. Status is set on the ResponseWriter; Content-Type is set
// before the body. Errors from json.Marshal are silently dropped —
// only string inputs are encoded so marshaling cannot fail in
// practice.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(body)
}

// writeJSONErrorWithMode is writeJSONError plus a "mode" field for
// the client to display which input was rejected. Used by the
// unsupported-mode 400 response so the client knows which button
// the user clicked got rejected.
func writeJSONErrorWithMode(w http.ResponseWriter, status int, msg, mode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"error": msg, "mode": mode})
	_, _ = w.Write(body)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestIsSupportedVisualizerMode|TestWriteJSONError'`
Expected: PASS — all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/visualizer.go internal/chassis/visualizer_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): isSupportedVisualizerMode + JSON-error helpers

Phase 1 / Spec 4 task 5. isSupportedVisualizerMode walks
config.SupportedVisualizerModes() (defensive NormalizeVisualizerMode
call kept idempotent); writeJSONError + writeJSONErrorWithMode produce
the JSON error bodies the POST handler will return in task 9.
Helpers are unexported — only the chassis package uses them.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: requireSameOrigin middleware

**Files:**
- Create: `internal/chassis/sameorigin.go`
- Create: `internal/chassis/sameorigin_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/chassis/sameorigin_test.go`:

```go
package chassis

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireSameOrigin_AllowsSameOrigin(t *testing.T) {
	t.Parallel()
	ok := false
	h := requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=retro_analyzer"))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !ok {
		t.Error("handler not called; same-origin should be accepted")
	}
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestRequireSameOrigin_AllowsSameSite(t *testing.T) {
	t.Parallel()
	ok := false
	h := requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ok = true }))
	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", nil)
	req.Header.Set("Sec-Fetch-Site", "same-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !ok {
		t.Error("handler not called; same-site should be accepted")
	}
}

func TestRequireSameOrigin_BlocksNone(t *testing.T) {
	t.Parallel()
	called := false
	h := requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", nil)
	req.Header.Set("Sec-Fetch-Site", "none")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if called {
		t.Error("handler should NOT be called when Sec-Fetch-Site is none on POST")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestRequireSameOrigin_BlocksCrossSite(t *testing.T) {
	t.Parallel()
	called := false
	h := requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if called {
		t.Error("handler should NOT be called for cross-site")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestRequireSameOrigin_BlocksMissingHeader(t *testing.T) {
	t.Parallel()
	called := false
	h := requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", nil)
	// No Sec-Fetch-Site header.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if called {
		t.Error("handler should NOT be called when Sec-Fetch-Site is absent")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestRequireSameOrigin_BlocksReturns403JSON(t *testing.T) {
	t.Parallel()
	h := requireSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if body := w.Body.String(); !strings.Contains(body, `"error":"cross-site request blocked"`) {
		t.Errorf("body = %q; want JSON error", body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestRequireSameOrigin`
Expected: FAIL — undefined `requireSameOrigin`.

- [ ] **Step 3: Implement the middleware**

Create `internal/chassis/sameorigin.go`:

```go
package chassis

import "net/http"

// requireSameOrigin rejects POST requests whose Sec-Fetch-Site is not
// same-origin or same-site. All current browsers send Sec-Fetch-Site
// on every fetch request; the chassis targets modern browsers only
// (Phase 0's container queries already require Safari 16+).
//
// Accepts: same-origin, same-site.
// Rejects: cross-site, none, missing header.
//
// "none" is deliberately rejected for POST endpoints even though it's
// a legitimate browser value for top-level navigations (typed URL,
// bookmark click). Top-level navigations are GETs; a POST that arrives
// with Sec-Fetch-Site: none is not a real browser flow, so rejecting
// it tightens the accept surface without breaking any real user path.
//
// This is intentionally stricter than the existing /ui/* CSRF
// middleware (which accepts "none", has an extension bypass, and an
// Origin fallback): chassis POST endpoints are first-party console
// controls driven by bundled JS only.
//
// Non-browser clients (curl, ops scripts) can opt in by setting the
// header explicitly: `-H "Sec-Fetch-Site: same-origin"`. Documented
// in the README troubleshooting section.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Sec-Fetch-Site") {
		case "same-origin", "same-site":
			next.ServeHTTP(w, r)
		default:
			writeJSONError(w, http.StatusForbidden, "cross-site request blocked")
		}
	})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestRequireSameOrigin`
Expected: PASS — all six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/sameorigin.go internal/chassis/sameorigin_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): requireSameOrigin Sec-Fetch-Site middleware

Phase 1 / Spec 4 task 6. First chassis cross-origin defence. Accepts
same-origin and same-site; rejects cross-site, none, and missing
header. Intentionally stricter than the /ui/* CSRF middleware
(no none-acceptance, no extension bypass, no Origin fallback) because
chassis POST endpoints are first-party JS only. Spec 3 (transport)
will reuse this middleware when it lands; the file stays in
internal/chassis/ until a third consumer justifies extraction.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: snapshotFromSession refactor + visualizer override

**Files:**
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/chassis_test.go`:

```go
func TestSnapshotFromSession_VisualizerModeOverridesIdleDefault(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	got := snapshotFromSession(cfg, nil, viewer, fixedNow)

	if got.Visualizer.ActiveMode != config.VisualizerModeStereoScope {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (viewer overrides cfg.Bridge default)",
			got.Visualizer.ActiveMode, config.VisualizerModeStereoScope)
	}
}

func TestSnapshotFromSession_NilVisualizerViewerFallsBackToCfg(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave

	got := snapshotFromSession(cfg, nil, nil, fixedNow)

	if got.Visualizer.ActiveMode != config.VisualizerModeOscilloscopeWave {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (nil viewer falls back to cfg.Bridge)",
			got.Visualizer.ActiveMode, config.VisualizerModeOscilloscopeWave)
	}
}

func TestSnapshotFromSession_NormalizesEmptyMode(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	viewer := &fakeVisualizerViewer{mode: ""}
	got := snapshotFromSession(cfg, nil, viewer, fixedNow)

	// NormalizeVisualizerMode defaults empty input to retro_analyzer.
	if got.Visualizer.ActiveMode != config.VisualizerModeRetroAnalyzer {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (empty viewer mode should normalize to retro_analyzer)",
			got.Visualizer.ActiveMode, config.VisualizerModeRetroAnalyzer)
	}
}

func TestSnapshotFromSession_LiveStateOverridesIdleDefaults_StillSetsVisualizer(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:    core.StatePlaying,
		Title:    "First Day on MTV",
		Source:   "plex",
		Position: 4*time.Minute + 23*time.Second,
		Duration: 9*time.Minute + 56*time.Second,
	}}
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	got := snapshotFromSession(cfg, sv, viewer, fixedNow)

	if got.State != StateLive {
		t.Errorf("State = %q, want %q", got.State, StateLive)
	}
	if got.Visualizer.ActiveMode != config.VisualizerModeStereoScope {
		t.Errorf("Visualizer.ActiveMode = %q, want %q (live state still applies viz override)",
			got.Visualizer.ActiveMode, config.VisualizerModeStereoScope)
	}
}
```

Note: `fakeSessionViewer` exists in the current Spec 2 baseline. If it doesn't, the checkout is on the wrong baseline; follow the Baseline Check at the top of this plan.

Spec 2 currently has tests using `snapshotFromSession(cfg, sv, fixedNow)` (3-arg). Those tests will break when the signature changes in Step 3. Update them as part of this task — search for `snapshotFromSession(` and add `nil` as the third argument before `fixedNow` in every existing call site:

```go
// Before: snapshotFromSession(cfg, nil, fixedNow)
// After:  snapshotFromSession(cfg, nil, nil, fixedNow)

// Before: snapshotFromSession(cfg, sv, fixedNow)
// After:  snapshotFromSession(cfg, sv, nil, fixedNow)
```

This is a mechanical edit. Verify by grep before Step 3.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession`
Expected: Mix of compile error (3-arg vs 4-arg calls Spec 2 still uses) and FAIL on the new 4-arg-aware tests. The compile error is expected at this point because the existing Spec-2 calls need updating in Step 3.

If only the new tests fail at compile time (and Spec 2's existing tests pass after a grep-and-replace), you have not yet changed `snapshotFromSession`'s signature — proceed to Step 3.

- [ ] **Step 3: Refactor snapshotFromSession to 4-arg + add liveVisualizerMode**

Edit `internal/chassis/session.go`. Replace the entire `snapshotFromSession` function body (currently the 3-arg form Spec 2 added) with the 4-arg fall-through shape:

```go
import (
	"fmt"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// snapshotFromSession builds the page-render data from current bridge
// state. Refactored from Spec 2's three early returns into a single
// fall-through that returns `base` once at the bottom; lets later
// specs (e.g. Spec 5 telemetry) layer overrides at the trailing step
// without duplicating the pattern at every return site.
//
// Spec 4 adds the `vv VisualizerViewer` parameter. When non-nil, the
// live in-memory visualizer mode overrides cfg.Bridge.Visualizer.Mode
// (a startup snapshot) on every refresh tick. When nil, falls back to
// the Phase 0 defaultVisualizerMode(cfg) helper.
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, now time.Time) ReceiverPageData {
	base := idleSnapshot(cfg, now)
	if sv != nil {
		view := sv.StatusHomeView()
		switch view.State {
		case core.StatePlaying, core.StatePaused:
			base.State = StateLive
			base.VFD.State = string(StateLive)
			base.VFD.Title = view.Title
			base.VFD.Marquee = formatLiveMarquee(view)
		default:
			// Preserve Spec 2's fallback: idle and unknown states render
			// as idle, not live.
		}
	}
	base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
	return base
}

// liveVisualizerMode returns the visualizer mode the chassis should
// render right now: the live viewer value when present, otherwise
// the Phase 0 cfg.Bridge fallback. Both go through
// config.NormalizeVisualizerMode so empty/whitespace strings become
// retro_analyzer instead of an empty button selection.
func liveVisualizerMode(cfg Config, vv VisualizerViewer) string {
	if vv == nil {
		return defaultVisualizerMode(cfg)
	}
	return config.NormalizeVisualizerMode(vv.VisualizerMode())
}
```

If Spec 2's `formatLiveMarquee`, `formatPlaybackPosition`, `formatPlaybackDuration`, `formatPlaybackClock` already live in `session.go`, leave them in place. If they live in a different file (e.g. `data.go`), don't move them.

Update every call site to `snapshotFromSession` to pass the viewer. Spec 2's plan adds call sites in at least two production files plus several tests. **Before changing the signature, grep to find all current call sites:**

```bash
grep -rn snapshotFromSession internal/chassis/
```

Spec 2's plan puts call sites in (verify against grep output):

- `internal/chassis/handler.go` — `handleIndex` calls it once.
- `internal/chassis/server.go` — the snapshot-cache seed (in `New`) and the refresher goroutine (started by `Mount`) call it. Whether the refresher lives in `server.go` or `events.go` depends on Spec 2's final placement; the grep tells you.
- `internal/chassis/chassis_test.go` and `events_test.go` — existing tests pass 3 args; mechanically add `nil` as the third arg.

For every match, transform:

```go
// Before (Spec 2):
data := snapshotFromSession(s.cfg, s.session, time.Now())
// After (Spec 4):
data := snapshotFromSession(s.cfg, s.session, s.visualizerViewer, time.Now())
```

Test sites that pass `nil` for the session viewer become:

```go
// Before: snapshotFromSession(cfg, nil, fixedNow)
// After:  snapshotFromSession(cfg, nil, nil, fixedNow)
```

None of these sites need the snapshot-cache structure to change; only the call signature.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession`
Expected: PASS — the four new tests + existing Spec 2 ones.

Run the full chassis test suite:

Run: `go test ./internal/chassis/...`
Expected: PASS — Spec 2 regressions caught by the existing test surface re-run green with the updated call sites.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/session.go internal/chassis/handler.go internal/chassis/events.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): snapshotFromSession takes VisualizerViewer override

Phase 1 / Spec 4 task 7. Adds vv parameter and refactors Spec 2's
three early returns into a single fall-through so future overrides
layer at a trailing step instead of repeating the pattern. The live
viewer's mode overrides cfg.Bridge.Visualizer.Mode (a startup
snapshot) on every refresh tick. Nil viewer falls back to
defaultVisualizerMode (Phase 0). Every existing call site updates
mechanically by passing s.visualizerViewer.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: vizEnvelope + visualizer SSE event emission

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/events_test.go`:

```go
func TestVizEnvelope_JSONCamelCase(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(vizEnvelope{Mode: "stereo_scope"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(body) != `{"mode":"stereo_scope"}` {
		t.Errorf("marshal = %q, want {\"mode\":\"stereo_scope\"}", string(body))
	}
}

func TestHandleEvents_EmitsInitialVisualizerEventOnConnect(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.VisualizerViewer = &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: visualizer\n") {
		t.Errorf("missing initial visualizer event in body:\n%s", body)
	}
	if !strings.Contains(body, `"mode":"stereo_scope"`) {
		t.Errorf("missing stereo_scope mode payload:\n%s", body)
	}
}

func TestHandleEvents_EmitsVisualizerEventOnModeChange(t *testing.T) {
	t.Parallel()
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeRetroAnalyzer}
	cfg := nonZeroConfig()
	cfg.VisualizerViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(150 * time.Millisecond) // > one diff tick (100ms in tests)
		viewer.set(config.VisualizerModeStereoScope)
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	// Initial: retro_analyzer. After mutation: stereo_scope.
	if !strings.Contains(body, `"mode":"retro_analyzer"`) {
		t.Errorf("missing initial retro_analyzer event:\n%s", body)
	}
	if !strings.Contains(body, `"mode":"stereo_scope"`) {
		t.Errorf("missing stereo_scope change event:\n%s", body)
	}
}

func TestHandleEvents_VisualizerEventOmittedWhenModeUnchanged(t *testing.T) {
	t.Parallel()
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeRetroAnalyzer}
	cfg := nonZeroConfig()
	cfg.VisualizerViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(300 * time.Millisecond) // multiple ticks, no mutation
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	// Initial emit fires once; after that, no further visualizer events.
	count := strings.Count(body, "event: visualizer\n")
	if count != 1 {
		t.Errorf("visualizer event count = %d, want 1 (initial only); body:\n%s", count, body)
	}
}
```

If `encoding/json` is not imported in the test file, add it. The flush-recorder + mutable-viewer fixtures should already exist from Spec 2 + task 4.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestVizEnvelope|TestHandleEvents_Emits(Initial)?Visualizer|TestHandleEvents_VisualizerEventOmittedWhenModeUnchanged'`
Expected: FAIL — undefined `vizEnvelope`; visualizer event not emitted.

- [ ] **Step 3: Implement vizEnvelope + diff-loop emission**

Edit `internal/chassis/events.go`. Add the envelope alongside Spec 2's `stateEnvelope` and `vfdEnvelope`:

```go
// vizEnvelope is the payload for the `visualizer` SSE event. Spec 4.
// Same JSON-tag discipline as Spec 2's envelopes — explicit
// json:"mode" so the wire format is camelCase regardless of Go field
// naming.
type vizEnvelope struct {
	Mode string `json:"mode"`
}
```

In the same file, locate the initial-snapshot emission inside `handleEvents` (Spec 2 emits `state` and `vfd` here). After the `vfd` emission, add:

```go
if err := emit(w, "visualizer", vizEnvelope{Mode: last.Visualizer.ActiveMode}); err != nil {
	return
}
```

In the diff-loop body inside the `case <-tick.C:` branch, alongside Spec 2's `state` and `vfd` comparisons, add:

```go
if curr.Visualizer.ActiveMode != last.Visualizer.ActiveMode {
	if err := emit(w, "visualizer", vizEnvelope{Mode: curr.Visualizer.ActiveMode}); err != nil {
		return
	}
	last.Visualizer.ActiveMode = curr.Visualizer.ActiveMode
}
```

The `last` variable is `ReceiverPageData` (Spec 2). `Visualizer.ActiveMode` is already a field on `ReceiverPageData.Visualizer` (Phase 0).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestVizEnvelope|TestHandleEvents_Emits(Initial)?Visualizer|TestHandleEvents_VisualizerEventOmittedWhenModeUnchanged'`
Expected: PASS — all four new tests.

Run the full chassis suite:

Run: `go test ./internal/chassis/...`
Expected: PASS — Spec 2's existing diff-ticker tests still green.

Run with race detector:

Run: `go test -race ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): SSE visualizer event on connect + mode change

Phase 1 / Spec 4 task 8. New vizEnvelope JSON struct + diff-loop
emission. Initial connect emits the current mode alongside state +
vfd; subsequent ticks emit visualizer only when the mode changes.
Cross-tab synchronization is automatic — both open tabs converge
within one diff-ticker interval (250 ms in production, 100 ms in
tests) of a save.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: handleVisualizerPost handler

**Files:**
- Modify: `internal/chassis/visualizer.go`
- Modify: `internal/chassis/visualizer_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/visualizer_test.go`:

```go
import (
	// ... existing imports ...
	"net/http"
	"net/http/httptest"
)

func TestHandleVisualizerPost_Returns204OnSuccess(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=stereo_scope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVisualizerPost(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	calls := saver.calls()
	if len(calls) != 1 || calls[0] != "stereo_scope" {
		t.Errorf("saver calls = %v, want [stereo_scope]", calls)
	}
}

func TestHandleVisualizerPost_Returns400OnMissingOrEmptyModeField(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, _ := New(cfg)

	cases := []struct {
		name string
		body string
	}{
		{"missing", ""},
		{"empty", "mode="},
		{"whitespace", "mode=%20%20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.handleVisualizerPost(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if !strings.Contains(w.Body.String(), `"error":"missing mode field"`) {
				t.Errorf("body = %s, want missing-mode error", w.Body.String())
			}
		})
	}
	if len(saver.calls()) != 0 {
		t.Errorf("saver should not be invoked; calls = %v", saver.calls())
	}
}

func TestHandleVisualizerPost_Returns400OnUnsupportedMode(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, _ := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=bogus_mode"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVisualizerPost(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"error":"unsupported visualizer mode"`) {
		t.Errorf("missing error key; body = %s", body)
	}
	if !strings.Contains(body, `"mode":"bogus_mode"`) {
		t.Errorf("missing mode key; body = %s", body)
	}
}

func TestHandleVisualizerPost_Returns400OnRadialSpectrumDeferred(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, _ := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=radial_spectrum"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVisualizerPost(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (radial_spectrum is deferred from v1)", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"mode":"radial_spectrum"`) {
		t.Errorf("body should echo mode; got %s", w.Body.String())
	}
}

func TestHandleVisualizerPost_DoesNotInvokeSaverForUnsupportedMode(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, _ := New(cfg)

	for _, mode := range []string{"radial_spectrum", "bogus", "STEREO_SCOPE", "stereo-scope"} {
		req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode="+mode))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.handleVisualizerPost(w, req)
	}
	if len(saver.calls()) != 0 {
		t.Errorf("saver invoked for unsupported modes; calls = %v", saver.calls())
	}
}

func TestHandleVisualizerPost_Returns503WhenSaverNil(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = nil
	s, _ := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=stereo_scope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVisualizerPost(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"error":"visualizer save not configured"`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestHandleVisualizerPost_Returns500OnSaverInternalError(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{err: fmt.Errorf("disk full")}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, _ := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=stereo_scope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVisualizerPost(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"error":"internal save failure"`) {
		t.Errorf("missing generic error message; body = %s", body)
	}
	if strings.Contains(body, "disk full") {
		t.Errorf("internal error leaked to client; body = %s", body)
	}
}

func TestHandleVisualizerPost_RapidSequentialClicks(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, _ := New(cfg)

	modes := []string{"retro_analyzer", "oscilloscope_wave", "stereo_scope", "retro_analyzer"}
	for _, mode := range modes {
		req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode="+mode))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.handleVisualizerPost(w, req)
		if w.Code != http.StatusNoContent {
			t.Errorf("status = %d for mode %s, want 204", w.Code, mode)
		}
	}
	calls := saver.calls()
	if !reflect.DeepEqual(calls, modes) {
		t.Errorf("saver calls = %v, want %v (in order)", calls, modes)
	}
}
```

If `fmt`, `reflect`, `strings`, `net/http`, `net/http/httptest` are not imported, add them.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestHandleVisualizerPost`
Expected: FAIL — `handleVisualizerPost` undefined.

- [ ] **Step 3: Implement the handler**

Edit `internal/chassis/visualizer.go`. Append the handler:

```go
import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// handleVisualizerPost is the chassis save endpoint at
// POST /receiver/visualizer. It validates the mode against
// config.SupportedVisualizerModes() before invoking VisualizerSaver,
// so unsupported values never reach the persistence layer.
// uiserver.BridgeSaver.saveLocked still runs full validation as
// defense in depth.
//
// Response shape (per spec §"Save Endpoint"):
//   - 204 No Content on success — SSE event is the success signal
//   - 400 Bad Request for missing/empty/unsupported mode
//   - 503 Service Unavailable when VisualizerSaver is not configured
//   - 500 Internal Server Error for unexpected saver errors
//     (full error logged server-side; client gets a generic message)
//
// Cross-origin protection (Sec-Fetch-Site) is enforced by the
// requireSameOrigin middleware wrapping this handler in Mount; this
// function does not re-check the header.
func (s *Server) handleVisualizerPost(w http.ResponseWriter, r *http.Request) {
	if s.visualizerSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "visualizer save not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}
	mode := strings.TrimSpace(r.PostFormValue("mode"))
	if mode == "" {
		writeJSONError(w, http.StatusBadRequest, "missing mode field")
		return
	}
	if !isSupportedVisualizerMode(mode) {
		writeJSONErrorWithMode(w, http.StatusBadRequest, "unsupported visualizer mode", mode)
		return
	}
	if err := s.visualizerSaver.SaveVisualizerMode(mode); err != nil {
		// Log the full error server-side via stdlib log.Printf (chassis
		// has no structured logger today; introducing one is out of
		// scope for this spec — Phase 5 polish). The client receives
		// only the generic message so internal failures don't leak.
		log.Printf("chassis: visualizer save failed: mode=%q err=%v", mode, err)
		writeJSONError(w, http.StatusInternalServerError, "internal save failure")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

If `config` is unused in this file after this change, leave it imported — `isSupportedVisualizerMode` uses it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestHandleVisualizerPost`
Expected: PASS — all eight handler tests.

Run with race detector:

Run: `go test -race ./internal/chassis/ -run TestHandleVisualizerPost`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/visualizer.go internal/chassis/visualizer_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleVisualizerPost validates + saves visualizer mode

Phase 1 / Spec 4 task 9. The HTTP handler trims/rejects empty,
validates against config.SupportedVisualizerModes (rejects
radial_spectrum and any other unknown value with 400), and only then
invokes VisualizerSaver. Internal save failures log server-side via
log.Printf and return a generic 500 to avoid leaking error details
to the client. Nil saver returns 503. The Sec-Fetch-Site check is
left to requireSameOrigin middleware applied at Mount time.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Mount POST /receiver/visualizer

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/visualizer_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/visualizer_test.go`:

```go
func TestMount_RegistersVisualizerPostWithSameOriginGuard(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mux := http.NewServeMux()
	s.Mount(mux)

	// Cross-site POST is rejected by the middleware before the handler runs.
	req := httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=stereo_scope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site status = %d, want 403", w.Code)
	}
	if len(saver.calls()) != 0 {
		t.Errorf("saver should not be invoked when middleware rejects; calls = %v", saver.calls())
	}

	// Same-origin POST reaches the handler.
	req = httptest.NewRequest(http.MethodPost, "/receiver/visualizer", strings.NewReader("mode=stereo_scope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("same-origin status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	if got := saver.calls(); len(got) != 1 || got[0] != "stereo_scope" {
		t.Errorf("saver calls = %v, want [stereo_scope]", got)
	}
}

func TestMount_GetVisualizerReturns405(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	s, _ := New(cfg)
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/receiver/visualizer", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestMount_RegistersVisualizerPost`
Expected: FAIL — no `/receiver/visualizer` route registered.

- [ ] **Step 3: Register the route in Mount**

Edit `internal/chassis/server.go`. Locate the `Mount` method (Spec 2 added the events route here). Append the new POST registration:

```go
// Mount registers chassis routes on mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /receiver", s.handleIndex)
	mux.HandleFunc("GET /receiver/{$}", s.handleIndex)
	mux.HandleFunc("GET /receiver/static/", s.handleStatic)
	mux.HandleFunc("GET /receiver/events", s.handleEvents) // Spec 2
	mux.Handle("POST /receiver/visualizer",
		requireSameOrigin(http.HandlerFunc(s.handleVisualizerPost)))
}
```

The exact prior lines may differ slightly depending on what Spec 2 wrote; preserve them and add only the new `Mount` line.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestMount`
Expected: PASS — the two new tests + all existing Spec 2 mount tests.

Run the full chassis suite + race:

Run: `go test -race ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/visualizer_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): mount POST /receiver/visualizer under requireSameOrigin

Phase 1 / Spec 4 task 10. The Go 1.22+ method-aware mux registers
POST distinctly from existing GET routes; cross-site POSTs are
rejected by the middleware before the handler runs (verified via
mux-level test). GETs against the path return 405. No other Mount
changes; Spec 2's routes are preserved.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: vfd-live.js subscribe helper (Spec 2 file)

**Files:**
- Modify: `internal/chassis/static/vfd-live.js`
- Modify: `internal/chassis/chassis_test.go`

This task amends a Spec 2-owned file. The amendment is additive: Spec 2's existing state/vfd listeners and `reconnect()` API stay intact, while later scripts gain a reconnect-safe subscription API.

- [ ] **Step 1: Write the failing static-asset test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestVfdLive_ExposesSubscribeHelper_StaticAssetCheck(t *testing.T) {
	t.Parallel()
	// Static substring check on the bundled vfd-live.js. Asserts the
	// Spec 4 amendments are present:
	//   1. window.Chassis.events.subscribe(name, fn) is exposed so
	//      sibling scripts can attach named-event listeners.
	//   2. registered listeners are reattached after every new
	//      EventSource(...) created by reconnect().
	// This is a Layer-1 placeholder until the Spec 5 Vitest/jsdom
	// harness lands; real reconnect-order verification then.
	bytes, err := chassisStaticFS.ReadFile("static/vfd-live.js")
	if err != nil {
		t.Fatalf("read vfd-live.js: %v", err)
	}
	js := string(bytes)
	for _, want := range []string{
		"window.Chassis.events",
		"subscribe(name, fn)",
		"attachRegisteredListeners",
		"listeners",
		"new EventSource",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("vfd-live.js missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestVfdLive_ExposesSubscribeHelper`
Expected: FAIL — the current Spec 2 file already exposes `window.Chassis.events.reconnect()`, but it does not yet expose `subscribe(name, fn)` or reattach registered listeners on reconnect.

- [ ] **Step 3: Amend vfd-live.js**

Edit `internal/chassis/static/vfd-live.js`. Add a listener registry near the existing `let source = null;`, then have `connect()` reattach all registered listeners after the built-in `state`/`vfd` listeners are attached:

```javascript
  let source = null;
  const listeners = new Map();

  function attachRegisteredListeners() {
    if (!source) return;
    for (const [name, fns] of listeners) {
      for (const fn of fns) source.addEventListener(name, fn);
    }
  }

function connect() {
  source = new EventSource('/receiver/events');
  source.addEventListener('state', handleStateEvent);
  source.addEventListener('vfd', handleVfdEvent);
  attachRegisteredListeners();
  source.addEventListener('error', () => {
    console.info('vfd-live: stream interrupted; browser will retry using the SSE retry directive');
  });
}
```

Preserve Spec 2's existing `window.Chassis.events.reconnect()` surface, but add `subscribe(name, fn)` alongside it:

If Spec 2 wrote:

```javascript
window.Chassis.events = {
  reconnect() {
    if (source) source.close();
    connect();
  },
};
```

Then change it to:

```javascript
window.Chassis.events = {
  subscribe(name, fn) {
    if (!listeners.has(name)) listeners.set(name, new Set());
    listeners.get(name).add(fn);
    if (source) source.addEventListener(name, fn);
  },
  reconnect() {
    if (source) source.close();
    connect();
  },
};
```

No `window.Chassis.events.source` escape hatch and no `chassis:eventsource` CustomEvent are introduced. Sibling scripts subscribe declaratively; `connect()` owns the source and reattaches listeners on every reconnect.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestVfdLive_ExposesSubscribeHelper`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/vfd-live.js internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): vfd-live.js exposes SSE subscribe helper

Phase 1 / Spec 4 task 11. Adds window.Chassis.events.subscribe(name, fn)
beside the existing reconnect() API so visualizer-bank.js and future
meter scripts can attach named SSE listeners without opening their own
EventSource. connect() reattaches registered listeners after every new
EventSource, so reconnect() keeps sibling listeners alive.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: visualizer-bank.js + shell.html script tag + CSS

**Files:**
- Create: `internal/chassis/static/visualizer-bank.js`
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/chassis_test.go`:

```go
func TestVisualizerBankJS_StaticAssetExists(t *testing.T) {
	t.Parallel()
	bytes, err := chassisStaticFS.ReadFile("static/visualizer-bank.js")
	if err != nil {
		t.Fatalf("read visualizer-bank.js: %v", err)
	}
		js := string(bytes)
		for _, want := range []string{
			"window.Chassis",          // attaches to chassis namespace
			".events.subscribe",       // uses the shared SSE subscription API
			"/receiver/visualizer",    // POSTs to the save endpoint
			"data-viz",                // reads the mode attribute from buttons
			"viz-btn--preview",        // skips the preview button
	} {
		if !strings.Contains(js, want) {
			t.Errorf("visualizer-bank.js missing %q", want)
		}
	}
}

func TestHandleIndex_LoadsVisualizerBankScript(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	s, _ := New(cfg)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "/receiver/static/visualizer-bank.js") {
		t.Errorf("rendered HTML missing visualizer-bank.js script tag")
	}
		// The script must come AFTER vfd-live.js per shell.html's
		// document-order defer guarantee (so window.Chassis.events.subscribe
		// exists before visualizer-bank.js's DOMContentLoaded runs).
	vfdIdx := strings.Index(body, "/receiver/static/vfd-live.js")
	vizIdx := strings.Index(body, "/receiver/static/visualizer-bank.js")
	if vfdIdx < 0 || vizIdx < 0 || vfdIdx >= vizIdx {
		t.Errorf("script order wrong: vfd-live.js at %d, visualizer-bank.js at %d (must come after)", vfdIdx, vizIdx)
	}
}

func TestChassisCSS_VizBtnPressedScoped(t *testing.T) {
	t.Parallel()
	// The new pressed-state rules must be body.receiver-scoped to
	// satisfy TestChassisCSS_AllSelectorsScoped (Phase 0). This
	// guard test re-asserts the specific selector text in case a
	// future refactor accidentally drops the scope prefix.
	bytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	css := string(bytes)
	for _, want := range []string{
		"body.receiver .viz-btn",
		"body.receiver .viz-btn.pressed",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("chassis.css missing scoped selector %q", want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestVisualizerBankJS_StaticAssetExists|TestHandleIndex_LoadsVisualizerBankScript|TestChassisCSS_VizBtnPressedScoped'`
Expected: FAIL — file doesn't exist; script tag missing; CSS selectors missing.

- [ ] **Step 3a: Create visualizer-bank.js**

Create `internal/chassis/static/visualizer-bank.js`:

```javascript
// Receiver chassis visualizer-bank client. Phase 1 / Spec 4.
// Wires the four CRT visualizer-bank buttons to the bridge config:
// click → POST /receiver/visualizer; SSE `visualizer` event → flip
// active/lit classes. Hybrid feedback: a momentary `pressed` CSS
// class on click for haptic response; the active/lit classes only
// move when the SSE event arrives.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('visualizer-bank: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  const PRESSED_MS = 180;

  function bankRoot() {
    return document.querySelector('.viz-bank');
  }

  function setActiveMode(mode) {
    const root = bankRoot();
    if (!root) return;
    for (const btn of root.querySelectorAll('.viz-btn')) {
      const isActive = btn.dataset.viz === mode &&
                       !btn.classList.contains('viz-btn--preview');
      btn.classList.toggle('active', isActive);
      btn.classList.toggle('lit', isActive);
      btn.setAttribute('aria-checked', isActive ? 'true' : 'false');
    }
  }

  function flashPressed(btn) {
    btn.classList.add('pressed');
    setTimeout(() => btn.classList.remove('pressed'), PRESSED_MS);
  }

  async function postMode(mode) {
    const body = new URLSearchParams({ mode });
    try {
      const res = await fetch('/receiver/visualizer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body,
        credentials: 'same-origin',
      });
      if (res.status === 204) return; // SSE event will arrive; nothing to do
      const text = await res.text().catch(() => '');
      console.warn('visualizer-bank: save failed', res.status, text);
    } catch (err) {
      console.warn('visualizer-bank: save request errored', err);
    }
  }

  function onClick(ev) {
    const btn = ev.target.closest('.viz-btn');
    if (!btn) return;
    if (btn.classList.contains('viz-btn--preview') || btn.disabled) return;
    const mode = btn.dataset.viz;
    if (!mode) return;
    flashPressed(btn);
    postMode(mode);
  }

  function handleVisualizerEvent(ev) {
    try {
      const { mode } = JSON.parse(ev.data);
      if (typeof mode === 'string' && mode.length > 0) {
        setActiveMode(mode);
      }
    } catch (err) {
      console.warn('visualizer-bank: bad payload', ev.data, err);
    }
  }

  function attach() {
    const root = bankRoot();
    if (root) root.addEventListener('click', onClick);
    // Subscribe to the shared EventSource owned by vfd-live.js. The
    // listener registry there reattaches this handler after reconnect().
    if (window.Chassis.events && typeof window.Chassis.events.subscribe === 'function') {
      window.Chassis.events.subscribe('visualizer', handleVisualizerEvent);
    } else {
      console.warn('visualizer-bank: window.Chassis.events.subscribe missing; vfd-live.js failed to load?');
    }
  }

  document.addEventListener('DOMContentLoaded', attach);
})();
```

- [ ] **Step 3b: Add the script tag to shell.html**

Edit `internal/chassis/templates/shell.html`. Locate the `<script defer src="/receiver/static/vfd-live.js?v={{.Version}}"></script>` tag (Spec 2). Append immediately after:

```html
<script defer src="/receiver/static/visualizer-bank.js?v={{.Version}}"></script>
```

The order matters: visualizer-bank.js must load *after* vfd-live.js so `window.Chassis.events.subscribe` exists before visualizer-bank.js's DOMContentLoaded handler runs.

- [ ] **Step 3c: Add the pressed-state CSS rules**

Edit `internal/chassis/static/chassis.css`. Append to the end of section 9 (Visualizer bank — search for `/* ===== 9. Visualizer bank ===== */` or similar banner; if Phase 0 didn't use numbered banners, append to the end of the existing visualizer-related rules):

```css
body.receiver .viz-btn {
  transition: transform 120ms ease, box-shadow 120ms ease;
}
body.receiver .viz-btn.pressed {
  transform: translateY(1px);
  box-shadow: inset 0 1px 3px rgba(0, 0, 0, 0.4);
  transition: none;
}
```

Both rules are `body.receiver`-scoped per the Phase 0 invariant.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestVisualizerBankJS_StaticAssetExists|TestHandleIndex_LoadsVisualizerBankScript|TestChassisCSS_VizBtnPressedScoped'`
Expected: PASS — all three.

Run `TestChassisCSS_AllSelectorsScoped` (Phase 0) to confirm no scope regression:

Run: `go test ./internal/chassis/ -run TestChassisCSS_AllSelectorsScoped`
Expected: PASS.

Run the full chassis suite:

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/visualizer-bank.js internal/chassis/templates/shell.html internal/chassis/static/chassis.css internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): visualizer-bank.js client + shell.html + pressed CSS

Phase 1 / Spec 4 task 12. New ~70-line vanilla ES2022 script: clicks
on the four CRT buttons POST /receiver/visualizer; the SSE visualizer
event flips active/lit classes. Hybrid feedback adds a 180ms pressed
class for haptic response. Shell template loads it AFTER vfd-live.js
so the shared EventSource cache is populated. CSS additions are
body.receiver-scoped; TestChassisCSS_AllSelectorsScoped re-runs green.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: main.go wiring (visualizerSaverAdapter + Config)

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

`main.go` has no unit tests as such; verification is via `go build` + the integration test in task 14 that boots a full server.

- [ ] **Step 1: Add the adapter struct**

Edit `cmd/mister-groovy-relay/main.go`. Add the adapter near the top of the file or alongside other adapter-style helpers (look for existing wiring helpers; group with them). The exact location doesn't matter, but a top-level type declaration is required because Go requires unexported types at file scope:

```go
// visualizerSaverAdapter bridges chassis.VisualizerSaver to
// uiserver.BridgeSaver's narrow-save path. The chassis HTTP handler
// validates the mode against config.SupportedVisualizerModes before
// invoking this adapter, so the implementation is a 3-line
// passthrough — no error-string translation, no typed-error mapping.
type visualizerSaverAdapter struct {
	bs *uiserver.BridgeSaver
}

func (a *visualizerSaverAdapter) SaveVisualizerMode(mode string) error {
	_, err := a.bs.SaveVisualizerMode(mode)
	return err
}
```

- [ ] **Step 2: Wire the chassis Config fields**

Locate the `chassis.New(chassis.Config{...})` call (Spec 2 added it). The block currently looks something like:

```go
chassisSrv, err := chassis.New(chassis.Config{
	Bridge:    sec.Bridge,
	Manager:   coreMgr,
	Registry:  reg,
	Version:   version,
	StartedAt: startedAt,
	HostIP:    hostIP,
	Session:   coreMgr, // Spec 2
})
```

Add `VisualizerViewer` and `VisualizerSaver` fields:

```go
chassisSrv, err := chassis.New(chassis.Config{
	Bridge:           sec.Bridge,
	Manager:          coreMgr,
	Registry:         reg,
	Version:          version,
	StartedAt:        startedAt,
	HostIP:           hostIP,
	Session:          coreMgr, // Spec 2
	VisualizerViewer: coreMgr, // Spec 4: *core.Manager satisfies VisualizerViewer via VisualizerMode()
	VisualizerSaver:  &visualizerSaverAdapter{bs: bridgeSaver}, // Spec 4
})
```

If the local variable holding the `*uiserver.BridgeSaver` is named differently (e.g. `bSaver`, `saver`), use that name. Look at the existing `uiserver.NewBridgeSaver(...)` call to find the local variable name.

- [ ] **Step 3: Verify the build**

Run: `go build ./...`
Expected: build succeeds with no errors.

Run a smoke test that exercises both packages together:

Run: `go test ./...`
Expected: PASS (excluding integration tests, which run in task 14).

- [ ] **Step 4: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "$(cat <<'EOF'
feat(cmd): wire chassis VisualizerViewer + VisualizerSaver

Phase 1 / Spec 4 task 13. *core.Manager is wired as the chassis
VisualizerViewer (satisfies the interface via VisualizerMode() added
in task 1). visualizerSaverAdapter wraps uiserver.BridgeSaver as the
VisualizerSaver — a 3-line passthrough because the chassis handler
validates modes before invoking the adapter. No error-string
translation; chassis owns input validation, BridgeSaver still runs
full config validation as defense in depth.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Integration tests

**Files:**
- Modify: `tests/integration/chassis_test.go`

The existing `tests/integration/chassis_test.go` (Phase 0) uses an inline construction pattern: build `chassis.Config` manually, call `chassis.New(...)`, mount on a fresh `http.ServeMux`, wrap in `httptest.NewServer(mux)`. There are no `startBridgeForTest` / `newSSEScanner` helpers. Spec 4 follows the same pattern, with two additions: a real `*uiserver.BridgeSaver` writing to a temp dir, and a small `readVisualizerEvent` SSE-record reader.

- [ ] **Step 1: Write the helper function**

Append to `tests/integration/chassis_test.go`:

```go
// readVisualizerEvent reads SSE records from r until it sees a
// `visualizer` event (or the deadline expires). Returns the event's
// `mode` field. Fails the test if no visualizer event arrives within
// the deadline.
//
// SSE record format: "event: <name>\ndata: <json>\n\n" (Spec 2's
// emitter; spec doc § "SSE Wire Protocol Extension"). This helper
// scans the body line-by-line and reassembles records.
func readVisualizerEvent(t *testing.T, r io.Reader, deadline time.Duration) string {
	t.Helper()
	type event struct{ name, data string }
	events := make(chan event, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(r)
		var curName, curData string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				curName = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				curData = strings.TrimPrefix(line, "data: ")
			case line == "":
				if curName != "" {
					events <- event{name: curName, data: curData}
					curName, curData = "", ""
				}
			}
		}
	}()

	timeout := time.After(deadline)
	for {
		select {
		case <-timeout:
			t.Fatalf("no visualizer event within %v", deadline)
			return ""
		case ev := <-events:
			if ev.name != "visualizer" {
				continue
			}
			var env struct {
				Mode string `json:"mode"`
			}
			if err := json.Unmarshal([]byte(ev.data), &env); err != nil {
				t.Fatalf("unmarshal visualizer payload %q: %v", ev.data, err)
			}
			return env.Mode
		case <-done:
			t.Fatalf("SSE stream closed before visualizer event arrived")
			return ""
		}
	}
}

// newChassisIntegrationServer wires a real uiserver.BridgeSaver +
// core.Manager + chassis.Server onto a fresh httptest.NewServer. The
// returned server's Config writes to a temp dir; t.Cleanup closes the
// sender, httptest server, and chassis snapshot refresher. Used by
// Spec 4 integration tests that need the save path to actually exercise
// atomic-write + UpdateBridge.
//
// Returns the httptest server URL plus a pointer to the live
// *core.Manager so tests can call VisualizerMode() to verify
// in-memory state, and the temp dir for disk-side assertions.
func newChassisIntegrationServer(t *testing.T) (url string, mgr *core.Manager, tempDir string) {
	t.Helper()
	tempDir = t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.toml")
	bridge := config.BridgeConfig{
		DataDir: tempDir,
		MiSTer: config.MisterConfig{
			Host: "127.0.0.1", Port: 32100, SourcePort: 32101,
		},
		UI: config.UIConfig{HTTPPort: 32500},
		Video: config.VideoConfig{
			Modeline: "NTSC_480i", InterlaceFieldOrder: "tff",
			AspectMode: "letterbox", RGBMode: "rgb888",
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2, OutputVolume: 100},
		Visualizer: config.VisualizerConfig{Mode: config.VisualizerModeRetroAnalyzer},
	}
	sec := &config.Sectioned{Bridge: bridge}
	// Seed config.toml so atomic-write has a source to read.
	seedConfigToml(t, cfgPath, bridge)

	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatalf("groovynet.NewSender: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })
	mgr = core.NewManager(bridge, sender)

	saver := uiserver.NewBridgeSaver(cfgPath, sec, mgr, adapters.NewRegistry())

	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:           bridge,
		Manager:          mgr,
		Registry:         adapters.NewRegistry(),
		Version:          "integration-test",
		StartedAt:        time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:           "127.0.0.1",
		Session:          mgr, // Spec 2
		VisualizerViewer: mgr,
		VisualizerSaver:  &integrationVisualizerSaver{bs: saver},
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = chassisSrv.Close() })
	mux := http.NewServeMux()
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL, mgr, tempDir
}

// integrationVisualizerSaver mirrors the adapter from main.go
// (cmd/mister-groovy-relay/main.go's visualizerSaverAdapter). Inlined
// here so the integration test does not need to import cmd/*.
type integrationVisualizerSaver struct {
	bs *uiserver.BridgeSaver
}

func (a *integrationVisualizerSaver) SaveVisualizerMode(mode string) error {
	_, err := a.bs.SaveVisualizerMode(mode)
	return err
}

// seedConfigToml writes a minimal config.toml at path so subsequent
// atomic-rewrites by BridgeSaver have a base to read. Uses BurntSushi
// TOML encoder so format matches what saveLocked rewrites.
func seedConfigToml(t *testing.T, path string, b config.BridgeConfig) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(struct {
		Bridge config.BridgeConfig `toml:"bridge"`
	}{b}); err != nil {
		t.Fatalf("encode seed toml: %v", err)
	}
}
```

Imports to merge into the file's existing import block:

```go
import (
	// ... existing Phase 0 imports preserved ...
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)
```

Verify the build compiles before writing the test bodies:

Run: `go build -tags=integration ./tests/integration/...`
Expected: PASS — no missing imports, no signature mismatches.

- [ ] **Step 2: Write the failing integration tests**

Append to `tests/integration/chassis_test.go` (after the helpers from Step 1):

```go
func TestReceiverVisualizer_EndToEnd_PostAndSSEEvent(t *testing.T) {
	t.Parallel()
	// 5-second SSE convergence deadline per spec § "Latency budget":
	// matches existing tests/integration/ 5-10s convention; ~10× the
	// 500ms UX SLO so CI flakes don't masquerade as regressions.
	url, mgr, tempDir := newChassisIntegrationServer(t)

	// Open SSE connection.
	sseResp, err := http.Get(url + "/receiver/events")
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer sseResp.Body.Close()

	// Read the initial-burst visualizer event (Spec 4 § "Initial-snapshot sequence").
	initialMode := readVisualizerEvent(t, sseResp.Body, 5*time.Second)
	if initialMode != config.VisualizerModeRetroAnalyzer {
		t.Errorf("initial visualizer event mode = %q, want %q",
			initialMode, config.VisualizerModeRetroAnalyzer)
	}

	// POST a mode change.
	postBody := strings.NewReader("mode=stereo_scope")
	postReq, _ := http.NewRequest(http.MethodPost, url+"/receiver/visualizer", postBody)
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Sec-Fetch-Site", "same-origin")
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST status = %d, want 204", postResp.StatusCode)
	}

	// Wait for the next visualizer event with the new mode.
	gotMode := readVisualizerEvent(t, sseResp.Body, 5*time.Second)
	if gotMode != config.VisualizerModeStereoScope {
		t.Errorf("post-save visualizer event mode = %q, want %q",
			gotMode, config.VisualizerModeStereoScope)
	}

	// Disk-side verification.
	tomlBytes, err := os.ReadFile(filepath.Join(tempDir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(tomlBytes), `mode = "stereo_scope"`) {
		t.Errorf("config.toml does not contain new mode; content:\n%s", string(tomlBytes))
	}

	// In-memory verification.
	if got := mgr.VisualizerMode(); got != config.VisualizerModeStereoScope {
		t.Errorf("core.Manager.VisualizerMode() = %q, want %q",
			got, config.VisualizerModeStereoScope)
	}
}

func TestReceiverVisualizer_BlocksCrossSitePost(t *testing.T) {
	t.Parallel()
	url, mgr, _ := newChassisIntegrationServer(t)

	postBody := strings.NewReader("mode=stereo_scope")
	req, _ := http.NewRequest(http.MethodPost, url+"/receiver/visualizer", postBody)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	// Confirm the save did not happen.
	if got := mgr.VisualizerMode(); got != config.VisualizerModeRetroAnalyzer {
		t.Errorf("VisualizerMode() = %q, want default %q (no save)",
			got, config.VisualizerModeRetroAnalyzer)
	}
}

func TestReceiverVisualizer_PreviewModeRejected(t *testing.T) {
	t.Parallel()
	url, _, _ := newChassisIntegrationServer(t)

	postBody := strings.NewReader("mode=radial_spectrum")
	req, _ := http.NewRequest(http.MethodPost, url+"/receiver/visualizer", postBody)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (radial_spectrum deferred from v1)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"mode":"radial_spectrum"`) {
		t.Errorf("body should echo mode; got %s", string(body))
	}
}

func TestReceiverVisualizer_GetReturns405(t *testing.T) {
	t.Parallel()
	url, _, _ := newChassisIntegrationServer(t)

	resp, err := http.Get(url + "/receiver/visualizer")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -tags=integration ./tests/integration/ -run TestReceiverVisualizer`
Expected: FAIL — either compile errors if Tasks 1-13 aren't all complete, or behavior failures if some piece is wired wrong.

- [ ] **Step 4: Run the tests to verify they pass**

After confirming Tasks 1-13 are all merged, run:

Run: `go test -tags=integration ./tests/integration/ -run TestReceiverVisualizer`
Expected: PASS — all four tests.

Run the full integration suite to confirm Spec 2's existing tests still pass:

Run: `go test -tags=integration ./tests/integration/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/chassis_test.go
git commit -m "$(cat <<'EOF'
test(integration): chassis visualizer save end-to-end

Phase 1 / Spec 4 task 14. Four new integration tests covering: POST
→ SSE event → disk + in-memory verification; cross-site POST blocked;
radial_spectrum preview rejected with 400; /ui/* coexistence. End-to-
end SSE convergence asserted with a 5-second deadline (matches the
existing 5-10s convention; ~10× the 500ms UX SLO so CI flakes don't
masquerade as regressions).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Docs (chassis doc.go + README troubleshooting)

**Files:**
- Modify: `internal/chassis/doc.go`
- Modify: `README.md`

- [ ] **Step 1: Update chassis package docs**

Edit `internal/chassis/doc.go`. Append a paragraph after the existing Spec 0 / Spec 2 doc text:

```go
// Spec 4 adds POST /receiver/visualizer for chassis-driven visualizer
// mode changes. The endpoint accepts form-encoded mode=<value> and
// returns 204 on success; the SSE stream's new `visualizer` event
// fans the change out to other open chassis tabs within ~250ms.
// Cross-origin POSTs are blocked by the requireSameOrigin middleware
// (Sec-Fetch-Site: same-origin or same-site only). The narrow
// VisualizerViewer + VisualizerSaver interfaces (defined in
// visualizer.go) keep the chassis package free of imports from
// internal/uiserver; main.go wires the production implementations.
```

- [ ] **Step 2: Update README troubleshooting**

Edit `README.md`. Locate the troubleshooting / operator-notes section (search for "troubleshooting" or "ops" near the bottom). Append:

```markdown
### Scripted POSTs to chassis endpoints

The chassis settings/control endpoints (`POST /receiver/visualizer`,
and future endpoints under `/receiver/*` introduced in Spec 3+) require
a `Sec-Fetch-Site` header indicating a same-origin or same-site
request. Browsers send this automatically; non-browser callers (curl,
ops scripts, monitoring probes) must set it explicitly:

```bash
curl -X POST http://localhost:32500/receiver/visualizer \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "Sec-Fetch-Site: same-origin" \
  -d "mode=stereo_scope"
```

Without the header, the request returns `403 Forbidden`. This is the
chassis's first-party-only CSRF defence and is intentionally stricter
than the existing `/ui/*` middleware.
```

Adjust the host:port to match the project's documented default.

- [ ] **Step 3: Verify the build (docs changes don't run tests, but a build sanity-checks the doc.go comment)**

Run: `go build ./internal/chassis/...`
Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/doc.go README.md
git commit -m "$(cat <<'EOF'
docs: chassis visualizer endpoint + Sec-Fetch-Site troubleshooting

Phase 1 / Spec 4 task 15. doc.go gains a paragraph describing the new
POST endpoint, the visualizer SSE event, and the narrow interface
pattern. README's troubleshooting section gains a curl example with
the required Sec-Fetch-Site header so ops scripts don't 403 silently.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: Final verification pass

**Files:** None modified.

- [ ] **Step 1: Run the full test suite**

Run: `make lint && make test`
Expected: PASS — go vet + all unit tests.

- [ ] **Step 2: Run with race detector**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 3: Run integration tests**

Run: `make test-integration`
Expected: PASS — Spec 2's tests + Spec 4's new four.

- [ ] **Step 4: Verify the cross-package boundary invariant**

Run: `go test ./internal/chassis/ -run TestProductionImports_NoCrossPackageCoupling`
Expected: PASS — chassis still imports only core/config/adapters.

- [ ] **Step 5: Verify CSS scope invariant**

Run: `go test ./internal/chassis/ -run TestChassisCSS_AllSelectorsScoped`
Expected: PASS.

- [ ] **Step 6: Manual smoke (optional, for PR description)**

Run the bridge locally with a sample config:

```bash
make build
./mister-groovy-relay --config config.toml
```

In a browser, open `http://localhost:32500/receiver` in two tabs. Click each non-preview visualizer-bank button in tab A. Verify in tab B that the active button moves within ~250ms. Open `http://localhost:32500/ui/` and verify the bridge-panel visualizer dropdown reflects the chassis-driven value.

Test the curl rejection:

```bash
# Without Sec-Fetch-Site — expect 403
curl -X POST http://localhost:32500/receiver/visualizer -d "mode=stereo_scope"
# => 403 {"error":"cross-site request blocked"}

# With Sec-Fetch-Site — expect 204
curl -X POST http://localhost:32500/receiver/visualizer \
  -H "Sec-Fetch-Site: same-origin" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "mode=stereo_scope"
# => 204 (empty body)

# radial_spectrum — expect 400
curl -X POST http://localhost:32500/receiver/visualizer \
  -H "Sec-Fetch-Site: same-origin" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "mode=radial_spectrum"
# => 400 {"error":"unsupported visualizer mode","mode":"radial_spectrum"}
```

DevTools Network → EventStream tab on `/receiver`: confirm `visualizer` events appear in the stream with the correct JSON payload.

Attach screenshots to the PR description per the spec's manual-verification checklist.

- [ ] **Step 7: Final commit (only if smoke surfaced any test gaps)**

No new commits expected. If the manual smoke surfaced a real gap (e.g. a missing test case), add it to the test file and commit per the standard pattern.

---

## Plan complete

Spec 4 is now end-to-end:
- `POST /receiver/visualizer` accepts form-encoded mode + validates against `config.SupportedVisualizerModes()`.
- The SSE stream's new `visualizer` event keeps multiple chassis tabs synchronized.
- `Sec-Fetch-Site` middleware is the first chassis cross-origin defence; Spec 3 (transport) reuses it.
- `internal/chassis/` still imports only core/config/adapters; `main.go` is the composition root.
- `/ui/*` is untouched; the bridge-panel visualizer dropdown reads/writes the same `config.toml` via the shared `BridgeSaver`.
