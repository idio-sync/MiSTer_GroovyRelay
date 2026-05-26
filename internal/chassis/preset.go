package chassis

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (s *Server) handlePresetCast(w http.ResponseWriter, r *http.Request) {
	slotStr := r.PathValue("slot")
	slot, err := strconv.Atoi(slotStr)
	if err != nil || slot < 1 || slot > 12 {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD SLOT")
		return
	}
	if s.presetCaster == nil {
		writeCastJSON(w, http.StatusNotFound, false, "NOT FOUND")
		return
	}
	if err := s.presetCaster.CastPreset(r.Context(), slot); err != nil {
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
