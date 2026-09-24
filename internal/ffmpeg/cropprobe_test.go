package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProbeCrop_ZeroPolicyArgvUnchanged guarantees that the historical
// crop-probe argv shape is preserved when no policy is set and the probe
// samples from the start of the stream.
func TestProbeCrop_ZeroPolicyArgvUnchanged(t *testing.T) {
	got := cropProbeArgs(CropProbeSpec{URL: "http://pms/clip.mp4", SampleDuration: 2 * time.Second}, noCropSeek)
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
	got := cropProbeArgs(CropProbeSpec{URL: "http://example/clip.mp4", Policy: policy, SampleDuration: 2 * time.Second}, noCropSeek)
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
		"crop=720:480:0:0":   {W: 720, H: 480, X: 0, Y: 0},
		"no match here":      nil,
		"crop=abc":           nil,
		"crop=1280:720:16:0": {W: 1280, H: 720, X: 16, Y: 0},
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

	rect, err := probeCropWithBinary(ctx, ffmpegBin, CropProbeSpec{URL: clip, SampleDuration: 2 * time.Second})
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

	rect, err := probeCropWithBinary(ctx, ffmpegBin, CropProbeSpec{URL: clip, SampleDuration: 2 * time.Second})
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

// TestPlausibleCropRect: auto-crop may only remove black bars. A rect that
// is small or off-center is something else — typically a studio logo on
// black during the probe window — and cropping to it would zoom the whole
// session into that box.
func TestPlausibleCropRect(t *testing.T) {
	cases := []struct {
		name string
		rect CropRect
		w, h int
		want bool
	}{
		{"full frame", CropRect{W: 1920, H: 1080, X: 0, Y: 0}, 1920, 1080, true},
		{"2.39 letterbox bars", CropRect{W: 1920, H: 800, X: 0, Y: 140}, 1920, 1080, true},
		{"4:3 pillarbox bars", CropRect{W: 1440, H: 1080, X: 240, Y: 0}, 1920, 1080, true},
		{"windowbox", CropRect{W: 1440, H: 800, X: 240, Y: 140}, 1920, 1080, true},
		{"cropdetect rounding asymmetry", CropRect{W: 1920, H: 804, X: 0, Y: 136}, 1920, 1080, true},
		{"centered studio logo", CropRect{W: 400, H: 200, X: 760, Y: 440}, 1920, 1080, false},
		{"less than half the height", CropRect{W: 1920, H: 520, X: 0, Y: 280}, 1920, 1080, false},
		{"off-center content", CropRect{W: 1440, H: 1080, X: 0, Y: 0}, 1920, 1080, false},
		{"subtitle in bottom bar", CropRect{W: 1920, H: 900, X: 0, Y: 140}, 1920, 1080, false},
		{"exceeds source", CropRect{W: 1920, H: 1080, X: 8, Y: 0}, 1920, 1080, false},
		{"empty rect", CropRect{}, 1920, 1080, false},
		{"unknown source size", CropRect{W: 1920, H: 800, X: 0, Y: 140}, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PlausibleCropRect(tc.rect, tc.w, tc.h); got != tc.want {
				t.Fatalf("PlausibleCropRect(%+v, %d, %d) = %v, want %v", tc.rect, tc.w, tc.h, got, tc.want)
			}
		})
	}
}

// TestProbeCrop_SeekPrecedesInput: each spread sample seeks with an input
// -ss so ffmpeg jumps straight to the sample instead of decoding up to it.
func TestProbeCrop_SeekPrecedesInput(t *testing.T) {
	got := strings.Join(cropProbeArgs(CropProbeSpec{URL: "http://pms/film.mkv", SampleDuration: time.Second}, 1234.5), " ")
	ssIdx := strings.Index(got, "-ss 1234.500")
	iIdx := strings.Index(got, "-i http://pms/film.mkv")
	if ssIdx < 0 || iIdx < 0 || ssIdx >= iIdx {
		t.Fatalf("want -ss 1234.500 before -i: %s", got)
	}
	if !strings.Contains(got, "-t 1.0") {
		t.Fatalf("want per-sample -t 1.0: %s", got)
	}
}

func TestUnionCropRects(t *testing.T) {
	got := unionCropRects([]*CropRect{
		nil,                               // a sample that saw only black
		{W: 1440, H: 600, X: 240, Y: 240}, // dark scene: content box too small
		{W: 1440, H: 1080, X: 240, Y: 0},  // bright scene: full pillarbox
		{W: 1400, H: 1000, X: 260, Y: 40},
	})
	want := &CropRect{W: 1440, H: 1080, X: 240, Y: 0}
	if got == nil || *got != *want {
		t.Fatalf("union = %+v, want %+v", got, want)
	}
	if unionCropRects([]*CropRect{nil, nil}) != nil {
		t.Fatal("union of no rects must be nil")
	}
}

// TestProbeCrop_SpreadSamplesSkipLogoIntro: a pillarboxed programme that
// opens on a small logo. Sampling only the stream start finds the logo box;
// spreading samples across the runtime finds the real content bars.
func TestProbeCrop_SpreadSamplesSkipLogoIntro(t *testing.T) {
	ffmpegBin := findFFBinary("ffmpeg")
	if ffmpegBin == "" {
		t.Skip("ffmpeg not findable")
	}
	clip := filepath.Join(t.TempDir(), "logo-then-pillarbox.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// 320x180 frame. 0-4 s: 40x20 logo centered on black. 4-30 s: 240x180
	// 4:3 programme pillarboxed at x=40 (moving testsrc so cropdetect sees
	// full-bandwidth content).
	gen := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=240x180:rate=10:duration=30",
		"-vf", "pad=320:180:40:0:color=black,"+
			"drawbox=x=0:y=0:w=320:h=180:color=black:t=fill:enable='lt(t,4)',"+
			"drawbox=x=140:y=80:w=40:h=20:color=white:t=fill:enable='lt(t,4)'",
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast",
		"-y", clip,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("fixture generation failed (%v): %s", err, out)
	}

	fromStart, err := probeCropWithBinary(ctx, ffmpegBin, CropProbeSpec{URL: clip, SampleDuration: 2 * time.Second})
	if err != nil {
		t.Fatalf("ProbeCrop from start: %v", err)
	}
	if fromStart == nil || fromStart.W > 60 {
		t.Fatalf("fixture sanity: start-only probe should see the logo box, got %+v", fromStart)
	}

	spread, err := probeCropWithBinary(ctx, ffmpegBin, CropProbeSpec{
		URL: clip, SampleDuration: time.Second, Starts: []float64{6, 15, 24},
	})
	if err != nil {
		t.Fatalf("ProbeCrop spread: %v", err)
	}
	if spread == nil || spread.W < 236 || spread.W > 244 || spread.H != 180 || spread.X < 36 || spread.X > 44 {
		t.Fatalf("spread probe = %+v, want ~240x180+40+0", spread)
	}
}
