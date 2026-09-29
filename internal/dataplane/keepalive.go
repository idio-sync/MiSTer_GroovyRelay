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
// (design §1.2), and during compressed-codec (LZ4/NLC) underrun holds, where holdField sends
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
