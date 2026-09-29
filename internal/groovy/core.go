package groovy

// Core identifies which Groovy_MiSTer build answered GET_VERSION.
// See docs/superpowers/specs/2026-09-28-groovynlc-support-design.md §3.1.
type Core uint8

const (
	CoreUnknown   Core = iota // not probed yet
	CoreGroovy                // original psakhis core (GET_VERSION = 1, or no reply)
	CoreGroovyNLC             // verbst fork (GET_VERSION >= 2)
)

// CoreFromVersion maps a GET_VERSION reply byte to a Core. 0 means the
// probe timed out; the original core is assumed.
func CoreFromVersion(v byte) Core {
	if v >= 2 {
		return CoreGroovyNLC
	}
	return CoreGroovy
}

func (c Core) String() string {
	switch c {
	case CoreGroovy:
		return "groovy"
	case CoreGroovyNLC:
		return "groovynlc"
	}
	return "unknown"
}

// NLCCompressionByte is INIT byte[1] for a GroovyNLC session: codec 2 (NLC),
// NEAR in bits [3:2], YCoCg colour (bit 4), display mode 2 (bits [6:5], the
// fork client's default) and the pack in bit 7 (1 = Rice). Design §4.3,
// fork groovy.cpp setInit / api/groovymister.cpp:836-837.
func NLCCompressionByte(near int, rice bool) byte {
	b := byte(nlcCodecMode) | byte(near&3)<<2 | 1<<4 | 2<<5
	if rice {
		b |= 1 << 7
	}
	return b
}

// nlcCodecMode is the codec field (INIT byte[1] bits [1:0]) the fork reads
// as NLC.
const nlcCodecMode = 2

// BuildGetVersion returns the 1-byte GET_VERSION query. The core replies
// with a single version byte.
func BuildGetVersion() []byte { return []byte{CmdGetVersion} }

// BuildGetStatus returns the 1-byte GET_STATUS query. Both cores reply
// with a 13-byte ACK whose frame echo is 0.
func BuildGetStatus() []byte { return []byte{CmdGetStatus} }
