package jellyfin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeviceProfile_StructureAndConditions(t *testing.T) {
	p := BuildDeviceProfile(4000)
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	wantContains := []string{
		`"Name": "MiSTer_GroovyRelay"`,
		`"MaxStreamingBitrate": 4000000`,
		`"Container": "ts"`,
		`"VideoCodec": "h264"`,
		`"AudioCodec": "aac"`,
		`"Protocol": "http"`,
		`"Context": "Streaming"`,
		`"Property": "Width"`,
		`"Value": "720"`,
		`"Property": "Height"`,
		`"Value": "480"`,
		`"Property": "VideoFramerate"`,
		`"Value": "30"`,
		`"Format": "srt"`,
		`"Format": "ass"`,
		`"Format": "pgs"`,
		`"Method": "Encode"`,
	}
	for _, s := range wantContains {
		if !strings.Contains(got, s) {
			t.Errorf("DeviceProfile JSON missing %q\nfull output:\n%s", s, got)
		}
	}
}

func TestDeviceProfile_BitrateScaling(t *testing.T) {
	cases := []struct {
		kbps int
		bps  int
	}{
		{200, 200_000},
		{4000, 4_000_000},
		{50000, 50_000_000},
	}
	for _, c := range cases {
		p := BuildDeviceProfile(c.kbps)
		if p.MaxStreamingBitrate != c.bps {
			t.Errorf("BuildDeviceProfile(%d).MaxStreamingBitrate = %d, want %d", c.kbps, p.MaxStreamingBitrate, c.bps)
		}
	}
}

func TestDeviceProfile_NoDirectPlay(t *testing.T) {
	p := BuildDeviceProfile(4000)
	if len(p.DirectPlayProfiles) != 0 {
		t.Errorf("DirectPlayProfiles = %v, want empty (forces transcode)", p.DirectPlayProfiles)
	}
}
