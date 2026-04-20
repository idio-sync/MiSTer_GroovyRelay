package plex

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TranscodeRequest carries the inputs to BuildTranscodeURL. OffsetMs is the
// seek start in milliseconds; PMS accepts whole seconds so we divide by 1000.
// MaxBitrate is in kbps; defaults to 2000 when zero.
type TranscodeRequest struct {
	PlexServerURL string
	MediaPath     string
	Token         string
	OffsetMs      int
	OutputWidth   int
	OutputHeight  int
	SessionID     string
	ClientID      string
	MaxBitrate    int
}

// BuildTranscodeURL constructs a PMS /video/:/transcode/universal/start.m3u8
// URL with all the parameters PMS needs to force a server-side transcode to
// our target profile (480p H.264, no direct play/stream). The returned URL
// is what FFmpeg consumes as its -i input.
func BuildTranscodeURL(r TranscodeRequest) string {
	if r.MaxBitrate == 0 {
		r.MaxBitrate = 2000
	}
	q := url.Values{}
	q.Set("path", r.MediaPath)
	q.Set("mediaIndex", "0")
	q.Set("partIndex", "0")
	q.Set("protocol", "http")
	q.Set("fastSeek", "1")
	q.Set("directPlay", "0")
	q.Set("directStream", "0")
	q.Set("copyts", "1")
	q.Set("videoResolution", fmt.Sprintf("%dx%d", r.OutputWidth, r.OutputHeight))
	q.Set("maxVideoBitrate", fmt.Sprintf("%d", r.MaxBitrate))
	q.Set("offset", fmt.Sprintf("%d", r.OffsetMs/1000))
	q.Set("X-Plex-Session-Identifier", r.SessionID)
	q.Set("X-Plex-Client-Identifier", r.ClientID)
	q.Set("X-Plex-Client-Profile-Extra", BuildProfileExtra())
	q.Set("X-Plex-Token", r.Token)
	return r.PlexServerURL + "/video/:/transcode/universal/start.m3u8?" + q.Encode()
}

// pmsMediaContainer is the narrow slice of PMS's /library/metadata response
// that we decode to find subtitle streams. We match by Stream id (streamType
// is the Plex convention for subtitles: 3, but we match by ID per the plan).
type pmsMediaContainer struct {
	Video []struct {
		Media []struct {
			Part []struct {
				Stream []struct {
					ID         string `xml:"id,attr"`
					StreamType string `xml:"streamType,attr"`
					Key        string `xml:"key,attr"`
				} `xml:"Stream"`
			} `xml:"Part"`
		} `xml:"Media"`
	} `xml:"Video"`
}

// SubtitleURLFor queries PMS metadata for mediaKey and returns a URL to the
// subtitle stream whose id matches streamID, token-appended so FFmpeg can
// fetch it directly. Returns an error if the stream isn't found so callers
// can log-and-continue without burn-in.
func SubtitleURLFor(serverURL, mediaKey, streamID, token string) (string, error) {
	u := fmt.Sprintf("%s%s?X-Plex-Token=%s",
		strings.TrimRight(serverURL, "/"),
		mediaKey,
		url.QueryEscape(token))
	resp, err := http.Get(u)
	if err != nil {
		return "", fmt.Errorf("metadata fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var mc pmsMediaContainer
	if err := xml.Unmarshal(body, &mc); err != nil {
		return "", fmt.Errorf("parse metadata: %w", err)
	}
	for _, v := range mc.Video {
		for _, media := range v.Media {
			for _, part := range media.Part {
				for _, s := range part.Stream {
					if s.ID == streamID && s.Key != "" {
						return fmt.Sprintf("%s%s?X-Plex-Token=%s",
							strings.TrimRight(serverURL, "/"),
							s.Key,
							url.QueryEscape(token)), nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("subtitle stream %q not found under %s", streamID, mediaKey)
}
