package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func TestBridgeAdapterSettingsSaver_Current_UnknownReturnsFalse(t *testing.T) {
	t.Parallel()
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/config.toml", mu)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	_, ok := wrapper.Current("unknown")
	if ok {
		t.Errorf("Current(unknown) returned ok=true; want false")
	}
}

type fakeRegistry struct {
	entries map[string]adapters.Adapter
}

func (f *fakeRegistry) Get(name string) (adapters.Adapter, bool) {
	a, ok := f.entries[name]
	return a, ok
}

func TestBridgeAdapterSettingsSaver_TorrentDownloadDirReportsRecast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"
data_dir = "`+dir+`"

[adapters.torrent]
enabled = false
download_dir = ""
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(cfgPath, mu)
	fake := &fakeTorrentAdapter{current: map[string]any{"enabled": false, "download_dir": ""}}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"torrent": fake}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	scope, err := wrapper.SaveTouched("torrent", map[string]string{"download_dir": "/srv/torrents"})
	if err != nil {
		t.Fatalf("SaveTouched: %v", err)
	}
	if scope != "recast" {
		t.Errorf("scope = %q, want recast", scope)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `download_dir = "/srv/torrents"`) {
		t.Errorf("config.toml missing download_dir:\n%s", got)
	}
}

type fakeTorrentAdapter struct{ current map[string]any }

func (f *fakeTorrentAdapter) Name() string              { return "torrent" }
func (f *fakeTorrentAdapter) DisplayName() string       { return "Torrent" }
func (f *fakeTorrentAdapter) Status() adapters.Status   { return adapters.Status{} }
func (f *fakeTorrentAdapter) IsEnabled() bool           { return false }
func (f *fakeTorrentAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "download_dir", Kind: adapters.KindText, ApplyScope: adapters.ScopeRestartCast},
	}
}
func (f *fakeTorrentAdapter) CurrentValues() map[string]any {
	out := make(map[string]any, len(f.current))
	for k, v := range f.current {
		out[k] = v
	}
	return out
}
func (f *fakeTorrentAdapter) DecodeConfig(prim toml.Primitive, meta toml.MetaData) error { return nil }
func (f *fakeTorrentAdapter) Validate(prim toml.Primitive, meta toml.MetaData) error     { return nil }
func (f *fakeTorrentAdapter) ApplyConfig(prim toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeRestartCast, nil
}
func (f *fakeTorrentAdapter) Start(ctx context.Context) error { return nil }
func (f *fakeTorrentAdapter) Stop() error                     { return nil }

func TestBridgeAdapterSettingsSaver_StreamsTopLevelSave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"
data_dir = "`+dir+`"

[adapters.streams]
enabled = true
manifest_refresh_hours = 24
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(cfgPath, mu)
	fake := &fakeStreamsAdapter{current: map[string]any{"enabled": true, "manifest_refresh_hours": int64(24)}}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"streams": fake}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	if _, err := wrapper.SaveTouched("streams", map[string]string{"manifest_refresh_hours": "12"}); err != nil {
		t.Fatalf("SaveTouched: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `manifest_refresh_hours = 12`) {
		t.Errorf("config.toml missing top-level edit:\n%s", got)
	}
}

func TestBridgeAdapterSettingsSaver_StreamsPerProviderSave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"
data_dir = "`+dir+`"

[adapters.streams]
enabled = true

[adapters.streams.providers.youtube]
catalog_refresh_hours = 6
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mu := &sync.Mutex{}
	saver := uiserver.NewAdapterSaver(cfgPath, mu)
	fake := &fakeStreamsAdapter{current: map[string]any{
		"enabled":   true,
		"providers": map[string]any{"youtube": map[string]any{"catalog_refresh_hours": int64(6)}},
	}}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"streams": fake}}
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	if _, err := wrapper.SaveTouched("streams", map[string]string{"providers.youtube.catalog_refresh_hours": "24"}); err != nil {
		t.Fatalf("SaveTouched: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `[providers.youtube]`) && !strings.Contains(string(got), `[adapters.streams.providers.youtube]`) {
		t.Errorf("config.toml missing provider subtable:\n%s", got)
	}
	if !strings.Contains(string(got), `catalog_refresh_hours = 24`) {
		t.Errorf("config.toml missing per-provider edit:\n%s", got)
	}
}

func TestBridgeAdapterSettingsSaver_StreamsProjectionHidesCatalogKeys(t *testing.T) {
	t.Parallel()
	fake := &fakeStreamsAdapter{}
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"streams": fake}}
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/cfg.toml", &sync.Mutex{})
	wrapper := newBridgeAdapterSettingsSaver(saver, reg)
	fields, ok := wrapper.Fields("streams")
	if !ok {
		t.Fatalf("Fields(streams) returned ok=false")
	}
	for _, fd := range fields {
		if catalogOwnedStreamsKeyForTest(fd.Key) {
			t.Errorf("Fields(streams) leaks Catalog-owned key %q", fd.Key)
		}
	}
	if !containsFieldKey(fields, "providers.*.catalog_refresh_hours") {
		t.Errorf("Fields(streams) missing wildcard catalog_refresh_hours allowlist: %#v", fields)
	}
}

// catalogOwnedStreamsKeyForTest mirrors the Catalog-owned key predicate
// for assertion purposes (production doesn't need this fn — the projection
// drops all providers.* rows and re-adds only the catalog_refresh_hours wildcard).
func catalogOwnedStreamsKeyForTest(key string) bool {
	return strings.HasPrefix(key, "providers.") &&
		(strings.HasSuffix(key, ".disabled") || strings.HasSuffix(key, ".hls_buffer_disabled"))
}

func containsFieldKey(fields []adapters.FieldDef, key string) bool {
	for _, fd := range fields {
		if fd.Key == key {
			return true
		}
	}
	return false
}

type fakeStreamsAdapter struct{ current map[string]any }

func (f *fakeStreamsAdapter) Name() string            { return "streams" }
func (f *fakeStreamsAdapter) DisplayName() string     { return "Streams" }
func (f *fakeStreamsAdapter) Status() adapters.Status { return adapters.Status{} }
func (f *fakeStreamsAdapter) IsEnabled() bool         { return true }
func (f *fakeStreamsAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "manifest_refresh_hours", Kind: adapters.KindInt, ApplyScope: adapters.ScopeHotSwap},
		{Key: "providers.youtube.disabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "providers.youtube.hls_buffer_disabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap},
		{Key: "providers.youtube.catalog_refresh_hours", Kind: adapters.KindInt, ApplyScope: adapters.ScopeHotSwap},
	}
}
func (f *fakeStreamsAdapter) CurrentValues() map[string]any {
	if f.current == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(f.current))
	for k, v := range f.current {
		out[k] = v
	}
	return out
}
func (f *fakeStreamsAdapter) DecodeConfig(prim toml.Primitive, meta toml.MetaData) error { return nil }
func (f *fakeStreamsAdapter) Validate(prim toml.Primitive, meta toml.MetaData) error     { return nil }
func (f *fakeStreamsAdapter) ApplyConfig(prim toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeStreamsAdapter) Start(ctx context.Context) error { return nil }
func (f *fakeStreamsAdapter) Stop() error                     { return nil }
