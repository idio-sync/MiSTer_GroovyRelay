package config

import "testing"

func TestDefaultAudioDSP_TransparentButEnabled(t *testing.T) {
	t.Parallel()
	d := DefaultAudioDSP()
	if !d.Enabled {
		t.Error("default DSP should be Enabled (defeat off)")
	}
	if len(d.EQ) != 10 {
		t.Fatalf("default EQ length = %d, want 10", len(d.EQ))
	}
	if d.Engaged() {
		t.Error("flat default DSP should not be Engaged")
	}
}

func TestDefaultBridge_SeedsTransparentDSP(t *testing.T) {
	t.Parallel()
	b := defaultBridge()
	if !b.Audio.DSP.Enabled || len(b.Audio.DSP.EQ) != 10 {
		t.Errorf("defaultBridge DSP = %+v, want enabled + 10-band EQ", b.Audio.DSP)
	}
}

func TestAudioDSP_Engaged(t *testing.T) {
	t.Parallel()
	base := DefaultAudioDSP()
	if base.Engaged() {
		t.Error("flat not engaged")
	}
	b := base
	b.Bass = 3
	if !b.Engaged() {
		t.Error("bass != 0 should engage")
	}
	d := base
	d.Enabled = false
	d.Bass = 3
	if d.Engaged() {
		t.Error("defeated DSP must not engage even with shaping set")
	}
	e := base
	e.EQ[4] = -2
	if !e.Engaged() {
		t.Error("eq band != 0 should engage")
	}
}
