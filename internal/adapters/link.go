package adapters

import "context"

// Link phases shared by every linkable adapter's LinkSnapshot and the
// chassis LinkView. Stable wire strings — the JS renderer branches on them.
const (
	LinkPhaseUnlinked = "unlinked"
	LinkPhasePending  = "pending"
	LinkPhaseLinked   = "linked"
	LinkPhaseError    = "error"
)

// LinkSnapshot is the transport-agnostic link state an adapter reports.
// The cmd/ binding maps it onto the chassis LinkView; the chassis never
// sees this type (it imports no adapter package).
type LinkSnapshot struct {
	Phase          string // one of LinkPhase*
	LinkedAs       string // optional identity; empty renders plain "Linked" (Plex)
	Code           string // pending only (Plex PIN)
	ExpiresInSec   int    // pending only (Plex PIN countdown)
	NeedsServerURL bool   // credential adapters with no server_url yet (Jellyfin)
	Error          string // error phase, or a linked-phase warning (post-auth restart trouble)
}

// LinkController is the orchestration a linkable adapter exposes so both
// the legacy /ui HTML handlers and the chassis JSON binding drive the
// same link-state machine. Implementations must keep the adapter's own
// phase + event emissions authoritative regardless of caller.
//
// Method names: the start method is StartLink (not Start) so an adapter
// can implement this interface directly — adapters.Adapter already
// requires a lifecycle Start(context.Context) error, and two Start
// methods on one receiver will not compile. Snapshot/PollLink/Unlink do
// not collide with the adapter interface.
type LinkController interface {
	// Snapshot returns current state with no side effects. Must be safe
	// to call before StartLink (initial drawer render), reading persisted
	// state from disk where the in-memory machine is not yet hydrated.
	Snapshot() LinkSnapshot
	// StartLink begins pairing. PIN adapters ignore params and return a
	// pending snapshot; credential adapters read params and return a
	// terminal (linked|error) snapshot.
	StartLink(ctx context.Context, params map[string]string) (LinkSnapshot, error)
	// PollLink advances/reads a pending flow (PIN adapters); credential
	// adapters return Snapshot().
	PollLink(ctx context.Context) (LinkSnapshot, error)
	// Unlink best-effort revokes/logs out, clears the token, returns an
	// unlinked snapshot. Idempotent.
	Unlink(ctx context.Context) (LinkSnapshot, error)
}
