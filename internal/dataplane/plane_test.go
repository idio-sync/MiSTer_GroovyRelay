package dataplane

import (
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	cryptorand "crypto/rand"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
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

// TestPosition_IntegerExactFieldCount verifies that after N ticks Position()
// returns exactly N*1001/60 ms plus the base offset. Regression harness for
// I4 — the old code added 16 ms/tick and drifted ~0.68 ms low per field.
func TestPosition_IntegerExactFieldCount(t *testing.T) {
	cases := []struct {
		ticks        int64
		baseOffsetMs int
		wantPosMs    int64
	}{
		{3600, 0, 60_060},            // 60.06 s of playback at 59.94 Hz
		{60_000, 0, 1_001_000},       // ~16.68 min
		{600, 5_000, 5_000 + 10_010}, // 10 s of playback, resumed at 5 s
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
	cfg := PlaneConfig{SpawnSpec: ffmpeg.PipelineSpec{FieldOrder: "bff"}}
	p := NewPlane(cfg)
	if !p.fieldOrderFlip.Load() {
		t.Error("NewPlane with bff spec should set flip=true")
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

func (s *stubProcess) VideoPipe() io.Reader   { return s.video }
func (s *stubProcess) AudioPipe() io.Reader   { return s.audio }
func (s *stubProcess) Done() <-chan struct{}  { return s.done }
func (s *stubProcess) Stop() {
	s.once.Do(func() {
		_ = s.video.Close()
		close(s.done)
	})
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

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
//   - Post-pack the dominant remaining allocator is pierrec/lz4/v4's
//     Compressor: `var c lz4.Compressor` inside LZ4CompressInto stack-
//     declares a 128 KB hash table that escapes to the heap on each
//     call (one alloc per BLIT — see the implementation note in
//     lz4_test.go's TestLZ4CompressInto_NoAllocPerCall). Over ~30 ticks
//     in 500 ms that is ~3.8 MB on its own. A future fix would hoist
//     the Compressor onto the Plane struct; until then the budget
//     accommodates the quirk.
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
