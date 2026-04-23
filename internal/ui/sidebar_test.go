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
