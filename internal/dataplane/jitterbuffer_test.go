package dataplane

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// stallingFrameReader serves frames instantly (ffmpeg runs faster than
// realtime) except for one stall of stallFor when frame stallAt is read,
// modelling a host CPU spike or source hiccup.
type stallingFrameReader struct {
	frameBytes int
	stallAt    int
	stallFor   time.Duration

	mu      sync.Mutex
	read    int
	stalled bool
	closed  bool
}

func (r *stallingFrameReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.EOF
	}
	frame := r.read / r.frameBytes
	stall := frame == r.stallAt && !r.stalled
	if stall {
		r.stalled = true
	}
	r.mu.Unlock()
	if stall {
		time.Sleep(r.stallFor)
	}
	n := r.frameBytes - r.read%r.frameBytes
	if n > len(p) {
		n = len(p)
	}
	for i := 0; i < n; i++ {
		p[i] = 0x55
	}
	r.mu.Lock()
	r.read += n
	r.mu.Unlock()
	return n, nil
}

func (r *stallingFrameReader) Close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
}

type stallingProcess struct {
	video *stallingFrameReader
	done  chan struct{}
	once  sync.Once
}

func (s *stallingProcess) VideoPipe() io.Reader  { return s.video }
func (s *stallingProcess) AudioPipe() io.Reader  { return &eofReader{} }
func (s *stallingProcess) Done() <-chan struct{} { return s.done }
func (s *stallingProcess) Stop() {
	s.once.Do(func() {
		s.video.Close()
		close(s.done)
	})
}

// TestPlane_VideoBufferRidesOutQuarterSecondStall: ffmpeg runs ahead of the
// field clock until the video queue is full, so the queue depth is how long
// a production stall can last before the pump has to emit duplicate fields.
// A 250 ms stall (a host CPU spike) must not reach the CRT.
func TestPlane_VideoBufferRidesOutQuarterSecondStall(t *testing.T) {
	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(false)
	events := make(chan fakemister.Command, 4096)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Run(events)
	}()
	defer func() {
		_ = listener.Close()
		<-listenerDone
	}()
	go func() {
		for range events {
		}
	}()

	sender, err := groovynet.NewSender("127.0.0.1", listener.Addr().(*net.UDPAddr).Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	const w, h, bpp = 8, 2, 3
	proc := &stallingProcess{
		video: &stallingFrameReader{frameBytes: w * h * 2 * bpp, stallAt: 90, stallFor: 250 * time.Millisecond},
		done:  make(chan struct{}),
	}
	origSpawn := spawnProcess
	spawnProcess = func(context.Context, ffmpeg.PipelineSpec) (processHandle, error) { return proc, nil }
	defer func() { spawnProcess = origSpawn }()

	plane := NewPlane(PlaneConfig{
		Sender:        sender,
		Modeline:      groovy.NTSC480i60,
		FieldWidth:    w,
		FieldHeight:   h,
		BytesPerPixel: bpp,
		RGBMode:       groovy.RGBMode888,
	})
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- plane.Run(ctx) }()

	// Frame 90 is read ~1 s in (the queue runs ahead of the clock); let
	// the stall fully play out, then stop.
	time.Sleep(2500 * time.Millisecond)
	cancel()
	<-runErr

	if blits := plane.BlitsTotal(); blits < 100 {
		t.Fatalf("only %d fields sent; pump did not run", blits)
	}
	if got := plane.Underruns(); got != 0 {
		t.Fatalf("underruns = %d: a 250 ms stall reached the CRT as duplicate fields (video queue cap %d = %s)",
			got, videoChCap, time.Duration(videoChCap)*time.Second*1001/60000)
	}
}
