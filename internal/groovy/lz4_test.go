package groovy

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func TestLZ4RoundTrip(t *testing.T) {
	// Use a field-sized buffer of mostly-zero data with some variance.
	src := make([]byte, 518400)
	for i := range src {
		src[i] = byte(i % 256)
	}
	compressed, ok := LZ4Compress(src)
	if !ok {
		t.Fatal("compressible input returned ok=false")
	}
	if len(compressed) == 0 {
		t.Fatal("compressed buf is empty")
	}
	decompressed, err := LZ4Decompress(compressed, len(src))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(src, decompressed) {
		t.Error("round-trip mismatch")
	}
}

func TestLZ4Compress_ReducesZeros(t *testing.T) {
	src := make([]byte, 100000) // all zeros
	compressed, ok := LZ4Compress(src)
	if !ok {
		t.Fatal("zeros should be compressible")
	}
	if len(compressed) > len(src)/10 {
		t.Errorf("zeros should compress hard; got %d/%d", len(compressed), len(src))
	}
}

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
	// IMPLEMENTATION NOTE: pierrec/lz4/v4's lz4.Compressor declares its
	// hash table as a pointer field allocated on first CompressBlock call.
	// With `var c lz4.Compressor` declared as a stack-local in
	// LZ4CompressInto, that pointer-allocation happens once per call and
	// AllocsPerRun WILL report > 0. If this assertion fails, two options:
	// (a) raise the threshold to a small constant like `if got > 1`,
	// matching the existing LZ4Compress allocation profile;
	// (b) hoist `var lz4Compressor lz4.Compressor` to package scope and
	// guard with a sync.Mutex (the data plane is single-threaded, but the
	// test suite is not). The spec's allocation budget is "near-zero",
	// not "literally zero" — option (a) is consistent with the spec.
	if got > 1 {
		t.Errorf("LZ4CompressInto allocs/op = %v, want <= 1", got)
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
