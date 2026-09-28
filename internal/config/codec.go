package config

import "github.com/BurntSushi/toml"

// Frame compression codecs for bridge.video.codec. See
// docs/superpowers/specs/2026-09-28-groovynlc-support-design.md §4.
const (
	CodecAuto = "auto" // bridge picks per core; LZ4 today (§4.2)
	CodecRaw  = "raw"
	CodecLZ4  = "lz4"
)

// EffectiveCodec resolves an unset Codec to CodecAuto.
func (v VideoConfig) EffectiveCodec() string {
	if v.Codec == "" {
		return CodecAuto
	}
	return v.Codec
}

// codecFromLegacyLZ4 maps the pre-codec lz4_enabled boolean.
func codecFromLegacyLZ4(enabled bool) string {
	if enabled {
		return CodecAuto
	}
	return CodecRaw
}

// migrateVideoCodec maps a sectioned config's legacy lz4_enabled key onto
// codec when codec itself is absent, then clears the legacy field so the
// next save drops it (design §4.4).
func migrateVideoCodec(v *VideoConfig, meta toml.MetaData) {
	legacy := v.LegacyLZ4Enabled
	v.LegacyLZ4Enabled = nil
	if legacy == nil || meta.IsDefined("bridge", "video", "codec") {
		return
	}
	v.Codec = codecFromLegacyLZ4(*legacy)
}
