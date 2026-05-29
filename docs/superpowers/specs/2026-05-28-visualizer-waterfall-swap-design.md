# Visualizer Waterfall Swap Design

Date: 2026-05-28
Status: Draft for review
Scope: Replace the redundant `neon_grid` visualizer mode with a new
`showspectrum` scrolling-waterfall mode, and fully remove the unused
`radial_spectrum` preview button and its preview-button infrastructure from
the receiver chassis UI. Menu stays at 8 live modes.

## Goal

Two cleanups plus one genuinely-new visualization, with no net change to the
mode count:

1. **Remove `neon_grid`.** It is a presentation-only reskin of
   `retro_analyzer`: both drive `showfreqs` bars; `neon_grid` only adds a
   recolor, a `drawgrid` overlay, and an animated `hue` rotate
   (`internal/ffmpeg/pipeline.go` `visualizerCoreGraph`). The original
   expansion design already flagged this — it called `neon_grid` "the arcade
   sibling of `retro_analyzer`" that "overlaps with `retro_analyzer` at the
   signal level" (see `docs/superpowers/specs/2026-05-21-music-visualizer-expansion-design.md`).
   It is the menu's clearest redundancy.

2. **Add `spectrum_waterfall`** in the vacated slot. A scrolling
   time-frequency spectrogram built on `showspectrum`. This is the first mode
   to use a motion-over-time / heatmap primitive — the current eight modes
   only cover four primitives (`showfreqs` bars, `showwaves` waveform,
   `avectorscope` phase, `showvolume` level), none of which scroll.

3. **Remove the `radial_spectrum` preview button** and the entire
   preview-button mechanism it was the sole user of. `radial_spectrum` is
   explicitly out of scope in the expansion design and is not in the
   supported-mode set; the button is a disabled placeholder that renders
   nothing.

## Non-Goals / Out Of Scope

- Adding any of the future catalog modes (Appendix A). This change ships only
  `spectrum_waterfall`.
- Changing core/manager, the data plane, adapters, or apply-scope behavior.
  Visualizer mode is an opaque string to the manager; mode changes remain
  `ScopeRestartCast` (apply on next music cast).
- Live switching the running visualizer mode, per-track/per-adapter
  preferences, album-art plumbing — all unchanged and out of scope.

## Design Principles (inherited)

These carry over from the existing visualizer specs and constrain this work:

1. Arcade/CRT modes favor large shapes, stable layout, and 240p/480i
   readability over dense detail.
2. All rendering goes through the existing FFmpeg pipeline → Groovy data
   plane. No browser-side rendering.
3. Required FFmpeg filters are gated by `RequiredVisualizerFilters` +
   `CheckVisualizerFilters`; a mode whose filter is missing fails fast before
   plane spawn rather than silently degrading the visual.
4. The Go constant set (`config.go` + `pipeline.go`) and the UI-exposed enum
   must stay in sync. The UI dropdown derives from
   `config.SupportedVisualizerModes()`
   (`internal/ui/bridge_fields.go`), so the config list is the single source
   of truth for the Settings UI; the chassis button bank
   (`internal/chassis/data.go`) is maintained in parallel.

## Part A — Remove `neon_grid`

`neon_grid` (`VisualizerModeNeonGrid = "neon_grid"`) is deleted from every
enum, graph, button, test, and doc. Because the Settings UI dropdown is
generated from `config.SupportedVisualizerModes()`, removing the config
constant + slice entry automatically removes it from the UI; no UI template
change is needed for the dropdown.

The `neon_grid` button in the chassis bank uses `IconKind: "analyzer"`, which
is shared with `retro_analyzer` and `cover_spectrum`, so no icon CSS is
removed for the `neon_grid` deletion.

## Part B — Add `spectrum_waterfall` (WATERFALL)

### Identity

- Go constant: `VisualizerModeSpectrumWaterfall = "spectrum_waterfall"` in
  both `internal/config/config.go` and `internal/ffmpeg/pipeline.go`.
- Chassis button: label `WATERFALL`, placed in the slot `neon_grid` vacated,
  with a new `IconKind: "waterfall"`.
- The name avoids the bare word "spectrum-as-bars" collision with
  `retro_analyzer` / `cover_spectrum` while staying descriptive.

### Core filter graph

Added to `visualizerCoreGraph` in `internal/ffmpeg/pipeline.go`, mirroring the
existing single-clause core-graph style that terminates in `[viz0]`:

```go
case VisualizerModeSpectrumWaterfall:
    return fmt.Sprintf("[%s]showspectrum=s=%dx%d:slide=scroll:mode=combined:"+
        "color=intensity:scale=cbrt:fscale=log:overlap=0.5:saturation=1.4:legend=0,"+
        "format=rgba[viz0]", audioMap, logicalW, logicalH), "viz0"
```

`audioMap` comes from `visualizerAudioInputMap(s)` (handles single vs.
dual-input audio mapping); `logicalW`/`logicalH` come from
`logicalCanvas(s.OutputHeight)`, exactly as the other core modes.

The mode is **not** an artwork mode, so `visualizerModeUsesArtwork` returns
false and the standard (non-cover) path is used. The terminal
`[viz0] … fps=<OutputFpsExpr>,scale=w=OutputWidth:h=OutputHeight,format=bgr24
[visualizer_video]` clause in `buildVisualizerFilterChain` handles cadence,
output sizing, and pixel format unchanged. `showspectrum` emits frames at its
FFT-hop rate (below 59.94); the terminal `fps` filter frame-duplicates up to
the output rate, which is acceptable for a scrolling waterfall.

### Parameter rationale (CRT readability)

The risk with a spectrogram on a low-resolution interlaced CRT is fine
detail smearing/flickering. Every parameter is chosen to mitigate that:

| Parameter        | Reason                                                                 |
|------------------|------------------------------------------------------------------------|
| `fscale=log`     | Log frequency axis compresses the dense high end (worst smear zone) and gives bass/mids more vertical room. |
| `scale=cbrt`     | Cube-root magnitude scaling lifts quiet detail so the frame isn't mostly black; raises contrast. |
| `color=intensity`| Bold blue→red→white heatmap. Maximally distinct from the green/cyan accents of existing modes; survives the limited CRT gamut. |
| `slide=scroll`   | Smooth one-column-per-frame horizontal scroll; no jarring wrap artifact. |
| `mode=combined`  | Mixes L/R into one full-height image. `separate` would halve vertical resolution per channel — unreadable at 240p. |
| `overlap=0.5`    | Moderate scroll speed; tuned during integration if it reads too fast/slow. |
| `saturation=1.4` | Punchy colors for CRT. |
| `legend=0`       | Suppresses FFmpeg's built-in axis text; the bridge draws its own metadata overlay on top. |

Default `orientation=vertical` is used (frequency on the Y axis, time
scrolling along X). The metadata overlay keeps the existing default layout
(`visualizerLayoutFor` non-stereo branch: lines at y = 24/48/72), which sits
over the high-frequency top band — consistent with the other full-frame modes.

### Required filters

`RequiredVisualizerFilters(VisualizerModeSpectrumWaterfall)` returns
`["showspectrum"]`. `format` and `scale` are ubiquitous and are not gated by
existing modes (e.g. `stereo_scope` gates only `avectorscope` despite using
`format=rgba`), so `showspectrum` is the only special filter to gate.
`showspectrum` is present in the mainstream FFmpeg build shipped in the Docker
image (Alpine `ffmpeg` package). If a user's custom FFmpeg lacks it,
`CheckVisualizerFilters` fails the mode fast with a clear error — the existing
contract.

### Icon

Add a `viz-icon--waterfall::before` rule to
`internal/chassis/static/chassis.css` near the other `viz-icon--*` rules
(stacked horizontal bands suggesting a scrolling waterfall). The chassis
template already renders `viz-icon--{{.IconKind}}`, so no template change is
needed beyond the new button entry.

## Part C — Remove `radial_spectrum` preview button + preview mechanism (full cleanup)

`radial_spectrum` is the only button with `IsPreview: true`. Per the chosen
scope, the button **and** all now-orphaned preview-button scaffolding are
removed, leaving zero preview concept in the chassis. A future deferred mode
would re-introduce a (simpler) scaffold at that time.

Removed:

- The `radial_spectrum` button entry in `internal/chassis/data.go`.
- The `IsPreview` field on `VisualizerButton` and its doc comment in
  `internal/chassis/data.go` (the comment that singles out `radial_spectrum`
  as a deferred preview).
- The `{{if .IsPreview}} … {{else}} … {{end}}` branch in
  `internal/chassis/templates/visualizer-bank.html`, collapsing to a single
  live-button render path.
- The `viz-btn--preview` rules and the now-dead `viz-icon--radial` rule in
  `internal/chassis/static/chassis.css`.
- The `isPreview()` helper in `internal/chassis/static/visualizer-bank.js` and
  its two call sites (`setMode` active check simplifies to
  `btn.dataset.viz === mode`; `bindClicks` no longer skips preview buttons).

Behavior that intentionally stays (independent of the button):

- `isSupportedVisualizerMode("radial_spectrum")` still returns false, and the
  visualizer POST handler still rejects it as an unsupported mode. The
  existing handler/validator tests in `internal/chassis/visualizer_test.go`
  remain valid (they now cover generic unknown-mode rejection); test names
  referencing "radial"/"deferred" may be left as-is or renamed for clarity,
  at the implementer's discretion.
- `idleSnapshot` still falls an unknown configured mode back to
  `retro_analyzer`. `TestIdleSnapshot_RadialPreviewModeFallsBack` stays valid
  as a generic unknown-mode-fallback test; rename to drop the "preview"
  framing is optional.

## Consolidated Touchpoints

| File | neon_grid → spectrum_waterfall | radial / preview cleanup |
|------|--------------------------------|--------------------------|
| `internal/config/config.go` | Remove `VisualizerModeNeonGrid` const, slice entry, and validate-switch case; add `VisualizerModeSpectrumWaterfall` in all three. | — |
| `internal/ffmpeg/pipeline.go` | Remove `neon_grid` const + `RequiredVisualizerFilters` case + `visualizerCoreGraph` case; add the three for `spectrum_waterfall`. | — |
| `internal/chassis/data.go` | Replace the NEON GRID button with the WATERFALL button (`IconKind: "waterfall"`) in the same slot. | Remove the RADIAL button and the `IsPreview` field + doc comment. |
| `internal/chassis/templates/visualizer-bank.html` | — | Collapse the `IsPreview` branch to the single live-button path. |
| `internal/chassis/static/chassis.css` | Add `viz-icon--waterfall::before`. | Remove `viz-btn--preview*` rules and `viz-icon--radial*` rule. |
| `internal/chassis/static/visualizer-bank.js` | — | Remove `isPreview()` and its two call sites. |
| `internal/config/example.toml` | Update the documented visualizer-mode comment/options. | — |
| `README.md` | Document `spectrum_waterfall`; note `neon_grid` removed (alongside the already-listed not-shipped `chiptune_equalizer`/`radial_spectrum`). | — |

## Testing

Keep `go vet`, unit, `-race`, and integration green (CI runs all four).

### Unit

- `internal/config/config_test.go`: supported-list assertions and validate
  table — drop `neon_grid`, add `spectrum_waterfall`.
- `internal/ffmpeg/pipeline_test.go`:
  - `RequiredVisualizerFilters` table — replace the `neon_grid` row with
    `{spectrum_waterfall, ["showspectrum"]}`.
  - core-graph substring table — replace the `neon_grid` row with a
    `spectrum_waterfall` row asserting the graph contains
    `showspectrum=`, `slide=scroll`, `fscale=log`, and `color=intensity`.
- `internal/chassis/chassis_test.go`:
  - Expected visualizer-button list (currently 9 rows: 8 live modes + the
    radial preview) — remove the `neon_grid` and `radial_spectrum` rows and
    add the `WATERFALL` row, leaving 8 rows (8 live modes, no preview button).
  - `TestChassisCSS_TransportNarrowLayoutAndPreviewDisabled` — drop the
    `viz-btn--preview:disabled` / `cursor: not-allowed;` preview assertions;
    keep the transport/narrow-layout assertions. Rename if the "preview"
    framing no longer fits.
  - JS runtime-contract test — remove `"viz-btn--preview"` from the asserted
    strings.
  - Page-render accessibility test — remove the
    `viz-btn--preview … disabled` assertion.
- `internal/chassis/visualizer_test.go`: unchanged (radial_spectrum still
  rejected as unsupported); optional test renames only.

### Integration (`go test -tags=integration`)

- `tests/integration/visualizer_modes_test.go`: replace the
  `{"neon grid", VisualizerModeNeonGrid, …}` case with
  `{"waterfall", VisualizerModeSpectrumWaterfall, false, false}`. This spawns
  real `ffmpeg`, gates on `showspectrum` availability via
  `skipIfVisualizerFiltersMissing`, and asserts a BGR frame + PCM block reach
  the data-plane pipes — the layer that catches a missing filter or a
  desynced graph.

### Verification

`go test ./...` plus `make test-integration` on a host with `ffmpeg`/`ffprobe`
on PATH.

## Appendix A — FFmpeg Visualization Catalog (future modes)

Reference menu for later expansion. The guiding lesson from `neon_grid`:
**prefer net-new primitives; treat restyles as toggles, not menu entries.**

### Tier A — Net-new primitives (each a distinct look; mode-worthy)

| Filter | Look | CRT fit | Availability | Notes |
|--------|------|---------|--------------|-------|
| `showcqt` / `showcqtbar` | Constant-Q musical spectrum — bars/sonogram aligned to musical notes & octaves, built-in coloring. The "music video" look. | Excellent (bold, musical) | Mainstream | **Top future pick.** Distinct from `showfreqs` (note-aligned, not raw FFT bins). `showcqtbar` is the simpler bars-only variant. |
| `abitscope` | PCM bit grid — lit/unlit cells per sample bit, per channel. "Data terminal" aesthetic. | Excellent (blocky, no smear) | Mainstream | Cheap, unique, ideal for low-res CRT. |
| `showcwt` | Wavelet scalogram — sharper, prettier scrolling frequency than the spectrogram. | Good | FFmpeg ≥ 6.1 only | Capability gate refuses it gracefully on older builds → safe to add, unsafe as a default. |
| `showspatial` / `aphasemeter` | Stereo field / phase-correlation meter. | OK | Mainstream | Overlaps `stereo_scope`'s territory; better as a small combined widget. |
| `ahistogram` / `adrawgraph` | Amplitude histogram / scrolling level-history line. | OK | Mainstream | Instrument-y, low drama; `adrawgraph` needs `astats`/`ametadata` feeding it. |

### Tier B — Restyle/compositing (the neon_grid trap → ship as a toggle, not a mode)

- **Phosphor persistence**: `tmix` / `feedback` / `tblend` decaying trails on
  `showwaves` / `avectorscope` for authentic CRT afterglow. Best as a global
  "CRT glow" toggle layered over any scope/waveform mode.
- **Recolor / hue-cycle**: `hue`, `lutrgb`, `colorchannelmixer`. Pure reskin
  (this is what `neon_grid` was).
- **Mirror / kaleidoscope**: `split` + `hflip`/`vflip` + `blend`.
  `raster_pulse` already does the 2-way mirror; a 4-way is a cheap extension.

### Tier C — Generative sources (cool but not audio-reactive without fragile glue → effectively out of scope)

- `geq` (plasma/interference via per-pixel math), `mandelbrot`, `life`,
  `cellauto`, `gradients`. Audio-reactivity requires hand-wired audio-derived
  expressions — brittle, high-effort. Same spirit as the existing exclusion of
  projectM/MilkDrop and browser rendering.
