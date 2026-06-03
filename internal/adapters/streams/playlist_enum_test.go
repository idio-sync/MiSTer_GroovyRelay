package streams

import (
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
