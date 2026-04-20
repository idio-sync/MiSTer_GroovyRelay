//go:build linux

package groovynet

import (
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
