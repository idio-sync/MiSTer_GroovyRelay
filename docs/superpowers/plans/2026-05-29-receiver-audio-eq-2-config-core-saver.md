# Receiver Audio Tone/EQ Strip — Plan 2: Config + Core Preview/Commit + Saver

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist, validate, and live-apply the tone/EQ params — add `config.AudioDSP`, wire it through the core `Manager` (a committed `SetAudioDSP` + an unpersisted `PreviewAudioDSP`, plus `StatusHomeView` exposure and the plane-config build), and add `BridgeSaver.SaveAudioDSP` (+ EQ-memory save/recall) following the existing `output_volume` hot-swap pipeline.

**Architecture:** `config.AudioDSP` nests on `config.AudioConfig` and is seeded transparent by `defaultBridge()` so a missing `[bridge.audio.dsp]` table is a no-op. The `Manager` keeps the persisted config (`m.bridge.Audio.DSP`) as truth plus a runtime snapshot (`m.audioDSPRuntime` + `m.audioDSPPersisted`) so a live drag can run ahead of disk; new casts always build from persisted. The `BridgeSaver` mirrors `SaveOutputVolume` → `saveLocked` → `applyHotSwapSideEffects` → `core.SetAudioDSP`, with `audio.dsp` scoped `ScopeHotSwap`.

**Tech Stack:** Go 1.26 stdlib, BurntSushi/toml, the Plan 1 `internal/dataplane/audiodsp` package.

**Spec reference:** [`docs/superpowers/specs/2026-05-29-receiver-audio-tone-eq-strip-design.md`](../specs/2026-05-29-receiver-audio-tone-eq-strip-design.md) — §Config & persistence, §Core plumbing.

**Plan set:** **Plan 2 of 3.** Depends on Plan 1 (the `audiodsp` package + `Plane.SetAudioDSP`). Plan 3 adds the chassis UI + routes + volume relocation. After Plan 2, params persist/validate and apply live to an active plane, but there is no UI yet (drive via tests).

**Prereqs:** Plan 1 merged. `Plane.SetAudioDSP(audiodsp.Params) error` and `audiodsp.Params` exist.

---

## File Structure

**Modified:**
- `internal/config/config.go` — `AudioDSP` + `AudioDSPMemory` types; `DSP` field on `AudioConfig`; `DefaultAudioDSP()`; `(AudioDSP).Engaged()`; `ValidateAudioDSP`; DSP rules in `Sectioned.Validate`.
- `internal/config/migration.go` — seed `DSP` in `defaultBridge()`; normalize EQ lengths in `normalizeSectionedRuntimeDefaults`.
- `internal/core/manager.go` — `dspParamsFromConfig` helper; `PlaneConfig.AudioDSP` in the plane build; `planeRunner.SetAudioDSP`; `Manager` runtime fields + constructor init; `AudioDSP()` / `SetAudioDSP` / `PreviewAudioDSP`; `StatusHomeView()` population.
- `internal/core/types.go` — `StatusHomeView` DSP fields.
- `internal/core/manager_test.go` — add `SetAudioDSP` to the planeRunner fakes.
- `internal/uiserver/bridge_saver.go` — `bridgeCore.SetAudioDSP`; `audio.dsp` in `diffBridgeConfig` + `scopeForBridgeField` + `applyHotSwapSideEffects`; `SaveAudioDSP` + memory methods.
- `internal/uiserver/bridge_saver_test.go` — add `SetAudioDSP` to `fakeBridgeCore`.

**Created:**
- `internal/config/audiodsp_config_test.go` — `AudioDSP` defaults/validate/normalize tests.

---

## Task 1: `config.AudioDSP` type, defaults, and `Engaged()`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/migration.go`
- Create: `internal/config/audiodsp_config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/audiodsp_config_test.go`:

```go
package config

import "testing"

func TestDefaultAudioDSP_TransparentButEnabled(t *testing.T) {
	t.Parallel()
	d := DefaultAudioDSP()
	if !d.Enabled {
		t.Error("default DSP should be Enabled (defeat off)")
	}
	if len(d.EQ) != 10 {
		t.Fatalf("default EQ length = %d, want 10", len(d.EQ))
	}
	if d.Engaged() {
		t.Error("flat default DSP should not be Engaged")
	}
}

func TestDefaultBridge_SeedsTransparentDSP(t *testing.T) {
	t.Parallel()
	b := defaultBridge()
	if !b.Audio.DSP.Enabled || len(b.Audio.DSP.EQ) != 10 {
		t.Errorf("defaultBridge DSP = %+v, want enabled + 10-band EQ", b.Audio.DSP)
	}
}

func TestAudioDSP_Engaged(t *testing.T) {
	t.Parallel()
	base := DefaultAudioDSP()
	if base.Engaged() {
		t.Error("flat not engaged")
	}
	b := base
	b.Bass = 3
	if !b.Engaged() {
		t.Error("bass != 0 should engage")
	}
	d := base
	d.Enabled = false
	d.Bass = 3
	if d.Engaged() {
		t.Error("defeated DSP must not engage even with shaping set")
	}
	e := base
	e.EQ[4] = -2
	if !e.Engaged() {
		t.Error("eq band != 0 should engage")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'AudioDSP|DefaultBridge_Seeds' -v`
Expected: FAIL — `DefaultAudioDSP`, `AudioDSP.Engaged`, `AudioConfig.DSP` undefined.

- [ ] **Step 3: Add the types + helpers**

In `internal/config/config.go`, add the `DSP` field to `AudioConfig`:

```go
type AudioConfig struct {
	SampleRate   int      `toml:"sample_rate"`
	Channels     int      `toml:"channels"`
	OutputVolume int      `toml:"output_volume"`
	DSP          AudioDSP `toml:"dsp"`
}
```

Then add (near `AudioConfig`):

```go
// AudioDSP is the live tone/EQ chain configuration (spec §Config). dB
// fields are clamped to ±12 and Balance to ±100 by ValidateAudioDSP; EQ is
// the 10 ISO-octave band gains (31 Hz..16 kHz). A missing [bridge.audio.dsp]
// table inherits DefaultAudioDSP() via defaultBridge() seeding.
type AudioDSP struct {
	Enabled  bool             `toml:"enabled"`
	Mono     bool             `toml:"mono"`
	Subsonic bool             `toml:"subsonic"`
	Loudness bool             `toml:"loudness"`
	Bass     float64          `toml:"bass"`
	Mid      float64          `toml:"mid"`
	Treble   float64          `toml:"treble"`
	Balance  int              `toml:"balance"`
	EQ       []float64        `toml:"eq"`
	Memory   []AudioDSPMemory `toml:"memory"`
}

// AudioDSPMemory is one saved EQ voicing (M1..M3). Stored distinguishes a
// saved-flat memory from an empty slot.
type AudioDSPMemory struct {
	Slot   int       `toml:"slot"`
	Name   string    `toml:"name"`
	Stored bool      `toml:"stored"`
	Bass   float64   `toml:"bass"`
	Mid    float64   `toml:"mid"`
	Treble float64   `toml:"treble"`
	EQ     []float64 `toml:"eq"`
}

// DefaultAudioDSP returns the transparent default: enabled (defeat off),
// nothing engaged, a 10-band flat EQ.
func DefaultAudioDSP() AudioDSP {
	return AudioDSP{Enabled: true, EQ: make([]float64, 10)}
}

// Engaged reports whether shaping is active (drives the status-bar EQ LED).
// Independent of mono/balance; false under defeat (Enabled == false).
func (a AudioDSP) Engaged() bool {
	if !a.Enabled {
		return false
	}
	if a.Subsonic || a.Loudness || a.Bass != 0 || a.Mid != 0 || a.Treble != 0 {
		return true
	}
	for _, g := range a.EQ {
		if g != 0 {
			return true
		}
	}
	return false
}
```

In `internal/config/migration.go`, seed the default in `defaultBridge()` —
change the `Audio:` literal (currently lines ~275-279):

```go
		Audio: AudioConfig{
			SampleRate:   d.AudioSampleRate,
			Channels:     d.AudioChannels,
			OutputVolume: d.AudioOutputVolume,
			DSP:          DefaultAudioDSP(),
		},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'AudioDSP|DefaultBridge_Seeds' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/migration.go internal/config/audiodsp_config_test.go
git commit -m "feat(config): add AudioDSP type, transparent default, Engaged()"
```

---

## Task 2: Validation + load-time normalization

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/migration.go`
- Modify: `internal/config/audiodsp_config_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/audiodsp_config_test.go`:

```go
func validSectionedWithDSP(d AudioDSP) *Sectioned {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.Audio.DSP = d
	return s
}

func TestValidateAudioDSP_Bounds(t *testing.T) {
	t.Parallel()
	ok := DefaultAudioDSP()
	if err := ValidateAudioDSP(ok); err != nil {
		t.Fatalf("flat default should validate: %v", err)
	}
	bad := DefaultAudioDSP()
	bad.Bass = 13
	if err := ValidateAudioDSP(bad); err == nil {
		t.Error("bass +13 dB should be rejected")
	}
	balBad := DefaultAudioDSP()
	balBad.Balance = 101
	if err := ValidateAudioDSP(balBad); err == nil {
		t.Error("balance 101 should be rejected")
	}
	eqBad := DefaultAudioDSP()
	eqBad.EQ = []float64{0, 0, 0} // wrong length
	if err := ValidateAudioDSP(eqBad); err == nil {
		t.Error("EQ length != 10 should be rejected")
	}
	eqRange := DefaultAudioDSP()
	eqRange.EQ[2] = -13
	if err := ValidateAudioDSP(eqRange); err == nil {
		t.Error("EQ band -13 dB should be rejected")
	}
}

func TestValidateAudioDSP_Memory(t *testing.T) {
	t.Parallel()
	d := DefaultAudioDSP()
	d.Memory = []AudioDSPMemory{
		{Slot: 1, Name: "M1", Stored: true, EQ: make([]float64, 10)},
		{Slot: 1, Name: "dup", Stored: true, EQ: make([]float64, 10)},
	}
	if err := ValidateAudioDSP(d); err == nil {
		t.Error("duplicate memory slot should be rejected")
	}
	d2 := DefaultAudioDSP()
	d2.Memory = []AudioDSPMemory{{Slot: 9, Stored: true, EQ: make([]float64, 10)}}
	if err := ValidateAudioDSP(d2); err == nil {
		t.Error("memory slot out of 1..3 should be rejected")
	}
}

func TestSectionedValidate_RejectsBadDSP(t *testing.T) {
	t.Parallel()
	s := validSectionedWithDSP(DefaultAudioDSP())
	s.Bridge.Audio.DSP.Treble = 99
	if err := s.Validate(); err == nil {
		t.Error("Sectioned.Validate should reject bad DSP")
	}
}

func TestNormalize_PadsEQToTen(t *testing.T) {
	t.Parallel()
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.Audio.DSP.EQ = nil // simulate omitted eq
	s.Bridge.Audio.DSP.Memory = []AudioDSPMemory{{Slot: 1, Stored: true, EQ: nil}}
	if err := normalizeSectionedRuntimeDefaults(s); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(s.Bridge.Audio.DSP.EQ) != 10 {
		t.Errorf("top-level EQ not padded: len=%d", len(s.Bridge.Audio.DSP.EQ))
	}
	if len(s.Bridge.Audio.DSP.Memory[0].EQ) != 10 {
		t.Errorf("memory EQ not padded: len=%d", len(s.Bridge.Audio.DSP.Memory[0].EQ))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'ValidateAudioDSP|SectionedValidate_RejectsBadDSP|Normalize_PadsEQ' -v`
Expected: FAIL — `ValidateAudioDSP` undefined; normalize does not pad.

- [ ] **Step 3: Implement validation + normalization**

In `internal/config/config.go`, add:

```go
const (
	audioDSPMaxDB   = 12.0
	audioDSPMaxBal  = 100
	audioDSPBands   = 10
	audioDSPMaxSlot = 3
)

// ValidateAudioDSP enforces the spec's bounds. EQ must already be length 10
// (normalizeSectionedRuntimeDefaults pads omitted arrays before Validate).
func ValidateAudioDSP(a AudioDSP) error {
	if err := dspDBInRange("bass", a.Bass); err != nil {
		return err
	}
	if err := dspDBInRange("mid", a.Mid); err != nil {
		return err
	}
	if err := dspDBInRange("treble", a.Treble); err != nil {
		return err
	}
	if a.Balance < -audioDSPMaxBal || a.Balance > audioDSPMaxBal {
		return fmt.Errorf("bridge.audio.dsp.balance must be in -100..100, got %d", a.Balance)
	}
	if len(a.EQ) != audioDSPBands {
		return fmt.Errorf("bridge.audio.dsp.eq must have %d bands, got %d", audioDSPBands, len(a.EQ))
	}
	for i, g := range a.EQ {
		if err := dspDBInRange(fmt.Sprintf("eq[%d]", i), g); err != nil {
			return err
		}
	}
	seen := map[int]bool{}
	for _, m := range a.Memory {
		if m.Slot < 1 || m.Slot > audioDSPMaxSlot {
			return fmt.Errorf("bridge.audio.dsp.memory slot must be 1..%d, got %d", audioDSPMaxSlot, m.Slot)
		}
		if seen[m.Slot] {
			return fmt.Errorf("bridge.audio.dsp.memory has duplicate slot %d", m.Slot)
		}
		seen[m.Slot] = true
		if err := dspDBInRange("memory.bass", m.Bass); err != nil {
			return err
		}
		if err := dspDBInRange("memory.mid", m.Mid); err != nil {
			return err
		}
		if err := dspDBInRange("memory.treble", m.Treble); err != nil {
			return err
		}
		if len(m.EQ) != audioDSPBands {
			return fmt.Errorf("bridge.audio.dsp.memory[%d].eq must have %d bands, got %d", m.Slot, audioDSPBands, len(m.EQ))
		}
		for i, g := range m.EQ {
			if err := dspDBInRange(fmt.Sprintf("memory[%d].eq[%d]", m.Slot, i), g); err != nil {
				return err
			}
		}
	}
	return nil
}

func dspDBInRange(name string, v float64) error {
	if v < -audioDSPMaxDB || v > audioDSPMaxDB {
		return fmt.Errorf("bridge.audio.dsp.%s must be in -12..12 dB, got %g", name, v)
	}
	return nil
}
```

Call it from `Sectioned.Validate` — add after the existing
`b.Audio.OutputVolume` check (config.go ~307):

```go
	if err := ValidateAudioDSP(b.Audio.DSP); err != nil {
		return err
	}
```

In `internal/config/migration.go`, extend `normalizeSectionedRuntimeDefaults`
(currently lines 251-258) to pad EQ arrays so omitted `eq` keys validate:

```go
func normalizeSectionedRuntimeDefaults(s *Sectioned) error {
	dataDir, err := ResolveDataDir(s.Bridge.DataDir)
	if err != nil {
		return err
	}
	s.Bridge.DataDir = dataDir
	normalizeAudioDSP(&s.Bridge.Audio.DSP)
	return nil
}

// normalizeAudioDSP pads omitted EQ arrays to the canonical 10 bands so a
// table that sets only some keys still validates. Pure shape fix-up; values
// are bounds-checked by ValidateAudioDSP afterward.
func normalizeAudioDSP(a *AudioDSP) {
	a.EQ = padEQ(a.EQ)
	for i := range a.Memory {
		a.Memory[i].EQ = padEQ(a.Memory[i].EQ)
	}
}

func padEQ(eq []float64) []float64 {
	if len(eq) == audioDSPBands {
		return eq
	}
	out := make([]float64, audioDSPBands)
	copy(out, eq) // truncates if longer, zero-pads if shorter
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS — new DSP tests plus all existing config tests (output-volume
bounds, default-bridge, migration round-trips).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/migration.go internal/config/audiodsp_config_test.go
git commit -m "feat(config): validate + normalize AudioDSP (bounds, EQ length, memories)"
```

---

## Task 3: Core mapping + plane-config wiring + `planeRunner.SetAudioDSP`

**Files:**
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/core/manager_test.go`:

```go
func TestDSPParamsFromConfig_Maps(t *testing.T) {
	t.Parallel()
	c := config.DefaultAudioDSP()
	c.Bass = 4
	c.EQ[3] = -2
	c.Balance = -10
	p := dspParamsFromConfig(c, 48000, 2)
	if !p.Enabled || p.Bass != 4 || p.EQ[3] != -2 || p.Balance != -10 ||
		p.SampleRate != 48000 || p.Channels != 2 {
		t.Errorf("mapped params = %+v", p)
	}
}
```

The planeRunner fakes also need the new method or the package won't compile
once Step 3 extends the interface — add a `SetAudioDSP` to each fake in
`internal/core/manager_test.go` (search for the existing `SetOutputVolume(int) error`
fake methods and add a sibling on each fake type — `fakePlane`,
`contextDonePlane`, `blockingDonePlane`, `volumePlane`, `errorPlane`):

```go
func (f *fakePlane) SetAudioDSP(audiodsp.Params) error { return nil }
```

(Repeat for each fake; `volumePlane` may record the call if a later test needs it.)
Add the import `"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/audiodsp"`
to `manager_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestDSPParamsFromConfig -v`
Expected: FAIL — `dspParamsFromConfig` undefined (and/or interface mismatch).

- [ ] **Step 3: Implement mapping + wiring**

In `internal/core/manager.go`:

1. Add the import `"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/audiodsp"`.
2. Add the mapping helper:

```go
// dspParamsFromConfig maps the persisted/runtime config.AudioDSP into the
// dataplane's plain audiodsp.Params (no config dependency leaks into the
// dataplane). EQ is copied into the fixed-size array; a normalized config
// always carries exactly 10 bands.
func dspParamsFromConfig(a config.AudioDSP, sampleRate, channels int) audiodsp.Params {
	p := audiodsp.Params{
		Enabled:    a.Enabled,
		Mono:       a.Mono,
		Subsonic:   a.Subsonic,
		Loudness:   a.Loudness,
		Bass:       a.Bass,
		Mid:        a.Mid,
		Treble:     a.Treble,
		Balance:    a.Balance,
		SampleRate: sampleRate,
		Channels:   channels,
	}
	for i := 0; i < len(p.EQ) && i < len(a.EQ); i++ {
		p.EQ[i] = a.EQ[i]
	}
	return p
}
```

3. Add `AudioDSP` to the `planeRunner` interface (manager.go:101):

```go
	SetOutputVolume(int) error
	SetAudioDSP(audiodsp.Params) error
```

4. Add `AudioDSP` to the `PlaneConfig` literal in the plane build (manager.go:833,
   right after `OutputVolume: m.bridge.Audio.OutputVolume,`):

```go
		AudioDSP:            dspParamsFromConfig(m.bridge.Audio.DSP, audioRate, audioChans),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/core/ -run TestDSPParamsFromConfig -v`
Expected: PASS, and the package compiles (all fakes satisfy `planeRunner`).

- [ ] **Step 5: Commit**

```bash
git add internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(core): map AudioDSP to plane config + extend planeRunner"
```

---

## Task 4: `Manager` runtime state + `SetAudioDSP` / `PreviewAudioDSP` / `AudioDSP`

**Files:**
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/core/manager_test.go`:

```go
func TestManager_SetAudioDSP_DualWrite(t *testing.T) {
	t.Parallel()
	m := newTestManager(t) // existing helper used by sibling volume tests
	fp := &volumePlane{}
	m.plane = fp
	dsp := config.DefaultAudioDSP()
	dsp.Bass = 6
	if err := m.SetAudioDSP(dsp); err != nil {
		t.Fatalf("SetAudioDSP: %v", err)
	}
	if m.bridge.Audio.DSP.Bass != 6 {
		t.Errorf("persisted bridge DSP not updated: %+v", m.bridge.Audio.DSP)
	}
	got := m.AudioDSP()
	if got.Bass != 6 {
		t.Errorf("AudioDSP() = %+v, want Bass 6", got)
	}
	if !m.audioDSPPersisted {
		t.Error("committed SetAudioDSP should mark persisted=true")
	}
}

func TestManager_PreviewAudioDSP_RuntimeOnly(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)
	m.plane = &volumePlane{}
	persisted := m.bridge.Audio.DSP
	preview := config.DefaultAudioDSP()
	preview.Treble = 5
	if err := m.PreviewAudioDSP(preview); err != nil {
		t.Fatalf("PreviewAudioDSP: %v", err)
	}
	if m.bridge.Audio.DSP.Treble != persisted.Treble {
		t.Error("preview must not touch persisted bridge config")
	}
	if m.AudioDSP().Treble != 5 {
		t.Error("preview should be visible via AudioDSP()")
	}
	if m.audioDSPPersisted {
		t.Error("preview should mark persisted=false")
	}
}

func TestManager_SetAudioDSP_RejectsBad(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)
	bad := config.DefaultAudioDSP()
	bad.Bass = 50
	if err := m.SetAudioDSP(bad); err == nil {
		t.Fatal("SetAudioDSP should reject out-of-range bass")
	}
}
```

If `newTestManager` does not exist, use the same construction the existing
volume tests use (search `manager_test.go` for how `m` is built for
`TestManager_SetOutputVolumeUpdatesActivePlane` and reuse it verbatim).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core/ -run 'TestManager_(SetAudioDSP|PreviewAudioDSP)' -v`
Expected: FAIL — methods/fields undefined.

- [ ] **Step 3: Add runtime fields + methods**

In `internal/core/manager.go`, add to the `Manager` struct (after
`nextGeneration uint64`, ~line 96):

```go
	// audioDSPRuntime is the currently auditioned tone/EQ params (committed
	// or preview). audioDSPPersisted is true when runtime == m.bridge.Audio.DSP.
	audioDSPRuntime   config.AudioDSP
	audioDSPPersisted bool
```

Initialize them in the Manager constructor. Find it:

```bash
grep -n "func New.*Manager" internal/core/manager.go
```

Where the constructor assigns the initial `bridge` field, add right after:

```go
	m.audioDSPRuntime = bridge.Audio.DSP
	m.audioDSPPersisted = true
```

(Use the actual field/parameter name the constructor uses for the bridge config.)

Add the methods next to `SetOutputVolume` (manager.go ~1221):

```go
// AudioDSP returns the current runtime tone/EQ params under m.mu. On process
// start and new casts this equals the persisted bridge config; during an
// active preview it may run ahead of disk.
func (m *Manager) AudioDSP() config.AudioDSP {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.audioDSPRuntime
}

// AudioDSPPersisted reports whether the runtime params equal the persisted
// bridge snapshot (false while a preview is auditioning).
func (m *Manager) AudioDSPPersisted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.audioDSPPersisted
}

// SetAudioDSP is the committed path: validate, update persisted + runtime
// state, and apply live to the active plane. Mirrors SetOutputVolume's
// dual-write. m.mu guards only the in-memory fields and the atomic coeff
// swap (no I/O under the lock); disk persistence happens in the uiserver
// saver before this is called via applyHotSwapSideEffects.
func (m *Manager) SetAudioDSP(dsp config.AudioDSP) error {
	if err := config.ValidateAudioDSP(dsp); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bridge.Audio.DSP = dsp
	m.audioDSPRuntime = dsp
	m.audioDSPPersisted = true
	if m.plane != nil {
		return m.plane.SetAudioDSP(dspParamsFromConfig(dsp, m.bridge.Audio.SampleRate, m.bridge.Audio.Channels))
	}
	return nil
}

// PreviewAudioDSP is the unpersisted drag path: validate, update only the
// runtime snapshot (not m.bridge), and apply live. The next cast still starts
// from persisted config until a commit (SetAudioDSP) lands.
func (m *Manager) PreviewAudioDSP(dsp config.AudioDSP) error {
	if err := config.ValidateAudioDSP(dsp); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioDSPRuntime = dsp
	m.audioDSPPersisted = false
	if m.plane != nil {
		return m.plane.SetAudioDSP(dspParamsFromConfig(dsp, m.bridge.Audio.SampleRate, m.bridge.Audio.Channels))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/core/ -run 'TestManager_(SetAudioDSP|PreviewAudioDSP)' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(core): Manager SetAudioDSP/PreviewAudioDSP with runtime/persisted state"
```

---

## Task 5: `StatusHomeView` DSP exposure

**Files:**
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/core/manager_test.go`:

```go
func TestStatusHomeView_CarriesAudioDSP(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)
	dsp := config.DefaultAudioDSP()
	dsp.Loudness = true
	if err := m.SetAudioDSP(dsp); err != nil {
		t.Fatalf("SetAudioDSP: %v", err)
	}
	v := m.StatusHomeView()
	if !v.AudioDSP.Loudness {
		t.Error("StatusHomeView.AudioDSP should carry the params")
	}
	if !v.AudioDSPEngaged {
		t.Error("loudness on should set AudioDSPEngaged")
	}
	if !v.AudioDSPPersisted {
		t.Error("committed DSP should report persisted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestStatusHomeView_CarriesAudioDSP -v`
Expected: FAIL — `StatusHomeView.AudioDSP` undefined.

- [ ] **Step 3: Add fields + populate**

In `internal/core/types.go`, add to `StatusHomeView` (after `Meter MeterHomeView`, ~line 255):

```go
	AudioDSP          config.AudioDSP // live tone/EQ params (runtime snapshot)
	AudioDSPEngaged   bool            // shaping active → status-bar EQ LED
	AudioDSPPersisted bool            // false while a preview runs ahead of disk
```

Ensure `internal/core/types.go` imports `"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"` (it already references config types elsewhere; add if missing).

In `internal/core/manager.go`, in `StatusHomeView()` (after the `m.mu.Lock()`
at ~1491, unconditional — these reflect bridge/runtime state regardless of
active session), set:

```go
	view.AudioDSP = m.audioDSPRuntime
	view.AudioDSPEngaged = m.audioDSPRuntime.Engaged()
	view.AudioDSPPersisted = m.audioDSPPersisted
```

Place these right after `view := StatusHomeView{...}` so they populate even
when idle.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestStatusHomeView_CarriesAudioDSP -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/types.go internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(core): expose AudioDSP on StatusHomeView (params, engaged, persisted)"
```

---

## Task 6: Saver — interface, diff, scope, hot-swap dispatch

**Files:**
- Modify: `internal/uiserver/bridge_saver.go`
- Modify: `internal/uiserver/bridge_saver_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/uiserver/bridge_saver_test.go`:

```go
func TestDiffBridgeConfig_DetectsDSP(t *testing.T) {
	t.Parallel()
	old := defaultBridgeForTest() // existing helper used by sibling diff tests
	next := old
	next.Audio.DSP.Bass = 3
	if !containsStr(diffBridgeConfig(old, next), "audio.dsp") {
		t.Error("bass change should surface audio.dsp")
	}
	next2 := old
	next2.Audio.DSP.EQ = append([]float64(nil), old.Audio.DSP.EQ...)
	next2.Audio.DSP.EQ[4] = -2
	if !containsStr(diffBridgeConfig(old, next2), "audio.dsp") {
		t.Error("EQ band change should surface audio.dsp")
	}
}

func TestScopeForBridgeField_DSPIsHotSwap(t *testing.T) {
	t.Parallel()
	if got := scopeForBridgeField("audio.dsp"); got != adapters.ScopeHotSwap {
		t.Errorf("scopeForBridgeField(audio.dsp) = %v, want ScopeHotSwap", got)
	}
}
```

If a `defaultBridgeForTest` helper isn't present, build `old` the same way
the sibling tests in this file build a baseline `config.BridgeConfig`
(they start from a default and tweak fields).

The `fakeBridgeCore` (bridge_saver_test.go:606) needs the new method:

```go
func (f *fakeBridgeCore) SetAudioDSP(dsp config.AudioDSP) error {
	f.lastDSP = dsp
	f.dspCalls++
	return f.dspErr
}
```

Add the `lastDSP config.AudioDSP`, `dspCalls int`, and `dspErr error` fields
to the `fakeBridgeCore` struct.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/uiserver/ -run 'DiffBridgeConfig_DetectsDSP|ScopeForBridgeField_DSP' -v`
Expected: FAIL — no `audio.dsp` diff/scope; `fakeBridgeCore` lacks `SetAudioDSP`.

- [ ] **Step 3: Implement interface + diff + scope + dispatch**

In `internal/uiserver/bridge_saver.go`:

1. Add to the `bridgeCore` interface (lines 36-41):

```go
	SetOutputVolume(volume int) error
	SetAudioDSP(dsp config.AudioDSP) error
```

2. In `diffBridgeConfig`, after the `audio.output_volume` block (line ~461):

```go
	if !audioDSPEqual(oldCfg.Audio.DSP, newCfg.Audio.DSP) {
		keys = append(keys, "audio.dsp")
	}
```

Add the comparison helper (near `diffBridgeConfig`):

```go
// audioDSPEqual reports value equality across the full DSP config including
// the EQ slice and memory slots. reflect.DeepEqual is used because EQ and
// Memory are slices (== is illegal on them).
func audioDSPEqual(a, b config.AudioDSP) bool {
	return reflect.DeepEqual(a, b)
}
```

Add `"reflect"` to the file's imports.

3. In `scopeForBridgeField`, add a case (next to `audio.output_volume`, line ~528):

```go
	case "audio.dsp":
		return adapters.ScopeHotSwap
```

4. In `applyHotSwapSideEffects`, after the `audio.output_volume` block (line ~323):

```go
	if containsStr(changed, "audio.dsp") {
		if err := r.core.SetAudioDSP(newCfg.Audio.DSP); err != nil {
			return fmt.Errorf("audio dsp hot-swap: %w", err)
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/uiserver/ -run 'DiffBridgeConfig|ScopeForBridgeField|applyHotSwap|Save' -v`
Expected: PASS — new tests plus existing saver tests (the `fakeBridgeCore`
now satisfies the extended interface).

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/bridge_saver.go internal/uiserver/bridge_saver_test.go
git commit -m "feat(uiserver): diff/scope/hot-swap audio.dsp through the bridge saver"
```

---

## Task 7: `BridgeSaver.SaveAudioDSP` + memory save/recall

**Files:**
- Modify: `internal/uiserver/bridge_saver.go`
- Modify: `internal/uiserver/bridge_saver_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/uiserver/bridge_saver_test.go`:

```go
func TestSaveAudioDSP_PersistsAndHotSwaps(t *testing.T) {
	t.Parallel()
	r, core := newBridgeSaverForTest(t) // existing helper used by SaveOutputVolume test
	dsp := config.DefaultAudioDSP()
	dsp.Bass = 4
	scope, err := r.SaveAudioDSP(dsp)
	if err != nil {
		t.Fatalf("SaveAudioDSP: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
	if r.Current().Audio.DSP.Bass != 4 {
		t.Error("in-memory bridge not updated")
	}
	if core.lastDSP.Bass != 4 || core.dspCalls != 1 {
		t.Errorf("core.SetAudioDSP not dispatched once with new params: %+v calls=%d", core.lastDSP, core.dspCalls)
	}
}

func TestSaveAudioDSPMemory_StoreAndRecall(t *testing.T) {
	t.Parallel()
	r, _ := newBridgeSaverForTest(t)
	store := config.DefaultAudioDSP()
	store.EQ[5] = 6
	if _, err := r.SaveAudioDSPMemory(2, "Rock", store); err != nil {
		t.Fatalf("store: %v", err)
	}
	mem := r.Current().Audio.DSP.Memory
	if len(mem) != 1 || mem[0].Slot != 2 || !mem[0].Stored || mem[0].EQ[5] != 6 {
		t.Fatalf("memory slot 2 not stored: %+v", mem)
	}
	// Recall returns the stored voicing for the chassis to apply.
	got, ok := r.RecallAudioDSPMemory(2)
	if !ok || got.EQ[5] != 6 {
		t.Errorf("recall slot 2 = %+v ok=%v", got, ok)
	}
	if _, ok := r.RecallAudioDSPMemory(3); ok {
		t.Error("empty slot 3 should not recall")
	}
}
```

If `newBridgeSaverForTest` isn't present, mirror the construction in the
existing `TestBridgeSaver_SaveOutputVolumePreservesLatestBridgeFields` test
(it builds a `BridgeSaver` over a temp config + a `fakeBridgeCore`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/uiserver/ -run 'SaveAudioDSP' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement the saver methods**

In `internal/uiserver/bridge_saver.go`, add (next to `SaveOutputVolume`, ~line 152):

```go
// SaveAudioDSP atomically persists bridge.audio.dsp against the latest
// in-memory bridge snapshot, then applies it live via the saveLocked
// hot-swap pipeline (audio.dsp → ScopeHotSwap → core.SetAudioDSP).
func (r *BridgeSaver) SaveAudioDSP(dsp config.AudioDSP) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.sec.Bridge
	next.Audio.DSP = dsp
	return r.saveLocked(next)
}

// SaveAudioDSPMemory stores a voicing (tone + EQ) into slot 1..3, replacing
// any existing entry for that slot, and persists it. Returns the applied
// scope from saveLocked. Mono/balance/loudness/volume are not stored.
func (r *BridgeSaver) SaveAudioDSPMemory(slot int, name string, voicing config.AudioDSP) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.sec.Bridge
	mem := config.AudioDSPMemory{
		Slot:   slot,
		Name:   name,
		Stored: true,
		Bass:   voicing.Bass,
		Mid:    voicing.Mid,
		Treble: voicing.Treble,
		EQ:     append([]float64(nil), voicing.EQ...),
	}
	replaced := false
	for i := range next.Audio.DSP.Memory {
		if next.Audio.DSP.Memory[i].Slot == slot {
			next.Audio.DSP.Memory[i] = mem
			replaced = true
			break
		}
	}
	if !replaced {
		next.Audio.DSP.Memory = append(append([]config.AudioDSPMemory(nil), next.Audio.DSP.Memory...), mem)
	}
	return r.saveLocked(next)
}

// RecallAudioDSPMemory returns the stored voicing for a slot, or ok=false if
// the slot is empty. The chassis applies it via SaveAudioDSP (commit) — this
// method only reads.
func (r *BridgeSaver) RecallAudioDSPMemory(slot int) (config.AudioDSPMemory, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.sec.Bridge.Audio.DSP.Memory {
		if m.Slot == slot && m.Stored {
			return m, true
		}
	}
	return config.AudioDSPMemory{}, false
}
```

Note: storing a memory changes `audio.dsp` (the Memory slice), so `saveLocked`'s
diff surfaces `audio.dsp` and dispatches `core.SetAudioDSP`. That re-applies the
(unchanged) live params — harmless, and keeps the contract that any `audio.dsp`
write flows through one path. The store does NOT recall the voicing into the
live chain; recall is a separate explicit commit in Plan 3.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/uiserver/ -run 'SaveAudioDSP' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/bridge_saver.go internal/uiserver/bridge_saver_test.go
git commit -m "feat(uiserver): SaveAudioDSP + EQ memory store/recall"
```

---

## Task 8: Phase-2 verification gate

**Files:** none (verification only).

- [ ] **Step 1: vet + unit tests**

Run: `make lint && make test`
Expected: clean vet; all unit tests pass (config, core, uiserver, dataplane).

- [ ] **Step 2: race (CI parity)**

Run: `go test -race ./internal/config/... ./internal/core/... ./internal/uiserver/...`
Expected: PASS (or rely on CI if gcc is unavailable locally).

- [ ] **Step 3: end-to-end persistence smoke (manual reasoning check)**

Confirm the path is whole: a `SaveAudioDSP` writes `[bridge.audio.dsp]` to
disk (atomic), updates `BridgeSaver.Current()`, calls
`core.UpdateBridge` + `core.SetAudioDSP`, which dual-writes the manager's
persisted+runtime state and calls `plane.SetAudioDSP` on the live plane. A
`PreviewAudioDSP` applies to the live plane without touching disk; the next
cast's plane build reads persisted `m.bridge.Audio.DSP`.

```bash
grep -n "SetAudioDSP\|audio.dsp\|AudioDSP" internal/core/manager.go internal/uiserver/bridge_saver.go | head -40
```
Expected: the wiring above is present and consistent.

---

## Self-review (run before handing off)

- **Spec coverage (this slice):** `config.AudioDSP` + memories + transparent
  default + validation + normalization (Tasks 1-2); core mapping + plane build
  + `planeRunner.SetAudioDSP` (Task 3); preview/commit + runtime/persisted
  (Task 4); `StatusHomeView` params/engaged/persisted (Task 5); saver
  diff/scope/dispatch + `SaveAudioDSP` + memories (Tasks 6-7) — all present.
- **Deferred to Plan 3 (intentionally):** the chassis strip, routes
  (`/receiver/audio/dsp` + memory), SSE `audioDsp`, volume relocation,
  `main.go` wiring of `AudioDSPSaver`/`AudioDSPViewer`.
- **Type consistency:** `config.AudioDSP`/`AudioDSPMemory`,
  `dspParamsFromConfig`, `audiodsp.Params`, `SetAudioDSP`/`PreviewAudioDSP`/
  `AudioDSP()`/`AudioDSPPersisted()`, `SaveAudioDSP`/`SaveAudioDSPMemory`/
  `RecallAudioDSPMemory`, and the `audio.dsp` diff key are used identically
  across tasks and match Plan 1's `audiodsp.Params`.
- **Invariant check:** `m.mu` is held only across in-memory updates + the
  atomic `plane.SetAudioDSP` coeff swap (no I/O); disk writes happen in the
  uiserver saver before `core.SetAudioDSP` is invoked. Validation precedes any
  disk write (`Sectioned.Validate` in `saveLocked`; `ValidateAudioDSP` in the
  Manager setters).
