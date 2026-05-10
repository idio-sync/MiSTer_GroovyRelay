package streams

import "embed"

//go:embed testdata/*.seed.json
var seedFS embed.FS

const youtubeChannelJSONProviderType = "youtube-channel-json"

func bundledManifest() Manifest {
	return Manifest{
		Version: 1,
		Providers: []ProviderDefinition{
			bundledMTVDefinition(),
			bundledCartoonDefinition(),
		},
	}
}

func bundledMTVDefinition() ProviderDefinition {
	return ProviderDefinition{
		ID:              "mtv-rewind",
		Type:            youtubeChannelJSONProviderType,
		DisplayName:     "MTV Rewind",
		BaseURL:         "https://wantmymtv.vercel.app",
		PlaylistURL:     "https://wantmymtv.vercel.app/public/mtv-playlists.json",
		DefaultChannel:  "1stday",
		DefaultPlayMode: PlayShuffle,
		URLRules: []URLRule{
			{
				ID:         "mtv-player-channel",
				Schemes:    []string{"https"},
				Hosts:      []string{"wantmymtv.vercel.app", "wantmymtv.xyz"},
				Path:       "/player.html",
				Target:     "channel",
				QueryParam: "channel",
			},
			{
				ID:         "mtv-player-video",
				Schemes:    []string{"https"},
				Hosts:      []string{"wantmymtv.vercel.app", "wantmymtv.xyz"},
				Path:       "/player.html",
				Target:     "item",
				QueryParam: "v",
			},
			{
				ID:         "mtv-short-video",
				Schemes:    []string{"https"},
				Hosts:      []string{"wantmymtv.vercel.app", "wantmymtv.xyz"},
				PathPrefix: "/s/",
				Target:     "item",
			},
		},
		Groups: []GroupDefinition{
			{ID: "shows", Name: "MTV Shows", Order: 50},
			{ID: "decades", Name: "By Decade", Order: 60},
		},
		Channels: []ChannelDefinition{
			{
				ID:       "1stday",
				Name:     "First Day on MTV",
				GroupID:  "shows",
				PlayMode: PlaySequential,
				Order:    10,
			},
			{
				ID:       "metal",
				Name:     "Headbangers Ball",
				GroupID:  "shows",
				PlayMode: PlayShuffle,
				Order:    40,
			},
			{
				ID:       "all",
				Name:     "All MTV Rewind",
				GroupID:  "decades",
				PlayMode: PlayShuffle,
				Order:    100,
			},
		},
	}
}

func bundledCartoonDefinition() ProviderDefinition {
	return ProviderDefinition{
		ID:              "cartoon-rewind",
		Type:            youtubeChannelJSONProviderType,
		DisplayName:     "Cartoon Rewind",
		BaseURL:         "https://cartoonrewind.tv",
		PlaylistURL:     "https://cartoonrewind.tv/cartoon-playlists.json",
		DefaultChannel:  "all",
		DefaultPlayMode: PlayShuffle,
		URLRules: []URLRule{
			{
				ID:         "cartoon-player-channel",
				Schemes:    []string{"https"},
				Hosts:      []string{"cartoonrewind.tv"},
				Path:       "/player.html",
				Target:     "channel",
				QueryParam: "channel",
			},
			{
				ID:         "cartoon-player-video",
				Schemes:    []string{"https"},
				Hosts:      []string{"cartoonrewind.tv"},
				Path:       "/player.html",
				Target:     "item",
				QueryParam: "v",
			},
		},
		Groups: []GroupDefinition{
			{ID: "shows", Name: "Cartoon Shows", Order: 50},
		},
		Channels: []ChannelDefinition{
			{
				ID:       "heman",
				Name:     "He-Man",
				GroupID:  "shows",
				PlayMode: PlayShuffle,
				Order:    10,
			},
			{
				ID:       "all",
				Name:     "All Cartoons",
				GroupID:  "shows",
				PlayMode: PlayShuffle,
				Order:    100,
			},
		},
	}
}
