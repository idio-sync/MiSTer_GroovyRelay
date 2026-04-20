//go:build !linux && !windows

package groovynet

import "syscall"

// controlSocket on non-Linux / non-Windows dev platforms is a no-op.
// Production target is Linux (see sender_linux.go for SO_REUSEADDR +
// IP_MTU_DISCOVER). Windows dev path sets SO_REUSEADDR in sender_windows.go.
func controlSocket(network, address string, c syscall.RawConn) error {
	return nil
}
