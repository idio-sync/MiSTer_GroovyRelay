package ui

import (
	"encoding/json"
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
type CompanionHistoryEntry struct {
	ID         string    `json:"id"`
	Title      string    `json:"title,omitempty"`
	URLDisplay string    `json:"url_display"`
	LastPlayed time.Time `json:"last_played_at"`
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
	CompanionHistory() []CompanionHistoryEntry
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
		return "online"
	case adapters.StateStarting:
		return "starting"
	case adapters.StateError:
		return "error"
	case adapters.StateStopped:
		return "offline"
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
