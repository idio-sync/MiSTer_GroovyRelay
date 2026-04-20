package fakemister

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/jedivoodoo/mister-groovy-relay/internal/groovy"
)

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
	Type      byte
	Init      *InitPayload
	Switchres *SwitchresPayload
	Audio     *AudioHeader
	Blit      *BlitHeader
	Raw       []byte
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
