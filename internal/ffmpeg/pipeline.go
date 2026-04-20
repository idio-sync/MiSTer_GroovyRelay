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
	UseSSSeek    bool // true on direct-play (pass -ss); false on transcode (offset is in URL)
	SourceProbe  *ProbeResult

	OutputWidth  int
	OutputHeight int
	FieldOrder   string // "tff" | "bff"
	AspectMode   string // "letterbox" | "zoom" | "auto"
	CropRect     *CropRect

	SubtitleURL   string // empty = no subs
	SubtitleIndex int

	AudioSampleRate int
	AudioChannels   int

	VideoPipePath string // "pipe:3", a named pipe path, or "-" for stdout
	AudioPipePath string // "pipe:4", etc.
}

// buildFilterChain assembles the comma-delimited ffmpeg `-vf` expression.
//
// Contract: the chain ALWAYS terminates in `separatefields`, so the caller's
// rawvideo output yields one 720×240 RGB24 field per read at 59.94 Hz. The
// data plane reads hActive*vActive*3 bytes per tick and sends one
// BLIT_FIELD_VSYNC alternating field=0/field=1.
//
// Order is load-bearing:
//  1. yadif (only if interlaced source) → produces one progressive frame per input frame.
//  2. telecine (23.976p) or fps=30000/1001 (everything else) → normalise to 29.97p.
//  3. crop/scale/pad for aspect mode.
//  4. subtitle burn-in BEFORE interlacing, so captions composite on the progressive raster.
//  5. interlace=scan=tff|bff:lowpass=0 → 29.97i at OutputWidth×OutputHeight.
//  6. separatefields → 59.94 fields/sec at OutputWidth×(OutputHeight/2).
func buildFilterChain(s PipelineSpec) string {
	var filters []string

	// 1. Deinterlace source if needed. send_frame = 1 input frame → 1 output
	//    frame (not 2 — we want to preserve source rate for the next step).
	if s.SourceProbe != nil && s.SourceProbe.Interlaced {
		filters = append(filters, "yadif=mode=send_frame")
	}

	// 2. Normalise to 29.97p. telecine applies 2:3 pulldown to 23.976 sources;
	//    everything else is rate-converted with fps=30000/1001.
	if s.SourceProbe != nil {
		fr := s.SourceProbe.FrameRate
		switch {
		case fr >= 23.0 && fr < 24.0:
			filters = append(filters, "telecine=pattern=23")
		default:
			filters = append(filters, "fps=30000/1001")
		}
	}

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

	// 4. Subtitle burn-in BEFORE interlacing.
	if s.SubtitleURL != "" {
		filters = append(filters,
			fmt.Sprintf("subtitles=filename='%s':si=%d", s.SubtitleURL, s.SubtitleIndex))
	}

	// 5. Build interlaced frame (OutputWidth×OutputHeight at 29.97i).
	scan := "tff"
	if s.FieldOrder == "bff" {
		scan = "bff"
	}
	filters = append(filters, fmt.Sprintf("interlace=scan=%s:lowpass=0", scan))

	// 6. Split into fields: 29.97i → 59.94p, halving the height.
	filters = append(filters, "separatefields")

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

	// Video output: raw rgb24 fields to the video pipe.
	args = append(args,
		"-map", "0:v:0",
		"-vf", buildFilterChain(s),
		"-pix_fmt", "rgb24",
		"-f", "rawvideo",
		s.VideoPipePath,
	)

	// Audio output: s16le PCM to the audio pipe.
	args = append(args,
		"-map", "0:a:0",
		"-ar", fmt.Sprintf("%d", s.AudioSampleRate),
		"-ac", fmt.Sprintf("%d", s.AudioChannels),
		"-f", "s16le",
		s.AudioPipePath,
	)

	return exec.CommandContext(ctx, "ffmpeg", args...)
}
