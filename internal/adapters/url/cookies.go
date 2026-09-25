package url

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// CookiesStat is the metadata reported to the settings UI's cookie
// status line. Never includes content.
type CookiesStat struct {
	Size  int64     // bytes on disk
	Mtime time.Time // file modtime, set by os.Rename to "now"
}

// saveCookies atomically writes data to path with mode 0600. Returns
// the resulting CookiesStat (size + mtime read AFTER os.Rename so it
// matches what the settings UI will display on next render — review
// fix M3). Symmetric with statCookies, so callers don't need to re-stat.
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
	// NTFS ACLs may not honor it. The comment here used to claim the
	// caller logs a warning on a mode mismatch — that was never wired
	// up, so the lie was removed. Operators on Windows accept the
	// LAN-trust threat model documented in the spec §"Security posture".
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
// returns nil. Backs the exported ClearCookies.
func clearCookies(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("clear cookies: %w", err)
}

// statCookies reports the file size + mtime, or ok=false if the file
// is absent. Backs the exported CookieStat (settings UI status line).
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

// ValidateCookies runs the lenient Netscape-format check on raw cookie
// bytes without writing anything. Exported for the chassis cookie route
// wrapper (package main cannot call the unexported validateCookies).
func (a *Adapter) ValidateCookies(raw []byte) error {
	return validateCookies(raw)
}

// SaveCookies validates then atomically writes raw cookie bytes to the
// adapter's cookies file, returning the resulting stat. Exported wrapper
// over saveCookies for the chassis cookie route.
func (a *Adapter) SaveCookies(raw []byte) (CookiesStat, error) {
	if err := validateCookies(raw); err != nil {
		return CookiesStat{}, err
	}
	return saveCookies(a.cookiesPath, raw)
}

// ClearCookies removes the cookies file (idempotent). Exported wrapper
// over clearCookies for the chassis cookie route.
func (a *Adapter) ClearCookies() error {
	return clearCookies(a.cookiesPath)
}

// CookieStat reports the cookies file size + mtime, or ok=false if the
// file is absent. Exported wrapper over statCookies for paint-time status.
func (a *Adapter) CookieStat() (CookiesStat, bool, error) {
	return statCookies(a.cookiesPath)
}
