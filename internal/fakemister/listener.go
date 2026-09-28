package fakemister

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync/atomic"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// Enough room for a full 720x240x3 RAW field burst plus command traffic.
// Tests can send a field before RunWithFields starts; the kernel buffer must
// hold those datagrams until the fake receiver begins reassembly.
const wantRcvBuf = 2 * 1024 * 1024

// Listener wraps a UDP socket and decodes incoming datagrams into typed
// Commands. The zero-value is not usable; construct via NewListener.
type Listener struct {
	conn *net.UDPConn

	// ackOnInit, when true, makes Run / RunWithFields emit a 13-byte ACK
	// back to the sender after every INIT datagram. Integration scenarios
	// that drive the real sender's SendInitAwaitACK path toggle this on so
	// the handshake completes; unit-style tests that poke raw bytes leave
	// it off. audioReadyBit is stamped into the ACK's status byte so a
	// scenario can choose whether the Plane proceeds to send AUDIO
	// (bit 6 = 1) or stays video-only (bit 6 = 0). See §8.2 of the design
	// doc — fake-mister "emits 13-byte ACK packets back to the sender so
	// the sender's drift-correction path exercises realistically."
	ackOnInit     bool
	audioReadyBit bool

	// zeroSizeLZ4Blits counts BLIT headers the real core would arm as a
	// compressed blit of size 0 (see RunWithFields).
	zeroSizeLZ4Blits atomic.Uint64

	// coreVersion is the GET_VERSION reply (0 = default 1, the original
	// core). Version >= 2 impersonates GroovyNLC: SWITCHRES is ACKed.
	// Version < 2 drops 6-byte INITs like the original core. Set before
	// Run / RunWithFields; read without locking by the loop goroutine.
	coreVersion byte
	// idleTimeout > 0 models GroovyNLC idle reaping (design 2026-09-28
	// §1.1f): once a session goes quiet for longer than this, it closes
	// and BLIT/AUDIO/SWITCHRES are ignored until the next INIT.
	// sessionOpen and lastRecv are loop-goroutine owned; reaps is read by
	// tests.
	idleTimeout time.Duration
	sessionOpen bool
	lastRecv    time.Time
	reaps       atomic.Uint64
}

// NewListener binds a UDP socket at addr (e.g. ":32100" or ":0" for an
// ephemeral port) and returns a ready-to-Run listener.
func NewListener(addr string) (*Listener, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetReadBuffer(wantRcvBuf); err != nil {
		slog.Warn("fake MiSTer SetReadBuffer failed", "err", err)
	}
	return &Listener{conn: conn}, nil
}

// ZeroSizeLZ4Blits reports how many BLIT_FIELD_VSYNC headers arrived as the
// 8-byte RAW or 9-byte duplicate variant while INIT had compression on. The
// Groovy core reads those as a compressed blit of size 0 (setBlit ignores the
// dup flag under LZ4), so each one is a corrupted or lost field on real
// hardware. A healthy LZ4 session keeps this at 0.
func (l *Listener) ZeroSizeLZ4Blits() uint64 { return l.zeroSizeLZ4Blits.Load() }

// Addr returns the local UDP address the listener is bound to.
func (l *Listener) Addr() net.Addr { return l.conn.LocalAddr() }

// Conn exposes the underlying UDP socket. Used by test helpers and
// integration harnesses that want to read/write directly (for example a
// stub that replies with an INIT ACK to the sender under test). Not for use
// while a Run or RunWithFields loop is actively consuming the socket.
func (l *Listener) Conn() *net.UDPConn { return l.conn }

// Close releases the underlying socket. Any in-flight Run/RunWithFields loop
// will exit promptly after Close.
func (l *Listener) Close() error { return l.conn.Close() }

// EnableACKs makes the listener reply to every INIT datagram with a 13-byte
// ACK packet, mirroring the real MiSTer's handshake. audioReady toggles
// status bit 6 so a scenario can exercise the Plane's audio-gated pump
// path. Must be called BEFORE Run / RunWithFields starts; the flag is read
// without locking inside the loop. Safe no-op outside integration use.
func (l *Listener) EnableACKs(audioReady bool) {
	l.ackOnInit = true
	l.audioReadyBit = audioReady
}

// SetCoreVersion sets the GET_VERSION reply: 1 impersonates the original
// Groovy core (the default), 2 impersonates GroovyNLC. Call before Run.
func (l *Listener) SetCoreVersion(v byte) { l.coreVersion = v }

// SetIdleTimeout enables GroovyNLC-style idle reaping. Call before Run.
func (l *Listener) SetIdleTimeout(d time.Duration) { l.idleTimeout = d }

// Reaps reports how many sessions the idle timeout has closed.
func (l *Listener) Reaps() uint64 { return l.reaps.Load() }

func (l *Listener) version() byte {
	if l.coreVersion == 0 {
		return 1
	}
	return l.coreVersion
}

// observeArrival records datagram activity. Any datagram counts, as on the
// real core. A gap longer than idleTimeout closes the open session first.
func (l *Listener) observeArrival(now time.Time) {
	if l.idleTimeout > 0 && l.sessionOpen && !l.lastRecv.IsZero() &&
		now.Sub(l.lastRecv) > l.idleTimeout {
		l.sessionOpen = false
		l.reaps.Add(1)
	}
	l.lastRecv = now
}

// respond mirrors the real core's replies to one command datagram. It
// returns false when the core would discard the command.
func (l *Listener) respond(cmd Command, src *net.UDPAddr) bool {
	switch cmd.Type {
	case groovy.CmdGetVersion:
		if l.ackOnInit {
			_, _ = l.conn.WriteToUDP([]byte{l.version()}, src)
		}
		return true
	case groovy.CmdGetStatus:
		if l.ackOnInit {
			l.emitInitACK(src)
		}
		return true
	case groovy.CmdInit:
		if len(cmd.Raw) == 6 && l.version() < 2 {
			return false
		}
		l.sessionOpen = true
		if l.ackOnInit {
			l.emitInitACK(src)
		}
		return true
	case groovy.CmdClose:
		l.sessionOpen = false
		return true
	}
	// Session gating only applies in idle-timeout mode, so existing tests
	// that send blits without an INIT keep working.
	if l.idleTimeout > 0 && !l.sessionOpen {
		return false
	}
	if cmd.Type == groovy.CmdSwitchres && l.ackOnInit && l.version() >= 2 {
		l.emitInitACK(src)
	}
	return true
}

// emitInitACK writes a synthesized ACK back to src. Echo fields are zero —
// the sender does not key any behavior on them across repeated INITs in v1.
// status carries the audio-ready bit 6 when the listener was configured
// with EnableACKs(true).
func (l *Listener) emitInitACK(src *net.UDPAddr) {
	ack := make([]byte, groovy.ACKPacketSize)
	// [0:4]  frameEcho  = 0
	// [4:6]  vCountEcho = 0
	// [6:10] fpgaFrame  = 0
	// [10:12] fpgaVCount = 0
	// [12]   status
	if l.audioReadyBit {
		ack[12] = 1 << 6
	}
	_, _ = l.conn.WriteToUDP(ack, src)
}

// Run reads datagrams and sends parsed Commands into events. Unknown packets
// are logged but not fatal. Exits when the connection is closed.
//
// NOTE: Run treats every datagram as an independent command. It does NOT
// understand BLIT/AUDIO payload datagrams — use RunWithFields for that.
func (l *Listener) Run(events chan<- Command) {
	buf := make([]byte, groovy.MaxDatagram*2)
	for {
		n, src, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		recvAt := time.Now()
		l.observeArrival(recvAt)
		cmd, err := ParseCommand(buf[:n])
		if err != nil {
			slog.Debug("fakemister parse error", "err", err, "n", n)
			continue
		}
		cmd.ReceivedAt = recvAt
		if !l.respond(cmd, src) {
			continue
		}
		events <- cmd
	}
}

// FieldEvent is emitted by RunWithFields after a BLIT_FIELD_VSYNC header and
// its payload datagrams have been reassembled into a contiguous byte buffer.
// Payload is compressed iff Header.Compressed — callers must LZ4-decompress
// before pixel interpretation.
type FieldEvent struct {
	Header  BlitHeader
	Payload []byte
}

// AudioEvent is emitted by RunWithFields after an AUDIO header and its PCM
// datagrams have been reassembled. PCM is 16-bit signed LE, interleaved LRLR
// for stereo, per INIT's sampleRate+channels.
type AudioEvent struct {
	PCM []byte
}

type payloadMode int

const (
	modeCommand payloadMode = iota
	modeBlit
	modeAudio
	// modeZeroLZ4 follows a header the core armed as a compressed blit of
	// size 0 (see RunWithFields); the next datagram resolves it.
	modeZeroLZ4
)

// maxCommandLen is the longest Groovy command datagram (SWITCHRES). The core
// length-checks every command, so a longer datagram in command mode — a
// stray payload chunk after a desync — is ignored rather than parsed.
const maxCommandLen = 26

// RunWithFields is the full listener loop. After a BLIT_FIELD_VSYNC header
// it reassembles the next N bytes (where N = fieldSizeFn() for RAW, or cSize
// from the LZ4 header) into a FieldEvent. After an AUDIO header it
// reassembles the next soundSize bytes into an AudioEvent. Non-BLIT /
// non-AUDIO commands (INIT, SWITCHRES, CLOSE) go straight to the cmds
// channel — callers use them to update session state.
//
// fieldSizeFn is invoked only for RAW-full BLIT headers (8-byte variant);
// LZ4 headers carry their size at [8..11] and dup headers have no payload.
//
// Header meaning follows the last INIT, as on the real core: while INIT has
// compression on, the core (groovy.cpp setBlit / CMD_BLIT_FIELD_VSYNC in
// both psakhis and the verbst fork) honours the dup flag only when
// !blitCompression and reads the 8- and 9-byte variants as a compressed blit
// of size 0. The next datagram then trips its lost-packet check: a short one
// (<= 26 bytes) is re-read as a command, anything else is swallowed. Such a
// header is reported on cmds as Compressed with CompressedSize 0, produces no
// FieldEvent, and is counted by ZeroSizeLZ4Blits.
func (l *Listener) RunWithFields(
	cmds chan<- Command,
	fields chan<- FieldEvent,
	audios chan<- AudioEvent,
	fieldSizeFn func() uint32,
) {
	buf := make([]byte, groovy.MaxDatagram*2)
	var (
		mode        payloadMode
		reass       *Reassembler
		blitHeader  BlitHeader
		compression bool // blitCompression as set by the last INIT
	)
	for {
		n, src, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		recvAt := time.Now()
		l.observeArrival(recvAt)
		data := make([]byte, n)
		copy(data, buf[:n])
		if mode == modeZeroLZ4 {
			mode = modeCommand
			if n > maxCommandLen {
				// A full chunk completes the size-0 blit as LZ4 data; any
				// other long datagram fails the lost-packet check and is
				// dropped. Either way the payload is lost.
				continue
			}
			// Short datagram: lost-packet abort, then re-read as a command.
		}
		switch mode {
		case modeCommand:
			if n > maxCommandLen {
				slog.Debug("fakemister ignoring oversized command datagram", "n", n)
				continue
			}
			cmd, err := ParseCommand(data)
			if err != nil {
				slog.Debug("fakemister parse error", "err", err, "n", n)
				continue
			}
			cmd.ReceivedAt = recvAt
			if !l.respond(cmd, src) {
				// The core discards this command (reaped session, or a
				// 6-byte INIT on the original core). A rejected BLIT/AUDIO
				// header's payload chunks exceed maxCommandLen and are
				// dropped by the oversized-datagram guard above, as on the
				// real core.
				continue
			}
			if cmd.Type == groovy.CmdInit {
				// verbst fork: codecMode = INIT[1] & 3, compression when >= 1.
				// psakhis: INIT[1] <= 1 ? INIT[1] : 0. Both agree on 0 and 1,
				// the only values the relay sends.
				compression = cmd.Init.LZ4Frames&0x3 != 0
			}
			if cmd.Type == groovy.CmdBlitFieldVSync && compression && cmd.Blit != nil && !cmd.Blit.Compressed {
				l.zeroSizeLZ4Blits.Add(1)
				slog.Warn("fakemister: RAW/dup BLIT header during an LZ4 session; real core arms a zero-size compressed blit",
					"frame", cmd.Blit.Frame, "header_len", n)
				cmd.Blit.Duplicate = false
				cmd.Blit.Compressed = true
				cmd.Blit.CompressedSize = 0
				cmds <- cmd
				mode = modeZeroLZ4
				continue
			}
			cmds <- cmd
			switch cmd.Type {
			case groovy.CmdBlitFieldVSync:
				if cmd.Blit == nil || cmd.Blit.Duplicate {
					continue // no payload
				}
				size := cmd.Blit.CompressedSize
				if !cmd.Blit.Compressed {
					size = fieldSizeFn()
				}
				if size == 0 {
					continue
				}
				blitHeader = *cmd.Blit
				reass = NewReassembler(size)
				mode = modeBlit
			case groovy.CmdAudio:
				if cmd.Audio == nil || cmd.Audio.SoundSize == 0 {
					continue
				}
				reass = NewReassembler(uint32(cmd.Audio.SoundSize))
				mode = modeAudio
			}
		case modeBlit:
			if reass.Write(data) {
				fields <- FieldEvent{Header: blitHeader, Payload: reass.Bytes()}
				reass = nil
				mode = modeCommand
			}
		case modeAudio:
			if reass.Write(data) {
				audios <- AudioEvent{PCM: reass.Bytes()}
				reass = nil
				mode = modeCommand
			}
		}
	}
}

// InitPayload carries the five INIT bytes the receiver uses to set up the
// session. NO width/height/interlace here — those come from SWITCHRES.
type InitPayload struct {
	LZ4Frames byte
	SoundRate byte
	SoundChan byte
	RGBMode   byte
}

// SwitchresPayload carries the modeline the receiver uses to program video.
// Groovy SWITCHRES keeps full-frame VActive/VTotal on the wire even for
// interlaced modes; field payload size derives from Interlace separately.
type SwitchresPayload struct {
	PClock    float64
	HActive   uint16
	HTotal    uint16
	VActive   uint16
	VTotal    uint16
	Interlace uint8
}

// AudioHeader is just the 3-byte header; PCM arrives as separate datagrams
// and is collected by the payload-mode listener.
type AudioHeader struct {
	SoundSize uint16
}

// AudioPayload carries reassembled PCM bytes from AudioEvent after the
// payload-mode listener has collected all soundSize bytes across datagrams.
// Distinct from AudioHeader, which only carries the header's soundSize
// metadata before payload collection. The recorder uses AudioPayload.PCM
// for byte accounting.
type AudioPayload struct {
	PCM []byte
}

// BlitHeader carries the fields decoded from a BLIT_FIELD_VSYNC header
// datagram. The RGB (or compressed) payload arrives in subsequent datagrams.
type BlitHeader struct {
	Frame          uint32
	Field          uint8
	VSync          uint16
	Compressed     bool
	Delta          bool
	Duplicate      bool
	CompressedSize uint32
}

// Command is the parsed form of a single command datagram. Exactly one of
// Init/Switchres/Audio/Blit will be non-nil for command types that carry a
// payload; CLOSE carries neither.
//
// ReceivedAt is stamped by Run/RunWithFields immediately after the UDP read
// returns, before the command is enqueued onto the events channel. Callers
// that care about true wire-arrival time (e.g. Recorder's per-field timing
// assertion) should read it instead of calling time.Now() after dequeue —
// downstream processing stalls would otherwise corrupt the measurement.
// Zero when a Command is synthesized outside the listener path.
type Command struct {
	Type         byte
	Init         *InitPayload
	Switchres    *SwitchresPayload
	Audio        *AudioHeader  // set by ParseCommand from a 3-byte AUDIO header
	AudioPayload *AudioPayload // set downstream (AudioEvent) for reassembled PCM
	Blit         *BlitHeader
	Raw          []byte
	ReceivedAt   time.Time
}

// ParseCommand decodes a single datagram into a typed Command. It handles all
// seven Groovy command IDs (INIT, SWITCHRES, AUDIO-header, BLIT_FIELD_VSYNC,
// CLOSE, GET_STATUS, GET_VERSION). Unknown command bytes return an error.
// Payload datagrams that follow BLIT/AUDIO headers are NOT command datagrams
// and must not reach this function — the listener routes them through the
// reassembler instead.
func ParseCommand(pkt []byte) (Command, error) {
	if len(pkt) == 0 {
		return Command{}, fmt.Errorf("empty packet")
	}
	c := Command{Type: pkt[0], Raw: pkt}
	switch pkt[0] {
	case groovy.CmdGetVersion, groovy.CmdGetStatus:
		// 1-byte queries. Longer datagrams starting with these bytes are
		// payload fragments that reached the stateless Run loop.
		if len(pkt) != 1 {
			return c, fmt.Errorf("query command %d must be 1 byte, got %d", pkt[0], len(pkt))
		}
	case groovy.CmdInit:
		// INIT is 4 or 5 bytes (5th = rgbMode, optional — default RGB888).
		if len(pkt) < 4 {
			return c, fmt.Errorf("INIT packet too short: %d", len(pkt))
		}
		ip := &InitPayload{
			LZ4Frames: pkt[1],
			SoundRate: pkt[2],
			SoundChan: pkt[3],
			RGBMode:   groovy.RGBMode888,
		}
		if len(pkt) >= 5 {
			ip.RGBMode = pkt[4]
		}
		c.Init = ip
	case groovy.CmdSwitchres:
		if len(pkt) < 26 {
			return c, fmt.Errorf("SWITCHRES packet too short: %d", len(pkt))
		}
		c.Switchres = &SwitchresPayload{
			PClock:    math.Float64frombits(binary.LittleEndian.Uint64(pkt[1:9])),
			HActive:   binary.LittleEndian.Uint16(pkt[9:11]),
			HTotal:    binary.LittleEndian.Uint16(pkt[15:17]),
			VActive:   binary.LittleEndian.Uint16(pkt[17:19]),
			VTotal:    binary.LittleEndian.Uint16(pkt[23:25]),
			Interlace: pkt[25],
		}
	case groovy.CmdAudio:
		if len(pkt) != groovy.AudioHeaderSize {
			return c, fmt.Errorf("AUDIO header must be exactly %d bytes, got %d",
				groovy.AudioHeaderSize, len(pkt))
		}
		c.Audio = &AudioHeader{
			SoundSize: binary.LittleEndian.Uint16(pkt[1:3]),
		}
	case groovy.CmdBlitFieldVSync:
		if len(pkt) < groovy.BlitHeaderRaw {
			return c, fmt.Errorf("BLIT header too short")
		}
		bh := &BlitHeader{
			Frame: binary.LittleEndian.Uint32(pkt[1:5]),
			Field: pkt[5],
			VSync: binary.LittleEndian.Uint16(pkt[6:8]),
		}
		switch len(pkt) {
		case groovy.BlitHeaderRaw:
			// raw, full field — no tail
		case groovy.BlitHeaderRawDup:
			bh.Duplicate = pkt[8] == groovy.BlitFlagDup
		case groovy.BlitHeaderLZ4:
			bh.Compressed = true
			bh.CompressedSize = binary.LittleEndian.Uint32(pkt[8:12])
		case groovy.BlitHeaderLZ4Delta:
			bh.Compressed = true
			bh.Delta = pkt[12] == groovy.BlitFlagDelta
			bh.CompressedSize = binary.LittleEndian.Uint32(pkt[8:12])
		default:
			return c, fmt.Errorf("BLIT header length %d not in {8,9,12,13}", len(pkt))
		}
		c.Blit = bh
	case groovy.CmdClose:
		// nothing to parse
	default:
		return c, fmt.Errorf("unknown command type %d", pkt[0])
	}
	return c, nil
}
