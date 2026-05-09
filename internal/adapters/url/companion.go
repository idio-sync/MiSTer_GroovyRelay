package url

import (
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

// CompanionHistory returns redacted point-in-time history snapshots for
// the browser extension. Raw URLs never cross the internal/ui boundary.
func (a *Adapter) CompanionHistory() []ui.CompanionHistoryEntry {
	history := a.history.List()
	out := make([]ui.CompanionHistoryEntry, 0, len(history))
	for _, e := range history {
		out = append(out, ui.CompanionHistoryEntry{
			ID:         e.ID,
			Title:      e.Title,
			URLDisplay: redactURL(e.URL),
			LastPlayed: e.LastPlayedAt,
		})
	}
	return out
}

// CompanionLastURLDisplay returns the most recent URL in redacted display
// form. Empty string means there is nothing safe/useful to show.
func (a *Adapter) CompanionLastURLDisplay() string {
	raw := a.snapshotLastURL()
	if raw == "" {
		return ""
	}
	return redactURL(raw)
}

// CompanionDisplay enriches URL-owned core sessions with the URL adapter's
// title/history knowledge. Foreign adapter refs intentionally return zero so
// internal/ui can fall back to the registry display name.
func (a *Adapter) CompanionDisplay(adapterRef string) ui.CompanionSessionDisplay {
	if !strings.HasPrefix(adapterRef, "url:") {
		return ui.CompanionSessionDisplay{}
	}
	raw := a.snapshotLastURL()
	display := ui.CompanionSessionDisplay{
		AdapterName:   a.DisplayName(),
		SourceDisplay: a.CompanionLastURLDisplay(),
	}
	if raw == "" {
		return display
	}
	key := dedupeKey(raw)
	for _, e := range a.history.List() {
		if dedupeKey(e.URL) == key {
			display.Title = e.Title
			break
		}
	}
	return display
}
