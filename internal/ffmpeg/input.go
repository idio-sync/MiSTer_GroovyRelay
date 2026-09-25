package ffmpeg

import (
	"fmt"
	"regexp"
	"time"
)

type CaptureInputSpec struct {
	Enabled         bool
	Format          string
	Device          string
	SampleRate      int
	Channels        int
	ThreadQueueSize int
	AnalyzeDuration time.Duration
	ProbeSize       int
}

type ProbeInputSpec struct {
	URL     string
	Headers map[string]string
	Policy  MediaInputPolicy
	Capture CaptureInputSpec
	Timeout time.Duration
}

func appendCaptureInputArgs(args []string, c CaptureInputSpec) []string {
	if c.ThreadQueueSize > 0 {
		args = append(args, "-thread_queue_size", fmt.Sprintf("%d", c.ThreadQueueSize))
	}
	return appendCaptureInputArgsWithoutQueue(args, c)
}

func appendProbeCaptureInputArgs(args []string, c CaptureInputSpec) []string {
	return appendCaptureInputArgs(args, c)
}

// rawPCMFormat matches FFmpeg's raw PCM demuxer names: s16le, f32be, u8, …
var rawPCMFormat = regexp.MustCompile(`^[suf](8|16|24|32|64)(le|be)?$`)

func isRawPCMFormat(format string) bool {
	return rawPCMFormat.MatchString(format)
}

func channelLayout(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	default:
		return fmt.Sprintf("%dc", channels)
	}
}

func appendCaptureInputArgsWithoutQueue(args []string, c CaptureInputSpec) []string {
	if c.Format != "" {
		args = append(args, "-f", c.Format)
	}
	if c.SampleRate > 0 {
		args = append(args, "-sample_rate", fmt.Sprintf("%d", c.SampleRate))
	}
	if c.Channels > 0 {
		if isRawPCMFormat(c.Format) {
			// Raw PCM demuxers dropped -channels (FFmpeg 7+ rejects it; ffprobe
			// silently ignores it and reports mono). -ch_layout exists since 5.1.
			args = append(args, "-ch_layout", channelLayout(c.Channels))
		} else {
			args = append(args, "-channels", fmt.Sprintf("%d", c.Channels))
		}
	}
	if c.AnalyzeDuration > 0 {
		args = append(args, "-analyzeduration", fmt.Sprintf("%d", c.AnalyzeDuration.Microseconds()))
	}
	if c.ProbeSize > 0 {
		args = append(args, "-probesize", fmt.Sprintf("%d", c.ProbeSize))
	}
	return append(args, "-i", c.Device)
}
