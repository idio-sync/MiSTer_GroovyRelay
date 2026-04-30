package ytdlp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubRunner records argv and returns canned output. Multiple calls
// stack canned responses; tests assert argv from the last invocation.
type stubRunner struct {
	calls   [][]string // captured argv per call (excluding binary)
	stdouts [][]byte
	stderrs [][]byte
	errs    []error
	delays  []time.Duration // optional per-call delay before returning
}

func (s *stubRunner) Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	idx := len(s.calls)
	s.calls = append(s.calls, append([]string{}, args...))
	if idx < len(s.delays) && s.delays[idx] > 0 {
		select {
		case <-time.After(s.delays[idx]):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	var so, se []byte
	var e error
	if idx < len(s.stdouts) {
		so = s.stdouts[idx]
	}
	if idx < len(s.stderrs) {
		se = s.stderrs[idx]
	}
	if idx < len(s.errs) {
		e = s.errs[idx]
	}
	return so, se, e
}

const validJSON = `{
"url": "https://rr2--googlevideo.com/videoplayback?id=abc",
"http_headers": {"User-Agent": "Mozilla/5.0", "Referer": "https://www.youtube.com/"},
"is_live": false,
"title": "Test Video"
}`

func TestResolve_BuildsCorrectArgv(t *testing.T) {
	r := &stubRunner{stdouts: [][]byte{[]byte(validJSON)}}
	res := Resolver{Binary: "/usr/local/bin/yt-dlp", Timeout: 5 * time.Second, Runner: r}

	_, err := res.Resolve(context.Background(), "https://youtu.be/abc", "best", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(r.calls))
	}
	got := r.calls[0]

	// Required argv: --dump-json, --no-playlist, -f <fmt>, <url>.
	// Critical: the JSON-dump flag MUST be --dump-json. --print-json does
	// not exist in yt-dlp; using it would fail at every invocation in
	// production (review fix C1).
	//
	// MUST NOT contain --no-warnings: WARNING lines from yt-dlp are the
	// most useful diagnostic when format resolution fails, and stderr is
	// only consumed in the error path so warnings cannot pollute logs on
	// successful resolves.
	mustContain(t, got, "--dump-json")
	mustContain(t, got, "--no-playlist")
	mustNotContain(t, got, "--no-warnings")
	mustContain(t, got, "-f")
	mustContain(t, got, "best")
	// --js-runtimes node MUST be present: yt-dlp defaults to Deno and
	// silently ignores Node even when Node is on PATH. Without the flag,
	// YouTube signature/n-challenge solving fails and the format list
	// collapses to storyboards only.
	mustContain(t, got, "--js-runtimes")
	mustContain(t, got, "node")
	if i := indexOf(got, "--js-runtimes"); i < 0 || i+1 >= len(got) || got[i+1] != "node" {
		t.Errorf("--js-runtimes not followed by 'node'; argv = %v", got)
	}
	// -f must be immediately followed by the format string; positional
	// flag-with-arg ordering matters to yt-dlp.
	if i := indexOf(got, "-f"); i < 0 || i+1 >= len(got) || got[i+1] != "best" {
		t.Errorf("-f not followed by format string; argv = %v", got)
	}
	mustContain(t, got, "https://youtu.be/abc")

	// MUST NOT contain --cookies when cookiesPath is empty.
	for _, a := range got {
		if a == "--cookies" {
			t.Errorf("--cookies present despite empty cookiesPath; argv = %v", got)
		}
	}
}

func TestResolve_AddsCookiesFlagWhenPathProvided(t *testing.T) {
	r := &stubRunner{stdouts: [][]byte{[]byte(validJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	_, err := res.Resolve(context.Background(), "https://youtu.be/x", "best", "/data/cookies.txt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := r.calls[0]
	mustContain(t, got, "--cookies")
	mustContain(t, got, "/data/cookies.txt")

	// And ensure --cookies precedes its value (it's a flag-with-arg).
	idx := indexOf(got, "--cookies")
	if idx < 0 || idx+1 >= len(got) || got[idx+1] != "/data/cookies.txt" {
		t.Fatalf("--cookies not followed by path; argv = %v", got)
	}
}

func TestResolve_ParsesJSONIntoResolution(t *testing.T) {
	r := &stubRunner{stdouts: [][]byte{[]byte(validJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	got, err := res.Resolve(context.Background(), "https://youtu.be/x", "best", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.URL != "https://rr2--googlevideo.com/videoplayback?id=abc" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Headers["User-Agent"] != "Mozilla/5.0" {
		t.Errorf("Headers[User-Agent] = %q", got.Headers["User-Agent"])
	}
	if got.Headers["Referer"] != "https://www.youtube.com/" {
		t.Errorf("Headers[Referer] = %q", got.Headers["Referer"])
	}
	if got.IsLive {
		t.Error("IsLive = true, want false")
	}
	if got.Title != "Test Video" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestResolve_NonZeroExit_ReturnsTrimmedStderr(t *testing.T) {
	r := &stubRunner{
		stderrs: [][]byte{[]byte("WARNING: nothing\nERROR: This video is unavailable\n")},
		errs:    []error{errors.New("exit status 1")},
	}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	_, err := res.Resolve(context.Background(), "https://youtu.be/dead", "best", "")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "This video is unavailable") {
		t.Errorf("err = %q, want contains stderr last-line", err.Error())
	}
}

func TestResolve_MalformedJSON(t *testing.T) {
	r := &stubRunner{stdouts: [][]byte{[]byte("not-json-at-all\n")}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	_, err := res.Resolve(context.Background(), "https://youtu.be/x", "best", "")
	if err == nil {
		t.Fatal("want error on malformed JSON")
	}
	if !strings.Contains(err.Error(), "unparseable JSON") {
		t.Errorf("err = %q, want 'unparseable JSON'", err.Error())
	}
}

func TestResolve_DropsHeaderInjectionAttempts(t *testing.T) {
	// yt-dlp normally produces clean headers, but an exploited
	// extractor or a future bug could return values with embedded
	// CR/LF — which would smuggle into ffmpeg's -headers argument
	// (joined with \r\n). The resolver must filter these out before
	// they reach core.SessionRequest.InputHeaders.
	//
	// CR and LF cases land via JSON escapes parsed by json.Unmarshal.
	// The NUL case is exercised separately by TestSanitizeHeaders_*
	// because embedding a literal NUL in a Go-source raw-string is
	// not portable across the build-tooling pipeline.
	const injectionJSON = `{
"url": "https://example.com/v.mp4",
"http_headers": {
  "User-Agent": "Mozilla/5.0\r\nX-Injected: yes",
  "Referer": "https://safe.example/",
  "X-Bad-Key\r\nX-Smuggled": "value"
},
"is_live": false,
"title": "ok"
}`
	r := &stubRunner{stdouts: [][]byte{[]byte(injectionJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	got, err := res.Resolve(context.Background(), "https://example.com/", "best", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Only Referer (clean) should survive.
	if _, ok := got.Headers["User-Agent"]; ok {
		t.Errorf("CRLF-tainted User-Agent leaked through: %q", got.Headers["User-Agent"])
	}
	for k := range got.Headers {
		if containsCRLFNUL(k) {
			t.Errorf("CRLF-tainted key leaked through: %q", k)
		}
	}
	if got.Headers["Referer"] != "https://safe.example/" {
		t.Errorf("Referer dropped or mangled: %q", got.Headers["Referer"])
	}
	// Exactly one header should survive (Referer).
	if len(got.Headers) != 1 {
		t.Errorf("got %d headers after sanitize, want 1 (Referer); headers=%v", len(got.Headers), got.Headers)
	}
}

func TestSanitizeHeaders_StripsControlBytes(t *testing.T) {
	// Use Go's \x00 escape to produce a real NUL byte at runtime
	// without a literal NUL in the source file. Same mechanism for
	// CR/LF — these escapes survive the build-tooling pipeline
	// safely (unlike literal control bytes in raw strings).
	in := map[string]string{
		"GoodKey":    "good value",
		"BadKey\r\n": "bad-key-payload",
		"NullKey\x00":  "nul-key-payload",
		"BadValue":   "tainted\r\nX-Smuggled: yes",
		"NullValue":  "tainted\x00",
	}
	out := sanitizeHeaders(in)
	if len(out) != 1 {
		t.Fatalf("got %d headers, want 1; out=%v", len(out), out)
	}
	if out["GoodKey"] != "good value" {
		t.Errorf("GoodKey = %q, want \"good value\"", out["GoodKey"])
	}
}

func TestSanitizeHeaders_NilAndEmpty(t *testing.T) {
	if sanitizeHeaders(nil) != nil {
		t.Error("nil input should produce nil output")
	}
	if sanitizeHeaders(map[string]string{}) != nil {
		t.Error("empty input should produce nil output")
	}
	// All-bad input → empty output collapses to nil.
	bad := map[string]string{"K\r": "v", "K": "v\n"}
	if sanitizeHeaders(bad) != nil {
		t.Errorf("all-bad input should collapse to nil; got %v", sanitizeHeaders(bad))
	}
}

func TestResolve_RejectsValidJSONWithEmptyURL(t *testing.T) {
	// yt-dlp could in principle return valid JSON with no "url" field
	// (extractor bug, partial response). Resolve must surface this at
	// its own layer so the operator sees a clear ytdlp-shaped error
	// instead of a confusing ffmpeg failure several seconds later.
	const noURLJSON = `{"http_headers": {}, "is_live": false, "title": "x"}`
	r := &stubRunner{stdouts: [][]byte{[]byte(noURLJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	_, err := res.Resolve(context.Background(), "https://youtu.be/x", "best", "")
	if err == nil {
		t.Fatal("want error when JSON has no url field")
	}
	if !strings.Contains(err.Error(), "missing required \"url\"") {
		t.Errorf("err = %q, want 'missing required \"url\"'", err.Error())
	}
}

// TestResolve_ParsesRequestedFormatsAsDualStream covers the YouTube DASH
// path: yt-dlp's selector merges a video-only + audio-only pair and
// reports both via `requested_formats`. Resolve must populate
// Resolution.URL/AudioURL with the right one each, classifying them by
// vcodec/acodec.
func TestResolve_ParsesRequestedFormatsAsDualStream(t *testing.T) {
	const dashJSON = `{
"url": "https://video.googlevideo.com/v.mp4?sig=v",
"http_headers": {"User-Agent": "Mozilla/5.0"},
"is_live": false,
"title": "Test DASH Video",
"requested_formats": [
  {
    "url": "https://video.googlevideo.com/v.mp4?sig=v",
    "http_headers": {"User-Agent": "yt-dlp/video", "Origin": "https://www.youtube.com"},
    "vcodec": "avc1.4d401f",
    "acodec": "none"
  },
  {
    "url": "https://audio.googlevideo.com/a.m4a?sig=a",
    "http_headers": {"User-Agent": "yt-dlp/audio"},
    "vcodec": "none",
    "acodec": "mp4a.40.2"
  }
]
}`
	r := &stubRunner{stdouts: [][]byte{[]byte(dashJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	got, err := res.Resolve(context.Background(), "https://youtu.be/x", "bv*+ba", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.URL != "https://video.googlevideo.com/v.mp4?sig=v" {
		t.Errorf("URL = %q, want video URL", got.URL)
	}
	if got.AudioURL != "https://audio.googlevideo.com/a.m4a?sig=a" {
		t.Errorf("AudioURL = %q, want audio URL", got.AudioURL)
	}
	if got.Headers["User-Agent"] != "yt-dlp/video" {
		t.Errorf("Headers[User-Agent] = %q, want video UA", got.Headers["User-Agent"])
	}
	if got.AudioHeaders["User-Agent"] != "yt-dlp/audio" {
		t.Errorf("AudioHeaders[User-Agent] = %q, want audio UA", got.AudioHeaders["User-Agent"])
	}
	if got.Title != "Test DASH Video" {
		t.Errorf("Title = %q", got.Title)
	}
}

// TestResolve_RequestedFormatsReverseOrder: the order of entries in
// requested_formats is not guaranteed (yt-dlp puts video first for
// YouTube but other extractors may differ). Resolve must classify by
// codec hint, not by index.
func TestResolve_RequestedFormatsReverseOrder(t *testing.T) {
	const reverseJSON = `{
"url": "ignored",
"http_headers": {},
"is_live": false,
"title": "x",
"requested_formats": [
  {
    "url": "https://audio.example/a.m4a",
    "http_headers": {"User-Agent": "audio-ua"},
    "vcodec": "none",
    "acodec": "mp4a.40.2"
  },
  {
    "url": "https://video.example/v.mp4",
    "http_headers": {"User-Agent": "video-ua"},
    "vcodec": "avc1.4d401f",
    "acodec": "none"
  }
]
}`
	r := &stubRunner{stdouts: [][]byte{[]byte(reverseJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	got, err := res.Resolve(context.Background(), "https://example.com/x", "bv*+ba", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.URL != "https://video.example/v.mp4" {
		t.Errorf("URL = %q, want video URL", got.URL)
	}
	if got.AudioURL != "https://audio.example/a.m4a" {
		t.Errorf("AudioURL = %q, want audio URL", got.AudioURL)
	}
}

// TestResolve_RequestedFormatsAmbiguousRejected: if both entries claim
// video (or both claim audio), the resolver must refuse rather than
// guess. Picking the wrong one would silently drop video or audio.
func TestResolve_RequestedFormatsAmbiguousRejected(t *testing.T) {
	const ambiguousJSON = `{
"url": "ignored",
"is_live": false,
"title": "x",
"requested_formats": [
  {"url": "https://a.example/1.mp4", "vcodec": "avc1", "acodec": "mp4a"},
  {"url": "https://a.example/2.mp4", "vcodec": "avc1", "acodec": "mp4a"}
]
}`
	r := &stubRunner{stdouts: [][]byte{[]byte(ambiguousJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	_, err := res.Resolve(context.Background(), "https://example.com/x", "bv*+ba", "")
	if err == nil {
		t.Fatal("want error on ambiguous requested_formats")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("err = %q, want 'ambiguous'", err.Error())
	}
}

// TestResolve_RequestedFormatsSinglestreamFallback: a non-merge selector
// (`b` / `best`) yields one URL at the top level and either no
// requested_formats or a single-element array. Either way the
// single-stream code path runs and AudioURL stays empty.
func TestResolve_RequestedFormatsSinglestreamFallback(t *testing.T) {
	const noReqFormats = `{
"url": "https://progressive.example/v.mp4",
"http_headers": {"User-Agent": "yt-dlp"},
"is_live": false,
"title": "Progressive"
}`
	r := &stubRunner{stdouts: [][]byte{[]byte(noReqFormats)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	got, err := res.Resolve(context.Background(), "https://example.com/x", "best", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.URL != "https://progressive.example/v.mp4" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.AudioURL != "" {
		t.Errorf("AudioURL must be empty in single-stream path; got %q", got.AudioURL)
	}
	if got.AudioHeaders != nil {
		t.Errorf("AudioHeaders must be nil in single-stream path; got %v", got.AudioHeaders)
	}
}

// TestResolve_RequestedFormatsHeaderSanitization: header injection
// defenses apply equally to the dual-stream path — a tainted header on
// either stream must be dropped before reaching Resolution.
func TestResolve_RequestedFormatsHeaderSanitization(t *testing.T) {
	const taintedJSON = `{
"url": "ignored",
"is_live": false,
"title": "x",
"requested_formats": [
  {
    "url": "https://video.example/v.mp4",
    "http_headers": {"User-Agent": "Mozilla/5.0\r\nX-Inject: yes", "Referer": "https://safe/"},
    "vcodec": "avc1",
    "acodec": "none"
  },
  {
    "url": "https://audio.example/a.m4a",
    "http_headers": {"User-Agent": "good", "X-Bad\r\n": "smuggled"},
    "vcodec": "none",
    "acodec": "mp4a"
  }
]
}`
	r := &stubRunner{stdouts: [][]byte{[]byte(taintedJSON)}}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Second, Runner: r}

	got, err := res.Resolve(context.Background(), "https://example.com/x", "bv*+ba", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Video-side: tainted UA dropped, clean Referer survives.
	if _, ok := got.Headers["User-Agent"]; ok {
		t.Errorf("CRLF-tainted video UA leaked through: %q", got.Headers["User-Agent"])
	}
	if got.Headers["Referer"] != "https://safe/" {
		t.Errorf("clean Referer dropped: %v", got.Headers)
	}
	// Audio-side: tainted key dropped, clean UA survives.
	for k := range got.AudioHeaders {
		if containsCRLFNUL(k) {
			t.Errorf("CRLF-tainted audio key leaked through: %q", k)
		}
	}
	if got.AudioHeaders["User-Agent"] != "good" {
		t.Errorf("clean audio UA dropped: %v", got.AudioHeaders)
	}
}

func TestResolve_ContextTimeout(t *testing.T) {
	r := &stubRunner{
		stdouts: [][]byte{[]byte(validJSON)},
		delays:  []time.Duration{200 * time.Millisecond},
	}
	res := Resolver{Binary: "yt-dlp", Timeout: 50 * time.Millisecond, Runner: r}

	start := time.Now()
	_, err := res.Resolve(context.Background(), "https://youtu.be/slow", "best", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("want timeout error")
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("Resolve took %v, want <150ms (timeout was 50ms)", elapsed)
	}
}

// helpers

func mustContain(t *testing.T, argv []string, want string) {
	t.Helper()
	for _, a := range argv {
		if a == want {
			return
		}
	}
	t.Errorf("argv missing %q; got %v", want, argv)
}

func mustNotContain(t *testing.T, argv []string, banned string) {
	t.Helper()
	for _, a := range argv {
		if a == banned {
			t.Errorf("argv contains banned arg %q; got %v", banned, argv)
			return
		}
	}
}

func indexOf(argv []string, want string) int {
	for i, a := range argv {
		if a == want {
			return i
		}
	}
	return -1
}
