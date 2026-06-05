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

// EnsureDataDirWritable creates dir when needed, then checks that the current
// process can create files in it. Writes + removes a small zero-byte probe file.
// The probe file name starts with "." so on platforms that refresh ls/dir
// listings during the test the noise stays hidden.
//
// Relative paths are rejected: a data_dir must be absolute so that the bridge
// can locate it deterministically regardless of the process working directory.
func EnsureDataDirWritable(dir string) error {
	if dir == "" {
		return fmt.Errorf("data_dir is empty")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("data_dir %q must be an absolute path", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("data_dir %q not writable: %w", dir, err)
	}
	return probeExistingDirWritable(dir)
}

// ProbeDirWritable preserves the existing pre-flight API while adopting the
// native first-run behavior: missing data directories are created.
func ProbeDirWritable(dir string) error {
	return EnsureDataDirWritable(dir)
}

func probeExistingDirWritable(dir string) error {
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
