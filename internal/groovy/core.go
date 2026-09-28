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

// BuildGetVersion returns the 1-byte GET_VERSION query. The core replies
// with a single version byte.
func BuildGetVersion() []byte { return []byte{CmdGetVersion} }

// BuildGetStatus returns the 1-byte GET_STATUS query. Both cores reply
// with a 13-byte ACK whose frame echo is 0.
func BuildGetStatus() []byte { return []byte{CmdGetStatus} }
