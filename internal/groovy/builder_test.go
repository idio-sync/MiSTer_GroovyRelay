package groovy

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestBuildInit_StereoLZ448kRGB888(t *testing.T) {
	got := BuildInit(LZ4ModeDefault, AudioRate48000, 2, RGBMode888)
	want := []byte{CmdInit, LZ4ModeDefault, AudioRate48000, 2, RGBMode888}
	if !bytes.Equal(got, want) {
		t.Errorf("INIT bytes = %v, want %v", got, want)
	}
}

func TestBuildInit_RawNoAudioRGB565(t *testing.T) {
	got := BuildInit(LZ4ModeOff, AudioRateOff, 0, RGBMode565)
	want := []byte{CmdInit, LZ4ModeOff, AudioRateOff, 0, RGBMode565}
	if !bytes.Equal(got, want) {
		t.Errorf("INIT bytes = %v, want %v", got, want)
	}
}

func TestBuildInit_PanicsOnInvalidRGBMode(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on unknown rgb mode")
		}
	}()
	BuildInit(LZ4ModeDefault, AudioRate48000, 2, 99)
}

func TestBuildInit_PanicsOnInvalidSoundRate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on unknown sound rate")
		}
	}()
	BuildInit(LZ4ModeDefault, 99, 2, RGBMode888)
}

func TestBuildSwitchres_NTSC480i60_Canonical(t *testing.T) {
	// Canonical NTSC 480i60 per mistercast.md:138:
	// pClock=13.5, hTotal=858, vTotal=525, interlace=1.
	got := BuildSwitchres(NTSC480i60)

	if got[0] != CmdSwitchres {
		t.Fatalf("cmd = %d, want %d", got[0], CmdSwitchres)
	}
	if len(got) != 26 {
		t.Fatalf("SWITCHRES must be 26 bytes, got %d", len(got))
	}
	gotPClock := math.Float64frombits(binary.LittleEndian.Uint64(got[1:9]))
	if gotPClock != 13.5 {
		t.Errorf("pClock = %f, want 13.5", gotPClock)
	}
	if v := binary.LittleEndian.Uint16(got[9:11]); v != 720 {
		t.Errorf("hActive = %d, want 720", v)
	}
	if v := binary.LittleEndian.Uint16(got[15:17]); v != 858 {
		t.Errorf("hTotal = %d, want 858", v)
	}
	if v := binary.LittleEndian.Uint16(got[17:19]); v != 240 {
		t.Errorf("vActive (per field) = %d, want 240", v)
	}
	if v := binary.LittleEndian.Uint16(got[23:25]); v != 525 {
		t.Errorf("vTotal = %d, want 525", v)
	}
	if got[25] != 1 {
		t.Errorf("interlace = %d, want 1", got[25])
	}
}

func TestBuildSwitchres_Progressive(t *testing.T) {
	ml := Modeline{PClock: 27.0, HActive: 720, HBegin: 736, HEnd: 798,
		HTotal: 858, VActive: 480, VBegin: 483, VEnd: 486, VTotal: 525, Interlace: 0}
	got := BuildSwitchres(ml)
	if got[25] != 0 {
		t.Error("interlace flag should be 0 for progressive modeline")
	}
}
