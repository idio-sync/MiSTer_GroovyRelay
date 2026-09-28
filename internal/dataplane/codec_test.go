package dataplane

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestResolveCodec(t *testing.T) {
	cases := []struct {
		in   Codec
		core groovy.Core
		want Codec
		warn bool
	}{
		{CodecAuto, groovy.CoreGroovy, CodecLZ4, false},
		{CodecAuto, groovy.CoreGroovyNLC, CodecLZ4, false}, // §4.2: NLC opt-in only
		{CodecLZ4, groovy.CoreGroovyNLC, CodecLZ4, false},
		{CodecRaw, groovy.CoreGroovyNLC, CodecRaw, false},
		{"", groovy.CoreGroovy, CodecRaw, false}, // bare PlaneConfig keeps today's raw default
		{"bogus", groovy.CoreGroovy, CodecLZ4, true},
	}
	for _, c := range cases {
		got, warn := ResolveCodec(c.in, c.core)
		if got != c.want || (warn != "") != c.warn {
			t.Errorf("ResolveCodec(%q, %v) = %q, %q; want %q, warn=%v", c.in, c.core, got, warn, c.want, c.warn)
		}
	}
}

func TestInitCompressionByte(t *testing.T) {
	if initCompressionByte(CodecLZ4) != groovy.LZ4ModeDefault || initCompressionByte(CodecRaw) != groovy.LZ4ModeOff {
		t.Fatal("unexpected INIT compression byte")
	}
}

func TestPlane_InitCompressionFollowsCodec(t *testing.T) {
	for codec, want := range map[Codec]byte{
		CodecLZ4:  groovy.LZ4ModeDefault,
		CodecAuto: groovy.LZ4ModeDefault,
		CodecRaw:  groovy.LZ4ModeOff,
		"":        groovy.LZ4ModeOff,
	} {
		initCmd, _, _ := runPlaneUntilInit(t, PlaneConfig{Codec: codec})
		if initCmd.Init == nil || initCmd.Init.LZ4Frames != want {
			t.Errorf("codec %q: INIT[1] = %v, want %d", codec, initCmd.Init, want)
		}
	}
}
