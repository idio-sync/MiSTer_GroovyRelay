package chassis

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

type fakeVolumeSaver struct {
	calls []int
	err   error
}

func (f *fakeVolumeSaver) SaveOutputVolume(volume int) error {
	f.calls = append(f.calls, volume)
	return f.err
}

func postVolume(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/volume", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleVolumePost(w, req)
	return w
}

func TestHandleVolumePost_AcceptsBoundsAndMiddle(t *testing.T) {
	for _, volume := range []int{0, 50, 100} {
		t.Run(strconv.Itoa(volume), func(t *testing.T) {
			saver := &fakeVolumeSaver{}
			s := &Server{volumeSaver: saver}
			w := postVolume(t, s, url.Values{"output_volume": {strconv.Itoa(volume)}}.Encode())
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
			}
			if w.Body.Len() != 0 {
				t.Fatalf("body = %q, want empty", w.Body.String())
			}
			if len(saver.calls) != 1 || saver.calls[0] != volume {
				t.Fatalf("saver calls = %#v, want [%d]", saver.calls, volume)
			}
		})
	}
}

func TestHandleVolumePost_RejectsMalformedMissingNonIntegerAndOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		body string
		msg  string
	}{
		{name: "malformed", body: "%zz", msg: "malformed form body"},
		{name: "missing", body: "", msg: "missing output_volume field"},
		{name: "blank", body: url.Values{"output_volume": {""}}.Encode(), msg: "missing output_volume field"},
		{name: "non integer", body: url.Values{"output_volume": {"loud"}}.Encode(), msg: "output_volume must be an integer"},
		{name: "negative", body: url.Values{"output_volume": {"-1"}}.Encode(), msg: "output_volume must be in 0..100"},
		{name: "too high", body: url.Values{"output_volume": {"101"}}.Encode(), msg: "output_volume must be in 0..100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saver := &fakeVolumeSaver{}
			s := &Server{volumeSaver: saver}
			w := postVolume(t, s, tc.body)
			assertJSONError(t, w, http.StatusBadRequest, tc.msg)
			if len(saver.calls) != 0 {
				t.Fatalf("saver calls = %#v, want none", saver.calls)
			}
		})
	}
}

func TestHandleVolumePost_NilSaverReturnsServiceUnavailable(t *testing.T) {
	s := &Server{}
	w := postVolume(t, s, url.Values{"output_volume": {"50"}}.Encode())
	assertJSONError(t, w, http.StatusServiceUnavailable, "volume save not configured")
}

func TestHandleVolumePost_SaverErrorReturnsGenericInternalError(t *testing.T) {
	saverErr := errors.New("disk path /secret/config.toml unavailable")
	saver := &fakeVolumeSaver{err: saverErr}
	s := &Server{volumeSaver: saver}
	var logs bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(orig) })

	w := postVolume(t, s, url.Values{"output_volume": {"50"}}.Encode())

	assertJSONError(t, w, http.StatusInternalServerError, "internal save failure")
	if strings.Contains(w.Body.String(), saverErr.Error()) || strings.Contains(w.Body.String(), "/secret/config.toml") {
		t.Fatalf("client body leaked saver error: %q", w.Body.String())
	}
	if got := logs.String(); !strings.Contains(got, saverErr.Error()) || !strings.Contains(got, "volume=50") {
		t.Fatalf("log = %q, want volume and full saver error", got)
	}
}

func TestMount_RegistersVolumeRouteThroughRequireSameOrigin(t *testing.T) {
	cfg := nonZeroConfig()
	cfg.VolumeSaver = &fakeVolumeSaver{}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	mux := http.NewServeMux()
	s.Mount(mux)

	body := url.Values{"output_volume": {"50"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/ui/volume", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status without same-origin = %d, want %d; body=%q", w.Code, http.StatusForbidden, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control without same-origin = %q, want %q", got, "no-store")
	}

	req = httptest.NewRequest(http.MethodPost, "/ui/volume", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status with same-origin = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control with same-origin = %q, want %q", got, "no-store")
	}
}
