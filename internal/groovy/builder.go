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
	VActive   uint16 // full-frame active lines; interlaced modes halve per field on the receiver
	VBegin    uint16
	VEnd      uint16
	VTotal    uint16
	Interlace uint8 // 0=progressive, 1=interlaced, 2=interlaced-force-field-fb
}

// Interlaced reports whether the modeline is one of Groovy's interlaced wire
// modes. The upstream sender treats both 1 and 2 as "halve the payload per
// field"; the distinction only affects how the core programs its framebuffer.
func (m Modeline) Interlaced() bool { return m.Interlace != 0 }

// FieldHeight returns the number of active lines in one transmitted field.
// Groovy SWITCHRES carries full-frame vActive even for interlaced modes; the
// sender/receiver both divide it by two when computing one BLIT payload.
func (m Modeline) FieldHeight() int {
	return FieldLines(m.VActive, m.Interlace)
}

// FieldRate returns the wire BLIT cadence derived from the modeline. The
// upstream sender free-runs at the modeline's frame rate, doubled for
// interlaced output because one BLIT_FIELD_VSYNC carries one field.
func (m Modeline) FieldRate() float64 {
	if m.PClock <= 0 || m.HTotal == 0 || m.VTotal == 0 {
		return 0
	}
	rate := m.PClock * 1_000_000 / (float64(m.HTotal) * float64(m.VTotal))
	if m.Interlaced() {
		rate *= 2
	}
	return rate
}

// FieldRateRatio returns the modeline's field rate as an integer rational
// (numerator, denominator) in Hz. NTSC 480i60 / 240p60: (60000, 1001). PAL
// 576i50 / 288p50: (50, 1). The integers are exact for the four shipped
// presets; for any other modeline values it falls back to deriving from
// FieldRate() with a 1000× scale to preserve three decimal places.
//
// Degenerate modelines (rate ≤ 0) return (1, 1) as a safety sentinel
// so callers can still divide without producing NaN or dividing by zero.
//
// Both Plane.Position (period-in-ms math) and AudioPipeReader (rate-in-Hz
// math) consume this so the data plane never carries a parallel
// rate-descriptor field. The lookup is keyed on (HTotal, VTotal,
// Interlace) — the integer trio uniquely identifies each preset and
// avoids the brittleness of float64 PClock equality.
func (m Modeline) FieldRateRatio() (numer, denom int64) {
	switch {
	case m.HTotal == 880 && m.VTotal == 525 && m.Interlace == 1:
		return 60000, 1001 // NTSC_480i
	case m.HTotal == 880 && m.VTotal == 263 && m.Interlace == 0:
		return 60000, 1001 // NTSC_240p
	case m.HTotal == 864 && m.VTotal == 625 && m.Interlace == 1:
		return 50, 1 // PAL_576i
	case m.HTotal == 864 && m.VTotal == 312 && m.Interlace == 0:
		return 50, 1 // PAL_288p
	}
	rate := m.FieldRate()
	if rate <= 0 {
		return 1, 1
	}
	return int64(rate * 1000), 1000
}

// FieldLines returns the active lines in one transmitted field for the given
// SWITCHRES values.
func FieldLines(vActive uint16, interlace uint8) int {
	if interlace != 0 {
		return int(vActive) / 2
	}
	return int(vActive)
}

// FieldPayloadBytes returns the raw BLIT payload size for one field.
func FieldPayloadBytes(hActive, vActive uint16, interlace uint8, bytesPerPixel int) int {
	return int(hActive) * FieldLines(vActive, interlace) * bytesPerPixel
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

// NTSC480i60 matches the 720x480i NTSC preset shipped by both MiSTerCast and
// Mistglow (`modelines.dat`). Interlaced modelines carry full-frame vertical
// timings on the wire; the core halves payload height internally per field.
var NTSC480i60 = Modeline{
	PClock:    13.846,
	HActive:   720,
	HBegin:    744,
	HEnd:      809,
	HTotal:    880,
	VActive:   480,
	VBegin:    488,
	VEnd:      494,
	VTotal:    525,
	Interlace: 1,
}

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

// BuildBlitHeaderInto writes the header bytes into dst and returns dst[:length]
// where length depends on the variant (see BuildBlitHeader for the variant
// table). dst MUST have len >= BlitHeaderLZ4Delta (13). Intended for the
// hot tick path where re-allocating an 8-13 byte slice on every send would
// churn the heap.
//
// All bytes within the returned dst[:length] are written explicitly: bytes
// 0 through 7 for every variant; byte 8 for Duplicate; bytes 8 through 11
// for Compressed; byte 12 for Compressed+Delta. The caller never observes
// bytes past length, so reuse of dst across calls cannot leak stale bytes.
func BuildBlitHeaderInto(dst []byte, o BlitOpts) []byte {
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
	h := dst[:length]
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
