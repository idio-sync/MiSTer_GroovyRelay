package dataplane

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// TestAudioPipeReader_EmitsPerFieldChunks verifies ReadAudioFromPipe slices
// its upstream reader into chunks sized to match the 59.94 Hz field cadence.
// Uses NextChunkSize to get the exact integer size the implementation computes.
func TestAudioPipeReader_EmitsPerFieldChunks(t *testing.T) {
	r := NewAudioPipeReader(48000, 2)
	chunk := r.NextChunkSize()
	if chunk <= 0 {
		t.Fatalf("unexpected chunk size %d", chunk)
	}
	// 3 chunks worth of distinct byte patterns.
	buf := &bytes.Buffer{}
	for i := 0; i < 3; i++ {
		c := make([]byte, chunk)
		for j := range c {
			c[j] = byte(i + 1)
		}
		buf.Write(c)
	}
	ch := make(chan []byte, 4)
	go ReadAudioFromPipe(buf, 48000, 2, ch)
	for i := 0; i < 3; i++ {
		select {
		case c := <-ch:
			if len(c) < chunk-1 || len(c) > chunk+1 {
				t.Errorf("chunk %d size = %d, want ~%d", i, len(c), chunk)
			}
			if c[0] != byte(i+1) {
				t.Errorf("chunk %d first byte = %d, want %d", i, c[0], i+1)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout on chunk %d", i)
		}
	}
}

// TestAudioPipeReader_ChunkSize48kStereo locks the expected PCM chunk size
// for the canonical 48 kHz s16le stereo path. Uses integer-exact formula:
// sampleRate * channels * bytesPerSample * 1001 / 60000 per field.
func TestAudioPipeReader_ChunkSize48kStereo(t *testing.T) {
	r := NewAudioPipeReader(48000, 2)
	got := r.NextChunkSize()
	// 48000 * 2 * 2 * 1001 / 60000 = 3203 bytes for field 1.
	if got < 3190 || got > 3220 {
		t.Errorf("48kHz stereo chunk = %d, want ~3203", got)
	}
}

// TestAudioPipeReader_ClosesOnEOF confirms out is closed when the upstream
// hits EOF so a consumer can detect end of stream.
func TestAudioPipeReader_ClosesOnEOF(t *testing.T) {
	r := NewAudioPipeReader(48000, 2)
	chunk := r.NextChunkSize()
	rd := bytes.NewReader(make([]byte, chunk))
	ch := make(chan []byte, 2)
	done := make(chan struct{})
	go func() {
		ReadAudioFromPipe(rd, 48000, 2, ch)
		close(done)
	}()
	if _, ok := <-ch; !ok {
		t.Fatal("expected one chunk before close")
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel close on EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for close")
	}
	<-done
}

// TestAudioPipeReader_ShortTailClosesClean verifies a partial final chunk
// closes out cleanly instead of emitting a malformed short slice.
func TestAudioPipeReader_ShortTailClosesClean(t *testing.T) {
	r := io.NopCloser(bytes.NewReader([]byte{0x01, 0x02, 0x03}))
	ch := make(chan []byte, 1)
	done := make(chan struct{})
	go func() {
		ReadAudioFromPipe(r, 48000, 2, ch)
		close(done)
	}()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("short tail should not emit")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout on short-tail close")
	}
	<-done
}

// TestAudioPipeReader_IntegerExactCumulative verifies the reader consumes
// exactly sampleRate * channels * 2 bytes per second (no drift) by integer
// math. Regression harness for I5 — old code rounded 3203.2 → 3203 and
// drifted ~53 B/sec.
func TestAudioPipeReader_IntegerExactCumulative(t *testing.T) {
	cases := []struct {
		sampleRate int
		channels   int
		fields     int64
	}{
		{48000, 2, 3596},   // ~60 s
		{48000, 2, 60_000}, // ~16.68 min
		{44100, 2, 3596},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			r := NewAudioPipeReader(tc.sampleRate, tc.channels)
			var total int64
			for i := int64(0); i < tc.fields; i++ {
				total += int64(r.NextChunkSize())
				r.Advance(r.lastSize)
			}
			want := tc.fields * int64(tc.sampleRate*tc.channels*2) * 1001 / 60000
			if total != want {
				t.Errorf("sampleRate=%d channels=%d fields=%d: got cumulative=%d want=%d",
					tc.sampleRate, tc.channels, tc.fields, total, want)
			}
		})
	}
}
