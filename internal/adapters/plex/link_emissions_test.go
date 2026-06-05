package plex

import (
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// finalizeLinkSuccess calls the production finishPendingLink path for a
// successful token receipt. The pending field must already be set on a.
func finalizeLinkSuccess(a *Adapter, token string) {
	a.finishPendingLink(a.pending, token, "")
}

// finalizeLinkFailure calls the production finishPendingLink path for a
// failure/denial. The pending field must already be set on a.
func finalizeLinkFailure(a *Adapter, errMsg string) {
	a.finishPendingLink(a.pending, "", errMsg)
}

func TestLinkPoll_EmitsAdapterLinkedOnSuccess(t *testing.T) {
	log := eventlog.New(16)
	a := newTestAdapter(t)
	a.cfg.EventLog = log
	a.pending = newPendingLink("ABCD", 12345, time.Now().Add(2*time.Minute))

	finalizeLinkSuccess(a, "tok-result")
	entries := log.Snapshot()
	if len(entries) == 0 {
		t.Fatal("expected adapter-linked entry")
	}
	e := entries[len(entries)-1]
	if !strings.Contains(e.Message, "adapter-linked") || e.Severity != eventlog.SeverityInfo {
		t.Errorf("entry mismatch: %+v", e)
	}
}

func TestLinkPoll_EmitsAdapterLinkFailedOnError(t *testing.T) {
	log := eventlog.New(16)
	a := newTestAdapter(t)
	a.cfg.EventLog = log
	a.pending = newPendingLink("ABCD", 12345, time.Now().Add(2*time.Minute))

	finalizeLinkFailure(a, "denied by user")
	entries := log.Snapshot()
	if len(entries) == 0 {
		t.Fatal("expected adapter-link-failed entry")
	}
	e := entries[len(entries)-1]
	if !strings.Contains(e.Message, "adapter-link-failed") || e.Severity != eventlog.SeverityErr {
		t.Errorf("entry mismatch: %+v", e)
	}
}

// TestLinkPoll_AbandonedDoesNotEmit verifies that when pollPendingLink
// detects a superseded flow (a.pending != pl) it does NOT emit any event.
// Abandoned flows are a race artifact; surfacing them as errors is noisy.
func TestLinkPoll_AbandonedDoesNotEmit(t *testing.T) {
	log := eventlog.New(16)
	a := newTestAdapter(t)
	a.cfg.EventLog = log

	// pl is the "old" pending; a.pending now points at a different one.
	pl := newPendingLink("OLD1", 11111, time.Now().Add(2*time.Minute))
	a.pending = newPendingLink("NEW2", 22222, time.Now().Add(2*time.Minute))

	// Simulate the abandoned branch: pl != a.pending, so finishPendingLink
	// is never called for pl. Call complete("", "abandoned") directly, as
	// pollPendingLink does, and verify no emission.
	pl.complete("", "abandoned")

	entries := log.Snapshot()
	if len(entries) != 0 {
		t.Errorf("abandoned flow must not emit; got %d entries: %+v", len(entries), entries)
	}
}

// TestLinkSnapshot_AbandonedCompletesUnlinked pins the snapshot side of the
// pollPendingLink context.Canceled guard: an abandoned flow (rapid re-click or
// Stop/disable while a PIN is pending) is completed with an empty error
// (pl.complete("", "")), which linkSnapshot must report as UNLINKED — not as an
// error phase — so a polling drawer doesn't flash a spurious "ERR" for a normal
// abandon. Paired with the no-emit guarantee covered above.
func TestLinkSnapshot_AbandonedCompletesUnlinked(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	pl := newPendingLink("ABCD", 1, time.Now().Add(2*time.Minute))
	pl.complete("", "") // exactly what the context.Canceled guard does
	a.pending = pl
	if got := a.linkSnapshot(); got.Phase != adapters.LinkPhaseUnlinked {
		t.Errorf("Phase = %q, want unlinked for an abandoned (canceled) flow", got.Phase)
	}
}
