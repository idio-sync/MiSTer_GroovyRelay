# Music Visualizer Expansion Design

Date: 2026-05-21
Status: Draft for review
Scope: Add a second wave of FFmpeg music visualizer modes for Plex and
Jellyfin music casts, including three CRT arcade modes and two album-art modes.

## Goal

Expand the current music visualizer feature beyond the shipped modes:

- `retro_analyzer`
- `oscilloscope_wave`
- `stereo_scope`

The next wave should add visual styles that feel more like CRT music screens
than lab instruments while keeping rendering in the existing FFmpeg-generated
video path.

## Selected Modes

### CRT Arcade Modes

Add three arcade-style modes:

- `vu_cabinet`
- `neon_grid`
- `raster_pulse`

Do not add `chiptune_equalizer`. It overlaps too heavily with
`retro_analyzer` and the desired arcade direction can be covered by the three
selected modes without another spectrum-skin variant.

### Album-Art Modes

Add two album-art-forward modes:

- `cover_vu`
- `cover_spectrum`

Keep later album-art ideas such as `cover_wall` and `sleeve_scope` out of the
first expansion.

## Current Context

Music casts from Plex and Jellyfin already create visualizer-enabled
`core.SessionRequest` values for music items. The visualizer metadata currently
carries title, artist, album, and duration. `core.Manager` resolves the global
bridge visualizer mode, snapshots that mode per session, validates it, and maps
the request into `ffmpeg.VisualizerSpec`. `internal/ffmpeg` then builds a
mode-specific filter graph and emits raw BGR video plus PCM audio through the
existing data plane.

The current FFmpeg visualizer branch has no album-art input. Album-art modes
therefore require a metadata and asset-caching extension before they can be
implemented cleanly.

## Design Principles

1. Music playback must never fail only because visual artwork is unavailable.
2. All new modes must render through the existing FFmpeg pipeline and Groovy
   data plane.
3. Arcade modes should favor large shapes, stable layout, and CRT readability
   over dense detail.
4. Album-art modes should use cached local image files, not ad hoc FFmpeg
   network fetches.
5. Plex and Jellyfin own metadata/artwork discovery; core owns session
   validation and mode snapshotting; FFmpeg owns graph construction.
6. Existing seek, pause, replay, and same-session mode snapshot behavior must
   remain unchanged.

## Mode Behavior

### `vu_cabinet`

`vu_cabinet` is a stereo level-meter display. It should look like a physical
front panel or cabinet display:

- large left/right level meters
- peak-hold tick marks when practical
- title, artist, album, and time in a compact HUD strip
- limited color palette that remains readable on 240p/480i CRT output

The mode should be visually distinct from the existing scopes by focusing on
loudness/level rather than waveform shape, frequency, or stereo phase.

Likely FFmpeg building blocks include `showvolume`, `showwaves`, generated
backgrounds, `drawbox`, `drawgrid`, `drawtext`, and palette/format filters.
If `showvolume` is unavailable in a target FFmpeg build, implementation may
fall back to a simplified stereo waveform-meter graph as long as the visual
intent remains a cabinet-style level meter.

### `neon_grid`

`neon_grid` is the arcade sibling of `retro_analyzer`. It is still
frequency-driven, but the presentation should be more stylized:

- bright spectrum bars or bands
- a simple grid/HUD frame
- optional mirrored or stacked bar layout
- stronger color contrast than `retro_analyzer`

This mode overlaps with `retro_analyzer` at the signal level, but not at the
presentation level. `retro_analyzer` remains the plain utility analyzer;
`neon_grid` becomes the high-energy arcade analyzer.

Likely FFmpeg building blocks include `showfreqs` or `showspectrum`, generated
backgrounds, `drawgrid`, `drawbox`, `drawtext`, `hue`, and format/scale
filters.

### `raster_pulse`

`raster_pulse` is an ambient reactive mode. It does not need to be a precise
instrument. It should feel like a CRT music screen:

- mirrored reactive bands or waves
- color cycling or palette shifts
- restrained trails or persistence
- minimal metadata overlay

This mode should be clearly different from `oscilloscope_wave`: less literal
waveform, more reactive motion. The implementation should avoid fragile filter
graphs that depend on unusual FFmpeg builds.

Likely FFmpeg building blocks include `showwaves`, `showspectrum`,
`aphasemeter` only if useful, `split`, `hflip`, `vflip`, `blend`, `tmix`,
`hue`, `drawtext`, and format/scale filters.

### `cover_vu`

`cover_vu` makes album art the main visual object, with level meters around it:

- square cover art dominant in the frame
- VU meters beside or below the art
- title/artist/album text in a stable HUD area
- generated placeholder panel when artwork is missing

The layout must be designed for both NTSC and PAL modelines. The cover should
not rely on fine detail at the edge of the visible CRT area.

### `cover_spectrum`

`cover_spectrum` makes album art the center of a spectrum display:

- square cover art centered or slightly elevated
- frequency bars as a strip, frame, or lower band
- metadata in a stable text area
- generated placeholder panel when artwork is missing

This should be the album-art counterpart to `retro_analyzer` and `neon_grid`:
recognizable music-library art plus an obvious audio-reactive element.

## Album-Art Metadata And Cache

Extend visualizer metadata with `ArtworkPath`, a local artwork file path
available by the time FFmpeg builds the graph:

```go
type VisualizerMetadata struct {
    Title       string
    Artist      string
    Album       string
    Duration    time.Duration
    ArtworkPath string
}
```

The core-to-FFmpeg boundary should carry this local path rather than raw
provider URLs.

### Plex

Plex metadata fetches should decode artwork candidates from PMS track metadata.
Preferred candidates:

1. track `thumb`
2. album/parent thumb where available
3. artist/grandparent thumb where available

Resolve relative PMS artwork paths against the Plex server URL and authenticate
them with the Plex token. Download to a per-session or cache-scoped local image
file before starting FFmpeg.

### Jellyfin

Jellyfin metadata should derive a primary image URL from the item ID first:

```text
/Items/{itemId}/Images/Primary
```

Include the API key or Jellyfin auth material using the same safe request path
the adapter already uses for metadata. Download to a local image file before
starting FFmpeg. If track-level primary art is missing, implementation may look
for album/parent art if that metadata is available.

### Cache Rules

Artwork download is best-effort:

- network failure does not block playback
- HTTP non-2xx does not block playback
- unsupported image format does not block playback
- oversized images are rejected or resized before FFmpeg use
- cache cleanup follows the existing data directory conventions

The first implementation can keep the cache simple: session-scoped files under
the bridge data directory with cleanup through the session `OnStop` path or a
startup reaper. A content-addressed persistent cache can be added later if
repeated downloads become a problem.

## FFmpeg Graph Shape

The existing visualizer graph builder should grow mode-specific branches. For
album-art modes, the command shape gains a second visual input:

```text
ffmpeg -i <audio-url> -loop 1 -i <artwork-path>
  -filter_complex "<audio-reactive branch>; <image scale/crop branch>; <overlay/composite branch>"
  -map "[visualizer_video]" ...
  -map 0:a:0 ...
```

The exact input ordering is implementation detail, but tests should pin it once
chosen so audio mapping remains stable. Audio-only DASH paths already use a
second audio input in some command shapes, so implementation must avoid
ambiguous input indexing by centralizing visualizer input map construction.

Album-art filters should:

- scale/crop art to a square region
- preserve aspect ratio
- avoid stretching cover art
- composite the art with generated backgrounds and audio-reactive elements
- finish with the same output cadence, output size, and `bgr24` conversion as
  current visualizer modes

If artwork is absent, the graph should use a generated placeholder panel rather
than adding the image input.

## Configuration And UI

Extend the existing `bridge.visualizer.mode` enum with the new modes:

- `retro_analyzer`
- `oscilloscope_wave`
- `stereo_scope`
- `vu_cabinet`
- `neon_grid`
- `raster_pulse`
- `cover_vu`
- `cover_spectrum`

Mode changes continue to use the existing visualizer apply behavior: they are
persisted, update manager bridge state, and apply to the next music cast or to
whatever restart is triggered by stronger mixed-scope changes.

No separate album-art enable toggle is needed in this wave. Album-art modes
implicitly request artwork; when artwork is unavailable they use placeholders.

## Error Handling

- Unsupported mode values fail bridge config validation and UI save validation.
- Missing required FFmpeg filters fail before process start with a clear error.
- Missing optional `drawtext` degrades to visual-only mode where practical.
- Missing album art degrades to generated placeholder art.
- Artwork fetch/decode/cache errors are logged at debug or warning level and do
  not fail music playback.
- If a mode-specific graph cannot be built for reasons unrelated to artwork,
  session startup fails with an explicit error rather than silently switching
  to a different mode.

## Testing

Add or update tests for:

- config defaults and validation for all added mode names
- Bridge UI enum rendering and form parsing for the new modes
- BridgeSaver invalid mode rejection before persisting
- manager mapping from core modes to FFmpeg modes
- same-session replay, seek, and resume preserving the snapshotted mode
- FFmpeg required-filter lists for each new mode
- FFmpeg command graph shape for each new mode
- album-art modes adding an artwork input only when `ArtworkPath` is present
- album-art modes using generated placeholders when `ArtworkPath` is empty
- Plex metadata extraction for title, artist, album, duration, and artwork
- Jellyfin metadata extraction for title, artist, album, duration, and artwork
- artwork download/cache failure falling back without blocking playback

The final verification remains `go test ./...`.

## Implementation Phasing

### Phase 1: Mode Constants And Validation

Add the selected mode names through config, core, UI, manager mapping, FFmpeg
mode definitions, and required-filter declarations.

### Phase 2: CRT Arcade Graphs

Implement `vu_cabinet`, `neon_grid`, and `raster_pulse` without artwork. These
should be independently testable using the current visualizer metadata path.

### Phase 3: Artwork Plumbing

Extend visualizer metadata and adapter metadata fetches to include local cached
artwork paths. Keep all artwork failures non-fatal.

### Phase 4: Album-Art Graphs

Implement `cover_vu` and `cover_spectrum` using the shared artwork input and
placeholder fallback.

### Phase 5: Documentation And Polish

Update README and example config with the supported modes. Keep the README
clear that `radial_spectrum` and `chiptune_equalizer` are not shipped modes.

## Out Of Scope

- `chiptune_equalizer`
- `radial_spectrum`
- projectM or MilkDrop preset playback
- browser-side visualizer rendering
- live switching the currently running visualizer mode
- per-adapter or per-track visualizer preferences
- persistent content-addressed album-art cache unless needed during
  implementation
