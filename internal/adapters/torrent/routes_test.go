package torrent

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestUIRoutes(t *testing.T) {
	a := &Adapter{}
	routes := a.UIRoutes()
	want := map[string]bool{
		"GET live":    false,
		"GET panel":   false,
		"GET status":  false,
		"POST play":   false,
		"POST upload": false,
		"POST stop":   false,
	}
	for _, route := range routes {
		want[route.Method+" "+route.Path] = true
		if route.Handler == nil {
			t.Fatalf("route %s %s has nil handler", route.Method, route.Path)
		}
	}
	for key, seen := range want {
		if !seen {
			t.Fatalf("missing route %s", key)
		}
	}
}

func TestHandlePlayMagnetUsesFormField(t *testing.T) {
	core := &recordingCore{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), &fakeTorrentClient{}, core)
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/torrent/play", strings.NewReader("magnet=+magnet%3A%3Fxt%3Durn%3Abtih%3A0123456789abcdef0123456789abcdef01234567+"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handlePlay(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	if len(core.reqs) != 1 {
		t.Fatalf("core StartSession calls = %d, want 1", len(core.reqs))
	}
	if !strings.Contains(rr.Body.String(), "torrent-panel") {
		t.Fatalf("non-JSON play did not return panel: %q", rr.Body.String())
	}
}

func TestHandlePlayMagnetJSONReturnsTokenOnly(t *testing.T) {
	a := newStartedTestAdapter(t, startedTorrentConfig(), &fakeTorrentClient{}, &recordingCore{})
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/torrent/play", strings.NewReader("magnet=magnet%3A%3Fxt%3Durn%3Abtih%3A0123456789abcdef0123456789abcdef01234567"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handlePlay(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "torrent-panel") {
		t.Fatalf("JSON play returned panel HTML: %q", rr.Body.String())
	}
	var got StartedSession
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v body=%q", err, rr.Body.String())
	}
	if got.Token == "" {
		t.Fatalf("JSON response missing token: %#v", got)
	}
}

func TestHandleUploadRejectsOversizedTorrent(t *testing.T) {
	a := &Adapter{}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("torrent_file", "too-large.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(strings.Repeat("x", maxTorrentUploadBytes+1))); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/torrent/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	a.handleUpload(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 body=%q", rr.Code, rr.Body.String())
	}
}

func TestHandleUploadRejectsMissingTorrentFile(t *testing.T) {
	a := &Adapter{}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("ignored", "x"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/torrent/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleUpload(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
}

func TestHandleStopCallsCore(t *testing.T) {
	core := &recordingCore{}
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   core,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/torrent/stop", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStop(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	if core.stops != 1 {
		t.Fatalf("core stops = %d, want 1", core.stops)
	}
}

func TestHandleStopRejectsForeignActiveSession(t *testing.T) {
	rec := &recordingCore{status: core.SessionStatus{AdapterRef: "plex:123"}}
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/torrent/stop", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStop(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%q, want 409", rr.Code, rr.Body.String())
	}
	if rec.stops != 0 {
		t.Fatalf("core stops = %d, want 0 for foreign session", rec.stops)
	}
}
