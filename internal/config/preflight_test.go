package config

import (
	"net"
	"testing"
)

func TestProbeTCPPort_Available(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	if err := ProbeTCPPort(port); err != nil {
		t.Errorf("port %d should be available: %v", port, err)
	}
}

func TestProbeTCPPort_InUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	if err := ProbeTCPPort(port); err == nil {
		t.Errorf("port %d should be unavailable (in use)", port)
	}
}

func TestProbeUDPPort_Available(t *testing.T) {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	_ = c.Close()

	if err := ProbeUDPPort(port); err != nil {
		t.Errorf("udp port %d should be available: %v", port, err)
	}
}

func TestProbeDirWritable_Exists(t *testing.T) {
	dir := t.TempDir()
	if err := ProbeDirWritable(dir); err != nil {
		t.Errorf("tempdir should be writable: %v", err)
	}
}

func TestProbeDirWritable_Missing(t *testing.T) {
	if err := ProbeDirWritable("/no/such/path/exists/here"); err == nil {
		t.Error("want error for missing dir")
	}
}
