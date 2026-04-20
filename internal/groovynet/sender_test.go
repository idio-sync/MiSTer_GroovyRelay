package groovynet

import (
	"net"
	"testing"
	"time"

	"github.com/jedivoodoo/mister-groovy-relay/internal/fakemister"
	"github.com/jedivoodoo/mister-groovy-relay/internal/groovy"
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
