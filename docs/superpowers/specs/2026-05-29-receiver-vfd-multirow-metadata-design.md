# Receiver VFD Multi-Row Metadata — Design

**Date:** 2026-05-29
**Status:** Approved (design); pending implementation plan
**Surfaces:** receiver chassis web UI (VFD), CRT visualizer (FFmpeg overlay)

## Goal

Today the receiver's VFD shows only a single title line plus a synthesized
`SOURCE · position / duration` marquee. Expand the VFD to display **up to
three rows of real media metadata**, repurposed per media type, in the
established 80s/90s AV-receiver visual language (DSEG14 segmented font,
CRT/scanline screen, marquee scrolling for overflow). Bring richer
metadata through the whole stack — adapter → core → SSE → DOM — and keep
the CRT visualizer (music-only) consistent with the VFD.

Examples the design must cover:

- **Music** → track title, artist, album
- **TV episode** → show title, episode title, season/episode number
- **Movie** → title, year
- **YouTube** → video title, channel, upload date
- **m3u8 / live stream** → channel/stream title, provider, group
- **Saved cast / preset** → stream title, provider
- **Idle** → `STANDBY` + status hint

## Visual decisions (locked via mockups)

- **Layout A — stacked segments, no labels.** Three left-aligned DSEG14
  rows of decreasing size (≈23 / 14 / 12 px): Primary (bold), Secondary,
  Tertiary (dim). Hierarchy is pure size/brightness — no per-row text
  labels. This is the most authentic "tape-deck / CD player" readout.
- **TV ordering: show-first.** The recognizable brand (show) is the big
  Primary line; the episode title is Secondary; `S04E05 · 2008` is
  Tertiary.
- **Music ordering: title-first.** Track title is Primary, artist
  Secondary, album Tertiary — the dominant now-playing convention.
- **Rationale for the asymmetry** (TV leads with the *container*, music
  leads with the *track*): a show is the recognizable brand and the
  episode is one of many, so the series name is the headline; a song's
  identity *is* the track title, so it leads. This is intentional, not an
  inconsistency. Adapters that populate `DisplayMetadata` should carry a
  short comment noting this ordering rule.
- **CRT visualizer realigned to title-first** to match the VFD.
- The big clock / queue / uptime **right panel is unchanged**.
- Empty tiers **collapse** (row hidden); the left block stays vertically
  centered, so two-tier items (Movie, Saved cast) read cleanly.

## Architecture

Two display surfaces, one source of truth carried as **pre-formatted
strings** (the adapter owns all media-type formatting; core never
interprets it — consistent with how `SessionRequest.Title` already works
and with the invariant that **core imports no adapter package**).

1. **Web VFD** (chassis page) — renders three stacked segmented rows for
   *every* media type.
2. **CRT visualizer** (FFmpeg drawtext overlay) — **music/audio only**;
   keeps role-based `Title/Artist/Album` rendering, reordered title-first.
   Video casts show the real picture, so there is no overlay for them.

### New core type

```go
// internal/core/types.go
type DisplayMetadata struct {
    Primary   string // headline row (biggest)
    Secondary string // attribution row
    Tertiary  string // detail row (dim)
}
```

- Added as a field on `SessionRequest`.
- Surfaced on `StatusHomeView` as `Display DisplayMetadata`.
- **Backward-compatible fallback:** when `Display` is empty, the chassis
  uses the existing `Title` as `Primary`, so any not-yet-updated path
  still renders something.
- The existing `VisualizerMetadata{Title,Artist,Album,Duration,ArtworkPath}`
  is unchanged and continues to drive the CRT overlay. Music adapters set
  **both** the tiers (for the VFD) and the structured fields (for the
  CRT) from the same source values.

## Tier-mapping contract

Each adapter composes its own three tier strings at the point where it
builds `core.SessionRequest`. This table is authoritative:

| Source / form | Primary (big) | Secondary | Tertiary (dim) |
|---|---|---|---|
| Music (Plex / Jellyfin / AUX) | Track title | Artist | Album |
| TV episode (Plex / Jellyfin) | **Show title** | Episode title | `S04E05 · 2008` |
| Movie (Plex / Jellyfin) | Title | Year | *(empty)* |
| YouTube (URL / streams via yt-dlp) | Video title | Channel | Upload date `2024-03-15` |
| Stream / m3u8 live (streams direct) | Channel/stream title | Provider | Group (fallback: description) |
| Saved cast / preset (streams) | Stream title | Provider | *(empty)* |
| Direct URL | Filename | `URL` | *(empty)* |
| DLNA | DIDL title | *(empty)* | *(empty)* |
| Torrent | Cleaned filename | *(empty)* | *(empty)* |
| AUX | Input name | `AUX` | *(empty)* |
| Idle | `STANDBY` | status hint | *(empty)* |

Formatting notes:
- Season/episode renders zero-padded as `S%02dE%02d`; year appended as
  ` · %d` only when known. If only one of season/episode is known, render
  what is available; if neither, the tertiary is the year alone (or
  empty).
- Upload date converts yt-dlp's `YYYYMMDD` to ISO `YYYY-MM-DD`.
- **Casing boundary:** adapters store **natural-case** strings in
  `DisplayMetadata`; the chassis renders them uppercase via CSS
  (`text-transform: uppercase` on the tier rows). Uppercasing is a
  presentation concern and happens once, in one place — not in adapters.
  (The CRT visualizer continues to uppercase in code via
  `strings.ToUpper`, since drawtext has no CSS layer; both surfaces look
  the same.)

## Adapter population

| Adapter | Work | Notes |
|---|---|---|
| **Plex music** | Trivial | `Artist`/`Album` already fetched for the visualizer ([transcode.go](../../../internal/adapters/plex/transcode.go)); set the three tiers from the same values. |
| **Plex TV/movie** | **Main new work** | Add a bounded PMS metadata fetch reusing the music-metadata path (`MusicMetadataFor`/`pmsMediaContainer` in [transcode.go](../../../internal/adapters/plex/transcode.go)). Extend the XML container to read `type` (movie/episode), `grandparentTitle` (show), `title` (episode), `index`/`parentIndex` (S·E), `year`. **Wrap the call in a 2s `context.WithTimeout`** mirroring the existing music lookup at [companion.go:250](../../../internal/adapters/plex/companion.go#L250) — independent of the shared 10s `plexHTTPClient`. On fetch failure/timeout, fall back to `Primary = controller title` (no regression vs. today). |
| **Jellyfin** | Low–moderate | The needed fields (`SeriesName`, `IndexNumber`, `ParentIndexNumber`, `ProductionYear`) are **not yet parsed** — neither `playbackInfoResponseDTO.Item` nor `itemMetadataDTO` ([playback.go:73-79, 115-122](../../../internal/adapters/jellyfin/playback.go#L73)) declares them. Work is **struct extension, not a new request**: add the fields to `itemMetadataDTO` (Jellyfin's `/Items/{id}` already returns them), ensure `FetchItemMetadata` runs for the video path (today it's an audio-hint/best-effort call), thread them into `PlaybackInfoResult`/`ItemMetadataResult`, and map by `ItemType` (Audio/Movie/Episode). Artist/Album already extracted. Fall back to `Name` when series/index fields are absent. |
| **URL / YouTube** | Low | yt-dlp's `--dump-json` output **already contains** `channel`/`uploader` and `upload_date`, but the resolver does not currently unmarshal them. Add the three keys to the **raw JSON struct** AND to the `Resolution` struct ([ytdlp/resolver.go:53-60, 144-154](../../../internal/adapters/url/ytdlp/resolver.go#L53)); format the date `YYYYMMDD → YYYY-MM-DD`. Direct URLs: `Primary = filename`, `Secondary = "URL"`. |
| **Streams** | Low–moderate | Split today's `"Provider / Channel"` title into tiers; add channel group (fallback description) as tertiary. Covers m3u8 live + saved casts/presets. yt-dlp-resolved stream items reuse the URL adapter's parsed channel where available. |
| **DLNA** | Low | Wire the already-parsed DIDL title → `Primary`. |
| **Torrent / AUX** | Trivial | Primary from existing title; AUX `Secondary = "AUX"`. |

## Core plumbing

- `Manager` already stores the whole `SessionRequest`; no storage change.
- `StatusHomeView` gains `Display DisplayMetadata`, populated next to
  `Title`/`Source` in `StatusHomeView()` (internal/core/manager.go).

## Chassis rendering

**Data ([data.go](../../../internal/chassis/data.go), [session.go](../../../internal/chassis/session.go)):**
- `VFDData`: replace `Title` + `Marquee` with `Primary` / `Secondary` /
  `Tertiary`; keep `SystemTime`, `QueueCurrent/Total`, `Uptime`.
- `snapshotFromStatusView` live branch sets the three tiers from
  `view.Display`, `Primary` falling back to `view.Title`. Idle snapshot:
  `Primary:"STANDBY"`, `Secondary:` the existing idle hint string already
  produced by `idleSnapshot` (today's `"MISTER LINK OK · …"` marquee
  text), `Tertiary:""`.
- **Remove `formatLiveMarquee` from the VFD path** (and its tests). The
  `SOURCE · 04:23 / 09:56` line is dropped: source is shown by the
  source-cluster lamps, and elapsed/total + seek already live in the
  transport row.

**SSE ([events.go](../../../internal/chassis/events.go)):**
- `vfdEnvelope` fields become `primary` / `secondary` / `tertiary`
  (+ `queueCurrent/Total`, `uptime`). `vfdChanged` compares the three
  tiers so the VFD re-emits only when text changes.

**Template ([vfd.html](../../../internal/chassis/templates/vfd.html)):**
- Left zone becomes three rows, each a `seg-display` with a ghost span +
  `data-vfd-primary` / `-secondary` / `-tertiary`. Right panel unchanged.

**JS ([vfd-live.js](../../../internal/chassis/static/vfd-live.js)):**
- `handleVfdEvent` writes the three spans, hides empty tiers, and drives
  scrolling: measure each row's `scrollWidth` vs `clientWidth`; on
  overflow add `.scroll` and set CSS custom properties for distance +
  duration (constant px/sec so long titles aren't dizzyingly fast).
  **Measurement correctness:** measure *after* the `textContent` write
  and gate the first measurement on `document.fonts.ready` so the DSEG14
  metrics are final (a pre-font measurement mis-sizes the marquee).
  Re-measure on a `window` `resize` listener. The existing `.seg-ghost`
  span stays as the stable layout baseline. Honor `prefers-reduced-motion`
  (no animation; static/clipped).

**CSS ([chassis.css](../../../internal/chassis/static/chassis.css)):**
- Three tier sizes per Layout A (~23 / 14 / 12 px DSEG14), scroll
  keyframes, empty-row collapse, reduced-motion fallback, and responsive
  breakpoints that scale the trio down like the current title/marquee.

## CRT visualizer realignment

[pipeline.go:385-407](../../../internal/ffmpeg/pipeline.go#L385-L407) —
`visualizerTextLines` reorders to **title → artist → album** (today:
artist → title → album). Title always renders (keeps the `"Now Playing"`
fallback); artist/album render only when present. Colors, marquee, the
progress clock, and artwork modes are untouched.

## Testing

Keep all four CI gates green (`go vet`, `go test`, `go test -race`,
`go test -tags=integration ./...`).

- **Adapters** — table tests per adapter asserting the three tiers for
  each form: Plex music + new Plex movie/episode XML parse (incl.
  fetch-failure fallback), Jellyfin music/movie/episode extraction,
  yt-dlp channel + upload-date parse and `YYYYMMDD→YYYY-MM-DD`, streams
  channel/provider/group split, DLNA DIDL title, AUX/torrent.
- **Core** — `StatusHomeView` carries `Display`; empty-`Display` →
  `Primary` falls back to `Title`.
- **Chassis** — `snapshotFromStatusView` maps `Display`→`VFDData`;
  `vfdEnvelope` serialization; `vfdChanged` fires only on tier change;
  template renders three rows and collapses empties.
- **Existing chassis tests to migrate** (the rename touches them): in
  [events_test.go](../../../internal/chassis/events_test.go) the
  `vfdChanged` field-mutation cases (~lines 147-148), the idle/VFDData
  fixtures using `Title`/`Marquee` (~45-46, 136-137, 166, 177), and the
  SSE-body assertion `"title":"Seeded Title"` (~632) plus
  `TestHandleEvents_EmitsVfdEventOnTitleChange` (~768) must move to the
  `primary`/`secondary`/`tertiary` field names. This is mechanical but
  must be enumerated so coverage isn't silently lost.
- **FFmpeg** — `visualizerTextLines` emits title-first; existing pipeline
  tests updated.
- **Manual** — `fake-mister` smoke for the CRT overlay order.

## Out of scope (non-goals)

- Artwork/thumbnails on the **web VFD** (the CRT keeps its existing
  cover-art modes).
- Per-field styling beyond the three tiers; localized/relative date
  formatting (dates stay ISO `YYYY-MM-DD`).
- New metadata for video casts *on the CRT* (overlay is music-only by
  design).
- Any change to the transport row, source cluster, meter, or settings
  panes.

## Risks & mitigations

- **Plex video fetch latency** — bounded by the existing 2s metadata
  timeout; failure degrades to controller title (Primary only). No
  regression versus today.
- **Scroll measurement timing** — text spans must be in the DOM and
  fonts loaded before measuring; measure in `handleVfdEvent` after the
  write and on `resize`/`load`. Reduced-motion users skip animation
  entirely.
- **Wire-format change** — `vfdEnvelope` field rename (`title`/`marquee`
  → `primary`/`secondary`/`tertiary`) is internal (server + bundled JS
  ship together); no external consumers. The only fallout is the chassis
  test suite, enumerated under Testing above — update those in the same
  task as the rename so coverage tracks the new fields.
