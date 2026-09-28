package dataplane

import (
	"context"
	cryptorand "crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// Under LZ4 the core has no header-only duplicate (a 9-byte header arms a
// zero-size compressed blit), so an underrun tick sends nothing at all; the
// FPGA keeps scanning its last field and the frame counter still advances.
func TestHoldField_LZ4SendsNothing(t *testing.T) {
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{Codec: CodecLZ4, FieldWidth: 8, FieldHeight: 2, BytesPerPixel: 3})
	p.fieldSender = sender
	p.holdField(3, 0)
	p.holdField(4, 1)
	if len(sender.headers) != 0 || len(sender.payloads) != 0 {
		t.Fatalf("hold under LZ4 sent %d headers / %d payloads, want none", len(sender.headers), len(sender.payloads))
	}
	if got := p.wireBytes.Load(); got != 0 {
		t.Fatalf("wire bytes = %d, want 0", got)
	}
}

// The RAW codec keeps the 9-byte duplicate header, which the core honours
// only while compression is off.
func TestHoldField_RawCodecSendsDupHeader(t *testing.T) {
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{FieldWidth: 8, FieldHeight: 2, BytesPerPixel: 3})
	p.fieldSender = sender
	p.holdField(3, 1)
	if len(sender.headers) != 1 {
		t.Fatalf("hold on RAW sent %d headers, want 1", len(sender.headers))
	}
	hdr := sender.headers[0]
	if len(hdr) != groovy.BlitHeaderRawDup || hdr[8] != groovy.BlitFlagDup || hdr[5] != 1 {
		t.Fatalf("hold header = %v, want 9-byte dup for field 1", hdr)
	}
	if len(sender.payloads) != 0 {
		t.Fatalf("hold on RAW sent %d payloads, want 0", len(sender.payloads))
	}
}

// End to end against a fake MiSTer that models the core's LZ4 header
// handling: underrun ticks during an LZ4 session must not arm zero-size
// compressed blits, and every field that follows must still decode.
func TestPlane_LZ4UnderrunKeepsFieldsDecodable(t *testing.T) {
	// No prebuffer wait, and a source stall at the first frame: the opening
	// ticks are all underruns.
	t.Setenv("GROOVY_PREBUFFER_TIMEOUT_MS", "1")
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(false)

	const w, h, bpp = 8, 2, 3
	const fieldBytes = w * h * bpp
	cmds := make(chan fakemister.Command, 4096)
	fields := make(chan fakemister.FieldEvent, 4096)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.RunWithFields(cmds, fields, make(chan fakemister.AudioEvent, 16), func() uint32 { return fieldBytes })
	}()
	defer func() {
		_ = listener.Close()
		<-listenerDone
	}()
	go func() {
		for range cmds {
		}
	}()

	sender, err := groovynet.NewSender("127.0.0.1", listener.Addr().(*net.UDPAddr).Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	proc := &stallingProcess{
		video: &stallingFrameReader{frameBytes: w * h * 2 * bpp, stallAt: 0, stallFor: 300 * time.Millisecond},
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
		Codec:         CodecLZ4,
	})
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- plane.Run(ctx) }()

	dec := fakemister.NewFieldDecoder()
	decoded := 0
	var lastFrame uint32
	deadline := time.After(3 * time.Second)
	for decoded < 30 {
		select {
		case fe := <-fields:
			if _, err := dec.Decode(fe, fieldBytes); err != nil {
				t.Fatalf("field frame %d does not decode: %v", fe.Header.Frame, err)
			}
			if fe.Header.Frame <= lastFrame {
				t.Fatalf("frame %d after %d; frame numbers must keep increasing", fe.Header.Frame, lastFrame)
			}
			lastFrame = fe.Header.Frame
			decoded++
		case err := <-runErr:
			t.Fatalf("Plane.Run exited early: %v", err)
		case <-deadline:
			t.Fatalf("decoded only %d fields before timeout", decoded)
		}
	}
	cancel()
	<-runErr

	if plane.Underruns() == 0 {
		t.Fatal("no underruns; the stall did not exercise the hold path")
	}
	if got := listener.ZeroSizeLZ4Blits(); got != 0 {
		t.Fatalf("fake MiSTer saw %d zero-size LZ4 blits (RAW/dup headers during an LZ4 session)", got)
	}
}

// Progressive modes send the whole frame, which can exceed FieldHeight rows;
// the LZ4 scratch must hold an incompressible frame's expanded block.
func TestSendField_ProgressiveIncompressibleFrameFitsScratch(t *testing.T) {
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		Codec:         CodecLZ4,
		Modeline:      groovy.NTSC240p60,
		FieldWidth:    64,
		FieldHeight:   8,
		BytesPerPixel: 3,
		SpawnSpec:     ffmpeg.PipelineSpec{OutputHeight: 16},
	})
	p.fieldSender = sender
	frame := make([]byte, 64*16*3)
	if _, err := cryptorand.Read(frame); err != nil {
		t.Fatal(err)
	}
	p.sendField(1, 0, frame)
	if len(sender.headers) != 1 || len(sender.headers[0]) != groovy.BlitHeaderLZ4 {
		t.Fatalf("headers = %d, want one LZ4 header", len(sender.headers))
	}
	if _, err := groovy.LZ4Decompress(sender.payloads[0], len(frame)); err != nil {
		t.Fatalf("frame payload does not decode: %v", err)
	}
}
