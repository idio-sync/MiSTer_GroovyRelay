package streams

import (
	"context"
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

// Presets returns the current 12-slot chassis preset bank snapshot
// from the store. Display fields are re-derived from the catalog at
// load time; only the persistent triple lives on disk.
func (a *Adapter) Presets() [12]adapters.PresetEntry {
	if a.presetStore == nil {
		return bundledChassisPresets
	}
	return a.presetStore.Snapshot()
}

// SetPresetStarred is the desired-state preset edit hook. The chassis
// HTTP handler validates inputs (non-empty provider/channel, strict
// "true"/"false" lexical form for starred) before forwarding here.
func (a *Adapter) SetPresetStarred(ctx context.Context, providerID, channelID string, starred bool) (adapters.PresetStarResult, error) {
	if a.presetStore == nil {
		return adapters.PresetStarResult{}, fmt.Errorf("streams: preset store not initialized")
	}
	return a.presetStore.SetStarred(providerID, channelID, starred)
}

// MovePreset swaps two slot contents. Out-of-range returns *QuickCastError{400, BAD SLOT}.
func (a *Adapter) MovePreset(ctx context.Context, from, to int) error {
	if a.presetStore == nil {
		return fmt.Errorf("streams: preset store not initialized")
	}
	return a.presetStore.Move(from, to)
}

// CastPreset starts a Streams cast for slot N (1-indexed). Reads the
// slot's (provider, channel) from the live store snapshot rather than
// the bundledChassisPresets literal so user edits take effect.
func (a *Adapter) CastPreset(ctx context.Context, slot int) error {
	if slot < 1 || slot > 12 {
		return &adapters.QuickCastError{
			Status:  http.StatusBadRequest,
			Chip:    "BAD SLOT",
			Message: fmt.Sprintf("streams: preset slot %d out of range", slot),
		}
	}
	if err := a.ensureStartupSnapshot(ctx); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams catalog is not ready",
			Cause:   err,
		}
	}
	snap := a.Presets()
	entry := snap[slot-1]
	if entry.ProviderID == "" {
		return &adapters.QuickCastError{
			Status:  http.StatusNotFound,
			Chip:    "NOT FOUND",
			Message: fmt.Sprintf("streams: preset slot %d is empty", slot),
		}
	}
	res := streamhandoff.Resolution{
		ProviderID: entry.ProviderID,
		ChannelID:  entry.ChannelID,
	}
	if err := a.validatePlayRequest(res); err != nil {
		return err
	}
	_, err := a.StartResolvedStream(ctx, res)
	return err
}
