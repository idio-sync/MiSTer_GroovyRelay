package chassis

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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

func TestHandleVisualizerPost_TrimsAndSavesSupportedModesInOrder(t *testing.T) {
	saver := &fakeVisualizerSaver{}
	s := &Server{visualizerSaver: saver}

	// Spec 4 trims HTTP input before exact SupportedVisualizerModes
	// membership validation; normalization is still intentionally absent.
	for _, mode := range []string{" " + config.VisualizerModeStereoScope + " ", config.VisualizerModeOscilloscopeWave} {
		w := postVisualizerMode(t, s, mode)

		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
		}
		if w.Body.Len() != 0 {
			t.Fatalf("body = %q, want empty", w.Body.String())
		}
	}

	want := []string{config.VisualizerModeStereoScope, config.VisualizerModeOscilloscopeWave}
	if got := saver.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("saved modes = %#v, want %#v", got, want)
	}
}

func TestHandleVisualizerPost_RejectsMissingEmptyAndWhitespaceMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing", body: ""},
		{name: "empty", body: url.Values{"mode": {""}}.Encode()},
		{name: "whitespace", body: url.Values{"mode": {" \t\n "}}.Encode()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saver := &fakeVisualizerSaver{}
			s := &Server{visualizerSaver: saver}
			req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			s.handleVisualizerPost(w, req)

			assertJSONError(t, w, http.StatusBadRequest, "missing mode field")
			if got := saver.calls(); len(got) != 0 {
				t.Fatalf("saver calls = %#v, want none", got)
			}
		})
	}
}

func TestHandleVisualizerPost_RejectsMalformedFormBody(t *testing.T) {
	saver := &fakeVisualizerSaver{}
	s := &Server{visualizerSaver: saver}
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.handleVisualizerPost(w, req)

	assertJSONError(t, w, http.StatusBadRequest, "malformed form body")
	if got := saver.calls(); len(got) != 0 {
		t.Fatalf("saver calls = %#v, want none", got)
	}
}

func TestHandleVisualizerPost_RejectsUnsupportedModeAndEchoesMode(t *testing.T) {
	saver := &fakeVisualizerSaver{}
	s := &Server{visualizerSaver: saver}

	w := postVisualizerMode(t, s, " STEREO_SCOPE ")

	assertJSONErrorWithMode(t, w, http.StatusBadRequest, "unsupported visualizer mode", "STEREO_SCOPE")
	if got := saver.calls(); len(got) != 0 {
		t.Fatalf("saver calls = %#v, want none", got)
	}
}

func TestHandleVisualizerPost_RejectsRadialSpectrumAsDeferred(t *testing.T) {
	saver := &fakeVisualizerSaver{}
	s := &Server{visualizerSaver: saver}

	w := postVisualizerMode(t, s, "radial_spectrum")

	assertJSONErrorWithMode(t, w, http.StatusBadRequest, "unsupported visualizer mode", "radial_spectrum")
	if got := saver.calls(); len(got) != 0 {
		t.Fatalf("saver calls = %#v, want none", got)
	}
}

func TestHandleVisualizerPost_NilSaverReturnsServiceUnavailable(t *testing.T) {
	s := &Server{}

	w := postVisualizerMode(t, s, config.VisualizerModeStereoScope)

	assertJSONError(t, w, http.StatusServiceUnavailable, "visualizer save not configured")
}

func TestHandleVisualizerPost_SaverErrorReturnsGenericInternalError(t *testing.T) {
	saverErr := errors.New("disk path /secret/config.toml unavailable")
	saver := &fakeVisualizerSaver{err: saverErr}
	s := &Server{visualizerSaver: saver}
	var logs bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(orig) })

	w := postVisualizerMode(t, s, config.VisualizerModeStereoScope)

	assertJSONError(t, w, http.StatusInternalServerError, "internal save failure")
	if strings.Contains(w.Body.String(), saverErr.Error()) || strings.Contains(w.Body.String(), "/secret/config.toml") {
		t.Fatalf("client body leaked saver error: %q", w.Body.String())
	}
	if got := logs.String(); !strings.Contains(got, saverErr.Error()) || !strings.Contains(got, `mode="stereo_scope"`) {
		t.Fatalf("log = %q, want mode and full saver error", got)
	}
}

func TestHandleVisualizerPost_RapidSequentialClicksPreserveOrder(t *testing.T) {
	saver := &fakeVisualizerSaver{}
	s := &Server{visualizerSaver: saver}
	want := []string{
		config.VisualizerModeRetroAnalyzer,
		config.VisualizerModeOscilloscopeWave,
		config.VisualizerModeStereoScope,
		config.VisualizerModeRetroAnalyzer,
		config.VisualizerModeStereoScope,
	}

	for _, mode := range want {
		w := postVisualizerMode(t, s, mode)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
		}
	}

	if got := saver.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("saved modes = %#v, want %#v", got, want)
	}
}

func TestMount_VisualizerPostBlocksCrossSiteAndDoesNotSave(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(url.Values{
		"mode": {config.VisualizerModeStereoScope},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	assertJSONError(t, w, http.StatusForbidden, "cross-site request blocked")
	if got := saver.calls(); len(got) != 0 {
		t.Fatalf("saver calls = %#v, want none", got)
	}
}

func TestMount_VisualizerPostSameOriginSavesMode(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(url.Values{
		"mode": {config.VisualizerModeStereoScope},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
	if got, want := saver.calls(), []string{config.VisualizerModeStereoScope}; !reflect.DeepEqual(got, want) {
		t.Fatalf("saved modes = %#v, want %#v", got, want)
	}
}

func TestMount_VisualizerGetReturnsMethodNotAllowed(t *testing.T) {
	t.Parallel()
	saver := &fakeVisualizerSaver{}
	cfg := nonZeroConfig()
	cfg.VisualizerSaver = saver
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodGet, "/ui/visualizer", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusMethodNotAllowed, w.Body.String())
	}
	if got := saver.calls(); len(got) != 0 {
		t.Fatalf("saver calls = %#v, want none", got)
	}
}

func postVisualizerMode(t *testing.T, s *Server, mode string) *httptest.ResponseRecorder {
	t.Helper()
	body := url.Values{"mode": {mode}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVisualizerPost(w, req)
	return w
}

func assertJSONError(t *testing.T, w *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, status, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json")
	}
	var body map[string]string
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&body); err != nil {
		t.Fatalf("Decode body: %v; raw=%q", err, w.Body.String())
	}
	if body["error"] != msg {
		t.Fatalf("body[error] = %q, want %q", body["error"], msg)
	}
}

func assertJSONErrorWithMode(t *testing.T, w *httptest.ResponseRecorder, status int, msg, mode string) {
	t.Helper()
	assertJSONError(t, w, status, msg)
	var body map[string]string
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&body); err != nil {
		t.Fatalf("Decode body: %v; raw=%q", err, w.Body.String())
	}
	if body["mode"] != mode {
		t.Fatalf("body[mode] = %q, want %q", body["mode"], mode)
	}
}
