package dlna

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// fakeDiscovery is a discoveryRunner double that records Run/Close
// invocations without binding sockets. Mirrors the seam the Plex
// adapter uses for its GDM Discovery. Run blocks on its ctx (so the
// adapter's discoveryDone channel only closes after Stop signals
// shutdown via Close, exactly matching the production sequence).
type fakeDiscovery struct {
	runCalled atomic.Bool
	runDone   chan struct{}
	closed    atomic.Bool
}

func newFakeDiscovery() *fakeDiscovery {
	return &fakeDiscovery{runDone: make(chan struct{})}
}

// Run blocks until Close is called or ctx is cancelled. Mirrors
// production Discovery.Run, which is the goroutine the adapter
// launches and waits on inside Stop.
func (f *fakeDiscovery) Run(ctx context.Context) {
	f.runCalled.Store(true)
	select {
	case <-ctx.Done():
	case <-f.runDone:
	}
}

func (f *fakeDiscovery) Close() error {
	if f.closed.CompareAndSwap(false, true) {
		close(f.runDone)
	}
	return nil
}

// fakeBuilder bundles the captured DiscoveryConfig and the fake
// runner the rebound newDiscoveryFn returns. Bound to a per-test
// instance so concurrent tests don't trample one another.
type fakeBuilder struct {
	mu        sync.Mutex
	captured  *DiscoveryConfig
	fake      *fakeDiscovery
	returnErr error
}

func newFakeBuilder() *fakeBuilder {
	return &fakeBuilder{fake: newFakeDiscovery()}
}

// build is the closure the rebound newDiscoveryFn invokes. Captures
// the config (so tests can assert what the adapter passed through)
// and returns either the fake runner or returnErr.
func (f *fakeBuilder) build(cfg DiscoveryConfig) (discoveryRunner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := cfg
	f.captured = &c
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f.fake, nil
}

// installBuilder rebinds the package-level newDiscoveryFn to use the
// builder's closure and registers a cleanup that restores the
// original.
func (f *fakeBuilder) installBuilder(t *testing.T) {
	t.Helper()
	prev := newDiscoveryFn
	newDiscoveryFn = f.build
	t.Cleanup(func() { newDiscoveryFn = prev })
}

func TestAdapterStart_SpawnsDiscoveryWhenEnabled(t *testing.T) {
	fb := newFakeBuilder()
	fb.installBuilder(t)

	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	// Wait for the spawned goroutine to enter Run. Run sets runCalled
	// synchronously on entry, so the wait is bounded.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fb.fake.runCalled.Load() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !fb.fake.runCalled.Load() {
		t.Error("expected Discovery.Run to be invoked")
	}

	// State should be StateRunning after a successful Start.
	if got := a.Status().State; got != adapters.StateRunning {
		t.Errorf("State after Start = %v, want StateRunning", got)
	}
}

func TestAdapterStart_NoDiscoveryWhenDisabled(t *testing.T) {
	calls := atomic.Int32{}
	prev := newDiscoveryFn
	newDiscoveryFn = func(cfg DiscoveryConfig) (discoveryRunner, error) {
		calls.Add(1)
		return nil, errors.New("should not be called")
	}
	t.Cleanup(func() { newDiscoveryFn = prev })

	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Default Enabled=false; do not flip.

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start (disabled): %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("newDiscoveryFn called %d times when disabled; want 0", got)
	}
	if got := a.Status().State; got != adapters.StateStopped {
		t.Errorf("State after disabled Start = %v, want StateStopped", got)
	}
}

func TestAdapterStart_DiscoveryBindFailureSetsStateError(t *testing.T) {
	fb := newFakeBuilder()
	fb.returnErr = errors.New("multicast join failed (simulated)")
	fb.installBuilder(t)

	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)

	startErr := a.Start(context.Background())
	if startErr == nil {
		t.Fatal("Start with bind failure: want error, got nil")
	}
	st := a.Status()
	if st.State != adapters.StateError {
		t.Errorf("State after bind failure = %v, want StateError", st.State)
	}
	if st.LastError == "" {
		t.Error("LastError empty after StateError; should describe bind failure")
	}
}

func TestAdapterStop_ClosesDiscovery(t *testing.T) {
	fb := newFakeBuilder()
	fb.installBuilder(t)

	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for Run to be entered so Stop has something to wait on.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fb.fake.runCalled.Load() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !fb.fake.closed.Load() {
		t.Error("expected Discovery.Close to be invoked during Stop")
	}
	// Stop should have waited for Run to return; the channel must be
	// drained by now.
	select {
	case <-fb.fake.runDone:
		// expected
	default:
		t.Error("Stop returned but Discovery.Run never finished")
	}
	if got := a.Status().State; got != adapters.StateStopped {
		t.Errorf("State after Stop = %v, want StateStopped", got)
	}
}

func TestAdapterStart_PassesConfigToDiscovery(t *testing.T) {
	fb := newFakeBuilder()
	fb.installBuilder(t)

	cfg := validAdapterConfig()
	cfg.HostIP = "10.0.0.42"
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	a.mu.Lock()
	a.cfg.DeviceName = "ProbeName"
	a.mu.Unlock()

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	fb.mu.Lock()
	captured := fb.captured
	fb.mu.Unlock()
	if captured == nil {
		t.Fatal("newDiscoveryFn was not invoked")
	}
	if captured.HostIP != "10.0.0.42" {
		t.Errorf("DiscoveryConfig.HostIP = %q, want 10.0.0.42", captured.HostIP)
	}
	if captured.DeviceUUID != cfg.DeviceUUID {
		t.Errorf("DiscoveryConfig.DeviceUUID = %q, want %q", captured.DeviceUUID, cfg.DeviceUUID)
	}
	if captured.DeviceName != "ProbeName" {
		t.Errorf("DiscoveryConfig.DeviceName = %q, want ProbeName", captured.DeviceName)
	}
	if captured.HTTPPort != cfg.HTTPPort {
		t.Errorf("DiscoveryConfig.HTTPPort = %d, want %d", captured.HTTPPort, cfg.HTTPPort)
	}
}
