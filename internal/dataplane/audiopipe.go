package dataplane

import (
	"io"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// bytesPerSample is the s16le output format the FFmpeg audio pipe produces
// (see BuildCommand: `-f s16le`). 16-bit little-endian = 2 bytes per sample
// per channel.
const bytesPerSample = 2

// AudioPipeReader computes per-field PCM chunk sizes using integer-exact
// arithmetic against the modeline's field rate, while preserving whole PCM
// sample frames. One reader per session; caller iterates by calling
// NextChunkSize, reading that many bytes from the pipe, then Advance to
// account for the bytes actually read.
//
// The field rate is expressed as an integer rational rateNumer/rateDenom Hz
// (e.g. 60000/1001 for NTSC, 50/1 for PAL). This matches the formula used
// by FieldRateRatio on the Modeline.
type AudioPipeReader struct {
	sampleRate       int
	channels         int
	rateNumer        int64 // ml.FieldRateRatio() numer (60000 NTSC, 50 PAL)
	rateDenom        int64 // ml.FieldRateRatio() denom (1001 NTSC, 1 PAL)
	fieldsRead       int64
	sampleFramesRead int64
	lastSize         int // size returned by the most recent NextChunkSize call
}

// NewAudioPipeReader returns a reader seeded at field 0, bytes 0.
// The modeline's FieldRateRatio drives per-tick chunk sizing: NTSC
// (60000/1001 Hz) preserves the legacy "* 1001 / 60000" math; PAL
// (50/1 Hz) uses "* 1 / 50".
func NewAudioPipeReader(sampleRate, channels int, ml groovy.Modeline) *AudioPipeReader {
	rateNumer, rateDenom := ml.FieldRateRatio()
	if rateNumer <= 0 {
		rateNumer = 60000
		rateDenom = 1001
	}
	return &AudioPipeReader{
		sampleRate: sampleRate,
		channels:   channels,
		rateNumer:  rateNumer,
		rateDenom:  rateDenom,
	}
}

// NextChunkSize returns the exact number of bytes the caller should read from
// the audio pipe for the NEXT field tick, preserving whole PCM sample frames.
// Derived from the integer formula:
//
//	floor((fieldsRead+1) * sampleRate * rateDenom / rateNumer) - sampleFramesRead
//
// and then scaled by channels * 2 bytes-per-sample. Never returns negative;
// if sampleRate*channels is zero (misconfigured), returns 0 so the caller can
// treat it as "no audio".
func (r *AudioPipeReader) NextChunkSize() int {
	bytesPerFrame := int64(r.channels) * int64(bytesPerSample)
	if r.sampleRate <= 0 || bytesPerFrame <= 0 {
		r.lastSize = 0
		return 0
	}
	// expectedFrames = (fieldsRead+1) * sampleRate * rateDenom / rateNumer
	// With fieldRateHz = rateNumer/rateDenom Hz, integer floor gives exact
	// cumulative sample count without fractional accumulation.
	expectedFrames := (r.fieldsRead + 1) * int64(r.sampleRate) * r.rateDenom / r.rateNumer
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
// Chunk size averages to sampleRate*channels*2 / fieldRateHz but varies by
// one whole sample frame between ticks to keep cumulative consumption aligned
// against the modeline's field rate.
func ReadAudioFromPipe(r io.Reader, sampleRate, channels int, ml groovy.Modeline, out chan<- []byte) {
	defer close(out)
	reader := NewAudioPipeReader(sampleRate, channels, ml)
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
