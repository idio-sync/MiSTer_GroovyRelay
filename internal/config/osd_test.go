package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDefaultBridge_OSDOnWithoutClock(t *testing.T) {
	want := OSDConfig{Enabled: true}
	if got := defaultBridge().OSD; got != want {
		t.Fatalf("defaultBridge().OSD = %+v, want %+v", got, want)
	}
}

func TestLoadSectioned_DefaultsOSDWhenAbsent(t *testing.T) {
	// sectionedTOML has no [bridge.osd]; upgraded deployments get the OSD
	// on and the clock off.
	s, _, err := loadSectionedFromBytes([]byte(sectionedTOML))
	if err != nil {
		t.Fatalf("loadSectionedFromBytes: %v", err)
	}
	if want := (OSDConfig{Enabled: true}); s.Bridge.OSD != want {
		t.Fatalf("bridge.osd = %+v, want default %+v", s.Bridge.OSD, want)
	}
}

func TestLoadSectioned_ParsesOSDSection(t *testing.T) {
	data := sectionedTOML + `
[bridge.osd]
enabled = false
clock = true
clock_24h = true
`
	s, _, err := loadSectionedFromBytes([]byte(data))
	if err != nil {
		t.Fatalf("loadSectionedFromBytes: %v", err)
	}
	want := OSDConfig{Enabled: false, Clock: true, Clock24h: true}
	if s.Bridge.OSD != want {
		t.Fatalf("bridge.osd = %+v, want %+v", s.Bridge.OSD, want)
	}
}

func TestMigrate_LegacyGetsDefaultOSD(t *testing.T) {
	migrated, err := Migrate([]byte(legacyTOML))
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s, _, err := loadSectionedFromBytes(migrated)
	if err != nil {
		t.Fatalf("decode migrated: %v", err)
	}
	if want := (OSDConfig{Enabled: true}); s.Bridge.OSD != want {
		t.Fatalf("migrated bridge.osd = %+v, want %+v", s.Bridge.OSD, want)
	}
}

func TestExampleTOML_DocumentsOSDDefaults(t *testing.T) {
	var s struct {
		Bridge BridgeConfig `toml:"bridge"`
	}
	meta, err := toml.Decode(string(ExampleTOML()), &s)
	if err != nil {
		t.Fatalf("decode example.toml: %v", err)
	}
	for _, key := range []string{"enabled", "clock", "clock_24h"} {
		if !meta.IsDefined("bridge", "osd", key) {
			t.Errorf("example.toml is missing bridge.osd.%s", key)
		}
	}
	if want := defaultBridge().OSD; s.Bridge.OSD != want {
		t.Errorf("example.toml bridge.osd = %+v, want the defaults %+v", s.Bridge.OSD, want)
	}
}
