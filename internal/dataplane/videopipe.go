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
