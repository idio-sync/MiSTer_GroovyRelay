package adapters

// SourceAvailabilityViewer reports whether a source-adapter is
// configured/ready to receive casts. The chassis uses this to drive
// the source-cluster lamp distinction between "unavailable" (lamp dark)
// and "configured-idle" (lamp dim amber).
//
// Implementations should treat Configured() as a fast in-memory check —
// it is invoked per chassis snapshot tick (4 Hz today). Anything that
// requires I/O should be cached behind an internal field updated on
// the adapter's own clock.
//
// SourceID() returns one of the canonical source identifiers used by
// the chassis source-cluster: "streams" | "plex" | "jellyfin" | "dlna".
type SourceAvailabilityViewer interface {
	SourceID() string
	Configured() bool
}
