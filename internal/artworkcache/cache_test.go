package artworkcache

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngWithSize(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngHeaderOnly(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var out bytes.Buffer
	out.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	writePNGChunk(t, &out, "IHDR", func() []byte {
		data := make([]byte, 13)
		binary.BigEndian.PutUint32(data[0:4], w)
		binary.BigEndian.PutUint32(data[4:8], h)
		data[8] = 8
		data[9] = 2
		return data
	}())
	writePNGChunk(t, &out, "IEND", nil)
	return out.Bytes()
}

func writePNGChunk(t *testing.T, out *bytes.Buffer, typ string, data []byte) {
	t.Helper()
	if len(typ) != 4 {
		t.Fatalf("png chunk type %q must be 4 bytes", typ)
	}
	if err := binary.Write(out, binary.BigEndian, uint32(len(data))); err != nil {
		t.Fatal(err)
	}
	out.WriteString(typ)
	out.Write(data)
	sum := crc32.NewIEEE()
	_, _ = sum.Write([]byte(typ))
	_, _ = sum.Write(data)
	if err := binary.Write(out, binary.BigEndian, sum.Sum32()); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSameOriginAnchorsRelativeAndRejectsForeign(t *testing.T) {
	got, ok := ResolveSameOrigin("http://media.local:32400", "/library/art/1")
	if !ok {
		t.Fatal("relative candidate rejected")
	}
	if got.String() != "http://media.local:32400/library/art/1" {
		t.Fatalf("resolved = %s", got)
	}
	if _, ok := ResolveSameOrigin("http://media.local:32400", "https://evil.example/art.png"); ok {
		t.Fatal("foreign absolute candidate accepted")
	}
}

func TestResolveSameOriginEffectiveDefaultPorts(t *testing.T) {
	cases := []struct {
		server    string
		candidate string
	}{
		{"http://MEDIA.local", "http://media.local:80/art.png"},
		{"https://media.local:443", "https://MEDIA.local/art.png"},
	}
	for _, tc := range cases {
		if _, ok := ResolveSameOrigin(tc.server, tc.candidate); !ok {
			t.Fatalf("ResolveSameOrigin(%q, %q) rejected equivalent default port origin", tc.server, tc.candidate)
		}
	}
	if _, ok := ResolveSameOrigin("http://media.local", "http://media.local:81/art.png"); ok {
		t.Fatal("different effective port accepted")
	}
}

func TestAppendTokenAfterSameOriginResolution(t *testing.T) {
	u, ok := ResolveSameOrigin("http://media.local:32400", "/art.png?size=large")
	if !ok {
		t.Fatal("candidate rejected")
	}
	got := AppendToken(u, "X-Plex-Token", "secret")
	if !strings.Contains(got, "size=large") || !strings.Contains(got, "X-Plex-Token=secret") {
		t.Fatalf("AppendToken = %s", got)
	}
	without := AppendToken(u, "", "secret")
	if without != u.String() {
		t.Fatalf("empty key changed URL: %s", without)
	}
}

func TestRedactURL(t *testing.T) {
	raw := "http://user:pass@example.test/art.png?X-Plex-Token=a&API_KEY=b&X-Emby-Token=c&AccessToken=d&access_token=e&auth_token=f&TOKEN=g&safe=h"
	got := RedactURL(raw)
	if strings.Contains(got, "user:pass") || strings.Contains(got, "=a") || strings.Contains(got, "=b") ||
		strings.Contains(got, "=c") || strings.Contains(got, "=d") || strings.Contains(got, "=e") ||
		strings.Contains(got, "=f") || strings.Contains(got, "=g") {
		t.Fatalf("URL was not redacted: %s", got)
	}
	if !strings.Contains(got, "safe=h") {
		t.Fatalf("safe query key was removed: %s", got)
	}
	if got := RedactURL("http://example.test/%zz"); got != "<unparseable URL>" {
		t.Fatalf("unparseable redaction = %q", got)
	}
}

func TestValidatePathRejectsEscapesAndAcceptsCachedFile(t *testing.T) {
	dataDir := t.TempDir()
	root, err := EnsureRoot(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "cover.png")
	if err := os.WriteFile(inside, tinyPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := ValidatePath(root, inside)
	if !ok || got == "" {
		t.Fatalf("ValidatePath inside = %q, %v", got, ok)
	}
	for _, bad := range []string{"", root, filepath.Join(dataDir, "outside.png")} {
		if bad != "" {
			_ = os.WriteFile(bad, []byte("x"), 0o600)
		}
		if got, ok := ValidatePath(root, bad); ok {
			t.Fatalf("ValidatePath(%q) = %q, true; want false", bad, got)
		}
	}
	outside := filepath.Join(dataDir, "secret.png")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got, ok := ValidatePath(root, link); ok {
		t.Fatalf("symlink escape validated as %q", got)
	}
}

func TestFetchToCacheRejectsOversizedContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8388609")
		_, _ = w.Write(tinyPNG(t))
	}))
	defer srv.Close()

	if _, err := FetchToCache(context.Background(), FetchOptions{DataDir: t.TempDir(), URL: srv.URL}); err == nil {
		t.Fatal("FetchToCache accepted oversized content length")
	}
}

func TestFetchToCacheRejectsOversizedBodyWithoutContentLength(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", MaxBytes+1))
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:       http.StatusOK,
			Status:           "200 OK",
			Header:           make(http.Header),
			ContentLength:    -1,
			Body:             io.NopCloser(body),
			Request:          req,
			Proto:            "HTTP/1.1",
			ProtoMajor:       1,
			ProtoMinor:       1,
			TransferEncoding: []string{"chunked"},
		}, nil
	})}

	_, err := FetchToCache(context.Background(), FetchOptions{
		DataDir: t.TempDir(),
		URL:     "http://artwork.test/cover.png",
		Client:  client,
	})
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("FetchToCache oversized body err = %v, want response exceeds", err)
	}
}

func TestFetchToCacheRejectsOversizedDecodedDimensions(t *testing.T) {
	body := pngWithSize(t, MaxDimension+1, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	if _, err := FetchToCache(context.Background(), FetchOptions{DataDir: t.TempDir(), URL: srv.URL}); err == nil {
		t.Fatal("FetchToCache accepted oversized dimensions")
	}
}

func TestFetchToCacheRejectsOversizedDecodeConfigBeforeFullDecode(t *testing.T) {
	body := pngHeaderOnly(t, MaxDimension+1, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	_, err := FetchToCache(context.Background(), FetchOptions{DataDir: t.TempDir(), URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("FetchToCache oversized DecodeConfig err = %v, want dimensions error", err)
	}
}

func TestFetchToCacheRejectsRedirectWithoutFollowing(t *testing.T) {
	followed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			followed = true
			_, _ = w.Write(tinyPNG(t))
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer srv.Close()

	if _, err := FetchToCache(context.Background(), FetchOptions{DataDir: t.TempDir(), URL: srv.URL}); err == nil {
		t.Fatal("FetchToCache accepted redirect")
	}
	if followed {
		t.Fatal("FetchToCache followed redirect")
	}
}

func TestFetchToCacheRejectsCorruptImageBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not an image"))
	}))
	defer srv.Close()

	if _, err := FetchToCache(context.Background(), FetchOptions{DataDir: t.TempDir(), URL: srv.URL}); err == nil {
		t.Fatal("FetchToCache accepted corrupt image body")
	}
}

func TestFetchToCacheWritesValidPathUnderArtworkRoot(t *testing.T) {
	dataDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tinyPNG(t))
	}))
	defer srv.Close()

	path, err := FetchToCache(context.Background(), FetchOptions{DataDir: dataDir, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != Root(dataDir) || filepath.Ext(path) != ".png" {
		t.Fatalf("cached path = %q, want PNG under artwork root %q", path, Root(dataDir))
	}
	if _, ok := ValidatePath(Root(dataDir), path); !ok {
		t.Fatalf("cached path failed ValidatePath: %q", path)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := png.Decode(f); err != nil {
		t.Fatalf("cached file is not PNG: %v", err)
	}
}

func TestWithCleanupInvokesOriginalAndRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := ""
	WithCleanup(path, func(reason string) { called = reason })("stopped")
	if called != "stopped" {
		t.Fatalf("original reason = %q", called)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup stat err = %v, want not exist", err)
	}
}

func TestWithCleanupRemovesWhenOriginalPanics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup stat err = %v, want not exist", err)
		}
	}()
	WithCleanup(path, func(string) { panic("boom") })("error")
}

func TestRemoveIgnoresEmptyAndMissing(t *testing.T) {
	Remove("")
	Remove(filepath.Join(t.TempDir(), "missing.png"))
}

func TestReapStaleRemovesOldFilesOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artwork-cache")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldFile := filepath.Join(root, "old.png")
	newFile := filepath.Join(root, "new.png")
	subdir := filepath.Join(root, "subdir")
	for _, path := range []string{oldFile, newFile} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldFile, now.Add(-25*time.Hour), now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newFile, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := ReapStale(root, 24*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old file stat err = %v, want not exist", err)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("new file removed: %v", err)
	}
	if _, err := os.Stat(subdir); err != nil {
		t.Fatalf("subdir removed: %v", err)
	}
	if err := ReapStale(filepath.Join(root, "missing"), 24*time.Hour, now); err != nil {
		t.Fatalf("missing root error: %v", err)
	}
}

func TestReapStaleEmptyRootIsNoop(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "old.png")
	if err := os.WriteFile(oldFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(oldFile, now.Add(-25*time.Hour), now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := ReapStale("", 24*time.Hour, now); err != nil {
		t.Fatalf("ReapStale empty root error = %v, want nil", err)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("empty root reaped current directory file: %v", err)
	}
}

func TestResolveSameOriginRejectsInvalidInputs(t *testing.T) {
	if _, ok := ResolveSameOrigin("http://[::1", "/art.png"); ok {
		t.Fatal("invalid server accepted")
	}
	if _, ok := ResolveSameOrigin("http://example.test", "://bad"); ok {
		t.Fatal("invalid candidate accepted")
	}
	if _, err := url.Parse(RedactURL("http://example.test/?token=REDACTED")); err != nil {
		t.Fatalf("redacted URL should remain parseable: %v", err)
	}
}
