package groovy

import (
	"encoding/binary"
	"testing"
)

func TestParseACK(t *testing.T) {
	pkt := make([]byte, ACKPacketSize)
	binary.LittleEndian.PutUint32(pkt[0:4], 42)    // frameEcho
	binary.LittleEndian.PutUint16(pkt[4:6], 100)   // vCountEcho
	binary.LittleEndian.PutUint32(pkt[6:10], 50)   // fpga.frame
	binary.LittleEndian.PutUint16(pkt[10:12], 120) // fpga.vCount
	pkt[12] = 0x40                                 // audio-ready bit

	ack, err := ParseACK(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ack.FrameEcho != 42 {
		t.Errorf("FrameEcho = %d", ack.FrameEcho)
	}
	if ack.FPGAFrame != 50 {
		t.Errorf("FPGAFrame = %d", ack.FPGAFrame)
	}
	if !ack.AudioReady() {
		t.Error("AudioReady should be true")
	}
}

func TestParseACK_WrongSize(t *testing.T) {
	_, err := ParseACK(make([]byte, 10))
	if err == nil {
		t.Error("expected error for short packet")
	}
}
