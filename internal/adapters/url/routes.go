package url

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// UIRoutes returns the adapter-owned HTTP routes. Mounted under
// /ui/adapter/url/ by the UI server's RouteProvider scan. POST and
// DELETE routes are wrapped in csrfMiddleware by the mounter.
//
// Routes (v1.1):
//
//	GET    /panel                 — htmx-rendered panel fragment
//	POST   /play                  — start a cast
//	POST   /cookies               — set cookies file (Netscape format)
//	DELETE /cookies               — clear cookies file
func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: "POST", Path: "play", Handler: a.handlePlay},
		{Method: "GET", Path: "panel", Handler: a.handlePanel},
		{Method: "POST", Path: "cookies", Handler: a.handleCookiesSet},
		{Method: "DELETE", Path: "cookies", Handler: a.handleCookiesClear},
	}
}
