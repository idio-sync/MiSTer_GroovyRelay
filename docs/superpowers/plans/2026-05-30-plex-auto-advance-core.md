# Plex Auto-Advance (Continuous Play) — Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a Plex item finishes playing (clean ffmpeg EOF) and a persisted `auto_advance` toggle is on, the bridge autonomously plays the next item in the Plex play queue — covering playlists, albums, TV "up next," and artist radio with one mechanism.

**Architecture:** Entirely inside the Plex adapter (`internal/adapters/plex/`) plus a one-line `core` interface surface. The existing per-request `OnStop(reason)` closure is the hook: we wrap it so a clean `"eof"` triggers a background goroutine that fetches the play queue, resolves the next item, and starts it via `core.Manager.StartSessionIfIdle` — a guard that makes the start race-safe against a still-connected Plex controller. The toggle is a per-adapter `[adapters.plex].auto_advance` bool mirrored into the running `Companion` as an `atomic.Bool`.

**Tech Stack:** Go 1.26, BurntSushi/toml, `sync/atomic`, `net/http`, `httptest` for hermetic tests. Build/test via `make build` / `go test ./internal/adapters/plex/...`.

**Scope note — this is Plan 1 of 2.** This plan delivers the *complete, working, testable* feature: the toggle persists in config, is editable through the chassis settings drawer (which auto-renders any adapter's `Fields()` via the existing `/receiver/settings/adapter/{name}` handler), and auto-advance fully works. **Plan 2** (written separately) adds the bespoke transport-row **CONTINUOUS** button and the VFD **AUTO** indicator described in the spec's Component 3 — pure presentation polish on the mid-build chassis, depending on this plan but not required for the feature to function.

**Spec:** `docs/superpowers/specs/2026-05-29-plex-auto-advance-queue-design.md`

---

## Background the implementer needs

Read these before starting; the tasks reference them by line:

- **`internal/adapters/plex/companion.go`**
  - `Companion` struct (line 79) holds `cfg CompanionConfig`, `core SessionManager`, the `maxVideoBitrateKbps atomic.Int64` mirror (line 88), and `sessMu`/`lastPlay` session bookkeeping.
  - `SessionManager` interface (line 98) — the adapter's narrow view of `core.Manager`. We add one method here.
  - `NewCompanion` (line 114) seeds the atomic mirrors. `SetMaxVideoBitrateKbps` (line 124) is the live-update pattern to copy.
  - `musicSessionRequestForPlay` (line 351) and `sessionRequestForPreset` (line 479) are the **two** functions that build a `core.SessionRequest` and attach `req.OnStop`. Every play (initial cast via `handlePlayMedia`, skip-next via `restartFromPlayQueueItem`, and our auto-advance) flows through one of these two. They are the wrap points.
  - `sessionRequestForPlay` (line 449) is the dispatcher that picks music vs video; auto-advance uses it to build the next request.
  - `fetchPlayQueue` (line 546), `nextPlayQueueItem` (line 584), `restartFromPlayQueueItem` (line 628) — existing queue plumbing. `restartFromPlayQueueItem` is HTTP-handler-shaped (`http.ResponseWriter`, unconditional `StartSession`); we extract its pure middle into a reusable helper.
  - `PlayMediaRequest` (line 863) carries `ContainerKey`, `PlayQueueItemID`, `PlayQueueID`, `PlayQueueVersion`, `MediaKey`, `MediaType`, `PlexServerScheme`/`Address`/`Port`, `PlexToken`, `OffsetMs`, `TranscodeSessionID`, `CommandID`, `Title`.
  - `rememberPlaySession` (line 1662) / `lastPlaySession` (line 1699) / `clearPlaySessionIfMatches` (line 1683) — `c.lastPlay` is a **single shared field**; it is unsafe to read from inside `OnStop` (a successor cast may have overwritten it). Auto-advance closes over a captured copy instead.
  - `NewTranscodeSessionID` is in `transcode.go:82`; `firstNonEmpty` (line 310) and `queryOrHeader` (line 1644) are helpers.
- **`internal/core/manager.go`**
  - `StartSession` (line 859) and `StartSessionIfIdle` (line 906) both run `probeForStart` (ffprobe) with `m.mu` **not** held — preserving the "`Manager.mu` is never held across network I/O" invariant. `StartSessionIfIdle` starts the request **only if no session is active**, checked under `m.mu`; returns `(started bool, err error)`.
- **`internal/adapters/plex/adapter.go`**
  - `Config` is in `config.go`. `Fields()` (line 328), `scopeForPlexField` (line 554), `diffPlexConfig` (line 518), `CurrentValues` (line 577), `ApplyConfig` (line 481, which calls `companion.SetMaxVideoBitrateKbps`), and `ensureFinalized` (line 280, which builds `CompanionConfig`).
- **`internal/adapters/plex/companion_test.go`** — `package plex` (internal test). `fakeCore` (line 228) is the `SessionManager` test double. Tests construct `NewCompanion(CompanionConfig{...}, fc)`.

**Critical correctness facts (do not violate):**

1. **Only `reason == "eof"` advances.** User stop and preempt/seek use other reasons; they must never trigger an advance. (`core` sets `"eof"` on clean ffmpeg exit — see `manager.go` handlePlaneExit.)
2. **Wrap both builders, not the dispatcher.** `handlePlayMedia` inlines the music/video dispatch (it does **not** call `sessionRequestForPlay`), so wrapping only the dispatcher would miss the *initial* cast and the feature would never fire on item 1. Wrap `musicSessionRequestForPlay` and `sessionRequestForPreset` directly — every construction site, every media type.
3. **Capture, never re-read.** The wrapped `OnStop` closes over the item's `PlayMediaRequest`; it must not read `c.lastPlay`.
4. **The guard, not timing, prevents double-advance.** Always start via `StartSessionIfIdle`. The settle delay is smoothness only.
5. **No runaway loop.** End-of-queue, unplayable next item, or fetch failure all stop quietly. We never skip past a failed item.

---

## File structure

- **Create:** `internal/adapters/plex/autoadvance.go` — the auto-advance unit: `withAutoAdvance` wrapper, `advanceAfterEOF` goroutine, `resolveNextQueueItem` shared helper, `errNoNextQueueItem` sentinel, `autoAdvanceSettleDelay` const, the EOF-reason const.
- **Create:** `internal/adapters/plex/autoadvance_test.go` — hermetic unit tests (httptest play-queue server + `fakeCore`).
- **Modify:** `internal/adapters/plex/config.go` — `Config.AutoAdvance` field + `DefaultConfig`.
- **Modify:** `internal/adapters/plex/adapter.go` — `Fields()`, `scopeForPlexField`, `diffPlexConfig`, `CurrentValues`, `ApplyConfig` (mirror push), `ensureFinalized` (pass-through).
- **Modify:** `internal/adapters/plex/companion.go` — `CompanionConfig.AutoAdvance`, `Companion.autoAdvance`/`autoAdvanceDelay` fields, `NewCompanion` seeding, `SetAutoAdvance`, `SessionManager` interface gains `StartSessionIfIdle`, wrap calls in both builders, refactor `restartFromPlayQueueItem` to use `resolveNextQueueItem`.
- **Modify:** `internal/adapters/plex/companion_test.go` — `fakeCore` implements `StartSessionIfIdle`.

**Import-snippet rule:** every later snippet that adds imports to
`autoadvance_test.go` means "merge these names into the single top-level import
block." Never paste an `import` declaration after functions. By Task 4 the test
file's import block should include `context`, `errors`, `net/http`,
`net/http/httptest`, `net/url`, `strings`, `testing`, `time`,
`internal/adapters`, and `internal/core` as needed.

---

## Task 1: Config field + Companion live mirror

Adds the persisted `auto_advance` toggle end-to-end through config, the adapter schema, and the live `Companion` mirror — no advance behavior yet.

**Files:**
- Modify: `internal/adapters/plex/config.go`
- Modify: `internal/adapters/plex/companion.go` (CompanionConfig, Companion struct, NewCompanion, SetAutoAdvance)
- Modify: `internal/adapters/plex/adapter.go` (Fields, scopeForPlexField, diffPlexConfig, CurrentValues, ApplyConfig, ensureFinalized)
- Create/Test: `internal/adapters/plex/autoadvance_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/adapters/plex/autoadvance_test.go`:

```go
package plex

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestAutoAdvance_ConfigDefaultsOff(t *testing.T) {
	if DefaultConfig().AutoAdvance {
		t.Fatal("auto_advance must default to false")
	}
}

func TestAutoAdvance_CurrentValuesIncludesKey(t *testing.T) {
	a := &Adapter{}
	a.plexCfg = Config{AutoAdvance: true}
	got := a.CurrentValues()["auto_advance"]
	if got != true {
		t.Fatalf("CurrentValues[auto_advance] = %v, want true", got)
	}
}

func TestAutoAdvance_ScopeIsHotSwap(t *testing.T) {
	if scopeForPlexField("auto_advance") != adaptersScopeHotSwapForTest() {
		t.Fatalf("auto_advance scope = %v, want ScopeHotSwap", scopeForPlexField("auto_advance"))
	}
}

func TestAutoAdvance_DiffDetectsChange(t *testing.T) {
	old := Config{AutoAdvance: false}
	neu := Config{AutoAdvance: true}
	changed := diffPlexConfig(old, neu)
	found := false
	for _, k := range changed {
		if k == "auto_advance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diffPlexConfig did not report auto_advance change: %v", changed)
	}
}

func TestAutoAdvance_CompanionMirrorSeedsAndUpdates(t *testing.T) {
	c := NewCompanion(CompanionConfig{AutoAdvance: true}, nil)
	if !c.autoAdvance.Load() {
		t.Fatal("NewCompanion did not seed autoAdvance from CompanionConfig")
	}
	c.SetAutoAdvance(false)
	if c.autoAdvance.Load() {
		t.Fatal("SetAutoAdvance(false) did not update the mirror")
	}
}

func adaptersScopeHotSwapForTest() adapters.ApplyScope { return adapters.ScopeHotSwap }
```

- [ ] **Step 2: Run the test to verify it fails to compile**

Run: `go test ./internal/adapters/plex/ -run TestAutoAdvance_ -v`
Expected: BUILD FAILS — `Config` has no field `AutoAdvance`, `Companion` has no `autoAdvance`/`SetAutoAdvance`.

- [ ] **Step 3: Add the config field.** In `internal/adapters/plex/config.go`, add to the `Config` struct (after `MaxVideoBitrateKbps`, line 19):

```go
	MaxVideoBitrateKbps int    `toml:"max_video_bitrate_kbps"`
	AutoAdvance         bool   `toml:"auto_advance"`
```

`DefaultConfig` needs no change (zero value `false` is the desired default), but add an explicit line for clarity inside the returned literal (after `MaxVideoBitrateKbps: 1500,`):

```go
		MaxVideoBitrateKbps: 1500,
		AutoAdvance:         false,
```

- [ ] **Step 4: Add the Companion mirror.** In `internal/adapters/plex/companion.go`:

In `CompanionConfig` (struct ending line 64), add before `EventLog`:

```go
	// AutoAdvance is the persisted [adapters.plex].auto_advance toggle,
	// snapshotted at finalization and mirrored live via Companion.autoAdvance.
	AutoAdvance bool

	// EventLog is the optional ring buffer for adapter lifecycle events.
	EventLog *eventlog.Log
```

In the `Companion` struct (after `modelineName atomic.Pointer[string]`, line 89), add:

```go
	maxVideoBitrateKbps atomic.Int64
	modelineName        atomic.Pointer[string]

	// autoAdvance mirrors CompanionConfig.AutoAdvance live (ScopeHotSwap).
	// Read from the EOF goroutine; written by Adapter.ApplyConfig.
	autoAdvance atomic.Bool
	// autoAdvanceDelay is the settle delay before a guarded auto-advance
	// start. Seeded to autoAdvanceSettleDelay in NewCompanion; tests set 0.
	autoAdvanceDelay time.Duration
```

In `NewCompanion` (line 114), after `c.maxVideoBitrateKbps.Store(...)`:

```go
	c := &Companion{cfg: cfg, core: core}
	c.maxVideoBitrateKbps.Store(int64(cfg.MaxVideoBitrateKbps))
	c.autoAdvance.Store(cfg.AutoAdvance)
	c.autoAdvanceDelay = autoAdvanceSettleDelay
	c.SetModeline(cfg.Modeline)
	return c
```

After `SetMaxVideoBitrateKbps` (line 126), add:

```go
// SetAutoAdvance updates the live auto-advance toggle. Called by
// Adapter.ApplyConfig when the UI saves a new value; the next EOF reads it.
func (c *Companion) SetAutoAdvance(v bool) {
	c.autoAdvance.Store(v)
}
```

Now create `internal/adapters/plex/autoadvance.go` with just the settle-delay constant so this task compiles. Tasks 3 and 4 extend this same file:

```go
// Package plex auto-advance: when a cast finishes cleanly and the
// auto_advance toggle is on, start the next play-queue item. The wrapper,
// goroutine, and shared queue helper are added in later tasks.
package plex

import "time"

// autoAdvanceSettleDelay is the default pause before a guarded auto-advance
// start. It gives a still-connected Plex controller the chance to drive the
// advance itself; StartSessionIfIdle is the actual race guard, so this value
// affects smoothness only, not correctness. Tunable; see the spec's Open
// items. Tests override via Companion.autoAdvanceDelay.
const autoAdvanceSettleDelay = 1 * time.Second
```

- [ ] **Step 5: Wire the adapter schema.** In `internal/adapters/plex/adapter.go`:

`Fields()` — add a new `FieldDef` to the returned slice (after the `max_video_bitrate_kbps` entry, before the closing `}` at line 381):

```go
		{
			Key:   "auto_advance",
			Label: "Continuous Play",
			Help: "When a track or episode ends, automatically play the next item" +
				" in the Plex queue (playlist, album, TV show, or artist radio)." +
				" Works even when the Plex app that started the cast is closed.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "Playback",
		},
```

`scopeForPlexField` (line 554) — add `auto_advance` to the hot-swap case:

```go
	case "enabled", "auto_advance":
		return adapters.ScopeHotSwap // handled live; enabled is out-of-band, auto_advance via the companion mirror
```

(Replace the existing `case "enabled":` line with the combined case above.)

`diffPlexConfig` (line 518) — add before `return changed`:

```go
	if oldCfg.AutoAdvance != newCfg.AutoAdvance {
		changed = append(changed, "auto_advance")
	}
	return changed
```

`CurrentValues` (line 577) — add the key to the returned map:

```go
		"max_video_bitrate_kbps": cfg.MaxVideoBitrateKbps,
		"auto_advance":           cfg.AutoAdvance,
```

`ApplyConfig` (line 504, inside `if companion != nil {`) — push the mirror:

```go
	if companion != nil {
		companion.SetMaxVideoBitrateKbps(newCfg.MaxVideoBitrateKbps)
		companion.SetAutoAdvance(newCfg.AutoAdvance)
	}
```

`ensureFinalized` (line 284, the `NewCompanion(CompanionConfig{...})` literal) — add the field:

```go
				MaxVideoBitrateKbps: cfgSnap.MaxVideoBitrateKbps,
				AutoAdvance:         cfgSnap.AutoAdvance,
				Modeline:            a.cfg.Bridge.Video.Modeline,
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/adapters/plex/ -run TestAutoAdvance_ -v`
Expected: PASS (5 tests). If `autoAdvanceSettleDelay` is undefined, you have not yet added Task 4's file or the temporary const — add the temporary const from Step 4's note.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/plex/config.go internal/adapters/plex/companion.go internal/adapters/plex/adapter.go internal/adapters/plex/autoadvance.go internal/adapters/plex/autoadvance_test.go
git commit -m "feat(plex): add auto_advance config field + companion live mirror"
```

---

## Task 2: Extend the SessionManager interface with StartSessionIfIdle

The Plex adapter sees `core.Manager` through the narrow `SessionManager` interface, which today lacks `StartSessionIfIdle`. Surface the existing core method and teach the test fake about it.

**Files:**
- Modify: `internal/adapters/plex/companion.go` (interface)
- Modify: `internal/adapters/plex/companion_test.go` (fakeCore)

- [ ] **Step 1: Write the failing test** — append to `internal/adapters/plex/autoadvance_test.go`:

```go
import "github.com/idio-sync/MiSTer_GroovyRelay/internal/core"

func TestAutoAdvance_FakeCoreImplementsStartSessionIfIdle(t *testing.T) {
	var sm SessionManager = &fakeCore{}
	started, err := sm.StartSessionIfIdle(core.SessionRequest{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !started {
		t.Fatal("idle fakeCore should report started=true")
	}
}
```

(Merge the `core` import into the file's existing import block.)

- [ ] **Step 2: Run the test to verify it fails to compile**

Run: `go test ./internal/adapters/plex/ -run TestAutoAdvance_FakeCore -v`
Expected: BUILD FAILS — `SessionManager` has no `StartSessionIfIdle`; `*fakeCore` does not implement it.

- [ ] **Step 3: Add the method to the interface.** In `internal/adapters/plex/companion.go`, `SessionManager` (line 98), add after `StartSession`:

```go
type SessionManager interface {
	StartSession(core.SessionRequest) error
	// StartSessionIfIdle starts req only when no session is active, checked
	// under the manager lock. Returns (false, nil) when a session is already
	// active (the caller stands down). Used by Plex auto-advance to avoid
	// double-advancing when a controller has already taken over.
	StartSessionIfIdle(core.SessionRequest) (bool, error)
	Pause() error
```

`core.Manager` already implements `StartSessionIfIdle` (`manager.go:906`), so the production wiring compiles unchanged.

- [ ] **Step 4: Implement it on the fake.** In `internal/adapters/plex/companion_test.go`, add fields to `fakeCore` (struct at line 228):

```go
type fakeCore struct {
	mu       sync.Mutex
	lastReq  core.SessionRequest
	starts   int
	paused   bool
	played   bool
	stopped  bool
	lastSeek int
	status   core.SessionStatus
	startErr error
	vizMode  string

	// auto-advance test controls
	notIdle          bool // when true, StartSessionIfIdle reports not-started
	startIfIdleCalls int
}
```

Add the method after `StartSession` (line 247):

```go
func (f *fakeCore) StartSessionIfIdle(r core.SessionRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startIfIdleCalls++
	if f.notIdle {
		return false, nil
	}
	f.lastReq = r
	if f.startErr != nil {
		return true, f.startErr
	}
	f.starts++
	return true, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/adapters/plex/ -run TestAutoAdvance_FakeCore -v`
Expected: PASS.

- [ ] **Step 6: Run the full plex package to confirm no regressions**

Run: `go test ./internal/adapters/plex/...`
Expected: PASS (the interface change compiles against every existing fakeCore usage).

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/plex/companion.go internal/adapters/plex/companion_test.go internal/adapters/plex/autoadvance_test.go
git commit -m "feat(plex): surface StartSessionIfIdle on SessionManager"
```

---

## Task 3: Extract resolveNextQueueItem (shared queue resolution)

`restartFromPlayQueueItem` mixes HTTP plumbing with the pure logic of "fetch the queue, pick an item, build the next `PlayMediaRequest`." Extract the pure middle so auto-advance can reuse it with a different start strategy, and refactor the HTTP handler to call it — behavior preserved.

**Files:**
- Create: `internal/adapters/plex/autoadvance.go`
- Modify: `internal/adapters/plex/companion.go` (`restartFromPlayQueueItem`)
- Test: `internal/adapters/plex/autoadvance_test.go`

- [ ] **Step 1: Write the failing test** — append to `autoadvance_test.go`. It stands up an httptest server returning a 3-item play queue and asserts `resolveNextQueueItem` advances from item 2 to item 3:

```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

func newPlayQueueServer(t *testing.T, body string) (*httptest.Server, PlayMediaRequest) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/playQueues/") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(body))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	u, _ := url.Parse(srv.URL)
	host, port := u.Hostname(), u.Port()
	p := PlayMediaRequest{
		PlexServerScheme: "http",
		PlexServerAddress: host,
		PlexServerPort:   port,
		ContainerKey:     "/playQueues/77",
		MediaKey:         "/library/metadata/200",
		MediaType:        "track",
		PlayQueueItemID:  "1002",
		PlayQueueID:      "77",
	}
	return srv, p
}

const threeItemQueue = `<?xml version="1.0"?>
<MediaContainer playQueueID="77" playQueueVersion="3">
  <Track key="/library/metadata/100" ratingKey="100" playQueueItemID="1001"/>
  <Track key="/library/metadata/200" ratingKey="200" playQueueItemID="1002"/>
  <Track key="/library/metadata/300" ratingKey="300" playQueueItemID="1003"/>
</MediaContainer>`

func TestResolveNextQueueItem_AdvancesToNext(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	c := NewCompanion(CompanionConfig{}, &fakeCore{})

	next, err := c.resolveNextQueueItem(context.Background(), p, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
	if err != nil {
		t.Fatalf("resolveNextQueueItem: %v", err)
	}
	if next.MediaKey != "/library/metadata/300" {
		t.Fatalf("next.MediaKey = %q, want /library/metadata/300", next.MediaKey)
	}
	if next.PlayQueueItemID != "1003" {
		t.Fatalf("next.PlayQueueItemID = %q, want 1003", next.PlayQueueItemID)
	}
	if next.OffsetMs != 0 {
		t.Fatalf("next.OffsetMs = %d, want 0", next.OffsetMs)
	}
	if next.TranscodeSessionID == "" || next.TranscodeSessionID == p.TranscodeSessionID {
		t.Fatalf("next.TranscodeSessionID must be freshly minted, got %q", next.TranscodeSessionID)
	}
}

func TestResolveNextQueueItem_EndOfQueueSentinel(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	p.PlayQueueItemID = "1003" // last item
	c := NewCompanion(CompanionConfig{}, &fakeCore{})

	_, err := c.resolveNextQueueItem(context.Background(), p, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
	if !errors.Is(err, errNoNextQueueItem) {
		t.Fatalf("want errNoNextQueueItem at end of queue, got %v", err)
	}
}
```

Merge the imports above into the existing top-level import block.

- [ ] **Step 2: Run to verify it fails to compile**

Run: `go test ./internal/adapters/plex/ -run TestResolveNextQueueItem -v`
Expected: BUILD FAILS — `resolveNextQueueItem` and `errNoNextQueueItem` undefined.

- [ ] **Step 3: Extend `internal/adapters/plex/autoadvance.go`** (created in Task 1) with the helper and sentinel. Replace its import line `import "time"` with the grouped block below, and append the sentinel + helper after the `autoAdvanceSettleDelay` const (the wrapper/goroutine come in Task 4):

```go
import (
	"context"
	"errors"
	"fmt"
	"time"
)
```

```go
// errNoNextQueueItem signals that selectItem found no item to advance to
// (end of queue, or the current item is not in the fetched queue). Callers
// treat this as a clean stop, not an error. Its message matches the legacy
// restartFromPlayQueueItem HTTP error so that handler's response is unchanged.
var errNoNextQueueItem = errors.New("play queue item not found")

// resolveNextQueueItem fetches p's play queue, applies selectItem to choose a
// target, and returns a PlayMediaRequest advanced to it: MediaKey/queue ids
// updated, Title cleared, OffsetMs zeroed, and a freshly-minted
// TranscodeSessionID. CommandID is left untouched (callers set it if they
// have a controller command). Returns errNoNextQueueItem when selectItem
// declines. Does not touch c.lastPlay.
func (c *Companion) resolveNextQueueItem(
	ctx context.Context,
	p PlayMediaRequest,
	selectItem func([]playQueueItem, PlayMediaRequest) (playQueueItem, bool),
) (PlayMediaRequest, error) {
	if p.MediaKey == "" {
		return PlayMediaRequest{}, fmt.Errorf("no plex session")
	}
	if p.ContainerKey == "" {
		return PlayMediaRequest{}, errNoNextQueueItem
	}
	pq, err := c.fetchPlayQueue(ctx, p)
	if err != nil {
		return PlayMediaRequest{}, err
	}
	item, ok := selectItem(pq.Items, p)
	if !ok {
		return PlayMediaRequest{}, errNoNextQueueItem
	}
	key := item.Key
	if key == "" && item.RatingKey != "" {
		key = "/library/metadata/" + item.RatingKey
	}
	if key == "" {
		return PlayMediaRequest{}, fmt.Errorf("play queue item has no media key")
	}
	p.MediaKey = key
	p.Title = ""
	p.PlayQueueItemID = item.PlayQueueItemID
	p.PlayQueueID = firstNonEmpty(p.PlayQueueID, pq.PlayQueueID)
	p.PlayQueueVersion = firstNonEmpty(p.PlayQueueVersion, pq.PlayQueueVersion)
	p.OffsetMs = 0
	p.TranscodeSessionID = NewTranscodeSessionID()
	return p, nil
}
```

- [ ] **Step 4: Refactor `restartFromPlayQueueItem` to use the helper.** In `companion.go`, replace the body from the `pq, err := c.fetchPlayQueue(...)` block through the `p.TranscodeSessionID = NewTranscodeSessionID()` line (lines 640–665) with a call to the helper. The function becomes:

```go
func (c *Companion) restartFromPlayQueueItem(w http.ResponseWriter, r *http.Request, selectItem func([]playQueueItem, PlayMediaRequest) (playQueueItem, bool)) bool {
	prevStatus := core.SessionStatus{}
	if c.core != nil {
		prevStatus = c.core.Status()
	}
	p := c.lastPlaySession()
	if p.MediaKey == "" {
		http.Error(w, "no plex session", 400)
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	next, err := c.resolveNextQueueItem(ctx, p, selectItem)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return false
	}
	next.CommandID = queryOrHeader(r, "commandID")
	preset, err := c.currentPreset()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	req := c.sessionRequestForPlay(r.Context(), next, preset)
	if prevStatus.State != core.StateIdle {
		c.notifyStoppedTimeline(prevStatus)
	}
	c.emit(eventlog.SeverityInfo, fmt.Sprintf("cast-requested %s", req.AdapterRef))
	if err := c.core.StartSession(req); err != nil {
		cleanupSessionArtwork(req)
		http.Error(w, err.Error(), 400)
		return false
	}
	c.rememberPlaySession(next)
	if !c.restorePausedIfNeeded(w, prevStatus.State == core.StatePaused) {
		return false
	}
	c.notifyTimeline()
	return true
}
```

Note the rename `p` → `next` after resolution so `rememberPlaySession(next)` stores the advanced request (identical to the old behavior where `p` was mutated in place).

- [ ] **Step 5: Run the new tests + the existing skip-next tests**

Run: `go test ./internal/adapters/plex/ -run 'TestResolveNextQueueItem|SkipNext|SkipPrevious|PlayQueue' -v`
Expected: PASS — both new resolve tests and every pre-existing skip-next/prev test (behavior preserved).

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/plex/autoadvance.go internal/adapters/plex/companion.go internal/adapters/plex/autoadvance_test.go
git commit -m "refactor(plex): extract resolveNextQueueItem from restartFromPlayQueueItem"
```

---

## Task 4: Auto-advance on EOF (the feature)

Wrap both request builders so a clean `"eof"` triggers a guarded background advance. This is the behavior that makes the toggle do something.

**Files:**
- Modify: `internal/adapters/plex/autoadvance.go` (add wrapper + goroutine)
- Modify: `internal/adapters/plex/companion.go` (wrap calls in both builders)
- Test: `internal/adapters/plex/autoadvance_test.go`

- [ ] **Step 1: Write the failing tests** — append to `autoadvance_test.go`.
Merge `time` into the top-level import block. These exercise the wrapper's
gating and the end-to-end advance via the httptest queue + `fakeCore`:

```go
func TestAutoAdvance_GatingOffDoesNotAdvance(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{AutoAdvance: false}, fc)
	innerCalled := false

	stop := c.withAutoAdvance(PlayMediaRequest{MediaKey: "/library/metadata/200"}, func(reason string) {
		innerCalled = true
	})
	stop("eof")

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if !innerCalled {
		t.Fatal("wrapped OnStop did not call inner callback")
	}
	if fc.startIfIdleCalls != 0 {
		t.Fatalf("auto-advance fired with toggle off: calls=%d", fc.startIfIdleCalls)
	}
}

func TestAutoAdvance_NonEOFReasonDoesNotAdvance(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{AutoAdvance: true}, fc)
	innerCalled := false

	stop := c.withAutoAdvance(PlayMediaRequest{MediaKey: "/library/metadata/200"}, func(reason string) {
		innerCalled = true
	})
	stop("stop") // user stop / preempt — must not advance

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if !innerCalled {
		t.Fatal("wrapped OnStop did not call inner callback")
	}
	if fc.startIfIdleCalls != 0 {
		t.Fatalf("auto-advance fired on non-eof reason: calls=%d", fc.startIfIdleCalls)
	}
}

func TestAutoAdvance_EOFAdvancesToNextItem(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	p.MediaType = "episode"
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{AutoAdvance: true, Modeline: "NTSC_480i"}, fc)
	c.autoAdvanceDelay = 0

	req := c.sessionRequestForPreset(p, presetForTest(t))
	req.OnStop("eof")
	waitForStartIfIdle(t, fc, 1)

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.lastReq.AdapterRef != "/library/metadata/300" {
		t.Fatalf("advanced to AdapterRef %q, want /library/metadata/300", fc.lastReq.AdapterRef)
	}
}

func TestAutoAdvance_MusicBuilderEOFAdvancesToNextItem(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	p.MediaType = "track"
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{AutoAdvance: true, Modeline: "NTSC_480i"}, fc)
	c.autoAdvanceDelay = 0

	req := c.musicSessionRequestForPlay(p, MusicMetadata{Title: "Track 2"})
	req.OnStop("eof")
	waitForStartIfIdle(t, fc, 1)

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if !strings.HasPrefix(fc.lastReq.AdapterRef, "/library/metadata/300:") {
		t.Fatalf("advanced to AdapterRef %q, want prefix /library/metadata/300:", fc.lastReq.AdapterRef)
	}
}

func TestAutoAdvance_StandsDownWhenNotIdle(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	p.MediaType = "episode"
	fc := &fakeCore{notIdle: true} // a controller already took over
	c := NewCompanion(CompanionConfig{AutoAdvance: true, Modeline: "NTSC_480i"}, fc)
	c.autoAdvanceDelay = 0

	req := c.sessionRequestForPreset(p, presetForTest(t))
	req.OnStop("eof")
	waitForStartIfIdle(t, fc, 1)

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.starts != 0 {
		t.Fatalf("stood-down advance still started a session: starts=%d", fc.starts)
	}
}

func TestAutoAdvance_EndOfQueueDoesNotStart(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	p.MediaType = "episode"
	p.PlayQueueItemID = "1003" // last item
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{AutoAdvance: true, Modeline: "NTSC_480i"}, fc)
	c.autoAdvanceDelay = 0

	// Call the worker synchronously here so the no-start assertion happens
	// after resolveNextQueueItem has definitely returned errNoNextQueueItem.
	c.advanceAfterEOF(p)

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.startIfIdleCalls != 0 {
		t.Fatalf("end-of-queue should not call StartSessionIfIdle: calls=%d", fc.startIfIdleCalls)
	}
}

// waitForStartIfIdle polls until fc.startIfIdleCalls >= want or times out.
func waitForStartIfIdle(t *testing.T, fc *fakeCore, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fc.mu.Lock()
		got := fc.startIfIdleCalls
		fc.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d StartSessionIfIdle call(s)", want)
}
```

`presetForTest` returns a valid modeline preset. Reuse the project's existing test preset helper if one exists in the package; otherwise add:

```go
func presetForTest(t *testing.T) core.ModelinePreset {
	t.Helper()
	preset, err := core.ResolvePreset("NTSC_480i")
	if err != nil {
		t.Fatalf("ResolvePreset: %v", err)
	}
	return preset
}
```

(Check `companion_test.go` first — if it already constructs presets via `core.ResolvePreset`, mirror that exact modeline name to avoid drift.)

**Important:** the video-path tests above construct `NewCompanion(CompanionConfig{AutoAdvance: …}, fc)`. For the cases that actually advance (`EOFAdvancesToNextItem`, `StandsDownWhenNotIdle`, `UsesCapturedContextNotLastPlay`), `advanceAfterEOF` calls `currentPreset()`, which reads the companion's modeline. Construct those companions with a concrete modeline so it resolves — i.e. `NewCompanion(CompanionConfig{AutoAdvance: true, Modeline: "NTSC_480i"}, fc)`. The gating/non-eof/end-of-queue cases never reach `currentPreset`, so a modeline is optional there (harmless to include for uniformity).

- [ ] **Step 2: Run to verify the gating tests fail**

Run: `go test ./internal/adapters/plex/ -run TestAutoAdvance_EOF -v`
Expected: BUILD FAILS or TEST FAILS — `withAutoAdvance`/`advanceAfterEOF` do not
exist yet; once they compile, `sessionRequestForPreset`'s `OnStop("eof")` still
does nothing until the builders are wrapped, so `startIfIdleCalls` stays 0.

- [ ] **Step 3: Add the wrapper + goroutine** to `autoadvance.go`. Add the EOF-reason const near the top:

```go
// autoAdvanceEOFReason is the OnStop reason core.Manager sets on a clean
// ffmpeg exit (see internal/core/manager.go handlePlaneExit). Only this
// reason triggers an advance; user stop / preempt use other reasons.
const autoAdvanceEOFReason = "eof"
```

Add the wrapper and goroutine. They add `log/slog` and `internal/eventlog` on top of Task 3's imports, so the file's import block becomes exactly:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// withAutoAdvance wraps inner so that, after it runs, a clean end-of-stream
// (reason == autoAdvanceEOFReason) starts the next play-queue item when the
// auto_advance toggle is on. captured is the finished item's request, closed
// over at construction time — never read c.lastPlay from here (a successor
// cast may have overwritten it). Safe to call with inner == nil.
func (c *Companion) withAutoAdvance(captured PlayMediaRequest, inner func(string)) func(string) {
	return func(reason string) {
		if inner != nil {
			inner(reason)
		}
		if reason != autoAdvanceEOFReason || !c.autoAdvance.Load() {
			return
		}
		go c.advanceAfterEOF(captured)
	}
}

// advanceAfterEOF resolves and starts the next play-queue item after captured.
// Best-effort: any failure (end of queue, fetch error, unplayable next item,
// or a controller having taken over) logs and stops — never a crash, never a
// runaway skip loop. The next request is built through sessionRequestForPlay,
// so it gets its own withAutoAdvance OnStop and the chain self-perpetuates.
func (c *Companion) advanceAfterEOF(captured PlayMediaRequest) {
	if c.core == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	next, err := c.resolveNextQueueItem(ctx, captured, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
	if err != nil {
		if errors.Is(err, errNoNextQueueItem) {
			slog.Info("plex auto-advance: end of queue", "key", captured.MediaKey)
		} else {
			slog.Warn("plex auto-advance: resolve next item failed", "key", captured.MediaKey, "err", err)
		}
		return
	}

	preset, err := c.currentPreset()
	if err != nil {
		slog.Warn("plex auto-advance: resolve preset failed", "err", err)
		return
	}
	req := c.sessionRequestForPlay(ctx, next, preset)

	// Settle delay immediately before the guarded start: a still-connected
	// controller may drive the advance itself. StartSessionIfIdle is the
	// actual guard against a double-advance; this only smooths that case.
	if c.autoAdvanceDelay > 0 {
		time.Sleep(c.autoAdvanceDelay)
	}

	started, err := c.core.StartSessionIfIdle(req)
	if err != nil {
		cleanupSessionArtwork(req)
		slog.Warn("plex auto-advance: start failed", "key", next.MediaKey, "err", err)
		return
	}
	if !started {
		cleanupSessionArtwork(req)
		slog.Debug("plex auto-advance: stood down, session no longer idle", "key", next.MediaKey)
		return
	}
	c.emit(eventlog.SeverityInfo, fmt.Sprintf("auto-advance %s", req.AdapterRef))
	c.rememberPlaySession(next)
	c.notifyTimeline()
}
```

- [ ] **Step 4: Wrap both builders.** In `companion.go`:

In `musicSessionRequestForPlay`, after the artwork-cleanup wrap (line 433), before `return req`:

```go
	req.OnStop = artworkcache.WithCleanup(md.ArtworkPath, req.OnStop)
	req.OnStop = c.withAutoAdvance(p, req.OnStop)
	return req
```

In `sessionRequestForPreset`, after the `req.OnStop = func(reason string){...}` closure (line 530), before `return req`:

```go
	}
	req.OnStop = c.withAutoAdvance(p, req.OnStop)
	return req
}
```

(For the video path, `p` is the function parameter — the captured request for this item.)

- [ ] **Step 5: Run the auto-advance tests**

Run: `go test ./internal/adapters/plex/ -run TestAutoAdvance_ -v`
Expected: PASS — gating off/non-eof don't advance; both video and music EOF
paths advance to `/library/metadata/300`; non-idle stands down (no start);
end-of-queue makes no `StartSessionIfIdle` call after the queue fetch resolves.

- [ ] **Step 6: Add the captured-context regression test** — append to `autoadvance_test.go`. It proves the advance follows the captured request, not a clobbered `lastPlay`:

```go
func TestAutoAdvance_UsesCapturedContextNotLastPlay(t *testing.T) {
	srv, qA := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	qA.MediaType = "episode"
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{AutoAdvance: true, Modeline: "NTSC_480i"}, fc)
	c.autoAdvanceDelay = 0

	// Build item A's request (queue 77, currently at item 1002).
	req := c.sessionRequestForPreset(qA, presetForTest(t))

	// Simulate a foreign successor clobbering lastPlay with a DIFFERENT queue.
	c.rememberPlaySession(PlayMediaRequest{
		PlexServerScheme: "http", PlexServerAddress: "127.0.0.1", PlexServerPort: "1",
		ContainerKey: "/playQueues/999", MediaKey: "/library/metadata/900", PlayQueueItemID: "9001",
	})

	req.OnStop("eof")
	waitForStartIfIdle(t, fc, 1)

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.lastReq.AdapterRef != "/library/metadata/300" {
		t.Fatalf("advance used clobbered lastPlay; AdapterRef=%q want /library/metadata/300", fc.lastReq.AdapterRef)
	}
}
```

Run: `go test ./internal/adapters/plex/ -run TestAutoAdvance_UsesCapturedContext -v`
Expected: PASS — the advance targets queue 77's item 3, ignoring the clobbered `lastPlay` (queue 999).

- [ ] **Step 7: Run the whole package + vet**

Run: `go test ./internal/adapters/plex/... && go vet ./internal/adapters/plex/...`
Expected: PASS, no vet complaints.

- [ ] **Step 8: Commit**

```bash
git add internal/adapters/plex/autoadvance.go internal/adapters/plex/companion.go internal/adapters/plex/autoadvance_test.go
git commit -m "feat(plex): auto-advance to next queue item on clean EOF"
```

---

## Task 5: Full build + race + integration sweep

Confirm the whole repo still builds and the feature holds under the race detector and the existing integration harness.

**Files:** none (verification only).

- [ ] **Step 1: Vet + unit tests, whole repo**

Run: `make lint && make test`
Expected: PASS. (`make lint` is `go vet ./...`; `make test` is the unit suite.)

- [ ] **Step 2: Race detector on the plex package**

Run: `go test -race ./internal/adapters/plex/...`
Expected: PASS, no race report. (If `gcc`/cgo is unavailable locally, this gate runs in CI — note it in the PR and rely on CI. See the repo's race-needs-cgo constraint.)

The race-sensitive paths: the EOF goroutine writes `c.lastPlay` via `rememberPlaySession` (guarded by `sessMu`) and reads `c.autoAdvance` (atomic); `StartSessionIfIdle` crosses into the manager under `m.mu`. All synchronized — the detector should be clean.

- [ ] **Step 3: Build both binaries**

Run: `make build`
Expected: both `mister-groovy-relay` and `fake-mister` build clean.

- [ ] **Step 4: Integration sweep (optional locally; required in CI)**

Run: `make test-integration` (requires ffmpeg + ffprobe on PATH)
Expected: PASS — auto-advance changes are adapter-internal and must not regress the data-plane handshake tests.

- [ ] **Step 5: Commit (only if any fixups were needed)**

```bash
git status --short
git add <only the auto-advance files intentionally changed by the fixup>
git commit -m "test(plex): green build/race/integration for auto-advance"
```

---

## Manual smoke test (after merge, with hardware or fake-mister)

Not an automated step — a checklist for the operator validating end-to-end:

1. In `config.toml`, set `[adapters.plex] auto_advance = true` (or flip "Continuous Play" in the chassis settings drawer once running).
2. From a Plex controller, cast a **playlist or album** to the MiSTer target, then **close the Plex app**.
3. Let the first track finish. Expect: ~1s gap, then the next track's INIT/SWITCHRES handshake and playback — without touching the controller.
4. Let the last track finish. Expect: playback stops (no loop), and the log shows `plex auto-advance: end of queue`.
5. Set `auto_advance = false` (or flip the drawer toggle); repeat step 2–3. Expect: playback stops at the end of the first track (today's behavior).

---

## What this plan does NOT cover (Plan 2)

- The transport-row **CONTINUOUS** button (lit/dim, `POST /receiver/continuous/toggle` → `AdapterSettingsSaver.SaveTouched("plex", {"auto_advance": …})`).
- The VFD **AUTO** indicator segment.
- Button/VFD templates, JS, and `internal/chassis` view-model wiring.

These are presentation polish; the feature is fully usable after this plan via config and the existing settings drawer. Plan 2 will be written against the same spec's Component 3 once this lands.
