package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// seedProviderCfg mutates the streams adapter's per-provider config
// via the public ApplyConfigValue path so cmd-side tests can stage
// state without touching unexported fields.
func seedProviderCfg(t *testing.T, a *streams.Adapter, providers map[string]streams.ProviderConfig) {
	t.Helper()
	cfg := a.ConfigSnapshot()
	cfg.Providers = providers
	if _, err := a.ApplyConfigValue(cfg, func(name string, raw []byte) error { return nil }); err != nil {
		t.Fatalf("seedProviderCfg: %v", err)
	}
}

func TestCatalogManager_Providers_EnrichesWithCfgState(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"mtv-rewind": {Disabled: true, HLSBufferDisabled: false},
	})
	m := &catalogManager{adapter: a, adapterSaver: nil}

	got := m.Providers()
	if len(got) == 0 {
		t.Fatalf("expected at least one provider; got 0")
	}
	var mtv chassis.CatalogProviderState
	for _, p := range got {
		if p.ID == "mtv-rewind" {
			mtv = p
			break
		}
	}
	if mtv.ID == "" {
		t.Fatalf("mtv-rewind missing from Providers() output")
	}
	if mtv.Enabled {
		t.Errorf("Enabled = true; want false (cfg.Disabled is true)")
	}
	if mtv.HLSBufferDisabled {
		t.Errorf("HLSBufferDisabled = true; want false")
	}
	if mtv.Origin == "" {
		t.Errorf("Origin should be populated by streams.Catalog() upstream")
	}
}

func TestCatalogManager_Providers_AbsentCfgEntryDefaultsToEnabled(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	// Freshly New()'d adapter has cfg.Providers == nil from
	// DefaultConfig(); no seeding needed.
	m := &catalogManager{adapter: a, adapterSaver: nil}

	got := m.Providers()
	for _, p := range got {
		if !p.Enabled {
			t.Errorf("provider %q Enabled = false; want true (zero-value ProviderConfig)", p.ID)
		}
	}
}

func TestCatalogManager_Providers_PropagatesCatalogRefreshHours(t *testing.T) {
	// 4D's streams pane per-provider override row renders
	// value="{{.CatalogRefreshHours}}" — verify the wrapper threads the
	// streams.ProviderConfig.CatalogRefreshHours value through to the
	// chassis-shaped state. Absence (zero value) is treated as
	// "inherit the streams-global default" by the renderer.
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"mtv-rewind": {CatalogRefreshHours: 24},
	})
	m := &catalogManager{adapter: a, adapterSaver: nil}

	got := m.Providers()
	var mtv chassis.CatalogProviderState
	other := chassis.CatalogProviderState{ID: "sentinel-not-mtv"}
	for _, p := range got {
		switch p.ID {
		case "mtv-rewind":
			mtv = p
		default:
			if other.ID == "sentinel-not-mtv" {
				other = p
			}
		}
	}
	if mtv.ID == "" {
		t.Fatalf("mtv-rewind missing from Providers() output")
	}
	if mtv.CatalogRefreshHours != 24 {
		t.Errorf("mtv-rewind CatalogRefreshHours = %d; want 24", mtv.CatalogRefreshHours)
	}
	// A provider with no override returns zero, the renderer's
	// "inherit global" sentinel.
	if other.ID != "sentinel-not-mtv" && other.CatalogRefreshHours != 0 {
		t.Errorf("provider %q CatalogRefreshHours = %d; want 0 (zero-value override)", other.ID, other.CatalogRefreshHours)
	}
}

// newStreamsForCatalogTest constructs a streams.Adapter via the public
// New() API. The local-only bootstrap inside
// streams.Adapter.chassisCatalogSnapshot (catalog.go:74-80) populates
// Catalog() without requiring Start(), so this is safe for unit tests.
func newStreamsForCatalogTest(t *testing.T) *streams.Adapter {
	t.Helper()
	a, err := streams.New(streams.AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	return a
}

func TestCatalogManager_UpdateProvider_EnabledOnly_HotScope(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"mtv-rewind": {Disabled: false},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	enabled := false
	scope, err := m.UpdateProvider("mtv-rewind", chassis.CatalogProviderPatch{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v; want ScopeHotSwap", scope)
	}
	if !a.ConfigSnapshot().Providers["mtv-rewind"].Disabled {
		t.Errorf("in-memory Disabled = false; want true")
	}
}

func TestCatalogManager_UpdateProvider_HLSOnly_RecastScope(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"toonami-aftermath": {HLSBufferDisabled: false},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v; want ScopeRestartCast (declared floor)", scope)
	}
	if !a.ConfigSnapshot().Providers["toonami-aftermath"].HLSBufferDisabled {
		t.Errorf("in-memory HLSBufferDisabled = false; want true")
	}
}

func TestCatalogManager_UpdateProvider_BothFields_MaxWinsRecast(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"toonami-aftermath": {},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	enabled := true
	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath",
		chassis.CatalogProviderPatch{Enabled: &enabled, HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v; want ScopeRestartCast (max-wins)", scope)
	}
}

func TestCatalogManager_UpdateProvider_NoopHLSStillReportsRecast(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"toonami-aftermath": {HLSBufferDisabled: false},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	// Set HLSBufferDisabled to its current value — no diff.
	disabled := false
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("no-op HLS save scope = %v; want ScopeRestartCast (declared floor)", scope)
	}
}

func TestCatalogManager_SetDirectStreamHLSBuffer_FlipsOnlyLive(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	scope, err := m.SetDirectStreamHLSBuffer(true)
	if err != nil {
		t.Fatalf("SetDirectStreamHLSBuffer: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v; want ScopeRestartCast", scope)
	}
	// Only Live providers should be flipped.
	cfg := a.ConfigSnapshot()
	for _, p := range a.Catalog() {
		got := cfg.Providers[p.ID].HLSBufferDisabled
		if p.Live && !got {
			t.Errorf("Live provider %q HLSBufferDisabled = false; want true", p.ID)
		}
		if !p.Live && got {
			t.Errorf("Non-Live provider %q HLSBufferDisabled = true; want false", p.ID)
		}
	}
}

// Regression for the read/write asymmetry: a DISABLED direct-stream provider
// is filtered out of Catalog() (mergeManifests drops it from a.definitions),
// so the global HLS toggle must iterate BundledCatalog() to still flip it —
// matching the set that Providers()/the rendered switch reflects. Before the
// fix this silently no-op'd while reporting RECAST success.
func TestCatalogManager_SetDirectStreamHLSBuffer_FlipsDisabledLiveProvider(t *testing.T) {
	a := newStreamsForCatalogTest(t)
	seedProviderCfg(t, a, map[string]streams.ProviderConfig{
		"toonami-aftermath": {Disabled: true, HLSBufferDisabled: false},
	})
	tomlPath := tmpConfigPath(t)
	saver := uiserver.NewAdapterSaver(tomlPath, &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}

	scope, err := m.SetDirectStreamHLSBuffer(true)
	if err != nil {
		t.Fatalf("SetDirectStreamHLSBuffer: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v; want ScopeRestartCast", scope)
	}
	// toonami-aftermath is Live (direct-streams) but Disabled; it must still be
	// flipped. Assert against the config directly since Catalog() omits it.
	pc := a.ConfigSnapshot().Providers["toonami-aftermath"]
	if !pc.HLSBufferDisabled {
		t.Errorf("disabled Live provider toonami-aftermath HLSBufferDisabled = false; want true")
	}
	if !pc.Disabled {
		t.Errorf("toonami-aftermath Disabled = false; want true (SetDirect must not re-enable it)")
	}
}

// tmpConfigPath writes a minimal valid config.toml fixture and returns
// the path. Cleanup is handled by t.TempDir().
func tmpConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body, err := config.DefaultConfigTOML(dir)
	if err != nil {
		t.Fatalf("seed default config: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}
	return path
}

func TestCatalogManager_EnsureStreamsEnabledPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.WriteAtomic(cfgPath, []byte("[adapters.streams]\nenabled = false\n")); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	a, err := streams.New(streams.AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	cm := &catalogManager{adapter: a, adapterSaver: saver}

	if a.IsEnabled() {
		t.Fatal("precondition: streams should be disabled")
	}
	if err := cm.EnsureStreamsEnabled(); err != nil {
		t.Fatalf("EnsureStreamsEnabled: %v", err)
	}
	if !a.IsEnabled() {
		t.Fatal("streams not enabled in-memory after EnsureStreamsEnabled")
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), "enabled = true") {
		t.Fatalf("config file was not updated:\n%s", raw)
	}
	// Idempotent second call.
	if err := cm.EnsureStreamsEnabled(); err != nil {
		t.Fatalf("EnsureStreamsEnabled (idempotent): %v", err)
	}
}
