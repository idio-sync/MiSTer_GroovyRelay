package localfiles

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func TestBrowseListsDirectoriesThenPlayableFiles(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "Shows"))
	mustMkdir(t, filepath.Join(root, "albums"))
	mustWrite(t, filepath.Join(root, "movie.MKV"))
	mustWrite(t, filepath.Join(root, "song.mp3"))
	mustWrite(t, filepath.Join(root, "notes.txt"))
	mustWrite(t, filepath.Join(root, ".hidden.mp4"))

	a := newBrowsingAdapter(t, Config{Enabled: true, Libraries: []Library{{Name: "Media", Root: root}}})
	a.probe = func(_ context.Context, url string, _ ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		switch filepath.Base(url) {
		case "movie.MKV":
			return &ffmpeg.ProbeResult{Duration: 42, Width: 640, AudioRate: 48000}, nil
		case "song.mp3":
			return &ffmpeg.ProbeResult{Duration: 9, AudioRate: 44100}, nil
		default:
			return nil, os.ErrNotExist
		}
	}

	entries, err := a.Browse("media", "")
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	gotNames := entryNames(entries)
	wantNames := []string{"albums", "Shows", "movie.MKV", "song.mp3"}
	if !equalStrings(gotNames, wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}
	for _, entry := range entries {
		switch entry.Name {
		case "movie.MKV":
			if !entry.Playable || entry.DurationS != 42 || entry.AudioOnly {
				t.Fatalf("movie entry = %+v", entry)
			}
		case "song.mp3":
			if !entry.Playable || entry.DurationS != 9 || !entry.AudioOnly {
				t.Fatalf("song entry = %+v", entry)
			}
		case "albums", "Shows":
			if !entry.IsDir || entry.Playable {
				t.Fatalf("dir entry = %+v", entry)
			}
		}
	}
}

func TestBrowseSubdirectoryRelPaths(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "Shows"))
	mustWrite(t, filepath.Join(root, "Shows", "Episode.mp4"))

	a := newBrowsingAdapter(t, Config{Enabled: true, Libraries: []Library{{Name: "Media", Root: root}}})
	entries, err := a.Browse("Media", "Shows")
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(entries) != 1 || entries[0].Rel != filepath.ToSlash(filepath.Join("Shows", "Episode.mp4")) {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestBrowseErrorsUnknownLibraryAndTraversal(t *testing.T) {
	root := t.TempDir()
	a := newBrowsingAdapter(t, Config{Enabled: true, Libraries: []Library{{Name: "Media", Root: root}}})
	if _, err := a.Browse("Other", ""); err == nil {
		t.Fatalf("unknown library returned nil error")
	}
	if _, err := a.Browse("Media", "../escape"); err == nil {
		t.Fatalf("traversal returned nil error")
	}
}

func TestBrowseDropsEscapingSymlinkChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.mp4"))
	if err := os.Symlink(filepath.Join(outside, "secret.mp4"), filepath.Join(root, "secret.mp4")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	mustWrite(t, filepath.Join(root, "ok.mp4"))

	a := newBrowsingAdapter(t, Config{Enabled: true, Libraries: []Library{{Name: "Media", Root: root}}})
	entries, err := a.Browse("Media", "")
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if got := entryNames(entries); !equalStrings(got, []string{"ok.mp4"}) {
		t.Fatalf("names = %v, want [ok.mp4]", got)
	}
}

func TestBrowseProbeErrorsDoNotFailListing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "movie.mp4"))
	a := newBrowsingAdapter(t, Config{Enabled: true, Libraries: []Library{{Name: "Media", Root: root}}})
	a.probe = func(context.Context, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return nil, os.ErrPermission
	}
	entries, err := a.Browse("Media", "")
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(entries) != 1 || entries[0].DurationS != 0 || entries[0].AudioOnly {
		t.Fatalf("entry = %+v", entries)
	}
}

func TestProbeDefaultRequiresResolver(t *testing.T) {
	a := newBrowsingAdapter(t, Config{})
	a.ffprobe = nil
	if _, err := a.probeDefault(context.Background(), "/tmp/movie.mp4", localFilePolicy()); err == nil {
		t.Fatalf("probeDefault without resolver = nil error, want error")
	}
}

func TestBrowseRejectsDisabledAdapter(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "movie.mp4"))
	a := newBrowsingAdapter(t, Config{Enabled: false, Libraries: []Library{{Name: "Media", Root: root}}})
	if _, err := a.Browse("Media", ""); err == nil {
		t.Fatalf("Browse disabled adapter = nil error")
	}
}

func newBrowsingAdapter(t *testing.T, cfg Config) *Adapter {
	t.Helper()
	a := newTestAdapter(t, nil)
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return a
}

func entryNames(entries []BrowseEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBrowseSortIsCaseInsensitiveStableEnough(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"beta.mp4", "Alpha.mp4", "gamma.mp4"} {
		mustWrite(t, filepath.Join(root, name))
	}
	a := newBrowsingAdapter(t, Config{Enabled: true, Libraries: []Library{{Name: "Media", Root: root}}})
	entries, err := a.Browse("Media", "")
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	got := entryNames(entries)
	want := append([]string(nil), got...)
	sort.Slice(want, func(i, j int) bool { return lower(want[i]) < lower(want[j]) })
	if !equalStrings(got, want) {
		t.Fatalf("names = %v, want case-insensitive sorted %v", got, want)
	}
}
