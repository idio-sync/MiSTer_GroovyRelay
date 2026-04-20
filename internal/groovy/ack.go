package groovy

import (
	"encoding/binary"
	"fmt"
)

// ACK (13 bytes) — emitted by the MiSTer in response to INIT, CMD_GET_STATUS,
// and every BLIT_FIELD_VSYNC. Wire layout: groovy_mister.md:94-103,
// mistercast.md:78-87.
//
//	[0..3]   frameEcho   uint32  — echoes sender's last frame
//	[4..5]   vCountEcho  uint16  — echoes sender's requested vSync line
//	[6..9]   fpgaFrame   uint32  — FPGA's current frame
//	[10..11] fpgaVCount  uint16  — FPGA's current raster line
//	[12]     status      uint8   bitfield:
//	           bit0 vramReady    bit1 vramEndFrame  bit2 vramSynced
//	           bit3 vgaFrameskip bit4 vgaVblank     bit5 vgaF1 (interlace field)
//	           bit6 audio        bit7 vramQueue
type ACK struct {
	FrameEcho  uint32
	VCountEcho uint16
	FPGAFrame  uint32
	FPGAVCount uint16
	Status     byte
}

// AudioReady reports whether the MiSTer's audio buffer wants more samples
// (bit 6 of Status). Sender MUST gate CMD_AUDIO on this bit.
func (a ACK) AudioReady() bool { return a.Status&(1<<6) != 0 }

// VGAField reports the FPGA's current interlace field (bit 5 of Status).
// Useful for field-order drift detection.
func (a ACK) VGAField() bool { return a.Status&(1<<5) != 0 }

// VRAMReady reports whether the FPGA can accept another BLIT right now
// (bit 0). Informational — not a gate; sender free-runs off its own clock.
func (a ACK) VRAMReady() bool { return a.Status&(1<<0) != 0 }

// ParseACK parses a 13-byte ACK datagram from the MiSTer.
func ParseACK(pkt []byte) (ACK, error) {
	if len(pkt) != ACKPacketSize {
		return ACK{}, fmt.Errorf("ack: expected %d bytes, got %d", ACKPacketSize, len(pkt))
	}
	return ACK{
		FrameEcho:  binary.LittleEndian.Uint32(pkt[0:4]),
		VCountEcho: binary.LittleEndian.Uint16(pkt[4:6]),
		FPGAFrame:  binary.LittleEndian.Uint32(pkt[6:10]),
		FPGAVCount: binary.LittleEndian.Uint16(pkt[10:12]),
		Status:     pkt[12],
	}, nil
}
