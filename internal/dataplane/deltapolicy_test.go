package dataplane

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

const policyFieldBytes = 720 * 240 * 3

func newPolicyPlane(t *testing.T) (*Plane, *scriptedFieldSender) {
	t.Helper()
	t.Setenv("GROOVY_DELTA_LZ4", "1")
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender
	return p, sender
}

// grainField is compressible on its own (a repeated tile) but shares
// nothing with its predecessor, like film grain or sensor noise: the delta
// is as random as the field, so delta can never beat full LZ4 by 5%.
func grainField(seed int64) []byte {
	tile := make([]byte, 4096)
	rand.New(rand.NewSource(seed)).Read(tile)
	return repeatedTileField(policyFieldBytes, tile)
}

func lastType(s *scriptedFieldSender) int { return blitPayloadType(s.headers[len(s.headers)-1]) }

// On content where delta keeps losing, stop paying for the delta attempt
// (~2.5 ms/field on a laptop, several times that on an Atom): back off,
// re-probe periodically, and keep sending correct full LZ4.
func TestDeltaPolicy_BacksOffDeltaAttemptsWhileDeltaLoses(t *testing.T) {
	p, s := newPolicyPlane(t)
	frame := uint32(0)
	send := func(seed int64) {
		frame += 2
		p.sendField(frame, 0, grainField(seed))
		if got := lastType(s); got != groovy.BlitHeaderLZ4 {
			t.Fatalf("frame %d: payload type %d, want full LZ4 on grain", frame, got)
		}
	}

	send(1) // seeds history
	for i := 0; i < deltaBackoffAfterLosses; i++ {
		send(int64(10 + i))
	}
	if p.deltaLZ4Attempts != deltaBackoffAfterLosses {
		t.Fatalf("delta attempts = %d, want %d before backoff", p.deltaLZ4Attempts, deltaBackoffAfterLosses)
	}
	for i := 0; i < deltaBackoffFields; i++ {
		send(int64(100 + i))
	}
	if p.deltaLZ4Attempts != deltaBackoffAfterLosses {
		t.Fatalf("delta attempts = %d during backoff, want none added", p.deltaLZ4Attempts)
	}
	send(500) // backoff expired: one re-probe, loses, backs off again at once
	send(501)
	if p.deltaLZ4Attempts != deltaBackoffAfterLosses+1 {
		t.Fatalf("delta attempts = %d, want exactly one re-probe", p.deltaLZ4Attempts)
	}
}

// While delta keeps winning, the full compression is skipped: the delta is
// compared against the last full size for that polarity instead.
func TestDeltaPolicy_SkipsFullCompressionWhileDeltaWins(t *testing.T) {
	p, s := newPolicyPlane(t)
	cur := repeatedTileField(policyFieldBytes, deterministicTile(4096))
	p.sendField(1, 0, cur) // seed: full
	cur = nextSmallDeltaField(cur, 1)
	p.sendField(3, 0, cur) // first delta: full computed to compare
	fullBefore := p.fullLZ4Attempts

	prev := cur
	for i := 0; i < 10; i++ {
		cur = nextSmallDeltaField(prev, byte(i+2))
		p.sendField(uint32(5+2*i), 0, cur)
		if got := lastType(s); got != groovy.BlitHeaderLZ4Delta {
			t.Fatalf("field %d: payload type %d, want delta", i, got)
		}
		got, err := groovy.LZ4Decompress(s.payloads[len(s.payloads)-1], policyFieldBytes)
		if err != nil {
			t.Fatalf("decompress delta %d: %v", i, err)
		}
		want := make([]byte, policyFieldBytes)
		writeFieldSubDeltaInto(want, cur, prev)
		if !bytes.Equal(got, want) {
			t.Fatalf("delta %d does not decode to cur-prev", i)
		}
		prev = cur
	}
	if p.fullLZ4Attempts != fullBefore {
		t.Fatalf("full LZ4 attempts rose by %d while delta kept winning, want 0", p.fullLZ4Attempts-fullBefore)
	}
}

// A scene change while delta has been winning: the delta loses against the
// remembered full size, so the full field is compressed and sent.
func TestDeltaPolicy_SceneChangeFallsBackToFreshFull(t *testing.T) {
	p, s := newPolicyPlane(t)
	cur := repeatedTileField(policyFieldBytes, deterministicTile(4096))
	p.sendField(1, 0, cur)
	for i := 0; i < 3; i++ {
		cur = nextSmallDeltaField(cur, byte(i+1))
		p.sendField(uint32(3+2*i), 0, cur)
	}
	fullBefore := p.fullLZ4Attempts

	cut := grainField(42)
	p.sendField(99, 0, cut)
	if got := lastType(s); got != groovy.BlitHeaderLZ4 {
		t.Fatalf("scene change: payload type %d, want full LZ4", got)
	}
	if p.fullLZ4Attempts != fullBefore+1 {
		t.Fatalf("full attempts = %d, want one fresh full compression", p.fullLZ4Attempts-fullBefore)
	}
	got, err := groovy.LZ4Decompress(s.payloads[len(s.payloads)-1], policyFieldBytes)
	if err != nil || !bytes.Equal(got, cut) {
		t.Fatalf("scene-change payload does not decode to the new field (err=%v)", err)
	}
}

// The polarities are independent: grain on one field parity must not stop
// delta on the other.
func TestDeltaPolicy_BackoffIsPerPolarity(t *testing.T) {
	p, s := newPolicyPlane(t)
	clean := repeatedTileField(policyFieldBytes, deterministicTile(4096))
	p.sendField(1, 1, clean)
	p.sendField(2, 0, grainField(1))
	for i := 0; i < deltaBackoffAfterLosses+2; i++ {
		p.sendField(uint32(4+2*i), 0, grainField(int64(10+i)))
	}
	clean = nextSmallDeltaField(clean, 9)
	p.sendField(99, 1, clean)
	if got := lastType(s); got != groovy.BlitHeaderLZ4Delta {
		t.Fatalf("polarity 1 payload type %d, want delta despite polarity 0 backoff", got)
	}
}
