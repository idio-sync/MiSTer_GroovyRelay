package uiserver

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestDiffBridgeConfig_OSDFields(t *testing.T) {
	oldCfg := config.BridgeConfig{OSD: config.OSDConfig{Enabled: true}}
	newCfg := config.BridgeConfig{OSD: config.OSDConfig{Enabled: false, Clock: true, Clock24h: true}}
	keys := diffBridgeConfig(oldCfg, newCfg)
	for _, want := range []string{"osd.enabled", "osd.clock", "osd.clock_24h"} {
		if !containsStr(keys, want) {
			t.Errorf("diff keys %v missing %q", keys, want)
		}
	}
	if keys := diffBridgeConfig(oldCfg, oldCfg); len(keys) != 0 {
		t.Errorf("diff of identical configs = %v, want none", keys)
	}
}

// The display re-reads its options on every field, so every OSD setting
// applies to a live cast without a rebuild.
func TestScopeForBridgeField_OSDFieldsHotSwap(t *testing.T) {
	for _, key := range []string{"osd.enabled", "osd.clock", "osd.clock_24h"} {
		if got := scopeForBridgeField(key); got != adapters.ScopeHotSwap {
			t.Errorf("scopeForBridgeField(%q) = %v, want ScopeHotSwap", key, got)
		}
	}
}
