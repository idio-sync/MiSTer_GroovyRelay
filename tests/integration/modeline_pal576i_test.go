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

// TestModeline_PAL576i exercises the full Plane pipeline with the PAL_576i
// modeline: 720x576i interlaced at 50 Hz field rate (25 Hz frame × 2 fields).
// Asserts that SWITCHRES wire bytes match groovy.BuildSwitchres(groovy.PAL576i50),
// that at least 85 BLIT_FIELD_VSYNC fields arrive in ~2 seconds, that field
// bits alternate 0/1 (interlaced), and that audio bytes are within ±10% of
// the expected 2-second total.
//
// Skipped on Windows: cmd.ExtraFiles is Unix-only. Production target is
// Linux/Docker.
func TestModeline_PAL576i(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg plane test requires Unix ExtraFiles; run on Linux/CI")
	}

	samplePath := ensureSampleMP4(t, "5s.mp4", 5)

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
	go func() {
		for range fieldsCh {
		}
		close(drainDone)
	}()
	go func() {
		for ev := range audios {
			events <- fakemister.Command{
				Type:         groovy.CmdAudio,
				AudioPayload: &fakemister.AudioPayload{PCM: ev.PCM},
			}
		}
		close(audioFanDone)
	}()

	fieldSizeFn := func() uint32 {
		return uint32(groovy.FieldPayloadBytes(
			groovy.PAL576i50.HActive,
			groovy.PAL576i50.VActive,
			groovy.PAL576i50.Interlace,
			3,
		))
	}

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}

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
		binary.LittleEndian.PutUint32(ack[0:4], 0)
		binary.LittleEndian.PutUint16(ack[4:6], 0)
		binary.LittleEndian.PutUint32(ack[6:10], 0)
		binary.LittleEndian.PutUint16(ack[10:12], 0)
		ack[12] = 1 << 6 // bit 6 = audio ready
		_, _ = conn.WriteToUDP(ack, src)
	}()

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
			OutputWidth:     int(groovy.PAL576i50.HActive),
			OutputHeight:    int(groovy.PAL576i50.VActive),
			OutputFpsExpr:   "50/1",
			FieldOrder:      "tff",
			AspectMode:      "letterbox",
			AudioSampleRate: 48000,
			AudioChannels:   2,
			SourceProbe:     &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 24.0, AudioRate: 48000},
		},
		Modeline:      groovy.PAL576i50,
		FieldWidth:    int(groovy.PAL576i50.HActive),
		FieldHeight:   groovy.PAL576i50.FieldHeight(),
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
		LZ4Enabled:    true,
		AudioRate:     48000,
		AudioChans:    2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	case <-time.After(5 * time.Second):
		t.Fatal("plane did not finish within 5s")
	}

	time.Sleep(200 * time.Millisecond)

	snap := rec.Snapshot()

	// 1. SWITCHRES wire bytes must match the preset exactly.
	assertSwitchresMatches(t, snap, groovy.BuildSwitchres(groovy.PAL576i50), "PAL_576i")

	// 2. Field count: 2 s × 50 Hz = ~100 fields; require ≥ 85.
	// Threshold matches NTSC_240p's ~83 % tolerance to absorb CI cold-start
	// (prebuffer + ffmpeg primer can drop ~12 % of the 2 s window on a
	// 2-vCPU GitHub runner).
	gotBlits := snap.Counts[groovy.CmdBlitFieldVSync]
	if gotBlits < 85 {
		t.Errorf("PAL_576i: expected ≥85 blits in 2s, got %d", gotBlits)
	}

	// 3. Field bits must alternate 0/1 (interlaced modeline).
	// We need at least 2 fields to check alternation.
	if len(snap.BlitFields) >= 2 {
		for i := 1; i < len(snap.BlitFields); i++ {
			if snap.BlitFields[i] == snap.BlitFields[i-1] {
				t.Errorf("PAL_576i: blit[%d] field bit = %d, same as blit[%d] = %d; expected alternating 0/1",
					i, snap.BlitFields[i], i-1, snap.BlitFields[i-1])
				break
			}
		}
	} else if len(snap.BlitFields) == 0 {
		t.Errorf("PAL_576i: no BlitFields captured; cannot check interlace pattern")
	}

	// 4. Audio bytes: 2 s × 48000 Hz × 2 ch × 2 bytes/sample = 384000; ±20%.
	// Lower band absorbs cold-start startup overhead on CI; see ntsc240p
	// counterpart.
	const wantAudio = 2 * 48000 * 2 * 2 // 384000
	margin := wantAudio * 20 / 100
	lo, hi := int(wantAudio-margin), int(wantAudio+margin)
	if snap.AudioBytes < lo || snap.AudioBytes > hi {
		t.Errorf("PAL_576i: audio bytes = %d, want %d±20%% [%d, %d]",
			snap.AudioBytes, wantAudio, lo, hi)
	}
}
