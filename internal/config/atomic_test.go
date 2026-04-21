package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAtomic_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	want := []byte("hello = 1\n")

	if err := WriteAtomic(path, want); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestWriteAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("new")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

func TestWriteAtomic_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := WriteAtomic(path, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "cfg.toml" {
			t.Errorf("unexpected residue: %s", e.Name())
		}
	}
}

func TestWriteAtomic_RenameFails_LeavesNoTempfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	// A file inside the target dir so it's non-empty; some platforms let
	// rename clobber an empty dir.
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(path, []byte("new")); err == nil {
		t.Fatal("expected error when target path is a directory, got nil")
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "target.tmp.") {
			t.Errorf("tempfile leaked: %s", e.Name())
		}
		if e.Name() == "target" && !e.IsDir() {
			t.Errorf("target was unexpectedly replaced by a file")
		}
	}
}
