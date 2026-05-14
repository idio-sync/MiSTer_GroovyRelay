package hlsbuffer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReapStaleSessionsKeepsActiveLock(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	stale := filepath.Join(root, "stale")
	active := filepath.Join(root, "active")
	fresh := filepath.Join(root, "fresh")
	for _, dir := range []string{stale, active, fresh} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(active, ActiveLockName), []byte("locked"), 0o600); err != nil {
		t.Fatalf("write active lock: %v", err)
	}
	oldTime := now.Add(-48 * time.Hour)
	freshTime := now.Add(-2 * time.Hour)
	for _, dir := range []string{stale, active} {
		if err := os.Chtimes(dir, oldTime, oldTime); err != nil {
			t.Fatalf("chtimes %s: %v", dir, err)
		}
	}
	if err := os.Chtimes(fresh, freshTime, freshTime); err != nil {
		t.Fatalf("chtimes fresh: %v", err)
	}

	if err := ReapStaleSessions(root, 24*time.Hour, now); err != nil {
		t.Fatalf("ReapStaleSessions: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale dir should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active dir should remain, stat err = %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh dir should remain, stat err = %v", err)
	}
}
