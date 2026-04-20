package fakemister

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"net"

	"github.com/jedivoodoo/mister-groovy-relay/internal/groovy"
)

// Listener wraps a UDP socket and decodes incoming datagrams into typed
// Commands. The zero-value is not usable; construct via NewListener.
type Listener struct {
	conn *net.UDPConn
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

// Close releases the underlying socket. Any in-flight Run/RunWithFields loop
// will exit promptly after Close.
func (l *Listener) Close() error { return l.conn.Close() }

// Run reads datagrams and sends parsed Commands into events. Unknown packets
// are logged but not fatal. Exits when the connection is closed.
//
// NOTE: Run treats every datagram as an independent command. It does NOT
// understand BLIT/AUDIO payload datagrams — use RunWithFields for that.
func (l *Listener) Run(events chan<- Command) {
	buf := make([]byte, groovy.MaxDatagram*2)
	for {
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		cmd, err := ParseCommand(buf[:n])
		if err != nil {
			slog.Debug("fakemister parse error", "err", err, "n", n)
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
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		switch mode {
		case modeCommand:
			cmd, err := ParseCommand(data)
			if err != nil {
				slog.Debug("fakemister parse error", "err", err, "n", n)
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
// Per-field VActive for interlaced modes.
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
type Command struct {
	Type         byte
	Init         *InitPayload
	Switchres    *SwitchresPayload
	Audio        *AudioHeader  // set by ParseCommand from a 3-byte AUDIO header
	AudioPayload *AudioPayload // set downstream (AudioEvent) for reassembled PCM
	Blit         *BlitHeader
	Raw          []byte
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
