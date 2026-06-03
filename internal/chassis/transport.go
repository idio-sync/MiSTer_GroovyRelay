package chassis

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

var allowedTransportActions = map[string]struct{}{
	adapters.PlaybackActionPause:    {},
	adapters.PlaybackActionResume:   {},
	adapters.PlaybackActionStop:     {},
	adapters.PlaybackActionPrevious: {},
	adapters.PlaybackActionNext:     {},
	adapters.PlaybackActionReplay:   {},
}

func transportNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleTransportAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if s.transportController == nil {
		writeJSONError(w, http.StatusInternalServerError, "transport controller not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}

	adapterRef := strings.TrimSpace(r.PostFormValue("adapter_ref"))
	if adapterRef == "" {
		writeJSONError(w, http.StatusBadRequest, "missing adapter_ref field")
		return
	}

	generationRaw := strings.TrimSpace(r.PostFormValue("generation"))
	if generationRaw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing generation field")
		return
	}
	generation, err := strconv.ParseUint(generationRaw, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid generation field")
		return
	}
	if generation == 0 {
		writeJSONError(w, http.StatusBadRequest, "generation must be greater than zero")
		return
	}

	action := strings.TrimSpace(r.PostFormValue("action"))
	if action == "" {
		writeJSONError(w, http.StatusBadRequest, "missing action field")
		return
	}
	if action == adapters.PlaybackActionSeek {
		writeJSONError(w, http.StatusBadRequest, "seek must use the /ui/transport/seek route")
		return
	}
	if _, ok := allowedTransportActions[action]; !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown action")
		return
	}

	_, err = s.transportController.HandlePlaybackAction(r.Context(), adapters.PlaybackActionRequest{
		Action:     action,
		AdapterRef: adapterRef,
		Generation: generation,
	})
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if errors.Is(err, adapters.ErrActiveSessionChanged) {
		writeJSONError(w, http.StatusConflict, adapters.ErrActiveSessionChangedMessage)
		return
	}
	if errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	log.Printf("chassis: transport action dispatch failed: action=%q adapter_ref=%q generation=%d err=%v", action, adapterRef, generation, err)
	writeJSONError(w, http.StatusInternalServerError, "internal dispatch failure")
}

func (s *Server) handleTransportSeek(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if s.transportController == nil {
		writeJSONError(w, http.StatusInternalServerError, "transport controller not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}

	adapterRef := strings.TrimSpace(r.PostFormValue("adapter_ref"))
	if adapterRef == "" {
		writeJSONError(w, http.StatusBadRequest, "missing adapter_ref field")
		return
	}

	generationRaw := strings.TrimSpace(r.PostFormValue("generation"))
	if generationRaw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing generation field")
		return
	}
	generation, err := strconv.ParseUint(generationRaw, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid generation field")
		return
	}
	if generation == 0 {
		writeJSONError(w, http.StatusBadRequest, "generation must be greater than zero")
		return
	}

	offsetRaw := strings.TrimSpace(r.PostFormValue("offset_ms"))
	offset, err := strconv.Atoi(offsetRaw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "offset_ms must be an integer")
		return
	}

	_, err = s.transportController.HandlePlaybackAction(r.Context(), adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionSeek,
		AdapterRef: adapterRef,
		Generation: generation,
		OffsetMS:   offset,
	})
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if errors.Is(err, adapters.ErrActiveSessionChanged) {
		writeJSONError(w, http.StatusConflict, adapters.ErrActiveSessionChangedMessage)
		return
	}
	if errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	log.Printf("chassis: transport seek dispatch failed: adapter_ref=%q generation=%d offset_ms=%d err=%v", adapterRef, generation, offset, err)
	writeJSONError(w, http.StatusInternalServerError, "internal dispatch failure")
}
