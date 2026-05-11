package torrent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheRootUsesOwnedChild(t *testing.T) {
	dataDir := t.TempDir()
	cfg := DefaultConfig()
	got := cacheRoot(cfg, dataDir)
	want := filepath.Join(dataDir, "groovyrelay-torrent", "storage")
	if got != want {
		t.Fatalf("cacheRoot = %q, want %q", got, want)
	}
}

func TestSessionRootUsesOwnedChild(t *testing.T) {
	dataDir := t.TempDir()
	cfg := DefaultConfig()
	got := sessionRoot(cfg, dataDir)
	want := filepath.Join(dataDir, "groovyrelay-torrent", "sessions")
	if got != want {
		t.Fatalf("sessionRoot = %q, want %q", got, want)
	}
}

func TestCacheRootWithConfiguredDownloadDirUsesOwnedChild(t *testing.T) {
	dataDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.DownloadDir = filepath.Join(dataDir, "cache-parent")
	got := cacheRoot(cfg, dataDir)
	want := filepath.Join(cfg.DownloadDir, "groovyrelay-torrent", "storage")
	if got != want {
		t.Fatalf("cacheRoot = %q, want %q", got, want)
	}
}

func TestInfoHashStorageDirNameLowercases(t *testing.T) {
	if got := infoHashStorageDirName("ABCDEF012345"); got != "abcdef012345" {
		t.Fatalf("infoHashStorageDirName = %q, want lowercase", got)
	}
}

func TestCreateSessionDirWritesMarkerBeforeData(t *testing.T) {
	root := t.TempDir()
	dir, err := createSessionDir(root, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, markerFileName)); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if !isMarkedSessionDir(dir) {
		t.Fatalf("isMarkedSessionDir(%q) = false, want true", dir)
	}
}

func TestCreateSessionDirRejectsUnsafeSessionID(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside")

	for _, sessionID := range []string{
		"",
		".",
		"..",
		"../outside",
		"nested/session",
		`nested\session`,
		filepath.Join(root, "absolute"),
	} {
		t.Run(sessionID, func(t *testing.T) {
			if _, err := createSessionDir(root, sessionID); err == nil {
				t.Fatal("createSessionDir succeeded, want error")
			}
		})
	}

	if _, err := os.Stat(filepath.Join(outside, markerFileName)); !os.IsNotExist(err) {
		t.Fatalf("outside marker exists or stat failed differently: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nested", "session", markerFileName)); !os.IsNotExist(err) {
		t.Fatalf("nested marker exists or stat failed differently: %v", err)
	}
}

func TestRemoveSessionDirRefusesUnmarkedDirectory(t *testing.T) {
	root := t.TempDir()
	unmarked := filepath.Join(root, "session-a")
	if err := os.MkdirAll(unmarked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionDir(root, unmarked); err == nil {
		t.Fatal("removeSessionDir unmarked succeeded, want error")
	}
	if _, err := os.Stat(unmarked); err != nil {
		t.Fatalf("unmarked directory was removed: %v", err)
	}
}

func TestRemoveSessionDirRemovesOnlyMarkedChild(t *testing.T) {
	root := t.TempDir()
	dir, err := createSessionDir(root, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionDir(root, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session dir still exists or stat failed differently: %v", err)
	}
}

func TestRemoveSessionDirRefusesRoot(t *testing.T) {
	root := t.TempDir()
	if err := removeSessionDir(root, root); err == nil {
		t.Fatal("removeSessionDir(root, root) succeeded, want error")
	}
}

func TestRemoveSessionDirRefusesMarkedDirectoryOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outside, err := createSessionDir(outsideRoot, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := removeSessionDir(root, outside); err == nil {
		t.Fatal("removeSessionDir outside marked dir succeeded, want error")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside marked directory was removed: %v", err)
	}
}

func TestPruneStorageCacheRequiresMarkedRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "oldhash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pruneStorageCache(root, 1, nil); err == nil {
		t.Fatal("pruneStorageCache on unmarked root succeeded, want error")
	}
}

func TestPruneStorageCacheNoOpsWhenDisabled(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "oldhash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pruneStorageCache(root, 0, nil); err != nil {
		t.Fatalf("pruneStorageCache disabled: %v", err)
	}
}

func TestPruneStorageCacheSkipsActiveNewFileByInfoHashDir(t *testing.T) {
	root := t.TempDir()
	if err := ensureStorageRoot(root); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(root, infoHashStorageDirName("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	activeKey := infoHashStorageDirName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	activeDir := filepath.Join(root, activeKey)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "data.bin"), []byte("old-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeDir, "data.bin"), []byte("active-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	active := map[string]struct{}{activeKey: {}}
	if err := pruneStorageCache(root, int64(len("active-data")), active); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old inactive dir still exists or stat failed differently: %v", err)
	}
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("active dir was removed: %v", err)
	}
}
