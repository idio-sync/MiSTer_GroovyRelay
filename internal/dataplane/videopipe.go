package dataplane

import (
	"io"
)

// ReadFieldsFromPipe reads fixed-size raw RGB fields from r (the FFmpeg video
// stdout / fd-3 pipe) and sends each complete field on out. The Phase 5 filter
// chain ends with `separatefields`, so every width*height*bytesPerPixel bytes
// on the wire is exactly ONE field at 59.94 Hz.
//
// Closes out on EOF or any read error (including a truncated final field).
// Intended to run as a goroutine; lifetime is bounded by the upstream reader.
func ReadFieldsFromPipe(r io.Reader, width, height, bytesPerPixel int, out chan<- []byte) {
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
