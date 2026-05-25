package chassis

import (
	"errors"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (s *Server) handleAUXStartPost(w http.ResponseWriter, r *http.Request) {
	if s.aux == nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "AUX input unavailable")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed AUX request")
		return
	}
	inputID := strings.TrimSpace(r.Form.Get("input_id"))
	if _, err := s.aux.StartAUX(r.Context(), inputID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, adapters.ErrSourceUnavailable) {
			status = http.StatusUnprocessableEntity
		}
		writeJSONError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAUXStopPost(w http.ResponseWriter, r *http.Request) {
	if s.aux == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed AUX request")
		return
	}
	_, err := s.aux.StopAUX(r.Context(), strings.TrimSpace(r.Form.Get("input_id")))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
