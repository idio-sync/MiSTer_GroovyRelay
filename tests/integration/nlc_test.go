//go:build integration

package integration

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy/nlc"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// nlcSessionResult collects what an nlcSession observed while a real Plane
// ran a real ffmpeg pipeline against a fake MiSTer.
type nlcSessionResult struct {
	initByte       byte
	sawInit        bool
	blits          int
	nonLZ4Headers  int // BLIT headers whose length isn't the 12-byte compressed form
	decoded        int
	decodeErr      error
	firstField     []byte
	fieldsDiffer   bool
	effectiveCodec dataplane.Codec
}

// runNLCSession drives a real dataplane.Plane (real ffmpeg, real Plane.Run)
// against a fake MiSTer impersonating coreVersion, with the given codec
// config and modeline. It runs for runFor and returns what the fake MiSTer
// observed: the INIT byte, BLIT header shapes, and decoded field content —
// decoding every field with a fakemister.FieldDecoder configured from the
// observed INIT byte, exactly as a real receiver would.
func runNLCSession(t *testing.T, coreVersion byte, codec dataplane.Codec, near int, pack nlc.Pack, modeline groovy.Modeline, fieldOrder string, outputFpsExpr string, runFor time.Duration) *nlcSessionResult {
	t.Helper()
	samplePath := ensureSampleMP4(t, "5s.mp4", 5)

	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.EnableACKs(true)
	l.SetCoreVersion(coreVersion)
	addr := l.Addr().(*net.UDPAddr)

	fieldWidth := int(modeline.HActive)
	fieldHeight := groovy.FieldLines(modeline.VActive, modeline.Interlace)
	fieldBytes := fieldWidth * fieldHeight * 3

	events := make(chan fakemister.Command, 4096)
	fieldsCh := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 4096)
	runDone := make(chan struct{})
	go func() {
		l.RunWithFields(events, fieldsCh, audios, func() uint32 { return uint32(fieldBytes) })
		close(runDone)
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
	})

	plane := dataplane.NewPlane(dataplane.PlaneConfig{
		Sender: sender,
		SpawnSpec: ffmpeg.PipelineSpec{
			InputURL:        samplePath,
			OutputWidth:     int(modeline.HActive),
			OutputHeight:    int(modeline.VActive),
			OutputFpsExpr:   outputFpsExpr,
			FieldOrder:      fieldOrder,
			AspectMode:      "letterbox",
			AudioSampleRate: 48000,
			AudioChannels:   2,
			SourceProbe:     &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 24.0, AudioRate: 48000},
		},
		Modeline:      modeline,
		FieldWidth:    fieldWidth,
		FieldHeight:   fieldHeight,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
		Codec:         codec,
		NLCNear:       near,
		NLCPack:       pack,
		AudioRate:     48000,
		AudioChans:    2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), runFor)
	defer cancel()
	planeDone := make(chan error, 1)
	go func() { planeDone <- plane.Run(ctx) }()

	// A single select loop is the only consumer of events/fieldsCh/audios,
	// so dec (not goroutine-safe) and res are touched from one goroutine
	// only. RunWithFields is one sequential producer, so a field's header
	// Command is always enqueued to events before that field's payload is
	// enqueued to fieldsCh, and INIT always precedes the first BLIT on the
	// wire — but events and fieldsCh are separate channels, so the select
	// below can still hand us the field first. handleField drains events
	// itself before checking sawInit so dec.SetInit/SetDims are applied
	// before the first Decode regardless of which case the select picks.
	res := &nlcSessionResult{}
	dec := fakemister.NewFieldDecoder()
	handleCmd := func(cmd fakemister.Command) {
		switch cmd.Type {
		case groovy.CmdInit:
			if !res.sawInit {
				res.sawInit = true
				res.initByte = cmd.Init.LZ4Frames
				dec.SetInit(res.initByte)
				dec.SetDims(fieldWidth, fieldHeight)
			}
		case groovy.CmdBlitFieldVSync:
			res.blits++
			if len(cmd.Raw) != groovy.BlitHeaderLZ4 {
				res.nonLZ4Headers++
			}
		}
	}
	handleField := func(fe fakemister.FieldEvent) {
		// events and fieldsCh are separate channels, and the select below
		// picks pseudo-randomly among ready cases, so a field's arrival can
		// win the race even though its header's INIT was enqueued to events
		// first (RunWithFields is one sequential producer, but consumption
		// order across two channels isn't). Drain any pending events
		// non-blockingly first, processing INIT/SWITCHRES exactly as the
		// main loop does, so real ordering is reflected before we judge it.
		for drained := false; !drained; {
			select {
			case cmd := <-events:
				handleCmd(cmd)
			default:
				drained = true
			}
		}
		if !res.sawInit {
			t.Fatalf("field arrived before INIT")
		}
		out, err := dec.Decode(fe, fieldBytes)
		if err != nil {
			if res.decodeErr == nil {
				res.decodeErr = err
			}
			return
		}
		res.decoded++
		if res.firstField == nil {
			res.firstField = append([]byte(nil), out...)
		} else if !res.fieldsDiffer && !bytes.Equal(out, res.firstField) {
			res.fieldsDiffer = true
		}
	}

loop:
	for {
		select {
		case cmd := <-events:
			handleCmd(cmd)
		case fe := <-fieldsCh:
			handleField(fe)
		case <-audios:
		case <-planeDone:
			break loop
		case <-time.After(runFor + 5*time.Second):
			t.Fatal("plane did not finish within the expected window")
		}
	}
	// Trailing datagrams.
	settle := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case cmd := <-events:
			handleCmd(cmd)
		case fe := <-fieldsCh:
			handleField(fe)
		case <-audios:
		case <-settle:
			break drain
		}
	}

	res.effectiveCodec = plane.EffectiveCodec()
	return res
}

// TestNLC_GroovyNLCCore_NTSC240p covers design §6.4 Part 2's first hardware
// target: a progressive NLC session against a GroovyNLC-reporting core.
func TestNLC_GroovyNLCCore_NTSC240p(t *testing.T) {
	res := runNLCSession(t, 2, dataplane.CodecNLC, 0, nlc.PackTiled,
		groovy.NTSC240p60, "progressive", "60000/1001", 3*time.Second)

	if res.effectiveCodec != dataplane.CodecNLC {
		t.Errorf("EffectiveCodec() = %q, want nlc", res.effectiveCodec)
	}
	if !res.sawInit {
		t.Fatal("no INIT observed")
	}
	if want := groovy.NLCCompressionByte(0, false); res.initByte != want {
		t.Errorf("INIT[1] = %#x, want %#x", res.initByte, want)
	}
	if res.blits == 0 {
		t.Fatal("no BLIT_FIELD_VSYNC headers observed")
	}
	if res.nonLZ4Headers != 0 {
		t.Errorf("%d of %d BLIT headers were not the 12-byte compressed form", res.nonLZ4Headers, res.blits)
	}
	if res.decodeErr != nil {
		t.Errorf("field decode error: %v", res.decodeErr)
	}
	if res.decoded < 60 {
		t.Errorf("decoded %d fields, want >= 60", res.decoded)
	}
	if !res.fieldsDiffer {
		t.Error("decoded fields never changed; testsrc content should move")
	}
}

// TestNLC_GroovyNLCCore_NTSC480i covers the same checks under NTSC 480i
// (interlaced), design §1.4's open risk — verified here only on the host
// side (wire shape and decodability), not against real GroovyNLC hardware.
func TestNLC_GroovyNLCCore_NTSC480i(t *testing.T) {
	res := runNLCSession(t, 2, dataplane.CodecNLC, 0, nlc.PackTiled,
		groovy.NTSC480i60, "tff", "", 3*time.Second)

	if res.effectiveCodec != dataplane.CodecNLC {
		t.Errorf("EffectiveCodec() = %q, want nlc", res.effectiveCodec)
	}
	if !res.sawInit {
		t.Fatal("no INIT observed")
	}
	if want := groovy.NLCCompressionByte(0, false); res.initByte != want {
		t.Errorf("INIT[1] = %#x, want %#x", res.initByte, want)
	}
	if res.blits == 0 {
		t.Fatal("no BLIT_FIELD_VSYNC headers observed")
	}
	if res.nonLZ4Headers != 0 {
		t.Errorf("%d of %d BLIT headers were not the 12-byte compressed form", res.nonLZ4Headers, res.blits)
	}
	if res.decodeErr != nil {
		t.Errorf("field decode error: %v", res.decodeErr)
	}
	if res.decoded < 60 {
		t.Errorf("decoded %d fields, want >= 60", res.decoded)
	}
	if !res.fieldsDiffer {
		t.Error("decoded fields never changed; testsrc content should move")
	}
}

// TestNLC_OriginalCore_FallsBackToLZ4 covers design §4.2: codec nlc against
// the original core resolves to lz4, and the wire reflects it.
func TestNLC_OriginalCore_FallsBackToLZ4(t *testing.T) {
	res := runNLCSession(t, 1, dataplane.CodecNLC, 2, nlc.PackRice,
		groovy.NTSC240p60, "progressive", "60000/1001", 3*time.Second)

	if res.effectiveCodec != dataplane.CodecLZ4 {
		t.Errorf("EffectiveCodec() = %q, want lz4", res.effectiveCodec)
	}
	if !res.sawInit {
		t.Fatal("no INIT observed")
	}
	if res.initByte != groovy.LZ4ModeDefault {
		t.Errorf("INIT[1] = %#x, want %#x (lz4)", res.initByte, groovy.LZ4ModeDefault)
	}
	if res.blits == 0 {
		t.Fatal("no BLIT_FIELD_VSYNC headers observed")
	}
	if res.decodeErr != nil {
		t.Errorf("field decode error (as lz4): %v", res.decodeErr)
	}
	if res.decoded < 60 {
		t.Errorf("decoded %d fields, want >= 60", res.decoded)
	}
}
