package groovynet

import (
	"log/slog"
	"net"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// Drainer reads ACK packets from the Sender's socket and delivers them on a
// buffered channel. Dropping on a full channel is intentional — ACKs are
// informational and missing a few does not break the session.
//
// Drainer MUST NOT run while SendInitAwaitACK is pending on the same socket.
// Lifecycle: call SendInitAwaitACK first, start the Drainer for steady-state,
// then call Stop() before tearing down the session so the next session's
// SendInitAwaitACK can own the socket uncontested. The Sender's socket stays
// open across sessions (stable source port); Stop signals the Drainer loop
// without closing the socket.
type Drainer struct {
	s      *Sender
	ch     chan<- groovy.ACK
	stopCh chan struct{}
	done   chan struct{}
}

// NewDrainer constructs a Drainer that reads ACKs off s's socket and pushes
// parsed ACKs onto ch (non-blockingly). ch is typically small (cap 1..4):
// the consumer only needs the most recent ACK for congestion / frame timing.
func NewDrainer(s *Sender, ch chan<- groovy.ACK) *Drainer {
	return &Drainer{
		s:      s,
		ch:     ch,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Run loops reading ACKs until Stop() is called or the socket closes. Polls
// with a 50 ms read deadline so Stop takes effect promptly without tearing
// down the shared socket (the sender holds a stable source port across
// session preempts). Malformed or wrong-sized datagrams are dropped silently.
func (d *Drainer) Run() {
	defer close(d.done)
	buf := make([]byte, groovy.ACKPacketSize*2)
	conn := d.s.Conn()
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		if n != groovy.ACKPacketSize {
			continue
		}
		ack, err := groovy.ParseACK(buf[:n])
		if err != nil {
			continue
		}
		select {
		case d.ch <- ack:
		default:
			slog.Debug("ack channel full, dropping")
		}
	}
}

// Stop signals the Run loop to exit and blocks until it has returned. Also
// clears any pending read deadline so subsequent SendInitAwaitACK calls on
// the same socket start clean. Not safe to call twice on the same Drainer —
// typical usage is `defer drainer.Stop()` right after `go drainer.Run()`.
func (d *Drainer) Stop() {
	close(d.stopCh)
	<-d.done
	_ = d.s.Conn().SetReadDeadline(time.Time{})
}
