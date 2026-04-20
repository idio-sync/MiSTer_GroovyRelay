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
	if !strings.Contains(extra, "codec=h264") {
		t.Error("profile extra should force H.264")
	}
}

func TestClientCapabilities_AdvertisesH264(t *testing.T) {
	caps := BuildClientCapabilities()
	if !strings.Contains(caps, "h264") {
		t.Errorf("client capabilities should mention h264: %s", caps)
	}
}
