package dataplane

import (
	"testing"
)

func TestFramePool_GetPutRoundTrip(t *testing.T) {
	const slots = 4
	const frameBytes = 1000
	p := NewFramePool(slots, frameBytes)

	// Drain pool, verify N distinct buffers, each correctly sized.
	bufs := make([]*FrameBuf, slots)
	seen := make(map[*FrameBuf]struct{}, slots)
	for i := 0; i < slots; i++ {
		bufs[i] = p.Get()
		if len(bufs[i].Data) != frameBytes {
			t.Errorf("slot %d Data len = %d, want %d", i, len(bufs[i].Data), frameBytes)
		}
		if _, dup := seen[bufs[i]]; dup {
			t.Errorf("duplicate buffer pointer at slot %d", i)
		}
		seen[bufs[i]] = struct{}{}
	}

	// Return them, then drain again — should get the same N pointers back
	// (in some order — channel doesn't guarantee FIFO across reads).
	for _, b := range bufs {
		p.Put(b)
	}
	seenBack := make(map[*FrameBuf]struct{}, slots)
	for i := 0; i < slots; i++ {
		b := p.Get()
		seenBack[b] = struct{}{}
	}
	if len(seenBack) != slots {
		t.Errorf("got %d distinct buffers on second drain, want %d", len(seenBack), slots)
	}
	for b := range seen {
		if _, ok := seenBack[b]; !ok {
			t.Error("buffer disappeared between rounds")
		}
	}
}

func TestFramePool_ZeroAllocsAfterConstruction(t *testing.T) {
	p := NewFramePool(4, 1000)
	// Warmup
	b := p.Get()
	p.Put(b)
	got := testing.AllocsPerRun(100, func() {
		b := p.Get()
		p.Put(b)
	})
	if got != 0 {
		t.Errorf("Get/Put cycle allocs/op = %v, want 0", got)
	}
}
