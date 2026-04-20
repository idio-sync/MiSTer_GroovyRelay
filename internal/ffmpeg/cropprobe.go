package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cropRegex matches ffmpeg's cropdetect log line format, e.g.
//   "[Parsed_cropdetect_0 @ 0x55] ... crop=1920:800:0:140"
var cropRegex = regexp.MustCompile(`crop=(\d+):(\d+):(\d+):(\d+)`)

// ProbeCrop runs a short ffmpeg cropdetect pass against inputURL and returns
// the last-detected stable crop rect. Callers pass the rect into
// PipelineSpec.CropRect when AspectMode == "auto".
//
// Returns nil if no rect was ever detected (e.g. fully-filled content or
// content that moves too fast for cropdetect to converge in the probe window).
// Returns an error if ffmpeg fails to start.
//
// The probe always runs with a wall-clock timeout of duration + 5s; that's
// enough headroom to let ffmpeg flush on short clips and to guarantee this
// call does not hang forever if the input URL is unreachable.
func ProbeCrop(ctx context.Context, inputURL string, headers map[string]string, duration time.Duration) (*CropRect, error) {
	return probeCropWithBinary(ctx, "ffmpeg", inputURL, headers, duration)
}

// probeCropWithBinary is the testable variant: callers can supply a full
// ffmpeg path for environments where the binary isn't in the Go runtime's
// view of PATH (e.g. Windows + Git-Bash wrapper scripts).
func probeCropWithBinary(ctx context.Context, ffmpegBin, inputURL string, headers map[string]string, duration time.Duration) (*CropRect, error) {
	probeCtx, cancel := context.WithTimeout(ctx, duration+5*time.Second)
	defer cancel()

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-t", fmt.Sprintf("%.1f", duration.Seconds()),
	}
	if len(headers) > 0 {
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
		args = append(args, "-headers", sb.String())
	}
	args = append(args,
		"-i", inputURL,
		"-vf", "cropdetect=limit=24:round=2:reset=0",
		"-f", "null", "-",
	)

	cmd := exec.CommandContext(probeCtx, ffmpegBin, args...)
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
	// accumulated up to the cancel point.
	_ = cmd.Wait()
	return last, nil
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
