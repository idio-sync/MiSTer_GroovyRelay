package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// FFmpeg 9 made -thread_queue_size a muxer-only option: placed before an -i
// it aborts with "cannot be applied to input url", so every capture/live-audio
// session failed to start. Older FFmpeg needs it there to keep capture
// devices from dropping packets, so probe the binary instead of dropping it.

const inputThreadQueueProbeTimeout = 5 * time.Second

// inputThreadQueueProbeCache holds definite probe results per ffmpeg path.
var inputThreadQueueProbeCache sync.Map // string -> bool

// InputThreadQueueSizeUsable reports whether ffmpegPath accepts
// -thread_queue_size as an input option. Results are cached per path. On an
// inconclusive probe it returns true with the error so callers keep the
// pre-probe behavior.
func InputThreadQueueSizeUsable(ctx context.Context, ffmpegPath string) (bool, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if v, ok := inputThreadQueueProbeCache.Load(ffmpegPath); ok {
		return v.(bool), nil
	}
	ctx, cancel := context.WithTimeout(ctx, inputThreadQueueProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner",
		"-v", "error",
		"-thread_queue_size", "8",
		"-f", "lavfi",
		"-i", "anullsrc=d=0.01",
		"-f", "null",
		"-",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	usable, err := classifyInputThreadQueueProbe(cmd.Run(), stderr.String())
	if err != nil {
		return usable, err
	}
	if _, loaded := inputThreadQueueProbeCache.LoadOrStore(ffmpegPath, usable); !loaded && !usable {
		slog.Info("ffmpeg rejects -thread_queue_size on inputs (FFmpeg 9+); omitting it for capture inputs",
			"ffmpeg_path", ffmpegPath)
	}
	return usable, nil
}

func classifyInputThreadQueueProbe(runErr error, stderr string) (bool, error) {
	if runErr == nil {
		return true, nil
	}
	if strings.Contains(stderr, "thread_queue_size") && strings.Contains(stderr, "cannot be applied to input") {
		return false, nil
	}
	return true, fmt.Errorf("ffmpeg -thread_queue_size input probe: %w: %s",
		runErr, strings.TrimSpace(stderr))
}

var inputThreadQueueSizeUsableFn = InputThreadQueueSizeUsable

// withCaptureInputCapabilities drops CaptureInput.ThreadQueueSize when the
// ffmpeg binary cannot take it as an input option.
func withCaptureInputCapabilities(ctx context.Context, s PipelineSpec) PipelineSpec {
	if !s.CaptureInput.Enabled || s.CaptureInput.ThreadQueueSize <= 0 {
		return s
	}
	usable, err := inputThreadQueueSizeUsableFn(ctx, s.FFmpegPath)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("ffmpeg -thread_queue_size probe inconclusive; keeping it",
			"ffmpeg_path", ffmpegPathFor(s), "err", err)
	}
	if !usable {
		s.CaptureInput.ThreadQueueSize = 0
	}
	return s
}
