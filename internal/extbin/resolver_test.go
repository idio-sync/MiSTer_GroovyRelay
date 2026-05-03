package extbin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolver_ConfigOverrideWinsAndCanUpdate(t *testing.T) {
	first := makeExecutable(t, "first")
	second := makeExecutable(t, "second")

	r := New("tool", first, "")
	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve first: %v", err)
	}
	if got != first {
		t.Fatalf("Resolve first = %q, want %q", got, first)
	}

	r.UpdateOverride(second)
	got, err = r.Resolve()
	if err != nil {
		t.Fatalf("Resolve second: %v", err)
	}
	if got != second {
		t.Fatalf("Resolve second = %q, want %q", got, second)
	}
}

func TestResolver_ConfigOverrideMissingIsHardError(t *testing.T) {
	r := New("tool", filepath.Join(t.TempDir(), "missing"), "")
	_, err := r.Resolve()
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "override") {
		t.Fatalf("error should mention override: %v", err)
	}
}

func TestResolver_SidecarBeforePATH(t *testing.T) {
	dir := t.TempDir()
	sidecar := filepath.Join(dir, executableName("tool"))
	writeExecutable(t, sidecar)

	r := New("tool", "", dir)
	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sidecar {
		t.Fatalf("Resolve = %q, want sidecar %q", got, sidecar)
	}
}

func makeExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), executableName(name))
	writeExecutable(t, path)
	return path
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
