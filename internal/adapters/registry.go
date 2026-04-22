package adapters

import (
	"fmt"
	"sync"
)

// Registry holds the set of adapters available to the bridge. Lookup
// is map-backed for O(1) Get; registration order is preserved for a
// deterministic sidebar. An RWMutex guards mutations so status
// polling doesn't contend with startup registration.
type Registry struct {
	mu       sync.RWMutex
	order    []string
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: map[string]Adapter{}}
}

// Register adds an adapter keyed by a.Name(). Returns an error on
// duplicate registration — silently overwriting would let a later
// Register erase an already-started adapter.
func (r *Registry) Register(a Adapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := a.Name()
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("adapter %q already registered", name)
	}
	r.adapters[name] = a
	r.order = append(r.order, name)
	return nil
}

// Get returns the adapter registered under name and a presence bool.
func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// List returns adapters in registration order. Returns a fresh slice
// each call so callers can't mutate the registry's internal state.
func (r *Registry) List() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Adapter, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.adapters[name])
	}
	return out
}
