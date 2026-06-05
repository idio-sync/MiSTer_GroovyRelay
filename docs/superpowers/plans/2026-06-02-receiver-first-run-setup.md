# Receiver First-Run Setup — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a guided first-run "setup mode" to the receiver chassis UI (`/receiver/*`) that reuses the existing settings drawer, gates casting until a MiSTer host + one source are configured, and dismisses the shared first-run sentinel on Finish.

**Architecture:** A thin gate inside `internal/chassis`. A new chassis-owned `FirstRunController` interface is satisfied structurally by the `*uiserver.BridgeSaver` already passed in (no `main.go` change). Setup mode is keyed on the sentinel (`IsFirstRun()`), surfaced as `ReceiverPageData.SetupMode`, enforced server-side by a `requireSetupComplete` wrapper returning `409 FINISH SETUP` on cast-initiation routes, and presented as a welcome banner + server-rendered open drawer + a `setup.js` Finish flow.

**Tech Stack:** Go 1.26, `net/http` `ServeMux`, `html/template`, vanilla JS, embedded static assets via `go:embed`.

**Spec:** `docs/superpowers/specs/2026-06-02-receiver-first-run-setup-design.md`

---

## File Structure

**Create:**
- `internal/chassis/firstrun.go` — `FirstRunController` interface, `SetupStatus` type, `firstRunActive()`, `setupStatus()`, `requireSetupComplete` middleware, `writeSetupGate`.
- `internal/chassis/firstrun_test.go` — unit tests for the above helpers + the gate.
- `internal/chassis/setup.go` — `handleSetupStatus`, `handleSetupFinish`.
- `internal/chassis/setup_test.go` — handler tests for status/finish + handleIndex setup-mode render.
- `internal/chassis/templates/setup-banner.html` — welcome banner partial.
- `internal/chassis/static/setup.js` — Finish button + status refresh.

**Modify:**
- `internal/chassis/server.go` — add `firstRun FirstRunController` field; resolve it in `New`; gate cast routes + register setup routes in `Mount`.
- `internal/chassis/data.go` — add `SetupMode bool` + `SetupStatus SetupStatus` to `ReceiverPageData`.
- `internal/chassis/handler.go` — populate `SetupMode`/`SetupStatus` in `handleIndex`.
- `internal/chassis/templates/shell.html` — body class, conditional `setup.js`, banner include.
- `internal/chassis/static/chassis.css` — `body.receiver.setup` banner + disabled-cast styles.
- `internal/chassis/static/settings-drawer.js` — dispatch `chassis:settings-saved` after bridge/adapter saves.
- `internal/chassis/static/{input-cast,preset-bank,catalog-browser,chassis,settings-drawer}.js` — client cast guard.

**No `main.go` change:** `chassis.Config.BridgeSaver` already carries `*uiserver.BridgeSaver`, which implements `IsFirstRun()`/`DismissFirstRun()`; `New` resolves it by type assertion.

**All commands run from the worktree root:** `C:/Users/Jake/Git/MiSTer_GroovyRelay/.worktrees/receiver-first-run-setup`

---

## Task 1: First-run controller, status helpers, and the cast gate

**Files:**
- Create: `internal/chassis/firstrun.go`
- Create: `internal/chassis/firstrun_test.go`
- Modify: `internal/chassis/server.go` (add `firstRun` field + resolve in `New`)

- [ ] **Step 1: Write the failing test**

Create `internal/chassis/firstrun_test.go`. It reuses two existing same-package
fixtures: `fakeBridgeSettingsSaver` (settings_test.go) and `fakeNamedAdapter`
(data_test.go, whose `IsEnabled()` returns true).

```go
package chassis

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// fakeFirstRun embeds the existing BridgeSettingsSaver conformance fixture
// and adds the FirstRunController sentinel methods. *fakeFirstRun therefore
// satisfies BOTH BridgeSettingsSaver (promoted value methods) and
// FirstRunController (pointer methods).
type fakeFirstRun struct {
	fakeBridgeSettingsSaver
	firstRun   bool
	dismissErr error
	dismissed  int
}

func (f *fakeFirstRun) IsFirstRun() bool       { return f.firstRun }
func (f *fakeFirstRun) DismissFirstRun() error { f.dismissed++; return f.dismissErr }

// hostSaver builds a *fakeFirstRun with the given MiSTer host and first-run flag.
func hostSaver(host string, firstRun bool) *fakeFirstRun {
	return &fakeFirstRun{
		fakeBridgeSettingsSaver: fakeBridgeSettingsSaver{
			cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: host}},
		},
		firstRun: firstRun,
	}
}

// enabledRegistry returns a registry containing one enabled source adapter,
// so setupStatus reports SourceEnabled=true.
func enabledRegistry() *adapters.Registry {
	return adapters.NewRegistryWith(fakeNamedAdapter{name: "src"})
}

// newSetupServer builds a *Server via New (so firstRun resolves, templates
// parse, and Mount is safe) with the given saver and registry.
func newSetupServer(t *testing.T, saver BridgeSettingsSaver, reg *adapters.Registry) *Server {
	t.Helper()
	if reg == nil {
		reg = adapters.NewRegistry()
	}
	s, err := New(Config{Version: "test", StartedAt: time.Now(), BridgeSaver: saver, Registry: reg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestFirstRunActive(t *testing.T) {
	cases := []struct {
		name     string
		saver    BridgeSettingsSaver
		want     bool
	}{
		{"nil controller", nil, false},
		{"wired, first-run", hostSaver("", true), true},
		{"wired, dismissed", hostSaver("", false), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: Config{BridgeSaver: tc.saver}}
			s.firstRun = resolveFirstRun(tc.saver)
			if got := s.firstRunActive(); got != tc.want {
				t.Fatalf("firstRunActive()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestSetupStatus_NilSafe(t *testing.T) {
	s := &Server{cfg: Config{}} // nil BridgeSaver, nil Registry
	st := s.setupStatus()
	if st.HostSet || st.SourceEnabled {
		t.Fatalf("nil deps must yield false/false, got %+v", st)
	}
}

func TestRequireSetupComplete_Gate(t *testing.T) {
	saver := hostSaver("", true)
	s := &Server{cfg: Config{BridgeSaver: saver}}
	s.firstRun = resolveFirstRun(saver)

	reached := false
	h := s.requireSetupComplete(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/receiver/cast", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("gated POST: got %d want 409", rec.Code)
	}
	if reached {
		t.Fatal("inner handler must not run while setup active")
	}

	// Dismiss → passes through.
	saver.firstRun = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/receiver/cast", nil))
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("after dismiss: got %d reached=%v", rec.Code, reached)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestFirstRunActive|TestSetupStatus_NilSafe|TestRequireSetupComplete_Gate'`
Expected: FAIL — `undefined: resolveFirstRun`, `s.firstRunActive`, `s.setupStatus`, `s.requireSetupComplete`, `Server.firstRun`.

- [ ] **Step 3: Create `internal/chassis/firstrun.go`**

```go
// Package chassis firstrun.go implements receiver first-run "setup mode":
// the optional FirstRunController sentinel, the configured-enough status
// checks, and the cast-initiation gate. See
// docs/superpowers/specs/2026-06-02-receiver-first-run-setup-design.md.
package chassis

import "net/http"

// FirstRunController is the optional first-run sentinel backing receiver
// setup mode. Production passes *uiserver.BridgeSaver, which satisfies it
// structurally. When nil/unwired (unit-test fixtures), setup mode is never
// active and the chassis behaves exactly as before this feature.
type FirstRunController interface {
	IsFirstRun() bool
	DismissFirstRun() error
}

// resolveFirstRun returns the BridgeSaver as a FirstRunController when its
// concrete type implements the sentinel methods, else nil. A nil
// BridgeSettingsSaver interface yields nil (assertion fails), keeping
// setup mode off by default for fixtures that do not wire a saver.
func resolveFirstRun(bs BridgeSettingsSaver) FirstRunController {
	if frc, ok := bs.(FirstRunController); ok {
		return frc
	}
	return nil
}

// firstRunActive reports whether the receiver should render/enforce setup
// mode: a first-run controller is wired AND the sentinel is still set.
func (s *Server) firstRunActive() bool {
	return s.firstRun != nil && s.firstRun.IsFirstRun()
}

// SetupStatus is the configured-enough state surfaced to the page and the
// status endpoint. Mirrors internal/ui/setup.go firstIncompleteStep.
type SetupStatus struct {
	HostSet       bool `json:"hostSet"`
	SourceEnabled bool `json:"sourceEnabled"`
}

// Complete reports whether both first-run criteria are met.
func (st SetupStatus) Complete() bool { return st.HostSet && st.SourceEnabled }

// setupStatus computes the configured-enough sub-checks. Nil-safe: a nil
// BridgeSaver yields HostSet=false; a nil Registry yields SourceEnabled=false.
func (s *Server) setupStatus() SetupStatus {
	st := SetupStatus{}
	if s.cfg.BridgeSaver != nil {
		st.HostSet = s.cfg.BridgeSaver.Current().MiSTer.Host != ""
	}
	if s.cfg.Registry != nil {
		for _, a := range s.cfg.Registry.List() {
			if a.IsEnabled() {
				st.SourceEnabled = true
				break
			}
		}
	}
	return st
}

// requireSetupComplete refuses cast-initiation actions with 409 while
// first-run setup mode is active (sentinel still set). No-op once the
// sentinel is dismissed or when no first-run controller is wired.
func (s *Server) requireSetupComplete(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.firstRunActive() {
			writeSetupGate(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeSetupGate emits the consistent 409 FINISH SETUP chip payload.
func writeSetupGate(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte(`{"chip":"FINISH SETUP","message":"Finish first-run setup before casting."}`))
}
```

- [ ] **Step 4: Add the `firstRun` field + resolution in `server.go`**

In `internal/chassis/server.go`, add a field to the `Server` struct (next to `cfg`/`session`):

```go
	firstRun FirstRunController
```

In `New`, after the `s := &Server{...}` literal and before `s.cache.Set(...)`, add:

```go
	s.firstRun = resolveFirstRun(cfg.BridgeSaver)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestFirstRunActive|TestSetupStatus_NilSafe|TestRequireSetupComplete_Gate'`
Expected: PASS.

> The fake embeds the existing `fakeBridgeSettingsSaver` (settings_test.go) so its interface conformance is inherited; no signature duplication. If `fakeNamedAdapter` or `adapters.NewRegistryWith` aren't found, grep `internal/chassis/*_test.go` for the current enabled-adapter fixture and registry constructor and substitute.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/firstrun.go internal/chassis/firstrun_test.go internal/chassis/server.go
git commit -m "feat(chassis): first-run controller, setup status, and cast gate"
```

---

## Task 2: Gate the cast-initiation routes in Mount

**Files:**
- Modify: `internal/chassis/server.go` (`Mount`)
- Modify: `internal/chassis/firstrun_test.go` (add route-level gate test)

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/firstrun_test.go`:

```go
func TestMount_GatesCastRoutes(t *testing.T) {
	gated := []struct {
		method, path string
	}{
		{"POST", "/receiver/cast"},
		{"POST", "/receiver/preset/1/cast"},
		{"POST", "/receiver/streams/cast"},
		{"POST", "/receiver/localfiles/cast"},
		{"POST", "/receiver/settings/adapter/localfiles/cast"},
		{"POST", "/receiver/history/play"},
		{"POST", "/receiver/aux/start"},
	}
	saver := hostSaver("", true)
	s := newSetupServer(t, saver, nil) // built via New → firstRun resolves, Mount safe

	mux := http.NewServeMux()
	s.Mount(mux)

	for _, g := range gated {
		req := httptest.NewRequest(g.method, g.path, nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin") // pass requireSameOrigin
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s %s: got %d want 409 (gated)", g.method, g.path, rec.Code)
		}
	}
}
```

> `newSetupServer`, `hostSaver`, and `enabledRegistry` are defined in `firstrun_test.go` (Task 1) and visible package-wide. `newSetupServer` builds via `New(...)` so `firstRun` resolves and the snapshot refresher initializes (memory `chassis_test_server_new`: `Mount` on a bare `&Server{}` nil-derefs).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestMount_GatesCastRoutes`
Expected: FAIL — routes return 200/4xx-from-handler, not 409, because the gate isn't wired.

- [ ] **Step 3: Wrap the gated handlers in `Mount`**

In `internal/chassis/server.go` `Mount`, change each of these seven registrations to wrap the inner handler with `s.requireSetupComplete(...)` **inside** the existing `requireSameOrigin(...)` (and inside `transportNoStore` where present). Exact replacements:

```go
	mux.Handle("POST /receiver/aux/start", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleAUXStartPost))))
	mux.Handle("POST /receiver/cast", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleCastPost))))
	mux.Handle("POST /receiver/history/play", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleHistoryPlayPost))))
	mux.Handle("POST /receiver/preset/{slot}/cast", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handlePresetCast))))
	mux.Handle("POST /receiver/streams/cast", requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleStreamsCast))))
	mux.Handle("POST /receiver/localfiles/cast",
		requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleReceiverLocalfilesCast))))
	mux.Handle("POST /receiver/settings/adapter/localfiles/cast",
		requireSameOrigin(s.requireSetupComplete(http.HandlerFunc(s.handleSettingsAdapterLocalfilesCast))))
```

Leave all other routes (transport/seek, volume, visualizer, audio dsp, preset star/move, settings saves, link/pairing, probe, launch-core, browse, libraries) unchanged.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestMount_GatesCastRoutes`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/firstrun_test.go
git commit -m "feat(chassis): gate cast-initiation routes behind setup completion"
```

---

## Task 3: Setup status + finish endpoints

**Files:**
- Create: `internal/chassis/setup.go`
- Create: `internal/chassis/setup_test.go`
- Modify: `internal/chassis/server.go` (`Mount` — register two routes)

- [ ] **Step 1: Write the failing test**

Create `internal/chassis/setup_test.go`:

```go
package chassis

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeStatus(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var m map[string]bool
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("bad status JSON %q: %v", body, err)
	}
	return m
}

func TestHandleSetupStatus(t *testing.T) {
	// No controller wired → complete:true.
	s := &Server{cfg: Config{}}
	rec := httptest.NewRecorder()
	s.handleSetupStatus(rec, httptest.NewRequest(http.MethodGet, "/receiver/setup/status", nil))
	if got := decodeStatus(t, rec.Body.Bytes()); !got["complete"] {
		t.Fatalf("no controller: want complete:true, got %+v", got)
	}

	// First-run, host set, no source → hostSet:true sourceEnabled:false complete:false.
	saver := hostSaver("10.0.0.5", true)
	s2 := &Server{cfg: Config{BridgeSaver: saver}} // empty Registry → no source
	s2.firstRun = resolveFirstRun(saver)
	rec = httptest.NewRecorder()
	s2.handleSetupStatus(rec, httptest.NewRequest(http.MethodGet, "/receiver/setup/status", nil))
	got := decodeStatus(t, rec.Body.Bytes())
	if !got["hostSet"] || got["sourceEnabled"] || got["complete"] {
		t.Fatalf("host-only: got %+v", got)
	}
}

func TestHandleSetupFinish(t *testing.T) {
	// No controller → 200 no-op.
	s := &Server{cfg: Config{}}
	rec := httptest.NewRecorder()
	s.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("no controller: got %d want 200", rec.Code)
	}

	// First-run but incomplete (no host) → 409.
	saver := hostSaver("", true)
	s2 := &Server{cfg: Config{BridgeSaver: saver}}
	s2.firstRun = resolveFirstRun(saver)
	rec = httptest.NewRecorder()
	s2.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusConflict || saver.dismissed != 0 {
		t.Fatalf("incomplete: got %d dismissed=%d", rec.Code, saver.dismissed)
	}

	// Dismiss failure → 500.
	bad := hostSaver("h", true)
	bad.dismissErr = errors.New("disk full")
	badSrv := &Server{cfg: Config{BridgeSaver: bad, Registry: enabledRegistry()}}
	badSrv.firstRun = resolveFirstRun(bad)
	rec = httptest.NewRecorder()
	badSrv.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("dismiss-fail: got %d want 500", rec.Code)
	}

	// Complete → 200 + DismissFirstRun called once.
	ok := hostSaver("h", true)
	okSrv := &Server{cfg: Config{BridgeSaver: ok, Registry: enabledRegistry()}}
	okSrv.firstRun = resolveFirstRun(ok)
	rec = httptest.NewRecorder()
	okSrv.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusOK || ok.dismissed != 1 {
		t.Fatalf("complete: got %d dismissed=%d", rec.Code, ok.dismissed)
	}
}
```

> `hostSaver` and `enabledRegistry` are the helpers from `firstrun_test.go` (Task 1). `errors` must be in this file's import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestHandleSetupStatus|TestHandleSetupFinish'`
Expected: FAIL — `s.handleSetupStatus` / `s.handleSetupFinish` undefined.

- [ ] **Step 3: Create `internal/chassis/setup.go`**

```go
package chassis

import (
	"encoding/json"
	"net/http"
)

// handleSetupStatus reports the configured-enough sub-checks plus an
// overall "complete" flag. complete is true when no first-run controller
// is wired or the sentinel is already dismissed (nothing to do). GET,
// non-mutating — intentionally not same-origin wrapped (that wrapper only
// enforces POST).
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	st := s.setupStatus()
	complete := st.Complete()
	if s.firstRun == nil || !s.firstRun.IsFirstRun() {
		complete = true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]bool{
		"hostSet":       st.HostSet,
		"sourceEnabled": st.SourceEnabled,
		"complete":      complete,
	})
}

// handleSetupFinish completes first-run setup. Order:
//   - no controller wired OR sentinel already dismissed → 200 (no-op),
//   - criteria incomplete → 409 naming the unmet item,
//   - DismissFirstRun error → 500,
//   - otherwise dismiss + 200.
// Idempotent under concurrency (DismissFirstRun is an idempotent os.Create
// and IsFirstRun re-Stats each call), so a duplicate finish or a race with
// /ui/setup observes the dismissed sentinel and returns 200.
func (s *Server) handleSetupFinish(w http.ResponseWriter, r *http.Request) {
	if s.firstRun == nil || !s.firstRun.IsFirstRun() {
		w.WriteHeader(http.StatusOK)
		return
	}
	st := s.setupStatus()
	if !st.Complete() {
		w.WriteHeader(http.StatusConflict)
		msg := "set a MiSTer host"
		if st.HostSet {
			msg = "enable a source"
		}
		_, _ = w.Write([]byte(msg))
		return
	}
	if err := s.firstRun.DismissFirstRun(); err != nil {
		http.Error(w, "finish failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Register routes in `Mount`**

In `internal/chassis/server.go` `Mount`, add (near the other `/receiver/setup`-adjacent or settings routes):

```go
	mux.HandleFunc("GET /receiver/setup/status", s.handleSetupStatus)
	mux.Handle("POST /receiver/setup/finish", requireSameOrigin(http.HandlerFunc(s.handleSetupFinish)))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestHandleSetupStatus|TestHandleSetupFinish'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/setup.go internal/chassis/setup_test.go internal/chassis/server.go
git commit -m "feat(chassis): /receiver/setup/status and /receiver/setup/finish"
```

---

## Task 4: Render setup mode on the page (data + handler + templates)

**Files:**
- Modify: `internal/chassis/data.go` (`ReceiverPageData`)
- Modify: `internal/chassis/handler.go` (`handleIndex`)
- Modify: `internal/chassis/templates/shell.html`
- Create: `internal/chassis/templates/setup-banner.html`
- Modify: `internal/chassis/setup_test.go` (render test)

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/setup_test.go`:

```go
import "strings" // add to the existing import block if not present

func TestHandleIndex_SetupMode(t *testing.T) {
	saver := hostSaver("", true)
	s := newSetupServer(t, saver, nil) // via New → templates parsed, firstRun resolved

	rec := httptest.NewRecorder()
	s.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/receiver", nil))
	html := rec.Body.String()

	if !strings.Contains(html, "setup settings-open") {
		t.Error("setup mode: body should carry 'setup settings-open' classes")
	}
	if !strings.Contains(html, "/receiver/static/setup.js") {
		t.Error("setup mode: setup.js script tag should be present")
	}
	if !strings.Contains(html, "setup-banner") {
		t.Error("setup mode: welcome banner should render")
	}

	// Dismissed → none of the above.
	saver.firstRun = false
	rec = httptest.NewRecorder()
	s.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/receiver", nil))
	html = rec.Body.String()
	if strings.Contains(html, "setup-banner") || strings.Contains(html, "/receiver/static/setup.js") {
		t.Error("dismissed: setup banner/script must not render")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestHandleIndex_SetupMode`
Expected: FAIL — `SetupMode` not set / templates lack the markup.

- [ ] **Step 3: Add fields to `ReceiverPageData` (`data.go`)**

In `internal/chassis/data.go`, inside the `ReceiverPageData` struct, add:

```go
	// SetupMode is true while first-run setup mode is active (sentinel set).
	// Only handleIndex populates it; see firstrun.go / setup.go.
	SetupMode   bool
	SetupStatus SetupStatus
```

- [ ] **Step 4: Populate in `handleIndex` (`handler.go`)**

In `internal/chassis/handler.go` `handleIndex`, after `data.Version = s.assetVer` and before the template execute, add:

```go
	data.SetupMode = s.firstRunActive()
	if data.SetupMode {
		data.SetupStatus = s.setupStatus()
	}
```

- [ ] **Step 5: Create the banner partial `internal/chassis/templates/setup-banner.html`**

```html
{{define "setup-banner.html"}}
{{htmlComment "chassis:setup-banner"}}
<section class="setup-banner" role="region" aria-label="First-run setup">
  <p class="setup-banner-title">Welcome — let’s set up your receiver</p>
  <ol class="setup-checklist">
    <li class="setup-step{{if .SetupStatus.HostSet}} done{{end}}">Point me at your MiSTer</li>
    <li class="setup-step{{if .SetupStatus.SourceEnabled}} done{{end}}">Turn on a source</li>
  </ol>
  <button type="button" id="setup-finish" class="setup-finish"
    {{if not .SetupStatus.Complete}}disabled aria-disabled="true"{{end}}>Finish setup</button>
</section>
{{end}}
```

> `SetupStatus.Complete` is the method defined in Task 1; `html/template` calls zero-arg methods in field position. If the template engine rejects the method call, replace `not .SetupStatus.Complete` with `not (and .SetupStatus.HostSet .SetupStatus.SourceEnabled)` (both `and`/`not` are built-ins). `htmlComment` is the existing chassis sentinel-comment helper used by other partials.

- [ ] **Step 6: Edit `shell.html`**

(a) Body tag — change:

```html
<body class="receiver {{.State}}">
```
to:
```html
<body class="receiver {{.State}}{{if .SetupMode}} setup settings-open{{end}}">
```

(b) In `<head>`, after the existing `settings-drawer.js` script line, add the conditional script:

```html
  {{if .SetupMode}}<script defer src="/receiver/static/setup.js?v={{.Version}}"></script>{{end}}
```

(c) Render the banner. Immediately after the opening `<div class="receiver">` line (the inner wrapper that follows `{{htmlComment "chassis:shell"}}`), add:

```html
    {{if .SetupMode}}{{template "setup-banner.html" .}}{{end}}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestHandleIndex_SetupMode|TestChassisCSS'`
Expected: PASS. (Re-run the whole package too: `go test ./internal/chassis/` — expect all green.)

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/data.go internal/chassis/handler.go internal/chassis/templates/shell.html internal/chassis/templates/setup-banner.html internal/chassis/setup_test.go
git commit -m "feat(chassis): render setup banner + open drawer + setup.js in setup mode"
```

---

## Task 5: CSS for setup banner and disabled cast controls

**Files:**
- Modify: `internal/chassis/static/chassis.css`

- [ ] **Step 1: Add `body.receiver.setup` rules**

Append to `internal/chassis/static/chassis.css` (all selectors MUST start with `body.receiver` to satisfy `TestChassisCSS_AllSelectorsScoped`):

```css
/* ---- First-run setup mode ---- */
body.receiver.setup .setup-banner {
  margin: 0 0 var(--gap, 12px);
  padding: 12px 16px;
  border: 1px solid var(--lcd-edge, #2c3a44);
  border-radius: 8px;
  background: color-mix(in oklch, var(--lcd, #0c1418) 80%, black);
  color: var(--lcd-ink, #cfe);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}
body.receiver.setup .setup-banner-title { font-weight: 600; margin: 0; flex: 1 1 100%; }
body.receiver.setup .setup-checklist { list-style: none; margin: 0; padding: 0; display: flex; gap: 16px; }
body.receiver.setup .setup-step::before { content: "○ "; opacity: .7; }
body.receiver.setup .setup-step.done { opacity: .85; }
body.receiver.setup .setup-step.done::before { content: "● "; opacity: 1; }
body.receiver.setup .setup-finish[disabled] { opacity: .5; cursor: not-allowed; }

/* Cast-initiation controls are visually disabled while setup is active.
   The .setup-disabled class is applied by setup.js to the cast trigger
   buttons; the server-side 409 gate remains the source of truth. */
body.receiver.setup .setup-disabled {
  pointer-events: none;
  opacity: .45;
  filter: grayscale(0.3);
}
```

> `--gap`, `--lcd`, `--lcd-edge`, `--lcd-ink` are illustrative; if the chassis uses different token names, substitute the nearest existing tokens (grep `:root` / `--` in `chassis.css`). The rule set just needs to be `body.receiver`-scoped and visually coherent — exact token names are not load-bearing.

- [ ] **Step 2: Verify CSS scope test passes**

Run: `go test ./internal/chassis/ -run TestChassisCSS`
Expected: PASS (all new selectors are `body.receiver`-scoped).

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "style(chassis): setup banner + disabled cast-control styles"
```

---

## Task 6: setup.js — Finish flow + status refresh + control disabling

**Files:**
- Create: `internal/chassis/static/setup.js`
- Create: `internal/chassis/testdata/setup.behavior.test.js` (manual node test)

- [ ] **Step 1: Create `internal/chassis/static/setup.js`**

```js
// setup.js — first-run setup mode client behavior. Loaded only when the
// page renders in setup mode (shell.html gates the <script> on .SetupMode).
// The server-side 409 gate is the source of truth; this is UX affordance.
(function () {
  'use strict';

  // Cast-trigger selectors disabled while setup mode is active. Kept here so
  // a single list governs the visual lockout; each cast script also guards
  // its POST via Chassis.setupBlocked() (see those files).
  var CAST_SELECTORS = [
    '#input-cast-button',
    '.preset-slot',
    '.catalog-channel-cast',
    '.history-replay',
    '#aux-start',
  ];

  function inSetup() {
    return document.body.classList.contains('setup');
  }

  function disableCastControls() {
    if (!inSetup()) return;
    CAST_SELECTORS.forEach(function (sel) {
      document.querySelectorAll(sel).forEach(function (el) {
        el.classList.add('setup-disabled');
        el.setAttribute('aria-disabled', 'true');
      });
    });
  }

  function applyStatus(st) {
    var steps = document.querySelectorAll('.setup-checklist .setup-step');
    if (steps[0]) steps[0].classList.toggle('done', !!st.hostSet);
    if (steps[1]) steps[1].classList.toggle('done', !!st.sourceEnabled);
    var finish = document.getElementById('setup-finish');
    if (finish) {
      finish.disabled = !st.complete;
      if (st.complete) finish.removeAttribute('aria-disabled');
      else finish.setAttribute('aria-disabled', 'true');
    }
  }

  function refreshStatus() {
    fetch('/receiver/setup/status', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (st) { if (st) applyStatus(st); })
      .catch(function () {});
  }

  function finish() {
    fetch('/receiver/setup/finish', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) {
        if (r.status === 200) {
          window.location.assign('/receiver');
        } else {
          // Criteria not yet met server-side; refresh the checklist.
          refreshStatus();
        }
      })
      .catch(function () {});
  }

  function init() {
    if (!inSetup()) return;
    disableCastControls();
    var btn = document.getElementById('setup-finish');
    if (btn) btn.addEventListener('click', finish);
    // Re-check status whenever the drawer reports a relevant save.
    document.addEventListener('chassis:settings-saved', refreshStatus);
    refreshStatus();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  if (window.Chassis) {
    window.Chassis.setup = { refreshStatus: refreshStatus, inSetup: inSetup };
  }
})();
```

> `CAST_SELECTORS` are best-effort visual targets. Before finalizing, open `templates/input-row.html`, `templates/preset-bank.html`, `templates/catalog*.html`, `templates/history*.html`, and the AUX control partial to confirm the real ids/classes for each cast trigger, and update the list to match. Missing or wrong selectors only weaken the cosmetic lockout — the server 409 and the per-script guards (Task 7) still enforce correctness.

- [ ] **Step 2: Create the behavior test `internal/chassis/testdata/setup.behavior.test.js`**

```js
// Run manually: node --test internal/chassis/testdata/setup.behavior.test.js
// (Not wired into Makefile/CI — mirrors the existing chassis JS test convention.)
const { test } = require('node:test');
const assert = require('node:assert');

// Minimal applyStatus extracted to validate checklist/Finish toggling logic.
function applyStatus(doc, st) {
  const steps = doc.steps;
  steps[0].done = !!st.hostSet;
  steps[1].done = !!st.sourceEnabled;
  doc.finish.disabled = !st.complete;
}

test('Finish disabled until complete; steps tick independently', () => {
  const doc = { steps: [{ done: false }, { done: false }], finish: { disabled: true } };
  applyStatus(doc, { hostSet: true, sourceEnabled: false, complete: false });
  assert.equal(doc.steps[0].done, true);
  assert.equal(doc.steps[1].done, false);
  assert.equal(doc.finish.disabled, true);
  applyStatus(doc, { hostSet: true, sourceEnabled: true, complete: true });
  assert.equal(doc.finish.disabled, false);
});
```

- [ ] **Step 3: Verify the package still builds/tests (embed picks up new static file)**

Run: `go test ./internal/chassis/`
Expected: PASS (the new `static/setup.js` is embedded; asset-version hash changes — that's expected).

Run (manual JS): `node --test internal/chassis/testdata/setup.behavior.test.js`
Expected: PASS (1 test).

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/setup.js internal/chassis/testdata/setup.behavior.test.js
git commit -m "feat(chassis): setup.js finish flow, status refresh, control lockout"
```

---

## Task 7: Client cast guards + settings-saved event

**Files:**
- Modify: `internal/chassis/static/chassis.js` (add `Chassis.setupBlocked` helper + guard `history/play`, `aux/start`)
- Modify: `internal/chassis/static/input-cast.js` (guard `/receiver/cast`, `/receiver/localfiles/cast`)
- Modify: `internal/chassis/static/preset-bank.js` (guard `/receiver/preset/{slot}/cast`)
- Modify: `internal/chassis/static/catalog-browser.js` (guard `/receiver/streams/cast`)
- Modify: `internal/chassis/static/settings-drawer.js` (guard `/receiver/settings/adapter/localfiles/cast`; dispatch `chassis:settings-saved`)

- [ ] **Step 1: Add the shared guard helper in `chassis.js`**

In `internal/chassis/static/chassis.js`, where `window.Chassis` is defined, add a helper:

```js
  // setupBlocked returns true while first-run setup mode is active. Cast
  // scripts call this before POSTing; the server-side 409 is authoritative.
  Chassis.setupBlocked = function () {
    if (!document.body.classList.contains('setup')) return false;
    if (Chassis.showChip) Chassis.showChip('FINISH SETUP');
    return true;
  };
```

> If `Chassis.showChip` is not the chip/notice helper name, use the chassis's existing transient-message helper (grep `showChip`/`showNotice`/`flash` in `chassis.js`). If none exists, omit the chip line — the early return alone is sufficient.

- [ ] **Step 2: Guard `history/play` and `aux/start` in `chassis.js`**

At the top of the function bodies that perform the fetches at `chassis.js:276` (`/receiver/history/play`) and `chassis.js:115` (`/receiver/aux/start`), add as the first statement inside the click/submit handler:

```js
      if (Chassis.setupBlocked()) return;
```

- [ ] **Step 3: Guard the casts in `input-cast.js`**

In `internal/chassis/static/input-cast.js`, add `if (window.Chassis && Chassis.setupBlocked()) return;` (or `return null;` to match the function's return type) as the first statement of the handlers that reach the fetches at lines 124/130 (`/receiver/cast`) and 288 (`/receiver/localfiles/cast`). Match the surrounding early-return style (these are `async` functions returning a parsed result — use `return null;`).

- [ ] **Step 4: Guard `preset-bank.js` and `catalog-browser.js`**

- `preset-bank.js`: first statement of the slot-cast handler reaching the fetch at line 138: `if (window.Chassis && Chassis.setupBlocked()) return;`
- `catalog-browser.js`: first statement of the channel-cast handler reaching the fetch at line 184: `if (window.Chassis && Chassis.setupBlocked()) return;` (do **not** guard the `preset/star` fetch at line 208 — starring is not gated).

- [ ] **Step 5: Guard the local-files cast + dispatch settings-saved in `settings-drawer.js`**

(a) Guard the cast at line 1159 (`/receiver/settings/adapter/localfiles/cast`): add `if (window.Chassis && Chassis.setupBlocked()) return;` as the first statement of that handler.

(b) Dispatch the event after successful saves. Add a small helper inside the drawer IIFE:

```js
  function notifySettingsSaved() {
    document.dispatchEvent(new CustomEvent('chassis:settings-saved'));
  }
```

Call `notifySettingsSaved();` at the bridge-save success branch (`saveField`, the `if (status >= 200 && status < 300 && body.ok)` at ~line 175) and at the adapter-save success branches (`saveAdapterField` success `if (payload.ok)` at ~line 557, and `handleAdapterSaveResponse` success `if (payload.ok)` at ~line 565). These are the saves that can flip `hostSet`/`sourceEnabled`.

> Per memory (settings-drawer IIFE scope): code appended after the IIFE is top-level and cannot see IIFE-local helpers. Add `notifySettingsSaved` and its call sites **inside** the existing IIFE, alongside `saveField`/`saveAdapterField`.

- [ ] **Step 6: Verify build + manual JS sanity**

Run: `go test ./internal/chassis/`
Expected: PASS (asset hash changes; Go tests unaffected).

Run: `node --check internal/chassis/static/chassis.js && node --check internal/chassis/static/input-cast.js && node --check internal/chassis/static/preset-bank.js && node --check internal/chassis/static/catalog-browser.js && node --check internal/chassis/static/settings-drawer.js && node --check internal/chassis/static/setup.js`
Expected: no output (all parse clean).

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/static/chassis.js internal/chassis/static/input-cast.js internal/chassis/static/preset-bank.js internal/chassis/static/catalog-browser.js internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): client cast guards + chassis:settings-saved event"
```

---

## Task 8: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Vet + full unit tests**

Run: `go vet ./... && go test ./...`
Expected: all packages `ok`. (`go test -race` is CI-only here — no local cgo; do not run it locally.)

- [ ] **Step 2: Integration smoke (if ffmpeg/ffprobe present)**

Run: `go test -tags=integration ./tests/integration/...`
Expected: PASS, or SKIP if ffmpeg/ffprobe are unavailable. If it fails for reasons unrelated to this change, note it; do not block on environment gaps.

- [ ] **Step 3: Manual visual check (optional, recommended)**

Per memory `reference_local_chassis_render`: render the chassis locally with a first-run fixture (`CHASSIS_REVIEW`-gated httptest server + headless-shell screenshot) to confirm the banner shows, the drawer is open on the Network pane, and cast buttons read disabled. Not required to pass; for confidence only.

- [ ] **Step 4: Final commit (if any verification fixups were needed)**

```bash
git add -A
git commit -m "test(chassis): verification fixups for first-run setup"
```

---

## Notes for the executor

- **Scope guard:** touch only `internal/chassis/**` (plus this plan/spec). No `internal/ui`, `internal/uiserver`, or `cmd/**` changes — the controller resolves by type assertion on the already-wired `BridgeSaver`.
- **`import_check_test.go`:** do not import `internal/ui` or `internal/uiserver` from `internal/chassis`. The plan adds none.
- **Test constructor:** build `*Server` via `New(...)` in tests that call `Mount`/`handleIndex` (memory: bare `&Server{}` nil-derefs the refresher). The pure-helper tests in Task 1/3 that never call `Mount` may use a bare `&Server{cfg: ...}` literal as shown.
- **Line numbers** (e.g. `chassis.js:276`) are anchors from the spec-time tree; confirm the fetch is the right one before editing.
