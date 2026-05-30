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

func validSectionedWithDSP(d AudioDSP) *Sectioned {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.Audio.DSP = d
	return s
}

func TestValidateAudioDSP_Bounds(t *testing.T) {
	t.Parallel()
	ok := DefaultAudioDSP()
	if err := ValidateAudioDSP(ok); err != nil {
		t.Fatalf("flat default should validate: %v", err)
	}
	bad := DefaultAudioDSP()
	bad.Bass = 13
	if err := ValidateAudioDSP(bad); err == nil {
		t.Error("bass +13 dB should be rejected")
	}
	balBad := DefaultAudioDSP()
	balBad.Balance = 101
	if err := ValidateAudioDSP(balBad); err == nil {
		t.Error("balance 101 should be rejected")
	}
	eqBad := DefaultAudioDSP()
	eqBad.EQ = []float64{0, 0, 0} // wrong length
	if err := ValidateAudioDSP(eqBad); err == nil {
		t.Error("EQ length != 10 should be rejected")
	}
	eqRange := DefaultAudioDSP()
	eqRange.EQ[2] = -13
	if err := ValidateAudioDSP(eqRange); err == nil {
		t.Error("EQ band -13 dB should be rejected")
	}
}

func TestValidateAudioDSP_Memory(t *testing.T) {
	t.Parallel()
	d := DefaultAudioDSP()
	d.Memory = []AudioDSPMemory{
		{Slot: 1, Name: "M1", Stored: true, EQ: make([]float64, 10)},
		{Slot: 1, Name: "dup", Stored: true, EQ: make([]float64, 10)},
	}
	if err := ValidateAudioDSP(d); err == nil {
		t.Error("duplicate memory slot should be rejected")
	}
	d2 := DefaultAudioDSP()
	d2.Memory = []AudioDSPMemory{{Slot: 9, Stored: true, EQ: make([]float64, 10)}}
	if err := ValidateAudioDSP(d2); err == nil {
		t.Error("memory slot out of 1..3 should be rejected")
	}
}

func TestSectionedValidate_RejectsBadDSP(t *testing.T) {
	t.Parallel()
	s := validSectionedWithDSP(DefaultAudioDSP())
	s.Bridge.Audio.DSP.Treble = 99
	if err := s.Validate(); err == nil {
		t.Error("Sectioned.Validate should reject bad DSP")
	}
}

func TestNormalize_PadsEQToTen(t *testing.T) {
	t.Parallel()
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.Audio.DSP.EQ = nil // simulate omitted eq
	s.Bridge.Audio.DSP.Memory = []AudioDSPMemory{{Slot: 1, Stored: true, EQ: nil}}
	if err := normalizeSectionedRuntimeDefaults(s); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(s.Bridge.Audio.DSP.EQ) != 10 {
		t.Errorf("top-level EQ not padded: len=%d", len(s.Bridge.Audio.DSP.EQ))
	}
	if len(s.Bridge.Audio.DSP.Memory[0].EQ) != 10 {
		t.Errorf("memory EQ not padded: len=%d", len(s.Bridge.Audio.DSP.Memory[0].EQ))
	}
}
