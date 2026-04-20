package dataplane

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// TestAudioPipeReader_EmitsPerFieldChunks verifies ReadAudioFromPipe slices
// its upstream reader into chunks sized to match the 59.94 Hz field cadence.
// 48 kHz stereo 16-bit = 48000 * 2 * 2 / 59.94 ≈ 3203 bytes/field — we use
// AudioChunkSize to get the exact integer size the implementation computes.
func TestAudioPipeReader_EmitsPerFieldChunks(t *testing.T) {
	chunk := AudioChunkSize(48000, 2)
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
			if len(c) != chunk {
				t.Errorf("chunk %d size = %d, want %d", i, len(c), chunk)
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
// for the canonical 48 kHz s16le stereo path. Exactly matches the plan's
// formula: sampleRate * channels * bytesPerSample / 59.94.
func TestAudioPipeReader_ChunkSize48kStereo(t *testing.T) {
	got := AudioChunkSize(48000, 2)
	// 48000 * 2 * 2 = 192000 bytes/sec; at 59.94 fps → 3203 bytes/field.
	if got < 3190 || got > 3220 {
		t.Errorf("48kHz stereo chunk = %d, want ~3203", got)
	}
}

// TestAudioPipeReader_ClosesOnEOF confirms out is closed when the upstream
// hits EOF so a consumer can detect end of stream.
func TestAudioPipeReader_ClosesOnEOF(t *testing.T) {
	chunk := AudioChunkSize(48000, 2)
	r := bytes.NewReader(make([]byte, chunk))
	ch := make(chan []byte, 2)
	done := make(chan struct{})
	go func() {
		ReadAudioFromPipe(r, 48000, 2, ch)
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
