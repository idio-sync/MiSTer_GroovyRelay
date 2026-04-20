# MiSTerCast

## Metadata
- URL: https://github.com/iequalshane/MiSTerCast
- License: not declared in repo (no `LICENSE` file at root as of this survey)
- Primary language: C++ (`Library/MiSTerCastLib/*`) with a C# WPF front-end (`FrontEnd/*`)
- Activity: small, personal tool; final commit `b7493f9` on `main`. Credits note: "Groovy_MiSTer communication adapted from the Groovy_Mame source" (`renderer_nogpu.h:1`).
- Key files: `Library/MiSTerCastLib/groovymister.{h,cpp}` (wire protocol), `Library/MiSTerCastLib/renderer_nogpu.h` (send-loop driver).

## Purpose
Captures the Windows desktop, resamples it to a low resolution CRT modeline, and streams raw/LZ4 RGB fields plus PCM audio to the Groovy_MiSTer FPGA core over UDP. It is the Windows analog of GroovyMAME's `nogpu` renderer, reused verbatim for general-purpose screen casting.

## Relevance to MiSTer_GroovyRelay
This is our byte-level wire reference. The `GroovyMister` C++ class is effectively the reference client for the Groovy_MiSTer protocol. Port its packet construction to Go verbatim.

## The Five Commands (Byte-Level)
All commands run on a single UDP socket to MiSTer port **32100** (`renderer_nogpu.h:35`, `UDP_PORT 32100`). Multi-byte fields are written with `memcpy` from native `uint16_t/uint32_t/double`, so the wire is **little-endian** (x86 host) with **no padding, no magic, no length prefix**. The command byte is the first byte; the payload follows. The send buffer `m_bufferSend[26]` is reused (`groovymister.h:121`).

Command IDs (`groovymister.cpp:30-37`):
```
CMD_CLOSE=1  CMD_INIT=2  CMD_SWITCHRES=3  CMD_AUDIO=4
CMD_GET_STATUS=5  CMD_BLIT_VSYNC=6  CMD_BLIT_FIELD_VSYNC=7  CMD_GET_VERSION=8
```

### CLOSE (1 byte) -- `groovymister.cpp:167-194`
- `[0]=0x01`. No payload. No reply required; client just tears down the socket.

### INIT (5 bytes) -- `groovymister.cpp:206-492`
- `[0]=0x02`
- `[1]=lz4Frames` (0=RAW, 1=LZ4; the field is re-used later as an adaptive mode selector 1..6)
- `[2]=soundRate` enum: `0=off, 1=22050, 2=44100, 3=48000`
- `[3]=soundChan` (1=mono, 2=stereo)
- `[4]=rgbMode` (0=RGB888, 1=RGBA8888/32-bit, 2=RGB565/16-bit -- deduced from `m_RGBSize = rgb==1 ? w*h*4 : rgb==2 ? w*h*2 : w*h*3`, `groovymister.cpp:501`)
- **Reply:** a 13-byte status packet (see BLIT ACK). MiSTerCast times out INIT at 60 ms (`groovymister.cpp:460`).

### SWITCHRES (26 bytes) -- `groovymister.cpp:494-529`
```
off len field         type
  0   1  cmd=0x03
  1   8  pClock        double (MHz)
  9   2  hActive       uint16
 11   2  hBegin        uint16
 13   2  hEnd          uint16
 15   2  hTotal        uint16
 17   2  vActive       uint16
 19   2  vBegin        uint16
 21   2  vEnd          uint16
 23   2  vTotal        uint16
 25   1  interlace     uint8 (0=progressive, 1=interlaced; `interlace_modeline` lives in an adjacent byte computation)
```
Modeline conventions follow Switchres (`renderer_nogpu.h:392-403`). Example 480i entry from `FrontEnd/modelines.dat`. No reply. Client caches `m_widthTime = 10*round(hTotal/pClock)` nanoseconds-per-line and `m_frameTime = widthTime*vTotal >> interlace`.

### AUDIO (3-byte header + PCM payload) -- `groovymister.cpp:670-682`
- Header: `[0]=0x04`, `[1..2]=soundSize` uint16 (bytes of PCM following).
- Sent to same UDP socket, then `SendStream(whichBuffer=1, ..., bytesToSend=soundSize)` fragments the PCM payload across MTU-sized datagrams.
- Call site passes `samples_this_frame << 1` (`renderer_nogpu.h:462`), i.e. stereo 16-bit PCM where each sample-pair = 4 bytes; payload is raw signed 16-bit little-endian interleaved LRLR.
- Only sent if `fpga.audio==1` (core reports audio on) and `m_isConnected`. No ACK.

### BLIT_FIELD_VSYNC (8/9/12/13-byte header + RGB/LZ4 payload) -- `groovymister.cpp:531-668`
Header layout:
```
off len field         type
  0   1  cmd=0x07
  1   4  frame         uint32 (frame counter, client-assigned, monotonic)
  5   1  field         uint8  (0=even/progressive, 1=odd)
  6   2  vSync         uint16 (raster line at which MiSTer should sync; 0 means MiSTer chooses)
```
Then a **variable tail** chosen by compression state:
- **Raw uncompressed full field:** send 8 bytes only, then stream `m_RGBSize` bytes.
- **LZ4 compressed full field:** add `[8..11] = cSize` uint32, header length 12, stream `cSize` bytes from `m_pBufferLZ4[0]`.
- **LZ4 delta field:** add `[8..11] = cSizeDelta`, `[12]=0x01` (frame_delta marker), header length 13, stream `cSizeDelta` bytes from `m_pBufferLZ4[1]`.
- **Duplicate field (no pixel change):** add `[8]=0x01` (frame_dup), header length 9, and send no payload.

The payload (when present) is streamed by `SendStream` as back-to-back UDP datagrams each of size `m_mtu = mtu - 28` (default `1500-28=1472`) -- no per-chunk header, no sequence number, just raw byte stream split at MTU (`groovymister.cpp:993-1040`). `IP_DONTFRAGMENT` is set (`groovymister.cpp:241`) so the OS cannot re-fragment.

**Reply:** a 13-byte status datagram on every received blit (see setFpgaStatus below). This is the *echo/telemetry channel*, NOT a permission-to-send signal.

### Status reply (13 bytes) -- `groovymister.cpp:1082-1101`
```
 0  4  frameEcho   uint32   (echoed frame id)
 4  2  vCountEcho  uint16   (raster line when echo emitted)
 6  4  frame       uint32   (FPGA's current frame on the GPU side)
10  2  vCount      uint16   (FPGA's current raster line)
12  1  bitfield    uint8    bit0=vramReady, bit1=vramEndFrame, bit2=vramSynced,
                             bit3=vgaFrameskip, bit4=vgaVblank, bit5=vgaF1,
                             bit6=audio, bit7=vramQueue
```
`GET_VERSION` reply is a single byte (core version).

## Clock Discipline: Push vs Pull
**The protocol is PUSH. Client free-runs on a modeline-derived clock and uses ACKs only as drift correction.**

Evidence, from the send loop in `renderer_nogpu.h`:
```cpp
// line 318-319
groovyMister.CmdBlit(m_frame, m_field, 0 /*m_vsync_scanline*/, 15000, 0);
groovyMister.WaitSync();
```
`WaitSync` (`groovymister.cpp:779-808`) is a busy-wait spin that sleeps until `realTime >= m_frameTime - emulationTime + rasterDrift`. `m_frameTime` was computed at SWITCHRES from the modeline (`m_widthTime * vTotal >> interlace`). There is **no `recv()` in the critical path** -- `DiffTimeRaster` calls `getACK(0)` with a zero timeout, which returns immediately and only updates drift if an echo happens to be queued.

In other words: the client sets its own 60 Hz (or 59.94, or whatever the modeline says) tempo, corrects phase against the FPGA's echoed frame/vCount when available, and never blocks waiting for a "ready" packet. The name `BLIT_FIELD_VSYNC` refers to the MiSTer *video* vsync raster, not a transport handshake.

`vSync` in the blit header is the *requested raster line at which the FPGA should swap* -- also push-side policy, not a pulled value.

**This is unambiguous.** The only inbound path (receive queue via `getACK`) is purely informational: frame echo for timing, FPGA status bits, optional version reply.

## LZ4 Framing
- Signaled at session level by the INIT byte `[1]` (`0`=raw, `1..6`=LZ4 modes including HC and delta-adaptive; `groovymister.cpp:556-610`).
- **Per-field, not per-packet.** Compression happens once on the whole field with `LZ4_compress_default` or `LZ4_compress_HC` into `m_pBufferLZ4[0]` (field) or `[1]` (delta-vs-previous-field).
- `cSize` in the BLIT header tells MiSTer the compressed length; the receiver LZ4-decompresses the concatenation of all MTU chunks for that field back to `m_RGBSize` bytes.
- **Delta marker:** BLIT header byte `[12]=0x01` means "payload is LZ4 of (current XOR previous)". Byte `[8]=0x01` with header length 9 means "duplicate previous field, no payload".
- No LZ4 magic/frame header -- it's raw LZ4 block format, length-prefixed externally by `cSize`.

## MTU and Fragmentation
- Effective UDP payload: `m_mtu = mtu_arg - 28` (IP+UDP header). Default 1472 bytes (`groovymister.h:25`).
- `IP_DONTFRAGMENT` is set, so over-size datagrams will drop rather than fragment -- the client hand-slices.
- A field becomes N = ceil(payloadLen/mtu) datagrams with **no per-chunk header, no sequence number, no total count**. The FPGA reassembles by simple byte concatenation; it learned the expected length from the preceding BLIT header's `cSize` (or `m_RGBSize` for raw).
- Lost/reordered UDP packets therefore corrupt the field silently -- fine on LAN, broken on Wi-Fi. `BUFFER_SLICES=846` upper-bounds per-field chunks.
- Congestion control: if a single field's `bytesToSend >= 500000`, client busy-waits 11 ms before the next blit (`K_CONGESTION_SIZE`, `K_CONGESTION_TIME`; `groovymister.cpp:56-57, 647-657`).

## Patterns Worth Adopting
- **Pre-registered, reused send buffers.** `m_bufferSend[26]` for control, pre-allocated aligned buffers per field. We can skip Windows RIO; Go's `net.UDPConn.WriteToUDP` with a pooled `[]byte` is enough.
- **Double-buffer per field** (`m_pBufferBlit[2]`, `m_pBufferLZ4[2]`) so the compressor can run while the previous frame drains.
- **Drift-corrected free-run clock.** Our pacing loop should compute `frameTime` from the modeline exactly as MiSTerCast does and treat any inbound ACKs as hints, not gates.
- **Congestion guard.** Keep the >500 KB / 11 ms back-off; useful for our 480i throughput.
- **LZ4 adaptive modes** (`lz4Frames` 1..6) are over-engineered for our fixed 480i use case; start with mode 1 (default LZ4).

## Notes for Our Go Implementation
All of this is **data plane**. In our Go binary:
- `packet.go`: byte-exact builders for the five commands. Use `encoding/binary.LittleEndian.PutUint16/32/64`. Keep layouts as exported constants for tests.
- `udp.go`: single `*net.UDPConn` bound to MiSTer `:32100`; set `IP_DONTFRAGMENT` via `syscall.SetsockoptInt(fd, IPPROTO_IP, IP_MTU_DISCOVER, IP_PMTUDISC_DO)` on Linux (Docker host) -- important to preserve MiSTerCast's no-IP-fragmentation guarantee.
- `clock.go`: 60 Hz (or modeline-driven) ticker. **No vsync listener** is required -- the protocol is push. Tick the blit cadence from a `time.Ticker` derived from `m_frameTime = 10 * hTotal / pClock * vTotal` (nanoseconds).
- `ack.go`: non-blocking `ReadFromUDP` drain into an `fpgaStatus` struct. Feed `frameEcho/vCountEcho` back into the clock as phase correction, not as a gate.
- `lz4.go`: use `github.com/pierrec/lz4/v4` block-mode compressor; one scratch buffer per field.
- `fragment.go`: slice LZ4/raw payload into `mtu-28` chunks and call `conn.Write` in a loop; no sub-packet framing.
- `audio.go`: stereo S16LE from FFmpeg's `-f s16le -ar 48000 -ac 2` piped straight into the AUDIO payload; header is 3 bytes, payload chunk-sliced like BLIT.

Fixed 480i NTSC target maps to a known modeline (pClock 13.5, hTotal 858, vTotal 525, interlace=1); precompute and hardcode as a fallback if Plex doesn't negotiate an alternative.
