package dataplane

import (
	"bytes"
	"testing"
)

func chunk(b byte) []byte { return []byte{b, b, b} }

func TestAudioDelayRing_HoldsNFieldsBeforePopping(t *testing.T) {
	r := newAudioDelayRing(2) // delay 2 fields → capacity 3

	r.Push(chunk(1))
	if got := r.PopIfBeyond(2); got != nil {
		t.Fatalf("pop with 1 held = %v, want nil (delay not reached)", got)
	}
	r.Push(chunk(2))
	if got := r.PopIfBeyond(2); got != nil {
		t.Fatalf("pop with 2 held = %v, want nil (delay not reached)", got)
	}
	r.Push(chunk(3))
	if got := r.PopIfBeyond(2); !bytes.Equal(got, chunk(1)) {
		t.Fatalf("pop with 3 held = %v, want oldest chunk(1)", got)
	}
	if r.Len() != 2 {
		t.Fatalf("Len after pop = %d, want 2", r.Len())
	}
}

func TestAudioDelayRing_ZeroDelayPassesThrough(t *testing.T) {
	r := newAudioDelayRing(0) // capacity 1: send the chunk read this tick

	r.Push(chunk(9))
	if got := r.PopIfBeyond(0); !bytes.Equal(got, chunk(9)) {
		t.Fatalf("pop = %v, want chunk(9)", got)
	}
	if got := r.PopIfBeyond(0); got != nil {
		t.Fatalf("pop from empty ring = %v, want nil", got)
	}
}

// TestAudioDelayRing_DropsOldestWhenFull: when the receiver is not ready for
// longer than the configured delay, the ring must shed the STALEST chunk and
// keep the freshest audio, counting what it dropped. Dropping the newest
// (the old inline behavior) silently discarded live audio while preserving
// an ever-staler buffer, so recovery replayed old audio. Audit finding F14.
func TestAudioDelayRing_DropsOldestWhenFull(t *testing.T) {
	r := newAudioDelayRing(2) // capacity 3

	r.Push(chunk(1))
	r.Push(chunk(2))
	if dropped := r.Push(chunk(3)); dropped {
		t.Fatal("Push into non-full ring reported a drop")
	}
	if r.Drops() != 0 {
		t.Fatalf("Drops before overflow = %d, want 0", r.Drops())
	}

	if dropped := r.Push(chunk(4)); !dropped {
		t.Fatal("Push into full ring did not report a drop")
	}
	if r.Drops() != 1 {
		t.Fatalf("Drops after overflow = %d, want 1", r.Drops())
	}
	if r.Len() != 3 {
		t.Fatalf("Len after overflow push = %d, want 3 (still full)", r.Len())
	}

	// Oldest chunk(1) was shed; the survivors pop in order 2, 3, 4.
	for _, want := range []byte{2, 3, 4} {
		if got := r.PopIfBeyond(0); !bytes.Equal(got, chunk(want)) {
			t.Fatalf("pop = %v, want chunk(%d)", got, want)
		}
	}
}

func TestAudioDelayRing_CapReportsSlotCount(t *testing.T) {
	if got := newAudioDelayRing(4).Cap(); got != 5 {
		t.Fatalf("Cap = %d, want delay+1 = 5", got)
	}
}
