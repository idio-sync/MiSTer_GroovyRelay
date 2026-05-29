package streams

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

// Catalog returns the chassis-shaped view of the bundled streams
// providers. Called per chassis snapshot (4 Hz) — keep it allocation-
// light. The filter to bundledChassisCatalogProviderIDs means remote
// or cached manifest providers do NOT appear in /receiver in 3B.
//
// Safe to call before Adapter.Start: definitions/catalogs are seeded
// from bundledManifest() if a.definitions is empty.
func (a *Adapter) Catalog() []adapters.CatalogProvider {
	defs, _ := a.chassisCatalogSnapshot()
	out := make([]adapters.CatalogProvider, 0, len(bundledChassisCatalogProviderIDs))
	for _, id := range bundledChassisCatalogProviderIDs {
		def, ok := defs[id]
		if !ok {
			continue
		}
		badge := providerBadges[id]
		live := def.Type == directStreamsProviderType
		origin := ""
		if u, err := url.Parse(def.BaseURL); err == nil {
			origin = u.Host
		}
		if origin == "" {
			if u, err := url.Parse(def.PlaylistURL); err == nil {
				origin = u.Host
			}
		}
		p := adapters.CatalogProvider{
			ID:             def.ID,
			DisplayName:    def.DisplayName,
			BadgeLabel:     badge.Label,
			BadgeClass:     badge.Class,
			Origin:         origin,
			Kind:           def.Type,
			Live:           live,
			DefaultChannel: def.DefaultChannel,
		}
		channelByGroup := groupChannels(def)
		for _, g := range def.Groups {
			cg := adapters.CatalogGroup{ID: g.ID, Name: g.Name}
			for _, ch := range channelByGroup[g.ID] {
				cg.Channels = append(cg.Channels, adapters.CatalogChannel{
					ID:       ch.ID,
					Name:     ch.Name,
					PlayMode: strings.ToUpper(string(ch.PlayMode)),
					Live:     live, // inherit provider.Live
				})
			}
			p.Groups = append(p.Groups, cg)
		}
		out = append(out, p)
	}
	return out
}

// BundledCatalog returns the chassis-shaped view of ALL bundled streams
// providers, including those whose ProviderConfig.Disabled == true. It
// always reads from bundledManifest() rather than the installed runtime
// definitions, so a disabled provider never disappears from the list.
//
// Use this instead of Catalog() when the caller needs to show every
// provider with its current enabled/disabled state (e.g. the 4C Catalog
// settings pane where the enabled switch must remain visible even for
// disabled providers so the operator can re-enable them).
func (a *Adapter) BundledCatalog() []adapters.CatalogProvider {
	m := bundledManifest()
	defs := make(map[string]ProviderDefinition, len(m.Providers))
	for _, p := range m.Providers {
		defs[p.ID] = p
	}
	out := make([]adapters.CatalogProvider, 0, len(bundledChassisCatalogProviderIDs))
	for _, id := range bundledChassisCatalogProviderIDs {
		def, ok := defs[id]
		if !ok {
			continue
		}
		badge := providerBadges[id]
		live := def.Type == directStreamsProviderType
		origin := ""
		if u, err := url.Parse(def.BaseURL); err == nil {
			origin = u.Host
		}
		if origin == "" {
			if u, err := url.Parse(def.PlaylistURL); err == nil {
				origin = u.Host
			}
		}
		p := adapters.CatalogProvider{
			ID:             def.ID,
			DisplayName:    def.DisplayName,
			BadgeLabel:     badge.Label,
			BadgeClass:     badge.Class,
			Origin:         origin,
			Kind:           def.Type,
			Live:           live,
			DefaultChannel: def.DefaultChannel,
		}
		channelByGroup := groupChannels(def)
		for _, g := range def.Groups {
			cg := adapters.CatalogGroup{ID: g.ID, Name: g.Name}
			for _, ch := range channelByGroup[g.ID] {
				cg.Channels = append(cg.Channels, adapters.CatalogChannel{
					ID:       ch.ID,
					Name:     ch.Name,
					PlayMode: strings.ToUpper(string(ch.PlayMode)),
					Live:     live,
				})
			}
			p.Groups = append(p.Groups, cg)
		}
		out = append(out, p)
	}
	return out
}

// chassisCatalogSnapshot returns the definitions and catalogs maps,
// seeding from bundledManifest if the adapter has not yet completed
// Start-time installation. Always returns non-empty maps.
func (a *Adapter) chassisCatalogSnapshot() (map[string]ProviderDefinition, map[string]ProviderCatalog) {
	a.mu.Lock()
	if len(a.definitions) > 0 {
		defs := make(map[string]ProviderDefinition, len(a.definitions))
		cats := make(map[string]ProviderCatalog, len(a.catalogs))
		for id, d := range a.definitions {
			defs[id] = d
		}
		for id, c := range a.catalogs {
			cats[id] = c
		}
		a.mu.Unlock()
		return defs, cats
	}
	a.mu.Unlock()

	// Local-only bootstrap: no remote fetch on a chassis render path.
	m := bundledManifest()
	defs := make(map[string]ProviderDefinition, len(m.Providers))
	for _, p := range m.Providers {
		defs[p.ID] = p
	}
	return defs, nil
}

// groupChannels indexes a provider's static ChannelDefinition slice by
// GroupID. The chassis catalog drawer in 3B exposes only the bundled
// definitions, so catalog-additions-merge semantics are intentionally
// out of scope. Lift this to (def, cat) when a future provider's
// catalog can introduce channels not in its static definition.
func groupChannels(def ProviderDefinition) map[string][]ChannelDefinition {
	by := map[string][]ChannelDefinition{}
	for _, ch := range def.Channels {
		by[ch.GroupID] = append(by[ch.GroupID], ch)
	}
	return by
}

// CastChannel starts a Streams cast for the (provider, channel) pair
// clicked in the catalog drawer. Returns typed *QuickCastError so the
// chassis emits the correct status + chip pair.
//
// Deliberate divergence from CastPreset (3A): validatePlayRequest
// errors here wrap to 404 NOT FOUND because the input came from a
// user-clicked catalog card that can legitimately point to a stale
// channel if the catalog reloaded between page render and click. The
// preset path treats the same error as 500 because every bundled slot
// is asserted to resolve.
func (a *Adapter) CastChannel(ctx context.Context, providerID, channelID string) error {
	if !a.IsEnabled() {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams adapter is disabled",
		}
	}
	if err := a.ensureStartupSnapshot(ctx); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams catalog is not ready",
			Cause:   err,
		}
	}
	res := streamhandoff.Resolution{ProviderID: providerID, ChannelID: channelID}
	if err := a.validatePlayRequest(res); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusNotFound,
			Chip:    "NOT FOUND",
			Message: err.Error(),
			Cause:   err,
		}
	}
	_, err := a.StartResolvedStream(ctx, res)
	return err
}
