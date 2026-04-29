package ui

import (
	"fmt"
	"net/http"
)

// handleSidebarDots returns the per-adapter status dots as a fragment
// of OOB-swap <span> elements. htmx targets each <span id="dot-...">
// in the sidebar template and swaps its outerHTML, leaving the
// surrounding <a> link and active-state intact. Spec §5.4.
//
// Polled every 3 seconds by the sidebar template.
func (s *Server) handleSidebarDots(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Registry == nil {
		http.Error(w, "registry not wired", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	for _, a := range s.cfg.Registry.List() {
		// Per spec §5.1 (PR 2), we eventually filter to enabled
		// adapters only. PR 1 keeps every registered adapter visible
		// to preserve existing behavior; the IsEnabled() filter
		// lands with the sidebar reflow in PR 2.
		// dotClass is the existing helper at server.go:280 — reused
		// here so the sidebar partial and the legacy /ui/sidebar/status
		// handler agree on State→class mapping.
		fmt.Fprintf(w, `<span id="dot-%s" class="dot %s" hx-swap-oob="outerHTML"></span>`+"\n",
			a.Name(), dotClass(a.Status().State))
	}
}
