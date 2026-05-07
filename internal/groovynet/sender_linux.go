//go:build linux

package groovynet

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// controlSocket on Linux sets IP_MTU_DISCOVER=PMTUDISC_DO so oversized
// datagrams are dropped at the IP layer rather than fragmented (the MiSTer
// receiver reassembles the UDP byte stream at the application level and would
// mis-parse OS-level IP fragments). Do not set SO_REUSEADDR here: the MiSTer
// session is keyed by sender IP:port, so the source port must be exclusive.
func controlSocket(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		// Don't-fragment bit: match the reference sender.
		if e := unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO); e != nil {
			setErr = e
		}
	})
	if err != nil {
		return err
	}
	return setErr
}

// readSndBuf returns the kernel's current SO_SNDBUF for conn, in bytes.
// On Linux the kernel returns approximately 2× the requested size as a
// long-standing bookkeeping quirk; callers must compare conservatively
// (actual >= requested means OK; actual < requested means clamped).
func readSndBuf(conn *net.UDPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var size int
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		size, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF)
	}); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, sockErr
	}
	return size, nil
}
