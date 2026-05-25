package chassis

import (
	"log"
	"net/http"
	"strconv"
	"strings"
)

// VolumeViewer is the read-only source for the live global output volume.
// *core.Manager satisfies this structurally via OutputVolume(). Tests use
// fakes. When nil, snapshots fall back to startup config.
type VolumeViewer interface {
	OutputVolume() int
}

// VolumeSaver persists a new global output volume and applies it live.
// main.go wires this through uiserver.BridgeSaver.SaveOutputVolume.
type VolumeSaver interface {
	SaveOutputVolume(volume int) error
}

func (s *Server) handleVolumePost(w http.ResponseWriter, r *http.Request) {
	if s.volumeSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "volume save not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}
	raw := strings.TrimSpace(r.PostFormValue("output_volume"))
	if raw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing output_volume field")
		return
	}
	volume, err := strconv.Atoi(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "output_volume must be an integer")
		return
	}
	if volume < 0 || volume > 100 {
		writeJSONError(w, http.StatusBadRequest, "output_volume must be in 0..100")
		return
	}
	if err := s.volumeSaver.SaveOutputVolume(volume); err != nil {
		log.Printf("chassis: volume save failed: volume=%d err=%v", volume, err)
		writeJSONError(w, http.StatusInternalServerError, "internal save failure")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
