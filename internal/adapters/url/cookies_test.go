package url

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const sampleCookies = `# Netscape HTTP Cookie File
# https://curl.se/docs/http-cookies.html
.youtube.com	TRUE	/	TRUE	1893456000	LOGIN_INFO	abc123
.youtube.com	TRUE	/	TRUE	1893456000	SID	xyz789
`

func TestSaveCookies_WritesAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "url_cookies.txt")

	st, err := saveCookies(path, []byte(sampleCookies))
	if err != nil {
		t.Fatalf("saveCookies: %v", err)
	}
	if st.Mtime.IsZero() {
		t.Fatal("returned mtime is zero")
	}
	if st.Size != int64(len(sampleCookies)) {
		t.Errorf("Size = %d, want %d", st.Size, len(sampleCookies))
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != sampleCookies {
		t.Errorf("file contents mismatch")
	}

	// .tmp file should not linger after rename.
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".tmp file lingered: %v", err)
	}
}

func TestSaveCookies_Permissions0600OnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics not applicable on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "url_cookies.txt")
	if _, err := saveCookies(path, []byte(sampleCookies)); err != nil {
		t.Fatalf("saveCookies: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("perm = %o, want 0600", mode)
	}
}

func TestSaveCookies_OverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "url_cookies.txt")

	if _, err := saveCookies(path, []byte("first")); err != nil {
		t.Fatalf("saveCookies first: %v", err)
	}
	if _, err := saveCookies(path, []byte("second")); err != nil {
		t.Fatalf("saveCookies second: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second" {
		t.Errorf("got %q, want %q", got, "second")
	}
}

func TestValidateCookies_AcceptsNetscape(t *testing.T) {
	if err := validateCookies([]byte(sampleCookies)); err != nil {
		t.Errorf("valid cookies rejected: %v", err)
	}
}

func TestValidateCookies_RejectsEmpty(t *testing.T) {
	if err := validateCookies(nil); err == nil {
		t.Error("nil accepted")
	}
	if err := validateCookies([]byte("   \n  ")); err == nil {
		t.Error("whitespace-only accepted")
	}
}

func TestValidateCookies_RejectsNoTabs(t *testing.T) {
	bad := "this is not netscape format\nat all\nno tabs anywhere\n"
	if err := validateCookies([]byte(bad)); err == nil {
		t.Error("no-tabs body accepted")
	}
}

func TestValidateCookies_AcceptsCommentsAndBlankLines(t *testing.T) {
	mixed := "# comment\n\n# more\n.youtube.com\tTRUE\t/\tTRUE\t1893456000\tFOO\tbar\n\n"
	if err := validateCookies([]byte(mixed)); err != nil {
		t.Errorf("mixed comments+blanks rejected: %v", err)
	}
}

func TestClearCookies_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "url_cookies.txt")
	if _, err := saveCookies(path, []byte(sampleCookies)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := clearCookies(path); err != nil {
		t.Fatalf("clearCookies: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("file still exists after clear")
	}
}

func TestClearCookies_IdempotentOnMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")
	if err := clearCookies(path); err != nil {
		t.Errorf("clearCookies on missing file: %v", err)
	}
}

func TestStatCookies_ReturnsSizeAndMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "url_cookies.txt")

	// Missing → ok=false.
	st, ok, err := statCookies(path)
	if err != nil {
		t.Fatalf("statCookies missing: %v", err)
	}
	if ok {
		t.Error("missing file reported ok=true")
	}

	// Present.
	if _, err := saveCookies(path, []byte(sampleCookies)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, ok, err = statCookies(path)
	if err != nil {
		t.Fatalf("statCookies present: %v", err)
	}
	if !ok {
		t.Error("present file reported ok=false")
	}
	if st.Size != int64(len(sampleCookies)) {
		t.Errorf("size = %d, want %d", st.Size, len(sampleCookies))
	}
	if st.Mtime.IsZero() {
		t.Error("mtime is zero")
	}
}

func TestAdapter_CookieMethods_RoundTrip(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Initially absent.
	if _, ok, err := a.CookieStat(); err != nil || ok {
		t.Fatalf("CookieStat initial = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	// Validate accepts good cookies.
	if err := a.ValidateCookies([]byte(sampleCookies)); err != nil {
		t.Fatalf("ValidateCookies(good): %v", err)
	}
	// Save writes the file.
	st, err := a.SaveCookies([]byte(sampleCookies))
	if err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	if st.Size != int64(len(sampleCookies)) {
		t.Errorf("Size = %d, want %d", st.Size, len(sampleCookies))
	}
	got, ok, err := a.CookieStat()
	if err != nil || !ok {
		t.Fatalf("CookieStat after save = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got.Size != st.Size {
		t.Errorf("CookieStat Size = %d, want %d", got.Size, st.Size)
	}
	// Clear removes it.
	if err := a.ClearCookies(); err != nil {
		t.Fatalf("ClearCookies: %v", err)
	}
	if _, ok, _ := a.CookieStat(); ok {
		t.Errorf("CookieStat after clear ok=true, want false")
	}
}

func TestAdapter_ValidateCookies_RejectsGarbage(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.ValidateCookies([]byte("not cookies")); err == nil {
		t.Fatal("ValidateCookies accepted garbage; want error")
	}
}

// The exported SaveCookies is the chassis cookie route's write path (the
// legacy POST /cookies handler is gone); invalid input must be rejected
// before anything touches disk.
func TestAdapter_SaveCookies_RejectsInvalidWithoutWriting(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.SaveCookies([]byte("not-cookies-format")); err == nil {
		t.Fatal("SaveCookies accepted invalid cookies; want error")
	}
	if _, err := os.Stat(a.CookiesPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file written despite invalid input (stat err = %v)", err)
	}
}

func TestAdapter_ClearCookies_IdempotentOnMissing(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.ClearCookies(); err != nil {
		t.Fatalf("ClearCookies on missing file: %v", err)
	}
}
