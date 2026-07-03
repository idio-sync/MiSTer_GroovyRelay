# Dataplane pipeline audit — throughput resilience & correctness

Date: 2026-07-02
Scope: the real-time video path — FFmpeg → frame channel/prebuffer → `Plane.Run`
pump → field extract → LZ4/delta-LZ4 → congestion/pacing → `groovynet.Sender`
UDP → MiSTer, plus the ACK return path.
Trigger: native-Windows builds show heavy macroblocking / persistent smear
(delta on) and transient flashes (delta off) against a real MiSTer, while the
identical pipeline on Linux/Docker is clean. `sendField exceeded 84% of field
period` warnings with `congestion_ms` dominant.

All line references are to the tree at commit `17292c6c`.

Wire math used throughout:

- Raw NTSC field = 720×240×3 = 518,400 B → ceil(518400/1472) = **353 payload
  datagrams** ≈ 21,200 pps at 59.94 Hz (plus 1 header + ~3 audio datagrams/tick).
- Default pacing 20 µs × 352 inter-chunk gaps ≈ **7.04 ms** per field
  (`internal/groovynet/sender.go:137`).
- NTSC field period = 16.683 ms; the 84% budget threshold = 14.01 ms.
- Raw field (518,400 B) always exceeds `CongestionSize` (500,000,
  `internal/groovy/constants.go:77`); LZ4 output in 500,001–518,399 also does.

## 1. Executive summary

1. **Congestion back-off stacks with per-chunk pacing and makes catch-up
   impossible** (CONFIRMED). `MarkBlitSent` stamps the *end* of the previous
   send; `WaitForCongestion` enforces 11 ms from that stamp; pacing then adds
   ~7 ms. Steady state fits with ~5 ms headroom, but once one tick is late the
   pump drops to zero-delay catch-up where blit-to-blit floor = 11 + 7 ≈ 18 ms
   > 16.683 ms — a death spiral it can never exit. This matches the live
   Windows issue exactly.
2. **ENOBUFS detection is dead code on Windows** (CONFIRMED empirically).
   Go's `syscall.ENOBUFS` on Windows is a synthetic value (0x20000046) that
   never equals Winsock's WSAENOBUFS (10055). `enobuf_total` is永 0 on the one
   platform with the corruption problem.
3. **A mid-field send abort desyncs receiver framing and leaves delta history
   poisoned** (sender side CONFIRMED; receiver recovery needs hardware
   validation). The next header datagram is consumed as payload; the code does
   not invalidate delta history after an abort, so deltas smear until the
   ~1 s forced-full resync.
4. **A stale ACK in the shared socket's receive buffer can satisfy the next
   session's INIT handshake** (CONFIRMED logic).
5. Biggest resilience wins, in order: decouple congestion-wait from pacing
   (tiny change); clean field-skip when late (dup header instead of a late
   353-chunk field); event-driven full-LZ4 resync keyed off the existing
   frame-echo watchdog; deeper/configurable jitter buffer; make the existing
   telemetry visible (5 s stats line is Debug-only; UI omits ENOBUFS,
   duplicates, frames-ahead entirely).

## 2. Findings (most severe first)

| # | Title | Track | Severity | Confidence |
|---|-------|-------|----------|------------|
| F1 | Congestion wait stacks with pacing; catch-up impossible | A+B | Critical | CONFIRMED |
| F2 | Windows ENOBUFS detection dead code | B | High | CONFIRMED (empirical) |
| F3 | Mid-field abort → receiver framing desync + stale delta history | B | High | CONFIRMED sender-side; receiver NEEDS HW |
| F4 | Stale ACK satisfies next session's INIT | B | High | CONFIRMED logic |
| F5 | No clean-skip policy under deadline pressure | A | High | CONFIRMED gap |
| F6 | A/V index pairing drifts on video-only underruns | B | Med-High | CONFIRMED mechanism |
| F7 | Delta base vs FPGA parity across dups/loss | B | Med-High | HYPOTHESIS — NEEDS HW |
| F8 | Echo-stall watchdog observes but never acts | A | Medium | CONFIRMED gap |
| F9 | Jitter buffer fixed & shallow (~166 ms), env-only | A | Medium | CONFIRMED |
| F10 | INIT single-shot 60 ms, no retry | A | Medium | CONFIRMED |
| F11 | Telemetry invisible; ACK bits 1/2/3/4/7 ignored | C→A | High value | CONFIRMED |
| F12 | Double compression per delta-eligible field | C | Medium | CONFIRMED |
| F13 | Busy-wait pacing burns ~42% of a core | C | Medium | CONFIRMED |
| F14 | Audio ring silently drops newest chunk | B/C | Low-Med | CONFIRMED |
| F15 | Tick clock self-referential; drifts without ACKs | C | Low | CONFIRMED |
| F16 | Windows never sets DF bit | C | Low | CONFIRMED |
| F17 | Dead code / paper cuts | C | Low | CONFIRMED |

### F1 — Congestion wait stacks with pacing; catch-up is mathematically impossible

- `internal/groovynet/sender.go:268-273` — `MarkBlitSent` stamps the **end** of
  the previous field's send.
- `internal/groovynet/sender.go:279-292` — `WaitForCongestion` enforces 11 ms
  from that stamp whenever the last payload exceeded 500,000 B.
- `internal/dataplane/plane.go:1208` — the wait runs inside `sendField`,
  before the header send.
- `internal/dataplane/plane.go:1119-1125` — `nextTickDelay` floors at 0; there
  is no other recovery mechanism.

Steady state: the wait overlaps prior tick idle, adding only ~1–2 ms — total
sendField ≈ 11 ms, fits. But one OS hiccup > ~5 ms makes the tick late; from
then on every field pays the full 11 ms (measured from the *end* of the
previous send) plus ~7 ms pacing → 18 ms blit-to-blit → sustained ~55.5 Hz,
permanent zero-delay loop, ffmpeg backpressure, `congestion_ms` dominant in
the budget warnings. `GROOVY_PACING_US=0` shrinks the floor to ~12–14 ms —
marginal, which matches the observed "didn't help."

**Direction:** stamp blit time at send **start** (pass it from `sendField` or
stamp on first chunk in `SendPayload`), or subtract the paced-send duration
from the wait. Optionally skip the wait when pacing already spreads the
burst. Platform-neutral; does not regress Linux.

**Reference check (resolved 2026-07-02):** MiSTerCast `groovymister.cpp`
anchors at the **end** of the send (`m_tickCongestion = m_tickEnd` after all
chunks; spin-wait at the start of the next blit) — but it has **no per-chunk
pacing**, so its catch-up floor is ~11 + 4 ms and it always recovers. The
relay's pacing is the added ingredient that turns end-anchoring into an
18 ms floor. Fix applied: anchor at payload-send start
(`Sender.payloadSendStart`); with pacing off this differs from the reference
only by the short unpaced send duration, and with pacing on it removes the
stack while keeping peak burst rate below the reference's unpaced line-rate
bursts.

### F2 — Windows ENOBUFS detection is dead code

- `internal/groovynet/sender.go:182` — `errors.Is(err, syscall.ENOBUFS)`.

Verified empirically on this machine (Go 1.26): `syscall.ENOBUFS` = 536870982
(0x20000046, Go's synthetic POSIX value); `windows.WSAENOBUFS` = 10055;
`errors.Is(WSAENOBUFS, syscall.ENOBUFS)` = **false**. The torn-field WARN
never fires and `enobuf_total` stays 0 on Windows. The error still aborts the
field — only the telemetry is blind.

**Direction:** platform-gated matcher (`isSendBufferFull(err)`) matching
`windows.WSAENOBUFS` in a `sender_windows.go` shim, `syscall.ENOBUFS`
elsewhere. Note Windows may also block or silently drop in AFD/NDIS instead
of erroring — the counter is necessary but may not be sufficient evidence.

### F3 — Mid-field abort desyncs receiver framing and leaves delta history poisoned

- `internal/groovynet/sender.go:181-194` — `SendPayload` returns on first
  error; remaining chunks dropped.
- `internal/dataplane/plane.go:1220-1224` — `sendField` returns early; skips
  `MarkBlitSent`, `recordDeltaLZ4SendState`, and `rememberSentFieldHistory`
  (`plane.go:1295`).

The protocol has no per-chunk framing: the receiver still expects
`cSize − sent` bytes, so the next BLIT header datagram is consumed as payload
(framing desync; recovery mechanics unknown — see open question Q1). Not
remembering history keeps the sender's base = the pre-torn field, but the
receiver's buffer was partially overwritten → history mismatch → every
subsequent delta smears until the 30-field forced full
(`plane.go:300`, `deltaLZ4ForcedFullInterval`).

**Direction:** on any payload send error, `invalidateFieldHistory` for **both**
slots (forces full-LZ4 on the next field of each polarity) and increment a
`torn_fields` counter. Padding out the remaining byte count to preserve
framing is worth testing but needs hardware validation (padded LZ4 payload =
invalid block at the receiver; behavior unknown).

### F4 — Stale ACK can satisfy the next session's INIT handshake

- `internal/groovynet/sender.go:303-326` — `SendInitAwaitACK` reads the
  *oldest* datagram in the buffer; any 13-byte packet parses as a valid ACK.
- `internal/dataplane/plane.go:761` — drainer stops at session end; ACKs
  answering the final blits/CLOSE then queue unread in the 256 KB recv buffer.
- `internal/core/manager.go:840` — the sender (and socket) is reused across
  sessions for source-port stability.
- `internal/dataplane/plane.go:713-721` — `audioReady`/`fpgaFrame` seeded from
  whatever ACK was read.

On fast preemption (skip/next episode) the next session's INIT can "succeed"
instantly against a stale ACK, possibly before the FPGA processed INIT.

**Direction:** flush the socket (drain reads with an immediate deadline) right
before sending INIT. Optionally verify the ACK arrived after INIT was sent.

### F5 — No clean-skip policy when late

- `internal/dataplane/plane.go:963-1048` — every tick emits a full field when
  a frame is available, no matter how late.
- `internal/dataplane/plane.go:1497-1506` — `sendDuplicate` (9 bytes) exists
  but is only reachable via pipe underrun.

When behind schedule there is no path that trades one frame for a dup to
regain 16.7 ms. Combined with F1 this makes overload self-sustaining.

**Direction:** at tick entry, if `now − lastTick > 1.5×period` (or K
consecutive budget overruns), pull the frame, return it to the pool, send a
dup instead. Bound the skip rate (e.g. ≤1 per 10 ticks). One frozen field is
invisible; macroblocking is not.

### F6 — A/V index pairing drifts one field per video-only underrun

- `internal/dataplane/plane.go:1057-1075` — audio pull/ring runs every tick
  regardless of `fbOK`.
- `internal/dataplane/plane.go:794-798` — sync contract is index pairing
  (both streams arrive from ffmpeg in PTS order).

If audio flows while video stalls, alignment shifts 16.7 ms per underrun tick,
permanently; the late video burst becomes a standing backlog (added latency).
The common case (both pipes starve together — single ffmpeg muxer) is safe.

**Direction:** track skew (audio chunks consumed during video underruns); when
video backlog exists, consume `skew+1` frames and blit the newest. Expose
skew in stats. The stats already track `video_backlog_after_pull`
(`plane.go:163-171`) — telemetry exists, corrective action doesn't.

### F7 — Delta base vs FPGA parity across duplicates/loss (HYPOTHESIS)

- `internal/dataplane/plane.go:1353-1362` — delta base =
  `fieldPrev[field&1]`, keyed by the **header field bit**.
- `internal/dataplane/plane.go:1497-1506` — dups advance the wire frame
  counter without touching sender history (sender-side bookkeeping is
  correct).

If the FPGA's RTL selects its delta base by internal frame-counter parity
rather than the header field bit (per the investigation lead), an odd dup run
or a lost blit inverts the mapping → delta applied to the wrong DDR buffer →
persistent smear. Windows sessions produce far more dups/loss → the observed
platform asymmetry. NEEDS HARDWARE VALIDATION (RTL reading or hardware A/B:
delta on, forced underruns, watch smear onset after odd dup runs).

**Direction (regardless of RTL truth):** invalidate both history slots after
any underrun/dup run while delta is enabled — cheap insurance; underruns are
rare on healthy links.

### F8 — Echo-stall watchdog observes but never acts

- `internal/dataplane/plane.go:975-982` — echo-stall detection logs only.
- `internal/dataplane/plane.go:968` — `framesAhead` already computed.

A stalled `FrameEcho` is exactly the "receiver lost/torn a field" signal that
should trigger delta resync.

**Direction:** on echo stall (and on `FrameEcho` jumping backward):
invalidate delta history → immediate full-LZ4 resync; count `resyncs`. Turns
the ≤1 s worst-case smear into ~2 fields.

### F9 — Jitter buffer fixed and shallow, env-only

- `internal/dataplane/plane.go:288-301` — `videoChCap=8`, pool = 10.
- `internal/dataplane/plane.go:314-319` — prebuffer default 6.
- `internal/dataplane/plane.go:1691-1705` — `GROOVY_PREBUFFER_FIELDS` silently
  clamped to ≤ `videoChCap` (8).

Total absorption ≈ 166 ms; VBR transcode valleys longer than that guarantee
duplicate storms (and, via F6, A/V drift). Depth costs ~1 MB/frame
(720×480×3).

**Direction:** config field (`[bridge.video] jitter_fields`, restart-cast
scope) sizing pool/channel/prebuffer together; default 8 → 16–24 for
transcode sources; warn on clamp. Fixed-deeper gets ~90% of adaptive's value.

### F10 — INIT handshake single-shot, 60 ms, no retry

- `internal/dataplane/plane.go:713`.

One lost datagram on a busy host = user-visible session failure.
**Direction:** retry ×3 with socket flush per F4.

### F11 — Telemetry exists but is invisible; ACK bits ignored

- `internal/dataplane/plane.go:958-960` — the 5 s stats line is Debug-gated.
- `internal/core/manager.go:1660-1675` — UI view exposes only
  frames/underruns/wireBytes/ACK-age. No ENOBUFS, duplicates %, frames-ahead,
  delta-selected, torn-field count.
- `internal/groovy/ack.go:20-38` — `vgaFrameskip`/`vramSynced`/`vramQueue`
  (bits 3/2/7) parsed and dropped — free receiver-distress evidence.

**Direction:** promote stats to Info (or per-session ring served by the UI);
add enobuf (fixed per F2), duplicates %, frames-ahead current/max,
torn_fields, resyncs, delta-selected, send-time p95 to the meter SSE; log ACK
status-bit transitions.

### F12 — Double compression per delta-eligible field

- `internal/dataplane/plane.go:1188` (full LZ4) + `plane.go:1356` (delta LZ4)
  — both over 518 KB, every eligible field. The reference evaluates delta on
  alternate frames.

**Direction:** use the previous full-LZ4 size as the 95% comparator; compute
full only when delta loses. Halves compression cost on delta-friendly content.

### F13 — Busy-wait pacing burns ~42% of a core

- `internal/groovynet/sender.go:208-215` — pure spin, ~7 ms/field.

Competes with ffmpeg on contended hosts → more jitter → more F1.
**Direction:** batch pacing (8–16 chunks per quantum, spin only the tail), or
sleep the coarse part (Go ≥1.23 Windows timers ~0.5 ms), spin as fallback.

### F14 — Audio ring drops newest chunk, silently

- `internal/dataplane/plane.go:1058-1061` — while `!audioReady` the ring
  fills; each later tick pulls a chunk and discards it with no counter; on
  recovery the stalest ~83 ms plays first.

**Direction:** drop oldest (advance head) instead; count drops; expose.

### F15 — Tick clock self-referential

- `internal/dataplane/plane.go:964`, `plane.go:1083` — `lastTick = now` at
  fire time accumulates scheduler latency as rate error; raster correction
  compensates only while ACKs flow.

**Direction:** absolute schedule (`nextDeadline += period`) with raster
correction applied on top. Low impact; cheap robustness under ACK loss.

### F16 — Windows never sets DF

- `internal/groovynet/sender_windows.go:13-15` — no-op vs Linux
  `IP_MTU_DISCOVER=DO` (`sender_linux.go:17-29`).

Sub-1500 path MTU fragments on Windows vs drops on Linux; fragmented fields
have higher composite loss probability. LAN impact ~nil.
**Direction:** set `IP_DONTFRAGMENT` in the Windows control func.

### F17 — Paper cuts

- `internal/dataplane/clock.go:21` — `RunFieldTimer` used only by its own
  tests; the pump has its own timer. Delete or mark deprecated.
- `internal/dataplane/plane.go:1701` — prebuffer env values >8 clamp silently;
  warn.

### Reviewed and found sound (Track B non-findings)

- Buffer aliasing: `lz4Scratch` / `headerScratch` / `fieldScratch` /
  `fieldDeltaScratch` / `fieldPrev` are all tick-goroutine-owned with safe
  write→send→copy ordering; `headerScratch` sharing between
  `sendField`/`sendDuplicate` is safe as documented (`plane.go:382-385`).
- `emitField` flip keeps header tag, payload slice, and history slot keyed
  consistently (`plane.go:994`, `plane.go:1013`); a mid-session TFF↔BFF flip
  cannot mis-slot delta history.
- Byte-wrap delta subtraction (`plane.go:1450-1454`) matches the documented
  `mod 256` contract.
- Delta bookkeeping across dups is correct *sender-side* (history untouched;
  forced-full counter not advanced) — the residual risk is FPGA-side (F7).
- Prebuffer index-pairing of A/V is sound at startup; the drift risk is only
  the steady-state underrun case (F6).

## 3. Throughput-resilience design

1. **Decouple congestion-wait from pacing** (F1; small). Stamp blit time at
   send start. Catch-up floor drops 18 ms → ~11 ms < 16.683 ms → always
   recoverable. Cross-check reference anchor first. Optionally skip the wait
   when pacing is active.
2. **Clean field-skip under deadline pressure** (F5; small). Late by >1.5
   periods → consume frame, send dup. Converts overload from "smear + runaway
   latency" to "occasional 1-field freeze."
3. **Closed-loop ACK-driven recovery** (F7/F8; small-medium). Echo stall or
   backward echo → invalidate delta history (2-field resync). Dup run while
   delta enabled → same. Sustained frames-ahead growth → escalate: log, raise
   pacing, clean-skip. All sender-side, protocol-safe.
4. **Configurable jitter buffer** (F9; small). `[bridge.video] jitter_fields`,
   restart-cast scope; default 16–24 for transcode; pair with F6 skew-burn so
   post-stall backlogs drain.
5. **Jumbo datagrams** (medium; NEEDS HW). Config-gated `max_datagram`: at
   8,972 B payloads a raw field is 58 datagrams (6.1× fewer, ~3.5k pps).
   Gate on receiver tolerance, startup DF probe, explicit opt-in. Keep 1472
   default.
6. **INIT robustness** (F4/F10; trivial). Flush socket before INIT; retry ×3.
7. **Observability package** (F2/F11; small-medium; highest diagnostic ROI).
   Errno fix + UI meter fields + ACK bit transition logs. Would have made the
   current issue a one-grep diagnosis.
8. **Config ergonomics** (small). `GROOVY_PACING_US`,
   `GROOVY_PREBUFFER_FIELDS`, `GROOVY_AUDIO_DELAY_FIELDS` → `[bridge.tuning]`
   with ApplyScope mapping (pacing = hot-swap via `SetPacingInterval`;
   prebuffer/audio-delay = restart-cast). Env vars stay as overrides.

Invariants respected by all proposals: one HTTP listener; source-port
stability (socket never rebound — F4 flushes, doesn't reopen); `Manager.mu`
untouched; ApplyScope tiers for new config; field-order flip untouched; core
imports no adapter; Linux behavior preserved except the F1 timestamp anchor
(platform-neutral, semantics verified against reference first).

## 4. Test coverage gaps (pump loop)

`sendField` compression/delta/history paths are well covered
(`internal/dataplane/plane_test.go`). Missing, all testable without hardware
via the existing `processHandle`/`fieldSender` seams:

- Congestion+pacing interaction under a late tick (fake clock + scripted
  `fieldSender` recording inter-send gaps) — would have caught F1.
- Mid-field abort state (F3): history invalidation, torn counter.
- Stale-ACK INIT (F4): pre-loaded socket buffer.
- A/V index skew across underruns (F6).

## 5. Open questions / needs hardware or reference validation

1. **Receiver framing recovery after a short field** — does groovy.cpp
   force-complete on the next command datagram, or realign via
   unknown-command drops? Determines whether padding out aborted fields
   helps (F3).
2. **FPGA delta base selection** — header field bit vs internal frame-counter
   parity; whether dup BLITs advance that counter (F7). Linchpin of the
   persistent-smear theory.
3. **Reference congestion anchor** — RESOLVED (see F1): reference anchors at
   end-of-send but is unpaced; the relay now anchors at payload-send start.
4. **Windows send-path drop locus** — blocks, WSAENOBUFS, or silent drop in
   AFD/NDIS under burst? Answerable after F2 plus a controlled capture.
5. **Receiver max datagram size** — required for jumbo datagrams.
6. **VSync field semantics** — relay always sends `VSync=0`
   (`plane.go:1183`); `VCountEcho` feeds `rasterCorrection`
   (`plane.go:1101`) — with 0 the echo term is degenerate; check whether the
   correction math still matches upstream intent.

## 6. Recommended sequencing

**Status (2026-07-02): waves 1 and 2 are landed on main.**
Wave 1: F1 `b100c0c2`, F2 `7e567778`, F3 `4fa9079c`, F4+F10 `488a7043`,
F14 `dfdb5275`, F17 `7fc2234c`. Wave 2: dataplane telemetry `36180824`
(LinkHealth accessor, frames-ahead tracking, ACK distress-edge warns,
eventful-stats Info promotion), meter plumbing `fcb9a9b7` (counters in the
status view and SSE meter envelope: tornPayloadSendsTotal, enobufTotal,
audioRingDropsTotal, framesAhead). Front-end rendering of the new envelope
fields was intentionally left out (matches the existing pattern —
blitsTotal/underrunsTotal ride the envelope unrendered). Next: validate
F1/F3 on the affected Windows + real-MiSTer setup using the new counters,
then plan wave 3.

Hybrid: two inline fix waves now (no formal plan needed — each change is
small, independently testable, and invariant-safe), then a written
implementation plan for the behavior-changing work, gated on the telemetry
from wave 2.

**Wave 1 — surgical fixes (inline, one commit each, unit-tested):**
F1 timestamp anchor, F2 Windows errno matcher, F3 history invalidation +
torn counter, F4 socket flush + F10 INIT retry, F14 ring drop-oldest +
counter, F17 paper cuts. Rationale: F1 alone plausibly resolves the Windows
death spiral; F2–F4 are prerequisites for trusting any future measurement.

**Wave 2 — observability (inline, small):** F11 UI meter fields + stats
promotion + ACK bit logging; F8's resync counter plumbing. Rationale: wave 3
changes pump behavior — we need before/after evidence from the affected
Windows + real-MiSTer setup, not just local tests.

**Wave 3 — behavioral changes (write a plan first, hardware-validated):**
F5 clean skip, F8 event-driven resync actions, F7 dup-run insurance,
F6 skew-burn, F9 jitter-buffer config, config ergonomics (§3.8), F12/F13
CPU reductions. These interact with each other and with FPGA behavior;
sequence and rollback strategy belong in a dated plan doc.

**Wave 4 — research spikes (separate, gated on open questions):** jumbo
datagrams (Q5), torn-field padding (Q1), delta parity RTL reading (Q2),
reference congestion-anchor check (Q3 — do this one *before* landing the F1
fix if possible; it's a repo read, not hardware).
