# GroovyNLC Part 1 — Detection and Plumbing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect whether the MiSTer runs the original Groovy core or the GroovyNLC fork, keep sessions alive through the silent prebuffer window, and replace the `lz4_enabled` boolean with a `codec` enum (`auto` | `raw` | `lz4`), without changing behaviour for existing users.

**Architecture:** A one-byte `GET_VERSION` probe runs at the start of every `Plane.Run` (before INIT, while no Drainer owns the socket). A `Run`-owned keepalive goroutine sends `GET_STATUS` after 2 s of send silence. ACKs with `FrameEcho == 0` stop counting as echo movement. The codec moves from a bool to a configured string resolved per session by a pure `dataplane.ResolveCodec`. The detected core and effective codec are exposed as plane atomics and surfaced in logs, the status view and the meter. Fake-mister learns to impersonate either core, including idle reaping.

**Tech Stack:** Go 1.26, `github.com/BurntSushi/toml`, `html/template` + vanilla JS (chassis), the in-repo fake-mister.

**Spec:** [docs/superpowers/specs/2026-09-28-groovynlc-support-design.md](../specs/2026-09-28-groovynlc-support-design.md). Section refs below ("§3.3") point into it.

## Global Constraints

- Go 1.26; pure Go, no cgo.
- Keep all four CI gates green: `go vet ./...`, `go test ./...`, `go test -race ./...` (runs in CI only, because local cgo is unavailable), `go test -tags=integration ./...` (needs ffmpeg + ffprobe on PATH).
- INIT stays 5 bytes on both cores. The bridge never sends a 6-byte INIT or capability flags (§3.2).
- Probe timeout 200 ms; keepalive idle threshold 2 s (§3.1, §3.3).
- `Manager.mu` is never held across network I/O. The probe and keepalive live in `dataplane`, never in `core`.
- `codec` default is `auto`. In Part 1, `auto` resolves to `lz4` on both cores (§4.2). `nlc` is **not** a valid value in Part 1.
- `ScopeRestartCast` for `video.codec`.
- Commit on the current branch (`main`); no new branch. Commit only your paths: `git commit -m "..." -- <paths>`, then check `git show --stat HEAD`.
- Some Go files are CRLF inside the blob (for example `internal/chassis/chassis_test.go`). After committing, `git show --stat HEAD` must not show a whole-file rewrite; if it does, restore CRLF (`perl -pi -e 's/\n/\r\n/'`), then `git -c core.autocrlf=false add --renormalize <file>` and amend.
- **Clean-tree gate (every task, Step 0).** Before starting a task, run `git status --porcelain -- internal cmd tests`. It must print nothing. If another session has uncommitted edits there, stop and wait for them to land. Commit steps use directory paths and `$(git diff --name-only ...)` lists, and those are only safe on a tree that holds nothing but your changes.
- Plan revision 2 (2026-09-28) is anchored on commit `57d87f0b` (LZ4 zero-size-blit fix). That commit added `holdField` to `plane.go`, `compression`/`modeZeroLZ4` handling to `fakemister.RunWithFields`, and `tests/integration/lz4_underrun_test.go`.
- Verification in every task that touches Go includes `go vet -tags=integration ./...`, because plain `go vet ./...` skips the `//go:build integration` files.
- Commit steps name directories for brevity. Before each commit, run `git diff --stat -- <those paths>` and confirm that every changed file is yours; another session may have uncommitted edits in the same directories (especially `internal/dataplane/plane.go`). If someone else's file shows up, list your files explicitly instead.
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
- Another session may be fixing the LZ4 zero-size-blit bug in `internal/dataplane/plane.go` (`sendField` / `sendDuplicate`). Before Tasks 4 and 5, run `git log --oneline -5 -- internal/dataplane/plane.go` and re-read the functions you edit; anchor edits on code, not on the line numbers quoted here.

## File Map

| File | Responsibility |
|------|----------------|
| `internal/groovy/core.go` (new) | `Core` kind, `CoreFromVersion`, `BuildGetVersion`, `BuildGetStatus` |
| `internal/groovynet/sender.go` | `ProbeVersion`, `LastSend` tracking |
| `internal/fakemister/listener.go` | GET_VERSION/GET_STATUS parsing, core impersonation, idle reaping |
| `cmd/fake-mister/main.go` | `-core`, `-idle-timeout` flags |
| `internal/config/codec.go` (new) | codec constants, `EffectiveCodec`, legacy `lz4_enabled` migration |
| `internal/config/config.go`, `migration.go`, `example.toml` | `Codec` field, validation, defaults, flat migration |
| `internal/chassis/settings.go`, `templates/settings-av.html` | codec select |
| `internal/uiserver/bridge_saver.go` | `video.codec` diff + scope |
| `internal/dataplane/codec.go` (new) | `Codec`, `ResolveCodec`, INIT byte |
| `internal/dataplane/keepalive.go` (new) | keepalive loop |
| `internal/dataplane/plane.go` | probe, codec, keepalive, echo filter, prebuffer ACK drain, accessors |
| `internal/core/{types,meter,manager}.go` | codec + core in meter/status |
| `internal/chassis/{meter,events}.go`, `templates/meter.html`, `static/meter.js` | core readout |
| `tests/integration/helper_test.go` + 4 modeline/plane tests | INIT replier tolerates the probe |
| `README.md` | GroovyNLC section, codec docs, license wording |

---

### Task 1: Core kind, version probe and send tracking

**Files:**
- Create: `internal/groovy/core.go`, `internal/groovy/core_test.go`
- Modify: `internal/groovynet/sender.go` (struct `Sender`, `Send`, `SendPayload`, new methods)
- Test: `internal/groovynet/probe_test.go` (new)

**Interfaces:**
- Produces: `groovy.Core` (`CoreUnknown`, `CoreGroovy`, `CoreGroovyNLC`), `groovy.CoreFromVersion(v byte) Core`, `(Core) String() string`, `groovy.BuildGetVersion() []byte`, `groovy.BuildGetStatus() []byte`, `(*groovynet.Sender).ProbeVersion(timeout time.Duration) (byte, error)`, `(*groovynet.Sender).LastSend() time.Time`, `(*groovynet.Sender).SendIfIdle(pkt []byte, idle time.Duration) (sent bool, err error)`. It checks and writes under `Sender.mu`, so a keepalive can never land between a blit/audio header and its payload. `SendInitAwaitACK` now skips datagrams that aren't 13 bytes (for example a GET_VERSION reply arriving after the probe timed out) instead of failing.

- [ ] **Step 1: Write the failing groovy test**

`internal/groovy/core_test.go`:

```go
package groovy

import "testing"

func TestCoreFromVersion(t *testing.T) {
	cases := []struct {
		v    byte
		want Core
	}{
		{0, CoreGroovy}, // no reply
		{1, CoreGroovy},
		{2, CoreGroovyNLC},
		{3, CoreGroovyNLC},
	}
	for _, c := range cases {
		if got := CoreFromVersion(c.v); got != c.want {
			t.Errorf("CoreFromVersion(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestCoreString(t *testing.T) {
	if CoreGroovy.String() != "groovy" || CoreGroovyNLC.String() != "groovynlc" || CoreUnknown.String() != "unknown" {
		t.Fatalf("unexpected names: %q %q %q", CoreGroovy, CoreGroovyNLC, CoreUnknown)
	}
}

func TestQueryBuilders(t *testing.T) {
	if got := BuildGetVersion(); len(got) != 1 || got[0] != CmdGetVersion {
		t.Fatalf("BuildGetVersion = %v", got)
	}
	if got := BuildGetStatus(); len(got) != 1 || got[0] != CmdGetStatus {
		t.Fatalf("BuildGetStatus = %v", got)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/groovy -run 'TestCore|TestQueryBuilders'`
Expected: FAIL, `undefined: CoreFromVersion`.

- [ ] **Step 3: Implement `internal/groovy/core.go`**

```go
package groovy

// Core identifies which Groovy_MiSTer build answered GET_VERSION.
// See docs/superpowers/specs/2026-09-28-groovynlc-support-design.md §3.1.
type Core uint8

const (
	CoreUnknown   Core = iota // not probed yet
	CoreGroovy                // original psakhis core (GET_VERSION = 1, or no reply)
	CoreGroovyNLC             // verbst fork (GET_VERSION >= 2)
)

// CoreFromVersion maps a GET_VERSION reply byte to a Core. 0 means the
// probe timed out; the original core is assumed.
func CoreFromVersion(v byte) Core {
	if v >= 2 {
		return CoreGroovyNLC
	}
	return CoreGroovy
}

func (c Core) String() string {
	switch c {
	case CoreGroovy:
		return "groovy"
	case CoreGroovyNLC:
		return "groovynlc"
	}
	return "unknown"
}

// BuildGetVersion returns the 1-byte GET_VERSION query. The core replies
// with a single version byte.
func BuildGetVersion() []byte { return []byte{CmdGetVersion} }

// BuildGetStatus returns the 1-byte GET_STATUS query. Both cores reply
// with a 13-byte ACK whose frame echo is 0.
func BuildGetStatus() []byte { return []byte{CmdGetStatus} }
```

- [ ] **Step 4: Run and confirm it passes**

Run: `go test ./internal/groovy -run 'TestCore|TestQueryBuilders'`
Expected: PASS.

- [ ] **Step 5: Write the failing groovynet tests**

`internal/groovynet/probe_test.go`:

```go
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
```

- [ ] **Step 6: Run and confirm they fail**

Run: `go test ./internal/groovynet -run 'TestProbeVersion|TestSender_LastSend|TestSender_SendIfIdle|TestSendInitAwaitACK_SkipsLateVersionReply'`
Expected: FAIL, `s.ProbeVersion undefined`.

- [ ] **Step 7: Implement in `internal/groovynet/sender.go`**

Add to the `Sender` struct, after `enobufCount`:

```go
	// lastSendUnix is the wall time (UnixNano) of the most recent successful
	// write. The dataplane keepalive reads it to detect send silence.
	lastSendUnix atomic.Int64
```

In `Send`, replace the body after the lock with:

```go
	_, err := s.conn.WriteToUDP(pkt, s.dstAddr)
	if err == nil {
		s.lastSendUnix.Store(time.Now().UnixNano())
	}
	return err
```

In `SendPayload`, directly before the final `return nil`, add:

```go
	s.lastSendUnix.Store(time.Now().UnixNano())
```

Add these methods after `SendInitAwaitACKWithRetry`:

```go
// LastSend returns when the Sender last wrote a datagram successfully, or
// the zero Time if it never has. Safe for concurrent use.
func (s *Sender) LastSend() time.Time {
	n := s.lastSendUnix.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// ProbeVersion sends GET_VERSION and waits up to timeout for the core's
// 1-byte reply (1 = original Groovy core, >= 2 = GroovyNLC). Datagrams of
// any other length, such as a stray 13-byte ACK, are skipped. A timeout
// is not an error: it returns 0, which callers treat as the original core.
//
// Same socket contract as SendInitAwaitACK: callers must not have a
// Drainer reading this socket. See design 2026-09-28 §3.1.
func (s *Sender) ProbeVersion(timeout time.Duration) (byte, error) {
	if flushed := s.flushStaleDatagrams(); flushed > 0 {
		slog.Debug("flushed stale datagrams before GET_VERSION", "count", flushed)
	}
	if err := s.Send(groovy.BuildGetVersion()); err != nil {
		return 0, err
	}
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}
	defer s.conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 64)
	for {
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return 0, nil
			}
			return 0, fmt.Errorf("read version reply: %w", err)
		}
		if n == 1 {
			return buf[0], nil
		}
	}
}

// SendIfIdle writes pkt only if nothing has been written for at least
// idle, and reports whether it wrote. The check and the write happen under
// mu, and every header write (Send) stamps lastSendUnix under that same
// lock. A keepalive therefore can never slip between a BLIT/AUDIO header
// and its payload: it either runs before the header or sees the header's
// fresh stamp. See design 2026-09-28 §3.3.
func (s *Sender) SendIfIdle(pkt []byte, idle time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.lastSendUnix.Load(); n != 0 && time.Since(time.Unix(0, n)) < idle {
		return false, nil
	}
	if _, err := s.conn.WriteToUDP(pkt, s.dstAddr); err != nil {
		return false, err
	}
	s.lastSendUnix.Store(time.Now().UnixNano())
	return true, nil
}
```

In `SendInitAwaitACK`, replace the single read

```go
	buf := make([]byte, groovy.ACKPacketSize*2)
	n, _, err := s.conn.ReadFromUDP(buf)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return groovy.ACK{}, &InitACKTimeoutError{Timeout: timeout, Err: err}
		}
		return groovy.ACK{}, fmt.Errorf("read INIT ack: %w", err)
	}
	if n != groovy.ACKPacketSize {
		return groovy.ACK{}, fmt.Errorf("INIT ack wrong size: %d", n)
	}
	return groovy.ParseACK(buf[:n])
```

with a loop that skips datagrams of the wrong size until the deadline:

```go
	buf := make([]byte, groovy.ACKPacketSize*2)
	for {
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return groovy.ACK{}, &InitACKTimeoutError{Timeout: timeout, Err: err}
			}
			return groovy.ACK{}, fmt.Errorf("read INIT ack: %w", err)
		}
		if n != groovy.ACKPacketSize {
			// For example a GET_VERSION reply that arrived after
			// ProbeVersion gave up. Keep waiting for the real ACK.
			slog.Debug("skipping non-ACK datagram while awaiting INIT ack", "n", n)
			continue
		}
		return groovy.ParseACK(buf[:n])
	}
```

If an existing sender test asserts the old `"INIT ack wrong size"` error, change it to expect an `InitACKTimeoutError` (`IsInitACKTimeout(err)`), because a wrong-sized datagram is now skipped and the wait runs out.

- [ ] **Step 8: Run and confirm it passes**

Run: `go test ./internal/groovy ./internal/groovynet`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/groovy/core.go internal/groovy/core_test.go internal/groovynet/probe_test.go
git commit -m "feat(groovynet): probe core version and track last send

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" -- internal/groovy/core.go internal/groovy/core_test.go internal/groovynet/sender.go internal/groovynet/probe_test.go
```

---

### Task 2: Fake-mister core impersonation and idle reaping

**Files:**
- Modify: `internal/fakemister/listener.go` (`Listener` struct, `EnableACKs` neighbourhood, `Run`, `RunWithFields`, `ParseCommand`)
- Modify: `cmd/fake-mister/main.go`
- Test: `internal/fakemister/core_test.go` (new)

**Interfaces:**
- Consumes: `groovy.CmdGetVersion`, `groovy.CmdGetStatus`.
- Produces: `(*fakemister.Listener).SetCoreVersion(v byte)`, `(*fakemister.Listener).SetIdleTimeout(d time.Duration)`, `(*fakemister.Listener).Reaps() uint64`. Behaviour: with `EnableACKs`, GET_VERSION is answered (default `1`) and GET_STATUS is ACKed; version ≥ 2 also ACKs SWITCHRES; version < 2 drops 6-byte INITs; with an idle timeout, a session silent for longer than it is closed, and BLIT/AUDIO/SWITCHRES are ignored until the next INIT.

- [ ] **Step 1: Write the failing tests**

`internal/fakemister/core_test.go`:

```go
package fakemister

import (
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// dialFake starts l.Run and returns a client socket connected to it plus
// the command channel.
func dialFake(t *testing.T, l *Listener) (*net.UDPConn, chan Command) {
	t.Helper()
	cmds := make(chan Command, 64)
	go l.Run(cmds)
	c, err := net.DialUDP("udp4", nil, l.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); l.Close() })
	return c, cmds
}

func readReply(t *testing.T, c *net.UDPConn) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

func newFake(t *testing.T) *Listener {
	t.Helper()
	l, err := NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestFake_GetVersionDefaultsToOriginalCore(t *testing.T) {
	l := newFake(t)
	l.EnableACKs(false)
	c, _ := dialFake(t, l)
	_, _ = c.Write([]byte{groovy.CmdGetVersion})
	if got := readReply(t, c); len(got) != 1 || got[0] != 1 {
		t.Fatalf("GET_VERSION reply = %v, want [1]", got)
	}
}

func TestFake_NLCModeRepliesVersionAndACKsSwitchres(t *testing.T) {
	l := newFake(t)
	l.EnableACKs(false)
	l.SetCoreVersion(2)
	c, _ := dialFake(t, l)
	_, _ = c.Write([]byte{groovy.CmdGetVersion})
	if got := readReply(t, c); len(got) != 1 || got[0] != 2 {
		t.Fatalf("GET_VERSION reply = %v, want [2]", got)
	}
	_, _ = c.Write(groovy.BuildSwitchres(groovy.NTSC480i60))
	if got := readReply(t, c); len(got) != groovy.ACKPacketSize {
		t.Fatalf("SWITCHRES reply len = %d, want ACK", len(got))
	}
}

func TestFake_OriginalCoreDoesNotACKSwitchres(t *testing.T) {
	l := newFake(t)
	l.EnableACKs(false)
	c, _ := dialFake(t, l)
	_, _ = c.Write(groovy.BuildSwitchres(groovy.NTSC480i60))
	if got := readReply(t, c); got != nil {
		t.Fatalf("original core replied to SWITCHRES: %v", got)
	}
}

func TestFake_GetStatusIsACKed(t *testing.T) {
	l := newFake(t)
	l.EnableACKs(false)
	c, cmds := dialFake(t, l)
	_, _ = c.Write([]byte{groovy.CmdGetStatus})
	if got := readReply(t, c); len(got) != groovy.ACKPacketSize {
		t.Fatalf("GET_STATUS reply len = %d, want %d", len(got), groovy.ACKPacketSize)
	}
	if cmd := <-cmds; cmd.Type != groovy.CmdGetStatus {
		t.Fatalf("command = %d, want GET_STATUS", cmd.Type)
	}
}

func TestFake_OriginalCoreDropsSixByteInit(t *testing.T) {
	l := newFake(t)
	l.EnableACKs(false)
	c, _ := dialFake(t, l)
	_, _ = c.Write([]byte{groovy.CmdInit, 1, 3, 2, 0, 0x04})
	if got := readReply(t, c); got != nil {
		t.Fatalf("original core ACKed a 6-byte INIT: %v", got)
	}
}

func TestFake_IdleTimeoutReapsAndDropsBlits(t *testing.T) {
	l := newFake(t)
	l.EnableACKs(false)
	l.SetIdleTimeout(100 * time.Millisecond)
	c, cmds := dialFake(t, l)
	_, _ = c.Write(groovy.BuildInit(groovy.LZ4ModeOff, groovy.AudioRateOff, 0, groovy.RGBMode888))
	readReply(t, c)
	<-cmds // INIT
	time.Sleep(200 * time.Millisecond)
	_, _ = c.Write(groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 1, Duplicate: true}))
	select {
	case cmd := <-cmds:
		t.Fatalf("blit after reap was delivered: type %d", cmd.Type)
	case <-time.After(100 * time.Millisecond):
	}
	if l.Reaps() != 1 {
		t.Fatalf("Reaps = %d, want 1", l.Reaps())
	}
	_, _ = c.Write(groovy.BuildInit(groovy.LZ4ModeOff, groovy.AudioRateOff, 0, groovy.RGBMode888))
	readReply(t, c)
	<-cmds // INIT reopens the session
	_, _ = c.Write(groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 2, Duplicate: true}))
	if cmd := <-cmds; cmd.Type != groovy.CmdBlitFieldVSync {
		t.Fatalf("blit after re-INIT not delivered: type %d", cmd.Type)
	}
}

func TestFake_KeepaliveTrafficPreventsReap(t *testing.T) {
	l := newFake(t)
	l.EnableACKs(false)
	l.SetIdleTimeout(150 * time.Millisecond)
	c, cmds := dialFake(t, l)
	_, _ = c.Write(groovy.BuildInit(groovy.LZ4ModeOff, groovy.AudioRateOff, 0, groovy.RGBMode888))
	readReply(t, c)
	<-cmds
	for i := 0; i < 4; i++ {
		time.Sleep(80 * time.Millisecond)
		_, _ = c.Write([]byte{groovy.CmdGetStatus})
		readReply(t, c)
	}
	if l.Reaps() != 0 {
		t.Fatalf("Reaps = %d, want 0", l.Reaps())
	}
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/fakemister -run TestFake_`
Expected: FAIL, `l.SetCoreVersion undefined`.

- [ ] **Step 3: Parse the query commands**

In `ParseCommand`, add a case before `case groovy.CmdAudio:`:

```go
	case groovy.CmdGetVersion, groovy.CmdGetStatus:
		// 1-byte queries. Longer datagrams starting with these bytes are
		// payload fragments that reached the stateless Run loop.
		if len(pkt) != 1 {
			return c, fmt.Errorf("query command %d must be 1 byte, got %d", pkt[0], len(pkt))
		}
```

Update the doc comment's "handles all five Groovy command IDs" to list GET_STATUS and GET_VERSION too.

- [ ] **Step 4: Add core state to `Listener`**

Add fields to the `Listener` struct after `audioReadyBit`:

```go
	// coreVersion is the GET_VERSION reply (0 = default 1, the original
	// core). Version >= 2 impersonates GroovyNLC: SWITCHRES is ACKed.
	// Version < 2 drops 6-byte INITs like the original core. Set before
	// Run / RunWithFields; read without locking by the loop goroutine.
	coreVersion byte
	// idleTimeout > 0 models GroovyNLC idle reaping (design 2026-09-28
	// §1.1f): once a session goes quiet for longer than this, it closes
	// and BLIT/AUDIO/SWITCHRES are ignored until the next INIT.
	// sessionOpen and lastRecv are loop-goroutine owned; reaps is read by
	// tests.
	idleTimeout time.Duration
	sessionOpen bool
	lastRecv    time.Time
	reaps       atomic.Uint64
```

`sync/atomic` is already imported (it is used for `zeroSizeLZ4Blits`). Add these methods after `EnableACKs`:

```go
// SetCoreVersion sets the GET_VERSION reply: 1 impersonates the original
// Groovy core (the default), 2 impersonates GroovyNLC. Call before Run.
func (l *Listener) SetCoreVersion(v byte) { l.coreVersion = v }

// SetIdleTimeout enables GroovyNLC-style idle reaping. Call before Run.
func (l *Listener) SetIdleTimeout(d time.Duration) { l.idleTimeout = d }

// Reaps reports how many sessions the idle timeout has closed.
func (l *Listener) Reaps() uint64 { return l.reaps.Load() }

func (l *Listener) version() byte {
	if l.coreVersion == 0 {
		return 1
	}
	return l.coreVersion
}

// observeArrival records datagram activity. Any datagram counts, as on the
// real core. A gap longer than idleTimeout closes the open session first.
func (l *Listener) observeArrival(now time.Time) {
	if l.idleTimeout > 0 && l.sessionOpen && !l.lastRecv.IsZero() &&
		now.Sub(l.lastRecv) > l.idleTimeout {
		l.sessionOpen = false
		l.reaps.Add(1)
	}
	l.lastRecv = now
}

// respond mirrors the real core's replies to one command datagram. It
// returns false when the core would discard the command.
func (l *Listener) respond(cmd Command, src *net.UDPAddr) bool {
	switch cmd.Type {
	case groovy.CmdGetVersion:
		if l.ackOnInit {
			_, _ = l.conn.WriteToUDP([]byte{l.version()}, src)
		}
		return true
	case groovy.CmdGetStatus:
		if l.ackOnInit {
			l.emitInitACK(src)
		}
		return true
	case groovy.CmdInit:
		if len(cmd.Raw) == 6 && l.version() < 2 {
			return false
		}
		l.sessionOpen = true
		if l.ackOnInit {
			l.emitInitACK(src)
		}
		return true
	case groovy.CmdClose:
		l.sessionOpen = false
		return true
	}
	// Session gating only applies in idle-timeout mode, so existing tests
	// that send blits without an INIT keep working.
	if l.idleTimeout > 0 && !l.sessionOpen {
		return false
	}
	if cmd.Type == groovy.CmdSwitchres && l.ackOnInit && l.version() >= 2 {
		l.emitInitACK(src)
	}
	return true
}
```

- [ ] **Step 5: Route both loops through `respond`**

In `Run`, replace

```go
		cmd.ReceivedAt = recvAt
		if l.ackOnInit && cmd.Type == groovy.CmdInit {
			l.emitInitACK(src)
		}
		events <- cmd
```

with

```go
		cmd.ReceivedAt = recvAt
		if !l.respond(cmd, src) {
			continue
		}
		events <- cmd
```

and call `l.observeArrival(recvAt)` immediately after the successful `ReadFromUDP` (before `ParseCommand`, so unparsable datagrams still count as activity).

In `RunWithFields` (as of `57d87f0b`), call `l.observeArrival(recvAt)` directly after `recvAt := time.Now()`, before the `modeZeroLZ4` check, so every datagram counts as activity.

In the `modeCommand` branch, replace this block:

```go
			cmd.ReceivedAt = recvAt
			if cmd.Type == groovy.CmdInit {
				// verbst fork: codecMode = INIT[1] & 3, compression when >= 1.
				// psakhis: INIT[1] <= 1 ? INIT[1] : 0. Both agree on 0 and 1,
				// the only values the relay sends.
				compression = cmd.Init.LZ4Frames&0x3 != 0
				if l.ackOnInit {
					l.emitInitACK(src)
				}
			}
```

with:

```go
			cmd.ReceivedAt = recvAt
			if !l.respond(cmd, src) {
				// The core discards this command (reaped session, or a
				// 6-byte INIT on the original core). A rejected BLIT/AUDIO
				// header's payload chunks exceed maxCommandLen and are
				// dropped by the oversized-datagram guard above, as on the
				// real core.
				continue
			}
			if cmd.Type == groovy.CmdInit {
				// verbst fork: codecMode = INIT[1] & 3, compression when >= 1.
				// psakhis: INIT[1] <= 1 ? INIT[1] : 0. Both agree on 0 and 1,
				// the only values the relay sends.
				compression = cmd.Init.LZ4Frames&0x3 != 0
			}
```

Leave everything after it unchanged: the zero-size-LZ4 branch, `cmds <- cmd`, and the blit/audio mode switch. `respond` now sends the INIT ACK, so it must not also be sent here.

- [ ] **Step 6: Run and confirm it passes**

Run: `go test ./internal/fakemister`
Expected: PASS, including the existing listener tests.

- [ ] **Step 7: Add the fake-mister flags**

In `cmd/fake-mister/main.go` add, after the `pngEvery` flag:

```go
	core := flag.String("core", "groovy", `core to impersonate: "groovy" (original) or "nlc" (GroovyNLC)`)
	idleTimeout := flag.Duration("idle-timeout", 0, "close a session idle this long, like GroovyNLC (0 disables)")
```

After `defer l.Close()`:

```go
	// Answer INIT / GET_STATUS / GET_VERSION like a real MiSTer so the
	// bridge's handshake and core probe complete.
	l.EnableACKs(true)
	switch *core {
	case "groovy":
		l.SetCoreVersion(1)
	case "nlc":
		l.SetCoreVersion(2)
	default:
		slog.Error("unknown -core", "core", *core)
		os.Exit(2)
	}
	l.SetIdleTimeout(*idleTimeout)
```

and add `"core", *core, "idle_timeout", idleTimeout.String()` to the existing `slog.Info("fake-mister listening", ...)` call.

- [ ] **Step 8: Build and vet**

Run: `go build -o /dev/null ./cmd/fake-mister && go vet ./internal/fakemister ./cmd/fake-mister && go vet -tags=integration ./...`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add internal/fakemister/core_test.go
git commit -m "feat(fakemister): impersonate Groovy or GroovyNLC cores with idle reaping

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" -- internal/fakemister/listener.go internal/fakemister/core_test.go cmd/fake-mister/main.go
```

---

### Task 3: `codec` config field, migration, settings UI and saver

**Files:**
- Create: `internal/config/codec.go`, `internal/config/codec_test.go`
- Modify: `internal/config/config.go` (`VideoConfig`, `Sectioned.Validate`), `internal/config/migration.go` (`Migrate`, `loadSectionedFromBytes`, `defaultBridge`), `internal/config/example.toml`
- Modify: `internal/chassis/settings.go`, `internal/chassis/templates/settings-av.html`, `internal/uiserver/bridge_saver.go`
- Modify (compile shims, replaced in Task 4): `internal/core/manager.go`, `internal/core/meter.go`
- Tests to update: every `*_test.go` referencing `Video.LZ4Enabled` / `video_lz4_enabled` (find them with `grep -rn "LZ4Enabled\|lz4_enabled" --include=*_test.go internal cmd tests`)

**Interfaces:**
- Produces: `config.CodecAuto = "auto"`, `config.CodecRaw = "raw"`, `config.CodecLZ4 = "lz4"`, field `VideoConfig.Codec string` (toml `codec`), `(VideoConfig).EffectiveCodec() string` (maps `""` to `"auto"`). `VideoConfig.LZ4Enabled` is **removed**; `VideoConfig.LegacyLZ4Enabled *bool` (toml `lz4_enabled`) exists only for migration and is always nil after load. Form field `video_codec`; saver key `video.codec`.

- [ ] **Step 1: Write the failing config tests**

`internal/config/codec_test.go`:

```go
package config

import (
	"regexp"
	"strings"
	"testing"
)

func TestEffectiveCodec(t *testing.T) {
	for in, want := range map[string]string{"": "auto", "auto": "auto", "raw": "raw", "lz4": "lz4"} {
		if got := (VideoConfig{Codec: in}).EffectiveCodec(); got != want {
			t.Errorf("EffectiveCodec(%q) = %q, want %q", in, got, want)
		}
	}
}

func loadVideo(t *testing.T, video string) VideoConfig {
	t.Helper()
	doc := "[bridge]\n[bridge.mister]\nhost = \"10.0.0.2\"\n[bridge.video]\n" + video
	s, meta, err := loadSectionedFromBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	s.meta = meta
	return s.Bridge.Video
}

func TestSectionedLoad_LegacyLZ4Migrates(t *testing.T) {
	cases := []struct {
		video string
		want  string
	}{
		{"lz4_enabled = false\n", CodecRaw},
		{"lz4_enabled = true\n", CodecAuto},
		{"", CodecAuto},                                  // default
		{"codec = \"lz4\"\nlz4_enabled = false\n", CodecLZ4}, // explicit codec wins
	}
	for _, c := range cases {
		v := loadVideo(t, c.video)
		if v.Codec != c.want {
			t.Errorf("%q: Codec = %q, want %q", c.video, v.Codec, c.want)
		}
		if v.LegacyLZ4Enabled != nil {
			t.Errorf("%q: LegacyLZ4Enabled not cleared", c.video)
		}
	}
}

func TestSectionedValidate_RejectsUnknownCodec(t *testing.T) {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.MiSTer.Host = "10.0.0.2"
	s.Bridge.Video.Codec = "nlc" // not valid until Part 2
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "bridge.video.codec") {
		t.Fatalf("Validate() = %v, want bridge.video.codec error", err)
	}
	s.Bridge.Video.Codec = ""
	if err := s.Validate(); err != nil {
		t.Fatalf("empty codec should validate as auto: %v", err)
	}
}

func TestMigrate_FlatLZ4DisabledBecomesRaw(t *testing.T) {
	out, err := Migrate([]byte("mister_host = \"10.0.0.2\"\nlz4_enabled = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `codec = "raw"`) {
		t.Fatalf("migrated config lacks codec = \"raw\":\n%s", out)
	}
	// Match the key itself, not delta_lz4_enabled (always encoded).
	if regexp.MustCompile(`(?m)^\s*lz4_enabled\s*=`).MatchString(string(out)) {
		t.Fatalf("migrated config still has lz4_enabled:\n%s", out)
	}
}
```

(If `Migrate` requires more legacy keys for format detection, copy the minimal legacy document used by an existing test in `migration_test.go`, keeping `lz4_enabled = false`.)

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/config -run 'Codec|LegacyLZ4|FlatLZ4'`
Expected: FAIL, `EffectiveCodec undefined`.

- [ ] **Step 3: Implement `internal/config/codec.go`**

```go
package config

import "github.com/BurntSushi/toml"

// Frame compression codecs for bridge.video.codec. See
// docs/superpowers/specs/2026-09-28-groovynlc-support-design.md §4.
const (
	CodecAuto = "auto" // bridge picks per core; LZ4 today (§4.2)
	CodecRaw  = "raw"
	CodecLZ4  = "lz4"
)

// EffectiveCodec resolves an unset Codec to CodecAuto.
func (v VideoConfig) EffectiveCodec() string {
	if v.Codec == "" {
		return CodecAuto
	}
	return v.Codec
}

// codecFromLegacyLZ4 maps the pre-codec lz4_enabled boolean.
func codecFromLegacyLZ4(enabled bool) string {
	if enabled {
		return CodecAuto
	}
	return CodecRaw
}

// migrateVideoCodec maps a sectioned config's legacy lz4_enabled key onto
// codec when codec itself is absent, then clears the legacy field so the
// next save drops it (design §4.4).
func migrateVideoCodec(v *VideoConfig, meta toml.MetaData) {
	legacy := v.LegacyLZ4Enabled
	v.LegacyLZ4Enabled = nil
	if legacy == nil || meta.IsDefined("bridge", "video", "codec") {
		return
	}
	v.Codec = codecFromLegacyLZ4(*legacy)
}
```

- [ ] **Step 4: Change `VideoConfig` in `internal/config/config.go`**

In the sectioned `VideoConfig` struct (not the flat legacy `Config`, which keeps `LZ4Enabled`), replace

```go
	LZ4Enabled          bool   `toml:"lz4_enabled"`
	DeltaLZ4Enabled     bool   `toml:"delta_lz4_enabled"`
```

with

```go
	// Codec selects frame compression: "auto" (default), "raw" or "lz4".
	// Empty means auto; see EffectiveCodec.
	Codec           string `toml:"codec"`
	DeltaLZ4Enabled bool   `toml:"delta_lz4_enabled"` // only used when the codec resolves to LZ4
	// LegacyLZ4Enabled reads the pre-codec lz4_enabled key for
	// migrateVideoCodec. It is always nil after load, so saves omit it.
	LegacyLZ4Enabled *bool `toml:"lz4_enabled,omitempty"`
```

In `Sectioned.Validate`, after the `interlace_filter` switch, add:

```go
	switch b.Video.Codec {
	case "", CodecAuto, CodecRaw, CodecLZ4:
	default:
		return fmt.Errorf("bridge.video.codec must be auto, raw, or lz4, got %q", b.Video.Codec)
	}
```

- [ ] **Step 5: Wire migration in `internal/config/migration.go`**

In `Migrate`, replace `LZ4Enabled: old.LZ4Enabled,` with `Codec: codecFromLegacyLZ4(old.LZ4Enabled),`.
In `defaultBridge`, replace `LZ4Enabled: d.LZ4Enabled,` with `Codec: CodecAuto,`.
In `loadSectionedFromBytes`, after the successful `toml.Decode`, add:

```go
	migrateVideoCodec(&s.Bridge.Video, meta)
```

- [ ] **Step 6: Update `internal/config/example.toml`**

Replace the `lz4_enabled = true ...` line with:

```toml
codec = "auto"                    # "auto" | "raw" | "lz4" — frame compression; auto = LZ4 today (recommended)
```

and change the delta comment to `# Adaptive delta-LZ4 BLITs (recommended; LZ4 only)`.

- [ ] **Step 7: Run the config tests**

Run: `go test ./internal/config`
Expected: the new tests PASS. Fix the existing config tests:
- `internal/config/migration_test.go:133` (`if s.Bridge.Video.LZ4Enabled {`) becomes `if s.Bridge.Video.Codec != CodecAuto {`, matching whatever `lz4_enabled` value that test's legacy input sets (`true` → `CodecAuto`, `false` → `CodecRaw`; read the input to decide).
- Any other `VideoConfig{... LZ4Enabled: ...}` in `internal/config` tests follows the same rule: `true` becomes `Codec: CodecAuto`, `false` becomes `Codec: CodecRaw`.

  **Never just delete an `LZ4Enabled: false` line from a `VideoConfig`.** An empty codec means auto, which is LZ4.
- Update assertions that compare example.toml or migrated output to expect `codec`.

Re-run until PASS. (`TestDefaultBridge_DeltaLZ4Enabled` and `TestSectioned_RoundTripDeltaLZ4Enabled` are about the delta flag and stay unchanged.)

- [ ] **Step 8: Replace the settings switch with a codec select**

`internal/chassis/settings.go`:
- In the decoder map, replace the `"video_lz4_enabled"` entry with:

```go
	"video_codec": func(s string) (any, error) {
		v, err := decodeCodec(s)
		return v, err
	},
```

- In the setter map, replace the `"video_lz4_enabled"` entry with:

```go
	"video_codec":                 func(c *config.BridgeConfig, v any) { c.Video.Codec = v.(string) },
```

- In the scope map, replace `"video_lz4_enabled": adapters.ScopeRestartCast,` with `"video_codec": adapters.ScopeRestartCast,`.
- After `decodeInterlaceFilter`, add:

```go
// decodeCodec accepts "auto", "raw", or "lz4".
func decodeCodec(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	switch s {
	case config.CodecAuto, config.CodecRaw, config.CodecLZ4:
		return s, nil
	}
	return "", fmt.Errorf("must be auto, raw, or lz4")
}
```

`internal/chassis/templates/settings-av.html`: replace the `video_lz4_enabled` switch block with:

```
    {{ field (dict
      "Name" "video_codec" "Type" "select" "Label" "Compression"
      "Help" "How each frame is packed for the MiSTer. Auto picks the best codec the connected core supports (LZ4 today)."
      "Value" .Bridge.Video.EffectiveCodec
      "Scope" "recast"
      "Options" (options
        (dict "Value" "auto" "Label" "Auto (recommended)")
        (dict "Value" "lz4" "Label" "LZ4")
        (dict "Value" "raw" "Label" "Raw (uncompressed)"))
      "Error" (errOf .Errors "video_codec")) }}
```

and change the Delta-LZ4 help to `"Sends only what changed between frames when that is smaller than a full LZ4 frame. Applies only when compression is Auto or LZ4."`. (Spec §4.5 asks for the switch to be disabled for raw; server-rendered help text replaces that in Part 1, and live enable/disable is deferred.)

- [ ] **Step 9: Update the bridge saver**

`internal/uiserver/bridge_saver.go`: replace

```go
	if oldCfg.Video.LZ4Enabled != newCfg.Video.LZ4Enabled {
		keys = append(keys, "video.lz4_enabled")
	}
```

with

```go
	if oldCfg.Video.EffectiveCodec() != newCfg.Video.EffectiveCodec() {
		keys = append(keys, "video.codec")
	}
```

and in the `ScopeRestartCast` case list replace `"video.lz4_enabled",` with `"video.codec",`.

- [ ] **Step 10: Temporary compile shims in core**

These keep the tree building until Task 4 replaces them.
- `internal/core/manager.go`, in the `dataplane.PlaneConfig{...}` literal: `LZ4Enabled: m.bridge.Video.EffectiveCodec() != config.CodecRaw,`.
- `internal/core/meter.go`, in `PipelineMeterView{...}`: `LZ4Enabled: bridge.Video.EffectiveCodec() != config.CodecRaw,`.

- [ ] **Step 11: Fix the remaining test references and run everything**

`VideoConfig` has both composite literals (`LZ4Enabled: true,`) and field assignments (`.Video.LZ4Enabled = true`), and a narrow grep misses the literals. Every one of these must change. Rule: `true` becomes `Codec: config.CodecAuto` (or `config.CodecAuto` in an assignment), `false` becomes `config.CodecRaw`, and a `false` is never deleted:

| File:line (at `57d87f0b`) | Change |
|---|---|
| `internal/core/manager_test.go:46` | `LZ4Enabled: false,` → `Codec: config.CodecRaw,` |
| `internal/core/manager_test.go:598`, `:3438` | `m.bridge.Video.LZ4Enabled = true` → `m.bridge.Video.Codec = config.CodecAuto` |
| `internal/chassis/chassis_test.go:4454` | `LZ4Enabled: true` → `Codec: config.CodecAuto` |
| `internal/chassis/chassis_test.go:4480` | `` `data-field="video_lz4_enabled"`, `` → `` `name="video_codec"`, `` (a select renders `name=`, not `data-field=`) |
| `internal/chassis/settings_test.go:1066` | `LZ4Enabled: true` → `Codec: config.CodecAuto` |
| `internal/uiserver/bridge_saver_test.go:566` | `LZ4Enabled: true,` → `Codec: config.CodecAuto,` |
| `tests/integration/calibration_test.go:108`, `chassis_test.go:2159`, `liveaudio_test.go:61`, `osd_test.go:114`, `scenarios_test.go:112`, `variance_test.go:147` | `LZ4Enabled: true,` → `Codec: config.CodecAuto,` |
| `tests/integration/url_test.go:41`, `user_provider_test.go:141` | `LZ4Enabled: false,` → `Codec: config.CodecRaw,` |

Leave `internal/chassis/meter_test.go:36` (`PipelineMeterView`) and the `PlaneConfig` literals (`tests/integration/plane_test.go:150`, `modeline_*_test.go:135`, `lz4_underrun_test.go:91`, and dataplane tests) for Task 4.

Replace the integration settings test `TestChassisSettings_PipelineLZ4Switch_Recast` (`tests/integration/chassis_test.go` around line 2476) with a codec version:

```go
func TestChassisSettings_PipelineCodec_Recast(t *testing.T) {
	t.Parallel()
	env := newChassisIntegrationEnvForSettings(t, settingsEnvOptions{})
	defer env.Close()

	resp := env.PostForm("/ui/settings/bridge", url.Values{"video_codec": {"raw"}})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, body)
	}
	if got := env.bridgeSaver.Current().Video.Codec; got != config.CodecRaw {
		t.Errorf("after save: Codec = %q, want raw", got)
	}
```

Keep the rest of the original function body (the JSON decode and the `scope == "recast"` assertion) unchanged.

Add to `internal/uiserver/bridge_saver_test.go`, next to `TestScopeForBridgeField_DeltaLZ4RestartCast`:

```go
func TestScopeForBridgeField_CodecRestartCast(t *testing.T) {
	if got := scopeForBridgeField("video.codec"); got != adapters.ScopeRestartCast {
		t.Errorf("scopeForBridgeField(video.codec) = %v, want ScopeRestartCast", got)
	}
}

func TestDiffBridgeConfig_EmptyCodecEqualsAuto(t *testing.T) {
	old := config.BridgeConfig{Video: config.VideoConfig{Codec: ""}}
	newCfg := config.BridgeConfig{Video: config.VideoConfig{Codec: config.CodecAuto}}
	for _, k := range diffBridgeConfig(old, newCfg) {
		if k == "video.codec" {
			t.Fatal("\"\" -> \"auto\" reported as a codec change")
		}
	}
}
```

Then run: `grep -rn "LZ4Enabled" --include=*.go internal cmd tests | grep -v DeltaLZ4Enabled`
Expected: only the flat legacy `internal/config` `Config.LZ4Enabled` (plus its `defaults()`/`migration.go` uses), the `dataplane.PlaneConfig`/`PipelineMeterView` fields and their users, and the Task 3 shims.

Run: `go vet ./... && go vet -tags=integration ./... && go test ./internal/config ./internal/chassis ./internal/uiserver ./internal/core`
Expected: PASS.

If you touched `internal/chassis/static/*.js`, also run `node --test internal/chassis/testdata/*.behavior.test.js`. This task shouldn't need to.

- [ ] **Step 12: Commit**

```bash
git add internal/config/codec.go internal/config/codec_test.go
git commit -m "feat(config): replace lz4_enabled with a codec setting

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" -- internal/config internal/chassis/settings.go internal/chassis/templates/settings-av.html internal/uiserver internal/core/manager.go internal/core/meter.go $(git diff --name-only -- '*_test.go')
```

Check `git show --stat HEAD` for CRLF whole-file rewrites (see Global Constraints).

---

### Task 4: Codec through the data plane, meter and pipe label

**Files:**
- Create: `internal/dataplane/codec.go`, `internal/dataplane/codec_test.go`
- Modify: `internal/dataplane/plane.go` (`PlaneConfig`, `Plane` struct, `NewPlane`, INIT build in `Run`, session-start log, `sendField`, and any other `p.cfg.LZ4Enabled` use)
- Modify: `internal/core/manager.go`, `internal/core/meter.go`, `internal/core/types.go` (`PipelineMeterView`), `internal/chassis/meter.go` (`formatPipe`)
- Tests to update: dataplane, core, chassis and integration tests that set `LZ4Enabled`

**Interfaces:**
- Consumes: `config.VideoConfig.EffectiveCodec()`, `groovy.Core`.
- Produces: `dataplane.Codec` (`CodecAuto`, `CodecRaw`, `CodecLZ4`), `dataplane.ResolveCodec(configured Codec, core groovy.Core) (Codec, string)`, `PlaneConfig.Codec Codec` (replaces `LZ4Enabled`; `""` = raw), `(*Plane).EffectiveCodec() Codec`, `PipelineMeterView.Codec string` (replaces `LZ4Enabled`).

- [ ] **Step 1: Write the failing tests**

`internal/dataplane/codec_test.go`:

```go
package dataplane

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestResolveCodec(t *testing.T) {
	cases := []struct {
		in   Codec
		core groovy.Core
		want Codec
		warn bool
	}{
		{CodecAuto, groovy.CoreGroovy, CodecLZ4, false},
		{CodecAuto, groovy.CoreGroovyNLC, CodecLZ4, false}, // §4.2: NLC opt-in only
		{CodecLZ4, groovy.CoreGroovyNLC, CodecLZ4, false},
		{CodecRaw, groovy.CoreGroovyNLC, CodecRaw, false},
		{"", groovy.CoreGroovy, CodecRaw, false}, // bare PlaneConfig keeps today's raw default
		{"bogus", groovy.CoreGroovy, CodecLZ4, true},
	}
	for _, c := range cases {
		got, warn := ResolveCodec(c.in, c.core)
		if got != c.want || (warn != "") != c.warn {
			t.Errorf("ResolveCodec(%q, %v) = %q, %q; want %q, warn=%v", c.in, c.core, got, warn, c.want, c.warn)
		}
	}
}

func TestInitCompressionByte(t *testing.T) {
	if initCompressionByte(CodecLZ4) != groovy.LZ4ModeDefault || initCompressionByte(CodecRaw) != groovy.LZ4ModeOff {
		t.Fatal("unexpected INIT compression byte")
	}
}
```

Append to the same new `internal/dataplane/codec_test.go`. This keeps the task out of the busy `plane_test.go`; `runPlaneUntilInit(t, cfg PlaneConfig)` lives in `plane_test.go` in the same package and already merges defaults into `cfg`:

```go
func TestPlane_InitCompressionFollowsCodec(t *testing.T) {
	for codec, want := range map[Codec]byte{
		CodecLZ4:  groovy.LZ4ModeDefault,
		CodecAuto: groovy.LZ4ModeDefault,
		CodecRaw:  groovy.LZ4ModeOff,
		"":        groovy.LZ4ModeOff,
	} {
		initCmd, _, _ := runPlaneUntilInit(t, PlaneConfig{Codec: codec})
		if initCmd.Init == nil || initCmd.Init.LZ4Frames != want {
			t.Errorf("codec %q: INIT[1] = %v, want %d", codec, initCmd.Init, want)
		}
	}
}
```


- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/dataplane -run 'TestResolveCodec|TestInitCompressionByte|TestPlane_InitCompressionFollowsCodec'`
Expected: FAIL, `undefined: ResolveCodec`.

- [ ] **Step 3: Implement `internal/dataplane/codec.go`**

```go
package dataplane

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// Codec is a frame compression codec. Configured values mirror
// bridge.video.codec. Effective values (after ResolveCodec) are only
// CodecRaw or CodecLZ4 in Part 1 of design 2026-09-28.
type Codec string

const (
	CodecAuto Codec = "auto"
	CodecRaw  Codec = "raw"
	CodecLZ4  Codec = "lz4"
)

// ResolveCodec maps a configured codec to the codec a session uses
// against core (design 2026-09-28 §4.2). The zero value "" means raw, so
// PlaneConfig literals that set no codec keep the uncompressed default.
// warning is non-empty when the configured codec could not be honoured.
func ResolveCodec(configured Codec, core groovy.Core) (effective Codec, warning string) {
	switch configured {
	case CodecAuto, CodecLZ4:
		return CodecLZ4, ""
	case CodecRaw, "":
		return CodecRaw, ""
	}
	return CodecLZ4, fmt.Sprintf("unknown codec %q on %s core; using lz4", configured, core)
}

// initCompressionByte is INIT byte[1] for an effective codec.
func initCompressionByte(c Codec) byte {
	if c == CodecLZ4 {
		return groovy.LZ4ModeDefault
	}
	return groovy.LZ4ModeOff
}
```

- [ ] **Step 4: Thread the codec through `plane.go`**

1. In `PlaneConfig`, replace `LZ4Enabled bool` with:

```go
	Codec               Codec // configured codec; "" = raw. Resolved per session by ResolveCodec.
```

2. In the `Plane` struct, next to `deltaLZ4Enabled`, add:

```go
	// codec is the session's effective codec. It is provisional from
	// NewPlane (resolved against CoreUnknown) and final once Run has
	// probed the core. Tick-goroutine owned; effCodec and core publish
	// it for cross-goroutine readers.
	codec    Codec
	effCodec atomic.Value // Codec
	core     atomic.Uint32 // groovy.Core
```

3. In `NewPlane`, replace

```go
	deltaLZ4Enabled := cfg.LZ4Enabled && deltaLZ4Requested
	if deltaLZ4Enabled {
		...
	} else if deltaLZ4Requested && !cfg.LZ4Enabled {
```

with

```go
	codec, _ := ResolveCodec(cfg.Codec, groovy.CoreUnknown)
	deltaLZ4Enabled := codec == CodecLZ4 && deltaLZ4Requested
	if deltaLZ4Enabled {
		...
	} else if deltaLZ4Requested && codec != CodecLZ4 {
```

(keep the two `slog.Info` bodies, and change the second message to `"adaptive delta-LZ4 requested but codec is not LZ4"`). In the `p := &Plane{...}` literal add `codec: codec,`. After the literal add `p.effCodec.Store(codec)`.

4. Add the accessors after `Done()`:

```go
// EffectiveCodec returns the session's codec: provisional until Run has
// probed the core, then final. Safe for concurrent use.
func (p *Plane) EffectiveCodec() Codec {
	c, _ := p.effCodec.Load().(Codec)
	return c
}

// Core returns the MiSTer core detected by Run's GET_VERSION probe, or
// groovy.CoreUnknown before the probe. Safe for concurrent use.
func (p *Plane) Core() groovy.Core { return groovy.Core(p.core.Load()) }
```

5. In `Run`, replace

```go
	lz4Mode := groovy.LZ4ModeOff
	if p.cfg.LZ4Enabled {
		lz4Mode = groovy.LZ4ModeDefault
	}
	initPkt := groovy.BuildInit(lz4Mode, soundRate, byte(audioChans), p.cfg.RGBMode)
```

with

```go
	initPkt := groovy.BuildInit(initCompressionByte(p.codec), soundRate, byte(audioChans), p.cfg.RGBMode)
```

In the `"dataplane session started"` log, replace `"lz4_enabled", p.cfg.LZ4Enabled,` with `"codec", string(p.codec),`.

6. In `sendField`, replace `if p.cfg.LZ4Enabled {` with `if p.codec == CodecLZ4 {`, and update its doc comment ("if LZ4 is enabled" → "if the codec is LZ4").

7. In `holdField` (added by `57d87f0b`), replace `if p.cfg.LZ4Enabled {` with `if p.codec != CodecRaw {`, and update its comment to say "any compressed codec". Both cores misread a dup header whenever compression is on (spec §1.3), and Part 2's NLC needs the same hold.
8. Run `grep -n "cfg.LZ4Enabled" internal/dataplane/*.go`. Replace every remaining production hit with `p.codec == CodecLZ4`. Log keys named `"lz4_enabled"` become `"codec", string(p.codec)`.

- [ ] **Step 5: Core and chassis**

- `internal/core/manager.go`, in the `dataplane.PlaneConfig{...}` literal: replace the Task 3 shim with `Codec: dataplane.Codec(m.bridge.Video.EffectiveCodec()),`.
- `internal/core/types.go`, `PipelineMeterView`: replace `LZ4Enabled bool` with `Codec string // effective codec: "raw" | "lz4"`.
- `internal/core/meter.go`: replace the Task 3 shim with `Codec: string(provisionalCodec(bridge)),` and add:

```go
// provisionalCodec is the codec a session will use before the core probe
// can refine it. In Part 1 the core never changes the answer (§4.2).
func provisionalCodec(bridge config.BridgeConfig) dataplane.Codec {
	c, _ := dataplane.ResolveCodec(dataplane.Codec(bridge.Video.EffectiveCodec()), groovy.CoreUnknown)
	return c
}
```

(add the `internal/dataplane` import).
- `internal/chassis/meter.go` `formatPipe`: replace the codec switch with

```go
	codec := "RAW"
	switch {
	case pipe.Codec == "lz4" && pipe.DeltaLZ4Enabled:
		codec = "LZ4+D"
	case pipe.Codec == "lz4":
		codec = "LZ4"
	}
```

- [ ] **Step 6: Update test references**

Run: `grep -rn "LZ4Enabled" --include=*.go internal cmd tests | grep -v DeltaLZ4Enabled`
Task 3 already converted every `config.VideoConfig` literal. The remaining hits are `dataplane.PlaneConfig` and `core.PipelineMeterView` literals:
- In `PlaneConfig` literals in `dataplane` package tests (including `deltapolicy_test.go`, `deltaresync_test.go`, `hold_test.go`, `lz4mode_test.go`, `plane_test.go`), `LZ4Enabled: true` becomes `Codec: CodecLZ4`. For `PlaneConfig` **only**, delete `LZ4Enabled: false` lines: the `PlaneConfig` zero value `""` is raw.
- Tests that set the field after construction (`cfg.LZ4Enabled = true`, `p.cfg.LZ4Enabled = ...`) become `cfg.Codec = CodecLZ4`. If such a test builds a bare `&Plane{cfg: ...}` without `NewPlane`, also set `p.codec = CodecLZ4`, because `sendField` and `holdField` read `p.codec`.
- In `PlaneConfig` literals in `tests/integration` (`plane_test.go:150`, `modeline_ntsc240p_test.go:135`, `modeline_pal288p_test.go:135`, `modeline_pal576i_test.go:135`, `lz4_underrun_test.go:91`), `LZ4Enabled: true` becomes `Codec: dataplane.CodecLZ4`.
- In `internal/chassis/meter_test.go:36`, `PipelineMeterView{... LZ4Enabled: true ...}` becomes `Codec: "lz4"`.
- In `internal/core/manager_test.go` near line 627, an assertion on `captured.LZ4Enabled` (if present) becomes `captured.Codec != dataplane.CodecAuto`.

The only remaining hits should be the flat legacy `config.Config.LZ4Enabled` in `internal/config`.

- [ ] **Step 7: Run tests**

Run: `go vet ./... && go vet -tags=integration ./... && go test ./internal/dataplane ./internal/core ./internal/chassis ./internal/config`
Expected: PASS.
Run: `go test -tags=integration ./tests/integration/... -run 'Plane|NTSC240p|PAL'`
Expected: PASS (this needs ffmpeg).

- [ ] **Step 8: Commit**

```bash
git add internal/dataplane/codec.go internal/dataplane/codec_test.go
git commit -m "refactor(dataplane): carry a resolved codec instead of an LZ4 flag

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" -- internal/dataplane internal/core internal/chassis/meter.go $(git diff --name-only -- '*_test.go')
```

---

### Task 5: Probe, keepalive, FrameEcho==0 filter and prebuffer ACK drain in `Plane.Run`

**Files:**
- Create: `internal/dataplane/keepalive.go`, `internal/dataplane/keepalive_test.go`, `internal/dataplane/core_session_test.go`
- Modify: `internal/dataplane/plane.go` (`PlaneConfig`, `Run`, `prebuffer`, tick-loop ACK case)
- Modify: `internal/dataplane/plane_test.go` (8 `p.prebuffer(` call sites)
- Modify: `tests/integration/helper_test.go`, `tests/integration/plane_test.go`, `tests/integration/modeline_ntsc240p_test.go`, `tests/integration/modeline_pal288p_test.go`, `tests/integration/modeline_pal576i_test.go`

**Interfaces:**
- Consumes: `(*groovynet.Sender).ProbeVersion`, `.SendIfIdle`, `groovy.CoreFromVersion`, `groovy.BuildGetStatus`, `ResolveCodec`, fake-mister `SetCoreVersion`, `SetIdleTimeout`, `Reaps`.
- Produces: `PlaneConfig.KeepaliveIdle time.Duration` (0 = 2 s), `echoAdvanced(a groovy.ACK, lastEcho uint32) bool`, `runKeepalive(s keepaliveSender, idle time.Duration, stop <-chan struct{})`, the prebuffer signature `prebuffer(ctx, procDone, videoCh, audioCh, acks <-chan groovy.ACK, onACK func(groovy.ACK), target, timeout)`, and the integration helper `answerInitHandshake(conn *net.UDPConn, status byte) (ok bool)`.

- [ ] **Step 1: Write the failing unit tests**

`internal/dataplane/keepalive_test.go`:

```go
package dataplane

import (
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

type recordingSender struct {
	mu   sync.Mutex
	last time.Time
	sent [][]byte
}

// SendIfIdle mirrors groovynet.Sender.SendIfIdle: check and write under
// one lock.
func (r *recordingSender) SendIfIdle(p []byte, idle time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.last.IsZero() && time.Since(r.last) < idle {
		return false, nil
	}
	r.sent = append(r.sent, append([]byte(nil), p...))
	r.last = time.Now()
	return true, nil
}

func (r *recordingSender) touch() {
	r.mu.Lock()
	r.last = time.Now()
	r.mu.Unlock()
}

func (r *recordingSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func TestKeepalive_SendsGetStatusAfterSilence(t *testing.T) {
	s := &recordingSender{last: time.Now()}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { runKeepalive(s, 40*time.Millisecond, stop); close(done) }()
	time.Sleep(150 * time.Millisecond)
	close(stop)
	<-done
	if s.count() < 1 {
		t.Fatal("no keepalive sent after silence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.sent {
		if len(p) != 1 || p[0] != groovy.CmdGetStatus {
			t.Fatalf("keepalive sent %v, want GET_STATUS", p)
		}
	}
}

func TestKeepalive_QuietWhileTrafficFlows(t *testing.T) {
	s := &recordingSender{last: time.Now()}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { runKeepalive(s, 60*time.Millisecond, stop); close(done) }()
	for i := 0; i < 10; i++ {
		time.Sleep(15 * time.Millisecond)
		s.touch()
	}
	close(stop)
	<-done
	if n := s.count(); n != 0 {
		t.Fatalf("keepalive sent %d datagrams during steady traffic", n)
	}
}

func TestEchoAdvanced_IgnoresZeroEcho(t *testing.T) {
	if echoAdvanced(groovy.ACK{FrameEcho: 0}, 57) {
		t.Fatal("FrameEcho==0 ACK (INIT/SWITCHRES/GET_STATUS) counted as echo movement")
	}
	if !echoAdvanced(groovy.ACK{FrameEcho: 58}, 57) {
		t.Fatal("real echo advance not detected")
	}
	if echoAdvanced(groovy.ACK{FrameEcho: 57}, 57) {
		t.Fatal("unchanged echo counted as movement")
	}
}
```

`internal/dataplane/core_session_test.go`:

```go
package dataplane

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// slowStartSource withholds its first frame for delay, then fills frames
// instantly, which models a slow transcode start inside the prebuffer.
type slowStartSource struct {
	delay   time.Duration
	started atomic.Bool
}

func (s *slowStartSource) ReadFrame(dst []byte) {
	if !s.started.Swap(true) {
		time.Sleep(s.delay)
	}
	for i := range dst {
		dst[i] = 0x33
	}
}

type coreSession struct {
	listener *fakemister.Listener
	fields   chan fakemister.FieldEvent
	plane    *Plane
	cancel   context.CancelFunc
	runErr   chan error
}

// startCoreSession runs a Frames plane against a fake MiSTer that
// impersonates the given core version.
func startCoreSession(t *testing.T, version byte, idleTimeout time.Duration, cfg PlaneConfig) *coreSession {
	t.Helper()
	l, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	l.EnableACKs(false)
	l.SetCoreVersion(version)
	l.SetIdleTimeout(idleTimeout)
	sender, err := groovynet.NewSender("127.0.0.1", l.Addr().(*net.UDPAddr).Port, 0)
	requireUDPSockets(t, err)

	const fieldBytes = 4 * 1 * 1
	cmds := make(chan fakemister.Command, 4096)
	fields := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 8)
	go l.RunWithFields(cmds, fields, audios, func() uint32 { return fieldBytes })

	cfg.Sender = sender
	cfg.Modeline = groovy.NTSC480i60
	cfg.FieldWidth, cfg.FieldHeight, cfg.BytesPerPixel = 4, 1, 1
	cfg.RGBMode = groovy.RGBMode888
	plane := NewPlane(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- plane.Run(ctx) }()
	cs := &coreSession{listener: l, fields: fields, plane: plane, cancel: cancel, runErr: runErr}
	t.Cleanup(func() {
		cancel()
		if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Plane.Run() = %v", err)
		}
		_ = sender.Close()
		_ = l.Close()
	})
	return cs
}

func (cs *coreSession) waitField(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-cs.fields:
	case <-time.After(within):
		t.Fatalf("no field within %v (reaps=%d)", within, cs.listener.Reaps())
	}
}

func TestPlane_DetectsGroovyNLC(t *testing.T) {
	cs := startCoreSession(t, 2, 0, PlaneConfig{Codec: CodecAuto, Frames: &fillSource{}})
	cs.waitField(t, 2*time.Second)
	if cs.plane.Core() != groovy.CoreGroovyNLC {
		t.Fatalf("Core() = %v, want groovynlc", cs.plane.Core())
	}
	if cs.plane.EffectiveCodec() != CodecLZ4 {
		t.Fatalf("EffectiveCodec() = %q, want lz4 (auto stays LZ4, §4.2)", cs.plane.EffectiveCodec())
	}
}

func TestPlane_DetectsOriginalGroovy(t *testing.T) {
	cs := startCoreSession(t, 1, 0, PlaneConfig{Codec: CodecAuto, Frames: &fillSource{}})
	cs.waitField(t, 2*time.Second)
	if cs.plane.Core() != groovy.CoreGroovy {
		t.Fatalf("Core() = %v, want groovy", cs.plane.Core())
	}
}

// Design §1.2 / §3.3: the prebuffer is the only silent window. With the
// keepalive, a fork core with a short idle timeout keeps the session.
func TestPlane_KeepaliveSurvivesSlowPrebuffer(t *testing.T) {
	cs := startCoreSession(t, 2, 400*time.Millisecond, PlaneConfig{
		Frames:        &slowStartSource{delay: 1500 * time.Millisecond},
		KeepaliveIdle: 100 * time.Millisecond,
	})
	cs.waitField(t, 4*time.Second)
	if n := cs.listener.Reaps(); n != 0 {
		t.Fatalf("session reaped %d times despite keepalive", n)
	}
}

// Negative control: the same scenario without a keepalive is reaped,
// which proves the test above is sensitive.
func TestPlane_NoKeepaliveIsReapedDuringSlowPrebuffer(t *testing.T) {
	cs := startCoreSession(t, 2, 400*time.Millisecond, PlaneConfig{
		Frames:        &slowStartSource{delay: 1500 * time.Millisecond},
		KeepaliveIdle: time.Hour,
	})
	deadline := time.Now().Add(4 * time.Second)
	for cs.listener.Reaps() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if cs.listener.Reaps() == 0 {
		t.Fatal("expected the idle core to reap a silent prebuffer")
	}
}
```

(`fillSource` already exists in `framesource_test.go`.)

- [ ] **Step 2: Run and confirm they fail**

Run: `go test ./internal/dataplane -run 'TestKeepalive|TestEchoAdvanced|TestPlane_Detects|TestPlane_Keepalive|TestPlane_NoKeepalive'`
Expected: FAIL, `undefined: runKeepalive`.

- [ ] **Step 3: Implement `internal/dataplane/keepalive.go`**

```go
package dataplane

import (
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// defaultKeepaliveIdle is how long the plane may send nothing before the
// keepalive sends GET_STATUS. It is below GroovyNLC's shortest idle
// timeout (5 s). See design 2026-09-28 §3.3.
const defaultKeepaliveIdle = 2 * time.Second

// keepaliveSender is the part of groovynet.Sender the keepalive uses.
// SendIfIdle checks and writes under the Sender's lock, so a keepalive can
// never land between a BLIT/AUDIO header and its payload.
type keepaliveSender interface {
	SendIfIdle(pkt []byte, idle time.Duration) (bool, error)
}

// runKeepalive sends a 1-byte GET_STATUS whenever nothing has been sent
// for idle, polling at idle/4, until stop closes. It fires in the prebuffer
// (design §1.2), and during LZ4 underrun holds, where holdField sends
// nothing. Both cores answer with a FrameEcho==0 ACK, which echoAdvanced
// ignores.
func runKeepalive(s keepaliveSender, idle time.Duration, stop <-chan struct{}) {
	poll := idle / 4
	if poll <= 0 {
		poll = time.Millisecond
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if _, err := s.SendIfIdle(groovy.BuildGetStatus(), idle); err != nil {
				slog.Debug("keepalive send failed", "err", err)
			}
		}
	}
}

// echoAdvanced reports whether an ACK moves the frame echo. INIT,
// SWITCHRES (GroovyNLC) and GET_STATUS ACKs all carry FrameEcho 0, and
// frameNum starts at 1, so an echo of 0 is never a real frame (§3.4).
func echoAdvanced(a groovy.ACK, lastEcho uint32) bool {
	return a.FrameEcho != 0 && a.FrameEcho != lastEcho
}
```

- [ ] **Step 4: Add `KeepaliveIdle` to `PlaneConfig`**

After `OnInit`:

```go
	// KeepaliveIdle overrides how long the plane may send nothing before
	// the keepalive sends GET_STATUS. 0 = defaultKeepaliveIdle (2 s).
	// Tests shorten it.
	KeepaliveIdle time.Duration
```

- [ ] **Step 5: Probe and resolve the codec in `Run`**

Add a constant next to `defaultAudioDelay`:

```go
	coreProbeTimeout = 200 * time.Millisecond
```

In `Run`, immediately before the `initPkt := ...` line from Task 4, insert:

```go
	// 0. Core probe (design 2026-09-28 §3.1). Same socket contract as
	//    INIT: it runs before the Drainer starts. It runs on every session,
	//    because the user may switch cores between casts.
	coreVersion, err := p.cfg.Sender.ProbeVersion(coreProbeTimeout)
	if err != nil {
		if p.cfg.OnInit != nil {
			p.cfg.OnInit(err)
		}
		return fmt.Errorf("core version probe: %w", err)
	}
	core := groovy.CoreFromVersion(coreVersion)
	p.core.Store(uint32(core))
	codec, codecWarning := ResolveCodec(p.cfg.Codec, core)
	if codecWarning != "" {
		slog.Warn("video codec fallback", "reason", codecWarning, "core", core.String())
	}
	if codec != CodecLZ4 {
		// Only reachable from a provisional LZ4 (Part 2 may do this); drop
		// delta so the raw path never consults LZ4 history.
		p.deltaLZ4Enabled = false
	}
	p.codec = codec
	p.effCodec.Store(codec)
```

Leave the later `ack, err := p.cfg.Sender.SendInitAwaitACKWithRetry(...)` as it is: `ack` is new, so `:=` still compiles. Add `"core", core.String(), "core_version", coreVersion,` to the `"dataplane session started"` log.

- [ ] **Step 6: Start the keepalive after the INIT handshake**

Right after `p.lastACKUnix.Store(time.Now().UnixNano())`, which follows the INIT success, insert:

```go
	keepaliveIdle := p.cfg.KeepaliveIdle
	if keepaliveIdle <= 0 {
		keepaliveIdle = defaultKeepaliveIdle
	}
	keepaliveStop := make(chan struct{})
	keepaliveDone := make(chan struct{})
	go func() {
		defer close(keepaliveDone)
		runKeepalive(p.cfg.Sender, keepaliveIdle, keepaliveStop)
	}()
	defer func() {
		close(keepaliveStop)
		<-keepaliveDone
	}()
```

- [ ] **Step 7: Filter FrameEcho==0 in the tick-loop ACK case**

Replace `if a.FrameEcho != lastEcho {` with `if echoAdvanced(a, lastEcho) {`.

- [ ] **Step 8: Drain ACKs during the prebuffer**

Change the `prebuffer` signature to:

```go
func (p *Plane) prebuffer(
	ctx context.Context,
	procDone <-chan struct{},
	videoCh <-chan *FrameBuf,
	audioCh <-chan []byte,
	acks <-chan groovy.ACK,
	onACK func(groovy.ACK),
	target int,
	timeout time.Duration,
) (video []*FrameBuf, audio [][]byte, exitReason string) {
```

and add a select case in its loop:

```go
		case a := <-acks:
			// SWITCHRES (GroovyNLC) and keepalive ACKs arrive here; draining
			// keeps ackCh from filling (design §3.4). A nil acks never fires.
			if onACK != nil {
				onACK(a)
			}
```

In `Run`, pass the drainer channel and a handler:

```go
	videoPrebuffer, audioPrebuffer, prebufferExit := p.prebuffer(
		ctx, proc.Done(), videoCh, audioCh, ackCh, func(a groovy.ACK) {
			p.audioReady.Store(audioEnabled && a.AudioReady())
			p.fpgaFrame.Store(a.FPGAFrame)
			p.lastACKUnix.Store(time.Now().UnixNano())
		}, prebufferTarget, prebufferTimeout)
```

Update the 8 test call sites in `plane_test.go`: `p.prebuffer(ctx, procDone, videoCh, audioCh, N, d)` becomes `p.prebuffer(ctx, procDone, videoCh, audioCh, nil, nil, N, d)`.

- [ ] **Step 9: Run the dataplane tests**

Run: `go test ./internal/dataplane`
Expected: PASS. The new tests pass, and existing Run tests still pass: fake-mister answers GET_VERSION when ACKs are enabled, so no test waits for the probe timeout. If a test with its own raw INIT stub now times out, apply the helper pattern from Step 10 to that stub.

- [ ] **Step 10: Make the integration INIT repliers tolerate the probe**

These four one-shot repliers read the first datagram and give up unless it is INIT. The probe is now first. Add to `tests/integration/helper_test.go`:

```go
// answerInitHandshake serves the bridge's pre-INIT handshake on conn: it
// answers GET_VERSION with 1 (the original core), then ACKs the first INIT
// with status, and returns. It returns false on a 2 s read timeout.
func answerInitHandshake(conn *net.UDPConn, status byte) bool {
	defer conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 64)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return false
		}
		switch {
		case n == 1 && buf[0] == groovy.CmdGetVersion:
			_, _ = conn.WriteToUDP([]byte{1}, src)
		case n > 0 && buf[0] == groovy.CmdInit:
			ack := make([]byte, groovy.ACKPacketSize)
			ack[12] = status
			_, _ = conn.WriteToUDP(ack, src)
			return true
		}
	}
}
```

(add `net`, `time` and `groovy` imports if missing). In `tests/integration/plane_test.go`, `modeline_ntsc240p_test.go`, `modeline_pal288p_test.go` and `modeline_pal576i_test.go`, replace the body of the one-shot `ackDone` goroutine (the read/`!= groovy.CmdInit`/write block) with:

```go
		defer close(ackDone)
		answerInitHandshake(l.Conn(), 1<<6)
```

keeping each file's original status byte (check whether it sets bit 6). The replaced block was the only user of `encoding/binary` in those four files (`plane_test.go:104-107`, `modeline_*_test.go:90-93`), so delete the `"encoding/binary"` import from each, or the package won't compile. Confirm with `go vet -tags=integration ./tests/integration/...`.

- [ ] **Step 11: Run the integration suite**

Run: `go test -tags=integration ./tests/integration/...`
Expected: PASS (needs ffmpeg/ffprobe on PATH). Integration flakes on Windows are known; re-run a single failure with `-count=1 -run <Name>` before investigating.

- [ ] **Step 12: Commit**

```bash
git add internal/dataplane/keepalive.go internal/dataplane/keepalive_test.go internal/dataplane/core_session_test.go
git commit -m "feat(dataplane): probe the MiSTer core and keep the prebuffer alive

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" -- internal/dataplane tests/integration
```

---

### Task 6: Surface the core and effective codec

**Files:**
- Modify: `internal/core/manager.go` (`planeRunner`, `StatusHomeView` builder), `internal/core/types.go` (`PipelineMeterView`, `StatusHomeView`)
- Modify: `internal/core/manager_test.go` (plane fakes: `fakePlane`, `contextDonePlane`, `blockingDonePlane`, `errorPlane`, `linkHealthPlane`, plus any other `planeRunner` implementers the compiler reports)
- Modify: `internal/chassis/meter.go` (readout build + JSON), `internal/chassis/events.go` (`meterChanged`), `internal/chassis/templates/meter.html`, `internal/chassis/static/meter.js`
- Test: `internal/core/manager_test.go`, `internal/chassis/meter_test.go`

**Interfaces:**
- Consumes: `(*dataplane.Plane).Core()`, `.EffectiveCodec()`.
- Produces: `planeRunner.Core() groovy.Core`, `planeRunner.EffectiveCodec() dataplane.Codec`, `PipelineMeterView.MisterCore string`, `ReadoutIdleData.Core string` (JSON `core`), `formatCore(core string) string`.

- [ ] **Step 1: Write the failing tests**

In `internal/core/manager_test.go`, add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"` to the imports (the file doesn't import it yet), then add a fake that reports a core and codec, and a test:

```go
type identityPlane struct {
	fakePlane
	core  groovy.Core
	codec dataplane.Codec
}

func (f *identityPlane) Core() groovy.Core                { return f.core }
func (f *identityPlane) EffectiveCodec() dataplane.Codec { return f.codec }

func TestStatusHomeView_ReportsPlaneCoreAndCodec(t *testing.T) {
	m := newTestManager(t)
	m.mu.Lock()
	m.plane = &identityPlane{core: groovy.CoreGroovyNLC, codec: dataplane.CodecRaw}
	m.mu.Unlock()
	view := m.StatusHomeView()
	if view.Meter.Pipeline.MisterCore != "groovynlc" {
		t.Fatalf("Pipeline.MisterCore = %q, want groovynlc", view.Meter.Pipeline.MisterCore)
	}
	if view.Meter.Pipeline.Codec != "raw" {
		t.Fatalf("Pipeline.Codec = %q, want raw (from the plane)", view.Meter.Pipeline.Codec)
	}
}
```


In `internal/chassis/meter_test.go`:

```go
func TestFormatCore(t *testing.T) {
	for in, want := range map[string]string{"groovy": "GROOVY", "groovynlc": "GROOVYNLC", "unknown": "", "": ""} {
		if got := formatCore(in); got != want {
			t.Errorf("formatCore(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/core ./internal/chassis -run 'TestStatusHomeView_ReportsPlaneCoreAndCodec|TestFormatCore'`
Expected: FAIL (compile errors: `MisterCore` and `formatCore` undefined).

- [ ] **Step 3: Core plumbing**

- `planeRunner`: add

```go
	Core() groovy.Core
	EffectiveCodec() dataplane.Codec
```

  and add both methods to each fake in `manager_test.go` (`func (f *fakePlane) Core() groovy.Core { return groovy.CoreUnknown }` and `func (f *fakePlane) EffectiveCodec() dataplane.Codec { return "" }`; same for the other fakes). Fakes that embed `fakePlane` inherit them.
- `types.go`: in `PipelineMeterView` add `MisterCore string // "groovy" | "groovynlc"; empty until the plane has probed`. (No separate `StatusHomeView` field: Part 1 surfaces the core through the meter readout and logs only; see the spec §3.5 amendment in Task 7.)
- `manager.go` `StatusHomeView()`, inside `if m.plane != nil {`, after `linkHealth := m.plane.LinkHealth()`:

```go
		if codec := m.plane.EffectiveCodec(); codec != "" {
			view.Meter.Pipeline.Codec = string(codec)
		}
		if core := m.plane.Core(); core != groovy.CoreUnknown {
			view.Meter.Pipeline.MisterCore = core.String()
		}
```

- [ ] **Step 4: Chassis readout**

- `ReadoutIdleData` (`internal/chassis/data.go:197`): add `Core string`.
- `meter.go`, after `base.Readout.Pipe = formatPipe(pipe)`: `base.Readout.Core = formatCore(pipe.MisterCore)`. Add:

```go
// formatCore labels the detected MiSTer core for the pipe readout's
// tooltip. Empty until the plane has probed.
func formatCore(core string) string {
	switch core {
	case "groovy":
		return "GROOVY"
	case "groovynlc":
		return "GROOVYNLC"
	}
	return ""
}
```

- The meter JSON readout struct (`meterReadoutE`, which has `Pipe string json:"pipe"`): add `Core string json:"core"` and set `Core: m.Readout.Core,` in the builder that sets `Pipe: m.Readout.Pipe,`.
- `events.go` `meterChanged`: add `curr.Readout.Core != last.Readout.Core ||` next to the Pipe comparison.
- `templates/meter.html` line with `data-meter-pipe`: add `title="{{if .Readout.Core}}MiSTer core: {{.Readout.Core}}{{end}}"` to the `seg-text` span.
- `static/meter.js`, after `setText('[data-meter-pipe]', readout.pipe);`:

```js
    const pipeEl = document.querySelector('[data-meter-pipe]');
    if (pipeEl) pipeEl.title = readout.core ? 'MiSTer core: ' + readout.core : '';
```

  (`meter.js` uses `const`. If `setText` takes a root element rather than `document`, query from that same root.)

- [ ] **Step 5: Run tests**

Run: `go vet ./... && go test ./internal/core ./internal/chassis`
Expected: PASS.
Run: `node --check internal/chassis/static/meter.js`, then `node --test internal/chassis/testdata/*.behavior.test.js`.
Expected: no syntax errors; the behaviour tests PASS.

- [ ] **Step 6: Commit**

```bash
git commit -m "feat(ui): show the detected MiSTer core and effective codec

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" -- internal/core internal/chassis
```

---

### Task 7: README, spec touch-up and full verification

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-28-groovynlc-support-design.md` (§4.5 and §6.4 notes)

- [ ] **Step 1: README section**

Add a `## GroovyNLC core` section before `## License`:

```markdown
## GroovyNLC core

The bridge works with both the original [Groovy_MiSTer](https://github.com/psakhis/Groovy_MiSTer)
core and the [GroovyNLC fork](https://github.com/verbst/Groovy_MiSTer). It asks the core for its
version at the start of every cast and logs the answer (`core=groovy` or `core=groovynlc` on the
`dataplane session started` line). The receiver meter's pipe readout shows it on hover.

To use GroovyNLC:

1. Install the fork's `.rbf` and `MiSTer_groovyNLC` binary, and add to `MiSTer.ini`:
   ```ini
   [GroovyNLC]
   main=MiSTer_groovyNLC
   ```
2. Launch GroovyNLC from the MiSTer menu. The UI's "Launch Groovy" button always starts the
   stock core.

3. Leave the core's OSD **Volatile framebuffer** option **Off**. GroovyNLC's NLC codec needs it
   off, and it does no harm with LZ4.

While a cast starts up, or while it waits out a stall, the bridge sends a status ping after
every 2 s of silence. This stops GroovyNLC v1.1–v1.3 from closing the session.

`bridge.video.codec` controls frame compression:

| Value | Behaviour |
|-------|-----------|
| `auto` (default) | LZ4 on both cores today. |
| `lz4` | Always LZ4. |
| `raw` | Uncompressed. |

Configs that still say `lz4_enabled` are migrated automatically: `true` becomes `auto`, `false`
becomes `raw`. `auto` may pick GroovyNLC's NLC codec in a future release, once that codec is
verified on hardware. To keep LZ4 regardless, set `codec = "lz4"`.
```

In `## License`, replace "several GPL-3 references (plexdlnaplayer, plex-mpv-shim, Groovy_MiSTer)" with "several GPL references (plexdlnaplayer and plex-mpv-shim under GPL-3, and the Groovy_MiSTer protocol, which is GPL-2)". Leave the rest of the sentence as it is.

- [ ] **Step 2: Spec notes**

In the spec:
- §4.5: add "Part 1: the delta switch stays enabled for every codec, and its help text states it only applies to LZ4. Live enable/disable is deferred."
- §6.4: add "Part 1 implemented the core-detection and prebuffer-keepalive checks as ffmpeg-free `internal/dataplane` tests (`core_session_test.go`) using fake-mister, not in `tests/integration`."
- §3.5: replace the "status snapshot and the SSE status event, as `mister_core` and `video_codec`" bullet with: "Part 1 surfaces the detected core in the plane start log and in the meter (`PipelineMeterView.MisterCore`, and the meter SSE JSON `readout.core`, shown as the pipe readout's tooltip). The effective codec reaches the meter's pipe label through `PipelineMeterView.Codec`. No separate status-snapshot field was added."
- §3.1: change the signature to `ProbeVersion(timeout) (byte, error)`. Add: "`SendInitAwaitACK` skips non-13-byte datagrams, so a GET_VERSION reply that arrives late cannot fail the handshake."
- §3.3: replace "records the time of every datagram it sends … sends a 1-byte `GET_STATUS`" with: "The Sender stamps every successful write. The keepalive calls `Sender.SendIfIdle(GET_STATUS, 2s)`, which checks the stamp and writes under the Sender's mutex, so a keepalive can never land between a BLIT/AUDIO header and its payload. Since `57d87f0b`, LZ4 underrun holds send nothing, and the keepalive covers those too."

- [ ] **Step 3: Full verification**

Run each command and read its output:

```bash
go vet ./...
go test ./...
go test -tags=integration ./tests/integration/...
go test ./internal/dataplane -run 'TestPlane_Keepalive|TestPlane_NoKeepalive|TestPlane_Detects' -count=10
```

Expected: all PASS. The `-count=10` run checks the timing-based tests for flakiness. If they flake on Windows, raise the slow-start delay and idle timeout proportionally, keeping `delay > 3 × idleTimeout` and `KeepaliveIdle < idleTimeout / 3`.

Run: `grep -rn "LZ4Enabled" --include=*.go internal cmd tests | grep -v DeltaLZ4Enabled`
Expected: only the flat legacy `Config.LZ4Enabled` in `internal/config` (plus `migration.go` reading it).

`go test -race` runs in CI only.

- [ ] **Step 4: Commit**

```bash
git add -f docs/superpowers/specs/2026-09-28-groovynlc-support-design.md
git commit -m "docs: document GroovyNLC support and the codec setting

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>" -- README.md docs/superpowers/specs/2026-09-28-groovynlc-support-design.md
```

---

## Out of scope for this plan (Part 2)

The NLC encoder, the `nlc` codec value, `nlc_near` / `nlc_pack`, golden vectors, fake-mister NLC decoding, NLC underrun behaviour, and moving codec-dependent allocation fully into `Run`. See spec §5, §6.1 and §7.
