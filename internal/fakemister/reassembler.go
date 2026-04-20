package fakemister

// Reassembler accumulates a fixed-size payload from a stream of UDP datagram
// chunks. Groovy BLIT and AUDIO payloads have no per-chunk header and no
// sequence number — the receiver trusts arrival order and simply concatenates
// bytes until the expected size is reached.
type Reassembler struct {
	expected uint32
	got      uint32
	buf      []byte
}

// NewReassembler returns a Reassembler that expects exactly expectedSize
// bytes. Pre-allocates the backing buffer to avoid reallocs during append.
func NewReassembler(expectedSize uint32) *Reassembler {
	return &Reassembler{expected: expectedSize, buf: make([]byte, 0, expectedSize)}
}

// Write appends a chunk. Returns true once the expected number of bytes has
// been received. Additional bytes beyond expected are dropped (the tail of
// the last chunk is truncated).
func (r *Reassembler) Write(chunk []byte) bool {
	remaining := r.expected - r.got
	if uint32(len(chunk)) > remaining {
		chunk = chunk[:remaining]
	}
	r.buf = append(r.buf, chunk...)
	r.got += uint32(len(chunk))
	return r.got >= r.expected
}

// Bytes returns the accumulated payload. Safe to call before the Reassembler
// reports done; in that case the slice is short.
func (r *Reassembler) Bytes() []byte { return r.buf }
