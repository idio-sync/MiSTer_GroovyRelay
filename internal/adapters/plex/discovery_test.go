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

// TestInterfaceForIP_FindsLoopback exercises the happy path: 127.0.0.1
// resolves to whichever interface the OS reports as loopback. Skipped on
// systems whose interface enumeration omits the loopback (sandboxed CI,
// some container runtimes) — verifying that case is platform-specific
// and not what this helper is for.
func TestInterfaceForIP_FindsLoopback(t *testing.T) {
	iface, err := interfaceForIP("127.0.0.1")
	if err != nil {
		t.Skipf("loopback enumeration unavailable: %v", err)
	}
	if iface == nil {
		t.Fatal("got nil interface")
	}
	if iface.Flags&net.FlagLoopback == 0 {
		t.Errorf("expected loopback interface, got name=%q flags=%v", iface.Name, iface.Flags)
	}
}

// TestInterfaceForIP_NotFound uses TEST-NET-3 (203.0.113.0/24, RFC 5737),
// which is documentation-reserved and never assignable on real hosts.
func TestInterfaceForIP_NotFound(t *testing.T) {
	if _, err := interfaceForIP("203.0.113.99"); err == nil {
		t.Fatal("expected error for unassigned IP, got nil")
	}
}

// TestInterfaceForIP_RejectsIPv6 keeps the helper IPv4-only since GDM
// is udp4. IPv6-only HostIP must fall back to nil-interface behavior in
// the caller, not be used here.
func TestInterfaceForIP_RejectsIPv6(t *testing.T) {
	if _, err := interfaceForIP("::1"); err == nil {
		t.Fatal("expected error for IPv6 address, got nil")
	}
}

// TestInterfaceForIP_RejectsGarbage rejects strings that aren't IPs.
func TestInterfaceForIP_RejectsGarbage(t *testing.T) {
	if _, err := interfaceForIP("not-an-ip"); err == nil {
		t.Fatal("expected error for invalid input, got nil")
	}
}
