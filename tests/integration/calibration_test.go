//go:build integration

package integration

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/calibration"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

type recordingPictureSaver struct {
	mu    sync.Mutex
	saved []config.PictureGeometry
}

func (s *recordingPictureSaver) SavePicture(g config.PictureGeometry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, g)
	return nil
}

// TestCalibration_PreviewMovesPatternWithoutRestart runs the calibration
// controller against a real core.Manager and fake-mister: the test pattern
// reaches the wire through the frame-source seam (no ffmpeg), a preview
// moves its border on later fields without a second INIT, and Save stops
// the session and persists the draft.
func TestCalibration_PreviewMovesPatternWithoutRestart(t *testing.T) {
	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.EnableACKs(true)
	addr := l.Addr().(*net.UDPAddr)

	const (
		width, fieldHeight = 720, 240
		fieldBytes         = width * fieldHeight * 3
		probeRow           = 110 // field row clear of grid lines and the circle
	)
	white := []byte{235, 235, 235}

	events := make(chan fakemister.Command, 4096)
	fieldEvents := make(chan fakemister.FieldEvent, 64)
	audioEvents := make(chan fakemister.AudioEvent, 16)
	decoder := fakemister.NewFieldDecoder()
	var (
		mu         sync.Mutex
		latest     []byte
		inits      atomic.Int32
		audio      atomic.Int32
		decodeErrs atomic.Int32
	)
	stop := make(chan struct{})
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for {
			select {
			case <-stop:
				return
			case cmd := <-events:
				if cmd.Type == groovy.CmdInit {
					inits.Add(1)
				}
			case <-audioEvents:
				audio.Add(1)
			case fe := <-fieldEvents:
				field, err := decoder.Decode(fe, fieldBytes)
				if err != nil {
					decodeErrs.Add(1)
					continue
				}
				mu.Lock()
				latest = append(latest[:0], field...)
				mu.Unlock()
			}
		}
	}()
	runDone := make(chan struct{})
	go func() {
		l.RunWithFields(events, fieldEvents, audioEvents, func() uint32 { return fieldBytes })
		close(runDone)
	}()

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	bridge := config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: "127.0.0.1", Port: addr.Port},
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			RGBMode:             "rgb888",
			LZ4Enabled:          true,
			DeltaLZ4Enabled:     true,
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2, OutputVolume: 100},
		OSD:   config.OSDConfig{Enabled: true},
	}
	mgr := core.NewManager(bridge, sender, core.WithOSD(osd.NewDisplay(osd.Options{})))
	t.Cleanup(func() {
		_ = mgr.Stop()
		sender.Close()
		l.Close()
		<-runDone
		close(stop)
		<-pumpDone
	})

	saver := &recordingPictureSaver{}
	ctl := calibration.NewController(mgr, saver, func() config.BridgeConfig { return bridge })

	// waitForField polls decoded fields until match holds for one.
	waitForField := func(what string, match func(field []byte) bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			ok := latest != nil && match(latest)
			mu.Unlock()
			if ok {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s (decode errors: %d)", what, decodeErrs.Load())
	}
	px := func(field []byte, x int) []byte {
		i := (probeRow*width + x) * 3
		return field[i : i+3]
	}
	isWhite := func(b []byte) bool { return b[0] == white[0] && b[1] == white[1] && b[2] == white[2] }
	isBlack := func(b []byte) bool { return b[0] == 0 && b[1] == 0 && b[2] == 0 }

	if err := ctl.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForField("full-raster border at column 0", func(f []byte) bool { return isWhite(px(f, 0)) })

	g := config.PictureGeometry{HSize: 90, VSize: 90}
	if err := ctl.Preview(g); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	r := g.Rect(720, 480, true)
	waitForField("border moved to the 90% rect", func(f []byte) bool {
		return isBlack(px(f, 0)) && isWhite(px(f, r.X)) && isWhite(px(f, r.X+1))
	})
	if n := inits.Load(); n != 1 {
		t.Fatalf("INIT sent %d times, want 1 (preview must not restart the session)", n)
	}
	if n := audio.Load(); n != 0 {
		t.Fatalf("calibration sent %d audio packets, want 0", n)
	}

	if err := ctl.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for mgr.Status().State != core.StateIdle {
		if time.Now().After(deadline) {
			t.Fatal("Save did not stop the calibration session")
		}
		time.Sleep(20 * time.Millisecond)
	}
	saver.mu.Lock()
	defer saver.mu.Unlock()
	if len(saver.saved) != 1 || saver.saved[0] != g {
		t.Fatalf("saved = %+v, want [%+v]", saver.saved, g)
	}
	if snap := ctl.Snapshot(); snap.State != calibration.StateIdle {
		t.Fatalf("controller state after Save = %s, want idle", snap.State)
	}
}
