// Package dlna — testhooks.go.
//
// Helpers that integration tests in tests/integration/ need to inject
// validator stubs without standing up a multi-host LAN testbed. NOT
// for production use. Go's test-scoped-public-API patterns are
// limited; this file is the pragmatic choice — same shape as
// internal/adapters/url/testhooks.go.
//
// External callers should never use these. Production wiring keeps
// the resolver as the unexported defaultDNSResolver variable so the
// adapter package's own _test.go files can swap it via the
// installResolverOverride helper. Integration tests live in a
// different package (tests/integration) and can't reach that helper,
// so we expose a thin public seam here.
package dlna

import (
	"context"
	"net"
)

// DNSResolverFunc is the exported alias of the unexported dnsResolverFunc.
// Integration tests construct stubs with this signature and pass them
// to SetDNSResolverForTesting. The function returns a list of net.IP
// for the given hostname; failure to resolve maps to ErrAddressNotAllowed.
type DNSResolverFunc = dnsResolverFunc

// SetDNSResolverForTesting swaps the package-level defaultDNSResolver
// the URL validator consults. Returns a restore function that puts the
// previous resolver back — callers register it on t.Cleanup.
//
// Concurrency: the swap is NOT safe with concurrent validator calls
// from other tests. Integration tests serialize naturally; if a
// future test suite parallelizes through this seam, switch to
// per-call resolver injection on validateMediaURL.
//
// Production behavior is unaffected — callers use the variable
// directly via validateMediaURL, which reads it at construction time
// inside validate(). A test that swaps the resolver only affects
// validations that begin AFTER the swap.
func SetDNSResolverForTesting(stub DNSResolverFunc) (restore func()) {
	prev := defaultDNSResolver
	defaultDNSResolver = stub
	return func() {
		defaultDNSResolver = prev
	}
}

// StaticIPResolver returns a DNSResolverFunc that maps every hostname
// to the given literal IP. Convenience for integration tests that
// stand up an httptest.Server (loopback) and need the validator to
// classify the response as a private LAN target.
//
// The returned IP must be valid — net.ParseIP failure surfaces as nil
// from the resolver, which the validator maps to ErrAddressNotAllowed
// (the safe default for "we don't know what this resolves to").
func StaticIPResolver(ip string) DNSResolverFunc {
	parsed := net.ParseIP(ip)
	return func(_ context.Context, _ string) ([]net.IP, error) {
		if parsed == nil {
			return nil, nil
		}
		return []net.IP{parsed}, nil
	}
}
