# URL Adapter (v1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a minimum-viable URL adapter (`internal/adapters/url/`) that accepts an HTTP/HTTPS media URL and casts it via the existing data plane, plus the Plex precursor work the URL adapter surfaces.

**Architecture:** New peer-of-Plex adapter package implementing `adapters.Adapter` + `adapters.Validator` + `adapters.RouteProvider` + `ui.EnableSetter`. Single TOML key (`enabled`). Two HTTP routes (`POST /play`, `GET /panel`). Fire-and-forget — no per-session pause/seek/stop. Plex precursor: the existing `OnStop` closure does not clear local state nor notify the timeline broker; URL adapter forces this fix by being structurally different from Plex.

**Tech Stack:** Go 1.26, BurntSushi/toml, html/template, htmx, net/url, crypto/rand. Tests use stdlib `testing` + `httptest`.

**Spec:** [docs/specs/2026-04-25-url-adapter-design.md](docs/specs/2026-04-25-url-adapter-design.md)

**Order rationale:** Phase A (Plex precursor) ships first because the URL adapter's preemption tests (Phase D3) depend on the broker contract introduced in A1/A2. Phases B and C can be developed against the in-memory adapter without the precursor work landing, but Phase D3 cannot pass until A is merged.

---

## Phase A — Plex precursor

### Task A1: Add `broadcastStoppedFor` to TimelineBroker

**Why:** The current `notifyStoppedTimeline` reads media identity through `t.playContext()` (== Companion's `lastPlaySession`). On cross-adapter preemption, the `lastPlay` is in flux and `Manager.Status()` already reflects the foreign session. The new entry point takes captured state explicitly so the closure can pass what it needs without depending on the broker's lookup path.

This task also performs a small upfront refactor: thread `play` through `buildTimelineXMLWithCommandID` as an explicit parameter so `broadcastStoppedFor` can reuse the existing builder instead of duplicating ~70 lines of XML struct definitions and field-mapping logic. Single source of truth for the Timeline XML schema.

**Files:**
- Modify: `internal/adapters/plex/timeline.go` (refactor builder signature; add `broadcastStoppedFor` near line 214 after `broadcastStatusOnce`)
- Test: `internal/adapters/plex/timeline_test.go` (append two new tests)

- [ ] **Step 1: Refactor `buildTimelineXMLWithCommandID` to take `play` as an explicit parameter**

In `internal/adapters/plex/timeline.go`:

1. Change the signature of `buildTimelineXMLWithCommandID` from
   `(s core.SessionStatus, commandID int) string` to
   `(s core.SessionStatus, play PlayMediaRequest, commandID int) string`.
2. Inside that function, **delete** the inner `play := PlayMediaRequest{}; if t.playContext != nil { play = t.playContext() }` block (currently around lines 313-316). The `play` parameter replaces it.
3. Update `buildTimelineXML` (currently `return t.buildTimelineXMLWithCommandID(s, 0)`) to:
   ```go
   func (t *TimelineBroker) buildTimelineXML(s core.SessionStatus) string {
       play := PlayMediaRequest{}
       if t.playContext != nil {
           play = t.playContext()
       }
       return t.buildTimelineXMLWithCommandID(s, play, 0)
   }
   ```
4. Update `broadcastStatusOnce`'s subscriber-loop builder call (currently
   `xmlBody := t.buildTimelineXMLWithCommandID(st, s.commandID)`) to
   `xmlBody := t.buildTimelineXMLWithCommandID(st, play, s.commandID)`.
   The local `play` is already in scope from line 232-235 of the existing code.

- [ ] **Step 2: Verify existing tests still pass after the refactor**

Run: `go test ./internal/adapters/plex/`
Expected: PASS. Behavior is preserved — only the indirection changed.

- [ ] **Step 3: Write the failing tests for `broadcastStoppedFor`**

Append at the end of `internal/adapters/plex/timeline_test.go`:

```go
// TestTimeline_BroadcastStoppedFor_UsesCapturedPlay verifies that the
// new broker entry point synthesizes timeline XML from the captured
// PlayMediaRequest and IGNORES playContext (which may already point at
// a foreign session after cross-adapter preempt).
func TestTimeline_BroadcastStoppedFor_UsesCapturedPlay(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	// playContext returns a DIFFERENT play (simulating "URL adapter has
	// already taken over"). The broker MUST NOT consult it.
	b.SetPlayContextProvider(func() PlayMediaRequest {
		return PlayMediaRequest{MediaKey: "/library/metadata/wrong"}
	})

	captured := PlayMediaRequest{
		PlexServerAddress: "192.168.1.10",
		PlexServerPort:    "32400",
		PlexServerScheme:  "http",
		MediaKey:          "/library/metadata/42",
		ContainerKey:      "/playQueues/99?own=1",
		PlayQueueItemID:   "item-123",
	}

	// Stand up an httptest controller endpoint and subscribe to it.
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	b.Subscribe("client-a", host, port, "http", 0)

	stopped := core.SessionStatus{State: core.StateIdle}
	b.broadcastStoppedFor(stopped, captured)
	// broadcastStoppedFor pushes synchronously, so no sleep is needed.

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("subscriber received %d pushes, want 1", len(bodies))
	}
	body := bodies[0]
	if !strings.Contains(body, `state="stopped"`) {
		t.Errorf("body missing state=stopped: %s", body)
	}
	if !strings.Contains(body, `key="/library/metadata/42"`) {
		t.Errorf("body did not use captured MediaKey: %s", body)
	}
	if strings.Contains(body, "/library/metadata/wrong") {
		t.Errorf("body leaked playContext data: %s", body)
	}
}

// TestTimeline_BroadcastStoppedFor_NoSubscribers is a no-op smoke test
// — calling with zero subscribers must not panic and must not push to PMS
// (the captured PlayMediaRequest may not have a PMS address set when the
// only target is a controller).
func TestTimeline_BroadcastStoppedFor_NoSubscribers(t *testing.T) {
	b := newTestBroker(t, core.SessionStatus{})
	b.broadcastStoppedFor(core.SessionStatus{State: core.StateIdle}, PlayMediaRequest{})
	// No assertion — just must not panic.
}
```

Add the missing imports to the existing `import` block (top of file):

```go
import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/adapters/plex/ -run TestTimeline_BroadcastStoppedFor -v`
Expected: FAIL — the test file won't compile because `b.broadcastStoppedFor` is undefined.

- [ ] **Step 5: Implement `broadcastStoppedFor`**

Insert into `internal/adapters/plex/timeline.go` immediately after `broadcastStatusOnce` (after line 270):

```go
// broadcastStoppedFor pushes one timeline to all current subscribers using
// the supplied PlayMediaRequest as the source of media identity, instead of
// reading the broker's playContext callback. Used by the cross-adapter
// preemption path: when a foreign adapter (e.g. URL) preempts Plex, the
// Companion's lastPlay is being torn down and Manager.Status() already
// reflects the foreign session; the closure captures the prior Plex play
// at request-construction time and passes it here so the controller-bound
// timeline accurately describes the prior media as state=stopped.
//
// PMS pushes are skipped: this is a controller-cleanup primitive only.
// The PMS-side StopTranscodeSession call is the prior session's
// responsibility (already wired into the OnStop closure).
//
// Race note: subscriber pruning by RunBroadcastLoop is concurrent with
// this call. "All current subscribers" means subscribers live at the
// moment we acquire mu; pruned subscribers are by definition not
// listening, so missing them is correct (spec §"Plex precursor" item 1.c).
func (t *TimelineBroker) broadcastStoppedFor(st core.SessionStatus, play PlayMediaRequest) {
	t.mu.Lock()
	subs := make([]subscriber, 0, len(t.subscribers))
	for _, s := range t.subscribers {
		subs = append(subs, *s)
	}
	client := t.httpClient
	t.mu.Unlock()

	body := t.buildTimelineXMLWithCommandID(st, play, 0)
	for _, s := range subs {
		protocol := s.protocol
		if protocol == "" {
			protocol = "http"
		}
		url := fmt.Sprintf("%s://%s:%s/:/timeline", protocol, s.host, s.port)
		if err := t.postTimeline(client, url, body, s.clientID, ""); err != nil {
			slog.Debug("stopped timeline push failed", "sub", s.clientID, "err", err)
			continue
		}
	}
}
```

This reuses the refactored `buildTimelineXMLWithCommandID` (see Step 1) — no duplicate XML builder.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/adapters/plex/ -run TestTimeline_BroadcastStoppedFor -v`
Expected: PASS for both tests.

- [ ] **Step 7: Run the full Plex test suite to confirm no regressions**

Run: `go test ./internal/adapters/plex/`
Expected: PASS — Step 1's refactor was transparent and `broadcastStoppedFor` is additive.

- [ ] **Step 8: Commit**

```bash
git add internal/adapters/plex/timeline.go internal/adapters/plex/timeline_test.go
git commit -m "feat(plex): add broadcastStoppedFor for cross-adapter preempt"
```

---

### Task A2: Wire `OnStop` closure to capture state, broadcast, and conditionally clear

**Why:** Today the `OnStop` body only calls `StopTranscodeSession`. After this task it also broadcasts a stopped timeline using captured state and conditionally clears `lastPlay` (only if still pointing at this session — see step 2). The conditional clear avoids a race with `handlePlayMedia`'s Plex→Plex flow, where the closure runs in a goroutine after the new `rememberPlaySession(p)` already overwrote `lastPlay`; an unconditional clear would wipe the new session's metadata.

**Files:**
- Modify: `internal/adapters/plex/companion.go` (add `clearPlaySessionIfMatches` helper near `clearPlaySession`; replace the `req.OnStop = func...` block in `sessionRequestFor`)
- Test: `internal/adapters/plex/companion_test.go` (append cross-adapter preempt test plus a Plex→Plex non-clear test)

- [ ] **Step 1: Find and confirm the existing helpers**

Run: `grep -n "lastPlaySession\|rememberPlaySession\|clearPlaySession" internal/adapters/plex/companion.go`
Expected: locates `clearPlaySession` (already at ~line 935), `lastPlaySession` getter (~line 941), and `rememberPlaySession` setter — all guarded by `c.sessMu`. The plan adds ONE new helper (`clearPlaySessionIfMatches`) but reuses the existing ones for everything else.

- [ ] **Step 1.5: Add the `clearPlaySessionIfMatches` helper**

Insert into `internal/adapters/plex/companion.go` immediately after `clearPlaySession` (~line 939):

```go
// clearPlaySessionIfMatches resets c.lastPlay to its zero value ONLY
// if the current MediaKey matches the supplied one. Used by OnStop
// closures to safely clear stale Plex session state without racing
// against a concurrent rememberPlaySession from handlePlayMedia for a
// new session. If lastPlay has already been overwritten with a fresh
// playMedia (Plex→Plex preempt), this is a no-op.
func (c *Companion) clearPlaySessionIfMatches(mediaKey string) {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()
	if c.lastPlay.MediaKey == mediaKey {
		c.lastPlay = PlayMediaRequest{}
	}
}
```

- [ ] **Step 2: Write the failing test**

Append to `internal/adapters/plex/companion_test.go`:

```go
// TestSessionRequestFor_OnStopBroadcastsAndClearsForCrossAdapter
// is the URL-adapter precursor contract for the cross-adapter case:
// when OnStop fires for the prior Plex session and lastPlay still
// points at that session (no successor Plex playMedia interleaved),
// the closure must (1) push a stopped timeline using the captured
// PlayMediaRequest — NOT whatever Manager.Status() currently reports —
// and (2) clear c.lastPlay so the broker's next 1Hz tick doesn't keep
// pushing Plex media identity while a foreign adapter owns the session.
func TestSessionRequestFor_OnStopBroadcastsAndClearsForCrossAdapter(t *testing.T) {
	// Set up a controller endpoint and a broker pointed at it.
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	c := NewCompanion(CompanionConfig{
		DeviceName: "MiSTer", DeviceUUID: "uuid-1", ProfileName: "Plex Home Theater",
	}, nil)
	broker := NewTimelineBroker(TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		func() core.SessionStatus { return core.SessionStatus{} })
	// playContext returns a wrong/foreign play — simulates URL adapter active.
	broker.SetPlayContextProvider(func() PlayMediaRequest {
		return PlayMediaRequest{MediaKey: "/library/metadata/wrong"}
	})
	c.SetTimeline(broker)
	u, _ := url.Parse(srv.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	broker.Subscribe("client-a", host, port, "http", 0)

	// Remember a Plex play, then build a SessionRequest from it.
	// PlexServerAddress points at 127.0.0.1:1 — guaranteed-unreachable
	// loopback; StopTranscodeSession's TCP connect fails fast (refused)
	// across Linux/macOS/Windows so the test isn't gated on network
	// timeouts. Note that even if it WERE slow, the broadcast happens
	// BEFORE StopTranscodeSession in the closure (see step 4) so this
	// test would still complete deterministically.
	prior := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-1", PlexToken: "tok",
	}
	c.rememberPlaySession(prior)
	req := c.sessionRequestFor(prior)

	// Fire OnStop synchronously (simulates Manager preempt notifying
	// the prior session). The closure body itself is synchronous — the
	// broadcast pushes happen before OnStop returns.
	req.OnStop("preempted")

	// 1. lastPlay must be cleared.
	if got := c.lastPlaySession().MediaKey; got != "" {
		t.Errorf("lastPlay not cleared: MediaKey = %q", got)
	}

	// 2. Subscriber received exactly one stopped timeline addressed to
	//    the prior media key.
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("subscriber pushes = %d, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], `key="/library/metadata/42"`) {
		t.Errorf("body did not use captured prior MediaKey: %s", bodies[0])
	}
	if strings.Contains(bodies[0], "/library/metadata/wrong") {
		t.Errorf("body leaked playContext data: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], `state="stopped"`) {
		t.Errorf("body not state=stopped: %s", bodies[0])
	}
}

// TestSessionRequestFor_OnStopDoesNotClearAfterPlexToPlex covers the
// regression case from round-3 review: handlePlayMedia's flow is
//   1. notifyStoppedTimeline (sync)
//   2. core.StartSession (spawns OnStop goroutine for OLD session)
//   3. rememberPlaySession(NEW)  // overwrites c.lastPlay
//   4. notifyTimeline             // broadcasts NEW
// If the OnStop goroutine wins the race against step 3 with an
// unconditional clearPlaySession, NEW lastPlay is wiped → broker emits
// timelines without media identity. Using clearPlaySessionIfMatches,
// the closure no-ops because by the time it runs c.lastPlay holds the
// NEW session's MediaKey.
func TestSessionRequestFor_OnStopDoesNotClearAfterPlexToPlex(t *testing.T) {
	c := NewCompanion(CompanionConfig{
		DeviceName: "MiSTer", DeviceUUID: "uuid-1", ProfileName: "Plex Home Theater",
	}, nil)
	// No timeline broker — this test only exercises lastPlay state.

	prior := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-old", PlexToken: "tok",
	}
	c.rememberPlaySession(prior)
	req := c.sessionRequestFor(prior)

	// Simulate handlePlayMedia step 3 happening BEFORE OnStop's clear:
	// remember a NEW session so c.lastPlay no longer matches captured.
	newer := PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/99", TranscodeSessionID: "tsid-new", PlexToken: "tok",
	}
	c.rememberPlaySession(newer)

	// Now fire the prior session's OnStop (simulating goroutine running
	// after rememberPlaySession). With clearPlaySessionIfMatches, this
	// must NOT wipe lastPlay because the captured MediaKey ("/library/
	// metadata/42") no longer matches c.lastPlay.MediaKey ("/library/
	// metadata/99").
	req.OnStop("preempted")

	if got := c.lastPlaySession().MediaKey; got != "/library/metadata/99" {
		t.Errorf("lastPlay MediaKey = %q, want %q (NEW session must survive prior OnStop)",
			got, "/library/metadata/99")
	}
}
```

Add any missing imports to `companion_test.go`'s import block (skip ones already present — companion_test.go imports `testing` and likely several others; be additive, do not duplicate):

```go
"io"
"net"
"net/http"
"net/http/httptest"
"net/url"
"strings"
"sync"

"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/adapters/plex/ -run TestSessionRequestFor_OnStop -v`
Expected:
- `TestSessionRequestFor_OnStopBroadcastsAndClearsForCrossAdapter` FAILS — `lastPlay not cleared` (current OnStop doesn't touch it) and `subscriber pushes = 0` (current OnStop doesn't push).
- `TestSessionRequestFor_OnStopDoesNotClearAfterPlexToPlex` PASSES coincidentally because today's OnStop never touches lastPlay at all — verify it stays passing after step 4 lands.

- [ ] **Step 4: Replace the `OnStop` closure body**

Find the `req.OnStop = func(reason string) { ... }` block in `internal/adapters/plex/companion.go`'s `sessionRequestFor` (currently around lines 144-150 — locate by content match, not absolute line numbers, in case earlier work has shifted things). Replace the entire block with:

```go
// Capture the prior PlayMediaRequest at request-construction time.
// Reading lastPlay or Manager.Status() from inside OnStop is unsafe:
// by the time the goroutine runs, a foreign adapter may have already
// taken over and both will reflect the new session, not this one.
captured := p
req.OnStop = func(reason string) {
	// Order matters: notify subscribed Plex controllers FIRST, then
	// clear local state (conditionally), then make the best-effort PMS
	// hint last. This way the controller sees the stopped state
	// immediately even if PMS is slow/unreachable (StopTranscodeSession
	// has a 5s timeout and we don't want to gate the controller-cleanup
	// latency on it).
	if c.timeline != nil {
		c.timeline.broadcastStoppedFor(core.SessionStatus{State: core.StateIdle}, captured)
	}
	// Conditional clear: only wipe lastPlay if it still references THIS
	// session. handlePlayMedia's Plex→Plex flow may have already called
	// rememberPlaySession(NEW) before this goroutine runs; an
	// unconditional clear would silently break that flow's metadata.
	c.clearPlaySessionIfMatches(captured.MediaKey)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := StopTranscodeSession(ctx, serverURL, captured.TranscodeSessionID, captured.PlexToken); err != nil {
		slog.Debug("plex stop transcode", "reason", reason, "session", captured.TranscodeSessionID, "err", err)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/adapters/plex/ -run TestSessionRequestFor_OnStop -v`
Expected: PASS for both tests.

- [ ] **Step 6: Run the full Plex test suite to confirm no regressions**

Run: `go test ./internal/adapters/plex/`
Expected: PASS. (Existing `notifyStoppedTimeline` callers — `handlePlayMedia`, `handleStop` — still work; they call the older path which uses the broker's `playContext`, which is correct because in those code paths the Companion's `lastPlay` is the live truth.)

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/plex/companion.go internal/adapters/plex/companion_test.go
git commit -m "fix(plex): OnStop clears lastPlay and broadcasts stopped timeline"
```

---

## Phase B — URL adapter package

### Task B1: Create `internal/adapters/url/config.go`

**Files:**
- Create: `internal/adapters/url/config.go`
- Create: `internal/adapters/url/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/url/config_test.go`:

```go
package url

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDefaultConfig_Disabled(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Error("DefaultConfig should be disabled by default (spec §Config schema)")
	}
}

func TestConfig_Validate_EmptyOK(t *testing.T) {
	c := Config{}
	if err := c.Validate(); err != nil {
		t.Errorf("empty config should validate, got %v", err)
	}
}

func TestConfig_Validate_EnabledTrueOK(t *testing.T) {
	c := Config{Enabled: true}
	if err := c.Validate(); err != nil {
		t.Errorf("enabled=true should validate, got %v", err)
	}
}

func TestConfig_TOMLDecode(t *testing.T) {
	raw := `
[adapters.url]
enabled = true
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	if err := meta.PrimitiveDecode(envelope.Adapters["url"], &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !c.Enabled {
		t.Error("Enabled not decoded")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/url/`
Expected: FAIL — `no Go files in internal/adapters/url`.

- [ ] **Step 3: Create `internal/adapters/url/config.go`**

```go
// Package url is the URL-input cast adapter. It accepts an http(s) media
// URL via POST or via the settings UI's "Play URL" form, builds a
// core.SessionRequest, and delegates to core.Manager.StartSession.
//
// Spec: docs/specs/2026-04-25-url-adapter-design.md
//
// The package is intentionally minimal: one Config field (enabled), no
// goroutines, no upstream protocol — its primary purpose is to validate
// the core.Manager / adapters.Adapter abstraction boundary by being
// structurally different from the Plex adapter (cast target). See the
// spec's "Cross-adapter preemption" section for the contract this
// adapter enforces against the rest of the bridge.
package url

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// Config is the [adapters.url] TOML section. Single field in v1.
type Config struct {
	Enabled bool `toml:"enabled"`
}

// DefaultConfig returns the zero-config baseline: disabled. Operators
// must opt in via the settings UI toggle (or by editing the section
// in config.toml).
func DefaultConfig() Config {
	return Config{Enabled: false}
}

// Validate is a no-op in v1 (no range checks needed for a single bool).
// Returns the FieldErrors accumulator pattern for consistency with other
// adapters and to keep the door open for future fields.
func (c *Config) Validate() error {
	var errs adapters.FieldErrors
	return errs.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/url/ -v`
Expected: PASS for all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/url/config.go internal/adapters/url/config_test.go
git commit -m "feat(url): add Config with enabled flag"
```

---

### Task B2: Create `internal/adapters/url/adapter.go` lifecycle

**Files:**
- Create: `internal/adapters/url/adapter.go`
- Create: `internal/adapters/url/adapter_interface_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapters/url/adapter_interface_test.go`:

```go
package url

import (
	"context"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestAdapter_ConformsToInterface(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
}

func TestAdapter_ConformsToValidator(t *testing.T) {
	var _ adapters.Validator = (*Adapter)(nil)
}

func TestAdapter_ConformsToRouteProvider(t *testing.T) {
	var _ adapters.RouteProvider = (*Adapter)(nil)
}

func TestAdapter_Name(t *testing.T) {
	a := New(nil)
	if a.Name() != "url" {
		t.Errorf("Name = %q", a.Name())
	}
}

func TestAdapter_DisplayName(t *testing.T) {
	a := New(nil)
	if a.DisplayName() != "URL" {
		t.Errorf("DisplayName = %q", a.DisplayName())
	}
}

func TestAdapter_Fields_HasEnabled(t *testing.T) {
	a := New(nil)
	fields := a.Fields()
	if len(fields) != 1 || fields[0].Key != "enabled" {
		t.Errorf("Fields = %+v, want single 'enabled' field", fields)
	}
	if fields[0].Kind != adapters.KindBool {
		t.Errorf("Fields[0].Kind = %v, want KindBool", fields[0].Kind)
	}
	if fields[0].ApplyScope != adapters.ScopeHotSwap {
		t.Errorf("Fields[0].ApplyScope = %v, want ScopeHotSwap", fields[0].ApplyScope)
	}
}

func TestAdapter_StatusInitial_Stopped(t *testing.T) {
	a := New(nil)
	if a.Status().State != adapters.StateStopped {
		t.Errorf("initial Status.State = %v, want StateStopped", a.Status().State)
	}
}

func TestAdapter_StartSetsRunning_StopSetsStopped(t *testing.T) {
	a := New(nil)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if a.Status().State != adapters.StateRunning {
		t.Errorf("after Start, State = %v, want StateRunning", a.Status().State)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if a.Status().State != adapters.StateStopped {
		t.Errorf("after Stop, State = %v, want StateStopped", a.Status().State)
	}
}

func TestAdapter_SetEnabled_TogglesIsEnabled(t *testing.T) {
	a := New(nil)
	a.SetEnabled(true)
	if !a.IsEnabled() {
		t.Error("after SetEnabled(true), IsEnabled = false")
	}
	a.SetEnabled(false)
	if a.IsEnabled() {
		t.Error("after SetEnabled(false), IsEnabled = true")
	}
}

func TestAdapter_DecodeConfig_SetsEnabled(t *testing.T) {
	raw := `
[adapters.url]
enabled = true
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, _ := toml.Decode(raw, &envelope)
	a := New(nil)
	if err := a.DecodeConfig(envelope.Adapters["url"], meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if !a.IsEnabled() {
		t.Error("Enabled not propagated")
	}
}

func TestAdapter_ApplyConfig_HotSwap(t *testing.T) {
	raw := `
[adapters.url]
enabled = true
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, _ := toml.Decode(raw, &envelope)
	a := New(nil)
	scope, err := a.ApplyConfig(envelope.Adapters["url"], meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/url/ -run TestAdapter -v`
Expected: FAIL — `New undefined, Adapter undefined`.

- [ ] **Step 3: Implement `internal/adapters/url/adapter.go`**

```go
package url

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// SessionManager is the adapter's narrow view of core.Manager. Declared
// here (rather than importing core.Manager concretely) so play_test.go
// can inject fakes without spinning up a real core. core.Manager
// satisfies this via structural typing.
type SessionManager interface {
	StartSession(core.SessionRequest) error
	Status() core.SessionStatus
}

// Adapter implements adapters.Adapter for the URL-input cast source.
// Spec: docs/specs/2026-04-25-url-adapter-design.md.
//
// Concurrency: all field reads and writes (cfg, state, lastErr,
// stateSince, lastURL) go through mu. Status() and OnStop's mutator
// share the same lock so the panel fragment never observes a torn read.
type Adapter struct {
	core SessionManager

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time
	lastURL    string // last URL handed to StartSession; surfaced in the panel
}

// New constructs a ready-to-Start Adapter. core may be nil for tests
// that don't exercise the play handler.
func New(coreMgr SessionManager) *Adapter {
	return &Adapter{
		core:       coreMgr,
		state:      adapters.StateStopped,
		stateSince: time.Now(),
	}
}

// ---- adapters.Adapter interface ----

func (a *Adapter) Name() string        { return "url" }
func (a *Adapter) DisplayName() string { return "URL" }

func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{
			Key:        "enabled",
			Label:      "Enabled",
			Help:       "Turn the URL adapter on or off. When enabled, the Play URL form below accepts http(s) media URLs.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
		},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("url: decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("url: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

// Start sets state to Running and returns nil. The URL adapter has no
// goroutines or upstream registration to bring up — "running" here means
// "enabled, ready to accept POSTs," not "background work in progress."
// Spec §"Lifecycle".
func (a *Adapter) Start(ctx context.Context) error {
	a.setState(adapters.StateRunning, "")
	return nil
}

// Stop sets state to Stopped and returns nil. Does NOT stop a mid-cast
// URL session — the data plane is owned by core.Manager. To stop a live
// cast, the operator issues a bridge-wide stop or POSTs another URL.
// Spec §"Operational edges / Disable while playing".
func (a *Adapter) Stop() error {
	a.setState(adapters.StateStopped, "")
	return nil
}

func (a *Adapter) Status() adapters.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return adapters.Status{
		State:     a.state,
		LastError: a.lastErr,
		Since:     a.stateSince,
	}
}

// SetEnabled implements ui.EnableSetter. The toggle handler at
// internal/ui/adapter.go:handleAdapterToggle calls this in sync with
// Start/Stop. Without it the toggle endpoint returns 500.
func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Enabled = v
}

// ApplyConfig diffs and applies. With only `enabled` in v1 there's no
// real diff to compute; we just store the new value and return
// ScopeHotSwap. (`enabled` is handled out-of-band by the toggle endpoint
// per the Plex precedent.)
func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	newCfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &newCfg); err != nil {
		return 0, fmt.Errorf("url: decode apply config: %w", err)
	}
	if err := newCfg.Validate(); err != nil {
		a.setState(adapters.StateError, err.Error())
		return 0, err
	}
	a.mu.Lock()
	a.cfg = newCfg
	a.mu.Unlock()
	return adapters.ScopeHotSwap, nil
}

// CurrentValues implements ui.ValueProvider via duck-typing — surfaces
// the current cfg values to the UI for form prefill.
func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"enabled": a.cfg.Enabled}
}

// setState atomically updates state, stateSince, and lastErr.
func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.stateSince = time.Now()
	a.lastErr = errMsg
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/url/ -v`
Expected: PASS for all tests in this task plus the Task B1 tests.

- [ ] **Step 5: Confirm `go vet` is clean**

Run: `go vet ./internal/adapters/url/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/url/adapter.go internal/adapters/url/adapter_interface_test.go
git commit -m "feat(url): add Adapter lifecycle implementing adapters.Adapter"
```

---

### Task B3: Create `internal/adapters/url/play.go` — POST handler + URL validation + redaction

**Files:**
- Create: `internal/adapters/url/play.go`
- Create: `internal/adapters/url/play_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapters/url/play_test.go`:

```go
package url

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// fakeCore captures the most recent StartSession call so tests can
// assert what the adapter passed.
type fakeCore struct {
	mu       sync.Mutex
	lastReq  core.SessionRequest
	startErr error
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastReq = req
	return f.startErr
}
func (f *fakeCore) Status() core.SessionStatus { return core.SessionStatus{} }

func (f *fakeCore) snapshot() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

func TestPlay_RejectsMalformedURL(t *testing.T) {
	a := New(&fakeCore{})
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=not%20a%20valid%20url"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlay_RejectsEmptyURL(t *testing.T) {
	a := New(&fakeCore{})
	req := httptest.NewRequest(http.MethodPost, "/play", strings.NewReader("url="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPlay_RejectsBadScheme(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"rtsp://10.0.0.1/stream",
		"ftp://example.com/v.mp4",
		"javascript:alert(1)",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			fc := &fakeCore{}
			a := New(fc)
			req := httptest.NewRequest(http.MethodPost, "/play",
				strings.NewReader("url="+in))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			a.handlePlay(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if got := fc.snapshot().StreamURL; got != "" {
				t.Errorf("StartSession called despite bad scheme: %q", got)
			}
		})
	}
}

func TestPlay_HappyPath_BuildsSessionRequest(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fvideo.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	got := fc.snapshot()
	if got.StreamURL != "https://example.com/video.mp4" {
		t.Errorf("StreamURL = %q", got.StreamURL)
	}
	if got.Capabilities.CanPause || got.Capabilities.CanSeek {
		t.Errorf("Capabilities should be {false,false}, got %+v", got.Capabilities)
	}
	if got.DirectPlay {
		t.Errorf("DirectPlay should be false in v1")
	}
	if !strings.HasPrefix(got.AdapterRef, "url:") {
		t.Errorf("AdapterRef should start with 'url:', got %q", got.AdapterRef)
	}
	if got.OnStop == nil {
		t.Errorf("OnStop should be set")
	}
}

func TestPlay_StartSessionFailure_500(t *testing.T) {
	fc := &fakeCore{startErr: errors.New("probe failed")}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if a.Status().State != adapters.StateError {
		t.Errorf("State = %v, want StateError", a.Status().State)
	}
}

func TestPlay_HXRequest_RespondsHTML(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "example.com") {
		t.Errorf("response body should mention the URL: %s", w.Body.String())
	}
}

func TestPlay_NoHXRequest_RespondsJSON(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader("url=https%3A%2F%2Fexample.com%2Fv.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"adapter_ref"`) || !strings.Contains(body, `"state":"running"`) {
		t.Errorf("JSON body missing expected keys: %s", body)
	}
}

func TestPlay_AcceptsJSONBody(t *testing.T) {
	fc := &fakeCore{}
	a := New(fc)
	body := `{"url": "https://example.com/v.mp4"}`
	req := httptest.NewRequest(http.MethodPost, "/play", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handlePlay(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if got := fc.snapshot().StreamURL; got != "https://example.com/v.mp4" {
		t.Errorf("StreamURL = %q", got)
	}
}

func TestRedactURL_StripsCredentials(t *testing.T) {
	got := redactURL("https://user:secret@example.com/v.mp4")
	if strings.Contains(got, "secret") {
		t.Errorf("password leaked: %q", got)
	}
	if !strings.Contains(got, "example.com") {
		t.Errorf("host stripped too: %q", got)
	}
}

func TestRedactURL_HandlesUnparseable(t *testing.T) {
	// Even on a parse failure the redactor must not panic and must not
	// echo arbitrary input verbatim.
	got := redactURL("\x00not-a-url")
	if got == "" {
		t.Error("redactURL returned empty for invalid input")
	}
}

func TestOnStop_ReasonHandling(t *testing.T) {
	cases := []struct {
		reason string
		want   adapters.State
	}{
		{"eof", adapters.StateStopped},
		{"preempted", adapters.StateStopped},
		{"stopped", adapters.StateStopped},
		{"", adapters.StateStopped}, // empty treated as eof
		{"error: ffmpeg crashed", adapters.StateError},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			a := New(nil)
			// Pretend a session is running.
			a.setState(adapters.StateRunning, "")
			a.handleOnStop(tc.reason)
			if got := a.Status().State; got != tc.want {
				t.Errorf("after OnStop(%q), State = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

```

The test file imports `errors` for `errors.New` (used by the `startErr` setup in `TestPlay_StartSessionFailure_500`). Add `"errors"` to the import block.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/url/ -run TestPlay -v`
Expected: FAIL — `handlePlay undefined, redactURL undefined, handleOnStop undefined`.

- [ ] **Step 3: Create `internal/adapters/url/play.go`**

The complete file in one block:

```go
package url

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	stdurl "net/url"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// handlePlay is the POST /play endpoint. It accepts form-encoded or JSON
// bodies, validates the URL (scheme must be http or https, must be
// well-formed), builds a fire-and-forget core.SessionRequest, and calls
// core.Manager.StartSession. Response shape switches on HX-Request.
func (a *Adapter) handlePlay(w http.ResponseWriter, r *http.Request) {
	rawURL, err := extractURL(r)
	if err != nil {
		a.respondError(w, r, http.StatusBadRequest, err.Error(), "url")
		return
	}

	parsed, err := stdurl.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		a.respondError(w, r, http.StatusBadRequest, "not a valid URL", "url")
		return
	}
	switch parsed.Scheme {
	case "http", "https":
		// ok
	default:
		a.respondError(w, r, http.StatusBadRequest,
			fmt.Sprintf("scheme not supported in v1: %s (only http and https)", parsed.Scheme),
			"url")
		return
	}

	ref := newAdapterRef()
	req := core.SessionRequest{
		StreamURL:    rawURL,
		Capabilities: core.Capabilities{CanSeek: false, CanPause: false},
		AdapterRef:   ref,
		DirectPlay:   false, // always false in v1; spec §"Known limitations"
		OnStop:       a.handleOnStop,
	}

	if a.core == nil {
		a.respondError(w, r, http.StatusInternalServerError, "core not wired", "")
		return
	}
	if err := a.core.StartSession(req); err != nil {
		// Redact the URL in stored / returned messages — ffprobe and
		// similar errors echo the raw input URL, which may contain
		// user:password credentials. The slog line below also redacts.
		safeMsg := strings.ReplaceAll(err.Error(), rawURL, redactURL(rawURL))
		a.setState(adapters.StateError, safeMsg)
		slog.Warn("url cast failed", "url", redactURL(rawURL), "err", err)
		a.respondError(w, r, http.StatusInternalServerError, safeMsg, "")
		return
	}

	a.markRunning(rawURL)
	slog.Info("url cast started", "url", redactURL(rawURL), "ref", ref)
	a.respondStarted(w, r, ref, rawURL)
}

// extractURL pulls the "url" field from either a form-encoded body or a
// JSON body, distinguished by Content-Type.
func extractURL(r *http.Request) (string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4096))
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		var payload struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", fmt.Errorf("invalid JSON: %w", err)
		}
		if payload.URL == "" {
			return "", fmt.Errorf("url is required")
		}
		return strings.TrimSpace(payload.URL), nil
	}
	if err := r.ParseForm(); err != nil {
		return "", fmt.Errorf("parse form: %w", err)
	}
	v := strings.TrimSpace(r.Form.Get("url"))
	if v == "" {
		return "", fmt.Errorf("url is required")
	}
	return v, nil
}

// handleOnStop is the closure handed to core.Manager via SessionRequest.OnStop.
// Reasons "eof", "preempted", "stopped" (literal Manager.Stop reason at
// manager.go:382), and the empty string (treated as "eof") all transition
// to StateStopped and clear lastError. Any other non-empty reason is an
// error path: state -> StateError, lastError -> reason.
func (a *Adapter) handleOnStop(reason string) {
	switch reason {
	case "eof", "preempted", "stopped", "":
		a.setState(adapters.StateStopped, "")
	default:
		a.setState(adapters.StateError, reason)
	}
	slog.Debug("url session ended", "reason", reason)
}

// markRunning records the active URL and transitions to StateRunning.
func (a *Adapter) markRunning(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = adapters.StateRunning
	a.lastErr = ""
	a.lastURL = url
	a.stateSince = time.Now()
}

// respondError writes a 4xx/5xx response. HX-Request = HTML fragment;
// otherwise JSON.
func (a *Adapter) respondError(w http.ResponseWriter, r *http.Request, code int, msg, field string) {
	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		fmt.Fprintf(w, `<div class="url-panel error" id="url-panel"><p class="err">%s</p></div>`, template.HTMLEscapeString(msg))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	payload := map[string]string{"error": msg}
	if field != "" {
		payload["field"] = field
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// respondStarted writes the 202 success response.
func (a *Adapter) respondStarted(w http.ResponseWriter, r *http.Request, ref, url string) {
	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w,
			`<div class="url-panel" id="url-panel"><p>Playing: <code>%s</code></p></div>`,
			template.HTMLEscapeString(url))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"adapter_ref": ref,
		"state":       "running",
		"url":         url,
	})
}

// isHTMXRequest mirrors internal/ui/server.go's helper. Local copy so the
// adapter doesn't take a UI-package dependency.
func isHTMXRequest(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// newAdapterRef returns "url:<8 hex>". 4 random bytes is plenty of entropy
// for a single-active-session adapter; collisions are inconsequential
// since AdapterRef is opaque to core.
func newAdapterRef() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "url:" + hex.EncodeToString(b[:])
}

// redactURL returns the URL with any user:password authority component
// stripped. Uses url.URL.Redacted() under the hood; on parse failure
// returns "<unparseable url>" rather than echoing arbitrary user input.
func redactURL(raw string) string {
	u, err := stdurl.Parse(raw)
	if err != nil || u == nil {
		return "<unparseable url>"
	}
	return u.Redacted()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/url/ -v`
Expected: PASS for all play tests + the previously passing tests.

- [ ] **Step 5: Run `go vet`**

Run: `go vet ./internal/adapters/url/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/url/play.go internal/adapters/url/play_test.go
git commit -m "feat(url): add POST /play handler with URL validation and redaction"
```

---

### Task B4: Create `internal/adapters/url/routes.go` + `ui.go`

**Files:**
- Create: `internal/adapters/url/routes.go`
- Create: `internal/adapters/url/ui.go`
- Create: `internal/adapters/url/ui_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapters/url/ui_test.go`:

```go
package url

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestUIRoutes_HasPlayAndPanel(t *testing.T) {
	a := New(&fakeCore{})
	routes := a.UIRoutes()
	if len(routes) != 2 {
		t.Fatalf("UIRoutes count = %d, want 2", len(routes))
	}
	have := map[string]string{}
	for _, r := range routes {
		have[r.Method+" "+r.Path] = "ok"
	}
	if _, ok := have["POST play"]; !ok {
		t.Errorf("missing POST play route: %v", have)
	}
	if _, ok := have["GET panel"]; !ok {
		t.Errorf("missing GET panel route: %v", have)
	}
}

func TestPanel_RendersIdle(t *testing.T) {
	a := New(&fakeCore{})
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Idle") {
		t.Errorf("idle panel missing 'Idle' text: %s", body)
	}
	if !strings.Contains(body, `hx-post="/ui/adapter/url/play"`) {
		t.Errorf("panel form should hx-post to /ui/adapter/url/play: %s", body)
	}
}

func TestPanel_RendersPlaying(t *testing.T) {
	a := New(&fakeCore{})
	a.markRunning("https://example.com/video.mp4")
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Playing") {
		t.Errorf("playing panel missing 'Playing' text: %s", body)
	}
	if !strings.Contains(body, "example.com/video.mp4") {
		t.Errorf("playing panel missing URL: %s", body)
	}
}

func TestPanel_RendersError(t *testing.T) {
	a := New(&fakeCore{})
	a.setState(adapters.StateError, "probe failed: connection refused")
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "probe failed") {
		t.Errorf("error panel missing error text: %s", body)
	}
}

func TestExtraPanelHTML_EmbedsPanel(t *testing.T) {
	a := New(&fakeCore{})
	html := string(a.ExtraPanelHTML())
	if !strings.Contains(html, "url-panel") {
		t.Errorf("ExtraPanelHTML should include the panel; got %s", html)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/url/ -run TestUIRoutes -v`
Expected: FAIL — `UIRoutes undefined, handlePanel undefined, ExtraPanelHTML undefined`.

- [ ] **Step 3: Create `internal/adapters/url/routes.go`**

```go
package url

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// UIRoutes returns the adapter-owned HTTP routes. They mount under
// /ui/adapter/url/ courtesy of the UI server's RouteProvider scan.
// POST routes are wrapped in csrfMiddleware by the mounter.
func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: "POST", Path: "play", Handler: a.handlePlay},
		{Method: "GET", Path: "panel", Handler: a.handlePanel},
	}
}
```

- [ ] **Step 4: Create `internal/adapters/url/ui.go`**

```go
package url

import (
	"fmt"
	"html/template"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// handlePanel renders the htmx fragment shown inside the URL adapter
// card on the settings page. Auto-refreshes every 5s so the status
// line catches EOF / preemption without operator action.
func (a *Adapter) handlePanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(a.renderPanel()))
}

// ExtraPanelHTML implements ui.ExtraHTMLProvider — the UI adapter
// template inserts whatever this returns below the standard form.
// Using it lets us embed the URL play form without changing the
// generic adapter-panel template.
func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel())
}

// renderPanel produces the panel fragment. Includes:
//   - status line (Idle / Playing: <url> / Error)
//   - text input bound to POST /ui/adapter/url/play via htmx
//   - hx-trigger="every 5s" self-refresh on the outer container so the
//     status reflects EOF/preempt without operator action
//
// Markup is intentionally minimal — no CSS framework dependencies; it
// inherits the bridge's app.css naming.
func (a *Adapter) renderPanel() string {
	a.mu.Lock()
	state := a.state
	lastURL := a.lastURL
	lastErr := a.lastErr
	a.mu.Unlock()

	status := `<p class="status">Idle</p>`
	switch state {
	case adapters.StateRunning:
		if lastURL != "" {
			status = fmt.Sprintf(`<p class="status run">Playing: <code>%s</code></p>`,
				template.HTMLEscapeString(redactURL(lastURL)))
		} else {
			status = `<p class="status run">Running</p>`
		}
	case adapters.StateError:
		status = fmt.Sprintf(`<p class="status err">Error: %s</p>`, template.HTMLEscapeString(lastErr))
	}

	return fmt.Sprintf(`<section class="url-panel" id="url-panel" hx-get="/ui/adapter/url/panel" hx-trigger="every 5s" hx-swap="outerHTML">
  <h3>Play URL</h3>
  %s
  <form hx-post="/ui/adapter/url/play" hx-target="#url-panel" hx-swap="outerHTML">
    <input type="url" name="url" placeholder="https://example.com/video.mp4" required>
    <button type="submit">Play</button>
  </form>
</section>`, status)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/adapters/url/ -v`
Expected: PASS for all url-package tests.

- [ ] **Step 6: Run `go vet`**

Run: `go vet ./internal/adapters/url/`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/url/routes.go internal/adapters/url/ui.go internal/adapters/url/ui_test.go
git commit -m "feat(url): add UIRoutes (POST /play, GET /panel) and panel fragment"
```

---

## Phase C — main.go wiring

### Task C1: Register the URL adapter in `main.go`

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go` (around line 117 after Plex registration)

- [ ] **Step 1: Add the import and registration**

In `cmd/mister-groovy-relay/main.go`, add to the import block:

```go
urladapter "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
```

(The `urladapter` alias avoids a name clash with the stdlib `net/url` package, even though we don't import `net/url` here today — the alias future-proofs against that and improves readability at the call site.)

Then, immediately after the Plex registration block (after the `if err := reg.Register(plexAdapter); err != nil { ... }` block ending around line 115), insert:

```go
	// URL adapter (v1): minimum-viable HTTP/HTTPS URL acceptor.
	// Spec: docs/specs/2026-04-25-url-adapter-design.md.
	// No constructor failure modes — New() never errors.
	urlAdapter := urladapter.New(coreMgr)
	if err := reg.Register(urlAdapter); err != nil {
		slog.Error("registry register url", "err", err)
		os.Exit(1)
	}
```

- [ ] **Step 2: Update the embedded `internal/config/example.toml`**

This file is embedded into the binary and seeded into the operator's
`data_dir` on first run (`internal/config/example.go:39-47`). Append a
URL adapter stub at the end of the file so first-run users see the
section and know it exists:

```toml

[adapters.url]
enabled = false                   # Set to true (or use the UI toggle) to accept POST URL casts
```

(Note the leading blank line — keeps section spacing consistent with the existing `[adapters.plex]` block above.)

- [ ] **Step 3: Build to verify the wiring compiles**

Run: `make build-bridge` (or `go build ./cmd/mister-groovy-relay/`)
Expected: PASS.

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Run with race detector**

Run: `go test -race ./internal/adapters/url/ ./internal/adapters/plex/`
Expected: PASS, no data race reports.

- [ ] **Step 6: Commit**

```bash
git add cmd/mister-groovy-relay/main.go internal/config/example.toml
git commit -m "feat(main): register url adapter and add example.toml section"
```

---

## Phase D — Integration tests

### Task D1: `TestRegistry_AcceptsAdapterWithNoBackgroundWork`

**Why:** This is the spec's primary abstraction probe. The URL adapter's `Start` returns nil and spawns no goroutines; this test proves the registry + UI mounting code don't assume every adapter has background work.

**Files:**
- Create: `internal/adapters/url/registry_test.go` (this can be a regular unit test, not integration-tagged — no fake-mister needed)

- [ ] **Step 1: Write the test**

Create `internal/adapters/url/registry_test.go`:

```go
package url

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// TestRegistry_AcceptsAdapterWithNoBackgroundWork is the spec's primary
// abstraction probe (spec §"Boundary-validation tests"). The URL
// adapter's Start returns nil and spawns no goroutines; this test
// verifies adapters.Registry, the lifecycle dance, and the UIRoutes
// mounting path all tolerate that — proving the abstraction does not
// secretly assume every adapter has background work.
func TestRegistry_AcceptsAdapterWithNoBackgroundWork(t *testing.T) {
	reg := adapters.NewRegistry()
	a := New(&fakeCore{})
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Lifecycle: Start must succeed; Status must reflect it; Stop must
	// succeed; Status reflects that too.
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := a.Status().State; got != adapters.StateRunning {
		t.Errorf("post-Start State = %v, want StateRunning", got)
	}

	// UIRoutes wired via the same loop the UI server uses (server.go:
	// "for _, a := range Registry.List(); rp, ok := a.(RouteProvider)").
	mounted := 0
	mux := http.NewServeMux()
	for _, listed := range reg.List() {
		rp, ok := listed.(adapters.RouteProvider)
		if !ok {
			continue
		}
		for _, r := range rp.UIRoutes() {
			pattern := "/ui/adapter/" + listed.Name() + "/" + r.Path
			switch r.Method {
			case "GET":
				mux.HandleFunc("GET "+pattern, r.Handler)
			case "POST":
				mux.HandleFunc("POST "+pattern, r.Handler)
			}
			mounted++
		}
	}
	if mounted != 2 {
		t.Errorf("mounted %d url routes, want 2", mounted)
	}

	// Sanity-check the GET /panel route is reachable via the mux.
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/ui/adapter/url/panel")
	if err != nil {
		t.Fatalf("GET /panel: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/panel status = %d, want 200", resp.StatusCode)
	}

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := a.Status().State; got != adapters.StateStopped {
		t.Errorf("post-Stop State = %v, want StateStopped", got)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/adapters/url/ -run TestRegistry_AcceptsAdapterWithNoBackgroundWork -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/url/registry_test.go
git commit -m "test(url): add registry boundary-validation test for no-background-work adapter"
```

---

### Task D2: Integration test — `TestURL_PlayDirectFile` + `TestURL_RejectsBadScheme` + `TestURL_ProbeTimeout`

**Files:**
- Create: `tests/integration/url_test.go`
- Create: `tests/integration/testdata/url/tiny.mp4` (small valid MP4 — see Step 1)

- [ ] **Step 1: Generate a tiny test MP4**

The test needs a small, valid MP4 to serve via `httptest.Server`. Reuse any existing fixture if one is already in `tests/integration/testdata/`. Otherwise, generate one:

```bash
mkdir -p tests/integration/testdata/url
ffmpeg -y -f lavfi -i color=c=blue:s=320x240:d=1:r=30 \
  -f lavfi -i sine=frequency=1000:duration=1 \
  -c:v libx264 -preset ultrafast -tune zerolatency -pix_fmt yuv420p \
  -c:a aac -b:a 64k -movflags +faststart \
  tests/integration/testdata/url/tiny.mp4
ls -la tests/integration/testdata/url/tiny.mp4
```

Expected: file exists, ~30–80 KB.

- [ ] **Step 2: Write the integration tests**

Create `tests/integration/url_test.go`:

```go
//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	urladapter "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// urlBridgeConfig returns a minimal, valid bridge config the Manager can
// run with. Aspect "letterbox" skips ProbeCrop so we don't depend on
// ffmpeg's cropdetect for the smoke test.
func urlBridgeConfig(t *testing.T) config.BridgeConfig {
	t.Helper()
	return config.BridgeConfig{
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			RGBMode:             "rgb888",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			LZ4Enabled:          false,
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
	}
}

// TestURL_PlayDirectFile spins up an httptest.Server serving the tiny
// MP4 fixture, posts the URL through the URL adapter, and asserts that
// the data plane initialises against fake-mister (Init + Switchres
// observed) before the short clip ends.
func TestURL_PlayDirectFile(t *testing.T) {
	h := NewHarness(t)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)

	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(mp4Path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(srv.Close)

	a := urladapter.New(mgr)
	form := url.Values{"url": {srv.URL + "/tiny.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("/play status = %d, want 202: body=%s", w.Code, w.Body.String())
	}

	// Wait up to 5s for at least one Init + one Switchres on the wire.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := h.Recorder.Snapshot()
		if snap.Counts[groovy.CmdInit] >= 1 && snap.Counts[groovy.CmdSwitchres] >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] < 1 {
		t.Errorf("Init count = %d, want >= 1", snap.Counts[groovy.CmdInit])
	}
	if snap.Counts[groovy.CmdSwitchres] < 1 {
		t.Errorf("Switchres count = %d, want >= 1", snap.Counts[groovy.CmdSwitchres])
	}

	// Stop the manager so the plane goroutine exits before the test ends.
	_ = mgr.Stop()
}

// TestURL_RejectsBadScheme: posting a file:// URL yields 400 and never
// reaches the data plane.
func TestURL_RejectsBadScheme(t *testing.T) {
	h := NewHarness(t)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)
	a := urladapter.New(mgr)

	form := url.Values{"url": {"file:///etc/passwd"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	// 100ms is plenty for any spurious wire activity to surface.
	time.Sleep(100 * time.Millisecond)
	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] != 0 {
		t.Errorf("Init count = %d, want 0 (no plane should have started)", snap.Counts[groovy.CmdInit])
	}
}

// TestURL_ProbeTimeout: an httptest.Server whose handler hangs longer
// than the Manager's 10s probe ceiling should yield a 500 within
// ~2 * probeTimeout. Asserts the timeout actually fires AND no plane
// starts.
func TestURL_ProbeTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("ProbeTimeout test takes ~12s; skipping in -short mode")
	}
	h := NewHarness(t)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)
	a := urladapter.New(mgr)

	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the test ends — well past the 10s probe ceiling.
		select {
		case <-r.Context().Done():
		case <-hang:
		}
	}))
	t.Cleanup(srv.Close)

	form := url.Values{"url": {srv.URL + "/forever.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	start := time.Now()
	// Run handler in a goroutine with a context-bounded wait so a bug
	// that lets the handler hang forever still fails the test.
	done := make(chan struct{})
	go func() {
		a.UIRoutes()[0].Handler(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("handler did not return within 20s — probe timeout broken?")
	}
	elapsed := time.Since(start)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if elapsed < 8*time.Second {
		t.Errorf("handler returned in %v — probe timeout too short?", elapsed)
	}
	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] != 0 {
		t.Errorf("Init count = %d, want 0 (no plane should have started)", snap.Counts[groovy.CmdInit])
	}
}
```

- [ ] **Step 3: Run the integration tests**

Run: `go test -tags=integration ./tests/integration/ -run TestURL -v`
Expected: PASS for all three (PlayDirectFile within ~5s, RejectsBadScheme fast, ProbeTimeout ~12s).

- [ ] **Step 4: Commit**

```bash
git add tests/integration/url_test.go tests/integration/testdata/url/tiny.mp4
git commit -m "test(url): add integration tests for play, bad-scheme reject, probe timeout"
```

---

### Task D3: Integration test — `TestURL_PreemptsPlex_TimelineReportsStopped` + `TestURL_CapCheckRejectsCrossAdapterPause`

**Why:** These are the spec's two remaining boundary-validation tests. They exercise the full cross-adapter contract: URL preempts Plex, the broker emits a stopped timeline addressed to the prior media key, and any subsequent Plex pause/seek calls hit the cap-check.

**Files:**
- Append to: `tests/integration/url_test.go`

- [ ] **Step 1: Add the cross-adapter tests**

Append to `tests/integration/url_test.go`:

```go
// TestURL_PreemptsPlex_TimelineReportsStopped is the C1 contract test
// from the spec (§"Plex precursor"). It:
//   1. Stands up a fake controller HTTP endpoint and subscribes it to
//      the Plex timeline broker.
//   2. Starts a "Plex" session by directly calling Manager.StartSession
//      with a request whose OnStop is the same closure sessionRequestFor
//      builds (so we exercise the broadcast-on-stop wiring).
//   3. POSTs a URL to the URL adapter, which preempts.
//   4. Asserts the controller received a stopped timeline addressed to
//      the prior media key during the preempt window.
func TestURL_PreemptsPlex_TimelineReportsStopped(t *testing.T) {
	h := NewHarness(t)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)

	// Controller endpoint — collects timeline POSTs.
	var mu sync.Mutex
	var bodies []string
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
	}))
	t.Cleanup(ctrl.Close)
	cu, _ := url.Parse(ctrl.URL)
	chost, cport, _ := net.SplitHostPort(cu.Host)

	// Build a real Plex Companion + TimelineBroker pointed at the manager.
	companion := plex.NewCompanion(plex.CompanionConfig{
		DeviceName: "MiSTer", DeviceUUID: "uuid-1", ProfileName: "Plex Home Theater",
	}, mgr)
	broker := plex.NewTimelineBroker(plex.TimelineConfig{DeviceUUID: "uuid-1", DeviceName: "MiSTer"},
		mgr.Status)
	broker.SetPlayContextProvider(companion.LastPlaySessionForTest) // exposed test helper, see Step 2
	companion.SetTimeline(broker)
	broker.Subscribe("client-a", chost, cport, "http", 0)

	// MP4 server reused from D2's harness pattern.
	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _ := os.Open(mp4Path)
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(srv.Close)

	// Fake "Plex play" — call sessionRequestFor through Companion's
	// exported test entry point, then StartSession. Port "1" matches
	// the unit test in A2: guaranteed-unreachable across platforms so
	// StopTranscodeSession's TCP connect fails fast (refused), avoiding
	// dependence on whether a real PMS happens to be running on the
	// developer's box.
	priorPlay := plex.PlayMediaRequest{
		PlexServerAddress: "127.0.0.1", PlexServerPort: "1", PlexServerScheme: "http",
		MediaKey: "/library/metadata/42", TranscodeSessionID: "tsid-1", PlexToken: "tok",
	}
	plexReq := companion.SessionRequestForTest(priorPlay) // exposed test helper, see Step 2
	// Override StreamURL to the local MP4 — we don't want the test
	// reaching out to a real PMS.
	plexReq.StreamURL = srv.URL + "/tiny.mp4"
	companion.RememberPlaySessionForTest(priorPlay) // exposed test helper, see Step 2
	if err := mgr.StartSession(plexReq); err != nil {
		t.Fatalf("plex StartSession: %v", err)
	}

	// Drain any baseline pushes from broker startup. The broker's
	// 1Hz tick is NOT running in this test (no RunBroadcastLoop), so
	// there shouldn't be any baseline pushes — but reset bodies
	// defensively in case a future change adds startup notifications.
	mu.Lock()
	bodies = nil
	mu.Unlock()

	// URL preempts.
	urlAdapter := urladapter.New(mgr)
	form := url.Values{"url": {srv.URL + "/tiny.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	urlAdapter.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("url /play status = %d, want 202: %s", w.Code, w.Body.String())
	}

	// notifySessionStop fires Plex's OnStop in a goroutine
	// (manager.go:38-43). Poll for the stopped-timeline push instead
	// of using a fixed sleep — Windows CI in particular can have
	// variable connection-refused latency for the inner
	// StopTranscodeSession call.
	deadline := time.Now().Add(5 * time.Second)
	stoppedCount := 0
	for time.Now().Before(deadline) {
		mu.Lock()
		stoppedCount = 0
		for _, b := range bodies {
			if strings.Contains(b, `state="stopped"`) && strings.Contains(b, `key="/library/metadata/42"`) {
				stoppedCount++
			}
		}
		mu.Unlock()
		if stoppedCount >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stoppedCount != 1 {
		mu.Lock()
		bodiesSnapshot := append([]string(nil), bodies...)
		mu.Unlock()
		t.Errorf("stopped-timeline-for-prior-key count = %d during preempt window, want 1; bodies: %v",
			stoppedCount, bodiesSnapshot)
	}

	_ = mgr.Stop()
}

// TestURL_CapCheckRejectsCrossAdapterPause: with a URL session active,
// calling Manager.Pause directly returns the cap-check error. Proves
// the cap check is the wall, not the data plane (spec §"Boundary-
// validation tests").
func TestURL_CapCheckRejectsCrossAdapterPause(t *testing.T) {
	h := NewHarness(t)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)

	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _ := os.Open(mp4Path)
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(srv.Close)

	a := urladapter.New(mgr)
	form := url.Values{"url": {srv.URL + "/tiny.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/play",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.UIRoutes()[0].Handler(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}

	// The URL session is now active. Pause must be rejected.
	if err := mgr.Pause(); err == nil {
		t.Error("Manager.Pause returned nil for URL session; cap-check failed")
	} else if !strings.Contains(err.Error(), "pause") {
		t.Errorf("Pause error = %q, want a 'pause' message", err)
	}
	// Same for SeekTo.
	if err := mgr.SeekTo(5000); err == nil {
		t.Error("Manager.SeekTo returned nil for URL session; cap-check failed")
	} else if !strings.Contains(err.Error(), "seek") {
		t.Errorf("SeekTo error = %q, want a 'seek' message", err)
	}

	_ = mgr.Stop()
}
```

Add the missing imports to the existing import block of `tests/integration/url_test.go`:

```go
"net"
"sync"

"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
```

- [ ] **Step 2: Expose the three test helpers from the Plex package**

The cross-adapter test needs access to private Companion methods (`lastPlaySession`, `sessionRequestFor`, `rememberPlaySession`). Add a small `companion_export_test.go` in the Plex package — but it must be a regular file (NOT `_test.go`) because the integration test is in a different package. Use exported `*ForTest` wrappers gated by a doc comment.

Create `internal/adapters/plex/companion_test_export.go`. (Note the filename — does NOT end in `_test.go` because the consumer lives in a different package; a `_test.go` file is package-private.)

```go
package plex

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/core"

// LastPlaySessionForTest is an exported alias for lastPlaySession used
// by cross-package integration tests (tests/integration/url_test.go).
// Production code uses the lowercase form.
func (c *Companion) LastPlaySessionForTest() PlayMediaRequest {
	return c.lastPlaySession()
}

// SessionRequestForTest is an exported alias for sessionRequestFor used
// by cross-package integration tests. Returns core.SessionRequest
// directly so the test can override StreamURL or other fields before
// passing to Manager.StartSession.
func (c *Companion) SessionRequestForTest(p PlayMediaRequest) core.SessionRequest {
	return c.sessionRequestFor(p)
}

// RememberPlaySessionForTest is an exported alias for rememberPlaySession.
func (c *Companion) RememberPlaySessionForTest(p PlayMediaRequest) {
	c.rememberPlaySession(p)
}
```

- [ ] **Step 3: Run the cross-adapter integration tests**

Run: `go test -tags=integration ./tests/integration/ -run "TestURL_PreemptsPlex|TestURL_CapCheckRejects" -v`
Expected: PASS for both.

- [ ] **Step 4: Run the entire integration suite to confirm no regressions**

Run: `go test -tags=integration ./tests/integration/`
Expected: PASS.

- [ ] **Step 5: Run the full test suite with race detector**

Run: `go test -race ./...`
Expected: PASS, no race reports.

- [ ] **Step 6: Run `make lint` and `make test`**

Run: `make lint && make test && make test-integration`
Expected: PASS for all three.

- [ ] **Step 7: Commit**

```bash
git add tests/integration/url_test.go internal/adapters/plex/companion_test_export.go
git commit -m "test(url): add cross-adapter preempt and cap-check boundary tests"
```

---

## Verification checklist (run before declaring done)

- [ ] `go vet ./...` — no output
- [ ] `go test ./...` — PASS
- [ ] `go test -race ./...` — PASS
- [ ] `go test -tags=integration ./tests/integration/...` — PASS
- [ ] `make build` — both binaries build
- [ ] Manual smoke: start the bridge, open `/ui/adapter/url`, toggle enabled, paste a working HTTPS MP4 URL, observe playback start on fake-mister (or real MiSTer)
- [ ] Spot-check `git log --oneline` shows one commit per task with `feat(url):` / `feat(plex):` / `fix(plex):` / `test(url):` prefixes matching the project's existing convention

---

## Notes for the implementer

- **Why Phase A first.** The URL adapter's preempt test (Phase D3) asserts the broker emits a stopped timeline addressed to the prior Plex media key. That contract only holds once `OnStop` is wired to call `broadcastStoppedFor` (Phase A). Inverting the order leaves D3 failing for a reason that has nothing to do with the URL adapter's own correctness.
- **Don't add a `crop_detect` field.** The spec deferred this; the bridge-level `aspect_mode = "auto"` already governs crop detection for both Plex and URL casts.
- **Don't add `looksLikeManifest`.** Set `DirectPlay: false` always in v1 (spec §"Known limitations"). With `SeekOffsetMs = 0`, the value has no observable effect on the data plane.
- **HX-Request, not Content-Type.** The discriminator for HTML-vs-JSON response is `HX-Request: true`. A `curl` user gets JSON regardless of their `-H "Content-Type"` choice.
- **500, not 502.** All `Manager.StartSession` failure modes return 500 (spec §"HTTP surface"). Validation failures (bad URL / scheme) return 400.
- **`testing.Short()` skip on `TestURL_ProbeTimeout`.** The test takes ~12 s; gating it on `-short` keeps `go test ./...` fast for incremental loops.
- **`companion_test_export.go` ships in the production binary.** This is a new project precedent — the existing pattern (e.g., `subscriberCount` on `TimelineBroker`) works because the existing tests live in `package plex` and can call lowercase methods. The cross-package integration test cannot, hence the `*ForTest` wrappers in a non-test file. If this convention is undesirable, the alternative is to put the cross-adapter test in `package plex` (in a new `internal/adapters/plex/cross_adapter_test.go`) and import the URL adapter from there — heavier, but no production-shipped helpers.
- **`OnStop` ordering is broadcast → clearPlaySessionIfMatches → StopTranscodeSession.** This decouples controller-cleanup latency from PMS reachability: the controller learns the session ended within one HTTP RTT to itself, regardless of PMS slowness. The CAS clear (only if `lastPlay.MediaKey` still matches captured) avoids a race against `handlePlayMedia`'s Plex→Plex flow. Don't rearrange and don't make the clear unconditional.
- **`POST /ui/adapter/url/play` is gated by CSRF middleware** (`internal/ui/csrf.go`). htmx automatically sets `Sec-Fetch-Site` so the panel form works without ceremony. `curl` users must include `-H "Origin: http://<bridge-host>:<port>"` (matching the Host header) or the request gets a 403. Document this in the operator-facing README when shipping. The "JSON for curl" response branch is real, but reaching it requires the Origin header.
