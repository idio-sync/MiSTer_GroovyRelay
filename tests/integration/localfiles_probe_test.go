//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/extbin"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func TestLocalFilesProbeUnderPolicy(t *testing.T) {
	selfDir := testSelfDir(t)
	ffmpegPath := resolveBinaryOrSkip(t, extbin.New("ffmpeg", "", selfDir))
	ffprobePath := resolveBinaryOrSkip(t, extbin.New("ffprobe", "", selfDir))
	fixture := generateTinyAudioFixture(t, ffmpegPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	probe, err := ffmpeg.Probe(ctx, ffprobePath, fixture, ffmpeg.MediaInputPolicy{
		ProtocolWhitelist: []string{"file"},
		DisableRedirects:  true,
		DisablePlaylists:  true,
	})
	if err != nil {
		t.Fatalf("probe local audio fixture: %v", err)
	}
	if probe.Duration <= 0 {
		t.Fatalf("Duration = %v, want > 0", probe.Duration)
	}
	if probe.AudioRate <= 0 {
		t.Fatalf("AudioRate = %d, want > 0", probe.AudioRate)
	}
}

func generateTinyAudioFixture(t *testing.T, ffmpegPath string) string {
	t.Helper()

	fixture := filepath.Join(t.TempDir(), "tiny.wav")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration=0.25:sample_rate=48000",
		"-ac", "1",
		"-ar", "48000",
		"-c:a", "pcm_s16le",
		fixture,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate audio fixture: %v\n%s", err, out)
	}
	return fixture
}

func resolveBinaryOrSkip(t *testing.T, resolver interface{ Resolve() (string, error) }) string {
	t.Helper()

	path, err := resolver.Resolve()
	if err != nil {
		t.Skip(err)
	}
	return path
}

func testSelfDir(t *testing.T) string {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}
