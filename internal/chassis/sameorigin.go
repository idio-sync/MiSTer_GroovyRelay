package chassis

import (
	"net/http"
	"net/url"
	"strings"
)

// requireSameOrigin rejects unsafe-method requests whose Sec-Fetch-Site is not
// same-origin or same-site. Chassis mutation endpoints are driven by bundled
// first-party JS; non-browser clients must opt in by setting the header.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafeMethod(r.Method) {
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
		}
		next.ServeHTTP(w, r)
	})
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func sameOriginByOriginOrReferer(r *http.Request) bool {
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return sameRequestOrigin(r, origin)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return sameRequestOrigin(r, referer)
	}
	return false
}

func sameRequestOrigin(r *http.Request, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.URL != nil && r.URL.Scheme != "" {
		scheme = r.URL.Scheme
	}
	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, host)
}
