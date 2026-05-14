package url

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	stdurl "net/url"
	"path"
	"strings"
	"time"
)

const owncastProbeTimeout = 1500 * time.Millisecond

var owncastProbeHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type owncastStatusResponse struct {
	ServerTime    string `json:"serverTime"`
	VersionNumber string `json:"versionNumber"`
	Online        *bool  `json:"online"`
}

func resolveOwncastHomepageURL(ctx context.Context, parsed *stdurl.URL) (string, bool) {
	if !isOwncastProbeCandidate(parsed) {
		return "", false
	}
	statusURL := owncastStatusURL(parsed)
	reqCtx, cancel := context.WithTimeout(ctx, owncastProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, statusURL, nil)
	if err != nil {
		return "", false
	}
	resp, err := owncastProbeHTTPClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", false
	}
	var status owncastStatusResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&status); err != nil {
		return "", false
	}
	if !status.looksLikeOwncast() {
		return "", false
	}
	return owncastHLSURL(parsed), true
}

func isOwncastProbeCandidate(u *stdurl.URL) bool {
	if u == nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	// Owncast public pages are normally extension-free. Treat paths with an
	// extension as direct media or ordinary files so explicit HLS/media URLs
	// stay on the existing FFmpeg path without an extra probe.
	ext := strings.ToLower(path.Ext(u.EscapedPath()))
	if ext != "" {
		return false
	}
	return true
}

func owncastStatusURL(u *stdurl.URL) string {
	probe := *u
	probe.Path = "/api/status"
	probe.RawPath = ""
	probe.RawQuery = ""
	probe.Fragment = ""
	return probe.String()
}

func owncastHLSURL(u *stdurl.URL) string {
	hls := *u
	hls.Path = "/hls/stream.m3u8"
	hls.RawPath = ""
	hls.RawQuery = ""
	hls.Fragment = ""
	return hls.String()
}

func (s owncastStatusResponse) looksLikeOwncast() bool {
	return strings.TrimSpace(s.ServerTime) != "" &&
		strings.TrimSpace(s.VersionNumber) != "" &&
		s.Online != nil
}
