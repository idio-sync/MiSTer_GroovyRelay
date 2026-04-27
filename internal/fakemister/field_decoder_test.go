package fakemister

import (
	"bytes"
	cryptorand "crypto/rand"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// TestFieldDecoder_RawPassthrough confirms an uncompressed BLIT round-trips
// unchanged.
func TestFieldDecoder_RawPassthrough(t *testing.T) {
	const fieldBytes = 720 * 240 * 3
	d := NewFieldDecoder()
	raw := make([]byte, fieldBytes)
	if _, err := cryptorand.Read(raw); err != nil {
		t.Fatal(err)
	}
	out, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 1, Field: 0},
		Payload: raw,
	}, fieldBytes)
	if err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("raw payload corrupted by decoder")
	}
}

// TestFieldDecoder_LZ4Passthrough confirms a non-delta LZ4 BLIT decompresses
// to the original bytes.
func TestFieldDecoder_LZ4Passthrough(t *testing.T) {
	const fieldBytes = 720 * 240 * 3
	d := NewFieldDecoder()
	raw := make([]byte, fieldBytes)
	for i := range raw {
		raw[i] = byte(i % 251)
	}
	scratch := make([]byte, len(raw)*2)
	n, ok := groovy.LZ4CompressInto(scratch, raw)
	if !ok {
		t.Fatal("compressible input wasn't compressed")
	}
	out, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 1, Field: 0, Compressed: true, CompressedSize: uint32(n)},
		Payload: scratch[:n],
	}, fieldBytes)
	if err != nil {
		t.Fatalf("decode lz4: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("lz4 round-trip corrupted")
	}
}

// TestFieldDecoder_DeltaReconstructs feeds a non-delta field followed by a
// delta-LZ4 field constructed the same way Plane.chooseBlitPayload does and
// asserts the second decode reconstructs the second field's raw bytes.
//
// This is the exact failure mode behind TestScenario_PixelVariance: without
// XOR-reversal the dumper sees a near-uniform-zero delta where it expects
// pixels.
func TestFieldDecoder_DeltaReconstructs(t *testing.T) {
	const fieldBytes = 720 * 240 * 3
	d := NewFieldDecoder()

	// Field A: random — primes prev[0].
	a := make([]byte, fieldBytes)
	if _, err := cryptorand.Read(a); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 1, Field: 0},
		Payload: a,
	}, fieldBytes); err != nil {
		t.Fatalf("seed prev: %v", err)
	}

	// Field B (same polarity): differs from A in a few scattered bytes. The
	// XOR delta is mostly zero — exactly the situation where mis-decode looks
	// like a black frame.
	b := append([]byte(nil), a...)
	for i := 0; i < 64; i++ {
		b[i*1024] ^= byte(i + 1)
	}
	delta := make([]byte, fieldBytes)
	for i := range delta {
		delta[i] = a[i] ^ b[i]
	}
	scratch := make([]byte, len(delta)*2)
	n, ok := groovy.LZ4CompressInto(scratch, delta)
	if !ok {
		t.Fatal("zero-heavy delta should be compressible")
	}

	got, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 3, Field: 0, Compressed: true, Delta: true, CompressedSize: uint32(n)},
		Payload: scratch[:n],
	}, fieldBytes)
	if err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if !bytes.Equal(got, b) {
		t.Fatal("delta payload did not reconstruct field B")
	}
}

// TestFieldDecoder_DeltaWithoutPrevErrors ensures we surface the protocol
// violation cleanly rather than silently emitting an XOR delta as pixels.
func TestFieldDecoder_DeltaWithoutPrevErrors(t *testing.T) {
	d := NewFieldDecoder()
	scratch := make([]byte, 4096)
	dummy := make([]byte, 720*240*3)
	n, ok := groovy.LZ4CompressInto(scratch, dummy)
	if !ok {
		t.Skip("zero buffer compressed to >= input; skip")
	}
	_, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 1, Field: 0, Compressed: true, Delta: true, CompressedSize: uint32(n)},
		Payload: scratch[:n],
	}, 720*240*3)
	if err == nil {
		t.Fatal("expected error on delta without prev field")
	}
}
