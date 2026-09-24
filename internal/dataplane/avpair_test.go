package dataplane

import "testing"

func pcmChunk(k byte) []byte { return []byte{k, k} }

// audioQueue builds an audio channel preloaded with the given chunks.
func audioQueue(chunks ...[]byte) chan []byte {
	ch := make(chan []byte, 16)
	for _, c := range chunks {
		ch <- c
	}
	return ch
}

func TestAVPairer_PairsOneChunkPerVideoFrame(t *testing.T) {
	var p avPairer
	var prebuf [][]byte
	ch := audioQueue(pcmChunk(0), pcmChunk(1))
	for k := byte(0); k < 2; k++ {
		if got := p.next(true, &prebuf, ch); len(got) == 0 || got[0] != k {
			t.Fatalf("tick %d: got %v, want chunk %d", k, got, k)
		}
	}
}

// A duplicate-field tick shows no new video, so audio must not advance
// either; otherwise audio permanently leads video by one field per dup.
func TestAVPairer_DuplicateTickDoesNotConsumeAudio(t *testing.T) {
	var p avPairer
	var prebuf [][]byte
	ch := audioQueue(pcmChunk(0), pcmChunk(1))

	if got := p.next(true, &prebuf, ch); got[0] != 0 {
		t.Fatalf("frame 0 paired with chunk %d", got[0])
	}
	for i := 0; i < 3; i++ { // video stall: three duplicate fields
		if got := p.next(false, &prebuf, ch); got != nil {
			t.Fatalf("duplicate tick consumed audio chunk %v", got)
		}
	}
	if got := p.next(true, &prebuf, ch); len(got) == 0 || got[0] != 1 {
		t.Fatalf("frame 1 paired with %v, want chunk 1", got)
	}
}

// Video frames shown while their audio had not arrived: the late chunks are
// stale when they turn up and are dropped, so frame k plays with chunk k.
func TestAVPairer_DropsStaleAudioAfterAudioStall(t *testing.T) {
	var p avPairer
	var prebuf [][]byte
	ch := make(chan []byte, 16)

	for k := 0; k < 3; k++ { // frames 0..2 shown, audio stalled
		if got := p.next(true, &prebuf, ch); got != nil {
			t.Fatalf("got audio %v with an empty queue", got)
		}
	}
	// Audio recovers: chunks 0..3 arrive together; frame 3 is due.
	for k := byte(0); k <= 3; k++ {
		ch <- pcmChunk(k)
	}
	if got := p.next(true, &prebuf, ch); len(got) == 0 || got[0] != 3 {
		t.Fatalf("frame 3 paired with %v, want chunk 3", got)
	}
	if p.resyncDrops != 3 {
		t.Fatalf("resync drops = %d, want 3", p.resyncDrops)
	}
}

// Recovery can be partial: only some stale chunks have arrived by the next
// frame. The debt carries over instead of pairing a stale chunk.
func TestAVPairer_PartialRecoveryKeepsDebt(t *testing.T) {
	var p avPairer
	var prebuf [][]byte
	ch := make(chan []byte, 16)

	p.next(true, &prebuf, ch) // frame 0, no audio
	p.next(true, &prebuf, ch) // frame 1, no audio
	ch <- pcmChunk(0)         // only chunk 0 has arrived by frame 2
	if got := p.next(true, &prebuf, ch); got != nil {
		t.Fatalf("frame 2 paired with stale %v", got)
	}
	for k := byte(1); k <= 3; k++ {
		ch <- pcmChunk(k)
	}
	if got := p.next(true, &prebuf, ch); len(got) == 0 || got[0] != 3 {
		t.Fatalf("frame 3 paired with %v, want chunk 3", got)
	}
}

func TestAVPairer_PrebufferedChunksComeFirst(t *testing.T) {
	var p avPairer
	prebuf := [][]byte{pcmChunk(0)}
	ch := audioQueue(pcmChunk(1))
	if got := p.next(true, &prebuf, ch); got[0] != 0 {
		t.Fatalf("got chunk %d, want prebuffered chunk 0", got[0])
	}
	if got := p.next(true, &prebuf, ch); got[0] != 1 {
		t.Fatalf("got chunk %d, want 1", got[0])
	}
}
