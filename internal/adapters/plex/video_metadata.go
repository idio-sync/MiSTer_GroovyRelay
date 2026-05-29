package plex

import (
	"context"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// VideoMetadata is the slice of PMS /library/metadata video attributes
// the VFD needs. Reuses the existing fetchMetadata path (transcode.go).
type VideoMetadata struct {
	Type    string // "movie" | "episode"
	Title   string // episode title (episode) or movie title (movie)
	Show    string // grandparentTitle (episode only)
	Season  int    // parentIndex (episode only)
	Episode int    // index (episode only)
	Year    int
}

// VideoMetadataFor fetches and decodes the first <Video> element under
// the media key. ok=false when the response carries no Video element
// (e.g. the key is music or the fetch returned nothing useful).
func VideoMetadataFor(ctx context.Context, serverURL, mediaKey, token string) (VideoMetadata, bool, error) {
	mc, err := fetchMetadata(ctx, serverURL, mediaKey, token)
	if err != nil {
		return VideoMetadata{}, false, err
	}
	if len(mc.Video) == 0 {
		return VideoMetadata{}, false, nil
	}
	v := mc.Video[0]
	return VideoMetadata{
		Type:    v.Type,
		Title:   v.Title,
		Show:    v.GrandparentTitle,
		Season:  v.ParentIndex,
		Episode: v.Index,
		Year:    v.Year,
	}, true, nil
}

// plexVideoDisplay maps video metadata onto the three VFD tiers.
// Episode: show-first (Primary=show, Secondary=episode, Tertiary=S·E·year).
// Movie: Primary=title, Secondary=year. Falls back to the controller
// title when metadata is absent.
func plexVideoDisplay(md VideoMetadata, fallbackTitle string) core.DisplayMetadata {
	switch md.Type {
	case "episode":
		return core.DisplayMetadata{
			Primary:   firstNonEmpty(md.Show, fallbackTitle),
			Secondary: md.Title,
			Tertiary:  adapters.FormatSeasonEpisode(md.Season, md.Episode, md.Year),
		}
	case "movie":
		return core.DisplayMetadata{
			Primary:   firstNonEmpty(md.Title, fallbackTitle),
			Secondary: adapters.FormatSeasonEpisode(0, 0, md.Year),
		}
	default:
		return core.DisplayMetadata{Primary: firstNonEmpty(md.Title, fallbackTitle)}
	}
}
