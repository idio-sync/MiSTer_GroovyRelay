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
		Groups:   mtvGroups(),
		Channels: mtvChannels(),
	}
}

func mtvGroups() []GroupDefinition {
	return []GroupDefinition{
		{ID: "shows", Name: "MTV Shows", Order: 10},
		{ID: "decades", Name: "By Decade", Order: 20},
		{ID: "genres", Name: "Genres", Order: 30},
		{ID: "directors", Name: "Directors", Order: 40},
		{ID: "labels", Name: "Labels & Scenes", Order: 50},
		{ID: "artists", Name: "Artists & Events", Order: 60},
	}
}

func mtvChannels() []ChannelDefinition {
	return []ChannelDefinition{
		{ID: "1stday", Name: "First Day on MTV", GroupID: "shows", PlayMode: PlaySequential, Order: 10},
		{ID: "120minutes", Name: "120 Minutes", GroupID: "shows", PlayMode: PlayShuffle, Order: 20},
		{ID: "amp", Name: "AMP", GroupID: "shows", PlayMode: PlayShuffle, Order: 30},
		{ID: "fuse", Name: "Fuse", GroupID: "shows", PlayMode: PlayShuffle, Order: 40},
		{ID: "mtv2", Name: "MTV2", GroupID: "shows", PlayMode: PlayShuffle, Order: 50},
		{ID: "mtvuk", Name: "MTV UK", GroupID: "shows", PlayMode: PlayShuffle, Order: 60},
		{ID: "muchmusic", Name: "MuchMusic", GroupID: "shows", PlayMode: PlayShuffle, Order: 70},
		{ID: "popupvideo", Name: "Pop-Up Video", GroupID: "shows", PlayMode: PlayShuffle, Order: 80},
		{ID: "springbreak", Name: "MTV Spring Break", GroupID: "shows", PlayMode: PlayShuffle, Order: 90},
		{ID: "trl", Name: "TRL", GroupID: "shows", PlayMode: PlayShuffle, Order: 100},
		{ID: "unplugged", Name: "MTV Unplugged", GroupID: "shows", PlayMode: PlayShuffle, Order: 110},
		{ID: "flexlist", Name: "Flex List", GroupID: "shows", PlayMode: PlayShuffle, Order: 120},
		{ID: "markgoodman", Name: "Mark Goodman", GroupID: "shows", PlayMode: PlayShuffle, Order: 130},
		{ID: "commercials", Name: "Commercials", GroupID: "shows", PlayMode: PlayShuffle, Order: 140},

		{ID: "70s", Name: "70s", GroupID: "decades", PlayMode: PlayShuffle, Order: 10},
		{ID: "80s", Name: "80s", GroupID: "decades", PlayMode: PlayShuffle, Order: 20},
		{ID: "90s", Name: "90s", GroupID: "decades", PlayMode: PlayShuffle, Order: 30},
		{ID: "2000s", Name: "2000s", GroupID: "decades", PlayMode: PlayShuffle, Order: 40},
		{ID: "2010s", Name: "2010s", GroupID: "decades", PlayMode: PlayShuffle, Order: 50},
		{ID: "2020s", Name: "2020s", GroupID: "decades", PlayMode: PlayShuffle, Order: 60},
		{ID: "all", Name: "All MTV Rewind", GroupID: "decades", PlayMode: PlayShuffle, Order: 100},

		{ID: "animated", Name: "Animated", GroupID: "genres", PlayMode: PlayShuffle, Order: 10},
		{ID: "club", Name: "Club", GroupID: "genres", PlayMode: PlayShuffle, Order: 20},
		{ID: "country", Name: "Country", GroupID: "genres", PlayMode: PlayShuffle, Order: 30},
		{ID: "familyfriendly", Name: "Family Friendly", GroupID: "genres", PlayMode: PlayShuffle, Order: 40},
		{ID: "jams", Name: "MTV Jams", GroupID: "genres", PlayMode: PlayShuffle, Order: 50},
		{ID: "metal", Name: "Headbangers Ball", GroupID: "genres", PlayMode: PlayShuffle, Order: 60},
		{ID: "raps", Name: "Rap & Hip-Hop", GroupID: "genres", PlayMode: PlayShuffle, Order: 70},
		{ID: "sonidolatino", Name: "Sonido Latino", GroupID: "genres", PlayMode: PlayShuffle, Order: 80},

		{ID: "chrisapplebaum", Name: "Chris Applebaum", GroupID: "directors", PlayMode: PlayShuffle, Order: 10},
		{ID: "chriscunningham", Name: "Chris Cunningham", GroupID: "directors", PlayMode: PlayShuffle, Order: 20},
		{ID: "deankarr", Name: "Dean Karr", GroupID: "directors", PlayMode: PlayShuffle, Order: 30},
		{ID: "floriasigismondi", Name: "Floria Sigismondi", GroupID: "directors", PlayMode: PlayShuffle, Order: 40},
		{ID: "johnhardin", Name: "John Hardin", GroupID: "directors", PlayMode: PlayShuffle, Order: 50},
		{ID: "michelgondry", Name: "Michel Gondry", GroupID: "directors", PlayMode: PlayShuffle, Order: 60},
		{ID: "romancoppola", Name: "Roman Coppola", GroupID: "directors", PlayMode: PlayShuffle, Order: 70},
		{ID: "spikejonze", Name: "Spike Jonze", GroupID: "directors", PlayMode: PlayShuffle, Order: 80},
		{ID: "wayneisham", Name: "Wayne Isham", GroupID: "directors", PlayMode: PlayShuffle, Order: 90},

		{ID: "4ad", Name: "4AD", GroupID: "labels", PlayMode: PlayShuffle, Order: 10},
		{ID: "atorecords", Name: "ATO Records", GroupID: "labels", PlayMode: PlayShuffle, Order: 20},
		{ID: "bangladesh", Name: "Bangladesh", GroupID: "labels", PlayMode: PlayShuffle, Order: 30},
		{ID: "chillhopmusic", Name: "Chillhop Music", GroupID: "labels", PlayMode: PlayShuffle, Order: 40},
		{ID: "colors", Name: "COLORS", GroupID: "labels", PlayMode: PlayShuffle, Order: 50},
		{ID: "deathrow", Name: "Death Row", GroupID: "labels", PlayMode: PlayShuffle, Order: 60},
		{ID: "defjam", Name: "Def Jam", GroupID: "labels", PlayMode: PlayShuffle, Order: 70},
		{ID: "earache", Name: "Earache", GroupID: "labels", PlayMode: PlayShuffle, Order: 80},
		{ID: "empire", Name: "EMPIRE", GroupID: "labels", PlayMode: PlayShuffle, Order: 90},
		{ID: "epitaph", Name: "Epitaph", GroupID: "labels", PlayMode: PlayShuffle, Order: 100},
		{ID: "gcs", Name: "GCS", GroupID: "labels", PlayMode: PlayShuffle, Order: 110},
		{ID: "killrockstars", Name: "Kill Rock Stars", GroupID: "labels", PlayMode: PlayShuffle, Order: 120},
		{ID: "mergerecords", Name: "Merge Records", GroupID: "labels", PlayMode: PlayShuffle, Order: 130},
		{ID: "metalblade", Name: "Metal Blade", GroupID: "labels", PlayMode: PlayShuffle, Order: 140},
		{ID: "motown", Name: "Motown", GroupID: "labels", PlayMode: PlayShuffle, Order: 150},
		{ID: "muterecords", Name: "Mute Records", GroupID: "labels", PlayMode: PlayShuffle, Order: 160},
		{ID: "roadrunner", Name: "Roadrunner", GroupID: "labels", PlayMode: PlayShuffle, Order: 170},
		{ID: "subpop", Name: "Sub Pop", GroupID: "labels", PlayMode: PlayShuffle, Order: 180},
		{ID: "tseries", Name: "T-Series", GroupID: "labels", PlayMode: PlayShuffle, Order: 190},
		{ID: "victoryrecords", Name: "Victory Records", GroupID: "labels", PlayMode: PlayShuffle, Order: 200},
		{ID: "warpedrecords", Name: "Warped Records", GroupID: "labels", PlayMode: PlayShuffle, Order: 210},
		{ID: "xlrecordings", Name: "XL Recordings", GroupID: "labels", PlayMode: PlayShuffle, Order: 220},

		{ID: "beatles", Name: "The Beatles", GroupID: "artists", PlayMode: PlayShuffle, Order: 10},
		{ID: "blackcrowes", Name: "The Black Crowes", GroupID: "artists", PlayMode: PlayShuffle, Order: 20},
		{ID: "eurovision", Name: "Eurovision", GroupID: "artists", PlayMode: PlayShuffle, Order: 30},
		{ID: "grateful", Name: "Grateful Dead", GroupID: "artists", PlayMode: PlayShuffle, Order: 40},
		{ID: "liveaid", Name: "Live Aid", GroupID: "artists", PlayMode: PlayShuffle, Order: 50},
		{ID: "pearljam", Name: "Pearl Jam", GroupID: "artists", PlayMode: PlayShuffle, Order: 60},
		{ID: "rollingstones", Name: "The Rolling Stones", GroupID: "artists", PlayMode: PlayShuffle, Order: 70},
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
