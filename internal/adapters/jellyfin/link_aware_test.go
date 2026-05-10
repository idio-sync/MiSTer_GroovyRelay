package jellyfin

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// newTestAdapter constructs a minimal Adapter suitable for testing
// IsLinked / LinkPhase without network or disk I/O.
func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(nil, t.TempDir(), "test-uuid", "", nil)
}

func TestAdapter_LinkAware_Linked(t *testing.T) {
	a := newTestAdapter(t)
	a.link.SetLinked("jake", "server-uuid")
	if !a.IsLinked() {
		t.Error("IsLinked: false; want true")
	}
	if got := a.LinkPhase(); got != "linked" {
		t.Errorf("LinkPhase: %q; want linked", got)
	}
}

func TestAdapter_LinkAware_Idle(t *testing.T) {
	a := newTestAdapter(t)
	if a.IsLinked() {
		t.Error("IsLinked: true; want false")
	}
	if got := a.LinkPhase(); got != "idle" {
		t.Errorf("LinkPhase: %q; want idle", got)
	}
}

func TestAdapter_LinkAware_Error(t *testing.T) {
	a := newTestAdapter(t)
	a.link.SetError("auth failed")
	if got := a.LinkPhase(); got != "error" {
		t.Errorf("LinkPhase: %q; want error", got)
	}
}

// Compile-time assertion.
var _ adapters.LinkAware = (*Adapter)(nil)
