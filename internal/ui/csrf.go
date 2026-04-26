package ui

import (
	"net/http"
	"net/url"
	"strings"
)

// csrfMiddleware rejects state-changing requests that appear to be
// cross-origin. Three cooperating checks, evaluated in order:
//
//  1. Extension bypass tier: requests bearing X-Bridge-Extension: 1
//     AND an extension-scheme Origin (moz-extension://,
//     chrome-extension://, safari-web-extension://) are accepted.
//     This is the entry point used by the companion browser
//     extension; spec docs/specs/2026-04-25-companion-extension-design.md
//     §"Bridge-side change". Both signals are required: header alone
//     or extension-scheme Origin alone falls through.
//
//  2. Sec-Fetch-Site: modern browsers always send this. Accepted
//     values: "same-origin", "same-site", "none" (direct navigation /
//     typed URL / bookmark).  "cross-site" is rejected.
//
//  3. Origin: fallback for clients that don't send Sec-Fetch-Site
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

		// Extension bypass tier (first-pass). Both signals required;
		// either alone falls through to the existing logic below.
		if r.Header.Get("X-Bridge-Extension") == "1" {
			if isExtensionOrigin(r.Header.Get("Origin")) {
				next.ServeHTTP(w, r)
				return
			}
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

// isExtensionOrigin reports whether origin's scheme is one of the
// browser extension schemes. Match is scheme-prefix only; the
// host portion (a per-install UUID assigned by the browser) is
// not validated because there is no way for the bridge to know
// the operator's install UUIDs in advance, and the security
// model does not depend on it (header presence is the trust
// signal). See spec §"isExtensionOrigin scheme-prefix only".
func isExtensionOrigin(origin string) bool {
	switch {
	case strings.HasPrefix(origin, "moz-extension://"):
		return true
	case strings.HasPrefix(origin, "chrome-extension://"):
		return true
	case strings.HasPrefix(origin, "safari-web-extension://"):
		return true
	}
	return false
}
