package streams

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
)

func TestPlaylistItems_RoundTrip(t *testing.T) {
	t.Parallel()
	in := []StreamItem{
		{ID: "dQw4w9WgXcQ", Title: "First", URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", SourceID: "dQw4w9WgXcQ"},
		{ID: "abcdefghijk", Title: "Second", URL: "https://www.youtube.com/watch?v=abcdefghijk", SourceID: "abcdefghijk"},
	}
	raw, err := encodePlaylistItems(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodePlaylistItems(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].ID != "dQw4w9WgXcQ" || out[1].URL != "https://www.youtube.com/watch?v=abcdefghijk" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	for _, it := range out {
		if it.Direct {
			t.Fatalf("decoded item Direct=true, want false (playlist items resolve via yt-dlp)")
		}
	}
}

func TestPlaylistEntriesToItems_YouTubeURLCapAndSafety(t *testing.T) {
	t.Parallel()
	entries := []ytdlp.PlaylistEntry{
		{ID: "dQw4w9WgXcQ", URL: "dQw4w9WgXcQ", Title: "Yt"},             // ID → canonical watch URL
		{ID: "", URL: "abcdefghijk", Title: "URL bare ID"},                // bare URL ID → canonical watch URL
		{ID: "", URL: "https://example.com/vid.mp4", Title: "Generic"},    // generic safe URL kept
		{ID: "", URL: "file:///etc/passwd", Title: "File"},                // dropped: scheme
		{ID: "", URL: "https://user:pass@example.com/s", Title: "Creds"},  // dropped: userinfo
		{ID: "", URL: "http://127.0.0.1:8080/meta", Title: "Loopback"},    // dropped: blocked IP
		{ID: "", URL: "http://169.254.169.254/latest", Title: "Metadata"}, // dropped: link-local IP
		{ID: "", URL: "", Title: "Empty"},                                 // dropped
	}
	items := playlistEntriesToItems(entries, 0)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (unsafe/empty entries dropped): %+v", len(items), items)
	}
	if items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" || items[0].SourceID != "dQw4w9WgXcQ" || items[0].Direct {
		t.Fatalf("items[0] = %+v", items[0])
	}
	if items[1].URL != "https://www.youtube.com/watch?v=abcdefghijk" || items[1].ID != "abcdefghijk" {
		t.Fatalf("items[1] = %+v", items[1])
	}
	if items[2].URL != "https://example.com/vid.mp4" || items[2].ID != "https://example.com/vid.mp4" {
		t.Fatalf("items[2] = %+v", items[2])
	}
	for _, it := range items {
		if strings.Contains(it.URL, "127.0.0.1") || strings.Contains(it.URL, "169.254.169.254") || strings.HasPrefix(it.URL, "file:") {
			t.Fatalf("unsafe item leaked through: %+v", it)
		}
	}
	if capped := playlistEntriesToItems(entries, 1); len(capped) != 1 {
		t.Fatalf("capped = %d, want 1", len(capped))
	}
}

func TestPlaylistEntriesToItems_YouTubeIDIgnoresHostileURL(t *testing.T) {
	t.Parallel()
	items := playlistEntriesToItems([]ytdlp.PlaylistEntry{
		{ID: "dQw4w9WgXcQ", URL: "http://169.254.169.254/latest", Title: "YT id + hostile url"},
	}, 0)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("hostile url not ignored; got %q", items[0].URL)
	}
}

func TestPlaylistItems_CodecEdgeCases(t *testing.T) {
	t.Parallel()
	for _, in := range [][]StreamItem{nil, {}} {
		raw, err := encodePlaylistItems(in)
		if err != nil {
			t.Fatalf("encode(%v): %v", in, err)
		}
		out, err := decodePlaylistItems(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out) != 0 {
			t.Fatalf("round-trip len = %d, want 0", len(out))
		}
	}
	for _, body := range []string{"[]", "null"} {
		out, err := decodePlaylistItems([]byte(body))
		if err != nil {
			t.Fatalf("decode(%q): %v", body, err)
		}
		if len(out) != 0 {
			t.Fatalf("decode(%q) len = %d, want 0", body, len(out))
		}
	}
	raw, err := encodePlaylistItems([]StreamItem{{ID: "x", URL: "https://e/x", SourceID: "x", Direct: true}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodePlaylistItems(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].Direct {
		t.Fatalf("decoded item must be Direct:false, got %+v", out)
	}
}

func ytEntries(ids ...string) []ytdlp.PlaylistEntry {
	out := make([]ytdlp.PlaylistEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, ytdlp.PlaylistEntry{ID: id, URL: id, Title: id})
	}
	return out
}

func TestEnumerator_LiveEnumeratesCachesAndIsServedFromCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pageURL := "https://youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		pageURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	cfg := DefaultConfig()
	live := userPlaylistEnumerator{resolver: fr, cacheDir: dir, cfg: cfg}

	items, err := live.channelItems(context.Background(), "user:mix", "list", pageURL)
	if err != nil {
		t.Fatalf("live channelItems: %v", err)
	}
	if len(items) != 2 || items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("live items = %+v", items)
	}
	if fr.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1", fr.enumCalls)
	}

	// A cache-only enumerator (resolver nil) now serves the written cache.
	cacheOnly := userPlaylistEnumerator{cacheDir: dir, cfg: cfg}
	cached, err := cacheOnly.channelItems(context.Background(), "user:mix", "list", pageURL)
	if err != nil {
		t.Fatalf("cache-only channelItems: %v", err)
	}
	if len(cached) != 2 {
		t.Fatalf("cache-only items = %d, want 2 (served from cache)", len(cached))
	}
}

func TestEnumerator_CacheOnlyEmptyWhenUncached(t *testing.T) {
	t.Parallel()
	cacheOnly := userPlaylistEnumerator{cacheDir: t.TempDir(), cfg: DefaultConfig()}
	items, err := cacheOnly.channelItems(context.Background(), "user:mix", "list", "https://youtube.com/playlist?list=PL1")
	if err != nil {
		t.Fatalf("channelItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("uncached cache-only items = %d, want 0", len(items))
	}
}

func TestEnumerator_ServeStaleOnLiveFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pageURL := "https://youtube.com/playlist?list=PL1"

	// Seed the cache via a successful live run.
	ok := userPlaylistEnumerator{
		resolver: &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{pageURL: ytEntries("dQw4w9WgXcQ")}},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	if _, err := ok.channelItems(context.Background(), "user:mix", "list", pageURL); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A subsequent live run that fails must serve the stale cache AND report
	// the error (for logging), not empty the channel.
	failing := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("yt-dlp: playlist temporarily unavailable")},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	items, err := failing.channelItems(context.Background(), "user:mix", "list", pageURL)
	if err == nil {
		t.Fatal("err = nil, want the transient enumerate error surfaced for logging")
	}
	if len(items) != 1 {
		t.Fatalf("serve-stale items = %d, want 1 (prior cache retained)", len(items))
	}
}

func TestEnumerator_LiveFailureNoCacheReturnsError(t *testing.T) {
	t.Parallel()
	failing := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("yt-dlp: private playlist")},
		cacheDir: t.TempDir(), cfg: DefaultConfig(),
	}
	items, err := failing.channelItems(context.Background(), "user:mix", "list", "https://youtube.com/playlist?list=PL1")
	if err == nil {
		t.Fatal("err = nil, want error when enumeration fails with no cache")
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0", len(items))
	}
}

func TestPlaylistErrorForLog_RedactsPageURL(t *testing.T) {
	t.Parallel()
	pageURL := "https://example.com/playlist?token=secret"
	got := playlistErrorForLog(fmt.Errorf("yt-dlp failed for %s", pageURL), pageURL)
	if strings.Contains(got, "token=secret") || strings.Contains(got, pageURL) {
		t.Fatalf("playlist error log leaked page URL/query: %q", got)
	}
	if !strings.Contains(got, "https://example.com/playlist") {
		t.Fatalf("playlist error log lost useful URL context: %q", got)
	}
}

func TestEnumerator_ChangedURLInvalidatesCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Seed the cache for URL-A via a successful live run.
	seed := userPlaylistEnumerator{
		resolver: &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
			"https://youtube.com/playlist?list=A": ytEntries("dQw4w9WgXcQ"),
		}},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	if _, err := seed.channelItems(context.Background(), "user:mix", "list", "https://youtube.com/playlist?list=A"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Same provider/channel, but the channel URL changed to B and live enumeration fails.
	// Must NOT serve the URL-A cache (cache key matches on SourceURL); expect no items + error.
	failing := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("yt-dlp: down")},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	items, err := failing.channelItems(context.Background(), "user:mix", "list", "https://youtube.com/playlist?list=B")
	if err == nil {
		t.Fatal("err = nil, want error (no valid cache for the new URL)")
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 (URL-A cache must NOT be served for URL-B)", len(items))
	}
}
