package streams

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestPresets_ReturnsTwelveEntries(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	presets := a.Presets()
	if len(presets) != 12 {
		t.Fatalf("len(Presets) = %d, want 12", len(presets))
	}
}

func TestPresets_EveryEntryResolvesAgainstBundledManifest(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	presets := a.Presets()
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

func TestPresets_BadgeClassWithinEnum(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{"mtv": true, "cartoon": true, "toonami": true}
	a := &Adapter{}
	for i, p := range a.Presets() {
		if !allowed[p.BadgeClass] {
			t.Errorf("slot %d: BadgeClass = %q, want one of {mtv, cartoon, toonami}", i+1, p.BadgeClass)
		}
	}
}

func TestPresets_LiveFlagOnToonamiSlots(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	presets := a.Presets()
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

func TestPresets_SlotsAre1Indexed(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	for i, p := range a.Presets() {
		if p.Slot != i+1 {
			t.Errorf("Presets[%d].Slot = %d, want %d", i, p.Slot, i+1)
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

func TestSetPresetStarred_DelegatesToStore(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	// Remove an existing default, then add it back.
	res, err := a.SetPresetStarred(context.Background(), "mtv-rewind", "1stday", false)
	if err != nil {
		t.Fatalf("SetPresetStarred remove: %v", err)
	}
	if res.Starred || len(res.Cleared) != 1 {
		t.Errorf("remove res = %+v, want Starred=false Cleared=[1]", res)
	}
	res, err = a.SetPresetStarred(context.Background(), "mtv-rewind", "1stday", true)
	if err != nil {
		t.Fatalf("SetPresetStarred add: %v", err)
	}
	if !res.Starred || res.Slot != 1 {
		t.Errorf("add res = %+v, want Starred=true Slot=1", res)
	}
}

func TestMovePreset_DelegatesToStore(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	pre := a.Presets()
	if err := a.MovePreset(context.Background(), 1, 7); err != nil {
		t.Fatalf("MovePreset: %v", err)
	}
	post := a.Presets()
	if post[0].ChannelID != pre[6].ChannelID || post[6].ChannelID != pre[0].ChannelID {
		t.Errorf("swap incomplete: pre[1]=%q pre[7]=%q post[1]=%q post[7]=%q",
			pre[0].ChannelID, pre[6].ChannelID, post[0].ChannelID, post[6].ChannelID)
	}
}

func TestPresets_ReadsFromStore(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	// Snapshot before & after a mutation diverge — proves Presets reads
	// the store, not the bundledChassisPresets literal.
	pre := a.Presets()
	if _, err := a.SetPresetStarred(context.Background(), "mtv-rewind", "1stday", false); err != nil {
		t.Fatalf("SetPresetStarred: %v", err)
	}
	post := a.Presets()
	if pre[0].ProviderID == post[0].ProviderID {
		t.Errorf("Presets did not reflect mutation: pre[1]=%+v post[1]=%+v", pre[0], post[0])
	}
}
