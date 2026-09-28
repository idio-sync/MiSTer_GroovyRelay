//go:build integration

package integration

import (
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

// TestOSD_OverlayReachesTheWireIntactAndExpires casts a 5 s clip through
// real ffmpeg with the OSD on, decodes every field fake-mister receives
// (LZ4 + delta-LZ4 reversed), and compares against a reference render of
// the same overlay:
//
//   - an early field must carry every overlay pixel byte-for-byte (the
//     stamp lands after field extraction and survives compression);
//   - the last field, sent after every element's duration has elapsed,
//     must no longer carry the coloured glyph pixels.
//
// Set OSD_DUMP_DIR to also write both fields as PNGs for eyeballing.
func TestOSD_OverlayReachesTheWireIntactAndExpires(t *testing.T) {
	sample := ensureSampleMP4(t, "5s.mp4", 5)

	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.EnableACKs(true)
	addr := l.Addr().(*net.UDPAddr)

	const (
		width, fieldHeight = 720, 240
		fieldBytes         = width * fieldHeight * 3
		// Fields early enough that every element (shortest: 3 s) is still up
		// even with slow ffmpeg startup, late enough to be past prebuffer.
		earlyFrom, earlyTo = 20, 60
		// The last field must be sent after ChannelDuration (4 s, the
		// longest). The pump never runs ahead of the 59.94 Hz field clock
		// and its first tick follows the announcement, so frame 250 goes out
		// at least ~4.15 s after the banner appeared.
		minLastFrame = 250
	)

	events := make(chan fakemister.Command, 4096)
	fieldEvents := make(chan fakemister.FieldEvent, 64)
	audioEvents := make(chan fakemister.AudioEvent, 256)
	decoder := fakemister.NewFieldDecoder()

	var (
		mu         sync.Mutex
		early      []byte
		earlyFrame uint32
		last       = make([]byte, fieldBytes)
		lastFrame  uint32
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
			case <-events:
			case <-audioEvents:
			case fe := <-fieldEvents:
				field, err := decoder.Decode(fe, fieldBytes)
				if err != nil {
					decodeErrs.Add(1)
					continue
				}
				mu.Lock()
				if early == nil && fe.Header.Frame >= earlyFrom && fe.Header.Frame <= earlyTo {
					early = append([]byte(nil), field...)
					earlyFrame = fe.Header.Frame
				}
				if fe.Header.Frame >= lastFrame {
					copy(last, field)
					lastFrame = fe.Header.Frame
				}
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
			Codec:          config.CodecAuto,
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

	if err := mgr.SetOutputVolume(40); err != nil {
		t.Fatalf("SetOutputVolume: %v", err)
	}
	req := defaultRequest(sample, "osd-clip")
	req.ChannelLabel = "CH 07"
	if err := mgr.StartSession(req); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for mgr.Status().State != core.StateIdle {
		if time.Now().After(deadline) {
			t.Fatal("cast did not finish within 15 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // trailing datagrams

	mu.Lock()
	defer mu.Unlock()
	if early == nil {
		t.Fatalf("no field in frames %d..%d decoded (decode errors: %d)", earlyFrom, earlyTo, decodeErrs.Load())
	}
	if lastFrame < minLastFrame {
		t.Fatalf("last field was frame %d, want >= %d to test expiry", lastFrame, minLastFrame)
	}
	if dir := os.Getenv("OSD_DUMP_DIR"); dir != "" {
		d := fakemister.NewDumper(dir, 1)
		_ = d.MaybeDumpField(earlyFrame, width, fieldHeight, early)
		_ = d.MaybeDumpField(lastFrame, width, fieldHeight, last)
		t.Logf("dumped frames %d and %d to %s", earlyFrame, lastFrame, dir)
	}

	// Reference render of the overlay the Manager should have drawn.
	const sentinel = 0x5A
	ref := osd.Canvas{Pix: make([]byte, fieldBytes), Width: width, Height: fieldHeight}
	for i := range ref.Pix {
		ref.Pix[i] = sentinel
	}
	refNow := time.Now()
	refDisplay := osd.NewDisplay(osd.Options{Enabled: true})
	refDisplay.ShowVolume(40, false, refNow)
	refDisplay.ShowChannel("CH 07", refNow)
	refDisplay.ShowTransport(osd.TransportPlay, refNow)
	refDisplay.Draw(ref, refNow)

	var overlay, earlyMatch, glyph, lastGlyphMatch int
	for p := 0; p < fieldBytes; p += 3 {
		px := ref.Pix[p : p+3]
		if px[0] == sentinel && px[1] == sentinel && px[2] == sentinel {
			continue
		}
		overlay++
		if early[p] == px[0] && early[p+1] == px[1] && early[p+2] == px[2] {
			earlyMatch++
		}
		if px[0] == 0 && px[1] == 0 && px[2] == 0 {
			continue // black outline: indistinguishable from letterbox bars
		}
		glyph++
		if last[p] == px[0] && last[p+1] == px[1] && last[p+2] == px[2] {
			lastGlyphMatch++
		}
	}
	if overlay == 0 || glyph == 0 {
		t.Fatal("reference render drew nothing")
	}
	if earlyMatch != overlay {
		t.Errorf("frame %d carries %d of %d overlay pixels exactly, want all", earlyFrame, earlyMatch, overlay)
	}
	if lastGlyphMatch*10 > glyph {
		t.Errorf("frame %d (after expiry) still carries %d of %d glyph pixels, want < 10%%", lastFrame, lastGlyphMatch, glyph)
	}
	t.Logf("frame %d: %d/%d overlay pixels exact; frame %d: %d/%d glyph pixels remain",
		earlyFrame, earlyMatch, overlay, lastFrame, lastGlyphMatch, glyph)
}
