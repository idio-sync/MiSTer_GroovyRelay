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
	"time"
)

// dotNetUnixEpochTicks is the number of 100ns ticks between
// 0001-01-01 00:00:00 UTC (.NET DateTime epoch) and 1970-01-01 (Unix
// epoch). JF's PlaybackStartTimeTicks is .NET ticks, not unix-ns/100.
const dotNetUnixEpochTicks int64 = 621_355_968_000_000_000

// dotNetTicks converts a Go time.Time to .NET DateTime ticks (100ns
// intervals since 0001-01-01 UTC).
func dotNetTicks(t time.Time) int64 {
	return dotNetUnixEpochTicks + t.UnixNano()/100
}

// RESTAuth bundles the identity carried on every authenticated JF
// REST call so each helper sends the same MediaBrowser header that
// Capabilities, PlaybackInfo, and the WebSocket handshake send.
type RESTAuth struct {
	ServerURL  string
	Token      string
	DeviceID   string
	DeviceName string
	Version    string
}

func (a RESTAuth) authHeader() string {
	return BuildAuthHeader(AuthHeaderInput{
		Token:    a.Token,
		Client:   jfClientName,
		Device:   effectiveDeviceName(a.DeviceName),
		DeviceID: a.DeviceID,
		Version:  a.Version,
	})
}

// QueueItem is one entry in NowPlayingQueue. The PlaylistItemId is
// adapter-assigned (we use the JF item id; JF's own clients use a
// per-queue ULID, but echoing the item id is also accepted).
type QueueItem struct {
	ID             string `json:"Id"`
	PlaylistItemID string `json:"PlaylistItemId,omitempty"`
}

// PlaybackProgressInfo is the REST body for both /Sessions/Playing
// and /Sessions/Playing/Progress (JF's PlaybackStartInfo derives from
// PlaybackProgressInfo with no added fields). Pointer fields use
// omitempty so we never claim "stream 0 selected" when nothing was
// ever set.
type PlaybackProgressInfo struct {
	ItemID                 string      `json:"ItemId"`
	SessionID              string      `json:"SessionId,omitempty"`
	MediaSourceID          string      `json:"MediaSourceId"`
	PlaySessionID          string      `json:"PlaySessionId"`
	PositionTicks          *int64      `json:"PositionTicks,omitempty"`
	PlaybackStartTimeTicks *int64      `json:"PlaybackStartTimeTicks,omitempty"`
	VolumeLevel            *int        `json:"VolumeLevel,omitempty"`
	AudioStreamIndex       *int        `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex    *int        `json:"SubtitleStreamIndex,omitempty"`
	IsPaused               bool        `json:"IsPaused"`
	IsMuted                bool        `json:"IsMuted"`
	PlayMethod             string      `json:"PlayMethod"`
	LiveStreamID           string      `json:"LiveStreamId,omitempty"`
	RepeatMode             string      `json:"RepeatMode"`
	PlaybackOrder          string      `json:"PlaybackOrder"`
	CanSeek                bool        `json:"CanSeek"`
	NowPlayingQueue        []QueueItem `json:"NowPlayingQueue,omitempty"`
	PlaylistItemID         string      `json:"PlaylistItemId,omitempty"`
}

// PlaybackStartInfo is the body of POST /Sessions/Playing. JF's
// PlaybackStartInfo extends PlaybackProgressInfo with no new fields,
// so we type-alias to keep call sites self-documenting.
type PlaybackStartInfo = PlaybackProgressInfo

// PlaybackStopInfo is the body of POST /Sessions/Playing/Stopped. JF
// defines this as a separate, slimmer schema (no IsPaused, no volume,
// adds Failed) — extra fields would be ignored but using the right
// shape is cleaner.
type PlaybackStopInfo struct {
	ItemID          string      `json:"ItemId"`
	SessionID       string      `json:"SessionId,omitempty"`
	MediaSourceID   string      `json:"MediaSourceId,omitempty"`
	PlaySessionID   string      `json:"PlaySessionId,omitempty"`
	PositionTicks   *int64      `json:"PositionTicks,omitempty"`
	LiveStreamID    string      `json:"LiveStreamId,omitempty"`
	Failed          bool        `json:"Failed"`
	PlaylistItemID  string      `json:"PlaylistItemId,omitempty"`
	NowPlayingQueue []QueueItem `json:"NowPlayingQueue,omitempty"`
}

// httpStatusErr is returned by postJSON when JF responds with a
// non-2xx status. Used by isTransient to decide whether to retry.
type httpStatusErr struct {
	Status int
	Path   string
}

func (e *httpStatusErr) Error() string {
	return fmt.Sprintf("jellyfin: %s: HTTP %d", e.Path, e.Status)
}

// isTransient classifies an error from postJSON as worth one retry.
// 5xx and transport errors are transient; 4xx is not (the server has
// rejected the body shape and a retry won't fix it).
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	var hse *httpStatusErr
	if errors.As(err, &hse) {
		return hse.Status >= 500
	}
	return true
}

func postJSON(ctx context.Context, auth RESTAuth, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("jellyfin: marshal %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(auth.ServerURL, "/")+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("jellyfin: build %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth.authHeader())
	resp, err := jfHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin: %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusErr{Status: resp.StatusCode, Path: path}
	}
	return nil
}

// postPlaybackStart fires PlaybackStart to JF. One-shot — if it fails,
// the next progress emit supersedes it and JF will pick up the session
// from the first successful Progress call.
func postPlaybackStart(ctx context.Context, auth RESTAuth, body PlaybackStartInfo) error {
	return postJSON(ctx, auth, "/Sessions/Playing", body)
}

// postPlaybackProgress fires a periodic progress update to JF.
// Fire-and-forget; subsequent ticks supersede a lost update.
func postPlaybackProgress(ctx context.Context, auth RESTAuth, body PlaybackProgressInfo) error {
	return postJSON(ctx, auth, "/Sessions/Playing/Progress", body)
}

// postPlaybackStopped fires the terminal report. Losing this leaves
// JF showing a stuck session, so a single 500ms-spaced retry on
// transient failure (network error or 5xx) is worth the latency.
func postPlaybackStopped(ctx context.Context, auth RESTAuth, body PlaybackStopInfo) error {
	err := postJSON(ctx, auth, "/Sessions/Playing/Stopped", body)
	if err == nil || !isTransient(err) {
		return err
	}
	select {
	case <-ctx.Done():
		return err
	case <-time.After(500 * time.Millisecond):
	}
	return postJSON(ctx, auth, "/Sessions/Playing/Stopped", body)
}

// postPlaybackPing keeps JF's transcoder alive when no other progress
// is flowing (e.g. WS reconnect, paused for a long time). JF's
// /Sessions/Playing/Ping endpoint resets the transcoder's idle timer.
func postPlaybackPing(ctx context.Context, auth RESTAuth, playSessionID string) error {
	q := url.Values{}
	q.Set("playSessionId", playSessionID)
	u := strings.TrimRight(auth.ServerURL, "/") + "/Sessions/Playing/Ping?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth.authHeader())
	resp, err := jfHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusErr{Status: resp.StatusCode, Path: "/Sessions/Playing/Ping"}
	}
	return nil
}
