package fakemister

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"net"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

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
	return &Listener{conn: conn}, nil
}

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
		cmd, err := ParseCommand(buf[:n])
		if err != nil {
			slog.Debug("fakemister parse error", "err", err, "n", n)
			continue
		}
		cmd.ReceivedAt = recvAt
		if l.ackOnInit && cmd.Type == groovy.CmdInit {
			l.emitInitACK(src)
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
)

// RunWithFields is the full listener loop. After a BLIT_FIELD_VSYNC header
// it reassembles the next N bytes (where N = fieldSizeFn() for RAW, or cSize
// from the LZ4 header) into a FieldEvent. After an AUDIO header it
// reassembles the next soundSize bytes into an AudioEvent. Non-BLIT /
// non-AUDIO commands (INIT, SWITCHRES, CLOSE) go straight to the cmds
// channel — callers use them to update session state.
//
// fieldSizeFn is invoked only for RAW-full BLIT headers (8-byte variant);
// LZ4 headers carry their size at [8..11] and dup headers have no payload.
func (l *Listener) RunWithFields(
	cmds chan<- Command,
	fields chan<- FieldEvent,
	audios chan<- AudioEvent,
	fieldSizeFn func() uint32,
) {
	buf := make([]byte, groovy.MaxDatagram*2)
	var (
		mode       payloadMode
		reass      *Reassembler
		blitHeader BlitHeader
	)
	for {
		n, src, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		recvAt := time.Now()
		data := make([]byte, n)
		copy(data, buf[:n])
		switch mode {
		case modeCommand:
			cmd, err := ParseCommand(data)
			if err != nil {
				slog.Debug("fakemister parse error", "err", err, "n", n)
				continue
			}
			cmd.ReceivedAt = recvAt
			if l.ackOnInit && cmd.Type == groovy.CmdInit {
				l.emitInitACK(src)
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
// five Groovy command IDs (INIT, SWITCHRES, AUDIO-header, BLIT_FIELD_VSYNC,
// CLOSE). Unknown command bytes return an error. Payload datagrams that
// follow BLIT/AUDIO headers are NOT command datagrams and must not reach this
// function — the listener routes them through the reassembler instead.
func ParseCommand(pkt []byte) (Command, error) {
	if len(pkt) == 0 {
		return Command{}, fmt.Errorf("empty packet")
	}
	c := Command{Type: pkt[0], Raw: pkt}
	switch pkt[0] {
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
