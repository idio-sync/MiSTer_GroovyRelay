//go:build windows

package groovynet

import (
	"errors"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

// isSendBufferFull reports whether err means the kernel send queue was full.
// Winsock surfaces this as WSAENOBUFS (10055). Go's syscall.ENOBUFS on
// Windows is a synthetic POSIX constant (0x20000046) that never equals a
// real Winsock errno, so matching it alone silently disables the torn-field
// ENOBUFS telemetry on Windows; it is still accepted here for uniformity
// with the POSIX platforms.
func isSendBufferFull(err error) bool {
	return errors.Is(err, windows.WSAENOBUFS) || errors.Is(err, syscall.ENOBUFS)
}

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
