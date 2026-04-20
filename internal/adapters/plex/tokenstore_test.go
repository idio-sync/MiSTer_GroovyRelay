package plex

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestStoredData_RoundTrip writes a StoredData and reads it back, verifying
// both JSON fields survive the marshal/unmarshal cycle.
func TestStoredData_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &StoredData{DeviceUUID: "abc-123", AuthToken: "secret"}
	if err := SaveStoredData(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadStoredData(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.DeviceUUID != in.DeviceUUID || out.AuthToken != in.AuthToken {
		t.Errorf("round-trip mismatch: %+v vs %+v", out, in)
	}
}

// TestLoadStoredData_Missing verifies the documented "missing file returns
// zero-value, not error" contract — callers treat an empty directory as
// "never linked" and trigger the PIN flow.
func TestLoadStoredData_Missing(t *testing.T) {
	dir := t.TempDir()
	out, err := LoadStoredData(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.DeviceUUID != "" || out.AuthToken != "" {
		t.Errorf("expected zero-value, got %+v", out)
	}
}

// TestSaveStoredData_Perms validates the 0600 file permission on POSIX.
// Windows permission bits don't map onto POSIX mode so we skip there.
func TestSaveStoredData_Perms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not enforced on Windows")
	}
	dir := t.TempDir()
	if err := SaveStoredData(dir, &StoredData{DeviceUUID: "u", AuthToken: "t"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected 0600, got %o", perm)
	}
}
