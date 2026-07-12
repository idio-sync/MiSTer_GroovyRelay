// Package ffmpeg provides ffprobe/ffmpeg wrappers used by the groovy relay
// data plane. Task 5.1: probe a media URL and return a structured view of
// its first video/audio streams.
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const probeWaitDelay = time.Second
const defaultCaptureProbeTimeout = 3 * time.Second

// ProbeResult is the subset of ffprobe output the pipeline cares about.
type ProbeResult struct {
	Width                 int
	Height                int
	FrameRate             float64
	Interlaced            bool
	AudioRate             int
	Duration              float64
	VideoCodec            string
	AudioCodec            string
	AudioChannels         int
	SampleAspectRatioNum  int
	SampleAspectRatioDen  int
	DisplayAspectRatioNum int
	DisplayAspectRatioDen int
	VideoBitrateBPS       int64
	AudioBitrateBPS       int64
	FormatBitrateBPS      int64
	AttachedPicture       bool
}

// ffprobeOutput mirrors the JSON shape of `ffprobe -print_format json`.
type ffprobeOutput struct {
	Streams []struct {
		CodecType          string `json:"codec_type"`
		CodecName          string `json:"codec_name"`
		Width              int    `json:"width"`
		Height             int    `json:"height"`
		FieldOrder         string `json:"field_order"`
		RFrameRate         string `json:"r_frame_rate"`
		SampleRate         string `json:"sample_rate"`
		Channels           int    `json:"channels"`
		SampleAspectRatio  string `json:"sample_aspect_ratio"`
		DisplayAspectRatio string `json:"display_aspect_ratio"`
		BitRate            string `json:"bit_rate"`
		Disposition        struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

// Probe runs `ffprobe` against url and returns a ProbeResult. Callers that
// need request headers should use ProbeInput.
//
// policy gates how ffprobe dereferences the URL: an empty / zero-value
// policy preserves the historical argv shape exactly. A non-zero policy
// emits its flags before the URL so they apply to the probe input. See
// MediaInputPolicy.Apply for the flag mapping.
func Probe(ctx context.Context, ffprobePath, url string, policy MediaInputPolicy) (*ProbeResult, error) {
	return ProbeInput(ctx, ffprobePath, ProbeInputSpec{
		URL:    url,
		Policy: policy,
	})
}

func ProbeInput(ctx context.Context, ffprobePath string, input ProbeInputSpec) (*ProbeResult, error) {
	if timeout := probeTimeout(input); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := probeCommandContext(ctx, ffprobePath, input)
	cmd.WaitDelay = probeWaitDelay
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbeOutput(out)
}

func probeTimeout(input ProbeInputSpec) time.Duration {
	if input.Timeout > 0 {
		return input.Timeout
	}
	if input.Capture.Enabled {
		return defaultCaptureProbeTimeout
	}
	return 0
}

func probeCommand(ffprobePath string, input ProbeInputSpec) *exec.Cmd {
	return probeCommandContext(context.Background(), ffprobePath, input)
}

func probeCommandContext(ctx context.Context, ffprobePath string, input ProbeInputSpec) *exec.Cmd {
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
	}
	if input.Capture.Enabled {
		args = appendProbeCaptureInputArgs(args, input.Capture)
	} else {
		args = input.Policy.Apply(args)
		args = appendHeadersArg(args, input.Policy.FilterHeaders(input.Headers))
		args = append(args, input.URL)
	}
	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	return cmd
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
			if s.Disposition.AttachedPic == 1 {
				r.AttachedPicture = true
				continue
			}
			if r.Width == 0 {
				r.Width = s.Width
				r.Height = s.Height
				r.FrameRate = parseFrameRate(s.RFrameRate)
				r.Interlaced = s.FieldOrder == "tt" || s.FieldOrder == "bb" ||
					s.FieldOrder == "tb" || s.FieldOrder == "bt"
				r.VideoCodec = s.CodecName
				r.SampleAspectRatioNum, r.SampleAspectRatioDen = parseAspectRatio(s.SampleAspectRatio)
				r.DisplayAspectRatioNum, r.DisplayAspectRatioDen = parseAspectRatio(s.DisplayAspectRatio)
				r.VideoBitrateBPS = parseInt64(s.BitRate)
			}
		case "audio":
			if r.AudioRate == 0 {
				fmt.Sscan(s.SampleRate, &r.AudioRate)
				r.AudioCodec = s.CodecName
				r.AudioChannels = s.Channels
				r.AudioBitrateBPS = parseInt64(s.BitRate)
			}
		}
	}
	fmt.Sscan(p.Format.Duration, &r.Duration)
	r.FormatBitrateBPS = parseInt64(p.Format.BitRate)
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

func parseAspectRatio(s string) (int, int) {
	var n, d int
	if _, err := fmt.Sscanf(s, "%d:%d", &n, &d); err == nil && n > 0 && d > 0 {
		return n, d
	}
	return 0, 0
}

func parseInt64(s string) int64 {
	var v int64
	if _, err := fmt.Sscan(s, &v); err != nil || v < 0 {
		return 0
	}
	return v
}
