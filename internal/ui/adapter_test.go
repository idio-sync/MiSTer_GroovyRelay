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

// richStub is an Adapter with a Fields() method so we can test form
// rendering without pulling in the real Plex package.
type richStub struct {
	name    string
	enabled bool
	state   adapters.State
}

func (a *richStub) Name() string        { return a.name }
func (a *richStub) DisplayName() string { return "StubDisplay" }
func (a *richStub) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{
			Key: "device_name", Label: "Device Name", Kind: adapters.KindText,
			Required: true, ApplyScope: adapters.ScopeHotSwap, Section: "Identity",
		},
	}
}
func (a *richStub) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error { return nil }
func (a *richStub) IsEnabled() bool                                           { return a.enabled }
func (a *richStub) Start(ctx context.Context) error                           { return nil }
func (a *richStub) Stop() error                                               { return nil }
func (a *richStub) Status() adapters.Status                                   { return adapters.Status{State: a.state} }
func (a *richStub) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

// CurrentValues reports the current field values for the UI handler.
// Adapter doesn't require this in the interface — but the UI needs
// them for prefill. Implementations satisfy ValueProvider ad-hoc.
func (a *richStub) CurrentValues() map[string]any {
	return map[string]any{"enabled": a.enabled, "device_name": "MiSTer"}
}

func newAdapterTestServer(t *testing.T, stub *richStub) *http.ServeMux {
	t.Helper()
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	s, err := New(Config{Registry: reg})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

func TestHandleAdapter_GET_RendersFields(t *testing.T) {
	mux := newAdapterTestServer(t, &richStub{name: "stub", enabled: true, state: adapters.StateRunning})
	req := httptest.NewRequest("GET", "/ui/adapter/stub", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, `name="device_name"`) {
		t.Error("device_name input missing")
	}
	if !strings.Contains(body, "RUN") {
		t.Error("status code RUN missing")
	}
}

func TestHandleAdapter_GET_UnknownAdapter(t *testing.T) {
	mux := newAdapterTestServer(t, &richStub{name: "stub"})
	req := httptest.NewRequest("GET", "/ui/adapter/nonesuch", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rw.Code)
	}
}

// SetEnabled satisfies EnableSetter so the toggle handler can mutate
// the stub's in-memory enabled flag.
func (a *richStub) SetEnabled(v bool) { a.enabled = v }

// toggleStub adds Start/Stop call counting on top of richStub so the
// toggle handler's side-effect dispatch is observable.
type toggleStub struct {
	richStub
	startCalls int
	stopCalls  int
}

func (t *toggleStub) Start(ctx context.Context) error { t.startCalls++; return nil }
func (t *toggleStub) Stop() error                     { t.stopCalls++; return nil }

func TestHandleAdapter_Toggle_StartsWhenEnabling(t *testing.T) {
	stub := &toggleStub{richStub: richStub{name: "stub", enabled: false, state: adapters.StateStopped}}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	s, _ := New(Config{Registry: reg})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/ui/adapter/stub/toggle", strings.NewReader("enabled=true"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if stub.startCalls != 1 {
		t.Errorf("want 1 Start call, got %d", stub.startCalls)
	}
	if !stub.IsEnabled() {
		t.Error("stub IsEnabled should be true after toggle-on")
	}
}

func TestHandleAdapter_Toggle_StopsWhenDisabling(t *testing.T) {
	stub := &toggleStub{richStub: richStub{name: "stub", enabled: true, state: adapters.StateRunning}}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	s, _ := New(Config{Registry: reg})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/ui/adapter/stub/toggle", strings.NewReader("enabled=false"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	if stub.stopCalls != 1 {
		t.Errorf("want 1 Stop call, got %d", stub.stopCalls)
	}
	if stub.IsEnabled() {
		t.Error("stub IsEnabled should be false after toggle-off")
	}
}

func TestHandleAdapter_StatusFragment(t *testing.T) {
	stub := &richStub{name: "stub", state: adapters.StateRunning}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	s, _ := New(Config{Registry: reg})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/ui/adapter/stub/status", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, "RUN") {
		t.Errorf("fragment missing RUN: %s", body)
	}
}
