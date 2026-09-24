package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cropRegex matches ffmpeg's cropdetect log line format, e.g.
//
//	"[Parsed_cropdetect_0 @ 0x55] ... crop=1920:800:0:140"
var cropRegex = regexp.MustCompile(`crop=(\d+):(\d+):(\d+):(\d+)`)

// CropProbeSpec describes one auto-crop probe of a source.
type CropProbeSpec struct {
	URL string
	// Headers are emitted as-is: the caller filters them against
	// Policy.BlockedHeaders first (core.Manager does this at its boundary).
	Headers map[string]string
	// Policy is applied before -i so it gates the crop probe's URL
	// dereference identically to ffprobe and the playback pipeline.
	Policy MediaInputPolicy
	// SampleDuration is the media time cropdetect sees per sample.
	SampleDuration time.Duration
	// Starts are sample positions in seconds, each probed with an input
	// -ss (seekable sources only) and run in parallel. Empty = a single
	// sample from the start of the stream.
	Starts []float64
}

// noCropSeek marks a sample taken from the start of the stream (no -ss).
const noCropSeek = -1.0

// ProbeCrop runs short ffmpeg cropdetect passes against spec.URL and returns
// the union of the detected rects, for PipelineSpec.CropRect when AspectMode
// == "auto". Spreading samples across the runtime keeps a logo intro or one
// dark scene from defining the crop; the union never cuts content any
// sample saw.
//
// Returns nil if no rect was ever detected (e.g. every sample saw only
// black). Returns an error only if no sample produced a rect and at least
// one failed to start ffmpeg.
//
// Each sample runs under a wall-clock timeout of SampleDuration + 5s so an
// unreachable URL cannot hang the caller.
func ProbeCrop(ctx context.Context, ffmpegPath string, spec CropProbeSpec) (*CropRect, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return probeCropWithBinary(ctx, ffmpegPath, spec)
}

// probeCropWithBinary is the testable variant: callers can supply a full
// ffmpeg path for environments where the binary isn't in the Go runtime's
// view of PATH (e.g. Windows + Git-Bash wrapper scripts).
func probeCropWithBinary(ctx context.Context, ffmpegBin string, spec CropProbeSpec) (*CropRect, error) {
	starts := spec.Starts
	if len(starts) == 0 {
		starts = []float64{noCropSeek}
	}
	rects := make([]*CropRect, len(starts))
	errs := make([]error, len(starts))
	var wg sync.WaitGroup
	for i, start := range starts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rects[i], errs[i] = probeCropSample(ctx, ffmpegBin, spec, start)
		}()
	}
	wg.Wait()
	if rect := unionCropRects(rects); rect != nil {
		return rect, nil
	}
	return nil, errors.Join(errs...)
}

func probeCropSample(ctx context.Context, ffmpegBin string, spec CropProbeSpec, start float64) (*CropRect, error) {
	probeCtx, cancel := context.WithTimeout(ctx, spec.SampleDuration+5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, ffmpegBin, cropProbeArgs(spec, start)...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	var last *CropRect
	scan := bufio.NewScanner(stderr)
	// cropdetect lines can be long when the prefix includes the filter
	// graph path; bump the max token size beyond the default 64k to be safe.
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		if rect := parseCropLine(scan.Text()); rect != nil {
			last = rect
		}
	}
	// ffmpeg exits cleanly when -t elapses; ignore non-zero exits (e.g. from
	// SIGKILL via the probeCtx timeout) because we still want any rect we
	// accumulated up to the cancel point. With reset=0 the last rect is the
	// union of everything cropdetect saw in this sample.
	_ = cmd.Wait()
	return last, nil
}

// cropProbeArgs builds the argv for one cropdetect sample. start ==
// noCropSeek samples from the start of the stream.
func cropProbeArgs(spec CropProbeSpec, start float64) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
	}
	if start >= 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", start))
	}
	args = append(args, "-t", fmt.Sprintf("%.1f", spec.SampleDuration.Seconds()))
	args = spec.Policy.Apply(args)
	if len(spec.Headers) > 0 {
		keys := make([]string, 0, len(spec.Headers))
		for k := range spec.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(spec.Headers[k])
			sb.WriteString("\r\n")
		}
		args = append(args, "-headers", sb.String())
	}
	return append(args,
		"-i", spec.URL,
		"-vf", "cropdetect=limit=24:round=2:reset=0",
		"-f", "null", "-",
	)
}

// unionCropRects returns the bounding box of the non-nil rects, or nil.
func unionCropRects(rects []*CropRect) *CropRect {
	var x1, y1, x2, y2 int
	found := false
	for _, r := range rects {
		if r == nil {
			continue
		}
		if !found {
			x1, y1, x2, y2 = r.X, r.Y, r.X+r.W, r.Y+r.H
			found = true
			continue
		}
		x1, y1 = min(x1, r.X), min(y1, r.Y)
		x2, y2 = max(x2, r.X+r.W), max(y2, r.Y+r.H)
	}
	if !found {
		return nil
	}
	return &CropRect{W: x2 - x1, H: y2 - y1, X: x1, Y: y1}
}

// PlausibleCropRect reports whether a cropdetect rect looks like black-bar
// removal on a srcW×srcH (storage-pixel) source: inside the frame, at least
// half the source on each axis, and bars of equal width on opposite edges.
// Anything else is not bars — typically a studio logo on black during the
// short probe window, which would otherwise zoom the whole session into the
// logo's box. Callers fall back to letterbox (no crop) on false. Letterbox
// bars that fail the size floor lose nothing: fitting the full frame shows
// the same picture as cropping them.
func PlausibleCropRect(r CropRect, srcW, srcH int) bool {
	if srcW <= 0 || srcH <= 0 || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 ||
		r.X+r.W > srcW || r.Y+r.H > srcH {
		return false
	}
	if 2*r.W < srcW || 2*r.H < srcH {
		return false
	}
	return barsSymmetric(r.X, srcW-r.X-r.W, srcW) && barsSymmetric(r.Y, srcH-r.Y-r.H, srcH)
}

// barsSymmetric allows cropdetect's rounding (round=2) and soft bar edges:
// opposite bars may differ by 2% of the extent, at least 16 pixels.
func barsSymmetric(a, b, extent int) bool {
	tolerance := max(16, extent/50)
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}

// parseCropLine pulls the first crop=W:H:X:Y match out of one line of
// ffmpeg stderr and returns it as a *CropRect. Returns nil if the line has
// no match.
func parseCropLine(line string) *CropRect {
	m := cropRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	w, err1 := strconv.Atoi(m[1])
	h, err2 := strconv.Atoi(m[2])
	x, err3 := strconv.Atoi(m[3])
	y, err4 := strconv.Atoi(m[4])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return nil
	}
	return &CropRect{W: w, H: h, X: x, Y: y}
}
