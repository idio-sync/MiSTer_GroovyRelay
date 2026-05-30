# Receiver Audio Tone/EQ Strip — Plan 3: Chassis Strip UI + Volume Relocation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the always-on audio strip to the chassis face — tone/balance knobs, a 10-band EQ, preset/memory buttons, and switches — wired to live preview/commit routes and an `audioDsp` SSE event, with the existing volume knob relocated into the strip; finish with integration coverage.

**Architecture:** The strip is a new `audio-strip.html` module mounted between `transport` and `visualizer-bank`. Volume relocation is a **template move** — the `.volume-control` markup moves from `transport.html` into the strip; `volume-knob.js`, the `volume` SSE event, and `POST /receiver/volume` are untouched. New `POST /receiver/audio/dsp` (preview/commit a partial patch) and `POST /receiver/audio/dsp/memory` (store/recall) routes call `core.Manager` (`PreviewAudioDSP`/`AudioDSP`) and the bridge saver (`SaveAudioDSP`/memory) from Plan 2. A new `audioDsp` SSE event mirrors the `volume` event so other browsers track changes. `audio-strip.js` handles the new controls (knobs/sliders/switches/presets/memories), posting previews while dragging and a commit on release, and syncs from the `audioDsp` event.

**Tech Stack:** Go 1.26 `html/template` + `net/http`, vanilla ES2022, the Plan 1/2 `audiodsp` + `config.AudioDSP` + core preview/commit + `BridgeSaver.SaveAudioDSP`.

**Spec reference:** [`docs/superpowers/specs/2026-05-29-receiver-audio-tone-eq-strip-design.md`](../specs/2026-05-29-receiver-audio-tone-eq-strip-design.md) — §Chassis UI, §Error handling, §Testing.

**Plan set:** **Plan 3 of 3.** Depends on Plans 1 & 2 (engine + config/core/saver). After this, the feature is end-to-end. Exact CSS/pixel styling is **finalized at build time against the live app** per the spec (Task 8 provides a working first pass; polish there).

**Prereqs:** Plans 1 & 2 merged. `core.Manager` exposes `AudioDSP()`/`PreviewAudioDSP()`/`SetAudioDSP()`; `uiserver.BridgeSaver` exposes `SaveAudioDSP`/`SaveAudioDSPMemory`/`RecallAudioDSPMemory`; `StatusHomeView` carries `AudioDSP`/`AudioDSPEngaged`/`AudioDSPPersisted`.

---

## File Structure

**Modified:**
- `internal/chassis/data.go` — `AudioStripData` type; `ReceiverPageData.AudioStrip`; populate in `idleSnapshot` + the live snapshot builder.
- `internal/chassis/events.go` — `audioDspEnvelope` + `audioDspEnvelopeFrom` + `audioDSPChanged`; initial emit + diff-ticker emit.
- `internal/chassis/server.go` — `Config` + `Server` fields (`AudioDSPController`, `AudioDSPSaver`); mount the two routes.
- `internal/chassis/templates/shell.html` — mount `audio-strip` between `transport` and `visualizer-bank`.
- `internal/chassis/templates/transport.html` — remove the `.volume-control` block.
- `internal/chassis/static/chassis.css` — strip styles (first pass; polish at build).
- `cmd/mister-groovy-relay/main.go` — `audioDSPSaverAdapter`; wire `AudioDSPController`/`AudioDSPSaver` into `chassis.Config`.
- Various `_test.go` — migrate volume-in-transport assertions to the strip.

**Created:**
- `internal/chassis/audiodsp_routes.go` — interfaces + `handleAudioDSPPost` + `handleAudioDSPMemoryPost` + patch merge.
- `internal/chassis/audiodsp_routes_test.go` — handler tests.
- `internal/chassis/templates/audio-strip.html` — the strip template.
- `internal/chassis/static/audio-strip.js` — control interactions.
- `internal/chassis/testdata/audio-strip.behavior.test.js` — DOM/behavior test (matches the repo's existing JS behavior-test convention).
- `cmd/mister-groovy-relay/audio_dsp_e2e_test.go` — integration-tag end-to-end.

---

## Task 1: `AudioStripData` + snapshot population

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/data_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/data_test.go`:

```go
func TestIdleSnapshot_SeedsAudioStripFromConfig(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t) // existing helper used by sibling data tests
	cfg.Bridge.Audio.DSP = config.DefaultAudioDSP()
	cfg.Bridge.Audio.DSP.Bass = 4
	snap := idleSnapshot(cfg, time.Unix(0, 0))
	if snap.AudioStrip.Bass != 4 {
		t.Errorf("idle AudioStrip.Bass = %v, want 4", snap.AudioStrip.Bass)
	}
	if len(snap.AudioStrip.EQ) != 10 {
		t.Errorf("idle AudioStrip.EQ length = %d, want 10", len(snap.AudioStrip.EQ))
	}
	if snap.AudioStrip.OutputVolume != cfg.Bridge.Audio.OutputVolume {
		t.Errorf("AudioStrip.OutputVolume = %d, want %d", snap.AudioStrip.OutputVolume, cfg.Bridge.Audio.OutputVolume)
	}
}
```

If `testConfig` isn't the helper name, use whatever the sibling tests in
`data_test.go` use to build a `chassis.Config` with a `Bridge`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestIdleSnapshot_SeedsAudioStrip -v`
Expected: FAIL — `AudioStripData`/`ReceiverPageData.AudioStrip` undefined.

- [ ] **Step 3: Add the type + populate snapshots**

In `internal/chassis/data.go`, add `AudioStrip` to `ReceiverPageData` (after `Transport TransportData`, line 24):

```go
	AudioStrip AudioStripData
```

Add the type (near `TransportData`):

```go
// AudioStripData drives the always-on audio/EQ face module. Tone/balance/EQ
// are the live tone-control values; Memory carries per-slot labels + stored
// flags for the M1..M3 buttons. OutputVolume duplicates the transport's
// volume so the relocated knob renders from this module's data; the volume
// SSE event + volume-knob.js are unchanged.
type AudioStripData struct {
	Enabled      bool // false = defeat (EQ OUT engaged)
	Mono         bool
	Subsonic     bool
	Loudness     bool
	Bass         float64
	Mid          float64
	Treble       float64
	Balance      int
	EQ           []float64 // 10 bands
	Memory       [3]AudioStripMemory
	Engaged      bool // status-bar EQ LED
	Persisted    bool // false while a preview runs ahead of disk
	OutputVolume int
}

type AudioStripMemory struct {
	Slot   int
	Name   string
	Stored bool
}
```

Add a builder from `config.AudioDSP`:

```go
// audioStripFromDSP flattens a config.AudioDSP (+ volume + persisted flag)
// into the render/SSE struct, normalizing the 3 memory slots so the template
// always renders M1..M3.
func audioStripFromDSP(d config.AudioDSP, engaged, persisted bool, volume int) AudioStripData {
	out := AudioStripData{
		Enabled: d.Enabled, Mono: d.Mono, Subsonic: d.Subsonic, Loudness: d.Loudness,
		Bass: d.Bass, Mid: d.Mid, Treble: d.Treble, Balance: d.Balance,
		EQ:           append([]float64(nil), d.EQ...),
		Engaged:      engaged,
		Persisted:    persisted,
		OutputVolume: volume,
	}
	if len(out.EQ) != 10 {
		eq := make([]float64, 10)
		copy(eq, out.EQ)
		out.EQ = eq
	}
	for i := range out.Memory {
		out.Memory[i] = AudioStripMemory{Slot: i + 1}
	}
	for _, m := range d.Memory {
		if m.Slot >= 1 && m.Slot <= 3 {
			out.Memory[m.Slot-1] = AudioStripMemory{Slot: m.Slot, Name: m.Name, Stored: m.Stored}
		}
	}
	return out
}
```

In `idleSnapshot` (data.go:609), set `AudioStrip` from config — add to the
`ReceiverPageData{...}` literal (next to where `Transport` is built, near line 677):

```go
		AudioStrip: audioStripFromDSP(
			cfg.Bridge.Audio.DSP,
			cfg.Bridge.Audio.DSP.Engaged(),
			true, // idle = persisted (no preview)
			cfg.Bridge.Audio.OutputVolume,
		),
```

In the **live snapshot builder** (the function that maps `core.StatusHomeView`
→ `ReceiverPageData`; find it with the grep below), set `AudioStrip` from the view:

```bash
grep -n "OutputVolume:" internal/chassis/*.go   # the live builder sets Transport.OutputVolume from the view
```

In that builder, after the transport population, add:

```go
	snap.AudioStrip = audioStripFromDSP(view.AudioDSP, view.AudioDSPEngaged, view.AudioDSPPersisted, view.Meter.Pipeline.AudioOutputVolume)
```

(Use the same `OutputVolume` source the live builder already uses for
`Transport.OutputVolume`; in the live path that is the view's audio output
volume field.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run TestIdleSnapshot_SeedsAudioStrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/data.go internal/chassis/data_test.go
git commit -m "feat(chassis): AudioStripData + snapshot population"
```

---

## Task 2: `audioDsp` SSE event

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/events_test.go`:

```go
func TestAudioDSPChanged(t *testing.T) {
	t.Parallel()
	a := AudioStripData{EQ: make([]float64, 10)}
	b := a
	if audioDSPChanged(a, b) {
		t.Error("identical strips should not change")
	}
	b.Bass = 3
	if !audioDSPChanged(a, b) {
		t.Error("bass change should be detected")
	}
	c := a
	c.EQ = append([]float64(nil), a.EQ...)
	c.EQ[2] = -1
	if !audioDSPChanged(a, c) {
		t.Error("EQ change should be detected")
	}
	d := a
	d.Persisted = !a.Persisted
	if !audioDSPChanged(a, d) {
		t.Error("persisted flip should be detected")
	}
}

func TestAudioDspEnvelope_Shape(t *testing.T) {
	t.Parallel()
	s := AudioStripData{Enabled: true, Bass: 4, EQ: make([]float64, 10), Engaged: true, Persisted: true}
	env := audioDspEnvelopeFrom(s)
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{`"params"`, `"bass":4`, `"engaged":true`, `"persisted":true`, `"eq":[`} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope JSON missing %q: %s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestAudioDSPChanged|TestAudioDspEnvelope' -v`
Expected: FAIL — `audioDSPChanged`/`audioDspEnvelopeFrom` undefined.

- [ ] **Step 3: Implement envelope + change-detection + emits**

In `internal/chassis/events.go`, add (near `volumeEnvelope`, line ~94):

```go
// audioDspEnvelope is the payload for the `audioDsp` SSE event. One shape for
// both preview and commit; `persisted` tells clients whether the runtime is
// ahead of disk.
type audioDspEnvelope struct {
	Params  audioDspParams `json:"params"`
	Engaged bool           `json:"engaged"`
	Persisted bool         `json:"persisted"`
}

type audioDspParams struct {
	Enabled  bool      `json:"enabled"`
	Mono     bool      `json:"mono"`
	Subsonic bool      `json:"subsonic"`
	Loudness bool      `json:"loudness"`
	Bass     float64   `json:"bass"`
	Mid      float64   `json:"mid"`
	Treble   float64   `json:"treble"`
	Balance  int       `json:"balance"`
	EQ       []float64 `json:"eq"`
}

func audioDspEnvelopeFrom(s AudioStripData) audioDspEnvelope {
	eq := s.EQ
	if eq == nil {
		eq = make([]float64, 10)
	}
	return audioDspEnvelope{
		Params: audioDspParams{
			Enabled: s.Enabled, Mono: s.Mono, Subsonic: s.Subsonic, Loudness: s.Loudness,
			Bass: s.Bass, Mid: s.Mid, Treble: s.Treble, Balance: s.Balance, EQ: eq,
		},
		Engaged:   s.Engaged,
		Persisted: s.Persisted,
	}
}

// audioDSPChanged isolates the audio-strip diff so the `audioDsp` event only
// emits when a tone/EQ/toggle/engaged/persisted value actually changed.
func audioDSPChanged(a, b AudioStripData) bool {
	if a.Enabled != b.Enabled || a.Mono != b.Mono || a.Subsonic != b.Subsonic ||
		a.Loudness != b.Loudness || a.Bass != b.Bass || a.Mid != b.Mid ||
		a.Treble != b.Treble || a.Balance != b.Balance ||
		a.Engaged != b.Engaged || a.Persisted != b.Persisted {
		return true
	}
	if len(a.EQ) != len(b.EQ) {
		return true
	}
	for i := range a.EQ {
		if a.EQ[i] != b.EQ[i] {
			return true
		}
	}
	return false
}
```

Ensure `events.go`/`events_test.go` import `encoding/json` + `strings` as the
tests need (events_test likely already imports them).

In `handleEvents`, add the initial emit after the `volume` emit (events.go:321):

```go
	if err := emit(w, "audioDsp", audioDspEnvelopeFrom(last.AudioStrip)); err != nil {
		return
	}
```

In the diff tick loop (after the `volumeChanged` arm — find it near the
`transportChanged`/`volumeChanged` arms around events.go:400-440), add:

```go
				if audioDSPChanged(curr.AudioStrip, last.AudioStrip) {
					if err := emit(w, "audioDsp", audioDspEnvelopeFrom(curr.AudioStrip)); err != nil {
						return
					}
					last.AudioStrip = curr.AudioStrip
				}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestAudioDSPChanged|TestAudioDspEnvelope' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "feat(chassis): audioDsp SSE event + change detection"
```

---

## Task 3: Route interfaces + `POST /receiver/audio/dsp`

**Files:**
- Create: `internal/chassis/audiodsp_routes.go`
- Create: `internal/chassis/audiodsp_routes_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/chassis/audiodsp_routes_test.go`:

```go
package chassis

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

type fakeDSPController struct {
	cur       config.AudioDSP
	preview   *config.AudioDSP
	previewN  int
}

func (f *fakeDSPController) AudioDSP() config.AudioDSP { return f.cur }
func (f *fakeDSPController) PreviewAudioDSP(d config.AudioDSP) error {
	f.preview = &d
	f.previewN++
	return nil
}

type fakeDSPSaver struct {
	saved    *config.AudioDSP
	mem      map[int]config.AudioDSPMemory
}

func (f *fakeDSPSaver) SaveAudioDSP(d config.AudioDSP) error { f.saved = &d; return nil }
func (f *fakeDSPSaver) SaveAudioDSPMemory(slot int, name string, v config.AudioDSP) error {
	if f.mem == nil {
		f.mem = map[int]config.AudioDSPMemory{}
	}
	f.mem[slot] = config.AudioDSPMemory{Slot: slot, Name: name, Stored: true, Bass: v.Bass, EQ: append([]float64(nil), v.EQ...)}
	return nil
}
func (f *fakeDSPSaver) RecallAudioDSPMemory(slot int) (config.AudioDSPMemory, bool) {
	m, ok := f.mem[slot]
	return m, ok
}

func newDSPTestServer(t *testing.T, ctl *fakeDSPController, saver *fakeDSPSaver) *http.ServeMux {
	t.Helper()
	srv, err := New(Config{Version: "t", AudioDSPController: ctl, AudioDSPSaver: saver})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return mux
}

func TestHandleAudioDSP_PreviewMergesPatch(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	saver := &fakeDSPSaver{}
	mux := newDSPTestServer(t, ctl, saver)
	body := `{"commit":false,"params":{"bass":4}}`
	req := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if ctl.previewN != 1 || ctl.preview == nil || ctl.preview.Bass != 4 {
		t.Errorf("preview not applied with merged bass: %+v (n=%d)", ctl.preview, ctl.previewN)
	}
	if saver.saved != nil {
		t.Error("preview must not persist")
	}
}

func TestHandleAudioDSP_CommitPersists(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	saver := &fakeDSPSaver{}
	mux := newDSPTestServer(t, ctl, saver)
	body := `{"commit":true,"params":{"treble":3}}`
	req := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if saver.saved == nil || saver.saved.Treble != 3 {
		t.Errorf("commit did not persist merged treble: %+v", saver.saved)
	}
}

func TestHandleAudioDSP_RejectsOutOfRange(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	mux := newDSPTestServer(t, ctl, &fakeDSPSaver{})
	body := `{"commit":true,"params":{"bass":99}}`
	req := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for out-of-range bass", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run TestHandleAudioDSP -v`
Expected: FAIL — `Config.AudioDSPController`/`AudioDSPSaver`, handlers, route undefined.

- [ ] **Step 3: Implement interfaces + handler + patch merge**

Create `internal/chassis/audiodsp_routes.go`:

```go
package chassis

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// AudioDSPController is the live tone/EQ runtime: read current params + apply
// an unpersisted preview. *core.Manager satisfies it (AudioDSP/PreviewAudioDSP).
type AudioDSPController interface {
	AudioDSP() config.AudioDSP
	PreviewAudioDSP(config.AudioDSP) error
}

// AudioDSPSaver persists + applies committed params and manages EQ memories.
// A thin main.go adapter over *uiserver.BridgeSaver satisfies it.
type AudioDSPSaver interface {
	SaveAudioDSP(config.AudioDSP) error
	SaveAudioDSPMemory(slot int, name string, voicing config.AudioDSP) error
	RecallAudioDSPMemory(slot int) (config.AudioDSPMemory, bool)
}

// audioDSPRequest is the POST /receiver/audio/dsp body. Params is a partial
// patch merged onto the current runtime params; pointer fields distinguish
// "set to zero" from "omitted".
type audioDSPRequest struct {
	Commit bool `json:"commit"`
	Params struct {
		Enabled  *bool     `json:"enabled"`
		Mono     *bool     `json:"mono"`
		Subsonic *bool     `json:"subsonic"`
		Loudness *bool     `json:"loudness"`
		Bass     *float64  `json:"bass"`
		Mid      *float64  `json:"mid"`
		Treble   *float64  `json:"treble"`
		Balance  *int      `json:"balance"`
		EQ       []float64 `json:"eq"`
	} `json:"params"`
}

func (s *Server) handleAudioDSPPost(w http.ResponseWriter, r *http.Request) {
	if s.audioDSPController == nil || s.audioDSPSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "audio dsp not configured")
		return
	}
	var req audioDSPRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed json body")
		return
	}
	merged := mergeAudioDSPPatch(s.audioDSPController.AudioDSP(), req)
	if err := config.ValidateAudioDSP(merged); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Commit {
		if err := s.audioDSPSaver.SaveAudioDSP(merged); err != nil {
			audioDSPWriteError(w, err)
			return
		}
	} else {
		if err := s.audioDSPController.PreviewAudioDSP(merged); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "preview failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// mergeAudioDSPPatch overlays the request's non-nil fields onto cur. EQ, when
// present, replaces wholesale; absent EQ keeps cur's (length is validated
// downstream).
func mergeAudioDSPPatch(cur config.AudioDSP, req audioDSPRequest) config.AudioDSP {
	p := req.Params
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.Mono != nil {
		cur.Mono = *p.Mono
	}
	if p.Subsonic != nil {
		cur.Subsonic = *p.Subsonic
	}
	if p.Loudness != nil {
		cur.Loudness = *p.Loudness
	}
	if p.Bass != nil {
		cur.Bass = *p.Bass
	}
	if p.Mid != nil {
		cur.Mid = *p.Mid
	}
	if p.Treble != nil {
		cur.Treble = *p.Treble
	}
	if p.Balance != nil {
		cur.Balance = *p.Balance
	}
	if p.EQ != nil {
		cur.EQ = append([]float64(nil), p.EQ...)
	}
	return cur
}

// audioDSPWriteError maps a saver error to a status. A validation error is a
// 400 (BAD INPUT, mirroring the bridge saver); anything else is a 500.
func audioDSPWriteError(w http.ResponseWriter, err error) {
	var se interface{ HTTPStatus() int }
	if errors.As(err, &se) {
		writeJSONError(w, se.HTTPStatus(), err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "save failed")
}
```

Note on `audioDSPWriteError`: if `*uiserver.settingsError` does not expose an
`HTTPStatus()` method, the `errors.As` simply won't match and the error maps to
500 — acceptable, because `ValidateAudioDSP` already ran in the handler, so a
saver-side validation failure is not expected on the commit path. (The
`writeJSONError` helper already exists in the package — see `volume.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis/ -run TestHandleAudioDSP -v`
Expected: PASS (after Task 5 mounts the route — if these fail with 404 before
Task 5, do Task 5 Step 3 first; the handler + route are a pair, so it is fine
to land them together if your runner needs the route to test the handler).

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/audiodsp_routes.go internal/chassis/audiodsp_routes_test.go
git commit -m "feat(chassis): audio dsp preview/commit handler + patch merge"
```

---

## Task 4: `POST /receiver/audio/dsp/memory` (store/recall)

**Files:**
- Modify: `internal/chassis/audiodsp_routes.go`
- Modify: `internal/chassis/audiodsp_routes_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/audiodsp_routes_test.go`:

```go
func TestHandleAudioDSPMemory_Store(t *testing.T) {
	t.Parallel()
	cur := config.DefaultAudioDSP()
	cur.EQ[5] = 6
	ctl := &fakeDSPController{cur: cur}
	saver := &fakeDSPSaver{}
	mux := newDSPTestServer(t, ctl, saver)
	req := httptest.NewRequest("POST", "/receiver/audio/dsp/memory", strings.NewReader(`{"op":"store","slot":2,"name":"Rock"}`))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if m, ok := saver.mem[2]; !ok || m.EQ[5] != 6 || m.Name != "Rock" {
		t.Errorf("memory slot 2 not stored from current params: %+v", saver.mem[2])
	}
}

func TestHandleAudioDSPMemory_RecallCommits(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	saver := &fakeDSPSaver{mem: map[int]config.AudioDSPMemory{
		1: {Slot: 1, Stored: true, Treble: 5, EQ: make([]float64, 10)},
	}}
	mux := newDSPTestServer(t, ctl, saver)
	req := httptest.NewRequest("POST", "/receiver/audio/dsp/memory", strings.NewReader(`{"op":"recall","slot":1}`))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if saver.saved == nil || saver.saved.Treble != 5 {
		t.Errorf("recall did not commit the stored voicing: %+v", saver.saved)
	}
}

func TestHandleAudioDSPMemory_RecallEmptyIs404(t *testing.T) {
	t.Parallel()
	mux := newDSPTestServer(t, &fakeDSPController{cur: config.DefaultAudioDSP()}, &fakeDSPSaver{})
	req := httptest.NewRequest("POST", "/receiver/audio/dsp/memory", strings.NewReader(`{"op":"recall","slot":3}`))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for empty slot recall", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestHandleAudioDSPMemory -v`
Expected: FAIL — handler/route undefined.

- [ ] **Step 3: Implement the memory handler**

Append to `internal/chassis/audiodsp_routes.go`:

```go
type audioDSPMemoryRequest struct {
	Op   string `json:"op"`   // "store" | "recall"
	Slot int    `json:"slot"` // 1..3
	Name string `json:"name"` // store only
}

func (s *Server) handleAudioDSPMemoryPost(w http.ResponseWriter, r *http.Request) {
	if s.audioDSPController == nil || s.audioDSPSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "audio dsp not configured")
		return
	}
	var req audioDSPMemoryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed json body")
		return
	}
	if req.Slot < 1 || req.Slot > 3 {
		writeJSONError(w, http.StatusBadRequest, "slot must be 1..3")
		return
	}
	cur := s.audioDSPController.AudioDSP()
	switch req.Op {
	case "store":
		if err := s.audioDSPSaver.SaveAudioDSPMemory(req.Slot, req.Name, cur); err != nil {
			audioDSPWriteError(w, err)
			return
		}
	case "recall":
		mem, ok := s.audioDSPSaver.RecallAudioDSPMemory(req.Slot)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "memory slot empty")
			return
		}
		merged := cur
		merged.Bass, merged.Mid, merged.Treble = mem.Bass, mem.Mid, mem.Treble
		merged.EQ = append([]float64(nil), mem.EQ...)
		if err := config.ValidateAudioDSP(merged); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.audioDSPSaver.SaveAudioDSP(merged); err != nil {
			audioDSPWriteError(w, err)
			return
		}
	default:
		writeJSONError(w, http.StatusBadRequest, "op must be store or recall")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestHandleAudioDSPMemory -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/audiodsp_routes.go internal/chassis/audiodsp_routes_test.go
git commit -m "feat(chassis): audio dsp memory store/recall handler"
```

---

## Task 5: Config/Server fields + route mounting

**Files:**
- Modify: `internal/chassis/server.go`

- [ ] **Step 1: Add the Config + Server fields**

In `internal/chassis/server.go`, add to `Config` (next to `VolumeSaver`, ~line 52):

```go
	// AudioDSPController is the live tone/EQ runtime (preview + read).
	AudioDSPController AudioDSPController
	// AudioDSPSaver persists committed params + manages EQ memories.
	AudioDSPSaver AudioDSPSaver
```

Add to the `Server` struct (next to `volumeSaver`, ~line 138):

```go
	audioDSPController AudioDSPController
	audioDSPSaver      AudioDSPSaver
```

Assign in `New` (next to `volumeSaver: cfg.VolumeSaver`, ~line 198):

```go
		audioDSPController:   cfg.AudioDSPController,
		audioDSPSaver:        cfg.AudioDSPSaver,
```

- [ ] **Step 2: Mount the routes**

In `Mount` (next to the `/receiver/volume` line, server.go:269):

```go
	mux.Handle("POST /receiver/audio/dsp", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleAudioDSPPost))))
	mux.Handle("POST /receiver/audio/dsp/memory", requireSameOrigin(http.HandlerFunc(s.handleAudioDSPMemoryPost)))
```

(`transportNoStore` on the preview route keeps the rapid drag posts
uncacheable, matching `/receiver/volume`.)

- [ ] **Step 3: Run handler + memory tests (now routed)**

Run: `go test ./internal/chassis/ -run 'TestHandleAudioDSP' -v`
Expected: PASS (handlers from Tasks 3-4 are now reachable through the mux).

- [ ] **Step 4: Full chassis sweep**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/server.go
git commit -m "feat(chassis): wire audio dsp routes + config fields"
```

---

## Task 6: Template — add the strip, relocate volume

**Files:**
- Create: `internal/chassis/templates/audio-strip.html`
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/templates/transport.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing render test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestRender_AudioStripPresent(t *testing.T) {
	t.Parallel()
	html := renderIndexForTest(t) // existing helper that renders the full page
	for _, want := range []string{
		`data-audio-strip`,
		`data-dsp-knob="bass"`,
		`data-dsp-eq="0"`,
		`data-dsp-eq="9"`,
		`data-dsp-switch="loudness"`,
		`data-dsp-preset="flat"`,
		`data-dsp-memory="1"`,
		`data-volume-knob`, // volume relocated into the strip
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

func TestRender_VolumeNotInTransport(t *testing.T) {
	t.Parallel()
	// The transport template must no longer carry the volume control.
	tmpl := transportTemplateSourceForTest(t) // or assert via the rendered transport block
	if strings.Contains(tmpl, "data-volume-knob") {
		t.Error("transport template still contains the volume knob; it should move to the strip")
	}
}
```

If `renderIndexForTest`/`transportTemplateSourceForTest` aren't the exact
helpers, use the existing chassis render-test helper (search `chassis_test.go`
for how it renders `shell.html`) and assert against the produced HTML string.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestRender_AudioStrip|TestRender_VolumeNotInTransport' -v`
Expected: FAIL — strip markup absent; volume still in transport.

- [ ] **Step 3: Create the template + mount + relocate**

Create `internal/chassis/templates/audio-strip.html`:

```html
{{define "audio-strip"}}
{{htmlComment "chassis:audio-strip"}}
<div class="audio-strip" data-audio-strip data-dsp-persisted="{{.Persisted}}" aria-label="Audio tone and equalizer">
  <span class="strip-label">Equalizer</span>
  <div class="audio-deck">

    <div class="deck-sect">
      <span class="deck-cap">Tone · Balance</span>
      <div class="knob-row">
        {{ template "dsp-knob" (dict "Key" "bass" "Label" "Bass" "Value" .Bass "Min" -12 "Max" 12) }}
        {{ template "dsp-knob" (dict "Key" "mid" "Label" "Mid" "Value" .Mid "Min" -12 "Max" 12) }}
        {{ template "dsp-knob" (dict "Key" "treble" "Label" "Treble" "Value" .Treble "Min" -12 "Max" 12) }}
        {{ template "dsp-knob" (dict "Key" "balance" "Label" "Bal" "Value" .Balance "Min" -100 "Max" 100) }}
      </div>
    </div>

    <div class="deck-div"></div>

    <div class="deck-sect deck-eq">
      <span class="deck-cap">Graphic Equalizer · dB</span>
      <div class="eq-bank">
        {{ range $i, $g := .EQ }}
        <div class="eq-band">
          <input class="eq-range" type="range" min="-12" max="12" step="1" value="{{ $g }}"
                 data-dsp-eq="{{ $i }}" aria-label="EQ band {{ $i }}">
          <span class="eq-hz">{{ index $.EQLabels $i }}</span>
        </div>
        {{ end }}
      </div>
    </div>

    <div class="deck-div"></div>

    <div class="deck-sect">
      <span class="deck-cap">Preset / Memory</span>
      <div class="btn-row">
        {{ range $p := .Presets }}<button class="action-btn eqbtn" type="button" data-dsp-preset="{{ $p }}">{{ $p }}</button>{{ end }}
      </div>
      <div class="btn-row">
        {{ range $m := .Memory }}<button class="action-btn eqbtn{{ if $m.Stored }} stored{{ end }}" type="button" data-dsp-memory="{{ $m.Slot }}" title="Tap recall · hold store">{{ if $m.Name }}{{ $m.Name }}{{ else }}M{{ $m.Slot }}{{ end }}</button>{{ end }}
      </div>
      <div class="sw-grid">
        {{ template "dsp-switch" (dict "Key" "loudness" "Label" "Loud" "On" .Loudness) }}
        {{ template "dsp-switch" (dict "Key" "mono" "Label" "Mono" "On" .Mono) }}
        {{ template "dsp-switch" (dict "Key" "subsonic" "Label" "Subsonic" "On" .Subsonic) }}
        {{ template "dsp-switch" (dict "Key" "defeat" "Label" "EQ Out" "On" (not .Enabled)) }}
      </div>
    </div>

    <div class="deck-div"></div>

    <div class="deck-sect">
      <span class="deck-cap">Level</span>
      {{ template "volume-control" . }}
    </div>

  </div>
</div>
{{end}}

{{define "dsp-knob"}}
<div class="dsp-knob volume-control" data-dsp-knob="{{.Key}}" data-dsp-value="{{.Value}}"
     style="--volume-angle: {{ dspKnobAngle .Value .Min .Max }}deg">
  <label class="volume-label">{{.Label}}</label>
  <div class="volume-tick-ring" aria-hidden="true">{{ range $i, $_ := until 21 }}<span class="volume-tick tick-{{$i}}"></span>{{ end }}</div>
  <div class="volume-dial" aria-hidden="true"><span class="volume-notch"></span></div>
  <input class="volume-range dsp-knob-range" type="range" min="{{.Min}}" max="{{.Max}}" step="1" value="{{.Value}}"
         data-dsp-knob-range="{{.Key}}" aria-label="{{.Label}}">
</div>
{{end}}

{{define "dsp-switch"}}
<div class="sw-cell">
  <button class="switch{{ if .On }} on{{ end }}" type="button" role="switch"
          aria-checked="{{ if .On }}true{{ else }}false{{ end }}" data-dsp-switch="{{.Key}}"></button>
  <span class="sw-lbl">{{.Label}}</span>
</div>
{{end}}

{{define "volume-control"}}
<div class="volume-control" data-volume-knob data-volume-value="{{.OutputVolume}}" style="--volume-angle: {{volumeAngle .OutputVolume}}deg">
  <label class="volume-label" for="receiver-volume-range">Volume</label>
  <div class="volume-tick-ring" aria-hidden="true">{{range $i, $_ := until 21}}<span class="volume-tick tick-{{$i}}"></span>{{end}}</div>
  <div class="volume-dial" aria-hidden="true"><span class="volume-notch"></span></div>
  <input id="receiver-volume-range" class="volume-range" data-volume-range type="range" name="output_volume" min="0" max="100" step="1" value="{{.OutputVolume}}" aria-label="Volume">
</div>
{{end}}
```

Notes:
- The `volume-control` block is **extracted verbatim** from `transport.html`
  into its own `{{define "volume-control"}}` and rendered inside the strip with
  the same markup, so `volume-knob.js` (querying `[data-volume-knob]` /
  `[data-volume-range]`) works unchanged in the new location.
- The template needs an `EQLabels` field and `Presets` field for the ranges.
  Add them to `AudioStripData` rendering by giving the template access — set
  package-level template funcs `dspKnobAngle` and (reuse) `until`/`dict`/`volumeAngle`,
  and add `EQLabels []string` + `Presets []string` to `AudioStripData` (populated
  in `audioStripFromDSP`: `EQLabels: []string{"31","63","125","250","500","1K","2K","4K","8K","16K"}`,
  `Presets: []string{"Flat","Rock","Jazz","Vocal"}`).

Register the `dspKnobAngle` template func where the other funcs (`volumeAngle`,
`until`, `dict`) are registered (search `internal/chassis/templates.go` for
`"volumeAngle"`):

```go
"dspKnobAngle": func(value, min, max int) int {
	if max == min {
		return -135
	}
	frac := float64(value-min) / float64(max-min)
	return int(-135 + frac*270)
},
```

(Use `float64` value/min/max if the knob values are floats; tone values are
dB floats, balance is int — pass them through a numeric template func that
accepts `any` and coerces, or split into `dspKnobAngleF`/`dspKnobAngleI`. The
simplest is one func taking `float64` and templating with `printf`-free calls;
adapt to the existing func style in `templates.go`.)

In `internal/chassis/templates/shell.html`, mount the strip between transport
and visualizer (line 35-36):

```html
      {{template "transport" .Transport}}
      {{template "audio-strip" .AudioStrip}}
      {{template "visualizer-bank" .Visualizer}}
```

In `internal/chassis/templates/transport.html`, **delete** the
`<div class="volume-control" ...>...</div>` block (transport.html:41-50). Leave
the transport grid; CSS Task 8 adjusts the now-empty grid column.

- [ ] **Step 4: Run render tests**

Run: `go test ./internal/chassis/ -run 'TestRender_AudioStrip|TestRender_VolumeNotInTransport' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/audio-strip.html internal/chassis/templates/shell.html internal/chassis/templates/transport.html internal/chassis/templates.go internal/chassis/data.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): audio-strip template + relocate volume knob"
```

---

## Task 7: `audio-strip.js` — control interactions

**Files:**
- Create: `internal/chassis/static/audio-strip.js`
- Modify: `internal/chassis/templates/shell.html` (register the script)
- Create: `internal/chassis/testdata/audio-strip.behavior.test.js`

- [ ] **Step 1: Write the failing behavior test**

Create `internal/chassis/testdata/audio-strip.behavior.test.js` following the
repo's existing behavior-test harness (see
`internal/chassis/testdata/volume-knob.behavior.test.js` for the DOM-stub
pattern this project uses). The test must assert:
- Dragging an EQ slider posts `{commit:false, params:{eq:[...]}}` (preview).
- Releasing an EQ slider posts `{commit:true, ...}` (commit).
- Toggling the Loud switch posts `{commit:true, params:{loudness:true}}`.
- Toggling EQ Out posts `{commit:true, params:{enabled:false}}` (inverted).
- Clicking a preset sets the 10 sliders to the preset curve and commits.
- Tapping a memory posts `{op:"recall", slot}`; a long-press posts `{op:"store", slot}`.
- An incoming `audioDsp` SSE event updates the controls when not editing.

(Mirror the structure/assert style of `volume-knob.behavior.test.js`; that
file shows how this repo stubs the DOM + `fetch` + `Chassis.events`.)

- [ ] **Step 2: Run test to verify it fails**

Run the repo's JS behavior-test command (see how `volume-knob.behavior.test.js`
is invoked — e.g. `node internal/chassis/testdata/audio-strip.behavior.test.js`
or the project's JS test runner). Expected: FAIL (script absent).

- [ ] **Step 3: Implement the script**

Create `internal/chassis/static/audio-strip.js`:

```js
// Receiver audio/EQ strip: tone/balance knobs, 10-band EQ, switches,
// presets, and EQ memories. Posts previews while dragging and a commit on
// release to /receiver/audio/dsp; memories to /receiver/audio/dsp/memory.
// Syncs from the shared `audioDsp` SSE event (vfd-live.js owns EventSource).
(() => {
  'use strict';
  if (!window.Chassis) {
    console.warn('audio-strip: window.Chassis missing');
    return;
  }

  const PREVIEW_MS = 120;
  const HOLD_MS = 500;
  const PRESETS = {
    flat:  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
    rock:  [4, 3, 1, 0, -1, -1, 0, 2, 3, 4],
    jazz:  [2, 1, 0, 1, 1, 0, 0, 1, 2, 2],
    vocal: [-2, -1, 0, 2, 3, 3, 2, 1, 0, -1],
  };

  let editing = false;
  let previewTimer = 0;

  function post(body) {
    return fetch('/receiver/audio/dsp', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }
  function postMemory(body) {
    return fetch('/receiver/audio/dsp/memory', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }
  function preview(params) {
    if (previewTimer) return;
    previewTimer = window.setTimeout(() => {
      previewTimer = 0;
      post({ commit: false, params }).catch((e) => console.warn('audio-strip preview', e));
    }, PREVIEW_MS);
  }
  function commit(params) {
    if (previewTimer) { window.clearTimeout(previewTimer); previewTimer = 0; }
    post({ commit: true, params }).catch((e) => console.warn('audio-strip commit', e));
  }

  function currentEQ() {
    return Array.from(document.querySelectorAll('[data-dsp-eq]'))
      .sort((a, b) => Number(a.dataset.dspEq) - Number(b.dataset.dspEq))
      .map((el) => Number(el.value));
  }

  function bindEQ() {
    document.querySelectorAll('[data-dsp-eq]').forEach((el) => {
      el.addEventListener('pointerdown', () => { editing = true; });
      el.addEventListener('input', () => { editing = true; preview({ eq: currentEQ() }); });
      el.addEventListener('change', () => { editing = false; commit({ eq: currentEQ() }); });
    });
  }

  function bindKnobs() {
    document.querySelectorAll('[data-dsp-knob-range]').forEach((el) => {
      const key = el.dataset.dspKnobRange;
      const field = key === 'balance' ? 'balance' : key; // bass/mid/treble/balance
      const read = () => (key === 'balance' ? parseInt(el.value, 10) : parseFloat(el.value));
      el.addEventListener('pointerdown', () => { editing = true; });
      el.addEventListener('input', () => { editing = true; preview({ [field]: read() }); });
      el.addEventListener('change', () => { editing = false; commit({ [field]: read() }); });
    });
  }

  function bindSwitches() {
    document.querySelectorAll('[data-dsp-switch]').forEach((el) => {
      el.addEventListener('click', () => {
        const on = !el.classList.contains('on');
        el.classList.toggle('on', on);
        el.setAttribute('aria-checked', on ? 'true' : 'false');
        const key = el.dataset.dspSwitch;
        if (key === 'defeat') {
          commit({ enabled: !on }); // EQ Out engaged = DSP disabled
        } else {
          commit({ [key]: on });
        }
      });
    });
  }

  function bindPresets() {
    document.querySelectorAll('[data-dsp-preset]').forEach((el) => {
      el.addEventListener('click', () => {
        const curve = PRESETS[el.dataset.dspPreset.toLowerCase()];
        if (!curve) return;
        document.querySelectorAll('[data-dsp-eq]').forEach((slider) => {
          slider.value = String(curve[Number(slider.dataset.dspEq)] || 0);
        });
        commit({ eq: curve.slice() });
      });
    });
  }

  function bindMemories() {
    document.querySelectorAll('[data-dsp-memory]').forEach((el) => {
      const slot = Number(el.dataset.dspMemory);
      let holdTimer = 0;
      let held = false;
      const startHold = () => {
        held = false;
        holdTimer = window.setTimeout(() => {
          held = true;
          postMemory({ op: 'store', slot, name: el.textContent.trim() })
            .then(() => { el.classList.add('stored'); flash(el, 'STORED'); })
            .catch((e) => console.warn('audio-strip store', e));
        }, HOLD_MS);
      };
      const endHold = () => {
        if (holdTimer) { window.clearTimeout(holdTimer); holdTimer = 0; }
        if (!held) postMemory({ op: 'recall', slot }).catch((e) => console.warn('audio-strip recall', e));
      };
      el.addEventListener('pointerdown', startHold);
      el.addEventListener('pointerup', endHold);
      el.addEventListener('pointerleave', () => { if (holdTimer) { window.clearTimeout(holdTimer); holdTimer = 0; } });
    });
  }

  function flash(el, text) {
    const prev = el.textContent;
    el.textContent = text;
    window.setTimeout(() => { el.textContent = prev; }, 600);
  }

  function applyFromEvent(params, persisted) {
    if (editing) return; // don't fight the operator mid-drag
    const setRange = (sel, v) => {
      const el = document.querySelector(sel);
      if (el && el.value !== String(v)) el.value = String(v);
    };
    setRange('[data-dsp-knob-range="bass"]', params.bass);
    setRange('[data-dsp-knob-range="mid"]', params.mid);
    setRange('[data-dsp-knob-range="treble"]', params.treble);
    setRange('[data-dsp-knob-range="balance"]', params.balance);
    (params.eq || []).forEach((g, i) => setRange(`[data-dsp-eq="${i}"]`, g));
    const sw = (key, on) => {
      const el = document.querySelector(`[data-dsp-switch="${key}"]`);
      if (!el) return;
      el.classList.toggle('on', on);
      el.setAttribute('aria-checked', on ? 'true' : 'false');
    };
    sw('loudness', params.loudness);
    sw('mono', params.mono);
    sw('subsonic', params.subsonic);
    sw('defeat', !params.enabled);
    const root = document.querySelector('[data-audio-strip]');
    if (root) root.dataset.dspPersisted = String(persisted);
  }

  function handleEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      applyFromEvent(data.params || {}, Boolean(data.persisted));
    } catch (err) {
      console.warn('audio-strip: bad audioDsp payload', ev.data, err);
    }
  }

  function init() {
    bindEQ();
    bindKnobs();
    bindSwitches();
    bindPresets();
    bindMemories();
    window.Chassis.events.subscribe('audioDsp', handleEvent);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
```

Register the script in `internal/chassis/templates/shell.html`, next to the
other `<script defer ...>` lines (after `volume-knob.js`, shell.html:14):

```html
  <script defer src="/receiver/static/audio-strip.js?v={{.Version}}"></script>
```

(The static file is served by the existing `GET /receiver/static/` handler via
the embedded FS — no Go change needed beyond the file existing under
`internal/chassis/static/`.)

- [ ] **Step 4: Run behavior test**

Run the JS behavior-test command. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/audio-strip.js internal/chassis/templates/shell.html internal/chassis/testdata/audio-strip.behavior.test.js
git commit -m "feat(chassis): audio-strip.js (knobs/eq/switches/presets/memories + SSE sync)"
```

---

## Task 8: CSS (first pass; finalize against the live app)

**Files:**
- Modify: `internal/chassis/static/chassis.css`

Per the spec, exact styling is finalized against the running chassis. This task
adds a working first pass reusing existing tokens/components so the strip is
legible and on-brand; polish (spacing, exact dial geometry) happens at build.

- [ ] **Step 1: Add the strip styles**

Append to `internal/chassis/static/chassis.css` (reusing `.volume-control`,
`.action-btn`, `.switch`, `.screen`/faceplate tokens already defined):

```css
/* ---- Audio / EQ strip --------------------------------------------- */
body.receiver .audio-strip {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
  gap: 12px;
  align-items: center;
}
body.receiver .audio-deck {
  display: flex;
  align-items: stretch;
  gap: 16px;
  overflow-x: auto;
  padding: 10px 12px;
  border-radius: 3px;
  background: linear-gradient(180deg, #26262a 0%, #1c1c20 100%);
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.06), inset 0 2px 8px rgba(0,0,0,0.5);
}
body.receiver .audio-deck .deck-sect { display: flex; flex-direction: column; gap: 8px; justify-content: center; }
body.receiver .audio-deck .deck-cap {
  color: #6a6a70; font: 700 7px 'Inter', sans-serif; letter-spacing: 0.2em;
  text-transform: uppercase; text-align: center;
}
body.receiver .audio-deck .deck-div { width: 1px; background: #0a0a0b; box-shadow: 1px 0 0 rgba(255,255,255,0.04); }
body.receiver .audio-deck .knob-row { display: flex; gap: 10px; }
body.receiver .audio-deck .dsp-knob { width: 64px; height: 64px; }
/* EQ vertical sliders — metal thumb matches .volume-dial */
body.receiver .eq-bank { display: flex; gap: 9px; align-items: flex-end; }
body.receiver .eq-band { display: flex; flex-direction: column; align-items: center; gap: 5px; }
body.receiver .eq-range {
  writing-mode: vertical-lr; direction: rtl; width: 6px; height: 88px;
  accent-color: var(--vfd);
}
body.receiver .eq-band .eq-hz { color: #6a6a70; font: 600 7px 'Inter', sans-serif; }
body.receiver .audio-deck .btn-row { display: flex; gap: 5px; }
body.receiver .action-btn.eqbtn { padding: 5px 8px; font-size: 10px; letter-spacing: 0.06em; text-transform: uppercase; }
body.receiver .action-btn.eqbtn.stored { color: var(--vfd); border-color: oklch(0.30 0.06 175); }
body.receiver .sw-grid { display: grid; grid-template-columns: auto auto; gap: 7px 12px; align-items: center; }
body.receiver .sw-cell { display: flex; align-items: center; gap: 7px; }
body.receiver .sw-cell .sw-lbl { color: #8a8a8e; font: 700 8px 'Inter', sans-serif; letter-spacing: 0.14em; text-transform: uppercase; }
@media (prefers-reduced-motion: reduce) {
  body.receiver .dsp-knob .volume-dial { transition: none; }
}
```

If the transport grid (`.transport-strip`, chassis.css:1724) now has an empty
trailing column after the volume removal, adjust its `grid-template-columns`
(drop one `auto`) so the remaining controls don't gap. Verify visually at build.

- [ ] **Step 2: CSS scope test**

Run: `go test ./internal/chassis/ -run 'CSS|Scope' -v`
Expected: PASS — the existing `css_scope_test.go` still passes (all new rules
are under `body.receiver`).

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "style(chassis): first-pass audio-strip CSS (polish at build)"
```

---

## Task 9: Wire into `main.go`

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Add the saver adapter**

In `cmd/mister-groovy-relay/main.go`, next to `volumeSaverAdapter` (line 62-68),
add an adapter that drops the `ApplyScope` return so the chassis interface
(error-only) is satisfied:

```go
type audioDSPSaverAdapter struct{ bs *uiserver.BridgeSaver }

func (a *audioDSPSaverAdapter) SaveAudioDSP(dsp config.AudioDSP) error {
	_, err := a.bs.SaveAudioDSP(dsp)
	return err
}
func (a *audioDSPSaverAdapter) SaveAudioDSPMemory(slot int, name string, voicing config.AudioDSP) error {
	_, err := a.bs.SaveAudioDSPMemory(slot, name, voicing)
	return err
}
func (a *audioDSPSaverAdapter) RecallAudioDSPMemory(slot int) (config.AudioDSPMemory, bool) {
	return a.bs.RecallAudioDSPMemory(slot)
}
```

(Confirm `config` and `uiserver` are already imported in main.go — they are,
since `volumeSaverAdapter` uses `bs *uiserver.BridgeSaver`.)

- [ ] **Step 2: Pass into the chassis Config**

In the `chassis.New(chassis.Config{...})` literal (main.go:393), next to
`VolumeViewer`/`VolumeSaver` (lines 405-406):

```go
		AudioDSPController:        coreMgr,
		AudioDSPSaver:             &audioDSPSaverAdapter{bs: saver},
```

(`coreMgr` is the `*core.Manager`; it satisfies `AudioDSPController` via
`AudioDSP()` + `PreviewAudioDSP()` from Plan 2.)

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(cmd): wire AudioDSPController + AudioDSPSaver into the chassis"
```

---

## Task 10: Migrate volume tests + integration coverage

**Files:**
- Modify: existing chassis tests that asserted volume-in-transport
- Create: `cmd/mister-groovy-relay/audio_dsp_e2e_test.go`

- [ ] **Step 1: Sweep for volume-relocation fallout**

Run the spec's enumerator:

```bash
rg 'volume-control|data-volume|data-transport.*volume' internal/chassis tests/integration
```

Any test asserting `data-volume-knob`/`data-volume-range` appears **inside the
transport block** must move to assert it appears in the page (now in the
strip). Update those assertions to match the relocated markup (the control is
identical; only its container changed). Re-run:

```bash
go test ./internal/chassis/...
```
Expected: PASS after migration.

- [ ] **Step 2: Write the integration test**

Create `cmd/mister-groovy-relay/audio_dsp_e2e_test.go`:

```go
//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAudioDSP_E2E_SSEAndCommit wires a real core.Manager + uiserver.BridgeSaver
// + chassis.Server over a temp config and verifies: (1) the SSE stream includes
// an `audioDsp` event, and (2) a commit POST persists to disk + is reflected in
// the manager's AudioDSP(). Mirrors the existing chassis integration harness
// (see tests that build chassis.New with a real Manager + BridgeSaver).
func TestAudioDSP_E2E_SSEAndCommit(t *testing.T) {
	mux, mgr, _ := newReceiverStackForTest(t) // build the same way sibling e2e tests do

	// (1) SSE includes audioDsp on initial burst.
	sreq := httptest.NewRequest("GET", "/receiver/events", nil)
	srec := httptest.NewRecorder()
	// Use a context with immediate cancel after first flush, per the sibling
	// SSE test pattern, then assert the body contains the event name.
	serveSSEBriefly(t, mux, sreq, srec)
	if !strings.Contains(srec.Body.String(), "event: audioDsp") {
		t.Errorf("SSE stream missing audioDsp event:\n%s", srec.Body.String())
	}

	// (2) Commit a tone change and confirm it reached the manager.
	body := `{"commit":true,"params":{"bass":6}}`
	creq := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	creq.Header.Set("Origin", "http://"+creq.Host)
	crec := httptest.NewRecorder()
	mux.ServeHTTP(crec, creq)
	if crec.Code != http.StatusNoContent {
		t.Fatalf("commit status = %d body=%s", crec.Code, crec.Body)
	}
	if mgr.AudioDSP().Bass != 6 {
		t.Errorf("manager AudioDSP().Bass = %v, want 6", mgr.AudioDSP().Bass)
	}
}
```

Use the existing integration harness helpers (`newReceiverStackForTest` /
`serveSSEBriefly` are placeholders for whatever the sibling tests in
`cmd/mister-groovy-relay/*_test.go` and `tests/integration/chassis_test.go`
already provide — reuse them; the SSE brief-serve pattern already exists in the
chassis SSE integration test).

- [ ] **Step 2b: Add a transparency assertion (no-regression)**

In `internal/dataplane` (or the integration harness), assert that with the
default transparent DSP the audio bytes are unchanged vs the volume-only path —
this is the Plan 1 transparency test extended to the wired config default. If
Plan 1's `TestProcessor_TransparentWithinOneLSB` + `TestSendAudioAppliesOutputVolume`
already cover this, reference them here and skip duplication.

- [ ] **Step 3: Run integration**

Run: `make test-integration` (or `go test -tags=integration ./...`)
Expected: PASS (ffmpeg/ffprobe on PATH required, per the project's integration
gate).

- [ ] **Step 4: Commit**

```bash
git add internal/chassis cmd/mister-groovy-relay/audio_dsp_e2e_test.go tests/integration
git commit -m "test(chassis): migrate volume tests + audio dsp e2e (SSE + commit)"
```

---

## Task 11: Full feature verification gate

**Files:** none (verification only).

- [ ] **Step 1: All four CI gates**

```bash
make lint
make test
go test -race ./...            # CI runs this; locally only if cgo/gcc available
make test-integration
```
Expected: all green.

- [ ] **Step 2: Manual smoke (fake-mister, no hardware)**

Per `CLAUDE.md`: run `fake-mister`, point `bridge.mister.host = 127.0.0.1`,
start the bridge, open the chassis UI, and confirm:
- The audio strip renders between transport and visualizer; the volume knob is
  in the strip (gone from transport).
- Dragging Bass/an EQ slider changes audio live (preview); releasing persists
  (reload keeps the value).
- Loudness / Mono / Subsonic / EQ Out toggle live; the status-bar **EQ LED**
  lights when shaping is engaged and goes dark on EQ Out / flat.
- A preset snaps the sliders; M1 hold stores, tap recalls.
- A second browser tab tracks changes via the `audioDsp` event.

- [ ] **Step 3: Confirm no double-spectrum / no transport regressions**

The meter's 32-band analyzer still renders; transport actions + seek + the
relocated volume all work.

---

## Self-review (run before handing off)

- **Spec coverage (this slice):** strip data + snapshot (Task 1); `audioDsp`
  SSE (Task 2); preview/commit + memory routes/handlers (Tasks 3-5); template +
  volume relocation (Task 6); JS interactions + SSE sync (Task 7); CSS first
  pass (Task 8); main wiring (Task 9); volume-test migration + integration
  (Task 10); verification incl. manual smoke + EQ LED + reduced-motion (Tasks
  8/11). All §Chassis UI / §Error handling / §Testing items map to a task.
- **Cross-plan type consistency:** `config.AudioDSP`/`AudioDSPMemory`,
  `core.Manager.AudioDSP()`/`PreviewAudioDSP()`, `BridgeSaver.SaveAudioDSP`/
  `SaveAudioDSPMemory`/`RecallAudioDSPMemory`, the `audioDsp` event name, and
  the `/receiver/audio/dsp(/memory)` routes match Plans 1-2.
- **Deferred (explicit):** exact CSS/pixel polish is finalized against the live
  app (Task 8 + manual smoke), per the spec's stated decision — not a gap.
- **Helper-name caveat:** several test steps reference existing harness helpers
  (`testConfig`, `renderIndexForTest`, `newReceiverStackForTest`,
  `serveSSEBriefly`, the JS behavior-test runner). Each step says to reuse the
  sibling test's actual helper; substitute the real name on encounter (these
  are findable by the cited sibling tests, not new inventions).
