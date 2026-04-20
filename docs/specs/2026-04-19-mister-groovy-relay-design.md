# MiSTer_GroovyRelay — Design Spec

**Date:** 2026-04-19
**Status:** Brainstorming deliverable. Pre-implementation.
**License:** GPL-3

## 1. Problem Statement

A Docker container that acts as a Plex Companion cast target and streams video and audio to a MiSTer FPGA running the Groovy_MiSTer core via a 5-command UDP protocol on port 32100. Conceptually: Plex Companion HTTP in, Groovy UDP out, FFmpeg in the middle. Runs on Unraid alongside an existing Plex Media Server.

The bridge exists so that a Plex user can cast a Plex movie or TV episode from their Plex iOS / Android / web / Plexamp app to a 15 kHz CRT driven by the MiSTer.

## 2. Scope

### In scope (v1)

- Plex Companion cast target registration and operation
- Playback primitives: play / pause / seek / stop
- Single video stream + audio stream
- Dual discovery: GDM local multicast **and** plex.tv account linking
- 480i60 NTSC output (single modeline; configurable default)
- Subtitle burn-in using whatever `subtitleStreamID` Plex Companion sends
- Docker image distributed via Docker Hub (built by GitHub Actions)
- Host networking mode (required for multicast)
- **Fake MiSTer sink binary** for local development and CI testing (see §8)
- **Integration test suite** exercising the sender against the fake MiSTer

### Out of scope (v1)

- Queue handling
- Library browsing on the bridge itself
- Multiple simultaneous cast sessions
- Jellyfin support (deferred to v2; see §4.5)
- URL-input adapter / web form / HTTP POST endpoint (deferred to v2 — recommended as the second adapter to force the core/adapter boundary to be right; see §4.5)
- Plugin runtime or dynamic adapter registry (not before v3; see §4.5)
- YouTube casting (different protocol — Google Cast; not planned as a cast target, though a yt-dlp-based URL-input source is a v3+ candidate)
- VLC casting (different protocol — typically Chromecast or DLNA renderer; not planned)
- MiSTer core auto-launch / remote control (v1 assumes core is already loaded)
- Pass-through optimization for already-interlaced sources
- Per-content aspect ratio override
- Mid-playback subtitle track changes via `setStreams` (v2)
- PAL 25 fps source handling (v2)
- Multi-sync / variable modeline support

## 3. Constraints and Context

- **Hardware:** Unraid server with GPU (Plex transcoding); MiSTer FPGA on wired gigabit Ethernet; 15 kHz CRT only.
- **Network:** Both devices on the same LAN. Multicast required for GDM. Host networking mode in Docker.
- **Plex server:** GPU-accelerated transcoding available and preferred — the bridge advertises a low-capability profile so PMS does the heavy decode/re-encode work.
- **User profile:** Solo / household use. Not a programmer; relies on Claude for implementation and reads code to verify. Wants code that is explicit and reviewable.

## 4. Architecture

**Single Go binary.** One process, two loosely-coupled internal modules:

### 4.1 Control plane

Low-throughput, transactional, HTTP-heavy. Split into two layers — a **core** that is adapter-agnostic, and one or more **adapters** that translate a specific input protocol into core session calls. v1 ships one adapter (Plex). See §4.5 for the expansion path.

#### 4.1.1 Core (`internal/core/`)

Adapter-agnostic session management. Takes `core.SessionRequest` (a generic descriptor — stream URL, offset, subtitle URL, audio selection, capability flags) and coordinates the data plane.

- Session state machine: idle → playing → paused → stopped. Single active session at a time. Preempt on `StartSession`.
- FFmpeg lifecycle manager: spawn, supervise, kill on stop / pause / preempt / seek / EOF
- Session status reporting (queried by adapters for their timeline protocols)

#### 4.1.2 Plex adapter (`internal/adapters/plex/`)

Everything Plex-specific. Translates Plex Companion requests into `core.SessionRequest` and subscribes to `core.SessionStatus` to broadcast timelines in Plex's XML format.

- HTTP server for Plex Companion endpoints (playMedia, play, pause, seekTo, stop, timeline/subscribe, timeline/poll, setStreams, resources, mirror/details)
- GDM multicast discovery (UDP 32412 listen, reply to M-SEARCH; `HELLO` on startup)
- plex.tv device registration and PIN linking (`POST /api/v2/pins`, poll for `authToken`, `PUT /devices/{uuid}` with connection URL every 60 s)
- Timeline broadcaster: 1 Hz push to subscribed controllers, plus long-poll handler for `timeline/poll`
- plex.tv auth token management, persisted to a single JSON file keyed by device UUID
- Transcode URL construction (injects `X-Plex-Client-Profile-Extra` to force H.264 480p)

### 4.2 Data plane

High-throughput, timing-sensitive. Contains:

- One FFmpeg process per session, producing video and audio from the same invocation (shared timestamps; this is the decisive anti-drift decision)
- Video pipe reader: raw RGB888 fields (720×240 pixels per field, two fields per interlaced frame, 59.94 fields/sec)
- Audio pipe reader: 48 kHz stereo 16-bit LE PCM
- Groovy packet builder for all 5 commands (CLOSE, INIT, SWITCHRES, AUDIO, BLIT_FIELD_VSYNC)
- UDP sender with a stable source port held across reconnects (the MiSTer identifies a session by source IP:port; see §6.2)
- Optional LZ4 block compression for BLIT_FIELD_VSYNC payloads
- ACK drainer (non-blocking; consumes 13-byte ACKs for next-frame raster alignment and drift correction — not a gate)
- Frame-timer clock: free-running 59.94 Hz derived from the configured modeline

### 4.3 Boundary and communication

Control plane spawns data plane with:

- A session descriptor (media URL, subtitle stream ID, audio stream ID, seek offset, profile hints)
- A `context.Context` for cancellation

Data plane runs until the context is cancelled (stop, preempt, EOF, error). Interface from control to data is narrow: start, stop, query-current-timestamp (for the timeline broadcaster). Data plane owns the FFmpeg subprocess handles; control plane does not touch pipes.

### 4.4 Why this shape

- Mirrors the physical reality: control plane is transactional and slow; data plane is streaming and fast. Different performance characteristics; different test strategies.
- Allows swapping the Groovy UDP sink for a fake during development without touching control plane code.
- Preserves the option to move the data plane into a Rust/C sidecar later if Go's GC pauses ever become a problem (I do not expect this, but the boundary is free insurance).

### 4.5 Adapter expansion path

v1 ships with a single adapter (Plex) and an explicit `internal/core/` + `internal/adapters/plex/` package boundary. No plugin runtime, no registry — `cmd/mister-groovy-relay/main.go` wires adapters in explicitly.

Future adapters add their own package under `internal/adapters/`:
- **v2 candidates:** Jellyfin cast target, URL-input endpoint (web form or HTTP POST accepting a media URL and optional seek offset)
- **v3+ candidates:** DLNA / UPnP AV renderer, AirPlay, IPTV / M3U playlists, HDHomeRun, yt-dlp input, Internet Archive, RTSP / RTMP security cameras, direct-file browsing

**Intentional non-goal for v1 and v2:** do not define a universal `SourceAdapter` interface prematurely. The future adapters above fall into at least three distinct structural classes — cast targets (Plex, Jellyfin, DLNA, AirPlay), URL acceptors (yt-dlp, Internet Archive, browser extension, direct files), live sources (IPTV, HDHomeRun, RTSP). Forcing one interface across these classes is almost certainly the wrong abstraction. When the second adapter exists (URL-input, recommended before Jellyfin), patterns common between it and Plex become visible and the first abstraction can be extracted honestly. Not before.

The URL-input adapter is recommended as the *second* adapter specifically because it exercises the `internal/core/` boundary against a maximally-different shape from Plex — confirming the boundary is right before Jellyfin (which is structurally similar to Plex) arrives.

## 5. Pipeline details

### 5.1 Source ingest

For every `playMedia`:

1. Parse the Plex Companion request. Extract media key, offset, session ID, subtitle stream ID, audio stream ID, client identifier, command ID.
2. Using the stored `authToken`, fetch playback metadata from PMS.
3. Construct a transcode URL with a **low-capability profile** — claim `X-Plex-Client-Profile-Name: "Plex Home Theater"` (or a custom name) and pass `X-Plex-Client-Profile-Extra` overrides that force H.264 Baseline/Main/High up to 480p progressive, AAC 2-channel. Copy the profile logic from `plex-mpv-shim` as a starting point.
4. Call `/video/:/transcode/universal/start.m3u8` with `directPlay=0&directStream=0&copyts=1&videoResolution=720x480&maxVideoBitrate=...`.
5. Hand the resulting HLS URL to the data plane.

For direct-play (uncommon at our profile): different code path, native media URL, FFmpeg seeks locally.

### 5.2 FFmpeg invocation

**One FFmpeg process**, two output streams:

- Input: HLS URL (or direct-play URL) with token headers
- Video filter graph (constructed per source properties via `ffprobe` first):
  - Decode
  - Deinterlace if source is interlaced (`yadif=send_frame`)
  - Frame-rate convert to 59.94 fps (3:2 pulldown via FFmpeg's `telecine` filter for 23.976 sources; exact filter syntax is an implementation detail)
  - Scale to 720×480 respecting aspect mode (letterbox / zoom / auto with `cropdetect` locked after ~2 s)
  - Subtitle burn-in if `subtitleStreamID` is set
  - Interlace to 480i60 (`interlace=lowpass=0:scan=tff` or `bff` per config flag)
  - Output as raw RGB888 (`-f rawvideo -pix_fmt rgb24`) to a named pipe
- Audio:
  - Decode and resample to 48 kHz stereo 16-bit LE PCM (`-f s16le -ar 48000 -ac 2`) to a named pipe

Two pipes, one process. FFmpeg internally preserves timestamps across both streams; the data plane consumes both at their natural rates.

### 5.3 Seek and pause

- **Seek:** tear down data plane, respawn FFmpeg with new seek offset in the HLS URL (transcode path) or in `-ss` (direct-play path). Report 1–2 s of black while respawning.
- **Pause:** same mechanism as seek. No attempt to keep FFmpeg alive. Treat pause and seek as identical internal operations.
- **Resume:** respawn with offset = paused position.
- **Stop:** tear down data plane, return session to idle.

## 6. Groovy protocol implementation

Wire details (authoritative from `psakhis/Groovy_MiSTer` source; see `docs/references/groovy_mister.md` and `docs/references/mistercast.md`).

- All commands on UDP port 32100. Little-endian. Protocol version = 1.
- Command byte is first byte of every datagram. IDs 1..8.
- **INIT:** establishes session parameters including `rgbMode` (0=RGB888, 1=RGBA8888, 2=RGB565). We use RGB888 — FFmpeg can output `rgb24` directly.
- **SWITCHRES:** modeline as VESA fields + `pClock` as IEEE-754 double (8 bytes) + interlace flag (1 byte). Fire-and-forget.
- **BLIT_FIELD_VSYNC:** header is 8 / 9 / 12 / 13 bytes depending on RAW vs LZ4 × full vs delta vs dup. Payload is the raw (or LZ4-compressed) field bytes, sliced into 1472-byte UDP datagrams with **no per-packet sequence numbers** — the receiver concatenates by arrival order.
- **AUDIO:** 3-byte header plus PCM payload. 48 kHz stereo 16-bit LE.
- **CLOSE:** teardown handshake.
- **ACK:** receiver emits a 13-byte status packet per blit. We drain it non-blockingly.

### 6.1 Clock discipline

**Push-driven.** Sender owns the clock. 59.94 Hz field timer paces BLIT_FIELD_VSYNC emission. The name "VSYNC" refers to the FPGA raster line encoded in the header, not a transport-level handshake.

Implications:

- Clock drift between our Go binary and the MiSTer is a real concern. Use a monotonic clock (Go's `time.Now()` is fine for this).
- Congestion back-off: **fields larger than 500 KB trigger an 11 ms stall** before the next blit. One field at 720×240 RGB888 is 518,400 bytes — just above threshold. LZ4 compression brings us well under and avoids the stall; running uncompressed triggers the back-off on almost every field.
- This makes LZ4 default-on a more defensible choice than "default off for latency" as originally argued.

### 6.2 Session identity

The MiSTer does not carry a session ID. It keys off source IP:port. The container must bind a **stable source UDP port** for the lifetime of a cast. Added to risk register.

## 7. Configuration

**Format:** TOML.

**Device identity:**
- `device_name` (default: `"MiSTer"`)
- `device_uuid` (generated on first run, persisted)

**Network:**
- `mister_host` (IP or hostname)
- `mister_port` (default: 32100)
- `source_port` (default: 32101; stable across casts)

**Video output:**
- `modeline` (default: NTSC 480i60 720×480 parameters)
- `interlace_field_order` (`tff` | `bff`, default `tff` — empirical from Mistglow validation)
- `aspect_mode` (`letterbox` | `zoom` | `auto`, default `auto`)
- `rgb_mode` (`rgb888` default)
- `lz4_enabled` (default `true`)

**Audio:**
- `audio_sample_rate` (default 48000)
- `audio_channels` (default 2)

**Plex:**
- `plex_profile_name` (default `"Plex Home Theater"`)
- `plex_server_url` (optional override; otherwise discovered)

**Paths:**
- `data_dir` (token storage, UUID, state)

## 8. Testing strategy

Three tiers, chosen to give automated feedback on as much of the surface as possible without requiring hardware for most iterations.

### 8.1 Unit tests (automated, no network)

- Groovy packet builder: byte-for-byte assertions on constructed packets for all 5 commands, including the 8 / 9 / 12 / 13-byte BLIT header variants and all `rgbMode` values
- LZ4 block compression / decompression round-trip with representative field data
- Plex Companion request parsers (playMedia, seekTo, setStreams, timeline/subscribe)
- Client capability profile string construction (compared against fixture)
- Transcode URL construction from Plex session parameters
- Session state machine transitions (idle ↔ playing ↔ paused, preempt on concurrent playMedia)
- Config file parsing and validation

Runs via `go test ./...` in under a minute. Claude iterates here without user involvement.

### 8.2 Integration tests against fake MiSTer (automated, localhost network)

A dedicated `cmd/fake-mister/` binary that:

- Listens on a configurable UDP port (default 32100)
- Parses and validates all 5 command types; records received command sequence with timestamps
- Reassembles BLIT field payloads by arrival order until `cSize` bytes received; validates against the size in the header
- Decompresses LZ4 blocks (using the same `pierrec/lz4/v4` library the sender uses)
- Dumps every Nth reassembled field as a PNG (sampling rate configurable)
- Dumps the audio stream as a WAV file
- Emits 13-byte ACK packets back to the sender so the sender's drift-correction path exercises realistically
- Exposes a structured recording of the session (JSON: command sequence, timing distribution, field counts, byte totals) for test assertions

Integration test scenarios run the real sender against the fake and assert:

- Command sequence matches expected (e.g., INIT → SWITCHRES → BLIT×N → CLOSE)
- Field count matches `fields_per_second × duration ± tolerance`
- Every field reassembles cleanly and decompresses without error
- Audio byte count matches `sample_rate × channels × bytes_per_sample × duration ± tolerance`
- Sampled PNGs exhibit non-trivial pixel variance (real content, not uniformly black or solid)
- Inter-field timing distribution stays within an expected band

Runs via `go test -tags=integration ./...` or a `make test-integration` target. Runs in CI on every PR.

### 8.3 End-to-end validation (manual, hardware required)

What the fake cannot verify and which therefore requires real MiSTer + real CRT + real Plex:

- Visual correctness on a 15 kHz CRT (color, interlace motion, field order, aspect)
- Long-horizon A/V sync (60+ minute playback without audible drift)
- Real network behavior (Wi-Fi if any, UDP reordering under actual load)
- Real Plex client compatibility: iOS, Android, web, Plexamp — each has known quirks
- Real PMS transcode negotiation (the bridge's claimed profile against what PMS actually serves)

This tier is exercised manually by the user at milestone checkpoints, not on every change.

## 9. Risk register

1. **A/V sync drift at steady state.** Mitigated by single-FFmpeg-process design with shared timestamps (Mistglow's drift bug has been traced to separate FFmpeg processes; we avoid that structurally). Still the hardest-to-test bug class.
2. **Two code paths for seek.** Direct-play seek (`-ss` to FFmpeg) vs transcode seek (new HLS URL from PMS). Both required.
3. **plex.tv API drift.** Plex has historically changed undocumented endpoints. Maintenance burden.
4. **Unraid CPU contention.** Parity checks and co-tenants may cause frame drops. Clock-push design means frame drops manifest as visible glitches, not drift.
5. **UDP reordering on BLIT payload.** No per-packet sequence number; receiver concatenates by arrival. On direct gigabit wire, low risk; on Wi-Fi or through a flaky switch, corruption.
6. **Stable source port across sessions.** Required by MiSTer's IP:port session keying. Docker host networking + explicit port binding.
7. **Congestion back-off at ~518 KB.** Our uncompressed RGB888 fields are exactly at the threshold. LZ4-on-by-default mitigates.
8. **Cropdetect misfire on dark content.** Auto-crop locks after ~2 s sampling to prevent mid-playback drift.
9. **IPv6 dual-stack edge cases.** GDM multicast on v6 differs from v4. Bind explicitly to v4 if dual-stack causes issues.
10. **Subtitle burn-in requires FFmpeg restart on track change.** Deferred to v2 (`setStreams` won't be honored mid-playback in v1).

## 10. References

- [plexdlnaplayer](../references/plexdlnaplayer.md) — primary structural reference; Plex Companion cast target in Docker. GPL-3 Python.
- [plex-mpv-shim](../references/plex-mpv-shim.md) — mature Plex Companion; device capability profile, timeline subscribe/poll mechanics, seek split.
- [Mistglow](../references/mistglow.md) — Swift implementation of the same concept; documents what *not* to do (two-FFmpeg drift bug, no plex.tv registration).
- [MiSTerCast](../references/mistercast.md) — canonical Groovy client; byte-level wire format.
- [Groovy_MiSTer](../references/groovy_mister.md) — the receiver core itself; authoritative protocol spec.
- [mister_plex](../references/mister_plex.md) — bash-era Plex-on-MiSTer; transcode URL parameters.

## 11. Deliverables

- Primary Go binary: `mister-groovy-relay` (the bridge itself)
- Secondary Go binary: `fake-mister` (test sink; see §8.2)
- Integration test suite running the real sender against `fake-mister`
- Docker image on Docker Hub containing `mister-groovy-relay` (fake-mister is shipped only as a built binary and run locally/in CI)
- GitHub Actions CI: unit + integration tests on every PR, build + publish Docker image on tag
- README covering install, configuration, first-cast walkthrough
- Unraid Community Apps template (optional; improves discoverability)

## 12. Open items for the implementation plan

- Empirical field-order validation procedure — ship TFF default (Mistglow-validated), provide runtime BFF toggle, document a test-pattern procedure for the user to run once on real hardware
- Exact device capability profile string — starting point is `plex-mpv-shim`'s profile name + `X-Plex-Client-Profile-Extra` override pattern; tailor the override to force H.264 Baseline/Main/High ≤ 480p and AAC 2-channel
- Integration test scenarios to script first: basic cast, seek, pause/resume, preempt, EOF, stop, sub burn-in present, sub burn-in absent

## 13. Explicit non-goals

The bridge is a **protocol translator**, not a Plex client. It does not browse libraries, present UI to users, or manage playlists. All user-facing Plex interaction happens in existing Plex apps. The bridge only appears in Plex's cast-target list and obeys commands sent to it.
