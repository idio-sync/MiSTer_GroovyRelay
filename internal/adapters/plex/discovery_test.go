package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDiscovery_RespondsToMSearch exercises the M-SEARCH responder by
// directly sending a unicast UDP datagram to an isolated loopback listener.
// This bypasses multicast (which is flaky in CI / across host firewalls)
// while still covering request parsing, real UDP reply delivery, and the
// response formatting logic in respondToMSearch.
func TestDiscovery_RespondsToMSearch(t *testing.T) {
	cfg := DiscoveryConfig{
		DeviceName: "MiSTer-Test",
		DeviceUUID: "uuid-abc-123",
		HTTPPort:   32500,
		Version:    "9.9.9",
	}

	listen, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		listen.Close()
		t.Fatalf("sender UDP: %v", err)
	}

	d := &Discovery{
		cfg:    cfg,
		listen: listen,
		sender: sender,
		stop:   make(chan struct{}),
	}
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		d.Run()
	}()
	t.Cleanup(func() {
		_ = d.Close()
		<-runDone
	})

	// Send an M-SEARCH from an ephemeral UDP socket directly to the
	// discovery conn's bound address.
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	defer client.Close()

	dst := d.listen.LocalAddr().(*net.UDPAddr)

	if _, err := client.WriteToUDP([]byte("M-SEARCH * HTTP/1.1\r\n\r\n"), dst); err != nil {
		t.Fatalf("write M-SEARCH: %v", err)
	}

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, src, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	senderPort := sender.LocalAddr().(*net.UDPAddr).Port
	if src.Port != senderPort {
		t.Errorf("response source port = %d; want %d", src.Port, senderPort)
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
	if !strings.Contains(resp, "Product: GroovyRelay") {
		t.Errorf("missing product header; got: %q", resp)
	}
	if !strings.Contains(resp, "Version: 9.9.9") {
		t.Errorf("missing version header; got: %q", resp)
	}
	if !strings.Contains(resp, "Content-Type: plex/media-player") {
		t.Errorf("missing plex content-type header; got: %q", resp)
	}
	if !strings.Contains(resp, "Protocol: plex") {
		t.Errorf("missing protocol header; got: %q", resp)
	}
	if !strings.Contains(resp, "Protocol-Version: 2") {
		t.Errorf("missing protocol version header; got: %q", resp)
	}
	if !strings.Contains(resp, "Protocol-Capabilities: timeline,playback,navigation,playqueues,provider-playback") {
		t.Errorf("missing full protocol capabilities header; got: %q", resp)
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
	gotDst, ok := writes[0].dst.(*net.UDPAddr)
	if !ok {
		t.Fatalf("reply dst type = %T; want *net.UDPAddr", writes[0].dst)
	}
	wantDst := client.LocalAddr().(*net.UDPAddr)
	if !gotDst.IP.Equal(wantDst.IP) || gotDst.Port != wantDst.Port {
		t.Errorf("reply dst = %s; want %s", gotDst, wantDst)
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

func TestDiscovery_HelloCarriesDescriptorFields(t *testing.T) {
	fake := &fakeWriter{}
	d := &Discovery{
		cfg: DiscoveryConfig{
			DeviceName: "MiSTer-Test",
			DeviceUUID: "uuid-hello",
			HTTPPort:   32500,
		},
		sender: fake,
	}

	if err := d.sendHello(); err != nil {
		t.Fatalf("sendHello: %v", err)
	}
	writes := fake.snapshot()
	if len(writes) != 1 {
		t.Fatalf("writes = %d; want 1", len(writes))
	}
	body := string(writes[0].body)
	for _, want := range []string{
		"HELLO * HTTP/1.0",
		"Name: MiSTer-Test",
		"Port: 32500",
		"Resource-Identifier: uuid-hello",
		"Content-Type: plex/media-player",
		"Protocol: plex",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("HELLO missing %q; body=%q", want, body)
		}
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

// erroringWriter is a packetWriter that always returns the configured
// error from WriteTo. Used to drive the WARN-logging path.
type erroringWriter struct {
	err error
}

func (e *erroringWriter) WriteTo(_ []byte, _ net.Addr) (int, error) { return 0, e.err }
func (e *erroringWriter) Close() error                              { return nil }

// capturingHandler records every slog Record passed through it. Used
// to assert WARN lines fire on send failure without coupling to the
// production handler's text/JSON shape.
type capturingHandler struct {
	mu      sync.Mutex
	records []capturedRecord
}

type capturedRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

func (h *capturingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	cr := capturedRecord{level: r.Level, msg: r.Message, attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		cr.attrs[a.Key] = fmt.Sprint(a.Value.Any())
		return true
	})
	h.records = append(h.records, cr)
	return nil
}
func (h *capturingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *capturingHandler) hasWarnContaining(msgSubstr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.level == slog.LevelWarn && strings.Contains(r.msg, msgSubstr) {
			return true
		}
	}
	return false
}

// installCapturingSlog swaps the default slog handler for a capturing
// one and restores it via t.Cleanup. Returned handler is the snapshot
// target.
func installCapturingSlog(t *testing.T) *capturingHandler {
	t.Helper()
	cap := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return cap
}

// TestDiscovery_HelloSendFailureLogsWarn pins the WARN-on-send-error
// path for the heartbeat. Triggers sendHello directly with an
// erroringWriter — no goroutine, no socket, no race.
func TestDiscovery_HelloSendFailureLogsWarn(t *testing.T) {
	cap := installCapturingSlog(t)

	d := &Discovery{
		cfg:    DiscoveryConfig{DeviceName: "x", DeviceUUID: "y", HTTPPort: 32500},
		sender: &erroringWriter{err: errors.New("simulated send failure")},
	}
	if err := d.sendHello(); err == nil {
		t.Error("expected sendHello to surface error from sender")
	}
	if !cap.hasWarnContaining("HELLO send failed") {
		t.Error("expected WARN log containing 'HELLO send failed'")
	}
}

// TestDiscovery_ReplySendFailureLogsWarn pins the WARN-on-send-error
// path for the M-SEARCH reply.
func TestDiscovery_ReplySendFailureLogsWarn(t *testing.T) {
	cap := installCapturingSlog(t)

	d := &Discovery{
		cfg:    DiscoveryConfig{DeviceName: "x", DeviceUUID: "y", HTTPPort: 32500},
		sender: &erroringWriter{err: errors.New("simulated reply failure")},
	}
	d.respondToMSearch(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345})
	if !cap.hasWarnContaining("M-SEARCH reply send failed") {
		t.Error("expected WARN log containing 'M-SEARCH reply send failed'")
	}
}

// TestSenderBindFor_EmptyHostIP pins the no-config-set fallback:
// listen joins multicast on the default interface, sender binds to
// GDM port 32412 so replies use the source port PMS expects.
func TestSenderBindFor_EmptyHostIP(t *testing.T) {
	addr, iface := senderBindFor("")
	if addr != ":32412" {
		t.Errorf("addr = %q; want :32412", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}

// TestSenderBindFor_LocalIP pins the multi-interface happy path:
// HostIP resolves to a local interface for multicast egress pinning,
// but the sender still binds to :32412 so unicast M-SEARCH replies can
// use the kernel-selected source address for loopback, Docker bridge,
// and LAN destinations.
func TestSenderBindFor_LocalIP(t *testing.T) {
	addr, iface := senderBindFor("127.0.0.1")
	if iface == nil {
		t.Skip("loopback enumeration unavailable on this host")
	}
	if addr != ":32412" {
		t.Errorf("addr = %q; want :32412", addr)
	}
	if iface.Flags&net.FlagLoopback == 0 {
		t.Errorf("iface = %q (flags %v); want loopback", iface.Name, iface.Flags)
	}
}

// TestSenderBindFor_FallsBackWhenHostIPNotLocal pins the regression
// the prior plan revision missed: a parseable IPv4 address that no
// local interface owns must fall back to (":32412", nil) — still using
// the expected GDM source port, but without blindly binding to a
// non-local IP. Uses TEST-NET-3 (203.0.113.0/24, RFC 5737) which is
// documentation-reserved and never assignable on real hosts.
func TestSenderBindFor_FallsBackWhenHostIPNotLocal(t *testing.T) {
	addr, iface := senderBindFor("203.0.113.99")
	if addr != ":32412" {
		t.Errorf("addr = %q; want :32412 (non-local IP must not be bound)", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}

// TestSenderBindFor_FallsBackOnGarbage pins the typo'd config case:
// non-IP-shaped strings fall back to (":32412", nil).
func TestSenderBindFor_FallsBackOnGarbage(t *testing.T) {
	addr, iface := senderBindFor("not-an-ip")
	if addr != ":32412" {
		t.Errorf("addr = %q; want :32412", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}

// TestSenderBindFor_FallsBackOnIPv6 pins that IPv6 HostIPs (e.g. ::1)
// fall back rather than attempting an IPv6 bind on the udp4 socket.
func TestSenderBindFor_FallsBackOnIPv6(t *testing.T) {
	addr, iface := senderBindFor("::1")
	if addr != ":32412" {
		t.Errorf("addr = %q; want :32412", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}

// TestDiscovery_CloseIdempotent ensures a second Close is a safe no-op.
// Without sync.Once, closing the d.stop channel twice would panic
// "close of closed channel".
func TestDiscovery_CloseIdempotent(t *testing.T) {
	listen, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	d := &Discovery{
		cfg:    DiscoveryConfig{DeviceName: "x", DeviceUUID: "y", HTTPPort: 32500},
		listen: listen,
		sender: &fakeWriter{},
		stop:   make(chan struct{}),
	}

	if err := d.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must not panic and must not return an error.
	if err := d.Close(); err != nil {
		t.Errorf("second Close returned err: %v", err)
	}
}
