package dataplane

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// TestVideoPipeReader_EmitsFrames verifies ReadFramesFromPipe slices its
// upstream reader into width*height*bpp-sized progressive frames and emits
// one per send.
func TestVideoPipeReader_EmitsFrames(t *testing.T) {
	frameSize := 720 * 480 * 3
	buf := &bytes.Buffer{}
	for i := 0; i < 3; i++ {
		frame := make([]byte, frameSize)
		for j := range frame {
			frame[j] = byte(i)
		}
		buf.Write(frame)
	}
	ch := make(chan []byte, 4)
	go ReadFramesFromPipe(buf, 720, 480, 3, ch)
	for i := 0; i < 3; i++ {
		select {
		case f := <-ch:
			if len(f) != frameSize {
				t.Errorf("frame %d size = %d, want %d", i, len(f), frameSize)
			}
			if f[0] != byte(i) {
				t.Errorf("frame %d first byte = %d, want %d", i, f[0], i)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout on frame %d", i)
		}
	}
}

// TestVideoPipeReader_ClosesOnEOF verifies the output channel is closed
// when the upstream reader reaches EOF, so the consumer can detect end
// of stream via a `v, ok := <-ch` pattern.
func TestVideoPipeReader_ClosesOnEOF(t *testing.T) {
	frameSize := 4 * 4 * 3
	buf := bytes.NewBuffer(make([]byte, frameSize))
	ch := make(chan []byte, 2)
	done := make(chan struct{})
	go func() {
		ReadFramesFromPipe(buf, 4, 4, 3, ch)
		close(done)
	}()
	if _, ok := <-ch; !ok {
		t.Fatal("expected one frame before close")
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

// TestVideoPipeReader_ClosesOnShortRead verifies a truncated final frame
// (partial read before EOF) does not produce a malformed emit — instead
// the channel closes so the consumer sees a clean end-of-stream.
func TestVideoPipeReader_ClosesOnShortRead(t *testing.T) {
	r := io.NopCloser(bytes.NewReader([]byte{0x42}))
	ch := make(chan []byte, 1)
	done := make(chan struct{})
	go func() {
		ReadFramesFromPipe(r, 4, 4, 3, ch)
		close(done)
	}()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected no frame emit on short read")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout on short-read close")
	}
	<-done
}

func TestExtractFieldFromFrame_RowStripesEvenAndOddRows(t *testing.T) {
	const (
		width  = 2
		height = 4
		bpp    = 3
	)
	frame := []byte{
		0, 0, 0, 0, 0, 0,
		1, 1, 1, 1, 1, 1,
		2, 2, 2, 2, 2, 2,
		3, 3, 3, 3, 3, 3,
	}

	top := ExtractFieldFromFrame(frame, width, height, bpp, 0)
	bottom := ExtractFieldFromFrame(frame, width, height, bpp, 1)

	if got, want := []byte{top[0], top[width*bpp]}, []byte{0, 2}; !bytes.Equal(got, want) {
		t.Fatalf("top field rows = %v, want %v", got, want)
	}
	if got, want := []byte{bottom[0], bottom[width*bpp]}, []byte{1, 3}; !bytes.Equal(got, want) {
		t.Fatalf("bottom field rows = %v, want %v", got, want)
	}
}

func TestExtractFieldFromFrameInto_MatchesLegacy(t *testing.T) {
	const w, h, bpp = 16, 8, 3
	frame := make([]byte, w*h*bpp)
	for i := range frame {
		frame[i] = byte(i)
	}
	for _, field := range []uint8{0, 1} {
		legacy := ExtractFieldFromFrame(frame, w, h, bpp, field)
		dst := make([]byte, w*(h/2)*bpp)
		ExtractFieldFromFrameInto(dst, frame, w, h, bpp, field)
		if !bytes.Equal(legacy, dst) {
			t.Errorf("field %d mismatch:\n  legacy: % x\n  new:    % x", field, legacy, dst)
		}
	}
}

func TestExtractFieldFromFrameInto_ZeroAllocs(t *testing.T) {
	const w, h, bpp = 720, 480, 3
	frame := make([]byte, w*h*bpp)
	dst := make([]byte, w*(h/2)*bpp)
	ExtractFieldFromFrameInto(dst, frame, w, h, bpp, 0) // warmup
	got := testing.AllocsPerRun(50, func() {
		ExtractFieldFromFrameInto(dst, frame, w, h, bpp, 0)
	})
	if got != 0 {
		t.Errorf("ExtractFieldFromFrameInto allocs/op = %v, want 0", got)
	}
}

func TestReadFramesFromPipePooled_RoundTrip(t *testing.T) {
	const frameBytes = 720 * 480 * 3
	pool := NewFramePool(4, frameBytes)
	buf := &bytes.Buffer{}
	for i := 0; i < 3; i++ {
		f := make([]byte, frameBytes)
		for j := range f {
			f[j] = byte(i)
		}
		buf.Write(f)
	}
	out := make(chan *FrameBuf, 4)
	go ReadFramesFromPipePooled(buf, pool, out)

	for i := 0; i < 3; i++ {
		select {
		case fb := <-out:
			if fb.N != frameBytes {
				t.Errorf("frame %d N = %d, want %d", i, fb.N, frameBytes)
			}
			if fb.Data[0] != byte(i) {
				t.Errorf("frame %d first byte = %d, want %d", i, fb.Data[0], i)
			}
			pool.Put(fb)
		case <-time.After(time.Second):
			t.Fatalf("timeout on frame %d", i)
		}
	}

	// EOF: out should close after the third frame.
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("expected channel close after EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestReadFramesFromPipePooled_PartialReadReturnsBufferAndCloses(t *testing.T) {
	const frameBytes = 1000
	pool := NewFramePool(2, frameBytes)
	// Write half a frame.
	half := bytes.NewReader(make([]byte, frameBytes/2))
	out := make(chan *FrameBuf, 2)
	done := make(chan struct{})
	go func() {
		ReadFramesFromPipePooled(half, pool, out)
		close(done)
	}()

	// out should close without emitting any *FrameBuf — partial reads
	// are not propagated.
	select {
	case fb, ok := <-out:
		if ok {
			t.Errorf("partial read should not emit; got *FrameBuf with N=%d", fb.N)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not close out after partial read")
	}

	<-done
	// All buffers should be back in the pool.
	for i := 0; i < 2; i++ {
		select {
		case <-pool.free:
		case <-time.After(time.Second):
			t.Fatalf("buffer %d not returned to pool after EOF", i)
		}
	}
}
