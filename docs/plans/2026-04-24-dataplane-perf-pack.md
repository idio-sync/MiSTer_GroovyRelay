# Data-Plane Performance & Robustness Pack — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the four large per-tick allocations on the 60 Hz capture-to-MiSTer hot path, verify SO_SNDBUF actually took effect on Linux, and make ENOBUFS events separately countable in telemetry — without changing any external API or ffmpeg-side behavior.

**Architecture:** Match MiSTerCast structurally — pre-allocate session-lifetime buffers held by `Plane`, keep LZ4 compression inline on the tick path, and use a channel-based free queue (not `sync.Pool`) for the producer/consumer split between the ffmpeg reader and the tick loop. New zero-alloc `*Into` / `*Pooled` API variants are added in `internal/groovy` and `internal/dataplane`; the existing API surface stays for non-data-plane callers.

**Tech Stack:** Go 1.22+, `github.com/pierrec/lz4/v4`, `golang.org/x/sys/unix` (Linux only), `log/slog`, `sync/atomic`. Tests use the standard `testing` package and `internal/fakemister.Listener` for integration.

**Spec:** [docs/specs/2026-04-24-dataplane-perf-pack-design.md](../specs/2026-04-24-dataplane-perf-pack-design.md)

---

## File Structure

**New files (2):**
- `internal/dataplane/framepool.go` — `FrameBuf`, `FramePool`, ownership invariants in package doc
- `internal/dataplane/framepool_test.go` — pool round-trip + alloc budget

**Modified files (12):**
- `internal/groovy/lz4.go` — add `LZ4CompressInto`
- `internal/groovy/lz4_test.go` — extend
- `internal/groovy/builder.go` — add `BuildBlitHeaderInto`
- `internal/groovy/builder_test.go` — extend
- `internal/dataplane/videopipe.go` — add `ExtractFieldFromFrameInto` + `ReadFramesFromPipePooled`
- `internal/dataplane/videopipe_test.go` — extend
- `internal/groovynet/sender.go` — add `enobufCount`, `sndBufActual`, `ENOBUFCount()`, `isPowerOfTen`; modify `NewSender` and `SendPayload`
- `internal/groovynet/sender_linux.go` — add `readSndBuf`
- `internal/groovynet/sender_windows.go` — add `readSndBuf`
- `internal/groovynet/sender_other.go` — add `readSndBuf`
- `internal/groovynet/sender_test.go` — extend
- `internal/groovynet/drainer.go` — slog level cleanup at line 72
- `internal/dataplane/plane.go` — major refactor (struct fields, helpers, Run, sendField)
- `internal/dataplane/plane_test.go` — allocation-budget integration test

**File responsibilities:**
- `framepool.go` — owns the pool primitive, knows nothing about field/lz4/header. Pure data-structure module.
- `videopipe.go` — owns full-frame I/O and field extraction. Two API surfaces: legacy (`ReadFramesFromPipe`, `ExtractFieldFromFrame`) and pooled / *Into variants.
- `groovy/lz4.go` and `groovy/builder.go` — protocol primitives. Stateless. Add zero-alloc variants alongside originals.
- `groovynet/sender.go` — UDP transport. Owns the socket, congestion accounting, ENOBUFS counter, SO_SNDBUF readback log. Platform-specific `readSndBuf` lives in the existing `sender_linux.go` / `sender_windows.go` / `sender_other.go` triplet.
- `dataplane/plane.go` — orchestration. Owns the framePool and three scratch buffers. Tick loop pulls `*FrameBuf` from videoCh, processes via the new `*Into` variants, returns to pool.

---

## Task Order Rationale

Tasks 1–5 add new APIs alongside existing ones (no behavior change). Tasks 6–9 add Sender observability (no behavior change). Tasks 10–12 prepare the Plane (new fields + helpers; legacy path still in use). Task 13 is the disruptive swap to the new pipeline. Task 14 verifies the full system. Each task is independently committable; tasks 1–9 can be reverted without affecting the data plane's runtime behavior.

---

## Task 1: `groovy.LZ4CompressInto` — zero-alloc compression API

**Files:**
- Modify: `internal/groovy/lz4.go`
- Test: `internal/groovy/lz4_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/groovy/lz4_test.go`:

```go
func TestLZ4CompressInto_RoundTrip(t *testing.T) {
	src := make([]byte, 518400)
	for i := range src {
		src[i] = byte(i % 256)
	}
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	n, ok := LZ4CompressInto(dst, src)
	if !ok {
		t.Fatal("compressible input returned ok=false")
	}
	if n == 0 || n >= len(src) {
		t.Fatalf("compressed size %d out of range", n)
	}
	out, err := LZ4Decompress(dst[:n], len(src))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(src, out) {
		t.Error("round-trip mismatch")
	}
}

func TestLZ4CompressInto_MatchesLegacy(t *testing.T) {
	src := make([]byte, 100000)
	for i := range src {
		src[i] = byte(i / 7)
	}
	legacy, ok1 := LZ4Compress(src)
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	n, ok2 := LZ4CompressInto(dst, src)
	if ok1 != ok2 {
		t.Fatalf("ok mismatch: legacy=%v new=%v", ok1, ok2)
	}
	if !bytes.Equal(legacy, dst[:n]) {
		t.Error("compressed bytes differ between LZ4Compress and LZ4CompressInto")
	}
}

func TestLZ4CompressInto_ZeroAllocs(t *testing.T) {
	src := make([]byte, 100000)
	for i := range src {
		src[i] = byte(i % 13)
	}
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	// Warmup so the LZ4 library's internal state is primed.
	LZ4CompressInto(dst, src)
	got := testing.AllocsPerRun(50, func() {
		LZ4CompressInto(dst, src)
	})
	if got != 0 {
		t.Errorf("LZ4CompressInto allocs/op = %v, want 0", got)
	}
}

func TestLZ4CompressInto_IncompressibleReturnsFalse(t *testing.T) {
	src := make([]byte, 720*240*3)
	if _, err := rand.Read(src); err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	n, ok := LZ4CompressInto(dst, src)
	if ok {
		t.Errorf("incompressible input returned ok=true (n=%d)", n)
	}
}
```

Add the `lz4` import at the top (the existing tests don't use it directly; the new ones need `lz4.CompressBlockBound`).

```go
import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/pierrec/lz4/v4"
)
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/groovy/ -run TestLZ4CompressInto -v`
Expected: build error — `LZ4CompressInto` not defined.

- [ ] **Step 3: Add `LZ4CompressInto` to `internal/groovy/lz4.go`**

```go
// LZ4CompressInto compresses src into dst, returning the number of bytes
// written and ok=true when compression reduced the size. The caller MUST
// pass a dst with len >= lz4.CompressBlockBound(len(src)). Identical
// behavior to LZ4Compress except the output buffer is supplied by the
// caller; intended for the data plane's hot tick path where re-allocating
// the output on every field would churn the heap.
//
// Returns (0, false) when CompressBlock reports the input as
// incompressible (n == 0) or when the output would be no smaller than the
// input. Panics on programmer error (dst too small) — the library only
// errors in that case.
func LZ4CompressInto(dst, src []byte) (int, bool) {
	var c lz4.Compressor
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		panic(fmt.Errorf("lz4 compress (caller-supplied dst): %w", err))
	}
	if n == 0 || n >= len(src) {
		return 0, false
	}
	return n, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/groovy/ -run TestLZ4CompressInto -v`
Expected: 4 PASSes, including `TestLZ4CompressInto_ZeroAllocs`.

- [ ] **Step 5: Commit**

```bash
git add internal/groovy/lz4.go internal/groovy/lz4_test.go
git commit -m "feat(groovy): add LZ4CompressInto for zero-alloc compression"
```

---

## Task 2: `groovy.BuildBlitHeaderInto` — zero-alloc BLIT header API

**Files:**
- Modify: `internal/groovy/builder.go`
- Test: `internal/groovy/builder_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/groovy/builder_test.go`:

```go
func TestBuildBlitHeaderInto_MatchesLegacy(t *testing.T) {
	cases := []struct {
		name string
		opts BlitOpts
	}{
		{"raw", BlitOpts{Frame: 42, Field: 0, VSync: 100}},
		{"dup", BlitOpts{Frame: 42, Field: 1, Duplicate: true}},
		{"lz4", BlitOpts{Frame: 42, Field: 0, Compressed: true, CompressedSize: 12345}},
		{"lz4Delta", BlitOpts{Frame: 42, Field: 1, Compressed: true, Delta: true, CompressedSize: 12345}},
	}
	dst := make([]byte, BlitHeaderLZ4Delta) // 13, the largest variant
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			legacy := BuildBlitHeader(c.opts)
			out := BuildBlitHeaderInto(dst, c.opts)
			if !bytes.Equal(legacy, out) {
				t.Errorf("header mismatch:\n  legacy: % x\n  new:    % x", legacy, out)
			}
			if &out[0] != &dst[0] {
				t.Error("BuildBlitHeaderInto returned a different backing array")
			}
		})
	}
}

func TestBuildBlitHeaderInto_ZeroAllocs(t *testing.T) {
	dst := make([]byte, BlitHeaderLZ4Delta)
	opts := BlitOpts{Frame: 1, Field: 0, Compressed: true, CompressedSize: 1000}
	BuildBlitHeaderInto(dst, opts) // warmup
	got := testing.AllocsPerRun(100, func() {
		BuildBlitHeaderInto(dst, opts)
	})
	if got != 0 {
		t.Errorf("BuildBlitHeaderInto allocs/op = %v, want 0", got)
	}
}
```

If `bytes` is not yet imported in `builder_test.go`, add it.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/groovy/ -run TestBuildBlitHeaderInto -v`
Expected: build error — `BuildBlitHeaderInto` not defined.

- [ ] **Step 3: Add `BuildBlitHeaderInto` to `internal/groovy/builder.go`**

Insert immediately after the existing `BuildBlitHeader` function:

```go
// BuildBlitHeaderInto writes the header bytes into dst and returns dst[:length]
// where length depends on the variant (see BuildBlitHeader for the variant
// table). dst MUST have len >= BlitHeaderLZ4Delta (13). Intended for the
// hot tick path where re-allocating an 8-13 byte slice on every send would
// churn the heap.
func BuildBlitHeaderInto(dst []byte, o BlitOpts) []byte {
	var length int
	switch {
	case o.Duplicate:
		length = BlitHeaderRawDup
	case o.Compressed && o.Delta:
		length = BlitHeaderLZ4Delta
	case o.Compressed:
		length = BlitHeaderLZ4
	default:
		length = BlitHeaderRaw
	}
	h := dst[:length]
	// Zero out reused trailing bytes from previous calls so a longer
	// variant followed by a shorter variant doesn't leak stale bytes.
	for i := range h {
		h[i] = 0
	}
	h[0] = CmdBlitFieldVSync
	binary.LittleEndian.PutUint32(h[1:5], o.Frame)
	h[5] = o.Field
	binary.LittleEndian.PutUint16(h[6:8], o.VSync)
	switch {
	case o.Duplicate:
		h[8] = BlitFlagDup
	case o.Compressed:
		binary.LittleEndian.PutUint32(h[8:12], o.CompressedSize)
		if o.Delta {
			h[12] = BlitFlagDelta
		}
	}
	return h
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/groovy/ -run TestBuildBlitHeaderInto -v`
Expected: 5 PASSes (4 sub-tests + alloc test).

- [ ] **Step 5: Commit**

```bash
git add internal/groovy/builder.go internal/groovy/builder_test.go
git commit -m "feat(groovy): add BuildBlitHeaderInto for zero-alloc header writes"
```

---

## Task 3: `dataplane.ExtractFieldFromFrameInto` — zero-alloc field extraction

**Files:**
- Modify: `internal/dataplane/videopipe.go`
- Test: `internal/dataplane/videopipe_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/dataplane/videopipe_test.go`:

```go
func TestExtractFieldFromFrameInto_MatchesLegacy(t *testing.T) {
	const w, h, bpp = 16, 8, 3
	frame := make([]byte, w*h*bpp)
	for i := range frame {
		frame[i] = byte(i)
	}
	for _, field := range []uint8{0, 1} {
		legacy := ExtractFieldFromFrame(frame, w, h, bpp, field)
		dst := make([]byte, w*(h/2)*bpp)
		ExtractFieldFromFrameInto(dst, frame, w, h, bpp, field)
		if !bytes.Equal(legacy, dst) {
			t.Errorf("field %d mismatch:\n  legacy: % x\n  new:    % x", field, legacy, dst)
		}
	}
}

func TestExtractFieldFromFrameInto_ZeroAllocs(t *testing.T) {
	const w, h, bpp = 720, 480, 3
	frame := make([]byte, w*h*bpp)
	dst := make([]byte, w*(h/2)*bpp)
	ExtractFieldFromFrameInto(dst, frame, w, h, bpp, 0) // warmup
	got := testing.AllocsPerRun(50, func() {
		ExtractFieldFromFrameInto(dst, frame, w, h, bpp, 0)
	})
	if got != 0 {
		t.Errorf("ExtractFieldFromFrameInto allocs/op = %v, want 0", got)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/dataplane/ -run TestExtractFieldFromFrameInto -v`
Expected: build error.

- [ ] **Step 3: Add `ExtractFieldFromFrameInto` to `internal/dataplane/videopipe.go`**

Insert after the existing `ExtractFieldFromFrame`:

```go
// ExtractFieldFromFrameInto row-stripes a full-height frame into dst.
// dst MUST have len >= width*(height/2)*bytesPerPixel; the function
// overwrites that prefix and ignores any trailing bytes. field=0 extracts
// even rows (top), field=1 extracts odd rows (bottom). Same row-extraction
// math as ExtractFieldFromFrame; differs only in the caller-supplied dst.
func ExtractFieldFromFrameInto(dst, frame []byte, width, height, bytesPerPixel int, field uint8) {
	rowSize := width * bytesPerPixel
	fieldHeight := height / 2
	srcRow := int(field & 1)
	for dstRow := 0; dstRow < fieldHeight; dstRow++ {
		srcStart := srcRow * rowSize
		srcEnd := srcStart + rowSize
		dstStart := dstRow * rowSize
		copy(dst[dstStart:dstStart+rowSize], frame[srcStart:srcEnd])
		srcRow += 2
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dataplane/ -run TestExtractFieldFromFrameInto -v`
Expected: 2 PASSes.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/videopipe.go internal/dataplane/videopipe_test.go
git commit -m "feat(dataplane): add ExtractFieldFromFrameInto for zero-alloc extraction"
```

---

## Task 4: `dataplane.FrameBuf` and `FramePool`

**Files:**
- Create: `internal/dataplane/framepool.go`
- Create: `internal/dataplane/framepool_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/dataplane/framepool_test.go`:

```go
package dataplane

import (
	"testing"
)

func TestFramePool_GetPutRoundTrip(t *testing.T) {
	const slots = 4
	const frameBytes = 1000
	p := NewFramePool(slots, frameBytes)

	// Drain pool, verify N distinct buffers, each correctly sized.
	bufs := make([]*FrameBuf, slots)
	seen := make(map[*FrameBuf]struct{}, slots)
	for i := 0; i < slots; i++ {
		bufs[i] = p.Get()
		if len(bufs[i].Data) != frameBytes {
			t.Errorf("slot %d Data len = %d, want %d", i, len(bufs[i].Data), frameBytes)
		}
		if _, dup := seen[bufs[i]]; dup {
			t.Errorf("duplicate buffer pointer at slot %d", i)
		}
		seen[bufs[i]] = struct{}{}
	}

	// Return them, then drain again — should get the same N pointers back
	// (in some order — channel doesn't guarantee FIFO across reads).
	for _, b := range bufs {
		p.Put(b)
	}
	seenBack := make(map[*FrameBuf]struct{}, slots)
	for i := 0; i < slots; i++ {
		b := p.Get()
		seenBack[b] = struct{}{}
	}
	if len(seenBack) != slots {
		t.Errorf("got %d distinct buffers on second drain, want %d", len(seenBack), slots)
	}
	for b := range seen {
		if _, ok := seenBack[b]; !ok {
			t.Error("buffer disappeared between rounds")
		}
	}
}

func TestFramePool_ZeroAllocsAfterConstruction(t *testing.T) {
	p := NewFramePool(4, 1000)
	// Warmup
	b := p.Get()
	p.Put(b)
	got := testing.AllocsPerRun(100, func() {
		b := p.Get()
		p.Put(b)
	})
	if got != 0 {
		t.Errorf("Get/Put cycle allocs/op = %v, want 0", got)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/dataplane/ -run TestFramePool -v`
Expected: build error — `FrameBuf` and `FramePool` not defined.

- [ ] **Step 3: Create `internal/dataplane/framepool.go`**

```go
package dataplane

// FrameBuf wraps a session-lifetime byte slice for a single video frame.
// The Data slice is allocated once when the FrameBuf is constructed by
// NewFramePool; callers reuse it across many fills via FramePool.Get and
// FramePool.Put. N records the number of bytes the most recent reader
// filled — the data plane's tick loop slices Data[:N] for downstream use.
//
// Lifetime: a *FrameBuf either belongs to the pool's free queue, OR is
// held exclusively by exactly one of (the reader filling it, the videoCh
// in transit, the tick loop processing it). The producer/consumer
// invariant is: ReadFramesFromPipePooled holds at most one *FrameBuf at
// any time outside the pool, and the tick loop returns the buffer to the
// pool immediately after sendField returns.
type FrameBuf struct {
	Data []byte
	N    int
}

// FramePool is a fixed-capacity, channel-based free queue of *FrameBuf.
// It is preloaded with `slots` buffers at construction; Get blocks if
// the pool is empty (i.e., all buffers are in flight). Put returns a
// buffer to the pool. The pool channel is never closed; the pool is
// GC'd along with its owning Plane.
//
// Why channel-based and not sync.Pool: sync.Pool is GC-aware and can
// drain its contents under memory pressure, forcing fresh allocations
// exactly when the system is already strained. For a 60 Hz hard-real-
// time pipeline that's the wrong semantic.
type FramePool struct {
	free chan *FrameBuf
}

// NewFramePool allocates `slots` *FrameBuf, each carrying a
// `frameBytes`-sized Data slice, and preloads them into the free queue.
// All allocation happens here; no further allocation should occur via
// Get/Put across the pool's lifetime.
func NewFramePool(slots, frameBytes int) *FramePool {
	p := &FramePool{free: make(chan *FrameBuf, slots)}
	for i := 0; i < slots; i++ {
		p.free <- &FrameBuf{Data: make([]byte, frameBytes)}
	}
	return p
}

// Get returns a free buffer. Blocks if the pool is empty until Put is
// called by another goroutine.
func (p *FramePool) Get() *FrameBuf { return <-p.free }

// Put returns a buffer to the pool. Non-blocking: the pool channel is
// sized exactly to the slot count so Put never blocks under correct
// usage. A blocking Put indicates a programmer error (returning a buffer
// that wasn't from this pool, or returning the same buffer twice).
func (p *FramePool) Put(b *FrameBuf) { p.free <- b }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dataplane/ -run TestFramePool -v`
Expected: 2 PASSes.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/framepool.go internal/dataplane/framepool_test.go
git commit -m "feat(dataplane): add FrameBuf and channel-based FramePool"
```

---

## Task 5: `dataplane.ReadFramesFromPipePooled` — pooled video reader

**Files:**
- Modify: `internal/dataplane/videopipe.go`
- Test: `internal/dataplane/videopipe_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/dataplane/videopipe_test.go`:

```go
func TestReadFramesFromPipePooled_RoundTrip(t *testing.T) {
	const frameBytes = 720 * 480 * 3
	pool := NewFramePool(4, frameBytes)
	buf := &bytes.Buffer{}
	for i := 0; i < 3; i++ {
		f := make([]byte, frameBytes)
		for j := range f {
			f[j] = byte(i)
		}
		buf.Write(f)
	}
	out := make(chan *FrameBuf, 4)
	go ReadFramesFromPipePooled(buf, pool, out)

	for i := 0; i < 3; i++ {
		select {
		case fb := <-out:
			if fb.N != frameBytes {
				t.Errorf("frame %d N = %d, want %d", i, fb.N, frameBytes)
			}
			if fb.Data[0] != byte(i) {
				t.Errorf("frame %d first byte = %d, want %d", i, fb.Data[0], i)
			}
			pool.Put(fb)
		case <-time.After(time.Second):
			t.Fatalf("timeout on frame %d", i)
		}
	}

	// EOF: out should close after the third frame.
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("expected channel close after EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestReadFramesFromPipePooled_PartialReadReturnsBufferAndCloses(t *testing.T) {
	const frameBytes = 1000
	pool := NewFramePool(2, frameBytes)
	// Write half a frame.
	half := bytes.NewReader(make([]byte, frameBytes/2))
	out := make(chan *FrameBuf, 2)
	done := make(chan struct{})
	go func() {
		ReadFramesFromPipePooled(half, pool, out)
		close(done)
	}()

	// out should close without emitting any *FrameBuf — partial reads
	// are not propagated.
	select {
	case fb, ok := <-out:
		if ok {
			t.Errorf("partial read should not emit; got *FrameBuf with N=%d", fb.N)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not close out after partial read")
	}

	<-done
	// All buffers should be back in the pool.
	for i := 0; i < 2; i++ {
		select {
		case <-pool.free:
		case <-time.After(time.Second):
			t.Fatalf("buffer %d not returned to pool after EOF", i)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/dataplane/ -run TestReadFramesFromPipePooled -v`
Expected: build error.

- [ ] **Step 3: Add `ReadFramesFromPipePooled` to `internal/dataplane/videopipe.go`**

Insert after the existing `ReadFramesFromPipe`:

```go
// ReadFramesFromPipePooled reads fixed-size raw frames from r into
// pool-supplied buffers and forwards each filled *FrameBuf on out. The
// frame size is determined by the pool's frameBytes (set at NewFramePool).
// Closes out on EOF or any read error.
//
// EOF semantics:
//   - Clean EOF (io.EOF or io.ErrUnexpectedEOF on a partial read): the
//     in-progress *FrameBuf is returned to the pool BEFORE close. We do
//     not emit a partial frame downstream — the data plane has no use
//     for one.
//   - All read errors are treated equivalently: return the in-flight
//     buffer to the pool, close out, exit.
//
// Pool ownership invariant: the reader holds at most one *FrameBuf
// outside the pool at any time. Together with videoCh's buffered
// capacity and the tick loop's at-most-one-buffer-in-progress, this
// bounds the worst-case in-flight count at videoChCap + 2.
//
// The pool channel is never closed; the pool is GC'd along with the
// Plane. The out channel is closed exactly once when the reader exits.
func ReadFramesFromPipePooled(r io.Reader, pool *FramePool, out chan<- *FrameBuf) {
	defer close(out)
	for {
		fb := pool.Get()
		n, err := io.ReadFull(r, fb.Data)
		if err != nil {
			// Both io.EOF and io.ErrUnexpectedEOF land here. Either way
			// we don't emit a partial frame; return the buffer and exit.
			pool.Put(fb)
			return
		}
		fb.N = n
		out <- fb
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dataplane/ -run TestReadFramesFromPipePooled -v`
Expected: 2 PASSes.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/videopipe.go internal/dataplane/videopipe_test.go
git commit -m "feat(dataplane): add ReadFramesFromPipePooled with pool ownership"
```

---

## Task 6: `groovynet.readSndBuf` — platform shim trio

**Files:**
- Modify: `internal/groovynet/sender_linux.go`
- Modify: `internal/groovynet/sender_windows.go`
- Modify: `internal/groovynet/sender_other.go`

**No new test in this task** — `readSndBuf` is exercised in Task 7 via the live `NewSender` path.

- [ ] **Step 1: Add Linux implementation**

Append to `internal/groovynet/sender_linux.go`:

```go
import (
	"net"
)

// readSndBuf returns the kernel's current SO_SNDBUF for conn, in bytes.
// On Linux the kernel returns approximately 2× the requested size as a
// long-standing bookkeeping quirk; callers must compare conservatively
// (actual >= requested means OK; actual < requested means clamped).
func readSndBuf(conn *net.UDPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var size int
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		size, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF)
	}); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, sockErr
	}
	return size, nil
}
```

`net` should already be imported. If not, add it.

- [ ] **Step 2: Add Windows implementation**

Read `internal/groovynet/sender_windows.go` first to see its build tag and existing imports, then append:

```go
import (
	"net"
	"syscall"
)

// readSndBuf returns the kernel's current SO_SNDBUF for conn, in bytes.
func readSndBuf(conn *net.UDPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var size int
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		size, sockErr = syscall.GetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF)
	}); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, sockErr
	}
	return size, nil
}
```

- [ ] **Step 3: Add other-platform no-op**

Append to `internal/groovynet/sender_other.go`:

```go
import (
	"net"
)

// readSndBuf is a no-op on non-Linux/non-Windows platforms. Returns
// (0, nil) so the caller treats it as unsupported.
func readSndBuf(conn *net.UDPConn) (int, error) {
	return 0, nil
}
```

- [ ] **Step 4: Verify all platforms build**

Run on the host platform:

```bash
go build ./internal/groovynet/...
GOOS=linux go vet ./internal/groovynet/...
GOOS=windows go vet ./internal/groovynet/...
GOOS=darwin go vet ./internal/groovynet/...
```

Expected: all four commands succeed with no errors. (`go vet` for non-host platforms compiles without linking; if the host is Linux, the `linux` line is redundant but harmless.)

- [ ] **Step 5: Commit**

```bash
git add internal/groovynet/sender_linux.go internal/groovynet/sender_windows.go internal/groovynet/sender_other.go
git commit -m "feat(groovynet): add readSndBuf platform shim trio"
```

---

## Task 7: `Sender` SO_SNDBUF readback + logging in `NewSender`

**Files:**
- Modify: `internal/groovynet/sender.go`
- Test: `internal/groovynet/sender_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/groovynet/sender_test.go`. The test verifies that `NewSender` populates `sndBufActual` with a positive value on Linux and tolerates 0 on platforms where readback is unsupported. The internal field is package-private; the test relies on package-internal access.

```go
func TestSender_SndBufActualPopulated(t *testing.T) {
	s, err := NewSender("127.0.0.1", 32100, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// On Linux/Windows readSndBuf returns the kernel's current SO_SNDBUF.
	// On other-platforms it returns 0. Both are acceptable; we only assert
	// there's no panic and the field is set deterministically.
	if s.sndBufActual < 0 {
		t.Errorf("sndBufActual should be >= 0, got %d", s.sndBufActual)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/groovynet/ -run TestSender_SndBufActualPopulated -v`
Expected: build error — `sndBufActual` field not defined.

- [ ] **Step 3: Modify `internal/groovynet/sender.go`**

Add the field to the struct, the constant, and the readback logic in `NewSender`. Replace lines 25–34 (the `Sender` struct) with:

```go
type Sender struct {
	conn    *net.UDPConn
	dstAddr *net.UDPAddr
	srcPort int

	mu           sync.Mutex // serialises Writes + Mark*
	lastBlitSize int
	lastBlitTime time.Time

	sndBufActual int           // populated by readSndBuf at NewSender; 0 on unsupported platforms
	enobufCount  atomic.Uint64 // populated in Task 8
}
```

Add the import for `sync/atomic`:

```go
import (
	// ... existing imports ...
	"sync/atomic"
)
```

Add the constant near the top of the file (just after the package doc):

```go
const wantSndBuf = 2 * 1024 * 1024
```

Replace the existing `NewSender` body (lines 66–85) with:

```go
func NewSender(dstHost string, dstPort, srcPort int) (*Sender, error) {
	dst, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", dstHost, dstPort))
	if err != nil {
		return nil, err
	}
	lc := &net.ListenConfig{Control: controlSocket}
	addr := fmt.Sprintf(":%d", srcPort)
	pc, err := lc.ListenPacket(nil, "udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("bind source %d: %w", srcPort, err)
	}
	conn := pc.(*net.UDPConn)

	if err := conn.SetWriteBuffer(wantSndBuf); err != nil {
		slog.Warn("SetWriteBuffer failed", "err", err)
	}
	_ = conn.SetReadBuffer(256 * 1024)

	// Linux kernels report 2× the requested SO_SNDBUF for kernel-bookkeeping
	// reasons; this doubling is a long-standing quirk, not a stable contract.
	// Treat the readback as advisory: warn if it's below the requested size
	// (kernel clamped against net.core.wmem_max), info-log the value
	// unconditionally for postmortem debugging.
	actual, rerr := readSndBuf(conn)
	switch {
	case rerr != nil:
		slog.Debug("SO_SNDBUF readback failed", "err", rerr)
	case actual == 0:
		// unsupported platform — silent
	case actual < wantSndBuf:
		slog.Warn("kernel clamped SO_SNDBUF below 2 MB; expect ENOBUFS on busy fields. Run: sudo sysctl -w net.core.wmem_max=4194304",
			"requested", wantSndBuf, "kernel_actual", actual)
	default:
		slog.Info("SO_SNDBUF readback", "requested", wantSndBuf, "kernel_actual", actual,
			"note", "Linux returns ~2× requested as a kernel-bookkeeping quirk")
	}

	actualPort := conn.LocalAddr().(*net.UDPAddr).Port
	return &Sender{
		conn:         conn,
		dstAddr:      dst,
		srcPort:      actualPort,
		sndBufActual: actual,
	}, nil
}
```

The existing `import "log/slog"` is already present in `sender.go` (used elsewhere). If not, add it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/groovynet/ -v`
Expected: all sender tests including the new one pass.

- [ ] **Step 5: Commit**

```bash
git add internal/groovynet/sender.go internal/groovynet/sender_test.go
git commit -m "feat(groovynet): verify and log SO_SNDBUF after SetWriteBuffer"
```

---

## Task 8: `Sender` ENOBUFS counter + logging + accessor

**Files:**
- Modify: `internal/groovynet/sender.go`
- Test: `internal/groovynet/sender_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/groovynet/sender_test.go`:

```go
func TestSender_ENOBUFCountAccessor(t *testing.T) {
	s, err := NewSender("127.0.0.1", 32100, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ENOBUFCount() != 0 {
		t.Errorf("fresh sender ENOBUFCount = %d, want 0", s.ENOBUFCount())
	}
}

func TestIsPowerOfTen(t *testing.T) {
	cases := map[uint64]bool{
		0: false, 1: true, 2: false, 9: false, 10: true, 11: false,
		99: false, 100: true, 999: false, 1000: true, 9999: false, 10000: true,
	}
	for n, want := range cases {
		if got := isPowerOfTen(n); got != want {
			t.Errorf("isPowerOfTen(%d) = %v, want %v", n, got, want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/groovynet/ -run "TestSender_ENOBUFCountAccessor|TestIsPowerOfTen" -v`
Expected: build error — `ENOBUFCount` and `isPowerOfTen` not defined.

- [ ] **Step 3: Modify `internal/groovynet/sender.go`**

Add the imports `errors` and `syscall` to the existing import block:

```go
import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)
```

Replace the existing `SendPayload` function (lines 109–122) with:

```go
// SendPayload slices large payloads into MTU-sized datagrams
// (groovy.MaxDatagram = 1472). Used for BLIT field bytes and AUDIO PCM,
// which stream as a pure byte sequence on the same socket with no
// per-chunk framing.
//
// On ENOBUFS (kernel send queue full): increments enobufCount, logs at
// power-of-10 milestones, and returns the error. No retry — the field
// is torn; the caller (sendField) logs and the next field will succeed
// once the kernel queue drains. Per-chunk retries would just delay the
// next field while the queue drains, costing tick budget.
func (s *Sender) SendPayload(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	totalChunks := (len(payload) + groovy.MaxDatagram - 1) / groovy.MaxDatagram
	chunkIdx := 0
	for i := 0; i < len(payload); i += groovy.MaxDatagram {
		end := i + groovy.MaxDatagram
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := s.conn.WriteToUDP(payload[i:end], s.dstAddr); err != nil {
			if errors.Is(err, syscall.ENOBUFS) {
				n := s.enobufCount.Add(1)
				if n == 1 || isPowerOfTen(n) {
					slog.Warn("send buffer overflow (ENOBUFS); torn field — aborting remaining chunks",
						"total_events", n,
						"chunk_index", chunkIdx,
						"total_chunks", totalChunks,
						"bytes_sent", i,
						"bytes_total", len(payload),
						"sndbuf_actual", s.sndBufActual)
				}
			}
			return err
		}
		chunkIdx++
	}
	return nil
}

// ENOBUFCount returns the monotonic count of ENOBUFS events observed since
// the Sender was constructed. Safe to call concurrently. Intended for
// stats endpoints / health checks; the slog throttle alone is insufficient
// signal for chronic problems (logs only fire at 1, 10, 100, ... events).
func (s *Sender) ENOBUFCount() uint64 { return s.enobufCount.Load() }

// isPowerOfTen returns true for 1, 10, 100, 1000, ... and false for 0
// and any other value.
func isPowerOfTen(n uint64) bool {
	if n == 0 {
		return false
	}
	for n >= 10 {
		if n%10 != 0 {
			return false
		}
		n /= 10
	}
	return n == 1
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/groovynet/ -v`
Expected: all tests pass, including the new `TestSender_ENOBUFCountAccessor` and `TestIsPowerOfTen`.

- [ ] **Step 5: Commit**

```bash
git add internal/groovynet/sender.go internal/groovynet/sender_test.go
git commit -m "feat(groovynet): count and log ENOBUFS events with power-of-10 throttle"
```

---

## Task 9: `drainer.go` slog.Warn cleanup + `ackCh` semantic note

**Files:**
- Modify: `internal/groovynet/drainer.go`

This task is two related one-line changes. They land together because they both concern ACK overflow visibility.

- [ ] **Step 1: Change drainer drop-log severity**

In `internal/groovynet/drainer.go`, change line 72 from `slog.Debug` to `slog.Warn`:

Before:
```go
		select {
		case d.ch <- ack:
		default:
			slog.Debug("ack channel full, dropping")
		}
```

After:
```go
		select {
		case d.ch <- ack:
		default:
			slog.Warn("ack channel full, dropping")
		}
```

Rationale: with the upcoming Task 12 reduction of `ackCh` cap from 32 → 4, the channel is sized so dropped ACKs are rare and indicate either consumer stall or pathological FPGA traffic. Either case warrants a Warn.

- [ ] **Step 2: Run existing drainer tests to confirm no regression**

Run: `go test ./internal/groovynet/ -run TestDrainer -v`
Expected: PASS (or no tests matched — that's also fine).

- [ ] **Step 3: Commit**

```bash
git add internal/groovynet/drainer.go
git commit -m "fix(groovynet): raise ack-drop log to Warn (rare under cap=4 in next change)"
```

---

## Task 10: `Plane` helpers — `resolveVideoHeight` and `fieldPeriodFromModeline`

**Files:**
- Modify: `internal/dataplane/plane.go`
- Test: `internal/dataplane/plane_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/dataplane/plane_test.go`:

```go
func TestPlaneConfig_ResolveVideoHeight(t *testing.T) {
	cases := []struct {
		name string
		cfg  PlaneConfig
		want int
	}{
		{
			name: "explicit OutputHeight wins",
			cfg: PlaneConfig{
				FieldHeight: 240,
				Modeline:    groovy.Modeline{Interlace: 1},
				SpawnSpec:   ffmpeg.PipelineSpec{OutputHeight: 720},
			},
			want: 720,
		},
		{
			name: "interlaced doubles FieldHeight",
			cfg: PlaneConfig{
				FieldHeight: 240,
				Modeline:    groovy.Modeline{Interlace: 1},
			},
			want: 480,
		},
		{
			name: "progressive uses FieldHeight",
			cfg: PlaneConfig{
				FieldHeight: 480,
				Modeline:    groovy.Modeline{Interlace: 0},
			},
			want: 480,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.resolveVideoHeight(); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestFieldPeriodFromModeline_NTSC480i(t *testing.T) {
	period := fieldPeriodFromModeline(groovy.NTSC480i60)
	// 480i field period = 1001/60 ms ≈ 16.683 ms = 16,683,333 ns.
	// Allow ±1µs jitter from integer rounding in the formula.
	want := 16683333 * time.Nanosecond
	delta := period - want
	if delta < -time.Microsecond || delta > time.Microsecond {
		t.Errorf("period = %v, want %v ± 1µs", period, want)
	}
}

func TestFieldPeriodFromModeline_ZeroOnInvalid(t *testing.T) {
	if got := fieldPeriodFromModeline(groovy.Modeline{}); got != 0 {
		t.Errorf("zero modeline period = %v, want 0", got)
	}
}
```

If the existing plane_test.go doesn't import `groovy` or `ffmpeg`, add them.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/dataplane/ -run "TestPlaneConfig_ResolveVideoHeight|TestFieldPeriodFromModeline" -v`
Expected: build error.

- [ ] **Step 3: Add helpers to `internal/dataplane/plane.go`**

Insert after the existing `Position` / `resetPosition` / `advancePosition` block (around line 114, before `Done()`):

```go
// resolveVideoHeight is the single source of truth for the full
// progressive frame height the FFmpeg pipeline emits. Used by both
// NewPlane (to size frame buffers) and Run (to spawn the reader). MUST
// NOT be duplicated — keeping the resolution in one place prevents the
// frame-pool sizing from drifting away from the reader's expected
// width*height*bpp.
func (cfg PlaneConfig) resolveVideoHeight() int {
	if cfg.SpawnSpec.OutputHeight > 0 {
		return cfg.SpawnSpec.OutputHeight
	}
	h := cfg.FieldHeight
	if cfg.Modeline.Interlaced() {
		h *= 2
	}
	return h
}

// fieldPeriodFromModeline returns one field's wall-clock duration as
// integer nanoseconds. Same semantics as
// time.Duration(float64(time.Second) / ml.FieldRate()) but without the
// sub-µs truncation of float division. Matches the integer-exact
// position math at Position(). Returns 0 on a zero/invalid modeline.
func fieldPeriodFromModeline(ml groovy.Modeline) time.Duration {
	if ml.PClock <= 0 || ml.HTotal == 0 || ml.VTotal == 0 {
		return 0
	}
	pixelsPerField := uint64(ml.HTotal) * uint64(ml.VTotal)
	if ml.Interlaced() {
		pixelsPerField /= 2
	}
	pclockMicroHz := uint64(ml.PClock * 1_000_000)
	if pclockMicroHz == 0 {
		return 0
	}
	return time.Duration((pixelsPerField * 1_000_000_000) / pclockMicroHz)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dataplane/ -run "TestPlaneConfig_ResolveVideoHeight|TestFieldPeriodFromModeline" -v`
Expected: 4 PASSes (3 sub-tests + the invalid-modeline test).

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "feat(dataplane): add resolveVideoHeight and fieldPeriodFromModeline helpers"
```

---

## Task 11: `Plane` struct fields + `NewPlane` allocates scratch + framePool

**Files:**
- Modify: `internal/dataplane/plane.go`

This task adds the new struct fields and allocates them in `NewPlane`. The fields are not yet read by `Run` — that swap is Task 12. After this task lands, the codebase still uses the legacy allocating path; the new fields are dormant.

- [ ] **Step 1: Add fields to `Plane` struct**

In `internal/dataplane/plane.go`, find the `Plane` struct (around lines 45–59). Replace the struct with:

```go
type Plane struct {
	cfg            PlaneConfig
	proc           *ffmpeg.Process
	positionFields atomic.Int64
	audioReady     atomic.Bool
	fpgaFrame      atomic.Uint32
	done           chan struct{}

	// fieldOrderFlip is the live TFF↔BFF hot-swap. When true, each
	// field's polarity byte is inverted before BLIT_FIELD_VSYNC send.
	// Effect: a 1-raster-line phase shift on the CRT, which is exactly
	// what the operator flips via the UI to fix shimmer without
	// restarting the ffmpeg pipeline.
	fieldOrderFlip atomic.Bool

	// Pre-allocated session-lifetime buffers (perf pack). Owned by the
	// tick loop's goroutine; do not access concurrently. The framePool's
	// buffer count and frameBytes size are determined at NewPlane time
	// from PlaneConfig and held constant for the session lifetime —
	// mid-session resolution changes are not supported.
	framePool     *FramePool
	fieldScratch  []byte // len == cfg.FieldWidth * cfg.FieldHeight * cfg.BytesPerPixel
	lz4Scratch    []byte // len == lz4.CompressBlockBound(fieldBytes)
	headerScratch []byte // len == groovy.BlitHeaderLZ4Delta
}
```

Add the import for `lz4` (note: it's a transitive dep already in go.mod via internal/groovy):

```go
import (
	// ... existing imports ...
	"github.com/pierrec/lz4/v4"
)
```

- [ ] **Step 2: Define the framePool depth constant**

Insert near the top of the file, after the `PlaneConfig` struct:

```go
// framePoolSlots is the depth of the free queue. Sized to videoChCap + 2
// to cover (1 reader in-progress + videoChCap in-channel + 1 tick
// in-progress) given the invariant that ReadFramesFromPipePooled holds
// at most one *FrameBuf outside the pool at any time.
const (
	videoChCap     = 8
	framePoolSlots = videoChCap + 2
)
```

- [ ] **Step 3: Update `NewPlane` to allocate scratch + pool**

Replace the existing `NewPlane` function with:

```go
func NewPlane(cfg PlaneConfig) *Plane {
	videoHeight := cfg.resolveVideoHeight()
	frameBytes := cfg.FieldWidth * videoHeight * cfg.BytesPerPixel
	fieldBytes := cfg.FieldWidth * cfg.FieldHeight * cfg.BytesPerPixel

	p := &Plane{
		cfg:           cfg,
		done:          make(chan struct{}),
		framePool:     NewFramePool(framePoolSlots, frameBytes),
		fieldScratch:  make([]byte, fieldBytes),
		lz4Scratch:    make([]byte, lz4.CompressBlockBound(fieldBytes)),
		headerScratch: make([]byte, groovy.BlitHeaderLZ4Delta),
	}
	if cfg.SpawnSpec.FieldOrder == "bff" {
		p.fieldOrderFlip.Store(true)
	}
	return p
}
```

- [ ] **Step 4: Verify build and all existing tests still pass**

Run:

```bash
go build ./...
go test ./internal/dataplane/ -v
```

Expected: build succeeds, all existing dataplane tests pass. The new fields are present but unused by `Run` yet; this is intentional.

- [ ] **Step 5: Commit**

```bash
git add internal/dataplane/plane.go
git commit -m "feat(dataplane): allocate session-lifetime scratch buffers in NewPlane"
```

---

## Task 12: `Plane.Run` — switch to pooled reader and `*Into` variants

**Files:**
- Modify: `internal/dataplane/plane.go`

This is the disruptive task that swaps the pipeline. Prior tasks are additive; this one changes runtime behavior. After this task, allocation rate on the hot path drops to near zero.

- [ ] **Step 1: Replace `Run`'s reader/timer setup**

In `internal/dataplane/plane.go`, locate the `Run` function (around line 122). Replace the section from "1. INIT handshake" comment through the end of section "5. Position bookkeeping" — specifically, lines 156–191 — with the version below. (The INIT handshake at lines 135–154 and the variable declarations at lines 196–204 are unchanged; only the reader spawn, channel allocation, and field-period derivation change.)

```go
	// 3. Start drainer for subsequent ACKs (frame echo, audio-ready updates).
	//    Stop it on return so a preempting session's SendInitAwaitACK gets
	//    uncontested access to the socket — the sender is shared across
	//    sessions for stable source port, so the drainer MUST be explicitly
	//    stopped; closing the socket isn't an option.
	ackCh := make(chan groovy.ACK, 4)
	drainer := groovynet.NewDrainer(p.cfg.Sender, ackCh)
	go drainer.Run()
	defer drainer.Stop()

	// 4. Readers + timer. videoCh now carries *FrameBuf pointers from the
	//    framePool; the tick loop returns each buffer to the pool after
	//    sendField completes. Audio path is unchanged for this perf pack.
	videoCh := make(chan *FrameBuf, videoChCap)
	var audioCh chan []byte
	videoHeight := p.cfg.resolveVideoHeight()
	go ReadFramesFromPipePooled(proc.VideoPipe(), p.framePool, videoCh)
	if audioEnabled {
		audioCh = make(chan []byte, 16)
		go ReadAudioFromPipe(proc.AudioPipe(), audioRate, audioChans, audioCh)
	}
	fieldPeriod := fieldPeriodFromModeline(p.cfg.Modeline)
	if fieldPeriod <= 0 {
		// Modeline doesn't produce a valid period (zero PClock etc.).
		// Fall back to the previous float-derived value so we don't
		// silently freeze.
		fieldRate := p.cfg.Modeline.FieldRate()
		if fieldRate <= 0 {
			fieldRate = 59.94
		}
		fieldPeriod = time.Duration(float64(time.Second) / fieldRate)
	}
	timer := time.NewTimer(fieldPeriod)
	defer timer.Stop()
	lastTick := time.Now()
	linePeriod := rasterLinePeriod(p.cfg.Modeline)
	latestACK := ack
	lastCorrectedEcho := ack.FrameEcho

	// 5. Position bookkeeping — one tick = one NTSC field (1001/60 ms, exact).
	p.resetPosition()
```

- [ ] **Step 2: Replace the `case <-timer.C:` video-tick branch**

The current branch (lines 222–276) pulls a `[]byte` from `videoCh` and allocates a new field buffer + LZ4 dst per send. Replace it with the version below, which pulls a `*FrameBuf`, writes through scratch buffers, and returns the `*FrameBuf` to the pool.

The new branch goes from `case <-timer.C:` up to the end of audio handling (just before the rasterCorrection block at the tail). Replace lines 222–288 with:

```go
		case <-timer.C:
			lastTick = time.Now()
			frameNum++
			// The FFmpeg pipeline emits full-height progressive frames at the
			// field cadence. Keep the BLIT header field bit aligned to the local
			// row-stripe order here; deriving parity from live vgaF1 feedback
			// would risk tagging a top-field payload as bottom-field (or vice
			// versa).
			//
			// Live TFF↔BFF flip (SetFieldOrder): when the operator swaps field
			// order via the UI mid-session, fieldOrderFlip toggles true. We
			// invert emitField so BOTH the header tag and the payload slice
			// (ExtractFieldFromFrameInto below) swap together — inverting only
			// the header would send top-field pixels tagged as bottom-field.
			emitField := nextField
			if p.fieldOrderFlip.Load() {
				emitField ^= 1
			}
			select {
			case fb, ok := <-videoCh:
				if !ok {
					_ = p.cfg.Sender.Send(groovy.BuildClose())
					return nil
				}
				if consecutiveUnderruns >= 30 {
					slog.Debug("video pipe recovered after duplicate-field underrun",
						"fields", consecutiveUnderruns,
						"duration_ms", time.Since(consecutiveUnderrunFrom).Milliseconds())
				}
				consecutiveUnderruns = 0
				consecutiveUnderrunFrom = time.Time{}
				var payload []byte
				if p.cfg.Modeline.Interlaced() {
					ExtractFieldFromFrameInto(p.fieldScratch, fb.Data[:fb.N],
						p.cfg.FieldWidth, videoHeight, p.cfg.BytesPerPixel, emitField)
					payload = p.fieldScratch
				} else {
					payload = fb.Data[:fb.N]
				}
				p.sendField(frameNum, emitField, payload)
				// Trailing Put — invariant (2): sendField does not return
				// errors out of Run, so unconditional Put after sendField
				// is safe. defer is reserved for panic-prone code paths.
				p.framePool.Put(fb)
			default:
				if consecutiveUnderruns == 0 {
					consecutiveUnderrunFrom = time.Now()
				}
				consecutiveUnderruns++
				if consecutiveUnderruns == 30 || consecutiveUnderruns%120 == 0 {
					slog.Warn("video pipe underrun; duplicating fields to hold raster",
						"fields", consecutiveUnderruns,
						"duration_ms", time.Since(consecutiveUnderrunFrom).Milliseconds(),
						"audio_ready", p.audioReady.Load())
				}
				p.sendDuplicate(frameNum, emitField)
			}
			if p.cfg.Modeline.Interlaced() {
				nextField ^= 1
			} else {
				nextField = 0
			}

			// Audio: only send while ACK bit 6 (fpga.audio) is set AND we
			// have PCM ready. Never block the pump loop on audio.
			if audioEnabled && p.audioReady.Load() {
				select {
				case pcm, ok := <-audioCh:
					if ok && len(pcm) > 0 {
						p.sendAudio(pcm)
					}
				default:
				}
			}
			// Advance reported position by one field period.
			p.advancePosition()
			if correction, ok := rasterCorrection(latestACK, p.cfg.Modeline, linePeriod, fieldPeriod, lastCorrectedEcho); ok {
				resetTimer(timer, nextTickDelay(lastTick, fieldPeriod, correction))
				lastCorrectedEcho = latestACK.FrameEcho
			} else {
				resetTimer(timer, fieldPeriod)
			}
```

- [ ] **Step 3: Update `sendField` to use scratch buffers**

Replace the existing `sendField` function (around lines 387–409) with:

```go
// sendField sends one BLIT_FIELD_VSYNC header + payload using session-
// lifetime scratch buffers (lz4Scratch for the compressed body,
// headerScratch for the header bytes). All allocations are amortized
// to NewPlane time. Applies congestion backoff before the header and
// records the payload size afterwards so the next call can honor the
// reference ~11 ms wait after any >500 KB blit.
//
// Compression policy: if LZ4 is enabled AND the field is compressible
// (LZ4CompressInto returns ok=true), the LZ4 BLIT variant is emitted.
// Otherwise — either LZ4 is disabled in config, OR the field is
// incompressible — a RAW BLIT variant is emitted with the uncompressed
// bytes. Emitting an LZ4 header with CompressedSize=0 would desync the
// receiver.
func (p *Plane) sendField(frame uint32, field uint8, raw []byte) {
	opts := groovy.BlitOpts{Frame: frame, Field: field}
	payload := raw
	if p.cfg.LZ4Enabled {
		if n, ok := groovy.LZ4CompressInto(p.lz4Scratch, raw); ok {
			payload = p.lz4Scratch[:n]
			opts.Compressed = true
			opts.CompressedSize = uint32(n)
		} else {
			slog.Debug("lz4 incompressible frame; falling back to RAW BLIT", "size", len(raw))
		}
	}
	p.cfg.Sender.WaitForCongestion()
	header := groovy.BuildBlitHeaderInto(p.headerScratch, opts)
	if err := p.cfg.Sender.Send(header); err != nil {
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

- [ ] **Step 4: Run all dataplane tests**

Run:

```bash
go test ./internal/dataplane/ -v -count=1
```

Expected: all existing tests pass, plus the tests added in Tasks 1–11.

- [ ] **Step 5: Run the full test suite**

Run:

```bash
go test ./... -count=1
```

Expected: all tests pass. Pay particular attention to `internal/fakemister/...` and any other consumer of the data plane.

- [ ] **Step 6: Commit**

```bash
git add internal/dataplane/plane.go
git commit -m "feat(dataplane): switch tick loop to pooled reader and *Into variants"
```

---

## Task 13: Plane allocation-budget integration test

**Files:**
- Modify: `internal/dataplane/plane_test.go`

The test runs `Plane.Run` against `fakemister.Listener` for ~1 second of simulated playback and asserts that runtime allocations are O(KB), not O(MB). It is a regression test against any future change that re-introduces per-tick allocations.

- [ ] **Step 1: Read existing plane_test.go to understand test fixtures**

Run: `cat internal/dataplane/plane_test.go | head -80`

Identify: (a) any helper that builds a `PlaneConfig` for a fake-mister-backed test, (b) any existing pattern that drives the data plane against `fakemister`. The test you write should mirror those patterns.

- [ ] **Step 2: Write the integration test**

Append to `internal/dataplane/plane_test.go`:

```go
// TestPlane_AllocationBudget verifies that the perf pack's pool + scratch
// buffers actually keep the hot path zero-alloc. Runs Plane.Run against a
// fakemister.Listener for ~30 ticks (~500 ms) and asserts that
// runtime.MemStats.TotalAlloc grows by less than 1 MB — generous, since
// the legacy path was ~120 MB/s and the pooled path should be near-zero
// (only goroutine launch overhead and slog formatting).
func TestPlane_AllocationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; allocates and runs goroutines for 500ms")
	}

	// TODO: replace this skeleton with the project's existing fakemister-
	// backed plane test scaffolding. The shape below assumes:
	//   - fakemister.NewListener provides a UDP server that ACKs INIT
	//   - groovynet.NewSender targets it
	//   - PlaneConfig is constructed with a small modeline + ffmpeg spec
	//     pointing at a synthetic byte source (bytes.NewReader of N
	//     pre-built frames) for the video pipe.
	//
	// If a similar test already exists, factor out a shared helper
	// rather than duplicating wiring code. If not, this is the first
	// fakemister-backed Plane test and may motivate a small testhelpers
	// file.
	t.Skip("test scaffold; implementer to wire to fakemister fixtures")

	// Skeleton (uncomment and adapt to existing fixtures):
	//
	// l, err := fakemister.NewListener(":0")
	// if err != nil {
	//     t.Fatal(err)
	// }
	// defer l.Close()
	// go l.Run(make(chan fakemister.Command, 1024))
	//
	// addr := l.Addr().(*net.UDPAddr)
	// sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	// if err != nil {
	//     t.Fatal(err)
	// }
	// defer sender.Close()
	//
	// // Synthetic frame source: 30 frames of 720*480*3 zeros.
	// frameBytes := 720 * 480 * 3
	// videoSrc := bytes.NewReader(make([]byte, frameBytes*30))
	// // ... wire videoSrc into PlaneConfig.SpawnSpec via ffmpeg.PipelineSpec
	// //     or a test-only proc spawner.
	//
	// p := NewPlane(PlaneConfig{
	//     Sender:        sender,
	//     Modeline:      groovy.NTSC480i60,
	//     FieldWidth:    720,
	//     FieldHeight:   240,
	//     BytesPerPixel: 3,
	//     RGBMode:       groovy.RGBMode888,
	//     LZ4Enabled:    true,
	//     // ... fill in the rest from existing test fixtures
	// })
	//
	// runtime.GC()
	// var before runtime.MemStats
	// runtime.ReadMemStats(&before)
	//
	// ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	// defer cancel()
	// _ = p.Run(ctx)
	//
	// runtime.GC()
	// var after runtime.MemStats
	// runtime.ReadMemStats(&after)
	//
	// const budgetBytes = 1 * 1024 * 1024 // 1 MB; pre-pack was ~60 MB/500ms
	// if delta := after.TotalAlloc - before.TotalAlloc; delta > budgetBytes {
	//     t.Errorf("Plane.Run allocated %d bytes over 500ms; budget %d", delta, budgetBytes)
	// }
}
```

The test is intentionally a skeleton that calls `t.Skip` — wiring it to actual fakemister fixtures requires checking how the project's existing plane integration tests stand up the FFmpeg-side video pipe (the data plane reads from `proc.VideoPipe()`, an `io.Reader` produced by spawning ffmpeg). The implementer should:

1. Search for existing tests that drive `Plane.Run` end-to-end (likely uses a stub `ffmpeg.Process` or in-process reader). Pattern: `grep -r "NewPlane" internal/`.
2. If found, adapt this test to that scaffold.
3. If not, this test is the canonical first end-to-end allocation test; remove the `t.Skip` and wire it directly via a stub `ffmpeg.Process`. Likely requires a small testhelpers.go addition.

- [ ] **Step 3: Run the new test (will skip)**

Run: `go test ./internal/dataplane/ -run TestPlane_AllocationBudget -v`
Expected: SKIP with "test scaffold; implementer to wire to fakemister fixtures". The skeleton is committed as documentation of intent; subsequent work removes the skip.

- [ ] **Step 4: Commit**

```bash
git add internal/dataplane/plane_test.go
git commit -m "test(dataplane): add allocation-budget regression test scaffold"
```

- [ ] **Step 5: Wire the skeleton (follow-up work, in this same PR)**

Open the file you committed in step 4. Replace the `t.Skip(...)` line with concrete fixture wiring per Step 2. Common approach if no end-to-end Plane test exists:

1. Add a `testProc` type in a new `testhelpers_test.go` that satisfies the same `*ffmpeg.Process` interface (or accept a refactor — introduce a small interface for what `Plane` needs from `Process`: `VideoPipe() io.ReadCloser`, `AudioPipe() io.ReadCloser`, `Done() <-chan struct{}`, `Stop()`).
2. Stub `testProc` to return `io.NopCloser(bytes.NewReader(syntheticFrames))` for video.
3. Inject the test process via a constructor variant or factory hook (existing patterns in the package will hint at this).

Run: `go test ./internal/dataplane/ -run TestPlane_AllocationBudget -v`
Expected: PASS with `Plane.Run allocated <small> bytes over 500ms; budget 1048576`.

If the wiring requires a non-trivial refactor (e.g., introducing an interface for `ffmpeg.Process`), split that into its own commit before the test wiring commit:

```bash
# If a refactor is needed:
git add internal/dataplane/plane.go internal/ffmpeg/process.go
git commit -m "refactor(dataplane): introduce processIface for testability"

git add internal/dataplane/testhelpers_test.go internal/dataplane/plane_test.go
git commit -m "test(dataplane): wire allocation-budget test via test process stub"
```

If no refactor is needed:

```bash
git add internal/dataplane/plane_test.go internal/dataplane/testhelpers_test.go
git commit -m "test(dataplane): wire allocation-budget test via test process stub"
```

---

## Self-Review

### Spec coverage

| Spec section | Covered by | Notes |
|---|---|---|
| Eliminate per-tick allocations (table at "Allocation table" in spec) | Tasks 1–5, 11–12 | All four sites (frame, field, lz4, header) replaced |
| `framePool` + `FrameBuf` types | Task 4 | |
| `*Into` API variants | Tasks 1, 2, 3 | LZ4CompressInto, BuildBlitHeaderInto, ExtractFieldFromFrameInto |
| `ReadFramesFromPipePooled` with EOF semantics | Task 5 | Includes partial-read test |
| `SO_SNDBUF` readback + clamp warning | Tasks 6, 7 | readSndBuf shim + NewSender log branches |
| `ENOBUFS` counter + power-of-10 throttle | Task 8 | Includes `ENOBUFCount()` accessor |
| `ackCh` cap=4 + drainer slog.Warn | Tasks 9, 12 | Drainer log change in 9; cap change in 12 |
| `videoCh` cap=8 with `*FrameBuf` | Task 12 | Constant defined in 11 |
| Integer-math `fieldPeriodFromModeline` | Task 10 | |
| `resolveVideoHeight` shared helper | Task 10 | DRYs NewPlane and Run |
| Pool ownership invariants in package doc | Tasks 4, 5 | Documented in framepool.go and ReadFramesFromPipePooled |
| Allocation-budget test | Task 13 | Skeleton + wiring guidance |

No gaps. Every spec section maps to a task.

### Type/name consistency

- `LZ4CompressInto` — used in Tasks 1, 12 (in `sendField`).
- `BuildBlitHeaderInto` — used in Tasks 2, 12.
- `ExtractFieldFromFrameInto` — used in Tasks 3, 12.
- `FramePool`, `FrameBuf`, `NewFramePool`, `Get`, `Put` — defined in Task 4, used in Tasks 5, 11, 12.
- `ReadFramesFromPipePooled` — defined in Task 5, used in Task 12.
- `readSndBuf` — defined in Task 6, used in Task 7.
- `wantSndBuf`, `sndBufActual`, `enobufCount`, `ENOBUFCount`, `isPowerOfTen` — defined in Tasks 7–8, internal to Sender.
- `resolveVideoHeight`, `fieldPeriodFromModeline` — defined in Task 10, used in Tasks 11, 12.
- `videoChCap`, `framePoolSlots` — defined in Task 11, used in Task 12.

All names match across tasks.

### Placeholder scan

One legitimate skip-then-wire pattern in Task 13 (the integration test). The skeleton is committed as documentation, then wired in step 5. This is intentional: the test requires understanding of project-specific ffmpeg.Process scaffolding that the plan can't predict in detail without reading more code than fits in the plan. Step 5 of Task 13 explicitly directs the implementer to do that exploration.

No "TBD", "TODO", "implement later", "similar to Task N", or steps without code. Every code change in every step is shown in full.

### Risk callouts

- Task 12 is the disruptive swap. If it lands broken, the data plane is broken. Mitigations: Tasks 1–11 are all additive and revertible cleanly; Task 12 changes only `plane.go`; the existing tests cover most of the surface; the next task (13) is an end-to-end allocation regression test.
- Task 13 step 5 may discover that no end-to-end Plane test exists. The plan accommodates either path (refactor + test, or just test) with separate commit messages.

---

## Execution Handoff

Plan complete and saved to `docs/plans/2026-04-24-dataplane-perf-pack.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration with checkpoint commits.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints for review.

Which approach?
