package groovynet

import (
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// versionStub answers GET_VERSION on a loopback socket. Before replying it
// sends each datagram in pre, so tests can inject stray ACKs.
func versionStub(t *testing.T, reply []byte, pre ...[]byte) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n == 1 && buf[0] == groovy.CmdGetVersion {
				for _, p := range pre {
					_, _ = conn.WriteToUDP(p, src)
				}
				if reply != nil {
					_, _ = conn.WriteToUDP(reply, src)
				}
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

func TestProbeVersion_ReturnsReplyByte(t *testing.T) {
	addr := versionStub(t, []byte{2})
	s, err := NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.ProbeVersion(200 * time.Millisecond)
	if err != nil || v != 2 {
		t.Fatalf("ProbeVersion = %d, %v; want 2, nil", v, err)
	}
}

func TestProbeVersion_SkipsStrayACK(t *testing.T) {
	addr := versionStub(t, []byte{1}, make([]byte, groovy.ACKPacketSize))
	s, err := NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.ProbeVersion(200 * time.Millisecond)
	if err != nil || v != 1 {
		t.Fatalf("ProbeVersion = %d, %v; want 1, nil", v, err)
	}
}

func TestProbeVersion_TimeoutReturnsZero(t *testing.T) {
	addr := versionStub(t, nil)
	s, err := NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now()
	v, err := s.ProbeVersion(50 * time.Millisecond)
	if err != nil || v != 0 {
		t.Fatalf("ProbeVersion = %d, %v; want 0, nil", v, err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("ProbeVersion took %v, want about 50ms", el)
	}
}

func TestSender_LastSendTracksWrites(t *testing.T) {
	s, err := NewSender("127.0.0.1", 12345, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.LastSend().IsZero() {
		t.Fatal("LastSend before any write should be zero")
	}
	before := time.Now()
	if err := s.Send([]byte{groovy.CmdGetStatus}); err != nil {
		t.Fatal(err)
	}
	if s.LastSend().Before(before.Add(-time.Millisecond)) {
		t.Fatalf("LastSend %v not updated by Send (before %v)", s.LastSend(), before)
	}
	mark := s.LastSend()
	time.Sleep(2 * time.Millisecond)
	if err := s.SendPayload(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	if !s.LastSend().After(mark) {
		t.Fatal("LastSend not updated by SendPayload")
	}
}

func TestSender_SendIfIdle(t *testing.T) {
	s, err := NewSender("127.0.0.1", 12345, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sent, err := s.SendIfIdle([]byte{groovy.CmdGetStatus}, time.Hour)
	if err != nil || !sent {
		t.Fatalf("first SendIfIdle = %v, %v; want sent (never written before)", sent, err)
	}
	sent, err = s.SendIfIdle([]byte{groovy.CmdGetStatus}, time.Hour)
	if err != nil || sent {
		t.Fatalf("SendIfIdle right after a write = %v, %v; want skipped", sent, err)
	}
	time.Sleep(20 * time.Millisecond)
	sent, err = s.SendIfIdle([]byte{groovy.CmdGetStatus}, 10*time.Millisecond)
	if err != nil || !sent {
		t.Fatalf("SendIfIdle after idle = %v, %v; want sent", sent, err)
	}
}

// A GET_VERSION reply that arrives after ProbeVersion timed out must not
// fail the INIT handshake that follows.
func TestSendInitAwaitACK_SkipsLateVersionReply(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 64)
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil || n == 0 || buf[0] != groovy.CmdInit {
			return
		}
		_, _ = conn.WriteToUDP([]byte{2}, src) // late version reply
		_, _ = conn.WriteToUDP(make([]byte, groovy.ACKPacketSize), src)
	}()
	s, err := NewSender("127.0.0.1", conn.LocalAddr().(*net.UDPAddr).Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.SendInitAwaitACK(groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888), 300*time.Millisecond); err != nil {
		t.Fatalf("SendInitAwaitACK = %v, want the ACK after the stray byte", err)
	}
}
