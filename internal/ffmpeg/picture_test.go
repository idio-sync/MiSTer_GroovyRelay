package ffmpeg

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func pictureTestSpec() PipelineSpec {
	return PipelineSpec{
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "bff", AspectMode: "letterbox",
	}
}

// assertInOrder fails unless every fragment appears in s, each after the last.
func assertInOrder(t *testing.T, s string, fragments ...string) {
	t.Helper()
	at := 0
	for _, f := range fragments {
		i := strings.Index(s[at:], f)
		if i < 0 {
			t.Fatalf("missing %q (in order) in:\n%s", f, s)
		}
		at += i + len(f)
	}
}

func TestBuildFilterChain_FullPictureAreaLeavesChainUnchanged(t *testing.T) {
	base := buildFilterChain(pictureTestSpec())
	full := pictureTestSpec()
	full.Picture = PictureArea{W: 720, H: 480}
	if got := buildFilterChain(full); got != base {
		t.Fatalf("full-raster picture area changed the chain:\n got %s\nwant %s", got, base)
	}
	if strings.Contains(base, "pad=w=720") {
		t.Fatalf("default chain must not pad the output raster: %s", base)
	}
}

func TestBuildFilterChain_ShrunkPictureScalesOnceThenPads(t *testing.T) {
	spec := pictureTestSpec()
	spec.Picture = PictureArea{X: 40, Y: 24, W: 648, H: 432}
	spec.SubtitlePath = "/tmp/subs.srt"
	spec.InterlaceFilter = "light"
	chain := buildFilterChain(spec)
	assertInOrder(t, chain,
		"pad=w=640:h=480", // letterbox in the logical canvas
		"scale=w=648:h=432",
		"subtitles=",
		"pad=w=720:h=480:x=40:y=24:color=black",
		"convolution=",
		"format=bgr24",
		"fps=",
	)
	if strings.Contains(chain, "scale=w=720:h=480") {
		t.Fatalf("shrunk picture must not also stretch to the full raster: %s", chain)
	}
	if strings.Contains(chain, "crop=") {
		t.Fatalf("an on-raster picture needs no crop: %s", chain)
	}
}

func TestBuildFilterChain_OffRasterPictureCropsBeforePad(t *testing.T) {
	spec := pictureTestSpec()
	spec.Picture = PictureArea{X: -10, Y: 6, W: 720, H: 480}
	chain := buildFilterChain(spec)
	assertInOrder(t, chain,
		"crop=710:474:10:0",
		"pad=w=720:h=480:x=0:y=6:color=black",
	)
}

func TestBuildVisualizerFilterChain_PicturePlacement(t *testing.T) {
	spec := PipelineSpec{
		OutputWidth: 720, OutputHeight: 480,
		OutputFpsExpr:   "60000/1001",
		InterlaceFilter: "light",
		Picture:         PictureArea{X: 36, Y: 24, W: 648, H: 432},
		Visualizer: VisualizerSpec{
			Enabled:                  true,
			Mode:                     VisualizerModeRetroAnalyzer,
			RequiredFiltersAvailable: true,
		},
	}
	graph, err := buildVisualizerFilterChain(spec)
	if err != nil {
		t.Fatal(err)
	}
	assertInOrder(t, graph,
		"fps=60000/1001,scale=w=648:h=432,pad=w=720:h=480:x=36:y=24:color=black,convolution=",
		"format=bgr24[visualizer_video]",
	)
}

// TestBuildFilterChain_PictureAreaWithFFmpeg runs a shrunk, shifted chain
// through real ffmpeg: frames stay raster-sized, the border is black, and
// the picture lands where the area says.
func TestBuildFilterChain_PictureAreaWithFFmpeg(t *testing.T) {
	ffmpegPath := findFFBinary("ffmpeg")
	if ffmpegPath == "" {
		t.Skip("ffmpeg not found")
	}
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 640, Height: 480, FrameRate: 30},
		OutputWidth: 720, OutputHeight: 480,
		AspectMode: "letterbox",
		Picture:    PictureArea{X: 60, Y: 20, W: 600, H: 420},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-v", "error",
		"-f", "lavfi", "-i", "color=c=white:s=640x480:r=30:d=0.1",
		"-vf", buildFilterChain(spec),
		"-frames:v", "1", "-pix_fmt", "bgr24", "-f", "rawvideo", "-",
	).Output()
	if err != nil {
		t.Fatalf("ffmpeg failed: %v\nchain=%s", err, buildFilterChain(spec))
	}
	if len(out) != 720*480*3 {
		t.Fatalf("frame is %d bytes, want %d", len(out), 720*480*3)
	}
	px := func(x, y int) byte { return out[(y*720+x)*3] }
	for _, p := range [][2]int{{0, 0}, {59, 240}, {661, 240}, {360, 19}, {360, 441}} {
		if v := px(p[0], p[1]); v > 16 {
			t.Errorf("border pixel %v = %d, want black", p, v)
		}
	}
	for _, p := range [][2]int{{62, 22}, {360, 240}, {657, 437}} {
		if v := px(p[0], p[1]); v < 200 {
			t.Errorf("picture pixel %v = %d, want white", p, v)
		}
	}
}
