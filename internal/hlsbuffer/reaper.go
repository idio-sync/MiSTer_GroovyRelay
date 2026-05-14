package hlsbuffer

import (
	"os"
	"path/filepath"
	"time"
)

const ActiveLockName = ".active.lock"

func ReapStaleSessions(root string, maxAge time.Duration, now time.Time) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) <= maxAge {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, ActiveLockName)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
