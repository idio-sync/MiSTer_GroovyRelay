package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSidebarDots_RendersOOBSpansForEachAdapter(t *testing.T) {
	reg := adapters.NewRegistry()
	// Register two stubs in different states — guards against the
	// "only first adapter rendered" regression and verifies State→class
	// mapping for both running and error.
	if err := reg.Register(&uiStubAdapter{name: "plex", state: adapters.StateRunning}); err != nil {
		t.Fatalf("Register plex: %v", err)
	}
	if err := reg.Register(&uiStubAdapter{name: "jellyfin", state: adapters.StateError}); err != nil {
		t.Fatalf("Register jellyfin: %v", err)
	}
	srv, err := New(Config{Registry: reg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/sidebar/dots", nil)
	w := httptest.NewRecorder()
	srv.handleSidebarDots(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="dot-plex"`) {
		t.Errorf("expected id=\"dot-plex\" in body, got: %s", body)
	}
	if !strings.Contains(body, `id="dot-jellyfin"`) {
		t.Errorf("expected id=\"dot-jellyfin\" in body, got: %s", body)
	}
	if !strings.Contains(body, `hx-swap-oob="outerHTML"`) {
		t.Errorf("expected hx-swap-oob=\"outerHTML\" in body, got: %s", body)
	}
	if !strings.Contains(body, `class="dot run"`) {
		t.Errorf("expected dot class \"run\" for running plex adapter; got: %s", body)
	}
	if !strings.Contains(body, `class="dot err"`) {
		t.Errorf("expected dot class \"err\" for errored jellyfin adapter; got: %s", body)
	}
}
