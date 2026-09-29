// Ported from the GroovyNLC reference codec (api/nlc_codec.cpp) of
// https://github.com/verbst/Groovy_MiSTer at commit e60f52a (release v1.4).
// Original work GPL-2.0-or-later; this port is distributed under this
// project's GPL-3.0 as permitted by that license.

package nlc

import (
	"errors"
	"fmt"
)

// ErrDstSize is returned by Decode when dst is not exactly FrameBytes long.
var ErrDstSize = errors.New("nlc: destination length does not match FrameBytes")

// Decode decodes src into dst (len == FrameBytes). Used by fake-mister and tests.
//
// It mirrors nlc_decode, and produces the reference's output for every
// well-formed stream. Where the reference silently decodes zeros from
// truncated or malformed input, Decode returns an error instead: a line
// header past the end of src, a segment length past the end of src, a
// segment whose bit reader runs past its declared length, or bytes left
// over after the last line. The fourth header length (lenP3) is ignored,
// as it is by the reference for three planes.
func Decode(dst, src []byte, p Params) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if len(dst) != FrameBytes(p) {
		return ErrDstSize
	}
	W, H := p.Width, p.Height
	var prevL, curL [numPlanes][]int16
	for k := range prevL {
		prevL[k] = make([]int16, W)
		curL[k] = make([]int16, W)
	}
	uline := make([]uint32, W)

	csize := len(src)
	pos := 0
	var br bitR
	for y := 0; y < H; y++ {
		var seglen [numPlanes]int
		if pos+headerBytes > csize {
			return fmt.Errorf("nlc: truncated input: line %d header at offset %d past end (%d bytes)", y, pos, csize)
		}
		for k := range seglen {
			seglen[k] = int(src[pos+2*k]) | int(src[pos+2*k+1])<<8
		}
		pos += headerBytes
		for k := 0; k < numPlanes; k++ {
			lo, hi := planeRange(k)
			if seglen[k] > csize-pos {
				return fmt.Errorf("nlc: truncated input: line %d plane %d segment of %d bytes at offset %d past end (%d bytes)", y, k, seglen[k], pos, csize)
			}
			br.init(src[pos : pos+seglen[k]])
			if p.Pack == PackTiled {
				unpackLineTiled(&br, uline, tileLen, widthBits)
			} else {
				unpackLineRice(&br, uline, tileLen, widthBits)
			}
			if br.overrun {
				return fmt.Errorf("nlc: corrupt input: line %d plane %d reads past its %d-byte segment", y, k, seglen[k])
			}
			lineDecode(uline, prevL[k], curL[k], y, p.Near, lo, hi)
			pos += seglen[k]
		}
		combineLine(&curL, dst[y*W*bpp:(y+1)*W*bpp])
		for k := range prevL {
			prevL[k], curL[k] = curL[k], prevL[k]
		}
	}
	if pos != csize {
		return fmt.Errorf("nlc: corrupt input: %d trailing bytes after line %d", csize-pos, H-1)
	}
	return nil
}
