//go:build integration

package integration

import (
	"context"
	"encoding/binary"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// TestPlane_StreamsFieldsToFake exercises the full Plane pipeline end-to-end:
// real ffmpeg spawn, INIT/ACK handshake via a stub listener, SWITCHRES,
// ~300 BLIT_FIELD_VSYNC packets over ~5 seconds, and a clean CLOSE when the
// process exits or context cancels.
//
// Skipped on Windows: cmd.ExtraFiles is Unix-only, so ffmpeg.Spawn will fail
// at cmd.Start(). Production target is Linux/Docker; Linux CI runs this
// test. The build still compiles on Windows so `go build ./...` stays clean.
func TestPlane_StreamsFieldsToFake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg plane test requires Unix ExtraFiles; run on Linux/CI")
	}

	samplePath := ensureSampleMP4(t, "5s.mp4", 5)

	// Bring up the fake-mister listener + a stub that replies to INIT with a
	// synthesized ACK. The basic listener does not emit ACKs, so we steal
	// the socket briefly to answer the handshake before the listener Run
	// loop starts.
	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().(*net.UDPAddr)
	events := make(chan fakemister.Command, 4096)
	fieldsCh := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 4096)
	rec := fakemister.NewRecorder()
	recDone := make(chan struct{})
	drainDone := make(chan struct{})
	audioFanDone := make(chan struct{})
	go func() {
		for c := range events {
			rec.Record(c)
		}
		close(recDone)
	}()
	// Drain reassembled field payloads — this test counts BLIT headers via
	// Command records on `events`, not FieldEvents.
	go func() {
		for range fieldsCh {
		}
		close(drainDone)
	}()
	// Fan AudioEvents into synthetic Commands so Recorder.audioBytes counts
	// PCM bytes correctly. RunWithFields reassembles audio payload datagrams
	// (which the stateless Run would not — some would even false-positive
	// as spurious SWITCHRES commands because payload bytes occasionally
	// start with 0x03).
	go func() {
		for ev := range audios {
			events <- fakemister.Command{
				Type:         groovy.CmdAudio,
				AudioPayload: &fakemister.AudioPayload{PCM: ev.PCM},
			}
		}
		close(audioFanDone)
	}()
	fieldSizeFn := func() uint32 { return 720 * 240 * 3 }

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}

	// INIT ACK replier: one-shot. Reads the INIT packet, writes a 13-byte
	// ACK back to the sender's source port (with audio-ready bit set so
	// the Plane sends CMD_AUDIO). After that the listener Run loop takes
	// over and records commands.
	ackDone := make(chan struct{})
	go func() {
		defer close(ackDone)
		conn := l.Conn()
		buf := make([]byte, 64)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil || n == 0 || buf[0] != groovy.CmdInit {
			_ = conn.SetReadDeadline(time.Time{})
			return
		}
		_ = conn.SetReadDeadline(time.Time{})
		ack := make([]byte, groovy.ACKPacketSize)
		// Frame echo = 0, vCountEcho = 0, fpgaFrame = 0, fpgaVCount = 0,
		// status = 0x40 (bit 6 audio ready).
		binary.LittleEndian.PutUint32(ack[0:4], 0)
		binary.LittleEndian.PutUint16(ack[4:6], 0)
		binary.LittleEndian.PutUint32(ack[6:10], 0)
		binary.LittleEndian.PutUint16(ack[10:12], 0)
		ack[12] = 1 << 6
		_, _ = conn.WriteToUDP(ack, src)
	}()

	// Start the listener AFTER the INIT replier returns so they don't race
	// on the same socket.
	runDone := make(chan struct{})
	go func() {
		<-ackDone
		l.RunWithFields(events, fieldsCh, audios, fieldSizeFn)
		close(runDone)
	}()

	t.Cleanup(func() {
		sender.Close()
		l.Close()
		<-runDone
		close(fieldsCh)
		close(audios)
		<-drainDone
		<-audioFanDone
		close(events)
		<-recDone
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
			SourceProbe:     &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 24.0},
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

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()

	var runErr atomic.Value
	planeDone := make(chan struct{})
	go func() {
		if err := plane.Run(ctx); err != nil {
			runErr.Store(err)
		}
		close(planeDone)
	}()

	select {
	case <-planeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("plane did not finish within 10s")
	}

	// Small settling delay so trailing datagrams land in the recorder.
	time.Sleep(200 * time.Millisecond)

	snap := rec.Snapshot()
	// 5 seconds × ~59.94 fields/sec = ~300 fields; tolerate ±15% to absorb
	// startup lag and ffmpeg ramp-up.
	got := snap.Counts[groovy.CmdBlitFieldVSync]
	if got < 255 || got > 345 {
		t.Errorf("expected ~300 blits, got %d", got)
	}
	if snap.Counts[groovy.CmdSwitchres] != 1 {
		t.Errorf("switchres count = %d, want 1", snap.Counts[groovy.CmdSwitchres])
	}
	if snap.Counts[groovy.CmdClose] < 1 {
		t.Errorf("close count = %d, want >=1", snap.Counts[groovy.CmdClose])
	}
}
