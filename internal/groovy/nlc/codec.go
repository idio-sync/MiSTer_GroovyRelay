// Ported from the GroovyNLC reference codec (api/nlc_codec.cpp) of
// https://github.com/verbst/Groovy_MiSTer at commit e60f52a (release v1.4).
// Original work GPL-2.0-or-later; this port is distributed under this
// project's GPL-3.0 as permitted by that license.

package nlc

import "math/bits"

// Fixed parameters, as built by the fork client's buildNlcParams. They are
// implicit on the wire (the FPGA hard-codes them), so they are not knobs.
const (
	nlcRGB888     = 0  // NLC_RGB888: 3 bytes/pixel, R,G,B
	nlcColorYCoCg = 1  // NLC_COLOR_YCOCG
	tileLen       = 16 // nlc_params.tile
	widthBits     = 4  // nlc_params.width_bits
	riceK         = -1 // nlc_params.rice_k: adaptive per tile

	numPlanes = 3 // plane_count(NLC_RGB888)
	bpp       = 3 // bytes per pixel for NLC_RGB888

	// headerBytes is the per-line record header: four little-endian u16
	// padded segment lengths (the fourth is lenP3, always 0 for RGB888).
	headerBytes = 8

	riceLimit = 20 // NLC_RICE_LIMIT: max unary run before escape
	riceUBits = 12 // NLC_RICE_UBITS: escape payload width
)

// iclamp mirrors iclamp.
func iclamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// zz mirrors zz: zigzag map signed -> unsigned (0,-1,1,-2,2,... -> 0,1,2,3,4,...).
func zz(v int) uint32 {
	if v >= 0 {
		return uint32(v << 1)
	}
	return uint32(((-v) << 1) - 1)
}

// unzz mirrors unzz. The (u + 1) is computed in uint32, wrapping as in C.
func unzz(u uint32) int {
	if u&1 != 0 {
		return -int((u + 1) >> 1)
	}
	return int(u >> 1)
}

// bitlen mirrors bitlen: bitlen(0)=0, bitlen(1)=1, bitlen(2..3)=2, ...
func bitlen(u uint32) int { return bits.Len32(u) }

// nlQuant mirrors nl_quant: the JPEG-LS near-lossless residual quantiser.
// Division truncates toward zero in both C and Go; the dividend is
// non-negative here anyway. Constant divisors let the compiler use a
// multiply-by-reciprocal, as the reference comment intends.
func nlQuant(e, nearLvl int) int {
	var a, q int
	if e > 0 {
		a = e
	} else {
		a = -e
	}
	switch nearLvl {
	case 0:
		return e
	case 1:
		q = (a + 1) / 3
	case 2:
		q = (a + 2) / 5
	case 3:
		q = (a + 3) / 7
	default:
		q = (a + nearLvl) / (2*nearLvl + 1)
	}
	if e > 0 {
		return q
	}
	return -q
}

// nlDequant mirrors nl_dequant.
func nlDequant(qe, nearLvl int) int { return qe * (2*nearLvl + 1) }

// rgbToYCoCg mirrors nlc_rgb_to_ycocg: reversible YCoCg-R lifting. Go's >>
// on a signed int is an arithmetic (floor) shift, matching the reference.
func rgbToYCoCg(r, g, b int) (y, co, cg int) {
	co = r - b
	t := b + (co >> 1)
	cg = g - t
	y = t + (cg >> 1)
	return y, co, cg
}

// ycocgToRGB mirrors nlc_ycocg_to_rgb.
func ycocgToRGB(y, co, cg int) (r, g, b int) {
	t := y - (cg >> 1)
	g = cg + t
	b = t - (co >> 1)
	r = b + co
	return r, g, b
}

// medPredict mirrors nlc_med_predict: the LOCO-I / JPEG-LS median edge
// predictor from a=left, b=above, c=above-left.
func medPredict(a, b, c int) int {
	mn, mx := a, b
	if !(a < b) {
		mn, mx = b, a
	}
	if c >= mx {
		return mn
	}
	if c <= mn {
		return mx
	}
	return a + b - c
}

// ---------------------------------------------------------------------------
// LSB-first bit I/O (the wire/RTL bit-order spec)
// ---------------------------------------------------------------------------

// bitW mirrors struct BitW. Bits are packed LSB-first; a value's low bit is
// written first. n counts bytes emitted even past cap, and ovf records any
// byte that did not fit, exactly as the reference does.
type bitW struct {
	buf   []byte // len(buf) is the reference's cap
	n     int
	acc   uint64
	nbits uint
	ovf   bool
}

// init mirrors BitW::init.
func (w *bitW) init(b []byte) {
	w.buf = b
	w.n = 0
	w.acc = 0
	w.nbits = 0
	w.ovf = false
}

// put mirrors BitW::put. nb must be in [0,24].
//
// Bytes are flushed four at a time once 32 bits are pending. That only
// changes when bytes reach buf, not which bytes: the stream is fully
// determined by the bit sequence, and finish flushes the remainder.
func (w *bitW) put(val uint32, nb int) {
	if nb <= 0 {
		return
	}
	mask := uint32(1)<<uint(nb) - 1
	w.acc |= uint64(val&mask) << w.nbits
	w.nbits += uint(nb)
	if w.nbits >= 32 {
		if w.n+4 <= len(w.buf) {
			b := w.buf[w.n : w.n+4 : w.n+4]
			b[0] = byte(w.acc)
			b[1] = byte(w.acc >> 8)
			b[2] = byte(w.acc >> 16)
			b[3] = byte(w.acc >> 24)
			w.n += 4
			w.acc >>= 32
			w.nbits -= 32
		} else {
			w.flushBytes()
		}
	}
}

// flushBytes is the reference's byte-at-a-time flush loop in BitW::put.
func (w *bitW) flushBytes() {
	for w.nbits >= 8 {
		if w.n < len(w.buf) {
			w.buf[w.n] = byte(w.acc)
		} else {
			w.ovf = true
		}
		w.n++
		w.acc >>= 8
		w.nbits -= 8
	}
}

// finish mirrors BitW::finish: flush whole bytes, then the final partial
// byte (upper bits zero). Returns the byte count.
func (w *bitW) finish() int {
	w.flushBytes()
	if w.nbits > 0 {
		if w.n < len(w.buf) {
			w.buf[w.n] = byte(w.acc)
		} else {
			w.ovf = true
		}
		w.n++
		w.acc = 0
		w.nbits = 0
	}
	return w.n
}

// bitR mirrors struct BitR. Reading past the segment yields zero bits, as in
// the reference; overrun additionally records that it happened so Decode can
// reject the stream. A well-formed segment is never overrun: bytes are only
// loaded when at least one of their bits is consumed, and the writer emitted
// every byte holding a written bit.
type bitR struct {
	buf     []byte // len(buf) is the reference's cap
	n       int
	acc     uint64
	nbits   uint
	overrun bool
}

// init mirrors BitR::init.
func (r *bitR) init(b []byte) {
	r.buf = b
	r.n = 0
	r.acc = 0
	r.nbits = 0
	r.overrun = false
}

// get mirrors BitR::get. nb must be in [0,24].
func (r *bitR) get(nb int) uint32 {
	if nb <= 0 {
		return 0
	}
	for r.nbits < uint(nb) {
		var b uint64
		if r.n < len(r.buf) {
			b = uint64(r.buf[r.n])
		} else {
			r.overrun = true
		}
		r.n++
		r.acc |= b << r.nbits
		r.nbits += 8
	}
	mask := uint32(1)<<uint(nb) - 1
	v := uint32(r.acc) & mask
	r.acc >>= uint(nb)
	r.nbits -= uint(nb)
	return v
}

// ---------------------------------------------------------------------------
// Per-line closed-loop DPCM (MED predict + NEAR quantise, reconstruction-exact)
//
// neighbour rule (must match RTL): prev = previous reconstructed row of this
// plane, cur = the row being reconstructed.
//
//	Ra(left)      = x>0    ? cur[x-1] : (y>0 ? prev[x] : 0)
//	Rb(above)     = y>0    ? prev[x]  : Ra
//	Rc(aboveleft) = (x&&y) ? prev[x-1]: Rb
// ---------------------------------------------------------------------------

// lineEncode mirrors line_encode. t is the source row of one plane; cur
// receives the reconstruction (stored as int16 like the reference; rv is
// already clamped to [lo,hi] so the conversion never truncates) and u the
// zigzagged quantised residuals.
func lineEncode(t, prev, cur []int16, u []uint32, y, nearLvl, lo, hi int) {
	w := len(cur)
	t = t[:w]
	prev = prev[:w]
	u = u[:w]
	for x := 0; x < w; x++ {
		var ra, rb, rc int
		if x > 0 {
			ra = int(cur[x-1])
		} else if y > 0 {
			ra = int(prev[x])
		}
		if y > 0 {
			rb = int(prev[x])
		} else {
			rb = ra
		}
		if x > 0 && y > 0 {
			rc = int(prev[x-1])
		} else {
			rc = rb
		}
		pred := medPredict(ra, rb, rc)
		e := int(t[x]) - pred
		qe := nlQuant(e, nearLvl)
		rv := iclamp(pred+nlDequant(qe, nearLvl), lo, hi)
		cur[x] = int16(rv)
		u[x] = zz(qe)
	}
}

// lineDecode mirrors line_decode.
func lineDecode(u []uint32, prev, cur []int16, y, nearLvl, lo, hi int) {
	w := len(cur)
	prev = prev[:w]
	u = u[:w]
	for x := 0; x < w; x++ {
		var ra, rb, rc int
		if x > 0 {
			ra = int(cur[x-1])
		} else if y > 0 {
			ra = int(prev[x])
		}
		if y > 0 {
			rb = int(prev[x])
		} else {
			rb = ra
		}
		if x > 0 && y > 0 {
			rc = int(prev[x-1])
		} else {
			rc = rb
		}
		pred := medPredict(ra, rb, rc)
		qe := unzz(u[x])
		rv := iclamp(pred+nlDequant(qe, nearLvl), lo, hi)
		cur[x] = int16(rv)
	}
}

// ---------------------------------------------------------------------------
// Front-end packers, one scanline of residuals at a time.
// ---------------------------------------------------------------------------

// packLineTiled mirrors pack_line_tiled: per 1-D tile, a wbits-wide width w
// followed by each residual in w bits.
func packLineTiled(bw *bitW, u []uint32, tile, wbits int) {
	W := len(u)
	for x0 := 0; x0 < W; x0 += tile {
		T := tile
		if x0+tile > W {
			T = W - x0
		}
		seg := u[x0 : x0+T]
		w := 0
		for _, v := range seg {
			if b := bitlen(v); b > w {
				w = b
			}
		}
		bw.put(uint32(w), wbits)
		for _, v := range seg {
			bw.put(v, w)
		}
	}
}

// unpackLineTiled mirrors unpack_line_tiled.
func unpackLineTiled(br *bitR, u []uint32, tile, wbits int) {
	W := len(u)
	for x0 := 0; x0 < W; x0 += tile {
		T := tile
		if x0+tile > W {
			T = W - x0
		}
		w := int(br.get(wbits))
		for i := 0; i < T; i++ {
			u[x0+i] = br.get(w)
		}
	}
}

// riceKForTile mirrors rice_k_for_tile: JPEG-LS style, the smallest k with
// (T << k) >= sum(u), capped at 15. The sum is 64-bit as in the reference.
func riceKForTile(u []uint32) int {
	T := uint64(len(u))
	var sum uint64
	for _, v := range u {
		sum += uint64(v)
	}
	k := 0
	for (T<<uint(k)) < sum && k < 15 {
		k++
	}
	return k
}

// ricePut mirrors rice_put: unary(q = u>>k) as the value 1<<q in q+1 bits,
// then k remainder bits; q >= NLC_RICE_LIMIT escapes to LIMIT zeros, a stop
// bit and u in NLC_RICE_UBITS bits (truncated to that width, as put masks).
func ricePut(bw *bitW, u uint32, k int) {
	q := u >> uint(k)
	if q < riceLimit {
		bw.put(uint32(1)<<q, int(q)+1)
		if k > 0 {
			bw.put(u&(uint32(1)<<uint(k)-1), k)
		}
	} else {
		bw.put(uint32(1)<<riceLimit, riceLimit+1)
		bw.put(u, riceUBits)
	}
}

// riceGet mirrors rice_get.
func riceGet(br *bitR, k int) uint32 {
	zeros := 0
	for zeros < riceLimit && br.get(1) == 0 {
		zeros++
	}
	if zeros < riceLimit {
		var r uint32
		if k > 0 {
			r = br.get(k)
		}
		return uint32(zeros)<<uint(k) | r
	}
	br.get(1) // consume the escape stop bit
	return br.get(riceUBits)
}

// packLineRice mirrors pack_line_rice with fixed_k = rice_k = -1 (adaptive
// per tile), the only mode the bridge uses.
func packLineRice(bw *bitW, u []uint32, tile, wbits int) {
	W := len(u)
	for x0 := 0; x0 < W; x0 += tile {
		T := tile
		if x0+tile > W {
			T = W - x0
		}
		seg := u[x0 : x0+T]
		k := riceKForTile(seg)
		bw.put(uint32(k), wbits)
		for _, v := range seg {
			ricePut(bw, v, k)
		}
	}
}

// unpackLineRice mirrors unpack_line_rice.
func unpackLineRice(br *bitR, u []uint32, tile, wbits int) {
	W := len(u)
	for x0 := 0; x0 < W; x0 += tile {
		T := tile
		if x0+tile > W {
			T = W - x0
		}
		k := int(br.get(wbits))
		for i := 0; i < T; i++ {
			u[x0+i] = riceGet(br, k)
		}
	}
}

// ---------------------------------------------------------------------------
// Plane layout
// ---------------------------------------------------------------------------

// planeRange mirrors plane_range for NLC_COLOR_YCOCG: plane 0 = Y in
// [0,255]; planes 1,2 = Co,Cg in [-255,255].
func planeRange(p int) (lo, hi int) {
	if p == 0 {
		return 0, 255
	}
	return -255, 255
}

// toPlaneLine mirrors to_planes for NLC_RGB888 + NLC_COLOR_YCOCG, restricted
// to one plane k of one row: it writes that plane's component of each pixel
// of the interleaved RGB row src into t. Values fit int16 (Y in [0,255],
// Co/Cg in [-255,255]), so the reference's (int16_t) store never truncates.
func toPlaneLine(src []byte, k int, t []int16) {
	w := len(t)
	src = src[:w*bpp]
	switch k {
	case 0:
		for x := 0; x < w; x++ {
			yv, _, _ := rgbToYCoCg(int(src[x*bpp]), int(src[x*bpp+1]), int(src[x*bpp+2]))
			t[x] = int16(yv)
		}
	case 1:
		for x := 0; x < w; x++ {
			_, co, _ := rgbToYCoCg(int(src[x*bpp]), int(src[x*bpp+1]), int(src[x*bpp+2]))
			t[x] = int16(co)
		}
	default:
		for x := 0; x < w; x++ {
			_, _, cg := rgbToYCoCg(int(src[x*bpp]), int(src[x*bpp+1]), int(src[x*bpp+2]))
			t[x] = int16(cg)
		}
	}
}

// combineLine mirrors combine_line for NLC_RGB888 + NLC_COLOR_YCOCG: the
// inverse colour transform of one reconstructed scanline into interleaved
// RGB, each channel clamped to [0,255] before the uint8 store.
func combineLine(cur *[numPlanes][]int16, outrow []byte) {
	yp, cop, cgp := cur[0], cur[1], cur[2]
	w := len(yp)
	cop = cop[:w]
	cgp = cgp[:w]
	outrow = outrow[:w*bpp]
	for x := 0; x < w; x++ {
		r, g, b := ycocgToRGB(int(yp[x]), int(cop[x]), int(cgp[x]))
		outrow[x*bpp+0] = uint8(iclamp(r, 0, 255))
		outrow[x*bpp+1] = uint8(iclamp(g, 0, 255))
		outrow[x*bpp+2] = uint8(iclamp(b, 0, 255))
	}
}
