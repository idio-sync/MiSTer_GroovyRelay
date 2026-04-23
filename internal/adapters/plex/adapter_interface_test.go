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

// sectionPrimitive wraps a [adapters.plex] block around body and
// decodes it, returning the Primitive + meta ApplyConfig needs.
func sectionPrimitive(t *testing.T, body string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	wrapper := "[adapters.plex]\n" + body
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(wrapper, &envelope)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.Adapters["plex"], meta
}

// TestApplyConfig_DeviceNameRestartBridge covers the 7.4 review
// correction: device_name is NOT a hot-swap because identity is
// snapshotted at startup into Companion /resources, GDM replies,
// timeline headers, and plex.tv registration. Until live identity
// propagation lands, the conservative choice is restart-required.
func TestApplyConfig_DeviceNameRestartBridge(t *testing.T) {
	a := &Adapter{plexCfg: Config{
		Enabled: true, DeviceName: "MiSTer", ProfileName: "Plex Home Theater",
	}}
	raw, meta := sectionPrimitive(t, `
device_name = "NewName"
enabled = true
profile_name = "Plex Home Theater"
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeRestartBridge {
		t.Errorf("scope = %v, want RestartBridge", scope)
	}
	if a.plexCfg.DeviceName != "NewName" {
		t.Errorf("DeviceName not applied: %q", a.plexCfg.DeviceName)
	}
}

func TestApplyConfig_ProfileNameRestartCast(t *testing.T) {
	a := &Adapter{plexCfg: Config{
		Enabled: true, DeviceName: "MiSTer", ProfileName: "Plex Home Theater",
	}}
	raw, meta := sectionPrimitive(t, `
device_name = "MiSTer"
enabled = true
profile_name = "Plex Web Client"
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v, want RestartCast", scope)
	}
}

// TestApplyConfig_MaxScopeWins verifies max-scope-wins aggregation
// (design §9.1). Changing device_name (restart-bridge) AND
// profile_name (restart-cast) together → restart-bridge wins.
func TestApplyConfig_MaxScopeWins(t *testing.T) {
	a := &Adapter{plexCfg: Config{
		Enabled: true, DeviceName: "MiSTer", ProfileName: "Plex Home Theater",
	}}
	raw, meta := sectionPrimitive(t, `
device_name = "NewName"
enabled = true
profile_name = "Plex Web Client"
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeRestartBridge {
		t.Errorf("scope = %v, want RestartBridge (max-wins)", scope)
	}
}

// TestApplyConfig_InvalidRejected confirms the state-untouched
// guarantee: a validation failure must leave plexCfg unchanged so
// the write-before-apply contract stays honest (disk already has
// the candidate; if we apply fails later, the running process
// sticks with the known-good old values).
func TestApplyConfig_InvalidRejected(t *testing.T) {
	before := Config{Enabled: true, DeviceName: "MiSTer", ProfileName: "Plex Home Theater"}
	a := &Adapter{plexCfg: before}
	raw, meta := sectionPrimitive(t, `
device_name = ""
enabled = true
profile_name = "Plex Home Theater"
`)
	_, err := a.ApplyConfig(raw, meta)
	if err == nil {
		t.Fatal("want validation error")
	}
	if a.plexCfg.DeviceName != before.DeviceName {
		t.Errorf("plexCfg mutated despite validation failure: %q", a.plexCfg.DeviceName)
	}
}
