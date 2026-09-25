package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
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

func TestAppendCaptureInputArgsRawPCMUsesChannelLayout(t *testing.T) {
	cases := []struct {
		format   string
		channels int
		want     []string
	}{
		{"s16le", 2, []string{"-f", "s16le", "-sample_rate", "44100", "-ch_layout", "stereo", "-i", "dev"}},
		{"f32be", 1, []string{"-f", "f32be", "-sample_rate", "44100", "-ch_layout", "mono", "-i", "dev"}},
		{"u8", 6, []string{"-f", "u8", "-sample_rate", "44100", "-ch_layout", "6c", "-i", "dev"}},
		{"dshow", 2, []string{"-f", "dshow", "-sample_rate", "44100", "-channels", "2", "-i", "dev"}},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			got := appendCaptureInputArgs(nil, CaptureInputSpec{Enabled: true, Format: tc.format, Device: "dev", SampleRate: 44100, Channels: tc.channels})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("args = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestProbeRawPCMCaptureReportsStereoWithFFprobe pins the argv against a real
// ffprobe: with -channels, FFmpeg 7+ probes raw PCM as mono (or refuses it).
func TestProbeRawPCMCaptureReportsStereoWithFFprobe(t *testing.T) {
	ffprobePath := findFFBinary("ffprobe")
	if ffprobePath == "" {
		t.Skip("ffprobe not found")
	}
	path := filepath.Join(t.TempDir(), "pcm.raw")
	if err := os.WriteFile(path, make([]byte, 44100*4), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	res, err := ProbeInput(ctx, ffprobePath, ProbeInputSpec{Capture: CaptureInputSpec{
		Enabled: true, Format: "s16le", Device: path, SampleRate: 44100, Channels: 2,
	}})
	if err != nil {
		t.Fatalf("ProbeInput: %v", err)
	}
	if res.AudioChannels != 2 || res.AudioRate != 44100 {
		t.Fatalf("probe = %d ch @ %d Hz, want 2 ch @ 44100 Hz", res.AudioChannels, res.AudioRate)
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
