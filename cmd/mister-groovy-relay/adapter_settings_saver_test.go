package main

import (
	"sync"
	"testing"

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
