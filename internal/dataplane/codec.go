package dataplane

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// Codec is a frame compression codec. Configured values mirror
// bridge.video.codec. Effective values (after ResolveCodec) are only
// CodecRaw or CodecLZ4 in Part 1 of design 2026-09-28.
type Codec string

const (
	CodecAuto Codec = "auto"
	CodecRaw  Codec = "raw"
	CodecLZ4  Codec = "lz4"
)

// ResolveCodec maps a configured codec to the codec a session uses
// against core (design 2026-09-28 §4.2). The zero value "" means raw, so
// PlaneConfig literals that set no codec keep the uncompressed default.
// warning is non-empty when the configured codec could not be honoured.
func ResolveCodec(configured Codec, core groovy.Core) (effective Codec, warning string) {
	switch configured {
	case CodecAuto, CodecLZ4:
		return CodecLZ4, ""
	case CodecRaw, "":
		return CodecRaw, ""
	}
	return CodecLZ4, fmt.Sprintf("unknown codec %q on %s core; using lz4", configured, core)
}

// initCompressionByte is INIT byte[1] for an effective codec.
func initCompressionByte(c Codec) byte {
	if c == CodecLZ4 {
		return groovy.LZ4ModeDefault
	}
	return groovy.LZ4ModeOff
}
