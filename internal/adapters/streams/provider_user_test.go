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

func TestBadgeColorValidationAndLoadNormalization(t *testing.T) {
	cases := map[string]string{
		"amber":  "amber",
		"AMBER":  "amber",
		" teal ": "teal",
	}
	for in, want := range cases {
		if got, err := validateBadgeColor(in); err != nil || got != want {
			t.Errorf("validateBadgeColor(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
	bad := []string{"", "fuchsia", "#ff0000"}
	for _, in := range bad {
		if _, err := validateBadgeColor(in); err == nil {
			t.Errorf("validateBadgeColor(%q) expected error, got nil", in)
		}
		if got := normalizeBadgeColorForLoad(in); got != defaultBadgeColor {
			t.Errorf("normalizeBadgeColorForLoad(%q) = %q, want %q", in, got, defaultBadgeColor)
		}
	}
}

func TestValidateGlyph(t *testing.T) {
	ok := []string{"F1", "CN", "TOM", "ABCD"}
	for _, g := range ok {
		if err := validateGlyph(g); err != nil {
			t.Errorf("validateGlyph(%q) unexpected error: %v", g, err)
		}
	}
	bad := []string{"", "   ", "X", "TOOLONG"}
	for _, g := range bad {
		if err := validateGlyph(g); err == nil {
			t.Errorf("validateGlyph(%q) expected error, got nil", g)
		}
	}
}

func TestValidateUserManifestIDs(t *testing.T) {
	if err := validateUserProviderID("user:f1-tv"); err != nil {
		t.Fatalf("valid provider ID rejected: %v", err)
	}
	for _, id := range []string{"f1-tv", "user:", "user:Bad", "user:bad/id", "user:adhoc"} {
		if err := validateUserProviderID(id); err == nil {
			t.Errorf("validateUserProviderID(%q) expected error, got nil", id)
		}
	}
	if err := validateUserManifestID("channel id", "live", true); err != nil {
		t.Fatalf("valid channel ID rejected: %v", err)
	}
	for _, id := range []string{"", "Bad", "bad/id", "adhoc"} {
		if err := validateUserManifestID("channel id", id, true); err == nil {
			t.Errorf("validateUserManifestID(%q) expected error, got nil", id)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"F1 TV":            "f1-tv",
		"Cartoon Network!": "cartoon-network",
		"  Lo-Fi  24/7  ":  "lo-fi-24-7",
		"???":              "provider",
	}
	for in, want := range cases {
		if got := slugify(in, "provider"); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueSlug(t *testing.T) {
	taken := map[string]bool{"f1-tv": true, "f1-tv-2": true}
	got := uniqueSlug("f1-tv", func(s string) bool { return taken[s] })
	if got != "f1-tv-3" {
		t.Errorf("uniqueSlug = %q, want f1-tv-3", got)
	}
	got = uniqueSlug("new", func(s string) bool { return taken[s] })
	if got != "new" {
		t.Errorf("uniqueSlug = %q, want new", got)
	}
}

func TestNewUserProviderID(t *testing.T) {
	taken := map[string]bool{"user:f1-tv": true}
	got := newUserProviderID("F1 TV", func(id string) bool { return taken[id] })
	if got != "user:f1-tv-2" {
		t.Errorf("newUserProviderID = %q, want user:f1-tv-2", got)
	}
}

func TestNewChannelID(t *testing.T) {
	// Collision appends a numeric suffix.
	taken := map[string]bool{"live": true}
	got := newChannelID("Live", func(id string) bool { return taken[id] })
	if got != "live-2" {
		t.Errorf("newChannelID collision = %q, want live-2", got)
	}
	// Unsluggable name falls back to "channel".
	got = newChannelID("???", func(string) bool { return false })
	if got != "channel" {
		t.Errorf("newChannelID unsluggable = %q, want channel", got)
	}
}

func TestValidateGlyph_MultiByteRunes(t *testing.T) {
	// Exactly 2 runes, each multi-byte: must be accepted (rune-counted, not byte-counted).
	g := "日本" // 2 CJK runes, 6 bytes
	if err := validateGlyph(g); err != nil {
		t.Errorf("validateGlyph(%q) unexpected error: %v", g, err)
	}
}

func TestValidateUserManifestID_RejectAdhocFalse(t *testing.T) {
	// rejectAdhoc=false means the reserved word "adhoc" must be accepted.
	if err := validateUserManifestID("channel id", "adhoc", false); err != nil {
		t.Errorf("validateUserManifestID(adhoc, rejectAdhoc=false) unexpected error: %v", err)
	}
}
