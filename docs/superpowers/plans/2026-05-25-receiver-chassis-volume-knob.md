# Receiver Chassis Volume Knob Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a physical, receiver-style volume knob to `/receiver` that controls the persisted global `bridge.audio.output_volume` value and syncs across open chassis tabs.

**Architecture:** The chassis gets a narrow live `VolumeViewer` backed by `core.Manager.OutputVolume()` and a narrow `VolumeSaver` backed by `uiserver.BridgeSaver.SaveOutputVolume`. Volume state lives on `TransportData.OutputVolume` because `transport.html` receives `.Transport`, and `volume-knob.js` uses the existing shared `/receiver/events` stream plus a same-origin `POST /receiver/volume` save route.

**Tech Stack:** Go stdlib (`net/http`, `sync`, `time`, `html/template`), existing chassis templates/CSS, vanilla ES2022, existing `cmd.exe /c node --check` syntax verification on Windows-hosted Node when available.

**Spec:** [docs/superpowers/specs/2026-05-25-receiver-chassis-volume-knob-design.md](../specs/2026-05-25-receiver-chassis-volume-knob-design.md)

---

## Scope Check

This is one implementation unit: receiver chassis volume control. It reuses the existing global output-volume saver and data-plane hot-swap path. It does not add mixer controls, mute, balance, settings-drawer fields, old `/ui` changes, or final route cutover.

## File Structure

**New files:**

| Path | Responsibility |
| --- | --- |
| `internal/chassis/volume.go` | `VolumeViewer`, `VolumeSaver`, `handleVolumePost`, volume parsing, and JSON error responses. |
| `internal/chassis/volume_test.go` | Handler tests for success, validation, missing saver, saver error, and same-origin route wrapping. |
| `internal/chassis/static/volume-knob.js` | Physical knob interaction, single-flight save queue, SSE synchronization, and rollback behavior. |

**Modified files:**

| Path | Change |
| --- | --- |
| `internal/core/manager.go` | Add `OutputVolume() int`. |
| `internal/core/manager_test.go` | Cover `OutputVolume` and `SetOutputVolume` visibility through the getter. |
| `internal/chassis/data.go` | Add `TransportData.OutputVolume`; idle snapshot reads `cfg.Bridge.Audio.OutputVolume`. |
| `internal/chassis/session.go` | Add `VolumeViewer` parameter to `snapshotFromSession`; populate `Transport.OutputVolume` for both idle and live states. |
| `internal/chassis/server.go` | Add `Config.VolumeViewer`, `Config.VolumeSaver`, and `Server.volumeViewer` / `Server.volumeSaver`; pass volume viewer at snapshot call sites; mount `/receiver/volume`. |
| `internal/chassis/handler.go` | Pass volume viewer from `handleIndex`. |
| `internal/chassis/events.go` | Add `volume` SSE envelope, initial emission, and diff emission. |
| `internal/chassis/events_test.go` | Update initial burst tests and add volume changed/no-repeat coverage. |
| `internal/chassis/chassis_test.go` | Add fake volume viewer, snapshot tests, template hook assertions, and adjust expected transport structs. |
| `internal/chassis/templates.go` | Add `volumeAngle` template helper for first-paint knob angle. |
| `internal/chassis/templates/transport.html` | Add knob markup after `.seek-time` and before `.gear-btn`. |
| `internal/chassis/templates/shell.html` | Load `volume-knob.js` after `transport.js`. |
| `internal/chassis/static/chassis.css` | Add scoped knob, tick ring, focusable range, and responsive transport grid rules. |
| `cmd/mister-groovy-relay/main.go` | Add `volumeSaverAdapter`; wire `VolumeViewer: coreMgr` and `VolumeSaver: &volumeSaverAdapter{bs: saver}`. |

## Task 1: Core Getter And Snapshot Data

**Files:**
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`
- Create: `internal/chassis/volume.go`
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/handler.go`
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing core getter tests**

Append these tests near `TestManager_SetOutputVolumeUpdatesActivePlane` in `internal/core/manager_test.go`:

```go
func TestManager_OutputVolumeReadsBridgeConfig(t *testing.T) {
	m := newTestManager(t)
	m.mu.Lock()
	m.bridge.Audio.OutputVolume = 64
	m.mu.Unlock()

	if got := m.OutputVolume(); got != 64 {
		t.Fatalf("OutputVolume() = %d, want 64", got)
	}
}

func TestManager_SetOutputVolumeVisibleThroughOutputVolume(t *testing.T) {
	m := newTestManager(t)
	vp := &volumePlane{}
	m.mu.Lock()
	m.plane = vp
	m.mu.Unlock()

	if err := m.SetOutputVolume(37); err != nil {
		t.Fatalf("SetOutputVolume: %v", err)
	}
	if got := m.OutputVolume(); got != 37 {
		t.Fatalf("OutputVolume() = %d, want 37", got)
	}
}
```

- [ ] **Step 2: Run core getter tests to verify they fail**

Run:

```bash
go test ./internal/core -run 'TestManager_OutputVolumeReadsBridgeConfig|TestManager_SetOutputVolumeVisibleThroughOutputVolume'
```

Expected: FAIL with `m.OutputVolume undefined`.

- [ ] **Step 3: Implement `Manager.OutputVolume`**

Add this method in `internal/core/manager.go` near `SetOutputVolume`:

```go
// OutputVolume returns the current global software output gain under m.mu.
// It tracks the in-memory bridge config updated by SetOutputVolume,
// UpdateBridge, and the UI saver hot-swap path.
func (m *Manager) OutputVolume() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bridge.Audio.OutputVolume
}
```

- [ ] **Step 4: Run core getter tests to verify they pass**

Run:

```bash
go test ./internal/core -run 'TestManager_OutputVolumeReadsBridgeConfig|TestManager_SetOutputVolumeVisibleThroughOutputVolume'
```

Expected: PASS.

- [ ] **Step 5: Create `internal/chassis/volume.go` with the viewer interface**

Create `internal/chassis/volume.go` with this initial content:

```go
package chassis

// VolumeViewer is the read-only source for the live global output volume.
// *core.Manager satisfies this structurally via OutputVolume(). Tests use
// fakes. When nil, snapshots fall back to startup config.
type VolumeViewer interface {
	OutputVolume() int
}
```

- [ ] **Step 6: Write failing chassis snapshot tests**

In `internal/chassis/chassis_test.go`, add this fake next to `fakeVisualizerViewer`:

```go
type fakeVolumeViewer struct {
	volume int
}

func (f *fakeVolumeViewer) OutputVolume() int {
	return f.volume
}
```

Add these tests near the existing `TestIdleSnapshot_TransportDataMatchesNewIdleShape` / `snapshotFromSession` tests:

```go
func TestIdleSnapshot_TransportOutputVolumeFromConfig(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 73

	got := idleSnapshot(cfg, fixedNow)

	if got.Transport.OutputVolume != 73 {
		t.Fatalf("Transport.OutputVolume = %d, want 73", got.Transport.OutputVolume)
	}
}

func TestSnapshotFromSession_OutputVolumeViewerOverridesConfigWhenIdle(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 12
	viewer := &fakeVolumeViewer{volume: 88}

	got := snapshotFromSession(cfg, nil, nil, viewer, nil, nil, fixedNow)

	if got.Transport.OutputVolume != 88 {
		t.Fatalf("Transport.OutputVolume = %d, want 88", got.Transport.OutputVolume)
	}
}

func TestSnapshotFromSession_OutputVolumeViewerSurvivesLiveTransportOverwrite(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 12
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Rio",
		Source:     "plex",
		AdapterRef: "plex:track:1",
		Generation: 9,
		Position:   30 * time.Second,
		Duration:   90 * time.Second,
	}}
	viewer := &fakeVolumeViewer{volume: 66}

	got := snapshotFromSession(cfg, sv, nil, viewer, nil, nil, fixedNow)

	if got.Transport.OutputVolume != 66 {
		t.Fatalf("Transport.OutputVolume = %d, want 66", got.Transport.OutputVolume)
	}
}
```

- [ ] **Step 7: Run chassis snapshot tests to verify they fail**

Run:

```bash
go test ./internal/chassis -run 'TestIdleSnapshot_TransportOutputVolumeFromConfig|TestSnapshotFromSession_OutputVolumeViewer'
```

Expected: FAIL with `Transport.OutputVolume undefined` and/or `snapshotFromSession` argument mismatch.

- [ ] **Step 8: Add `OutputVolume` to `TransportData` and idle snapshot**

In `internal/chassis/data.go`, extend `TransportData`:

```go
type TransportData struct {
	State           string
	SeekFillPercent int
	ElapsedTime     string
	TotalTime       string
	PercentPlayed   string
	OffsetMS        int
	DurationMS      int
	ActionsEnabled  ActionsEnabled
	AdapterRef      string
	Generation      uint64
	OutputVolume    int
}
```

In `idleSnapshot`, add `OutputVolume` to the `TransportData` literal:

```go
Transport: TransportData{
	State:           "stopped",
	SeekFillPercent: 0,
	ElapsedTime:     "",
	TotalTime:       "",
	PercentPlayed:   "",
	OffsetMS:        0,
	DurationMS:      0,
	ActionsEnabled:  ActionsEnabled{},
	AdapterRef:      "",
	Generation:      0,
	OutputVolume:    cfg.Bridge.Audio.OutputVolume,
},
```

- [ ] **Step 9: Wire `VolumeViewer` through server config and snapshots**

In `internal/chassis/server.go`, add config/server fields:

```go
	// VolumeViewer is the optional read-only source for global output
	// volume. When nil, chassis falls back to startup bridge config.
	VolumeViewer VolumeViewer
```

```go
	volumeViewer VolumeViewer
```

In `New`, store it:

```go
volumeViewer: cfg.VolumeViewer,
```

Update the cache seed and refresher calls:

```go
s.cache.Set(snapshotFromSession(s.cfg, s.session, s.visualizerViewer, s.volumeViewer, s.transportViewer, s.aux, time.Now()))
```

```go
s.cache.Set(snapshotFromSession(s.cfg, s.session, s.visualizerViewer, s.volumeViewer, s.transportViewer, s.aux, time.Now()))
```

In `internal/chassis/handler.go`, update `handleIndex`:

```go
data := snapshotFromSession(s.cfg, s.session, s.visualizerViewer, s.volumeViewer, s.transportViewer, s.aux, time.Now())
```

In `internal/chassis/events.go`, update `handleEvents` initial snapshot:

```go
last := snapshotFromSession(s.cfg, s.session, s.visualizerViewer, s.volumeViewer, s.transportViewer, s.aux, time.Now())
```

- [ ] **Step 10: Update `snapshotFromSession` signature and volume assignment**

In `internal/chassis/session.go`, change the signature:

```go
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, volv VolumeViewer, tv TransportViewer, aux AUXStarter, now time.Time) ReceiverPageData {
```

After the live/idle branch and before `applyAUXSourceState`, add:

```go
	if volv != nil {
		base.Transport.OutputVolume = volv.OutputVolume()
	} else {
		base.Transport.OutputVolume = cfg.Bridge.Audio.OutputVolume
	}
```

Use this exact placement so the live branch's `base.Transport = buildTransportData(...)` cannot erase the volume value.

- [ ] **Step 11: Update all existing `snapshotFromSession` TEST call sites**

Steps 9-10 already updated the four production call sites (`server.go` x2, `handler.go`, `events.go`). Step 11 targets test files only. Scope the grep accordingly:

```bash
rg -n "snapshotFromSession\\(" internal/chassis --glob '*_test.go'
```

For every remaining test call that does not have a volume viewer, insert `nil` between the visualizer viewer argument and the transport viewer argument. The grep result should contain only `_test.go` paths; if any production file still appears, recheck Steps 9-10 before editing further.

Representative conversions:

```go
// Before:
got := snapshotFromSession(cfg, nil, viewer, nil, nil, fixedNow)

// After:
got := snapshotFromSession(cfg, nil, viewer, nil, nil, nil, fixedNow)
```

```go
// Before:
got := snapshotFromSession(cfg, sv, nil, tv, nil, fixedNow)

// After:
got := snapshotFromSession(cfg, sv, nil, nil, tv, nil, fixedNow)
```

- [ ] **Step 12: Update existing expected `TransportData` literals**

Existing tests that compare full `TransportData` values may now fail if `cfg.Bridge.Audio.OutputVolume` is non-zero. Add `OutputVolume: cfg.Bridge.Audio.OutputVolume` or the explicit test value to the expected literal when the test intentionally compares the whole transport struct.

Example update:

```go
want := TransportData{
	State:           "playing",
	SeekFillPercent: 64,
	ElapsedTime:     "02:08",
	TotalTime:       "03:20",
	PercentPlayed:   "64%",
	OffsetMS:        129999,
	DurationMS:      201234,
	ActionsEnabled:  ActionsEnabled{Previous: true, Next: true, PauseResume: true, Stop: true, Replay: true, Seek: true},
	AdapterRef:      "jellyfin:item:123",
	Generation:      12,
	OutputVolume:    cfg.Bridge.Audio.OutputVolume,
}
```

- [ ] **Step 13: Run gofmt and focused tests**

Run:

```bash
gofmt -w internal/core/manager.go internal/core/manager_test.go internal/chassis/volume.go internal/chassis/data.go internal/chassis/session.go internal/chassis/server.go internal/chassis/handler.go internal/chassis/events.go internal/chassis/chassis_test.go
go test ./internal/core ./internal/chassis -run 'TestManager_OutputVolume|TestManager_SetOutputVolumeVisibleThroughOutputVolume|TestIdleSnapshot_TransportOutputVolumeFromConfig|TestSnapshotFromSession_OutputVolumeViewer'
```

Expected: PASS.

- [ ] **Step 14: Commit Task 1**

```bash
git add internal/core/manager.go internal/core/manager_test.go internal/chassis/volume.go internal/chassis/data.go internal/chassis/session.go internal/chassis/server.go internal/chassis/handler.go internal/chassis/events.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): expose output volume in receiver snapshots"
```

## Task 2: Volume POST Handler And Saver Wiring

**Files:**
- Modify: `internal/chassis/volume.go`
- Create: `internal/chassis/volume_test.go`
- Modify: `internal/chassis/server.go`
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Write handler tests**

Create `internal/chassis/volume_test.go`:

```go
package chassis

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

type fakeVolumeSaver struct {
	calls []int
	err   error
}

func (f *fakeVolumeSaver) SaveOutputVolume(volume int) error {
	f.calls = append(f.calls, volume)
	return f.err
}

func postVolume(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/volume", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVolumePost(w, req)
	return w
}

func TestHandleVolumePost_AcceptsBoundsAndMiddle(t *testing.T) {
	for _, volume := range []int{0, 50, 100} {
		t.Run(strconv.Itoa(volume), func(t *testing.T) {
			saver := &fakeVolumeSaver{}
			s := &Server{volumeSaver: saver}
			w := postVolume(t, s, url.Values{"output_volume": {strconv.Itoa(volume)}}.Encode())
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
			}
			if w.Body.Len() != 0 {
				t.Fatalf("body = %q, want empty", w.Body.String())
			}
			if len(saver.calls) != 1 || saver.calls[0] != volume {
				t.Fatalf("saver calls = %#v, want [%d]", saver.calls, volume)
			}
		})
	}
}

func TestHandleVolumePost_RejectsMalformedMissingNonIntegerAndOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		body string
		msg  string
	}{
		{name: "malformed", body: "%zz", msg: "malformed form body"},
		{name: "missing", body: "", msg: "missing output_volume field"},
		{name: "blank", body: url.Values{"output_volume": {""}}.Encode(), msg: "missing output_volume field"},
		{name: "non integer", body: url.Values{"output_volume": {"loud"}}.Encode(), msg: "output_volume must be an integer"},
		{name: "negative", body: url.Values{"output_volume": {"-1"}}.Encode(), msg: "output_volume must be in 0..100"},
		{name: "too high", body: url.Values{"output_volume": {"101"}}.Encode(), msg: "output_volume must be in 0..100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saver := &fakeVolumeSaver{}
			s := &Server{volumeSaver: saver}
			w := postVolume(t, s, tc.body)
			assertJSONError(t, w, http.StatusBadRequest, tc.msg)
			if len(saver.calls) != 0 {
				t.Fatalf("saver calls = %#v, want none", saver.calls)
			}
		})
	}
}

func TestHandleVolumePost_NilSaverReturnsServiceUnavailable(t *testing.T) {
	s := &Server{}
	w := postVolume(t, s, url.Values{"output_volume": {"50"}}.Encode())
	assertJSONError(t, w, http.StatusServiceUnavailable, "volume save not configured")
}

func TestHandleVolumePost_SaverErrorReturnsGenericInternalError(t *testing.T) {
	saverErr := errors.New("disk path /secret/config.toml unavailable")
	saver := &fakeVolumeSaver{err: saverErr}
	s := &Server{volumeSaver: saver}
	var logs bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(orig) })

	w := postVolume(t, s, url.Values{"output_volume": {"50"}}.Encode())

	assertJSONError(t, w, http.StatusInternalServerError, "internal save failure")
	if strings.Contains(w.Body.String(), saverErr.Error()) || strings.Contains(w.Body.String(), "/secret/config.toml") {
		t.Fatalf("client body leaked saver error: %q", w.Body.String())
	}
	if got := logs.String(); !strings.Contains(got, saverErr.Error()) || !strings.Contains(got, "volume=50") {
		t.Fatalf("log = %q, want volume and full saver error", got)
	}
}

func TestMount_RegistersVolumeRouteThroughRequireSameOrigin(t *testing.T) {
	cfg := nonZeroConfig()
	cfg.VolumeSaver = &fakeVolumeSaver{}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	mux := http.NewServeMux()
	s.Mount(mux)

	body := url.Values{"output_volume": {"50"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/receiver/volume", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status without same-origin = %d, want %d; body=%q", w.Code, http.StatusForbidden, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/receiver/volume", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status with same-origin = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
}
```

- [ ] **Step 2: Run handler tests to verify they fail**

Run:

```bash
go test ./internal/chassis -run 'TestHandleVolumePost|TestMount_RegistersVolumeRoute'
```

Expected: FAIL because `VolumeSaver`, `Server.volumeSaver`, `Config.VolumeSaver`, and `handleVolumePost` are not yet defined.

- [ ] **Step 3: Add `VolumeSaver` and handler implementation**

Extend `internal/chassis/volume.go`:

```go
package chassis

import (
	"log"
	"net/http"
	"strconv"
	"strings"
)

// VolumeViewer is the read-only source for the live global output volume.
// *core.Manager satisfies this structurally via OutputVolume(). Tests use
// fakes. When nil, snapshots fall back to startup config.
type VolumeViewer interface {
	OutputVolume() int
}

// VolumeSaver persists a new global output volume and applies it live.
// main.go wires this through uiserver.BridgeSaver.SaveOutputVolume.
type VolumeSaver interface {
	SaveOutputVolume(volume int) error
}

func (s *Server) handleVolumePost(w http.ResponseWriter, r *http.Request) {
	if s.volumeSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "volume save not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}
	raw := strings.TrimSpace(r.PostFormValue("output_volume"))
	if raw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing output_volume field")
		return
	}
	volume, err := strconv.Atoi(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "output_volume must be an integer")
		return
	}
	if volume < 0 || volume > 100 {
		writeJSONError(w, http.StatusBadRequest, "output_volume must be in 0..100")
		return
	}
	if err := s.volumeSaver.SaveOutputVolume(volume); err != nil {
		log.Printf("chassis: volume save failed: volume=%d err=%v", volume, err)
		writeJSONError(w, http.StatusInternalServerError, "internal save failure")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Wire saver through server config and route**

In `internal/chassis/server.go`, add `VolumeSaver` to `Config`:

```go
	// VolumeSaver is the optional persistence hook for output-volume
	// changes. When nil, chassis renders the knob read-only for POSTs.
	VolumeSaver VolumeSaver
```

Add server field:

```go
	volumeSaver VolumeSaver
```

Store it in `New`:

```go
volumeSaver: cfg.VolumeSaver,
```

Mount the route:

```go
mux.Handle("POST /receiver/volume", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleVolumePost))))
```

Use `transportNoStore` so POST responses match the transport mutation cache behavior.

- [ ] **Step 5: Wire production saver in `main.go`**

In `cmd/mister-groovy-relay/main.go`, add next to `visualizerSaverAdapter`:

```go
type volumeSaverAdapter struct {
	bs *uiserver.BridgeSaver
}

func (a *volumeSaverAdapter) SaveOutputVolume(volume int) error {
	_, err := a.bs.SaveOutputVolume(volume)
	return err
}
```

When constructing `chassis.Config`, add:

```go
VolumeViewer:        coreMgr,
VolumeSaver:         &volumeSaverAdapter{bs: saver},
```

- [ ] **Step 6: Run gofmt and handler tests**

Run:

```bash
gofmt -w internal/chassis/volume.go internal/chassis/volume_test.go internal/chassis/server.go cmd/mister-groovy-relay/main.go
go test ./internal/chassis -run 'TestHandleVolumePost|TestMount_RegistersVolumeRoute'
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add internal/chassis/volume.go internal/chassis/volume_test.go internal/chassis/server.go cmd/mister-groovy-relay/main.go
git commit -m "feat(chassis): persist receiver volume changes"
```

## Task 3: Volume SSE Synchronization

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Add mutable volume viewer test helper**

In `internal/chassis/events_test.go`, add near `mutableSessionViewer`:

```go
type mutableVolumeViewer struct {
	mu     sync.Mutex
	volume int
}

func (m *mutableVolumeViewer) OutputVolume() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.volume
}

func (m *mutableVolumeViewer) set(volume int) {
	m.mu.Lock()
	m.volume = volume
	m.mu.Unlock()
}
```

`events_test.go` already imports `sync`; if not, add it.

- [ ] **Step 2: Write failing SSE tests**

Add these tests to `internal/chassis/events_test.go` near existing SSE event tests. They use the current `newFlushRecorder` pattern from this file rather than adding a second SSE helper stack:

```go
func TestHandleEvents_InitialSnapshotIncludesVolume(t *testing.T) {
	t.Parallel()
	vv := &mutableVolumeViewer{volume: 73}
	cfg := nonZeroConfig()
	cfg.VolumeViewer = vv
	s, err := New(cfg)
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

	if !strings.Contains(body, "event: volume\n") {
		t.Fatalf("SSE stream missing volume event:\n%s", body)
	}
	if !strings.Contains(body, `"outputVolume":73`) {
		t.Fatalf("SSE stream missing outputVolume 73:\n%s", body)
	}
}

func TestHandleEvents_EmitsVolumeWhenChanged(t *testing.T) {
	t.Parallel()
	vv := &mutableVolumeViewer{volume: 40}
	cfg := nonZeroConfig()
	cfg.VolumeViewer = vv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(150 * time.Millisecond)
		vv.set(41)
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)
	body := w.Body.String()

	if got := strings.Count(body, "event: volume\n"); got < 2 {
		t.Fatalf("volume event count = %d, want initial plus changed event; body:\n%s", got, body)
	}
	if !strings.Contains(body, `"outputVolume":40`) || !strings.Contains(body, `"outputVolume":41`) {
		t.Fatalf("SSE stream missing initial/changed volume payloads:\n%s", body)
	}
}

func TestVolumeChanged_ComparesOnlyOutputVolume(t *testing.T) {
	if !volumeChanged(TransportData{OutputVolume: 1}, TransportData{OutputVolume: 2}) {
		t.Fatal("volumeChanged = false, want true for changed output volume")
	}
	if volumeChanged(TransportData{OutputVolume: 2, State: "playing"}, TransportData{OutputVolume: 2, State: "paused"}) {
		t.Fatal("volumeChanged = true, want false for transport-only changes")
	}
}
```

- [ ] **Step 3: Run SSE tests to verify they fail**

Run:

```bash
go test ./internal/chassis -run 'TestHandleEvents_InitialSnapshotIncludesVolume|TestHandleEvents_EmitsVolumeWhenChanged|TestVolumeChanged'
```

Expected: FAIL because no `volume` event or helper exists yet.

- [ ] **Step 4: Add SSE volume envelope and diff helper**

In `internal/chassis/events.go`, add after `transportEnvelope`:

```go
type volumeEnvelope struct {
	OutputVolume int `json:"outputVolume"`
}

func volumeEnvelopeFrom(t TransportData) volumeEnvelope {
	return volumeEnvelope{OutputVolume: t.OutputVolume}
}
```

Add near `transportChanged`:

```go
func volumeChanged(a, b TransportData) bool {
	return a.OutputVolume != b.OutputVolume
}
```

Do not add `OutputVolume` to `transportEnvelope`. Keeping `volume` separate avoids forcing transport UI updates every time only volume changes.

- [ ] **Step 5: Emit initial and changed volume events**

In `handleEvents`, after the initial `transport` emit, add:

```go
	if err := emit(w, "volume", volumeEnvelopeFrom(last.Transport)); err != nil {
		return
	}
```

In the diff loop, after the `transportChanged` block, add:

```go
			if volumeChanged(curr.Transport, last.Transport) {
				if err := emit(w, "volume", volumeEnvelopeFrom(curr.Transport)); err != nil {
					return
				}
				last.Transport.OutputVolume = curr.Transport.OutputVolume
			}
```

Because `transportChanged` updates `last.Transport = curr.Transport`, place `volumeChanged` before `transportChanged` or store `lastVolume := last.Transport.OutputVolume` before the transport block. Use this ordering:

```go
			if volumeChanged(curr.Transport, last.Transport) {
				if err := emit(w, "volume", volumeEnvelopeFrom(curr.Transport)); err != nil {
					return
				}
				last.Transport.OutputVolume = curr.Transport.OutputVolume
			}
			if transportChanged(curr.Transport, last.Transport) {
				if err := emit(w, "transport", transportEnvelopeFrom(curr.Transport)); err != nil {
					return
				}
				last.Transport = curr.Transport
			}
```

With `transportChanged` not comparing `OutputVolume`, this emits one `volume` event for pure volume changes and avoids duplicate transport events.

- [ ] **Step 6: Keep `transportChanged` volume-neutral**

Do not add `OutputVolume` to `transportChanged`. The final function remains:

```go
func transportChanged(a, b TransportData) bool {
	return a.State != b.State ||
		a.SeekFillPercent != b.SeekFillPercent ||
		a.ElapsedTime != b.ElapsedTime ||
		a.TotalTime != b.TotalTime ||
		a.PercentPlayed != b.PercentPlayed ||
		a.OffsetMS != b.OffsetMS ||
		a.DurationMS != b.DurationMS ||
		a.ActionsEnabled != b.ActionsEnabled ||
		a.AdapterRef != b.AdapterRef ||
		a.Generation != b.Generation
}
```

- [ ] **Step 7: Update the initial-burst order assertion**

The only existing order-assertion test is `TestHandleEvents_EmitsInitialVisualizerEventOnConnect` in `internal/chassis/events_test.go` (around line 462). It currently only checks `state < vfd < visualizer < transport` (it ignores the `source` event entirely, even though `source` is emitted in production). Extend it to assert the full canonical order including `source` and the new `volume`:

```go
stateIdx := strings.Index(body, "event: state\n")
vfdIdx := strings.Index(body, "event: vfd\n")
sourceIdx := strings.Index(body, "event: source\n")
vizIdx := strings.Index(body, "event: visualizer\n")
transportIdx := strings.Index(body, "event: transport\n")
volumeIdx := strings.Index(body, "event: volume\n")
if vizIdx < 0 {
    t.Fatalf("body missing initial visualizer event:\n%s", body)
}
if transportIdx < 0 {
    t.Fatalf("body missing initial transport event:\n%s", body)
}
if volumeIdx < 0 {
    t.Fatalf("body missing initial volume event:\n%s", body)
}
if !(stateIdx >= 0 && vfdIdx > stateIdx && sourceIdx > vfdIdx && vizIdx > sourceIdx && transportIdx > vizIdx && volumeIdx > transportIdx) {
    t.Errorf("initial event order should be state, vfd, source, visualizer, transport, volume; body:\n%s", body)
}
```

Also update the error message string in that test so the new canonical order is documented in test output.

- [ ] **Step 7b: Add a no-repeat assertion for unchanged volume**

Per the spec's testing notes, add a test asserting an unchanged volume does NOT emit a second `volume` event after the initial burst. Place it next to `TestHandleEvents_EmitsVolumeWhenChanged`:

```go
func TestHandleEvents_DoesNotRepeatUnchangedVolume(t *testing.T) {
    t.Parallel()
    vv := &mutableVolumeViewer{volume: 40}
    cfg := nonZeroConfig()
    cfg.VolumeViewer = vv
    s, err := New(cfg)
    if err != nil {
        t.Fatalf("New: %v", err)
    }
    t.Cleanup(func() { _ = s.Close() })
    s.Mount(http.NewServeMux())

    ctx, cancel := context.WithCancel(context.Background())
    w := newFlushRecorder()
    req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
    go func() {
        time.Sleep(400 * time.Millisecond)
        cancel()
    }()
    s.handleEvents(w, req)

    if got := strings.Count(w.Body.String(), "event: volume\n"); got != 1 {
        t.Fatalf("volume event count = %d, want exactly 1 (initial only); body:\n%s", got, w.Body.String())
    }
}
```

- [ ] **Step 8: Run gofmt and SSE tests**

Run:

```bash
gofmt -w internal/chassis/events.go internal/chassis/events_test.go
go test ./internal/chassis -run 'TestHandleEvents_InitialSnapshotIncludesVolume|TestHandleEvents_EmitsVolumeWhenChanged|TestHandleEvents_DoesNotRepeatUnchangedVolume|TestVolumeChanged|TestHandleEvents_EmitsInitialVisualizerEventOnConnect'
```

Expected: PASS. The `TestHandleEvents_EmitsInitialVisualizerEventOnConnect` run is non-negotiable — it now enforces the canonical `state, vfd, source, visualizer, transport, volume` order.

- [ ] **Step 9: Commit Task 3**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "feat(chassis): stream receiver volume state"
```

## Task 4: Knob Template And CSS

**Files:**
- Modify: `internal/chassis/templates.go`
- Modify: `internal/chassis/templates/transport.html`
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/chassis_test.go`
- Modify: `internal/chassis/css_scope_test.go` to update the 600 px `body.receiver .transport-strip` contract assertion (the existing pinned `grid-template-columns` value will no longer match after Step 6). The selector-sanity ruleset minimum stays unchanged.

- [ ] **Step 1: Write failing template tests**

In `internal/chassis/chassis_test.go`, add:

```go
func TestVolumeAngle_MapsOutputVolumeToArc(t *testing.T) {
	t.Parallel()
	tests := []struct {
		volume int
		want   int
	}{
		{volume: -10, want: -135},
		{volume: 0, want: -135},
		{volume: 50, want: 0},
		{volume: 100, want: 135},
		{volume: 150, want: 135},
	}
	for _, tc := range tests {
		if got := volumeAngle(tc.volume); got != tc.want {
			t.Errorf("volumeAngle(%d) = %d, want %d", tc.volume, got, tc.want)
		}
	}
}

func TestHandleIndex_RendersVolumeKnobHooks(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Bridge.Audio.OutputVolume = 73
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)

	s.handleIndex(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		`data-volume-knob`,
		`data-volume-value="73"`,
		`data-volume-range`,
		`aria-label="Volume"`,
		`min="0"`,
		`max="100"`,
		`value="73"`,
		`--volume-angle: 62deg`,
		`/receiver/static/volume-knob.js?v=test-1.0.0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("receiver HTML missing %q\n%s", want, body)
		}
	}
}
```

- [ ] **Step 2: Run template tests to verify they fail**

Run:

```bash
go test ./internal/chassis -run 'TestVolumeAngle|TestHandleIndex_RendersVolumeKnobHooks'
```

Expected: FAIL because `volumeAngle` and volume markup/script do not exist.

- [ ] **Step 3: Add `volumeAngle` template helper**

In `internal/chassis/templates.go`, add a helper function below `templateFuncs`:

```go
func volumeAngle(volume int) int {
	if volume < 0 {
		volume = 0
	}
	if volume > 100 {
		volume = 100
	}
	return -135 + int(float64(volume)*2.7)
}
```

Add it to `templateFuncs`:

```go
"volumeAngle": volumeAngle,
```

- [ ] **Step 4: Add script tag**

In `internal/chassis/templates/shell.html`, add after `transport.js`:

```html
  <script defer src="/receiver/static/volume-knob.js?v={{.Version}}"></script>
```

- [ ] **Step 5: Add knob markup to `transport.html`**

In `internal/chassis/templates/transport.html`, add this block after `.seek-time` and before `.gear-btn`:

```html
  <div class="volume-control" data-volume-knob data-volume-value="{{.OutputVolume}}" style="--volume-angle: {{volumeAngle .OutputVolume}}deg">
    <label class="volume-label" for="receiver-volume-range">Volume</label>
    <div class="volume-tick-ring" aria-hidden="true">
      {{range $i, $_ := until 21}}<span class="volume-tick tick-{{$i}}"></span>{{end}}
    </div>
    <div class="volume-dial" aria-hidden="true">
      <span class="volume-notch"></span>
    </div>
    <input id="receiver-volume-range" class="volume-range" data-volume-range type="range" name="output_volume" min="0" max="100" step="1" value="{{.OutputVolume}}" aria-label="Volume">
  </div>
```

The visible label is styled as uppercase `VOLUME`; screen readers can use either the label or the aria-label. Keeping both is acceptable because they match.

- [ ] **Step 6: Add CSS for the knob and responsive grid**

In `internal/chassis/static/chassis.css`, update the transport grid:

```css
body.receiver .transport-strip {
  display: grid;
  grid-template-columns: 80px auto minmax(160px, 1fr) auto auto auto;
  gap: 12px;
  align-items: center;
}
```

Add below the seek-time styles:

```css
body.receiver .volume-control {
  position: relative;
  width: 82px;
  height: 72px;
  display: grid;
  place-items: center;
  --volume-angle: -135deg;
}

body.receiver .volume-label {
  position: absolute;
  left: 50%;
  bottom: 0;
  transform: translateX(-50%);
  color: #8a8a8e;
  font: 700 8px 'Inter', sans-serif;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  white-space: nowrap;
}

body.receiver .volume-tick-ring {
  position: absolute;
  width: 72px;
  height: 72px;
  border-radius: 50%;
  pointer-events: none;
}

body.receiver .volume-tick {
  position: absolute;
  left: 50%;
  top: 50%;
  width: 1px;
  height: 7px;
  transform-origin: 0 30px;
  background: #6d6d72;
  opacity: 0.72;
}

body.receiver .volume-tick:nth-child(5n + 1) {
  height: 10px;
  background: #a8a8ae;
  opacity: 0.9;
}
```

Add the 21 tick rotations:

```css
body.receiver .volume-tick.tick-0 { transform: rotate(-135deg) translateY(-30px); }
body.receiver .volume-tick.tick-1 { transform: rotate(-121.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-2 { transform: rotate(-108deg) translateY(-30px); }
body.receiver .volume-tick.tick-3 { transform: rotate(-94.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-4 { transform: rotate(-81deg) translateY(-30px); }
body.receiver .volume-tick.tick-5 { transform: rotate(-67.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-6 { transform: rotate(-54deg) translateY(-30px); }
body.receiver .volume-tick.tick-7 { transform: rotate(-40.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-8 { transform: rotate(-27deg) translateY(-30px); }
body.receiver .volume-tick.tick-9 { transform: rotate(-13.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-10 { transform: rotate(0deg) translateY(-30px); }
body.receiver .volume-tick.tick-11 { transform: rotate(13.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-12 { transform: rotate(27deg) translateY(-30px); }
body.receiver .volume-tick.tick-13 { transform: rotate(40.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-14 { transform: rotate(54deg) translateY(-30px); }
body.receiver .volume-tick.tick-15 { transform: rotate(67.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-16 { transform: rotate(81deg) translateY(-30px); }
body.receiver .volume-tick.tick-17 { transform: rotate(94.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-18 { transform: rotate(108deg) translateY(-30px); }
body.receiver .volume-tick.tick-19 { transform: rotate(121.5deg) translateY(-30px); }
body.receiver .volume-tick.tick-20 { transform: rotate(135deg) translateY(-30px); }
```

Add dial and range rules:

```css
body.receiver .volume-dial {
  position: relative;
  width: 52px;
  height: 52px;
  border: 1px solid #050506;
  border-radius: 50%;
  background:
    radial-gradient(circle at 32% 28%, rgba(255,255,255,0.55) 0 8%, transparent 9%),
    radial-gradient(circle at 50% 54%, #c8c8cc 0%, #85858c 44%, #3a3a3e 70%, #111113 100%);
  box-shadow:
    inset 0 2px 3px rgba(255,255,255,0.28),
    inset 0 -5px 8px rgba(0,0,0,0.55),
    0 4px 10px rgba(0,0,0,0.65);
  transform: rotate(var(--volume-angle));
  transition: transform 120ms cubic-bezier(0.22,1,0.36,1), filter 120ms ease;
}

body.receiver .volume-notch {
  position: absolute;
  left: 50%;
  top: 5px;
  width: 3px;
  height: 16px;
  transform: translateX(-50%);
  border-radius: 2px;
  background: #121214;
  box-shadow: inset 0 1px 1px rgba(255,255,255,0.18);
}

body.receiver .volume-range {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  opacity: 0;
  cursor: pointer;
}

body.receiver .volume-control:focus-within .volume-dial {
  filter: brightness(1.12);
  box-shadow:
    inset 0 2px 3px rgba(255,255,255,0.28),
    inset 0 -5px 8px rgba(0,0,0,0.55),
    0 0 0 2px var(--vfd-glow-soft),
    0 0 12px var(--vfd-glow-soft);
}

body.receiver .volume-control.saving .volume-dial {
  filter: brightness(1.08);
}

body.receiver .volume-control.failed .volume-dial {
  filter: saturate(0.7) brightness(0.9);
}
```

Update breakpoints:

```css
@container chassis (max-width: 600px) {
  body.receiver .transport-strip {
    grid-template-columns: 60px auto 1fr auto auto;
  }

  body.receiver .volume-control {
    width: 66px;
    height: 62px;
  }

  body.receiver .volume-dial {
    width: 44px;
    height: 44px;
  }
}

@container chassis (max-width: 420px) {
  body.receiver .transport-strip {
    grid-template-columns: 48px 1fr auto auto;
    grid-template-areas:
      "label controls controls controls"
      "label seek volume gear";
    gap: 6px;
  }

  body.receiver .volume-control {
    grid-area: volume;
    width: 58px;
    height: 58px;
  }
}
```

Preserve the existing `label`, `controls`, `seek`, and `gear` grid-area rules in that same breakpoint block.

- [ ] **Step 6b: Update the 600 px transport-strip contract assertion**

`internal/chassis/css_scope_test.go` pins the 600 px breakpoint grid in `TestChassisCSS_Task24ResponsiveContainerContracts` (look for the `"600 collapses transport controls"` row). Today its `want` is:

```go
want: []string{"grid-template-columns: 60px auto 1fr auto;"},
```

After Step 6 the grid is 5-column. Update the row to:

```go
want: []string{"grid-template-columns: 60px auto 1fr auto auto;"},
```

Do not add a new 420 px transport-strip contract row — the existing 420 px contract block does not include `.transport-strip` (verify with `rg -n "max-width: 420" internal/chassis/css_scope_test.go`), so introducing one would be scope creep beyond this plan.

- [ ] **Step 7: Run CSS/template tests to verify they pass**

Run:

```bash
gofmt -w internal/chassis/templates.go internal/chassis/chassis_test.go
go test ./internal/chassis -run 'TestVolumeAngle|TestHandleIndex_RendersVolumeKnobHooks|TestChassisCSS_AllSelectorsScoped|TestChassisCSS_RulesetCountSanity|TestChassisCSS_Task24ResponsiveContainerContracts'
```

Expected: PASS. If `TestChassisCSS_RulesetCountSanity` fails only because the ruleset count increased, leave the minimum unchanged; the test wants a lower bound. If `TestChassisCSS_Task24ResponsiveContainerContracts` still fails for the 600 px row, the Step 6 grid value and the Step 6b expected value must match character-for-character.

- [ ] **Step 8: Commit Task 4**

```bash
git add internal/chassis/templates.go internal/chassis/templates/transport.html internal/chassis/templates/shell.html internal/chassis/static/chassis.css internal/chassis/chassis_test.go internal/chassis/css_scope_test.go
git commit -m "feat(chassis): render receiver volume knob"
```

## Task 5: Volume Knob Client Script

**Files:**
- Create: `internal/chassis/static/volume-knob.js`

- [ ] **Step 1: Add the client script**

Create `internal/chassis/static/volume-knob.js`:

```js
// Receiver chassis physical volume knob.
//
// Uses the shared /receiver/events EventSource exposed by vfd-live.js and
// posts global output-volume changes back to /receiver/volume.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('volume-knob: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  const MIN = 0;
  const MAX = 100;
  const START_DEG = -135;
  const ARC_DEG = 270;
  const SAVE_INTERVAL_MS = 200;

  let source = null;
  let root = null;
  let range = null;
  let authoritative = 100;
  let localValue = 100;
  let inFlight = false;
  let queuedValue = null;
  let editing = false;
  let finalCommitNeeded = false;
  let saveTimer = 0;

  function clamp(value) {
    const n = Number.parseInt(value, 10);
    if (!Number.isFinite(n)) {
      return MIN;
    }
    return Math.min(MAX, Math.max(MIN, n));
  }

  function angleFor(value) {
    return START_DEG + (ARC_DEG * clamp(value) / MAX);
  }

  function setVisual(value, className) {
    localValue = clamp(value);
    if (root) {
      root.dataset.volumeValue = String(localValue);
      root.style.setProperty('--volume-angle', `${Math.round(angleFor(localValue))}deg`);
      root.classList.toggle('saving', className === 'saving');
      root.classList.toggle('failed', className === 'failed');
    }
    if (range && range.value !== String(localValue)) {
      range.value = String(localValue);
    }
  }

  function postVolume(value) {
    const body = new URLSearchParams();
    body.set('output_volume', String(clamp(value)));
    return fetch('/receiver/volume', {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body,
    });
  }

  function scheduleSave(value, finalCommit) {
    queuedValue = clamp(value);
    finalCommitNeeded = finalCommitNeeded || Boolean(finalCommit);
    if (saveTimer) {
      return;
    }
    saveTimer = window.setTimeout(() => {
      saveTimer = 0;
      drainSaves();
    }, finalCommit ? 0 : SAVE_INTERVAL_MS);
  }

  async function drainSaves() {
    if (inFlight || queuedValue === null) {
      return;
    }
    const value = queuedValue;
    const wasFinal = finalCommitNeeded;
    queuedValue = null;
    finalCommitNeeded = false;
    inFlight = true;
    setVisual(localValue, 'saving');
    try {
      const res = await postVolume(value);
      if (res.status !== 204) {
        const text = await res.text().catch(() => '');
        console.warn('volume-knob: save failed', res.status, text);
        if (wasFinal || queuedValue === null) {
          setVisual(authoritative, 'failed');
          window.setTimeout(() => setVisual(authoritative, ''), 350);
        }
      }
    } catch (err) {
      console.warn('volume-knob: volume POST failed', err);
      if (wasFinal || queuedValue === null) {
        setVisual(authoritative, 'failed');
        window.setTimeout(() => setVisual(authoritative, ''), 350);
      }
    } finally {
      inFlight = false;
      if (queuedValue !== null) {
        drainSaves();
      } else if (!editing) {
        setVisual(localValue, '');
      }
    }
  }

  function beginEdit() {
    editing = true;
  }

  function updateEdit(value) {
    const next = clamp(value);
    setVisual(next, '');
    scheduleSave(next, false);
  }

  function commitEdit(value) {
    editing = false;
    const next = clamp(value);
    setVisual(next, '');
    scheduleSave(next, true);
    drainSaves();
  }

  function handleVolumeEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      const next = clamp(data.outputVolume);
      authoritative = next;
      if (!editing && !inFlight && queuedValue === null) {
        setVisual(next, '');
      }
    } catch (err) {
      console.warn('volume-knob: bad volume payload', ev.data, err);
    }
  }

  function attachSource(nextSource) {
    if (!nextSource || nextSource === source) {
      return;
    }
    if (source) {
      source.removeEventListener('volume', handleVolumeEvent);
    }
    source = nextSource;
    source.addEventListener('volume', handleVolumeEvent);
  }

  function bind() {
    if (!root || !range) {
      return;
    }
    authoritative = clamp(root.dataset.volumeValue || range.value);
    setVisual(authoritative, '');

    range.addEventListener('pointerdown', () => beginEdit());
    range.addEventListener('input', () => updateEdit(range.value));
    range.addEventListener('change', () => commitEdit(range.value));
    range.addEventListener('blur', () => {
      if (editing) {
        commitEdit(range.value);
      }
    });
    range.addEventListener('keydown', (ev) => {
      if (ev.key === 'Home' || ev.key === 'End' || ev.key === 'PageUp' || ev.key === 'PageDown' || ev.key.startsWith('Arrow')) {
        beginEdit();
      }
    });
    root.addEventListener('wheel', (ev) => {
      if (document.activeElement !== range && !root.matches(':hover')) {
        return;
      }
      ev.preventDefault();
      beginEdit();
      const delta = ev.deltaY < 0 ? 1 : -1;
      const next = clamp(localValue + delta);
      updateEdit(next);
      commitEdit(next);
    }, { passive: false });
  }

  function init() {
    root = document.querySelector('[data-volume-knob]');
    range = document.querySelector('[data-volume-range]');
    bind();
    if (window.Chassis.events && window.Chassis.events.source) {
      attachSource(window.Chassis.events.source);
    }
    document.addEventListener('chassis:eventsource', (ev) => {
      attachSource(ev.detail && ev.detail.source);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
```

- [ ] **Step 2: Run JS syntax check**

Run:

```bash
cmd.exe /c node --check internal/chassis/static/volume-knob.js
```

Expected: PASS with no output.

- [ ] **Step 3: Add focused static verification by grep**

Run:

```bash
rg -n "SAVE_INTERVAL_MS|addEventListener\\('volume'|/receiver/volume|finalCommitNeeded|data-volume" internal/chassis/static/volume-knob.js
```

Expected: the command prints matches for all five patterns.

- [ ] **Step 4: Commit Task 5**

```bash
git add internal/chassis/static/volume-knob.js
git commit -m "feat(chassis): handle receiver volume knob input"
```

## Task 6: End-To-End Verification And Cleanup

**Files:**
- Modify only files already touched by Tasks 1-5 for fixes discovered during verification: `internal/core/manager.go`, `internal/core/manager_test.go`, `internal/chassis/*`, and `cmd/mister-groovy-relay/main.go`.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./internal/core ./internal/chassis ./internal/uiserver ./internal/ui
```

Expected: PASS.

- [ ] **Step 2: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run CSS and JS checks**

Run:

```bash
go test ./internal/chassis -run 'TestChassisCSS'
cmd.exe /c node --check internal/chassis/static/volume-knob.js
```

Expected: PASS.

- [ ] **Step 4: Manual receiver verification**

Start the bridge in the usual local development mode for this repo, then open `/receiver`. Verify:

- knob appears between transport time and setup gear;
- knob angle matches configured `bridge.audio.output_volume`;
- drag changes the knob and persists after release;
- wheel over the knob adjusts by one step;
- Tab focuses the range and focus styling is visible on the knob;
- Arrow keys, PageUp/PageDown, Home, and End work;
- two tabs stay synchronized after one tab changes volume;
- DevTools EventStream pane shows `event: volume` on connect and after each successful POST;
- invalid direct POST returns JSON error (substitute the local `bridge.ui.http_port` for `32500` if your `config.toml` overrides it):

```bash
curl -i -X POST http://localhost:32500/receiver/volume \
  -H 'Sec-Fetch-Site: same-origin' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data 'output_volume=101'
```

Expected invalid POST response includes:

```text
HTTP/1.1 400 Bad Request
{"error":"output_volume must be in 0..100"}
```

- [ ] **Step 5: Check git state for unintended files**

Run:

```bash
git status --short
```

Expected: only intended volume-knob implementation files are modified or the worktree is clean after task commits. Do not revert unrelated pre-existing user changes.

- [ ] **Step 6: Final commit if verification required fixes**

If Step 1-4 required any fixes after Task 5, commit only those touched implementation files:

```bash
git add internal/core/manager.go internal/core/manager_test.go internal/chassis cmd/mister-groovy-relay/main.go
git commit -m "fix(chassis): finish receiver volume knob verification"
```

Skip this commit when no files changed after Task 5.

- [ ] **Step 7: Final summary**

Prepare a completion note with:

- commits created;
- tests run and pass/fail status;
- manual browser checks completed or not completed;
- any remaining limitations, especially if `cmd.exe /c node --check` or browser verification could not run.
