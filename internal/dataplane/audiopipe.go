package dataplane

import (
	"io"
)

// bytesPerSample is the s16le output format the FFmpeg audio pipe produces
// (see BuildCommand: `-f s16le`). 16-bit little-endian = 2 bytes per sample
// per channel.
const bytesPerSample = 2

// AudioPipeReader computes per-field PCM chunk sizes using integer-exact
// arithmetic against the NTSC 60000/1001 Hz field rate, while preserving
// whole PCM sample frames. One reader per session; caller iterates by
// calling NextChunkSize, reading that many bytes from the pipe, then Advance
// to account for the bytes actually read.
type AudioPipeReader struct {
	sampleRate       int
	channels         int
	fieldsRead       int64
	sampleFramesRead int64
	lastSize         int // size returned by the most recent NextChunkSize call
}

// NewAudioPipeReader returns a reader seeded at field 0, bytes 0.
func NewAudioPipeReader(sampleRate, channels int) *AudioPipeReader {
	return &AudioPipeReader{sampleRate: sampleRate, channels: channels}
}

// NextChunkSize returns the exact number of bytes the caller should read from
// the audio pipe for the NEXT field tick, preserving whole PCM sample frames.
// Derived from the integer formula:
// floor((fieldsRead+1) * sampleRate * 1001 / 60000) - sampleFramesRead
// and then scaled by channels * 2 bytes-per-sample. Never returns negative;
// if sampleRate*channels is zero (misconfigured), returns 0 so the caller can
// treat it as "no audio".
func (r *AudioPipeReader) NextChunkSize() int {
	bytesPerFrame := int64(r.channels) * int64(bytesPerSample)
	if r.sampleRate <= 0 || bytesPerFrame <= 0 {
		r.lastSize = 0
		return 0
	}
	expectedFrames := (r.fieldsRead + 1) * int64(r.sampleRate) * 1001 / 60000
	frames := expectedFrames - r.sampleFramesRead
	if frames < 0 {
		frames = 0
	}
	r.lastSize = int(frames * bytesPerFrame)
	return r.lastSize
}

// Advance records that `got` bytes were actually read in response to the
// most recent NextChunkSize call, and increments the field counter. `got`
// may be less than lastSize on a short read (EOF); the next call to
// NextChunkSize will compensate automatically.
func (r *AudioPipeReader) Advance(got int) {
	bytesPerFrame := r.channels * bytesPerSample
	if bytesPerFrame > 0 {
		r.sampleFramesRead += int64(got / bytesPerFrame)
	}
	r.fieldsRead++
}

// ReadAudioFromPipe reads PCM chunks sized by AudioPipeReader from r and
// sends each on out. Closes out on EOF or any read error (including a
// truncated tail).
//
// Chunk size averages to sampleRate*channels*2 / 59.94 but varies by one
// whole sample frame between ticks to keep cumulative consumption aligned
// against the 60000/1001 Hz field rate.
func ReadAudioFromPipe(r io.Reader, sampleRate, channels int, out chan<- []byte) {
	defer close(out)
	reader := NewAudioPipeReader(sampleRate, channels)
	for {
		size := reader.NextChunkSize()
		if size <= 0 {
			return
		}
		buf := make([]byte, size)
		n, err := io.ReadFull(r, buf)
		reader.Advance(n)
		if err != nil {
			return
		}
		out <- buf
	}
}
