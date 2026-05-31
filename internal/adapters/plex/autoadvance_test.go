package plex

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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

func adaptersScopeHotSwapForTest() adapters.ApplyScope { return adapters.ScopeHotSwap }
