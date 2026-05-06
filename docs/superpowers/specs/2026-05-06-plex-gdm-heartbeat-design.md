# Plex GDM heartbeat + interface-aware discovery — design

**Date:** 2026-05-06
**Status:** Draft, pending implementation

## Problem

The bridge does not appear in Plex Web's cast picker when it runs on
a host different from the user's Plex Media Server (PMS). The
companion path that *does* work in this scenario is PMS-as-proxy:

```
Plex Web (https://app.plex.tv)
   └─> PMS via plex.direct HTTPS
         └─> local LAN client (over plain HTTP)
```

For PMS to act as proxy it must list the bridge in its `/clients`
endpoint, which in Plex Companion v1 is populated exclusively by GDM
discovery. Today it does not — Bishop's `/clients` lists only PS4-903
and a Roku, never MiSTer, even after several minutes of bridge uptime.

Three concrete defects in [internal/adapters/plex/discovery.go]
(../../internal/adapters/plex/discovery.go):

1. The unsolicited `HELLO` advertisement is sent **once at startup**.
   If PMS is not listening at that exact instant — boot ordering,
   packet loss, NIC just came up, container restart — PMS never learns
   about the bridge and there is no rebroadcast.
2. The unicast M-SEARCH reply is written through the same
   `*net.UDPConn` that joined the multicast group. Sending unicast
   from a multicast-joined socket is fragile on Windows: the source IP
   selection is undefined and replies may arrive at the controller with
   a source address PMS does not accept.
3. `WriteToUDP` errors are silently discarded. A failure here is
   invisible from logs, so operators cannot tell whether GDM is
   actually working.

Co-located deployments (Docker container with `--network=host` on the
PMS host) sidestep all three defects because PMS reaches the bridge
over loopback and learns it within milliseconds. Cross-host deployments
do not.

## Goals

1. Bishop's PMS reliably lists `MiSTer` in `/clients` within ~60 s of
   bridge startup, every time, when bridge and PMS are on hosts that
   share LAN multicast reachability.
2. Reasonable resilience to boot-order races, isolated packet loss, and
   brief PMS restarts — without relying on PMS-side probing cadence.
3. Send-side failures are visible at WARN in the bridge log so an
   operator can diagnose a bad LAN segment without running a packet
   capture.
4. Deterministic interface selection on multi-NIC Windows hosts using
   the existing `bridge.host_ip` config (auto-detected via outbound-IP
   when blank).
5. No regression for the working co-located Docker deployment.

## Non-goals

- Joining every multicast-capable interface when no host IP is
  resolved. The existing `host_ip` mechanism already covers multi-NIC
  hosts; broadcasting on every interface multiplies complexity and
  Windows-specific quirks for a case the bridge doesn't have today.
- Implementing the plex.tv pubsub websocket (`provides=pubsub-player`).
  That is a separate, larger fix kept in reserve for cases where LAN
  GDM cannot work — see "Plan B" in the in-session debugging notes.
- Changing the GDM wire format, descriptor fields, or port choices.
  The descriptor that PMS already accepts in the co-located case must
  go out byte-for-byte the same.
- Platform-specific socket tuning (`IP_MULTICAST_IF`,
  `IP_MULTICAST_LOOP`, etc.). Stdlib defaults are good enough for v1;
  revisit only if heartbeats prove unreliable in practice.
- A retry/backoff policy for failed sends. WARN-log and move on; the
  next heartbeat tick is the retry.

## Approach

Two-socket model with an explicit interface choice and a periodic
HELLO heartbeat.

### Interface and address selection

`DiscoveryConfig` gains one field:

```go
type DiscoveryConfig struct {
    DeviceName string
    DeviceUUID string
    HTTPPort   int
    // HostIP is the configured-or-autodetected LAN IPv4 address the
    // bridge advertises as its connection URI. When non-empty AND
    // IPv4, Discovery uses the interface owning this address for
    // multicast join and binds the outbound sender socket to it.
    // Empty / IPv6 falls back to nil-interface default behavior.
    HostIP string
}
```

`HostIP` is what `cmd/mister-groovy-relay/main.go` already passes via
`AdapterConfig.HostIP` — populated either from `bridge.host_ip` or
from `outboundIP()`'s default-route auto-detect. From `Adapter.Start`
it threads into `DiscoveryConfig.HostIP` ([adapter.go:181-186]
(../../internal/adapters/plex/adapter.go#L181-L186)).

A side-effect-free helper `interfaceForIP(ip string) (*net.Interface,
error)` walks `net.Interfaces()` and returns the first interface whose
`Addrs()` contains the given IP. Used during `NewDiscovery`. Reads
system interface state but does not mutate it and binds no sockets,
so it's easy to unit-test.

Selection rules:

1. `HostIP` non-empty AND parseable AND `.To4() != nil`: try
   `interfaceForIP(HostIP)`. On success, multicast-join on that
   interface; bind sender to `HostIP:0`.
2. `HostIP` empty, IPv6, or interface lookup fails: log WARN with the
   reason and fall back to today's behavior — `nil` interface for the
   multicast join, `0.0.0.0:0` for the sender.
3. Multicast join itself fails (rare; port conflict, permissions): the
   adapter already treats GDM as best-effort. Log WARN and return an
   error from `NewDiscovery`; the caller in `Adapter.Start` keeps the
   bridge running with `disco = nil`.

### Two sockets per Discovery

- `listen` (`*net.UDPConn`): the multicast-joined socket on
  `239.0.0.250:32412`. Used **only** for reading M-SEARCH datagrams
  and detecting close.
- `sender` (held internally as a `packetWriter` interface; see "Test
  seam" below): a plain UDP socket created with `net.ListenPacket(
  "udp4", hostIP+":0")` (or `0.0.0.0:0` in fallback). Used for **all**
  outbound: HELLO heartbeats and unicast M-SEARCH replies.

For multicast HELLOs specifically, **binding the sender socket to a
host IP picks the source IP for unicast but does not by itself
constrain multicast egress to a specific interface** — particularly
on Windows, where outgoing multicast interface is governed by
`IP_MULTICAST_IF`, not by the bind address. To make multicast egress
deterministic on multi-NIC hosts, after creating the sender socket
the code calls
`golang.org/x/net/ipv4.NewPacketConn(sender).SetMulticastInterface(iface)`
when an interface was selected (Tier 1). `golang.org/x/net` v0.52.0 is
already in the module graph as a transitive dep; this change promotes
it to a direct require. Best-effort: failure to set the multicast
interface logs WARN and continues; multicast may then route to whichever
interface the kernel picks.

Sender socket source-IP becomes deterministic for unicast replies on
multi-NIC hosts (via the bind), and outgoing multicast interface
becomes deterministic for HELLOs (via `SetMulticastInterface`). UDP
**source port becomes ephemeral** instead of the listener's `32412`.

**Behavioral note for manual validation:** the descriptor body is
unchanged byte-for-byte; only the UDP envelope's source port changes.
GDM does not strictly require replies to come from `:32412`, and the
descriptor carries the Companion HTTP port explicitly, but PMS
implementations vary. The validation procedure below includes a
packet-capture step on Bishop precisely so this can be confirmed.
If `MiSTer` doesn't appear in PMS `/clients` after the change lands
*and* the capture shows our replies arriving but with an ephemeral
source port, the addendum fix is to bind sender to `:32412` with
`SO_REUSEADDR` so it shares the listener's port without conflict.

### HELLO heartbeat

Goroutine started by `NewDiscovery`. Sends HELLO immediately (matches
today's startup behavior, so co-located deployments don't regress on
first-tick latency), then on a `time.Ticker` at `helloInterval` until
`Close` signals stop.

`helloInterval` is a package-level `var` defaulting to `30 * time.Second`,
matching the pattern used elsewhere (`pollInterval`, `registerInterval`
in [linking.go](../../internal/adapters/plex/linking.go)). Tests
shorten it to ~50 ms.

The heartbeat goroutine writes via the `sender` socket and logs any
`WriteToUDP` error at WARN with destination context. The M-SEARCH
reply path does the same — both replace today's silent error swallow.

### Lifecycle

External API of `Discovery` is unchanged: `NewDiscovery` →
`Run` → `Close`. Internally `Close`:

1. Closes `listen` (causes `Run`'s read loop to return).
2. Signals heartbeat goroutine to stop, waits for it.
3. Closes `sender`.

`Close` is **idempotent**: subsequent calls are safe no-ops. Guarded
by a `sync.Once` over the close sequence so `Adapter.Stop`-then-test-
cleanup or any other double-call cannot panic on a second
`Close()`-on-closed-`*net.UDPConn` or send on a closed channel.

`Adapter.Stop` continues to call `disco.Close()` and then
`<-discoDone` (the `Run` goroutine's done channel) as today.

### Test seams

Two seams, both unexported:

1. **`packetWriter` interface for the sender side.** Define
   `type packetWriter interface { WriteTo(p []byte, addr net.Addr)
   (int, error); Close() error }`. `Discovery` holds the sender as a
   `packetWriter`, defaulting to a real `*net.UDPConn`. The HELLO
   heartbeat and M-SEARCH reply paths both write through this
   interface. Tests can swap in a counting fake (atomic counter, no
   real socket) and assert HELLO write count directly **without**
   relying on any loopback multicast delivery.
2. **Package-level `var newDiscovery = NewDiscovery` constructor seam.**
   The adapter test rebinds it to a fake that captures the
   `DiscoveryConfig` and returns a stub. `Adapter.Start` calls through
   this var instead of `NewDiscovery` directly.

## Implementation

Files touched:

- `internal/adapters/plex/discovery.go` — main change. Add `HostIP` to
  `DiscoveryConfig`, add `interfaceForIP`, refactor `Discovery` to
  hold both sockets and a heartbeat goroutine, replace silent error
  drops with WARN logs.
- `internal/adapters/plex/discovery_test.go` — existing tests stay;
  add tests listed below.
- `internal/adapters/plex/adapter.go` — at the call site in
  `Adapter.Start` ([adapter.go:181-186]
  (../../internal/adapters/plex/adapter.go#L181-L186)), pass
  `HostIP: a.cfg.HostIP` and call through the new `newDiscovery` var.
- `internal/adapters/plex/adapter_interface_test.go` (or a new test
  file) — adapter integration test pinning the threading.

## Tests

TDD order: tests written first.

1. **`TestDiscovery_RespondsToMSearch`** (existing, minor edit) —
   descriptor fields unchanged byte-for-byte. The test currently
   reads `d.conn.LocalAddr()` directly to find where to send the
   M-SEARCH; under the new field names it becomes `d.listen.LocalAddr()`.
   The test does *not* assert on the response's UDP source port (it
   discards the source addr from `ReadFromUDP`), so the source-port
   change is invisible to it. One-line update only.
2. **`TestDiscovery_HelloHeartbeatRepeats`** — set `helloInterval =
   50ms`, install a counting `packetWriter` fake via the test seam,
   run `NewDiscovery` for ~200ms, assert the fake recorded ≥3 HELLO
   writes to `239.0.0.250:32413`. **No real socket, no multicast,
   no loopback delivery dependency.**
3. **`TestDiscovery_RepliesViaSenderNotListener`** — install the
   counting `packetWriter` fake; deliver an M-SEARCH datagram via
   the listen socket; assert the fake (sender) recorded one
   `WriteTo` to the M-SEARCH source. Proves replies do not go back
   through the listen socket.
4. **`TestInterfaceForIP_FindsLoopback`** — pass `127.0.0.1`, assert
   returned interface is the system's loopback. Skips with `t.Skip`
   if `net.Interfaces()` returns no interface containing `127.0.0.1`
   (some sandboxed CI environments enumerate interfaces oddly).
5. **`TestInterfaceForIP_NotFound`** — pass `203.0.113.99` (TEST-NET-3,
   not assignable in the wild), assert the helper returns an error.
6. **`TestAdapter_StartPassesHostIPToDiscovery`** — rebind the
   package-level `newDiscovery` var to a fake constructor that
   records the `DiscoveryConfig`. Start the adapter with
   `AdapterConfig.HostIP = "10.0.0.5"`. Assert
   `recorded.HostIP == "10.0.0.5"`.
7. **`TestDiscovery_CloseIdempotent`** — call `Close` twice, assert
   no panic and no error on the second call.

Loopback multicast behavior is flaky cross-platform, so we
deliberately do not exercise multicast-join-on-loopback in any of
these tests. The `packetWriter` seam is what makes that possible.
The `interfaceForIP` helper plus the adapter-threading test plus
the seam-based heartbeat/reply tests cover all the new logic
without delivery flakiness.

## Risks

- **AP-level multicast filtering.** Consumer routers routinely apply
  IGMP snooping or multicast-to-unicast conversion across Wi-Fi /
  Ethernet boundaries. If the user's LAN drops multicast between the
  bridge host's segment and PMS's segment, no bridge-side change can
  fix it. Mitigation: documented manual M-SEARCH from Bishop as a
  diagnostic; if traffic doesn't arrive, recommend Ethernet or fall
  back to Plan B.
- **PMS Docker network namespace.** If PMS runs on a Docker bridge
  network rather than `--network=host`, LAN multicast may not reach
  it at all. LinuxServer.io's PMS image defaults to host networking
  and appears to be configured that way for the user's Bishop
  instance, so this should not bite — but it's worth a one-line
  mention in the README troubleshooting note.
- **Ephemeral source port for replies.** GDM responses now egress
  from a non-32412 source port. The Plex Companion descriptor body
  carries the Companion HTTP port explicitly, so receivers should not
  rely on the UDP source port — but PMS implementations vary across
  versions. If the validation step shows replies are reaching PMS but
  PMS still doesn't add MiSTer to `/clients`, source-port mismatch is
  the next thing to investigate. The fix would be a small addendum:
  bind the sender to `:32412` with `SO_REUSEADDR` so it shares the
  listener's port without conflict.

## Validation steps

After implementation lands and the bridge is rebuilt and running:

1. Tail the bridge log. Expect HELLO heartbeat lines at the configured
   cadence and no new WARN errors for sender writes.
2. **Start a packet capture on Bishop** for the duration of validation
   so we can directly observe what's on the wire instead of inferring
   from PMS state. Either tcpdump (Unraid host) or a temporary
   privileged container:

   ```bash
   tcpdump -i any -n -s0 -w /tmp/gdm.pcap '(udp port 32412 or udp port 32413) and host 192.168.50.252'
   ```

   Replace `.252` with whichever host the bridge is on. Run for ~2
   minutes covering at least 2 heartbeat cycles.
3. From Bishop, after waiting ~60 s:

   ```bash
   curl -s "http://127.0.0.1:32400/clients?X-Plex-Token=<bishop-token>" \
     | xmllint --format -
   ```

   `MiSTer` must be listed.
4. Hard-refresh Plex Web (clear `app.plex.tv` site data first), open
   the cast picker, confirm `MiSTer` is selectable.
5. (Optional negative test) Stop the bridge; wait 60–120 s; PMS should
   age out the entry from `/clients`. Restart the bridge; entry should
   reappear within one heartbeat tick.

If step 3 fails, inspect `/tmp/gdm.pcap` from step 2:

- **No HELLO datagrams from the bridge host arriving on Bishop's
  `:32413`** → LAN multicast is not traversing from bridge → PMS.
  No bridge-side fix can resolve this. Either put bridge on Ethernet,
  inspect router IGMP snooping settings, or move to Plan B (pubsub).
- **HELLOs arriving but PMS still doesn't list `MiSTer` in `/clients`** →
  PMS isn't acting on unsolicited HELLOs from cross-host clients
  (some PMS versions only trust HELLOs from the local host). Run the
  manual M-SEARCH below; a positive M-SEARCH path then ephemeral
  source-port becomes the next suspect.
- **M-SEARCHes from Bishop arriving at the bridge but no PMS-side
  reply receipts visible in the capture** → bridge's reply isn't
  reaching Bishop. WARN logs from the bridge should show write
  errors; if not, suspect Windows source-port/IP filtering on
  Bishop's side.
- **Replies arriving but PMS still ignoring them** → ephemeral source
  port is the cause. Apply the addendum from the "Two sockets"
  section: bind sender to `:32412` with `SO_REUSEADDR`.

Manual M-SEARCH from Bishop (used in the second-bullet case above):

```bash
printf 'M-SEARCH * HTTP/1.1\r\nHost: 239.0.0.250:32412\r\nMan: "ssdp:discover"\r\nMX: 1\r\nST: plex/media-player\r\n\r\n' \
  | nc -u -w1 239.0.0.250 32412
```

Then watch the bridge log for an M-SEARCH receipt line with
`src=192.168.50.137:…`. Receipt seen → reply path is in play and the
capture's reply-side bullets above narrow further. No receipt → LAN
multicast doesn't traverse Bishop → bridge host; same conclusion as
"no HELLOs arriving" above.
