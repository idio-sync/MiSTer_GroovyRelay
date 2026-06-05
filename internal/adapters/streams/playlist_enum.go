package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

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
		// Non-YouTube entries with no id reuse the (validated) URL as the item ID;
		// yt-dlp resolves this URL at play time and bad-item tracking keys on it.
		if id == "" {
			id = pageURL
		}
		items = append(items, StreamItem{ID: id, Title: e.Title, URL: pageURL, SourceID: id, Direct: false})
	}
	return items
}

// userPlaylistEnumerator resolves a user playlist channel's items. It is the
// single seam through which buildUserCatalog gets playlist items, so the catalog
// builder stays free of network/cache concerns.
//
//   - resolver == nil (startup, "cache-only"): returns cached items if present,
//     else nil. Startup never blocks on yt-dlp; the background refresh fills
//     uncached playlist channels (see Task 6 + the AllowRemoteManifest residual).
//   - resolver != nil (refresh, "live"): enumerates via yt-dlp, caches on
//     success, and SERVES STALE (returns the prior cache) on failure so a
//     transient yt-dlp error never empties a working channel. The returned
//     error is advisory (for logging); callers keep the provider usable.
type userPlaylistEnumerator struct {
	resolver    streamResolver // nil → cache-only
	cookiesPath string
	cacheDir    string
	cfg         Config
}

func (e userPlaylistEnumerator) cached(providerID, channelID, pageURL string) ([]StreamItem, bool) {
	raw, _, ok := readConditionalCache(e.cacheDir, userPlaylistCacheKey(providerID, channelID), pageURL)
	if !ok {
		return nil, false
	}
	items, err := decodePlaylistItems(raw)
	if err != nil {
		return nil, false
	}
	return items, true
}

func (e userPlaylistEnumerator) channelItems(ctx context.Context, providerID, channelID, pageURL string) ([]StreamItem, error) {
	cachedItems, cachedOK := e.cached(providerID, channelID, pageURL)

	if e.resolver == nil {
		if cachedOK {
			return cachedItems, nil
		}
		return nil, nil
	}

	maxItems := e.cfg.MaxItemsPerChannel
	timeout := time.Duration(e.cfg.CatalogRequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	enumCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	entries, err := e.resolver.EnumeratePlaylist(enumCtx, pageURL, e.cookiesPath, maxItems)
	if err != nil {
		if cachedOK {
			return cachedItems, fmt.Errorf("enumerate playlist %q/%q (serving %d cached): %w",
				providerID, channelID, len(cachedItems), err)
		}
		return nil, fmt.Errorf("enumerate playlist %q/%q: %w", providerID, channelID, err)
	}

	items := playlistEntriesToItems(entries, maxItems)
	// Cache write failure is non-fatal: return the freshly enumerated items now
	// and let the next refresh cycle re-attempt the write.
	if raw, encErr := encodePlaylistItems(items); encErr == nil {
		meta := CacheMetadata{SourceURL: pageURL, FetchedAt: time.Now().UTC()}
		if wErr := writeCacheFile(e.cacheDir, userPlaylistCacheKey(providerID, channelID), raw, meta); wErr != nil {
			slog.Warn("user_providers: playlist cache write failed",
				"provider", providerID, "channel", channelID, "err", wErr)
		}
	} else {
		slog.Warn("user_providers: playlist cache encode failed",
			"provider", providerID, "channel", channelID, "err", encErr)
	}
	return items, nil
}

func playlistErrorForLog(err error, pageURL string) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), pageURL, redactPlaylistPageURL(pageURL))
}

func redactPlaylistPageURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<redacted-url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
