package core

import (
	"path/filepath"
	"testing"
)

func TestFFmpegVisualizerSpecCarriesLiveTextDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "viz")
	got := ffmpegVisualizerSpec("", VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer, LiveTextDir: dir})
	if got.LiveTextDir != dir {
		t.Fatalf("LiveTextDir = %q, want %q", got.LiveTextDir, dir)
	}
}

func TestValidateVisualizerRequestRejectsRelativeLiveTextDir(t *testing.T) {
	req := SessionRequest{
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer, LiveTextDir: "viz"},
	}
	if err := validateVisualizerRequest(req); err == nil {
		t.Fatal("relative LiveTextDir accepted")
	}
	req.Visualizer.LiveTextDir = t.TempDir()
	if err := validateVisualizerRequest(req); err != nil {
		t.Fatalf("absolute LiveTextDir rejected: %v", err)
	}
}

func TestManager_UpdateNowPlayingIfSession(t *testing.T) {
	m := newTestManager(t)
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef:      "spotify:1",
		Title:           "Old",
		DisplayMetadata: DisplayMetadata{Primary: "OLD"},
	}, generation: 7}
	m.mu.Unlock()

	display := DisplayMetadata{Primary: "NEW TRACK", Secondary: "ARTIST", Tertiary: "ALBUM"}
	if m.UpdateNowPlayingIfSession("spotify:1", 6, "Stale", display) {
		t.Fatal("stale generation matched")
	}
	if m.UpdateNowPlayingIfSession("airplay:1", 7, "Other", display) {
		t.Fatal("other adapter ref matched")
	}
	if got := m.StatusHomeView(); got.Title != "Old" || got.Display.Primary != "OLD" {
		t.Fatalf("mismatched update changed the session: %+v", got.Display)
	}

	if !m.UpdateNowPlayingIfSession("spotify:1", 7, "New Track", display) {
		t.Fatal("matching session did not match")
	}
	if got := m.StatusHomeView(); got.Title != "New Track" || got.Display != display {
		t.Fatalf("view = %q %+v, want New Track %+v", got.Title, got.Display, display)
	}
}

func TestManager_UpdateNowPlayingIfSessionIdle(t *testing.T) {
	m := newTestManager(t)
	if m.UpdateNowPlayingIfSession("spotify:1", 1, "x", DisplayMetadata{}) {
		t.Fatal("idle manager matched")
	}
}
