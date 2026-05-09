//go:build windows

package dlna

import "syscall"

// controlReusableSocket sets SO_REUSEADDR on a fresh UDP socket so the
// SSDP listener can share port 1900 with other UPnP daemons running on
// the host (Windows Network Discovery, BubbleUPnP server, miniDLNA, the
// Plex media server's own renderer impersonator, etc.). Mirrors the
// Plex GDM helper at internal/adapters/plex/discovery_socket_windows.go;
// kept package-local because the spec is explicit that DLNA must not
// import Plex.
//
// Windows lacks SO_REUSEPORT — REUSEADDR alone is sufficient for the
// shared multicast bind pattern, and the documented Windows behavior is
// that REUSEADDR on a UDP socket grants exactly the multicast-join
// semantics we want.
func controlReusableSocket(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		if e := syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
			setErr = e
			return
		}
	})
	if err != nil {
		return err
	}
	return setErr
}
