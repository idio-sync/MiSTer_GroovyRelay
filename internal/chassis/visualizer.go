package chassis

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// VisualizerViewer is the narrow read-only view of the live bridge's
// visualizer mode. *core.Manager satisfies it structurally via
// VisualizerMode(). Tests inject fakes. Mirrors Spec 2's SessionViewer
// pattern.
type VisualizerViewer interface {
	VisualizerMode() string
}

// VisualizerSaver persists a new visualizer mode and refreshes the
// live in-memory bridge config. main.go wires this via a small adapter
// struct over uiserver.BridgeSaver.SaveVisualizerMode so chassis does
// not depend on internal/uiserver. The chassis HTTP handler validates
// the mode before invoking the saver.
type VisualizerSaver interface {
	SaveVisualizerMode(mode string) error
}

func isSupportedVisualizerMode(mode string) bool {
	for _, supported := range config.SupportedVisualizerModes() {
		if mode == supported {
			return true
		}
	}
	return false
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(body)
}

func writeJSONErrorWithMode(w http.ResponseWriter, status int, msg, mode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"error": msg, "mode": mode})
	_, _ = w.Write(body)
}

func (s *Server) handleVisualizerPost(w http.ResponseWriter, r *http.Request) {
	if s.visualizerSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "visualizer save not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}

	mode := strings.TrimSpace(r.PostFormValue("mode"))
	if mode == "" {
		writeJSONError(w, http.StatusBadRequest, "missing mode field")
		return
	}
	if !isSupportedVisualizerMode(mode) {
		writeJSONErrorWithMode(w, http.StatusBadRequest, "unsupported visualizer mode", mode)
		return
	}

	if err := s.visualizerSaver.SaveVisualizerMode(mode); err != nil {
		log.Printf("chassis: visualizer save failed: mode=%q err=%v", mode, err)
		writeJSONError(w, http.StatusInternalServerError, "internal save failure")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
