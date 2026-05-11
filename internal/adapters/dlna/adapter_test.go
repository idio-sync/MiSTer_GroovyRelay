package dlna

import (
	"context"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// validAdapterConfig builds an AdapterConfig satisfying the constructor
// invariants. Tests can override fields after the call. Core defaults
// to a zero-behavior stub so existing tests (which don't exercise the
// session manager) keep passing without per-test plumbing; tests that
// need to drive ownership-guard behavior or capture StartSession calls
// override Core with a tailored fakeSessionManager.
func validAdapterConfig() AdapterConfig {
	return AdapterConfig{
		DeviceUUID: "abcdef01-2345-6789-abcd-ef0123456789",
		HostIP:     "192.168.1.50",
		HTTPPort:   32500,
		Core:       &stubSessionManager{},
	}
}

// stubSessionManager is a no-op SessionManager used by tests that don't
// care about core interactions. All methods return zero values so a
// nil-safe Core dereference works without panicking. Tests that need
// to inspect calls or inject AdapterRef into Status use
// fakeSessionManager (session_lifecycle_test.go) instead.
type stubSessionManager struct{}

func (*stubSessionManager) StartSession(core.SessionRequest) error { return nil }
func (*stubSessionManager) StartSessionIfAdapterRef(core.SessionRequest, string) (bool, error) {
	return true, nil
}
func (*stubSessionManager) Status() core.SessionStatus { return core.SessionStatus{} }
func (*stubSessionManager) Pause() error               { return nil }
func (*stubSessionManager) PauseIfAdapterRef(string) (bool, error) {
	return true, nil
}
func (*stubSessionManager) Play() error { return nil }
func (*stubSessionManager) PlayIfAdapterRef(string) (bool, error) {
	return true, nil
}
func (*stubSessionManager) Stop() error { return nil }
func (*stubSessionManager) StopIfAdapterRef(string) (bool, error) {
	return true, nil
}
func (*stubSessionManager) SeekTo(int) error { return nil }
func (*stubSessionManager) SeekToIfAdapterRef(string, int) (bool, error) {
	return true, nil
}

func TestNew_RequiresCore(t *testing.T) {
	cfg := validAdapterConfig()
	cfg.Core = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("New with nil Core: want error, got nil")
	}
}

func TestNew_StoresCore(t *testing.T) {
	stub := &stubSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = stub
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Interface == concrete-pointer requires casting one side; the
	// underlying pointer is what matters here.
	got, ok := a.core.(*stubSessionManager)
	if !ok || got != stub {
		t.Errorf("adapter.core not the injected stub: got=%v ok=%v", got, ok)
	}
}

func TestNew_RequiresDeviceUUID(t *testing.T) {
	cfg := validAdapterConfig()
	cfg.DeviceUUID = ""
	if _, err := New(cfg); err == nil {
		t.Fatal("New with empty DeviceUUID: want error, got nil")
	}
}

func TestNew_RequiresValidPort(t *testing.T) {
	for _, port := range []int{0, -1, 70000, 65536} {
		cfg := validAdapterConfig()
		cfg.HTTPPort = port
		if _, err := New(cfg); err == nil {
			t.Errorf("New with HTTPPort=%d: want error, got nil", port)
		}
	}
}

func TestNew_DefaultConfigSet(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := DefaultConfig()
	if a.cfg != want {
		t.Errorf("adapter.cfg = %+v, want %+v", a.cfg, want)
	}
	if a.state != adapters.StateStopped {
		t.Errorf("initial state = %v, want StateStopped", a.state)
	}
	if a.stateSince.IsZero() {
		t.Error("stateSince should be non-zero after New")
	}
}

func TestStart_DisabledIsNoop(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Default config has Enabled=false.
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start (disabled): %v, want nil", err)
	}
	if got := a.Status().State; got != adapters.StateStopped {
		t.Errorf("Status.State after disabled Start = %v, want StateStopped", got)
	}
}

func TestStart_EnabledRequiresHostIP_Empty(t *testing.T) {
	cfg := validAdapterConfig()
	cfg.HostIP = ""
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("Start with empty HostIP: want error, got nil")
	}
	st := a.Status()
	if st.State != adapters.StateError {
		t.Errorf("State = %v, want StateError", st.State)
	}
	if st.LastError == "" {
		t.Error("LastError empty after StateError; should describe HostIP requirement")
	}
	if !strings.Contains(st.LastError, "host_ip") {
		t.Errorf("LastError = %q; expected to mention host_ip", st.LastError)
	}
}

func TestStart_EnabledRequiresHostIP_Invalid(t *testing.T) {
	cfg := validAdapterConfig()
	cfg.HostIP = "not-an-ip"
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("Start with HostIP=not-an-ip: want error, got nil")
	}
	if got := a.Status().State; got != adapters.StateError {
		t.Errorf("State = %v, want StateError", got)
	}
}

func TestStart_EnabledWithValidHostIP(t *testing.T) {
	fb := newFakeBuilder()
	fb.installBuilder(t)

	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start with valid HostIP: %v, want nil", err)
	}
	t.Cleanup(func() {
		_ = a.Stop()
	})
	st := a.Status()
	if st.State != adapters.StateRunning {
		t.Errorf("State = %v, want StateRunning", st.State)
	}
	if st.LastError != "" {
		t.Errorf("LastError = %q, want empty after successful Start", st.LastError)
	}
}

func TestStart_EnabledWithIPv6HostIP(t *testing.T) {
	fb := newFakeBuilder()
	fb.installBuilder(t)

	cfg := validAdapterConfig()
	cfg.HostIP = "fe80::1"
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	if err := a.Start(context.Background()); err != nil {
		t.Errorf("Start with IPv6 HostIP: %v, want nil", err)
	}
	t.Cleanup(func() {
		_ = a.Stop()
	})
}

func TestStop_FromAnyState(t *testing.T) {
	for _, initial := range []adapters.State{
		adapters.StateStopped,
		adapters.StateStarting,
		adapters.StateRunning,
		adapters.StateError,
	} {
		a, err := New(validAdapterConfig())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		a.setState(initial, "")
		if err := a.Stop(); err != nil {
			t.Errorf("Stop from %v: %v, want nil", initial, err)
		}
		if got := a.Status().State; got != adapters.StateStopped {
			t.Errorf("State after Stop from %v = %v, want StateStopped", initial, got)
		}
	}
}

func TestSetEnabled(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.IsEnabled() {
		t.Error("initial IsEnabled = true, want false")
	}
	a.SetEnabled(true)
	if !a.IsEnabled() {
		t.Error("after SetEnabled(true), IsEnabled = false")
	}
	a.SetEnabled(false)
	if a.IsEnabled() {
		t.Error("after SetEnabled(false), IsEnabled = true")
	}
}

func TestApplyConfig_DeviceNameChange_RestartBridge(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
enabled = false
device_name = "Living-Room-CRT"
autoplay_on_set_uri = false
allow_public_source_urls = false
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeRestartBridge {
		t.Errorf("scope = %v, want ScopeRestartBridge", scope)
	}
}

func TestApplyConfig_EnabledChange_HotSwap(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Default has Enabled=false; flip to true with no other change.
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
enabled = true
device_name = "MiSTer"
autoplay_on_set_uri = false
allow_public_source_urls = false
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
}

func TestApplyConfig_MultipleHotSwap_StaysHotSwap(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
enabled = true
device_name = "MiSTer"
autoplay_on_set_uri = true
allow_public_source_urls = false
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap (HotSwap+HotSwap)", scope)
	}
}

func TestApplyConfig_MultipleMixedScope_MaxWins(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mix HotSwap (enabled) + RestartBridge (device_name).
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
enabled = true
device_name = "MyHomeMister"
autoplay_on_set_uri = false
allow_public_source_urls = false
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeRestartBridge {
		t.Errorf("scope = %v, want ScopeRestartBridge (max-wins over HotSwap)", scope)
	}
}

func TestApplyConfig_NoChange_HotSwap(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Same as DefaultConfig() — no fields change.
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
enabled = false
device_name = "MiSTer"
autoplay_on_set_uri = false
allow_public_source_urls = false
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap (no-change is the lowest scope)", scope)
	}
}

func TestApplyConfig_InvalidConfig_ReturnsError(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
enabled = true
device_name = ""
`)
	if _, err := a.ApplyConfig(raw, meta); err == nil {
		t.Fatal("ApplyConfig with empty device_name: want error, got nil")
	}
	// Adapter should not have absorbed the bad config.
	if got := a.cfg.DeviceName; got != "MiSTer" {
		t.Errorf("after rejected ApplyConfig, cfg.DeviceName = %q, want %q (unchanged)", got, "MiSTer")
	}
}

func TestCurrentValues_AllFour(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	a.mu.Lock()
	a.cfg.DeviceName = "Lounge-MiSTer"
	a.cfg.AutoplayOnSetURI = true
	a.cfg.AllowPublicSourceURLs = true
	a.mu.Unlock()

	got := a.CurrentValues()
	if len(got) != 4 {
		t.Errorf("CurrentValues returned %d keys, want 4", len(got))
	}
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if got["device_name"] != "Lounge-MiSTer" {
		t.Errorf("device_name = %v, want %q", got["device_name"], "Lounge-MiSTer")
	}
	if got["autoplay_on_set_uri"] != true {
		t.Errorf("autoplay_on_set_uri = %v, want true", got["autoplay_on_set_uri"])
	}
	if got["allow_public_source_urls"] != true {
		t.Errorf("allow_public_source_urls = %v, want true", got["allow_public_source_urls"])
	}
}

func TestNameAndDisplayName(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Name() != "dlna" {
		t.Errorf("Name = %q, want %q", a.Name(), "dlna")
	}
	if a.DisplayName() != "DLNA / UPnP" {
		t.Errorf("DisplayName = %q, want %q", a.DisplayName(), "DLNA / UPnP")
	}
}

func TestFields_FourFieldsWithCorrectScopes(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fields := a.Fields()
	if len(fields) != 4 {
		t.Fatalf("Fields returned %d entries, want 4", len(fields))
	}
	// Spec §Config table — pin the per-field ApplyScope.
	wantScope := map[string]adapters.ApplyScope{
		"enabled":                  adapters.ScopeHotSwap,
		"device_name":              adapters.ScopeRestartBridge,
		"autoplay_on_set_uri":      adapters.ScopeHotSwap,
		"allow_public_source_urls": adapters.ScopeHotSwap,
	}
	wantKind := map[string]adapters.FieldKind{
		"enabled":                  adapters.KindBool,
		"device_name":              adapters.KindText,
		"autoplay_on_set_uri":      adapters.KindBool,
		"allow_public_source_urls": adapters.KindBool,
	}
	wantHelpSubstrings := map[string][]string{
		"enabled": {
			"UDP 1900",
			"trusted LAN",
		},
		"device_name": {
			"Restart-bridge",
			"controllers cache",
		},
		"autoplay_on_set_uri": {
			"send SetAVTransportURI without Play",
		},
		"allow_public_source_urls": {
			"SSRF risk",
			"does not allow loopback or link-local",
		},
	}
	seen := map[string]bool{}
	for _, f := range fields {
		seen[f.Key] = true
		if got, ok := wantScope[f.Key]; ok && f.ApplyScope != got {
			t.Errorf("Fields[%q].ApplyScope = %v, want %v", f.Key, f.ApplyScope, got)
		}
		if got, ok := wantKind[f.Key]; ok && f.Kind != got {
			t.Errorf("Fields[%q].Kind = %v, want %v", f.Key, f.Kind, got)
		}
		for _, want := range wantHelpSubstrings[f.Key] {
			if !strings.Contains(f.Help, want) {
				t.Errorf("Fields[%q].Help = %q, want substring %q", f.Key, f.Help, want)
			}
		}
	}
	for k := range wantScope {
		if !seen[k] {
			t.Errorf("Fields missing key %q", k)
		}
	}
}

func TestDecodeConfig_PopulatesAdapter(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
enabled = true
device_name = "Cabinet"
autoplay_on_set_uri = true
allow_public_source_urls = true
`)
	if err := a.DecodeConfig(raw, meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if !a.cfg.Enabled || a.cfg.DeviceName != "Cabinet" || !a.cfg.AutoplayOnSetURI || !a.cfg.AllowPublicSourceURLs {
		t.Errorf("DecodeConfig produced unexpected cfg: %+v", a.cfg)
	}
}

func TestDecodeConfig_RejectsInvalid(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
device_name = ""
`)
	if err := a.DecodeConfig(raw, meta); err == nil {
		t.Fatal("DecodeConfig with empty device_name: want error, got nil")
	}
}

func TestValidate_Method(t *testing.T) {
	// The package-level Validator interface lets the UI run validation
	// without mutating adapter state.
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, meta := decodeDLNASection(t, `
[adapters.dlna]
device_name = ""
`)
	if err := a.Validate(raw, meta); err == nil {
		t.Error("Validate with empty device_name: want error, got nil")
	}
	// Adapter cfg should be untouched.
	if a.cfg.DeviceName != "MiSTer" {
		t.Errorf("Validate mutated cfg.DeviceName: got %q, want %q", a.cfg.DeviceName, "MiSTer")
	}
}

// decodeDLNASection wraps the boilerplate envelope-decode used by every
// ApplyConfig / DecodeConfig test. Matches the pattern in
// internal/adapters/url/adapter_interface_test.go:91-94.
func decodeDLNASection(t *testing.T, raw string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatalf("toml.Decode: %v", err)
	}
	return envelope.Adapters["dlna"], meta
}
