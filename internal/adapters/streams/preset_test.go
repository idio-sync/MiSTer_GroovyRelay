package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestBundledPresets_ReturnsTwelveEntries(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	presets := a.BundledPresets()
	if len(presets) != 12 {
		t.Fatalf("len(BundledPresets) = %d, want 12", len(presets))
	}
}

func TestBundledPresets_EveryEntryResolvesAgainstBundledManifest(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	presets := a.BundledPresets()
	manifest := bundledManifest()
	providers := map[string]*ProviderDefinition{}
	for i := range manifest.Providers {
		p := manifest.Providers[i]
		providers[p.ID] = &p
	}
	for i, p := range presets {
		prov, ok := providers[p.ProviderID]
		if !ok {
			t.Errorf("slot %d: ProviderID %q not in bundled manifest", i+1, p.ProviderID)
			continue
		}
		var found bool
		for _, c := range prov.Channels {
			if c.ID == p.ChannelID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("slot %d: ChannelID %q not in provider %q channels", i+1, p.ChannelID, p.ProviderID)
		}
	}
}

func TestBundledPresets_BadgeClassWithinEnum(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"mtv": true, "cartoon": true, "toonami": true}
	a := &Adapter{}
	for i, p := range a.BundledPresets() {
		if !allowed[p.BadgeClass] {
			t.Errorf("slot %d: BadgeClass = %q, want one of {mtv, cartoon, toonami}", i+1, p.BadgeClass)
		}
	}
}

func TestBundledPresets_LiveFlagOnToonamiSlots(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	presets := a.BundledPresets()
	for i, p := range presets {
		switch i + 1 {
		case 11, 12:
			if !p.Live {
				t.Errorf("slot %d: Live = false, want true (toonami)", i+1)
			}
		default:
			if p.Live {
				t.Errorf("slot %d: Live = true, want false (non-toonami)", i+1)
			}
		}
	}
	_ = adapters.PresetEntry{} // exercise the import
}

func TestBundledPresets_SlotsAre1Indexed(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	for i, p := range a.BundledPresets() {
		if p.Slot != i+1 {
			t.Errorf("BundledPresets[%d].Slot = %d, want %d", i, p.Slot, i+1)
		}
	}
}
