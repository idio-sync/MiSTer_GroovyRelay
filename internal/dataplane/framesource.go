package dataplane

import (
	"bytes"
	"io"
	"sync"
)

// FrameSource supplies raw raster frames in place of an ffmpeg child: full
// progressive frames of FieldWidth × resolveVideoHeight() pixels at
// BytesPerPixel, in the wire pixel format. ReadFrame fills dst (always one
// whole frame) with the current frame and must not block for long; the
// plane's reader calls it whenever a frame buffer is free.
type FrameSource interface {
	ReadFrame(dst []byte)
}

// frameSourceProcess adapts a FrameSource to the processHandle Run consumes,
// so a Go-generated session runs through the same prebuffer, field
// extraction, OSD, compression and pacing as an ffmpeg one. It has no
// audio, and only "exits" when Run stops it.
type frameSourceProcess struct {
	video *frameSourceReader
	done  chan struct{}
	once  sync.Once
}

func newFrameSourceProcess(src FrameSource, frameBytes int) *frameSourceProcess {
	done := make(chan struct{})
	return &frameSourceProcess{
		video: &frameSourceReader{src: src, frameBytes: frameBytes, done: done},
		done:  done,
	}
}

func (f *frameSourceProcess) VideoPipe() io.Reader  { return f.video }
func (f *frameSourceProcess) AudioPipe() io.Reader  { return bytes.NewReader(nil) }
func (f *frameSourceProcess) Done() <-chan struct{} { return f.done }
func (f *frameSourceProcess) Stop()                 { f.once.Do(func() { close(f.done) }) }

// frameSourceReader streams ReadFrame output as a byte stream, one whole
// frame at a time. The video reader asks for exactly one frame per Read, so
// that path fills the caller's buffer directly; any other read size goes
// through a scratch frame. Reads return io.EOF once the process is stopped.
type frameSourceReader struct {
	src        FrameSource
	frameBytes int
	done       <-chan struct{}
	scratch    []byte
	off        int // bytes of scratch already returned; 0 = none pending
}

func (r *frameSourceReader) Read(p []byte) (int, error) {
	select {
	case <-r.done:
		return 0, io.EOF
	default:
	}
	if r.off == 0 && len(p) == r.frameBytes {
		r.src.ReadFrame(p)
		return len(p), nil
	}
	if r.off == 0 {
		if r.scratch == nil {
			r.scratch = make([]byte, r.frameBytes)
		}
		r.src.ReadFrame(r.scratch)
	}
	n := copy(p, r.scratch[r.off:])
	r.off = (r.off + n) % r.frameBytes
	return n, nil
}
