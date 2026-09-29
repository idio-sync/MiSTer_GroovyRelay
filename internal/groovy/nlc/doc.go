// Ported from the GroovyNLC reference codec (api/nlc_codec.{h,cpp}) of
// https://github.com/verbst/Groovy_MiSTer at commit e60f52a (release v1.4).
// Original work GPL-2.0-or-later; this port is distributed under this
// project's GPL-3.0 as permitted by that license.

// Package nlc is a pure-Go, byte-exact port of the GroovyNLC near-lossless
// video codec: the reference model at api/nlc_codec.{h,cpp} in
// verbst/Groovy_MiSTer at commit e60f52a (release v1.4), licensed
// GPL-2.0-or-later. The fork's FPGA decoder (rtl/nlc_*.v) is bit-exact to
// that reference, so this encoder must be too: any byte of difference is a
// corrupted picture on hardware.
//
// Pipeline (encode): RGB888 -> YCoCg-R (reversible) -> per-plane MED
// prediction -> NEAR quantisation (closed loop) -> TILED or Golomb-Rice
// packing. Wire format v2: each scanline is an 8-byte header of four
// little-endian u16 padded segment lengths, followed by one 64-bit-aligned
// segment per plane (Y, Co, Cg), bits packed LSB-first.
//
// Only Near (0..3) and Pack (tiled or rice) vary. Everything else is fixed
// to what the fork's client builds in GroovyMister::buildNlcParams: RGB888
// input, YCoCg colour, 16-pixel tiles, 4-bit tile headers and adaptive
// per-tile Rice k. These parameters are not transmitted: the INIT byte
// carries only codec, NEAR, colour, display mode and pack, and the FPGA
// decoder hard-codes the rest. Any other value would decode as garbage, so
// they are unexported constants rather than knobs. The reference's GLOBAL
// pack, RGB-direct colour and RGBA/RGB565 formats are deliberately not
// ported.
//
// Every ported function carries a comment naming its C counterpart so the
// two can be read side by side. Bit-exactness is proven by the golden
// vectors in testdata/, which tools/nlcvectors generates by running the
// vendored C++ reference over a fixed input matrix.
package nlc
