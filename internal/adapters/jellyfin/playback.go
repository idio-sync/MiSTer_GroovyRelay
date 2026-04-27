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

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
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

// playRequestInput aggregates everything needed to build a single
// core.SessionRequest. Used by HandlePlay (Phase 6) and by the
// track-switching code (Phase 8).
type playRequestInput struct {
	ItemID             string
	StartPositionTicks int64
	PlayInfo           PlaybackInfoResult
	ServerURL          string
	Token              string
}

// buildSessionRequest assembles a core.SessionRequest from the
// playback negotiation result. The OnStop closure captures the
// adapter-internal "<itemId>:<playSessionId>" key so the elision
// logic in the reporter can identity-check against the adapter's
// current key.
func (a *Adapter) buildSessionRequest(in playRequestInput) core.SessionRequest {
	refKey := in.ItemID + ":" + in.PlayInfo.PlaySessionID
	return core.SessionRequest{
		StreamURL:     BuildAbsoluteStreamURL(in.ServerURL, in.PlayInfo.TranscodingURL, in.Token),
		InputHeaders:  nil,
		SeekOffsetMs:  int(in.StartPositionTicks / 10_000),
		SubtitleURL:   "",
		SubtitlePath:  "",
		SubtitleIndex: 0,
		Capabilities:  core.Capabilities{CanSeek: true, CanPause: true},
		AdapterRef:    refKey,
		DirectPlay:    false,
		OnStop:        a.makeOnStop(refKey),
	}
}

// makeOnStop returns the OnStop closure to attach to a SessionRequest.
// On any reason the closure: records errReason if reason=="error",
// wakes the reporter so it doesn't have to wait for its next 10 s tick.
// The reporter does the actual termination classification.
func (a *Adapter) makeOnStop(refKey string) func(string) {
	return func(reason string) {
		a.mu.Lock()
		r, ok := a.reporters[refKey]
		if !ok {
			a.mu.Unlock()
			return
		}
		if reason == "error" {
			r.errReason = reason
		}
		ch := r.wakeup
		a.mu.Unlock()

		// Non-blocking wake. Send outside the mutex so a slow reader
		// can't stall any unrelated state mutation.
		if ch != nil {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// beginSelfPreempt updates currentRefKey under Adapter.mu and returns
// the prior value (so the caller can pass it to rollbackSelfPreempt
// on StartSession error).
func (a *Adapter) beginSelfPreempt(newRef string) (prev string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev = a.currentRefKey
	a.currentRefKey = newRef
	a.pendingRollback = prev
	return prev
}

// rollbackSelfPreempt reverts currentRefKey to prev and clears
// pendingRollback. Called when StartSession returns an error.
func (a *Adapter) rollbackSelfPreempt(prev string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentRefKey = prev
	a.pendingRollback = ""
}

// commitSelfPreempt clears pendingRollback after StartSession
// succeeds. currentRefKey is left at the new value.
func (a *Adapter) commitSelfPreempt() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingRollback = ""
}

// snapshotCurrentRefKey returns currentRefKey under Adapter.mu.
// Used by the reporter on each tick.
func (a *Adapter) snapshotCurrentRefKey() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentRefKey
}
