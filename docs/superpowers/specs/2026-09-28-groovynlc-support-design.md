# GroovyNLC core support — design

**Date:** 2026-09-28
**Status:** Approved (brainstorm), pending implementation plans
**Scope:** Support the verbst/Groovy_MiSTer fork ("GroovyNLC") alongside the
original psakhis/Groovy_MiSTer core, including its NLC video codec.

## 1. Background

GroovyNLC (https://github.com/verbst/Groovy_MiSTer, pinned at commit
`e60f52a`, release v1.4) forks the original core at `109908c`. It installs
side by side with stock Groovy (`[GroovyNLC] main=MiSTer_groovyNLC` in
MiSTer.ini) and keeps the same UDP ports (32100 video, 32101 inputs).

### 1.1 Wire differences vs the original core

Unchanged: command IDs 1–8, the 13-byte ACK and its status bits,
BLIT_FIELD_VSYNC header lengths 8/9/12/13, AUDIO, GET_STATUS, 1472-byte
payload chunking.

Changed (fork `hps_linux/src/support/groovy/groovy.cpp`, "F:"):

| # | Change | Detail |
|---|--------|--------|
| a | `GET_VERSION` reply | 1-byte reply is `2` (original: `1`). F:128, F:1279-1283. |
| b | INIT length 6 | byte[5] = capability flags: `0x01` INPUTS_V2, `0x02` RUMBLE, `0x04` KEEPALIVE. F:424-431, F:2373-2391. The original core silently drops a 6-byte INIT (no ACK). |
| c | INIT byte[1] bitfield | bits[1:0] codec (0 raw, 1 LZ4, 2 NLC, 3→raw). NLC only: [3:2] NEAR 0–3, [4] colour (1 = YCoCg), [6:5] display mode (client default 2), [7] pack (1 = Rice, 0 = TILED). F:1405-1414. Values 0/1 are identical on both cores. |
| d | SWITCHRES is ACKed | Normal 13-byte ACK with frame=0, vsync=0. F:2393-2403. |
| e | Session gating | Commands other than INIT / GET_VERSION / CLOSE are ignored until an INIT arrives. F:2339-2371. |
| f | Idle reaping | v1.4: only closes idle sessions of clients that set CAP_KEEPALIVE (timeout 5/10/15 s/off via OSD). **v1.1–v1.3 closed idle sessions for every client**, silently. Any datagram counts as activity. F:3116-3128. |

The current bridge (5-byte INIT, codec 0/1) works against the fork
unmodified; the SWITCHRES ACK carries `FrameEcho == 0` and is ignored by
the drainer's echo-progress guard. The one latent break is (f) on fork
v1.1–v1.3: a paused cast is reaped after ~5 s and never recovers.

### 1.2 NLC codec

Near-lossless per-scanline codec: RGB → YCoCg-R (reversible) → per-plane
MED prediction → NEAR quantisation (closed loop) → TILED or Golomb-Rice
packing. Wire format v2: each scanline is an 8-byte header of four
little-endian u16 padded segment lengths, followed by one 64-bit-aligned
segment per plane, bits packed LSB-first. Reference implementation:
fork `api/nlc_codec.{h,cpp}` (433 lines C).

Encoder parameters are **not** on the wire; the host must match the
fork client's `buildNlcParams` (`api/groovymister.cpp` L297-308) exactly:
tile = 16, width_bits = 4, rice_k = −1 (adaptive per tile), colour =
YCoCg, pixel format RGB888. Interlaced frames are encoded per field
(height = field height). NLC transport reuses the LZ4 blit shape: a
12-byte BLIT_FIELD_VSYNC header with the encoded size at [8..11], payload
in 1472-byte chunks. The fork's client never sends delta (13-byte) blits
under NLC. The fork README requires OSD "Volatile framebuffer" = Off.

### 1.3 License caveat

The fork's `LICENSE` is GPL-2.0 text; `nlc_codec.cpp` has no per-file
"or later" notice. This project is GPL-3.0. A Go port and the vendored C
reference in `tools/nlcvectors/` are derivative works. If the fork is
GPL-2.0-only, they cannot be combined into this GPL-3.0 project. **Confirm
"v2 or later" with the fork author before merging Part 2.** Part 1 contains
no fork-derived code (protocol facts only) and is unaffected.

## 2. Goals / non-goals

Goals:
- Detect which core is on the other end and report it.
- Keep fork sessions alive across pauses (keepalive).
- Offer NLC as a selectable codec on GroovyNLC, bit-exact with the FPGA
  decoder.
- Zero behaviour change for users on the original core or with default
  settings.

Non-goals:
- Inputs v2 / rumble (port 32101) — the bridge does not consume inputs.
- 31 kHz / 480p modes (where NLC's bandwidth savings matter most).
- GLOBAL pack mode, RGB-direct colour, RGBA/RGB565 under NLC.
- Making NLC the default (see §4.2).

## 3. Core detection and session lifecycle (Part 1)

### 3.1 Version probe

Before INIT, `Plane.Run` sends `GET_VERSION` (1 byte, `0x08`) and waits up
to 200 ms for a 1-byte reply.

- Reply `1`, or no reply → `CoreGroovy` (original).
- Reply `>= 2` → `CoreGroovyNLC`.
- Any other reply shape (length ≠ 1) is ignored while waiting; a stray
  13-byte ACK from a prior session does not end the wait.

The result is cached on the `groovynet.Sender` keyed by destination
address. It is invalidated when the MiSTer host changes and re-probed at
the start of any cast that follows a CLOSE (the user may have switched
cores from the MiSTer menu in between).

### 3.2 INIT

- `CoreGroovy`: the existing 5-byte INIT, unchanged.
- `CoreGroovyNLC`: 6-byte INIT; byte[5] = `CAP_KEEPALIVE` (`0x04`);
  byte[1] carries the effective codec (§4.3).

### 3.3 Keepalive

On `CoreGroovyNLC` only, while a session is open and no BLIT or AUDIO has
been sent for ≥ 2 s (paused, idle, buffering), the plane sends a 1-byte
`GET_STATUS` every 2 s. This covers the v1.4 CAP_KEEPALIVE timeout and
the v1.1–v1.3 unconditional reaper. Keepalive sends are not echo-tracked
and must not perturb the field clock or drainer.

### 3.4 SWITCHRES ACK

On `CoreGroovyNLC`, an ACK received after SWITCHRES is counted and logged
at debug level. It is not a gate: the plane does not wait for it or retry
SWITCHRES.

### 3.5 Surfacing

The detected core (`groovy` / `groovynlc` with version number) and the
effective codec are included in the plane start log line, the receiver
UI status, and the status SSE event.

## 4. Codec selection and config (Part 1)

### 4.1 Fields (`[bridge.video]`)

| Key | Values | Default | Scope |
|-----|--------|---------|-------|
| `codec` | `auto` \| `raw` \| `lz4` \| `nlc` | `auto` | ScopeRestartCast |
| `nlc_near` | `0`–`3` (0 = lossless) | `0` | ScopeRestartCast |
| `nlc_pack` | `tiled` \| `rice` | `tiled` | ScopeRestartCast |
| `delta_lz4_enabled` | bool (unchanged) | `true` | ScopeRestartCast |

`lz4_enabled` is removed from `BridgeConfig`, `core` types and
`dataplane.PlaneConfig`, replaced by `Codec`, `NLCNear`, `NLCPack`.
`delta_lz4_enabled` applies only when the effective codec is LZ4.

Validation rejects unknown `codec` / `nlc_pack` values and `nlc_near`
outside 0–3 before any disk write.

### 4.2 Resolution

A pure function `resolveCodec(configured, core) → (effective, warning)`
runs once per plane start, after the probe:

| configured | CoreGroovy | CoreGroovyNLC |
|------------|-----------|---------------|
| `auto` | lz4 | lz4 |
| `raw` | raw | raw |
| `lz4` | lz4 | lz4 |
| `nlc` | lz4 + warning | nlc |

`auto` deliberately resolves to LZ4 on both cores until the NLC encoder has
been verified on real GroovyNLC hardware. Flipping `auto` → `nlc` on
GroovyNLC later is a one-line change in this table. Before Part 2 lands,
`nlc` resolves to LZ4 with a "not yet supported" warning on both cores.

The warning is logged and shown in the receiver UI. The meter label
(`internal/chassis/meter.go`) and status report the **effective** codec.

### 4.3 INIT byte[1]

- raw → `0`, lz4 → `1`.
- nlc → `2 | near<<2 | 1<<4 | 2<<5 | pack<<7`, where `pack` is 1 for rice
  and 0 for tiled, built by `groovy.NLCCompressionByte(near, pack)`.

### 4.4 Migration

On load, a config that has `lz4_enabled` and no `codec`:
`lz4_enabled = false` → `codec = "raw"`; `true` → `codec = "auto"`. The
legacy key is dropped on the next save, following the existing
`internal/config/migration.go` pattern.

### 4.5 Settings UI

The LZ4 checkbox becomes a codec select (Auto / Raw / LZ4 / NLC). NLC
near and pack sit under it as advanced controls and are disabled unless
the codec is `auto` or `nlc`. The delta-LZ4 checkbox is disabled when the
codec is `raw` or `nlc`.

## 5. NLC encoder and data plane (Part 2)

### 5.1 Package `internal/groovy/nlc`

Pure Go, no dependencies, mirroring `nlc_codec.cpp` at fork `e60f52a`.

- `Params{Width, Height, Near int; Pack Pack}` with `Pack` ∈
  {`PackTiled`, `PackRice`}. Tile 16, width_bits 4, rice_k −1, YCoCg and
  RGB888 are unexported constants.
- `MaxEncodedSize(Params) int`.
- `Encoder` owns reusable scratch (plane rows, residuals, bit writer,
  per-line segment buffers). `(*Encoder).EncodeInto(dst, src []byte)
  (int, error)` performs no allocations in steady state.
- `Decode(dst, src []byte, Params) error` — used by fake-mister (§6.2)
  and tests.

### 5.2 `sendField`

`sendField` switches on the effective codec instead of `LZ4Enabled`. The
raw and LZ4 paths are unchanged. The NLC path:

1. Encode the field (field height for interlaced modes) into NLC scratch.
2. Send a 12-byte header whose size field is the encoded byte count,
   then the payload.
3. Never emit a raw 8-byte blit mid-session and never a 13-byte delta
   blit. NLC is always sent even when larger than raw. The fork's
   handling of size-0 or raw blits in NLC mode is not trustworthy.
4. If the encoder returns an error (impossible with validated params),
   skip the field, increment a counter and log at a rate limit. The core
   repeats its previous field.

Under NLC, `deltaLZ4Enabled` is forced false; field-history bookkeeping
is only kept if field diagnostics are enabled.

### 5.3 Stats

Per-field stats rename the `lz4` duration to a codec-neutral `encode`
duration (log key `max_encode_ms`). Compressed-bytes and ratio reporting
apply to NLC as well.

### 5.4 Performance budget

Target: NLC encode < 4 ms per 720×240 field on the dev machine, measured
by `BenchmarkEncode` for both pack modes at near 0. Start
single-threaded. If over budget, encode the three planes in parallel per
line batch and stitch the per-line records; the per-plane segment layout
makes this split clean.

## 6. Testing

### 6.1 Golden vectors (bit-exactness)

`tools/nlcvectors/` contains a C++ harness and a vendored copy of
`nlc_codec.{h,cpp}` from fork `e60f52a` with its license header and source
URL. `make nlc-vectors` builds it with a local clang/gcc and writes
`internal/groovy/nlc/testdata/*.golden` (params, input, encoded bytes).
It is not run in CI; the committed golden files are the contract.

Input matrix: flat, gradient, sharp edges, noise, one real 720×240 field,
and a width not divisible by 16; each × near {0,1,2,3} × pack {tiled,
rice}.

Go tests require byte-identical encoder output for every case, and a Go
decode round-trip: exact at near 0, and per the reference decoder's
output (also in the golden file) at near 1–3.

### 6.2 Fake-mister NLC mode

`cmd/fake-mister -core=nlc`:
- Replies `2` to GET_VERSION; `-core=groovy` (default) replies `1`.
- Accepts the 6-byte INIT and parses the byte[1] bitfield.
- ACKs SWITCHRES.
- `-idle-timeout=5s` reaps sessions idle longer than that (emulates fork
  v1.1–v1.3); ignores post-reap blits until the next INIT, like F:2339.
- Decodes NLC payloads via `nlc.Decode` so `-png-every` dumps keep
  working (Part 2).

In `-core=groovy` mode, fake-mister drops 6-byte INITs without an ACK,
matching the original core.

### 6.3 Unit tests

`resolveCodec` table; GET_VERSION reply parsing including timeout and
stray ACKs; 5- vs 6-byte INIT and `NLCCompressionByte`; config migration
and validation; keepalive fires only when idle and only on GroovyNLC.

### 6.4 Integration tests (`-tags=integration`)

- `-core=nlc`, codec `nlc`: decoded fields match the source within the
  NEAR bound (Part 2).
- `-core=nlc`, codec `auto`: session uses LZ4.
- `-core=groovy`, codec `nlc`: falls back to LZ4 with a warning.
- `-core=nlc -idle-timeout=1s`: pause 3 s, resume, frames still accepted.

### 6.5 Manual hardware check (not automatable)

On a MiSTer running GroovyNLC v1.4 with Volatile framebuffer off: 480i
cast with `codec = "nlc"`, near 0, both pack modes; visual check and
meter showing the NLC ratio. Also pause for > 10 s and resume. Pass here
is the precondition for flipping `auto` to NLC (§4.2).

## 7. Delivery

Two implementation plans against this spec:

1. **Part 1 — detection and plumbing:** §3, §4, §6.2 (minus NLC decode),
   §6.3, the non-NLC integration tests, README section on GroovyNLC
   (MiSTer.ini entry, Volatile framebuffer, codec settings). Shippable
   alone; `nlc` resolves to LZ4 with a warning.
2. **Part 2 — NLC encoder:** §5, §6.1, fake-mister NLC decode, the NLC
   integration test. Gated on the §1.3 license confirmation.
