package streams

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

type routeErrorResponse struct {
	Error string `json:"error"`
}

type refreshStatusResponse struct {
	ProviderID string    `json:"provider_id,omitempty"`
	Source     string    `json:"source,omitempty"`
	FetchedAt  time.Time `json:"fetched_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: http.MethodGet, Path: "panel", Handler: a.handlePanel},
		{Method: http.MethodGet, Path: "status", Handler: a.handleStatus},
		{Method: http.MethodGet, Path: "providers", Handler: a.handleProviders},
		{Method: http.MethodPost, Path: "refresh", Handler: a.handleRefresh},
		{Method: http.MethodPost, Path: "play", Handler: a.handlePlay},
		{Method: http.MethodPost, Path: "replay", Handler: a.handleReplay},
		{Method: http.MethodPost, Path: "next", Handler: a.handleNext},
		{Method: http.MethodPost, Path: "previous", Handler: a.handlePrevious},
		{Method: http.MethodPost, Path: "stop", Handler: a.handleStop},
	}
}

func (a *Adapter) handlePanel(w http.ResponseWriter, r *http.Request) {
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) handleStatus(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) handleProviders(w http.ResponseWriter, r *http.Request) {
	providers := filterProviderStatusViews(a.statusView().Providers, r)
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, providers)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderProvidersFromView(providers)))
}

func filterProviderStatusViews(providers []ProviderStatusView, r *http.Request) []ProviderStatusView {
	if r == nil {
		return providers
	}
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if providerID == "" && query == "" {
		return providers
	}

	out := make([]ProviderStatusView, 0, len(providers))
	for _, provider := range providers {
		if providerID != "" && provider.ID != providerID {
			continue
		}
		if query != "" && !providerMatchesQuery(provider, query) {
			continue
		}
		out = append(out, provider)
	}
	return out
}

func providerMatchesQuery(provider ProviderStatusView, query string) bool {
	if strings.Contains(strings.ToLower(provider.ID), query) ||
		strings.Contains(strings.ToLower(provider.Name), query) {
		return true
	}
	for _, channel := range provider.Channels {
		if strings.Contains(strings.ToLower(channel.ID), query) ||
			strings.Contains(strings.ToLower(channel.Name), query) ||
			strings.Contains(strings.ToLower(channel.Description), query) {
			return true
		}
	}
	return false
}

func (a *Adapter) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.respondRouteError(w, r, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	providerID := strings.TrimSpace(r.Form.Get("provider_id"))
	if providerID != "" {
		if err := a.validateRefreshProvider(providerID); err != nil {
			a.respondRouteError(w, r, streamErrorStatus(err), err.Error())
			return
		}
	}
	status := a.RefreshNow(r.Context(), providerID)
	if status.Err != nil {
		a.respondRouteError(w, r, http.StatusBadGateway, status.Err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, refreshStatusView(status))
		return
	}
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) handlePlay(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.respondRouteError(w, r, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	res := streamhandoff.Resolution{
		ProviderID: strings.TrimSpace(r.Form.Get("provider_id")),
		ChannelID:  strings.TrimSpace(r.Form.Get("channel_id")),
		ItemID:     strings.TrimSpace(r.Form.Get("item_id")),
	}
	if res.ProviderID == "" {
		a.respondRouteError(w, r, http.StatusBadRequest, "provider_id required")
		return
	}
	if res.ChannelID == "" && res.ItemID == "" {
		a.respondRouteError(w, r, http.StatusBadRequest, "channel_id or item_id required")
		return
	}
	if err := a.validatePlayRequest(res); err != nil {
		a.respondRouteError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	started, err := a.StartResolvedStream(r.Context(), res)
	if err != nil {
		a.respondRouteError(w, r, streamErrorStatus(err), err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, started)
		return
	}
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) handleReplay(w http.ResponseWriter, r *http.Request) {
	if err := a.Replay(r.Context()); err != nil {
		a.respondRouteError(w, r, http.StatusConflict, err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) handleNext(w http.ResponseWriter, r *http.Request) {
	if err := a.Next(r.Context()); err != nil {
		a.respondRouteError(w, r, http.StatusConflict, err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) handlePrevious(w http.ResponseWriter, r *http.Request) {
	if err := a.Previous(r.Context()); err != nil {
		a.respondRouteError(w, r, http.StatusConflict, err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := a.StopQueue(r.Context()); err != nil {
		a.respondRouteError(w, r, http.StatusConflict, err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.respondPanel(w, r, http.StatusOK)
}

func (a *Adapter) validateRefreshProvider(providerID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if providerCfg, ok := a.cfg.Providers[providerID]; ok && providerCfg.Disabled {
		return &StreamsError{Kind: ErrKindProviderDisabled, Message: "streams provider " + quote(providerID) + " is disabled"}
	}
	if _, ok := a.definitions[providerID]; ok {
		return nil
	}
	if _, ok := a.catalogs[providerID]; ok {
		return nil
	}
	return invalidExtraction(providerID, "provider is not cataloged")
}

func (a *Adapter) validatePlayRequest(res streamhandoff.Resolution) error {
	if res.ChannelID != "" && res.ItemID != "" && res.ChannelID != reservedAdhocID {
		return playbackError(res.ProviderID, "resolution must identify exactly one channel or item")
	}
	if res.ItemID != "" && !youtubeIDRE.MatchString(res.ItemID) {
		return invalidExtraction(res.ProviderID, "item is not a valid YouTube ID")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	cat, ok := a.catalogs[res.ProviderID]
	if !ok {
		return invalidExtraction(res.ProviderID, "provider is not cataloged")
	}
	if res.ChannelID != "" && res.ChannelID != reservedAdhocID && cat.Channel(res.ChannelID) == nil {
		return invalidExtraction(res.ProviderID, "channel is not cataloged")
	}
	return nil
}

func streamErrorStatus(err error) int {
	if se, ok := err.(*StreamsError); ok {
		switch se.Kind {
		case ErrKindInvalidExtraction, ErrKindNoMatch:
			return http.StatusBadRequest
		}
	}
	return http.StatusConflict
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *Adapter) respondRouteError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if wantsJSON(r) {
		respondJSON(w, status, routeErrorResponse{Error: msg})
		return
	}
	a.respondPanelWithError(w, r, msg)
}

func refreshStatusView(status RefreshStatus) refreshStatusResponse {
	out := refreshStatusResponse{
		ProviderID: status.ProviderID,
		Source:     status.Source,
		FetchedAt:  status.FetchedAt,
	}
	if status.Err != nil {
		out.Error = status.Err.Error()
	}
	return out
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
