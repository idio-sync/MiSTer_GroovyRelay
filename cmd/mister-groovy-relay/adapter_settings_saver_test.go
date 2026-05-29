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
