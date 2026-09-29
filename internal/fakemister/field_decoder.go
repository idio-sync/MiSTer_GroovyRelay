package fakemister

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy/nlc"
)

// codecRaw, codecLZ4 and codecNLC are INIT byte[1] bits[1:0], the codec
// field SetInit reads (the same shape groovy.NLCCompressionByte builds).
const (
	codecRaw byte = 0
	codecLZ4 byte = 1
	codecNLC byte = 2
)

// FieldDecoder reconstructs the raw BGR bytes of a BLIT field, applying
// LZ4 or NLC decompression and the optional delta-against-previous-same-
// polarity-field reversal used by Groovy_MiSTer delta-LZ4.
//
// The decoder keeps one previous-field buffer per field polarity (top vs.
// bottom). Delta-LZ4 payloads contain modulo-256 byte subtraction
// (current - previous), and the FPGA reconstructs pixels by adding the
// previous framebuffer bytes back to the decompressed delta.
//
// Not goroutine-safe: a FieldDecoder is owned by a single consumer of the
// FieldEvent channel and mutated in place.
type FieldDecoder struct {
	prev      [2][]byte
	prevValid [2]bool

	// codec, nlcNear and nlcPack are learned from SetInit. The zero value
	// (SetInit never called) is codecRaw, which never matches codecNLC, so
	// Decode falls back to today's behaviour: raw or LZ4 selected by
	// FieldEvent.Header.Compressed alone.
	codec   byte
	nlcNear int
	nlcPack nlc.Pack

	// width and fieldHeight, from SetDims, are the one-field dimensions
	// NLC decoding needs; NLC params aren't on the wire (design §1.4).
	width, fieldHeight int
}

// NewFieldDecoder returns a fresh decoder with no previous-field history.
func NewFieldDecoder() *FieldDecoder {
	return &FieldDecoder{}
}

// SetInit records the session's codec, and for NLC its NEAR and pack, from
// a Groovy INIT's byte[1] — the same bitfield groovy.NLCCompressionByte
// builds: bits[1:0] the codec (0 raw, 1 LZ4, 2 NLC); NLC only: bits[3:2]
// NEAR, bit[7] pack (1 = Rice, 0 = Tiled). Call once per session, from the
// INIT command. Without a call, Decode behaves exactly as before SetInit
// existed.
func (d *FieldDecoder) SetInit(initByte byte) {
	d.codec = initByte & 3
	d.nlcNear = int((initByte >> 2) & 3)
	d.nlcPack = nlc.PackTiled
	if initByte&(1<<7) != 0 {
		d.nlcPack = nlc.PackRice
	}
}

// SetDims records the one-field width and height, used only when the
// session codec is NLC. Call after SWITCHRES (the same one-field
// dimensions cmd/fake-mister's fieldDims already computes), before the
// first field of a session that may use NLC.
func (d *FieldDecoder) SetDims(width, fieldHeight int) {
	d.width = width
	d.fieldHeight = fieldHeight
}

// Decode returns the raw BGR bytes for one FieldEvent. fieldBytes is the
// expected uncompressed payload size (width * fieldHeight * bytesPerPixel)
// — same value the listener uses for RAW BLITs. Compressed and delta
// payloads are detected via fe.Header; a compressed payload decodes as NLC
// when SetInit recorded codec 2, LZ4 otherwise. After a successful decode
// the reconstructed bytes are stored as the new previous field for the
// matching polarity.
//
// The returned slice aliases an internal buffer; callers that need to
// retain the bytes across the next Decode call must copy.
func (d *FieldDecoder) Decode(fe FieldEvent, fieldBytes int) ([]byte, error) {
	if fieldBytes <= 0 {
		return nil, fmt.Errorf("invalid fieldBytes: %d", fieldBytes)
	}
	var raw []byte
	if fe.Header.Compressed {
		if d.codec == codecNLC {
			out := make([]byte, fieldBytes)
			params := nlc.Params{Width: d.width, Height: d.fieldHeight, Near: d.nlcNear, Pack: d.nlcPack}
			if err := nlc.Decode(out, fe.Payload, params); err != nil {
				return nil, fmt.Errorf("nlc decode: %w", err)
			}
			raw = out
		} else {
			out, err := groovy.LZ4Decompress(fe.Payload, fieldBytes)
			if err != nil {
				return nil, fmt.Errorf("lz4 decompress: %w", err)
			}
			raw = out
		}
	} else {
		if len(fe.Payload) != fieldBytes {
			return nil, fmt.Errorf("raw payload size mismatch: got %d, want %d", len(fe.Payload), fieldBytes)
		}
		raw = fe.Payload
	}

	idx := int(fe.Header.Field & 1)
	if fe.Header.Delta {
		if !d.prevValid[idx] {
			return nil, fmt.Errorf("delta payload but no previous field for polarity %d", idx)
		}
		if len(d.prev[idx]) != len(raw) {
			return nil, fmt.Errorf("prev field size %d != delta size %d", len(d.prev[idx]), len(raw))
		}
		for i := range raw {
			raw[i] += d.prev[idx][i]
		}
	}

	if len(d.prev[idx]) != len(raw) {
		d.prev[idx] = make([]byte, len(raw))
	}
	copy(d.prev[idx], raw)
	d.prevValid[idx] = true
	return raw, nil
}
