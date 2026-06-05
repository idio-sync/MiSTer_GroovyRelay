package localfiles

import (
	"reflect"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func TestIsPlayableAllowsDirectContainers(t *testing.T) {
	for _, name := range []string{
		"movie.mkv", "movie.MP4", "clip.m4v", "clip.mov", "clip.avi", "clip.webm",
		"clip.ts", "clip.mpg", "clip.mpeg", "clip.wmv", "song.flac", "song.mp3",
		"song.m4a", "song.aac", "song.ogg", "song.opus", "song.wav",
	} {
		if !isPlayable(name) {
			t.Fatalf("isPlayable(%q) = false, want true", name)
		}
	}
}

func TestIsPlayableRejectsPlaylistsSidecarsAndText(t *testing.T) {
	for _, name := range []string{
		"list.m3u", "list.m3u8", "list.pls", "list.xspf", "stream.strm", "site.url",
		"session.sdp", "show.smil", "concat.ffconcat", "disc.cue", "cut.edl",
		"subs.srt", "subs.ass", "note.txt", "unknown.bin",
	} {
		if isPlayable(name) {
			t.Fatalf("isPlayable(%q) = true, want false", name)
		}
	}
}

func TestLocalFilePolicy(t *testing.T) {
	got := localFilePolicy()
	want := ffmpeg.MediaInputPolicy{
		ProtocolWhitelist: []string{"file"},
		DisableRedirects:  true,
		DisablePlaylists:  true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
	if got.DisableReconnect {
		t.Fatalf("DisableReconnect = true, want false")
	}
}
