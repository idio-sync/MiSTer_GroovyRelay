package localfiles

import (
	"path/filepath"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

var playableExts = map[string]struct{}{
	".mkv": {}, ".mp4": {}, ".m4v": {}, ".mov": {}, ".avi": {}, ".webm": {},
	".ts": {}, ".mpg": {}, ".mpeg": {}, ".wmv": {}, ".flac": {}, ".mp3": {},
	".m4a": {}, ".aac": {}, ".ogg": {}, ".opus": {}, ".wav": {},
}

var rejectedExts = map[string]struct{}{
	".m3u": {}, ".m3u8": {}, ".pls": {}, ".xspf": {}, ".strm": {}, ".url": {},
	".sdp": {}, ".smil": {}, ".ffconcat": {}, ".cue": {}, ".edl": {}, ".srt": {},
	".ass": {}, ".txt": {},
}

func isPlayable(name string) bool {
	ext := lower(filepath.Ext(name))
	if _, rejected := rejectedExts[ext]; rejected {
		return false
	}
	_, ok := playableExts[ext]
	return ok
}

func localFilePolicy() ffmpeg.MediaInputPolicy {
	return ffmpeg.MediaInputPolicy{
		ProtocolWhitelist: []string{"file"},
		DisableRedirects:  true,
		DisablePlaylists:  true,
	}
}

func lower(s string) string {
	return strings.ToLower(s)
}
