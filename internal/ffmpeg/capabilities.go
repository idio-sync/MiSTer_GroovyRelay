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

func CheckVisualizerFilters(ctx context.Context, ffmpegPath string, mode VisualizerMode) error {
	if !isSupportedVisualizerMode(mode) {
		return fmt.Errorf("unsupported visualizer mode %q", mode)
	}
	for _, filter := range RequiredVisualizerFilters(mode) {
		ok, err := filterAvailableFn(ctx, ffmpegPath, filter)
		if err != nil {
			return fmt.Errorf("check visualizer filter %q: %w", filter, err)
		}
		if !ok {
			return fmt.Errorf("required visualizer filter %q unavailable for mode %q", filter, mode)
		}
	}
	return nil
}

func DrawTextUsable(ctx context.Context, ffmpegPath string) (bool, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner",
		"-v", "error",
		"-f", "lavfi",
		"-i", "color=s=16x16:d=0.1",
		"-vf", "drawtext=text=test",
		"-frames:v", "1",
		"-f", "null",
		"-",
	)
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("ffmpeg drawtext smoke: %w", err)
	}
	return true, nil
}

var drawTextUsableFn = DrawTextUsable

func visualizerOverlayFiltersAvailable(ctx context.Context, ffmpegPath string) bool {
	for _, filter := range RequiredVisualizerOverlayFilters() {
		ok, err := filterAvailableFn(ctx, ffmpegPath, filter)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

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

	requiredOK := false
	if isSupportedVisualizerMode(s.Visualizer.Mode) {
		requiredOK = true
		for _, filter := range RequiredVisualizerFilters(s.Visualizer.Mode) {
			ok, err := filterAvailableFn(checkCtx, ffmpegPath, filter)
			if err != nil || !ok {
				requiredOK = false
				break
			}
		}
	}
	s.Visualizer.RequiredFiltersAvailable = requiredOK

	if visualizerOverlayFiltersAvailable(checkCtx, ffmpegPath) {
		ok, err := drawTextUsableFn(checkCtx, ffmpegPath)
		if err == nil && ok {
			s.Visualizer.DrawTextAvailable = true
			return s
		}
	}
	s.Visualizer.DrawTextAvailable = false
	return s
}
