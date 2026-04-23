package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// ProbeTCPPort tries to bind a TCP listener on the given port at
// 127.0.0.1, immediately closing it on success. Returns nil if the
// port is currently bindable, error otherwise. Pre-flight guard
// against "save http_port → container restart → bind fails →
// unbootable bridge" per design §11.3.1.
//
// Caveat: inherently racy — a free port at probe time can be taken
// by something else before the bridge binds it for real. The 99%
// case being caught is typos ("meant 32500, typed 32100 which is
// the UDP sender port").
func ProbeTCPPort(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d not bindable: %w", port, err)
	}
	return l.Close()
}

// ProbeUDPPort tries to bind a UDP packet connection on the given
// port at 127.0.0.1.
func ProbeUDPPort(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("udp port %d not bindable: %w", port, err)
	}
	return c.Close()
}

// ProbeDirWritable checks that dir exists and the current process
// can create files in it. Writes + removes a small zero-byte probe
// file. The probe file name starts with "." so on platforms that
// refresh ls/dir listings during the test the noise stays hidden.
func ProbeDirWritable(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("data_dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("data_dir %q: not a directory", dir)
	}
	probe := filepath.Join(dir, ".writable-probe")
	f, err := os.Create(probe)
	if err != nil {
		return fmt.Errorf("data_dir %q not writable: %w", dir, err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}
