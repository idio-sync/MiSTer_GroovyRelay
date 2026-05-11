package torrent

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type FileCandidate struct {
	DisplayPath string
	Length      int64
	Index       int
}

var playableExtensions = map[string]struct{}{
	".mp4":  {},
	".m4v":  {},
	".mkv":  {},
	".avi":  {},
	".mov":  {},
	".mpg":  {},
	".mpeg": {},
	".ts":   {},
	".webm": {},
	".wmv":  {},
}

func pickLargestPlayable(files []FileCandidate) (FileCandidate, error) {
	playable := make([]FileCandidate, 0, len(files))
	for _, f := range files {
		if _, ok := playableExtensions[strings.ToLower(filepath.Ext(f.DisplayPath))]; ok {
			playable = append(playable, f)
		}
	}
	if len(playable) == 0 {
		return FileCandidate{}, &TorrentError{Kind: ErrNoPlayableFile, Message: "torrent contains no playable video file"}
	}
	sort.SliceStable(playable, func(i, j int) bool {
		if playable[i].Length != playable[j].Length {
			return playable[i].Length > playable[j].Length
		}
		if playable[i].DisplayPath != playable[j].DisplayPath {
			return playable[i].DisplayPath < playable[j].DisplayPath
		}
		return playable[i].Index < playable[j].Index
	})
	return playable[0], nil
}

func sanitizeTitle(displayPath string) string {
	clean := strings.ReplaceAll(displayPath, "\\", "/")
	base := filepath.Base(clean)
	var b strings.Builder
	for _, r := range base {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "Torrent video"
	}
	return out
}
