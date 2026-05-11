package fakemister

import (
	"bytes"
	cryptorand "crypto/rand"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/pierrec/lz4/v4"
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
	var c lz4.Compressor
	n, ok := groovy.LZ4CompressInto(&c, scratch, raw)
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

func TestFieldDecoder_DeltaReconstructs(t *testing.T) {
	const fieldBytes = 720 * 240 * 3
	d := NewFieldDecoder()

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

	b := append([]byte(nil), a...)
	for i := 0; i < 64; i++ {
		b[i*1024] += byte(i + 1)
	}
	delta := make([]byte, fieldBytes)
	for i := range delta {
		delta[i] = b[i] - a[i]
	}
	scratch := make([]byte, len(delta)*2)
	var c lz4.Compressor
	n, ok := groovy.LZ4CompressInto(&c, scratch, delta)
	if !ok {
		t.Fatal("zero-heavy subtraction delta should be compressible")
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
// violation cleanly rather than silently emitting a subtraction delta as pixels.
func TestFieldDecoder_DeltaWithoutPrevErrors(t *testing.T) {
	d := NewFieldDecoder()
	scratch := make([]byte, 4096)
	dummy := make([]byte, 720*240*3)
	var c lz4.Compressor
	n, ok := groovy.LZ4CompressInto(&c, scratch, dummy)
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

func TestFieldDecoder_LostDeltaDesyncsUntilFullField(t *testing.T) {
	const fieldBytes = 720 * 240 * 3
	d := NewFieldDecoder()

	a := make([]byte, fieldBytes)
	if _, err := cryptorand.Read(a); err != nil {
		t.Fatal(err)
	}
	b := append([]byte(nil), a...)
	cField := append([]byte(nil), b...)
	for i := 0; i < 128; i++ {
		b[i*512] += byte(i + 1)
		cField[i*512] += byte(i + 3)
	}

	if _, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 1, Field: 0},
		Payload: a,
	}, fieldBytes); err != nil {
		t.Fatalf("seed prev: %v", err)
	}

	// Simulate losing B on the wire. Sender history advances to B, while
	// fake receiver history remains A. The next delta C-B cannot reconstruct C.
	deltaCFromB := make([]byte, fieldBytes)
	for i := range deltaCFromB {
		deltaCFromB[i] = cField[i] - b[i]
	}
	scratch := make([]byte, len(deltaCFromB)*2)
	var comp lz4.Compressor
	n, ok := groovy.LZ4CompressInto(&comp, scratch, deltaCFromB)
	if !ok {
		t.Fatal("zero-heavy delta should be compressible")
	}

	got, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 5, Field: 0, Compressed: true, Delta: true, CompressedSize: uint32(n)},
		Payload: scratch[:n],
	}, fieldBytes)
	if err != nil {
		t.Fatalf("decode delta after simulated loss: %v", err)
	}
	if bytes.Equal(got, cField) {
		t.Fatal("lost delta unexpectedly reconstructed the correct field")
	}

	got, err = d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 7, Field: 0},
		Payload: cField,
	}, fieldBytes)
	if err != nil {
		t.Fatalf("decode full resync field: %v", err)
	}
	if !bytes.Equal(got, cField) {
		t.Fatal("full field did not resync decoder history")
	}

	dField := append([]byte(nil), cField...)
	for i := 0; i < 128; i++ {
		dField[i*512] += byte(i + 5)
	}
	deltaDFromC := make([]byte, fieldBytes)
	for i := range deltaDFromC {
		deltaDFromC[i] = dField[i] - cField[i]
	}
	scratch = make([]byte, len(deltaDFromC)*2)
	n, ok = groovy.LZ4CompressInto(&comp, scratch, deltaDFromC)
	if !ok {
		t.Fatal("zero-heavy post-resync delta should be compressible")
	}

	got, err = d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 9, Field: 0, Compressed: true, Delta: true, CompressedSize: uint32(n)},
		Payload: scratch[:n],
	}, fieldBytes)
	if err != nil {
		t.Fatalf("decode delta after full resync: %v", err)
	}
	if !bytes.Equal(got, dField) {
		t.Fatal("post-resync delta did not reconstruct field D")
	}
}
