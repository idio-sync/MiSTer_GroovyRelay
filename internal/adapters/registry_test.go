package adapters

import (
	"sync"
	"testing"
)

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
