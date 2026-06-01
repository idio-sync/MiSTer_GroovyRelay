package jellyfin

import (
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
)

const companionHistoryMaxEntries = 10

type companionHistoryEntry struct {
	ItemID     string
	Title      string
	LastPlayed time.Time
}

func (a *Adapter) recordCompanionHistory(itemID string, info PlaybackInfoResult, at time.Time) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return
	}
	title := strings.TrimSpace(jellyfinDisplayMetadata(info).Primary)
	if title == "" {
		title = strings.TrimSpace(info.Title)
	}
	if title == "" {
		title = itemID
	}
	entry := companionHistoryEntry{ItemID: itemID, Title: title, LastPlayed: at}

	a.mu.Lock()
	defer a.mu.Unlock()
	for i, existing := range a.history {
		if existing.ItemID == itemID {
			a.history = append([]companionHistoryEntry{entry}, append(a.history[:i], a.history[i+1:]...)...)
			return
		}
	}
	a.history = append([]companionHistoryEntry{entry}, a.history...)
	if len(a.history) > companionHistoryMaxEntries {
		a.history = a.history[:companionHistoryMaxEntries]
	}
}

func (a *Adapter) CompanionHistory() []companion.CompanionHistoryEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]companion.CompanionHistoryEntry, 0, len(a.history))
	for _, entry := range a.history {
		out = append(out, companion.CompanionHistoryEntry{
			Title:      entry.Title,
			URLDisplay: entry.ItemID,
			LastPlayed: entry.LastPlayed,
		})
	}
	return out
}
