package streams

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

// Catalog returns the chassis-shaped view of installed providers: enabled
// bundled providers in their fixed mockup order, followed by user providers
// in definition (install/file) order. Called per chassis snapshot (4 Hz) —
// keep it allocation-light.
//
// Safe to call before Adapter.Start: definitions/catalogs are seeded
// from bundledManifest() if a.definitions is empty. Disabled bundled
// providers are absent because mergeManifests filters them from the
// installed definitions; use BundledCatalog() when disabled providers must
// remain visible.
func (a *Adapter) Catalog() []adapters.CatalogProvider {
	ordered := a.catalogProvidersInOrder()
	byID := make(map[string]ProviderDefinition, len(ordered))
	for _, d := range ordered {
		byID[d.ID] = d
	}
	out := make([]adapters.CatalogProvider, 0, len(ordered))
	// Bundled chassis providers first, in their fixed mockup order. Absent
	// entries (e.g. disabled → filtered from definitions) are skipped.
	for _, id := range bundledChassisCatalogProviderIDs {
		if d, ok := byID[id]; ok {
			out = append(out, buildChassisCatalogProvider(d))
		}
	}
	// User providers next, in definition (install/file) order.
	for _, d := range ordered {
		if isUserProviderID(d.ID) {
			out = append(out, buildChassisCatalogProvider(d))
		}
	}
	return out
}

// catalogProvidersInOrder returns the installed definitions in definitionOrder,
// seeding from bundledManifest() if Start-time installation has not run yet
// (local-only bootstrap; no remote fetch on a chassis render path).
func (a *Adapter) catalogProvidersInOrder() []ProviderDefinition {
	a.mu.Lock()
	if len(a.definitions) > 0 {
		out := make([]ProviderDefinition, 0, len(a.definitionOrder))
		for _, id := range a.definitionOrder {
			if d, ok := a.definitions[id]; ok {
				out = append(out, d)
			}
		}
		a.mu.Unlock()
		return out
	}
	a.mu.Unlock()

	m := bundledManifest()
	out := make([]ProviderDefinition, 0, len(m.Providers))
	out = append(out, m.Providers...)
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
		out = append(out, buildChassisCatalogProvider(def))
	}
	return out
}

// buildChassisCatalogProvider converts a ProviderDefinition into the
// chassis-shaped adapters.CatalogProvider (badge, parsed Origin with
// PlaylistURL fallback, Kind, and grouped channels). Shared by
// Catalog() and BundledCatalog() so the conversion lives in one place.
func buildChassisCatalogProvider(def ProviderDefinition) adapters.CatalogProvider {
	badge := providerBadges[def.ID]
	badgeLabel, badgeClass := badge.Label, badge.Class
	if isUserProviderID(def.ID) {
		// providerBadges has no user entries: fall back to the authored
		// glyph + a CSS class derived from the BadgeColor token. The
		// "u-<token>" classes (.ic.u-amber / .badge.u-amber, spec §8) are
		// added to chassis.css in Phase 6.
		badgeLabel = def.BadgeLabel
		badgeClass = userBadgeClass(def.BadgeColor)
	}
	// Only direct-streams providers are provider-level "always live". User
	// providers are mixed, so provider.Live stays false and liveness is
	// computed per channel from its Kind below.
	providerLive := def.Type == directStreamsProviderType
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
		BadgeLabel:     badgeLabel,
		BadgeClass:     badgeClass,
		Origin:         origin,
		Kind:           def.Type,
		Live:           providerLive,
		DefaultChannel: def.DefaultChannel,
	}
	channelByGroup := groupChannels(def)
	appendChannel := func(cg *adapters.CatalogGroup, ch ChannelDefinition) {
		cg.Channels = append(cg.Channels, adapters.CatalogChannel{
			ID:       ch.ID,
			Name:     ch.Name,
			PlayMode: strings.ToUpper(string(ch.PlayMode)),
			Live:     providerLive || ch.Kind == kindDirect,
		})
	}
	for _, g := range def.Groups {
		cg := adapters.CatalogGroup{ID: g.ID, Name: g.Name}
		for _, ch := range channelByGroup[g.ID] {
			appendChannel(&cg, ch)
		}
		p.Groups = append(p.Groups, cg)
	}
	// Ungrouped channels (GroupID == "") — bundled providers have none, but
	// user providers may omit groups entirely (spec §8: "channels list flat").
	if ungrouped := channelByGroup[""]; len(ungrouped) > 0 {
		cg := adapters.CatalogGroup{ID: "", Name: ""}
		for _, ch := range ungrouped {
			appendChannel(&cg, ch)
		}
		p.Groups = append(p.Groups, cg)
	}
	return p
}

// userBadgeClass maps a user provider's BadgeColor token to the CSS class the
// chassis renders (spec §8: ".ic.u-<token>" / ".badge.u-<token>"). It runs the
// load-time normalizer so a malformed/empty token falls back to the default
// palette class instead of emitting "u-".
func userBadgeClass(token string) string {
	return "u-" + normalizeBadgeColorForLoad(token)
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
