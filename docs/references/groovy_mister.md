# Groovy_MiSTer

## Metadata
- URL: https://github.com/psakhis/Groovy_MiSTer
- License: GPL-2.0
- Primary language: C (DE10-Nano userland daemon) + SystemVerilog RTL (`Groovy.sv`, `rtl/`)
- Last push: 2024-09-23. Not archived; wiki enabled but empty.
- Maintenance: **unmaintained** upstream after the author's passing in 2024. 13 forks, none canonical. Companion userland source: `psakhis/Main_MiSTer:support/groovy/groovy.cpp`.
- Authoritative headers: `api/groovymister.h`, `api/groovymister.cpp` (client library). Receiver: `Main_MiSTer:support/groovy/groovy.cpp`.
- Protocol version: `#define GROOVY_VERSION 1` (receiver). Library string: `GROOVYMISTER_VERSION "1.0.0"`.

## Purpose
"Analog GPU for CRTs aiming for very low subframe latency" (Readme). An FPGA core (`Groovy.rbf`) plus a replacement `MiSTer_groovy` userland binary. Emulators (GroovyMAME, Mednafen emu4crt, RetroArch, 86Box) push RGB frames + audio + switchres modelines over UDP; the ARM hands them to the FPGA via SPI and DDR shared memory; VGA out to a 15 kHz CRT.

## Protocol Specification
Two relevant UDP listeners on the MiSTer:
- **32100** — control + data plane (commands, video, audio, ACKs).
- **32101** — inputs plane (MiSTer -> sender; joystick / PS2). Not used by our relay.
- (32105 is a GMC file-transfer helper, ignore.)

Command IDs, verbatim from both sender and receiver source:

```
#define CMD_CLOSE             1
#define CMD_INIT              2
#define CMD_SWITCHRES         3
#define CMD_AUDIO             4
#define CMD_GET_STATUS        5
#define CMD_BLIT_VSYNC        6   // deprecated, progressive-only
#define CMD_BLIT_FIELD_VSYNC  7   // current
#define CMD_GET_VERSION       8
```

All multi-byte integers on the wire are **little-endian** (sender uses `memcpy` from native x86/ARM; receiver reassembles LE byte-at-a-time). Byte 0 of every client->server datagram is the command ID. Length disambiguates optional fields — the receiver switches on `len`.

## Command-by-Command Wire Format

### CLOSE (1 byte)
`[0]=0x01`. Sender then closes sockets. No receiver ACK; state resets on next INIT.

### INIT (4 or 5 bytes)
```
[0]  cmd=0x02
[1]  lz4Frames  (0=RAW, 1=LZ4 enabled; sender modes 1..6 toggle LZ4/LZ4HC/delta)
[2]  audioRate  (0=off, 1=22050, 2=44100, 3=48000)
[3]  soundChan  (0=off, 1=mono, 2=stereo)
[4]  rgbMode    (0=RGB888, 1=RGBA8888, 2=RGB565) -- optional; 4-byte INIT implies 0
```
Receiver responds with the 13-byte ACK (see below). This is the handshake: sender `getACK(60)` with 60 ms timeout, failure = tear down. No session ID, no version byte in INIT. Version is retrieved separately with `CMD_GET_VERSION` (1 byte -> 1-byte reply = `GROOVY_VERSION`).

### SWITCHRES (26 bytes)
```
[0]      cmd=0x03
[1..8]   pClock   double (IEEE-754, 8 bytes LE) -- pixel clock in MHz
[9..10]  hActive  uint16
[11..12] hBegin   uint16
[13..14] hEnd     uint16
[15..16] hTotal   uint16
[17..18] vActive  uint16
[19..20] vBegin   uint16
[21..22] vEnd     uint16
[23..24] vTotal   uint16
[25]     interlace uint8 (0=progressive, 1=interlaced, 2=interlaced force field-fb)
```
Fire-and-forget; the receiver does NOT ACK switchres.

### AUDIO (3-byte header + PCM burst)
```
[0]    cmd=0x04
[1..2] soundSize uint16 -- PCM bytes that follow
```
Immediately after, the sender streams `soundSize` bytes of PCM as back-to-back `mtu`-sized UDP datagrams on the same socket (no per-chunk header). Only sent if ACK bit `audio==1`.

### BLIT_FIELD_VSYNC (8/9/12/13-byte header + RGB burst)
```
[0]      cmd=0x07
[1..4]   frame   uint32 (monotonic)
[5]      field   uint8  (0=progressive or top field, 1=bottom field)
[6..7]   vSync   uint16 (raster line to sync with)
[8..11]  csize   uint32 (ONLY present when LZ4 enabled)
[12 or 8] frame_delta uint8 = 0x01 if payload is delta-vs-prior, else absent (full frame)
```
Valid header lengths:
- `8`  — RAW, full-frame
- `9`  — RAW duplicate (delta=1, no payload) or RAW+delta flag
- `12` — LZ4, full-frame
- `13` — LZ4, with delta flag

After the header, `bytesToSend` RGB (or compressed) bytes follow as `mtu`-sized UDP datagrams, back-to-back, concatenated by arrival order on the receiver. **No per-packet header, no sequence number.**

### GET_STATUS / GET_VERSION
`[0]=0x05` -> 13-byte ACK. `[0]=0x08` -> 1-byte reply = `GROOVY_VERSION`.

### ACK (server->client, 13 bytes)
```
[0..3]   frameEcho  uint32 (echoes sender's last frame)
[4..5]   vCountEcho uint16 (echoes sender's requested vSync line)
[6..9]   fpga.frame uint32
[10..11] fpga.vCount uint16
[12]     bitfield:
  bit0 vramReady  bit1 vramEndFrame  bit2 vramSynced  bit3 vgaFrameskip
  bit4 vgaVblank  bit5 vgaF1 (interlace field)  bit6 audio (enabled)  bit7 vramQueue
```
Source: `groovy.cpp:sendACK`, mirrored by `groovymister.cpp:setFpgaStatus`.

## Pixel Format for Video Fields
`rgbMode` from INIT selects the layout:
- `0` = **RGB888**, 3 B/pixel, row-major, no padding. Default.
- `1` = **RGBA8888**, 4 B/pixel (alpha byte sent, ignored by core).
- `2` = **RGB565**, 2 B/pixel, little-endian.

Payload per field = `hActive * vActive * bpp`. For interlaced, `vActive` is already halved per field; `field=0`/`field=1` alternates across two consecutive BLIT_FIELD_VSYNC. No per-line header, no padding, no scanline interleave — the payload is a contiguous top-to-bottom scan of the current field. Byte order is **R, G, B** (not BGR): receiver DMAs straight into FPGA framebuffer DDR.

Target for 480i NTSC: `rgbMode=0`, two BLIT_FIELD_VSYNC per 59.94 Hz displayed frame, each `hActive*240*3` bytes.

## Audio Format
- **Sample rates**: 22050 / 44100 / 48000 Hz (INIT byte [2] = 1/2/3). Zero = disabled. No other rates.
- **Channels**: 1 or 2 via INIT byte [3].
- **Bit depth**: 16-bit signed PCM, little-endian. Confirmed by receiver's buffer-sizing comment `AUDIO_SIZE (8192 * 2 * 2)` // "8192 samples with 2 16bit-channels".
- **Interleaving**: LRLR for stereo.
- **Framing**: 3-byte CMD_AUDIO announces `soundSize` bytes, PCM follows as MTU-sliced datagrams on the same socket, no per-chunk header.

## LZ4 Framing
**Not classic LZ4 frame format.** No magic `04 22 4D 18`, no frame header. Raw LZ4 block output from `LZ4_compress_default` / `LZ4_compress_HC` (upstream `lz4.h`/`lz4hc.h`). Compressed size lives in the BLIT_FIELD_VSYNC header at bytes 8..11. Receiver hands the compressed stream to an on-FPGA LZ4 decompressor via SPI commands `UIO_SET_GROOVY_BLIT_LZ4` (0xf7) / `UIO_SET_GROOVY_BLIT_FIELD_LZ4` (0xf8). Granularity: **one block per field**.

Delta: sender modes 3..6 can emit the compressed XOR-delta between prior and current frame; `frame_delta=0x01` in the header selects it. `frame_dup=1` with header len 9 and zero payload bytes means "repeat prior field."

## Clock Discipline (Push or Pull)
**Hybrid, leans PUSH with ACK-paced sender feedback.** Evidence:

1. Sender sends the BLIT header, then `SendStream(...)` blasts the whole field as back-to-back UDP datagrams with **no per-packet ACK** (`groovymister.cpp:SendStream`).
2. Receiver emits exactly **one ACK per blit** (not per packet), at `groovy.cpp:2069` and `:2103`, immediately after `setBlit(...)`.
3. Sender **does not block** on that ACK. `CmdBlit` returns after `SendStream` + a local congestion delay. ACKs are drained later, non-blockingly, by `getACK(0)` inside `WaitSync` / `DiffTimeRaster`, which uses `fpga.frameEcho` and `fpga.vCount` to time the **next** frame submission against the FPGA's current raster line.
4. **No "ready for next field" signal.** The status bits `vramReady` / `vramQueue` in the ACK are informational; the sender uses them for logging and congestion heuristics, not as a gate.
5. Sender-local congestion throttle: `K_CONGESTION_SIZE=500000` bytes, `K_CONGESTION_TIME=110000` ticks (~11 ms). After any blit above that size, sender stalls ~11 ms before the next. This is sender-local, not a pull from the MiSTer.
6. INIT is the single ACK-gated handshake (60 ms timeout).

**For our relay:** send at the source cadence (NTSC 59.94), drain 13-byte ACKs asynchronously to drive sleep timing, respect the self-imposed congestion backoff after >500 KB transfers. Nothing to block on.

## Maintenance Notes
- Upstream dormant since 2024-09-23. No successor fork is canonical. Reference sender integrations (`antonioginer/GroovyMAME`, `antonioginer/RetroArch` mister branch, `psakhis/86Box` mister) are themselves partly unmaintained.
- Wiki empty. README is the only narrative doc; the protocol is implicit in the two source files above.
- **Unresolved/inferred** (worth confirming with a pcap against a working GroovyMAME):
  - Audio PCM bit depth (16-bit signed LE) — inferred from buffer comment only.
  - LZ4 = raw block, not frame format — inferred from direct `LZ4_compress_default` call path.
  - RGB byte order R,G,B — inferred from DDR DMA + core RTL; sender never byteswaps.
  - Endianness — not documented; deduced from `memcpy` patterns.

## Notes for Our Go Implementation
1. **Single UDP socket on port 32100**, `connect(2)`-style. Commands + video + audio all share it; serialize CMD_AUDIO and BLIT bursts.
2. **Little-endian everywhere** (`binary.LittleEndian.*`). Not `htons`.
3. **Send INIT first, wait ~60 ms for 13-byte ACK** before anything else. No ACK = dead session.
4. **SWITCHRES once per resolution change**, fire-and-forget. For Plex cast, typically once after INIT for the 480i modeline.
5. **BLIT_FIELD_VSYNC header length (8/9/12/13) must be exact** — the receiver uses `len` to disambiguate LZ4 vs RAW vs delta vs dup. Extra bytes will silently drop to `default`.
6. **RGB888 = 3 B/px, no padding, no stride**. 480i at `640x240` per field = 460,800 B ≈ 307 MTU-1472 datagrams blasted back-to-back. Set `SO_SNDBUF>=2 MB` (reference sender uses 2,097,152).
7. **Parse ACKs on a goroutine**; don't block `CmdBlit`. Use them only for next-frame timing. Missing ACKs are not fatal mid-session.
8. **LZ4 = raw block**, not frame format. Use `pierrec/lz4/v4` block API, not `lz4.NewWriter` (which adds frame magic). Put compressed size in header bytes 8..11.
9. **Congestion backoff**: ~11 ms after any blit >500 KB. Mirror this or risk the 100 Mbit DE10-Nano link dropping mid-field (no retransmit).
10. **Audio prerequisite**: only send CMD_AUDIO while ACK bit 6 (`audio`) == 1.
11. **Interlace**: SWITCHRES `interlace=1`, halve `vActive` in the per-field buffer, alternate `field=0`/`field=1` across two successive BLIT_FIELD_VSYNC.
12. **No session/connection ID.** The MiSTer keys off source IP:port from the first INIT. Our container must use a stable source port for the cast's lifetime.
13. LZ4 payload ceiling the receiver allocates: `LZ4_SIZE = 720*576*4 = 1,658,880` B. Our 480i RGB888 field is well below this.
