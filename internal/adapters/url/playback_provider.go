package url

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func (a *Adapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	if snap.Source != "url" && !strings.HasPrefix(snap.AdapterRef, "url:") {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	view := adapters.PlaybackBannerAdapterView{SourceDisplay: "URL"}
	if snap.Title != "" {
		view.Title = snap.Title
	}
	if snap.State == core.StatePaused {
		view.Actions = append(view.Actions, adapters.PlaybackAction{ID: adapters.PlaybackActionResume, Label: "Resume", Icon: "play", Enabled: true})
	} else if snap.State == core.StatePlaying {
		view.Actions = append(view.Actions, adapters.PlaybackAction{ID: adapters.PlaybackActionPause, Label: "Pause", Icon: "pause", Enabled: true})
	}
	if snap.State == core.StatePlaying || snap.State == core.StatePaused {
		view.Actions = append(view.Actions,
			adapters.PlaybackAction{ID: adapters.PlaybackActionStop, Label: "Stop", Icon: "stop", Enabled: true},
			adapters.PlaybackAction{ID: adapters.PlaybackActionReplay, Label: "Replay", Icon: "replay", Enabled: true},
		)
	}
	if snap.Duration > 0 {
		view.Seek = &adapters.PlaybackSeek{
			Enabled:    true,
			OffsetMS:   int(snap.Position / time.Millisecond),
			DurationMS: int(snap.Duration / time.Millisecond),
		}
	}
	return view, true
}

func (a *Adapter) HandlePlaybackAction(ctx context.Context, action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if a.core == nil {
		return adapters.PlaybackActionResult{}, fmt.Errorf("core not wired")
	}
	switch action.Action {
	case adapters.PlaybackActionPause:
		return a.pauseBanner(action)
	case adapters.PlaybackActionResume:
		return a.resumeBanner(ctx, action)
	case adapters.PlaybackActionStop:
		return a.stopBanner(action)
	case adapters.PlaybackActionSeek:
		return a.seekBanner(action)
	case adapters.PlaybackActionReplay:
		return a.replayBanner(ctx, action)
	default:
		return adapters.PlaybackActionResult{}, adapters.UnsupportedPlaybackActionError(fmt.Sprintf("unknown playback action %q", action.Action))
	}
}

func (a *Adapter) pauseBanner(action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	matched, err := a.core.PauseIfSession(action.AdapterRef, action.Generation)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	return adapters.PlaybackActionResult{Message: "paused"}, nil
}

func (a *Adapter) stopBanner(action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	matched, err := a.core.StopIfSession(action.AdapterRef, action.Generation)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	return adapters.PlaybackActionResult{Message: "stopped"}, nil
}

func (a *Adapter) seekBanner(action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if action.OffsetMS < 0 {
		action.OffsetMS = 0
	}
	matched, err := a.core.SeekToIfSession(action.AdapterRef, action.Generation, action.OffsetMS)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	return adapters.PlaybackActionResult{Message: "seeked"}, nil
}

func (a *Adapter) resumeBanner(ctx context.Context, action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	st := a.core.Status()
	if st.AdapterRef != action.AdapterRef || st.Generation != action.Generation {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	if st.Duration > 0 {
		matched, err := a.core.PlayIfSession(action.AdapterRef, action.Generation)
		if err != nil {
			return adapters.PlaybackActionResult{}, err
		}
		if !matched {
			return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
		}
		return adapters.PlaybackActionResult{Message: "resumed"}, nil
	}
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		return adapters.PlaybackActionResult{}, fmt.Errorf("no URL to resume")
	}
	ref, _, _, err := a.castURLGuarded(ctx, lastURL, "auto", action.AdapterRef, action.Generation)
	if err != nil {
		redacted := redactErr(err, lastURL)
		if redacted == adapters.ErrActiveSessionChangedMessage {
			return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
		}
		return adapters.PlaybackActionResult{}, fmt.Errorf("%s", redacted)
	}
	return adapters.PlaybackActionResult{Message: "resumed " + ref}, nil
}

func (a *Adapter) replayBanner(ctx context.Context, action adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		return adapters.PlaybackActionResult{}, fmt.Errorf("no URL to replay")
	}
	ref, _, _, err := a.castURLGuarded(ctx, lastURL, "auto", action.AdapterRef, action.Generation)
	if err != nil {
		redacted := redactErr(err, lastURL)
		if redacted == adapters.ErrActiveSessionChangedMessage {
			return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
		}
		return adapters.PlaybackActionResult{}, fmt.Errorf("%s", redacted)
	}
	return adapters.PlaybackActionResult{Message: "replayed " + ref}, nil
}

func (a *Adapter) QuickCastTabs() []adapters.QuickCastTab {
	a.mu.Lock()
	cfg := a.cfg
	probe := a.ytdlpProbe
	a.mu.Unlock()

	fields := []adapters.QuickCastField{{Name: "url", Label: "URL", Type: "url", Placeholder: "https://example.com/video.mp4", Required: true}}
	if cfg.YtdlpEnabled && probe.OK {
		fields = append(fields, adapters.QuickCastField{
			Name:     "mode",
			Label:    "Mode",
			Type:     "radio",
			Required: true,
			Options: []adapters.QuickCastOption{
				{Value: "auto", Label: "Auto"},
				{Value: "ytdlp", Label: "yt-dlp"},
				{Value: "direct", Label: "Direct"},
			},
		})
	}
	return []adapters.QuickCastTab{{
		ID:       "url",
		Label:    "URL",
		Enabled:  a.IsEnabled(),
		Encoding: adapters.QuickCastEncodingForm,
		Fields:   fields,
	}}
}

func (a *Adapter) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	if !a.IsEnabled() {
		return adapters.QuickCastResult{}, &adapters.QuickCastError{
			Status:  http.StatusConflict,
			Chip:    "BLOCKED",
			Message: "url adapter is disabled",
		}
	}
	rawURL := strings.TrimSpace(req.Values["url"])
	if rawURL == "" {
		return adapters.QuickCastResult{}, &adapters.QuickCastError{
			Status:  http.StatusBadRequest,
			Chip:    "BAD URL",
			Message: "url is required",
		}
	}
	mode := strings.TrimSpace(req.Values["mode"])
	if mode == "" {
		mode = "auto"
	}
	hlsBufferMode := strings.TrimSpace(req.Values["hls_buffer"])
	if hlsBufferMode == "" {
		hlsBufferMode = "auto"
	}
	ref, _, status, err := a.castURLWithHLSBuffer(ctx, rawURL, mode, hlsBufferMode)
	if err != nil {
		return adapters.QuickCastResult{}, wrapURLCastError(status, err)
	}
	return adapters.QuickCastResult{Message: "cast started", AdapterRef: ref}, nil
}

// wrapURLCastError lifts the integer status returned by castURLWithHLSBuffer
// into a *QuickCastError with a chip derived from the status code. The
// underlying error is preserved as Cause so errors.Unwrap still works.
func wrapURLCastError(status int, err error) *adapters.QuickCastError {
	chip := "CAST FAILED"
	switch status {
	case http.StatusBadRequest:
		chip = "BAD URL"
	case http.StatusForbidden:
		chip = "BLOCKED"
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return &adapters.QuickCastError{
		Status:  status,
		Chip:    chip,
		Cause:   err,
		Message: err.Error(),
	}
}
