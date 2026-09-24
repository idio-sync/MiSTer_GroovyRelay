package dataplane

import "testing"

// feedParity records frames [from, from+n) as sent with alternating field
// parity and ACKs each one with vgaF1 set so that sent^vgaF1 == relation.
func feedParity(tr *fieldParityTracker, from uint32, n int, relation uint8) (locks, slips int) {
	for i := 0; i < n; i++ {
		frame := from + uint32(i)
		field := uint8(frame & 1)
		tr.recordSent(frame, field)
		locked, slipped := tr.observe(frame, field^relation == 1)
		if locked {
			locks++
		}
		if slipped {
			slips++
		}
	}
	return locks, slips
}

func TestFieldParityTracker_LocksAfterConsistentSamples(t *testing.T) {
	tr := newFieldParityTracker()
	if _, ok := tr.relation(); ok {
		t.Fatal("tracker reports a relation before any samples")
	}
	locks, slips := feedParity(tr, 1, fieldParityLockSamples-1, 1)
	if locks != 0 || slips != 0 {
		t.Fatalf("locked early: locks=%d slips=%d", locks, slips)
	}
	locks, slips = feedParity(tr, fieldParityLockSamples, 1, 1)
	if locks != 1 || slips != 0 {
		t.Fatalf("locks=%d slips=%d, want one lock and no slip", locks, slips)
	}
	if rel, ok := tr.relation(); !ok || rel != 1 {
		t.Fatalf("relation = %d/%v, want 1/true", rel, ok)
	}
}

func TestFieldParityTracker_BriefFlutterIsNotASlip(t *testing.T) {
	tr := newFieldParityTracker()
	feedParity(tr, 1, fieldParityLockSamples, 0)

	// ACKs landing near a field boundary sample vgaF1 on either side of it.
	_, slips := feedParity(tr, 100, 5, 1)
	if slips != 0 {
		t.Fatalf("slips = %d after 5 disagreeing samples, want 0", slips)
	}
	feedParity(tr, 105, 10, 0)
	if got := tr.flutter; got != 5 {
		t.Fatalf("flutter = %d, want 5", got)
	}
	if rel, _ := tr.relation(); rel != 0 {
		t.Fatalf("relation = %d after flutter, want 0", rel)
	}
}

func TestFieldParityTracker_SustainedChangeIsOneSlip(t *testing.T) {
	tr := newFieldParityTracker()
	feedParity(tr, 1, fieldParityLockSamples, 0)

	locks, slips := feedParity(tr, 200, 3*fieldParityLockSamples, 1)
	if locks != 0 || slips != 1 {
		t.Fatalf("locks=%d slips=%d, want exactly one slip", locks, slips)
	}
	if tr.slips != 1 {
		t.Fatalf("slips total = %d, want 1", tr.slips)
	}
	if rel, _ := tr.relation(); rel != 1 {
		t.Fatalf("relation = %d, want 1", rel)
	}
}

func TestFieldParityTracker_IgnoresUnknownOrOverwrittenFrames(t *testing.T) {
	tr := newFieldParityTracker()
	tr.recordSent(5, 1)
	// Never sent.
	if locked, slipped := tr.observe(6, true); locked || slipped {
		t.Fatal("unknown frame produced an event")
	}
	// Frame 5's slot overwritten by a later frame sharing the index.
	tr.recordSent(5+fieldParityHistory, 0)
	tr.observe(5, true)
	if tr.candidateRun != 0 {
		t.Fatalf("stale echo counted toward lock: run=%d", tr.candidateRun)
	}
}
