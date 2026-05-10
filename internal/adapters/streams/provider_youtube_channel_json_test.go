package streams

import (
	"fmt"
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

func TestYouTubeChannelJSONUsesBundledMTVChannelMetadata(t *testing.T) {
	def := bundledMTVDefinition()
	raw := []byte(`{"120minutes":["dQw4w9WgXcQ"],"80s":["9bZkp7q19f0"],"defjam":["3JZ_D3ELwOQ"],"spikejonze":["AAAAAAAAAAA"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	cases := map[string]struct {
		name  string
		group string
	}{
		"120minutes": {name: "120 Minutes", group: "shows"},
		"80s":        {name: "80s", group: "decades"},
		"defjam":     {name: "Def Jam", group: "labels"},
		"spikejonze": {name: "Spike Jonze", group: "directors"},
	}
	for id, want := range cases {
		ch := cat.Channel(id)
		if ch == nil {
			t.Fatalf("channel %q missing", id)
		}
		if ch.Name != want.name || ch.GroupID != want.group {
			t.Fatalf("channel %q metadata = name %q group %q, want name %q group %q", id, ch.Name, ch.GroupID, want.name, want.group)
		}
	}
}

func TestYouTubeChannelJSONSynthesizesAllChannelWhenMissing(t *testing.T) {
	def := bundledMTVDefinition()
	raw := []byte(`{"80s":["AAAAAAAAAAA","BBBBBBBBBBB"],"90s":["CCCCCCCCCCC"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	all := cat.Channel("all")
	if all == nil {
		t.Fatal("all channel missing")
	}
	if all.Name != "All MTV Rewind" || all.GroupID != "decades" {
		t.Fatalf("all metadata = name %q group %q", all.Name, all.GroupID)
	}
	if got := len(all.Items); got != 3 {
		t.Fatalf("all items = %d, want flattened playlist items", got)
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

func TestYouTubeChannelJSONRejectsTooManyCatalogChannels(t *testing.T) {
	var raw strings.Builder
	raw.WriteByte('{')
	for i := 0; i <= maxManifestChannels; i++ {
		if i > 0 {
			raw.WriteByte(',')
		}
		fmt.Fprintf(&raw, "%q:[\"dQw4w9WgXcQ\"]", fmt.Sprintf("chan-%d", i))
	}
	raw.WriteByte('}')

	_, err := buildYouTubeChannelCatalog(bundledMTVDefinition(), []byte(raw.String()), DefaultConfig())
	if err == nil {
		t.Fatal("oversized channel catalog accepted")
	}
	if !strings.Contains(err.Error(), "channels") {
		t.Fatalf("error = %q, want channel bound error", err)
	}
}

func TestYouTubeChannelJSONRejectsTooManyCatalogItems(t *testing.T) {
	cfg := DefaultConfig()
	channelCount := maxCatalogItems/cfg.MaxItemsPerChannel + 1

	var raw strings.Builder
	raw.WriteByte('{')
	for channel := 0; channel < channelCount; channel++ {
		if channel > 0 {
			raw.WriteByte(',')
		}
		fmt.Fprintf(&raw, "%q:[", fmt.Sprintf("chan-%d", channel))
		for item := 0; item < cfg.MaxItemsPerChannel; item++ {
			if item > 0 {
				raw.WriteByte(',')
			}
			raw.WriteString(`"dQw4w9WgXcQ"`)
		}
		raw.WriteByte(']')
	}
	raw.WriteByte('}')

	_, err := buildYouTubeChannelCatalog(bundledMTVDefinition(), []byte(raw.String()), cfg)
	if err == nil {
		t.Fatal("oversized item catalog accepted")
	}
	if !strings.Contains(err.Error(), "items") {
		t.Fatalf("error = %q, want item bound error", err)
	}
}

func TestYouTubeChannelJSONAllowsTotalCatalogItemsAtLimit(t *testing.T) {
	cfg := DefaultConfig()
	channelCount := maxCatalogItems / cfg.MaxItemsPerChannel

	var raw strings.Builder
	raw.WriteByte('{')
	for channel := 0; channel < channelCount; channel++ {
		if channel > 0 {
			raw.WriteByte(',')
		}
		fmt.Fprintf(&raw, "%q:[", fmt.Sprintf("chan-%d", channel))
		for item := 0; item < cfg.MaxItemsPerChannel; item++ {
			if item > 0 {
				raw.WriteByte(',')
			}
			raw.WriteString(`"dQw4w9WgXcQ"`)
		}
		raw.WriteByte(']')
	}
	raw.WriteByte('}')

	if _, err := buildYouTubeChannelCatalog(bundledMTVDefinition(), []byte(raw.String()), cfg); err != nil {
		t.Fatalf("limit-sized catalog rejected: %v", err)
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

func TestBundledSeedCatalogsDoNotContainPlaceholderVideos(t *testing.T) {
	placeholderIDs := map[string]string{
		"dQw4w9WgXcQ": "sample URL placeholder",
		"9bZkp7q19f0": "sample URL placeholder",
		"3JZ_D3ELwOQ": "sample URL placeholder",
	}
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
			for _, ch := range cat.Channels {
				for _, item := range ch.Items {
					if reason, ok := placeholderIDs[item.SourceID]; ok {
						t.Fatalf("%s seed channel %q contains placeholder video %q (%s)", tt.name, ch.ID, item.SourceID, reason)
					}
				}
			}
		})
	}
}
