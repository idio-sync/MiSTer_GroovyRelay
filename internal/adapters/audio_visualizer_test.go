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
		{name: "video wins", vcodec: "avc1", acodec: "mp4a", want: Video},
		{name: "audio only", vcodec: "none", acodec: "opus", want: AudioOnly},
		{name: "both none unknown", vcodec: "none", acodec: "none", want: Unknown},
		{name: "missing video unknown", vcodec: "", acodec: "opus", want: Unknown},
		{name: "case whitespace audio only", vcodec: " NONE ", acodec: " OPUS ", want: AudioOnly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyCodecs(tt.vcodec, tt.acodec); got != tt.want {
				t.Fatalf("ClassifyCodecs(%q, %q) = %v, want %v", tt.vcodec, tt.acodec, got, tt.want)
			}
		})
	}
}

func TestClassifyProbeResult(t *testing.T) {
	tests := []struct {
		name   string
		result *ffmpeg.ProbeResult
		want   AudioClassification
	}{
		{name: "audio rate only", result: &ffmpeg.ProbeResult{AudioRate: 44100}, want: AudioOnly},
		{name: "width and audio rate", result: &ffmpeg.ProbeResult{Width: 1920, AudioRate: 48000}, want: Video},
		{name: "attached picture marker with audio", result: &ffmpeg.ProbeResult{AudioRate: 44100, AttachedPicture: true}, want: AudioOnly},
		{name: "video codec with whitespace", result: &ffmpeg.ProbeResult{VideoCodec: " h264 "}, want: Video},
		{name: "audio codec with whitespace", result: &ffmpeg.ProbeResult{AudioCodec: " opus "}, want: AudioOnly},
		{name: "audio codec none is absent", result: &ffmpeg.ProbeResult{AudioCodec: "none"}, want: Unknown},
		{name: "codec none values are absent", result: &ffmpeg.ProbeResult{VideoCodec: " none ", AudioCodec: " none "}, want: Unknown},
		{name: "nil", result: nil, want: Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyProbeResult(tt.result); got != tt.want {
				t.Fatalf("ClassifyProbeResult(%+v) = %v, want %v", tt.result, got, tt.want)
			}
		})
	}
}

func TestApplyAudioOnlyVisualizer(t *testing.T) {
	req := core.SessionRequest{MediaKind: core.MediaKindVideo}
	duration := 3*time.Minute + 2*time.Second

	ApplyAudioOnlyVisualizer(&req, AudioOnlyVisualizerMetadata{
		Title:    "Song",
		Artist:   "Artist",
		Album:    "Album",
		Duration: duration,
	})

	if req.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want %q", req.MediaKind, core.MediaKindMusic)
	}
	if !req.Visualizer.Enabled || req.Visualizer.Mode != core.VisualizerModeRetroAnalyzer {
		t.Fatalf("Visualizer = %+v, want retro analyzer enabled", req.Visualizer)
	}
	meta := req.Visualizer.Metadata
	if meta.Title != "Song" || meta.Artist != "Artist" || meta.Album != "Album" || meta.Duration != duration || meta.ArtworkPath != "" {
		t.Fatalf("Visualizer metadata = %+v, want title/artist/album/duration and empty artwork", meta)
	}
}

func TestRedactMediaURLForLogStripsSecrets(t *testing.T) {
	raw := "https://user:secret@cdn.example/audio.m4a?sig=supersecret#fragment"

	got := RedactMediaURLForLog(raw)

	if got != "https://cdn.example/audio.m4a" {
		t.Fatalf("RedactMediaURLForLog() = %q, want redacted URL", got)
	}
	for _, leak := range []string{"user", "secret", "sig", "supersecret", "fragment"} {
		if strings.Contains(got, leak) {
			t.Fatalf("RedactMediaURLForLog() leaked %q in %q", leak, got)
		}
	}
}

func TestSanitizeMediaProbeErrorReplacesRawURL(t *testing.T) {
	raw := "https://user:secret@cdn.example/audio.m4a?sig=supersecret#fragment"
	err := errors.New("ffprobe failed for " + raw)

	got := SanitizeMediaProbeError(raw, err)

	if !strings.Contains(got, "https://cdn.example/audio.m4a") {
		t.Fatalf("SanitizeMediaProbeError() = %q, want redacted URL", got)
	}
	for _, leak := range []string{"user", "secret", "sig", "supersecret", "fragment"} {
		if strings.Contains(got, leak) {
			t.Fatalf("SanitizeMediaProbeError() leaked %q in %q", leak, got)
		}
	}
}

func TestSanitizeMediaProbeErrorRedactsTrimmedVariantURL(t *testing.T) {
	mediaURL := "  https://user:secret@cdn.example/audio.m4a?sig=supersecret#fragment  "
	err := errors.New("ffprobe failed for https://user:secret@cdn.example/audio.m4a?sig=supersecret#fragment")

	got := SanitizeMediaProbeError(mediaURL, err)

	assertSanitizedProbeError(t, got, "https://cdn.example/audio.m4a")
}

func TestSanitizeMediaProbeErrorRedactsURLLikeSubstrings(t *testing.T) {
	mediaURL := "https://cdn.example/different.m4a"
	err := errors.New("ffprobe failed for https://user:secret@cdn.example/audio.m4a?sig=supersecret#fragment")

	got := SanitizeMediaProbeError(mediaURL, err)

	assertSanitizedProbeError(t, got, "https://cdn.example/audio.m4a")
}

func assertSanitizedProbeError(t *testing.T, got, safeURL string) {
	t.Helper()

	if !strings.Contains(got, safeURL) {
		t.Fatalf("SanitizeMediaProbeError() = %q, want redacted URL %q", got, safeURL)
	}
	for _, leak := range []string{"user", "secret", "sig=", "supersecret", "fragment"} {
		if strings.Contains(got, leak) {
			t.Fatalf("SanitizeMediaProbeError() leaked %q in %q", leak, got)
		}
	}
}
