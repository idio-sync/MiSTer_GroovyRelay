package chassis

import (
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// SessionViewer is the narrow read-only view of bridge session state
// the chassis needs. *core.Manager satisfies this structurally via its
// StatusHomeView() method. Tests inject fakes; production wires
// *core.Manager. Mirrors internal/ui.StatusViewer.
//
// Phase 1 / Spec 2 consumes only StatusHomeView(). Spec 3 (transport
// controls) will extend this interface with Pause / Play / Stop / SeekTo
// or introduce a sibling SessionController interface — to be decided
// in that spec's review.
type SessionViewer interface {
	StatusHomeView() core.StatusHomeView
}

// snapshotFromSession builds the page-render data. When sv is nil the
// chassis renders idle-only (offline-friendly + test-friendly).
// Subsequent tasks add live-state mapping; this first implementation
// is the fallback path so handleIndex can be re-wired immediately
// without breaking Phase 0 behaviour.
func snapshotFromSession(cfg Config, sv SessionViewer, now time.Time) ReceiverPageData {
	if sv == nil {
		return idleSnapshot(cfg, now)
	}
	// Live-state mapping lands in Task 3.
	return idleSnapshot(cfg, now)
}
