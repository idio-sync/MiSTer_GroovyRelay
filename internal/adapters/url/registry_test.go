package url

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// TestRegistry_AcceptsAdapterWithNoBackgroundWork is the spec's primary
// abstraction probe (spec §"Boundary-validation tests"). The URL
// adapter's Start returns nil and spawns no goroutines; this test
// verifies adapters.Registry and the lifecycle dance tolerate that —
// proving the abstraction does not secretly assume every adapter has
// background work.
func TestRegistry_AcceptsAdapterWithNoBackgroundWork(t *testing.T) {
	reg := adapters.NewRegistry()
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   &fakeCore{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Lifecycle: Start must succeed; Status must reflect it; Stop must
	// succeed; Status reflects that too.
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := a.Status().State; got != adapters.StateRunning {
		t.Errorf("post-Start State = %v, want StateRunning", got)
	}

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := a.Status().State; got != adapters.StateStopped {
		t.Errorf("post-Stop State = %v, want StateStopped", got)
	}
}

// TestRegistry_AcceptsAdapterWithExternalProcessDep extends the v1
// boundary test (TestRegistry_AcceptsAdapterWithNoBackgroundWork) to
// confirm the URL adapter starts cleanly even when its external
// process dependency (yt-dlp) is absent. Graceful degradation through
// the registry boundary.
func TestRegistry_AcceptsAdapterWithExternalProcessDep(t *testing.T) {
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   nil,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.cfg.Enabled = true
	a.cfg.YtdlpEnabled = true
	// Probe says binary is missing.
	a.probeFn = func() ytdlpProbe { return ytdlpProbe{OK: false} }

	reg := adapters.NewRegistry()
	if err := reg.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Adapter should be Running, resolver should be nil, and the Quick
	// Cast tab should degrade to direct-only (no yt-dlp mode radio).
	if a.Status().State != adapters.StateRunning {
		t.Errorf("State = %v, want Running", a.Status().State)
	}
	if a.resolver != nil {
		t.Error("resolver should be nil when probe failed")
	}
	tabs := a.QuickCastTabs()
	if len(tabs) != 1 {
		t.Fatalf("QuickCastTabs len = %d, want 1", len(tabs))
	}
	for _, f := range tabs[0].Fields {
		if f.Name == "mode" {
			t.Errorf("mode radio offered even though yt-dlp probe failed: %+v", f)
		}
	}
}
