package ffmpeg

import (
	"fmt"
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
	return appendCaptureInputArgsWithoutQueue(args, c)
}

func appendCaptureInputArgsWithoutQueue(args []string, c CaptureInputSpec) []string {
	if c.Format != "" {
		args = append(args, "-f", c.Format)
	}
	if c.SampleRate > 0 {
		args = append(args, "-sample_rate", fmt.Sprintf("%d", c.SampleRate))
	}
	if c.Channels > 0 {
		args = append(args, "-channels", fmt.Sprintf("%d", c.Channels))
	}
	if c.AnalyzeDuration > 0 {
		args = append(args, "-analyzeduration", fmt.Sprintf("%d", c.AnalyzeDuration.Microseconds()))
	}
	if c.ProbeSize > 0 {
		args = append(args, "-probesize", fmt.Sprintf("%d", c.ProbeSize))
	}
	return append(args, "-i", c.Device)
}
