package main

import (
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// catalogManager satisfies chassis.CatalogSettingsManager from outside
// internal/chassis. It composes the streams adapter (read snapshot +
// apply value + drop active cast) and the shared uiserver.AdapterSaver
// (atomic [adapters.streams] section rewrite under the bridge mutex).
type catalogManager struct {
	adapter      *streams.Adapter
	adapterSaver *uiserver.AdapterSaver
}

func (m *catalogManager) Providers() []chassis.CatalogProviderState {
	cfg := m.adapter.ConfigSnapshot()
	// BundledCatalog returns ALL bundled providers regardless of Disabled
	// state, so disabled providers still appear with Enabled=false and the
	// operator can re-enable them. Catalog() omits disabled providers (they
	// are filtered by mergeManifests when installed into a.definitions).
	cat := m.adapter.BundledCatalog()
	out := make([]chassis.CatalogProviderState, 0, len(cat))
	for _, p := range cat {
		channels := 0
		for _, g := range p.Groups {
			channels += len(g.Channels)
		}
		pc := cfg.Providers[p.ID]
		out = append(out, chassis.CatalogProviderState{
			ID:                  p.ID,
			DisplayName:         p.DisplayName,
			BadgeLabel:          p.BadgeLabel,
			BadgeClass:          p.BadgeClass,
			Origin:              p.Origin,
			Kind:                p.Kind,
			DefaultChannel:      p.DefaultChannel,
			Live:                p.Live,
			ChannelCount:        channels,
			Enabled:             !pc.Disabled,
			HLSBufferDisabled:   pc.HLSBufferDisabled,
			CatalogRefreshHours: pc.CatalogRefreshHours,
		})
	}
	return out
}

func (m *catalogManager) UpdateProvider(id string, patch chassis.CatalogProviderPatch) (adapters.ApplyScope, error) {
	scope, err := m.patch(func(cfg *streams.Config) {
		ensureProvider(cfg, id)
		pc := cfg.Providers[id]
		if patch.Enabled != nil {
			pc.Disabled = !*patch.Enabled
		}
		if patch.HLSBufferDisabled != nil {
			pc.HLSBufferDisabled = *patch.HLSBufferDisabled
		}
		cfg.Providers[id] = pc
	})
	if err != nil {
		return 0, err
	}
	return m.reportAndDispatch(scope, declaredProviderScope(patch))
}

func (m *catalogManager) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
	// BundledCatalog (not Catalog) so the write side covers the SAME provider
	// set as Providers()/the rendered switch. Catalog() omits disabled
	// providers (mergeManifests filters them from a.definitions), which would
	// silently skip a disabled-but-Live direct-stream provider here and leave
	// the read and write sides disagreeing.
	cat := m.adapter.BundledCatalog()
	scope, err := m.patch(func(cfg *streams.Config) {
		if cfg.Providers == nil {
			cfg.Providers = map[string]streams.ProviderConfig{}
		}
		for _, p := range cat {
			if !p.Live {
				continue
			}
			pc := cfg.Providers[p.ID]
			pc.HLSBufferDisabled = disabled
			cfg.Providers[p.ID] = pc
		}
	})
	if err != nil {
		return 0, err
	}
	return m.reportAndDispatch(scope, adapters.ScopeRestartCast)
}

// EnsureStreamsEnabled persists [adapters.streams].enabled=true and applies it
// in memory via the existing ApplyConfigValue path (atomic section rewrite
// under the bridge mutex). Idempotent in effect: safe to call when already
// enabled — the section is re-persisted but the in-memory enabled state is
// unchanged.
// This is the ONLY persistent-TOML touch in the user-providers feature (spec
// §10); it does NOT start the adapter — the caller hot-starts separately.
func (m *catalogManager) EnsureStreamsEnabled() error {
	_, err := m.patch(func(cfg *streams.Config) {
		cfg.Enabled = true
	})
	return err
}

func (m *catalogManager) patch(apply func(*streams.Config)) (adapters.ApplyScope, error) {
	cfg := m.adapter.ConfigSnapshot()
	apply(&cfg)
	return m.adapter.ApplyConfigValue(cfg, m.adapterSaver.Save)
}

// reportAndDispatch floors actual at the declared scope and dispatches
// the RECAST runtime side effect when the reported scope is RECAST.
// Runs only after the save/apply succeeds, so a failed write or
// rejected streams snapshot never drops the active cast.
func (m *catalogManager) reportAndDispatch(actual, floor adapters.ApplyScope) (adapters.ApplyScope, error) {
	reported := maxScope(actual, floor)
	// Exact RECAST match is intentional: HOT needs no runtime side effect, and
	// a scope above RECAST (REBOOT) is remediated by a container restart, not a
	// mid-session cast drop. 4C never produces a scope above RECAST.
	if reported == adapters.ScopeRestartCast {
		if err := m.adapter.StopActiveCast(); err != nil {
			return reported, err
		}
	}
	return reported, nil
}

// declaredProviderScope returns the max-wins declared scope across the
// patch's non-nil fields.
func declaredProviderScope(patch chassis.CatalogProviderPatch) adapters.ApplyScope {
	s := adapters.ApplyScope(0)
	if patch.Enabled != nil {
		s = maxScope(s, adapters.ScopeHotSwap)
	}
	if patch.HLSBufferDisabled != nil {
		s = maxScope(s, adapters.ScopeRestartCast)
	}
	return s
}

func maxScope(a, b adapters.ApplyScope) adapters.ApplyScope {
	if a > b {
		return a
	}
	return b
}

func ensureProvider(cfg *streams.Config, id string) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]streams.ProviderConfig{}
	}
	if _, ok := cfg.Providers[id]; !ok {
		cfg.Providers[id] = streams.ProviderConfig{}
	}
}
