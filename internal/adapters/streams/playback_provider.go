package streams

import (
	"context"
	"fmt"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (a *Adapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	_ = ctx
	if snap.Source != "streams" && !strings.HasPrefix(snap.AdapterRef, "streams:") {
		return adapters.PlaybackBannerAdapterView{}, false
	}

	a.mu.Lock()
	q := a.active
	if q == nil || activeAdapterRef(q) != snap.AdapterRef {
		a.mu.Unlock()
		// Source claim is "streams" but the adapter no longer owns this
		// session (e.g. the queue was cleared or a different streams session
		// is active). Surrender ownership so the UI renders the banner as
		// read-only and routes its actions through the source-display
		// fallback rather than calling HandlePlaybackAction.
		return adapters.PlaybackBannerAdapterView{}, false
	}

	view := adapters.PlaybackBannerAdapterView{SourceDisplay: "Streams"}
	title := q.ChannelName
	subtitle := q.ProviderName
	itemTitle := ""
	if item, ok := q.currentItem(); ok {
		itemTitle = item.Title
	}
	canPrevious := q.canAdvancePrevious()
	canNext := q.canAdvanceNext()
	canReplay := a.canReplayLocked(q)
	a.mu.Unlock()

	view.Title = firstNonEmpty(itemTitle, title, snap.Title)
	view.Subtitle = subtitle
	view.Actions = []adapters.PlaybackAction{
		{ID: adapters.PlaybackActionPrevious, Label: "Previous", Icon: "skip-back", Enabled: canPrevious, DisabledReason: disabledReason(canPrevious, "no previous item")},
		{ID: adapters.PlaybackActionNext, Label: "Next", Icon: "skip-forward", Enabled: canNext, DisabledReason: disabledReason(canNext, "no next item")},
		{ID: adapters.PlaybackActionReplay, Label: "Replay", Icon: "rotate-ccw", Enabled: canReplay, DisabledReason: disabledReason(canReplay, "cannot replay current queue")},
		{ID: adapters.PlaybackActionStop, Label: "Stop", Icon: "stop", Enabled: true},
	}
	return view, true
}

func (a *Adapter) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if err := a.ensureOwnsCoreSession(req.AdapterRef, req.Generation); err != nil {
		return adapters.PlaybackActionResult{}, err
	}

	var err error
	switch req.Action {
	case adapters.PlaybackActionPrevious:
		err = a.PreviousGuarded(ctx, req.AdapterRef, req.Generation)
	case adapters.PlaybackActionNext:
		err = a.NextGuarded(ctx, req.AdapterRef, req.Generation)
	case adapters.PlaybackActionReplay:
		err = a.ReplayGuarded(ctx, req.AdapterRef, req.Generation)
	case adapters.PlaybackActionStop:
		err = a.StopQueueGuarded(ctx, req.AdapterRef, req.Generation)
	default:
		err = fmt.Errorf("unknown playback action %q", req.Action)
	}
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	return adapters.PlaybackActionResult{Message: "streams updated"}, nil
}

func (a *Adapter) ensureOwnsCoreSession(ref string, generation uint64) error {
	if a.core == nil {
		return playbackError("", "core playback manager is not configured")
	}
	st := a.core.Status()
	if st.AdapterRef != ref || st.Generation != generation {
		return playbackError("", adapters.ErrActiveSessionChangedMessage)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil || activeAdapterRef(a.active) != ref {
		return playbackError("", "streams does not own the active queue")
	}
	return nil
}

func disabledReason(enabled bool, reason string) string {
	if enabled {
		return ""
	}
	return reason
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
