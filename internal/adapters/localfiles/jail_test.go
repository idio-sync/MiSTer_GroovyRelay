package localfiles

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveInLibrary(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ok.mkv"))
	mustMkdir(t, filepath.Join(root, "sub"))
	mustWrite(t, filepath.Join(root, "sub", "inner.mp4"))
	mustWrite(t, filepath.Join(root, "existing.mp3"))

	tests := []struct {
		name string
		rel  string
		want string
	}{
		{name: "file at root", rel: "ok.mkv", want: filepath.Join(root, "ok.mkv")},
		{name: "file in child", rel: "sub/inner.mp4", want: filepath.Join(root, "sub", "inner.mp4")},
		{name: "missing single leaf", rel: "new.mov", want: filepath.Join(root, "new.mov")},
		{name: "missing multi segment tail", rel: "missing/deep/file.mov", want: filepath.Join(root, "missing", "deep", "file.mov")},
		{name: "existing file leaf", rel: "existing.mp3", want: filepath.Join(root, "existing.mp3")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveInLibrary(root, tc.rel)
			if err != nil {
				t.Fatalf("resolveInLibrary: %v", err)
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveInLibraryRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../etc/passwd", "sub/../../etc/passwd"} {
		if _, err := resolveInLibrary(root, rel); err == nil {
			t.Fatalf("resolveInLibrary(%q) = nil err, want traversal error", rel)
		}
	}
}

func TestResolveInLibraryRejectsAbsoluteInput(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveInLibrary(root, filepath.Join(root, "ok.mkv")); err == nil {
		t.Fatalf("absolute input accepted")
	}
}

func TestResolveInLibraryRejectsEscapingSymlinkLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.mkv"))
	if err := os.Symlink(filepath.Join(outside, "secret.mkv"), filepath.Join(root, "escape.mkv")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := resolveInLibrary(root, "escape.mkv"); err == nil {
		t.Fatalf("escaping symlink leaf accepted")
	}
}

func TestResolveInLibraryRejectsBoundaryCollision(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Dir(root)
	secret := root + "-secret"
	if err := os.Mkdir(secret, 0o755); err != nil {
		t.Fatalf("Mkdir secret: %v", err)
	}
	if _, err := resolveInLibrary(root, filepath.Join("..", filepath.Base(root)+"-secret")); err == nil {
		t.Fatalf("boundary collision traversal accepted under parent %s", parent)
	}
}

func TestResolveInLibraryPermissionDeniedAncestorFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission enforcement is platform/user dependent")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	defer os.Chmod(locked, 0o755)

	if _, err := resolveInLibrary(root, "locked/file.mkv"); err == nil {
		t.Fatalf("permission-denied ancestor accepted")
	}
}

func TestCaseInsensitivePathsOnlyOnWindows(t *testing.T) {
	tests := []struct {
		goos string
		want bool
	}{
		{goos: "windows", want: true},
		{goos: "darwin", want: false},
		{goos: "linux", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			if got := caseInsensitivePathsForGOOS(tc.goos); got != tc.want {
				t.Fatalf("caseInsensitivePathsForGOOS(%q) = %v, want %v", tc.goos, got, tc.want)
			}
		})
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}
