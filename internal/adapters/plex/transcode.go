package plex

import (
	"context"
	"crypto/rand"
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
// MaxBitrate is in kbps; defaults to 1500 when zero. The lower default gives
// PMS less room to preserve high-motion detail that later turns into large,
// poorly-compressible raw fields on the MiSTer side.
type TranscodeRequest struct {
	PlexServerURL string
	MediaPath     string
	Token         string
	OffsetMs      int
	OutputWidth   int
	OutputHeight  int
	SessionID     string
	ClientID      string
	DeviceName    string
	ProfileName   string
	Product       string
	Platform      string
	Version       string
	Provides      string
	MaxBitrate    int
	AudioStreamID    string
	SubtitleStreamID string
	// TranscodeSessionID is the PMS transcoder session UUID documented as the
	// transcodeSessionId query parameter. It is distinct from
	// X-Plex-Session-Identifier, which identifies the client playback session.
	TranscodeSessionID string
}

func NewTranscodeSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// BuildTranscodeURL constructs a PMS /video/:/transcode/universal/start.m3u8
// URL with all the parameters PMS needs to force a server-side transcode to
// our target profile (480p H.264, no direct play/stream). The returned URL
// is what FFmpeg consumes as its -i input.
func BuildTranscodeURL(r TranscodeRequest) string {
	if r.MaxBitrate == 0 {
		r.MaxBitrate = 1500
	}
	if r.ProfileName == "" {
		r.ProfileName = "Plex Home Theater"
	}
	if r.Product == "" {
		r.Product = companionProduct
	}
	if r.Platform == "" {
		r.Platform = companionPlatform
	}
	if r.Provides == "" {
		r.Provides = companionProvides
	}
	q := url.Values{}
	q.Set("path", r.MediaPath)
	q.Set("mediaIndex", "0")
	q.Set("partIndex", "0")
	q.Set("protocol", "hls")
	q.Set("fastSeek", "1")
	q.Set("directPlay", "0")
	q.Set("directStream", "0")
	q.Set("copyts", "1")
	q.Set("videoResolution", fmt.Sprintf("%dx%d", r.OutputWidth, r.OutputHeight))
	q.Set("maxVideoBitrate", fmt.Sprintf("%d", r.MaxBitrate))
	q.Set("offset", fmt.Sprintf("%d", r.OffsetMs/1000))
	if r.TranscodeSessionID != "" {
		q.Set("transcodeSessionId", r.TranscodeSessionID)
	}
	if r.AudioStreamID != "" {
		q.Set("audioStreamID", r.AudioStreamID)
	}
	if r.SubtitleStreamID != "" {
		q.Set("subtitleStreamID", r.SubtitleStreamID)
		if r.SubtitleStreamID == "0" {
			q.Set("subtitles", "none")
		} else {
			q.Set("subtitles", "burn")
			q.Set("advancedSubtitles", "burn")
		}
	}
	q.Set("X-Plex-Session-Identifier", r.SessionID)
	q.Set("X-Plex-Client-Identifier", r.ClientID)
	q.Set("X-Plex-Device-Name", r.DeviceName)
	q.Set("X-Plex-Product", r.Product)
	q.Set("X-Plex-Platform", r.Platform)
	q.Set("X-Plex-Version", r.Version)
	q.Set("X-Plex-Provides", r.Provides)
	q.Set("X-Plex-Client-Profile-Name", r.ProfileName)
	q.Set("X-Plex-Client-Profile-Extra", BuildProfileExtra())
	q.Set("X-Plex-Client-Capabilities", BuildClientCapabilities())
	q.Set("X-Plex-Token", r.Token)
	return r.PlexServerURL + "/video/:/transcode/universal/start.m3u8?" + q.Encode()
}

func StopTranscodeSession(ctx context.Context, serverURL, transcodeSessionID, token string) error {
	if serverURL == "" || transcodeSessionID == "" {
		return nil
	}
	u := strings.TrimRight(serverURL, "/") + "/transcode/session/" + url.PathEscape(transcodeSessionID)
	reqURL, err := url.Parse(u)
	if err != nil {
		return err
	}
	if token != "" {
		q := reqURL.Query()
		q.Set("X-Plex-Token", token)
		reqURL.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL.String(), nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("stop transcode: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("stop transcode: %s", resp.Status)
	}
	return nil
}

// pmsMediaContainer is the narrow slice of PMS's /library/metadata response
// that we decode to find subtitle streams. We match by Stream id (streamType
// is the Plex convention for subtitles: 3, but we match by ID per the plan).
type pmsMediaContainer struct {
	Video []struct {
		Media []struct {
			Part []struct {
				ID     string `xml:"id,attr"`
				Stream []struct {
					ID         string `xml:"id,attr"`
					StreamType string `xml:"streamType,attr"`
					Key        string `xml:"key,attr"`
				} `xml:"Stream"`
			} `xml:"Part"`
		} `xml:"Media"`
	} `xml:"Video"`
}

func fetchMetadata(ctx context.Context, serverURL, mediaKey, token string) (*pmsMediaContainer, error) {
	u := fmt.Sprintf("%s%s?X-Plex-Token=%s",
		strings.TrimRight(serverURL, "/"),
		mediaKey,
		url.QueryEscape(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metadata fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("metadata fetch: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var mc pmsMediaContainer
	if err := xml.Unmarshal(body, &mc); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	return &mc, nil
}

func PartIDFor(ctx context.Context, serverURL, mediaKey, token string) (string, error) {
	mc, err := fetchMetadata(ctx, serverURL, mediaKey, token)
	if err != nil {
		return "", err
	}
	for _, v := range mc.Video {
		for _, media := range v.Media {
			for _, part := range media.Part {
				if part.ID != "" {
					return part.ID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("part id not found under %s", mediaKey)
}

func SetStreamSelection(ctx context.Context, serverURL, mediaKey, token, audioStreamID, subtitleStreamID string) error {
	if audioStreamID == "" && subtitleStreamID == "" {
		return nil
	}
	partID, err := PartIDFor(ctx, serverURL, mediaKey, token)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/library/parts/%s",
		strings.TrimRight(serverURL, "/"),
		url.PathEscape(partID))
	reqURL, err := url.Parse(u)
	if err != nil {
		return err
	}
	q := reqURL.Query()
	if audioStreamID != "" {
		q.Set("audioStreamID", audioStreamID)
	}
	if subtitleStreamID != "" {
		q.Set("subtitleStreamID", subtitleStreamID)
	}
	q.Set("allParts", "1")
	if token != "" {
		q.Set("X-Plex-Token", token)
	}
	reqURL.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL.String(), nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("set stream selection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("set stream selection: %s", resp.Status)
	}
	return nil
}

// SubtitleURLFor queries PMS metadata for mediaKey and returns a URL to the
// subtitle stream whose id matches streamID, token-appended so FetchSubtitleToFile
// can download it. ctx bounds the metadata fetch; callers should pass a
// context with a bounded deadline (10 s is idiomatic for PMS calls).
func SubtitleURLFor(ctx context.Context, serverURL, mediaKey, streamID, token string) (string, error) {
	mc, err := fetchMetadata(ctx, serverURL, mediaKey, token)
	if err != nil {
		return "", err
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
	// Fail-fast: validate sessionID BEFORE any network I/O or filesystem
	// work so malformed/malicious inputs do not trigger a PMS round-trip.
	safeID, err := sanitizeSessionID(sessionID)
	if err != nil {
		return "", fmt.Errorf("subtitle fetch: %w", err)
	}
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
