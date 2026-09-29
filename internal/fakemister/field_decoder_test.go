package fakemister

import (
	"bytes"
	cryptorand "crypto/rand"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy/nlc"
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

// TestFieldDecoder_NLCRoundTrip confirms SetInit(2 | ...) plus SetDims makes
// Decode take the NLC path and recover the encoder's exact input at near 0.
func TestFieldDecoder_NLCRoundTrip(t *testing.T) {
	const w, h = 96, 24
	p := nlc.Params{Width: w, Height: h, Near: 0, Pack: nlc.PackTiled}
	src := make([]byte, nlc.FrameBytes(p))
	for i := range src {
		src[i] = byte(i * 7 % 251)
	}
	enc, err := nlc.NewEncoder(p)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dst := make([]byte, nlc.MaxEncodedSize(p))
	n, err := enc.EncodeInto(dst, src)
	if err != nil {
		t.Fatalf("EncodeInto: %v", err)
	}

	d := NewFieldDecoder()
	d.SetInit(groovy.NLCCompressionByte(0, false))
	d.SetDims(w, h)
	out, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 1, Field: 0, Compressed: true, CompressedSize: uint32(n)},
		Payload: dst[:n],
	}, w*h*3)
	if err != nil {
		t.Fatalf("decode nlc: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatal("nlc round trip at near 0 is not lossless")
	}
}

// TestFieldDecoder_NLCCorruptPayloadErrors confirms a corrupted NLC payload
// surfaces as a decode error rather than a silently wrong or truncated field.
func TestFieldDecoder_NLCCorruptPayloadErrors(t *testing.T) {
	const w, h = 96, 24
	p := nlc.Params{Width: w, Height: h, Near: 0, Pack: nlc.PackTiled}
	src := make([]byte, nlc.FrameBytes(p))
	if _, err := cryptorand.Read(src); err != nil {
		t.Fatal(err)
	}
	enc, err := nlc.NewEncoder(p)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dst := make([]byte, nlc.MaxEncodedSize(p))
	n, err := enc.EncodeInto(dst, src)
	if err != nil {
		t.Fatalf("EncodeInto: %v", err)
	}
	encoded := dst[:n]
	// A segment-length field pointing past the end of the payload makes the
	// stream truncated, which nlc.Decode rejects (see nlc.Decode's doc).
	corrupt := append([]byte(nil), encoded...)
	corrupt[2], corrupt[3] = 0xff, 0xff

	d := NewFieldDecoder()
	d.SetInit(groovy.NLCCompressionByte(0, false))
	d.SetDims(w, h)
	if _, err := d.Decode(FieldEvent{
		Header:  BlitHeader{Frame: 1, Field: 0, Compressed: true, CompressedSize: uint32(len(corrupt))},
		Payload: corrupt,
	}, w*h*3); err == nil {
		t.Fatal("expected error decoding a corrupt NLC payload")
	}
}

// TestFieldDecoder_SetInitRawAndLZ4Unaffected confirms SetInit(0) and
// SetInit(1) leave raw and LZ4 decoding exactly as before SetInit existed.
func TestFieldDecoder_SetInitRawAndLZ4Unaffected(t *testing.T) {
	const fieldBytes = 720 * 240 * 3

	t.Run("raw", func(t *testing.T) {
		d := NewFieldDecoder()
		d.SetInit(groovy.LZ4ModeOff)
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
			t.Fatal("raw payload corrupted by decoder after SetInit(0)")
		}
	})

	t.Run("lz4", func(t *testing.T) {
		d := NewFieldDecoder()
		d.SetInit(groovy.LZ4ModeDefault)
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
			t.Fatal("lz4 round-trip corrupted after SetInit(1)")
		}
	})
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
