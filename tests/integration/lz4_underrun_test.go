//go:build integration

package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// TestPlane_LZ4UnderrunKeepsFieldsDecodable forces underrun ticks during an
// LZ4 session and checks the fake MiSTer — which models the Groovy core's
// LZ4 header handling (8/9-byte headers arm a zero-size compressed blit) —
// never sees a RAW or dup header, and every field decodes.
//
// The underruns come from skipping the startup prebuffer: with a 1 ms
// prebuffer timeout the tick loop starts before ffmpeg has produced its
// first frame.
func TestPlane_LZ4UnderrunKeepsFieldsDecodable(t *testing.T) {
	samplePath := ensureSampleMP4(t, "5s.mp4", 5)
	t.Setenv("GROOVY_PREBUFFER_TIMEOUT_MS", "1")

	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// The listener must answer INIT itself so it learns compression is on.
	l.EnableACKs(true)
	addr := l.Addr().(*net.UDPAddr)

	fieldBytes := groovy.FieldPayloadBytes(
		groovy.NTSC480i60.HActive,
		groovy.NTSC480i60.VActive,
		groovy.NTSC480i60.Interlace,
		3,
	)
	events := make(chan fakemister.Command, 4096)
	fieldsCh := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 4096)
	runDone := make(chan struct{})
	go func() {
		l.RunWithFields(events, fieldsCh, audios, func() uint32 { return uint32(fieldBytes) })
		close(runDone)
	}()
	go func() {
		for range events {
		}
	}()
	go func() {
		for range audios {
		}
	}()

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sender.Close()
		l.Close()
		<-runDone
		close(events)
		close(audios)
	})

	plane := dataplane.NewPlane(dataplane.PlaneConfig{
		Sender: sender,
		SpawnSpec: ffmpeg.PipelineSpec{
			InputURL:        samplePath,
			OutputWidth:     720,
			OutputHeight:    480,
			FieldOrder:      "tff",
			AspectMode:      "letterbox",
			AudioSampleRate: 48000,
			AudioChannels:   2,
			SourceProbe:     &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 24.0, AudioRate: 48000},
		},
		Modeline:      groovy.NTSC480i60,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
		LZ4Enabled:    true,
		AudioRate:     48000,
		AudioChans:    2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	planeDone := make(chan error, 1)
	go func() { planeDone <- plane.Run(ctx) }()

	dec := fakemister.NewFieldDecoder()
	decoded := 0
	var lastFrame uint32
	collect := func(fe fakemister.FieldEvent) {
		t.Helper()
		if !fe.Header.Compressed {
			t.Fatalf("frame %d arrived RAW during an LZ4 session", fe.Header.Frame)
		}
		if _, err := dec.Decode(fe, fieldBytes); err != nil {
			t.Fatalf("frame %d does not decode: %v", fe.Header.Frame, err)
		}
		if fe.Header.Frame <= lastFrame {
			t.Fatalf("frame %d after %d; frame numbers must keep increasing", fe.Header.Frame, lastFrame)
		}
		lastFrame = fe.Header.Frame
		decoded++
	}
wait:
	for {
		select {
		case fe := <-fieldsCh:
			collect(fe)
		case <-planeDone:
			break wait
		case <-time.After(6 * time.Second):
			t.Fatal("plane did not finish")
		}
	}
	// Trailing datagrams.
	settle := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case fe := <-fieldsCh:
			collect(fe)
		case <-settle:
			break drain
		}
	}

	underruns := plane.Underruns()
	t.Logf("underruns=%d decoded_fields=%d last_frame=%d", underruns, decoded, lastFrame)
	if underruns == 0 {
		t.Fatal("no underruns: the test did not exercise the LZ4 hold path")
	}
	if got := l.ZeroSizeLZ4Blits(); got != 0 {
		t.Fatalf("fake MiSTer saw %d zero-size LZ4 blits (RAW/dup headers during an LZ4 session)", got)
	}
	if decoded < 60 {
		t.Fatalf("decoded %d fields, want >= 60 across a 3 s session", decoded)
	}
	// Fields after the underrun run must carry frame numbers past the held
	// ticks: the counter advances on every tick, sent or not.
	if uint64(lastFrame) < uint64(decoded)+underruns-1 {
		t.Fatalf("last frame %d < decoded %d + underruns %d - 1; held ticks did not advance frameNum",
			lastFrame, decoded, underruns)
	}
}
