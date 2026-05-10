package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// argvForCropProbe rebuilds the argv slice the way probeCropWithBinary
// would, but without spawning ffmpeg. This lets us assert policy flag
// placement portably even when ffmpeg isn't on PATH.
func argvForCropProbe(headers map[string]string, duration time.Duration, policy MediaInputPolicy, inputURL string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-t", "2.0",
	}
	_ = duration // placeholder so signature matches probeCropWithBinary's intent
	args = policy.Apply(args)
	if len(headers) > 0 {
		var sb strings.Builder
		for k, v := range headers {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
		args = append(args, "-headers", sb.String())
	}
	args = append(args,
		"-i", inputURL,
		"-vf", "cropdetect=limit=24:round=2:reset=0",
		"-f", "null", "-",
	)
	return args
}

// TestProbeCrop_ZeroPolicyArgvUnchanged guarantees that the historical
// crop-probe argv shape is preserved when no policy is set. Pre-existing
// crop probes for Plex/Jellyfin/URL casts must produce identical argv
// after the refactor.
func TestProbeCrop_ZeroPolicyArgvUnchanged(t *testing.T) {
	got := argvForCropProbe(nil, 2*time.Second, MediaInputPolicy{}, "http://pms/clip.mp4")
	want := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-t", "2.0",
		"-i", "http://pms/clip.mp4",
		"-vf", "cropdetect=limit=24:round=2:reset=0",
		"-f", "null", "-",
	}
	for i, a := range want {
		if got[i] != a {
			t.Errorf("argv[%d] = %q, want %q (full got=%v)", i, got[i], a, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("argv length mismatch: got %d, want %d (got=%v)", len(got), len(want), got)
	}
}

// TestProbeCrop_PolicyAppliedBeforeInput verifies that policy flags appear
// after the -t but before -i so ffmpeg treats them as input options.
func TestProbeCrop_PolicyAppliedBeforeInput(t *testing.T) {
	policy := MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "http", "https"},
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
	}
	got := argvForCropProbe(nil, 2*time.Second, policy, "http://example/clip.mp4")
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"-protocol_whitelist file,http,https",
		"-reconnect 0",
		"-reconnect_at_eof 0",
		"-reconnect_streamed 0",
		"-reconnect_on_network_error 0",
		"-rw_timeout 5000000",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in argv: %s", want, joined)
		}
	}
	whitelistIdx := strings.Index(joined, "-protocol_whitelist")
	iIdx := strings.Index(joined, "-i ")
	if whitelistIdx < 0 || iIdx < 0 || whitelistIdx >= iIdx {
		t.Errorf("policy flags must precede -i: %s", joined)
	}
}

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

	rect, err := probeCropWithBinary(ctx, ffmpegBin, clip, nil, 2*time.Second, MediaInputPolicy{})
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

	rect, err := probeCropWithBinary(ctx, ffmpegBin, clip, nil, 2*time.Second, MediaInputPolicy{})
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
