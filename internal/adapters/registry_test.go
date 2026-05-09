package adapters

import (
	"net/http"
	"sync"
	"testing"
)

// publicRouteAdapter is a stubAdapter that also satisfies
// PublicRouteProvider by recording MountPublicRoutes invocations on a
// counter. Used to verify the registry walk in main.go correctly
// dispatches to adapters that implement the optional interface.
type publicRouteAdapter struct {
	stubAdapter
	mountCount int
}

func (p *publicRouteAdapter) MountPublicRoutes(*http.ServeMux) {
	p.mountCount++
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	a := &stubAdapter{name: "plex"}
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("plex")
	if !ok || got != a {
		t.Error("Get did not return the registered adapter")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubAdapter{name: "plex"})
	if err := r.Register(&stubAdapter{name: "plex"}); err == nil {
		t.Fatal("want error on duplicate Register")
	}
}

func TestRegistry_Get_Missing(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("nope"); ok {
		t.Error("Get(nope) should return ok=false")
	}
}

func TestRegistry_ListPreservesOrder(t *testing.T) {
	r := NewRegistry()
	names := []string{"plex", "jellyfin", "dlna", "url"}
	for _, n := range names {
		_ = r.Register(&stubAdapter{name: n})
	}
	got := r.List()
	for i, n := range names {
		if got[i].Name() != n {
			t.Errorf("List[%d] = %q, want %q", i, got[i].Name(), n)
		}
	}
}

// TestPublicRouteProvider_TypeAssertionFromList verifies an adapter
// that satisfies PublicRouteProvider is type-assertable from List(),
// proving the main.go startup walk can dispatch protocol routes
// (DLNA SSDP, SOAP, GENA) onto the shared mux.
func TestPublicRouteProvider_TypeAssertionFromList(t *testing.T) {
	r := NewRegistry()
	pra := &publicRouteAdapter{stubAdapter: stubAdapter{name: "dlna"}}
	if err := r.Register(pra); err != nil {
		t.Fatalf("Register: %v", err)
	}
	mux := http.NewServeMux()
	var matched int
	for _, a := range r.List() {
		if pp, ok := a.(PublicRouteProvider); ok {
			pp.MountPublicRoutes(mux)
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}
	if pra.mountCount != 1 {
		t.Errorf("mountCount = %d, want 1", pra.mountCount)
	}
}

// TestPublicRouteProvider_NonImplementerSkipped verifies a plain
// stubAdapter (which does not implement PublicRouteProvider) is
// correctly skipped by the type assertion. Guards against accidentally
// embedding a method on the base Adapter interface.
func TestPublicRouteProvider_NonImplementerSkipped(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubAdapter{name: "plain"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, a := range r.List() {
		if _, ok := a.(PublicRouteProvider); ok {
			t.Errorf("stubAdapter %q unexpectedly satisfies PublicRouteProvider", a.Name())
		}
	}
}

// TestPublicRouteProvider_MixedRegistry verifies the walk dispatches
// only to implementers when both kinds are registered side-by-side.
func TestPublicRouteProvider_MixedRegistry(t *testing.T) {
	r := NewRegistry()
	plain := &stubAdapter{name: "plex"}
	pra := &publicRouteAdapter{stubAdapter: stubAdapter{name: "dlna"}}
	if err := r.Register(plain); err != nil {
		t.Fatalf("Register plain: %v", err)
	}
	if err := r.Register(pra); err != nil {
		t.Fatalf("Register pra: %v", err)
	}
	mux := http.NewServeMux()
	for _, a := range r.List() {
		if pp, ok := a.(PublicRouteProvider); ok {
			pp.MountPublicRoutes(mux)
		}
	}
	if pra.mountCount != 1 {
		t.Errorf("publicRouteAdapter mountCount = %d, want 1", pra.mountCount)
	}
}

func TestRegistry_ConcurrentReadAndRegister(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubAdapter{name: "a"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Get("a")
			_ = r.List()
		}()
	}
	wg.Wait()
}
