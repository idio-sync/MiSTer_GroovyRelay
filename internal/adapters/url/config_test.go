package url

import (
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
	c := Config{}
	if err := c.Validate(); err != nil {
		t.Errorf("empty config should validate, got %v", err)
	}
}

func TestConfig_Validate_EnabledTrueOK(t *testing.T) {
	c := Config{Enabled: true}
	if err := c.Validate(); err != nil {
		t.Errorf("enabled=true should validate, got %v", err)
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
