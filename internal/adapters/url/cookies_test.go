package url

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const sampleCookies = `# Netscape HTTP Cookie File
# https://curl.se/docs/http-cookies.html
.youtube.com	TRUE	/	TRUE	1893456000	LOGIN_INFO	abc123
.youtube.com	TRUE	/	TRUE	1893456000	SID	xyz789
`

func TestSaveCookies_WritesAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "url_cookies.txt")

	mtime, err := saveCookies(path, []byte(sampleCookies))
	if err != nil {
		t.Fatalf("saveCookies: %v", err)
	}
	if mtime.IsZero() {
		t.Fatal("returned mtime is zero")
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

// readCookiesFromBody enforces the 1 MiB cap at the read site
// (review fix I5). The handler test in play_test.go exercises this
// via httptest; this unit test pins the helper's contract.
func TestReadCookiesFromBody_CapsAtOneMiB(t *testing.T) {
	body := strings.Repeat("a", (1<<20)+1)
	_, err := readCookiesFromBody(strings.NewReader(body))
	if err == nil {
		t.Fatal("oversize body accepted")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("err = %q, want 'too large'", err.Error())
	}
}
