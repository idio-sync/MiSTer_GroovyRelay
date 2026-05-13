package ui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
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

func (s *Server) handlePlaybackAction(w http.ResponseWriter, r *http.Request) {
	req, err := parsePlaybackActionRequest(r, false)
	if err != nil {
		s.renderPlaybackMessage(w, r, "err", err.Error(), false, "")
		return
	}
	s.handlePlaybackMutation(w, r, req)
}

func (s *Server) handlePlaybackSeek(w http.ResponseWriter, r *http.Request) {
	req, err := parsePlaybackActionRequest(r, true)
	if err != nil {
		s.renderPlaybackMessage(w, r, "err", err.Error(), false, "")
		return
	}
	s.handlePlaybackMutation(w, r, req)
}

func parsePlaybackActionRequest(r *http.Request, requireOffset bool) (adapters.PlaybackActionRequest, error) {
	if err := r.ParseForm(); err != nil {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("parse form: %w", err)
	}
	gen, err := strconv.ParseUint(strings.TrimSpace(r.Form.Get("generation")), 10, 64)
	if err != nil || gen == 0 {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("generation required")
	}
	action := strings.TrimSpace(r.Form.Get("action"))
	if requireOffset {
		action = adapters.PlaybackActionSeek
	}
	req := adapters.PlaybackActionRequest{
		Action:     action,
		AdapterRef: strings.TrimSpace(r.Form.Get("adapter_ref")),
		Generation: gen,
	}
	if req.AdapterRef == "" {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("adapter_ref required")
	}
	if req.Action == "" {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("action required")
	}
	if !requireOffset && req.Action == adapters.PlaybackActionSeek {
		return adapters.PlaybackActionRequest{}, fmt.Errorf("seek must use seek route")
	}
	if requireOffset {
		offset, err := strconv.Atoi(strings.TrimSpace(r.Form.Get("offset_ms")))
		if err != nil {
			return adapters.PlaybackActionRequest{}, fmt.Errorf("offset_ms must be an integer")
		}
		req.OffsetMS = offset
	}
	return req, nil
}

func (s *Server) handlePlaybackMutation(w http.ResponseWriter, r *http.Request, req adapters.PlaybackActionRequest) {
	snap := s.currentPlaybackSnapshot()
	if snap.AdapterRef != req.AdapterRef || snap.Generation != req.Generation {
		s.renderPlaybackMessage(w, r, "err", "active session changed", false, "")
		return
	}
	provider, ok := s.playbackProviderForSnapshot(snap)
	if !ok {
		s.renderPlaybackMessage(w, r, "err", "active adapter does not expose playback controls", false, "")
		return
	}
	providerView, owns := provider.PlaybackBanner(r.Context(), snap)
	if !owns || !playbackActionEnabled(providerView, req) {
		s.renderPlaybackMessage(w, r, "err", "playback action unavailable", false, "")
		return
	}
	result, err := provider.HandlePlaybackAction(r.Context(), req)
	if err != nil {
		s.renderPlaybackMessage(w, r, "err", err.Error(), false, "")
		return
	}
	s.renderPlaybackMessage(w, r, "ok", result.Message, false, "")
}

func (s *Server) handlePlaybackQuickCast(w http.ResponseWriter, r *http.Request) {
	req, err := parseQuickCastRequest(w, r)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		s.renderPlaybackMessage(w, r, "err", err.Error(), true, "")
		return
	}
	provider, tab, ok := s.quickCastProviderForTab(req.TabID)
	if !ok {
		s.renderPlaybackMessage(w, r, "err", "quick-cast provider unavailable", true, req.TabID)
		return
	}
	if !tab.Enabled {
		reason := tab.DisabledReason
		if reason == "" {
			reason = "quick-cast tab disabled"
		}
		s.renderPlaybackMessage(w, r, "err", reason, true, req.TabID)
		return
	}
	result, err := provider.HandleQuickCast(r.Context(), req)
	if err != nil {
		s.renderPlaybackMessage(w, r, "err", err.Error(), true, req.TabID)
		return
	}
	msg := result.Message
	if msg == "" {
		msg = "cast started"
	}
	s.renderPlaybackMessage(w, r, "ok", msg, false, "")
}

func playbackActionEnabled(view adapters.PlaybackBannerAdapterView, req adapters.PlaybackActionRequest) bool {
	if req.Action == adapters.PlaybackActionSeek {
		return view.Seek != nil && view.Seek.Enabled
	}
	for _, action := range view.Actions {
		if action.ID == req.Action {
			return action.Enabled
		}
	}
	return false
}

const maxQuickCastMultipartBytes = 4*1024*1024 + 64*1024

func parseQuickCastRequest(w http.ResponseWriter, r *http.Request) (adapters.QuickCastRequest, error) {
	ct := r.Header.Get("Content-Type")
	req := adapters.QuickCastRequest{Values: map[string]string{}}
	if strings.HasPrefix(ct, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxQuickCastMultipartBytes)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			return req, fmt.Errorf("parse multipart: %w", err)
		}
		for k, values := range r.MultipartForm.Value {
			if len(values) > 0 {
				req.Values[k] = values[0]
			}
		}
		for k, files := range r.MultipartForm.File {
			if len(files) > 0 {
				req.File = &adapters.QuickCastFile{FieldName: k, Header: files[0]}
				break
			}
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return req, fmt.Errorf("parse form: %w", err)
		}
		for k, values := range r.PostForm {
			if len(values) > 0 {
				req.Values[k] = values[0]
			}
		}
	}
	req.TabID = strings.TrimSpace(req.Values["tab_id"])
	delete(req.Values, "tab_id")
	if req.TabID == "" {
		return req, fmt.Errorf("tab_id required")
	}
	return req, nil
}

func (s *Server) currentPlaybackSnapshot() adapters.PlaybackBannerSnapshot {
	view := core.StatusHomeView{State: core.StateIdle}
	if s.cfg.StatusViewer != nil {
		view = s.cfg.StatusViewer.StatusHomeView()
	}
	return adapters.PlaybackBannerSnapshot{
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
}

func (s *Server) renderPlaybackMessage(w http.ResponseWriter, r *http.Request, kind, msg string, drawer bool, tab string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	opts := playbackRenderOptions{Message: msg, MessageKind: kind, CastDrawerOpen: drawer, ActiveQuickCast: tab}
	_ = s.tmpl.ExecuteTemplate(w, "now-playing-banner.html", s.buildPlaybackBannerData(r.Context(), opts))
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
	if len(data.QuickCastTabs) == 0 {
		data.CastDrawerOpen = false
		data.ActiveQuickCast = ""
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
