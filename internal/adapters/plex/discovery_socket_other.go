//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris && !windows

package plex

import "syscall"

func controlReusableSocket(network, address string, c syscall.RawConn) error {
	return nil
}
