# MiSTer_GroovyRelay v1 Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close all 11 Critical + Important findings from the 2026-04-20 code review of v1 so the bridge is ready for Task 14.2 hardware validation without carrying known-wrong timing math, protocol edge cases, or silently-dropped plan commitments.

**Architecture:** No architectural changes. This is a remediation batch that fixes timing math bugs, protocol edge cases, control-plane robustness gaps, one test-infra wiring bug, and a config + docs drop flagged in the v1 plan self-review. Module boundaries, core/adapter split, and data/control plane separation are all unchanged.

**Tech Stack:** Go (go.mod = 1.26.2), FFmpeg external process, `github.com/BurntSushi/toml`, `github.com/pierrec/lz4/v4`, Go stdlib. **No new dependencies.**

**Source of truth:** `docs/specs/2026-04-20-v1-remediation-design.md`. Read §3 before starting to understand the mixed TDD/regression-test discipline per item.

---

## Plan overview

11 tasks, one commit each, in dependency order. C2 first so later scenario tests observe audio correctly; then the TDD correctness-math items (C1, C3, I4, I5); then Manager/HTTP hardening (I8, I6, I7, I9, I10); ending with the config + docs drop (I11).

No phase checkpoints — flat list. Each task is self-contained: code change + its own test + clean `go vet` + clean `go test ./...` + clean `go test -tags=integration ./tests/integration/...`. Windows continues to skip 7 of 8 integration scenarios; that's accepted for this batch.

After the final commit, run a whole-repo verification pass (Task 12).

**Per-item verification discipline (from spec §3):**
- *TDD (failing test first, then fix):* C1, C3, I4, I5.
- *Regression test in the same commit:* C2, I6, I7, I8, I9, I10, I11.

---

## Task 1 (C2): Listener records AUDIO payload bytes

**Context:** `Listener.Run` in `internal/fakemister/listener.go` parses each datagram independently. AUDIO commands consist of a 3-byte header datagram plus separate PCM payload datagrams; `Run` treats the PCM datagrams as unknown commands and discards them. `Recorder.audioBytes` only increments when `Command.AudioPayload != nil`, so `TestScenario_Cast`'s `snap.AudioBytes < 200_000` assertion is unreachable — the assertion will fail on the first Linux CI run.

**Fix approach:** The scenario harness (`tests/integration/scenarios_test.go::newScenarioHarness`) uses `Listener.Run` but should use `Listener.RunWithFields`, which *does* reassemble AUDIO payloads and emits them on a separate `audios` channel. We bridge that channel into synthetic `Command{Type: CmdAudio, AudioPayload: ...}` records and feed them into the existing events channel — Recorder then counts them. The Listener itself is unchanged.

A unit-level regression test lives in `internal/fakemister/listener_test.go` so the fix has cross-platform coverage (the scenario test itself stays Linux-only).

**Files:**
- Modify: `tests/integration/scenarios_test.go` (the `newScenarioHarness` helper).
- Modify: `internal/fakemister/listener_test.go` (new test case).

- [ ] **Step 1: Add a regression unit test** for AUDIO reassembly via `RunWithFields`. Append to `internal/fakemister/listener_test.go`:

```go
func TestRunWithFields_ReassemblesAudioPayload(t *testing.T) {
	l, err := NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	cmds := make(chan Command, 8)
	fields := make(chan FieldEvent, 8)
	audios := make(chan AudioEvent, 8)
	fieldSizeFn := func() uint32 { return 720 * 240 * 3 }
	go l.RunWithFields(cmds, fields, audios, fieldSizeFn)

	addr := l.Addr().(*net.UDPAddr)
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send a 3-byte AUDIO header declaring 8 bytes of PCM.
	pcm := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	hdr := groovy.BuildAudioHeader(uint16(len(pcm)))
	if _, err := conn.Write(hdr); err != nil {
		t.Fatal(err)
	}
	// Send the PCM in one datagram (well under MTU).
	if _, err := conn.Write(pcm); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-audios:
		if !bytes.Equal(ev.PCM, pcm) {
			t.Errorf("PCM mismatch: got %x, want %x", ev.PCM, pcm)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for AudioEvent")
	}
}
```

Add imports `"bytes"`, `"net"`, `"time"` to `listener_test.go` if not already present. `groovy` is already imported for the existing tests.

- [ ] **Step 2: Run the new test** — it should compile and pass already (it tests existing `RunWithFields` behavior). This locks in the behavior going forward.

Run: `go test ./internal/fakemister/... -run TestRunWithFields_ReassemblesAudioPayload -v`
Expected: PASS.

- [ ] **Step 3: Upgrade `newScenarioHarness`** to use `RunWithFields` and fan audio events into the Recorder. In `tests/integration/scenarios_test.go`, replace the current body of `newScenarioHarness` (the part from `events := make(...)` through `<-recDone` in the cleanup) with the wiring below.

Replace the channel setup + goroutines:

```go
	events := make(chan fakemister.Command, 4096)
	fieldsCh := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 4096)
	rec := fakemister.NewRecorder()
	recDone := make(chan struct{})
	runDone := make(chan struct{})
	drainDone := make(chan struct{})
	audioFanDone := make(chan struct{})

	go func() {
		for c := range events {
			rec.Record(c)
		}
		close(recDone)
	}()
	// Drain fields — scenario assertions only count BLIT headers (via Command records),
	// not reassembled field payloads.
	go func() {
		for range fieldsCh {
		}
		close(drainDone)
	}()
	// Fan AudioEvents into synthetic Commands with AudioPayload so Recorder.audioBytes increments.
	go func() {
		for ev := range audios {
			events <- fakemister.Command{
				Type:         groovy.CmdAudio,
				AudioPayload: &fakemister.AudioPayload{PCM: ev.PCM},
			}
		}
		close(audioFanDone)
	}()
	fieldSizeFn := func() uint32 { return 720 * 240 * 3 } // RAW BLIT fallback size
	go func() {
		l.RunWithFields(events, fieldsCh, audios, fieldSizeFn)
		close(runDone)
	}()
```

Replace the cleanup function:

```go
	cleaned := false
	var cleanMu sync.Mutex
	cleanup := func() {
		cleanMu.Lock()
		defer cleanMu.Unlock()
		if cleaned {
			return
		}
		cleaned = true
		_ = mgr.Stop()
		sender.Close()
		l.Close()
		<-runDone
		// Listener has exited; close the downstream channels so the fan-in
		// and drain goroutines terminate, then close events and wait for
		// the recorder goroutine.
		close(fieldsCh)
		close(audios)
		<-drainDone
		<-audioFanDone
		close(events)
		<-recDone
	}
```

- [ ] **Step 4: Verify the upgraded harness still passes existing scenarios** on Linux (or defer to CI on Windows).

Run: `go test -tags=integration ./tests/integration/... -v`
Expected on Linux: all non-Cast scenarios pass. `TestScenario_Cast` now exercises audio and should report `AudioBytes >= 200_000`.
Expected on Windows: 7 of 8 skipped; the one that runs (`TestBasic_InitSwitchresClose`) passes.

- [ ] **Step 5: Full test verification**

Run: `go test ./...`
Expected: PASS. `go test -tags=integration ./tests/integration/...` — PASS on Linux.
Run: `go vet ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/fakemister/listener_test.go tests/integration/scenarios_test.go
git commit -m "test(integration): scenario harness records AUDIO payload bytes"
```

---

## Task 2 (C1): Filter chain produces 59.94 fields/sec for any source rate (TDD)

**Context:** `buildFilterChain` in `internal/ffmpeg/pipeline.go:57-116` uses `fps=30000/1001 → interlace → separatefields`. FFmpeg's `interlace` filter halves the input frame rate, so 29.97p input produces 14.985i, then `separatefields` produces 29.97 fields/sec — half the target. Only the 23.976p→`telecine=pattern=23` branch reaches 59.94. The data-plane field timer still runs at 59.94 Hz; the FFmpeg pipe backpressures at the wrong rate and the Plane emits duplicate BLITs for roughly half of all fields.

**Fix approach:** Unified chain that always outputs 59.94 progressive frames *before* the interlace step. For 23.976p sources (detected from the probe-reported frame rate), substitute `telecine=pattern=23` for `fps=60000/1001` to get film-accurate 3:2 cadence.

```
decode
  → [yadif=mode=send_frame if input is interlaced]
  → fps=60000/1001   (or telecine=pattern=23 for 23.976p sources)
  → crop
  → scale=720:480
  → [subtitles=filename='<path>':si=<idx> if present]
  → interlace=scan=<tff|bff>:lowpass=0
  → separatefields
  → format=rgb24
```

Output: 59.94 fields/sec regardless of source.

**Files:**
- Modify: `internal/ffmpeg/pipeline.go` (rewrite `buildFilterChain` step 2).
- Modify: `internal/ffmpeg/pipeline_test.go` (per-rate tests).
- Modify: `tests/integration/plane_test.go` (per-rate end-to-end).

- [ ] **Step 1: Write the failing per-rate unit tests** in `internal/ffmpeg/pipeline_test.go`. Append:

```go
// TestBuildFilterChain_SourceRateProducesCorrectNormalizer validates that the
// rate-normalization filter chosen for each source rate either produces a
// 59.94 progressive intermediate (fps=60000/1001) or leaves the output at
// 29.97i via telecine (which separatefields then doubles to 59.94 fields).
func TestBuildFilterChain_SourceRateProducesCorrectNormalizer(t *testing.T) {
	cases := []struct {
		name      string
		frameRate float64
		want      string // normalizer filter that MUST appear before `interlace=`
	}{
		{"film 23.976p", 23.976, "telecine=pattern=23"},
		{"film 24p", 24.0, "fps=60000/1001"},
		{"tv 29.97p", 29.97, "fps=60000/1001"},
		{"tv 30p", 30.0, "fps=60000/1001"},
		{"sports 59.94p", 59.94, "fps=60000/1001"},
		{"sports 60p", 60.0, "fps=60000/1001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := PipelineSpec{
				SourceProbe:  &ProbeResult{FrameRate: tc.frameRate, Interlaced: false},
				OutputWidth:  720,
				OutputHeight: 480,
				FieldOrder:   "tff",
				AspectMode:   "letterbox",
			}
			chain := buildFilterChain(spec)
			if !strings.Contains(chain, tc.want) {
				t.Errorf("rate %.3f: chain missing %q\nchain=%s", tc.frameRate, tc.want, chain)
			}
			// Every chain must still terminate in interlace + separatefields.
			if !strings.Contains(chain, "interlace=scan=tff:lowpass=0") {
				t.Errorf("rate %.3f: chain missing interlace filter", tc.frameRate)
			}
			if !strings.HasSuffix(chain, "separatefields") {
				t.Errorf("rate %.3f: chain must end with separatefields, got %s", tc.frameRate, chain)
			}
		})
	}
}
```

Imports: `"strings"` and `"testing"` should already be present.

- [ ] **Step 2: Run the new test to verify it fails**

Run: `go test ./internal/ffmpeg/... -run TestBuildFilterChain_SourceRateProducesCorrectNormalizer -v`
Expected: FAIL — the chain currently produces `fps=30000/1001` for every non-23.976 rate. The test looks for `fps=60000/1001`.

- [ ] **Step 3: Rewrite the rate-normalization branch** in `internal/ffmpeg/pipeline.go`. Replace lines 66–76 (the `// 2. Normalise to 29.97p.` block) with:

```go
	// 2. Normalise to 59.94p.
	//    For 23.976p film sources, use telecine=pattern=23 so the 2:3
	//    cadence lands on the fields correctly (film-accurate). Everything
	//    else is rate-converted to 59.94p via fps=60000/1001. The downstream
	//    interlace filter halves the rate to 29.97i; separatefields then
	//    doubles it back to 59.94 fields/sec.
	if s.SourceProbe != nil {
		fr := s.SourceProbe.FrameRate
		switch {
		case fr >= 23.0 && fr < 24.0:
			filters = append(filters, "telecine=pattern=23")
		default:
			filters = append(filters, "fps=60000/1001")
		}
	}
```

Also update the doc comment above `buildFilterChain` (the `// Order is load-bearing:` block) to reflect the 59.94p intermediate:

```go
//  1. yadif (only if interlaced source) → one progressive frame per input frame.
//  2. fps=60000/1001 → normalise to 59.94p (or telecine=pattern=23 for 23.976p).
//  3. crop/scale/pad for aspect mode.
//  4. subtitle burn-in BEFORE interlacing.
//  5. interlace=scan=tff|bff:lowpass=0 → halves rate to 29.97i.
//  6. separatefields → 59.94 fields/sec at OutputWidth×(OutputHeight/2).
```

- [ ] **Step 4: Run the per-rate tests — they should pass now**

Run: `go test ./internal/ffmpeg/... -run TestBuildFilterChain_SourceRateProducesCorrectNormalizer -v`
Expected: PASS for all 6 rate cases.

- [ ] **Step 5: Run the full ffmpeg test suite** to make sure no existing test regressed.

Run: `go test ./internal/ffmpeg/... -v`
Expected: all pass.

- [ ] **Step 6: Add an end-to-end rate-verification integration test** in `tests/integration/plane_test.go`. Append a test that generates synthetic clips at two different rates (23.976p and 60p — the extremes) and asserts the sender emits ~300 fields for a 5-second clip each. This requires the `ensureSampleMP4` helper to accept a rate argument; if it doesn't, extend it here.

If `ensureSampleMP4` doesn't already take a rate, add a sibling `ensureSampleMP4Rate(t, name, seconds, rate)` in `tests/integration/testdata_helper_test.go` that calls ffmpeg with `-r <rate>`:

```go
// ensureSampleMP4Rate generates a silent/colour-bar MP4 at a specific frame
// rate. Used by filter-chain rate tests that need to exercise both film
// (23.976) and sports (60) sources end-to-end.
func ensureSampleMP4Rate(t *testing.T, name string, seconds int, rate string) string {
	t.Helper()
	dir := testdataDir(t)
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg",
		"-y", "-f", "lavfi",
		"-i", fmt.Sprintf("testsrc=size=720x480:rate=%s:duration=%d", rate, seconds),
		"-f", "lavfi",
		"-i", fmt.Sprintf("sine=frequency=440:duration=%d", seconds),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast",
		"-c:a", "aac",
		"-shortest",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg sample gen: %v\n%s", err, out)
	}
	return path
}
```

Then in `tests/integration/plane_test.go` append (or create a new `tests/integration/filter_rate_test.go`):

```go
//go:build integration

package integration

import (
	"runtime"
	"testing"
	"time"

	"github.com/jedivoodoo/mister-groovy-relay/internal/core"
	"github.com/jedivoodoo/mister-groovy-relay/internal/groovy"
)

// TestFilterRate_FieldsPerSecondMatchesTarget runs the full FFmpeg pipeline
// against synthetic clips at 23.976p and 60p and asserts the sender emits the
// expected ~300 fields per 5-second clip regardless of source rate. This is
// the regression harness for C1 — before the fix, 60p input produced ~150
// fields (half rate).
func TestFilterRate_FieldsPerSecondMatchesTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}
	cases := []struct {
		name string
		rate string
	}{
		{"film_23.976p", "24000/1001"},
		{"sports_60p", "60"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sample := ensureSampleMP4Rate(t, tc.name+".mp4", 5, tc.rate)
			h := newScenarioHarness(t)
			req := core.SessionRequest{
				StreamURL:    sample,
				AdapterRef:   tc.name,
				DirectPlay:   true,
				Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
			}
			if err := h.Manager.StartSession(req); err != nil {
				t.Fatalf("start: %v", err)
			}
			waitIdle(t, h, 15*time.Second)
			time.Sleep(200 * time.Millisecond)

			snap := h.Recorder.Snapshot()
			blits := snap.Counts[groovy.CmdBlitFieldVSync]
			if blits < 255 || blits > 345 {
				t.Errorf("%s: expected ~300 blits, got %d", tc.name, blits)
			}
		})
	}
}
```

- [ ] **Step 7: Run integration tests**

Run: `go test -tags=integration ./tests/integration/... -run TestFilterRate -v`
Expected on Linux: PASS. Windows skips.

- [ ] **Step 8: Full verification**

Run: `go test ./... && go vet ./...`
Expected: all clean.

- [ ] **Step 9: Commit**

```bash
git add internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go \
        tests/integration/plane_test.go tests/integration/testdata_helper_test.go
# If you added a new filter_rate_test.go file, add it too:
# git add tests/integration/filter_rate_test.go
git commit -m "feat(ffmpeg): filter chain normalizes any source rate to 59.94 fields/sec"
```

---

## Task 3 (C3): LZ4 returns ok=false on incompressible input; Plane emits RAW BLIT (TDD)

**Context:** `LZ4Compress` in `internal/groovy/lz4.go` calls `pierrec/lz4/v4`'s block-API `CompressBlock`, which returns `n=0` (no error) when input is incompressible. Current code silently returns `dst[:0]`. `Plane.sendField` then emits a 12-byte LZ4 BLIT header with `CompressedSize=0` and zero payload bytes — the receiver cannot decode that. One noisy/encrypted frame desyncs a session.

**Fix approach:** Change `LZ4Compress` to `(compressed []byte, ok bool)`. `ok == false` when `n == 0` or `n >= len(src)`. In `Plane.sendField`, when `ok == false`, emit a RAW BLIT variant (the 8-byte `BlitHeaderRaw` header) with raw bytes, not the LZ4 variant.

**Files:**
- Modify: `internal/groovy/lz4.go` (signature change).
- Modify: `internal/groovy/lz4_test.go` (incompressible test).
- Modify: `internal/dataplane/plane.go` (`sendField` branches on `ok`).
- Modify: `internal/dataplane/plane_test.go` (new RAW-fallback test).

**Note:** The signature change propagates. Grep for callers before starting: `grep -rn LZ4Compress internal/`. Expected callers: `internal/dataplane/plane.go::sendField`. That's it.

- [ ] **Step 1: Write the failing unit test for `LZ4Compress` incompressible input.** Append to `internal/groovy/lz4_test.go`:

```go
func TestLZ4Compress_IncompressibleReturnsFalse(t *testing.T) {
	// 720×240 RGB888 = 518 400 bytes of crypto/rand → nothing to compress.
	src := make([]byte, 720*240*3)
	if _, err := rand.Read(src); err != nil {
		t.Fatal(err)
	}
	out, ok := LZ4Compress(src)
	if ok {
		t.Errorf("incompressible input returned ok=true (len=%d); want ok=false", len(out))
	}
	if len(out) != 0 {
		t.Errorf("incompressible input returned %d bytes; want 0", len(out))
	}
}

func TestLZ4Compress_CompressibleReturnsTrue(t *testing.T) {
	// Highly compressible: all-zeros.
	src := make([]byte, 720*240*3)
	out, ok := LZ4Compress(src)
	if !ok {
		t.Error("compressible input returned ok=false")
	}
	if len(out) == 0 || len(out) >= len(src) {
		t.Errorf("compressible output should be 0 < len < %d, got %d", len(src), len(out))
	}
}
```

Add `"crypto/rand"` to the imports if not already present.

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/groovy/... -run TestLZ4Compress -v`
Expected: **compile failure** — `LZ4Compress` currently returns `([]byte, error)`, not `([]byte, bool)`. That's the point: the signature change is part of the test.

- [ ] **Step 3: Update `LZ4Compress` signature + semantics** in `internal/groovy/lz4.go`. Replace lines 12–20 with:

```go
// LZ4Compress compresses src using the LZ4 block format (NOT frame format).
// Returns the compressed bytes and ok=true when compression reduced the size.
// Returns (nil, false) when CompressBlock reports the input as incompressible
// (n == 0) or when the output would be no smaller than the input. Callers
// emit the RAW BLIT header variant in the ok=false case — never an LZ4 header
// with zero-length payload (the receiver cannot decode that).
//
// A genuine lz4 library error still panics: the library only errors on
// programmer mistakes (e.g. dst too small), and the dst sizing below is
// bounded correctly.
func LZ4Compress(src []byte) ([]byte, bool) {
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	var c lz4.Compressor
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		panic(fmt.Errorf("lz4 compress (dst sized by CompressBlockBound): %w", err))
	}
	if n == 0 || n >= len(src) {
		return nil, false
	}
	return dst[:n], true
}
```

- [ ] **Step 4: Run the lz4 tests**

Run: `go test ./internal/groovy/... -run TestLZ4Compress -v`
Expected: PASS.

Run: `go test ./internal/groovy/... -v`
Expected: pre-existing LZ4 tests (round-trip etc.) must still pass. If any existing test references the old `(..., error)` return, update it — grep for `LZ4Compress` in `internal/groovy/lz4_test.go` and flip `err := ... ; if err != nil` to `_, ok := ... ; if !ok`.

- [ ] **Step 5: Update `Plane.sendField`** in `internal/dataplane/plane.go` to branch on `ok` and emit RAW when compression fails. Replace the `sendField` body (lines 179–202) with:

```go
// sendField sends one BLIT_FIELD_VSYNC header + payload. Applies congestion
// backoff before the header and records the payload size afterwards so the
// next call can honor the reference ~11 ms wait after any >500 KB blit.
//
// Compression policy: if LZ4 is enabled AND the field is compressible
// (LZ4Compress returns ok=true), the LZ4 BLIT variant is emitted. Otherwise
// — either LZ4 is disabled in config, OR the field is incompressible (e.g.
// random-noise content, encrypted stream payload) — a RAW BLIT variant is
// emitted with the uncompressed bytes. Emitting an LZ4 header with
// CompressedSize=0 would desync the receiver.
func (p *Plane) sendField(frame uint32, field uint8, raw []byte) {
	opts := groovy.BlitOpts{Frame: frame, Field: field}
	payload := raw
	if p.cfg.LZ4Enabled {
		if compressed, ok := groovy.LZ4Compress(raw); ok {
			payload = compressed
			opts.Compressed = true
			opts.CompressedSize = uint32(len(compressed))
		} else {
			slog.Debug("lz4 incompressible frame; falling back to RAW BLIT", "size", len(raw))
		}
	}
	p.cfg.Sender.WaitForCongestion()
	if err := p.cfg.Sender.Send(groovy.BuildBlitHeader(opts)); err != nil {
		slog.Warn("blit header send", "err", err)
		return
	}
	if err := p.cfg.Sender.SendPayload(payload); err != nil {
		slog.Warn("blit payload send", "err", err)
		return
	}
	p.cfg.Sender.MarkBlitSent(len(payload))
}
```

Also remove the now-unused `"fmt"` import from `internal/groovy/lz4.go` if it's no longer referenced (after Step 3 the panic still uses `fmt.Errorf`, so leave it).

- [ ] **Step 6: Add a plane regression test** that asserts the RAW header variant is emitted when LZ4Compress returns ok=false. In `internal/dataplane/plane_test.go` append (or create):

```go
// TestSendField_RawFallbackOnIncompressible verifies that when the LZ4
// compressor returns ok=false (incompressible input), sendField emits an
// 8-byte RAW BLIT header — not a 12-byte LZ4 header with CompressedSize=0.
// This is the regression harness for C3: the LZ4 header variant is invalid
// on the wire when CompressedSize=0, and an earlier bug allowed that.
func TestSendField_RawFallbackOnIncompressible(t *testing.T) {
	// Stand up a loopback UDP listener as the "MiSTer"; capture datagrams.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	p := &Plane{cfg: PlaneConfig{Sender: sender, LZ4Enabled: true}}

	// Random bytes — LZ4Compress will return ok=false for a 518 400-byte
	// crypto/rand field.
	field := make([]byte, 720*240*3)
	if _, err := cryptorand.Read(field); err != nil {
		t.Fatal(err)
	}

	done := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 4096)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				close(done)
				return
			}
			cp := make([]byte, n)
			copy(cp, buf[:n])
			done <- cp
		}
	}()

	p.sendField(0, 0, field)

	// The first datagram is the BLIT header. Expect 8 bytes (RAW), not 12
	// (LZ4).
	hdr, ok := <-done
	if !ok {
		t.Fatal("no header datagram received")
	}
	if len(hdr) != groovy.BlitHeaderRaw {
		t.Errorf("got header length %d, want %d (RAW variant)", len(hdr), groovy.BlitHeaderRaw)
	}
	if hdr[0] != groovy.CmdBlitFieldVSync {
		t.Errorf("header[0] = %#x, want CmdBlitFieldVSync %#x", hdr[0], groovy.CmdBlitFieldVSync)
	}
}
```

Add imports as needed: `"net"`, `"testing"`, `"time"`, `cryptorand "crypto/rand"`, plus the two internal packages `groovy` and `groovynet`.

- [ ] **Step 7: Run plane tests**

Run: `go test ./internal/dataplane/... -v`
Expected: PASS.

- [ ] **Step 8: Full verification**

Run: `go test ./... && go vet ./... && go test -tags=integration ./tests/integration/...`
Expected: all clean (Windows skips the integration audio/field scenarios).

- [ ] **Step 9: Commit**

```bash
git add internal/groovy/lz4.go internal/groovy/lz4_test.go \
        internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "fix(groovy): LZ4 returns ok=false on incompressible input; Plane emits RAW BLIT"
```

---

## Task 4 (I4): Position uses integer-exact field-count accumulator (TDD)

**Context:** `Plane.Run` in `internal/dataplane/plane.go:120` increments `positionMs.Add(int64(16))` per field tick. One field at 60000/1001 Hz is 16.6835 ms, so each tick reports ~0.6835 ms slow. Over 60 minutes that's ~2.4 minutes of under-reporting to the Plex timeline.

**Fix approach:** Track field count, not milliseconds. Compute `Position()` from the integer-exact formula `fields * 1001 / 60` ms. No floating point, no rounding.

**Files:**
- Modify: `internal/dataplane/plane.go` (struct field rename + Position + tick).
- Modify: `internal/dataplane/plane_test.go` (exact-value regression test).

- [ ] **Step 1: Write the failing test** in `internal/dataplane/plane_test.go`. Append:

```go
// TestPosition_IntegerExactFieldCount verifies that after N ticks Position()
// returns exactly N*1001/60 ms plus the base offset. Regression harness for
// I4 — the old code added 16 ms/tick and drifted ~0.68 ms low per field.
func TestPosition_IntegerExactFieldCount(t *testing.T) {
	cases := []struct {
		ticks         int64
		baseOffsetMs  int
		wantPosMs     int64
	}{
		{3600, 0, 60_060},        // 60.06 s of playback at 59.94 Hz
		{60_000, 0, 1_001_000},   // ~16.68 min
		{600, 5_000, 5_000 + 10_010}, // 10 s of playback, resumed at 5 s
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			p := &Plane{}
			p.cfg.SeekOffsetMs = tc.baseOffsetMs
			p.resetPosition()
			for i := int64(0); i < tc.ticks; i++ {
				p.advancePosition()
			}
			got := p.Position()
			wantDur := time.Duration(tc.wantPosMs) * time.Millisecond
			if got != wantDur {
				t.Errorf("ticks=%d offset=%d: Position=%v, want %v",
					tc.ticks, tc.baseOffsetMs, got, wantDur)
			}
		})
	}
}
```

Note this test references `resetPosition()` and `advancePosition()` methods that don't exist yet — that's intentional. The test will fail to compile, which is the TDD red stage.

- [ ] **Step 2: Run the new test — expect compile failure**

Run: `go test ./internal/dataplane/... -run TestPosition_IntegerExactFieldCount -v`
Expected: FAIL to compile (`undefined: resetPosition`, `undefined: advancePosition`).

- [ ] **Step 3: Implement the field-count accumulator** in `internal/dataplane/plane.go`.

Replace the `positionMs atomic.Int64` field on the `Plane` struct with `positionFields atomic.Int64`:

```go
type Plane struct {
	cfg        PlaneConfig
	proc       *ffmpeg.Process
	positionFields atomic.Int64 // fields emitted since session start; Position() derives ms
	audioReady atomic.Bool
	fpgaFrame  atomic.Uint32
	done       chan struct{}
}
```

Replace the `Position()` method with:

```go
// Position returns the current playback offset since start. Seeded with
// cfg.SeekOffsetMs; advanced by one NTSC field period (1001/60 ms, exact)
// per tick. The timeline broadcaster (plex adapter) queries this every
// second; exact integer math prevents drift relative to PMS's timestamps.
func (p *Plane) Position() time.Duration {
	fields := p.positionFields.Load()
	ms := fields*1001/60 + int64(p.cfg.SeekOffsetMs)
	return time.Duration(ms) * time.Millisecond
}

// resetPosition clears the field counter. Called at the start of Run before
// the pump loop begins — ensures each session starts at exactly the seek
// offset.
func (p *Plane) resetPosition() {
	p.positionFields.Store(0)
}

// advancePosition increments the field counter by one. Called once per field
// tick after a successful BLIT (or BLIT-dup) send.
func (p *Plane) advancePosition() {
	p.positionFields.Add(1)
}
```

In `Plane.Run` (around line 120–121), replace the `const fieldPeriodMs = int64(16)` + `p.positionMs.Store(...)` pair with:

```go
	p.resetPosition()
```

And at the end of the tick handler (around line 171), replace `p.positionMs.Add(fieldPeriodMs)` with:

```go
			p.advancePosition()
```

- [ ] **Step 4: Run the failing test — it should pass now**

Run: `go test ./internal/dataplane/... -run TestPosition_IntegerExactFieldCount -v`
Expected: PASS. All three cases hit exact equality.

- [ ] **Step 5: Run the full dataplane test suite** to catch any incidental regressions (e.g. a plane_test that referenced `positionMs` directly).

Run: `go test ./internal/dataplane/... -v`
Expected: all pass. If any test references `positionMs`, update it to use `positionFields` + the public `Position()` API.

- [ ] **Step 6: Full verification**

Run: `go test ./... && go vet ./...`
Expected: all clean.

Run integration smoke on Linux (skip on Windows): `go test -tags=integration ./tests/integration/...`
Expected: existing scenarios pass (position is queried by the timeline broker; a slight value shift is compatible with the current assertions).

- [ ] **Step 7: Commit**

```bash
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "fix(dataplane): Position uses integer-exact field-count accumulator"
```

---

## Task 5 (I5): Audio chunk size from integer-exact fraction (TDD)

**Context:** `AudioChunkSize(48000, 2)` in `internal/dataplane/audiopipe.go:24-26` returns `int(192000/59.94) == 3203` bytes/field. Real per-field rate is `192000 * 1001 / 60000 = 3203.2` bytes. Reader under-consumes FFmpeg by ~53 B/sec; audio backpressure → stutter / A/V drift — the exact failure mode the single-FFmpeg design was meant to prevent.

**Fix approach:** Drop the constant. The reader tracks total `fieldsRead` and `bytesRead`. Per field, compute `expected := (fieldsRead+1) * sampleRate * channels * 2 * 1001 / 60000`, read `expected - bytesRead` bytes, update both counters. Exact integer math, no drift.

**Files:**
- Modify: `internal/dataplane/audiopipe.go` (new reader struct + per-field method).
- Modify: `internal/dataplane/audiopipe_test.go` (integer-exact assertions).
- Modify: `internal/dataplane/plane.go` (construct reader and call its per-field method).

**Design note:** The current `ReadAudioFromPipe` runs in its own goroutine and pushes chunks onto a channel. The Plane then `select`s on the channel per tick. We need to preserve that shape. The simplest approach is: `ReadAudioFromPipe` loops using an internal `*AudioPipeReader` struct and calls `reader.NextChunk(r)` — each call computes the exact next chunk size. External API (channel send) is unchanged.

- [ ] **Step 1: Write the failing test** in `internal/dataplane/audiopipe_test.go`. Append:

```go
// TestAudioPipeReader_IntegerExactCumulative verifies the reader consumes
// exactly sampleRate * channels * 2 bytes per second (no drift) by integer
// math. Regression harness for I5 — old code rounded 3203.2 → 3203 and
// drifted ~53 B/sec.
func TestAudioPipeReader_IntegerExactCumulative(t *testing.T) {
	cases := []struct {
		sampleRate int
		channels   int
		fields     int64
	}{
		{48000, 2, 3596},   // ~60 s
		{48000, 2, 60_000}, // ~16.68 min
		{44100, 2, 3596},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			r := NewAudioPipeReader(tc.sampleRate, tc.channels)
			var total int64
			for i := int64(0); i < tc.fields; i++ {
				total += int64(r.NextChunkSize())
				r.Advance(r.lastSize) // see implementation
			}
			want := tc.fields * int64(tc.sampleRate*tc.channels*2) * 1001 / 60000
			if total != want {
				t.Errorf("sampleRate=%d channels=%d fields=%d: got cumulative=%d want=%d",
					tc.sampleRate, tc.channels, tc.fields, total, want)
			}
		})
	}
}
```

Note this test exercises a new API (`NewAudioPipeReader`, `NextChunkSize`, `Advance`, and a struct field `lastSize`). That's intentional — the public shape is part of this task.

- [ ] **Step 2: Run the new test — expect compile failure**

Run: `go test ./internal/dataplane/... -run TestAudioPipeReader_IntegerExactCumulative -v`
Expected: FAIL (undefined `NewAudioPipeReader` etc).

- [ ] **Step 3: Implement the reader** in `internal/dataplane/audiopipe.go`. Replace the file's current body with:

```go
package dataplane

import (
	"io"
)

// bytesPerSample is the s16le output format the FFmpeg audio pipe produces
// (see BuildCommand: `-f s16le`). 16-bit little-endian = 2 bytes per sample
// per channel.
const bytesPerSample = 2

// AudioPipeReader computes per-field PCM chunk sizes using integer-exact
// arithmetic against the NTSC 60000/1001 Hz field rate, so no rounding
// drift accumulates between the FFmpeg pipe and the field pump. One reader
// per session; caller iterates by calling NextChunkSize, reading that many
// bytes from the pipe, then Advance to account for the bytes actually read.
type AudioPipeReader struct {
	sampleRate int
	channels   int
	fieldsRead int64
	bytesRead  int64
	lastSize   int // size returned by the most recent NextChunkSize call
}

// NewAudioPipeReader returns a reader seeded at field 0, bytes 0.
func NewAudioPipeReader(sampleRate, channels int) *AudioPipeReader {
	return &AudioPipeReader{sampleRate: sampleRate, channels: channels}
}

// NextChunkSize returns the exact number of bytes the caller should read from
// the audio pipe for the NEXT field tick. Derived from the integer formula
// (fieldsRead+1) * sampleRate * channels * 2 * 1001 / 60000 - bytesRead.
// Never returns negative; if sampleRate*channels is zero (misconfigured),
// returns 0 so the caller can treat it as "no audio".
func (r *AudioPipeReader) NextChunkSize() int {
	per := int64(r.sampleRate) * int64(r.channels) * int64(bytesPerSample)
	if per <= 0 {
		r.lastSize = 0
		return 0
	}
	expected := (r.fieldsRead + 1) * per * 1001 / 60000
	n := int(expected - r.bytesRead)
	if n < 0 {
		n = 0
	}
	r.lastSize = n
	return n
}

// Advance records that `got` bytes were actually read in response to the
// most recent NextChunkSize call, and increments the field counter. `got`
// may be less than lastSize on a short read (EOF); the next call to
// NextChunkSize will compensate automatically.
func (r *AudioPipeReader) Advance(got int) {
	r.bytesRead += int64(got)
	r.fieldsRead++
}

// ReadAudioFromPipe reads PCM chunks sized by AudioPipeReader from r and
// sends each on out. Closes out on EOF or any read error (including a
// truncated tail).
//
// Chunk size averages to sampleRate*channels*2 / 59.94 but varies by ±1
// byte between ticks to keep cumulative consumption integer-exact against
// the 60000/1001 Hz field rate.
func ReadAudioFromPipe(r io.Reader, sampleRate, channels int, out chan<- []byte) {
	defer close(out)
	reader := NewAudioPipeReader(sampleRate, channels)
	for {
		size := reader.NextChunkSize()
		if size <= 0 {
			return
		}
		buf := make([]byte, size)
		n, err := io.ReadFull(r, buf)
		reader.Advance(n)
		if err != nil {
			return
		}
		out <- buf
	}
}
```

**Deletion:** Remove the `AudioChunkSize` function and the `fieldsPerSecond` constant. Grep for external references — if any test imports `AudioChunkSize`, delete or update it:

```bash
grep -rn "AudioChunkSize\|fieldsPerSecond" internal/ tests/
```

Expected: only hits are in `audiopipe.go` and `audiopipe_test.go`. Update `audiopipe_test.go` to remove any test of the old function (the new `TestAudioPipeReader_IntegerExactCumulative` replaces it).

- [ ] **Step 4: Run the test — it should pass now**

Run: `go test ./internal/dataplane/... -run TestAudioPipeReader -v`
Expected: PASS for all three cases.

- [ ] **Step 5: Run all dataplane tests**

Run: `go test ./internal/dataplane/... -v`
Expected: PASS. If any existing test references `AudioChunkSize`, either delete it or port to the new API.

- [ ] **Step 6: Full verification**

Run: `go test ./... && go vet ./... && go test -tags=integration ./tests/integration/...`
Expected: all clean. Integration tests continue to produce the expected audio byte counts (the new reader averages to the same rate; only the drift is removed).

- [ ] **Step 7: Commit**

```bash
git add internal/dataplane/audiopipe.go internal/dataplane/audiopipe_test.go
git commit -m "fix(dataplane): audio chunk size from integer-exact fraction"
```

---

## Task 6 (I8): Probe/ProbeCrop run outside Manager.mu with bounded context

**Context:** `Manager.startPlaneLocked` in `internal/core/manager.go:73-97` calls `ffmpeg.Probe(context.Background(), ...)` and `ffmpeg.ProbeCrop(context.Background(), ...)` while holding `m.mu`. A stuck PMS call blocks every other control operation (Pause, Stop, SeekTo) indefinitely.

**Fix approach:** Restructure `StartSession`, `Play`, and `SeekTo` so they run Probe/ProbeCrop *before* acquiring `m.mu`, with a bounded `context.WithTimeout(parentCtx, 10*time.Second)`. `startPlaneLocked` becomes `startPlaneLocked(req, offsetMs, probe, cropRect)` — caller is responsible for probing.

**Files:**
- Modify: `internal/core/manager.go` (signature change, all three call sites).
- Modify: `internal/core/manager_test.go` (regression: concurrent Stop during slow Probe).

- [ ] **Step 1: Refactor `startPlaneLocked` to take probe + cropRect as parameters.** In `internal/core/manager.go`, change the function signature and delete the probe calls from its body:

```go
// startPlaneLocked spawns a new data plane. Caller MUST hold m.mu AND have
// already run Probe/ProbeCrop (passed in as probe + cropRect) — this
// function must not perform network I/O while the mutex is held.
func (m *Manager) startPlaneLocked(req SessionRequest, offsetMs int,
	probe *ffmpeg.ProbeResult, cropRect *ffmpeg.CropRect) error {
	// 1. Preempt and await prior plane. Drop the lock while awaiting Done()
	//    so the plane's exit goroutine (which re-acquires m.mu to clear
	//    m.plane) is free to run.
	if m.cancelFn != nil {
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
		}
	}

	// Resolve the SWITCHRES modeline from config (falls back to NTSC 480i60).
	modeline, err := resolveModeline(m.cfg.Modeline)
	if err != nil {
		return err
	}
	rgbMode, err := resolveRGBMode(m.cfg.RGBMode)
	if err != nil {
		return err
	}

	// Per-field vActive for interlaced modes; full height otherwise.
	fieldH := int(modeline.VActive)
	bpp := bytesPerPixel(rgbMode)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	spec := ffmpeg.PipelineSpec{
		InputURL:        req.StreamURL,
		InputHeaders:    req.InputHeaders,
		SeekSeconds:     float64(offsetMs) / 1000.0,
		UseSSSeek:       req.DirectPlay,
		SourceProbe:     probe,
		OutputWidth:     int(modeline.HActive),
		OutputHeight:    fieldH * 2,
		FieldOrder:      m.cfg.InterlaceFieldOrder,
		AspectMode:      m.cfg.AspectMode,
		CropRect:        cropRect,
		SubtitleURL:     req.SubtitleURL,
		SubtitleIndex:   req.SubtitleIndex,
		AudioSampleRate: m.cfg.AudioSampleRate,
		AudioChannels:   m.cfg.AudioChannels,
	}

	plane := dataplane.NewPlane(dataplane.PlaneConfig{
		Sender:        m.sender,
		SpawnSpec:     spec,
		Modeline:      modeline,
		FieldWidth:    int(modeline.HActive),
		FieldHeight:   fieldH,
		BytesPerPixel: bpp,
		RGBMode:       rgbMode,
		LZ4Enabled:    m.cfg.LZ4Enabled,
		AudioRate:     m.cfg.AudioSampleRate,
		AudioChans:    m.cfg.AudioChannels,
		SeekOffsetMs:  offsetMs,
	})
	m.plane = plane
	m.active = &activeSession{req: req, startedAt: time.Now(), baseOffsetMs: offsetMs}

	go func() {
		runErr := plane.Run(ctx)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			slog.Warn("data plane exited", "err", runErr)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.plane != plane {
			return
		}
		m.plane = nil
		if runErr == nil {
			_ = m.fsm.Transition(EvEOF)
		}
	}()
	return nil
}
```

- [ ] **Step 2: Add a helper `probeForStart`** that performs both probes under a bounded context. Add to `internal/core/manager.go`:

```go
// probeForStart runs Probe and (conditionally) ProbeCrop with a bounded
// context so a stuck PMS cannot deadlock the control plane. Called by
// StartSession/Play/SeekTo BEFORE acquiring Manager.mu so the mutex is
// never held during network I/O.
func (m *Manager) probeForStart(req SessionRequest) (*ffmpeg.ProbeResult, *ffmpeg.CropRect, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe, err := ffmpeg.Probe(ctx, req.StreamURL)
	if err != nil {
		return nil, nil, fmt.Errorf("probe source: %w", err)
	}
	var cropRect *ffmpeg.CropRect
	if m.cfg.AspectMode == "auto" {
		// ProbeCrop failures degrade gracefully to letterbox — ignore the error.
		cropRect, _ = ffmpeg.ProbeCrop(ctx, req.StreamURL, req.InputHeaders, 2*time.Second)
	}
	return probe, cropRect, nil
}
```

- [ ] **Step 3: Update the three call sites.** Modify `StartSession`:

```go
func (m *Manager) StartSession(req SessionRequest) error {
	probe, cropRect, err := m.probeForStart(req)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startPlaneLocked(req, req.SeekOffsetMs, probe, cropRect); err != nil {
		return err
	}
	return m.fsm.Transition(EvPlayMedia)
}
```

Modify `Play` (resume after pause — same probe + offset logic):

```go
func (m *Manager) Play() error {
	// Capture the active request outside the lock so we can probe against
	// the same URL without holding the mutex.
	m.mu.Lock()
	a := m.active
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("no session to resume")
	}
	req := a.req
	resumeMs := int(a.pausedPosition / time.Millisecond)
	if resumeMs <= 0 {
		resumeMs = a.baseOffsetMs
	}
	m.mu.Unlock()

	probe, cropRect, err := m.probeForStart(req)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startPlaneLocked(req, resumeMs, probe, cropRect); err != nil {
		return err
	}
	return m.fsm.Transition(EvPlay)
}
```

Modify `SeekTo`:

```go
func (m *Manager) SeekTo(offsetMs int) error {
	m.mu.Lock()
	a := m.active
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("no session")
	}
	if !a.req.Capabilities.CanSeek {
		m.mu.Unlock()
		return fmt.Errorf("adapter does not support seek")
	}
	req := a.req
	m.mu.Unlock()

	probe, cropRect, err := m.probeForStart(req)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startPlaneLocked(req, offsetMs, probe, cropRect); err != nil {
		return err
	}
	return m.fsm.Transition(EvSeek)
}
```

- [ ] **Step 4: Add a regression test** in `internal/core/manager_test.go` that demonstrates Stop returns quickly during a slow Probe. If Probe's current contract doesn't allow easy stubbing, skip the test or use a filter/probe injection mechanism. A pragmatic alternative: test that Probe context timeout bubbles up as an error on a non-responsive URL.

Append:

```go
// TestProbeTimeout_DoesNotDeadlockManager exercises I8: a slow/unreachable
// StreamURL must not hold Manager.mu. We fire StartSession against a URL
// that never responds; concurrently call Stop; assert Stop returns quickly
// regardless of whether Probe is still in flight.
func TestProbeTimeout_DoesNotDeadlockManager(t *testing.T) {
	// A TCP listener that accepts but never writes: ffprobe will hang
	// waiting for response headers, hitting our 10 s timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Park the connection; never reply.
			_ = c
		}
	}()
	url := "http://" + ln.Addr().String() + "/never.mp4"

	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	cfg := &config.Config{
		Modeline:            "NTSC_480i",
		InterlaceFieldOrder: "tff",
		AspectMode:          "letterbox",
		RGBMode:             "rgb888",
		AudioSampleRate:     48000,
		AudioChannels:       2,
	}
	m := NewManager(cfg, sender)

	startErr := make(chan error, 1)
	go func() {
		startErr <- m.StartSession(SessionRequest{
			StreamURL:  url,
			DirectPlay: true,
		})
	}()

	// Stop must not block even though Probe is in flight.
	stopDone := make(chan struct{})
	go func() {
		_ = m.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop blocked on in-flight Probe — mutex discipline regressed")
	}

	// StartSession eventually returns (ffprobe either hits timeout or errors).
	select {
	case err := <-startErr:
		if err == nil {
			t.Errorf("StartSession returned nil for unreachable URL; expected an error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("StartSession never returned — probe timeout not enforced")
	}
}
```

Add imports: `"net"`, `"time"`, and whatever's needed from `internal/config`, `internal/groovynet`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/core/... -v`
Expected: all pass, including the new regression test. The test takes ~10 s (the probe timeout), which is acceptable.

- [ ] **Step 6: Full verification**

Run: `go test ./... && go vet ./... && go test -tags=integration ./tests/integration/...`
Expected: all clean.

- [ ] **Step 7: Commit**

```bash
git add internal/core/manager.go internal/core/manager_test.go
git commit -m "refactor(core): Probe/ProbeCrop run outside Manager.mu with bounded context"
```

---

## Task 7 (I6): Fetch subtitle track to temp file before libass reads it

**Context:** `SubtitleURLFor` in `internal/adapters/plex/transcode.go:75-108` returns an HTTP URL with auth token appended. `pipeline.go:100-103` plugs that URL directly into the FFmpeg `subtitles=filename='<URL>':si=<idx>` filter. libass expects a local filesystem path, not a URL. Some FFmpeg builds error; others silently drop the filter.

**Fix approach:** Before spawning the FFmpeg process, fetch the subtitle URL to `<cfg.DataDir>/subtitles/<sessionID>.<ext>` using a timed HTTP client. Extension is inferred from `Content-Type` (`text/x-ssa` → `.ass`, `application/x-subrip` → `.srt`, default `.srt`). `PipelineSpec` gets a new field `SubtitlePath`. `buildFilterChain` uses the path. Plane removes the file on shutdown.

The fetch happens in the Plex adapter (not in core) because only the adapter knows how to authenticate; core sees the local path via `SessionRequest.SubtitlePath`.

**Files:**
- Modify: `internal/core/types.go` (new `SubtitlePath` field on `SessionRequest`).
- Modify: `internal/ffmpeg/pipeline.go` (new `SubtitlePath` field on `PipelineSpec`, filter uses it).
- Modify: `internal/core/manager.go` (pass `req.SubtitlePath` into `spec.SubtitlePath`).
- Modify: `internal/adapters/plex/companion.go` (fetch to temp file before `StartSession`).
- Modify: `internal/adapters/plex/transcode.go` (new helper `FetchSubtitleToFile`).
- Modify: `internal/adapters/plex/transcode_test.go` (regression test).

- [ ] **Step 1: Find the `SubtitleURL` usage in the SessionRequest / PipelineSpec chain** — use `grep -rn 'SubtitleURL' internal/`. Expected hits: `internal/core/types.go` (field), `internal/core/manager.go` (passthrough), `internal/ffmpeg/pipeline.go` (filter), `internal/adapters/plex/companion.go` (setter), `internal/adapters/plex/transcode.go` (helper).

- [ ] **Step 2: Extend `core.SessionRequest`** — add a `SubtitlePath string` field next to `SubtitleURL` (keep both for now; we'll remove `SubtitleURL` once it's fully replaced). Edit `internal/core/types.go`:

```go
	// SubtitlePath is a local filesystem path to a subtitle file (SRT or ASS)
	// that the data plane hands to libass via the ffmpeg `subtitles=filename=`
	// filter. Mutually exclusive with SubtitleURL; adapters prefer SubtitlePath
	// and set SubtitleURL only during migration. Libass cannot fetch URLs, so
	// adapters that source captions from the network MUST download to a file
	// first and pass the path here.
	SubtitlePath string
```

- [ ] **Step 3: Extend `ffmpeg.PipelineSpec`** — in `internal/ffmpeg/pipeline.go`, add `SubtitlePath string` next to `SubtitleURL`:

```go
	SubtitleURL   string // deprecated; libass cannot fetch URLs. Use SubtitlePath.
	SubtitlePath  string // local filesystem path the filter graph passes to libass
	SubtitleIndex int
```

- [ ] **Step 4: Update `buildFilterChain`** in `internal/ffmpeg/pipeline.go` to use `SubtitlePath`. Replace the subtitle block (lines 99–103):

```go
	// 4. Subtitle burn-in BEFORE interlacing. Only filesystem paths work for
	//    libass; URL-sourced captions must be downloaded by the adapter first.
	if s.SubtitlePath != "" {
		filters = append(filters,
			fmt.Sprintf("subtitles=filename='%s':si=%d", s.SubtitlePath, s.SubtitleIndex))
	}
```

- [ ] **Step 5: Update `Manager.startPlaneLocked`** — in `internal/core/manager.go`, change the line that sets `SubtitleURL` in the PipelineSpec to set both (passthrough):

```go
		SubtitleURL:     req.SubtitleURL,
		SubtitlePath:    req.SubtitlePath,
		SubtitleIndex:   req.SubtitleIndex,
```

- [ ] **Step 6: Add `FetchSubtitleToFile` to the Plex adapter.** Append to `internal/adapters/plex/transcode.go`:

```go
// FetchSubtitleToFile downloads the subtitle resource at srtURL (the token-
// bearing URL returned by SubtitleURLFor) to a file under
// <dataDir>/subtitles/<sessionID>.<ext>. The extension is derived from the
// HTTP Content-Type header: `text/x-ssa` → `.ass`, `application/x-subrip`
// and everything else → `.srt` (SubRip is the format PMS defaults to).
//
// Returns the absolute file path. The caller is responsible for removing
// the file when the session ends — see Plane.Stop teardown in core.Manager.
//
// Uses the 10 s-timeout HTTP client so a stuck PMS doesn't wedge session
// start.
func FetchSubtitleToFile(ctx context.Context, srtURL, dataDir, sessionID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srtURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("subtitle fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("subtitle fetch: %s", resp.Status)
	}
	ext := ".srt"
	switch ct := resp.Header.Get("Content-Type"); {
	case strings.HasPrefix(ct, "text/x-ssa"), strings.HasPrefix(ct, "text/x-ass"):
		ext = ".ass"
	}
	dir := filepath.Join(dataDir, "subtitles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, sessionID+ext)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
```

Add imports to `internal/adapters/plex/transcode.go`: `"context"`, `"os"`, `"path/filepath"`. `plexHTTPClient` is introduced in Task 9 (I9) — for now, use `http.DefaultClient.Do(req)` as a placeholder and note it'll be swapped in Task 9. Actually to preserve ordering, add a package-level `plexHTTPClient` *here* with a short timeout (Task 9 will point `linking.go` at the same variable):

At the top of `internal/adapters/plex/transcode.go` (after imports, before types):

```go
// plexHTTPClient is the shared HTTP client for PMS + plex.tv requests.
// 10 s timeout bounds every network call; the bridge must never wait on a
// hung remote under a caller that holds a mutex or drives a ticker.
// Declared as a var so tests can swap in a faster client.
var plexHTTPClient = &http.Client{Timeout: 10 * time.Second}
```

Add `"time"` import.

- [ ] **Step 7: Wire the fetch into the Companion `handlePlayMedia` path** — find where `SubtitleURL` is set on the `SessionRequest` in `internal/adapters/plex/companion.go` (`grep -n 'SubtitleURL' internal/adapters/plex/companion.go`). Replace the assignment so instead of passing the URL, the handler downloads and passes the local path.

A representative edit (exact structure depends on what's already there):

```go
	// Resolve subtitle: if the controller asked for a stream and PMS has
	// one, download to a temp file so libass can read it. On any error
	// (PMS miss, network hiccup, transient 5xx), fall back to no burn-in
	// rather than failing the whole cast — callers can retry by issuing
	// playMedia again.
	var subtitlePath string
	var subtitleIndex int
	if streamID := r.URL.Query().Get("subtitleStreamID"); streamID != "" {
		subURL, err := SubtitleURLFor(a.cfg.PlexServerURL, mediaKey, streamID, a.store.AuthToken)
		if err != nil {
			slog.Warn("subtitle lookup", "err", err, "streamID", streamID)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			subtitlePath, err = FetchSubtitleToFile(ctx, subURL, a.cfg.DataDir, sessionID)
			cancel()
			if err != nil {
				slog.Warn("subtitle download", "err", err)
				subtitlePath = ""
			} else {
				subtitleIndex = 0 // libass stream index for single-stream files
			}
		}
	}

	req := core.SessionRequest{
		StreamURL:     transcodeURL,
		AdapterRef:    commandID,
		DirectPlay:    false,
		SubtitlePath:  subtitlePath,
		SubtitleIndex: subtitleIndex,
		SeekOffsetMs:  offsetMs,
		Capabilities:  core.Capabilities{CanSeek: true, CanPause: true},
	}
```

(**Guidance for the implementer:** the existing `handlePlayMedia` flow already constructs a `core.SessionRequest`. Insert the subtitle-download block immediately before that construction, using the existing `sessionID` and `mediaKey` variable names. If any variable names differ, adapt to local names — keep the semantics.)

- [ ] **Step 8: Add a regression test** in `internal/adapters/plex/transcode_test.go`:

```go
func TestFetchSubtitleToFile_WritesLocalPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-subrip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("1\n00:00:00,000 --> 00:00:02,000\nhello\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path, err := FetchSubtitleToFile(ctx, srv.URL+"/sub.srt", dir, "session-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "session-xyz.srt" {
		t.Errorf("unexpected filename: %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("hello")) {
		t.Errorf("subtitle body not written: %q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file perms = %o, want 0600", info.Mode().Perm())
	}
}
```

Add imports: `"bytes"`, `"context"`, `"net/http"`, `"net/http/httptest"`, `"os"`, `"path/filepath"`, `"testing"`, `"time"`.

- [ ] **Step 9: Run tests**

Run: `go test ./internal/adapters/plex/... -v`
Expected: PASS.

Run: `go test ./... && go vet ./...`
Expected: all clean.

- [ ] **Step 10: Cleanup (deferred).** Removing `SubtitleURL` from `PipelineSpec` and `SessionRequest` is intentionally left for a polish pass — changing both in one commit while keeping old callers working adds risk. This commit leaves `SubtitleURL` in place but unused by the new path.

- [ ] **Step 11: Commit**

```bash
git add internal/core/types.go internal/ffmpeg/pipeline.go internal/core/manager.go \
        internal/adapters/plex/transcode.go internal/adapters/plex/transcode_test.go \
        internal/adapters/plex/companion.go
git commit -m "fix(ffmpeg): fetch subtitle track to temp file before libass reads it"
```

---

## Task 8 (I7): rgb_mode validates to rgb888 only (v1 scope)

**Context:** `config.Validate` in `internal/config/config.go:86-90` accepts `rgb_mode ∈ {rgb888, rgba8888, rgb565}`. `manager.go::resolveRGBMode` + `bytesPerPixel` both have full support. But `pipeline.go:160` hardcodes `-pix_fmt rgb24`, so non-rgb888 configs produce a torn stream (FFmpeg emits 3 bytes/pixel into a buffer sized for 4 or 2 bytes/pixel).

**Fix approach:** Narrow config validation to reject non-rgb888 with a clear error message. `resolveRGBMode` and `bytesPerPixel` stay untouched — they'll be ready when a future v2+ wires `-pix_fmt` through. This is a pure YAGNI move: v1 scope.

**Files:**
- Modify: `internal/config/config.go` (narrow validation).
- Modify: `internal/config/config_test.go` (assertion).
- Modify: `config.example.toml` (comment).

- [ ] **Step 1: Narrow the validation** in `internal/config/config.go`. Replace the `switch c.RGBMode` block (lines 86–90):

```go
	// v1 scope: only rgb888 is wired through the FFmpeg pipeline. The Groovy
	// protocol supports rgba8888 and rgb565 and the constants exist in
	// internal/groovy and internal/core for future use, but the FFmpeg
	// command in internal/ffmpeg/pipeline.go hardcodes -pix_fmt rgb24.
	// Selecting a non-rgb888 mode before those wires are complete produces
	// a torn raster. Revisit when v2+ extends the pipeline.
	if c.RGBMode != "rgb888" {
		return fmt.Errorf("rgb_mode: only rgb888 is supported in v1 (got %q; rgba8888/rgb565 reserved for future work)", c.RGBMode)
	}
```

- [ ] **Step 2: Add a regression test** in `internal/config/config_test.go`:

```go
func TestValidate_RejectsNonRGB888(t *testing.T) {
	for _, mode := range []string{"rgba8888", "rgb565", "rgb16"} {
		c := defaults()
		c.RGBMode = mode
		err := c.Validate()
		if err == nil {
			t.Errorf("rgb_mode=%q: expected validation error, got nil", mode)
			continue
		}
		if !strings.Contains(err.Error(), "rgb888") {
			t.Errorf("rgb_mode=%q: error %q should mention 'rgb888'", mode, err)
		}
	}
}

func TestValidate_AcceptsRGB888(t *testing.T) {
	c := defaults()
	c.RGBMode = "rgb888"
	if err := c.Validate(); err != nil {
		t.Errorf("rgb_mode=rgb888: expected OK, got %v", err)
	}
}
```

Add `"strings"` to imports if missing.

- [ ] **Step 3: Update `config.example.toml`** — replace the `rgb_mode` comment (line 18) with:

```toml
rgb_mode               = "rgb888"     # v1: rgb888 only (rgba8888 / rgb565 reserved for v2+)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/... -v`
Expected: PASS.

Run: `go test ./... && go vet ./... && go test -tags=integration ./tests/integration/...`
Expected: all clean. The integration harness already uses `rgb888`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go config.example.toml
git commit -m "fix(config): rgb_mode validates to rgb888 only (v1 scope)"
```

---

## Task 9 (I9): plex.tv HTTP client has 10s timeout; RegisterDevice checks status

**Context:** `RequestPIN`, `PollPIN`, `RegisterDevice` in `internal/adapters/plex/linking.go` use `http.DefaultClient.Do` with no timeout. `RegisterDevice` runs on a 60 s ticker (`RunRegistrationLoop`); a stuck plex.tv call blocks every subsequent tick. `RegisterDevice` also drops `resp.StatusCode`, so a 401 (expired token) is silent.

**Fix approach:** Reuse the `plexHTTPClient` package-level variable introduced in Task 7 (10 s timeout). Replace the three `http.DefaultClient.Do` call sites. Add a status-code check to `RegisterDevice`.

**Files:**
- Modify: `internal/adapters/plex/linking.go` (3 call sites + 1 status check).
- Modify: `internal/adapters/plex/linking_test.go` (timeout + status regression tests).

- [ ] **Step 1: Swap the three call sites** — in `internal/adapters/plex/linking.go`, replace each `http.DefaultClient.Do(req)` with `plexHTTPClient.Do(req)`:

In `RequestPIN` (line 58):
```go
	resp, err := plexHTTPClient.Do(req)
```

In `PollPIN` (line 87):
```go
	resp, err := plexHTTPClient.Do(req)
```

In `RegisterDevice` (line 117), replace the tail of the function:
```go
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("plex.tv register: %s", resp.Status)
	}
	return nil
```

(Note: the previous code called `resp.Body.Close()` without defer and with a bare `return nil`. The new version uses defer and adds the status check.)

- [ ] **Step 2: Add regression tests** in `internal/adapters/plex/linking_test.go`. If the file doesn't exist, create it — it should, from the v1 implementation; check with `ls internal/adapters/plex/linking_test.go`. Append:

```go
// TestRegisterDevice_Returns4xxAsError verifies I9: a plex.tv 401 (expired
// token) surfaces as an error so the caller / ticker loop can log it,
// instead of being silently dropped.
func TestRegisterDevice_Returns4xxAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	oldBase := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = oldBase })

	err := RegisterDevice("uuid-x", "stale-token", "10.0.0.1", 32500)
	if err == nil {
		t.Fatal("expected error from 401 response; got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

// TestPlexHTTPClient_HasTimeout verifies the shared client is configured
// with a bounded timeout so a hanging plex.tv call cannot wedge a ticker
// or caller.
func TestPlexHTTPClient_HasTimeout(t *testing.T) {
	if plexHTTPClient.Timeout <= 0 {
		t.Errorf("plexHTTPClient.Timeout = %v; must be > 0", plexHTTPClient.Timeout)
	}
	if plexHTTPClient.Timeout > 30*time.Second {
		t.Errorf("plexHTTPClient.Timeout = %v; too generous", plexHTTPClient.Timeout)
	}
}
```

Add imports as needed: `"net/http"`, `"net/http/httptest"`, `"strings"`, `"testing"`, `"time"`.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/adapters/plex/... -v`
Expected: PASS.

- [ ] **Step 4: Full verification**

Run: `go test ./... && go vet ./...`
Expected: all clean.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/plex/linking.go internal/adapters/plex/linking_test.go
git commit -m "fix(plex): plex.tv HTTP client has 10s timeout; RegisterDevice checks status"
```

---

## Task 10 (I10): SubtitleURLFor HTTP call has 10s timeout

**Context:** `SubtitleURLFor` in `internal/adapters/plex/transcode.go:80` uses `http.Get` with no timeout. A stuck PMS metadata response blocks the `handlePlayMedia` flow.

**Fix approach:** Replace `http.Get` with a `NewRequestWithContext`-based call using the shared `plexHTTPClient`. Caller-provided `ctx` bounds the request; if the caller passes `context.Background()`, the 10 s client timeout still fires.

**Files:**
- Modify: `internal/adapters/plex/transcode.go` (signature change + body).
- Modify: `internal/adapters/plex/transcode_test.go` (hanging-server regression test).
- Modify: `internal/adapters/plex/companion.go` (caller passes a context).

- [ ] **Step 1: Change `SubtitleURLFor`'s signature to accept a context** — in `internal/adapters/plex/transcode.go`:

```go
// SubtitleURLFor queries PMS metadata for mediaKey and returns a URL to the
// subtitle stream whose id matches streamID, token-appended so FetchSubtitleToFile
// can download it. ctx bounds the metadata fetch; callers should pass a
// context with a bounded deadline (10 s is idiomatic for PMS calls).
func SubtitleURLFor(ctx context.Context, serverURL, mediaKey, streamID, token string) (string, error) {
	u := fmt.Sprintf("%s%s?X-Plex-Token=%s",
		strings.TrimRight(serverURL, "/"),
		mediaKey,
		url.QueryEscape(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("metadata fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("metadata fetch: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var mc pmsMediaContainer
	if err := xml.Unmarshal(body, &mc); err != nil {
		return "", fmt.Errorf("parse metadata: %w", err)
	}
	for _, v := range mc.Video {
		for _, media := range v.Media {
			for _, part := range media.Part {
				for _, s := range part.Stream {
					if s.ID == streamID && s.Key != "" {
						return fmt.Sprintf("%s%s?X-Plex-Token=%s",
							strings.TrimRight(serverURL, "/"),
							s.Key,
							url.QueryEscape(token)), nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("subtitle stream %q not found under %s", streamID, mediaKey)
}
```

Add `"context"` to imports.

- [ ] **Step 2: Update the caller** in `internal/adapters/plex/companion.go`. Find the `SubtitleURLFor(...)` call (from Task 7 this is inside `handlePlayMedia`) and add a bounded context:

```go
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		subURL, err := SubtitleURLFor(ctx, a.cfg.PlexServerURL, mediaKey, streamID, a.store.AuthToken)
		cancel()
```

- [ ] **Step 3: Add a regression test** in `internal/adapters/plex/transcode_test.go`:

```go
// TestSubtitleURLFor_ContextCancelled verifies a slow PMS metadata server
// does not block SubtitleURLFor beyond the caller's context deadline.
// Regression harness for I10.
func TestSubtitleURLFor_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block forever
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := SubtitleURLFor(ctx, srv.URL, "/library/metadata/42", "3", "token")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from cancelled context; got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("context cancel did not short-circuit request: took %v", elapsed)
	}
}
```

Add any missing imports: `"context"`, `"net/http"`, `"net/http/httptest"`, `"testing"`, `"time"`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/adapters/plex/... -v`
Expected: PASS.

Run: `go test ./... && go vet ./...`
Expected: all clean.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/plex/transcode.go internal/adapters/plex/transcode_test.go \
        internal/adapters/plex/companion.go
git commit -m "fix(plex): SubtitleURLFor HTTP call has 10s timeout"
```

---

## Task 11 (I11): host_ip config key + README multi-NIC + cgroup docs

**Context:** The v1 plan self-review explicitly said: "on Unraid the recommendation is to make `host_ip` a required config key and bypass auto-detection." Not implemented. `outboundIP()` in `cmd/mister-groovy-relay/main.go` routes to 8.8.8.8 and picks the default-route interface — on multi-NIC Unraid this is frequently the wrong interface (Plex-controller-facing NIC ≠ default route). README also does not mention the Docker cgroup / parity-check CPU-contention risk the plan flagged.

**Fix approach:**
1. New optional string field `HostIP` in `internal/config/config.go`.
2. `cmd/mister-groovy-relay/main.go`: if `cfg.HostIP != ""` use it; else fall back to `outboundIP()` with a warning log.
3. `config.example.toml`: commented-out `host_ip` with a pointer to the README section.
4. README: two new subsections — "Multi-NIC Unraid hosts" and "CPU contention under Docker".

**Files:**
- Modify: `internal/config/config.go` (new field).
- Modify: `internal/config/config_test.go` (round-trip).
- Modify: `cmd/mister-groovy-relay/main.go` (conditional use).
- Modify: `config.example.toml` (comment).
- Modify: `README.md` (two new subsections).

- [ ] **Step 1: Add the config field** in `internal/config/config.go`. Insert into the `Config` struct after `HTTPPort`:

```go
	// HostIP is the LAN IP address the bridge advertises in /resources and
	// plex.tv RegisterDevice. If empty, the bridge falls back to a route-based
	// auto-detection which routes a UDP packet to 8.8.8.8 and reads the
	// local address. On multi-NIC hosts (Unraid with both LAN and WireGuard
	// interfaces is the common case), the auto-detected IP may be the WG
	// interface, not the LAN — and the Plex controller cannot reach the
	// WG-only address. Set host_ip explicitly when the default route is not
	// the Plex-facing NIC. See README "Multi-NIC Unraid hosts".
	HostIP string `toml:"host_ip"`
```

No change to `defaults()` — `HostIP` defaults to the empty string.

- [ ] **Step 2: Add a round-trip test** in `internal/config/config_test.go`:

```go
func TestLoad_HostIPRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := []byte(`mister_host = "192.168.1.50"` + "\n" + `host_ip = "192.168.1.20"` + "\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostIP != "192.168.1.20" {
		t.Errorf("host_ip = %q, want %q", cfg.HostIP, "192.168.1.20")
	}
}

func TestLoad_HostIPDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`mister_host = "192.168.1.50"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostIP != "" {
		t.Errorf("host_ip default = %q, want empty", cfg.HostIP)
	}
}
```

- [ ] **Step 3: Use the config value in main.go.** In `cmd/mister-groovy-relay/main.go`, replace the single line in the `plex.AdapterConfig{ ... HostIP: outboundIP() ... }` struct literal (around line 83) with a computed value, and log a warning when auto-detection was used:

Find:
```go
	plexAdapter, err := plex.NewAdapter(plex.AdapterConfig{
		Cfg:        cfg,
		Core:       coreMgr,
		TokenStore: store,
		HostIP:     outboundIP(),
		Version:    version,
	})
```

Replace with:
```go
	hostIP := cfg.HostIP
	if hostIP == "" {
		hostIP = outboundIP()
		slog.Warn("host_ip not set; auto-detected via default route — override in config for multi-NIC hosts",
			"detected", hostIP)
	}
	plexAdapter, err := plex.NewAdapter(plex.AdapterConfig{
		Cfg:        cfg,
		Core:       coreMgr,
		TokenStore: store,
		HostIP:     hostIP,
		Version:    version,
	})
```

- [ ] **Step 4: Update `config.example.toml`.** Under the `# --- Network ---` block, below `http_port`, add:

```toml
# host_ip = "192.168.1.20"  # LAN IP this bridge advertises to Plex (/resources, plex.tv register).
                             # Leave commented to auto-detect; set explicitly on multi-NIC Unraid
                             # hosts where the Plex-facing NIC is not the default route.
                             # See README "Multi-NIC Unraid hosts".
```

- [ ] **Step 5: Add two README subsections.** Open `README.md`, find the "Troubleshooting" section (or equivalent — grep for `## Troubleshooting`). Add these subsections just before or inside Troubleshooting:

```markdown
### Multi-NIC Unraid hosts

The bridge advertises its own LAN address to Plex (in the `/resources` response
and in the plex.tv device registration PUT). By default it auto-detects that
address by asking the kernel which interface it would use to reach 8.8.8.8 — a
trick that works when the default route points at the LAN.

On Unraid hosts with multiple network interfaces — typical combinations are
LAN + WireGuard, LAN + Docker bridge, or LAN + secondary subnet — the default
route may not be the Plex-facing one. Symptoms: the cast target shows up in
the Plex picker but "commands never arrive" — the controller is trying to
reach the bridge on an unreachable NIC.

Fix: set `host_ip` explicitly to the LAN IP the Plex controller can reach.
Find it with `ip -4 addr show | grep inet` on the host; the `br0` or `eth0`
interface IP on the same subnet as your Plex Media Server is what you want.

```toml
host_ip = "192.168.1.20"
```

Restart the bridge. Check the startup log for the `host_ip not set` warning —
if it's gone, your override took effect.

### CPU contention under Docker

The data plane pushes fields at 59.94 Hz regardless of scheduling pressure.
Under heavy CPU contention (Unraid parity check, mover, a co-tenant container
spiking CPU) the FFmpeg decoder can fall behind; the bridge covers with
duplicate-field BLITs, which the FPGA rescans — so the symptom is visible
motion glitches, not A/V drift. (This is by design — the clock-push architecture
trades a graceful fallback against a hard drift bug.)

If you see glitches during parity checks, cap container CPU with
`docker run --cpus=2 ...` or the Unraid template's CPU-pinning option so the
bridge has dedicated cores that aren't preempted. 2 cores is typically
sufficient for a single 480p transcode plus Groovy packet framing.
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/config/... -v`
Expected: PASS, including the two new tests.

Run: `go build ./cmd/mister-groovy-relay/...`
Expected: clean build.

Run: `go test ./... && go vet ./...`
Expected: all clean.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go \
        cmd/mister-groovy-relay/main.go config.example.toml README.md
git commit -m "feat(config): host_ip config key + README multi-NIC + cgroup docs"
```

---

## Task 12: Final whole-repo verification

After all 11 commits are in, perform a single final verification pass before considering the remediation batch complete.

- [ ] **Step 1: Race-detector run.** This catches any data-race regression from the I4 / I5 accumulators and the I8 mutex reshape:

```bash
go test -race ./...
```

Expected: all pass, no race output.

- [ ] **Step 2: Full integration suite on Linux** (skip if the host is Windows — rely on CI):

```bash
go test -tags=integration ./tests/integration/...
```

Expected: all scenarios PASS. `TestScenario_Cast` now reports `AudioBytes >= 200_000` (C2 + C1 + I5 collectively verified).

- [ ] **Step 3: `go vet` and `go build` both binaries**

```bash
go vet ./...
go build -o /tmp/mgr ./cmd/mister-groovy-relay
go build -o /tmp/fake ./cmd/fake-mister
```

Expected: all clean.

- [ ] **Step 4: Spot-check commit mapping.** Open `git log --oneline | head -11` and confirm the 11 commit subjects match the fix IDs one-to-one:

```
feat(config): host_ip config key + README multi-NIC + cgroup docs     # I11
fix(plex): SubtitleURLFor HTTP call has 10s timeout                   # I10
fix(plex): plex.tv HTTP client has 10s timeout; RegisterDevice ...    # I9
fix(config): rgb_mode validates to rgb888 only (v1 scope)             # I7
fix(ffmpeg): fetch subtitle track to temp file before libass ...      # I6
refactor(core): Probe/ProbeCrop run outside Manager.mu ...            # I8
fix(dataplane): audio chunk size from integer-exact fraction          # I5
fix(dataplane): Position uses integer-exact field-count accumulator   # I4
fix(groovy): LZ4 returns ok=false on incompressible input; ...        # C3
feat(ffmpeg): filter chain normalizes any source rate to 59.94 ...    # C1
test(integration): scenario harness records AUDIO payload bytes       # C2
```

No extra commits (scope stayed tight); no missing items (11 Critical+Important all landed).

- [ ] **Step 5: Manual README render check.** Open `README.md` in a viewer (or `gh`'s preview) and confirm the two new subsections render cleanly (no broken code blocks, no markdown errors).

- [ ] **Step 6: Declare the remediation batch complete.** The bridge is ready for Task 14.2 scheduling (real MiSTer + real CRT manual end-to-end). The pcap porch verification and field-order TFF/BFF empirical check remain on Task 14.2's plate per the spec §2 out-of-scope list — not this batch.

---

## Notes for the Implementer

- **TDD tasks (C1, C3, I4, I5)** expect the failing-test step to actually fail before the implementation step. If the test passes on Step 2, something is wrong with the test — not with the rest of the plan.
- **Regression tests** in the non-TDD tasks (C2, I6, I7, I8, I9, I10, I11) can live in the same commit as the fix. Do not split them into separate commits.
- **If a task's file already exists and differs slightly from what the plan expects** (e.g. a helper function moved, a variable renamed), prefer adapting to local names over restructuring. The plan's goal is semantic correctness of each fix, not byte-for-byte match.
- **If any step reveals a bug the plan didn't anticipate**, note it and finish the current task; raise the new finding in a follow-up discussion. Do not let a newly-discovered issue expand the scope of this batch (per spec §2 out-of-scope: the 6 Minor items are deferred regardless of how easy they look while editing).
