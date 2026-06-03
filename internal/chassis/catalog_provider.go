package chassis

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// --- SSE envelope shapes (Phase 5 defines the shape; Phase 6 wires emission
// into handleEvents and renders the chips client-side). ---

// catalogProviderEnvelope is one provider in a `catalog` SSE event.
type catalogProviderEnvelope struct {
	ID          string                 `json:"id"`
	DisplayName string                 `json:"displayName"`
	BadgeLabel  string                 `json:"badgeLabel"`
	BadgeClass  string                 `json:"badgeClass"`
	Live        bool                   `json:"live"`
	Groups      []catalogGroupEnvelope `json:"groups"`
}

type catalogGroupEnvelope struct {
	ID       string                   `json:"id"`
	Name     string                   `json:"name"`
	Channels []catalogChannelEnvelope `json:"channels"`
}

type catalogChannelEnvelope struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PlayMode string `json:"playMode"`
	Live     bool   `json:"live"`
}

// catalogEnvelope is the `catalog` SSE event payload.
type catalogEnvelope struct {
	Providers []catalogProviderEnvelope `json:"providers"`
}

// providerStatusEnvelope is the `providerStatus` SSE event payload: per-channel
// enumeration status plus the optional auto-enable signal (spec §8). The chassis
// renders "Enumerating…"/"N videos"/"✗ error" chips and the auto-enable toast
// from this in Phase 6.
type providerStatusEnvelope struct {
	Provider           string                  `json:"provider"`
	Channels           []channelStatusEnvelope `json:"channels,omitempty"`
	AutoEnabledStreams string                  `json:"autoEnabledStreams,omitempty"` // "on" | "restart-required"
}

type channelStatusEnvelope struct {
	Channel   string `json:"channel"`
	State     string `json:"state"` // "ready" | "pending" | "error"
	ItemCount int    `json:"itemCount"`
}

func buildCatalogProviderEnvelope(p adapters.CatalogProvider) catalogProviderEnvelope {
	groups := make([]catalogGroupEnvelope, 0, len(p.Groups))
	for _, g := range p.Groups {
		chans := make([]catalogChannelEnvelope, 0, len(g.Channels))
		for _, c := range g.Channels {
			chans = append(chans, catalogChannelEnvelope{ID: c.ID, Name: c.Name, PlayMode: c.PlayMode, Live: c.Live})
		}
		groups = append(groups, catalogGroupEnvelope{ID: g.ID, Name: g.Name, Channels: chans})
	}
	return catalogProviderEnvelope{
		ID: p.ID, DisplayName: p.DisplayName, BadgeLabel: p.BadgeLabel,
		BadgeClass: p.BadgeClass, Live: p.Live, Groups: groups,
	}
}

// --- request/response wire shapes (camelCase to match the receiver JS) ---

type catalogProviderRequest struct {
	DisplayName string                  `json:"displayName"`
	BadgeLabel  string                  `json:"badgeLabel"`
	BadgeColor  string                  `json:"badgeColor"`
	Groups      []catalogGroupRequest   `json:"groups"`
	Channels    []catalogChannelRequest `json:"channels"`
}

type catalogGroupRequest struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

type catalogChannelRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	PlayMode string `json:"playMode"`
	GroupID  string `json:"groupId"`
	Order    int    `json:"order"`
}

type catalogProviderResponse struct {
	OK                 bool                     `json:"ok"`
	Provider           *catalogProviderEnvelope `json:"provider,omitempty"`
	ClearedSlots       []int                    `json:"clearedSlots,omitempty"`
	AutoEnabledStreams string                   `json:"autoEnabledStreams,omitempty"`
}

func (r catalogProviderRequest) toForm(id string) adapters.UserProviderForm {
	groups := make([]adapters.UserGroupForm, 0, len(r.Groups))
	for _, g := range r.Groups {
		groups = append(groups, adapters.UserGroupForm{ID: g.ID, Name: g.Name, Order: g.Order})
	}
	channels := make([]adapters.UserChannelForm, 0, len(r.Channels))
	for _, c := range r.Channels {
		channels = append(channels, adapters.UserChannelForm{
			ID: c.ID, Name: c.Name, URL: c.URL, Kind: c.Kind,
			PlayMode: c.PlayMode, GroupID: c.GroupID, Order: c.Order,
		})
	}
	return adapters.UserProviderForm{
		ID: id, DisplayName: r.DisplayName, BadgeLabel: r.BadgeLabel,
		BadgeColor: r.BadgeColor, Groups: groups, Channels: channels,
	}
}

func writeCatalogError(w http.ResponseWriter, err error) {
	var qerr *adapters.QuickCastError
	if errors.As(err, &qerr) {
		writeSettingsChip(w, qerr.Status, qerr.Chip)
		return
	}
	writeSettingsChip(w, http.StatusInternalServerError, "SAVE FAILED")
}

func (s *Server) handleCatalogProviderCreate(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	var req catalogProviderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	res, err := s.userProviderEditor.CreateUserProvider(r.Context(), req.toForm(""))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	provider := buildCatalogProviderEnvelope(res.Provider)
	resp := catalogProviderResponse{OK: true, Provider: &provider}
	if res.AutoEnableNeeded {
		// Provider is already saved; auto-enable is best-effort. On failure we
		// fall back to the existing "restart the bridge" UX tier (spec §10).
		if s.ensureAdapterStarted == nil {
			resp.AutoEnabledStreams = "restart-required"
		} else if err := s.ensureAdapterStarted("streams"); err != nil {
			slog.Warn("chassis: auto-enable streams failed", "err", err)
			resp.AutoEnabledStreams = "restart-required"
		} else {
			resp.AutoEnabledStreams = "on"
		}
	}
	s.refreshSnapshotNow()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCatalogProviderUpdate(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	id := r.PathValue("id")
	var req catalogProviderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	res, err := s.userProviderEditor.UpdateUserProvider(r.Context(), id, req.toForm(id))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	s.refreshSnapshotNow()
	provider := buildCatalogProviderEnvelope(res.Provider)
	writeJSON(w, http.StatusOK, catalogProviderResponse{
		OK: true, Provider: &provider, ClearedSlots: res.ClearedSlots,
	})
}

func (s *Server) handleCatalogProviderDelete(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	id := r.PathValue("id")
	res, err := s.userProviderEditor.DeleteUserProvider(r.Context(), id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	s.refreshSnapshotNow()
	writeJSON(w, http.StatusOK, catalogProviderResponse{OK: true, ClearedSlots: res.ClearedSlots})
}

func (s *Server) handleCatalogProviderReorder(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	id := r.PathValue("id")
	var body struct {
		Channels []adapters.UserOrderEntry `json:"channels"`
		Groups   []adapters.UserOrderEntry `json:"groups"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&body); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if err := s.userProviderEditor.ReorderUserProvider(r.Context(), id, adapters.ReorderRequest{
		Channels: body.Channels, Groups: body.Groups,
	}); err != nil {
		writeCatalogError(w, err)
		return
	}
	s.refreshSnapshotNow()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCatalogChannelVerify(w http.ResponseWriter, r *http.Request) {
	if s.userProviderEditor == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	var body struct {
		URL  string `json:"url"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&body); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	res, err := s.userProviderEditor.VerifyChannel(r.Context(), adapters.VerifyChannelRequest{URL: body.URL, Kind: body.Kind})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
