package localfiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const companionHistoryMaxEntries = 10
const companionHistorySchemaVersion = 1

type companionHistoryFile struct {
	Version int                     `json:"version"`
	Entries []companionHistoryEntry `json:"entries"`
}

type companionHistoryEntry struct {
	ID         string    `json:"id"`
	Library    string    `json:"library"`
	Rel        string    `json:"rel"`
	Title      string    `json:"title,omitempty"`
	LastPlayed time.Time `json:"last_played"`
}

type companionHistoryError struct {
	status int
	msg    string
}

func (e companionHistoryError) Error() string   { return e.msg }
func (e companionHistoryError) HTTPStatus() int { return e.status }

func (a *Adapter) recordCompanionHistory(libName, rel, title string, at time.Time) {
	libName = strings.TrimSpace(libName)
	rel = cleanHistoryRel(rel)
	title = strings.TrimSpace(title)
	if libName == "" || rel == "" {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for i, entry := range a.history {
		if entry.Library == libName && entry.Rel == rel {
			entry.Title = title
			entry.LastPlayed = at
			a.history = append([]companionHistoryEntry{entry}, append(a.history[:i], a.history[i+1:]...)...)
			a.saveCompanionHistoryLocked()
			return
		}
	}
	idPart, err := randHex8()
	if err != nil {
		return
	}
	entry := companionHistoryEntry{
		ID:         "h_" + idPart,
		Library:    libName,
		Rel:        rel,
		Title:      title,
		LastPlayed: at,
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
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			title = titleFromPath(entry.Rel)
		}
		out = append(out, companion.CompanionHistoryEntry{
			ID:         entry.ID,
			Title:      title,
			URLDisplay: entry.Library + "/" + entry.Rel,
			LastPlayed: entry.LastPlayed,
		})
	}
	return out
}

func (a *Adapter) CompanionHistoryPlay(ctx context.Context, id string) (companion.CompanionPlayResult, error) {
	id = strings.TrimSpace(id)
	a.mu.Lock()
	var entry companionHistoryEntry
	for _, candidate := range a.history {
		if candidate.ID == id {
			entry = candidate
			break
		}
	}
	a.mu.Unlock()
	if entry.ID == "" {
		return companion.CompanionPlayResult{}, companionHistoryError{
			status: http.StatusNotFound,
			msg:    fmt.Sprintf("localfiles history entry %q not found", id),
		}
	}
	if err := a.Cast(ctx, entry.Library, entry.Rel); err != nil {
		return companion.CompanionPlayResult{}, err
	}
	return companion.CompanionPlayResult{State: core.StatePlaying}, nil
}

func cleanHistoryRel(rel string) string {
	rel = filepath.Clean(strings.TrimSpace(rel))
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func companionHistoryPath(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "localfiles_history.json")
}

func loadCompanionHistory(dataDir string) ([]companionHistoryEntry, string) {
	path := companionHistoryPath(dataDir)
	if path == "" {
		return nil, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("localfiles history load: file unreadable, starting empty", "path", path, "err", err)
		}
		return nil, path
	}
	var hf companionHistoryFile
	if err := json.Unmarshal(data, &hf); err != nil {
		slog.Warn("localfiles history load: corrupt JSON, starting empty", "path", path, "err", err)
		return nil, path
	}
	if hf.Version != companionHistorySchemaVersion {
		slog.Warn("localfiles history load: unknown version, starting empty",
			"version", hf.Version, "want", companionHistorySchemaVersion, "path", path)
		return nil, path
	}
	entries := compactCompanionHistory(hf.Entries)
	return entries, path
}

func compactCompanionHistory(entries []companionHistoryEntry) []companionHistoryEntry {
	out := make([]companionHistoryEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entry.Library = strings.TrimSpace(entry.Library)
		entry.Rel = cleanHistoryRel(entry.Rel)
		entry.Title = strings.TrimSpace(entry.Title)
		if entry.ID == "" || entry.Library == "" || entry.Rel == "" || entry.LastPlayed.IsZero() {
			continue
		}
		key := entry.Library + "\x00" + entry.Rel
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
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
		slog.Warn("localfiles history save: marshal failed", "err", err)
		return
	}
	if err := config.WriteAtomic(a.historyPath, data); err != nil {
		slog.Warn("localfiles history save: disabling persistence after first failure",
			"path", a.historyPath, "err", err)
		a.historyPersistDisabled = true
	}
}
