package adapters

import "context"

// PresetEntry is one slot in the chassis preset bank — a reference to
// a specific streams catalog entry plus the display metadata the
// chassis needs to render it.
//
// Display fields (Title, BadgeLabel, BadgeClass, Live) are derived
// from the catalog at load time and refreshed on each catalog reload;
// the persistent (Slot, ProviderID, ChannelID) triple is the only data
// written to {data_dir}/chassis_presets.json.
type PresetEntry struct {
	Slot       int    // 1..12, 1-indexed to match the mockup
	ProviderID string // e.g. "mtv-rewind"
	ChannelID  string // e.g. "1stday"
	Title      string // "First Day on MTV" — derived from catalog
	BadgeLabel string // "MTV REWIND" — derived from catalog
	BadgeClass string // "mtv" | "cartoon" | "toonami" — derived from catalog
	Live       bool   // derived from provider/channel Live flag
}

// PresetViewer returns the 12-slot preset bank snapshot. 3B backs this
// with a mutable file-persisted store; the method name no longer
// implies "bundled" because users can edit the slots.
type PresetViewer interface {
	Presets() [12]PresetEntry
}

// PresetCaster fires a cast for a specific preset slot. Implementations
// look up the slot's catalog entry from their own state.
//
// Slot is 1-indexed (1..12). Implementations MUST return a non-nil
// error for slots outside this range as defense-in-depth.
type PresetCaster interface {
	CastPreset(ctx context.Context, slot int) error
}

// PresetEditor mutates the 12-slot preset bank. The chassis HTTP
// handler translates user actions (catalog star click, drag-reorder)
// into these calls.
//
// SetPresetStarred is idempotent in the desired-state sense:
//   - starred=true, channel present → no-op, return current slot
//   - starred=true, channel absent → write to first empty slot
//   - starred=true, all 12 slots full → *QuickCastError{409, BANK FULL}
//   - starred=false, channel present → clear all matching slots
//   - starred=false, channel absent → no-op
//
// MovePreset is swap semantics: from and to trade. from==to is a
// no-op success. from or to outside 1..12 → *QuickCastError{400, BAD SLOT}.
type PresetEditor interface {
	SetPresetStarred(ctx context.Context, providerID, channelID string, starred bool) (PresetStarResult, error)
	MovePreset(ctx context.Context, from, to int) error
}

// PresetStarResult is the typed return from PresetEditor.SetPresetStarred.
// Zero-value rules (enforced by callers, not the type):
//   - Starred=true:  Slot in 1..12, Cleared MUST be nil.
//   - Starred=false: Slot MUST be 0, Cleared MAY be empty (no-op remove)
//                    or populated. The JSON tags use omitempty so the
//                    wire never carries stale fields.
type PresetStarResult struct {
	Starred bool  `json:"starred"`
	Slot    int   `json:"slot,omitempty"`
	Cleared []int `json:"cleared,omitempty"`
}
