package dataplane

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/pierrec/lz4/v4"
)

// processHandle is the narrow set of methods Plane.Run consumes from the
// FFmpeg child process. *ffmpeg.Process satisfies it, and tests can inject
// stubs (in-memory pipe readers, deterministic exit signals) without
// requiring ffmpeg-on-PATH or Unix-only ExtraFiles wiring. This is the
// seam used by TestPlane_AllocationBudget to drive Plane.Run end-to-end
// without spawning a real child.
type processHandle interface {
	VideoPipe() io.Reader
	AudioPipe() io.Reader
	Done() <-chan struct{}
	Stop()
}

type fieldSender interface {
	WaitForCongestion()
	Send([]byte) error
	SendPayload([]byte) error
	MarkBlitSent(int)
}

type fieldSendStats struct {
	total           time.Duration
	lz4             time.Duration
	congestion      time.Duration
	send            time.Duration
	rawBytes        int
	payloadBytes    int
	compressedBytes int
	payloadChunks   int
	wireBytes       int
	deltaSelected   bool
}

type dataplaneStatsWindow struct {
	ticks      uint64
	fieldsSent uint64
	duplicates uint64

	fieldTotal         time.Duration
	maxField           time.Duration
	maxLZ4             time.Duration
	maxCongestion      time.Duration
	maxSend            time.Duration
	sendBudgetOverruns uint64

	maxRawBytes        int
	maxPayloadBytes    int
	maxCompressedBytes int
	maxPayloadChunks   int

	videoQueueHigh           int
	videoPrebufferHigh       int
	videoBacklogAfterPull    uint64
	videoBacklogAfterPullMax int

	audioQueueHigh     int
	audioPrebufferHigh int
	audioRingHigh      int

	wireBytesStart     uint64
	deltaSelectedStart uint64
}

type dataplaneStatsSnapshot struct {
	window time.Duration

	videoQueueLen     int
	videoQueueCap     int
	videoPrebufferLen int

	audioQueueLen     int
	audioQueueCap     int
	audioPrebufferLen int
	audioRingLen      int
	audioRingCap      int

	framesTotal    uint64
	underrunsTotal uint64
	blitsTotal     uint64

	maxFramesAhead     uint32
	currentFramesAhead uint32

	enobufTotal    uint64
	wireBytesTotal uint64
	position       time.Duration
	audioReady     bool

	deltaEnabled       bool
	deltaSelectedTotal uint64
}

func (w *dataplaneStatsWindow) observeField(stats fieldSendStats, budgetThreshold time.Duration) {
	w.fieldTotal += stats.total
	if stats.total > w.maxField {
		w.maxField = stats.total
	}
	if stats.lz4 > w.maxLZ4 {
		w.maxLZ4 = stats.lz4
	}
	if stats.congestion > w.maxCongestion {
		w.maxCongestion = stats.congestion
	}
	if stats.send > w.maxSend {
		w.maxSend = stats.send
	}
	if budgetThreshold > 0 && stats.total > budgetThreshold {
		w.sendBudgetOverruns++
	}
	if stats.rawBytes > w.maxRawBytes {
		w.maxRawBytes = stats.rawBytes
	}
	if stats.payloadBytes > w.maxPayloadBytes {
		w.maxPayloadBytes = stats.payloadBytes
	}
	if stats.compressedBytes > w.maxCompressedBytes {
		w.maxCompressedBytes = stats.compressedBytes
	}
	if stats.payloadChunks > w.maxPayloadChunks {
		w.maxPayloadChunks = stats.payloadChunks
	}
}

func (w *dataplaneStatsWindow) observeQueues(videoQueueLen, videoPrebufferLen, audioQueueLen, audioPrebufferLen, audioRingLen int) {
	if videoQueueLen > w.videoQueueHigh {
		w.videoQueueHigh = videoQueueLen
	}
	if videoPrebufferLen > w.videoPrebufferHigh {
		w.videoPrebufferHigh = videoPrebufferLen
	}
	if audioQueueLen > w.audioQueueHigh {
		w.audioQueueHigh = audioQueueLen
	}
	if audioPrebufferLen > w.audioPrebufferHigh {
		w.audioPrebufferHigh = audioPrebufferLen
	}
	if audioRingLen > w.audioRingHigh {
		w.audioRingHigh = audioRingLen
	}
}

func (w *dataplaneStatsWindow) observeVideoBacklogAfterPull(backlog int) {
	if backlog <= 0 {
		return
	}
	w.videoBacklogAfterPull++
	if backlog > w.videoBacklogAfterPullMax {
		w.videoBacklogAfterPullMax = backlog
	}
}

func (w *dataplaneStatsWindow) reset(wireBytesTotal, deltaSelectedTotal uint64) {
	*w = dataplaneStatsWindow{
		wireBytesStart:     wireBytesTotal,
		deltaSelectedStart: deltaSelectedTotal,
	}
}

func dataplaneStatsAttrs(window dataplaneStatsWindow, snap dataplaneStatsSnapshot) []any {
	var avgFieldMs int64
	if window.fieldsSent > 0 {
		avgFieldMs = (window.fieldTotal / time.Duration(window.fieldsSent)).Milliseconds()
	}
	var duplicatePct float64
	if window.ticks > 0 {
		duplicatePct = 100 * float64(window.duplicates) / float64(window.ticks)
	}
	wireBytesWindow := snap.wireBytesTotal
	if snap.wireBytesTotal >= window.wireBytesStart {
		wireBytesWindow = snap.wireBytesTotal - window.wireBytesStart
	}

	attrs := []any{
		"window_s", int64(snap.window / time.Second),
		"ticks", window.ticks,
		"fields_sent", window.fieldsSent,
		"duplicates", window.duplicates,
		"duplicate_pct", duplicatePct,
		"avg_field_ms", avgFieldMs,
		"max_field_ms", window.maxField.Milliseconds(),
		"max_lz4_ms", window.maxLZ4.Milliseconds(),
		"max_congestion_ms", window.maxCongestion.Milliseconds(),
		"max_send_ms", window.maxSend.Milliseconds(),
		"send_budget_overruns", window.sendBudgetOverruns,
		"max_raw_bytes", window.maxRawBytes,
		"max_payload_bytes", window.maxPayloadBytes,
		"max_compressed_bytes", window.maxCompressedBytes,
		"max_payload_chunks", window.maxPayloadChunks,
		"max_frames_ahead", snap.maxFramesAhead,
		"current_frames_ahead", snap.currentFramesAhead,
		"video_queue_len", snap.videoQueueLen,
		"video_queue_cap", snap.videoQueueCap,
		"video_queue_high", window.videoQueueHigh,
		"video_prebuffer_len", snap.videoPrebufferLen,
		"video_prebuffer_high", window.videoPrebufferHigh,
		"video_backlog_after_pull_ticks", window.videoBacklogAfterPull,
		"video_backlog_after_pull_max", window.videoBacklogAfterPullMax,
		"audio_queue_len", snap.audioQueueLen,
		"audio_queue_cap", snap.audioQueueCap,
		"audio_queue_high", window.audioQueueHigh,
		"audio_prebuffer_len", snap.audioPrebufferLen,
		"audio_prebuffer_high", window.audioPrebufferHigh,
		"audio_ring_len", snap.audioRingLen,
		"audio_ring_cap", snap.audioRingCap,
		"audio_ring_high", window.audioRingHigh,
		"frames_total", snap.framesTotal,
		"underruns_total", snap.underrunsTotal,
		"blits_total", snap.blitsTotal,
		"wire_bytes_window", wireBytesWindow,
		"wire_bytes_total", snap.wireBytesTotal,
		"enobuf_total", snap.enobufTotal,
		"position_s", snap.position.Seconds(),
		"audio_ready", snap.audioReady,
	}
	if snap.deltaEnabled {
		deltaSelected := snap.deltaSelectedTotal
		if snap.deltaSelectedTotal >= window.deltaSelectedStart {
			deltaSelected = snap.deltaSelectedTotal - window.deltaSelectedStart
		}
		attrs = append(attrs,
			"delta_selected", deltaSelected,
			"delta_selected_total", snap.deltaSelectedTotal)
	}
	return attrs
}

// spawnProcess is the indirection Run uses to obtain its processHandle.
// Default points at the production ffmpeg.Spawn; tests swap it for a stub
// constructor. It is not part of the package's public API.
var spawnProcess = func(ctx context.Context, spec ffmpeg.PipelineSpec) (processHandle, error) {
	return ffmpeg.Spawn(ctx, spec)
}

// PlaneConfig is the full knob set Plane.Run needs to stream one session.
// Built by the control plane (core.Manager in Phase 7+) from a SessionRequest
// plus a resolved PipelineSpec / Modeline / LZ4 toggle.
type PlaneConfig struct {
	Sender              *groovynet.Sender
	SpawnSpec           ffmpeg.PipelineSpec
	Modeline            groovy.Modeline // SWITCHRES modeline
	FieldWidth          int             // hActive
	FieldHeight         int             // per-field vActive (e.g. 240 for 480i)
	BytesPerPixel       int
	RGBMode             byte // groovy.RGBMode888 etc.
	LZ4Enabled          bool
	DeltaLZ4Enabled     bool
	AudioRate           int // Go-side integer (48000)
	AudioChans          int // 2 for stereo
	SuppressAudioOutput bool
	OutputVolume        int // global output gain, 0..100
	SeekOffsetMs        int // reported as session start position

	// OnInit is fired exactly once after the INIT handshake completes.
	// nil err = success (FPGA accepted INIT, ready for frames); non-nil
	// err = INIT timeout or socket error (Run will return this same
	// error). Manager wires this into eventlog emission per spec §S7.
	// Optional; may be nil for tests that don't care about init events.
	OnInit func(err error)
}

// framePoolSlots is the depth of the free queue. Sized to videoChCap + 2
// to cover (1 reader in-progress + videoChCap in-channel + 1 tick
// in-progress) given the invariant that ReadFramesFromPipePooled holds
// at most one *FrameBuf outside the pool at any time.
const (
	videoChCap               = 8
	framePoolSlots           = videoChCap + 2
	lz4FallbackDebugInterval = time.Second
	fieldDiagEnv             = "GROOVY_FIELD_DIAG"
	deltaLZ4Env              = "GROOVY_DELTA_LZ4"
	deltaLZ4WinNumerator     = 95
	deltaLZ4WinDenominator   = 100
)

// Startup prebuffer defaults. The field tick loop runs at 59.94 Hz;
// without prebuffer, the first ~30-180 ticks consume a starved videoCh
// (because ffmpeg is still initializing its decoder + filter chain) and
// emit duplicate-field BLITs. Operators see that as "choppy startup."
//
// defaultPrebufferFields holds 6 frames (~100 ms) before the tick starts
// — large enough to absorb ffmpeg's burst-output startup pattern, small
// enough that first-picture latency stays well under Plex's own session-
// start UI delay. defaultPrebufferTimeoutMs is the hard ceiling that
// guarantees a slow ffmpeg cannot deadlock the prebuffer indefinitely;
// on timeout we fall through to the existing underrun→duplicate path.
const (
	defaultPrebufferFields    = 6
	defaultPrebufferTimeoutMs = 5000
	defaultAudioDelay         = 67 * time.Millisecond
	maxAudioDelayFields       = 16
)

// Plane streams one FFmpeg session to the MiSTer. One BLIT_FIELD_VSYNC per
// 59.94 Hz tick, audio gated on the latest ACK's audio-ready bit, and a
// playback-position atomic exposed via Position() for the timeline.
//
// Lifecycle (see Run):
//  1. ffmpeg.Spawn — child process writing raw RGB + s16le PCM into fd 3/4.
//  2. INIT → 60 ms ACK handshake. MUST complete before the Drainer starts,
//     otherwise the Drainer races the handshake reader on the same socket.
//  3. SWITCHRES (fire-and-forget) — sets up the modeline on the FPGA.
//  4. Start the ACK drainer goroutine.
//  5. Start video/audio readers + field timer.
//  6. Pump loop: one BLIT per tick, AUDIO when ACK bit 6 is set.
//  7. CLOSE on ctx cancel or ffmpeg exit.
type Plane struct {
	cfg PlaneConfig
	// fieldSender is the per-field write surface: WaitForCongestion / Send /
	// SendPayload / MarkBlitSent. It is initialized from cfg.Sender at
	// NewPlane time and is intentionally narrower than cfg.Sender (which
	// also exposes INIT, SWITCHRES, CLOSE, ACK draining). The narrow
	// interface exists so tests can substitute a scriptedFieldSender
	// without re-implementing the full groovynet.Sender surface; production
	// code outside sendField/sendDuplicate/sendAudio uses cfg.Sender
	// directly for handshake and lifecycle packets.
	fieldSender    fieldSender
	proc           processHandle
	positionFields atomic.Int64  // fields emitted since session start; Position() derives ms
	framesTotal    atomic.Uint64 // ffmpeg frames consumed since session start
	underruns      atomic.Uint64 // dataplane underruns since session start
	wireBytes      atomic.Uint64 // post-LZ4 bytes sent since session start
	lastACKUnix    atomic.Int64  // unix nanos of last received ACK; 0 = never
	audioReady     atomic.Bool
	outputVolume   atomic.Int32
	fpgaFrame      atomic.Uint32
	done           chan struct{}

	// fieldOrderFlip is the live TFF↔BFF hot-swap. When true, each
	// field's polarity byte is inverted before BLIT_FIELD_VSYNC send.
	// Effect: a 1-raster-line phase shift on the CRT, which is exactly
	// what the operator flips via the UI to fix shimmer without
	// restarting the ffmpeg pipeline.
	fieldOrderFlip atomic.Bool

	// Pre-allocated session-lifetime buffers (perf pack). Owned by the
	// tick loop's goroutine; do not access concurrently. The framePool's
	// buffer count and frameBytes size are determined at NewPlane time
	// from PlaneConfig and held constant for the session lifetime —
	// mid-session resolution changes are not supported.
	framePool    *FramePool
	fieldScratch []byte // len == cfg.FieldWidth * cfg.FieldHeight * cfg.BytesPerPixel
	lz4Scratch   []byte // len == lz4.CompressBlockBound(fieldBytes)
	// lz4Compressor is reused across every BLIT to amortize lz4.Compressor's
	// ~136 KB inline hash table; a fresh one per call would escape to the
	// heap. Owned by the tick goroutine; same single-writer discipline as
	// the scratch slices above. CompressBlock resets the in-use bitmap on
	// entry, so reuse produces identical output to a fresh Compressor.
	lz4Compressor lz4.Compressor
	// headerScratch is shared by sendField and sendDuplicate. Safe because
	// they are called from the same goroutine in mutually-exclusive branches
	// of the tick `select` — never concurrently. Any future change that
	// invokes either from a different goroutine must add a separate scratch
	// buffer or a copy.
	headerScratch []byte // len == groovy.BlitHeaderLZ4Delta

	// Optional delta/diagnostic state. Owned by the tick goroutine. Allocated
	// only when delta-LZ4 or GROOVY_FIELD_DIAG is enabled so the default
	// hot path keeps the existing buffer profile.
	deltaLZ4Enabled  bool
	fieldDiagEnabled bool
	fieldPrev        [2][]byte
	fieldPrevID      [2]fieldHistoryIdentity
	fieldPrevValid   [2]bool

	fieldDeltaScratch    []byte
	fieldDeltaLZ4Scratch []byte
	fieldDeltaLZ4        lz4.Compressor

	// Period of one field in milliseconds, as the rational
	// periodMsNumer/periodMsDenom precomputed at NewPlane from
	// cfg.Modeline.FieldRateRatio(). NTSC: 1001/60 (≈16.683 ms).
	// PAL:  1000/50 (= 20 ms exact). The values are stored as a
	// rational (rather than a float64) so Position()'s integer
	// math stays exact.
	periodMsNumer int64
	periodMsDenom int64

	// lastBudgetWarn throttles the per-tick budget-overrun WARN to at most
	// once per second. Owned by the tick goroutine; not safe for concurrent
	// access (which never happens by construction).
	lastBudgetWarn time.Time

	// LZ4 fallback debug logging is also tick-goroutine owned. Some
	// high-detail scenes can be incompressible for many consecutive fields,
	// so aggregate the hot-path debug line instead of printing per field.
	lastLZ4FallbackDebug       time.Time
	lz4FallbackDebugSuppressed int

	// deltaSelectedTotal counts how many fields the adaptive selector chose
	// to emit as 13-byte LZ4-delta BLITs since session start. Tick-goroutine
	// owned (same single-writer discipline as the scratch slices above); the
	// rolling 5-second stats log in Run snapshots this and reports the
	// per-window delta count via simple subtraction. Counts selector intent
	// regardless of subsequent send success — failed sends are visible as
	// "blit payload send" WARNs.
	deltaSelectedTotal uint64
}

// NewPlane constructs a Plane that is ready to Run. The Sender inside cfg
// must already be bound to the MiSTer's address; Plane does not own the
// sender's lifecycle (the control plane may reuse it across sessions).
// Seeds the field-order flip from cfg.SpawnSpec.FieldOrder: "bff" inverts
// the label, "tff" (or empty) leaves it as-is.
func NewPlane(cfg PlaneConfig) *Plane {
	videoHeight := cfg.resolveVideoHeight()
	frameBytes := cfg.FieldWidth * videoHeight * cfg.BytesPerPixel
	fieldBytes := cfg.FieldWidth * cfg.FieldHeight * cfg.BytesPerPixel
	fieldDiagEnabled := envFieldDiagnostics()
	deltaLZ4Requested := resolveDeltaLZ4Enabled(cfg.DeltaLZ4Enabled)
	deltaLZ4Enabled := cfg.LZ4Enabled && deltaLZ4Requested
	if deltaLZ4Enabled {
		slog.Info("adaptive delta-LZ4 enabled",
			"env", deltaLZ4Env)
	} else if deltaLZ4Requested && !cfg.LZ4Enabled {
		slog.Info("adaptive delta-LZ4 requested but LZ4 disabled",
			"env", deltaLZ4Env)
	}
	fieldHistoryEnabled := fieldDiagEnabled || deltaLZ4Enabled

	p := &Plane{
		cfg:              cfg,
		fieldSender:      cfg.Sender,
		done:             make(chan struct{}),
		framePool:        NewFramePool(framePoolSlots, frameBytes),
		fieldScratch:     make([]byte, fieldBytes),
		lz4Scratch:       make([]byte, lz4.CompressBlockBound(fieldBytes)),
		headerScratch:    make([]byte, groovy.BlitHeaderLZ4Delta),
		deltaLZ4Enabled:  deltaLZ4Enabled,
		fieldDiagEnabled: fieldDiagEnabled,
	}
	p.outputVolume.Store(int32(clampOutputVolume(cfg.OutputVolume)))
	if fieldHistoryEnabled {
		p.fieldPrev[0] = make([]byte, fieldBytes)
		p.fieldPrev[1] = make([]byte, fieldBytes)
		p.fieldDeltaScratch = make([]byte, fieldBytes)
		p.fieldDeltaLZ4Scratch = make([]byte, lz4.CompressBlockBound(fieldBytes))
	}
	// Derive field period (in ms) as a rational from the modeline's
	// FieldRateRatio: period_ms = 1000 / rate_hz, with rate_hz as
	// (rateNumer / rateDenom) → period_ms = 1000 * rateDenom / rateNumer.
	rateNumer, rateDenom := cfg.Modeline.FieldRateRatio()
	if rateNumer <= 0 {
		rateNumer = 60000
		rateDenom = 1001
	}
	p.periodMsNumer = 1000 * rateDenom
	p.periodMsDenom = rateNumer
	if cfg.Modeline.Interlaced() && cfg.SpawnSpec.FieldOrder == "bff" {
		p.fieldOrderFlip.Store(true)
	}
	return p
}

// SetFieldOrder changes the interlace field polarity for subsequent
// BLIT_FIELD_VSYNC packets. Safe to call concurrently with Run —
// the pump loop reads fieldOrderFlip atomically per field. Inverting
// the byte without restarting ffmpeg yields a 1-raster-line phase
// shift, which is the "hot-swap" the UI's ScopeHotSwap tier is
// designed around. Only "tff" and "bff" are valid.
func (p *Plane) SetFieldOrder(order string) error {
	switch order {
	case "tff":
		p.fieldOrderFlip.Store(false)
		return nil
	case "bff":
		p.fieldOrderFlip.Store(true)
		return nil
	default:
		return fmt.Errorf("plane: invalid field order %q (want tff or bff)", order)
	}
}

// SetOutputVolume changes the software gain applied to outgoing PCM audio.
// Safe to call concurrently with Run; the pump loop reads the value atomically
// immediately before each AUDIO packet is emitted.
func (p *Plane) SetOutputVolume(volume int) error {
	if volume < 0 || volume > 100 {
		return fmt.Errorf("output volume must be in 0..100, got %d", volume)
	}
	p.outputVolume.Store(int32(volume))
	return nil
}

// Position returns the current playback offset since start. Seeded with
// cfg.SeekOffsetMs; advanced by one modeline-derived field period
// (periodMsNumer/periodMsDenom ms, exact) per tick. The timeline
// broadcaster (plex adapter) queries this every second; exact integer
// math prevents drift relative to PMS's timestamps.
func (p *Plane) Position() time.Duration {
	fields := p.positionFields.Load()
	ms := fields*p.periodMsNumer/p.periodMsDenom + int64(p.cfg.SeekOffsetMs)
	return time.Duration(ms) * time.Millisecond
}

// resetPosition clears the field counter. Called at the start of Run before
// the pump loop begins — ensures each session starts at exactly the seek
// offset.
func (p *Plane) resetPosition() {
	p.positionFields.Store(0)
}

// BlitsTotal returns the cumulative count of fields emitted (one per
// BLIT_FIELD_VSYNC) since this plane's Run() started. Same source as
// Position(); exposed under a more general name for the status home.
func (p *Plane) BlitsTotal() uint64 {
	v := p.positionFields.Load()
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// FramesTotal returns the cumulative count of ffmpeg frames consumed
// (one per source field after deinterlace, equivalent for progressive).
func (p *Plane) FramesTotal() uint64 { return p.framesTotal.Load() }

// Underruns returns the cumulative dataplane underrun count. Increments
// each time the field-pump goroutine has no frame ready when the field
// deadline arrives.
func (p *Plane) Underruns() uint64 { return p.underruns.Load() }

// WireBytes returns the cumulative post-LZ4 bytes sent across the
// Groovy UDP socket. Drives the status home's throughput readout.
// See PR2 spec §S10.
func (p *Plane) WireBytes() uint64 { return p.wireBytes.Load() }

// LastACKAge returns the wall-clock duration since the last ACK was
// parsed, or 0 if no ACK has arrived yet. The status home displays
// "echo stall" style indicators when this exceeds an adapter-defined
// threshold.
func (p *Plane) LastACKAge() time.Duration {
	ns := p.lastACKUnix.Load()
	if ns == 0 {
		return 0
	}
	return time.Since(time.Unix(0, ns))
}

// advancePosition increments the field counter by one. Called once per field
// tick after a successful BLIT (or BLIT-dup) send.
func (p *Plane) advancePosition() {
	p.positionFields.Add(1)
}

func (p *Plane) emitField(nextField uint8) uint8 {
	if !p.cfg.Modeline.Interlaced() {
		return 0
	}
	if p.fieldOrderFlip.Load() {
		return nextField ^ 1
	}
	return nextField
}

// resolveVideoHeight is the single source of truth for the full
// progressive frame height the FFmpeg pipeline emits. Used by both
// NewPlane (to size frame buffers) and Run (to spawn the reader). MUST
// NOT be duplicated — keeping the resolution in one place prevents the
// frame-pool sizing from drifting away from the reader's expected
// width*height*bpp.
func (cfg PlaneConfig) resolveVideoHeight() int {
	if cfg.SpawnSpec.OutputHeight > 0 {
		return cfg.SpawnSpec.OutputHeight
	}
	h := cfg.FieldHeight
	if cfg.Modeline.Interlaced() {
		h *= 2
	}
	return h
}

// fieldPeriodFromModeline returns one field's wall-clock duration as
// integer nanoseconds. Same semantics as
// time.Duration(float64(time.Second) / ml.FieldRate()) but without the
// sub-µs truncation of float division. Matches the integer-exact
// position math at Position(). Returns 0 on a zero/invalid modeline.
func fieldPeriodFromModeline(ml groovy.Modeline) time.Duration {
	if ml.PClock <= 0 || ml.HTotal == 0 || ml.VTotal == 0 {
		return 0
	}
	pixelsPerField := uint64(ml.HTotal) * uint64(ml.VTotal)
	if ml.Interlaced() {
		pixelsPerField /= 2
	}
	pclockHz := uint64(ml.PClock * 1_000_000)
	if pclockHz == 0 {
		return 0
	}
	return time.Duration((pixelsPerField * 1_000_000_000) / pclockHz)
}

// Done returns a channel closed when Run exits (EOF, ctx cancel, or error).
func (p *Plane) Done() <-chan struct{} { return p.done }

// Run is the orchestration spine. Blocks until ctx is cancelled, the FFmpeg
// child exits, or the video pipe closes. Returns ctx.Err() on cancellation
// and nil on a clean EOF; propagates spawn / handshake errors directly.
func (p *Plane) Run(ctx context.Context) error {
	defer close(p.done)

	proc, err := spawnProcess(ctx, p.cfg.SpawnSpec)
	if err != nil {
		return fmt.Errorf("ffmpeg spawn: %w", err)
	}
	p.proc = proc
	defer proc.Stop()

	audioRate, audioChans := p.effectiveAudioConfig()
	audioEnabled := audioRate > 0 && audioChans > 0
	audioDelayN := 0
	if audioEnabled {
		audioDelayN = envAudioDelayFields(p.cfg.Modeline)
	}

	// 1. INIT handshake (ACK-gated; 60 ms timeout). Must happen BEFORE the
	//    Drainer goroutine starts reading from the socket — otherwise it
	//    swallows the ACK.
	soundRate := rateCodeForHz(audioRate)
	lz4Mode := groovy.LZ4ModeOff
	if p.cfg.LZ4Enabled {
		lz4Mode = groovy.LZ4ModeDefault
	}
	initPkt := groovy.BuildInit(lz4Mode, soundRate, byte(audioChans), p.cfg.RGBMode)
	ack, err := p.cfg.Sender.SendInitAwaitACK(initPkt, 60*time.Millisecond)
	if p.cfg.OnInit != nil {
		p.cfg.OnInit(err)
	}
	if err != nil {
		return fmt.Errorf("init handshake: %w", err)
	}
	p.audioReady.Store(audioEnabled && ack.AudioReady())
	p.fpgaFrame.Store(ack.FPGAFrame)
	p.lastACKUnix.Store(time.Now().UnixNano())

	// Session-start lifecycle marker. One INFO line per session with the
	// negotiated parameters so the operator can correlate later events
	// against what was actually configured.
	sessionStart := time.Now()
	slog.Info("dataplane session started",
		"lz4_enabled", p.cfg.LZ4Enabled,
		"rgb_mode", p.cfg.RGBMode,
		"audio_rate", audioRate,
		"audio_chans", audioChans,
		"audio_ready", p.audioReady.Load(),
		"audio_delay_fields", audioDelayN,
		"audio_delay_ms", float64(audioDelayN)*float64(p.periodMsNumer)/float64(p.periodMsDenom),
		"field_width", p.cfg.FieldWidth,
		"field_height", p.cfg.FieldHeight,
		"interlaced", p.cfg.Modeline.Interlaced(),
		"pclock_mhz", p.cfg.Modeline.PClock,
		"htotal", p.cfg.Modeline.HTotal,
		"vtotal", p.cfg.Modeline.VTotal,
		"direct_play", p.cfg.SpawnSpec.UseSSSeek,
		"seek_offset_ms", p.cfg.SeekOffsetMs,
		"field_order", p.cfg.SpawnSpec.FieldOrder,
		"aspect_mode", p.cfg.SpawnSpec.AspectMode,
		"pacing_us", p.cfg.Sender.PacingInterval().Microseconds())

	// 2. SWITCHRES (fire-and-forget).
	if err := p.cfg.Sender.Send(groovy.BuildSwitchres(p.cfg.Modeline)); err != nil {
		return fmt.Errorf("switchres: %w", err)
	}

	// 3. Start drainer for subsequent ACKs (frame echo, audio-ready updates).
	//    Stop it on return so a preempting session's SendInitAwaitACK gets
	//    uncontested access to the socket — the sender is shared across
	//    sessions for stable source port, so the drainer MUST be explicitly
	//    stopped; closing the socket isn't an option.
	ackCh := make(chan groovy.ACK, 4)
	drainer := groovynet.NewDrainer(p.cfg.Sender, ackCh)
	go drainer.Run()
	defer drainer.Stop()

	// 4. Readers + timer. videoCh now carries *FrameBuf pointers from the
	//    framePool; the tick loop returns each buffer to the pool after
	//    sendField completes. Audio path is unchanged for this perf pack.
	videoCh := make(chan *FrameBuf, videoChCap)
	var audioCh chan []byte
	videoHeight := p.cfg.resolveVideoHeight()
	go ReadFramesFromPipePooledContext(ctx, proc.VideoPipe(), p.framePool, videoCh)
	if audioEnabled {
		audioCh = make(chan []byte, 16)
		go ReadAudioFromPipeContext(ctx, proc.AudioPipe(), audioRate, audioChans, p.cfg.Modeline, audioCh)
	}

	// Audio delay buffer. The CRT introduces structural latency between BLIT
	// arrival and pixels-visible-on-screen (FPGA buffers the field for the
	// next vsync, then the raster scans top-to-bottom over a full field
	// period). Audio chunks queue into the FPGA's audio FIFO and play out
	// through the DAC nearly immediately. Net effect: audio leads video by
	// some integer number of fields, perceptible as A/V desync.
	//
	// GROOVY_AUDIO_DELAY_FIELDS holds N field-periods of audio in a ring
	// buffer before sending. The default is modeline-derived from roughly
	// 67 ms of display latency (4 NTSC fields, 3 PAL fields). Set
	// GROOVY_AUDIO_DELAY_FIELDS=0 to disable for diagnostics, or override
	// with an empirically tuned field count.
	audioRing := make([][]byte, audioDelayN+1)
	audioRingHead, audioRingLen := 0, 0

	// 4b. Startup prebuffer. Wait for ffmpeg to ramp up (open input,
	//     init decoder, build filter chain) before the field tick fires.
	//     Without this, the first ~30-180 ticks consume a starved videoCh
	//     and emit duplicate-field BLITs while ffmpeg is still warming
	//     up — operators see that as choppy startup. audioCh is drained
	//     concurrently so the audio reader can't fill (cap=16) and
	//     backpressure ffmpeg's muxer into a prebuffer deadlock. Index-
	//     paired pulls in the tick loop preserve A/V sync because both
	//     streams arrive from ffmpeg in PTS order.
	prebufferTarget := envPrebufferFields(videoChCap)
	prebufferTimeout := envPrebufferTimeout()
	videoPrebuffer, audioPrebuffer, prebufferExit := p.prebuffer(
		ctx, proc.Done(), videoCh, audioCh, prebufferTarget, prebufferTimeout)
	switch prebufferExit {
	case "context_cancelled", "ffmpeg_exit", "video_pipe_eof":
		for _, fb := range videoPrebuffer {
			p.framePool.Put(fb)
		}
		slog.Info("dataplane session ended during prebuffer",
			"reason", prebufferExit,
			"duration_s", time.Since(sessionStart).Seconds(),
			"enobuf_total", p.cfg.Sender.ENOBUFCount())
		_ = p.cfg.Sender.Send(groovy.BuildClose())
		if prebufferExit == "context_cancelled" {
			return ctx.Err()
		}
		return nil
	}
	// "timeout" or "" — proceed to the tick loop. On timeout, the tick
	// loop's existing underrun → sendDuplicate path covers the gap until
	// ffmpeg catches up, so the prebuffer can never deadlock playback.

	fieldPeriod := fieldPeriodFromModeline(p.cfg.Modeline)
	if fieldPeriod <= 0 {
		// Modeline doesn't produce a valid period (zero PClock etc.).
		// Fall back to the previous float-derived value so we don't
		// silently freeze.
		fieldRate := p.cfg.Modeline.FieldRate()
		if fieldRate <= 0 {
			// Last-resort fallback when the modeline carries no
			// timing at all. Matches NTSC 60000/1001 to avoid
			// freezing the tick loop; production never hits this
			// because all manager-built modelines produce valid
			// periods.
			fieldRate = 60000.0 / 1001.0
		}
		fieldPeriod = time.Duration(float64(time.Second) / fieldRate)
	}
	timer := time.NewTimer(fieldPeriod)
	defer timer.Stop()
	lastTick := time.Now()
	linePeriod := rasterLinePeriod(p.cfg.Modeline)
	latestACK := ack
	lastCorrectedEcho := ack.FrameEcho

	// 5. Position bookkeeping — one tick = one NTSC field (1001/60 ms, exact).
	p.resetPosition()

	var (
		frameNum                uint32 // increments once per BLIT_FIELD_VSYNC
		nextField               uint8
		consecutiveUnderruns    int
		consecutiveUnderrunFrom time.Time

		// FPGA-frame-echo watchdog. The receiver echoes the most recent
		// successfully-decoded frame in every ACK. If our outgoing frameNum
		// keeps advancing but echo stalls, the FPGA is stuck — usually a
		// torn LZ4 field where chunks were dropped mid-payload. Audio
		// continues to play (independent path) which is exactly the
		// "video freezes, audio continues" symptom.
		lastEcho            uint32
		ticksSinceEchoMoved int

		// Rolling 5-second stats. Reset each statsTicker fire. Gives the
		// operator a "grep dataplane stats" view of the session evolving
		// without waiting for threshold-triggered warns.
		statWindow         dataplaneStatsWindow
		statMaxFramesAhead uint32

		// Cumulative counters for the session-end lifecycle marker.
		// Incremented in tandem with the rolling-window counters but
		// never reset — survives across stats-ticker fires.
		sessionTotalFields     uint64
		sessionTotalDuplicates uint64
		sessionEndReason       = "unknown"
	)
	lastEcho = ack.FrameEcho
	statWindow.reset(p.wireBytes.Load(), p.deltaSelectedTotal)
	statsTicker := time.NewTicker(5 * time.Second)
	defer statsTicker.Stop()

	// Session-end lifecycle marker. Fires on every Run exit (ctx cancel,
	// ffmpeg exit, EOF, error). Captures cumulative session totals so the
	// operator can compute aggregate health (avg fields/sec, duplicate
	// share, etc.) without scanning every rolling-stats line.
	defer func() {
		slog.Info("dataplane session ended",
			"reason", sessionEndReason,
			"duration_s", time.Since(sessionStart).Seconds(),
			"fields_sent_total", sessionTotalFields,
			"duplicates_total", sessionTotalDuplicates,
			"final_position_s", p.Position().Seconds(),
			"frame_num_final", frameNum,
			"frame_echo_final", lastEcho,
			"enobuf_total", p.cfg.Sender.ENOBUFCount())
	}()
	// nextField is the row-stripe parity walking 0,1,0,1 every tick. The
	// configured TFF/BFF baseline is encoded in fieldOrderFlip alone (set by
	// NewPlane and SetFieldOrder); seeding nextField here would double-encode
	// it and cancel the flip out across a restart.

	for {
		select {
		case <-ctx.Done():
			sessionEndReason = "context_cancelled"
			_ = p.cfg.Sender.Send(groovy.BuildClose())
			return ctx.Err()
		case <-proc.Done():
			sessionEndReason = "ffmpeg_exit"
			_ = p.cfg.Sender.Send(groovy.BuildClose())
			return nil
		case a := <-ackCh:
			p.audioReady.Store(audioEnabled && a.AudioReady())
			p.fpgaFrame.Store(a.FPGAFrame)
			p.lastACKUnix.Store(time.Now().UnixNano())
			latestACK = a
			if a.FrameEcho != lastEcho {
				lastEcho = a.FrameEcho
				ticksSinceEchoMoved = 0
			}
			if correction, ok := rasterCorrection(a, p.cfg.Modeline, linePeriod, fieldPeriod, lastCorrectedEcho); ok {
				resetTimer(timer, nextTickDelay(lastTick, fieldPeriod, correction))
				lastCorrectedEcho = a.FrameEcho
			}
		case <-statsTicker.C:
			// One-line rolling snapshot of the session. Reset counters
			// each interval so values represent "the last 5 seconds."
			currentFramesAhead := uint32(0)
			if frameNum > lastEcho {
				currentFramesAhead = frameNum - lastEcho
			}
			statWindow.observeQueues(len(videoCh), len(videoPrebuffer), len(audioCh), len(audioPrebuffer), audioRingLen)
			wireBytesTotal := p.wireBytes.Load()
			snap := dataplaneStatsSnapshot{
				window:             5 * time.Second,
				videoQueueLen:      len(videoCh),
				videoQueueCap:      cap(videoCh),
				videoPrebufferLen:  len(videoPrebuffer),
				audioQueueLen:      len(audioCh),
				audioQueueCap:      cap(audioCh),
				audioPrebufferLen:  len(audioPrebuffer),
				audioRingLen:       audioRingLen,
				audioRingCap:       len(audioRing),
				framesTotal:        p.framesTotal.Load(),
				underrunsTotal:     p.underruns.Load(),
				blitsTotal:         p.BlitsTotal(),
				maxFramesAhead:     statMaxFramesAhead,
				currentFramesAhead: currentFramesAhead,
				enobufTotal:        p.cfg.Sender.ENOBUFCount(),
				wireBytesTotal:     wireBytesTotal,
				position:           p.Position(),
				audioReady:         p.audioReady.Load(),
				deltaEnabled:       p.deltaLZ4Enabled,
				deltaSelectedTotal: p.deltaSelectedTotal,
			}
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("dataplane stats", dataplaneStatsAttrs(statWindow, snap)...)
			}
			statWindow.reset(wireBytesTotal, p.deltaSelectedTotal)
			statMaxFramesAhead = 0
		case <-timer.C:
			lastTick = time.Now()
			frameNum++
			ticksSinceEchoMoved++
			statWindow.ticks++
			if framesAhead := frameNum - lastEcho; frameNum > lastEcho && framesAhead > statMaxFramesAhead {
				statMaxFramesAhead = framesAhead
			}
			// FPGA-frame-echo watchdog: if our frameNum keeps advancing but
			// the echo hasn't moved, the receiver is stuck (usually a torn
			// LZ4 field). Log at first sign (60 ticks ≈ 1 sec) and every
			// 5 sec after, so the operator can see the freeze in real time.
			if ticksSinceEchoMoved == 60 || (ticksSinceEchoMoved > 60 && ticksSinceEchoMoved%300 == 0) {
				slog.Warn("FPGA frame echo stalled; sender ahead of receiver",
					"frame_sent", frameNum,
					"frame_echo", lastEcho,
					"frames_ahead", frameNum-lastEcho,
					"ticks_since_echo_moved", ticksSinceEchoMoved,
					"audio_ready", p.audioReady.Load())
			}
			// The FFmpeg pipeline emits full-height progressive frames at the
			// field cadence. Keep the BLIT header field bit aligned to the local
			// row-stripe order here; deriving parity from live vgaF1 feedback
			// would risk tagging a top-field payload as bottom-field (or vice
			// versa).
			//
			// Live TFF↔BFF flip (SetFieldOrder): when the operator swaps field
			// order via the UI mid-session, fieldOrderFlip toggles true. We
			// invert emitField so BOTH the header tag and the payload slice
			// (ExtractFieldFromFrameInto below) swap together — inverting only
			// the header would send top-field pixels tagged as bottom-field.
			emitField := p.emitField(nextField)
			fb, fbOK, fbClosed := pullVideoFrame(&videoPrebuffer, videoCh)
			statWindow.observeQueues(len(videoCh), len(videoPrebuffer), len(audioCh), len(audioPrebuffer), audioRingLen)
			statWindow.observeVideoBacklogAfterPull(len(videoPrebuffer) + len(videoCh))
			if fbClosed {
				sessionEndReason = "video_pipe_eof"
				_ = p.cfg.Sender.Send(groovy.BuildClose())
				return nil
			}
			if fbOK {
				if consecutiveUnderruns >= 30 {
					slog.Debug("video pipe recovered after duplicate-field underrun",
						"fields", consecutiveUnderruns,
						"duration_ms", time.Since(consecutiveUnderrunFrom).Milliseconds())
				}
				consecutiveUnderruns = 0
				consecutiveUnderrunFrom = time.Time{}
				var payload []byte
				if p.cfg.Modeline.Interlaced() {
					ExtractFieldFromFrameInto(p.fieldScratch, fb.Data[:fb.N],
						p.cfg.FieldWidth, videoHeight, p.cfg.BytesPerPixel, emitField)
					payload = p.fieldScratch
				} else {
					payload = fb.Data[:fb.N]
				}
				sendStats := p.sendField(frameNum, emitField, payload)
				statWindow.fieldsSent++
				statWindow.observeField(sendStats, p.fieldBudgetThreshold())
				sessionTotalFields++
				// Trailing Put — invariant (2): sendField does not return
				// errors out of Run, so unconditional Put after sendField
				// is safe. defer is reserved for panic-prone code paths.
				p.framePool.Put(fb)
				p.framesTotal.Add(1)
			} else {
				if consecutiveUnderruns == 0 {
					consecutiveUnderrunFrom = time.Now()
				}
				consecutiveUnderruns++
				p.underruns.Add(1)
				if consecutiveUnderruns == 30 || consecutiveUnderruns%120 == 0 {
					slog.Warn("video pipe underrun; duplicating fields to hold raster",
						"fields", consecutiveUnderruns,
						"duration_ms", time.Since(consecutiveUnderrunFrom).Milliseconds(),
						"audio_ready", p.audioReady.Load())
				}
				p.sendDuplicate(frameNum, emitField)
				statWindow.duplicates++
				sessionTotalDuplicates++
			}
			if p.cfg.Modeline.Interlaced() {
				nextField ^= 1
			} else {
				nextField = 0
			}

			// Audio: gated on ACK bit 6 (fpga.audio). Each tick we (a) drain
			// the audio reader's pending chunk into the ring's tail and (b)
			// pop the ring's head if it holds more than audioDelayN entries.
			// audioDelayN=0 collapses to "send the chunk we just read this
			// tick" (today's behavior); audioDelayN>0 holds N ticks of audio
			// before starting to send, shifting playback later relative to
			// video on the CRT. Never blocks the pump.
			if audioEnabled {
				if pcm := pullAudioChunk(&audioPrebuffer, audioCh); len(pcm) > 0 && audioRingLen < len(audioRing) {
					tail := (audioRingHead + audioRingLen) % len(audioRing)
					audioRing[tail] = pcm
					audioRingLen++
				}
				if audioRingLen > audioDelayN && p.audioReady.Load() {
					oldest := audioRing[audioRingHead]
					audioRing[audioRingHead] = nil
					audioRingHead = (audioRingHead + 1) % len(audioRing)
					audioRingLen--
					if len(oldest) > 0 {
						p.sendAudio(oldest)
					}
				}
			}
			statWindow.observeQueues(len(videoCh), len(videoPrebuffer), len(audioCh), len(audioPrebuffer), audioRingLen)
			// Advance reported position by one field period.
			p.advancePosition()
			if correction, ok := rasterCorrection(latestACK, p.cfg.Modeline, linePeriod, fieldPeriod, lastCorrectedEcho); ok {
				resetTimer(timer, nextTickDelay(lastTick, fieldPeriod, correction))
				lastCorrectedEcho = latestACK.FrameEcho
			} else {
				resetTimer(timer, fieldPeriod)
			}
		}
	}
}

func rasterLinePeriod(ml groovy.Modeline) time.Duration {
	if ml.PClock <= 0 || ml.HTotal == 0 {
		return 0
	}
	seconds := float64(ml.HTotal) / (ml.PClock * 1_000_000)
	return time.Duration(seconds * float64(time.Second))
}

func rasterCorrection(ack groovy.ACK, ml groovy.Modeline, linePeriod, fieldPeriod time.Duration, lastEcho uint32) (time.Duration, bool) {
	if linePeriod <= 0 || ack.FrameEcho == 0 || ack.FrameEcho == lastEcho || ml.VTotal == 0 {
		return 0, false
	}
	vCount1 := (uint64(ack.FrameEcho-1) * uint64(ml.VTotal)) + uint64(ack.VCountEcho)
	vCount2 := (uint64(ack.FPGAFrame) * uint64(ml.VTotal)) + uint64(ack.FPGAVCount)
	if ml.Interlaced() {
		vCount1 >>= 1
		vCount2 >>= 1
	}
	diffLines := int64(vCount1) - int64(vCount2)
	diffLines /= 2 // upstream sender applies half the measured correction.
	correction := time.Duration(diffLines) * linePeriod
	if correction > fieldPeriod/2 {
		correction = fieldPeriod / 2
	}
	if correction < -fieldPeriod/2 {
		correction = -fieldPeriod / 2
	}
	return correction, true
}

func nextTickDelay(lastTick time.Time, fieldPeriod, correction time.Duration) time.Duration {
	delay := fieldPeriod - time.Since(lastTick) + correction
	if delay < 0 {
		return 0
	}
	return delay
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

// effectiveAudioConfig returns the session-level audio settings that should be
// advertised to the MiSTer and used for FFmpeg pipe reads. Sources with no
// audio stream are treated as video-only even when the global config requests
// audio, so the relay doesn't advertise PCM it can never send.
//
// On the DASH dual-input path (SpawnSpec.AudioInputURL non-empty) the probe
// only sees the video-only stream, so probe.AudioRate==0. The presence of a
// separately-resolved audio URL is the affirmative signal that audio exists,
// so we override the probe-zero check in that case.
func (p *Plane) effectiveAudioConfig() (rate, chans int) {
	if p.cfg.SuppressAudioOutput {
		return 0, 0
	}
	if p.cfg.AudioRate <= 0 || p.cfg.AudioChans <= 0 {
		return 0, 0
	}
	if p.cfg.SpawnSpec.AudioInputURL == "" {
		if probe := p.cfg.SpawnSpec.SourceProbe; probe != nil && probe.AudioRate <= 0 {
			return 0, 0
		}
	}
	return p.cfg.AudioRate, p.cfg.AudioChans
}

func (p *Plane) fieldBudgetThreshold() time.Duration {
	fieldPeriodMs := time.Duration(p.periodMsNumer) * time.Millisecond / time.Duration(p.periodMsDenom)
	return fieldPeriodMs * 84 / 100
}

// sendField sends one BLIT_FIELD_VSYNC header + payload using session-
// lifetime scratch buffers (lz4Scratch for the compressed body, headerScratch
// for the header bytes). All allocations are amortized to NewPlane time.
// Applies congestion backoff before the header and records the payload size
// afterwards so the next call can honor the reference ~11 ms wait after any
// >500 KB blit.
//
// Compression policy: if LZ4 is enabled AND the field is compressible
// (LZ4CompressInto returns ok=true), the LZ4 BLIT variant is emitted.
// Otherwise — either LZ4 is disabled in config, OR the field is
// incompressible — a RAW BLIT variant is emitted with the uncompressed
// bytes. Emitting an LZ4 header with CompressedSize=0 would desync the
// receiver.
func (p *Plane) sendField(frame uint32, field uint8, raw []byte) fieldSendStats {
	fieldStart := time.Now()
	stats := fieldSendStats{rawBytes: len(raw)}

	opts := groovy.BlitOpts{Frame: frame, Field: field}
	payload := raw
	choice := fieldPayloadChoice{payload: raw}
	if p.cfg.LZ4Enabled {
		t := time.Now()
		if n, ok := groovy.LZ4CompressInto(&p.lz4Compressor, p.lz4Scratch, raw); ok {
			choice = p.chooseFieldPayload(field, raw, p.lz4Scratch[:n], ok)
			payload = choice.payload
			opts.Compressed = true
			opts.CompressedSize = uint32(len(payload))
			opts.Delta = choice.delta
			stats.compressedBytes = len(payload)
			stats.deltaSelected = choice.delta
			if choice.delta {
				p.deltaSelectedTotal++
			}
		} else {
			p.logLZ4RawFallback(len(raw), time.Now())
		}
		stats.lz4 = time.Since(t)
	}
	stats.payloadBytes = len(payload)
	stats.payloadChunks = datagramChunks(len(payload))

	t := time.Now()
	p.fieldSender.WaitForCongestion()
	stats.congestion = time.Since(t)

	t = time.Now()
	header := groovy.BuildBlitHeaderInto(p.headerScratch, opts)
	if err := p.fieldSender.Send(header); err != nil {
		slog.Warn("blit header send", "err", err)
		stats.total = time.Since(fieldStart)
		return stats
	}
	p.wireBytes.Add(uint64(len(header)))
	stats.wireBytes += len(header)
	if err := p.fieldSender.SendPayload(payload); err != nil {
		slog.Warn("blit payload send", "err", err)
		stats.total = time.Since(fieldStart)
		return stats
	}
	p.wireBytes.Add(uint64(len(payload)))
	stats.wireBytes += len(payload)
	p.fieldSender.MarkBlitSent(len(payload))
	stats.send = time.Since(t)

	// Throttled budget-overrun warn. Threshold is 84% of the field
	// period: NTSC (16.683 ms) => 14.0 ms; PAL (20 ms) => 16.8 ms.
	// If sendField regularly hits this threshold the tick is
	// consistently late and lag will accumulate against the source.
	//
	// Unit dance: time.Duration is int64 nanoseconds. So
	//   time.Duration(periodMsNumer) * time.Millisecond
	// reads as "1001 ns × 1e6 = 1,001,000,000 ns = 1.001 s" for NTSC,
	// then `/ time.Duration(periodMsDenom)` divides by 60-as-ns and the
	// result is 16,683,333 ns = 16.683 ms — the correct field period.
	// The * 84 / 100 scaling stays in time.Duration throughout.
	stats.total = time.Since(fieldStart)
	budgetThreshold := p.fieldBudgetThreshold()
	if stats.total > budgetThreshold && time.Since(p.lastBudgetWarn) > time.Second {
		p.lastBudgetWarn = time.Now()
		attrs := []any{
			"threshold_ms", budgetThreshold.Milliseconds(),
			"total_ms", stats.total.Milliseconds(),
			"lz4_ms", stats.lz4.Milliseconds(),
			"congestion_ms", stats.congestion.Milliseconds(),
			"send_ms", stats.send.Milliseconds(),
			"raw_bytes", stats.rawBytes,
			"compressed_bytes", stats.compressedBytes,
			"lz4_enabled", p.cfg.LZ4Enabled,
		}
		if p.deltaLZ4Enabled {
			deltaSavingsBytes := 0
			if choice.deltaLZ4OK {
				deltaSavingsBytes = choice.fullLZ4Bytes - choice.deltaLZ4Bytes
			}
			attrs = append(attrs,
				"delta_lz4_enabled", true,
				"delta_lz4_available", choice.deltaLZ4Available,
				"delta_lz4_ok", choice.deltaLZ4OK,
				"delta_lz4_selected", choice.delta,
				"delta_lz4_full_bytes", choice.fullLZ4Bytes,
				"delta_lz4_bytes", choice.deltaLZ4Bytes,
				"delta_lz4_chunks", datagramChunks(choice.deltaLZ4Bytes),
				"delta_lz4_savings_bytes", deltaSavingsBytes,
				"delta_lz4_threshold_pct", deltaLZ4WinNumerator)
		}
		if p.fieldDiagEnabled {
			diag := p.measureFieldDiagnostic(field, raw)
			attrs = append(attrs,
				"field_diag_enabled", true,
				"field_diag_current_payload_bytes", len(payload),
				"field_diag_current_payload_chunks", datagramChunks(len(payload)),
				"field_diag_delta_available", diag.available)
			if diag.available {
				deltaSavingsBytes := 0
				if diag.deltaLZ4OK {
					deltaSavingsBytes = len(payload) - diag.deltaLZ4Bytes
				}
				attrs = append(attrs,
					"field_diag_delta_lz4_ok", diag.deltaLZ4OK,
					"field_diag_delta_lz4_bytes", diag.deltaLZ4Bytes,
					"field_diag_delta_lz4_chunks", datagramChunks(diag.deltaLZ4Bytes),
					"field_diag_delta_lz4_ms", diag.deltaLZ4Elapsed.Milliseconds(),
					"field_diag_delta_savings_bytes", deltaSavingsBytes)
			}
		}
		slog.Warn("sendField exceeded 84% of field period", attrs...)
	}
	p.rememberSentFieldHistory(field, raw)
	return stats
}

type fieldPayloadChoice struct {
	payload           []byte
	delta             bool
	fullLZ4Bytes      int
	deltaLZ4Available bool
	deltaLZ4OK        bool
	deltaLZ4Bytes     int
}

type fieldDiagnostic struct {
	available       bool
	deltaLZ4OK      bool
	deltaLZ4Bytes   int
	deltaLZ4Elapsed time.Duration
}

type fieldHistoryIdentity struct {
	bytes       int
	width       int
	height      int
	bytesPerPix int
	rgbMode     byte
	interlaced  bool
}

func shouldUseDeltaLZ4(fullBytes, deltaBytes int) bool {
	if fullBytes <= 0 || deltaBytes <= 0 {
		return false
	}
	return deltaBytes*deltaLZ4WinDenominator < fullBytes*deltaLZ4WinNumerator
}

func (p *Plane) chooseFieldPayload(field uint8, raw []byte, fullLZ4 []byte, fullOK bool) fieldPayloadChoice {
	choice := fieldPayloadChoice{
		payload:      raw,
		fullLZ4Bytes: len(fullLZ4),
	}
	if !fullOK {
		return choice
	}

	choice.payload = fullLZ4
	if !p.deltaLZ4Enabled || !p.hasFieldHistory(field, raw) ||
		len(p.fieldDeltaScratch) < len(raw) || len(p.fieldDeltaLZ4Scratch) == 0 {
		return choice
	}

	choice.deltaLZ4Available = true
	slot := int(field & 1)
	delta := p.fieldDeltaScratch[:len(raw)]
	writeFieldSubDeltaInto(delta, raw, p.fieldPrev[slot])
	n, ok := groovy.LZ4CompressInto(&p.fieldDeltaLZ4, p.fieldDeltaLZ4Scratch, delta)
	choice.deltaLZ4OK = ok
	choice.deltaLZ4Bytes = n
	if ok && shouldUseDeltaLZ4(len(fullLZ4), n) {
		choice.payload = p.fieldDeltaLZ4Scratch[:n]
		choice.delta = true
	}
	return choice
}

func (p *Plane) currentFieldHistoryIdentity(raw []byte) fieldHistoryIdentity {
	return fieldHistoryIdentity{
		bytes:       len(raw),
		width:       p.cfg.FieldWidth,
		height:      p.cfg.FieldHeight,
		bytesPerPix: p.cfg.BytesPerPixel,
		rgbMode:     p.cfg.RGBMode,
		interlaced:  p.cfg.Modeline.Interlaced(),
	}
}

func (p *Plane) hasFieldHistory(field uint8, raw []byte) bool {
	slot := int(field & 1)
	if !p.fieldPrevValid[slot] || len(p.fieldPrev[slot]) != len(raw) ||
		p.fieldPrevID[slot] != p.currentFieldHistoryIdentity(raw) {
		p.invalidateFieldHistory(field)
		return false
	}
	return true
}

func (p *Plane) invalidateFieldHistory(field uint8) {
	slot := int(field & 1)
	p.fieldPrevValid[slot] = false
	p.fieldPrevID[slot] = fieldHistoryIdentity{}
}

func (p *Plane) rememberFieldHistory(field uint8, raw []byte) {
	slot := int(field & 1)
	prev := p.fieldPrev[slot]
	if len(prev) != len(raw) {
		p.invalidateFieldHistory(field)
		return
	}
	copy(prev, raw)
	p.fieldPrevID[slot] = p.currentFieldHistoryIdentity(raw)
	p.fieldPrevValid[slot] = true
}

func (p *Plane) measureFieldDiagnostic(field uint8, raw []byte) fieldDiagnostic {
	slot := int(field & 1)
	if !p.hasFieldHistory(field, raw) ||
		len(p.fieldDeltaScratch) < len(raw) || len(p.fieldDeltaLZ4Scratch) == 0 {
		return fieldDiagnostic{}
	}

	t := time.Now()
	delta := p.fieldDeltaScratch[:len(raw)]
	writeFieldSubDeltaInto(delta, raw, p.fieldPrev[slot])
	n, ok := groovy.LZ4CompressInto(&p.fieldDeltaLZ4, p.fieldDeltaLZ4Scratch, delta)
	return fieldDiagnostic{
		available:       true,
		deltaLZ4OK:      ok,
		deltaLZ4Bytes:   n,
		deltaLZ4Elapsed: time.Since(t),
	}
}

func writeFieldSubDeltaInto(dst, current, previous []byte) {
	for i := range current {
		dst[i] = current[i] - previous[i]
	}
}

func (p *Plane) rememberFieldDiagnostic(field uint8, raw []byte) {
	if !p.fieldDiagEnabled {
		return
	}
	p.rememberFieldHistory(field, raw)
}

func (p *Plane) rememberSentFieldHistory(field uint8, raw []byte) {
	if p.deltaLZ4Enabled {
		p.rememberFieldHistory(field, raw)
		return
	}
	p.rememberFieldDiagnostic(field, raw)
}

func datagramChunks(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + groovy.MaxDatagram - 1) / groovy.MaxDatagram
}

func (p *Plane) logLZ4RawFallback(size int, now time.Time) {
	if !p.lastLZ4FallbackDebug.IsZero() && now.Sub(p.lastLZ4FallbackDebug) < lz4FallbackDebugInterval {
		p.lz4FallbackDebugSuppressed++
		return
	}

	attrs := []any{"size", size}
	if p.lz4FallbackDebugSuppressed > 0 {
		attrs = append(attrs, "suppressed_fields", p.lz4FallbackDebugSuppressed)
		p.lz4FallbackDebugSuppressed = 0
	}
	slog.Debug("lz4 incompressible frame; falling back to RAW BLIT", attrs...)
	p.lastLZ4FallbackDebug = now
}

// sendDuplicate emits a 9-byte dup-BLIT header with no payload. Used on pipe
// under-run to hold the raster: the FPGA re-scans the last field, and our
// frame counter still advances so timing doesn't drift. MarkBlitSent(0)
// resets the congestion window so the next real field isn't delayed.
func (p *Plane) sendDuplicate(frame uint32, field uint8) {
	opts := groovy.BlitOpts{Frame: frame, Field: field, Duplicate: true}
	header := groovy.BuildBlitHeaderInto(p.headerScratch, opts)
	if err := p.fieldSender.Send(header); err != nil {
		slog.Warn("duplicate blit header send", "err", err)
		return
	}
	p.wireBytes.Add(uint64(len(header)))
	p.fieldSender.MarkBlitSent(0) // no payload, no congestion hit
}

// sendAudio emits the 3-byte AUDIO header then the PCM payload. The wire
// soundSize field is uint16, so anything larger than 65535 bytes must be
// truncated. Typical chunk is ~3.2 KB so truncation is purely defensive.
func (p *Plane) sendAudio(pcm []byte) {
	const maxSoundSize = int(^uint16(0)) // 65535
	if len(pcm) > maxSoundSize {
		pcm = pcm[:maxSoundSize]
	}
	scalePCMVolumeInPlace(pcm, int(p.outputVolume.Load()))
	audioHeader := groovy.BuildAudioHeader(uint16(len(pcm)))
	if err := p.fieldSender.Send(audioHeader); err != nil {
		slog.Warn("audio header send", "err", err)
		return
	}
	p.wireBytes.Add(uint64(len(audioHeader)))
	if err := p.fieldSender.SendPayload(pcm); err != nil {
		slog.Warn("audio payload send", "err", err)
		return
	}
	p.wireBytes.Add(uint64(len(pcm)))
}

func clampOutputVolume(volume int) int {
	if volume < 0 {
		return 0
	}
	if volume > 100 {
		return 100
	}
	return volume
}

func scalePCMVolumeInPlace(pcm []byte, volume int) {
	volume = clampOutputVolume(volume)
	if volume >= 100 || len(pcm) == 0 {
		return
	}
	if volume <= 0 {
		for i := range pcm {
			pcm[i] = 0
		}
		return
	}
	for i := 0; i+1 < len(pcm); i += bytesPerSample {
		sample := int32(int16(binary.LittleEndian.Uint16(pcm[i : i+bytesPerSample])))
		scaled := sample * int32(volume) / 100
		binary.LittleEndian.PutUint16(pcm[i:i+bytesPerSample], uint16(int16(scaled)))
	}
}

// prebuffer blocks until videoCh has accumulated `target` frames or a
// terminal condition fires. While waiting it concurrently drains audioCh
// into a local slice — without that drain, ffmpeg's audio-output pipe
// would fill (the audio reader's channel is cap=16, ~267 ms of audio)
// and the muxer would backpressure both outputs, preventing the video
// prebuffer from ever filling. Index-pairing in the tick loop's slice-
// first pull keeps audio aligned with video because both streams arrive
// from ffmpeg in PTS order.
//
// Returns the captured frames/chunks plus an exitReason:
//   - "" — target hit, caller should start the tick loop with these
//     prebuffers.
//   - "context_cancelled" — ctx cancelled mid-wait; caller MUST return
//     ctx.Err() and release the captured frames to the pool.
//   - "ffmpeg_exit" — proc.Done fired before target hit; caller MUST
//     return nil and release captured frames.
//   - "video_pipe_eof" — videoCh closed mid-wait; same as ffmpeg_exit.
//   - "timeout" — hard timeout fired with partial buffer; caller
//     proceeds to the tick loop, which will sendDuplicate until ffmpeg
//     catches up. The captured frames (if any) still feed the loop.
//
// audioCh may be nil (the production state when audio is disabled). Go's
// nil-channel semantics mean the corresponding select case is never
// selected — the prebuffer simply ignores audio in that case.
func (p *Plane) prebuffer(
	ctx context.Context,
	procDone <-chan struct{},
	videoCh <-chan *FrameBuf,
	audioCh <-chan []byte,
	target int,
	timeout time.Duration,
) (video []*FrameBuf, audio [][]byte, exitReason string) {
	if target <= 0 {
		return nil, nil, ""
	}
	video = make([]*FrameBuf, 0, target)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	start := time.Now()
	for len(video) < target {
		select {
		case <-ctx.Done():
			return video, audio, "context_cancelled"
		case <-procDone:
			return video, audio, "ffmpeg_exit"
		case <-deadline.C:
			slog.Warn("prebuffer timeout; starting tick loop with partial buffer",
				"got", len(video), "want", target,
				"audio_chunks", len(audio),
				"wait_ms", time.Since(start).Milliseconds())
			return video, audio, "timeout"
		case fb, ok := <-videoCh:
			if !ok {
				return video, audio, "video_pipe_eof"
			}
			video = append(video, fb)
		case pcm, ok := <-audioCh:
			if ok && len(pcm) > 0 {
				audio = append(audio, pcm)
			}
		}
	}
	slog.Debug("prebuffer complete",
		"video_frames", len(video),
		"audio_chunks", len(audio),
		"wait_ms", time.Since(start).Milliseconds())
	return video, audio, ""
}

// pullVideoFrame returns the next video frame, sourced from the
// prebuffer slice first then from the channel. Mirrors the existing
// `select { case <-videoCh: default }` non-blocking semantics so the
// tick loop never stalls on a missing frame.
//
// Return modes:
//   - fb!=nil, ok=true, closed=false: real frame; caller must Put it.
//   - fb=nil, ok=false, closed=false: underrun (channel empty);
//     caller should sendDuplicate.
//   - fb=nil, ok=false, closed=true: videoCh closed; caller must
//     exit Run.
func pullVideoFrame(prebuf *[]*FrameBuf, ch <-chan *FrameBuf) (fb *FrameBuf, ok, closed bool) {
	if len(*prebuf) > 0 {
		fb = (*prebuf)[0]
		*prebuf = (*prebuf)[1:]
		return fb, true, false
	}
	select {
	case f, open := <-ch:
		if !open {
			return nil, false, true
		}
		return f, true, false
	default:
		return nil, false, false
	}
}

// pullAudioChunk returns the next audio chunk, sourced from the
// prebuffer slice first then from the channel. Returns nil if neither
// has data — caller treats nil as "no audio this tick" (the FPGA's
// audio FIFO holds enough margin that an occasional skip is inaudible).
// Honors a nil channel argument — production passes nil when audio is
// disabled.
func pullAudioChunk(prebuf *[][]byte, ch <-chan []byte) []byte {
	if len(*prebuf) > 0 {
		chunk := (*prebuf)[0]
		*prebuf = (*prebuf)[1:]
		return chunk
	}
	if ch == nil {
		return nil
	}
	select {
	case pcm, ok := <-ch:
		if ok {
			return pcm
		}
		return nil
	default:
		return nil
	}
}

// envPrebufferFields returns the configured number of video frames to
// accumulate before the field tick starts. Tunable via
// GROOVY_PREBUFFER_FIELDS; clamped to [0, max] where max is the video
// channel capacity so the prebuffer can never wait for frames the
// channel cannot hold. n=0 disables the prebuffer entirely (operator
// escape hatch for diagnostics).
func envPrebufferFields(max int) int {
	n := defaultPrebufferFields
	if v := os.Getenv("GROOVY_PREBUFFER_FIELDS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			n = parsed
		} else {
			slog.Warn("invalid GROOVY_PREBUFFER_FIELDS; using default",
				"value", v, "default", defaultPrebufferFields)
		}
	}
	if n > max {
		n = max
	}
	return n
}

func defaultAudioDelayFieldsFor(ml groovy.Modeline) int {
	period := fieldPeriodFromModeline(ml)
	if period <= 0 {
		period = time.Second * 1001 / 60000
	}
	fields := int((defaultAudioDelay + period/2) / period)
	if fields < 0 {
		return 0
	}
	if fields > maxAudioDelayFields {
		return maxAudioDelayFields
	}
	return fields
}

func envAudioDelayFields(ml groovy.Modeline) int {
	n := defaultAudioDelayFieldsFor(ml)
	if v := os.Getenv("GROOVY_AUDIO_DELAY_FIELDS"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err == nil && parsed >= 0 && parsed <= maxAudioDelayFields {
			return parsed
		}
		attrs := []any{"value", v, "default", n, "max", maxAudioDelayFields}
		if err != nil {
			attrs = append(attrs, "err", err)
		}
		slog.Warn("invalid GROOVY_AUDIO_DELAY_FIELDS; using default", attrs...)
	}
	return n
}

// envPrebufferTimeout returns the maximum wall-clock time prebuffer
// will wait. Tunable via GROOVY_PREBUFFER_TIMEOUT_MS. On timeout the
// tick loop starts with whatever frames were captured and the existing
// underrun→sendDuplicate path takes over.
func envPrebufferTimeout() time.Duration {
	ms := defaultPrebufferTimeoutMs
	if v := os.Getenv("GROOVY_PREBUFFER_TIMEOUT_MS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			ms = parsed
		} else {
			slog.Warn("invalid GROOVY_PREBUFFER_TIMEOUT_MS; using default",
				"value", v, "default", defaultPrebufferTimeoutMs)
		}
	}
	return time.Duration(ms) * time.Millisecond
}

func envFieldDiagnostics() bool {
	v := os.Getenv(fieldDiagEnv)
	if v == "" {
		return false
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		slog.Warn("invalid GROOVY_FIELD_DIAG; diagnostics disabled",
			"value", v)
		return false
	}
	if enabled {
		slog.Info("field diagnostics enabled",
			"env", fieldDiagEnv)
	}
	return enabled
}

func resolveDeltaLZ4Enabled(configEnabled bool) bool {
	if envEnabled, ok := envDeltaLZ4Override(); ok {
		return envEnabled
	}
	return configEnabled
}

func envDeltaLZ4() bool {
	enabled, _ := envDeltaLZ4Override()
	return enabled
}

func envDeltaLZ4Override() (bool, bool) {
	v := os.Getenv(deltaLZ4Env)
	if v == "" {
		return false, false
	}
	switch strings.ToLower(v) {
	case "1", "true", "t", "yes", "y", "on":
		return true, true
	case "0", "false", "f", "no", "n", "off":
		return false, true
	default:
		slog.Warn("invalid GROOVY_DELTA_LZ4; delta-LZ4 disabled",
			"value", v)
		return false, true
	}
}

// rateCodeForHz maps the integer audio rate to the wire enum for INIT byte[2].
// Returns AudioRateOff for any unsupported rate — callers should validate
// config upstream so this default does not silently disable audio.
func rateCodeForHz(hz int) byte {
	switch hz {
	case groovy.AudioSampleRate22050:
		return groovy.AudioRate22050
	case groovy.AudioSampleRate44100:
		return groovy.AudioRate44100
	case groovy.AudioSampleRate48000:
		return groovy.AudioRate48000
	}
	return groovy.AudioRateOff
}
