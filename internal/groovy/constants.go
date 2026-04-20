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
const (
	LZ4ModeOff     byte = 0 // raw / no compression
	LZ4ModeDefault byte = 1 // standard LZ4 block
	// Modes 2..6 exist in the reference (HC, delta-adaptive) but we ship mode 1.
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
