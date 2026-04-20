package fakemister

import (
	"net"
	"testing"
	"time"

	"github.com/jedivoodoo/mister-groovy-relay/internal/groovy"
)

func TestParseCommand_Init(t *testing.T) {
	pkt := groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888)
	cmd, err := ParseCommand(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cmd.Type != groovy.CmdInit {
		t.Errorf("Type = %d, want %d", cmd.Type, groovy.CmdInit)
	}
	if cmd.Init == nil {
		t.Fatal("Init payload nil")
	}
	if cmd.Init.RGBMode != groovy.RGBMode888 {
		t.Errorf("RGBMode = %d", cmd.Init.RGBMode)
	}
	if cmd.Init.LZ4Frames != groovy.LZ4ModeDefault {
		t.Errorf("LZ4Frames = %d", cmd.Init.LZ4Frames)
	}
	if cmd.Init.SoundRate != groovy.AudioRate48000 {
		t.Errorf("SoundRate = %d", cmd.Init.SoundRate)
	}
	if cmd.Init.SoundChan != 2 {
		t.Errorf("SoundChan = %d", cmd.Init.SoundChan)
	}
}

func TestParseCommand_UnknownType(t *testing.T) {
	_, err := ParseCommand([]byte{99, 0, 0})
	if err == nil {
		t.Error("expected error")
	}
}

func TestListener_ReceivesInit(t *testing.T) {
	l, err := NewListener(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	events := make(chan Command, 8)
	go l.Run(events)

	// Send an INIT to the listener's port.
	addr := l.Addr()
	conn, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write(groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888))

	select {
	case cmd := <-events:
		if cmd.Type != groovy.CmdInit {
			t.Errorf("got cmd %d", cmd.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for command")
	}
}
