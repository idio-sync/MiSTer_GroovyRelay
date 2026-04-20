# MiSTer_GroovyRelay v1 Remediation — Design Spec

**Date:** 2026-04-20
**Status:** Pre-implementation.
**License:** GPL-3
**Supersedes:** nothing — this is a remediation batch layered on top of
`docs/specs/2026-04-19-mister-groovy-relay-design.md` (the v1 design) and
`docs/plans/2026-04-19-mister-groovy-relay-v1.md` (the v1 plan).

## 1. Problem Statement

The 2026-04-20 code review of v1 (commit `82dc8b6`, review conducted against
BASE `0024bb1`..HEAD `82dc8b6`) identified 3 Critical and 8 Important issues.
The v1 implementation otherwise matches the design spec: architecture
boundaries are clean, protocol byte layouts are correct against
`docs/references/groovy_mister.md`, scope has not drifted, and no v2 features
snuck in. The issues are concentrated in the FFmpeg↔protocol glue (filter
chain, timing math), the LZ4 incompressible-input path, control-plane
robustness (HTTP timeouts, mutex-held-during-network-I/O), and one test-infra
bug that makes a scenario assertion unreachable. The plan self-review also
flagged a `host_ip` config key as an "accepted weakness" which was then
silently dropped in implementation.

This spec closes all 11 items so the bridge is ready for Task 14.2 (real
MiSTer + real CRT manual end-to-end) without carrying known-wrong timing
math, protocol edge cases, or silently-dropped plan commitments.

## 2. Scope

### In scope (this batch)

All 11 Critical and Important findings from the 2026-04-20 review, in
dependency order:

1. **C2** — `fakemister.Listener` records AUDIO payload bytes so scenario
   tests' audio assertions become meaningful.
2. **C1** — FFmpeg filter chain produces 59.94 fields/sec for every source
   rate Plex actually serves (23.976p, 29.97p, 30p, 59.94p, 60p).
3. **C3** — LZ4 incompressible-input fallback emits a RAW BLIT header
   instead of a zero-length LZ4 frame.
4. **I4** — `Plane.Position()` derives from a fractional nanosecond
   accumulator so Plex timeline doesn't skew.
5. **I5** — Audio chunk reader uses an integer-exact fraction so it
   consumes FFmpeg at the real 60000/1001 Hz rate, not a rounded 59.94.
6. **I8** — `ffmpeg.Probe` and `ffmpeg.ProbeCrop` run without holding
   `Manager.mu` and with a bounded 10 s context.
7. **I6** — Plex subtitle track is fetched to a temp file before libass
   reads it.
8. **I7** — `rgb_mode` config validates to `rgb888` only until other modes
   are wired through.
9. **I9** — plex.tv HTTP client has a 10 s timeout; `RegisterDevice`
   checks response status.
10. **I10** — `SubtitleURLFor` HTTP call has a 10 s timeout.
11. **I11** — New optional `host_ip` config key plus README section on
    multi-NIC Unraid and Docker cgroup throttling.

### Out of scope

- The 6 Minor items from the 2026-04-20 review (subtitle filter shell
  escaping, `setStreams` 200-OK no-op, hardcoded 720×480 transcode
  dimensions, profile-string fixture tightening, SWITCHRES porch pcap
  verification, Dockerfile EXPOSE-comment typo). Deferred to a separate
  polish pass.
- The Windows integration-test skip (7 of 8 tests skip on Windows).
  Verification for this batch stays CI-only on Linux; fixing the Windows
  skip is an unrelated tangent.
- Anything the v1 spec says is v2 (Jellyfin, URL-input adapter,
  mid-playback `setStreams`, PAL 25 fps, queue, multi-session, per-content
  aspect override).
- Task 14.2 itself (real MiSTer + real CRT). Gated on this batch landing
  clean, then scheduled separately. The plan self-review caveats about
  NTSC porches being pcap-unverified and field-order TFF/BFF empirical
  validation stay on 14.2's plate, not this one.

## 3. Plan shape

- **Flat** — one commit per fix, 11 commits total. No phase checkpoints.
- **Atomic** — each commit is self-contained: code change + its own test
  + clean `go vet` + clean `go test ./...` + clean
  `go test -tags=integration ./tests/integration/...`.
- **Verification discipline mixed by fix type:**
  - *TDD (failing test first, then fix):* C1, C3, I4, I5. These are
    correctness-math items where "it compiled and looked fine" was the
    failure mode in the original implementation.
  - *Regression test after:* C2, I6, I7, I8, I9, I10, I11. These are
    either one-line hardening changes or refactors where the failure
    mode is "compiles or doesn't" and a regression test locks in the
    intended behavior cheaply.

## 4. Per-item fix design

### Bucket A — Test infrastructure (fix first)

#### C2 · Listener records AUDIO payload bytes

**Bug.** In `internal/fakemister/listener.go` (around lines 90–107), the
AUDIO case parses the 3-byte header but never sets `cmd.AudioPayload`.
`Recorder.audioBytes` only increments when `AudioPayload != nil`, so
`AudioBytes` stays 0 in every test path that uses `Listener.Run`. The
scenario test's `snap.AudioBytes < 200_000` check is therefore
unreachable and will always fail once the Windows skip is lifted.

**Fix.** In the AUDIO-cmd branch, after reading the 3-byte header, slice
the remaining bytes from the datagram and set
`cmd.AudioPayload = append([]byte(nil), payload...)` before dispatching
to the recorder.

**Verification.** *Regression test.* New unit test in
`internal/fakemister/listener_test.go` that sends a synthetic AUDIO
datagram to a running `Listener.Run` via loopback UDP, drains one
Command from the channel, and asserts `cmd.AudioPayload` equals the
expected bytes. A second assertion ensures `Recorder.Snapshot().AudioBytes`
is non-zero after the datagram.

### Bucket B — Correctness math (TDD)

#### C1 · Filter chain produces 59.94 fields/sec for every source rate

**Bug.** `buildFilterChain` in `internal/ffmpeg/pipeline.go` uses
`fps=30000/1001 → interlace → separatefields`. FFmpeg's `interlace`
filter halves the input frame rate, so 29.97p input produces 14.985i,
then `separatefields` produces 29.97 fields/sec — half the target. Only
the 23.976p branch (`telecine=pattern=23`) reaches 59.94 correctly.
The field timer in the data plane still runs at 59.94 Hz; the FFmpeg
pipe backpressures at the wrong rate and the Plane emits duplicate
BLITs for roughly half of all fields.

**Fix.** Replace the rate-specific dispatch with a unified chain that
always outputs 59.94 progressive frames before the interlace step:

```
decode
  → [yadif=deint=interlaced if input is interlaced]
  → fps=60000/1001
  → crop
  → scale=720:480
  → [subtitles=filename='<local path>':si=<idx> if subtitle present]
  → interlace=scan=<tff|bff>:lowpass=0
  → separatefields
  → format=rgb24
```

`fps=60000/1001` normalizes any source rate (23.976/29.97/30/59.94/60)
to 59.94 progressive via frame duplication or drop. `interlace` then
halves the rate back to 29.97i, and `separatefields` doubles it to
59.94 fields/sec. Net: 59.94 fields/sec regardless of source.

For 23.976p sources (detected from the probe-reported frame rate,
within a small tolerance around 24000/1001), substitute
`telecine=pattern=23` for `fps=60000/1001` — same output rate (29.97i
before `separatefields`), but film-accurate 3:2 cadence instead of the
`fps` filter's generic frame duplication.

**Verification.** *TDD.*
- Per-rate unit tests in `internal/ffmpeg/pipeline_test.go` that
  construct the filter chain for each of {23.976p, 29.97p, 30p,
  59.94p, 60p} and assert the string contains the expected filters in
  the expected order.
- New integration test in `tests/integration/plane_test.go` (or a
  dedicated `filter_rate_test.go`) that runs the real FFmpeg pipeline
  against synthetic 5-second clips at 30p, 60p, and 23.976p,
  counts fields emitted by the sender for each, and asserts the count
  is 255–345 (the existing 59.94 × 5 ± tolerance band).

#### C3 · LZ4 incompressible-input fallback emits RAW BLIT

**Bug.** `LZ4Compress` in `internal/groovy/lz4.go` calls
`pierrec/lz4/v4`'s block-API `CompressBlock`, which returns `n=0` (not
an error) when the input is incompressible. The current code silently
returns `dst[:0]`. `Plane.sendField` then emits a 12-byte LZ4 BLIT
header with `CompressedSize=0` and zero payload bytes, which the
receiver cannot decode. A single random-noise frame on a compressed
source can desync a session.

**Fix.** Change signature to
`func LZ4Compress(src []byte) (compressed []byte, ok bool)`.
`ok == false` when `CompressBlock` returns `n == 0` or `n >= len(src)`
(incompressible or not worth it). In `internal/dataplane/plane.go`
`sendField`, branch on `ok`:
- `ok == true`: emit the LZ4 BLIT header variant and the compressed
  payload.
- `ok == false`: emit the RAW BLIT header variant (already defined in
  `internal/groovy/builder.go`) and send the raw RGB bytes uncompressed.

The spec §6 BLIT_FIELD_VSYNC definition already supports both RAW and
LZ4 variants; this just wires the switch.

**Verification.** *TDD.*
- Unit test in `internal/groovy/lz4_test.go` that feeds a
  `crypto/rand`-filled 518,400-byte buffer (one 720×240 RGB888 field's
  worth) and asserts `ok == false` and `len(compressed) == 0`.
- Unit test in `internal/dataplane/plane_test.go` (or `lz4` wiring
  test) verifying that when `LZ4Compress` returns `ok=false`, the
  emitted BLIT header matches the RAW variant byte layout (8-byte
  full-RAW or 9-byte delta-RAW) — not the 12/13-byte LZ4 variants.

#### I4 · Position fractional nanosecond accumulator

**Bug.** `internal/dataplane/plane.go` increments
`positionMs.Add(int64(16))` per field tick. One field at 60000/1001 Hz
is 16.6835 ms, so each tick reports ~0.6835 ms slow. Over 60 minutes
that accumulates to ~2.4 minutes of under-reporting to the Plex
timeline broadcaster.

**Fix.** Replace `positionMs` with `positionFields`. Each tick
increments the field counter by 1. `Position()` computes from the
integer-exact field period of NTSC 60000/1001 Hz:

```go
func (p *Plane) Position() int64 {
    fields := p.positionFields.Load()
    return fields*1001/60 + p.baseOffsetMs
}
```

`fields * 1001 / 60` is milliseconds — integer-exact, no floating
point, no rounding error. `baseOffsetMs` is the resume offset from
`PlaneConfig` (unchanged).

**Verification.** *TDD.* Unit test in
`internal/dataplane/plane_test.go`:
- After 3600 ticks: `Position() - baseOffsetMs == 60_060` exactly
  (3600 fields × 1001/60 = 60,060 ms; 3600 fields at 59.94 Hz is
  60.06 seconds).
- After 60,000 ticks: `Position() - baseOffsetMs == 1_001_000`
  exactly (60,000 × 1001/60 = 1,001,000 ms = ~16.68 minutes, matching
  60,000 fields / 59.94 Hz).
- With `baseOffsetMs = 5_000`, after 600 ticks: `Position() == 15_010`
  (5,000 + 600×1001/60 = 5,000 + 10,010).

#### I5 · Audio chunk size from integer-exact fraction

**Bug.** `internal/dataplane/audiopipe.go`'s `AudioChunkSize(48000, 2)`
returns `int(192000/59.94) == 3203` bytes/field. The real per-field
rate is `192000 × 1001 / 60000 = 3203.2` bytes. Reader under-consumes
FFmpeg by ~53 B/sec; the audio pipe periodically backpressures,
causing stutter or A/V drift — reintroducing the drift the
single-FFmpeg-process design was chosen to prevent.

**Fix.** `AudioPipeReader` tracks `fieldsRead int64` and
`bytesRead int64`. Per call to the reader's per-field read method,
compute:

```go
expected := (fieldsRead + 1) * sampleRate * channels * bytesPerSample * 1001 / 60000
chunkBytes := expected - bytesRead
```

then read exactly `chunkBytes` from the audio pipe, increment
`bytesRead` by the amount actually read, increment `fieldsRead`. Exact,
no floating point, no drift.

**Verification.** *TDD.* Unit test in
`internal/dataplane/audiopipe_test.go` that drives the reader through
3596 fields (one full second at 59.94 Hz is ~59.94 fields; 60 s is
~3596) against a mock pipe producing monotonic bytes, and asserts
cumulative bytesRead equals `3596 * 192000 * 1001 / 60000` exactly.
Second test drives 60,000 fields and verifies cumulative equals the
integer formula.

### Bucket C — Control-plane robustness (regression after)

#### I8 · Probe outside Manager.mu with bounded context

**Bug.** `Manager.startPlaneLocked` in `internal/core/manager.go` calls
`ffmpeg.Probe(context.Background(), ...)` and `ffmpeg.ProbeCrop(...)`
while holding `m.mu`. A stuck PMS call deadlocks every other control
operation (Pause, Stop, SeekTo).

**Fix.** Restructure `StartSession`: Probe/ProbeCrop run first,
outside `m.mu`, with `ctx, cancel := context.WithTimeout(parentCtx,
10*time.Second)`. Mutex is held only for state mutation and plane
spawn:

```go
func (m *Manager) StartSession(parent context.Context, req SessionRequest) error {
    probeCtx, cancel := context.WithTimeout(parent, 10*time.Second)
    defer cancel()
    info, err := ffmpeg.Probe(probeCtx, req.URL)
    if err != nil { return err }
    crop, err := ffmpeg.ProbeCrop(probeCtx, req.URL)
    if err != nil { return err }

    m.mu.Lock()
    defer m.mu.Unlock()
    // existing state transition + spawn using info + crop
}
```

**Verification.** *Regression test* in
`internal/core/manager_test.go` with a stubbed `Probe` that sleeps on a
channel; a concurrent `Stop()` call returns within 100 ms instead of
blocking until probe completes. A second test asserts probe timeout at
10 s returns a context-deadline-exceeded error to the caller.

#### I9 · plex.tv HTTP timeout + status check

**Bug.** `RequestPIN`, `PollPIN`, `RegisterDevice` in
`internal/adapters/plex/linking.go` use `http.DefaultClient.Do` with
no timeout. `RegisterDevice` is called from a 60 s ticker loop; a
stuck plex.tv call blocks all subsequent ticks. `RegisterDevice` also
ignores `resp.StatusCode`, so a 401 (expired token) is silent.

**Fix.** Package-level `var plexTvClient = &http.Client{Timeout:
10 * time.Second}`. Replace the three `http.DefaultClient.Do` call
sites. `RegisterDevice` adds:
```go
if resp.StatusCode >= 400 {
    return fmt.Errorf("plex.tv register: %s", resp.Status)
}
```

**Verification.** *Regression tests* in
`internal/adapters/plex/linking_test.go`:
- `httptest.Server` returning 401 → `RegisterDevice` returns an error
  containing "401".
- `httptest.Server` that hangs (`<-blockCh`) → caller returns within
  ~10 s with a net-timeout error.

#### I10 · SubtitleURLFor HTTP timeout

**Bug.** `SubtitleURLFor` in `internal/adapters/plex/transcode.go`
uses `http.Get` with no timeout.

**Fix.** Reuse the `plexTvClient` from I9 (or a sibling client with
the same 10 s timeout). Call becomes
`plexTvClient.Do(req)` with an explicit `http.NewRequestWithContext`.

**Verification.** *Regression test* in
`internal/adapters/plex/transcode_test.go`: httptest hang server;
assert caller returns within ~10 s.

### Bucket D — FFmpeg pipeline hardening (regression after)

#### I6 · Subtitle track fetched to temp file

**Bug.** `SubtitleURLFor` returns an HTTP URL with auth token
appended. `pipeline.go` currently plugs that URL into the
`subtitles=filename='<URL>':si=<idx>` filter. libass (which backs the
`subtitles` filter) expects a local filesystem path, not a URL. Some
FFmpeg builds error; others silently drop the filter.

**Fix.** Before spawning the FFmpeg process, in
`internal/core/manager.go` (or a helper in `internal/dataplane/`),
fetch the subtitle URL to `<cfg.DataDir>/subtitles/<sessionID>.<ext>`
using the same timed HTTP client from I9/I10. Extension is inferred
from the response `Content-Type` (`text/x-ssa` → `.ass`,
`application/x-subrip` → `.srt`, else `.srt` as fallback).
`PipelineSpec` gets a new field `SubtitlePath string` that replaces
the URL; `buildFilterChain` emits
`subtitles=filename='<path>':si=<idx>` using the local path. On
`Plane.Stop()` (or at session teardown), the temp file is removed.

The temp directory is created with `os.MkdirAll(..., 0700)` and the
file is written with `os.WriteFile(..., 0600)` to match the
token-store security convention.

**Verification.** *Regression test* with an httptest server serving a
minimal SRT body. Assert: (1) a temp file is created under
`DataDir/subtitles/`, (2) the filter string contains the path not the
URL, (3) the file is removed after `Stop()`.

#### I7 · rgb_mode validates to rgb888 only

**Bug.** Config accepts `rgb_mode ∈ {rgb888, rgba8888, rgb565}`;
`manager.go` maps to the right INIT byte and bytes-per-pixel, but
`pipeline.go` hardcodes `-pix_fmt rgb24`. Selecting `rgba8888` or
`rgb565` produces a torn stream.

**Fix.** Narrow config validation in `internal/config/config.go` to:
```go
if cfg.RGBMode != "rgb888" {
    return fmt.Errorf("rgb_mode: only rgb888 is supported in v1 (rgba8888/rgb565 reserved for future work)")
}
```
Update `config.example.toml` comment to reflect this.

**Verification.** *Regression test* in
`internal/config/config_test.go`: `rgb_mode = "rgba8888"` returns the
validation error with the expected message.

### Bucket E — Config and docs (regression after)

#### I11 · host_ip config key + README operational notes

**Bug.** The v1 plan self-review explicitly said "on Unraid the
recommendation is to make `host_ip` a required config key and bypass
auto-detection." Not implemented. `outboundIP()` in
`cmd/mister-groovy-relay/main.go` routes to 8.8.8.8 and picks the
default-route interface — on multi-NIC Unraid this is frequently the
wrong interface. README also does not mention the Docker cgroup
throttling risk the plan flagged.

**Fix.**
- New optional string field `HostIP` in `internal/config/config.go`.
- `cmd/mister-groovy-relay/main.go` replaces `outboundIP()` with:
  ```go
  hostIP := cfg.HostIP
  if hostIP == "" {
      hostIP = outboundIP()
      slog.Warn("auto-detected host IP; set host_ip in config for multi-NIC hosts", "ip", hostIP)
  }
  ```
- `config.example.toml` gets a commented-out `host_ip = "..."` line
  with a one-sentence explanation pointing at the README.
- README gets two new subsections:
  - **"Multi-NIC Unraid hosts"** — explains when to set `host_ip`
    (host has multiple interfaces and the Plex-facing one is not the
    default route), how to find the right IP, and what symptom an
    incorrect IP produces (Plex controller can't reach the bridge
    for timeline pushes, cast target appears but commands never
    arrive).
  - **"CPU contention under Docker"** — notes that Unraid parity
    checks and co-tenant containers can cause frame drops, that the
    clock-push design makes drops visible as glitches rather than
    drift, and points at `--cpus` or cgroup limits as a
    containment option.

**Verification.** *Regression test* in
`internal/config/config_test.go`: (1) loading with `host_ip =
"192.168.1.10"` round-trips the value; (2) loading without
`host_ip` leaves `HostIP == ""` (no default). Manual sanity-check
that README renders cleanly.

## 5. Commit order and verification per commit

11 commits in this order. Messages use the existing repo's
conventional-commit style (verified against
`git log --oneline`):

1. `test(integration): scenario harness records AUDIO payload bytes` — C2
2. `feat(ffmpeg): filter chain normalizes any source rate to 59.94 fields/sec` — C1
3. `fix(groovy): LZ4 returns ok=false on incompressible input; Plane emits RAW BLIT` — C3
4. `fix(dataplane): Position uses integer-exact field-count accumulator` — I4
5. `fix(dataplane): audio chunk size from integer-exact fraction` — I5
6. `refactor(core): Probe/ProbeCrop run outside Manager.mu with bounded context` — I8
7. `fix(ffmpeg): fetch subtitle track to temp file before libass reads it` — I6
8. `fix(config): rgb_mode validates to rgb888 only (v1 scope)` — I7
9. `fix(plex): plex.tv HTTP client has 10s timeout; RegisterDevice checks status` — I9
10. `fix(plex): SubtitleURLFor HTTP call has 10s timeout` — I10
11. `feat(config): host_ip config key + README multi-NIC + cgroup docs` — I11

**Each commit must pass:**
- `go vet ./...` clean.
- `go test ./...` clean (unit suite).
- `go test -tags=integration ./tests/integration/...` clean on Linux.
  Windows continues to skip 7 of 8 scenarios — accepted.

**Each commit must include its own test** — either a failing test
added first for the TDD items (C1, C3, I4, I5) and a separate or
combined fix commit that turns it green, or an after-the-fact
regression test in the same commit for the other 7 items.

**Final verification after all 11 commits:**
- `go test -race ./...` — catches any data-race regressions from the
  I4/I5/I8 refactors (the accumulators, the mutex reshape).
- `go test -tags=integration ./tests/integration/...` — full scenario
  suite, including the now-meaningful audio-bytes assertion.
- Spot-check that the 11 commits cleanly map to the 11 review items
  (no drift, no bonus work sneaking in).
- Manual read-through of `config.example.toml` and README for I11.

## 6. Non-goals (explicit)

- No universal `SourceAdapter` interface (v1 rule §4.5 unchanged).
- No new adapters.
- No Windows integration-test revival.
- No SWITCHRES porch re-verification (stays on Task 14.2).
- No protocol-layer changes beyond the LZ4 fallback and BLIT RAW-variant
  wiring already present in the v1 builder.
- No refactor of `Plane` or `Manager` beyond what I8 requires (Probe
  hoist) and what I4/I5 require (accumulator storage types).
- No new dependencies.

## 7. References

- v1 design: `docs/specs/2026-04-19-mister-groovy-relay-design.md`.
- v1 implementation plan and self-review:
  `docs/plans/2026-04-19-mister-groovy-relay-v1.md`.
- Code review findings (conducted 2026-04-20): in session history;
  summarized in §1 above.
- Protocol references unchanged: `docs/references/groovy_mister.md`,
  `docs/references/mistercast.md`.
