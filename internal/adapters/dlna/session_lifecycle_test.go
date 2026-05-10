package dlna

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// fakeSessionManager is a configurable SessionManager for lifecycle and
// ownership-guard tests. statusFn lets a test inject the SessionStatus
// returned by Status() so we can model the three ownership cases:
// no active session, our session, and a foreign (e.g. plex:) session.
//
// Method counters are kept for completeness even though P2.1 only
// exercises Status; P2.3 / P2.4 will reuse this fake for StartSession,
// Pause, etc.
type fakeSessionManager struct {
	mu sync.Mutex

	statusFn func() core.SessionStatus

	startCalls int
	pauseCalls int
	playCalls  int
	stopCalls  int
	seekCalls  int
}

func (f *fakeSessionManager) StartSession(core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	return nil
}

func (f *fakeSessionManager) StartSessionIfAdapterRef(req core.SessionRequest, ref string) (bool, error) {
	return true, f.StartSession(req)
}

func (f *fakeSessionManager) Status() core.SessionStatus {
	if f.statusFn != nil {
		return f.statusFn()
	}
	return core.SessionStatus{}
}

func (f *fakeSessionManager) Pause() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauseCalls++
	return nil
}

func (f *fakeSessionManager) PauseIfAdapterRef(ref string) (bool, error) {
	return true, f.Pause()
}

func (f *fakeSessionManager) Play() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playCalls++
	return nil
}

func (f *fakeSessionManager) PlayIfAdapterRef(ref string) (bool, error) {
	return true, f.Play()
}

func (f *fakeSessionManager) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	return nil
}

func (f *fakeSessionManager) StopIfAdapterRef(ref string) (bool, error) {
	return true, f.Stop()
}

func (f *fakeSessionManager) SeekTo(int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seekCalls++
	return nil
}

func (f *fakeSessionManager) SeekToIfAdapterRef(ref string, offsetMs int) (bool, error) {
	return true, f.SeekTo(offsetMs)
}

// newAdapterWithFake builds an Adapter whose core is the given fake.
// Mirrors the validAdapterConfig() helper but routes the test-supplied
// SessionManager so ownership-guard tests can inject AdapterRef values.
func newAdapterWithFake(t *testing.T, fake SessionManager) *Adapter {
	t.Helper()
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestMintSessionRef_FormatAndUniqueness(t *testing.T) {
	a := newAdapterWithFake(t, &fakeSessionManager{})

	const iters = 32
	seen := make(map[string]struct{}, iters)
	for i := 0; i < iters; i++ {
		ref := a.mintSessionRef()
		if !strings.HasPrefix(ref, "dlna:") {
			t.Fatalf("ref %q missing %q prefix", ref, "dlna:")
		}
		hex := strings.TrimPrefix(ref, "dlna:")
		if hex == "" {
			t.Fatalf("ref %q has empty payload after prefix", ref)
		}
		// crypto/rand fallback aside, normal output is 16 hex chars (8
		// random bytes). Tolerate any non-empty hex payload to keep the
		// test robust against the time-based fallback path.
		if _, dup := seen[ref]; dup {
			t.Fatalf("duplicate ref %q after %d iterations", ref, i)
		}
		seen[ref] = struct{}{}
	}
}

func TestMarkStartInFlight_SetsRefAndFlag(t *testing.T) {
	a := newAdapterWithFake(t, &fakeSessionManager{})

	// Initial state: no session, no in-flight start.
	a.mu.Lock()
	if a.currentRef != "" || a.startInFlight {
		t.Fatalf("initial state: currentRef=%q startInFlight=%v, want \"\"/false",
			a.currentRef, a.startInFlight)
	}
	a.mu.Unlock()

	ref := a.markStartInFlight()
	if ref == "" || !strings.HasPrefix(ref, "dlna:") {
		t.Fatalf("markStartInFlight returned %q, want non-empty %q-prefixed", ref, "dlna:")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != ref {
		t.Errorf("currentRef = %q, want %q", a.currentRef, ref)
	}
	if !a.startInFlight {
		t.Error("startInFlight = false, want true after markStartInFlight")
	}
}

func TestClearStartInFlight_SuccessKeepsRef(t *testing.T) {
	a := newAdapterWithFake(t, &fakeSessionManager{})
	ref := a.markStartInFlight()

	a.clearStartInFlight(ref, true)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != ref {
		t.Errorf("after success clear, currentRef = %q, want %q (success keeps ref)",
			a.currentRef, ref)
	}
	if a.startInFlight {
		t.Error("startInFlight = true after success clear, want false")
	}
}

func TestClearStartInFlight_FailureClearsRef(t *testing.T) {
	a := newAdapterWithFake(t, &fakeSessionManager{})
	ref := a.markStartInFlight()

	a.clearStartInFlight(ref, false)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != "" {
		t.Errorf("after failure clear, currentRef = %q, want empty (rollback)", a.currentRef)
	}
	if a.startInFlight {
		t.Error("startInFlight = true after failure clear, want false")
	}
}

func TestClearStartInFlight_RefMismatchIsNoOp(t *testing.T) {
	// Models lifecycle step 4: a faster session's OnStop or a newer
	// markStartInFlight has already replaced currentRef. The original
	// caller's clearStartInFlight must not clobber the winner's state.
	a := newAdapterWithFake(t, &fakeSessionManager{})

	staleRef := a.markStartInFlight()
	// Simulate the "newer session won the race" condition by minting
	// a fresh ref under mu — the same thing markStartInFlight would do.
	winnerRef := a.markStartInFlight()
	if winnerRef == staleRef {
		t.Fatalf("test setup: two markStartInFlight calls produced same ref %q", winnerRef)
	}

	a.clearStartInFlight(staleRef, false)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != winnerRef {
		t.Errorf("stale clear corrupted currentRef: got %q, want %q (winner)",
			a.currentRef, winnerRef)
	}
	if !a.startInFlight {
		t.Error("stale clear cleared startInFlight; winner is still in-flight")
	}
}

func TestOwnershipGuard_NoActiveSession_Allows(t *testing.T) {
	fake := &fakeSessionManager{
		// Default zero-value SessionStatus has empty AdapterRef — the
		// "no active core session" case. The guard must allow regardless
		// of whether the adapter itself holds a currentRef.
		statusFn: func() core.SessionStatus { return core.SessionStatus{} },
	}
	a := newAdapterWithFake(t, fake)
	_ = a.markStartInFlight() // adapter thinks it owns one; core says no session.

	if err := a.ownershipGuard(); err != nil {
		t.Errorf("ownershipGuard with empty AdapterRef = %v, want nil", err)
	}
}

func TestOwnershipGuard_OwnSession_Allows(t *testing.T) {
	// statusRef is the AdapterRef the fake will return; we set it after
	// markStartInFlight so the closure can read the actual minted ref.
	// Modeling the real data flow: core.Manager carries the ref it
	// received from StartSession.
	var statusRef string
	fake := &fakeSessionManager{
		statusFn: func() core.SessionStatus {
			return core.SessionStatus{AdapterRef: statusRef}
		},
	}
	a := newAdapterWithFake(t, fake)
	statusRef = a.markStartInFlight()

	if err := a.ownershipGuard(); err != nil {
		t.Errorf("ownershipGuard with matching AdapterRef = %v, want nil", err)
	}
}

func TestOwnershipGuard_ForeignSession_Rejects(t *testing.T) {
	fake := &fakeSessionManager{
		statusFn: func() core.SessionStatus {
			// Plex / Jellyfin / URL adapter has the active session.
			return core.SessionStatus{AdapterRef: "plex:/library/metadata/1234"}
		},
	}
	a := newAdapterWithFake(t, fake)
	_ = a.markStartInFlight() // adapter holds a dlna:* ref; core has plex:*.

	err := a.ownershipGuard()
	if err == nil {
		t.Fatal("ownershipGuard with foreign AdapterRef = nil, want errForeignSession")
	}
	if !errors.Is(err, errForeignSession) {
		t.Errorf("ownershipGuard error = %v, want errForeignSession", err)
	}
}
