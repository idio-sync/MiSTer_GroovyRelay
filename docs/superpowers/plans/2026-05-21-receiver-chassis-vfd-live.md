# Receiver Chassis VFD Live (Phase 1 / Spec 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Import hygiene:** When snippets show imports for an existing Go file, merge the new names into the existing import block and run `gofmt`; do not create a second `import` declaration.

**Goal:** Wire the chassis VFD to real bridge session state via an SSE stream at `GET /receiver/events`. Server reads `core.Manager.StatusHomeView()`, emits `state` + `vfd` JSON events; the client (`vfd-live.js`) toggles `body.receiver.<state>` and updates VFD `data-vfd-*` spans in place. Phase 0's idle preview becomes a session-aware live display without changing chassis CSS.

**Architecture:** New package files `internal/chassis/{events.go, session.go}` define a narrow `SessionViewer` interface (`*core.Manager` satisfies it structurally via `StatusHomeView()`) plus the SSE handler. A per-server snapshot cache decouples 250 ms tick cadence from connected-tab fan-out so `core.Manager.mu` is acquired at constant 4 Hz regardless of viewers. Client-side adds `vfd-live.js`, a ~70-line vanilla ES2022 `EventSource` subscriber that writes into Phase 0's `seg-text` overlay spans via `data-vfd-*` hooks.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `encoding/json`, `text/template` already in chassis, `html/template`, `embed`, `sync`, `context`), zero new dependencies. Browser-side `EventSource` (Server-Sent Events; widely supported in every chassis target browser).

**Spec:** [docs/superpowers/specs/2026-05-21-receiver-chassis-vfd-live-design.md](../specs/2026-05-21-receiver-chassis-vfd-live-design.md).

**Phase 0 plan reference (format model):** [docs/superpowers/plans/2026-05-21-receiver-chassis-foundation.md](2026-05-21-receiver-chassis-foundation.md).

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/chassis/session.go` | `SessionViewer` interface + `snapshotFromSession(cfg, sv, now)` mapper |
| `internal/chassis/events.go` | `handleEvents` SSE handler, `emit()` helper, envelope structs, `vfdChanged` helper, `snapshotCache` |
| `internal/chassis/events_test.go` | Layer 1 tests for handler + cache + emit/diff helpers |
| `internal/chassis/static/vfd-live.js` | EventSource subscriber + DOM-write hooks |

**Files modified:**

| Path | Change |
|---|---|
| `internal/chassis/server.go` | Add `Session SessionViewer` field to `Config`; store on `Server`; seed snapshot cache synchronously in `New`; start snapshot-cache refresher in `Mount`; add `Close()`; mount `/receiver/events` route |
| `internal/chassis/data.go` | `VFDData` gains a `Uptime` change-tracked already; no struct churn. Extract `idleSnapshot()` body — unchanged behavior, but exposed so `snapshotFromSession()` can layer over it |
| `internal/chassis/handler.go` | `handleIndex` calls `snapshotFromSession(s.cfg, s.session, time.Now())` instead of `idleSnapshot` |
| `internal/chassis/templates/vfd.html` | Add `data-vfd-title`, `data-vfd-marquee`, `data-vfd-queue`, `data-vfd-uptime` on the `seg-text` spans only (overlay `seg-ghost` siblings untouched) |
| `internal/chassis/templates/shell.html` | Add `<script defer src="/receiver/static/vfd-live.js?v={{.Version}}">` after the existing chassis.js script tag |
| `internal/chassis/chassis_test.go` | Add `TestVfdTemplate_RendersDataAttributeHooks` Layer 2 guard |
| `cmd/mister-groovy-relay/main.go` | Pass `Session: coreMgr` into `chassis.Config`; defer `chassisSrv.Close()` |
| `tests/integration/chassis_test.go` | Add `TestReceiverEvents_EndToEnd` + `TestReceiverEvents_DoesNotShadowUIRoutes` |

**Files unchanged:** All existing `internal/ui/*`, `internal/uiserver/*`, `internal/core/*`. The `internal/chassis/static/chassis.js` runtime (Phase 0) is also unchanged — `vfd-live.js` is a sibling script.

---

## Task 1: SessionViewer Interface + Config.Session Field

**Files:**
- Create: `internal/chassis/session.go`
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test for SessionViewer satisfaction**

Append to `internal/chassis/chassis_test.go`:

```go
func TestSessionViewer_StatusHomeViewSatisfiesInterface(t *testing.T) {
	t.Parallel()
	// Compile-time and runtime assertion that *core.Manager satisfies
	// the chassis SessionViewer interface. Catches regressions where
	// core.Manager.StatusHomeView() changes signature without the
	// chassis side noticing.
	var _ SessionViewer = (*core.Manager)(nil)

	cfg := nonZeroConfig()
	cfg.Session = cfg.Manager
	if cfg.Session == nil {
		t.Fatal("expected non-nil Session after assignment from Manager")
	}
	view := cfg.Session.StatusHomeView()
	// Smoke-only: the result is a value type so any field read works.
	_ = view.State
}
```

Add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"` to the test file imports if not already present.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestSessionViewer_StatusHomeViewSatisfiesInterface`
Expected: FAIL — undefined `SessionViewer`, undefined `Config.Session` field.

- [ ] **Step 3: Create `session.go` with the SessionViewer interface**

Create `internal/chassis/session.go`:

```go
package chassis

import (
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// SessionViewer is the narrow read-only view of bridge session state
// the chassis needs. *core.Manager satisfies this structurally via its
// StatusHomeView() method. Tests inject fakes; production wires
// *core.Manager. Mirrors internal/ui.StatusViewer.
//
// Phase 1 / Spec 2 consumes only StatusHomeView(). Spec 3 (transport
// controls) will extend this interface with Pause / Play / Stop / SeekTo
// or introduce a sibling SessionController interface — to be decided
// in that spec's review.
type SessionViewer interface {
	StatusHomeView() core.StatusHomeView
}
```

- [ ] **Step 4: Add `Session` field to `chassis.Config`**

Edit `internal/chassis/server.go` Config struct — replace the existing definition:

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
	// rendering and SSE events. Optional: when nil, the chassis renders
	// idle-only and the /receiver/events stream emits the initial idle
	// snapshot then sits silent. *core.Manager satisfies the interface
	// structurally; main.go wires that.
	Session SessionViewer
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestSessionViewer_StatusHomeViewSatisfiesInterface`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/session.go internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): add SessionViewer interface + Config.Session field

Phase 1 / Spec 2 task 1. New session.go defines the narrow read-only
session-state interface the chassis consumes. *core.Manager satisfies
it structurally via StatusHomeView(). Config gains a Session field;
nil is legal (idle-only mode). Subsequent tasks wire it into
snapshotFromSession and the SSE handler.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: snapshotFromSession (nil-session fallback)

**Files:**
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test for nil-session fallback**

Append to `internal/chassis/chassis_test.go`:

```go
func TestSnapshotFromSession_NilSessionFallsBackToIdle(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Session = nil

	got := snapshotFromSession(cfg, nil, fixedNow)
	want := idleSnapshot(cfg, fixedNow)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nil Session should match idleSnapshot exactly; got %+v\nwant %+v", got, want)
	}
}
```

(Add `"reflect"` to the test imports if not already present.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession_NilSessionFallsBackToIdle`
Expected: FAIL — undefined `snapshotFromSession`.

- [ ] **Step 3: Implement `snapshotFromSession` (nil path)**

Edit `internal/chassis/session.go`. Replace the existing imports block with the final form (single block at the top):

```go
import (
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)
```

Append the function after the existing `SessionViewer` interface:

```go
// snapshotFromSession builds the page-render data. When sv is nil the
// chassis renders idle-only (offline-friendly + test-friendly).
// Subsequent tasks add live-state mapping; this first implementation
// is the fallback path so handleIndex can be re-wired immediately
// without breaking Phase 0 behaviour.
func snapshotFromSession(cfg Config, sv SessionViewer, now time.Time) ReceiverPageData {
	if sv == nil {
		return idleSnapshot(cfg, now)
	}
	// Live-state mapping lands in Task 3.
	return idleSnapshot(cfg, now)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession_NilSessionFallsBackToIdle`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/session.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): snapshotFromSession returns idle when Session is nil

Phase 1 / Spec 2 task 2. First implementation of the snapshot helper:
nil Session falls back to idleSnapshot. Live-state mapping lands in
the next task. Idle-only mode is the test-friendly default plus an
offline-friendly production fallback.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: snapshotFromSession (live-state mapping)

**Files:**
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing tests for live-state mapping**

Append to `internal/chassis/chassis_test.go`:

```go
// fakeSessionViewer is the test double for SessionViewer. Lets tests
// drive the chassis from a known StatusHomeView without spinning up a
// real *core.Manager (which requires a full bridge graph).
type fakeSessionViewer struct {
	view core.StatusHomeView
}

func (f *fakeSessionViewer) StatusHomeView() core.StatusHomeView { return f.view }

func TestSnapshotFromSession_LiveStateOverridesIdleDefaults(t *testing.T) {
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
	got := snapshotFromSession(cfg, sv, fixedNow)

	if got.State != StateLive {
		t.Errorf("State = %q, want %q", got.State, StateLive)
	}
	if got.VFD.Title != "First Day on MTV" {
		t.Errorf("VFD.Title = %q, want First Day on MTV", got.VFD.Title)
	}
	if got.VFD.Marquee != "PLEX · 04:23 / 09:56" {
		t.Errorf("VFD.Marquee = %q, want PLEX · 04:23 / 09:56", got.VFD.Marquee)
	}
	if got.VFD.State != string(StateLive) {
		t.Errorf("VFD.State = %q, want %q (mirrors top-level State)", got.VFD.State, StateLive)
	}
}

func TestSnapshotFromSession_PausedMapsToLive(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePaused, Title: "Take On Me", Source: "plex",
	}}
	got := snapshotFromSession(cfg, sv, fixedNow)

	// core.StatePaused -> chassis "live" so the body stays bright
	// during transport pause. The transport-row pause indicator is
	// Spec 3's job; chassis state class should not flicker.
	if got.State != StateLive {
		t.Errorf("paused -> chassis State = %q, want %q", got.State, StateLive)
	}
}

func TestSnapshotFromSession_IdleStateMatchesIdleSnapshot(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	got := snapshotFromSession(cfg, sv, fixedNow)
	want := idleSnapshot(cfg, fixedNow)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("idle-from-session should match idleSnapshot exactly")
	}
}

func TestSnapshotFromSession_MapsStatusHomeViewToVFDData(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:    core.StatePlaying,
		Title:    "TEST",
		Source:   "jellyfin",
		Position: 30 * time.Second,
		Duration: 3 * time.Minute,
	}}
	got := snapshotFromSession(cfg, sv, fixedNow)

	if got.VFD.Marquee != "JELLYFIN · 00:30 / 03:00" {
		t.Errorf("VFD.Marquee = %q, want JELLYFIN · 00:30 / 03:00", got.VFD.Marquee)
	}
	if got.VFD.QueueCurrent != 0 || got.VFD.QueueTotal != 0 {
		t.Errorf("queue should be 0/0 placeholder in Phase 1; got %d/%d",
			got.VFD.QueueCurrent, got.VFD.QueueTotal)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession`
Expected: FAIL — live-state mapping not implemented (returns idleSnapshot for all).

- [ ] **Step 3: Implement the live-state mapper per spec formatting rules**

Replace the body of `snapshotFromSession` in `internal/chassis/session.go`. Imports must be a single block at the top of the file:

```go
import (
	"fmt"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// snapshotFromSession builds the page-render data from current bridge
// session state. When sv is nil OR the bridge is idle, falls back to
// idleSnapshot. When live (playing or paused), overrides VFD title +
// marquee + State; queue stays 0/0 placeholder until a later spec
// surfaces real queue data on StatusHomeView.
//
// State mapping (per spec):
//   core.StateIdle    -> chassis StateIdle ("idle")
//   core.StatePlaying -> chassis StateLive ("live")
//   core.StatePaused  -> chassis StateLive ("live")
//
// Paused maps to live so the chassis stays bright during transport
// pause; the transport-row controls (Spec 3) own pause indication.
func snapshotFromSession(cfg Config, sv SessionViewer, now time.Time) ReceiverPageData {
	if sv == nil {
		return idleSnapshot(cfg, now)
	}
	view := sv.StatusHomeView()
	if view.State == core.StateIdle {
		return idleSnapshot(cfg, now)
	}

	base := idleSnapshot(cfg, now)
	base.State = StateLive
	base.VFD.State = string(StateLive)
	base.VFD.Title = view.Title
	base.VFD.Marquee = formatLiveMarquee(view)
	// QueueCurrent / QueueTotal stay 0/0 (Phase 1 placeholder).
	// SystemTime + Uptime are computed in idleSnapshot from `now` and
	// cfg.StartedAt; they remain valid in live state.
	return base
}

// formatLiveMarquee composes the VFD marquee string for live state per
// spec §"Field mapping": "<UPPER(Source)> · <position> / <duration>".
// Examples:
//   PLEX, 04:23, 09:56  → "PLEX · 04:23 / 09:56"
//   PLEX, 04:23, 0      → "PLEX · 04:23 / --:--"   (unknown duration)
//   plex, 0,      0     → "PLEX · 00:00 / --:--"   (start of unknown stream)
//   "",   0,      0     → "BRIDGE · 00:00 / --:--" (empty source fallback)
//   1h4m5s position with same duration → "PLEX · 1:04:05 / 1:04:05"
//
// Source fallback is "BRIDGE" (NOT "PLAYING") per spec §"Field mapping".
func formatLiveMarquee(view core.StatusHomeView) string {
	src := strings.ToUpper(view.Source)
	if src == "" {
		src = "BRIDGE"
	}
	return fmt.Sprintf("%s · %s / %s", src,
		formatPlaybackPosition(view.Position),
		formatPlaybackDuration(view.Duration))
}

// formatPlaybackPosition renders the current position. Negative durations
// clamp to "00:00"; non-negative durations truncate to whole seconds.
// Below one hour → "MM:SS"; >= one hour → "H:MM:SS" (single-digit hours
// for < 10h; multi-digit hours expand naturally via %d).
func formatPlaybackPosition(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return formatPlaybackClock(d)
}

// formatPlaybackDuration renders the total duration. d <= 0 means
// "unknown" per spec §"Field mapping" and renders as "--:--"; positive
// durations use the same MM:SS / H:MM:SS formatting as position.
func formatPlaybackDuration(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	return formatPlaybackClock(d)
}

// formatPlaybackClock is the shared MM:SS / H:MM:SS formatter. d is
// assumed non-negative — callers clamp.
func formatPlaybackClock(d time.Duration) string {
	total := int(d / time.Second)
	hours := total / 3600
	minutes := (total / 60) % 60
	seconds := total % 60
	if hours > 0 {
		// Single-digit hours per spec example ("1:04:05"). %d (not %02d)
		// for the hours component; minutes/seconds always two digits.
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}
```

Also add an extra test case to lock in the unknown-duration and hour-formatting paths. Append to the `TestSnapshotFromSession_MapsStatusHomeViewToVFDData` test block (or to a new test), and update the `LiveStateOverridesIdleDefaults` expectation if the position-only path is involved.

Add this new test:

```go
func TestFormatLiveMarquee_HandlesUnknownDurationAndHours(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		view core.StatusHomeView
		want string
	}{
		{
			name: "unknown duration",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "plex",
				Position: 4*time.Minute + 23*time.Second},
			want: "PLEX · 04:23 / --:--",
		},
		{
			name: "zero position unknown duration",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "plex"},
			want: "PLEX · 00:00 / --:--",
		},
		{
			name: "empty source fallback",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "",
				Position: 30 * time.Second, Duration: 3 * time.Minute},
			want: "BRIDGE · 00:30 / 03:00",
		},
		{
			name: "hour-long position single-digit hours",
			view: core.StatusHomeView{State: core.StatePlaying, Source: "plex",
				Position: time.Hour + 4*time.Minute + 5*time.Second,
				Duration: time.Hour + 30*time.Minute},
			want: "PLEX · 1:04:05 / 1:30:00",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLiveMarquee(tc.view)
			if got != tc.want {
				t.Errorf("formatLiveMarquee = %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession`
Expected: PASS — all four `TestSnapshotFromSession_*` cases green.

- [ ] **Step 5: Run the full chassis test suite to confirm no regression**

Run: `go test ./internal/chassis/...`
Expected: PASS — everything from Phase 0 + tasks 1-2 + the new mapping tests.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/session.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): snapshotFromSession maps StatusHomeView to live VFD

Phase 1 / Spec 2 task 3. Live-state path implemented: reads
StatusHomeView, maps core.StatePlaying and core.StatePaused to chassis
"live" (paused stays bright; transport-row owns pause display in
Spec 3), formats the marquee from Source + Position/Duration. Queue
stays 0/0 placeholder until a later spec surfaces queue data.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Wire handleIndex to use snapshotFromSession

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/handler.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write a failing test for handleIndex live rendering**

Append to `internal/chassis/chassis_test.go`:

```go
func TestHandleIndex_RendersLiveStateFromSession(t *testing.T) {
	t.Parallel()
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:    core.StatePlaying,
		Title:    "Burning Down the House",
		Source:   "plex",
		Position: 8 * time.Second,
		Duration: 4*time.Minute + 1*time.Second,
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="receiver live"`) {
		t.Errorf("body missing live body class: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "Burning Down the House") {
		t.Errorf("body missing live title")
	}
	if !strings.Contains(body, "PLEX · 00:08 / 04:01") {
		t.Errorf("body missing live marquee")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestHandleIndex_RendersLiveStateFromSession`
Expected: FAIL — `Server.session` field doesn't exist; handleIndex still calls `idleSnapshot` directly.

- [ ] **Step 3: Add `session` field to Server**

Edit `internal/chassis/server.go` Server struct:

```go
// Server owns the chassis runtime state.
type Server struct {
	cfg      Config
	session  SessionViewer
	tmpl     *template.Template
	cssBytes []byte // chassis.css with {{.Version}} substituted, cached
}
```

And update the `New` constructor to populate it:

```go
return &Server{
	cfg:      cfg,
	session:  cfg.Session,
	tmpl:     tmpl,
	cssBytes: cssBytes,
}, nil
```

- [ ] **Step 4: Rewrite handleIndex to call snapshotFromSession**

Replace the body of `handleIndex` in `internal/chassis/handler.go`:

```go
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data := snapshotFromSession(s.cfg, s.session, time.Now())
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "shell.html", data); err != nil {
		http.Error(w, "template execute failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/chassis/...`
Expected: PASS — `TestHandleIndex_RendersLiveStateFromSession` plus all prior tests.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/server.go internal/chassis/handler.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleIndex reads live state via snapshotFromSession

Phase 1 / Spec 2 task 4. Server now stores cfg.Session as s.session;
handleIndex calls snapshotFromSession instead of idleSnapshot. With
Session=nil (default for now until main.go is wired in task 18) the
behaviour is unchanged — idle preview as before. A live SessionViewer
test exercises the new path end-to-end through the template.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: emit() helper + envelope structs with JSON tags

**Files:**
- Create: `internal/chassis/events.go`
- Create: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test for the emit helper**

Create `internal/chassis/events_test.go`:

```go
package chassis

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmit_FormatsValidSSERecord(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := emit(&buf, "state", stateEnvelope{State: "idle"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := buf.String()
	want := "event: state\ndata: {\"state\":\"idle\"}\n\n"
	if got != want {
		t.Errorf("emit produced:\n%q\nwant:\n%q", got, want)
	}
}

func TestEmit_VfdEnvelopeUsesCamelCaseFieldNames(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := emit(&buf, "vfd", vfdEnvelope{
		Title:        "STANDBY",
		Marquee:      "MISTER LINK OK",
		QueueCurrent: 0,
		QueueTotal:   0,
		Uptime:       "4H 12M",
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := buf.String()
	for _, want := range []string{`"title":"STANDBY"`, `"marquee":"MISTER LINK OK"`,
		`"queueCurrent":0`, `"queueTotal":0`, `"uptime":"4H 12M"`} {
		if !strings.Contains(got, want) {
			t.Errorf("emit output missing %q\nfull output:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestEmit'`
Expected: FAIL — undefined `emit`, `stateEnvelope`, `vfdEnvelope`.

- [ ] **Step 3: Create `events.go` with envelope structs + emit helper**

Create `internal/chassis/events.go`:

```go
package chassis

import (
	"encoding/json"
	"fmt"
	"io"
)

// stateEnvelope is the payload for the `state` SSE event. Explicit
// struct tag because Go's default JSON encoder emits PascalCase
// ("State") and the wire format mandates camelCase ("state").
type stateEnvelope struct {
	State string `json:"state"`
}

// vfdEnvelope is the payload for the `vfd` SSE event. Carries the
// minimal Phase 1 fields the client needs to update the VFD spans;
// Spec 5 (telemetry) and later specs add their own envelope types
// for other event names.
type vfdEnvelope struct {
	Title        string `json:"title"`
	Marquee      string `json:"marquee"`
	QueueCurrent int    `json:"queueCurrent"`
	QueueTotal   int    `json:"queueTotal"`
	Uptime       string `json:"uptime"`
}

// vfdEnvelopeFrom flattens a VFDData into the wire-format envelope.
// Kept separate from vfdEnvelope's definition so the envelope is a
// pure data type and the conversion lives next to its caller in
// handleEvents.
func vfdEnvelopeFrom(v VFDData) vfdEnvelope {
	return vfdEnvelope{
		Title:        v.Title,
		Marquee:      v.Marquee,
		QueueCurrent: v.QueueCurrent,
		QueueTotal:   v.QueueTotal,
		Uptime:       v.Uptime,
	}
}

// emit writes one SSE record (event line + data line + terminating
// blank line). Returns the underlying writer error so callers can
// detect mid-write client disconnects and bail cleanly.
func emit(w io.Writer, name string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestEmit'`
Expected: PASS — both envelope tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): SSE envelope structs + emit helper

Phase 1 / Spec 2 task 5. New events.go with stateEnvelope, vfdEnvelope
(both using explicit json struct tags so the wire format is camelCase
regardless of Go field names), vfdEnvelopeFrom flattener, and the emit
helper that writes one SSE record per call. Returns writer error so
mid-write disconnects bubble up to the handler.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: vfdChanged helper

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing tests for vfdChanged**

Append to `internal/chassis/events_test.go`:

```go
func TestVfdChanged_DetectsEveryFieldDelta(t *testing.T) {
	t.Parallel()
	base := VFDData{
		Title:        "STANDBY",
		Marquee:      "hint",
		QueueCurrent: 0,
		QueueTotal:   0,
		Uptime:       "0H 0M",
	}

	tests := []struct {
		name  string
		mutate func(*VFDData)
	}{
		{"title", func(v *VFDData) { v.Title = "Live Title" }},
		{"marquee", func(v *VFDData) { v.Marquee = "PLEX · 00:00 / 03:00" }},
		{"queueCurrent", func(v *VFDData) { v.QueueCurrent = 1 }},
		{"queueTotal", func(v *VFDData) { v.QueueTotal = 12 }},
		{"uptime", func(v *VFDData) { v.Uptime = "0H 1M" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			tt.mutate(&next)
			if !vfdChanged(base, next) {
				t.Errorf("vfdChanged should return true when %s changes", tt.name)
			}
		})
	}
}

func TestVfdChanged_IgnoresSystemTimeAndState(t *testing.T) {
	t.Parallel()
	base := VFDData{Title: "X", State: "idle", SystemTime: "10:30"}
	next := base
	next.SystemTime = "10:31"
	next.State = "live" // duplicate of ReceiverPageData.State; handled separately
	if vfdChanged(base, next) {
		t.Errorf("vfdChanged should ignore SystemTime and VFDData.State changes")
	}
}

func TestVfdChanged_IdenticalReturnsFalse(t *testing.T) {
	t.Parallel()
	v := VFDData{Title: "X", Marquee: "Y", QueueCurrent: 3, QueueTotal: 12, Uptime: "1H 2M"}
	if vfdChanged(v, v) {
		t.Errorf("vfdChanged should be false for identical inputs")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestVfdChanged`
Expected: FAIL — undefined `vfdChanged`.

- [ ] **Step 3: Implement `vfdChanged` in events.go**

Append to `internal/chassis/events.go`:

```go
// vfdChanged enumerates exactly the fields that participate in the
// `vfd` event payload. Other fields on VFDData (notably SystemTime,
// which is client-ticker-driven via [data-system-time], and the
// duplicated State that mirrors ReceiverPageData.State and is handled
// by a separate `state` event) are deliberately excluded.
//
// Explicit field-level compare beats reflect.DeepEqual for speed
// (no reflection) and clarity (the function definition IS the spec
// of which fields are part of the Phase 1 VFD wire-format surface).
func vfdChanged(a, b VFDData) bool {
	return a.Title != b.Title ||
		a.Marquee != b.Marquee ||
		a.QueueCurrent != b.QueueCurrent ||
		a.QueueTotal != b.QueueTotal ||
		a.Uptime != b.Uptime
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestVfdChanged`
Expected: PASS — all three subtests + the table-driven detection test.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): vfdChanged field-level diff helper

Phase 1 / Spec 2 task 6. Compares the five wire-format fields
(Title, Marquee, QueueCurrent, QueueTotal, Uptime) and deliberately
ignores SystemTime (client-driven) and VFDData.State (handled via the
separate `state` SSE event). Faster than reflect.DeepEqual and
self-documenting as the Phase 1 vfd-event surface.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: handleEvents scaffolding (headers, retry, initial snapshot, flusher, nil-session, context cancel)

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

This task lands the SSE handler's *static* surface — everything before the diff ticker loop. The next two tasks add the loop.

- [ ] **Step 1: Write the failing tests for the static handler surface**

Extend the existing import block in `internal/chassis/events_test.go` to include the new imports (do NOT add a second `import` declaration):

```go
import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)
```

Append these test fixtures and tests to `internal/chassis/events_test.go`:

```go
// flushRecorder is httptest.ResponseRecorder + a Flusher implementation
// so SSE handlers can call w.(http.Flusher).Flush() without panic.
// Tracks flushes for assertion.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
	mu      sync.Mutex
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (f *flushRecorder) Flush() {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
}

// nonFlushableWriter implements http.ResponseWriter but deliberately
// does NOT satisfy http.Flusher. httptest.ResponseRecorder satisfies
// Flusher in Go 1.20+, so we hand-roll a minimal writer to drive the
// 500-on-non-flushable branch of handleEvents.
type nonFlushableWriter struct {
	headers http.Header
	body    bytes.Buffer
	status  int
}

func (n *nonFlushableWriter) Header() http.Header        { return n.headers }
func (n *nonFlushableWriter) Write(b []byte) (int, error) { return n.body.Write(b) }
func (n *nonFlushableWriter) WriteHeader(s int)          { n.status = s }

func TestHandleEvents_RejectsNonFlushableResponseWriter(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := &nonFlushableWriter{headers: http.Header{}}
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil)
	s.handleEvents(w, req)
	if w.status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.status)
	}
}

func TestHandleEvents_SetsCorrectHeaders(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	headers := w.Result().Header
	if got := headers.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := headers.Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
		t.Errorf("Cache-Control = %q, want no-cache, no-store, must-revalidate", got)
	}
	if got := headers.Get("Connection"); got != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", got)
	}
	if got := headers.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestHandleEvents_EmitsRetryDirectiveOnConnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.HasPrefix(body, "retry: 3000\n\n") {
		t.Errorf("body should start with retry: 3000 directive; got:\n%s",
			body[:min(120, len(body))])
	}
}

func TestHandleEvents_EmitsInitialSnapshotOnConnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: state\n") {
		t.Errorf("body missing initial state event:\n%s", body)
	}
	if !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("body missing idle state payload")
	}
	if !strings.Contains(body, "event: vfd\n") {
		t.Errorf("body missing initial vfd event")
	}
	if !strings.Contains(body, `"title":"STANDBY"`) {
		t.Errorf("body missing STANDBY title payload")
	}
}

func TestHandleEvents_NilSessionStreamsIdleOnly(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.session != nil {
		t.Fatalf("expected nil session by default (nonZeroConfig does not set Session); got %v", s.session)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("nil session should still emit initial idle snapshot; body:\n%s", body)
	}
}

func TestHandleEvents_TerminatesOnClientDisconnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.handleEvents(w, req)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// handler returned
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleEvents did not return within 200ms of context cancel")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestHandleEvents`
Expected: FAIL — `handleEvents` undefined.

- [ ] **Step 3: Implement the static surface of handleEvents**

Extend the existing `internal/chassis/events.go` import block from Task 5 with `net/http` and `time` (the file already imports `io`, which `handleEvents` also uses), then append only the handler below the existing helpers:

```go
import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// handleEvents serves a long-lived SSE stream at GET /receiver/events.
// Scaffolding only — the diff ticker that emits change events lands
// in the next task. This implementation handles:
//   - 500 when the ResponseWriter cannot flush
//   - SSE response headers
//   - retry: 3000 directive (pins browser reconnect cadence)
//   - initial state + vfd snapshot
//   - clean termination on r.Context().Done()
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return
	}

	last := snapshotFromSession(s.cfg, s.session, time.Now())
	if err := emit(w, "state", stateEnvelope{State: string(last.State)}); err != nil {
		return
	}
	if err := emit(w, "vfd", vfdEnvelopeFrom(last.VFD)); err != nil {
		return
	}
	flusher.Flush()

	// Diff loop lands in Task 8. For now block until disconnect so the
	// stream stays open per the handler contract.
	<-r.Context().Done()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestHandleEvents`
Expected: PASS — all 6 static-surface tests.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleEvents static surface (headers + initial snapshot)

Phase 1 / Spec 2 task 7. SSE handler scaffolding: 500 on non-Flusher
writers, four required SSE headers, retry: 3000 directive on first
write, initial state + vfd snapshot from snapshotFromSession, clean
context-cancel termination. Diff-ticker emission lands in tasks 8-9.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: handleEvents diff ticker emits state events on transition

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test for state transition events**

Append to `internal/chassis/events_test.go`:

```go
// mutableSessionViewer flips between idle and live snapshots on
// demand. Lets tests drive state transitions through handleEvents
// without spinning up a real bridge.
type mutableSessionViewer struct {
	mu   sync.Mutex
	view core.StatusHomeView
}

func (m *mutableSessionViewer) StatusHomeView() core.StatusHomeView {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.view
}

func (m *mutableSessionViewer) set(view core.StatusHomeView) {
	m.mu.Lock()
	m.view = view
	m.mu.Unlock()
}

func TestHandleEvents_EmitsStateEventOnTransition(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mount is called even though this test invokes handleEvents directly:
	// Task 13 makes Mount start the shared snapshot-cache refresher that
	// Task 14's handleEvents path reads. Task 13 also adds Close cleanup.
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	// Trigger a state transition shortly after the handler boots.
	go func() {
		time.Sleep(150 * time.Millisecond) // > one diff tick (100ms in tests)
		sv.set(core.StatusHomeView{
			State: core.StatePlaying, Title: "T", Source: "plex",
		})
		// Leave room for both the shared cache refresher and the per-handler
		// diff ticker to observe the mutation after Task 14.
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	// Initial snapshot: state:idle. Then after the mutation: state:live.
	if !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("missing initial idle state in body:\n%s", body)
	}
	if !strings.Contains(body, `"state":"live"`) {
		t.Errorf("missing transition-to-live state event in body:\n%s", body)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestHandleEvents_EmitsStateEventOnTransition`
Expected: FAIL — diff ticker not implemented; only the initial snapshot is emitted.

- [ ] **Step 3: Expose the tick interval as a package var for test override**

Append to `internal/chassis/events.go`:

```go
// chassisTickInterval is the diff-ticker cadence. Package-level var so
// tests can shorten it via TestMain / setter without changing
// production behaviour. Production default 250 ms; tests override to
// 100 ms (still slow enough to be reliable on busy CI workers).
var chassisTickInterval = 250 * time.Millisecond
```

Add a test-only override at the top of `events_test.go`:

```go
func init() {
	// Shorten diff ticker for tests so transition detection completes
	// quickly. This assignment happens before any tests run, so no test
	// mutates package-level timing vars at runtime (race-safe under
	// go test -race).
	chassisTickInterval = 100 * time.Millisecond
}
```

- [ ] **Step 4: Add the diff loop with state-event emission**

Replace the trailing `<-r.Context().Done()` block in `handleEvents` with:

```go
	tick := time.NewTicker(chassisTickInterval)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			curr := snapshotFromSession(s.cfg, s.session, time.Now())
			if curr.State != last.State {
				if err := emit(w, "state", stateEnvelope{State: string(curr.State)}); err != nil {
					return
				}
				last.State = curr.State
			}
			flusher.Flush()
		}
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestHandleEvents -v`
Expected: PASS — all 7 handler tests including the new state-transition test.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleEvents diff ticker emits state events

Phase 1 / Spec 2 task 8. The 250ms diff ticker is now alive; emits
`state` events when the chassis state (idle/live derived from
core.State per spec mapping) transitions. Tests override the tick
interval to 100ms for fast feedback. vfd-event emission lands next.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: handleEvents diff ticker emits vfd events on field changes

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test for vfd-event emission**

Append to `internal/chassis/events_test.go`:

```go
func TestHandleEvents_EmitsVfdEventOnTitleChange(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "First Track", Source: "plex",
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mount starts the shared snapshot-cache refresher once Task 13 lands;
	// before that it is harmless and keeps this test linearly valid.
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(150 * time.Millisecond)
		sv.set(core.StatusHomeView{
			State: core.StatePlaying, Title: "Second Track", Source: "plex",
		})
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"title":"First Track"`) {
		t.Errorf("missing initial title event:\n%s", body)
	}
	if !strings.Contains(body, `"title":"Second Track"`) {
		t.Errorf("missing title-change vfd event:\n%s", body)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestHandleEvents_EmitsVfdEventOnTitleChange`
Expected: FAIL — vfd-event emission not implemented; only state events fire after the initial snapshot.

- [ ] **Step 3: Add vfd-event emission to the diff loop**

Edit the `case <-tick.C:` branch in `handleEvents`:

```go
		case <-tick.C:
			curr := snapshotFromSession(s.cfg, s.session, time.Now())
			if curr.State != last.State {
				if err := emit(w, "state", stateEnvelope{State: string(curr.State)}); err != nil {
					return
				}
				last.State = curr.State
			}
			if vfdChanged(curr.VFD, last.VFD) {
				if err := emit(w, "vfd", vfdEnvelopeFrom(curr.VFD)); err != nil {
					return
				}
				last.VFD = curr.VFD
			}
			flusher.Flush()
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/chassis/ -run TestHandleEvents`
Expected: PASS — all 8 handler tests.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleEvents emits vfd events on field changes

Phase 1 / Spec 2 task 9. Diff loop now emits `vfd` events when any of
Title / Marquee / QueueCurrent / QueueTotal / Uptime changes (per
vfdChanged). Title-change test drives the new path; state events
continue working unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: handleEvents heartbeat comments

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test for heartbeat**

Append to `internal/chassis/events_test.go`:

```go
// chassisHeartbeatInterval is a package-level var for the same reason
// chassisTickInterval is — tests shorten it once during package init to
// keep the suite fast without runtime races.
//
// We assert heartbeat by leaving the handler running for 3x the
// (shortened) interval and counting `: heartbeat\n\n` occurrences.

func TestHandleEvents_EmitsHeartbeatComments(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(180 * time.Millisecond) // > 3x heartbeat
		cancel()
	}()
	s.handleEvents(w, req)

	count := strings.Count(w.Body.String(), ": heartbeat\n\n")
	if count < 2 {
		t.Errorf("expected at least 2 heartbeat comments after ~180ms with 50ms interval, got %d. body:\n%s",
			count, w.Body.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestHandleEvents_EmitsHeartbeatComments`
Expected: FAIL — heartbeat ticker not implemented.

- [ ] **Step 3: Add the heartbeat ticker**

In `internal/chassis/events.go`, add the package-level interval:

```go
// chassisHeartbeatInterval is the cadence at which the handler emits
// `: heartbeat` SSE comments to defeat reverse-proxy idle timeouts.
// Production default 30s; tests override.
var chassisHeartbeatInterval = 30 * time.Second
```

Update the existing `events_test.go` `init()` from Task 8 to also set the test heartbeat interval once before any tests run:

```go
func init() {
	chassisTickInterval = 100 * time.Millisecond
	chassisHeartbeatInterval = 50 * time.Millisecond
}
```

Add a heartbeat ticker to `handleEvents`. After the existing `tick := time.NewTicker(...)` line, add:

```go
	heartbeat := time.NewTicker(chassisHeartbeatInterval)
	defer heartbeat.Stop()
```

Add a new branch to the select:

```go
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/chassis/ -run TestHandleEvents`
Expected: PASS — all 9 handler tests.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleEvents emits periodic heartbeat comments

Phase 1 / Spec 2 task 10. Every 30s the handler writes ": heartbeat"
SSE comment + blank line to keep the connection alive through nginx
and similar reverse proxies that idle-time out long-lived requests.
Comments are ignored by EventSource clients. Tests override the
interval to 50ms for fast feedback.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: handleEvents bails on mid-write disconnect

**Files:**
- Modify: `internal/chassis/events_test.go`

The diff loop already returns on `emit` / `io.WriteString` error (added in tasks 7-10). This task adds an explicit test to lock the behaviour in.

- [ ] **Step 1: Write the failing test for mid-write disconnect bail**

Append to `internal/chassis/events_test.go`:

```go
// failingWriter implements http.ResponseWriter + http.Flusher but
// returns io.ErrClosedPipe from Write after the first N bytes,
// simulating a TCP RST while the handler is mid-stream.
type failingWriter struct {
	headers http.Header
	written int
	cutoff  int
	flushes int
}

func (f *failingWriter) Header() http.Header { return f.headers }
func (f *failingWriter) Write(b []byte) (int, error) {
	if f.written >= f.cutoff {
		return 0, io.ErrClosedPipe
	}
	f.written += len(b)
	return len(b), nil
}
func (f *failingWriter) WriteHeader(int) {}
func (f *failingWriter) Flush()          { f.flushes++ }

func TestHandleEvents_BailsOnMidWriteDisconnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &failingWriter{headers: http.Header{}, cutoff: 20} // fail after a few bytes
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.handleEvents(w, req)
		close(done)
	}()
	select {
	case <-done:
		// handler returned quickly on write error
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleEvents did not bail on mid-write disconnect within 200ms")
	}
}
```

Add `"io"` to test imports if not already present.

- [ ] **Step 2: Run the test to verify behaviour**

Run: `go test ./internal/chassis/ -run TestHandleEvents_BailsOnMidWriteDisconnect`
Expected: PASS — the handler already returns on write error (tasks 7-10 wired `if err := ...; err != nil { return }`). This test locks the behaviour in.

If it FAILs, the diff loop's error handling regressed; re-examine `events.go` and ensure every `emit` / `io.WriteString` return value is checked.

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
test(chassis): lock in mid-write disconnect bail behaviour

Phase 1 / Spec 2 task 11. The handler already returns on any emit /
WriteString error (tasks 7-10); this test makes the contract explicit
with a failingWriter that returns io.ErrClosedPipe after 20 bytes.
Future refactors that drop error checks fail loudly here.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Mount /receiver/events + concurrent connections

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test for concurrent connections**

Append to `internal/chassis/events_test.go`:

```go
func TestHandleEvents_MultipleConcurrentConnections(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux()) // starts the shared cache refresher after Task 13

	const n = 5
	bodies := make([]*flushRecorder, n)
	ctxs := make([]context.CancelFunc, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		ctx, cancel := context.WithCancel(context.Background())
		ctxs[i] = cancel
		bodies[i] = newFlushRecorder()
		req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleEvents(bodies[i], req)
		}()
	}

	// Drive a state transition; every connected handler should observe it.
	time.Sleep(150 * time.Millisecond)
	sv.set(core.StatusHomeView{State: core.StatePlaying, Title: "X", Source: "plex"})
	time.Sleep(350 * time.Millisecond)
	for _, cancel := range ctxs {
		cancel()
	}
	wg.Wait()

	for i, w := range bodies {
		body := w.Body.String()
		if !strings.Contains(body, `"state":"live"`) {
			t.Errorf("connection %d missed the live transition:\n%s", i, body)
		}
	}
}

func TestMount_RegistersEventsRoute(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /receiver/events status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestHandleEvents_MultipleConcurrent|TestMount_RegistersEvents'`
Expected: FAIL — `TestMount_RegistersEventsRoute` returns 404 because `/receiver/events` isn't mounted. The concurrent test may PASS already since the handler doesn't share state — verify and only proceed if both lights up.

- [ ] **Step 3: Mount the events route**

Edit `internal/chassis/server.go` `Mount`:

```go
// Mount registers chassis routes on mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /receiver", s.handleIndex)
	mux.HandleFunc("GET /receiver/{$}", s.handleIndex)
	mux.HandleFunc("GET /receiver/static/", s.handleStatic)
	mux.HandleFunc("GET /receiver/events", s.handleEvents)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/chassis/...`
Expected: PASS — all handler + mount tests including concurrent.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): mount /receiver/events + concurrent-connection test

Phase 1 / Spec 2 task 12. Mount registers the SSE route. Concurrent
connection test (5 simultaneous EventSource goroutines against the
shared Server) confirms each observes the same state transition,
exposing accidental shared mutation in CI before production.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Snapshot cache + Close() lifecycle (New seeds; Mount starts refresher)

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

The cache lives on the `Server` so a single background goroutine fans `StatusHomeView()` results out to N connected handlers. **Per spec §"Locking and concurrency":** `New` seeds the cache synchronously and does **not** start a goroutine (so unmounted servers + Phase 0 tests don't leak background work). `Mount` starts the refresher exactly once via `sync.Once`. `Close()` cancels via a `context.CancelFunc` and waits on a `cacheDone` channel.

- [ ] **Step 1: Write the failing tests for cache + lifecycle**

Append to `internal/chassis/events_test.go`:

```go
// countingViewer wraps a SessionViewer and counts StatusHomeView calls.
// Used by snapshot-cache tests to assert call cadence.
type countingViewer struct {
	mu    sync.Mutex
	calls int
	view  core.StatusHomeView
}

func (c *countingViewer) StatusHomeView() core.StatusHomeView {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.view
}

func (c *countingViewer) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestSnapshotCache_SeedsSynchronouslyBeforeFirstSSE(t *testing.T) {
	t.Parallel()
	// A New-only server (no Mount) should already have a valid cached
	// snapshot reflecting the live SessionViewer state — proving the
	// seed call in New happens synchronously and the first SSE
	// connection cannot emit a zero-value or stale "vfd" frame.
	cv := &countingViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "Live Title", Source: "plex",
	}}
	cfg := nonZeroConfig()
	cfg.Session = cv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Do NOT call Mount; we're proving the seed is synchronous in New.
	if cv.Calls() < 1 {
		t.Fatalf("StatusHomeView not called during New; want >= 1 (synchronous seed)")
	}
	snap := s.cache.Get()
	if snap.State != StateLive {
		t.Errorf("cached snapshot State = %q, want %q (the live SessionViewer)", snap.State, StateLive)
	}
	if snap.VFD.Title != "Live Title" {
		t.Errorf("cached snapshot VFD.Title = %q, want %q", snap.VFD.Title, "Live Title")
	}
}

func TestServerClose_StopsSnapshotCacheRefresher(t *testing.T) {
	t.Parallel()
	cv := &countingViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = cv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	// Let the refresher tick a few times.
	time.Sleep(350 * time.Millisecond)
	preClose := cv.Calls()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Subsequent ticks (if the goroutine hadn't actually stopped) would
	// increment the call counter; pin down by sleeping > 3 intervals.
	time.Sleep(400 * time.Millisecond)
	postClose := cv.Calls()

	// Allow at most one in-flight tick to land between Close and the
	// goroutine actually returning.
	if delta := postClose - preClose; delta > 1 {
		t.Errorf("StatusHomeView calls after Close: pre=%d post=%d (delta %d); refresher did not stop", preClose, postClose, delta)
	}

	// Idempotence: calling Close twice must not panic or block.
	if err := s.Close(); err != nil {
		t.Errorf("second Close returned error: %v (want nil; Close must be idempotent)", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestSnapshotCache|TestServerClose'`
Expected: FAIL — `snapshotCache`, `s.cache`, `s.Mount` (with refresher start), and `s.Close()` don't exist yet.

- [ ] **Step 3: Add the snapshotCache type to events.go**

Append to `internal/chassis/events.go`. Extend the existing `import` block at the top to include `"sync"` (do not add a second `import` declaration):

```go
import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)
```

Then append the type:

```go
// snapshotCache holds the most recent ReceiverPageData read from
// SessionViewer. New seeds it synchronously; Mount starts a single
// background goroutine that refreshes it every chassisTickInterval.
// All connected SSE handlers read via Get (RLock), so Manager.mu
// pressure is decoupled from tab count.
type snapshotCache struct {
	mu   sync.RWMutex
	data ReceiverPageData
}

func (c *snapshotCache) Get() ReceiverPageData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}

func (c *snapshotCache) Set(d ReceiverPageData) {
	c.mu.Lock()
	c.data = d
	c.mu.Unlock()
}
```

- [ ] **Step 4: Add cache fields + lifecycle to Server**

Edit `internal/chassis/server.go`. Imports must include `"context"` and `"sync"` (extend the existing block, don't add a second one):

```go
import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)
```

Replace the `Server` struct:

```go
// Server owns the chassis runtime state.
type Server struct {
	cfg      Config
	session  SessionViewer
	tmpl     *template.Template
	cssBytes []byte

	cache       *snapshotCache
	cacheOnce   sync.Once          // Mount starts the refresher exactly once
	cacheCancel context.CancelFunc // Close() signals the refresher to exit
	cacheDone   chan struct{}      // closed when the refresher goroutine returns
}
```

Replace the `return &Server{...}` block in `New` with:

```go
	s := &Server{
		cfg:       cfg,
		session:   cfg.Session,
		tmpl:      tmpl,
		cssBytes:  cssBytes,
		cache:     &snapshotCache{},
		cacheDone: make(chan struct{}),
	}
	// Seed the cache synchronously so the first SSE connection always
	// sees a coherent snapshot — no zero-value VFD or stale state.
	// New deliberately does NOT start a goroutine: unmounted servers
	// (test ergonomics, offline-friendly modes) leak no background work.
	s.cache.Set(snapshotFromSession(s.cfg, s.session, time.Now()))
	return s, nil
```

Add the lifecycle methods at the bottom of `server.go`:

```go
// startSnapshotRefresher starts the cache refresher goroutine once.
// Called from Mount via sync.Once so multiple Mounts (defensive) or
// no-Mount paths (tests / unmounted servers) don't spawn extras.
func (s *Server) startSnapshotRefresher() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cacheCancel = cancel
	go func() {
		defer close(s.cacheDone)
		t := time.NewTicker(chassisTickInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.cache.Set(snapshotFromSession(s.cfg, s.session, time.Now()))
			}
		}
	}()
}

// Close stops the snapshot refresher goroutine (if Mount ever started
// one) and waits for it to exit. Safe to call multiple times; calling
// Close without a prior Mount returns nil immediately. Production wires
// this from main.go's shutdown sequence; tests register via t.Cleanup.
func (s *Server) Close() error {
	if s.cacheCancel == nil {
		// Mount never ran — nothing to stop.
		return nil
	}
	// Cancel may be called from multiple Close calls; context.CancelFunc
	// is itself idempotent.
	s.cacheCancel()
	select {
	case <-s.cacheDone:
		// goroutine exited
	case <-time.After(time.Second):
		return fmt.Errorf("chassis: snapshot refresher did not exit within 1s")
	}
	return nil
}
```

Update `Mount` to start the refresher:

```go
// Mount registers chassis routes on mux and starts the snapshot cache
// refresher exactly once. Safe to call multiple times (sync.Once
// guards the goroutine start) but only the first call wins.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /receiver", s.handleIndex)
	mux.HandleFunc("GET /receiver/{$}", s.handleIndex)
	mux.HandleFunc("GET /receiver/static/", s.handleStatic)
	mux.HandleFunc("GET /receiver/events", s.handleEvents)
	s.cacheOnce.Do(s.startSnapshotRefresher)
}
```

- [ ] **Step 5: Run the snapshot cache + close tests**

Run: `go test ./internal/chassis/ -run 'TestSnapshotCache_SeedsSynchronouslyBeforeFirstSSE|TestServerClose_StopsSnapshotCacheRefresher'`
Expected: PASS — the synchronous seed and Close lifecycle tests are green. The tab-fanout cache test is introduced in Task 14, after `handleEvents` is routed through the cache, so this task does not commit a known-red test.

Also update existing chassis tests that call `s.Mount(...)` only to start the server lifecycle (for example the state-transition, vfd-title-change, and concurrent-connection tests added above) to register cleanup now that `Close()` exists:

```go
s.Mount(http.NewServeMux())
t.Cleanup(func() { _ = s.Close() })
```

- [ ] **Step 6: Run the full chassis test suite to confirm Phase 0 stability**

Run: `go test ./internal/chassis/...`
Expected: PASS — all Phase 0 tests still green. Tests that call `New()` but never `Mount()` don't leak goroutines because the refresher only starts on `Mount`; tests that do call `Mount()` now register `Close()` cleanup.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/server.go internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): snapshot cache + lifecycle (New seeds; Mount starts; Close waits)

Phase 1 / Spec 2 task 13. Server now owns a snapshotCache:
- New seeds it synchronously via snapshotFromSession — no goroutine
  starts here, so unmounted servers and Phase 0 tests do not leak
  background work.
- Mount starts the refresher exactly once via sync.Once, ticking at
  chassisTickInterval.
- Close cancels the refresher's context and waits for the goroutine
  to exit via cacheDone (1s timeout). Idempotent; safe without Mount.

Tests:
- SeedsSynchronouslyBeforeFirstSSE: New populates the cache with the
  live SessionViewer's state before returning.
- StopsSnapshotCacheRefresher: Close stops the goroutine + is
  idempotent across double-calls.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Route handleEvents through the snapshot cache

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing cache fan-out test**

Append to `internal/chassis/events_test.go`:

```go
func TestSnapshotCache_SingleStatusHomeViewCallPerTickRegardlessOfTabs(t *testing.T) {
	t.Parallel()

	cv := &countingViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = cv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux) // starts the refresher goroutine
	t.Cleanup(func() { _ = s.Close() })

	// Open 5 SSE handlers; let them run for ~5 cache ticks; cancel.
	const tabs = 5
	const ticks = 5
	var wg sync.WaitGroup
	ctxs := make([]context.CancelFunc, tabs)
	for i := 0; i < tabs; i++ {
		i := i
		ctx, cancel := context.WithCancel(context.Background())
		ctxs[i] = cancel
		req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
		w := newFlushRecorder()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleEvents(w, req)
			_ = i
		}()
	}
	time.Sleep(time.Duration(ticks) * chassisTickInterval)
	for _, c := range ctxs {
		c()
	}
	wg.Wait()

	got := cv.Calls()
	// One call from New's synchronous seed + ~one per tick. Allow
	// generous slack for scheduler jitter on slow CI; still vastly
	// less than the per-tab-polling worst case of tabs*ticks plus seed.
	maxAllowed := ticks*2 + 2
	if got > maxAllowed {
		t.Errorf("StatusHomeView called %d times across %d tabs over %d ticks; want <= %d (single shared refresher, not per-tab polling)",
			got, tabs, ticks, maxAllowed)
	}
	if got < 1 {
		t.Errorf("StatusHomeView never called; expected at least the New-time seed call")
	}
}
```

Run: `go test ./internal/chassis/ -run TestSnapshotCache_SingleStatusHomeView`
Expected: FAIL — `handleEvents` still calls `snapshotFromSession` per connected handler.

- [ ] **Step 2: Replace direct snapshotFromSession calls with cache reads in handleEvents**

Edit `internal/chassis/events.go` `handleEvents`. Replace the two `snapshotFromSession(s.cfg, s.session, time.Now())` call sites (one for the initial snapshot, one inside the tick branch) with `s.cache.Get()`:

```go
	last := s.cache.Get()
	if err := emit(w, "state", stateEnvelope{State: string(last.State)}); err != nil {
		return
	}
	if err := emit(w, "vfd", vfdEnvelopeFrom(last.VFD)); err != nil {
		return
	}
	flusher.Flush()

	tick := time.NewTicker(chassisTickInterval)
	defer tick.Stop()
	heartbeat := time.NewTicker(chassisHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			curr := s.cache.Get()
			if curr.State != last.State {
				if err := emit(w, "state", stateEnvelope{State: string(curr.State)}); err != nil {
					return
				}
				last.State = curr.State
			}
			if vfdChanged(curr.VFD, last.VFD) {
				if err := emit(w, "vfd", vfdEnvelopeFrom(curr.VFD)); err != nil {
					return
				}
				last.VFD = curr.VFD
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
```

- [ ] **Step 3: Run the snapshot cache test**

Run: `go test ./internal/chassis/ -run TestSnapshotCache_SingleStatusHomeView`
Expected: PASS — `StatusHomeView` calls are now bounded by the refresher (≈ 1 per tick) regardless of tab count.

- [ ] **Step 4: Run the full chassis test suite to confirm no regression**

Run: `go test ./internal/chassis/...`
Expected: PASS — all events tests still green; state-transition and vfd-change tests now route through the cache; concurrent test still observes events on every connection.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleEvents reads from snapshotCache instead of direct

Phase 1 / Spec 2 task 14. The 5-tab concurrent SSE test now drives one
StatusHomeView call per tick total (down from N×ticks). Manager.mu
pressure is constant regardless of how many chassis viewers are open.
Per-handler diff state (`last`) stays as before so each tab's emit
cadence remains independent — only the upstream read is shared.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: vfd.html data-vfd-* attribute hooks + Layer 2 template test

**Files:**
- Modify: `internal/chassis/templates/vfd.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing Layer 2 template test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestVfdTemplate_RendersDataAttributeHooks(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	var buf bytes.Buffer
	data := VFDData{
		Title:        "TEST-TITLE",
		Marquee:      "TEST-MARQUEE",
		QueueCurrent: 1,
		QueueTotal:   12,
		SystemTime:   "22:47",
		Uptime:       "4H 12M",
	}
	if err := tmpl.ExecuteTemplate(&buf, "vfd", data); err != nil {
		t.Fatalf("execute vfd partial: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"data-vfd-title",
		"data-vfd-marquee",
		"data-vfd-queue",
		"data-vfd-uptime",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("vfd partial missing %q hook; full output:\n%s", want, body)
		}
	}
	// Ghost overlays must still be present — regression guard against
	// accidentally placing data-vfd-* on the outer divs and breaking
	// the seg-text/seg-ghost overlay vocabulary.
	if !strings.Contains(body, "seg-ghost") {
		t.Errorf("vfd partial is missing seg-ghost spans; overlay vocabulary broken")
	}
}
```

Add `"bytes"` to test imports if not already present.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestVfdTemplate_RendersDataAttributeHooks`
Expected: FAIL — vfd.html doesn't have the data-vfd-* attributes yet.

- [ ] **Step 3: Add data-vfd-* attributes to the seg-text spans only**

Edit `internal/chassis/templates/vfd.html`. Replace lines 7, 8, 14, 19 (the title, marquee, queue, and uptime seg-text spans) so each has its `data-vfd-*` attribute. Final template:

```html
{{define "vfd"}}
{{htmlComment "chassis:vfd"}}
<div class="screen-frame">
  <div class="screen vfd">
    <div class="vfd-state vfd-state--idle">
      <div>
        <div class="title-line seg-display"><span class="seg-ghost" aria-hidden="true">~~~~~~~</span><span class="seg-text" data-vfd-title>{{.Title}}</span></div>
        <div class="marquee-line seg-display"><span class="seg-ghost" aria-hidden="true">~~~~~~ ~~~~ ~~ ~ ~~~~ ~~ ~~ ~~~~~~~ ~ ~~ ~~~~~~~ ~ ~ ~~~~~ ~~~ ~~ ~~~~ ~ ~~~~~~</span><span class="seg-text" data-vfd-marquee>{{.Marquee}}</span></div>
      </div>
      <div class="right-panel">
        <div class="lbl">System time</div>
        <div class="time-row">
          <div class="queue-stack">
            <div class="queue-v seg-display"><span class="seg-ghost" aria-hidden="true">88 ~ 88</span><span class="seg-text" data-vfd-queue>{{.QueueCurrent}} / {{.QueueTotal}}</span></div>
            <div class="queue-k">QUEUE</div>
          </div>
          <div class="big-time"><span class="seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-system-time>{{.SystemTime}}</span></span></div>
        </div>
        <div class="freq"><span class="seg-display"><span class="seg-ghost" aria-hidden="true">~~~~~~</span><span class="seg-text">UPTIME</span></span> <span class="seg-display"><span class="seg-ghost" aria-hidden="true">88~ 88~</span><span class="seg-text" data-vfd-uptime>{{.Uptime}}</span></span></div>
      </div>
    </div>
  </div>
</div>
{{end}}
```

(Only the four seg-text spans gain attributes; ghost spans stay untouched. `data-system-time` on the clock seg-text is unchanged from Phase 0.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestVfdTemplate_RendersDataAttributeHooks`
Expected: PASS.

- [ ] **Step 5: Run the full chassis test suite to confirm no regression**

Run: `go test ./internal/chassis/...`
Expected: PASS — including the existing `TestHandleIndex_*` tests, which still find `STANDBY` in the rendered idle output (the new attribute placement doesn't change visible text).

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/templates/vfd.html internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): VFD template data-vfd-* hooks on seg-text spans

Phase 1 / Spec 2 task 15. Adds data-vfd-title, data-vfd-marquee,
data-vfd-queue, data-vfd-uptime attributes on the inner seg-text
spans only. The seg-ghost overlay siblings stay untouched so the
"all-segments-lit-behind" dim background survives live JS updates.
Template test asserts both the new attributes and the surviving
seg-ghost vocabulary.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: shell.html loads vfd-live.js

**Files:**
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test for the script tag**

Append to `internal/chassis/chassis_test.go`:

```go
func TestShellTemplate_LoadsVfdLiveScript(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/receiver/static/vfd-live.js?v=test-1.0.0`) {
		t.Errorf("shell.html should include versioned vfd-live.js script tag")
	}
	chassisIdx := strings.Index(body, "chassis.js?v=")
	vfdIdx := strings.Index(body, "vfd-live.js?v=")
	if chassisIdx < 0 || vfdIdx < 0 {
		t.Fatalf("missing one of the script tags")
	}
	if vfdIdx <= chassisIdx {
		t.Errorf("vfd-live.js script must appear AFTER chassis.js so the deferred load order initializes window.Chassis first")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestShellTemplate_LoadsVfdLiveScript`
Expected: FAIL — shell.html doesn't reference vfd-live.js yet.

- [ ] **Step 3: Add the script tag**

Edit `internal/chassis/templates/shell.html`. Find the existing chassis.js script tag and add the vfd-live.js tag immediately after it. The relevant portion of `<head>`:

```html
<link rel="stylesheet" href="/receiver/static/chassis.css?v={{.Version}}">
<script defer src="/receiver/static/chassis.js?v={{.Version}}"></script>
<script defer src="/receiver/static/vfd-live.js?v={{.Version}}"></script>
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestShellTemplate_LoadsVfdLiveScript`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/shell.html internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): shell.html loads vfd-live.js after chassis.js

Phase 1 / Spec 2 task 16. The new script tag is deferred and ordered
after chassis.js so window.Chassis is fully populated by the time
vfd-live.js boots. Test asserts both presence and ordering.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 17: Create vfd-live.js

**Files:**
- Create: `internal/chassis/static/vfd-live.js`

- [ ] **Step 1: Create the file**

Create `internal/chassis/static/vfd-live.js`:

```javascript
// Receiver chassis VFD live wire. Phase 1 / Spec 2.
//
// Subscribes to /receiver/events (SSE), routes named events:
//   state -> window.Chassis.State.set(idle|live)
//   vfd   -> textContent updates on data-vfd-{title,marquee,queue,uptime}
//
// Loaded after chassis.js (Phase 0) so window.Chassis is populated.
// Each later spec ships its own JS file that hangs off window.Chassis
// the same way.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('vfd-live: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  let source = null;

  function handleStateEvent(ev) {
    try {
      const { state } = JSON.parse(ev.data);
      if (state === 'idle' || state === 'live') {
        window.Chassis.State.set(state);
      }
    } catch (err) {
      console.warn('vfd-live: bad state payload', ev.data, err);
    }
  }

  function handleVfdEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      const title = document.querySelector('[data-vfd-title]');
      const marquee = document.querySelector('[data-vfd-marquee]');
      const queue = document.querySelector('[data-vfd-queue]');
      const uptime = document.querySelector('[data-vfd-uptime]');
      if (title) title.textContent = data.title || '';
      if (marquee) marquee.textContent = data.marquee || '';
      if (queue) queue.textContent = `${data.queueCurrent} / ${data.queueTotal}`;
      if (uptime) uptime.textContent = data.uptime || '';
    } catch (err) {
      console.warn('vfd-live: bad vfd payload', ev.data, err);
    }
  }

  function connect() {
    source = new EventSource('/receiver/events');
    source.addEventListener('state', handleStateEvent);
    source.addEventListener('vfd', handleVfdEvent);
    source.addEventListener('error', () => {
      console.info('vfd-live: stream interrupted; browser will retry using the SSE retry directive');
    });
  }

  // Expose for the ?dev=1 toggle and integration debugging.
  window.Chassis.events = {
    reconnect() {
      if (source) source.close();
      connect();
    },
  };

  document.addEventListener('DOMContentLoaded', connect);
})();
```

- [ ] **Step 2: Verify the embed picks the file up**

Run: `go build ./...`
Expected: build succeeds. `//go:embed static` recursively includes the new file automatically (matches Phase 0 pattern).

- [ ] **Step 3: Add a smoke handler test that the file is served**

Append to `internal/chassis/chassis_test.go`:

```go
func TestHandleStatic_VfdLiveJSServed(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodGet, "/receiver/static/vfd-live.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET vfd-live.js status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") && !strings.HasPrefix(got, "application/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript or application/javascript", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "window.Chassis.events") {
		t.Errorf("served vfd-live.js doesn't contain window.Chassis.events namespace export")
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/chassis/ -run TestHandleStatic_VfdLiveJSServed`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/vfd-live.js internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): vfd-live.js EventSource subscriber

Phase 1 / Spec 2 task 17. ~70-line vanilla ES2022 script subscribes to
/receiver/events, routes `state` and `vfd` named events, writes title/
marquee/queue/uptime into the seg-text spans via data-vfd-* queries.
window.Chassis.events.reconnect() exposed for ?dev=1 debugging. Smoke
test confirms the embedded file serves with a JavaScript Content-Type.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 18: Wire chassis.Config.Session + Close() in main.go

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Locate the existing chassis.New call**

Find the existing `chassis.New(...)` block in `cmd/mister-groovy-relay/main.go` (added during Phase 0). It looks like:

```go
chassisSrv, err := chassis.New(chassis.Config{
    Bridge:    sec.Bridge,
    Manager:   coreMgr,
    Registry:  reg,
    Version:   version,
    StartedAt: startedAt,
    HostIP:    hostIP,
})
if err != nil {
    dieFriendly("chassis init", err)
}
chassisSrv.Mount(mux)
```

- [ ] **Step 2: Add Session + defer Close()**

Replace with:

```go
chassisSrv, err := chassis.New(chassis.Config{
    Bridge:    sec.Bridge,
    Manager:   coreMgr,
    Registry:  reg,
    Version:   version,
    StartedAt: startedAt,
    HostIP:    hostIP,
    Session:   coreMgr, // *core.Manager structurally satisfies chassis.SessionViewer
})
if err != nil {
    dieFriendly("chassis init", err)
}
defer func() {
    if err := chassisSrv.Close(); err != nil {
        slog.Warn("chassis close", "err", err)
    }
}()
chassisSrv.Mount(mux)
```

- [ ] **Step 3: Build and verify**

Run:

```bash
go build ./cmd/mister-groovy-relay
go test ./...
```

Expected: build succeeds, all tests pass.

- [ ] **Step 4: Manual smoke test**

Run the bridge against a real config:

```bash
./mister-groovy-relay --config path/to/config.toml
```

In a browser open `http://localhost:32500/receiver`. Expected:

- Page renders idle state (chassis chrome + STANDBY).
- DevTools Network panel shows an `EventStream` request to `/receiver/events`.
- The EventStream tab shows the `retry: 3000` directive, then `state: idle` + `vfd: {...STANDBY...}` events, then periodic `: heartbeat` comments.

Trigger a real cast (Plex push or equivalent). Within ~500ms the chassis should:
- Body class flips to `live`
- VFD title shows the cast title
- VFD marquee shows `<SOURCE> · <pos> / <dur>`

Stop the cast. Body flips back to `idle`, title back to `STANDBY`.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "$(cat <<'EOF'
feat(chassis): wire chassis.Config.Session = coreMgr + defer Close()

Phase 1 / Spec 2 task 18. main.go now passes *core.Manager as the
SessionViewer (structurally satisfied via StatusHomeView). Server.Close
deferred so the snapshot refresher goroutine shuts down cleanly when
main.go returns. Bridge starts and serves a live chassis at /receiver.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 19: Integration tests (Layer 3)

**Files:**
- Modify: `tests/integration/chassis_test.go`

- [ ] **Step 1: Locate the existing Phase 0 integration test file**

The file `tests/integration/chassis_test.go` already exists (added during Phase 0) with `//go:build integration` and `TestReceiverEndToEnd`. We extend it.

- [ ] **Step 2: Add the SSE end-to-end test**

Extend the existing `tests/integration/chassis_test.go` import block with `bufio` and `context` (the file already imports `net/http`, `net/http/httptest`, `strings`, and `time`). Also add `defer chassisSrv.Close()` to the existing `TestReceiverEndToEnd` after `chassis.New(...)`, because `Mount` starts the snapshot-cache refresher once this spec lands.

Then append these tests and helper types below the existing tests:

```go
func TestReceiverEvents_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Now(),
		HostIP:    "10.0.0.5",
		// Session=nil — exercises the idle-only path through the real handler.
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/receiver/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	// Read until we observe the initial state + vfd events.
	rdr := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var sawState, sawVfd, sawRetry bool
	for time.Now().Before(deadline) && !(sawState && sawVfd && sawRetry) {
		line, err := rdr.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE stream: %v", err)
		}
		switch {
		case strings.HasPrefix(line, "retry: 3000"):
			sawRetry = true
		case strings.HasPrefix(line, "event: state"):
			sawState = true
		case strings.HasPrefix(line, "event: vfd"):
			sawVfd = true
		}
	}
	if !sawRetry {
		t.Errorf("did not observe retry: 3000 directive")
	}
	if !sawState {
		t.Errorf("did not observe event: state record")
	}
	if !sawVfd {
		t.Errorf("did not observe event: vfd record")
	}
}

func TestReceiverEvents_LivePathReachesClient(t *testing.T) {
	// Exercises the full live-session SSE path: a fake SessionViewer
	// reports core.StatePlaying; the chassis Server (with Mount started
	// so the refresher runs) must emit a vfd event whose payload
	// contains the cast title within the first few records.
	mux := http.NewServeMux()

	// integration-local fake matching the chassis.SessionViewer shape.
	// Defined here (not imported from internal/chassis) because the
	// integration package legitimately depends on both internal/chassis
	// and internal/core — the production cross-import lint exempts
	// _test.go files (Phase 0).
	fake := &fakeIntegrationSession{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "Integration Live Title", Source: "plex",
		Position: 10 * time.Second, Duration: 90 * time.Second,
	}}
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Now(),
		HostIP:    "10.0.0.5",
		Session:   fake,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/receiver/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer resp.Body.Close()

	rdr := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var sawLiveState, sawLiveTitle bool
	for time.Now().Before(deadline) && !(sawLiveState && sawLiveTitle) {
		line, err := rdr.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE stream: %v", err)
		}
		if strings.Contains(line, `"state":"live"`) {
			sawLiveState = true
		}
		if strings.Contains(line, `"title":"Integration Live Title"`) {
			sawLiveTitle = true
		}
	}
	if !sawLiveState {
		t.Errorf("did not observe live state event")
	}
	if !sawLiveTitle {
		t.Errorf("did not observe live title in vfd event payload")
	}
}

// fakeIntegrationSession is the integration-test SessionViewer fake.
// Lives in the integration package; chassis.SessionViewer is satisfied
// structurally via the StatusHomeView() method signature.
type fakeIntegrationSession struct {
	view core.StatusHomeView
}

func (f *fakeIntegrationSession) StatusHomeView() core.StatusHomeView { return f.view }

func TestReceiverEvents_DoesNotShadowUIRoutes(t *testing.T) {
	mux := http.NewServeMux()

	uiSrv, err := ui.New(ui.Config{
		Registry: adapters.NewRegistry(),
		Version:  "integration-test",
	})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	uiSrv.Mount(mux)

	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Now(),
		HostIP:    "10.0.0.5",
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// /ui/playback/banner (the existing htmx-polled live banner) is
	// independent of /receiver/events.
	uiResp, err := srv.Client().Get(srv.URL + "/ui/playback/banner")
	if err != nil {
		t.Fatalf("GET /ui/playback/banner: %v", err)
	}
	defer uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK {
		t.Errorf("/ui/playback/banner status = %d, want 200", uiResp.StatusCode)
	}
	if got := uiResp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("/ui/playback/banner Content-Type = %q, want text/html prefix", got)
	}

	// /receiver/events is SSE.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/receiver/events", nil)
	rxResp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer rxResp.Body.Close()
	if got := rxResp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("/receiver/events Content-Type = %q, want text/event-stream", got)
	}
}
```

- [ ] **Step 3: Run the integration tests**

Run: `make test-integration` (or `go test -tags=integration ./tests/integration/...`)
Expected: PASS — both new tests + all existing integration tests.

- [ ] **Step 4: Commit**

```bash
git add tests/integration/chassis_test.go
git commit -m "$(cat <<'EOF'
test(chassis): integration coverage for /receiver/events

Phase 1 / Spec 2 task 19. TestReceiverEvents_EndToEnd boots a chassis
server, opens an http.Client to /receiver/events, scans the SSE stream
for retry: 3000 + event: state + event: vfd records.
TestReceiverEvents_DoesNotShadowUIRoutes mounts both ui.Server and
chassis.Server on a shared mux and confirms /ui/playback/banner (htmx)
and /receiver/events (SSE) coexist without route collision.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 20: Manual verification + PR description

**Files:**
- None (verification + PR prep only)

- [ ] **Step 1: Run the full CI matrix locally**

Run:

```bash
make lint
make test
go test -race ./...
make test-integration
```

Expected: all four exit 0. If anything is red, return to the offending task.

- [ ] **Step 2: Build a fresh binary and start the bridge**

```bash
make build
./mister-groovy-relay --config path/to/test-config.toml
```

- [ ] **Step 3: Run the manual verification checklist**

Open the chassis at `http://<bridge-host>:<http_port>/receiver` and confirm each item:

| # | Check | Pass condition |
|---|---|---|
| 1 | First load (no cast) | `<body class="receiver idle">`; VFD reads `STANDBY` + the idle marquee hint |
| 2 | Browser DevTools → Network → EventStream | `retry: 3000` directive visible; `event: state {"state":"idle"}` + `event: vfd` followed by periodic `: heartbeat` comments every ~30 s |
| 3 | Start a Plex cast (push a known title) | Within ~1 s: body class flips to `live`; VFD title shows the real cast title; marquee shows `PLEX · <pos> / <dur>` updating each tick |
| 4 | Pause the cast | Body stays `live`; transport-row pause indicator is Spec 3 territory and not exercised here |
| 5 | Stop the cast | Within ~1 s: body flips back to `idle`; VFD reads `STANDBY`; marquee back to idle hint |
| 6 | Open `/receiver?dev=1` | Phase 0 floating toggle button visible; clicking it flips `body` between idle/live when no real cast is active. Starting a real cast then ending it overrides the manual toggle on the next `state` event |
| 7 | Simulate disconnect via DevTools → Network → Offline | After ~3 s the EventStream tab shows the connection reconnect attempt; console shows `vfd-live: stream interrupted...`. Restore Online and the stream resumes; state re-syncs |
| 8 | Open 3+ chassis tabs simultaneously | Each tab independently shows the same live state during a cast; bridge logs do not show goroutine-count growth proportional to tabs (per-server snapshot cache decouples lock pressure) |

- [ ] **Step 4: Capture screenshots for the PR description**

For each of the following states, attach a screenshot to the PR:

1. Idle preview at 1920px (matches Phase 0 visual; sanity check)
2. Live state during an active cast at 1920px (VFD shows real title + marquee)
3. DevTools EventStream tab during a cast (visible `state` + `vfd` events)
4. DevTools Console showing the `vfd-live: stream interrupted` log line after simulated disconnect

- [ ] **Step 5: Draft the PR description**

Use this template:

```markdown
# Phase 1 / Spec 2: Receiver Chassis VFD Live

Implements docs/superpowers/specs/2026-05-21-receiver-chassis-vfd-live-design.md.

## What's in

- New SSE stream at `GET /receiver/events` emitting `state` + `vfd` named JSON events
- `SessionViewer` interface (`internal/chassis/session.go`); `*core.Manager` satisfies structurally via `StatusHomeView()`
- `snapshotFromSession()` maps `core.StatusHomeView` to chassis page data. State mapping: `StateIdle → idle`, `StatePlaying + StatePaused → live`. Marquee formatted server-side from `Source + Position/Duration`. Queue stays 0/0 placeholder for now (Spec 3+ surfaces queue data).
- Per-server snapshot cache + background refresher → `core.Manager.mu` acquired at constant 4 Hz regardless of connected-tab count
- `vfd-live.js` (~70 lines) subscribes to the stream, writes title/marquee/queue/uptime into the VFD's `seg-text` spans via `data-vfd-*` hooks. seg-ghost overlay siblings stay intact.
- `chassis.Server.Close()` lifecycle hook; wired in `main.go`

## Tests

- 21 Layer 1 tests (handler, cache lifecycle including synchronous seed + Close idempotence, snapshot mapping, envelope encoding, marquee clock formatting per spec)
- 1 Layer 2 template guard for the data-vfd-* attributes
- 3 Layer 3 integration tests (`TestReceiverEvents_EndToEnd`, `TestReceiverEvents_LivePathReachesClient`, `TestReceiverEvents_DoesNotShadowUIRoutes`)
- Cross-package import lint (Phase 0) still green — no imports of `internal/ui` / `internal/uiserver`

## Manual verification

Performed at 1920px in Chrome current. Screenshots attached:

- [ ] Idle preview
- [ ] Live during an active cast
- [ ] DevTools EventStream tab
- [ ] DevTools Console showing reconnect log after simulated disconnect

State transitions observed within <1s of cast start/stop. SSE reconnect within ~3s of DevTools Offline toggle. Five-tab concurrent test (`/receiver` open in 5 tabs simultaneously) shows synchronized live state.

## What's not in (deferred per spec)

- Transport POST endpoints — Spec 3
- Visualizer wiring — Spec 4
- Spectrum / goniometer / throughput / ACK events — Spec 5
- Real queue data on the wire format — later spec, when `core.StatusHomeView` grows queue fields
```

- [ ] **Step 6: Open the PR**

```bash
git push -u origin feature/receiver-chassis-vfd-live
gh pr create --title "Phase 1 / Spec 2: receiver chassis VFD live" --body "$(cat <pr-body.md)"
```

(Or the equivalent of your normal PR workflow. The plan is complete once the PR is opened with the manual verification checklist signed off.)

---

## Self-Review

Cross-checking the spec sections against tasks:

**Goals (spec §Goals 1-5):**
- Mount `/receiver/events` SSE → **Tasks 7-12.**
- Server emits `state` + `vfd` events → **Tasks 8-9.**
- `idleSnapshot` → `snapshotFromSession` with SessionViewer → **Tasks 1-4.**
- Client-side `vfd-live.js` + `data-vfd-*` hooks → **Tasks 15-17.**
- SSE auto-reconnect; server sends fresh initial snapshot → **Task 7** (initial snapshot on connect; `retry: 3000` pins reconnect cadence).

**Architecture (spec §Architecture):**
- `SessionViewer` interface mirrors `internal/ui.StatusViewer` → **Task 1.**
- `Server.session` + `Server.Close()` + `snapshotCache` → **Tasks 1, 4, 13, 14.**
- Field mapping (`core.State` → chassis state; marquee formula; queue placeholder) → **Tasks 3, 4.**
- Lock contention guard via shared snapshot cache → **Tasks 13, 14.**

**SSE Wire Protocol (spec §SSE Wire Protocol):**
- Connection headers + `X-Accel-Buffering: no` → **Task 7.**
- `event: state` + `event: vfd` named events with camelCase JSON → **Tasks 5, 8, 9.**
- Initial snapshot on connect → **Task 7.**
- Heartbeat `: heartbeat\n\n` every 30s → **Task 10.**
- `retry: 3000` directive → **Task 7.**
- `vfdChanged` field-level diff helper → **Task 6.**

**Client-Side Update Strategy (spec §Client-Side):**
- `vfd-live.js` runtime → **Task 17.**
- `data-vfd-*` attribute hooks on seg-text spans → **Task 15.**
- `<script defer>` document ordering after `chassis.js` → **Task 16.**
- Server-side initial render → unchanged from Phase 0 (handleIndex still renders the right state on cold load via `snapshotFromSession`).
- Dev toggle composition → no code change needed; Phase 0 toggle already composes correctly.

**Testing Approach (spec §Testing):**
- Layer 1 (21 tests, including all spec-mandated names) → **Tasks 1-14 each lead with the failing test. Task 13 covers `TestSnapshotCache_SeedsSynchronouslyBeforeFirstSSE` + `TestServerClose_StopsSnapshotCacheRefresher` per spec.**
- Layer 2 (template guard) → **Task 15.**
- Layer 3 (`TestReceiverEvents_EndToEnd` + `TestReceiverEvents_LivePathReachesClient` + `TestReceiverEvents_DoesNotShadowUIRoutes`) → **Task 19.**
- Manual verification → **Task 20.**

**Migration & Rollout (spec §Migration):**
- Coexistence with `/ui/*` → **Task 19** (`TestReceiverEvents_DoesNotShadowUIRoutes`).
- `vfd-live.js` cache-bust `?v={{.Version}}` → **Task 16.**
- No new config fields → satisfied by omission throughout.
- `chassis.Server.Close()` wired in main.go → **Task 18.**
- README preview note (Phase 0) covers Spec 2 — no docs change.

**Design Decisions Worth Revisiting (spec §):**
- SSE over htmx polling — implemented per spec choice; reviewer recourse documented in spec.
- Diff ticker over publish/subscribe broker — implemented per spec choice; **Task 13** lock-contention test guards the choice empirically.
- `data-vfd-*` over class-name selectors — implemented per spec choice; **Task 15** seg-ghost regression guard locks it in.

**Placeholder scan:** None. Every step has concrete code, exact commands, expected output.

**Type consistency:**
- `SessionViewer` interface defined in Task 1, referenced in Tasks 2-4, 7, 13. Signature is `StatusHomeView() core.StatusHomeView` throughout.
- `chassis.Config.Session SessionViewer` consistent.
- `Server.session` field consistent.
- `snapshotCache` / `Server.cache` / `Server.cacheCancel` / `Server.cacheDone` / `Server.Close()` consistent across Tasks 13-14, 18.
- `chassisTickInterval` and `chassisHeartbeatInterval` package vars consistent.
- `stateEnvelope.State`, `vfdEnvelope.Title/Marquee/QueueCurrent/QueueTotal/Uptime` consistent across Tasks 5, 7-9, 14.
- `vfdChanged(a, b VFDData) bool` signature consistent across Task 6 and its consumer in Task 9.
- `emit(w io.Writer, name string, payload any) error` consistent across Task 5 and consumers in Tasks 7-9.

No drift detected.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-21-receiver-chassis-vfd-live.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
