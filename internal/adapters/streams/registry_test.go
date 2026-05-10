package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestAdapterCanRegisterAndStartDisabled(t *testing.T) {
	reg := adapters.NewRegistry()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got, ok := reg.Get("streams"); !ok || got.DisplayName() != "Streams" {
		t.Fatalf("registry lookup = %v, %v", got, ok)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start disabled: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
}
