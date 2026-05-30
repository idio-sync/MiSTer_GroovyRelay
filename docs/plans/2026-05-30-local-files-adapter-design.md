# Local Files Adapter — Design

> **Status:** Design approved, ready for `writing-plans`. Brainstormed 2026-05-30;
> revised same day after a codebase-grounded review (fixed: audio visualizer is
> adapter-built not automatic; browse must be POST for CSRF; added `MediaInputPolicy`
> as a second required control; flagged array-of-tables wire type + non-stock
> library-list UI; hardened the path-jail; chose the streams-adapter cast model).
>
> This is "A2" from a broader feature brainstorm exploring two axes: new cast
> sources and baked-in CRT visuals. The remaining menu items (internet radio,
> IPTV+EPG, ambient data channel, OSD overlays, etc.) are intentionally **not**
> captured here — this doc scopes only the Local Files adapter.

## Summary

Add a `localfiles` cast-source adapter that lets the user browse media files on
disk — host media bind-mounted into the Docker container, or real OS paths on
native builds — from the receiver-chassis web UI and cast a single file to the
MiSTer.

The key architectural insight: **casting is already solved.** Every adapter
builds a `core.SessionRequest` and hands it to `Manager.StartSession`, which
drives the FFmpeg → RGB-fields + PCM → Groovy UDP pipeline. A local file is the
simplest possible source — no fetch, no `yt-dlp` resolve, no HLS buffer; FFmpeg
reads the path directly. So the overwhelming majority of the work is the
**browse-and-pick UI plus path-jail security**, not the casting.

## Goals (v1)

- A new `localfiles` adapter implementing the standard `Adapter` interface,
  **disabled by default** (like `torrent` and `dlna`).
- **Named libraries**: a list of `{ name, root }` entries (e.g.
  `Movies → /media/movies`, `Music → /media/music`), configured in the adapter's
  settings pane.
- A **file-browser drawer** in the chassis adapter pane: hidden by default,
  opens drawer-style when picked. Top level lists libraries; drill into folders
  with breadcrumb navigation; playable files are listed and castable.
- **Single-file cast** → builds a `SessionRequest` and starts a session through
  the existing spine.
- **Strict path jailing** per library root; read-only; same-origin CSRF like the
  rest of `/ui/*`.
- Light `ffprobe` metadata (title/duration, embedded cover art) feeding the
  existing display/artwork-cache path.

## Non-goals (v1)

- Folder-as-queue / auto-advance (defer to the in-flight Plex auto-advance work).
- Subtitle burn-in (sidecar `.srt` detection is noted as a future hook only).
- Multi-root search / unified library view.
- Mounting network shares from inside the app (the operator mounts shares; the
  adapter only ever sees paths).

## Architecture

### Adapter shape

`internal/adapters/localfiles/` mirrors the existing adapter packages
(`url`, `streams`, `torrent`). It implements:

- `Adapter` — `Name()` = `"localfiles"`, `DisplayName()`, `Fields()`,
  `DecodeConfig`, `IsEnabled`, `Start`/`Stop`, `Status`, `ApplyConfig`.
- `Validator` — validate the candidate `[adapters.localfiles]` section
  (including each library root) before any on-disk write, per the
  "validate before disk write" contract.
- `RouteProvider` — mounts the browse + cast routes under
  `/ui/adapter/localfiles/`.
- Optionally `SourceAvailabilityViewer` later, if a chassis source-cluster lamp
  is wanted; not required for v1. Note: `SourceID()` is documented to return one
  of `"streams"|"plex"|"jellyfin"|"dlna"`
  ([internal/adapters/source.go:17](../../internal/adapters/source.go#L17));
  adding `"localfiles"` to the chassis source-cluster is a separate change there,
  not just an adapter method — flagged so the future lamp work isn't assumed free.

There is **no** `LinkAware` (no auth handshake) and **no**
`PublicRouteProvider` (all routes live behind the standard `/ui/*` CSRF
middleware).

### Config

```toml
[adapters.localfiles]
enabled = false

  [[adapters.localfiles.library]]
  name = "Movies"
  root = "/media/movies"

  [[adapters.localfiles.library]]
  name = "Music"
  root = "/media/music"
```

`root` is **always an absolute path this process can read** — a container-side
path under Docker, a real OS path on native. Library config is editable in the
adapter settings pane (the file-picker section), not TOML-only.

**Two non-trivial plumbing problems the planner must solve (no in-repo
precedent for either):**

1. **Array-of-tables round-trip.** No existing adapter decodes a `[]struct`
   array-of-tables; nested config uses `map[string]…`
   ([internal/adapters/streams/config.go:29](../../internal/adapters/streams/config.go#L29)),
   and `streams` still needed a custom `UnmarshalTOML` wire type even for a
   single repeated field ([config.go:43-86](../../internal/adapters/streams/config.go#L43-L86)).
   The save path must decode → validate → **re-serialize to disk** intact, which
   is precisely where streams introduced `configWire`/`configToWire`/
   `wireToConfig`. Plan on a wire type for the library list.
2. **Flat `FieldDef` schema vs. a variable-length list.** The settings UI renders
   a flat list of `FieldDef` keyed controls
   ([internal/adapters/adapter.go:117-134](../../internal/adapters/adapter.go#L117-L134));
   there is no built-in "repeating group" control. Editing a list of
   `{name, root}` pairs through that schema is unsolved and is **real UI design
   work** — likely a custom widget/route (à la the URL adapter's host tag-list
   pane), not a stock `FieldDef`. This is the biggest unmodeled gap; do not
   assume the standard form renderer covers it.

### Browse endpoint

```
POST /ui/adapter/localfiles/browse   (form body: lib=<name>&path=<rel>)
```

- `lib` selects a configured library; `path` is **relative to that library's
  root** (empty = library root).
- Returns the folder listing: subfolders + playable files (filtered by a
  media-extension allowlist), with light `ffprobe`-derived title/duration for
  files. ffprobe is best-effort and must not block the listing on failure.
- **Must be a POST, not a GET.** Verified against the codebase:
  `csrfMiddleware` unconditionally allows all GET/HEAD/OPTIONS
  ([internal/ui/csrf.go:34-38](../../internal/ui/csrf.go#L34-L38)), and the
  RouteProvider mount wraps **only** POST/DELETE/PUT/PATCH with
  `csrfMiddleware`; GET routes get just `extensionCORSMiddleware(s.guard(...))`
  with no same-origin guard
  ([internal/ui/server.go:282-292](../../internal/ui/server.go#L282-L292)).
  A browse endpoint that enumerates on-disk paths is exactly the kind of read a
  hostile LAN page would issue cross-origin (filesystem reconnaissance /
  existence oracle), so it **must** inherit CSRF protection. Making it a POST is
  the simplest correct option; the alternative is an explicit `Sec-Fetch-Site:
  same-origin` check inside a GET handler. Note `s.guard` provides
  auth/first-run gating, **not** cross-origin protection — the two are
  independent.

### Path jailing (the security core)

For every request, resolve the target as:

1. Look up the named library → its configured `root`; resolve `root` itself
   through `filepath.EvalSymlinks` once and cache the real root.
2. Reject absolute `path` inputs and any `path` containing `..` segments
   *before* joining. Then `filepath.Clean` + join onto the real root.
3. Resolve symlinks on the final path. **Containment check must be
   boundary-aware:** use `filepath.Rel(realRoot, realTarget)` and reject if the
   result starts with `..` — do **not** use `strings.HasPrefix`, which lets
   `/media/movies-secret` pass a `/media/movies` jail. Reuse the tested pattern
   in [internal/adapters/torrent/cache.go:95-101](../../internal/adapters/torrent/cache.go#L95-L101).
4. Read-only throughout — no write/delete/rename surface exists.

**Known gaps to handle explicitly (this is billed as the test centerpiece, so
do not hand-wave them):**

- **TOCTOU.** `EvalSymlinks`-then-later-`os.Open` is a swap window. Resolve once
  and, where practical, operate on the returned `*os.File`/fd rather than
  re-opening by path. At minimum, document the race.
- **Case-insensitive filesystems** (default macOS/Windows — both listed as
  native targets). A case-varied path can defeat a case-sensitive `Rel` check.
  Normalize case for the containment comparison on those platforms.
- **Non-existent leaf.** `EvalSymlinks` errors on a path whose final element
  doesn't exist; define the "not found" behavior (jail the *parent*, then check
  the leaf) rather than leaking the error.
- **Hardlinks** cannot be defended by path-jailing — acknowledge as a non-goal.

### FFmpeg input policy (a SECOND, independent required control)

Path-jailing the *string* is not sufficient. Once the path reaches core, both
ffprobe and ffmpeg dereference it
([internal/core/manager.go:638,678,804](../../internal/core/manager.go#L638)),
and a crafted container can demux **child/external resources** after the
adapter's path check passed — the exact class of bug `MediaInputPolicy` exists
to stop ([internal/core/types.go:200-212](../../internal/core/types.go#L200-L212)
explicitly: "adapter-only URL validation is not sufficient because
ffprobe/ffmpeg can otherwise … demux child resources").

So the cast route **must** set a restrictive `MediaInputPolicy` for local files:
a minimal `-protocol_whitelist` (`file` only, plus what the demuxer strictly
needs), no reconnect/redirect following, no nested external fetch. The path-jail
and the ffmpeg policy are **two distinct controls and both are required.**

This is the primary test surface: traversal attempts, symlink escapes, absolute
paths, boundary-collision roots, missing/!directory roots, case-variation
escapes, and platform path quirks (Windows drive letters/separators, UNC) all
get explicit tests.

### Cast path

Picking a file POSTs to a cast route that:

1. Re-resolves + re-jails the file path (never trust a path round-tripped
   through the client).
2. Builds a `core.SessionRequest` — `DirectPlay: true`, full `Capabilities`
   (`CanSeek`/`CanPause`), `Source` = the local file path, `MediaKind`,
   `Title`, `DisplayMetadata`, and a `MediaInputPolicy` (see below).
3. Calls `Manager.StartSession`.

The **streams adapter is the closest precedent** for a browse-then-cast
adapter: it owns its cast route via `RouteProvider`
(`/ui/adapter/streams/play`) and calls `core.StartSession` directly
([internal/adapters/streams/playback.go:341-348](../../internal/adapters/streams/playback.go#L341-L348)).
`localfiles` follows that model — its own `RouteProvider` POST cast route — **not**
the chassis `QuickCastProvider` drawer spine (see "Cast wiring" below).

**Audio is not an automatic visualizer — the adapter must build the
`VisualizerRequest` itself.** Core *rejects* a session with no video unless the
adapter explicitly sets `Visualizer.Enabled = true`, `MediaKind = MediaKindMusic`,
and a valid `Visualizer.Mode`
([internal/core/manager.go:383-401](../../internal/core/manager.go#L383-L401)).
So for audio files the cast route must: detect audio-only (via ffprobe), resolve
the **global visualizer mode** from config, populate
`VisualizerRequest{Enabled, Mode, Metadata}` including artwork, and set
`MediaKind = MediaKindMusic` — mirroring
[internal/adapters/plex/companion.go:407-410](../../internal/adapters/plex/companion.go#L407-L410).
This is real work, not inherited for free (see corrected "Free wins").

Everything downstream of a correctly-built request — transcode, transport,
visualizer rendering, artwork cache — is reused unchanged.

### Cast wiring (which contract)

The chassis cast drawer dispatches through `adapters.QuickCastProvider`
(`QuickCastTabs()` / `HandleQuickCast`,
[internal/adapters/playback.go:76-78](../../internal/adapters/playback.go#L76-L78))
with a hardcoded `castKindToTab` map
([internal/chassis/cast.go:19-23](../../internal/chassis/cast.go#L19-L23)).
Adapter *settings panes* are a separate surface served by the UI server
(`handleAdapterGET`, [internal/ui/server.go:255](../../internal/ui/server.go#L255)).
v1 wires `localfiles` as: a UI-server adapter pane (settings + browse drawer) +
its own `RouteProvider` browse/cast POST routes calling `core.StartSession` —
the streams model. It does **not** register a `QuickCastProvider` tab in v1
(the file browser needs a tree UI, not a one-line paste box). "Chassis adapter
pane" in this doc means the adapter settings pane, not the QuickCast drawer.

### Browse UX (chassis pane)

- The adapter pane shows **settings + a "Browse" control**. The browser is
  **hidden by default** and **opens as a drawer** when picked.
- Drawer: libraries at top level → folder drill-down with breadcrumb → playable
  files → pick one to cast. Single-file only in v1.
- Library directory configuration lives in the file-picker section of the
  adapter settings.

## Inherited from the pipeline

- **Full transport (genuinely free).** Local files have real duration and are
  seekable, so pause / seek / scrub / accurate elapsed all work via
  `DirectPlay` + `Capabilities{CanSeek, CanPause}` — a better transport
  experience than live URL/HLS sources, with no extra code.
- **Visualizer for audio (NOT free — requires adapter work).** The bridge does
  **not** auto-route audio to the visualizer; the adapter must build the
  `VisualizerRequest` (see "Cast path" / C1 above). The *rendering* and artwork
  cache are reused, but resolving the global mode, populating metadata, and
  detecting audio-only items is a required build step, not a freebie.
- **Sidecar subtitles** (`movie.srt` beside `movie.mkv`) are a natural future
  burn-in hook (not built in v1).

## Deployment & path semantics

One shared abstraction — *a library is a name + an absolute path this process
can read, jailed to that root.* **No deployment-branching in the browse/cast
flow** — Docker vs. native differ only in documentation and the validate-root
error copy. (Platform path handling — drive letters, UNC, separators,
case-folding — is genuine per-OS logic, but it's delegated to `path/filepath`
and the jail's case-normalization, not a Docker/native branch.)

### Docker (primary target): a volume mount is required

The bridge sees only the container filesystem, so host media must be
bind-mounted in, and the configured `root` is the **container-side** path:

```bash
docker run -d --name mister-groovy-relay --network=host \
  -v /opt/mister-groovy-relay:/config \
  -v /mnt/user/media:/media:ro \        # host media, read-only
  idiosync000/mister-groovy-relay:latest
```

Then configure `Movies → /media/movies`.

- **`:ro` recommended** — the adapter is read-only anyway; defense in depth.
- **Path gotcha:** the browser shows *container* paths. A user who mounts
  `/mnt/user/media:/media` then types the host path `/mnt/user/media` as a root
  gets "not found." Mitigated by the config-time validation below.
- **Permissions:** the entrypoint does no PUID/PGID remapping, so mounted files
  must be readable by the container's UID (world-readable or matching owner).
  Docs note; not code.
- Host networking is orthogonal to volumes — no interaction.

### Native (Windows/macOS/Linux): no mount, just a real path

Runs directly on the host with the user's filesystem permissions. A library root
is any real OS path: `D:\Media\Movies` (incl. UNC `\\NAS\media`),
`/Users/jake/Movies`, `/home/jake/media`. Network shares are the OS's concern; if
mounted/UNC, the adapter just sees a path.

### Config-time validation (deployment-aware error)

When a library root is saved (`Validator`), stat it in-process:

- missing or not a directory → reject with a **deployment-aware message**, e.g.
  *"path not found — in Docker this must be a path you've mounted into the
  container."*

This turns the #1 anticipated support question into a self-explaining error.

## Error handling

- **Invalid root at save time** → `FieldError` on the offending library field;
  on-disk config untouched (Validator contract).
- **Path-jail violation at browse/cast time** → 4xx, generic "not found" (don't
  leak whether a path exists outside the jail).
- **ffprobe failure on a file** → list the file with degraded metadata; never
  fail the whole listing.
- **Cast start failure** → surfaced through the existing inline-error /
  banner path used by other adapters.

## Testing strategy

- **Path jail unit tests** (highest priority): `..` traversal, absolute-path
  input, symlink escape, root == file, non-existent root, nested-library
  overlap, **boundary collision** (`/media/movies-secret` vs `/media/movies`),
  **case-variation escape** on case-insensitive FS, non-existent leaf, platform
  separators.
- **FFmpeg input policy test**: confirm the cast `SessionRequest` carries a
  restrictive `MediaInputPolicy` (file-only protocol whitelist, no redirect/
  nested fetch) so a crafted container can't demux outside the jail.
- **Visualizer-request test**: audio-only file produces a `SessionRequest` with
  `Visualizer.Enabled`, `MediaKindMusic`, and a valid resolved `Mode`.
- **Validator tests**: missing/!dir root produces a `FieldError`; valid config
  passes and writes.
- **Browse endpoint tests**: listing shape, extension filtering, same-origin
  enforcement, ffprobe-failure degradation.
- **Cast tests**: file → `SessionRequest` mapping (DirectPlay + caps), re-jail
  on cast, audio file routes to visualizer branch.
- Follow the existing adapter test conventions (`*_test.go` alongside source).

## Open questions for planning

- Exact media-extension allowlist (video + audio) and whether it's
  user-overridable (lean: fixed list v1).
- Whether to ffprobe eagerly per-listing or lazily on hover/selection
  (perf vs. richness; lean: cheap probe per file, bounded concurrency).
- Drawer markup/JS reuse vs. new component within the chassis templates.

## Build sequence (high level — detailed plan via writing-plans)

1. Config types + wire type for the `[[library]]` array-of-tables (decode →
   validate → re-serialize), `DecodeConfig`/`Validator` (named libraries, root
   validation with deployment-aware error).
2. Path-jail helper (boundary-aware `filepath.Rel` containment, symlink resolve,
   case-fold on case-insensitive FS, TOCTOU note) + exhaustive tests.
3. Adapter skeleton (`Adapter` interface, disabled-by-default, status).
4. Browse endpoint as a **POST** `RouteProvider` route (inherits CSRF) +
   extension filter + best-effort ffprobe metadata.
5. Cast route → build `SessionRequest` (incl. **`MediaInputPolicy`** for `file:`,
   and the **`VisualizerRequest`** for audio-only items) → `Manager.StartSession`.
6. Adapter settings pane: library-list editor (custom repeating-group widget,
   not stock `FieldDef`) + browse drawer UI.
7. Docs: README volume-mount + permissions note; deployment-aware validation copy.
