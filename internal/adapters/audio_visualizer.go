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
	Unknown AudioClassification = iota
	Video
	AudioOnly
)

type AudioOnlyVisualizerMetadata struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
}

func ClassifyCodecs(vcodec, acodec string) AudioClassification {
	videoCodec := strings.ToLower(strings.TrimSpace(vcodec))
	audioCodec := strings.ToLower(strings.TrimSpace(acodec))
	if videoCodec != "" && videoCodec != "none" {
		return Video
	}
	if videoCodec == "none" && audioCodec != "" && audioCodec != "none" {
		return AudioOnly
	}
	return Unknown
}

func ClassifyProbeResult(result *ffmpeg.ProbeResult) AudioClassification {
	if result == nil {
		return Unknown
	}
	if result.Width > 0 || strings.TrimSpace(result.VideoCodec) != "" {
		return Video
	}
	if result.AudioRate > 0 || strings.TrimSpace(result.AudioCodec) != "" {
		return AudioOnly
	}
	return Unknown
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
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "<unparseable url>"
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil {
		return "<unparseable url>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func SanitizeMediaProbeError(mediaURL string, err error) string {
	if err == nil {
		return ""
	}
	if mediaURL == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), mediaURL, RedactMediaURLForLog(mediaURL))
}
