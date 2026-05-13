package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// newTestServer constructs a *Server + mux for tests. Pass option
// funcs to populate Config fields before construction; do NOT call
// Mount again afterward.
func newTestServer(t *testing.T, opts ...func(*Config)) (*Server, *http.ServeMux) {
	t.Helper()
	cfg := Config{Registry: adapters.NewRegistry()}
	for _, opt := range opts {
		opt(&cfg)
	}
	s, err := New(cfg)
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

func TestShellLoadsStreamsArtworkScript(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/ui/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, `<script src="/ui/static/streams-artwork.js" defer></script>`) {
		t.Fatalf("shell missing streams artwork script: %s", body)
	}
}

func TestStaticStreamsArtworkScriptServed(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/streams-artwork.js", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "data-streams-artwork") ||
		!strings.Contains(rr.Body.String(), "streams-artwork-failed") {
		t.Fatalf("unexpected artwork script body: %s", rr.Body.String())
	}
}

func TestStaticAppCSSHidesArtworkFallbackUntilImageFails(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/app.css", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		".streams-provider-art:not(.streams-artwork-failed) + .streams-provider-wordmark",
		".streams-provider-art.streams-artwork-failed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app.css missing streams artwork fallback rule %q: %s", want, body)
		}
	}
}

func TestStaticAppCSSScopesStreamsWidePanelToRegularAdapterPanel(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/app.css", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "#panel:has(> #panel-content > .gr-config-head):has(.streams-panel)") {
		t.Fatalf("streams wide panel rule should require the regular adapter header: %s", body)
	}
	if strings.Contains(body, "#panel:has(.streams-panel) {\n") {
		t.Fatalf("streams wide panel rule should not match setup wizard panels: %s", body)
	}
}

func TestStaticAppCSSKeepsStreamsMobileCategoryTabsReadable(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/app.css", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	want := "grid-template-columns: repeat(auto-fit, minmax(min(132px, 100%), 1fr));"
	if !strings.Contains(body, want) {
		t.Fatalf("mobile streams category rail should keep readable tab minimum %q: %s", want, body)
	}
}

// fakeRouteAdapter is the minimum adapter needed to exercise route mounting.
// It implements adapters.Adapter + adapters.RouteProvider and registers
// one route per HTTP method we expect the mounter to support.
type fakeRouteAdapter struct {
	hits map[string]int // method+path -> count
}

func (f *fakeRouteAdapter) Name() string                                     { return "fake" }
func (f *fakeRouteAdapter) DisplayName() string                              { return "Fake" }
func (f *fakeRouteAdapter) Fields() []adapters.FieldDef                      { return nil }
func (f *fakeRouteAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeRouteAdapter) IsEnabled() bool                                  { return true }
func (f *fakeRouteAdapter) Start(context.Context) error                      { return nil }
func (f *fakeRouteAdapter) Stop() error                                      { return nil }
func (f *fakeRouteAdapter) Status() adapters.Status                          { return adapters.Status{} }
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

func TestServer_Mount_AllowsExtensionCORSForUIRoutes(t *testing.T) {
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

	origin := "moz-extension://abcd-1234"
	preflight, err := http.NewRequest(http.MethodOptions, ts.URL+"/ui/adapter/fake/thing", nil)
	if err != nil {
		t.Fatalf("build preflight: %v", err)
	}
	preflight.Header.Set("Origin", origin)
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "content-type, x-bridge-extension")
	resp, err := ts.Client().Do(preflight)
	if err != nil {
		t.Fatalf("preflight do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "x-bridge-extension") {
		t.Fatalf("preflight Access-Control-Allow-Headers = %q, want x-bridge-extension", got)
	}

	getReq, err := http.NewRequest(http.MethodGet, ts.URL+"/ui/adapter/fake/thing", nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	getReq.Header.Set("Origin", origin)
	getResp, err := ts.Client().Do(getReq)
	if err != nil {
		t.Fatalf("GET do: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getResp.StatusCode)
	}
	if got := getResp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("GET Access-Control-Allow-Origin = %q, want %q", got, origin)
	}

	postReq, err := http.NewRequest(http.MethodPost, ts.URL+"/ui/adapter/fake/thing", nil)
	if err != nil {
		t.Fatalf("build POST: %v", err)
	}
	postReq.Header.Set("Origin", origin)
	postReq.Header.Set("Sec-Fetch-Site", "cross-site")
	postReq.Header.Set("X-Bridge-Extension", "1")
	postResp, err := ts.Client().Do(postReq)
	if err != nil {
		t.Fatalf("POST do: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", postResp.StatusCode)
	}
	if got := postResp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("POST Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
}

func TestShell_IncludesConsoleEasterEgg(t *testing.T) {
	_, mux := newTestServer(t)
	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "GroovyRelay") || !strings.Contains(body, "console.log") {
		t.Errorf("missing console easter egg block in shell")
	}
}

func TestStaticAssets_AppCSSIncludesPR2bClasses(t *testing.T) {
	_, mux := newTestServer(t)
	r := httptest.NewRequest("GET", "/ui/static/app.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}
	body := w.Body.String()
	for _, cls := range []string{".gr-hero", ".gr-tile", ".gr-activity-entry", ".gr-callout", ".gr-build-grid", "prefers-reduced-motion"} {
		if !strings.Contains(body, cls) {
			t.Errorf("missing %q in app.css", cls)
		}
	}
}

func TestStaticAssets_AppCSSIncludesPR2cClasses(t *testing.T) {
	_, mux := newTestServer(t)
	r := httptest.NewRequest("GET", "/ui/static/app.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}
	body := w.Body.String()
	for _, cls := range []string{".gr-stepper", ".gr-pick", ".gr-form-locked", ".gr-wizard-foot", ".gr-config", ".gr-config-head", "#panel:has(.gr-config-head)"} {
		if !strings.Contains(body, cls) {
			t.Errorf("missing %q in app.css", cls)
		}
	}
}

func TestMount_RegistersSidebarDotsRoute(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", state: adapters.StateRunning}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	s, err := New(Config{Registry: reg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/sidebar/dots", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("/ui/sidebar/dots not registered: got 404")
	}
	// Distinguish the dots handler from the /ui/ catch-all that
	// renders the shell template — only handleSidebarDots emits
	// hx-swap-oob spans for registered adapters.
	body := w.Body.String()
	if !strings.Contains(body, `hx-swap-oob`) {
		t.Errorf("dots route did not produce OOB span output (route may be served by shell catch-all)\nbody: %s", body)
	}
}

// newMockAdapter returns a minimal uiStubAdapter with the given name and
// enabled state. Used by shellData filter tests.
func newMockAdapter(name string, enabled bool) *uiStubAdapter {
	return &uiStubAdapter{name: name, enabled: enabled, enabledSet: true}
}

func TestShellData_IncludesDisabledAdapters(t *testing.T) {
	srv, _ := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(
			newMockAdapter("plex", true),     // enabled
			newMockAdapter("jellyfin", true), // enabled
			newMockAdapter("url", false),     // disabled — still listed, dot off
		)
	})
	data := srv.shellData()
	names := []string{}
	for _, a := range data.Adapters {
		names = append(names, a.Name)
	}
	if !reflect.DeepEqual(names, []string{"plex", "jellyfin", "url"}) {
		t.Errorf("sidebar adapters: got %v, want [plex jellyfin url]", names)
	}
	for _, a := range data.Adapters {
		if a.Name == "url" && a.DotClass != "off" {
			t.Errorf("disabled adapter url: dot=%q, want off", a.DotClass)
		}
	}
}

func TestShell_RendersAddSourceLink(t *testing.T) {
	_, mux := newTestServer(t)
	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "+ Add source") {
		t.Errorf("missing + Add source link")
	}
	if !strings.Contains(body, `href="/ui/setup?step=adapters"`) {
		t.Errorf("missing wizard link target")
	}
}

func TestShell_ReadPagesUsePreviewMainLayout(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
		c.EventLog = eventlog.New(8)
	})

	for _, path := range []string{"/ui/", "/ui/diagnostics"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s status = %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), `<main class="gr-main" id="panel">`) {
			t.Errorf("%s should use preview gr-main panel layout: %s", path, w.Body.String())
		}
	}
}

func TestShell_ConfigPagesUsePreviewConfigLayout(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.BridgeSaver = &fakeBridgeSaver{}
		c.Registry = adapters.NewRegistryWith(&uiStubAdapter{name: "jellyfin", displayName: "Jellyfin"})
	})

	for _, tc := range []struct {
		path  string
		title string
	}{
		{path: "/ui/bridge", title: "Bridge"},
		{path: "/ui/adapter/jellyfin", title: "Jellyfin"},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s status = %d", tc.path, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `<main class="gr-config" id="panel">`) {
			t.Errorf("%s should use preview gr-config panel layout: %s", tc.path, body)
		}
		if !strings.Contains(body, `<div class="gr-config-head">`) {
			t.Errorf("%s should render the config header marker for HTMX-safe spacing: %s", tc.path, body)
		}
		if !strings.Contains(body, "<h1>"+tc.title+"</h1>") {
			t.Errorf("%s missing title %q: %s", tc.path, tc.title, body)
		}
	}
}

func TestShellRendersNowPlayingBannerOnNonSetupPages(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.BridgeSaver = &fakeBridgeSaver{}
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	for _, path := range []string{"/ui/", "/ui/bridge", "/ui/diagnostics"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `id="gr-now-playing"`) {
				t.Fatalf("missing now-playing banner on %s: %s", path, rr.Body.String())
			}
		})
	}
}

func TestShellNavigationTargetsPanelContentSoBannerPersists(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{`id="gr-now-playing"`, `id="panel-content"`, `hx-target="#panel-content"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `hx-target="#panel"`) {
		t.Fatalf("shell still targets #panel and would replace the banner: %s", body)
	}
}

func TestSetupShellDoesNotRenderNowPlayingBanner(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/setup", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if strings.Contains(rr.Body.String(), `id="gr-now-playing"`) {
		t.Fatalf("setup page rendered now-playing banner: %s", rr.Body.String())
	}
}
