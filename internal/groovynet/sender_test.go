package groovynet

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestSender_DeliversInit(t *testing.T) {
	l, err := fakemister.NewListener(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	events := make(chan fakemister.Command, 4)
	go l.Run(events)

	addr := l.Addr().(*net.UDPAddr)
	s, err := NewSender("127.0.0.1", addr.Port, 0 /* any source port */)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Send(groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888)); err != nil {
		t.Fatal(err)
	}

	select {
	case c := <-events:
		if c.Type != groovy.CmdInit {
			t.Errorf("got %d", c.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestSender_StableSourcePort(t *testing.T) {
	s, err := NewSender("127.0.0.1", 32100, 32199)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.SourcePort() != 32199 {
		t.Errorf("source port = %d, want 32199", s.SourcePort())
	}
}

func TestSender_InitACKHandshakeSuccess(t *testing.T) {
	l, err := fakemister.NewListener(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// Stub: fake-mister replies with a 13-byte ACK when it sees INIT.
	// We read directly from the Listener's socket (bypassing the Run loop)
	// so the reply path doesn't race the Listener's decoder goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		n, src, err := l.Conn().ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n >= 1 && buf[0] == groovy.CmdInit {
			reply := make([]byte, groovy.ACKPacketSize)
			reply[12] = 1 << 6 // audio-ready
			_, _ = l.Conn().WriteToUDP(reply, src)
		}
	}()

	addr := l.Addr().(*net.UDPAddr)
	s, err := NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ack, err := s.SendInitAwaitACK(
		groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888),
		200*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("INIT ACK: %v", err)
	}
	if !ack.AudioReady() {
		t.Error("expected audio-ready in ACK")
	}
	<-done
}

func TestSender_CongestionBackoff(t *testing.T) {
	s, err := NewSender("127.0.0.1", 12345, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.MarkBlitSent(600 * 1024)
	// Should block for ~11ms on the next call.
	start := time.Now()
	s.WaitForCongestion()
	elapsed := time.Since(start)
	// Windows timer granularity is ~15.6 ms; loosen lower bound to 5ms
	// while still proving a stall actually happened.
	if elapsed < 5*time.Millisecond {
		t.Errorf("congestion wait elapsed=%v, expected >=5ms", elapsed)
	}
}

func TestSender_CongestionNoBackoffUnderThreshold(t *testing.T) {
	s, err := NewSender("127.0.0.1", 12345, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.MarkBlitSent(100 * 1024) // below CongestionSize
	start := time.Now()
	s.WaitForCongestion()
	elapsed := time.Since(start)
	if elapsed > 2*time.Millisecond {
		t.Errorf("congestion wait elapsed=%v for sub-threshold payload, expected ~0", elapsed)
	}
}

func TestSender_InitACKTimeout(t *testing.T) {
	// Bind a local "black hole" socket so the destination port exists but
	// never replies. Using a random bound socket (held open) guarantees the
	// OS won't send ICMP Port Unreachable that would race our timeout.
	bh, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer bh.Close()

	s, err := NewSender("127.0.0.1", bh.LocalAddr().(*net.UDPAddr).Port, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.SendInitAwaitACK(
		groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888),
		60*time.Millisecond,
	)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !IsInitACKTimeout(err) {
		t.Fatalf("expected InitACKTimeoutError, got %T (%v)", err, err)
	}
	var ackErr *InitACKTimeoutError
	if !errors.As(err, &ackErr) {
		t.Fatalf("errors.As failed for InitACKTimeoutError: %v", err)
	}
	if ackErr.Timeout != 60*time.Millisecond {
		t.Errorf("timeout = %v, want %v", ackErr.Timeout, 60*time.Millisecond)
	}
}

func TestSender_SndBufActualPopulated(t *testing.T) {
	s, err := NewSender("127.0.0.1", 32100, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// On Linux/Windows readSndBuf returns the kernel's current SO_SNDBUF.
	// On other-platforms it returns 0. Both are acceptable; we only assert
	// there's no panic and the field is set deterministically.
	if s.sndBufActual < 0 {
		t.Errorf("sndBufActual should be >= 0, got %d", s.sndBufActual)
	}
}
