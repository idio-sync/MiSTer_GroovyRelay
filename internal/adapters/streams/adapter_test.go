package streams

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestAdapter_ApplyConfigValue_PersistsAndApplies(t *testing.T) {
	a := mustTestAdapter(t)
	// Seed a non-default starting config.
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false},
	}

	var savedName string
	var savedBytes []byte
	save := func(name string, raw []byte) error {
		savedName = name
		savedBytes = append([]byte(nil), raw...)
		return nil
	}

	newCfg := a.ConfigSnapshot()
	newCfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}

	scope, err := a.ApplyConfigValue(newCfg, save)
	if err != nil {
		t.Fatalf("ApplyConfigValue: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("expected ScopeHotSwap; got %v", scope)
	}
	if savedName != "streams" {
		t.Fatalf("expected saver to be called with name=%q; got %q", "streams", savedName)
	}
	if len(savedBytes) == 0 {
		t.Fatalf("expected saver to receive non-empty TOML bytes")
	}
	if !a.cfg.Providers["mtv-rewind"].Disabled {
		t.Fatalf("expected in-memory cfg to reflect Disabled=true; got false")
	}
}

func TestAdapter_ApplyConfigValue_ValidationFailureNoSave(t *testing.T) {
	a := mustTestAdapter(t)
	called := false
	save := func(name string, raw []byte) error {
		called = true
		return nil
	}

	bad := a.ConfigSnapshot()
	bad.ManifestURL = "   " // Validate() rejects empty/whitespace.

	_, err := a.ApplyConfigValue(bad, save)
	if err == nil {
		t.Fatalf("expected validation error; got nil")
	}
	if called {
		t.Fatalf("saver should NOT be called on validation failure")
	}
}

func TestAdapter_ApplyConfigValue_SaveFailureNoInMemoryChange(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false},
	}
	original := a.cfg.Providers["mtv-rewind"].Disabled

	saveErr := errors.New("disk write failed")
	save := func(name string, raw []byte) error { return saveErr }

	newCfg := a.ConfigSnapshot()
	newCfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}

	_, err := a.ApplyConfigValue(newCfg, save)
	if !errors.Is(err, saveErr) {
		t.Fatalf("expected save error to surface; got %v", err)
	}
	if a.cfg.Providers["mtv-rewind"].Disabled != original {
		t.Fatalf("in-memory state mutated after save failure")
	}
}

func TestAdapter_ApplyConfigValue_SnapshotRebuildFailureNoSaveNoInMemoryChange(t *testing.T) {
	a := mustTestAdapter(t)
	a.cfg.Providers = map[string]ProviderConfig{
		"mtv-rewind": {Disabled: false},
	}
	original := a.cfg.Providers["mtv-rewind"].Disabled
	called := false
	save := func(name string, raw []byte) error {
		called = true
		return nil
	}

	rebuildErr := errors.New("snapshot rebuild failed")
	oldBuild := buildStartupSnapshotForApplyConfigValue
	buildStartupSnapshotForApplyConfigValue = func(ctx context.Context, cfg Config, cacheDir string) ([]ProviderDefinition, []ProviderCatalog, error) {
		return nil, nil, rebuildErr
	}
	t.Cleanup(func() { buildStartupSnapshotForApplyConfigValue = oldBuild })

	newCfg := a.ConfigSnapshot()
	newCfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}

	_, err := a.ApplyConfigValue(newCfg, save)
	if !errors.Is(err, rebuildErr) {
		t.Fatalf("expected rebuild error to surface; got %v", err)
	}
	if called {
		t.Fatalf("saver should NOT be called when snapshot rebuild fails")
	}
	if a.cfg.Providers["mtv-rewind"].Disabled != original {
		t.Fatalf("in-memory state mutated after snapshot rebuild failure")
	}
}

func TestAdapter_StopActiveCast_DropsActiveQueueAndCallsCore(t *testing.T) {
	a := mustTestAdapter(t)
	core := &fakeCore{}
	a.core = core
	a.active = newFakeActiveQueueForStopActiveCast(t)
	activeRef := activeAdapterRef(a.active)
	if activeRef == "" {
		t.Fatalf("activeAdapterRef returned empty ref for seeded queue")
	}
	core.status.AdapterRef = activeRef

	if err := a.StopActiveCast(); err != nil {
		t.Fatalf("StopActiveCast: %v", err)
	}
	if a.active != nil {
		t.Fatalf("expected a.active to be cleared; got non-nil")
	}
	if core.stopCalls != 1 {
		t.Fatalf("StopIfAdapterRef calls = %d; want 1", core.stopCalls)
	}
	if core.status.AdapterRef != "" {
		t.Fatalf("core AdapterRef = %q; want cleared", core.status.AdapterRef)
	}
}

func TestAdapter_StopActiveCast_NoActiveQueue_NoOp(t *testing.T) {
	a := mustTestAdapter(t)
	core := &fakeCore{}
	a.core = core
	a.active = nil

	if err := a.StopActiveCast(); err != nil {
		t.Fatalf("StopActiveCast: %v", err)
	}
	if core.stopCalls != 0 {
		t.Fatalf("StopIfAdapterRef calls = %d; want 0", core.stopCalls)
	}
}

func TestAdapter_StopActiveCast_NoCoreManager_NoOp(t *testing.T) {
	a := mustTestAdapter(t)
	a.core = nil
	a.active = newFakeActiveQueueForStopActiveCast(t)

	if err := a.StopActiveCast(); err != nil {
		t.Fatalf("StopActiveCast: %v", err)
	}
	// active is still cleared even when core is nil — clearActiveLocked runs.
	if a.active != nil {
		t.Fatalf("expected a.active to be cleared even without core; got non-nil")
	}
}

func newFakeActiveQueueForStopActiveCast(t *testing.T) *ActiveQueue {
	t.Helper()
	return &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "1stday",
		SessionID:  "streams-session",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "one", URL: "https://media.example/one.m3u8"}},
	}
}

func TestAdapter_Catalog_PopulatesOriginAndKindForBundledProviders(t *testing.T) {
	a := mustTestAdapter(t)

	want := map[string]struct {
		origin string
		kind   string
	}{
		"mtv-rewind":        {origin: "wantmymtv.vercel.app", kind: "youtube-channel-json"},
		"cartoon-rewind":    {origin: "cartoonrewind.tv", kind: "youtube-channel-json"},
		"toonami-aftermath": {origin: "www.toonamiaftermath.com", kind: "direct-streams"},
	}

	got := a.Catalog()
	if len(got) < len(want) {
		t.Fatalf("expected at least %d providers; got %d", len(want), len(got))
	}
	byID := map[string]adapters.CatalogProvider{}
	for _, p := range got {
		byID[p.ID] = p
	}
	for id, expect := range want {
		p, ok := byID[id]
		if !ok {
			t.Fatalf("provider %q missing from Catalog()", id)
		}
		if p.Origin != expect.origin {
			t.Errorf("provider %q Origin = %q; want %q", id, p.Origin, expect.origin)
		}
		if p.Kind != expect.kind {
			t.Errorf("provider %q Kind = %q; want %q", id, p.Kind, expect.kind)
		}
	}
}

func TestNew_BuildsUserProviderStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`{"version":1,"providers":[{"id":"user:demo","type":"user","display_name":"Demo","badge_label":"DM","badge_color":"teal","channels":[{"name":"Live","url":"https://example.com/stream.m3u8","kind":"direct"}]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "user_providers.json"), body, 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.userStore == nil {
		t.Fatal("a.userStore is nil; want a built store")
	}
	got := a.userStore.Snapshot()
	if len(got) != 1 || got[0].ID != "user:demo" {
		t.Fatalf("Snapshot = %+v, want one provider user:demo", got)
	}
}
