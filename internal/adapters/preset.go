package adapters

import "context"

// PresetEntry is one slot in the chassis preset bank — a reference to
// a specific streams catalog entry plus the display metadata the
// chassis needs to render it.
//
// 3A: produced exclusively by the streams adapter's BundledPresets.
// Future per-source preset banks may produce these too, but only
// streams is registered as a PresetViewer in 3A.
type PresetEntry struct {
	Slot       int    // 1..12, 1-indexed to match the mockup
	ProviderID string // e.g. "mtv-rewind"
	ChannelID  string // e.g. "1stday"
	Title      string // "First Day on MTV" — rendered in the slot's name line
	BadgeLabel string // "MTV REWIND" — rendered in the slot's badge line
	BadgeClass string // "mtv" | "cartoon" | "toonami" — CSS hook for badge color
	Live       bool   // matches mockup `.preset.live` — always-on live channels
}

// PresetViewer returns the 12-slot preset bank snapshot. The chassis
// reads this once per page render. 3A treats the result as static for
// the lifetime of the bridge process; future user-edit specs may
// expose a notification channel.
type PresetViewer interface {
	BundledPresets() [12]PresetEntry
}

// PresetCaster fires a cast for a specific preset slot. Implementations
// look up the slot's catalog entry from their own state and start the
// appropriate session.
//
// Slot is 1-indexed (1..12) to match the URL path parameter and the
// mockup. Implementations MUST return a non-nil error for slots
// outside this range; chassis validates first as defense-in-depth.
type PresetCaster interface {
	CastPreset(ctx context.Context, slot int) error
}
