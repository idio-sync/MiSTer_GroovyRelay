package chassis

import (
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
