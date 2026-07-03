package dataplane

// audioDelayRing holds PCM chunks for N field-periods before they are sent,
// compensating for the CRT's display latency relative to the FPGA's audio
// DAC (see the audio-delay commentary in Plane.Run). One chunk is pushed and
// at most one popped per field tick, both from the tick goroutine — the type
// is intentionally not safe for concurrent use.
//
// When the receiver reports not-ready for longer than the configured delay
// the ring fills; Push then sheds the OLDEST chunk to make room so the ring
// always holds the freshest audio. (Dropping the newest instead would freeze
// the ring's contents and replay ever-staler audio on recovery.) Shed chunks
// are counted in Drops for telemetry — each one is audible content lost to
// receiver backpressure.
type audioDelayRing struct {
	buf   [][]byte
	head  int
	n     int
	drops uint64
}

// newAudioDelayRing returns a ring holding delayFields+1 slots: delayFields
// chunks in flight plus the one pushed this tick. delayFields=0 collapses to
// "send the chunk read this tick."
func newAudioDelayRing(delayFields int) *audioDelayRing {
	if delayFields < 0 {
		delayFields = 0
	}
	return &audioDelayRing{buf: make([][]byte, delayFields+1)}
}

// Push appends pcm at the tail. If the ring is full the oldest chunk is
// dropped to make room; the return value reports that drop so the caller
// can log it.
func (r *audioDelayRing) Push(pcm []byte) (droppedOldest bool) {
	if r.n == len(r.buf) {
		r.buf[r.head] = nil
		r.head = (r.head + 1) % len(r.buf)
		r.n--
		r.drops++
		droppedOldest = true
	}
	tail := (r.head + r.n) % len(r.buf)
	r.buf[tail] = pcm
	r.n++
	return droppedOldest
}

// PopIfBeyond removes and returns the oldest chunk when more than delayN
// chunks are held, or nil while the ring is still filling its delay budget.
func (r *audioDelayRing) PopIfBeyond(delayN int) []byte {
	if r.n <= delayN {
		return nil
	}
	oldest := r.buf[r.head]
	r.buf[r.head] = nil
	r.head = (r.head + 1) % len(r.buf)
	r.n--
	return oldest
}

// Len returns the number of chunks currently held.
func (r *audioDelayRing) Len() int { return r.n }

// Cap returns the total slot count (delayFields + 1).
func (r *audioDelayRing) Cap() int { return len(r.buf) }

// Drops returns the cumulative count of oldest-chunk drops from Push.
func (r *audioDelayRing) Drops() uint64 { return r.drops }
