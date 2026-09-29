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
