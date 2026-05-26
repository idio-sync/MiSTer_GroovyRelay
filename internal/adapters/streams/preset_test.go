package streams

import (
	"context"
	"errors"
	"net/http"
	"strings"
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

func TestCastPreset_SlotOutOfRange(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, slot := range []int{0, -1, 13, 99} {
		err := a.CastPreset(context.Background(), slot)
		if err == nil {
			t.Errorf("CastPreset(%d) err = nil, want non-nil", slot)
			continue
		}
		var qerr *adapters.QuickCastError
		if !errors.As(err, &qerr) {
			t.Errorf("CastPreset(%d) err = %v, want *QuickCastError", slot, err)
			continue
		}
		if qerr.Status != http.StatusBadRequest || qerr.Chip != "BAD SLOT" {
			t.Errorf("CastPreset(%d) qerr = %+v, want Status=400 Chip=BAD SLOT", slot, qerr)
		}
	}
}

func TestCastPreset_SuccessfulSlotForwardsToFakeCore(t *testing.T) {
	t.Parallel()
	// newTestAdapterWithFakeCore constructs an *Adapter with catalogs
	// pre-populated AND returns a *fakeCore that records SessionRequests
	// in core.lastReq. ensureStartupSnapshot is a no-op in this scenario
	// (catalogs already loaded), so this test covers the happy path:
	// slot 9 → cartoon-rewind:heman → StartResolvedStream → fakeCore.StartSession.
	a, core := newTestAdapterWithFakeCore(t)
	if err := a.CastPreset(context.Background(), 9); err != nil {
		t.Fatalf("CastPreset(9) err = %v", err)
	}
	// fakeCore.lastReq carries the SessionRequest that StartResolvedStream
	// built. The adapter sets req.Source = "streams" and req.AdapterRef =
	// "streams:cartoon-rewind:heman:<sessionID>:<token>" (5 segments).
	if core.lastReq.Source != "streams" {
		t.Errorf("lastReq.Source = %q, want streams", core.lastReq.Source)
	}
	if !strings.HasPrefix(core.lastReq.AdapterRef, "streams:cartoon-rewind:heman:") {
		t.Errorf("lastReq.AdapterRef = %q, want prefix streams:cartoon-rewind:heman:", core.lastReq.AdapterRef)
	}
}
