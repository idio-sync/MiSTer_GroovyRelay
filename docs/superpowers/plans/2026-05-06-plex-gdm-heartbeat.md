# Plex GDM heartbeat + interface-aware discovery — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Bishop's PMS reliably discover the bridge cross-host so it lands in `/clients` and Plex Web's cast picker surfaces it via the PMS-proxy path. Achieved by replacing the single multicast-joined socket with a listen+sender pair, broadcasting HELLO on a 30 s heartbeat instead of once at startup, surfacing send errors at WARN, and pinning the multicast egress interface via `golang.org/x/net/ipv4.SetMulticastInterface` when `bridge.host_ip` resolves to a known interface.

**Architecture:** `Discovery` gains a `packetWriter`-typed sender field separate from its multicast-joined `*net.UDPConn` listener. A goroutine sends HELLO every `helloInterval` (package var, 30 s default). A new `interfaceForIP` helper plus `DiscoveryConfig.HostIP` make multi-NIC selection deterministic. Two unexported test seams — the `packetWriter` interface and a package-level `newDiscovery` constructor var — let tests assert HELLO/reply behavior and adapter threading without binding real multicast sockets or relying on flaky loopback delivery.

**Tech Stack:** Go 1.26.2, `net`, `log/slog`, `golang.org/x/net/ipv4` (promoted from transitive to direct require — already at v0.52.0 in the module graph), `sync` (`sync.Once` for idempotent close).

**Spec:** [docs/superpowers/specs/2026-05-06-plex-gdm-heartbeat-design.md](../specs/2026-05-06-plex-gdm-heartbeat-design.md)

---

## Files

**Create:** none.

**Modify:**
- `internal/adapters/plex/discovery.go` — primary change.
- `internal/adapters/plex/discovery_test.go` — one existing test edited (rename `d.conn`→`d.listen`); five new tests added.
- `internal/adapters/plex/adapter.go` — call through `newDiscovery` seam, pass `HostIP`.
- `internal/adapters/plex/adapter_interface_test.go` — append `TestAdapter_StartPassesHostIPToDiscovery` (existing test file, no new file).
- `go.mod` / `go.sum` — promote `golang.org/x/net` to direct require.

---

## Conventions for this plan

- **Working directory:** `c:\Users\Jake\Git\MiSTer_GroovyRelay`. All paths below are relative.
- **Shell:** PowerShell (commands shown PowerShell-friendly; Bash also works).
- **Test invocation:** `go test ./internal/adapters/plex/...` from repo root. Single test via `-run TestName`. Race detector with `-race`.
- **Existing test pattern:** `discovery_test.go` skips with `t.Skipf` if `NewDiscovery` fails (port held by real PMS / multicast unavailable). New tests follow the same `t.Skipf` pattern when they actually open multicast sockets.
- **Commit style:** conventional-commits-ish (`feat`, `fix`, `refactor`, `test`, `docs`). Recent examples in this repo: `feat(plex): ...`, `fix(plex): ...`, `refactor(plex): ...`. Subject under 70 chars. Wrap commit body at ~72 chars. Add `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` trailer when appropriate (matches recent commit pattern).
- **No emojis in source files.**
- **`replace_all` in Edit:** don't use it; do targeted single edits.
- **Structural refactor + behavior change in same task:** avoid. Each task adds either structure or behavior, not both.

---

## Task 1: Add `interfaceForIP` helper

**Files:**
- Modify: `internal/adapters/plex/discovery.go`
- Modify: `internal/adapters/plex/discovery_test.go`

A side-effect-free helper that returns the network interface owning a given IPv4 address. Read-only — enumerates interfaces and matches addresses. Used by `NewDiscovery` later (Task 6) to pin the multicast egress interface.

- [ ] **Step 1: Write failing tests**

Append to `internal/adapters/plex/discovery_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go test ./internal/adapters/plex/... -run TestInterfaceForIP -v
```

Expected: `undefined: interfaceForIP` build error or test failure.

- [ ] **Step 3: Implement the helper**

Append to `internal/adapters/plex/discovery.go` (above `// Close releases ...`):

```go
// interfaceForIP returns the network interface that owns the given IPv4
// address. Side-effect free — reads system interface state via
// net.Interfaces but does not mutate it and binds no sockets. Returns
// an error for non-IPv4 input or when no interface owns the address.
//
// Used by NewDiscovery to make multicast egress deterministic on
// multi-NIC hosts. GDM is udp4-only, so non-IPv4 input is a
// configuration mistake worth surfacing rather than silently ignoring.
func interfaceForIP(hostIP string) (*net.Interface, error) {
	target := net.ParseIP(hostIP)
	if target == nil {
		return nil, fmt.Errorf("interfaceForIP: invalid IP %q", hostIP)
	}
	target = target.To4()
	if target == nil {
		return nil, fmt.Errorf("interfaceForIP: %q is not IPv4", hostIP)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("interfaceForIP: enumerate interfaces: %w", err)
	}
	for i := range ifaces {
		addrs, err := ifaces[i].Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if ip.To4() != nil && ip.Equal(target) {
				return &ifaces[i], nil
			}
		}
	}
	return nil, fmt.Errorf("interfaceForIP: no interface owns %s", hostIP)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```powershell
go test ./internal/adapters/plex/... -run TestInterfaceForIP -v
```

Expected: 4 PASS (or `TestInterfaceForIP_FindsLoopback` SKIP if your env can't enumerate loopback, plus 3 PASS).

- [ ] **Step 5: Commit**

```powershell
git add internal/adapters/plex/discovery.go internal/adapters/plex/discovery_test.go
git commit -m "feat(plex): add interfaceForIP helper for GDM iface selection"
```

---

## Task 2: Refactor `Discovery` to listen + sender via `packetWriter`

**Files:**
- Modify: `internal/adapters/plex/discovery.go`
- Modify: `internal/adapters/plex/discovery_test.go`

Pure structural refactor: split the single multicast-joined socket into a `listen` socket (read-only) and a `sender` field typed as a `packetWriter` interface (real impl: a separate `*net.UDPConn` from `net.ListenPacket("udp4", ":0")`). The `packetWriter` interface is the test seam that later tasks lean on. Existing behavior — single startup HELLO, reply on M-SEARCH, error swallow — is preserved here. No new functionality.

The existing `TestDiscovery_RespondsToMSearch` reads `d.conn.LocalAddr()` directly to find the listener address; it must update to `d.listen.LocalAddr()`.

- [ ] **Step 1: Write the failing test**

Add this new test to `internal/adapters/plex/discovery_test.go` (above `TestInterfaceForIP_*` if you wish, or below — order doesn't matter):

```go
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
		<-runDone           // wait for goroutine exit; no leak
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
```

Also update the existing test reference in the same file:

```go
// In TestDiscovery_RespondsToMSearch, change:
target := d.conn.LocalAddr().(*net.UDPAddr)
// to:
target := d.listen.LocalAddr().(*net.UDPAddr)
```

You'll also need to add `"sync"` to the imports of `discovery_test.go` if not already present.

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery -v
```

Expected: build errors (`d.conn undefined`, `d.sender undefined`, `d.listen undefined`).

- [ ] **Step 3: Refactor `discovery.go`**

Replace the entire body of `internal/adapters/plex/discovery.go` (keeping the package and existing imports plus a new `sync` import — note `sync` is needed for Task 7's `Close` idempotency, but since this task touches the struct definition, declaring it now and carrying the empty `sync.Once` field forward is cheaper than re-shaping later):

```go
package plex

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
)

// GDM (Good Day Mate) is Plex's LAN discovery protocol. The bridge joins
// multicast group 239.0.0.250 on UDP/32412 to listen for M-SEARCH queries
// from Plex controllers, replies with a unicast HTTP-like descriptor, and
// broadcasts an unsolicited HELLO advertisement on 32413 on a heartbeat
// (see Task 3 — added below in this plan).
//
// Spec: docs/superpowers/specs/2026-05-06-plex-gdm-heartbeat-design.md.

// DiscoveryConfig is the minimal set of fields the responder splices into
// the M-SEARCH reply. DeviceName is user-facing (appears in the Plex cast
// picker); DeviceUUID must be stable across restarts so controllers dedupe
// correctly; HTTPPort is the Companion server's TCP port.
type DiscoveryConfig struct {
	DeviceName string
	DeviceUUID string
	HTTPPort   int
}

// packetWriter is the small interface Discovery uses for outbound UDP. In
// production it's a *net.UDPConn from net.ListenPacket; tests substitute
// a counting fake to assert HELLO heartbeats and M-SEARCH replies without
// binding real sockets or relying on flaky loopback multicast delivery.
type packetWriter interface {
	WriteTo(b []byte, addr net.Addr) (int, error)
	Close() error
}

// Discovery owns the GDM listen socket and a separate outbound sender.
// Splitting the two avoids a Windows quirk where sending unicast from a
// multicast-joined socket can fail or use an unexpected source IP.
type Discovery struct {
	cfg       DiscoveryConfig
	listen    *net.UDPConn
	sender    packetWriter
	closeOnce sync.Once
	closeErr  error
}

// NewDiscovery joins the GDM multicast group and immediately broadcasts a
// HELLO announcement. Callers are expected to invoke Run in a goroutine
// and Close on shutdown.
func NewDiscovery(cfg DiscoveryConfig) (*Discovery, error) {
	group := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32412}
	listen, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, err
	}
	sender, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		listen.Close()
		return nil, fmt.Errorf("plex GDM: bind sender: %w", err)
	}
	d := &Discovery{cfg: cfg, listen: listen, sender: sender.(*net.UDPConn)}
	if err := d.sendHello(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// sendHello announces our presence by writing a HELLO datagram to the GDM
// advertisement port (32413, distinct from the listen group port 32412).
func (d *Discovery) sendHello() error {
	dst := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32413}
	_, err := d.sender.WriteTo([]byte("HELLO * HTTP/1.0\r\n\r\n"), dst)
	return err
}

// Run reads datagrams until the listen socket is closed and responds to
// each M-SEARCH with a unicast descriptor targeted at the source address.
func (d *Discovery) Run() {
	buf := make([]byte, 4096)
	for {
		n, src, err := d.listen.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := string(buf[:n])
		if strings.HasPrefix(req, "M-SEARCH") {
			slog.Debug("plex GDM M-SEARCH received",
				"src", src.String(),
				"reply_uuid", d.cfg.DeviceUUID,
				"reply_name", d.cfg.DeviceName,
				"reply_port", d.cfg.HTTPPort,
			)
			d.respondToMSearch(src)
		}
	}
}

// respondToMSearch sends the GDM descriptor fields Plex controllers look
// for when populating the cast target list.
func (d *Discovery) respondToMSearch(dst *net.UDPAddr) {
	body := fmt.Sprintf("HTTP/1.0 200 OK\r\n"+
		"Name: %s\r\n"+
		"Port: %d\r\n"+
		"Resource-Identifier: %s\r\n"+
		"Product: MiSTer_GroovyRelay\r\n"+
		"Version: 1.0\r\n"+
		"Content-Type: plex/media-player\r\n"+
		"Protocol: plex\r\n"+
		"Protocol-Capabilities: timeline,playback,playqueues\r\n"+
		"Device-Class: stb\r\n"+
		"Protocol-Version: 1\r\n\r\n",
		d.cfg.DeviceName, d.cfg.HTTPPort, d.cfg.DeviceUUID)
	_, _ = d.sender.WriteTo([]byte(body), dst)
}

// Close releases the listen and sender sockets; Run will return shortly
// after the listen socket closes. Idempotency lands in a later task.
func (d *Discovery) Close() error {
	listenErr := d.listen.Close()
	senderErr := d.sender.Close()
	if listenErr != nil {
		return listenErr
	}
	return senderErr
}

// interfaceForIP from Task 1 stays unchanged below this point.
```

(Important: keep the `interfaceForIP` function from Task 1 intact at the bottom of the file — the snippet above doesn't show it but you must not delete it. Don't replace the file wholesale; modify the parts that changed.)

- [ ] **Step 4: Run tests to verify they pass**

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery -v
```

Expected: `TestDiscovery_RespondsToMSearch` PASS, `TestDiscovery_RepliesViaSenderNotListener` PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/adapters/plex/discovery.go internal/adapters/plex/discovery_test.go
git commit -m "refactor(plex): split GDM socket into listen + sender packetWriter"
```

---

## Task 3: Periodic HELLO heartbeat

**Files:**
- Modify: `internal/adapters/plex/discovery.go`
- Modify: `internal/adapters/plex/discovery_test.go`

Add a goroutine that fires `sendHello` immediately (preserving the today behavior so co-located Docker users don't regress) and then every `helloInterval` until `Close` signals stop. `helloInterval` is a package-level `var` defaulting to 30 s, mirroring `pollInterval`/`registerInterval` in [linking.go](../../internal/adapters/plex/linking.go); tests shorten it.

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/plex/discovery_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go test ./internal/adapters/plex/... -run "TestDiscovery_(HelloHeartbeatRepeats|HeartbeatFiresHelloImmediately)" -v
```

Expected: build error (`undefined: helloInterval`, `undefined: runHeartbeat`, or `undefined: d.stop` / `d.wg`) — the heartbeat goroutine and its supporting fields don't exist yet.

- [ ] **Step 3: Add the heartbeat goroutine**

In `internal/adapters/plex/discovery.go`, add the package var and modify `NewDiscovery` plus `Close`:

Add near the top, after the `DiscoveryConfig` struct:

```go
// helloInterval is the cadence at which Discovery rebroadcasts the
// HELLO multicast advertisement. Plex's GDM presence TTLs are typically
// 60-120 s, so 30 s gives PMS multiple recovery chances if a packet
// drops or PMS started after the bridge. Exposed as a var so tests
// shorten it to ~50 ms; matches the var-based-knob pattern used by
// pollInterval and registerInterval in linking.go.
var helloInterval = 30 * time.Second
```

You'll also need to add `"time"` to the imports.

Add a `done` channel and `wg` to `Discovery`:

```go
type Discovery struct {
	cfg       DiscoveryConfig
	listen    *net.UDPConn
	sender    packetWriter
	closeOnce sync.Once
	closeErr  error
	stop      chan struct{}
	wg        sync.WaitGroup
}
```

Modify `NewDiscovery` to initialize `stop` and launch the heartbeat goroutine. **Crucially**, the immediate HELLO send moves *into* `runHeartbeat` (fired before entering the ticker `select` loop) rather than being a separate pre-goroutine call in `NewDiscovery`. Two reasons:

1. The "fire immediately" behavior is now exclusively the goroutine's responsibility — direct-construction tests can pin both the immediate send AND the ticked sends through the same fake sender, with no seam needed for `NewDiscovery` itself.
2. `NewDiscovery`'s startup sequence becomes simpler: just bind sockets, build the struct, launch the goroutine, return. Send-side failure during startup can never abort `NewDiscovery` because there *is* no startup send call from `NewDiscovery` to fail.

```go
func NewDiscovery(cfg DiscoveryConfig) (*Discovery, error) {
	group := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32412}
	listen, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, err
	}
	sender, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		listen.Close()
		return nil, fmt.Errorf("plex GDM: bind sender: %w", err)
	}

	d := &Discovery{
		cfg:    cfg,
		listen: listen,
		sender: sender.(*net.UDPConn),
		stop:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.runHeartbeat()
	return d, nil
}

// runHeartbeat fires HELLO immediately on entry (preserving the
// working co-located-Docker discovery latency) and then re-broadcasts
// every helloInterval until Close signals stop. Send errors propagate
// through sendHello's WARN logging (Task 4 below).
func (d *Discovery) runHeartbeat() {
	defer d.wg.Done()
	_ = d.sendHello()
	t := time.NewTicker(helloInterval)
	defer t.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-t.C:
			_ = d.sendHello()
		}
	}
}
```

(Task 5 will replace the `nil`/`":0"` literals here with `senderBindFor(cfg.HostIP)` outputs. Until then, this commit's `NewDiscovery` retains today's nil-interface-and-ephemeral-port behavior — it's a TDD-clean intermediate step.)

Modify `Close` to signal stop and wait for the heartbeat goroutine:

```go
func (d *Discovery) Close() error {
	close(d.stop)
	d.wg.Wait()
	listenErr := d.listen.Close()
	senderErr := d.sender.Close()
	if listenErr != nil {
		return listenErr
	}
	return senderErr
}
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery_HelloHeartbeatRepeats -v
```

Expected: PASS.

Run the whole `TestDiscovery_*` suite to confirm no regressions:

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery -v
```

All PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/adapters/plex/discovery.go internal/adapters/plex/discovery_test.go
git commit -m "feat(plex): broadcast GDM HELLO on a 30s heartbeat"
```

---

## Task 4: Surface send errors as WARN logs

**Files:**
- Modify: `internal/adapters/plex/discovery.go`
- Modify: `internal/adapters/plex/discovery_test.go`

Both `sendHello` and `respondToMSearch` currently discard `WriteTo` errors. Replace the silent drops with `slog.Warn` lines so an operator can diagnose a broken LAN segment from logs alone. Pin the WARN behavior with a deterministic `slog`-capture test so a future cleanup can't silently revert it.

- [ ] **Step 1: Write the failing tests**

Append to `internal/adapters/plex/discovery_test.go`:

```go
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
```

You'll need to add `"context"`, `"errors"`, `"fmt"`, and `"log/slog"` to the test file imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go test ./internal/adapters/plex/... -run "TestDiscovery_(Hello|Reply)SendFailureLogsWarn" -v
```

Expected: tests fail (no WARN log because the production code currently swallows errors silently).

- [ ] **Step 3: Update `sendHello`**

In `internal/adapters/plex/discovery.go`, replace the body of `sendHello`:

```go
func (d *Discovery) sendHello() error {
	dst := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32413}
	if _, err := d.sender.WriteTo([]byte("HELLO * HTTP/1.0\r\n\r\n"), dst); err != nil {
		slog.Warn("plex GDM HELLO send failed",
			"dst", dst.String(),
			"err", err,
		)
		return err
	}
	return nil
}
```

- [ ] **Step 4: Update `respondToMSearch`**

Replace the trailing write line (currently `_, _ = d.sender.WriteTo([]byte(body), dst)`) with:

```go
	if _, err := d.sender.WriteTo([]byte(body), dst); err != nil {
		slog.Warn("plex GDM M-SEARCH reply send failed",
			"dst", dst.String(),
			"err", err,
		)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

```powershell
go test ./internal/adapters/plex/... -run "TestDiscovery_(Hello|Reply)SendFailureLogsWarn" -v
```

Expected: PASS.

Run the whole `TestDiscovery_*` suite to confirm no regressions:

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery -v
```

All PASS. The benign `fakeWriter` returns `nil` from `WriteTo` so non-error tests don't fire WARN lines and are unaffected.

- [ ] **Step 6: Commit**

```powershell
git add internal/adapters/plex/discovery.go internal/adapters/plex/discovery_test.go
git commit -m "feat(plex): surface GDM send errors as WARN logs"
```

---

## Task 5: `HostIP` field + interface-aware listen + sender bind

**Files:**
- Modify: `internal/adapters/plex/discovery.go`
- Modify: `internal/adapters/plex/discovery_test.go`

Add `HostIP` to `DiscoveryConfig`. Add a side-effect-free helper `senderBindFor(hostIP) (addr, iface)` that decides the sender's bind address and (when applicable) the local interface used for multicast send/receive. `NewDiscovery` calls the helper once and uses its outputs for **both** the multicast listen-side join and the sender bind. The helper is the testing focus: it lets us pin every fallback case as pure-function unit tests (no real sockets, no platform skips).

**Three cases the helper covers:**
1. `HostIP` empty → `(":0", nil)`. Listen joins multicast on `nil` interface; sender binds to ephemeral on default interface.
2. `HostIP` set AND resolves to a local interface → `(hostIP+":0", iface)`. Listen joins multicast on that interface; sender binds to that IP.
3. `HostIP` set but is invalid IP, IPv6, or not assigned to any local interface → `(":0", nil)` with a WARN log. **This is the case that previously broke GDM**: parseable-but-non-local `HostIP` (typo'd config, stale IP) would have failed `net.ListenPacket("udp4", "1.2.3.4:0")` with `bind: cannot assign requested address`, returning an error from `NewDiscovery` and disabling GDM entirely. The helper's fallback prevents that.

- [ ] **Step 1: Write the failing tests**

These tests exercise the `senderBindFor` helper directly — pure functions, no real sockets, no platform skips. The three cases match the helper's contract above.

Append to `internal/adapters/plex/discovery_test.go`:

```go
// TestSenderBindFor_EmptyHostIP pins the no-config-set fallback:
// listen joins multicast on the default interface, sender binds to
// ephemeral.
func TestSenderBindFor_EmptyHostIP(t *testing.T) {
	addr, iface := senderBindFor("")
	if addr != ":0" {
		t.Errorf("addr = %q; want :0", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}

// TestSenderBindFor_LocalIP pins the happy path: HostIP resolves to a
// local interface, sender binds to it. Uses 127.0.0.1 since every
// test environment has loopback.
func TestSenderBindFor_LocalIP(t *testing.T) {
	addr, iface := senderBindFor("127.0.0.1")
	if iface == nil {
		t.Skip("loopback enumeration unavailable on this host")
	}
	if addr != "127.0.0.1:0" {
		t.Errorf("addr = %q; want 127.0.0.1:0", addr)
	}
	if iface.Flags&net.FlagLoopback == 0 {
		t.Errorf("iface = %q (flags %v); want loopback", iface.Name, iface.Flags)
	}
}

// TestSenderBindFor_FallsBackWhenHostIPNotLocal pins the regression
// the prior plan revision missed: a parseable IPv4 address that no
// local interface owns must fall back to (":0", nil) — never blindly
// bind to a non-local IP, which would fail net.ListenPacket and
// disable GDM. Uses TEST-NET-3 (203.0.113.0/24, RFC 5737) which is
// documentation-reserved and never assignable on real hosts.
func TestSenderBindFor_FallsBackWhenHostIPNotLocal(t *testing.T) {
	addr, iface := senderBindFor("203.0.113.99")
	if addr != ":0" {
		t.Errorf("addr = %q; want :0 (non-local IP must not be bound)", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}

// TestSenderBindFor_FallsBackOnGarbage pins the typo'd config case:
// non-IP-shaped strings fall back to (":0", nil).
func TestSenderBindFor_FallsBackOnGarbage(t *testing.T) {
	addr, iface := senderBindFor("not-an-ip")
	if addr != ":0" {
		t.Errorf("addr = %q; want :0", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}

// TestSenderBindFor_FallsBackOnIPv6 pins that IPv6 HostIPs (e.g. ::1)
// fall back rather than attempting an IPv6 bind on the udp4 socket.
func TestSenderBindFor_FallsBackOnIPv6(t *testing.T) {
	addr, iface := senderBindFor("::1")
	if addr != ":0" {
		t.Errorf("addr = %q; want :0", addr)
	}
	if iface != nil {
		t.Errorf("iface = %v; want nil", iface)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go test ./internal/adapters/plex/... -run TestSenderBindFor -v
```

Expected: build error (`undefined: senderBindFor`).

- [ ] **Step 3: Add the field, the helper, and use both**

In `internal/adapters/plex/discovery.go`, update `DiscoveryConfig`:

```go
type DiscoveryConfig struct {
	DeviceName string
	DeviceUUID string
	HTTPPort   int
	// HostIP is the configured-or-autodetected LAN IPv4 address the
	// bridge advertises as its connection URI (mirror of
	// AdapterConfig.HostIP from the calling adapter). When non-empty
	// AND it resolves via interfaceForIP to a local interface, that
	// interface is used for the multicast listen-side join and the
	// sender binds to HostIP:0 for a deterministic source IP. When
	// HostIP is empty, isn't IPv4, or doesn't match any local
	// interface, Discovery falls back to nil-interface multicast and
	// :0 sender bind (today's default behavior).
	HostIP string
}
```

Add the helper near the top of the file (just below `interfaceForIP` from Task 1 is a natural spot):

```go
// senderBindFor decides the sender's bind address and outgoing
// interface based on HostIP. Returning ("",  nil) is impossible —
// the helper always returns a usable bind string, falling back to
// ":0" + nil whenever HostIP is empty, malformed, IPv6, or doesn't
// match any local interface. The fallback is what keeps GDM running
// in the face of a typo'd or stale bridge.host_ip; binding directly
// to a non-local IP would fail net.ListenPacket and disable
// discovery entirely.
func senderBindFor(hostIP string) (addr string, iface *net.Interface) {
	if hostIP == "" {
		return ":0", nil
	}
	found, err := interfaceForIP(hostIP)
	if err != nil {
		slog.Warn("plex GDM: HostIP not on a local interface; using default route for multicast and unicast",
			"host_ip", hostIP,
			"err", err,
		)
		return ":0", nil
	}
	return hostIP + ":0", found
}
```

Replace the entire socket-creation block in `NewDiscovery`. Find the current sequence (lines that create `listen` and `sender`) and replace it with:

```go
	senderAddr, iface := senderBindFor(cfg.HostIP)

	group := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32412}
	listen, err := net.ListenMulticastUDP("udp4", iface, group)
	if err != nil {
		return nil, err
	}

	sender, err := net.ListenPacket("udp4", senderAddr)
	if err != nil {
		listen.Close()
		return nil, fmt.Errorf("plex GDM: bind sender: %w", err)
	}
```

(The rest of `NewDiscovery` — the `Discovery` struct literal, the best-effort `sendHello`, the heartbeat goroutine — stays as in Task 3. The `iface` local is reused by Task 6's `SetMulticastInterface` call.)

- [ ] **Step 4: Run tests**

```powershell
go test ./internal/adapters/plex/... -run "(TestSenderBindFor|TestDiscovery)" -v
```

Expected: all PASS, including the five new helper tests.

- [ ] **Step 5: Commit**

```powershell
git add internal/adapters/plex/discovery.go internal/adapters/plex/discovery_test.go
git commit -m "feat(plex): interface-aware GDM listen + sender via HostIP"
```

---

## Task 6: Pin multicast egress interface via `golang.org/x/net/ipv4`

**Files:**
- Modify: `go.mod`
- Modify: `internal/adapters/plex/discovery.go`

Bind alone doesn't constrain *multicast* egress; the kernel picks based on its routing table (and on Windows specifically, governed by `IP_MULTICAST_IF`). When `iface` resolves in Task 5, call `ipv4.NewPacketConn(sender).SetMulticastInterface(iface)` to make HELLO egress deterministic on multi-NIC hosts. Best-effort — failure logs WARN and continues.

`golang.org/x/net` is already in the module graph at v0.52.0 as a transitive dependency; we promote it to a direct require by editing `go.mod` directly (no `go get` — that contacts the network and may upgrade the version).

No dedicated unit test — testing this requires a multi-NIC fixture or platform-specific mocking the stdlib doesn't expose. Code change is short and the log line covers verification at runtime.

- [ ] **Step 1: Promote `golang.org/x/net` to a direct require**

`golang.org/x/net` is currently a transitive dep of `golang.org/x/crypto` and `github.com/coder/websocket` — present in `go.sum` but not as a direct `require` in `go.mod`. Promote it with an explicit version pin (no `go get`, which would touch the network and may upgrade):

```powershell
go mod edit -require=golang.org/x/net@v0.52.0
go mod tidy
```

(Bash is identical — same command.)

If `v0.52.0` is no longer the resolved transitive version (rare; check `go.sum` for the highest-numbered `golang.org/x/net` line to find what's actually been verified locally), substitute that version in the command above. `go mod tidy` will fail loudly if the chosen version isn't already in `go.sum` *and* the build environment lacks network access — in that case, run `go get golang.org/x/net@v0.52.0` once with network access to populate the module cache, then proceed.

Confirm:

```powershell
go list -m golang.org/x/net
```

Expected output: `golang.org/x/net v0.52.0` and the `go.mod` `require` line for `golang.org/x/net` no longer carries `// indirect`.

- [ ] **Step 2: Add the multicast-interface pin**

In `internal/adapters/plex/discovery.go`, add to imports:

```go
	"golang.org/x/net/ipv4"
```

After the sender is created in `NewDiscovery` and before the `Discovery` struct literal is constructed, add:

```go
	// Pin multicast egress to the interface from Task 5's iface lookup
	// so HELLOs don't drift to whichever interface the kernel happens
	// to pick (matters on multi-NIC Windows hosts where
	// IP_MULTICAST_IF governs outgoing interface separately from the
	// bind address). Best-effort: failure logs WARN and the kernel
	// default takes over. The iface==nil case is already covered by
	// Task 5's WARN, so no log here.
	if iface != nil {
		if err := ipv4.NewPacketConn(sender.(*net.UDPConn)).SetMulticastInterface(iface); err != nil {
			slog.Warn("plex GDM: SetMulticastInterface failed; falling back to kernel default",
				"iface", iface.Name,
				"err", err,
			)
		}
	}
```

- [ ] **Step 3: Run tests**

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery -v
```

Expected: all PASS. No new tests in this task; existing ones still pass since the new code path is best-effort and logs only.

Also run `go vet` and a build to catch any import issues:

```powershell
go vet ./internal/adapters/plex/...
go build ./...
```

Both clean.

- [ ] **Step 4: Commit**

```powershell
git add go.mod go.sum internal/adapters/plex/discovery.go
git commit -m "feat(plex): pin GDM multicast egress interface via x/net/ipv4"
```

---

## Task 7: Idempotent `Close`

**Files:**
- Modify: `internal/adapters/plex/discovery.go`
- Modify: `internal/adapters/plex/discovery_test.go`

Wrap the close sequence in `sync.Once` so a second `Close` is a safe no-op. This avoids `close(d.stop)`-on-already-closed-channel panics if `Adapter.Stop` and a deferred `Close` in tests both fire.

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/plex/discovery_test.go`:

```go
// TestDiscovery_CloseIdempotent ensures a second Close is a safe no-op.
// Without sync.Once, closing the d.stop channel twice would panic
// "close of closed channel".
func TestDiscovery_CloseIdempotent(t *testing.T) {
	cfg := DiscoveryConfig{DeviceName: "x", DeviceUUID: "y", HTTPPort: 32500}
	d, err := NewDiscovery(cfg)
	if err != nil {
		t.Skipf("port 32412 busy or multicast unavailable: %v", err)
	}

	if err := d.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must not panic and must not return an error.
	if err := d.Close(); err != nil {
		t.Errorf("second Close returned err: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery_CloseIdempotent -v
```

Expected: PANIC ("close of closed channel") or test failure.

- [ ] **Step 3: Wrap close in `sync.Once`**

In `internal/adapters/plex/discovery.go`, replace `Close`:

```go
func (d *Discovery) Close() error {
	d.closeOnce.Do(func() {
		close(d.stop)
		d.wg.Wait()
		listenErr := d.listen.Close()
		senderErr := d.sender.Close()
		if listenErr != nil {
			d.closeErr = listenErr
			return
		}
		d.closeErr = senderErr
	})
	return d.closeErr
}
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/adapters/plex/... -run TestDiscovery_CloseIdempotent -v
```

Expected: PASS.

Run all tests to confirm no regressions:

```powershell
go test ./internal/adapters/plex/... -v
```

All PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/adapters/plex/discovery.go internal/adapters/plex/discovery_test.go
git commit -m "fix(plex): make Discovery.Close idempotent via sync.Once"
```

---

## Task 8: Thread `HostIP` through `Adapter.Start` via `newDiscovery` seam

**Files:**
- Modify: `internal/adapters/plex/discovery.go`
- Modify: `internal/adapters/plex/adapter.go`
- Modify: `internal/adapters/plex/adapter_interface_test.go`

Add a package-level `var newDiscovery = NewDiscovery` so the adapter test can rebind it to a fake constructor, capture the `DiscoveryConfig`, and assert that `HostIP` is threaded through correctly. Modify `Adapter.Start` to call through this var and pass `a.cfg.HostIP`.

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/plex/adapter_interface_test.go`:

```go
// fakeCore is a minimal SessionManager stub so Adapter.Start can run
// to completion without spinning up real session machinery. The
// adapter's Start method doesn't call any of these on the happy
// startup path — they're only invoked at runtime when a Plex
// controller issues a cast command, which the test doesn't exercise.
type fakeCore struct{}

func (fakeCore) StartSession(core.SessionRequest) error { return nil }
func (fakeCore) Pause() error                           { return nil }
func (fakeCore) Play() error                            { return nil }
func (fakeCore) Stop() error                            { return nil }
func (fakeCore) SeekTo(int) error                       { return nil }
func (fakeCore) Status() core.SessionStatus             { return core.SessionStatus{} }
func (fakeCore) DropActiveCast(string) error            { return nil }

// TestAdapter_StartPassesHostIPToDiscovery pins that the adapter
// threads its configured HostIP through to DiscoveryConfig. Uses the
// package-level newDiscovery seam so we don't bind real multicast
// sockets.
//
// The fake constructor returns (nil, error) rather than a partial
// Discovery. Adapter.Start treats discovery as best-effort: on error
// it logs WARN and skips launching the Run goroutine. Returning a
// nil-listen Discovery would crash Run() on its first ReadFromUDP.
func TestAdapter_StartPassesHostIPToDiscovery(t *testing.T) {
	var captured DiscoveryConfig
	prev := newDiscovery
	newDiscovery = func(cfg DiscoveryConfig) (*Discovery, error) {
		captured = cfg
		return nil, errors.New("test fake: discovery disabled")
	}
	t.Cleanup(func() { newDiscovery = prev })

	a, err := NewAdapter(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: t.TempDir(),
			UI:      config.UIConfig{HTTPPort: 32500},
		},
		Core:       fakeCore{},
		TokenStore: &StoredData{DeviceUUID: "uuid-thread"},
		HostIP:     "10.42.42.42",
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.plexCfg = DefaultConfig()
	a.plexCfg.Enabled = true
	a.plexCfg.DeviceName = "Probe"

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	if captured.HostIP != "10.42.42.42" {
		t.Errorf("DiscoveryConfig.HostIP = %q; want 10.42.42.42", captured.HostIP)
	}
	if captured.DeviceName != "Probe" {
		t.Errorf("DiscoveryConfig.DeviceName = %q; want Probe", captured.DeviceName)
	}
	if captured.DeviceUUID != "uuid-thread" {
		t.Errorf("DiscoveryConfig.DeviceUUID = %q; want uuid-thread", captured.DeviceUUID)
	}
}
```

You'll also need these imports in `adapter_interface_test.go` if not already present:

```go
"context"
"errors"

"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/adapters/plex/... -run TestAdapter_StartPassesHostIPToDiscovery -v
```

Expected: build error (`undefined: newDiscovery`) or test failure (`captured.HostIP = ""; want 10.42.42.42`).

- [ ] **Step 3: Add the seam in `discovery.go`**

In `internal/adapters/plex/discovery.go`, add this at the top of the file (under the `package plex` line and the imports):

```go
// newDiscovery is the package-level seam Adapter.Start calls instead
// of NewDiscovery directly. Rebound by tests to a fake constructor
// that captures the DiscoveryConfig without binding real multicast
// sockets.
var newDiscovery = NewDiscovery
```

- [ ] **Step 4: Update `Adapter.Start` to thread `HostIP` and call through the seam**

In `internal/adapters/plex/adapter.go`, find the existing `disco, err := NewDiscovery(DiscoveryConfig{...})` block (search for `NewDiscovery(DiscoveryConfig`):

```go
	disco, err := NewDiscovery(DiscoveryConfig{
		DeviceName: cfgSnap.DeviceName,
		DeviceUUID: deviceUUID,
		HTTPPort:   a.cfg.Bridge.UI.HTTPPort,
	})
```

Replace with:

```go
	disco, err := newDiscovery(DiscoveryConfig{
		DeviceName: cfgSnap.DeviceName,
		DeviceUUID: deviceUUID,
		HTTPPort:   a.cfg.Bridge.UI.HTTPPort,
		HostIP:     a.cfg.HostIP,
	})
```

- [ ] **Step 5: Run test to verify it passes**

```powershell
go test ./internal/adapters/plex/... -run TestAdapter_StartPassesHostIPToDiscovery -v
```

Expected: PASS.

Run the whole package suite:

```powershell
go test ./internal/adapters/plex/... -v
```

All PASS.

- [ ] **Step 6: Run full repo tests + race detector**

```powershell
go vet ./...
go test ./...
go test -race ./internal/adapters/plex/...
```

All clean / PASS.

- [ ] **Step 7: Commit**

```powershell
git add internal/adapters/plex/discovery.go internal/adapters/plex/adapter.go internal/adapters/plex/adapter_interface_test.go
git commit -m "feat(plex): thread HostIP through adapter to GDM discovery"
```

---

## Task 9: Manual end-to-end validation

**Files:** none (validation only).

The unit tests don't validate the actual outcome — that Bishop's PMS now sees `MiSTer` in `/clients` and Plex Web's cast picker shows it. This task is the manual procedure from the spec's "Validation steps" section.

This task does not produce a commit. Findings inform whether we ship as-is or apply the source-port addendum (sender bound to `:32412` with `SO_REUSEADDR`).

- [ ] **Step 1: Build the new binary**

```powershell
go build -ldflags "-X main.version=1.4.3-test" -o mister-groovy-relay.exe ./cmd/mister-groovy-relay
```

- [ ] **Step 2: Replace and restart the bridge**

Replace the running binary on the test host (laptop or Win10 VM, whichever has been used for prior verification) with the freshly built `mister-groovy-relay.exe`. Confirm it's the new one:

```powershell
$listenPid = (Get-NetTCPConnection -LocalPort 32500 -State Listen).OwningProcess
Get-Process -Id $listenPid | Format-List Name,Path,StartTime
```

`Path` should point at the freshly built `.exe`, not `Downloads\…1.4.1\…`.

- [ ] **Step 3: Start packet capture on Bishop**

On Bishop (Unraid host or a privileged container with `tcpdump`):

```bash
tcpdump -i any -n -s0 -w /tmp/gdm.pcap '(udp port 32412 or udp port 32413) and host <bridge-host-ip>'
```

Replace `<bridge-host-ip>` with `192.168.50.56` (laptop) or `192.168.50.252` (VM) depending on where the bridge is running.

Run for at least 2 minutes covering ≥2 heartbeat cycles.

- [ ] **Step 4: Watch the bridge log**

Tail the bridge's stdout. Successful HELLO/reply sends are intentionally silent (only failures log) — confirmation that they're going out comes from the pcap in step 3, not the log. What you're checking here:

- **Absence** of any new `WARN` lines containing `HELLO send failed` or `M-SEARCH reply send failed`. Either of those means the bridge tried to write a UDP datagram and the kernel rejected the call (typical causes: the sender bound to a now-invalid IP, or the network interface went down). If you see them, fix that before relying on the rest of validation.
- Existing `M-SEARCH received` debug lines should keep appearing whenever PMS or another local Plex client probes; that confirms inbound multicast still reaches the bridge.

- [ ] **Step 5: Query Bishop's `/clients` after waiting ~60 s**

```bash
curl -s "http://127.0.0.1:32400/clients?X-Plex-Token=<bishop-token>" | xmllint --format -
```

Get `<bishop-token>` from `/var/lib/plexmediaserver/Library/Application Support/Plex Media Server/Preferences.xml` (`PlexOnlineToken="..."`) on Bishop.

**Expected outcome A (success):** `MiSTer` appears in the `<MediaContainer>` alongside `PS4-903`. Stop here and proceed to step 6.

**Expected outcome B (still missing):** Inspect the pcap to determine which leg failed:

```bash
tcpdump -r /tmp/gdm.pcap -n
```

Decision tree:
- *No HELLO datagrams from `<bridge-host-ip>` arriving on Bishop's `:32413`* → LAN multicast is not traversing to Bishop. Bridge-side fix can't help. Recommend Ethernet for the bridge or pivot to Plan B (pubsub).
- *HELLOs arriving but `MiSTer` not in `/clients`* → PMS isn't acting on cross-host HELLOs. Run the manual M-SEARCH below; if M-SEARCH does land in the bridge log, source-port becomes the suspect → apply the source-port addendum (Task 10 below).
- *Replies from bridge to Bishop visible in pcap with ephemeral source port but `/clients` still empty* → source-port mismatch confirmed. Apply Task 10 addendum.

Manual M-SEARCH from Bishop (used in the second bullet above):

```bash
printf 'M-SEARCH * HTTP/1.1\r\nHost: 239.0.0.250:32412\r\nMan: "ssdp:discover"\r\nMX: 1\r\nST: plex/media-player\r\n\r\n' \
  | nc -u -w1 239.0.0.250 32412
```

Watch the bridge log for `plex GDM M-SEARCH received src=192.168.50.137:…`.

- [ ] **Step 6: Validate the cast picker**

In Firefox at `https://app.plex.tv`: F12 → Application → Storage → "Clear site data" for `app.plex.tv`. Hard-refresh (`Ctrl+Shift+R`). Start playing any item, click the cast icon. `MiSTer` should be selectable.

If `MiSTer` is in `/clients` (step 5 success) but absent from the picker, the picker may have a separate filter we haven't accounted for; capture DevTools Network for the cast click and report what the picker fetched. Otherwise, click `MiSTer` and verify the cast actually starts.

- [ ] **Step 7: Optional negative test — restart resilience**

Stop the bridge. Wait 60–120 s. PMS should age `MiSTer` out of `/clients`. Restart the bridge; entry should reappear within one heartbeat tick (~30 s by default).

---

## Task 10 (conditional): Source-port addendum

**Files (only if Task 9 step 5 outcome B with source-port suspicion):**
- Modify: `internal/adapters/plex/discovery.go`

**Skip this task entirely if Task 9 step 5 outcome A succeeded.**

If the packet capture in Task 9 confirms PMS is ignoring replies because they egress from an ephemeral source port instead of `:32412`, change the sender bind to share `:32412` with the listener via `SO_REUSEADDR`. This requires `golang.org/x/net/ipv4` (already added in Task 6) plus a small platform-specific control socket.

Defer detailed steps until we know we need them — implementing this against the wrong root cause introduces complexity for no benefit. If outcome B with this specific evidence appears, ask the operator and we'll add the implementation here as a focused follow-up.

---

## Self-review

**Spec coverage check:**
- HELLO heartbeat: Task 3.
- Two sockets / packetWriter seam: Task 2.
- HostIP in DiscoveryConfig: Task 5.
- Sender bound to HostIP: Task 5.
- SetMulticastInterface via x/net/ipv4: Task 6.
- WARN logging for send errors: Task 4.
- `interfaceForIP` helper: Task 1.
- `newDiscovery` seam + adapter threading: Task 8.
- `Close` idempotency: Task 7.
- TestDiscovery_RespondsToMSearch existing test edit: Task 2.
- TestDiscovery_RepliesViaSenderNotListener (with done-channel goroutine cleanup): Task 2.
- TestDiscovery_HelloHeartbeatRepeats: Task 3.
- TestDiscovery_HeartbeatFiresHelloImmediately: Task 3.
- TestDiscovery_HelloSendFailureLogsWarn / ReplySendFailureLogsWarn: Task 4.
- TestInterfaceForIP_FindsLoopback / NotFound / IPv6 / garbage: Task 1.
- TestSenderBindFor_EmptyHostIP / LocalIP / FallsBackWhenHostIPNotLocal / FallsBackOnGarbage / FallsBackOnIPv6: Task 5.
- TestDiscovery_CloseIdempotent: Task 7.
- TestAdapter_StartPassesHostIPToDiscovery: Task 8.
- Validation procedure: Task 9.
- Source-port addendum: Task 10 (conditional).

All spec items covered.

**Placeholder scan:** No "TBD", "TODO", or "implement appropriate X" left. Every code step shows the actual code; every command shows expected output. The one place the plan defers is the source-port addendum (Task 10), which is intentional and gated on validation evidence.

**Type/symbol consistency:**
- `packetWriter` interface: defined Task 2, used Tasks 2/3/4. The `fakeWriter` and `erroringWriter` test helpers both satisfy it.
- `helloInterval` package var: defined Task 3, used by `runHeartbeat` (also Task 3).
- `newDiscovery` package var: defined Task 8, called from `Adapter.Start` (Task 8).
- `interfaceForIP` helper: defined Task 1, used by `senderBindFor` (Task 5).
- `senderBindFor` helper: defined Task 5, called once in `NewDiscovery` (Task 5). Returns iface that's also reused by Task 6's SetMulticastInterface call.
- `closeOnce` / `closeErr` / `stop` / `wg` fields: declared in struct refactor (Task 2 for closeOnce/closeErr; Task 3 for stop/wg). Used Task 7 (closeOnce wraps the close sequence). Consistent throughout.
- `HostIP` field on `DiscoveryConfig`: defined Task 5, used Tasks 5/6/8.
- `iface` local in `NewDiscovery`: introduced Task 5 (resolves once, drives both listen and sender), reused by Task 6 for SetMulticastInterface.
- `capturingHandler` and `installCapturingSlog` test helpers: defined Task 4, only used by Task 4 (kept narrow).
- `fakeCore` test stub: defined Task 8, only used by Task 8.

No missing references.

**Review-fix coverage** (responses to two rounds of plan review):

First-round critical fixes (still in place):
- Initial-HELLO failure no longer aborts `NewDiscovery` — refactored further in the second-round fixes below; the immediate send is now inside `runHeartbeat`, so there is *no* startup send call from `NewDiscovery` to fail (Task 3 step 3).
- Non-local `HostIP` no longer disables GDM (Task 5): `senderBindFor` falls back to `(":0", nil)` on miss and emits a single WARN.
- Listen-side multicast join uses the resolved interface (Task 5 step 3).
- Adapter test fake returns `(nil, error)` so no nil-listen Run goroutine launches (Task 8 step 1).

Second-round fixes (this revision):
- `go mod edit -require=golang.org/x/net@v0.52.0` with explicit version + fallback note (Task 6 step 1) — no `go list` derivation that breaks when the dep is purely transitive.
- Task 8 test uses concrete `config.BridgeConfig{...}` literal and an inline `fakeCore` stub instead of non-existent `fakeBridgeConfig` / `fakeSessionManager` helpers.
- `senderBindFor` extracted as a side-effect-free helper testable via pure-function unit tests; five new `TestSenderBindFor_*` cases cover empty / local / non-local / garbage / IPv6 inputs without binding any real socket (Task 5 step 1). The previously-missing non-local fallback case is now pinned by `TestSenderBindFor_FallsBackWhenHostIPNotLocal`.
- Immediate-HELLO behavior is moved into `runHeartbeat` itself, then pinned by `TestDiscovery_HeartbeatFiresHelloImmediately` (Task 3) using the same packetWriter seam as the ticker test — no `NewDiscovery`-side seam needed.
- Reply test (`TestDiscovery_RepliesViaSenderNotListener`) now uses a `runDone` channel and explicit `listen.Close()`-then-wait sequence in `t.Cleanup` to avoid a goroutine leak (Task 2 step 1).
- Validation step 4 reworded — successful HELLO/reply sends are intentionally silent; the pcap is the on-the-wire confirmation, the log is only checked for *absence* of WARN failures (Task 9).
