package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PlaybackInfoInput is the request payload for FetchPlaybackInfo.
type PlaybackInfoInput struct {
	ServerURL           string
	Token               string
	DeviceID            string
	Version             string
	ItemID              string
	UserID              string
	MaxVideoBitrateKbps int
	StartPositionTicks  int64
	MediaSourceID       string // optional
	AudioStreamIndex    *int   // optional
	SubtitleStreamIndex *int   // optional
}

// PlaybackInfoResult is the relevant slice of JF's
// PlaybackInfoResponse — what the caller needs to start a session.
type PlaybackInfoResult struct {
	MediaSourceID  string
	PlaySessionID  string
	TranscodingURL string // relative path with query string
}

type playbackInfoBody struct {
	UserID                              string        `json:"UserId"`
	MaxStreamingBitrate                 int           `json:"MaxStreamingBitrate"`
	StartTimeTicks                      int64         `json:"StartTimeTicks,omitempty"`
	AudioStreamIndex                    *int          `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex                 *int          `json:"SubtitleStreamIndex,omitempty"`
	MediaSourceID                       string        `json:"MediaSourceId,omitempty"`
	EnableDirectPlay                    bool          `json:"EnableDirectPlay"`
	EnableDirectStream                  bool          `json:"EnableDirectStream"`
	EnableTranscoding                   bool          `json:"EnableTranscoding"`
	AlwaysBurnInSubtitleWhenTranscoding bool          `json:"AlwaysBurnInSubtitleWhenTranscoding"`
	DeviceProfile                       DeviceProfile `json:"DeviceProfile"`
}

type playbackInfoResponseDTO struct {
	MediaSources []struct {
		ID             string `json:"Id"`
		TranscodingURL string `json:"TranscodingUrl"`
	} `json:"MediaSources"`
	PlaySessionID string `json:"PlaySessionId"`
	ErrorCode     string `json:"ErrorCode"`
}

// FetchPlaybackInfo POSTs /Items/{ItemId}/PlaybackInfo and returns
// the negotiated MediaSourceId / PlaySessionId / relative
// TranscodingUrl. The caller uses BuildAbsoluteStreamURL to convert
// the relative URL into an ffmpeg-consumable absolute URL.
//
// Returns an error if the server returns ErrorCode (NotAllowed /
// NoCompatibleStream / RateLimitExceeded) or HTTP non-2xx.
func FetchPlaybackInfo(ctx context.Context, in PlaybackInfoInput) (PlaybackInfoResult, error) {
	body := playbackInfoBody{
		UserID:                              in.UserID,
		MaxStreamingBitrate:                 in.MaxVideoBitrateKbps * 1000,
		StartTimeTicks:                      in.StartPositionTicks,
		AudioStreamIndex:                    in.AudioStreamIndex,
		SubtitleStreamIndex:                 in.SubtitleStreamIndex,
		MediaSourceID:                       in.MediaSourceID,
		EnableDirectPlay:                    false,
		EnableDirectStream:                  false,
		EnableTranscoding:                   true,
		AlwaysBurnInSubtitleWhenTranscoding: true,
		DeviceProfile:                       BuildDeviceProfile(in.MaxVideoBitrateKbps),
	}
	data, err := json.Marshal(body)
	if err != nil {
		return PlaybackInfoResult{}, fmt.Errorf("jellyfin: marshal PlaybackInfo body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(in.ServerURL, "/")+"/Items/"+url.PathEscape(in.ItemID)+"/PlaybackInfo",
		bytes.NewReader(data))
	if err != nil {
		return PlaybackInfoResult{}, fmt.Errorf("jellyfin: build PlaybackInfo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", BuildAuthHeader(AuthHeaderInput{
		Token: in.Token, Client: "MiSTer_GroovyRelay", Device: "MiSTer",
		DeviceID: in.DeviceID, Version: in.Version,
	}))

	resp, err := jfHTTPClient.Do(req)
	if err != nil {
		return PlaybackInfoResult{}, fmt.Errorf("jellyfin: PlaybackInfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return PlaybackInfoResult{}, fmt.Errorf("jellyfin: PlaybackInfo: HTTP %d", resp.StatusCode)
	}

	var dto playbackInfoResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return PlaybackInfoResult{}, fmt.Errorf("jellyfin: decode PlaybackInfo: %w", err)
	}
	if dto.ErrorCode != "" {
		return PlaybackInfoResult{}, fmt.Errorf("jellyfin: PlaybackInfo error: %s", dto.ErrorCode)
	}
	if len(dto.MediaSources) == 0 {
		return PlaybackInfoResult{}, errors.New("jellyfin: PlaybackInfo returned no MediaSources")
	}
	src := dto.MediaSources[0]
	return PlaybackInfoResult{
		MediaSourceID:  src.ID,
		PlaySessionID:  dto.PlaySessionID,
		TranscodingURL: src.TranscodingURL,
	}, nil
}

// BuildAbsoluteStreamURL converts a relative TranscodingUrl into an
// absolute, ffmpeg-consumable URL by prefixing the server base and
// appending api_key=<token> only if the relative URL doesn't already
// have one. JF transcoding URLs typically already contain api_key
// (the JF web client embeds it); the conditional avoids dup'ing.
func BuildAbsoluteStreamURL(serverURL, relativeURL, token string) string {
	abs := strings.TrimRight(serverURL, "/") + relativeURL
	u, err := url.Parse(abs)
	if err != nil {
		return abs
	}
	q := u.Query()
	if q.Get("api_key") == "" {
		q.Set("api_key", token)
		u.RawQuery = q.Encode()
	}
	return u.String()
}
