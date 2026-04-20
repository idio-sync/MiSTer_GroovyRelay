package groovynet

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestDrainer_PushesAndDrops(t *testing.T) {
	// Set up a pair of UDP sockets; one acts as fake-mister emitting ACKs.
	fake, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()

	s, err := NewSender("127.0.0.1", fake.LocalAddr().(*net.UDPAddr).Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ch := make(chan groovy.ACK, 1) // intentionally small
	d := NewDrainer(s, ch)
	go d.Run()

	// The sender binds 0.0.0.0:srcPort; Windows can't route a sendto to
	// 0.0.0.0, so rewrite the target to the loopback IP with the same port.
	senderAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: s.Conn().LocalAddr().(*net.UDPAddr).Port,
	}
	// Fake-mister sends 3 ACKs; drainer delivers what it can, drops the rest.
	for i := uint32(0); i < 3; i++ {
		pkt := make([]byte, groovy.ACKPacketSize)
		binary.LittleEndian.PutUint32(pkt[0:4], i)
		if _, err := fake.WriteToUDP(pkt, senderAddr); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case ack := <-ch:
		if ack.FrameEcho != 0 {
			t.Errorf("first ack frame = %d", ack.FrameEcho)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no ack received")
	}
}
