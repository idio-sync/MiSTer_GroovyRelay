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
	// BlitFields is the per-blit Field byte (0 = top/progressive,
	// 1 = bottom). Populated for every CmdBlitFieldVSync command.
	// Integration tests use this to assert the field-bit pattern:
	// alternating 0/1 for interlaced modelines, all-0 for progressive
	// modelines.
	BlitFields []uint8
	// SwitchresRaw is the verbatim wire bytes of every SWITCHRES
	// command observed (one entry per CmdSwitchres). Each entry is a
	// fresh copy of Command.Raw so callers can compare against
	// groovy.BuildSwitchres(presetModeline) byte-for-byte without
	// worrying about the listener buffer being reused.
	SwitchresRaw [][]byte
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
	blitFields      []uint8
	switchresRaw    [][]byte
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
	// Prefer the listener's wire-arrival stamp so downstream processing stalls
	// (e.g. the PNG dumper in the variance test) can't corrupt inter-field
	// timing measurements. Fall back to time.Now() for synthesized commands
	// that never passed through the listener.
	now := c.ReceivedAt
	if now.IsZero() {
		now = time.Now()
	}
	if r.firstSeen.IsZero() {
		r.firstSeen = now
	}
	r.lastSeen = now
	r.counts[c.Type]++
	r.sequence = append(r.sequence, c.Type)
	if c.Type == groovy.CmdBlitFieldVSync {
		r.fieldTimestamps = append(r.fieldTimestamps, now)
		if c.Blit != nil {
			r.blitFields = append(r.blitFields, c.Blit.Field)
		}
	}
	if c.Type == groovy.CmdSwitchres && len(c.Raw) > 0 {
		// Copy because the listener may reuse its read buffer for
		// subsequent datagrams. Without the copy, a long-running
		// recorder would observe later writes overwriting earlier
		// SWITCHRES payloads it had already "captured".
		raw := make([]byte, len(c.Raw))
		copy(raw, c.Raw)
		r.switchresRaw = append(r.switchresRaw, raw)
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
	bf := make([]uint8, len(r.blitFields))
	copy(bf, r.blitFields)
	sr := make([][]byte, len(r.switchresRaw))
	for i, raw := range r.switchresRaw {
		sr[i] = make([]byte, len(raw))
		copy(sr[i], raw)
	}
	return RecorderSnapshot{
		Counts:          counts,
		AudioBytes:      r.audioBytes,
		FirstSeen:       r.firstSeen,
		LastSeen:        r.lastSeen,
		Sequence:        seq,
		FieldTimestamps: ts,
		BlitFields:      bf,
		SwitchresRaw:    sr,
	}
}
