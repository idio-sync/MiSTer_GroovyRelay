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
	AspectMode   string // "letterbox" | "zoom" | "auto"
	CropRect     *CropRect

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

// buildFilterChain assembles the comma-delimited ffmpeg `-vf` expression.
//
// Contract: the chain emits full-height progressive BGR24 frames at 59.94 Hz.
// For interlaced output modes the data plane row-stripes those frames into one
// 720x240 field per tick, mirroring the approach used by working MiSTerCast /
// Mistglow senders. We intentionally avoid ffmpeg's interlace/separatefields
// path here because it has proven less interoperable with the Groovy receiver.
//
// Order is load-bearing:
//  1. yadif (only if interlaced source) → one progressive frame per input frame.
//  2. fps=60000/1001 → normalize every source to the 59.94 Hz field cadence.
//  3. crop/scale/pad for aspect mode.
//  4. subtitle burn-in on the full progressive frame.
func buildFilterChain(s PipelineSpec) string {
	var filters []string

	// 1. Deinterlace source if needed. send_frame = 1 input frame → 1 output
	//    frame (not 2 — we want to preserve source rate for the next step).
	if s.SourceProbe != nil && s.SourceProbe.Interlaced {
		filters = append(filters, "yadif=mode=send_frame")
	}

	// 2. Normalize every source to 59.94 progressive frames/sec. The data
	//    plane treats each output frame as the source for exactly one field
	//    tick, extracting either the even or odd rows depending on the
	//    outgoing field parity.
	filters = append(filters, "fps=60000/1001")

	// 3. Aspect / crop.
	switch {
	case s.AspectMode == "auto" && s.CropRect != nil:
		r := s.CropRect
		filters = append(filters,
			fmt.Sprintf("crop=%d:%d:%d:%d", r.W, r.H, r.X, r.Y),
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease", s.OutputWidth, s.OutputHeight),
			fmt.Sprintf("pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black", s.OutputWidth, s.OutputHeight),
		)
	case s.AspectMode == "zoom":
		filters = append(filters,
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=increase", s.OutputWidth, s.OutputHeight),
			fmt.Sprintf("crop=%d:%d", s.OutputWidth, s.OutputHeight),
		)
	default: // letterbox, or auto with no probed rect yet
		filters = append(filters,
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease", s.OutputWidth, s.OutputHeight),
			fmt.Sprintf("pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black", s.OutputWidth, s.OutputHeight),
		)
	}

	// 4. Subtitle burn-in on the full progressive frame. Only filesystem
	//    paths work for libass; URL-sourced captions must be downloaded by
	//    the adapter first.
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
