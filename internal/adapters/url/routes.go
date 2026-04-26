package url

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// UIRoutes returns the adapter-owned HTTP routes. Mounted under
// /ui/adapter/url/ by the UI server's RouteProvider scan
// (internal/ui/server.go:138-153). POST and DELETE routes are
// wrapped in csrfMiddleware by the mounter.
//
// Routes (v1.5):
//
//	GET    /panel                 — htmx-rendered panel fragment
//	POST   /play                  — start a cast
//	POST   /pause                 — pause active session
//	POST   /resume                — resume paused session (Duration-aware)
//	POST   /stop                  — stop active session (URL-only ownership guard)
//	POST   /replay                — re-cast lastURL from offset 0
//	POST   /seek                  — seek active session (offset_ms form field)
//	POST   /history/play          — re-cast a history entry
//	POST   /history/delete        — remove a history entry
//	POST   /cookies               — set cookies file (Netscape format)
//	DELETE /cookies               — clear cookies file
func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: "POST", Path: "play", Handler: a.handlePlay},
		{Method: "POST", Path: "pause", Handler: a.handlePause},
		{Method: "POST", Path: "resume", Handler: a.handleResume},
		{Method: "POST", Path: "stop", Handler: a.handleStop},
		{Method: "POST", Path: "replay", Handler: a.handleReplay},
		{Method: "POST", Path: "seek", Handler: a.handleSeek},
		{Method: "POST", Path: "history/play", Handler: a.handleHistoryPlay},
		{Method: "POST", Path: "history/delete", Handler: a.handleHistoryDelete},
		{Method: "GET", Path: "panel", Handler: a.handlePanel},
		{Method: "POST", Path: "cookies", Handler: a.handleCookiesSet},
		{Method: "DELETE", Path: "cookies", Handler: a.handleCookiesClear},
	}
}
