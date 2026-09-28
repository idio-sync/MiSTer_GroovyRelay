# GroovyNLC core support — design

**Date:** 2026-09-28 (rev 2, after spec review)
**Status:** Approved (brainstorm), pending implementation plans
**Scope:** Support the verbst/Groovy_MiSTer fork ("GroovyNLC") alongside the
original psakhis/Groovy_MiSTer core, including its NLC video codec.

## 1. Background

GroovyNLC (https://github.com/verbst/Groovy_MiSTer, pinned at commit
`e60f52a`, release v1.4) forks the original core at `109908c`. It installs
side by side with stock Groovy (`[GroovyNLC] main=MiSTer_groovyNLC` in
MiSTer.ini) and keeps the same UDP ports (32100 video, 32101 inputs).
Codec source is bit-identical from tag v1.2 through v1.4.

### 1.1 Wire differences vs the original core

Unchanged: command IDs 1–8, the 13-byte ACK and its status bits,
BLIT_FIELD_VSYNC header lengths 8/9/12/13, AUDIO, GET_STATUS (both cores
answer it with `sendACK(0,0)`), 1472-byte payload chunking.

Changed (fork `hps_linux/src/support/groovy/groovy.cpp`, "F:"; original
core at `109908c`, "U:"):

| # | Change | Detail |
|---|--------|--------|
| a | `GET_VERSION` reply | 1-byte reply is `2` (U: `1`). F:128, F:1277-1287. `>= 2` means "protocol v2", not a guarantee that every NLC pack mode matches the installed rbf. |
| b | INIT length 6 | byte[5] = capability flags: `0x01` INPUTS_V2, `0x02` RUMBLE, `0x04` KEEPALIVE. F:424-431, F:2373-2391. U accepts only 4/5 bytes and drops a 6-byte INIT with no ACK. **The bridge does not use it** (§3.2). |
| c | INIT byte[1] bitfield | Parsed for 4/5/6-byte INITs alike (F:2380, `setInit` F:1406-1414): bits[1:0] codec (0 raw, 1 LZ4, 2 NLC, 3→raw); NLC only: [3:2] NEAR 0–3, [4] colour (1 = YCoCg), [6:5] display mode, [7] pack (1 = Rice, 0 = TILED). Values 0/1 are identical on both cores. |
| d | SWITCHRES is ACKed | `sendACK(0,0)` after SWITCHRES, F:2393-2403. Indistinguishable from the INIT and GET_STATUS ACKs. The fork's client waits for it and retries SWITCHRES 3× (api/groovymister.cpp:937-958). |
| e | Session gate (v1.4 only) | While no session is open (including at boot), commands other than INIT / GET_VERSION / CLOSE are ignored. F:2339-2371. |
| f | Idle reaping | v1.4: closes an idle session only if the client set CAP_KEEPALIVE (timeout 5/10/15 s/off via OSD, F:595-596, F:3116-3128). **v1.1–v1.3 close any idle session** and, lacking the §1.1(e) gate, keep processing blits against a freed session. Any received datagram counts as activity. |

### 1.2 Where the bridge actually goes silent

Pause tears the data plane down: `Manager.pauseLocked` cancels the plane
(internal/core/manager.go:1171-1185), `Plane.Run` sends CLOSE on
`ctx.Done` (internal/dataplane/plane.go:1102-1105), and resume performs a
fresh INIT. Inside the tick loop every tick sends a blit or a 9-byte dup
(plane.go:1258, 1279). So neither pause nor playback is ever silent.

The one silent window is the **prebuffer**: after SWITCHRES and before the
tick loop (plane.go:979-982), bounded by `defaultPrebufferTimeoutMs =
5000` (plane.go:414). That equals the fork's shortest idle timeout. A slow
Plex transcode start can therefore be reaped on fork v1.1–v1.3, after
which the session is dead (and on those versions, unsafe). §3.3 covers
this window.

### 1.3 Zero-size compressed blits (affects both cores today)

While a compressed codec is active (`blitCompression != 0`), both cores
read a 9-byte dup header and an 8-byte raw header as a compressed blit
of size 0: the dup flag is only honoured when `!blitCompression`
(F:2455-2458; U `setBlit` 1198-1223, F:1503-1520). The next datagram then
trips the lost-packet abort path (F:2295-2305).

The bridge emits these today under LZ4: `sendDuplicate` on every underrun
tick (plane.go:1949-1959) and the raw fallback when LZ4 cannot compress a
field (plane.go:1494-1499). **This is a pre-existing LZ4 bug, out of scope
for this spec** and tracked separately. NLC must not repeat it (§5.2).

### 1.4 NLC codec

Near-lossless per-scanline codec: RGB → YCoCg-R (reversible) → per-plane
MED prediction → NEAR quantisation (closed loop) → TILED or Golomb-Rice
packing. Wire format v2: each scanline is an 8-byte header of four
little-endian u16 padded segment lengths, followed by one 64-bit-aligned
segment per plane, bits packed LSB-first. Reference: fork
`api/nlc_codec.{h,cpp}` (433 lines, C++11: uses `std::thread`).

Encoder parameters are **not** on the wire; the host must match the fork
client's `buildNlcParams` (api/groovymister.cpp:297-308) exactly: tile =
16, width_bits = 4, rice_k = −1 (adaptive per tile), colour = YCoCg,
pixel format RGB888. The client's INIT byte is
`2 | near<<2 | 1<<4 | dispMode<<5 | rice<<7` (:836-837) with dispMode
default 2 (:146). Interlaced frames are encoded per field, height halved
(:910-913). Transport reuses the LZ4 blit shape: a 12-byte header with the
encoded size at [8..11], payload in 1472-byte chunks; the client never
sends 13-byte delta blits under NLC (:1086-1105). The fork README requires
OSD "Volatile framebuffer" = Off, and Rice needs a matching rbf (README
"Garbage picture" troubleshooting row), which the host cannot detect.

**480i is unverified.** Groovy.sv:1639-1640 notes "NLC runs
progressive-only for now" for the streaming path; display mode 2, which
we send, addresses odd fields separately (Groovy.sv:1865-1867), so 480i
may work, but the README only documents 640×480p. The bridge's default
modeline is NTSC 480i.

### 1.5 License caveat

The fork's `LICENSE` is GPL-2.0 text, as is the original core's at
`109908c`. `nlc_codec.{h,cpp}` carry no license header. Other fork files
(Groovy.sv:3-6, hps_linux/src/menu.cpp) say "version 2 … or (at your
option) any later version", which suggests but does not establish
v2-or-later for the codec. This project is GPL-3.0.

The Go port is a derivative work; if the codec is GPL-2.0-only it cannot
be combined into this project. The vendored C reference used only as a
separate test-vector tool is closer to aggregation. **Confirm "v2 or
later" with the fork author before merging Part 2.** Part 1 contains no
fork-derived code. (Separately, README.md:272 calls Groovy_MiSTer a
"GPL-3 reference", which is inaccurate; fix in Part 1's README edit.)

## 2. Goals / non-goals

Goals:
- Detect which core is on the other end and report it.
- Keep sessions alive through the prebuffer window on every fork version.
- Offer NLC as a selectable codec on GroovyNLC, bit-exact with the FPGA
  decoder.
- No behaviour change for users on the original core or with default
  settings, other than harmless GET_STATUS pings during a long prebuffer.

Non-goals:
- The 6-byte INIT and capability flags (inputs v2, rumble, keepalive).
- Recovering from a core switch in the middle of a cast. It surfaces as
  the existing echo-stall warnings; the next cast re-probes.
- Fixing the pre-existing LZ4 zero-size-blit bug (§1.3).
- A GroovyNLC-aware "Launch Groovy" button. `internal/misterctl/launcher.go:44`
  hard-codes the stock rbf; the README tells GroovyNLC users to launch
  their core from the MiSTer menu.
- 31 kHz / 480p modes; GLOBAL pack; RGB-direct colour; RGBA/RGB565 under
  NLC.
- Making NLC the default (see §4.2).

## 3. Core detection and session lifecycle (Part 1)

### 3.1 Version probe

New `groovynet.(*Sender).ProbeVersion(timeout) (int, error)`, following
`SendInitAwaitACK`'s contract (sender.go:312-316): it must be called with
no Drainer running, flushes stale datagrams first (`flushStaleDatagrams`,
sender.go:360-374), sends `GET_VERSION` (`0x08`), and waits up to
`timeout` for a datagram of exactly 1 byte. Datagrams of any other length
(for example a stray 13-byte ACK) are discarded and the wait continues.
Returns the reply byte, or 0 on timeout.

`Plane.Run` calls it immediately before INIT with a 200 ms timeout, on
every run. There is no cache: the Sender's destination is fixed for the
process (mister.host is ScopeRestartBridge), and every plane exit sends
CLOSE, so each cast (including each resume) is a fresh session in which
the user may have switched cores.

- Reply `1`, or timeout → `CoreGroovy`.
- Reply `>= 2` → `CoreGroovyNLC`.

### 3.2 INIT

Always the existing 5-byte INIT on both cores. byte[1] carries the
effective codec (§4.3), which the fork parses from a 5-byte INIT
(§1.1c). No capability flags are sent, so fork v1.4 never idle-reaps the
bridge, exactly as today.

### 3.3 Keepalive

`Plane.Run` starts a keepalive goroutine after the INIT ACK and stops it
when `Run` returns. The plane records the time of every datagram it sends
(blit header, dup, audio, keepalive) in an atomic. The goroutine wakes
every 500 ms and, if nothing has been sent for ≥ 2 s, sends a 1-byte
`GET_STATUS`. It runs regardless of the detected core: the original core
answers GET_STATUS harmlessly, and it protects fork v1.1–v1.3 during the
prebuffer (§1.2). In steady playback it never fires.

Keepalive sends use the same `Sender` as the rest of the plane; `Send`
must already be safe for concurrent use with the tick loop (verify during
planning; if not, route through a mutex or the tick loop's select).

### 3.4 ACKs with FrameEcho == 0

The INIT, SWITCHRES and GET_STATUS ACKs all carry `FrameEcho == 0`. The
Run loop currently treats any `FrameEcho != lastEcho` as echo movement
(plane.go:1115-1123), which would reset `lastEcho` and resync delta
history on every keepalive. Change: an ACK with `FrameEcho == 0` still
updates `audioReady`, `fpgaFrame`, `lastACKUnix` and distress status, but
never counts as echo movement, parity evidence or frames-ahead input.

During the prebuffer the ACK channel (capacity 4, plane.go:937) is not
drained. The prebuffer wait must drain it non-blockingly (applying the
same `FrameEcho == 0` rules), so SWITCHRES and keepalive ACKs do not fill
it and log "ack channel full" (drainer.go:69-73).

The SWITCHRES ACK is not counted or gated on. Retrying SWITCHRES the way
the fork's client does is left for hardware testing to justify.

### 3.5 Surfacing

`Plane` exposes `Core() CoreKind` and `EffectiveCodec() Codec` backed by
atomics set in `Run` after the probe. The Manager reads them without
holding `mu`, the same way it reads `AudioScopes` (manager.go:1326-1330).
They appear in:

- the plane start log line (`core=groovy|groovynlc core_version=N
  codec=raw|lz4|nlc`);
- the status snapshot and SSE status event, as `mister_core` and
  `video_codec`;
- the meter label (`internal/chassis/meter.go:405-415`), which switches
  from reading config (`internal/core/meter.go:57-58`) to the active
  plane's effective codec, falling back to the configured one when idle.

## 4. Codec selection and config

### 4.1 Fields (`[bridge.video]`)

| Key | Values | Default | Scope | Part |
|-----|--------|---------|-------|------|
| `codec` | `auto` \| `raw` \| `lz4` (+ `nlc` in Part 2) | `auto` | ScopeRestartCast | 1 |
| `nlc_near` | `0`–`3` (0 = lossless) | `0` | ScopeRestartCast | 2 |
| `nlc_pack` | `tiled` \| `rice` | `tiled` | ScopeRestartCast | 2 |
| `delta_lz4_enabled` | bool (unchanged) | `true` | ScopeRestartCast | — |

`lz4_enabled` is removed from `BridgeConfig.Video`, the `core` session
types (types.go:330) and `dataplane.PlaneConfig` (plane.go:331), replaced
by `Codec` (and, in Part 2, `NLCNear`, `NLCPack`). `delta_lz4_enabled`
applies only when the effective codec is LZ4.

Validation rejects unknown `codec` / `nlc_pack` values and `nlc_near`
outside 0–3 before any disk write. An empty `Codec` validates and
resolves as `auto`, because many constructors and tests build
`VideoConfig` without it and `BridgeSaver` validates every candidate
(bridge_saver.go:242).

`nlc_pack` defaults to `tiled` because Rice needs a matching rbf that the
host cannot detect (§1.4).

### 4.2 Resolution

A pure function `resolveCodec(configured, core) → (effective, warning)`
runs in `Plane.Run` after the probe:

| configured | CoreGroovy | CoreGroovyNLC |
|------------|-----------|---------------|
| `auto` / `""` | lz4 | lz4 |
| `raw` | raw | raw |
| `lz4` | lz4 | lz4 |
| `nlc` (Part 2) | lz4 + warning | nlc |

`auto` deliberately resolves to LZ4 on both cores until NLC passes the
§6.5 hardware checks. Flipping `auto` to NLC on GroovyNLC later is a
one-cell change. The warning is logged and surfaced as a receiver UI
notice.

Because `effective` is known only in `Run`, codec-dependent allocation
moves out of `NewPlane` (plane.go:569-597): `NewPlane` no longer decides
delta support from config; `Run` allocates LZ4/delta scratch or NLC
scratch after resolution.

### 4.3 INIT byte[1]

- raw → `0`, lz4 → `1`.
- nlc → `2 | near<<2 | 1<<4 | 2<<5 | pack<<7`, with `pack` = 1 for rice
  and 0 for tiled, built by `groovy.NLCCompressionByte(near, pack)`
  (Part 2).

### 4.4 Migration

The existing `migration.go` handles only whole-file flat→sectioned
migration keyed on top-level legacy keys (migration.go:28-48, 92-162);
there is no in-section rename pattern, so this adds one:

1. Flat→sectioned `Migrate()` maps `old.LZ4Enabled` to `Codec`
   (migration.go:116): `false` → `raw`, `true` → `auto`.
2. The sectioned loader reads `lz4_enabled` into a legacy-only field. If
   `codec` is not defined in `[bridge.video]` (checked with
   `toml.MetaData.IsDefined`, the `Sectioned.meta` at config.go:345-351)
   and `lz4_enabled` is, it applies the same mapping.
3. The legacy key disappears on the next save, because
   `marshalBridgeSection` re-encodes the struct without it.

Consequence to note in the README: `lz4_enabled = true` (the old default)
becomes `auto`, so those users follow `auto` if it is ever flipped to
NLC. Users who want LZ4 pinned set `codec = "lz4"`.

Touch points: internal/config/config.go:180-181, migration.go:116 and
:294-295, example.toml:16, chassis/settings.go:328/603/660,
templates/settings-av.html:47-54, uiserver/bridge_saver.go:527-531 and
:652, core/types.go:330-331, core/meter.go:57-58, chassis/meter.go:409,
core/manager.go:934-935, dataplane/plane.go:331-332 and the LZ4 branches.

### 4.5 Settings UI

The LZ4 checkbox becomes a codec select: Auto / Raw / LZ4 in Part 1, plus
NLC in Part 2. The NLC near and pack controls (Part 2) are enabled only
when the codec is `nlc`. The delta-LZ4 checkbox is enabled only when the
codec is `auto` or `lz4`.

## 5. NLC encoder and data plane (Part 2)

### 5.1 Package `internal/groovy/nlc`

Pure Go, no dependencies, mirroring `nlc_codec.cpp` at fork `e60f52a`.

- `Params{Width, Height, Near int; Pack Pack}` with `Pack` ∈
  {`PackTiled`, `PackRice`}. Tile 16, width_bits 4, rice_k −1, YCoCg and
  RGB888 are unexported constants.
- `MaxEncodedSize(Params) int`.
- `Encoder` owns reusable scratch (plane buffers, residuals, per-plane
  per-line segment buffers). `(*Encoder).EncodeInto(dst, src []byte)
  (int, error)` performs no allocations in steady state.
- Encoding is **plane-parallel from the start**, mirroring the reference:
  one goroutine per plane (Y, Co, Cg) encodes all lines of that plane
  into per-line segments (within a plane each line depends on the
  previous reconstructed row, so lines cannot be split), then a serial
  pass assembles each line's 8-byte header and three segments. The output
  is identical to a serial encode.
- `Decode(dst, src []byte, Params) error`, used by fake-mister (§6.2) and
  tests.

### 5.2 Data-plane behaviour under NLC

`sendField` switches on the effective codec instead of `LZ4Enabled`.
The raw and LZ4 paths keep their current behaviour (including the §1.3
bug). Under NLC:

1. Encode the field (field height for interlaced modes) into NLC scratch.
2. Send a 12-byte header whose size field is the encoded byte count,
   then the payload.
3. Never send an 8-byte raw header, a 9-byte dup or a 13-byte delta
   (§1.3). NLC is always sent, even when larger than raw.
4. **Underrun:** where the tick loop would call `sendDuplicate`, send
   nothing for that tick but still advance `frameNum`. The core keeps
   scanning its last field (non-volatile framebuffer), and its frame gate
   only requires `new_frame > PoC_frame_lz4` (setInit comment,
   F:1432-1442). The keepalive (§3.3) covers a long underrun.
5. If the encoder returns an error (impossible with validated params),
   skip the field as in step 4, increment a counter and log at a rate
   limit.

Under NLC, delta-LZ4 is off; field-history bookkeeping is kept only when
field diagnostics are enabled.

### 5.3 Stats

Per-field stats rename the `lz4` duration to a codec-neutral `encode`
duration (log key `max_encode_ms`, replacing `max_lz4_ms`). Compressed
bytes and ratio reporting cover NLC too.

### 5.4 Performance budget

NLC encode must fit inside the existing `sendField` budget threshold
(84% of the field period, `fieldBudgetThreshold`) together with pacing
and send time. The reference reports 12–18 ms per frame for a serial
encode, which is why §5.1 is plane-parallel. `BenchmarkEncode` (720×240,
both packs, near 0) sets the concrete target during Part 2: p99 encode
≤ 5 ms on the dev machine. The README states the CPU cost and that NAS-
class hosts should benchmark before choosing NLC.

## 6. Testing

### 6.1 Golden vectors (bit-exactness, Part 2)

`tools/nlcvectors/` contains a C++11 harness and a vendored copy of
`nlc_codec.{h,cpp}` from fork `e60f52a` with its license notice and
source URL. `make nlc-vectors` builds it with local clang/gcc (`-std=c++11
-pthread`) and writes `internal/groovy/nlc/testdata/`. It is not run in
CI; the committed files are the contract. (The fork's own
`tools/nlc_vectors.cpp` emits one synthetic image as hex for the RTL
testbench, so a separate harness is needed.)

Layout, to keep the testdata small: each input image stored once
(gzip); per case, the encoded bytes (gzip) and, for near > 0, a SHA-256
of the reference decoder's output.

Input matrix: flat, gradient, sharp edges, noise, alternating saturated
primaries (Co/Cg extremes), content that forces the Rice escape path
(residual ≥ 20<<k), one real 720×240 field, and a width not divisible by
16; each × near {0,1,2,3} × pack {tiled, rice}.

Go tests require byte-identical encoder output for every case; Go decode
is exact at near 0 and matches the reference decode hash at near 1–3.

### 6.2 Fake-mister core modes

`internal/fakemister` `ParseCommand` learns `GET_VERSION` and
`GET_STATUS` (today rejected as unknown, listener.go:297+). Both modes
ACK GET_STATUS with `sendACK(0,0)` semantics.

`cmd/fake-mister -core=groovy|nlc` (default `groovy`):
- `groovy`: replies `1` to GET_VERSION; drops 6-byte INITs without ACK;
  no SWITCHRES ACK.
- `nlc`: replies `2`; ACKs SWITCHRES; parses the byte[1] bitfield.
- `-idle-timeout=D` (either mode, default off): closes a session that
  receives nothing for D and then ignores blits until the next INIT.
  This models "a reaped session stops accepting frames"; it does not
  model v1.1–v1.3's use-after-free.
- Part 2: `nlc` mode decodes NLC payloads via `nlc.Decode`, so
  `-png-every` dumps keep working.

### 6.3 Unit tests

- `resolveCodec` table, including `""`.
- `ProbeVersion`: reply 1, reply 2, timeout, stray 13-byte ACK before
  the reply.
- `FrameEcho == 0` ACKs do not move `lastEcho` or frames-ahead.
- Keepalive: fires after 2 s of silence, never during steady sends, and
  stops when `Run` returns.
- Config: both migration paths, validation, empty codec.
- Part 2: `NLCCompressionByte`; the NLC underrun path sends no datagram
  and advances `frameNum`.

### 6.4 Integration tests (`-tags=integration`)

Part 1:
- `-core=nlc`, codec `auto`: plane logs `core=groovynlc codec=lz4`.
- `-core=groovy`: plane logs `core=groovy`.
- `-core=nlc -idle-timeout=1s` with a frame source that withholds the
  first frame for 3 s (prebuffer): frames are accepted afterwards.

Part 2:
- `-core=nlc`, codec `nlc`: decoded fields match the source within the
  NEAR bound.
- `-core=groovy`, codec `nlc`: falls back to LZ4 with a warning.

### 6.5 Manual hardware checks (not automatable)

On a MiSTer running GroovyNLC v1.4 with Volatile framebuffer off:
1. 240p cast, `codec = "nlc"`, near 0, tiled: clean picture; meter shows
   the NLC ratio.
2. Same with rice.
3. 480i cast, `codec = "nlc"` (open risk, §1.4).
4. Pause > 10 s, resume; force a slow start (prebuffer > 5 s).

Checks 1, 3 and 4 passing is the precondition for flipping `auto` to NLC
(§4.2). If 480i fails, NLC stays opt-in and 480i + NLC resolves to LZ4
with a warning.

## 7. Delivery

Two implementation plans against this spec:

1. **Part 1 — detection and plumbing:** §3, §4 without the NLC fields,
   §6.2 minus NLC decode, §6.3 and §6.4 Part 1 items. README section on
   GroovyNLC: MiSTer.ini entry, launching the core from the MiSTer menu,
   Volatile framebuffer, the `codec` setting and its `auto` semantics,
   and the §1.5 license wording fix. Shippable alone.
2. **Part 2 — NLC encoder:** §5, §6.1, `nlc` codec + `nlc_near` +
   `nlc_pack` config/UI, fake-mister NLC decode, the Part 2 tests.
   Gated on the §1.5 license confirmation.
