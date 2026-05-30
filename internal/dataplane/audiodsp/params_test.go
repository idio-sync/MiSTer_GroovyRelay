package audiodsp

import "testing"

func flatStereo() Params {
	return Params{Enabled: true, SampleRate: 48000, Channels: 2}
}

func TestDesign_FlatIsTransparent(t *testing.T) {
	t.Parallel()
	c := Design(flatStereo())
	if !c.Transparent {
		t.Error("flat enabled DSP should be Transparent")
	}
	if c.Engaged {
		t.Error("flat DSP should not be Engaged")
	}
	for i, s := range c.Slots {
		if s != Unity() {
			t.Errorf("slot %d = %+v, want Unity", i, s)
		}
	}
}

func TestDesign_BassEngagesAndShapes(t *testing.T) {
	t.Parallel()
	p := flatStereo()
	p.Bass = 6
	c := Design(p)
	if c.Transparent {
		t.Error("bass +6 should not be Transparent")
	}
	if !c.Engaged {
		t.Error("bass +6 should be Engaged")
	}
	if c.Slots[SlotBass] == Unity() {
		t.Error("bass slot should be shaped")
	}
}

func TestDesign_DefeatBypassesShapingButKeepsBalance(t *testing.T) {
	t.Parallel()
	p := flatStereo()
	p.Enabled = false // defeat
	p.Bass = 6
	p.Balance = -100
	c := Design(p)
	if c.Engaged {
		t.Error("defeated DSP must not be Engaged")
	}
	if c.Slots[SlotBass] != Unity() {
		t.Error("defeat must make the bass slot unity")
	}
	if c.BalL != 1.0 || c.BalR != 0.0 {
		t.Errorf("balance still applies under defeat: L=%v R=%v want 1,0", c.BalL, c.BalR)
	}
	if c.Transparent {
		t.Error("defeat with balance off-center is not transparent")
	}
}

func TestDesign_NyquistSlotsGoUnity(t *testing.T) {
	t.Parallel()
	p := Params{Enabled: true, SampleRate: 22050, Channels: 2, Loudness: true}
	p.EQ[9] = 6 // 16 kHz band, above Nyquist (11025)
	c := Design(p)
	if c.Slots[SlotEQ0+9] != Unity() {
		t.Error("16 kHz EQ band must be unity at 22050 Hz")
	}
	if c.Slots[SlotLoudHi] != Unity() {
		t.Error("12 kHz loudness shelf must be unity at 22050 Hz")
	}
	// 8 kHz band (index 8) is below Nyquist and may be shaped if requested.
	// LED still reflects the requested intent.
	if !c.Engaged {
		t.Error("requested loudness + EQ should light the LED even if a slot is Nyquist-clamped")
	}
}

func TestDesign_BalanceAttenuateOnly(t *testing.T) {
	t.Parallel()
	cases := []struct{ bal int; l, r float64 }{
		{0, 1, 1}, {-100, 1, 0}, {100, 0, 1}, {-50, 1, 0.5}, {50, 0.5, 1},
	}
	for _, tc := range cases {
		p := flatStereo()
		p.Balance = tc.bal
		c := Design(p)
		if c.BalL != tc.l || c.BalR != tc.r {
			t.Errorf("balance %d: L=%v R=%v, want %v,%v", tc.bal, c.BalL, c.BalR, tc.l, tc.r)
		}
	}
}
