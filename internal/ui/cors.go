package ui

import "net/http"

const (
	extensionCORSAllowHeaders = "Content-Type, X-Bridge-Extension, HX-Request"
	extensionCORSAllowMethods = "GET, POST, DELETE, PUT, PATCH, OPTIONS"
)

func extensionCORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setExtensionCORSHeaders(w, r)
		next.ServeHTTP(w, r)
	})
}

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
}
