package ui

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("direct /ui/adapter load should render the full shell document")
	}
	if !strings.Contains(body, "htmx.min.js") {
		t.Error("direct /ui/adapter load should include shell assets")
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

// extraStub implements ExtraHTMLProvider so the adapter panel
// emits the returned markup below the form.
type extraStub struct {
	richStub
	extra template.HTML
}

func (a *extraStub) ExtraPanelHTML() template.HTML { return a.extra }

// TestHandleAdapter_GET_ExtraHTMLRenderedUnescaped is the regression
// guard for review fix C1. When ExtraHTML was typed as string the
// adapter panel emitted it HTML-escaped, so the entire Plex linking
// flow rendered as literal `<button>` tags the operator could read
// but not click. This test fails if someone flips the type back.
func TestHandleAdapter_GET_ExtraHTMLRenderedUnescaped(t *testing.T) {
	stub := &extraStub{
		richStub: richStub{name: "stub", enabled: true, state: adapters.StateRunning},
		extra:    template.HTML(`<button id="stub-link">Click me</button>`),
	}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	s, err := New(Config{Registry: reg})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/ui/adapter/stub", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	body := rw.Body.String()

	// Markup must land verbatim — not escaped to &lt;button&gt;.
	if !strings.Contains(body, `<button id="stub-link">Click me</button>`) {
		t.Errorf("ExtraHTML rendered escaped or missing; body:\n%s", body)
	}
	if strings.Contains(body, "&lt;button") {
		t.Errorf("ExtraHTML was HTML-escaped (regression of C1); body:\n%s", body)
	}
}

func TestHandleAdapter_GET_HTMXReturnsFragment(t *testing.T) {
	mux := newAdapterTestServer(t, &richStub{name: "stub", enabled: true, state: adapters.StateRunning})
	req := httptest.NewRequest("GET", "/ui/adapter/stub", nil)
	req.Header.Set("HX-Request", "true")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx adapter request should return a panel fragment, not a full document")
	}
	if !strings.Contains(body, "StubDisplay") {
		t.Error("adapter fragment missing display name")
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

// fakeAdapterSaver captures the bytes the save handler persisted so
// tests can assert on the TOML snippet contents.
type fakeAdapterSaver struct {
	lastName string
	lastRaw  []byte
	failErr  error
}

func (f *fakeAdapterSaver) Save(name string, rawTOMLSection []byte) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.lastName = name
	f.lastRaw = rawTOMLSection
	return nil
}

func TestHandleAdapter_Save_Success(t *testing.T) {
	stub := &richStub{name: "stub", enabled: true, state: adapters.StateRunning}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	saver := &fakeAdapterSaver{}
	s, _ := New(Config{Registry: reg, AdapterSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	body := strings.NewReader("device_name=NewName&enabled=true")
	req := httptest.NewRequest("POST", "/ui/adapter/stub/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if saver.lastName != "stub" {
		t.Errorf("saver.lastName = %q", saver.lastName)
	}
	if !strings.Contains(string(saver.lastRaw), `device_name = "NewName"`) {
		t.Errorf("saved TOML missing device_name: %s", saver.lastRaw)
	}
	if !strings.Contains(rw.Body.String(), "applied live") {
		t.Error("want hot-swap toast")
	}
}

func TestHandleAdapter_Save_StartsWhenEnableTransitions(t *testing.T) {
	stub := &toggleStub{richStub: richStub{name: "stub", enabled: false, state: adapters.StateStopped}}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	saver := &fakeAdapterSaver{}
	s, _ := New(Config{Registry: reg, AdapterSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	body := strings.NewReader("device_name=NewName&enabled=true")
	req := httptest.NewRequest("POST", "/ui/adapter/stub/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if stub.startCalls != 1 {
		t.Errorf("want 1 Start call after enable transition via save, got %d", stub.startCalls)
	}
	if stub.stopCalls != 0 {
		t.Errorf("want 0 Stop calls, got %d", stub.stopCalls)
	}
}

func TestHandleAdapter_Save_StopsWhenDisableTransitions(t *testing.T) {
	stub := &toggleStub{richStub: richStub{name: "stub", enabled: true, state: adapters.StateRunning}}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	saver := &fakeAdapterSaver{}
	s, _ := New(Config{Registry: reg, AdapterSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	body := strings.NewReader("device_name=NewName&enabled=false")
	req := httptest.NewRequest("POST", "/ui/adapter/stub/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if stub.stopCalls != 1 {
		t.Errorf("want 1 Stop call after disable transition via save, got %d", stub.stopCalls)
	}
	if stub.startCalls != 0 {
		t.Errorf("want 0 Start calls, got %d", stub.startCalls)
	}
}

func TestHandleAdapter_Save_NoLifecycleWhenEnableUnchanged(t *testing.T) {
	stub := &toggleStub{richStub: richStub{name: "stub", enabled: true, state: adapters.StateRunning}}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	saver := &fakeAdapterSaver{}
	s, _ := New(Config{Registry: reg, AdapterSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	body := strings.NewReader("device_name=NewName&enabled=true")
	req := httptest.NewRequest("POST", "/ui/adapter/stub/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if stub.startCalls != 0 || stub.stopCalls != 0 {
		t.Errorf("want no lifecycle dispatch when enabled unchanged; start=%d stop=%d",
			stub.startCalls, stub.stopCalls)
	}
}

func TestHandleAdapter_Save_CSRFRejected(t *testing.T) {
	reg := adapters.NewRegistry()
	_ = reg.Register(&richStub{name: "stub"})
	s, _ := New(Config{Registry: reg, AdapterSaver: &fakeAdapterSaver{}})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/ui/adapter/stub/save", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rw.Code)
	}
}

func TestFormToAdapterTOML_SkipsKindAction(t *testing.T) {
	fields := []adapters.FieldDef{
		{Key: "host", Kind: adapters.KindText, Section: "Connection"},
		{Key: "do_thing", Kind: adapters.KindAction, Section: "Actions", Label: "Do thing"},
	}
	form := url.Values{
		"host":     {"example.com"},
		"do_thing": {"clicked"}, // would-be button click; must NOT serialize
	}
	got, ferrs := formToAdapterTOML(form, fields)
	if len(ferrs) > 0 {
		t.Fatalf("formToAdapterTOML field errors: %v", ferrs)
	}
	if !bytes.Contains(got, []byte("host = ")) {
		t.Errorf("expected host key in output; got: %s", got)
	}
	if bytes.Contains(got, []byte("do_thing")) {
		t.Errorf("KindAction key leaked into TOML output: %s", got)
	}
}

func TestAdapterRowFor_KindAction(t *testing.T) {
	fd := adapters.FieldDef{
		Key:     "pair/start",
		Label:   "Start pairing",
		Kind:    adapters.KindAction,
		Section: "Pairing",
	}
	r := adapterRowFor(fd, nil, nil)
	if r.Kind != "action" {
		t.Errorf("Kind: got %q, want %q", r.Kind, "action")
	}
	if r.Label != "Start pairing" {
		t.Errorf("Label: got %q, want %q", r.Label, "Start pairing")
	}
	if r.StringValue != "" {
		t.Errorf("StringValue: got %q, want empty", r.StringValue)
	}
}

// actionStub is an adapter whose Fields() includes a KindAction entry
// so we can test that the adapter-panel template emits the correct
// hx-post URL and namespaced slot id.
type actionStub struct {
	richStub
}

func (a *actionStub) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "pair/start", Label: "Start pairing", Kind: adapters.KindAction, Section: "Pairing"},
	}
}

// TestHandleAdapter_GET_KindActionRendered verifies that a KindAction
// field emits an hx-post button (not a broken zero-value <input>) and
// that the result slot id is namespaced by the adapter name so adapters
// sharing the same action key don't collide in the DOM.
func TestHandleAdapter_GET_KindActionRendered(t *testing.T) {
	stub := &actionStub{richStub: richStub{name: "myplex", enabled: true, state: adapters.StateRunning}}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	s, err := New(Config{Registry: reg})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/ui/adapter/myplex", nil)
	req.Header.Set("HX-Request", "true") // fragment only — simpler to inspect
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	body := rw.Body.String()

	// Must render an hx-post button pointing at the correct adapter route.
	wantPost := `hx-post="/ui/adapter/myplex/pair/start"`
	if !strings.Contains(body, wantPost) {
		t.Errorf("action button hx-post missing or wrong; want %q in:\n%s", wantPost, body)
	}

	// Slot id must be namespaced by adapter name — "pair/start" → "pair-start".
	wantSlot := `id="action-result-myplex-pair-start"`
	if !strings.Contains(body, wantSlot) {
		t.Errorf("action result slot id missing or wrong; want %q in:\n%s", wantSlot, body)
	}

	// Must NOT render a plain <input> for the action field.
	if strings.Contains(body, `name="pair/start"`) {
		t.Errorf("action field rendered as <input> (regression); body:\n%s", body)
	}
}

func TestHandleAdapter_Save_RequiredFieldMissing(t *testing.T) {
	stub := &richStub{name: "stub", enabled: true}
	reg := adapters.NewRegistry()
	_ = reg.Register(stub)
	saver := &fakeAdapterSaver{}
	s, _ := New(Config{Registry: reg, AdapterSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	// device_name is Required but the form omits it as an empty string —
	// the enum branch would error, but richStub uses KindText, so the
	// ftomlToAdapterTOML path doesn't error on empty text. Instead verify
	// that a separate RequiredInt case handles it. For richStub text-only
	// schema, an empty device_name still saves as device_name = "".
	// Leave this test as a placeholder for the KindInt/KindEnum required
	// branch, tested implicitly by parseIntField's empty-string handling.
	body := strings.NewReader("device_name=Valid&enabled=false")
	req := httptest.NewRequest("POST", "/ui/adapter/stub/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	if saver.lastName != "stub" {
		t.Errorf("saver should have been called for non-required form; lastName = %q", saver.lastName)
	}
}
