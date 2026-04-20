package fakemister

import (
	"bytes"
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

func TestListener_BlitHeaderThenPayload(t *testing.T) {
	l, _ := NewListener(":0")
	defer l.Close()

	cmds := make(chan Command, 8)
	fields := make(chan FieldEvent, 8)
	audios := make(chan AudioEvent, 8)
	go l.RunWithFields(cmds, fields, audios, func() uint32 { return 100 })

	conn, _ := net.Dial("udp", l.Addr().String())
	defer conn.Close()
	hdr := groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 1, Field: 0})
	conn.Write(hdr)
	conn.Write(make([]byte, 50))
	conn.Write(make([]byte, 50))

	select {
	case fe := <-fields:
		if len(fe.Payload) != 100 {
			t.Errorf("payload len = %d, want 100", len(fe.Payload))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestListener_AudioHeaderThenPayload(t *testing.T) {
	l, _ := NewListener(":0")
	defer l.Close()

	cmds := make(chan Command, 8)
	fields := make(chan FieldEvent, 8)
	audios := make(chan AudioEvent, 8)
	go l.RunWithFields(cmds, fields, audios, func() uint32 { return 0 })

	conn, _ := net.Dial("udp", l.Addr().String())
	defer conn.Close()
	conn.Write(groovy.BuildAudioHeader(1000))
	conn.Write(make([]byte, 500))
	conn.Write(make([]byte, 500))

	select {
	case ae := <-audios:
		if len(ae.PCM) != 1000 {
			t.Errorf("audio len = %d, want 1000", len(ae.PCM))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestRunWithFields_ReassemblesAudioPayload(t *testing.T) {
	l, err := NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	cmds := make(chan Command, 8)
	fields := make(chan FieldEvent, 8)
	audios := make(chan AudioEvent, 8)
	fieldSizeFn := func() uint32 { return 720 * 240 * 3 }
	go l.RunWithFields(cmds, fields, audios, fieldSizeFn)

	addr := l.Addr().(*net.UDPAddr)
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send a 3-byte AUDIO header declaring 8 bytes of PCM.
	pcm := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	hdr := groovy.BuildAudioHeader(uint16(len(pcm)))
	if _, err := conn.Write(hdr); err != nil {
		t.Fatal(err)
	}
	// Send the PCM in one datagram (well under MTU).
	if _, err := conn.Write(pcm); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-audios:
		if !bytes.Equal(ev.PCM, pcm) {
			t.Errorf("PCM mismatch: got %x, want %x", ev.PCM, pcm)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for AudioEvent")
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
