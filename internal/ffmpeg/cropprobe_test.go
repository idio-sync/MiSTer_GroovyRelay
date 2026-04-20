package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestParseCropLine is a pure-unit test for the cropdetect regex. No ffmpeg.
func TestParseCropLine(t *testing.T) {
	cases := map[string]*CropRect{
		"[Parsed_cropdetect_0 @ 0x55] x1:0 x2:1919 y1:140 y2:939 w:1920 h:800 x:0 y:140 pts:720 t:0.720000 limit:0.094118 crop=1920:800:0:140": {W: 1920, H: 800, X: 0, Y: 140},
		"crop=720:480:0:0":      {W: 720, H: 480, X: 0, Y: 0},
		"no match here":         nil,
		"crop=abc":              nil,
		"crop=1280:720:16:0":    {W: 1280, H: 720, X: 16, Y: 0},
	}
	for line, want := range cases {
		got := parseCropLine(line)
		if (got == nil) != (want == nil) {
			t.Errorf("parseCropLine(%q) nil-ness mismatch: got %v want %v", line, got, want)
			continue
		}
		if want == nil {
			continue
		}
		if *got != *want {
			t.Errorf("parseCropLine(%q) = %+v, want %+v", line, got, want)
		}
	}
}

// TestProbeCrop_FindsLetterbox generates a 2s letterboxed clip with ffmpeg
// (720x480 frame with a 720x360 active video region padded by 60 px top/bottom)
// then calls ProbeCrop and checks the rect is close to the true letterbox.
func TestProbeCrop_FindsLetterbox(t *testing.T) {
	ffmpegBin := findFFBinary("ffmpeg")
	if ffmpegBin == "" {
		t.Skip("ffmpeg not findable")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "letterbox.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Generate: testsrc 720x360 at 24 fps, padded to 720x480 with 60 px black
	// bars top and bottom. Use H.264 so cropdetect has full-bandwidth luma.
	gen := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=720x360:rate=24",
		"-vf", "pad=720:480:0:60:color=black",
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast",
		"-t", "2", "-y", clip,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("fixture generation failed (%v): %s", err, out)
	}

	rect, err := probeCropWithBinary(ctx, ffmpegBin, clip, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("ProbeCrop: %v", err)
	}
	if rect == nil {
		t.Fatal("expected non-nil crop rect")
	}
	// Y ≈ 60 (within ±4 for cropdetect round=2 + codec noise).
	if rect.Y < 56 || rect.Y > 64 {
		t.Errorf("Y out of range: got %d, want ~60 (±4)", rect.Y)
	}
	// H ≈ 360 (within ±8).
	if rect.H < 352 || rect.H > 368 {
		t.Errorf("H out of range: got %d, want ~360 (±8)", rect.H)
	}
	// W should still be full width.
	if rect.W != 720 {
		t.Errorf("W: got %d, want 720", rect.W)
	}
}

// TestProbeCrop_NoLetterboxReturnsFullFrame: a fully-filled testsrc produces
// a rect covering the full frame (i.e. non-nil with W=source, Y=0). This
// documents the behaviour: ProbeCrop returns the LAST detected rect.
func TestProbeCrop_NoLetterboxReturnsFullFrame(t *testing.T) {
	ffmpegBin := findFFBinary("ffmpeg")
	if ffmpegBin == "" {
		t.Skip("ffmpeg not findable")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "full.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	gen := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=720x480:rate=24",
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast",
		"-t", "2", "-y", clip,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("fixture generation failed (%v): %s", err, out)
	}

	rect, err := probeCropWithBinary(ctx, ffmpegBin, clip, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("ProbeCrop: %v", err)
	}
	// Either nil (no letterbox detected) or full-frame rect is acceptable.
	if rect != nil {
		if rect.Y != 0 || rect.H != 480 {
			t.Errorf("expected full-frame rect when no letterbox, got %+v", rect)
		}
	}
}
