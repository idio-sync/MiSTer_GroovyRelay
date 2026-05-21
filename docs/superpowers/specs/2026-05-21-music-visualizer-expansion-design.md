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

FFmpeg building blocks: `showvolume` (required), `drawbox`, `drawgrid`, and
optional `drawtext`. See the required-filters table below; `showvolume` is
non-optional for this mode.

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

FFmpeg building blocks: `showfreqs` (required), `drawgrid`, `hue`, optional
`drawtext`. See the required-filters table below.

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

FFmpeg building blocks: `showwaves` (required), `split`, `hflip`, `blend`,
optional `hue` and `drawtext`. See the required-filters table below.
`aphasemeter` and `tmix` are not used by this mode.

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
available by the time FFmpeg builds the graph. Add the field to both
`core.VisualizerMetadata` and `ffmpeg.VisualizerMetadata`, preserving the
existing core-to-FFmpeg mapping boundary:

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
provider URLs. The artwork download happens entirely inside the adapter,
before `Manager.StartSession` is called, using the adapter's own HTTP client.
This mirrors the existing `SubtitlePath` precedent (see `internal/core/types.go`)
and preserves the CLAUDE.md invariant that `Manager.mu` is never held across
network I/O.

### Path And URL Validation Contract

Artwork plumbing crosses an adapter / FFmpeg trust boundary. All five of the
following constraints apply to every adapter that sets `ArtworkPath`:

1. **Allowed cache root.** Artwork files must live under
   `<bridge.data_dir>/artwork-cache/`. Adapters create files only via a shared
   `internal/artworkcache` helper that returns paths anchored under this root.
2. **Validation at the core-to-FFmpeg boundary.** Before FFmpeg builds the
   graph, `ArtworkPath` is canonicalised with `filepath.EvalSymlinks` and
   rejected unless the result is a descendant of the canonical cache root. A
   rejected path is treated as missing — the graph falls through to the
   placeholder branch and a warning is logged. Provider metadata can never
   inject an arbitrary local path into the FFmpeg command.
3. **Origin pinning for artwork URLs.** Plex `thumb` values may be relative
   paths or absolute URLs (PMS forwards upstream URLs verbatim for some
   providers). Adapters MUST confine artwork fetches to the configured server
   origin: scheme, host, and effective port must match after canonical URL
   parsing. Default ports count as their scheme defaults (`https` = 443,
   `http` = 80). An absolute URL whose origin does not match the configured
   Plex/Jellyfin server is dropped and the graph uses the placeholder branch.
   This prevents the bridge from issuing authenticated outbound HTTP to
   attacker-controlled hosts or downgrading credentials from HTTPS to HTTP.
4. **Token append after origin pinning.** The Plex token (or Jellyfin API key)
   may only be attached to a request URL after the request has been
   re-anchored to the configured server origin. Composing this rule with (3)
   guarantees the token cannot leak to a third-party host. URL logging for
   artwork fetches must use a shared redaction helper, for example
   `artworkcache.RedactURL`, that strips userinfo and redacts token-bearing
   query keys such as `X-Plex-Token`, `api_key`, `X-Emby-Token`,
   `AccessToken`, and `token` before any URL is logged. Do not rely on
   `internal/core/manager.go`'s unexported `redactURL`; adapter and artwork
   cache packages cannot call it directly.
5. **Decode bounds.** Decoded images larger than 4096×4096 pixels or 8 MiB on
   disk are rejected (decode-bomb mitigation). Successful decodes are resized
   to fit the visible CRT area before FFmpeg use; the exact target dimensions
   are chosen by the mode builder.

### Plex

Plex metadata fetches decode artwork candidates from PMS track metadata, in
preference order:

1. track `thumb`
2. album/parent thumb where available
3. artist/grandparent thumb where available

For each candidate the adapter:

1. Parses the value. Relative paths are joined against the configured Plex
   server URL. Absolute URLs are accepted only if their scheme, host, and
   effective port match the configured Plex server; otherwise the candidate is
   discarded.
2. Appends the `X-Plex-Token` query parameter after the URL is anchored to
   the Plex origin (never before).
3. Issues a GET with a 2 s bounded timeout (matching the existing
   `musicMetadataForPlay` timeout for parity).
4. On 2xx, decodes and validates the image against the decode bounds, then
   writes it under the artwork cache root and stores the resulting path on
   `SessionRequest.Visualizer.Metadata.ArtworkPath`.

Any failure — timeout, cancellation, network error, non-2xx, decode error,
oversize rejection — is logged at debug level and `ArtworkPath` is left empty.
Playback starts with the placeholder branch in that case.

### Jellyfin

Jellyfin metadata derives a primary image URL from the item ID first:

```text
/Items/{itemId}/Images/Primary
```

The URL is built against the configured Jellyfin server origin and authenticated
using the same `api_key` material the adapter already threads through metadata
requests. If track-level primary art is missing, the adapter may look for
album/parent art if the metadata path exposes it. The same 2 s timeout, decode
bounds, fail-open behaviour, and origin pinning rules from the validation
contract apply.

### Cache Rules

Artwork download is best-effort:

- network failure does not block playback
- HTTP non-2xx does not block playback
- unsupported image format does not block playback
- oversized images are rejected per the decode bounds above
- cache cleanup follows the existing data directory conventions

The cache layout is session-scoped: each successful download produces one file
under `<bridge.data_dir>/artwork-cache/`. A startup reaper removes files in
that directory whose `mtime` is older than 24 h. The reaper runs once during
adapter startup (after config load, before HTTP bind), failures are logged at
warn and ignored, and the directory is created on demand. The reaper lives in
`internal/artworkcache` and is called from each adapter's startup so it does
not violate the no-cross-adapter-imports invariant. A content-addressed
persistent cache can be added later if repeated downloads become a problem.

Artwork cleanup must compose with existing adapter cleanup. Plex and Jellyfin
already attach `OnStop` handlers for transcode cleanup and reporting; artwork
cleanup wraps the existing handler. The wrapper has a precise contract:

```go
// WithCleanup returns an OnStop closure that invokes original and
// removes path, with these guarantees:
//
//   - artwork removal runs even if original panics (defer ordering),
//   - empty path is a no-op,
//   - Remove tolerates os.IsNotExist so the returned closure is safe
//     to invoke more than once,
//   - if original is nil, only artwork removal runs.
//
// Adapters call artworkcache.WithCleanup around their existing OnStop rather
// than assigning OnStop directly from multiple sites.
func WithCleanup(path string, original func(reason string)) func(reason string) {
    return func(reason string) {
        defer Remove(path)
        if original != nil {
            original(reason)
        }
    }
}
```

`artworkcache.Remove` mirrors `removeSubtitleFile` in
`internal/core/manager.go`: empty paths are a no-op, `os.IsNotExist` is
swallowed, all other errors are logged at debug.

## FFmpeg Graph Shape

The existing visualizer graph builder should grow mode-specific branches. For
album-art modes, the command shape gains a second visual input:

```text
ffmpeg -i <audio-url> -loop 1 -i <artwork-path>
  -filter_complex "<audio-reactive branch>; <image scale/crop branch>; <overlay/composite branch>"
  -map "[visualizer_video]" ...
  -map 0:a:0 ...
```

### Input-Indexing Contract

- input `0` is always the primary media input
- when `AudioInputURL` is empty, visualizer audio comes from `0:a:0`
- when `AudioInputURL` is present, input `1` is the separate audio input and
  visualizer audio comes from `1:a:0`
- when `ArtworkPath` is present and valid (passes the validation contract
  above), artwork is appended after media/audio inputs: index `1` without
  `AudioInputURL`, or index `2` with `AudioInputURL`
- when `ArtworkPath` is empty or rejected, no image input is appended and the
  graph uses the placeholder branch (see below)

Implementation centralises these decisions in two helpers:

```go
// visualizerAudioInputMap returns "[0:a]" or "[1:a]" depending on AudioInputURL.
func visualizerAudioInputMap(s PipelineSpec) string

// visualizerArtworkInput returns the input-args extension and the filter-graph
// label for the artwork branch. When ArtworkPath is empty or invalid, args is
// nil and label is "" — mode builders interpret this as the placeholder case
// and emit a generated panel instead of an image input.
func visualizerArtworkInput(s PipelineSpec) (args []string, label string)
```

Mode builders never reference literal input indexes; they consume these
helpers as the single source of truth so that the input contract above and
the constructed argv stay in lockstep.

### Cadence For The Image Branch

The existing visualizer graph rate-locks the audio-reactive branch to
`OutputFpsExpr` (default `60000/1001`) via the terminal `fps=...,format=bgr24`
clause in `buildVisualizerFilterChain`. The image branch produced by a
`-loop 1 -i <artwork>` input is not implicitly rate-limited — without an
explicit `fps=` clause the loop emits as fast as it can and starves audio /
desyncs `overlay`/`blend`/`tmix` joins. Cover-art graphs MUST therefore start
the image branch with:

```text
[<artwork-label>]fps=<OutputFpsExpr>,format=bgr24,scale=...:flags=lanczos[cover]
```

so the image and audio branches share one cadence before any join.

### Album-Art Filter Requirements

Album-art filters should:

- scale/crop art to a square region within the safe visible area
- preserve aspect ratio (no stretch)
- composite the art with generated backgrounds and audio-reactive elements
- finish with the same output cadence, output size, and `bgr24` conversion as
  current visualizer modes

### Placeholder Branch

When `ArtworkPath` is empty or rejected, cover-art modes MUST still produce a
recognisable layout. The placeholder branch uses a generated solid-color panel
(via `color=` source) with the same dimensions the image branch would have
produced. The placeholder is produced inside the filter graph rather than as
an extra `-i` input, so the input index contract collapses to "no image input
appended". Both `cover_vu` and `cover_spectrum` MUST share one placeholder
helper so the two modes render identical panels when artwork is absent.

### Required-Filters Table

`RequiredVisualizerFilters` in `internal/ffmpeg/pipeline.go` gates plane spawn
through `CheckVisualizerFilters`. Each new mode adds the following entries.
Implementation MUST fail-fast before plane spawn if any required filter is
missing in the runtime ffmpeg build:

| Mode             | Required filters                                       |
|------------------|--------------------------------------------------------|
| `vu_cabinet`     | `showvolume`, `drawbox`, `drawgrid`                    |
| `neon_grid`      | `showfreqs`, `drawgrid`, `hue`                         |
| `raster_pulse`   | `showwaves`, `split`, `hflip`, `blend`                 |
| `cover_vu`       | `showvolume`, `overlay`, `scale`                       |
| `cover_spectrum` | `showfreqs`, `overlay`, `scale`                        |

`drawtext` remains optional across all modes (current behaviour): when
unavailable, metadata strings are dropped and the visual element still
renders. `showvolume` for `vu_cabinet`/`cover_vu` is required, not optional —
the earlier "fall back to a simplified waveform-meter" suggestion is removed
to keep `RequiredVisualizerFilters` a single-valued table. `aphasemeter` is
not in the required set for any mode and is not used by `raster_pulse`.

## Configuration And UI

Two enums live in the codebase and must be kept distinct:

- **Go constant set** (`internal/config/config.go` plus
  `internal/ffmpeg/pipeline.go`) — the canonical list of mode IDs the manager,
  saver, and graph builder recognise. New modes are added here first.
- **UI-exposed enum** (`internal/ui/bridge_fields.go`) — the subset surfaced as
  selectable options in the Settings UI and emitted in `config.example.toml`.
  A mode appears here only when its FFmpeg graph and validation path are
  implemented end-to-end.

After Phase 2 (album-art modes shipped) the Go constant set is:

- `retro_analyzer`
- `oscilloscope_wave`
- `stereo_scope`
- `vu_cabinet`
- `neon_grid`
- `raster_pulse`
- `cover_vu`
- `cover_spectrum`

Mode changes continue to use the existing visualizer apply behaviour: they are
persisted, update manager bridge state, and apply to the next music cast or to
whatever restart is triggered by stronger mixed-scope changes.

No separate album-art enable toggle is needed in this wave. Album-art modes
implicitly request artwork; when artwork is unavailable they use placeholders.

`cover_vu` and `cover_spectrum` MUST NOT appear in the UI-exposed enum or in
`config.example.toml` until Phase 2 lands (Phase 2 is the merged
artwork-plus-album-art phase; see Implementation Phasing below).

### Unknown-Mode Handling Across Phase Rollbacks

A config file written under a newer phase (e.g. `cover_vu` selected) might be
read by an older binary. `NormalizeVisualizerMode` in
`internal/config/config.go` today returns its input verbatim for non-empty
values (only empty falls back to `retro_analyzer`). That behaviour MUST be
preserved: unknown modes pass through normalisation unchanged and are caught
by `ValidateVisualizerMode` at config-load. The validator MUST reject unknown
modes with a clear error rather than silently coercing them — an operator who
downgrades the binary should see a hard config error, not a silently lost UI
selection.

## Error Handling

- Unsupported mode values fail bridge config validation and UI save validation.
- Missing required FFmpeg filters fail before process start with a clear error.
- Missing optional `drawtext` degrades to visual-only mode where practical.
- Missing album art degrades to generated placeholder art.
- Artwork fetch/decode/cache errors are logged at debug or warning level and do
  not fail music playback.
- Artwork path, origin, token, and decode-bound rules are enumerated in the
  "Path And URL Validation Contract" section above. Any violation maps to "no
  artwork" and falls through to the placeholder branch — it never aborts
  music playback or surfaces to the operator.
- If a mode-specific graph cannot be built for reasons unrelated to artwork,
  session startup fails with an explicit error rather than silently switching
  to a different mode.

## Testing

### Unit Tests

Add or update unit tests for:

- config defaults and validation for all added mode names
- `NormalizeVisualizerMode` preserving unknown values verbatim
- `ValidateVisualizerMode` rejecting unknown modes with a clear error
- Bridge UI enum rendering and form parsing for the new modes (UI-exposed
  subset only; album-art modes excluded until Phase 2)
- BridgeSaver invalid mode rejection before persisting
- manager mapping from core modes to FFmpeg modes
- same-session replay, seek, and resume preserving the snapshotted mode
- FFmpeg required-filter table values for each new mode
- FFmpeg command argv shape for each new mode (CRT and album-art)
- `visualizerArtworkInput` returning empty args/label when `ArtworkPath` is
  empty, and non-empty args/label when set
- album-art modes adding an artwork input only when `ArtworkPath` is present
- album-art modes using the shared placeholder helper when `ArtworkPath` is
  empty or fails validation
- album-art command input mapping when both `AudioInputURL` and `ArtworkPath`
  are present
- path validation: rejecting `ArtworkPath` values that resolve outside the
  artwork cache root (including symlink escapes via `EvalSymlinks`)
- origin pinning: Plex/Jellyfin adapters discarding absolute artwork URLs
  whose scheme, host, and effective port do not match the configured server
- token-append ordering: tokens are never attached to URLs that still point
  at a non-server origin
- URL redaction: `artworkcache.RedactURL` removes userinfo and token-bearing
  query keys before log sites receive artwork URLs
- Plex metadata extraction for title, artist, album, duration, and artwork
- Jellyfin metadata extraction for title, artist, album, duration, and artwork
- artwork download/cache failure falling back without blocking playback
- artwork fetch 2 s timeout falling back without blocking playback
- decode-bound enforcement: oversize image rejected, playback continues with
  placeholder
- `artworkcache.WithCleanup` invoking both artwork removal and the wrapped `OnStop`
  even when the wrapped handler panics
- repeated invocation of the wrapped `OnStop` being a no-op on the second call
- startup reaper removing files older than 24 h in
  `<DataDir>/artwork-cache/` and ignoring failures

### Integration Tests

The CRT arcade graphs and album-art graphs introduce new filter chains that
unit tests over argv alone do not exercise. Under `tests/integration/`
(`go test -tags=integration`), add one test per new mode that:

1. Spawns the real `ffmpeg` and `ffprobe` binaries (PATH-resolved, mirroring
   `make test-integration` preconditions).
2. Feeds a 1 s lavfi-generated stereo audio source plus, for album-art modes,
   a temporary PNG or JPEG fixture generated by ffmpeg and passed through the
   production `ArtworkPath` / `-loop 1 -i <artwork-path>` path.
3. Asserts at least one BGR video frame and at least one s16le PCM block reach
   the data-plane output pipes.

Without this layer a missing filter or a desynced image-branch cadence will
not be caught by CI until a user opens a real cast.

### Verification

The final verification remains `go test ./...` plus `make test-integration`
on a host with `ffmpeg`/`ffprobe` on `PATH`. CI already runs both.

## Implementation Phasing

### Phase 1: CRT Arcade Modes End To End

Ship the three CRT arcade modes (`vu_cabinet`, `neon_grid`, `raster_pulse`)
end to end. This phase adds each mode through config, core, UI, manager
mapping, FFmpeg mode definitions, the required-filter table, and the concrete
FFmpeg graph in one change set. A mode is not exposed in the UI, accepted by
config validation, or returned from `RequiredVisualizerFilters` until its graph
and tests exist.

No artwork plumbing lands in Phase 1. Each graph ships with unit tests for
argv shape and one integration test that spawns ffmpeg.

### Phase 2: Artwork Plumbing + Album-Art Graphs

Artwork plumbing and cover-art graphs ship together. Shipping artwork plumbing
without consumers would land dead code (an artwork cache filling during every
music cast for no rendering benefit), so this phase ships:

- `internal/artworkcache` package: cache root creation,
  `artworkcache.WithCleanup` helper, `artworkcache.Remove` helper,
  `artworkcache.RedactURL` helper, startup reaper.
- `ArtworkPath` field on both `core.VisualizerMetadata` and
  `ffmpeg.VisualizerMetadata`.
- Plex and Jellyfin adapter changes to download artwork before
  `StartSession`, honouring the path & URL validation contract.
- `visualizerArtworkInput` helper in `internal/ffmpeg`.
- `cover_vu` and `cover_spectrum` mode IDs added to the Go constant set, the
  required-filter table, the UI-exposed enum, and the example config.
- Cover-art FFmpeg graphs with placeholder fallback when `ArtworkPath` is
  empty or rejected.

### Phase 3: Documentation And Polish

Update README and any non-example narrative docs to reflect the supported
modes. `config.example.toml` is updated in the same phase that exposes each
mode publicly, so Phase 3 does not make another example-config change. The
README also briefly notes which previously discussed mode IDs are intentionally
not shipped (`chiptune_equalizer`, `radial_spectrum`) so users searching past
design docs do not assume they were dropped silently.

## Out Of Scope

- `chiptune_equalizer`
- `radial_spectrum`
- projectM or MilkDrop preset playback
- browser-side visualizer rendering
- live switching the currently running visualizer mode
- per-adapter or per-track visualizer preferences
- persistent content-addressed album-art cache unless needed during
  implementation
