package ffmpeg

import (
	"reflect"
	"testing"
	"time"
)

func TestAppendCaptureInputArgsStructured(t *testing.T) {
	args := appendCaptureInputArgs(nil, CaptureInputSpec{
		Enabled:         true,
		Format:          "alsa",
		Device:          "hw:1,0",
		SampleRate:      48000,
		Channels:        2,
		ThreadQueueSize: 64,
		AnalyzeDuration: 100 * time.Millisecond,
		ProbeSize:       32768,
	})
	want := []string{
		"-thread_queue_size", "64",
		"-f", "alsa",
		"-sample_rate", "48000",
		"-channels", "2",
		"-analyzeduration", "100000",
		"-probesize", "32768",
		"-i", "hw:1,0",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("appendCaptureInputArgs() = %#v, want %#v", args, want)
	}
}

func assertArgsContainSubsequence(t *testing.T, args, want []string) {
	t.Helper()
	if argsContainSubsequence(args, want) {
		return
	}
	t.Fatalf("argv missing subsequence\nwant: %#v\n got: %#v", want, args)
}

func assertArgsDoNotContainSubsequence(t *testing.T, args, banned []string) {
	t.Helper()
	if !argsContainSubsequence(args, banned) {
		return
	}
	t.Fatalf("argv contains banned subsequence\nbanned: %#v\n   got: %#v", banned, args)
}

func argsContainSubsequence(args, want []string) bool {
	if len(want) == 0 {
		return true
	}
	matched := 0
	for _, arg := range args {
		if arg == want[matched] {
			matched++
			if matched == len(want) {
				return true
			}
		}
	}
	return false
}
