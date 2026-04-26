// Package url — testhooks.go.
//
// Helpers that integration tests in tests/integration/ need to inject
// stubs without spinning up real yt-dlp processes. NOT for production
// use. Go's test-scoped-public-API patterns are limited; this file is
// the pragmatic choice (clearly named, single-purpose).
//
// External callers should never use these. Production wiring is in
// Adapter.Start() — see adapter.go.
package url

// ResolverIface is the exported alias of the unexported resolverIface
// so integration tests can name it. Internal callers use resolverIface.
type ResolverIface = resolverIface

// YtdlpProbe is the exported alias of the unexported ytdlpProbe so
// integration tests can construct one.
type YtdlpProbe = ytdlpProbe

// SetResolverForTesting injects a stub resolver under a.mu (matches
// the locking discipline production Start uses — review fix I4).
// Intended only for tests/integration/.
func (a *Adapter) SetResolverForTesting(r ResolverIface) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resolver = r
}

// SetYtdlpProbeForTesting injects a stub probe result. Same locking
// caveat as SetResolverForTesting.
func (a *Adapter) SetYtdlpProbeForTesting(p YtdlpProbe) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ytdlpProbe = p
}
