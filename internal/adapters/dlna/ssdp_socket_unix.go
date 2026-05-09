//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || solaris

package dlna

import "syscall"

// controlReusableSocket sets SO_REUSEADDR on the SSDP listen socket so
// port 1900 can be shared with other UPnP daemons (miniDLNA, gerbera,
// kodi, etc.) on the host. Mirrors
// internal/adapters/plex/discovery_socket_unix.go — kept
// package-local because the DLNA spec is explicit that the adapter
// must not import Plex.
//
// We deliberately only set SO_REUSEADDR (not SO_REUSEPORT). REUSEADDR
// is sufficient for the multicast-bind-shared-port case the SSDP
// listener exercises: two processes can both join 239.255.255.250:1900
// and each receive a copy of inbound multicast datagrams. SO_REUSEPORT
// is non-portable across the BSD family (different numeric opt values
// across Linux / macOS / FreeBSD / NetBSD) and adding it here would
// require per-OS build tags for a benefit that REUSEADDR already
// delivers in practice.
func controlReusableSocket(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
			setErr = e
			return
		}
	})
	if err != nil {
		return err
	}
	return setErr
}
