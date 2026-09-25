// Package companion serves the /ui/companion/* JSON API consumed by
// the browser extension (extension/firefox) and holds the DTO types
// shared with internal/adapters/url, which implements the URL-adapter
// half of the companion contract.
//
// Layering: this package imports the adapters root package (for the
// registry) but never an adapter subpackage, so internal/adapters/url
// can import it for the DTOs without a cycle. The consumer-side
// interfaces (CompanionURLSource, CompanionSessionProvider,
// CompanionDisplayProvider) are satisfied structurally and wired
// through Config in main.go.
package companion

import (
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// CompanionPlayResult is the JSON-safe result companion mutating
// routes return. State is the post-call session FSM state (the
// companion API uses the same enum as core.SessionStatus.State,
// per the locked spec). AdapterRef and ResolvedVia mirror the URL
// adapter's existing cast outputs; Title and SourceDisplay are
// optional, populated when a metadata-aware resolver supplied them.
type CompanionPlayResult struct {
	State         core.State
	AdapterRef    string
	ResolvedVia   string
	Title         string
	SourceDisplay string
}

// CompanionHistoryEntry is the redacted, stable-id history shape
// exposed to the browser extension. ID is opaque and stable across
// reorder/bump events. URLDisplay is already redacted by the
// implementation; raw URLs never reach this type.
//
// JSON tag last_played matches the locked spec
// (docs/superpowers/specs/2026-05-09-companion-extension-mini-remote-design.md
// line 226). The on-disk url_history.json HistoryEntry uses
// last_played_at; only the API DTO emits last_played.
type CompanionHistoryEntry struct {
	ID         string    `json:"id"`
	Title      string    `json:"title,omitempty"`
	URLDisplay string    `json:"url_display"`
	LastPlayed time.Time `json:"last_played"`
}

// CompanionSessionDisplay lets adapters enrich core.SessionStatus
// without leaking adapter internals into this package. The URL adapter
// returns a populated value for url:-prefixed AdapterRefs and the
// zero value for foreign refs (so the status route can fall back to
// the registry display name).
type CompanionSessionDisplay struct {
	AdapterName   string
	Title         string
	SourceDisplay string
	ResolvedVia   string
}
