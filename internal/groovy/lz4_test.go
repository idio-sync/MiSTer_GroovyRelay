package groovy

import (
	"bytes"
	"testing"
)

func TestLZ4RoundTrip(t *testing.T) {
	// Use a field-sized buffer of mostly-zero data with some variance.
	src := make([]byte, 518400)
	for i := range src {
		src[i] = byte(i % 256)
	}
	compressed, err := LZ4Compress(src)
	if err != nil {
		t.Fatalf("compress: %v", err)
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
	compressed, err := LZ4Compress(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) > len(src)/10 {
		t.Errorf("zeros should compress hard; got %d/%d", len(compressed), len(src))
	}
}
