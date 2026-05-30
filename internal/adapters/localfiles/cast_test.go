package localfiles

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func TestCastStartsVideoSession(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Movie.Name.mp4")
	mustWrite(t, path)
	fc := &recordingCore{}
	a := newCastAdapter(t, fc, Config{Enabled: true, Libraries: []Library{{Name: "Movies", Root: root}}})
	a.probe = func(context.Context, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 1920, AudioRate: 48000}, nil
	}

	if err := a.Cast(context.Background(), "movies", "Movie.Name.mp4"); err != nil {
		t.Fatalf("Cast: %v", err)
	}
	req := fc.lastReq(t)
	if req.StreamURL != path {
		t.Fatalf("StreamURL = %q, want %q", req.StreamURL, path)
	}
	if req.Source != "localfiles" || !strings.HasPrefix(req.AdapterRef, "localfiles:") {
		t.Fatalf("Source/AdapterRef = %q/%q", req.Source, req.AdapterRef)
	}
	if !req.DirectPlay || !req.Capabilities.CanSeek || !req.Capabilities.CanPause {
		t.Fatalf("request playback flags = %+v", req)
	}
	if req.MediaKind != core.MediaKindVideo {
		t.Fatalf("MediaKind = %q, want video", req.MediaKind)
	}
	if req.Title != "Movie.Name" || req.DisplayMetadata.Primary != "Movie.Name" || req.DisplayMetadata.Secondary != "Movies" {
		t.Fatalf("metadata title/display = %q %+v", req.Title, req.DisplayMetadata)
	}
	if req.Visualizer.Enabled {
		t.Fatalf("Visualizer enabled for video")
	}
	if req.MediaInputPolicy.DisableReconnect {
		t.Fatalf("DisableReconnect = true, want false")
	}
}

func TestCastStartsAudioVisualizerSession(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Song.flac"))
	fc := &recordingCore{}
	a := newCastAdapter(t, fc, Config{Enabled: true, Libraries: []Library{{Name: "Music", Root: root}}})
	a.probe = func(context.Context, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 44100}, nil
	}

	if err := a.Cast(context.Background(), "Music", "Song.flac"); err != nil {
		t.Fatalf("Cast: %v", err)
	}
	req := fc.lastReq(t)
	if req.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want music", req.MediaKind)
	}
	if !req.Visualizer.Enabled || req.Visualizer.Mode != core.VisualizerModeRetroAnalyzer {
		t.Fatalf("Visualizer = %+v, want retro analyzer enabled", req.Visualizer)
	}
	if req.Visualizer.Metadata.Title != "Song" || req.Visualizer.Metadata.ArtworkPath != "" {
		t.Fatalf("visualizer metadata = %+v", req.Visualizer.Metadata)
	}
}

func TestCastRejectsNonPlayableAndTraversalWithoutStartingSession(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "note.txt"))
	fc := &recordingCore{}
	a := newCastAdapter(t, fc, Config{Enabled: true, Libraries: []Library{{Name: "Docs", Root: root}}})

	if err := a.Cast(context.Background(), "Docs", "note.txt"); err == nil {
		t.Fatalf("Cast non-playable = nil error")
	}
	if err := a.Cast(context.Background(), "Docs", "../escape.mp4"); err == nil {
		t.Fatalf("Cast traversal = nil error")
	}
	if len(fc.reqs) != 0 {
		t.Fatalf("StartSession calls = %d, want 0", len(fc.reqs))
	}
}

func TestCastProbeErrorStillStartsVideoSession(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "movie.mp4"))
	fc := &recordingCore{}
	a := newCastAdapter(t, fc, Config{Enabled: true, Libraries: []Library{{Name: "Movies", Root: root}}})
	a.probe = func(context.Context, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return nil, errors.New("probe failed")
	}
	if err := a.Cast(context.Background(), "Movies", "movie.mp4"); err != nil {
		t.Fatalf("Cast: %v", err)
	}
	if got := fc.lastReq(t).MediaKind; got != core.MediaKindVideo {
		t.Fatalf("MediaKind = %q, want video fallback", got)
	}
}

func TestCastRejectsDisabledAdapterWithoutStartingSession(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "movie.mp4"))
	fc := &recordingCore{}
	a := newCastAdapter(t, fc, Config{Enabled: false, Libraries: []Library{{Name: "Movies", Root: root}}})

	if err := a.Cast(context.Background(), "Movies", "movie.mp4"); err == nil {
		t.Fatalf("Cast disabled adapter = nil error")
	}
	if len(fc.reqs) != 0 {
		t.Fatalf("StartSession calls = %d, want 0", len(fc.reqs))
	}
}

func TestRandHex8(t *testing.T) {
	got, err := randHex8()
	if err != nil {
		t.Fatalf("randHex8: %v", err)
	}
	if len(got) != 8 {
		t.Fatalf("len(randHex8) = %d, want 8", len(got))
	}
	for _, r := range got {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("randHex8 = %q, contains non-hex rune %q", got, r)
		}
	}
}

func newCastAdapter(t *testing.T, c *recordingCore, cfg Config) *Adapter {
	t.Helper()
	a := newTestAdapter(t, c)
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return a
}
