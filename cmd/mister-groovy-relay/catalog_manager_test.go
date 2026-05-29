package main

import (
	"os"
	"path/filepath"
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
