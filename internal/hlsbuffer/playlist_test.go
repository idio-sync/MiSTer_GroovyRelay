package hlsbuffer

import (
	"strings"
	"testing"
	"time"
)

func TestParseMediaPlaylistAcceptsLiveSegments(t *testing.T) {
	const body = `#EXTM3U
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:42
#EXTINF:5.5,
seg-042.ts
#EXTINF:6,
https://cdn.example/live/seg-043.ts
`

	got, err := ParsePlaylist([]byte(body))
	if err != nil {
		t.Fatalf("ParsePlaylist: %v", err)
	}
	if got.Kind != PlaylistMedia {
		t.Fatalf("Kind = %v, want PlaylistMedia", got.Kind)
	}
	if got.Target != 6*time.Second {
		t.Errorf("Target = %v, want 6s", got.Target)
	}
	if got.MediaSeq != 42 {
		t.Errorf("MediaSeq = %d, want 42", got.MediaSeq)
	}
	if len(got.Segments) != 2 {
		t.Fatalf("Segments len = %d, want 2", len(got.Segments))
	}
	want := []Segment{
		{URI: "seg-042.ts", Duration: 5500 * time.Millisecond, Sequence: 42},
		{URI: "https://cdn.example/live/seg-043.ts", Duration: 6 * time.Second, Sequence: 43},
	}
	for i := range want {
		if got.Segments[i] != want[i] {
			t.Errorf("segment[%d] = %+v, want %+v", i, got.Segments[i], want[i])
		}
	}
}

func TestParseMediaPlaylistRejectsUnsupportedTags(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "encryption key",
			body: `#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="key.bin"
#EXTINF:4,
seg.ts
`,
			want: "#EXT-X-KEY",
		},
		{
			name: "byte range",
			body: `#EXTM3U
#EXTINF:4,
#EXT-X-BYTERANGE:10@0
seg.ts
`,
			want: "#EXT-X-BYTERANGE",
		},
		{
			name: "unknown uri-bearing tag",
			body: `#EXTM3U
#EXT-X-CONTENT-STEERING:SERVER-URI="https://cdn.example/steer.json"
#EXTINF:4,
seg.ts
`,
			want: "URI",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePlaylist([]byte(tc.body))
			if err == nil {
				t.Fatal("ParsePlaylist error = nil, want unsupported tag error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParsePlaylist error = %q, want mention %q", err, tc.want)
			}
		})
	}
}

func TestParseMediaPlaylistRejectsAudioOnly(t *testing.T) {
	const body = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg-000.aac
#EXTINF:4,
seg-001.aac
`

	_, err := ParsePlaylist([]byte(body))
	if err == nil {
		t.Fatal("ParsePlaylist error = nil, want audio-only rejection")
	}
	if !strings.Contains(err.Error(), "audio-only") {
		t.Fatalf("ParsePlaylist error = %q, want mention audio-only", err)
	}
}

func TestParseMasterPlaylistSelectsVariantByHeightThenBandwidth(t *testing.T) {
	const body = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,CODECS="avc1.64001f,mp4a.40.2"
360p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=854x480,CODECS="avc1.64001f,mp4a.40.2"
480p-high.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=854x480,CODECS="avc1.64001f,mp4a.40.2"
480p-low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2200000,RESOLUTION=1280x720,CODECS="avc1.64001f,mp4a.40.2"
720p.m3u8
`

	playlist, err := ParsePlaylist([]byte(body))
	if err != nil {
		t.Fatalf("ParsePlaylist: %v", err)
	}
	if playlist.Kind != PlaylistMaster {
		t.Fatalf("Kind = %v, want PlaylistMaster", playlist.Kind)
	}
	got, err := SelectVariant(playlist.Variants, 480, 720)
	if err != nil {
		t.Fatalf("SelectVariant: %v", err)
	}
	if got.URI != "480p-low.m3u8" {
		t.Fatalf("selected URI = %q, want 480p-low.m3u8", got.URI)
	}
}

func TestParseMasterPlaylistFallsBackWhenResolutionMissing(t *testing.T) {
	const body = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=2200000,CODECS="avc1.64001f,mp4a.40.2"
high.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,CODECS="avc1.64001f,mp4a.40.2"
low.m3u8
#EXT-X-STREAM-INF:CODECS="avc1.64001f,mp4a.40.2"
unknown.m3u8
`

	playlist, err := ParsePlaylist([]byte(body))
	if err != nil {
		t.Fatalf("ParsePlaylist: %v", err)
	}
	got, err := SelectVariant(playlist.Variants, 480, 720)
	if err != nil {
		t.Fatalf("SelectVariant: %v", err)
	}
	if got.URI != "low.m3u8" {
		t.Fatalf("selected URI = %q, want low.m3u8", got.URI)
	}

	first, err := SelectVariant([]Variant{
		{URI: "first.m3u8"},
		{URI: "second.m3u8"},
	}, 480, 720)
	if err != nil {
		t.Fatalf("SelectVariant without metadata: %v", err)
	}
	if first.URI != "first.m3u8" {
		t.Fatalf("selected URI without metadata = %q, want first.m3u8", first.URI)
	}
}
