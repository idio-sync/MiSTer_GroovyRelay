package url

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// CookiesStat is the metadata reported in the UI panel + JSON
// responses. Never includes content.
type CookiesStat struct {
	Size  int64     // bytes on disk
	Mtime time.Time // file modtime, set by os.Rename to "now"
}

// saveCookies atomically writes data to path with mode 0600. Returns
// the resulting CookiesStat (size + mtime read AFTER os.Rename so it
// matches what the panel will display on next render — review fix M3).
// Symmetric with statCookies, so handlers don't need to re-stat.
//
// Algorithm:
//  1. Write to <path>.tmp at mode 0600.
//  2. os.Rename(.tmp, path) — atomic on POSIX, atomic-enough on Windows.
//  3. Stat the renamed file for mtime; size is len(data).
//
// On any failure, the .tmp file is removed; an existing path file is
// untouched.
//
// Edge case: if the file is deleted between rename and stat (rare; the
// path is bridge-owned so only operator error or external interference
// would do this), saveCookies returns an error — but the rename did
// succeed, so the file may exist again on retry. Retries are
// idempotent (atomic rewrite), so a "save failed" report followed by a
// successful retry is safe.
func saveCookies(path string, data []byte) (CookiesStat, error) {
	tmp := path + ".tmp"
	// 0600 is best-effort on Windows; OpenFile will accept the mode but
	// NTFS ACLs may not honor it. Logged as a warning by the caller if
	// the post-write Stat reports a different mode (handler does this).
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return CookiesStat{}, fmt.Errorf("save cookies: open tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return CookiesStat{}, fmt.Errorf("save cookies: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return CookiesStat{}, fmt.Errorf("save cookies: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return CookiesStat{}, fmt.Errorf("save cookies: close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return CookiesStat{}, fmt.Errorf("save cookies: rename: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return CookiesStat{}, fmt.Errorf("save cookies: stat after rename: %w", err)
	}
	return CookiesStat{Size: info.Size(), Mtime: info.ModTime()}, nil
}

// clearCookies removes the cookies file. Idempotent — missing file
// returns nil. Used by DELETE /ui/adapter/url/cookies.
func clearCookies(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("clear cookies: %w", err)
}

// statCookies reports the file size + mtime, or ok=false if the file
// is absent. Surfaced in the panel status line and in JSON responses.
func statCookies(path string) (CookiesStat, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CookiesStat{}, false, nil
		}
		return CookiesStat{}, false, err
	}
	return CookiesStat{Size: info.Size(), Mtime: info.ModTime()}, true, nil
}

// validateCookies applies lenient Netscape-format checks. Browsers
// and converters produce slight variations, so we accept anything
// that looks plausibly cookies-shaped and let yt-dlp do the strict
// parse at use time.
//
// Required:
//   - non-empty after trim
//   - at least one non-comment, non-blank line splits to ≥7 tab fields
func validateCookies(data []byte) error {
	body := strings.TrimSpace(string(data))
	if body == "" {
		return errors.New("cookies body is empty")
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 7 {
			return nil // at least one well-formed line
		}
	}
	return errors.New("cookies body has no Netscape-format lines (expected ≥7 tab-separated fields)")
}

// readCookiesFromBody is the low-level cap-enforcing reader. It
// reads up to 1 MiB from r at the read boundary, not after buffering —
// an adversarial 100 MiB POST is rejected without ever fully
// buffering (review fix I5). HTTP callers should use
// readCookiesFromHTTPRequest, which adds Content-Type dispatch and
// http.MaxBytesReader on top of this helper.
//
// Returns the raw bytes; caller passes to validateCookies and
// saveCookies.
const maxCookiesBody = 1 << 20 // 1 MiB

func readCookiesFromBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxCookiesBody+1))
	if err != nil {
		return nil, fmt.Errorf("read cookies body: %w", err)
	}
	if len(data) > maxCookiesBody {
		return nil, fmt.Errorf("cookies body too large (>%d bytes)", maxCookiesBody)
	}
	return data, nil
}

// readCookiesFromHTTPRequest is the HTTP-shaped wrapper. Form-encoded
// bodies extract the "cookies" field; JSON bodies extract {"cookies":
// "..."}. Mirrors extractURL's content-type dispatch in play.go.
func readCookiesFromHTTPRequest(r *http.Request) ([]byte, error) {
	// Defend memory at read time (review fix I5).
	r.Body = http.MaxBytesReader(nil, r.Body, maxCookiesBody+1)

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		var payload struct {
			Cookies string `json:"cookies"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return []byte(payload.Cookies), nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("parse form: %w", err)
	}
	v := r.Form.Get("cookies")
	return []byte(v), nil
}
