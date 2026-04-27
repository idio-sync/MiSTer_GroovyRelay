package fakemister

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// FieldDecoder reconstructs the raw BGR bytes of a BLIT field, applying
// LZ4 decompression and the optional delta-against-previous-same-polarity-
// field reversal used by Plane.chooseBlitPayload (see internal/dataplane).
//
// The decoder keeps one previous-field buffer per field polarity (top vs.
// bottom) so that a delta-LZ4 payload can be XORed back into the actual
// field. Without this reversal the dumper sees an XOR delta where it
// expects pixels — a mostly-static frame round-trips as near-uniform
// black, defeating any downstream variance / image check.
//
// Not goroutine-safe: a FieldDecoder is owned by a single consumer of the
// FieldEvent channel and mutated in place.
type FieldDecoder struct {
	prev      [2][]byte
	prevValid [2]bool
}

// NewFieldDecoder returns a fresh decoder with no previous-field history.
func NewFieldDecoder() *FieldDecoder {
	return &FieldDecoder{}
}

// Decode returns the raw BGR bytes for one FieldEvent. fieldBytes is the
// expected uncompressed payload size (width * fieldHeight * bytesPerPixel)
// — same value the listener uses for RAW BLITs. Compressed and delta
// payloads are detected via fe.Header. After a successful decode the
// reconstructed bytes are stored as the new previous field for the
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
		out, err := groovy.LZ4Decompress(fe.Payload, fieldBytes)
		if err != nil {
			return nil, fmt.Errorf("lz4 decompress: %w", err)
		}
		raw = out
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
			raw[i] ^= d.prev[idx][i]
		}
	}

	if len(d.prev[idx]) != len(raw) {
		d.prev[idx] = make([]byte, len(raw))
	}
	copy(d.prev[idx], raw)
	d.prevValid[idx] = true
	return raw, nil
}
