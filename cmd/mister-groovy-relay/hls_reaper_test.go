package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

func TestReapHLSBufferCachesUsesStreamsAndURLRoots(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)

	staleStreams := filepath.Join(dataDir, "streams", "hls", "hls-buffer-stale-streams")
	staleURL := filepath.Join(dataDir, "url", "hls", "hls-buffer-stale-url")
	activeURL := filepath.Join(dataDir, "url", "hls", "hls-buffer-active-url")
	for _, dir := range []string{staleStreams, staleURL, activeURL} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(activeURL, hlsbuffer.ActiveLockName), []byte("active"), 0o600); err != nil {
		t.Fatalf("write active lock: %v", err)
	}

	err := reapHLSBufferCaches(config.BridgeConfig{
		DataDir: dataDir,
		HLSBuffer: config.HLSBufferConfig{
			StaleCacheReapHours: 24,
		},
	}, now)
	if err != nil {
		t.Fatalf("reapHLSBufferCaches: %v", err)
	}
	for _, dir := range []string{staleStreams, staleURL} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("stale cache %s should be reaped, stat err=%v", dir, err)
		}
	}
	if _, err := os.Stat(activeURL); err != nil {
		t.Fatalf("active cache should remain: %v", err)
	}
}
