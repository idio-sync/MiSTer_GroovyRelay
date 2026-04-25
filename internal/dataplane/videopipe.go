package dataplane

import (
	"io"
)

// ReadFramesFromPipe reads fixed-size raw RGB frames from r (the FFmpeg video
// stdout / fd-3 pipe) and sends each complete frame on out. The FFmpeg filter
// chain emits full-height progressive frames at 59.94 Hz; the data plane then
// extracts one field's rows per tick.
//
// Closes out on EOF or any read error (including a truncated final field).
// Intended to run as a goroutine; lifetime is bounded by the upstream reader.
func ReadFramesFromPipe(r io.Reader, width, height, bytesPerPixel int, out chan<- []byte) {
	defer close(out)
	size := width * height * bytesPerPixel
	for {
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		out <- buf
	}
}

// ReadFramesFromPipePooled reads fixed-size raw frames from r into
// pool-supplied buffers and forwards each filled *FrameBuf on out. The
// frame size is determined by the pool's frameBytes (set at NewFramePool).
// Closes out on EOF or any read error.
//
// EOF semantics:
//   - Clean EOF (io.EOF or io.ErrUnexpectedEOF on a partial read): the
//     in-progress *FrameBuf is returned to the pool BEFORE close. We do
//     not emit a partial frame downstream — the data plane has no use
//     for one.
//   - All read errors are treated equivalently: return the in-flight
//     buffer to the pool, close out, exit.
//
// Pool ownership invariant: the reader holds at most one *FrameBuf
// outside the pool at any time. Together with videoCh's buffered
// capacity and the tick loop's at-most-one-buffer-in-progress, this
// bounds the worst-case in-flight count at videoChCap + 2.
//
// The pool channel is never closed; the pool is GC'd along with the
// Plane. The out channel is closed exactly once when the reader exits.
func ReadFramesFromPipePooled(r io.Reader, pool *FramePool, out chan<- *FrameBuf) {
	defer close(out)
	for {
		fb := pool.Get()
		n, err := io.ReadFull(r, fb.Data)
		if err != nil {
			// Both io.EOF and io.ErrUnexpectedEOF land here. Either way
			// we don't emit a partial frame; return the buffer and exit.
			pool.Put(fb)
			return
		}
		fb.N = n
		out <- fb
	}
}

// ExtractFieldFromFrame row-stripes a full-height BGR24 frame into one field.
// field=0 extracts even rows (top field), field=1 extracts odd rows (bottom
// field). The caller must pass the full progressive frame dimensions.
func ExtractFieldFromFrame(frame []byte, width, height, bytesPerPixel int, field uint8) []byte {
	rowSize := width * bytesPerPixel
	fieldHeight := height / 2
	out := make([]byte, rowSize*fieldHeight)
	srcRow := int(field & 1)
	for dstRow := 0; dstRow < fieldHeight; dstRow++ {
		srcStart := srcRow * rowSize
		srcEnd := srcStart + rowSize
		dstStart := dstRow * rowSize
		copy(out[dstStart:dstStart+rowSize], frame[srcStart:srcEnd])
		srcRow += 2
	}
	return out
}

// ExtractFieldFromFrameInto row-stripes a full-height frame into dst.
// dst MUST have len >= width*(height/2)*bytesPerPixel; the function
// overwrites that prefix and ignores any trailing bytes. field=0 extracts
// even rows (top), field=1 extracts odd rows (bottom). Same row-extraction
// math as ExtractFieldFromFrame; differs only in the caller-supplied dst.
func ExtractFieldFromFrameInto(dst, frame []byte, width, height, bytesPerPixel int, field uint8) {
	rowSize := width * bytesPerPixel
	fieldHeight := height / 2
	srcRow := int(field & 1)
	for dstRow := 0; dstRow < fieldHeight; dstRow++ {
		srcStart := srcRow * rowSize
		srcEnd := srcStart + rowSize
		dstStart := dstRow * rowSize
		copy(dst[dstStart:dstStart+rowSize], frame[srcStart:srcEnd])
		srcRow += 2
	}
}
