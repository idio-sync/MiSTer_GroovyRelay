package url

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// UIRoutes returns the adapter-owned HTTP routes. They mount under
// /ui/adapter/url/ courtesy of the UI server's RouteProvider scan.
// POST routes are wrapped in csrfMiddleware by the mounter.
func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: "POST", Path: "play", Handler: a.handlePlay},
		{Method: "GET", Path: "panel", Handler: a.handlePanel},
	}
}
