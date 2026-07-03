//go:build windows

package groovynet

import (
	"net"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestIsSendBufferFull_MatchesWSAENOBUFS proves the matcher recognises the
// error chain the net package actually produces on Windows when the kernel
// send queue is full: net.OpError → os.SyscallError → WSAENOBUFS (10055).
// Go's syscall.ENOBUFS on Windows is a synthetic POSIX constant
// (0x20000046) that never equals a real Winsock error, so matching it alone
// leaves the torn-field telemetry permanently blind on Windows.
func TestIsSendBufferFull_MatchesWSAENOBUFS(t *testing.T) {
	err := &net.OpError{
		Op:  "write",
		Net: "udp",
		Err: os.NewSyscallError("wsasendto", windows.WSAENOBUFS),
	}
	if !isSendBufferFull(err) {
		t.Error("isSendBufferFull() = false for wrapped WSAENOBUFS, want true")
	}
}
