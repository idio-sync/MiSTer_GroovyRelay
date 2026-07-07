# Audio URL Streams Visualizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make audio-only URL, yt-dlp, Streams/user-provider, and Local Files browser sources start CRT playback through the existing music visualizer while preserving current video behavior.

**Architecture:** Add a small shared adapter helper for audio-only classification and visualizer request shaping, then wire it into URL and Streams at their existing request-building seams. URL and Streams receive ffprobe resolution like Local Files, use bounded best-effort probes only when metadata is ambiguous or direct, and fall back to the existing video path on any classification failure.

**Tech Stack:** Go, existing `core.SessionRequest`, `ffmpeg.ProbeInputSpec`, yt-dlp JSON parsing, URL/Streams adapter tests, Local Files adapter tests, and hermetic fake probe/core seams.

---

Source spec: `docs/superpowers/specs/2026-07-07-audio-url-streams-visualizer-design.md`

Do not execute this plan until the user approves it. This document is the review artifact.

## File Structure

- Modify `internal/ffmpeg/input.go`: add `Headers` to `ProbeInputSpec`.
- Modify `internal/ffmpeg/probe.go`: emit `-headers` for probe URL inputs and update comments.
- Modify `internal/ffmpeg/probe_test.go`: pin probe header argv ordering.
- Modify `internal/core/manager.go`: pass filtered primary input headers into core's normal start probe.
- Modify `internal/core/manager_test.go`: prove filtered headers reach `probeInputFn`.
- Create `internal/adapters/audio_visualizer.go`: shared audio classifier, visualizer request helper, and classification log redaction helpers.
- Create `internal/adapters/audio_visualizer_test.go`: helper, classifier, and redaction tests.
- Modify `internal/adapters/url/ytdlp/resolver.go`: parse top-level `vcodec`, `acodec`, and `duration`.
- Modify `internal/adapters/url/ytdlp/resolver_test.go`: resolver media-shape tests.
- Modify `internal/adapters/url/adapter.go`: add ffprobe resolver and probe seam.
- Modify `internal/adapters/url/play.go`: classify yt-dlp/direct URL media and route clear audio-only sessions to the visualizer.
- Modify `internal/adapters/url/play_test.go`: URL yt-dlp, direct audio, and HLS bypass/fallback tests.
- Modify `internal/adapters/streams/adapter.go`: add ffprobe resolver and probe seam.
- Modify `internal/adapters/streams/playback.go`: classify resolved and direct stream items before starting core or opening HLS buffers.
- Modify `internal/adapters/streams/playback_test.go`: Streams resolved/direct/user-provider audio visualizer tests.
- Modify `internal/adapters/localfiles/cast.go`: replace duplicated visualizer shaping with the shared helper.
- Modify `internal/adapters/localfiles/cast_test.go`: assert Local Files visualizer metadata still includes library album context.
- Modify `cmd/mister-groovy-relay/main.go`: pass `ffprobeResolver` into URL and Streams adapter configs.
- Modify constructor call sites that fail compilation after adding optional URL/Streams `FFprobe` fields; Task 8 identifies concrete fixes with package tests.

## Task 1: Thread Headers Through ffprobe

**Files:**
- Modify: `internal/ffmpeg/input.go`
- Modify: `internal/ffmpeg/probe.go`
- Modify: `internal/ffmpeg/probe_test.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write the failing ffprobe header test**

Add this test to `internal/ffmpeg/probe_test.go` near `TestProbeInputURLAppliesPolicyBeforeURL`:

```go
func TestProbeInputURLAppliesHeadersBeforeURL(t *testing.T) {
	cmd := probeCommand("ffprobe", ProbeInputSpec{
		URL: "https://media.example/audio.m4a",
		Headers: map[string]string{
			"Referer":    "https://sound.example/",
			"User-Agent": "GroovyRelay",
		},
		Policy: MediaInputPolicy{
			ProtocolWhitelist: []string{"http", "https", "tcp", "tls"},
			BlockedHeaders:    []string{"Referer"},
		},
	})

	assertArgsContainSubsequence(t, cmd.Args, []string{
		"-protocol_whitelist", "http,https,tcp,tls",
		"-headers", "User-Agent: GroovyRelay\r\n",
		"https://media.example/audio.m4a",
	})
	for _, arg := range cmd.Args {
		if strings.Contains(arg, "Referer:") {
			t.Fatalf("blocked Referer reached ffprobe argv: %v", cmd.Args)
		}
	}
}
```

- [ ] **Step 2: Run the ffmpeg probe test to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ffmpeg -run TestProbeInputURLAppliesHeadersBeforeURL
```

Expected: FAIL because `ProbeInputSpec.Headers` does not exist.

- [ ] **Step 3: Implement probe headers**

Update `internal/ffmpeg/input.go`:

```go
type ProbeInputSpec struct {
	URL     string
	Headers map[string]string
	Policy  MediaInputPolicy
	Capture CaptureInputSpec
	Timeout time.Duration
}
```

Update the non-capture branch in `probeCommandContext` in `internal/ffmpeg/probe.go`:

```go
	} else {
		args = input.Policy.Apply(args)
		args = appendHeadersArg(args, input.Policy.FilterHeaders(input.Headers))
		args = append(args, input.URL)
	}
```

Update the `Probe` comment to remove the old statement that headers are not threaded through.

- [ ] **Step 4: Verify ffmpeg probe GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ffmpeg -run "TestProbeInputURLApplies"
```

Expected: PASS.

- [ ] **Step 5: Write the failing core probe-header test**

Add to `internal/core/manager_test.go` near `TestProbeForStart_ThreadsPolicyToProbeAndProbeCrop`:

```go
func TestProbeForStart_ThreadsFilteredHeadersToProbeInput(t *testing.T) {
	origProbe := probeInputFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeInputFn = origProbe
		probeCropFn = origCrop
	})

	var capturedHeaders map[string]string
	probeInputFn = func(_ context.Context, _ string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		capturedHeaders = input.Headers
		return &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 60}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}

	m := newTestManager(t)
	_, _, _, err := m.probeForStart(SessionRequest{
		StreamURL: "https://media.example/audio.m4a",
		InputHeaders: map[string]string{
			"Cookie":     "secret",
			"User-Agent": "GroovyRelay",
		},
		MediaInputPolicy: MediaInputPolicy{BlockedHeaders: []string{"Cookie"}},
	})
	if err != nil {
		t.Fatalf("probeForStart: %v", err)
	}
	if _, ok := capturedHeaders["Cookie"]; ok {
		t.Fatalf("Cookie reached ProbeInput headers: %v", capturedHeaders)
	}
	if capturedHeaders["User-Agent"] != "GroovyRelay" {
		t.Fatalf("User-Agent header = %q, want GroovyRelay", capturedHeaders["User-Agent"])
	}
}
```

- [ ] **Step 6: Run the core test to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/core -run TestProbeForStart_ThreadsFilteredHeadersToProbeInput
```

Expected: FAIL because `probeForStart` does not populate `ProbeInputSpec.Headers`.

- [ ] **Step 7: Implement core probe header threading**

Update `probeInput` construction in `internal/core/manager.go`:

```go
	filteredProbeHeaders := req.MediaInputPolicy.FilterHeaders(req.InputHeaders)
	probeInput := ffmpeg.ProbeInputSpec{
		URL:     probeURL,
		Headers: filteredProbeHeaders,
		Policy:  req.MediaInputPolicy,
		Capture: ffmpegCaptureSpec(req.AudioCapture),
	}
```

- [ ] **Step 8: Verify Task 1 tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ffmpeg ./internal/core -run "TestProbeInputURLAppliesHeadersBeforeURL|TestProbeForStart_ThreadsFilteredHeadersToProbeInput|TestProbeForStart_ThreadsPolicyToProbeAndProbeCrop"
```

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

```bash
git add internal/ffmpeg/input.go internal/ffmpeg/probe.go internal/ffmpeg/probe_test.go internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(ffmpeg): pass headers to ffprobe inputs"
```

## Task 2: Shared Audio Visualizer Helper

**Files:**
- Create: `internal/adapters/audio_visualizer.go`
- Create: `internal/adapters/audio_visualizer_test.go`
- Modify: `internal/adapters/localfiles/cast.go`
- Modify: `internal/adapters/localfiles/cast_test.go`

- [ ] **Step 1: Write failing helper tests**

Create `internal/adapters/audio_visualizer_test.go`:

```go
package adapters

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func TestClassifyCodecs(t *testing.T) {
	tests := []struct {
		name   string
		vcodec string
		acodec string
		want   AudioClassification
	}{
		{"video wins", "avc1", "mp4a", AudioClassificationVideo},
		{"audio only", "none", "opus", AudioClassificationAudioOnly},
		{"both none unknown", "none", "none", AudioClassificationUnknown},
		{"missing video unknown", "", "mp4a", AudioClassificationUnknown},
		{"case and whitespace", " NONE ", " MP4A.40.2 ", AudioClassificationAudioOnly},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodecs(tc.vcodec, tc.acodec); got != tc.want {
				t.Fatalf("ClassifyCodecs(%q,%q) = %v, want %v", tc.vcodec, tc.acodec, got, tc.want)
			}
		})
	}
}

func TestClassifyProbeResult(t *testing.T) {
	if got := ClassifyProbeResult(&ffmpeg.ProbeResult{AudioRate: 44100}); got != AudioClassificationAudioOnly {
		t.Fatalf("audio-only probe = %v", got)
	}
	if got := ClassifyProbeResult(&ffmpeg.ProbeResult{Width: 1920, AudioRate: 48000}); got != AudioClassificationVideo {
		t.Fatalf("video probe = %v", got)
	}
	if got := ClassifyProbeResult(nil); got != AudioClassificationUnknown {
		t.Fatalf("nil probe = %v", got)
	}
}

func TestApplyAudioOnlyVisualizer(t *testing.T) {
	req := core.SessionRequest{MediaKind: core.MediaKindVideo}
	ApplyAudioOnlyVisualizer(&req, AudioOnlyVisualizerMetadata{
		Title:    "Track",
		Artist:   "Artist",
		Album:    "Album",
		Duration: 2 * time.Minute,
	})
	if req.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want music", req.MediaKind)
	}
	if !req.Visualizer.Enabled || req.Visualizer.Mode != core.VisualizerModeRetroAnalyzer {
		t.Fatalf("Visualizer = %+v", req.Visualizer)
	}
	if req.Visualizer.Metadata.Title != "Track" ||
		req.Visualizer.Metadata.Artist != "Artist" ||
		req.Visualizer.Metadata.Album != "Album" ||
		req.Visualizer.Metadata.Duration != 2*time.Minute ||
		req.Visualizer.Metadata.ArtworkPath != "" {
		t.Fatalf("metadata = %+v", req.Visualizer.Metadata)
	}
}

func TestRedactMediaURLForLogStripsSecrets(t *testing.T) {
	raw := "https://user:secret@cdn.example/audio.m4a?sig=supersecret#fragment"
	got := RedactMediaURLForLog(raw)
	if got != "https://cdn.example/audio.m4a" {
		t.Fatalf("RedactMediaURLForLog = %q", got)
	}
	for _, leak := range []string{"user", "secret", "sig=", "supersecret", "fragment"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted URL leaked %q: %q", leak, got)
		}
	}
}

func TestSanitizeMediaProbeErrorReplacesRawURL(t *testing.T) {
	raw := "https://user:secret@cdn.example/audio.m4a?sig=supersecret#fragment"
	got := SanitizeMediaProbeError(raw, errors.New("ffprobe failed opening "+raw))
	if strings.Contains(got, "secret") || strings.Contains(got, "sig=") || strings.Contains(got, "fragment") {
		t.Fatalf("sanitized error leaked URL secret: %q", got)
	}
	if !strings.Contains(got, "https://cdn.example/audio.m4a") {
		t.Fatalf("sanitized error missing safe URL context: %q", got)
	}
}
```

- [ ] **Step 2: Run helper tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters -run "TestClassify|TestApplyAudioOnlyVisualizer|TestRedactMediaURLForLogStripsSecrets|TestSanitizeMediaProbeErrorReplacesRawURL"
```

Expected: FAIL because the helper types and functions do not exist.

- [ ] **Step 3: Implement helper**

Create `internal/adapters/audio_visualizer.go`:

```go
package adapters

import (
	"net/url"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

type AudioClassification int

const (
	AudioClassificationUnknown AudioClassification = iota
	AudioClassificationVideo
	AudioClassificationAudioOnly
)

type AudioOnlyVisualizerMetadata struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
}

func ClassifyCodecs(vcodec, acodec string) AudioClassification {
	v := strings.ToLower(strings.TrimSpace(vcodec))
	a := strings.ToLower(strings.TrimSpace(acodec))
	if v != "" && v != "none" {
		return AudioClassificationVideo
	}
	if v == "none" && a != "" && a != "none" {
		return AudioClassificationAudioOnly
	}
	return AudioClassificationUnknown
}

func ClassifyProbeResult(result *ffmpeg.ProbeResult) AudioClassification {
	if result == nil {
		return AudioClassificationUnknown
	}
	if result.Width > 0 || result.VideoCodec != "" {
		return AudioClassificationVideo
	}
	if result.AudioRate > 0 || result.AudioCodec != "" {
		return AudioClassificationAudioOnly
	}
	return AudioClassificationUnknown
}

func ApplyAudioOnlyVisualizer(req *core.SessionRequest, meta AudioOnlyVisualizerMetadata) {
	if req == nil {
		return
	}
	req.MediaKind = core.MediaKindMusic
	req.Visualizer = core.VisualizerRequest{
		Enabled: true,
		Mode:    core.VisualizerModeRetroAnalyzer,
		Metadata: core.VisualizerMetadata{
			Title:       meta.Title,
			Artist:      meta.Artist,
			Album:       meta.Album,
			Duration:    meta.Duration,
			ArtworkPath: "",
		},
	}
}

func RedactMediaURLForLog(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return "<unparseable url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func SanitizeMediaProbeError(mediaURL string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.TrimSpace(mediaURL) == "" {
		return msg
	}
	return strings.ReplaceAll(msg, mediaURL, RedactMediaURLForLog(mediaURL))
}
```

- [ ] **Step 4: Refactor Local Files to use helper**

In `internal/adapters/localfiles/cast.go`, remove the direct `core.VisualizerRequest` construction and replace it with:

```go
	if isAudioOnlyProbe(probeResult) {
		adapters.ApplyAudioOnlyVisualizer(&req, adapters.AudioOnlyVisualizerMetadata{
			Title:    title,
			Album:    lib.Name,
			Duration: time.Duration(probeResult.Duration * float64(time.Second)),
		})
	}
```

Ensure imports include both `time` and `github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters`.

- [ ] **Step 5: Strengthen the Local Files visualizer test**

In `TestCastStartsAudioVisualizerSession` in `internal/adapters/localfiles/cast_test.go`, update the metadata assertion:

```go
	if req.Visualizer.Metadata.Title != "Song" ||
		req.Visualizer.Metadata.Album != "Music" ||
		req.Visualizer.Metadata.ArtworkPath != "" {
		t.Fatalf("visualizer metadata = %+v", req.Visualizer.Metadata)
	}
```

- [ ] **Step 6: Verify Task 2 tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters ./internal/adapters/localfiles
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add internal/adapters/audio_visualizer.go internal/adapters/audio_visualizer_test.go internal/adapters/localfiles/cast.go internal/adapters/localfiles/cast_test.go
git commit -m "feat(adapters): share audio visualizer request helper"
```

## Task 3: Parse yt-dlp Media Shape

**Files:**
- Modify: `internal/adapters/url/ytdlp/resolver.go`
- Modify: `internal/adapters/url/ytdlp/resolver_test.go`

- [ ] **Step 1: Write failing yt-dlp media-shape tests**

Add to `internal/adapters/url/ytdlp/resolver_test.go`:

```go
func TestResolve_ParsesSingleStreamCodecsAndDuration(t *testing.T) {
	const audioJSON = `{
"url": "https://audio.example/track.m4a",
"http_headers": {"User-Agent": "yt-dlp"},
"title": "Synth Jam",
"channel": "Patch Notes",
"vcodec": "none",
"acodec": "mp4a.40.2",
"duration": 123.5
}`
	runner := &stubRunner{stdouts: [][]byte{[]byte(audioJSON)}}
	r := &Resolver{Binary: "yt-dlp", Timeout: time.Second, Runner: runner}

	res, err := r.Resolve(context.Background(), "https://soundcloud.com/artist/track", "best", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.VCodec != "none" || res.ACodec != "mp4a.40.2" {
		t.Fatalf("codecs = %q/%q", res.VCodec, res.ACodec)
	}
	if res.Duration != 123500*time.Millisecond {
		t.Fatalf("Duration = %s, want 123.5s", res.Duration)
	}
}

func TestResolve_MissingSingleStreamCodecsStayEmpty(t *testing.T) {
	runner := &stubRunner{stdouts: [][]byte{[]byte(validJSON)}}
	r := &Resolver{Binary: "yt-dlp", Timeout: time.Second, Runner: runner}

	res, err := r.Resolve(context.Background(), "https://example.com/watch", "best", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.VCodec != "" || res.ACodec != "" {
		t.Fatalf("missing codecs should stay empty, got %q/%q", res.VCodec, res.ACodec)
	}
}
```

- [ ] **Step 2: Run yt-dlp tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url/ytdlp -run "TestResolve_ParsesSingleStreamCodecsAndDuration|TestResolve_MissingSingleStreamCodecsStayEmpty"
```

Expected: FAIL because `Resolution.VCodec`, `Resolution.ACodec`, and `Resolution.Duration` do not exist.

- [ ] **Step 3: Implement yt-dlp media-shape parsing**

Update the `Resolution` comment in `resolver.go` so it no longer says `duration` and codec fields are ignored, then add fields:

```go
	VCodec   string
	ACodec   string
	Duration time.Duration
```

Update the raw JSON struct:

```go
		VCodec     string  `json:"vcodec"`
		ACodec     string  `json:"acodec"`
		Duration   float64 `json:"duration"`
```

Add helper near `firstNonEmptyStr` or the other resolver helpers:

```go
func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
```

Populate both single-stream and dual-stream returns with `Duration: durationFromSeconds(raw.Duration)`. Populate `VCodec` and `ACodec` only on the single-stream return:

```go
		VCodec:     raw.VCodec,
		ACodec:     raw.ACodec,
		Duration:   durationFromSeconds(raw.Duration),
```

Dual-stream results should keep `VCodec` and `ACodec` empty because `AudioURL != ""` already classifies them as video at adapter level.

- [ ] **Step 4: Verify Task 3 tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url/ytdlp
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
git add internal/adapters/url/ytdlp/resolver.go internal/adapters/url/ytdlp/resolver_test.go
git commit -m "feat(ytdlp): expose resolved media shape"
```

## Task 4: URL Adapter Audio Classification

**Files:**
- Modify: `internal/adapters/url/adapter.go`
- Modify: `internal/adapters/url/play.go`
- Modify: `internal/adapters/url/play_test.go`
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Write failing URL tests**

Add to `internal/adapters/url/play_test.go`:

```go
type staticProbeResolver struct {
	path string
	err  error
}

func (s staticProbeResolver) Resolve() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.path, nil
}

func enableURLProbeForTest(a *Adapter) {
	a.ffprobe = staticProbeResolver{path: "ffprobe"}
}

func TestCastURL_YTDLPAudioOnlyStartsVisualizer(t *testing.T) {
	fc := &fakeCore{}
	a := newAdapterWithResolver(t, &fakeResolver{res: &ytdlp.Resolution{
		URL:      "https://cdn.example/song.m4a",
		Headers:  map[string]string{"User-Agent": "yt-dlp"},
		Title:    "Night Drive",
		Channel:  "Tape Deck",
		VCodec:   "none",
		ACodec:   "mp4a.40.2",
		Duration: 90 * time.Second,
	}})
	a.core = fc
	a.cfg.YtdlpHosts = []string{"soundcloud.com"}

	_, via, status, err := a.castURL(t.Context(), "https://soundcloud.com/tape/night-drive", "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if via != "ytdlp" || status != http.StatusOK {
		t.Fatalf("via/status = %q/%d", via, status)
	}
	req := fc.snapshot()
	if req.MediaKind != core.MediaKindMusic || !req.Visualizer.Enabled {
		t.Fatalf("request did not start visualizer: %+v", req)
	}
	if req.Visualizer.Metadata.Title != "Night Drive" ||
		req.Visualizer.Metadata.Artist != "Tape Deck" ||
		req.Visualizer.Metadata.Duration != 90*time.Second {
		t.Fatalf("visualizer metadata = %+v", req.Visualizer.Metadata)
	}
}

func TestCastURL_YTDLPUnknownCodecsFallBackToVideoWhenProbeUnavailable(t *testing.T) {
	fc := &fakeCore{}
	a := newAdapterWithResolver(t, &fakeResolver{res: &ytdlp.Resolution{
		URL:   "https://cdn.example/media",
		Title: "Unknown Shape",
	}})
	a.core = fc
	a.ffprobe = nil
	a.cfg.YtdlpHosts = []string{"bandcamp.com"}

	if _, _, _, err := a.castURL(t.Context(), "https://artist.bandcamp.com/track/x", "auto"); err != nil {
		t.Fatalf("castURL: %v", err)
	}
	req := fc.snapshot()
	if req.MediaKind == core.MediaKindMusic || req.Visualizer.Enabled {
		t.Fatalf("unknown codecs should preserve video fallback: %+v", req)
	}
}

func TestCastURL_YTDLPAmbiguousCodecsProbeUsesResolvedHeaders(t *testing.T) {
	fc := &fakeCore{}
	a := newAdapterWithResolver(t, &fakeResolver{res: &ytdlp.Resolution{
		URL:     "https://cdn.example/audio?sig=secret",
		Headers: map[string]string{"User-Agent": "yt-dlp", "Referer": "https://soundcloud.com/"},
		Title:   "Header Track",
	}})
	a.core = fc
	a.cfg.YtdlpHosts = []string{"soundcloud.com"}
	enableURLProbeForTest(a)
	var captured ffmpeg.ProbeInputSpec
	a.probeMedia = func(_ context.Context, _ string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		captured = input
		return &ffmpeg.ProbeResult{AudioRate: 44100}, nil
	}

	if _, _, _, err := a.castURL(t.Context(), "https://soundcloud.com/tape/header-track", "auto"); err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if captured.URL != "https://cdn.example/audio?sig=secret" {
		t.Fatalf("probe URL = %q", captured.URL)
	}
	if captured.Headers["User-Agent"] != "yt-dlp" || captured.Headers["Referer"] != "https://soundcloud.com/" {
		t.Fatalf("probe headers = %v", captured.Headers)
	}
	if fc.snapshot().MediaKind != core.MediaKindMusic {
		t.Fatalf("ambiguous probed audio should visualize: %+v", fc.snapshot())
	}
}

func TestCastURL_DirectAudioProbeStartsVisualizer(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	enableURLProbeForTest(a)
	a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 44100, Duration: 12}, nil
	}

	if _, _, _, err := a.castURL(t.Context(), "https://media.example/song.mp3", "direct"); err != nil {
		t.Fatalf("castURL: %v", err)
	}
	req := fc.snapshot()
	if req.MediaKind != core.MediaKindMusic || !req.Visualizer.Enabled {
		t.Fatalf("direct audio should start visualizer: %+v", req)
	}
	if req.Visualizer.Metadata.Title != "song.mp3" || req.Visualizer.Metadata.Duration != 12*time.Second {
		t.Fatalf("visualizer metadata = %+v", req.Visualizer.Metadata)
	}
}

func TestCastURL_DirectAudioOnlyHLSBypassesBuffer(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	enableURLBridgeHLSBufferForTest(a)
	enableURLProbeForTest(a)
	a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 44100}, nil
	}
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		t.Fatal("audio-only HLS should bypass URL HLS buffer")
		return nil, nil
	}

	raw := "https://media.example/radio.m3u8"
	if _, _, _, err := a.castURL(t.Context(), raw, "direct"); err != nil {
		t.Fatalf("castURL: %v", err)
	}
	req := fc.snapshot()
	if req.StreamURL != raw || req.MediaKind != core.MediaKindMusic || !req.Visualizer.Enabled {
		t.Fatalf("request = %+v, want direct audio visualizer", req)
	}
}
```

Ensure `internal/adapters/url/play_test.go` imports `github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg`.

Add this compact helper near the existing URL HLS tests:

```go
func enableURLBridgeHLSBufferForTest(a *Adapter) {
	a.bridge.HLSBuffer.Enabled = true
	a.bridge.HLSBuffer.LiveEdgeSegments = 3
	a.bridge.HLSBuffer.StartSegments = 2
	a.bridge.HLSBuffer.MaxCachedSegments = 6
	a.bridge.HLSBuffer.MaxCacheBytes = 268435456
	a.bridge.HLSBuffer.MaxPlaylistBytes = 1048576
	a.bridge.HLSBuffer.MaxSegmentBytes = 52428800
	a.bridge.HLSBuffer.SegmentTimeoutSeconds = 10
	a.bridge.HLSBuffer.PlaylistTimeoutSeconds = 10
	a.bridge.HLSBuffer.MaxVariantHeight = 720
	a.bridge.HLSBuffer.StaleCacheReapHours = 24
}
```

- [ ] **Step 2: Run URL tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run "TestCastURL_YTDLPAudioOnlyStartsVisualizer|TestCastURL_YTDLPAmbiguousCodecsProbeUsesResolvedHeaders|TestCastURL_YTDLPUnknownCodecsFallBackToVideoWhenProbeUnavailable|TestCastURL_DirectAudioProbeStartsVisualizer|TestCastURL_DirectAudioOnlyHLSBypassesBuffer"
```

Expected: FAIL because URL adapter has no ffprobe seam and does not apply visualizer classification.

- [ ] **Step 3: Add URL ffprobe wiring and probe seam**

In `internal/adapters/url/adapter.go`, add the `github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg` import and add:

```go
type binaryResolver interface {
	Resolve() (string, error)
}

type urlMediaProbeFunc func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error)
```

Add fields to `Adapter`:

```go
	ffprobe   binaryResolver
	probeMedia urlMediaProbeFunc
```

Add `FFprobe binaryResolver` to `AdapterConfig`, and initialize in `New`:

```go
		ffprobe:   cfg.FFprobe,
		probeMedia: ffmpeg.ProbeInput,
```

- [ ] **Step 4: Implement URL classification helpers**

In `internal/adapters/url/play.go`, add:

```go
const audioClassificationProbeTimeout = 800 * time.Millisecond

func (a *Adapter) classifyResolvedURLMedia(ctx context.Context, res *ytdlp.Resolution, policy core.MediaInputPolicy) (adapters.AudioClassification, *ffmpeg.ProbeResult) {
	if res == nil {
		return adapters.AudioClassificationUnknown, nil
	}
	if strings.TrimSpace(res.AudioURL) != "" {
		return adapters.AudioClassificationVideo, nil
	}
	if c := adapters.ClassifyCodecs(res.VCodec, res.ACodec); c != adapters.AudioClassificationUnknown {
		return c, nil
	}
	return a.classifyURLByProbe(ctx, res.URL, res.Headers, policy)
}

func (a *Adapter) classifyURLByProbe(ctx context.Context, mediaURL string, headers map[string]string, policy core.MediaInputPolicy) (adapters.AudioClassification, *ffmpeg.ProbeResult) {
	if a.ffprobe == nil || a.probeMedia == nil || strings.TrimSpace(mediaURL) == "" {
		return adapters.AudioClassificationUnknown, nil
	}
	ffprobePath, err := a.ffprobe.Resolve()
	if err != nil {
		safeErr := adapters.SanitizeMediaProbeError(mediaURL, err)
		slog.Warn("url audio classification skipped", "url", adapters.RedactMediaURLForLog(mediaURL), "err", safeErr)
		return adapters.AudioClassificationUnknown, nil
	}
	result, err := a.probeMedia(ctx, ffprobePath, ffmpeg.ProbeInputSpec{
		URL:     mediaURL,
		Headers: headers,
		Policy:  policy,
		Timeout: audioClassificationProbeTimeout,
	})
	if err != nil {
		safeErr := adapters.SanitizeMediaProbeError(mediaURL, err)
		slog.Warn("url audio classification failed", "url", adapters.RedactMediaURLForLog(mediaURL), "err", safeErr)
		return adapters.AudioClassificationUnknown, nil
	}
	return adapters.ClassifyProbeResult(result), result
}
```

Ensure `internal/adapters/url/play.go` imports `github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg`.

- [ ] **Step 5: Apply URL visualizer classification in request construction**

In `castURLWithStarter`, keep the existing base request construction. Track classification state:

```go
	var audioClass adapters.AudioClassification
	var audioProbe *ffmpeg.ProbeResult
	var resolvedDuration time.Duration
```

After yt-dlp resolve:

```go
		resolvedDuration = res.Duration
		audioClass, audioProbe = a.classifyResolvedURLMedia(ctx, res, mediaPolicy)
```

Before opening the direct HLS buffer:

```go
	} else if shouldBufferDirectM3U8(parsed, hlsBufferMode, bridge) {
		audioClass, audioProbe = a.classifyURLByProbe(ctx, rawURL, nil, core.MediaInputPolicy{})
		if audioClass != adapters.AudioClassificationAudioOnly {
			// existing HLS buffer code remains here
		}
```

For non-HLS direct URLs, classify after title is known and before `starter.startCore(req)`:

```go
	if !useYtdlp && hlsSession == nil && audioClass == adapters.AudioClassificationUnknown {
		audioClass, audioProbe = a.classifyURLByProbe(ctx, streamURL, headers, mediaPolicy)
	}
	if audioClass == adapters.AudioClassificationAudioOnly {
		duration := resolvedDuration
		if duration == 0 && audioProbe != nil && audioProbe.Duration > 0 {
			duration = time.Duration(audioProbe.Duration * float64(time.Second))
		}
		adapters.ApplyAudioOnlyVisualizer(&req, adapters.AudioOnlyVisualizerMetadata{
			Title:    title,
			Artist:   resolvedChannel,
			Duration: duration,
		})
	}
```

Keep video and unknown paths unchanged.

- [ ] **Step 6: Wire ffprobe in main**

In `cmd/mister-groovy-relay/main.go`, update URL adapter construction:

```go
	urlAdapter, err := urladapter.New(urladapter.AdapterConfig{
		Bridge:        sec.Bridge,
		Core:          coreMgr,
		YTDLPResolver: ytdlpResolver,
		FFprobe:       ffprobeResolver,
		EventLog:      elog,
	})
```

- [ ] **Step 7: Verify Task 4 tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url ./cmd/mister-groovy-relay -run "TestCastURL_YTDLPAudioOnlyStartsVisualizer|TestCastURL_YTDLPAmbiguousCodecsProbeUsesResolvedHeaders|TestCastURL_YTDLPUnknownCodecsFallBackToVideoWhenProbeUnavailable|TestCastURL_DirectAudioProbeStartsVisualizer|TestCastURL_DirectAudioOnlyHLSBypassesBuffer|Test.*URL"
```

Expected: PASS.

- [ ] **Step 8: Commit Task 4**

```bash
git add internal/adapters/url/adapter.go internal/adapters/url/play.go internal/adapters/url/play_test.go cmd/mister-groovy-relay/main.go
git commit -m "feat(url): route audio-only sources to visualizer"
```

## Task 5: URL Direct HLS Fallback Coverage

**Files:**
- Modify: `internal/adapters/url/play_test.go`
- Modify: `internal/adapters/url/play.go`

- [ ] **Step 1: Write failing or protective direct HLS fallback tests**

Add to `internal/adapters/url/play_test.go`:

```go
func TestCastURL_DirectHLSProbeFailureUsesBuffer(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	enableURLBridgeHLSBufferForTest(a)
	enableURLProbeForTest(a)
	a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return nil, errors.New("probe failed")
	}
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return &hlsbuffer.Session{
			PlaybackPath: "/tmp/url-buffered.m3u8",
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
		}, nil
	}

	if _, _, _, err := a.castURL(t.Context(), "https://media.example/live.m3u8", "direct"); err != nil {
		t.Fatalf("castURL: %v", err)
	}
	req := fc.snapshot()
	if req.StreamURL != "/tmp/url-buffered.m3u8" || req.MediaKind != core.MediaKindVideo || req.Visualizer.Enabled {
		t.Fatalf("request = %+v, want buffered video fallback", req)
	}
}

func TestCastURL_DirectHLSVideoProbeUsesBuffer(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	enableURLBridgeHLSBufferForTest(a)
	enableURLProbeForTest(a)
	a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 1280, AudioRate: 48000}, nil
	}
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return &hlsbuffer.Session{
			PlaybackPath: "/tmp/url-buffered.m3u8",
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
		}, nil
	}

	if _, _, _, err := a.castURL(t.Context(), "https://media.example/live.m3u8", "direct"); err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if fc.snapshot().StreamURL != "/tmp/url-buffered.m3u8" {
		t.Fatalf("StreamURL = %q, want buffered fallback", fc.snapshot().StreamURL)
	}
}
```

- [ ] **Step 2: Run fallback tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run "TestCastURL_DirectHLS.*(UsesBuffer|BypassesBuffer)"
```

Expected: PASS after Task 4. If either test fails, fix only the direct HLS classification branch so audio-only bypasses and video/unknown/error opens the buffer.

- [ ] **Step 3: Commit Task 5**

```bash
git add internal/adapters/url/play.go internal/adapters/url/play_test.go
git commit -m "test(url): cover direct hls audio fallback"
```

## Task 6: Streams Resolved yt-dlp Audio

**Files:**
- Modify: `internal/adapters/streams/adapter.go`
- Modify: `internal/adapters/streams/playback.go`
- Modify: `internal/adapters/streams/playback_test.go`
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Write failing Streams resolved-audio test**

Add to `internal/adapters/streams/playback_test.go`:

```go
type staticProbeResolver struct {
	path string
	err  error
}

func (s staticProbeResolver) Resolve() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.path, nil
}

func enableStreamsProbeForTest(a *Adapter) {
	a.ffprobe = staticProbeResolver{path: "ffprobe"}
}

func TestStartResolvedStreamAudioOnlyYTDLPStartsVisualizer(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{
		URL:      "https://media.example/song.opus",
		Title:    "Station ID",
		Channel:  "Uploader Name",
		VCodec:   "none",
		ACodec:   "opus",
		Duration: 45 * time.Second,
	}}

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	req := fc.lastReq
	if req.MediaKind != core.MediaKindMusic || !req.Visualizer.Enabled {
		t.Fatalf("request did not start visualizer: %+v", req)
	}
	if req.Visualizer.Metadata.Title != "Station ID" ||
		req.Visualizer.Metadata.Artist != "Metal" ||
		req.Visualizer.Metadata.Album != "MTV Rewind" ||
		req.Visualizer.Metadata.Duration != 45*time.Second {
		t.Fatalf("visualizer metadata = %+v", req.Visualizer.Metadata)
	}
	if !req.Capabilities.CanPause || req.Capabilities.CanSeek {
		t.Fatalf("resolved stream capabilities changed: %+v", req.Capabilities)
	}
}

func TestStartResolvedStreamAmbiguousYTDLPProbeUsesResolvedHeaders(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{
		URL:     "https://media.example/audio?sig=secret",
		Headers: map[string]string{"User-Agent": "yt-dlp", "Referer": "https://provider.example/"},
		Title:   "Header Track",
	}}
	enableStreamsProbeForTest(a)
	var captured ffmpeg.ProbeInputSpec
	a.probeMedia = func(_ context.Context, _ string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		captured = input
		return &ffmpeg.ProbeResult{AudioRate: 44100}, nil
	}

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if captured.URL != "https://media.example/audio?sig=secret" {
		t.Fatalf("probe URL = %q", captured.URL)
	}
	if captured.Headers["User-Agent"] != "yt-dlp" || captured.Headers["Referer"] != "https://provider.example/" {
		t.Fatalf("probe headers = %v", captured.Headers)
	}
	if fc.lastReq.MediaKind != core.MediaKindMusic {
		t.Fatalf("ambiguous probed audio should visualize: %+v", fc.lastReq)
	}
}
```

Ensure `internal/adapters/streams/playback_test.go` imports `errors` and `github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg`.

- [ ] **Step 2: Run Streams resolved-audio test to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestStartResolvedStreamAudioOnlyYTDLPStartsVisualizer|TestStartResolvedStreamAmbiguousYTDLPProbeUsesResolvedHeaders"
```

Expected: FAIL because resolved audio-only streams still build video requests.

- [ ] **Step 3: Add Streams ffprobe seam**

In `internal/adapters/streams/adapter.go`, add:

```go
type binaryResolver interface {
	Resolve() (string, error)
}

type mediaProbeFunc func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error)
```

Add fields:

```go
	ffprobe   binaryResolver
	probeMedia mediaProbeFunc
```

Add `FFprobe binaryResolver` to `AdapterConfig`, and initialize in `New`:

```go
		ffprobe:   cfg.FFprobe,
		probeMedia: ffmpeg.ProbeInput,
```

Add `internal/ffmpeg` to imports.

- [ ] **Step 4: Add Streams classification helpers**

In `internal/adapters/streams/playback.go`, add:

```go
func (a *Adapter) classifyResolvedStreamMedia(ctx context.Context, resolved *ytdlp.Resolution, policy core.MediaInputPolicy) (adapters.AudioClassification, *ffmpeg.ProbeResult) {
	if resolved == nil {
		return adapters.AudioClassificationUnknown, nil
	}
	if strings.TrimSpace(resolved.AudioURL) != "" {
		return adapters.AudioClassificationVideo, nil
	}
	if c := adapters.ClassifyCodecs(resolved.VCodec, resolved.ACodec); c != adapters.AudioClassificationUnknown {
		return c, nil
	}
	return a.classifyStreamURLByProbe(ctx, resolved.URL, resolved.Headers, policy)
}

func (a *Adapter) classifyStreamURLByProbe(ctx context.Context, mediaURL string, headers map[string]string, policy core.MediaInputPolicy) (adapters.AudioClassification, *ffmpeg.ProbeResult) {
	if a.ffprobe == nil || a.probeMedia == nil || strings.TrimSpace(mediaURL) == "" {
		return adapters.AudioClassificationUnknown, nil
	}
	ffprobePath, err := a.ffprobe.Resolve()
	if err != nil {
		safeErr := adapters.SanitizeMediaProbeError(mediaURL, err)
		slog.Warn("streams audio classification skipped", "url", adapters.RedactMediaURLForLog(mediaURL), "err", safeErr)
		return adapters.AudioClassificationUnknown, nil
	}
	result, err := a.probeMedia(ctx, ffprobePath, ffmpeg.ProbeInputSpec{
		URL:     mediaURL,
		Headers: headers,
		Policy:  policy,
		Timeout: audioClassificationProbeTimeout,
	})
	if err != nil {
		safeErr := adapters.SanitizeMediaProbeError(mediaURL, err)
		slog.Warn("streams audio classification failed", "url", adapters.RedactMediaURLForLog(mediaURL), "err", safeErr)
		return adapters.AudioClassificationUnknown, nil
	}
	return adapters.ClassifyProbeResult(result), result
}
```

Add `github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp` and `github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg` imports. Add a package-local timeout constant in `internal/adapters/streams/playback.go`:

```go
const audioClassificationProbeTimeout = 800 * time.Millisecond
```

- [ ] **Step 5: Apply visualizer to resolved stream requests**

After building the resolved-item `req` in `playCurrentWithStarter`, before `a.playbackMu.Lock()`:

```go
	audioClass, audioProbe := a.classifyResolvedStreamMedia(ctx, resolved, req.MediaInputPolicy)
	if audioClass == adapters.AudioClassificationAudioOnly {
		duration := resolved.Duration
		if duration == 0 && audioProbe != nil && audioProbe.Duration > 0 {
			duration = time.Duration(audioProbe.Duration * float64(time.Second))
		}
		adapters.ApplyAudioOnlyVisualizer(&req, adapters.AudioOnlyVisualizerMetadata{
			Title:    title,
			Artist:   q.ChannelName,
			Album:    q.ProviderName,
			Duration: duration,
		})
	}
```

Keep dual-stream `AudioURL` results classified as video.

- [ ] **Step 6: Wire ffprobe in main**

In `cmd/mister-groovy-relay/main.go`, update Streams construction:

```go
	streamsAdapter, err := streams.New(streams.AdapterConfig{
		Bridge:        sec.Bridge,
		Core:          coreMgr,
		YTDLPResolver: ytdlpResolver,
		FFprobe:       ffprobeResolver,
	})
```

- [ ] **Step 7: Verify Task 6 tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams ./cmd/mister-groovy-relay -run "TestStartResolvedStreamAudioOnlyYTDLPStartsVisualizer|TestStartResolvedStreamAmbiguousYTDLPProbeUsesResolvedHeaders|TestStartResolvedStreamStartsCoreSession|Test.*Streams"
```

Expected: PASS.

- [ ] **Step 8: Commit Task 6**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/playback.go internal/adapters/streams/playback_test.go cmd/mister-groovy-relay/main.go
git commit -m "feat(streams): visualize audio-only resolved items"
```

## Task 7: Streams Direct Media and HLS Audio

**Files:**
- Modify: `internal/adapters/streams/playback.go`
- Modify: `internal/adapters/streams/playback_test.go`

- [ ] **Step 1: Write failing direct Streams tests**

Add to `internal/adapters/streams/playback_test.go`:

```go
func TestStreamsDirectHLSAudioOnlyBypassesBuffer(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	enableBridgeHLSBufferForTest(a)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	enableStreamsProbeForTest(a)
	a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 44100, Duration: 30}, nil
	}
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		t.Fatal("audio-only direct HLS should bypass buffer")
		return nil, nil
	}

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	req := c.lastReq
	if req.StreamURL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" ||
		req.MediaKind != core.MediaKindMusic ||
		!req.Visualizer.Enabled {
		t.Fatalf("request = %+v, want direct audio visualizer", req)
	}
	if req.Capabilities.CanPause || req.Capabilities.CanSeek {
		t.Fatalf("direct stream capabilities changed: %+v", req.Capabilities)
	}
	if req.Visualizer.Metadata.Title != "East" ||
		req.Visualizer.Metadata.Artist != "East" ||
		req.Visualizer.Metadata.Album != "Toonami Aftermath" {
		t.Fatalf("visualizer metadata = %+v", req.Visualizer.Metadata)
	}
}

func TestStreamsDirectHLSVideoProbeKeepsBuffer(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	enableBridgeHLSBufferForTest(a)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	enableStreamsProbeForTest(a)
	a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, AudioRate: 48000}, nil
	}
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return &hlsbuffer.Session{
			PlaybackPath: "/tmp/streams-buffered.m3u8",
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
		}, nil
	}

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if c.lastReq.StreamURL != "/tmp/streams-buffered.m3u8" || c.lastReq.Visualizer.Enabled {
		t.Fatalf("request = %+v, want buffered video path", c.lastReq)
	}
}

func TestStreamsDirectHLSUnknownOrFailedProbeKeepsBuffer(t *testing.T) {
	cases := []struct {
		name   string
		result *ffmpeg.ProbeResult
		err    error
	}{
		{name: "unknown", result: &ffmpeg.ProbeResult{}},
		{name: "failure", err: errors.New("probe failed")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, c := newTestAdapterWithFakeCore(t)
			enableBridgeHLSBufferForTest(a)
			def := bundledToonamiAftermathDefinition()
			cat, err := buildDirectStreamsCatalog(def)
			if err != nil {
				t.Fatalf("buildDirectStreamsCatalog: %v", err)
			}
			a.replaceDefinitionsForTest([]ProviderDefinition{def})
			a.replaceCatalogsForTest([]ProviderCatalog{cat})
			enableStreamsProbeForTest(a)
			a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
				return tc.result, tc.err
			}
			a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
				return &hlsbuffer.Session{
					PlaybackPath: "/tmp/streams-buffered.m3u8",
					Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
					Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
				}, nil
			}

			if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
				t.Fatalf("StartResolvedStream: %v", err)
			}
			if c.lastReq.StreamURL != "/tmp/streams-buffered.m3u8" || c.lastReq.Visualizer.Enabled {
				t.Fatalf("request = %+v, want buffered video fallback", c.lastReq)
			}
		})
	}
}

func TestUserDirectAudioOnlyStartsVisualizerWithUserPolicy(t *testing.T) {
	a, c := installUserDirectAdapter(t)
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{
		"cdn.example.com":   {"93.184.216.34"},
		"media.example.com": {"93.184.216.35"},
	}}
	a.userRedirectDoer = stubDoer{resp: map[string]*http.Response{
		"https://cdn.example.com/live.m3u8": redirectResp("https://media.example.com/live.m3u8"),
	}}
	enableStreamsProbeForTest(a)
	var captured ffmpeg.ProbeInputSpec
	a.probeMedia = func(_ context.Context, _ string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		captured = input
		return &ffmpeg.ProbeResult{AudioRate: 44100}, nil
	}

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:cdn", ChannelID: "live"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	req := c.lastReq
	if req.MediaKind != core.MediaKindMusic || !req.Visualizer.Enabled {
		t.Fatalf("user direct audio should visualize: %+v", req)
	}
	wantPolicy := userDirectInputPolicy()
	if strings.Join(req.MediaInputPolicy.ProtocolWhitelist, ",") != strings.Join(wantPolicy.ProtocolWhitelist, ",") ||
		req.MediaInputPolicy.RWTimeout != wantPolicy.RWTimeout {
		t.Fatalf("user direct policy not preserved: %+v", req.MediaInputPolicy)
	}
	if captured.URL != "https://media.example.com/live.m3u8" {
		t.Fatalf("probe URL = %q, want prevalidated redirect target", captured.URL)
	}
	if strings.Join(captured.Policy.ProtocolWhitelist, ",") != strings.Join(wantPolicy.ProtocolWhitelist, ",") ||
		captured.Policy.RWTimeout != wantPolicy.RWTimeout {
		t.Fatalf("probe policy = %+v, want user direct policy", captured.Policy)
	}
}

func TestStreamsAudioOnlyDirectStartFailureAdvancesQueue(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	c.startErrs = []error{errors.New("first failed"), nil}
	a.replaceDefinitionsForTest([]ProviderDefinition{{ID: "direct-audio", Type: directStreamsProviderType, DisplayName: "Direct Audio"}})
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "direct-audio",
		Name:       "Direct Audio",
		Channels: []Channel{{
			ID:       "mix",
			Name:     "Mix",
			PlayMode: PlaySequential,
			Items: []StreamItem{
				{ID: "first", Title: "First", URL: "https://media.example/first.mp3", SourceID: "first", Direct: true},
				{ID: "second", Title: "Second", URL: "https://media.example/second.mp3", SourceID: "second", Direct: true},
			},
		}},
	}})
	enableStreamsProbeForTest(a)
	a.probeMedia = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 44100}, nil
	}

	started, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "direct-audio", ChannelID: "mix"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if c.startCalls != 2 {
		t.Fatalf("StartSession calls = %d, want retry on next direct item", c.startCalls)
	}
	if started.ItemID != "second" || c.lastReq.Title != "Second" || c.lastReq.MediaKind != core.MediaKindMusic {
		t.Fatalf("started=%+v lastReq=%+v, want second audio item visualized", started, c.lastReq)
	}
}
```

- [ ] **Step 2: Run direct Streams tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestStreamsDirectHLSAudioOnlyBypassesBuffer|TestStreamsDirectHLSVideoProbeKeepsBuffer|TestStreamsDirectHLSUnknownOrFailedProbeKeepsBuffer|TestUserDirectAudioOnlyStartsVisualizerWithUserPolicy|TestStreamsAudioOnlyDirectStartFailureAdvancesQueue"
```

Expected: FAIL because the direct branch does not classify audio-only media before buffer/core start.

- [ ] **Step 3: Implement direct branch classification**

In the direct branch of `playCurrentWithStarter`, after user-provider prevalidation and before `if a.shouldBufferDirectHLS(q, item)`:

```go
		audioClass, audioProbe := a.classifyStreamURLByProbe(resolveCtx, playbackURL, nil, mediaPolicy)
		shouldBuffer := a.shouldBufferDirectHLS(q, item)
		if shouldBuffer && audioClass != adapters.AudioClassificationAudioOnly {
			// existing HLS buffer open block
		}
```

When building the direct `req`, after the HLS buffer block and before starting core:

```go
		if audioClass == adapters.AudioClassificationAudioOnly {
			var duration time.Duration
			if audioProbe != nil && audioProbe.Duration > 0 {
				duration = time.Duration(audioProbe.Duration * float64(time.Second))
			}
			adapters.ApplyAudioOnlyVisualizer(&req, adapters.AudioOnlyVisualizerMetadata{
				Title:    streamSessionTitle(item, ""),
				Artist:   q.ChannelName,
				Album:    q.ProviderName,
				Duration: duration,
			})
		}
```

Important: if `shouldBuffer` is true and classification is video, unknown, or failed, keep the existing HLS buffer path unchanged. If classification is audio-only, skip opening the buffer.

- [ ] **Step 4: Verify direct Streams GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestStreamsDirectHLS|TestUserDirect_|TestStartResolvedDirectStreamSkipsResolverAndSetsPolicy|TestStreamsAudioOnlyDirectStartFailureAdvancesQueue"
```

Expected: PASS.

- [ ] **Step 5: Commit Task 7**

```bash
git add internal/adapters/streams/playback.go internal/adapters/streams/playback_test.go
git commit -m "feat(streams): visualize direct audio-only items"
```

## Task 8: Adapter Construction and Compatibility Sweep

**Files:**
- Modify only compile-failing tests or constructors from previous tasks.

- [ ] **Step 1: Run adapter package compile/test sweep**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/... ./cmd/mister-groovy-relay
```

Expected: PASS. Likely failures, if any, will be test constructors that need the new optional `FFprobe` field or package imports.

- [ ] **Step 2: Fix any constructor or import fallout**

For tests that need explicit ffprobe behavior, add a small fake resolver in the test package:

```go
type staticBinaryResolver struct {
	path string
	err  error
}

func (s staticBinaryResolver) Resolve() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.path, nil
}
```

For tests that should avoid probing, leave `FFprobe` nil and assert video fallback.

- [ ] **Step 3: Re-run adapter package sweep**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/... ./cmd/mister-groovy-relay
```

Expected: PASS.

- [ ] **Step 4: Commit Task 8 if fixes were needed**

If files changed:

```bash
git add internal/adapters cmd/mister-groovy-relay
git commit -m "test(adapters): update audio visualizer wiring coverage"
```

If no files changed, record in the execution notes that Task 8 required no commit.

## Task 9: Full Verification

**Files:**
- No planned source edits.

- [ ] **Step 1: Run gofmt**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/ffmpeg/input.go internal/ffmpeg/probe.go internal/ffmpeg/probe_test.go internal/core/manager.go internal/core/manager_test.go internal/adapters/audio_visualizer.go internal/adapters/audio_visualizer_test.go internal/adapters/url/ytdlp/resolver.go internal/adapters/url/ytdlp/resolver_test.go internal/adapters/url/adapter.go internal/adapters/url/play.go internal/adapters/url/play_test.go internal/adapters/streams/adapter.go internal/adapters/streams/playback.go internal/adapters/streams/playback_test.go internal/adapters/localfiles/cast.go internal/adapters/localfiles/cast_test.go cmd/mister-groovy-relay/main.go
```

Expected: command exits 0.

- [ ] **Step 2: Run focused tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ffmpeg ./internal/core ./internal/adapters ./internal/adapters/localfiles ./internal/adapters/url/ytdlp ./internal/adapters/url ./internal/adapters/streams ./cmd/mister-groovy-relay
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./...
```

Expected: PASS.

- [ ] **Step 4: Run race suite before merge**

Run:

```bash
cmd.exe /c set CGO_ENABLED=1&&C:\Users\Jake\sdk\go\bin\go.exe test -race ./...
```

Expected: PASS. If this fails for an existing unrelated race, capture the package/test name and decide whether to fix or defer before merge.

- [ ] **Step 5: Final code review request**

Use `superpowers:requesting-code-review` with:

- Base SHA: commit before Task 1.
- Head SHA: final implementation commit.
- Requirements: the reviewed design spec and this implementation plan.
- Focus: media classification fallback safety, SSRF/policy preservation, HLS buffer bypass/fallback, and visualizer metadata.

## Self-Review Checklist

- Spec coverage: Tasks 1, 4, 6, and 7 cover URL, yt-dlp, direct URL, HLS bypass, Streams resolved/direct/user-provider, and header-dependent probes. Task 2 covers the shared helper and Local Files reuse.
- Fallback behavior: URL and Streams tests explicitly cover unknown/probe failure preserving video/HLS paths.
- Security posture: user-provider direct prevalidation remains before probe; policy and blocked-header filtering are preserved for ffprobe and FFmpeg.
- No artwork expansion: helper always leaves `ArtworkPath` empty.
- Review gate: stop here until the user approves running this plan.
