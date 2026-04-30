// resolver.go: yt-dlp Runner interface + Resolve function.
//
// Spec: docs/specs/2026-04-25-url-ytdlp-design.md §"Resolver interface".
package ytdlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner is the testable boundary around exec.CommandContext. The
// production implementation is OSRunner; tests inject a stub that
// records argv and returns canned stdout/stderr.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// OSRunner runs commands via os/exec. The default Runner.
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return so.Bytes(), se.Bytes(), err
}

// Resolution is the parsed yt-dlp JSON output, narrowed to fields the
// URL adapter cares about. Other JSON keys (formats[], duration, etc.)
// are ignored.
//
// Single-stream sources (progressive YouTube, generic direct URLs,
// HLS, etc.) populate URL/Headers and leave AudioURL/AudioHeaders
// empty. The bridge feeds one ffmpeg input.
//
// DASH (separate video + audio) sources populate URL with the
// video-only stream and AudioURL with the audio-only stream. yt-dlp
// signals this by returning a `requested_formats` array of two
// entries when the format selector merges streams (e.g. `bv*+ba`).
// The bridge then runs ffmpeg with two -i inputs.
type Resolution struct {
	URL          string            // resolved direct video URL (or HLS m3u8 in single-stream case)
	Headers      map[string]string // http_headers map for ffmpeg -headers (video stream)
	AudioURL     string            // empty in single-stream case
	AudioHeaders map[string]string // empty in single-stream case
	IsLive       bool              // true for live streams (YouTube Live, Twitch)
	Title        string            // surfaced in the URL adapter's history panel + slog
}

// Resolver runs yt-dlp and parses its JSON output. Construct one per
// adapter (binary + timeout are stable for the adapter's lifetime);
// use it concurrently from multiple play handlers.
type Resolver struct {
	Binary  string        // resolved at adapter Start via exec.LookPath
	Timeout time.Duration // hard timeout per Resolve call
	Runner  Runner        // OSRunner in prod; stub in tests
}

// Resolve invokes yt-dlp and returns the parsed Resolution.
//
// argv: --dump-json --no-playlist --no-warnings -f <format>
//
//	[--cookies <cookiesPath> if non-empty] <pageURL>
//
// CRITICAL: the JSON-dump flag is --dump-json (NOT --print-json).
// --print-json does not exist in yt-dlp; using it would cause every
// invocation to fail in production (review fix C1).
func (r *Resolver) Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*Resolution, error) {
	if r.Binary == "" {
		return nil, fmt.Errorf("ytdlp: binary not configured")
	}

	// Build argv. --dump-json must be in here; --print-json is wrong.
	//
	// We deliberately do NOT pass --no-warnings: yt-dlp's WARNING lines
	// ("Some formats are missing because no PO Token is provided",
	// "nsig extraction failed", "Sign in to confirm you're not a bot")
	// are the most useful diagnostic we get when format selection fails.
	// Stderr is only consumed in the error path below, so warnings never
	// pollute the bridge log on successful resolves.
	args := []string{
		"--dump-json",
		"--no-playlist",
		"-f", format,
	}
	if cookiesPath != "" {
		args = append(args, "--cookies", cookiesPath)
	}
	args = append(args, pageURL)

	timeoutCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	stdout, stderr, err := r.Runner.Run(timeoutCtx, r.Binary, args...)
	if err != nil {
		// Distinguish timeout from non-zero exit.
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("ytdlp: resolve timed out after %s", r.Timeout)
		}
		// NOTE: stderr may echo the input URL with embedded credentials
		// (yt-dlp's "ERROR: [generic] https://user:pass@host/...:" form).
		// Callers MUST redact via the URL adapter's redactURL helper
		// before surfacing this error to logs or HTTP responses.
		return nil, fmt.Errorf("ytdlp: %s", summarizeStderr(stderr))
	}

	// Top-level url + http_headers are the single-stream fallback.
	// requested_formats, when present and length 2, is yt-dlp's signal
	// that the selector merged a video-only + audio-only DASH pair —
	// each entry carries its own URL, http_headers, and a vcodec/acodec
	// hint we use to disambiguate which is which (yt-dlp's element
	// order is not guaranteed for non-YouTube extractors).
	var raw struct {
		URL              string            `json:"url"`
		HTTPHeaders      map[string]string `json:"http_headers"`
		IsLive           bool              `json:"is_live"`
		Title            string            `json:"title"`
		RequestedFormats []struct {
			URL         string            `json:"url"`
			HTTPHeaders map[string]string `json:"http_headers"`
			VCodec      string            `json:"vcodec"`
			ACodec      string            `json:"acodec"`
		} `json:"requested_formats"`
	}
	if err := json.Unmarshal(stdout, &raw); err != nil {
		preview := string(stdout)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return nil, fmt.Errorf("ytdlp: returned unparseable JSON: %s", preview)
	}

	// DASH dual-stream path: classify the two requested_formats as
	// video and audio by their codec hints. yt-dlp uses "none" as the
	// sentinel for "this format does not have this stream type."
	if len(raw.RequestedFormats) == 2 {
		videoIdx, audioIdx, ok := classifyDualFormats(
			raw.RequestedFormats[0].VCodec, raw.RequestedFormats[0].ACodec,
			raw.RequestedFormats[1].VCodec, raw.RequestedFormats[1].ACodec,
		)
		if !ok {
			return nil, fmt.Errorf(
				"ytdlp: requested_formats does not look like a video+audio pair (vcodec/acodec ambiguous)")
		}
		v := raw.RequestedFormats[videoIdx]
		a := raw.RequestedFormats[audioIdx]
		if v.URL == "" || a.URL == "" {
			return nil, fmt.Errorf("ytdlp: requested_formats entry missing url field")
		}
		return &Resolution{
			URL:          v.URL,
			Headers:      sanitizeHeaders(v.HTTPHeaders),
			AudioURL:     a.URL,
			AudioHeaders: sanitizeHeaders(a.HTTPHeaders),
			IsLive:       raw.IsLive,
			Title:        raw.Title,
		}, nil
	}

	// Single-stream path (progressive, HLS, or any non-merged selector).
	if raw.URL == "" {
		// Defensive: yt-dlp returned valid JSON but no URL field.
		// Surface clearly here rather than letting ffmpeg fail with
		// an opaque "empty URL" error several layers down.
		return nil, fmt.Errorf("ytdlp: JSON missing required \"url\" field")
	}

	// Sanitize http_headers against control-character injection BEFORE
	// they flow into core.SessionRequest.InputHeaders → ffmpeg's
	// -headers argument (which joins key:value pairs with \r\n).
	// A header with embedded \r/\n/\x00 from a buggy or malicious yt-dlp
	// extractor would be header-smuggled into ffmpeg's outbound HTTP.
	// The bridge runs in a trusted operator role, so this is the right
	// layer to defend at — drop offending headers and continue.
	headers := sanitizeHeaders(raw.HTTPHeaders)

	return &Resolution{
		URL:     raw.URL,
		Headers: headers,
		IsLive:  raw.IsLive,
		Title:   raw.Title,
	}, nil
}

// classifyDualFormats inspects the (vcodec, acodec) of two requested_formats
// entries and decides which is the video stream and which is the audio
// stream. yt-dlp uses the literal "none" for "this format does not provide
// this stream type." Returns (videoIdx, audioIdx, ok) — ok=false when the
// pair does not cleanly resolve to one video-only + one audio-only stream
// (e.g. both have video, both have audio, or codec hints are missing).
func classifyDualFormats(vcodec0, acodec0, vcodec1, acodec1 string) (videoIdx, audioIdx int, ok bool) {
	v0 := vcodec0 != "" && vcodec0 != "none"
	a0 := acodec0 != "" && acodec0 != "none"
	v1 := vcodec1 != "" && vcodec1 != "none"
	a1 := acodec1 != "" && acodec1 != "none"
	switch {
	case v0 && !a0 && !v1 && a1:
		return 0, 1, true
	case !v0 && a0 && v1 && !a1:
		return 1, 0, true
	default:
		return 0, 0, false
	}
}

// summarizeStderr returns up to the last 5 non-empty trimmed lines of
// buf, joined with " | ". yt-dlp typically prints WARNING lines (e.g.
// "Some formats are missing because no PO Token is provided", "nsig
// extraction failed", "Sign in to confirm you're not a bot") just
// before the final ERROR. Surfacing those in the bridge's error log
// lets the operator diagnose root cause without re-running yt-dlp by
// hand. The cap on lines keeps a single failed resolve from ballooning
// a log line when yt-dlp emits dozens of debug entries.
func summarizeStderr(buf []byte) string {
	const maxLines = 5
	lines := strings.Split(string(buf), "\n")
	var keep []string
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		keep = append([]string{s}, keep...)
		if len(keep) >= maxLines {
			break
		}
	}
	if len(keep) == 0 {
		return "no error message from yt-dlp"
	}
	return strings.Join(keep, " | ")
}

// sanitizeHeaders drops any header whose key OR value contains a CR,
// LF, or NUL byte. Those characters are header-injection primitives
// when concatenated into ffmpeg's -headers argument (which uses \r\n
// as the field separator). yt-dlp normally produces clean headers,
// but a malicious site exploiting an extractor bug, or a future
// extractor regression, could in principle return e.g.
//
//	{"User-Agent": "Mozilla/5.0\r\nX-Inject: yes"}
//
// Without this filter, the bridge would smuggle "X-Inject: yes" into
// every outbound HTTP request ffmpeg makes for the resolved stream.
// Dropping is preferable to escaping because there's no legitimate
// header value with embedded control bytes — a drop is conservative
// and never breaks a real upstream.
//
// Returns nil when in is nil or empty (preserves the "no headers"
// signal — ffmpeg's -headers gets omitted).
func sanitizeHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if containsCRLFNUL(k) || containsCRLFNUL(v) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsCRLFNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r', '\n', '\x00':
			return true
		}
	}
	return false
}
