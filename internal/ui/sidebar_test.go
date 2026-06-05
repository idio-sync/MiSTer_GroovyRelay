package ui

import (
	"context"
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
	name        string
	displayName string
	enabled     bool
	enabledSet  bool
	state       adapters.State
}

func (a *uiStubAdapter) Name() string { return a.name }
func (a *uiStubAdapter) DisplayName() string {
	if a.displayName != "" {
		return a.displayName
	}
	return a.name
}
func (a *uiStubAdapter) Fields() []adapters.FieldDef { return nil }
func (a *uiStubAdapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	return nil
}
func (a *uiStubAdapter) IsEnabled() bool {
	if a.enabledSet {
		return a.enabled
	}
	return true
}
func (a *uiStubAdapter) Start(ctx context.Context) error { return nil }
func (a *uiStubAdapter) Stop() error                     { return nil }
func (a *uiStubAdapter) Status() adapters.Status         { return adapters.Status{State: a.state} }
func (a *uiStubAdapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func TestShell_DoesNotPollOuterAside(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()

	// Old behavior: <aside hx-get="/old_ui/sidebar/status" hx-swap="outerHTML">.
	// New behavior: aside has no hx-get directly; a child element polls
	// /ui/sidebar/dots with hx-swap="none" (OOB swaps target individual
	// dot spans).
	if strings.Contains(body, `<aside`) && strings.Contains(body, `hx-get="/old_ui/sidebar/status"`) {
		t.Error("aside still polls /ui/sidebar/status with outerHTML — must be /ui/sidebar/dots with hx-swap=none")
	}
	if !strings.Contains(body, `hx-get="/old_ui/sidebar/dots"`) {
		t.Error("expected sidebar to poll /ui/sidebar/dots")
	}
	if !strings.Contains(body, `hx-swap="none"`) {
		t.Error("expected hx-swap=\"none\" on the polling element (OOB swaps own the visible state)")
	}
}

func TestShell_RendersActiveLinkServerSide(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()

	if !strings.Contains(body, `href="/old_ui/bridge"`) {
		t.Fatal("bridge link not rendered")
	}
	// Look for "active" class within ~200 chars on either side of the
	// bridge href — the class attribute now precedes href in the
	// preview-aligned template.
	idx := strings.Index(body, `href="/old_ui/bridge"`)
	start := idx - 200
	if start < 0 {
		start = 0
	}
	end := idx + 200
	if end > len(body) {
		end = len(body)
	}
	window := body[start:end]
	if !strings.Contains(window, "active") {
		t.Errorf("bridge link not marked active server-side; window=%q", window)
	}
}

func TestHTMXNavigationResponseCarriesActiveSidebarOOB(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})

	fullReq := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	fullRW := httptest.NewRecorder()
	mux.ServeHTTP(fullRW, fullReq)
	if !strings.Contains(fullRW.Body.String(), `<aside id="gr-sidebar" class="gr-sidebar">`) {
		t.Fatalf("full shell should expose #gr-sidebar as the OOB target; body=%s", fullRW.Body.String())
	}

	req := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	req.Header.Set("HX-Request", "true")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()

	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatal("htmx navigation response should not render a full document")
	}
	if !strings.Contains(body, "<h1>Bridge</h1>") {
		t.Fatal("htmx navigation response missing bridge panel")
	}
	if !strings.Contains(body, `id="gr-sidebar"`) || !strings.Contains(body, `hx-swap-oob="innerHTML"`) {
		t.Fatalf("htmx navigation response should carry an OOB sidebar refresh; body=%s", body)
	}

	bridgeLink := navLinkMarkup(t, body, `href="/old_ui/bridge"`)
	if !strings.Contains(bridgeLink, "active") {
		t.Fatalf("OOB sidebar bridge link should be active; link=%q", bridgeLink)
	}
	statusLink := navLinkMarkup(t, body, `href="/old_ui/"`)
	if strings.Contains(statusLink, "active") {
		t.Fatalf("OOB sidebar status link should not remain active; link=%q", statusLink)
	}
}

func navLinkMarkup(t *testing.T, body, href string) string {
	t.Helper()
	idx := strings.Index(body, href)
	if idx < 0 {
		t.Fatalf("link %s not rendered in body: %s", href, body)
	}
	start := strings.LastIndex(body[:idx], "<a ")
	if start < 0 {
		t.Fatalf("link %s missing opening anchor: %s", href, body)
	}
	closeIdx := strings.Index(body[idx:], "</a>")
	if closeIdx < 0 {
		t.Fatalf("link %s missing closing anchor: %s", href, body)
	}
	end := idx + closeIdx + len("</a>")
	return body[start:end]
}

// TestShell_LoadsClipboardScript guards the toast copy-to-clipboard
// fallback. The shell must reference /ui/static/clipboard.js so that
// plain-HTTP LAN deployments (where navigator.clipboard is gated on
// secure context) get a working execCommand fallback.
func TestShell_LoadsClipboardScript(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()

	if !strings.Contains(body, `src="/old_ui/static/clipboard.js"`) {
		t.Error("shell must load /ui/static/clipboard.js (toast copy fallback)")
	}

	// Confirm the asset itself is served.
	req2 := httptest.NewRequest("GET", "/old_ui/static/clipboard.js", nil)
	rw2 := httptest.NewRecorder()
	mux.ServeHTTP(rw2, req2)
	if rw2.Code != 200 {
		t.Errorf("/old_ui/static/clipboard.js: got %d, want 200", rw2.Code)
	}
	if !strings.Contains(rw2.Body.String(), "data-copy-target") {
		t.Error("clipboard.js doesn't reference data-copy-target attribute")
	}
}
