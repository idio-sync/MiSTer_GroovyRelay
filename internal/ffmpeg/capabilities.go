package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func FilterAvailable(ctx context.Context, ffmpegPath, filterName string) (bool, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-filters")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("ffmpeg filters: %w", err)
	}
	return filterListContains(string(out), filterName), nil
}

func filterListContains(output, filterName string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == filterName {
			return true
		}
	}
	return false
}

var filterAvailableFn = FilterAvailable

func withVisualizerCapabilities(ctx context.Context, s PipelineSpec) PipelineSpec {
	if !s.Visualizer.Enabled {
		return s
	}
	ffmpegPath := s.FFmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ok, err := filterAvailableFn(checkCtx, ffmpegPath, "drawtext")
	if err == nil && ok {
		s.Visualizer.DrawTextAvailable = true
		return s
	}
	s.Visualizer.DrawTextAvailable = false
	return s
}
