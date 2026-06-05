package audiodsp

import (
	"encoding/binary"
	"math"
)

const (
	bytesPerSample = 2
	rampSamples    = 480               // ~10 ms at 48 kHz; crossfade window for hard changes
	hardGainDelta  = 1.0               // dB; > this on any slot is a hard change
	balGainStep    = 1.0 / rampSamples // max per-sample balance-gain change (~10 ms full swing)
)

// Processor applies a Coeffs chain to interleaved s16le PCM and owns the
// click-free transition between coefficient generations. Single-goroutine:
// the audio-send goroutine is the only caller.
type Processor struct {
	channels   int
	cur        *Coeffs
	fromCoeffs *Coeffs
	curSt      [][NumSlots]BiquadState // [channel][slot]
	fromSt     [][NumSlots]BiquadState // old cascade during a ramp
	ramp       int                     // samples remaining in the active crossfade (0 = none)
	balL, balR float64                 // smoothed per-channel balance gains (track Coeffs.BalL/BalR)
}

func NewProcessor(channels int) *Processor {
	return &Processor{
		channels: channels,
		curSt:    make([][NumSlots]BiquadState, channels),
		fromSt:   make([][NumSlots]BiquadState, channels),
	}
}

// Active reports whether the processor is doing non-trivial work (shaping,
// mono, balance, or mid-ramp) — i.e. the float path is required. It also stays
// active while the smoothed balance gains are still approaching their target,
// so a balance return-to-center finishes its glide instead of snapping when the
// plane hands off to the bit-identical volume-only fast path.
func (p *Processor) Active() bool {
	if p.cur == nil {
		return false
	}
	if p.ramp > 0 || !p.cur.Transparent {
		return true
	}
	return p.balL != p.cur.BalL || p.balR != p.cur.BalR
}

// Process applies target to pcm in place. target is the latest atomically
// published chain; the processor diffs it against its current generation to
// classify the transition.
func (p *Processor) Process(pcm []byte, target *Coeffs, volume int) {
	if target == nil {
		return
	}
	if p.cur == nil {
		p.adopt(target)
	} else if target != p.cur {
		p.transition(target)
	}
	g := float64(clampVol(volume)) / 100

	frames := len(pcm) / (bytesPerSample * p.channels)
	for n := 0; n < frames; n++ {
		var s [2]float64
		for ch := 0; ch < p.channels; ch++ {
			s[ch] = sampleToFloat(pcm, (n*p.channels+ch)*bytesPerSample)
		}
		if p.channels == 2 && p.cur.Mono {
			m := (s[0] + s[1]) * 0.5
			s[0], s[1] = m, m
		}
		var t float64
		if p.ramp > 0 {
			t = 1 - float64(p.ramp)/float64(rampSamples)
		}
		// Glide the per-channel balance gains toward their target so a balance
		// change (applied outside the biquad cascade) ramps instead of stepping.
		p.balL = approach(p.balL, p.cur.BalL, balGainStep)
		p.balR = approach(p.balR, p.cur.BalR, balGainStep)
		for ch := 0; ch < p.channels; ch++ {
			y := p.cascade(&p.curSt[ch], p.cur, s[ch])
			if p.ramp > 0 {
				yOld := p.cascade(&p.fromSt[ch], p.curFrom(), s[ch])
				y = yOld*(1-t) + y*t
			}
			// balance (smoothed; skip on mono / mono source where BalL=BalR=1)
			if ch == 0 {
				y *= p.balL
			} else {
				y *= p.balR
			}
			y *= g
			floatToSample(pcm, (n*p.channels+ch)*bytesPerSample, y)
		}
		if p.ramp > 0 {
			p.ramp--
		}
	}
}

// curFrom returns the coeffs the from-state cascade should run. During a
// ramp the old cascade keeps running the previous chain; we stash it in
// fromCoeffs.
func (p *Processor) curFrom() *Coeffs { return p.fromCoeffs }

func (p *Processor) cascade(st *[NumSlots]BiquadState, c *Coeffs, x float64) float64 {
	for i := range st {
		st[i].SetCoeffs(c.Slots[i])
		x = st[i].Process(x)
	}
	return x
}

func (p *Processor) adopt(c *Coeffs) {
	p.cur = c
	p.ramp = 0
	p.balL, p.balR = c.BalL, c.BalR // start settled — no glide on the first chunk
	for ch := 0; ch < p.channels; ch++ {
		for i := range p.curSt[ch] {
			p.curSt[ch][i].SetCoeffs(c.Slots[i])
		}
	}
}

func (p *Processor) transition(next *Coeffs) {
	if p.isIncremental(p.cur, next) {
		// keep state, just swap target coeffs
		p.cur = next
		p.ramp = 0
		return
	}
	// Hard change: seed from-state from the current state, start a ramp.
	//
	// Limitation: a *supersede* (a second hard change while this ramp is still
	// in flight) reseeds "from" to the in-flight target rather than the current
	// crossfaded output, so the spec's ideal "ramp from the current effective
	// output" is approximated, not exact. This is benign in practice: every UI
	// param change is gated by audio-strip.js's 120 ms preview throttle (and a
	// trailing commit), while a ramp is only ~10 ms — so consecutive hard
	// changes are always >10x the ramp apart and never overlap. A fully exact
	// bound across overlapping ramps would need an output-residual crossfade;
	// deferred as unwarranted for the realistic single-transition path.
	p.fromCoeffs = p.cur
	for ch := 0; ch < p.channels; ch++ {
		p.fromSt[ch] = p.curSt[ch] // value copy of the old per-slot state
	}
	p.cur = next
	p.ramp = rampSamples
}

// approach moves cur toward target by at most step, without overshoot.
func approach(cur, target, step float64) float64 {
	if cur < target {
		if cur+step >= target {
			return target
		}
		return cur + step
	}
	if cur > target {
		if cur-step <= target {
			return target
		}
		return cur - step
	}
	return cur
}

func (p *Processor) isIncremental(a, b *Coeffs) bool {
	pa, pb := a.Src, b.Src
	if pa.Enabled != pb.Enabled || pa.Mono != pb.Mono ||
		pa.Subsonic != pb.Subsonic || pa.Loudness != pb.Loudness {
		return false
	}
	if (pa.Balance == 0) != (pb.Balance == 0) {
		return false
	}
	if math.Abs(pa.Bass-pb.Bass) > hardGainDelta ||
		math.Abs(pa.Mid-pb.Mid) > hardGainDelta ||
		math.Abs(pa.Treble-pb.Treble) > hardGainDelta {
		return false
	}
	for i := range pa.EQ {
		if math.Abs(pa.EQ[i]-pb.EQ[i]) > hardGainDelta {
			return false
		}
	}
	return true
}

func sampleToFloat(pcm []byte, off int) float64 {
	return float64(int16(binary.LittleEndian.Uint16(pcm[off:off+2]))) / 32768
}

func floatToSample(pcm []byte, off int, v float64) {
	s := v * 32768
	if s > 32767 {
		s = 32767
	} else if s < -32768 {
		s = -32768
	}
	binary.LittleEndian.PutUint16(pcm[off:off+2], uint16(int16(s)))
}

func clampVol(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
