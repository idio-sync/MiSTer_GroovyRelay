package dataplane

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// TestVideoPipeReader_EmitsFields verifies ReadFieldsFromPipe slices its
// upstream reader into width*height*bpp-sized fields and emits one per
// send. The ffmpeg filter chain ends in `separatefields`, so each read
// corresponds to exactly ONE 720×240×3 RGB888 field (518,400 bytes).
func TestVideoPipeReader_EmitsFields(t *testing.T) {
	fieldSize := 720 * 240 * 3
	buf := &bytes.Buffer{}
	// Write 3 fields of distinct byte patterns so we can confirm ordering.
	for i := 0; i < 3; i++ {
		field := make([]byte, fieldSize)
		for j := range field {
			field[j] = byte(i)
		}
		buf.Write(field)
	}
	ch := make(chan []byte, 4)
	go ReadFieldsFromPipe(buf, 720, 240, 3, ch)
	for i := 0; i < 3; i++ {
		select {
		case f := <-ch:
			if len(f) != fieldSize {
				t.Errorf("field %d size = %d, want %d", i, len(f), fieldSize)
			}
			if f[0] != byte(i) {
				t.Errorf("field %d first byte = %d, want %d", i, f[0], i)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout on field %d", i)
		}
	}
}

// TestVideoPipeReader_ClosesOnEOF verifies the output channel is closed
// when the upstream reader reaches EOF, so the consumer can detect end
// of stream via a `v, ok := <-ch` pattern.
func TestVideoPipeReader_ClosesOnEOF(t *testing.T) {
	fieldSize := 4 * 2 * 3 // tiny synthetic field for the test
	buf := bytes.NewBuffer(make([]byte, fieldSize))
	ch := make(chan []byte, 2)
	done := make(chan struct{})
	go func() {
		ReadFieldsFromPipe(buf, 4, 2, 3, ch)
		close(done)
	}()
	// Drain the field then wait for EOF.
	if _, ok := <-ch; !ok {
		t.Fatal("expected one field before close")
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel close after EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader goroutine did not exit")
	}
}

// TestVideoPipeReader_ClosesOnShortRead verifies a truncated final field
// (partial read before EOF) does not produce a malformed emit — instead
// the channel closes so the consumer sees a clean end-of-stream.
func TestVideoPipeReader_ClosesOnShortRead(t *testing.T) {
	// Write a single byte then EOF — ReadFull returns ErrUnexpectedEOF.
	r := io.NopCloser(bytes.NewReader([]byte{0x42}))
	ch := make(chan []byte, 1)
	done := make(chan struct{})
	go func() {
		ReadFieldsFromPipe(r, 4, 2, 3, ch)
		close(done)
	}()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected no field emit on short read")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout on short-read close")
	}
	<-done
}
