package dataplane

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

// stubFill is the byte staticFrameReader fills every frame with.
const stubFill = 0x55

func volumeDisplay(now time.Time) *osd.Display {
	d := osd.NewDisplay(osd.Options{Enabled: true})
	d.ShowVolume(50, false, now)
	return d
}

func countNonFill(b []byte) int {
	n := 0
	for _, v := range b {
		if v != stubFill {
			n++
		}
	}
	return n
}

func TestPlane_StampOSDDrawsOverlayIntoPayload(t *testing.T) {
	now := time.Now()
	p := NewPlane(PlaneConfig{FieldWidth: 720, FieldHeight: 240, BytesPerPixel: 3, OSD: volumeDisplay(now)})
	payload := bytes.Repeat([]byte{stubFill}, 720*240*3)

	p.stampOSD(payload, now)
	if countNonFill(payload) == 0 {
		t.Fatal("stampOSD left the payload untouched, want volume overlay drawn")
	}
}

func TestPlane_StampOSDWithoutDisplayIsNoOp(t *testing.T) {
	p := NewPlane(PlaneConfig{FieldWidth: 720, FieldHeight: 240, BytesPerPixel: 3})
	payload := bytes.Repeat([]byte{stubFill}, 720*240*3)

	p.stampOSD(payload, time.Now())
	if n := countNonFill(payload); n != 0 {
		t.Fatalf("stampOSD changed %d bytes with no display configured", n)
	}
}

func TestPlane_StampOSDSkipsNonBGR24(t *testing.T) {
	now := time.Now()
	p := NewPlane(PlaneConfig{FieldWidth: 720, FieldHeight: 240, BytesPerPixel: 2, OSD: volumeDisplay(now)})
	payload := bytes.Repeat([]byte{stubFill}, 720*240*2)

	p.stampOSD(payload, now)
	if n := countNonFill(payload); n != 0 {
		t.Fatalf("stampOSD changed %d bytes of a 2-byte-per-pixel payload; OSD only speaks bgr24", n)
	}
}

// The progressive path stamps the whole pool frame, so the canvas height
// must come from the payload rather than FieldHeight.
func TestPlane_StampOSDUsesPayloadHeight(t *testing.T) {
	now := time.Now()
	p := NewPlane(PlaneConfig{FieldWidth: 720, FieldHeight: 240, BytesPerPixel: 3, OSD: volumeDisplay(now)})
	const rows = 288
	payload := bytes.Repeat([]byte{stubFill}, 720*rows*3)

	p.stampOSD(payload, now)
	if countNonFill(payload[720*240*3:]) == 0 {
		t.Fatal("no overlay below row 240 of a 288-row payload; volume bar should sit near its bottom")
	}
}

func TestPlane_RunStampsOSDIntoSentFields(t *testing.T) {
	t.Setenv("GROOVY_PREBUFFER_FIELDS", "0")
	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(false)
	t.Cleanup(func() { _ = listener.Close() })

	addr := listener.Addr().(*net.UDPAddr)
	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	t.Cleanup(func() { _ = sender.Close() })

	const (
		fieldWidth, fieldHeight = 240, 120
		fieldBytes              = fieldWidth * fieldHeight * 3
	)
	cmds := make(chan fakemister.Command, 256)
	fields := make(chan fakemister.FieldEvent, 8)
	audios := make(chan fakemister.AudioEvent, 8)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.RunWithFields(cmds, fields, audios, func() uint32 { return fieldBytes })
	}()
	go func() {
		for range cmds {
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-listenerDone
	})

	stub := newStubProcess()
	origSpawn := spawnProcess
	spawnProcess = func(_ context.Context, _ ffmpeg.PipelineSpec) (processHandle, error) {
		return stub, nil
	}
	t.Cleanup(func() { spawnProcess = origSpawn })

	display := osd.NewDisplay(osd.Options{Enabled: true})
	display.ShowVolume(50, false, time.Now().Add(time.Hour)) // outlives the test
	plane := NewPlane(PlaneConfig{
		Sender:              sender,
		SpawnSpec:           ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{}, SuppressAudioOutput: true},
		Modeline:            groovy.NTSC480i60,
		FieldWidth:          fieldWidth,
		FieldHeight:         fieldHeight,
		BytesPerPixel:       3,
		RGBMode:             groovy.RGBMode888,
		SuppressAudioOutput: true,
		OSD:                 display,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- plane.Run(ctx) }()

	select {
	case field := <-fields:
		cancel()
		if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Plane.Run() error = %v", err)
		}
		if len(field.Payload) != fieldBytes {
			t.Fatalf("field payload = %d bytes, want %d", len(field.Payload), fieldBytes)
		}
		if countNonFill(field.Payload) == 0 {
			t.Fatal("sent field is pure source video; OSD was not stamped before sendField")
		}
	case err := <-runErr:
		t.Fatalf("Plane.Run() exited before sending a field: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a field")
	}
}
