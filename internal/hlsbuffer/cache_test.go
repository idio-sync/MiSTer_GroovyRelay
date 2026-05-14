package hlsbuffer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheFilenameCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := SafeCachePath(root, "../escape.ts"); err == nil {
		t.Fatal("SafeCachePath accepted ../escape.ts")
	}
	if _, err := SafeCachePath(root, "segment-../../escape.ts"); err == nil {
		t.Fatal("SafeCachePath accepted path separator in generated name")
	}

	got, err := SafeCachePath(root, "segment-000001.ts")
	if err != nil {
		t.Fatalf("SafeCachePath valid name: %v", err)
	}
	cleanRoot := filepath.Clean(root)
	cleanGot := filepath.Clean(got)
	if !strings.HasPrefix(cleanGot, cleanRoot+string(os.PathSeparator)) {
		t.Fatalf("SafeCachePath returned path outside root: %s", cleanGot)
	}
}

func TestCacheLimitsEnforceCountAndBytes(t *testing.T) {
	root := t.TempDir()
	cache := NewSegmentCache(root, 2, 10)
	if err := cache.Put("segment-000001.ts", []byte("1234")); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if err := cache.Put("segment-000002.ts", []byte("5678")); err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if err := cache.Put("segment-000003.ts", []byte("abcde")); err != nil {
		t.Fatalf("Put 3: %v", err)
	}

	entries := cache.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[0].Name != "segment-000002.ts" || entries[1].Name != "segment-000003.ts" {
		t.Fatalf("entries = %+v, want segment-000002 and segment-000003", entries)
	}
	if cache.TotalBytes() != 9 {
		t.Fatalf("TotalBytes = %d, want 9", cache.TotalBytes())
	}
	if _, err := os.Stat(filepath.Join(root, "segment-000001.ts")); !os.IsNotExist(err) {
		t.Fatalf("oldest segment should be evicted, stat err = %v", err)
	}
}
