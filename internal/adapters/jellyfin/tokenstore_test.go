package jellyfin

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tempTokenPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "jellyfin", "token.json")
}

func TestTokenstore_LoadMissingReturnsEmpty(t *testing.T) {
	got, err := LoadToken(tempTokenPath(t))
	if err != nil {
		t.Fatalf("LoadToken on missing file: err = %v, want nil", err)
	}
	if got != (Token{}) {
		t.Errorf("LoadToken on missing file = %+v, want zero value", got)
	}
}

func TestTokenstore_SaveThenLoadRoundTrip(t *testing.T) {
	path := tempTokenPath(t)
	want := Token{
		AccessToken: "aaa",
		UserID:      "user-id",
		ServerID:    "server-id",
		ServerURL:   "https://jf.example.com",
	}
	if err := SaveToken(path, want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	got, err := LoadToken(path)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestTokenstore_SaveCreatesParentDir(t *testing.T) {
	path := tempTokenPath(t)
	if err := SaveToken(path, Token{AccessToken: "x"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}

func TestTokenstore_FileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits don't map cleanly to Windows ACLs")
	}
	path := tempTokenPath(t)
	if err := SaveToken(path, Token{AccessToken: "x"}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got := info.Mode().Perm()
	if got != 0o600 {
		t.Errorf("file mode = %o, want 0600", got)
	}
}

func TestTokenstore_LoadCorruptReturnsEmptyNoError(t *testing.T) {
	path := tempTokenPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-json{"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken(path)
	if err != nil {
		t.Fatalf("LoadToken on corrupt file: err = %v, want nil (corrupt should be soft-fail)", err)
	}
	if got != (Token{}) {
		t.Errorf("LoadToken on corrupt = %+v, want zero", got)
	}
}

func TestTokenstore_WipeRemovesFile(t *testing.T) {
	path := tempTokenPath(t)
	if err := SaveToken(path, Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := WipeToken(path); err != nil {
		t.Fatalf("WipeToken: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after Wipe: err = %v", err)
	}
}

func TestTokenstore_WipeMissingIsNoOp(t *testing.T) {
	if err := WipeToken(tempTokenPath(t)); err != nil {
		t.Errorf("WipeToken on missing file: err = %v, want nil", err)
	}
}

func TestTokenstore_SaveAtomic(t *testing.T) {
	// Save twice; the second save must replace the first atomically (not
	// leave a half-written intermediate file). Verified by content match.
	path := tempTokenPath(t)
	if err := SaveToken(path, Token{AccessToken: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(path, Token{AccessToken: "second"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Token
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "second" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "second")
	}
	// No leftover .tmp files in the parent dir.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if e.Type()&fs.ModeType == 0 && e.Name() != "token.json" {
			t.Errorf("leftover file in token dir: %q", e.Name())
		}
	}
}
