package jellyfin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const companionHistoryMaxEntries = 10
const companionHistorySchemaVersion = 1

type companionHistoryFile struct {
	Version int                     `json:"version"`
	Entries []companionHistoryEntry `json:"entries"`
}

type companionHistoryEntry struct {
	ItemID     string    `json:"item_id"`
	Title      string    `json:"title"`
	LastPlayed time.Time `json:"last_played"`
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
			a.saveCompanionHistoryLocked()
			return
		}
	}
	a.history = append([]companionHistoryEntry{entry}, a.history...)
	if len(a.history) > companionHistoryMaxEntries {
		a.history = a.history[:companionHistoryMaxEntries]
	}
	a.saveCompanionHistoryLocked()
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

func companionHistoryPath(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "jellyfin_history.json")
}

func loadCompanionHistory(dataDir string) ([]companionHistoryEntry, string) {
	path := companionHistoryPath(dataDir)
	if path == "" {
		return nil, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("jellyfin history load: file unreadable, starting empty", "path", path, "err", err)
		}
		return nil, path
	}
	var hf companionHistoryFile
	if err := json.Unmarshal(data, &hf); err != nil {
		slog.Warn("jellyfin history load: corrupt JSON, starting empty", "path", path, "err", err)
		return nil, path
	}
	if hf.Version != companionHistorySchemaVersion {
		slog.Warn("jellyfin history load: unknown version, starting empty",
			"version", hf.Version, "want", companionHistorySchemaVersion, "path", path)
		return nil, path
	}
	return compactCompanionHistory(hf.Entries), path
}

func compactCompanionHistory(entries []companionHistoryEntry) []companionHistoryEntry {
	out := make([]companionHistoryEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entry.ItemID = strings.TrimSpace(entry.ItemID)
		entry.Title = strings.TrimSpace(entry.Title)
		if entry.ItemID == "" || entry.Title == "" || entry.LastPlayed.IsZero() {
			continue
		}
		if _, ok := seen[entry.ItemID]; ok {
			continue
		}
		seen[entry.ItemID] = struct{}{}
		out = append(out, entry)
		if len(out) == companionHistoryMaxEntries {
			break
		}
	}
	return out
}

func (a *Adapter) saveCompanionHistoryLocked() {
	if a.historyPath == "" || a.historyPersistDisabled {
		return
	}
	data, err := json.Marshal(companionHistoryFile{
		Version: companionHistorySchemaVersion,
		Entries: a.history,
	})
	if err != nil {
		slog.Warn("jellyfin history save: marshal failed", "err", err)
		return
	}
	if err := config.WriteAtomic(a.historyPath, data); err != nil {
		slog.Warn("jellyfin history save: disabling persistence after first failure",
			"path", a.historyPath, "err", err)
		a.historyPersistDisabled = true
	}
}
