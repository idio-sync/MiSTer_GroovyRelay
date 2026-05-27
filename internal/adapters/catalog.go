package adapters

import "context"

// CatalogProvider is one provider tab in the receiver catalog drawer.
// 3B exposes exactly the three bundled Streams providers (MTV Rewind,
// Cartoon Rewind, Toonami Aftermath). Future per-source library specs
// (plex, jellyfin) will return the same shape.
type CatalogProvider struct {
	ID             string         // e.g. "mtv-rewind"
	DisplayName    string         // e.g. "MTV Rewind"
	BadgeLabel     string         // e.g. "MTV" — small text in .ic glyph
	BadgeClass     string         // e.g. "mtv" | "cartoon" | "toonami" — CSS hook
	Live           bool           // whole provider is always-live (direct streams)
	DefaultChannel string         // for the catalog's initial selection
	Groups         []CatalogGroup // ordered
}

// CatalogGroup is a left-rail entry: a named group of channels within
// one provider.
type CatalogGroup struct {
	ID       string
	Name     string
	Channels []CatalogChannel // ordered
}

// CatalogChannel is one channel card. PlayMode is uppercased ("SEQ" /
// "SHUFFLE") so the template renders the .meta line literally. Live is
// true when the channel is always-live (Toonami direct streams) or its
// provider.Live is true.
type CatalogChannel struct {
	ID       string
	Name     string
	PlayMode string
	Live     bool
}

// StreamsCatalogViewer returns the chassis-shaped catalog for the
// receiver drawer. Read-only; the chassis snapshots this per page
// render.
//
// Implementations must be safe to call before adapter Start (main.go
// binds HTTP first). The streams impl returns the bundled-manifest
// providers as a local-only bootstrap when remote refresh has not yet
// populated catalogs.
type StreamsCatalogViewer interface {
	Catalog() []CatalogProvider
}

// StreamsCaster casts a specific catalog channel. The chassis HTTP
// handler validates the inputs are non-empty and forwards directly;
// implementations must validate against their own catalog and return
// a typed *QuickCastError for status/chip propagation.
type StreamsCaster interface {
	CastChannel(ctx context.Context, providerID, channelID string) error
}
