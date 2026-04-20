package groovy

import "fmt"

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
