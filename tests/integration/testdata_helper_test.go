//go:build integration

package integration

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

// testdataDir is the relative path that integration tests (running from the
// tests/integration package) use to resolve generated media.
const testdataDir = "testdata"

var (
	generateOnce sync.Mutex
	generated    = map[string]bool{}
)

// ensureSampleMP4 synthesises a short test video with ffmpeg's lavfi source
// when it is not already present on disk. The 5-second file is small
// (sub-MB at the generated bitrate) but we still do not check it in — the
// generator is deterministic and CI always has ffmpeg available.
//
// Returns the absolute path to the generated file. Calls t.Skip when
// ffmpeg is not on PATH so a dev machine without ffmpeg still gets a
// clean skip instead of a failure.
func ensureSampleMP4(t *testing.T, name string, durationSec int) string {
	t.Helper()

	absDir, err := filepath.Abs(testdataDir)
	if err != nil {
		t.Fatalf("abs testdata dir: %v", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	path := filepath.Join(absDir, name)

	generateOnce.Lock()
	defer generateOnce.Unlock()

	if generated[path] {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		generated[path] = true
		return path
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-f", "lavfi",
		"-i", "testsrc=duration="+itoa(durationSec)+":size=1920x1080:rate=24",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration="+itoa(durationSec)+":sample_rate=48000",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-c:a", "aac",
		"-t", itoa(durationSec),
		path,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate %s: %v", path, err)
	}
	generated[path] = true
	return path
}

func ensureSampleWAV(t *testing.T, name string, durationSec int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration="+itoa(durationSec)+":sample_rate=48000",
		"-ac", "2",
		"-ar", "48000",
		"-c:a", "pcm_s16le",
		path,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate %s: %v", path, err)
	}
	return path
}

func ensureSamplePNG(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)

	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 0x80, A: 0xff})
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("create sample PNG: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode sample PNG: %v", err)
	}
	return path
}

func ffmpegPathOrSkip(t *testing.T) string {
	t.Helper()

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	return ffmpegPath
}

func skipIfVisualizerFiltersMissing(t *testing.T, ffmpegPath string, mode ffmpeg.VisualizerMode) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ffmpeg.CheckVisualizerFilters(ctx, ffmpegPath, mode); err != nil {
		t.Skipf("ffmpeg %q lacks required filters for %s: %v", ffmpegPath, mode, err)
	}
}

// ensureSampleMP4Rate generates a silent/colour-bar MP4 at a specific frame
// rate. Used by filter-chain rate tests that need to exercise both film
// (23.976) and sports (60) sources end-to-end.
func ensureSampleMP4Rate(t *testing.T, name string, seconds int, rate string) string {
	t.Helper()

	absDir, err := filepath.Abs(testdataDir)
	if err != nil {
		t.Fatalf("abs testdata dir: %v", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	path := filepath.Join(absDir, name)

	generateOnce.Lock()
	defer generateOnce.Unlock()

	if generated[path] {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		generated[path] = true
		return path
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc=size=720x480:rate=%s:duration=%d", rate, seconds),
		"-f", "lavfi",
		"-i", fmt.Sprintf("sine=frequency=440:duration=%d", seconds),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast",
		"-c:a", "aac",
		"-shortest",
		path,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate %s: %v", path, err)
	}
	generated[path] = true
	return path
}

// itoa avoids pulling strconv in a test helper where we know the input is
// a small positive duration.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
