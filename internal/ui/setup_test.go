package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

type setupLifecycleAdapter struct {
	name        string
	displayName string
	enabled     bool
	state       adapters.State
	applyErr    error
	startErr    error
	applyCalls  int
	startCalls  int
}

func (a *setupLifecycleAdapter) Name() string { return a.name }
func (a *setupLifecycleAdapter) DisplayName() string {
	if a.displayName != "" {
		return a.displayName
	}
	return a.name
}
func (a *setupLifecycleAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{{
		Key:      "host",
		Label:    "Host",
		Kind:     adapters.KindText,
		Required: true,
	}}
}
func (a *setupLifecycleAdapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	return nil
}
func (a *setupLifecycleAdapter) IsEnabled() bool { return a.enabled }
func (a *setupLifecycleAdapter) Start(ctx context.Context) error {
	a.startCalls++
	if a.startErr != nil {
		return a.startErr
	}
	a.state = adapters.StateRunning
	return nil
}
func (a *setupLifecycleAdapter) Stop() error             { return nil }
func (a *setupLifecycleAdapter) Status() adapters.Status { return adapters.Status{State: a.state} }
func (a *setupLifecycleAdapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	a.applyCalls++
	if a.applyErr != nil {
		return 0, a.applyErr
	}
	a.enabled = true
	return adapters.ScopeHotSwap, nil
}
func (a *setupLifecycleAdapter) SetEnabled(v bool) { a.enabled = v }
func (a *setupLifecycleAdapter) CurrentValues() map[string]any {
	return map[string]any{"host": "127.0.0.1"}
}

// fakeBridgeSaverFirstRun is a fake implementing both BridgeSaver and
// FirstRunAware so tests can drive the wizard's first-run gate.
type fakeBridgeSaverFirstRun struct {
	mu    sync.Mutex
	first bool
	cur   config.BridgeConfig
	saved *config.BridgeConfig
}

func (f *fakeBridgeSaverFirstRun) Current() config.BridgeConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

func (f *fakeBridgeSaverFirstRun) Save(newCfg config.BridgeConfig) (adapters.ApplyScope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = &newCfg
	f.cur = newCfg
	return adapters.ScopeHotSwap, nil
}

func (f *fakeBridgeSaverFirstRun) IsFirstRun() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.first
}

func (f *fakeBridgeSaverFirstRun) DismissFirstRun() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.first = false
	return nil
}

// newTestServerWithFirstRun builds a Server with a fake first-run-aware
// BridgeSaver. Pass firstRun=true to simulate a fresh install.
func newTestServerWithFirstRun(t *testing.T, firstRun bool, opts ...func(*Config)) (*Server, *http.ServeMux, *fakeBridgeSaverFirstRun) {
	t.Helper()
	saver := &fakeBridgeSaverFirstRun{
		first: firstRun,
		cur: config.BridgeConfig{
			DataDir: "/tmp/groovyrelay",
			Video: config.VideoConfig{
				Modeline:            "NTSC_480i",
				InterlaceFieldOrder: "tff",
				AspectMode:          "auto",
				RGBMode:             "rgb888",
				LZ4Enabled:          true,
			},
			Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
			MiSTer: config.MisterConfig{
				Port:       32100,
				SourcePort: 32101,
				// Host intentionally empty when firstRun=true so
				// firstIncompleteStep returns "bridge".
			},
			UI: config.UIConfig{HTTPPort: 32500},
		},
	}
	if !firstRun {
		// When not first-run, prefill mister.host so firstIncompleteStep
		// doesn't bounce to "bridge".
		saver.cur.MiSTer.Host = "192.168.1.42"
	}
	cfg := Config{
		Registry:    adapters.NewRegistry(),
		BridgeSaver: saver,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return s, mux, saver
}

// TestSetup_Root_RedirectsToFirstIncomplete verifies GET /ui/setup with
// first-run flag set redirects to the first incomplete step (bridge).
func TestSetup_Root_RedirectsToFirstIncomplete(t *testing.T) {
	_, mux, _ := newTestServerWithFirstRun(t, true)
	req := httptest.NewRequest("GET", "/ui/setup", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rw.Code)
	}
	loc := rw.Header().Get("Location")
	if !strings.HasPrefix(loc, "/ui/setup/step/") {
		t.Errorf("Location = %q, want /ui/setup/step/<something>", loc)
	}
	// firstIncompleteStep should pick "bridge" because mister.host is empty.
	if loc != "/ui/setup/step/bridge" {
		t.Errorf("Location = %q, want /ui/setup/step/bridge", loc)
	}
}

// TestSetup_Root_HonorsExplicitStepQuery verifies ?step=adapters takes
// precedence over the firstIncompleteStep walk.
func TestSetup_Root_HonorsExplicitStepQuery(t *testing.T) {
	_, mux, _ := newTestServerWithFirstRun(t, true)
	req := httptest.NewRequest("GET", "/ui/setup?step=adapters", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rw.Code)
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/setup/step/adapters" {
		t.Errorf("Location = %q, want /ui/setup/step/adapters", loc)
	}
}

// TestSetup_StepBridge_Renders verifies the bridge step renders the
// expected form fields.
func TestSetup_StepBridge_Renders(t *testing.T) {
	_, mux, _ := newTestServerWithFirstRun(t, true)
	req := httptest.NewRequest("GET", "/ui/setup/step/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%q", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	wants := []string{
		"Bridge basics",
		"MiSTer host",
		"Continue",
		`name="mister.host"`,
		`name="mister.port"`,
		`name="mister.source_port"`,
		`<form method="POST"`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in body", w)
		}
	}
	// Sidebar must be hidden in setup mode.
	if strings.Contains(body, `<aside class="sidebar">`) {
		t.Error("sidebar should be hidden in SetupMode")
	}
	// Stepper must be visible.
	if !strings.Contains(body, `class="gr-stepper"`) {
		t.Error("stepper should render in SetupMode")
	}
}

// TestSetup_StepAdapters_Renders verifies the adapters picker step.
func TestSetup_StepAdapters_Renders(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, mux, _ := newTestServerWithFirstRun(t, true, func(c *Config) {
		c.Registry = reg
	})

	req := httptest.NewRequest("GET", "/ui/setup/step/adapters", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	wants := []string{
		"Pick your sources",
		"Plex",
		`name="adapters"`,
		`value="plex"`,
		"Skip",
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in body", w)
		}
	}
}

// TestSetup_Done_DismissesFlagAndRedirects verifies POST /ui/setup/done
// clears the first-run flag and redirects to /ui/.
func TestSetup_Done_DismissesFlagAndRedirects(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex", enabled: true, enabledSet: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, mux, saver := newTestServerWithFirstRun(t, false, func(c *Config) {
		c.Registry = reg
	})
	saver.mu.Lock()
	saver.first = true
	saver.mu.Unlock()

	if !saver.IsFirstRun() {
		t.Fatal("precondition: saver.IsFirstRun() should be true")
	}

	req := httptest.NewRequest("POST", "/ui/setup/done", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%q", rw.Code, rw.Body.String())
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/" {
		t.Errorf("Location = %q, want /ui/", loc)
	}
	if saver.IsFirstRun() {
		t.Error("first-run flag was not dismissed")
	}
}

// TestSetup_Done_BlocksIncompleteWizard verifies POST /ui/setup/done
// cannot dismiss first-run while required setup steps are still incomplete.
func TestSetup_Done_BlocksIncompleteWizard(t *testing.T) {
	_, mux, saver := newTestServerWithFirstRun(t, true)

	req := httptest.NewRequest("POST", "/ui/setup/done", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%q", rw.Code, rw.Body.String())
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/setup/step/bridge" {
		t.Errorf("Location = %q, want /ui/setup/step/bridge", loc)
	}
	if !saver.IsFirstRun() {
		t.Error("first-run flag should not be dismissed while setup is incomplete")
	}
}

// TestFirstRunMount_RootRedirectIsNotGuarded verifies GET / returns 302
// to /ui/ even when first-run is set (so the operator can reach /ui/setup
// from a bare host URL).
func TestFirstRunMount_RootRedirectIsNotGuarded(t *testing.T) {
	_, mux, _ := newTestServerWithFirstRun(t, true)
	req := httptest.NewRequest("GET", "/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rw.Code)
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/" {
		t.Errorf("Location = %q, want /ui/", loc)
	}
}

// TestSetup_BridgePOST_PersistsAndRedirects verifies a successful bridge
// POST writes the new config and redirects to the next step.
func TestSetup_BridgePOST_PersistsAndRedirects(t *testing.T) {
	_, mux, saver := newTestServerWithFirstRun(t, true)

	form := url.Values{}
	form.Set("mister.host", "192.168.1.99")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")

	req := httptest.NewRequest("POST", "/ui/setup/step/bridge", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%q", rw.Code, rw.Body.String())
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/setup/step/adapters" {
		t.Errorf("Location = %q, want /ui/setup/step/adapters", loc)
	}
	if saver.saved == nil {
		t.Fatal("saver.Save was not called")
	}
	if saver.saved.MiSTer.Host != "192.168.1.99" {
		t.Errorf("saved host = %q, want 192.168.1.99", saver.saved.MiSTer.Host)
	}
}

// TestSetup_AdaptersPOST_RedirectsToFirstPicked verifies the adapters
// picker redirects to the first picked adapter's configure step.
func TestSetup_AdaptersPOST_RedirectsToFirstPicked(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, mux, _ := newTestServerWithFirstRun(t, true, func(c *Config) {
		c.Registry = reg
	})

	form := url.Values{}
	form.Add("adapters", "plex")

	req := httptest.NewRequest("POST", "/ui/setup/step/adapters", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%q", rw.Code, rw.Body.String())
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/setup/step/plex" {
		t.Errorf("Location = %q, want /ui/setup/step/plex", loc)
	}
}

// TestSetup_AdaptersPOST_SkipRedirectsToDone verifies that submitting
// the adapter picker with no selections redirects to the done step.
func TestSetup_AdaptersPOST_SkipRedirectsToDone(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, mux, _ := newTestServerWithFirstRun(t, true, func(c *Config) {
		c.Registry = reg
	})

	form := url.Values{}

	req := httptest.NewRequest("POST", "/ui/setup/step/adapters", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", rw.Code, rw.Body.String())
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/setup/step/done" {
		t.Errorf("Location = %q, want /ui/setup/step/done", loc)
	}
}

func TestSetup_AdapterConfigPOST_StartsAdapterAfterSave(t *testing.T) {
	reg := adapters.NewRegistry()
	adapter := &setupLifecycleAdapter{name: "plex", displayName: "Plex", state: adapters.StateStopped}
	if err := reg.Register(adapter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	saver := &fakeAdapterSaver{}
	_, mux, _ := newTestServerWithFirstRun(t, false, func(c *Config) {
		c.Registry = reg
		c.AdapterSaver = saver
	})

	form := url.Values{"host": {"127.0.0.1"}}
	req := httptest.NewRequest("POST", "/ui/setup/step/plex", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%q", rw.Code, rw.Body.String())
	}
	if loc := rw.Header().Get("Location"); loc != "/ui/setup" {
		t.Errorf("Location = %q, want /ui/setup", loc)
	}
	if saver.lastName != "plex" {
		t.Errorf("adapter saver name = %q, want plex", saver.lastName)
	}
	if !strings.Contains(string(saver.lastRaw), "enabled = true") {
		t.Errorf("saved TOML missing enabled=true: %s", saver.lastRaw)
	}
	if adapter.applyCalls != 1 {
		t.Errorf("ApplyConfig calls = %d, want 1", adapter.applyCalls)
	}
	if adapter.startCalls != 1 {
		t.Errorf("Start calls = %d, want 1", adapter.startCalls)
	}
	if !adapter.IsEnabled() {
		t.Error("adapter should be enabled in memory after setup save")
	}
}

func TestSetup_AdapterConfigPOST_RendersApplyError(t *testing.T) {
	reg := adapters.NewRegistry()
	adapter := &setupLifecycleAdapter{
		name:        "plex",
		displayName: "Plex",
		state:       adapters.StateStopped,
		applyErr:    errors.New("apply exploded"),
	}
	if err := reg.Register(adapter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	saver := &fakeAdapterSaver{}
	_, mux, _ := newTestServerWithFirstRun(t, false, func(c *Config) {
		c.Registry = reg
		c.AdapterSaver = saver
	})

	form := url.Values{"host": {"127.0.0.1"}}
	req := httptest.NewRequest("POST", "/ui/setup/step/plex", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%q", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	if !strings.Contains(body, "Saved to disk but apply failed") || !strings.Contains(body, "apply exploded") {
		t.Errorf("missing apply error in body: %s", body)
	}
	if adapter.startCalls != 0 {
		t.Errorf("Start calls = %d, want 0 after apply failure", adapter.startCalls)
	}
	if adapter.IsEnabled() {
		t.Error("adapter should not be enabled in memory after apply failure")
	}
}

func TestSetup_LinkAwareConfigFormEnabledBeforeLinking(t *testing.T) {
	mock := newMockLinkAwareAdapter("plex")
	_, mux, _ := newTestServerWithFirstRun(t, false, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(mock)
	})

	req := httptest.NewRequest("GET", "/ui/setup/step/plex", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	if strings.Contains(body, "<fieldset disabled>") {
		t.Errorf("setup adapter config form should stay enabled before linking; body=%s", body)
	}
	if !strings.Contains(body, `name="host"`) {
		t.Errorf("setup adapter config form missing host field: %s", body)
	}
}

// TestSetup_StepDone_Renders verifies the done step renders the
// completion message and "Take me to Status" button.
func TestSetup_StepDone_Renders(t *testing.T) {
	_, mux, _ := newTestServerWithFirstRun(t, true)
	req := httptest.NewRequest("GET", "/ui/setup/step/done", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	wants := []string{
		"all set",
		"Take me to Status",
		`action="/ui/setup/done"`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in body", w)
		}
	}
}

// TestSetup_FirstIncompleteStep_DoneWhenAdapterEnabled verifies that
// when host is set and an adapter is enabled, the wizard short-circuits
// to "done".
func TestSetup_FirstIncompleteStep_DoneWhenAdapterEnabled(t *testing.T) {
	reg := adapters.NewRegistry()
	a := &uiStubAdapter{name: "plex", displayName: "Plex", enabled: true, enabledSet: true}
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// non-firstRun means host is set, adapter is enabled
	_, mux, _ := newTestServerWithFirstRun(t, false, func(c *Config) {
		c.Registry = reg
	})

	req := httptest.NewRequest("GET", "/ui/setup", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d", rw.Code)
	}
	loc := rw.Header().Get("Location")
	if loc != "/ui/setup/step/done" {
		t.Errorf("Location = %q, want /ui/setup/step/done", loc)
	}
}

func TestSetup_StepperIncludesActiveUnenabledAdapter(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex", enabled: false, enabledSet: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	items := stepperFor("plex", reg)
	for _, item := range items {
		if item.Label == "Plex" {
			if !item.Active {
				t.Fatal("Plex step should be active")
			}
			return
		}
	}
	t.Fatalf("active Plex step missing from stepper: %#v", items)
}

// TestSetup_AdaptersStep_RejectsInvalidName verifies an unknown adapter
// name in the picker form is filtered out before redirect.
func TestSetup_AdaptersStep_RejectsInvalidName(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, mux, _ := newTestServerWithFirstRun(t, true, func(c *Config) {
		c.Registry = reg
	})

	form := url.Values{}
	form.Add("adapters", "ghost") // not registered
	form.Add("adapters", "plex")

	req := httptest.NewRequest("POST", "/ui/setup/step/adapters", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%q", rw.Code, rw.Body.String())
	}
	loc := rw.Header().Get("Location")
	// "ghost" filtered, "plex" remains.
	if loc != "/ui/setup/step/plex" {
		t.Errorf("Location = %q, want /ui/setup/step/plex", loc)
	}
}
