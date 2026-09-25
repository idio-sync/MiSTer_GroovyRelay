package companion

import (
	"net/http"
	"strings"
)

const (
	extensionCORSAllowHeaders = "Content-Type, X-Bridge-Extension, HX-Request"
	extensionCORSAllowMethods = "GET, POST, DELETE, PUT, PATCH, OPTIONS"
)

func handleExtensionCORSPreflight(w http.ResponseWriter, r *http.Request) {
	setExtensionCORSHeaders(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func setExtensionCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !isExtensionOrigin(origin) {
		return
	}
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Headers", extensionCORSAllowHeaders)
	w.Header().Set("Access-Control-Allow-Methods", extensionCORSAllowMethods)
	if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
}

// isExtensionOrigin reports whether origin uses one of the browser
// extension schemes. Match is scheme-prefix only; the host portion (a
// per-install UUID assigned by the browser) is not validated because
// there is no way for the bridge to know the operator's install UUIDs
// in advance, and the security model does not depend on it (header
// presence is the trust signal). See spec §"isExtensionOrigin
// scheme-prefix only".
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
