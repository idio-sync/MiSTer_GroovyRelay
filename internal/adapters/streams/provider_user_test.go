package streams

import (
	"encoding/json"
	"testing"
)

func TestProviderDefinition_NewFieldsRoundTrip(t *testing.T) {
	raw := `{
		"id": "user:f1-tv",
		"type": "user",
		"display_name": "F1 TV",
		"badge_color": "amber",
		"groups": [{"id": "races", "name": "Races", "order": 0}],
		"channels": [{"id": "live", "name": "Live", "kind": "single", "url": "https://twitch.tv/formula1", "order": 0}]
	}`
	var def ProviderDefinition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if def.BadgeColor != "amber" {
		t.Errorf("BadgeColor = %q, want amber", def.BadgeColor)
	}
	if len(def.Channels) != 1 || def.Channels[0].Kind != "single" {
		t.Errorf("Channels[0].Kind = %q, want single", def.Channels[0].Kind)
	}

	out, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ProviderDefinition
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.BadgeColor != "amber" || back.Channels[0].Kind != "single" {
		t.Errorf("round-trip lost new fields: %+v", back)
	}
}

func TestDetectChannelKind(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://cdn.example.com/live/stream.m3u8", kindDirect},
		{"https://cdn.example.com/live/stream.m3u", kindDirect},
		{"https://cdn.example.com/live/manifest.mpd", kindDirect},
		{"https://www.youtube.com/playlist?list=PLabc123", kindPlaylist},
		{"https://www.youtube.com/watch?v=abc&list=PLxyz", kindPlaylist},
		{"https://www.twitch.tv/formula1", kindSingle},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", kindSingle},
		{"https://vimeo.com/12345", kindSingle},
		{"   ", kindSingle},
	}
	for _, c := range cases {
		if got := detectChannelKind(c.url); got != c.want {
			t.Errorf("detectChannelKind(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
