//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris && !windows

package dlna

import "syscall"

// controlReusableSocket is a no-op on platforms whose syscall package
// doesn't expose SO_REUSEADDR (plan9, js/wasm, etc.). DLNA still works
// on these targets — controllers can reach the bridge — they just
// can't co-exist with another UPnP daemon on port 1900. Mirrors the
// fallback at internal/adapters/plex/discovery_socket_other.go.
func controlReusableSocket(network, address string, c syscall.RawConn) error {
	return nil
}
