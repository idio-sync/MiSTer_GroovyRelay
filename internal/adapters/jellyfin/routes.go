package jellyfin

import (
	"html"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// UIRoutes returns the JF-specific HTTP routes mounted under
// /ui/adapter/jellyfin/ by the UI server.
func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: http.MethodPost, Path: "link/start", Handler: a.handleLinkStart},
		{Method: http.MethodPost, Path: "link/cancel", Handler: a.handleLinkCancel},
		{Method: http.MethodPost, Path: "unlink", Handler: a.handleUnlink},
		{Method: http.MethodGet, Path: "status", Handler: a.handleStatusFragment},
	}
}

// handleStatusFragment renders the sidebar status badge fragment.
// Mirrors plex's /ui/adapter/plex/status pattern. Used by htmx polls.
func (a *Adapter) handleStatusFragment(w http.ResponseWriter, r *http.Request) {
	st := a.Status()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	state := html.EscapeString(st.State.String())
	lastErr := html.EscapeString(st.LastError)
	_, _ = w.Write([]byte(
		`<span class="adapter-badge adapter-badge-` + state + `">` + state + `</span>` +
			`<div class="adapter-error">` + lastErr + `</div>`,
	))
}
