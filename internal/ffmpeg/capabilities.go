package ffmpeg

import (
	"context"
	"fmt"
	"log/slog"
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

// visualizerOverlayFiltersAvailable reports whether the ffmpeg binary
// exposes every filter required to render the overlay text layer. The
// missing filter (if any) is returned so callers can log it as the reason
// for downgrading to bars-only.
func visualizerOverlayFiltersAvailable(ctx context.Context, ffmpegPath string) (bool, string) {
	for _, filter := range RequiredVisualizerOverlayFilters() {
		ok, err := filterAvailableFn(ctx, ffmpegPath, filter)
		if err != nil || !ok {
			return false, filter
		}
	}
	return true, ""
}

// visualizerCapabilityProbeBudget bounds the total time a Spawn will
// spend probing ffmpeg for visualizer overlay capabilities. Five probes
// run sequentially (up to one required-filter probe, three overlay-filter
// probes, and the drawtext smoke render), and the smoke render alone has
// been measured at 200–800 ms on slow-disk cold starts. A 2 s budget was
// too tight in those conditions and produced silent fallbacks.
const visualizerCapabilityProbeBudget = 5 * time.Second

func withVisualizerCapabilities(ctx context.Context, s PipelineSpec) PipelineSpec {
	if !s.Visualizer.Enabled {
		return s
	}
	ffmpegPath := s.FFmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	checkCtx, cancel := context.WithTimeout(ctx, visualizerCapabilityProbeBudget)
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

	overlayOK, missing := visualizerOverlayFiltersAvailable(checkCtx, ffmpegPath)
	if overlayOK {
		ok, err := drawTextUsableFn(checkCtx, ffmpegPath)
		if err == nil && ok {
			s.Visualizer.DrawTextAvailable = true
			return s
		}
		slog.Warn("visualizer overlay text disabled: drawtext smoke probe failed",
			"ffmpeg_path", ffmpegPath,
			"mode", string(s.Visualizer.Mode),
			"err", err)
	} else {
		slog.Warn("visualizer overlay text disabled: required filter unavailable",
			"ffmpeg_path", ffmpegPath,
			"mode", string(s.Visualizer.Mode),
			"missing_filter", missing)
	}
	s.Visualizer.DrawTextAvailable = false
	return s
}
