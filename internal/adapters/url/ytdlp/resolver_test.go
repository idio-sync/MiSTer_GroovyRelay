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

	// Required argv: --dump-json, --no-playlist, --no-warnings, -f <fmt>, <url>.
	// Critical: the JSON-dump flag MUST be --dump-json. --print-json does
	// not exist in yt-dlp; using it would fail at every invocation in
	// production (review fix C1).
	mustContain(t, got, "--dump-json")
	mustContain(t, got, "--no-playlist")
	mustContain(t, got, "--no-warnings")
	mustContain(t, got, "-f")
	mustContain(t, got, "best")
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

func indexOf(argv []string, want string) int {
	for i, a := range argv {
		if a == want {
			return i
		}
	}
	return -1
}
