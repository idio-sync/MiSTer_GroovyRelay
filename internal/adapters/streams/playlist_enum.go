package streams

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
)

// cachedPlaylistItem is the on-disk shape of an enumerated playlist item.
// StreamItem has no JSON tags, so cache persistence uses this explicit struct
// (and stays stable if StreamItem gains in-memory-only fields later).
type cachedPlaylistItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	SourceID string `json:"source_id"`
}

func encodePlaylistItems(items []StreamItem) ([]byte, error) {
	out := make([]cachedPlaylistItem, 0, len(items))
	for _, it := range items {
		out = append(out, cachedPlaylistItem{ID: it.ID, Title: it.Title, URL: it.URL, SourceID: it.SourceID})
	}
	return json.Marshal(out)
}

func decodePlaylistItems(raw []byte) ([]StreamItem, error) {
	var in []cachedPlaylistItem
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("decode playlist items: %w", err)
	}
	out := make([]StreamItem, 0, len(in))
	for _, c := range in {
		out = append(out, StreamItem{ID: c.ID, Title: c.Title, URL: c.URL, SourceID: c.SourceID, Direct: false})
	}
	return out, nil
}

// playlistEntriesToItems maps flat-playlist entries to non-direct StreamItems.
// A YouTube-ID entry (from either "id" or a bare "url") gets the canonical
// watch URL (matching the youtube-channel-json builder,
// provider_youtube_channel_json.go:78). Non-YouTube entries must pass the same
// syntactic user-provider URL gate used at authoring time before they can be
// cached or later dereferenced by yt-dlp; unsafe entries are dropped. Items are
// Direct:false → resolved by yt-dlp at play time, where the §7.2 user resolved
// media-URL recheck (playback.go) applies.
func playlistEntriesToItems(entries []ytdlp.PlaylistEntry, maxItems int) []StreamItem {
	items := make([]StreamItem, 0, len(entries))
	for _, e := range entries {
		if maxItems > 0 && len(items) >= maxItems {
			break
		}
		id, pageURL := strings.TrimSpace(e.ID), strings.TrimSpace(e.URL)
		switch {
		case youtubeIDRE.MatchString(id):
			pageURL = youtubeWatchURL(id)
		case youtubeIDRE.MatchString(pageURL):
			id = pageURL
			pageURL = youtubeWatchURL(id)
		case pageURL == "":
			continue
		default:
			if err := validateUserProviderHost(pageURL); err != nil {
				continue
			}
		}
		if id == "" {
			id = pageURL
		}
		items = append(items, StreamItem{ID: id, Title: e.Title, URL: pageURL, SourceID: id, Direct: false})
	}
	return items
}

// (userPlaylistEnumerator follows in Task 4.)
