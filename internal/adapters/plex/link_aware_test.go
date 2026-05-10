package plex

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// newTestAdapter returns an Adapter wired with a minimal AdapterConfig
// sufficient for testing IsLinked / LinkPhase without network or disk I/O.
// The caller may mutate a.cfg.TokenStore.AuthToken and a.pending directly
// before the test assertion.
func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	return &Adapter{
		cfg: AdapterConfig{
			Core:       &fakeCore{},
			TokenStore: &StoredData{},
		},
	}
}

func TestAdapter_LinkAware_Linked(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.TokenStore.AuthToken = "tok-abc"
	if !a.IsLinked() {
		t.Error("IsLinked: got false, want true")
	}
	if got := a.LinkPhase(); got != "linked" {
		t.Errorf("LinkPhase: got %q, want %q", got, "linked")
	}
}

func TestAdapter_LinkAware_Idle(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.TokenStore.AuthToken = ""
	if a.IsLinked() {
		t.Error("IsLinked: got true, want false")
	}
	if got := a.LinkPhase(); got != "idle" {
		t.Errorf("LinkPhase: got %q, want %q", got, "idle")
	}
}

func TestAdapter_LinkAware_PINIssued(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.TokenStore.AuthToken = ""
	a.pending = newPendingLink("ABCD", 12345, time.Now().Add(2*time.Minute))
	if a.IsLinked() {
		t.Error("IsLinked with pending: got true, want false")
	}
	if got := a.LinkPhase(); got != "pin-issued" {
		t.Errorf("LinkPhase pending: got %q, want %q", got, "pin-issued")
	}
}

func TestAdapter_LinkAware_PINExpired(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.TokenStore.AuthToken = ""
	a.pending = newPendingLink("ABCD", 12345, time.Now().Add(-1*time.Second))
	if got := a.LinkPhase(); got != "error" {
		t.Errorf("LinkPhase expired: got %q, want %q", got, "error")
	}
}

func TestAdapter_LinkAware_PendingDoneWithError(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.TokenStore.AuthToken = ""
	p := newPendingLink("ABCD", 12345, time.Now().Add(2*time.Minute))
	p.complete("", "denied by user")
	a.pending = p
	if got := a.LinkPhase(); got != "error" {
		t.Errorf("LinkPhase done-with-error: got %q, want %q", got, "error")
	}
}

func TestAdapter_LinkAware_PendingDoneSuccess(t *testing.T) {
	a := newTestAdapter(t)
	a.cfg.TokenStore.AuthToken = "" // not yet snapshotted
	p := newPendingLink("ABCD", 12345, time.Now().Add(2*time.Minute))
	p.complete("tok-just-arrived", "")
	a.pending = p
	if got := a.LinkPhase(); got != "linked" {
		t.Errorf("LinkPhase done-success: got %q, want %q", got, "linked")
	}
}

// Compile-time assertion.
var _ adapters.LinkAware = (*Adapter)(nil)
