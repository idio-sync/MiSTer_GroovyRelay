package url

import (
	"encoding/json"
	"errors"
	"log/slog"
	stdurl "net/url"
	"os"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// dedupeKey returns the URL with all userinfo (username AND password)
// stripped. Used as the history dedupe key so that re-casts with
// different credentials collapse to one entry. Returns "" if the URL
// fails to parse — callers should reject this case before calling
// AddOrBump (handlePlay's upstream validation already does).
//
// Why not redactURL()? net/url.URL.Redacted() blanks only the
// password (xxxxx), leaving the username intact. Two URLs with
// different usernames would produce different keys despite redacting
// to nearly-identical strings. Stripping userinfo entirely gives the
// stronger guarantee the spec requires.
func dedupeKey(rawURL string) string {
	u, err := stdurl.Parse(rawURL)
	if err != nil || u == nil {
		return ""
	}
	u.User = nil
	return u.String()
}

const (
	historyMaxEntries    = 10
	historySchemaVersion = 1
)

// HistoryEntry is one row of the on-disk history file. JSON tags match
// the schema in spec §"History / Schema".
type HistoryEntry struct {
	URL          string    `json:"url"`
	LastPlayedAt time.Time `json:"last_played_at"`
}

// historyFile is the on-disk envelope. Version is reserved for forward
// compat (spec §"History / Schema").
type historyFile struct {
	Version int            `json:"version"`
	Entries []HistoryEntry `json:"entries"`
}

// History is the URL adapter's persistent recent-URLs list. It is its
// own mutex domain — independent of Adapter.mu — because OnStop
// callbacks (manager.go:38-43) run in their own goroutines and may
// overlap a play handler.
type History struct {
	mu              sync.Mutex
	path            string // "" = in-memory only (no save)
	entries         []HistoryEntry
	persistDisabled bool // set after the first save failure
}

// LoadHistory reads the history file at path. Failures (missing,
// corrupt JSON, unknown version, IO error) collapse to empty history
// + warning log; never returns nil. Path "" means in-memory only.
func LoadHistory(path string) *History {
	h := &History{path: path}
	if path == "" {
		return h
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("url history load: file unreadable, starting empty", "path", path, "err", err)
		}
		return h
	}
	var hf historyFile
	if err := json.Unmarshal(data, &hf); err != nil {
		slog.Warn("url history load: corrupt JSON, starting empty", "path", path, "err", err)
		return h
	}
	if hf.Version != historySchemaVersion {
		slog.Warn("url history load: unknown version, starting empty",
			"version", hf.Version, "want", historySchemaVersion, "path", path)
		return h
	}
	if len(hf.Entries) > historyMaxEntries {
		hf.Entries = hf.Entries[:historyMaxEntries]
	}
	h.entries = hf.Entries
	return h
}

// AddOrBump records rawURL. If a URL with the same dedupe key already
// exists, it is moved to position 0 and its stored URL is replaced
// with rawURL (latest creds win). Otherwise rawURL is inserted at
// position 0; older entries shift down; entries beyond the max are
// evicted. Persists to disk if path is set.
func (h *History) AddOrBump(rawURL string) {
	key := dedupeKey(rawURL)
	if key == "" {
		return // unparseable; caller should validate first
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	for i, e := range h.entries {
		if dedupeKey(e.URL) == key {
			// Re-insert at position 0 with the new raw URL + timestamp.
			h.entries = append(h.entries[:i], h.entries[i+1:]...)
			h.entries = append([]HistoryEntry{{URL: rawURL, LastPlayedAt: now}}, h.entries...)
			h.saveLocked()
			return
		}
	}
	h.entries = append([]HistoryEntry{{URL: rawURL, LastPlayedAt: now}}, h.entries...)
	if len(h.entries) > historyMaxEntries {
		h.entries = h.entries[:historyMaxEntries]
	}
	h.saveLocked()
}

// List returns a copy of the entries (safe to mutate the copy without
// holding the lock).
func (h *History) List() []HistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]HistoryEntry, len(h.entries))
	copy(out, h.entries)
	return out
}

// Len returns the number of entries.
func (h *History) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}

// Remove deletes the entry at idx. Returns false on out-of-range.
// Persists if path is set.
func (h *History) Remove(idx int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if idx < 0 || idx >= len(h.entries) {
		return false
	}
	h.entries = append(h.entries[:idx], h.entries[idx+1:]...)
	h.saveLocked()
	return true
}

// Get returns the entry at idx. Returns ok=false on out-of-range.
func (h *History) Get(idx int) (HistoryEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if idx < 0 || idx >= len(h.entries) {
		return HistoryEntry{}, false
	}
	return h.entries[idx], true
}

// saveLocked persists to disk if path is set and persistDisabled is
// false. Caller must hold h.mu. On rename failure, sets
// persistDisabled so subsequent saves are silent no-ops.
func (h *History) saveLocked() {
	if h.path == "" || h.persistDisabled {
		return
	}
	data, err := json.Marshal(historyFile{
		Version: historySchemaVersion,
		Entries: h.entries,
	})
	if err != nil {
		slog.Warn("url history save: marshal failed", "err", err)
		return
	}
	if err := config.WriteAtomic(h.path, data); err != nil {
		slog.Warn("url history save: disabling persistence after first failure",
			"path", h.path, "err", err)
		h.persistDisabled = true
		return
	}
}
