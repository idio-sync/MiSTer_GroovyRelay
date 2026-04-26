package url

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestReadCookiesFromHTTPRequest_FormEncoded(t *testing.T) {
	form := neturl.Values{"cookies": {sampleCookies}}
	req := httptest.NewRequest(http.MethodPost, "/whatever",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	got, err := readCookiesFromHTTPRequest(req)
	if err != nil {
		t.Fatalf("readCookiesFromHTTPRequest: %v", err)
	}
	if string(got) != sampleCookies {
		t.Errorf("got %q, want sampleCookies", got)
	}
}

func TestReadCookiesFromHTTPRequest_JSON(t *testing.T) {
	body := `{"cookies":` + jsonStringLiteral(sampleCookies) + `}`
	req := httptest.NewRequest(http.MethodPost, "/whatever",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	got, err := readCookiesFromHTTPRequest(req)
	if err != nil {
		t.Fatalf("readCookiesFromHTTPRequest: %v", err)
	}
	if string(got) != sampleCookies {
		t.Errorf("got %q, want sampleCookies", got)
	}
}

func TestReadCookiesFromHTTPRequest_OversizeRejected(t *testing.T) {
	// Build an oversized form body. http.MaxBytesReader on the request
	// body must reject it before any handler-level processing.
	huge := strings.Repeat("a", (1<<20)+100)
	form := neturl.Values{"cookies": {huge}}
	req := httptest.NewRequest(http.MethodPost, "/whatever",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, err := readCookiesFromHTTPRequest(req)
	if err == nil {
		t.Fatal("oversize HTTP request body accepted")
	}
}

func TestHandleCookiesPOST_Form_WritesFile(t *testing.T) {
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   nil,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := strings.NewReader("cookies=" + neturl.QueryEscape(sampleCookies))
	req := httptest.NewRequest("POST", "/ui/adapter/url/cookies", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleCookiesSet(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	got, err := os.ReadFile(a.CookiesPath())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != sampleCookies {
		t.Errorf("file content mismatch")
	}
}

func TestHandleCookiesPOST_JSON_WritesFile(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	body, _ := json.Marshal(map[string]string{"cookies": sampleCookies})
	req := httptest.NewRequest("POST", "/ui/adapter/url/cookies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handleCookiesSet(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	if _, err := os.Stat(a.CookiesPath()); err != nil {
		t.Errorf("cookies file not written: %v", err)
	}
}

func TestHandleCookiesPOST_RejectsInvalid(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	body := strings.NewReader("cookies=" + neturl.QueryEscape("not-cookies-format"))
	req := httptest.NewRequest("POST", "/ui/adapter/url/cookies", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleCookiesSet(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if _, err := os.Stat(a.CookiesPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file written despite invalid input")
	}
}

func TestHandleCookiesPOST_RejectsOversize(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	huge := strings.Repeat("a", (1<<20)+100)
	body := strings.NewReader("cookies=" + neturl.QueryEscape(huge))
	req := httptest.NewRequest("POST", "/ui/adapter/url/cookies", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleCookiesSet(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for oversize", w.Code)
	}
}

func TestHandleCookiesDELETE_RemovesFile(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if _, err := saveCookies(a.CookiesPath(), []byte(sampleCookies)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	req := httptest.NewRequest("DELETE", "/ui/adapter/url/cookies", nil)
	w := httptest.NewRecorder()
	a.handleCookiesClear(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if _, err := os.Stat(a.CookiesPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("file still exists after DELETE")
	}
}

func TestHandleCookiesDELETE_IdempotentOnMissing(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	req := httptest.NewRequest("DELETE", "/ui/adapter/url/cookies", nil)
	w := httptest.NewRecorder()
	a.handleCookiesClear(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// F1 will use hx-target="#url-cookies-status" to swap the cookies-section
// status line in place after Save/Clear. Pin the selector contract here so
// any future fragment refactor that drops the id breaks loudly at the
// HTTP-layer test, not silently in the panel.
func TestHandleCookiesPOST_HTMX_RendersStatusFragment(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	body := strings.NewReader("cookies=" + neturl.QueryEscape(sampleCookies))
	req := httptest.NewRequest("POST", "/ui/adapter/url/cookies", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	a.handleCookiesSet(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), `id="url-cookies-status"`) {
		t.Errorf("body missing id=\"url-cookies-status\" selector: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Cookies stored") {
		t.Errorf("body missing 'Cookies stored' text: %s", w.Body.String())
	}
}

func TestHandleCookiesDELETE_HTMX_RendersStatusFragment(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	req := httptest.NewRequest("DELETE", "/ui/adapter/url/cookies", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	a.handleCookiesClear(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), `id="url-cookies-status"`) {
		t.Errorf("body missing id=\"url-cookies-status\" selector: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "No cookies set") {
		t.Errorf("body missing 'No cookies set' text: %s", w.Body.String())
	}
}

func TestUIRoutes_IncludesCookiesEndpoints(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	got := a.UIRoutes()
	want := map[string]bool{
		"GET panel":      false,
		"POST play":      false,
		"POST cookies":   false,
		"DELETE cookies": false,
	}
	for _, r := range got {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("missing route: %s", k)
		}
	}
}

// jsonStringLiteral encodes s as a Go string literal suitable for
// embedding inside JSON. Avoids depending on encoding/json just for
// the test scaffold (the production path uses json.Unmarshal).
func jsonStringLiteral(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, string(r)...)
		}
	}
	out = append(out, '"')
	return string(out)
}

// TestHandleCookiesPOST_HTMX_ErrorFragmentTargetsCookiesStatus pins
// the IMP-3 fix: when the cookies handler errors on HTMX, the response
// fragment must carry id="url-cookies-status" so htmx's hx-target swap
// lands on the right element. Previously the handler used
// respondError, which emits id="url-panel" — leaving the cookies form
// pointing at a missing target until the next 5s panel refresh.
func TestHandleCookiesPOST_HTMX_ErrorFragmentTargetsCookiesStatus(t *testing.T) {
	a, _ := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})

	// Invalid cookies body triggers validateCookies → 400.
	body := strings.NewReader("cookies=" + neturl.QueryEscape("not-cookies-format"))
	req := httptest.NewRequest("POST", "/ui/adapter/url/cookies", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	a.handleCookiesSet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, `id="url-cookies-status"`) {
		t.Errorf("error fragment missing id=\"url-cookies-status\"; got %s", bodyStr)
	}
	if strings.Contains(bodyStr, `id="url-panel"`) {
		t.Errorf("error fragment wrongly carries id=\"url-panel\" (would break cookies form swap target); got %s", bodyStr)
	}
}
