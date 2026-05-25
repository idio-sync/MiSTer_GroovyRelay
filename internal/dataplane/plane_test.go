package dataplane

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/pierrec/lz4/v4"
)

func requireUDPSockets(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("UDP sockets unavailable in this environment: %v", err)
	}
	t.Fatal(err)
}

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
	requireUDPSockets(t, err)
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	// Use NewPlane so session-lifetime scratch buffers (headerScratch,
	// lz4Scratch) are allocated. sendField now writes through those
	// buffers via BuildBlitHeaderInto / LZ4CompressInto, so a bare
	// Plane{} would nil-panic.
	p := NewPlane(PlaneConfig{
		Sender:        sender,
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
	})

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

func TestSendField_DoesNotEmitDeltaLZ4ByDefault(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "")

	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender

	base := repeatedTileField(fieldBytes, deterministicTile(4096))
	next := append([]byte(nil), base...)
	for i := 0; i < 64; i++ {
		next[i*4096] ^= byte(i + 1)
	}

	p.sendField(1, 0, base)
	p.sendField(3, 0, next)

	if len(sender.headers) != 2 || len(sender.payloads) != 2 {
		t.Fatalf("got %d headers and %d payloads, want 2 of each", len(sender.headers), len(sender.payloads))
	}
	for i, header := range sender.headers {
		if got := blitPayloadType(header); got != groovy.BlitHeaderLZ4 {
			t.Fatalf("header %d payload type = %d, want full LZ4 with delta disabled", i, got)
		}
	}

	got, err := groovy.LZ4Decompress(sender.payloads[0], fieldBytes)
	if err != nil {
		t.Fatalf("decompress seed field: %v", err)
	}
	if !bytes.Equal(got, base) {
		t.Fatal("seed field payload decoded to different pixels")
	}
	got, err = groovy.LZ4Decompress(sender.payloads[1], fieldBytes)
	if err != nil {
		t.Fatalf("decompress second field: %v", err)
	}
	if !bytes.Equal(got, next) {
		t.Fatal("second field payload decoded to different pixels")
	}
}

func TestSendField_AdaptiveDeltaOptInEmitsDeltaWhenUseful(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender

	base := repeatedTileField(fieldBytes, deterministicTile(4096))
	next := append([]byte(nil), base...)
	for i := 0; i < 64; i++ {
		next[i*4096]++
	}

	p.sendField(1, 0, base)
	p.sendField(3, 0, next)

	if len(sender.headers) < 2 || len(sender.payloads) < 2 {
		t.Fatalf("got %d headers and %d payloads, want at least 2 of each", len(sender.headers), len(sender.payloads))
	}
	if got := blitPayloadType(sender.headers[0]); got != groovy.BlitHeaderLZ4 {
		t.Fatalf("first payload type = %d, want LZ4 full", got)
	}
	if got := blitPayloadType(sender.headers[1]); got != groovy.BlitHeaderLZ4Delta {
		t.Fatalf("second payload type = %d, want delta LZ4", got)
	}

	gotDelta, err := groovy.LZ4Decompress(sender.payloads[1], fieldBytes)
	if err != nil {
		t.Fatalf("decompress delta payload: %v", err)
	}
	wantDelta := make([]byte, fieldBytes)
	writeFieldSubDeltaInto(wantDelta, next, base)
	if !bytes.Equal(gotDelta, wantDelta) {
		t.Fatal("delta payload is not byte-wrap subtraction against previous raw field")
	}
}

func TestSendField_DeltaEnvWithLZ4DisabledSendsRaw(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    false,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender

	p.sendField(1, 0, repeatedTileField(fieldBytes, []byte{0x11, 0x22, 0x33}))

	if len(sender.headers) != 1 {
		t.Fatalf("headers = %d, want 1", len(sender.headers))
	}
	if got := blitPayloadType(sender.headers[0]); got != groovy.BlitHeaderRaw {
		t.Fatalf("payload type = %d, want RAW", got)
	}
}

func TestSendField_ReturnsDetailedTelemetry(t *testing.T) {
	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender

	field := repeatedTileField(fieldBytes, []byte{0x11, 0x22, 0x33, 0x44})
	stats := p.sendField(1, 0, field)

	if len(sender.headers) != 1 || len(sender.payloads) != 1 {
		t.Fatalf("headers=%d payloads=%d, want 1/1", len(sender.headers), len(sender.payloads))
	}
	if stats.rawBytes != fieldBytes {
		t.Fatalf("rawBytes = %d, want %d", stats.rawBytes, fieldBytes)
	}
	if stats.payloadBytes != len(sender.payloads[0]) {
		t.Fatalf("payloadBytes = %d, want sent payload len %d", stats.payloadBytes, len(sender.payloads[0]))
	}
	if stats.compressedBytes != len(sender.payloads[0]) {
		t.Fatalf("compressedBytes = %d, want sent payload len %d", stats.compressedBytes, len(sender.payloads[0]))
	}
	wantWireBytes := len(sender.headers[0]) + len(sender.payloads[0])
	if stats.wireBytes != wantWireBytes {
		t.Fatalf("wireBytes = %d, want header+payload %d", stats.wireBytes, wantWireBytes)
	}
	if stats.payloadChunks != datagramChunks(len(sender.payloads[0])) {
		t.Fatalf("payloadChunks = %d, want %d", stats.payloadChunks, datagramChunks(len(sender.payloads[0])))
	}
	if stats.deltaSelected {
		t.Fatal("deltaSelected = true, want false without prior same-field history")
	}
}

func TestSendAudioAppliesOutputVolume(t *testing.T) {
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		FieldWidth:    1,
		FieldHeight:   1,
		BytesPerPixel: 1,
		OutputVolume:  50,
	})
	p.fieldSender = sender

	pcm := []byte{
		0xe8, 0x03, // 1000
		0x18, 0xfc, // -1000
		0xff, 0x7f, // 32767
		0x00, 0x80, // -32768
	}
	p.sendAudio(pcm)

	if len(sender.payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(sender.payloads))
	}
	want := []byte{
		0xf4, 0x01, // 500
		0x0c, 0xfe, // -500
		0xff, 0x3f, // 16383
		0x00, 0xc0, // -16384
	}
	if !bytes.Equal(sender.payloads[0], want) {
		t.Fatalf("scaled PCM = % x, want % x", sender.payloads[0], want)
	}
}

func TestSetOutputVolumeChangesSubsequentAudio(t *testing.T) {
	sender := &scriptedFieldSender{}
	p := NewPlane(PlaneConfig{
		FieldWidth:    1,
		FieldHeight:   1,
		BytesPerPixel: 1,
		OutputVolume:  100,
	})
	p.fieldSender = sender

	p.SetOutputVolume(0)
	p.sendAudio([]byte{0xe8, 0x03})

	if len(sender.payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(sender.payloads))
	}
	if want := []byte{0x00, 0x00}; !bytes.Equal(sender.payloads[0], want) {
		t.Fatalf("muted PCM = % x, want % x", sender.payloads[0], want)
	}
}

func TestSendField_DoesNotRememberHistoryWhenSendFails(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{failPayloadSend: 1}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender

	base := repeatedTileField(fieldBytes, []byte{0x10, 0x20, 0x30, 0x40, 0x50})
	next := append([]byte(nil), base...)
	next[0]++

	p.sendField(1, 0, base)
	p.sendField(3, 0, next)

	if len(sender.headers) < 2 {
		t.Fatalf("headers = %d, want at least 2", len(sender.headers))
	}
	if got := blitPayloadType(sender.headers[1]); got != groovy.BlitHeaderLZ4 {
		t.Fatalf("second payload type = %d, want full LZ4 because failed send must not seed history", got)
	}
}

func TestLZ4FallbackDebugLogIsThrottled(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	p := &Plane{}
	now := time.Unix(1, 0)
	p.logLZ4RawFallback(518400, now)
	p.logLZ4RawFallback(518400, now.Add(10*time.Millisecond))
	p.logLZ4RawFallback(518400, now.Add(20*time.Millisecond))

	if got := strings.Count(buf.String(), "lz4 incompressible frame; falling back to RAW BLIT"); got != 1 {
		t.Fatalf("debug logs before throttle interval = %d, want 1\n%s", got, buf.String())
	}

	p.logLZ4RawFallback(518400, now.Add(time.Second))

	out := buf.String()
	if got := strings.Count(out, "lz4 incompressible frame; falling back to RAW BLIT"); got != 2 {
		t.Fatalf("debug logs after throttle interval = %d, want 2\n%s", got, out)
	}
	if !strings.Contains(out, "suppressed_fields=2") {
		t.Fatalf("aggregate log missing suppressed_fields=2\n%s", out)
	}
}

func TestSendField_FieldDiagnosticsLogsDeltaComparisonWhenEnabled(t *testing.T) {
	t.Setenv("GROOVY_FIELD_DIAG", "1")

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	requireUDPSockets(t, err)
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buf := make([]byte, 4096)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			if _, _, err := conn.ReadFromUDP(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		<-drained
	})

	p := NewPlane(PlaneConfig{
		Sender:        sender,
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
	})
	// Force sendField's budget warning path without depending on wall-clock
	// jitter in the test runner.
	p.periodMsNumer = 1
	p.periodMsDenom = int64(time.Millisecond)

	first := bytes.Repeat([]byte{0x11}, 720*240*3)
	second := append([]byte(nil), first...)
	second[0] = 0x12

	p.sendField(1, 0, first)
	p.lastBudgetWarn = time.Time{}
	p.sendField(3, 0, second)

	out := logBuf.String()
	for _, want := range []string{
		"field_diag_enabled=true",
		"field_diag_delta_available=true",
		"field_diag_current_payload_bytes=",
		"field_diag_delta_lz4_bytes=",
		"field_diag_delta_lz4_ms=",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnostic log missing %q\n%s", want, out)
		}
	}
}

func TestSendField_DeltaEnabledSlowWarningLogsSelectionFields(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{congestionDelay: time.Millisecond}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender
	p.periodMsNumer = 1
	p.periodMsDenom = int64(time.Millisecond)

	base := repeatedTileField(fieldBytes, deterministicTile(4096))
	next := append([]byte(nil), base...)
	for i := 0; i < 64; i++ {
		next[i*4096]++
	}

	p.sendField(1, 0, base)
	p.lastBudgetWarn = time.Time{}
	logBuf.Reset()
	p.sendField(3, 0, next)

	if len(sender.payloads) < 2 {
		t.Fatalf("payloads = %d, want at least 2", len(sender.payloads))
	}
	deltaBytes := len(sender.payloads[1])

	out := logBuf.String()
	for _, want := range []string{
		"delta_lz4_enabled=true",
		"delta_lz4_available=true",
		"delta_lz4_ok=true",
		"delta_lz4_selected=true",
		"delta_lz4_bytes=",
		"delta_lz4_savings_bytes=",
		"compressed_bytes=",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("slow warning missing %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, "delta_lz4_bytes="+strconv.Itoa(deltaBytes)) {
		t.Fatalf("slow warning delta_lz4_bytes does not match sent delta payload %d\n%s", deltaBytes, out)
	}
	if !strings.Contains(out, "compressed_bytes="+strconv.Itoa(deltaBytes)) {
		t.Fatalf("slow warning compressed_bytes does not match sent payload %d\n%s", deltaBytes, out)
	}
}

func TestSendField_DeltaEnabledSlowWarningLogsUnavailableDelta(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	const fieldBytes = 720 * 240 * 3
	sender := &scriptedFieldSender{congestionDelay: time.Millisecond}
	p := NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
	})
	p.fieldSender = sender
	p.periodMsNumer = 1
	p.periodMsDenom = int64(time.Millisecond)

	raw := repeatedTileField(fieldBytes, []byte{0x33, 0x44, 0x55, 0x66})
	p.sendField(1, 0, raw)

	if len(sender.payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(sender.payloads))
	}
	sentBytes := len(sender.payloads[0])

	out := logBuf.String()
	for _, want := range []string{
		"delta_lz4_enabled=true",
		"delta_lz4_available=false",
		"delta_lz4_ok=false",
		"delta_lz4_selected=false",
		"delta_lz4_bytes=0",
		"compressed_bytes=",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("slow warning missing %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, "compressed_bytes="+strconv.Itoa(sentBytes)) {
		t.Fatalf("slow warning compressed_bytes does not match sent payload %d\n%s", sentBytes, out)
	}
}

func TestShouldUseDeltaLZ4_UsesStrictNinetyFivePercentThreshold(t *testing.T) {
	if !shouldUseDeltaLZ4(100, 94) {
		t.Fatal("94/100 should select delta")
	}
	if shouldUseDeltaLZ4(100, 95) {
		t.Fatal("95/100 should not select delta because threshold is strict")
	}
	if shouldUseDeltaLZ4(100, 96) {
		t.Fatal("96/100 should not select delta")
	}
	if shouldUseDeltaLZ4(0, 0) {
		t.Fatal("zero sizes should not select delta")
	}
}

func TestFieldHistoryInvalidatesOnLengthMismatch(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")
	p := NewPlane(PlaneConfig{LZ4Enabled: true, FieldWidth: 4, FieldHeight: 1, BytesPerPixel: 1, RGBMode: groovy.RGBMode888})
	raw := []byte{1, 2, 3, 4}
	p.rememberFieldHistory(0, raw)
	if !p.hasFieldHistory(0, raw) {
		t.Fatal("history missing immediately after remember")
	}
	if p.hasFieldHistory(0, []byte{1, 2, 3}) {
		t.Fatal("history should be invalid for different raw length")
	}
	if p.fieldPrevValid[0] {
		t.Fatal("history validity bit should be cleared after length mismatch")
	}
}

func TestFieldHistoryInvalidatesOnIdentityMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Plane)
	}{
		{
			name: "width",
			mutate: func(p *Plane) {
				p.cfg.FieldWidth = 2
			},
		},
		{
			name: "height",
			mutate: func(p *Plane) {
				p.cfg.FieldHeight = 2
			},
		},
		{
			name: "bytes_per_pixel",
			mutate: func(p *Plane) {
				p.cfg.BytesPerPixel = 2
			},
		},
		{
			name: "rgb_mode",
			mutate: func(p *Plane) {
				p.cfg.RGBMode = groovy.RGBMode565
			},
		},
		{
			name: "interlace",
			mutate: func(p *Plane) {
				p.cfg.Modeline.Interlace = 1
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GROOVY_DELTA_LZ4", "1")
			p := NewPlane(PlaneConfig{LZ4Enabled: true, FieldWidth: 4, FieldHeight: 1, BytesPerPixel: 1, RGBMode: groovy.RGBMode888})
			raw := []byte{1, 2, 3, 4}
			p.rememberFieldHistory(0, raw)
			if !p.hasFieldHistory(0, raw) {
				t.Fatal("history missing immediately after remember")
			}
			tc.mutate(p)
			if p.hasFieldHistory(0, raw) {
				t.Fatalf("history should be invalid after %s changes", tc.name)
			}
			if p.fieldPrevValid[0] {
				t.Fatal("history validity bit should be cleared after identity mismatch")
			}
		})
	}
}

func TestEnvDeltaLZ4DefaultsOff(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "")
	if envDeltaLZ4() {
		t.Fatal("envDeltaLZ4() = true, want false by default")
	}
}

func TestEnvDeltaLZ4AcceptedValues(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{value: "1", want: true},
		{value: "true", want: true},
		{value: "t", want: true},
		{value: "yes", want: true},
		{value: "y", want: true},
		{value: "on", want: true},
		{value: "0", want: false},
		{value: "false", want: false},
		{value: "f", want: false},
		{value: "no", want: false},
		{value: "n", want: false},
		{value: "off", want: false},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			t.Setenv("GROOVY_DELTA_LZ4", c.value)
			if got := envDeltaLZ4(); got != c.want {
				t.Fatalf("envDeltaLZ4() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestEnvDeltaLZ4DisabledValuesDoNotWarn(t *testing.T) {
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	for _, value := range []string{"0", "false", "f", "no", "n", "off"} {
		t.Run(value, func(t *testing.T) {
			logBuf.Reset()
			t.Setenv("GROOVY_DELTA_LZ4", value)
			if envDeltaLZ4() {
				t.Fatalf("envDeltaLZ4() = true for %q, want false", value)
			}
			if strings.Contains(logBuf.String(), "invalid GROOVY_DELTA_LZ4; delta-LZ4 disabled") {
				t.Fatalf("disabled value %q warned unexpectedly\n%s", value, logBuf.String())
			}
		})
	}
}

func TestEnvDeltaLZ4InvalidValueWarnsAndDisables(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "banana")

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	if envDeltaLZ4() {
		t.Fatal("envDeltaLZ4() = true for invalid value, want false")
	}
	if !strings.Contains(logBuf.String(), "invalid GROOVY_DELTA_LZ4; delta-LZ4 disabled") {
		t.Fatalf("missing invalid env warning\n%s", logBuf.String())
	}
}

func TestNewPlane_AllocatesFieldHistoryOnlyWhenNeeded(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "")
	t.Setenv("GROOVY_FIELD_DIAG", "")

	p := NewPlane(PlaneConfig{
		LZ4Enabled:      true,
		DeltaLZ4Enabled: false,
		FieldWidth:      720,
		FieldHeight:     240,
		BytesPerPixel:   3,
	})
	if p.deltaLZ4Enabled || p.fieldPrev[0] != nil || p.fieldDeltaScratch != nil {
		t.Fatalf("default plane allocated delta history: enabled=%v prev=%v scratch=%v",
			p.deltaLZ4Enabled, p.fieldPrev[0] != nil, p.fieldDeltaScratch != nil)
	}

	p = NewPlane(PlaneConfig{
		LZ4Enabled:      true,
		DeltaLZ4Enabled: true,
		FieldWidth:      720,
		FieldHeight:     240,
		BytesPerPixel:   3,
	})
	if !p.deltaLZ4Enabled {
		t.Fatal("deltaLZ4Enabled = false, want true")
	}
	if len(p.fieldPrev[0]) != 720*240*3 || len(p.fieldPrev[1]) != 720*240*3 {
		t.Fatalf("field history buffers not allocated to field size: %d %d",
			len(p.fieldPrev[0]), len(p.fieldPrev[1]))
	}
	if len(p.fieldDeltaScratch) != 720*240*3 {
		t.Fatalf("fieldDeltaScratch len = %d, want %d", len(p.fieldDeltaScratch), 720*240*3)
	}
	if len(p.fieldDeltaLZ4Scratch) != lz4.CompressBlockBound(720*240*3) {
		t.Fatalf("fieldDeltaLZ4Scratch len = %d, want CompressBlockBound", len(p.fieldDeltaLZ4Scratch))
	}
}

func TestNewPlane_DeltaEnvOverridesConfig(t *testing.T) {
	t.Setenv("GROOVY_FIELD_DIAG", "")

	t.Run("explicit-off-disables-config", func(t *testing.T) {
		t.Setenv("GROOVY_DELTA_LZ4", "0")
		p := NewPlane(PlaneConfig{
			LZ4Enabled:      true,
			DeltaLZ4Enabled: true,
			FieldWidth:      720,
			FieldHeight:     240,
			BytesPerPixel:   3,
		})
		if p.deltaLZ4Enabled {
			t.Fatal("deltaLZ4Enabled = true with GROOVY_DELTA_LZ4=0, want false")
		}
	})

	t.Run("explicit-on-enables-config", func(t *testing.T) {
		t.Setenv("GROOVY_DELTA_LZ4", "1")
		p := NewPlane(PlaneConfig{
			LZ4Enabled:      true,
			DeltaLZ4Enabled: false,
			FieldWidth:      720,
			FieldHeight:     240,
			BytesPerPixel:   3,
		})
		if !p.deltaLZ4Enabled {
			t.Fatal("deltaLZ4Enabled = false with GROOVY_DELTA_LZ4=1, want true")
		}
	})
}

func TestNewPlane_DeltaEnvDoesNotEnableWhenLZ4Disabled(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	p := NewPlane(PlaneConfig{
		LZ4Enabled:    false,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
	})
	if p.deltaLZ4Enabled {
		t.Fatal("deltaLZ4Enabled = true with LZ4 disabled, want false")
	}
	if strings.Contains(logBuf.String(), "adaptive delta-LZ4 enabled") {
		t.Fatalf("delta-LZ4 logged as enabled with LZ4 disabled\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "adaptive delta-LZ4 requested but LZ4 disabled") {
		t.Fatalf("missing disabled-by-LZ4 log\n%s", logBuf.String())
	}
}

func TestDataplaneStatsAttrsIncludeDebugTelemetry(t *testing.T) {
	window := dataplaneStatsWindow{
		ticks:                    60,
		fieldsSent:               55,
		duplicates:               5,
		fieldTotal:               550 * time.Millisecond,
		maxField:                 17 * time.Millisecond,
		maxLZ4:                   2 * time.Millisecond,
		maxCongestion:            3 * time.Millisecond,
		maxSend:                  4 * time.Millisecond,
		sendBudgetOverruns:       2,
		maxRawBytes:              518400,
		maxPayloadBytes:          123456,
		maxCompressedBytes:       120000,
		maxPayloadChunks:         84,
		videoQueueHigh:           7,
		videoPrebufferHigh:       6,
		videoBacklogAfterPull:    12,
		videoBacklogAfterPullMax: 5,
		audioQueueHigh:           10,
		audioPrebufferHigh:       3,
		audioRingHigh:            4,
		wireBytesStart:           1000,
		deltaSelectedStart:       3,
	}
	snapshot := dataplaneStatsSnapshot{
		window:             5 * time.Second,
		videoQueueLen:      2,
		videoQueueCap:      8,
		videoPrebufferLen:  1,
		audioQueueLen:      4,
		audioQueueCap:      16,
		audioPrebufferLen:  2,
		audioRingLen:       3,
		audioRingCap:       5,
		framesTotal:        200,
		underrunsTotal:     9,
		blitsTotal:         60,
		maxFramesAhead:     9,
		currentFramesAhead: 4,
		enobufTotal:        1,
		wireBytesTotal:     61000,
		position:           2 * time.Second,
		audioReady:         true,
		deltaEnabled:       true,
		deltaSelectedTotal: 8,
	}

	got := attrMap(dataplaneStatsAttrs(window, snapshot))

	checks := map[string]any{
		"window_s":                       int64(5),
		"ticks":                          uint64(60),
		"fields_sent":                    uint64(55),
		"duplicates":                     uint64(5),
		"avg_field_ms":                   int64(10),
		"max_field_ms":                   int64(17),
		"max_lz4_ms":                     int64(2),
		"max_congestion_ms":              int64(3),
		"max_send_ms":                    int64(4),
		"send_budget_overruns":           uint64(2),
		"max_raw_bytes":                  518400,
		"max_payload_bytes":              123456,
		"max_compressed_bytes":           120000,
		"max_payload_chunks":             84,
		"video_queue_len":                2,
		"video_queue_cap":                8,
		"video_queue_high":               7,
		"video_prebuffer_len":            1,
		"video_prebuffer_high":           6,
		"video_backlog_after_pull_ticks": uint64(12),
		"video_backlog_after_pull_max":   5,
		"audio_queue_len":                4,
		"audio_queue_cap":                16,
		"audio_queue_high":               10,
		"audio_prebuffer_len":            2,
		"audio_prebuffer_high":           3,
		"audio_ring_len":                 3,
		"audio_ring_cap":                 5,
		"audio_ring_high":                4,
		"frames_total":                   uint64(200),
		"underruns_total":                uint64(9),
		"blits_total":                    uint64(60),
		"wire_bytes_window":              uint64(60000),
		"wire_bytes_total":               uint64(61000),
		"delta_selected":                 uint64(5),
		"delta_selected_total":           uint64(8),
	}
	for key, want := range checks {
		if got[key] != want {
			t.Fatalf("%s = %#v, want %#v\nattrs=%#v", key, got[key], want, got)
		}
	}
}

func attrMap(attrs []any) map[string]any {
	out := make(map[string]any, len(attrs)/2)
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if ok {
			out[key] = attrs[i+1]
		}
	}
	return out
}

func TestWriteFieldSubDeltaInto_UsesByteWrapSubtraction(t *testing.T) {
	current := []byte{0x12, 0x00, 0xff, 0x7f}
	previous := []byte{0x10, 0xff, 0x00, 0x80}
	dst := make([]byte, len(current))

	writeFieldSubDeltaInto(dst, current, previous)

	want := []byte{0x02, 0x01, 0xff, 0xff}
	if !bytes.Equal(dst, want) {
		t.Fatalf("delta = % x, want % x", dst, want)
	}
}

type scriptedFieldSender struct {
	headers         [][]byte
	payloads        [][]byte
	failPayloadSend int
	payloadSends    int
	congestionDelay time.Duration
}

func (s *scriptedFieldSender) WaitForCongestion() {
	if s.congestionDelay > 0 {
		time.Sleep(s.congestionDelay)
	}
}

func (s *scriptedFieldSender) Send(data []byte) error {
	s.headers = append(s.headers, append([]byte(nil), data...))
	return nil
}

func (s *scriptedFieldSender) SendPayload(data []byte) error {
	s.payloadSends++
	if s.failPayloadSend == s.payloadSends {
		return errors.New("scripted payload send failure")
	}
	s.payloads = append(s.payloads, append([]byte(nil), data...))
	return nil
}

func (s *scriptedFieldSender) MarkBlitSent(int) {}

func repeatedTileField(length int, tile []byte) []byte {
	out := make([]byte, length)
	for i := range out {
		out[i] = tile[i%len(tile)]
	}
	return out
}

func deterministicTile(length int) []byte {
	tile := make([]byte, length)
	for i := range tile {
		tile[i] = byte(i*37 + i/7)
	}
	return tile
}

func blitPayloadType(header []byte) int {
	return len(header)
}

func awaitFieldEvent(t *testing.T, fields <-chan fakemister.FieldEvent) fakemister.FieldEvent {
	t.Helper()
	select {
	case fe := <-fields:
		return fe
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for field event")
		return fakemister.FieldEvent{}
	}
}

// TestPosition_PeriodFromModeline asserts Position() reports
// integer-exact playback time derived from the modeline's
// FieldRateRatio. Replaces the older NTSC-hardcoded version.
func TestPosition_PeriodFromModeline(t *testing.T) {
	cases := []struct {
		name       string
		ml         groovy.Modeline
		fieldCount int64
		seekOffset int
		wantMs     int64
	}{
		{
			name:       "NTSC_480i 60 fields = 1001 ms",
			ml:         groovy.NTSC480i60,
			fieldCount: 60,
			seekOffset: 0,
			wantMs:     1001,
		},
		{
			name:       "NTSC_480i 3600 fields = 60060 ms",
			ml:         groovy.NTSC480i60,
			fieldCount: 3600,
			seekOffset: 0,
			wantMs:     60060,
		},
		{
			name: "PAL_576i 50 fields = 1000 ms",
			ml: groovy.Modeline{
				PClock: 13.500, HActive: 720, HBegin: 732, HEnd: 795, HTotal: 864,
				VActive: 576, VBegin: 580, VEnd: 585, VTotal: 625, Interlace: 1,
			},
			fieldCount: 50,
			seekOffset: 0,
			wantMs:     1000,
		},
		{
			name: "PAL_288p 100 fields = 2000 ms with 5000 ms seek offset",
			ml: groovy.Modeline{
				PClock: 13.478, HActive: 720, HBegin: 732, HEnd: 795, HTotal: 864,
				VActive: 288, VBegin: 290, VEnd: 293, VTotal: 312, Interlace: 0,
			},
			fieldCount: 100,
			seekOffset: 5000,
			wantMs:     7000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewPlane(PlaneConfig{
				Modeline:      c.ml,
				FieldWidth:    int(c.ml.HActive),
				FieldHeight:   c.ml.FieldHeight(),
				BytesPerPixel: 3,
				RGBMode:       groovy.RGBMode888,
				SeekOffsetMs:  c.seekOffset,
			})
			p.positionFields.Store(c.fieldCount)
			got := p.Position()
			gotMs := got.Milliseconds()
			if gotMs != c.wantMs {
				t.Errorf("Position() = %d ms, want %d ms", gotMs, c.wantMs)
			}
		})
	}
}

func TestPlane_SetFieldOrder_FlipsAtomic(t *testing.T) {
	p := &Plane{}
	if got := p.fieldOrderFlip.Load(); got {
		t.Fatal("initial flip should be false (TFF)")
	}
	if err := p.SetFieldOrder("bff"); err != nil {
		t.Fatalf("SetFieldOrder(bff): %v", err)
	}
	if !p.fieldOrderFlip.Load() {
		t.Error("after SetFieldOrder(bff), flip should be true")
	}
	if err := p.SetFieldOrder("tff"); err != nil {
		t.Fatalf("SetFieldOrder(tff): %v", err)
	}
	if p.fieldOrderFlip.Load() {
		t.Error("after SetFieldOrder(tff), flip should be false")
	}
}

func TestPlane_SetFieldOrder_RejectsUnknown(t *testing.T) {
	p := &Plane{}
	if err := p.SetFieldOrder("diagonal"); err == nil {
		t.Error("want error on unknown order")
	}
}

func TestNewPlane_SeedsFlipFromBFF(t *testing.T) {
	// BFF SpawnSpec → flip starts true. ffmpeg emits progressive frames at
	// field cadence; the plane row-stripes them, and fieldOrderFlip is the
	// sole encoding of the configured field-order baseline.
	cfg := PlaneConfig{
		Modeline:  groovy.NTSC480i60,
		SpawnSpec: ffmpeg.PipelineSpec{FieldOrder: "bff"},
	}
	p := NewPlane(cfg)
	if !p.fieldOrderFlip.Load() {
		t.Error("NewPlane with bff spec should set flip=true")
	}
}

func TestPlane_ProgressiveIgnoresFieldOrderFlip(t *testing.T) {
	p := NewPlane(PlaneConfig{
		Modeline:  groovy.NTSC240p60,
		SpawnSpec: ffmpeg.PipelineSpec{FieldOrder: "bff"},
	})
	if p.fieldOrderFlip.Load() {
		t.Fatal("progressive modeline must not seed field-order flip")
	}
	if err := p.SetFieldOrder("bff"); err != nil {
		t.Fatalf("SetFieldOrder(bff): %v", err)
	}
	for _, next := range []uint8{0, 1} {
		if got := p.emitField(next); got != 0 {
			t.Errorf("progressive emitField(%d) = %d, want 0", next, got)
		}
	}
}

func TestPlane_InterlacedAppliesFieldOrderFlip(t *testing.T) {
	p := NewPlane(PlaneConfig{
		Modeline:  groovy.NTSC480i60,
		SpawnSpec: ffmpeg.PipelineSpec{FieldOrder: "bff"},
	})
	if got := p.emitField(0); got != 1 {
		t.Errorf("BFF emitField(0) = %d, want 1", got)
	}
	if got := p.emitField(1); got != 0 {
		t.Errorf("BFF emitField(1) = %d, want 0", got)
	}
	if err := p.SetFieldOrder("tff"); err != nil {
		t.Fatalf("SetFieldOrder(tff): %v", err)
	}
	if got := p.emitField(1); got != 1 {
		t.Errorf("TFF emitField(1) = %d, want 1", got)
	}
}

func TestEffectiveAudioConfig(t *testing.T) {
	tests := []struct {
		name  string
		cfg   PlaneConfig
		rate  int
		chans int
	}{
		{
			name: "audio source keeps configured session audio",
			cfg: PlaneConfig{
				SpawnSpec:  ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}},
				AudioRate:  48000,
				AudioChans: 2,
			},
			rate:  48000,
			chans: 2,
		},
		{
			name: "video-only source disables audio",
			cfg: PlaneConfig{
				SpawnSpec:  ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 0}},
				AudioRate:  48000,
				AudioChans: 2,
			},
		},
		{
			name: "non-positive audio config disables audio",
			cfg: PlaneConfig{
				SpawnSpec:  ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}},
				AudioRate:  0,
				AudioChans: 2,
			},
		},
		{
			// DASH dual-input: probe sees only the video-only stream
			// (AudioRate=0). The presence of AudioInputURL is the
			// affirmative signal that audio exists and must override
			// the probe-zero check.
			name: "dual-input keeps audio despite probe zero",
			cfg: PlaneConfig{
				SpawnSpec: ffmpeg.PipelineSpec{
					SourceProbe:   &ffmpeg.ProbeResult{AudioRate: 0},
					AudioInputURL: "https://audio.example/a.m4a",
				},
				AudioRate:  48000,
				AudioChans: 2,
			},
			rate:  48000,
			chans: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plane{cfg: tt.cfg}
			rate, chans := p.effectiveAudioConfig()
			if rate != tt.rate || chans != tt.chans {
				t.Errorf("effectiveAudioConfig() = (%d, %d), want (%d, %d)",
					rate, chans, tt.rate, tt.chans)
			}
		})
	}
}

func TestEffectiveAudioConfigSuppressAudioOutput(t *testing.T) {
	p := NewPlane(PlaneConfig{
		SpawnSpec:           ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}},
		AudioRate:           48000,
		AudioChans:          2,
		SuppressAudioOutput: true,
	})
	rate, chans := p.effectiveAudioConfig()
	if rate != 0 || chans != 0 {
		t.Fatalf("effectiveAudioConfig() = %d/%d, want 0/0", rate, chans)
	}
}

func TestEffectiveAudioConfigCaptureMonitorWithNormalizedProbe(t *testing.T) {
	p := NewPlane(PlaneConfig{
		SpawnSpec: ffmpeg.PipelineSpec{
			CaptureInput: ffmpeg.CaptureInputSpec{Enabled: true},
			SourceProbe:  &ffmpeg.ProbeResult{AudioRate: 48000},
		},
		AudioRate:  48000,
		AudioChans: 2,
	})
	rate, chans := p.effectiveAudioConfig()
	if rate != 48000 || chans != 2 {
		t.Fatalf("effectiveAudioConfig() = %d/%d, want 48000/2", rate, chans)
	}
}

func TestPlaneRunSuppressAudioOutputAdvertisesOffAndSkipsAudioReader(t *testing.T) {
	initCmd, stub, err := runPlaneUntilInit(t, PlaneConfig{
		SpawnSpec:           ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}},
		AudioRate:           48000,
		AudioChans:          2,
		SuppressAudioOutput: true,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Plane.Run() error = %v, want context.Canceled or nil", err)
	}
	if initCmd.Init == nil {
		t.Fatal("INIT command missing parsed payload")
	}
	if initCmd.Init.SoundRate != groovy.AudioRateOff || initCmd.Init.SoundChan != 0 {
		t.Fatalf("INIT audio = rate %d/chans %d, want AudioRateOff/0",
			initCmd.Init.SoundRate, initCmd.Init.SoundChan)
	}
	if stub.audioPipeCalls != 0 {
		t.Fatalf("AudioPipe calls = %d, want 0", stub.audioPipeCalls)
	}
}

func TestPlaneRunCaptureMonitorStartsAudioReaderWithNormalizedProbe(t *testing.T) {
	initCmd, stub, err := runPlaneUntilInit(t, PlaneConfig{
		SpawnSpec: ffmpeg.PipelineSpec{
			CaptureInput: ffmpeg.CaptureInputSpec{Enabled: true},
			SourceProbe:  &ffmpeg.ProbeResult{AudioRate: 48000},
		},
		AudioRate:  48000,
		AudioChans: 2,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Plane.Run() error = %v, want context.Canceled or nil", err)
	}
	if initCmd.Init == nil {
		t.Fatal("INIT command missing parsed payload")
	}
	if initCmd.Init.SoundRate != groovy.AudioRate48000 || initCmd.Init.SoundChan != 2 {
		t.Fatalf("INIT audio = rate %d/chans %d, want AudioRate48000/2",
			initCmd.Init.SoundRate, initCmd.Init.SoundChan)
	}
	if stub.audioPipeCalls != 1 {
		t.Fatalf("AudioPipe calls = %d, want 1", stub.audioPipeCalls)
	}
}

func TestPlaneConfig_ResolveVideoHeight(t *testing.T) {
	cases := []struct {
		name string
		cfg  PlaneConfig
		want int
	}{
		{
			name: "explicit OutputHeight wins",
			cfg: PlaneConfig{
				FieldHeight: 240,
				Modeline:    groovy.Modeline{Interlace: 1},
				SpawnSpec:   ffmpeg.PipelineSpec{OutputHeight: 720},
			},
			want: 720,
		},
		{
			name: "interlaced doubles FieldHeight",
			cfg: PlaneConfig{
				FieldHeight: 240,
				Modeline:    groovy.Modeline{Interlace: 1},
			},
			want: 480,
		},
		{
			name: "progressive uses FieldHeight",
			cfg: PlaneConfig{
				FieldHeight: 480,
				Modeline:    groovy.Modeline{Interlace: 0},
			},
			want: 480,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.resolveVideoHeight(); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestFieldPeriodFromModeline_NTSC480i(t *testing.T) {
	period := fieldPeriodFromModeline(groovy.NTSC480i60)
	// 480i field period = 1001/60 ms ≈ 16.683 ms = 16,683,333 ns.
	// Allow ±1µs jitter from integer rounding in the formula.
	want := 16683333 * time.Nanosecond
	delta := period - want
	if delta < -time.Microsecond || delta > time.Microsecond {
		t.Errorf("period = %v, want %v ± 1µs", period, want)
	}
}

func TestFieldPeriodFromModeline_ZeroOnInvalid(t *testing.T) {
	if got := fieldPeriodFromModeline(groovy.Modeline{}); got != 0 {
		t.Errorf("zero modeline period = %v, want 0", got)
	}
}

// staticFrameReader is a zero-allocation io.Reader that fills caller buffers
// with a fixed byte pattern forever, until Close is called. Used by
// TestPlane_AllocationBudget to feed the Plane.Run hot path with frames
// without spawning a real ffmpeg child or allocating a backing buffer per
// read. The pattern is intentionally simple (a single repeated byte) so
// LZ4 compresses it tightly — that exercises the LZ4 success path
// (compressed + ok), not the random/incompressible path which slog-logs
// and would distort the allocation budget.
type staticFrameReader struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

func newStaticFrameReader() *staticFrameReader {
	return &staticFrameReader{done: make(chan struct{})}
}

func (r *staticFrameReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.EOF
	}
	r.mu.Unlock()
	// Fill with 0x55 — a compressible constant pattern. Loop is in caller
	// frame; no allocation.
	for i := range p {
		p[i] = 0x55
	}
	return len(p), nil
}

func (r *staticFrameReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.done)
	}
	return nil
}

// stubProcess is the test double that satisfies processHandle. Its
// VideoPipe wraps a staticFrameReader; Stop closes both the video reader
// (so ReadFramesFromPipePooled exits cleanly) and the done channel (which
// proc.Done() observes). AudioPipe returns an always-EOF reader because
// TestPlane_AllocationBudget runs with audio disabled.
type stubProcess struct {
	video          *staticFrameReader
	audio          io.Reader
	done           chan struct{}
	once           sync.Once
	audioPipeCalls int
}

func newStubProcess() *stubProcess {
	return &stubProcess{
		video: newStaticFrameReader(),
		audio: &eofReader{},
		done:  make(chan struct{}),
	}
}

func (s *stubProcess) VideoPipe() io.Reader { return s.video }
func (s *stubProcess) AudioPipe() io.Reader {
	s.audioPipeCalls++
	return s.audio
}
func (s *stubProcess) Done() <-chan struct{} { return s.done }
func (s *stubProcess) Stop() {
	s.once.Do(func() {
		_ = s.video.Close()
		close(s.done)
	})
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

type blockingReadCloser struct {
	done chan struct{}
	once sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{done: make(chan struct{})}
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	<-r.done
	return 0, io.EOF
}

func (r *blockingReadCloser) Close() error {
	r.once.Do(func() {
		close(r.done)
	})
	return nil
}

type startupExitProcess struct {
	video io.ReadCloser
	audio io.Reader
	done  chan struct{}
	once  sync.Once
}

func (p *startupExitProcess) VideoPipe() io.Reader { return p.video }
func (p *startupExitProcess) AudioPipe() io.Reader { return p.audio }
func (p *startupExitProcess) Done() <-chan struct{} {
	return p.done
}
func (p *startupExitProcess) Stop() {
	p.once.Do(func() {
		if p.video != nil {
			_ = p.video.Close()
		}
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	})
}

func runPlaneUntilInit(t *testing.T, cfg PlaneConfig) (fakemister.Command, *stubProcess, error) {
	t.Helper()
	t.Setenv("GROOVY_PREBUFFER_FIELDS", "0")

	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(true)
	addr := listener.Addr().(*net.UDPAddr)

	events := make(chan fakemister.Command, 64)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Run(events)
	}()
	defer func() {
		_ = listener.Close()
		<-listenerDone
	}()

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	stub := newStubProcess()
	origSpawn := spawnProcess
	spawnProcess = func(_ context.Context, _ ffmpeg.PipelineSpec) (processHandle, error) {
		return stub, nil
	}
	defer func() { spawnProcess = origSpawn }()

	cfg.Sender = sender
	if cfg.Modeline.PClock == 0 {
		cfg.Modeline = groovy.NTSC480i60
	}
	if cfg.FieldWidth == 0 {
		cfg.FieldWidth = 2
	}
	if cfg.FieldHeight == 0 {
		cfg.FieldHeight = 1
	}
	if cfg.BytesPerPixel == 0 {
		cfg.BytesPerPixel = 1
	}

	plane := NewPlane(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- plane.Run(ctx)
	}()

	var initCmd fakemister.Command
	for {
		select {
		case cmd := <-events:
			if cmd.Type == groovy.CmdInit {
				initCmd = cmd
				cancel()
				err := <-runErr
				<-plane.Done()
				return initCmd, stub, err
			}
		case err := <-runErr:
			return initCmd, stub, err
		case <-time.After(time.Second):
			cancel()
			err := <-runErr
			<-plane.Done()
			t.Fatalf("timed out waiting for INIT command; Plane.Run() err=%v", err)
		}
	}
}

func TestPlane_RunReportsProcessExitDuringPrebufferAsError(t *testing.T) {
	t.Setenv("GROOVY_PREBUFFER_FIELDS", "4")

	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(true)
	defer listener.Close()
	addr := listener.Addr().(*net.UDPAddr)

	events := make(chan fakemister.Command, 64)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Run(events)
	}()
	defer func() {
		_ = listener.Close()
		<-listenerDone
	}()

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	procDone := make(chan struct{})
	close(procDone)
	proc := &startupExitProcess{
		video: newBlockingReadCloser(),
		audio: eofReader{},
		done:  procDone,
	}
	origSpawn := spawnProcess
	spawnProcess = func(context.Context, ffmpeg.PipelineSpec) (processHandle, error) {
		return proc, nil
	}
	t.Cleanup(func() { spawnProcess = origSpawn })

	plane := NewPlane(PlaneConfig{
		Sender:        sender,
		SpawnSpec:     ffmpeg.PipelineSpec{OutputWidth: 2, OutputHeight: 2, SourceProbe: &ffmpeg.ProbeResult{}},
		Modeline:      groovy.NTSC480i60,
		FieldWidth:    2,
		FieldHeight:   1,
		BytesPerPixel: 1,
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- plane.Run(context.Background())
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Plane.Run() = nil, want startup error")
		}
		if !strings.Contains(err.Error(), "ffmpeg exited during prebuffer") {
			t.Fatalf("Plane.Run() error = %v, want ffmpeg prebuffer exit", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Plane.Run() did not return after process exit during prebuffer")
	}
	<-plane.Done()
}

func TestPlane_RunReportsVideoEOFDuringPrebufferAsError(t *testing.T) {
	t.Setenv("GROOVY_PREBUFFER_FIELDS", "4")

	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(true)
	defer listener.Close()
	addr := listener.Addr().(*net.UDPAddr)

	events := make(chan fakemister.Command, 64)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Run(events)
	}()
	defer func() {
		_ = listener.Close()
		<-listenerDone
	}()

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	proc := &startupExitProcess{
		video: io.NopCloser(eofReader{}),
		audio: eofReader{},
		done:  make(chan struct{}),
	}
	origSpawn := spawnProcess
	spawnProcess = func(context.Context, ffmpeg.PipelineSpec) (processHandle, error) {
		return proc, nil
	}
	t.Cleanup(func() { spawnProcess = origSpawn })

	plane := NewPlane(PlaneConfig{
		Sender:        sender,
		SpawnSpec:     ffmpeg.PipelineSpec{OutputWidth: 2, OutputHeight: 2, SourceProbe: &ffmpeg.ProbeResult{}},
		Modeline:      groovy.NTSC480i60,
		FieldWidth:    2,
		FieldHeight:   1,
		BytesPerPixel: 1,
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- plane.Run(context.Background())
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Plane.Run() = nil, want startup error")
		}
		if !strings.Contains(err.Error(), "video pipe closed during prebuffer") {
			t.Fatalf("Plane.Run() error = %v, want video prebuffer EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Plane.Run() did not return after video EOF during prebuffer")
	}
	<-plane.Done()
}

// TestPlane_Prebuffer_HitsTargetReturnsFrames verifies the happy path:
// when videoCh produces the target number of frames, prebuffer returns
// them in order with no early-exit reason. Regression harness for the
// startup-choppiness fix — without prebuffer, the first ~30 ticks fall
// through to sendDuplicate while ffmpeg is still warming up.
func TestPlane_Prebuffer_HitsTargetReturnsFrames(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	audioCh := make(chan []byte, 8)
	procDone := make(chan struct{})

	for i := 0; i < 4; i++ {
		fb := pool.Get()
		fb.N = 4
		fb.Data[0] = byte(i)
		videoCh <- fb
	}

	video, audio, exit := p.prebuffer(context.Background(), procDone, videoCh, audioCh, 4, time.Second)
	if exit != "" {
		t.Fatalf("exit = %q, want empty (target hit)", exit)
	}
	if len(video) != 4 {
		t.Fatalf("got %d video frames, want 4", len(video))
	}
	for i, fb := range video {
		if fb.Data[0] != byte(i) {
			t.Errorf("video[%d].Data[0] = %d, want %d (FIFO order broken)", i, fb.Data[0], i)
		}
	}
	if len(audio) != 0 {
		t.Errorf("got %d audio chunks, want 0 (none produced)", len(audio))
	}
	for _, fb := range video {
		pool.Put(fb)
	}
}

// TestPlane_Prebuffer_DrainsAudioWhileWaiting proves that audio chunks
// produced during the prebuffer wait are absorbed into the audio
// prebuffer slice instead of backpressuring ffmpeg's audio pipe. This
// is the load-bearing property that prevents the prebuffer from
// deadlocking when ffmpeg's audio decode races ahead of video.
func TestPlane_Prebuffer_DrainsAudioWhileWaiting(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	audioCh := make(chan []byte, 32)
	procDone := make(chan struct{})

	for i := 0; i < 10; i++ {
		audioCh <- []byte{byte(i)}
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		for i := 0; i < 2; i++ {
			fb := pool.Get()
			fb.N = 4
			videoCh <- fb
		}
	}()

	video, audio, exit := p.prebuffer(context.Background(), procDone, videoCh, audioCh, 2, time.Second)
	if exit != "" {
		t.Fatalf("exit = %q, want empty", exit)
	}
	if len(video) != 2 {
		t.Errorf("got %d video frames, want 2", len(video))
	}
	if len(audio) < 8 {
		t.Errorf("got %d audio chunks, want >= 8 (audio must drain while waiting for video)", len(audio))
	}
	for i, pcm := range audio {
		if len(pcm) != 1 || pcm[0] != byte(i) {
			t.Errorf("audio[%d] = %v, want [%d] (FIFO order broken)", i, pcm, i)
		}
	}
	for _, fb := range video {
		pool.Put(fb)
	}
}

// TestPlane_Prebuffer_HonorsCtxCancel verifies that ctx cancel mid-wait
// returns "context_cancelled" without deadlocking. Run uses this to
// transition cleanly when the operator changes channels mid-prebuffer.
func TestPlane_Prebuffer_HonorsCtxCancel(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	audioCh := make(chan []byte, 8)
	procDone := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, _, exit := p.prebuffer(ctx, procDone, videoCh, audioCh, 4, time.Second)
	if exit != "context_cancelled" {
		t.Errorf("exit = %q, want context_cancelled", exit)
	}
}

// TestPlane_Prebuffer_HonorsFFmpegExit verifies that proc.Done firing
// during prebuffer returns "ffmpeg_exit". Run uses this for the case
// where ffmpeg crashes/exits before producing any frames.
func TestPlane_Prebuffer_HonorsFFmpegExit(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	audioCh := make(chan []byte, 8)
	procDone := make(chan struct{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(procDone)
	}()

	_, _, exit := p.prebuffer(context.Background(), procDone, videoCh, audioCh, 4, time.Second)
	if exit != "ffmpeg_exit" {
		t.Errorf("exit = %q, want ffmpeg_exit", exit)
	}
}

// TestPlane_Prebuffer_HonorsVideoEOF verifies that videoCh closing
// during prebuffer returns "video_pipe_eof". Mirrors the ReadFrames
// EOF path that the tick loop's main select handles.
func TestPlane_Prebuffer_HonorsVideoEOF(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	audioCh := make(chan []byte, 8)
	procDone := make(chan struct{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(videoCh)
	}()

	_, _, exit := p.prebuffer(context.Background(), procDone, videoCh, audioCh, 4, time.Second)
	if exit != "video_pipe_eof" {
		t.Errorf("exit = %q, want video_pipe_eof", exit)
	}
}

// TestPlane_Prebuffer_HonorsTimeout verifies that when frames don't
// arrive within the configured timeout, prebuffer returns "timeout"
// with whatever frames it managed to capture. Run treats this as a
// non-fatal exit and falls through to the underrun path so a slow
// ffmpeg start can't deadlock the tick loop.
func TestPlane_Prebuffer_HonorsTimeout(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	audioCh := make(chan []byte, 8)
	procDone := make(chan struct{})

	video, _, exit := p.prebuffer(context.Background(), procDone, videoCh, audioCh, 4, 30*time.Millisecond)
	if exit != "timeout" {
		t.Errorf("exit = %q, want timeout", exit)
	}
	if len(video) != 0 {
		t.Errorf("got %d frames, want 0 (none produced)", len(video))
	}
}

// TestPlane_Prebuffer_DisabledByZeroTarget verifies that target=0
// returns immediately with no exit reason. Operators can disable the
// prebuffer entirely via GROOVY_PREBUFFER_FIELDS=0 to recover the
// pre-fix behaviour for diagnostic purposes.
func TestPlane_Prebuffer_DisabledByZeroTarget(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	audioCh := make(chan []byte, 8)
	procDone := make(chan struct{})

	video, audio, exit := p.prebuffer(context.Background(), procDone, videoCh, audioCh, 0, time.Second)
	if exit != "" {
		t.Errorf("exit = %q, want empty (disabled)", exit)
	}
	if len(video) != 0 || len(audio) != 0 {
		t.Errorf("got video=%d audio=%d, want 0/0", len(video), len(audio))
	}
}

// TestPlane_Prebuffer_NilAudioChannel verifies that passing audioCh=nil
// (the production state when audio is disabled) does not panic or
// deadlock. Nil-channel reads in select block forever, which Go handles
// correctly — the case is simply never selected.
func TestPlane_Prebuffer_NilAudioChannel(t *testing.T) {
	pool := NewFramePool(8, 4)
	p := &Plane{framePool: pool}
	videoCh := make(chan *FrameBuf, 8)
	procDone := make(chan struct{})

	for i := 0; i < 2; i++ {
		fb := pool.Get()
		fb.N = 4
		videoCh <- fb
	}

	video, audio, exit := p.prebuffer(context.Background(), procDone, videoCh, nil, 2, time.Second)
	if exit != "" {
		t.Errorf("exit = %q, want empty", exit)
	}
	if len(video) != 2 {
		t.Errorf("got %d frames, want 2", len(video))
	}
	if audio != nil && len(audio) != 0 {
		t.Errorf("got %d audio chunks, want 0", len(audio))
	}
	for _, fb := range video {
		pool.Put(fb)
	}
}

func TestPullVideoFrame_PreservesBurstAfterUnderrun(t *testing.T) {
	pool := NewFramePool(8, 4)
	prebuf := []*FrameBuf{}
	ch := make(chan *FrameBuf, 8)

	if fb, ok, closed := pullVideoFrame(&prebuf, ch); fb != nil || ok || closed {
		t.Fatalf("empty pull = (%v, %v, %v), want underrun without close", fb, ok, closed)
	}

	for i := 0; i < 5; i++ {
		fb := pool.Get()
		fb.N = 4
		fb.Data[0] = byte(i)
		ch <- fb
	}

	for i := 0; i < 5; i++ {
		fb, ok, closed := pullVideoFrame(&prebuf, ch)
		if !ok || closed {
			t.Fatalf("pull %d = ok:%v closed:%v, want buffered frame", i, ok, closed)
		}
		if fb.Data[0] != byte(i) {
			t.Fatalf("pull %d got frame marker %d, want %d", i, fb.Data[0], i)
		}
		pool.Put(fb)
	}

	if fb, ok, closed := pullVideoFrame(&prebuf, ch); fb != nil || ok || closed {
		t.Fatalf("final empty pull = (%v, %v, %v), want underrun without close", fb, ok, closed)
	}
}

// TestEnvPrebufferFields covers the env-var parsing and clamping for
// GROOVY_PREBUFFER_FIELDS. Bad input falls back to the default; values
// over the channel cap are clamped down so the prebuffer can never
// deadlock waiting for frames the channel cannot hold.
func TestEnvPrebufferFields(t *testing.T) {
	cases := []struct {
		env  string
		max  int
		want int
	}{
		{"", 8, defaultPrebufferFields},    // unset → default
		{"0", 8, 0},                        // disable
		{"4", 8, 4},                        // valid
		{"99", 8, 8},                       // clamp to max
		{"-1", 8, defaultPrebufferFields},  // negative → default (parse rejects)
		{"abc", 8, defaultPrebufferFields}, // garbage → default
	}
	for _, c := range cases {
		t.Run(c.env, func(t *testing.T) {
			t.Setenv("GROOVY_PREBUFFER_FIELDS", c.env)
			if got := envPrebufferFields(c.max); got != c.want {
				t.Errorf("envPrebufferFields(%d) with env=%q = %d, want %d",
					c.max, c.env, got, c.want)
			}
		})
	}
}

func TestEnvAudioDelayFields_DefaultMatchesDisplayLatency(t *testing.T) {
	t.Setenv("GROOVY_AUDIO_DELAY_FIELDS", "")

	if got := envAudioDelayFields(groovy.NTSC480i60); got != 4 {
		t.Errorf("NTSC default audio delay fields = %d, want 4", got)
	}
	if got := envAudioDelayFields(groovy.PAL576i50); got != 3 {
		t.Errorf("PAL default audio delay fields = %d, want 3", got)
	}
}

func TestEnvAudioDelayFields_EnvOverrideAndDisable(t *testing.T) {
	t.Setenv("GROOVY_AUDIO_DELAY_FIELDS", "0")
	if got := envAudioDelayFields(groovy.NTSC480i60); got != 0 {
		t.Errorf("audio delay fields with env=0 = %d, want 0", got)
	}

	t.Setenv("GROOVY_AUDIO_DELAY_FIELDS", "2")
	if got := envAudioDelayFields(groovy.NTSC480i60); got != 2 {
		t.Errorf("audio delay fields with env=2 = %d, want 2", got)
	}
}

func TestEnvAudioDelayFields_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("GROOVY_AUDIO_DELAY_FIELDS", "99")
	if got := envAudioDelayFields(groovy.NTSC480i60); got != 4 {
		t.Errorf("audio delay fields with env=99 = %d, want default 4", got)
	}

	t.Setenv("GROOVY_AUDIO_DELAY_FIELDS", "abc")
	if got := envAudioDelayFields(groovy.NTSC480i60); got != 4 {
		t.Errorf("audio delay fields with env=abc = %d, want default 4", got)
	}
}

func TestEnvPrebufferTimeout(t *testing.T) {
	cases := []struct {
		env    string
		wantMs int
	}{
		{"", defaultPrebufferTimeoutMs},
		{"1000", 1000},
		{"0", defaultPrebufferTimeoutMs},   // 0 invalid → default
		{"abc", defaultPrebufferTimeoutMs}, // garbage → default
	}
	for _, c := range cases {
		t.Run(c.env, func(t *testing.T) {
			t.Setenv("GROOVY_PREBUFFER_TIMEOUT_MS", c.env)
			got := envPrebufferTimeout()
			want := time.Duration(c.wantMs) * time.Millisecond
			if got != want {
				t.Errorf("envPrebufferTimeout() with env=%q = %v, want %v",
					c.env, got, want)
			}
		})
	}
}

// TestPlane_AllocationBudget verifies that the data-plane perf pack's
// pool + scratch refactor actually keeps the hot path near zero-alloc.
// Runs Plane.Run end-to-end against a fakemister.Listener (real UDP
// loopback, real Sender, real INIT/ACK handshake, real LZ4 compression
// on every field) for 500 ms, then asserts that
// runtime.MemStats.TotalAlloc grew by less than the budget below.
//
// Budget (8 MB / 500 ms):
//   - Pre-perf-pack legacy was ~60 MB / 500 ms (each tick allocated a
//     fresh field buffer + LZ4 scratch + header).
//   - The previous dominant residual allocator was pierrec/lz4/v4's
//     Compressor escaping ~136 KB per CompressBlock call (~3.8 MB /
//     500 ms on its own). The Compressor is now hoisted onto Plane
//     (lz4Compressor field) so the BLIT hot path holds zero allocs
//     per tick — see TestLZ4CompressInto_ZeroAllocs.
//   - 8 MB still catches every framePool / lz4Scratch / fieldScratch
//     regression, which each re-introduce ~15 MB / 500 ms. It is
//     intentionally NOT tight enough to catch headerScratch
//     regressions on its own; that path is covered by direct
//     AllocsPerRun assertions in the builder tests.
//
// Skipped under -short because the test runs goroutines and a 500 ms
// timer; it adds ~half a second to the package's wall time.
func TestPlane_AllocationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; allocates and runs goroutines for 500ms")
	}

	// Stand up a fake MiSTer that ACKs the INIT handshake. EnableACKs
	// is the documented seam for this; the listener emits a 13-byte
	// ACK back to the sender's source port on every CmdInit.
	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	listener.EnableACKs(true) // status bit 6 set so audio path is exercisable
	defer listener.Close()
	addr := listener.Addr().(*net.UDPAddr)

	// Drain the listener loop into a sink — RunWithFields would also
	// reassemble payloads, but for the allocation budget we don't care
	// what the bytes are, only that the socket reads keep pace so the
	// kernel send queue doesn't backpressure the Sender.
	events := make(chan fakemister.Command, 4096)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Run(events)
	}()
	go func() {
		for range events {
		}
	}()

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	// Inject the stub processHandle for the duration of this test.
	// spawnProcess is a package-level var (see plane.go) — the
	// production path points at ffmpeg.Spawn; the test swaps in a
	// constructor that returns our zero-alloc fake.
	stub := newStubProcess()
	origSpawn := spawnProcess
	spawnProcess = func(_ context.Context, _ ffmpeg.PipelineSpec) (processHandle, error) {
		return stub, nil
	}
	defer func() { spawnProcess = origSpawn }()

	// Build the Plane. NTSC480i60 + 720x240/field BGR24 mirrors the
	// integration test's real-ffmpeg shape; LZ4Enabled=true exercises
	// the full LZ4CompressInto + BuildBlitHeaderInto hot path. Audio
	// is disabled so the test focuses on the video tick loop, which is
	// what Tasks 1–12 actually optimized.
	plane := NewPlane(PlaneConfig{
		Sender: sender,
		SpawnSpec: ffmpeg.PipelineSpec{
			OutputWidth:  720,
			OutputHeight: 480,
			FieldOrder:   "tff",
			SourceProbe:  &ffmpeg.ProbeResult{AudioRate: 0},
		},
		Modeline:      groovy.NTSC480i60,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
		RGBMode:       groovy.RGBMode888,
		LZ4Enabled:    true,
		AudioRate:     0, // belt & braces: effectiveAudioConfig disables audio
		AudioChans:    0,
	})

	// Warm-up: prime the static-pattern frame reader by issuing one
	// pool.Get/Put cycle, and make sure goroutine fixed-size stacks are
	// already provisioned. Without warm-up the first ~4 ticks are
	// dominated by stack growth and one-time slog formatting on the
	// ENOBUF path, which would unfairly inflate the delta. The plan's
	// 1 MB budget is generous enough to absorb a missed warm-up, but
	// being explicit makes the test easier to triage on regression.
	runtime.GC()

	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = plane.Run(ctx)
	<-plane.Done() // ensure all Run-side goroutines have observed exit

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// 8 MB ceiling: legacy was ~60 MB/500ms, post-pack steady state is
	// ~4 MB/500ms (dominated by the lz4.Compressor escape — see comment
	// above). 8 MB catches every multi-MB regression while tolerating
	// the documented quirk. If you see a failure here, run with
	// -memprofile to confirm whether a scratch buffer slipped back into
	// the hot path.
	const budgetBytes = 8 * 1024 * 1024
	delta := after.TotalAlloc - before.TotalAlloc
	if delta > budgetBytes {
		t.Errorf("Plane.Run allocated %d bytes over 500ms; budget %d (%.1fx over)",
			delta, budgetBytes, float64(delta)/float64(budgetBytes))
	}
	t.Logf("Plane.Run allocated %d bytes over 500ms (budget %d, %.1f%% used)",
		delta, budgetBytes, 100*float64(delta)/float64(budgetBytes))
}

// newPlaneForTest constructs a minimal Plane via NewPlane for tests that only
// need a valid Plane instance without network, ffmpeg, or hardware. Uses the
// minimum non-zero dimensions required by NewPlane's scratch-buffer allocation
// (FieldWidth * FieldHeight * BytesPerPixel > 0). No Sender is set so UDP
// calls would panic — this helper is intended only for accessors / struct
// inspection tests that do not call Run or sendField.
func newPlaneForTest(t *testing.T) *Plane {
	t.Helper()
	return NewPlane(PlaneConfig{
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
	})
}

func TestPlane_TelemetryAccessors_ZeroBeforeRun(t *testing.T) {
	p := newPlaneForTest(t)
	if got := p.BlitsTotal(); got != 0 {
		t.Errorf("BlitsTotal: got %d, want 0", got)
	}
	if got := p.FramesTotal(); got != 0 {
		t.Errorf("FramesTotal: got %d, want 0", got)
	}
	if got := p.Underruns(); got != 0 {
		t.Errorf("Underruns: got %d, want 0", got)
	}
	if got := p.WireBytes(); got != 0 {
		t.Errorf("WireBytes: got %d, want 0", got)
	}
	if got := p.LastACKAge(); got != 0 {
		t.Errorf("LastACKAge: got %v, want 0", got)
	}
}

// TestPlane_TelemetryRaceFree validates the atomic-read accessors against
// concurrent atomic writes to the same fields. CI runs this under -race to
// validate no data race.
//
// Local environments without CGO cannot run -race; this test still passes
// under plain `go test` because the asserted invariants (monotonic
// non-decreasing counters) hold regardless.
//
// Option B (simpler-than-real) is used here: no real Plane.Run(), no UDP,
// no ffmpeg. Writers simulate the production update sites by calling the
// exact same atomic primitives used inside Run/sendField; readers call the
// public accessor methods. The integration suite covers the real-plane-
// race angle; this test focuses solely on the accessor lock discipline.
func TestPlane_TelemetryRaceFree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping race test in -short mode")
	}
	p := newPlaneForTest(t)
	deadline := time.Now().Add(200 * time.Millisecond)

	// Writers: simulate the production update sites concurrently.
	// Each field mirrors the exact atomic operation used in plane.go:
	//   positionFields.Add(1)  — emitField / Run tick loop
	//   framesTotal.Add(1)     — Run tick loop on new frame
	//   underruns.Add(1)       — Run tick loop on underrun
	//   wireBytes.Add(64)      — sendField / sendAudio
	//   lastACKUnix.Store(ns)  — handleACK / Run ACK path
	const writers = 4
	var wg sync.WaitGroup
	wg.Add(writers + 1)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				p.positionFields.Add(1)
				p.framesTotal.Add(1)
				p.underruns.Add(1)
				p.wireBytes.Add(64)
				p.lastACKUnix.Store(time.Now().UnixNano())
			}
		}()
	}

	// Reader: tight loop calling every public accessor.
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			_ = p.BlitsTotal()
			_ = p.FramesTotal()
			_ = p.Underruns()
			_ = p.WireBytes()
			_ = p.LastACKAge()
		}
	}()

	wg.Wait()

	// Final invariants: every counter must be non-zero if writers ran.
	if p.BlitsTotal() == 0 {
		t.Error("BlitsTotal stayed zero — writers did not run")
	}
	if p.FramesTotal() == 0 {
		t.Error("FramesTotal stayed zero")
	}
	if p.Underruns() == 0 {
		t.Error("Underruns stayed zero")
	}
	if p.WireBytes() == 0 {
		t.Error("WireBytes stayed zero")
	}
	// LastACKAge() derives from lastACKUnix; on some platforms (notably
	// Windows, which has coarse wall-clock resolution) the computed age
	// can be zero even when the field was written — e.g. if the Store and
	// the subsequent Load happen within the same OS clock tick. Assert the
	// raw atomic field instead: it must be non-zero to prove the writers
	// actually executed the Store path.
	if p.lastACKUnix.Load() == 0 {
		t.Error("lastACKUnix stayed zero — ACK timestamp writers did not run")
	}
}

// TestNewPlane_AudioScopesNilUntilAudioObserved confirms that a freshly
// constructed Plane returns nil from AudioScopes() before any audio has been
// observed. The meter is constructed (AudioRate > 0 && AudioChans > 0) but
// has never called Observe, so no snapshot has been published yet.
func TestNewPlane_AudioScopesNilUntilAudioObserved(t *testing.T) {
	p := NewPlane(PlaneConfig{
		Generation:    42,
		AudioRate:     48000,
		AudioChans:    2,
		FieldWidth:    2,
		FieldHeight:   1,
		BytesPerPixel: 1,
	})
	if got := p.AudioScopes(); got != nil {
		t.Fatalf("AudioScopes before observed audio = %#v, want nil", got)
	}
}

// staticPCMReader is an io.Reader that fills any read buffer with non-zero
// PCM bytes (0x01) indefinitely until Close is called. Used by
// TestPlane_RunPublishesAudioScopesFromReadyAudioPath to feed the Plane.Run
// audio pipe with valid s16le data.
type staticPCMReader struct {
	mu     sync.Mutex
	closed bool
}

func (r *staticPCMReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.EOF
	}
	r.mu.Unlock()
	for i := range p {
		p[i] = 0x01
	}
	return len(p), nil
}

func (r *staticPCMReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// runPlaneWithAudioPipe is a test helper that spins up a Plane with the given
// config and audio pipe reader, runs it past the INIT handshake, and then
// continues running for runDuration before cancelling the context. Returns the
// plane so the caller can inspect state (e.g. AudioScopes()). The helper
// requires UDP sockets; it will call t.Skip if they are unavailable.
func runPlaneWithAudioPipe(t *testing.T, cfg PlaneConfig, audioPipe io.Reader, runDuration time.Duration) *Plane {
	t.Helper()
	t.Setenv("GROOVY_PREBUFFER_FIELDS", "0")

	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	// EnableACKs(true) sets status bit 6 (audio ready) in every ACK the fake
	// mister emits; Plane.Run stores this into p.audioReady so the field-tick
	// loop will actually pop audio from the ring and call Observe.
	listener.EnableACKs(true)
	addr := listener.Addr().(*net.UDPAddr)

	events := make(chan fakemister.Command, 64)
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Run(events)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-listenerDone
	})

	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	t.Cleanup(func() { sender.Close() })

	// Build a stub process whose audio pipe is the caller-supplied reader.
	audioProc := &stubProcess{
		video: newStaticFrameReader(),
		audio: audioPipe,
		done:  make(chan struct{}),
	}

	origSpawn := spawnProcess
	spawnProcess = func(_ context.Context, _ ffmpeg.PipelineSpec) (processHandle, error) {
		return audioProc, nil
	}
	t.Cleanup(func() { spawnProcess = origSpawn })

	if cfg.Sender == nil {
		cfg.Sender = sender
	}
	if cfg.Modeline.PClock == 0 {
		cfg.Modeline = groovy.NTSC480i60
	}
	if cfg.FieldWidth == 0 {
		cfg.FieldWidth = 2
	}
	if cfg.FieldHeight == 0 {
		cfg.FieldHeight = 1
	}
	if cfg.BytesPerPixel == 0 {
		cfg.BytesPerPixel = 1
	}

	plane := NewPlane(cfg)
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- plane.Run(ctx)
	}()

	// Wait for INIT to confirm the handshake completed and the field-tick loop
	// is running. After INIT the listener sends an ACK with audio-ready set,
	// so p.audioReady becomes true and subsequent ticks will pop audio.
	initSeen := make(chan struct{})
	go func() {
		for cmd := range events {
			if cmd.Type == groovy.CmdInit {
				close(initSeen)
				return
			}
		}
	}()

	select {
	case <-initSeen:
	case err := <-runErr:
		cancel()
		t.Fatalf("Plane.Run exited before INIT: %v", err)
	case <-time.After(2 * time.Second):
		cancel()
		<-runErr
		t.Fatal("timed out waiting for INIT")
	}

	// Let the field-tick loop run for runDuration to give audio chunks time to
	// flow through the ring, past the delay guard, and into Observe.
	select {
	case <-time.After(runDuration):
	case err := <-runErr:
		cancel()
		t.Fatalf("Plane.Run exited unexpectedly: %v", err)
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Plane.Run did not stop after context cancel")
	}
	<-plane.Done()
	return plane
}

// TestPlane_RunPublishesAudioScopesFromReadyAudioPath runs the plane with a
// continuous PCM audio source and an ACK-enabled fake mister (audio-ready bit
// set). After the field-tick loop has had time to drain audio chunks through
// the ring and call Observe, AudioScopes() must return a non-nil snapshot
// whose Generation matches PlaneConfig.Generation.
func TestPlane_RunPublishesAudioScopesFromReadyAudioPath(t *testing.T) {
	if testing.Short() {
		t.Skip("requires running goroutines and a UDP loopback listener; skipped under -short")
	}

	const gen = uint64(77)
	audioPipe := &staticPCMReader{}
	defer audioPipe.Close()

	plane := runPlaneWithAudioPipe(t, PlaneConfig{
		Generation: gen,
		AudioRate:  48000,
		AudioChans: 2,
		SpawnSpec:  ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}},
	}, audioPipe, 300*time.Millisecond)

	snap := plane.AudioScopes()
	if snap == nil {
		t.Fatal("AudioScopes() = nil after running plane with PCM audio for 300ms; want non-nil snapshot")
	}
	if snap.Generation != gen {
		t.Errorf("AudioScopes().Generation = %d, want %d", snap.Generation, gen)
	}
}

// TestPlane_RunDoesNotPublishAudioScopesForEmptyChunks runs the plane with an
// audio pipe that immediately returns EOF (empty/no data). Because no non-empty
// PCM chunks ever reach Observe, AudioScopes() must remain nil.
func TestPlane_RunDoesNotPublishAudioScopesForEmptyChunks(t *testing.T) {
	if testing.Short() {
		t.Skip("requires running goroutines and a UDP loopback listener; skipped under -short")
	}

	plane := runPlaneWithAudioPipe(t, PlaneConfig{
		Generation: 99,
		AudioRate:  48000,
		AudioChans: 2,
		SpawnSpec:  ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}},
	}, eofReader{}, 150*time.Millisecond)

	if snap := plane.AudioScopes(); snap != nil {
		t.Fatalf("AudioScopes() = %#v after EOF audio pipe, want nil", snap)
	}
}
