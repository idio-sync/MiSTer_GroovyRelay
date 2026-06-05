package streams

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// catalogResolver maps (providerID, channelID) to a fully populated
// PresetEntry (or returns ok=false for stale references). The store
// uses this to:
//   1. Hydrate display fields on load.
//   2. Validate SetStarred(true) inputs before mutating.
type catalogResolver func(providerID, channelID string) (adapters.PresetEntry, bool)

// persistedPresetIO is the on-disk representation of one slot. Display
// fields (Title/BadgeLabel/BadgeClass/Live) are NOT persisted — they
// derive from the catalog at load time. Only the persistent triple
// (Slot, Provider, Channel) is stable across catalog reloads.
type persistedPresetIO struct {
	Slot     int    `json:"slot"`
	Provider string `json:"provider"`
	Channel  string `json:"channel"`
}

type persistedFile struct {
	Version int                 `json:"version"`
	Slots   []persistedPresetIO `json:"slots"`
}

const presetStoreFileVersion = 1

// presetStore owns the 12-slot in-memory preset array and the file
// persistence side-effect. All mutation methods return values that the
// HTTP handler can shape into the wire envelope; the store itself does
// not know about HTTP.
type presetStore struct {
	mu       sync.Mutex
	path     string
	resolve  catalogResolver
	slots    [12]adapters.PresetEntry // index = slot-1
	saveErrs *onePerInstanceLog       // bounded log noise for save errors
}

// newPresetStore reads the file at path (if present), validates each
// entry against the resolver (dropping stale references with an info
// log), and returns a populated store. A missing or unreadable file
// seeds from bundledChassisPresets in memory without writing the file —
// lazy write occurs on first successful mutation.
func newPresetStore(path string, resolve catalogResolver) (*presetStore, error) {
	st := &presetStore{
		path:     path,
		resolve:  resolve,
		saveErrs: &onePerInstanceLog{},
	}
	for i := 0; i < 12; i++ {
		st.slots[i] = adapters.PresetEntry{Slot: i + 1}
	}

	body, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.seedFromBundled()
		return st, nil
	case err != nil:
		// Non-NotExist read failure (permissions, etc.): seed from bundled
		// and log. Don't fail adapter init — the chassis must still serve.
		slog.Warn("chassis_presets: read failed; using bundled defaults", "err", err, "path", path)
		st.seedFromBundled()
		return st, nil
	}

	var doc persistedFile
	if err := json.Unmarshal(body, &doc); err != nil {
		slog.Warn("chassis_presets: parse failed; using bundled defaults", "err", err, "path", path)
		st.seedFromBundled()
		return st, nil
	}
	if doc.Version != presetStoreFileVersion {
		slog.Warn("chassis_presets: version mismatch; using bundled defaults",
			"got", doc.Version, "want", presetStoreFileVersion, "path", path)
		st.seedFromBundled()
		return st, nil
	}

	dropped := []string{}
	for _, p := range doc.Slots {
		if p.Slot < 1 || p.Slot > 12 {
			continue
		}
		if entry, ok := resolve(p.Provider, p.Channel); ok {
			entry.Slot = p.Slot
			st.slots[p.Slot-1] = entry
		} else {
			dropped = append(dropped, fmt.Sprintf("%s:%s", p.Provider, p.Channel))
		}
	}
	if len(dropped) > 0 {
		slog.Info("chassis_presets: dropped stale references on startup",
			"count", len(dropped), "refs", dropped)
	}
	return st, nil
}

// seedFromBundled fills the in-memory slots from the bundled preset
// literal without writing to disk. Lazy-write semantics: the file is
// created only when the user's first mutation succeeds.
func (s *presetStore) seedFromBundled() {
	for i, p := range bundledChassisPresets {
		// Re-derive display fields via the resolver so badge metadata
		// stays consistent with the catalog (defense against assets.go
		// drift between bundledChassisPresets and the manifest).
		entry, ok := s.resolve(p.ProviderID, p.ChannelID)
		if !ok {
			// Stale bundled entry — keep the persistent triple but show
			// the literal fields. Should not happen if assets.go is
			// internally consistent.
			s.slots[i] = p
			continue
		}
		entry.Slot = i + 1
		s.slots[i] = entry
	}
}

// Snapshot returns a value copy of the 12-slot array. The chassis
// reads this once per snapshot tick — keep it allocation-light.
func (s *presetStore) Snapshot() [12]adapters.PresetEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slots
}

// SetStarred applies the user's desired-state intent for one channel.
// Returns the slot index (when starred) or the list of cleared slots
// (when unstarred). Idempotent: starring an existing channel returns
// the current slot without writing; unstarring an absent channel
// returns empty Cleared without writing.
func (s *presetStore) SetStarred(providerID, channelID string, starred bool) (adapters.PresetStarResult, error) {
	entry, ok := s.resolve(providerID, channelID)
	if !ok {
		return adapters.PresetStarResult{}, &adapters.QuickCastError{
			Status:  http.StatusNotFound,
			Chip:    "NOT FOUND",
			Message: fmt.Sprintf("streams: %s:%s not in catalog", providerID, channelID),
		}
	}
	if starred {
		s.mu.Lock()
		// Already in some slot? Return that slot, no write.
		for i, e := range s.slots {
			if e.ProviderID == providerID && e.ChannelID == channelID {
				s.mu.Unlock()
				return adapters.PresetStarResult{Starred: true, Slot: i + 1}, nil
			}
		}
		// Find first empty slot.
		target := -1
		for i, e := range s.slots {
			if e.ProviderID == "" {
				target = i
				break
			}
		}
		if target < 0 {
			s.mu.Unlock()
			return adapters.PresetStarResult{}, &adapters.QuickCastError{
				Status:  http.StatusConflict,
				Chip:    "BANK FULL",
				Message: "streams: preset bank is full",
			}
		}
		entry.Slot = target + 1
		next := s.slots
		next[target] = entry
		if err := s.persistSlotsLocked(next); err != nil {
			s.mu.Unlock()
			return adapters.PresetStarResult{}, err
		}
		s.slots = next
		s.mu.Unlock()
		return adapters.PresetStarResult{Starred: true, Slot: target + 1}, nil
	}

	// starred=false: clear all matching slots.
	s.mu.Lock()
	next := s.slots
	cleared := []int{}
	for i, e := range next {
		if e.ProviderID == providerID && e.ChannelID == channelID {
			cleared = append(cleared, i+1)
			next[i] = adapters.PresetEntry{Slot: i + 1}
		}
	}
	if len(cleared) > 0 {
		if err := s.persistSlotsLocked(next); err != nil {
			s.mu.Unlock()
			return adapters.PresetStarResult{}, err
		}
		s.slots = next
	}
	s.mu.Unlock()
	sort.Ints(cleared)
	return adapters.PresetStarResult{Starred: false, Cleared: cleared}, nil
}

// Move swaps the contents of two slots. from==to is a no-op success
// (no file write). Either slot may be empty — swap semantics still
// apply, the empty slot becomes filled and vice versa.
func (s *presetStore) Move(from, to int) error {
	if from < 1 || from > 12 || to < 1 || to > 12 {
		return &adapters.QuickCastError{
			Status:  http.StatusBadRequest,
			Chip:    "BAD SLOT",
			Message: fmt.Sprintf("streams: move(%d,%d): slots must be 1..12", from, to),
		}
	}
	if from == to {
		return nil
	}
	s.mu.Lock()
	next := s.slots
	next[from-1], next[to-1] = next[to-1], next[from-1]
	next[from-1].Slot = from
	next[to-1].Slot = to
	if err := s.persistSlotsLocked(next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.slots = next
	s.mu.Unlock()
	return nil
}

// ClearProviderSlots clears every slot whose ProviderID == providerID,
// persists, and returns the cleared slot numbers (1-indexed, ascending).
// Unlike SetStarred it does NOT consult the resolver, so it works after the
// provider/channel has already left the catalog (delete-with-cleanup, spec
// §9 item 8). A no-match is a no-op success (nil, nil) with no file write.
func (s *presetStore) ClearProviderSlots(providerID string) ([]int, error) {
	return s.clearMatching(func(e adapters.PresetEntry) bool {
		return e.ProviderID == providerID
	})
}

// ClearChannelSlots clears slots matching the full (providerID, channelID)
// pair — channel IDs are provider-scoped (spec §4.5), so a same-named channel
// under a different provider is never touched.
func (s *presetStore) ClearChannelSlots(providerID, channelID string) ([]int, error) {
	return s.clearMatching(func(e adapters.PresetEntry) bool {
		return e.ProviderID == providerID && e.ChannelID == channelID
	})
}

func (s *presetStore) clearMatching(match func(adapters.PresetEntry) bool) ([]int, error) {
	s.mu.Lock()
	next := s.slots
	cleared := []int{}
	for i, e := range next {
		if e.ProviderID != "" && match(e) {
			cleared = append(cleared, i+1)
			next[i] = adapters.PresetEntry{Slot: i + 1}
		}
	}
	if len(cleared) == 0 {
		s.mu.Unlock()
		return nil, nil
	}
	if err := s.persistSlotsLocked(next); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.slots = next
	s.mu.Unlock()
	sort.Ints(cleared)
	return cleared, nil
}

// Rehydrate re-resolves the display fields (Title/BadgeLabel/BadgeClass/Live)
// of every populated slot against the current catalog, so an in-app rename
// reflects in the preset bank without a restart (spec §10 "re-derive
// presets"). It is NON-destructive: a slot whose reference no longer resolves
// (e.g. a playlist channel mid-enumeration) keeps its persistent triple and
// last-known display fields rather than being dropped — explicit removals go
// through ClearProviderSlots/ClearChannelSlots. The persistent triple is
// unchanged, so no file write is needed.
func (s *presetStore) Rehydrate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.slots {
		if e.ProviderID == "" {
			continue
		}
		if fresh, ok := s.resolve(e.ProviderID, e.ChannelID); ok {
			fresh.Slot = e.Slot
			s.slots[i] = fresh
		}
	}
}

// persistSlotsLocked writes the candidate state to s.path atomically
// (temp file in the same directory + os.Rename). Called with s.mu held.
// Callers commit s.slots only after this returns nil, so HTTP success
// and SSE updates never acknowledge a mutation that failed to persist.
func (s *presetStore) persistSlotsLocked(slots [12]adapters.PresetEntry) error {
	doc := persistedFile{Version: presetStoreFileVersion}
	for _, e := range slots {
		if e.ProviderID == "" {
			continue
		}
		doc.Slots = append(doc.Slots, persistedPresetIO{
			Slot:     e.Slot,
			Provider: e.ProviderID,
			Channel:  e.ChannelID,
		})
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		s.saveErrs.Warn("chassis_presets: marshal failed", "err", err)
		return fmt.Errorf("chassis_presets: marshal: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, "chassis_presets-*.json.tmp")
	if err != nil {
		s.saveErrs.Warn("chassis_presets: create temp failed", "err", err, "dir", dir)
		return fmt.Errorf("chassis_presets: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		s.saveErrs.Warn("chassis_presets: write temp failed", "err", err, "path", tmpPath)
		return fmt.Errorf("chassis_presets: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		s.saveErrs.Warn("chassis_presets: close temp failed", "err", err, "path", tmpPath)
		return fmt.Errorf("chassis_presets: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		s.saveErrs.Warn("chassis_presets: rename failed", "err", err, "src", tmpPath, "dst", s.path)
		return fmt.Errorf("chassis_presets: rename: %w", err)
	}
	return nil
}

// onePerInstanceLog is a tiny rate limiter so a misconfigured data
// dir doesn't spam the slog on every preset edit.
type onePerInstanceLog struct {
	once sync.Once
}

func (o *onePerInstanceLog) Warn(msg string, kv ...any) {
	o.once.Do(func() { slog.Warn(msg, kv...) })
}
