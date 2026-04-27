package jellyfin

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestConfig_DefaultsWhenSectionAbsent(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Enabled {
		t.Errorf("default Enabled = true, want false")
	}
	if cfg.MaxVideoBitrateKbps != 4000 {
		t.Errorf("default MaxVideoBitrateKbps = %d, want 4000", cfg.MaxVideoBitrateKbps)
	}
	if cfg.ServerURL != "" {
		t.Errorf("default ServerURL = %q, want empty", cfg.ServerURL)
	}
	if cfg.DeviceName != "" {
		t.Errorf("default DeviceName = %q, want empty", cfg.DeviceName)
	}
}

func TestConfig_TOMLRoundTrip(t *testing.T) {
	src := `
enabled                = true
server_url             = "https://jellyfin.example.com"
device_name            = "Living Room MiSTer"
max_video_bitrate_kbps = 8000
`
	var cfg Config
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if cfg.ServerURL != "https://jellyfin.example.com" {
		t.Errorf("ServerURL = %q", cfg.ServerURL)
	}
	if cfg.DeviceName != "Living Room MiSTer" {
		t.Errorf("DeviceName = %q", cfg.DeviceName)
	}
	if cfg.MaxVideoBitrateKbps != 8000 {
		t.Errorf("MaxVideoBitrateKbps = %d", cfg.MaxVideoBitrateKbps)
	}
}

func TestConfig_Validate_Accepts(t *testing.T) {
	cases := []Config{
		{Enabled: false, ServerURL: "", MaxVideoBitrateKbps: 4000},                            // disabled, empty URL OK
		{Enabled: true, ServerURL: "https://jellyfin.example.com", MaxVideoBitrateKbps: 4000}, // https
		{Enabled: true, ServerURL: "http://10.0.0.5:8096", MaxVideoBitrateKbps: 200},          // http with port, lower bound
		{Enabled: true, ServerURL: "http://jellyfin", MaxVideoBitrateKbps: 50000},             // bare host, upper bound
		{Enabled: true, ServerURL: "https://jellyfin.example.com", MaxVideoBitrateKbps: 4000, DeviceName: "Library Den"},
	}
	for i, c := range cases {
		if err := c.Validate(); err != nil {
			t.Errorf("case %d: Validate() = %v, want nil", i, err)
		}
	}
}

func TestConfig_Validate_Rejects(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string // substring expected in error message
	}{
		{"missing-url-when-enabled", Config{Enabled: true, ServerURL: "", MaxVideoBitrateKbps: 4000}, "server_url"},
		{"bad-scheme", Config{Enabled: true, ServerURL: "ftp://jellyfin.example.com", MaxVideoBitrateKbps: 4000}, "scheme"},
		{"url-with-userinfo", Config{Enabled: true, ServerURL: "https://user:pass@jellyfin.example.com", MaxVideoBitrateKbps: 4000}, "username"},
		{"path-in-url", Config{Enabled: true, ServerURL: "https://jellyfin.example.com/jf", MaxVideoBitrateKbps: 4000}, "path"},
		{"query-in-url", Config{Enabled: true, ServerURL: "https://jellyfin.example.com?x=1", MaxVideoBitrateKbps: 4000}, "query"},
		{"fragment-in-url", Config{Enabled: true, ServerURL: "https://jellyfin.example.com#x", MaxVideoBitrateKbps: 4000}, "fragment"},
		{"bitrate-low", Config{Enabled: true, ServerURL: "https://jellyfin.example.com", MaxVideoBitrateKbps: 100}, "max_video_bitrate_kbps"},
		{"bitrate-high", Config{Enabled: true, ServerURL: "https://jellyfin.example.com", MaxVideoBitrateKbps: 50001}, "max_video_bitrate_kbps"},
		{"device-name-too-long", Config{Enabled: true, ServerURL: "https://jellyfin.example.com", MaxVideoBitrateKbps: 4000, DeviceName: strings.Repeat("x", 65)}, "device_name"},
		{"device-name-non-ascii", Config{Enabled: true, ServerURL: "https://jellyfin.example.com", MaxVideoBitrateKbps: 4000, DeviceName: "MiSTer\x00"}, "device_name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate() = %v, want substring %q", err, c.want)
			}
		})
	}
}
