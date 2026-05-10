package ui

import (
	"net/http"
	"strings"
)

// firstRunGuard returns an http.Handler middleware that enforces the
// first-run wizard. Decision rule (parent spec §5.3 + PR2 spec §S5):
//
//  1. saver nil OR !IsFirstRun() → pass through (hot path).
//  2. path == "/ui/setup" OR begins with "/ui/setup/" → pass through.
//  3. path begins with "/ui/static/" → pass through.
//  4. path matches "/ui/adapter/<name>/link/*" or "/ui/adapter/<name>/save"
//     → pass through. The wizard renders adapter-owned link UI (Plex PIN
//     flow, Jellyfin password form) and per-adapter save inside its
//     step pages; those POSTs target the adapter routes. Without this
//     bypass, every wizard linking interaction would 409 and the user
//     could never complete the wizard.
//  5. method == POST → 409 Conflict (don't 302 a POST).
//  6. otherwise (GET to a wrapped UI route) → 302 to /ui/setup.
//
// See docs/specs/2026-05-08-ui-redesign-pr2-design.md §S5 for the full
// route-wrapping matrix.
func firstRunGuard(saver FirstRunAware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if saver == nil || !saver.IsFirstRun() {
				next.ServeHTTP(w, r)
				return
			}

			p := r.URL.Path
			if p == "/ui/setup" || strings.HasPrefix(p, "/ui/setup/") {
				next.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(p, "/ui/static/") {
				next.ServeHTTP(w, r)
				return
			}
			if isWizardAdapterRoute(p) {
				next.ServeHTTP(w, r)
				return
			}

			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte("first-run not complete; visit /ui/setup"))
				return
			}

			http.Redirect(w, r, "/ui/setup", http.StatusFound)
		})
	}
}

// isWizardAdapterRoute returns true if path matches an adapter route
// the wizard needs during first-run linking — /ui/adapter/<name>/link/*
// (PIN flow, password submit, link status polling) and
// /ui/adapter/<name>/save (per-adapter form save). The function is
// path-shape-only; it does NOT check the adapter actually exists in
// the registry — the underlying handler returns 404 for unknown
// adapters, which is fine.
func isWizardAdapterRoute(p string) bool {
	const prefix = "/ui/adapter/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	rest := p[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return false
	}
	tail := rest[slash+1:]
	return tail == "save" || strings.HasPrefix(tail, "link/")
}
