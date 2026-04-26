package dataplane

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// TestAudioPipeReader_EmitsPerFieldChunks verifies ReadAudioFromPipe slices
// its upstream reader into chunks sized to match the 59.94 Hz field cadence.
// Uses NextChunkSize to get the exact integer size the implementation computes.
func TestAudioPipeReader_EmitsPerFieldChunks(t *testing.T) {
	r := NewAudioPipeReader(48000, 2, groovy.NTSC480i60)
	buf := &bytes.Buffer{}
	var chunks []int
	for i := 0; i < 3; i++ {
		chunk := r.NextChunkSize()
		if chunk <= 0 {
			t.Fatalf("unexpected chunk size %d", chunk)
		}
		chunks = append(chunks, chunk)
		c := make([]byte, chunk)
		for j := range c {
			c[j] = byte(i + 1)
		}
		buf.Write(c)
		r.Advance(chunk)
	}
	ch := make(chan []byte, 4)
	go ReadAudioFromPipe(buf, 48000, 2, groovy.NTSC480i60, ch)
	for i := 0; i < 3; i++ {
		select {
		case c := <-ch:
			if len(c) != chunks[i] {
				t.Errorf("chunk %d size = %d, want %d", i, len(c), chunks[i])
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
// for the canonical 48 kHz s16le stereo path. Chunks must stay aligned to
// 16-bit stereo sample frames, so the sequence alternates 3200/3204 bytes
// instead of producing odd byte counts like 3203.
func TestAudioPipeReader_ChunkSize48kStereo(t *testing.T) {
	r := NewAudioPipeReader(48000, 2, groovy.NTSC480i60)
	got1 := r.NextChunkSize()
	r.Advance(got1)
	got2 := r.NextChunkSize()
	if got1 != 3200 {
		t.Errorf("field 1 chunk = %d, want 3200", got1)
	}
	if got2 != 3204 {
		t.Errorf("field 2 chunk = %d, want 3204", got2)
	}
	if got1%4 != 0 || got2%4 != 0 {
		t.Errorf("stereo chunks must stay sample-frame aligned: got %d and %d", got1, got2)
	}
}

// TestAudioPipeReader_ClosesOnEOF confirms out is closed when the upstream
// hits EOF so a consumer can detect end of stream.
func TestAudioPipeReader_ClosesOnEOF(t *testing.T) {
	r := NewAudioPipeReader(48000, 2, groovy.NTSC480i60)
	chunk := r.NextChunkSize()
	rd := bytes.NewReader(make([]byte, chunk))
	ch := make(chan []byte, 2)
	done := make(chan struct{})
	go func() {
		ReadAudioFromPipe(rd, 48000, 2, groovy.NTSC480i60, ch)
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
		ReadAudioFromPipe(r, 48000, 2, groovy.NTSC480i60, ch)
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

// TestAudioPipeReader_IntegerExactCumulative verifies the reader consumes an
// integer-exact number of PCM sample frames against the NTSC field cadence.
// Regression harness for the old byte-based chunker, which emitted odd-sized
// bursts and could split 16-bit stereo sample frames across AUDIO commands.
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
			r := NewAudioPipeReader(tc.sampleRate, tc.channels, groovy.NTSC480i60)
			var total int64
			bytesPerFrame := int64(tc.channels * bytesPerSample)
			for i := int64(0); i < tc.fields; i++ {
				chunk := r.NextChunkSize()
				if chunk%int(bytesPerFrame) != 0 {
					t.Fatalf("chunk %d not sample-frame aligned: %d", i, chunk)
				}
				total += int64(chunk)
				r.Advance(chunk)
			}
			wantFrames := tc.fields * int64(tc.sampleRate) * 1001 / 60000
			want := wantFrames * bytesPerFrame
			if total != want {
				t.Errorf("sampleRate=%d channels=%d fields=%d: got cumulative=%d want=%d",
					tc.sampleRate, tc.channels, tc.fields, total, want)
			}
		})
	}
}

func TestAudioPipeReader_RateFromModeline(t *testing.T) {
	const sampleRate = 48000
	const channels = 2

	cases := []struct {
		name      string
		ml        groovy.Modeline
		ticks     int
		wantBytes int // total bytes consumed by `ticks` calls
	}{
		{
			name:  "NTSC_480i 60 ticks = 192192 bytes",
			ml:    groovy.NTSC480i60,
			ticks: 60,
			// Integer exact: floor(60 × 48000 × 1001 / 60000) × 4 = 48048 frames × 4 bytes = 192192 bytes
			wantBytes: 192192,
		},
		{
			name: "PAL_576i 50 ticks = 192000 bytes (one second exact)",
			ml: groovy.Modeline{
				PClock: 13.500, HActive: 720, HBegin: 732, HEnd: 795, HTotal: 864,
				VActive: 576, VBegin: 580, VEnd: 585, VTotal: 625, Interlace: 1,
			},
			ticks: 50,
			// 50 ticks × (48000 × 2 × 2 / 50) = 50 × 3840 = 192000 bytes
			wantBytes: 192000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewAudioPipeReader(sampleRate, channels, c.ml)
			total := 0
			for i := 0; i < c.ticks; i++ {
				size := r.NextChunkSize()
				total += size
				r.Advance(size)
			}
			if total != c.wantBytes {
				t.Errorf("after %d ticks total bytes = %d, want %d (drift %d)",
					c.ticks, total, c.wantBytes, total-c.wantBytes)
			}
		})
	}
}

func TestAudioPipeReader_PALPerTickSize(t *testing.T) {
	// PAL 50 Hz × stereo × 48000 Hz: each tick is exactly 3840 bytes
	// because 48000 / 50 = 960 sample-frames; 960 × 2 channels × 2
	// bytes/sample = 3840.
	pal := groovy.Modeline{
		PClock: 13.500, HActive: 720, HBegin: 732, HEnd: 795, HTotal: 864,
		VActive: 576, VBegin: 580, VEnd: 585, VTotal: 625, Interlace: 1,
	}
	r := NewAudioPipeReader(48000, 2, pal)
	for i := 0; i < 5; i++ {
		got := r.NextChunkSize()
		if got != 3840 {
			t.Errorf("tick %d: NextChunkSize() = %d, want 3840", i, got)
		}
		r.Advance(got)
	}
}
