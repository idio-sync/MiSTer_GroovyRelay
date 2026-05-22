package chassis

import (
	"encoding/json"
	"net/http"

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
