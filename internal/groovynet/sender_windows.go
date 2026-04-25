//go:build windows

package groovynet

import (
	"net"
	"syscall"
)

// controlSocket on Windows sets SO_REUSEADDR so a rapid bridge restart does
// not hit TIME_WAIT on the stable source port. IP_DONTFRAGMENT is available
// via golang.org/x/sys/windows but is not strictly required for the dev
// loop on this platform — the MiSTer production path is Linux, where the
// analogous IP_MTU_DISCOVER is set in sender_linux.go.
func controlSocket(network, address string, c syscall.RawConn) error {
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
