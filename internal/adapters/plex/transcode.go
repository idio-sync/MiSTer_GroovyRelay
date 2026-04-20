package plex

import (
	"fmt"
	"net/url"
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
