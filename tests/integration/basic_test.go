//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/jedivoodoo/mister-groovy-relay/internal/groovy"
)

// TestBasic_InitSwitchresClose is the first true end-to-end smoke test:
// it drives the Phase 4 Sender against the Phase 3 fake-mister Listener
// over real UDP loopback and verifies every command lands, in order, with
// the correct count. Uses the unguarded Send (NOT SendInitAwaitACK)
// because the basic fake listener never emits an ACK; the real Plane.Run
// code path will use SendInitAwaitACK in a later phase.
func TestBasic_InitSwitchresClose(t *testing.T) {
	h := NewHarness(t)
	if err := h.Sender.Send(groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888)); err != nil {
		t.Fatal(err)
	}
	if err := h.Sender.Send(groovy.BuildSwitchres(groovy.NTSC480i60)); err != nil {
		t.Fatal(err)
	}
	if err := h.Sender.Send(groovy.BuildClose()); err != nil {
		t.Fatal(err)
	}
	// Give the listener goroutine time to parse and the recorder goroutine
	// time to record. 100 ms on loopback is comfortable.
	time.Sleep(100 * time.Millisecond)

	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] != 1 {
		t.Errorf("init count = %d", snap.Counts[groovy.CmdInit])
	}
	if snap.Counts[groovy.CmdSwitchres] != 1 {
		t.Errorf("switchres count = %d", snap.Counts[groovy.CmdSwitchres])
	}
	if snap.Counts[groovy.CmdClose] != 1 {
		t.Errorf("close count = %d", snap.Counts[groovy.CmdClose])
	}
}
