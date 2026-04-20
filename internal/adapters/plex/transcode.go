package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// plexHTTPClient is the shared HTTP client for PMS + plex.tv requests.
// 10 s timeout bounds every network call; the bridge must never wait on a
// hung remote under a caller that holds a mutex or drives a ticker.
// Declared as a var so tests can swap in a faster client.
var plexHTTPClient = &http.Client{Timeout: 10 * time.Second}

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

// sanitizeSessionID restricts the input to characters safe for use as a
// filename component — [A-Za-z0-9._-], non-empty, length <= 128. Plex
// controllers generate session IDs using these characters in practice,
// but the HTTP endpoint is unauthenticated so the SessionID parameter is
// attacker-controlled. Rejecting anything outside the safe set prevents
// path-traversal (../, absolute paths) and command-line injection via
// the FFmpeg filtergraph.
func sanitizeSessionID(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("sessionID: empty")
	}
	if len(s) > 128 {
		return "", fmt.Errorf("sessionID: too long (%d > 128)", len(s))
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			// ok
		default:
			return "", fmt.Errorf("sessionID: invalid character %q", r)
		}
	}
	return s, nil
}

// FetchSubtitleToFile downloads the subtitle resource at srtURL (the token-
// bearing URL returned by SubtitleURLFor) to a file under
// <dataDir>/subtitles/<sessionID>.<ext>. The extension is derived from the
// HTTP Content-Type header: `text/x-ssa` or `text/x-ass` → `.ass`,
// everything else → `.srt` (SubRip is the format PMS defaults to).
//
// Returns the absolute file path. The caller is responsible for removing
// the file when the session ends.
//
// Uses the 10 s-timeout HTTP client so a stuck PMS doesn't wedge session
// start.
func FetchSubtitleToFile(ctx context.Context, srtURL, dataDir, sessionID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srtURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("subtitle fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("subtitle fetch: %s", resp.Status)
	}
	ext := ".srt"
	switch ct := resp.Header.Get("Content-Type"); {
	case strings.HasPrefix(ct, "text/x-ssa"), strings.HasPrefix(ct, "text/x-ass"):
		ext = ".ass"
	}
	dir := filepath.Join(dataDir, "subtitles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	safeID, err := sanitizeSessionID(sessionID)
	if err != nil {
		return "", fmt.Errorf("subtitle fetch: %w", err)
	}
	path := filepath.Join(dir, safeID+ext)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
