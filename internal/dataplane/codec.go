package dataplane

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// Codec is a frame compression codec. Configured values mirror
// bridge.video.codec. Effective values (after ResolveCodec) are CodecRaw,
// CodecLZ4 or CodecNLC (design 2026-09-28 §4.2).
type Codec string

const (
	CodecAuto Codec = "auto"
	CodecRaw  Codec = "raw"
	CodecLZ4  Codec = "lz4"
	CodecNLC  Codec = "nlc" // GroovyNLC near-lossless; needs the GroovyNLC core
)

// ResolveCodec maps a configured codec to the codec a session uses
// against core (design 2026-09-28 §4.2). The zero value "" means raw, so
// PlaneConfig literals that set no codec keep the uncompressed default.
// warning is non-empty when the configured codec could not be honoured.
//
// NLC needs the GroovyNLC core. Against groovy.CoreUnknown (the provisional
// resolution NewPlane makes before the probe) it resolves to LZ4 without a
// warning; Run re-resolves once the core is known.
func ResolveCodec(configured Codec, core groovy.Core) (effective Codec, warning string) {
	switch configured {
	case CodecAuto, CodecLZ4:
		return CodecLZ4, ""
	case CodecRaw, "":
		return CodecRaw, ""
	case CodecNLC:
		switch core {
		case groovy.CoreGroovyNLC:
			return CodecNLC, ""
		case groovy.CoreUnknown:
			return CodecLZ4, ""
		}
		return CodecLZ4, "codec nlc needs the GroovyNLC core; using lz4"
	}
	return CodecLZ4, fmt.Sprintf("unknown codec %q on %s core; using lz4", configured, core)
}

// initCompressionByte is INIT byte[1] for an effective codec. near and rice
// are the NLC parameters and are ignored for the other codecs.
func initCompressionByte(c Codec, near int, rice bool) byte {
	switch c {
	case CodecLZ4:
		return groovy.LZ4ModeDefault
	case CodecNLC:
		return groovy.NLCCompressionByte(near, rice)
	}
	return groovy.LZ4ModeOff
}
