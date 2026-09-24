package dataplane

const (
	// fieldParityHistory is how many sent (frame, field) pairs are kept for
	// matching ACK echoes. ACKs trail the send by a field or two; 64 covers
	// ~1 s of receiver lag.
	fieldParityHistory = 64
	// fieldParityLockSamples is how many consecutive agreeing ACKs establish
	// (or change) the parity relation: ~0.5 s at NTSC field rate. Shorter
	// disagreements are flutter from ACKs sampled near a field boundary.
	fieldParityLockSamples = 30
)

// fieldParityTracker watches the relation between the field bit the relay
// sends and the raster field the FPGA reports (ACK status bit 5, vgaF1) for
// the echoed frame. While the sender's field cadence stays phase-locked to
// the CRT's raster, sent^vgaF1 is constant. A sustained change is a field
// slip: every subsequent field lands on the opposite raster field, showing
// as persistent 1-line shimmer until the operator flips tff/bff.
//
// Diagnostic only; nothing acts on it. Owned by the tick goroutine.
type fieldParityTracker struct {
	sent [fieldParityHistory]struct {
		frame uint32
		field uint8
		ok    bool
	}

	stable       int8 // locked relation (0/1); -1 before the first lock
	candidate    int8 // relation being accumulated toward a (re)lock; -1 none
	candidateRun int

	slips   uint64 // sustained relation changes after the first lock
	flutter uint64 // ACKs disagreeing with the locked relation
}

func newFieldParityTracker() *fieldParityTracker {
	return &fieldParityTracker{stable: -1, candidate: -1}
}

// recordSent notes the field bit carried by the BLIT for frame.
func (t *fieldParityTracker) recordSent(frame uint32, field uint8) {
	s := &t.sent[frame%fieldParityHistory]
	s.frame, s.field, s.ok = frame, field&1, true
}

// observe folds in one ACK that echoed frame while the FPGA was on raster
// field vgaF1. It reports the first lock and each later slip. Echoes of
// frames no longer in the history are ignored.
func (t *fieldParityTracker) observe(frame uint32, vgaF1 bool) (locked, slipped bool) {
	s := t.sent[frame%fieldParityHistory]
	if !s.ok || s.frame != frame {
		return false, false
	}
	rel := int8(s.field)
	if vgaF1 {
		rel ^= 1
	}
	if rel == t.stable {
		t.candidate, t.candidateRun = -1, 0
		return false, false
	}
	if t.stable >= 0 {
		t.flutter++
	}
	if rel == t.candidate {
		t.candidateRun++
	} else {
		t.candidate, t.candidateRun = rel, 1
	}
	if t.candidateRun < fieldParityLockSamples {
		return false, false
	}
	prev := t.stable
	t.stable, t.candidate, t.candidateRun = rel, -1, 0
	if prev < 0 {
		return true, false
	}
	t.slips++
	return false, true
}

// relation returns the locked sent^vgaF1 relation, if one is established.
func (t *fieldParityTracker) relation() (uint8, bool) {
	if t.stable < 0 {
		return 0, false
	}
	return uint8(t.stable), true
}
