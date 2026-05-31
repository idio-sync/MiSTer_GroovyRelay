package plex

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestAutoAdvance_ConfigDefaultsOff(t *testing.T) {
	if DefaultConfig().AutoAdvance {
		t.Fatal("auto_advance must default to false")
	}
}

func TestAutoAdvance_CurrentValuesIncludesKey(t *testing.T) {
	a := &Adapter{}
	a.plexCfg = Config{AutoAdvance: true}
	got := a.CurrentValues()["auto_advance"]
	if got != true {
		t.Fatalf("CurrentValues[auto_advance] = %v, want true", got)
	}
}

func TestAutoAdvance_ScopeIsHotSwap(t *testing.T) {
	if scopeForPlexField("auto_advance") != adaptersScopeHotSwapForTest() {
		t.Fatalf("auto_advance scope = %v, want ScopeHotSwap", scopeForPlexField("auto_advance"))
	}
}

func TestAutoAdvance_DiffDetectsChange(t *testing.T) {
	old := Config{AutoAdvance: false}
	neu := Config{AutoAdvance: true}
	changed := diffPlexConfig(old, neu)
	found := false
	for _, k := range changed {
		if k == "auto_advance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diffPlexConfig did not report auto_advance change: %v", changed)
	}
}

func TestAutoAdvance_CompanionMirrorSeedsAndUpdates(t *testing.T) {
	c := NewCompanion(CompanionConfig{AutoAdvance: true}, nil)
	if !c.autoAdvance.Load() {
		t.Fatal("NewCompanion did not seed autoAdvance from CompanionConfig")
	}
	c.SetAutoAdvance(false)
	if c.autoAdvance.Load() {
		t.Fatal("SetAutoAdvance(false) did not update the mirror")
	}
}

func TestAutoAdvance_ApplyConfigUpdatesRunningCompanionMirror(t *testing.T) {
	a, err := NewAdapter(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: t.TempDir(),
			UI:      config.UIConfig{HTTPPort: 32500},
		},
		Core:       &fakeCore{},
		TokenStore: &StoredData{DeviceUUID: "uuid-auto-advance"},
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.plexCfg = DefaultConfig()
	a.plexCfg.AutoAdvance = false
	a.ensureFinalized()
	if a.companion == nil {
		t.Fatal("companion was not finalized")
	}
	if a.companion.autoAdvance.Load() {
		t.Fatal("initial companion autoAdvance = true, want false")
	}

	raw, meta := sectionPrimitive(t, `
enabled = true
device_name = "MiSTer"
profile_name = "Plex Home Theater"
max_video_bitrate_kbps = 1500
auto_advance = true
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v, want ScopeHotSwap", scope)
	}
	if !a.companion.autoAdvance.Load() {
		t.Fatal("ApplyConfig did not update companion autoAdvance mirror")
	}
}

func adaptersScopeHotSwapForTest() adapters.ApplyScope { return adapters.ScopeHotSwap }
