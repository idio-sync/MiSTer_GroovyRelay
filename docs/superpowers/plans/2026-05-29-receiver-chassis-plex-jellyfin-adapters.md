# Receiver Chassis Plex + Jellyfin Adapter Sections (Phase 4E) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two "Spec 4E" stubs in the chassis Settings drawer Adapters pane with real Plex and Jellyfin sections — config fields (riding 4D's saver unchanged) plus a shared, chassis-native Account/link sub-section that drives plex.tv PIN linking and Jellyfin username/password linking over JSON, with no htmx in the drawer.

**Architecture:** Each linkable adapter gains a thin, additive `adapters.LinkController` (Snapshot/Start/Poll/Unlink → `adapters.LinkSnapshot`) extracted from the orchestration currently inside its legacy `/ui` HTML handlers; the handlers are rewired to delegate (no behavior change). The chassis owns an `AdapterLinker` interface + `LinkView` wire shape and three JSON routes under `/receiver/settings/adapter/{name}/link/*`; vanilla JS renders link state and runs the Plex PIN poll loop. A `cmd/` binding maps `LinkSnapshot`→`LinkView` and dispatches by adapter name. The chassis imports no adapter package.

**Tech Stack:** Go 1.26 stdlib (`net/http` method-pattern mux, `html/template`, `sync`, `context`), embedded HTML/CSS/JS via `go:embed`, vanilla ES2022. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-05-29-receiver-chassis-plex-jellyfin-adapters-design.md`](../specs/2026-05-29-receiver-chassis-plex-jellyfin-adapters-design.md).

---

## Prerequisites

**Branch from `main`** (4D is merged):

```bash
git switch main
git pull --ff-only
git switch -c phase-4e-plex-jellyfin-adapters
```

**Verify the 4D + adapter contract is present before Task 1:**

```bash
# 4D chassis surface 4E rides:
grep -n "func settingsDataFromConfig" internal/chassis/data.go
grep -n "type AdapterPaneData" internal/chassis/data.go
grep -n "func writeSettingsChip\|func writeSettingsSuccess\|func writeJSON" internal/chassis/settings.go
grep -n "streamsRefreshGate" internal/chassis/server.go
# Adapter link primitives 4E reuses:
grep -n "func RequestPIN\|func PollPIN\|func RevokeDevice\|func pollForTokenCtx" internal/adapters/plex/linking.go internal/adapters/plex/link_ui.go
grep -n "func AuthenticateByName\|func Logout" internal/adapters/jellyfin/linking.go
grep -n "func LoadToken\|func SaveToken\|func WipeToken" internal/adapters/jellyfin/tokenstore.go
```

All must match. If any is missing, stop — the base branch is wrong.

**Naming facts (verified against `main`; do not "fix" these):**

| Looks like | Actually is |
|---|---|
| `s.buildSettingsData()` populates adapters | Production population is in package func `settingsDataFromConfig(cfg Config)` ([data.go:501](../../../internal/chassis/data.go#L501)); the pure `buildSettingsData(bridge, registry, catalog, catalogManager)` ([data.go:319](../../../internal/chassis/data.go#L319)) stays linker-agnostic |
| Plex `PollPIN(ctx, …)` | `PollPIN(id int, clientID string, timeout time.Duration)` — not ctx-aware; `pollForTokenCtx(ctx, pinID, uuid, timeout)` ([plex/link_ui.go:325](../../../internal/adapters/plex/link_ui.go#L325)) already wraps it with ctx cancellation |
| Plex linked identity | Plex stores only `StoredData{DeviceUUID, AuthToken}` ([plex/tokenstore.go:15](../../../internal/adapters/plex/tokenstore.go#L15)) — **no username**; linked label is plain "Linked" |
| chassis `json.Marshal` helpers | `writeSettingsSuccess(w, scope)`, `writeSettingsChip(w, status, chip)`, `writeSettingsFieldErrors(w, status, errs)`, `writeJSON(w, status, body)` ([settings.go:762-781,880](../../../internal/chassis/settings.go#L762)) |
| chassis `ServeHTTP`/`Handler()` | `(*Server).Mount(mux *http.ServeMux)`; routes mounted as `mux.Handle("POST /receiver/...", requireSameOrigin(http.HandlerFunc(...)))` ([server.go:276-285](../../../internal/chassis/server.go#L276)) |

---

## File Structure

**Created:**
- `internal/adapters/link.go` — shared `LinkController` interface + `LinkSnapshot` struct + phase constants. One responsibility: the transport-agnostic link contract both adapters implement and `cmd/` consumes.
- `internal/adapters/plex/link_core.go` — Plex `LinkController` impl (Snapshot/Start/Poll/Unlink) wrapping existing PIN primitives.
- `internal/adapters/plex/link_core_test.go`
- `internal/adapters/jellyfin/link_core.go` — Jellyfin `LinkController` impl (disk-hydrated Snapshot; credential Start; Unlink).
- `internal/adapters/jellyfin/link_core_test.go`
- `internal/chassis/templates/settings-adapter-plex.html`
- `internal/chassis/templates/settings-adapter-jellyfin.html`
- `internal/chassis/templates/settings-link.html` — shared Account sub-section (branches on Kind/Phase).
- `cmd/mister-groovy-relay/adapter_linker.go` — production `AdapterLinker` binding.
- `cmd/mister-groovy-relay/adapter_linker_test.go`
- `cmd/mister-groovy-relay/adapter_linker_e2e_test.go` — integration-tag Jellyfin credential flow end-to-end.

**Modified:**
- `internal/adapters/plex/link_ui.go` — rewire the 3 HTML handlers to delegate to `link_core.go` (no behavior change).
- `internal/adapters/jellyfin/link_ui.go` — rewire the 2 mutating HTML handlers to delegate.
- `internal/chassis/settings.go` — `AdapterLinker` interface, `LinkView`/`LinkField`, three handlers, error mapping.
- `internal/chassis/server.go` — `Config.AdapterLinker`, per-adapter link single-flight, 3 route mounts.
- `internal/chassis/data.go` — `AdapterPaneData.Linkable`/`LinkView`; add `plex`/`jellyfin` to the `settingsDataFromConfig` loop + `buildAdapterHint` cases; update the stale struct doc comment.
- `internal/chassis/templates/settings-adapters.html` — swap both stubs for the section templates.
- `internal/chassis/static/settings-drawer.js` — link handlers + PIN poll controller.
- `internal/chassis/static/chassis.css` — PIN/countdown/linked-collapse styles.
- `cmd/mister-groovy-relay/main.go` — construct + wire the `AdapterLinker` binding into `chassis.Config`.
- `internal/chassis/settings_test.go`, `chassis_test.go`, `data_test.go` — handler/render/data tests.
- `internal/chassis/testdata/*.behavior.test.js` (new behavior test alongside existing) — poll controller + safe repaint.

---

## Task 1: Shared `adapters.LinkController` contract

**Files:**
- Create: `internal/adapters/link.go`
- Create: `internal/adapters/link_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/link_test.go`:

```go
package adapters

import "testing"

func TestLinkPhaseConstants(t *testing.T) {
	cases := map[string]string{
		LinkPhaseUnlinked: "unlinked",
		LinkPhasePending:  "pending",
		LinkPhaseLinked:   "linked",
		LinkPhaseError:    "error",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("phase constant = %q, want %q", got, want)
		}
	}
}

// Compile-time guard: a value type can satisfy LinkController.
func TestLinkControllerShape(t *testing.T) {
	var _ LinkController = (*noopLinkController)(nil)
}

type noopLinkController struct{}

func (*noopLinkController) Snapshot() LinkSnapshot { return LinkSnapshot{Phase: LinkPhaseUnlinked} }
func (*noopLinkController) StartLink(_ contextContext, _ map[string]string) (LinkSnapshot, error) {
	return LinkSnapshot{}, nil
}
func (*noopLinkController) PollLink(_ contextContext) (LinkSnapshot, error) { return LinkSnapshot{}, nil }
func (*noopLinkController) Unlink(_ contextContext) (LinkSnapshot, error)   { return LinkSnapshot{}, nil }
```

> Note: replace `contextContext` with `context.Context` once the import is added in Step 3 — it is written deliberately wrong here so the test fails to compile until the contract exists.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters -run TestLinkPhase -v`
Expected: FAIL — `undefined: LinkPhaseUnlinked` (and the rest).

- [ ] **Step 3: Write the contract**

Create `internal/adapters/link.go`:

```go
package adapters

import "context"

// Link phases shared by every linkable adapter's LinkSnapshot and the
// chassis LinkView. Stable wire strings — the JS renderer branches on them.
const (
	LinkPhaseUnlinked = "unlinked"
	LinkPhasePending  = "pending"
	LinkPhaseLinked   = "linked"
	LinkPhaseError    = "error"
)

// LinkSnapshot is the transport-agnostic link state an adapter reports.
// The cmd/ binding maps it onto the chassis LinkView; the chassis never
// sees this type (it imports no adapter package).
type LinkSnapshot struct {
	Phase          string // one of LinkPhase*
	LinkedAs       string // optional identity; empty renders plain "Linked" (Plex)
	Code           string // pending only (Plex PIN)
	ExpiresInSec   int    // pending only (Plex PIN countdown)
	NeedsServerURL bool   // credential adapters with no server_url yet (Jellyfin)
	Error          string // error phase, or a linked-phase warning (post-auth restart trouble)
}

// LinkController is the orchestration a linkable adapter exposes so both
// the legacy /ui HTML handlers and the chassis JSON binding drive the
// same link-state machine. Implementations must keep the adapter's own
// phase + event emissions authoritative regardless of caller.
//
// Method names: the start method is StartLink (not Start) so an adapter
// can implement this interface directly — adapters.Adapter already
// requires a lifecycle Start(context.Context) error, and two Start
// methods on one receiver will not compile. Snapshot/PollLink/Unlink do
// not collide with the adapter interface.
type LinkController interface {
	// Snapshot returns current state with no side effects. Must be safe
	// to call before StartLink (initial drawer render), reading persisted
	// state from disk where the in-memory machine is not yet hydrated.
	Snapshot() LinkSnapshot
	// StartLink begins pairing. PIN adapters ignore params and return a
	// pending snapshot; credential adapters read params and return a
	// terminal (linked|error) snapshot.
	StartLink(ctx context.Context, params map[string]string) (LinkSnapshot, error)
	// PollLink advances/reads a pending flow (PIN adapters); credential
	// adapters return Snapshot().
	PollLink(ctx context.Context) (LinkSnapshot, error)
	// Unlink best-effort revokes/logs out, clears the token, returns an
	// unlinked snapshot. Idempotent.
	Unlink(ctx context.Context) (LinkSnapshot, error)
}
```

Then fix the test: replace `contextContext` with `context.Context` and add `"context"` to the test's imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters -run "TestLinkPhase|TestLinkControllerShape" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/link.go internal/adapters/link_test.go
git commit -m "feat(adapters): add shared LinkController + LinkSnapshot contract"
```

---

## Task 2: Plex `LinkController` — Snapshot (read path)

**Files:**
- Create: `internal/adapters/plex/link_core.go`
- Create: `internal/adapters/plex/link_core_test.go`

Snapshot maps the existing read state (`snapshotToken`, `snapshotPending`) onto `adapters.LinkSnapshot`. Plex stores no username, so `LinkedAs` stays empty.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/plex/link_core_test.go`:

```go
package plex

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestLinkSnapshot_Unlinked(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked {
		t.Errorf("Phase = %q, want unlinked", got.Phase)
	}
}

func TestLinkSnapshot_Linked(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{AuthToken: "tok"}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseLinked {
		t.Errorf("Phase = %q, want linked", got.Phase)
	}
	if got.LinkedAs != "" {
		t.Errorf("LinkedAs = %q, want empty (Plex stores no identity)", got.LinkedAs)
	}
}

func TestLinkSnapshot_Pending(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	a.pending = newPendingLink("K3F9", 42, time.Now().Add(10*time.Minute))
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhasePending {
		t.Errorf("Phase = %q, want pending", got.Phase)
	}
	if got.Code != "K3F9" {
		t.Errorf("Code = %q, want K3F9", got.Code)
	}
	if got.ExpiresInSec <= 0 || got.ExpiresInSec > 600 {
		t.Errorf("ExpiresInSec = %d, want (0,600]", got.ExpiresInSec)
	}
}

func TestLinkSnapshot_Expired(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	a.pending = newPendingLink("K3F9", 42, time.Now().Add(-time.Second))
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error", got.Phase)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/plex -run TestLinkSnapshot -v`
Expected: FAIL — `a.linkSnapshot undefined`.

- [ ] **Step 3: Implement Snapshot**

Create `internal/adapters/plex/link_core.go`:

```go
package plex

import (
	"context"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// linkSnapshot reports the current link state as an adapters.LinkSnapshot,
// mirroring the state logic in ExtraPanelHTML/handleLinkStatus. Plex
// persists only a device UUID + auth token (no account identity), so a
// linked snapshot carries an empty LinkedAs and the UI renders plain
// "Linked".
func (a *Adapter) linkSnapshot() adapters.LinkSnapshot {
	token := a.snapshotToken()
	pending := a.snapshotPending()

	switch {
	case token != "":
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseLinked}
	case pending != nil && pending.Done() && pending.Error() != "":
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: pending.Error()}
	case pending != nil && pending.Expired():
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "link code expired"}
	case pending != nil && !pending.Done():
		tl := pending.TimeLeft()
		return adapters.LinkSnapshot{
			Phase:        adapters.LinkPhasePending,
			Code:         pending.Code(),
			ExpiresInSec: int(tl / time.Second),
		}
	default:
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked}
	}
}

// Snapshot implements adapters.LinkController.
func (a *Adapter) Snapshot() adapters.LinkSnapshot { return a.linkSnapshot() }

// _ keeps context imported until StartLink/PollLink/Unlink land in Task 3.
var _ = context.Background
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/plex -run TestLinkSnapshot -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/plex/link_core.go internal/adapters/plex/link_core_test.go
git commit -m "feat(plex): LinkController.Snapshot from token+pending state"
```

---

## Task 3: Plex `LinkController` — Start / Poll / Unlink

**Files:**
- Modify: `internal/adapters/plex/link_core.go`
- Modify: `internal/adapters/plex/link_core_test.go`

Reuse the existing primitives (`RequestPIN`, `pollPendingLink`, `RevokeDevice`, `SaveStoredData`, `tokenFilePath`) so the state machine and `adapter-linked` events stay authoritative. `ctx` bounds the short `RequestPIN`/`RevokeDevice` round-trips; the 15-minute background poll goroutine (`pollPendingLink`) stays decoupled from `ctx`, exactly as today.

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/plex/link_core_test.go`:

```go
func TestLinkController_PollReadsState(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{AuthToken: "tok"}
	got, err := a.PollLink(context.Background())
	if err != nil {
		t.Fatalf("PollLink err = %v", err)
	}
	if got.Phase != adapters.LinkPhaseLinked {
		t.Errorf("Phase = %q, want linked", got.Phase)
	}
}

func TestLinkController_Conformance(t *testing.T) {
	var _ adapters.LinkController = (*Adapter)(nil)
}
```

Add `"context"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/plex -run "TestLinkController" -v`
Expected: FAIL — `*Adapter does not implement adapters.LinkController (missing StartLink)`.

- [ ] **Step 3: Implement StartLink/PollLink/Unlink**

Replace the `var _ = context.Background` placeholder at the bottom of `internal/adapters/plex/link_core.go` with the full implementation:

```go
// StartLink implements adapters.LinkController. params is ignored (Plex's
// PIN flow takes no inputs). It mirrors handleLinkStart: abandon any
// in-flight flow, request a fresh PIN under linkStartMu, store the
// pendingLink, and arm the background poller. ctx bounds the RequestPIN
// round-trip; the poller itself runs to the 15-minute expiry regardless
// of ctx (it must outlive the originating request).
func (a *Adapter) StartLink(ctx context.Context, _ map[string]string) (adapters.LinkSnapshot, error) {
	a.linkStartMu.Lock()
	defer a.linkStartMu.Unlock()

	if old := a.snapshotPending(); old != nil && !old.Done() {
		old.abandon()
	}

	deviceUUID := a.cfg.TokenStore.DeviceUUID
	deviceName := a.snapshotCfg().DeviceName

	type pinResult struct {
		pin *PinResponse
		err error
	}
	done := make(chan pinResult, 1)
	go func() {
		pin, err := RequestPIN(deviceUUID, deviceName, a.cfg.Version)
		done <- pinResult{pin, err}
	}()
	var pin *PinResponse
	select {
	case <-ctx.Done():
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "plex.tv request cancelled"}, nil
	case res := <-done:
		if res.err != nil {
			return adapters.LinkSnapshot{
				Phase: adapters.LinkPhaseError,
				Error: "plex.tv unreachable: " + res.err.Error(),
			}, nil
		}
		pin = res.pin
	}

	pl := newPendingLink(pin.Code, pin.ID, time.Now().Add(15*time.Minute))
	a.mu.Lock()
	a.pending = pl
	a.mu.Unlock()

	go a.pollPendingLink(pl, pin.ID, deviceUUID)

	return a.linkSnapshot(), nil
}

// PollLink implements adapters.LinkController by reading current state.
// The actual plex.tv polling happens in the background pollPendingLink
// goroutine; this just reports the latest snapshot so the chassis status
// route never stacks plex.tv network calls per browser tick.
func (a *Adapter) PollLink(_ context.Context) (adapters.LinkSnapshot, error) {
	return a.linkSnapshot(), nil
}

// Unlink implements adapters.LinkController. Mirrors handleUnlink:
// best-effort RevokeDevice (ctx-bounded), rotate the token file aside,
// clear the in-memory token, and cancel the plex.tv registration loop.
func (a *Adapter) Unlink(ctx context.Context) (adapters.LinkSnapshot, error) {
	a.mu.Lock()
	uuid := a.cfg.TokenStore.DeviceUUID
	token := a.cfg.TokenStore.AuthToken
	a.mu.Unlock()
	if token != "" {
		done := make(chan error, 1)
		go func() { done <- RevokeDevice(uuid, token) }()
		select {
		case <-ctx.Done():
			slog.Info("plex.tv revoke cancelled; proceeding with local cleanup")
		case err := <-done:
			if err != nil {
				slog.Info("plex.tv revoke failed; proceeding with local cleanup", "err", err)
			}
		}
	}

	src := tokenFilePath(a.cfg.Bridge.DataDir)
	dst := filepath.Join(a.cfg.Bridge.DataDir,
		fmt.Sprintf(".%s.unlinked-%d", storedDataFilename, time.Now().Unix()))
	_ = os.Rename(src, dst)

	a.mu.Lock()
	a.cfg.TokenStore.AuthToken = ""
	cancel := a.regCancel
	a.regCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked}, nil
}

// Compile-time conformance: *Adapter satisfies the link contract.
var _ adapters.LinkController = (*Adapter)(nil)
```

Update the import block of `link_core.go` to:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/plex -run "TestLinkController|TestLinkSnapshot" -v`
Expected: PASS (including `TestLinkController_Conformance`).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/plex/link_core.go internal/adapters/plex/link_core_test.go
git commit -m "feat(plex): implement LinkController Start/Poll/Unlink over existing PIN primitives"
```

---

## Task 4: Rewire Plex `/ui` handlers to delegate (no behavior change)

**Files:**
- Modify: `internal/adapters/plex/link_ui.go`
- Modify: `internal/adapters/plex/link_ui_test.go` (regression assertions)

The HTML handlers now derive their fragment from `linkSnapshot()` / the controller methods, so there is exactly one orchestration. The wire behavior (fragments + status codes) is unchanged.

- [ ] **Step 1: Add a regression test pinning current HTML+status**

Append to `internal/adapters/plex/link_ui_test.go` (create if absent, `package plex`):

```go
func TestHandleLinkStatus_UnlinkedHTMLUnchanged(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	rec := httptest.NewRecorder()
	a.handleLinkStatus(rec, httptest.NewRequest("GET", "/ui/adapter/plex/link/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="plex-link-slot"`) ||
		!strings.Contains(rec.Body.String(), "OFF · not linked") {
		t.Errorf("unlinked fragment changed:\n%s", rec.Body.String())
	}
}

func TestHandleLinkStatus_LinkedHTMLUnchanged(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{AuthToken: "tok"}
	rec := httptest.NewRecorder()
	a.handleLinkStatus(rec, httptest.NewRequest("GET", "/ui/adapter/plex/link/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "RUN · linked") {
		t.Errorf("linked fragment changed:\n%s", rec.Body.String())
	}
}
```

Add imports `net/http`, `net/http/httptest`, `strings`, `testing` if not present.

- [ ] **Step 2: Run to verify it passes against the CURRENT handler**

Run: `go test ./internal/adapters/plex -run TestHandleLinkStatus -v`
Expected: PASS (pins existing behavior before the refactor).

- [ ] **Step 3: Rewire `handleLinkStatus` to derive from the snapshot**

Replace the body of `handleLinkStatus` in `internal/adapters/plex/link_ui.go` with:

```go
func (a *Adapter) handleLinkStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	snap := a.linkSnapshot()
	switch snap.Phase {
	case adapters.LinkPhaseLinked:
		w.WriteHeader(http.StatusOK)
		_ = linkTemplate.ExecuteTemplate(w, "linked", struct{ TokenPath string }{
			TokenPath: tokenFilePath(a.cfg.Bridge.DataDir),
		})
	case adapters.LinkPhasePending:
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(renderPending(a.snapshotPending())))
	case adapters.LinkPhaseError:
		w.WriteHeader(http.StatusGone)
		_ = linkTemplate.ExecuteTemplate(w, "expired", nil)
	default:
		w.WriteHeader(http.StatusOK)
		_ = linkTemplate.ExecuteTemplate(w, "unlinked", nil)
	}
}
```

Then change `handleLinkStart` to call the controller for the mutation, preserving its existing fragment output. Replace everything in `handleLinkStart` between the `linkStartMu` lock and the final write with:

```go
func (a *Adapter) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	snap, _ := a.StartLink(r.Context(), nil)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if snap.Phase == adapters.LinkPhaseError {
		http.Error(w, "plex.tv unreachable: "+snap.Error, http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte(renderPending(a.snapshotPending())))
}
```

And rewire `handleUnlink` to delegate:

```go
func (a *Adapter) handleUnlink(w http.ResponseWriter, r *http.Request) {
	_, _ = a.Unlink(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = linkTemplate.ExecuteTemplate(w, "unlinked", nil)
}
```

Add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"` to the `link_ui.go` import block if not already present. Remove now-unused imports (`os`, `path/filepath`, `slog`, `eventlog` may still be used by `pollPendingLink`/`finishPendingLink`, which stay — only remove imports the compiler flags).

- [ ] **Step 4: Run the full Plex package + regression tests**

Run: `go test ./internal/adapters/plex -v`
Expected: PASS — `TestHandleLinkStatus*` still green (HTML unchanged), plus all pre-existing Plex tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/plex/link_ui.go internal/adapters/plex/link_ui_test.go
git commit -m "refactor(plex): /ui link handlers delegate to LinkController (no behavior change)"
```

---

## Task 5: Jellyfin `LinkController` — Snapshot (disk-hydrated)

**Files:**
- Create: `internal/adapters/jellyfin/link_core.go`
- Create: `internal/adapters/jellyfin/link_core_test.go`

Per spec, Jellyfin's in-memory `LinkState` is only hydrated at `Start()`, so Snapshot reads the persisted token directly: token present → linked (`"<user> on <server>"`); no token → unlinked (+ `NeedsServerURL` when `server_url` is blank); load/parse failure → error.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/jellyfin/link_core_test.go`:

```go
package jellyfin

import (
	"path/filepath"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func newTestAdapter(t *testing.T, serverURL string) *Adapter {
	t.Helper()
	a := &Adapter{deviceID: "dev-1"}
	a.cfg.ServerURL = serverURL
	a.dataDir = t.TempDir() // tokenPath() derives from this; see note below
	return a
}

func TestJFSnapshot_NoToken(t *testing.T) {
	a := newTestAdapter(t, "")
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked {
		t.Errorf("Phase = %q, want unlinked", got.Phase)
	}
	if !got.NeedsServerURL {
		t.Errorf("NeedsServerURL = false, want true (blank server_url)")
	}
}

func TestJFSnapshot_NoTokenWithURL(t *testing.T) {
	a := newTestAdapter(t, "http://jf.local:8096")
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked || got.NeedsServerURL {
		t.Errorf("got %+v, want unlinked + NeedsServerURL=false", got)
	}
}

func TestJFSnapshot_Linked(t *testing.T) {
	a := newTestAdapter(t, "http://jf.local:8096")
	if err := SaveToken(a.tokenPath(), Token{
		AccessToken: "tok", UserName: "jake", ServerID: "srv-9", ServerURL: "http://jf.local:8096",
	}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseLinked {
		t.Fatalf("Phase = %q, want linked", got.Phase)
	}
	if got.LinkedAs != "jake on srv-9" {
		t.Errorf("LinkedAs = %q, want 'jake on srv-9'", got.LinkedAs)
	}
}

func TestJFSnapshot_ParseError(t *testing.T) {
	a := newTestAdapter(t, "http://jf.local:8096")
	if err := writeFileForTest(a.tokenPath(), "{not json"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error", got.Phase)
	}
}

func writeFileForTest(path, body string) error {
	return osWriteFile(path, []byte(body), 0o600)
}
```

> Note on `a.dataDir` / `tokenPath()`: confirm the field `tokenPath()` reads from. If the adapter computes `tokenPath()` from `a.cfg.Bridge.DataDir` rather than an `a.dataDir` field, set that field in `newTestAdapter` instead. Run `grep -n "func (a \*Adapter) tokenPath" internal/adapters/jellyfin/*.go` and match it. Replace `osWriteFile` with `os.WriteFile` and add the `os` import.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/jellyfin -run TestJFSnapshot -v`
Expected: FAIL — `a.linkSnapshot undefined`.

- [ ] **Step 3: Implement Snapshot**

Create `internal/adapters/jellyfin/link_core.go`:

```go
package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// linkSnapshot reports link state from the persisted token, NOT the
// in-memory LinkState (which is only hydrated during Start). This makes
// the initial drawer render correct even when the adapter is disabled or
// not yet started. It never probes the server or wipes tokens — server_url
// drift / token rejection remain Start()'s responsibility.
func (a *Adapter) linkSnapshot() adapters.LinkSnapshot {
	tok, err := LoadToken(a.tokenPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return adapters.LinkSnapshot{
				Phase:          adapters.LinkPhaseUnlinked,
				NeedsServerURL: a.configuredServerURL() == "",
			}
		}
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: err.Error()}
	}
	if tok.AccessToken == "" {
		return adapters.LinkSnapshot{
			Phase:          adapters.LinkPhaseUnlinked,
			NeedsServerURL: a.configuredServerURL() == "",
		}
	}
	return adapters.LinkSnapshot{
		Phase:    adapters.LinkPhaseLinked,
		LinkedAs: fmt.Sprintf("%s on %s", tok.UserName, tok.ServerID),
	}
}

// Snapshot implements adapters.LinkController.
func (a *Adapter) Snapshot() adapters.LinkSnapshot { return a.linkSnapshot() }

var _ = context.Background // kept until Start/Unlink land in Task 6
var _ = strings.TrimSpace
```

> If `LoadToken` returns `(Token{}, nil)` for a missing file rather than an `fs.ErrNotExist` error, the `tok.AccessToken == ""` branch already covers it — both paths yield unlinked. Keep both branches.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/jellyfin -run TestJFSnapshot -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/jellyfin/link_core.go internal/adapters/jellyfin/link_core_test.go
git commit -m "feat(jellyfin): LinkController.Snapshot hydrated from persisted token"
```

---

## Task 6: Jellyfin `LinkController` — Start / Poll / Unlink

**Files:**
- Modify: `internal/adapters/jellyfin/link_core.go`
- Modify: `internal/adapters/jellyfin/link_core_test.go`

Extract the auth+persist+restart orchestration from `handleLinkStart` and the logout+wipe+stop from `handleUnlink` into controller methods returning `LinkSnapshot`.

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/jellyfin/link_core_test.go`:

```go
func TestJFController_StartMissingURL(t *testing.T) {
	a := newTestAdapter(t, "")
	got, _ := a.StartLink(contextTODO(), map[string]string{"username": "x", "password": "y"})
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error (no server_url)", got.Phase)
	}
}

func TestJFController_StartBlankCreds(t *testing.T) {
	a := newTestAdapter(t, "http://jf.local:8096")
	got, _ := a.StartLink(contextTODO(), map[string]string{"username": "", "password": ""})
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error (blank creds)", got.Phase)
	}
}

func TestJFController_Conformance(t *testing.T) {
	var _ adapters.LinkController = (*Adapter)(nil)
}

func contextTODO() context.Context { return context.TODO() }
```

Add `"context"` to the test imports.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/adapters/jellyfin -run "TestJFController" -v`
Expected: FAIL — `*Adapter does not implement adapters.LinkController (missing StartLink)`.

- [ ] **Step 3: Implement StartLink/PollLink/Unlink**

Replace the two trailing `var _` lines in `internal/adapters/jellyfin/link_core.go` with the implementation below. The adapter implements `adapters.LinkController` directly — the start method is named `StartLink`, so it does not clash with the lifecycle `Start(ctx) error` ([jellyfin/adapter.go:337](../../../internal/adapters/jellyfin/adapter.go#L337)); `PollLink`/`Unlink`/`Snapshot` don't clash with `adapters.Adapter` either.

```go
// StartLink implements adapters.LinkController. Reads username/password from
// params, authenticates against the saved server_url, persists the token,
// and (when enabled) restarts the adapter via the lifecycle Start to pick up
// the rotated token. Returns a terminal snapshot; a successful auth whose
// restart fails is still linked, with the restart trouble surfaced in Error.
func (a *Adapter) StartLink(ctx context.Context, params map[string]string) (adapters.LinkSnapshot, error) {
	serverURL := a.configuredServerURL()
	username := strings.TrimSpace(params["username"])
	password := params["password"]

	if serverURL == "" {
		a.link.SetError("set Server URL above and save before linking")
		return adapters.LinkSnapshot{
			Phase: adapters.LinkPhaseError, NeedsServerURL: true,
			Error: "Set a Server URL above and save before linking.",
		}, nil
	}
	if username == "" || password == "" {
		a.link.SetError("username and password are required")
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "Username and password are required."}, nil
	}

	a.link.SetLinking()
	res, err := AuthenticateByName(ctx, AuthRequest{
		ServerURL: serverURL, Username: username, Password: password,
		DeviceID: a.deviceID, Version: linkVersion,
	})
	if err != nil {
		a.link.SetError(err.Error())
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: err.Error()}, nil
	}

	tok := Token{
		AccessToken: res.AccessToken, UserID: res.UserID, UserName: res.UserName,
		ServerID: res.ServerID, ServerURL: serverURL,
	}
	if err := SaveToken(a.tokenPath(), tok); err != nil {
		a.link.SetError("link succeeded but persist failed: " + err.Error())
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "link succeeded but persist failed: " + err.Error()}, nil
	}
	a.link.SetLinked(res.UserName, res.ServerID)

	linkedAs := fmt.Sprintf("%s on %s", res.UserName, res.ServerID)
	a.mu.Lock()
	enabled := a.cfg.Enabled
	a.mu.Unlock()
	if enabled {
		_ = a.Stop()
		if startErr := a.Start(context.Background()); startErr != nil {
			return adapters.LinkSnapshot{
				Phase: adapters.LinkPhaseLinked, LinkedAs: linkedAs,
				Error: "adapter restart failed: " + startErr.Error(),
			}, nil
		}
	}
	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseLinked, LinkedAs: linkedAs}, nil
}

// PollLink implements adapters.LinkController; Jellyfin links synchronously,
// so polling just reports the current persisted state.
func (a *Adapter) PollLink(_ context.Context) (adapters.LinkSnapshot, error) {
	return a.linkSnapshot(), nil
}

// Unlink implements adapters.LinkController. Best-effort server logout
// (ctx-bounded), wipe the local token, reset link state, stop the adapter.
func (a *Adapter) Unlink(ctx context.Context) (adapters.LinkSnapshot, error) {
	tok, _ := LoadToken(a.tokenPath())
	if tok.AccessToken != "" {
		a.mu.Lock()
		deviceName := a.cfg.DeviceName
		a.mu.Unlock()
		if err := Logout(ctx, LogoutInput{
			ServerURL: tok.ServerURL, Token: tok.AccessToken,
			DeviceID: a.deviceID, DeviceName: deviceName, Version: linkVersion,
		}); err != nil {
			slog.Info("jellyfin: server-side logout failed; proceeding with local unlink", "err", err)
		}
	}
	_ = WipeToken(a.tokenPath())
	a.link.SetIdle()
	_ = a.Stop()
	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked, NeedsServerURL: a.configuredServerURL() == ""}, nil
}

// Compile-time conformance: *Adapter satisfies the link contract directly.
var _ adapters.LinkController = (*Adapter)(nil)
```

- [ ] **Step 4: Add `log/slog` import, run tests**

Update `link_core.go` imports to include `log/slog` (used by `Unlink`); `io/fs`/`errors`/`fmt`/`strings`/`context` are already present from Task 5. Run: `go test ./internal/adapters/jellyfin -run "TestJF" -v`
Expected: PASS including `TestJFController_Conformance`.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/jellyfin/link_core.go internal/adapters/jellyfin/link_core_test.go
git commit -m "feat(jellyfin): implement LinkController StartLink/PollLink/Unlink"
```

---

## Task 7: Rewire Jellyfin `/ui` handlers to delegate

**Files:**
- Modify: `internal/adapters/jellyfin/link_ui.go`
- Modify: `internal/adapters/jellyfin/link_ui_test.go`

- [ ] **Step 1: Add a regression test pinning the linked/unlinked fragments**

Append to `internal/adapters/jellyfin/link_ui_test.go`:

```go
func TestJFHandleUnlink_FragmentUnchanged(t *testing.T) {
	a := newTestAdapter(t, "http://jf.local:8096")
	_ = SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserName: "jake", ServerID: "s", ServerURL: "http://jf.local:8096"})
	rec := httptest.NewRecorder()
	a.handleUnlink(rec, httptest.NewRequest("POST", "/ui/adapter/jellyfin/unlink", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "Account") {
		t.Errorf("unlink fragment changed:\n%s", body)
	}
}
```

Add imports as needed (`net/http/httptest`, `strings`, `net/http`).

- [ ] **Step 2: Run to verify it passes against the current handler**

Run: `go test ./internal/adapters/jellyfin -run TestJFHandleUnlink -v`
Expected: PASS.

- [ ] **Step 3: Rewire `handleLinkStart` and `handleUnlink` to delegate**

Replace `handleLinkStart`'s body (everything after `ParseForm`) so it calls the controller and then renders the existing fragment from current state:

```go
func (a *Adapter) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderLinkFragment(w, "Bad form")
		return
	}
	snap, _ := a.StartLink(r.Context(), map[string]string{
		"username": r.FormValue("username"),
		"password": r.FormValue("password"),
	})
	// Preserve legacy fragment behavior: error text under the form,
	// linked/linking callout otherwise.
	errMsg := ""
	if snap.Phase == adapters.LinkPhaseError || (snap.Phase == adapters.LinkPhaseLinked && snap.Error != "") {
		errMsg = snap.Error
	}
	a.renderLinkFragment(w, errMsg)
}

func (a *Adapter) handleUnlink(w http.ResponseWriter, r *http.Request) {
	_, _ = a.Unlink(r.Context())
	a.renderLinkFragment(w, "")
}
```

Add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"` to `link_ui.go` imports. Remove imports made unused (`time`, `html` may still be used by `linkFragmentHTML` — only drop what the compiler flags). `handleLinkCancel` stays as-is.

- [ ] **Step 4: Run the full Jellyfin package**

Run: `go test ./internal/adapters/jellyfin -v`
Expected: PASS — regression + all pre-existing tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/jellyfin/link_ui.go internal/adapters/jellyfin/link_ui_test.go
git commit -m "refactor(jellyfin): /ui link handlers delegate to LinkController (no behavior change)"
```

---

## Task 8: Chassis `AdapterLinker` interface + `LinkView`/`LinkField`

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/settings_test.go`:

```go
type fakeAdapterLinker struct {
	views   map[string]LinkView
	startErr error
}

func (f *fakeAdapterLinker) LinkView(name string) (LinkView, bool) {
	v, ok := f.views[name]
	return v, ok
}
func (f *fakeAdapterLinker) StartLink(_ context.Context, name string, _ map[string]string) (LinkView, error) {
	if f.startErr != nil {
		return LinkView{}, f.startErr
	}
	return LinkView{Kind: "pin", Phase: "pending", Code: "K3F9", ExpiresInSec: 600}, nil
}
func (f *fakeAdapterLinker) LinkStatus(_ context.Context, name string) (LinkView, error) {
	v, _ := f.views[name]
	return v, nil
}
func (f *fakeAdapterLinker) Unlink(_ context.Context, name string) (LinkView, error) {
	return LinkView{Kind: "pin", Phase: "unlinked"}, nil
}

func TestAdapterLinker_StructuralConformance(t *testing.T) {
	var _ AdapterLinker = &fakeAdapterLinker{}
}
```

Add `"context"` to the test imports if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestAdapterLinker_StructuralConformance -v`
Expected: FAIL — `undefined: AdapterLinker` / `undefined: LinkView`.

- [ ] **Step 3: Add the interface + types**

In `internal/chassis/settings.go`, after the `StreamsRefresher` block, add:

```go
// AdapterLinker is the chassis-side interface backing the per-adapter
// /receiver/settings/adapter/{name}/link/* routes. The production binding
// (cmd/mister-groovy-relay) wraps each adapter's adapters.LinkController
// and maps its LinkSnapshot onto LinkView. The chassis imports no adapter
// package.
type AdapterLinker interface {
	// LinkView returns the current render state for an adapter's Account
	// sub-section, or ok=false if the named adapter is not linkable.
	LinkView(name string) (LinkView, bool)
	// StartLink begins pairing. PIN adapters ignore params; credential
	// adapters read params["username"]/["password"].
	StartLink(ctx context.Context, name string, params map[string]string) (LinkView, error)
	// LinkStatus polls progress (PIN adapters); credential adapters return
	// the current view.
	LinkStatus(ctx context.Context, name string) (LinkView, error)
	// Unlink revokes/logs out and clears the token. Idempotent.
	Unlink(ctx context.Context, name string) (LinkView, error)
}

// LinkView is the JSON wire/render shape for the Account sub-section.
type LinkView struct {
	Kind           string      `json:"kind"`            // "pin" | "credential"
	Phase          string      `json:"phase"`           // "unlinked"|"pending"|"linked"|"error"
	LinkedAs       string      `json:"linkedAs,omitempty"`
	Code           string      `json:"code,omitempty"`
	ExpiresInSec   int         `json:"expiresInSec,omitempty"`
	NeedsServerURL bool        `json:"needsServerURL,omitempty"`
	Error          string      `json:"error,omitempty"`
	Fields         []LinkField `json:"fields,omitempty"`
}

// LinkField is one credential input a credential adapter wants rendered.
type LinkField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // "text" | "secret"
}
```

Confirm `"context"` is imported in `settings.go` (it is — `handleSettingsActionStreamsRefresh` uses `context.WithTimeout`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestAdapterLinker_StructuralConformance -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): add AdapterLinker interface + LinkView/LinkField"
```

---

## Task 9: `Config.AdapterLinker` + `AdapterPaneData` link fields + per-adapter gate

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/data.go`

- [ ] **Step 1: Add the Config field + Server gate**

In `internal/chassis/server.go`, add to the `Config` struct (after `StreamsRefresher`):

```go
	// 4E: per-adapter link/pairing flow (Plex PIN, Jellyfin credentials).
	AdapterLinker AdapterLinker
```

Add to the `Server` struct (after `streamsRefreshGate`):

```go
	// linkStartGates enforces single-flight per adapter for the link/start
	// action. sync.Map keyed by adapter name → *sync.Mutex.
	linkStartGates sync.Map
```

Add this helper near the bottom of `server.go`:

```go
// linkStartGate returns the per-adapter single-flight mutex for link/start.
func (s *Server) linkStartGate(name string) *sync.Mutex {
	v, _ := s.linkStartGates.LoadOrStore(name, &sync.Mutex{})
	return v.(*sync.Mutex)
}
```

- [ ] **Step 2: Extend `AdapterPaneData` + fix the stale doc comment**

In `internal/chassis/data.go`, replace the `AdapterPaneData` doc comment + struct with:

```go
// AdapterPaneData carries the per-adapter render context for the Adapters
// pane. Populated by settingsDataFromConfig: config Fields/Values from the
// AdapterSettingsSaver, and (4E) Linkable + LinkView from the AdapterLinker
// for adapters that expose a link/pairing flow (Plex, Jellyfin).
type AdapterPaneData struct {
	Name      string
	Hint      string
	Fields    []adapters.FieldDef
	Values    map[string]any
	Providers []AdapterProviderRow
	Linkable  bool     // 4E — render the Account sub-section for this pane
	LinkView  LinkView // 4E — valid only when Linkable is true
}
```

- [ ] **Step 3: Build (no behavior change yet)**

Run: `go build ./internal/chassis/...`
Expected: builds. `go test ./internal/chassis -run TestAdapterLinker -v` still PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/server.go internal/chassis/data.go
git commit -m "feat(chassis): Config.AdapterLinker, AdapterPaneData link fields, per-adapter gate"
```

---

## Task 10: Render Plex/Jellyfin panes + populate `LinkView`

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/data_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/data_test.go`:

```go
func TestSettingsData_LinkablePanes(t *testing.T) {
	linker := &fakeAdapterLinker{views: map[string]LinkView{
		"plex":     {Kind: "pin", Phase: "unlinked"},
		"jellyfin": {Kind: "credential", Phase: "linked", LinkedAs: "jake on s"},
	}}
	saver := &fakeAdapterSettingsSaver{
		fields: map[string][]adapters.FieldDef{
			"plex":     {{Key: "enabled", Kind: adapters.KindBool}},
			"jellyfin": {{Key: "enabled", Kind: adapters.KindBool}},
		},
		current: map[string]map[string]any{
			"plex": {"enabled": true}, "jellyfin": {"enabled": false},
		},
	}
	data := settingsDataFromConfig(Config{AdapterSettingsSaver: saver, AdapterLinker: linker})

	byName := map[string]AdapterPaneData{}
	for _, p := range data.Adapters {
		byName[p.Name] = p
	}
	if !byName["plex"].Linkable || byName["plex"].LinkView.Kind != "pin" {
		t.Errorf("plex pane = %+v, want Linkable pin", byName["plex"])
	}
	if !byName["jellyfin"].Linkable || byName["jellyfin"].LinkView.LinkedAs != "jake on s" {
		t.Errorf("jellyfin pane = %+v, want Linkable + LinkedAs", byName["jellyfin"])
	}
}

func TestSettingsData_NilLinkerNotLinkable(t *testing.T) {
	saver := &fakeAdapterSettingsSaver{
		fields:  map[string][]adapters.FieldDef{"plex": {{Key: "enabled", Kind: adapters.KindBool}}},
		current: map[string]map[string]any{"plex": {"enabled": true}},
	}
	data := settingsDataFromConfig(Config{AdapterSettingsSaver: saver}) // no AdapterLinker
	for _, p := range data.Adapters {
		if p.Name == "plex" && p.Linkable {
			t.Errorf("plex Linkable=true with nil AdapterLinker; want false")
		}
	}
}
```

> If `fakeAdapterSettingsSaver` does not already exist in the chassis test files, reuse the one introduced by 4D (search `grep -n "fakeAdapterSettingsSaver" internal/chassis/*_test.go`). Match its field names; the shapes above (`fields`, `current`) mirror 4D's fake.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsData_ -v`
Expected: FAIL — plex/jellyfin not in `data.Adapters` (loop only covers dlna/torrent/streams).

- [ ] **Step 3: Extend the loop + hints**

In `internal/chassis/data.go` `settingsDataFromConfig`, change the loop list and add LinkView population:

```go
	if saver := cfg.AdapterSettingsSaver; saver != nil {
		for _, name := range []string{"dlna", "torrent", "streams", "plex", "jellyfin"} {
			fields, ok := saver.Fields(name)
			if !ok {
				continue
			}
			values, _ := saver.Current(name)
			pane := AdapterPaneData{
				Name:   name,
				Hint:   buildAdapterHint(cfg, name, values),
				Fields: fields,
				Values: values,
			}
			if name == "streams" {
				pane.Providers = buildStreamsProviderRows(cfg)
			}
			if linker := cfg.AdapterLinker; linker != nil {
				if lv, ok := linker.LinkView(name); ok {
					pane.Linkable = true
					pane.LinkView = lv
				}
			}
			data.Adapters = append(data.Adapters, pane)
		}
	}
```

Add Plex/Jellyfin cases to `buildAdapterHint` (before the final `return ""`):

```go
	case "plex":
		if v, _ := values["enabled"].(bool); v {
			return "CAST · LISTENING"
		}
		return "CAST · DISABLED"
	case "jellyfin":
		if v, _ := values["enabled"].(bool); v {
			return "CAST · LISTENING"
		}
		return "CAST · DISABLED"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestSettingsData_ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/data.go internal/chassis/data_test.go
git commit -m "feat(chassis): render plex/jellyfin panes + populate LinkView from AdapterLinker"
```

---

## Task 11: Chassis link handlers (start / status / unlink)

**Files:**
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
func newServerWithLinker(linker AdapterLinker) *Server {
	s, err := New(Config{Bridge: config.BridgeConfig{Mister: config.MisterConfig{Host: "x"}}, AdapterLinker: linker})
	if err != nil {
		panic(err)
	}
	return s
}

func TestLinkStart_NilLinker(t *testing.T) {
	s := newServerWithLinker(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/plex/link/start", nil)
	req.SetPathValue("name", "plex")
	s.handleSettingsAdapterLinkStart(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestLinkStart_UnknownAdapter(t *testing.T) {
	s := newServerWithLinker(&fakeAdapterLinker{views: map[string]LinkView{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/nope/link/start", nil)
	req.SetPathValue("name", "nope")
	s.handleSettingsAdapterLinkStart(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestLinkStart_Success(t *testing.T) {
	s := newServerWithLinker(&fakeAdapterLinker{views: map[string]LinkView{"plex": {Kind: "pin", Phase: "unlinked"}}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/plex/link/start", nil)
	req.SetPathValue("name", "plex")
	s.handleSettingsAdapterLinkStart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"phase":"pending"`) ||
		!strings.Contains(rec.Body.String(), `"code":"K3F9"`) {
		t.Errorf("body = %s, want pending view with code", rec.Body.String())
	}
}

func TestLinkUnlink_Success(t *testing.T) {
	s := newServerWithLinker(&fakeAdapterLinker{views: map[string]LinkView{"plex": {Kind: "pin", Phase: "linked"}}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/plex/link/unlink", nil)
	req.SetPathValue("name", "plex")
	s.handleSettingsAdapterLinkUnlink(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"phase":"unlinked"`) {
		t.Errorf("status=%d body=%s, want 200 unlinked", rec.Code, rec.Body.String())
	}
}
```

Ensure imports include `net/http`, `net/http/httptest`, `strings`, and `github.com/idio-sync/MiSTer_GroovyRelay/internal/config`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chassis -run "TestLinkStart|TestLinkUnlink" -v`
Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the three handlers + helpers**

Append to `internal/chassis/settings.go`:

```go
// link action timeouts (chained off r.Context()).
const (
	linkStartTimeout  = 15 * time.Second
	linkStatusTimeout = 5 * time.Second
	linkUnlinkTimeout = 5 * time.Second
)

// resolveLinkable returns (name, ok) after the nil-linker + linkability
// guards, writing the appropriate chip on failure. ok=false means a
// response was already written.
func (s *Server) resolveLinkable(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.cfg.AdapterLinker == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return "", false
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return "", false
	}
	if _, ok := s.cfg.AdapterLinker.LinkView(name); !ok {
		writeSettingsChip(w, http.StatusNotFound, "UNKNOWN ADAPTER")
		return "", false
	}
	return name, true
}

func writeLinkView(w http.ResponseWriter, view LinkView) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "view": view})
}

func (s *Server) handleSettingsAdapterLinkStart(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveLinkable(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	params := map[string]string{}
	for _, k := range []string{"username", "password"} {
		if v := r.PostForm.Get(k); v != "" {
			params[k] = v
		}
	}
	gate := s.linkStartGate(name)
	if !gate.TryLock() {
		writeSettingsChip(w, http.StatusConflict, "BUSY")
		return
	}
	defer gate.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), linkStartTimeout)
	defer cancel()
	view, err := s.cfg.AdapterLinker.StartLink(ctx, name, params)
	if err != nil {
		writeSettingsChip(w, http.StatusInternalServerError, "LINK FAILED")
		return
	}
	writeLinkView(w, view)
}

func (s *Server) handleSettingsAdapterLinkStatus(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveLinkable(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), linkStatusTimeout)
	defer cancel()
	view, err := s.cfg.AdapterLinker.LinkStatus(ctx, name)
	if err != nil {
		writeSettingsChip(w, http.StatusInternalServerError, "LINK FAILED")
		return
	}
	writeLinkView(w, view)
}

func (s *Server) handleSettingsAdapterLinkUnlink(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveLinkable(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), linkUnlinkTimeout)
	defer cancel()
	view, err := s.cfg.AdapterLinker.Unlink(ctx, name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeLinkView(w, view)
}
```

Confirm `time` is imported in `settings.go` (it is — `streamsRefreshTimeout` uses it).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis -run "TestLinkStart|TestLinkUnlink" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/settings_test.go
git commit -m "feat(chassis): link start/status/unlink JSON handlers"
```

---

## Task 12: Mount the three link routes

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing test (route registration through Mount)**

Append to `internal/chassis/settings_test.go`:

```go
func TestLinkRoutesMounted(t *testing.T) {
	s := newServerWithLinker(&fakeAdapterLinker{views: map[string]LinkView{"plex": {Kind: "pin", Phase: "unlinked"}}})
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("POST", "/receiver/settings/adapter/plex/link/start", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mounted start route status = %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/chassis -run TestLinkRoutesMounted -v`
Expected: FAIL — 404 (routes not mounted).

- [ ] **Step 3: Mount the routes**

In `internal/chassis/server.go` `Mount`, after the streams-refresh mount (`server.go:284-285`) and before `s.cacheOnce.Do(...)`, add:

```go
	mux.Handle("POST /receiver/settings/adapter/{name}/link/start",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLinkStart)))
	mux.Handle("GET /receiver/settings/adapter/{name}/link/status",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLinkStatus)))
	mux.Handle("POST /receiver/settings/adapter/{name}/link/unlink",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLinkUnlink)))
```

> Go 1.22+ pattern matching: `{name}` matches a single path segment, so `POST /receiver/settings/adapter/{name}` (4D save) and `POST /receiver/settings/adapter/{name}/link/start` (4E) are distinct, non-conflicting patterns.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/chassis -run TestLinkRoutesMounted -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/settings_test.go
git commit -m "feat(chassis): mount link start/status/unlink routes behind requireSameOrigin"
```

---

## Task 13: Shared `settings-link.html` template

**Files:**
- Create: `internal/chassis/templates/settings-link.html`
- Modify: `internal/chassis/chassis_test.go`

The template receives a `LinkView` and branches on `.Kind`/`.Phase`. It is `{{ define "settings-link" }}` so the section templates can invoke it.

- [ ] **Step 1: Write the failing render test**

Append to `internal/chassis/chassis_test.go`:

```go
func renderLink(t *testing.T, view LinkView) string {
	t.Helper()
	s := newRenderTestServer(t) // existing helper that parses templates; see note
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "settings-link", view); err != nil {
		t.Fatalf("execute settings-link: %v", err)
	}
	return buf.String()
}

func TestSettingsLink_Renders(t *testing.T) {
	cases := []struct {
		name string
		view LinkView
		want string
	}{
		{"pin-unlinked", LinkView{Kind: "pin", Phase: "unlinked"}, "Link Plex Account"},
		{"pin-pending", LinkView{Kind: "pin", Phase: "pending", Code: "K3F9", ExpiresInSec: 120}, "K3F9"},
		{"pin-linked", LinkView{Kind: "pin", Phase: "linked"}, "✓ Linked"},
		{"cred-linked", LinkView{Kind: "credential", Phase: "linked", LinkedAs: "jake on s"}, "jake on s"},
		{"cred-needurl", LinkView{Kind: "credential", Phase: "unlinked", NeedsServerURL: true}, "Server URL"},
		{"cred-form", LinkView{Kind: "credential", Phase: "unlinked", Fields: []LinkField{{Key: "username", Label: "Username", Kind: "text"}, {Key: "password", Label: "Password", Kind: "secret"}}}, "data-link-field"},
		{"error", LinkView{Kind: "credential", Phase: "error", Error: "Invalid credentials", Fields: []LinkField{{Key: "username", Label: "Username", Kind: "text"}}}, "Invalid credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderLink(t, tc.view)
			if !strings.Contains(got, tc.want) {
				t.Errorf("render(%+v) missing %q:\n%s", tc.view, tc.want, got)
			}
		})
	}
}
```

> Note on `newRenderTestServer`: reuse whatever helper the existing chassis render tests use to get a `*Server` with parsed templates (search `grep -n "tmpl.ExecuteTemplate\|func newTestServer\|template.Must" internal/chassis/chassis_test.go`). If tests execute templates via a package-level parsed set instead, call that. Add `bytes`/`strings` imports.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsLink_Renders -v`
Expected: FAIL — `settings-link` undefined / template not found.

- [ ] **Step 3: Create the template**

Create `internal/chassis/templates/settings-link.html`:

```html
{{- define "settings-link" -}}
<div class="settings-link" data-link-kind="{{ .Kind }}" data-link-phase="{{ .Phase }}">
  <h5 class="settings-subhead">Account</h5>
  {{- if eq .Phase "linked" -}}
    <div class="link-line ok">
      <span class="link-status">✓ Linked{{ if .LinkedAs }} as {{ .LinkedAs }}{{ end }}</span>
      <button class="action-btn ghost" type="button" data-link-action="unlink">Unlink</button>
    </div>
    {{- if .Error }}<div class="link-warn">{{ .Error }}</div>{{ end -}}
  {{- else if eq .Phase "pending" -}}
    {{- if eq .Kind "pin" -}}
      <div class="link-pin-wrap">
        <div class="help">Enter this code at <b>plex.tv/link</b>:</div>
        <div class="link-pin">{{ .Code }}</div>
        <div class="link-count" data-link-expires="{{ .ExpiresInSec }}">expires in <span class="link-count-val">{{ .ExpiresInSec }}</span>s</div>
        <div class="link-waiting">● waiting for plex.tv…</div>
      </div>
    {{- else -}}
      <div class="link-waiting">↻ Linking…</div>
    {{- end -}}
  {{- else if eq .Phase "error" -}}
    {{- if eq .Kind "pin" -}}
      <div class="link-line err"><span class="link-status">ERR · {{ .Error }}</span>
        <button class="action-btn" type="button" data-link-action="start">Try Again</button></div>
    {{- else -}}
      {{- template "settings-link-credform" . -}}
    {{- end -}}
  {{- else -}}
    {{- /* unlinked */ -}}
    {{- if eq .Kind "pin" -}}
      <div class="link-line">
        <div><span class="badge off">OFF · not linked</span>
          <div class="help">Link this bridge to your Plex account to receive casts.</div></div>
        <button class="action-btn" type="button" data-link-action="start">Link Plex Account</button>
      </div>
    {{- else if .NeedsServerURL -}}
      <div class="help">Set a <b>Server URL</b> in the fields below — it saves automatically — then link.</div>
    {{- else -}}
      {{- template "settings-link-credform" . -}}
    {{- end -}}
  {{- end -}}
</div>
{{- end -}}

{{- define "settings-link-credform" -}}
<form class="link-credform" data-link-action="start">
  {{- range .Fields }}
  <div class="field-row">
    <label>{{ .Label }}</label>
    <input class="field-input" type="{{ if eq .Kind "secret" }}password{{ else }}text{{ end }}"
      data-link-field="{{ .Key }}" name="{{ .Key }}" autocomplete="off">
    <span></span>
  </div>
  {{- end }}
  {{- if .Error }}<div class="link-warn">{{ .Error }}</div>{{ end }}
  <div class="field-row action-row"><label></label>
    <button class="action-btn" type="submit" data-link-submit>Link ▸</button><span></span></div>
</form>
{{- end -}}
```

> The template auto-escapes `.Error`, `.LinkedAs`, and `.Code` (Go `html/template`), so server-rendered states are XSS-safe. The JS repaint path (Task 16) must keep that guarantee with `textContent`.

- [ ] **Step 4: Register the template file (if templates are embedded by glob, skip)**

Confirm how chassis templates are parsed: `grep -n "ParseFS\|ParseGlob\|embed" internal/chassis/*.go`. If they parse `templates/*.html` by glob, the new file is picked up automatically. If files are listed explicitly, add `settings-link.html` to that list.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/chassis -run TestSettingsLink_Renders -v`
Expected: PASS for all subtests.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/templates/settings-link.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): shared settings-link template for the Account sub-section"
```

---

## Task 14: Plex/Jellyfin section templates + swap the stubs

**Files:**
- Create: `internal/chassis/templates/settings-adapter-plex.html`
- Create: `internal/chassis/templates/settings-adapter-jellyfin.html`
- Modify: `internal/chassis/templates/settings-adapters.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestAdaptersPane_PlexJellyfinSections(t *testing.T) {
	saver := &fakeAdapterSettingsSaver{
		fields: map[string][]adapters.FieldDef{
			"plex":     {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled"}},
			"jellyfin": {{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled"}},
		},
		current: map[string]map[string]any{"plex": {"enabled": true}, "jellyfin": {"enabled": false}},
	}
	linker := &fakeAdapterLinker{views: map[string]LinkView{
		"plex":     {Kind: "pin", Phase: "unlinked"},
		"jellyfin": {Kind: "credential", Phase: "unlinked", NeedsServerURL: true},
	}}
	html := renderAdaptersPane(t, Config{AdapterSettingsSaver: saver, AdapterLinker: linker})

	if strings.Contains(html, "Spec 4E") {
		t.Errorf("stub copy still present:\n%s", html)
	}
	// Section order: Plex above Jellyfin.
	if strings.Index(html, ">Plex<") > strings.Index(html, ">Jellyfin<") {
		t.Errorf("Plex should render above Jellyfin")
	}
	if !strings.Contains(html, "Link Plex Account") {
		t.Errorf("plex Account sub-section missing")
	}
}
```

> Note: `renderAdaptersPane` should render the `settings-adapters` template with a `SettingsData` built from `settingsDataFromConfig(cfg)`. Reuse/extend the existing chassis render-test helper; if none renders a single pane, execute `settings-adapters` against `settingsDataFromConfig(cfg)`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/chassis -run TestAdaptersPane_PlexJellyfin -v`
Expected: FAIL — "Spec 4E" still present / templates undefined.

- [ ] **Step 3: Create the section templates**

Create `internal/chassis/templates/settings-adapter-plex.html`:

```html
{{- define "settings-adapter-plex" -}}
<section class="settings-section" data-adapter-section="plex">
  <h4>Plex <span class="hint">{{ .Hint }}</span></h4>
  {{ if .Linkable }}{{ template "settings-link" .LinkView }}{{ end }}
  {{ range .Fields }}
  {{ template "adapter-field-row" (dict "Adapter" "plex" "Field" . "Values" $.Values) }}
  {{ end }}
</section>
{{- end -}}
```

Create `internal/chassis/templates/settings-adapter-jellyfin.html`:

```html
{{- define "settings-adapter-jellyfin" -}}
<section class="settings-section" data-adapter-section="jellyfin">
  <h4>Jellyfin <span class="hint">{{ .Hint }}</span></h4>
  {{ if .Linkable }}{{ template "settings-link" .LinkView }}{{ end }}
  {{ range .Fields }}
  {{ template "adapter-field-row" (dict "Adapter" "jellyfin" "Field" . "Values" $.Values) }}
  {{ end }}
</section>
{{- end -}}
```

- [ ] **Step 4: Swap the stubs**

In `internal/chassis/templates/settings-adapters.html`, replace the Plex stub block:

```html
  <!-- Plex (4E) -->
  <section class="settings-section">
    <h4>Plex <span class="hint">— pending</span></h4>
    <div class="action-result shown">▸ Spec 4E — implementation in progress</div>
  </section>
```

with:

```html
  {{ template "settings-adapter-plex" (adapterPane .Adapters "plex") }}
```

and replace the Jellyfin stub block:

```html
  <!-- Jellyfin (4E) -->
  <section class="settings-section">
    <h4>Jellyfin <span class="hint">— pending</span></h4>
    <div class="action-result shown">▸ Spec 4E — implementation in progress</div>
  </section>
```

with:

```html
  {{ template "settings-adapter-jellyfin" (adapterPane .Adapters "jellyfin") }}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/chassis -run "TestAdaptersPane_PlexJellyfin|TestSettingsLink" -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/templates/settings-adapter-plex.html internal/chassis/templates/settings-adapter-jellyfin.html internal/chassis/templates/settings-adapters.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): real Plex/Jellyfin sections; delete 4E stubs"
```

---

## Task 15: CSS for the Account sub-section

**Files:**
- Modify: `internal/chassis/static/chassis.css`

No test (pure styling, asserted indirectly by render tests). Keep the receiver palette.

- [ ] **Step 1: Append the styles**

Append to `internal/chassis/static/chassis.css` (within the settings-drawer section, after the `.settings-subhead` rule near line 4457):

```css
/* 4E — Account / link sub-section */
body.receiver .settings-link { padding: 4px 0 10px; border-bottom: 1px solid oklch(0.18 0.012 80); margin-bottom: 6px; }
body.receiver .settings-link .link-line { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
body.receiver .settings-link .link-status { font-size: 12px; color: oklch(0.80 0.12 150); }
body.receiver .settings-link .link-line.err .link-status { color: oklch(0.82 0.16 25); }
body.receiver .settings-link .badge.off { font-size: 9px; letter-spacing: 0.06em; padding: 2px 7px; border-radius: 2px; border: 1px solid oklch(0.40 0.16 25); color: oklch(0.82 0.16 25); }
body.receiver .settings-link .link-pin { font-family: 'DSEG7-Classic', monospace; font-weight: 700; font-size: 30px; letter-spacing: 0.30em; color: var(--vfd); text-shadow: 0 0 7px var(--vfd-glow-soft); padding: 4px 0 2px; }
body.receiver .settings-link .link-count { font-size: 10px; color: #8a8a8e; }
body.receiver .settings-link .link-waiting { font-size: 11px; color: var(--lock-amber); letter-spacing: 0.04em; margin-top: 4px; }
body.receiver .settings-link .link-warn { font-size: 10px; color: oklch(0.82 0.16 25); margin-top: 5px; }
body.receiver .settings-link .link-credform .action-row .action-btn { margin-left: auto; }
/* Collapse-when-linked: linked phase shows only the one-liner (the template
   already emits just .link-line for linked); pending/error/unlinked expand. */
body.receiver .settings-link[data-link-phase="linked"] { padding-bottom: 8px; }
```

- [ ] **Step 2: Build the embed + run chassis tests**

Run: `go test ./internal/chassis -run TestSettingsLink -v`
Expected: PASS (CSS is embedded; tests still green).

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "style(chassis): Account sub-section PIN/countdown/linked styles"
```

---

## Task 16: JS — link start/unlink handlers + safe repaint

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`
- Create: `internal/chassis/testdata/settings-link.behavior.test.js`

- [ ] **Step 1: Write the failing behavior test**

Create `internal/chassis/testdata/settings-link.behavior.test.js` modeled on the existing `source-cluster.behavior.test.js` harness (search it for the DOM/fetch-stub setup). Assert:

```js
// renderLinkView builds DOM with textContent (no innerHTML of server text),
// password inputs cleared after submit, and credential inputs do not carry
// [data-adapter]/[data-field] (so 4D autosave never grabs them).
test('renderLinkView escapes error text via textContent', () => {
  const el = document.createElement('div');
  el.className = 'settings-link';
  Chassis.settings.renderLinkView(el, { kind: 'credential', phase: 'error', error: '<img src=x onerror=alert(1)>', fields: [{key:'username',label:'Username',kind:'text'}] });
  assert(!el.innerHTML.includes('<img'), 'error text must not be injected as HTML');
  assert(el.textContent.includes('<img src=x'), 'error text present as text');
});

test('credential inputs avoid 4D autosave selectors', () => {
  const el = document.createElement('div');
  Chassis.settings.renderLinkView(el, { kind: 'credential', phase: 'unlinked', fields: [{key:'username',label:'Username',kind:'text'},{key:'password',label:'Password',kind:'secret'}] });
  assert(el.querySelector('[data-adapter]') === null, 'no data-adapter on link inputs');
  assert(el.querySelector('[data-field]') === null, 'no data-field on link inputs');
  assert(el.querySelector('[data-link-field="username"]') !== null, 'uses data-link-field');
});
```

Run it via the same node/test runner the existing behavior test uses (check `grep -rn "behavior.test.js" internal/chassis` and the Makefile/CI for the runner command).

- [ ] **Step 2: Run to verify it fails**

Run the behavior-test runner (e.g. `node internal/chassis/testdata/run-behavior.js settings-link` — match the existing invocation).
Expected: FAIL — `Chassis.settings.renderLinkView` undefined.

- [ ] **Step 3: Implement `renderLinkView` + handlers**

In `internal/chassis/static/settings-drawer.js`, inside the IIFE (before the `window.Chassis.settings` exports near line 607), add the renderer and handlers:

```js
  // renderLinkView rebuilds a .settings-link container's inner DOM from a
  // LinkView object. Untrusted strings (error, linkedAs, code) go through
  // textContent — never innerHTML — so remote/operator text can't inject markup.
  function el(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }
  function renderLinkView(container, view) {
    container.setAttribute('data-link-kind', view.kind || '');
    container.setAttribute('data-link-phase', view.phase || '');
    container.replaceChildren();
    container.appendChild(el('h5', 'settings-subhead', 'Account'));

    if (view.phase === 'linked') {
      const line = el('div', 'link-line ok');
      line.appendChild(el('span', 'link-status', view.linkedAs ? `✓ Linked as ${view.linkedAs}` : '✓ Linked'));
      const btn = el('button', 'action-btn ghost', 'Unlink');
      btn.type = 'button'; btn.setAttribute('data-link-action', 'unlink');
      line.appendChild(btn);
      container.appendChild(line);
      if (view.error) container.appendChild(el('div', 'link-warn', view.error));
      return;
    }
    if (view.phase === 'pending') {
      if (view.kind === 'pin') {
        const wrap = el('div', 'link-pin-wrap');
        wrap.appendChild(el('div', 'help', 'Enter this code at plex.tv/link:'));
        wrap.appendChild(el('div', 'link-pin', view.code || ''));
        const c = el('div', 'link-count');
        c.setAttribute('data-link-expires', String(view.expiresInSec || 0));
        c.appendChild(document.createTextNode('expires in '));
        c.appendChild(el('span', 'link-count-val', String(view.expiresInSec || 0)));
        c.appendChild(document.createTextNode('s'));
        wrap.appendChild(c);
        wrap.appendChild(el('div', 'link-waiting', '● waiting for plex.tv…'));
        container.appendChild(wrap);
      } else {
        container.appendChild(el('div', 'link-waiting', '↻ Linking…'));
      }
      return;
    }
    if (view.phase === 'unlinked' && view.kind === 'pin') {
      const line = el('div', 'link-line');
      const left = el('div');
      left.appendChild(el('span', 'badge off', 'OFF · not linked'));
      left.appendChild(el('div', 'help', 'Link this bridge to your Plex account to receive casts.'));
      line.appendChild(left);
      const btn = el('button', 'action-btn', 'Link Plex Account');
      btn.type = 'button'; btn.setAttribute('data-link-action', 'start');
      line.appendChild(btn);
      container.appendChild(line);
      return;
    }
    if (view.kind === 'credential' && view.phase === 'unlinked' && view.needsServerURL) {
      container.appendChild(el('div', 'help', 'Set a Server URL in the fields below — it saves automatically — then link.'));
      return;
    }
    // credential unlinked-with-url OR error → form
    const form = el('form', 'link-credform');
    form.setAttribute('data-link-action', 'start');
    (view.fields || []).forEach((f) => {
      const row = el('div', 'field-row');
      row.appendChild(el('label', null, f.label));
      const inp = el('input', 'field-input');
      inp.type = f.kind === 'secret' ? 'password' : 'text';
      inp.setAttribute('data-link-field', f.key);
      inp.setAttribute('name', f.key);
      inp.setAttribute('autocomplete', 'off');
      row.appendChild(inp);
      row.appendChild(el('span'));
      form.appendChild(row);
    });
    if (view.error) form.appendChild(el('div', 'link-warn', view.error));
    const actionRow = el('div', 'field-row action-row');
    actionRow.appendChild(el('label'));
    const submit = el('button', 'action-btn', 'Link ▸');
    submit.type = 'submit'; submit.setAttribute('data-link-submit', '');
    actionRow.appendChild(submit);
    actionRow.appendChild(el('span'));
    form.appendChild(actionRow);
    container.appendChild(form);
  }

  function adapterOfLink(node) {
    const sec = node.closest('[data-adapter-section]');
    return sec ? sec.getAttribute('data-adapter-section') : null;
  }

  async function postLink(adapter, action, body) {
    const res = await fetch(`/receiver/settings/adapter/${encodeURIComponent(adapter)}/link/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body ? body.toString() : '',
    });
    return res.json();
  }

  // Start (PIN button or credential form submit) + Unlink, delegated.
  document.addEventListener('click', async (ev) => {
    const startBtn = ev.target.closest('button[data-link-action="start"]');
    const unlinkBtn = ev.target.closest('button[data-link-action="unlink"]');
    if (!startBtn && !unlinkBtn) return;
    ev.preventDefault();
    const btn = startBtn || unlinkBtn;
    if (btn.disabled) return;
    const container = btn.closest('.settings-link');
    const adapter = adapterOfLink(btn);
    if (!adapter) return;
    btn.disabled = true;
    try {
      const payload = await postLink(adapter, unlinkBtn ? 'unlink' : 'start', null);
      if (payload.ok && payload.view) {
        renderLinkView(container, payload.view);
        if (payload.view.phase === 'pending' && payload.view.kind === 'pin') startPoll(adapter, container);
      } else if (payload.chip) {
        showNotice(payload.chip, 'err');
      }
    } catch (e) {
      showNotice('NETWORK ERROR', 'err');
    } finally {
      btn.disabled = false;
    }
  });

  document.addEventListener('submit', async (ev) => {
    const form = ev.target.closest('form.link-credform');
    if (!form) return;
    ev.preventDefault();
    const container = form.closest('.settings-link');
    const adapter = adapterOfLink(form);
    if (!adapter) return;
    const body = new URLSearchParams();
    form.querySelectorAll('[data-link-field]').forEach((inp) => body.set(inp.getAttribute('data-link-field'), inp.value));
    const submit = form.querySelector('[data-link-submit]');
    if (submit) submit.disabled = true;
    // optimistic "Linking…"
    renderLinkView(container, { kind: 'credential', phase: 'pending' });
    try {
      const payload = await postLink(adapter, 'start', body);
      if (payload.ok && payload.view) {
        renderLinkView(container, payload.view); // clears password (fresh inputs)
      } else if (payload.chip) {
        showNotice(payload.chip, 'err');
      }
    } catch (e) {
      showNotice('NETWORK ERROR', 'err');
    }
  });
```

Then add to the exports block near line 607:

```js
  window.Chassis.settings.renderLinkView = renderLinkView;
```

> Password clearing: `renderLinkView` rebuilds the form with empty inputs on every repaint, so the password field is never retained after submit or error.

- [ ] **Step 4: Run the behavior test to verify it passes**

Run the behavior-test runner for `settings-link`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/settings-drawer.js internal/chassis/testdata/settings-link.behavior.test.js
git commit -m "feat(chassis): link start/unlink JS handlers + safe DOM repaint"
```

---

## Task 17: JS — PIN poll controller

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`
- Modify: `internal/chassis/testdata/settings-link.behavior.test.js`

- [ ] **Step 1: Write the failing behavior test**

Append to `internal/chassis/testdata/settings-link.behavior.test.js`:

```js
test('poll controller single-flight + stop on terminal', async () => {
  let calls = 0;
  globalThis.fetch = async () => { calls++; return { json: async () => ({ ok: true, view: { kind: 'pin', phase: calls >= 2 ? 'linked' : 'pending', code: 'K3F9', expiresInSec: 100 } }) }; };
  const el = document.createElement('div');
  el.className = 'settings-link';
  document.body.appendChild(el);
  const sec = document.createElement('div'); sec.setAttribute('data-adapter-section', 'plex'); sec.appendChild(el);
  Chassis.settings.startPoll('plex', el);
  Chassis.settings.startPoll('plex', el); // second start must not double-run
  await Chassis.settings.__pollTick('plex'); // first tick → pending
  await Chassis.settings.__pollTick('plex'); // second tick → linked, stops
  assert(el.getAttribute('data-link-phase') === 'linked', 'should reach linked');
  assert(Chassis.settings.__pollActive('plex') === false, 'poller stopped on terminal');
});
```

> Match the test harness's async tick model; if it cannot drive timers, expose `__pollTick`/`__pollActive` test hooks as shown so the loop is deterministic (no real `setTimeout` in tests).

- [ ] **Step 2: Run to verify it fails**

Run the behavior-test runner.
Expected: FAIL — `startPoll` / `__pollTick` undefined.

- [ ] **Step 3: Implement the poll controller**

In `internal/chassis/static/settings-drawer.js`, add inside the IIFE:

```js
  // One poll controller per adapter. setTimeout (not setInterval) so a slow
  // response can't stack requests. Stops on terminal phase, expiry, unlink,
  // or pane/drawer close.
  const pollers = {}; // adapter -> {container, stopped}

  async function pollOnce(adapter) {
    const p = pollers[adapter];
    if (!p || p.stopped) return;
    let payload;
    try {
      const res = await fetch(`/receiver/settings/adapter/${encodeURIComponent(adapter)}/link/status`, { method: 'GET' });
      payload = await res.json();
    } catch (e) {
      // transient: keep polling until expiry
      scheduleNextPoll(adapter);
      return;
    }
    if (!payload.ok || !payload.view) { stopPoll(adapter); return; }
    renderLinkView(p.container, payload.view);
    const v = payload.view;
    // Stop priority: (1) terminal phase, (2) expiry, (3/4) handled by stopPoll callers.
    if (v.phase === 'linked' || v.phase === 'error' || (v.expiresInSec || 0) <= 0) {
      stopPoll(adapter);
      return;
    }
    scheduleNextPoll(adapter);
  }

  function scheduleNextPoll(adapter) {
    const p = pollers[adapter];
    if (!p || p.stopped) return;
    p.timer = setTimeout(() => pollOnce(adapter), 2000);
  }

  function startPoll(adapter, container) {
    if (pollers[adapter] && !pollers[adapter].stopped) return; // single-flight
    pollers[adapter] = { container, stopped: false, timer: null };
    scheduleNextPoll(adapter);
  }

  function stopPoll(adapter) {
    const p = pollers[adapter];
    if (!p) return;
    p.stopped = true;
    if (p.timer) clearTimeout(p.timer);
  }

  // Stop all pollers when the drawer/Adapters pane is hidden.
  function stopAllPolls() { Object.keys(pollers).forEach(stopPoll); }

  // Test hooks (deterministic, no real timers).
  window.Chassis.settings.startPoll = startPoll;
  window.Chassis.settings.stopPoll = stopPoll;
  window.Chassis.settings.stopAllPolls = stopAllPolls;
  window.Chassis.settings.__pollTick = (adapter) => pollOnce(adapter);
  window.Chassis.settings.__pollActive = (adapter) => !!(pollers[adapter] && !pollers[adapter].stopped);
```

Wire `stopAllPolls()` into the existing drawer-close / pane-switch handlers (search `grep -n "drawer-open\|closeDrawer\|data-pane" internal/chassis/static/settings-drawer.js` and call `stopAllPolls()` where the drawer closes or the Adapters pane is left). Also call `stopPoll(adapter)` in the unlink click handler (Task 16) after a successful unlink.

- [ ] **Step 4: Run to verify it passes**

Run the behavior-test runner.
Expected: PASS — single-flight + stop-on-terminal.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/settings-drawer.js internal/chassis/testdata/settings-link.behavior.test.js
git commit -m "feat(chassis): PIN poll controller (single-flight, stop-on-terminal, 2s setTimeout)"
```

---

## Task 18: `cmd/` production `AdapterLinker` binding

**Files:**
- Create: `cmd/mister-groovy-relay/adapter_linker.go`
- Create: `cmd/mister-groovy-relay/adapter_linker_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/adapter_linker_test.go`:

```go
package main

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

type fakeLinkAdapter struct {
	adapters.Adapter // embed nil; only LinkController methods used in this test
	snap             adapters.LinkSnapshot
}

func (f *fakeLinkAdapter) Snapshot() adapters.LinkSnapshot                              { return f.snap }
func (f *fakeLinkAdapter) StartLink(context.Context, map[string]string) (adapters.LinkSnapshot, error) { return f.snap, nil }
func (f *fakeLinkAdapter) PollLink(context.Context) (adapters.LinkSnapshot, error)      { return f.snap, nil }
func (f *fakeLinkAdapter) Unlink(context.Context) (adapters.LinkSnapshot, error) {
	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked}, nil
}

type fakeReg struct{ m map[string]adapters.Adapter }

func (r fakeReg) Get(name string) (adapters.Adapter, bool) { a, ok := r.m[name]; return a, ok }

func TestAdapterLinker_KindAndFields(t *testing.T) {
	reg := fakeReg{m: map[string]adapters.Adapter{
		"plex":     &fakeLinkAdapter{snap: adapters.LinkSnapshot{Phase: "unlinked"}},
		"jellyfin": &fakeLinkAdapter{snap: adapters.LinkSnapshot{Phase: "unlinked", NeedsServerURL: true}},
		"dlna":     &fakeLinkAdapter{}, // present but NOT a LinkController? see note
	}}
	l := newAdapterLinker(reg)

	pv, ok := l.LinkView("plex")
	if !ok || pv.Kind != "pin" || len(pv.Fields) != 0 {
		t.Errorf("plex view = %+v ok=%v, want pin + no fields", pv, ok)
	}
	jv, ok := l.LinkView("jellyfin")
	if !ok || jv.Kind != "credential" || len(jv.Fields) != 2 {
		t.Errorf("jellyfin view = %+v ok=%v, want credential + 2 fields", jv, ok)
	}
}

var _ chassis.AdapterLinker = (*adapterLinker)(nil)
```

> Note: `dlna` in the fake DOES implement LinkController here (the embedded fake has the methods), which is unrealistic — real dlna does not. To test the "not linkable" path precisely, add a `fakeNonLinkAdapter` that embeds `adapters.Adapter` but defines none of the Link methods, register it as `"dlna"`, and assert `_, ok := l.LinkView("dlna"); ok == false`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/mister-groovy-relay -run TestAdapterLinker_KindAndFields -v`
Expected: FAIL — `newAdapterLinker` undefined.

- [ ] **Step 3: Implement the binding**

Create `cmd/mister-groovy-relay/adapter_linker.go`:

```go
package main

import (
	"context"
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

// adapterLinker binds chassis.AdapterLinker to the registry's
// adapters.LinkController-implementing adapters (Plex, Jellyfin). It maps
// adapters.LinkSnapshot → chassis.LinkView and decides Kind/Fields by name.
// The chassis never imports an adapter package; this binding does the join.
type adapterLinker struct{ reg adapterLookup }

func newAdapterLinker(reg adapterLookup) *adapterLinker { return &adapterLinker{reg: reg} }

func (l *adapterLinker) controller(name string) (adapters.LinkController, bool) {
	a, ok := l.reg.Get(name)
	if !ok {
		return nil, false
	}
	lc, ok := a.(adapters.LinkController)
	return lc, ok
}

func linkKind(name string) string {
	if name == "jellyfin" {
		return "credential"
	}
	return "pin" // plex
}

func linkFields(name string) []chassis.LinkField {
	if name == "jellyfin" {
		return []chassis.LinkField{
			{Key: "username", Label: "Username", Kind: "text"},
			{Key: "password", Label: "Password", Kind: "secret"},
		}
	}
	return nil
}

func toLinkView(name string, snap adapters.LinkSnapshot) chassis.LinkView {
	v := chassis.LinkView{
		Kind:           linkKind(name),
		Phase:          snap.Phase,
		LinkedAs:       snap.LinkedAs,
		Code:           snap.Code,
		ExpiresInSec:   snap.ExpiresInSec,
		NeedsServerURL: snap.NeedsServerURL,
		Error:          snap.Error,
	}
	// Credential adapters render their inputs whenever not linked.
	if linkKind(name) == "credential" && snap.Phase != adapters.LinkPhaseLinked {
		v.Fields = linkFields(name)
	}
	return v
}

func (l *adapterLinker) LinkView(name string) (chassis.LinkView, bool) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, false
	}
	return toLinkView(name, lc.Snapshot()), true
}

func (l *adapterLinker) StartLink(ctx context.Context, name string, params map[string]string) (chassis.LinkView, error) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, fmt.Errorf("adapter %q is not linkable", name)
	}
	snap, err := lc.StartLink(ctx, params)
	if err != nil {
		return chassis.LinkView{}, err
	}
	return toLinkView(name, snap), nil
}

func (l *adapterLinker) LinkStatus(ctx context.Context, name string) (chassis.LinkView, error) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, fmt.Errorf("adapter %q is not linkable", name)
	}
	snap, err := lc.PollLink(ctx)
	if err != nil {
		return chassis.LinkView{}, err
	}
	return toLinkView(name, snap), nil
}

func (l *adapterLinker) Unlink(ctx context.Context, name string) (chassis.LinkView, error) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, fmt.Errorf("adapter %q is not linkable", name)
	}
	snap, err := lc.Unlink(ctx)
	if err != nil {
		return chassis.LinkView{}, err
	}
	return toLinkView(name, snap), nil
}
```

`adapterLookup` is the existing interface in `cmd/mister-groovy-relay/adapter_settings_saver.go` (`Get(name string) (adapters.Adapter, bool)`) — reuse it; do not redeclare.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/mister-groovy-relay -run TestAdapterLinker -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_linker.go cmd/mister-groovy-relay/adapter_linker_test.go
git commit -m "feat(cmd): production AdapterLinker binding (snapshot->view, kind/fields by name)"
```

---

## Task 19: Wire the binding into `chassis.Config`

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Wire it**

In `cmd/mister-groovy-relay/main.go`, where `chassis.Config` is constructed (the same place `AdapterSettingsSaver`/`StreamsRefresher` are set — search `grep -n "AdapterSettingsSaver:" cmd/mister-groovy-relay/main.go`), add:

```go
		AdapterLinker: newAdapterLinker(registry),
```

Use the same registry value already passed to `newBridgeAdapterSettingsSaver`. If that value is a concrete `*adapters.Registry`, it already satisfies `adapterLookup` (it has `Get`).

- [ ] **Step 2: Build the binary**

Run: `go build ./cmd/mister-groovy-relay`
Expected: builds cleanly.

- [ ] **Step 3: Sanity-run unit tests for the package**

Run: `go test ./cmd/mister-groovy-relay -run "TestAdapterLinker|TestAdapterSettings" -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(cmd): wire AdapterLinker into chassis.Config"
```

---

## Task 20: End-to-end test + full verification

**Files:**
- Create: `cmd/mister-groovy-relay/adapter_linker_e2e_test.go`

- [ ] **Step 1: Write the integration-tag e2e (deterministic, no network)**

Create `cmd/mister-groovy-relay/adapter_linker_e2e_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// Drives the chassis link routes through the production adapterLinker binding
// against a real registry, asserting the unlinked→view JSON contract end to
// end. Auth that needs a live Jellyfin/plex.tv server is out of scope here;
// this pins the wiring (route → requireSameOrigin → handler → binding →
// adapter.Snapshot) which is where 4E's integration risk lives.
func TestE2E_LinkStatusContract(t *testing.T) {
	reg := buildTestRegistry(t) // helper: registry with real plex+jellyfin adapters (data_dir = t.TempDir())
	s, err := chassis.New(chassis.Config{
		Bridge:        config.BridgeConfig{Mister: config.MisterConfig{Host: "127.0.0.1"}},
		AdapterLinker: newAdapterLinker(reg),
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/receiver/settings/adapter/jellyfin/link/status", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"kind":"credential"`) ||
		!strings.Contains(rec.Body.String(), `"phase":"unlinked"`) {
		t.Errorf("body = %s, want credential/unlinked view", rec.Body.String())
	}
	_ = context.Background
}
```

> `buildTestRegistry` builds the same adapter registry `main.go` builds, with `data_dir` pointed at `t.TempDir()`. Reuse the registry-construction helper if one exists in the package's tests; otherwise factor the registry build in `main.go` into a small helper `buildAdapterRegistry(cfg)` and call it here. A fuller variant can stand up an `httptest` Jellyfin server returning a canned `/Users/AuthenticateByName` body and assert the credential `start` reaches `linked` — add it if the harness supports it.

- [ ] **Step 2: Run the e2e**

Run: `go test -tags=integration ./cmd/mister-groovy-relay -run TestE2E_LinkStatusContract -v`
Expected: PASS.

- [ ] **Step 3: Full green gate (the four CI checks + isolation)**

Run each and confirm:

```bash
go vet ./...
go test ./...
go test -race ./...
go test -tags=integration ./...
go test ./internal/chassis -run TestProductionImports_NoCrossPackageCoupling -v
```

Expected: all PASS. The isolation test confirms `internal/chassis` still imports no adapter package (the binding lives in `cmd/`).

- [ ] **Step 4: Manual smoke (optional, with fake-mister or a real server)**

Build, run the bridge, open the Settings drawer → Adapters pane. Confirm: Plex shows "Link Plex Account"; clicking renders a PIN + countdown that polls; Jellyfin shows the credential form (or "set a Server URL" when blank); a successful link collapses to "✓ Linked"; Unlink returns to unlinked. Legacy `/ui/adapter/plex` + `/ui/adapter/jellyfin` still work unchanged.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_linker_e2e_test.go
git commit -m "test(cmd): integration e2e for chassis link-status contract"
```

---

## Self-Review

**Spec coverage** — every spec section maps to a task:

| Spec section | Task(s) |
|---|---|
| Goal 1/2 (Plex/Jellyfin sections render, scope chips) | 10, 14 |
| Goal 3 (stubs deleted) | 14 |
| Goal 4 (shared sub-section, collapsed-linked identical, `✓ Linked`/`as`) | 13, 14 |
| Goal 5 (Plex PIN flow e2e; no identity lookup) | 2,3,4,16,17 |
| Goal 6 (Jellyfin credential flow; NeedsServerURL hint) | 5,6,7,13,16 |
| Goal 7 (unlink) | 3,6,11,16 |
| Goal 8 (config saves unchanged) | rides 4D; covered by 10 |
| Goal 9 (chassis isolation; binding in cmd/) | 8,18,20 |
| Goal 10 (`/ui/*` unchanged) | 4,7 regression tests |
| Adapter-side API (LinkController/LinkSnapshot; ctx; disk-hydrated JF Snapshot) | 1,2,3,5,6 |
| Chassis contract (AdapterLinker/LinkView/Fields) | 8 |
| Wire contract (routes, envelopes, 503/404/400, timeouts, single-flight, error table) | 11, 12 |
| Rendering (placement C, Linkable guard, server-rendered initial) | 10, 13, 14 |
| JS (handlers, poll controller, safe repaint, no data-adapter on creds) | 16, 17 |
| server_url cascade (no special wiring) | documented; no task needed (rides 4D REBOOT + JF Start reconcile) |
| ApplyScope positioning (link = action, no scope) | 11 (views carry no scope) |
| Testing surface | 4,7,10,11,12,13,16,17,18,20 |

No spec requirement is left without a task.

**Placeholder scan:** no "TBD"/"implement later". The "> Note" callouts point the engineer at a concrete existing helper to match (test fixtures, template-parse glob, behavior-test runner) rather than leaving logic unspecified — each names the exact `grep` to resolve it.

**Type consistency:** the contract method names are consistent end to end — adapters expose `Snapshot`/`StartLink`/`PollLink`/`Unlink` (Tasks 1,3,6); the chassis interface uses `LinkView`/`StartLink`/`LinkStatus`/`Unlink` (Task 8); the binding maps between them (Task 18). `LinkView` JSON keys (`kind`,`phase`,`linkedAs`,`code`,`expiresInSec`,`needsServerURL`,`error`,`fields`) match between the Go struct (Task 8), the JS renderer (Task 16), and the poll controller (Task 17). `data-link-action`/`data-link-field`/`data-link-submit`/`data-adapter-section` are used consistently in the template (Task 13) and JS (Tasks 16,17).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-29-receiver-chassis-plex-jellyfin-adapters.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
