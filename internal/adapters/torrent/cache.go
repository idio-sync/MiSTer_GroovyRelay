package torrent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const markerFileName = ".groovyrelay-torrent-session.json"
const storageRootMarkerName = ".groovyrelay-torrent-storage-root.json"

type sessionMarker struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

func cacheRoot(cfg Config, dataDir string) string {
	return filepath.Join(ownedDownloadRoot(cfg.DownloadDir, dataDir), "storage")
}

func sessionRoot(cfg Config, dataDir string) string {
	return filepath.Join(ownedDownloadRoot(cfg.DownloadDir, dataDir), "sessions")
}

func infoHashStorageDirName(infoHash string) string {
	return strings.ToLower(infoHash)
}

func createSessionDir(root, sessionID string) (string, error) {
	if err := validateSessionID(sessionID); err != nil {
		return "", err
	}
	dir := filepath.Join(root, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	marker := sessionMarker{SessionID: sessionID, CreatedAt: time.Now().UTC()}
	body, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, markerFileName), body, 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

func validateSessionID(sessionID string) error {
	return validateCacheChildName(sessionID, "session id")
}

func validateCacheChildName(name, label string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s required", label)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%s must be a single safe path element", label)
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%s must be a single safe path element", label)
	}
	if filepath.Clean(name) != name {
		return fmt.Errorf("%s must be a single safe path element", label)
	}
	return nil
}

func isMarkedSessionDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, markerFileName))
	return err == nil && !info.IsDir()
}

func removeSessionDir(root, dir string) error {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return err
	}
	if samePath(rootAbs, dirAbs) {
		return fmt.Errorf("refuse to remove torrent session root")
	}
	rel, err := filepath.Rel(rootAbs, dirAbs)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refuse to remove path outside torrent session root")
	}
	if !isMarkedSessionDir(dirAbs) {
		return fmt.Errorf("refuse to remove unmarked torrent session directory")
	}
	return os.RemoveAll(dirAbs)
}

func ensureStorageRoot(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(map[string]any{
		"kind":       "groovyrelay-torrent-storage-root",
		"created_at": time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, storageRootMarkerName), body, 0o600)
}

func isMarkedStorageRoot(root string) bool {
	info, err := os.Stat(filepath.Join(root, storageRootMarkerName))
	return err == nil && !info.IsDir()
}

func removeStorageDir(root, storageKey string) error {
	if err := validateCacheChildName(storageKey, "storage key"); err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return err
	}
	if !isMarkedStorageRoot(rootAbs) {
		return fmt.Errorf("refuse to remove storage from unmarked torrent storage root")
	}
	dirAbs := filepath.Join(rootAbs, storageKey)
	rel, err := filepath.Rel(rootAbs, dirAbs)
	if err != nil {
		return err
	}
	if rel != storageKey {
		return fmt.Errorf("refuse to remove path outside torrent storage root")
	}
	return os.RemoveAll(dirAbs)
}

type cacheEntry struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
}

func pruneStorageCache(root string, maxBytes int64, active map[string]struct{}) error {
	if maxBytes <= 0 {
		return nil
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return err
	}
	if !isMarkedStorageRoot(rootAbs) {
		return fmt.Errorf("refuse to prune unmarked torrent storage root")
	}
	entries, total, err := collectCacheEntries(rootAbs)
	if err != nil {
		return err
	}
	if total <= maxBytes {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ModTime.Before(entries[j].ModTime)
	})
	for _, entry := range entries {
		if _, ok := active[entry.Name]; ok {
			continue
		}
		if err := os.RemoveAll(entry.Path); err != nil {
			return err
		}
		total -= entry.Size
		if total <= maxBytes {
			return nil
		}
	}
	return nil
}

func collectCacheEntries(root string) ([]cacheEntry, int64, error) {
	children, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, err
	}
	var entries []cacheEntry
	var total int64
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		path := filepath.Join(root, child.Name())
		info, err := child.Info()
		if err != nil {
			return nil, 0, err
		}
		size, err := dirSize(path)
		if err != nil {
			return nil, 0, err
		}
		total += size
		entries = append(entries, cacheEntry{Path: path, Name: child.Name(), Size: size, ModTime: info.ModTime()})
	}
	return entries, total, nil
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
