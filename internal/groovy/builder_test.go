package groovy

import (
	"bytes"
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
