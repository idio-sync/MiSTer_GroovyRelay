package plex

import (
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestProfileExtra_Forces480pH264(t *testing.T) {
	extra := BuildProfileExtra(mustPreset(t, "NTSC_480i"))
	if !strings.Contains(extra, "video-resolution-match=match(videoResolution,\"480\")") &&
		!strings.Contains(extra, "resolution=720x480") &&
		!strings.Contains(extra, "value=480") {
		t.Error("profile extra should constrain resolution to 480")
	}
	if !strings.Contains(extra, "videoCodec=h264") {
		t.Error("profile extra should force H.264")
	}
	if !strings.Contains(extra, "protocol=http") || !strings.Contains(extra, "container=mpegts") {
		t.Errorf("profile extra should force progressive http/mpegts transport: %s", extra)
	}
	if strings.Contains(extra, "protocol=hls") {
		t.Errorf("profile extra should not advertise HLS: %s", extra)
	}
	if strings.Contains(extra, "container=mkv") {
		t.Errorf("profile extra should not advertise MKV (uses MPEG-TS for streaming resync): %s", extra)
	}
	if !strings.Contains(extra, "audioCodec=aac") {
		t.Errorf("profile extra should force AAC audio: %s", extra)
	}
}

func TestClientCapabilities_AdvertisesH264(t *testing.T) {
	caps := BuildClientCapabilities(mustPreset(t, "NTSC_480i"))
	if !strings.Contains(caps, "h264") {
		t.Errorf("client capabilities should mention h264: %s", caps)
	}
	if !strings.Contains(caps, "http-streaming-video") {
		t.Errorf("client capabilities should advertise progressive http-streaming-video: %s", caps)
	}
	if !strings.Contains(caps, "http-mp2t-video") {
		t.Errorf("client capabilities should advertise progressive MPEG-TS: %s", caps)
	}
	if strings.Contains(caps, "http-hls") || strings.Contains(caps, "http-live-streaming") {
		t.Errorf("client capabilities should not advertise HLS transports: %s", caps)
	}
	if strings.Contains(caps, "http-mkv-video") {
		t.Errorf("client capabilities should not advertise MKV (uses MPEG-TS for streaming resync): %s", caps)
	}
	if !strings.Contains(caps, "audioDecoders=aac{channels:2}") {
		t.Errorf("client capabilities should advertise stereo AAC: %s", caps)
	}
}

func TestProfileExtra_UsesModelineSourceCaps(t *testing.T) {
	cases := []struct {
		preset string
		height string
		fps    string
	}{
		{"NTSC_480i", "480", "30"},
		{"NTSC_240p", "240", "60"},
		{"PAL_576i", "576", "25"},
		{"PAL_288p", "288", "50"},
	}
	for _, c := range cases {
		t.Run(c.preset, func(t *testing.T) {
			extra := BuildProfileExtra(mustPreset(t, c.preset))
			for _, want := range []string{
				"name=video.width&value=720&isRequired=true",
				"name=video.height&value=" + c.height + "&isRequired=true",
				"name=video.framerate&value=" + c.fps + "&isRequired=true",
			} {
				if !strings.Contains(extra, want) {
					t.Errorf("profile extra missing %q: %s", want, extra)
				}
			}
		})
	}
}

func TestClientCapabilities_UsesPlexTranscodeTargetSize(t *testing.T) {
	cases := []struct {
		preset     string
		resolution string
	}{
		{"NTSC_480i", "720x480"},
		{"NTSC_240p", "320x240"},
		{"PAL_576i", "720x576"},
		{"PAL_288p", "384x288"},
	}
	for _, c := range cases {
		t.Run(c.preset, func(t *testing.T) {
			caps := BuildClientCapabilities(mustPreset(t, c.preset))
			want := "resolution:" + c.resolution
			if !strings.Contains(caps, want) {
				t.Errorf("client capabilities missing %q: %s", want, caps)
			}
		})
	}
}

func mustPreset(t *testing.T, name string) core.ModelinePreset {
	t.Helper()
	preset, err := core.ResolvePreset(name)
	if err != nil {
		t.Fatal(err)
	}
	return preset
}
