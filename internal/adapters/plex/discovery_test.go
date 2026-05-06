package plex

import (
	"net"
	"strings"
	"sync"
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

	target := d.listen.LocalAddr().(*net.UDPAddr)
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

// fakeWriter is a packetWriter that records every WriteTo call. Used
// in tests to assert HELLO heartbeats and M-SEARCH replies fire as
// expected without binding real sockets or relying on multicast
// delivery (which is flaky on Windows / loopback).
type fakeWriter struct {
	mu     sync.Mutex
	writes []writeRecord
}

type writeRecord struct {
	body []byte
	dst  net.Addr
}

func (f *fakeWriter) WriteTo(b []byte, addr net.Addr) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	f.writes = append(f.writes, writeRecord{body: cp, dst: addr})
	return len(b), nil
}

func (f *fakeWriter) Close() error { return nil }

func (f *fakeWriter) snapshot() []writeRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]writeRecord, len(f.writes))
	copy(out, f.writes)
	return out
}

// TestDiscovery_RepliesViaSenderNotListener proves the M-SEARCH reply
// path uses the sender packetWriter, not the multicast-joined listener.
// On Windows, sending unicast from a multicast-joined socket is
// fragile; this test pins the post-refactor behavior so a future change
// can't silently regress to one-socket.
//
// We construct Discovery directly via its struct fields (same package,
// so unexported fields are accessible) rather than going through
// NewDiscovery. That way the fake sender is wired in BEFORE Run starts —
// no goroutine race, no real multicast bind, no flake on hosts where
// 32412 is held by a real Plex client.
func TestDiscovery_RepliesViaSenderNotListener(t *testing.T) {
	listen, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}

	fake := &fakeWriter{}
	d := &Discovery{
		cfg: DiscoveryConfig{
			DeviceName: "MiSTer-Test",
			DeviceUUID: "uuid-z",
			HTTPPort:   32500,
		},
		listen: listen,
		sender: fake,
	}
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		d.Run()
	}()
	t.Cleanup(func() {
		_ = listen.Close() // unblocks d.Run's ReadFromUDP
		<-runDone          // wait for goroutine exit; no leak
	})

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	defer client.Close()
	target := listen.LocalAddr().(*net.UDPAddr)
	dst := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: target.Port}
	if _, err := client.WriteToUDP([]byte("M-SEARCH * HTTP/1.1\r\n\r\n"), dst); err != nil {
		t.Fatalf("write M-SEARCH: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.snapshot()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	writes := fake.snapshot()
	if len(writes) == 0 {
		t.Fatal("expected a reply via sender, got none")
	}
	if !strings.Contains(string(writes[0].body), "HTTP/1.0 200 OK") {
		t.Errorf("first write should be M-SEARCH reply; body=%q", writes[0].body)
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

// TestDiscovery_HelloHeartbeatRepeats proves HELLO is sent on a ticker,
// not just once at startup. Constructs Discovery directly via struct
// fields and starts only the heartbeat goroutine — no real multicast
// listener bound, no socket-creation race, no flake on hosts where
// 32412 is held by a real Plex client.
func TestDiscovery_HelloHeartbeatRepeats(t *testing.T) {
	// Shorten the heartbeat interval. Restored by t.Cleanup.
	prev := helloInterval
	helloInterval = 50 * time.Millisecond
	t.Cleanup(func() { helloInterval = prev })

	fake := &fakeWriter{}
	d := &Discovery{
		cfg: DiscoveryConfig{
			DeviceName: "MiSTer-Test",
			DeviceUUID: "uuid-h",
			HTTPPort:   32500,
		},
		sender: fake,
		stop:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.runHeartbeat()
	t.Cleanup(func() {
		close(d.stop)
		d.wg.Wait()
	})

	// Wait long enough for ~4 heartbeat ticks plus startup margin.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if countHellos(fake.snapshot()) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := countHellos(fake.snapshot()); got < 3 {
		t.Errorf("expected >=3 HELLO writes, got %d", got)
	}
}

func countHellos(writes []writeRecord) int {
	n := 0
	for _, w := range writes {
		if strings.HasPrefix(string(w.body), "HELLO ") {
			n++
		}
	}
	return n
}

// TestDiscovery_HeartbeatFiresHelloImmediately pins the working
// co-located-Docker discovery latency: the very first HELLO must go
// out without waiting for the ticker. Uses a long ticker interval so
// any HELLO observed during the test window comes from the immediate
// startup send, not a tick.
func TestDiscovery_HeartbeatFiresHelloImmediately(t *testing.T) {
	prev := helloInterval
	helloInterval = 5 * time.Second
	t.Cleanup(func() { helloInterval = prev })

	fake := &fakeWriter{}
	d := &Discovery{
		cfg: DiscoveryConfig{
			DeviceName: "MiSTer-Test",
			DeviceUUID: "uuid-imm",
			HTTPPort:   32500,
		},
		sender: fake,
		stop:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.runHeartbeat()
	t.Cleanup(func() {
		close(d.stop)
		d.wg.Wait()
	})

	// Wait briefly for the goroutine to issue the immediate send.
	// 200ms is well below the 5s ticker, so any HELLO observed here
	// is the startup send, not a tick.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if countHellos(fake.snapshot()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := countHellos(fake.snapshot())
	if got != 1 {
		t.Errorf("expected exactly 1 immediate HELLO (heartbeat ticker should not have fired in 200ms with 5s interval), got %d", got)
	}
}
