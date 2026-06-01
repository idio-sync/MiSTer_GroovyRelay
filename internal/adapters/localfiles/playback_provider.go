package localfiles

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

var _ adapters.PlaybackControlProvider = (*Adapter)(nil)

type playbackSessionController interface {
	PauseIfSession(string, uint64) (bool, error)
	PlayIfSession(string, uint64) (bool, error)
	StopIfSession(string, uint64) (bool, error)
	SeekToIfSession(string, uint64, int) (bool, error)
}

func (a *Adapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	_ = ctx
	if snap.Source != adapterName && !strings.HasPrefix(snap.AdapterRef, adapterName+":") {
		return adapters.PlaybackBannerAdapterView{}, false
	}

	view := adapters.PlaybackBannerAdapterView{
		Title:         snap.Title,
		SourceDisplay: "Local Files",
	}
	switch snap.State {
	case core.StatePlaying:
		view.Actions = append(view.Actions, adapters.PlaybackAction{ID: adapters.PlaybackActionPause, Label: "Pause", Icon: "pause", Enabled: true})
	case core.StatePaused:
		view.Actions = append(view.Actions, adapters.PlaybackAction{ID: adapters.PlaybackActionResume, Label: "Resume", Icon: "play", Enabled: true})
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

func (a *Adapter) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	_ = ctx
	controller, ok := a.core.(playbackSessionController)
	if a.core == nil || !ok {
		return adapters.PlaybackActionResult{}, fmt.Errorf("localfiles: core playback manager is not configured")
	}

	var (
		matched bool
		err     error
		message string
	)
	switch req.Action {
	case adapters.PlaybackActionPause:
		matched, err = controller.PauseIfSession(req.AdapterRef, req.Generation)
		message = "paused"
	case adapters.PlaybackActionResume:
		matched, err = controller.PlayIfSession(req.AdapterRef, req.Generation)
		message = "resumed"
	case adapters.PlaybackActionStop:
		matched, err = controller.StopIfSession(req.AdapterRef, req.Generation)
		message = "stopped"
	case adapters.PlaybackActionSeek:
		if req.OffsetMS < 0 {
			req.OffsetMS = 0
		}
		matched, err = controller.SeekToIfSession(req.AdapterRef, req.Generation, req.OffsetMS)
		message = "seeked"
	case adapters.PlaybackActionReplay:
		matched, err = controller.SeekToIfSession(req.AdapterRef, req.Generation, 0)
		message = "replayed"
	default:
		return adapters.PlaybackActionResult{}, adapters.UnsupportedPlaybackActionError(fmt.Sprintf("unknown playback action %q", req.Action))
	}
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	return adapters.PlaybackActionResult{Message: message}, nil
}
