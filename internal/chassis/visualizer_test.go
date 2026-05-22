package chassis

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestIsSupportedVisualizerMode_AcceptsAllSupported(t *testing.T) {
	for _, mode := range config.SupportedVisualizerModes() {
		if !isSupportedVisualizerMode(mode) {
			t.Errorf("isSupportedVisualizerMode(%q) = false, want true", mode)
		}
	}
}

func TestIsSupportedVisualizerMode_RejectsRadialSpectrum(t *testing.T) {
	if isSupportedVisualizerMode("radial_spectrum") {
		t.Fatal(`isSupportedVisualizerMode("radial_spectrum") = true, want false`)
	}
}

func TestIsSupportedVisualizerMode_RejectsArbitraryStrings(t *testing.T) {
	for _, mode := range []string{"", "  ", "STEREO_SCOPE", "stereo-scope", "garbage", "../../../etc/passwd"} {
		if isSupportedVisualizerMode(mode) {
			t.Errorf("isSupportedVisualizerMode(%q) = true, want false", mode)
		}
	}
}

func TestWriteJSONError_FormatsBodyAndHeaders(t *testing.T) {
	w := httptest.NewRecorder()

	writeJSONError(w, 400, "bad")

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&body); err != nil {
		t.Fatalf("Decode body: %v", err)
	}
	if body["error"] != "bad" {
		t.Fatalf("body[error] = %q, want %q", body["error"], "bad")
	}
}

func TestWriteJSONErrorWithMode_IncludesMode(t *testing.T) {
	w := httptest.NewRecorder()

	writeJSONErrorWithMode(w, 400, "unsupported visualizer mode", "radial_spectrum")

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&body); err != nil {
		t.Fatalf("Decode body: %v", err)
	}
	if body["error"] != "unsupported visualizer mode" {
		t.Fatalf("body[error] = %q, want %q", body["error"], "unsupported visualizer mode")
	}
	if body["mode"] != "radial_spectrum" {
		t.Fatalf("body[mode] = %q, want %q", body["mode"], "radial_spectrum")
	}
}
