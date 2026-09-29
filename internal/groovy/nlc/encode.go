// Ported from the GroovyNLC reference codec (api/nlc_codec.cpp) of
// https://github.com/verbst/Groovy_MiSTer at commit e60f52a (release v1.4).
// Original work GPL-2.0-or-later; this port is distributed under this
// project's GPL-3.0 as permitted by that license.

package nlc

import (
	"errors"
	"fmt"
	"sync"
)

// Pack selects the residual packing front-end (nlc_pack_t).
type Pack uint8

const (
	PackTiled Pack = iota // NLC_PACK_TILED
	PackRice              // NLC_PACK_RICE
)

// String returns "tiled" or "rice".
func (p Pack) String() string {
	switch p {
	case PackTiled:
		return "tiled"
	case PackRice:
		return "rice"
	}
	return fmt.Sprintf("Pack(%d)", uint8(p))
}

// Params are the per-session knobs; everything else is fixed (see package doc).
type Params struct {
	Width, Height int // pixels; Height is the field height for interlaced modes
	Near          int // 0 = lossless, 1..3 near-lossless
	Pack          Pack
}

// Validate reports whether p is usable: positive dimensions, 0 <= Near <= 3
// and a known Pack.
func (p Params) Validate() error {
	if p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("nlc: invalid dimensions %dx%d", p.Width, p.Height)
	}
	if p.Near < 0 || p.Near > 3 {
		return fmt.Errorf("nlc: invalid near %d (want 0..3)", p.Near)
	}
	if p.Pack != PackTiled && p.Pack != PackRice {
		return fmt.Errorf("nlc: invalid pack %d", uint8(p.Pack))
	}
	return nil
}

// FrameBytes mirrors nlc_frame_bytes for NLC_RGB888: W*H*3.
func FrameBytes(p Params) int { return p.Width * p.Height * bpp }

// MaxEncodedSize mirrors nlc_max_encoded_size: 2x raw + W*H + 40 bytes per
// line (header and segment padding) + 1024.
func MaxEncodedSize(p Params) int {
	return 2*FrameBytes(p) + p.Width*p.Height + p.Height*40 + 1024
}

var (
	// ErrSrcSize is returned when the source is not exactly FrameBytes long.
	ErrSrcSize = errors.New("nlc: source length does not match FrameBytes")
	// ErrDstTooSmall is returned when dst is shorter than MaxEncodedSize.
	ErrDstTooSmall = errors.New("nlc: destination shorter than MaxEncodedSize")
	// ErrOverflow mirrors nlc_encode's -1 for an output that does not fit
	// (a line segment over 0xffff bytes or a frame over the buffer). It
	// cannot happen for validated Params of any realistic size.
	ErrOverflow = errors.New("nlc: encoded output overflow")
)

// planeScratch is one plane's private encode state: the equivalents of the
// reference encode_plane lambda's locals plus its segbuf/seglen/povf slot.
type planeScratch struct {
	t      []int16  // this plane's source row (to_planes output, one row)
	prev   []int16  // previous reconstructed row
	cur    []int16  // row being reconstructed
	ul     []uint32 // zigzag residuals of one row
	segbuf []byte   // all of this plane's line segments, back to back
	seglen []uint16 // padded segment length of each line
	ovf    bool     // povf[k]
}

// Encoder encodes RGB888 frames for one fixed Params, reusing its scratch.
// An Encoder is not safe for concurrent use.
type Encoder struct {
	p       Params
	maxSize int
	src     []byte // frame being encoded, valid only during EncodeInto
	planes  [numPlanes]planeScratch
	wg      sync.WaitGroup
	work    [numPlanes]func() // prebuilt goroutine bodies (no per-call closure alloc)
}

// NewEncoder validates p and allocates all scratch for it.
func NewEncoder(p Params) (*Encoder, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	e := &Encoder{p: p, maxSize: MaxEncodedSize(p)}
	for k := range e.planes {
		ps := &e.planes[k]
		ps.t = make([]int16, p.Width)
		ps.prev = make([]int16, p.Width)
		ps.cur = make([]int16, p.Width)
		ps.ul = make([]uint32, p.Width)
		// The reference gives each plane a dst_cap-sized buffer; dst_cap is
		// nlc_max_encoded_size in the fork client and in tools/nlcvectors.
		ps.segbuf = make([]byte, e.maxSize)
		ps.seglen = make([]uint16, p.Height)
	}
	for k := 1; k < numPlanes; k++ {
		e.work[k] = func() {
			defer e.wg.Done()
			e.encodePlane(k)
		}
	}
	return e, nil
}

// EncodeInto encodes one RGB888 frame src (len == FrameBytes) into dst and
// returns the encoded length. dst must have len >= MaxEncodedSize.
//
// It mirrors nlc_encode: planes 1 and 2 are encoded on their own goroutines
// while the caller encodes plane 0 (the reference runs std::thread for
// planes 1..np-1 and encode_plane(0) inline), then a serial pass assembles
// the v2 line records. The split leaves every plane's segment bytes
// unchanged, so the output is identical to a serial encode.
func (e *Encoder) EncodeInto(dst, src []byte) (int, error) {
	if len(src) != FrameBytes(e.p) {
		return 0, ErrSrcSize
	}
	if len(dst) < e.maxSize {
		return 0, ErrDstTooSmall
	}
	e.src = src
	e.wg.Add(numPlanes - 1)
	for k := 1; k < numPlanes; k++ {
		go e.work[k]()
	}
	e.encodePlane(0)
	e.wg.Wait()
	e.src = nil
	return e.assemble(dst)
}

// encodePlane mirrors the encode_plane lambda in nlc_encode, fused with the
// matching slice of to_planes: each row's plane-k component is computed just
// before that row is encoded, rather than converting the whole frame first.
// The values are identical; only the order of independent work changes.
func (e *Encoder) encodePlane(k int) {
	ps := &e.planes[k]
	W, H := e.p.Width, e.p.Height
	nearLvl := e.p.Near
	lo, hi := planeRange(k)
	capacity := len(ps.segbuf) // the reference's dst_cap for this plane
	prev, cur := ps.prev, ps.cur
	// calloc'd prev/cur in the reference: row 0 never reads prev (Ra/Rb/Rc
	// all fall back when y == 0) and cur is fully written before being read,
	// but zero them anyway so a reused Encoder starts from the same state.
	clear(prev)
	clear(cur)
	ps.ovf = false
	off := 0
	var bw bitW
	for y := 0; y < H; y++ {
		toPlaneLine(e.src[y*W*bpp:(y+1)*W*bpp], k, ps.t)
		lineEncode(ps.t, prev, cur, ps.ul, y, nearLvl, lo, hi)
		bw.init(ps.segbuf[off:capacity])
		if e.p.Pack == PackTiled {
			packLineTiled(&bw, ps.ul, tileLen, widthBits)
		} else {
			packLineRice(&bw, ps.ul, tileLen, widthBits)
		}
		seg := bw.finish()
		pad := (8 - (seg & 7)) & 7 // word-align the segment
		if bw.ovf || seg+pad > 0xffff || off+seg+pad > capacity {
			ps.ovf = true
			break
		}
		clear(ps.segbuf[off+seg : off+seg+pad])
		seg += pad
		ps.seglen[y] = uint16(seg)
		off += seg
		prev, cur = cur, prev
	}
	ps.prev, ps.cur = prev, cur
}

// assemble is the serial record-assembly pass at the end of nlc_encode:
// per line, an 8-byte header of little-endian u16 padded segment lengths
// (lenP0..lenP2, then lenP3 = 0 because RGB888 has three planes; the
// reference zeroes the header before filling np entries), followed by the
// three segments.
func (e *Encoder) assemble(dst []byte) (int, error) {
	for k := range e.planes {
		if e.planes[k].ovf {
			return 0, ErrOverflow
		}
	}
	dstCap := len(dst)
	csize := 0
	var soff [numPlanes]int
	for y := 0; y < e.p.Height; y++ {
		hdr := csize // 8-byte header slot
		csize += headerBytes
		if csize > dstCap {
			return 0, ErrOverflow
		}
		clear(dst[hdr : hdr+headerBytes])
		for k := range e.planes {
			ps := &e.planes[k]
			seg := int(ps.seglen[y])
			if csize+seg > dstCap {
				return 0, ErrOverflow
			}
			copy(dst[csize:csize+seg], ps.segbuf[soff[k]:soff[k]+seg])
			soff[k] += seg
			dst[hdr+2*k] = uint8(seg & 0xff)
			dst[hdr+2*k+1] = uint8(seg >> 8)
			csize += seg
		}
	}
	return csize, nil
}
