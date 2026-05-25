package dataplane

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

func TestAUXVisualOnlyProducesVideoWithoutAudioPipe(t *testing.T) {
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
	cmds := make(chan fakemister.Command, 64)
	fields := make(chan fakemister.FieldEvent, 8)
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

	stub := newStubProcess()
	origSpawn := spawnProcess
	var capturedSpec ffmpeg.PipelineSpec
	spawnProcess = func(_ context.Context, spec ffmpeg.PipelineSpec) (processHandle, error) {
		capturedSpec = spec
		return stub, nil
	}
	t.Cleanup(func() { spawnProcess = origSpawn })

	plane := NewPlane(PlaneConfig{
		Sender:              sender,
		SpawnSpec:           ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}, SuppressAudioOutput: true},
		Modeline:            groovy.NTSC480i60,
		FieldWidth:          fieldWidth,
		FieldHeight:         fieldHeight,
		BytesPerPixel:       bytesPerPixel,
		RGBMode:             groovy.RGBMode888,
		AudioRate:           48000,
		AudioChans:          2,
		SuppressAudioOutput: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- plane.Run(ctx)
	}()

	recorder := fakemister.NewRecorder()
	var sawInit bool
	timeout := time.After(2 * time.Second)
	for {
		select {
		case cmd := <-cmds:
			recorder.Record(cmd)
			if cmd.Type == groovy.CmdInit {
				if cmd.Init == nil {
					t.Fatal("INIT command missing parsed payload")
				}
				if cmd.Init.SoundRate != groovy.AudioRateOff || cmd.Init.SoundChan != 0 {
					t.Fatalf("INIT audio = rate %d/chans %d, want AudioRateOff/0",
						cmd.Init.SoundRate, cmd.Init.SoundChan)
				}
				sawInit = true
			}
		case field := <-fields:
			if len(field.Payload) != fieldBytes {
				t.Fatalf("field payload bytes = %d, want %d", len(field.Payload), fieldBytes)
			}
			if !sawInit {
				t.Fatal("received video field before observing INIT")
			}
			cancel()
			err := <-runErr
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Plane.Run() error = %v, want context.Canceled or nil", err)
			}
			if !capturedSpec.SuppressAudioOutput {
				t.Fatalf("SpawnSpec.SuppressAudioOutput = false, want true")
			}
			if stub.audioPipeCalls != 0 {
				t.Fatalf("AudioPipe calls = %d, want 0", stub.audioPipeCalls)
			}
			select {
			case audio := <-audios:
				t.Fatalf("unexpected audio payload bytes = %d", len(audio.PCM))
			default:
			}
			snap := recorder.Snapshot()
			if snap.Counts[groovy.CmdBlitFieldVSync] == 0 {
				t.Fatalf("recorder saw no BLIT_FIELD_VSYNC commands: %+v", snap.Counts)
			}
			return
		case audio := <-audios:
			t.Fatalf("unexpected audio payload bytes = %d", len(audio.PCM))
		case err := <-runErr:
			t.Fatalf("Plane.Run() exited before video field: %v", err)
		case <-timeout:
			cancel()
			err := <-runErr
			t.Fatalf("timed out waiting for AUX visual-only field; Plane.Run() err=%v", err)
		}
	}
}
