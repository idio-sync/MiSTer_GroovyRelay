package dataplane

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// newDeltaTestPlane returns a delta-enabled plane with both field-history
// slots seeded, plus the fields that seeded them.
func newDeltaTestPlane(t *testing.T) (*Plane, *scriptedFieldSender, []byte, []byte) {
	t.Helper()
	t.Setenv("GROOVY_DELTA_LZ4", "1")
	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender
	f0 := repeatedTileField(fieldBytes, deterministicTile(4096))
	f1 := repeatedTileField(fieldBytes, deterministicTile(2048))
	p.sendField(1, 0, f0)
	p.sendField(2, 1, f1)
	return p, sender, f0, f1
}

// sendNextAndType sends the next small-delta field of the given polarity and
// returns the BLIT variant chosen for it.
func sendNextAndType(p *Plane, s *scriptedFieldSender, frame uint32, field uint8, prev []byte, seed byte) ([]byte, int) {
	next := nextSmallDeltaField(prev, seed)
	p.sendField(frame, field, next)
	return next, blitPayloadType(s.headers[len(s.headers)-1])
}

func TestDeltaResync_SanityDeltaFlowsWithoutEvents(t *testing.T) {
	p, s, f0, _ := newDeltaTestPlane(t)
	if _, typ := sendNextAndType(p, s, 3, 0, f0, 1); typ != groovy.BlitHeaderLZ4Delta {
		t.Fatalf("payload type = %d, want delta when nothing happened", typ)
	}
}

// A duplicate-field tick (underrun) resyncs both polarities: if the FPGA
// keys its delta base on its own frame counter rather than the header field
// bit, a dup would otherwise misalign every following delta.
func TestDeltaResync_DuplicateForcesFullFieldsForBothPolarities(t *testing.T) {
	p, s, f0, f1 := newDeltaTestPlane(t)
	p.sendDuplicate(3, 0)
	p.sendDuplicate(4, 1)
	p.sendDuplicate(5, 0)

	if _, typ := sendNextAndType(p, s, 6, 1, f1, 1); typ != groovy.BlitHeaderLZ4 {
		t.Fatalf("field 1 after dup run: type %d, want full LZ4", typ)
	}
	f0, typ := sendNextAndType(p, s, 7, 0, f0, 2)
	if typ != groovy.BlitHeaderLZ4 {
		t.Fatalf("field 0 after dup run: type %d, want full LZ4", typ)
	}
	if p.deltaResyncs != 1 {
		t.Fatalf("delta resyncs = %d, want 1 per dup run", p.deltaResyncs)
	}
	if _, typ := sendNextAndType(p, s, 9, 0, f0, 3); typ != groovy.BlitHeaderLZ4Delta {
		t.Fatalf("after resync: type %d, want delta to resume", typ)
	}
}

// The MiSTer ACKs every BLIT, so an echo that skips a frame (or goes
// backwards) means a BLIT or its ACK was lost; a lost BLIT leaves the
// receiver's buffer out of step with the sender's delta history.
func TestDeltaResync_EchoGapForcesFullFields(t *testing.T) {
	p, s, f0, _ := newDeltaTestPlane(t)
	p.noteEchoAdvance(5, 7, 10)
	if _, typ := sendNextAndType(p, s, 11, 0, f0, 1); typ != groovy.BlitHeaderLZ4 {
		t.Fatalf("after echo gap: type %d, want full LZ4", typ)
	}
	if p.deltaResyncs != 1 {
		t.Fatalf("delta resyncs = %d, want 1", p.deltaResyncs)
	}
}

func TestDeltaResync_ConsecutiveEchoIsNotAGap(t *testing.T) {
	p, s, f0, _ := newDeltaTestPlane(t)
	p.noteEchoAdvance(5, 6, 10)
	if _, typ := sendNextAndType(p, s, 11, 0, f0, 1); typ != groovy.BlitHeaderLZ4Delta {
		t.Fatalf("consecutive echo: type %d, want delta", typ)
	}
	p.noteEchoAdvance(0, 3, 12) // first echo of the session
	if p.deltaResyncs != 0 {
		t.Fatalf("delta resyncs = %d, want 0", p.deltaResyncs)
	}
}

func TestDeltaResync_BackwardEchoIsAGap(t *testing.T) {
	p, _, _, _ := newDeltaTestPlane(t)
	p.noteEchoAdvance(9, 8, 10)
	if p.deltaResyncs != 1 {
		t.Fatalf("delta resyncs = %d, want 1", p.deltaResyncs)
	}
}

// If the real receiver coalesces ACKs, every echo would look like a gap;
// the rate limit keeps delta mostly working in that case.
func TestDeltaResync_EchoGapResyncsAreRateLimited(t *testing.T) {
	p, s, f0, f1 := newDeltaTestPlane(t)
	p.noteEchoAdvance(5, 7, 10)
	f0, _ = sendNextAndType(p, s, 11, 0, f0, 1) // full, reseeds slot 0
	f1, _ = sendNextAndType(p, s, 12, 1, f1, 2) // full, reseeds slot 1

	p.noteEchoAdvance(7, 9, 10+deltaResyncMinTicks-1)
	if _, typ := sendNextAndType(p, s, 13, 0, f0, 3); typ != groovy.BlitHeaderLZ4Delta {
		t.Fatalf("gap inside rate-limit window: type %d, want delta", typ)
	}
	p.noteEchoAdvance(9, 11, 10+deltaResyncMinTicks)
	if p.deltaResyncs != 2 {
		t.Fatalf("delta resyncs = %d, want 2", p.deltaResyncs)
	}
}

func TestDeltaResync_NoopWhenDeltaDisabled(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "0")
	p := NewPlane(PlaneConfig{LZ4Enabled: true, FieldWidth: 8, FieldHeight: 2, BytesPerPixel: 3})
	p.fieldSender = &scriptedFieldSender{}
	p.sendDuplicate(1, 0)
	p.noteEchoAdvance(1, 5, 10)
	if p.deltaResyncs != 0 {
		t.Fatalf("delta resyncs = %d with delta disabled, want 0", p.deltaResyncs)
	}
}
