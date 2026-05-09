package dlna

import "net/http"

// Phase 1 placeholder body returned for every /dlna/* request. Real
// descriptors and SOAP handlers replace these in T4 (see
// docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
// §Service Action Surface). The 503 status communicates "the route
// exists but the implementation is not yet ready" to anyone polling
// the bridge during phased rollout.
const phase1PlaceholderBody = "DLNA Phase 1 placeholder; descriptors and SOAP arrive in T4"

// MountPublicRoutes mounts the 13 protocol-side HTTP routes on the
// shared bridge mux. Called by main.go via the
// adapters.PublicRouteProvider interface BEFORE the UI server mounts
// /ui/*; the paths under /dlna/* are deliberately disjoint from the
// settings UI tree so DLNA control points (which cannot send the
// origin headers the /ui/* CSRF middleware requires) reach the
// adapter without going through that middleware.
//
// Phase 1 ships stubs returning 503 — the goal is just to claim the
// path patterns so a controller's request matches a handler instead
// of hitting the mux's default 404. T3/T4 replace these handlers.
//
// **Non-standard HTTP methods.** UPnP eventing uses SUBSCRIBE and
// UNSUBSCRIBE, which Go's http.ServeMux does NOT dispatch on. Even
// the Go 1.22+ method-prefixed pattern syntax ("GET /path") only
// recognizes standard methods. To accept SUBSCRIBE/UNSUBSCRIBE we
// register a single path-only pattern for each event endpoint and
// switch on r.Method inside the handler. The Phase 1 stub accepts
// every method (returning 503) because the goal is route claiming;
// T4 will tighten dispatch and reject unknown methods with 405.
func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	// Descriptors: device + per-service SCPD documents (GET only in v1).
	// We use Go 1.22's method-prefixed pattern syntax for these — they
	// only need to dispatch on GET, and the pattern enforces 405 on
	// other methods automatically.
	mux.HandleFunc("GET /dlna/device.xml", stubHandler)
	mux.HandleFunc("GET /dlna/AVTransport.xml", stubHandler)
	mux.HandleFunc("GET /dlna/ConnectionManager.xml", stubHandler)
	mux.HandleFunc("GET /dlna/RenderingControl.xml", stubHandler)

	// SOAP control endpoints: POST only.
	mux.HandleFunc("POST /dlna/control/AVTransport", stubHandler)
	mux.HandleFunc("POST /dlna/control/ConnectionManager", stubHandler)
	mux.HandleFunc("POST /dlna/control/RenderingControl", stubHandler)

	// Eventing endpoints: SUBSCRIBE/UNSUBSCRIBE are non-standard HTTP
	// methods — register path-only and dispatch in the handler. Phase
	// 1 stubs accept any method to keep route registration simple;
	// T4 will switch to method-aware handlers that 405 on Unknown.
	mux.HandleFunc("/dlna/event/AVTransport", stubHandler)
	mux.HandleFunc("/dlna/event/RenderingControl", stubHandler)
	mux.HandleFunc("/dlna/event/ConnectionManager", stubHandler)
}

// stubHandler returns 503 with the Phase 1 placeholder body. Kept as a
// package-level function (not a method) so it has no surprising
// adapter-state dependencies — every Phase 1 route shares the same
// handler. T3/T4 replace it with per-route handlers that close over
// *Adapter for state access.
func stubHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(phase1PlaceholderBody))
}
