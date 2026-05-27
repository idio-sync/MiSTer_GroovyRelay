package chassis

import (
	"errors"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (s *Server) handleStreamsCast(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}
	provider := strings.TrimSpace(r.PostFormValue("provider"))
	channel := strings.TrimSpace(r.PostFormValue("channel"))
	if provider == "" || channel == "" {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}
	if s.streamsCaster == nil {
		writeCastJSON(w, http.StatusNotFound, false, "NOT FOUND")
		return
	}
	if err := s.streamsCaster.CastChannel(r.Context(), provider, channel); err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writeCastJSON(w, qerr.Status, false, qerr.Chip)
			return
		}
		writeCastJSON(w, http.StatusInternalServerError, false, "CAST FAILED")
		return
	}
	writeCastJSON(w, http.StatusOK, true, "")
}
