// Package ffmpeg provides ffprobe/ffmpeg wrappers used by the groovy relay
// data plane. Task 5.1: probe a media URL and return a structured view of
// its first video/audio streams.
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ProbeResult is the subset of ffprobe output the pipeline cares about.
type ProbeResult struct {
	Width      int
	Height     int
	FrameRate  float64
	Interlaced bool
	AudioRate  int
	Duration   float64
}

// ffprobeOutput mirrors the JSON shape of `ffprobe -print_format json`.
type ffprobeOutput struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		FieldOrder string `json:"field_order"`
		RFrameRate string `json:"r_frame_rate"`
		SampleRate string `json:"sample_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe runs `ffprobe` against url and returns a ProbeResult. The caller is
// responsible for supplying any authentication tokens via URL query params —
// ffprobe's `-headers` is not threaded through here because the production
// callers (Plex transcode URLs) already embed credentials in the URL.
func Probe(ctx context.Context, url string) (*ProbeResult, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
		url,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbeOutput(out)
}

// parseProbeOutput is split out so unit tests can exercise the JSON mapping
// without invoking ffprobe.
func parseProbeOutput(raw []byte) (*ProbeResult, error) {
	var p ffprobeOutput
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse ffprobe: %w", err)
	}
	r := &ProbeResult{}
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if r.Width == 0 {
				r.Width = s.Width
				r.Height = s.Height
				r.FrameRate = parseFrameRate(s.RFrameRate)
				r.Interlaced = s.FieldOrder == "tt" || s.FieldOrder == "bb" ||
					s.FieldOrder == "tb" || s.FieldOrder == "bt"
			}
		case "audio":
			if r.AudioRate == 0 {
				fmt.Sscan(s.SampleRate, &r.AudioRate)
			}
		}
	}
	fmt.Sscan(p.Format.Duration, &r.Duration)
	return r, nil
}

// parseFrameRate turns "30000/1001" or "24/1" into a float.
func parseFrameRate(s string) float64 {
	var num, den float64
	if _, err := fmt.Sscanf(s, "%f/%f", &num, &den); err == nil && den != 0 {
		return num / den
	}
	return 0
}
