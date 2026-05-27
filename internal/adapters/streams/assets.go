package streams

import (
	"embed"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

//go:embed testdata/*.seed.json
var seedFS embed.FS

const (
	youtubeChannelJSONProviderType = "youtube-channel-json"
	directStreamsProviderType      = "direct-streams"
)

func bundledManifest() Manifest {
	return Manifest{
		Version: 1,
		Providers: []ProviderDefinition{
			bundledMTVDefinition(),
			bundledCartoonDefinition(),
			bundledToonamiAftermathDefinition(),
		},
	}
}

func bundledMTVDefinition() ProviderDefinition {
	return ProviderDefinition{
		ID:              "mtv-rewind",
		Type:            youtubeChannelJSONProviderType,
		DisplayName:     "MTV Rewind",
		LogoURL:         "https://wantmymtv.vercel.app/public/images/rewindlogo.png",
		LogoAlt:         "MTV Rewind logo",
		FallbackLabel:   "MTV REWIND",
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
		LogoURL:         "https://cartoonrewind.tv/social.png",
		LogoAlt:         "Cartoon Rewind logo",
		FallbackLabel:   "CARTOON REWIND",
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
		Groups:   cartoonGroups(),
		Channels: cartoonChannels(),
	}
}

func cartoonGroups() []GroupDefinition {
	return []GroupDefinition{
		{ID: "1930s", Name: "1930s", Order: 10},
		{ID: "1940s", Name: "1940s", Order: 20},
		{ID: "1950s", Name: "1950s", Order: 30},
		{ID: "1960s", Name: "1960s", Order: 40},
		{ID: "1980s", Name: "1980s", Order: 50},
		{ID: "1990s", Name: "1990s", Order: 60},
		{ID: "all", Name: "All", Order: 100},
	}
}

func cartoonChannels() []ChannelDefinition {
	return []ChannelDefinition{
		{ID: "loonytunes", Name: "Looney Tunes", GroupID: "1930s", PlayMode: PlayShuffle, Order: 10},
		{ID: "tomandjerry", Name: "Tom and Jerry", GroupID: "1940s", PlayMode: PlayShuffle, Order: 10},
		{ID: "rockyandbullwinkle", Name: "Rocky and Bullwinkle", GroupID: "1950s", PlayMode: PlayShuffle, Order: 10},
		{ID: "pinkpanther", Name: "Pink Panther", GroupID: "1960s", PlayMode: PlayShuffle, Order: 10},
		{ID: "speedracer", Name: "Speed Racer", GroupID: "1960s", PlayMode: PlayShuffle, Order: 20},
		{ID: "underdog", Name: "Underdog", GroupID: "1960s", PlayMode: PlayShuffle, Order: 30},
		{ID: "smurfs", Name: "The Smurfs", GroupID: "1980s", PlayMode: PlayShuffle, Order: 10},
		{ID: "heman", Name: "He-Man", GroupID: "1980s", PlayMode: PlayShuffle, Order: 20},
		{ID: "inspectorgadget", Name: "Inspector Gadget", GroupID: "1980s", PlayMode: PlayShuffle, Order: 30},
		{ID: "transformers", Name: "Transformers", GroupID: "1980s", PlayMode: PlayShuffle, Order: 40},
		{ID: "animaniacs", Name: "Animaniacs", GroupID: "1990s", PlayMode: PlayShuffle, Order: 10},
		{ID: "carmensandiego", Name: "Where on Earth Is Carmen Sandiego?", GroupID: "1990s", PlayMode: PlayShuffle, Order: 20},
		{ID: "all", Name: "All Cartoons", GroupID: "all", PlayMode: PlayShuffle, Order: 10},
	}
}

func bundledToonamiAftermathDefinition() ProviderDefinition {
	return ProviderDefinition{
		ID:              "toonami-aftermath",
		Type:            directStreamsProviderType,
		DisplayName:     "Toonami Aftermath",
		BaseURL:         "https://www.toonamiaftermath.com",
		DefaultChannel:  "east",
		DefaultPlayMode: PlaySequential,
		Groups: []GroupDefinition{
			{ID: "live", Name: "Live Channels", Order: 10},
		},
		Channels: []ChannelDefinition{
			{ID: "east", Name: "East", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/est/playlist.m3u8", PlayMode: PlaySequential, Order: 10},
			{ID: "west", Name: "West", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/pst/playlist.m3u8", PlayMode: PlaySequential, Order: 20},
			{ID: "movies", Name: "Movies", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/movies/playlist.m3u8", PlayMode: PlaySequential, Order: 30},
			{ID: "radio", Name: "Radio", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/radio/playlist.m3u8", PlayMode: PlaySequential, Order: 40},
		},
	}
}

// bundledChassisPresets is the source of truth for the 12-slot chassis
// preset bank. The mockup's PRESETS map at docs/superpowers/reference/
// 2026-05-21-receiver-v24.html is the visual spec; this literal mirrors
// it. Live is hardcoded per-slot — ChannelDefinition has no Live field
// to derive from. Channels named here MUST exist in the bundled
// manifest; a unit test asserts this at run time.
var bundledChassisPresets = [12]adapters.PresetEntry{
	{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day on MTV", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 2, ProviderID: "mtv-rewind", ChannelID: "80s", Title: "MTV 80s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 3, ProviderID: "mtv-rewind", ChannelID: "90s", Title: "MTV 90s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 4, ProviderID: "mtv-rewind", ChannelID: "trl", Title: "TRL", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 5, ProviderID: "mtv-rewind", ChannelID: "120minutes", Title: "120 Minutes", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 6, ProviderID: "mtv-rewind", ChannelID: "unplugged", Title: "Unplugged", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 7, ProviderID: "cartoon-rewind", ChannelID: "loonytunes", Title: "Looney Tunes", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 8, ProviderID: "cartoon-rewind", ChannelID: "animaniacs", Title: "Animaniacs", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 9, ProviderID: "cartoon-rewind", ChannelID: "heman", Title: "He-Man", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 10, ProviderID: "cartoon-rewind", ChannelID: "all", Title: "All Cartoons", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 11, ProviderID: "toonami-aftermath", ChannelID: "east", Title: "Toonami East", BadgeLabel: "TOONAMI", BadgeClass: "toonami", Live: true},
	{Slot: 12, ProviderID: "toonami-aftermath", ChannelID: "movies", Title: "Toonami Movies", BadgeLabel: "TOONAMI", BadgeClass: "toonami", Live: true},
}

// bundledChassisCatalogProviderIDs is the subset of bundledManifest()
// providers the chassis catalog drawer exposes. Remote/cached manifest
// providers stay available to URL resolution and /ui/streams/*, but
// the receiver chassis is intentionally limited to the 3 mockup
// providers in 3B. A future spec can lift this restriction once
// per-provider catalog browsing rules exist.
var bundledChassisCatalogProviderIDs = []string{
	"mtv-rewind",
	"cartoon-rewind",
	"toonami-aftermath",
}

// providerBadges holds the small-glyph badge metadata for each chassis
// catalog provider. Mirrors the mockup's .ic glyph rendering (label =
// short uppercase string; class = CSS hook for color/treatment).
var providerBadges = map[string]struct {
	Label string
	Class string
}{
	"mtv-rewind":        {"MTV", "mtv"},
	"cartoon-rewind":    {"CART", "cartoon"},
	"toonami-aftermath": {"TOON", "toonami"},
}
