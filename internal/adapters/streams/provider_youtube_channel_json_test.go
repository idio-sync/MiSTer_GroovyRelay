package streams

import (
	"strings"
	"testing"
)

func TestYouTubeChannelJSONBuildsCatalog(t *testing.T) {
	def := bundledMTVDefinition()
	raw := []byte(`{"metal":["dQw4w9WgXcQ"],"unknown":["9bZkp7q19f0"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cat.ProviderID != "mtv-rewind" {
		t.Fatalf("ProviderID = %q", cat.ProviderID)
	}
	if got := cat.Channel("metal").Items[0].URL; got != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("item URL = %q", got)
	}
	if cat.Channel("unknown").GroupID != "ungrouped" {
		t.Fatal("unknown channel should move to ungrouped")
	}
}

func TestYouTubeChannelJSONSkipsMalformedIDs(t *testing.T) {
	def := bundledMTVDefinition()
	raw := []byte(`{"metal":["not a youtube id","dQw4w9WgXcQ"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := len(cat.Channel("metal").Items); got != 1 {
		t.Fatalf("items = %d, want 1", got)
	}
}

func TestYouTubeChannelJSONSkipsInvalidUnknownChannelIDs(t *testing.T) {
	def := bundledMTVDefinition()
	raw := []byte(`{"adhoc":["dQw4w9WgXcQ"],"":["dQw4w9WgXcQ"],"bad/id":["dQw4w9WgXcQ"],"bonus-channel":["9bZkp7q19f0"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, id := range []string{"adhoc", "", "bad/id"} {
		if cat.Channel(id) != nil {
			t.Fatalf("invalid unknown channel %q should be skipped", id)
		}
	}
	ch := cat.Channel("bonus-channel")
	if ch == nil {
		t.Fatal("valid unknown channel should be preserved")
	}
	if ch.GroupID != "ungrouped" {
		t.Fatalf("valid unknown channel group = %q, want ungrouped", ch.GroupID)
	}
}

func TestYouTubeChannelJSONRejectsNullPlaylistMap(t *testing.T) {
	_, err := buildYouTubeChannelCatalog(bundledMTVDefinition(), []byte(`null`), DefaultConfig())
	if err == nil {
		t.Fatal("null playlist map accepted")
	}
	if !strings.Contains(err.Error(), "playlist JSON must be an object") {
		t.Fatalf("error = %q, want clear null object error", err)
	}
}

func TestCartoonCommercialsExcludedFromAll(t *testing.T) {
	def := bundledCartoonDefinition()
	raw := []byte(`{"all":["dQw4w9WgXcQ"],"commercials":["9bZkp7q19f0"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cat.Channel("commercials") != nil {
		t.Fatal("commercials should not be exposed as a normal v1 channel")
	}
}

func TestBundledSeedCatalogsBuild(t *testing.T) {
	tests := []struct {
		name string
		path string
		def  ProviderDefinition
	}{
		{name: "mtv", path: "testdata/mtv-playlists.seed.json", def: bundledMTVDefinition()},
		{name: "cartoon", path: "testdata/cartoon-playlists.seed.json", def: bundledCartoonDefinition()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := seedFS.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("read seed: %v", err)
			}
			cat, err := buildYouTubeChannelCatalog(tt.def, raw, DefaultConfig())
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if len(cat.Channels) == 0 {
				t.Fatal("seed catalog has no channels")
			}
			for _, ch := range cat.Channels {
				if len(ch.Items) > 0 {
					return
				}
			}
			t.Fatal("seed catalog has no channel items")
		})
	}
}
