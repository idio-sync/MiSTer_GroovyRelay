# Mistglow

## Metadata
- URL: https://github.com/jcramer83/Mistglow
- License: MIT
- Primary languages: C 46.1%, Swift 44.2%, C++ 9.2%, Shell 0.5% (C/C++ is bundled LZ4; the app is Swift)
- Platform: macOS 14+ (Sonoma), native SwiftUI app, SwiftPM (`Package.swift`)
- Activity: active. Latest release v1.08 on 2026-03-14; open issues filed as recently as 2026-04-03

## Purpose
Mistglow is a native macOS app that captures the Mac screen (or a Plex media stream) and streams it to a MiSTer FPGA running the Groovy_MiSTer core over the Groovy UDP protocol. It targets CRT-native output (e.g., 480i NTSC) by driving exact modelines on the FPGA side. It also embeds a Plex Companion HTTP server so Plex apps can "cast" to the MiSTer.

## Relevance to MiSTer_GroovyRelay
Mistglow is the closest prior art for our exact use case. It proves the Plex-Companion-to-Groovy bridge is viable on a single host. The pieces we directly care about are `Sources/Mistglow/Protocol/GroovyProtocol.swift` (command IDs and byte layouts), `Sources/Mistglow/Protocol/GroovyConnection.swift` (UDP socket, MTU chunking), `Sources/Mistglow/Plex/PlexCompanionServer.swift` (the HTTP endpoint set we must mirror), `Sources/Mistglow/Plex/PlexGDMAdvertiser.swift` (the GDM discovery path we are intentionally supplementing with plex.tv), `Sources/Mistglow/Plex/PlexAVPlayerRenderer.swift` (the dual-FFmpeg pattern), and `Sources/Mistglow/Streaming/StreamEngine.swift` (the field-pump timer). These map almost 1:1 to our Go control plane and data plane.

## Key Files and Code Paths
- `Sources/Mistglow/Protocol/GroovyProtocol.swift` - `GroovyCommand` enum, constants, packet builders.
- `Sources/Mistglow/Protocol/GroovyConnection.swift` - POSIX UDP socket, `sendFrame()` with MTU chunking.
- `Sources/Mistglow/Protocol/Modeline.swift` - modeline struct; presets in `Resources/modelines.dat`.
- `Sources/Mistglow/Streaming/StreamEngine.swift` - main field-pump `DispatchSourceTimer`, interlace field split (TFF, even rows = field 0).
- `Sources/Mistglow/Streaming/FrameTransmitter.swift` - MTU chunking helper, congestion backoff.
- `Sources/Mistglow/Streaming/LZ4Compressor.swift` - wraps bundled C LZ4.
- `Sources/Mistglow/Plex/PlexGDMAdvertiser.swift` - UDP multicast 239.0.0.250:32412, passive M-SEARCH responder.
- `Sources/Mistglow/Plex/PlexCompanionServer.swift` - `NWListener` on TCP 3005, full Plex Companion endpoint set.
- `Sources/Mistglow/Plex/PlexAVPlayerRenderer.swift` - dual `Process` FFmpeg (video + audio), BGR24 pipes.
- `Sources/Mistglow/Plex/PlexPlaybackController.swift` - glues renderer callbacks into `StreamEngine`.
- `Sources/Mistglow/App/AppState.swift` - central `@MainActor` coordinator and lifecycle.

## Protocol / API Insights
Transport is UDP to port 32100 with a default MTU of 1472 and a 2 MB socket send buffer. Commands (from `GroovyCommand`):

- `CLOSE 0x01` - 1 byte.
- `INIT 0x02` - 5 bytes: `[cmd][compression][sampleRate][channels][rgbMode]`.
- `SWITCHRES 0x03` - 26 bytes: `[cmd][pClock UInt64 LE][hActive u16 LE][hBegin][hEnd][hTotal][vActive][vBegin][vEnd][vTotal][interlace u8]`.
- `AUDIO 0x04` - 3-byte header `[cmd][size u16 LE]` followed by PCM payload (s16le stereo 48 kHz).
- `GET_STATUS 0x05`, `BLIT_VSYNC 0x06`, `GET_VERSION 0x08` - defined but secondary.
- `BLIT_FIELD_VSYNC 0x07` - 8 bytes base: `[cmd][frame u32 LE][field u8][vSync u16 LE]`; optional `[compressedSize u32 LE]` extends to 12 bytes; optional `[isDelta=0x01]` extends to 13 bytes.

LZ4 is used selectively: `LZ4Compressor` compresses only when `compressedSize` fits the MTU budget; otherwise uncompressed is sent and the compressed-size field is omitted. Frames/fields are chunked across UDP datagrams by `FrameTransmitter`, a `wireLock` serializes wire access so audio and field packets never interleave mid-blit. There are no sequence numbers, no retransmission, no ACKs - the receiver (Groovy core) resynchronizes by frame/field counter.

## FFmpeg Pipeline
Two independent FFmpeg processes are spawned per play request (not a single muxed pipeline).

Video:
`ffmpeg -re [-ss <offset>] -i <url> -map 0:v:0 -f rawvideo -pix_fmt bgr24 -s <W>x<H> -r <rate> -vsync cfr -v error -nostdin pipe:1`

Audio:
`ffmpeg [-ss <offset>] -i <url> -map 0:a:<idx> -ac 2 -ar 48000 -f s16le -acodec pcm_s16le -v warning -nostdin pipe:1`

Notes for our Go port: output is **BGR24**, not RGB565; for interlaced modelines the controller passes `rate = baseRate * 2` so FFmpeg emits one frame per field; there is **no yadif, tinterlace, or explicit scale filter** in the graph - scaling is implicit from `-s WxH`, and the "interlace" is produced by the host by row-striping the delivered progressive frames inside `StreamEngine`. The audio reader discards roughly the first 300 ms of PCM to compensate for video decoder start-up latency - this is the only explicit A/V alignment.

## Patterns Worth Adopting
- Single modeline-driven clock source: `intervalUs = max(8000, 1e6/fieldRate)` drives a high-priority dispatch timer. Port this as a Go ticker.
- Blit packet family keyed on flags (delta, compressed): clean way to evolve the header without breaking old cores.
- Dual FFmpeg with `-re` and shared `-ss` offset is simpler than managing a muxed pipe and a demuxer in-process.
- `wireLock` serialization of audio and video onto one UDP socket avoids core-side reassembly races; mirror this with a single goroutine owning the `net.UDPConn` write side.
- Lazy subsystem instantiation in `AppState` is a clean lifecycle; our control plane can follow the same shape.

## Pitfalls / Known Issues

### plex.tv discovery - Issue #8 (OPEN)
Title: "MistGlow not discoverable as Plex cast target in app.plex.tv or Plex apps" (opened by SeeThruHead, 2026-04-03, still open). Symptom: the device never shows up as a cast target in app.plex.tv or the Plex mobile apps. Root cause in the issue thread: "MistGlow only advertises via local GDM, which app.plex.tv doesn't use - it relies on cloud-registered devices." Confirmed by reading `PlexGDMAdvertiser.swift`: it is a **passive** responder on `239.0.0.250:32412` only; there is no plex.tv sign-in, no `plex.tv/devices.xml` POST, no `X-Plex-Token`-authenticated registration anywhere in the tree. What works today: Plex Web in a browser on the same LAN finds it; `/clients` on a local PMS lists it; manual `curl` against port 3005 triggers playback. Status: no fix committed; the maintainer has not announced a plex.tv publish path. **This is the gap our dual discovery (GDM + plex.tv) closes.**

### A/V drift at steady state
`StreamEngine.swift` runs the video timer on `sendQueue` at field-rate microseconds and the audio timer on `audioQueue` at a **fixed 10 ms interval**. `audioLastCallbackTime` is recorded but never read; there is no PTS comparison, no drift correction, no resampling. Once the initial 300 ms audio discard lines things up, the two clocks free-run. README acknowledges "audio crackling during Plex playback under investigation" and "frame dropping/doubling may occur". Issue #2 (HD audio sync, closed 2026-03-14) was filed against this and closed without a public root-cause write-up.

### Field order
Hardcoded **top-field-first** with field 0 = even rows (`y*2`), field 1 = odd rows (`y*2+1`). No BFF option. Fine for NTSC 480i but a trap if we ever aim at 576i PAL, which is conventionally BFF.

### Other closed issues
#1 macOS Tahoe launch crash, #3 PAL/NTSC auto-switch, #4 Dark-mode-off UI, #5 "Plex Receiver not being found" (closed without public root cause - likely overlaps with #8). Open #6 desktop overlay feature request, #7 subtitles not rendered during Plex casting (FFmpeg does not burn subs in - we will inherit this unless we add `subtitles=` to the filter graph).

## Notes for Our Go Implementation

**Control plane** (cheap, stateful, goroutine-per-concern):
- HTTP server on :3005 replicating `/resources`, `/player/playback/{playMedia,stop,pause,play,seekTo,skipNext,skipPrevious,stepBack,stepForward}`, `/player/timeline`, `/player/timeline/poll` (~950 ms long-poll), `/player/timeline/subscribe`, `/player/navigation/*`. Echo the max received `commandID`.
- Discovery: GDM responder on `239.0.0.250:32412` (port it from `PlexGDMAdvertiser`) **plus** the plex.tv publish path Mistglow lacks - sign-in, `POST /devices.xml`, periodic `/api/v2/resources` heartbeat with `X-Plex-Token`.
- Session/PlayQueue manager and a modeline picker that drives SWITCHRES.
- FFmpeg lifecycle: exec two `os/exec.Cmd` (video BGR24, audio s16le 48 kHz stereo) with shared `-ss`. Keep the 300 ms audio prelude discard - it is the only alignment they have and it works for initial sync.

**Data plane** (hot path, minimal allocation):
- One goroutine owns the UDP socket; a `sync.Mutex` around writes mirrors `wireLock`. 2 MB `SO_SNDBUF`.
- `time.Ticker` at `max(8ms, 1e6/fieldRate us)` for the field pump; reuse buffers; build `BLIT_FIELD_VSYNC` headers from a packed struct with `encoding/binary.LittleEndian`.
- Row-stripe the incoming BGR24 into field 0/field 1 buffers in place; cache field 1 on even ticks, re-send on odd.

**Swift idioms that don't translate cleanly**: Swift `actor` isolation - replicate with explicit channels and a single owner goroutine per resource. `DispatchSourceTimer` with sub-millisecond cadence - Go's ticker is coarser; consider `time.AfterFunc` loops or a spin-sleep hybrid, and measure. `@MainActor` observable state - wire through a small event-bus channel to the HTTP handlers.

**What to actively avoid**: the free-running audio vs video clocks. Add a PTS-driven correction: tag each BGR24 frame and PCM block with the FFmpeg wall-clock or progress readout (`-progress pipe:2`), and either drop/duplicate a field or resample audio in ~50 ms windows when skew exceeds a threshold. This is the steady-state drift bug Mistglow ships with and the single biggest quality win available to us.
