package dlna

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// Compile-time interface conformance. If any of these stop compiling,
// the adapter has drifted from the contract — fix the adapter, not
// the assertion. Mirrors internal/adapters/url/adapter_interface_test.go
// and adds the PublicRouteProvider check introduced in T1.
var (
	_ adapters.Adapter             = (*Adapter)(nil)
	_ adapters.Validator           = (*Adapter)(nil)
	_ adapters.PublicRouteProvider = (*Adapter)(nil)
)
