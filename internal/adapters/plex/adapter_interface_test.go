package plex

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestAdapter_ConformsToInterface(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
}

func TestAdapter_Name(t *testing.T) {
	a := &Adapter{}
	if a.Name() != "plex" {
		t.Errorf("Name = %q", a.Name())
	}
}

func TestAdapter_DisplayName(t *testing.T) {
	a := &Adapter{}
	if a.DisplayName() != "Plex" {
		t.Errorf("DisplayName = %q", a.DisplayName())
	}
}

func TestAdapter_Fields_HasExpectedKeys(t *testing.T) {
	a := &Adapter{}
	want := map[string]bool{"enabled": false, "device_name": false, "profile_name": false, "server_url": false}
	for _, f := range a.Fields() {
		if _, ok := want[f.Key]; ok {
			want[f.Key] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("Fields() missing %q", k)
		}
	}
}

func TestAdapter_DecodeConfig_Basics(t *testing.T) {
	raw := `
[adapters.plex]
enabled = true
device_name = "TestMiSTer"
profile_name = "Plex Home Theater"
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{}
	if err := a.DecodeConfig(envelope.Adapters["plex"], meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if a.plexCfg.DeviceName != "TestMiSTer" {
		t.Errorf("DeviceName not decoded: %q", a.plexCfg.DeviceName)
	}
}

func TestAdapter_DecodeConfig_InvalidRejected(t *testing.T) {
	raw := `
[adapters.plex]
enabled = true
device_name = ""
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, _ := toml.Decode(raw, &envelope)
	a := &Adapter{}
	if err := a.DecodeConfig(envelope.Adapters["plex"], meta); err == nil {
		t.Fatal("want validation error for empty device_name")
	}
}

func TestAdapter_IsEnabled(t *testing.T) {
	a := &Adapter{plexCfg: Config{Enabled: true}}
	if !a.IsEnabled() {
		t.Error("want true")
	}
	a.plexCfg.Enabled = false
	if a.IsEnabled() {
		t.Error("want false")
	}
}

func TestAdapter_StatusInitial(t *testing.T) {
	a := &Adapter{}
	if a.Status().State != adapters.StateStopped {
		t.Error("initial state should be StateStopped")
	}
}
