package groovy

import (
	"bytes"
	"crypto/rand"
	"testing"
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
