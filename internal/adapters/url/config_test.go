package url

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDefaultConfig_Disabled(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Error("DefaultConfig should be disabled by default (spec §Config schema)")
	}
}

func TestConfig_Validate_EmptyOK(t *testing.T) {
	// DefaultConfig() is the realistic baseline: Adapter.DecodeConfig
	// starts there and overlays TOML, so an "empty TOML section" still
	// inherits the timeout=30 default. Constructing Config{} directly
	// would fail the new YtdlpResolveTimeoutSeconds range check.
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Errorf("DefaultConfig() should validate, got %v", err)
	}
}

func TestConfig_Validate_EnabledTrueOK(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	if err := c.Validate(); err != nil {
		t.Errorf("DefaultConfig() with Enabled=true should validate, got %v", err)
	}
}

func TestConfig_TOMLDecode(t *testing.T) {
	raw := `
[adapters.url]
enabled = true
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	if err := meta.PrimitiveDecode(envelope.Adapters["url"], &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !c.Enabled {
		t.Error("Enabled not decoded")
	}
}

func TestDefaultConfig_YtdlpDefaults(t *testing.T) {
	c := DefaultConfig()
	if !c.YtdlpEnabled {
		t.Error("YtdlpEnabled default should be true")
	}
	if c.YtdlpResolveTimeoutSeconds != 30 {
		t.Errorf("timeout default = %d, want 30", c.YtdlpResolveTimeoutSeconds)
	}
	if c.YtdlpFormat == "" {
		t.Error("YtdlpFormat default is empty")
	}
	if !strings.Contains(c.YtdlpFormat, "height<=720") {
		t.Errorf("YtdlpFormat = %q, expected to cap at 720p", c.YtdlpFormat)
	}
	if len(c.YtdlpHosts) == 0 {
		t.Error("YtdlpHosts default is empty")
	}
	// Spot-check the curated list.
	want := map[string]bool{"youtube.com": true, "twitch.tv": true, "archive.org": true}
	for _, h := range c.YtdlpHosts {
		delete(want, h)
	}
	if len(want) != 0 {
		t.Errorf("missing default hosts: %v", want)
	}
	// Spot-check the explicit exclusions (review fix I6).
	for _, bad := range []string{"tiktok.com", "instagram.com", "x.com", "twitter.com", "reddit.com"} {
		for _, h := range c.YtdlpHosts {
			if h == bad {
				t.Errorf("default hosts wrongly include %q (excluded per review fix I6)", bad)
			}
		}
	}
}

func TestConfigValidate_RejectsTimeoutOutOfRange(t *testing.T) {
	c := DefaultConfig()
	c.YtdlpResolveTimeoutSeconds = 0
	if err := c.Validate(); err == nil {
		t.Error("timeout 0 accepted")
	}
	c.YtdlpResolveTimeoutSeconds = 4
	if err := c.Validate(); err == nil {
		t.Error("timeout 4 accepted (below 5)")
	}
	c.YtdlpResolveTimeoutSeconds = 121
	if err := c.Validate(); err == nil {
		t.Error("timeout 121 accepted (above 120)")
	}
}

func TestConfigValidate_AcceptsTimeoutInRange(t *testing.T) {
	c := DefaultConfig()
	for _, v := range []int{5, 30, 60, 120} {
		c.YtdlpResolveTimeoutSeconds = v
		if err := c.Validate(); err != nil {
			t.Errorf("timeout %d: %v", v, err)
		}
	}
}

func TestConfigValidate_RejectsBadHostnames(t *testing.T) {
	c := DefaultConfig()
	for _, bad := range []string{
		"https://youtube.com", // scheme present
		"youtube.com:443",     // port
		"youtube.com/foo",     // path
		"with space.com",      // whitespace
		"",                    // empty
	} {
		c.YtdlpHosts = []string{bad}
		if err := c.Validate(); err == nil {
			t.Errorf("hostname %q accepted", bad)
		}
	}
}

func TestConfigValidate_LowercasesHostnames(t *testing.T) {
	c := DefaultConfig()
	c.YtdlpHosts = []string{"YouTube.COM", "TWITCH.tv"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.YtdlpHosts[0] != "youtube.com" || c.YtdlpHosts[1] != "twitch.tv" {
		t.Errorf("hostnames not lowercased: %v", c.YtdlpHosts)
	}
}
