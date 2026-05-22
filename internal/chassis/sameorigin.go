package chassis

import "net/http"

// requireSameOrigin rejects POST requests whose Sec-Fetch-Site is not
// same-origin or same-site. Chassis POST endpoints are driven by bundled
// first-party JS; non-browser clients must opt in by setting the header.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			switch r.Header.Get("Sec-Fetch-Site") {
			case "same-origin", "same-site":
				// allowed
			default:
				writeJSONError(w, http.StatusForbidden, "cross-site request blocked")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
