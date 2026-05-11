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
	const fieldBytes = 720 * 240 * 3

	listener, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)

	cmds := make(chan fakemister.Command, 8)
	fields := make(chan fakemister.FieldEvent, 2)
	audios := make(chan fakemister.AudioEvent, 1)
	runDone := make(chan struct{})
	go func() {
		listener.RunWithFields(cmds, fields, audios, func() uint32 { return fieldBytes })
		close(runDone)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-runDone
	})

	addr := listener.Addr().(*net.UDPAddr)
	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	requireUDPSockets(t, err)
	defer sender.Close()

	p := NewPlane(PlaneConfig{
		Sender:        sender,
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
	})

	base := make([]byte, fieldBytes)
	if _, err := cryptorand.Read(base); err != nil {
		t.Fatal(err)
	}
	next := append([]byte(nil), base...)
	for i := 0; i < 64; i++ {
		next[i*1024] ^= byte(i + 1)
	}

	p.sendField(1, 0, base)
	first := awaitFieldEvent(t, fields)
	if first.Header.Compressed || first.Header.Delta {
		t.Fatalf("seed field header = %+v, want RAW full field", first.Header)
	}

	p.sendField(3, 0, next)
	second := awaitFieldEvent(t, fields)
	if second.Header.Compressed || second.Header.Delta {
		t.Fatalf("second field header = %+v, want RAW full field", second.Header)
	}

	decoder := fakemister.NewFieldDecoder()
	if _, err := decoder.Decode(first, fieldBytes); err != nil {
		t.Fatalf("decode seed field: %v", err)
	}
	got, err := decoder.Decode(second, fieldBytes)
	if err != nil {
		t.Fatalf("decode delta field: %v", err)
	}
	if !bytes.Equal(got, next) {
		t.Fatal("delta field decoded to different pixels")
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
	p.sender = sender

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
	p.sender = sender

	p.sendField(1, 0, repeatedTileField(fieldBytes, []byte{0x11, 0x22, 0x33}))

	if len(sender.headers) != 1 {
		t.Fatalf("headers = %d, want 1", len(sender.headers))
	}
	if got := blitPayloadType(sender.headers[0]); got != groovy.BlitHeaderRaw {
		t.Fatalf("payload type = %d, want RAW", got)
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
	p.sender = sender

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
	p.sender = sender
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

func TestSendField_DeltaEnabledSlowWarningLogsFailedDeltaCompression(t *testing.T) {
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
	p.sender = sender
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

func TestFieldHistoryInvalidatesOnModeIdentityMismatch(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")
	p := NewPlane(PlaneConfig{LZ4Enabled: true, FieldWidth: 4, FieldHeight: 1, BytesPerPixel: 1, RGBMode: groovy.RGBMode888})
	raw := []byte{1, 2, 3, 4}
	p.rememberFieldHistory(0, raw)
	if !p.hasFieldHistory(0, raw) {
		t.Fatal("history missing immediately after remember")
	}
	p.cfg.RGBMode = groovy.RGBMode565
	if p.hasFieldHistory(0, raw) {
		t.Fatal("history should be invalid after RGB mode changes")
	}
	if p.fieldPrevValid[0] {
		t.Fatal("history validity bit should be cleared after mode mismatch")
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
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
	})
	if p.deltaLZ4Enabled || p.fieldPrev[0] != nil || p.fieldDeltaScratch != nil {
		t.Fatalf("default plane allocated delta history: enabled=%v prev=%v scratch=%v",
			p.deltaLZ4Enabled, p.fieldPrev[0] != nil, p.fieldDeltaScratch != nil)
	}

	t.Setenv("GROOVY_DELTA_LZ4", "1")
	p = NewPlane(PlaneConfig{
		LZ4Enabled:    true,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
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

func TestNewPlane_DeltaEnvDoesNotEnableWhenLZ4Disabled(t *testing.T) {
	t.Setenv("GROOVY_DELTA_LZ4", "1")

	p := NewPlane(PlaneConfig{
		LZ4Enabled:    false,
		FieldWidth:    720,
		FieldHeight:   240,
		BytesPerPixel: 3,
	})
	if p.deltaLZ4Enabled {
		t.Fatal("deltaLZ4Enabled = true with LZ4 disabled, want false")
	}
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
	video *staticFrameReader
	audio io.Reader
	done  chan struct{}
	once  sync.Once
}

func newStubProcess() *stubProcess {
	return &stubProcess{
		video: newStaticFrameReader(),
		audio: &eofReader{},
		done:  make(chan struct{}),
	}
}

func (s *stubProcess) VideoPipe() io.Reader  { return s.video }
func (s *stubProcess) AudioPipe() io.Reader  { return s.audio }
func (s *stubProcess) Done() <-chan struct{} { return s.done }
func (s *stubProcess) Stop() {
	s.once.Do(func() {
		_ = s.video.Close()
		close(s.done)
	})
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

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
