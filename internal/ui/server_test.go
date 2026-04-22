package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func newTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	reg := adapters.NewRegistry()
	s, err := New(Config{Registry: reg})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return s, mux
}

func TestServer_RootRedirectsToUI(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "http://localhost:32500")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusMovedPermanently && rw.Code != http.StatusFound {
		t.Errorf("status = %d, want 301 or 302", rw.Code)
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/" {
		t.Errorf("Location = %q, want /ui/", loc)
	}
}

func TestServer_ShellPageRenders(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/ui/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, "MiSTer GroovyRelay") {
		t.Error("shell missing title")
	}
	if !strings.Contains(body, "htmx.min.js") {
		t.Error("shell missing htmx script tag")
	}
}

func TestServer_StaticCSS(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/ui/static/app.css", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	if ct := rw.Header().Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("Content-Type = %q, want */css", ct)
	}
}

func TestServer_StaticHtmx(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/ui/static/htmx.min.js", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
}

func TestServer_StaticFont(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/ui/static/fonts/InterTight-400.woff2", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	if ct := rw.Header().Get("Content-Type"); !strings.Contains(ct, "font") && !strings.Contains(ct, "woff2") {
		t.Errorf("Content-Type = %q, want font/woff2-ish", ct)
	}
}
