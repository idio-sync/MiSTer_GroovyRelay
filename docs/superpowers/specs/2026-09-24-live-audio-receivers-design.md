# Live Audio Receivers (Spotify Connect + AirPlay) Design

**Date:** 2026-09-24
**Status:** Design approved; implementing commit-by-commit on `feat/live-audio-receivers`.
**Scope:** Make the bridge a native Spotify Connect and AirPlay (classic) audio
receiver whose audio drives the existing CRT music visualizer. Each protocol is
its own adapter, peer to Plex/Jellyfin/DLNA. A shared `liveaudio` package owns
everything protocol-agnostic.

## Background

Phones can already reach the CRT through Plex, Jellyfin, and the DLNA renderer
(Android apps such as BubbleUPnP). Two large gaps remain: iOS has no native
DLNA sender, and the Spotify app only casts to Spotify Connect devices. Both
ecosystems have mature open-source receivers that emit raw PCM:

- **librespot** (Rust) — Spotify Connect receiver; `--backend pipe` writes
  s16le 44.1 kHz stereo to stdout; `--onevent <program>` runs a program per
  player event with metadata in environment variables.
- **shairport-sync** (C) — AirPlay receiver; `-o stdout` writes s16le
  44.1 kHz stereo; metadata (`core/*`, `ssnc/*` items incl. artwork) can be
  sent to a UDP socket.

The visualizer already renders audio-only sessions (`MediaKindMusic`,
`Visualizer.Enabled`) and the AUX adapter proved the live, unseekable,
non-probed capture input path (`core.AudioCaptureInput`).

## Goals

1. `internal/adapters/spotify`: Spotify Connect receiver backed by a
   bridge-supervised librespot.
2. `internal/adapters/airplay`: AirPlay (classic / AirPlay 1) receiver backed
   by a bridge-supervised shairport-sync. Linux/Docker only in v1.
3. `internal/adapters/liveaudio`: shared helper supervision, PCM relay, session
   state machine, and metadata handling.
4. Live on-screen track text: title/artist/album update on track change
   without restarting the pipeline.
5. Artwork changes in cover visualizer modes trigger one fast plane restart
   with no audio loss.
6. Pause holds the session for a configurable grace window before ending.
7. AIRPLAY and SPOTIFY lamps in the receiver chassis source cluster.

## Non-goals

- AirPlay 2 (multi-room, nqptp). Follow-up phase.
- AirPlay screen/video mirroring.
- Spotify username/password or Web API login; zeroconf only.
- Transport control (pause/next) from the receiver UI.
- Per-adapter visualizer mode; the bridge default mode applies.
- Multi-room synchronisation.
- AirPlay on native Windows (shairport-sync does not build there).

## User decisions

| Topic | Decision |
| --- | --- |
| Adapter boundary | Separate cast-target adapters, not AUX inputs |
| Helper lifecycle | Bridge supervises helpers as child processes |
| Track change | Live text update; plane restart only when artwork changes in a cover mode |
| Pause | Hold with silence for `pause_grace_seconds` (default 30), then end |
| AirPlay flavour | Classic first; AirPlay 2 later |
| Chassis | Two new lamps: AIRPLAY, SPOTIFY |

## Architecture

```
internal/adapters/liveaudio/     protocol-agnostic
  supervisor.go   spawn/restart/stop a helper (backoff, stderr → slog, health)
  relay.go        helper stdout PCM → ring buffer → paced loopback HTTP
  session.go      core.SessionRequest builder + Idle/Live/Held FSM
  events.go       Event{Play, Pause, Resume, Track, Stop} + TrackMeta
internal/adapters/spotify/       librespot argv, onevent mapping, config/Fields/scopes
internal/adapters/airplay/       shairport-sync argv/config, UDP metadata parser, config
internal/core/                   + LiveTextDir pass-through, UpdateNowPlayingIfSession
internal/ffmpeg/                 + drawtext textfile/reload path
internal/chassis/                + AIRPLAY, SPOTIFY lamps
cmd/mister-groovy-relay/         + register adapters; --librespot-event subcommand
Dockerfile                       + librespot and shairport-sync build stages
```

Protocol adapters only translate helper events into `liveaudio` events.
Everything that touches `core.Manager` lives in `liveaudio`, so protocol
adapters hold no locks shared with core. Core still imports no adapter package.

### Audio path

```
helper stdout (s16le, 44.1 kHz, 2 ch)
  → relay reader goroutine → bounded ring buffer (~300 ms)
        full buffer blocks the reader → backpressure to the helper
  → GET /internal/liveaudio/<adapter>/<token>
        loopback-only; token is random per relay instance
        paced at wall clock (176 400 B/s); empty buffer → silence
  → ffmpeg -f s16le -ar 44100 -ac 2 -i <url>   (existing AudioCapture input)
  → visualizer video + PCM to the MiSTer (AudioOutputMonitor by default)
```

- Exactly one reader at a time. A new connection (e.g. after an artwork
  restart) takes over; the previous response is ended.
- Between sessions the relay keeps draining helper stdout so the helper never
  blocks; audio with no reader is discarded.
- Volume stays helper-side, so the phone's slider works (low volume shrinks
  the visualizer; accepted).

The loopback route mounts on the single shared HTTP listener (no second
listener), following `/internal/aux-proxy/`. Requests from non-loopback remote
addresses are rejected after `netip.Addr.Unmap()`.

### Events

`liveaudio.Event` kinds: `Play`, `Pause`, `Resume`, `Track(TrackMeta)`, `Stop`.
`TrackMeta` holds `Title`, `Artist`, `Album`, `Duration`, and artwork as either
bytes (AirPlay) or an HTTPS URL (Spotify).

**Spotify.** librespot `--onevent` points at the bridge binary itself
(`mister-groovy-relay --librespot-event`), which POSTs the event environment to
`/internal/liveaudio/spotify/events/<token>` and exits. No shell script, so it
works on Windows. The bridge URL and token reach the child through environment
variables set when librespot is spawned (librespot passes its environment
through to the onevent program). Mapping:

| librespot `PLAYER_EVENT` | liveaudio event |
| --- | --- |
| `playing` | `Play` (the FSM treats `Play` while Held as `Resume`, and ignores it while Live) |
| `paused` | `Pause` |
| `track_changed` | `Track` (from `NAME`, `ARTISTS`, `ALBUM`, `DURATION_MS`, `COVERS`) |
| `stopped`, `session_disconnected` | `Stop` |

Cover URLs are fetched over HTTPS into `artworkcache`, best effort.

**AirPlay.** shairport-sync sends metadata to a UDP socket on 127.0.0.1 (a
port the adapter picks). Items parsed:

| Item | Meaning |
| --- | --- |
| `ssnc/pbeg` | `Play` |
| `ssnc/pfls` | `Pause` |
| `ssnc/prsm` | `Resume` |
| `ssnc/pend` | `Stop` |
| `core/minm`, `core/asar`, `core/asal` | title, artist, album |
| `ssnc/mden` | end of metadata bundle → emit `Track` |
| `ssnc/PICT` | artwork bytes (may arrive chunked via `ssnc/chnk`) |

### Session state machine (`liveaudio.Session`)

```
Idle ──Play──▶ Live ──Pause──▶ Held ──Resume──▶ Live
               │  ▲             │ grace timer (pause_grace_seconds)
               │  └──Track──┘   ▼
               └────Stop──────▶ Idle
```

- **Play** → `Manager.StartSession` (last caster wins, as for every adapter).
  `AdapterRef` is `<adapter>:<session-id>`.
- **Track** → rewrite the live text files + `Manager.UpdateNowPlayingIfSession`. If the current mode uses artwork
  and the artwork hash changed, restart via `StartSessionIfSession(ref,
  generation)`; the relay buffers across the restart.
- **Pause** → Held: relay emits silence (visualizer idles flat), grace timer
  armed. **Resume** within the window → Live on the same pipeline.
- **Grace expiry / Stop** → end the core session only if its AdapterRef and
  generation are still ours.
- **Preempted** (core `OnStop` fires with a reason we did not cause) → restart
  the helper so the phone sees a clean disconnect rather than playing into the
  void.
- A helper crash while Live is treated as `Stop`.

### Live visualizer text (core + ffmpeg)

- `VisualizerRequest` gains `LiveTextDir string` (absolute). The adapter owns
  that directory: it writes the title/artist/album files with
  `ffmpeg.WriteVisualizerText` before starting the session, rewrites them on
  track change, and removes them after the session ends. The pipeline renders
  those three lines with `drawtext=textfile=…:reload=1:expansion=none`
  instead of `text='…'`, reserving all three slots.
- Core does no file I/O for live text, so there is no new locking or cleanup
  path in `Manager`.
- drawtext aborts the graph when a reload fails, so a file must never be
  missing or resized mid-read. Linux/macOS replace files by atomic rename.
  Windows cannot (rename and resize both race ffmpeg's open/mapping), so
  Windows files are a fixed 512 bytes, NUL-padded (drawtext stops at the
  first NUL), overwritten in place.
- `Manager.UpdateNowPlayingIfSession(ref, generation, title, display)` updates
  the active session's `Title`/`DisplayMetadata` (the VFD rows) under the
  session-key guard. It is an in-memory write; the chassis picks it up on its
  next tick.
- Live sessions have `Duration = 0`, so no progress/duration text is shown.

## Configuration

`[adapters.spotify]` and `[adapters.airplay]`, each with `Fields()`,
`ApplyScope` table, and `Validator`.

| Field | Spotify | AirPlay | Default | Scope |
| --- | --- | --- | --- | --- |
| `enabled` | ✓ | ✓ | false | restart-cast |
| `name` (advertised, 1–63 chars) | ✓ | ✓ | `MiSTer CRT` | restart-cast |
| `binary_path` (empty = PATH lookup) | ✓ | ✓ | "" | restart-cast |
| `audio_output` (`monitor`\|`visual_only`) | ✓ | ✓ | monitor | next-cast |
| `pause_grace_seconds` (0–600) | ✓ | ✓ | 30 | hot-swap |
| `bitrate` (96\|160\|320) | ✓ | — | 320 | restart-cast |
| `zeroconf_port` (0 = random) | ✓ | — | 0 | restart-cast |
| `port` (RTSP) | — | ✓ | 5000 | restart-cast |

`restart-cast` fields restart the helper in-process (no bridge restart). The
librespot credential cache lives in `<data_dir>/librespot`; the audio cache is
disabled.

## Error handling

- Helper exit → supervisor restarts with backoff 1 s → 30 s cap. After three
  failures inside 60 s the adapter reports `StateError` with the last stderr
  line as `LastError` (red lamp; VFD flash shows detail). Backoff continues.
- Missing binary → `StateError` at `Start` with a readable message; no retry
  loop until config changes.
- Unsupported platform (AirPlay on Windows) → `StateError`
  "AirPlay is not supported on this platform".
- Loopback routes reject non-loopback callers (403) and unknown tokens (404).
- Artwork fetch/decode failures are logged and ignored (no artwork).
- `Stop()` → end our core session if still ours, SIGTERM the helper (Kill on
  Windows), wait 3 s, then kill.

## Packaging

- Dockerfile gains two musl build stages at pinned tags:
  - librespot with the pipe backend and built-in (libmdns) discovery.
  - shairport-sync built `--with-stdout --with-metadata` with built-in mDNS
    (`--with-tinysvcmdns`); runtime adds the shared libs it links (libconfig,
    popt, openssl, soxr).
- Native installs set `binary_path` or put the helper on PATH. README documents
  both.
- Unverified until CI/Docker: that the pinned shairport-sync still supports
  tinysvcmdns (fallback: supervise avahi-daemon + dbus) and arm64 Rust build
  time under QEMU (mitigate with cache mounts).

## Testing

- **liveaudio unit:** relay pacing/silence/backpressure/reader-takeover/token
  and loopback rejection (fake clock); FSM grace expiry, artwork restart,
  preemption → helper restart, generation guards (fake core); supervisor with
  a re-exec'd test binary as the fake helper.
- **spotify unit:** env → event mapping table; `--librespot-event` POST; argv.
- **airplay unit:** metadata parser on captured fixtures incl. chunked PICT;
  argv/config.
- **core/ffmpeg unit:** `UpdateNowPlayingIfSession` guards; textfile/reload argv;
  rewrites under a real ffmpeg reloading every frame.
- **integration (build tag):** fake helper → relay → real ffmpeg →
  fake-mister: fields and audio arrive; text update causes no second INIT;
  artwork change causes exactly one restart.
- **chassis:** lamp tests cover AIRPLAY/SPOTIFY.
- **Manual (not automatable here):** iPhone and Spotify app against the Docker
  image on the LAN.

## Phases

1. `liveaudio` + core/ffmpeg live text + fake-helper integration test.
2. Spotify adapter + librespot packaging + SPOTIFY lamp.
3. AirPlay classic adapter + shairport-sync packaging + AIRPLAY lamp.
4. (Later) AirPlay 2.
