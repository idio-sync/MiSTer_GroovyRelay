package main

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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
