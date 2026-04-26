package url

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// TestRegistry_AcceptsAdapterWithNoBackgroundWork is the spec's primary
// abstraction probe (spec §"Boundary-validation tests"). The URL
// adapter's Start returns nil and spawns no goroutines; this test
// verifies adapters.Registry, the lifecycle dance, and the UIRoutes
// mounting path all tolerate that — proving the abstraction does not
// secretly assume every adapter has background work.
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

	// UIRoutes wired via the same loop the UI server uses (server.go:
	// "for _, a := range Registry.List(); rp, ok := a.(RouteProvider)").
	mounted := 0
	mux := http.NewServeMux()
	for _, listed := range reg.List() {
		rp, ok := listed.(adapters.RouteProvider)
		if !ok {
			continue
		}
		for _, r := range rp.UIRoutes() {
			pattern := "/ui/adapter/" + listed.Name() + "/" + r.Path
			switch r.Method {
			case "GET":
				mux.HandleFunc("GET "+pattern, r.Handler)
			case "POST":
				mux.HandleFunc("POST "+pattern, r.Handler)
			case "DELETE":
				mux.HandleFunc("DELETE "+pattern, r.Handler)
			}
			mounted++
		}
	}
	if mounted != 11 {
		t.Errorf("mounted %d url routes, want 11", mounted)
	}

	// Sanity-check the GET /panel route is reachable via the mux.
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/ui/adapter/url/panel")
	if err != nil {
		t.Fatalf("GET /panel: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/panel status = %d, want 200", resp.StatusCode)
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

	// Adapter should be Running, resolver should be nil, panel should
	// render the "yt-dlp not found" line.
	if a.Status().State != adapters.StateRunning {
		t.Errorf("State = %v, want Running", a.Status().State)
	}
	if a.resolver != nil {
		t.Error("resolver should be nil when probe failed")
	}
	html := a.renderPanel()
	if !strings.Contains(html, "yt-dlp not found") {
		t.Error("panel should show 'yt-dlp not found'")
	}
}
