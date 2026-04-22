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
	"net"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

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
// targets dstHost:dstPort for every Write. SO_REUSEADDR is set via the
// platform-specific controlSocket so a rapid restart does not hit TIME_WAIT.
// Returns the bound Sender or a wrapping error.
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

	// Reference sender sets SO_SNDBUF >= 2 MB to absorb 518 KB field bursts.
	_ = conn.SetWriteBuffer(2 * 1024 * 1024)
	_ = conn.SetReadBuffer(256 * 1024)

	actual := conn.LocalAddr().(*net.UDPAddr).Port
	return &Sender{conn: conn, dstAddr: dst, srcPort: actual}, nil
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
func (s *Sender) SendPayload(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < len(payload); i += groovy.MaxDatagram {
		end := i + groovy.MaxDatagram
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := s.conn.WriteToUDP(payload[i:end], s.dstAddr); err != nil {
			return err
		}
	}
	return nil
}

// Close tears down the underlying UDP socket. After Close any in-flight
// reader (e.g. the Drainer goroutine) returns with a net.OpError.
func (s *Sender) Close() error { return s.conn.Close() }

// MarkBlitSent records the size and time of the last BLIT field sent so
// WaitForCongestion can enforce the back-off window. Per reference
// (K_CONGESTION_SIZE=500000, K_CONGESTION_TIME~=11 ms): applies to the
// total payload bytes of the last blit, not the header.
func (s *Sender) MarkBlitSent(size int) {
	s.mu.Lock()
	s.lastBlitSize = size
	s.lastBlitTime = time.Now()
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
