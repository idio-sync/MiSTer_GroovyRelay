//go:build linux

package groovynet

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// controlSocket on Linux sets SO_REUSEADDR and IP_MTU_DISCOVER=PMTUDISC_DO
// so oversized datagrams are dropped at the IP layer rather than fragmented
// (the MiSTer receiver reassembles the UDP byte stream at the application
// level and would mis-parse OS-level IP fragments).
func controlSocket(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); e != nil {
			setErr = e
			return
		}
		// Don't-fragment bit: match the reference sender.
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO)
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
