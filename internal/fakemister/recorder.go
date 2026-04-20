package fakemister

import (
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// RecorderSnapshot is an immutable copy of a Recorder's counters at a moment
// in time. Safe to serialize (JSON-compatible modulo time.Time handling).
type RecorderSnapshot struct {
	Counts     map[byte]int
	AudioBytes int
	FirstSeen  time.Time
	LastSeen   time.Time
	Sequence   []byte // command type sequence
	// FieldTimestamps records the arrival time of each BLIT_FIELD_VSYNC
	// command (one entry per BLIT, in arrival order). Used by integration
	// tests to assert the inter-field gap stays within the ~17 ms band
	// implied by 59.94 Hz — see tests/integration's assertInterFieldTiming.
	FieldTimestamps []time.Time
}

// Recorder tracks per-command-type counts, reassembled-audio byte totals, and
// the full command-type arrival sequence. Safe for concurrent Record calls
// from the listener goroutine plus Snapshot calls from test/assertion code.
type Recorder struct {
	mu              sync.Mutex
	counts          map[byte]int
	audioBytes      int
	firstSeen       time.Time
	lastSeen        time.Time
	sequence        []byte
	fieldTimestamps []time.Time
}

// NewRecorder returns a zero-state Recorder with the counts map pre-allocated.
func NewRecorder() *Recorder {
	return &Recorder{counts: make(map[byte]int)}
}

// Record accumulates stats for a single command. AudioPayload.PCM length is
// added to AudioBytes when present — callers pass reassembled audio via
// AudioPayload, not AudioHeader (the header only carries soundSize metadata).
func (r *Recorder) Record(c Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.firstSeen.IsZero() {
		r.firstSeen = now
	}
	r.lastSeen = now
	r.counts[c.Type]++
	r.sequence = append(r.sequence, c.Type)
	if c.Type == groovy.CmdBlitFieldVSync {
		r.fieldTimestamps = append(r.fieldTimestamps, now)
	}
	if c.AudioPayload != nil {
		r.audioBytes += len(c.AudioPayload.PCM)
	}
}

// Snapshot returns a deep copy of the recorder's current state. The maps and
// slices in the result are independent of the recorder's internal state.
func (r *Recorder) Snapshot() RecorderSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := make(map[byte]int, len(r.counts))
	for k, v := range r.counts {
		counts[k] = v
	}
	seq := make([]byte, len(r.sequence))
	copy(seq, r.sequence)
	ts := make([]time.Time, len(r.fieldTimestamps))
	copy(ts, r.fieldTimestamps)
	return RecorderSnapshot{
		Counts:          counts,
		AudioBytes:      r.audioBytes,
		FirstSeen:       r.firstSeen,
		LastSeen:        r.lastSeen,
		Sequence:        seq,
		FieldTimestamps: ts,
	}
}
