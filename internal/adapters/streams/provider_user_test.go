package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
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

// TestSlugify_Cap verifies that slugify caps its output at 60 bytes and that
// the resulting ID satisfies manifestIDPattern (no trailing hyphen, ≤64
// total chars as required by the manifest ID format).
func TestSlugify_Cap(t *testing.T) {
	// 70 alphanumeric characters — no separators so no mid-cap hyphen risk.
	longName := "abcdefghijklmnopqrstuvwxyz01234567890abcdefghijklmnopqrstuvwxyz01234"
	got := slugify(longName, "provider")
	if len(got) > 60 {
		t.Errorf("slugify capped length = %d, want ≤60; got %q", len(got), got)
	}
	if got == "" {
		t.Fatal("slugify returned empty string for valid input")
	}
	if got[len(got)-1] == '-' {
		t.Errorf("slugify result has trailing hyphen: %q", got)
	}

	// The generated provider ID (user:<slug>) must pass manifest validation.
	fullID := newUserProviderID(longName, func(string) bool { return false })
	if err := validateUserProviderID(fullID); err != nil {
		t.Errorf("newUserProviderID for long name failed validation: %v (id=%q)", err, fullID)
	}
}

// TestSlugify_Cap_HyphenBoundary verifies that a 60-char slug that would end
// in a hyphen at the cap boundary has the trailing hyphen stripped.
func TestSlugify_Cap_HyphenBoundary(t *testing.T) {
	// Build a 61-char name where character 60 (0-indexed) is a space (→hyphen).
	// "aaaa...aaa aaa...aaa": 59 'a's + space + more 'a's = slug "aaa...aaa-aaa..."
	// Simpler: repeat "ab " so position 60 falls on a hyphen after truncation.
	name := strings.Repeat("a", 59) + " extra"
	got := slugify(name, "provider")
	if len(got) > 60 {
		t.Errorf("length after cap = %d, want ≤60", len(got))
	}
	if got[len(got)-1] == '-' {
		t.Errorf("trailing hyphen not stripped: %q", got)
	}
}

func userCatalogTestDef() ProviderDefinition {
	return ProviderDefinition{
		ID:          "user:mix",
		Type:        userProviderType,
		DisplayName: "Mix",
		BadgeLabel:  "MX",
		BadgeColor:  "teal",
		Groups:      []GroupDefinition{{ID: "g1", Name: "Group One"}},
		Channels: []ChannelDefinition{
			{ID: "live", Name: "Live", GroupID: "g1", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"},
			{ID: "vid", Name: "Single", GroupID: "g1", Kind: kindSingle, URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
			{ID: "list", Name: "Playlist", GroupID: "g1", Kind: kindPlaylist, URL: "https://www.youtube.com/playlist?list=PL123"},
		},
	}
}

func TestBuildUserCatalog_PlaylistEnumeratesDirectAndSingleUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	playlistURL := "https://www.youtube.com/playlist?list=PL123"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	enum := userPlaylistEnumerator{resolver: fr, cacheDir: dir, cfg: DefaultConfig()}

	cat, err := buildUserCatalog(context.Background(), userCatalogTestDef(), enum)
	if err != nil {
		t.Fatalf("buildUserCatalog: %v", err)
	}
	if cat.ProviderID != "user:mix" || cat.Name != "Mix" {
		t.Fatalf("identity = (%q,%q)", cat.ProviderID, cat.Name)
	}
	// All three channels now appear (playlist no longer skipped).
	if len(cat.Channels) != 3 {
		t.Fatalf("len(Channels) = %d, want 3", len(cat.Channels))
	}
	byID := map[string]Channel{}
	for _, ch := range cat.Channels {
		byID[ch.ID] = ch
	}
	if live := byID["live"]; len(live.Items) != 1 || !live.Items[0].Direct || live.Items[0].URL != "https://cdn.example.com/live.m3u8" {
		t.Fatalf("direct channel = %+v", live.Items)
	}
	if vid := byID["vid"]; len(vid.Items) != 1 || vid.Items[0].Direct || vid.Items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("single channel = %+v", vid.Items)
	}
	list, ok := byID["list"]
	if !ok {
		t.Fatal("playlist channel 'list' missing — it must be enumerated, not skipped")
	}
	if len(list.Items) != 2 || list.Items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" || list.Items[0].Direct {
		t.Fatalf("playlist items = %+v, want 2 non-direct enumerated items", list.Items)
	}
	if fr.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1 (only the playlist channel enumerates)", fr.enumCalls)
	}
}

func TestBuildUserCatalog_PlaylistEnumerationFailureKeepsProvider(t *testing.T) {
	t.Parallel()
	enum := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("private playlist")},
		cacheDir: t.TempDir(), cfg: DefaultConfig(),
	}
	cat, err := buildUserCatalog(context.Background(), userCatalogTestDef(), enum)
	if err != nil {
		t.Fatalf("buildUserCatalog must not fail on a single playlist error: %v", err)
	}
	if len(cat.Channels) != 3 {
		t.Fatalf("len(Channels) = %d, want 3 (provider stays usable)", len(cat.Channels))
	}
	byID := map[string]Channel{}
	for _, ch := range cat.Channels {
		byID[ch.ID] = ch
	}
	if len(byID["list"].Items) != 0 {
		t.Fatalf("failed playlist should have 0 items, got %d", len(byID["list"].Items))
	}
	if len(byID["live"].Items) != 1 || len(byID["vid"].Items) != 1 {
		t.Fatalf("non-playlist channels must keep their items on playlist failure: live=%d vid=%d", len(byID["live"].Items), len(byID["vid"].Items))
	}
}

func TestBuildProviderCatalog_DispatchesUserType(t *testing.T) {
	t.Parallel()
	cat, err := buildProviderCatalog(userCatalogTestDef(), nil, DefaultConfig())
	if err != nil {
		t.Fatalf("buildProviderCatalog(user): %v", err)
	}
	if cat.ProviderID != "user:mix" {
		t.Fatalf("ProviderID = %q, want user:mix", cat.ProviderID)
	}
}
