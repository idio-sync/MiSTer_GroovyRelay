# Visualizer Waterfall Swap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the redundant `neon_grid` visualizer mode with a new `showspectrum` scrolling-waterfall mode (`spectrum_waterfall`, button label WATERFALL), and fully remove the unused `radial_spectrum` preview button plus its preview-button mechanism. Menu stays at 8 live modes.

**Architecture:** The visualizer mode is a typed enum mirrored as three independent constant sets — `internal/config` (canonical `string`), `internal/core` (`core.VisualizerMode`), and `internal/ffmpeg` (`ffmpeg.VisualizerMode`) — joined by two mapping functions and a validation switch in `internal/core/manager.go`. The Settings-UI dropdown derives from `config.SupportedVisualizerModes()`; the receiver chassis renders its own parallel button list in `internal/chassis/data.go`. Rendering happens entirely in an FFmpeg `filter_complex` graph built in `internal/ffmpeg/pipeline.go`.

**Tech Stack:** Go 1.26, FFmpeg filtergraphs (`showspectrum`), `html/template` + vanilla JS for the chassis UI, table-driven `go test` (+ `-tags=integration` tests that spawn real ffmpeg).

**Source spec:** `docs/superpowers/specs/2026-05-28-visualizer-waterfall-swap-design.md`

---

## Why the swap is one atomic commit

`config.VisualizerModeNeonGrid` is referenced from other packages (e.g. `internal/core/manager.go` `coreVisualizerModeFromConfig`, `internal/ui/bridge_fields_test.go`, `internal/chassis`). Renaming/removing it leaves the tree non-compiling until every reference is updated in the same change. Task 1 therefore edits all swap touchpoints and commits once. Within the task we still go red→green: edit the test expectations first, watch them fail, then make the source edits and watch them pass. Tasks 2 (radial/preview cleanup) and 3 (docs) are independent and each commit cleanly.

## File Structure

**Task 1 — swap `neon_grid` → `spectrum_waterfall` (one commit):**
- Modify: `internal/config/config.go` — const, `supportedVisualizerModes` slice, validate switch
- Modify: `internal/core/types.go` — `core.VisualizerMode` const
- Modify: `internal/core/manager.go` — `validateVisualizerRequest`, `coreVisualizerModeFromConfig`, `ffmpegVisualizerMode`
- Modify: `internal/ffmpeg/pipeline.go` — const, `RequiredVisualizerFilters`, `visualizerCoreGraph`
- Modify: `internal/chassis/data.go` — the NEON GRID button → WATERFALL button
- Modify: `internal/chassis/static/chassis.css` — add `viz-icon--waterfall`
- Tests: `internal/config/config_test.go`, `internal/ffmpeg/pipeline_test.go`, `internal/core/manager_test.go`, `internal/ui/bridge_fields_test.go`, `internal/chassis/chassis_test.go`, `tests/integration/visualizer_modes_test.go`

**Task 2 — remove `radial_spectrum` button + preview mechanism (one commit):**
- Modify: `internal/chassis/data.go` — drop radial button, drop `IsPreview` field + doc comments
- Modify: `internal/chassis/templates/visualizer-bank.html` — collapse the `{{if .IsPreview}}` branch
- Modify: `internal/chassis/static/chassis.css` — drop `viz-icon--radial` + `viz-btn--preview*` rules
- Modify: `internal/chassis/static/visualizer-bank.js` — drop `isPreview()` and its two call sites
- Tests: `internal/chassis/chassis_test.go` — drop radial row + `IsPreview` from fixture, drop three preview-contract assertions

**Task 3 — docs (one commit):**
- Modify: `README.md` — supported-modes list
- Modify: `internal/config/example.toml` — visualizer mode comment

---

## Task 1: Swap `neon_grid` → `spectrum_waterfall`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/ffmpeg/pipeline.go`
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/static/chassis.css`
- Test: `internal/config/config_test.go`, `internal/ffmpeg/pipeline_test.go`, `internal/core/manager_test.go`, `internal/ui/bridge_fields_test.go`, `internal/chassis/chassis_test.go`, `tests/integration/visualizer_modes_test.go`

- [ ] **Step 1: Update unit-test expectations (red)**

In `internal/config/config_test.go`, in `TestSupportedVisualizerModes_ReturnsDefensiveCopy` (the `want` slice) and `TestSectioned_Validate_VisualizerMode` (the `valid` slice), replace the line `VisualizerModeNeonGrid,` with `VisualizerModeSpectrumWaterfall,` (same position, after `VisualizerModeVUCabinet,`).

In `internal/ffmpeg/pipeline_test.go`:
- In `TestBuildVisualizerFilterChain_AllModesBarsOnlyWhenDrawTextUnavailable`, replace the row
  `{"neon grid", VisualizerModeNeonGrid, "showfreqs=s=640x480:mode=bar"},`
  with
  `{"spectrum waterfall", VisualizerModeSpectrumWaterfall, "showspectrum=s=640x480:slide=scroll"},`
- In `TestVisualizerRequiredFilters`, replace the row
  `{VisualizerModeNeonGrid, []string{"showfreqs", "drawgrid", "hue"}},`
  with
  `{VisualizerModeSpectrumWaterfall, []string{"showspectrum"}},`

In `internal/core/manager_test.go`:
- In `TestValidateVisualizerRequestModes` (the `[]VisualizerMode{...}` slice), replace `VisualizerModeNeonGrid,` with `VisualizerModeSpectrumWaterfall,`.
- In `TestFFmpegVisualizerSpecMapsModes` (the cases slice), replace
  `{VisualizerModeNeonGrid, ffmpeg.VisualizerModeNeonGrid},`
  with
  `{VisualizerModeSpectrumWaterfall, ffmpeg.VisualizerModeSpectrumWaterfall},`

In `internal/ui/bridge_fields_test.go`, in `TestBridgeFields_HasVisualizerMode` (the `wantEnum` slice), replace `config.VisualizerModeNeonGrid,` with `config.VisualizerModeSpectrumWaterfall,`.

In `internal/chassis/chassis_test.go`, in the visualizer-button fixture, replace the line
`{Mode: config.VisualizerModeNeonGrid, Label: "NEON GRID", IconKind: "analyzer", IsPreview: false},`
with
`{Mode: config.VisualizerModeSpectrumWaterfall, Label: "WATERFALL", IconKind: "waterfall", IsPreview: false},`

Add this new CSS-contract test to `internal/chassis/chassis_test.go` (near the other `TestChassisCSS_*` tests):

```go
func TestChassisCSS_HasWaterfallIcon(t *testing.T) {
	t.Parallel()
	css, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	if !strings.Contains(string(css), "viz-icon--waterfall") {
		t.Error("chassis.css missing .viz-icon--waterfall rule for the WATERFALL button")
	}
}
```

- [ ] **Step 2: Confirm the tree does not build (red)**

Run: `go build ./... 2>&1 | head`
Expected: compile errors such as `undefined: VisualizerModeSpectrumWaterfall` / `undefined: config.VisualizerModeSpectrumWaterfall` in config, ffmpeg, core, ui, and chassis test packages. (This confirms the tests now demand the new constant.)

- [ ] **Step 3: Add the `config` constant, slice entry, and validate case**

In `internal/config/config.go`, in the `const (...)` block, replace
`VisualizerModeNeonGrid         = "neon_grid"`
with
`VisualizerModeSpectrumWaterfall = "spectrum_waterfall"`

In the `supportedVisualizerModes` slice, replace `VisualizerModeNeonGrid,` with `VisualizerModeSpectrumWaterfall,`.

In the `Validate` switch (the `case VisualizerModeRetroAnalyzer, ...` list), replace `VisualizerModeNeonGrid,` with `VisualizerModeSpectrumWaterfall,`.

- [ ] **Step 4: Add the `core` constant and update the three switches**

In `internal/core/types.go`, in the `const (...)` block, replace
`VisualizerModeNeonGrid         VisualizerMode = "neon_grid"`
with
`VisualizerModeSpectrumWaterfall VisualizerMode = "spectrum_waterfall"`

In `internal/core/manager.go`:
- `validateVisualizerRequest` switch: replace `VisualizerModeNeonGrid,` with `VisualizerModeSpectrumWaterfall,`.
- `coreVisualizerModeFromConfig`: replace

```go
	case config.VisualizerModeNeonGrid:
		return VisualizerModeNeonGrid
```

with

```go
	case config.VisualizerModeSpectrumWaterfall:
		return VisualizerModeSpectrumWaterfall
```

- `ffmpegVisualizerMode`: replace

```go
	case VisualizerModeNeonGrid:
		return ffmpeg.VisualizerModeNeonGrid
```

with

```go
	case VisualizerModeSpectrumWaterfall:
		return ffmpeg.VisualizerModeSpectrumWaterfall
```

- [ ] **Step 5: Add the `ffmpeg` constant, required-filter case, and core graph**

In `internal/ffmpeg/pipeline.go`:
- In the `VisualizerMode` `const (...)` block, replace
  `VisualizerModeNeonGrid         VisualizerMode = "neon_grid"`
  with
  `VisualizerModeSpectrumWaterfall VisualizerMode = "spectrum_waterfall"`
- In `RequiredVisualizerFilters`, replace

```go
	case VisualizerModeNeonGrid:
		return []string{"showfreqs", "drawgrid", "hue"}
```

with

```go
	case VisualizerModeSpectrumWaterfall:
		return []string{"showspectrum"}
```

- In `visualizerCoreGraph`, replace

```go
	case VisualizerModeNeonGrid:
		return fmt.Sprintf("[%s]showfreqs=s=%dx%d:mode=bar:ascale=log:fscale=log:colors=0xff2bd6|0x28f7ff,drawgrid=w=iw/12:h=ih/6:t=1:c=0x28f7ff@0.22,hue=h=2*PI*t:s=1.35[viz0]", audioMap, logicalW, logicalH), "viz0"
```

with

```go
	case VisualizerModeSpectrumWaterfall:
		return fmt.Sprintf("[%s]showspectrum=s=%dx%d:slide=scroll:mode=combined:color=intensity:scale=cbrt:fscale=log:overlap=0.5:saturation=1.4:legend=0,format=rgba[viz0]", audioMap, logicalW, logicalH), "viz0"
```

- [ ] **Step 6: Swap the chassis button and add the icon**

In `internal/chassis/data.go`, in the `Buttons: []VisualizerButton{...}` literal, replace
`{Mode: config.VisualizerModeNeonGrid, Label: "NEON GRID", IconKind: "analyzer", IsPreview: false},`
with
`{Mode: config.VisualizerModeSpectrumWaterfall, Label: "WATERFALL", IconKind: "waterfall", IsPreview: false},`

In `internal/chassis/static/chassis.css`, add this rule immediately after the `body.receiver .viz-icon--scope::after { ... }` rule (i.e. with the other `viz-icon--*` rules):

```css
body.receiver .viz-icon--waterfall::before {
  content: "";
  width: 28px;
  height: 12px;
  background: repeating-linear-gradient(
    90deg,
    currentColor 0 2px,
    transparent 2px 4px
  );
  opacity: 0.85;
  -webkit-mask: linear-gradient(180deg, #000 0 45%, rgba(0, 0, 0, 0.4) 100%);
  mask: linear-gradient(180deg, #000 0 45%, rgba(0, 0, 0, 0.4) 100%);
}
```

- [ ] **Step 7: Build and run unit tests (green)**

First re-format the edited Go files — `VisualizerModeSpectrumWaterfall` (31 chars) is now the longest identifier in the `config.go`, `core/types.go`, and `pipeline.go` const blocks, so gofmt reflows each block's `=` alignment:

Run: `gofmt -w internal/config/config.go internal/core/types.go internal/core/manager.go internal/ffmpeg/pipeline.go internal/chassis/data.go`
Expected: no error (files reformatted in place if needed).

Run: `go build ./...`
Expected: builds with no errors.

Run: `go test ./internal/config/ ./internal/core/ ./internal/ffmpeg/ ./internal/ui/ ./internal/chassis/`
Expected: PASS (all packages).

Run: `go vet ./...`
Expected: no findings.

- [ ] **Step 8: Update and run the integration test (green)**

In `tests/integration/visualizer_modes_test.go`, in the `cases` slice, replace
`{"neon grid", ffmpeg.VisualizerModeNeonGrid, false, false},`
with
`{"waterfall", ffmpeg.VisualizerModeSpectrumWaterfall, false, false},`

Run: `go test -tags=integration -run TestVisualizerModesSpawnRealFFmpeg ./tests/integration/`
Expected: PASS (requires `ffmpeg`/`ffprobe` on PATH with the `showspectrum` filter; the test self-skips a mode whose required filters are missing via `skipIfVisualizerFiltersMissing`).

- [ ] **Step 9: Confirm no stray `neon_grid` references remain in code**

Run: `git grep -n "neon_grid\|NeonGrid\|NEON GRID"`
Expected: matches ONLY in `docs/superpowers/` (specs/plans). No matches under `internal/`, `cmd/`, `tests/`, `README.md`, or `internal/config/example.toml`. (README/example.toml are handled in Task 3; if they still match here that is expected until Task 3.)

- [ ] **Step 10: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go \
  internal/core/types.go internal/core/manager.go internal/core/manager_test.go \
  internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go \
  internal/ui/bridge_fields_test.go \
  internal/chassis/data.go internal/chassis/chassis_test.go internal/chassis/static/chassis.css \
  tests/integration/visualizer_modes_test.go
git commit -m "feat(visualizer): replace neon_grid with spectrum_waterfall

Swap the redundant neon_grid mode (a showfreqs reskin) for a showspectrum
scrolling-waterfall tuned for 15kHz CRT readability (intensity heatmap, log
freq, cbrt magnitude). Updates the config/core/ffmpeg enum mirror, the core
mapping/validation switches, the chassis WATERFALL button + icon, and all
unit/integration tests."
```

---

## Task 2: Remove the `radial_spectrum` preview button and the preview mechanism

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/templates/visualizer-bank.html`
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/static/visualizer-bank.js`
- Test: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Update tests (red)**

In `internal/chassis/chassis_test.go`:

(a) In the visualizer-button fixture, delete the radial line entirely:
`{Mode: "radial_spectrum", Label: "RADIAL", IconKind: "radial", IsPreview: true},`
and remove `, IsPreview: false` from each of the remaining 8 button rows (the `IsPreview` field is being deleted from the struct). The fixture becomes:

```go
			Buttons: []VisualizerButton{
				{Mode: config.VisualizerModeRetroAnalyzer, Label: "ANALYZER", IconKind: "analyzer"},
				{Mode: config.VisualizerModeOscilloscopeWave, Label: "OSCILLOSCOPE", IconKind: "wave"},
				{Mode: config.VisualizerModeStereoScope, Label: "STEREO SCOPE", IconKind: "scope"},
				{Mode: config.VisualizerModeVUCabinet, Label: "VU CABINET", IconKind: "scope"},
				{Mode: config.VisualizerModeSpectrumWaterfall, Label: "WATERFALL", IconKind: "waterfall"},
				{Mode: config.VisualizerModeRasterPulse, Label: "RASTER PULSE", IconKind: "wave"},
				{Mode: config.VisualizerModeCoverVU, Label: "COVER VU", IconKind: "scope"},
				{Mode: config.VisualizerModeCoverSpectrum, Label: "COVER SPECTRUM", IconKind: "analyzer"},
			},
```

(b) In `TestChassisCSS_TransportNarrowLayoutAndPreviewDisabled`, delete these two entries from the `want` slice:
`` `body.receiver .viz-btn--preview:disabled`, ``
`` `cursor: not-allowed;`, ``
(Keep all the transport/narrow-layout entries.)

(c) In the JS runtime-contract test (the one reading `static/visualizer-bank.js`), delete the entry `"viz-btn--preview",` from the asserted-strings slice.

(d) In the page-render accessibility test, delete the entry:
`` `class="hw-btn viz-btn viz-btn--preview" type="button" role="radio" aria-checked="false" aria-disabled="true" disabled`, ``

- [ ] **Step 2: Run chassis tests to confirm failure (red)**

Run: `go test ./internal/chassis/ 2>&1 | head -30`
Expected: FAIL — the fixture no longer compiles (`unknown field IsPreview` once Step 3 lands is the reverse; before Step 3 the production `data.go` still emits the radial button and `IsPreview`, so the fixture DeepEqual fails / struct mismatch). Either way the chassis package is red, proving the tests drive the change.

- [ ] **Step 3: Remove the `IsPreview` field and radial button from `data.go`**

In `internal/chassis/data.go`:

Replace the `VisualizerData` doc comment

```go
// VisualizerData drives the 4-button visualizer-bank selector. One of
// the buttons, radial_spectrum, is rendered as a deferred preview.
```

with

```go
// VisualizerData drives the visualizer-bank selector.
```

Replace the `VisualizerButton` struct + its doc comment

```go
// VisualizerButton represents one visualizer-bank button. IsPreview
// renders the deferred-state badge and short-circuits click handlers.
type VisualizerButton struct {
	Mode      string
	Label     string
	IconKind  string
	IsPreview bool
}
```

with

```go
// VisualizerButton represents one visualizer-bank button.
type VisualizerButton struct {
	Mode     string
	Label    string
	IconKind string
}
```

In the `Buttons: []VisualizerButton{...}` literal, delete the radial line
`{Mode: "radial_spectrum", Label: "RADIAL", IconKind: "radial", IsPreview: true},`
and remove `, IsPreview: false` from each of the remaining 8 rows (matching the fixture in Step 1a).

- [ ] **Step 4: Collapse the template preview branch**

In `internal/chassis/templates/visualizer-bank.html`, replace the entire `{{range .Buttons}} ... {{end}}` body

```html
    {{range .Buttons}}
    {{if .IsPreview}}
    <button class="hw-btn viz-btn viz-btn--preview" type="button" role="radio" aria-checked="false" aria-disabled="true" disabled data-viz="{{.Mode}}" title="{{.Label}} (preview &mdash; deferred from v1)">
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
```

with

```html
    {{range .Buttons}}
    <button class="hw-btn viz-btn{{if eq .Mode $.ActiveMode}} active lit{{end}}" type="button" role="radio" aria-checked="{{if eq .Mode $.ActiveMode}}true{{else}}false{{end}}" data-viz="{{.Mode}}" title="{{.Label}}">
      <span class="viz-icon viz-icon--{{.IconKind}}" aria-hidden="true"></span>
      <span class="viz-label">{{.Label}}</span>
    </button>
    {{end}}
```

- [ ] **Step 5: Remove preview CSS and the radial icon**

In `internal/chassis/static/chassis.css`, delete the entire `body.receiver .viz-icon--radial::before { ... }` rule, and delete all four preview rules: `body.receiver .viz-btn--preview { ... }`, `body.receiver .viz-btn--preview:hover, body.receiver .viz-btn--preview:disabled { ... }`, `body.receiver .viz-btn--preview .viz-badge { ... }`, and `body.receiver.idle .viz-btn--preview .viz-badge { ... }`.

- [ ] **Step 6: Remove `isPreview()` from the JS and its two call sites**

In `internal/chassis/static/visualizer-bank.js`:

Delete the helper:

```js
  function isPreview(btn) {
    return btn.classList.contains('viz-btn--preview') || btn.disabled || btn.getAttribute('aria-disabled') === 'true';
  }
```

In `setMode`, change

```js
      const active = !isPreview(btn) && btn.dataset.viz === mode;
```

to

```js
      const active = btn.dataset.viz === mode;
```

In `bindClicks`, change

```js
    buttons().forEach((btn) => {
      if (isPreview(btn)) {
        return;
      }
      btn.addEventListener('click', () => {
```

to

```js
    buttons().forEach((btn) => {
      btn.addEventListener('click', () => {
```

- [ ] **Step 7: Build and test (green)**

Run: `gofmt -w internal/chassis/data.go`
Then: `go build ./... && go test ./internal/chassis/`
Expected: PASS.

Run: `git grep -n "radial\|IsPreview\|viz-btn--preview\|isPreview\|viz-badge" -- internal/ cmd/ tests/`
Expected: no matches in `internal/chassis` source/templates/static or other code (radial-named tests in `internal/chassis/visualizer_test.go` are about rejecting the unsupported mode string `radial_spectrum`; those references are to the literal string, not the removed mechanism, and may remain — confirm any remaining matches are only those handler/fallback tests).

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/data.go internal/chassis/chassis_test.go \
  internal/chassis/templates/visualizer-bank.html \
  internal/chassis/static/chassis.css internal/chassis/static/visualizer-bank.js
git commit -m "refactor(chassis): remove radial_spectrum preview button and preview mechanism

Delete the unused radial preview button along with the entire preview-button
scaffolding it solely used: the IsPreview field, the template's preview
branch, the viz-btn--preview and viz-icon--radial CSS, the JS isPreview()
helper, and the preview-contract test assertions."
```

---

## Task 3: Documentation

**Files:**
- Modify: `README.md`
- Modify: `internal/config/example.toml`

- [ ] **Step 1: Update the README supported-modes list**

In `README.md`, in the "Music visualizer modes" list, replace the line
`- \`neon_grid\`: arcade spectrum bars with a neon grid treatment.`
with
`- \`spectrum_waterfall\`: scrolling spectrogram waterfall (showspectrum).`

- [ ] **Step 2: Update the example config comment**

In `internal/config/example.toml`, replace the visualizer mode comment line
`mode = "retro_analyzer"           # retro_analyzer, oscilloscope_wave, stereo_scope, vu_cabinet, neon_grid, raster_pulse, cover_vu, cover_spectrum`
with
`mode = "retro_analyzer"           # retro_analyzer, oscilloscope_wave, stereo_scope, vu_cabinet, spectrum_waterfall, raster_pulse, cover_vu, cover_spectrum`

- [ ] **Step 3: Verify no `neon_grid` remains anywhere outside design docs**

Run: `git grep -n "neon_grid\|NEON GRID"`
Expected: matches only under `docs/superpowers/` (the spec and this plan). No matches under `internal/`, `cmd/`, `tests/`, or `README.md`.

- [ ] **Step 4: Final full verification**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS.

Run (if ffmpeg/ffprobe available): `make test-integration`
Expected: PASS (the waterfall integration case spawns real ffmpeg).

- [ ] **Step 5: Commit**

```bash
git add README.md internal/config/example.toml
git commit -m "docs: document spectrum_waterfall, drop neon_grid"
```

---

## Self-Review

**Spec coverage** (against `docs/superpowers/specs/2026-05-28-visualizer-waterfall-swap-design.md`):
- Part A (remove neon_grid): config/core/ffmpeg/chassis + tests — Task 1. ✓
- Part B (add spectrum_waterfall graph, required filter, button, icon): Task 1, Steps 5–6. ✓
- Part C (remove radial preview button + full preview mechanism): Task 2. ✓
- Part D (core mode mirror — types.go + three manager.go switches + manager_test.go): Task 1, Steps 4 + 1. ✓
- Consolidated touchpoints incl. `bridge_fields_test.go` (found during planning, not in the spec's test list): Task 1, Step 1. ✓
- Docs (README + example.toml): Task 3. ✓
- The ffmpeg catalog appendix is reference-only; no implementation. ✓

**Placeholder scan:** No TBD/TODO; every code step shows the exact before/after text and the exact command + expected result.

**Type/name consistency:** `VisualizerModeSpectrumWaterfall` (config `string`, `core.VisualizerMode`, `ffmpeg.VisualizerMode`) and button label `WATERFALL` / `IconKind: "waterfall"` / CSS `viz-icon--waterfall` are used identically across all tasks. The required-filter set `["showspectrum"]` matches the single special filter in the proposed graph.

**Note on commit shape:** Task 1 is intentionally a single commit spanning packages because the typed constant is cross-package referenced (a partial swap will not compile). Red→green is preserved within the task by editing test expectations first (Steps 1–2) before the source (Steps 3–6).
