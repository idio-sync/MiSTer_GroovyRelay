package streams

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func newStoreForTest(t *testing.T) (*presetStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "chassis_presets.json")
	st, err := newPresetStore(path, defaultCatalogResolver(t))
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	return st, path
}

// defaultCatalogResolver returns a function the store uses to validate
// (provider, channel) pairs against the bundled streams catalog.
// Implementation detail: the store takes a catalogResolver func at
// construction so unit tests don't depend on a full Adapter.
func defaultCatalogResolver(t *testing.T) catalogResolver {
	t.Helper()
	m := bundledManifest()
	return func(providerID, channelID string) (adapters.PresetEntry, bool) {
		for _, p := range m.Providers {
			if p.ID != providerID {
				continue
			}
			for _, c := range p.Channels {
				if c.ID != channelID {
					continue
				}
				badge := providerBadges[p.ID]
				return adapters.PresetEntry{
					ProviderID: p.ID,
					ChannelID:  c.ID,
					Title:      c.Name,
					BadgeLabel: badge.Label,
					BadgeClass: badge.Class,
					Live:       p.Type == directStreamsProviderType,
				}, true
			}
		}
		return adapters.PresetEntry{}, false
	}
}

func TestPresetStore_LoadMissingFileSeedsFromBundled(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	if _, err := os.Stat(path); err == nil {
		t.Errorf("file %s should not exist on first load", path)
	}
	snap := st.Snapshot()
	filled := 0
	for _, e := range snap {
		if e.ProviderID != "" {
			filled++
		}
	}
	if filled != 12 {
		t.Errorf("seeded snapshot filled = %d, want 12 (from bundledChassisPresets)", filled)
	}
}

func TestPresetStore_SetStarredAddFillsFirstEmpty(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// Clear slot 7 so we know which one will be hit by the add.
	if _, err := st.SetStarred("cartoon-rewind", "loonytunes", false); err != nil {
		t.Fatalf("remove pre-condition: %v", err)
	}
	res, err := st.SetStarred("mtv-rewind", "amp", true)
	if err != nil {
		t.Fatalf("SetStarred add: %v", err)
	}
	if !res.Starred || res.Slot != 7 {
		t.Errorf("res = %+v, want Starred=true Slot=7", res)
	}
}

func TestPresetStore_SetStarredAddExistingNoop(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// "1stday" is already in slot 1 from the seeded defaults.
	res, err := st.SetStarred("mtv-rewind", "1stday", true)
	if err != nil {
		t.Fatalf("SetStarred add existing: %v", err)
	}
	if !res.Starred || res.Slot != 1 {
		t.Errorf("res = %+v, want Starred=true Slot=1 (no-op)", res)
	}
}

func TestPresetStore_SetStarredAddBankFull(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// Seeded snapshot is exactly 12 filled.
	_, err := st.SetStarred("mtv-rewind", "amp", true)
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusConflict || qerr.Chip != "BANK FULL" {
		t.Errorf("qerr = %+v, want Status=409 Chip=BANK FULL", qerr)
	}
}

func TestPresetStore_SetStarredRemoveExisting(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// "1stday" is in slot 1 from defaults.
	res, err := st.SetStarred("mtv-rewind", "1stday", false)
	if err != nil {
		t.Fatalf("SetStarred remove: %v", err)
	}
	if res.Starred {
		t.Errorf("res.Starred = true, want false")
	}
	if len(res.Cleared) != 1 || res.Cleared[0] != 1 {
		t.Errorf("res.Cleared = %v, want [1]", res.Cleared)
	}
}

func TestPresetStore_SetStarredRemoveAbsentNoop(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	res, err := st.SetStarred("mtv-rewind", "amp", false)
	if err != nil {
		t.Fatalf("SetStarred remove absent: %v", err)
	}
	if res.Starred {
		t.Errorf("res.Starred = true, want false")
	}
	if len(res.Cleared) != 0 {
		t.Errorf("res.Cleared = %v, want empty", res.Cleared)
	}
}

func TestPresetStore_UnknownChannelReturnsNotFound(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	_, err := st.SetStarred("mtv-rewind", "nonexistent", true)
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusNotFound || qerr.Chip != "NOT FOUND" {
		t.Errorf("qerr = %+v, want Status=404 Chip=NOT FOUND", qerr)
	}
}

func TestPresetStore_RemoveUnknownChannelReturnsNotFound(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	_, err := st.SetStarred("mtv-rewind", "nonexistent", false)
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusNotFound || qerr.Chip != "NOT FOUND" {
		t.Errorf("qerr = %+v, want Status=404 Chip=NOT FOUND", qerr)
	}
}

func TestPresetStore_MoveSwap(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	pre := st.Snapshot()
	if err := st.Move(1, 7); err != nil {
		t.Fatalf("Move: %v", err)
	}
	post := st.Snapshot()
	if post[0].ChannelID != pre[6].ChannelID {
		t.Errorf("slot 1 channel = %q, want %q (was in slot 7)", post[0].ChannelID, pre[6].ChannelID)
	}
	if post[6].ChannelID != pre[0].ChannelID {
		t.Errorf("slot 7 channel = %q, want %q (was in slot 1)", post[6].ChannelID, pre[0].ChannelID)
	}
}

func TestPresetStore_MoveNoOpFromEqualsTo(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	// File should NOT be written for a no-op move.
	if err := st.Move(3, 3); err != nil {
		t.Fatalf("Move(3,3): %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("no-op Move should not write the file")
	}
}

func TestPresetStore_MoveOutOfRange(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	for _, pair := range []struct{ from, to int }{
		{0, 1}, {1, 0}, {13, 1}, {1, 13}, {-1, 1}, {1, -1},
	} {
		err := st.Move(pair.from, pair.to)
		var qerr *adapters.QuickCastError
		if !errors.As(err, &qerr) {
			t.Errorf("Move(%d,%d) err = %v, want *QuickCastError", pair.from, pair.to, err)
			continue
		}
		if qerr.Status != http.StatusBadRequest || qerr.Chip != "BAD SLOT" {
			t.Errorf("Move(%d,%d) qerr = %+v, want Status=400 Chip=BAD SLOT", pair.from, pair.to, qerr)
		}
	}
}

func TestPresetStore_AtomicWriteVisible(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	if _, err := st.SetStarred("mtv-rewind", "1stday", false); err != nil {
		t.Fatalf("SetStarred remove: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after mutation: %v", err)
	}
	var doc struct {
		Version int                 `json:"version"`
		Slots   []persistedPresetIO `json:"slots"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1", doc.Version)
	}
	// slot 1 was cleared, so the persisted slice should NOT contain slot=1.
	for _, s := range doc.Slots {
		if s.Slot == 1 {
			t.Errorf("file still contains slot 1 after clear: %+v", s)
		}
	}
}

func TestPresetStore_AddPersistenceFailureDoesNotMutateMemory(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	// Make one slot available with a successful write first.
	if _, err := st.SetStarred("cartoon-rewind", "loonytunes", false); err != nil {
		t.Fatalf("remove pre-condition: %v", err)
	}
	before := st.Snapshot()
	// Point the store at a directory so the final os.Rename file->dir fails.
	st.path = t.TempDir()
	if _, err := st.SetStarred("mtv-rewind", "amp", true); err == nil {
		t.Fatalf("SetStarred add with failing persistence returned nil error")
	}
	if after := st.Snapshot(); after != before {
		t.Errorf("snapshot mutated despite persistence failure\nafter=%+v\nbefore=%+v", after, before)
	}
}

func TestPresetStore_MovePersistenceFailureDoesNotMutateMemory(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	before := st.Snapshot()
	// Point the store at a directory so the final os.Rename file->dir fails.
	st.path = t.TempDir()
	if err := st.Move(1, 7); err == nil {
		t.Fatalf("Move with failing persistence returned nil error")
	}
	if after := st.Snapshot(); after != before {
		t.Errorf("snapshot mutated despite persistence failure\nafter=%+v\nbefore=%+v", after, before)
	}
}

func TestPresetStore_LoadStaleReferencesDropped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "chassis_presets.json")
	// Pre-seed file with a stale reference (provider doesn't exist) plus one valid entry.
	body := `{"version":1,"slots":[{"slot":1,"provider":"gone","channel":"x"},{"slot":2,"provider":"mtv-rewind","channel":"80s"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, err := newPresetStore(path, defaultCatalogResolver(t))
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	snap := st.Snapshot()
	if snap[0].ProviderID != "" {
		t.Errorf("snap[0] = %+v, want empty (stale ref dropped)", snap[0])
	}
	if snap[1].ProviderID != "mtv-rewind" || snap[1].ChannelID != "80s" {
		t.Errorf("snap[1] = %+v, want mtv-rewind/80s", snap[1])
	}
}

func TestPresetStore_LoadParseErrorFallsBackToBundled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "chassis_presets.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, err := newPresetStore(path, defaultCatalogResolver(t))
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	snap := st.Snapshot()
	filled := 0
	for _, e := range snap {
		if e.ProviderID != "" {
			filled++
		}
	}
	if filled != 12 {
		t.Errorf("parse-error fallback filled = %d, want 12 (bundled defaults)", filled)
	}
}

func TestPresetStore_SnapshotSlotsAre1Indexed(t *testing.T) {
	t.Parallel()
	st, _ := newStoreForTest(t)
	snap := st.Snapshot()
	for i, e := range snap {
		if e.Slot != i+1 {
			t.Errorf("snap[%d].Slot = %d, want %d", i, e.Slot, i+1)
		}
	}
}

func TestPresetStore_NoOpRemoveDoesNotWriteFile(t *testing.T) {
	t.Parallel()
	st, path := newStoreForTest(t)
	if _, err := st.SetStarred("mtv-rewind", "amp", false); err != nil {
		t.Fatalf("remove absent: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("no-op remove should not write file")
	}
	_ = sort.Search // import sentinel; can be removed once tests grow
	_ = context.Background
}

func newPresetStoreForCleanup(t *testing.T) *presetStore {
	t.Helper()
	resolve := func(providerID, channelID string) (adapters.PresetEntry, bool) {
		// Resolve any user:* channel to a populated entry; unknown → stale.
		if isUserProviderID(providerID) {
			return adapters.PresetEntry{ProviderID: providerID, ChannelID: channelID, Title: channelID + " title", BadgeLabel: "UX", BadgeClass: "u-teal"}, true
		}
		return adapters.PresetEntry{}, false
	}
	// Pre-create an empty-slots file so seedFromBundled is skipped (the
	// bundled defaults use non-user providers unknown to our resolver, which
	// would otherwise fill all 12 slots via the stale-entry fallback path).
	path := filepath.Join(t.TempDir(), "chassis_presets.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"slots":[]}`), 0o600); err != nil {
		t.Fatalf("pre-create empty presets file: %v", err)
	}
	st, err := newPresetStore(path, resolve)
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	return st
}

func TestPresetStore_ClearProviderSlots(t *testing.T) {
	t.Parallel()
	st := newPresetStoreForCleanup(t)
	if _, err := st.SetStarred("user:mix", "a", true); err != nil {
		t.Fatalf("star a: %v", err)
	}
	if _, err := st.SetStarred("user:mix", "b", true); err != nil {
		t.Fatalf("star b: %v", err)
	}
	if _, err := st.SetStarred("user:other", "c", true); err != nil {
		t.Fatalf("star c: %v", err)
	}
	cleared, err := st.ClearProviderSlots("user:mix")
	if err != nil {
		t.Fatalf("ClearProviderSlots: %v", err)
	}
	if len(cleared) != 2 {
		t.Fatalf("cleared = %v, want 2 slots", cleared)
	}
	// user:other survives.
	snap := st.Snapshot()
	count := 0
	for _, e := range snap {
		if e.ProviderID != "" {
			count++
			if e.ProviderID == "user:mix" {
				t.Fatalf("user:mix slot survived: %+v", e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("surviving slots = %d, want 1 (user:other)", count)
	}
}

func TestPresetStore_ClearChannelSlots(t *testing.T) {
	t.Parallel()
	st := newPresetStoreForCleanup(t)
	if _, err := st.SetStarred("user:mix", "keep", true); err != nil {
		t.Fatalf("star keep: %v", err)
	}
	if _, err := st.SetStarred("user:mix", "drop", true); err != nil {
		t.Fatalf("star drop: %v", err)
	}
	cleared, err := st.ClearChannelSlots("user:mix", "drop")
	if err != nil {
		t.Fatalf("ClearChannelSlots: %v", err)
	}
	if len(cleared) != 1 {
		t.Fatalf("cleared = %v, want 1", cleared)
	}
	for _, e := range st.Snapshot() {
		if e.ProviderID == "user:mix" && e.ChannelID == "drop" {
			t.Fatal("dropped channel slot survived")
		}
	}
}

func TestPresetStore_RehydrateRefreshesDisplayFields(t *testing.T) {
	t.Parallel()
	title := "old"
	resolve := func(providerID, channelID string) (adapters.PresetEntry, bool) {
		if providerID != "user:mix" {
			return adapters.PresetEntry{}, false
		}
		return adapters.PresetEntry{ProviderID: providerID, ChannelID: channelID, Title: title, BadgeLabel: "MX", BadgeClass: "u-teal"}, true
	}
	// Pre-create an empty-slots file so seedFromBundled is skipped.
	path := filepath.Join(t.TempDir(), "chassis_presets.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"slots":[]}`), 0o600); err != nil {
		t.Fatalf("pre-create empty presets file: %v", err)
	}
	st, err := newPresetStore(path, resolve)
	if err != nil {
		t.Fatalf("newPresetStore: %v", err)
	}
	if _, err := st.SetStarred("user:mix", "a", true); err != nil {
		t.Fatalf("star: %v", err)
	}
	title = "new" // simulate a rename reflected by the rebuilt catalog
	st.Rehydrate()
	got := ""
	for _, e := range st.Snapshot() {
		if e.ProviderID == "user:mix" && e.ChannelID == "a" {
			got = e.Title
		}
	}
	if got != "new" {
		t.Fatalf("rehydrated title = %q, want new", got)
	}
}
