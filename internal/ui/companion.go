package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// CompanionSessionProvider is the UI package's narrow read view of
// core.Manager. The companion status response uses fresh point-in-time
// snapshots only; clients must not extrapolate position between polls.
type CompanionSessionProvider interface {
	Status() core.SessionStatus
}

// CompanionPlayResult is the JSON-safe result companion mutating routes
// return. It intentionally does not mirror the URL adapter's HTML/form
// response helpers, keeping URL redaction rules local to the adapter.
type CompanionPlayResult struct {
	State         core.State
	AdapterRef    string
	ResolvedVia   string
	Title         string
	SourceDisplay string
}

// CompanionHistoryEntry is the redacted, stable-id history shape exposed to
// the browser extension. ID is opaque and stable across reorder/bump events.
// JSON tag last_played matches the locked spec
// (docs/superpowers/specs/2026-05-09-companion-extension-mini-remote-design.md
// line 226).
type CompanionHistoryEntry struct {
	ID         string    `json:"id"`
	Title      string    `json:"title,omitempty"`
	URLDisplay string    `json:"url_display"`
	LastPlayed time.Time `json:"last_played"`
}

// CompanionSessionDisplay lets adapters enrich core.SessionStatus without
// leaking adapter internals into internal/ui.
type CompanionSessionDisplay struct {
	AdapterName   string
	Title         string
	SourceDisplay string
	ResolvedVia   string
}

// CompanionURLSource is the companion surface owned by the URL adapter.
// Step 1 uses only the read methods; mutating methods are added to this
// contract when the companion write routes land.
type CompanionURLSource interface {
	CompanionPlay(context.Context, string, string) (CompanionPlayResult, error)
	CompanionPause(context.Context) error
	CompanionResume(context.Context) error
	CompanionStop(context.Context) error
	CompanionReplay(context.Context) (CompanionPlayResult, error)
	CompanionSeek(context.Context, int) error
	CompanionHistory() []CompanionHistoryEntry
	CompanionHistoryPlay(context.Context, string) (CompanionPlayResult, error)
	CompanionHistoryDelete(context.Context, string) error
	CompanionLastURLDisplay() string
}

// CompanionDisplayProvider resolves an adapter-owned session ref to a
// redacted display snapshot. Non-URL refs should return the zero value.
type CompanionDisplayProvider interface {
	CompanionDisplay(adapterRef string) CompanionSessionDisplay
}

type companionStatusResponse struct {
	OK         bool                    `json:"ok"`
	Configured bool                    `json:"configured"`
	BridgeURL  string                  `json:"bridge_url"`
	Health     companionHealth         `json:"health"`
	Session    companionSessionPayload `json:"session"`
	History    []CompanionHistoryEntry `json:"history"`
}

type companionHealth struct {
	Bridge     string `json:"bridge"`
	Mister     string `json:"mister"`
	URLAdapter string `json:"url_adapter"`
}

// State uses the same enum values as core.SessionStatus.State:
// idle, playing, or paused.
type companionSessionPayload struct {
	State         core.State            `json:"state"`
	AdapterRef    string                `json:"adapter_ref,omitempty"`
	AdapterName   string                `json:"adapter_name,omitempty"`
	Title         string                `json:"title,omitempty"`
	SourceDisplay string                `json:"source_display,omitempty"`
	ResolvedVia   string                `json:"resolved_via,omitempty"`
	PositionMS    int64                 `json:"position_ms"`
	DurationMS    int64                 `json:"duration_ms"`
	StartedAt     *time.Time            `json:"started_at,omitempty"`
	Capabilities  companionCapabilities `json:"capabilities"`
}

type companionCapabilities struct {
	CanPlay   bool `json:"can_play"`
	CanPause  bool `json:"can_pause"`
	CanResume bool `json:"can_resume"`
	CanStop   bool `json:"can_stop"`
	CanReplay bool `json:"can_replay"`
	CanSeek   bool `json:"can_seek"`
}

type companionErrorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func companionExtensionGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleExtensionCORSPreflight(w, r)
			return
		}
		setExtensionCORSHeaders(w, r)
		if !isExtensionOrigin(r.Header.Get("Origin")) || r.Header.Get("X-Bridge-Extension") != "1" {
			writeCompanionError(w, http.StatusForbidden, "companion extension origin and header required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) mountCompanion(mux *http.ServeMux, method, pattern string, handler http.HandlerFunc) {
	mux.Handle(method+" "+pattern, companionExtensionGate(http.HandlerFunc(handler)))
}

func (s *Server) handleCompanionStatus(w http.ResponseWriter, r *http.Request) {
	st := core.SessionStatus{State: core.StateIdle}
	if s.cfg.CompanionSession != nil {
		st = s.cfg.CompanionSession.Status()
		if st.State == "" {
			st.State = core.StateIdle
		}
	}

	history := []CompanionHistoryEntry{}
	if s.cfg.CompanionURL != nil {
		history = s.cfg.CompanionURL.CompanionHistory()
		if history == nil {
			history = []CompanionHistoryEntry{}
		}
	}

	writeCompanionJSON(w, http.StatusOK, companionStatusResponse{
		OK:         true,
		Configured: true,
		BridgeURL:  companionBridgeURL(r),
		Health: companionHealth{
			Bridge:     "online",
			Mister:     "unknown",
			URLAdapter: s.companionAdapterHealth("url"),
		},
		Session: s.companionSession(st),
		History: history,
	})
}

func (s *Server) handleCompanionPlay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.URL == "" {
		writeCompanionError(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.Mode == "" {
		req.Mode = "auto"
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	res, err := src.CompanionPlay(r.Context(), req.URL, req.Mode)
	if err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusAccepted, companionStartedResponse(res))
}

func (s *Server) handleCompanionControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action   string `json:"action"`
		OffsetMS int    `json:"offset_ms"`
	}
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	var err error
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "pause":
		err = src.CompanionPause(r.Context())
	case "resume":
		err = src.CompanionResume(r.Context())
	case "stop":
		err = src.CompanionStop(r.Context())
	case "replay":
		var res CompanionPlayResult
		res, err = src.CompanionReplay(r.Context())
		if err == nil {
			writeCompanionJSON(w, http.StatusAccepted, companionStartedResponse(res))
			return
		}
	case "seek":
		err = src.CompanionSeek(r.Context(), req.OffsetMS)
	default:
		writeCompanionError(w, http.StatusBadRequest, "unsupported action")
		return
	}
	if err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	state := core.StateIdle
	if s.cfg.CompanionSession != nil {
		state = s.cfg.CompanionSession.Status().State
		if state == "" {
			state = core.StateIdle
		}
	}
	writeCompanionJSON(w, http.StatusOK, map[string]any{"ok": true, "state": state})
}

func (s *Server) handleCompanionHistoryPlay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		writeCompanionError(w, http.StatusBadRequest, "id is required")
		return
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	res, err := src.CompanionHistoryPlay(r.Context(), req.ID)
	if err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusAccepted, companionStartedResponse(res))
}

func (s *Server) handleCompanionHistoryDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		writeCompanionError(w, http.StatusBadRequest, "id is required")
		return
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	if err := src.CompanionHistoryDelete(r.Context(), req.ID); err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCompanionLaunch(w http.ResponseWriter, r *http.Request) {
	if !requireCompanionJSON(w, r) {
		return
	}
	if s.cfg.MisterLauncher == nil {
		writeCompanionError(w, http.StatusInternalServerError, "mister launcher not wired")
		return
	}
	if err := s.cfg.MisterLauncher.Launch(r.Context()); err != nil {
		writeCompanionError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func requireCompanionJSON(w http.ResponseWriter, r *http.Request) bool {
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	if ct == "" {
		if r.ContentLength == 0 {
			return true
		}
		writeCompanionError(w, http.StatusUnsupportedMediaType, "Content-Type application/json required")
		return false
	}
	mt := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	if mt != "application/json" {
		writeCompanionError(w, http.StatusUnsupportedMediaType, "Content-Type application/json required")
		return false
	}
	return true
}

func decodeCompanionJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !requireCompanionJSON(w, r) {
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeCompanionError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Server) companionURLRequired(w http.ResponseWriter) (CompanionURLSource, bool) {
	if s.cfg.CompanionURL == nil {
		writeCompanionError(w, http.StatusInternalServerError, "companion URL source not wired")
		return nil, false
	}
	return s.cfg.CompanionURL, true
}

func companionStartedResponse(res CompanionPlayResult) map[string]any {
	state := res.State
	if state == "" {
		state = core.StatePlaying
	}
	return map[string]any{
		"ok":           true,
		"adapter_ref":  res.AdapterRef,
		"state":        state,
		"resolved_via": res.ResolvedVia,
	}
}

func (s *Server) companionSession(st core.SessionStatus) companionSessionPayload {
	if st.State == "" {
		st.State = core.StateIdle
	}
	display := CompanionSessionDisplay{
		AdapterName: s.companionAdapterName(st.AdapterRef),
	}
	if s.cfg.CompanionDisplay != nil {
		if enriched := s.cfg.CompanionDisplay.CompanionDisplay(st.AdapterRef); enriched.AdapterName != "" ||
			enriched.Title != "" || enriched.SourceDisplay != "" || enriched.ResolvedVia != "" {
			if enriched.AdapterName != "" {
				display.AdapterName = enriched.AdapterName
			}
			if enriched.Title != "" {
				display.Title = enriched.Title
			}
			if enriched.SourceDisplay != "" {
				display.SourceDisplay = enriched.SourceDisplay
			}
			if enriched.ResolvedVia != "" {
				display.ResolvedVia = enriched.ResolvedVia
			}
		}
	}

	var startedAt *time.Time
	if !st.StartedAt.IsZero() {
		t := st.StartedAt.UTC()
		startedAt = &t
	}
	return companionSessionPayload{
		State:         st.State,
		AdapterRef:    st.AdapterRef,
		AdapterName:   display.AdapterName,
		Title:         display.Title,
		SourceDisplay: display.SourceDisplay,
		ResolvedVia:   display.ResolvedVia,
		PositionMS:    st.Position.Milliseconds(),
		DurationMS:    st.Duration.Milliseconds(),
		StartedAt:     startedAt,
		Capabilities:  s.companionCapabilities(st),
	}
}

func (s *Server) companionCapabilities(st core.SessionStatus) companionCapabilities {
	if s.cfg.CompanionURL == nil {
		return companionCapabilities{}
	}
	if st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:") {
		return companionCapabilities{}
	}
	if st.State == "" {
		st.State = core.StateIdle
	}
	caps := companionCapabilities{
		CanPlay:   s.companionURLReady(),
		CanReplay: s.cfg.CompanionURL.CompanionLastURLDisplay() != "",
	}
	switch st.State {
	case core.StatePlaying:
		caps.CanPause = true
		caps.CanStop = true
	case core.StatePaused:
		caps.CanResume = true
		caps.CanStop = true
	}
	if (st.State == core.StatePlaying || st.State == core.StatePaused) && st.Duration > 0 {
		caps.CanSeek = true
	}
	return caps
}

func (s *Server) companionURLReady() bool {
	a, ok := s.cfg.Registry.Get("url")
	if !ok {
		return false
	}
	if !a.IsEnabled() {
		return false
	}
	state := a.Status().State
	return state == adapters.StateRunning || state == adapters.StateStarting
}

func (s *Server) companionAdapterName(adapterRef string) string {
	name := adapterNameFromRef(adapterRef)
	if name == "" {
		return ""
	}
	a, ok := s.cfg.Registry.Get(name)
	if !ok {
		return name
	}
	return a.DisplayName()
}

// companionAdapterHealth maps an adapter's runtime state to the spec
// health enum (spec line 242: enabled/disabled/running/error). The
// extension popup renders this as plain text, so keep the strings
// short and operator-readable. "missing" means the adapter is not
// registered at all; "off" is the post-Stop terminal state; the rest
// align with adapters.State.
func (s *Server) companionAdapterHealth(name string) string {
	a, ok := s.cfg.Registry.Get(name)
	if !ok {
		return "missing"
	}
	if !a.IsEnabled() {
		return "disabled"
	}
	switch a.Status().State {
	case adapters.StateRunning:
		return "running"
	case adapters.StateStarting:
		return "starting"
	case adapters.StateError:
		return "error"
	case adapters.StateStopped:
		return "off"
	default:
		return "unknown"
	}
}

func adapterNameFromRef(adapterRef string) string {
	if adapterRef == "" {
		return ""
	}
	name, _, ok := strings.Cut(adapterRef, ":")
	if !ok {
		return adapterRef
	}
	return name
}

func companionBridgeURL(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host
}

func writeCompanionJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeCompanionError(w http.ResponseWriter, status int, message string) {
	writeCompanionJSON(w, status, companionErrorResponse{OK: false, Error: message})
}

func companionHTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var ce interface{ HTTPStatus() int }
	if errors.As(err, &ce) {
		return ce.HTTPStatus()
	}
	return http.StatusInternalServerError
}
