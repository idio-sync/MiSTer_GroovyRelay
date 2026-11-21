package ffmpeg

import (
	"context"
	"errors"
	"testing"
)

// FFmpeg 9.x made -thread_queue_size output-only; this is its stderr when
// the option precedes an -i (captured from the 9.1 gyan.dev build).
const ffmpeg9ThreadQueueRejection = `Option thread_queue_size (set the maximum number of queued packets per stream on the muxer) cannot be applied to input url anullsrc=d=0.01 -- you are trying to apply an input option to an output file or vice versa. Move this option before the file it belongs to.
Error parsing options for input file anullsrc=d=0.01.
Error opening input files: Invalid argument
`

func TestClassifyInputThreadQueueProbe(t *testing.T) {
	exitErr := errors.New("exit status 0xfffffff2")
	cases := []struct {
		name       string
		runErr     error
		stderr     string
		wantUsable bool
		wantErr    bool
	}{
		{"accepted", nil, "", true, false},
		{"ffmpeg 9 rejects input option", exitErr, ffmpeg9ThreadQueueRejection, false, false},
		// Any other failure (no lavfi, binary missing) says nothing about the
		// option: keep passing it, as before, and report the error.
		{"unrelated failure", exitErr, "Unknown input format: 'lavfi'\n", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			usable, err := classifyInputThreadQueueProbe(c.runErr, c.stderr)
			if usable != c.wantUsable || (err != nil) != c.wantErr {
				t.Fatalf("classify = (%v, %v), want (%v, err=%v)", usable, err, c.wantUsable, c.wantErr)
			}
		})
	}
}

func stubInputThreadQueueProbe(t *testing.T, fn func(context.Context, string) (bool, error)) {
	t.Helper()
	orig := inputThreadQueueSizeUsableFn
	t.Cleanup(func() { inputThreadQueueSizeUsableFn = orig })
	inputThreadQueueSizeUsableFn = fn
}

func captureSpec(queue int) PipelineSpec {
	return PipelineSpec{
		FFmpegPath: "/opt/ffmpeg",
		CaptureInput: CaptureInputSpec{
			Enabled:         true,
			Format:          "s16le",
			Device:          "http://127.0.0.1:1/pcm",
			ThreadQueueSize: queue,
		},
	}
}

func TestWithCaptureInputCapabilitiesDropsUnsupportedThreadQueue(t *testing.T) {
	stubInputThreadQueueProbe(t, func(_ context.Context, path string) (bool, error) {
		if path != "/opt/ffmpeg" {
			t.Errorf("probe path = %q, want /opt/ffmpeg", path)
		}
		return false, nil
	})
	spec := withCaptureInputCapabilities(t.Context(), captureSpec(64))
	if spec.CaptureInput.ThreadQueueSize != 0 {
		t.Fatalf("ThreadQueueSize = %d, want 0 when ffmpeg rejects it on inputs", spec.CaptureInput.ThreadQueueSize)
	}
	args := BuildCommand(t.Context(), spec).Args
	assertArgsDoNotContainSubsequence(t, args, []string{"-thread_queue_size"})
}

func TestWithCaptureInputCapabilitiesKeepsSupportedThreadQueue(t *testing.T) {
	stubInputThreadQueueProbe(t, func(context.Context, string) (bool, error) { return true, nil })
	spec := withCaptureInputCapabilities(t.Context(), captureSpec(64))
	if spec.CaptureInput.ThreadQueueSize != 64 {
		t.Fatalf("ThreadQueueSize = %d, want 64", spec.CaptureInput.ThreadQueueSize)
	}
}

func TestWithCaptureInputCapabilitiesSkipsProbeWithoutQueue(t *testing.T) {
	stubInputThreadQueueProbe(t, func(context.Context, string) (bool, error) {
		t.Fatal("probe should not run when no thread queue is requested")
		return false, nil
	})
	withCaptureInputCapabilities(t.Context(), captureSpec(0))
	withCaptureInputCapabilities(t.Context(), PipelineSpec{InputURL: "http://x/video.mp4"})
}
