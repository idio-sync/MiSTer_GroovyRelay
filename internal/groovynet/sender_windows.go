//go:build windows

package groovynet

import (
	"net"
	"syscall"
)

// controlSocket on Windows intentionally leaves the UDP source port exclusive.
// The MiSTer session is keyed by sender IP:port, so sharing it with another
// process can route ACKs away from this socket.
func controlSocket(network, address string, c syscall.RawConn) error {
	return nil
}

// readSndBuf returns the kernel's current SO_SNDBUF for conn, in bytes.
func readSndBuf(conn *net.UDPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var size int
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		size, sockErr = syscall.GetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF)
	}); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, sockErr
	}
	return size, nil
}
