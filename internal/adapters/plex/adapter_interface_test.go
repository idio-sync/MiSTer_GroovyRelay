package plex

import (
	"context"
	"errors"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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
	want := map[string]bool{
		"enabled":                false,
		"device_name":            false,
		"profile_name":           false,
		"server_url":             false,
		"max_video_bitrate_kbps": false,
	}
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

func TestApplyConfig_MaxBitrateRestartCastAndPropagatesLive(t *testing.T) {
	companion := NewCompanion(CompanionConfig{
		DeviceName:          "MiSTer",
		MaxVideoBitrateKbps: 1500,
	}, nil)
	a := &Adapter{
		plexCfg: Config{
			Enabled:             true,
			DeviceName:          "MiSTer",
			ProfileName:         "Plex Home Theater",
			MaxVideoBitrateKbps: 1500,
		},
		companion: companion,
	}
	raw, meta := sectionPrimitive(t, `
device_name = "MiSTer"
enabled = true
profile_name = "Plex Home Theater"
max_video_bitrate_kbps = 6000
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v, want RestartCast", scope)
	}
	if a.plexCfg.MaxVideoBitrateKbps != 6000 {
		t.Errorf("plexCfg not updated: got %d", a.plexCfg.MaxVideoBitrateKbps)
	}
	if got := companion.maxVideoBitrateKbps.Load(); got != 6000 {
		t.Errorf("companion live mirror not updated: got %d", got)
	}
}

func TestApplyConfig_BitrateOutOfBoundsRejected(t *testing.T) {
	a := &Adapter{plexCfg: Config{
		Enabled: true, DeviceName: "MiSTer", ProfileName: "Plex Home Theater",
		MaxVideoBitrateKbps: 1500,
	}}
	raw, meta := sectionPrimitive(t, `
device_name = "MiSTer"
enabled = true
profile_name = "Plex Home Theater"
max_video_bitrate_kbps = 99999
`)
	if _, err := a.ApplyConfig(raw, meta); err == nil {
		t.Fatal("want validation error for out-of-bounds bitrate")
	}
	if a.plexCfg.MaxVideoBitrateKbps != 1500 {
		t.Errorf("plexCfg mutated despite validation failure: %d", a.plexCfg.MaxVideoBitrateKbps)
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

// TestAdapter_OnVideoConfigChanged_PropagatesToCompanion pins the wiring
// from BridgeSaver's notify path through to the companion's modeline mirror.
// Covers the post-finalization path (companion already constructed): the
// adapter delegates to companion.SetModeline so the next request build sees
// the new preset shape.
func TestAdapter_OnVideoConfigChanged_PropagatesToCompanion(t *testing.T) {
	companion := NewCompanion(CompanionConfig{
		DeviceName: "MiSTer",
		Modeline:   "NTSC_480i",
	}, nil)
	a := &Adapter{
		plexCfg:   Config{Enabled: true, DeviceName: "MiSTer"},
		cfg:       AdapterConfig{Core: &fakeCore{}, TokenStore: &StoredData{}},
		companion: companion,
	}
	// Mark finalizeOnce as already-done so OnVideoConfigChanged doesn't try
	// to re-construct over our test companion.
	a.finalizeOnce.Do(func() {})

	a.OnVideoConfigChanged("PAL_576i")

	preset, err := companion.currentPreset()
	if err != nil {
		t.Fatalf("currentPreset: %v", err)
	}
	if preset.Name != "PAL_576i" {
		t.Errorf("companion preset = %q, want PAL_576i", preset.Name)
	}
}

// TestAdapter_OnVideoConfigChanged_FinalizesIfNeeded covers the early-save
// path: a modeline save lands before MountRoutes/Start runs ensureFinalized.
// OnVideoConfigChanged must trigger finalization AND override the stale
// bridge-snapshot modeline with the freshly-saved value.
func TestAdapter_OnVideoConfigChanged_FinalizesIfNeeded(t *testing.T) {
	a, err := NewAdapter(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: t.TempDir(),
			// Adapter's Bridge snapshot still reflects the pre-save value.
			Video: config.VideoConfig{Modeline: "NTSC_480i"},
			UI:    config.UIConfig{HTTPPort: 32500},
		},
		Core:       &fakeCore{},
		TokenStore: &StoredData{DeviceUUID: "uuid-finalize"},
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.plexCfg = DefaultConfig()
	a.plexCfg.DeviceName = "Probe"
	a.plexCfg.Enabled = true

	a.OnVideoConfigChanged("PAL_288p")

	if a.companion == nil {
		t.Fatal("companion was not finalized")
	}
	preset, err := a.companion.currentPreset()
	if err != nil {
		t.Fatalf("currentPreset: %v", err)
	}
	if preset.Name != "PAL_288p" {
		t.Errorf("companion preset = %q, want PAL_288p (must override stale bridge snapshot)", preset.Name)
	}
}

// TestAdapter_StartPassesHostIPToDiscovery pins that the adapter
// threads its configured HostIP through to DiscoveryConfig. Uses the
// package-level newDiscovery seam so we don't bind real multicast
// sockets.
//
// The fake constructor returns (nil, error) rather than a partial
// Discovery. Adapter.Start treats discovery as best-effort: on error
// it logs WARN and skips launching the Run goroutine. Returning a
// nil-listen Discovery would crash Run() on its first ReadFromUDP.
func TestAdapter_StartPassesHostIPToDiscovery(t *testing.T) {
	var captured DiscoveryConfig
	prev := newDiscovery
	newDiscovery = func(cfg DiscoveryConfig) (*Discovery, error) {
		captured = cfg
		return nil, errors.New("test fake: discovery disabled")
	}
	t.Cleanup(func() { newDiscovery = prev })

	a, err := NewAdapter(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: t.TempDir(),
			UI:      config.UIConfig{HTTPPort: 32500},
		},
		Core:       &fakeCore{},
		TokenStore: &StoredData{DeviceUUID: "uuid-thread"},
		HostIP:     "10.42.42.42",
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.plexCfg = DefaultConfig()
	a.plexCfg.Enabled = true
	a.plexCfg.DeviceName = "Probe"

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	if captured.HostIP != "10.42.42.42" {
		t.Errorf("DiscoveryConfig.HostIP = %q; want 10.42.42.42", captured.HostIP)
	}
	if captured.DeviceName != "Probe" {
		t.Errorf("DiscoveryConfig.DeviceName = %q; want Probe", captured.DeviceName)
	}
	if captured.DeviceUUID != "uuid-thread" {
		t.Errorf("DiscoveryConfig.DeviceUUID = %q; want uuid-thread", captured.DeviceUUID)
	}
	if captured.Version != "test" {
		t.Errorf("DiscoveryConfig.Version = %q; want test", captured.Version)
	}
}
