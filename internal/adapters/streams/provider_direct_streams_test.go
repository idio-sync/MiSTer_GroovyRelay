package streams

import "testing"

func TestBuildDirectStreamsCatalogToonami(t *testing.T) {
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	if cat.ProviderID != "toonami-aftermath" || cat.Name != "Toonami Aftermath" {
		t.Fatalf("catalog identity = %#v", cat)
	}
	if len(cat.Groups) != 1 || cat.Groups[0].ID != "live" || cat.Groups[0].Name != "Live Channels" {
		t.Fatalf("groups = %#v", cat.Groups)
	}
	if len(cat.Channels) != 4 {
		t.Fatalf("channels = %d, want 4", len(cat.Channels))
	}
	want := map[string]string{
		"east":   "http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
		"west":   "http://api.toonamiaftermath.com:3000/pst/playlist.m3u8",
		"movies": "http://api.toonamiaftermath.com:3000/movies/playlist.m3u8",
		"radio":  "http://api.toonamiaftermath.com:3000/radio/playlist.m3u8",
	}
	seen := make(map[string]bool, len(want))
	for _, ch := range cat.Channels {
		wantURL, ok := want[ch.ID]
		if !ok {
			t.Fatalf("unexpected channel ID %q", ch.ID)
		}
		if seen[ch.ID] {
			t.Fatalf("duplicate channel ID %q", ch.ID)
		}
		seen[ch.ID] = true
		if ch.GroupID != "live" {
			t.Fatalf("channel %q group = %q, want live", ch.ID, ch.GroupID)
		}
		if len(ch.Items) != 1 {
			t.Fatalf("channel %q items = %d, want 1", ch.ID, len(ch.Items))
		}
		item := ch.Items[0]
		if item.ID != ch.ID || item.SourceID != ch.ID || item.Title != ch.Name {
			t.Fatalf("channel %q item identity = %#v", ch.ID, item)
		}
		if item.URL != wantURL {
			t.Fatalf("channel %q URL = %q, want %q", ch.ID, item.URL, wantURL)
		}
		if !item.Direct {
			t.Fatalf("channel %q item Direct = false, want true", ch.ID)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("missing channel ID %q", id)
		}
	}
}

func TestBuildDirectStreamsCatalogRejectsBadURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "blank", url: ""},
		{name: "surrounding whitespace", url: " http://api.toonamiaftermath.com:3000/est/playlist.m3u8 "},
		{name: "userinfo", url: "http://user@api.toonamiaftermath.com:3000/est/playlist.m3u8"},
		{name: "ftp", url: "ftp://api.toonamiaftermath.com:3000/est/playlist.m3u8"},
		{name: "missing host", url: "http:///playlist.m3u8"},
		{name: "wrong host", url: "http://example.com/est/playlist.m3u8"},
		{name: "wrong path", url: "http://api.toonamiaftermath.com:3000/evil/playlist.m3u8"},
		{name: "wrong port", url: "http://api.toonamiaftermath.com/est/playlist.m3u8"},
		{name: "query string", url: "http://api.toonamiaftermath.com:3000/est/playlist.m3u8?token=x"},
		{name: "fragment", url: "http://api.toonamiaftermath.com:3000/est/playlist.m3u8#frag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := bundledToonamiAftermathDefinition()
			def.Channels[0].URL = tt.url
			if _, err := buildDirectStreamsCatalog(def); err == nil {
				t.Fatal("bad direct stream URL accepted")
			}
		})
	}
}

func TestBuildDirectStreamsCatalogRejectsUnknownProviderWithoutValidator(t *testing.T) {
	def := bundledToonamiAftermathDefinition()
	def.ID = "future-direct"
	if _, err := buildDirectStreamsCatalog(def); err == nil {
		t.Fatal("direct-stream provider without validator accepted")
	}
}

func TestYouTubeChannelCatalogIgnoresChannelDefinitionURL(t *testing.T) {
	def := bundledMTVDefinition()
	def.Channels = []ChannelDefinition{{
		ID:       "metal",
		Name:     "Metal",
		URL:      "http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
		PlayMode: PlayShuffle,
	}}
	cat, err := buildYouTubeChannelCatalog(def, []byte(`{"metal":["dQw4w9WgXcQ"]}`), DefaultConfig())
	if err != nil {
		t.Fatalf("buildYouTubeChannelCatalog: %v", err)
	}
	item := cat.Channel("metal").Items[0]
	if item.URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("item URL = %q, want YouTube watch URL", item.URL)
	}
	if item.Direct {
		t.Fatal("YouTube item Direct = true, want false")
	}
}
