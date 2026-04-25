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
	// Canonical 720x480i NTSC per MiSTerCast/Mistglow modelines.dat:
	// pClock=13.846, hTotal=880, vActive=480, vTotal=525, interlace=1.
	got := BuildSwitchres(NTSC480i60)

	if got[0] != CmdSwitchres {
		t.Fatalf("cmd = %d, want %d", got[0], CmdSwitchres)
	}
	if len(got) != 26 {
		t.Fatalf("SWITCHRES must be 26 bytes, got %d", len(got))
	}
	gotPClock := math.Float64frombits(binary.LittleEndian.Uint64(got[1:9]))
	if gotPClock != 13.846 {
		t.Errorf("pClock = %f, want 13.846", gotPClock)
	}
	if v := binary.LittleEndian.Uint16(got[9:11]); v != 720 {
		t.Errorf("hActive = %d, want 720", v)
	}
	if v := binary.LittleEndian.Uint16(got[11:13]); v != 744 {
		t.Errorf("hBegin = %d, want 744", v)
	}
	if v := binary.LittleEndian.Uint16(got[13:15]); v != 809 {
		t.Errorf("hEnd = %d, want 809", v)
	}
	if v := binary.LittleEndian.Uint16(got[15:17]); v != 880 {
		t.Errorf("hTotal = %d, want 880", v)
	}
	if v := binary.LittleEndian.Uint16(got[17:19]); v != 480 {
		t.Errorf("vActive = %d, want 480", v)
	}
	if v := binary.LittleEndian.Uint16(got[19:21]); v != 488 {
		t.Errorf("vBegin = %d, want 488", v)
	}
	if v := binary.LittleEndian.Uint16(got[21:23]); v != 494 {
		t.Errorf("vEnd = %d, want 494", v)
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

func TestModelineFieldHeight(t *testing.T) {
	if got := NTSC480i60.FieldHeight(); got != 240 {
		t.Errorf("interlaced field height = %d, want 240", got)
	}
	if got := (Modeline{VActive: 240, Interlace: 0}).FieldHeight(); got != 240 {
		t.Errorf("progressive field height = %d, want 240", got)
	}
}

func TestFieldPayloadBytes(t *testing.T) {
	got := FieldPayloadBytes(NTSC480i60.HActive, NTSC480i60.VActive, NTSC480i60.Interlace, 3)
	if got != 720*240*3 {
		t.Errorf("field payload bytes = %d, want %d", got, 720*240*3)
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

func TestBuildClose(t *testing.T) {
	got := BuildClose()
	if len(got) != 1 {
		t.Fatalf("CLOSE must be 1 byte, got %d", len(got))
	}
	if got[0] != CmdClose {
		t.Errorf("cmd = %d, want %d", got[0], CmdClose)
	}
}

func TestBuildBlitHeaderInto_MatchesLegacy(t *testing.T) {
	cases := []struct {
		name string
		opts BlitOpts
	}{
		{"raw", BlitOpts{Frame: 42, Field: 0, VSync: 100}},
		{"dup", BlitOpts{Frame: 42, Field: 1, Duplicate: true}},
		{"lz4", BlitOpts{Frame: 42, Field: 0, Compressed: true, CompressedSize: 12345}},
		{"lz4Delta", BlitOpts{Frame: 42, Field: 1, Compressed: true, Delta: true, CompressedSize: 12345}},
	}
	dst := make([]byte, BlitHeaderLZ4Delta) // 13, the largest variant
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			legacy := BuildBlitHeader(c.opts)
			out := BuildBlitHeaderInto(dst, c.opts)
			if !bytes.Equal(legacy, out) {
				t.Errorf("header mismatch:\n  legacy: % x\n  new:    % x", legacy, out)
			}
			if &out[0] != &dst[0] {
				t.Error("BuildBlitHeaderInto returned a different backing array")
			}
		})
	}
}

func TestBuildBlitHeaderInto_ZeroAllocs(t *testing.T) {
	dst := make([]byte, BlitHeaderLZ4Delta)
	opts := BlitOpts{Frame: 1, Field: 0, Compressed: true, CompressedSize: 1000}
	BuildBlitHeaderInto(dst, opts) // warmup
	got := testing.AllocsPerRun(100, func() {
		BuildBlitHeaderInto(dst, opts)
	})
	if got != 0 {
		t.Errorf("BuildBlitHeaderInto allocs/op = %v, want 0", got)
	}
}
