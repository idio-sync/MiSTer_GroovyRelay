package dataplane

import (
	"io"
)

// fieldsPerSecond is the NTSC 59.94 Hz field cadence (3 × 30000/1001).
// Kept local to the package so AudioChunkSize stays a pure int function.
const fieldsPerSecond = 59.94

// bytesPerSample is the s16le output format the FFmpeg audio pipe produces
// (see BuildCommand: `-f s16le`). 16-bit little-endian = 2 bytes per sample
// per channel.
const bytesPerSample = 2

// AudioChunkSize returns the integer PCM byte count the data plane reads per
// 59.94 Hz field tick. For the canonical 48 kHz stereo path:
//
//	48000 * 2 channels * 2 bytes/sample / 59.94 ≈ 3203 bytes/field.
//
// Integer truncation is fine — FFmpeg produces audio at the real rate and
// brief divergence is absorbed by the audio-ready ACK gate and the FPGA's
// downstream resampler.
func AudioChunkSize(sampleRate, channels int) int {
	return int(float64(sampleRate*channels*bytesPerSample) / fieldsPerSecond)
}

// ReadAudioFromPipe reads fixed-size PCM chunks (one per 59.94 Hz field) from
// r (the FFmpeg audio stdout / fd-4 pipe) and sends each chunk on out.
// Closes out on EOF or any read error (including a truncated tail).
//
// Chunk size is AudioChunkSize(sampleRate, channels). The caller is expected
// to forward at most one chunk per field tick; a full channel simply applies
// backpressure onto the reader (the pipe buffers in FFmpeg / kernel).
func ReadAudioFromPipe(r io.Reader, sampleRate, channels int, out chan<- []byte) {
	defer close(out)
	size := AudioChunkSize(sampleRate, channels)
	if size <= 0 {
		return
	}
	for {
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		out <- buf
	}
}
