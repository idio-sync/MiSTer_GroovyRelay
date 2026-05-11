package jellyfin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestDeviceProfile_StructureAndConditions(t *testing.T) {
	p := BuildDeviceProfile(4000, mustPreset(t, "NTSC_480i"))
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	wantContains := []string{
		`"Name": "GroovyRelay"`,
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

func TestAudioDeviceProfile_UsesMP3TranscodeAndOmitsVideoCodec(t *testing.T) {
	p := BuildAudioDeviceProfile(4000)
	if len(p.TranscodingProfiles) != 1 {
		t.Fatalf("TranscodingProfiles len = %d, want 1", len(p.TranscodingProfiles))
	}
	profile := p.TranscodingProfiles[0]
	if profile.Type != "Audio" {
		t.Errorf("Type = %q, want Audio", profile.Type)
	}
	if profile.Container != "mp3" {
		t.Errorf("Container = %q, want mp3", profile.Container)
	}
	if profile.AudioCodec != "mp3" {
		t.Errorf("AudioCodec = %q, want mp3", profile.AudioCodec)
	}
	if profile.Protocol != "http" {
		t.Errorf("Protocol = %q, want http", profile.Protocol)
	}
	if profile.Context != "Streaming" {
		t.Errorf("Context = %q, want Streaming", profile.Context)
	}
	if profile.MaxAudioChannels != "2" {
		t.Errorf("MaxAudioChannels = %q, want 2", profile.MaxAudioChannels)
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "VideoCodec") {
		t.Fatalf("audio profile JSON contains VideoCodec: %s", got)
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
		p := BuildDeviceProfile(c.kbps, mustPreset(t, "NTSC_480i"))
		if p.MaxStreamingBitrate != c.bps {
			t.Errorf("BuildDeviceProfile(%d).MaxStreamingBitrate = %d, want %d", c.kbps, p.MaxStreamingBitrate, c.bps)
		}
	}
}

func TestDeviceProfile_NoDirectPlay(t *testing.T) {
	p := BuildDeviceProfile(4000, mustPreset(t, "NTSC_480i"))
	if p.DirectPlayProfiles == nil {
		t.Fatal("DirectPlayProfiles is nil, want empty array")
	}
	if len(p.DirectPlayProfiles) != 0 {
		t.Errorf("DirectPlayProfiles = %v, want empty (forces transcode)", p.DirectPlayProfiles)
	}
}

func TestDeviceProfile_EmptyCollectionsMarshalAsArrays(t *testing.T) {
	data, err := json.Marshal(BuildDeviceProfile(4000, mustPreset(t, "NTSC_480i")))
	if err != nil {
		t.Fatal(err)
	}

	got := string(data)
	for _, want := range []string{
		`"DirectPlayProfiles":[]`,
		`"ContainerProfiles":[]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("DeviceProfile JSON missing %s\nfull output:\n%s", want, got)
		}
	}
}

func TestDeviceProfile_UsesModelineSourceShape(t *testing.T) {
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
			p := BuildDeviceProfile(4000, mustPreset(t, c.preset))
			conds := p.CodecProfiles[0].Conditions
			want := map[string]string{
				"Width":          "720",
				"Height":         c.height,
				"VideoFramerate": c.fps,
			}
			for _, cond := range conds {
				if got, ok := want[cond.Property]; ok && cond.Value != got {
					t.Errorf("%s = %q, want %q", cond.Property, cond.Value, got)
				}
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
