package plex

import (
	"context"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// linkSnapshot reports the current link state as an adapters.LinkSnapshot,
// mirroring the state logic in ExtraPanelHTML/handleLinkStatus. Plex
// persists only a device UUID + auth token (no account identity), so a
// linked snapshot carries an empty LinkedAs and the UI renders plain
// "Linked".
func (a *Adapter) linkSnapshot() adapters.LinkSnapshot {
	token := a.snapshotToken()
	pending := a.snapshotPending()

	switch {
	case token != "":
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseLinked}
	case pending != nil && pending.Done() && pending.Error() != "":
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: pending.Error()}
	case pending != nil && pending.Expired():
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "link code expired"}
	case pending != nil && !pending.Done():
		tl := pending.TimeLeft()
		return adapters.LinkSnapshot{
			Phase:        adapters.LinkPhasePending,
			Code:         pending.Code(),
			ExpiresInSec: int(tl / time.Second),
		}
	default:
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked}
	}
}

// Snapshot implements adapters.LinkController.
func (a *Adapter) Snapshot() adapters.LinkSnapshot { return a.linkSnapshot() }

// _ keeps context imported until StartLink/PollLink/Unlink land in Task 3.
var _ = context.Background
