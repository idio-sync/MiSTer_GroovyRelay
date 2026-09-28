package dataplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// fillSource fills every frame with one byte value, switchable mid-session.
type fillSource struct{ v atomic.Uint32 }

func (s *fillSource) ReadFrame(dst []byte) {
	b := byte(s.v.Load())
	for i := range dst {
		dst[i] = b
	}
}

func TestFrameSourceReader(t *testing.T) {
	src := &fillSource{}
	src.v.Store(7)
	proc := newFrameSourceProcess(src, 6)

	// Whole-frame reads go straight to the source.
	buf := make([]byte, 6)
	if n, err := proc.VideoPipe().Read(buf); n != 6 || err != nil || !bytes.Equal(buf, bytes.Repeat([]byte{7}, 6)) {
		t.Fatalf("whole-frame read = %d, %v, %v", n, err, buf)
	}
	// Odd-sized reads stay frame-aligned: the source is sampled once per frame.
	var got []byte
	small := make([]byte, 4)
	for len(got) < 12 {
		n, err := proc.VideoPipe().Read(small)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, small[:n]...)
		if len(got) == 4 {
			src.v.Store(9) // mid-frame change must not tear the frame
		}
	}
	want := append(bytes.Repeat([]byte{7}, 6), bytes.Repeat([]byte{9}, 6)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("odd-sized reads = %v, want %v", got, want)
	}
	if n, _ := io.ReadFull(proc.AudioPipe(), make([]byte, 1)); n != 0 {
		t.Fatal("frame source must have no audio")
	}

	proc.Stop()
	proc.Stop() // idempotent
	select {
	case <-proc.Done():
	default:
		t.Fatal("Done not closed after Stop")
	}
	if _, err := proc.VideoPipe().Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("read after Stop = %v, want io.EOF", err)
	}
}

// A Frames plane never spawns ffmpeg, sends one INIT, runs video-only, and
// a source change reaches later fields without restarting the session.
func TestPlane_FrameSourceSwapsWithoutRestart(t *testing.T) {
	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(false)
	t.Cleanup(func() { _ = listener.Close() })

	addr := listener.Addr().(*net.UDPAddr)
	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	t.Cleanup(func() { _ = sender.Close() })

	const (
		fieldWidth    = 4
		fieldHeight   = 1
		bytesPerPixel = 1
		fieldBytes    = fieldWidth * fieldHeight * bytesPerPixel
	)
	cmds := make(chan fakemister.Command, 256)
	fields := make(chan fakemister.FieldEvent, 64)
	audios := make(chan fakemister.AudioEvent, 8)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.RunWithFields(cmds, fields, audios, func() uint32 { return fieldBytes })
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-listenerDone:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for fake MiSTer listener to stop")
		}
	})

	origSpawn := spawnProcess
	spawnProcess = func(context.Context, ffmpeg.PipelineSpec) (processHandle, error) {
		t.Error("Frames plane must not spawn ffmpeg")
		return nil, errors.New("unexpected spawn")
	}
	t.Cleanup(func() { spawnProcess = origSpawn })

	src := &fillSource{}
	src.v.Store(0x11)
	plane := NewPlane(PlaneConfig{
		Sender:        sender,
		Modeline:      groovy.NTSC480i60,
		FieldWidth:    fieldWidth,
		FieldHeight:   fieldHeight,
		BytesPerPixel: bytesPerPixel,
		RGBMode:       groovy.RGBMode888,
		Frames:        src,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- plane.Run(ctx) }()

	inits := 0
	swapped := false
	timeout := time.After(5 * time.Second)
	for {
		select {
		case cmd := <-cmds:
			if cmd.Type == groovy.CmdInit {
				inits++
				if cmd.Init.SoundRate != groovy.AudioRateOff {
					t.Fatalf("INIT sound rate = %d, want AudioRateOff", cmd.Init.SoundRate)
				}
			}
		case field := <-fields:
			if len(field.Payload) != fieldBytes {
				t.Fatalf("field payload bytes = %d, want %d", len(field.Payload), fieldBytes)
			}
			switch v := field.Payload[0]; {
			case v == 0x11 && !swapped:
				src.v.Store(0x22)
				swapped = true
			case v == 0x22:
				cancel()
				if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("Plane.Run() = %v", err)
				}
				for len(cmds) > 0 {
					if (<-cmds).Type == groovy.CmdInit {
						inits++
					}
				}
				if inits != 1 {
					t.Fatalf("INIT sent %d times, want 1", inits)
				}
				return
			}
		case audio := <-audios:
			t.Fatalf("unexpected audio payload bytes = %d", len(audio.PCM))
		case err := <-runErr:
			t.Fatalf("Plane.Run() exited early: %v", err)
		case <-timeout:
			cancel()
			t.Fatalf("timed out; swapped=%v inits=%d err=%v", swapped, inits, <-runErr)
		}
	}
}
