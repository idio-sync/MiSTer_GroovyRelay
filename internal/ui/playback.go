package ui

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type playbackBannerData struct {
	StateLabel      string
	State           core.State
	SourceDisplay   string
	Title           string
	AdapterRef      string
	Generation      uint64
	Position        string
	Duration        string
	PositionMS      int
	DurationMS      int
	HasTimeline     bool
	Actions         []adapters.PlaybackAction
	Seek            *adapters.PlaybackSeek
	QuickCastTabs   []adapters.QuickCastTab
	CastDrawerOpen  bool
	ActiveQuickCast string
	Message         string
	MessageKind     string
	PollTrigger     string
	ReadOnly        bool
}

type playbackRenderOptions struct {
	CastDrawerOpen  bool
	ActiveQuickCast string
	Message         string
	MessageKind     string
}

func (s *Server) handlePlaybackBanner(w http.ResponseWriter, r *http.Request) {
	opts := playbackRenderOptions{}
	if r.URL.Query().Get("drawer") == "cast" {
		opts.CastDrawerOpen = true
		opts.ActiveQuickCast = strings.TrimSpace(r.URL.Query().Get("tab"))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "now-playing-banner.html", s.buildPlaybackBannerData(r.Context(), opts)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildPlaybackBannerData(ctx context.Context, opts playbackRenderOptions) playbackBannerData {
	view := core.StatusHomeView{State: core.StateIdle}
	if s.cfg.StatusViewer != nil {
		view = s.cfg.StatusViewer.StatusHomeView()
	}
	snap := adapters.PlaybackBannerSnapshot{
		State:      view.State,
		Source:     view.Source,
		Title:      view.Title,
		AdapterRef: view.AdapterRef,
		Generation: view.Generation,
		Position:   view.Position,
		Duration:   view.Duration,
		StartedAt:  view.StartedAt,
		MediaKind:  view.MediaKind,
		Modeline:   view.Modeline,
	}
	data := playbackBannerData{
		State:           view.State,
		StateLabel:      playbackStateLabel(view.State),
		SourceDisplay:   displayNameForSource(s.cfg.Registry, view.Source, view.AdapterRef),
		Title:           firstNonEmpty(view.Title, view.AdapterRef, "Ready"),
		AdapterRef:      view.AdapterRef,
		Generation:      view.Generation,
		Position:        formatClock(view.Position),
		Duration:        formatClock(view.Duration),
		PositionMS:      int(view.Position / time.Millisecond),
		DurationMS:      int(view.Duration / time.Millisecond),
		HasTimeline:     view.Duration > 0,
		CastDrawerOpen:  opts.CastDrawerOpen,
		ActiveQuickCast: opts.ActiveQuickCast,
		Message:         opts.Message,
		MessageKind:     opts.MessageKind,
		QuickCastTabs:   s.quickCastTabs(),
	}
	if data.SourceDisplay == "" && view.State == core.StateIdle && len(data.QuickCastTabs) > 0 {
		data.SourceDisplay = data.QuickCastTabs[0].Label
	}
	if data.SourceDisplay == "" {
		data.SourceDisplay = "No source"
	}
	if provider, ok := s.playbackProviderForSnapshot(snap); ok {
		if providerView, owns := provider.PlaybackBanner(ctx, snap); owns {
			if providerView.Title != "" {
				data.Title = providerView.Title
			}
			if providerView.SourceDisplay != "" {
				data.SourceDisplay = providerView.SourceDisplay
			}
			data.Actions = providerView.Actions
			data.Seek = providerView.Seek
			if providerView.Seek != nil {
				data.HasTimeline = data.HasTimeline || providerView.Seek.DurationMS > 0
				data.PositionMS = providerView.Seek.OffsetMS
				data.DurationMS = providerView.Seek.DurationMS
			}
		}
	}
	if view.State != core.StateIdle && len(data.Actions) == 0 && data.Seek == nil {
		data.ReadOnly = true
	}
	data.PollTrigger = playbackPollTrigger(data)
	return data
}

func (s *Server) playbackProviderForSnapshot(snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackControlProvider, bool) {
	if s.cfg.Registry == nil || snap.AdapterRef == "" {
		return nil, false
	}
	if snap.Source != "" {
		if a, ok := s.cfg.Registry.Get(snap.Source); ok {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	for _, a := range s.cfg.Registry.List() {
		if adapterRefBelongsTo(a.Name(), snap.AdapterRef) {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	return nil, false
}

func (s *Server) quickCastTabs() []adapters.QuickCastTab {
	if s.cfg.Registry == nil {
		return nil
	}
	var tabs []adapters.QuickCastTab
	for _, a := range s.cfg.Registry.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		tabs = append(tabs, p.QuickCastTabs()...)
	}
	return tabs
}

func (s *Server) quickCastProviderForTab(tabID string) (adapters.QuickCastProvider, adapters.QuickCastTab, bool) {
	if s.cfg.Registry == nil || tabID == "" {
		return nil, adapters.QuickCastTab{}, false
	}
	for _, a := range s.cfg.Registry.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		for _, tab := range p.QuickCastTabs() {
			if tab.ID == tabID {
				return p, tab, true
			}
		}
	}
	return nil, adapters.QuickCastTab{}, false
}

func playbackStateLabel(state core.State) string {
	switch state {
	case core.StatePlaying:
		return "Now Playing"
	case core.StatePaused:
		return "Paused"
	default:
		return "Ready to cast"
	}
}

func playbackPollTrigger(data playbackBannerData) string {
	if data.CastDrawerOpen {
		return ""
	}
	if data.State == core.StateIdle || data.ReadOnly {
		return "every 5s"
	}
	if data.HasTimeline && data.Seek != nil && data.Seek.Enabled {
		return "every 1s"
	}
	return "every 3s"
}

func quickCastTabActive(tab adapters.QuickCastTab, active string, first bool) bool {
	if active != "" {
		return tab.ID == active
	}
	return first
}

func adapterRefBelongsTo(adapterName, ref string) bool {
	return strings.HasPrefix(ref, adapterName+":") || strings.HasPrefix(ref, adapterName+"/")
}
