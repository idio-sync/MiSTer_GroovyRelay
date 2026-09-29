package config

import (
	"regexp"
	"strings"
	"testing"
)

func TestEffectiveCodec(t *testing.T) {
	for in, want := range map[string]string{"": "auto", "auto": "auto", "raw": "raw", "lz4": "lz4", "nlc": "nlc"} {
		if got := (VideoConfig{Codec: in}).EffectiveCodec(); got != want {
			t.Errorf("EffectiveCodec(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEffectiveNLCPack(t *testing.T) {
	for in, want := range map[string]string{"": "tiled", "tiled": "tiled", "rice": "rice"} {
		if got := (VideoConfig{NLCPack: in}).EffectiveNLCPack(); got != want {
			t.Errorf("EffectiveNLCPack(%q) = %q, want %q", in, got, want)
		}
	}
}

func loadVideo(t *testing.T, video string) VideoConfig {
	t.Helper()
	doc := "[bridge]\n[bridge.mister]\nhost = \"10.0.0.2\"\n[bridge.video]\n" + video
	s, meta, err := loadSectionedFromBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	s.meta = meta
	return s.Bridge.Video
}

func TestSectionedLoad_LegacyLZ4Migrates(t *testing.T) {
	cases := []struct {
		video string
		want  string
	}{
		{"lz4_enabled = false\n", CodecRaw},
		{"lz4_enabled = true\n", CodecAuto},
		{"", CodecAuto},                                      // default
		{"codec = \"lz4\"\nlz4_enabled = false\n", CodecLZ4}, // explicit codec wins
	}
	for _, c := range cases {
		v := loadVideo(t, c.video)
		if v.Codec != c.want {
			t.Errorf("%q: Codec = %q, want %q", c.video, v.Codec, c.want)
		}
		if v.LegacyLZ4Enabled != nil {
			t.Errorf("%q: LegacyLZ4Enabled not cleared", c.video)
		}
	}
}

func TestSectionedValidate_RejectsUnknownCodec(t *testing.T) {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.MiSTer.Host = "10.0.0.2"
	s.Bridge.Video.Codec = "bogus"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "bridge.video.codec") {
		t.Fatalf("Validate() = %v, want bridge.video.codec error", err)
	}
	s.Bridge.Video.Codec = ""
	if err := s.Validate(); err != nil {
		t.Fatalf("empty codec should validate as auto: %v", err)
	}
}

func TestSectionedValidate_AcceptsNLCCodec(t *testing.T) {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.MiSTer.Host = "10.0.0.2"
	s.Bridge.Video.Codec = "nlc"
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nlc codec to be accepted", err)
	}
}

func TestSectionedValidate_RejectsBadNLCNear(t *testing.T) {
	for _, bad := range []int{4, -1} {
		s := &Sectioned{Bridge: defaultBridge()}
		s.Bridge.MiSTer.Host = "10.0.0.2"
		s.Bridge.Video.NLCNear = bad
		err := s.Validate()
		if err == nil || !strings.Contains(err.Error(), "bridge.video.nlc_near") {
			t.Errorf("NLCNear=%d: Validate() = %v, want bridge.video.nlc_near error", bad, err)
		}
	}
}

func TestSectionedValidate_RejectsBadNLCPack(t *testing.T) {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.MiSTer.Host = "10.0.0.2"
	s.Bridge.Video.NLCPack = "global"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "bridge.video.nlc_pack") {
		t.Fatalf("Validate() = %v, want bridge.video.nlc_pack error", err)
	}
}

func TestSectionedLoad_NLCFieldsRoundTrip(t *testing.T) {
	v := loadVideo(t, "codec = \"nlc\"\nnlc_near = 2\nnlc_pack = \"rice\"\n")
	if v.Codec != "nlc" {
		t.Errorf("Codec = %q, want nlc", v.Codec)
	}
	if v.NLCNear != 2 {
		t.Errorf("NLCNear = %d, want 2", v.NLCNear)
	}
	if v.NLCPack != "rice" {
		t.Errorf("NLCPack = %q, want rice", v.NLCPack)
	}
}

func TestDefaultBridge_NLCDefaults(t *testing.T) {
	b := defaultBridge()
	if b.Video.NLCNear != 0 {
		t.Errorf("default NLCNear = %d, want 0", b.Video.NLCNear)
	}
	if b.Video.NLCPack != NLCPackTiled {
		t.Errorf("default NLCPack = %q, want %q", b.Video.NLCPack, NLCPackTiled)
	}
}

func TestMigrate_FlatLZ4DisabledBecomesRaw(t *testing.T) {
	out, err := Migrate([]byte("mister_host = \"10.0.0.2\"\nlz4_enabled = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `codec = "raw"`) {
		t.Fatalf("migrated config lacks codec = \"raw\":\n%s", out)
	}
	// Match the key itself, not delta_lz4_enabled (always encoded).
	if regexp.MustCompile(`(?m)^\s*lz4_enabled\s*=`).MatchString(string(out)) {
		t.Fatalf("migrated config still has lz4_enabled:\n%s", out)
	}
}
