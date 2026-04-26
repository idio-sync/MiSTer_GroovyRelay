package ffmpeg

import (
	"context"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Filter-chain string-assembly tests. These are pure unit tests and run on any
// platform — they never invoke ffmpeg.
// -----------------------------------------------------------------------------

func TestBuildFilterChain_Progressive24p(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976, Interlaced: false},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
	}
	chain := buildFilterChain(spec)
	for _, need := range []string{"fps=60000/1001", "pad="} {
		if !strings.Contains(chain, need) {
			t.Errorf("chain missing %q: %s", need, chain)
		}
	}
	if strings.Contains(chain, "yadif") {
		t.Errorf("progressive source should not yadif: %s", chain)
	}
	for _, unwanted := range []string{"telecine", "interlace=scan=", "separatefields"} {
		if strings.Contains(chain, unwanted) {
			t.Errorf("progressive frame pipeline must not include %q: %s", unwanted, chain)
		}
	}
}

func TestBuildFilterChain_Interlaced30i(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 720, Height: 480, FrameRate: 29.97, Interlaced: true},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
	}
	chain := buildFilterChain(spec)
	if !strings.Contains(chain, "yadif") {
		t.Errorf("expected yadif for interlaced source: %s", chain)
	}
	if !strings.Contains(chain, "fps=60000/1001") {
		t.Errorf("expected fps normalizer for interlaced source: %s", chain)
	}
	// yadif must be the very first filter (before any rate conversion).
	if !strings.HasPrefix(chain, "yadif") {
		t.Errorf("yadif must come first: %s", chain)
	}
}

func TestBuildFilterChain_FieldOrderHandledOutsideFFmpeg(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 720, Height: 480, FrameRate: 29.97},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "bff", AspectMode: "letterbox",
	}
	chain := buildFilterChain(spec)
	if strings.Contains(chain, "interlace=scan=") {
		t.Errorf("field order should no longer be encoded in the ffmpeg chain: %s", chain)
	}
}

func TestBuildFilterChain_SubtitleAfterRateNormalize(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 24, Interlaced: false},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
		SubtitlePath: "/tmp/subtitle.srt", SubtitleIndex: 0,
	}
	chain := buildFilterChain(spec)
	subIdx := strings.Index(chain, "subtitles=")
	fpsIdx := strings.Index(chain, "fps=60000/1001")
	if subIdx < 0 || fpsIdx < 0 || subIdx <= fpsIdx {
		t.Errorf("subtitles must follow the fps normalizer: %s", chain)
	}
}

func TestBuildFilterChain_AutoCropUsesLockedRect(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "auto",
		CropRect: &CropRect{W: 1920, H: 800, X: 0, Y: 140},
	}
	chain := buildFilterChain(spec)
	if !strings.Contains(chain, "crop=1920:800:0:140") {
		t.Errorf("expected locked crop, got %s", chain)
	}
	if strings.Contains(chain, "cropdetect") {
		t.Errorf("main chain must NOT include cropdetect (probe pass only): %s", chain)
	}
}

// -----------------------------------------------------------------------------
// BuildCommand argv shape tests. Still pure string assembly; we never Start().
// -----------------------------------------------------------------------------

func TestBuildCommand_TranscodeSkipsSSSeek(t *testing.T) {
	spec := PipelineSpec{
		InputURL:    "http://pms/video.m3u8",
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
		SeekSeconds: 90, UseSSSeek: false,
		AudioSampleRate: 48000, AudioChannels: 2,
		VideoPipePath: "pipe:3", AudioPipePath: "pipe:4",
	}
	cmd := BuildCommand(context.Background(), spec)
	joined := strings.Join(cmd.Args, " ")
	if strings.Contains(joined, "-ss ") {
		t.Errorf("transcode path must not pass -ss: %s", joined)
	}
	if !strings.Contains(joined, "-i http://pms/video.m3u8") {
		t.Errorf("expected input URL: %s", joined)
	}
}

func TestBuildCommand_DirectPlayPassesSSSeek(t *testing.T) {
	spec := PipelineSpec{
		InputURL:    "file:///media/sample.mkv",
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
		SeekSeconds: 123.456, UseSSSeek: true,
		AudioSampleRate: 48000, AudioChannels: 2,
		VideoPipePath: "pipe:3", AudioPipePath: "pipe:4",
	}
	cmd := BuildCommand(context.Background(), spec)
	joined := strings.Join(cmd.Args, " ")
	// Find indices: -ss must appear BEFORE -i (ffmpeg fast-seeks the input).
	ssIdx := strings.Index(joined, "-ss ")
	iIdx := strings.Index(joined, "-i ")
	if ssIdx < 0 {
		t.Fatalf("expected -ss in argv: %s", joined)
	}
	if iIdx < 0 || ssIdx >= iIdx {
		t.Errorf("-ss must precede -i: %s", joined)
	}
	if !strings.Contains(joined, "123.456") {
		t.Errorf("seek seconds missing: %s", joined)
	}
}

func TestBuildCommand_HeadersCombinedIntoOneArg(t *testing.T) {
	spec := PipelineSpec{
		InputURL: "http://pms/video.m3u8",
		InputHeaders: map[string]string{
			"X-Plex-Token":   "abc123",
			"X-Plex-Product": "groovyrelay",
		},
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
		AudioSampleRate: 48000, AudioChannels: 2,
		VideoPipePath: "pipe:3", AudioPipePath: "pipe:4",
	}
	cmd := BuildCommand(context.Background(), spec)
	// Count occurrences of "-headers": ffmpeg takes a single concatenated arg.
	count := 0
	var headersVal string
	for i, a := range cmd.Args {
		if a == "-headers" {
			count++
			if i+1 < len(cmd.Args) {
				headersVal = cmd.Args[i+1]
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one -headers arg, got %d", count)
	}
	for _, want := range []string{"X-Plex-Token: abc123", "X-Plex-Product: groovyrelay"} {
		if !strings.Contains(headersVal, want) {
			t.Errorf("headers arg missing %q: %q", want, headersVal)
		}
	}
	// CRLF separator.
	if !strings.Contains(headersVal, "\r\n") {
		t.Errorf("expected CRLF-delimited headers: %q", headersVal)
	}
}

func TestBuildCommand_OutputsBothPipes(t *testing.T) {
	spec := PipelineSpec{
		InputURL:    "http://pms/video.m3u8",
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976, AudioRate: 48000},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
		AudioSampleRate: 48000, AudioChannels: 2,
		VideoPipePath: "pipe:3", AudioPipePath: "pipe:4",
	}
	cmd := BuildCommand(context.Background(), spec)
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"-map 0:v:0",
		"-pix_fmt bgr24",
		"-f rawvideo",
		"pipe:3",
		"-map 0:a:0",
		"-ar 48000",
		"-ac 2",
		"-f s16le",
		"pipe:4",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in argv: %s", want, joined)
		}
	}
}

func TestBuildCommand_OmitsAudioOutputWhenSourceHasNoAudio(t *testing.T) {
	spec := PipelineSpec{
		InputURL:    "http://pms/video.m3u8",
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976, AudioRate: 0},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
		AudioSampleRate: 48000, AudioChannels: 2,
		VideoPipePath: "pipe:3", AudioPipePath: "pipe:4",
	}
	cmd := BuildCommand(context.Background(), spec)
	joined := strings.Join(cmd.Args, " ")
	for _, unwanted := range []string{"-map 0:a:0", "-ar 48000", "-ac 2", "pipe:4"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("unexpected %q in argv for video-only source: %s", unwanted, joined)
		}
	}
	if !strings.Contains(joined, "-map 0:v:0") || !strings.Contains(joined, "pipe:3") {
		t.Errorf("video output missing from argv: %s", joined)
	}
}

func TestBuildFilterChain_FpsExpr(t *testing.T) {
	cases := []struct {
		name      string
		fpsExpr   string
		wantChain string
	}{
		{name: "NTSC default 60000/1001", fpsExpr: "60000/1001", wantChain: "fps=60000/1001"},
		{name: "PAL 50/1", fpsExpr: "50/1", wantChain: "fps=50/1"},
		{name: "empty defaults to NTSC", fpsExpr: "", wantChain: "fps=60000/1001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := PipelineSpec{
				OutputWidth:   720,
				OutputHeight:  480,
				AspectMode:    "letterbox",
				OutputFpsExpr: c.fpsExpr,
			}
			chain := buildFilterChain(s)
			if !strings.Contains(chain, c.wantChain) {
				t.Errorf("buildFilterChain() = %q, want substring %q", chain, c.wantChain)
			}
		})
	}
}

// TestBuildFilterChain_SourceRateProducesCorrectNormalizer validates that
// every source rate is normalized to 59.94 progressive output for the
// row-stripe data plane.
func TestBuildFilterChain_SourceRateProducesCorrectNormalizer(t *testing.T) {
	cases := []struct {
		name           string
		frameRate      float64
		wantNormalizer string // filter that MUST appear
	}{
		{"film 23.976p", 23.976, "fps=60000/1001"},
		{"film 24p", 24.0, "fps=60000/1001"},
		{"tv 29.97p", 29.97, "fps=60000/1001"},
		{"tv 30p", 30.0, "fps=60000/1001"},
		{"sports 59.94p", 59.94, "fps=60000/1001"},
		{"sports 60p", 60.0, "fps=60000/1001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := PipelineSpec{
				SourceProbe:  &ProbeResult{FrameRate: tc.frameRate, Interlaced: false},
				OutputWidth:  720,
				OutputHeight: 480,
				FieldOrder:   "tff",
				AspectMode:   "letterbox",
			}
			chain := buildFilterChain(spec)
			if !strings.Contains(chain, tc.wantNormalizer) {
				t.Errorf("rate %.3f: chain missing %q\nchain=%s", tc.frameRate, tc.wantNormalizer, chain)
			}
			for _, unwanted := range []string{"telecine", "interlace=scan=", "separatefields"} {
				if strings.Contains(chain, unwanted) {
					t.Errorf("rate %.3f: chain must not contain %q, got %s", tc.frameRate, unwanted, chain)
				}
			}
		})
	}
}
