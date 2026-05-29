package streams

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

func TestAdapterInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.Validator = (*Adapter)(nil)
	var _ adapters.RouteProvider = (*Adapter)(nil)
	var _ ui.ValueProvider = (*Adapter)(nil)
	var _ ui.ExtraHTMLProvider = (*Adapter)(nil)
}

func TestDecodeConfigDefaults(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var raw toml.Primitive
	var meta toml.MetaData
	if err := a.DecodeConfig(raw, meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if a.IsEnabled() {
		t.Fatal("decoded default should be disabled")
	}
}

func TestProviderOverrideFieldsAndCurrentValues(t *testing.T) {
	raw, meta := decodeStreamsSection(t, `
[adapters.streams.providers.zeta]
disabled = true
catalog_refresh_hours = 9

[adapters.streams.providers.alpha]
disabled = false
catalog_refresh_hours = 3
`)
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.DecodeConfig(raw, meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	var gotKeys []string
	for _, field := range a.Fields() {
		switch field.Key {
		case "providers.alpha.disabled",
			"providers.alpha.catalog_refresh_hours",
			"providers.zeta.disabled",
			"providers.zeta.catalog_refresh_hours":
			gotKeys = append(gotKeys, field.Key)
		}
	}
	wantKeys := []string{
		"providers.alpha.disabled",
		"providers.alpha.catalog_refresh_hours",
		"providers.zeta.disabled",
		"providers.zeta.catalog_refresh_hours",
	}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("provider override field keys = %#v, want %#v", gotKeys, wantKeys)
	}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("provider override field keys = %#v, want %#v", gotKeys, wantKeys)
		}
	}

	values := a.CurrentValues()
	if values["providers.alpha.disabled"] != false {
		t.Fatalf("providers.alpha.disabled = %#v, want false", values["providers.alpha.disabled"])
	}
	if values["providers.alpha.catalog_refresh_hours"] != 3 {
		t.Fatalf("providers.alpha.catalog_refresh_hours = %#v, want 3", values["providers.alpha.catalog_refresh_hours"])
	}
	if values["providers.zeta.disabled"] != true {
		t.Fatalf("providers.zeta.disabled = %#v, want true", values["providers.zeta.disabled"])
	}
	if values["providers.zeta.catalog_refresh_hours"] != 9 {
		t.Fatalf("providers.zeta.catalog_refresh_hours = %#v, want 9", values["providers.zeta.catalog_refresh_hours"])
	}
}

func mustTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	a, err := newTestAdapter(t)
	if err != nil {
		t.Fatalf("newTestAdapter: %v", err)
	}
	return a
}

func TestAdapter_ConfigSnapshot_IndependentProvidersMap(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false, HLSBufferDisabled: false},
	}

	snap := a.ConfigSnapshot()
	snap.Providers["mtv-rewind"] = ProviderConfig{Disabled: true, HLSBufferDisabled: true}

	if a.cfg.Providers["mtv-rewind"].Disabled {
		t.Fatalf("mutating snapshot mutated adapter Providers[id].Disabled")
	}
	if a.cfg.Providers["mtv-rewind"].HLSBufferDisabled {
		t.Fatalf("mutating snapshot mutated adapter Providers[id].HLSBufferDisabled")
	}
}

func TestAdapter_ConfigSnapshot_IndependentChannelsMap(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"toonami-aftermath": {
			Channels: map[string]ChannelConfig{
				"east": {HLSBufferDisabled: false},
			},
		},
	}

	snap := a.ConfigSnapshot()
	snap.Providers["toonami-aftermath"].Channels["east"] = ChannelConfig{HLSBufferDisabled: true}

	if a.cfg.Providers["toonami-aftermath"].Channels["east"].HLSBufferDisabled {
		t.Fatalf("mutating snapshot mutated adapter nested ChannelConfig")
	}
}

func TestAdapter_ConfigSnapshot_IndependentAllowedHostsSlice(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.RemoteProviderAllowedHosts = []string{"example.com"}

	snap := a.ConfigSnapshot()
	snap.RemoteProviderAllowedHosts = append(snap.RemoteProviderAllowedHosts, "evil.example")

	if len(a.cfg.RemoteProviderAllowedHosts) != 1 {
		t.Fatalf("appending to snapshot slice mutated adapter slice; len=%d",
			len(a.cfg.RemoteProviderAllowedHosts))
	}
}
