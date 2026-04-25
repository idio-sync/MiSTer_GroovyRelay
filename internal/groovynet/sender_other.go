//go:build !linux && !windows

package groovynet

import (
	"net"
	"syscall"
)

// controlSocket on non-Linux / non-Windows dev platforms is a no-op.
// Production target is Linux (see sender_linux.go for SO_REUSEADDR +
// IP_MTU_DISCOVER). Windows dev path sets SO_REUSEADDR in sender_windows.go.
func controlSocket(network, address string, c syscall.RawConn) error {
	return nil
}

// readSndBuf is a no-op on non-Linux/non-Windows platforms. Returns
// (0, nil) so the caller treats it as unsupported. The conn parameter
// is unused but matches the signature of the Linux/Windows shims.
func readSndBuf(conn *net.UDPConn) (int, error) {
	_ = conn
	return 0, nil
}
