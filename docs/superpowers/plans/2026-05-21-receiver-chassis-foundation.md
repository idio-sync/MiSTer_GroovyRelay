# Receiver Chassis Foundation (Phase 0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a new `/receiver/*` route tree in parallel to the existing `/ui/*` UI, serving the receiver-chassis mockup's full idle-state visual with all CSS, fonts, and JS embedded in the single Go binary. Later specs (1-9) will wire live behaviour into this foundation.

**Architecture:** New `internal/chassis/` package with `templates/` and `static/` subdirectories embedded via `go:embed`. Single page template (`shell.html`) composes 12 partials. CSS port from the brainstorm mockup with a `body.receiver` scope-prefix pass. ~150-line vanilla JS runtime exposes a `window.Chassis` namespace for subsequent specs. No imports between `internal/chassis/`, `internal/ui/`, or `internal/uiserver/` — composition root is `cmd/mister-groovy-relay/main.go`.

**Tech Stack:** Go 1.26, `html/template` + `text/template` (stdlib), `embed.FS` (stdlib), `net/http.ServeMux` (Go 1.22 method-aware), `github.com/tdewolff/parse/v2/css` (new dep, for CSS scope test), vanilla ES2022 (no bundler), self-hosted DSEG + Inter woff2 fonts.

**Spec:** [docs/superpowers/specs/2026-05-21-receiver-chassis-foundation-design.md](../specs/2026-05-21-receiver-chassis-foundation-design.md).

**Mockup source:** `.superpowers/brainstorm/1973-1779237107/receiver-v24.html` (~6,100 lines). Plan tasks reference mockup line ranges and CSS selector anchors; the file is the source of truth for all visual decisions.

---

## File Structure

**New files (in `internal/chassis/`):**

| Path | Responsibility |
|---|---|
| `internal/chassis/doc.go` | Package doc, parallel-replacement strategy explanation |
| `internal/chassis/server.go` | `Config` + `Server` structs, `New(Config) (*Server, error)`, `Mount(*http.ServeMux)` |
| `internal/chassis/data.go` | `ReceiverPageData` + sub-structs, `idleSnapshot(Config, time.Time)` helper |
| `internal/chassis/handler.go` | `handleIndex`, `handleStatic`, MIME init |
| `internal/chassis/templates.go` | Template parser, helper FuncMap (`inc`, `hasString`, `replaceAll`, `pad2`, `dim`, `list`, `until`), `chassis.css` `{{.Version}}` substitution |
| `internal/chassis/chassis_test.go` | Layer 1 unit + handler tests |
| `internal/chassis/css_scope_test.go` | `TestChassisCSS_AllSelectorsScoped` (separate file for clarity) |
| `internal/chassis/import_check_test.go` | Cross-package import-isolation lint |
| `internal/chassis/templates/shell.html` | Top-level document template |
| `internal/chassis/templates/status-bar.html` | `{{define "status-bar"}}` partial |
| `internal/chassis/templates/masthead.html` | `{{define "masthead"}}` partial |
| `internal/chassis/templates/vfd-source-row.html` | Composes vfd + source-cluster |
| `internal/chassis/templates/vfd.html` | VFD partial (idle state) |
| `internal/chassis/templates/source-cluster.html` | 4-button hardware source bank |
| `internal/chassis/templates/meter.html` | 3-row meter screen |
| `internal/chassis/templates/transport.html` | Transport buttons + seek bar + Setup gear |
| `internal/chassis/templates/visualizer-bank.html` | 4-button visualizer selector |
| `internal/chassis/templates/input-row.html` | Paste + CAST + .TORRENT |
| `internal/chassis/templates/preset-bank.html` | 12-slot preset grid |
| `internal/chassis/templates/history.html` | Recent-casts row (empty in idle) |
| `internal/chassis/templates/settings-drawer.html` | Drawer (closed in Phase 0) |
| `internal/chassis/static/chassis.css` | Single stylesheet (~3,200 lines, ported from mockup) |
| `internal/chassis/static/chassis.js` | ~150-line vanilla runtime |
| `internal/chassis/static/fonts/DSEG7Classic-{Regular,Bold}.woff2` | DSEG7 classic |
| `internal/chassis/static/fonts/DSEG7Modern-{Regular,Bold}.woff2` | DSEG7 modern |
| `internal/chassis/static/fonts/DSEG14Classic-{Regular,Bold}.woff2` | DSEG14 classic |
| `internal/chassis/static/fonts/DSEG14Modern-{Regular,Bold}.woff2` | DSEG14 modern |
| `internal/chassis/static/fonts/Inter-Variable.woff2` | Latin-subset variable Inter |
| `internal/chassis/static/fonts/LICENSE` | DSEG + Inter attribution |
| `internal/chassis/static/fonts/SOURCES.md` | Upstream URLs + SHA-256 checksums |
| `tests/integration/chassis_test.go` | Layer 3 integration tests (build-tagged) |

**Files modified:**

| Path | Change |
|---|---|
| `cmd/mister-groovy-relay/main.go` | Add `chassis.New(...)` + `chassisSrv.Mount(mux)` after the existing `ui.Server` mount |
| `README.md` | One-line note about the preview `/receiver` route |
| `go.mod` / `go.sum` | Add `github.com/tdewolff/parse/v2` dependency |

**Files unchanged:** All existing `internal/ui/*`, `internal/uiserver/*`, `internal/ui/templates/*`, `internal/ui/static/*`. The chassis is parallel-replacement; existing `/ui/*` surface is untouched.

---

## Task 1: Package Skeleton + Config + New

**Files:**
- Create: `internal/chassis/doc.go`
- Create: `internal/chassis/server.go`
- Create: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test for `New` config validation**

Create `internal/chassis/chassis_test.go`:

```go
package chassis

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// nonZeroConfig returns a Config valid enough for New(). Tests that
// want to assert error paths shadow individual fields with zero values.
func nonZeroConfig() Config {
	return Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "test-1.0.0",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "10.0.0.5",
	}
}

func TestNew_ReturnsServerWithValidConfig(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("New returned nil Server with no error")
	}
}

func TestNew_RejectsZeroStartedAt(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.StartedAt = time.Time{}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for zero StartedAt, got nil")
	}
}

func TestNew_RejectsEmptyVersion(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Version = ""
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for empty Version, got nil")
	}
}

func TestNew_AllowsEmptyHostIP(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.HostIP = "" // offline host — should not fail startup
	_, err := New(cfg)
	if err != nil {
		t.Fatalf("New should accept empty HostIP, got: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/...`
Expected: FAIL with "no Go files" or "undefined: New / Config / Server".

- [ ] **Step 3: Create the package doc**

Create `internal/chassis/doc.go`:

```go
// Package chassis serves the receiver-chassis-styled UI under /receiver/.
//
// Phase 0 of a 9-spec rollout that replaces the existing /ui/* surface
// (served by internal/ui) with a chassis aesthetic. The chassis ships
// in parallel under /receiver/* until a final cutover spec swaps the
// routes; until then, /ui/* is unaffected.
//
// Design isolation: this package has zero imports of internal/ui or
// internal/uiserver, and those packages have zero imports of this one.
// The composition root is cmd/mister-groovy-relay/main.go, which wires
// both servers onto the same http.ServeMux. The disjoint /ui/ and
// /receiver/ prefixes guarantee no route collision.
//
// Phase 0 renders the chassis in idle state only — no live data, no
// playback control, no telemetry. Later specs replace idleSnapshot()
// with snapshotFromSession() and add interactive routes.
//
// See docs/superpowers/specs/2026-05-21-receiver-chassis-foundation-design.md
// for the full design.
package chassis
```

- [ ] **Step 4: Create `server.go` with `Config`, `Server`, and `New`**

Create `internal/chassis/server.go`:

```go
package chassis

import (
	"fmt"
	"net/http"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// Config is the dependencies bundle passed to New. Mirrors the
// internal/ui.Config pattern. Version and StartedAt are required even
// in Phase 0 (asset URLs use Version; idle VFD uptime uses StartedAt).
// HostIP may be empty on hosts where outboundIP() fails to resolve a
// route; idleSnapshot renders empty HostIP as the literal "OFFLINE"
// rather than failing server startup.
type Config struct {
	Bridge    config.BridgeConfig
	Manager   *core.Manager
	Registry  *adapters.Registry
	Version   string
	StartedAt time.Time
	HostIP    string
}

// Server owns the parsed templates + embedded static assets + the
// resolved Config. Constructed once at startup and mounted onto the
// shared HTTP mux.
type Server struct {
	cfg Config
	// tmpl and cssBytes are populated in subsequent tasks (template
	// parsing and CSS preprocessing). Leaving them as nil/empty here
	// keeps Task 1 self-contained.
}

// New builds a Server from cfg, validating required fields. Returns a
// non-nil error when Version is empty or StartedAt is zero.
func New(cfg Config) (*Server, error) {
	if cfg.Version == "" {
		return nil, fmt.Errorf("chassis: Config.Version is required")
	}
	if cfg.StartedAt.IsZero() {
		return nil, fmt.Errorf("chassis: Config.StartedAt is required (zero time disallowed)")
	}
	return &Server{cfg: cfg}, nil
}

// Mount registers the chassis routes on mux. Phase 0 implementation
// lands in Task 7; the empty body here lets the package compile while
// downstream tasks build the actual handlers.
func (s *Server) Mount(mux *http.ServeMux) {
	// Implemented in Task 7.
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/chassis/...`
Expected: PASS — `TestNew_*` (4 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/doc.go internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add package skeleton with Config + New validation

Phase 0 task 1 of the receiver chassis foundation rollout. New package
internal/chassis with empty Mount() to be filled in by later tasks.
Config validation rejects zero StartedAt and empty Version; HostIP is
optional so the route survives offline hosts where outboundIP() fails.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: MIME Type Registration

**Files:**
- Create: `internal/chassis/handler.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Add the failing test for the MIME registration**

Append to `internal/chassis/chassis_test.go`:

```go
import "mime"

func TestInit_RegistersWoff2MIME(t *testing.T) {
	t.Parallel()
	// Package init must register font/woff2 so http.FileServer's
	// content-type lookup succeeds on hosts where the system MIME
	// database doesn't know woff2 (Alpine, scratch containers).
	got := mime.TypeByExtension(".woff2")
	if got != "font/woff2" {
		t.Fatalf(`TypeByExtension(".woff2") = %q, want "font/woff2"`, got)
	}
}

func TestInit_RegistersWoffMIME(t *testing.T) {
	t.Parallel()
	got := mime.TypeByExtension(".woff")
	if got != "font/woff" {
		t.Fatalf(`TypeByExtension(".woff") = %q, want "font/woff"`, got)
	}
}
```

The existing `import "testing"` block needs `"mime"` added — make sure both imports are inside one import block in the test file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestInit_Registers`
Expected: FAIL — TypeByExtension returns empty string or `application/octet-stream`.

- [ ] **Step 3: Create `handler.go` with the init function**

Create `internal/chassis/handler.go`:

```go
package chassis

import "mime"

// Register woff2/woff content types at package init. http.FileServer
// falls back to mime.TypeByExtension when serving embedded assets, and
// minimal Linux containers (Alpine, scratch) plus some Windows hosts
// return "" for these extensions — yielding application/octet-stream
// and tripping strict-CSP deployments. Registering once at init keeps
// the static handler deterministic across host environments.
func init() {
	mime.AddExtensionType(".woff2", "font/woff2")
	mime.AddExtensionType(".woff", "font/woff")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestInit_Registers`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/handler.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): register woff2/woff MIME types at init

Prevents http.FileServer from falling back to application/octet-stream
on Alpine/scratch containers and Windows hosts that lack font/woff2 in
their system MIME database.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Data Structures

**Files:**
- Create: `internal/chassis/data.go`

Builds the `ReceiverPageData` shape and all sub-structs. No tests yet — `idleSnapshot()` in Task 4 will exercise them. Tests for the struct shape come implicitly via compile + Task 4's tests.

- [ ] **Step 1: Create `data.go` with the full struct hierarchy**

Create `internal/chassis/data.go`:

```go
package chassis

// ReceiverPageData is the page-level struct shell.html renders against.
// Each sub-struct holds the smallest set of fields its partial needs;
// live-state fields stay zero/empty in Phase 0. Subsequent specs
// populate them — the shape is forward-compatible by design.
type ReceiverPageData struct {
	Version    string
	BrandName  string
	HostInfo   HostInfo
	State      ReceiverState
	VFD        VFDData
	Source     SourceData
	Meter      MeterData
	Transport  TransportData
	Visualizer VisualizerData
	Input      InputData
	Presets    PresetsData
	History    HistoryData
	Settings   SettingsData
}

// ReceiverState is the body-class controller — "idle" or "live". Phase 0
// always renders "idle". Spec 2 (VFD live) introduces "live".
type ReceiverState string

const (
	StateIdle ReceiverState = "idle"
	StateLive ReceiverState = "live"
)

// HostInfo renders into the status bar. HostIP may be the literal
// string "OFFLINE" if cfg.HostIP was empty (offline host).
type HostInfo struct {
	HostIP   string
	HTTPPort int
}

// VFDData drives the VFD frame in the top row of the chassis. Idle
// state shows STANDBY + the marquee hint. SystemTime is server-
// rendered for initial paint and ticked client-side at minute boundary.
type VFDData struct {
	State        string // "idle" | "live"
	Title        string
	Marquee      string
	QueueCurrent int
	QueueTotal   int
	SystemTime   string
	Uptime       string
}

// SourceData is the 4-button hardware source selector cluster.
type SourceData struct {
	Buttons []SourceButton
}

// SourceButton represents one hardware-style button in the source
// cluster. Active = "this tab is currently selected for browsing";
// Lit = "currently casting from this source" (idle has none lit).
type SourceButton struct {
	Label  string
	Active bool
	Lit    bool
}

// MeterData is the 3-row signal meter screen. Each sub-row's struct
// holds idle placeholder strings; live values arrive in Spec 5.
type MeterData struct {
	State       string
	SourceStrip SourceStripIdleData
	MidRow      MidRowIdleData
	Readout     ReadoutIdleData
}

// SourceStripIdleData is the top row of the meter screen (input-side
// metadata about the cast source: codec, dimensions, crop, buffer).
type SourceStripIdleData struct {
	AudioIn   string
	AudioOut  string
	Src       string
	Crop      string
	HLSBuffer string
	Drops     string
}

// MidRowIdleData is the analytics row (bitrate, sample rate, MODE,
// NTSC/PAL lamps, ODD/EVEN/LOCK field-flip, throughput, ACK).
type MidRowIdleData struct {
	BitrateMbps   string
	FreqKHz       string
	Mode          string
	StandardNTSC  bool
	StandardPAL   bool
	FieldFlip     string
	ThroughputMBs string
	MSAck         string
}

// ReadoutIdleData is the bottom row (audio scope cluster + OUTPUT /
// ASPECT + PIPE / SPEED / LINK).
type ReadoutIdleData struct {
	LRBars      int // 0-12, number of lit segments per L/R bar
	PhaseNeedle string
	LUFS        string
	Output      string
	Aspect      string
	Pipe        string
	Speed       string
	Link        string
}

// TransportData drives the transport row (play/pause/stop buttons,
// seek bar, time readout, gear). Phase 0 idle: buttons dim, seek 0%.
type TransportData struct {
	PlayState       string
	ElapsedTime     string
	TotalTime       string
	PercentPlayed   string
	SeekFillPercent int
}

// VisualizerData drives the 4-button visualizer-bank selector. One of
// the buttons (radial_spectrum) is rendered as a deferred preview.
type VisualizerData struct {
	ActiveMode string
	Buttons    []VisualizerButton
}

// VisualizerButton represents one visualizer-bank button. IsPreview
// renders the deferred-state badge and short-circuits click handlers.
type VisualizerButton struct {
	Mode      string
	Label     string
	IconKind  string
	IsPreview bool
}

// InputData drives the paste/cast row.
type InputData struct {
	PastePlaceholder string
	DetectedKind     string
	CastEnabled      bool
}

// PresetsData drives the 12-slot preset bank.
type PresetsData struct {
	ModeLabel string
	Count     string
	Slots     [12]PresetSlot
}

// PresetSlot is one entry in the preset grid. Empty Filled=false slots
// still render a numbered placeholder.
type PresetSlot struct {
	Filled   bool
	Title    string
	Subtitle string
}

// HistoryData is the recent-casts row. Empty in Phase 0 idle.
type HistoryData struct {
	Rows         []HistoryRow
	EmptyMessage string
}

// HistoryRow represents one entry in the recent-casts row. Empty in
// Phase 0; populated in Spec 9.
type HistoryRow struct {
	Title   string
	Source  string
	When    string
	Artwork string
}

// SettingsData is the settings drawer (closed in Phase 0).
type SettingsData struct {
	Open bool
}
```

- [ ] **Step 2: Verify the package still compiles**

Run: `go build ./internal/chassis/...`
Expected: build succeeds, no output.

- [ ] **Step 3: Run existing tests to verify no regression**

Run: `go test ./internal/chassis/...`
Expected: PASS — all tests from Task 1 + Task 2 still green.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/data.go
git commit -m "feat(chassis): add ReceiverPageData struct hierarchy

Defines the page-level data struct shell.html will render against,
plus 12 sub-structs (VFD, Source, Meter, Transport, Visualizer,
Input, Presets, History, Settings) and their nested types. Live-state
fields are present but zero in Phase 0; subsequent specs populate.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: idleSnapshot Helper

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing tests for `idleSnapshot`**

Append to `internal/chassis/chassis_test.go`:

```go
func TestIdleSnapshot_AllFieldsPopulated(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.HostIP = "10.0.0.5"
	got := idleSnapshot(cfg, fixedNow)

	if got.State != StateIdle {
		t.Errorf("State = %q, want %q", got.State, StateIdle)
	}
	if got.Version != "test-1.0.0" {
		t.Errorf("Version = %q, want test-1.0.0", got.Version)
	}
	if got.BrandName != "GROOVY · RELAY" {
		t.Errorf("BrandName = %q, want GROOVY · RELAY", got.BrandName)
	}
	if got.HostInfo.HostIP != "10.0.0.5" {
		t.Errorf("HostInfo.HostIP = %q, want 10.0.0.5", got.HostInfo.HostIP)
	}
	if got.VFD.Title != "STANDBY" {
		t.Errorf("VFD.Title = %q, want STANDBY", got.VFD.Title)
	}
	if got.VFD.SystemTime != "22:47" {
		t.Errorf("VFD.SystemTime = %q, want 22:47", got.VFD.SystemTime)
	}
	if len(got.Source.Buttons) != 4 {
		t.Errorf("Source.Buttons length = %d, want 4", len(got.Source.Buttons))
	}
	if !got.Source.Buttons[0].Active {
		t.Errorf("Source.Buttons[0] (STREAMS) should be Active in idle")
	}
	if !got.Meter.MidRow.StandardNTSC {
		t.Errorf("Meter.MidRow.StandardNTSC should be true (NTSC is the v1 standard)")
	}
	if got.Meter.MidRow.StandardPAL {
		t.Errorf("Meter.MidRow.StandardPAL should be false (dim in idle)")
	}
	if got.Visualizer.ActiveMode == "" {
		t.Errorf("Visualizer.ActiveMode should not be empty")
	}
	if len(got.Visualizer.Buttons) != 4 {
		t.Errorf("Visualizer.Buttons length = %d, want 4", len(got.Visualizer.Buttons))
	}
	if !got.Visualizer.Buttons[3].IsPreview {
		t.Errorf("Visualizer.Buttons[3] (radial_spectrum) should be IsPreview")
	}
	for i, slot := range got.Presets.Slots {
		if slot.Filled {
			t.Errorf("Presets.Slots[%d] should be empty (Filled=false) in idle", i)
		}
	}
	if got.Input.CastEnabled {
		t.Errorf("Input.CastEnabled should be false in idle")
	}
	if got.Settings.Open {
		t.Errorf("Settings.Open should be false in Phase 0")
	}
}

func TestIdleSnapshot_DeterministicGivenSameNow(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	a := idleSnapshot(cfg, fixedNow)
	b := idleSnapshot(cfg, fixedNow)
	if a != b {
		t.Errorf("idleSnapshot is not deterministic")
	}
}

func TestIdleSnapshot_EmptyHostIPRendersAsOFFLINE(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.HostIP = ""
	got := idleSnapshot(cfg, fixedNow)
	if got.HostInfo.HostIP != "OFFLINE" {
		t.Errorf("HostInfo.HostIP with empty cfg.HostIP = %q, want OFFLINE", got.HostInfo.HostIP)
	}
}

func TestIdleSnapshot_InvalidVisualizerModeFallsBack(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = "bogus"
	got := idleSnapshot(cfg, fixedNow)
	if got.Visualizer.ActiveMode != "retro_analyzer" {
		t.Errorf("Visualizer.ActiveMode = %q, want retro_analyzer", got.Visualizer.ActiveMode)
	}
}

func TestIdleSnapshot_RadialPreviewModeFallsBack(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	cfg.Bridge.Visualizer.Mode = "radial_spectrum"
	got := idleSnapshot(cfg, fixedNow)
	if got.Visualizer.ActiveMode != "retro_analyzer" {
		t.Errorf("Visualizer.ActiveMode = %q, want retro_analyzer for preview-only radial_spectrum", got.Visualizer.ActiveMode)
	}
}

func TestIdleSnapshot_UptimeFromStartedAt(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 5, 21, 18, 35, 0, 0, time.UTC)
	now := time.Date(2026, 5, 21, 22, 47, 0, 0, time.UTC) // 4h 12m later
	cfg := nonZeroConfig()
	cfg.StartedAt = startedAt
	got := idleSnapshot(cfg, now)
	if got.VFD.Uptime != "4H 12M" {
		t.Errorf("VFD.Uptime = %q, want 4H 12M", got.VFD.Uptime)
	}
}
```

The struct comparison in `TestIdleSnapshot_DeterministicGivenSameNow` requires `ReceiverPageData` to be comparable. Since it contains slices (`Source.Buttons`, `Visualizer.Buttons`, `History.Rows`) and an array (`Presets.Slots`), struct equality on `ReceiverPageData` is not directly possible — adjust the test to compare via `reflect.DeepEqual`:

Replace the `if a != b` block with:

```go
	if !reflect.DeepEqual(a, b) {
		t.Errorf("idleSnapshot is not deterministic: a=%+v b=%+v", a, b)
	}
```

and add `"reflect"` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestIdleSnapshot`
Expected: FAIL — `undefined: idleSnapshot`.

- [ ] **Step 3: Implement `idleSnapshot` in `data.go`**

Merge the imports into `internal/chassis/data.go` immediately after `package chassis`, then append the helper functions below the existing type declarations. Do not append an `import` block after declarations; Go imports must stay directly under the package clause.

```go
import (
	"fmt"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)
```

Append the helper functions below the existing type declarations:

```go
// idleSnapshot returns a fully populated ReceiverPageData with State =
// StateIdle and placeholder content matching the mockup's idle state.
// Spec 2 (VFD live) will replace this with snapshotFromSession that
// reads real session state and falls back to idleSnapshot when no
// session is active.
//
// Field values mirror Appendix A of the design doc. The selector
// anchors in that appendix point at the mockup elements each field
// corresponds to.
func idleSnapshot(cfg Config, now time.Time) ReceiverPageData {
	hostIP := cfg.HostIP
	if hostIP == "" {
		hostIP = "OFFLINE"
	}

	return ReceiverPageData{
		Version:   cfg.Version,
		BrandName: "GROOVY · RELAY",
		State:     StateIdle,
		HostInfo: HostInfo{
			HostIP:   hostIP,
			HTTPPort: cfg.Bridge.UI.HTTPPort,
		},
		VFD: VFDData{
			State:        string(StateIdle),
			Title:        "STANDBY",
			Marquee:      "MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET",
			QueueCurrent: 0,
			QueueTotal:   0,
			SystemTime:   fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute()),
			Uptime:       formatUptime(now.Sub(cfg.StartedAt)),
		},
		Source: SourceData{
			Buttons: []SourceButton{
				{Label: "STREAMS", Active: true, Lit: false},
				{Label: "PLEX", Active: false, Lit: false},
				{Label: "JELLYFIN", Active: false, Lit: false},
				{Label: "DLNA", Active: false, Lit: false},
			},
		},
		Meter: MeterData{
			State: string(StateIdle),
			SourceStrip: SourceStripIdleData{
				AudioIn:   "---",
				AudioOut:  "---",
				Src:       "---",
				Crop:      "---",
				HLSBuffer: "0 / 0 SEG",
				Drops:     "0.0",
			},
			MidRow: MidRowIdleData{
				BitrateMbps:   "0.0",
				FreqKHz:       "---",
				Mode:          "---",
				StandardNTSC:  true,
				StandardPAL:   false,
				FieldFlip:     "idle",
				ThroughputMBs: "0.0",
				MSAck:         "--",
			},
			Readout: ReadoutIdleData{
				LRBars:      0,
				PhaseNeedle: "0",
				LUFS:        "---",
				Output:      "---",
				Aspect:      "---",
				Pipe:        "---",
				Speed:       "---",
				Link:        "---",
			},
		},
		Transport: TransportData{
			PlayState:       "stopped",
			ElapsedTime:     "--:--",
			TotalTime:       "--:--",
			PercentPlayed:   "---",
			SeekFillPercent: 0,
		},
		Visualizer: VisualizerData{
			ActiveMode: defaultVisualizerMode(cfg),
			Buttons: []VisualizerButton{
				{Mode: "retro_analyzer", Label: "ANALYZER", IconKind: "analyzer", IsPreview: false},
				{Mode: "oscilloscope_wave", Label: "OSCILLOSCOPE", IconKind: "wave", IsPreview: false},
				{Mode: "stereo_scope", Label: "STEREO SCOPE", IconKind: "scope", IsPreview: false},
				{Mode: "radial_spectrum", Label: "RADIAL", IconKind: "radial", IsPreview: true},
			},
		},
		Input: InputData{
			PastePlaceholder: "Paste URL or magnet",
			DetectedKind:     "URL",
			CastEnabled:      false,
		},
		Presets: PresetsData{
			ModeLabel: "Memory · 0 / 12 slots",
			Count:     "★ 0",
			// Slots is [12]PresetSlot{}, all zero-valued (Filled: false).
		},
		History: HistoryData{
			Rows:         nil,
			EmptyMessage: "No recent casts",
		},
		Settings: SettingsData{
			Open: false,
		},
	}
}

// formatUptime turns a duration into the "Nh Nm" / "NH NM" string used
// by the VFD freq line. Returns "0H 0M" for zero or negative durations.
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "0H 0M"
	}
	total := int(d / time.Minute)
	hours := total / 60
	minutes := total % 60
	return fmt.Sprintf("%dH %dM", hours, minutes)
}

// defaultVisualizerMode reads the active visualizer mode from the
// bridge config, falling back to retro_analyzer if the config field
// is empty, invalid, or preview-only.
func defaultVisualizerMode(cfg Config) string {
	switch mode := cfg.Bridge.Visualizer.Mode; mode {
	case config.VisualizerModeRetroAnalyzer,
		config.VisualizerModeOscilloscopeWave,
		config.VisualizerModeStereoScope:
		return mode
	default:
		return config.VisualizerModeRetroAnalyzer
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestIdleSnapshot`
Expected: PASS — 6 tests.

- [ ] **Step 5: Run the full chassis package test suite**

Run: `go test ./internal/chassis/...`
Expected: PASS — all tests from Tasks 1-2 + the new idleSnapshot tests.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/data.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add idleSnapshot helper

Single source of truth for Phase 0's default page content. Populates
every sub-struct with idle placeholders matching the mockup. HostIP=''
renders as 'OFFLINE'; uptime formatted from StartedAt; visualizer
active mode read from bridge config with retro_analyzer fallback.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: Embed Declarations + Template Parser + Helper FuncMap

**Files:**
- Create: `internal/chassis/templates.go`
- Create: `internal/chassis/templates/shell.html` (minimal stub for parse test)
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Add the failing test for template parsing**

Append to `internal/chassis/chassis_test.go`:

```go
func TestTemplatesParse(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	if tmpl.Lookup("shell.html") == nil {
		t.Error("shell.html not found in parsed templates")
	}
}

func TestTemplatesExpectedHelpersAvailable(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	// Render a tiny harness that exercises each helper. If a helper is
	// missing or renamed, html/template fails to execute and we see it
	// here instead of in a later handler test where the failure mode
	// is harder to diagnose.
	probes := []struct {
		name, src string
	}{
		{"inc", `{{inc 0}}`},
		{"hasString", `{{hasString (list "a" "b") "b"}}`},
		{"replaceAll", `{{replaceAll "a/b" "/" "-"}}`},
		{"pad2", `{{pad2 5}}`},
		{"dim", `{{dim true}}`},
	}
	for _, p := range probes {
		probe, err := tmpl.New("probe-" + p.name).Parse(p.src)
		if err != nil {
			t.Fatalf("probe parse %s: %v", p.name, err)
		}
		var sb strings.Builder
		if err := probe.Execute(&sb, nil); err != nil {
			t.Errorf("helper %q execute: %v", p.name, err)
		}
	}
}
```

Add `"strings"` to the test file's imports.

- [ ] **Step 2: Create the minimal shell template**

Create `internal/chassis/templates/shell.html`:

```html
{{define "shell.html"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.BrandName}}</title>
  <link rel="stylesheet" href="/receiver/static/chassis.css?v={{.Version}}">
  <script defer src="/receiver/static/chassis.js?v={{.Version}}"></script>
</head>
<body class="receiver {{.State}}">
  <!-- chassis:shell -->
  <!-- additional partials added by later tasks -->
</body>
</html>{{end}}
```

- [ ] **Step 3: Create the minimal static CSS and JS stubs required by `go:embed` and `shell.html`**

Task 5 embeds the `static` directory, so the directory must exist before the package can compile. `shell.html` also links `chassis.js`, so create both static placeholders now. Task 6 preprocesses the CSS; Task 26 replaces the JS stub with the real runtime.

Create `internal/chassis/static/chassis.css`:

```css
/* Receiver chassis stylesheet. Sections + content land in Tasks 17-25.
   Phase 0 stub: just enough for embed + preprocessor to round-trip. */
@font-face {
  font-family: 'Inter';
  src: url('/receiver/static/fonts/Inter-Variable.woff2?v={{.Version}}') format('woff2-variations');
  font-weight: 100 900;
  font-style: normal;
  font-display: swap;
}
```

Create `internal/chassis/static/chassis.js`:

```js
// Receiver chassis runtime stub. Replaced by Task 26.
window.Chassis = window.Chassis || {};
```

- [ ] **Step 4: Create `templates.go` with embed FS + parser + helper FuncMap**

Create `internal/chassis/templates.go`:

```go
package chassis

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
)

// chassisTemplatesFS holds the html/template files used to render the
// chassis page. Embedded at build so the binary is self-contained;
// no filesystem reads at runtime.
//
//go:embed templates/*.html
var chassisTemplatesFS embed.FS

// chassisStaticFS holds chassis.css, chassis.js, and woff2 fonts.
// Embedded wholesale; served under /receiver/static/ via Mount.
//
//go:embed static
var chassisStaticFS embed.FS

// templateFuncs supplies the helpers the chassis templates need. The
// first three are duplicated verbatim from internal/ui/server.go:
// during the parallel-replacement period the chassis package has zero
// imports of internal/ui, so we accept coupling-by-copy rather than
// coupling-by-import. The final cutover spec deduplicates.
//
// Chassis-specific helpers:
//   pad2 — zero-padded two-digit strings for clock display.
//   dim  — returns the CSS class string for inactive lamps.
//   list — constructs a string slice for small template membership probes.
var templateFuncs = template.FuncMap{
	"inc":        func(i int) int { return i + 1 },
	"replaceAll": strings.ReplaceAll,
	"hasString": func(haystack []string, needle string) bool {
		for _, s := range haystack {
			if s == needle {
				return true
			}
		}
		return false
	},
	"pad2": func(n int) string {
		if n < 10 {
			return fmt.Sprintf("0%d", n)
		}
		return fmt.Sprintf("%d", n)
	},
	"dim": func(active bool) string {
		if active {
			return ""
		}
		return "dim"
	},
	"list": func(args ...string) []string { return args },
}

// parseTemplates parses the embedded chassis templates with the helper
// FuncMap pre-registered. Called once at server startup from New.
func parseTemplates() (*template.Template, error) {
	tmpl, err := template.New("chassis").Funcs(templateFuncs).ParseFS(chassisTemplatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("chassis: parse templates: %w", err)
	}
	return tmpl, nil
}
```

- [ ] **Step 5: Wire `parseTemplates` into `New` in `server.go`**

Modify the `New` function in `internal/chassis/server.go` to add template parsing. Replace the existing body:

```go
func New(cfg Config) (*Server, error) {
	if cfg.Version == "" {
		return nil, fmt.Errorf("chassis: Config.Version is required")
	}
	if cfg.StartedAt.IsZero() {
		return nil, fmt.Errorf("chassis: Config.StartedAt is required (zero time disallowed)")
	}
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, tmpl: tmpl}, nil
}
```

Update the `Server` struct to include `tmpl`:

```go
type Server struct {
	cfg  Config
	tmpl *template.Template
}
```

Add `"html/template"` to the `server.go` imports.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/chassis/...`
Expected: PASS — `TestTemplatesParse`, `TestTemplatesExpectedHelpersAvailable`, plus all prior tests.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/templates.go internal/chassis/templates/shell.html internal/chassis/static/chassis.css internal/chassis/static/chassis.js internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add embed FS + template parser + helper FuncMap

Two go:embed declarations (templates and static), one template.Funcs
binding for inc/hasString/replaceAll (duplicated verbatim from
internal/ui per the parallel-replacement isolation contract) plus
chassis-specific pad2, dim, and list. New() now parses templates at
startup so handler tests can render shell.html in later tasks. Adds
minimal CSS/JS static stubs so shell-linked assets do not 404 before
the full stylesheet/runtime land.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: CSS Version Substitution (text/template)

**Files:**
- Modify: `internal/chassis/templates.go`
- Modify: `internal/chassis/server.go`
- Create: `internal/chassis/static/chassis.css` (minimal stub with one substituted font URL)
- Modify: `internal/chassis/chassis_test.go`

The handler will serve `chassis.css` with `{{.Version}}` placeholders inside `url(...)` declarations substituted at server startup. Using `text/template` (not `html/template`) avoids context-aware escaping that would break CSS.

- [ ] **Step 1: Add the failing test for CSS substitution**

Append to `internal/chassis/chassis_test.go`:

```go
func TestPreprocessCSS_SubstitutesVersionPlaceholder(t *testing.T) {
	t.Parallel()
	src := []byte(`@font-face { src: url('/receiver/static/fonts/Inter-Variable.woff2?v={{.Version}}'); }`)
	got, err := preprocessCSS(src, "test-1.2.3")
	if err != nil {
		t.Fatalf("preprocessCSS: %v", err)
	}
	want := "?v=test-1.2.3"
	if !strings.Contains(string(got), want) {
		t.Errorf("preprocessCSS output = %s, want substring %s", got, want)
	}
	if strings.Contains(string(got), "{{.Version}}") {
		t.Errorf("preprocessCSS left a raw placeholder in output: %s", got)
	}
}

func TestPreprocessCSS_LeavesCSSCommentsAlone(t *testing.T) {
	t.Parallel()
	// html/template would mangle "<=" inside CSS comments; text/template
	// must not. This test locks that distinction in.
	src := []byte(`/* breakpoint <= 1180px */`)
	got, err := preprocessCSS(src, "v1")
	if err != nil {
		t.Fatalf("preprocessCSS: %v", err)
	}
	if !strings.Contains(string(got), `<=`) {
		t.Errorf("preprocessCSS escaped <= in CSS comment: %s", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestPreprocessCSS`
Expected: FAIL — `undefined: preprocessCSS`.

- [ ] **Step 3: Implement `preprocessCSS` using `text/template`**

Replace the imports block at the top of `internal/chassis/templates.go` with this complete final form (consolidate any prior imports into one block):

```go
import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	textTemplate "text/template"
)
```

The `textTemplate` alias is required because the file imports both `html/template` (for `parseTemplates`) and `text/template` (for `preprocessCSS`); without the alias the two packages collide on the unqualified name `template`.

Add the function below the existing `parseTemplates` function:

```go
// preprocessCSS substitutes {{.Version}} placeholders inside the
// embedded chassis.css and returns the result. Uses text/template
// (not html/template) because CSS is not HTML — html/template's
// context-aware escaping would mangle characters like "<" and ">"
// inside CSS comments (e.g., "/* breakpoint <= 1180px */").
//
// Called once at server startup from New; the result is cached in
// Server.cssBytes and served verbatim from handleCSS.
func preprocessCSS(src []byte, version string) ([]byte, error) {
	tmpl, err := textTemplate.New("chassis.css").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("chassis: parse CSS template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Version": version}); err != nil {
		return nil, fmt.Errorf("chassis: execute CSS template: %w", err)
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 4: Verify the minimal `chassis.css` stub for CSS preprocessing**

Task 5 created `internal/chassis/static/chassis.css` so `//go:embed static` had a match. Ensure it still has the version placeholder that `preprocessCSS` tests exercise (replace it with this content if needed):

```css
/* Receiver chassis stylesheet. Sections + content land in Tasks 17-25.
   Phase 0 stub: just enough for embed + preprocessor to round-trip. */
@font-face {
  font-family: 'Inter';
  src: url('/receiver/static/fonts/Inter-Variable.woff2?v={{.Version}}') format('woff2-variations');
  font-weight: 100 900;
  font-style: normal;
  font-display: swap;
}
```

- [ ] **Step 5: Wire `preprocessCSS` into `New` and store the result on `Server`**

Modify `internal/chassis/server.go`:

```go
type Server struct {
	cfg       Config
	tmpl      *template.Template
	cssBytes  []byte // chassis.css with {{.Version}} substituted, cached
}
```

No new `server.go` imports are needed for `ReadFile`; update `New`:

```go
func New(cfg Config) (*Server, error) {
	if cfg.Version == "" {
		return nil, fmt.Errorf("chassis: Config.Version is required")
	}
	if cfg.StartedAt.IsZero() {
		return nil, fmt.Errorf("chassis: Config.StartedAt is required (zero time disallowed)")
	}
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	cssSrc, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		return nil, fmt.Errorf("chassis: read embedded chassis.css: %w", err)
	}
	cssBytes, err := preprocessCSS(cssSrc, cfg.Version)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, tmpl: tmpl, cssBytes: cssBytes}, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/chassis/...`
Expected: PASS — `TestPreprocessCSS_*` plus everything from prior tasks.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/templates.go internal/chassis/server.go internal/chassis/static/chassis.css internal/chassis/chassis_test.go
git commit -m "feat(chassis): preprocess chassis.css with text/template

Substitutes {{.Version}} placeholders inside chassis.css at server
startup, caches the bytes on Server. Uses text/template explicitly so
context-aware escaping does not mangle <= inside CSS breakpoint
comments — a real failure case under html/template that the spec
calls out.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: handleIndex + Static Asset Handler + Mount

**Files:**
- Modify: `internal/chassis/handler.go`
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go`

Now wire the handler. Both routes (index and static) plus the Mount registration.

- [ ] **Step 1: Add the failing handler tests**

Merge the new standard-library packages (`io`, `net/http`, `net/http/httptest`) into the existing `internal/chassis/chassis_test.go` import block, then append the tests below the existing tests:

```go
// newTestServer builds a Server with a deterministic Config so handler
// responses are stable across test runs. Tests should not call New
// directly because it requires non-trivial config wiring.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("newTestServer: %v", err)
	}
	return s
}

func TestHandleIndex_RendersShell200(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html prefix", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<body class="receiver idle">`) {
		t.Errorf("body missing chassis-idle body class: %s", body[:min(len(body), 200)])
	}
	if !strings.Contains(body, "GROOVY · RELAY") {
		t.Errorf("body missing brand name")
	}
	if !strings.Contains(body, "<!-- chassis:shell -->") {
		t.Errorf("body missing shell sentinel comment")
	}
}

func TestHandleIndex_AssetURLsCarryVersionQueryParam(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "/receiver/static/chassis.css?v=test-1.0.0") {
		t.Errorf("body missing versioned CSS URL")
	}
	if !strings.Contains(body, "/receiver/static/chassis.js?v=test-1.0.0") {
		t.Errorf("body missing versioned JS URL")
	}
}

func TestHandleStatic_CSS_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/chassis.css", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css prefix", ct)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("Cache-Control = %q, want max-age=31536000", cc)
	}
}

func TestHandleStatic_CSS_VersionedFontURLs(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/chassis.css", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "?v=test-1.0.0") {
		t.Errorf("served CSS missing substituted version: %s", body)
	}
	if strings.Contains(body, "{{.Version}}") {
		t.Errorf("served CSS still has raw {{.Version}} placeholder")
	}
}

func TestHandleStatic_UnknownAsset404(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/nonexistent.css", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleStatic_PathTraversalBlocked(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	// http.FileServer normalizes "../" and refuses to escape the FS
	// root. The status is either 404 or a clean redirect — what we
	// care about is that the response is not a 200 with file contents
	// from outside the embed FS.
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/../config.toml", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("path traversal returned 200: body=%q", rec.Body.String())
	}
}

func TestMount_TrailingSlashIndex(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	// Both /receiver and /receiver/ must serve the shell.
	for _, p := range []string{"/receiver", "/receiver/"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", p, rec.Code)
		}
	}
}
```

Note: the `body[:min(len(body), 200)]` expression in `TestHandleIndex_RendersShell200` above uses Go 1.21+'s built-in `min` directly. Do not declare a local `min(int, int) int` helper — it would shadow the builtin and trigger `go vet`'s `predeclared` analyzer.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestHandle|TestMount_Trailing'`
Expected: FAIL — `s.handleIndex undefined`, `Mount has empty body`.

- [ ] **Step 3: Implement `handleIndex` in `handler.go`**

Replace the top of `internal/chassis/handler.go` with one consolidated import block (preserving the MIME init from Task 2), then append `handleIndex` below the existing `init` function:

```go
import (
	"mime"
	"net/http"
	"time"
)

// handleIndex renders the chassis shell with idle placeholder data.
// Spec 2 (VFD live) replaces idleSnapshot here with snapshotFromSession
// so the same handler renders live state when a cast is active.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data := idleSnapshot(s.cfg, time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shell.html", data); err != nil {
		// Template execution failure after headers are written can't
		// recover with a clean status. Log via the http.Error path
		// (writes 500 if headers not yet sent).
		http.Error(w, "template execute failed", http.StatusInternalServerError)
	}
}
```

- [ ] **Step 4: Implement `handleStatic` for the embedded asset subtree**

Add `io/fs` to the same `handler.go` import block, then append `handleStatic` below `handleIndex`. The final import block after this step should contain `io/fs`, `mime`, `net/http`, and `time`.

```go
// handleStatic serves the embedded chassis static assets under
// /receiver/static/. CSS goes through the cached preprocessed bytes
// (with {{.Version}} substituted); everything else (JS, fonts) is
// served raw from the embed FS via http.FileServer.
//
// Cache-Control is set to immutable + one year because asset paths
// carry a ?v=<build-version> query bust from shell.html. The query
// string is ignored by the handler — it only exists so browsers
// re-fetch on version change.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// /receiver/static/chassis.css needs the preprocessed bytes (with
	// {{.Version}} substituted). Everything else goes through
	// http.FileServer for normal MIME + 404 handling.
	if r.URL.Path == "/receiver/static/chassis.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(s.cssBytes)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	staticSub, _ := fs.Sub(chassisStaticFS, "static")
	http.StripPrefix("/receiver/static/", http.FileServer(http.FS(staticSub))).ServeHTTP(w, r)
}
```

- [ ] **Step 5: Implement `Mount` in `server.go`**

Replace the empty `Mount` body in `internal/chassis/server.go`:

```go
// Mount registers chassis routes on mux. Two route trees:
//   - GET /receiver and GET /receiver/{$} both render shell.html.
//   - GET /receiver/static/ serves embedded assets.
//
// Both routes are method-aware (Go 1.22 mux) so non-GET requests
// fall through naturally to a 405 from the default handler.
//
// The chassis package owns no other routes in Phase 0. Spec 2 adds
// /receiver/events (SSE); Spec 3+ add interactive routes.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /receiver", s.handleIndex)
	mux.HandleFunc("GET /receiver/{$}", s.handleIndex)
	mux.HandleFunc("GET /receiver/static/", s.handleStatic)
}
```

Add `"net/http"` to the `server.go` imports if not already present.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/chassis/...`
Expected: PASS — all handler tests plus everything from earlier tasks.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/handler.go internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): implement handleIndex + handleStatic + Mount

GET /receiver and GET /receiver/{$} both render shell.html with
idleSnapshot data. GET /receiver/static/* serves embedded CSS (with
{{.Version}} substituted), JS, and fonts via http.StripPrefix +
http.FileServer. Cache-Control immutable + one year, busted via the
?v= query param injected by shell.html.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: Stub Partial Files + Composition

**Files:**
- Modify: `internal/chassis/templates/shell.html`
- Create: 12 partial files in `internal/chassis/templates/`
- Modify: `internal/chassis/chassis_test.go`

Each partial gets a `{{define ...}}` block with a sentinel HTML comment. Content is intentionally minimal — Tasks 9-15 port the real mockup HTML into each partial. This task just establishes the composition skeleton + sentinel-marker test.

- [ ] **Step 1: Add the failing test for sentinel markers**

Append to `internal/chassis/chassis_test.go`:

```go
func TestHandleIndex_IncludesEveryPartialMarker(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	markers := []string{
		"<!-- chassis:status-bar -->",
		"<!-- chassis:masthead -->",
		"<!-- chassis:vfd-source-row -->",
		"<!-- chassis:vfd -->",
		"<!-- chassis:source-cluster -->",
		"<!-- chassis:meter -->",
		"<!-- chassis:transport -->",
		"<!-- chassis:visualizer-bank -->",
		"<!-- chassis:input-row -->",
		"<!-- chassis:preset-bank -->",
		"<!-- chassis:history -->",
		"<!-- chassis:settings-drawer -->",
	}
	for _, m := range markers {
		if !strings.Contains(body, m) {
			t.Errorf("body missing marker %q", m)
		}
	}
}
```

- [ ] **Step 2: Update `shell.html` to compose all partials**

Replace `internal/chassis/templates/shell.html` content with the full composition:

```html
{{define "shell.html"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.BrandName}}</title>
  <link rel="stylesheet" href="/receiver/static/chassis.css?v={{.Version}}">
  <script defer src="/receiver/static/chassis.js?v={{.Version}}"></script>
</head>
<body class="receiver {{.State}}">
  <!-- chassis:shell -->
  <div class="receiver">
    {{template "status-bar" .HostInfo}}
    {{template "masthead" .BrandName}}
    <div class="receiver-inner">
      {{template "vfd-source-row" .}}
      {{template "meter" .Meter}}
      {{template "transport" .Transport}}
      {{template "visualizer-bank" .Visualizer}}
      {{template "input-row" .Input}}
      {{template "preset-bank" .Presets}}
      {{template "history" .History}}
    </div>
    {{template "settings-drawer" .Settings}}
  </div>
</body>
</html>{{end}}
```

- [ ] **Step 3: Create the 12 stub partial files**

Create each file with the listed contents. These are placeholders — Tasks 9-15 expand them with real markup ported from the mockup.

Create `internal/chassis/templates/status-bar.html`:

```html
{{define "status-bar"}}
<!-- chassis:status-bar -->
<div class="status-bar"></div>
{{end}}
```

Create `internal/chassis/templates/masthead.html`:

```html
{{define "masthead"}}
<!-- chassis:masthead -->
<div class="masthead"></div>
{{end}}
```

Create `internal/chassis/templates/vfd-source-row.html`:

```html
{{define "vfd-source-row"}}
<!-- chassis:vfd-source-row -->
<div class="vfd-source-row">
  {{template "vfd" .VFD}}
  {{template "source-cluster" .Source}}
</div>
{{end}}
```

Create `internal/chassis/templates/vfd.html`:

```html
{{define "vfd"}}
<!-- chassis:vfd -->
<div class="screen-frame"><div class="screen vfd"></div></div>
{{end}}
```

Create `internal/chassis/templates/source-cluster.html`:

```html
{{define "source-cluster"}}
<!-- chassis:source-cluster -->
<div class="source-cluster"></div>
{{end}}
```

Create `internal/chassis/templates/meter.html`:

```html
{{define "meter"}}
<!-- chassis:meter -->
<div class="meter-source-row"></div>
{{end}}
```

Create `internal/chassis/templates/transport.html`:

```html
{{define "transport"}}
<!-- chassis:transport -->
<div class="transport-strip"></div>
{{end}}
```

Create `internal/chassis/templates/visualizer-bank.html`:

```html
{{define "visualizer-bank"}}
<!-- chassis:visualizer-bank -->
<div class="section-strip viz-section"></div>
{{end}}
```

Create `internal/chassis/templates/input-row.html`:

```html
{{define "input-row"}}
<!-- chassis:input-row -->
<div class="section-strip input-section"></div>
{{end}}
```

Create `internal/chassis/templates/preset-bank.html`:

```html
{{define "preset-bank"}}
<!-- chassis:preset-bank -->
<div class="preset-strip preset-section"></div>
{{end}}
```

Create `internal/chassis/templates/history.html`:

```html
{{define "history"}}
<!-- chassis:history -->
<div class="history-section"></div>
{{end}}
```

Create `internal/chassis/templates/settings-drawer.html`:

```html
{{define "settings-drawer"}}
<!-- chassis:settings-drawer -->
<div class="settings-panel"></div>
{{end}}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/...`
Expected: PASS — `TestHandleIndex_IncludesEveryPartialMarker` plus all prior tests.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/
git commit -m "feat(chassis): stub 12 partial templates with sentinel markers

Each partial declares its {{define}} block plus a sentinel HTML comment
that test handlers grep for to confirm composition. Markup bodies are
intentionally minimal — Tasks 9-15 port the real mockup HTML into each
partial. Phase 0 hereafter renders a structurally-complete-but-empty
chassis shell.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 9: Port status-bar + masthead Partials

**Files:**
- Modify: `internal/chassis/templates/status-bar.html`
- Modify: `internal/chassis/templates/masthead.html`

Port from the mockup. The mockup's status bar contains the brand plate, build version, LED indicators, and load-core button; masthead contains the GROOVY • RELAY title plate.

- [ ] **Step 1: Inspect the mockup**

Open `.superpowers/brainstorm/1973-1779237107/receiver-v24.html` and locate:
- The `.status-bar` `<div>` near line 3450 (use editor search for `class="status-bar"`).
- The `.brand-plate` and `.masthead` near line 3467.

- [ ] **Step 2: Port status-bar.html**

Replace `internal/chassis/templates/status-bar.html` content. Copy the HTML structure from the mockup's `.status-bar` block; replace placeholders with template directives matching the `HostInfo` sub-struct (the partial receives `.HostInfo`). Keep the sentinel comment.

```html
{{define "status-bar"}}
<!-- chassis:status-bar -->
<div class="status-bar">
  <div class="brand-plate"><span class="name">GROOVY · RELAY</span><span class="model">RECEIVER 2026</span></div>
  <div class="status-spacer"></div>
  <div class="led-row">
    <span class="led on" title="Bridge online"><span class="dot"></span><span class="lbl">BRIDGE</span></span>
    <span class="led" title="MiSTer link"><span class="dot"></span><span class="lbl">MISTER</span></span>
    <span class="led" title="Cast active"><span class="dot"></span><span class="lbl">CAST</span></span>
  </div>
  <button class="load-core-btn"><span class="dot"></span><span class="label-text">LOAD CORE</span></button>
</div>
{{end}}
```

- [ ] **Step 3: Port masthead.html**

Replace `internal/chassis/templates/masthead.html` content. The masthead partial receives `.BrandName` (a string), so the template directive is `{{.}}`:

```html
{{define "masthead"}}
<!-- chassis:masthead -->
<div class="masthead">
  <div class="brand-plate masthead-plate"><span class="name">{{.}}</span></div>
</div>
{{end}}
```

- [ ] **Step 4: Run tests to verify partials still parse + render**

Run: `go test ./internal/chassis/...`
Expected: PASS — `TestHandleIndex_IncludesEveryPartialMarker` still finds both markers.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/status-bar.html internal/chassis/templates/masthead.html
git commit -m "feat(chassis): port status-bar + masthead partials from mockup

Adds the BRIDGE / MISTER / CAST LED row, brand plate, and load-core
button to the status-bar partial. Masthead partial renders the
GROOVY · RELAY brand name passed in via .BrandName.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 10: Port vfd-source-row + vfd + source-cluster Partials

**Files:**
- Modify: `internal/chassis/templates/vfd.html`
- Modify: `internal/chassis/templates/source-cluster.html`

`vfd-source-row.html` already composes `vfd` + `source-cluster` from Task 8; this task fleshes out the two component partials with their idle-state markup.

- [ ] **Step 1: Locate VFD idle state in the mockup**

Grep the mockup for `vfd-state--idle` (around line 3500-3520). The idle VFD shows `STANDBY` title, the marquee hint string, and the right-panel system time + uptime.

- [ ] **Step 2: Port vfd.html**

Replace `internal/chassis/templates/vfd.html`:

```html
{{define "vfd"}}
<!-- chassis:vfd -->
<div class="screen-frame">
  <div class="screen vfd">
    <div class="vfd-state vfd-state--idle">
      <div>
        <div class="title-line seg-display" data-ghost="~~~~~~~">{{.Title}}</div>
        <div class="marquee-line seg-display" data-ghost="~~~~~~ ~~~~ ~~ ~ ~~~~ ~~ ~~ ~~~~~~~ ~ ~~ ~~~~~~~ ~ ~ ~~~~~ ~~~ ~~ ~~~~ ~ ~~~~~~">{{.Marquee}}</div>
      </div>
      <div class="right-panel">
        <div class="lbl">System time</div>
        <div class="time-row">
          <div class="queue-stack">
            <div class="queue-v seg-display" data-ghost="88 ~ 88">{{.QueueCurrent}} / {{.QueueTotal}}</div>
            <div class="queue-k">QUEUE</div>
          </div>
          <div class="big-time"><span class="seg-display" data-ghost="88:88" data-system-time>{{.SystemTime}}</span></div>
        </div>
        <div class="freq"><span class="seg-display" data-ghost="~~~~~~">UPTIME</span> <span class="seg-display" data-ghost="88~ 88~">{{.Uptime}}</span></div>
      </div>
    </div>
  </div>
</div>
{{end}}
```

The `data-system-time` attribute is what the JS time ticker hooks onto in Task 27.

- [ ] **Step 3: Port source-cluster.html**

Replace `internal/chassis/templates/source-cluster.html`:

```html
{{define "source-cluster"}}
<!-- chassis:source-cluster -->
<div class="source-cluster">
  {{range .Buttons}}
  <button class="hw-btn{{if .Active}} active{{end}}{{if .Lit}} lit{{end}}" title="{{.Label}} adapter">{{.Label}}</button>
  {{end}}
</div>
{{end}}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/vfd.html internal/chassis/templates/source-cluster.html
git commit -m "feat(chassis): port VFD + source-cluster partials

VFD renders the idle state (STANDBY title, marquee hint, system time
with data-system-time hook for client-side ticker, queue, uptime).
Source-cluster iterates the 4 buttons from .Source.Buttons with
.Active and .Lit classes.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 11: Port meter.html (3-Row Signal Meter)

**Files:**
- Modify: `internal/chassis/templates/meter.html`

The meter is the most content-dense partial — three rows with audio + video + network groups in each. Use selector anchors from Appendix A of the spec to identify which mockup elements map to which data fields.

- [ ] **Step 1: Locate meter in the mockup**

Grep the mockup for `meter-source-row`, then identify the three rows: `meter-source-strip` (top), `meter-mid-row` (middle), `meter-readout-line` (bottom). The mockup's meter spans roughly lines 3580-3700.

- [ ] **Step 2: Port meter.html**

Replace `internal/chassis/templates/meter.html`. The partial receives `.Meter` which is `MeterData{SourceStrip, MidRow, Readout}`:

```html
{{define "meter"}}
<!-- chassis:meter -->
<div class="meter-source-row">
  <div class="screen-frame meter-screen-frame">
    <div class="screen meter-screen meter-screen--compact">

      {{/* ROW 1 — Source strip */}}
      <div class="meter-source-strip meter-row">
        <div class="audio-grp">
          <span class="grp codec-grp"><span class="key">AUDIO IN</span><span class="val">{{.SourceStrip.AudioIn}}</span></span>
          <span class="grp"><span class="key">AUDIO OUT</span><span class="seg-display" data-ghost="~~~~~ ~ ~~~">{{.SourceStrip.AudioOut}}</span></span>
        </div>
        <div class="video-grp">
          <span class="grp"><span class="key">SRC</span><span class="seg-display" data-ghost="~~~~~~~~~~~~~~ ~ ~~~~~">{{.SourceStrip.Src}}</span></span>
          <span class="grp"><span class="key">CROP</span><span class="seg-display" data-ghost="~~~~ ~ ~~~ ~~~~~~">{{.SourceStrip.Crop}}</span></span>
        </div>
        <div class="net-grp">
          <span class="grp"><span class="key">HLS BUF</span><span class="seg-display" data-ghost="8 ~ 88 ~~~">{{.SourceStrip.HLSBuffer}}</span></span>
          <span class="grp"><span class="key">DROPS</span><span class="seg-display" data-ghost="88.8">{{.SourceStrip.Drops}}</span></span>
        </div>
      </div>

      {{/* ROW 2 — Spectrum / stats / throughput */}}
      <div class="meter-mid-row meter-row">
        <div class="audio-grp">
          <div class="spectrum">
            {{range $i, $_ := until 6}}<div class="spectrum-band"><div class="stack">{{range until 8}}<div class="seg"></div>{{end}}<div class="peak-dot"></div></div><div class="lbl">{{index (list "60" "250" "1K" "4K" "8K" "16K") $i}}</div></div>{{end}}
          </div>
          <div class="gonio-wrap aux-scope" title="Stereo phase scope">
            <div class="gonio"><canvas id="gonio-canvas" width="172" height="172"></canvas><span class="lbl l">L</span><span class="lbl r">R</span><span class="lbl t">+</span><span class="lbl b">-</span></div>
            <span class="gonio-lbl aux-lbl">PHASE · X/Y</span>
          </div>
        </div>
        <div class="video-grp">
          <div class="stat"><div class="v seg-display" data-ghost="88.8">{{.MidRow.BitrateMbps}}</div><div class="k seg-display" data-ghost="~~~~">Mbps</div></div>
          <div class="stat"><div class="v seg-display" data-ghost="88.8">{{.MidRow.FreqKHz}}</div><div class="k seg-display" data-ghost="~~~~">kHz</div></div>
          <div class="stat"><div class="v seg-display" data-ghost="888">{{.MidRow.Mode}}</div><div class="k seg-display" data-ghost="~~~~">MODE</div></div>
          <div class="std-lamps std-lamps--stack" title="Output analog standard">
            <span class="std-ind{{if .MidRow.StandardNTSC}} active{{end}} seg-display" data-ghost="~~~~">NTSC</span>
            <span class="std-ind{{if .MidRow.StandardPAL}} active{{end}} seg-display" data-ghost="~~~">PAL</span>
          </div>
          <div class="field-flip" title="Interlace field interleave">
            <div class="row"><span class="dot odd"></span><span class="lbl">ODD</span></div>
            <div class="row"><span class="dot even"></span><span class="lbl">EVEN</span></div>
            <span class="lock">LOCK</span>
          </div>
        </div>
        <div class="net-grp">
          <div class="stat"><div class="v seg-display" data-ghost="88.8">{{.MidRow.ThroughputMBs}}</div><div class="k seg-display" data-ghost="~~~~">MB/S</div></div>
          <div class="throughput-wrap aux-scope" title="Network throughput · last 30 seconds"><div class="aux-screen"><canvas id="throughput-canvas" width="220" height="120"></canvas></div><span class="aux-lbl" id="throughput-lbl">NET · 0.0 MB/S</span></div>
          <div class="stat"><div class="v seg-display" data-ghost="88.8">{{.MidRow.MSAck}}</div><div class="k seg-display" data-ghost="~~~~~~">MS ACK</div></div>
          <div class="ack-wrap aux-scope" title="ACK latency · last 128 frames"><div class="aux-screen"><canvas id="ack-canvas" width="220" height="120"></canvas></div><span class="aux-lbl" id="ack-lbl">ACK · -- MS</span></div>
        </div>
      </div>

      {{/* ROW 3 — Readout / audio scope / OUTPUT / PIPE */}}
      <div class="meter-readout-line meter-row">
        <div class="audio-grp">
          <div class="tr-vu" title="Audio out · L/R level · phase · LUFS">
            <div class="vu-lr">
              <span class="ch-lbl">L</span>
              <span class="ch-bar">{{range until 12}}<span class="s"></span>{{end}}</span>
              <span class="ch-lbl">R</span>
              <span class="ch-bar">{{range until 12}}<span class="s"></span>{{end}}</span>
            </div>
            <span class="vu-sep"></span>
            <div class="vu-phase" title="Stereo phase correlation">
              <span class="lbl">PHASE</span>
              <div class="bar"><div class="needle" id="phase-needle"></div></div>
              <div class="scale"><span>-1</span><span>0</span><span>+1</span></div>
            </div>
            <span class="vu-sep"></span>
            <div class="vu-lufs"><span class="lbl">LUFS · ST</span><span class="val seg-display" id="lufs-val" data-ghost="~88.8">{{.Readout.LUFS}}</span></div>
          </div>
        </div>
        <div class="video-grp">
          <span class="grp"><span class="key">OUTPUT</span><span class="seg-display" data-ghost="~~~~~~~~~~ ~~~~ ~ ~~~~~~">{{.Readout.Output}}</span></span>
          <span class="grp"><span class="key">ASPECT</span><span class="seg-display" data-ghost="~~~ ~~~~~~~~~~">{{.Readout.Aspect}}</span></span>
        </div>
        <div class="net-grp">
          <span class="grp"><span class="key">PIPE</span><span class="seg-display" data-ghost="~~~~ ~ ~~~~">{{.Readout.Pipe}}</span></span>
          <span class="grp speed-grp"><span class="key">SPEED</span><span class="val seg-display" data-ghost="8.88~ ~~~~">{{.Readout.Speed}}</span></span>
          <span class="grp link-grp"><span class="key">LINK</span><span class="val seg-display" data-ghost="~~~~~~ ~ ~~~~~~~">{{.Readout.Link}}</span></span>
        </div>
      </div>

    </div>
  </div>
</div>
{{end}}
```

The `until N` template helper repeats a block N times. It is not in the chassis FuncMap yet — add it next.

- [ ] **Step 3: Add the `until` helper to the existing FuncMap**

The mockup uses `{{range until 6}}...{{end}}` and `{{index (list "60" "250" ...) $i}}` to render N copies of a band element. Task 5 already added `list`; this task adds `until`. Edit `internal/chassis/templates.go`: inside the existing `templateFuncs` map, append `until` immediately before the closing brace. Find the existing map:

```go
var templateFuncs = template.FuncMap{
	"inc":        func(i int) int { return i + 1 },
	"replaceAll": strings.ReplaceAll,
	"hasString": func(haystack []string, needle string) bool {
		for _, s := range haystack {
			if s == needle {
				return true
			}
		}
		return false
	},
	"pad2": func(n int) string {
		if n < 10 {
			return fmt.Sprintf("0%d", n)
		}
		return fmt.Sprintf("%d", n)
	},
	"dim": func(active bool) string {
		if active {
			return ""
		}
		return "dim"
	},
	"list": func(args ...string) []string { return args },
}
```

Replace it with:

```go
var templateFuncs = template.FuncMap{
	"inc":        func(i int) int { return i + 1 },
	"replaceAll": strings.ReplaceAll,
	"hasString": func(haystack []string, needle string) bool {
		for _, s := range haystack {
			if s == needle {
				return true
			}
		}
		return false
	},
	"pad2": func(n int) string {
		if n < 10 {
			return fmt.Sprintf("0%d", n)
		}
		return fmt.Sprintf("%d", n)
	},
	"dim": func(active bool) string {
		if active {
			return ""
		}
		return "dim"
	},
	"list":  func(args ...string) []string { return args },
	"until": func(n int) []struct{} { return make([]struct{}, n) },
}
```

`index` is already a built-in template function. No need to add it.

- [ ] **Step 4: Update the helpers-available test to cover `until`**

In `internal/chassis/chassis_test.go`, append one more probe to `TestTemplatesExpectedHelpersAvailable` (`list` is already covered by the `hasString` probe from Task 5):

```go
		{"until", `{{range until 3}}x{{end}}`},
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/chassis/...`
Expected: PASS — meter renders, all sentinel markers present, helpers work.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/templates/meter.html internal/chassis/templates.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): port 3-row meter screen partial

Renders the source strip (AUDIO IN/OUT, SRC, CROP, HLS BUF, DROPS),
mid row (spectrum bars, goniometer, BITRATE/kHz/MODE stats, NTSC/PAL
lamps, ODD/EVEN/LOCK field-flip, throughput + ACK scopes), and
readout (L/R VU, PHASE, LUFS, OUTPUT, ASPECT, PIPE/SPEED/LINK). Adds
'until' and 'list' template helpers for the repeated band/segment
elements.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 12: Port transport + visualizer-bank Partials

**Files:**
- Modify: `internal/chassis/templates/transport.html`
- Modify: `internal/chassis/templates/visualizer-bank.html`

- [ ] **Step 1: Port transport.html**

The mockup's transport row is around line 3740-3760. The partial receives `.Transport`.

Replace `internal/chassis/templates/transport.html`:

```html
{{define "transport"}}
<!-- chassis:transport -->
<div class="transport-strip">
  <span class="strip-label">Transport</span>
  <div class="transport-row">
    <button class="trn" title="Previous">⏮</button>
    <button class="trn" title="Next">⏭</button>
    <button class="trn primary" title="Pause / Resume">⏸</button>
    <button class="trn" title="Stop">⏹</button>
    <button class="trn" title="Replay">⟲</button>
  </div>
  <div class="seek-bar" title="Cast position">
    <div class="fill" style="width: {{.SeekFillPercent}}%"></div>
    <div class="head"><span class="grip"></span></div>
  </div>
  <div class="seek-time">
    <span class="seg-display" data-ghost="88:88">{{.ElapsedTime}}</span>
    <span class="sep">/</span>
    <span class="total seg-display" data-ghost="88:88">{{.TotalTime}}</span>
    <span class="pct" title="Playback position">{{.PercentPlayed}}</span>
  </div>
  <button class="gear-btn" id="gear-btn">⚙ Setup</button>
</div>
{{end}}
```

- [ ] **Step 2: Port visualizer-bank.html**

The visualizer bank lives around line 3770 in the mockup. The partial receives `.Visualizer`.

Replace `internal/chassis/templates/visualizer-bank.html`:

```html
{{define "visualizer-bank"}}
<!-- chassis:visualizer-bank -->
<div class="section-strip viz-section">
  <span class="strip-label viz-strip-label">CRT<br>Visualizer</span>
  <div class="viz-bank" role="radiogroup" aria-label="CRT visualizer mode">
    {{range .Buttons}}
    {{if .IsPreview}}
    <button class="hw-btn viz-btn viz-btn--preview" type="button" role="radio" aria-checked="false" aria-disabled="true" data-viz="{{.Mode}}" title="{{.Label}} (preview — deferred from v1)">
      <span class="viz-icon viz-icon--{{.IconKind}}" aria-hidden="true"></span>
      <span class="viz-label">{{.Label}}</span>
      <span class="viz-badge">Preview</span>
    </button>
    {{else}}
    <button class="hw-btn viz-btn{{if eq .Mode $.ActiveMode}} active lit{{end}}" type="button" role="radio" aria-checked="{{if eq .Mode $.ActiveMode}}true{{else}}false{{end}}" data-viz="{{.Mode}}" title="{{.Label}}">
      <span class="viz-icon viz-icon--{{.IconKind}}" aria-hidden="true"></span>
      <span class="viz-label">{{.Label}}</span>
    </button>
    {{end}}
    {{end}}
  </div>
</div>
{{end}}
```

The viz icons are pure-CSS glyphs (analyzer bars, sine wave, crosshair, radial spokes) styled in `chassis.css` — the empty `viz-icon` spans here are the hooks.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/templates/transport.html internal/chassis/templates/visualizer-bank.html
git commit -m "feat(chassis): port transport + visualizer-bank partials

Transport: 5 buttons + seek bar (driven by .SeekFillPercent) + time
readout + Setup gear. Visualizer bank: 4 buttons (3 live + 1 preview)
iterating .Visualizer.Buttons, with active+lit set for the mode
matching .Visualizer.ActiveMode.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 13: Port input-row + preset-bank Partials

**Files:**
- Modify: `internal/chassis/templates/input-row.html`
- Modify: `internal/chassis/templates/preset-bank.html`

- [ ] **Step 1: Port input-row.html**

Mockup around line 3810-3830. Partial receives `.Input`.

Replace `internal/chassis/templates/input-row.html`:

```html
{{define "input-row"}}
<!-- chassis:input-row -->
<div class="section-strip input-section">
  <span class="strip-label">Input</span>
  <div style="display:flex; gap:6px;">
    <label class="input-panel" style="flex:1" for="paste-input">
      <span class="glyph">▸</span>
      <input class="paste-input" id="paste-input" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" placeholder="{{.PastePlaceholder}}">
      <button class="paste-clear" id="paste-clear" type="button" aria-label="Clear input" style="display:none;">✕</button>
      <span class="chip" id="paste-chip">{{.DetectedKind}}</span>
    </label>
    <button class="cast-btn{{if not .CastEnabled}} disabled{{end}}" id="cast-btn" type="button"{{if not .CastEnabled}} disabled{{end}}>CAST</button>
    <button class="upload-btn" id="upload-btn" type="button">↑ .TORRENT</button>
    <input type="file" id="torrent-file-input" accept=".torrent,application/x-bittorrent" style="display:none;">
  </div>
</div>
{{end}}
```

- [ ] **Step 2: Port preset-bank.html**

Mockup around line 3855-3920. Partial receives `.Presets`.

Replace `internal/chassis/templates/preset-bank.html`:

```html
{{define "preset-bank"}}
<!-- chassis:preset-bank -->
<div class="preset-strip preset-section">
  <span class="strip-label">Presets</span>
  <div>
    <div class="preset-header">
      <span class="title" id="preset-mode-label">{{.ModeLabel}}</span>
      <span class="count" id="preset-count">{{.Count}}</span>
    </div>
    <div class="preset-bank">
      {{range $i, $slot := .Slots}}
      <button class="preset{{if not $slot.Filled}} empty{{end}}">
        <div class="num">{{pad2 (inc $i)}}</div>
        {{if $slot.Filled}}
        <div class="name">{{$slot.Title}}</div>
        <div class="badge">{{$slot.Subtitle}}</div>
        {{end}}
      </button>
      {{end}}
    </div>
  </div>
</div>
{{end}}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/templates/input-row.html internal/chassis/templates/preset-bank.html
git commit -m "feat(chassis): port input-row + preset-bank partials

Input row: paste field + URL/magnet detection chip + CAST + .TORRENT
button. Preset bank: header + 12-slot grid, each slot 2-digit numbered
via pad2(inc $i); filled slots show title + subtitle, empty slots
just show the number placeholder.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 14: Port history + settings-drawer Partials

**Files:**
- Modify: `internal/chassis/templates/history.html`
- Modify: `internal/chassis/templates/settings-drawer.html`

- [ ] **Step 1: Port history.html**

History row around line 3980-4030 in the mockup. Partial receives `.History`.

Replace `internal/chassis/templates/history.html`:

```html
{{define "history"}}
<!-- chassis:history -->
<div class="history-section section-strip">
  <span class="strip-label">History</span>
  <div>
    {{if .Rows}}
      {{range .Rows}}
      <div class="history-row">
        <div class="artwork"></div>
        <div class="title">{{.Title}}</div>
        <div class="source">{{.Source}}</div>
        <div class="when">{{.When}}</div>
      </div>
      {{end}}
    {{else}}
      <div class="history-empty">{{.EmptyMessage}}</div>
    {{end}}
  </div>
</div>
{{end}}
```

- [ ] **Step 2: Port settings-drawer.html**

Settings drawer around line 4050+ in the mockup. The drawer is closed in Phase 0; structure exists but CSS keeps it hidden.

Replace `internal/chassis/templates/settings-drawer.html`:

```html
{{define "settings-drawer"}}
<!-- chassis:settings-drawer -->
<div class="settings-panel{{if .Open}} open{{end}}">
  <div class="settings-pane single-col active">
    <!-- Drawer content land in Spec 8 (settings & adapters port). -->
  </div>
</div>
{{end}}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/chassis/...`
Expected: PASS — all 12 partial sentinels present in rendered body.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/templates/history.html internal/chassis/templates/settings-drawer.html
git commit -m "feat(chassis): port history + settings-drawer partials

History row: ranges over .Rows; renders 'No recent casts' empty state
in idle. Settings drawer: structurally present but closed by default
(Spec 8 wires open/close + ports the real form content).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 15: Copy Fonts + LICENSE + SOURCES.md

**Files:**
- Create: `internal/chassis/static/fonts/DSEG7Classic-Regular.woff2`
- Create: `internal/chassis/static/fonts/DSEG7Classic-Bold.woff2`
- Create: `internal/chassis/static/fonts/DSEG7Modern-Regular.woff2`
- Create: `internal/chassis/static/fonts/DSEG7Modern-Bold.woff2`
- Create: `internal/chassis/static/fonts/DSEG14Classic-Regular.woff2`
- Create: `internal/chassis/static/fonts/DSEG14Classic-Bold.woff2`
- Create: `internal/chassis/static/fonts/DSEG14Modern-Regular.woff2`
- Create: `internal/chassis/static/fonts/DSEG14Modern-Bold.woff2`
- Create: `internal/chassis/static/fonts/Inter-Variable.woff2`
- Create: `internal/chassis/static/fonts/LICENSE`
- Create: `internal/chassis/static/fonts/SOURCES.md`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Add the failing test for font serving + MIME**

Append to `internal/chassis/chassis_test.go`:

```go
func TestHandleStatic_Fonts_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	cases := []string{
		"DSEG7Classic-Regular.woff2",
		"DSEG7Classic-Bold.woff2",
		"DSEG7Modern-Regular.woff2",
		"DSEG7Modern-Bold.woff2",
		"DSEG14Classic-Regular.woff2",
		"DSEG14Classic-Bold.woff2",
		"DSEG14Modern-Regular.woff2",
		"DSEG14Modern-Bold.woff2",
		"Inter-Variable.woff2",
	}
	for _, name := range cases {
		req := httptest.NewRequest(http.MethodGet, "/receiver/static/fonts/"+name, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("font %s status = %d, want 200", name, rec.Code)
			continue
		}
		ct := rec.Header().Get("Content-Type")
		if ct != "font/woff2" {
			t.Errorf("font %s Content-Type = %q, want font/woff2", name, ct)
		}
	}
}

func TestHandleStatic_LICENSE_Served(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/fonts/LICENSE", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("LICENSE status = %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestHandleStatic_Fonts|TestHandleStatic_LICENSE'`
Expected: FAIL — files don't exist; build may fail because go:embed cannot find files.

- [ ] **Step 3: Copy DSEG fonts from the brainstorm directory**

The 8 DSEG woff2 files already exist in `.superpowers/brainstorm/1973-1779237107/`. Copy them to `internal/chassis/static/fonts/`:

```bash
mkdir -p internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG7Classic-Regular.woff2 internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG7Classic-Bold.woff2    internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG7Modern-Regular.woff2  internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG7Modern-Bold.woff2     internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG14Classic-Regular.woff2 internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG14Classic-Bold.woff2    internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG14Modern-Regular.woff2  internal/chassis/static/fonts/
cp .superpowers/brainstorm/1973-1779237107/DSEG14Modern-Bold.woff2     internal/chassis/static/fonts/
```

PowerShell equivalent for the default Codex workspace on Windows:

```powershell
New-Item -ItemType Directory -Force -Path internal/chassis/static/fonts | Out-Null
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG7Classic-Regular.woff2 internal/chassis/static/fonts/
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG7Classic-Bold.woff2 internal/chassis/static/fonts/
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG7Modern-Regular.woff2 internal/chassis/static/fonts/
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG7Modern-Bold.woff2 internal/chassis/static/fonts/
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG14Classic-Regular.woff2 internal/chassis/static/fonts/
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG14Classic-Bold.woff2 internal/chassis/static/fonts/
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG14Modern-Regular.woff2 internal/chassis/static/fonts/
Copy-Item .superpowers/brainstorm/1973-1779237107/DSEG14Modern-Bold.woff2 internal/chassis/static/fonts/
```

- [ ] **Step 4: Download the pinned Inter variable font**

Download the Inter variable font from the pinned upstream release declared in `SOURCES.md`: `rsms/inter` `v4.0`. Place it at `internal/chassis/static/fonts/Inter-Variable.woff2`. If this URL changes in a future release, update the download URL, release field, and checksum in `SOURCES.md` in the same commit.

```bash
curl -L -o internal/chassis/static/fonts/Inter-Variable.woff2 \
  "https://github.com/rsms/inter/releases/download/v4.0/InterVariable.woff2"
```

PowerShell equivalent:

```powershell
Invoke-WebRequest -Uri "https://github.com/rsms/inter/releases/download/v4.0/InterVariable.woff2" -OutFile internal/chassis/static/fonts/Inter-Variable.woff2
```

- [ ] **Step 5: Compute SHA-256 checksums**

```bash
cd internal/chassis/static/fonts/
sha256sum *.woff2 > /tmp/font-checksums.txt
cat /tmp/font-checksums.txt
```

PowerShell equivalent:

```powershell
Get-ChildItem internal/chassis/static/fonts -Filter *.woff2 |
  Sort-Object Name |
  ForEach-Object {
    "{0}  {1}" -f (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant(), $_.Name
  } | Set-Content -Encoding ascii font-checksums.txt
Get-Content font-checksums.txt
```

Keep the checksums; you'll paste them into `SOURCES.md` in the next step.

- [ ] **Step 6: Create LICENSE file**

Create `internal/chassis/static/fonts/LICENSE`:

```text
================================================================
DSEG Font License (MIT)
================================================================
Copyright (c) 2017 Keshikan

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

Project: https://github.com/keshikan/DSEG

================================================================
Inter Font License (SIL Open Font License 1.1)
================================================================
Copyright (c) 2016 The Inter Project Authors.

This Font Software is licensed under the SIL Open Font License, Version 1.1.

The full text of the OFL is available at:
https://openfontlicense.org/

Inter is published under the SIL Open Font License (OFL), version 1.1. This
license permits unrestricted use, modification, and redistribution of Inter
provided that the OFL conditions are met (in particular, attribution is
preserved and modified versions are not sold as standalone software).

Project: https://github.com/rsms/inter
```

- [ ] **Step 7: Create SOURCES.md**

Create `internal/chassis/static/fonts/SOURCES.md`. Paste the SHA-256 values from Step 5 into the Checksum column:

````markdown
# Chassis Font Sources

Reproducibility manifest for the woff2 files committed alongside
`internal/chassis/`. Update both file and checksum when bumping
to a new upstream release.

## DSEG (8 files)

Source: https://github.com/keshikan/DSEG
Release: v0.46
License: MIT (see LICENSE)
Download URL: https://github.com/keshikan/DSEG/releases/download/v0.46/fonts-DSEG_v046.zip

| Local filename | SHA-256 |
|---|---|
| DSEG7Classic-Regular.woff2 | <PASTE-SHA-256-HERE> |
| DSEG7Classic-Bold.woff2 | <PASTE-SHA-256-HERE> |
| DSEG7Modern-Regular.woff2 | <PASTE-SHA-256-HERE> |
| DSEG7Modern-Bold.woff2 | <PASTE-SHA-256-HERE> |
| DSEG14Classic-Regular.woff2 | <PASTE-SHA-256-HERE> |
| DSEG14Classic-Bold.woff2 | <PASTE-SHA-256-HERE> |
| DSEG14Modern-Regular.woff2 | <PASTE-SHA-256-HERE> |
| DSEG14Modern-Bold.woff2 | <PASTE-SHA-256-HERE> |

## Inter

Source: https://github.com/rsms/inter
Release: v4.0
License: SIL Open Font License 1.1 (see LICENSE)
Download URL: https://github.com/rsms/inter/releases/download/v4.0/InterVariable.woff2

| Local filename | SHA-256 |
|---|---|
| Inter-Variable.woff2 | <PASTE-SHA-256-HERE> |

## Verification

```bash
cd internal/chassis/static/fonts/
sha256sum -c <(grep -E '\| \S+\.woff2 \|' SOURCES.md | awk -F'[ |]+' '{ print $4 "  " $3 }')
```

PowerShell verification:

```powershell
$expected = @{}
Get-Content internal/chassis/static/fonts/SOURCES.md |
  Where-Object { $_ -match '^\| .*\.woff2 \|' } |
  ForEach-Object {
    $parts = $_ -split '\|'
    $expected[$parts[1].Trim()] = $parts[2].Trim().ToLowerInvariant()
  }
foreach ($name in $expected.Keys) {
  $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath "internal/chassis/static/fonts/$name").Hash.ToLowerInvariant()
  if ($actual -ne $expected[$name]) { throw "$name checksum mismatch: $actual != $($expected[$name])" }
}
```
````

Replace each `<PASTE-SHA-256-HERE>` with the actual checksums from `/tmp/font-checksums.txt`.
On PowerShell, use the `font-checksums.txt` file produced in the workspace by the Step 5 command.

- [ ] **Step 8: Verify embed picks up the new files**

Run: `go test ./internal/chassis/ -run 'TestHandleStatic_Fonts|TestHandleStatic_LICENSE'`
Expected: PASS — all font + LICENSE requests return 200 with `font/woff2` (fonts) or 200 + reasonable content type (LICENSE).

- [ ] **Step 9: Commit**

```bash
git add internal/chassis/static/fonts/
git commit -m "feat(chassis): add DSEG + Inter fonts + LICENSE + SOURCES.md

8 DSEG woff2 files (Classic + Modern, Regular + Bold) + Inter variable
font, all served at /receiver/static/fonts/ via go:embed. LICENSE
collects DSEG MIT + Inter SIL OFL attributions; SOURCES.md records
upstream release tags, download URLs, and SHA-256 checksums for
reproducibility.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 16: chassis.css Scaffold (Tokens + @font-face + Reset)

**Files:**
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/chassis_test.go`

Replace the stub with the foundational CSS — design tokens (verbatim from mockup), all `@font-face` declarations, and the custom reset. Per-section CSS lands in Tasks 17-24.

- [ ] **Step 1: Replace `chassis.css` with foundation content**

Replace the entire contents of `internal/chassis/static/chassis.css` with:

```css
/* ── 1. @font-face declarations ───────────────────────────────────── */
@font-face { font-family: 'DSEG7-Classic';  src: url('/receiver/static/fonts/DSEG7Classic-Regular.woff2?v={{.Version}}') format('woff2'); font-weight: 400; font-display: block; }
@font-face { font-family: 'DSEG7-Classic';  src: url('/receiver/static/fonts/DSEG7Classic-Bold.woff2?v={{.Version}}')    format('woff2'); font-weight: 700; font-display: block; }
@font-face { font-family: 'DSEG14-Classic'; src: url('/receiver/static/fonts/DSEG14Classic-Regular.woff2?v={{.Version}}') format('woff2'); font-weight: 400; font-display: block; }
@font-face { font-family: 'DSEG14-Classic'; src: url('/receiver/static/fonts/DSEG14Classic-Bold.woff2?v={{.Version}}')   format('woff2'); font-weight: 700; font-display: block; }
@font-face { font-family: 'DSEG7-Modern';   src: url('/receiver/static/fonts/DSEG7Modern-Regular.woff2?v={{.Version}}') format('woff2'); font-weight: 400; font-display: block; }
@font-face { font-family: 'DSEG7-Modern';   src: url('/receiver/static/fonts/DSEG7Modern-Bold.woff2?v={{.Version}}')    format('woff2'); font-weight: 700; font-display: block; }
@font-face { font-family: 'DSEG14-Modern';  src: url('/receiver/static/fonts/DSEG14Modern-Regular.woff2?v={{.Version}}') format('woff2'); font-weight: 400; font-display: block; }
@font-face { font-family: 'DSEG14-Modern';  src: url('/receiver/static/fonts/DSEG14Modern-Bold.woff2?v={{.Version}}')    format('woff2'); font-weight: 700; font-display: block; }
@font-face { font-family: 'Inter'; src: url('/receiver/static/fonts/Inter-Variable.woff2?v={{.Version}}') format('woff2-variations'); font-weight: 100 900; font-style: normal; font-display: swap; }

/* ── 2. :root design tokens ──────────────────────────────────────── */
/* Ported verbatim from receiver-v24.html lines 21-30. Do not paraphrase. */
:root {
  --vfd:           oklch(0.92 0.20 175);
  --vfd-bright:    oklch(0.96 0.18 175);
  --vfd-glow:      oklch(0.85 0.22 175 / 0.75);
  --vfd-glow-soft: oklch(0.85 0.22 175 / 0.32);
  --vfd-dim:       oklch(0.55 0.16 175 / 0.78);
  --vfd-faded:     oklch(0.55 0.16 175 / 0.35);
  --vfd-ghost:     oklch(0.42 0.12 175 / 0.18);
  --lock-amber:    oklch(0.82 0.18 75);
}

/* ── 3. Reset and base typography ────────────────────────────────── */
body.receiver, body.receiver * { box-sizing: border-box; }
body.receiver { margin: 0; padding: 0; background: #0a0a0b; color: #c0c0c4; font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; font-size: 14px; line-height: 1.4; }
body.receiver h1, body.receiver h2, body.receiver h3, body.receiver h4, body.receiver h5, body.receiver h6 { margin: 0; }
body.receiver ::-webkit-scrollbar { width: 10px; height: 10px; }
body.receiver ::-webkit-scrollbar-track { background: #0a0a0b; }
body.receiver ::-webkit-scrollbar-thumb { background: #2a2a2e; border-radius: 5px; }
body.receiver ::-webkit-scrollbar-thumb:hover { background: #3a3a3e; }

/* ── 4-13. Component CSS land in Tasks 17-23 (banner-commented) ─────── */
/* ── 14. Idle / live state overrides land in Task 24 ────────────────── */
/* ── 15. Responsive container queries land in Task 25 ───────────────── */
```

- [ ] **Step 2: Add a hosted-font audit test**

Add this audit test to `internal/chassis/chassis_test.go` so the CSS port cannot keep mockup-only or externally hosted font references:

```go
func TestChassisCSS_UsesOnlyHostedFontFamilies(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	text := string(src)
	for _, banned := range []string{
		"fonts.googleapis.com",
		"fonts.gstatic.com",
		"Orbitron",
		"Major Mono",
		"JetBrains",
	} {
		if strings.Contains(text, banned) {
			t.Fatalf("chassis.css contains unhosted/mockup-only font reference %q", banned)
		}
	}
}
```

`internal/chassis/chassis_test.go` should already import `strings` from earlier template tests; if not, merge it into the existing import block.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/chassis/...`
Expected: PASS — CSS still preprocesses, all font URLs carry `?v=`, no template error, and no external/unhosted font references remain.

- [ ] **Step 4: Verify the page renders in a browser**

Build and run the bridge with a manual exec:

```bash
go build -o bin/mister-groovy-relay ./cmd/mister-groovy-relay
./bin/mister-groovy-relay --config /path/to/config.toml &
```

PowerShell equivalent:

```powershell
go build -o bin\mister-groovy-relay.exe .\cmd\mister-groovy-relay
Start-Process -FilePath .\bin\mister-groovy-relay.exe -ArgumentList "--config", "C:\path\to\config.toml" -WindowStyle Hidden
```

Visit `http://localhost:32500/receiver` in a browser. Expected: a black page (no per-section CSS yet) with no console errors. Tabs/title show `GROOVY · RELAY`.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/chassis.css internal/chassis/chassis_test.go
git commit -m "feat(chassis): add chassis.css scaffold (fonts + tokens + reset)

8 DSEG @font-face declarations with font-display: block (matching
mockup — segmented-display digits must not flash a fallback). Inter
variable font with font-display: swap. OKLCH design tokens ported
verbatim from receiver-v24.html lines 21-30. Custom reset scoped
under body.receiver. Per-section component CSS land in Tasks 17-25.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 17: Port Chassis Shell + Status Bar + Masthead CSS

**Files:**
- Modify: `internal/chassis/static/chassis.css`

For Tasks 17-24, the work is **mechanical CSS porting** from the brainstorm mockup. The implementer locates each rule by **selector grep** (not by mockup line number — the mockup is still iterating, line numbers drift). Copy each rule, apply the scope-prefix transformation (rules from the spec's "Scope isolation" section):

1. Leave `@font-face`, `@container`, `@keyframes`, `@media`, comments untouched.
2. `body.idle ...` → `body.receiver.idle ...` and similar for `body.live`, `body:not(.idle)`, `body.settings-open`, `body.browse-open`, `body.catalog-scanning`, `body[data-event-filter=...]`.
3. Scope global element selectors under `body.receiver`.
4. Prepend `body.receiver` to every remaining top-level class/id/attribute selector.
5. Inside at-rules, apply rules 2-4 to nested selectors.

Each task ends with the scope assertion test (Task 25) being green for the chunks ported so far. Use the following grep recipe to find rules cleanly:

```bash
grep -nE '^\s*\.<selector>\s*[\{,]' .superpowers/brainstorm/1973-1779237107/receiver-v24.html
```

PowerShell / ripgrep equivalent:

```powershell
rg -n '^\s*\.<selector>\s*[\{,]' .superpowers/brainstorm/1973-1779237107/receiver-v24.html
```

The brace at end of line means "rule definition"; the comma means "comma-separated multi-selector rule list" — both yield the rule starts.

- [ ] **Step 1: Locate the chassis-shell + status bar + masthead CSS in the mockup**

**Important:** the chassis frame in the mockup uses the class `.receiver` (not `.chassis-shell` as some early plan drafts erroneously named it). The mockup has both an outer `<body class="receiver ...">` AND an inner `<div class="receiver">` chassis frame — the spec's scope strategy uses the body class as the scope gate while the inner div keeps its `.receiver` class so existing mockup selectors port mechanically.

Selectors to grep for in `receiver-v24.html`:

- `.receiver` (the inner chassis-frame element — `^\s*\.receiver\s*\{`)
- `.screw` (decorative corner screws — `^\s*\.screw`)
- `.status-bar` and its descendants (`^\s*\.status-bar`)
- `.brand-plate` (`^\s*\.brand-plate`)
- `.masthead` (`^\s*\.masthead`)
- `.led`, `.led-row`, `.load-core-btn` (status-bar lamps and core-loader button)

- [ ] **Step 2: Port the CSS, applying the scope-prefix transformation**

Append to `internal/chassis/static/chassis.css` (after the existing section 3 reset, before the section banners):

```css
/* ── 4. Chassis shell + screws + chrome ──────────────────────────── */
/* Mockup source: search receiver-v24.html for ^\s*\.receiver\s*\{
   and the .screw.{tl,tr,bl,br} rules nearby */
body.receiver .receiver {
  /* outer chassis frame — brushed aluminium gradient + inset bezel */
  position: relative;
  margin: 32px auto;
  max-width: 1400px;
  padding: 24px 28px 28px;
  background: linear-gradient(180deg, #2a2a2e 0%, #1a1a1c 100%);
  border: 1px solid #0a0a0b;
  border-radius: 8px;
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.06),
    inset 0 -1px 0 rgba(0, 0, 0, 0.5),
    0 8px 24px rgba(0, 0, 0, 0.65);
}
body.receiver .receiver-inner {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 14px;
}
/* ...port the rest of .receiver chrome rules + .screw.tl/.tr/.bl/.br positioning rules from the mockup here... */

/* ── 5. Status bar + masthead ────────────────────────────────────── */
/* Mockup source: grep for .status-bar, .brand-plate, .masthead, .led,
   .led-row, .load-core-btn rules. */
body.receiver .status-bar {
  display: grid;
  grid-template-columns: auto 1fr auto auto;
  gap: 18px;
  align-items: center;
  padding: 6px 10px 10px;
  border-bottom: 1px solid #0a0a0b;
  box-shadow: 0 1px 0 rgba(255, 255, 255, 0.04);
}
/* ...port .brand-plate, .led, .led-row, .load-core-btn rules from the mockup, prepending body.receiver to each selector... */

body.receiver .masthead {
  /* masthead between status-bar and the VFD row */
  padding: 4px 12px;
  border-bottom: 1px solid #0a0a0b;
}
body.receiver .masthead .name {
  font: 700 14px 'Inter', sans-serif;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: #c0c0c4;
}
```

The skeleton above is the structure; the implementer copies the full rules from the mockup. Replace the `/* ...port the rest... */` placeholders with the actual ported CSS.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 4: Manual visual check at 1920px**

Open `http://localhost:32500/receiver` (with the bridge running from Task 16's manual exec). Expected: chassis frame with rounded corners + brushed gradient is now visible; status bar + masthead render at the top. VFD/meter/transport/etc. are still empty stubs.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port chassis chrome + status bar + masthead CSS

Sections 4-5 of chassis.css. All selectors scoped under body.receiver
per the spec's scope-isolation rules. Visual at 1920px: chassis frame
with bezel + brushed-aluminium gradient + corner screws, status bar
with LED row + load-core button, masthead band with brand name.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 18: Port VFD + Source Cluster CSS

**Files:**
- Modify: `internal/chassis/static/chassis.css`

- [ ] **Step 1: Identify the VFD + source cluster CSS in the mockup**

Selectors to grep for in `receiver-v24.html`:

- VFD container + states: `.vfd`, `.screen.vfd`, `.vfd-state`, `.vfd-state--idle`, `.vfd-state--live`
- VFD content: `.title-line`, `.marquee-line`, `.right-panel`, `.big-time`, `.queue-stack`, `.freq`
- Segmented-display effect: `.seg-display`, `.seg-display::before`
- Source cluster + buttons: `.vfd-source-row`, `.source-cluster`, `.hw-btn` (including `.hw-btn::before`, `.hw-btn.active`, `.hw-btn.lit`)

Use `grep -nE '^\s*\.(vfd|seg-display|hw-btn|source-cluster|vfd-source-row)' receiver-v24.html` to locate them.

- [ ] **Step 2: Port both sections to chassis.css**

Append to `internal/chassis/static/chassis.css`:

```css
/* ── 6. VFD + source cluster ─────────────────────────────────────── */
/* Mockup source: grep for .vfd, .seg-display, .hw-btn, .source-cluster
   selectors as listed above. */

body.receiver .vfd-source-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 14px;
  align-items: stretch;
}
body.receiver .screen-frame { /* inset frame around the VFD/meter screens */ }
body.receiver .screen.vfd { /* the VFD itself — phosphor-tint background, inset shadow */ }
body.receiver .vfd-state--idle { /* idle layout */ }
body.receiver .vfd-state--live { /* live layout */ }
body.receiver .vfd .title-line { font-family: 'DSEG14-Classic', monospace; /* etc. */ }
body.receiver .vfd .marquee-line { /* scrolling marquee under the title */ }
body.receiver .vfd .right-panel { /* clock + uptime panel */ }
body.receiver .vfd .big-time { font-family: 'DSEG7-Classic', monospace; /* etc. */ }
body.receiver .seg-display { /* segmented display "ghost segment behind text" effect */ }
body.receiver .seg-display::before { content: attr(data-ghost); /* etc. */ }

body.receiver .source-cluster {
  display: grid;
  grid-template-columns: repeat(4, minmax(108px, 1fr));
  gap: 6px;
  align-items: stretch;
}
body.receiver .source-cluster .hw-btn { /* hardware-button look */ }
body.receiver .source-cluster .hw-btn.active { /* lit-tab look */ }
body.receiver .source-cluster .hw-btn.lit::before { /* green LED dot */ }
```

Replace placeholders with the full rules ported from the mockup.

- [ ] **Step 3: Run tests + manual visual check**

Run: `go test ./internal/chassis/...`
Expected: PASS.

Browser check at `/receiver`: VFD should now show `STANDBY` in segmented font + the marquee + the system time. Source cluster shows 4 hardware buttons with STREAMS active.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port VFD + source cluster CSS

Section 6 of chassis.css. VFD renders STANDBY title in DSEG14, marquee
scrolls, right panel shows segmented system time + uptime + queue.
Source cluster shows 4 chunky buttons with active/lit states. All
selectors scoped under body.receiver.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 19: Port Meter Screen CSS

**Files:**
- Modify: `internal/chassis/static/chassis.css`

Section 7 is the largest section in the stylesheet (the meter screen plus the spectrum, goniometer, throughput, ACK, audio scope cluster, NTSC/PAL lamps, field-flip, and per-row 3-region grid).

- [ ] **Step 1: Identify meter CSS in the mockup**

Selectors to grep for in `receiver-v24.html`:

- Outer: `.meter-screen`, `.meter-screen--compact`, `.meter-source-row`, `.meter-screen-frame`
- Per-row layout: `.meter-row`, `.meter-source-strip`, `.meter-mid-row`, `.meter-readout-line`
- Group containers: `.audio-grp`, `.video-grp`, `.net-grp`, `.grp`, `.codec-grp`, `.speed-grp`, `.link-grp`
- Spectrum: `.spectrum`, `.spectrum-band`, `.peak-dot`
- Goniometer: `.gonio`, `.gonio-wrap`, `.gonio-lbl`
- Network scopes: `.throughput-wrap`, `.ack-wrap`, `.aux-scope`, `.aux-screen`, `.aux-lbl`
- Audio scope cluster: `.tr-vu`, `.vu-lr`, `.ch-bar`, `.ch-lbl`, `.vu-phase`, `.vu-lufs`, `.vu-sep`
- Status lamps + indicators: `.std-lamps`, `.std-ind`, `.field-flip`, `.field-flip .dot`, `.field-flip .row`, `.field-flip .lock`
- Stat columns: `.stat`, `.stat .v`, `.stat .k`

Use: `grep -nE '^\s*\.(meter-|audio-grp|video-grp|net-grp|spectrum|gonio|throughput-wrap|ack-wrap|tr-vu|vu-|std-|field-flip|stat[ .]|grp |aux-|peak-dot)' receiver-v24.html`

- [ ] **Step 2: Port section 7 to chassis.css**

Append a `/* ── 7. Meter screen (3 rows) ── */` banner to chassis.css and copy the entire meter section from the mockup, applying the scope-prefix transformation. This is the longest single porting task.

- [ ] **Step 3: Run tests + manual visual check at multiple breakpoints**

Run: `go test ./internal/chassis/...`
Expected: PASS.

Browser checks:
- 1920px: meter screen shows 3 rows with spectrum (6 bands × 8 segments), goniometer canvas placeholder, segmented numerals, throughput + ACK canvases.
- 1180px: throughput + ACK scopes disappear, MB/S + MS ACK stats remain.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port 3-row meter screen CSS

Section 7 of chassis.css — the largest single section. Spectrum bars
(6 bands × 8 segments + peak dots), goniometer scope, throughput +
ACK canvases, L/R VU + phase + LUFS audio scope, stat numerals,
NTSC/PAL lamps, ODD/EVEN/LOCK field-flip, per-row 3-region grid with
audio/video/net groups. All selectors scoped under body.receiver.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 20: Port Transport + Visualizer-Bank CSS

**Files:**
- Modify: `internal/chassis/static/chassis.css`

- [ ] **Step 1: Identify transport + visualizer-bank CSS in the mockup**

Selectors to grep for in `receiver-v24.html`:

- Transport: `.transport-strip`, `.transport-row`, `.trn`, `.seek-bar`, `.seek-bar .fill`, `.seek-bar .head`, `.seek-time`, `.gear-btn`
- Visualizer bank: `.viz-section`, `.viz-bank`, `.viz-btn`, `.viz-icon`, `.viz-icon--analyzer`, `.viz-icon--wave`, `.viz-icon--scope`, `.viz-icon--radial`, `.viz-label`, `.viz-badge`, `.viz-btn--preview`, `.viz-strip-label`

Use: `grep -nE '^\s*\.(transport-|trn[ .]|seek-|gear-btn|viz-)' receiver-v24.html`

- [ ] **Step 2: Port both sections to chassis.css**

Append:

```css
/* ── 8. Transport row + seek bar ─────────────────────────────────── */
/* Mockup source: grep for .transport-, .trn, .seek-*, .gear-btn */
body.receiver .transport-strip {
  display: grid;
  grid-template-columns: 80px auto 1fr auto auto;
  gap: 12px;
  align-items: center;
}
/* ...port .transport-row, .trn, .seek-bar, .seek-bar .fill, .seek-bar .head, .seek-time, .gear-btn... */

/* ── 9. Visualizer bank ──────────────────────────────────────────── */
/* Mockup source: grep for .viz-section, .viz-bank, .viz-btn, .viz-icon */
body.receiver .viz-section .viz-bank { display: flex; gap: 6px; flex-wrap: wrap; }
body.receiver .viz-bank .viz-btn { /* hardware-button look matching .hw-btn but with icon + label stack */ }
body.receiver .viz-bank .viz-btn .viz-icon { /* CSS icon container */ }
body.receiver .viz-icon--analyzer > span { /* analyzer bars */ }
body.receiver .viz-icon--wave svg { /* sine wave */ }
body.receiver .viz-icon--scope svg { /* crosshair + lissajous */ }
body.receiver .viz-icon--radial svg { /* polar spokes */ }
body.receiver .viz-bank .viz-btn--preview { opacity: 0.55; cursor: not-allowed; }
body.receiver .viz-bank .viz-btn--preview .viz-badge { /* amber PREVIEW tag */ }
```

Replace placeholders with full ported rules.

- [ ] **Step 3: Run tests + manual visual check**

Run: `go test ./internal/chassis/...`

Browser: transport row shows 5 buttons + seek bar with empty fill + `--:--/--:--` + ⚙ Setup. Visualizer bank shows 4 buttons with icons; ANALYZER is active+lit; RADIAL is dimmed with PREVIEW badge.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port transport + visualizer-bank CSS

Sections 8-9 of chassis.css. Transport row with chunky seek bar (40px
tall, tick marks, chrome thumb), 5 transport buttons, time readout,
Setup gear. Visualizer bank with 4 CSS-drawn icons (bars, wave,
crosshair, spokes); RADIAL slot rendered as dimmed PREVIEW.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 21: Port Input + Preset Bank + Catalog CSS

**Files:**
- Modify: `internal/chassis/static/chassis.css`

- [ ] **Step 1: Identify the CSS sections in the mockup**

Selectors to grep for in `receiver-v24.html`:

- Input row: `.input-section`, `.input-panel`, `.paste-input`, `.paste-clear`, `.cast-btn`, `.upload-btn`, `.chip`, `.glyph`
- Preset bank: `.preset-strip`, `.preset-section`, `.preset-header`, `.preset-bank`, `.preset`, `.preset .num`, `.preset .name`, `.preset .badge`
- Catalog (Phase 0 ships the CSS so Spec 3 drops markup in without re-styling): `.catalog-body`, `.catalog-grid`, `.catalog-tile`, `.catalog-header`, `.catalog-empty`
- Section strip generic: `.section-strip`, `.strip-label` (also used by Input/Preset/History rows)

Use: `grep -nE '^\s*\.(input-|paste-|cast-btn|upload-btn|chip|preset|catalog|section-strip|strip-label|glyph)' receiver-v24.html`

- [ ] **Step 2: Port to chassis.css**

Append `/* ── 10. Input row + cast / torrent buttons ── */`, `/* ── 11. Preset bank + catalog ── */` banners and the rules under each. Apply the scope-prefix transformation.

- [ ] **Step 3: Run tests + visual check**

Run: `go test ./internal/chassis/...`

Browser: input row shows paste field + CAST button (disabled in idle) + .TORRENT. Preset bank shows 12 empty numbered slots with header `Memory · 0 / 12 slots`.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port input row + preset bank + catalog CSS

Sections 10-11 of chassis.css. Input row: recessed paste field with
type-detection chip, primary CAST + secondary .TORRENT buttons. Preset
bank: 12-slot grid with header + ★ count; empty slots show numbered
placeholders, filled slots show name + badge. Catalog rules included
so Spec 3 can plug markup in without re-styling.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 22: Port History + Event Log + Settings Drawer CSS

**Files:**
- Modify: `internal/chassis/static/chassis.css`

- [ ] **Step 1: Identify CSS sections in the mockup**

Selectors to grep for in `receiver-v24.html`:

- History: `.history-section`, `.history-row`, `.history-empty`, `.artwork` (when inside `.history-row`), `.title`, `.source`, `.when`
- Event log: `.event-log-section`, `.event-log-row`, `.event-log-severity`, `.event-log-filter`
- Settings drawer: `.settings-panel`, `.settings-pane`, `.settings-pane.active`, `.settings-pane.single-col`, `.settings-row`, `.settings-field`, `.settings-tabs`, `.settings-body`

Use: `grep -nE '^\s*\.(history-|event-log-|settings-)' receiver-v24.html`

- [ ] **Step 2: Port to chassis.css**

Append `/* ── 12. History + event log ── */` and `/* ── 13. Settings drawer ── */` banners and the rules.

- [ ] **Step 3: Run tests + visual check**

Run: `go test ./internal/chassis/...`

Browser: history row shows `No recent casts` empty state. Settings drawer is hidden by default (Phase 0 closed).

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port history + event log + settings drawer CSS

Sections 12-13 of chassis.css. History row: recent casts list with
empty-state message. Event log: severity-filtered row vocabulary
(Spec 9 wires the live feed). Settings drawer: closed-by-default
recessed panel; Spec 8 ports the actual form content.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 23: Port Idle / Live State Overrides

**Files:**
- Modify: `internal/chassis/static/chassis.css`

The mockup uses ~50 `body.idle ...` rules to dim/disable elements in the idle state. This task ports them, applying the compound-rewrite rule (`body.idle` → `body.receiver.idle`).

- [ ] **Step 1: Identify body-state rules in the mockup**

Search for `body.idle`, `body.live`, `body:not(.idle)`, `body.settings-open`, `body.browse-open`, `body.catalog-scanning`, `body[data-event-filter`. Each one becomes its scoped form per the porting rules in the spec.

- [ ] **Step 2: Port all body-state rules with the compound rewrite**

Append a `/* ── 14. Idle / live state overrides ── */` banner. For each `body.<state> X` rule, write `body.receiver.<state> X`:

```css
/* ── 14. Idle / live state overrides ─────────────────────────────── */
/* Compound-rewrite of every body-rooted state selector in the mockup
   so /ui/* pages that ever set the same body class do not inherit
   chassis overrides. */

body.receiver.idle .meter-screen--compact .spectrum-band .seg { background: oklch(0.10 0.04 175); box-shadow: none; }
body.receiver.idle .meter-screen--compact .bargraph .seg { background: oklch(0.10 0.04 175); box-shadow: none; }
body.receiver.idle .vfd .title-line { color: var(--vfd-dim); }
body.receiver.idle .vfd .marquee-line { color: var(--vfd-faded); }
body.receiver.idle .source-cluster .hw-btn { color: var(--vfd-faded); }
body.receiver.idle .source-cluster .hw-btn::before { background: #1a1a1c; box-shadow: inset 0 1px 1px rgba(0,0,0,0.6); }
body.receiver.idle .source-cluster .hw-btn.active { /* idle-active look */ }
/* ...continue for all body.idle rules from the mockup... */

body.receiver:not(.idle) .field-flip .dot.odd { background: var(--vfd); }
/* ...continue for all body:not(.idle), body.settings-open, body.browse-open,
   body.catalog-scanning, body[data-event-filter=...] rules... */
```

- [ ] **Step 3: Run tests + visual check at idle vs live**

Run: `go test ./internal/chassis/...`

Browser: confirm the chassis at `/receiver` (idle by default) shows dimmed transport buttons, dim source labels, no active casting LED, blank meter scopes. Then test live state via `http://localhost:32500/receiver?dev=1` — once the JS toggle from Task 27 is in place, clicking the dev toggle should re-brighten the chassis. For this task, just confirm idle dimming looks right.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port idle / live state overrides

Section 14 of chassis.css. ~50 body.idle, body.live, body:not(.idle),
body.settings-open, body.browse-open, body.catalog-scanning, and
body[data-event-filter=...] rules from the mockup, all rewritten as
compound body.receiver.<state> selectors so future /ui/* pages that
ever set the same body class do not inherit chassis dimming.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 24: Port Responsive Container Queries

**Files:**
- Modify: `internal/chassis/static/chassis.css`

The mockup uses `@container` queries on the chassis frame (`.receiver`) and VFD elements. Container queries require `container-name` and `container-type` declarations on the named elements, which must be ported alongside the breakpoint rules — without them, the `@container` blocks silently never fire.

- [ ] **Step 1: Identify container query declarations + at-rules**

Use: `grep -nE 'container-name|container-type|@container' .superpowers/brainstorm/1973-1779237107/receiver-v24.html`. Container-name declarations live on `.receiver` (`container-name: chassis`) and `.vfd` (`container-name: vfd`). The breakpoints are:

- `chassis ≤ 1180px` — drop network scopes from meter row 2
- `chassis ≤ 900px` — drop goniometer; vfd-source-row collapses to single column
- `chassis ≤ 600px` — drop meter source-strip; hide field-flip
- `vfd ≤ 720px` — hide VFD right panel

- [ ] **Step 2: Port container declarations + breakpoint rules**

Append a `/* ── 15. Responsive (container queries) ── */` banner. Port the `container-name`/`container-type` declarations into the appropriate existing rules (likely `body.receiver .receiver` and `body.receiver .vfd`). Then add the breakpoint blocks:

```css
/* ── 15. Responsive (container queries) ──────────────────────────── */
body.receiver .receiver { container-name: chassis; container-type: inline-size; }
body.receiver .vfd { container-name: vfd; container-type: inline-size; }

@container chassis (max-width: 1180px) {
  body.receiver .meter-screen--compact .throughput-wrap,
  body.receiver .meter-screen--compact .ack-wrap { display: none; }
  /* ...rest of the 1180px rules from the mockup... */
}

@container chassis (max-width: 900px) {
  body.receiver .meter-screen--compact .gonio-wrap { display: none; }
  body.receiver .vfd-source-row { grid-template-columns: 1fr; }
  /* ...rest of the 900px rules... */
}

@container chassis (max-width: 600px) {
  body.receiver .meter-screen--compact .meter-source-strip { display: none; }
  body.receiver .field-flip { display: none; }
  /* ...rest of the 600px rules... */
}

@container vfd (max-width: 720px) {
  body.receiver .vfd .right-panel { display: none; }
}
```

Note: selectors inside `@container` rules still need the `body.receiver` prefix. Container queries decide when a rule applies; they do not scope the selector to the queried container. Apply the same scope-prefix rules inside `@container` / `@media` blocks as top-level CSS, while leaving `@font-face` and `@keyframes` internals exempt.

- [ ] **Step 3: Run tests + manual visual check at every breakpoint**

Run: `go test ./internal/chassis/...`

Browser resize checks:
- 1920px: full chassis
- 1180px: network scopes (throughput + ACK) disappear from meter row 2
- 900px: goniometer disappears; source-cluster falls below VFD
- 600px: meter source strip + field-flip hidden
- 720px chassis with VFD container narrower: VFD right panel (clock + queue) hides

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): port responsive container queries

Section 15 of chassis.css. container-name + container-type declarations
on .receiver (chassis) and .vfd elements so the @container rules
actually fire. Four breakpoints: chassis ≤ 1180/900/600px progressively
drop scopes/strips, vfd ≤ 720px hides the right panel. Visual
verified at all breakpoints in Chrome.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 25: CSS Scope Assertion Test

**Files:**
- Create: `internal/chassis/css_scope_test.go`
- Modify: `go.mod` / `go.sum` (add `github.com/tdewolff/parse/v2`)

- [ ] **Step 1: Add the `tdewolff/parse` dependency**

Run:

```bash
go get github.com/tdewolff/parse/v2/css@v2.8.12
go mod tidy
```

This adds the package to `go.mod` and downloads it.

- [ ] **Step 2: Write the failing scope-assertion test**

Create `internal/chassis/css_scope_test.go`:

```go
package chassis

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// TestChassisCSS_AllSelectorsScoped scans the embedded chassis.css and
// fails any element/class/id/attribute selector that is not either
// explicitly allowlisted (:root, @font-face, @keyframes, keyframe
// percentage selectors) or rooted at body.receiver. Catches both
// missed-prefix bugs during the initial port and accidental leaks
// added by later patches.
//
// The test also has explicit fixture assertions for the leak-prone
// mockup selectors so a regression that re-introduces them is caught
// with a precise message.
func TestChassisCSS_AllSelectorsScoped(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	leaks := findUnscopedSelectors(src)
	if len(leaks) > 0 {
		t.Errorf("found %d unscoped selectors in chassis.css:", len(leaks))
		for _, sel := range leaks {
			t.Errorf("  %s", sel)
		}
	}
}

func TestChassisCSS_LeakProneSelectorsAreScoped(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	bannedSubstrings := []string{
		"body.idle ",        // must be body.receiver.idle
		"body.live ",        // must be body.receiver.live
		"body:not(.idle)",   // must be body.receiver:not(.idle)
		"body.settings-open",
		"body.browse-open",
		"body.catalog-scanning",
		"body[data-event-filter",
	}
	for _, banned := range bannedSubstrings {
		if bytes.Contains(src, []byte(banned)) {
			// Only fail if the bare form appears without the receiver class.
			// "body.receiver.idle " is allowed; "body.idle " (no receiver) isn't.
			scopedForm := strings.Replace(banned, "body", "body.receiver", 1)
			lines := strings.Split(string(src), "\n")
			for i, line := range lines {
				if strings.Contains(line, banned) && !strings.Contains(line, scopedForm) {
					t.Errorf("line %d: leak-prone selector %q without body.receiver prefix: %s", i+1, banned, line)
				}
			}
		}
	}
}

// findUnscopedSelectors parses css and returns any selectors that are
// not allowlisted or scoped under body.receiver. Selectors inside
// @media and @container are still checked; those at-rules are
// conditional, not scoping boundaries. @font-face and @keyframes
// internals are exempt because they do not select page elements.
func findUnscopedSelectors(src []byte) []string {
	var leaks []string
	p := css.NewParser(parse.NewInput(bytes.NewReader(src)), false)
	skipAtRuleDepth := 0
	for {
		gt, _, data := p.Next()
		switch gt {
		case css.ErrorGrammar:
			return leaks
		case css.BeginAtRuleGrammar:
			at := strings.TrimSpace(string(data))
			if isSelectorExemptAtRule(at) || skipAtRuleDepth > 0 {
				skipAtRuleDepth++
			}
		case css.EndAtRuleGrammar:
			if skipAtRuleDepth > 0 {
				skipAtRuleDepth--
			}
		case css.BeginRulesetGrammar:
			if skipAtRuleDepth > 0 {
				continue
			}
			sel := strings.TrimSpace(string(data))
			// Split comma-separated selector lists before checking prefix.
			// Otherwise `body.receiver .ok, .leak {}` would incorrectly
			// pass because the full selector starts with body.receiver.
			for _, part := range strings.Split(sel, ",") {
				part = strings.TrimSpace(part)
				if part == "" || isAllowlistedSelector(part) || strings.HasPrefix(part, "body.receiver") {
					continue
				}
				leaks = append(leaks, part)
			}
		}
	}
}

func isSelectorExemptAtRule(at string) bool {
	at = strings.TrimPrefix(strings.TrimSpace(at), "@")
	return strings.HasPrefix(at, "font-face") || strings.HasPrefix(at, "keyframes")
}

func isAllowlistedSelector(sel string) bool {
	switch sel {
	case ":root":
		return true
	}
	// Keyframe percentage selectors like "0%", "50%", "100%", "from", "to"
	if sel == "from" || sel == "to" {
		return true
	}
	if len(sel) > 1 && sel[len(sel)-1] == '%' {
		return true
	}
	return false
}

// TestChassisCSS_RulesetCountSanity guards against an under-ported CSS
// file. The mockup contains roughly 700-850 CSS rulesets (rule blocks
// with selectors); the ported chassis.css should contain a comparable
// count after the body.receiver scope-prefix pass. Catches "implementer
// forgot to copy section 11" or "implementer dropped half a section
// while applying scope rules" mistakes that the scope-isolation test
// alone cannot detect (the scope test only flags rules that ARE present
// but unscoped, not rules that are MISSING).
//
// Tolerance is wide: the mockup is still iterating and small additions
// or removals shouldn't break the test. The threshold is a lower bound
// only; a higher count is always fine.
func TestChassisCSS_RulesetCountSanity(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("read chassis.css: %v", err)
	}
	count := countRulesets(src)
	const minRulesets = 600
	if count < minRulesets {
		t.Errorf("chassis.css has %d rulesets, want at least %d (the port from receiver-v24.html is incomplete)", count, minRulesets)
	}
}

func countRulesets(src []byte) int {
	p := css.NewParser(parse.NewInput(bytes.NewReader(src)), false)
	n := 0
	for {
		gt, _, _ := p.Next()
		if gt == css.ErrorGrammar {
			return n
		}
		if gt == css.BeginRulesetGrammar {
			n++
		}
	}
}
```

- [ ] **Step 3: Add fixture-based parser validation**

Before running the test against the real ~3,000-line `chassis.css`, validate that the parser correctly classifies known-good and known-bad selectors. Add the following test to `internal/chassis/css_scope_test.go`:

```go
func TestFindUnscopedSelectors_FixtureGood(t *testing.T) {
	t.Parallel()
	src := []byte(`
:root { --foo: red; }
@font-face { font-family: 'X'; src: url('x.woff2'); }
body.receiver .foo { color: red; }
body.receiver.idle .bar { opacity: 0.5; }
body.receiver .foo, body.receiver .baz { color: blue; }
body.receiver .ok, body.receiver .also-ok { color: green; }
@container chassis (max-width: 900px) {
  body.receiver .foo { display: none; }
}
@media (max-width: 900px) {
  body.receiver .bar { display: none; }
}
@keyframes spin { 0% { transform: rotate(0); } 100% { transform: rotate(360deg); } }
`)
	leaks := findUnscopedSelectors(src)
	if len(leaks) != 0 {
		t.Errorf("expected zero leaks for well-scoped fixture, got: %v", leaks)
	}
}

func TestFindUnscopedSelectors_FixtureBad(t *testing.T) {
	t.Parallel()
	src := []byte(`
.foo { color: red; }
body.idle .bar { opacity: 0.5; }
.foo, body.receiver .baz { color: blue; }
body.receiver .ok, .leak-after-scoped { color: red; }
@container chassis (max-width: 900px) { .inside-container { display: none; } }
@media (max-width: 900px) { .inside-media { display: none; } }
`)
	leaks := findUnscopedSelectors(src)
	want := map[string]bool{
		".foo":               true,
		"body.idle .bar":     true,
		".leak-after-scoped": true,
		".inside-container":  true,
		".inside-media":      true,
	}
	// ".foo" appears twice (rules 1 and 3); a scoped-first mixed list
	// must still flag its unscoped second selector; nested @container/@media
	// selectors must also be checked, while "body.receiver .baz" is fine.
	if len(leaks) != 6 {
		t.Errorf("expected 6 leaks for fixture, got %d: %v", len(leaks), leaks)
	}
	for _, leak := range leaks {
		if !want[leak] && leak != ".foo" {
			t.Errorf("unexpected leak: %q", leak)
		}
	}
}
```

These fixtures lock in the parser's behaviour so a refactor of `findUnscopedSelectors` cannot accidentally make the whole test no-op. The good fixture covers: `:root`, `@font-face`, scoped selectors inside `@container` and `@media`, `@keyframes`, percentage keyframe selectors, compound state selectors, comma-separated multi-selector lists. The bad fixture covers: bare class, descendant `body.idle`, mixed-comma list with the unscoped selector first, scoped-first mixed-comma list with the unscoped selector second, and unscoped selectors nested inside `@container` / `@media`.

- [ ] **Step 4: Run all CSS-scope tests**

Run: `go test ./internal/chassis/ -run 'TestChassisCSS|TestFindUnscopedSelectors'`
Expected: PASS — fixtures pass, then `chassis.css` passes (if Tasks 17-24 followed the scope rules correctly) or FAILs with a precise list of leaked selectors.

If the real-CSS test FAILs, iterate: open `chassis.css`, find each reported selector, prepend `body.receiver` (or apply the appropriate scope-prefix rule from the spec), re-run until green.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/css_scope_test.go go.mod go.sum
git commit -m "test(chassis): assert all CSS selectors scoped under body.receiver

Uses github.com/tdewolff/parse/v2/css to parse chassis.css and verify
every page selector, including selectors nested under @media/@container,
is either allowlisted (:root / @font-face / @keyframes / keyframe
percentage) or rooted at body.receiver. Also asserts explicit
absence of leak-prone mockup forms (body.idle, body:not(.idle),
body.settings-open, etc.) in bare unscoped form. Adds tdewolff/parse
as a new dependency — call out in PR description.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 26: chassis.js Runtime

**Files:**
- Create: `internal/chassis/static/chassis.js`

The ~150-line vanilla runtime: `Chassis.State`, animator registry, minute-aligned system time ticker, `?dev=1` toggle. No test framework — the runtime is small and exercised manually + via integration tests in Task 30.

- [ ] **Step 1: Create `chassis.js`**

Create `internal/chassis/static/chassis.js`:

```javascript
// Receiver chassis runtime. Phase 0 ships the state machinery + system
// time ticker + ?dev=1 toggle. Later specs (vfd-live.js, transport.js,
// visualizer-bank.js, meter-animators.js) load via additional <script>
// tags and hook into window.Chassis.
(() => {
  'use strict';

  // ── State registry ─────────────────────────────────────────────
  // The body class is the source of truth. Every other runtime piece
  // (animators, label swaps) keys off it via State.current().
  const State = {
    IDLE: 'idle',
    LIVE: 'live',
    current() {
      return document.body.classList.contains('live') ? 'live' : 'idle';
    },
    set(next) {
      document.body.classList.remove('idle', 'live');
      document.body.classList.add(next);
      animators.notify(next);
    },
  };

  // ── Animator registry ──────────────────────────────────────────
  // Empty in Phase 0. Spec 5 (telemetry) registers the spectrum /
  // goniometer / throughput / ACK animators. Each animator exposes
  // start(), stop(), and optionally handleState() for state-aware
  // animators (e.g., spectrum stops drawing in idle).
  const animators = {
    items: [],
    register(animator) {
      this.items.push(animator);
      if (typeof animator.handleState === 'function') {
        animator.handleState(State.current());
      }
    },
    notify(state) {
      this.items.forEach(a => {
        if (typeof a.handleState === 'function') {
          a.handleState(state);
        }
      });
    },
  };

  // ── System time ticker ─────────────────────────────────────────
  // VFD shows live wall-clock time even in idle. Server renders an
  // initial value; this updates it once per minute, aligned to the
  // :00 minute boundary so the visual tick is in phase with the clock.
  function startSystemTimeTicker() {
    const el = document.querySelector('[data-system-time]');
    if (!el) return;
    const tick = () => {
      const d = new Date();
      el.textContent = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
    };
    tick();
    // Math is in unix ms so all timezones and DST transitions align
    // correctly (minute boundaries are universal in epoch terms).
    // Long-lived tabs that survive a system-clock change keep ticking
    // at the original phase until the next reload — acceptable for
    // HH:MM display precision.
    const msUntilNextMinute = 60_000 - (Date.now() % 60_000);
    setTimeout(() => {
      tick();
      setInterval(tick, 60_000);
    }, msUntilNextMinute);
  }

  function pad(n) {
    return n.toString().padStart(2, '0');
  }

  // ── Dev-mode toggle ────────────────────────────────────────────
  // Gated behind ?dev=1 query parameter. Injects a fixed-position
  // floating button that flips between idle and live for design
  // iteration. Never visible unless the operator opens /receiver?dev=1.
  function installDevStateToggle() {
    const btn = document.createElement('button');
    btn.id = 'chassis-dev-state-toggle';
    btn.style.cssText =
      'position:fixed;bottom:12px;right:12px;z-index:9999;' +
      'padding:8px 14px;background:#3a3a3e;color:#c0c0c4;' +
      'border:1px solid #0a0a0b;border-radius:3px;font:600 11px Inter,sans-serif;' +
      'letter-spacing:0.14em;text-transform:uppercase;cursor:pointer;';
    const refreshLabel = () => {
      btn.textContent = `[dev] state: ${State.current()}`;
    };
    refreshLabel();
    btn.addEventListener('click', () => {
      State.set(State.current() === 'idle' ? 'live' : 'idle');
      refreshLabel();
    });
    document.body.appendChild(btn);
  }

  // ── Exports ────────────────────────────────────────────────────
  // Subsequent specs attach feature code to window.Chassis.* rather
  // than creating new globals.
  window.Chassis = { State, animators };

  // ── Boot ───────────────────────────────────────────────────────
  document.addEventListener('DOMContentLoaded', () => {
    startSystemTimeTicker();
    if (new URLSearchParams(location.search).get('dev') === '1') {
      installDevStateToggle();
    }
  });
})();
```

- [ ] **Step 2: Verify the JS loads in a browser**

With the bridge running, visit `http://localhost:32500/receiver` and open DevTools console. Expected: no JS errors. `window.Chassis` is defined with `State` and `animators` properties.

Visit `http://localhost:32500/receiver?dev=1`. Expected: a small `[dev] state: idle` button appears in the bottom-right corner. Clicking it toggles to `[dev] state: live` and `<body>` gets the `live` class (chassis brightens up where idle-state overrides applied).

- [ ] **Step 3: Run all tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/chassis.js
git commit -m "feat(chassis): add chassis.js runtime

~120-line vanilla ES2022 script. Exposes window.Chassis.State (body
class controller) + window.Chassis.animators (registry later specs
hang spectrum/goniometer/throughput/ACK loops off). Minute-aligned
system time ticker keeps the VFD clock in phase. ?dev=1 query param
injects a floating idle/live toggle for design iteration.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 27: Cross-Package Import Lint

**Files:**
- Create: `internal/chassis/import_check_test.go`

Production files in `internal/chassis/`, `internal/ui/`, and `internal/uiserver/` must not import each other. Test files (`*_test.go`) are exempt so integration tests can mount both surfaces.

- [ ] **Step 1: Write the import-check test**

Create `internal/chassis/import_check_test.go`:

```go
package chassis

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionImports_NoCrossPackageCoupling asserts the parallel-
// replacement invariant: production files in internal/chassis,
// internal/ui, and internal/uiserver have zero cross-imports of each
// other. _test.go files are exempt because integration tests in
// tests/integration/ legitimately need to mount both packages.
//
// Uses parser.ImportsOnly to avoid adding a dependency solely for this
// lint. The filepath walker skips *_test.go files, so integration tests
// remain free to mount both surfaces.
func TestProductionImports_NoCrossPackageCoupling(t *testing.T) {
	t.Parallel()
	const modBase = "github.com/idio-sync/MiSTer_GroovyRelay"
	rules := []struct {
		fromDir string
		fromPkg string
		banned []string
	}{
		{"internal/chassis", modBase + "/internal/chassis", []string{modBase + "/internal/ui", modBase + "/internal/uiserver"}},
		{"internal/ui", modBase + "/internal/ui", []string{modBase + "/internal/chassis"}},
		{"internal/uiserver", modBase + "/internal/uiserver", []string{modBase + "/internal/chassis"}},
	}
	root := repoRootFromWD(t)
	for _, rule := range rules {
		dir := filepath.Join(root, filepath.FromSlash(rule.fromDir))
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				for _, banned := range rule.banned {
					if importPath == banned {
						pos := fset.Position(imp.Pos())
						t.Errorf("forbidden production import: %s -> %s at %s",
							rule.fromPkg, importPath, pos)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", rule.fromDir, err)
		}
	}
}

func repoRootFromWD(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("could not find repo root from %s", wd)
		}
		wd = parent
	}
}
```

- [ ] **Step 2: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestProductionImports`
Expected: PASS — no cross-imports currently exist.

- [ ] **Step 3: Verify the test catches a deliberate violation**

Temporarily add to `internal/chassis/server.go`:

```go
import _ "github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
```

Re-run: `go test ./internal/chassis/ -run TestProductionImports`
Expected: FAIL with `forbidden production import: ...chassis -> ...ui`.

Remove the deliberate import and re-run — back to PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/import_check_test.go
git commit -m "test(chassis): assert no cross-imports between chassis/ui/uiserver

Production-file import-check protecting the parallel-replacement
isolation invariant. Uses stdlib parser.ImportsOnly to scan production
Go files only; test files are exempt — tests/integration/ can
legitimately mount both packages. Caught a deliberate violation in
manual verification.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 28: Wire chassis.New into main.go

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Locate the existing ui.Server mount in main.go**

Search for `ui.New(` or `ui.Server` or `uiSrv.Mount(` in `cmd/mister-groovy-relay/main.go`. The chassis init goes right after the ui mount call.

- [ ] **Step 2: Add the chassis init + mount**

After the existing `ui.Server.Mount(mux)` call (or equivalent), insert:

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

Add the import to the import block at the top of `main.go`:

```go
"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
```

- [ ] **Step 3: Build and run**

Run:

```bash
make build
./mister-groovy-relay --config path/to/config.toml
```

PowerShell equivalent when `make` is unavailable:

```powershell
go build -o mister-groovy-relay.exe .\cmd\mister-groovy-relay
.\mister-groovy-relay.exe --config path\to\config.toml
```

Expected: bridge starts successfully. `/ui` and `/receiver` both reachable.

- [ ] **Step 4: Manual route check**

```bash
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:32500/ui     # 200
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:32500/receiver # 200
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:32500/receiver/static/chassis.css # 200
```

PowerShell equivalent:

```powershell
(Invoke-WebRequest -UseBasicParsing http://localhost:32500/ui).StatusCode
(Invoke-WebRequest -UseBasicParsing http://localhost:32500/receiver).StatusCode
(Invoke-WebRequest -UseBasicParsing http://localhost:32500/receiver/static/chassis.css).StatusCode
```

Expected: 200 for all three.

- [ ] **Step 5: Run all tests one more time**

Run: `go test ./...`
Expected: PASS — existing /ui tests still green, chassis tests still green.

- [ ] **Step 6: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(main): wire chassis.New + Mount onto shared mux

Chassis server constructed after the existing ui.Server mount;
shares the same http.ServeMux on port :http_port. Mount order is
documented: ui first, chassis second. dieFriendly handles
construction failures (same pattern as the rest of main.go).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 29: Integration Tests

**Files:**
- Create: `tests/integration/chassis_test.go`

Two tests, both build-tagged `//go:build integration` so they run via `make test-integration` only:

1. `TestReceiverEndToEnd` — boot a full bridge, GET `/receiver`, assert all section root elements present in the response body.
2. `TestMount_DoesNotShadowUIRoutes` — mount both `ui.Server` and `chassis.Server` on a shared mux, assert each surface returns its own content.

- [ ] **Step 1: Locate the existing integration test pattern**

Look at `tests/integration/` for existing tests like `basic_test.go`. Note the test harness — `httptest.NewServer`, the `setup()` helper that builds the full bridge graph, etc.

- [ ] **Step 2: Create the chassis integration test file**

Create `tests/integration/chassis_test.go`:

```go
//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

func TestReceiverEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Date(2026, 5, 21, 18, 35, 0, 0, time.UTC),
		HostIP:    "10.0.0.5",
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/receiver")
	if err != nil {
		t.Fatalf("GET /receiver: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Walk the DOM, collect class names on every element.
	classes := collectClasses(doc)
	required := []string{
		"vfd", "meter-screen", "transport-strip", "viz-bank",
		"source-cluster", "input-section", "preset-bank", "history-section",
	}
	for _, want := range required {
		if !classes[want] {
			t.Errorf("response body missing element with class %q", want)
		}
	}
}

func TestMount_DoesNotShadowUIRoutes(t *testing.T) {
	mux := http.NewServeMux()

	// Build a minimal ui.Server. The existing /ui surface needs more
	// dependencies than chassis; pass nil where the interface allows
	// (ui.Config documents that nil saveables surface as 500 at
	// request time but read-path handlers still work for this test).
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
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cases := []struct {
		path      string
		wantOK    bool
		wantInBody string
	}{
		{"/ui", true, "/ui/static/app.css"},     // ui shell
		{"/ui/static/app.css", true, ""},         // ui CSS
		{"/receiver", true, "<!-- chassis:shell -->"},
		{"/receiver/static/chassis.css", true, ""},
	}
	for _, c := range cases {
		resp, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Errorf("GET %s: %v", c.path, err)
			continue
		}
		ok := resp.StatusCode == http.StatusOK
		if ok != c.wantOK {
			t.Errorf("GET %s status = %d (want OK=%v)", c.path, resp.StatusCode, c.wantOK)
		}
		if c.wantInBody != "" {
			b, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(b), c.wantInBody) {
				t.Errorf("GET %s body missing %q", c.path, c.wantInBody)
			}
		}
		resp.Body.Close()
	}
}

// collectClasses walks the html.Node tree and returns a set of every
// class name found on any element. Used to assert the chassis renders
// all expected section root classes.
func collectClasses(n *html.Node) map[string]bool {
	out := map[string]bool{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, a := range node.Attr {
				if a.Key == "class" {
					for _, c := range strings.Fields(a.Val) {
						out[c] = true
					}
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}
```

- [ ] **Step 3: Confirm `golang.org/x/net/html` is already available**

The repo already requires `golang.org/x/net` in `go.mod`, so `golang.org/x/net/html` should be available without a new dependency change. If a future branch lacks it, add the same pinned module version already used by this repo family:

```bash
go get golang.org/x/net@v0.52.0
go mod tidy
```

Expected for the current branch: no `go get` needed.

- [ ] **Step 4: Run the integration tests**

Run: `make test-integration`
Expected: PASS — both new tests + all existing integration tests.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/chassis_test.go go.mod go.sum
git commit -m "test(chassis): add integration tests for end-to-end + mount isolation

TestReceiverEndToEnd boots a full mux + chassis server, GETs /receiver,
parses the response as HTML, and asserts every expected section root
class (.vfd, .meter-screen, .transport-strip, .viz-bank, etc.) is
present. TestMount_DoesNotShadowUIRoutes mounts both ui.Server and
chassis.Server on the same mux and asserts each surface returns its
own content without cross-contamination. Lives in tests/integration/
because it imports both packages — would violate the production no-
cross-import invariant if placed inside either package.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 30: README + Visual Verification

**Files:**
- Modify: `README.md`
- Create: `docs/superpowers/reference/2026-05-21-receiver-v24.html`

- [ ] **Step 1: Locate the deployment section in README**

Open `README.md` and find the section that documents the UI surface (probably under "Deployment" or "Running the bridge").

- [ ] **Step 2: Add the one-line preview note**

Insert a single paragraph after the existing UI documentation:

```markdown
> **Preview UI:** A new receiver-chassis-styled UI is being built at
> `/receiver` in parallel with `/ui`. It currently renders an idle-state
> preview with no live data — Phases 1-5 wire functionality in. License
> attributions for the bundled fonts: `/receiver/static/fonts/LICENSE`.
```

- [ ] **Step 3: Freeze the visual reference artifact**

The brainstorm directory is not durable product source. Before manual verification, commit the exact mockup artifact used for comparison:

```powershell
New-Item -ItemType Directory -Force -Path docs/superpowers/reference | Out-Null
Copy-Item .superpowers/brainstorm/1973-1779237107/receiver-v24.html docs/superpowers/reference/2026-05-21-receiver-v24.html
```

Git Bash / WSL equivalent:

```bash
mkdir -p docs/superpowers/reference
cp .superpowers/brainstorm/1973-1779237107/receiver-v24.html docs/superpowers/reference/2026-05-21-receiver-v24.html
```

- [ ] **Step 4: Manual visual verification at all breakpoints**

With the bridge running, open `http://localhost:32500/receiver` in a browser and resize / take screenshots at each breakpoint. For each, confirm the visual matches the brainstorm mockup at the same width:

- 1920px desktop — full chassis with all sections visible.
- 1440px — wide-desktop layout integrity.
- 1180px — network scopes hidden in meter row 2; rest unchanged.
- 900px — goniometer hidden; vfd-source-row collapses to a single column.
- 600px — meter source strip hidden; field-flip hidden.
- 480px — degraded but readable (full mobile polish is Phase 5).
- Safari (current macOS) at 1920px and 600px — container queries and OKLCH render correctly.

For each breakpoint, also verify the **required invariants** from the spec:
- No body-level horizontal scroll.
- Status bar / masthead / VFD / meter / transport / visualizer / input / preset / history regions all visible (or explicitly hidden at this breakpoint per the rules above).
- DSEG glyphs loaded for VFD / meter numerals (numerals look segmented, not Inter / monospace).
- Inter loaded for non-segmented UI text (status bar label, source button labels, etc.).
- Focus rings visible on buttons / inputs (Tab through and confirm).

Compare against `docs/superpowers/reference/2026-05-21-receiver-v24.html` and attach screenshots to the PR description for each desktop breakpoint.

- [ ] **Step 5: Commit the README + reference artifact change**

```bash
git add README.md
git add -f docs/superpowers/reference/2026-05-21-receiver-v24.html
git commit -m "docs: note the new /receiver preview UI

One-paragraph mention under the deployment section plus the exact
receiver-v24 mockup artifact used for Phase 0 visual comparison.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

- [ ] **Step 6: Open the PR**

The PR description should include:

```markdown
# Phase 0: Receiver Chassis Foundation

Implements docs/superpowers/specs/2026-05-21-receiver-chassis-foundation-design.md.

## What's in

- New `internal/chassis/` package serving `/receiver/*` in parallel with the existing `/ui/*` surface.
- Full chassis idle-state visual rendered at every container-query breakpoint.
- Self-hosted DSEG + Inter fonts (woff2), `chassis.css` (~3,200 lines, ported from brainstorm mockup with `body.receiver` scope-prefix pass), `chassis.js` (~120 lines, exposes `window.Chassis` namespace for later specs).
- 13 partial templates composed by `shell.html`.
- `idleSnapshot()` helper produces all idle placeholder content; Spec 2 will replace with `snapshotFromSession()`.
- Tests: 13 Layer 1 + 2 Layer 2 + 2 Layer 3 (integration) + cross-package import lint + CSS scope assertion (uses `github.com/tdewolff/parse/v2/css` — new dep).

## New dependency

`github.com/tdewolff/parse/v2` — MIT-licensed CSS tokenizer used only by `TestChassisCSS_AllSelectorsScoped` to verify the `body.receiver` scope-isolation invariant. Not pulled into runtime code paths.

## Verified visually at

- 1920px Chrome
- 1440px Chrome
- 1180px Chrome (network scopes hidden in meter row 2)
- 900px Chrome (goniometer hidden, source cluster wraps)
- 600px Chrome (meter source strip hidden)
- 480px Chrome (degraded but readable)
- 1920px Safari current macOS (OKLCH + container queries)
- 600px Safari (responsive collapse)

Screenshots attached.

## What's not in (deferred to later specs)

- Any live data, telemetry, playback control, real session state — Specs 1-5.
- Settings drawer content — Spec 8 (ports the existing bridge/adapter forms into chassis style).
- History row content — Spec 9.
- Final cutover of `/ui/*` → `/receiver/*` — last spec.
```

---

## Self-Review

Run through the spec's section list against this plan:

**Goals (spec §Goals 1-5):**
- Mount `/receiver/*` parallel to `/ui/*` → **Tasks 7 + 28.**
- Render full chassis chrome at `GET /receiver` → **Tasks 8-14 + 17-24.**
- Establish production patterns for later specs → **Tasks 1-7 establish all of them.**
- Self-host all chassis fonts → **Task 15.**
- ~150-line vanilla JS runtime → **Task 26.**

**Non-goals (spec §Non-goals):** None implemented (deferred to later specs as the spec dictates).

**Done When (spec §Done When):**
- `/receiver` returns mockup idle visual at every breakpoint → **Tasks 16-24 + 30.**
- No regressions on `/ui/*` → **Task 29 (TestMount_DoesNotShadowUIRoutes).**
- All chassis assets embedded → **Tasks 5, 6, 15.**
- `internal/chassis/` exists with `templates/` and `static/` → **Tasks 1, 5, 8.**
- Inter self-hosted, no Google Fonts link → **Tasks 15, 16.**

**Architecture (spec §Architecture):**
- File layout → **Tasks 1, 5, 8, 15** (creates each file).
- Embed declarations → **Task 5.**
- Server config → **Task 1.**
- Route mounting → **Task 7 + 28.**
- MIME-type discipline → **Task 2.**

**Data Model (spec §Data Model & Template Composition):**
- `ReceiverPageData` + sub-structs → **Task 3.**
- `idleSnapshot()` → **Task 4.**
- Template composition (shell + 12 partials + `vfd-source-row` composer) → **Tasks 8-14.**
- Helper FuncMap (inc, hasString, replaceAll, pad2, dim, +until, +list) → **Tasks 5, 11.**

**CSS Architecture (spec §CSS Architecture & Design Tokens):**
- Single `chassis.css` with 15 banner sections → **Tasks 16-24** (sections 1-15 ported across the tasks).
- Scope isolation rules → **Tasks 17-24** apply, **Task 25** asserts.
- Design tokens verbatim from mockup → **Task 16.**
- Font loading + LICENSE + SOURCES.md → **Tasks 15, 16.**
- Container queries with `container-name` + `container-type` ports → **Task 24.**

**JS Runtime (spec §JS Runtime & State Machine):**
- Single `chassis.js` file → **Task 26.**
- State machinery + `window.Chassis` namespace → **Task 26.**
- Minute-aligned system time ticker → **Task 26.**
- `?dev=1` toggle → **Task 26.**

**Testing (spec §Testing Approach):**
- Layer 1 Go unit + handler tests → **Tasks 1, 2, 4, 5, 6, 7, 8, 15.**
- Layer 2 template compilation tests → **Task 5.**
- Layer 3 integration tests → **Task 29.**
- Cross-package import lint → **Task 27.**
- `TestChassisCSS_AllSelectorsScoped` → **Task 25.**
- Visual verification → **Task 30.**

**Migration & Rollout (spec §Migration & Rollout):**
- Coexistence invariants → **Tasks 27, 29.**
- Asset caching + version querystring → **Tasks 6, 7.**
- README preview note → **Task 30.**
- No new config flags → satisfied by omission throughout.

**Implementation Checklist (spec §Implementation Checklist 1-17):** Cross-referenced — every checklist item maps to one or more plan tasks.

**Appendix A (`idleSnapshot()` Content Map):** Used directly to derive Task 4's struct population. Test cases in Task 4 spot-check the mapping.

No `TBD` / `TODO` / `FIXME` markers. The only explicit placeholders are the `<PASTE-SHA-256-HERE>` entries inside the `SOURCES.md` template, and Task 15 requires replacing them before commit. Types and method signatures are consistent across tasks (e.g., `chassis.Config`, `chassis.New`, `s.handleIndex`, `s.handleStatic`, `s.cssBytes`, `s.tmpl` are all used consistently).

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-21-receiver-chassis-foundation.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
