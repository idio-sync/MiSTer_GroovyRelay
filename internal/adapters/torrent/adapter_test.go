package torrent

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type recordingCore struct {
	starts int
	stops  int
	status core.SessionStatus
}

func (c *recordingCore) StartSession(core.SessionRequest) error {
	c.starts++
	return nil
}

func (c *recordingCore) Status() core.SessionStatus {
	return c.status
}

func (c *recordingCore) Stop() error {
	c.stops++
	return nil
}

func TestAdapterImplementsCoreInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.RouteProvider = (*Adapter)(nil)
	var _ adapters.PublicRouteProvider = (*Adapter)(nil)
}

func TestNewRequiresBridgeDataDir(t *testing.T) {
	if _, err := New(AdapterConfig{}); err == nil {
		t.Fatal("New without Bridge.DataDir = nil error, want error")
	}
}

func TestStartDoesNotCreateTorrentClient(t *testing.T) {
	factoryCalls := 0
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   &recordingCore{},
		ClientFactory: func(ClientConfig) (TorrentClient, error) {
			factoryCalls++
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("ClientFactory calls = %d, want 0", factoryCalls)
	}
}

func TestSetEnabledGatesIsEnabled(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.IsEnabled() {
		t.Fatal("new adapter IsEnabled = true, want false")
	}
	a.SetEnabled(true)
	if !a.IsEnabled() {
		t.Fatal("after SetEnabled(true), IsEnabled = false")
	}
	a.SetEnabled(false)
	if a.IsEnabled() {
		t.Fatal("after SetEnabled(false), IsEnabled = true")
	}
}
