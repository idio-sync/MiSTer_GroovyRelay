package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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

// fakeRouteAdapter is the minimum adapter needed to exercise route mounting.
// It implements adapters.Adapter + adapters.RouteProvider and registers
// one route per HTTP method we expect the mounter to support.
type fakeRouteAdapter struct {
	hits map[string]int // method+path -> count
}

func (f *fakeRouteAdapter) Name() string                                    { return "fake" }
func (f *fakeRouteAdapter) DisplayName() string                             { return "Fake" }
func (f *fakeRouteAdapter) Fields() []adapters.FieldDef                     { return nil }
func (f *fakeRouteAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeRouteAdapter) IsEnabled() bool                                 { return true }
func (f *fakeRouteAdapter) Start(context.Context) error                     { return nil }
func (f *fakeRouteAdapter) Stop() error                                     { return nil }
func (f *fakeRouteAdapter) Status() adapters.Status                         { return adapters.Status{} }
func (f *fakeRouteAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return 0, nil
}

func (f *fakeRouteAdapter) UIRoutes() []adapters.Route {
	mk := func(method string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.hits[method+" "+r.URL.Path]++
			w.WriteHeader(http.StatusOK)
		}
	}
	return []adapters.Route{
		{Method: "GET", Path: "thing", Handler: mk("GET")},
		{Method: "POST", Path: "thing", Handler: mk("POST")},
		{Method: "DELETE", Path: "thing", Handler: mk("DELETE")},
		{Method: "PUT", Path: "thing", Handler: mk("PUT")},
		{Method: "PATCH", Path: "thing", Handler: mk("PATCH")},
	}
}

func TestServer_Mount_HonorsAllRouteMethods(t *testing.T) {
	reg := adapters.NewRegistry()
	fa := &fakeRouteAdapter{hits: map[string]int{}}
	if err := reg.Register(fa); err != nil {
		t.Fatalf("register fake: %v", err)
	}

	srv, err := New(Config{Registry: reg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	for _, method := range []string{"GET", "POST", "DELETE", "PUT", "PATCH"} {
		req, err := http.NewRequest(method, ts.URL+"/ui/adapter/fake/thing", nil)
		if err != nil {
			t.Fatalf("build %s: %v", method, err)
		}
		// Mounter wraps non-GET in csrfMiddleware. The middleware
		// (internal/ui/csrf.go:31) accepts Sec-Fetch-Site values
		// "same-origin" / "same-site" / "none". Set it for non-GET so
		// the request bypasses the middleware's CSRF rejection without
		// having to fabricate a matching Origin header.
		if method != "GET" {
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s do: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", method, resp.StatusCode)
		}
	}

	for _, method := range []string{"GET", "POST", "DELETE", "PUT", "PATCH"} {
		key := method + " /ui/adapter/fake/thing"
		if fa.hits[key] != 1 {
			t.Errorf("hits[%q] = %d, want 1", key, fa.hits[key])
		}
	}
}
