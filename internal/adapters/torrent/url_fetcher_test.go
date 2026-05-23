package torrent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/sourcefetch"
)

func TestTorrentURLAcceptPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		originalURL string
		finalURL    string
		contentType string
		want        bool
	}{
		{
			name:        "original torrent path accepts octet stream",
			originalURL: "https://example.com/file.torrent",
			finalURL:    "https://example.com/file.bin",
			contentType: "application/octet-stream",
			want:        true,
		},
		{
			name:        "final torrent path accepts octet stream",
			originalURL: "https://example.com/download",
			finalURL:    "https://example.com/file.torrent",
			contentType: "application/octet-stream",
			want:        true,
		},
		{
			name:        "bittorrent media type accepts",
			originalURL: "https://example.com/download",
			finalURL:    "https://example.com/download",
			contentType: "Application/X-Bittorrent; charset=binary",
			want:        true,
		},
		{
			name:        "octet stream without torrent path rejects",
			originalURL: "https://example.com/download",
			finalURL:    "https://example.com/file.bin",
			contentType: "application/octet-stream",
			want:        false,
		},
		{
			name:        "ordinary binary video rejects",
			originalURL: "https://example.com/file.mp4",
			finalURL:    "https://example.com/file.mp4",
			contentType: "video/mp4",
			want:        false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := torrentURLAcceptable(tc.originalURL, tc.finalURL, tc.contentType); got != tc.want {
				t.Fatalf("torrentURLAcceptable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTorrentURLFetcherRejectsUnsafeInputBeforeSharedFetch(t *testing.T) {
	t.Parallel()

	tests := []string{
		"ftp://example.com/file.torrent",
		"https://user:pass@example.com/file.torrent",
		"http://[::1]/file.torrent",
	}

	for _, rawURL := range tests {
		rawURL := rawURL
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()

			source := &recordingSourceFetcher{}
			fetcher := torrentURLHTTPFetcher{source: source}

			_, err := fetcher.FetchTorrentURL(context.Background(), rawURL, maxTorrentUploadBytes)
			if err == nil {
				t.Fatal("FetchTorrentURL() error = nil, want unsafe input rejection")
			}
			if source.calls != 0 {
				t.Fatalf("source calls = %d, want 0", source.calls)
			}
		})
	}
}

func TestTorrentURLFetcherRequiresHeadForNonTorrentPath(t *testing.T) {
	t.Parallel()

	source := &recordingSourceFetcher{
		headErr: errors.New("head not supported"),
	}
	fetcher := torrentURLHTTPFetcher{source: source}

	_, err := fetcher.FetchTorrentURL(context.Background(), "https://example.com/download", maxTorrentUploadBytes)
	if err == nil {
		t.Fatal("FetchTorrentURL() error = nil, want HEAD requirement error")
	}
	if !strings.Contains(err.Error(), "must end in .torrent or support HEAD") {
		t.Fatalf("FetchTorrentURL() error = %q, want actionable HEAD message", err)
	}
	if source.heads != 1 {
		t.Fatalf("HEAD calls = %d, want 1", source.heads)
	}
	if source.gets != 0 {
		t.Fatalf("GET calls = %d, want 0", source.gets)
	}
}

func TestTorrentURLFetcherUsesCappedGetForTorrentPath(t *testing.T) {
	t.Parallel()

	source := &recordingSourceFetcher{
		getResp: sourcefetch.Response{
			Body:        []byte("torrent bytes"),
			FinalURL:    "https://cdn.example.com/file.torrent",
			ContentType: "application/octet-stream",
		},
	}
	fetcher := torrentURLHTTPFetcher{source: source}

	result, err := fetcher.FetchTorrentURL(context.Background(), "https://example.com/file.torrent", maxTorrentUploadBytes)
	if err != nil {
		t.Fatalf("FetchTorrentURL() error = %v", err)
	}
	if source.heads != 0 {
		t.Fatalf("HEAD calls = %d, want 0", source.heads)
	}
	if source.gets != 1 {
		t.Fatalf("GET calls = %d, want 1", source.gets)
	}
	if source.lastLimit.MaxBytes != maxTorrentUploadBytes {
		t.Fatalf("MaxBytes = %d, want %d", source.lastLimit.MaxBytes, maxTorrentUploadBytes)
	}
	if string(result.Body) != "torrent bytes" {
		t.Fatalf("Body = %q, want torrent bytes", result.Body)
	}
	if result.FinalURL != "https://cdn.example.com/file.torrent" {
		t.Fatalf("FinalURL = %q, want redirected torrent URL", result.FinalURL)
	}
	if result.ContentType != "application/octet-stream" {
		t.Fatalf("ContentType = %q, want application/octet-stream", result.ContentType)
	}
}

func TestTorrentURLFetcherAllowsPublicHTTP(t *testing.T) {
	t.Parallel()

	source := &recordingSourceFetcher{
		getResp: sourcefetch.Response{
			Body:        []byte("torrent"),
			FinalURL:    "http://example.com/file.torrent",
			ContentType: "application/octet-stream",
		},
	}
	fetcher := torrentURLHTTPFetcher{source: source}

	if _, err := fetcher.FetchTorrentURL(context.Background(), "http://example.com/file.torrent", maxTorrentUploadBytes); err != nil {
		t.Fatalf("FetchTorrentURL() error = %v", err)
	}
}

func TestTorrentURLFetcherRejectsOversizedSharedFetch(t *testing.T) {
	t.Parallel()

	source := &recordingSourceFetcher{
		getErr: sourcefetch.ErrBodyTooLarge,
	}
	fetcher := torrentURLHTTPFetcher{source: source}

	_, err := fetcher.FetchTorrentURL(context.Background(), "https://example.com/file.torrent", maxTorrentUploadBytes)
	if err == nil {
		t.Fatal("FetchTorrentURL() error = nil, want upload too large")
	}
	var terr *TorrentError
	if !errors.As(err, &terr) {
		t.Fatalf("FetchTorrentURL() error type = %T, want *TorrentError", err)
	}
	if terr.Kind != ErrUploadTooLarge {
		t.Fatalf("TorrentError kind = %s, want %s", terr.Kind, ErrUploadTooLarge)
	}
}

func TestTorrentURLFetcherErrorsDoNotLeakSensitiveURLParts(t *testing.T) {
	t.Parallel()

	source := &recordingSourceFetcher{
		getErr: errors.New("failed to fetch https://example.com/file.torrent?token=secret"),
	}
	fetcher := torrentURLHTTPFetcher{source: source}

	_, err := fetcher.FetchTorrentURL(context.Background(), "https://example.com/file.torrent?token=secret", maxTorrentUploadBytes)
	if err == nil {
		t.Fatal("FetchTorrentURL() error = nil, want fetch failure")
	}
	if strings.Contains(err.Error(), "token=secret") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("FetchTorrentURL() error = %q, leaked sensitive query", err)
	}

	_, err = fetcher.FetchTorrentURL(context.Background(), "https://user:pass@example.com/file.torrent", maxTorrentUploadBytes)
	if err == nil {
		t.Fatal("FetchTorrentURL() credentialed URL error = nil, want rejection")
	}
	if strings.Contains(err.Error(), "user") || strings.Contains(err.Error(), "pass") {
		t.Fatalf("FetchTorrentURL() credentialed URL error = %q, leaked credentials", err)
	}
}

type recordingSourceFetcher struct {
	calls int
	heads int
	gets  int

	headResp sourcefetch.Response
	headErr  error
	getResp  sourcefetch.Response
	getErr   error

	lastLimit sourcefetch.Limits
}

func (f *recordingSourceFetcher) Fetch(ctx context.Context, method, rawURL string, limits sourcefetch.Limits, condition sourcefetch.Condition) (sourcefetch.Response, error) {
	f.calls++
	f.lastLimit = limits

	switch method {
	case http.MethodHead:
		f.heads++
		return f.headResp, f.headErr
	case http.MethodGet:
		f.gets++
		return f.getResp, f.getErr
	default:
		return sourcefetch.Response{}, errors.New("unexpected method")
	}
}
