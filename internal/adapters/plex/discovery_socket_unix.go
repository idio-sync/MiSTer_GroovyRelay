//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || solaris

package plex

import "syscall"

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
