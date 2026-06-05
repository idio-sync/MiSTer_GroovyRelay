package audiodsp

// Slot indices into Coeffs.Slots (fixed chain order).
const (
	SlotSubsonic = iota
	SlotBass
	SlotMid
	SlotTreble
	SlotEQ0 // SlotEQ0+i for band i in 0..9
	_       // EQ1
	_       // EQ2
	_       // EQ3
	_       // EQ4
	_       // EQ5
	_       // EQ6
	_       // EQ7
	_       // EQ8
	_       // EQ9
	SlotLoudLo = SlotEQ0 + 10   // 14
	SlotLoudHi = SlotLoudLo + 1 // 15 (chain order: ...EQ9, loud-lo, loud-hi)
	NumSlots   = SlotLoudHi + 1 // 16
)

// eqCenters are the 10 ISO-octave band centers (Hz), index-aligned to EQ[i].
var eqCenters = [10]float64{31.25, 62.5, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

const (
	subsonicHz = 22.0
	bassHz     = 100.0
	midHz      = 1000.0
	trebleHz   = 10000.0
	loudLoHz   = 100.0
	loudHiHz   = 12000.0
	loudLoDB   = 6.0
	loudHiDB   = 4.0
	eqQ        = 1.4
)

// Params is the plain runtime parameter set the data plane feeds in. Gains
// are dB; Balance is -100..+100; EQ is index-aligned to eqCenters.
type Params struct {
	Enabled    bool
	Mono       bool
	Subsonic   bool
	Loudness   bool
	Bass       float64
	Mid        float64
	Treble     float64
	Balance    int
	EQ         [10]float64
	SampleRate int
	Channels   int
}

// Coeffs is the immutable precomputed chain held behind an atomic.Pointer.
type Coeffs struct {
	Slots       [NumSlots]Biquad
	Mono        bool
	BalL, BalR  float64
	Transparent bool // fully bit-identical to volume-only path (fast path)
	Engaged     bool // status LED: shaping requested (independent of mono/balance)
	Src         Params
}

// Design builds the chain from params, applying Nyquist safety and the
// defeat/transparency/engaged rules.
func Design(p Params) Coeffs {
	c := Coeffs{Src: p}
	for i := range c.Slots {
		c.Slots[i] = Unity()
	}
	fs := float64(p.SampleRate)
	nyquist := fs / 2

	shape := p.Enabled // when disabled (defeat), all frequency slots stay unity
	peakOK := func(hz float64) bool { return hz < nyquist }

	if shape {
		if p.Subsonic && peakOK(subsonicHz) {
			c.Slots[SlotSubsonic] = DesignHighpass(fs, subsonicHz, 0.707)
		}
		if p.Bass != 0 && peakOK(bassHz) {
			c.Slots[SlotBass] = DesignLowShelf(fs, bassHz, 0.707, p.Bass)
		}
		if p.Mid != 0 && peakOK(midHz) {
			c.Slots[SlotMid] = DesignPeaking(fs, midHz, 0.7, p.Mid)
		}
		if p.Treble != 0 && peakOK(trebleHz) {
			c.Slots[SlotTreble] = DesignHighShelf(fs, trebleHz, 0.707, p.Treble)
		}
		for i, g := range p.EQ {
			if g != 0 && peakOK(eqCenters[i]) {
				c.Slots[SlotEQ0+i] = DesignPeaking(fs, eqCenters[i], eqQ, g)
			}
		}
		if p.Loudness {
			if peakOK(loudLoHz) {
				c.Slots[SlotLoudLo] = DesignLowShelf(fs, loudLoHz, 0.707, loudLoDB)
			}
			if peakOK(loudHiHz) {
				c.Slots[SlotLoudHi] = DesignHighShelf(fs, loudHiHz, 0.707, loudHiDB)
			}
		}
	}

	c.Mono = p.Mono && p.Channels == 2
	c.BalL, c.BalR = balanceGains(p)

	c.Engaged = p.Enabled && shapingRequested(p)
	c.Transparent = !c.Mono && c.BalL == 1.0 && c.BalR == 1.0 && !c.Engaged
	return c
}

func shapingRequested(p Params) bool {
	if p.Subsonic || p.Loudness || p.Bass != 0 || p.Mid != 0 || p.Treble != 0 {
		return true
	}
	for _, g := range p.EQ {
		if g != 0 {
			return true
		}
	}
	return false
}

// balanceGains implements the attenuate-only law. On mono or a mono source
// balance is forced to center.
func balanceGains(p Params) (l, r float64) {
	if p.Mono || p.Channels != 2 {
		return 1, 1
	}
	b := p.Balance
	if b < -100 {
		b = -100
	}
	if b > 100 {
		b = 100
	}
	l, r = 1, 1
	if b > 0 {
		l = 1 - float64(b)/100
	} else if b < 0 {
		r = 1 + float64(b)/100
	}
	return l, r
}
