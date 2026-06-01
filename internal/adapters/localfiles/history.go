package localfiles

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const companionHistoryMaxEntries = 10

type companionHistoryEntry struct {
	ID         string
	Library    string
	Rel        string
	Title      string
	LastPlayed time.Time
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
