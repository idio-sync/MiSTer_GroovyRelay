package plex

import (
	"strings"
	"testing"
)

func TestProfileExtra_Forces480pH264(t *testing.T) {
	extra := BuildProfileExtra()
	if !strings.Contains(extra, "video-resolution-match=match(videoResolution,\"480\")") &&
		!strings.Contains(extra, "resolution=720x480") &&
		!strings.Contains(extra, "value=480") {
		t.Error("profile extra should constrain resolution to 480")
	}
	if !strings.Contains(extra, "videoCodec=h264") {
		t.Error("profile extra should force H.264")
	}
	if !strings.Contains(extra, "protocol=http") || !strings.Contains(extra, "container=mkv") {
		t.Errorf("profile extra should force progressive http/mkv transport: %s", extra)
	}
	if strings.Contains(extra, "protocol=hls") {
		t.Errorf("profile extra should not advertise HLS: %s", extra)
	}
	if !strings.Contains(extra, "audioCodec=aac") {
		t.Errorf("profile extra should force AAC audio: %s", extra)
	}
}

func TestClientCapabilities_AdvertisesH264(t *testing.T) {
	caps := BuildClientCapabilities()
	if !strings.Contains(caps, "h264") {
		t.Errorf("client capabilities should mention h264: %s", caps)
	}
	if !strings.Contains(caps, "http-streaming-video") {
		t.Errorf("client capabilities should advertise progressive http-streaming-video: %s", caps)
	}
	if strings.Contains(caps, "http-hls") || strings.Contains(caps, "http-live-streaming") {
		t.Errorf("client capabilities should not advertise HLS transports: %s", caps)
	}
	if !strings.Contains(caps, "audioDecoders=aac{channels:2}") {
		t.Errorf("client capabilities should advertise stereo AAC: %s", caps)
	}
}
