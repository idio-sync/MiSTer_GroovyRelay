# Visualizer Metadata Overlay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refine the FFmpeg music visualizer overlay so metadata renders as a compact all-caps receiver display with Artist, Track Title, Album, separate right-side progress, and contained marquee behavior for long metadata lines.

**Architecture:** Keep the change inside `internal/ffmpeg`: `pipeline.go` owns overlay descriptors, layout math, graph generation, and metadata escaping; `capabilities.go` owns pre-start overlay capability fallback. Adapters and core continue passing the existing metadata fields unchanged.

**Tech Stack:** Go unit tests, FFmpeg `filter_complex`, `drawtext`, transparent `color` line layers, `overlay`, existing visualizer capability probes.

**Spec:** [docs/superpowers/specs/2026-05-25-visualizer-metadata-overlay-design.md](../specs/2026-05-25-visualizer-metadata-overlay-design.md)

---

## File Map

- Modify `internal/ffmpeg/pipeline.go`: replace the current two/three-line text assembly with private overlay descriptors, multi-resolution layout helpers, per-line transparent layers, right-side progress, and text-width-based marquee expressions.
- Modify `internal/ffmpeg/capabilities.go`: extend `withVisualizerCapabilities` so missing overlay-only filters disable overlay text without disabling the audio-reactive visualizer core.
- Modify `internal/ffmpeg/pipeline_test.go`: update existing visualizer graph tests and add focused tests for metadata order, casing, font sizes/colors, layout sizing, marquee expression, fallback, capability behavior, and an optional FFmpeg smoke test.

Before editing, run:

```bash
git status --short
```

Preserve unrelated local edits. At plan-writing time the worktree had unrelated modifications in `.gitignore`, `README.md`, `docs/superpowers/plans/2026-05-21-music-visualizer-expansion.md`, and `docs/superpowers/specs/2026-05-24-receiver-chassis-meter-telemetry-design.md`; do not stage or revert them.

---

### Task 1: Overlay Descriptor And Layout Helpers

**Files:**
- Modify: `internal/ffmpeg/pipeline.go`
- Modify: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Write failing tests for metadata descriptors and logical-canvas layout**

In `internal/ffmpeg/pipeline_test.go`, add these tests after `TestBuildVisualizerFilterChain_ApostropheInTitle`:

```go
func TestVisualizerTextLines_MetadataOrderStylesAndProgress(t *testing.T) {
	spec := PipelineSpec{
		OutputWidth:  720,
		OutputHeight: 480,
		Visualizer: VisualizerSpec{
			Mode: VisualizerModeRetroAnalyzer,
			Metadata: VisualizerMetadata{
				Title:    "Blue Monday",
				Artist:   "New Order",
				Album:    "Power Corruption & Lies",
				Duration: 7*time.Minute + 29*time.Second,
			},
		},
	}
	lines := visualizerTextLines(spec)
	if len(lines) != 4 {
		t.Fatalf("visualizerTextLines len = %d, want 4: %#v", len(lines), lines)
	}
	want := []visualizerTextLine{
		{Role: visualizerTextRoleArtist, Text: "NEW ORDER", FontSize: 20, FontColor: "0x9dff9d", X: "24", Y: "24", WindowWidth: 392, Marquee: true},
		{Role: visualizerTextRoleTitle, Text: "BLUE MONDAY", FontSize: 20, FontColor: "0x9dff9d", X: "24", Y: "48", WindowWidth: 392, Marquee: true},
		{Role: visualizerTextRoleAlbum, Text: "POWER CORRUPTION & LIES", FontSize: 18, FontColor: "0x7fdc7f", X: "24", Y: "72", WindowWidth: 392, Marquee: true},
		{Role: visualizerTextRoleProgress, Text: "%{pts\\:hms} / 7:29", TrustedExpr: true, FontSize: 16, FontColor: "0x70c870", X: "w-tw-24", Y: "24"},
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %#v, want %#v", i, lines[i], want[i])
		}
	}
}

func TestVisualizerTextLines_TitleFallbackAndBlankMetadata(t *testing.T) {
	spec := PipelineSpec{
		OutputWidth:  720,
		OutputHeight: 480,
		Visualizer: VisualizerSpec{
			Mode: VisualizerModeRetroAnalyzer,
			Metadata: VisualizerMetadata{
				Title:  "   ",
				Artist: "   ",
				Album:  "",
			},
		},
	}
	lines := visualizerTextLines(spec)
	if len(lines) != 1 {
		t.Fatalf("visualizerTextLines len = %d, want 1: %#v", len(lines), lines)
	}
	if lines[0].Role != visualizerTextRoleTitle || lines[0].Text != "NOW PLAYING" {
		t.Fatalf("fallback line = %#v, want NOW PLAYING title", lines[0])
	}
	if lines[0].WindowWidth != 392 || !lines[0].Marquee {
		t.Fatalf("fallback line layout = %#v, want metadata window marquee line", lines[0])
	}
}

func TestVisualizerLayoutForLogicalCanvases(t *testing.T) {
	cases := []struct {
		name          string
		mode          VisualizerMode
		logicalW      int
		logicalH      int
		sideMargin    int
		metadataWidth int
		metadataY     []string
		progressX     string
		progressY     string
		showProgress  bool
	}{
		{
			name: "ntsc 240p upper",
			mode: VisualizerModeRetroAnalyzer,
			logicalW: 320, logicalH: 240,
			sideMargin: 16, metadataWidth: 176,
			metadataY: []string{"24", "48", "72"},
			progressX: "w-tw-16", progressY: "24",
			showProgress: true,
		},
		{
			name: "pal 288p upper",
			mode: VisualizerModeOscilloscopeWave,
			logicalW: 384, logicalH: 288,
			sideMargin: 16, metadataWidth: 234,
			metadataY: []string{"24", "48", "72"},
			progressX: "w-tw-16", progressY: "24",
			showProgress: true,
		},
		{
			name: "ntsc 480i upper",
			mode: VisualizerModeRetroAnalyzer,
			logicalW: 640, logicalH: 480,
			sideMargin: 24, metadataWidth: 392,
			metadataY: []string{"24", "48", "72"},
			progressX: "w-tw-24", progressY: "24",
			showProgress: true,
		},
		{
			name: "pal 576i lower",
			mode: VisualizerModeStereoScope,
			logicalW: 768, logicalH: 576,
			sideMargin: 24, metadataWidth: 504,
			metadataY: []string{"h-88", "h-64", "h-40"},
			progressX: "w-tw-24", progressY: "h-88",
			showProgress: true,
		},
		{
			name: "tiny unexpected canvas omits progress",
			mode: VisualizerModeRetroAnalyzer,
			logicalW: 180, logicalH: 120,
			sideMargin: 16, metadataWidth: 148,
			metadataY: []string{"24", "48", "72"},
			progressX: "", progressY: "",
			showProgress: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := visualizerLayoutFor(tc.mode, tc.logicalW, tc.logicalH)
			if got.SideMargin != tc.sideMargin || got.MetadataX != tc.sideMargin {
				t.Fatalf("margins = %#v, want side/meta %d", got, tc.sideMargin)
			}
			if got.MetadataWidth != tc.metadataWidth {
				t.Fatalf("MetadataWidth = %d, want %d in %#v", got.MetadataWidth, tc.metadataWidth, got)
			}
			if strings.Join(got.MetadataY, ",") != strings.Join(tc.metadataY, ",") {
				t.Fatalf("MetadataY = %v, want %v", got.MetadataY, tc.metadataY)
			}
			if got.ProgressX != tc.progressX || got.ProgressY != tc.progressY || got.ShowProgress != tc.showProgress {
				t.Fatalf("progress layout = %#v, want x=%q y=%q show=%v", got, tc.progressX, tc.progressY, tc.showProgress)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/ffmpeg -run 'TestVisualizerTextLines|TestVisualizerLayoutForLogicalCanvases'
```

Expected: FAIL with undefined `Role`, `FontSize`, `visualizerTextRoleArtist`, and `visualizerLayoutFor`.

- [ ] **Step 3: Implement descriptors and layout helpers**

In `internal/ffmpeg/pipeline.go`, replace the existing `visualizerTextLine` struct and `visualizerTextLines` function with this block. Keep `visualizerDrawText`, `nonEmpty`, and `formatDurationClock` below it for now; later tasks will update `visualizerDrawText`.

```go
const (
	visualizerTextRoleArtist   = "artist"
	visualizerTextRoleTitle    = "title"
	visualizerTextRoleAlbum    = "album"
	visualizerTextRoleProgress = "progress"
)

const (
	visualizerMetadataColor = "0x9dff9d"
	visualizerAlbumColor    = "0x7fdc7f"
	visualizerProgressColor = "0x70c870"
)

type visualizerTextLine struct {
	Text        string
	TrustedExpr bool
	Role        string
	FontSize    int
	FontColor   string
	X           string
	Y           string
	WindowWidth int
	Marquee     bool
}

type visualizerOverlayLayout struct {
	SideMargin   int
	MetadataX    int
	MetadataWidth int
	MetadataY    []string
	ProgressX    string
	ProgressY    string
	ShowProgress bool
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func visualizerLayoutFor(mode VisualizerMode, logicalW, logicalH int) visualizerOverlayLayout {
	sideMargin := 24
	if logicalW < 640 {
		sideMargin = 16
	}
	progressReserve := clampInt((logicalW*35+50)/100, 128, 240)
	metadataWidth := logicalW - sideMargin - progressReserve
	showProgress := metadataWidth >= 160
	if !showProgress {
		metadataWidth = logicalW - sideMargin*2
		if metadataWidth < 0 {
			metadataWidth = 0
		}
	}

	layout := visualizerOverlayLayout{
		SideMargin:    sideMargin,
		MetadataX:     sideMargin,
		MetadataWidth: metadataWidth,
		ShowProgress:  showProgress,
	}
	switch mode {
	case VisualizerModeStereoScope:
		layout.MetadataY = []string{"h-88", "h-64", "h-40"}
	default:
		layout.MetadataY = []string{"24", "48", "72"}
	}
	if showProgress {
		layout.ProgressX = fmt.Sprintf("w-tw-%d", sideMargin)
		layout.ProgressY = layout.MetadataY[0]
	}
	return layout
}

func visualizerMetadataLine(layout visualizerOverlayLayout, role, text, y string, fontSize int, color string) visualizerTextLine {
	return visualizerTextLine{
		Text:        strings.ToUpper(strings.TrimSpace(text)),
		Role:        role,
		FontSize:    fontSize,
		FontColor:   color,
		X:           fmt.Sprintf("%d", layout.MetadataX),
		Y:           y,
		WindowWidth: layout.MetadataWidth,
		Marquee:     true,
	}
}

func visualizerTextLines(s PipelineSpec) []visualizerTextLine {
	md := s.Visualizer.Metadata
	logicalW, logicalH := logicalCanvas(s.OutputHeight)
	layout := visualizerLayoutFor(s.Visualizer.Mode, logicalW, logicalH)

	lines := make([]visualizerTextLine, 0, 4)
	y := 0
	if artist := strings.TrimSpace(md.Artist); artist != "" {
		lines = append(lines, visualizerMetadataLine(layout, visualizerTextRoleArtist, artist, layout.MetadataY[y], 20, visualizerMetadataColor))
		y++
	}
	title := strings.TrimSpace(md.Title)
	if title == "" {
		title = "Now Playing"
	}
	lines = append(lines, visualizerMetadataLine(layout, visualizerTextRoleTitle, title, layout.MetadataY[y], 20, visualizerMetadataColor))
	y++
	if album := strings.TrimSpace(md.Album); album != "" {
		lines = append(lines, visualizerMetadataLine(layout, visualizerTextRoleAlbum, album, layout.MetadataY[y], 18, visualizerAlbumColor))
	}
	if md.Duration > 0 && layout.ShowProgress {
		lines = append(lines, visualizerTextLine{
			Text:        "%{pts\\:hms} / " + formatDurationClock(md.Duration),
			TrustedExpr: true,
			Role:        visualizerTextRoleProgress,
			FontSize:    16,
			FontColor:   visualizerProgressColor,
			X:           layout.ProgressX,
			Y:           layout.ProgressY,
		})
	}
	return lines
}
```

- [ ] **Step 4: Run tests to verify pass**

Run:

```bash
go test ./internal/ffmpeg -run 'TestVisualizerTextLines|TestVisualizerLayoutForLogicalCanvases'
```

Expected: PASS.

- [ ] **Step 5: Commit**

Commit only this task's files:

```bash
git add internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go
git commit -m "feat(ffmpeg): describe visualizer metadata overlay layout"
```

---

### Task 2: Render Metadata Layers, Progress, And Marquee Expressions

**Files:**
- Modify: `internal/ffmpeg/pipeline.go`
- Modify: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Replace old graph expectations with failing overlay graph tests**

Update `TestBuildVisualizerFilterChain_ApostropheInTitle` so the expected escaped string is uppercase:

```go
if !strings.Contains(graph, `text='DON'\''T STOP BELIEVIN'\'''`) {
	t.Fatalf("apostrophes not escaped via close-reopen idiom:\n%s", graph)
}
if strings.Contains(graph, `text='DON\'T`) {
	t.Fatalf("graph contains pre-fix broken `\\'` escape:\n%s", graph)
}
```

Replace the `want` list in `TestBuildVisualizerFilterChain_RetroAnalyzerShape` with:

```go
for _, want := range []string{
	"showfreqs=s=640x480",
	"colors=0x70ff70",
	"color=c=black@0.0:s=392x24",
	"color=c=black@0.0:s=392x22",
	"drawtext=text='NEW ORDER'",
	"drawtext=text='BLUE MONDAY'",
	"drawtext=text='POWER CORRUPTION & LIES'",
	"fontsize=20:fontcolor=0x9dff9d",
	"fontsize=18:fontcolor=0x7fdc7f",
	"x='if(lte(tw,392),0,-mod(max(t-1.5,0)*24,tw+24))'",
	"overlay=x=24:y=24",
	"overlay=x=24:y=48",
	"overlay=x=24:y=72",
	"drawtext=text='%{pts\\:hms} / 7:29':x=w-tw-24:y=24:fontsize=16:fontcolor=0x70c870",
	"fps=60000/1001",
	"scale=w=720:h=480",
	"format=bgr24",
	"[visualizer_video]",
} {
	if !strings.Contains(graph, want) {
		t.Fatalf("graph missing %q:\n%s", want, graph)
	}
}
```

In the same test, replace the `bad` list with:

```go
for _, bad := range []string{`\%{pts`, "(w-48)*min", "drawbox=", "Blue Monday", "New Order"} {
	if strings.Contains(graph, bad) {
		t.Fatalf("graph contains invalid or non-uppercase fragment %q:\n%s", bad, graph)
	}
}
```

Update `TestBuildVisualizerFilterChain_ModeGraphs` case expectations:

```go
{
	name: "retro analyzer",
	mode: VisualizerModeRetroAnalyzer,
	want: []string{
		"[0:a:0]showfreqs=s=640x480:mode=bar:ascale=log:fscale=log:colors=0x70ff70[viz0]",
		"overlay=x=24:y=24",
		"overlay=x=24:y=48",
		"overlay=x=24:y=72",
		"drawtext=text='%{pts\\:hms} / 7:29':x=w-tw-24:y=24",
	},
},
{
	name: "oscilloscope wave",
	mode: VisualizerModeOscilloscopeWave,
	want: []string{
		"[0:a:0]showwaves=s=640x480:mode=line:colors=0x58e8ff[viz0]",
		"overlay=x=24:y=24",
		"overlay=x=24:y=48",
		"overlay=x=24:y=72",
		"drawtext=text='%{pts\\:hms} / 7:29':x=w-tw-24:y=24",
	},
},
{
	name: "stereo scope",
	mode: VisualizerModeStereoScope,
	want: []string{
		"[0:a:0]avectorscope=s=640x480:mode=lissajous:draw=line:scale=lin:swap=0,format=rgba[viz0]",
		"overlay=x=24:y=h-88",
		"overlay=x=24:y=h-64",
		"overlay=x=24:y=h-40",
		"drawtext=text='%{pts\\:hms} / 7:29':x=w-tw-24:y=h-88",
	},
},
```

Add a new test after `TestBuildVisualizerFilterChain_ModeGraphs`:

```go
func TestBuildVisualizerFilterChain_MetadataWindowScalesWithModeline(t *testing.T) {
	cases := []struct {
		name         string
		outputH      int
		mode         VisualizerMode
		lineSize     string
		overlay      string
		progress     string
		marqueeWidth string
	}{
		{"ntsc 240p", 240, VisualizerModeRetroAnalyzer, "color=c=black@0.0:s=176x24", "overlay=x=16:y=24", "x=w-tw-16:y=24", "lte(tw,176)"},
		{"pal 288p", 288, VisualizerModeOscilloscopeWave, "color=c=black@0.0:s=234x24", "overlay=x=16:y=24", "x=w-tw-16:y=24", "lte(tw,234)"},
		{"pal 576i lower", 576, VisualizerModeStereoScope, "color=c=black@0.0:s=504x24", "overlay=x=24:y=h-88", "x=w-tw-24:y=h-88", "lte(tw,504)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := PipelineSpec{
				OutputWidth: 720, OutputHeight: tc.outputH,
				OutputFpsExpr: "50/1",
				Visualizer: VisualizerSpec{
					Enabled: true, Mode: tc.mode,
					DrawTextAvailable: true, RequiredFiltersAvailable: true,
					Metadata: VisualizerMetadata{
						Title: "Blue Monday", Artist: "New Order",
						Album: "Power Corruption & Lies", Duration: time.Minute,
					},
				},
			}
			graph, err := buildVisualizerFilterChain(spec)
			if err != nil {
				t.Fatalf("buildVisualizerFilterChain: %v", err)
			}
			for _, want := range []string{tc.lineSize, tc.overlay, tc.progress, tc.marqueeWidth} {
				if !strings.Contains(graph, want) {
					t.Fatalf("graph missing %q:\n%s", want, graph)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/ffmpeg -run 'TestBuildVisualizerFilterChain_(ApostropheInTitle|RetroAnalyzerShape|ModeGraphs|MetadataWindowScalesWithModeline)'
```

Expected: FAIL because the graph still renders title first, joined artist-album text, uniform `fontsize=24`, and no line layers.

- [ ] **Step 3: Implement line-layer graph rendering**

In `internal/ffmpeg/pipeline.go`, replace the old `visualizerDrawText` function with these helpers:

```go
func visualizerDrawText(line visualizerTextLine) string {
	if line.TrustedExpr {
		return line.Text
	}
	return escapeFilterText(line.Text)
}

func visualizerLineLayerHeight(fontSize int) int {
	return fontSize + 4
}

func visualizerMarqueeX(line visualizerTextLine) string {
	if !line.Marquee {
		return line.X
	}
	return fmt.Sprintf("if(lte(tw,%d),0,-mod(max(t-1.5,0)*24,tw+24))", line.WindowWidth)
}

func visualizerLineLayerFilter(line visualizerTextLine, idx int) string {
	return fmt.Sprintf(
		"color=c=black@0.0:s=%dx%d,format=rgba,drawtext=text='%s':x='%s':y=0:fontsize=%d:fontcolor=%s:box=1:boxcolor=0x00000099[vizline%d]",
		line.WindowWidth,
		visualizerLineLayerHeight(line.FontSize),
		visualizerDrawText(line),
		visualizerMarqueeX(line),
		line.FontSize,
		line.FontColor,
		idx,
	)
}

func visualizerOverlayFilter(base string, line visualizerTextLine, idx int, next string) string {
	return fmt.Sprintf("[%s][vizline%d]overlay=x=%s:y=%s:format=auto[%s]", base, idx, line.X, line.Y, next)
}

func visualizerProgressFilter(base string, line visualizerTextLine, next string) string {
	return fmt.Sprintf(
		"[%s]drawtext=text='%s':x=%s:y=%s:fontsize=%d:fontcolor=%s:box=1:boxcolor=0x00000099[%s]",
		base,
		visualizerDrawText(line),
		line.X,
		line.Y,
		line.FontSize,
		line.FontColor,
		next,
	)
}
```

Then replace the text-overlay loop inside `buildVisualizerFilterChain` with:

```go
	label := "viz0"
	if s.Visualizer.DrawTextAvailable {
		lineLayer := 0
		for i, line := range visualizerTextLines(s) {
			next := fmt.Sprintf("viztext%d", i)
			if line.Role == visualizerTextRoleProgress {
				parts = append(parts, visualizerProgressFilter(label, line, next))
				label = next
				continue
			}
			parts = append(parts, visualizerLineLayerFilter(line, lineLayer))
			parts = append(parts, visualizerOverlayFilter(label, line, lineLayer, next))
			lineLayer++
			label = next
		}
	}
```

- [ ] **Step 4: Run tests to verify pass for overlay graph shape**

Run:

```bash
go test ./internal/ffmpeg -run 'TestBuildVisualizerFilterChain_(ApostropheInTitle|RetroAnalyzerShape|ModeGraphs|MetadataWindowScalesWithModeline|BarsOnlyWhenDrawTextUnavailable|AllModesBarsOnlyWhenDrawTextUnavailable)'
```

Expected: PASS.

- [ ] **Step 5: Run full ffmpeg package tests**

Run:

```bash
go test ./internal/ffmpeg
```

Expected: PASS.

- [ ] **Step 6: Commit**

Commit only this task's files:

```bash
git add internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go
git commit -m "feat(ffmpeg): render compact visualizer metadata overlay"
```

---

### Task 3: Overlay Capability Fallback

**Files:**
- Modify: `internal/ffmpeg/pipeline.go`
- Modify: `internal/ffmpeg/capabilities.go`
- Modify: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Write failing tests for overlay filter requirements**

In `internal/ffmpeg/pipeline_test.go`, add this test after `TestVisualizerRequiredFilters`:

```go
func TestVisualizerOverlayFilters(t *testing.T) {
	got := RequiredVisualizerOverlayFilters()
	want := []string{"color", "drawtext", "format", "overlay"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("RequiredVisualizerOverlayFilters() = %v, want %v", got, want)
	}
}
```

Add this test after `TestWithVisualizerCapabilitiesFallsBackWhenDrawTextCannotRender`:

```go
func TestWithVisualizerCapabilitiesFallsBackWhenOverlayFilterUnavailable(t *testing.T) {
	origFilter := filterAvailableFn
	origDrawText := drawTextUsableFn
	t.Cleanup(func() {
		filterAvailableFn = origFilter
		drawTextUsableFn = origDrawText
	})
	filterAvailableFn = func(_ context.Context, _ string, filter string) (bool, error) {
		return filter != "overlay", nil
	}
	drawTextUsableFn = func(context.Context, string) (bool, error) {
		return true, nil
	}
	spec := withVisualizerCapabilities(t.Context(), PipelineSpec{
		Visualizer: VisualizerSpec{
			Enabled: true,
			Mode:    VisualizerModeRetroAnalyzer,
		},
	})
	if spec.Visualizer.DrawTextAvailable {
		t.Fatal("DrawTextAvailable = true, want false when overlay filter is unavailable")
	}
	if !spec.Visualizer.RequiredFiltersAvailable {
		t.Fatal("RequiredFiltersAvailable = false, want true; overlay fallback must not disable visualizer core")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/ffmpeg -run 'TestVisualizerOverlayFilters|TestWithVisualizerCapabilitiesFallsBackWhenOverlayFilterUnavailable'
```

Expected: FAIL with `undefined: RequiredVisualizerOverlayFilters` and/or `DrawTextAvailable = true`.

- [ ] **Step 3: Add overlay filter list**

In `internal/ffmpeg/pipeline.go`, add this function below `RequiredVisualizerFilters`:

```go
func RequiredVisualizerOverlayFilters() []string {
	return []string{"color", "drawtext", "format", "overlay"}
}
```

- [ ] **Step 4: Extend capability probing**

In `internal/ffmpeg/capabilities.go`, add this helper above `withVisualizerCapabilities`:

```go
func visualizerOverlayFiltersAvailable(ctx context.Context, ffmpegPath string) bool {
	for _, filter := range RequiredVisualizerOverlayFilters() {
		ok, err := filterAvailableFn(ctx, ffmpegPath, filter)
		if err != nil || !ok {
			return false
		}
	}
	return true
}
```

Then replace the drawtext availability block in `withVisualizerCapabilities` with:

```go
	if visualizerOverlayFiltersAvailable(checkCtx, ffmpegPath) {
		ok, err := drawTextUsableFn(checkCtx, ffmpegPath)
		if err == nil && ok {
			s.Visualizer.DrawTextAvailable = true
			return s
		}
	}
	s.Visualizer.DrawTextAvailable = false
	return s
```

This keeps the existing `RequiredFiltersAvailable` behavior independent from text overlay availability.

- [ ] **Step 5: Run targeted tests**

Run:

```bash
go test ./internal/ffmpeg -run 'TestVisualizerOverlayFilters|TestWithVisualizerCapabilities'
```

Expected: PASS.

- [ ] **Step 6: Run full ffmpeg package tests**

Run:

```bash
go test ./internal/ffmpeg
```

Expected: PASS.

- [ ] **Step 7: Commit**

Commit only this task's files:

```bash
git add internal/ffmpeg/pipeline.go internal/ffmpeg/capabilities.go internal/ffmpeg/pipeline_test.go
git commit -m "fix(ffmpeg): fallback when visualizer overlay filters are unavailable"
```

---

### Task 4: FFmpeg Smoke Test And Final Verification

**Files:**
- Modify: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Write optional FFmpeg smoke test**

Add `"os/exec"` to the import block in `internal/ffmpeg/pipeline_test.go`.

Add this test after `TestBuildVisualizerFilterChain_MetadataWindowScalesWithModeline`:

```go
func TestBuildVisualizerFilterChain_MarqueeGraphSmokeWithFFmpeg(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH; skipping marquee graph smoke test")
	}
	spec := PipelineSpec{
		OutputWidth:   720,
		OutputHeight:  480,
		OutputFpsExpr: "60000/1001",
		Visualizer: VisualizerSpec{
			Enabled:                  true,
			Mode:                     VisualizerModeRetroAnalyzer,
			DrawTextAvailable:        true,
			RequiredFiltersAvailable: true,
			Metadata: VisualizerMetadata{
				Artist:   "A Very Long Artist Name That Should Exercise The Receiver Window Marquee",
				Title:    "An Even Longer Track Title That Forces The Drawtext Width Expression To Scroll",
				Album:    "An Album Name Long Enough To Need The Same Contained Marquee Treatment",
				Duration: 7*time.Minute + 29*time.Second,
			},
		},
	}
	graph, err := buildVisualizerFilterChain(spec)
	if err != nil {
		t.Fatalf("buildVisualizerFilterChain: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), ffmpegPath,
		"-hide_banner",
		"-v", "error",
		"-f", "lavfi",
		"-i", "anullsrc=r=48000:cl=stereo",
		"-filter_complex", graph,
		"-map", "[visualizer_video]",
		"-frames:v", "1",
		"-f", "null",
		"-",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg marquee graph smoke failed: %v\n%s\ngraph:\n%s", err, string(out), graph)
	}
}
```

- [ ] **Step 2: Run smoke test**

Run:

```bash
go test ./internal/ffmpeg -run TestBuildVisualizerFilterChain_MarqueeGraphSmokeWithFFmpeg
```

Expected:
- PASS when `ffmpeg` is on `PATH`
- SKIP with `ffmpeg not on PATH` when unavailable

If the smoke test fails because the generated graph is invalid, fix `internal/ffmpeg/pipeline.go` before continuing. Do not weaken the smoke test to pass an invalid graph.

- [ ] **Step 3: Run all ffmpeg tests**

Run:

```bash
go test ./internal/ffmpeg
```

Expected: PASS.

- [ ] **Step 4: Run command-builder regression tests**

Run:

```bash
go test ./internal/ffmpeg -run 'TestBuildCommand_Visualizer|TestBuildVisualizerFilterChain'
```

Expected: PASS.

- [ ] **Step 5: Inspect generated graph manually**

Run:

```bash
go test ./internal/ffmpeg -run TestBuildVisualizerFilterChain_RetroAnalyzerShape -count=1
```

Expected: PASS. If it fails, read the printed graph and verify these invariants before fixing:

- artist/title/album are uppercase
- artist/title/album are drawn through fixed-width transparent line layers
- metadata overlays use the metadata-side x coordinate
- progress is a direct right-anchored drawtext on the opposite side
- final stage still maps `[visualizer_video]`

- [ ] **Step 6: Commit**

Commit only this task's file:

```bash
git add internal/ffmpeg/pipeline_test.go
git commit -m "test(ffmpeg): smoke visualizer metadata marquee graph"
```

- [ ] **Step 7: Final package verification**

Run:

```bash
go test ./internal/ffmpeg
go test ./internal/core ./internal/adapters/plex ./internal/adapters/jellyfin
```

Expected: PASS or SKIP only for the optional FFmpeg smoke test when `ffmpeg` is not installed. Core and adapter packages should remain behaviorally unchanged because no metadata collection or mapping code changed.

- [ ] **Step 8: Final status check**

Run:

```bash
git status --short
```

Expected: only unrelated pre-existing local edits remain. No uncommitted changes in:

- `internal/ffmpeg/pipeline.go`
- `internal/ffmpeg/capabilities.go`
- `internal/ffmpeg/pipeline_test.go`

---

## Completion Notes

After all tasks pass, request code review for the implementation range before merging. The review should focus on FFmpeg graph validity, fallback behavior, test coverage, and ensuring no adapter/core/UI behavior drifted into scope.
