package artworkcache

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxBytes     = 8 * 1024 * 1024
	MaxDimension = 4096
)

type FetchOptions struct {
	DataDir string
	URL     string
	Client  *http.Client
}

func Root(dataDir string) string {
	return filepath.Join(dataDir, "artwork-cache")
}

func EnsureRoot(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("artwork cache data dir is required")
	}
	root := Root(dataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func ValidatePath(root, path string) (string, bool) {
	if root == "" || path == "" {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	if resolvedPath == resolvedRoot {
		return "", false
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || info.IsDir() {
		return "", false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", false
	}
	return resolvedPath, true
}

func ResolveSameOrigin(serverURL, candidate string) (*url.URL, bool) {
	base, err := url.Parse(serverURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, false
	}
	if strings.HasPrefix(candidate, "://") {
		return nil, false
	}
	c, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil || c.String() == "" {
		return nil, false
	}
	u := base.ResolveReference(c)
	if !sameOrigin(base, u) {
		return nil, false
	}
	return u, true
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func AppendToken(u *url.URL, key, token string) string {
	if u == nil {
		return ""
	}
	cp := *u
	if key == "" || token == "" {
		return cp.String()
	}
	q := cp.Query()
	q.Set(key, token)
	cp.RawQuery = q.Encode()
	return cp.String()
}

func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable URL>"
	}
	u.User = nil
	q := u.Query()
	for k := range q {
		switch strings.ToLower(k) {
		case "x-plex-token", "api_key", "x-emby-token", "accesstoken", "access_token", "auth_token", "token":
			q.Set(k, "REDACTED")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func FetchToCache(ctx context.Context, opt FetchOptions) (string, error) {
	if strings.TrimSpace(opt.DataDir) == "" {
		return "", fmt.Errorf("artwork cache data dir is required")
	}
	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opt.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch artwork: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch artwork: %s", resp.Status)
	}
	if resp.ContentLength > MaxBytes {
		return "", fmt.Errorf("fetch artwork: content length %d exceeds %d", resp.ContentLength, MaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("fetch artwork: %w", err)
	}
	if len(body) > MaxBytes {
		return "", fmt.Errorf("fetch artwork: response exceeds %d bytes", MaxBytes)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("decode artwork config: %w", err)
	}
	if cfg.Width > MaxDimension || cfg.Height > MaxDimension {
		return "", fmt.Errorf("decode artwork: dimensions %dx%d exceed %d", cfg.Width, cfg.Height, MaxDimension)
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("decode artwork: %w", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() > MaxDimension || bounds.Dy() > MaxDimension {
		return "", fmt.Errorf("decode artwork: dimensions %dx%d exceed %d", bounds.Dx(), bounds.Dy(), MaxDimension)
	}
	root, err := EnsureRoot(opt.DataDir)
	if err != nil {
		return "", err
	}
	var nameBytes [16]byte
	if _, err := rand.Read(nameBytes[:]); err != nil {
		return "", err
	}
	path := filepath.Join(root, hex.EncodeToString(nameBytes[:])+".png")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func Remove(path string) {
	if path == "" {
		return
	}
	if !isGeneratedCachePath(path) {
		slog.Debug("artwork cache cleanup skipped non-cache path", "path", path)
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Debug("artwork cache cleanup", "path", path, "err", err)
	}
}

func isGeneratedCachePath(path string) bool {
	if filepath.Base(filepath.Dir(path)) != "artwork-cache" {
		return false
	}
	base := filepath.Base(path)
	if filepath.Ext(base) != ".png" {
		return false
	}
	name := strings.TrimSuffix(base, ".png")
	if len(name) != 32 {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func WithCleanup(path string, original func(reason string)) func(reason string) {
	return func(reason string) {
		defer Remove(path)
		if original != nil {
			original(reason)
		}
	}
}

func ReapStale(root string, maxAge time.Duration, now time.Time) error {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := now.Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
