package chassis

import (
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// catalogProviderFormResponse is the GET .../provider/{id} body: the authoring
// form for the edit form. Field tags mirror catalogProviderRequest so the
// client maps the read and write symmetrically.
type catalogProviderFormResponse struct {
	OK          bool                    `json:"ok"`
	ID          string                  `json:"id"`
	DisplayName string                  `json:"displayName"`
	BadgeLabel  string                  `json:"badgeLabel"`
	BadgeColor  string                  `json:"badgeColor"`
	Groups      []catalogGroupRequest   `json:"groups"`
	Channels    []catalogChannelRequest `json:"channels"`
}

func catalogProviderFormResponseFrom(form adapters.UserProviderForm) catalogProviderFormResponse {
	groups := make([]catalogGroupRequest, 0, len(form.Groups))
	for _, g := range form.Groups {
		groups = append(groups, catalogGroupRequest{ID: g.ID, Name: g.Name, Order: g.Order})
	}
	channels := make([]catalogChannelRequest, 0, len(form.Channels))
	for _, c := range form.Channels {
		channels = append(channels, catalogChannelRequest{
			ID: c.ID, Name: c.Name, URL: c.URL, Kind: c.Kind,
			PlayMode: c.PlayMode, GroupID: c.GroupID, Order: c.Order,
		})
	}
	return catalogProviderFormResponse{
		OK: true, ID: form.ID, DisplayName: form.DisplayName, BadgeLabel: form.BadgeLabel,
		BadgeColor: form.BadgeColor, Groups: groups, Channels: channels,
	}
}

// requireSameOriginRead guards a read endpoint that returns authored URLs.
// requireSameOrigin only checks unsafe methods, so this helper applies the same
// browser signal policy to GET: allow same-origin/same-site, otherwise require a
// matching Origin or Referer when Sec-Fetch-Site is absent (older clients).
func requireSameOriginRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Sec-Fetch-Site") {
		case "same-origin", "same-site":
			// allowed
		case "":
			if !sameOriginByOriginOrReferer(r) {
				writeJSONError(w, http.StatusForbidden, "cross-site request blocked")
				return
			}
		default:
			writeJSONError(w, http.StatusForbidden, "cross-site request blocked")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleCatalogProviderForm serves the authoring form for one user provider so
// the edit form opens pre-populated. Read-only; the mounted route wraps it with
// requireSameOriginRead because it returns authored URLs. 404 when the viewer is
// unwired or the id is unknown/non-user.
func (s *Server) handleCatalogProviderForm(w http.ResponseWriter, r *http.Request) {
	if s.userProviderViewer == nil {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	form, ok := s.userProviderViewer.UserProviderForm(r.PathValue("id"))
	if !ok {
		writeSettingsChip(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	writeJSON(w, http.StatusOK, catalogProviderFormResponseFrom(form))
}
