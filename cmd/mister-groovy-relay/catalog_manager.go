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
			ID:                p.ID,
			DisplayName:       p.DisplayName,
			BadgeLabel:        p.BadgeLabel,
			BadgeClass:        p.BadgeClass,
			Origin:            p.Origin,
			Kind:              p.Kind,
			DefaultChannel:    p.DefaultChannel,
			Live:              p.Live,
			ChannelCount:      channels,
			Enabled:           !pc.Disabled,
			HLSBufferDisabled: pc.HLSBufferDisabled,
		})
	}
	return out
}

var _ = adapters.ScopeHotSwap // imported for use in later tasks
