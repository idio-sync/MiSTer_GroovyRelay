package dataplane

// FrameBuf wraps a session-lifetime byte slice for a single video frame.
// The Data slice is allocated once when the FrameBuf is constructed by
// NewFramePool; callers reuse it across many fills via FramePool.Get and
// FramePool.Put. N records the number of bytes the most recent reader
// filled — the data plane's tick loop slices Data[:N] for downstream use.
//
// Lifetime: a *FrameBuf either belongs to the pool's free queue, OR is
// held exclusively by exactly one of (the reader filling it, the videoCh
// in transit, the tick loop processing it). The producer/consumer
// invariant is: ReadFramesFromPipePooled holds at most one *FrameBuf at
// any time outside the pool, and the tick loop returns the buffer to the
// pool immediately after sendField returns.
type FrameBuf struct {
	Data []byte
	N    int
}

// FramePool is a fixed-capacity, channel-based free queue of *FrameBuf.
// It is preloaded with `slots` buffers at construction; Get blocks if
// the pool is empty (i.e., all buffers are in flight). Put returns a
// buffer to the pool. The pool channel is never closed; the pool is
// GC'd along with its owning Plane.
//
// Why channel-based and not sync.Pool: sync.Pool is GC-aware and can
// drain its contents under memory pressure, forcing fresh allocations
// exactly when the system is already strained. For a 60 Hz hard-real-
// time pipeline that's the wrong semantic.
type FramePool struct {
	free chan *FrameBuf
}

// NewFramePool allocates `slots` *FrameBuf, each carrying a
// `frameBytes`-sized Data slice, and preloads them into the free queue.
// All allocation happens here; no further allocation should occur via
// Get/Put across the pool's lifetime.
func NewFramePool(slots, frameBytes int) *FramePool {
	p := &FramePool{free: make(chan *FrameBuf, slots)}
	for i := 0; i < slots; i++ {
		p.free <- &FrameBuf{Data: make([]byte, frameBytes)}
	}
	return p
}

// Get returns a free buffer. Blocks if the pool is empty until Put is
// called by another goroutine.
func (p *FramePool) Get() *FrameBuf { return <-p.free }

// Put returns a buffer to the pool. Non-blocking: the pool channel is
// sized exactly to the slot count so Put never blocks under correct
// usage. A blocking Put indicates a programmer error (returning a buffer
// that wasn't from this pool, or returning the same buffer twice).
func (p *FramePool) Put(b *FrameBuf) { p.free <- b }
