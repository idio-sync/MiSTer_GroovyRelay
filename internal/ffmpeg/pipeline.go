package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// CropRect is a locked crop window produced by Task 5.4's probe pass.
// When non-nil (auto mode) it replaces the default pad-to-fit behaviour.
type CropRect struct {
	W, H, X, Y int
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

	SubtitleURL   string // deprecated; libass cannot fetch URLs. Use SubtitlePath.
	SubtitlePath  string // local filesystem path the filter graph passes to libass
	SubtitleIndex int

	AudioSampleRate int
	AudioChannels   int

	VideoPipePath string // "pipe:3", a named pipe path, or "-" for stdout
	AudioPipePath string // "pipe:4", etc.
}

// audioOutputEnabled reports whether the ffmpeg command should emit the s16le
// audio output. Production callers always provide SourceProbe, so clips with
// no audio stream naturally degrade to video-only instead of failing on
// `-map 0:a:0`.
func audioOutputEnabled(s PipelineSpec) bool {
	if s.AudioSampleRate <= 0 || s.AudioChannels <= 0 {
		return false
	}
	if s.SourceProbe != nil && s.SourceProbe.AudioRate <= 0 {
		return false
	}
	return true
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
			fmt.Sprintf("subtitles=filename='%s':si=%d", s.SubtitlePath, s.SubtitleIndex))
	}

	return strings.Join(filters, ",")
}

// BuildCommand returns a ready-to-run *exec.Cmd for the pipeline described by
// s. The caller is responsible for wiring up the fd-3/fd-4 pipes via
// cmd.ExtraFiles (see Spawn in process.go).
//
// Seeking:
//   - Transcode path: the transcode URL encodes `offset=` server-side; do NOT
//     pass -ss. Caller sets UseSSSeek=false.
//   - Direct-play path: pass -ss <seconds> BEFORE -i so ffmpeg fast-seeks the
//     container. Caller sets UseSSSeek=true.
func BuildCommand(ctx context.Context, s PipelineSpec) *exec.Cmd {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+genpts",
	}
	if s.UseSSSeek && s.SeekSeconds > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", s.SeekSeconds))
	}
	// ffmpeg's `-headers` takes a single string with all headers concatenated;
	// passing multiple `-headers` overwrites the previous value. Sort keys so
	// the output is deterministic (tests depend on this).
	if len(s.InputHeaders) > 0 {
		keys := make([]string, 0, len(s.InputHeaders))
		for k := range s.InputHeaders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(s.InputHeaders[k])
			sb.WriteString("\r\n")
		}
		args = append(args, "-headers", sb.String())
	}
	args = append(args, "-i", s.InputURL)

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
		args = append(args,
			"-map", "0:a:0",
			"-ar", fmt.Sprintf("%d", s.AudioSampleRate),
			"-ac", fmt.Sprintf("%d", s.AudioChannels),
			"-f", "s16le",
			s.AudioPipePath,
		)
	}

	return exec.CommandContext(ctx, "ffmpeg", args...)
}
