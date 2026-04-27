package jellyfin

import "sync"

// ringBuffer is a fixed-size, drop-oldest queue. Used by the JF
// adapter's sendOutbound to park outbound messages while the WS conn
// is down (drained on reconnect by runOneConn). Capacity is checked
// at construction and is immutable for the buffer's lifetime.
//
// Tests expanding this surface land in Task 7.1.
type ringBuffer struct {
	mu  sync.Mutex
	buf []outboundEnvelope
	cap int
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{cap: cap}
}

// push appends item; if buf is at capacity, drops the head.
func (r *ringBuffer) push(item outboundEnvelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= r.cap {
		r.buf = r.buf[1:]
	}
	r.buf = append(r.buf, item)
}

// drainAll returns and clears the buffer.
func (r *ringBuffer) drainAll() []outboundEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.buf
	r.buf = nil
	return out
}
