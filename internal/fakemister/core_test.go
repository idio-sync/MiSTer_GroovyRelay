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
