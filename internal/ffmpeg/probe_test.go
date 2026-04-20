package ffmpeg

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseProbeOutput_ProgressiveVideoWithAudio(t *testing.T) {
	// Mimics ffprobe JSON for a 1920x1080 23.976p h264 clip with stereo AAC.
	raw := []byte(`{
		"streams": [
			{"codec_type":"video","width":1920,"height":1080,"field_order":"progressive","r_frame_rate":"24000/1001"},
			{"codec_type":"audio","sample_rate":"48000"}
		],
		"format":{"duration":"12.345"}
	}`)
	got, err := parseProbeOutput(raw)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if got.Width != 1920 || got.Height != 1080 {
		t.Errorf("dims: %dx%d", got.Width, got.Height)
	}
	if math.Abs(got.FrameRate-23.976) > 0.01 {
		t.Errorf("frame rate: %f", got.FrameRate)
	}
	if got.Interlaced {
		t.Error("expected progressive")
	}
	if got.AudioRate != 48000 {
		t.Errorf("audio rate: %d", got.AudioRate)
	}
	if math.Abs(got.Duration-12.345) > 0.001 {
		t.Errorf("duration: %f", got.Duration)
	}
}

func TestParseProbeOutput_Interlaced(t *testing.T) {
	for _, fo := range []string{"tt", "bb", "tb", "bt"} {
		raw := []byte(`{"streams":[{"codec_type":"video","width":720,"height":480,"field_order":"` + fo + `","r_frame_rate":"30000/1001"}]}`)
		got, err := parseProbeOutput(raw)
		if err != nil {
			t.Fatalf("%s: %v", fo, err)
		}
		if !got.Interlaced {
			t.Errorf("field_order %q should be flagged interlaced", fo)
		}
	}
}

func TestParseProbeOutput_MalformedJSON(t *testing.T) {
	if _, err := parseProbeOutput([]byte("not json")); err == nil {
		t.Error("expected error for malformed json")
	}
}

func TestParseFrameRate(t *testing.T) {
	cases := map[string]float64{
		"30000/1001": 30000.0 / 1001.0,
		"24/1":       24,
		"":           0,
		"bogus":      0,
	}
	for in, want := range cases {
		got := parseFrameRate(in)
		if math.Abs(got-want) > 0.001 {
			t.Errorf("parseFrameRate(%q) = %f, want %f", in, got, want)
		}
	}
}

// TestProbe_LiveFixture generates a 1-second synthetic test clip with ffmpeg
// then probes it. Skipped if ffmpeg / ffprobe are not findable.
func TestProbe_LiveFixture(t *testing.T) {
	ffmpegBin := findFFBinary("ffmpeg")
	ffprobeBin := findFFBinary("ffprobe")
	if ffmpegBin == "" || ffprobeBin == "" {
		t.Skip("ffmpeg/ffprobe not findable")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "fixture.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=24",
		"-pix_fmt", "yuv420p", "-y", clip,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg fixture generation failed (%v): %s", err, out)
	}
	if _, err := os.Stat(clip); err != nil {
		t.Skipf("fixture not written: %v", err)
	}

	// Use the full ffprobe path too (since `ffprobe` isn't in Windows PATH).
	probeCmd := exec.CommandContext(ctx, ffprobeBin,
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
		clip,
	)
	out, err := probeCmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	res, err := parseProbeOutput(out)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if res.Width != 320 || res.Height != 240 {
		t.Errorf("dims: %dx%d", res.Width, res.Height)
	}
	if math.Abs(res.FrameRate-24) > 0.01 {
		t.Errorf("frame rate: %f", res.FrameRate)
	}
	if res.Interlaced {
		t.Error("testsrc is progressive")
	}
	if res.Duration < 0.5 || res.Duration > 2.0 {
		t.Errorf("duration suspect: %f", res.Duration)
	}
}
