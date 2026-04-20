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

func TestBuildAudioHeader(t *testing.T) {
	got := BuildAudioHeader(3200)
	if len(got) != AudioHeaderSize {
		t.Fatalf("len = %d, want %d", len(got), AudioHeaderSize)
	}
	if got[0] != CmdAudio {
		t.Errorf("cmd = %d, want %d", got[0], CmdAudio)
	}
	if v := binary.LittleEndian.Uint16(got[1:3]); v != 3200 {
		t.Errorf("soundSize = %d, want 3200", v)
	}
}

func TestBuildAudioHeader_ZeroOK(t *testing.T) {
	// Zero-length audio is valid (no-op between blits when audio enabled but
	// no samples produced this tick).
	got := BuildAudioHeader(0)
	if got[0] != CmdAudio || got[1] != 0 || got[2] != 0 {
		t.Errorf("zero-size audio header = %v", got)
	}
}

func TestBuildBlitHeader_RawFull(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 42, Field: 1, VSync: 0})
	if h[0] != CmdBlitFieldVSync {
		t.Fatalf("cmd = %d, want %d", h[0], CmdBlitFieldVSync)
	}
	if len(h) != BlitHeaderRaw {
		t.Errorf("raw header len = %d, want %d", len(h), BlitHeaderRaw)
	}
	if v := binary.LittleEndian.Uint32(h[1:5]); v != 42 {
		t.Errorf("frame = %d, want 42", v)
	}
	if h[5] != 1 {
		t.Errorf("field = %d, want 1", h[5])
	}
	if v := binary.LittleEndian.Uint16(h[6:8]); v != 0 {
		t.Errorf("vSync = %d, want 0", v)
	}
}

func TestBuildBlitHeader_Duplicate(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 43, Field: 0, Duplicate: true})
	if len(h) != BlitHeaderRawDup {
		t.Fatalf("dup header len = %d, want %d", len(h), BlitHeaderRawDup)
	}
	if h[8] != BlitFlagDup {
		t.Errorf("dup marker = 0x%x, want 0x%x", h[8], BlitFlagDup)
	}
}

func TestBuildBlitHeader_LZ4Full(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 44, Field: 0, CompressedSize: 120000, Compressed: true})
	if len(h) != BlitHeaderLZ4 {
		t.Fatalf("lz4 header len = %d, want %d", len(h), BlitHeaderLZ4)
	}
	if v := binary.LittleEndian.Uint32(h[8:12]); v != 120000 {
		t.Errorf("cSize = %d, want 120000", v)
	}
}

func TestBuildBlitHeader_LZ4Delta(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 45, Field: 1, CompressedSize: 90000, Compressed: true, Delta: true})
	if len(h) != BlitHeaderLZ4Delta {
		t.Fatalf("lz4 delta header len = %d, want %d", len(h), BlitHeaderLZ4Delta)
	}
	if h[12] != BlitFlagDelta {
		t.Errorf("delta marker at [12] = 0x%x, want 0x%x", h[12], BlitFlagDelta)
	}
}
