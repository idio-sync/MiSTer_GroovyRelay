package groovy

// Protocol command IDs (first byte of every UDP datagram).
// VERIFIED against docs/references/groovy_mister.md §"Command-by-Command Wire
// Format" and docs/references/mistercast.md §"The Five Commands (Byte-Level)".
// These are the exact IDs used by the psakhis/Groovy_MiSTer receiver:
const (
	CmdClose          byte = 1
	CmdInit           byte = 2
	CmdSwitchres      byte = 3
	CmdAudio          byte = 4
	CmdGetStatus      byte = 5
	CmdBlitVSync      byte = 6 // deprecated, progressive-only; unused by relay
	CmdBlitFieldVSync byte = 7
	CmdGetVersion     byte = 8
)

// RGB mode values for INIT byte[4].
const (
	RGBMode888  byte = 0
	RGBMode8888 byte = 1
	RGBMode565  byte = 2
)

// BLIT header duplicate / delta marker values.
const (
	BlitFlagDup   byte = 0x01 // at header byte[8] with header length 9
	BlitFlagDelta byte = 0x01 // at header byte[12] with header length 13
)

// BLIT header length variants (see reference doc).
const (
	BlitHeaderRaw      = 8  // cmd+frame+field+vSync — raw full field
	BlitHeaderRawDup   = 9  // raw with dup marker at [8]
	BlitHeaderLZ4      = 12 // LZ4 full field with cSize at [8..11]
	BlitHeaderLZ4Delta = 13 // LZ4 delta with cSize at [8..11] and delta marker at [12]
)

// INIT byte[1] (lz4Frames) mode values.
//
// On the wire INIT[1] is a binary flag: 0 = raw, 1 = LZ4 enabled. Both
// MiSTerCast (Library/MiSTerCastLib/groovymister.cpp:449 — `m_bufferSend[1] =
// (lz4Frames) ? 1 : 0`) and the Groovy_MiSTer receiver
// (hps_linux/src/support/groovy/groovy.cpp — `compression = recvbufPtr[1]`,
// only 0/1 are recognized) treat it that way. The values 2..6 that appear in
// the MiSTerCast source are an internal per-frame strategy parameter
// (fast vs. LZ4-HC, evaluate delta vs. skip) — they are never transmitted.
//
// Delta-LZ4 is selected per-frame on the wire via the 13-byte BLIT header
// variant (BlitFlagDelta at byte[12]); the receiver accepts that variant by
// header length alone, regardless of INIT[1]. So `LZ4ModeDefault = 1` is the
// correct INIT byte even when emitting delta-flagged BLITs.
const (
	LZ4ModeOff     byte = 0 // raw / no compression
	LZ4ModeDefault byte = 1 // standard LZ4 block (with optional per-frame delta variant)
)

// INIT byte[2] (soundRate) enum.
const (
	AudioRateOff   byte = 0
	AudioRate22050 byte = 1
	AudioRate44100 byte = 2
	AudioRate48000 byte = 3
)

// Integer sample-rate constants (for Go-side math, not wire encoding).
const (
	AudioSampleRate22050 = 22050
	AudioSampleRate44100 = 44100
	AudioSampleRate48000 = 48000
)

// UDP transport constants.
const (
	MisterUDPPort  = 32100
	MaxDatagram    = 1472   // MTU 1500 - IP20 - UDP8
	CongestionSize = 500000 // K_CONGESTION_SIZE in reference (decimal, not 500*1024)
	CongestionWait = 11     // milliseconds (K_CONGESTION_TIME ≈ 110000 ticks)
)

// ACK / status packet size emitted by the MiSTer.
const ACKPacketSize = 13

// AUDIO header size: [0]=cmd, [1..2]=soundSize uint16. PCM bytes that follow
// are streamed as MTU-sized datagrams on the same socket — NOT inlined.
const AudioHeaderSize = 3
