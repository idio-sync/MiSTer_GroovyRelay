package url

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

type companionHTTPError struct {
	status int
	msg    string
}

func (e companionHTTPError) Error() string      { return e.msg }
func (e companionHTTPError) HTTPStatus() int    { return e.status }
func companionMsg(status int, msg string) error { return companionHTTPError{status: status, msg: msg} }

func companionErr(status int, err error) error {
	if err == nil {
		return nil
	}
	return companionHTTPError{status: status, msg: err.Error()}
}

func foreignSession(st core.SessionStatus) bool {
	return st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:")
}

// CompanionHistory returns redacted point-in-time history snapshots for
// the browser extension. Raw URLs never cross the internal/ui boundary.
func (a *Adapter) CompanionHistory() []ui.CompanionHistoryEntry {
	history := a.history.List()
	out := make([]ui.CompanionHistoryEntry, 0, len(history))
	for _, e := range history {
		out = append(out, ui.CompanionHistoryEntry{
			ID:         e.ID,
			Title:      e.Title,
			URLDisplay: redactURL(e.URL),
			LastPlayed: e.LastPlayedAt,
		})
	}
	return out
}

// CompanionLastURLDisplay returns the most recent URL in redacted display
// form. Empty string means there is nothing safe/useful to show.
func (a *Adapter) CompanionLastURLDisplay() string {
	raw := a.snapshotLastURL()
	if raw == "" {
		return ""
	}
	return redactURL(raw)
}

// CompanionDisplay enriches URL-owned core sessions with the URL adapter's
// title/history knowledge. Foreign adapter refs intentionally return zero so
// internal/ui can fall back to the registry display name.
func (a *Adapter) CompanionDisplay(adapterRef string) ui.CompanionSessionDisplay {
	if !strings.HasPrefix(adapterRef, "url:") {
		return ui.CompanionSessionDisplay{}
	}
	raw := a.snapshotLastURL()
	display := ui.CompanionSessionDisplay{
		AdapterName:   a.DisplayName(),
		SourceDisplay: a.CompanionLastURLDisplay(),
	}
	if raw == "" {
		return display
	}
	key := dedupeKey(raw)
	for _, e := range a.history.List() {
		if dedupeKey(e.URL) == key {
			display.Title = e.Title
			break
		}
	}
	return display
}

func (a *Adapter) CompanionPlay(ctx context.Context, rawURL, mode string) (ui.CompanionPlayResult, error) {
	ref, resolvedVia, status, err := a.castURL(ctx, rawURL, mode)
	if err != nil {
		return ui.CompanionPlayResult{}, companionErr(status, err)
	}
	return ui.CompanionPlayResult{State: core.StatePlaying, AdapterRef: ref, ResolvedVia: resolvedVia}, nil
}

func (a *Adapter) CompanionPause(ctx context.Context) error {
	_ = ctx
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if st.State == core.StatePaused {
		return nil
	}
	if err := a.core.Pause(); err != nil {
		return companionErr(http.StatusConflict, err)
	}
	return nil
}

func (a *Adapter) CompanionResume(ctx context.Context) error {
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if st.State == core.StatePlaying {
		return nil
	}
	if st.Duration > 0 {
		if err := a.core.Play(); err != nil {
			return companionMsg(http.StatusConflict, redactErr(err, a.snapshotLastURL()))
		}
		return nil
	}
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		return companionMsg(http.StatusBadRequest, "no URL to resume")
	}
	_, _, status, err := a.castURL(ctx, lastURL, "auto")
	if err != nil {
		return companionErr(status, err)
	}
	return nil
}

func (a *Adapter) CompanionStop(ctx context.Context) error {
	_ = ctx
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if err := a.core.Stop(); err != nil {
		return companionErr(http.StatusConflict, err)
	}
	return nil
}

func (a *Adapter) CompanionReplay(ctx context.Context) (ui.CompanionPlayResult, error) {
	st := a.core.Status()
	if foreignSession(st) {
		return ui.CompanionPlayResult{}, companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		return ui.CompanionPlayResult{}, companionMsg(http.StatusBadRequest, "no URL to replay")
	}
	ref, resolvedVia, status, err := a.castURL(ctx, lastURL, "auto")
	if err != nil {
		return ui.CompanionPlayResult{}, companionErr(status, err)
	}
	return ui.CompanionPlayResult{State: core.StatePlaying, AdapterRef: ref, ResolvedVia: resolvedVia}, nil
}

// CompanionSeek seeks to an absolute offset in milliseconds, clamped to the
// current session duration.
func (a *Adapter) CompanionSeek(ctx context.Context, offsetMs int) error {
	_ = ctx
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if st.Duration <= 0 {
		return companionMsg(http.StatusConflict, "source not seekable")
	}
	durMs := int(st.Duration / time.Millisecond)
	if offsetMs < 0 {
		offsetMs = 0
	}
	if offsetMs > durMs {
		offsetMs = durMs
	}
	if err := a.core.SeekTo(offsetMs); err != nil {
		return companionMsg(http.StatusConflict, redactErr(err, a.snapshotLastURL()))
	}
	return nil
}

func (a *Adapter) CompanionHistoryPlay(ctx context.Context, id string) (ui.CompanionPlayResult, error) {
	entry, ok := a.history.GetByID(id)
	if !ok {
		return ui.CompanionPlayResult{}, companionMsg(http.StatusNotFound, "history entry no longer exists")
	}
	ref, resolvedVia, status, err := a.castURL(ctx, entry.URL, "auto")
	if err != nil {
		return ui.CompanionPlayResult{}, companionErr(status, err)
	}
	return ui.CompanionPlayResult{State: core.StatePlaying, AdapterRef: ref, ResolvedVia: resolvedVia}, nil
}

func (a *Adapter) CompanionHistoryDelete(ctx context.Context, id string) error {
	_ = ctx
	if !a.history.RemoveByID(id) {
		return companionMsg(http.StatusNotFound, "history entry no longer exists")
	}
	return nil
}
