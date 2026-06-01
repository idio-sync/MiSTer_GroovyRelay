package plex

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	companionapi "github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const companionHistorySchemaVersion = 1

type companionHistoryFile struct {
	Version int                                  `json:"version"`
	Entries []companionapi.CompanionHistoryEntry `json:"entries"`
}

func companionHistoryPath(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "plex_history.json")
}

func loadCompanionHistory(dataDir string) ([]companionapi.CompanionHistoryEntry, string) {
	path := companionHistoryPath(dataDir)
	if path == "" {
		return nil, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("plex history load: file unreadable, starting empty", "path", path, "err", err)
		}
		return nil, path
	}
	var hf companionHistoryFile
	if err := json.Unmarshal(data, &hf); err != nil {
		slog.Warn("plex history load: corrupt JSON, starting empty", "path", path, "err", err)
		return nil, path
	}
	if hf.Version != companionHistorySchemaVersion {
		slog.Warn("plex history load: unknown version, starting empty",
			"version", hf.Version, "want", companionHistorySchemaVersion, "path", path)
		return nil, path
	}
	return compactCompanionHistory(hf.Entries), path
}

func compactCompanionHistory(entries []companionapi.CompanionHistoryEntry) []companionapi.CompanionHistoryEntry {
	out := make([]companionapi.CompanionHistoryEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entry.Title = strings.TrimSpace(entry.Title)
		entry.URLDisplay = strings.TrimSpace(entry.URLDisplay)
		if entry.Title == "" || entry.URLDisplay == "" || entry.LastPlayed.IsZero() {
			continue
		}
		if _, ok := seen[entry.URLDisplay]; ok {
			continue
		}
		seen[entry.URLDisplay] = struct{}{}
		out = append(out, entry)
		if len(out) == companionHistoryMaxEntries {
			break
		}
	}
	return out
}

func (c *Companion) saveCompanionHistoryLocked() {
	if c.historyPath == "" || c.historyPersistDisabled {
		return
	}
	data, err := json.Marshal(companionHistoryFile{
		Version: companionHistorySchemaVersion,
		Entries: c.history,
	})
	if err != nil {
		slog.Warn("plex history save: marshal failed", "err", err)
		return
	}
	if err := config.WriteAtomic(c.historyPath, data); err != nil {
		slog.Warn("plex history save: disabling persistence after first failure",
			"path", c.historyPath, "err", err)
		c.historyPersistDisabled = true
	}
}
