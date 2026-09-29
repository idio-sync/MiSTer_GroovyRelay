package groovy

import "testing"

func TestCoreFromVersion(t *testing.T) {
	cases := []struct {
		v    byte
		want Core
	}{
		{0, CoreGroovy}, // no reply
		{1, CoreGroovy},
		{2, CoreGroovyNLC},
		{3, CoreGroovyNLC},
	}
	for _, c := range cases {
		if got := CoreFromVersion(c.v); got != c.want {
			t.Errorf("CoreFromVersion(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestCoreString(t *testing.T) {
	if CoreGroovy.String() != "groovy" || CoreGroovyNLC.String() != "groovynlc" || CoreUnknown.String() != "unknown" {
		t.Fatalf("unexpected names: %q %q %q", CoreGroovy, CoreGroovyNLC, CoreUnknown)
	}
}

func TestQueryBuilders(t *testing.T) {
	if got := BuildGetVersion(); len(got) != 1 || got[0] != CmdGetVersion {
		t.Fatalf("BuildGetVersion = %v", got)
	}
	if got := BuildGetStatus(); len(got) != 1 || got[0] != CmdGetStatus {
		t.Fatalf("BuildGetStatus = %v", got)
	}
}

func TestNLCCompressionByte(t *testing.T) {
	// 2 | near<<2 | 1<<4 | 2<<5 | rice<<7 (design §4.3):
	//   (0,false) = 0x02|0x10|0x40      = 0x52
	//   (0,true)  = 0x52|0x80           = 0xD2
	//   (3,false) = 0x52|0x0C           = 0x5E
	//   (2,true)  = 0x52|0x08|0x80      = 0xDA
	cases := []struct {
		near int
		rice bool
		want byte
	}{
		{0, false, 0x52},
		{0, true, 0xD2},
		{3, false, 0x5E},
		{2, true, 0xDA},
	}
	for _, c := range cases {
		if got := NLCCompressionByte(c.near, c.rice); got != c.want {
			t.Errorf("NLCCompressionByte(%d, %v) = %#x, want %#x", c.near, c.rice, got, c.want)
		}
	}
}

func TestBuildInit_AcceptsNLCCompressionByte(t *testing.T) {
	b := NLCCompressionByte(2, true)
	got := BuildInit(b, AudioRate48000, 2, RGBMode888)
	if got[1] != b {
		t.Fatalf("INIT[1] = %#x, want %#x", got[1], b)
	}
}

func TestBuildInit_PanicsOnInvalidCodecByte(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on INIT[1] codec 3")
		}
	}()
	BuildInit(3, AudioRate48000, 2, RGBMode888)
}
