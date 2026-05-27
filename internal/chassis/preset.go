package chassis

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

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

const (
	presetStarFormStarred  = "starred"
	presetStarFormProvider = "provider"
	presetStarFormChannel  = "channel"
	presetMoveFormFrom     = "from"
	presetMoveFormTo       = "to"
)

func (s *Server) handlePresetStar(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writePresetEditError(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	provider := strings.TrimSpace(r.PostFormValue(presetStarFormProvider))
	channel := strings.TrimSpace(r.PostFormValue(presetStarFormChannel))
	starredRaw := r.PostFormValue(presetStarFormStarred)
	if provider == "" || channel == "" {
		writePresetEditError(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	// Strict lexical form: ONLY the literal strings "true" and "false"
	// are accepted. strconv.ParseBool would also accept "1", "t", "TRUE",
	// etc. — rejected here so the wire is deterministic and sloppy
	// clients get a fast 400 instead of mysterious downstream behavior.
	var starred bool
	switch starredRaw {
	case "true":
		starred = true
	case "false":
		starred = false
	default:
		writePresetEditError(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if s.presetEditor == nil {
		writePresetEditError(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	res, err := s.presetEditor.SetPresetStarred(r.Context(), provider, channel, starred)
	if err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writePresetEditError(w, qerr.Status, qerr.Chip)
			return
		}
		writePresetEditError(w, http.StatusInternalServerError, "CAST FAILED")
		return
	}
	s.refreshSnapshotNow()
	writePresetStarSuccess(w, res.Starred, res.Slot, res.Cleared)
}

func (s *Server) handlePresetMove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writePresetEditError(w, http.StatusBadRequest, "BAD SLOT")
		return
	}
	fromStr := r.PostFormValue(presetMoveFormFrom)
	toStr := r.PostFormValue(presetMoveFormTo)
	from, errFrom := strconv.Atoi(fromStr)
	to, errTo := strconv.Atoi(toStr)
	if errFrom != nil || errTo != nil || from < 1 || from > 12 || to < 1 || to > 12 {
		writePresetEditError(w, http.StatusBadRequest, "BAD SLOT")
		return
	}
	if s.presetEditor == nil {
		// 404 precedes the from==to no-op short-circuit so a connectivity
		// test never gets a misleading 200 from a chassis that has no
		// editor wired.
		writePresetEditError(w, http.StatusNotFound, "NOT FOUND")
		return
	}
	if from == to {
		// No-op success; chassis still refreshes snapshot for uniformity
		// (the events-loop diff suppresses the spurious emit).
		s.refreshSnapshotNow()
		writePresetMoveSuccess(w)
		return
	}
	if err := s.presetEditor.MovePreset(r.Context(), from, to); err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writePresetEditError(w, qerr.Status, qerr.Chip)
			return
		}
		writePresetEditError(w, http.StatusInternalServerError, "CAST FAILED")
		return
	}
	s.refreshSnapshotNow()
	writePresetMoveSuccess(w)
}
