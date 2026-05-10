package ui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// mockLinkAwareAdapter satisfies adapters.Adapter + adapters.LinkAware
// + ExtraHTMLProvider + RouteProvider so we can drive a full wizard
// pass-through: /ui/setup/step/<name> renders, link route fires, wizard
// advances to done.
type mockLinkAwareAdapter struct {
	name    string
	enabled bool
	linked  bool
}

func newMockLinkAwareAdapter(name string) *mockLinkAwareAdapter {
	return &mockLinkAwareAdapter{name: name}
}

func (a *mockLinkAwareAdapter) Name() string { return a.name }

func (a *mockLinkAwareAdapter) DisplayName() string {
	if strings.EqualFold(a.name, "plex") {
		return "Plex"
	}
	return a.name
}

func (a *mockLinkAwareAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{{
		Key:      "host",
		Label:    "Host",
		Kind:     adapters.KindText,
		Required: true,
	}}
}

func (a *mockLinkAwareAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }

func (a *mockLinkAwareAdapter) IsEnabled() bool { return a.enabled }

func (a *mockLinkAwareAdapter) Start(context.Context) error { return nil }

func (a *mockLinkAwareAdapter) Stop() error { return nil }

func (a *mockLinkAwareAdapter) Status() adapters.Status {
	return adapters.Status{State: adapters.StateRunning}
}

func (a *mockLinkAwareAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

// SetEnabled satisfies EnableSetter so handleSetupAdapterConfigPOST
// can flip the in-memory enabled flag.
func (a *mockLinkAwareAdapter) SetEnabled(v bool) { a.enabled = v }

// LinkPhase satisfies adapters.LinkAware.
func (a *mockLinkAwareAdapter) LinkPhase() string {
	if a.linked {
		return "linked"
	}
	return "idle"
}

// IsLinked satisfies adapters.LinkAware.
func (a *mockLinkAwareAdapter) IsLinked() bool { return a.linked }

// ExtraPanelHTML satisfies ExtraHTMLProvider — renders the link button
// that the test asserts is present in the adapter setup page body.
func (a *mockLinkAwareAdapter) ExtraPanelHTML() template.HTML {
	return template.HTML(fmt.Sprintf(
		`<button hx-post="/ui/adapter/%s/link/start" hx-target="#link">Link</button>`,
		a.name,
	))
}

// UIRoutes satisfies adapters.RouteProvider. The link/start handler
// sets both linked=true AND enabled=true so firstIncompleteStep sees
// the adapter as enabled after the link step, allowing the wizard to
// advance to done without a separate adapter-config form POST.
func (a *mockLinkAwareAdapter) UIRoutes() []adapters.Route {
	return []adapters.Route{{
		Method: "POST",
		Path:   "link/start",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			a.linked = true
			a.enabled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<div id="link">linked</div>`))
		},
	}}
}

// Compile-time assertions.
var _ adapters.Adapter = (*mockLinkAwareAdapter)(nil)
var _ adapters.LinkAware = (*mockLinkAwareAdapter)(nil)
var _ ExtraHTMLProvider = (*mockLinkAwareAdapter)(nil)
var _ adapters.RouteProvider = (*mockLinkAwareAdapter)(nil)
var _ EnableSetter = (*mockLinkAwareAdapter)(nil)

// TestSetupE2E_HappyPath exercises the full wizard route stack:
//
//  1. GET /ui/ → 302 to /ui/setup (first-run guard fires)
//  2. POST /ui/setup/step/bridge → saves host, 303 to adapters
//  3. POST /ui/setup/step/adapters → selects plex, 303 to plex configure
//  4. GET /ui/setup → bridge set, no enabled adapter → plex step
//  5. GET /ui/setup/step/plex → renders link UI from ExtraPanelHTML
//  6. POST /ui/adapter/plex/link/start → mock marks linked+enabled
//  7. GET /ui/setup → bridge set, adapter enabled → done
//  8. POST /ui/setup/done → flag cleared, 303 to /ui/
//  9. GET /ui/setup?step=adapters → re-entry honours explicit ?step
func TestSetupE2E_HappyPath(t *testing.T) {
	mock := newMockLinkAwareAdapter("plex")
	srv, mux, saver := newTestServerWithFirstRun(t, true, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(mock)
	})
	_ = srv

	// Step 1: GET /ui/ → first-run guard redirects to /ui/setup
	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/ui/setup" {
		t.Fatalf("step1 GET /ui/: %d → %q, want 302 → /ui/setup",
			w.Code, w.Header().Get("Location"))
	}

	// Step 2: POST /ui/setup/step/bridge with valid bridge fields → 303 to adapters
	form := url.Values{
		"mister.host":        {"192.168.1.50"},
		"mister.port":        {"32100"},
		"mister.source_port": {"32101"},
	}
	r = httptest.NewRequest("POST", "/ui/setup/step/bridge", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("step2 POST step/bridge: %d, body: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/ui/setup/step/adapters" {
		t.Fatalf("step2 POST step/bridge: want redirect to /ui/setup/step/adapters, got %q", loc)
	}
	if saver.saved == nil || saver.saved.MiSTer.Host != "192.168.1.50" {
		t.Fatal("step2: bridge save was not called or host not persisted")
	}

	// Step 3: POST /ui/setup/step/adapters selecting plex → 303 to plex
	form = url.Values{"adapters": {"plex"}}
	r = httptest.NewRequest("POST", "/ui/setup/step/adapters", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("step3 POST step/adapters: %d, body: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/ui/setup/step/plex" {
		t.Fatalf("step3 POST step/adapters: want redirect to /ui/setup/step/plex, got %q", loc)
	}

	// Step 4: GET /ui/setup → bridge set, no adapter enabled yet →
	// firstIncompleteStep returns "adapters" (picker step, not plex configure).
	// The explicit redirect to /ui/setup/step/plex came from the picker POST in
	// step 3; the wizard's automatic routing only knows "adapters" vs "done".
	r = httptest.NewRequest("GET", "/ui/setup", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	loc := w.Header().Get("Location")
	if loc != "/ui/setup/step/adapters" {
		t.Fatalf("step4 GET /ui/setup: expected /ui/setup/step/adapters (no adapter enabled), got %q (code %d)",
			loc, w.Code)
	}

	// Step 5: GET /ui/setup/step/plex → renders adapter configure page with link UI
	r = httptest.NewRequest("GET", "/ui/setup/step/plex", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("step5 GET step/plex: %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `hx-post="/ui/adapter/plex/link/start"`) {
		t.Fatalf("step5: adapter setup page did not render link htmx UI; body: %s", w.Body.String())
	}

	// Step 6: POST /ui/adapter/plex/link/start → mock marks linked+enabled
	r = httptest.NewRequest("POST", "/ui/adapter/plex/link/start", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("step6 POST link/start: %d, body: %s", w.Code, w.Body.String())
	}
	if !mock.linked {
		t.Fatal("step6: link/start did not mark mock adapter linked")
	}
	if !mock.enabled {
		t.Fatal("step6: link/start did not mark mock adapter enabled")
	}

	// Step 7: GET /ui/setup → adapter now enabled → redirects to done
	r = httptest.NewRequest("GET", "/ui/setup", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.HasSuffix(w.Header().Get("Location"), "/done") {
		t.Fatalf("step7 GET /ui/setup: expected redirect to done, got %q (code %d)",
			w.Header().Get("Location"), w.Code)
	}

	// Step 8: POST /ui/setup/done → flag cleared, 303 to /ui/
	r = httptest.NewRequest("POST", "/ui/setup/done", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/ui/" {
		t.Fatalf("step8 POST /ui/setup/done: %d → %q, want 302 → /ui/",
			w.Code, w.Header().Get("Location"))
	}
	if saver.IsFirstRun() {
		t.Error("step8: first-run flag was not dismissed after /ui/setup/done")
	}

	// Step 9: GET /ui/setup?step=adapters → re-entry honours explicit ?step
	// (wizard is complete at this point, but ?step overrides firstIncompleteStep)
	r = httptest.NewRequest("GET", "/ui/setup?step=adapters", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/ui/setup/step/adapters" {
		t.Fatalf("step9 re-entry: %d → %q, want 302 → /ui/setup/step/adapters",
			w.Code, w.Header().Get("Location"))
	}
}
