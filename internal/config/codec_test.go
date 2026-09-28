package config

import (
	"regexp"
	"strings"
	"testing"
)

func TestEffectiveCodec(t *testing.T) {
	for in, want := range map[string]string{"": "auto", "auto": "auto", "raw": "raw", "lz4": "lz4"} {
		if got := (VideoConfig{Codec: in}).EffectiveCodec(); got != want {
			t.Errorf("EffectiveCodec(%q) = %q, want %q", in, got, want)
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
	s.Bridge.Video.Codec = "nlc" // not valid until Part 2
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "bridge.video.codec") {
		t.Fatalf("Validate() = %v, want bridge.video.codec error", err)
	}
	s.Bridge.Video.Codec = ""
	if err := s.Validate(); err != nil {
		t.Fatalf("empty codec should validate as auto: %v", err)
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
