package streams

import (
	"net/url"
	"strings"
)

// userProviderType is the ProviderDefinition.Type value for user-authored
// providers. Its Groups/Channels are inline (not fetched), and its ID is
// always userProviderIDPrefix + slug.
const userProviderType = "user"

// userProviderIDPrefix namespaces user provider IDs so they can never
// collide with or shadow bundled/remote provider IDs in the merged maps.
const userProviderIDPrefix = "user:"

// Channel kinds (ChannelDefinition.Kind) for user providers.
const (
	kindPlaylist = "playlist"
	kindSingle   = "single"
	kindDirect   = "direct"
)

// Limits (spec §10).
const (
	maxUserProviders       = 32
	maxChannelsPerProvider = 100
)

// detectChannelKind infers a channel Kind from a URL using purely syntactic
// rules (no network). First match wins (spec §4.7):
//  1. direct  — path ends in a recognized HLS/DASH manifest suffix.
//  2. playlist — YouTube list= URL.
//  3. single  — everything else (the default).
func detectChannelKind(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return kindSingle
	}
	path := strings.ToLower(u.Path)
	for _, suf := range []string{".m3u8", ".m3u", ".mpd"} {
		if strings.HasSuffix(path, suf) {
			return kindDirect
		}
	}
	host := strings.ToLower(u.Hostname())
	if isYouTubeHost(host) && u.Query().Get("list") != "" {
		return kindPlaylist
	}
	return kindSingle
}

func isYouTubeHost(host string) bool {
	host = strings.TrimPrefix(host, "www.")
	return host == "youtube.com" || host == "m.youtube.com" || host == "music.youtube.com"
}
