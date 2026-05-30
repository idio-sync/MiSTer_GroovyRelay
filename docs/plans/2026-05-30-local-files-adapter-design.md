# Local Files Adapter — Design

> **Status:** Design approved, ready for `writing-plans`. Brainstormed 2026-05-30.
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
  is wanted; not required for v1.

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

### Browse endpoint

```
GET /ui/adapter/localfiles/browse?lib=<name>&path=<rel>
```

- `lib` selects a configured library; `path` is **relative to that library's
  root** (empty = library root).
- Returns the folder listing: subfolders + playable files (filtered by a
  media-extension allowlist), with light `ffprobe`-derived title/duration for
  files. ffprobe is best-effort and must not block the listing on failure.
- Same-origin (`Sec-Fetch-Site: same-origin`) enforced like other `/ui` POSTs;
  GET listing follows the existing UI route conventions.

### Path jailing (the security core)

For every request, resolve the target as:

1. Look up the named library → its configured `root`.
2. Join `root` + cleaned relative `path`; reject any input containing `..`
   segments before joining.
3. Resolve symlinks (`filepath.EvalSymlinks`) on the final path **and** confirm
   the resolved real path is still inside the resolved real `root`. Reject
   symlink escapes.
4. Reject absolute `path` inputs and anything that escapes the root.
5. Read-only throughout — no write/delete/rename surface exists.

This is the primary test surface: traversal attempts, symlink escapes, absolute
paths, missing/!directory roots, and platform path quirks (Windows drive
letters/separators, UNC) all get explicit tests.

### Cast path

Picking a file POSTs to a cast route that:

1. Re-resolves + re-jails the file path (never trust a path round-tripped
   through the client).
2. Builds a `core.SessionRequest` with `DirectPlay` + full capabilities
   (seek/pause/duration), source = the local file path.
3. Calls `Manager.StartSession`.

Everything downstream — transcode, transport, music-visualizer branching,
artwork cache — is reused unchanged.

### Browse UX (chassis pane)

- The adapter pane shows **settings + a "Browse" control**. The browser is
  **hidden by default** and **opens as a drawer** when picked.
- Drawer: libraries at top level → folder drill-down with breadcrumb → playable
  files → pick one to cast. Single-file only in v1.
- Library directory configuration lives in the file-picker section of the
  adapter settings.

## Free wins inherited from the pipeline

- **Full transport.** Local files have real duration and are seekable, so
  pause / seek / scrub / accurate elapsed all work — a better transport
  experience than live URL/HLS sources.
- **Audio auto-visualizer.** The bridge already routes audio-only items to the
  music visualizer; pointing a library at a FLAC/MP3 folder yields the CRT
  visualizer for free, with embedded cover art via the artwork cache.
- **Sidecar subtitles** (`movie.srt` beside `movie.mkv`) are a natural future
  burn-in hook (not built in v1).

## Deployment & path semantics

One shared abstraction — *a library is a name + an absolute path this process
can read, jailed to that root* — with the only deployment-specific concern being
documentation plus a validate-root-exists check. **No branching code paths
between Docker and native.**

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
  overlap, platform separators.
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

1. Config types + `DecodeConfig`/`Validator` (named libraries, root validation).
2. Path-jail helper + exhaustive tests.
3. Adapter skeleton (`Adapter` interface, disabled-by-default, status).
4. Browse endpoint (`RouteProvider`) + extension filter + ffprobe metadata.
5. Cast route → `SessionRequest` → `Manager.StartSession`.
6. Chassis adapter pane: settings (library config) + browse drawer UI.
7. Docs: README volume-mount + permissions note; deployment-aware validation copy.
