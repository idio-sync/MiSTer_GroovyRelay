package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// PlaneConfig is the full knob set Plane.Run needs to stream one session.
// Built by the control plane (core.Manager in Phase 7+) from a SessionRequest
// plus a resolved PipelineSpec / Modeline / LZ4 toggle.
type PlaneConfig struct {
	Sender        *groovynet.Sender
	SpawnSpec     ffmpeg.PipelineSpec
	Modeline      groovy.Modeline // SWITCHRES modeline
	FieldWidth    int             // hActive
	FieldHeight   int             // per-field vActive (e.g. 240 for 480i)
	BytesPerPixel int
	RGBMode       byte // groovy.RGBMode888 etc.
	LZ4Enabled    bool
	AudioRate     int // Go-side integer (48000)
	AudioChans    int // 2 for stereo
	SeekOffsetMs  int // reported as session start position
}

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
	cfg            PlaneConfig
	proc           *ffmpeg.Process
	positionFields atomic.Int64 // fields emitted since session start; Position() derives ms
	audioReady     atomic.Bool
	fpgaFrame      atomic.Uint32
	done           chan struct{}
}

// NewPlane constructs a Plane that is ready to Run. The Sender inside cfg
// must already be bound to the MiSTer's address; Plane does not own the
// sender's lifecycle (the control plane may reuse it across sessions).
func NewPlane(cfg PlaneConfig) *Plane {
	return &Plane{cfg: cfg, done: make(chan struct{})}
}

// Position returns the current playback offset since start. Seeded with
// cfg.SeekOffsetMs; advanced by one NTSC field period (1001/60 ms, exact)
// per tick. The timeline broadcaster (plex adapter) queries this every
// second; exact integer math prevents drift relative to PMS's timestamps.
func (p *Plane) Position() time.Duration {
	fields := p.positionFields.Load()
	ms := fields*1001/60 + int64(p.cfg.SeekOffsetMs)
	return time.Duration(ms) * time.Millisecond
}

// resetPosition clears the field counter. Called at the start of Run before
// the pump loop begins — ensures each session starts at exactly the seek
// offset.
func (p *Plane) resetPosition() {
	p.positionFields.Store(0)
}

// advancePosition increments the field counter by one. Called once per field
// tick after a successful BLIT (or BLIT-dup) send.
func (p *Plane) advancePosition() {
	p.positionFields.Add(1)
}

// Done returns a channel closed when Run exits (EOF, ctx cancel, or error).
func (p *Plane) Done() <-chan struct{} { return p.done }

// Run is the orchestration spine. Blocks until ctx is cancelled, the FFmpeg
// child exits, or the video pipe closes. Returns ctx.Err() on cancellation
// and nil on a clean EOF; propagates spawn / handshake errors directly.
func (p *Plane) Run(ctx context.Context) error {
	defer close(p.done)

	proc, err := ffmpeg.Spawn(ctx, p.cfg.SpawnSpec)
	if err != nil {
		return fmt.Errorf("ffmpeg spawn: %w", err)
	}
	p.proc = proc
	defer proc.Stop()

	audioRate, audioChans := p.effectiveAudioConfig()
	audioEnabled := audioRate > 0 && audioChans > 0

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
	if err != nil {
		return fmt.Errorf("init handshake: %w", err)
	}
	p.audioReady.Store(audioEnabled && ack.AudioReady())
	p.fpgaFrame.Store(ack.FPGAFrame)

	// 2. SWITCHRES (fire-and-forget).
	if err := p.cfg.Sender.Send(groovy.BuildSwitchres(p.cfg.Modeline)); err != nil {
		return fmt.Errorf("switchres: %w", err)
	}

	// 3. Start drainer for subsequent ACKs (frame echo, audio-ready updates).
	//    Stop it on return so a preempting session's SendInitAwaitACK gets
	//    uncontested access to the socket — the sender is shared across
	//    sessions for stable source port, so the drainer MUST be explicitly
	//    stopped; closing the socket isn't an option.
	ackCh := make(chan groovy.ACK, 32)
	drainer := groovynet.NewDrainer(p.cfg.Sender, ackCh)
	go drainer.Run()
	defer drainer.Stop()

	// 4. Readers + timer.
	videoCh := make(chan []byte, 4)
	var audioCh chan []byte
	videoHeight := p.cfg.SpawnSpec.OutputHeight
	if videoHeight <= 0 {
		videoHeight = p.cfg.FieldHeight
		if p.cfg.Modeline.Interlaced() {
			videoHeight *= 2
		}
	}
	go ReadFramesFromPipe(proc.VideoPipe(), p.cfg.FieldWidth, videoHeight, p.cfg.BytesPerPixel, videoCh)
	if audioEnabled {
		audioCh = make(chan []byte, 16)
		go ReadAudioFromPipe(proc.AudioPipe(), audioRate, audioChans, audioCh)
	}
	fieldRate := p.cfg.Modeline.FieldRate()
	if fieldRate <= 0 {
		fieldRate = 59.94
	}
	fieldPeriod := time.Duration(float64(time.Second) / fieldRate)
	timer := time.NewTimer(fieldPeriod)
	defer timer.Stop()
	lastTick := time.Now()
	linePeriod := rasterLinePeriod(p.cfg.Modeline)
	latestACK := ack
	lastCorrectedEcho := ack.FrameEcho

	// 5. Position bookkeeping — one tick = one NTSC field (1001/60 ms, exact).
	p.resetPosition()

	var (
		frameNum  uint32 // increments once per BLIT_FIELD_VSYNC
		nextField uint8
	)
	if p.cfg.Modeline.Interlaced() {
		nextField = initialFieldForOrder(p.cfg.SpawnSpec.FieldOrder)
	}

	for {
		select {
		case <-ctx.Done():
			_ = p.cfg.Sender.Send(groovy.BuildClose())
			return ctx.Err()
		case <-proc.Done():
			_ = p.cfg.Sender.Send(groovy.BuildClose())
			return nil
		case a := <-ackCh:
			p.audioReady.Store(audioEnabled && a.AudioReady())
			p.fpgaFrame.Store(a.FPGAFrame)
			latestACK = a
			if correction, ok := rasterCorrection(a, p.cfg.Modeline, linePeriod, fieldPeriod, lastCorrectedEcho); ok {
				resetTimer(timer, nextTickDelay(lastTick, fieldPeriod, correction))
				lastCorrectedEcho = a.FrameEcho
			}
		case <-timer.C:
			lastTick = time.Now()
			frameNum++
			// The FFmpeg pipeline emits full-height progressive frames at the
			// field cadence. Keep the BLIT header field bit aligned to the local
			// row-stripe order here; deriving parity from live vgaF1 feedback
			// would risk tagging a top-field payload as bottom-field (or vice
			// versa).
			select {
			case frame, ok := <-videoCh:
				if !ok {
					_ = p.cfg.Sender.Send(groovy.BuildClose())
					return nil
				}
				payload := frame
				if p.cfg.Modeline.Interlaced() {
					payload = ExtractFieldFromFrame(frame, p.cfg.FieldWidth, videoHeight, p.cfg.BytesPerPixel, nextField)
				}
				p.sendField(frameNum, nextField, payload)
			default:
				// Under-run — send a duplicate field to hold the raster.
				p.sendDuplicate(frameNum, nextField)
			}
			if p.cfg.Modeline.Interlaced() {
				nextField ^= 1
			} else {
				nextField = 0
			}

			// Audio: only send while ACK bit 6 (fpga.audio) is set AND we
			// have PCM ready. Never block the pump loop on audio.
			if audioEnabled && p.audioReady.Load() {
				select {
				case pcm, ok := <-audioCh:
					if ok && len(pcm) > 0 {
						p.sendAudio(pcm)
					}
				default:
				}
			}
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

func initialFieldForOrder(order string) uint8 {
	if order == "bff" {
		return 1
	}
	return 0
}

func terminalFieldForOrder(order string) uint8 {
	if order == "bff" {
		return 0
	}
	return 1
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
func (p *Plane) effectiveAudioConfig() (rate, chans int) {
	if p.cfg.AudioRate <= 0 || p.cfg.AudioChans <= 0 {
		return 0, 0
	}
	if probe := p.cfg.SpawnSpec.SourceProbe; probe != nil && probe.AudioRate <= 0 {
		return 0, 0
	}
	return p.cfg.AudioRate, p.cfg.AudioChans
}

// sendField sends one BLIT_FIELD_VSYNC header + payload. Applies congestion
// backoff before the header and records the payload size afterwards so the
// next call can honor the reference ~11 ms wait after any >500 KB blit.
//
// Compression policy: if LZ4 is enabled AND the field is compressible
// (LZ4Compress returns ok=true), the LZ4 BLIT variant is emitted. Otherwise
// — either LZ4 is disabled in config, OR the field is incompressible (e.g.
// random-noise content, encrypted stream payload) — a RAW BLIT variant is
// emitted with the uncompressed bytes. Emitting an LZ4 header with
// CompressedSize=0 would desync the receiver.
func (p *Plane) sendField(frame uint32, field uint8, raw []byte) {
	opts := groovy.BlitOpts{Frame: frame, Field: field}
	payload := raw
	if p.cfg.LZ4Enabled {
		if compressed, ok := groovy.LZ4Compress(raw); ok {
			payload = compressed
			opts.Compressed = true
			opts.CompressedSize = uint32(len(compressed))
		} else {
			slog.Debug("lz4 incompressible frame; falling back to RAW BLIT", "size", len(raw))
		}
	}
	p.cfg.Sender.WaitForCongestion()
	if err := p.cfg.Sender.Send(groovy.BuildBlitHeader(opts)); err != nil {
		slog.Warn("blit header send", "err", err)
		return
	}
	if err := p.cfg.Sender.SendPayload(payload); err != nil {
		slog.Warn("blit payload send", "err", err)
		return
	}
	p.cfg.Sender.MarkBlitSent(len(payload))
}

// sendDuplicate emits a 9-byte dup-BLIT header with no payload. Used on pipe
// under-run to hold the raster: the FPGA re-scans the last field, and our
// frame counter still advances so timing doesn't drift. MarkBlitSent(0)
// resets the congestion window so the next real field isn't delayed.
func (p *Plane) sendDuplicate(frame uint32, field uint8) {
	opts := groovy.BlitOpts{Frame: frame, Field: field, Duplicate: true}
	_ = p.cfg.Sender.Send(groovy.BuildBlitHeader(opts))
	p.cfg.Sender.MarkBlitSent(0) // no payload, no congestion hit
}

// sendAudio emits the 3-byte AUDIO header then the PCM payload. The wire
// soundSize field is uint16, so anything larger than 65535 bytes must be
// truncated. Typical chunk is ~3.2 KB so truncation is purely defensive.
func (p *Plane) sendAudio(pcm []byte) {
	const maxSoundSize = int(^uint16(0)) // 65535
	if len(pcm) > maxSoundSize {
		pcm = pcm[:maxSoundSize]
	}
	if err := p.cfg.Sender.Send(groovy.BuildAudioHeader(uint16(len(pcm)))); err != nil {
		slog.Warn("audio header send", "err", err)
		return
	}
	if err := p.cfg.Sender.SendPayload(pcm); err != nil {
		slog.Warn("audio payload send", "err", err)
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
