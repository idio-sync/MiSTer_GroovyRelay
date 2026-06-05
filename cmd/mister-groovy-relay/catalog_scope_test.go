//go:build integration

package main

import (
	"context"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func TestCatalogScope_HLSBufferDisabled_WireLabelMatchesRecast(t *testing.T) {
	a := newStreamsForCatalogScopeTest(t, nil)
	saver := uiserver.NewAdapterSaver(tmpConfigPath(t), &sync.Mutex{})

	m := &catalogManager{adapter: a, adapterSaver: saver}
	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %v; want ScopeRestartCast", scope)
	}
	label, ok := chassis.WireLabelForScope(scope)
	if !ok || label != "recast" {
		t.Fatalf("chassis label = %q ok=%v; want \"recast\" true", label, ok)
	}
}

func TestCatalogScope_EnabledFalse_WireLabelMatchesHot(t *testing.T) {
	a := newStreamsForCatalogScopeTest(t, nil)
	saver := uiserver.NewAdapterSaver(tmpConfigPath(t), &sync.Mutex{})

	m := &catalogManager{adapter: a, adapterSaver: saver}
	enabled := false
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v; want ScopeHotSwap", scope)
	}
	label, ok := chassis.WireLabelForScope(scope)
	if !ok || label != "hot" {
		t.Fatalf("chassis label = %q ok=%v; want \"hot\" true", label, ok)
	}
}

func TestCatalogScope_HLSBufferDisabled_StopsActiveStreamsCast(t *testing.T) {
	fakeCore := &catalogScopeFakeCore{}
	a := newStreamsForCatalogScopeTest(t, fakeCore)
	enableStreamsForCatalogScopeTest(t, a)
	channelID := firstCatalogChannelForProvider(t, a, "toonami-aftermath")
	if err := a.CastChannel(context.Background(), "toonami-aftermath", channelID); err != nil {
		t.Fatalf("CastChannel: %v", err)
	}
	if fakeCore.currentRef() == "" {
		t.Fatalf("expected active core AdapterRef after CastChannel")
	}

	saver := uiserver.NewAdapterSaver(tmpConfigPath(t), &sync.Mutex{})
	m := &catalogManager{adapter: a, adapterSaver: saver}
	disabled := true
	scope, err := m.UpdateProvider("toonami-aftermath", chassis.CatalogProviderPatch{HLSBufferDisabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %v; want ScopeRestartCast", scope)
	}
	if fakeCore.stopCalls() != 1 {
		t.Fatalf("StopIfAdapterRef calls = %d; want 1", fakeCore.stopCalls())
	}
	if fakeCore.currentRef() != "" {
		t.Fatalf("active core AdapterRef = %q; want cleared", fakeCore.currentRef())
	}
}

func newStreamsForCatalogScopeTest(t *testing.T, c streams.SessionManager) *streams.Adapter {
	t.Helper()
	a, err := streams.New(streams.AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   c,
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	return a
}

func enableStreamsForCatalogScopeTest(t *testing.T, a *streams.Adapter) {
	t.Helper()
	cfg := a.ConfigSnapshot()
	cfg.Enabled = true
	if _, err := a.ApplyConfigValue(cfg, func(name string, raw []byte) error { return nil }); err != nil {
		t.Fatalf("enable streams: %v", err)
	}
}

func firstCatalogChannelForProvider(t *testing.T, a *streams.Adapter, providerID string) string {
	t.Helper()
	for _, p := range a.Catalog() {
		if p.ID != providerID {
			continue
		}
		for _, g := range p.Groups {
			if len(g.Channels) > 0 {
				return g.Channels[0].ID
			}
		}
	}
	t.Fatalf("provider %q has no channels in Catalog()", providerID)
	return ""
}

type catalogScopeFakeCore struct {
	mu     sync.Mutex
	status core.SessionStatus
	stops  int
}

func (f *catalogScopeFakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.AdapterRef = req.AdapterRef
	return nil
}

func (f *catalogScopeFakeCore) StartSessionIfIdle(req core.SessionRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status.AdapterRef != "" {
		return false, nil
	}
	f.status.AdapterRef = req.AdapterRef
	return true, nil
}

func (f *catalogScopeFakeCore) StartSessionIfSession(req core.SessionRequest, ref string, generation uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref == "" || f.status.AdapterRef != ref || f.status.Generation != generation {
		return false, nil
	}
	f.status.AdapterRef = req.AdapterRef
	return true, nil
}

func (f *catalogScopeFakeCore) PauseIfAdapterRef(ref string) (bool, error) { return false, nil }

func (f *catalogScopeFakeCore) StopIfAdapterRef(ref string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref == "" || f.status.AdapterRef != ref {
		return false, nil
	}
	f.stops++
	f.status.AdapterRef = ""
	return true, nil
}

func (f *catalogScopeFakeCore) StopIfSession(ref string, generation uint64) (bool, error) {
	return false, nil
}

func (f *catalogScopeFakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *catalogScopeFakeCore) currentRef() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status.AdapterRef
}

func (f *catalogScopeFakeCore) stopCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}
