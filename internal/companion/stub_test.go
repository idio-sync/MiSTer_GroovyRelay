package companion

import (
	"context"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// stubAdapter is a minimal adapters.Adapter for registry-backed
// companion tests (health + capability lookups by name).
type stubAdapter struct {
	name        string
	displayName string
	enabled     bool
	enabledSet  bool
	state       adapters.State
}

func (a *stubAdapter) Name() string { return a.name }
func (a *stubAdapter) DisplayName() string {
	if a.displayName != "" {
		return a.displayName
	}
	return a.name
}
func (a *stubAdapter) Fields() []adapters.FieldDef { return nil }
func (a *stubAdapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	return nil
}
func (a *stubAdapter) IsEnabled() bool {
	if a.enabledSet {
		return a.enabled
	}
	return true
}
func (a *stubAdapter) Start(ctx context.Context) error { return nil }
func (a *stubAdapter) Stop() error                     { return nil }
func (a *stubAdapter) Status() adapters.Status         { return adapters.Status{State: a.state} }
func (a *stubAdapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

type fakeMisterLauncher struct {
	called bool
	err    error
}

func (f *fakeMisterLauncher) Launch(_ context.Context) error {
	f.called = true
	return f.err
}
