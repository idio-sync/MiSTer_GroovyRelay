package groovynet

import (
	"log/slog"

	"github.com/jedivoodoo/mister-groovy-relay/internal/groovy"
)

// Drainer reads ACK packets from the Sender's socket and delivers them on a
// buffered channel. Dropping on a full channel is intentional — ACKs are
// informational and missing a few does not break the session.
//
// Drainer MUST NOT run while SendInitAwaitACK is pending on the same socket.
// Lifecycle: call SendInitAwaitACK first; only then start the Drainer.
// The goroutine exits when the underlying socket is closed (ReadFromUDP
// returns a net.OpError) — no explicit stop signal is needed.
type Drainer struct {
	s  *Sender
	ch chan<- groovy.ACK
}

// NewDrainer constructs a Drainer that reads ACKs off s's socket and pushes
// parsed ACKs onto ch (non-blockingly). ch is typically small (cap 1..4):
// the consumer only needs the most recent ACK for congestion / frame timing.
func NewDrainer(s *Sender, ch chan<- groovy.ACK) *Drainer {
	return &Drainer{s: s, ch: ch}
}

// Run loops forever reading ACKs until the socket is closed. Malformed or
// wrong-sized datagrams are dropped silently (the socket is shared with the
// command path, so stray packets should not kill the drainer).
func (d *Drainer) Run() {
	buf := make([]byte, groovy.ACKPacketSize*2)
	conn := d.s.Conn()
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
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
