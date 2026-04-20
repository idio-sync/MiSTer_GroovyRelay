//go:build integration

package integration

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// TestScenario_PixelVariance exercises the full fakemister.RunWithFields
// path — BLIT header parse → payload reassembly → LZ4 decompress → PNG
// dump via fakemister.Dumper — and then asserts that at least one dumped
// PNG has non-trivial pixel variance. This is the integration-level
// equivalent of "the stream is real pixels, not black frames."
//
// The dumper writes a PNG every 30 fields (~twice per second at 59.94
// Hz), so even a 5 s clip produces several samples. assertPixelVariance
// checks the R-channel variance is > 100, which any real content easily
// clears.
//
// Inter-field timing is also asserted here so the one scenario that
// captures BLIT timestamps exercises both the variance and timing sides
// of §8.2.
func TestScenario_PixelVariance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}

	sample := ensureSampleMP4(t, "5s.mp4", 5)

	// Per-test dump directory under the test's tempdir so artifacts are
	// auto-cleaned and concurrent tests don't clobber each other.
	dumpDir := filepath.Join(t.TempDir(), "dumps")
	if err := os.MkdirAll(dumpDir, 0o755); err != nil {
		t.Fatalf("mkdir dumps: %v", err)
	}
	dumper := fakemister.NewDumper(dumpDir, 30)

	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.EnableACKs(true)
	addr := l.Addr().(*net.UDPAddr)

	events := make(chan fakemister.Command, 4096)
	fieldEvents := make(chan fakemister.FieldEvent, 64)
	audioEvents := make(chan fakemister.AudioEvent, 256)
	rec := fakemister.NewRecorder()

	// Session state carried across events (same shape as cmd/fake-mister).
	var (
		initPayload atomic.Pointer[fakemister.InitPayload]
		modelinePtr atomic.Pointer[fakemister.SwitchresPayload]
	)

	fieldSize := func() uint32 {
		ml := modelinePtr.Load()
		if ml == nil {
			return 0
		}
		bpp := 3
		if ip := initPayload.Load(); ip != nil {
			switch ip.RGBMode {
			case groovy.RGBMode8888:
				bpp = 4
			case groovy.RGBMode565:
				bpp = 2
			}
		}
		return uint32(int(ml.HActive) * int(ml.VActive) * bpp)
	}

	pumpDone := make(chan struct{})
	runDone := make(chan struct{})
	stop := make(chan struct{})

	// Recorder + state-sink fan-in goroutine. Exits when stop is closed.
	// Closing the RunWithFields channels from this side is unsafe (the
	// listener goroutine writes into them) so we drive shutdown via stop
	// and just drop whatever is in the channels at that point.
	go func() {
		defer close(pumpDone)
		for {
			select {
			case <-stop:
				return
			case cmd := <-events:
				rec.Record(cmd)
				switch cmd.Type {
				case groovy.CmdInit:
					initPayload.Store(cmd.Init)
				case groovy.CmdSwitchres:
					modelinePtr.Store(cmd.Switchres)
				}
			case fe := <-fieldEvents:
				payload := fe.Payload
				if fe.Header.Compressed {
					raw, err := groovy.LZ4Decompress(payload, int(fieldSize()))
					if err != nil {
						continue
					}
					payload = raw
				}
				if ml := modelinePtr.Load(); ml != nil {
					_ = dumper.MaybeDumpField(fe.Header.Frame,
						int(ml.HActive), int(ml.VActive), payload)
				}
			case ae := <-audioEvents:
				_ = dumper.WriteAudio(ae.PCM)
			}
		}
	}()

	go func() {
		l.RunWithFields(events, fieldEvents, audioEvents, fieldSize)
		close(runDone)
	}()

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}

	cfg := &config.Config{
		MisterHost:          "127.0.0.1",
		MisterPort:          addr.Port,
		SourcePort:          0,
		Modeline:            "NTSC_480i",
		InterlaceFieldOrder: "tff",
		AspectMode:          "letterbox",
		RGBMode:             "rgb888",
		LZ4Enabled:          true,
		AudioSampleRate:     48000,
		AudioChannels:       2,
	}
	mgr := core.NewManager(cfg, sender)
	t.Cleanup(func() {
		_ = mgr.Stop()
		sender.Close()
		l.Close()
		<-runDone
		close(stop)
		<-pumpDone
	})

	if err := mgr.StartSession(core.SessionRequest{
		StreamURL:    sample,
		AdapterRef:   "variance-clip",
		DirectPlay:   true,
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Drive to completion.
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Status().State == core.StateIdle {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	// Collect PNGs dumped by the helper. With sampleEvery=30 and ~600
	// BLITs (5 s × ~60 Hz × 2 field passes per frame, counted as distinct
	// increments in the reference sender — actual count ~300 frames ⇒ 10
	// samples on average), we need at least one. If zero exist, the
	// pipeline never produced a decodable field.
	matches, err := filepath.Glob(filepath.Join(dumpDir, "field_*.png"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("no PNGs dumped in %s — pipeline produced no decodable fields", dumpDir)
	}
	assertPixelVariance(t, matches[0])

	snap := rec.Snapshot()
	assertInterFieldTiming(t, snap.FieldTimestamps)
}
