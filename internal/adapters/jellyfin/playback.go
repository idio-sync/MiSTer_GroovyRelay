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

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// PlaybackInfoInput is the request payload for FetchPlaybackInfo.
type PlaybackInfoInput struct {
	ServerURL           string
	Token               string
	DeviceID            string
	DeviceName          string
	Version             string
	ItemID              string
	UserID              string
	MaxVideoBitrateKbps int
	Preset              core.ModelinePreset
	StartPositionTicks  int64
	MediaSourceID       string // optional
	AudioStreamIndex    *int   // optional
	SubtitleStreamIndex *int   // optional
	MediaKind           core.MediaKind
}

// PlaybackInfoResult is the relevant slice of JF's
// PlaybackInfoResponse — what the caller needs to start a session.
type PlaybackInfoResult struct {
	MediaSourceID  string
	PlaySessionID  string
	TranscodingURL string // relative path with query string
	Title          string // human title for status/eventlog; empty if unavailable
	MediaKind      core.MediaKind
	ItemType       string
	Artist         string
	Album          string
	Duration       time.Duration
	ArtworkPath    string
	SeriesName     string
	Season         int
	Episode        int
	Year           int
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
		Name           string `json:"Name"`
		TranscodingURL string `json:"TranscodingUrl"`
	} `json:"MediaSources"`
	PlaySessionID string `json:"PlaySessionId"`
	ErrorCode     string `json:"ErrorCode"`
	Item          struct {
		Type              string   `json:"Type"`
		Name              string   `json:"Name"`
		Artists           []string `json:"Artists"`
		Album             string   `json:"Album"`
		RunTimeTicks      int64    `json:"RunTimeTicks"`
		SeriesName        string   `json:"SeriesName"`
		IndexNumber       int      `json:"IndexNumber"`
		ParentIndexNumber int      `json:"ParentIndexNumber"`
		ProductionYear    int      `json:"ProductionYear"`
	} `json:"Item"`
}

type playbackInfoServerError struct {
	code string
}

func (e playbackInfoServerError) Error() string {
	return fmt.Sprintf("jellyfin: PlaybackInfo error: %s", e.code)
}

// ItemMetadataInput is the request payload for FetchItemMetadata.
type ItemMetadataInput struct {
	ServerURL  string
	Token      string
	DeviceID   string
	DeviceName string
	Version    string
	ItemID     string
	UserID     string
}

// ItemMetadataResult carries Jellyfin item metadata used to hint audio
// PlaybackInfo requests and fill music visualizer metadata when
// PlaybackInfo omits it.
type ItemMetadataResult struct {
	ItemID      string
	Type        string
	MediaKind   core.MediaKind
	Title       string
	Artist      string
	Album       string
	Duration    time.Duration
	ArtworkPath string
	SeriesName  string
	Season      int
	Episode     int
	Year        int
}

type itemMetadataDTO struct {
	ID                string   `json:"Id"`
	Type              string   `json:"Type"`
	Name              string   `json:"Name"`
	Artists           []string `json:"Artists"`
	Album             string   `json:"Album"`
	RunTimeTicks      int64    `json:"RunTimeTicks"`
	SeriesName        string   `json:"SeriesName"`
	IndexNumber       int      `json:"IndexNumber"`
	ParentIndexNumber int      `json:"ParentIndexNumber"`
	ProductionYear    int      `json:"ProductionYear"`
}

// FetchPlaybackInfo POSTs /Items/{ItemId}/PlaybackInfo and returns
// the negotiated MediaSourceId / PlaySessionId / relative
// TranscodingUrl. The caller uses BuildAbsoluteStreamURL to convert
// the relative URL into an ffmpeg-consumable absolute URL.
//
// Returns an error if the server returns ErrorCode (NotAllowed /
// NoCompatibleStream / RateLimitExceeded) or HTTP non-2xx.
func FetchPlaybackInfo(ctx context.Context, in PlaybackInfoInput) (PlaybackInfoResult, error) {
	res, err := fetchPlaybackInfoOnce(ctx, in)
	if err == nil {
		if in.MediaKind != core.MediaKindMusic && isJellyfinAudioType(res.ItemType) {
			audioIn := in
			audioIn.MediaKind = core.MediaKindMusic
			return fetchPlaybackInfoOnce(ctx, audioIn)
		}
		return res, nil
	}

	var serverErr playbackInfoServerError
	if in.MediaKind != core.MediaKindMusic && errors.As(err, &serverErr) && serverErr.code == "NoCompatibleStream" {
		audioIn := in
		audioIn.MediaKind = core.MediaKindMusic
		retry, retryErr := fetchPlaybackInfoOnce(ctx, audioIn)
		if retryErr == nil && isJellyfinAudioType(retry.ItemType) {
			return retry, nil
		}
	}
	return PlaybackInfoResult{}, err
}

func fetchPlaybackInfoOnce(ctx context.Context, in PlaybackInfoInput) (PlaybackInfoResult, error) {
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
		DeviceProfile:                       playbackDeviceProfile(in),
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
		Token: in.Token, Client: jfClientName, Device: effectiveDeviceName(in.DeviceName),
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
		return PlaybackInfoResult{}, playbackInfoServerError{code: dto.ErrorCode}
	}
	if len(dto.MediaSources) == 0 {
		return PlaybackInfoResult{}, errors.New("jellyfin: PlaybackInfo returned no MediaSources")
	}
	src := dto.MediaSources[0]
	title := dto.Item.Name
	if title == "" {
		title = src.Name
	}
	if title == "" {
		title = in.ItemID
	}
	if title == "" {
		title = "Now Playing"
	}
	mediaKind := core.MediaKindVideo
	if in.MediaKind == core.MediaKindMusic || isJellyfinAudioType(dto.Item.Type) {
		mediaKind = core.MediaKindMusic
	}
	return PlaybackInfoResult{
		MediaSourceID:  src.ID,
		PlaySessionID:  dto.PlaySessionID,
		TranscodingURL: src.TranscodingURL,
		Title:          title,
		MediaKind:      mediaKind,
		ItemType:       dto.Item.Type,
		Artist:         strings.Join(dto.Item.Artists, ", "),
		Album:          dto.Item.Album,
		Duration:       durationFromJellyfinTicks(dto.Item.RunTimeTicks),
		SeriesName:     dto.Item.SeriesName,
		Season:         dto.Item.ParentIndexNumber,
		Episode:        dto.Item.IndexNumber,
		Year:           dto.Item.ProductionYear,
	}, nil
}

func playbackDeviceProfile(in PlaybackInfoInput) DeviceProfile {
	if in.MediaKind == core.MediaKindMusic {
		return BuildAudioDeviceProfile(in.MaxVideoBitrateKbps)
	}
	return BuildDeviceProfile(in.MaxVideoBitrateKbps, in.Preset)
}

// FetchItemMetadata fetches /Items/{ItemId} metadata. Callers use the result
// best-effort: metadata failures should not block playback because
// PlaybackInfo may still include enough information to start a session.
func FetchItemMetadata(ctx context.Context, in ItemMetadataInput) (ItemMetadataResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(in.ServerURL, "/")+"/Items/"+url.PathEscape(in.ItemID),
		nil)
	if err != nil {
		return ItemMetadataResult{}, fmt.Errorf("jellyfin: build item metadata request: %w", err)
	}
	q := req.URL.Query()
	if in.UserID != "" {
		q.Set("UserId", in.UserID)
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Set("Authorization", BuildAuthHeader(AuthHeaderInput{
		Token: in.Token, Client: jfClientName, Device: effectiveDeviceName(in.DeviceName),
		DeviceID: in.DeviceID, Version: in.Version,
	}))

	resp, err := jfHTTPClient.Do(req)
	if err != nil {
		return ItemMetadataResult{}, fmt.Errorf("jellyfin: item metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ItemMetadataResult{}, fmt.Errorf("jellyfin: item metadata: HTTP %d", resp.StatusCode)
	}

	var dto itemMetadataDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return ItemMetadataResult{}, fmt.Errorf("jellyfin: decode item metadata: %w", err)
	}
	return itemMetadataFromDTO(dto), nil
}

func itemMetadataFromDTO(dto itemMetadataDTO) ItemMetadataResult {
	kind := core.MediaKindVideo
	if isJellyfinAudioType(dto.Type) {
		kind = core.MediaKindMusic
	}
	return ItemMetadataResult{
		ItemID:     dto.ID,
		Type:       dto.Type,
		MediaKind:  kind,
		Title:      dto.Name,
		Artist:     strings.Join(dto.Artists, ", "),
		Album:      dto.Album,
		Duration:   durationFromJellyfinTicks(dto.RunTimeTicks),
		SeriesName: dto.SeriesName,
		Season:     dto.ParentIndexNumber,
		Episode:    dto.IndexNumber,
		Year:       dto.ProductionYear,
	}
}

func mergePlaybackMetadata(info PlaybackInfoResult, meta ItemMetadataResult) PlaybackInfoResult {
	if info.MediaKind != core.MediaKindMusic && meta.MediaKind == core.MediaKindMusic {
		info.MediaKind = core.MediaKindMusic
	}
	if meta.Title != "" && (info.Title == "" || info.Title == meta.ItemID) {
		info.Title = meta.Title
	}
	if info.Artist == "" {
		info.Artist = meta.Artist
	}
	if info.Album == "" {
		info.Album = meta.Album
	}
	if info.Duration == 0 {
		info.Duration = meta.Duration
	}
	if info.ArtworkPath == "" {
		info.ArtworkPath = meta.ArtworkPath
	}
	if info.SeriesName == "" {
		info.SeriesName = meta.SeriesName
	}
	if info.Season == 0 {
		info.Season = meta.Season
	}
	if info.Episode == 0 {
		info.Episode = meta.Episode
	}
	if info.Year == 0 {
		info.Year = meta.Year
	}
	return info
}

func isJellyfinAudioType(itemType string) bool {
	return strings.EqualFold(itemType, "Audio")
}

func durationFromJellyfinTicks(ticks int64) time.Duration {
	if ticks <= 0 {
		return 0
	}
	return time.Duration(ticks) * 100 * time.Nanosecond
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

// jellyfinDisplayMetadata maps a negotiated PlaybackInfoResult onto the
// three VFD tiers. Episode: show-first. Movie: title + year. Audio:
// title/artist/album. Unknown types: title only.
func jellyfinDisplayMetadata(info PlaybackInfoResult) core.DisplayMetadata {
	switch {
	case strings.EqualFold(info.ItemType, "Episode"):
		primary := info.SeriesName
		if primary == "" {
			primary = info.Title
		}
		return core.DisplayMetadata{
			Primary:   primary,
			Secondary: info.Title,
			Tertiary:  adapters.FormatSeasonEpisode(info.Season, info.Episode, info.Year),
		}
	case strings.EqualFold(info.ItemType, "Movie"):
		return core.DisplayMetadata{
			Primary:   info.Title,
			Secondary: adapters.FormatSeasonEpisode(0, 0, info.Year),
		}
	case info.MediaKind == core.MediaKindMusic || strings.EqualFold(info.ItemType, "Audio"):
		return core.DisplayMetadata{Primary: info.Title, Secondary: info.Artist, Tertiary: info.Album}
	default:
		return core.DisplayMetadata{Primary: info.Title}
	}
}

// buildSessionRequest assembles a core.SessionRequest from the
// playback negotiation result. The OnStop closure captures the
// adapter-internal "<itemId>:<playSessionId>" key so the elision
// logic in the reporter can identity-check against the adapter's
// current key.
func (a *Adapter) buildSessionRequest(in playRequestInput) core.SessionRequest {
	refKey := in.ItemID + ":" + in.PlayInfo.PlaySessionID
	req := core.SessionRequest{
		StreamURL:       BuildAbsoluteStreamURL(in.ServerURL, in.PlayInfo.TranscodingURL, in.Token),
		InputHeaders:    nil,
		SeekOffsetMs:    int(in.StartPositionTicks / 10_000),
		SubtitleURL:     "",
		SubtitlePath:    "",
		SubtitleIndex:   0,
		Capabilities:    core.Capabilities{CanSeek: true, CanPause: true},
		AdapterRef:      refKey,
		Source:          "jellyfin",
		DirectPlay:      false,
		OnStop:          artworkcache.WithCleanup(in.PlayInfo.ArtworkPath, a.makeOnStop(refKey)),
		Title:           in.PlayInfo.Title,
		DisplayMetadata: jellyfinDisplayMetadata(in.PlayInfo),
	}
	if in.PlayInfo.MediaKind == core.MediaKindMusic {
		req.MediaKind = core.MediaKindMusic
		req.Visualizer = core.VisualizerRequest{
			Enabled: true,
			Mode:    core.VisualizerModeRetroAnalyzer,
			Metadata: core.VisualizerMetadata{
				Title:       in.PlayInfo.Title,
				Artist:      in.PlayInfo.Artist,
				Album:       in.PlayInfo.Album,
				Duration:    in.PlayInfo.Duration,
				ArtworkPath: in.PlayInfo.ArtworkPath,
			},
		}
	}
	return req
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
