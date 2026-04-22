package ui

import (
	"net/http"
	"net/url"
)

// csrfMiddleware rejects state-changing requests that appear to be
// cross-origin. Two cooperating checks:
//
//  1. Sec-Fetch-Site: modern browsers always send this. Accepted
//     values: "same-origin", "same-site", "none" (direct navigation /
//     typed URL / bookmark).  "cross-site" is rejected.
//
//  2. Origin: fallback for clients that don't send Sec-Fetch-Site
//     (curl, older browsers, programmatic use). Must match the
//     request Host.
//
// Design §3: defense in depth against a hostile page in the
// operator's browser issuing cross-origin POSTs to the bridge on
// the LAN. Reject-by-default on POST/PUT/DELETE; GET/HEAD/OPTIONS
// are always allowed (reads have no side effects).
func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if s := r.Header.Get("Sec-Fetch-Site"); s != "" {
			switch s {
			case "same-origin", "same-site", "none":
				next.ServeHTTP(w, r)
				return
			default:
				http.Error(w, "CSRF: cross-site request refused", http.StatusForbidden)
				return
			}
		}

		origin := r.Header.Get("Origin")
		if origin == "" {
			http.Error(w, "CSRF: missing Origin / Sec-Fetch-Site on state-changing request", http.StatusForbidden)
			return
		}
		u, err := url.Parse(origin)
		if err != nil {
			http.Error(w, "CSRF: malformed Origin", http.StatusForbidden)
			return
		}
		if u.Host != r.Host {
			http.Error(w, "CSRF: Origin does not match Host", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
