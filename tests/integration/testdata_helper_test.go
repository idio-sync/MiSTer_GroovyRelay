//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
