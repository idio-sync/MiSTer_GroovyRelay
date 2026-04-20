package groovy

import (
	"encoding/binary"
	"fmt"
	"math"
)

// BuildInit returns the 5-byte INIT command packet.
// Wire layout (groovy_mister.md:41-49, mistercast.md:28-34):
//
//	[0] cmd       = 0x02
//	[1] lz4Frames (LZ4ModeOff | LZ4ModeDefault)
//	[2] soundRate (AudioRateOff | AudioRate22050 | AudioRate44100 | AudioRate48000)
//	[3] soundChan (0=off, 1=mono, 2=stereo)
//	[4] rgbMode   (RGBMode888 | RGBMode8888 | RGBMode565)
//
// INIT is the ONE ACK-gated handshake: caller must wait for a 13-byte status
// reply within ~60ms after sending INIT. See groovynet.Sender.SendInitAwaitACK.
func BuildInit(lz4Frames, soundRate, soundChan, rgbMode byte) []byte {
	switch lz4Frames {
	case LZ4ModeOff, LZ4ModeDefault:
	default:
		panic(fmt.Sprintf("groovy: invalid lz4Frames %d", lz4Frames))
	}
	switch soundRate {
	case AudioRateOff, AudioRate22050, AudioRate44100, AudioRate48000:
	default:
		panic(fmt.Sprintf("groovy: invalid soundRate %d", soundRate))
	}
	if soundChan > 2 {
		panic(fmt.Sprintf("groovy: invalid soundChan %d", soundChan))
	}
	switch rgbMode {
	case RGBMode888, RGBMode8888, RGBMode565:
	default:
		panic(fmt.Sprintf("groovy: invalid rgbMode %d", rgbMode))
	}
	return []byte{CmdInit, lz4Frames, soundRate, soundChan, rgbMode}
}

// Modeline holds the SWITCHRES wire values directly. Fields are the
// cumulative VESA offsets (hBegin = hActive+hFrontPorch, hEnd = hBegin+hSync,
// hTotal = hEnd+hBackPorch). Use ModelineFromPorches() to convert from the
// conventional (hFrontPorch, hSync, hBackPorch) triple.
type Modeline struct {
	PClock    float64 // MHz
	HActive   uint16
	HBegin    uint16
	HEnd      uint16
	HTotal    uint16
	VActive   uint16 // per-field when Interlace > 0
	VBegin    uint16
	VEnd      uint16
	VTotal    uint16
	Interlace uint8 // 0=progressive, 1=interlaced, 2=interlaced-force-field-fb
}

// ModelineFromPorches builds a Modeline from the conventional porch triple,
// computing the cumulative offsets the wire format expects.
func ModelineFromPorches(pClock float64,
	hActive, hFrontPorch, hSync, hBackPorch,
	vActive, vFrontPorch, vSync, vBackPorch uint16,
	interlace uint8) Modeline {
	hBegin := hActive + hFrontPorch
	hEnd := hBegin + hSync
	hTotal := hEnd + hBackPorch
	vBegin := vActive + vFrontPorch
	vEnd := vBegin + vSync
	vTotal := vEnd + vBackPorch
	return Modeline{
		PClock:  pClock,
		HActive: hActive, HBegin: hBegin, HEnd: hEnd, HTotal: hTotal,
		VActive: vActive, VBegin: vBegin, VEnd: vEnd, VTotal: vTotal,
		Interlace: interlace,
	}
}

// BuildSwitchres returns the 26-byte SWITCHRES packet.
// Wire layout: groovy_mister.md:51-65, mistercast.md:36-51.
func BuildSwitchres(ml Modeline) []byte {
	buf := make([]byte, 26)
	buf[0] = CmdSwitchres
	binary.LittleEndian.PutUint64(buf[1:9], math.Float64bits(ml.PClock))
	binary.LittleEndian.PutUint16(buf[9:11], ml.HActive)
	binary.LittleEndian.PutUint16(buf[11:13], ml.HBegin)
	binary.LittleEndian.PutUint16(buf[13:15], ml.HEnd)
	binary.LittleEndian.PutUint16(buf[15:17], ml.HTotal)
	binary.LittleEndian.PutUint16(buf[17:19], ml.VActive)
	binary.LittleEndian.PutUint16(buf[19:21], ml.VBegin)
	binary.LittleEndian.PutUint16(buf[21:23], ml.VEnd)
	binary.LittleEndian.PutUint16(buf[23:25], ml.VTotal)
	buf[25] = ml.Interlace
	return buf
}

// NTSC480i60 matches the canonical psakhis/Groovy_MiSTer NTSC 480i entry
// (mistercast.md:138: pClock=13.5, hTotal=858, vTotal=525, interlace=1).
// VActive is per-field (240 lines per field; interlace=1 means the receiver
// alternates field 0 / field 1 across consecutive BLIT_FIELD_VSYNC packets).
// If your CRT prefers different timing, override in config.
var NTSC480i60 = func() Modeline {
	ml := ModelineFromPorches(
		13.5,            // pClock MHz
		720, 16, 62, 60, // hActive, hFrontPorch, hSync, hBackPorch  → hTotal=858
		240, 3, 3, 19, // vActive per-field, vFrontPorch, vSync, vBackPorch
		1, // interlaced
	)
	// Override vTotal to the full-frame value the receiver expects on the wire.
	// mistercast.md:51 shows `m_frameTime = widthTime*vTotal >> interlace`,
	// which requires the wire vTotal be 525 (then right-shifted by 1 for
	// interlace=1). The per-field porch triple above computes 265 locally.
	ml.VTotal = 525
	return ml
}()

// Note: per-field vTotal computed above is 265; vTotal for interlaced NTSC is
// 525 across both fields. Receiver expects per-field values (see
// groovy_mister.md:112 "vActive is already halved per field"); the FPGA
// reconstructs the 525-line frame from two 262/263-line fields. Verify the
// exact vertical porches against a working GroovyMAME pcap before shipping.

// BuildClose returns the 1-byte CLOSE command. Sender tears down the socket
// after; receiver resets session state on next INIT. No ACK.
func BuildClose() []byte {
	return []byte{CmdClose}
}

// BlitOpts controls the variant of the BLIT_FIELD_VSYNC header produced by
// BuildBlitHeader. See BuildBlitHeader for the header-length-to-variant map.
type BlitOpts struct {
	Frame          uint32 // monotonic frame counter
	Field          uint8  // 0 = top/progressive, 1 = bottom
	VSync          uint16 // target raster line; 0 = FPGA chooses
	CompressedSize uint32 // only used when Compressed == true
	Compressed     bool
	Delta          bool
	Duplicate      bool
}

// BuildBlitHeader returns the BLIT_FIELD_VSYNC header bytes. Payload bytes
// (when present) follow the header and MUST be sliced into MaxDatagram-sized
// UDP datagrams with no per-chunk framing. See groovy_mister.md:74-89,
// mistercast.md:59-75 for authoritative byte layout.
//
// Header length encodes the variant:
//
//	 8 bytes — raw uncompressed, full field
//	 9 bytes — duplicate-of-previous (no payload follows)
//	12 bytes — LZ4, full field
//	13 bytes — LZ4 delta (XOR vs previous)
func BuildBlitHeader(o BlitOpts) []byte {
	var length int
	switch {
	case o.Duplicate:
		length = BlitHeaderRawDup
	case o.Compressed && o.Delta:
		length = BlitHeaderLZ4Delta
	case o.Compressed:
		length = BlitHeaderLZ4
	default:
		length = BlitHeaderRaw
	}
	h := make([]byte, length)
	h[0] = CmdBlitFieldVSync
	binary.LittleEndian.PutUint32(h[1:5], o.Frame)
	h[5] = o.Field
	binary.LittleEndian.PutUint16(h[6:8], o.VSync)
	switch {
	case o.Duplicate:
		h[8] = BlitFlagDup
	case o.Compressed:
		binary.LittleEndian.PutUint32(h[8:12], o.CompressedSize)
		if o.Delta {
			h[12] = BlitFlagDelta
		}
	}
	return h
}

// BuildAudioHeader returns the 3-byte AUDIO command header. The caller MUST
// send the `soundSize` PCM bytes immediately after, using the MTU-slicing
// sender (e.g. Sender.SendPayload). NEVER inline PCM into the header datagram
// — PCM fields can reach ~3.2 KB/tick and IP_DONTFRAGMENT will drop
// oversized datagrams.
//
// Sample rate and channel count are session-level state established in INIT.
// Audio is only valid to send while the last ACK's bit 6 (fpga.audio) == 1.
func BuildAudioHeader(soundSize uint16) []byte {
	buf := make([]byte, AudioHeaderSize)
	buf[0] = CmdAudio
	binary.LittleEndian.PutUint16(buf[1:3], soundSize)
	return buf
}
