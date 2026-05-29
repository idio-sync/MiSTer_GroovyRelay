package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// linkSnapshot reports link state from the persisted token, NOT the
// in-memory LinkState (which is only hydrated during Start). This makes
// the initial drawer render correct even when the adapter is disabled or
// not yet started. It never probes the server or wipes tokens — server_url
// drift / token rejection remain Start()'s responsibility.
//
// Unlike LoadToken, this function distinguishes a JSON parse failure
// (→ error phase) from a missing file (→ unlinked). LoadToken silently
// swallows corrupt JSON; the drawer must surface corruption so the
// operator knows to re-link rather than silently appearing unlinked.
func (a *Adapter) linkSnapshot() adapters.LinkSnapshot {
	data, err := os.ReadFile(a.tokenPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return adapters.LinkSnapshot{
				Phase:          adapters.LinkPhaseUnlinked,
				NeedsServerURL: a.configuredServerURL() == "",
			}
		}
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: err.Error()}
	}

	var tok Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return adapters.LinkSnapshot{
			Phase: adapters.LinkPhaseError,
			Error: fmt.Sprintf("token file corrupt: %v", err),
		}
	}

	if tok.AccessToken == "" {
		return adapters.LinkSnapshot{
			Phase:          adapters.LinkPhaseUnlinked,
			NeedsServerURL: a.configuredServerURL() == "",
		}
	}
	return adapters.LinkSnapshot{
		Phase:    adapters.LinkPhaseLinked,
		LinkedAs: fmt.Sprintf("%s on %s", tok.UserName, tok.ServerID),
	}
}

// Snapshot implements adapters.LinkController.
func (a *Adapter) Snapshot() adapters.LinkSnapshot { return a.linkSnapshot() }

// _ keeps context and strings imported until StartLink/PollLink/Unlink land in Task 6.
var _ = context.Background
var _ = strings.TrimSpace
