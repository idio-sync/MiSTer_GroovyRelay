package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// CropRect is a locked crop window produced by Task 5.4's probe pass.
// When non-nil (auto mode) it replaces the default pad-to-fit behaviour.
type CropRect struct {
	W, H, X, Y int
}

type VisualizerMode string

const (
	VisualizerModeRetroAnalyzer    VisualizerMode = "retro_analyzer"
	VisualizerModeOscilloscopeWave VisualizerMode = "oscilloscope_wave"
	VisualizerModeStereoScope      VisualizerMode = "stereo_scope"
)

type VisualizerMetadata struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
}

type VisualizerSpec struct {
	Enabled  bool
	Mode     VisualizerMode
	Metadata VisualizerMetadata

	// DrawTextAvailable is populated by ffmpeg.Spawn after probing the
	// resolved FFmpeg binary. BuildCommand stays pure and renders bars-only
	// when this is false.
	DrawTextAvailable        bool
	RequiredFiltersAvailable bool
}

// PipelineSpec is the full set of knobs the filter-chain/command builder needs.
// Callers (the control plane) construct one PipelineSpec per playback session
// and hand it to Spawn.
type PipelineSpec struct {
	InputURL     string
	InputHeaders map[string]string // for Plex transcode URL tokens
	SeekSeconds  float64
	UseSSSeek    bool         // true on direct-play (pass -ss); false on transcode (offset is in URL)
	SourceProbe  *ProbeResult // includes first audio stream presence/rate when available

	// AudioInputURL, when non-empty, makes ffmpeg take a SECOND -i input
	// for audio-only. This is the YouTube DASH path: yt-dlp returns a
	// video-only stream URL and an audio-only stream URL, and the bridge
	// muxes them via `-map 0:v -map 1:a`. Empty string preserves the
	// single-input behavior used by Plex and progressive (pre-merged)
	// YouTube formats. AudioInputHeaders apply only to the second input.
	AudioInputURL       string
	AudioInputHeaders   map[string]string
	SuppressAudioOutput bool
	CaptureInput        CaptureInputSpec

	OutputWidth  int
	OutputHeight int
	FieldOrder   string // "tff" | "bff"
	// OutputFpsExpr is the ffmpeg "fps=" filter argument the pipeline
	// uses to coerce source content to the modeline's field cadence.
	// "60000/1001" for NTSC modes (any), "50/1" for PAL modes (any).
	// Empty string defaults to "60000/1001" so spec literals built by
	// hand in tests retain pre-multi-resolution behavior.
	OutputFpsExpr string
	AspectMode    string // "letterbox" | "zoom" | "auto"
	CropRect      *CropRect
	Visualizer    VisualizerSpec

	SubtitleURL   string // deprecated; libass cannot fetch URLs. Use SubtitlePath.
	SubtitlePath  string // local filesystem path the filter graph passes to libass
	SubtitleIndex int

	AudioSampleRate int
	AudioChannels   int

	VideoPipePath string // "pipe:3", a named pipe path, or "-" for stdout
	AudioPipePath string // "pipe:4", etc.
	FFmpegPath    string // empty = "ffmpeg"

	// Policy gates how ffmpeg dereferences InputURL (and AudioInputURL
	// when set). Applied to BOTH inputs in the dual-input path so a DLNA
	// adapter that only validated the primary URL cannot leak the policy
	// at the secondary input. Zero value preserves historical argv shape.
	// See MediaInputPolicy in policy.go.
	//
	// Note: BlockedHeaders is NOT applied here. The caller (core.Manager)
	// is responsible for filtering InputHeaders / AudioInputHeaders before
	// they reach this struct so that BuildCommand stays a pure argv
	// builder over already-validated inputs (spec line 115 puts the
	// header filter at the core/FFmpeg boundary).
	Policy MediaInputPolicy
}

// audioOutputEnabled reports whether the ffmpeg command should emit the s16le
// audio output. Production callers always provide SourceProbe, so clips with
// no audio stream naturally degrade to video-only instead of failing on
// `-map 0:a:0`.
//
// When AudioInputURL is set (DASH dual-stream path), audio is unconditionally
// enabled: the existence of a separately-resolved audio URL is itself the
// signal that the source has audio, and SourceProbe (which sees only the
// video-only DASH stream) cannot tell us so.
func audioOutputEnabled(s PipelineSpec) bool {
	if s.SuppressAudioOutput {
		return false
	}
	if s.AudioSampleRate <= 0 || s.AudioChannels <= 0 {
		return false
	}
	if s.AudioInputURL != "" {
		return true
	}
	if s.SourceProbe != nil && s.SourceProbe.AudioRate <= 0 {
		return false
	}
	return true
}

func audioInputMap(s PipelineSpec) string {
	if s.AudioInputURL != "" {
		return "1:a:0"
	}
	return "0:a:0"
}

// visibleDARNum / visibleDARDen describe the displayed aspect of the output
// buffer on the target CRT. All four shipped modelines drive a 15 kHz analog
// CRT whose visible area is 4:3, so the 720×N output buffer is rendered with
// non-square pixels (8:9 PAR for NTSC 480i, etc.) and undoes any horizontal
// stretch we apply in the filter chain. To get correct aspect on screen we
// scale the source into a logical square-pixel canvas of (OutputHeight × 4/3,
// OutputHeight) and anamorphic-stretch as the final filter step.
//
// v1 hardcodes 4:3 because every shipped preset is a 4:3 CRT preset; if we
// ever need to support 16:9 NTSC monitors or non-4:3 arcade tubes this would
// become a per-modeline or per-bridge config knob.
const (
	visibleDARNum = 4
	visibleDARDen = 3
)

// logicalCanvas returns the square-pixel (W,H) the source is fitted into
// before anamorphic-stretch to (OutputWidth, OutputHeight). For NTSC 480i
// (480 high) it returns (640, 480); for NTSC 240p (240 high) it returns
// (320, 240); PAL 576i (576 high) → (768, 576); PAL 288p (288 high) → (384,
// 288). All four are even on both axes so subsequent scale/pad/crop don't
// need fractional-pixel rounding.
func logicalCanvas(outputHeight int) (int, int) {
	w := outputHeight * visibleDARNum / visibleDARDen
	if w%2 != 0 {
		w++
	}
	return w, outputHeight
}

// buildFilterChain assembles the comma-delimited ffmpeg `-vf` expression.
//
// Contract: the chain emits full-height progressive BGR24 frames at the
// modeline's field cadence (PipelineSpec.OutputFpsExpr; defaults to 59.94 Hz).
// For interlaced output modes the data plane row-stripes those frames into one
// 720x240 field per tick, mirroring the approach used by working MiSTerCast /
// Mistglow senders. We intentionally avoid ffmpeg's interlace/separatefields
// path here because it has proven less interoperable with the Groovy receiver.
//
// Order is load-bearing:
//  1. yadif (only if interlaced source) → one progressive frame per input frame.
//  2. fps=<OutputFpsExpr> → normalize every source to the modeline's field cadence.
//  3. crop/scale/pad for aspect mode in a square-pixel logical canvas.
//  4. anamorphic stretch from logical canvas to OutputWidth×OutputHeight.
//  5. subtitle burn-in on the stretched buffer.
//
// The aspect chain operates in the logical (square-pixel) canvas so a 4:3
// source fills the visible 4:3 CRT area exactly and a 16:9 source produces
// correct top/bottom letterbox bars. The anamorphic stretch in step 4 is the
// inverse of the CRT's horizontal squish, so the picture lands at correct
// aspect on screen.
func buildFilterChain(s PipelineSpec) string {
	var filters []string

	// 1. Deinterlace source if needed. send_frame = 1 input frame → 1 output
	//    frame (not 2 — we want to preserve source rate for the next step).
	if s.SourceProbe != nil && s.SourceProbe.Interlaced {
		filters = append(filters, "yadif=mode=send_frame")
	}

	// 2. Normalize every source to the modeline's field cadence. The
	//    data plane treats each output frame as the source for one field
	//    tick. NTSC presets emit "fps=60000/1001"; PAL presets emit
	//    "fps=50/1". Empty OutputFpsExpr defaults to NTSC for back-compat
	//    with hand-built specs in tests.
	fpsExpr := s.OutputFpsExpr
	if fpsExpr == "" {
		fpsExpr = "60000/1001"
	}
	filters = append(filters, "fps="+fpsExpr)

	// 3. Aspect / crop in the square-pixel logical canvas.
	logicalW, logicalH := logicalCanvas(s.OutputHeight)
	switch {
	case s.AspectMode == "auto" && s.CropRect != nil:
		r := s.CropRect
		filters = append(filters,
			fmt.Sprintf("crop=%d:%d:%d:%d", r.W, r.H, r.X, r.Y),
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease", logicalW, logicalH),
			fmt.Sprintf("pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black", logicalW, logicalH),
		)
	case s.AspectMode == "zoom":
		filters = append(filters,
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=increase", logicalW, logicalH),
			fmt.Sprintf("crop=%d:%d", logicalW, logicalH),
		)
	default: // letterbox, or auto with no probed rect yet
		filters = append(filters,
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease", logicalW, logicalH),
			fmt.Sprintf("pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black", logicalW, logicalH),
		)
	}

	// 4. Anamorphic stretch from logical canvas to the output buffer.
	//    For NTSC 480i this is 640×480 → 720×480 (PAR 8:9); the CRT undoes
	//    the stretch on display so the picture lands at correct 4:3 aspect.
	if logicalW != s.OutputWidth || logicalH != s.OutputHeight {
		filters = append(filters,
			fmt.Sprintf("scale=w=%d:h=%d", s.OutputWidth, s.OutputHeight))
	}

	// 5. Subtitle burn-in on the stretched buffer. Only filesystem paths
	//    work for libass; URL-sourced captions must be downloaded by the
	//    adapter first. Burning after the anamorphic stretch keeps subtitle
	//    glyphs proportioned in screen space rather than logical space.
	if s.SubtitlePath != "" {
		filters = append(filters,
			fmt.Sprintf("subtitles=filename='%s':si=%d", escapeSubtitlePath(s.SubtitlePath), s.SubtitleIndex))
	}

	return strings.Join(filters, ",")
}

func escapeSubtitlePath(p string) string {
	return escapeSubtitlePathFor(runtime.GOOS, p)
}

func escapeSubtitlePathFor(goos, p string) string {
	if goos == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `'`, `'\''`)
	return p
}

// escapeFilterText escapes user-supplied metadata for inclusion inside a
// drawtext `text='...'` value. FFmpeg's filtergraph parser treats the
// single-quoted region as literal (backslash escapes are NOT processed
// inside it), so the only character that needs special handling at the
// filtergraph layer is `'` itself, which must close the quote, emit an
// escaped apostrophe, and reopen: `'\''`. After the filtergraph parser
// hands the extracted value to drawtext, drawtext runs its own `%{...}`
// expansion and consumes backslash escapes, so `:`, `%`, and `\` must be
// escaped as `\:`, `\%`, and `\\` to render literally. Bracket / comma /
// semicolon are filtergraph metacharacters but are inert inside single
// quotes, so they pass through unchanged.
func escapeFilterText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\r', '\n', '\t':
			b.WriteByte(' ')
		case '\'':
			b.WriteString(`'\''`)
		case '\\':
			b.WriteString(`\\`)
		case ':':
			b.WriteString(`\:`)
		case '%':
			b.WriteString(`\%`)
		default:
			if r < 0x20 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

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
	SideMargin    int
	MetadataX     int
	MetadataWidth int
	MetadataY     []string
	ProgressX     string
	ProgressY     string
	ShowProgress  bool
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
	layout := visualizerOverlayLayout{SideMargin: sideMargin, MetadataX: sideMargin, MetadataWidth: metadataWidth, ShowProgress: showProgress}
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
	return visualizerTextLine{Text: strings.ToUpper(strings.TrimSpace(text)), Role: role, FontSize: fontSize, FontColor: color, X: fmt.Sprintf("%d", layout.MetadataX), Y: y, WindowWidth: layout.MetadataWidth, Marquee: true}
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
		lines = append(lines, visualizerTextLine{Text: "%{pts\\:hms} / " + formatDurationClock(md.Duration), TrustedExpr: true, Role: visualizerTextRoleProgress, FontSize: 16, FontColor: visualizerProgressColor, X: layout.ProgressX, Y: layout.ProgressY})
	}
	return lines
}

func visualizerDrawText(line visualizerTextLine) string {
	if line.TrustedExpr {
		return line.Text
	}
	return escapeFilterText(line.Text)
}

func visualizerProgressText(line visualizerTextLine) string {
	const prefix = "%{pts\\:hms} / "
	if strings.HasPrefix(line.Text, prefix) {
		return prefix + escapeFilterText(strings.TrimPrefix(line.Text, prefix))
	}
	return visualizerDrawText(line)
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

func visualizerOverlayY(line visualizerTextLine) string {
	if strings.HasPrefix(line.Y, "h-") {
		return "H-" + strings.TrimPrefix(line.Y, "h-")
	}
	return line.Y
}

func visualizerOverlayFilter(base string, line visualizerTextLine, idx int, next string) string {
	return fmt.Sprintf("[%s][vizline%d]overlay=x=%s:y=%s:format=auto[%s]", base, idx, line.X, visualizerOverlayY(line), next)
}

func visualizerProgressFilter(base string, line visualizerTextLine, next string) string {
	return fmt.Sprintf(
		"[%s]drawtext=text='%s':x=%s:y=%s:fontsize=%d:fontcolor=%s:box=1:boxcolor=0x00000099[%s]",
		base,
		visualizerProgressText(line),
		line.X,
		line.Y,
		line.FontSize,
		line.FontColor,
		next,
	)
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func formatDurationClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func RequiredVisualizerFilters(mode VisualizerMode) []string {
	switch mode {
	case VisualizerModeRetroAnalyzer:
		return []string{"showfreqs"}
	case VisualizerModeOscilloscopeWave:
		return []string{"showwaves"}
	case VisualizerModeStereoScope:
		return []string{"avectorscope"}
	default:
		return nil
	}
}

func RequiredVisualizerOverlayFilters() []string {
	return []string{"color", "drawtext", "overlay"}
}

func isSupportedVisualizerMode(mode VisualizerMode) bool {
	return len(RequiredVisualizerFilters(mode)) > 0
}

func visualizerCoreFilter(mode VisualizerMode, logicalW, logicalH int) string {
	switch mode {
	case VisualizerModeRetroAnalyzer:
		return fmt.Sprintf("showfreqs=s=%dx%d:mode=bar:ascale=log:fscale=log:colors=0x70ff70", logicalW, logicalH)
	case VisualizerModeOscilloscopeWave:
		return fmt.Sprintf("showwaves=s=%dx%d:mode=line:colors=0x58e8ff", logicalW, logicalH)
	case VisualizerModeStereoScope:
		return fmt.Sprintf("avectorscope=s=%dx%d:mode=lissajous:draw=line:scale=lin:swap=0,format=rgba", logicalW, logicalH)
	default:
		return ""
	}
}

func buildVisualizerFilterChain(s PipelineSpec) (string, error) {
	if !isSupportedVisualizerMode(s.Visualizer.Mode) {
		return "", fmt.Errorf("unsupported visualizer mode %q", s.Visualizer.Mode)
	}
	if !s.Visualizer.RequiredFiltersAvailable {
		return "", fmt.Errorf("required visualizer filter unavailable for mode %q", s.Visualizer.Mode)
	}
	fpsExpr := s.OutputFpsExpr
	if fpsExpr == "" {
		fpsExpr = "60000/1001"
	}
	logicalW, logicalH := logicalCanvas(s.OutputHeight)
	parts := []string{
		fmt.Sprintf("[%s]%s[viz0]", audioInputMap(s), visualizerCoreFilter(s.Visualizer.Mode, logicalW, logicalH)),
	}
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
	parts = append(parts, fmt.Sprintf("[%s]fps=%s,scale=w=%d:h=%d,format=bgr24[visualizer_video]",
		label, fpsExpr, s.OutputWidth, s.OutputHeight))
	return strings.Join(parts, ";"), nil
}

func ffmpegPathFor(s PipelineSpec) string {
	if s.FFmpegPath != "" {
		return s.FFmpegPath
	}
	return "ffmpeg"
}

// BuildCommand returns a ready-to-run *exec.Cmd for the pipeline described by
// s. The caller is responsible for wiring up the platform stream transport
// before starting the command.
//
// Seeking:
//   - Transcode path: the transcode URL encodes `offset=` server-side; do NOT
//     pass -ss. Caller sets UseSSSeek=false.
//   - Direct-play path: pass -ss <seconds> BEFORE -i so ffmpeg fast-seeks the
//     container. Caller sets UseSSSeek=true.
//
// Dual-input (DASH) path:
//
//	When s.AudioInputURL is non-empty, ffmpeg is invoked with TWO -i inputs:
//	the video-only stream as input 0 and the audio-only stream as input 1.
//	The audio output then maps `-map 1:a:0` instead of `-map 0:a:0`. Each
//	input gets its own `-headers` block (yt-dlp may return different
//	User-Agent / Origin / cookies per stream). On the direct-play seek
//	path, `-ss` is repeated before each `-i` so both streams seek to the
//	same offset and stay in sync.
func BuildCommand(ctx context.Context, s PipelineSpec) *exec.Cmd {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+genpts",
	}

	dualInput := s.AudioInputURL != ""

	// Input 0 (video). On direct-play seek, -ss precedes -i for fast-seek.
	// Policy flags must immediately precede their input (FFmpeg input
	// options apply to the next -i), so they go AFTER -ss but BEFORE
	// -headers / -i. When Policy is the zero value, Apply is a no-op
	// and argv is identical to the pre-policy implementation.
	if s.UseSSSeek && s.SeekSeconds > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", s.SeekSeconds))
	}
	if s.CaptureInput.Enabled {
		args = appendCaptureInputArgs(args, s.CaptureInput)
	} else {
		args = s.Policy.Apply(args)
		args = appendHeadersArg(args, s.InputHeaders)
		args = append(args, "-i", s.InputURL)
	}

	// Input 1 (audio), DASH path only. Repeat -ss before this -i so the
	// audio stream starts at the same offset as video — without it the two
	// streams would drift on every seek. Policy flags repeat too so the
	// secondary input is constrained identically.
	if dualInput {
		if s.UseSSSeek && s.SeekSeconds > 0 {
			args = append(args, "-ss", fmt.Sprintf("%.3f", s.SeekSeconds))
		}
		args = s.Policy.Apply(args)
		args = appendHeadersArg(args, s.AudioInputHeaders)
		args = append(args, "-i", s.AudioInputURL)
	}

	if s.Visualizer.Enabled {
		audioMap := audioInputMap(s)
		graph, err := buildVisualizerFilterChain(s)
		if err != nil {
			cmd := exec.CommandContext(ctx, ffmpegPathFor(s), "-version")
			cmd.Err = err
			return cmd
		}
		args = append(args,
			"-filter_complex", graph,
			"-map", "[visualizer_video]",
			"-pix_fmt", "bgr24",
			"-f", "rawvideo",
			s.VideoPipePath,
		)
		if audioOutputEnabled(s) {
			args = append(args,
				"-map", audioMap,
				"-ar", fmt.Sprintf("%d", s.AudioSampleRate),
				"-ac", fmt.Sprintf("%d", s.AudioChannels),
				"-f", "s16le",
				s.AudioPipePath,
			)
		}
		return exec.CommandContext(ctx, ffmpegPathFor(s), args...)
	}

	// Video output: raw full-height bgr24 progressive frames to the video
	// pipe. The data plane row-stripes these into interlaced fields when the
	// active modeline is interlaced. This matches the working MiSTerCast /
	// Mistglow senders' de facto wire byte order for Groovy mode 0
	// ("rgb888"), despite the historical name.
	args = append(args,
		"-map", "0:v:0",
		"-vf", buildFilterChain(s),
		"-pix_fmt", "bgr24",
		"-f", "rawvideo",
		s.VideoPipePath,
	)

	// Audio output: s16le PCM to the audio pipe. Omitted entirely when the
	// probe says the source has no audio stream; otherwise ffmpeg would fail
	// the session before any video is emitted.
	if audioOutputEnabled(s) {
		audioMap := audioInputMap(s)
		args = append(args,
			"-map", audioMap,
			"-ar", fmt.Sprintf("%d", s.AudioSampleRate),
			"-ac", fmt.Sprintf("%d", s.AudioChannels),
			"-f", "s16le",
			s.AudioPipePath,
		)
	}

	return exec.CommandContext(ctx, ffmpegPathFor(s), args...)
}

// appendHeadersArg formats `headers` into a single `-headers <CRLF-joined>`
// argv pair and appends it to args. Sorted by key for deterministic output
// (tests depend on this). Returns args unchanged when headers is empty —
// ffmpeg accepts no -headers and uses defaults.
func appendHeadersArg(args []string, headers map[string]string) []string {
	if len(headers) == 0 {
		return args
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(headers[k])
		sb.WriteString("\r\n")
	}
	return append(args, "-headers", sb.String())
}
