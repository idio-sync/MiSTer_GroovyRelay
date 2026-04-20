package dataplane

import (
	"net"
	"testing"
	"time"

	cryptorand "crypto/rand"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// TestRateCodeForHz locks the integer→wire-enum mapping the INIT handshake
// depends on. Unknown rates fall through to AudioRateOff — callers are
// expected to validate config upstream.
func TestRateCodeForHz(t *testing.T) {
	cases := []struct {
		hz   int
		want byte
	}{
		{22050, groovy.AudioRate22050},
		{44100, groovy.AudioRate44100},
		{48000, groovy.AudioRate48000},
		{0, groovy.AudioRateOff},
		{16000, groovy.AudioRateOff},
	}
	for _, c := range cases {
		if got := rateCodeForHz(c.hz); got != c.want {
			t.Errorf("rateCodeForHz(%d) = %d, want %d", c.hz, got, c.want)
		}
	}
}

// TestNewPlane_PreservesConfig confirms the constructor stashes config
// verbatim and exposes a Done channel that is open until Run completes.
func TestNewPlane_PreservesConfig(t *testing.T) {
	cfg := PlaneConfig{
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
		LZ4Enabled:    true,
		AudioRate:     48000,
		AudioChans:    2,
		SeekOffsetMs:  12345,
	}
	p := NewPlane(cfg)
	if p.cfg.FieldWidth != 720 || p.cfg.FieldHeight != 240 {
		t.Errorf("config not preserved: %+v", p.cfg)
	}
	// Position reflects cfg.SeekOffsetMs from construction (field counter
	// is 0, so Position == baseOffset). Run doesn't re-seed it.
	wantStart := time.Duration(cfg.SeekOffsetMs) * time.Millisecond
	if p.Position() != wantStart {
		t.Errorf("pre-Run Position = %v, want %v", p.Position(), wantStart)
	}
	select {
	case <-p.Done():
		t.Fatal("Done channel closed before Run")
	default:
	}
}

// TestSendField_RawFallbackOnIncompressible verifies that when the LZ4
// compressor returns ok=false (incompressible input), sendField emits an
// 8-byte RAW BLIT header — not a 12-byte LZ4 header with CompressedSize=0.
// This is the regression harness for C3: the LZ4 header variant is invalid
// on the wire when CompressedSize=0, and an earlier bug allowed that.
func TestSendField_RawFallbackOnIncompressible(t *testing.T) {
	// Stand up a loopback UDP listener as the "MiSTer"; capture datagrams.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	p := &Plane{cfg: PlaneConfig{Sender: sender, LZ4Enabled: true}}

	// Random bytes — LZ4Compress will return ok=false for a 518 400-byte
	// crypto/rand field.
	field := make([]byte, 720*240*3)
	if _, err := cryptorand.Read(field); err != nil {
		t.Fatal(err)
	}

	done := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 4096)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				close(done)
				return
			}
			cp := make([]byte, n)
			copy(cp, buf[:n])
			done <- cp
		}
	}()

	p.sendField(0, 0, field)

	// The first datagram is the BLIT header. Expect 8 bytes (RAW), not 12
	// (LZ4).
	hdr, ok := <-done
	if !ok {
		t.Fatal("no header datagram received")
	}
	if len(hdr) != groovy.BlitHeaderRaw {
		t.Errorf("got header length %d, want %d (RAW variant)", len(hdr), groovy.BlitHeaderRaw)
	}
	if hdr[0] != groovy.CmdBlitFieldVSync {
		t.Errorf("header[0] = %#x, want CmdBlitFieldVSync %#x", hdr[0], groovy.CmdBlitFieldVSync)
	}
}

// TestPosition_IntegerExactFieldCount verifies that after N ticks Position()
// returns exactly N*1001/60 ms plus the base offset. Regression harness for
// I4 — the old code added 16 ms/tick and drifted ~0.68 ms low per field.
func TestPosition_IntegerExactFieldCount(t *testing.T) {
	cases := []struct {
		ticks        int64
		baseOffsetMs int
		wantPosMs    int64
	}{
		{3600, 0, 60_060},              // 60.06 s of playback at 59.94 Hz
		{60_000, 0, 1_001_000},         // ~16.68 min
		{600, 5_000, 5_000 + 10_010},   // 10 s of playback, resumed at 5 s
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			p := &Plane{}
			p.cfg.SeekOffsetMs = tc.baseOffsetMs
			p.resetPosition()
			for i := int64(0); i < tc.ticks; i++ {
				p.advancePosition()
			}
			got := p.Position()
			wantDur := time.Duration(tc.wantPosMs) * time.Millisecond
			if got != wantDur {
				t.Errorf("ticks=%d offset=%d: Position=%v, want %v",
					tc.ticks, tc.baseOffsetMs, got, wantDur)
			}
		})
	}
}
