package audiodsp

import (
	"encoding/binary"
	"math"
)

const (
	bytesPerSample = 2
	rampSamples    = 480 // ~10 ms at 48 kHz; crossfade window for hard changes
	hardGainDelta  = 1.0 // dB; > this on any slot is a hard change
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
}

func NewProcessor(channels int) *Processor {
	return &Processor{
		channels: channels,
		curSt:    make([][NumSlots]BiquadState, channels),
		fromSt:   make([][NumSlots]BiquadState, channels),
	}
}

// Active reports whether the processor is doing non-trivial work (shaping,
// mono, balance, or mid-ramp) — i.e. the float path is required.
func (p *Processor) Active() bool {
	return p.ramp > 0 || (p.cur != nil && !p.cur.Transparent)
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
		for ch := 0; ch < p.channels; ch++ {
			y := p.cascade(&p.curSt[ch], p.cur, s[ch])
			if p.ramp > 0 {
				yOld := p.cascade(&p.fromSt[ch], p.curFrom(), s[ch])
				y = yOld*(1-t) + y*t
			}
			// balance (skip on mono / mono source)
			if ch == 0 {
				y *= p.cur.BalL
			} else {
				y *= p.cur.BalR
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
	// hard change: seed from-state from current state, start a ramp.
	p.fromCoeffs = p.cur
	for ch := 0; ch < p.channels; ch++ {
		p.fromSt[ch] = p.curSt[ch] // value copy of the old per-slot state
	}
	p.cur = next
	p.ramp = rampSamples
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
