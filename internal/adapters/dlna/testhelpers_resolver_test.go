package dlna

import "testing"

// installResolverOverride swaps defaultDNSResolver for a test-supplied
// stub for the duration of the test. Returned closure is registered on
// t.Cleanup AND returned to the caller — caller-style usage looks like:
//
//	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))
//
// The double-restore pattern (Cleanup + return) is intentional: tests
// that need finer-grained restore (e.g. swap mid-test) call the
// returned closure directly, while tests that want "for the whole
// test" just t.Cleanup it.
//
// Tests run sequentially by default; SetAVTransportURI tests don't
// invoke t.Parallel(), so the package-level swap is safe. If a future
// test marks itself parallel and depends on this seam, switch to a
// table-driven sub-test pattern with single-test Cleanup or thread the
// resolver through validateMediaURL via an explicit options arg.
func installResolverOverride(t *testing.T, stub dnsResolverFunc) func() {
	t.Helper()
	prev := defaultDNSResolver
	defaultDNSResolver = stub
	restore := func() {
		defaultDNSResolver = prev
	}
	t.Cleanup(restore)
	return restore
}
