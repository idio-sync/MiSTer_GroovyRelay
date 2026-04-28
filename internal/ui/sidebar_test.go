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

// uiStubAdapter is a minimal Adapter usable for UI-package tests.
// Lives here rather than being imported from the adapters package
// because test files don't export symbols across packages.
type uiStubAdapter struct {
	name  string
	state adapters.State
}

func (a *uiStubAdapter) Name() string                { return a.name }
func (a *uiStubAdapter) DisplayName() string         { return a.name }
func (a *uiStubAdapter) Fields() []adapters.FieldDef { return nil }
func (a *uiStubAdapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	return nil
}
func (a *uiStubAdapter) IsEnabled() bool                 { return true }
func (a *uiStubAdapter) Start(ctx context.Context) error { return nil }
func (a *uiStubAdapter) Stop() error                     { return nil }
func (a *uiStubAdapter) Status() adapters.Status         { return adapters.Status{State: a.state} }
func (a *uiStubAdapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func TestHandleSidebarStatus_RendersDotsForEachAdapter(t *testing.T) {
	reg := adapters.NewRegistry()
	_ = reg.Register(&uiStubAdapter{name: "plex", state: adapters.StateRunning})
	s, _ := New(Config{Registry: reg})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/ui/sidebar/status", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, "plex") {
		t.Errorf("missing plex: %s", body)
	}
	if !strings.Contains(body, `class="dot run"`) {
		t.Errorf("missing run dot: %s", body)
	}
}

func TestHandleSidebarStatus_ReflectsErrorState(t *testing.T) {
	reg := adapters.NewRegistry()
	_ = reg.Register(&uiStubAdapter{name: "plex", state: adapters.StateError})
	s, _ := New(Config{Registry: reg})
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("GET", "/ui/sidebar/status", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if !strings.Contains(rw.Body.String(), `class="dot err"`) {
		t.Errorf("missing err dot: %s", rw.Body.String())
	}
}

// Sidebar links must override the inherited hx-swap from <aside>. The
// aside polls itself with hx-swap="outerHTML"; htmx 2.x inherits that
// down the tree, so without an explicit hx-swap on the link a click on
// "Bridge" or an adapter would do an outerHTML swap on #panel and
// destroy the <main class="panel"> wrapper. Subsequent clicks would
// then fire htmx:targetError because #panel no longer exists, and the
// panel content would reflow into the sidebar's grid column.
func TestHandleSidebarStatus_LinksOverrideInheritedSwap(t *testing.T) {
	reg := adapters.NewRegistry()
	_ = reg.Register(&uiStubAdapter{name: "plex", state: adapters.StateRunning})
	s, _ := New(Config{Registry: reg})
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("GET", "/ui/sidebar/status", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	for _, link := range []string{`href="/ui/bridge"`, `href="/ui/adapter/plex"`} {
		idx := strings.Index(body, link)
		if idx < 0 {
			t.Fatalf("link %s not found in body", link)
		}
		// Look only at the opening <a> tag for this link.
		end := strings.Index(body[idx:], ">")
		if end < 0 {
			t.Fatalf("malformed <a> for %s", link)
		}
		tag := body[idx : idx+end]
		if !strings.Contains(tag, `hx-swap="innerHTML"`) {
			t.Errorf("link %s missing hx-swap=\"innerHTML\" override (would inherit outerHTML from <aside>): %s", link, tag)
		}
	}
}

func TestShell_DoesNotPollOuterAside(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()

	// Old behavior: <aside hx-get="/ui/sidebar/status" hx-swap="outerHTML">.
	// New behavior: aside has no hx-get directly; a child element polls
	// /ui/sidebar/dots with hx-swap="none" (OOB swaps target individual
	// dot spans).
	if strings.Contains(body, `<aside`) && strings.Contains(body, `hx-get="/ui/sidebar/status"`) {
		t.Error("aside still polls /ui/sidebar/status with outerHTML — must be /ui/sidebar/dots with hx-swap=none")
	}
	if !strings.Contains(body, `hx-get="/ui/sidebar/dots"`) {
		t.Error("expected sidebar to poll /ui/sidebar/dots")
	}
	if !strings.Contains(body, `hx-swap="none"`) {
		t.Error("expected hx-swap=\"none\" on the polling element (OOB swaps own the visible state)")
	}
}

func TestShell_RendersActiveLinkServerSide(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()

	if !strings.Contains(body, `href="/ui/bridge"`) {
		t.Fatal("bridge link not rendered")
	}
	// Look for "active" class within ~200 chars after the bridge href.
	idx := strings.Index(body, `href="/ui/bridge"`)
	end := idx + 200
	if end > len(body) {
		end = len(body)
	}
	window := body[idx:end]
	if !strings.Contains(window, "active") {
		t.Errorf("bridge link not marked active server-side; window=%q", window)
	}
}
