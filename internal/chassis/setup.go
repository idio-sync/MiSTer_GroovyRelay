package chassis

import (
	"encoding/json"
	"net/http"
)

// handleSetupStatus reports the configured-enough sub-checks plus an
// overall "complete" flag. complete is true when no first-run controller
// is wired or the sentinel is already dismissed (nothing to do). GET,
// non-mutating — intentionally not same-origin wrapped (that wrapper only
// enforces POST).
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	st := s.setupStatus()
	complete := st.Complete()
	if s.firstRun == nil || !s.firstRun.IsFirstRun() {
		complete = true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]bool{
		"hostSet":       st.HostSet,
		"sourceEnabled": st.SourceEnabled,
		"complete":      complete,
	})
}

// handleSetupFinish completes first-run setup. Order:
//   - no controller wired OR sentinel already dismissed → 200 (no-op),
//   - criteria incomplete → 409 naming the unmet item,
//   - DismissFirstRun error → 500,
//   - otherwise dismiss + 200.
//
// Idempotent under concurrency (DismissFirstRun is an idempotent os.Create
// and IsFirstRun re-Stats each call), so a duplicate finish or a race with
// /ui/setup observes the dismissed sentinel and returns 200.
func (s *Server) handleSetupFinish(w http.ResponseWriter, r *http.Request) {
	if s.firstRun == nil || !s.firstRun.IsFirstRun() {
		w.WriteHeader(http.StatusOK)
		return
	}
	st := s.setupStatus()
	if !st.Complete() {
		w.WriteHeader(http.StatusConflict)
		msg := "set a MiSTer host"
		if st.HostSet {
			msg = "enable a source"
		}
		_, _ = w.Write([]byte(msg))
		return
	}
	if err := s.firstRun.DismissFirstRun(); err != nil {
		http.Error(w, "finish failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
