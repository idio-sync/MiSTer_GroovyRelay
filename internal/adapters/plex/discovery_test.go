package plex

import (
	"net"
	"strings"
	"testing"
	"time"
)

// TestDiscovery_RespondsToMSearch exercises the M-SEARCH responder by
// directly sending a unicast UDP datagram to the discovery listener's
// bound address. This bypasses multicast (which is flaky in CI / across
// host firewalls) while still covering the request parsing and response
// formatting logic in respondToMSearch.
//
// On Windows the port 32412 may be held by a real Plex Media Server or
// Plex client. If ListenMulticastUDP fails we skip rather than fail the
// suite.
func TestDiscovery_RespondsToMSearch(t *testing.T) {
	cfg := DiscoveryConfig{
		DeviceName: "MiSTer-Test",
		DeviceUUID: "uuid-abc-123",
		HTTPPort:   32500,
	}
	d, err := NewDiscovery(cfg)
	if err != nil {
		t.Skipf("port 32412 busy or multicast unavailable: %v", err)
	}
	defer d.Close()

	go d.Run()

	// Send an M-SEARCH from an ephemeral UDP socket directly to the
	// discovery conn's bound address.
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	defer client.Close()

	target := d.conn.LocalAddr().(*net.UDPAddr)
	// Rewrite 0.0.0.0 -> 127.0.0.1 so Windows will actually deliver the
	// packet back to the bound socket.
	dst := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: target.Port}

	if _, err := client.WriteToUDP([]byte("M-SEARCH * HTTP/1.1\r\n\r\n"), dst); err != nil {
		t.Fatalf("write M-SEARCH: %v", err)
	}

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "HTTP/1.0 200 OK") {
		t.Errorf("missing 200 OK status line; got: %q", resp)
	}
	if !strings.Contains(resp, "Name: MiSTer-Test") {
		t.Errorf("missing device name header; got: %q", resp)
	}
	if !strings.Contains(resp, "Port: 32500") {
		t.Errorf("missing http port header; got: %q", resp)
	}
	if !strings.Contains(resp, "Resource-Identifier: uuid-abc-123") {
		t.Errorf("missing uuid header; got: %q", resp)
	}
	if !strings.Contains(resp, "Content-Type: plex/media-player") {
		t.Errorf("missing plex content-type header; got: %q", resp)
	}
	if !strings.Contains(resp, "Protocol: plex") {
		t.Errorf("missing protocol header; got: %q", resp)
	}
}
