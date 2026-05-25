# Visualizer Metadata Overlay Design

Date: 2026-05-25
Status: Brainstormed; awaiting implementation plan
Scope: Refine the FFmpeg-rendered music visualizer metadata overlay for Plex
and Jellyfin music casts. This changes only `internal/ffmpeg` overlay graph
construction, overlay capability checks, and tests. It does not change adapter
metadata collection, core session state, persisted configuration, browser UI,
or the receiver chassis UI.

## Goal

Make music visualizer metadata read more like a compact receiver display:

- Artist first.
- Track title below artist.
- Album below title.
- All displayed metadata in uppercase.
- Smaller text than the current overlay.
- Progress time moved to the opposite side of the screen.
- Long metadata lines scroll horizontally inside a fixed safe-width window.

The result should preserve CRT readability while giving the visualizer more
intentional hierarchy and freeing vertical space in the metadata corner.

## Current Context

Plex and Jellyfin already pass separate visualizer metadata fields:
`Title`, `Artist`, `Album`, and `Duration`. `core.Manager` maps those fields to
`ffmpeg.VisualizerSpec`, and `internal/ffmpeg/pipeline.go` builds the
`filter_complex` graph.

The current `visualizerTextLines` behavior renders:

1. title, or `Now Playing` when missing
2. artist and album joined as one `"artist - album"` line
3. elapsed/duration, when duration is known

Every line currently uses the same `drawtext` styling (`fontsize=24`) and the
same left-side text stack. Mode-aware placement already exists: analyzer and
wave modes use the upper-left block, while `stereo_scope` uses a lower-left
block.

## Decisions

1. Metadata order is `Artist`, `Track Title`, `Album`.
2. Displayed metadata is uppercased by the FFmpeg renderer only. Adapters,
   core session state, status views, and event payloads keep original casing.
3. Artist and title render at `fontsize=20`; album renders at `fontsize=18`
   with `fontcolor=0x7fdc7f`. Baseline spacing drops from `30px` to `24px`.
   Artist and title keep the current `fontcolor=0x9dff9d`.
4. Progress time renders separately at `fontsize=16` with
   `fontcolor=0x70c870`.
5. Progress moves to the opposite side of the screen: upper-right for
   upper-left metadata modes, lower-right for lower-left metadata modes.
6. All long metadata lines are marquee-eligible. Short lines remain static.
7. Marquee movement is contained inside the metadata block's safe-width text
   window. The progress readout never scrolls.
8. If drawtext is unavailable, the existing bars/scope-only fallback remains:
   no metadata, no progress text, no session failure caused by text rendering.

## Non-Goals

- No adapter changes for Plex or Jellyfin metadata fetching.
- No core type changes.
- No browser-side visualizer rendering.
- No receiver chassis or `/ui` now-playing layout changes.
- No album art, text wrapping, multiple fonts, or user-editable visualizer
  themes.
- No live update of the active FFmpeg process when metadata changes mid-track.
- No runtime retry/rebuild path after FFmpeg process startup. Overlay capability
  checks and smoke tests should prevent known text-overlay failures before
  process start; unexpected graph bugs still surface through the existing
  session-start error path.

## Overlay Layout

`visualizerTextLines` should build separate line descriptors instead of plain
text-only rows. Each descriptor should carry at least:

- text
- role (`artist`, `title`, `album`, `progress`)
- font size
- color or opacity intent
- anchor (`metadata` or `progress`)
- marquee eligibility
- trusted-expression flag for progress

Metadata line rules:

- Trim all incoming metadata fields before rendering.
- Render artist only when non-empty.
- Render title from `md.Title`; fallback to `Now Playing` when empty.
- Render album only when non-empty.
- Uppercase artist, title, and album after fallback selection and trimming.
- Keep progress in clock format and original numeric characters.
- Omit progress when `md.Duration <= 0`, matching current behavior.

The metadata block should stay inside the square-pixel logical canvas returned
by `logicalCanvas(OutputHeight)`, before the anamorphic stretch to the output
modeline. Use these starting positions:

- For logical widths `>= 640`: side margin `24`.
- For logical widths `< 640`: side margin `16`.
- Upper-left metadata y values: `24`, `48`, `72`.
- Lower-left metadata y values: `h-88`, `h-64`, `h-40`.

The metadata text window should reserve the opposite side for progress. As a
starting rule:

```text
sideMargin      = 24 when logicalW >= 640, else 16
progressReserve = clamp(round(logicalW * 0.35), min=128, max=240)
metadataX       = sideMargin
metadataWidth   = logicalW - sideMargin - progressReserve
progressX       = w - tw - sideMargin
```

Examples:

- `320x240`: metadata window starts at `16` and is about `176px` wide.
- `384x288`: metadata window starts at `16` and is about `234px` wide.
- `640x480`: metadata window starts at `24` and is about `392px` wide.
- `768x576`: metadata window starts at `24` and is about `504px` wide.

Implementation may tune the reserve if real FFmpeg text metrics show a better
value, but metadata must not overlap the progress readout. If an unexpected
logical size cannot fit both metadata and progress with at least a `160px`
metadata window, omit progress before shrinking or overlapping metadata.

## Progress Placement

Progress is no longer part of the metadata line stack.

For modes whose metadata block is upper-left:

- metadata: left side, upper block
- progress: right side, aligned with the upper block
- progress x expression: right anchored, conceptually `w-tw-sideMargin`
- progress y expression: same y as the artist line

For modes whose metadata block is lower-left:

- metadata: left side, lower block
- progress: right side, aligned with the lower block
- progress x expression: right anchored, conceptually `w-tw-sideMargin`
- progress y expression: same y as the artist line

Progress should use the same clock expression path as today:
`%{pts\:hms} / <duration>`, still marked as trusted expression text so it is
not escaped as literal metadata.

## Marquee Behavior

All metadata lines can scroll, but only when they exceed the safe-width window.
The intended behavior for a long line is:

1. Hold still briefly.
2. Scroll left at a slow, CRT-readable speed.
3. Leave a small blank gap.
4. Loop.

Suggested starting values:

- hold: about `1.5s`
- speed: about `24 logical px/s`
- gap: about `24 logical px`

Short lines should render as static text at the window's left edge. Marquee
eligibility should be decided from rendered text width, not rune count. Use
FFmpeg drawtext metrics (`tw`/`text_w`) in the x expression to choose static
versus scrolling behavior for a line. Do not use a Go rune-count threshold for
the production behavior unless implementation investigation proves FFmpeg text
metrics cannot be used; if that happens, revise this spec before proceeding.

The marquee must be contained. A long line should not pass through the progress
readout or wander across the whole visualizer. Implementation should prefer a
small per-line text layer that is cropped to the metadata window, then overlaid
onto the visualizer frame. If implementation investigation shows that the
available FFmpeg filter set cannot support a clipped text layer reliably, the
plan should pause and revise the spec rather than shipping an uncontained
marquee.

## FFmpeg Graph Shape

Keep the production change inside `internal/ffmpeg`. Expected files are
`pipeline.go`, `capabilities.go`, and matching tests. Adapters, core request
types, config, `/ui`, and chassis files remain out of scope.

Expected implementation direction:

- Replace the single `visualizerTextY(mode, line)` plus uniform drawtext call
  with line descriptors and layout helpers.
- Continue using the logical canvas returned by `logicalCanvas(OutputHeight)`.
- Add helpers for metadata anchor positions and progress anchor positions.
- Add a right-anchored progress drawtext expression.
- Add a contained marquee path for metadata lines that can exceed the safe
  window.
- If the contained marquee path needs filters beyond the existing visualizer
  core filters and drawtext, add explicit overlay capability checks in
  `capabilities.go` rather than assuming they exist silently.
- Preserve the current no-drawtext fallback: when `DrawTextAvailable=false`,
  build the audio-reactive visualizer graph with no overlay text.

Static metadata lines may use direct `drawtext` if the implementation can keep
the code simple. Long marquee lines should use a clipped text-layer path so the
receiver-window behavior is visually bounded.

The design does not require adapters or core to pre-measure text. The FFmpeg
graph decides whether a line is long by using drawtext expressions such as
`text_w`/`tw` compared to the configured window width.

## Error Handling

- Metadata escaping continues to happen only in `internal/ffmpeg`.
- User-provided metadata must still pass through `escapeFilterText`.
- Progress remains a trusted expression and must not be escaped into literal
  text.
- If drawtext is unavailable or fails capability probing, omit all overlay text
  as today.
- If the new clipped-marquee filter path requires additional standard filters,
  `withVisualizerCapabilities` should check them before process start. Missing
  text-overlay filters disable all overlay text by setting the same effective
  overlay availability to false; they do not disable the audio-reactive
  visualizer core.
- The no-playback-failure guarantee applies to capability/preflight failures
  discovered before process start. Unexpected FFmpeg startup/runtime failures
  still follow the existing session-start/session-run error path; the test plan
  below requires a smoke test to catch bad generated overlay graphs before
  release.

## Tests

Update or add tests in `internal/ffmpeg/pipeline_test.go` for:

- Metadata order: artist, title, album.
- All-caps rendering for artist/title/album.
- Title fallback to `NOW PLAYING`.
- Blank artist and album omission.
- Font sizes: artist/title `20`, album `18`, progress `16`.
- Font colors: artist/title `0x9dff9d`, album `0x7fdc7f`, progress
  `0x70c870`.
- Progress rendered separately from the metadata stack.
- Progress right-anchor expression for upper-left and lower-left modes.
- Metadata window sizing for `320x240`, `384x288`, `640x480`, and `768x576`
  logical canvases.
- Lower-left modes keep progress lower-right, with progress y matching the
  artist line y.
- Long metadata line graph includes a contained marquee expression/path.
- Short metadata line graph remains static.
- Marquee static-versus-scroll decision uses FFmpeg text width (`tw`/`text_w`)
  rather than a Go rune count.
- `DrawTextAvailable=false` still produces bars/scope-only output with no
  metadata, no progress, and no text-layer filters.
- If additional overlay filters are capability-checked, missing overlay filters
  disable overlay text while preserving the audio-reactive core.
- When FFmpeg is available, at least one smoke test should run a generated
  marquee overlay graph from a `lavfi` audio source through one frame to
  `null`; skip this smoke test when FFmpeg is unavailable.
- Existing escaping tests still cover apostrophes, percent signs, backslashes,
  and control characters after uppercasing and descriptor changes.

Run at least:

```bash
go test ./internal/ffmpeg
```

Broader verification for implementation should include the normal project test
matrix if shared FFmpeg command construction changes beyond the visualizer path.

## Rollback

Rollback is local to `internal/ffmpeg/pipeline.go`,
`internal/ffmpeg/capabilities.go`, and their tests. Reverting the
line-descriptor layout helpers and any overlay-specific capability checks
should restore the previous title plus artist-album stack. No config, adapter,
core, or persisted data migration is involved.
