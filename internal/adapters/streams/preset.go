package streams

import (
	"context"
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

// BundledPresets returns the 12 default chassis preset slots. The
// list is constant for the adapter's lifetime; 3A does not support
// editing. CastPreset (in this file) consumes the same array.
func (a *Adapter) BundledPresets() [12]adapters.PresetEntry {
	return bundledChassisPresets
}

// CastPreset starts a Streams cast for slot N (1-indexed). The slot's
// ProviderID/ChannelID come from bundledChassisPresets. Returns a typed
// *adapters.QuickCastError for surfaces that need HTTP status + chip
// text; untyped errors collapse to 500/CAST FAILED via the chassis
// fallback.
func (a *Adapter) CastPreset(ctx context.Context, slot int) error {
	if slot < 1 || slot > 12 {
		// Typed error so any caller bypassing the chassis-side validation
		// (tests, future callers) still gets the spec's 400/BAD SLOT
		// response instead of collapsing to 500/CAST FAILED.
		return &adapters.QuickCastError{
			Status:  http.StatusBadRequest,
			Chip:    "BAD SLOT",
			Message: fmt.Sprintf("streams: preset slot %d out of range", slot),
		}
	}
	// main.go binds and serves HTTP before adapter Start(ctx) runs, so
	// preset clicks can arrive before catalogs are populated. Guard with
	// the existing ensureStartupSnapshot path and surface a typed
	// QuickCastError so the chassis emits 503/NOT READY.
	if err := a.ensureStartupSnapshot(ctx); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams catalog is not ready",
			Cause:   err,
		}
	}
	entry := bundledChassisPresets[slot-1]
	res := streamhandoff.Resolution{
		ProviderID: entry.ProviderID,
		ChannelID:  entry.ChannelID,
	}
	if err := a.validatePlayRequest(res); err != nil {
		// validatePlayRequest errors here represent adapter-coding bugs
		// (a slot pointing to a non-existent channel), not user-facing
		// failures. They collapse to 500/CAST FAILED via the chassis's
		// errors.As fallback. preset_test.go asserts every slot resolves.
		return err
	}
	_, err := a.StartResolvedStream(ctx, res)
	return err
}
