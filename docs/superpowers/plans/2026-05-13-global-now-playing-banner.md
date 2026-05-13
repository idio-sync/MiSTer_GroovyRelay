# Global Now Playing Banner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one global now-playing command band with active playback controls and URL/Torrent quick-cast, then remove duplicated active controls from adapter panels.

**Architecture:** Core provides a stable monotonic session generation and full-session-key guarded mutations. Adapters opt into shared playback and quick-cast provider interfaces from `internal/adapters`; the UI shell renders one server-side htmx banner and dispatches to providers without adapter-name switches.

**Tech Stack:** Go 1.26.2, stdlib `net/http`, `html/template`, htmx fragments, existing `internal/ui` server templates, existing adapter registry, Go unit tests via `cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test`.

**Spec:** [docs/superpowers/specs/2026-05-13-global-now-playing-banner-design.md](../specs/2026-05-13-global-now-playing-banner-design.md)

---

## Files

**Create:**
- `internal/adapters/playback.go` — shared playback provider and quick-cast provider interfaces plus DTOs.
- `internal/adapters/playback_test.go` — compile-time and default-value coverage for shared DTO constants.
- `internal/ui/playback.go` — banner view model, snapshot builder, provider lookup, route handlers, quick-cast form parsing.
- `internal/ui/playback_test.go` — fake-provider UI tests for rendering, polling cadence, stale-session rejection, and quick-cast behavior.
- `internal/ui/templates/now-playing-banner.html` — shell banner partial and Cast drawer forms.
- `internal/adapters/url/playback_provider.go` — URL playback controls and URL quick-cast provider.
- `internal/adapters/url/playback_provider_test.go` — URL provider ownership, action, seek, replay, and quick-cast tests.
- `internal/adapters/streams/playback_provider.go` — Streams playback controls for previous/next/replay/stop.
- `internal/adapters/streams/playback_provider_test.go` — Streams provider view/action ownership tests.
- `internal/adapters/torrent/playback_provider.go` — Torrent stop control plus magnet/upload quick-cast provider.
- `internal/adapters/torrent/playback_provider_test.go` — Torrent stop and quick-cast provider tests.

**Modify:**
- `internal/core/types.go` — add `Generation uint64` to `SessionStatus` and `StatusHomeView`.
- `internal/core/manager.go` — assign stable session generations and add full-key guarded helpers.
- `internal/core/manager_test.go` — generation and guarded-helper tests.
- `internal/ui/server.go` — mount `/ui/playback/*` routes and attach banner data to shell renders.
- `internal/ui/templates/shell.html` — render `now-playing-banner.html` only outside setup mode.
- `internal/ui/static/app.css` — top command band, drawer, timeline, action-row, and narrow-width styles.
- `internal/ui/server_test.go` — shell inclusion/exclusion tests.
- `internal/adapters/url/adapter.go` — extend URL `SessionManager` narrow interface for full-key helpers.
- `internal/adapters/url/ui.go` — remove active position/scrub/pause/resume/stop/replay controls from the URL panel.
- `internal/adapters/url/ui_test.go` — assert URL panel keeps launch/history/cookies and omits active controls.
- `internal/adapters/streams/adapter.go` — extend Streams `SessionManager` narrow interface for full-key helpers.
- `internal/adapters/streams/playback.go` — add guarded action helpers used by the provider.
- `internal/adapters/streams/ui.go` — keep focused guide/status/refresh; remove previous/next/replay/stop from local now strip.
- `internal/adapters/streams/ui_test.go` — assert Streams panel no longer renders active transport controls.
- `internal/adapters/torrent/adapter.go` — extend Torrent `SessionManager` narrow interface for full-key stop.
- `internal/adapters/torrent/ui.go` — remove active Stop from `renderLiveStatus`.
- `internal/adapters/torrent/routes.go` — keep existing page-local magnet/upload/stop routes for compatibility, but banner provider owns global controls.
- `internal/adapters/torrent/ui_test.go` — assert Torrent panel no longer renders active Stop.

**Do not modify:**
- Plex Companion timeline/control routes.
- Jellyfin reporting routes.
- DLNA eventing/control-point routes.
- `README.md` while it is conflicted in the current worktree.

---

## Task 1: Core Session Generation And Full-Key Guards

**Files:**
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write failing generation status tests**

Append these tests to `internal/core/manager_test.go`:

```go
func TestManager_SessionGenerationStatusIncrementsOnFreshStarts(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60, Duration: 120}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner { return &fakePlane{} }

	m := newTestManager(t)
	if err := m.StartSession(SessionRequest{StreamURL: "http://example.test/one.mp4", AdapterRef: "url:one", Source: "url", Capabilities: Capabilities{CanSeek: true, CanPause: true}, DirectPlay: true}); err != nil {
		t.Fatalf("start one: %v", err)
	}
	first := m.Status()
	if first.Generation == 0 {
		t.Fatalf("first generation = 0, want non-zero")
	}
	if got := m.StatusHomeView().Generation; got != first.Generation {
		t.Fatalf("home generation = %d, want %d", got, first.Generation)
	}

	if err := m.StartSession(SessionRequest{StreamURL: "http://example.test/two.mp4", AdapterRef: "url:two", Source: "url", Capabilities: Capabilities{CanSeek: true, CanPause: true}, DirectPlay: true}); err != nil {
		t.Fatalf("start two: %v", err)
	}
	second := m.Status()
	if second.Generation <= first.Generation {
		t.Fatalf("second generation = %d, want > %d", second.Generation, first.Generation)
	}
}

func TestManager_SessionGenerationStableAcrossPauseResumeAndSeek(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60, Duration: 120}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner { return &fakePlane{} }

	m := newTestManager(t)
	req := SessionRequest{StreamURL: "http://example.test/movie.mp4", AdapterRef: "url:movie", Source: "url", Capabilities: Capabilities{CanSeek: true, CanPause: true}, DirectPlay: true}
	if err := m.StartSession(req); err != nil {
		t.Fatalf("start: %v", err)
	}
	gen := m.Status().Generation

	if err := m.Pause(); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if got := m.Status().Generation; got != gen {
		t.Fatalf("generation after pause = %d, want %d", got, gen)
	}
	if err := m.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	if got := m.Status().Generation; got != gen {
		t.Fatalf("generation after play = %d, want %d", got, gen)
	}
	if err := m.SeekTo(5000); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if got := m.Status().Generation; got != gen {
		t.Fatalf("generation after seek = %d, want %d", got, gen)
	}
}
```

- [ ] **Step 2: Run generation tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/core -run "TestManager_SessionGeneration" -count=1
```

Expected: FAIL with `first.Generation undefined` or equivalent missing `Generation` fields.

- [ ] **Step 3: Add generation fields to public status types**

Modify `internal/core/types.go`:

```go
type SessionStatus struct {
	State      State
	MediaKind  MediaKind
	Position   time.Duration
	Duration   time.Duration
	AdapterRef string
	StartedAt  time.Time
	Generation uint64
}
```

```go
type StatusHomeView struct {
	State       State
	MediaKind   MediaKind
	Title       string
	AdapterRef  string
	Source      string
	Generation  uint64
	Modeline    string
	Position    time.Duration
	Duration    time.Duration
	StartedAt   time.Time
	BlitsTotal  uint64
	FramesTotal uint64
	Underruns   uint64
	WireBytes   uint64
	LastACKAge  time.Duration
}
```

- [ ] **Step 4: Add manager generation bookkeeping**

Modify `internal/core/manager.go`:

```go
type Manager struct {
	bridge config.BridgeConfig
	sender *groovynet.Sender
	fsm    *StateMachine

	ffmpegResolver  BinaryResolver
	ffprobeResolver BinaryResolver

	mu             sync.Mutex
	cancelFn       context.CancelFunc
	plane          planeRunner
	active         *activeSession
	nextGeneration uint64

	eventLog *eventlog.Log
}
```

```go
type activeSession struct {
	req            SessionRequest
	startedAt      time.Time
	generation     uint64
	baseOffsetMs   int
	pausedPosition time.Duration
	duration       time.Duration
}
```

Add helpers near `errAdapterRefChanged`:

```go
func (m *Manager) allocateGenerationLocked() uint64 {
	m.nextGeneration++
	if m.nextGeneration == 0 {
		m.nextGeneration = 1
	}
	return m.nextGeneration
}

func (m *Manager) sessionMatchesLocked(ref string, generation uint64) bool {
	return ref != "" &&
		generation != 0 &&
		m.active != nil &&
		m.active.req.AdapterRef == ref &&
		m.active.generation == generation
}
```

Change `startPlaneLocked` to accept a generation:

```go
func (m *Manager) startPlaneLocked(req SessionRequest, offsetMs int, probe *ffmpeg.ProbeResult, cropRect *ffmpeg.CropRect, ffmpegPath string, generation uint64) error {
	// keep existing body; only the activeSession assignment changes
	m.active = &activeSession{
		req:          req,
		startedAt:    time.Now(),
		generation:   generation,
		baseOffsetMs: offsetMs,
		duration:     visualizerDuration(req, probe),
	}
	// keep existing goroutine/start return
}
```

Update public start paths to allocate:

```go
generation := m.allocateGenerationLocked()
if err := m.startPlaneLocked(req, req.SeekOffsetMs, probe, cropRect, ffmpegPath, generation); err != nil {
	return err
}
```

Update resume and seek rebuilds to reuse `a.generation`:

```go
if err := m.startPlaneLocked(req, resumeMs, probe, cropRect, ffmpegPath, a.generation); err != nil {
	return true, err
}
```

```go
if err := m.startPlaneLocked(req, offsetMs, probe, cropRect, ffmpegPath, a.generation); err != nil {
	return true, err
}
```

Set generation in status snapshots:

```go
st.Generation = m.active.generation
```

```go
view.Generation = m.active.generation
```

- [ ] **Step 5: Run generation tests and verify they pass**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/core -run "TestManager_SessionGeneration" -count=1
```

Expected: PASS.

- [ ] **Step 6: Write failing full-key guarded-helper tests**

Append to `internal/core/manager_test.go`:

```go
func TestManager_StopIfSessionRejectsStaleGeneration(t *testing.T) {
	m := newTestManager(t)
	stopped := make(chan string, 1)
	m.mu.Lock()
	m.active = &activeSession{
		req:        SessionRequest{AdapterRef: "url:same", OnStop: func(reason string) { stopped <- reason }},
		generation: 7,
	}
	m.mu.Unlock()

	matched, err := m.StopIfSession("url:same", 6)
	if err != nil {
		t.Fatalf("StopIfSession stale: %v", err)
	}
	if matched {
		t.Fatal("StopIfSession stale returned matched=true")
	}
	if got := m.Status().AdapterRef; got != "url:same" {
		t.Fatalf("AdapterRef = %q, want url:same", got)
	}
	select {
	case reason := <-stopped:
		t.Fatalf("OnStop fired on stale generation with reason %q", reason)
	default:
	}
}

func TestManager_StopIfSessionMatchesGeneration(t *testing.T) {
	m := newTestManager(t)
	stopped := make(chan string, 1)
	m.mu.Lock()
	m.active = &activeSession{
		req:        SessionRequest{AdapterRef: "url:same", OnStop: func(reason string) { stopped <- reason }},
		generation: 7,
	}
	m.mu.Unlock()

	matched, err := m.StopIfSession("url:same", 7)
	if err != nil {
		t.Fatalf("StopIfSession match: %v", err)
	}
	if !matched {
		t.Fatal("StopIfSession match returned matched=false")
	}
	if got := m.Status().AdapterRef; got != "" {
		t.Fatalf("AdapterRef after stop = %q, want empty", got)
	}
}
```

- [ ] **Step 7: Run guarded-helper tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/core -run "TestManager_StopIfSession" -count=1
```

Expected: FAIL with undefined `StopIfSession`.

- [ ] **Step 8: Add full-key guarded helpers**

Add to `internal/core/manager.go` next to existing `*IfAdapterRef` methods:

```go
func (m *Manager) StopIfSession(ref string, generation uint64) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.sessionMatchesLocked(ref, generation) {
		return false, nil
	}
	if err := m.stopLocked(ref); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}

func (m *Manager) PauseIfSession(ref string, generation uint64) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.sessionMatchesLocked(ref, generation) {
		return false, nil
	}
	if err := m.pauseLocked(ref); err != nil {
		if errors.Is(err, errAdapterRefChanged) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}
```

Refactor `playIfAdapterRef` and `seekToIfAdapterRef` into internal helpers that accept an optional generation. The lock checks before probing and immediately before mutation must use:

```go
if expectedGeneration != 0 {
	if !m.sessionMatchesLocked(expectedRef, expectedGeneration) {
		m.mu.Unlock()
		return false, nil
	}
} else if expectedRef != "" && a.req.AdapterRef != expectedRef {
	m.mu.Unlock()
	return false, nil
}
```

Expose:

```go
func (m *Manager) PlayIfSession(ref string, generation uint64) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	return m.playIfSession(ref, generation)
}

func (m *Manager) SeekToIfSession(ref string, generation uint64, offsetMs int) (bool, error) {
	if ref == "" || generation == 0 {
		return false, nil
	}
	return m.seekToIfSession(ref, generation, offsetMs)
}
```

Add guarded start helpers for replay/queue actions that need to start a replacement without preempting a newer session:

```go
func (m *Manager) StartSessionIfSession(req SessionRequest, expectedRef string, expectedGeneration uint64) (bool, error) {
	if expectedRef == "" || expectedGeneration == 0 {
		return false, nil
	}
	return m.startSessionGuarded(req, func() bool {
		return m.sessionMatchesLocked(expectedRef, expectedGeneration)
	})
}

func (m *Manager) StartSessionIfIdle(req SessionRequest) (bool, error) {
	return m.startSessionGuarded(req, func() bool {
		return m.active == nil
	})
}
```

Implement `startSessionGuarded` by reusing the current `StartSessionIfAdapterRef` structure: validate/probe outside `Manager.mu`, lock, run the supplied guard, allocate a new generation, call `startPlaneLocked`, then transition `EvPlayMedia`.

- [ ] **Step 9: Run full core tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/core -count=1
```

Expected: PASS.

- [ ] **Step 10: Format and commit core slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/core/types.go internal/core/manager.go internal/core/manager_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/core -count=1
git add internal/core/types.go internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(core): add stable playback session keys"
```

Expected: commit contains only core files.

---

## Task 2: Shared Adapter Playback Contracts

**Files:**
- Create: `internal/adapters/playback.go`
- Create: `internal/adapters/playback_test.go`

- [ ] **Step 1: Write failing contract tests**

Create `internal/adapters/playback_test.go`:

```go
package adapters

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestPlaybackBannerSnapshotCarriesFullSessionKey(t *testing.T) {
	snap := PlaybackBannerSnapshot{
		State:      core.StatePlaying,
		Source:     "url",
		Title:      "clip.mp4",
		AdapterRef: "url:abc",
		Generation: 42,
		Position:   5 * time.Second,
		Duration:   60 * time.Second,
		MediaKind:  core.MediaKindVideo,
		Modeline:   "NTSC_480i",
	}
	if snap.AdapterRef != "url:abc" || snap.Generation != 42 {
		t.Fatalf("session key = %q/%d", snap.AdapterRef, snap.Generation)
	}
}

func TestQuickCastEncodingConstants(t *testing.T) {
	if QuickCastEncodingForm != "form" {
		t.Fatalf("QuickCastEncodingForm = %q", QuickCastEncodingForm)
	}
	if QuickCastEncodingMultipart != "multipart" {
		t.Fatalf("QuickCastEncodingMultipart = %q", QuickCastEncodingMultipart)
	}
}
```

- [ ] **Step 2: Run contract tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters -run "TestPlaybackBannerSnapshot|TestQuickCastEncoding" -count=1
```

Expected: FAIL with undefined playback DTOs/constants.

- [ ] **Step 3: Add shared provider interfaces and DTOs**

Create `internal/adapters/playback.go`:

```go
package adapters

import (
	"context"
	"mime/multipart"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const (
	PlaybackActionPause    = "pause"
	PlaybackActionResume   = "resume"
	PlaybackActionStop     = "stop"
	PlaybackActionSeek     = "seek"
	PlaybackActionReplay   = "replay"
	PlaybackActionPrevious = "previous"
	PlaybackActionNext     = "next"

	QuickCastEncodingForm      = "form"
	QuickCastEncodingMultipart = "multipart"
)

type PlaybackControlProvider interface {
	PlaybackBanner(ctx context.Context, snap PlaybackBannerSnapshot) (PlaybackBannerAdapterView, bool)
	HandlePlaybackAction(ctx context.Context, action PlaybackActionRequest) (PlaybackActionResult, error)
}

type QuickCastProvider interface {
	QuickCastTabs() []QuickCastTab
	HandleQuickCast(ctx context.Context, req QuickCastRequest) (QuickCastResult, error)
}

type PlaybackBannerSnapshot struct {
	State      core.State
	Source     string
	Title      string
	AdapterRef string
	Generation uint64
	Position   time.Duration
	Duration   time.Duration
	StartedAt  time.Time
	MediaKind  core.MediaKind
	Modeline   string
}

type PlaybackBannerAdapterView struct {
	Title         string
	Subtitle      string
	SourceDisplay string
	Actions       []PlaybackAction
	Seek          *PlaybackSeek
}

type PlaybackAction struct {
	ID             string
	Label          string
	Icon           string
	Enabled        bool
	DisabledReason string
}

type PlaybackActionRequest struct {
	Action     string
	AdapterRef string
	Generation uint64
	OffsetMS   int
}

type PlaybackActionResult struct {
	Message string
}

type PlaybackSeek struct {
	Enabled        bool
	DisabledReason string
	OffsetMS       int
	DurationMS     int
}

type QuickCastTab struct {
	ID             string
	Label          string
	Enabled        bool
	DisabledReason string
	Encoding       string
	Fields         []QuickCastField
}

type QuickCastRequest struct {
	TabID  string
	Values map[string]string
	File   *QuickCastFile
}

type QuickCastResult struct {
	Message    string
	AdapterRef string
}

type QuickCastField struct {
	Name        string
	Label       string
	Type        string
	Placeholder string
	Required    bool
	Options     []QuickCastOption
}

type QuickCastOption struct {
	Value string
	Label string
}

type QuickCastFile struct {
	FieldName string
	Header    *multipart.FileHeader
}
```

- [ ] **Step 4: Run adapter contract tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters -count=1
```

Expected: PASS.

- [ ] **Step 5: Format and commit contract slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/adapters/playback.go internal/adapters/playback_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters -count=1
git add internal/adapters/playback.go internal/adapters/playback_test.go
git commit -m "feat(adapters): add playback provider contracts"
```

Expected: commit contains only shared adapter contract files.

---

## Task 3: Banner View Model, Shell Partial, And Polling

**Files:**
- Create: `internal/ui/playback.go`
- Create: `internal/ui/playback_test.go`
- Create: `internal/ui/templates/now-playing-banner.html`
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/templates/shell.html`
- Modify: `internal/ui/server_test.go`

- [ ] **Step 1: Write failing banner render tests**

Create `internal/ui/playback_test.go` with the shared fakes:

```go
package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakePlaybackAdapter struct {
	name        string
	displayName string
	enabled     bool
	view        adapters.PlaybackBannerAdapterView
	owns        bool
	actionCalls []adapters.PlaybackActionRequest
	actionErr   error
	actionMsg   string
	tabs        []adapters.QuickCastTab
	quickCalls  []adapters.QuickCastRequest
	quickErr    error
	quickMsg    string
}

func (f *fakePlaybackAdapter) Name() string { return f.name }
func (f *fakePlaybackAdapter) DisplayName() string {
	if f.displayName != "" {
		return f.displayName
	}
	return f.name
}
func (f *fakePlaybackAdapter) Fields() []adapters.FieldDef { return nil }
func (f *fakePlaybackAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f *fakePlaybackAdapter) IsEnabled() bool {
	if f.enabled {
		return true
	}
	return false
}
func (f *fakePlaybackAdapter) Start(context.Context) error { return nil }
func (f *fakePlaybackAdapter) Stop() error                 { return nil }
func (f *fakePlaybackAdapter) Status() adapters.Status     { return adapters.Status{State: adapters.StateRunning} }
func (f *fakePlaybackAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakePlaybackAdapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	return f.view, f.owns
}
func (f *fakePlaybackAdapter) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	f.actionCalls = append(f.actionCalls, req)
	if f.actionErr != nil {
		return adapters.PlaybackActionResult{}, f.actionErr
	}
	return adapters.PlaybackActionResult{Message: f.actionMsg}, nil
}
func (f *fakePlaybackAdapter) QuickCastTabs() []adapters.QuickCastTab { return f.tabs }
func (f *fakePlaybackAdapter) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	f.quickCalls = append(f.quickCalls, req)
	if f.quickErr != nil {
		return adapters.QuickCastResult{}, f.quickErr
	}
	return adapters.QuickCastResult{Message: f.quickMsg, AdapterRef: "fake:new"}, nil
}

func TestPlaybackBannerIdleRendersReadyAndQuickCast(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		tabs: []adapters.QuickCastTab{{
			ID:       "url",
			Label:    "URL",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingForm,
			Fields:   []adapters.QuickCastField{{Name: "url", Label: "URL", Type: "url", Required: true}},
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})

	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, body)
	}
	for _, want := range []string{`id="gr-now-playing"`, "Ready to cast", "Cast", "URL", `hx-trigger="every 5s"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("banner missing %q: %s", want, body)
		}
	}
}

func TestPlaybackBannerPlayingRendersProviderActionsAndTimeline(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		owns:    true,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{{ID: adapters.PlaybackActionPause, Label: "Pause", Icon: "pause", Enabled: true}},
			Seek:    &adapters.PlaybackSeek{Enabled: true, OffsetMS: 90000, DurationMS: 600000},
		},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "url",
			Title:      "Movie Night",
			AdapterRef: "url:abc",
			Generation: 11,
			Position:   90 * time.Second,
			Duration:   10 * time.Minute,
		}}
	})

	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"Now Playing", "Movie Night", "01:30", "10:00", `name="generation" value="11"`, "Pause", `hx-trigger="every 1s"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("banner missing %q: %s", want, body)
		}
	}
}
```

- [ ] **Step 2: Run banner render tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestPlaybackBannerIdle|TestPlaybackBannerPlaying" -count=1
```

Expected: FAIL with 404 for `/ui/playback/banner` or missing template/data.

- [ ] **Step 3: Add banner data model and helpers**

Create `internal/ui/playback.go` with these types and helpers:

```go
package ui

import (
	"context"
	"fmt"
	"html/template"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type playbackBannerData struct {
	StateLabel        string
	State            core.State
	SourceDisplay    string
	Title            string
	AdapterRef       string
	Generation       uint64
	Position         string
	Duration         string
	PositionMS       int
	DurationMS       int
	HasTimeline      bool
	Actions          []adapters.PlaybackAction
	Seek             *adapters.PlaybackSeek
	QuickCastTabs    []adapters.QuickCastTab
	CastDrawerOpen   bool
	ActiveQuickCast  string
	Message          string
	MessageKind      string
	PollTrigger      string
	ReadOnly         bool
}

type playbackRenderOptions struct {
	CastDrawerOpen  bool
	ActiveQuickCast string
	Message         string
	MessageKind     string
}

func (s *Server) buildPlaybackBannerData(ctx context.Context, opts playbackRenderOptions) playbackBannerData {
	view := core.StatusHomeView{State: core.StateIdle}
	if s.cfg.StatusViewer != nil {
		view = s.cfg.StatusViewer.StatusHomeView()
	}
	snap := adapters.PlaybackBannerSnapshot{
		State:      view.State,
		Source:     view.Source,
		Title:      view.Title,
		AdapterRef: view.AdapterRef,
		Generation: view.Generation,
		Position:   view.Position,
		Duration:   view.Duration,
		StartedAt:  view.StartedAt,
		MediaKind:  view.MediaKind,
		Modeline:   view.Modeline,
	}
	data := playbackBannerData{
		State:           view.State,
		StateLabel:      playbackStateLabel(view.State),
		SourceDisplay:   displayNameForSource(s.cfg.Registry, view.Source, view.AdapterRef),
		Title:           firstNonEmpty(view.Title, view.AdapterRef, "Ready"),
		AdapterRef:      view.AdapterRef,
		Generation:      view.Generation,
		Position:        formatClock(view.Position),
		Duration:        formatClock(view.Duration),
		PositionMS:      int(view.Position / time.Millisecond),
		DurationMS:      int(view.Duration / time.Millisecond),
		HasTimeline:     view.Duration > 0,
		CastDrawerOpen:  opts.CastDrawerOpen,
		ActiveQuickCast: opts.ActiveQuickCast,
		Message:         opts.Message,
		MessageKind:     opts.MessageKind,
		QuickCastTabs:   s.quickCastTabs(),
	}
	if data.SourceDisplay == "" {
		data.SourceDisplay = "No source"
	}
	if provider, ok := s.playbackProviderForSnapshot(snap); ok {
		if providerView, owns := provider.PlaybackBanner(ctx, snap); owns {
			if providerView.Title != "" {
				data.Title = providerView.Title
			}
			if providerView.SourceDisplay != "" {
				data.SourceDisplay = providerView.SourceDisplay
			}
			data.Actions = providerView.Actions
			data.Seek = providerView.Seek
		}
	}
	if view.State != core.StateIdle && len(data.Actions) == 0 && data.Seek == nil {
		data.ReadOnly = true
	}
	data.PollTrigger = playbackPollTrigger(data)
	return data
}
```

Add helper functions in the same file:

```go
func playbackStateLabel(state core.State) string {
	switch state {
	case core.StatePlaying:
		return "Now Playing"
	case core.StatePaused:
		return "Paused"
	default:
		return "Ready to cast"
	}
}

func playbackPollTrigger(data playbackBannerData) string {
	if data.State == core.StateIdle || data.ReadOnly {
		return "every 5s"
	}
	if data.HasTimeline && data.Seek != nil && data.Seek.Enabled {
		return "every 1s"
	}
	return "every 3s"
}

func quickCastTabActive(tab adapters.QuickCastTab, active string, first bool) bool {
	if active != "" {
		return tab.ID == active
	}
	return first
}
```

Add `quickCastTabActive` to `templateFuncs` in `internal/ui/server.go`:

```go
"quickCastTabActive": quickCastTabActive,
```

- [ ] **Step 4: Add provider discovery helpers**

Add to `internal/ui/playback.go`:

```go
func (s *Server) playbackProviderForSnapshot(snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackControlProvider, bool) {
	if s.cfg.Registry == nil || snap.AdapterRef == "" {
		return nil, false
	}
	if snap.Source != "" {
		if a, ok := s.cfg.Registry.Get(snap.Source); ok {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	for _, a := range s.cfg.Registry.List() {
		if adapterRefBelongsTo(a.Name(), snap.AdapterRef) {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	return nil, false
}

func (s *Server) quickCastTabs() []adapters.QuickCastTab {
	if s.cfg.Registry == nil {
		return nil
	}
	var tabs []adapters.QuickCastTab
	for _, a := range s.cfg.Registry.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		tabs = append(tabs, p.QuickCastTabs()...)
	}
	return tabs
}

func (s *Server) quickCastProviderForTab(tabID string) (adapters.QuickCastProvider, bool) {
	if s.cfg.Registry == nil || tabID == "" {
		return nil, false
	}
	for _, a := range s.cfg.Registry.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		for _, tab := range p.QuickCastTabs() {
			if tab.ID == tabID {
				return p, true
			}
		}
	}
	return nil, false
}

func adapterRefBelongsTo(adapterName, ref string) bool {
	return strings.HasPrefix(ref, adapterName+":") || strings.HasPrefix(ref, adapterName+"/")
}
```

- [ ] **Step 5: Add banner route and shell data field**

Modify `internal/ui/server.go`:

```go
s.mountGET(mux, "/ui/playback/banner", s.handlePlaybackBanner)
```

Add to `shellTemplateData`:

```go
Playback playbackBannerData
```

Set it in `shellDataForPath`:

```go
data.Playback = s.buildPlaybackBannerData(context.Background(), playbackRenderOptions{})
```

Add handler to `internal/ui/playback.go`:

```go
func (s *Server) handlePlaybackBanner(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "now-playing-banner.html", s.buildPlaybackBannerData(r.Context(), playbackRenderOptions{})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 6: Add banner template**

Create `internal/ui/templates/now-playing-banner.html`:

```html
{{define "now-playing-banner.html"}}
<section id="gr-now-playing" class="gr-now-playing gr-now-playing--top" hx-get="/ui/playback/banner" hx-trigger="{{.PollTrigger}}" hx-swap="outerHTML">
	<div class="gr-now-playing-main">
		<div class="gr-now-playing-copy">
			<div class="gr-now-playing-state">{{.StateLabel}}</div>
			<div class="gr-now-playing-title" title="{{.Title}}">{{.Title}}</div>
			<div class="gr-now-playing-source">{{.SourceDisplay}}</div>
		</div>
		{{if .HasTimeline}}
		<div class="gr-now-playing-time">
			<span>{{.Position}}</span>
			<span>{{.Duration}}</span>
		</div>
		{{end}}
		<div class="gr-playback-actions">
			{{range .Actions}}
			<form hx-post="/ui/playback/action" hx-target="#gr-now-playing" hx-swap="outerHTML">
				<input type="hidden" name="action" value="{{.ID}}">
				<input type="hidden" name="adapter_ref" value="{{$.AdapterRef}}">
				<input type="hidden" name="generation" value="{{$.Generation}}">
				<button type="submit" title="{{.DisabledReason}}" {{if not .Enabled}}disabled{{end}}>{{.Label}}</button>
			</form>
			{{end}}
			<button type="button" hx-get="/ui/playback/banner?drawer=cast" hx-target="#gr-now-playing" hx-swap="outerHTML">Cast</button>
		</div>
	</div>
	{{if .Seek}}
	{{if .Seek.Enabled}}
	<form class="gr-now-playing-seek" hx-post="/ui/playback/seek" hx-target="#gr-now-playing" hx-swap="outerHTML">
		<input type="hidden" name="adapter_ref" value="{{.AdapterRef}}">
		<input type="hidden" name="generation" value="{{.Generation}}">
		<input type="range" name="offset_ms" min="0" max="{{.Seek.DurationMS}}" value="{{.Seek.OffsetMS}}" aria-label="Playback position">
	</form>
	{{end}}
	{{end}}
	{{if .Message}}
	<div class="gr-now-playing-message {{.MessageKind}}">
		<span>{{.Message}}</span>
		<button type="button" hx-get="/ui/playback/banner" hx-target="#gr-now-playing" hx-swap="outerHTML">Dismiss</button>
	</div>
	{{end}}
	{{if .CastDrawerOpen}}
	<div class="gr-now-playing-drawer">
		<div class="gr-quick-cast-tabs">
			{{range $i, $tab := .QuickCastTabs}}
			<button type="button" hx-get="/ui/playback/banner?drawer=cast&tab={{$tab.ID}}" hx-target="#gr-now-playing" hx-swap="outerHTML" {{if quickCastTabActive $tab $.ActiveQuickCast (eq $i 0)}}aria-current="true"{{end}}>{{$tab.Label}}</button>
			{{end}}
		</div>
		{{range $i, $tab := .QuickCastTabs}}
		{{if quickCastTabActive $tab $.ActiveQuickCast (eq $i 0)}}
		<form class="gr-quick-cast" hx-post="/ui/playback/quick-cast" hx-target="#gr-now-playing" hx-swap="outerHTML" {{if eq $tab.Encoding "multipart"}}enctype="multipart/form-data"{{end}}>
			<input type="hidden" name="tab_id" value="{{$tab.ID}}">
			{{range $tab.Fields}}
			<label>{{.Label}}<input name="{{.Name}}" type="{{.Type}}" placeholder="{{.Placeholder}}" {{if .Required}}required{{end}}></label>
			{{end}}
			<button type="submit" {{if not $tab.Enabled}}disabled{{end}}>Cast</button>
		</form>
		{{end}}
		{{end}}
	</div>
	{{end}}
</section>
{{end}}
```

Update `internal/ui/templates/shell.html` inside the non-setup `<main>` before panel content:

```html
{{if not .SetupMode}}{{template "now-playing-banner.html" .Playback}}{{end}}
```

- [ ] **Step 7: Teach `handlePlaybackBanner` to open drawer from query string**

Modify `handlePlaybackBanner`:

```go
opts := playbackRenderOptions{}
if r.URL.Query().Get("drawer") == "cast" {
	opts.CastDrawerOpen = true
	opts.ActiveQuickCast = strings.TrimSpace(r.URL.Query().Get("tab"))
}
if err := s.tmpl.ExecuteTemplate(w, "now-playing-banner.html", s.buildPlaybackBannerData(r.Context(), opts)); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
```

- [ ] **Step 8: Add shell inclusion/exclusion tests**

Append to `internal/ui/server_test.go`:

```go
func TestShellRendersNowPlayingBannerOnNonSetupPages(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	for _, path := range []string{"/ui/", "/ui/bridge", "/ui/diagnostics"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `id="gr-now-playing"`) {
				t.Fatalf("missing now-playing banner on %s: %s", path, rr.Body.String())
			}
		})
	}
}

func TestSetupShellDoesNotRenderNowPlayingBanner(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/setup", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if strings.Contains(rr.Body.String(), `id="gr-now-playing"`) {
		t.Fatalf("setup page rendered now-playing banner: %s", rr.Body.String())
	}
}
```

- [ ] **Step 9: Run UI banner render tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestPlaybackBanner|TestShellRendersNowPlaying|TestSetupShell" -count=1
```

Expected: PASS.

- [ ] **Step 10: Format and commit banner render slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/ui/playback.go internal/ui/playback_test.go internal/ui/server.go internal/ui/server_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestPlaybackBanner|TestShellRendersNowPlaying|TestSetupShell" -count=1
git add internal/ui/playback.go internal/ui/playback_test.go internal/ui/server.go internal/ui/server_test.go internal/ui/templates/now-playing-banner.html internal/ui/templates/shell.html
git commit -m "feat(ui): render global now playing banner"
```

Expected: commit contains only UI banner render files.

---

## Task 4: Generic Playback Action, Seek, And Quick-Cast Routes

**Files:**
- Modify: `internal/ui/playback.go`
- Modify: `internal/ui/playback_test.go`
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/templates/now-playing-banner.html`

- [ ] **Step 1: Write failing action and stale-key route tests**

Append to `internal/ui/playback_test.go`:

```go
func TestPlaybackActionDispatchesToOwningProvider(t *testing.T) {
	fake := &fakePlaybackAdapter{name: "url", enabled: true, owns: true, actionMsg: "paused"}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 9}}
	})
	form := url.Values{"action": {"pause"}, "adapter_ref": {"url:abc"}, "generation": {"9"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 1 || fake.actionCalls[0].Action != "pause" || fake.actionCalls[0].Generation != 9 {
		t.Fatalf("action calls = %#v", fake.actionCalls)
	}
	if !strings.Contains(rr.Body.String(), "paused") {
		t.Fatalf("success message missing: %s", rr.Body.String())
	}
}

func TestPlaybackActionRejectsSameAdapterStaleGenerationBeforeDispatch(t *testing.T) {
	fake := &fakePlaybackAdapter{name: "url", enabled: true, owns: true}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 10}}
	})
	form := url.Values{"action": {"stop"}, "adapter_ref": {"url:abc"}, "generation": {"9"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 0 {
		t.Fatalf("provider was called for stale action: %#v", fake.actionCalls)
	}
	if !strings.Contains(rr.Body.String(), "active session changed") {
		t.Fatalf("stale error missing: %s", rr.Body.String())
	}
}

func TestPlaybackSeekDispatchesOffset(t *testing.T) {
	fake := &fakePlaybackAdapter{name: "url", enabled: true, owns: true}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 12, Duration: time.Minute}}
	})
	form := url.Values{"adapter_ref": {"url:abc"}, "generation": {"12"}, "offset_ms": {"42000"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/seek", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 1 || fake.actionCalls[0].Action != adapters.PlaybackActionSeek || fake.actionCalls[0].OffsetMS != 42000 {
		t.Fatalf("seek calls = %#v", fake.actionCalls)
	}
}
```

- [ ] **Step 2: Run action tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestPlayback(Action|Seek)" -count=1
```

Expected: FAIL with 404 for action/seek routes.

- [ ] **Step 3: Mount routes**

Modify `internal/ui/server.go`:

```go
s.mountPOST(mux, "/ui/playback/action", s.handlePlaybackAction)
s.mountPOST(mux, "/ui/playback/seek", s.handlePlaybackSeek)
s.mountPOST(mux, "/ui/playback/quick-cast", s.handlePlaybackQuickCast)
```

- [ ] **Step 4: Add action and seek handlers**

Add to `internal/ui/playback.go`:

```go
func (s *Server) handlePlaybackAction(w http.ResponseWriter, r *http.Request) {
	req, err := parsePlaybackActionRequest(r, false)
	if err != nil {
		s.renderPlaybackMessage(w, r, http.StatusBadRequest, "err", err.Error(), false, "")
		return
	}
	s.handlePlaybackMutation(w, r, req)
}

func (s *Server) handlePlaybackSeek(w http.ResponseWriter, r *http.Request) {
	req, err := parsePlaybackActionRequest(r, true)
	if err != nil {
		s.renderPlaybackMessage(w, r, http.StatusBadRequest, "err", err.Error(), false, "")
		return
	}
	req.Action = adapters.PlaybackActionSeek
	s.handlePlaybackMutation(w, r, req)
}

func parsePlaybackActionRequest(r *http.Request, requireOffset bool) (adapters.PlaybackActionRequest, error) {
	if err := r.ParseForm(); err != nil {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("parse form: %w", err)
	}
	gen, err := strconv.ParseUint(strings.TrimSpace(r.Form.Get("generation")), 10, 64)
	if err != nil || gen == 0 {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("generation required")
	}
	req := adapters.PlaybackActionRequest{
		Action:     strings.TrimSpace(r.Form.Get("action")),
		AdapterRef: strings.TrimSpace(r.Form.Get("adapter_ref")),
		Generation: gen,
	}
	if req.AdapterRef == "" {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("adapter_ref required")
	}
	if req.Action == "" && !requireOffset {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("action required")
	}
	if requireOffset {
		offset, err := strconv.Atoi(strings.TrimSpace(r.Form.Get("offset_ms")))
		if err != nil {
			return adapters.PlaybackActionRequest{}, fmt.Errorf("offset_ms must be an integer")
		}
		req.OffsetMS = offset
	}
	return req, nil
}

func (s *Server) handlePlaybackMutation(w http.ResponseWriter, r *http.Request, req adapters.PlaybackActionRequest) {
	snap := s.currentPlaybackSnapshot()
	if snap.AdapterRef != req.AdapterRef || snap.Generation != req.Generation {
		s.renderPlaybackMessage(w, r, http.StatusConflict, "err", "active session changed", false, "")
		return
	}
	provider, ok := s.playbackProviderForSnapshot(snap)
	if !ok {
		s.renderPlaybackMessage(w, r, http.StatusConflict, "err", "active adapter does not expose playback controls", false, "")
		return
	}
	result, err := provider.HandlePlaybackAction(r.Context(), req)
	if err != nil {
		s.renderPlaybackMessage(w, r, http.StatusConflict, "err", err.Error(), false, "")
		return
	}
	s.renderPlaybackMessage(w, r, http.StatusOK, "ok", result.Message, false, "")
}

func (s *Server) currentPlaybackSnapshot() adapters.PlaybackBannerSnapshot {
	view := core.StatusHomeView{State: core.StateIdle}
	if s.cfg.StatusViewer != nil {
		view = s.cfg.StatusViewer.StatusHomeView()
	}
	return adapters.PlaybackBannerSnapshot{
		State:      view.State,
		Source:     view.Source,
		Title:      view.Title,
		AdapterRef: view.AdapterRef,
		Generation: view.Generation,
		Position:   view.Position,
		Duration:   view.Duration,
		StartedAt:  view.StartedAt,
		MediaKind:  view.MediaKind,
		Modeline:   view.Modeline,
	}
}

func (s *Server) renderPlaybackMessage(w http.ResponseWriter, r *http.Request, status int, kind, msg string, drawer bool, tab string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	opts := playbackRenderOptions{Message: msg, MessageKind: kind, CastDrawerOpen: drawer, ActiveQuickCast: tab}
	_ = s.tmpl.ExecuteTemplate(w, "now-playing-banner.html", s.buildPlaybackBannerData(r.Context(), opts))
}
```

- [ ] **Step 5: Write failing quick-cast tests**

Append to `internal/ui/playback_test.go`:

```go
func TestQuickCastDispatchesToSelectedProvider(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:     "url",
		enabled:  true,
		quickMsg: "started",
		tabs: []adapters.QuickCastTab{{
			ID:       "url",
			Label:    "URL",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingForm,
			Fields:   []adapters.QuickCastField{{Name: "url", Label: "URL", Type: "url", Required: true}},
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	form := url.Values{"tab_id": {"url"}, "url": {"https://example.test/video.mp4"}, "mode": {"direct"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/quick-cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.quickCalls) != 1 || fake.quickCalls[0].Values["url"] != "https://example.test/video.mp4" {
		t.Fatalf("quick calls = %#v", fake.quickCalls)
	}
	if !strings.Contains(rr.Body.String(), "started") {
		t.Fatalf("quick-cast success missing: %s", rr.Body.String())
	}
}

func TestQuickCastErrorKeepsDrawerOpen(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:     "url",
		enabled:  true,
		quickErr: errors.New("bad url"),
		tabs: []adapters.QuickCastTab{{ID: "url", Label: "URL", Enabled: true, Encoding: adapters.QuickCastEncodingForm}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	form := url.Values{"tab_id": {"url"}, "url": {"not-a-url"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/quick-cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"bad url", "gr-now-playing-drawer"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("quick-cast error response missing %q: %s", want, rr.Body.String())
		}
	}
}
```

- [ ] **Step 6: Add quick-cast handler**

Add to `internal/ui/playback.go`:

```go
func (s *Server) handlePlaybackQuickCast(w http.ResponseWriter, r *http.Request) {
	req, err := parseQuickCastRequest(r)
	if err != nil {
		s.renderPlaybackMessage(w, r, http.StatusBadRequest, "err", err.Error(), true, "")
		return
	}
	provider, ok := s.quickCastProviderForTab(req.TabID)
	if !ok {
		s.renderPlaybackMessage(w, r, http.StatusBadRequest, "err", "quick-cast provider unavailable", true, req.TabID)
		return
	}
	result, err := provider.HandleQuickCast(r.Context(), req)
	if err != nil {
		s.renderPlaybackMessage(w, r, http.StatusConflict, "err", err.Error(), true, req.TabID)
		return
	}
	msg := result.Message
	if msg == "" {
		msg = "cast started"
	}
	s.renderPlaybackMessage(w, r, http.StatusOK, "ok", msg, false, "")
}

func parseQuickCastRequest(r *http.Request) (adapters.QuickCastRequest, error) {
	ct := r.Header.Get("Content-Type")
	req := adapters.QuickCastRequest{Values: map[string]string{}}
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			return req, fmt.Errorf("parse multipart: %w", err)
		}
		for k, values := range r.MultipartForm.Value {
			if len(values) > 0 {
				req.Values[k] = values[0]
			}
		}
		for k, files := range r.MultipartForm.File {
			if len(files) > 0 {
				req.File = &adapters.QuickCastFile{FieldName: k, Header: files[0]}
				break
			}
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return req, fmt.Errorf("parse form: %w", err)
		}
		for k, values := range r.PostForm {
			if len(values) > 0 {
				req.Values[k] = values[0]
			}
		}
	}
	req.TabID = strings.TrimSpace(req.Values["tab_id"])
	delete(req.Values, "tab_id")
	if req.TabID == "" {
		return req, fmt.Errorf("tab_id required")
	}
	return req, nil
}
```

- [ ] **Step 7: Run route tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestPlayback(Action|Seek)|TestQuickCast" -count=1
```

Expected: PASS.

- [ ] **Step 8: Format and commit route slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/ui/playback.go internal/ui/playback_test.go internal/ui/server.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -count=1
git add internal/ui/playback.go internal/ui/playback_test.go internal/ui/server.go internal/ui/templates/now-playing-banner.html
git commit -m "feat(ui): dispatch global playback actions"
```

Expected: commit contains only generic UI route files.

---

## Task 5: URL Playback Provider And Panel Control Removal

**Files:**
- Modify: `internal/adapters/url/adapter.go`
- Create: `internal/adapters/url/playback_provider.go`
- Create: `internal/adapters/url/playback_provider_test.go`
- Modify: `internal/adapters/url/ui.go`
- Modify: `internal/adapters/url/ui_test.go`

- [ ] **Step 1: Write failing URL provider tests**

Create `internal/adapters/url/playback_provider_test.go`:

```go
package url

import (
	"context"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type providerCoreStub struct {
	status       core.SessionStatus
	pauseRef     string
	pauseGen     uint64
	playRef      string
	playGen      uint64
	stopRef      string
	stopGen      uint64
	seekRef      string
	seekGen      uint64
	seekOffset   int
	startRef     string
	startGen     uint64
	startReq     core.SessionRequest
	startMatched bool
}

func (s *providerCoreStub) StartSession(core.SessionRequest) error { return nil }
func (s *providerCoreStub) Status() core.SessionStatus             { return s.status }
func (s *providerCoreStub) Pause() error                           { return nil }
func (s *providerCoreStub) Play() error                            { return nil }
func (s *providerCoreStub) Stop() error                            { return nil }
func (s *providerCoreStub) SeekTo(int) error                       { return nil }
func (s *providerCoreStub) PauseIfSession(ref string, gen uint64) (bool, error) {
	s.pauseRef, s.pauseGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) PlayIfSession(ref string, gen uint64) (bool, error) {
	s.playRef, s.playGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) StopIfSession(ref string, gen uint64) (bool, error) {
	s.stopRef, s.stopGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) SeekToIfSession(ref string, gen uint64, offset int) (bool, error) {
	s.seekRef, s.seekGen, s.seekOffset = ref, gen, offset
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) StartSessionIfSession(req core.SessionRequest, ref string, gen uint64) (bool, error) {
	s.startReq, s.startRef, s.startGen = req, ref, gen
	return s.startMatched, nil
}

func TestURLPlaybackBannerOwnsURLSession(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 3, Duration: time.Minute}}
	a := &Adapter{core: coreStub}
	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 3, Duration: time.Minute})
	if !owns {
		t.Fatal("URL provider did not own url snapshot")
	}
	if len(view.Actions) != 3 {
		t.Fatalf("actions = %#v, want pause/stop/replay", view.Actions)
	}
	if view.Seek == nil || !view.Seek.Enabled {
		t.Fatalf("seek = %#v, want enabled", view.Seek)
	}
}

func TestURLPlaybackActionUsesFullSessionKey(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 3}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionPause, AdapterRef: "url:abc", Generation: 3})
	if err != nil {
		t.Fatalf("HandlePlaybackAction pause: %v", err)
	}
	if coreStub.pauseRef != "url:abc" || coreStub.pauseGen != 3 {
		t.Fatalf("pause key = %q/%d", coreStub.pauseRef, coreStub.pauseGen)
	}
}
```

- [ ] **Step 2: Run URL provider tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run "TestURLPlayback" -count=1
```

Expected: FAIL with undefined `PlaybackBanner` and `HandlePlaybackAction` or missing full-key methods on `SessionManager`.

- [ ] **Step 3: Extend URL core interface**

Modify `internal/adapters/url/adapter.go`:

```go
type SessionManager interface {
	StartSession(core.SessionRequest) error
	StartSessionIfSession(core.SessionRequest, string, uint64) (bool, error)
	Status() core.SessionStatus
	Pause() error
	Play() error
	Stop() error
	SeekTo(offsetMs int) error
	PauseIfSession(string, uint64) (bool, error)
	PlayIfSession(string, uint64) (bool, error)
	StopIfSession(string, uint64) (bool, error)
	SeekToIfSession(string, uint64, int) (bool, error)
}
```

- [ ] **Step 4: Add URL playback provider**

Create `internal/adapters/url/playback_provider.go`:

```go
package url

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func (a *Adapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	if snap.Source != "url" && !strings.HasPrefix(snap.AdapterRef, "url:") {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	view := adapters.PlaybackBannerAdapterView{SourceDisplay: "URL"}
	if snap.Title != "" {
		view.Title = snap.Title
	}
	if snap.State == core.StatePaused {
		view.Actions = append(view.Actions, adapters.PlaybackAction{ID: adapters.PlaybackActionResume, Label: "Resume", Icon: "play", Enabled: true})
	} else if snap.State == core.StatePlaying {
		view.Actions = append(view.Actions, adapters.PlaybackAction{ID: adapters.PlaybackActionPause, Label: "Pause", Icon: "pause", Enabled: true})
	}
	if snap.State == core.StatePlaying || snap.State == core.StatePaused {
		view.Actions = append(view.Actions,
			adapters.PlaybackAction{ID: adapters.PlaybackActionStop, Label: "Stop", Icon: "stop", Enabled: true},
			adapters.PlaybackAction{ID: adapters.PlaybackActionReplay, Label: "Replay", Icon: "replay", Enabled: true},
		)
	}
	if snap.Duration > 0 {
		view.Seek = &adapters.PlaybackSeek{
			Enabled:    true,
			OffsetMS:   int(snap.Position / time.Millisecond),
			DurationMS: int(snap.Duration / time.Millisecond),
		}
	}
	return view, true
}

func (a *Adapter) HandlePlaybackAction(ctx context.Context, action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if a.core == nil {
		return adapters.PlaybackActionResult{}, fmt.Errorf("core not wired")
	}
	switch action.Action {
	case adapters.PlaybackActionPause:
		return a.pauseBanner(action)
	case adapters.PlaybackActionResume:
		return a.resumeBanner(ctx, action)
	case adapters.PlaybackActionStop:
		return a.stopBanner(action)
	case adapters.PlaybackActionSeek:
		return a.seekBanner(action)
	case adapters.PlaybackActionReplay:
		return a.replayBanner(ctx, action)
	default:
		return adapters.PlaybackActionResult{}, fmt.Errorf("unknown playback action %q", action.Action)
	}
}
```

Add helper methods in the same file:

```go
func (a *Adapter) pauseBanner(action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	matched, err := a.core.PauseIfSession(action.AdapterRef, action.Generation)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, fmt.Errorf("active session changed")
	}
	return adapters.PlaybackActionResult{Message: "paused"}, nil
}

func (a *Adapter) stopBanner(action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	matched, err := a.core.StopIfSession(action.AdapterRef, action.Generation)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, fmt.Errorf("active session changed")
	}
	return adapters.PlaybackActionResult{Message: "stopped"}, nil
}

func (a *Adapter) seekBanner(action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if action.OffsetMS < 0 {
		action.OffsetMS = 0
	}
	matched, err := a.core.SeekToIfSession(action.AdapterRef, action.Generation, action.OffsetMS)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, fmt.Errorf("active session changed")
	}
	return adapters.PlaybackActionResult{Message: "seeked"}, nil
}
```

Implement `resumeBanner` with the existing URL duration behavior:

```go
func (a *Adapter) resumeBanner(ctx context.Context, action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	st := a.core.Status()
	if st.AdapterRef != action.AdapterRef || st.Generation != action.Generation {
		return adapters.PlaybackActionResult{}, fmt.Errorf("active session changed")
	}
	if st.Duration > 0 {
		matched, err := a.core.PlayIfSession(action.AdapterRef, action.Generation)
		if err != nil {
			return adapters.PlaybackActionResult{}, err
		}
		if !matched {
			return adapters.PlaybackActionResult{}, fmt.Errorf("active session changed")
		}
		return adapters.PlaybackActionResult{Message: "resumed"}, nil
	}
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		return adapters.PlaybackActionResult{}, fmt.Errorf("no URL to resume")
	}
	ref, _, status, err := a.castURLGuarded(ctx, lastURL, "auto", action.AdapterRef, action.Generation)
	if err != nil {
		if status == 0 {
			status = http.StatusConflict
		}
		return adapters.PlaybackActionResult{}, fmt.Errorf("%s", redactErr(err, lastURL))
	}
	return adapters.PlaybackActionResult{Message: "resumed " + ref}, nil
}
```

Refactor `castURL` so the session-start operation is injected. Keep all validation, history, resolver, title, `SessionRequest`, state, and logging behavior in one shared helper.

Add this helper shape in `internal/adapters/url/play.go`:

```go
type urlSessionStarter func(core.SessionRequest) (bool, error)

func (a *Adapter) castURL(ctx context.Context, rawURL, mode string) (ref, resolvedVia string, status int, err error) {
	return a.castURLWithStarter(ctx, rawURL, mode, func(req core.SessionRequest) (bool, error) {
		return true, a.core.StartSession(req)
	})
}

func (a *Adapter) castURLGuarded(ctx context.Context, rawURL, mode, expectedRef string, expectedGeneration uint64) (ref, resolvedVia string, status int, err error) {
	return a.castURLWithStarter(ctx, rawURL, mode, func(req core.SessionRequest) (bool, error) {
		return a.core.StartSessionIfSession(req, expectedRef, expectedGeneration)
	})
}

func (a *Adapter) castURLWithStarter(ctx context.Context, rawURL, mode string, starter urlSessionStarter) (ref, resolvedVia string, status int, err error) {
	// Move the existing castURL body here unchanged until the final core start.
	// Replace the direct StartSession call with the starter block below.
}
```

Inside `castURLWithStarter`, replace:

```go
if serr := a.core.StartSession(req); serr != nil {
	// existing failure handling
}
```

with:

```go
matched, serr := starter(req)
if serr != nil {
	safeMsg := strings.ReplaceAll(serr.Error(), rawURL, redactURL(rawURL))
	a.setState(adapters.StateError, safeMsg)
	slog.Warn("url cast failed", "url", redactURL(rawURL), "err", serr)
	return "", "", http.StatusInternalServerError, fmt.Errorf("%s", safeMsg)
}
if !matched {
	return "", "", http.StatusConflict, fmt.Errorf("active session changed")
}
```

- [ ] **Step 5: Add URL quick-cast provider**

Add to `internal/adapters/url/playback_provider.go`:

```go
func (a *Adapter) QuickCastTabs() []adapters.QuickCastTab {
	a.mu.Lock()
	cfg := a.cfg
	probe := a.ytdlpProbe
	a.mu.Unlock()
	fields := []adapters.QuickCastField{{Name: "url", Label: "URL", Type: "url", Placeholder: "https://example.com/video.mp4", Required: true}}
	if cfg.YtdlpEnabled && probe.OK {
		fields = append(fields, adapters.QuickCastField{
			Name: "mode", Label: "Mode", Type: "radio", Required: true,
			Options: []adapters.QuickCastOption{{Value: "auto", Label: "Auto"}, {Value: "ytdlp", Label: "yt-dlp"}, {Value: "direct", Label: "Direct"}},
		})
	}
	return []adapters.QuickCastTab{{
		ID:       "url",
		Label:    "URL",
		Enabled:  a.IsEnabled(),
		Encoding: adapters.QuickCastEncodingForm,
		Fields:   fields,
	}}
}

func (a *Adapter) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	rawURL := strings.TrimSpace(req.Values["url"])
	if rawURL == "" {
		return adapters.QuickCastResult{}, fmt.Errorf("url is required")
	}
	mode := strings.TrimSpace(req.Values["mode"])
	if mode == "" {
		mode = "auto"
	}
	ref, _, _, err := a.castURL(ctx, rawURL, mode)
	if err != nil {
		return adapters.QuickCastResult{}, err
	}
	return adapters.QuickCastResult{Message: "cast started", AdapterRef: ref}, nil
}
```

- [ ] **Step 6: Remove active controls from URL panel**

Modify `internal/adapters/url/ui.go`:

Remove the active-session position paragraph, range input, control row, and inline drag-protection script. Keep:

```go
// URL launch, yt-dlp status, cookies, and history remain in the adapter panel.
```

Keep panel polling at `every 5s`; active transport polling now belongs to the banner.

- [ ] **Step 7: Add URL panel removal test**

Append to `internal/adapters/url/ui_test.go`:

```go
func TestURLPanelDoesNotRenderActiveTransportControls(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 3, Duration: time.Minute}}
	a := &Adapter{core: coreStub, history: LoadHistory("")}
	html := a.renderPanel()
	for _, forbidden := range []string{"/ui/adapter/url/pause", "/ui/adapter/url/resume", "/ui/adapter/url/stop", "/ui/adapter/url/replay", "/ui/adapter/url/seek", `class="scrub"`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("URL panel still renders %q: %s", forbidden, html)
		}
	}
	for _, want := range []string{`hx-post="/ui/adapter/url/play"`, "yt-dlp", "cookies"} {
		if !strings.Contains(html, want) {
			t.Fatalf("URL panel lost expected %q: %s", want, html)
		}
	}
}
```

- [ ] **Step 8: Run URL tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -count=1
```

Expected: PASS.

- [ ] **Step 9: Format and commit URL slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/adapters/url/adapter.go internal/adapters/url/playback_provider.go internal/adapters/url/playback_provider_test.go internal/adapters/url/ui.go internal/adapters/url/ui_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -count=1
git add internal/adapters/url/adapter.go internal/adapters/url/playback_provider.go internal/adapters/url/playback_provider_test.go internal/adapters/url/ui.go internal/adapters/url/ui_test.go
git commit -m "feat(url): move playback controls to global banner"
```

Expected: commit contains only URL adapter files.

---

## Task 6: Streams Playback Provider And Panel Control Removal

**Files:**
- Modify: `internal/adapters/streams/adapter.go`
- Modify: `internal/adapters/streams/playback.go`
- Create: `internal/adapters/streams/playback_provider.go`
- Create: `internal/adapters/streams/playback_provider_test.go`
- Modify: `internal/adapters/streams/ui.go`
- Modify: `internal/adapters/streams/ui_test.go`

- [ ] **Step 1: Write failing Streams provider tests**

Create `internal/adapters/streams/playback_provider_test.go`:

```go
package streams

import (
	"context"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestStreamsPlaybackBannerActions(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.mu.Lock()
	a.active = &ActiveQueue{
		SessionID:    "sess",
		ProviderID:   "mtv",
		ProviderName: "MTV",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Items:        []StreamItem{{ID: "one", SourceID: "one", Title: "One"}, {ID: "two", SourceID: "two", Title: "Two"}},
		baseItems:    []StreamItem{{ID: "one", SourceID: "one", Title: "One"}, {ID: "two", SourceID: "two", Title: "Two"}},
		Index:        0,
		Generation:   4,
		ItemToken:    2,
	}
	a.mu.Unlock()
	ref := "streams:mtv:metal:sess:2"
	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{State: core.StatePlaying, Source: "streams", AdapterRef: ref, Generation: 8})
	if !owns {
		t.Fatal("streams provider did not own streams snapshot")
	}
	got := actionIDs(view.Actions)
	for _, want := range []string{adapters.PlaybackActionNext, adapters.PlaybackActionReplay, adapters.PlaybackActionStop} {
		if !strings.Contains(got, want) {
			t.Fatalf("actions %q missing %q", got, want)
		}
	}
}

func actionIDs(actions []adapters.PlaybackAction) string {
	var ids []string
	for _, a := range actions {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ",")
}
```

- [ ] **Step 2: Run Streams provider tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestStreamsPlaybackBanner" -count=1
```

Expected: FAIL with undefined provider methods.

- [ ] **Step 3: Extend Streams core interface**

Modify `internal/adapters/streams/adapter.go`:

```go
type SessionManager interface {
	StartSession(core.SessionRequest) error
	StartSessionIfIdle(core.SessionRequest) (bool, error)
	PauseIfAdapterRef(string) (bool, error)
	StopIfAdapterRef(string) (bool, error)
	StopIfSession(string, uint64) (bool, error)
	Status() core.SessionStatus
}
```

- [ ] **Step 4: Add Streams provider view**

Create `internal/adapters/streams/playback_provider.go`:

```go
package streams

import (
	"context"
	"fmt"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (a *Adapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	if snap.Source != "streams" && !strings.HasPrefix(snap.AdapterRef, "streams:") {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	a.mu.Lock()
	q := a.active
	if q == nil || activeAdapterRef(q) != snap.AdapterRef {
		a.mu.Unlock()
		return adapters.PlaybackBannerAdapterView{SourceDisplay: "Streams"}, true
	}
	title := q.ChannelName
	subtitle := q.ProviderName
	canPrevious := q.canAdvancePrevious()
	canNext := q.canAdvanceNext()
	canReplay := a.canReplayLocked(q)
	a.mu.Unlock()

	return adapters.PlaybackBannerAdapterView{
		Title:         firstNonEmpty(title, snap.Title),
		Subtitle:      subtitle,
		SourceDisplay: "Streams",
		Actions: []adapters.PlaybackAction{
			{ID: adapters.PlaybackActionPrevious, Label: "Previous", Icon: "skip-back", Enabled: canPrevious, DisabledReason: disabledReason(canPrevious, "no previous item")},
			{ID: adapters.PlaybackActionNext, Label: "Next", Icon: "skip-forward", Enabled: canNext, DisabledReason: disabledReason(canNext, "no next item")},
			{ID: adapters.PlaybackActionReplay, Label: "Replay", Icon: "rotate-ccw", Enabled: canReplay, DisabledReason: disabledReason(canReplay, "cannot replay current queue")},
			{ID: adapters.PlaybackActionStop, Label: "Stop", Icon: "stop", Enabled: true},
		},
	}, true
}

func disabledReason(enabled bool, reason string) string {
	if enabled {
		return ""
	}
	return reason
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
```

- [ ] **Step 5: Add guarded Streams action handler**

Add to `internal/adapters/streams/playback_provider.go`:

```go
func (a *Adapter) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if err := a.ensureOwnsCoreSession(req.AdapterRef, req.Generation); err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	var err error
	switch req.Action {
	case adapters.PlaybackActionPrevious:
		err = a.PreviousGuarded(ctx, req.AdapterRef, req.Generation)
	case adapters.PlaybackActionNext:
		err = a.NextGuarded(ctx, req.AdapterRef, req.Generation)
	case adapters.PlaybackActionReplay:
		err = a.ReplayGuarded(ctx, req.AdapterRef, req.Generation)
	case adapters.PlaybackActionStop:
		err = a.StopQueueGuarded(ctx, req.AdapterRef, req.Generation)
	default:
		err = fmt.Errorf("unknown playback action %q", req.Action)
	}
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	return adapters.PlaybackActionResult{Message: "streams updated"}, nil
}

func (a *Adapter) ensureOwnsCoreSession(ref string, generation uint64) error {
	if a.core == nil {
		return playbackError("", "core playback manager is not configured")
	}
	st := a.core.Status()
	if st.AdapterRef != ref || st.Generation != generation {
		return playbackError("", "active session changed")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil || activeAdapterRef(a.active) != ref {
		return playbackError("", "streams does not own the active queue")
	}
	return nil
}
```

- [ ] **Step 6: Add guarded Streams action methods**

Modify `internal/adapters/streams/playback.go`:

```go
func (a *Adapter) NextGuarded(ctx context.Context, ref string, generation uint64) error {
	return a.moveGuarded(ctx, ref, generation, func(q *ActiveQueue) bool { return q.canAdvanceNext() }, func(q *ActiveQueue) bool { return q.advanceNext(a.rng) })
}

func (a *Adapter) PreviousGuarded(ctx context.Context, ref string, generation uint64) error {
	return a.moveGuarded(ctx, ref, generation, func(q *ActiveQueue) bool { return q.canAdvancePrevious() }, func(q *ActiveQueue) bool { return q.advancePrevious() })
}

func (a *Adapter) moveGuarded(ctx context.Context, ref string, generation uint64, canMove, mutator func(*ActiveQueue) bool) error {
	if err := a.stopPreviousOwnedCoreGuarded(ref, generation, canMove, true, true); err != nil {
		return err
	}
	a.mu.Lock()
	if a.active == nil {
		a.mu.Unlock()
		return playbackError("", "no active streams queue")
	}
	if !mutator(a.active) {
		a.mu.Unlock()
		return playbackError(a.active.ProviderID, "queue has no next item")
	}
	a.active.Failures = nil
	a.mu.Unlock()
	_, err := a.playCurrentGuarded(ctx, queueVersionOf(a.active))
	return err
}
```

Add `stopPreviousOwnedCoreGuarded` next to `stopPreviousOwnedCore`:

```go
func (a *Adapter) stopPreviousOwnedCoreGuarded(ref string, generation uint64, canProceed func(*ActiveQueue) bool, bumpGeneration bool, requireOwned bool) error {
	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return nil
	}
	if canProceed != nil && !canProceed(q) {
		providerID := q.ProviderID
		a.mu.Unlock()
		return playbackError(providerID, "queue has no next item")
	}
	activeRef := activeAdapterRef(q)
	providerID := q.ProviderID
	coreManager := a.core
	hasInFlightResolve := q.cancelResolve != nil
	a.mu.Unlock()

	if activeRef != ref {
		return playbackError(providerID, "active session changed")
	}
	if coreManager == nil || ref == "" {
		if requireOwned && !hasInFlightResolve {
			return playbackError(providerID, "streams does not own the active core session")
		}
		a.cancelAndBumpQueueIfCurrent(q, bumpGeneration)
		return nil
	}

	a.cancelAndBumpQueueIfCurrent(q, bumpGeneration)

	a.playbackMu.Lock()
	matched, err := coreManager.StopIfSession(ref, generation)
	a.playbackMu.Unlock()
	if err != nil {
		return playbackError(providerID, "failed to stop previous stream playback")
	}
	if requireOwned && !matched && !hasInFlightResolve {
		return playbackError(providerID, "active session changed")
	}
	return nil
}
```

Add a guarded start variant so a banner next/previous/replay cannot preempt a foreign session that appears after the guarded stop:

```go
func (a *Adapter) playCurrentIfCoreIdle(ctx context.Context) (streamhandoff.StartResult, error) {
	return a.playCurrentWithStarter(ctx, queueVersion{}, func(coreManager SessionManager, req core.SessionRequest) (bool, error) {
		return coreManager.StartSessionIfIdle(req)
	})
}
```

Refactor `playCurrentGuarded` to call a shared `playCurrentWithStarter`. Existing non-banner code passes:

```go
func(coreManager SessionManager, req core.SessionRequest) (bool, error) {
	return true, coreManager.StartSession(req)
}
```

In `moveGuarded`, replace `a.playCurrentGuarded(ctx, queueVersionOf(a.active))` with `a.playCurrentIfCoreIdle(ctx)`.

- [ ] **Step 7: Remove Streams local transport controls**

Modify `internal/adapters/streams/ui.go` in `renderNowStrip`:

Keep:

```go
button(b, "/ui/adapter/streams/refresh", "Refresh", false, selection)
```

Remove local buttons for:

```go
"/ui/adapter/streams/previous"
"/ui/adapter/streams/next"
"/ui/adapter/streams/replay"
"/ui/adapter/streams/stop"
```

- [ ] **Step 8: Add Streams panel removal test**

Append to `internal/adapters/streams/ui_test.go`:

```go
func TestStreamsPanelDoesNotRenderActiveTransportControls(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.mu.Lock()
	a.active = &ActiveQueue{SessionID: "sess", ProviderID: "mtv", ProviderName: "MTV", ChannelID: "metal", ChannelName: "Metal", Items: []StreamItem{{ID: "one"}, {ID: "two"}}, Index: 0, ItemToken: 1}
	a.mu.Unlock()
	html := a.renderPanel(panelSelectionRequest{})
	for _, forbidden := range []string{"/ui/adapter/streams/previous", "/ui/adapter/streams/next", "/ui/adapter/streams/replay", "/ui/adapter/streams/stop"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("streams panel still renders %q: %s", forbidden, html)
		}
	}
	if !strings.Contains(html, "/ui/adapter/streams/refresh") {
		t.Fatalf("streams panel lost refresh control: %s", html)
	}
}
```

- [ ] **Step 9: Run Streams tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -count=1
```

Expected: PASS.

- [ ] **Step 10: Format and commit Streams slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/adapters/streams/adapter.go internal/adapters/streams/playback.go internal/adapters/streams/playback_provider.go internal/adapters/streams/playback_provider_test.go internal/adapters/streams/ui.go internal/adapters/streams/ui_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -count=1
git add internal/adapters/streams/adapter.go internal/adapters/streams/playback.go internal/adapters/streams/playback_provider.go internal/adapters/streams/playback_provider_test.go internal/adapters/streams/ui.go internal/adapters/streams/ui_test.go
git commit -m "feat(streams): expose playback controls through banner"
```

Expected: commit contains only Streams adapter files.

---

## Task 7: Torrent Playback Provider, Quick-Cast, And Stop Removal

**Files:**
- Modify: `internal/adapters/torrent/adapter.go`
- Create: `internal/adapters/torrent/playback_provider.go`
- Create: `internal/adapters/torrent/playback_provider_test.go`
- Modify: `internal/adapters/torrent/ui.go`
- Modify: `internal/adapters/torrent/ui_test.go`

- [ ] **Step 1: Write failing Torrent provider tests**

Create `internal/adapters/torrent/playback_provider_test.go`:

```go
package torrent

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type torrentProviderCoreStub struct {
	status  core.SessionStatus
	stopRef string
	stopGen uint64
}

func (s *torrentProviderCoreStub) StartSession(core.SessionRequest) error { return nil }
func (s *torrentProviderCoreStub) Status() core.SessionStatus             { return s.status }
func (s *torrentProviderCoreStub) Stop() error                            { return nil }
func (s *torrentProviderCoreStub) StopIfSession(ref string, gen uint64) (bool, error) {
	s.stopRef, s.stopGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}

func TestTorrentPlaybackBannerStop(t *testing.T) {
	coreStub := &torrentProviderCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "torrent:abc", Generation: 4}}
	a := &Adapter{core: coreStub, cfg: Config{Enabled: true, TrafficAcknowledged: true}}
	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{State: core.StatePlaying, Source: "torrent", AdapterRef: "torrent:abc", Generation: 4})
	if !owns {
		t.Fatal("torrent provider did not own torrent snapshot")
	}
	if len(view.Actions) != 1 || view.Actions[0].ID != adapters.PlaybackActionStop {
		t.Fatalf("actions = %#v, want stop", view.Actions)
	}
}

func TestTorrentStopUsesFullSessionKey(t *testing.T) {
	coreStub := &torrentProviderCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "torrent:abc", Generation: 4}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "torrent:abc", Generation: 4})
	if err != nil {
		t.Fatalf("HandlePlaybackAction stop: %v", err)
	}
	if coreStub.stopRef != "torrent:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop key = %q/%d", coreStub.stopRef, coreStub.stopGen)
	}
}
```

- [ ] **Step 2: Run Torrent provider tests and verify they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/torrent -run "TestTorrentPlayback|TestTorrentStop" -count=1
```

Expected: FAIL with undefined provider methods or missing `StopIfSession` on interface.

- [ ] **Step 3: Extend Torrent core interface**

Modify `internal/adapters/torrent/adapter.go`:

```go
type SessionManager interface {
	StartSession(core.SessionRequest) error
	Status() core.SessionStatus
	Stop() error
	StopIfSession(string, uint64) (bool, error)
}
```

- [ ] **Step 4: Add Torrent provider**

Create `internal/adapters/torrent/playback_provider.go`:

```go
package torrent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (a *Adapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	if snap.Source != torrentAdapterName && !strings.HasPrefix(snap.AdapterRef, "torrent:") {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	return adapters.PlaybackBannerAdapterView{
		SourceDisplay: "Torrent",
		Actions: []adapters.PlaybackAction{{
			ID:      adapters.PlaybackActionStop,
			Label:   "Stop",
			Icon:    "stop",
			Enabled: true,
		}},
	}, true
}

func (a *Adapter) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if req.Action != adapters.PlaybackActionStop {
		return adapters.PlaybackActionResult{}, fmt.Errorf("unknown playback action %q", req.Action)
	}
	if a.core == nil {
		return adapters.PlaybackActionResult{}, fmt.Errorf("core not wired")
	}
	matched, err := a.core.StopIfSession(req.AdapterRef, req.Generation)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, fmt.Errorf("active session changed")
	}
	return adapters.PlaybackActionResult{Message: "stopped"}, nil
}
```

- [ ] **Step 5: Add Torrent quick-cast provider**

Add to `internal/adapters/torrent/playback_provider.go`:

```go
func (a *Adapter) QuickCastTabs() []adapters.QuickCastTab {
	enabled := a.IsEnabled()
	a.mu.Lock()
	ack := a.cfg.TrafficAcknowledged
	a.mu.Unlock()
	disabled := ""
	if !enabled {
		disabled = "torrent adapter is disabled"
	} else if !ack {
		disabled = "BitTorrent traffic acknowledgement required"
	}
	return []adapters.QuickCastTab{
		{
			ID:             "torrent-magnet",
			Label:          "Magnet",
			Enabled:        enabled && ack,
			DisabledReason: disabled,
			Encoding:       adapters.QuickCastEncodingForm,
			Fields:         []adapters.QuickCastField{{Name: "magnet", Label: "Magnet", Type: "text", Required: true}},
		},
		{
			ID:             "torrent-file",
			Label:          "Torrent File",
			Enabled:        enabled && ack,
			DisabledReason: disabled,
			Encoding:       adapters.QuickCastEncodingMultipart,
			Fields:         []adapters.QuickCastField{{Name: "torrent_file", Label: "Torrent File", Type: "file", Required: true}},
		},
	}
}

func (a *Adapter) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	switch req.TabID {
	case "torrent-magnet":
		raw := strings.TrimSpace(req.Values["magnet"])
		if raw == "" {
			return adapters.QuickCastResult{}, fmt.Errorf("magnet is required")
		}
		started, err := a.startMagnet(ctx, raw)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	case "torrent-file":
		if req.File == nil || req.File.Header == nil {
			return adapters.QuickCastResult{}, fmt.Errorf("torrent_file is required")
		}
		f, err := req.File.Header.Open()
		if err != nil {
			return adapters.QuickCastResult{}, fmt.Errorf("open torrent_file: %w", err)
		}
		defer f.Close()
		body, err := io.ReadAll(io.LimitReader(f, maxTorrentUploadBytes+1))
		if err != nil {
			return adapters.QuickCastResult{}, fmt.Errorf("read torrent_file: %w", err)
		}
		if len(body) > maxTorrentUploadBytes {
			return adapters.QuickCastResult{}, fmt.Errorf("torrent file exceeds 4 MiB")
		}
		started, err := a.startTorrentBytes(ctx, body)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	default:
		return adapters.QuickCastResult{}, fmt.Errorf("unknown quick-cast tab %q", req.TabID)
	}
}
```

- [ ] **Step 6: Remove Torrent active Stop from panel**

Modify `internal/adapters/torrent/ui.go`:

Remove this block from `renderLiveStatus`:

```go
if view.ActiveToken != "" {
	b.WriteString(`<form hx-post="/ui/adapter/torrent/stop" hx-target="#torrent-live" hx-swap="outerHTML">`)
	b.WriteString(`<button type="submit">Stop</button>`)
	b.WriteString(`</form>`)
}
```

Keep `renderLiveStatus` polling and status text.

- [ ] **Step 7: Add Torrent panel removal test**

Append to `internal/adapters/torrent/ui_test.go`:

```go
func TestTorrentPanelDoesNotRenderActiveStop(t *testing.T) {
	html := renderLiveStatus(statusView{Enabled: true, TrafficAcknowledged: true, ActiveTitle: "Movie", ActiveToken: "tok-1"})
	if strings.Contains(html, "/ui/adapter/torrent/stop") || strings.Contains(html, ">Stop<") {
		t.Fatalf("torrent live status still renders Stop: %s", html)
	}
	if !strings.Contains(html, "Playing") {
		t.Fatalf("torrent live status lost active text: %s", html)
	}
}
```

- [ ] **Step 8: Run Torrent tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/torrent -count=1
```

Expected: PASS.

- [ ] **Step 9: Format and commit Torrent slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/adapters/torrent/adapter.go internal/adapters/torrent/playback_provider.go internal/adapters/torrent/playback_provider_test.go internal/adapters/torrent/ui.go internal/adapters/torrent/ui_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/torrent -count=1
git add internal/adapters/torrent/adapter.go internal/adapters/torrent/playback_provider.go internal/adapters/torrent/playback_provider_test.go internal/adapters/torrent/ui.go internal/adapters/torrent/ui_test.go
git commit -m "feat(torrent): expose banner stop and quick cast"
```

Expected: commit contains only Torrent adapter files.

---

## Task 8: Banner Styling And Layout Safety

**Files:**
- Modify: `internal/ui/static/app.css`
- Modify: `internal/ui/server_test.go`

- [ ] **Step 1: Write failing CSS marker test**

Append to `internal/ui/server_test.go`:

```go
func TestStaticAppCSSIncludesNowPlayingBannerRules(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/app.css", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	css := rr.Body.String()
	for _, want := range []string{
		".gr-now-playing",
		".gr-now-playing--top",
		".gr-now-playing--bottom",
		".gr-now-playing-drawer",
		".gr-playback-actions",
		".gr-quick-cast",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("app.css missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run CSS marker test and verify it fails**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run TestStaticAppCSSIncludesNowPlayingBannerRules -count=1
```

Expected: FAIL with missing `.gr-now-playing`.

- [ ] **Step 3: Add scoped banner CSS**

Append to `internal/ui/static/app.css` near shell layout rules:

```css
.gr-now-playing {
	position: sticky;
	top: 0;
	z-index: 20;
	display: grid;
	gap: 10px;
	padding: 12px 16px;
	border-bottom: 1px solid color-mix(in oklch, currentColor 16%, transparent);
	background: oklch(0.17 0.018 250);
	color: oklch(0.94 0.012 85);
}

.gr-now-playing--top {
	top: 0;
}

.gr-now-playing--bottom {
	top: auto;
	bottom: 0;
	border-top: 1px solid color-mix(in oklch, currentColor 16%, transparent);
	border-bottom: 0;
}

.gr-now-playing-main {
	display: grid;
	grid-template-columns: minmax(180px, 1fr) auto auto;
	gap: 14px;
	align-items: center;
}

.gr-now-playing-copy {
	min-width: 0;
	display: grid;
	gap: 2px;
}

.gr-now-playing-state {
	font: 700 0.72rem/1.2 var(--font-mono, monospace);
	text-transform: uppercase;
	color: oklch(0.78 0.16 80);
}

.gr-now-playing-title,
.gr-now-playing-source {
	overflow: hidden;
	text-overflow: ellipsis;
	white-space: nowrap;
}

.gr-now-playing-title {
	font-weight: 700;
}

.gr-now-playing-source,
.gr-now-playing-time {
	color: oklch(0.72 0.018 85);
}

.gr-now-playing-time {
	display: grid;
	grid-auto-flow: column;
	gap: 8px;
	font: 600 0.82rem/1.2 var(--font-mono, monospace);
}

.gr-playback-actions {
	display: flex;
	flex-wrap: wrap;
	justify-content: flex-end;
	gap: 8px;
}

.gr-now-playing-seek input[type="range"] {
	width: 100%;
}

.gr-now-playing-message {
	display: flex;
	justify-content: space-between;
	gap: 12px;
	padding: 8px 10px;
	border: 1px solid color-mix(in oklch, currentColor 20%, transparent);
}

.gr-now-playing-message.err {
	color: oklch(0.82 0.16 30);
}

.gr-now-playing-message.ok {
	color: oklch(0.82 0.14 145);
}

.gr-now-playing-drawer {
	display: grid;
	gap: 10px;
	padding-top: 10px;
	border-top: 1px solid color-mix(in oklch, currentColor 14%, transparent);
}

.gr-quick-cast-tabs {
	display: flex;
	flex-wrap: wrap;
	gap: 8px;
}

.gr-quick-cast {
	display: grid;
	grid-template-columns: minmax(160px, 1fr) auto;
	gap: 10px;
	align-items: end;
}

@media (max-width: 760px) {
	.gr-now-playing-main {
		grid-template-columns: 1fr;
		align-items: stretch;
	}

	.gr-playback-actions {
		justify-content: flex-start;
	}

	.gr-quick-cast {
		grid-template-columns: 1fr;
	}
}
```

Keep the class names and top/bottom separation exactly as shown. During implementation, choose nearby existing `app.css` color tokens if these literal OKLCH values conflict with the surrounding palette.

- [ ] **Step 4: Run CSS marker test**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run TestStaticAppCSSIncludesNowPlayingBannerRules -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit styling slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run TestStaticAppCSSIncludesNowPlayingBannerRules -count=1
git add internal/ui/static/app.css internal/ui/server_test.go
git commit -m "style(ui): add now playing banner layout"
```

Expected: commit contains CSS and marker test only.

---

## Task 9: Integration Verification And Cleanup

**Files:**
- Modify only files needed to fix failures discovered by the commands below.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/core ./internal/adapters ./internal/ui ./internal/adapters/url ./internal/adapters/streams ./internal/adapters/torrent -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full repository tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run vet**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe vet ./...
```

Expected: PASS.

- [ ] **Step 4: Manual browser checks**

Manual browser checks are advisory in this repo because there is no committed Playwright/browser-test stage. Start the app with the operator's local config after the Go verification passes; if a local runnable config is not available, record that browser checks were not executed.

Check these pages manually:

```text
/ui/
/ui/bridge
/ui/diagnostics
/ui/adapter/url
/ui/adapter/streams
/ui/adapter/torrent
/ui/setup
```

Expected:
- banner appears on every non-setup page;
- banner is absent on setup pages;
- idle banner keeps layout space reserved;
- URL quick-cast opens, starts or reports a redacted error, and closes on success;
- Torrent magnet and torrent-file quick-cast tabs render;
- active URL controls appear only in the banner;
- active Streams previous/next/replay/stop appear only in the banner;
- active Torrent Stop appears only in the banner;
- read-only Plex/Jellyfin/DLNA sessions show status without controls;
- narrow viewport wraps without button text overlap;
- sticky top placement does not cover adapter save buttons.

- [ ] **Step 5: Search for duplicate active transport controls**

Run:

```bash
rg -n "/ui/adapter/(url|streams|torrent)/(pause|resume|stop|replay|seek|previous|next)|class=\"controls\"|streams-now-controls|torrent-live" internal/adapters internal/ui/templates
```

Expected:
- adapter route definitions may still exist for compatibility;
- URL/Streams/Torrent panel render functions do not emit active transport controls;
- `now-playing-banner.html` is the only UI template that emits active playback controls.

- [ ] **Step 6: Final verification before branch completion**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/core/types.go internal/core/manager.go internal/core/manager_test.go internal/adapters/playback.go internal/adapters/playback_test.go internal/ui/playback.go internal/ui/playback_test.go internal/ui/server.go internal/ui/server_test.go internal/adapters/url/adapter.go internal/adapters/url/playback_provider.go internal/adapters/url/playback_provider_test.go internal/adapters/url/ui.go internal/adapters/url/ui_test.go internal/adapters/streams/adapter.go internal/adapters/streams/playback.go internal/adapters/streams/playback_provider.go internal/adapters/streams/playback_provider_test.go internal/adapters/streams/ui.go internal/adapters/streams/ui_test.go internal/adapters/torrent/adapter.go internal/adapters/torrent/playback_provider.go internal/adapters/torrent/playback_provider_test.go internal/adapters/torrent/ui.go internal/adapters/torrent/ui_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./... -count=1
git diff --check
git status --short
```

Expected:
- gofmt produces no additional diff after the final pass;
- full tests pass;
- `git diff --check` reports no whitespace errors;
- `git status --short` contains only intentional implementation files plus any unrelated pre-existing worktree state.

- [ ] **Step 7: Commit final fixes if Step 6 required changes**

Run only if Step 6 created fixes:

```bash
git add internal/core/types.go internal/core/manager.go internal/core/manager_test.go internal/adapters/playback.go internal/adapters/playback_test.go internal/ui/playback.go internal/ui/playback_test.go internal/ui/server.go internal/ui/server_test.go internal/ui/static/app.css internal/ui/templates/now-playing-banner.html internal/ui/templates/shell.html internal/adapters/url/adapter.go internal/adapters/url/play.go internal/adapters/url/playback_provider.go internal/adapters/url/playback_provider_test.go internal/adapters/url/ui.go internal/adapters/url/ui_test.go internal/adapters/streams/adapter.go internal/adapters/streams/playback.go internal/adapters/streams/playback_provider.go internal/adapters/streams/playback_provider_test.go internal/adapters/streams/ui.go internal/adapters/streams/ui_test.go internal/adapters/torrent/adapter.go internal/adapters/torrent/playback_provider.go internal/adapters/torrent/playback_provider_test.go internal/adapters/torrent/ui.go internal/adapters/torrent/ui_test.go
git commit -m "fix(ui): finish now playing banner integration"
```

Expected: commit contains only intentional integration fixes.

---

## Self-Review Notes

- Spec coverage: core generation and stale-key guards are in Task 1; shared adapter package location is in Task 2; banner shell/polling/layout is in Tasks 3 and 8; action/seek/quick-cast routes are in Task 4; URL/Streams/Torrent provider migration and panel control removal are in Tasks 5-7; browser checks and duplicate-control search are in Task 9.
- Layering check: `internal/adapters/playback.go` imports `internal/core`; `internal/core` never imports `internal/adapters`; UI discovers providers through registry type assertions.
- Out-of-scope check: no Plex Companion, Jellyfin reporting, or DLNA eventing changes are planned.
- Risk check: Streams guarded next/previous/replay is the highest-risk slice because it combines queue mutation with core stop/start. Keep that task isolated and commit it separately.
