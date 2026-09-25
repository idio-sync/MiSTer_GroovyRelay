package ffmpeg

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEscapeFilterPathFor(t *testing.T) {
	cases := []struct {
		name string
		goos string
		in   string
		want string
	}{
		{"linux simple", "linux", "/tmp/viz/title.txt", "/tmp/viz/title.txt"},
		{"windows drive colon", "windows", `C:\Temp\viz\title.txt`, `C\:/Temp/viz/title.txt`},
		{"apostrophe", "linux", "/tmp/Bob's/title.txt", `/tmp/Bob'\''s/title.txt`},
		{"unix backslash kept literal", "linux", `/tmp/a\b.txt`, `/tmp/a\\b.txt`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeFilterPathFor(tc.goos, tc.in); got != tc.want {
				t.Fatalf("escapeFilterPathFor(%q, %q) = %q, want %q", tc.goos, tc.in, got, tc.want)
			}
		})
	}
}

func liveTextSpec(dir string) PipelineSpec {
	return PipelineSpec{
		OutputWidth:   720,
		OutputHeight:  480,
		OutputFpsExpr: "60000/1001",
		Visualizer: VisualizerSpec{
			Enabled:                  true,
			Mode:                     VisualizerModeRetroAnalyzer,
			DrawTextAvailable:        true,
			RequiredFiltersAvailable: true,
			LiveTextDir:              dir,
			// Static metadata must be ignored once LiveTextDir is set.
			Metadata: VisualizerMetadata{Title: "Static Title", Artist: "Static Artist"},
		},
	}
}

func TestVisualizerTextLines_LiveTextReservesThreeFileLines(t *testing.T) {
	dir := filepath.Join("viz", "session")
	lines := visualizerTextLines(liveTextSpec(dir))
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3: %#v", len(lines), lines)
	}
	want := []struct{ role, file, y string }{
		{visualizerTextRoleTitle, VisualizerTitleFile, "24"},
		{visualizerTextRoleArtist, VisualizerArtistFile, "48"},
		{visualizerTextRoleAlbum, VisualizerAlbumFile, "72"},
	}
	for i, w := range want {
		got := lines[i]
		if got.Role != w.role || got.File != filepath.Join(dir, w.file) || got.Y != w.y || got.Text != "" || !got.Marquee {
			t.Fatalf("line %d = %#v, want role %s file %s y %s", i, got, w.role, w.file, w.y)
		}
	}
}

func TestBuildVisualizerFilterChain_LiveTextUsesReloadedTextfiles(t *testing.T) {
	graph, err := buildVisualizerFilterChain(liveTextSpec("/run/viz"))
	if err != nil {
		t.Fatalf("buildVisualizerFilterChain: %v", err)
	}
	for _, name := range []string{VisualizerTitleFile, VisualizerArtistFile, VisualizerAlbumFile} {
		want := "drawtext=textfile='/run/viz/" + name + "':reload=1:expansion=none:"
		if !strings.Contains(graph, want) {
			t.Fatalf("graph missing %q:\n%s", want, graph)
		}
	}
	if strings.Contains(graph, "STATIC") {
		t.Fatalf("live graph still renders static metadata:\n%s", graph)
	}
}

// readFile returns the text drawtext would render from path: the file
// contents up to the first NUL (Windows files are NUL-padded).
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func TestFixedSizeText(t *testing.T) {
	if got := fixedSizeText("ABC"); len(got) != liveTextFileSize || string(got[:4]) != "ABC\x00" {
		t.Fatalf("fixedSizeText(ABC) = %q..., len %d", got[:4], len(got))
	}
	// 511 ASCII bytes then a 3-byte rune straddling the limit: the rune
	// must be dropped whole, not split.
	long := strings.Repeat("A", liveTextFileSize-1) + "€"
	got := fixedSizeText(long)
	if len(got) != liveTextFileSize {
		t.Fatalf("len = %d, want %d", len(got), liveTextFileSize)
	}
	text := string(got[:bytes.IndexByte(got, 0)])
	if text != strings.Repeat("A", liveTextFileSize-1) {
		t.Fatalf("truncated text keeps a partial rune: %q", text[len(text)-4:])
	}
}

func TestWriteVisualizerText_NormalizesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	if err := WriteVisualizerText(dir, VisualizerMetadata{Title: "  Blue Monday\n(12\")  ", Artist: "New Order", Album: "Power: 100% \\o/"}); err != nil {
		t.Fatalf("WriteVisualizerText: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, VisualizerTitleFile)); got != `BLUE MONDAY (12")` {
		t.Fatalf("title = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, VisualizerAlbumFile)); got != `POWER: 100% \O/` {
		t.Fatalf("album = %q (expansion=none renders it literally, so no escaping)", got)
	}

	if err := WriteVisualizerText(dir, VisualizerMetadata{Artist: "Joy Division"}); err != nil {
		t.Fatalf("second WriteVisualizerText: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, VisualizerTitleFile)); got != "NOW PLAYING" {
		t.Fatalf("blank title = %q, want NOW PLAYING", got)
	}
	if got := readFile(t, filepath.Join(dir, VisualizerArtistFile)); got != "JOY DIVISION" {
		t.Fatalf("artist = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, VisualizerAlbumFile)); got != "" {
		t.Fatalf("cleared album = %q, want empty", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir holds %v, want only the three text files (no temp leftovers)", names)
	}
}

// TestLiveTextSurvivesRewritesWithFFmpeg rewrites the text files while a
// real ffmpeg reloads them every frame. drawtext aborts the whole graph on
// a failed reload, so this pins that replacement never exposes a missing
// file, including under Windows sharing rules.
func TestLiveTextSurvivesRewritesWithFFmpeg(t *testing.T) {
	ffmpegPath := findFFBinary("ffmpeg")
	if ffmpegPath == "" {
		t.Skip("ffmpeg not found; skipping live text reload test")
	}
	probeCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	usable, err := DrawTextUsable(probeCtx, ffmpegPath)
	cancel()
	if err != nil || !usable {
		t.Skipf("ffmpeg %q drawtext unusable: %v", ffmpegPath, err)
	}

	dir := t.TempDir()
	if err := WriteVisualizerText(dir, VisualizerMetadata{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	graph, err := buildVisualizerFilterChain(liveTextSpec(dir))
	if err != nil {
		t.Fatalf("buildVisualizerFilterChain: %v", err)
	}

	runCtx, cancelRun := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancelRun()
	cmd := exec.CommandContext(runCtx, ffmpegPath,
		"-hide_banner", "-v", "error",
		"-re", "-f", "lavfi", "-i", "sine=f=440:r=48000",
		"-filter_complex", graph,
		"-map", "[visualizer_video]",
		// The overlay line layers are infinite color sources, so bound
		// the run by frames: ~2 s of per-frame reloads at real time.
		"-frames:v", "120",
		"-f", "null", "-",
	)
	done := make(chan struct{})
	writerErr := make(chan error, 1)
	go func() {
		defer close(writerErr)
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			md := VisualizerMetadata{Title: "track " + strings.Repeat("x", i%7), Artist: "artist", Album: ""}
			if err := WriteVisualizerText(dir, md); err != nil {
				writerErr <- err
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	out, runErr := cmd.CombinedOutput()
	close(done)
	if err := <-writerErr; err != nil {
		t.Fatalf("WriteVisualizerText during playback: %v", err)
	}
	if runErr != nil {
		t.Fatalf("ffmpeg failed while text files were rewritten: %v\n%s\ngraph:\n%s", runErr, out, graph)
	}
}
