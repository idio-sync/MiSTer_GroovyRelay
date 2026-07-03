// Package groovynet provides the UDP transport for the Groovy protocol:
// a Sender that binds a stable source port, slices payloads at MTU, and a
// Drainer (see drainer.go) that non-blockingly collects ACKs from the MiSTer.
//
// INIT is the ONE ack-gated handshake (60 ms timeout); every other command
// is fire-and-forget at the transport level. Callers MUST call
// SendInitAwaitACK before starting the Drainer on the same socket —
// otherwise the Drainer will race the handshake read and swallow the ACK.
package groovynet

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

const wantSndBuf = 2 * 1024 * 1024

// Sender owns a single UDP4 socket bound to srcPort (ephemeral if srcPort=0)
// and addresses every write at dstAddr. A Sender is safe for concurrent use:
// Send, SendPayload, MarkBlitSent, and WaitForCongestion serialise through mu.
// The Drainer reads on the same socket AFTER SendInitAwaitACK has completed.
type Sender struct {
	conn    *net.UDPConn
	dstAddr *net.UDPAddr
	srcPort int

	mu           sync.Mutex // serialises Writes + Mark*
	lastBlitSize int
	lastBlitTime time.Time
	// payloadSendStart is stamped at SendPayload entry so MarkBlitSent can
	// anchor the congestion window at the START of the blit's payload send.
	// The reference sender (MiSTerCast groovymister.cpp) anchors at the end
	// of the send, but it has no per-chunk pacing; with pacing enabled an
	// end anchor stacks the full congestion wait on top of the paced send,
	// pushing the catch-up floor past one NTSC field period so a late pump
	// can never recover. A start anchor keeps the inter-blit interval at
	// K_CONGESTION_TIME without the stack, and with pacing disabled differs
	// from the reference only by the (short) unpaced send duration.
	payloadSendStart time.Time

	sndBufActual int           // populated by readSndBuf at NewSender; 0 on unsupported platforms
	enobufCount  atomic.Uint64 // populated in Task 8

	// paceInterval is an optional inter-chunk delay applied between
	// consecutive WriteToUDP calls inside SendPayload. Defaults to 0
	// (no pacing — chunks are sent back-to-back at line rate, matching
	// MiSTerCast's behavior). Setting this to a small positive value
	// (e.g., 10 µs) spreads the field's burst over a few ms, giving the
	// MiSTer's UDP receive buffer time to drain. Recommended on
	// Wi-Fi / power-line / less-capable receivers; unnecessary on a
	// dedicated wired link to the MiSTer.
	//
	// Read under the same mu that serializes SendPayload, so changes
	// take effect on the next field.
	paceInterval time.Duration
}

// InitACKTimeoutError reports that the MiSTer never acknowledged the INIT
// handshake before the caller's deadline elapsed.
type InitACKTimeoutError struct {
	Timeout time.Duration
	Err     error
}

func (e *InitACKTimeoutError) Error() string {
	if e == nil {
		return "INIT ack timeout"
	}
	return fmt.Sprintf("INIT ack timeout after %s: %v", e.Timeout, e.Err)
}

func (e *InitACKTimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsInitACKTimeout reports whether err wraps an InitACKTimeoutError.
func IsInitACKTimeout(err error) bool {
	var target *InitACKTimeoutError
	return errors.As(err, &target)
}

// NewSender binds a UDP4 socket on srcPort (0 = OS-assigned ephemeral) and
// targets dstHost:dstPort for every Write. Returns the bound Sender or a
// wrapping error. The source port is intentionally exclusive: the MiSTer keys
// a session by sender IP:port, so sharing it with another process can route
// ACKs away from this socket.
func NewSender(dstHost string, dstPort, srcPort int) (*Sender, error) {
	dst, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", dstHost, dstPort))
	if err != nil {
		return nil, err
	}
	lc := &net.ListenConfig{Control: controlSocket}
	addr := fmt.Sprintf(":%d", srcPort)
	pc, err := lc.ListenPacket(nil, "udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("bind source %d: %w", srcPort, err)
	}
	conn := pc.(*net.UDPConn)

	if err := conn.SetWriteBuffer(wantSndBuf); err != nil {
		slog.Warn("SetWriteBuffer failed", "err", err)
	}
	_ = conn.SetReadBuffer(256 * 1024)

	// Linux kernels report 2× the requested SO_SNDBUF for kernel-bookkeeping
	// reasons; this doubling is a long-standing quirk, not a stable contract.
	// Treat the readback as advisory: warn if it's below the requested size
	// (kernel clamped against net.core.wmem_max), info-log the value
	// unconditionally for postmortem debugging.
	actual, rerr := readSndBuf(conn)
	switch {
	case rerr != nil:
		slog.Debug("SO_SNDBUF readback failed", "err", rerr)
	case actual == 0:
		// unsupported platform — silent
	case actual < wantSndBuf:
		slog.Warn("kernel clamped SO_SNDBUF below 2 MB; expect ENOBUFS on busy fields. Run: sudo sysctl -w net.core.wmem_max=4194304",
			"requested", wantSndBuf, "kernel_actual", actual)
	default:
		slog.Info("SO_SNDBUF readback", "requested", wantSndBuf, "kernel_actual", actual,
			"note", "Linux returns ~2× requested as a kernel-bookkeeping quirk")
	}

	actualPort := conn.LocalAddr().(*net.UDPAddr).Port
	return &Sender{
		conn:         conn,
		dstAddr:      dst,
		srcPort:      actualPort,
		sndBufActual: actual,
		// Default to 20 µs per chunk. That keeps a full 518 KB RAW field
		// below gigabit line rate while leaving several milliseconds of
		// NTSC field budget for syscalls and compression. Override via
		// SetPacingInterval (GROOVY_PACING_US env var in main.go) — set 0
		// to disable pacing on a dedicated link if profiling shows it's
		// unnecessary.
		paceInterval: 20 * time.Microsecond,
	}, nil
}

// SourcePort returns the actual bound source port (resolved after bind even
// when srcPort=0 was requested).
func (s *Sender) SourcePort() int { return s.srcPort }

// Conn exposes the underlying UDPConn for co-located components (Drainer).
// Cross-package access beyond groovynet is not supported.
func (s *Sender) Conn() *net.UDPConn { return s.conn }

// Send writes a single packet (typically a command header like INIT,
// SWITCHRES, CLOSE, BLIT_FIELD_VSYNC, or AUDIO header). Does not enter the
// congestion window.
func (s *Sender) Send(pkt []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.conn.WriteToUDP(pkt, s.dstAddr)
	return err
}

// SendPayload slices large payloads into MTU-sized datagrams
// (groovy.MaxDatagram = 1472). Used for BLIT field bytes and AUDIO PCM,
// which stream as a pure byte sequence on the same socket with no
// per-chunk framing.
//
// On ENOBUFS (kernel send queue full): increments enobufCount, logs at
// power-of-10 milestones, and returns the error. No retry — the field
// is torn; the caller (sendField) logs and the next field will succeed
// once the kernel queue drains. Per-chunk retries would just delay the
// next field while the queue drains, costing tick budget.
func (s *Sender) SendPayload(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	totalChunks := (len(payload) + groovy.MaxDatagram - 1) / groovy.MaxDatagram
	chunkIdx := 0
	pace := s.paceInterval
	sendStart := time.Now()
	s.payloadSendStart = sendStart
	for i := 0; i < len(payload); i += groovy.MaxDatagram {
		end := i + groovy.MaxDatagram
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := s.conn.WriteToUDP(payload[i:end], s.dstAddr); err != nil {
			// Platform-specific matcher: Winsock reports a full send queue
			// as WSAENOBUFS, which Go's generic syscall.ENOBUFS never
			// matches on Windows (see sender_windows.go).
			if isSendBufferFull(err) {
				n := s.enobufCount.Add(1)
				if n == 1 || isPowerOfTen(n) {
					slog.Warn("send buffer overflow (ENOBUFS); torn field — aborting remaining chunks",
						"total_events", n,
						"chunk_index", chunkIdx,
						"total_chunks", totalChunks,
						"bytes_sent", i,
						"bytes_total", len(payload),
						"sndbuf_actual", s.sndBufActual)
				}
			}
			return err
		}
		chunkIdx++
		// Per-chunk pacing via busy-wait against an absolute cumulative
		// deadline. Linux's nanosleep clamps sub-ms sleeps to ~100-200 µs
		// (kernel HZ + hrtimer slack), so time.Sleep(paceInterval) for
		// values under ~500 µs over-sleeps by 10-20×, blowing the tick
		// budget. Busy-waiting against a cumulative wall-clock target
		// achieves true microsecond precision and self-corrects: if one
		// chunk's syscall took longer than the per-chunk slice, the next
		// chunk's deadline is already past and we skip the wait entirely.
		// Cost: a few ms of spin CPU per field — acceptable on any modern
		// host, and orders of magnitude cheaper than the 50+ ms over-sleep
		// the naive approach incurs.
		if pace > 0 && i+groovy.MaxDatagram < len(payload) {
			deadline := sendStart.Add(time.Duration(chunkIdx) * pace)
			for time.Now().Before(deadline) {
				// spin — runtime.Gosched() would yield to the scheduler
				// but at sub-ms granularity the scheduler latency itself
				// dominates the sleep. Pure spin is intentional.
			}
		}
	}
	return nil
}

// SetPacingInterval configures the per-chunk delay applied inside
// SendPayload. Pass 0 to disable pacing entirely (default — chunks
// blast back-to-back at line rate). Typical values: 5-20 µs, picked
// empirically based on whether the receiver shows tail-of-field
// corruption.
func (s *Sender) SetPacingInterval(d time.Duration) {
	s.mu.Lock()
	s.paceInterval = d
	s.mu.Unlock()
}

// PacingInterval returns the current per-chunk pacing delay.
func (s *Sender) PacingInterval() time.Duration {
	s.mu.Lock()
	d := s.paceInterval
	s.mu.Unlock()
	return d
}

// ENOBUFCount returns the monotonic count of ENOBUFS events observed since
// the Sender was constructed. Safe to call concurrently. Intended for
// stats endpoints / health checks; the slog throttle alone is insufficient
// signal for chronic problems (logs only fire at 1, 10, 100, ... events).
func (s *Sender) ENOBUFCount() uint64 { return s.enobufCount.Load() }

// isPowerOfTen returns true for 1, 10, 100, 1000, ... and false for 0
// and any other value.
func isPowerOfTen(n uint64) bool {
	if n == 0 {
		return false
	}
	for n >= 10 {
		if n%10 != 0 {
			return false
		}
		n /= 10
	}
	return n == 1
}

// Close tears down the underlying UDP socket. After Close any in-flight
// reader (e.g. the Drainer goroutine) returns with a net.OpError.
func (s *Sender) Close() error { return s.conn.Close() }

// MarkBlitSent records the size and start time of the last BLIT field sent
// so WaitForCongestion can enforce the back-off window. Per reference
// (K_CONGESTION_SIZE=500000, K_CONGESTION_TIME~=11 ms): applies to the
// total payload bytes of the last blit, not the header. The window is
// anchored at the start of the payload send (see payloadSendStart) so it
// overlaps the paced send instead of stacking after it. Callers invoke this
// immediately after the blit's SendPayload, so payloadSendStart is always
// that blit's stamp; the time.Now fallback only covers callers that never
// sent a payload (dup blits pass size=0, which skips the wait anyway).
func (s *Sender) MarkBlitSent(size int) {
	s.mu.Lock()
	s.lastBlitSize = size
	if !s.payloadSendStart.IsZero() {
		s.lastBlitTime = s.payloadSendStart
	} else {
		s.lastBlitTime = time.Now()
	}
	s.mu.Unlock()
}

// WaitForCongestion blocks until the minimum inter-blit interval has elapsed
// if the previous blit exceeded the congestion threshold. Safe to call once
// per tick from the data-plane pump loop; returns immediately when the last
// payload was under groovy.CongestionSize or the wait has already elapsed.
func (s *Sender) WaitForCongestion() {
	s.mu.Lock()
	size := s.lastBlitSize
	last := s.lastBlitTime
	s.mu.Unlock()
	if size <= groovy.CongestionSize {
		return
	}
	elapsed := time.Since(last)
	remaining := time.Duration(groovy.CongestionWait)*time.Millisecond - elapsed
	if remaining > 0 {
		time.Sleep(remaining)
	}
}

// SendInitAwaitACK sends INIT, then blocks up to timeout waiting for the
// 13-byte status reply. Returns the parsed ACK or an error (including the
// timeout case). Callers must NOT have a Drainer goroutine reading the same
// socket at this point — the Drainer is started AFTER the handshake
// succeeds, otherwise it will consume the ACK first.
//
// Reference: groovy_mister.md — "Sender getACK(60) with 60 ms timeout,
// failure = tear down." INIT is the ONE ack-gated handshake; every other
// command is fire-and-forget.
func (s *Sender) SendInitAwaitACK(initPacket []byte, timeout time.Duration) (groovy.ACK, error) {
	if len(initPacket) == 0 || initPacket[0] != groovy.CmdInit {
		return groovy.ACK{}, fmt.Errorf("not an INIT packet")
	}
	if flushed := s.flushStaleDatagrams(); flushed > 0 {
		slog.Debug("flushed stale datagrams before INIT", "count", flushed)
	}
	if err := s.Send(initPacket); err != nil {
		return groovy.ACK{}, err
	}
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return groovy.ACK{}, err
	}
	defer s.conn.SetReadDeadline(time.Time{})
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
}

// flushStaleDatagrams drains any datagrams already queued on the socket.
// The socket is reused across sessions (stable source port) and the previous
// session's drainer stops before the receiver's final ACKs arrive, so stale
// 13-byte ACKs can sit in the receive buffer — and SendInitAwaitACK reads
// the OLDEST datagram, which parses as a valid ACK. Draining first ensures
// the handshake can only observe a reply to THIS INIT (modulo a stale ACK
// still in flight during the ~1 ms window, which the shared-socket design
// cannot exclude). Uses a short absolute deadline: queued datagrams read
// back instantly, and once the deadline passes reads fail immediately, so
// the flush is bounded at ~1 ms even under a continuous inbound stream.
// Callers must not have a Drainer running (same contract as the handshake).
func (s *Sender) flushStaleDatagrams() int {
	buf := make([]byte, 512)
	flushed := 0
	if err := s.conn.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
		return 0
	}
	defer s.conn.SetReadDeadline(time.Time{})
	for flushed < 4096 {
		if _, _, err := s.conn.ReadFromUDP(buf); err != nil {
			break
		}
		flushed++
	}
	return flushed
}

// SendInitAwaitACKWithRetry runs SendInitAwaitACK up to `attempts` times,
// re-sending INIT after each per-attempt timeout. Only timeout errors are
// retried — socket errors fail immediately. INIT is safe to repeat: the
// receiver resets session state on each one, and a late ACK to attempt N
// read by attempt N+1 is still a genuine INIT ACK. The final error keeps the
// InitACKTimeoutError type so callers' IsInitACKTimeout handling still works.
func (s *Sender) SendInitAwaitACKWithRetry(initPacket []byte, timeout time.Duration, attempts int) (groovy.ACK, error) {
	if attempts < 1 {
		attempts = 1
	}
	var ack groovy.ACK
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		ack, err = s.SendInitAwaitACK(initPacket, timeout)
		if err == nil || !IsInitACKTimeout(err) {
			return ack, err
		}
		if attempt < attempts {
			slog.Warn("INIT ACK timeout; retrying",
				"attempt", attempt,
				"max_attempts", attempts,
				"timeout_ms", timeout.Milliseconds())
		}
	}
	return ack, err
}
